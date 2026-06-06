package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/keaume34/qwen2api/internal/qwen"
)

// streamPacer is the canonical async wrapper around qwen.StreamReader.
//
// Why it exists: when the model spends a long time in the "thinking" phase
// (or any phase where qwen.ai pauses output), the gateway emits zero SSE
// frames to the client. Reverse proxies (Cloudflare quick tunnel, nginx,
// codex CLI's idle read timer, ...) interpret that silence as a dead
// connection and tear it down — which from the user's POV looks like the
// model "got cut off suddenly".
//
// streamPacer fixes that by:
//   1. Running reader.Next() in a goroutine so the main loop can also
//      service a ticker and the request context.
//   2. Emitting a `: keepalive` SSE comment line every `keepAlive`
//      duration when no real frame has been written by the handler.
//   3. Surfacing context cancellation so handlers exit cleanly on client
//      disconnect.
//
// Because http.ResponseWriter is NOT safe for concurrent writes, the
// keep-alive writes ARE done from the main loop (via the OnKeepAlive
// callback), not from the reader goroutine. The reader goroutine only
// pushes parsed events onto a buffered channel.
type streamPacer struct {
	reader   *qwen.StreamReader
	events   chan pacerEvent
	keepIntv time.Duration
}

type pacerEvent struct {
	Evt qwen.StreamEvent
	Err error
}

// newStreamPacer kicks off the reader goroutine immediately.
// keepAlive=0 disables the keepalive ticker.
func newStreamPacer(r *qwen.StreamReader, keepAlive time.Duration) *streamPacer {
	p := &streamPacer{
		reader:   r,
		events:   make(chan pacerEvent, 256), // Large buffer prevents reader goroutine blocking under fast model output
		keepIntv: keepAlive,
	}
	go p.run()
	return p
}

func (p *streamPacer) run() {
	defer close(p.events)
	for {
		evt, err := p.reader.Next()
		p.events <- pacerEvent{Evt: evt, Err: err}
		if err != nil {
			return
		}
	}
}

// loop drives the pacer. onEvent is called for every parsed event
// (including SSE comments — caller may forward them). onKeepAlive is
// invoked from the same goroutine, so writing into the same
// ResponseWriter is safe and ordered.
//
// Returns when the upstream returns io.EOF (nil), a non-EOF read error
// (the error), the ctx is cancelled (ctx.Err()), or onEvent returns a
// non-nil error (passed through verbatim).
func (p *streamPacer) loop(
	ctx context.Context,
	onEvent func(qwen.StreamEvent) error,
	onKeepAlive func(),
) error {
	var ticker *time.Ticker
	var tickC <-chan time.Time
	if p.keepIntv > 0 && onKeepAlive != nil {
		ticker = time.NewTicker(p.keepIntv)
		defer ticker.Stop()
		tickC = ticker.C
	}

	lastEmit := time.Now()
	for {
		select {
		case <-ctx.Done():
			slog.Debug("stream pacer: context cancelled", "err", ctx.Err())
			return ctx.Err()
		case res, ok := <-p.events:
			if !ok {
				slog.Debug("stream pacer: event channel closed (upstream EOF)")
				return nil
			}
			if res.Err == io.EOF {
				return nil
			}
			if res.Err != nil {
				// Classify the error for observability.
				errStr := res.Err.Error()
				isTransient := isTransientStreamError(res.Err)
				slog.Warn("stream pacer: upstream read error",
					"err", errStr,
					"transient", isTransient,
					"since_last_emit", time.Since(lastEmit).Round(time.Millisecond),
				)
				return res.Err
			}
			if err := onEvent(res.Evt); err != nil {
				// Client-side write error (disconnect, broken pipe).
				slog.Debug("stream pacer: client write error", "err", err)
				return err
			}
			lastEmit = time.Now()
			if res.Evt.Done {
				return nil
			}
		case <-tickC:
			if time.Since(lastEmit) >= p.keepIntv {
				onKeepAlive()
				lastEmit = time.Now()
			}
		}
	}
}

