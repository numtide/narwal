package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/dustin/go-humanize"
	"github.com/numtide/narwal/pkg/config"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

const (
	// progressInterval defines how often to log progress (every N records).
	progressInterval = 50_000

	// maxRowsPerRowGroup controls row group size for dictionary encoding efficiency.
	//
	// Typically, we would want 500k or 1 million rows per row group to reduce the memory overhead and improve lookup
	// speed. However, in our case we are also trying to maximise the dictionary encoding efficiency for the references
	// column to reduce the size of the exported file. Experimentally, this number was found as a good compromise.
	maxRowsPerRowGroup = 30_000_000

	// iteratorBatchSize is the number of records to fetch before parallel parsing.
	iteratorBatchSize = 10_000

	// parseWorkerCount is the number of parsing goroutines.
	parseWorkerCount = 16

	// prefetchSize is the badger iterator prefetch buffer size.
	prefetchSize = 128
)

// exportStats tracks global export statistics and timing.
type exportStats struct {
	processed     atomic.Int64
	exported      atomic.Int64
	failedToParse atomic.Int64

	// Timing stats (nanoseconds)
	parseTime atomic.Int64
	writeTime atomic.Int64
}

// kvPair holds a copied key-value pair from the iterator.
type kvPair struct {
	key   []byte
	value []byte
}

// parseResult holds the result of parsing a narinfo with its original index.
type parseResult struct {
	index  int
	record NarInfoRecord
	err    error
}

// iteratorExporter handles ordered export using badger iterator.
type iteratorExporter struct {
	db     *badger.DB
	writer *parquet.GenericWriter[NarInfoRecord]
	stats  *exportStats

	// Memory management
	rowsInGroup   int64
	rowGroupCount int
}

// ExportNarinfos exports all narinfo entries from the badger database to a single parquet file.
func ExportNarinfos(ctx context.Context, cfg *config.Config, outputPath string) error {
	start := time.Now()

	log.Infof("exporting narinfos to: %s", outputPath)

	if cfg.Badger == nil {
		return errors.New("badger config is required")
	}

	// Open the db and ensure it is closed at the end
	db, err := OpenDB(cfg.Badger, true)
	if err != nil {
		return fmt.Errorf("failed to open db: %w", err)
	}

	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Errorf("failed to close db: %s", closeErr)
		}
	}()

	// Export the narinfos
	if err = exportNarinfosFromDB(ctx, db, outputPath); err != nil {
		return fmt.Errorf("failed to export narinfos: %w", err)
	}

	log.Infof("finished exporting narinfos in %v", time.Since(start))

	return nil
}

// createParquetWriter creates a parquet writer with ZSTD compression.
// Bloom filters are disabled to save space (~0.34 GB); use ClickHouse for indexed lookups.
func createParquetWriter(file *os.File) *parquet.GenericWriter[NarInfoRecord] {
	return parquet.NewGenericWriter[NarInfoRecord](file,
		// Use ZSTD compression to reduce the overall file size
		parquet.Compression(&zstd.Codec{
			Level: zstd.SpeedBetterCompression,
		}),
		// Large row groups improve dictionary encoding efficiency for references.
		// The references column contains many repeated hashes (common dependencies),
		// and larger row groups allow better deduplication within the dictionary.
		parquet.MaxRowsPerRowGroup(maxRowsPerRowGroup),
	)
}

// exportNarinfosFromDB exports narinfos using Badger's Iterator API for ordered output.
// Records are exported in lexicographic order by key (hash order).
func exportNarinfosFromDB(
	ctx context.Context,
	db *badger.DB,
	outputPath string,
) error {
	stats := &exportStats{}

	// Create the output file and close and ensure it is closed at the end
	file, err := os.Create(outputPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}

	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Errorf("failed to close output file: %s", closeErr)
		}
	}()

	// Create the parquet writer
	writer := createParquetWriter(file)

	// Create an exporter instance and export the narinfos
	exporter := &iteratorExporter{
		db:     db,
		writer: writer,
		stats:  stats,
	}

	exportErr := exporter.export(ctx)

	if errors.Is(exportErr, context.Canceled) {
		log.Info("export cancelled, finishing up...")
	} else if exportErr != nil {
		return fmt.Errorf("export: %w", exportErr)
	}

	// Explicitly close the writer before logging stats to ensure parquet footer is written.
	// This is important for graceful cancellation - we want valid output even if interrupted.
	// Note: parquet-go can panic during Close() if cancelled mid-write due to internal buffer
	// state issues. We recover from this to allow graceful shutdown.
	if closeErr := closeParquetWriter(writer); closeErr != nil {
		log.Warnf("failed to close parquet writer cleanly: %s", closeErr)
		// Don't return error - the partial file may still be valid
	}

	logExportStats(stats, file)

	return nil
}

