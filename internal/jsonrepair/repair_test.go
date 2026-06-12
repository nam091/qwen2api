package jsonrepair

import (
	"encoding/json"
	"testing"
)

func TestRepair_ValidJSON(t *testing.T) {
	input := `{"name": "read_file", "arguments": {"path": "/foo"}}`
	got, changed := Repair(input)
	if changed {
		t.Error("valid JSON should not be changed")
	}
	if got != input {
		t.Errorf("got %q want %q", got, input)
	}
}

func TestRepair_UnescapedNewlines(t *testing.T) {
	input := "{\"name\": \"write\", \"arguments\": {\"content\": \"line1\nline2\nline3\"}}"
	got, changed := Repair(input)
	if !changed {
		t.Error("should have changed")
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("result not valid JSON: %s", got)
	}
}

func TestRepair_TrailingComma(t *testing.T) {
	input := `{"name": "test", "arguments": {"a": 1, "b": 2,}}`
	got, changed := Repair(input)
	if !changed {
		t.Error("should have changed")
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("result not valid JSON: %s", got)
	}
}

func TestRepair_SingleQuotes(t *testing.T) {
	input := `{'name': 'read_file', 'arguments': {'path': '/foo'}}`
	got, changed := Repair(input)
	if !changed {
		t.Error("should have changed")
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("result not valid JSON: %s", got)
	}
}

func TestRepair_LoneBackslash(t *testing.T) {
	// Single backslash before 'x' is invalid - \x is not a valid JSON escape
	input := `{"name": "write", "arguments": {"content": "path` + "\\" + `x` + "\\" + `file"}}`
	got, changed := Repair(input)
	if !changed {
		t.Error("should have changed")
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("result not valid JSON: %s", got)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatal(err)
	}
}

func TestRepair_ExtractFromGarbage(t *testing.T) {
	input := `Here is the tool call: {"name": "search", "arguments": {"q": "test"}} end`
	got, changed := Repair(input)
	if !changed {
		t.Error("should have changed")
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("result not valid JSON: %s", got)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["name"] != "search" {
		t.Errorf("name = %v", parsed["name"])
	}
}

func TestRepair_Unfixable(t *testing.T) {
	input := `this is not json at all`
	got, changed := Repair(input)
	if changed {
		t.Error("unfixable should not claim changed")
	}
	if got != input {
		t.Errorf("unfixable should return original")
	}
}

func TestRepair_TabsInStrings(t *testing.T) {
	input := "{\"content\": \"col1\tcol2\tcol3\"}"
	got, changed := Repair(input)
	if !changed {
		t.Error("should have changed")
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("result not valid JSON: %s", got)
	}
}

func TestRepair_MalformedKeyWithColon(t *testing.T) {
	input := `{"name:": "web_query", "arguments": {"query": "test"}}`
	got, changed := Repair(input)
	if !changed {
		t.Error("should have changed")
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("result not valid JSON: %s", got)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["name"] != "web_query" {
		t.Errorf("name = %v, want web_query", parsed["name"])
	}
}

func TestRepair_MalformedKeyMultiple(t *testing.T) {
	input := `{"name:": "Bash", "arguments:": {"command": "ls"}}`
	got, changed := Repair(input)
	if !changed {
		t.Error("should have changed")
	}
	if !json.Valid([]byte(got)) {
		t.Errorf("result not valid JSON: %s", got)
	}
}
