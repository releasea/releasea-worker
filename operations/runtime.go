package operations

import (
	"context"
	"log"
	"net/http"
	"time"
)

func Run(ctx context.Context, cfg Config) error {
	tokens := newTokenManager(cfg.Token)
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
		runCurator(ctx, cfg, tokens)
	}()
	go func() {
		runRuntimeMonitor(ctx, cfg, tokens)
	}()
	go func() {
		runAutoDeployMonitor(ctx, cfg, tokens)
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

func runConsumerWithRetry(ctx context.Context, cfg Config, tokens *tokenManager) {
	backoff := 2 * time.Second
	maxBackoff := 30 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		if err := runConsumer(ctx, cfg, tokens); err != nil {
			log.Printf("[worker] consumer error: %v (retrying in %s)", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
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
