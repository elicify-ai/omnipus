// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"encoding/json"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
)

// ADR-092 review finding E: a bash tool_approval_required frame must carry
// segments, split and resolved the way the bash tool does it, with a
// suggested prefix ONLY where "Allow" with scope=prefix would really record
// a prefix grant — the modal must never offer a scope the server downgrades.

// captureApprovalFrame broadcasts a tool_approval_required for a toolName
// call with args and returns the decoded frame.
func captureApprovalFrame(t *testing.T, toolName string, args map[string]any) generated.ToolApprovalRequiredFrame {
	t.Helper()
	frame, _, _ := captureApprovalFrameWithAPI(t, toolName, args)
	return frame
}

// captureApprovalFrameWithAPI is captureApprovalFrame that also returns a
// restAPI over the same agent loop and registry, plus the approval id, so a
// test can resolve the very approval the frame described.
func captureApprovalFrameWithAPI(
	t *testing.T, toolName string, args map[string]any,
) (generated.ToolApprovalRequiredFrame, *restAPI, string) {
	t.Helper()
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	handler, _ := newTestWSHandlerForModelName(t, msgBus)

	reg := newApprovalRegistryV2(64, 300*time.Second)
	entry, accepted := reg.requestApproval("tc-seg", toolName, args, "mia", "sess-seg", "turn-seg")
	require.True(t, accepted)
	t.Cleanup(func() { go func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel) }() })
	go func() { <-entry.resultCh }()
	handler.approvalRegV2 = reg
	api := &restAPI{agentLoop: handler.agentLoop, approvalReg: reg}

	wc := &wsConn{sendCh: make(chan []byte, 4), doneCh: make(chan struct{}), userID: "seg-test"}
	handler.mu.Lock()
	if handler.sessions == nil {
		handler.sessions = make(map[string]*wsConn)
	}
	handler.sessions["seg-test"] = wc
	handler.mu.Unlock()

	handler.broadcastToolApprovalRequired(entry)
	select {
	case raw := <-wc.sendCh:
		var frame generated.ToolApprovalRequiredFrame
		require.NoError(t, json.Unmarshal(raw, &frame))
		return frame, api, entry.ApprovalID
	case <-time.After(2 * time.Second):
		t.Fatal("no tool_approval_required frame")
		return generated.ToolApprovalRequiredFrame{}, nil, ""
	}
}

// TestWS_ToolApprovalRequired_PrefixOfferMatchesRecordedScope is the
// end-to-end oracle for finding E: for every command, the frame offers the
// prefix scope exactly when "Allow" with scope=prefix then records a prefix
// grant — never an offer the server silently downgrades to exact.
func TestWS_ToolApprovalRequired_PrefixOfferMatchesRecordedScope(t *testing.T) {
	for _, cmd := range []string{
		"echo hello world", "ls hello -la", "ls", "sudo echo hi", "sh -c 'echo hi'",
		"echo one && ls two", "echo a | wc -l", "definitely-not-a-program-xyz run",
	} {
		t.Run(cmd, func(t *testing.T) {
			frame, api, approvalID := captureApprovalFrameWithAPI(t, "bash", map[string]any{"command": cmd})
			require.NotEmpty(t, frame.Segments)
			offered := false
			for _, seg := range frame.Segments {
				require.NotNil(t, seg.PrefixAvailable)
				offered = offered || *seg.PrefixAvailable
			}
			resp := postScopedApproval(t, api, approvalID, "prefix")
			assert.Equal(t, offered, resp["scope"] == "prefix",
				"offered prefix=%v but the server recorded scope %v", offered, resp["scope"])
		})
	}
}

func TestWS_ToolApprovalRequired_BashSegments(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX segment splitting and prefix grants; Windows is exact-only (FR-041)")
	}

	t.Run("single command offers the prefix the server records", func(t *testing.T) {
		frame := captureApprovalFrame(t, "bash", map[string]any{"command": "echo hello world"})
		require.Len(t, frame.Segments, 1)
		seg := frame.Segments[0]
		assert.Equal(t, 0, seg.SegmentIndex)
		assert.Equal(t, "echo hello world", seg.CommandText)
		require.NotNil(t, seg.ResolvedBinary, "echo resolves on PATH")
		assert.Contains(t, *seg.ResolvedBinary, "echo")
		require.NotNil(t, seg.PrefixAvailable)
		assert.True(t, *seg.PrefixAvailable)
		require.NotNil(t, seg.SuggestedPrefix)
		assert.Equal(t, "echo hello world", *seg.SuggestedPrefix)
	})

	t.Run("prefix stops at the first flag", func(t *testing.T) {
		frame := captureApprovalFrame(t, "bash", map[string]any{"command": "ls hello -la /tmp"})
		require.Len(t, frame.Segments, 1)
		require.NotNil(t, frame.Segments[0].SuggestedPrefix)
		assert.Equal(t, "ls hello", *frame.Segments[0].SuggestedPrefix)
	})

	// Every case where the server records exact instead must say so, so
	// the modal never offers a scope it would silently downgrade.
	for name, cmd := range map[string]string{
		"bare program": "ls",
		"wrapper":      "sudo echo hi",
		"sh -c":        "sh -c 'echo hi'",
	} {
		t.Run("no prefix for "+name, func(t *testing.T) {
			frame := captureApprovalFrame(t, "bash", map[string]any{"command": cmd})
			require.Len(t, frame.Segments, 1)
			require.NotNil(t, frame.Segments[0].PrefixAvailable)
			assert.False(t, *frame.Segments[0].PrefixAvailable)
			assert.Nil(t, frame.Segments[0].SuggestedPrefix)
		})
	}

	t.Run("chained command lists every part, none with a prefix", func(t *testing.T) {
		frame := captureApprovalFrame(t, "bash", map[string]any{"command": "echo one && ls two"})
		require.Len(t, frame.Segments, 2)
		assert.Equal(t, 0, frame.Segments[0].SegmentIndex)
		assert.Equal(t, "echo one", frame.Segments[0].CommandText)
		assert.Equal(t, 1, frame.Segments[1].SegmentIndex)
		assert.Equal(t, "ls two", frame.Segments[1].CommandText)
		for _, seg := range frame.Segments {
			require.NotNil(t, seg.PrefixAvailable)
			assert.False(t, *seg.PrefixAvailable, "a chained command is recorded as exact")
			assert.Nil(t, seg.SuggestedPrefix)
			assert.NotNil(t, seg.ResolvedBinary)
		}
	})

	t.Run("unresolvable program has no resolved binary and no prefix", func(t *testing.T) {
		frame := captureApprovalFrame(t, "bash", map[string]any{"command": "definitely-not-a-program-xyz run it"})
		require.Len(t, frame.Segments, 1)
		assert.Nil(t, frame.Segments[0].ResolvedBinary)
		require.NotNil(t, frame.Segments[0].PrefixAvailable)
		assert.False(t, *frame.Segments[0].PrefixAvailable)
	})

	t.Run("non-bash approvals carry no segments", func(t *testing.T) {
		frame := captureApprovalFrame(t, "write_file", map[string]any{"path": "a.txt"})
		assert.Nil(t, frame.Segments)
	})
}
