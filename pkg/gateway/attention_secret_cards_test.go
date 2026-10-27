// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// attention_secret_cards_test.go — RED pack, FR-091 / BDD-10.2: a pending
// secret-request card lights needs_attention on the owning MAIN in the
// sessions listing the sidebar reads (GET /api/v1/sessions), and the cue
// clears on submit, cancel or stop.
//
// Every expected value derives from docs/internal/specs/omnipus-system-agent-spec.md:
//   - FR-091: "Omnipus's main needs_attention MUST include pending questions,
//     approvals and secret cards of its helpers; a pending secret card counts
//     for any main; unknown attention MUST be shown as unknown."
//   - BDD-10.2 (US-10 AS-2, Outline): card kinds question / approval /
//     secret request / helper approval / helper secret request → the main's
//     needs_attention is true. This pack drives the SECRET rows of that
//     outline (own and helper secret request); the APPROVAL rows are
//     Train-3 scope and are deliberately not asserted here.
//   - BDD-10.3 (US-10 AS-3): no pending card → needs_attention false.
//   - US-10 AC2/AC3 and spec test row 40 (pkg/gateway integration, 10.2+10.3).
//
// The cards are raised by REAL turns (request_secret tool calls inside turns
// the loop consumes — a webchat inbound for mains and root chats, a real
// steered self-delegation for the helper row), never by stamping registry
// entries directly. The only fake at the process edge is the LLM provider.
//
// RED expectation on the pre-implementation base: the positive oracles
// (main lit by the card, cue cleared by submit/cancel/stop) fail at their
// needs_attention assertions because nothing assigns cards to mains. The two
// negative controls (NoMainForTheAgent, NoPendingCardNoCue) pass on both
// bases by design — a control failing on base indicts the fixture, not the
// implementation.
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/require"
)

// attentionPurpose is the card purpose BDD-6.1 requires the card to show; the
// value is this pack's own fixture text, not a pinned product string.
const attentionPurpose = "Attention OpenAI key"

// attentionWS is the fixture workspace. Its record is written to disk so the
// listing's main-visibility guard (mainSessionVisible) lists its mains: a
// main whose workspace record cannot be read is hidden, which would make
// every oracle here vacuous.
const attentionWS = "attws01"

// newAttentionFixture is the WU-6 fixture (real loop, real stores, real K5
// routes, real approval machinery, running bus consumer) plus the workspace
// record the listing's visibility guard reads and the approval registry wired
// onto the REST api (a nil registry reads as UNKNOWN on every main, which
// would turn every "false" assertion here into a vacuous pass).
func newAttentionFixture(t *testing.T) *wu6Fixture {
	t.Helper()
	w := newWU6Fixture(t)
	hbWriteWorkspaceRecord(t, w.api, workspace.Workspace{
		ID:        attentionWS,
		Name:      "Attention",
		Status:    "active",
		CoreTeam:  []string{"omnipus", "mia"},
		CreatedAt: "2026-01-01T00:00:00Z",
		UpdatedAt: "2026-01-01T00:00:00Z",
	})
	w.api.approvalReg = w.reg
	return w
}

// attentionRow is one session row's needs_attention as the wire carries it:
// present=false means the field is absent (unknown on a main, never-present
// on a non-main row).
type attentionRow struct {
	row     gen.Session
	present bool
	value   bool
}

// attentionNeedsAttention GETs the sessions listing the sidebar reads and
// returns id's row with its needs_attention presence. The row must be listed.
func attentionNeedsAttention(t *testing.T, w *wu6Fixture, id string) attentionRow {
	t.Helper()
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?flat=true", nil)
	w.api.HandleSessions(resp, req)
	require.Equal(t, http.StatusOK, resp.Code, "sessions listing: %s", resp.Body.String())
	var page gen.SessionPage
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &page))
	require.Empty(t, page.PartialErrors, "the listing must not be degraded in this proof")
	for _, row := range page.Sessions {
		if row.Id == id {
			if row.NeedsAttention == nil {
				return attentionRow{row: row, present: false}
			}
			return attentionRow{row: row, present: true, value: *row.NeedsAttention}
		}
	}
	t.Fatalf("session %s is not in the listing (%d rows) — the fixture must make it visible", id, len(page.Sessions))
	return attentionRow{}
}

// requireNoRowLit sweeps the listing and fails when any row carries
// needs_attention true — the "no main gives no cue" oracle.
func requireNoRowLit(t *testing.T, w *wu6Fixture) {
	t.Helper()
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?flat=true", nil)
	w.api.HandleSessions(resp, req)
	require.Equal(t, http.StatusOK, resp.Code, "sessions listing: %s", resp.Body.String())
	var page gen.SessionPage
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &page))
	for _, row := range page.Sessions {
		require.False(t, row.NeedsAttention != nil && *row.NeedsAttention,
			"no main may be lit by this card, but session %s reads needs_attention true", row.Id)
	}
}

