// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// window_runtime_test.go — Gaps 3 and 4 (R1 coverage-gaps brief):
//
// Gap 3: appendWindowMessage (window_runtime.go) must propagate
// AppendWindowMessage's own error, never swallow it. Forced via a
// pre-canceled turn context — ctx.Err() is the first real check
// JSONLStore.AppendWindowMessage's internal snapshotWindowLocked performs,
// so this is a genuine store failure, not a fake.
//
// Gap 4: validateWindowControls (window_runtime.go) must genuinely reject a
// call-message slice that drops or reorders a pending window control
// message (MAJ-CW-006's steering-floor requirement — ADR-066 §"D6/control-
// plane boundary").

//go:build goolm && stdjson

package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

func TestAppendWindowMessage_PropagatesStoreError_NotSwallowed(t *testing.T) {
	_, agent := midTurnFixture(t, 40_000, 0)
	key := "cw-gap3-append-propagate"
	ts := newTurnState(agent, processOptions{SessionKey: key}, turnEventScope{turnID: "gap3"})

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	ts.ctx = canceledCtx

	_, err := ts.appendWindowMessage(providers.Message{Role: "user", Content: "hello"}, windowProducerUser)
	require.Error(t, err, "appendWindowMessage must propagate the underlying store error, not swallow it "+
		"and proceed as if the append succeeded")
	require.ErrorIs(t, err, context.Canceled,
		"the underlying store failure must survive in the returned error (wrapped with %w)")
	require.Contains(t, err.Error(), "context admission: append user message",
		"the documented wrapper context must be present, not just the bare underlying error")

	// The window-error latch must also be set — contextWindowError() is
	// what every later checkpointRequest/validateWindowControls call
	// consults to refuse further admission once the archive is unreliable.
	require.Error(t, ts.contextWindowError(), "a propagated append failure must latch ts.windowError")

	// No corruption: the message was never actually admitted.
	store, ok := agent.Sessions.(session.ContextWindowStore)
	require.True(t, ok)
	snap, snapErr := store.SnapshotWindow(context.Background(), key)
	require.NoError(t, snapErr)
	require.Zero(t, snap.State.Count, "a failed append must leave the archive untouched")
}

// TestValidateWindowControls_RejectsDroppedControl is Gap 4: populate
// ts.windowControls (turn.go) directly with a pending control message, then
// hand validateWindowControls a call-message slice that OMITS it entirely.
// The validator's job (its own name and MAJ-CW-006's steering-floor
// requirement — ADR-066's "D6/control-plane boundary" amendment) is to
// refuse a request that would silently drop a just-injected control message
// before it ever crossed the provider send boundary.
func TestValidateWindowControls_RejectsDroppedControl(t *testing.T) {
	_, agent := midTurnFixture(t, 40_000, 0)
	ts := newTurnState(agent, processOptions{SessionKey: "cw-gap4-validate-controls"}, turnEventScope{turnID: "gap4"})

	control := providers.Message{Role: "user", Content: "[[steer]] stop and summarize instead"}
	ts.mu.Lock()
	ts.windowControls = []providers.Message{control}
	ts.mu.Unlock()

	// The call messages the request is about to send never include the
	// pending control — exactly the bug D6's floor exists to catch.
	msgsWithoutControl := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}

	err := ts.validateWindowControls(msgsWithoutControl)
	require.Error(t, err, "validateWindowControls must reject a request that drops a pending, "+
		"unconsumed window control message — the request never carries it")
	require.Contains(t, err.Error(), "unconsumed control is missing or reordered",
		"the specific documented refusal reason must be the one returned, not an unrelated error")

	// Negative control: the SAME control message present in the call
	// messages (same archive identity, sameArchiveIdentity's definition)
	// must validate cleanly — proves the rejection above is about the drop,
	// not an unrelated mismatch.
	msgsWithControl := []providers.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		control,
	}
	require.NoError(t, ts.validateWindowControls(msgsWithControl),
		"precondition/negative control: a call-message slice that DOES carry the pending control must pass")
}
