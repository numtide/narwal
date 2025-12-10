package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dgraph-io/badger/v4"
	"github.com/dgraph-io/badger/v4/pb"
	"github.com/dgraph-io/ristretto/v2/z"
	"github.com/dustin/go-humanize"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/nixbase32"
	"github.com/numtide/narwal/pkg/config"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

const (
	// progressInterval defines how often to log progress (every N records).
	progressInterval = 50_000

	// maxRowsPerRowGroup limits memory by flushing to disk periodically.
	// With 250M records, this creates ~250 row groups.
	maxRowsPerRowGroup = 500_000

	// numStreamWorkers defines the number of concurrent stream workers.
	numStreamWorkers = 16
)

// storePathRegex matches /nix/store/<32-char-hash>-<name>.
var storePathRegex = regexp.MustCompile(`^/nix/store/([a-z0-9]{32})-(.+)$`)

// parseStorePath extracts hash (as decoded bytes) and pname from a Nix store path.
func parseStorePath(storePath string) ([]byte, string, error) {
	matches := storePathRegex.FindStringSubmatch(storePath)
	if len(matches) != 3 {
		return nil, "", fmt.Errorf("invalid store path: %s", storePath)
	}

	// Decode the nixbase32 hash to bytes (32 chars -> 20 bytes)
	hash, err := nixbase32.DecodeString(matches[1])
	if err != nil {
		return nil, "", fmt.Errorf("decode hash: %w", err)
	}

	return hash, matches[2], nil
}

// exportStats tracks global export statistics.
type exportStats struct {
	processed     atomic.Int64
	exported      atomic.Int64
	failedToParse atomic.Int64
}

