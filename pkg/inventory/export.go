package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/dustin/go-humanize"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/numtide/narwal/pkg/config"
	"github.com/parquet-go/parquet-go"
	"golang.org/x/sync/errgroup"
)

const (
	// recordsPerFile defines the maximum records per parquet file.
	recordsPerFile = 1_000_000

	// recordBatchSize defines how many records to accumulate before writing.
	recordBatchSize = 50_000

	// progressInterval defines how often to log progress.
	progressInterval = 100_000

	// numParsers defines the number of concurrent parsing workers.
	numParsers = 16

	// rawChannelSize defines the buffer size for raw data channel.
	rawChannelSize = 1000

	// recordChannelSize defines the buffer size for parsed records channel.
	recordChannelSize = 1000
)

// narinfoBytes represents raw narinfo data from the database.
type rawNarinfo struct {
	idx   int64
	key   []byte
	value []byte
}

// exportStats tracks global export statistics.
type exportStats struct {
	processed     atomic.Int64
	exported      atomic.Int64
	failedToParse atomic.Int64
}

// ExportNarinfos exports all narinfo entries from the badger database to parquet files.
func ExportNarinfos(ctx context.Context, cfg *config.Config, outputDir string) error {
	start := time.Now()

	log.Infof("exporting narinfos to directory: %s", outputDir)

	if cfg.Badger == nil {
		return errors.New("badger config is required")
	}

	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	db, err := OpenDB(cfg.Badger)
	if err != nil {
		return fmt.Errorf("failed to open db: %w", err)
	}

	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			log.Errorf("failed to close db: %s", closeErr)
		}
	}()

	if err = exportNarinfosFromDB(ctx, db, outputDir); err != nil {
		return fmt.Errorf("failed to export narinfos: %w", err)
	}

	log.Infof("finished exporting narinfos in %v", time.Since(start))

	return nil
}

// readNarinfos reads raw narinfos from BadgerDB.
func readNarinfos(
	ctx context.Context,
	db *badger.DB,
	rawChan chan<- rawNarinfo,
	stats *exportStats,
) error {
	// close the raw data channel when we're done
	defer close(rawChan)

	// create a new read-only transaction
	tx := db.NewTransaction(false)
	defer tx.Discard()

	// create an object iterator
	prefix := []byte(BadgerPrefixObject)
	iter := tx.NewIterator(badger.IteratorOptions{
		Prefix:         prefix,
		PrefetchSize:   rawChannelSize * 10,
		PrefetchValues: true,
	})

	defer iter.Close()

	var idx int64

	for iter.Rewind(); iter.ValidForPrefix(prefix); iter.Next() {
		select {
		case <-ctx.Done():
			// exit early if context is cancelled
			return nil
		default:
			item := iter.Item()

			// increment the total count of processed objects
			stats.processed.Add(1)

			// skip anything that isn't a narinfo
			// we shouldn't be storing anything other than narinfo, but we do this check anyway for completeness
			if !bytes.HasSuffix(item.Key(), []byte(".narinfo")) {
				iter.Next()
				continue
			}

			// copy the value to a new slice
			val, err := item.ValueCopy(nil)
			if err != nil {
				return fmt.Errorf("failed to read value for %s: %w", item.Key(), err)
			}

			// add to the channel for parsing
			rawChan <- rawNarinfo{
				idx:   idx,
				key:   item.Key(),
				value: val,
			}

			idx++
		}
	}

	return nil
}

// parseNarinfos parses raw narinfo data into records.
func parseNarinfos(
	ctx context.Context,
	rawChan <-chan rawNarinfo,
	recordChan chan<- NarInfoRecord,
	stats *exportStats,
) {
	for {
		select {
		case <-ctx.Done():
			// exit early if context is cancelled
			return
		case raw, ok := <-rawChan:
			if !ok {
				return // Channel closed, we're done
			}

			// parse the narinfo
			info, err := parseNarinfo(raw.value)
			if err != nil {
				log.Debugf("failed to parse narinfo %s: %s", raw.key, err)
				stats.failedToParse.Add(1)

				continue
			}

			// add to the channel for writing
			record := convertToRecord(info)

			record.Idx = raw.idx

			recordChan <- record
		}
	}
}

