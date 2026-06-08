package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// ConversationResponse represents a conversation in API responses.
type ConversationResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// MessageResponse represents a message in API responses.
type MessageResponse struct {
	ID             int64  `json:"id"`
	ConversationID string `json:"conversation_id"`
	Role           string `json:"role"`
	Content        string `json:"content"`
	CreatedAt      string `json:"created_at"`
}

// CreateConversationRequest is the request body for creating a conversation.
type CreateConversationRequest struct {
	Title string `json:"title"`
	Model string `json:"model"`
}

// UpdateConversationRequest is the request body for updating a conversation.
type UpdateConversationRequest struct {
	Title string `json:"title,omitempty"`
	Model string `json:"model,omitempty"`
}

// createConversation handles POST /v1/conversations
func (h *handlers) createConversation(w http.ResponseWriter, r *http.Request) {
	if h.deps.Database == nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "conversation memory is not enabled")
		return
	}

	var req CreateConversationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
		return
	}

	if req.Model == "" {
		req.Model = "qwen3.7-plus"
	}

	store := h.deps.Database
	conv, err := store.CreateConversation(req.Title, req.Model)
	if err != nil {
		h.deps.Logger.Error("create conversation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to create conversation")
		return
	}

	resp := ConversationResponse{
		ID:        conv.ID,
		Title:     conv.Title,
		Model:     conv.Model,
		CreatedAt: conv.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: conv.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}

	writeJSON(w, http.StatusCreated, resp)
}

// listConversations handles GET /v1/conversations
func (h *handlers) listConversations(w http.ResponseWriter, r *http.Request) {
	if h.deps.Database == nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "conversation memory is not enabled")
		return
	}

	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	limit := 50
	offset := 0

	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	store := h.deps.Database
	conversations, err := store.ListConversations(limit, offset)
	if err != nil {
		h.deps.Logger.Error("list conversations failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to list conversations")
		return
	}

	resp := make([]ConversationResponse, len(conversations))
	for i, conv := range conversations {
		resp[i] = ConversationResponse{
			ID:        conv.ID,
			Title:     conv.Title,
			Model:     conv.Model,
			CreatedAt: conv.CreatedAt.Format("2006-01-02T15:04:05Z"),
			UpdatedAt: conv.UpdatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// getConversation handles GET /v1/conversations/{id}
func (h *handlers) getConversation(w http.ResponseWriter, r *http.Request) {
	if h.deps.Database == nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "conversation memory is not enabled")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "conversation id is required")
		return
	}

	store := h.deps.Database
	conv, err := store.GetConversation(id)
	if err != nil {
		h.deps.Logger.Error("get conversation failed", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get conversation")
		return
	}
	if conv == nil {
		writeError(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}

	resp := ConversationResponse{
		ID:        conv.ID,
		Title:     conv.Title,
		Model:     conv.Model,
		CreatedAt: conv.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: conv.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}

	writeJSON(w, http.StatusOK, resp)
}

// updateConversation handles PUT /v1/conversations/{id}
func (h *handlers) updateConversation(w http.ResponseWriter, r *http.Request) {
	if h.deps.Database == nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "conversation memory is not enabled")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "conversation id is required")
		return
	}

	var req UpdateConversationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "invalid JSON body: "+err.Error())
		return
	}

	store := h.deps.Database

	// Check if conversation exists
	conv, err := store.GetConversation(id)
	if err != nil {
		h.deps.Logger.Error("get conversation failed", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get conversation")
		return
	}
	if conv == nil {
		writeError(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}

	// Update fields
	if req.Title != "" {
		if err := store.UpdateConversationTitle(id, req.Title); err != nil {
			h.deps.Logger.Error("update conversation title failed", "id", id, "err", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to update conversation")
			return
		}
	}
	if req.Model != "" {
		if err := store.UpdateConversationModel(id, req.Model); err != nil {
			h.deps.Logger.Error("update conversation model failed", "id", id, "err", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to update conversation")
			return
		}
	}

	// Fetch updated conversation
	conv, err = store.GetConversation(id)
	if err != nil {
		h.deps.Logger.Error("get updated conversation failed", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get updated conversation")
		return
	}

	resp := ConversationResponse{
		ID:        conv.ID,
		Title:     conv.Title,
		Model:     conv.Model,
		CreatedAt: conv.CreatedAt.Format("2006-01-02T15:04:05Z"),
		UpdatedAt: conv.UpdatedAt.Format("2006-01-02T15:04:05Z"),
	}

	writeJSON(w, http.StatusOK, resp)
}

// deleteConversation handles DELETE /v1/conversations/{id}
func (h *handlers) deleteConversation(w http.ResponseWriter, r *http.Request) {
	if h.deps.Database == nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "conversation memory is not enabled")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "conversation id is required")
		return
	}

	store := h.deps.Database
	if err := store.DeleteConversation(id); err != nil {
		h.deps.Logger.Error("delete conversation failed", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to delete conversation")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"deleted": id})
}

// getConversationMessages handles GET /v1/conversations/{id}/messages
func (h *handlers) getConversationMessages(w http.ResponseWriter, r *http.Request) {
	if h.deps.Database == nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "conversation memory is not enabled")
		return
	}

	id := chi.URLParam(r, "id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "conversation id is required")
		return
	}

	store := h.deps.Database

	// Check if conversation exists
	exists, err := store.ConversationExists(id)
	if err != nil {
		h.deps.Logger.Error("check conversation exists failed", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to check conversation")
		return
	}
	if !exists {
		writeError(w, http.StatusNotFound, "not_found", "conversation not found")
		return
	}

	messages, err := store.GetMessages(id)
	if err != nil {
		h.deps.Logger.Error("get messages failed", "id", id, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to get messages")
		return
	}

	resp := make([]MessageResponse, len(messages))
	for i, msg := range messages {
		resp[i] = MessageResponse{
			ID:             msg.ID,
			ConversationID: msg.ConversationID,
			Role:           msg.Role,
			Content:        msg.Content,
			CreatedAt:      msg.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	writeJSON(w, http.StatusOK, resp)
}