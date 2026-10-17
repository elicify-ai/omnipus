// Omnipus — RED payload #1343/#1329 round1, file 7 of the backend QA pack
// (round-2 correction 7, F1 transport-safety scope).
// Target location when applied: pkg/gateway/ws_cancel_scope_card_failure_test.go
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// RED STATUS (qa-lead, 2026-10-16): authored against the round-2 F1
// transport-safety requirement only — once a card-save failure is carried
// into the Stop's returned error, the ORDINARY web Stop surface
// (websocket_cancel.go::handleCancelWithScope) must surface that ordinary
// Stop failure SESSION-SCOPED to the requested Stop session (the existing
// ErrorFrame.session_id), never as a card-action error and never unscoped
// onto whatever chat is currently open. No card-action semantics here: the
// frame carries NO ask_user_card_id, the pending state is unchanged, and the
// Stop's other effects (the approval-cancel cascade, driven through a REAL
// approvalRegistryV2 entry) still ran. No StopSession is mocked — the REAL
// AgentLoop.StopSession runs under the REAL handler.
//
// RED today: handleCancelWithScope's RequestCancel-error branch sends an
// UNSCOPED generic ErrorFrame (no session_id), and today the card-save
// failure never even reaches it (the result is discarded upstream) — so the
// scoped error frame does not exist and the bounded read below fails for
// that reason.
//
// Recommended narrow gate (serial):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestWebStopCancelScope_CarriesCardSaveFailureSessionScoped$' -count=1 -v -p 1 ./pkg/gateway/
//
// Skill note: per elicify-test-writing in RED, items 2 (see it green), 3
// (mutate and confirm it dies) and the Proof-of-failability checklist are
// DEFERRED TO CHECK — they are not claimed here.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// errQaScopeSave is the exact sentinel the MetaStore wrapper injects for the
// pending-ask save failure during the Stop.
var errQaScopeSave = errors.New("qa-1343: injected pending-ask save failure on the Stop surface")

// qaScopeFaultMeta wraps a REAL UnifiedStore and faults SetMeta ONLY for
// pending-ask writes while armed.
type qaScopeFaultMeta struct {
	inner *session.UnifiedStore
	mu    sync.Mutex
	armed bool
}

func (m *qaScopeFaultMeta) GetMeta(sessionID string) (*session.UnifiedMeta, error) {
	return m.inner.GetMeta(sessionID)
}

func (m *qaScopeFaultMeta) SetMeta(sessionID string, patch session.MetaPatch) error {
	m.mu.Lock()
	armed := m.armed
	m.mu.Unlock()
	if armed && patch.PendingAskJSON != nil {
		return errQaScopeSave
	}
	return m.inner.SetMeta(sessionID, patch)
}

func (m *qaScopeFaultMeta) AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error {
	return m.inner.AppendTranscriptStrict(sessionID, entry)
}

func (m *qaScopeFaultMeta) arm() {
	m.mu.Lock()
	m.armed = true
	m.mu.Unlock()
}

func (m *qaScopeFaultMeta) disarm() {
	m.mu.Lock()
	m.armed = false
	m.mu.Unlock()
}

// qaScopeResumeRecorder counts resume dispatches (a session Stop may never
// resume).
type qaScopeResumeRecorder struct {
	mu    sync.Mutex
	calls int
}

func (r *qaScopeResumeRecorder) DispatchResume(*askuser.PendingSet, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	return nil
}

func (r *qaScopeResumeRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// qaScopeProvider is never called — no turn runs in this fixture.
type qaScopeProvider struct{}

func (qaScopeProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "qa-1343 scope provider"}, nil
}
func (qaScopeProvider) GetDefaultModel() string { return "qa-1343-scope-provider" }

// qaScopeCard builds a non-default-safe one-question set (no timers armed).
func qaScopeCard(sessionID, question string) *askuser.PendingSet {
	return &askuser.PendingSet{
		CardID:              askuser.NewCardID(),
		RoutingSessionKey:   "session:" + sessionID,
		TranscriptSessionID: sessionID,
		AgentID:             "mia",
		Channel:             "webchat",
		ChatID:              "chat-1343-scope",
		Questions: []askuser.Question{{
			Header:   "Scope",
			Question: question,
			Options:  []askuser.Option{{Label: "Backend"}, {Label: "Full stack"}},
		}},
	}
}

// qaScopeReadErrorFrame drains frames until an error-typed frame arrives —
// the handler may legitimately emit other frames on this path (stage/notice
// frames), so the oracle scans for THE error frame rather than assuming it is
// first. The deadline is the failure detector; the returned frame is the
// oracle.
func qaScopeReadErrorFrame(t *testing.T, wc *wsConn) map[string]any {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case raw := <-wc.sendCh:
			var frame map[string]any
			require.NoError(t, json.Unmarshal(raw, &frame), "raw=%s", raw)
			if frame["type"] == "error" {
				return frame
			}
			// A non-error frame on this path is legal transport noise; keep
			// scanning for the error frame the requirement is about.
		case <-deadline:
			t.Fatal("no error frame reached the Stop requester within the bound — the card-save failure was not surfaced (or was surfaced unscopably)")
			return nil
		}
	}
}

