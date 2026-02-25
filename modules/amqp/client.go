package amqpclient

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"os"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"
)

func Dial(rabbitURL string) (*amqp.Connection, error) {
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
	enableTLS := envBool("RABBITMQ_TLS_ENABLE", false)
	serverName := strings.TrimSpace(os.Getenv("RABBITMQ_TLS_SERVER_NAME"))
	if parsed, err := url.Parse(rabbitURL); err == nil {
		if parsed.Scheme == "amqps" {
			enableTLS = true
		}
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
	insecure := envBool("RABBITMQ_TLS_INSECURE", false)

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

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	value = strings.ToLower(value)
	switch value {
	case "true", "1", "yes", "y":
		return true
	case "false", "0", "no", "n":
		return false
	default:
		return fallback
	}
}
