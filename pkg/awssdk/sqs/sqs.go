package sqs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/numtide/narwal/pkg/awssdk"
	"github.com/numtide/narwal/pkg/config"
)

func NewClient(
	ctx context.Context,
	awsCfg *config.AWS,
) (*sqs.Client, error) {
	// Load AWS SDK config
	sdkCfg, err := awssdk.LoadSDKConfig(ctx, awsCfg)
	if err != nil {
		//nolint:wrapcheck
		return nil, err
	}

	// Create SQS client
	return sqs.NewFromConfig(*sdkCfg), nil
}

// resolveQueueURL resolves a queue name to a queue URL.
func resolveQueueURL(
	ctx context.Context,
	client *sqs.Client,
	queueName string,
) (string, error) {
	output, err := client.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{
		QueueName: aws.String(queueName),
	})
	if err != nil {
		return "", fmt.Errorf("failed to get queue URL for %s: %w", queueName, err)
	}

	return aws.ToString(output.QueueUrl), nil
}
