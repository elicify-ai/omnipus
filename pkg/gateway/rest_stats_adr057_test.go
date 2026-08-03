// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// rest_stats_adr057_test.go — tests for ADR-057 unit U25 (pkg/gateway/gateway.go's
// u25AllSessionsForUsage, pkg/gateway/rest_stats.go's HandleTokenStats): W16i,
// wiring the list-consumer call sites onto U9's paginated
// AgentLoop.ListAllSessions(limit, offset int, parentSessionID string, flat bool)
// signature.
//
// Per the ADR-057 session-unification spec's binding rule 5, every unit's new
// tests go in a NEW file named `<subject>_adr057_test.go`; this is U25's. Rule
// 6: this file's own new package-level helpers are prefixed `u25` so a
// same-wave collision with another unit's new package-level test helper is a
// compile error, not a silent shadow.
//
// Binding rule 1 (real state, never a spy): every test below drives a REAL
// *agent.AgentLoop backed by a REAL session.UnifiedStore (via
// newTestRestAPIWithHome → mustAgentLoop), and stamps token stats through the
// REAL production write path (session.UnifiedStore.AppendTranscriptStrict) —
// never a hand-built in-memory *session.UnifiedMeta slice or a spy closure
// that just returns a fixture. Nothing here asserts "a function was called";
// every assertion lands on a real HTTP response body, a real tool result, or
// a real *session.UnifiedMeta read back from the store.
//
// The substantive risk this file exists to prove/disprove (FR-104): a
// roots-only session listing silently drops a delegated child's token spend
// from usage accounting, because AgentLoop.ListAllSessions' hierarchy filter
// (FR-091) excludes any session whose ParentSessionID resolves to another
// session in the same merge.
//
//   - TestU25ListAllSessions_RootsOnly_UndercountsDelegatedChildSpend is the
//     RED case: it shows the raw mechanism (flat=false) would under-count if
//     it were ever used at these call sites. Binding rule 4 (positive lower
//     bound before any exclusion/zero-count assertion): it first proves the
//     child row carries real, non-zero, larger-than-the-parent's spend in the
//     full merged (flat=true) set, before trusting the roots-only exclusion.
//   - TestU25HandleTokenStats_FlatTrue_IncludesDelegatedChildSpend and
//     TestU25AllSessionsForUsage_FeedsGetUsageWithDelegatedChildSpend are the
//     GREEN cases: they exercise the ACTUAL shipped call sites end to end
//     (rest_stats.go's HandleTokenStats over a real HTTP request/response;
//     gateway.go's u25AllSessionsForUsage feeding a real get_usage tool
//     Execute call) and prove both correctly include the child's spend.
//
// Corollary "distinct ids everywhere" (mirrors U18's rest_adr057_test.go):
// every parent/child pair below is constructed as two distinct, non-equal ids.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// ---------------------------------------------------------------------
// Shared fixtures — real store-backed parent/child sessions with real,
// production-path token spend (binding rule 1).
// ---------------------------------------------------------------------

// u25RecordAssistantTokens appends one real assistant transcript entry via
// AppendTranscriptStrict — the production stats-accumulation path (mirrors
// what pkg/agent/turn.go does after a real LLM call) — so a session's
// Stats.TokensTotal/TokensOut/ByModel become non-zero through the real store,
// never via a hand-set struct field.
func u25RecordAssistantTokens(t *testing.T, store *session.UnifiedStore, sessionID, agentID, model string, tokens int) {
	t.Helper()
	require.NoError(t, store.AppendTranscriptStrict(sessionID, session.TranscriptEntry{
		Role:      "assistant",
		Content:   "u25 test assistant reply",
		Timestamp: time.Now().UTC(),
		Tokens:    tokens,
		Model:     model,
		AgentID:   agentID,
	}), "AppendTranscriptStrict must succeed for session %q", sessionID)
}

// u25NewParentWithTokens mints a real root chat session via NewSession and
// records tokens of real assistant-turn spend on it.
func u25NewParentWithTokens(t *testing.T, store *session.UnifiedStore, agentID, model string, tokens int) *session.UnifiedMeta {
	t.Helper()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", agentID)
	require.NoError(t, err, "NewSession must succeed")
	u25RecordAssistantTokens(t, store, meta.ID, agentID, model, tokens)
	return meta
}

