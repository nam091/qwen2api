package browserengine

import (
	"log/slog"
	"net/http"
	"os"
	"testing"
)

func TestIsBlockResponse(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{200, false},
		{403, true},
		{401, true},
		{429, true},
		{500, false},
	}
	for _, tt := range tests {
		resp := &http.Response{StatusCode: tt.status, Header: http.Header{}}
		got := isBlockResponse(resp)
		if got != tt.want {
			t.Errorf("isBlockResponse(%d) = %v, want %v", tt.status, got, tt.want)
		}
	}
}

func TestIsBlockResponseNil(t *testing.T) {
	if isBlockResponse(nil) {
		t.Error("nil response should not be blocked")
	}
}

func TestNewHybridEngineDefaults(t *testing.T) {
	e := NewHybridEngine(nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), HTTPFirst)
	if e.httpClient == nil {
		t.Error("expected default http client")
	}
	stats := e.Stats()
	if stats.HTTPHits != 0 || stats.BrowserHits != 0 || stats.Fallbacks != 0 {
		t.Error("expected all stats to be 0")
	}
}

func TestCloseNilBrowser(t *testing.T) {
	e := NewHybridEngine(nil, nil, slog.New(slog.NewTextHandler(os.Stderr, nil)), HTTPFirst)
	if err := e.Close(); err != nil {
		t.Errorf("Close on nil browser should not error: %v", err)
	}
}
