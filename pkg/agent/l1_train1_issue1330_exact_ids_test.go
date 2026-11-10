//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const train1Issue1330UUID = "4f563ff0-b7cd-43ec-9527-0cf6140e9ad8"

// #1330 / WU3-TOOL-SHAPE-RULINGS-20261014.md, "Agent id matching is exact":
// both built-in ids and generated UUID ids are identifiers, not case aliases.
// These messages cross the real registry, inbound routing and turn boundary;
// only the paid model edge is scripted. No filesystem case assumption is used.
func TestTrain1Issue1330RuntimeAgentIDsMatchExactly(t *testing.T) {
	for _, entryPath := range []string{"new_chat", "existing_session", "bound_channel"} {
		t.Run(entryPath, func(t *testing.T) {
			for _, row := range []struct {
				name, storedID, requestedID string
				accepted                    bool
			}{
				{"builtin_exact_control", "jim", "jim", true},
				{"builtin_wrong_case", "jim", "Jim", false},
				{"uuid_exact_control", train1Issue1330UUID, train1Issue1330UUID, true},
				{"uuid_wrong_case", train1Issue1330UUID, strings.ToUpper(train1Issue1330UUID), false},
				{"unknown_control", "jim", "not-a-registered-agent", false},
			} {
				t.Run(row.name, func(t *testing.T) {
					al, provider := train1Issue1330Runtime(t)
					inst, found := al.GetRegistry().GetAgent(row.requestedID)
					if row.accepted {
						require.True(t, found, "exact id must be registered")
						require.NotNil(t, inst)
						assert.Equal(t, row.storedID, inst.ID)
					} else {
						assert.False(t, found, "#1330: differently-cased/unknown identifier must not find an agent")
						assert.Nil(t, inst, "#1330: no case-folded registry alias")
					}

					msg := train1Issue1330Message(t, al, entryPath, row.storedID, row.requestedID)
					before := train1Issue1330SessionSnapshot(t, al.GetSessionStore())
					answer, owner, err := al.processMessage(context.Background(), msg)
					if !row.accepted {
						// Runtime error wording is not specified as the tools' wire
						// AGENT_NOT_FOUND envelope. Assert the refusal and its effects,
						// not a new, invented runtime error format.
						assert.Error(t, err, "#1330: unknown id %q must be refused, not delivered", row.requestedID)
						assert.Nil(t, owner, "unknown id must not select any agent")
						assert.Empty(t, answer, "unknown id must not receive an agent reply")
						assert.Empty(t, provider.AllRequests(), "#1330: no agent/model may receive the wrong-case message")
						assert.Equal(t, before, train1Issue1330SessionSnapshot(t, al.GetSessionStore()), "refused input must not enter any session")
						return
					}
					require.NoError(t, err, "exact id control must execute a real turn")
					require.NotNil(t, owner)
					assert.Equal(t, row.storedID, owner.ID)
					assert.Equal(t, "exact-id delivery control", answer)
					train1Issue1330AssertDelivered(t, provider, msg.Content)
				})
			}
		})
	}

	// Kept WU-1 exception: only the omnipus owner comparison case-folds,
	// and an existing session continues under its stored owner spelling.
	for _, entryPath := range []string{"new_chat", "existing_session"} {
		t.Run("omnipus_exception_"+entryPath, func(t *testing.T) {
			al, provider := train1Issue1330Runtime(t)
			msg := train1Issue1330Message(t, al, entryPath, "omnipus", "OmNiPuS")
			route, routedOwner, routeErr := al.resolveMessageRoute(msg)
			require.NoError(t, routeErr)
			require.NotNil(t, routedOwner)
			assert.Equal(t, "omnipus", route.AgentID, "pin the exception to the stored spelling")
			answer, owner, err := al.processMessage(context.Background(), msg)
			require.NoError(t, err, "#1330 must preserve the WU-1 system-owner exception")
			require.NotNil(t, owner)
			assert.Equal(t, "omnipus", owner.ID)
			assert.Equal(t, "exact-id delivery control", answer)
			train1Issue1330AssertDelivered(t, provider, msg.Content)
			if msg.SessionID != "" {
				meta, readErr := al.GetSessionStore().GetMeta(msg.SessionID)
				require.NoError(t, readErr)
				assert.Equal(t, "omnipus", meta.AgentID, "case alias must never rewrite the stored owner")
			}
		})
	}
}

func train1Issue1330Runtime(t *testing.T) (*AgentLoop, *testutil.ScenarioProvider) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, ".omnipus")
	t.Setenv("HOME", root)
	t.Setenv("OMNIPUS_HOME", home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "jim", Type: config.AgentTypeCore},
		{ID: train1Issue1330UUID, Type: config.AgentTypeCustom},
		{ID: "omnipus", Type: config.AgentTypeCore},
	}
	wsPath := filepath.Join(home, "workspaces", "exact-ids.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(wsPath), 0o700))
	ws := `{"id":"exact-ids","name":"Exact ids","status":"active","core_team":["jim","` + train1Issue1330UUID + `","omnipus"]}`
	require.NoError(t, os.WriteFile(wsPath, []byte(ws), 0o600))
	provider := testutil.NewScenario().WithText("exact-id delivery control")
	al, err := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	require.NoError(t, err)
	t.Cleanup(al.Close)
	return al, provider
}

func train1Issue1330Message(t *testing.T, al *AgentLoop, entryPath, storedID, requestedID string) bus.InboundMessage {
	t.Helper()
	msg := bus.InboundMessage{
		Channel: "webchat", ChatID: "exact-id-control", Content: "deliver this exact-id test message",
		UserInitiated: true, Metadata: map[string]string{"agent_id": requestedID, "workspace_id": "exact-ids"},
	}
	switch entryPath {
	case "new_chat":
	case "existing_session":
		meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", storedID)
		require.NoError(t, err)
		wsID := "exact-ids"
		require.NoError(t, al.GetSessionStore().SetMeta(meta.ID, session.MetaPatch{WorkspaceID: &wsID}))
		msg.SessionID = meta.ID
	case "bound_channel":
		msg.Channel, msg.InstanceID = "telegram", "telegram.exact-ids"
		msg.Metadata = nil
		al.GetConfig().Channels = map[string]config.ChannelInstanceConfig{
			msg.InstanceID: {Type: "telegram", Enabled: true, WorkspaceID: "exact-ids",
				Identity: &config.ChannelIdentity{Kind: "agent", ID: requestedID}},
		}
	default:
		t.Fatalf("unknown test entry path %q", entryPath)
	}
	return msg
}

func train1Issue1330AssertDelivered(t *testing.T, provider *testutil.ScenarioProvider, content string) {
	t.Helper()
	requests := provider.AllRequests()
	require.Len(t, requests, 1, "positive control must reach the model exactly once")
	var userContent strings.Builder
	for _, message := range requests[0] {
		if message.Role == "user" {
			userContent.WriteString(message.Content)
		}
	}
	assert.Contains(t, userContent.String(), content, "request capture must see the actual inbound message")
}

func train1Issue1330SessionSnapshot(t *testing.T, store *session.UnifiedStore) string {
	t.Helper()
	metas, err := store.ListSessions()
	require.NoError(t, err)
	transcripts := make(map[string][]session.TranscriptEntry)
	for _, meta := range metas {
		transcripts[meta.ID], err = store.ReadTranscript(meta.ID)
		require.NoError(t, err)
	}
	encoded, err := json.Marshal([]any{metas, transcripts})
	require.NoError(t, err)
	return string(encoded)
}
