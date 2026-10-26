package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/stretchr/testify/require"
)

// wu7LiveFixture opts into the production reload pipeline without changing
// the runtime boundary of the other newWU6Fixture callers. Counters wrap real
// writes and reload waits; they do not replace either operation.
type wu7LiveFixture struct {
	*wu6Fixture
	saves       atomic.Int64
	reloadWaits atomic.Int64
	// Test-only completion receipts for the actual owning reload cycle.
	reloadJoins *orderingReloadJoins
}

func newWU7LiveFixture(t *testing.T) *wu7LiveFixture {
	t.Helper()
	f := &wu7LiveFixture{wu6Fixture: newWU6Fixture(t), reloadJoins: newOrderingReloadJoins()}
	w := f.wu6Fixture
	load := newReloadConfigLoader(w.f1277.configPath, w.f1277.home, w.store, w.svc)
	w.svc.loadConfigForSwap = load
	w.svc.reloadTrigger = newReloadTrigger(w.svc, w.al)
	w.al.SetReloadFunc(w.svc.reloadTrigger)

	// These are the same trigger, cycle and executor as serveReloadLoop.
	// The only fake remains the external HTTP/model edge, not channel start,
	// credential resolution, persistence, reload or live-status resolution.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var provider providers.LLMProvider = w.provider
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.svc.manualReloadChan:
				cycleDone := f.reloadJoins.begin()
				runReloadCycle(w.al, w.svc, nil, 0, func(c *config.Config) error {
					if err := executeReload(ctx, w.al, c, &provider, w.svc, w.bus, false); err != nil {
						return err
					}
					// #1340 rebuilds the named provider pools on reload.
					// Reinstall the fixture's qa1277 script after that rebuild,
					// before runReloadCycle clears the pending flag, so the
					// next real turn cannot borrow the HTTP/default client.
					return wireWU6ScriptedProviderPool(w.al, w.provider)
				}, load)
				close(cycleDone) // final coalesced completion, not a pending-value observation
			}
		}
	}()
	t.Cleanup(func() { cancel(); <-done })

	w.al.WireSysagentDeps(&systools.Deps{
		Home:         w.f1277.home,
		ConfigPath:   w.f1277.configPath,
		GetCfg:       w.al.GetConfig,
		MutateConfig: w.al.MutateConfig,
		SaveConfigLocked: func(c *config.Config) error {
			f.saves.Add(1)
			return config.SaveConfig(w.f1277.configPath, c)
		},
		LockConfigWrites: func() func() { return lockConfigWrites(w.svc) },
		CredStore:        w.store,
		WaitForReloadFunc: func() error {
			f.reloadWaits.Add(1)
			return waitForReload(w.al, w.svc.reloadOutcome)
		},
		// Omit ChannelLiveStatus deliberately: WireSysagentDeps fills the
		// production resolver, which follows the loop's CURRENT manager
		// after reload instead of retaining a pre-reload manager pointer.
	})
	return f
}

// TestWU7LiveFixture_ScriptedPairSurvivesReload guards the #1340 fixture
// repair: a real reload must rebuild the registry, then the next real turn
// must still consume the session-aware script from its named qa1277 pair.
// Both existing live-connection oracles are re-driven after that reload;
// neither can pass by inspecting the injected default client alone.
func TestWU7LiveFixture_ScriptedPairSurvivesReload(t *testing.T) {
	for _, tc := range []struct {
		name, access string
	}{
		{name: "working", access: issue1277ConnectAccess},
		{name: "controlled_401", access: issue1277UnauthorizedAccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWU7LiveFixture(t)
			before := f.al.GetRegistry()
			require.NoError(t, waitForReload(f.al, f.svc.reloadOutcome), "the real reload must complete")
			require.NotSame(t, before, f.al.GetRegistry(), "the regression must exercise rebuilt agent/provider pools")
			attempt := wu7ConnectJourney(t, f, "mia", tc.access)
			if tc.access == issue1277UnauthorizedAccess {
				wu7RequireRejectedCredential(t, f, attempt)
			} else {
				wu7RequireWorking(t, f, attempt)
			}
		})
	}
}

// wu7ConnectSnapshot records state immediately before/after the approved
// CONNECT, not before configure/card submission. Hash equality pins exact
// disk bytes without printing encrypted-store contents on failure.
type wu7ConnectSnapshot struct {
	configBytes        [sha256.Size]byte
	credentialBytes    [sha256.Size]byte
	liveChannels       string
	inventory          []credentials.CardInventoryEntry
	saves              int64
	reloads            int64
	acceptedProbes     int64
	unauthorizedProbes int64
	healthyProbes      int64
}

func wu7Snapshot(t *testing.T, f *wu7LiveFixture) wu7ConnectSnapshot {
	t.Helper()
	cfg, err := os.ReadFile(f.f1277.configPath)
	require.NoError(t, err)
	store, err := os.ReadFile(filepath.Join(f.f1277.home, "credentials.json"))
	require.NoError(t, err)
	live, err := json.Marshal(f.al.GetConfig().Channels)
	require.NoError(t, err)
	inventory, err := f.store.CardInventory()
	require.NoError(t, err)
	return wu7ConnectSnapshot{
		configBytes: sha256.Sum256(cfg), credentialBytes: sha256.Sum256(store),
		liveChannels: string(live), inventory: inventory,
		saves: f.saves.Load(), reloads: f.reloadWaits.Load(),
		acceptedProbes:     f.f1277.connectProbes.Load(),
		unauthorizedProbes: f.f1277.unauthorizedProbes.Load(),
		healthyProbes:      f.f1277.authorizedProbes.Load(),
	}
}

func wu7RequireUnchanged(t *testing.T, before, after wu7ConnectSnapshot) {
	t.Helper()
	require.Equal(t, before.configBytes, after.configBytes, "refused connect changed config file bytes")
	require.Equal(t, before.credentialBytes, after.credentialBytes, "refused connect changed credential-store bytes")
	require.Equal(t, before.liveChannels, after.liveChannels, "refused connect changed live configuration/reference selections")
	require.Equal(t, before.inventory, after.inventory, "refused connect changed stored recipients or selections")
	require.Equal(t, before.saves, after.saves, "refused connect invoked a config save")
	require.Equal(t, before.reloads, after.reloads, "refused connect invoked a reload")
	require.Equal(t, before.acceptedProbes, after.acceptedProbes, "refused connect probed the platform")
	require.Equal(t, before.unauthorizedProbes, after.unauthorizedProbes, "refused connect made a rejected platform probe")
	require.Equal(t, before.healthyProbes, after.healthyProbes, "refused connect restarted/probed an unrelated channel")
}
