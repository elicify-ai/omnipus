// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// session_attention_u11_red_test.go — RED pack, session-core U11 (FR-047,
// BDD-13.1..13.4; C-ATTENTION). Every expected value here derives from
// docs/internal/specs/session-core-spec.md, never from the implementation.
//
// On this branch only the WIRE FIELDS exist (Session.needs_attention,
// SessionStateFrame.attention_bound, AttachSessionFrame.ack_attention). No
// production code computes needs_attention, returns an attention_bound, or
// writes the shared seen mark, so every test below fails loudly at its first
// assertion. RED rule: this is the correct failure — do NOT weaken the
// assertion to make it pass.
//
// The tests drive ONLY existing, stable entry points (the REST session
// handler and the WS session_state emit) and inspect the wire bytes the SPA
// actually consumes, so they stay correct whatever internal shape the GREEN
// implementation chooses. Where the spec does not decide a name or a source
// (the seen-mark storage, the pending-approval source, the ack seam) the test
// does NOT invent one — see the report's questions.
package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/askuser"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// u11Agent / u11WorkspaceID are the fixture pair. The workspace id must match
// an existing workspace record (u11WriteWorkspace) so a main computed from the
// pair resolves through the REST read guard (rest_sessions.go::
// resolveMainSessionForRead).
const (
	u11Agent       = "mia"
	u11WorkspaceID = "01JXHBTESTWSID0000000001"
)

// u11RestFixture builds the REST API plus a workspace record and the pair's
// main session, returning the api, the shared store and the main's id.
func u11RestFixture(t *testing.T) (*restAPI, *session.UnifiedStore, string) {
	t.Helper()
	api, _ := buildHeartbeatTestAPI(t)
	hbWriteWorkspaceRecord(t, api, workspace.Workspace{
		ID: u11WorkspaceID, Name: "WS", Status: "active", CoreTeam: []string{u11Agent},
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
	})
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "the shared session store must be wired")
	// Wire an EMPTY approval registry (no pending approvals). A nil registry
	// makes needs_attention UNKNOWN by design (founder: never a false "off"),
	// which would leave every "clean main" case meaningless; an empty registry
	// lets a clean main resolve to an authoritative false. The nil-registry
	// case is covered separately by TestSessionCoreU11_NilApprovalRegistryIsUnknown.
	api.approvalReg = newApprovalRegistryV2(64, 300*time.Second)
	main, err := store.GetOrCreateMainSession(u11WorkspaceID, u11Agent)
	require.NoError(t, err, "GetOrCreateMainSession")
	return api, store, main.ID
}

// TestSessionCoreU11_NilApprovalRegistryIsUnknown pins the founder's rule that a
// source which cannot be read never reports a false "off": with no approval
// registry wired, needs_attention is OMITTED on a clean main (unknown), not
// false. This is the counterpart to the fixture's empty registry.
func TestSessionCoreU11_NilApprovalRegistryIsUnknown(t *testing.T) {
	api, _, mainID := u11RestFixture(t)
	api.approvalReg = nil // wiring fault: the registry was never installed

	detail := u11GetSessionBody(t, api, mainID)
	sessionObj, ok := detail["session"].(map[string]any)
	require.True(t, ok, "detail must carry a session object: %v", detail)
	_, present := u11NeedsAttention(t, sessionObj)
	assert.False(t, present,
		"C-ATTENTION: a nil approval registry leaves needs_attention UNKNOWN (omitted) on a clean main — "+
			"never a false 'off'")
}

// u11GetSessionBody GETs /api/v1/sessions/{id} and returns the decoded
// top-level object ({session, messages}).
func u11GetSessionBody(t *testing.T, api *restAPI, id string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+id, nil)
	r.URL.Path = "/api/v1/sessions/" + id
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET session detail: body=%s", w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

// u11NeedsAttention reads the needs_attention field out of a session object
// (the inner "session" of a detail response, or a row of a list response).
// present=false means the key is absent on the wire.
func u11NeedsAttention(t *testing.T, sessionObj map[string]any) (value bool, present bool) {
	t.Helper()
	raw, ok := sessionObj["needs_attention"]
	if !ok {
		return false, false
	}
	b, ok := raw.(bool)
	require.True(t, ok, "needs_attention must be a JSON boolean, got %T (%v)", raw, raw)
	return b, true
}

