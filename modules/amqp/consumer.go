package amqpclient

import (
	"context"
	"errors"
	"log"
)

type Config struct {
	RabbitURL string
	QueueName string
}

type Handler func(ctx context.Context, body []byte) error

func Consume(ctx context.Context, cfg Config, handler Handler) error {
	if handler == nil {
		return errors.New("amqp handler not configured")
	}

	conn, err := Dial(cfg.RabbitURL)
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

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-msgs:
			if !ok {
				return errors.New("rabbitmq channel closed")
			}
			if err := handler(ctx, msg.Body); err != nil {
				log.Printf("[worker] job failed: %v", err)
				_ = msg.Nack(false, false)
				continue
			}
			_ = msg.Ack(false)
		}
	}
}
