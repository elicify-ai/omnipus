// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"os"
	"runtime"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
)

// TestToolChoice_CodexCli_NoOp is DS-2's CLI-bridged row: the codex CLI
// flattens tools into prompt text (buildPrompt), so there is no request
// shape to force a tool choice onto. A caller passing Required must not
// error and must not change the CLI invocation at all — this proves the
// NO-OP half of ADR-081 D3 [G-B1]'s CLI exclusion; the WARN half is
// exercised in the source but not separately asserted (no log-capture
// harness in this codebase).
func TestToolChoice_CodexCli_NoOp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock CLI uses #!/bin/bash shebang with no Windows equivalent (see #113)")
	}
	scriptPath := createMockCodexCLI(t, []string{
		`{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"ok"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}`,
	})
	p := &CodexCliProvider{command: scriptPath}

	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hi"}}, nil, "", options)
	if err != nil {
		t.Fatalf("Chat() error = %v, want a forced tool_choice to be silently no-op'd, not errored", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want %q", resp.Content, "ok")
	}
}

// TestToolChoice_CopilotCli_NoOp is the copilot-cli counterpart of
// TestToolChoice_CodexCli_NoOp: the Copilot CLI also flattens tools into
// prompt text, and its verified argv contract (TestCopilotCliProvider_
// InvokesVerifiedFlags) must not change when a tool_choice is requested —
// no new flag is added, no error is returned.
func TestToolChoice_CopilotCli_NoOp(t *testing.T) {
	skipOnWindows(t)

	script, argsFile, _ := writeFakeCopilotCLI(t, "ok", "", 0)
	p := &CopilotCliProvider{command: script}

	options := map[string]any{
		protocoltypes.OptionKeyToolChoice: protocoltypes.ToolChoice{Mode: protocoltypes.ToolChoiceRequired},
	}
	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "ping"}}, nil, "", options)
	if err != nil {
		t.Fatalf("Chat() error = %v, want a forced tool_choice to be silently no-op'd, not errored", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want %q", resp.Content, "ok")
	}

	rawBytes, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading captured args: %v", err)
	}
	raw := string(rawBytes)
	wantExact := "-p\nping\n-s\n--allow-all-tools\n--no-ask-user\n--no-color\n--log-level\nnone\n"
	if raw != wantExact {
		t.Errorf("argv = %q, want %q (a forced tool_choice must not alter the CLI invocation)", raw, wantExact)
	}
}
