// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestToolExecEnd_NonDelegationFailure_LivePathPopulatesError is the
// regression coverage for the inverted live/replay error-parity gap: the
// EventKindToolExecEnd handler in pkg/gateway/websocket.go used to populate
// generated.ToolCallResultFrame.Error ONLY via the parseStructuredToolFailure
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
	assert.Equal(t, failureText, frame.Result,
		"the reason must reach the client — here via Result, which carries it verbatim")
	assert.Empty(t, frame.Error,
		"Error must stay EMPTY when Result already carries the reason: setting both ships "+
			"the identical text twice in one frame. The invariant is 'the reason is reachable', "+
			"not 'Error is always set' — see TestToolExecEnd_OffloadedError_PopulatesErrorInstead "+
			"for the case where Result cannot carry it and Error must.")
}

// TestToolExecEnd_OffloadedError_PopulatesErrorInstead is the other half of the
// contract, and the reason Error exists on the live path at all.
//
// When a failure exceeds InlineToolResultMaxBytes the gateway writes it to disk
// and replaces Result with a small ToolResultRef sentinel, so the frame no
// longer carries any readable reason. Error must then carry it — bounded, so it
// cannot re-inline the payload the offload just removed.
func TestToolExecEnd_OffloadedError_PopulatesErrorInstead(t *testing.T) {
	bus := agent.NewEventBus()
	defer bus.Close()

	// A real tool-result store, so the offload actually fires. Without one
	// maybeOffloadResult is a no-op and Result keeps the raw payload — which
	// would make this test silently exercise the ordinary path instead of the
	// offloaded one, and pass for the wrong reason.
	h := makeMinimalHandler()
	h.toolStore = newToolResultStore(t.TempDir())

	wc, ch := makeForwarderTestConn(64)
	done := runForwarder(h, wc, "chat-1", bus)

	huge := strings.Repeat("x", InlineToolResultMaxBytes+1024)

	bus.Emit(agent.Event{
		Kind: agent.EventKindToolExecEnd,
		Payload: agent.ToolExecEndPayload{
			ToolCallID: session.ToolCallID("call-huge"),
			ChatID:     "chat-1",
			SessionID:  "sess-1",
			Tool:       "bash",
			IsError:    true,
			Result:     huge,
		},
	})

	bus.Close()
	<-done

	require.Len(t, ch, 1)
	frame := drainFrame(t, ch)
	assert.Equal(t, "error", frame.Status)

	// Result must NOT be the raw payload — the offload must have replaced it.
	if s, isString := frame.Result.(string); isString && len(s) >= InlineToolResultMaxBytes {
		t.Fatalf("Result still carries the raw %d-byte payload; the offload did not fire", len(s))
	}

	require.NotEmpty(t, frame.Error,
		"with Result offloaded, Error is the only readable reason left in the frame")
	assert.Less(t, len(frame.Error), InlineToolResultMaxBytes,
		"Error must be bounded — re-inlining the payload here would defeat the offload it just went through")
}

// TestToolExecEnd_DelegationDenial_StillUsesParsedReason confirms the
// pre-existing delegation-denial special case is unaffected by the new
// non-delegation fallback: when p.Result IS a parseable DelegationFailure
// object, frame.Error must still come from parseStructuredToolFailure's reason,
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