// export iterates through narinfos in key order and exports them.
func (e *iteratorExporter) export(ctx context.Context) error {
	prefix := []byte(BadgerPrefixObject)
	suffix := []byte(".narinfo")

	err := e.db.View(func(txn *badger.Txn) error {
		opts := badger.IteratorOptions{
			Prefix:         prefix,
			PrefetchValues: true, // Need values for parsing
			PrefetchSize:   prefetchSize,
			Reverse:        false, // Forward = lexicographic order
			AllVersions:    false,
		}

		iter := txn.NewIterator(opts)
		defer iter.Close()

		batch := make([]kvPair, 0, iteratorBatchSize)

		for iter.Rewind(); iter.Valid(); iter.Next() {
			// Check cancellation periodically
			if len(batch)%1000 == 0 {
				if err := ctx.Err(); err != nil {
					return fmt.Errorf("context cancelled: %w", err)
				}
			}

			item := iter.Item()

			// Filter for .narinfo files
			if !bytes.HasSuffix(item.Key(), suffix) {
				continue
			}

			// Copy key and value (iterator reuses buffers)
			pair := kvPair{
				key: item.KeyCopy(nil),
			}

			var err error

			pair.value, err = item.ValueCopy(nil)
			if err != nil {
				log.Debugf("failed to get value for %s: %s", item.Key(), err)
				continue
			}

			batch = append(batch, pair)

			// Process batch when full
			if len(batch) >= iteratorBatchSize {
				if err := e.processBatch(ctx, batch); err != nil {
					return err
				}

				batch = batch[:0] // Reset slice, reuse capacity
			}
		}

		// Process remaining records
		if len(batch) > 0 {
			if err := e.processBatch(ctx, batch); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("badger view: %w", err)
	}

	return nil
}

// processBatch parses a batch of KV pairs in parallel and writes them in order.
func (e *iteratorExporter) processBatch(ctx context.Context, batch []kvPair) error {
	if len(batch) == 0 {
		return nil
	}

	// Recover from parquet-go panics during write.
	// This can happen when cancelled mid-flush due to a bug in parquet-go's
	// chunkMemoryBuffer.WriteTo which doesn't check for empty data slice.
	var panicErr error

	defer func() {
		if r := recover(); r != nil {
			panicErr = fmt.Errorf("parquet write panic (likely cancelled mid-flush): %v", r)
		}
	}()

	e.stats.processed.Add(int64(len(batch)))

	// Create channels for work distribution and result collection
	workCh := make(chan int, len(batch))
	resultCh := make(chan parseResult, len(batch))

	// Start worker pool
	var wg sync.WaitGroup

	for range parseWorkerCount {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for idx := range workCh {
				parseStart := time.Now()
				record, err := parseNarinfoToRecord(batch[idx].value)

				e.stats.parseTime.Add(time.Since(parseStart).Nanoseconds())

				resultCh <- parseResult{
					index:  idx,
					record: record,
					err:    err,
				}
			}
		}()
	}

	// Send work
	for i := range batch {
		workCh <- i
	}

	close(workCh)

	// Wait for workers and close results
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results into ordered slice
	results := make([]parseResult, len(batch))
	for result := range resultCh {
		results[result.index] = result
	}

	// Build ordered records slice
	records := make([]NarInfoRecord, 0, len(batch))

	for i, result := range results {
		if result.err != nil {
			log.Debugf("failed to parse narinfo %s: %s", batch[i].key, result.err)
			e.stats.failedToParse.Add(1)

			continue
		}

		records = append(records, result.record)
	}

	if len(records) == 0 {
		return panicErr
	}

	// Check context before writing
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled: %w", err)
	}

	// Write to parquet (sequential, preserves order)
	writeStart := time.Now()

	if _, err := e.writer.Write(records); err != nil {
		return fmt.Errorf("write records: %w", err)
	}

	e.stats.writeTime.Add(time.Since(writeStart).Nanoseconds())

	// Progress and memory management
	e.logProgress(int64(len(records)))
	e.handleRowGroupFlush(int64(len(records)))

	return panicErr
}

// logProgress logs export progress every progressInterval records.
func (e *iteratorExporter) logProgress(count int64) {
	exported := e.stats.exported.Add(count)
	prev := exported - count

	if exported/progressInterval != prev/progressInterval {
		log.Infof("exported %s narinfos", humanize.Comma(exported))
	}
}

// handleRowGroupFlush detects row group flush and releases memory.
func (e *iteratorExporter) handleRowGroupFlush(count int64) {
	e.rowsInGroup += count
	if e.rowsInGroup >= maxRowsPerRowGroup {
		e.rowGroupCount++
		e.rowsInGroup = 0
		// Force GC and return memory to OS after each row group flush
		// to prevent unbounded memory growth with large row groups
		debug.FreeOSMemory()
		log.Infof("row group %d flushed, released memory to OS", e.rowGroupCount)
	}
}

// closeParquetWriter closes the parquet writer, recovering from any panics.
// parquet-go can panic during Close() if cancelled mid-write due to internal buffer issues.
func closeParquetWriter(writer *parquet.GenericWriter[NarInfoRecord]) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("parquet writer panic during close: %v", r)
		}
	}()

	if err := writer.Close(); err != nil {
		return fmt.Errorf("close parquet writer: %w", err)
	}

	return nil
}

// logExportStats logs the final export statistics.
func logExportStats(stats *exportStats, file *os.File) {
	log.Infof("=== FINAL EXPORT STATISTICS ===")
	log.Infof("Total objects processed: %s", humanize.Comma(stats.processed.Load()))
	log.Infof("Total narinfos exported: %s", humanize.Comma(stats.exported.Load()))
	log.Infof("Total parse failures: %s", humanize.Comma(stats.failedToParse.Load()))

	if stat, err := file.Stat(); err == nil {
		log.Infof("Output file size: %s", humanize.Bytes(uint64(stat.Size()))) //nolint:gosec
	}

	// Log timing breakdown
	parseNs := stats.parseTime.Load()
	writeNs := stats.writeTime.Load()
	totalNs := parseNs + writeNs

	if totalNs > 0 {
		log.Infof("=== TIMING BREAKDOWN ===")
		log.Infof("Parse narinfo:  %v (%.1f%%)",
			time.Duration(parseNs),
			float64(parseNs)/float64(totalNs)*100)
		log.Infof("Write parquet:  %v (%.1f%%)",
			time.Duration(writeNs),
			float64(writeNs)/float64(totalNs)*100)
		log.Infof("Total measured: %v", time.Duration(totalNs))
	}
}