// writeNarinfosToParquet batches and writes records to parquet files.
func writeNarinfosToParquet(
	ctx context.Context,
	recordChan <-chan NarInfoRecord,
	outputDir string,
	stats *exportStats,
) error {
	writer, err := newPipelineWriter(outputDir)
	if err != nil {
		return fmt.Errorf("failed to create writer: %w", err)
	}

	defer func() {
		if err := writer.close(); err != nil {
			log.Errorf("failed to close writer: %s", err)
		}
	}()

	batch := make([]NarInfoRecord, 0, recordBatchSize)

	writeBatch := func() error {
		// write the batch
		if err := writer.writeBatch(batch); err != nil {
			return fmt.Errorf("failed to write batch: %w", err)
		}

		// update stats
		exported := stats.exported.Add(int64(len(batch)))

		// Log progress
		if exported%progressInterval == 0 {
			log.Infof("exported %s narinfos (file #%d, %s records in current file)",
				humanize.Comma(exported),
				writer.fileNum,
				humanize.Comma(writer.recordsInFile),
			)
		}

		// reset the batch
		batch = batch[:0]

		return nil
	}

	tryRotateFile := func() error {
		// check if we need to rotate files and abort early if not
		if writer.recordsInFile < recordsPerFile {
			return nil
		}

		// rotate files
		log.Infof("record limit reached (%s records), rotating to new file",
			humanize.Comma(writer.recordsInFile))

		if err = writer.newFile(); err != nil {
			return fmt.Errorf("failed to rotate file: %w", err)
		}

		return nil
	}

LOOP:
	for {
		select {
		case <-ctx.Done():
			break LOOP

		case record, ok := <-recordChan:
			if !ok {
				// Channel closed, write any remaining records
				break LOOP
			}

			batch = append(batch, record)

			// write batch when it reaches the target size
			if len(batch) < recordBatchSize {
				continue LOOP
			}

			if err = writeBatch(); err != nil {
				return fmt.Errorf("failed to write batch: %w", err)
			}

			if err = tryRotateFile(); err != nil {
				return fmt.Errorf("failed to rotate file: %w", err)
			}
		}
	}

	// flush any remaining records
	if err = writeBatch(); err != nil {
		return fmt.Errorf("failed to write batch: %w", err)
	}

	return nil
}

// pipelineWriter manages writing records to parquet files.
type pipelineWriter struct {
	outputDir     string
	file          *os.File
	fileNum       int
	writer        *parquet.GenericWriter[NarInfoRecord]
	recordsInFile int64
	totalExported int64
}

// newPipelineWriter creates a new pipeline writer.
func newPipelineWriter(outputDir string) (*pipelineWriter, error) {
	w := &pipelineWriter{
		outputDir: outputDir,
		fileNum:   1,
	}

	if err := w.createFile(); err != nil {
		return nil, err
	}

	return w, nil
}

// createFile creates a new parquet file.
func (w *pipelineWriter) createFile() error {
	filename := fmt.Sprintf("narinfos_%04d.parquet", w.fileNum)
	filePath := filepath.Join(w.outputDir, filename)

	log.Infof("creating new parquet file: %s", filename)

	file, err := os.Create(filePath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}

	w.file = file
	w.writer = parquet.NewGenericWriter[NarInfoRecord](file)

	return nil
}

// newFile rotates to a new file.
func (w *pipelineWriter) newFile() error {
	if err := w.closeCurrentFile(); err != nil {
		return err
	}

	w.fileNum++
	w.recordsInFile = 0

	return w.createFile()
}

// closeCurrentFile closes the current writer and file.
func (w *pipelineWriter) closeCurrentFile() error {
	if w.writer != nil {
		if err := w.writer.Close(); err != nil {
			return fmt.Errorf("failed to close parquet writer: %w", err)
		}

		w.writer = nil
	}

	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return fmt.Errorf("failed to close output file: %w", err)
		}

		w.file = nil
	}

	return nil
}

// writeBatch writes a batch of records to the current file.
func (w *pipelineWriter) writeBatch(batch []NarInfoRecord) error {
	if len(batch) == 0 {
		return nil
	}

	// Sort the batch by Idx to ensure records are written in order
	sort.Slice(batch, func(i, j int) bool {
		return batch[i].Idx < batch[j].Idx
	})

	if _, err := w.writer.Write(batch); err != nil {
		return fmt.Errorf("failed to write batch: %w", err)
	}

	w.recordsInFile += int64(len(batch))
	w.totalExported += int64(len(batch))

	return nil
}