// raiseCard drives one REAL turn in sessionID (the loop consumes a webchat
// inbound, the scripted model calls request_secret) and waits for the card to
// be pending on that session. Returns the pending card.
func raiseCard(t *testing.T, w *wu6Fixture, sessionID, agentID string) gen.SessionStateSecretRequest {
	t.Helper()
	// request_secret is an ADR-071 lazy (Tier-3) tool: callable only after
	// ToolSearch promotes it into the offered set
	// (pkg/agent/loop_run_turn_tools.go::toolNotOfferedRefusal). The
	// promotion step is part of the real flow — ToolSearch's own description
	// says "After loading, call the tool directly" — so the script drives
	// both steps, exactly like the WU-6 fixture's issueRemove.
	w.provider.setDefaultScript([]providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "att-load-" + sessionID, Type: "function",
			Name:      "ToolSearch",
			Arguments: map[string]any{"names": []any{"request_secret"}}}}},
		{ToolCalls: []providers.ToolCall{{ID: "att-request-" + sessionID, Type: "function",
			Name: "request_secret",
			Arguments: map[string]any{
				"purpose":  attentionPurpose,
				"endpoint": secretCardEndpoint,
			}}}},
	})
	// A plain webchat owner message — NO channel-person Peer and NO
	// InstanceID: request_secret refuses channel-triggered turns by design
	// (BDD-6.6), and an inbound with an instance id derives "a person" as the
	// turn's channel origin (pkg/agent/turn_origin.go::inboundInputOrigin),
	// which makes the turn channel-triggered. The SPA's own chat turn carries
	// neither.
	pubCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, w.bus.PublishInbound(pubCtx, bus.InboundMessage{
		Channel: "webchat", ChatID: "att-" + sessionID,
		SessionID:     sessionID,
		Content:       "please store an API key for " + secretCardEndpoint,
		UserInitiated: true,
		Metadata:      map[string]string{"agent_id": agentID, "workspace_id": attentionWS},
	}))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		// The owner must match the record's ancestry owner: Snapshot filters
		// on it, and an empty owner matches only ownerless records.
		for _, card := range w.al.SecretRequests().Snapshot(wu6Owner, sessionID) {
			if card.SessionId == sessionID && card.State == "pending" {
				require.NotEmpty(t, card.Fields, "the pending card must carry its requested fields (K5 submits exactly those)")
				return card
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("BLOCKED: no pending secret card appeared on session %s within the wait — the real request_secret turn must raise it (US-6 AS-2, FR-068)", sessionID)
	return gen.SessionStateSecretRequest{}
}

// requireMainReads fails unless id's row carries needs_attention == want.
func requireMainReads(t *testing.T, w *wu6Fixture, id string, want bool) {
	t.Helper()
	got := attentionNeedsAttention(t, w, id)
	require.True(t, got.present, "main %s must carry needs_attention on the wire (present=true/false on valid mains, omitted only for unknown)", id)
	require.Equal(t, want, got.value, "main %s needs_attention", id)
}

// cardK5Get GETs the card through the real K5 route and returns its state.
func cardK5Get(t *testing.T, w *wu6Fixture, requestID string) gen.SecretRequestState {
	t.Helper()
	resp := w.do(http.MethodGet, "/api/v1/secret-requests/"+requestID, "")
	require.Equal(t, http.StatusOK, resp.Code, "K5 GET: %s", resp.Body.String())
	var state gen.SecretRequestState
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &state))
	return state
}

// attentionSubmitValues submits the card's requested fields through the real
// K5 submit route with the fixture value.
func attentionSubmitValues(t *testing.T, w *wu6Fixture, card gen.SessionStateSecretRequest) gen.SecretRequestState {
	t.Helper()
	values := map[string]string{}
	for _, field := range card.Fields {
		values[field.Key] = cardMatrixHTTPValue
	}
	raw, err := json.Marshal(gen.SecretSubmitRequest{Values: &values})
	require.NoError(t, err)
	resp := w.do(http.MethodPost, "/api/v1/secret-requests/"+card.RequestId+"/submit", string(raw))
	require.Equal(t, http.StatusOK, resp.Code, "K5 submit: %s", resp.Body.String())
	var state gen.SecretRequestState
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &state))
	return state
}

