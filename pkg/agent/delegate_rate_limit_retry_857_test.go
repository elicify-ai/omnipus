// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// delegate_rate_limit_retry_857_test.go: issue #857 — a delegated (steered)
// child that meets a provider rate limit must recover, driven through the
// production Launch + Dispatch path (never a hand-built turnState).

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
)

func TestSteeredChild_ProviderRateLimit_RetriesAndCompletes(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	provider := testutil.NewScenario().
		WithError(errors.New("HTTP 429: rate limit exceeded, too many requests")).
		WithText("recovered after the rate limit")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              home,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: testDefaultAgentID, Home: home},
				{ID: "worker", Home: home},
			},
		},
	}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	mintGenuineBootEpochForLoop(t, al)
	t.Cleanup(func() { al.Close() })
	lifecycle := session.NewLifecycleStore(filepath.Join(home, "session_lifecycle"))
	inbox := session.NewMessageInboxStore(filepath.Join(home, "session_messages"))
	al.SetSessionMessagingStores(inbox, lifecycle)
	parentSessionID := newTestSteeringSession(t, al, "")
	launcher := NewSteerLauncher(al)

	launch, err := launcher.Launch(context.Background(), steer.LaunchRequest{
		SteeringSessionID: parentSessionID,
		TargetAgentID:     "worker",
		Task:              "do the thing",
		Origin:            steer.Origin{Kind: steer.OriginKindDelegate, CallID: "call-857-rl"},
	})
	require.NoError(t, err)
	_, err = launcher.Dispatch(context.Background(), launch.SessionID, launch.Generation)
	require.NoError(t, err)

	var final *session.LifecycleRecord
	waitFor(t, 30*time.Second, func() bool {
		rec, loadErr := lifecycle.Load(launch.SessionID)
		if loadErr != nil || !session.IsTerminalLifecycleState(rec.State) {
			return false
		}
		final = rec
		return true
	})
	require.Equal(t, session.LifecycleCompleted, final.State,
		"a delegated child that meets one provider 429 must retry and complete, not fail")
	require.Equal(t, 2, provider.CallCount(), "one rate-limited call plus exactly one retry")
}