// TestWebStopCancelScope_CarriesCardSaveFailureSessionScoped pins the F1
// transport contract: a web Stop whose pending-ask card-save fails surfaces
// an error frame SESSION-SCOPED to the requested Stop session, with no
// ask_user_card_id (this is an ordinary Stop failure, not a card-action
// error), with the pending state unchanged, and with the Stop's other
// effects (the approval-cancel cascade, through a REAL approval registry
// entry) still executed.
func TestWebStopCancelScope_CarriesCardSaveFailureSessionScoped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home + "/workspace", DefaultModel: config.DefaultModel{Model: "qa-1343-scope-provider"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Type: config.AgentTypeCustom}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 2, MaxDelegationDepth: 2},
		Sandbox:     config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, qaScopeProvider{})
	store := al.GetSessionStore()
	t.Cleanup(func() { require.NoError(t, store.Close(), "scope pack store.Close must succeed") })

	created, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := created.ID
	otherCreated, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	otherSid := otherCreated.ID

	meta := &qaScopeFaultMeta{inner: store}
	resume := &qaScopeResumeRecorder{}
	reg := askuser.NewRegistry(meta, resume, askuser.Options{})
	al.SetAskUserRegistry(reg)
	t.Cleanup(func() {
		meta.disarm()
		reg.Quiesce()
	})
	cardA := qaScopeCard(sid, "Stopped session head question?")
	cardB := qaScopeCard(sid, "Stopped session queued question?")
	require.NoError(t, reg.CreatePending(cardA))
	require.NoError(t, reg.CreatePending(cardB))
	// A card on ANOTHER session: the scoped error must not be charged to it.
	otherCard := qaScopeCard(otherSid, "Unrelated session question?")
	require.NoError(t, reg.CreatePending(otherCard))

	// The REAL approval registry with one genuinely pending approval for the
	// stopped session: its denial is the "other Stop effects continue"
	// witness. Its outcome channel is drained by a reader BEFORE the stop —
	// cancelAllPendingForSessions blocks until the outcome is consumed, and
	// in production the approval waiter is that consumer.
	approvalReg := newApprovalRegistryV2(64, 30*time.Second)
	t.Cleanup(func() { approvalReg.cancelAllPendingForRestart() })
	entry := makeTestApprovalEntry("approval-qa-1343-scope", sid)
	approvalReg.mu.Lock()
	approvalReg.entries[entry.ApprovalID] = entry
	approvalReg.pendingCount.Store(1)
	approvalReg.mu.Unlock()
	outcomeDrained := make(chan ApprovalOutcome, 1)
	go func() { outcomeDrained <- <-entry.resultCh }()

	h := &WSHandler{
		agentLoop:     al,
		askUserReg:    reg,
		approvalRegV2: approvalReg,
		sessions:      map[string]*wsConn{},
	}
	wc := &wsConn{userID: "daniel", sendCh: make(chan []byte, 8)}

	meta.arm()
	h.handleCancelWithScope(wc, sid, false)
	meta.disarm()

	// THE F1 oracle: the ordinary Stop failure reaches the requester
	// SESSION-SCOPED to the requested Stop session — not unscoped, and never
	// charged to another session's chat.
	frame := qaScopeReadErrorFrame(t, wc)
	scoped, hasSession := frame["session_id"].(string)
	require.True(t, hasSession, "an ordinary Stop failure must be session-scoped (ErrorFrame.session_id); raw=%v", frame)
	require.Equal(t, sid, scoped, "the error must be scoped to the REQUESTED Stop session")
	_, hasCardID := frame["ask_user_card_id"]
	require.False(t, hasCardID,
		"an ordinary Stop failure is NOT a card-action error — it must carry no ask_user_card_id; raw=%v", frame)
	require.NotEmpty(t, frame["message"], "the scoped Stop failure must keep a human-readable message")

	// The card-save failure is retained, not consumed: head and queue intact.
	active, still := reg.PendingForSession(sid)
	require.True(t, still, "the failed card save must retain the head")
	require.Equal(t, cardA.CardID, active.CardID)
	require.Equal(t, askuser.StatusPending, active.Status)
	_, queuedVisible := reg.Get(cardB.CardID)
	require.False(t, queuedVisible, "the failed card save must retain the queue hidden")
	require.Equal(t, 3, reg.PendingCount(), "the unrelated session's card must also be untouched")
	require.Equal(t, 0, resume.count(), "neither the failed save nor the Stop may resume")

	// Other Stop effects continued: the pending approval for the stopped
	// session was really denied despite the failed card save.
	select {
	case <-outcomeDrained:
	case <-time.After(5 * time.Second):
		t.Fatal("the approval-cancel cascade never delivered an outcome — the Stop's other effects must continue past the failed card save")
	}
	approvalReg.mu.Lock()
	state := approvalReg.entries[entry.ApprovalID]
	approvalReg.mu.Unlock()
	if state != nil {
		require.Equal(t, ApprovalStateDeniedCancel, state.state,
			"the pending approval must be denied-cancel by the Stop's approval cascade")
	}
	// (state == nil is accepted: with immediate terminal retention the entry
	// is deleted after denial — the same acceptance the approval registry's
	// own tests use.)
}
