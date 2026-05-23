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
func marshalStreamChunk(v any) ([]byte, error) {
	buf := getStreamBuf()
	defer putStreamBuf(buf)

	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encode appends newline, trim it for SSE format
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	// Copy since buffer will be reused
	result := make([]byte, len(b))
	copy(result, b)
	return result, nil
}
