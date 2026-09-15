// Omnipus — the knowledge rate budgets: who is asking decides which bucket.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// ---------------------------------------------------------------------------
// WHAT THESE TESTS ARE FOR
//
// The knowledge routes used to share ONE bucket of 60 requests a minute per
// workspace: every account, every tab, reads and writes together. Ordinary
// signed-in work overran it (see knowledgeRESTLimiter's comment). These tests
// pin the replacement from both sides:
//
//   - an ordinary burst of signed-in work is NOT refused, and
//   - a genuine flood still IS, at a stated ceiling, for signed-in callers and
//     for requests with no signed-in account alike.
//
// EXPECTED NUMBERS ARE WRITTEN OUT, NOT READ FROM THE CODE. The ceilings below
// are the documented contract; reading knowledgeSignedInReadsPerMinute back
// here would let a silent change to the constant pass every test. The burst
// sizes are the traffic the ceilings were chosen to absorb, derived from the
// web app's own request pattern and the UAT's measured pace, not from the
// limiter.
//
// EVERY REQUEST IN THE BURSTS GOES THROUGH THE REAL HANDLER, so these tests
// also prove the handler charges the bucket they reason about. A handler that
// still charged the old shared bucket fails the bursts at request 61.
// ---------------------------------------------------------------------------

const (
	budgetSignedInReads  = 600
	budgetSignedInWrites = 120
	budgetAnonymous      = 60
)

// admittedWriteBody is a record write the door refuses with 400 only AFTER the
// limiter has admitted it: a version token on a create. It touches no file, so
// a burst of them is cheap, and it tells an admitted write (400) apart from one
// the limiter refused (429).
func admittedWriteBody() map[string]any {
	return map[string]any{
		"mode":          "create",
		"type":          "widget",
		"path":          "budget.md",
		"version_token": "v1:9f2a7c4081e3b5d6a0c1f2e3b4d5a6c7",
		"properties": []map[string]any{
			{"property": "name", "values": []map[string]any{{"type": "text", "text": "x"}}},
		},
	}
}

// budgetRead is one knowledge READ through the real handler, as `username`
// (empty for no signed-in account). record-schema stands in for every read
// route — find, graph, view, views, one record, files/search all draw on the
// same read budget — because it is the cheapest to evaluate.
func budgetRead(t *testing.T, api *restAPI, ws, username string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/library/"+ws+"/knowledge/record-schema", nil)
	if username != "" {
		r = r.WithContext(context.WithValue(r.Context(), UserContextKey{}, &config.UserConfig{Username: username}))
	}
	api.HandleLibraryTree(w, r)
	return w
}

// budgetWrite is one knowledge WRITE through the real handler, as username.
func budgetWrite(t *testing.T, api *restAPI, ws, username string) *httptest.ResponseRecorder {
	t.Helper()
	return knowledgePostWithUser(t, api, recordsPath(ws), admittedWriteBody(), &config.UserConfig{Username: username})
}

// requireAdmittedRead and requireAdmittedWrite assert the limiter let the call
// through: the read succeeds, the write reaches the door's own 400.
func requireAdmittedRead(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	require.Equalf(t, http.StatusOK, w.Code, "%s must be admitted: %s", what, w.Body.String())
}

func requireAdmittedWrite(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	require.Equalf(t, http.StatusBadRequest, w.Code, "%s must be admitted (and then refused by the door itself): %s", what, w.Body.String())
}

// requireRefusedByBudget asserts w is the knowledge limiter's own refusal —
// not some other 429, and not a success — carrying a usable Retry-After.
func requireRefusedByBudget(t *testing.T, w *httptest.ResponseRecorder, what string) {
	t.Helper()
	require.Equalf(t, http.StatusTooManyRequests, w.Code, "%s must be refused: %s", what, w.Body.String())
	assert.Contains(t, w.Body.String(), "too many knowledge requests", what)
	retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
	require.NoErrorf(t, err, "%s: Retry-After must be whole seconds, got %q", what, w.Header().Get("Retry-After"))
	assert.GreaterOrEqualf(t, retry, 1, "%s: Retry-After must ask the caller to wait at least a second", what)
}

