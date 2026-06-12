package toolcall

import (
	"context"
	"fmt"
	"strings"

	"github.com/keaume34/qwen2api/internal/openai"
)

// MaxContinuationRounds giới hạn số lần xin tiếp để tránh loop vô hạn.
const MaxContinuationRounds = 2

// MaxFixRounds giới hạn số lần fix tool call lỗi.
const MaxFixRounds = 2

// ContinuationPrompt là chỉ thị gửi kèm khi xin model viết tiếp block dở.
const ContinuationPrompt = "Lượt trước của bạn bị cắt giữa một khối <tool_call>. " +
	"Chỉ xuất phần JSON còn thiếu và </tool_call> để hoàn tất block. " +
	"Không lặp lại nội dung đã in, không thêm prose."

// FixToolCallPrompt là prompt gửi về Qwen khi tool call bị lỗi.
// Chỉ gửi đoạn tool call lỗi, không gửi toàn bộ history.
const FixToolCallPrompt = "Tool call bên dưới bị lỗi (sai tên tool hoặc JSON không hợp lệ). " +
	"Hãy sửa lại và trả về đúng format <tool_call> với tên tool hợp lệ và JSON đúng. " +
	"Chỉ trả về 1 block <tool_call>, không thêm giải thích:\n\n%s"

// Continuer trừu tượng hóa một lượt gọi upstream non-stream, trả về text thô.
type Continuer interface {
	ContinueOnce(ctx context.Context, priorText string) (string, error)
}

// stripDuplicateToolCallTags removes duplicate/repeated <tool_call> opening
// tags that the model may emit during continuation. When the model repeats
// the opening tag instead of continuing the JSON body, naive concatenation
// produces nested or duplicate blocks that the parser cannot handle.
//
// Strategy: find the FIRST unclosed <tool_call>, then remove any subsequent
// <tool_call> tags that appear before the matching </tool_call>.
func stripDuplicateToolCallTags(s string) string {
	const openTag = "<tool_call>"
	const closeTag = "</tool_call>"

	// Find first unclosed <tool_call>
	firstOpen := strings.Index(s, openTag)
	if firstOpen < 0 {
		return s
	}
	// Check if there's already a closing tag after the first open
	firstClose := strings.Index(s[firstOpen:], closeTag)
	if firstClose >= 0 {
		// Already closed — check for duplicates AFTER the close
		absClose := firstOpen + firstClose + len(closeTag)
		remainder := s[absClose:]
		// Remove any stray duplicate <tool_call>...</tool_call> blocks
		// that the continuation appended
		for {
			nextOpen := strings.Index(remainder, openTag)
			if nextOpen < 0 {
				break
			}
			nextClose := strings.Index(remainder[nextOpen:], closeTag)
			if nextClose < 0 {
				// Unclosed duplicate — remove everything from this point
				remainder = remainder[:nextOpen]
				break
			}
			// Remove this duplicate block entirely
			absNextClose := nextOpen + nextClose + len(closeTag)
			remainder = remainder[:nextOpen] + remainder[absNextClose:]
		}
		return s[:absClose] + remainder
	}

	// No closing tag found yet — look for duplicate opens in the unclosed portion
	unclosed := s[firstOpen+len(openTag):]
	for {
		dupOpen := strings.Index(unclosed, openTag)
		if dupOpen < 0 {
			break
		}
		// Remove the duplicate opening tag but keep content after it
		unclosed = unclosed[:dupOpen] + unclosed[dupOpen+len(openTag):]
	}
	return s[:firstOpen+len(openTag)] + unclosed
}

// ResolveTruncatedToolCall: nếu accumulated có <tool_call> dở dang, xin model
// viết tiếp tối đa MaxContinuationRounds lần rồi parse lại trên text đã nối.
// Sau mỗi lần nối, strip duplicate tags để tránh JSON invalid.
func ResolveTruncatedToolCall(
	ctx context.Context,
	accumulated string,
	multiFormat bool,
	c Continuer,
) (ParseResult, bool) {
	if c == nil {
		return ParseWithFormats(accumulated, multiFormat), false
	}
	full := accumulated
	for round := 0; round < MaxContinuationRounds; round++ {
		if !HasUnclosedToolCall(full) {
			break
		}
		more, err := c.ContinueOnce(ctx, full)
		if err != nil || strings.TrimSpace(more) == "" {
			break
		}
		full += more
		// Clean up duplicate/malformed tags introduced by continuation
		full = stripDuplicateToolCallTags(full)
	}
	res := ParseWithFormats(full, multiFormat)
	resolved := len(res.ToolCalls) > 0 && !HasUnclosedToolCall(full)
	return res, resolved
}

