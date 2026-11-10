package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// TestSavedWakeControlAdmission follows parent-wake PW-D5/PW-D16/PW-D17:
// status-polled finals are not placed again, but pending finals reach the next
// request exactly once. ADR-066 MAJ-CW-006 protects only actually injected
// controls until a successful provider send. The two-helper/poll ordering is
// deterministic; only the external model boundary is a recording fake.
func TestSavedWakeControlAdmission(t *testing.T) {
	const alpha = "A delegated session handed back (final): ALPHA"
	const bravo = "A delegated session handed back (final): BRAVO"
	for _, tc := range []struct {
		name   string
		polled int
	}{
		{"neither_polled", 0},
		{"ALPHA_polled_BRAVO_pending", 1},
		{"both_polled", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(config.EnvHome, t.TempDir())
			provider := &scriptChatProvider{fallback: "parent completed"}
			al, _ := newChatTargetAskUserLoop(t, provider)
			gate1WireSessionMessaging(t, al)
			store := al.GetSessionStore()
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
			require.NoError(t, err, "fixture: create ordinary parent")
			sid := meta.ID
			ws := testHarnessWorkspaceMembershipID
			require.NoError(t, store.SetMeta(sid, session.MetaPatch{WorkspaceID: &ws}))
			_, err = al.ensureOrdinaryRootRecord(al.GetSessionLifecycleStore(), sid, store)
			require.NoError(t, err, "fixture: real root lifecycle")
			inst, ok := al.GetRegistry().GetAgent("mia")
			require.True(t, ok, "fixture: native parent registered")
			scope := "agent:mia:session:" + sid
			ts := newTurnState(inst, processOptions{
				SessionKey: scope, TranscriptSessionID: sid, TranscriptStore: store,
				Channel: "webchat", ChatID: sid, WorkspaceID: ws,
			}, turnEventScope{turnID: "saved-wake-control-admission"})
			ts.ctx = context.Background()
			ts.setIteration(2) // Reproduce the next-round fence, not the iteration ceiling.
			require.NoError(t, ts.contextWindowError(), "fixture: real model archive ready")
			al.registerActiveTurn(ts)
			defer al.clearActiveTurn(ts)

			// Construct the two completed inputs, then use the production live-wake
			// enqueue. No helper process, model call or timing sleep is needed.
			bodies := []string{alpha, bravo}
			for i, body := range bodies {
				id := fmt.Sprintf("control-helper-%d:1:final", i)
				_, err := store.SaveChatInput(sid, "saved-input-"+id, "mia", body)
				require.NoError(t, err, "fixture: save completion before enqueue")
				al.asyncNotifier.sessionWakes.Store(sid+"\x00"+id, true)
				require.NoError(t, al.EnqueueSteeringWake(scope, "mia", sid, id,
					providers.Message{Role: "user", Content: body}))
			}
			require.Equal(t, 2, al.steering.lenScope(scope), "fixture: ALPHA then BRAVO queued")

			// This is the real final-read hook used by delegate status, executed
			// before injection; admission and its include decision are not mocked.
			for i := 0; i < tc.polled; i++ {
				already, err := al.recordPolledFinalConsumed(sid, "mia", fmt.Sprintf("control-helper-%d:1:final", i))
				require.NoError(t, err, "fixture: status poll writes consumed marker")
				require.False(t, already, "fixture: first poll owns this final")
			}

			// Real assembled prompts contain structured system text. Supply it
			// up front so normalization is not an unrelated shape discrepancy.
			base := []providers.Message{{Role: "system", Content: "parent control admission",
				SystemParts: []providers.ContentBlock{{Type: "text", Text: "parent control admission"}}}}
			rt := &agentLoopRunTurn{al: al, ts: ts, turnCtx: context.Background(), activeProvider: provider, llmModel: "test-model"}
			rf := &agentLoopRunTurnFallbacks{rt: rt}
			ri := &agentLoopRunTurnIteration{rf: rf, cfg: al.GetConfig(), messages: base, activeModel: "test-model"}
			require.Equal(t, agentLoopRunTurnIterationNext, ri.beginIteration(), "real injection fence must continue")
			require.Equal(t, 0, al.steering.lenScope(scope), "real fence dequeued the batch")
			want := append([]providers.Message(nil), base...)
			for _, body := range bodies[tc.polled:] {
				want = append(want, providers.Message{Role: "user", Content: body})
			}
			require.Equal(t, want, ri.messages, "PW-D16: polled input absent; pending input once in FIFO order")
			t.Logf("polled=%d protected=%#v request=%#v", tc.polled, ts.windowControls, ri.messages)
			validationErr := ts.validateWindowControls(ri.messages)
			t.Logf("real validateWindowControls result: %v", validationErr)

			if tc.polled == 0 {
				require.NoError(t, validationErr, "control: both legitimate inputs present")
				assertSavedWakeControlsRejectInvalidRequest(t, rt, provider, want)
			}

			// The request must cross the real send guard, not just match a local
			// assembled slice. The defect rejects it before the provider is called.
			_, sendErr := rt.callProviderOnce(ri.messages, nil)
			t.Logf("real callProviderOnce result: %v; provider calls=%d", sendErr, provider.callCount())
			require.NoError(t, sendErr, "an intentionally excluded already-polled completion must not block the parent's next request")
			require.NoError(t, validationErr, "only actually admitted messages are unconsumed window controls")
			require.Equal(t, 1, provider.callCount(), "parent reaches provider exactly once")
			got, captured := provider.lastMessages(1)
			require.True(t, captured, "recording provider captured the real send")
			require.Equal(t, want, got, "provider receives no duplicate polled completion")
			require.Empty(t, ts.windowControls, "successful real send clears included controls")
		})
	}
}

// These adversarial requests are test inputs, not implementation mutations.
// Keep both guards and retry protection observable: simply removing protection
// or disabling validation must not make the regression pack pass.
func assertSavedWakeControlsRejectInvalidRequest(t *testing.T, rt *agentLoopRunTurn, provider *scriptChatProvider, want []providers.Message) {
	t.Helper()
	const missing = "context request: unconsumed control is missing or reordered"
	protected := append([]providers.Message(nil), rt.ts.windowControls...)
	for _, tc := range []struct {
		name     string
		messages []providers.Message
	}{
		{"missing_included_control_is_rejected_before_provider", want[:2]},
		{"reordered_included_controls_are_rejected_before_provider", []providers.Message{want[0], want[2], want[1]}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.EqualError(t, rt.ts.validateWindowControls(tc.messages), missing,
				"ADR-066: detect genuinely missing or reordered admitted controls")
			response, err := rt.callProviderOnce(tc.messages, nil)
			require.EqualError(t, err, missing, "real send guard must reject the invalid request")
			require.Nil(t, response, "rejected request produces no model response")
			require.Equal(t, 0, provider.callCount(), "invalid request never crosses the model boundary")
			require.Equal(t, protected, rt.ts.windowControls, "pre-send rejection preserves every control for the correct retry")
		})
	}
}
