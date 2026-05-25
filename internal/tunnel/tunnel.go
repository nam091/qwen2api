// Package tunnel manages cloudflared tunnel for exposing the local server.
package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// Manager handles tunnel lifecycle.
type Manager struct {
	mu      sync.RWMutex
	cmd     *exec.Cmd
	cancel  context.CancelFunc
	url     string
	port    int
	logger  *slog.Logger
	running bool
}

// New creates a tunnel manager.
func New(logger *slog.Logger) *Manager {
	return &Manager{logger: logger}
}

// Start launches cloudflared tunnel for the given port.
// Returns the public URL once tunnel is established.
func (m *Manager) Start(port int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		return m.url, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "cloudflared", "tunnel", "--protocol", "http2", "--url", fmt.Sprintf("http://localhost:%d", port))

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return "", fmt.Errorf("stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return "", fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return "", fmt.Errorf("start cloudflared: %w", err)
	}

	m.cmd = cmd
	m.cancel = cancel
	m.port = port
	m.running = true

	urlChan := make(chan string, 1)
	errChan := make(chan error, 1)

	// Parse URL from cloudflared output
	urlPattern := regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			m.logger.Debug("cloudflared stdout", "line", line)
			if match := urlPattern.FindString(line); match != "" {
				select {
				case urlChan <- match:
				default:
				}
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			m.logger.Debug("cloudflared stderr", "line", line)
			if match := urlPattern.FindString(line); match != "" {
				select {
				case urlChan <- match:
				default:
				}
			}
		}
	}()

	go func() {
		if err := cmd.Wait(); err != nil {
			m.logger.Warn("cloudflared exited", "err", err)
			select {
			case errChan <- err:
			default:
			}
		}
	}()

	select {
	case url := <-urlChan:
		m.url = url
		m.logger.Info("tunnel established", "url", url, "port", port)
		return url, nil
	case err := <-errChan:
		m.running = false
		return "", fmt.Errorf("cloudflared failed: %w", err)
	case <-time.After(30 * time.Second):
		m.Stop()
		return "", fmt.Errorf("timeout waiting for tunnel URL")
	}
}

// Stop terminates the tunnel.
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.running {
		return nil
	}

	if m.cancel != nil {
		m.cancel()
	}

	if m.cmd != nil && m.cmd.Process != nil {
		if err := m.cmd.Process.Kill(); err != nil {
			m.logger.Warn("kill cloudflared", "err", err)
		}
	}

	m.running = false
	m.url = ""
	m.logger.Info("tunnel stopped")
	return nil
}

// Status returns current tunnel state.
func (m *Manager) Status() (running bool, url string, port int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running, m.url, m.port
}
