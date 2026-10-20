// Omnipus — RED payload #1343/#1329 round1, file 6 of the backend QA pack.
// Target location when applied:
// pkg/sysagent/tools/agent_delete_cascade_ask_user_test.go
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// RED STATUS (qa-lead, 2026-10-16): pins team-lead F4 Decision A on the
// THIRD real deletion path — the agent-delete cascade
// (pkg/sysagent/tools/agent.go::RunAgentDeleteCascade -> cascadeDeleteAgentSessions
// -> UnifiedStore.DeleteSession), the SAME entry the delete_agent tool and
// the REST DELETE /api/v1/agents/{id} handler both run. External test
// package (systools_test) exactly so pkg/agent can be imported for the
// production SetAskUserRegistry wiring without any import cycle.
//
// Everything real: a real AgentLoop (its ONE shared UnifiedStore at
// $OMNIPUS_HOME/sessions/), a real registry wired through the production
// SetAskUserRegistry entry point, and the real exported cascade entry. No
// mock of Store.DeleteSession, no mock of StopSession — the cascade's
// documented nil-stopSession branch is used because the fixture's sessions
// have no live turn (the Stop half of delete is covered by the gateway REST
// payload). RED today is behavioral: the cascade deletes the row but nothing
// clears the wired registry, so the deleted agent's head+FIFO leak memory
// and global capacity.
//
// Deliberate non-assertion: res.CleanupFailed / res.Warnings are NOT
// asserted — this fixture seeds no task/workspace/agent-store records, so
// the cascade's later best-effort steps may legitimately warn once the
// session step (the only step under test) has already succeeded.
//
// Recommended narrow gate (serial):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestAgentDeleteCascade_ClearsWiredAskUserState$' -count=1 -v -p 1 ./pkg/sysagent/tools/
//
// Skill note: per elicify-test-writing in RED, items 2 (see it green), 3
// (mutate and confirm it dies) and the Proof-of-failability checklist are
// DEFERRED TO CHECK — they are not claimed here.

package systools_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/require"
)

// Compile-time: the cascade's narrow store seam is satisfied by the loop's
// ONE real shared store (DEL-10: the cascade never opens a store of its own).
var _ systools.AgentSessionStore = (*session.UnifiedStore)(nil)

// qaCascadeProvider is never called — no turn runs in this fixture; reaching
// it is a fixture defect, so it panics loudly.
type qaCascadeProvider struct{}

func (qaCascadeProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	panic("qa-1343: the cascade fixture must never reach the provider")
}

func (qaCascadeProvider) GetDefaultModel() string { return "qa-1343-cascade-provider" }

// qaCascadeResumeRecorder counts resume dispatches (the oracle is the count:
// a deletion observer must never resume).
type qaCascadeResumeRecorder struct {
	mu    sync.Mutex
	calls int
}

func (r *qaCascadeResumeRecorder) DispatchResume(*askuser.PendingSet, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return nil
}

func (r *qaCascadeResumeRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// newCascadeTestConfig is the minimal loop config: one native worker agent
// ("mia") with its own home, the repo-standard build shape for tests.
func newCascadeTestConfig(home string) *config.Config {
	return &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         home + "/agents-default-home",
				DefaultModel: config.DefaultModel{Model: "qa-1343-cascade-provider"},
			},
			List: []config.AgentConfig{
				{ID: "mia", Name: "Cascade Victim", Type: config.AgentTypeWorker, Home: home + "/agent-mia"},
				{ID: "pat", Name: "Cascade Survivor", Type: config.AgentTypeWorker, Home: home + "/agent-pat"},
			},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 2, MaxDelegationDepth: 2},
		Sandbox:     config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
}

// qaCascadeCard builds a non-default-safe one-question set (no timer is ever
// armed by this fixture — cleanup prefers no live timers).
func qaCascadeCard(sessionID, question string) *askuser.PendingSet {
	return &askuser.PendingSet{
		CardID:              askuser.NewCardID(),
		RoutingSessionKey:   "session:" + sessionID,
		TranscriptSessionID: sessionID,
		AgentID:             "mia",
		Channel:             "webchat",
		ChatID:              "chat-1343-cascade",
		Questions: []askuser.Question{{
			Header:   "Scope",
			Question: question,
			Options:  []askuser.Option{{Label: "Backend"}, {Label: "Full stack"}},
		}},
	}
}

