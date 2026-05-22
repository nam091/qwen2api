package server

import (
	"net/http"

	"github.com/keaume34/qwen2api/internal/openai"
	"github.com/keaume34/qwen2api/internal/qwen"
)

// staticModels is the fallback model list used when the upstream /api/models
// call is unavailable (e.g. no token configured yet).
var staticModels = []string{
	"qwen3.6-plus",
	"qwen3.6-max-preview",
	"qwen3.6-27b",
	"qwen3.6-35b-a3b",
	"qwen3.6-plus-preview",
	"qwen3.5-plus",
	"qwen3.5-flash",
	"qwen3-max",
	"qwen3-max-latest",
	"qwen-max",
	"qwen-max-latest",
	"qwen-plus",
	"qwen-plus-latest",
	"qwen-turbo",
	"qwen3-coder-plus",
	"qwen3-235b-a22b",
}

// standardClaudeModels are injected so Claude Code / Cline / Cursor
// automatically recognize the gateway as supporting them.
var standardClaudeModels = []string{
	"claude-3-7-sonnet-20250219",
	"claude-3.7-sonnet",
	"claude-3-5-sonnet-20241022",
	"claude-3.5-sonnet",
	"claude-3-opus-20240229",
	"claude-3-haiku-20240307",
}

func (h *handlers) listModels(w http.ResponseWriter, r *http.Request) {
	now := unixNow()
	out := openai.ModelList{Object: "list"}
	seen := map[string]bool{}

	// 1. Add upstream models if available
	token, err := h.deps.TokenPool.Take()
	if err == nil {
		resp, errModels := h.deps.Qwen.Models(r.Context(), token.Value)
		if errModels == nil {
			for _, m := range resp.Data {
				if m.ID == "" || seen[m.ID] {
					continue
				}
				seen[m.ID] = true
				out.Data = append(out.Data, openai.Model{
					ID:      m.ID,
					Object:  "model",
					Created: now,
					OwnedBy: "qwen",
				})
			}
		} else {
			h.deps.Logger.Warn("upstream models fetch failed; falling back to static list", "err", errModels)
			if _, ok := errModels.(*qwen.UpstreamError); ok {
				h.deps.TokenPool.MarkBad(token.Value)
			}
		}
	}

	// 2. If no upstream models, fall back to static list
	if len(out.Data) == 0 {
		for _, id := range staticModels {
			if seen[id] {
				continue
			}
			seen[id] = true
			out.Data = append(out.Data, openai.Model{
				ID:      id,
				Object:  "model",
				Created: now,
				OwnedBy: "qwen",
			})
		}
	}

	// 3. Always append standard Claude models so clients (Claude Code, Cline, Cursor) recognize them
	for _, id := range standardClaudeModels {
		if seen[id] {
			continue
		}
		seen[id] = true
		out.Data = append(out.Data, openai.Model{
			ID:      id,
			Object:  "model",
			Created: now,
			OwnedBy: "anthropic",
		})
	}

	// 4. Always append user custom aliases
	if h.deps.Config != nil {
		for alias := range h.deps.Config.ModelAliases {
			if alias == "" || seen[alias] {
				continue
			}
			seen[alias] = true
			out.Data = append(out.Data, openai.Model{
				ID:      alias,
				Object:  "model",
				Created: now,
				OwnedBy: "custom",
			})
		}
	}

	writeJSON(w, http.StatusOK, out)
}
