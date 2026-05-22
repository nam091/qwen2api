package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/keaume34/qwen2api/internal/config"
)

// metricsMiddleware tracks request counts and latency.
func (h *handlers) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.deps.Metrics == nil || !h.deps.Config.Features.Metrics {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		ww := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.deps.Metrics.Gauge("qwen2api_active_requests").Inc()
		defer h.deps.Metrics.Gauge("qwen2api_active_requests").Dec()
		next.ServeHTTP(ww, r)
		h.deps.Metrics.Counter("qwen2api_http_requests_total").Inc()
		h.deps.Metrics.Histogram("qwen2api_http_latency_seconds").Observe(time.Since(start).Seconds())
		if ww.status >= 400 {
			h.deps.Metrics.Counter("qwen2api_http_errors_total").Inc()
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// prometheusMetrics renders metrics in text exposition format.
func (h *handlers) prometheusMetrics(w http.ResponseWriter, _ *http.Request) {
	if !h.deps.Config.Features.Metrics {
		http.Error(w, "Metrics feature is disabled", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write([]byte(h.deps.Metrics.Render()))
}

// adminMiddleware requires the configured admin token.
func (h *handlers) adminMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerOrQuery(r)
		if !h.deps.Config.AuthorizedAdmin(token) {
			writeError(w, http.StatusUnauthorized, "invalid_admin_token", "admin token required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// listAPIKeys returns the configured API keys (values masked).
func (h *handlers) listAPIKeys(w http.ResponseWriter, _ *http.Request) {
	if !h.deps.Config.Features.APIKeyRotation {
		writeError(w, http.StatusForbidden, "feature_disabled", "API Key Rotation feature is disabled")
		return
	}
	type maskedKey struct {
		Name      string `json:"name,omitempty"`
		Value     string `json:"value"`
		ExpiresAt int64  `json:"expires_at,omitempty"`
	}
	out := make([]maskedKey, 0, len(h.deps.Config.APIKeys))
	for _, k := range h.deps.Config.APIKeys {
		v := k.Value
		if len(v) > 8 {
			v = v[:4] + "..." + v[len(v)-4:]
		}
		out = append(out, maskedKey{
			Name:      k.Name,
			Value:     v,
			ExpiresAt: k.ExpiresAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

// createAPIKey appends a new key (in-memory only; not persisted).
func (h *handlers) createAPIKey(w http.ResponseWriter, r *http.Request) {
	if !h.deps.Config.Features.APIKeyRotation {
		writeError(w, http.StatusForbidden, "feature_disabled", "API Key Rotation feature is disabled")
		return
	}
	var body config.APIKey
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}
	if body.Value == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "value is required")
		return
	}
	h.deps.Config.APIKeys = append(h.deps.Config.APIKeys, body)
	writeJSON(w, http.StatusCreated, map[string]any{"created": body.Name, "total": len(h.deps.Config.APIKeys)})
}

// deleteAPIKey removes a key by exact value match (admin must know the value).
func (h *handlers) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if !h.deps.Config.Features.APIKeyRotation {
		writeError(w, http.StatusForbidden, "feature_disabled", "API Key Rotation feature is disabled")
		return
	}
	value := chi.URLParam(r, "value")
	if value == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "value is required")
		return
	}
	out := h.deps.Config.APIKeys[:0]
	deleted := 0
	for _, k := range h.deps.Config.APIKeys {
		masked := k.Value
		if len(masked) > 8 {
			masked = masked[:4] + "..." + masked[len(masked)-4:]
		}
		if k.Value == value || k.Name == value || masked == value {
			deleted++
			continue
		}
		out = append(out, k)
	}
	h.deps.Config.APIKeys = out
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

// embeddings is a placeholder OpenAI-compatible endpoint.
func (h *handlers) embeddings(w http.ResponseWriter, r *http.Request) {
	if !h.deps.Config.Features.Embeddings {
		writeError(w, http.StatusForbidden, "feature_disabled", "Embeddings feature is disabled")
		return
	}
	defer func() { _ = r.Body.Close() }()
	var req struct {
		Model string `json:"model"`
		Input any    `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}
	if req.Model == "" || req.Input == nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "model and input required")
		return
	}

	// chat.qwen.ai web does not expose an embeddings endpoint; we return 501
	// with a structured response rather than fabricating vectors.
	writeError(w, http.StatusNotImplemented, "not_implemented", "embeddings are not supported by chat.qwen.ai upstream; configure a dedicated embeddings backend")
}

// dashboard renders a single-page HTML view.
func (h *handlers) dashboard(w http.ResponseWriter, _ *http.Request) {
	if !h.deps.Config.Features.Dashboard {
		http.Error(w, "Dashboard feature is disabled", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Try to serve from web/dashboard.html first
	content, err := os.ReadFile("web/dashboard.html")
	if err == nil {
		_, _ = w.Write(content)
		return
	}
	// Fallback to embedded HTML
	_, _ = w.Write([]byte(dashboardHTML))
}

// dashboardData returns a JSON snapshot consumed by the dashboard page.
func (h *handlers) dashboardData(w http.ResponseWriter, _ *http.Request) {
	if !h.deps.Config.Features.Dashboard {
		http.Error(w, "Dashboard feature is disabled", http.StatusNotFound)
		return
	}
	out := map[string]any{
		"version":  "v2",
		"uptime":   time.Since(startedAt).Seconds(),
		"tokens":   h.deps.TokenPool.Statuses(),
		"features": h.deps.Config.Features,
	}
	if h.deps.Metrics != nil {
		out["metrics"] = h.deps.Metrics.Snapshot()
	}
	if h.deps.Cache != nil {
		size, hits, misses := h.deps.Cache.Stats()
		out["cache"] = map[string]any{
			"size":   size,
			"hits":   hits,
			"misses": misses,
		}
	}
	out["model_aliases"] = h.deps.Config.ModelAliases

	keys := make([]map[string]any, 0, len(h.deps.Config.APIKeys))
	for _, k := range h.deps.Config.APIKeys {
		v := k.Value
		if len(v) > 8 {
			v = v[:4] + "..." + v[len(v)-4:]
		}
		entry := map[string]any{"name": k.Name, "value": v}
		if k.ExpiresAt > 0 {
			entry["expires_at"] = k.ExpiresAt
		}
		keys = append(keys, entry)
	}
	out["api_keys"] = keys

	sortedKeys := make([]string, 0)
	for k := range out {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)

	writeJSON(w, http.StatusOK, out)
}

// streamLogs provides real-time HTTP server-sent events for request logs.
func (h *handlers) streamLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("X-Accel-Buffering", "no")

	logChan := h.deps.ReqLog.Subscribe()
	if logChan == nil {
		http.Error(w, "Logger not configured", http.StatusInternalServerError)
		return
	}
	defer h.deps.ReqLog.Unsubscribe(logChan)

	// Keep alive ticker to prevent connection close due to inactivity
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Initial message to verify connection
	_, _ = fmt.Fprintf(w, "event: connected\ndata: {\"status\":\"connected\"}\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, "event: ping\ndata: {}\n\n")
			flusher.Flush()
		case entry, ok := <-logChan:
			if !ok {
				return
			}
			data, err := json.Marshal(entry)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}


// startedAt is initialized when the server starts.
var startedAt = time.Now()

// dashboardHTML is the static UI served at /dashboard.
const dashboardHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>qwen2api dashboard</title>
<style>
  * { box-sizing: border-box; }
  body { font-family: -apple-system, system-ui, "Segoe UI", sans-serif; margin: 0; background: #0f172a; color: #e2e8f0; }
  header { padding: 16px 24px; background: #1e293b; border-bottom: 1px solid #334155; }
  header h1 { margin: 0; font-size: 18px; font-weight: 600; }
  header span { color: #64748b; font-size: 12px; margin-left: 8px; }
  main { padding: 24px; display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 16px; }
  .card { background: #1e293b; border: 1px solid #334155; border-radius: 8px; padding: 16px; }
  .card h2 { margin: 0 0 12px 0; font-size: 14px; color: #94a3b8; text-transform: uppercase; letter-spacing: 0.5px; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th, td { text-align: left; padding: 6px 8px; border-bottom: 1px solid #334155; }
  th { color: #64748b; font-weight: 500; }
  .ok { color: #4ade80; }
  .warn { color: #fbbf24; }
  .err { color: #f87171; }
  .metric { display: flex; justify-content: space-between; padding: 4px 0; font-size: 13px; }
  .metric .label { color: #94a3b8; }
  .metric .value { font-variant-numeric: tabular-nums; }
  .grid2 { display: grid; grid-template-columns: 1fr 1fr; gap: 4px 16px; }
  code { font-family: "JetBrains Mono", "Consolas", monospace; font-size: 12px; color: #cbd5e1; }
</style>
</head>
<body>
<header><h1>qwen2api</h1><span>v2 dashboard</span><span id="uptime"></span></header>
<main id="root">Loading…</main>
<script>
async function refresh() {
  try {
    const r = await fetch("/dashboard/data");
    if (!r.ok) throw new Error("HTTP " + r.status);
    const d = await r.json();
    render(d);
  } catch (e) {
    document.getElementById("root").innerHTML = "<div class='card err'>Failed to load: " + e.message + "</div>";
  }
}
function render(d) {
  const root = document.getElementById("root");
  document.getElementById("uptime").textContent = "  uptime: " + Math.round(d.uptime) + "s";
  let html = "";

  html += "<div class='card'><h2>Tokens</h2>";
  if (!d.tokens || d.tokens.length === 0) {
    html += "<div class='warn'>No tokens configured</div>";
  } else {
    html += "<table><tr><th>Name</th><th>Status</th><th>Hits</th><th>Failures</th></tr>";
    for (const t of d.tokens) {
      const cls = t.on_cooldown ? "err" : "ok";
      const status = t.on_cooldown ? "cooldown" : "active";
      html += "<tr><td>" + (t.name || "—") + "</td><td class='" + cls + "'>" + status + "</td><td>" + t.hits + "</td><td>" + t.failures + "</td></tr>";
    }
    html += "</table>";
  }
  html += "</div>";

  html += "<div class='card'><h2>Features</h2><div class='grid2'>";
  for (const k of Object.keys(d.features || {}).sort()) {
    const v = d.features[k];
    const cls = v ? "ok" : "warn";
    html += "<div class='label'>" + k + "</div><div class='" + cls + "'>" + (v ? "on" : "off") + "</div>";
  }
  html += "</div></div>";

  if (d.cache) {
    html += "<div class='card'><h2>Prompt Cache</h2>";
    html += "<div class='metric'><span class='label'>Size</span><span class='value'>" + d.cache.size + "</span></div>";
    html += "<div class='metric'><span class='label'>Hits</span><span class='value ok'>" + d.cache.hits + "</span></div>";
    html += "<div class='metric'><span class='label'>Misses</span><span class='value warn'>" + d.cache.misses + "</span></div>";
    html += "</div>";
  }

  if (d.metrics) {
    html += "<div class='card'><h2>Metrics</h2>";
    const counters = d.metrics.counters || {};
    for (const k of Object.keys(counters).sort()) {
      html += "<div class='metric'><span class='label'>" + k + "</span><span class='value'>" + counters[k] + "</span></div>";
    }
    const gauges = d.metrics.gauges || {};
    for (const k of Object.keys(gauges).sort()) {
      html += "<div class='metric'><span class='label'>" + k + "</span><span class='value'>" + gauges[k] + "</span></div>";
    }
    html += "</div>";
  }

  if (d.api_keys && d.api_keys.length > 0) {
    html += "<div class='card'><h2>API Keys</h2><table><tr><th>Name</th><th>Value</th><th>Expires</th></tr>";
    for (const k of d.api_keys) {
      const exp = k.expires_at ? new Date(k.expires_at * 1000).toISOString().slice(0, 10) : "never";
      html += "<tr><td>" + (k.name || "—") + "</td><td><code>" + k.value + "</code></td><td>" + exp + "</td></tr>";
    }
    html += "</table></div>";
  }

  if (d.model_aliases && Object.keys(d.model_aliases).length > 0) {
    html += "<div class='card'><h2>Model Aliases</h2><table><tr><th>Alias</th><th>Real Model</th></tr>";
    for (const k of Object.keys(d.model_aliases).sort()) {
      html += "<tr><td><code>" + k + "</code></td><td><code>" + d.model_aliases[k] + "</code></td></tr>";
    }
    html += "</table></div>";
  }

  root.innerHTML = html;
}
refresh();
setInterval(refresh, 5000);
</script>
</body>
</html>`

// Ensure strings is imported (used in dashboardHTML escaping if added later).
var _ = strings.Builder{}

func (h *handlers) testModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "die", "error": "invalid json: " + err.Error()})
		return
	}
	if body.Model == "" {
		writeJSON(w, http.StatusOK, map[string]any{"status": "die", "error": "model is required"})
		return
	}
	token, err := h.deps.TokenPool.Take()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "die", "error": "no upstream tokens available: " + err.Error()})
		return
	}
	start := time.Now()
	// NewChat creates a lightweight chat conversation. Perfect for latency and live/die check.
	chatId, err := h.deps.Qwen.NewChat(r.Context(), token.Value, body.Model, "normal")
	latency := time.Since(start).Milliseconds()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"status": "die", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "live", "latency_ms": latency, "chat_id": chatId})
}
