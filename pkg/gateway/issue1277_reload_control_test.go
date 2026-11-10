package gateway

import (
	"context"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// #1277 positive control: the same real executeReload/swap-time loader and
// service/manager restart used by the RED cases must apply healthy config.
// This catches a fixture failure later in the pipeline that an early RED
// credential guard could otherwise hide.
func TestIssue1277_HealthySecretReloadAppliesUnrelatedChange(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.NoError(t, f.store.Set(issue1277BrokenRef, "qa1277-healthy-reload"))
	cfg, bundle, _ := f.boot(t)
	api, svc, msgBus, provider := f.startRuntime(t, cfg, bundle)
	f.cfg.Gateway.LogLevel = "debug"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))
	candidate, err := config.LoadConfigWithStore(f.configPath, f.store)
	require.NoError(t, err)
	require.NoError(t, executeReload(context.Background(), api.agentLoop, candidate, &provider, svc, msgBus, false))
	require.Equal(t, "debug", api.agentLoop.GetConfig().Gateway.LogLevel)
	require.Equal(t, "qa1277-healthy-reload", svc.bundle.GetString(issue1277BrokenRef))
	degraded, reason := svc.reloadDegradedStatus()
	require.False(t, degraded)
	require.Empty(t, reason)
	require.Empty(t, svc.ChannelManager.FailedChannels())
	ch, exists := svc.ChannelManager.GetChannel("line")
	require.True(t, exists)
	require.True(t, ch.IsRunning())
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
}