// u11AppendGoalOutcome appends a goal_outcome transcript entry to id — the one
// lasting line a goal ending leaves (pkg/agent/goal_outcome.go::
// recordGoalOutcome). It is the spec's "saved outcome ... order" fixture
// (C-ATTENTION). ending is a generated.GoalOutcomeEnding value.
func u11AppendGoalOutcome(t *testing.T, store *session.UnifiedStore, id, goalID string, ending generated.GoalOutcomeEnding) {
	t.Helper()
	outcome := &generated.GoalOutcome{
		GoalId:     goalID,
		GoalText:   "finish the report",
		Ending:     ending,
		RoundsUsed: 2,
		MaxRounds:  10,
		EndedAt:    time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
	}
	err := store.AppendTranscriptStrict(id, session.TranscriptEntry{
		ID:            "goal-outcome-" + goalID,
		Type:          session.EntryTypeSystem,
		Role:          "system",
		Content:       "Goal ended.",
		AgentID:       u11Agent,
		Timestamp:     outcome.EndedAt,
		SystemSubtype: session.SystemSubtypeGoalOutcome,
		GoalOutcome:   outcome,
	})
	require.NoError(t, err, "append goal_outcome entry")
}

// u11SetPendingAsk stamps the canonical durable pending AskUserQuestion set
// (SessionMeta.PendingAskJSON, askuserquestion-tool-spec §0.4) onto id. The
// REST layer can only see durable session state, so this is the pending-ask
// fixture the wire contract describes for "pending structured ask".
func u11SetPendingAsk(t *testing.T, store *session.UnifiedStore, id string) {
	t.Helper()
	set := &askuser.PendingSet{
		CardID:              "ask_u11",
		TranscriptSessionID: id,
		AgentID:             u11Agent,
		Channel:             "webchat",
		ChatID:              "chat-u11",
		Owner:               "daniel",
		CreatedAt:           time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC),
		Status:              askuser.StatusPending,
		Questions: []askuser.Question{{
			Header:   "Scope",
			Question: "Which emails?",
			Options:  []askuser.Option{{Label: "All"}, {Label: "Unanswered"}},
		}},
	}
	raw, err := json.Marshal(set)
	require.NoError(t, err)
	js := string(raw)
	require.NoError(t, store.SetMeta(id, session.MetaPatch{PendingAskJSON: &js}),
		"stamp the pending ask onto the main's metadata")
}

// TestSessionCoreU11_NeedsAttentionPresenceFollowsSessionType pins C-ATTENTION:
// "present true/false on valid mains including default Admin, omitted on
// non-main". A clean main must therefore carry needs_attention == false (an
// authoritative "off"), never omit it; an ordinary chat must omit it.
//
// BDD-13.1: "Stop-pause alone/negative/no-source false."
func TestSessionCoreU11_NeedsAttentionPresenceFollowsSessionType(t *testing.T) {
	api, store, mainID := u11RestFixture(t)

	detail := u11GetSessionBody(t, api, mainID)
	sessionObj, ok := detail["session"].(map[string]any)
	require.True(t, ok, "detail must carry a session object: %v", detail)
	require.Equal(t, "main", sessionObj["type"], "fixture precondition: the session is the pair's main")

	value, present := u11NeedsAttention(t, sessionObj)
	assert.True(t, present,
		"C-ATTENTION: needs_attention must be PRESENT (true/false) on a valid main, not omitted")
	assert.False(t, value,
		"a main with no pending ask/approval and no unseen outcome is not in attention")

	// Control: an ordinary chat (not a main) must OMIT the field entirely
	// (C-ATTENTION "omitted on non-main") — the field is main-only, so a
	// later blanket-write that leaks it onto every session is caught here.
	chat, err := store.NewSession(session.SessionTypeChat, "webchat", u11Agent)
	require.NoError(t, err)
	chatDetail := u11GetSessionBody(t, api, chat.ID)
	chatObj, ok := chatDetail["session"].(map[string]any)
	require.True(t, ok, "chat detail must carry a session object")
	_, chatPresent := u11NeedsAttention(t, chatObj)
	assert.False(t, chatPresent, "C-ATTENTION: needs_attention must be omitted on a non-main")
}

// TestSessionCoreU11_PendingAskMakesMainNeedAttention pins BDD-13.1: a main
// with a pending structured AskUserQuestion is in attention (needs_attention
// true). The durable pending set is the canonical pending-ask record.
func TestSessionCoreU11_PendingAskMakesMainNeedAttention(t *testing.T) {
	api, store, mainID := u11RestFixture(t)
	u11SetPendingAsk(t, store, mainID)

	detail := u11GetSessionBody(t, api, mainID)
	sessionObj := detail["session"].(map[string]any)
	value, present := u11NeedsAttention(t, sessionObj)
	require.True(t, present, "needs_attention must be present on a main")
	assert.True(t, value, "a pending structured ask must make the main need attention (BDD-13.1)")
}

