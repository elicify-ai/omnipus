// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestToolExecEnd_NonDelegationFailure_LivePathPopulatesError is the
// regression coverage for the inverted live/replay error-parity gap: the
// EventKindToolExecEnd handler in pkg/gateway/websocket.go used to populate
// generated.ToolCallResultFrame.Error ONLY via the parseDelegationFailure
// special case, while pkg/gateway/replay.go's buildResult (RC-5c) populates
// .Error from session.ToolCall.Error for EVERY persisted failure. That meant
// a failed non-delegation tool call (e.g. bash) showed NO error live but DID
// show one after a page reload — the reverse of parity, and the reverse of
// what the RC-5c replay commit claimed to fix.
//
// This test drives EventKindToolExecEnd with a non-delegation errored
// payload (a plain "bash" tool failure, not a DelegationFailure JSON
// object) and asserts the emitted live tool_call_result frame carries a
// non-empty Error matching the payload's Result. It must FAIL on the
// pre-fix code (frame.Error == "") and PASS once the live path falls back
// to p.Result for any non-delegation error.
func TestToolExecEnd_NonDelegationFailure_LivePathPopulatesError(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()

	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	const failureText = "bash: command not found: frobnicate"

	bus.Emit(agent.Event{
		Kind: agent.EventKindToolExecEnd,
		Payload: agent.ToolExecEndPayload{
			ToolCallID: session.ToolCallID("call-1"),
			ChatID:     "chat-1",
			SessionID:  "sess-1",
			Tool:       "bash",
			IsError:    true,
			Result:     failureText,
		},
	})

	bus.Close()
	<-done

	require.Len(t, ch, 1, "exactly one frame must be emitted for ToolExecEnd")

	frame := drainFrame(t, ch)
	assert.Equal(t, "tool_call_result", frame.Type, "frame type must be tool_call_result")
	assert.Equal(t, "error", frame.Status, "status must be error")
	assert.Equal(t, failureText, frame.Error,
		"a non-delegation tool failure must populate the live frame's Error field "+
			"from the same string (ToolExecEndPayload.Result) that pkg/agent/loop.go "+
			"persists as session.ToolCall.Error, so live and replay agree")
}

// TestToolExecEnd_DelegationDenial_StillUsesParsedReason confirms the
// pre-existing delegation-denial special case is unaffected by the new
// non-delegation fallback: when p.Result IS a parseable DelegationFailure
// object, frame.Error must still come from parseDelegationFailure's reason,
// not the raw (JSON-encoded) p.Result string.
func TestToolExecEnd_DelegationDenial_StillUsesParsedReason(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()

	h := makeMinimalHandler()
	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	const denialJSON = `{"error":"delegation_denied","reason":"target agent not in trust set"}`

	bus.Emit(agent.Event{
		Kind: agent.EventKindToolExecEnd,
		Payload: agent.ToolExecEndPayload{
			ToolCallID: session.ToolCallID("call-2"),
			ChatID:     "chat-1",
			SessionID:  "sess-1",
			Tool:       "delegate",
			IsError:    true,
			Result:     denialJSON,
		},
	})

	bus.Close()
	<-done

	require.Len(t, ch, 1, "exactly one frame must be emitted for ToolExecEnd")

	frame := drainFrame(t, ch)
	assert.Equal(t, "error", frame.Status, "status must be error")
	assert.NotEqual(t, denialJSON, frame.Error,
		"a delegation denial must not fall back to the raw JSON payload as Error")
	assert.NotEmpty(t, frame.Error, "a delegation denial must still populate a human-readable Error")
}
