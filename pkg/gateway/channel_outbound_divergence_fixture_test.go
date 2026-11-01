package gateway

import (
	"net/http"
	"path/filepath"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

func t32Fixture(t *testing.T, id string, targets []string) (*wu6Fixture, *t13Adapter) {
	t.Helper()
	f, _ := t14Fixture(t)
	t13Seed(t, f, id, "discord", true, "agent_may_start", targets)
	require.NoError(t, f.store.Set("QA_T32_DISCORD_TOKEN", "qa-t32-synthetic-token"))
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		inst := cfg.Channels[id]
		inst.TokenRef = "QA_T32_DISCORD_TOKEN"
		inst.WorkspaceID = "ws-t32"
		inst.Identity = &config.ChannelIdentity{Kind: "agent", ID: "mia"}
		cfg.Channels[id] = inst
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	adapter := &t13Adapter{BaseChannel: channels.NewBaseChannel(id, nil, f.bus, nil)}
	adapter.SetRunning(true)
	f.svc.ChannelManager.RegisterChannel(id, adapter)
	f.svc.ChannelManager.SetPolicySource(f.al.GetConfig)
	return f, adapter
}

func t32FaultRefresh(t *testing.T, f *wu6Fixture) func() {
	t.Helper()
	previous := f.api.credStore
	locked := credentials.NewStore(filepath.Join(t.TempDir(), "locked-t32-credentials.json"))
	f.api.credStore = locked
	var once sync.Once
	restore := func() { once.Do(func() { f.api.credStore = previous; locked.Close() }) }
	t.Cleanup(restore)
	return restore
}

// Prepare saved/live divergence at the REAL common commit/refresh seam. For
// d/e/f, dedicated=true allows the dataset's smaller saved target set. This
// intentionally stops at the common commit after its actual refresh error:
// it does not claim a complete prior outbound REST call (which heals the list
// by narrow publication). The action under test always uses t14Put/real REST.
func t32FailedCommonSave(t *testing.T, f *wu6Fixture, dedicated bool, mutate func(map[string]any) error) {
	t.Helper()
	before, err := f.al.GetConfig().Clone()
	require.NoError(t, err)
	func() {
		f.api.configMu.Lock()
		defer f.api.configMu.Unlock()
		prior := f.api.outboundDedicatedWrite
		f.api.outboundDedicatedWrite = dedicated
		defer func() { f.api.outboundDedicatedWrite = prior }()
		err = f.api.orderedConfigJSONLocked(f.api.pairingOrderStore(), nil, nil, mutate)
	}()
	var refreshErr *configRefreshError
	require.ErrorAs(t, err, &refreshErr, "instrument: actual file write succeeded and real live refresh failed")
	require.Equal(t, before.Channels, f.al.GetConfig().Channels, "instrument: failed refresh leaves the old independent live snapshot")
}

func t32Saved(t *testing.T, f *wu6Fixture, id string) map[string]any {
	t.Helper()
	inst, ok := readOutboundConfigChannelsFile(t, f)[id].(map[string]any)
	require.True(t, ok, "instrument: selected channel persisted")
	return inst
}

func t32Binding(t *testing.T, f *wu6Fixture, id, savedAgent, liveAgent string) {
	t.Helper()
	saved := t32Saved(t, f, id)
	require.Equal(t, "ws-t32", saved["workspace_id"])
	identity, ok := saved["identity"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, savedAgent, identity["id"], "outbound write never transfers the saved binding")
	live := f.al.GetConfig().Channels[id]
	require.Equal(t, "ws-t32", live.WorkspaceID)
	require.NotNil(t, live.Identity)
	require.Equal(t, liveAgent, live.Identity.ID, "narrow publish never transfers the live binding")
}

func t32RequireMismatch(t *testing.T, f *wu6Fixture, id string, targets []string) {
	t.Helper()
	before := t13Before(t, f)
	rec := t14Put(t, f, id, "agent_may_start", targets, t14Read(t, f, id).Revision)
	body := t14Rejection(t, rec, http.StatusConflict, "saved_live_mismatch")
	require.Equal(t, "The saved and running configurations for this connection differ because an earlier change did not finish applying. Nothing was saved. Finish applying the earlier change first (any successful save of this connection, or disable/enable), then save the list again.", body.Error, "T-6b exact recovery explanation")
	t13Unchanged(t, f, before)
}

func t32RebindDoc(t *testing.T) func(map[string]any) error {
	t.Helper()
	return func(doc map[string]any) error {
		instances, ok := doc["channels"].(map[string]any)
		require.True(t, ok, "instrument: configuration contains a channels object")
		inst, ok := instances["discord"].(map[string]any)
		require.True(t, ok, "instrument: configuration contains the selected Discord instance")
		inst["identity"] = map[string]any{"kind": "agent", "id": "qa-b"}
		return nil
	}
}

func t32SmallerSavedDoc(t *testing.T) func(map[string]any) error {
	t.Helper()
	return func(doc map[string]any) error {
		instances, ok := doc["channels"].(map[string]any)
		require.True(t, ok, "instrument: configuration contains a channels object")
		inst, ok := instances["discord"].(map[string]any)
		require.True(t, ok, "instrument: configuration contains the selected Discord instance")
		inst["outbound"] = map[string]any{"policy": "agent_may_start", "targets": []string{"discord:222"}}
		return nil
	}
}
