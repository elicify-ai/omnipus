// Omnipus — RED payload #1343/#1329 round1, file 2 of the backend QA pack.
// Target location when applied: pkg/gateway/ask_user_queue_lifecycle_test.go
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// RED STATUS (qa-lead, 2026-10-16): authored against the founder FIFO
// decision and team-lead F4 Decision A only ("deletion must remove ALL
// active+queued question state/timers AFTER confirmed deletion", via ONE
// per-UnifiedStore post-success deletion observer wired by
// AgentLoop.SetAskUserRegistry). Oracles are NOT derived from any
// implementation — today nothing clears a deleted session's question state,
// so the delete cases are behavioral RED on PendingCount/Get. Backend-lead
// applies this payload unchanged and owns the RED run and its receipt.
// Recommended narrow gate (serial, never with another local suite):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestAskUserQueueDeleteLifecycle$|^TestAskUserProtectedMainDeleteRefusesWithoutCleanup$|^TestAskUserWorkspaceMainReleaseClearsQueue$' -count=1 -v -p 1 ./pkg/gateway/
//
// Skill note: per elicify-test-writing in RED, items 2 (see it green), 3
// (mutate and confirm it dies) and the Proof-of-failability checklist are
// DEFERRED TO CHECK — they are not claimed here.
//
// Instrument notes:
//   - The card on queued set B carries a one-HOUR default-safe delay (long
//     physical delay): no real timer can auto-resolve B before or after the
//     delete, so a pass can never be a timer artifact. The in-package timer
//     map proof lives in the pkg/askuser checkpoint pack.
//   - qaQueueMetaStore's TEARDOWN-ONLY flag (SetMeta becomes a no-op once
//     armed) exists ONLY so t.Cleanup can drain leftovers after a FAILED
//     run whose storage is already deleted; no oracle ever reads it.
//   - Deletion goes through the REAL REST handler (HandleSessions +
//     httptest) and the REAL workspace release cascade — never a fake
//     store.DeleteSession or a fake stopSession.

package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// qaQueueProvider is never called (no turn is driven in this pack); the
// loop harness requires a provider at construction only.
type qaQueueProvider struct{}

func (qaQueueProvider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "qa-1329 lifecycle provider"}, nil
}
func (qaQueueProvider) GetDefaultModel() string { return "qa-1329-lifecycle-provider" }

// qaQueueMetaStore wraps the loop's REAL UnifiedStore at the askuser.MetaStore
// seam. The teardown flag is the pack's ONLY special power: once set, SetMeta
// is a no-op so cleanup-time draining can never write into (or resurrect) a
// deleted session's storage — including when the implementation under test
// failed to clear its state. NO oracle reads or depends on this flag.
type qaQueueMetaStore struct {
	inner *session.UnifiedStore
	mu    sync.Mutex
	noop  bool
}

func newQaQueueMetaStore(inner *session.UnifiedStore) *qaQueueMetaStore {
	return &qaQueueMetaStore{inner: inner}
}

func (m *qaQueueMetaStore) GetMeta(sessionID string) (*session.UnifiedMeta, error) {
	return m.inner.GetMeta(sessionID)
}

func (m *qaQueueMetaStore) SetMeta(sessionID string, patch session.MetaPatch) error {
	m.mu.Lock()
	noop := m.noop
	m.mu.Unlock()
	if noop {
		return nil // teardown-only; never consulted by an oracle
	}
	return m.inner.SetMeta(sessionID, patch)
}

func (m *qaQueueMetaStore) AppendTranscriptStrict(sessionID string, entry session.TranscriptEntry) error {
	return m.inner.AppendTranscriptStrict(sessionID, entry)
}

// armTeardownNoop is registered in t.Cleanup only.
func (m *qaQueueMetaStore) armTeardownNoop() {
	m.mu.Lock()
	m.noop = true
	m.mu.Unlock()
}

