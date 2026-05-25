package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
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
		events:   make(chan pacerEvent, 4),
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
			return ctx.Err()
		case res, ok := <-p.events:
			if !ok {
				return nil
			}
			if res.Err == io.EOF {
				return nil
			}
			if res.Err != nil {
				return res.Err
			}
			if err := onEvent(res.Evt); err != nil {
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
// (nginx, traefik) default to 60s. 10s leaves ample headroom.
const defaultStreamKeepAlive = 10 * time.Second
