package qwen

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
)

// StreamEvent is a parsed line from upstream SSE.
type StreamEvent struct {
	Delta *StreamDelta
	Raw   string
	Done  bool
	// Comment is set when the upstream emitted an SSE comment line
	// (e.g. `: keepalive`). Forwarding these through preserves the
	// upstream's natural pacing so proxies don't time out idle conns.
	Comment string
}

// ReadStream parses one upstream SSE event per call. Returns io.EOF when the
// stream ends naturally.
type StreamReader struct {
	s *bufio.Scanner
}

// NewStreamReader wraps r as an SSE parser sized for long responses.
// The buffer ceiling is intentionally large (32 MiB) so a single fat
// SSE event — e.g. a model emitting tens of KB of code in one chunk —
// can't trigger bufio.ErrTooLong and abruptly kill the stream.
func NewStreamReader(r io.Reader) *StreamReader {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	return &StreamReader{s: scanner}
}

// Next returns the next event. The returned Delta may be nil when the event
// was a keep-alive or non-data line; in that case Raw is populated.
func (r *StreamReader) Next() (StreamEvent, error) {
	for r.s.Scan() {
		line := r.s.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") {
			// SSE comment / keep-alive — surface it so the caller can
			// forward an idle-signal to the downstream client.
			return StreamEvent{Comment: strings.TrimSpace(line[1:]), Raw: line}, nil
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			return StreamEvent{Done: true, Raw: data}, nil
		}
		delta := &StreamDelta{}
		if err := json.Unmarshal([]byte(data), delta); err != nil {
			// Don't fail the whole stream on a single malformed chunk.
			// Log it for debugging instead of silently dropping.
			slog.Debug("malformed SSE chunk", "data", data, "err", err)
			return StreamEvent{Raw: data}, nil
		}
		return StreamEvent{Delta: delta, Raw: data}, nil
	}
	if err := r.s.Err(); err != nil {
		return StreamEvent{}, err
	}
	return StreamEvent{}, io.EOF
}

// ErrStreamClosed is returned when consumers try to read after Close.
var ErrStreamClosed = errors.New("stream closed")