// qaQueueResumeRecorder counts resume dispatches (the count and ids are the
// oracle; nothing in this pack may ever resume).
type qaQueueResumeRecorder struct {
	mu    sync.Mutex
	cards []string
}

func (r *qaQueueResumeRecorder) DispatchResume(set *askuser.PendingSet, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cards = append(r.cards, set.CardID)
	return nil
}

func (r *qaQueueResumeRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cards)
}

// qaCloseStoreAtCleanup registers an error-checked store Close in the LIFO
// cleanup chain (queue pack precedent: the store's background flusher must
// not outlive t.TempDir removal).
func qaCloseStoreAtCleanup(t *testing.T, store *session.UnifiedStore) {
	t.Cleanup(func() {
		require.NoError(t, store.Close(), "lifecycle pack store.Close must succeed")
	})
}

// qaTeardownDrainCancel is the TEARDOWN-ONLY compatibility cancellation:
// it adapts the registry's CancelOnSessionStop across the legacy bool and
// the current (bool, error) shapes. Never an oracle; used only so cleanup
// can drain a failed run without hanging on scheduled one-hour timers.
func qaTeardownDrainCancel(reg *askuser.Registry, key string) bool {
	type cancelBoolErr interface {
		CancelOnSessionStop(sessionID string) (bool, error)
	}
	if be, ok := any(reg).(cancelBoolErr); ok {
		cancelled, err := be.CancelOnSessionStop(key)
		if err != nil {
			return false
		}
		return cancelled
	}
	type cancelBoolOnly interface {
		CancelOnSessionStop(sessionID string) bool
	}
	if bo, ok := any(reg).(cancelBoolOnly); ok {
		return bo.CancelOnSessionStop(key)
	}
	return false
}

// qaDrainRegistryAtTeardown drains every remaining session's card state so
// cleanup can stop scheduled one-hour timers and Quiesce without hanging —
// including on an expected old-code RED where nothing was cleared. Order:
// the memory-only ForgetDeletedSession when present (stops timers for the
// whole session in one call); otherwise bounded passes of the compatibility
// cancellation (each pass cancels the live head and promotes the next).
// TEARDOWN-ONLY: never consulted by an oracle; failures are loud.
func qaDrainRegistryAtTeardown(t *testing.T, reg *askuser.Registry) {
	t.Helper()
	type sessionForgetter interface{ ForgetDeletedSession(sessionID string) }
	forget, canForget := any(reg).(sessionForgetter)
	const maxPasses = 16
	for pass := 0; pass < maxPasses; pass++ {
		pending := reg.PendingAll()
		if len(pending) == 0 {
			return
		}
		if canForget {
			for _, set := range pending {
				forget.ForgetDeletedSession(set.TranscriptSessionID)
			}
			continue
		}
		seen := map[string]bool{}
		for _, set := range pending {
			if seen[set.TranscriptSessionID] {
				continue
			}
			seen[set.TranscriptSessionID] = true
			qaTeardownDrainCancel(reg, set.TranscriptSessionID)
		}
	}
	if remaining := reg.PendingCount(); remaining != 0 {
		t.Errorf("TEARDOWN: %d pending set(s) could not be drained — cleanup could not stop their scheduled timers; Quiesce may block until they fire", remaining)
	}
}

// qaQueueFixture is the shared fixture: real loop, real store, real REST
// surface, real registry wired through the production SetAskUserRegistry.
type qaQueueFixture struct {
	t      *testing.T
	api    *restAPI
	store  *session.UnifiedStore
	meta   *qaQueueMetaStore
	reg    *askuser.Registry
	resume *qaQueueResumeRecorder
}

