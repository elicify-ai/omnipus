//go:build !cgo

// Tests for verifier-session visibility (ADR-052 FR-036, US-13 Acceptance 6,
// SC-014):
//   - GET /api/v1/sessions excludes type="verifier" sessions by default.
//   - GET /api/v1/sessions?include_verifier=true includes them.
//   - The exclusion applies REGARDLESS of an explicit ?type=verifier filter —
//     include_verifier is a separate, mandatory gate (openapi.yaml listSessions
//     description).
//   - GET /api/v1/stats/tokens (the usage/cost aggregation path) is NOT
//     filtered by session type at all, so verifier LLM spend stays visible
//     even though the sessions themselves are hidden from the general list.
//
// Traces to: pkg/gateway/rest.go listSessions, pkg/gateway/rest_stats.go
// HandleTokenStats, pkg/session/unified.go SessionTypeVerifier/NewVerifierSession.

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// wireSession is a minimal decode target for gen.Session — only the fields
// these tests need to assert on.
type wireSession struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// decodeSessionList decodes a listSessions response body, which is either a
// bare JSON array (no partial errors) or {"sessions": [...], "partial_errors": [...]}
// (gen.ListSessions200JSONResponseBody1). Tests in this file never trigger
// partial errors, so a bare-array decode is expected; we fail loudly if the
// shape is unexpectedly the object form so the test doesn't silently pass on
// an empty slice.
func decodeSessionList(t *testing.T, body []byte) []wireSession {
	t.Helper()
	var out []wireSession
	require.NoError(t, json.Unmarshal(body, &out), "listSessions response must decode as a bare array; body=%s", string(body))
	return out
}

