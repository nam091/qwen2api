// Package schemacompressor converts verbose JSON Schema to compact TypeScript-like
// signatures, reducing tool definition size by ~90%.
//
// Example:
//   Input:  {"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}
//   Output: {path: string, content: string}
package schemacompressor

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Compress converts a JSON Schema to a compact TypeScript-like signature.
// Returns the original schema if compression fails.
func Compress(schemaJSON json.RawMessage) string {
	if len(schemaJSON) == 0 {
		return "{}"
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return string(schemaJSON)
	}

	compressed := compressSchema(schema, 0)
	if compressed == "" {
		return string(schemaJSON)
	}
	return compressed
}

func compressSchema(schema map[string]interface{}, depth int) string {
	if depth > 5 {
		return "any"
	}

	schemaType, _ := schema["type"].(string)

	switch schemaType {
	case "object":
		return compressObject(schema, depth)
	case "array":
		return compressArray(schema, depth)
	case "string":
		if enum, ok := schema["enum"].([]interface{}); ok {
			return compressEnum(enum)
		}
		return "string"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "null":
		return "null"
	default:
		// No type specified - try to infer
		if _, hasProps := schema["properties"]; hasProps {
			return compressObject(schema, depth)
		}
		if _, hasItems := schema["items"]; hasItems {
			return compressArray(schema, depth)
		}
		return "any"
	}
}

func compressObject(schema map[string]interface{}, depth int) string {
	props, ok := schema["properties"].(map[string]interface{})
	if !ok || len(props) == 0 {
		return "{}"
	}

	required := make(map[string]bool)
	if reqList, ok := schema["required"].([]interface{}); ok {
		for _, r := range reqList {
			if name, ok := r.(string); ok {
				required[name] = true
			}
		}
	}

	// Sort keys for deterministic output
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, key := range keys {
		propSchema, ok := props[key].(map[string]interface{})
		if !ok {
			continue
		}

		propType := compressSchema(propSchema, depth+1)
		optional := ""
		if !required[key] {
			optional = "?"
		}

		parts = append(parts, fmt.Sprintf("%s%s: %s", key, optional, propType))
	}

	if len(parts) == 0 {
		return "{}"
	}

	return "{" + strings.Join(parts, ", ") + "}"
}

func compressArray(schema map[string]interface{}, depth int) string {
	items, ok := schema["items"].(map[string]interface{})
	if !ok {
		return "any[]"
	}

	itemType := compressSchema(items, depth+1)
	return itemType + "[]"
}

func compressEnum(enum []interface{}) string {
	var values []string
	for _, v := range enum {
		if s, ok := v.(string); ok {
			values = append(values, fmt.Sprintf("%q", s))
		} else {
			values = append(values, fmt.Sprintf("%v", v))
		}
	}
	if len(values) == 0 {
		return "string"
	}
	return strings.Join(values, " | ")
}

// CompressToolSchema compresses a tool's parameter schema and returns a
// compact function signature.
//
// Example:
//   Input:  name="read_file", schema={"type":"object","properties":{"path":{"type":"string"}}}
//   Output: read_file({path: string})
func CompressToolSchema(name string, schemaJSON json.RawMessage) string {
	compressed := Compress(schemaJSON)
	return fmt.Sprintf("%s(%s)", name, compressed)
}
