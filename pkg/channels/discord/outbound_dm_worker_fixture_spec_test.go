package discord

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/require"
)

func t24Receive[T any](t *testing.T, ch <-chan T, failure string) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second) // S-89: 5s + small 1s allowance
	defer cancel()
	return t24ReceiveUntil(t, ctx, ch, failure)
}

// One shared deadline bounds a multi-event lifecycle sequence; waiting for each
// signal must not restart the product's deadline plus the test allowance.
func t24ReceiveUntil[T any](t *testing.T, ctx context.Context, ch <-chan T, failure string) T {
	t.Helper()
	select {
	case value := <-ch:
		require.NoError(t, ctx.Err(), failure)
		return value
	case <-ctx.Done():
		t.Fatal(failure)
		var zero T // unreachable after Fatal; Go still requires a return
		return zero
	}
}

// Register the actual session-backed adapter after starting an empty lifecycle
// catalog, so no live Discord gateway connection is attempted by construction.
// Start permission comes from a real separate config snapshot, not a fake rule.
// Reload of this catalog still cancels/joins/restarts the actual registered
// worker generation; the worker and the shared REST wrapper are never replaced.
func t24Manager(t *testing.T, adapter *DiscordChannel) (*channels.Manager, *bus.MessageBus, *config.Config) {
	t.Helper()
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	lifecycle := &config.Config{Channels: map[string]config.ChannelInstanceConfig{}}
	manager, err := channels.NewManager(lifecycle, nil, mb, nil)
	require.NoError(t, err)
	policy := &config.Config{Channels: map[string]config.ChannelInstanceConfig{"discord": {
		Type: "discord", Enabled: true, WorkspaceID: "ws-t24", Identity: &config.ChannelIdentity{Kind: "agent", ID: "mia"},
		Outbound: &config.ChannelOutboundConfig{Policy: "agent_may_start", Targets: []string{"discord:333"}},
	}}}
	manager.SetPolicySource(func() *config.Config { return policy })
	require.NoError(t, manager.StartAll(context.Background()))
	manager.RegisterChannel("discord", adapter)
	t.Cleanup(func() {
		stopped := make(chan error, 1)
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			stopped <- manager.StopAll(ctx)
		}()
		require.NoError(t, t24Receive(t, stopped, "instrument: actual manager teardown did not finish"))
	})
	return manager, mb, lifecycle
}

func t24ErrorLog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dm-errors.jsonl")
	prior := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	require.NoError(t, logger.EnableFileLogging(path))
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(prior) })
	return path
}

// Observe the real adapter error at the actual manager's documented logging
// boundary, instead of replacing/spying on DiscordChannel.Send. S-87 specifies
// this error record; the send callback/adapter return is never mocked.
func t24RequireAdapterError(t *testing.T, path string) {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	rows := []map[string]any{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var row map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &row))
		if row["message"] == "Send failed" {
			rows = append(rows, row)
		}
	}
	require.NoError(t, scanner.Err())
	require.Len(t, rows, 1, "S-89/S-87: one actual adapter failure, not delivery success or retries")
	require.Equal(t, "error", rows[0]["level"])
	require.Equal(t, "discord", rows[0]["channel"])
	require.Equal(t, "333", rows[0]["chat_id"])
	require.Contains(t, rows[0]["error"], channels.ErrSendFailed.Error(), "the real adapter's permanent failure reaches the manager")
}
