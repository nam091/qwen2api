package openai

import "encoding/json"

// ResponsesRequest is the POST /v1/responses request body.
type ResponsesRequest struct {
	Model              string            `json:"model"`
	Input              json.RawMessage   `json:"input"` // string or []ResponseInputItem
	Instructions       string            `json:"instructions,omitempty"`
	Stream             bool              `json:"stream,omitempty"`
	Temperature        *float64          `json:"temperature,omitempty"`
	TopP               *float64          `json:"top_p,omitempty"`
	MaxOutputTokens    *int              `json:"max_output_tokens,omitempty"`
	Tools              []ResponseTool    `json:"tools,omitempty"`
	PreviousResponseID string            `json:"previous_response_id,omitempty"`
	Truncation         string            `json:"truncation,omitempty"`
}

// ResponseInputItem represents an input item in the Responses API.
type ResponseInputItem struct {
	Type    string          `json:"type"` // "message", "function_call_output"
	Role    string          `json:"role,omitempty"`
	Content json.RawMessage `json:"content,omitempty"` // string or []ContentPart
	CallID  string          `json:"call_id,omitempty"`
	Output  string          `json:"output,omitempty"`
}

// ResponseTool defines a tool for the Responses API.
type ResponseTool struct {
	Type        string          `json:"type"` // "function"
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ResponseObject is the main response envelope for /v1/responses.
type ResponseObject struct {
	ID                string               `json:"id"`
	Object            string               `json:"object"` // "response"
	CreatedAt         int64                `json:"created_at"`
	Status            string               `json:"status"` // "completed", "in_progress", "failed"
	Model             string               `json:"model"`
	Output            []ResponseOutputItem `json:"output"`
	Usage             ResponseUsage        `json:"usage,omitempty"`
	IncompleteDetails *struct {
		Reasoning string `json:"reasoning,omitempty"`
	} `json:"incomplete_details,omitempty"`
	Error *ErrorBody `json:"error,omitempty"`
}

// ResponseOutputItem can be a message or function_call.
type ResponseOutputItem struct {
	Type      string                 `json:"type"` // "message", "function_call"
	ID        string                 `json:"id,omitempty"`
	Status    string                 `json:"status,omitempty"`
	Role      string                 `json:"role,omitempty"`
	Content   []ResponseContentBlock `json:"content,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
}

// ResponseContentBlock is a content block within an output item.
type ResponseContentBlock struct {
	Type string `json:"type"` // "output_text"
	Text string `json:"text"`
}

// ResponseUsage carries token accounting for the Responses API.
type ResponseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ResponseStreamEvent is an SSE event for streaming responses.
type ResponseStreamEvent struct {
	Type         string                `json:"type"`
	Response     *ResponseObject       `json:"response,omitempty"`
	Item         *ResponseOutputItem   `json:"item,omitempty"`
	Part         *ResponseContentBlock `json:"part,omitempty"`
	Delta        string                `json:"delta,omitempty"`
	Text         string                `json:"text,omitempty"`
	Arguments    string                `json:"arguments,omitempty"`
	OutputIndex  int                   `json:"output_index,omitempty"`
	ContentIndex int                   `json:"content_index,omitempty"`
	ItemID       string                `json:"item_id,omitempty"`
}
