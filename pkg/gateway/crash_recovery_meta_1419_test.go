//go:build goolm && stdjson

package gateway

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// Issue 1419 documented-separation option: real list/detail must project the
// canonical helper lifecycle, never infer running from coarse active metadata.
// The end transitions themselves are driven natively in the agent test pack.
func TestCrashRecovery_1419_RESTUsesLifecycleNotActiveMetadata(t *testing.T) {
	for _, c := range []struct{ name, state, reason, want string }{{"intentional_stop", "stopped", "", "stopped"}, {"tool_limit", "interrupted", "tool_limit", "interrupted"}, {"crash_unknown_effect", "interrupted", "reconciliation_required", "interrupted"}} {
		t.Run(c.name, func(t *testing.T) {
			f := newI1R1BootFixture(t)
			api := f.api
			meta, err := api.agentLoop.GetSessionStore().NewSession(session.SessionTypeDelegate, "webchat", "mia")
			require.NoError(t, err)
			active := session.StatusActive
			require.NoError(t, api.agentLoop.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{Status: &active}))
			rec := &session.LifecycleRecord{SessionID: meta.ID, Generation: 1, State: session.LifecycleState(c.state), AgentID: "mia", WorkspaceID: "ws", OwnerScopeKind: session.OwnerScopeHuman}
			if c.state == "stopped" {
				rec.StopNote = &session.StopNote{At: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC), By: session.StopActorHumanUser("owner"), Seq: 1, Cause: session.StopCauseStop}
			}
			if c.reason != "" {
				if !session.IsValidLifecycleState(rec.State) {
					t.Fatal("BLOCKED: durable Interrupted helper lifecycle/reason not implemented — required by DESIGN Q14/Q15 and issue 1419")
				}
				bytes, e := json.Marshal(rec)
				require.NoError(t, e)
				var fields map[string]any
				require.NoError(t, json.Unmarshal(bytes, &fields))
				fields["interruption_reason"] = c.reason
				bytes, e = json.Marshal(fields)
				require.NoError(t, e)
				require.NoError(t, json.Unmarshal(bytes, rec))
			}
			require.NoError(t, f.ls.Persist(rec))
			before, err := os.ReadFile(filepath.Join(api.agentLoop.GetSessionStore().BaseDir(), meta.ID, "meta.json"))
			require.NoError(t, err)
			page, code := u18DoListSessions(t, api, "flat=true")
			require.Equal(t, 200, code)
			row := findSessionRow(page, meta.ID)
			require.NotNil(t, row)
			detail, code := doGetSession(t, api, meta.ID)
			require.Equal(t, 200, code)
			object, ok := detail["session"].(map[string]any)
			require.True(t, ok)
			for _, value := range []map[string]any{row, object} {
				assert.Equal(t, "active", value["status"], "metadata describes conversation availability, not compute")
				assert.Equal(t, c.want, value["lifecycle_state"], "ended helper must not be presented Working because metadata is active")
				_, present := value["execution"]
				assert.False(t, present, "ended helper has no queued/running execution or timer")
				if c.reason != "" {
					assert.Equal(t, c.reason, value["interruption_reason"])
				}
			}
			after, err := os.ReadFile(filepath.Join(api.agentLoop.GetSessionStore().BaseDir(), meta.ID, "meta.json"))
			require.NoError(t, err)
			assert.Equal(t, before, after, "reading exact run state must not mutate conversation metadata")
		})
	}
}
