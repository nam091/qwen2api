package schemacompressor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompress_Object(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path"]}`)
	got := Compress(schema)
	if got != "{content?: string, path: string}" {
		t.Fatalf("unexpected compressed schema: %s", got)
	}
}

func TestCompress_Array(t *testing.T) {
	schema := json.RawMessage(`{"type":"array","items":{"type":"number"}}`)
	got := Compress(schema)
	if got != "number[]" {
		t.Fatalf("unexpected compressed schema: %s", got)
	}
}

func TestCompress_Enum(t *testing.T) {
	schema := json.RawMessage(`{"type":"string","enum":["fast","slow"]}`)
	got := Compress(schema)
	if got != `"fast" | "slow"` {
		t.Fatalf("unexpected compressed schema: %s", got)
	}
}

func TestCompress_InvalidJSONFallback(t *testing.T) {
	input := json.RawMessage(`not-json`)
	got := Compress(input)
	if got != "not-json" {
		t.Fatalf("expected raw fallback, got: %s", got)
	}
}

func TestCompressToolSchema(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)
	got := CompressToolSchema("search", schema)
	if got != "search({q?: string})" {
		t.Fatalf("unexpected signature: %s", got)
	}
}

func TestCompressionIsSmaller(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"absolute file path"},"recursive":{"type":"boolean"},"max_depth":{"type":"integer"}},"required":["path"]}`)
	compressed := Compress(schema)
	if len(compressed) >= len(string(schema)) {
		t.Fatalf("compression ineffective: original=%d compressed=%d", len(string(schema)), len(compressed))
	}
	if !strings.Contains(compressed, "path") {
		t.Fatalf("compressed output should contain property names: %s", compressed)
	}
}
