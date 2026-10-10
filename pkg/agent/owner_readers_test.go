package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// session-core DEL-11 follow-up: a session's agent is its IMMUTABLE owner
// (Session.agent_id). The retired handover field active_agent_id is never
// written any more, so a reader that consults it sees an empty id on every
// fresh session — and, for a legacy file that still carries one, a stale id.
// Oracle: the spec (FR-002/C-MAIN/DEL-11): the owner is agent_id, nothing else.

func newOwnerReaderLoop(t *testing.T) *AgentLoop {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: home}, {ID: "ray", Home: home}},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(al.Close)
	return al
}

// writeLegacyActiveAgent makes a session file carry a stale active_agent_id the
// way an old install's file does, then evicts the cached meta so the next read
// comes from disk. It uses raw JSON on purpose: the field must not need to
// exist in Go for this fixture to compile.
func writeLegacyActiveAgent(t *testing.T, store *session.UnifiedStore, sessionID, stale string) {
	t.Helper()
	if err := store.FlushAndEvictSessionMeta(sessionID); err != nil {
		t.Fatalf("evict: %v", err)
	}
	path := filepath.Join(store.BaseDir(), sessionID, "meta.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read meta.json: %v", err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse meta.json: %v", err)
	}
	doc["active_agent_id"] = stale
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerReaders_RedirectMessageNamesTheOwner(t *testing.T) {
	al := newOwnerReaderLoop(t)
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "ray")
	if err != nil {
		t.Fatal(err)
	}
	msg, err := al.ordinaryRedirectMessage(meta.ID, "go on", "alice", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.Metadata["agent_id"]; got != "ray" {
		t.Fatalf("redirect names agent %q, want the chat's owner %q (an empty id routes the person's instruction to the default agent)", got, "ray")
	}
}

func TestOwnerReaders_LegacyActiveAgentFieldIsIgnored(t *testing.T) {
	al := newOwnerReaderLoop(t)
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", "ray")
	if err != nil {
		t.Fatal(err)
	}
	writeLegacyActiveAgent(t, store, meta.ID, "mia")

	t.Run("redirect", func(t *testing.T) {
		msg, err := al.ordinaryRedirectMessage(meta.ID, "go on", "alice", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := msg.Metadata["agent_id"]; got != "ray" {
			t.Fatalf("agent = %q, want owner ray", got)
		}
	})
	t.Run("goal working agent", func(t *testing.T) {
		got, err := goalWorkingAgentID(meta.ID, store)
		if err != nil || got != "ray" {
			t.Fatalf("goalWorkingAgentID = %q, %v; want owner ray", got, err)
		}
	})
	t.Run("agent for session", func(t *testing.T) {
		inst, err := al.AgentForSession(meta.ID)
		if err != nil || inst == nil || inst.ID != "ray" {
			t.Fatalf("AgentForSession = %v, %v; want owner ray", inst, err)
		}
	})
}

func TestOwnerReaders_GoalAgentIsTheOwnerNotTheFirstJoinedAgent(t *testing.T) {
	al := newOwnerReaderLoop(t)
	// A session owned by ray in which mia also took part (listed first).
	meta := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "s", AgentID: "ray", AgentIDs: []string{"mia", "ray"}}}
	inst := resolveGoalAgent(al, meta)
	if inst == nil || inst.ID != "ray" {
		t.Fatalf("resolveGoalAgent = %v, want the owner ray", inst)
	}
	// Owner missing from AgentIDs entirely: still the owner, not the default.
	meta2 := &session.UnifiedMeta{SessionMeta: session.SessionMeta{ID: "s2", AgentID: "ray"}}
	if inst := resolveGoalAgent(al, meta2); inst == nil || inst.ID != "ray" {
		t.Fatalf("resolveGoalAgent(owner only) = %v, want ray", inst)
	}
}