// u25NewDelegatedChildWithTokens mints a real store-backed delegate-type
// child session under parentID (which must already be a real, existing
// session — CreateSessionWithID's doc comment) and records tokens of real
// assistant-turn spend on it — the shape a delegated child has post-D1
// (FR-005/FR-008). SetMeta's explicit ParentSessionID stamp mirrors U18's
// documented finding (rest_adr057_test.go's u18SetParent): CreateSessionWithID
// does not itself persist ParentSessionID; pkg/agent/subturn.go's
// spawnSubTurn closes that gap in production.
func u25NewDelegatedChildWithTokens(t *testing.T, store *session.UnifiedStore, childID, parentID, agentID, model string, tokens int) *session.UnifiedMeta {
	t.Helper()
	require.NotEqual(t, parentID, childID, "fixture defect: parent and child ids must be distinct")
	meta, err := store.CreateSessionWithID(childID, parentID, session.SessionTypeDelegate, "webchat", agentID)
	require.NoError(t, err, "CreateSessionWithID(%q) must succeed", childID)
	require.NoError(t, store.SetMeta(childID, session.MetaPatch{ParentSessionID: &parentID}),
		"SetMeta must persist ParentSessionID")
	u25RecordAssistantTokens(t, store, childID, agentID, model, tokens)
	return meta
}

// u25FilterByID returns only the sessions in all whose ID is in ids — used so
// assertions are immune to noise from any other session a shared test store
// might contain.
func u25FilterByID(all []*session.UnifiedMeta, ids map[string]bool) []*session.UnifiedMeta {
	var out []*session.UnifiedMeta
	for _, m := range all {
		if ids[m.ID] {
			out = append(out, m)
		}
	}
	return out
}

func u25SumTokensTotal(metas []*session.UnifiedMeta) int {
	total := 0
	for _, m := range metas {
		total += m.Stats.TokensTotal
	}
	return total
}

// ---------------------------------------------------------------------
// RED case — the mechanism, used the wrong way, really does under-count.
// ---------------------------------------------------------------------

// TestU25ListAllSessions_RootsOnly_UndercountsDelegatedChildSpend is the RED
// case FR-104 warns about: a roots-only (flat=false) AgentLoop.ListAllSessions
// call drops a delegated child's real token spend from the merged set
// entirely, because FR-091's hierarchy filter excludes any session whose
// ParentSessionID resolves to another session present in the same merge.
//
// This is deliberately NOT what either of U25's shipped call sites do — both
// pass flat=true (see the GREEN tests below). This test exists to prove the
// under-count risk the ADR-057 spec asked this unit to weigh was real, not
// hypothetical, before recording flat=true as the correct decision.
//
// The child is given MORE tokens than the parent (900 vs 400) on purpose:
// under ADR-057's D1, heavy work routinely happens in a delegated child, so
// silently dropping it is not a rounding error — it can be the majority of
// real spend.
func TestU25ListAllSessions_RootsOnly_UndercountsDelegatedChildSpend(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be initialized")

	const parentTokens = 400
	const childTokens = 900
	parent := u25NewParentWithTokens(t, store, "u25-agent-red", "test-model", parentTokens)
	child := u25NewDelegatedChildWithTokens(t, store, "u25-child-red-"+parent.ID, parent.ID, "u25-agent-red", "test-model", childTokens)
	ids := map[string]bool{parent.ID: true, child.ID: true}

	// Positive lower bound (binding rule 4): prove the child row genuinely
	// carries real, non-zero spend in the full merged set BEFORE trusting the
	// exclusion assertion below — otherwise a broken fixture (e.g. the child
	// never actually being written) would make the roots-only assertion pass
	// vacuously.
	flatPage, flatErrs := api.agentLoop.ListAllSessions(0, 0, "", true)
	require.Empty(t, flatErrs, "flat listing must not error")
	flatOurs := u25FilterByID(flatPage.Sessions, ids)
	require.Len(t, flatOurs, 2, "flat=true must return both parent and child")
	require.Equal(t, parentTokens+childTokens, u25SumTokensTotal(flatOurs),
		"positive lower bound: the full merged set must carry both sessions' real combined spend")

	// RED case: roots-only excludes the delegated child entirely.
	rootsPage, rootsErrs := api.agentLoop.ListAllSessions(0, 0, "", false)
	require.Empty(t, rootsErrs, "roots-only listing must not error")
	rootsOurs := u25FilterByID(rootsPage.Sessions, ids)
	require.Len(t, rootsOurs, 1, "roots-only must exclude the delegated child (FR-091)")
	require.Equal(t, parent.ID, rootsOurs[0].ID, "the one surviving row must be the parent, not the child")

	undercounted := u25SumTokensTotal(rootsOurs)
	require.Equal(t, parentTokens, undercounted)
	require.Less(t, undercounted, parentTokens+childTokens,
		"FR-104: roots-only must under-count relative to the true combined total — "+
			"this is the exact defect class flat=true (used by both real call sites below) avoids")
}

