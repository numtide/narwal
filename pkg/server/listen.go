package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	"github.com/nix-community/go-nix/pkg/narinfo"
	"github.com/nix-community/go-nix/pkg/nixbase32"
	"github.com/numtide/narwal/pkg/awssdk/sqs"
	"github.com/numtide/narwal/pkg/db"
	"golang.org/x/sync/errgroup"
)

//nolint:gocognit
func (s *Server) listenToS3(ctx context.Context) error {
	// Create an errgroup to process events concurrently
	eg := errgroup.Group{}
	// todo how to size?
	eg.SetLimit(8)

LOOP:
	for {
		select {
		case <-ctx.Done():
			// Exit early if the context is cancelled
			break LOOP

		default:
			log.Debugf("polling for S3 messages")

			start := time.Now()

			msgs, err := s.uploadEvents.Receive(ctx)
			if errors.Is(err, context.Canceled) {
				// Continue the loop, which will then exit due to the clause above
				continue LOOP
			} else if err != nil {
				return fmt.Errorf("failed to receive upload events: %w", err)
			}

			var (
				failed,
				successful []*sqs.S3Message
				recordCount int
			)

			for _, msg := range msgs {
				log.Debug("received S3 message", "message_id", msg.Id())

				eg.Go(func() error {
					processingErr := s.processS3Message(ctx, msg)
					if processingErr != nil {
						// Log the error and append to the list of failed messages
						failed = append(failed, msg)

						log.Error(
							"failed to process upload message",
							"message_id", msg.Id(),
							"err", processingErr,
						)
					} else {
						// Otherwise, append to the list of successful messages
						successful = append(successful, msg)
						recordCount += len(msg.Records)
					}

					return nil
				})
			}

			// Wait for all messages to finish processing
			if err = eg.Wait(); err != nil {
				return fmt.Errorf("failure during message processing: %w", err)
			}

			// Delete any messages from the queue that were successfully processed
			if len(successful) > 0 {
				log.Debug("deleting successful messages from queue", "count", len(successful))

				if err = s.uploadEvents.Delete(ctx, successful); err != nil {
					return fmt.Errorf("failed to delete successful messages from queue: %w", err)
				}
			}

			// Log the results of the processing and return an error if there were any failures
			// We want to stop processing messages if any single message fails
			log.Info("finished processing messages",
				"count", len(msgs),
				"successful", len(successful),
				"failed", len(failed),
				"elapsed", time.Since(start),
				"record_count", recordCount,
			)

			if len(failed) > 0 {
				return fmt.Errorf("failed to process %d messages", len(failed))
			}
		}
	}

	log.Info("stopped listening for S3 messages")

	return nil
}

func (s *Server) processS3Message(ctx context.Context, msg *sqs.S3Message) error {
	// Acquire a db connection from the pool
	conn, err := s.pgPool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire postgres connection: %w", err)
	}

	// Release the connection when we're done
	defer conn.Release()

	// Begin a transaction
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin postgres transaction: %w", err)
	}

	defer func() {
		// Ensures the transaction is rolled back when the method returns
		// If tx.Commit() was called before the function returned, this has no effect
		_ = tx.Rollback(ctx)
	}()

	// Create a new type wrapper for postgres queries
	queries := db.New(tx)

	// Process each record in the message
	for _, record := range msg.Records {
		switch record.EventName {
		// TODO handle copy events
		case sqs.EventNameObjectCreatedCopy:
			log.Warn("unhandled upload event", "event", record.EventName)

		case sqs.EventNameObjectCreatedPut,
			sqs.EventNameObjectCreatedPost,
			sqs.EventNameObjectCreatedCompleteMultipartUpload:
			// Skip paths that should be ignored
			if shouldIgnorePath(record.Key) {
				log.Debug("ignoring path", "key", record.Key)
				continue
			}

			// Determine object type and compression
			analysis, err := examinePath(record.Key)
			if err != nil {
				return fmt.Errorf("failed to analyze path '%s': %w", record.Key, err)
			}

			// Get the nix hash from the path
			hash, err := hashFromPath(record.Key, analysis.ObjectType)
			if err != nil {
				return fmt.Errorf("failed to get hash from path %s: %w", record.Key, err)
			}

			// Insert the object into the database
			if err = queries.PutObject(ctx, db.PutObjectParams{
				Hash:       hash,
				ObjectType: analysis.ObjectType,
				Path:       record.Key,
				Size:       record.Size,
			}); err != nil {
				return fmt.Errorf("failed to put object in db: %w", err)
			}

			// if it's a narinfo, fetch it from S3 and import into the DB

			if analysis.ObjectType != db.ObjectTypeNarinfo {
				continue
			}

			if importErr := s.importNarInfo(ctx, hash, queries, msg, &record); importErr != nil {
				return fmt.Errorf("failed to import narinfo: %w", importErr)
			}
		}
	}

	// Commit the transaction
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	log.Debug("finished processing S3 message", "record_count", len(msg.Records))

	return nil
}

func (s *Server) importNarInfo(
	ctx context.Context,
	hash []byte,
	queries *db.Queries,
	msg *sqs.S3Message,
	record *sqs.S3Record,
) error {
	log.Debug("importing narinfo", "bucket", record.BucketName, "key", record.Key)

	// Check that the message is for the expected bucket
	if record.BucketName != s.s3.BucketName() {
		log.Warn(
			"received message from unexpected bucket, ignoring",
			"message_id", msg.Id(),
			"expected", s.s3.BucketName(),
			"received", record.BucketName,
		)

		return nil
	}

	// Fetch the narinfo from s3
	out, err := s.s3.GetObject(ctx, record.Key)
	if err != nil {
		return fmt.Errorf("failed to get object '%s' from s3: %w", record.Key, err)
	}

	// Parse the narinfo
	info, err := narinfo.Parse(out.Body)
	if err != nil {
		return fmt.Errorf("failed to parse narinfo: %w", err)
	}

	// todo check the hash provided matches the one parsed from the narinfo

	// Extract and decode reference hashes (first 32 chars of each reference)
	references := make([][]byte, 0, len(info.References))
	for _, ref := range info.References {
		if len(ref) >= 32 {
			refBytes, err := nixbase32.DecodeString(ref[:32])
			if err != nil {
				log.Warn("failed to decode reference hash", "ref", ref[:32], "error", err)
				continue
			}

			references = append(references, refBytes)
		}
	}

	// Build signatures array as "name:base64data" strings
	signatures := make([]string, len(info.Signatures))
	for idx, sig := range info.Signatures {
		signatures[idx] = sig.Name + ":" + base64.StdEncoding.EncodeToString(sig.Data)
	}

	err = queries.PutNarInfo(ctx, db.PutNarInfoParams{
		Hash:        hash,
		Url:         info.URL,
		StorePath:   info.StorePath,
		Compression: db.CompressionType(info.Compression),
		FileHash:    info.FileHash.String(),
		//nolint:gosec
		FileSize: int64(info.FileSize),
		NarHash:  info.NarHash.String(),
		//nolint:gosec
		NarSize:    int64(info.NarSize),
		Deriver:    info.Deriver,
		References: references,
		Signatures: signatures,
	})
	if err != nil {
		return fmt.Errorf("failed to put narinfo in db: %w", err)
	}

	log.Debug("finished importing narinfo", "bucket", record.BucketName, "key", record.Key)

	return nil
}