// TestAttentionSecretCard_PendingCardInMainLightsThatMain is BDD-10.2's
// "secret request" row and US-10 AC2: a pending card in Omnipus's own main
// chat lights that main — and only that main.
func TestAttentionSecretCard_PendingCardInMainLightsThatMain(t *testing.T) {
	w := newAttentionFixture(t)
	omniMain := w.mainSession(attentionWS, "omnipus")
	miaMain := w.mainSession(attentionWS, "mia")

	// Instrument: the mains are listed before any card exists, so a failure
	// below is the card→main mapping, never a visibility gap.
	requireMainReads(t, w, omniMain, false)

	card := raiseCard(t, w, omniMain, "omnipus")
	require.Equal(t, omniMain, card.SessionId, "the card belongs to the main chat it was raised in")

	requireMainReads(t, w, omniMain, true)
	requireMainReads(t, w, miaMain, false)
}

// TestAttentionSecretCard_HelperCardLightsTheMainNotTheHelperRow is FR-091's
// helper clause and BDD-10.2's "helper secret request" row: a card a helper
// chat raises lights the main the helper hangs from (the parent walk), while
// the helper's own row never carries the field (only mains do).
func TestAttentionSecretCard_HelperCardLightsTheMainNotTheHelperRow(t *testing.T) {
	w := newAttentionFixture(t)
	omniMain := w.mainSession(attentionWS, "omnipus")

	// The production helper shape: a real steered self-delegation (BDD-6.11),
	// whose session hangs from the main by a parent edge.
	lifecycle := w.al.GetSessionLifecycleStore()
	classifier := agent.NewSteerRecordClassifier(lifecycle, w.al.GetSessionStore())
	w.al.SetSteerAudienceDeps(agent.NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, agent.NewSteerUpwardDeliverer())
	launcher := agent.NewSteerLauncher(w.al)

	w.provider.setDefaultScript([]providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "att-helper-load", Type: "function",
			Name:      "ToolSearch",
			Arguments: map[string]any{"names": []any{"request_secret"}}}}},
		{ToolCalls: []providers.ToolCall{{ID: "att-helper-request", Type: "function",
			Name: "request_secret",
			Arguments: map[string]any{
				"purpose":  attentionPurpose,
				"endpoint": secretCardEndpoint,
			}}}},
	})
	child, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: omniMain, TargetAgentID: "omnipus", Label: "Attention helper",
		Task:   "Request the setup key on the secure card.",
		Origin: steer.Origin{Kind: steer.OriginKindDelegate, CallID: "att-helper"},
	})
	require.NoError(t, err, "fixture: real self-delegation must be available")
	_, err = launcher.Dispatch(context.Background(), child.SessionID, child.Generation)
	require.NoError(t, err)

	var card gen.SessionStateSecretRequest
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		// Owner must match the record's ancestry owner (inherited from the
		// main the fixture stamped); "" matches only ownerless records.
		for _, pending := range w.al.SecretRequests().Snapshot(wu6Owner, child.SessionID) {
			if pending.SessionId == child.SessionID && pending.State == "pending" {
				card = pending
			}
		}
		if card.RequestId != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.NotEmpty(t, card.RequestId, "BLOCKED: the steered helper's real request_secret turn never raised its card (BDD-6.11)")
	require.Equal(t, omniMain, *card.RootSessionId, "the helper card belongs under the main chat (BDD-6.11)")

	requireMainReads(t, w, omniMain, true)
	helperRow := attentionNeedsAttention(t, w, child.SessionID)
	require.False(t, helperRow.present, "a non-main row must not carry needs_attention on the wire (only mains do)")
}

// TestAttentionSecretCard_NonMainRootChatLightsTheAgentMainInItsWorkspace is
// FR-091's "a pending secret card counts for any main": a card raised in a
// plain non-main chat (no parent) lights that chat's agent's main in the
// chat's workspace.
func TestAttentionSecretCard_NonMainRootChatLightsTheAgentMainInItsWorkspace(t *testing.T) {
	w := newAttentionFixture(t)
	miaMain := w.mainSession(attentionWS, "mia")
	chat, err := w.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	ws := attentionWS
	owner := wu6Owner
	require.NoError(t, w.al.GetSessionStore().SetMeta(chat.ID, session.MetaPatch{WorkspaceID: &ws, Owner: &owner}))

	card := raiseCard(t, w, chat.ID, "mia")
	require.Equal(t, chat.ID, card.SessionId, "the card lives in the chat that raised it")

	requireMainReads(t, w, miaMain, true)
	chatRow := attentionNeedsAttention(t, w, chat.ID)
	require.False(t, chatRow.present, "a non-main row must not carry needs_attention on the wire")
}

