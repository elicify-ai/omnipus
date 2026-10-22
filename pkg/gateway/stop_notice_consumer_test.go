package gateway

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/notifications"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

// not-wire-format: test-input container only; each frame/list comes from the
// actual gateway producer and REST projection. These are INPUTS to the real
// SPA store tests, not expected UI-state values copied from implementation.
type stopNoticeConsumerInputs struct {
	OwnerSessionID string                     `json:"owner_session_id"`
	Cases          map[string]json.RawMessage `json:"cases"`
	Lists          map[string]json.RawMessage `json:"lists"`
}

// Oracle: round-2 requirement 1 and architect-decision.md. Operational Stop
// notices go to the existing notification center, never the terminal error
// reducer. The JS test consumes these exact producer inputs through that reducer.
func TestStopNoticeProducerInputs_AreNonTerminalAndPersisted(t *testing.T) {
	inputs := stopNoticeConsumerInputs{
		OwnerSessionID: "stop-notice-owner",
		Cases:          make(map[string]json.RawMessage), Lists: make(map[string]json.RawMessage),
	}
	cases := []struct {
		name     string
		count    int
		terminal bool
		trigger  func(*testing.T, *stopHelperApprovalFixture, string)
	}{
		{"approved", 1, false, func(t *testing.T, f *stopHelperApprovalFixture, owner string) {
			f.h.publishStopApprovedOutcome(f.wc, owner, stopApprovalResult{AlreadyApproved: 1})
		}},
		{"partial_and_failure", 2, false, func(t *testing.T, f *stopHelperApprovalFixture, owner string) {
			f.h.sendCancelPartialNotice(f.wc, owner, steer.CancelReport{
				Reached: []string{owner}, Unreachable: []steer.UnreachableSession{{ID: "unreachable-helper", Reason: "saved data is unreadable"}},
			})
			// Actual generic-failure handler, not a copied error fixture. Only
			// the temporary target journal is made unreadable, then restored.
			journal := filepath.Join(f.al.GetSessionLifecycleStore().Dir(), owner+".jsonl")
			backup := journal + ".notice-test-backup"
			require.NoError(t, os.Rename(journal, backup))
			require.NoError(t, os.Mkdir(journal, 0o700))
			t.Cleanup(func() { require.NoError(t, os.Remove(journal)); require.NoError(t, os.Rename(backup, journal)) })
			f.h.handleCancel(f.wc, owner)
		}},
		{"settlement_failure", 1, false, func(t *testing.T, f *stopHelperApprovalFixture, owner string) {
			f.h.buildCancelHooks(f.wc).OnStopSettled(owner, errors.New("injected storage failure"))
		}},
		{"latch_expired", 1, false, func(t *testing.T, f *stopHelperApprovalFixture, owner string) {
			f.h.buildCancelHooks(f.wc).OnLatchExpired(agent.CancelScope{SessionID: owner}, agent.CancelCanceller{UserID: f.wc.userID, Channel: "web"})
		}},
		{"latch_no_session", 1, false, func(t *testing.T, f *stopHelperApprovalFixture, _ string) {
			f.h.buildCancelHooks(f.wc).OnLatchExpired(agent.CancelScope{Channel: "webchat", ChatID: "old-chat"}, agent.CancelCanceller{UserID: f.wc.userID, Channel: "web"})
		}},
		{"approved_zero", 0, false, func(t *testing.T, f *stopHelperApprovalFixture, owner string) {
			f.h.publishStopApprovedOutcome(f.wc, owner, stopApprovalResult{})
		}},
		{"no_recipient", 0, false, func(t *testing.T, f *stopHelperApprovalFixture, owner string) {
			f.wc.userID = ""
			f.h.publishStopApprovedOutcome(f.wc, owner, stopApprovalResult{AlreadyApproved: 1})
		}},
		{"real_turn_error_control", 1, true, func(t *testing.T, f *stopHelperApprovalFixture, owner string) {
			sendConnGenFrame(f.wc, "error", generated.ErrorFrame{Type: "error", SessionId: &owner, Message: "A real turn failed."})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newStopHelperApprovalFixture(t)
			f.h.notifStore = notifications.NewStore(t.TempDir())
			owner := f.newChat(t)
			forward := &eventForwardState{h: f.h, wc: f.wc}
			// Only the socket edge is synchronous in this fixture. Both the
			// event producer and recipient-filtering serializer are production.
			f.al.SetEventSyncTap(func(event agent.Event) {
				f.h.hubSyncTap(event)
				if event.Kind == agent.EventKindNotification {
					forward.onNotification(event)
				}
			})
			tc.trigger(t, f, owner)
			frames := drainStopNoticeInputs(t, f)
			assert.Len(t, frames, tc.count)
			for _, raw := range frames {
				frame, ok := raw.(map[string]any)
				require.True(t, ok)
				if tc.terminal {
					assert.Equal(t, "error", frame["type"])
					continue
				}
				assert.Equal(t, "notification", frame["type"], "Stop operation notice must never be a terminal chat error")
				assert.Equal(t, "stop_notice", frame["notification_type"])
				assert.NotEmpty(t, frame["id"])
				assert.Nil(t, frame["seq"], "global notification must not acquire a session-hub sequence")
				if tc.name == "latch_no_session" {
					assert.Nil(t, frame["session_id"])
				} else {
					assert.Equal(t, owner, frame["session_id"])
				}
			}
			api := &restAPI{notifStore: f.h.notifStore}
			response := httptest.NewRecorder()
			api.handleListNotifications(response, &config.UserConfig{Username: "stop-helper-owner"})
			require.Equal(t, 200, response.Code)
			stored, err := f.h.notifStore.ListForUser("stop-helper-owner")
			require.NoError(t, err)
			wantStored := tc.count
			if tc.terminal {
				wantStored = 0
			}
			assert.Len(t, stored, wantStored, "Stop notice must survive notification-center reload")
			var list any
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
			// Normalize only opaque identity/time values. Classification, body,
			// routing and the actual producer/list shape are left untouched.
			ids := make(map[string]string)
			canonicalizeStopNoticeInput(frames, owner, tc.name, ids)
			canonicalizeStopNoticeInput(list, owner, tc.name, ids)
			// Independent notice rows can share a millisecond. Canonical input
			// ordering avoids making the producer/consumer pairing depend on
			// random id tie-breaks; frame delivery order is never changed.
			if object, ok := list.(map[string]any); ok {
				if rows, ok := object["notifications"].([]any); ok {
					sort.Slice(rows, func(i, j int) bool {
						return rows[i].(map[string]any)["id"].(string) < rows[j].(map[string]any)["id"].(string)
					})
				}
			}
			inputs.Cases[tc.name], err = json.Marshal(frames)
			require.NoError(t, err)
			inputs.Lists[tc.name], err = json.Marshal(list)
			require.NoError(t, err)
		})
	}
	encoded, err := json.MarshalIndent(inputs, "", "  ")
	require.NoError(t, err)
	fixturePath := filepath.Join("..", "..", "tests", "e2e", "fixtures", "stop-notice-frames.json")
	if os.Getenv("UPDATE_STOP_NOTICE_INPUTS") == "1" {
		require.NoError(t, os.WriteFile(fixturePath, append(encoded, '\n'), 0o600))
	}
	fixture, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	assert.JSONEq(t, string(encoded), string(fixture), "SPA test INPUTS must stay paired with actual producers; this is not the behavioral oracle")
}

func drainStopNoticeInputs(t *testing.T, f *stopHelperApprovalFixture) []any {
	t.Helper()
	frames := make([]any, 0)
	for {
		select {
		case raw := <-f.wc.sendCh:
			var frame map[string]any
			require.NoError(t, json.Unmarshal(raw, &frame))
			frames = append(frames, frame)
		default:
			return frames
		}
	}
}

func canonicalizeStopNoticeInput(value any, owner, name string, ids map[string]string) {
	switch item := value.(type) {
	case []any:
		for _, child := range item {
			canonicalizeStopNoticeInput(child, owner, name, ids)
		}
	case map[string]any:
		for key, child := range item {
			switch key {
			case "session_id":
				if child == owner {
					item[key] = "stop-notice-owner"
				}
			case "id":
				if id, ok := child.(string); ok {
					if _, exists := ids[id]; !exists {
						ids[id] = name + "-" + string(rune('a'+len(ids)))
					}
					item[key] = ids[id]
				}
			case "created_at_ms", "updated_at_ms":
				item[key] = float64(1792645200000)
			default:
				canonicalizeStopNoticeInput(child, owner, name, ids)
			}
		}
	}
}
