package config

import (
	"github.com/spf13/pflag"
)

type SQS struct {
	// SQS queue name for S3 upload events
	UploadEventQueue string `mapstructure:"upload_event_queue"`
	// SQS queue name for S3 delete events
	DeleteEventQueue string `mapstructure:"delete_event_queue"`
}

func SetSQSFlags(fs *pflag.FlagSet) {
	fs.String("sqs.upload_event_queue", "", "SQS queue name for S3 upload events")
	fs.String("sqs.delete_event_queue", "", "SQS queue name for S3 delete events")
}