// ---------------------------------------------------------------------
// GREEN cases — the actual shipped call sites get it right.
// ---------------------------------------------------------------------

// TestU25HandleTokenStats_FlatTrue_IncludesDelegatedChildSpend is the GREEN
// case for rest_stats.go's HandleTokenStats (U25's fix at the REST layer): the
// ACTUAL shipped handler is called end to end over a real HTTP request and
// response, and its reported per-agent total includes the delegated child's
// spend that the RED test above shows flat=false would have dropped.
func TestU25HandleTokenStats_FlatTrue_IncludesDelegatedChildSpend(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be initialized")

	const agentID = "u25-stats-agent"
	const parentTokens = 120
	const childTokens = 380
	parent := u25NewParentWithTokens(t, store, agentID, "test-model", parentTokens)
	child := u25NewDelegatedChildWithTokens(t, store, "u25-stats-child-"+parent.ID, parent.ID, agentID, "test-model", childTokens)
	require.NotEqual(t, parent.ID, child.ID)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/stats/tokens?period=all", nil)
	api.HandleTokenStats(w, r)
	require.Equal(t, http.StatusOK, w.Code, "GET /stats/tokens must return 200; body=%s", w.Body.String())

	var resp struct {
		Agents []struct {
			AgentId     string `json:"agent_id"`
			TokensTotal int    `json:"tokens_total"`
		} `json:"agents"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	found := false
	for _, a := range resp.Agents {
		if a.AgentId == agentID {
			found = true
			require.Equal(t, parentTokens+childTokens, a.TokensTotal,
				"HandleTokenStats must include the delegated child's spend (FR-104): "+
					"got %d, want parent(%d)+child(%d)=%d", a.TokensTotal, parentTokens, childTokens, parentTokens+childTokens)
		}
	}
	require.True(t, found, "agent %q must appear in the token stats response; body=%s", agentID, w.Body.String())
}

// TestU25AllSessionsForUsage_FeedsGetUsageWithDelegatedChildSpend is the GREEN
// case for gateway.go's u25AllSessionsForUsage — the closure wired into
// systools.Deps.ListSessions in production (see gateway.go's sysAgentDeps
// construction). It proves that closure correctly feeds the REAL get_usage
// tool (pkg/sysagent/tools/diag.go's UsageQueryTool) every session including
// delegated children, end to end through a real tool Execute call — not a
// spy standing in for the wiring.
func TestU25AllSessionsForUsage_FeedsGetUsageWithDelegatedChildSpend(t *testing.T) {
	api := newTestRestAPIWithHome(t)
	store := api.agentLoop.GetSessionStore()
	require.NotNil(t, store, "shared session store must be initialized")

	const agentID = "u25-usage-agent"
	const parentTokens = 55
	const childTokens = 145
	parent := u25NewParentWithTokens(t, store, agentID, "test-model", parentTokens)
	child := u25NewDelegatedChildWithTokens(t, store, "u25-usage-child-"+parent.ID, parent.ID, agentID, "test-model", childTokens)
	require.NotEqual(t, parent.ID, child.ID)

	deps := &systools.Deps{
		GetCfg:       func() *config.Config { return api.agentLoop.GetConfig() },
		ListSessions: u25AllSessionsForUsage(api.agentLoop),
	}
	tool := systools.NewUsageQueryTool(deps)

	res := tool.Execute(context.Background(), map[string]any{
		"period":   "all",
		"agent_id": agentID,
	})
	require.False(t, res.IsError, "get_usage must succeed: %s", res.ForLLM)

	var body struct {
		Total struct {
			Total int `json:"total"`
		} `json:"total"`
	}
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &body))
	require.Equal(t, parentTokens+childTokens, body.Total.Total,
		"get_usage (via u25AllSessionsForUsage's flat=true wiring) must include the "+
			"delegated child's spend (FR-104); resp=%s", res.ForLLM)
}