// close closes the writer and logs summary.
func (w *pipelineWriter) close() error {
	if err := w.closeCurrentFile(); err != nil {
		return err
	}

	w.logSummary()

	return nil
}

// logSummary logs the final export summary.
func (w *pipelineWriter) logSummary() {
	// calculate total size of all parquet files
	var totalSize int64

	for i := 1; i <= w.fileNum; i++ {
		filename := fmt.Sprintf("narinfos_%04d.parquet", i)
		filePath := filepath.Join(w.outputDir, filename)

		if stat, err := os.Stat(filePath); err == nil {
			totalSize += stat.Size()
		}
	}

	log.Infof(
		"writer summary: %d file(s), %s total records, total size: %s",
		w.fileNum,
		humanize.Comma(w.totalExported),
		humanize.Bytes(uint64(totalSize)), //nolint:gosec
	)
}

// parseNarinfo parses a narinfo from badger value.
func parseNarinfo(val []byte) (*narinfo.NarInfo, error) {
	info, err := narinfo.Parse(bytes.NewReader(val))
	if err != nil {
		return nil, fmt.Errorf("parse narinfo: %w", err)
	}

	return info, nil
}

// convertToRecord converts a NarInfo to a NarInfoRecord.
func convertToRecord(info *narinfo.NarInfo) NarInfoRecord {
	signatures := make([]string, 0, len(info.Signatures))
	for _, sig := range info.Signatures {
		signatures = append(signatures, sig.String())
	}

	record := NarInfoRecord{
		StorePath:   info.StorePath,
		URL:         info.URL,
		Compression: info.Compression,
		FileSize:    info.FileSize,
		NarSize:     info.NarSize,
		References:  info.References,
		Deriver:     info.Deriver,
		System:      info.System,
		CA:          info.CA,
		Signatures:  signatures,
	}

	if info.FileHash != nil {
		record.FileHash = info.FileHash.String()
	}

	if info.NarHash != nil {
		record.NarHash = info.NarHash.String()
	}

	return record
}

// exportNarinfosFromDB orchestrates the pipeline export process.
func exportNarinfosFromDB(
	ctx context.Context,
	db *badger.DB,
	outputDir string,
) error {
	// create a global stats tracker
	stats := &exportStats{}

	// create channels for the pipeline
	rawDataChan := make(chan rawNarinfo, rawChannelSize)
	recordChan := make(chan NarInfoRecord, recordChannelSize)

	// create errgroup for managing goroutines
	eg, ctx := errgroup.WithContext(ctx)

	// stage 1: read narinfos from the db
	eg.Go(func() error {
		return readNarinfos(ctx, db, rawDataChan, stats)
	})

	// stage 2: parse narinfos concurrently
	parserWg := sync.WaitGroup{}
	parserWg.Add(numParsers)

	for range numParsers {
		eg.Go(func() error {
			defer parserWg.Done()

			parseNarinfos(ctx, rawDataChan, recordChan, stats)

			return nil
		})
	}

	// close recordChan when all parsers are done
	eg.Go(func() error {
		parserWg.Wait()
		close(recordChan)

		return nil
	})

	// stage 3: write records to parquet files
	eg.Go(func() error {
		return writeNarinfosToParquet(ctx, recordChan, outputDir, stats)
	})

	// wait for everything to finish
	if err := eg.Wait(); err != nil {
		return fmt.Errorf("pipeline failed: %w", err)
	}

	// log final statistics
	log.Infof("=== FINAL EXPORT STATISTICS ===")
	log.Infof("Total objects processed: %s", humanize.Comma(stats.processed.Load()))
	log.Infof("Total narinfos exported: %s", humanize.Comma(stats.exported.Load()))

	parseFailures := stats.failedToParse.Load()
	log.Infof("Total parse failures: %s", humanize.Comma(parseFailures))

	if parseFailures > 0 {
		return fmt.Errorf("there were %d parse failures", parseFailures)
	}

	return nil
}