func qaNewQueueFixture(t *testing.T) *qaQueueFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: home + "/workspace", DefaultModel: config.DefaultModel{Model: "qa-1329-lifecycle-provider"}, MaxTokens: 4096},
			List:     []config.AgentConfig{{ID: "mia", Type: config.AgentTypeCustom}},
		},
		Performance: config.PerformanceConfig{MaxParallelAgents: 2, MaxDelegationDepth: 2},
		Sandbox:     config.OmnipusSandboxConfig{Mode: config.SandboxModeOff},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, qaQueueProvider{})
	store := al.GetSessionStore()
	qaCloseStoreAtCleanup(t, store)
	meta := newQaQueueMetaStore(store)
	resume := &qaQueueResumeRecorder{}
	// MaxPending 4: exactly the fixture's four cards fill the global cap, so
	// the post-delete capacity assertion is meaningful.
	reg := askuser.NewRegistry(meta, resume, askuser.Options{MaxPending: 4, DefaultSafeDelay: time.Hour})
	al.SetAskUserRegistry(reg) // production wiring entry point
	// Cleanup registered BEFORE any assertion (brief): LIFO runs the
	// teardown-only drain and Quiesce before the store Close above, and
	// before al.Close. Round-4 correction C: a bare Quiesce would hang for
	// the remaining one-hour scheduled timer on an expected old-code RED —
	// so the cleanup FIRST drains every remaining session's card state
	// (teardown-only: never an oracle, failures loud), THEN Quiesces.
	t.Cleanup(func() {
		meta.armTeardownNoop() // teardown-only: no write may touch deleted storage during cleanup
		qaDrainRegistryAtTeardown(t, reg)
		reg.Quiesce()
	})
	return &qaQueueFixture{
		t: t, api: &restAPI{agentLoop: al, homePath: home}, store: store,
		meta: meta, reg: reg, resume: resume,
	}
}

// qaLifecycleCard builds a one-question set; defaultSafe marks the question
// default-safe (only ever combined with the fixture's one-hour delay).
func qaLifecycleCard(sessionID, question string, defaultSafe bool) *askuser.PendingSet {
	q := askuser.Question{
		Header:   "Scope",
		Question: question,
		Options:  []askuser.Option{{Label: "Backend"}, {Label: "Full stack"}},
	}
	if defaultSafe {
		q.DefaultSafe = true
		q.Recommended = "Backend"
	}
	return &askuser.PendingSet{
		CardID:              askuser.NewCardID(),
		RoutingSessionKey:   "session:" + sessionID,
		TranscriptSessionID: sessionID,
		AgentID:             "mia",
		Channel:             "webchat",
		ChatID:              "chat-1329",
		Questions:           []askuser.Question{q},
	}
}

