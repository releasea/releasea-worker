package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"releaseaworker/internal/worker/config"
	"releaseaworker/internal/worker/ops"
)

func main() {
	cfg := config.Load()
	log.Printf("[worker] starting id=%s name=%s env=%s namespace=%s", cfg.WorkerID, cfg.WorkerName, cfg.Environment, cfg.Namespace)
	log.Printf("[worker] api=%s heartbeat=%s", cfg.ApiBaseURL, cfg.HeartbeatInterval)
	if cfg.RabbitURL != "" {
		log.Printf("[worker] rabbitmq=%s queue=%s", cfg.RabbitURL, cfg.QueueName)
	} else {
		log.Printf("[worker] rabbitmq=disabled")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := ops.Run(ctx, cfg)
	if err != nil {
		log.Fatalf("[worker] exited with error: %v", err)
	}
}
