// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RC-5c (ADR-057 UAT root-cause fix) regression coverage.
//
// buildResult (replay.go) reconstructs a generated.ToolCallResultFrame from a
// persisted session.ToolCall on reload. Before this fix it never set .Error
// under any circumstance, even though the live path (websocket.go's
// EventKindToolExecEnd handler) already populates it for the hand-coded
// delegation-denial case — so error context visible in a live session
// silently vanished after a page reload. This test proves buildResult now
// restores that parity for ANY persisted tc.Error (RC-5 populates it for
// every failed tool call, not just delegation denials).

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestReplay_FailedToolCall_ErrorFieldSurvivesReplay is the RC-5c regression
// test. It must FAIL on the pre-fix buildResult (which never assigns
// f.Error) and PASS once buildResult copies tc.Error onto the reconstructed
// ToolCallResultFrame.
func TestReplay_FailedToolCall_ErrorFieldSurvivesReplay(t *testing.T) {
	const wantErr = "rc5_test: disk full while writing output"
	tc := session.ToolCall{
		ID:         "t-rc5c",
		Tool:       "fail_tool",
		Status:     "error",
		DurationMS: 12,
		Parameters: map[string]any{},
		Error:      wantErr,
	}
	entries := []session.TranscriptEntry{
		assistantEntry("", "mia", tc),
	}

	frames, _ := runReplay(t, entries)

	result := findFrame(frames, "tool_call_result")
	require.NotNil(t, result, "expected a tool_call_result frame for the failed call")
	assert.Equal(t, "error", result.Status)
	assert.Equal(t, wantErr, result.Error,
		"RC-5c: buildResult must copy the persisted tc.Error onto the replayed "+
			"ToolCallResultFrame — before the fix .Error was never set on reload, "+
			"silently dropping the failure reason a live session showed")
}

// TestReplay_SuccessfulToolCall_NoErrorField proves the copy is conditional
// on tc.Error being non-empty — a successful call (Error == "") must not
// surface a stray, empty error string on the wire.
func TestReplay_SuccessfulToolCall_NoErrorField(t *testing.T) {
	tc := session.ToolCall{
		ID:         "t-rc5c-ok",
		Tool:       "read_file",
		Status:     "success",
		DurationMS: 5,
		Result:     map[string]any{"content": "hi"},
	}
	entries := []session.TranscriptEntry{
		assistantEntry("", "mia", tc),
	}

	frames, _ := runReplay(t, entries)

	result := findFrame(frames, "tool_call_result")
	require.NotNil(t, result)
	assert.Equal(t, "success", result.Status)
	assert.Empty(t, result.Error, "a successful call must never carry a stray Error string")
}
