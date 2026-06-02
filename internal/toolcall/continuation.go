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

// ResolveTruncatedToolCall: nếu accumulated có <tool_call> dở dang, xin model
// viết tiếp tối đa MaxContinuationRounds lần rồi parse lại trên text đã nối.
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
	}
	res := ParseWithFormats(full, multiFormat)
	resolved := len(res.ToolCalls) > 0 && !HasUnclosedToolCall(full)
	return res, resolved
}
