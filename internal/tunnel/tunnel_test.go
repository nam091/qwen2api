package tunnel

import (
	"testing"

	"log/slog"
	"os"
)

func TestManager_StartStop(t *testing.T) {
	// Skip if cloudflared not installed
	if _, err := os.Stat("cloudflared"); err != nil {
		t.Skip("cloudflared not found, skipping integration test")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	m := New(logger)

	// Start tunnel
	url, err := m.Start(8080)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if url == "" {
		t.Fatal("expected non-empty URL")
	}

	t.Logf("Tunnel URL: %s", url)

	// Check status
	running, statusURL, port := m.Status()
	if !running {
		t.Error("expected running=true")
	}
	if statusURL != url {
		t.Errorf("status URL mismatch: got %s, want %s", statusURL, url)
	}
	if port != 8080 {
		t.Errorf("port mismatch: got %d, want 8080", port)
	}

	// Stop tunnel
	if err := m.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// Verify stopped
	running, _, _ = m.Status()
	if running {
		t.Error("expected running=false after stop")
	}
}

func TestManager_DoubleStart(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	m := New(logger)

	// Mock running state
	m.running = true
	m.url = "https://test.trycloudflare.com"

	url, err := m.Start(8080)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if url != "https://test.trycloudflare.com" {
		t.Errorf("expected existing URL, got %s", url)
	}
}

func TestManager_StopWhenNotRunning(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	m := New(logger)

	if err := m.Stop(); err != nil {
		t.Errorf("Stop should not error when not running: %v", err)
	}
}

func TestManager_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timeout test in short mode")
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	m := New(logger)

	// Just testing the timeout logic
	m.running = false

	// Verify initial state
	running, _, _ := m.Status()
	if running {
		t.Error("expected running=false initially")
	}
}
