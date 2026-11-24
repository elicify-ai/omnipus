//go:build s1seams

package agent

// S25/FR017: observe actual mint ENTRY, including discarded/revoked tokens.
// Only the paid model/external gh/person approval edges are fixtures; the
// generator, proxy, resolver, runtime tool and children are production code.
import (
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/testseam"
	"github.com/stretchr/testify/require"
)

func TestProgramNetwork_ActualMintHistory(t *testing.T) {
	t.Run("S25_god_mode_token_available_no_egress_token", func(t *testing.T) {
		var mu sync.Mutex
		var mints []string
		t.Cleanup(testseam.ObserveEgressTokenMints(func(kind string) {
			mu.Lock()
			defer mu.Unlock()
			mints = append(mints, kind)
		}))
		snapshot := func() []string {
			mu.Lock()
			defer mu.Unlock()
			return append([]string(nil), mints...)
		}

		// Positive instrument: the SAME entry observer must see the actual Auto
		// program-site grant. A final empty proxy map is not this observation.
		auto := newProgramsNetworkFixture(t, false, true, false)
		finish := auto.start(t, "gh api user")
		token := auto.childToken(t, true)
		auto.probe(t, token, "api.github.com", true)
		require.Equal(t, "octocat", strings.TrimSpace(finish()), "instrument: actual Auto child verified its connected token")
		auto.probe(t, token, "api.github.com", false)
		control := snapshot()
		require.Contains(t, control, testseam.EgressTokenOnce, "instrument: the observer reached the real once-token mint before its later revocation")
		for _, kind := range control {
			require.Contains(t, []string{testseam.EgressTokenOnce, testseam.EgressTokenSession}, kind, "mint observer carries kind only")
		}

		// Nothing is unregistered between the positive and negative controls.
		// Join the Auto producer before resetting the observation interval.
		mu.Lock()
		mints = nil
		mu.Unlock()
		god := newProgramsNetworkFixture(t, true, true, false)
		require.Equal(t, "16", programsChildCount(t, god.f, "GH_TOKEN", "s25-mint-history"), "S25: the actual God Mode printenv child receives the fifteen-byte token plus newline")
		require.Empty(t, god.approver.snapshot(), "S25: the actual God Mode command did not depend on approval")
		require.Empty(t, snapshot(), "S25/FR017: no once OR session egress token was ever minted, not even one later discarded or revoked")
		t.Logf("actual Auto mint-entry control kinds=%v; actual God Mode child_count=16; God Mode mint_entries=0", control)
	})
}