// TestKnowledgeBudget_OrdinaryBurstsAreNotRefused is the half the old single
// bucket failed: two minutes of ordinary signed-in work, each of which the old
// 60-a-minute workspace bucket refused part-way through.
func TestKnowledgeBudget_OrdinaryBurstsAreNotRefused(t *testing.T) {
	t.Run("30 inline edits, each double-submitted, on a dashboard with 5 embedded views", func(t *testing.T) {
		api, ws, _ := buildRecordTestVault(t)
		useFreshKnowledgeLimiter(t)
		// One cell every two seconds for a minute. The text editor submits each
		// edit twice (D-112), and after a write BasePreview reloads every view
		// over the edited collection: 2 writes + 5 reads per edit, 60 writes and
		// 150 reads in all.
		const edits, writesPerEdit, embeddedViews = 30, 2, 5
		for e := 1; e <= edits; e++ {
			for s := 1; s <= writesPerEdit; s++ {
				requireAdmittedWrite(t, budgetWrite(t, api, ws, recordTestUsername),
					"edit "+strconv.Itoa(e)+" submit "+strconv.Itoa(s))
			}
			for v := 1; v <= embeddedViews; v++ {
				requireAdmittedRead(t, budgetRead(t, api, ws, recordTestUsername),
					"edit "+strconv.Itoa(e)+" view reload "+strconv.Itoa(v))
			}
		}
	})

	t.Run("bulk trash at 36 files a minute beside a 5-embed dashboard", func(t *testing.T) {
		api, ws, _ := buildRecordTestVault(t)
		useFreshKnowledgeLimiter(t)
		// D-109 measured 54 deletes in about 90 s: 36 a minute. Each Library
		// write sends library_changed, which reloads every mounted knowledge
		// query in the workspace — up to 5 view results and 5 link graphs. The
		// deletes themselves are not knowledge calls; only the reloads are.
		const deletes, reloadsPerDelete = 36, 10
		for d := 1; d <= deletes; d++ {
			for q := 1; q <= reloadsPerDelete; q++ {
				requireAdmittedRead(t, budgetRead(t, api, ws, recordTestUsername),
					"delete "+strconv.Itoa(d)+" reload "+strconv.Itoa(q))
			}
		}
	})
}

// TestKnowledgeBudget_FloodIsStillRefused pins each ceiling exactly: the last
// call inside it is admitted, the first call past it is refused.
func TestKnowledgeBudget_FloodIsStillRefused(t *testing.T) {
	t.Run("signed-in writes: the 121st in a minute is refused", func(t *testing.T) {
		api, ws, _ := buildRecordTestVault(t)
		useFreshKnowledgeLimiter(t)
		for i := 1; i <= budgetSignedInWrites; i++ {
			requireAdmittedWrite(t, budgetWrite(t, api, ws, recordTestUsername), "write "+strconv.Itoa(i))
		}
		requireRefusedByBudget(t, budgetWrite(t, api, ws, recordTestUsername), "write 121")
	})

	t.Run("signed-in reads: the 601st in a minute is refused", func(t *testing.T) {
		api, ws, _ := buildRecordTestVault(t)
		useFreshKnowledgeLimiter(t)
		for i := 1; i <= budgetSignedInReads; i++ {
			requireAdmittedRead(t, budgetRead(t, api, ws, recordTestUsername), "read "+strconv.Itoa(i))
		}
		requireRefusedByBudget(t, budgetRead(t, api, ws, recordTestUsername), "read 601")
	})

	t.Run("no signed-in account: the 61st call of any kind is refused", func(t *testing.T) {
		api, ws, _ := buildRecordTestVault(t)
		useFreshKnowledgeLimiter(t)
		for i := 1; i <= budgetAnonymous; i++ {
			requireAdmittedRead(t, budgetRead(t, api, ws, ""), "unauthenticated read "+strconv.Itoa(i))
		}
		requireRefusedByBudget(t, budgetRead(t, api, ws, ""), "unauthenticated read 61")
		// Reads and writes share this one bucket: an unauthenticated write is
		// refused by the limiter before the door's own sign-in check (403) runs.
		requireRefusedByBudget(t,
			knowledgePostWithUser(t, api, recordsPath(ws), admittedWriteBody(), nil),
			"unauthenticated write after 60 reads")
	})
}

