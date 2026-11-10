package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// Existing-but-empty is disconnected (3f6822287; chief Q1 A, 2026-10-14).
// #1277 founder 2026-10-12: the store, not a stale injected env value, is the
// authority for a configured reference. This is NOT a missing-ref/error case.
func TestIssue1277_EmptyStoreEntryWithStaleEnvIsDisconnected(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-good-line"))
	cfg, bundle, _ := f.boot(t)
	const ref = "QA1277_EMPTY_STALE_REF"
	const stale = "qa1277-synthetic-stale-value"
	require.NoError(t, f.store.Set(ref, ""))
	value, err := f.store.Get(ref)
	require.NoError(t, err, "instrument: entry must exist and decrypt, not be missing or unreadable")
	require.Equal(t, "", value, "instrument: the authoritative store value must actually be empty")
	t.Setenv(ref, stale)
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Provider: "qa1277-empty", Model: "health-probe", Models: []string{"health-probe"}, Custom: true,
		Protocol: "openai-compatible", APIBase: cfg.Providers[0].APIBase, APIKeyRef: ref,
	})
	api := f.startAPI(t, cfg, bundle)
	rows := issue1277WireProviderRows(t, api)
	require.Len(t, rows, 2)
	require.Equal(t, "connected", issue1277WireString(t, rows["qa1277"], "status"))
	require.NotContains(t, rows["qa1277"], "error")
	issue1277RequireHealthyProvider(t, api.agentLoop)
	require.Equal(t, int64(1), f.providerCalls.Load())
	row := rows["qa1277-empty"]
	require.Equal(t, "disconnected", issue1277WireString(t, row, "status"),
		"an existing empty store entry must not become connected through a stale process value")
	require.NotContains(t, row, "error", "an existing empty entry is not a missing-secret error")
}
