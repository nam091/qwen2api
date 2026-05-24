package toolcall

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParse_JSONFormat(t *testing.T) {
	input := `Here is the result:
<tool_call>
{"name": "read_file", "arguments": {"absolute_path": "/foo/bar.go"}}
</tool_call>`

	result := Parse(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Function.Name != "read_file" {
		t.Errorf("name = %q, want %q", tc.Function.Name, "read_file")
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args["absolute_path"] != "/foo/bar.go" {
		t.Errorf("args[absolute_path] = %q, want %q", args["absolute_path"], "/foo/bar.go")
	}
	if tc.Type != "function" {
		t.Errorf("type = %q, want %q", tc.Type, "function")
	}
	if !strings.HasPrefix(tc.ID, "call_") {
		t.Errorf("id = %q, want prefix call_", tc.ID)
	}
	if result.Content != "Here is the result:" {
		t.Errorf("content = %q, want %q", result.Content, "Here is the result:")
	}
}

func TestParse_XMLFormat(t *testing.T) {
	input := `<tool_call>
<function=read_file>
<parameter=absolute_path>/foo/bar.go</parameter>
</function>
</tool_call>`

	result := Parse(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Function.Name != "read_file" {
		t.Errorf("name = %q, want %q", tc.Function.Name, "read_file")
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args["absolute_path"] != "/foo/bar.go" {
		t.Errorf("args[absolute_path] = %q, want %q", args["absolute_path"], "/foo/bar.go")
	}
}

func TestParse_MultipleToolCalls(t *testing.T) {
	input := `I'll read both files.
<tool_call>
{"name": "read_file", "arguments": {"path": "/a.go"}}
</tool_call>
<tool_call>
{"name": "read_file", "arguments": {"path": "/b.go"}}
</tool_call>`

	result := Parse(input)
	if len(result.ToolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("call[0].name = %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[1].Function.Name != "read_file" {
		t.Errorf("call[1].name = %q", result.ToolCalls[1].Function.Name)
	}
	if result.ToolCalls[0].ID == result.ToolCalls[1].ID {
		t.Error("tool call IDs should be unique")
	}
	if !strings.Contains(result.Content, "I'll read both files.") {
		t.Errorf("content = %q, want to contain prefix text", result.Content)
	}
}

func TestParse_NoToolCalls(t *testing.T) {
	input := "Just a normal response with no tool calls."
	result := Parse(input)
	if len(result.ToolCalls) != 0 {
		t.Fatalf("got %d tool calls, want 0", len(result.ToolCalls))
	}
	if result.Content != input {
		t.Errorf("content = %q, want %q", result.Content, input)
	}
}

func TestParse_MalformedBlock(t *testing.T) {
	input := `prefix prose
<tool_call>
this is not valid json or xml
</tool_call>
suffix prose`

	result := Parse(input)
	if len(result.ToolCalls) != 0 {
		t.Fatalf("got %d tool calls, want 0 for malformed block", len(result.ToolCalls))
	}
	if strings.Contains(result.Content, "<tool_call>") {
		t.Errorf("malformed block must be stripped from content, got: %q", result.Content)
	}
	if !strings.Contains(result.Content, "prefix prose") || !strings.Contains(result.Content, "suffix prose") {
		t.Errorf("surrounding prose must be preserved, got: %q", result.Content)
	}
}

func TestParse_MixedContentAndToolCalls(t *testing.T) {
	input := `Let me check that file.
<tool_call>
{"name": "read_file", "arguments": {"path": "/x.go"}}
</tool_call>
And here's another one:
<tool_call>
{"name": "write_file", "arguments": {"path": "/y.go", "content": "hello"}}
</tool_call>
Done.`

	result := Parse(input)
	if len(result.ToolCalls) != 2 {
		t.Fatalf("got %d tool calls, want 2", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "read_file" {
		t.Errorf("call[0].name = %q", result.ToolCalls[0].Function.Name)
	}
	if result.ToolCalls[1].Function.Name != "write_file" {
		t.Errorf("call[1].name = %q", result.ToolCalls[1].Function.Name)
	}
	if !strings.Contains(result.Content, "Let me check") {
		t.Error("content should contain text before tool calls")
	}
	if !strings.Contains(result.Content, "Done.") {
		t.Error("content should contain text after tool calls")
	}
}

func TestParse_EmptyArguments(t *testing.T) {
	input := `<tool_call>
{"name": "get_status", "arguments": {}}
</tool_call>`

	result := Parse(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Arguments != "{}" {
		t.Errorf("arguments = %q, want %q", result.ToolCalls[0].Function.Arguments, "{}")
	}
}

func TestParse_XMLMultipleParameters(t *testing.T) {
	input := `<tool_call>
<function=write_file>
<parameter=path>/foo/bar.go</parameter>
<parameter=content>package main</parameter>
</function>
</tool_call>`

	result := Parse(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Function.Name != "write_file" {
		t.Errorf("name = %q, want %q", tc.Function.Name, "write_file")
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("unmarshal args: %v", err)
	}
	if args["path"] != "/foo/bar.go" {
		t.Errorf("args[path] = %q", args["path"])
	}
	if args["content"] != "package main" {
		t.Errorf("args[content] = %q", args["content"])
	}
}

func TestParse_ClaudeFunctionCalls(t *testing.T) {
	input := `Some text before.
<function_calls>
<invoke name="read_file">
<parameter name="path">/foo.go</parameter>
</invoke>
</function_calls>
After.`
	result := Parse(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Function.Name != "read_file" {
		t.Errorf("name = %q want read_file", tc.Function.Name)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if args["path"] != "/foo.go" {
		t.Errorf("args[path] = %q", args["path"])
	}
}

func TestParse_BareJSON(t *testing.T) {
	input := `Here is the call: {"name": "search", "arguments": {"query": "go"}} done.`
	result := Parse(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("name = %q", result.ToolCalls[0].Function.Name)
	}
}

func TestParseWithFormats_Disabled(t *testing.T) {
	input := `<function_calls>
<invoke name="read_file">
<parameter name="path">/x</parameter>
</invoke>
</function_calls>`
	result := ParseWithFormats(input, false)
	if len(result.ToolCalls) != 0 {
		t.Errorf("disabled multi-format should not parse function_calls; got %d", len(result.ToolCalls))
	}
}

func TestParse_MalformedArgumentsRepair(t *testing.T) {
	// Unescaped newlines in arguments
	input := `<tool_call>
{"name": "write_file", "arguments": {"content": "line1
line2
line3"}}
</tool_call>`
	result := Parse(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Function.Name != "write_file" {
		t.Errorf("name = %q", tc.Function.Name)
	}
	// Arguments should be valid JSON after repair
	if !json.Valid([]byte(tc.Function.Arguments)) {
		t.Errorf("arguments not valid JSON after repair: %s", tc.Function.Arguments)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(args["content"], "line1") {
		t.Errorf("content missing line1: %q", args["content"])
	}
}

func TestParse_TrailingCommaRepair(t *testing.T) {
	input := `<tool_call>
{"name": "test", "arguments": {"a": 1, "b": 2,}}
</tool_call>`
	result := Parse(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(result.ToolCalls))
	}
	if !json.Valid([]byte(result.ToolCalls[0].Function.Arguments)) {
		t.Errorf("arguments not valid after repair: %s", result.ToolCalls[0].Function.Arguments)
	}
}

// TestParse_HybridFormat_FunctionCloser exercises the exact malformed
// output observed in the wild from qwen3.7-max: a Qwen-style <tool_call>
// opener carrying a JSON name marker plus Anthropic <parameter=KEY>VALUE
// </parameter> blocks, closed with </function>.
func TestParse_HybridFormat_FunctionCloser(t *testing.T) {
	input := "Mình sẽ chạy verify đơn giản hơn.\n\n  <tool_call>\n  {\"name\": \"u_shell_command\",\n  <parameter=command>\n  Select-String -LiteralPath x.ps1 -Pattern 'theme'\n  </parameter>\n  <parameter=justification>\n  Verify theme path\n  </parameter>\n  </function>\n\nDone."
	result := Parse(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1; content=%q", len(result.ToolCalls), result.Content)
	}
	tc := result.ToolCalls[0]
	// toolname.FromQwen strips the u_ prefix → client sees shell_command
	if tc.Function.Name != "shell_command" {
		t.Errorf("name = %q, want shell_command (after u_ prefix strip)", tc.Function.Name)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("unmarshal: %v; args=%s", err, tc.Function.Arguments)
	}
	if !strings.Contains(args["command"], "Select-String") {
		t.Errorf("args[command] missing Select-String: %q", args["command"])
	}
	if !strings.Contains(args["justification"], "Verify") {
		t.Errorf("args[justification] missing Verify: %q", args["justification"])
	}
	if strings.Contains(result.Content, "<tool_call") || strings.Contains(result.Content, "<parameter=") {
		t.Errorf("hybrid block leaked into content: %q", result.Content)
	}
	if !strings.Contains(result.Content, "Mình sẽ chạy") {
		t.Errorf("prose lost: %q", result.Content)
	}
	if !strings.Contains(result.Content, "Done.") {
		t.Errorf("trailing prose lost: %q", result.Content)
	}
}

// TestParse_HybridFormat_StrictCloser handles the case where the hybrid
// content is inside a proper <tool_call>...</tool_call> pair (so the
// inner block is parsed by the strict tool_call parser, which then
// falls through to parseHybrid).
func TestParse_HybridFormat_StrictCloser(t *testing.T) {
	input := `<tool_call>
{"name": "write_file"<parameter=path>/x.go</parameter>
<parameter=content>package main</parameter>
</tool_call>`
	result := Parse(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1; content=%q", len(result.ToolCalls), result.Content)
	}
	tc := result.ToolCalls[0]
	if tc.Function.Name != "write_file" {
		t.Errorf("name = %q, want write_file", tc.Function.Name)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if args["path"] != "/x.go" || args["content"] != "package main" {
		t.Errorf("args = %v", args)
	}
}

// TestParse_UnrepairableBlockStripped covers the case where a strict
// <tool_call>...</tool_call> block contains JSON that no parser can
// rescue (e.g. unescaped quotes from PowerShell here-strings). We must
// still strip the block from the displayed text so the user never sees
// raw `<tool_call>` syntax in their CLI.
func TestParse_UnrepairableBlockStripped(t *testing.T) {
	input := `Setting up the file.
<tool_call>
{"name": "u_shell_command", "arguments": {"command": "@"\nstuff "@ | Set-Content"}}
</tool_call>
Done.`
	result := Parse(input)
	if strings.Contains(result.Content, "<tool_call") {
		t.Errorf("unrepairable tool_call block leaked into content: %q", result.Content)
	}
	if !strings.Contains(result.Content, "Setting up the file.") {
		t.Errorf("prose before block lost: %q", result.Content)
	}
	if !strings.Contains(result.Content, "Done.") {
		t.Errorf("prose after block lost: %q", result.Content)
	}
}

// TestParse_HybridFormat_FunctionCallsCloser covers </function_calls>
// as the closer for a <tool_call> opener (another observed variant).
func TestParse_HybridFormat_FunctionCallsCloser(t *testing.T) {
	input := `<tool_call>
{"name": "search"<parameter=query>golang</parameter>
</function_calls>`
	result := Parse(input)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("got %d tool calls, want 1; content=%q", len(result.ToolCalls), result.Content)
	}
	if result.ToolCalls[0].Function.Name != "search" {
		t.Errorf("name = %q", result.ToolCalls[0].Function.Name)
	}
}

