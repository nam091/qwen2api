package toolcall

import (
	"context"
	"strings"
)

// MaxContinuationRounds giới hạn số lần xin tiếp để tránh loop vô hạn.
const MaxContinuationRounds = 2

// ContinuationPrompt là chỉ thị gửi kèm khi xin model viết tiếp block dở.
const ContinuationPrompt = "Lượt trước của bạn bị cắt giữa một khối <tool_call>. " +
	"Chỉ xuất phần JSON còn thiếu và </tool_call> để hoàn tất block. " +
	"Không lặp lại nội dung đã in, không thêm prose."

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