// ExportNarinfos exports all narinfo entries from the badger database to a single parquet file.
func ExportNarinfos(ctx context.Context, cfg *config.Config, outputPath string) error {
	start := time.Now()

	log.Infof("exporting narinfos to: %s", outputPath)

	if cfg.Badger == nil {
		return errors.New("badger config is required")
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

	if err = exportNarinfosFromDB(ctx, db, outputPath); err != nil {
		return fmt.Errorf("failed to export narinfos: %w", err)
	}

	log.Infof("finished exporting narinfos in %v", time.Since(start))

	return nil
}

// createParquetWriter creates a parquet writer with ZSTD compression and bloom filter on hash column.
func createParquetWriter(file *os.File) *parquet.GenericWriter[NarInfoRecord] {
	return parquet.NewGenericWriter[NarInfoRecord](file,
		// Use ZSTD compression for smaller file size
		parquet.Compression(&zstd.Codec{
			Level: zstd.SpeedBetterCompression,
		}),
		// Enable bloom filter on hash column for fast lookups
		parquet.BloomFilters(
			parquet.SplitBlockFilter(10, "hash"),
		),
		// Limit memory usage: flush to disk every 1M records
		parquet.MaxRowsPerRowGroup(maxRowsPerRowGroup),
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
func convertToRecord(info *narinfo.NarInfo) (NarInfoRecord, error) {
	hash, pname, err := parseStorePath(info.StorePath)
	if err != nil {
		return NarInfoRecord{}, err
	}

	signatures := make([]string, 0, len(info.Signatures))
	for _, sig := range info.Signatures {
		signatures = append(signatures, sig.String())
	}

	// Parse references - extract hash and pname from each (format: hash32chars-pname)
	referenceHashes := make([][]byte, 0, len(info.References))
	referencePnames := make([]string, 0, len(info.References))

	for _, ref := range info.References {
		if len(ref) < 34 { // 32 chars hash + "-" + at least 1 char pname
			continue // skip invalid references
		}

		hashBytes, err := nixbase32.DecodeString(ref[:32])
		if err != nil {
			continue // skip references with invalid hashes
		}

		referenceHashes = append(referenceHashes, hashBytes)
		referencePnames = append(referencePnames, ref[33:]) // skip "hash-"
	}

	record := NarInfoRecord{
		Hash:            hash,
		Pname:           pname,
		URL:             info.URL,
		Compression:     info.Compression,
		FileSize:        info.FileSize,
		NarSize:         info.NarSize,
		ReferenceHashes: referenceHashes,
		ReferencePnames: referencePnames,
		Deriver:         info.Deriver,
		System:          info.System,
		CA:              info.CA,
		Signatures:      signatures,
	}

	if info.FileHash != nil {
		record.FileHash = info.FileHash.String()
	}

	if info.NarHash != nil {
		record.NarHash = info.NarHash.String()
	}

	return record, nil
}

// streamHandler handles the Send callback for the badger stream.
type streamHandler struct {
	writer *parquet.GenericWriter[NarInfoRecord]
	stats  *exportStats
}

// handleBatch processes a batch of KV pairs from the stream.
func (h *streamHandler) handleBatch(ctx context.Context, buf *z.Buffer) error {
	// Check if context is cancelled before processing
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled: %w", err)
	}

	list, err := badger.BufferToKVList(buf)
	if err != nil {
		return fmt.Errorf("buffer to KV list: %w", err)
	}

	records := h.parseRecords(list.GetKv())

	if len(records) == 0 {
		return nil
	}

	// Check again before writing in case context was cancelled during processing
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context cancelled: %w", err)
	}

	if _, err := h.writer.Write(records); err != nil {
		return fmt.Errorf("write records: %w", err)
	}

	h.logProgress(int64(len(records)))

	return nil
}

// parseRecords converts KV pairs to NarInfoRecords.
func (h *streamHandler) parseRecords(kvList []*pb.KV) []NarInfoRecord {
	records := make([]NarInfoRecord, 0, len(kvList))

	for _, kv := range kvList {
		if kv.GetStreamDone() {
			continue
		}

		h.stats.processed.Add(1)

		info, err := parseNarinfo(kv.GetValue())
		if err != nil {
			log.Debugf("failed to parse narinfo %s: %s", kv.GetKey(), err)
			h.stats.failedToParse.Add(1)

			continue
		}

		record, err := convertToRecord(info)
		if err != nil {
			log.Debugf("failed to convert narinfo %s: %s", kv.GetKey(), err)
			h.stats.failedToParse.Add(1)

			continue
		}

		records = append(records, record)
	}

	return records
}

// logProgress logs export progress every progressInterval records.
func (h *streamHandler) logProgress(count int64) {
	exported := h.stats.exported.Add(count)
	prev := exported - count

	if exported/progressInterval != prev/progressInterval {
		log.Infof("exported %s narinfos", humanize.Comma(exported))
	}
}

// exportNarinfosFromDB exports narinfos using Badger's Stream API for better performance.
func exportNarinfosFromDB(
	ctx context.Context,
	db *badger.DB,
	outputPath string,
) error {
	stats := &exportStats{}

	file, err := os.Create(outputPath) //nolint:gosec
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}

	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Errorf("failed to close output file: %s", closeErr)
		}
	}()

	writer := createParquetWriter(file)

	defer func() {
		if closeErr := writer.Close(); closeErr != nil {
			log.Errorf("failed to close parquet writer: %s", closeErr)
		}
	}()

	handler := &streamHandler{writer: writer, stats: stats}

	stream := db.NewStream()
	stream.NumGo = numStreamWorkers
	stream.Prefix = []byte(BadgerPrefixObject)
	stream.LogPrefix = "narinfo-export"
	stream.ChooseKey = func(item *badger.Item) bool {
		return bytes.HasSuffix(item.Key(), []byte(".narinfo"))
	}
	stream.Send = func(buf *z.Buffer) error {
		return handler.handleBatch(ctx, buf)
	}

	if err := stream.Orchestrate(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			return fmt.Errorf("stream orchestration: %w", err)
		}

		log.Info("export cancelled, finishing up...")
	}

	logExportStats(stats, file)

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
}