// FixMalformedToolCall gửi tool call lỗi về Qwen để sửa lại.
// Chỉ gửi đoạn tool call bị lỗi, không gửi toàn bộ history.
// Trả về tool call đã được sửa, hoặc nil nếu không fix được.
func FixMalformedToolCall(
	ctx context.Context,
	brokenCall string,
	clientTools []openai.Tool,
	c Continuer,
) *openai.ToolCall {
	if c == nil || brokenCall == "" {
		return nil
	}

	// Build list of valid tool names for the prompt
	var validNames []string
	for _, t := range clientTools {
		validNames = append(validNames, t.Function.Name)
	}

	prompt := fmt.Sprintf(FixToolCallPrompt, brokenCall)
	if len(validNames) > 0 {
		prompt += "\nCác tool names hợp lệ: " + strings.Join(validNames, ", ")
	}

	fixed, err := c.ContinueOnce(ctx, prompt)
	if err != nil || strings.TrimSpace(fixed) == "" {
		return nil
	}

	// Parse the fixed response
	result := ParseWithFormats(fixed, true)
	if len(result.ToolCalls) == 0 {
		return nil
	}

	// Validate the fixed tool call against client tools
	fixedCall := result.ToolCalls[0]
	if len(clientTools) > 0 {
		validMap := make(map[string]bool, len(clientTools))
		for _, t := range clientTools {
			validMap[t.Function.Name] = true
		}
		// Try exact match
		if validMap[fixedCall.Function.Name] {
			return &fixedCall
		}
		// Try deobfuscated name
		deobfuscated := fromQwenName(fixedCall.Function.Name)
		if deobfuscated != fixedCall.Function.Name && validMap[deobfuscated] {
			fixedCall.Function.Name = deobfuscated
			return &fixedCall
		}
		return nil
	}

	return &fixedCall
}

// FixMalformedToolCalls phát hiện và sửa các tool call bị lỗi.
// Trả về danh sách tool calls đã được sửa (kể cả những cái không lỗi).
func FixMalformedToolCalls(
	ctx context.Context,
	calls []openai.ToolCall,
	rawText string,
	clientTools []openai.Tool,
	c Continuer,
) []openai.ToolCall {
	if c == nil || len(calls) == 0 {
		return calls
	}

	var fixed []openai.ToolCall
	for _, call := range calls {
		// Check if this call has issues
		hasIssue := false
		
		// Check for malformed JSON arguments
		if call.Function.Arguments != "" && call.Function.Arguments != "{}" {
			// Try to parse arguments
			if !isValidJSON(call.Function.Arguments) {
				hasIssue = true
			}
		}
		
		// Check for empty name
		if call.Function.Name == "" {
			hasIssue = true
		}

		if !hasIssue {
			fixed = append(fixed, call)
			continue
		}

		// Try to fix this call
		brokenJSON := fmt.Sprintf(`{"name": "%s", "arguments": %s}`, call.Function.Name, call.Function.Arguments)
		fixedCall := FixMalformedToolCall(ctx, brokenJSON, clientTools, c)
		if fixedCall != nil {
			fixed = append(fixed, *fixedCall)
		}
		// If we can't fix it, skip it (don't include the broken call)
	}

	return fixed
}

// fromQwenName is a helper that strips the u_ prefix if present.
func fromQwenName(name string) string {
	const autoPrefix = "u_"
	if len(name) >= len(autoPrefix) && name[:len(autoPrefix)] == autoPrefix {
		return name[len(autoPrefix):]
	}
	return name
}

// isValidJSON checks if a string is valid JSON.
func isValidJSON(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Quick check: must start with { or [
	if s[0] != '{' && s[0] != '[' {
		return false
	}
	// Use a simple bracket counter
	depth := 0
	inString := false
	escaped := false
	for _, ch := range s {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' || ch == '[' {
			depth++
		} else if ch == '}' || ch == ']' {
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0 && !inString
}
