package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func fix1PairingToolDeps(f *pairingRESTFixture) *systools.Deps {
	return &systools.Deps{
		Home: f.f1277.home, ConfigPath: f.f1277.configPath, GetCfg: f.al.GetConfig, MutateConfig: f.al.MutateConfig,
		SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(f.f1277.configPath, c) },
		LockConfigWrites: func() func() { return lockConfigWrites(f.svc) },
		PairingAttempts:  f.svc.ChannelManager.PairingAttempts, CredStore: f.svc.credStore,
		PublishPairingProgress: f.al.PublishPairingProgress,
	}
}

// T2-SF-22 / DESIGN §3–4: a real accepted write interrupts the live predecessor
// now. Its result must not deny this effect, nor pretend held teardown is done.
// The existing adapter double controls only the external captured disposal.
func TestChannelWrite_ResultAcknowledgesItsCurrentLiveInterruption(t *testing.T) {
	for _, action := range []string{"disable", "configure"} {
		for _, active := range []bool{true, false} {
			name := action + "/inactive"
			if active {
				name = action + "/active"
			}
			t.Run(name, func(t *testing.T) {
				f := newPairingRESTFixture(t)
				require.Equal(t, http.StatusOK, f.put("configure", "{}").Code)
				require.Equal(t, http.StatusOK, f.put("enable", "").Code)
				f.await(channels.PairingStatusCode)
				f.reloadJoins.joinCurrent(t)
				ch := f.factory.latest(t)
				a, ok := f.store.Latest("whatsapp")
				require.True(t, ok)
				if !active {
					ch.timeout()
				}
				entered, resume := make(chan struct{}), make(chan struct{})
				var once sync.Once
				release := func() { once.Do(func() { close(resume) }) }
				ch.mu.Lock()
				ch.onTeardown = func() { close(entered); <-resume }
				ch.mu.Unlock()
				t.Cleanup(release)
				d := fix1PairingToolDeps(f)
				args := map[string]any{"id": "whatsapp", "expected_revision": orderingToolRevision(t, d)}
				var result *tools.ToolResult
				if action == "disable" {
					result = systools.NewChannelDisableTool(d).Execute(context.Background(), args)
				} else {
					args["bot_id"] = "r7-new-recipient"
					result = systools.NewChannelConfigureTool(d).Execute(context.Background(), args)
				}
				require.NotNil(t, result)
				require.False(t, result.IsError, result.ForLLM)
				var body map[string]any
				require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body))
				if active {
					orderingWaitStage(t, entered)
					state, exists := f.store.Attempt("whatsapp", a.AttemptID)
					require.True(t, exists)
					require.Equal(t, channels.PairingPhaseStopping, state.Phase)
					require.Equal(t, channels.PairingOutcomeInterrupted, state.Outcome)
					assert.Equal(t, "saved but not working", body["outcome"])
					assert.NotContains(t, result.ForLLM, "live connection was not changed", "the actual live interruption already committed")
					reason, _ := body["reason"].(string)
					assert.True(t, strings.Contains(strings.ToLower(reason), "interrupt") || strings.Contains(strings.ToLower(reason), "stopping"), "report the current interruption/stopping effect, not only a future reload: %s", result.ForLLM)
					release()
					select {
					case disposed := <-ch.stopCalled:
						require.Equal(t, a.AttemptID, disposed)
					case <-time.After(3 * time.Second):
						t.Fatal("HARNESS: captured fixture disposal did not join")
					}
				} else {
					assert.Contains(t, result.ForLLM, "live connection was not changed or tested", "no-active control keeps the config-only explanation")
				}
			})
		}
	}
}