// writeSSEKeepAlive writes a `: keepalive` SSE comment to w and flushes.
// SSE comments are valid in OpenAI chat-completions, Responses, and Claude
// Messages streams — all of those formats are SSE-based and consumers
// ignore lines starting with `:`.
func writeSSEKeepAlive(w http.ResponseWriter, flusher http.Flusher) {
	// Include a timestamp so the comment is unique each time — some
	// intermediaries dedupe identical chunks aggressively.
	_, _ = fmt.Fprintf(w, ": keepalive %d\n\n", time.Now().Unix())
	if flusher != nil {
		flusher.Flush()
	}
}

// defaultStreamKeepAlive is the gap between keepalive comments. Chosen
// well under common idle-timeouts: codex CLI uses ~60s, Cloudflare
// quick-tunnel kills idle conns after ~100s, and most reverse proxies
// (nginx, traefik) default to 60s. 5s leaves ample headroom.
const defaultStreamKeepAlive = 5 * time.Second

// EffectiveKeepAlive returns the configured keepalive if > 0, otherwise
// the default. This allows runtime override via QWEN2API_STREAM_KEEPALIVE_SECONDS.
func EffectiveKeepAlive(configuredSeconds int) time.Duration {
	if configuredSeconds > 0 {
		return time.Duration(configuredSeconds) * time.Second
	}
	return defaultStreamKeepAlive
}

// isTransientStreamError returns true if the error is likely a temporary
// network issue that could succeed on retry (connection reset, timeout,
// DNS failure, etc.) as opposed to a permanent error (auth failure, 4xx).
func isTransientStreamError(err error) bool {
	if err == nil {
		return false
	}
	// Network-level errors are transient.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// Connection reset / broken pipe / ECONNREFUSED.
	if errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	// String-based fallback for wrapped errors that don't unwrap cleanly.
	s := strings.ToLower(err.Error())
	for _, kw := range []string{
		"connection reset", "broken pipe", "eof",
		"timeout", "timed out", "dns", "refused",
		"connection closed", "stream error", "http2:",
	} {
		if strings.Contains(s, kw) {
			return true
		}
	}
	return false
}

// preflightChatIDReuse handles a subtle but high-impact upstream quirk:
// chat.qwen.ai returns an EMPTY stream (zero content delta, immediate
// finish_reason="stop", roughly 0.25s) whenever you POST a completion on
// a chat_id that has already been used for a prior completion. Our cache
// layer (prompt cache + conversation continuity) routinely reuses
// chat_ids to skip the /chats/new round-trip — so without intervention,
// any cached request silently returns nothing to the client.
//
// preflightChatIDReuse buffers upstream bytes via a TeeReader, walks the
// SSE events with a small look-ahead until we either:
//   - see an actual content delta -> safe to forward; returns a
//     "replay" io.ReadCloser that hands back the buffered bytes first,
//     then continues to read fresh bytes from the upstream.
//   - hit finish_reason or [DONE] with no content -> the upstream is
//     empty; returns isEmpty=true so the caller can invalidate cache,
//     re-issue NewChat, and retry the completion before the client sees
//     any of this.
//
// Look-ahead is bounded by maxEvents and maxBufferBytes so a pathological
// upstream cannot wedge the request. If the bound is hit before we can
// decide, we return the buffered body as-is (isEmpty=false) so the
// downstream path runs unchanged.
//
// Only call when the chat_id was reused (cacheHit=true). For fresh
// chat_ids the upstream cannot be in the broken state, so skipping the
// preflight saves first-byte latency.
func preflightChatIDReuse(body io.ReadCloser) (replay io.ReadCloser, isEmpty bool, err error) {
	r, e, _ := preflightChatIDReuseDebug(body)
	return r, e, nil
}

