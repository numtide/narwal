package sqs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/charmbracelet/log"
	"github.com/numtide/narwal/pkg/config"
)

const (
	EventNameObjectCreatedPut                     = "ObjectCreated:Put"
	EventNameObjectCreatedPost                    = "ObjectCreated:Post"
	EventNameObjectCreatedCopy                    = "ObjectCreated:Copy"
	EventNameObjectCreatedCompleteMultipartUpload = "ObjectCreated:CompleteMultipartUpload"
)

// S3Record represents a parsed S3 event notification from SQS.
type S3Record struct {
	// Event metadata
	EventName string // e.g., "ObjectCreated:Put", "ObjectCreated:CompleteMultipartUpload"
	EventTime time.Time

	// Bucket information
	BucketName string

	// Object information
	Key  string // URL-decoded object key
	Size int64
	ETag string

	// Err contains any error that occurred during message retrieval or parsing.
	// When Err is non-nil, other fields may be empty/zero.
	Err error
}

type S3Message struct {
	Msg     types.Message
	Records []S3Record
}

func (s S3Message) Id() string {
	return aws.ToString(s.Msg.MessageId)
}

// ParseS3Msg parses an SQS message body into S3Record structs.
func ParseS3Msg(msg types.Message) (*S3Message, error) {
	body := aws.ToString(msg.Body)

	var notification s3Message
	if err := json.Unmarshal([]byte(body), &notification); err != nil {
		return nil, fmt.Errorf("unmarshal S3 notification: %w", err)
	}

	events := make([]S3Record, 0, len(notification.Records))

	for _, record := range notification.Records {
		// URL-decode the object key
		decodedKey, err := url.QueryUnescape(record.S3.Object.Key)
		if err != nil {
			decodedKey = record.S3.Object.Key // Fallback to encoded key
		}

		event := S3Record{
			EventName:  record.EventName,
			EventTime:  record.EventTime,
			BucketName: record.S3.Bucket.Name,
			Key:        decodedKey,
			Size:       record.S3.Object.Size,
			ETag:       record.S3.Object.ETag,
		}

		events = append(events, event)
	}

	return &S3Message{Msg: msg, Records: events}, nil
}

type S3EventQueue struct {
	log      *log.Logger
	client   *sqs.Client
	queueURL string
}

func NewS3EventQueue(
	ctx context.Context,
	client *sqs.Client,
	cfg *config.SQS,
) (*S3EventQueue, error) {
	// Resolve the queue name to a URL
	queueURL, err := resolveQueueURL(ctx, client, cfg.UploadEventQueue)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve queue URL: %w", err)
	}

	return &S3EventQueue{
		client:   client,
		queueURL: queueURL,
		log:      log.WithPrefix("S3UploadEvents").With("queueURL", queueURL),
	}, nil
}

func (s *S3EventQueue) Receive(ctx context.Context) ([]*S3Message, error) {
	output, err := s.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
		QueueUrl:            aws.String(s.queueURL),
		MaxNumberOfMessages: 10, // AWS maximum per request
		WaitTimeSeconds:     20, // AWS maximum per request,
	})
	if err != nil {
		return nil, fmt.Errorf("receive message failed: %w", err)
	}

	batch := make([]*S3Message, 0, len(output.Messages))

	for _, msg := range output.Messages {
		// Parse the message
		s3Msg, err := ParseS3Msg(msg)
		if err != nil {
			return nil, fmt.Errorf("failed to parse S3 message: %w", err)
		}

		// Append to the batch
		batch = append(batch, s3Msg)
	}

	return batch, nil
}

func (s *S3EventQueue) Delete(ctx context.Context, batch []*S3Message) error {
	entries := make([]types.DeleteMessageBatchRequestEntry, len(batch))

	for i, msg := range batch {
		entries[i] = types.DeleteMessageBatchRequestEntry{
			Id:            msg.Msg.MessageId,
			ReceiptHandle: msg.Msg.ReceiptHandle,
		}
	}

	output, err := s.client.DeleteMessageBatch(ctx, &sqs.DeleteMessageBatchInput{
		Entries:  entries,
		QueueUrl: aws.String(s.queueURL),
	})
	if err != nil {
		return fmt.Errorf("failed to delete messages: %w", err)
	}

	for _, failed := range output.Failed {
		s.log.Error(
			"failed to delete message",
			"id", failed.Id,
			"code", failed.Code,
			"message", failed.Message,
			"sender_fault", failed.SenderFault,
		)
	}

	if len(output.Failed) > 0 {
		return fmt.Errorf("failed to delete %d messages", len(output.Failed))
	}

	return nil
}

// s3Message represents the top-level SQS message body structure.
type s3Message struct {
	Records []s3Record `json:"Records"`
}

// s3Record represents a single S3 event record.
type s3Record struct {
	EventName string    `json:"eventName"`
	EventTime time.Time `json:"eventTime"`

	S3 struct {
		Bucket struct {
			Name string `json:"name"`
		} `json:"bucket"`
		Object struct {
			Key  string `json:"key"`
			Size int64  `json:"size"`
			ETag string `json:"eTag"`
		} `json:"object"`
	} `json:"s3"`
}