// TestKnowledgeBudget_OneAccountCannotRefuseAnother: two people in one
// workspace no longer share an allowance, and neither shares the strict
// unauthenticated bucket.
func TestKnowledgeBudget_OneAccountCannotRefuseAnother(t *testing.T) {
	api, ws, _ := buildRecordTestVault(t)
	useFreshKnowledgeLimiter(t)
	drainKnowledgeBudget(t, recordTestUsername, ws, knowledgeRead)
	drainKnowledgeBudget(t, recordTestUsername, ws, knowledgeWrite)

	// Control: the drain really reached the bucket the handler charges.
	requireRefusedByBudget(t, budgetRead(t, api, ws, recordTestUsername), "the drained account's read")
	requireRefusedByBudget(t, budgetWrite(t, api, ws, recordTestUsername), "the drained account's write")

	requireAdmittedWrite(t, budgetWrite(t, api, ws, "marek"), "another account's write")
	requireAdmittedRead(t, budgetRead(t, api, ws, "marek"), "another account's read")
	requireAdmittedRead(t, budgetRead(t, api, ws, ""), "an unauthenticated read")
}

// TestKnowledgeBudget_ReadsAndWritesAreCountedApart: a burst of edits cannot
// refuse the reloads it triggers, and heavy reading cannot refuse an edit.
func TestKnowledgeBudget_ReadsAndWritesAreCountedApart(t *testing.T) {
	t.Run("exhausted writes leave reads admitted", func(t *testing.T) {
		api, ws, _ := buildRecordTestVault(t)
		useFreshKnowledgeLimiter(t)
		drainKnowledgeBudget(t, recordTestUsername, ws, knowledgeWrite)
		requireRefusedByBudget(t, budgetWrite(t, api, ws, recordTestUsername), "a write past the write ceiling")
		requireAdmittedRead(t, budgetRead(t, api, ws, recordTestUsername), "a read by the same account")
	})

	t.Run("exhausted reads leave writes admitted", func(t *testing.T) {
		api, ws, _ := buildRecordTestVault(t)
		useFreshKnowledgeLimiter(t)
		drainKnowledgeBudget(t, recordTestUsername, ws, knowledgeRead)
		requireRefusedByBudget(t, budgetRead(t, api, ws, recordTestUsername), "a read past the read ceiling")
		requireAdmittedWrite(t, budgetWrite(t, api, ws, recordTestUsername), "a write by the same account")
	})
}

// TestKnowledgeBudget_WorkspacesStayApart keeps the property the old
// per-workspace key existed for: one workspace's traffic cannot refuse the
// same account's work in another.
func TestKnowledgeBudget_WorkspacesStayApart(t *testing.T) {
	api1, ws1, _ := buildRecordTestVault(t)
	api2, ws2, _ := buildRecordTestVault(t)
	require.NotEqual(t, ws1, ws2, "the fixture must hand out two different workspaces")
	useFreshKnowledgeLimiter(t)

	drainKnowledgeBudget(t, recordTestUsername, ws1, knowledgeRead)
	requireRefusedByBudget(t, budgetRead(t, api1, ws1, recordTestUsername), "a read in the drained workspace")
	requireAdmittedRead(t, budgetRead(t, api2, ws2, recordTestUsername), "the same account's read in another workspace")
}

// TestKnowledgeAccountRateKey_NoTwoPairsShareAKey: a username or workspace ID
// containing the separator cannot land two different pairs in one bucket.
func TestKnowledgeAccountRateKey_NoTwoPairsShareAKey(t *testing.T) {
	assert.NotEqual(t, knowledgeAccountRateKey("a:b", "c"), knowledgeAccountRateKey("a", "b:c"))
	assert.NotEqual(t, knowledgeAccountRateKey("ab", "c"), knowledgeAccountRateKey("a", "bc"))
}
