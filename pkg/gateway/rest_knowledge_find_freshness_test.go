// Regression test for F5 (2026-09-08 code review): a failed freshness probe
// in buildVaultSearchResult produced a MORE confident answer than a
// successful one. ferr was discarded and unlogged; the switch in
// vaultSearchStatement then fell straight to its `case out.Complete:` branch
// and rendered the fully-confident "Searched the whole of this knowledge
// base; its index was complete at query time" — while a probe that
// SUCCEEDED but found the index behind only ever produces the hedged
// "Searched X of Y notes known to the index." The degradation ran backwards.

package gateway

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records/knowledgefind"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
)

// freshnessProbeFailingText decorates a real knowledgefind.TextSearcher,
// delegating every method to it EXCEPT IndexFreshness, which always errors —
// simulating a freshness-probe failure without re-implementing the whole
// TextSearcher surface (Search/NearestTerms/SourceHash/Populated all still
// run for real, against the real index).
type freshnessProbeFailingText struct {
	knowledgefind.TextSearcher
}

func (freshnessProbeFailingText) IndexFreshness(context.Context) (knowledgefind.TextIndexFreshness, error) {
	return knowledgefind.TextIndexFreshness{}, errors.New("simulated freshness probe failure")
}

// TestVaultSearch_FreshnessProbeFailureIsNoMoreConfidentThanSuccess proves
// the failure path is now AT LEAST as cautious as a successful probe: the
// verdict is not complete, the statement no longer claims unqualified
// completeness, and the failure is logged for operator visibility.
func TestVaultSearch_FreshnessProbeFailureIsNoMoreConfidentThanSuccess(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "freshness-probe-failure.log")
	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	require.NoError(t, logger.EnableFileLogging(logFile))
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	api, ws, colID := buildVaultSearchVault(t)
	col, inScope := api.resolveScopedCollection(ws, colID)
	require.True(t, inScope)

	env, closeEnv, err := vaultprops.OpenFindEnv(context.Background(), api.homePath, col)
	require.NoError(t, err)
	defer closeEnv()

	// Sanity: the real Text implementation really does satisfy
	// TextFreshnessReporter, or this whole test would trivially no-op (the
	// `fr, ok := ...; ok` gate in buildVaultSearchResult would just skip the
	// whole honesty block on ANY text searcher, proving nothing about F5).
	_, implementsReporter := env.Deps.Text.(knowledgefind.TextFreshnessReporter)
	require.True(t, implementsReporter, "fixture drift: the real text index no longer implements TextFreshnessReporter")

	env.Deps.Text = freshnessProbeFailingText{TextSearcher: env.Deps.Text}

	out := buildVaultSearchResult(context.Background(), env, col.Root, colID, "seccomp", 20, 20, false)

	require.NotEmpty(t, out.Notes, "fixture drift: this query must produce a real hit so the honesty "+
		"block's hasHits gate actually opens")
	assert.False(t, out.Complete,
		"a failed freshness probe must not let the verdict claim unqualified completeness")
	require.NotNil(t, out.CompleteReason)
	require.NotNil(t, out.Statement)
	assert.NotEqual(t, vaultSearchCompleteStatement, *out.Statement,
		"a probe FAILURE must not render the SAME fully-confident statement a verified-complete search gets")

	logged, readErr := os.ReadFile(logFile)
	require.NoError(t, readErr)
	assert.Contains(t, string(logged), "freshness probe failed",
		"the probe failure must be logged for operator visibility — got: %s", string(logged))
}

// TestVaultSearch_FreshnessProbeSuccessStillHedges is the negative control:
// a probe that SUCCEEDS but reports the index behind still produces the
// hedged coverage sentence — proving the failure path above is not simply
// "always incomplete now" but specifically closes the gap where failure was
// MORE confident than success.
func TestVaultSearch_FreshnessProbeSuccessStillHedges(t *testing.T) {
	api, ws, colID := buildVaultSearchVault(t)
	col, inScope := api.resolveScopedCollection(ws, colID)
	require.True(t, inScope)

	env, closeEnv, err := vaultprops.OpenFindEnv(context.Background(), api.homePath, col)
	require.NoError(t, err)
	defer closeEnv()

	out := buildVaultSearchResult(context.Background(), env, col.Root, colID, "seccomp", 20, 20, false)

	require.NotEmpty(t, out.Notes, "fixture drift: this query must produce a real hit")
	require.NotNil(t, out.Statement)
	assert.NotEqual(t, vaultSearchCompleteStatement, *out.Statement,
		"a genuinely successful probe over a real (small, fully-caught-up) fixture still renders "+
			"the coverage-numbers sentence, not the bare complete statement")
}
