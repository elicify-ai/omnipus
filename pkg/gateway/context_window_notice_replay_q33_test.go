package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Q33 / #1081: ADR-066 MAJ-CW-009 requires the classified diagnostic, payload
// and original identity to survive the durable-history read and REST/replay.
// The existing loop_context_notice_q33_test.go owns ordinary-chat suppression;
// this pack does not drive the agent loop or change either existing Q33 test.
//
// Real: JSONL storage, UnifiedStore.ReadTranscript, REST routing and streamReplay.
// Fake: the external model provider in newTestRestAPI and the WebSocket sink.
// Seed generated-contract records directly on disk, not through a test-local
// TranscriptEntry conversion that could discard the payload before the read.
// GREEN and production mutation probes are deferred to an independent CHECK.
const q33DurableNoticeJSON = `{
  "id": "q33-original-notice",
  "type": "context_window_notice",
  "role": "system",
  "content": "The context budget was reduced before retrying this turn.",
  "view_membership": "chat",
  "timestamp": "2026-09-30T12:34:56.123456789Z",
  "agent_id": "jim",
  "turn_id": "q33-original-turn",
  "context_window_notice": {
    "kind": "provider_retry",
    "message": "The context budget was reduced before retrying this turn."
  }
}`

func q33SeedDurableNotice(t *testing.T, store *session.UnifiedStore, sessionID, kind string) generated.Message {
	t.Helper()
	var want generated.Message
	require.NoError(t, json.Unmarshal([]byte(q33DurableNoticeJSON), &want),
		"the fixture must decode into the landed generated Message contract")
	require.NotNil(t, want.ContextWindowNotice, "the seed must contain the spec's required notice payload")
	want.ContextWindowNotice.Kind = generated.MessageContextWindowNoticeKind(kind)
	// session-core U2 (effects design D4/D11): the archive carries no reader
	// default for view_membership, so the raw fixture must state it as "chat".
	// The line is written verbatim (json.RawMessage) so the classified kind
	// override lands without a TranscriptEntry conversion dropping it.
	line := strings.Replace(q33DurableNoticeJSON,
		`"kind": "provider_retry"`, `"kind": "`+string(want.ContextWindowNotice.Kind)+`"`, 1)
	require.NoError(t, fileutil.AppendJSONL(filepath.Join(store.BaseDir(), sessionID, "transcript.jsonl"), json.RawMessage(line)),
		"seed the actual durable transcript with the complete classified wire record")
	return want
}

func TestContextWindowNoticeRESTPreservesClassification_Q33(t *testing.T) {
	// ContextWindowNotice.kind's two equivalence classes come from the shared
	// contract, not from whichever notice branch is implemented first.
	for _, kind := range []string{"provider_retry", "mid_turn"} {
		t.Run(kind, func(t *testing.T) {
			api, cleanup := newTestRestAPI(t)
			defer cleanup()
			sessionID := createTestSession(t, api)
			store := api.agentLoop.GetSessionStore()
			require.NotNil(t, store, "the REST test must use the real durable session store")
			want := q33SeedDurableNotice(t, store, sessionID, kind)

			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sessionID+"/messages", nil)
			api.HandleSessions(w, r)
			require.Equal(t, http.StatusOK, w.Code, "real session-history GET: %s", w.Body.String())

			var got []generated.Message
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got), "decode the real GET response, not a fixture response")
			// An empty/failed history read cannot satisfy an absence assertion.
			require.Len(t, got, 1, "the retained diagnostic must remain in REST history even when hidden by the client")
			assert.Equal(t, want.Type, got[0].Type, "REST must not erase context_window_notice classification")
			if got[0].ContextWindowNotice == nil {
				t.Fatal("BLOCKED: persisted notice payload retention not implemented — required by ADR-066 MAJ-CW-009")
			}
			assert.Equal(t, want.ContextWindowNotice, got[0].ContextWindowNotice,
				"BLOCKED: REST notice payload retention not implemented — required by ADR-066 MAJ-CW-009")
			assert.Equal(t, want.Id, got[0].Id, "REST must preserve the original transcript entry id")
			assert.Equal(t, want.Timestamp, got[0].Timestamp, "REST must preserve the original timestamp")
			assert.Equal(t, want.AgentId, got[0].AgentId, "REST must preserve the original author")
			assert.Equal(t, want.TurnId, got[0].TurnId, "REST must preserve the original turn id")
		})
	}
}

