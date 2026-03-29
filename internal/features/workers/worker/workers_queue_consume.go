package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
	platformcorrelation "releaseaworker/internal/platform/correlation"
	"releaseaworker/internal/platform/models"
	platformqueue "releaseaworker/internal/platform/queue"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func runConsumer(ctx context.Context, cfg models.Config, tokens *platformauth.TokenManager) error {
	conn, err := platformqueue.DialRabbitMQ(cfg.RabbitURL)
	if err != nil {
		return err
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.Qos(cfg.QueuePrefetch, 0, false); err != nil {
		return err
	}

	topology := platformqueue.ResolveQueueTopology(cfg.QueueName)
	if err := platformqueue.DeclareQueueTopology(ch, topology); err != nil {
		return err
	}

	msgs, err := ch.Consume(
		cfg.QueueName,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	if topology.DeadLetterEnabled {
		log.Printf("[worker] listening for jobs on %s (prefetch=%d dlq=%s)", cfg.QueueName, cfg.QueuePrefetch, topology.DeadLetterQueueName)
	} else {
		log.Printf("[worker] listening for jobs on %s (prefetch=%d)", cfg.QueueName, cfg.QueuePrefetch)
	}

	client := &http.Client{Timeout: 15 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-msgs:
			if !ok {
				return errors.New("rabbitmq channel closed")
			}
			control, err := defaultWorkerPoolControlCache.get(ctx, client, cfg, tokens)
			if err == nil && workerPoolBlocksClaims(control) {
				_ = msg.Nack(false, true)
				if waitErr := waitWithContext(ctx, 2*time.Second); waitErr != nil {
					return waitErr
				}
				continue
			}
			if err := processJob(ctx, client, cfg, tokens, msg); err != nil {
				if errors.Is(err, ErrOperationNotCompatible) {
					log.Printf("[worker] job requeued: %v", err)
					_ = msg.Nack(false, true)
					continue
				}
				log.Printf("[worker] job failed: %v", err)
				_ = msg.Nack(false, false)
				continue
			}
			_ = msg.Ack(false)
		}
	}
}

func processJob(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, msg amqp.Delivery) error {
	var job models.OperationMessage
	if err := json.Unmarshal(msg.Body, &job); err != nil || job.OperationID == "" {
		return fmt.Errorf("invalid job payload: %s", string(msg.Body))
	}
	jobCtx := platformcorrelation.WithID(ctx, job.CorrelationID)
	if platformcorrelation.IDFromContext(jobCtx) == "" {
		jobCtx = platformcorrelation.WithID(jobCtx, platformcorrelation.NewID())
	}
	return processOperationByID(jobCtx, client, cfg, tokens, job.OperationID)
}
