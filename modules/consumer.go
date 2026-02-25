package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	amqpclient "releaseaworker/modules/amqp"
)

func runConsumer(ctx context.Context, cfg Config, tokens *tokenManager) error {
	client := &http.Client{Timeout: 15 * time.Second}
	return amqpclient.Consume(ctx, amqpclient.Config{
		RabbitURL: cfg.RabbitURL,
		QueueName: cfg.QueueName,
	}, func(jobCtx context.Context, body []byte) error {
		return processJob(jobCtx, client, cfg, tokens, body)
	})
}

func processJob(ctx context.Context, client *http.Client, cfg Config, tokens *tokenManager, body []byte) error {
	var job operationMessage
	if err := json.Unmarshal(body, &job); err != nil || job.OperationID == "" {
		return fmt.Errorf("invalid job payload: %s", string(body))
	}

	return processOperationByID(ctx, client, cfg, tokens, job.OperationID)
}
