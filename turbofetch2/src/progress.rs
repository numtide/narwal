use indicatif::{ProgressBar, ProgressStyle};
use std::sync::atomic::{AtomicU64, AtomicUsize, Ordering};
use std::sync::Arc;
use std::time::Instant;
use tokio::sync::RwLock;

// Constants for nixbase32 hash format
const HASH_LENGTH: usize = 32;
const AVERAGE_LINE_LENGTH: usize = HASH_LENGTH + 1; // hash + newline

#[derive(Clone)]
pub struct ProgressTracker {
    overall_bar: ProgressBar,
    stats: Arc<ProgressStats>,
    start_time: Instant,
}

pub struct ProgressStats {
    pub total_items: AtomicUsize,
    pub skipped: AtomicUsize,
    pub downloaded: AtomicUsize,
    pub in_progress: AtomicUsize,
    pub bytes_downloaded: AtomicU64,
    pub last_update_time: RwLock<Instant>,
    pub last_downloaded_count: AtomicUsize,
    pub last_message_update: RwLock<Instant>,
}

impl Default for ProgressStats {
    fn default() -> Self {
        Self {
            total_items: AtomicUsize::new(0),
            skipped: AtomicUsize::new(0),
            downloaded: AtomicUsize::new(0),
            in_progress: AtomicUsize::new(0),
            bytes_downloaded: AtomicU64::new(0),
            last_update_time: RwLock::new(Instant::now()),
            last_downloaded_count: AtomicUsize::new(0),
            last_message_update: RwLock::new(Instant::now()),
        }
    }
}

impl ProgressTracker {
    pub fn new(file_size: u64) -> Self {
        // Estimate total items based on file size
        let estimated_total = (file_size as usize / AVERAGE_LINE_LENGTH).max(1);

        // Create overall progress bar
        let overall_bar = ProgressBar::new(estimated_total as u64);
        // Use stderr and update at 10Hz, with proper terminal handling
        overall_bar.set_draw_target(indicatif::ProgressDrawTarget::stderr_with_hz(10));
        overall_bar.set_style(
            ProgressStyle::default_bar()
                .template("[{elapsed_precise}] {bar:20.cyan/blue} {percent:>3}% {pos:>7}/{len:7} ETA: {eta_precise} | {msg}")
                .unwrap()
                .progress_chars("█▉▊▋▌▍▎▏  "),
        );
        overall_bar.set_message("Download: 0 | Skip: 0 | In Flight: 0 | 0.0 MB | 0.0/s");
        overall_bar.enable_steady_tick(std::time::Duration::from_millis(100));

        let stats = Arc::new(ProgressStats {
            total_items: AtomicUsize::new(estimated_total),
            last_update_time: RwLock::new(Instant::now()),
            ..Default::default()
        });

        Self {
            overall_bar,
            stats,
            start_time: Instant::now(),
        }
    }

    pub fn update_total_items(&self, total: usize) {
        self.stats.total_items.store(total, Ordering::Relaxed);
        self.overall_bar.set_length(total as u64);
    }

    pub fn increment_skipped(&self, count: usize) {
        self.stats.skipped.fetch_add(count, Ordering::Relaxed);
        self.update_overall_progress();

        // Rate-limit message updates to every 100ms during reading phase
        if let Ok(last_update) = self.stats.last_message_update.try_read() {
            if last_update.elapsed() < std::time::Duration::from_millis(100) {
                return;
            }
        }

        // Update message to show current stats with current rate
        if let Ok(mut last_update) = self.stats.last_message_update.try_write() {
            *last_update = Instant::now();
            let elapsed = self.start_time.elapsed().as_secs_f64();
            let current_downloaded = self.stats.downloaded.load(Ordering::Relaxed);
            let rate = if elapsed > 0.0 {
                current_downloaded as f64 / elapsed
            } else {
                0.0
            };
            self.update_overall_message(rate);
        }
    }

    pub fn increment_downloaded(&self, count: usize) {
        self.stats.downloaded.fetch_add(count, Ordering::Relaxed);
        self.update_overall_progress();
    }

    pub fn add_bytes_downloaded(&self, bytes: u64) {
        self.stats
            .bytes_downloaded
            .fetch_add(bytes, Ordering::Relaxed);
    }

    pub fn start_batch(&self, _worker_id: usize, _batch_id: usize, batch_size: usize) {
        self.stats
            .in_progress
            .fetch_add(batch_size, Ordering::Relaxed);
    }

    pub async fn complete_batch(
        &self,
        _worker_id: usize,
        _batch_id: usize,
        batch_size: usize,
        downloaded: usize,
    ) {
        self.stats
            .in_progress
            .fetch_sub(batch_size, Ordering::Relaxed);
        self.stats
            .downloaded
            .fetch_add(downloaded, Ordering::Relaxed);
        self.stats
            .skipped
            .fetch_add(batch_size - downloaded, Ordering::Relaxed);

        // Update download rate after each batch
        self.update_download_rate().await;
        self.update_overall_progress();
    }

    async fn update_download_rate(&self) {
        let now = Instant::now();
        let mut last_update = self.stats.last_update_time.write().await;

        let elapsed = now.duration_since(*last_update).as_secs_f64();
        if elapsed >= 1.0 {
            let current_downloaded = self.stats.downloaded.load(Ordering::Relaxed);
            let last_downloaded = self.stats.last_downloaded_count.load(Ordering::Relaxed);
            let downloads_delta = current_downloaded - last_downloaded;

            let rate = downloads_delta as f64 / elapsed;

            self.stats
                .last_downloaded_count
                .store(current_downloaded, Ordering::Relaxed);
            *last_update = now;

            // Update the overall bar message with current stats
            self.update_overall_message(rate);
        }
    }

    fn update_overall_progress(&self) {
        let skipped = self.stats.skipped.load(Ordering::Relaxed);
        let downloaded = self.stats.downloaded.load(Ordering::Relaxed);
        let completed = skipped + downloaded;

        self.overall_bar.set_position(completed as u64);
    }

    fn update_overall_message(&self, rate: f64) {
        let skipped = self.stats.skipped.load(Ordering::Relaxed);
        let downloaded = self.stats.downloaded.load(Ordering::Relaxed);
        let in_progress = self.stats.in_progress.load(Ordering::Relaxed);
        let bytes = self.stats.bytes_downloaded.load(Ordering::Relaxed);

        let bytes_mb = bytes as f64 / 1_048_576.0;

        let msg = format!(
            "Download: {} | Skip: {} | In Flight: {} | {:.1} MB | {:.1}/s",
            downloaded, skipped, in_progress, bytes_mb, rate
        );

        self.overall_bar.set_message(msg);
    }

    pub fn finish(&self) {
        let elapsed = self.start_time.elapsed();
        let total_downloaded = self.stats.downloaded.load(Ordering::Relaxed);
        let total_skipped = self.stats.skipped.load(Ordering::Relaxed);
        let total_bytes = self.stats.bytes_downloaded.load(Ordering::Relaxed);

        let bytes_mb = total_bytes as f64 / 1_048_576.0;
        let rate = total_downloaded as f64 / elapsed.as_secs_f64();

        self.overall_bar.finish_with_message(format!(
            "Completed: {} downloaded, {} skipped | {:.1} MB | {:.1} items/s | Duration: {:?}",
            total_downloaded, total_skipped, bytes_mb, rate, elapsed
        ));
    }
}
