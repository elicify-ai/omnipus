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
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wireSession is a minimal decode target for gen.Session — only the fields
// these tests need to assert on.
type wireSession struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// decodeSessionList decodes a listSessions response body. ADR-057 FR-091
// (grill2 M2-10) retired the pre-existing two-variant oneOf (a bare JSON
// array, or {"sessions": [...], "partial_errors": [...]}
// gen.ListSessions200JSONResponseBody1) in favor of a single named
// gen.SessionPage envelope ({"sessions": [...], "next_cursor"?,
// "partial_errors"?}) that listSessions now always returns — greenfield
// permits retiring the bare-array variant outright (operator decision 1).
func decodeSessionList(t *testing.T, body []byte) []wireSession {
	t.Helper()
	var page struct {
		Sessions []wireSession `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(body, &page), "listSessions response must decode as a SessionPage envelope; body=%s", string(body))
	return page.Sessions
}

func sessionIDs(sessions []wireSession) []string {
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.ID)
	}
	return ids
}

// --- FR-036 creation-side verifier spoof guard -------------------------------

// TestCreateSession_TypeVerifierSpoof_CoercedToChat verifies POST
// /api/v1/sessions can never MINT a verifier-type session directly: a client
// that supplies {"type":"verifier"} does not get a verifier session back —
// createSessionHTTP's switch (rest.go's createSessionHTTP, ~L1122-1134)
// recognizes only "task" and "channel" as explicit types and falls through
// to its `default` branch (SessionTypeChat) for anything else, including
// "verifier". Only store.NewVerifierSession (never client-reachable) mints
// the real thing — see TestListSessions_IncludesVerifierWithParam above for
// that path.
//
// Traces to: FR-036 (creation-side spoof guard, gap-sweep fix-wave-2 finding #3).
func TestCreateSession_TypeVerifierSpoof_CoercedToChat(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()

	body := `{"type":"verifier"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createSessionHTTP(w, r)

	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var created wireSession
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.NotEqual(t, "verifier", created.Type, "a client-supplied type:verifier must never mint a real verifier session")
	assert.Equal(t, "chat", created.Type, "an unrecognized/disallowed type coerces to chat, the switch's default branch")
}

// TestCreateSession_TypeVerifierSpoof_RejectedWithValidateInboundEnabled
// verifies the SAME spoof attempt 400s at the schema gate when
// gateway.validate_inbound is enabled: SessionCreateRequest.yaml's type enum
// is intentionally narrower than Session.type (chat|task|channel only,
// EXCLUDING verifier and scheduled — both are server-minted only, never
// client-created via POST /sessions). With validation on, the malformed
// body never even reaches the coercion switch exercised by the test above.
//
// Traces to: FR-036 (creation-side spoof guard, gap-sweep fix-wave-2 finding #3).
func TestCreateSession_TypeVerifierSpoof_RejectedWithValidateInboundEnabled(t *testing.T) {
	api, cleanup := newTestRestAPI(t)
	defer cleanup()
	api.agentLoop.GetConfig().Gateway.ValidateInbound = true

	body := `{"type":"verifier"}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = withAdminRole(r)

	api.createSessionHTTP(w, r)

	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "SessionCreateRequest", "must reject at the schema gate, referencing the schema name")
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
