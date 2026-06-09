package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// errorDashboard returns error tracking dashboard data
func (h *handlers) errorDashboard(w http.ResponseWriter, r *http.Request) {
	if h.deps.ErrorTracker == nil {
		writeError(w, http.StatusServiceUnavailable, "feature_disabled", "Error tracking is not enabled")
		return
	}

	// Get query parameters
	limitStr := r.URL.Query().Get("limit")
	limit := 50 // Default limit
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}

	pathFilter := r.URL.Query().Get("path")
	statusFilter := r.URL.Query().Get("status")

	var errors []ErrorEntry
	if pathFilter != "" {
		errors = h.deps.ErrorTracker.GetErrorsByPath(pathFilter, limit)
	} else if statusFilter != "" {
		if status, err := strconv.Atoi(statusFilter); err == nil {
			errors = h.deps.ErrorTracker.GetErrorsByStatusCode(status, limit)
		} else {
			errors = h.deps.ErrorTracker.GetRecentErrors(limit)
		}
	} else {
		errors = h.deps.ErrorTracker.GetRecentErrors(limit)
	}

	stats := h.deps.ErrorTracker.GetErrorStats()

	response := map[string]interface{}{
		"errors":      errors,
		"stats":       stats,
		"generated_at": time.Now().Format(time.RFC3339),
		"filters": map[string]interface{}{
			"limit":         limit,
			"path":          pathFilter,
			"status_filter": statusFilter,
		},
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.deps.Logger.Error("failed to encode error dashboard response", "error", err.Error())
	}
}

// errorStats returns error statistics
func (h *handlers) errorStats(w http.ResponseWriter, _ *http.Request) {
	if h.deps.ErrorTracker == nil {
		writeError(w, http.StatusServiceUnavailable, "feature_disabled", "Error tracking is not enabled")
		return
	}

	stats := h.deps.ErrorTracker.GetErrorStats()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		h.deps.Logger.Error("failed to encode error stats response", "error", err.Error())
	}
}

// clearErrors clears all tracked errors
func (h *handlers) clearErrors(w http.ResponseWriter, _ *http.Request) {
	if h.deps.ErrorTracker == nil {
		writeError(w, http.StatusServiceUnavailable, "feature_disabled", "Error tracking is not enabled")
		return
	}

	h.deps.ErrorTracker.Clear()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "cleared"}); err != nil {
		h.deps.Logger.Error("failed to encode clear errors response", "error", err.Error())
	}
}

// cleanupErrors removes old error entries
func (h *handlers) cleanupErrors(w http.ResponseWriter, r *http.Request) {
	if h.deps.ErrorTracker == nil {
		writeError(w, http.StatusServiceUnavailable, "feature_disabled", "Error tracking is not enabled")
		return
	}

	// Get max age from query parameter (default: 24 hours)
	maxAgeStr := r.URL.Query().Get("max_age_hours")
	maxAgeHours := 24
	if maxAgeStr != "" {
		if hours, err := strconv.Atoi(maxAgeStr); err == nil && hours > 0 {
			maxAgeHours = hours
		}
	}

	maxAge := time.Duration(maxAgeHours) * time.Hour
	h.deps.ErrorTracker.CleanupOldErrors(maxAge)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status":     "cleaned",
		"max_age_hours": maxAgeHours,
	}); err != nil {
		h.deps.Logger.Error("failed to encode cleanup response", "error", err.Error())
	}
}

