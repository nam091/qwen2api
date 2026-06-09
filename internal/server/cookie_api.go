package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// CookieStore stores the latest cookies from the browser.
type CookieStore struct {
	mu        sync.RWMutex
	cookies   map[string]string
	updatedAt time.Time
}

// NewCookieStore creates a new CookieStore.
func NewCookieStore() *CookieStore {
	return &CookieStore{
		cookies: make(map[string]string),
	}
}

// Get returns the stored cookies.
func (cs *CookieStore) Get() map[string]string {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	result := make(map[string]string)
	for k, v := range cs.cookies {
		result[k] = v
	}
	return result
}

// Set updates the stored cookies.
func (cs *CookieStore) Set(cookies map[string]string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.cookies = cookies
	cs.updatedAt = time.Now()
}

// GetUpdatedAt returns when cookies were last updated.
func (cs *CookieStore) GetUpdatedAt() time.Time {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.updatedAt
}

// CookieUpdateRequest is the request body for updating cookies.
type CookieUpdateRequest struct {
	Cookies map[string]string `json:"cookies"`
}

// CookieUpdateResponse is the response for cookie updates.
type CookieUpdateResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	UpdatedAt string `json:"updated_at"`
}

func registerCookieRoutes(r chi.Router, h *handlers) {
	r.Route("/admin/cookies", func(r chi.Router) {
		r.Use(h.adminMiddleware)
		r.Get("/", h.getCookies)
		r.Post("/", h.updateCookies)
		r.Get("/status", h.cookieStatus)
	})
}

func (h *handlers) getCookies(w http.ResponseWriter, r *http.Request) {
	cookies := h.deps.CookieStore.Get()
	updatedAt := h.deps.CookieStore.GetUpdatedAt()

	resp := map[string]interface{}{
		"cookies":    cookies,
		"updated_at": updatedAt.Format(time.RFC3339),
		"is_empty":   len(cookies) == 0,
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *handlers) updateCookies(w http.ResponseWriter, r *http.Request) {
	var req CookieUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
		return
	}

	if len(req.Cookies) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "cookies must not be empty")
		return
	}

	h.deps.CookieStore.Set(req.Cookies)

	// Update the config with the new cookies
	if ssxmodItNa, ok := req.Cookies["ssxmod_itna"]; ok {
		h.deps.Config.SsxmodItna = ssxmodItNa
	}
	if ssxmodItNa2, ok := req.Cookies["ssxmod_itna2"]; ok {
		h.deps.Config.SsxmodItna2 = ssxmodItNa2
	}

	h.deps.Logger.Info("cookies updated",
		"count", len(req.Cookies),
		"updated_at", h.deps.CookieStore.GetUpdatedAt().Format(time.RFC3339),
	)

	resp := CookieUpdateResponse{
		Success:   true,
		Message:   "cookies updated successfully",
		UpdatedAt: h.deps.CookieStore.GetUpdatedAt().Format(time.RFC3339),
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *handlers) cookieStatus(w http.ResponseWriter, r *http.Request) {
	cookies := h.deps.CookieStore.Get()
	updatedAt := h.deps.CookieStore.GetUpdatedAt()

	resp := map[string]interface{}{
		"has_cookies": len(cookies) > 0,
		"updated_at":  updatedAt.Format(time.RFC3339),
		"age_seconds": int(time.Since(updatedAt).Seconds()),
	}

	writeJSON(w, http.StatusOK, resp)
}
