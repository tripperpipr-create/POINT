package modeljson

import (
	"strings"
	"testing"
)

func TestRepairToolArgumentsFixesTypicalLocalModelDamage(t *testing.T) {
	cases := map[string]string{
		"":                                         `{}`,
		`{"path":"a.go"}`:                          `{"path":"a.go"}`,
		"```json\n{\"path\":\"a.go\"}\n```":        `{"path":"a.go"}`,
		`<think>hm</think>{"path":"a.go"}`:         `{"path":"a.go"}`,
		`{"path":"a.go"}}`:                         `{"path":"a.go"}`,
		`{"path":"a.go"} I will read it`:           `{"path":"a.go"}`,
		`"{\"path\":\"a.go\"}"`:                    `{"path":"a.go"}`,
		`{"oldText":"if (a) { b }","newText":"x"}`: `{"oldText":"if (a) { b }","newText":"x"}`,
		`{"text":"brace } in string"} trailing }`:  `{"text":"brace } in string"}`,
	}
	for raw, want := range cases {
		got, _, ok := RepairToolArguments(raw)
		if !ok || string(got) != want {
			t.Fatalf("raw=%q got=%q ok=%v", raw, got, ok)
		}
	}
	for _, raw := range []string{`{"path":"a.go"`, `not json`, `[1,2]`} {
		if _, _, ok := RepairToolArguments(raw); ok {
			t.Fatalf("must not invent arguments for %q", raw)
		}
	}
	if _, repaired, _ := RepairToolArguments(`{"path":"a.go"}`); repaired {
		t.Fatal("valid arguments are not a repair")
	}
}

func TestExtractTextToolCallsReadsHermesBlocks(t *testing.T) {
	allowed := func(name string) bool { return name == "read_file" || name == "list_files" }
	content := "Сначала посмотрю файлы.\n<tool_call>\n{\"name\": \"read_file\", \"arguments\": {\"path\": \"a.go\"}}\n</tool_call>\n<tool_call>{\"name\":\"list_files\",\"arguments\":\"{\\\"maxDepth\\\":2}\"}</tool_call>"
	calls, rest := ExtractTextToolCalls(content, allowed)
	if len(calls) != 2 || calls[0].Name != "read_file" || string(calls[0].Arguments) != `{"path": "a.go"}` || string(calls[1].Arguments) != `{"maxDepth":2}` {
		t.Fatalf("calls=%+v", calls)
	}
	if rest != "Сначала посмотрю файлы." {
		t.Fatalf("rest=%q", rest)
	}
}

func TestExtractTextToolCallsRefusesUnofferedOrBrokenCalls(t *testing.T) {
	allowed := func(name string) bool { return name == "read_file" }
	for _, content := range []string{
		`<tool_call>{"name":"run_command","arguments":{"command":"rm -rf /"}}</tool_call>`,
		`<tool_call>{"name":"read_file","arguments":{"path":</tool_call>`,
		`<tool_call>{"name":"read_file","arguments":{"path":"a"}}</tool_call><tool_call>{"name":"run_command","arguments":{}}</tool_call>`,
		`plain answer without calls`,
	} {
		calls, rest := ExtractTextToolCalls(content, allowed)
		if len(calls) != 0 || rest != content {
			t.Fatalf("content=%q calls=%+v rest=%q", content, calls, rest)
		}
	}
}

func TestExtractTextToolCallsAcceptsUnclosedFinalBlock(t *testing.T) {
	calls, rest := ExtractTextToolCalls(`<tool_call>{"name":"read_file","parameters":{"path":"b.go"}}`, func(string) bool { return true })
	if len(calls) != 1 || !strings.Contains(string(calls[0].Arguments), "b.go") || rest != "" {
		t.Fatalf("calls=%+v rest=%q", calls, rest)
	}
}