func sessionIDs(sessions []wireSession) []string {
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

// TestListSessions_ExcludesVerifierByDefault verifies GET /api/v1/sessions
// (no include_verifier param) omits verifier-type sessions while still
// returning ordinary chat sessions.
//
// BDD: Given a chat session and a verifier session both exist,
// When GET /api/v1/sessions is called with no include_verifier param,
// Then only the chat session appears in the response.
//
// Traces to: FR-036, US-13 Acceptance 6, Test 25.
func TestListSessions_ExcludesVerifierByDefault(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be available")

	chatMeta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	verifierMeta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
	api.listSessions(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	got := decodeSessionList(t, w.Body.Bytes())

	ids := sessionIDs(got)
	assert.Contains(t, ids, chatMeta.ID, "chat session must be present")
	assert.NotContains(t, ids, verifierMeta.ID, "verifier session must be excluded by default")
}

// TestListSessions_IncludesVerifierWithParam verifies GET
// /api/v1/sessions?include_verifier=true surfaces verifier-type sessions
// alongside ordinary ones.
//
// BDD: Given a chat session and a verifier session both exist,
// When GET /api/v1/sessions?include_verifier=true is called,
// Then both sessions appear in the response.
//
// Traces to: FR-036, US-13 Acceptance 6, Test 25.
func TestListSessions_IncludesVerifierWithParam(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	chatMeta, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	verifierMeta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?include_verifier=true", nil)
	api.listSessions(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	got := decodeSessionList(t, w.Body.Bytes())

	ids := sessionIDs(got)
	assert.Contains(t, ids, chatMeta.ID, "chat session must still be present")
	assert.Contains(t, ids, verifierMeta.ID, "verifier session must be included with include_verifier=true")

	// The returned verifier entry must round-trip its type as "verifier" on
	// the wire (not silently coerced/omitted).
	for _, s := range got {
		if s.ID == verifierMeta.ID {
			assert.Equal(t, "verifier", s.Type, "wire type must be verifier")
		}
	}
}

// TestListSessions_TypeFilterVerifierAloneStillExcluded verifies that an
// explicit ?type=verifier filter, WITHOUT include_verifier=true, still
// excludes verifier sessions — per openapi.yaml's listSessions description:
// "Verifier-role sessions ... are excluded by default regardless of the type
// filter unless include_verifier=true is passed." Combining both params is
// required to actually retrieve them via the type filter.
//
// Traces to: FR-036, contracts/openapi.yaml listSessions description.
func TestListSessions_TypeFilterVerifierAloneStillExcluded(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	verifierMeta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	// type=verifier alone -> still excluded.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?type=verifier", nil)
	api.listSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	got := decodeSessionList(t, w.Body.Bytes())
	assert.Empty(t, got, "type=verifier alone must NOT surface verifier sessions; body=%s", w.Body.String())

	// type=verifier + include_verifier=true -> now included.
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?type=verifier&include_verifier=true", nil)
	api.listSessions(w2, r2)
	require.Equal(t, http.StatusOK, w2.Code, "body=%s", w2.Body.String())
	got2 := decodeSessionList(t, w2.Body.Bytes())
	require.Len(t, got2, 1, "type=verifier + include_verifier=true must return exactly the verifier session; body=%s", w2.Body.String())
	assert.Equal(t, verifierMeta.ID, got2[0].ID)
}

// TestListSessions_IncludeVerifierFalseExplicit verifies that an explicit
// include_verifier=false behaves identically to the param being absent
// (belt-and-braces on the strconv.ParseBool default-false path).
func TestListSessions_IncludeVerifierFalseExplicit(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	verifierMeta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/sessions?include_verifier=false", nil)
	api.listSessions(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	got := decodeSessionList(t, w.Body.Bytes())
	assert.NotContains(t, sessionIDs(got), verifierMeta.ID)
}

// TestHandleTokenStats_IncludesVerifierSessionSpend verifies that the
// usage/cost aggregation path (GET /api/v1/stats/tokens) counts token spend
// from verifier sessions, even though those same sessions are hidden from
// GET /api/v1/sessions by default. This is SC-014's "100% of UsageScreen
// cost reporting" requirement — the aggregator must NOT filter by session
// type at all (it calls agentLoop.ListAllSessions() directly, bypassing
// listSessions' include_verifier gate entirely).
//
// BDD: Given a verifier session owned by "judge" with recorded token usage,
// When GET /api/v1/stats/tokens?period=all is called,
// Then the "judge" agent's token totals include the verifier session's spend.
//
// Traces to: FR-036, SC-014.
func TestHandleTokenStats_IncludesVerifierSessionSpend(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	verifierMeta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)

	// Record token spend on the verifier session the same way a real
	// verifier turn would: an assistant transcript entry with Tokens set.
	// AppendTranscript attributes assistant-role tokens to TokensOut and
	// always adds to TokensTotal (pkg/session/unified.go AppendTranscript).
	err = store.AppendTranscript(verifierMeta.ID, session.TranscriptEntry{
		ID:      "verify-entry-1",
		Role:    "assistant",
		Content: "verdict",
		Tokens:  777,
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens?period=all", nil)
	r.URL.RawQuery = "period=all"
	api.HandleTokenStats(w, r)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Agents []struct {
			AgentID     string `json:"agent_id"`
			TokensOut   int    `json:"tokens_out"`
			TokensTotal int    `json:"tokens_total"`
		} `json:"agents"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	var judgeEntry *struct {
		AgentID     string
		TokensOut   int
		TokensTotal int
	}
	for _, a := range resp.Agents {
		if a.AgentID == "judge" {
			judgeEntry = &struct {
				AgentID     string
				TokensOut   int
				TokensTotal int
			}{a.AgentID, a.TokensOut, a.TokensTotal}
		}
	}
	require.NotNil(t, judgeEntry, "judge (the verifier owner) must appear in usage/cost aggregation; agents=%+v", resp.Agents)
	assert.Equal(t, 777, judgeEntry.TokensOut, "verifier session's TokensOut must be counted")
	assert.Equal(t, 777, judgeEntry.TokensTotal, "verifier session's TokensTotal must be counted")
}

// TestHandleTokenStats_VerifierSpendCountedAlongsideChat is a companion
// positive-data test asserting the usage aggregator sums BOTH a normal chat
// session's tokens AND a verifier session's tokens for the SAME agent id,
// proving the verifier session is additive to (not a substitute for or
// excluded from) ordinary usage — i.e. no accidental double-count guard is
// silently dropping one of the two.
func TestHandleTokenStats_VerifierSpendCountedAlongsideChat(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store)

	chatMeta, err := store.NewSession(session.SessionTypeChat, "webchat", "judge")
	require.NoError(t, err)
	require.NoError(t, store.AppendTranscript(chatMeta.ID, session.TranscriptEntry{
		ID: "chat-entry-1", Role: "assistant", Content: "hi", Tokens: 100,
	}))

	verifierMeta, err := store.NewVerifierSession("judge")
	require.NoError(t, err)
	require.NoError(t, store.AppendTranscript(verifierMeta.ID, session.TranscriptEntry{
		ID: "verify-entry-1", Role: "assistant", Content: "verdict", Tokens: 50,
	}))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens?period=all", nil)
	r.URL.RawQuery = "period=all"
	api.HandleTokenStats(w, r)
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Agents []struct {
			AgentID   string `json:"agent_id"`
			TokensOut int    `json:"tokens_out"`
		} `json:"agents"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	var total int
	found := false
	for _, a := range resp.Agents {
		if a.AgentID == "judge" {
			total = a.TokensOut
			found = true
		}
	}
	require.True(t, found, "judge must appear in usage aggregation")
	assert.Equal(t, 150, total, "chat (100) + verifier (50) tokens must both be counted for the same agent")
}