// preflightChatIDReuseDebug is the same as preflightChatIDReuse but also
// returns a short debug summary of what the preflight observed
// (event count, flags, and a preview of buffered bytes). Used by
// handlers for slog visibility when the empty branch (or its absence)
// needs explaining.
func preflightChatIDReuseDebug(body io.ReadCloser) (replay io.ReadCloser, isEmpty bool, debug string) {
	const (
		maxEvents      = 6
		maxBufferBytes = 64 * 1024
	)
	var buf bytes.Buffer
	limited := &capturedReader{src: body, sink: &buf, cap: maxBufferBytes}
	reader := qwen.NewStreamReader(limited)

	sawContent := false
	sawFinish := false
	sawDone := false
	events := 0
	for events < maxEvents {
		evt, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return newReplayBody(buf.Bytes(), body), false,
				fmt.Sprintf("events=%d err=%q preview=%q", events, e.Error(), shortBytes(buf.Bytes()))
		}
		events++
		if evt.Done {
			sawDone = true
			break
		}
		if evt.Comment != "" {
			continue
		}
		if evt.Delta == nil || len(evt.Delta.Choices) == 0 {
			continue
		}
		ch := evt.Delta.Choices[0]
		if ch.Delta.Content != "" {
			sawContent = true
			break
		}
		if ch.FinishReason != nil {
			sawFinish = true
		}
		if buf.Len() >= maxBufferBytes {
			break
		}
	}

	// qwen.ai surfaces chat_id-reuse failures in two different shapes
	// depending on internal state:
	//   1) SSE-shaped: events with empty deltas + finish_reason="stop"
	//      and a final [DONE] (sawFinish/sawDone branch below).
	//   2) Non-SSE JSON envelope on a 200 OK ("the chat is not exist",
	//      no "data:" prefix). The Scanner consumes the line but
	//      can't parse it as an event, so we observe events==0 with a
	//      non-empty buffer (the JSON body) and EOF.
	// Both must be treated as "empty on reuse" so the caller retries.
	bodyBytes := buf.Bytes()
	nonSSEPayload := events == 0 && len(bytes.TrimSpace(bodyBytes)) > 0
	dbg := fmt.Sprintf("events=%d content=%v finish=%v done=%v nonsse=%v preview=%q",
		events, sawContent, sawFinish, sawDone, nonSSEPayload, shortBytes(bodyBytes))
	if !sawContent && (sawFinish || sawDone || nonSSEPayload) {
		_ = body.Close()
		return nil, true, dbg
	}
	return newReplayBody(bodyBytes, body), false, dbg
}

func shortBytes(b []byte) string {
	if len(b) > 256 {
		b = b[:256]
	}
	return string(b)
}

// capturedReader is io.TeeReader-equivalent with an explicit byte cap.
// Once cap is reached, reads pass through to src without further capture.
type capturedReader struct {
	src     io.Reader
	sink    *bytes.Buffer
	cap     int
	dropped bool
}

func (c *capturedReader) Read(p []byte) (int, error) {
	n, err := c.src.Read(p)
	if n > 0 && !c.dropped {
		room := c.cap - c.sink.Len()
		if room > 0 {
			toCapture := n
			if toCapture > room {
				toCapture = room
			}
			_, _ = c.sink.Write(p[:toCapture])
			if toCapture < n {
				c.dropped = true
			}
		} else {
			c.dropped = true
		}
	}
	return n, err
}

// replayBody serves a fixed byte prefix, then continues from the wrapped
// body. Closing the replay closes the underlying body once.
type replayBody struct {
	prefix io.Reader
	body   io.ReadCloser
	closed bool
}

func newReplayBody(prefix []byte, body io.ReadCloser) *replayBody {
	return &replayBody{prefix: bytes.NewReader(prefix), body: body}
}

func (r *replayBody) Read(p []byte) (int, error) {
	if r.prefix != nil {
		n, err := r.prefix.Read(p)
		if n > 0 {
			return n, nil
		}
		// Prefix exhausted.
		r.prefix = nil
		if err != nil && err != io.EOF {
			return n, err
		}
	}
	return r.body.Read(p)
}

func (r *replayBody) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.body.Close()
}