func TestContextWindowNoticeReplayUsesSameClassifiedFrame_Q33(t *testing.T) {
	for _, kind := range []string{"provider_retry", "mid_turn"} {
		t.Run(kind, func(t *testing.T) {
			store, err := session.NewUnifiedStore(t.TempDir())
			require.NoError(t, err, "open a real disk-backed store for the reload")
			t.Cleanup(func() { require.NoError(t, store.Close(), "close the fixture store and its background flusher") })
			// No session creation is needed for a read-only disk fixture. The
			// REST case separately exercises session creation and request routing.
			const sessionID = "q33-reloaded-session"
			wantEntry := q33SeedDurableNotice(t, store, sessionID, kind)
			entries, err := store.ReadTranscript(sessionID)
			require.NoError(t, err, "replay input must come from the real durable-history decoder")
			require.Len(t, entries, 1, "the actual persisted diagnostic must be replayed, never silently dropped")

			sink := &sliceSink{}
			n, err := streamReplay(context.Background(), sessionID, entries, computeReplayStats(entries), sink.emit, nil, nil, nil, nil)
			require.NoError(t, err, "classified notice replay must complete")
			// One content frame plus the legitimate replay-completion terminator.
			// Do not mistake that terminator for a diagnostic degraded into done.
			assert.Equal(t, 1, n, "exactly one diagnostic content frame must be emitted")
			require.Len(t, sink.frames, 2, "one classified notice followed by one replay terminator")
			types := make([]string, 0, len(sink.frames))
			for _, raw := range sink.frames {
				var envelope map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(raw, &envelope), "every emitted frame must be valid JSON")
				var frameType string
				require.NoError(t, json.Unmarshal(envelope["type"], &frameType), "every emitted frame must have its wire discriminator")
				types = append(types, frameType)
			}
			if types[0] != "context_window_notice" {
				t.Fatalf("BLOCKED: classified notice replay not implemented — required by ADR-066 MAJ-CW-009; want context_window_notice, got %q", types[0])
			}
			assert.Equal(t, []string{"context_window_notice", "done"}, types,
				"ADR-066 MAJ-CW-009 forbids replay_message/token/done diagnostic content")

			var got generated.ContextWindowNoticeFrame
			require.NoError(t, json.Unmarshal(sink.frames[0], &got), "decode using the same generated carrier as live delivery")
			want := generated.ContextWindowNoticeFrame{
				Type:      "context_window_notice",
				SessionId: sessionID,
				EntryId:   wantEntry.Id,
				Timestamp: wantEntry.Timestamp.Format(time.RFC3339Nano),
				AgentId:   wantEntry.AgentId,
				TurnId:    "q33-original-turn",
				Notice: generated.ContextWindowNoticeFrameNotice{
					Kind:    kind,
					Message: "The context budget was reduced before retrying this turn.",
				},
			}
			// seq is optional transport metadata assigned by the session hub,
			// outside streamReplay. All required carrier fields are compared.
			assert.Equal(t, want.Type, got.Type, "same live/replay discriminator")
			assert.Equal(t, want.SessionId, got.SessionId, "same original session")
			assert.Equal(t, want.EntryId, got.EntryId, "same original entry id, not a new replay id")
			assert.Equal(t, want.Timestamp, got.Timestamp, "same original timestamp, not replay time")
			assert.Equal(t, want.AgentId, got.AgentId, "same original author, not the session default")
			assert.Equal(t, want.TurnId, got.TurnId, "same original turn id")
			assert.Equal(t, want.Notice, got.Notice, "same full kind/message payload as the live carrier")
		})
	}
}