// TestSessionCoreU11_UnseenMetOutcomeMakesMainNeedAttention pins BDD-13.1:
// an unseen met (finished) goal outcome is attention. The outcome entry is
// appended directly (the persisted line); nothing has acknowledged it.
func TestSessionCoreU11_UnseenMetOutcomeMakesMainNeedAttention(t *testing.T) {
	api, store, mainID := u11RestFixture(t)
	u11AppendGoalOutcome(t, store, mainID, "goal-met-u11", generated.GoalOutcomeEndingMet)

	detail := u11GetSessionBody(t, api, mainID)
	sessionObj := detail["session"].(map[string]any)
	value, present := u11NeedsAttention(t, sessionObj)
	require.True(t, present, "needs_attention must be present on a main")
	assert.True(t, value, "an unseen met (finished) outcome must make the main need attention (BDD-13.1)")
}

// TestSessionCoreU11_RoundsExhaustedOutcomeMakesMainNeedAttention pins
// BDD-13.1: rounds_exhausted (failed) is mapped attention, like `other`.
func TestSessionCoreU11_RoundsExhaustedOutcomeMakesMainNeedAttention(t *testing.T) {
	api, store, mainID := u11RestFixture(t)
	u11AppendGoalOutcome(t, store, mainID, "goal-exhausted-u11", generated.GoalOutcomeEndingRoundsExhausted)

	detail := u11GetSessionBody(t, api, mainID)
	sessionObj := detail["session"].(map[string]any)
	value, present := u11NeedsAttention(t, sessionObj)
	require.True(t, present, "needs_attention must be present on a main")
	assert.True(t, value, "an unseen rounds_exhausted outcome must make the main need attention (BDD-13.1)")
}

// TestSessionCoreU11_StoppedByUserOutcomeIsNotAttention pins BDD-13.1's
// negative: a stopped_by_user ending is NOT attention ("not stopped_by_user").
func TestSessionCoreU11_StoppedByUserOutcomeIsNotAttention(t *testing.T) {
	api, store, mainID := u11RestFixture(t)
	u11AppendGoalOutcome(t, store, mainID, "goal-stopped-u11", generated.GoalOutcomeEndingStoppedByUser)

	detail := u11GetSessionBody(t, api, mainID)
	sessionObj := detail["session"].(map[string]any)
	value, present := u11NeedsAttention(t, sessionObj)
	require.True(t, present, "needs_attention must be present on a main")
	assert.False(t, value,
		"a stopped_by_user ending must NOT make the main need attention (BDD-13.1: not stopped_by_user)")
}

// TestSessionCoreU11_ListProjectsAttentionForUnopenedMain pins BDD-13.3's
// unopened-main query path: the Session-list projection (not a live frame) is
// how the SPA learns an unopened main's attention (C-ATTENTION "Live/
// reconnect ... Session-list projection for unopened mains"). A clean main
// listed without ever being attached must still carry the field.
func TestSessionCoreU11_ListProjectsAttentionForUnopenedMain(t *testing.T) {
	api, _, mainID := u11RestFixture(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?flat=true", nil)
	api.HandleSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET session list: body=%s", w.Body.String())
	var page map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &page))
	rows, ok := page["sessions"].([]any)
	require.True(t, ok, "list response must carry sessions[]")

	var found map[string]any
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		if row["id"] == mainID {
			found = row
			break
		}
	}
	require.NotNil(t, found, "the pair's main must be listed; rows=%v", rows)
	value, present := u11NeedsAttention(t, found)
	assert.True(t, present,
		"BDD-13.3: the unopened main's list row must carry needs_attention (the SPA's only signal before attach)")
	assert.False(t, value, "the clean unopened main is not in attention")
}

// u11WSFixture builds a WSHandler over a real agent loop plus the pair's main
// session, returning the handler, the shared store and the main's id — for the
// session_state (attach response) side of C-ATTENTION.
func u11WSFixture(t *testing.T) (*WSHandler, *session.UnifiedStore, string) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpDir)
	workspaceDir := filepath.Join(tmpDir, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 18835},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: workspaceDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
			List: []config.AgentConfig{{ID: u11Agent, Name: "Mia", Home: workspaceDir}},
		},
	}
	seedAgentEntities(t, tmpDir, cfg.Agents.List)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	store := al.GetSessionStore()
	require.NotNil(t, store)
	main, err := store.GetOrCreateMainSession(u11WorkspaceID, u11Agent)
	require.NoError(t, err)
	return handler, store, main.ID
}

