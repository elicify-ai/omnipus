package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

// Use the real gateway/policy/writer and a real Manager with a registered fake
// transport. The empty lifecycle catalog avoids constructing any real network
// connector during Reload; its actual registered worker is still cancelled,
// joined and restarted. Its policy source is the REAL AgentLoop live config,
// never that empty construction catalog. No lock/check/reload is simulated.
func t29Fixture(t *testing.T) (*wu6Fixture, *t13Adapter, *channels.Manager, *config.Config) {
	t.Helper()
	f, adapter := t13Fixture(t, "discord", "running", "agent_may_start", []string{"discord:222"})
	t15Bound(t, f)
	stopCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	require.NoError(t, f.svc.ChannelManager.StopAll(stopCtx))
	// The fixture now owns exactly one teardown of the replacement. Shared
	// cleanup must not retry StopAll (or hang after a deliberate CHECK deadlock).
	f.svc.ChannelManager = nil
	lifecycle := &config.Config{Channels: map[string]config.ChannelInstanceConfig{}}
	manager, err := channels.NewManager(lifecycle, nil, f.bus, nil)
	require.NoError(t, err)
	manager.SetPolicySource(f.al.GetConfig)
	f.al.SetChannelManager(manager)
	require.NoError(t, manager.StartAll(context.Background()))
	adapter.SetRunning(true)
	manager.RegisterChannel("discord", adapter)
	t.Cleanup(func() {
		stopped := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stopped <- manager.StopAll(ctx)
		}()
		select {
		case err := <-stopped:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("instrument: real manager teardown did not finish after releasing the worker")
		}
	})
	return f, adapter, manager, lifecycle
}
