package queue

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"os"
	workerutils "releaseaworker/internal/platform/utils"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"
)

func DialRabbitMQ(rabbitURL string) (*amqp.Connection, error) {
	tlsConfig, err := rabbitTLSConfig(rabbitURL)
	if err != nil {
		return nil, err
	}
	if tlsConfig != nil {
		return amqp.DialConfig(rabbitURL, amqp.Config{TLSClientConfig: tlsConfig})
	}
	return amqp.Dial(rabbitURL)
}

func rabbitTLSConfig(rabbitURL string) (*tls.Config, error) {
	enableTLS, err := rabbitTLSEnabled(rabbitURL)
	if err != nil {
		return nil, err
	}
	serverName := strings.TrimSpace(os.Getenv("RABBITMQ_TLS_SERVER_NAME"))
	if parsed, err := url.Parse(rabbitURL); err == nil {
		if serverName == "" {
			serverName = parsed.Hostname()
		}
	}
	if !enableTLS {
		return nil, nil
	}

	caPath := strings.TrimSpace(os.Getenv("RABBITMQ_TLS_CA_PATH"))
	certPath := strings.TrimSpace(os.Getenv("RABBITMQ_TLS_CERT_PATH"))
	keyPath := strings.TrimSpace(os.Getenv("RABBITMQ_TLS_KEY_PATH"))
	insecure := workerutils.EnvBool("RABBITMQ_TLS_INSECURE", false)
	if insecure && rabbitTLSRequired() && !workerutils.EnvBool("RABBITMQ_ALLOW_INSECURE_IN_PRODUCTION", false) {
		return nil, errors.New("RABBITMQ_TLS_INSECURE is not allowed when production queue TLS is required")
	}

	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecure,
	}
	if serverName != "" {
		tlsConfig.ServerName = serverName
	}
	if caPath != "" {
		caBytes, err := os.ReadFile(caPath)
		if err != nil {
			return nil, err
		}
		rootCAs := x509.NewCertPool()
		if !rootCAs.AppendCertsFromPEM(caBytes) {
			return nil, errors.New("failed to parse RABBITMQ_TLS_CA_PATH")
		}
		tlsConfig.RootCAs = rootCAs
	}
	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" {
			return nil, errors.New("RABBITMQ_TLS_CERT_PATH and RABBITMQ_TLS_KEY_PATH must both be set")
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	return tlsConfig, nil
}

func rabbitTLSEnabled(rabbitURL string) (bool, error) {
	enableTLS := workerutils.EnvBool("RABBITMQ_TLS_ENABLE", false)
	parsed, _ := url.Parse(rabbitURL)
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme == "amqps" {
		enableTLS = true
	}

	switch strings.ToLower(strings.TrimSpace(os.Getenv("RABBITMQ_TLS_MODE"))) {
	case "", "auto":
		if rabbitTLSRequired() && !enableTLS {
			return false, errors.New("production queue transport requires TLS; configure amqps:// or enable RabbitMQ TLS explicitly")
		}
		return enableTLS, nil
	case "required":
		if !enableTLS {
			return false, errors.New("RABBITMQ_TLS_MODE=required requires TLS-enabled RabbitMQ transport")
		}
		return true, nil
	case "disabled":
		if rabbitTLSRequired() && !workerutils.EnvBool("RABBITMQ_ALLOW_INSECURE_IN_PRODUCTION", false) {
			return false, errors.New("RABBITMQ_TLS_MODE=disabled is not allowed in production without RABBITMQ_ALLOW_INSECURE_IN_PRODUCTION=true")
		}
		return false, nil
	default:
		return false, errors.New("RABBITMQ_TLS_MODE must be one of auto, required, or disabled")
	}
}

func rabbitTLSRequired() bool {
	if workerutils.EnvBool("RABBITMQ_TLS_REQUIRE", false) {
		return true
	}
	for _, key := range []string{"RELEASEA_RUNTIME_ENV", "RELEASEA_ENV", "APP_ENV", "ENVIRONMENT"} {
		switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
		case "prod", "production":
			return true
		}
	}
	return false
}
