package workers

import (
	"context"
	"log"
	"net/http"
	maintenancemodule "releaseaworker/internal/features/maintenance/worker"
	platformauth "releaseaworker/internal/platform/auth"
	"releaseaworker/internal/platform/models"
	"time"
)

var consumerRunner = runConsumer
var backoffWaiter = waitWithBackoff

func Run(ctx context.Context, cfg models.Config) error {
	tokens := platformauth.NewTokenManager(cfg.Token)
	log.Printf("[worker] starting id=%s name=%s env=%s namespace=%s version=%s", cfg.WorkerID, cfg.WorkerName, cfg.Environment, cfg.Namespace, cfg.Version)

	if cfg.RabbitURL != "" {
		go func() {
			runConsumerWithRetry(ctx, cfg, tokens)
		}()
	}
	go func() {
		runPoller(ctx, cfg, tokens)
	}()
	go func() {
		maintenancemodule.RunCurator(ctx, cfg, tokens)
	}()
	go func() {
		maintenancemodule.RunRuntimeMonitor(ctx, cfg, tokens)
	}()
	go func() {
		maintenancemodule.RunAutoDeployMonitor(ctx, cfg, tokens)
	}()

	client := &http.Client{Timeout: 10 * time.Second}
	ticker := time.NewTicker(cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		if err := sendHeartbeat(ctx, client, cfg, tokens); err != nil {
			log.Printf("[worker] heartbeat error: %v", err)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			continue
		}
	}
}

func runConsumerWithRetry(ctx context.Context, cfg models.Config, tokens *platformauth.TokenManager) {
	backoff := 2 * time.Second
	maxBackoff := 30 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := consumerRunner(ctx, cfg, tokens); err != nil {
			log.Printf("[worker] consumer error: %v (retrying in %s)", err, backoff)
			if !backoffWaiter(ctx, backoff) {
				return
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		backoff = 2 * time.Second
	}
}

func waitWithBackoff(ctx context.Context, backoff time.Duration) bool {
	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
