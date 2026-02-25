package utils

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	workerlog "releaseaworker/internal/platform/logging"
	"releaseaworker/internal/platform/models"
	"strings"
)

func execOutput(ctx context.Context, workDir, name string, args []string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd.CombinedOutput()
}

func RunCommandWithLogger(ctx context.Context, workDir, name string, args []string, env []string, logger *workerlog.DeployLogger) error {
	output, err := execOutput(ctx, workDir, name, args, env)
	if len(output) > 0 {
		log.Printf("[worker] %s output: %s", name, strings.TrimSpace(string(output)))
		if logger != nil {
			logger.AppendLines(ctx, splitOutputLines(output))
		}
	}
	if err != nil {
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

func RunShellWithLogger(ctx context.Context, workDir, command string, logger *workerlog.DeployLogger) error {
	if err := RunCommandWithLogger(ctx, workDir, "sh", []string{"-c", command}, nil, logger); err != nil {
		return fmt.Errorf("shell command failed: %w", err)
	}
	return nil
}

func splitOutputLines(output []byte) []string {
	text := strings.ReplaceAll(string(output), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	parts := strings.Split(text, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func RunCommandOutput(ctx context.Context, workDir, name string, args []string, env []string) (string, error) {
	output, err := execOutput(ctx, workDir, name, args, env)
	out := strings.TrimSpace(string(output))
	if len(output) > 0 {
		log.Printf("[worker] %s output: %s", name, out)
	}
	if err != nil {
		return out, fmt.Errorf("%s failed: %w", name, err)
	}
	return out, nil
}

func RunCommandWithInput(ctx context.Context, name string, args []string, input string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(output))
	if err != nil {
		if out != "" {
			return out, fmt.Errorf("%s failed: %s", name, out)
		}
		return out, fmt.Errorf("%s failed: %w", name, err)
	}
	return out, nil
}

func DockerLogin(ctx context.Context, registry, username, password string) error {
	if registry == "" {
		registry = "docker.io"
	}
	cmd := exec.CommandContext(ctx, "docker", "login", registry, "-u", username, "--password-stdin")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	go func() {
		defer stdin.Close()
		_, _ = io.WriteString(stdin, password)
	}()
	output, err := cmd.CombinedOutput()
	if len(output) > 0 {
		log.Printf("[worker] docker login: %s", strings.TrimSpace(string(output)))
	}
	if err != nil {
		return fmt.Errorf("docker login failed: %w", err)
	}
	return nil
}

func InjectToken(repoURL string, cred *models.SCMCredential) string {
	if cred == nil || cred.Token == "" {
		return repoURL
	}
	parsed, err := url.Parse(repoURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return repoURL
	}
	if parsed.User != nil && parsed.User.Username() != "" {
		return repoURL
	}
	user := "x-access-token"
	pass := cred.Token
	if strings.ToLower(cred.Provider) != "github" {
		user = cred.Token
		pass = ""
	}
	parsed.User = url.UserPassword(user, pass)
	return parsed.String()
}

func RegistryFromImage(image string) string {
	if image == "" {
		return ""
	}
	parts := strings.Split(image, "/")
	if len(parts) < 2 {
		return ""
	}
	host := parts[0]
	if strings.Contains(host, ".") || strings.Contains(host, ":") {
		return host
	}
	return ""
}

func NormalizeRegistryHost(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		if parsed, err := url.Parse(value); err == nil {
			value = parsed.Host
		}
	}
	value = strings.TrimSuffix(value, "/v1/")
	value = strings.TrimSuffix(value, "/")
	if value == "index.docker.io" {
		return "docker.io"
	}
	return value
}
