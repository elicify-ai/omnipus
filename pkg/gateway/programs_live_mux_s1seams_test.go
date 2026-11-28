//go:build s1seams

package gateway

// S113's revised last And only: read the actual shared mux AFTER real gateway
// boot in both editions. Existing S113 mutation/sweep and S127 behavioural rows
// remain mandatory; counting rows is never reported as executing their payloads.
import (
	"net/http"
	"slices"
	"sort"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestProgramCredential_LiveMuxInventory(t *testing.T) {
	t.Run("local", func(t *testing.T) { pgLiveMuxInventoryEdition(t, "local", config.EditionCore) })
	t.Run("platform", func(t *testing.T) { pgLiveMuxInventoryEdition(t, "platform", config.EditionHosted) })
}

func pgLiveMuxInventoryEdition(t *testing.T, name, edition string) {
	t.Helper()
	withEdition(t, edition)
	if name == "platform" {
		// Only the external sign-in provider is a fixture. Its returned
		// routes still traverse real platformAuthMode and registration.
		setSignInProviderForTest(t, &fakeSignInProvider{})
	}
	api, stop := krbBoot(t, t.TempDir())
	defer stop()
	manager := api.agentLoop.GetChannelManager()
	require.NotNil(t, manager, "real boot must publish its ChannelManager")
	patterns := manager.RegisteredPatternsForTest()
	require.NotEmpty(t, patterns, "BLOCKED: no actual live-mux patterns after real boot — S113")
	require.True(t, sort.StringsAreSorted(patterns), "seam returns sorted snapshot")
	require.Equal(t, len(patterns), len(slices.Compact(slices.Clone(patterns))), "actual router patterns are unique")
	for _, positive := range []string{"/health", "/ready", "/reload", "/api/v1/programs", "/api/v1/programs/", "/api/v1/chat/ws", "/preview/"} {
		require.Contains(t, patterns, positive, "instrument: real boot includes health, REST and transport registrations")
	}
	if name == "local" {
		require.Contains(t, patterns, "/api/v1/auth/login", "actual local auth mode")
		require.NotContains(t, patterns, PlatformAuthStartPath)
	} else {
		for _, path := range []string{PlatformAuthStartPath, "/api/v1/auth/session", "/api/v1/auth/platform/claim", "/auth/callback"} {
			require.Contains(t, patterns, path, "actual provider-returned platform route")
		}
		require.NotContains(t, patterns, "/api/v1/auth/login")
	}
	rows := pgLiveMuxRows()
	require.Empty(t, pgLiveMuxAccounting(patterns, rows), "S113: every live pattern needs exactly one explicit sweep row/rationale")
	// Returned data is a copy, never a capability to change the mux.
	copyProbe := manager.RegisteredPatternsForTest()
	copyProbe[0] = "/s113-corrupted-snapshot"
	require.Equal(t, patterns, manager.RegisteredPatternsForTest(), "snapshot mutation must not alter actual registrations")
	var omitted []pgLiveMuxRow
	for _, row := range rows {
		if row.pattern != "/health" {
			omitted = append(omitted, row)
		}
	}
	require.Equal(t, []string{"/health: rows=0, want exactly1"}, pgLiveMuxAccounting(patterns, omitted), "instrument: a real omitted route fails exact accounting")
	repeated := append(slices.Clone(rows), pgLiveMuxRow{"/health", "generic", "duplicate instrument only"})
	require.Equal(t, []string{"/health: rows=2, want exactly1"}, pgLiveMuxAccounting(patterns, repeated), "instrument: duplicate rows cannot cover a route twice")
	const plant = "/api/v1/arbitrary-s113-live-pattern"
	manager.RegisterHTTPHandler(plant, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	require.Equal(t, []string{plant + ": rows=0, want exactly1"}, pgLiveMuxAccounting(manager.RegisteredPatternsForTest(), rows), "instrument: an arbitrary actual new registration cannot inherit a generic default row")
	t.Logf("real %s gateway boot: %d sorted live patterns have exactly one explicit row; dynamic provider/health/REST controls, read-only snapshot, missing/duplicate/new-registration detectors exercised", name, len(patterns))
}
