package deploy

import (
	"context"
	"fmt"
	"net/http"
)

type DeploymentStrategyConfig struct {
	Type             string
	CanaryPercent    int
	BlueGreenPrimary string
}

type ServiceConfig struct {
	ID                 string
	Replicas           int
	MinReplicas        int
	MaxReplicas        int
	CPU                int
	DeployTemplateID   string
	DeploymentStrategy DeploymentStrategyConfig
}

type Logger interface {
	Logf(ctx context.Context, format string, args ...interface{})
	Flush(ctx context.Context)
}

type Dependencies struct {
	ApplyResourceFn  func(ctx context.Context, client *http.Client, token string, resource map[string]interface{}) error
	DeleteResourceFn func(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) error
	ResourceExistsFn func(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) (bool, error)
	FetchResourceFn  func(ctx context.Context, client *http.Client, token, apiVersion, kind, namespace, name string) (map[string]interface{}, error)
	CloneResourceFn  func(resource map[string]interface{}) map[string]interface{}
}

func (deps Dependencies) validate() error {
	if deps.ApplyResourceFn == nil {
		return fmt.Errorf("apply resource dependency missing")
	}
	if deps.DeleteResourceFn == nil {
		return fmt.Errorf("delete resource dependency missing")
	}
	if deps.ResourceExistsFn == nil {
		return fmt.Errorf("resource exists dependency missing")
	}
	return nil
}