// TestAgentDeleteCascade_ClearsWiredAskUserState is the single narrow entry
// point for the agent-delete deletion path.
func TestAgentDeleteCascade_ClearsWiredAskUserState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := newCascadeTestConfig(home)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al, err := agent.NewAgentLoop(cfg, msgBus, qaCascadeProvider{})
	require.NoError(t, err)
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store, "SETUP: the loop must expose its shared session store")
	t.Cleanup(func() { require.NoError(t, store.Close(), "cascade pack store.Close must succeed") })

	resume := &qaCascadeResumeRecorder{}
	reg := askuser.NewRegistry(store, resume, askuser.Options{DefaultSafeDelay: time.Hour})
	// Cleanup registered BEFORE any assertion: on any failure path the
	// teardown drain stops the victim head's scheduled one-hour timer (never
	// an oracle, failures loud) so the Quiesce below cannot hang. LIFO: drain
	// + Quiesce run before the store Close and loop Close above.
	t.Cleanup(func() {
		qaSysagentDrainAtTeardown(t, reg)
		reg.Quiesce()
	})
	al.SetAskUserRegistry(reg) // the production wiring entry point (F4 Decision A)

	// The deleted agent's sessions: an active head, a queued tail — and
	// another agent's session that must survive the cascade untouched.
	// NewSession returns the session's *UnifiedMeta — the id is the meta's
	// field (round-1 measured compile-failure class).
	victimMeta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	victimSid := victimMeta.ID
	cardA := qaCascadeCard(victimSid, "Active head for the deleted agent?")
	cardA.Questions[0].DefaultSafe = true
	cardA.Questions[0].Recommended = "Backend"
	cardB := qaCascadeCard(victimSid, "Queued tail for the deleted agent?")
	require.NoError(t, reg.CreatePending(cardA))
	require.NoError(t, reg.CreatePending(cardB))
	keepMeta, err := store.NewSession(session.SessionTypeChat, "webchat", "pat")
	require.NoError(t, err)
	keepSid := keepMeta.ID
	cardKeep := qaCascadeCard(keepSid, "Another agent's question?")
	cardKeep.AgentID = "pat"
	require.NoError(t, reg.CreatePending(cardKeep))
	require.Equal(t, 3, reg.PendingCount(), "SETUP: two cards for the victim session, one for the survivor")

	res := systools.RunAgentDeleteCascade(home, "mia", "rev-1", store, nil)

	// The session half of the cascade really ran through the ONE shared store.
	require.GreaterOrEqual(t, res.SessionsDeleted, 1,
		"the cascade must delete the deleted agent's solely-owned session(s)")
	require.Equal(t, 0, res.SessionsPreservedShared, "the fixture holds no shared sessions")
	goneMeta, goneErr := store.GetMeta(victimSid)
	require.True(t, goneErr != nil || goneMeta == nil, "the deleted agent's session row must be gone")

	// THE finding (F4 Decision A): the confirmed deletion clears the wired
	// registry's head AND FIFO — this delete path runs no per-session Stop
	// that would cancel a card, so only the store's post-success deletion
	// observer can clear it.
	require.Equal(t, 1, reg.PendingCount(),
		"after the confirmed cascade delete only the other agent's card may remain — head AND FIFO of the deleted session must be forgotten")
	_, visible := reg.Get(cardA.CardID)
	require.False(t, visible, "the deleted session's head card must be gone from the registry")
	_, visible = reg.Get(cardB.CardID)
	require.False(t, visible, "the deleted session's queued card must be gone — the whole FIFO")
	_, visible = reg.Get(cardKeep.CardID)
	require.True(t, visible, "another agent's card must be untouched")
	keptMeta, keptErr := store.GetMeta(keepSid)
	require.NoError(t, keptErr)
	require.NotNil(t, keptMeta, "another agent's session row must be untouched")
	require.Equal(t, 0, resume.count(), "the deletion observer dispatches no resume")

	// Timer completion barrier: reachable ONLY after the count assertions
	// passed (a RED short-circuits at them and the cleanup Quiesce would
	// wait — bounded by the cleanup-drain discipline below if that ever
	// changes). On GREEN the deletion observer forgot the deleted session's
	// timers and the survivor card armed none, so Quiesce has nothing
	// scheduled and returns immediately: the observable proof that no
	// scheduled timer survived the direct cascade deletion. The white-box
	// timer-map proof stays in the pkg/askuser pack.
	reg.Quiesce()
}

// qaSysagentDrainAtTeardown drains every remaining session's card state so
// cleanup can stop scheduled one-hour timers and Quiesce without hanging —
// including on an expected old-code RED where nothing was cleared. Order:
// the memory-only ForgetDeletedSession when present (stops timers for the
// whole session in one call); otherwise bounded passes of the
// compatibility cancellation across the legacy bool and current (bool,
// error) shapes. TEARDOWN-ONLY: never consulted by an oracle; failures are
// loud.
func qaSysagentDrainAtTeardown(t *testing.T, reg *askuser.Registry) {
	t.Helper()
	type sessionForgetter interface {
		ForgetDeletedSession(sessionID string)
	}
	forget, canForget := any(reg).(sessionForgetter)
	type cancelBoolErr interface {
		CancelOnSessionStop(sessionID string) (bool, error)
	}
	type cancelBoolOnly interface {
		CancelOnSessionStop(sessionID string) bool
	}
	const maxPasses = 16
	for pass := 0; pass < maxPasses; pass++ {
		pending := reg.PendingAll()
		if len(pending) == 0 {
			return
		}
		seen := map[string]bool{}
		for _, set := range pending {
			if seen[set.TranscriptSessionID] {
				continue
			}
			seen[set.TranscriptSessionID] = true
			if canForget {
				forget.ForgetDeletedSession(set.TranscriptSessionID)
				continue
			}
			if be, ok := any(reg).(cancelBoolErr); ok {
				_, _ = be.CancelOnSessionStop(set.TranscriptSessionID)
			} else if bo, ok := any(reg).(cancelBoolOnly); ok {
				bo.CancelOnSessionStop(set.TranscriptSessionID)
			}
		}
	}
	if remaining := reg.PendingCount(); remaining != 0 {
		t.Errorf("TEARDOWN: %d pending set(s) could not be drained — cleanup could not stop their scheduled timers; Quiesce may block until they fire", remaining)
	}
}
