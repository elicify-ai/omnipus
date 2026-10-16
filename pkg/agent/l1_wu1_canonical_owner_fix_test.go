//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func l1WU1OwnerSnapshot(t *testing.T, store *session.UnifiedStore) string {
	t.Helper()
	metas, err := store.ListSessions()
	require.NoError(t, err)
	entries := make(map[string][]session.TranscriptEntry)
	for _, meta := range metas {
		entries[meta.ID], err = store.ReadTranscript(meta.ID)
		require.NoError(t, err)
	}
	raw, err := json.Marshal([]any{metas, entries})
	require.NoError(t, err)
	return string(raw)
}

func l1WU1BothOwnerRuntime(t *testing.T) *AgentLoop {
	t.Helper()
	al, _ := l1WU1Runtime(t)
	path := filepath.Join(al.GetConfig().Agents.Defaults.Home, "workspaces", "sales.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"id":"sales","name":"Sales","status":"active","core_team":["mia","jim"]}`), 0o600))
	return al
}

func TestL1WU1EngineExistingSession_ConflictingMetadataRefusesBeforeTurn(t *testing.T) {
	// F02 / FR-123 / §6.4 / chief Q1 A: the bus metadata is not @ addressing.
	for _, row := range []struct {
		name, owner, explicit string
		accepted              bool
	}{
		{"omitted_control", "mia", "", true},
		{"matching_control", "jim", "jim", true},
		{"normalized_match_control", "jim", "JIM", false},
		{"mia_owner_jim_requested", "mia", "jim", false},
		{"jim_owner_mia_requested", "jim", "mia", false},
		{"deleted_owner_survivor_requested", "deleted", "mia", false},
	} {
		t.Run(row.name, func(t *testing.T) {
			al := l1WU1BothOwnerRuntime(t)
			store := al.GetSessionStore()
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", row.owner)
			require.NoError(t, err)
			ws := "sales"
			require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &ws}))
			before := l1WU1OwnerSnapshot(t, store)
			msg := bus.InboundMessage{Channel: "webchat", ChatID: "owner-check", SessionID: meta.ID,
				Content: "canonical owner input", UserInitiated: true, Metadata: map[string]string{"agent_id": row.explicit}}
			answer, owner, err := al.processMessage(context.Background(), msg)
			if !row.accepted {
				assert.Error(t, err, "conflict must not silently run under another agent: answer=%q", answer)
				if err != nil {
					assert.Contains(t, err.Error(), row.owner, "refusal names canonical owner")
					assert.Contains(t, err.Error(), row.explicit, "refusal names requested agent")
				}
				assert.Nil(t, owner, "no admitted/returned substitute owner")
				assert.Empty(t, answer)
				assert.Equal(t, before, l1WU1OwnerSnapshot(t, store), "no transcript/session metadata writes")
				for _, id := range []string{"mia", "jim"} {
					inst, ok := al.GetRegistry().GetAgent(id)
					require.True(t, ok)
					capture, hasCapture := inst.Provider.(interface{ AllRequests() [][]providers.Message })
					require.True(t, hasCapture, "provider fixture must expose request capture for %s", id)
					assert.Empty(t, capture.AllRequests(), "no provider dispatch to %s", id)
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, owner)
			assert.Equal(t, row.owner, owner.ID)
			assert.Equal(t, "named owner control", answer)
			assert.NotEqual(t, before, l1WU1OwnerSnapshot(t, store), "accepted control genuinely persists")
		})
	}
}

func TestL1WU1SteeringRawStoredSession_ActualOwnerAndDeletionDisposition(t *testing.T) {
	// F05 / M12: raw stored ids, not only agent:<owner>:session:<id>.
	for _, ownerID := range []string{"jim", "deleted"} {
		t.Run(ownerID, func(t *testing.T) {
			al, provider := l1WU1Runtime(t) // Mia remains registered to expose substitution.
			meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", ownerID)
			require.NoError(t, err)
			resolvedID, owner, named := al.steeringScopeOwner(meta.ID)
			assert.True(t, named, "raw stored id must identify a canonical owner")
			assert.Equal(t, ownerID, resolvedID)
			item := steeringQueueItem{message: providers.Message{Role: "user", Content: "raw scope steer"}}
			_, err = al.enqueueSteeringItemWithStatus(meta.ID, "", item, nil)
			if ownerID == "deleted" {
				assert.Nil(t, owner)
				assert.EqualError(t, err, "steering refused: the session's owner agent deleted no longer exists")
				assert.Zero(t, al.steering.lenScope(meta.ID), "refused item joins no raw-scope queue")
				assert.Zero(t, al.steering.lenScope("agent:mia:session:"+meta.ID), "no invented Mia queue")
			} else {
				require.NotNil(t, owner)
				assert.Equal(t, "jim", owner.ID, "must not borrow registered Mia")
				require.NoError(t, err)
				assert.Equal(t, 1, al.steering.lenScope(meta.ID), "live raw-scope positive control really queues")
				assert.Equal(t, owner, al.agentForSession(meta.ID))
			}
			assert.Empty(t, provider.AllRequests(), "enqueue/refusal is not a model turn")
		})
	}
}

func TestL1WU1ExistingSessionRouting_CanonicalNonMiaOwnerNeverSubstituted(t *testing.T) {
	// F05 / M13: independent owner differentiation plus real turn control.
	for _, ownerID := range []string{"mia", "jim", "deleted"} {
		t.Run(ownerID, func(t *testing.T) {
			al := l1WU1BothOwnerRuntime(t)
			store := al.GetSessionStore()
			meta, err := store.NewSession(session.SessionTypeChat, "webchat", ownerID)
			require.NoError(t, err)
			ws := "sales"
			require.NoError(t, store.SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &ws}))
			before := l1WU1OwnerSnapshot(t, store)
			msg := bus.InboundMessage{Channel: "webchat", ChatID: "raw-owner", SessionID: meta.ID,
				Content: "existing owner control", UserInitiated: true}
			route, owner, err := al.resolveMessageRoute(msg)
			if ownerID == "deleted" {
				assert.EqualError(t, err, "the session's owner agent deleted no longer exists")
				assert.Nil(t, owner)
				_, dispatchedOwner, dispatchErr := al.processMessage(context.Background(), msg)
				assert.EqualError(t, dispatchErr, "the session's owner agent deleted no longer exists")
				assert.Nil(t, dispatchedOwner)
				assert.Equal(t, before, l1WU1OwnerSnapshot(t, store))
				return
			}
			require.NoError(t, err)
			require.NotNil(t, owner)
			assert.Equal(t, ownerID, owner.ID)
			assert.Equal(t, ownerID, route.AgentID)
			assert.Equal(t, "session.owner", route.MatchedBy)
			assert.Equal(t, "agent:"+ownerID+":session:"+meta.ID, route.SessionKey)
			answer, dispatchedOwner, err := al.processMessage(context.Background(), msg)
			require.NoError(t, err)
			require.NotNil(t, dispatchedOwner)
			assert.Equal(t, ownerID, dispatchedOwner.ID)
			assert.Equal(t, "named owner control", answer)
			assert.NotEqual(t, before, l1WU1OwnerSnapshot(t, store))
		})
	}
}