// TestAttentionSecretCard_NoMainForTheAgent_NoCueAnywhereCardStillShows is the
// dispatch's "no main gives no cue, but the card still shows in its own chat":
// a card whose agent has no main in the workspace lights no session at all,
// and the card itself stays pending in its own chat (K5 GET).
func TestAttentionSecretCard_NoMainForTheAgent_NoCueAnywhereCardStillShows(t *testing.T) {
	w := newAttentionFixture(t)
	omniMain := w.mainSession(attentionWS, "omnipus")
	// Deliberately NO main for mia in attentionWS.
	chat, err := w.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	ws := attentionWS
	owner := wu6Owner
	require.NoError(t, w.al.GetSessionStore().SetMeta(chat.ID, session.MetaPatch{WorkspaceID: &ws, Owner: &owner}))

	card := raiseCard(t, w, chat.ID, "mia")

	requireNoRowLit(t, w)
	requireMainReads(t, w, omniMain, false)

	state := cardK5Get(t, w, card.RequestId)
	require.Equal(t, gen.SecretRequestStateStatePending, state.State,
		"the card must still show in its own chat — no main is no licence to swallow it")
}

// TestAttentionSecretCard_SubmitClearsTheCue: after the person submits through
// the real K5 route, the main's cue is false again (BDD-10.2's pending is the
// only lit state; FR-087's submitted state ends it).
func TestAttentionSecretCard_SubmitClearsTheCue(t *testing.T) {
	w := newAttentionFixture(t)
	omniMain := w.mainSession(attentionWS, "omnipus")

	card := raiseCard(t, w, omniMain, "omnipus")
	requireMainReads(t, w, omniMain, true)

	state := attentionSubmitValues(t, w, card)
	require.Equal(t, gen.SecretRequestStateStateSubmitted, state.State, "K5 submit")
	require.NotNil(t, state.ReferenceNames, "a submitted state carries reference names (FR-068)")

	requireMainReads(t, w, omniMain, false)
}

// TestAttentionSecretCard_CancelClearsTheCue: a cancelled card is not pending,
// so the cue clears (dispatch oracle: the cue clears on cancel).
func TestAttentionSecretCard_CancelClearsTheCue(t *testing.T) {
	w := newAttentionFixture(t)
	omniMain := w.mainSession(attentionWS, "omnipus")

	card := raiseCard(t, w, omniMain, "omnipus")
	requireMainReads(t, w, omniMain, true)

	cancel := gen.SecretSubmitRequestCancel(true)
	raw, err := json.Marshal(gen.SecretSubmitRequest{Cancel: &cancel})
	require.NoError(t, err)
	resp := w.do(http.MethodPost, "/api/v1/secret-requests/"+card.RequestId+"/submit", string(raw))
	require.Equal(t, http.StatusOK, resp.Code, "K5 cancel: %s", resp.Body.String())
	var state gen.SecretRequestState
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &state))
	require.Equal(t, gen.SecretRequestStateStateCancelled, state.State, "K5 cancel must record the cancelled state")

	requireMainReads(t, w, omniMain, false)
}

// TestAttentionSecretCard_StopClearsTheCue: an explicit Stop of the chat (the
// one Stop, ADR-082) resolves the pending card as stopped, so the cue clears
// and the card reads stopped through K5.
func TestAttentionSecretCard_StopClearsTheCue(t *testing.T) {
	w := newAttentionFixture(t)
	omniMain := w.mainSession(attentionWS, "omnipus")

	card := raiseCard(t, w, omniMain, "omnipus")
	requireMainReads(t, w, omniMain, true)

	_, _, err := w.al.RequestCancelForSession(context.Background(), omniMain, wu6Owner, "webchat")
	require.NoError(t, err, "the explicit Stop must be accepted")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got := attentionNeedsAttention(t, w, omniMain)
		if got.present && !got.value {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	requireMainReads(t, w, omniMain, false)

	state := cardK5Get(t, w, card.RequestId)
	require.Equal(t, gen.SecretRequestStateStateStopped, state.State,
		"a Stop resolves the pending card as stopped (dispatch oracle: the cue clears on stop)")
}

// TestAttentionSecretCard_NoPendingCardNoCue is BDD-10.3's control: with no
// pending card anywhere, every listed main reads an authoritative false —
// never omitted (unknown), never true.
func TestAttentionSecretCard_NoPendingCardNoCue(t *testing.T) {
	w := newAttentionFixture(t)
	omniMain := w.mainSession(attentionWS, "omnipus")
	miaMain := w.mainSession(attentionWS, "mia")

	requireMainReads(t, w, omniMain, false)
	requireMainReads(t, w, miaMain, false)
}