// u11ReadSessionState reads one raw session_state frame and extracts its
// attention_bound field, returning (value, present).
func u11ReadSessionState(t *testing.T, ch chan []byte) (int64, bool) {
	t.Helper()
	select {
	case raw := <-ch:
		var f struct {
			Type           string `json:"type"`
			AttentionBound *int64 `json:"attention_bound"`
		}
		require.NoError(t, json.Unmarshal(raw, &f))
		require.Equal(t, "session_state", f.Type)
		if f.AttentionBound == nil {
			return 0, false
		}
		return *f.AttentionBound, true
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for session_state frame")
		return 0, false
	}
}

// TestSessionCoreU11_AttachResponseCarriesAttentionBoundForMain pins the
// architect ruling Q2 (ARCHITECT-ANSWER-ADMIN-MAIN-BOUND.md, 2026-10-10):
// the bound is on the `session_state` frame, which `sendSnapshot` emits right
// after `session_snapshot` and the incremental path emits first, so BOTH
// attach paths carry it in the same place. A main with a saved outcome
// therefore carries a nonnegative attention_bound on its session_state; a
// non-main attach and the connection-open emit carry none. The `session_snapshot`
// frame itself must never carry the field (schema `additionalProperties:false`).
func TestSessionCoreU11_AttachResponseCarriesAttentionBoundForMain(t *testing.T) {
	handler, store, mainID := u11WSFixture(t)
	u11AppendGoalOutcome(t, store, mainID, "goal-bound-u11", generated.GoalOutcomeEndingMet)

	// (1)+(2) main attach: session_state carries the bound (same builder for
	// the incremental path and each snapshot reason).
	wc := &wsConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	handler.emitSessionState(wc, mainID)
	bound, present := u11ReadSessionState(t, wc.sendCh)
	assert.True(t, present,
		"Q2(1/2): the attach session_state for a main with a saved outcome must carry attention_bound")
	assert.GreaterOrEqual(t, bound, int64(0), "attention_bound is a nonnegative saved order")

	// (2) session_snapshot must not carry the bound — the carrier is
	// session_state alone, so the snapshot frame's type has no such field.
	_, hasField := reflect.TypeOf(generated.SessionSnapshotFrame{}).FieldByName("AttentionBound")
	assert.False(t, hasField,
		"Q2(2): session_snapshot must NOT carry attention_bound (one carrier: session_state)")

	// (3) a non-main attach carries no bound.
	chat, err := store.NewSession(session.SessionTypeChat, "webchat", u11Agent)
	require.NoError(t, err)
	wc2 := &wsConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	handler.emitSessionState(wc2, chat.ID)
	_, present2 := u11ReadSessionState(t, wc2.sendCh)
	assert.False(t, present2, "Q2(3): a non-main attach must not carry an attention_bound")

	// (3) the connection-open emit (no session bound yet) carries no bound.
	wc3 := &wsConn{sendCh: make(chan []byte, 8), doneCh: make(chan struct{})}
	handler.emitSessionState(wc3, "")
	_, present3 := u11ReadSessionState(t, wc3.sendCh)
	assert.False(t, present3, "Q2(3): the connection-open session_state must not carry an attention_bound")
}

// TestSessionCoreU11_NegativeAttentionBoundRejectedBySchema pins Q2(4): once
// `minimum: 0` is added to AttachSessionFrame.attention_bound (contract +
// inboundschemas copy + regeneration, backend-lead) a negative value is
// refused by the generated inbound validator. Today neither carrier carries
// the bound, so the validator accepts -1 — this test is RED until that
// contract change lands.
func TestSessionCoreU11_NegativeAttentionBoundRejectedBySchema(t *testing.T) {
	// Control: a well-formed, nonnegative bound is accepted (the validator can
	// say yes), so the rejection below is not vacuous.
	ok := []byte(`{"type":"attach_session","session_id":"main-session-W+m","attention_bound":0}`)
	okMsg, _ := ValidateInboundFrameJSON("AttachSessionFrame", ok)
	require.Empty(t, okMsg, "control: attention_bound 0 must be accepted")

	neg := []byte(`{"type":"attach_session","session_id":"main-session-W+m","attention_bound":-1}`)
	negMsg, _ := ValidateInboundFrameJSON("AttachSessionFrame", neg)
	assert.NotEmpty(t, negMsg,
		"Q2(4): a negative attention_bound must be refused by the schema (minimum: 0)")
}
