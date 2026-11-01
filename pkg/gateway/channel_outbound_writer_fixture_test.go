package gateway

// T-13 and subsequent writer/pipeline rows share one external-adapter fixture.
// REST, persistence, policy, queueing and lifecycle remain production code.

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

type t13Adapter struct {
	*channels.BaseChannel
	mu       sync.Mutex
	lookups  []string
	attempts []bus.OutboundMessage
	lookup   func(context.Context, string) channels.OutboundTargetResult
	send     func(context.Context, bus.OutboundMessage) error
}

func (a *t13Adapter) Start(context.Context) error { a.SetRunning(true); return nil }
func (a *t13Adapter) Stop(context.Context) error  { a.SetRunning(false); return nil }
func (a *t13Adapter) Send(ctx context.Context, msg bus.OutboundMessage) error {
	a.mu.Lock()
	a.attempts = append(a.attempts, msg)
	a.mu.Unlock()
	if a.send != nil {
		return a.send(ctx, msg)
	}
	return nil
}
func (a *t13Adapter) ResolveOutboundTarget(ctx context.Context, canonical string) channels.OutboundTargetResult {
	a.mu.Lock()
	a.lookups = append(a.lookups, canonical)
	a.mu.Unlock()
	if a.lookup != nil {
		return a.lookup(ctx, canonical)
	}
	return channels.OutboundTargetResult{}
}
func (a *t13Adapter) lookupSnapshot() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string{}, a.lookups...)
}
func (a *t13Adapter) attemptSnapshot() []bus.OutboundMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]bus.OutboundMessage{}, a.attempts...)
}

func t13Seed(t *testing.T, f *wu6Fixture, id, platform string, enabled bool, policy string, targets []string) {
	t.Helper()
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		cfg.Channels[id] = config.ChannelInstanceConfig{
			Type: platform, Enabled: enabled,
			Outbound: &config.ChannelOutboundConfig{Policy: policy, Targets: append([]string{}, targets...)},
		}
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
}

func t13Fixture(t *testing.T, platform, state, policy string, targets []string) (*wu6Fixture, *t13Adapter) {
	t.Helper()
	f, _ := t14Fixture(t)
	t13Seed(t, f, platform, platform, state != "disabled", policy, targets)
	adapter := &t13Adapter{BaseChannel: channels.NewBaseChannel(platform, nil, f.bus, nil)}
	adapter.SetRunning(state == "running")
	if state == "running" || state == "not_running" {
		f.svc.ChannelManager.RegisterChannel(platform, adapter)
	}
	return f, adapter
}

type t13SavedState struct {
	disk []byte
	live *config.Config
}

func t13Before(t *testing.T, f *wu6Fixture) t13SavedState {
	t.Helper()
	disk, err := os.ReadFile(f.f1277.configPath)
	require.NoError(t, err)
	live, err := f.al.GetConfig().Clone()
	require.NoError(t, err, "instrument: independent deep snapshot before refusal")
	return t13SavedState{disk: disk, live: live}
}

func t13Unchanged(t *testing.T, f *wu6Fixture, before t13SavedState) {
	t.Helper()
	disk, err := os.ReadFile(f.f1277.configPath)
	require.NoError(t, err)
	require.Equal(t, before.disk, disk, "refusal must not change any durable config byte")
	require.Equal(t, before.live.Channels, f.al.GetConfig().Channels, "refusal must not mutate live channel state")
}

func t13AssertOutbound(t *testing.T, f *wu6Fixture, id, policy string, targets []string) {
	t.Helper()
	read := t14Read(t, f, id)
	require.Equal(t, policy, string(read.Outbound.Policy), "GET uses the committed policy")
	require.Equal(t, targets, read.Outbound.Targets, "GET uses exact canonical targets")
	live := f.al.GetConfig().Channels[id].Outbound
	require.NotNil(t, live)
	require.Equal(t, policy, live.Policy, "live policy is in force without adapter reload")
	require.Equal(t, targets, []string(live.Targets))
	doc := readOutboundConfigChannelsFile(t, f)
	inst, ok := doc[id].(map[string]any)
	require.True(t, ok)
	expectedTargets := make([]any, len(targets))
	for i, target := range targets {
		expectedTargets[i] = target
	}
	require.Equal(t, map[string]any{"policy": policy, "targets": expectedTargets}, inst["outbound"], "exact durable outbound object")
}
