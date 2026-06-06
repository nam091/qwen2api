package server

import (
	"bytes"
	"encoding/json"
	"sync"
)

// streamBufPool reuses bytes.Buffer for SSE chunk marshaling to reduce GC pressure.
var streamBufPool = sync.Pool{
	New: func() any {
		buf := make([]byte, 0, 512)
		return bytes.NewBuffer(buf)
	},
}

// getStreamBuf retrieves a reusable buffer from the pool.
func getStreamBuf() *bytes.Buffer {
	buf := streamBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

// putStreamBuf returns a buffer to the pool.
func putStreamBuf(buf *bytes.Buffer) {
	if buf.Cap() <= 4096 { // Don't pool oversized buffers
		streamBufPool.Put(buf)
	}
}

// marshalStreamChunk efficiently marshals a stream chunk using pooled buffers.
// The returned byte slice is a copy so the pooled buffer can be reused immediately.
func marshalStreamChunk(v any) ([]byte, error) {
	// json.Marshal is faster than Encoder for single-shot serialization
	// in Go 1.23+ and avoids the per-call Encoder allocation.
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}
