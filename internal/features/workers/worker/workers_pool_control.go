package workers

import (
	"context"
	"net/http"
	platformauth "releaseaworker/internal/platform/auth"
	platformops "releaseaworker/internal/platform/integrations/operations"
	"releaseaworker/internal/platform/models"
	"sync"
	"time"
)

type workerPoolControl struct {
	ID                 string `json:"id"`
	MaintenanceEnabled bool   `json:"maintenanceEnabled"`
	DrainEnabled       bool   `json:"drainEnabled"`
}

type workerPoolControlCache struct {
	mu       sync.Mutex
	value    workerPoolControl
	loadedAt time.Time
	hasValue bool
}

var fetchWorkerPoolControlRequest = func(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager) (workerPoolControl, error) {
	var control workerPoolControl
	err := platformops.DoJSONRequest(ctx, client, cfg, tokens, http.MethodGet, cfg.ApiBaseURL+"/workers/pool-control", nil, &control, "worker pool control fetch")
	return control, err
}

var poolControlCacheTTL = 5 * time.Second
var defaultWorkerPoolControlCache workerPoolControlCache

func (c *workerPoolControlCache) get(ctx context.Context, client *http.Client, cfg models.Config, tokens *platformauth.TokenManager) (workerPoolControl, error) {
	c.mu.Lock()
	if c.hasValue && time.Since(c.loadedAt) < poolControlCacheTTL {
		value := c.value
		c.mu.Unlock()
		return value, nil
	}
	c.mu.Unlock()

	control, err := fetchWorkerPoolControlRequest(ctx, client, cfg, tokens)
	if err != nil {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.hasValue {
			return c.value, nil
		}
		return workerPoolControl{}, err
	}

	c.mu.Lock()
	c.value = control
	c.loadedAt = time.Now()
	c.hasValue = true
	c.mu.Unlock()
	return control, nil
}

func workerPoolBlocksClaims(control workerPoolControl) bool {
	return control.MaintenanceEnabled || control.DrainEnabled
}