// errorDashboardPage serves the error tracking dashboard HTML page
func (h *handlers) errorDashboardPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Serve the embedded HTML template
	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Error Tracking Dashboard - qwen2api</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background: #1a1a2e; color: #eee; min-height: 100vh; padding: 20px; }
        .container { max-width: 1400px; margin: 0 auto; }
        .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 30px; padding-bottom: 20px; border-bottom: 1px solid #333; }
        h1 { font-size: 28px; color: #00d4ff; }
        .controls { display: flex; gap: 10px; align-items: center; }
        button { padding: 10px 20px; border: none; border-radius: 6px; cursor: pointer; font-size: 14px; font-weight: 500; transition: all 0.2s; }
        button.primary { background: #00d4ff; color: #1a1a2e; }
        button.primary:hover { background: #00b8d4; }
        button.secondary { background: #333; color: #eee; }
        button.secondary:hover { background: #444; }
        button.danger { background: #ff4444; color: white; }
        button.danger:hover { background: #cc0000; }
        .stats-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 20px; margin-bottom: 30px; }
        .stat-card { background: #16213e; border-radius: 10px; padding: 20px; text-align: center; }
        .stat-value { font-size: 36px; font-weight: bold; color: #00d4ff; margin-bottom: 5px; }
        .stat-label { font-size: 14px; color: #888; }
        .filters { display: flex; gap: 15px; margin-bottom: 20px; flex-wrap: wrap; }
        .filter-group { display: flex; flex-direction: column; gap: 5px; }
        label { font-size: 12px; color: #888; text-transform: uppercase; }
        select, input { padding: 8px 12px; background: #16213e; border: 1px solid #333; border-radius: 6px; color: #eee; font-size: 14px; }
        .error-table { width: 100%; border-collapse: collapse; background: #16213e; border-radius: 10px; overflow: hidden; }
        .error-table th { background: #0f3460; padding: 15px; text-align: left; font-weight: 600; font-size: 14px; }
        .error-table td { padding: 12px 15px; border-bottom: 1px solid #1a1a2e; font-size: 13px; }
        .error-table tr:hover { background: #1a1a3e; }
        .status-code { display: inline-block; padding: 4px 8px; border-radius: 4px; font-weight: bold; font-size: 12px; }
        .status-4xx { background: #ff9800; color: #000; }
        .status-5xx { background: #f44336; color: white; }
        .error-type { display: inline-block; padding: 4px 8px; background: #333; border-radius: 4px; font-size: 12px; }
        .timestamp { color: #888; font-size: 12px; }
        .message-cell { max-width: 300px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
        .message-cell:hover { white-space: normal; overflow: visible; }
        .empty-state { text-align: center; padding: 60px 20px; color: #888; }
        .loading { text-align: center; padding: 40px; color: #888; }
        .refresh-indicator { font-size: 12px; color: #888; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🚨 Error Tracking Dashboard</h1>
            <div class="controls">
                <span class="refresh-indicator" id="refreshIndicator">Auto-refresh: 30s</span>
                <button class="secondary" onclick="refreshData()">Refresh</button>
                <button class="danger" onclick="clearErrors()">Clear All</button>
                <button class="secondary" onclick="cleanupOldErrors()">Cleanup Old (24h+)</button>
            </div>
        </div>
        <div class="stats-grid" id="statsGrid">
            <div class="stat-card"><div class="stat-value" id="totalErrors">-</div><div class="stat-label">Total Errors</div></div>
            <div class="stat-card"><div class="stat-value" id="errors4xx">-</div><div class="stat-label">4xx Errors</div></div>
            <div class="stat-card"><div class="stat-value" id="errors5xx">-</div><div class="stat-label">5xx Errors</div></div>
            <div class="stat-card"><div class="stat-value" id="topPath">-</div><div class="stat-label">Top Error Path</div></div>
        </div>
        <div class="filters">
            <div class="filter-group"><label>Limit</label><select id="limitFilter" onchange="refreshData()"><option value="50">50</option><option value="100">100</option><option value="200">200</option><option value="500">500</option></select></div>
            <div class="filter-group"><label>Path Filter</label><input type="text" id="pathFilter" placeholder="e.g., /v1/chat/completions" onchange="refreshData()"></div>
            <div class="filter-group"><label>Status Filter</label><select id="statusFilter" onchange="refreshData()"><option value="">All</option><option value="400">400</option><option value="401">401</option><option value="403">403</option><option value="404">404</option><option value="429">429</option><option value="500">500</option><option value="502">502</option><option value="503">503</option></select></div>
        </div>
        <div id="errorTableContainer"><div class="loading">Loading errors...</div></div>
    </div>
    <script>
        let refreshInterval;
        async function fetchErrors() {
            const limit = document.getElementById('limitFilter').value;
            const path = document.getElementById('pathFilter').value;
            const status = document.getElementById('statusFilter').value;
            let url = '/admin/errors?limit=' + limit;
            if (path) url += '&path=' + encodeURIComponent(path);
            if (status) url += '&status=' + status;
            try {
                const response = await fetch(url);
                if (!response.ok) throw new Error('HTTP ' + response.status);
                return await response.json();
            } catch (error) { console.error('Failed to fetch errors:', error); return null; }
        }
        async function fetchStats() {
            try {
                const response = await fetch('/admin/errors/stats');
                if (!response.ok) throw new Error('HTTP ' + response.status);
                return await response.json();
            } catch (error) { console.error('Failed to fetch stats:', error); return null; }
        }
        function updateStats(stats) {
            if (!stats) return;
            document.getElementById('totalErrors').textContent = stats.total_errors || 0;
            let count4xx = 0, count5xx = 0;
            if (stats.by_status) {
                Object.entries(stats.by_status).forEach(([status, count]) => {
                    const code = parseInt(status);
                    if (code >= 400 && code < 500) count4xx += count;
                    if (code >= 500 && code < 600) count5xx += count;
                });
            }
            document.getElementById('errors4xx').textContent = count4xx;
            document.getElementById('errors5xx').textContent = count5xx;
            if (stats.by_path && Object.keys(stats.by_path).length > 0) {
                const topPath = Object.entries(stats.by_path).sort(([,a], [,b]) => b - a)[0][0];
                document.getElementById('topPath').textContent = topPath.length > 20 ? topPath.substring(0, 20) + '...' : topPath;
            } else { document.getElementById('topPath').textContent = '-'; }
        }
        function renderErrorTable(errors) {
            const container = document.getElementById('errorTableContainer');
            if (!errors || errors.length === 0) {
                container.innerHTML = '<div class="empty-state"><h3>No errors found</h3><p>Everything is running smoothly!</p></div>';
                return;
            }
            let html = '<table class="error-table"><thead><tr><th>Timestamp</th><th>Status</th><th>Error Code</th><th>Path</th><th>Message</th><th>Client IP</th><th>Duration</th></tr></thead><tbody>';
            errors.reverse().forEach(error => {
                const timestamp = new Date(error.timestamp).toLocaleString();
                const statusClass = error.status_code >= 500 ? 'status-5xx' : 'status-4xx';
                const duration = error.duration_ms ? error.duration_ms + 'ms' : '-';
                const message = error.message || '-';
                const clientIp = error.client_ip || '-';
                html += '<tr><td class="timestamp">' + timestamp + '</td><td><span class="status-code ' + statusClass + '">' + error.status_code + '</span></td><td><span class="error-type">' + (error.error_code || '-') + '</span></td><td>' + (error.path || '-') + '</td><td class="message-cell" title="' + message + '">' + message + '</td><td>' + clientIp + '</td><td>' + duration + '</td></tr>';
            });
            html += '</tbody></table>';
            container.innerHTML = html;
        }
        async function refreshData() {
            const [data, stats] = await Promise.all([fetchErrors(), fetchStats()]);
            if (data) renderErrorTable(data.errors);
            if (stats) updateStats(stats);
        }
        async function clearErrors() {
            if (!confirm('Are you sure you want to clear all error history?')) return;
            try {
                const response = await fetch('/admin/errors', { method: 'DELETE' });
                if (response.ok) await refreshData();
                else alert('Failed to clear errors');
            } catch (error) { console.error('Failed to clear errors:', error); alert('Failed to clear errors'); }
        }
        async function cleanupOldErrors() {
            try {
                const response = await fetch('/admin/errors/cleanup', { method: 'POST' });
                if (response.ok) await refreshData();
                else alert('Failed to cleanup errors');
            } catch (error) { console.error('Failed to cleanup errors:', error); alert('Failed to cleanup errors'); }
        }
        refreshData();
        refreshInterval = setInterval(refreshData, 30000);
        setInterval(() => {
            const indicator = document.getElementById('refreshIndicator');
            indicator.textContent = 'Last updated: ' + new Date().toLocaleTimeString();
        }, 1000);
    </script>
</body>
</html>`

	_, _ = w.Write([]byte(html))
}

// registerErrorRoutes registers error tracking API routes
func registerErrorRoutes(r chi.Router, h *handlers) {
	r.Route("/admin/errors", func(r chi.Router) {
		r.Use(h.adminMiddleware)
		r.Get("/", h.errorDashboard)
		r.Get("/dashboard", h.errorDashboardPage)
		r.Get("/stats", h.errorStats)
		r.Delete("/", h.clearErrors)
		r.Post("/cleanup", h.cleanupErrors)
	})
}
