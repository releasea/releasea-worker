package workers

import (
	"context"
	"log"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
	platformops "releaseaworker/internal/platform/integrations/operations"
	"releaseaworker/internal/platform/models"
	"time"
)

func runPoller(ctx context.Context, cfg models.Config, tokens *platformauth.TokenManager) {
	interval := cfg.PollInterval
	if interval <= 0 {
		interval = 20 * time.Second
	}
	limit := cfg.PollBatchLimit
	if limit <= 0 {
		limit = 10
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	client := &http.Client{Timeout: 15 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := drainQueuedOperations(ctx, client, cfg, tokens, limit); err != nil {
				log.Printf("[worker] poller error: %v", err)
			}
		}
	}
}

func drainQueuedOperations(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager, limit int) error {
	ops, err := platformops.FetchQueuedOperations(ctx, client, cfg, tokens)
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return nil
	}
	processed := 0
	for _, op := range ops {
		if processed >= limit {
			break
		}
		if err := processOperation(ctx, client, cfg, tokens, op); err != nil {
			log.Printf("[worker] poller operation %s failed: %v", op.ID, err)
		}
		processed++
	}
	return nil
}
