package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestPermissionPromptDeniesNativeMutations(t *testing.T) {
	result := PermissionPrompt{}.Execute(context.Background(), json.RawMessage(`{"tool_name":"Write","input":{"path":"a.go"}}`))
	if !result.OK {
		t.Fatalf("denied write should still be a tool result: %#v", result)
	}
	if !strings.Contains(string(result.Output), `"behavior":"deny"`) {
		t.Fatalf("write was not denied: %s", result.Output)
	}
	allowed := PermissionPrompt{}.Execute(context.Background(), json.RawMessage(`{"tool_name":"Read","input":{"path":"a.go"}}`))
	if !allowed.OK || !strings.Contains(string(allowed.Output), `"behavior":"allow"`) {
		t.Fatalf("read should be allowed: %#v", allowed)
	}
}
