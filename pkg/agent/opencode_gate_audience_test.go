package agent

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TEST-ANALYSIS #1 / ADR-091 D3: refusal at the typed-error boundary must
// suppress the EXTERNAL emitter, not merely the native emitter's sibling path.
// The classifier, stores, resolver, observer and event bus are all real. A
// delegate metadata row missing its mandatory lifecycle record is the refused
// input; the fully recorded child is the positive publication control.
func TestOpenCodeGateB1_ExternalTypedErrorHonorsAudienceNone(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		name := "damaged_child_has_no_audience"
		if allowed {
			name = "recorded_child_publishes_to_own_session"
		}
		t.Run(name, func(t *testing.T) {
			al := newSteerBoundaryTestLoop(t)
			lifecycle, unified := newTestClassifierStores(t)
			t.Cleanup(func() { require.NoError(t, unified.Close()) })
			parent := newMetaSession(t, unified, session.SessionTypeChat, "")
			require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
				SessionID: parent, Generation: 1, State: session.LifecycleRunning,
				OwnerScopeKind: session.OwnerScopeHuman, WorkspaceID: "ws-1", AgentID: "worker",
				Origin: &session.Origin{Kind: session.OriginKindChat},
			}))
			child := newMetaSession(t, unified, session.SessionTypeDelegate, parent)
			wantAudience := steer.AudienceNone
			if allowed {
				wantAudience = steer.AudienceSteeringSession
				require.NoError(t, lifecycle.Persist(&session.LifecycleRecord{
					SessionID: child, Generation: 1, State: session.LifecycleRunning,
					OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: parent,
					WorkspaceID: "ws-1", AgentID: "worker",
					Origin:    &session.Origin{Kind: session.OriginKindDelegate},
					SteeredBy: &session.SteeredBy{SteeringSessionID: parent, RootSessionID: parent},
				}))
			}
			resolver := NewSteerAudienceResolver(NewSteerRecordClassifier(lifecycle, unified))
			audience, _, err := resolver.Audience(context.Background(), child)
			require.NoError(t, err)
			require.Equal(t, wantAudience, audience, "instrument: exercise the actual refused/allowed classification")
			observer := &recordingBoundaryObserver{}
			al.SetSteerAudienceDeps(resolver, observer, NewSteerUpwardDeliverer())
			sub := al.eventBus.Subscribe(2)
			defer al.eventBus.Unsubscribe(sub.ID)
			ts := &turnState{sessionKey: child, transcriptSessionID: child, chatID: "gate-external-viewer"}
			const message = "gate-external-error-sentinel"
			const detail = "gate-sanitized-detail"
			al.emitExternalErrorFrame(ts, "external_cli", LLMError{Code: "external_cli_failed", Message: message}, detail)
			require.Len(t, observer.calls, 1, "the external publication site must consult its boundary")
			assert.Equal(t, steer.BoundaryTypedErrorFrame, observer.calls[0].boundary)
			assert.Equal(t, child, observer.calls[0].session)
			assert.Equal(t, wantAudience, observer.calls[0].audience)
			assert.True(t, ts.externalErrorAnnounced.Load())
			if allowed {
				select {
				case event := <-sub.C:
					assert.Equal(t, EventKindError, event.Kind)
					assert.Equal(t, ErrorPayload{
						Stage: "external_cli", ChatID: "gate-external-viewer", SessionID: child,
						Code: "external_cli_failed", Message: message, Detail: detail,
					}, event.Payload, "the control emits the exact child-owned typed error, not a parent-addressed substitute")
				default:
					t.Fatal("allowed external error did not reach the real event bus")
				}
			}
			select {
			case event := <-sub.C:
				t.Fatalf("refused audience must emit zero events; allowed audience exactly one; extra event: %+v", event)
			default:
			}
		})
	}
}
