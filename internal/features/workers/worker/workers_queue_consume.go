package workers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
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

	_, err = ch.QueueDeclare(
		cfg.QueueName,
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
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

	log.Printf("[worker] listening for jobs on %s", cfg.QueueName)

	client := &http.Client{Timeout: 15 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-msgs:
			if !ok {
				return errors.New("rabbitmq channel closed")
			}
			if err := processJob(ctx, client, cfg, tokens, msg); err != nil {
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

	return processOperationByID(ctx, client, cfg, tokens, job.OperationID)
}
