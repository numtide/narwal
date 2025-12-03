package config

import (
	"fmt"
	"time"

	"github.com/spf13/pflag"
)

// DefaultLongPollingTimeout is the default SQS long polling wait time.
// AWS SQS supports 0-20 seconds, with 20 being the maximum recommended value
// for reducing empty responses and API costs.
const DefaultLongPollingTimeout = 20 * time.Second

type SQS struct {
	// SQS queue name for S3 upload events
	UploadEventQueue string `mapstructure:"upload_event_queue"`
	// SQS queue name for S3 delete events
	DeleteEventQueue string `mapstructure:"delete_event_queue"`
	// Long polling timeout for SQS ReceiveMessage calls (0-20 seconds)
	LongPollingTimeout time.Duration `mapstructure:"long_polling_timeout"`
}

func (s *SQS) Validate() error {
	if s.LongPollingTimeout < 0 {
		return fmt.Errorf("%w: sqs long_polling_timeout cannot be negative", ErrInvalidConfig)
	}

	if s.LongPollingTimeout > 20*time.Second {
		return fmt.Errorf("%w: sqs long_polling_timeout cannot exceed 20 seconds (AWS limit)", ErrInvalidConfig)
	}

	return nil
}

// GetLongPollingTimeout returns the configured timeout or the default value.
func (s *SQS) GetLongPollingTimeout() time.Duration {
	if s.LongPollingTimeout == 0 {
		return DefaultLongPollingTimeout
	}

	return s.LongPollingTimeout
}

func SetSQSFlags(fs *pflag.FlagSet) {
	fs.String("sqs.upload_event_queue", "", "SQS queue name for S3 upload events")
	fs.String("sqs.delete_event_queue", "", "SQS queue name for S3 delete events")
	fs.Duration("sqs.long_polling_timeout", DefaultLongPollingTimeout, "Long polling timeout for SQS (0-20s)")
}
