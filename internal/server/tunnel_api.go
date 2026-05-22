package server

import (
	"encoding/json"
	"net/http"
)

// getTunnelStatus returns the current tunnel state.
func (h *handlers) getTunnelStatus(w http.ResponseWriter, _ *http.Request) {
	if h.deps.TunnelManager == nil || !h.deps.Config.Features.Tunnel {
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": false,
			"running": false,
			"message": "Tunnel manager not initialized. Restart server with tunnel feature enabled to use tunnels.",
		})
		return
	}

	running, url, port := h.deps.TunnelManager.Status()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": true,
		"running": running,
		"url":     url,
		"port":    port,
	})
}

func (h *handlers) startTunnel(w http.ResponseWriter, r *http.Request) {
	if h.deps.TunnelManager == nil || !h.deps.Config.Features.Tunnel {
		writeError(w, http.StatusServiceUnavailable, "tunnel_unavailable", "Tunnel manager not initialized. Restart server with tunnel feature enabled in config.")
		return
	}

	var body struct {
		Port int `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON: "+err.Error())
		return
	}

	if body.Port == 0 {
		body.Port = h.deps.Config.Port
	}

	url, err := h.deps.TunnelManager.Start(body.Port)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tunnel_start_failed", err.Error())
		return
	}

	h.deps.Logger.Info("tunnel started via API", "url", url, "port", body.Port)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"url":     url,
		"port":    body.Port,
	})
}

// stopTunnel stops the cloudflared tunnel.
func (h *handlers) stopTunnel(w http.ResponseWriter, _ *http.Request) {
	if h.deps.TunnelManager == nil || !h.deps.Config.Features.Tunnel {
		writeError(w, http.StatusServiceUnavailable, "tunnel_unavailable", "Tunnel manager not initialized. Restart server with tunnel feature enabled in config.")
		return
	}

	if err := h.deps.TunnelManager.Stop(); err != nil {
		writeError(w, http.StatusInternalServerError, "tunnel_stop_failed", err.Error())
		return
	}

	h.deps.Logger.Info("tunnel stopped via API")
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
	})
}
