package queue

import "testing"

func TestRabbitTLSEnabledRequiresTLSInProduction(t *testing.T) {
	t.Setenv("RELEASEA_RUNTIME_ENV", "production")
	t.Setenv("RABBITMQ_TLS_MODE", "auto")
	t.Setenv("RABBITMQ_TLS_ENABLE", "false")

	if _, err := rabbitTLSEnabled("amqp://guest:guest@rabbitmq:5672/"); err == nil {
		t.Fatalf("expected production TLS requirement error")
	}
}

func TestRabbitTLSEnabledRejectsDisabledModeInProduction(t *testing.T) {
	t.Setenv("RELEASEA_RUNTIME_ENV", "production")
	t.Setenv("RABBITMQ_TLS_MODE", "disabled")

	if _, err := rabbitTLSEnabled("amqp://guest:guest@rabbitmq:5672/"); err == nil {
		t.Fatalf("expected disabled mode rejection in production")
	}
}

func TestRabbitTLSEnabledAcceptsAmqpsWhenRequired(t *testing.T) {
	t.Setenv("RABBITMQ_TLS_MODE", "required")

	enabled, err := rabbitTLSEnabled("amqps://guest:guest@rabbitmq:5671/")
	if err != nil {
		t.Fatalf("unexpected TLS mode error: %v", err)
	}
	if !enabled {
		t.Fatalf("expected TLS to be enabled")
	}
}
