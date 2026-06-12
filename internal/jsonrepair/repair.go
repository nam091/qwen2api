// Package jsonrepair attempts to fix common JSON malformations produced by LLMs
// when generating tool call arguments containing code strings.
package jsonrepair

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Repair attempts to fix malformed JSON. Returns the repaired string and
// whether any changes were made. If the input is already valid JSON, it is
// returned unchanged.
func Repair(s string) (string, bool) {
	s = strings.TrimSpace(s)

	// Fix malformed keys even when the JSON is technically valid
	// (e.g. "name:": "value" is valid JSON but semantically wrong for tool calls)
	fixed := fixMalformedKeys(s)
	if fixed != s {
		if json.Valid([]byte(fixed)) {
			return fixed, true
		}
	}

	if json.Valid([]byte(s)) {
		return s, false
	}

	original := s
	s = fixUnescapedNewlines(s)
	s = fixUnescapedBackslashes(s)
	s = fixSingleQuotes(s)
	s = fixTrailingCommas(s)
	s = fixUnescapedQuotes(s)
	s = fixMalformedKeys(s)

	if json.Valid([]byte(s)) {
		return s, s != original
	}

	// Last resort: try to extract the outermost JSON object/array.
	if extracted := extractJSON(s); extracted != "" && json.Valid([]byte(extracted)) {
		return extracted, true
	}

	return original, false
}

// fixUnescapedNewlines replaces literal newlines inside JSON string values
// with \n escape sequences.
func fixUnescapedNewlines(s string) string {
	var result strings.Builder
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			result.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			result.WriteByte(ch)
			continue
		}
		if ch == '"' {
			inString = !inString
			result.WriteByte(ch)
			continue
		}
		if inString && ch == '\n' {
			result.WriteString("\\n")
			continue
		}
		if inString && ch == '\r' {
			continue
		}
		if inString && ch == '\t' {
			result.WriteString("\\t")
			continue
		}
		result.WriteByte(ch)
	}
	return result.String()
}

// fixUnescapedBackslashes fixes lone backslashes that aren't valid escape sequences.
func fixUnescapedBackslashes(s string) string {
	var result strings.Builder
	inString := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' && (i == 0 || s[i-1] != '\\') {
			inString = !inString
			result.WriteByte(ch)
			continue
		}
		if inString && ch == '\\' {
			if i+1 < len(s) {
				next := s[i+1]
				if next == '"' || next == '\\' || next == '/' || next == 'b' ||
					next == 'f' || next == 'n' || next == 'r' || next == 't' || next == 'u' {
					result.WriteByte(ch)
					continue
				}
			}
			result.WriteString("\\\\")
			continue
		}
		result.WriteByte(ch)
	}
	return result.String()
}

// fixSingleQuotes replaces single-quoted strings with double-quoted.
func fixSingleQuotes(s string) string {
	if !strings.Contains(s, "'") {
		return s
	}
	if strings.Contains(s, `"`) {
		return s
	}
	return strings.ReplaceAll(s, "'", `"`)
}

var reTrailingComma = regexp.MustCompile(`,\s*([}\]])`)

// fixTrailingCommas removes trailing commas before } or ].
func fixTrailingCommas(s string) string {
	return reTrailingComma.ReplaceAllString(s, "$1")
}

// fixUnescapedQuotes attempts to escape unescaped quotes inside string values.
func fixUnescapedQuotes(s string) string {
	if !strings.Contains(s, `"`) {
		return s
	}
	var result strings.Builder
	inString := false
	escaped := false
	quoteCount := strings.Count(s, `"`)
	if quoteCount%2 != 0 {
		return s
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escaped {
			result.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			result.WriteByte(ch)
			continue
		}
		if ch == '"' {
			inString = !inString
		}
		result.WriteByte(ch)
	}
	return result.String()
}

// extractJSON finds the outermost { ... } or [ ... ] in the string.
func extractJSON(s string) string {
	start := -1
	var opener byte
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			start = i
			opener = s[i]
			break
		}
	}
	if start < 0 {
		return ""
	}
	closer := byte('}')
	if opener == '[' {
		closer = ']'
	}
	depth := 0
	inStr := false
	esc := false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if esc {
			esc = false
			continue
		}
		if ch == '\\' && inStr {
			esc = true
			continue
		}
		if ch == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		if ch == opener {
			depth++
		} else if ch == closer {
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

// reMalformedKey matches JSON keys with trailing colons inside the key string
// (e.g. "name:": "value" instead of "name": "value"). This is a common LLM
// typo when generating tool call JSON.
var reMalformedKey = regexp.MustCompile(`"([a-zA-Z_][a-zA-Z0-9_]*):"\s*:`)

// fixMalformedKeys fixes common LLM typos where a colon is included inside
// the JSON key string instead of outside it.
func fixMalformedKeys(s string) string {
	return reMalformedKey.ReplaceAllString(s, `"$1":`)
}