// TestAskUserQueueDeleteLifecycle pins F4 Decision A on the REAL chat REST
// delete path (rest_sessions.go::deleteSession -> removeStoppedSession ->
// UnifiedStore.DeleteSession): after a CONFIRMED delete, the deleted
// session's head, COMPLETE FIFO (B and C — the whole FIFO, not just the
// head) and timers are gone from the registry, the freed global capacity
// admits a new session's card, and a protected main's 409 refusal cleans
// nothing.
func TestAskUserQueueDeleteLifecycle(t *testing.T) {
	f := qaNewQueueFixture(t)
	// NewSession returns the session's *UnifiedMeta — the id is the meta's
	// field (round-1 measured compile failure, fixture-only correction).
	createdMeta, err := f.store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	sid := createdMeta.ID
	cardA := qaLifecycleCard(sid, "Active head question?", false)        // non-default-safe head
	cardB := qaLifecycleCard(sid, "Queued default-safe question?", true) // 1h delay: no pre-delete auto-resolution
	cardC := qaLifecycleCard(sid, "Queued tail question?", false)        // proves the WHOLE FIFO goes
	require.NoError(t, f.reg.CreatePending(cardA))
	require.NoError(t, f.reg.CreatePending(cardB))
	require.NoError(t, f.reg.CreatePending(cardC))

	// Protected main (the 409 control) holds the fourth and final card.
	mainMeta, err := f.store.GetOrCreateMainSession("wsqa1329", "mia")
	require.NoError(t, err)
	cardMain := qaLifecycleCard(mainMeta.ID, "Main session question?", false)
	require.NoError(t, f.reg.CreatePending(cardMain))
	require.Equal(t, 4, f.reg.PendingCount(), "SETUP: the fixture's four cards fill the global cap")

	// Capacity is genuinely full: a fifth card must be refused. This makes
	// the post-delete "capacity freed" assertion meaningful rather than
	// vacuous.
	otherMeta, err := f.store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	otherSession := otherMeta.ID
	err = f.reg.CreatePending(qaLifecycleCard(otherSession, "Probe question?", false))
	require.ErrorIs(t, err, askuser.ErrSaturated, "SETUP: the cap must be full before the delete")

	// The REAL REST delete of the non-main chat session.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+sid, nil)
	f.api.HandleSessions(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, "delete body=%s", rec.Body.String())
	var deleted struct {
		Success bool `json:"success"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &deleted))
	require.True(t, deleted.Success, "the handler must report success")

	// The row is really gone (reopened store: no cache can manufacture this).
	reopened, err := session.NewUnifiedStore(f.store.BaseDir())
	require.NoError(t, err)
	qaCloseStoreAtCleanup(t, reopened)
	goneMeta, goneErr := reopened.GetMeta(sid)
	require.True(t, goneErr != nil || goneMeta == nil, "the deleted session must be absent from reopened storage")

	// THE finding: deletion removes ALL of the session's question state.
	require.Equal(t, 1, f.reg.PendingCount(),
		"after the confirmed delete only the untouched protected-main card may remain — head AND whole FIFO must be forgotten")
	for _, card := range []*askuser.PendingSet{cardA, cardB, cardC} {
		_, visible := f.reg.Get(card.CardID)
		require.False(t, visible, "deleted session's card %s must no longer be registry-visible", card.CardID)
	}
	_, visible := f.reg.PendingForSession(sid)
	require.False(t, visible, "no pending set may stay reachable for the deleted session")
	require.Len(t, f.reg.PendingAll(), 1, "PendingAll must expose only the surviving protected-main card")
	_, mainVisible := f.reg.Get(cardMain.CardID)
	require.True(t, mainVisible, "the protected main's card must be untouched by the other session's delete")
	require.Equal(t, 0, f.resume.count(), "deletion must dispatch no resume")

	// Freed global capacity admits another session's card.
	require.NoError(t, f.reg.CreatePending(qaLifecycleCard(otherSession, "Admitted after delete?", false)),
		"deleting the session must free its global capacity")

	// Timer completion barrier (round-4 correction C): B was default-safe
	// with a one-hour delay and was promoted by the delete's own Stop, so a
	// real one-hour timer existed. It reaches this barrier ONLY after the
	// count assertions above passed (a RED short-circuits at them and the
	// cleanup drain stops the timer), so on GREEN — where the deletion
	// observer forgot head+FIFO+timers — Quiesce has nothing scheduled and
	// returns immediately. Its prompt return is the gateway-observable proof
	// that no scheduled timer survived the deletion; the white-box timer-map
	// proof stays in the pkg/askuser pack (the gateway cannot see private
	// maps).
	f.reg.Quiesce()
	require.Equal(t, 0, f.resume.count(), "no resume may appear after the deletion")
}

// TestAskUserProtectedMainDeleteRefusesWithoutCleanup pins the negative
// control: a protected main's DELETE is refused 409 BEFORE any deletion, and
// its live question card survives untouched (no cleanup of a live session).
func TestAskUserProtectedMainDeleteRefusesWithoutCleanup(t *testing.T) {
	f := qaNewQueueFixture(t)
	mainMeta, err := f.store.GetOrCreateMainSession("wsqa1329ctrl", "mia")
	require.NoError(t, err)
	cardMain := qaLifecycleCard(mainMeta.ID, "Main control question?", false)
	require.NoError(t, f.reg.CreatePending(cardMain))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/sessions/"+mainMeta.ID, nil)
	f.api.HandleSessions(rec, req)
	require.Equal(t, http.StatusConflict, rec.Code, "a protected main's delete must be refused; body=%s", rec.Body.String())

	still, err := f.store.GetMeta(mainMeta.ID)
	require.NoError(t, err)
	require.NotNil(t, still, "the refused delete must not touch the main session row")
	_, visible := f.reg.Get(cardMain.CardID)
	require.True(t, visible, "the refused delete must clean nothing — the card stays live")
	require.Equal(t, 1, f.reg.PendingCount(), "no registry state may change on a refused delete")
}

// TestAskUserWorkspaceMainReleaseClearsQueue pins F4 Decision A on the REAL
// workspace-delete cascade (rest_workspaces.go::releaseMainSessionsForWorkspace):
// that path deletes main sessions DIRECTLY — it never runs a Stop — so
// without the store's post-success deletion observer NOTHING would ever
// clear the released main's head or FIFO. After the release, the released
// main's complete question state is gone; every other workspace's main and
// card are untouched.
func TestAskUserWorkspaceMainReleaseClearsQueue(t *testing.T) {
	f := qaNewQueueFixture(t)
	mainMeta, err := f.store.GetOrCreateMainSession("wsqa1329rel", "mia")
	require.NoError(t, err)
	// The released main's HEAD is all-default-safe with the fixture's
	// one-hour delay: a REAL scheduled timer is armed the moment it is
	// admitted (round-4 correction C — the timer half of "ALL state" must be
	// exercised on this direct-deletion path, which runs no Stop). The
	// survivor card is non-default-safe: it arms nothing.
	cardHead := qaLifecycleCard(mainMeta.ID, "Workspace main head?", true)
	cardQueued := qaLifecycleCard(mainMeta.ID, "Workspace main queued?", false)
	require.NoError(t, f.reg.CreatePending(cardHead))
	require.NoError(t, f.reg.CreatePending(cardQueued))

	otherMeta, err := f.store.GetOrCreateMainSession("wsqa1329keep", "mia")
	require.NoError(t, err)
	cardKeep := qaLifecycleCard(otherMeta.ID, "Other workspace question?", false)
	require.NoError(t, f.reg.CreatePending(cardKeep))

	complete := releaseMainSessionsForWorkspace(f.api.agentLoop, workspace.Workspace{ID: "wsqa1329rel"})
	require.True(t, complete, "the release cascade must complete for the fixture's single main")

	goneMeta, goneErr := f.store.GetMeta(mainMeta.ID)
	require.True(t, goneErr != nil || goneMeta == nil, "the released main session row must be gone")
	require.Equal(t, 1, f.reg.PendingCount(),
		"the released main's head AND FIFO must be forgotten — this delete path runs no Stop, so only the deletion observer can clear it")
	_, visible := f.reg.Get(cardHead.CardID)
	require.False(t, visible, "the released main's head card must be gone")
	_, visible = f.reg.Get(cardQueued.CardID)
	require.False(t, visible, "the released main's queued card must be gone — the whole FIFO")
	_, visible = f.reg.Get(cardKeep.CardID)
	require.True(t, visible, "another workspace's main card must be untouched")
	keptMeta, keptErr := f.store.GetMeta(otherMeta.ID)
	require.NoError(t, keptErr)
	require.NotNil(t, keptMeta, "another workspace's main session row must be untouched")
	require.Equal(t, 0, f.resume.count(), "the release must dispatch no resume")

	// Timer completion barrier: reachable ONLY after the count assertions
	// passed (a RED short-circuits at them and the cleanup drain stops the
	// one-hour timer). On GREEN the deletion observer forgot the released
	// main's timers, and the survivor card armed none — so Quiesce has
	// nothing scheduled and returns immediately: the gateway-observable proof
	// that no scheduled timer survived the direct deletion. The white-box
	// timer-map proof stays in the pkg/askuser pack.
	f.reg.Quiesce()
}
