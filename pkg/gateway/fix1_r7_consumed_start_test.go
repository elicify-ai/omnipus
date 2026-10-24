package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// CR-T2-R7-01 / R7-A02: actual writer readiness precedes the continuation.
// The real owning reload consumes the exact token while that caller is held.
// Only the existing platform adapter fixture is doubled; no CommitPairing,
// admission, config writer, reload, manager or attempt-state resolver is mocked.
func TestPairingREST_ConsumedOwnStartIsNotReportedCancelled(t *testing.T) {
	for _, linked := range []bool{false, true} {
		name := "active"
		if linked {
			name = "already linked"
		}
		t.Run(name, func(t *testing.T) {
			f := newPairingRESTFixture(t)
			f.endedOwnedAttempt()
			f.reloadJoins.joinCurrent(t)
			built := f.factory.count()
			f.factory.mu.Lock()
			f.factory.paired = linked
			f.factory.mu.Unlock()
			saved, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(resume) }) }
			previous := enableSavedHook
			enableSavedHook = func(id string) {
				if id == "whatsapp" {
					close(saved)
					<-resume
				}
			}
			var response *httptest.ResponseRecorder
			go func() { defer close(done); response = f.put("enable", retryBody("req-a", f.chatA)) }()
			t.Cleanup(func() { release(); orderingJoin(t, done); enableSavedHook = previous })
			orderingWaitStage(t, saved)
			ready, ok := f.store.Latest("whatsapp")
			require.True(t, ok)
			require.Equal(t, channels.PairingPhaseReady, ready.Phase)
			require.NoError(t, waitForReload(f.al, f.svc.reloadOutcome), "start the real reload while the HTTP caller is held")
			f.reloadJoins.joinCurrent(t)
			consumed, ok := f.store.Attempt("whatsapp", ready.AttemptID)
			require.True(t, ok)
			if linked {
				require.Equal(t, channels.PairingOutcomeNoPairing, consumed.Outcome)
				require.Equal(t, "req-a", f.await(channels.PairingStatusLinked).RequestID)
			} else {
				require.Equal(t, channels.PairingPhaseActive, consumed.Phase)
				require.Equal(t, ready.AttemptID, f.factory.latest(t).currentAttempt())
				require.Equal(t, "req-a", f.await(channels.PairingStatusCode).RequestID)
			}
			release()
			orderingJoin(t, done)
			enableSavedHook = previous
			assert.Equal(t, http.StatusOK, response.Code, "its own successful start is not cancellation: %s", response.Body.String())
			assert.NotContains(t, response.Body.String(), "cancelled before it started")
			assert.Equal(t, built+1, f.factory.count(), "the continuation must not start the consumed operation twice")
			final, ok := f.store.Attempt("whatsapp", ready.AttemptID)
			require.True(t, ok)
			assert.Equal(t, consumed, final)
		})
	}
}

func TestConnectTool_ConsumedOwnStartReportsActualDisposition(t *testing.T) {
	for _, linked := range []bool{false, true} {
		name := "active"
		if linked {
			name = "already linked"
		}
		t.Run(name, func(t *testing.T) {
			f := newPairingRESTFixture(t)
			f.endedOwnedAttempt()
			f.reloadJoins.joinCurrent(t)
			require.NoError(t, os.MkdirAll(filepath.Join(f.f1277.home, "workspaces"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(f.f1277.home, "workspaces", "ordering.json"), []byte(`{"id":"ordering","name":"Ordering","status":"active","core_team":["mia"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`), 0o600))
			f.factory.mu.Lock()
			f.factory.paired = linked
			f.factory.mu.Unlock()
			saved, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(resume) }) }
			d := &systools.Deps{
				Home: f.f1277.home, ConfigPath: f.f1277.configPath, GetCfg: f.al.GetConfig, MutateConfig: f.al.MutateConfig,
				SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(f.f1277.configPath, c) },
				LockConfigWrites: func() func() { unlock := lockConfigWrites(f.svc); return func() { unlock(); close(saved); <-resume } },
				PairingAttempts:  f.svc.ChannelManager.PairingAttempts, CredStore: f.svc.credStore,
				CommitPairing: f.al.CommitPairing, ReleasePairing: f.al.ReleasePairing, PairingAttemptState: f.al.PairingAttemptState,
				PublishPairingProgress: f.al.PublishPairingProgress, SettlePairing: f.al.SettlePairing,
				WaitForReloadFunc: func() error { return waitForReload(f.al, f.svc.reloadOutcome) },
				ChannelLiveStatus: func(id string) (bool, string, bool) {
					c, ok := f.svc.ChannelManager.GetChannel(id)
					if !ok {
						return false, "", true
					}
					return c.IsRunning(), "", true
				},
			}
			revision := orderingToolRevision(t, d)
			built := f.factory.count()
			var result *tools.ToolResult
			ctx := tools.WithToolCallID(tools.WithTranscriptSessionID(context.Background(), f.chatA), "req-connect")
			go func() {
				defer close(done)
				result = systools.NewChannelConnectTool(d).Execute(ctx, map[string]any{"id": "whatsapp", "workspace_id": "ordering", "agent_id": "mia", "expected_revision": revision})
			}()
			t.Cleanup(func() { release(); orderingJoin(t, done) })
			orderingWaitStage(t, saved)
			ready, ok := f.store.Latest("whatsapp")
			require.True(t, ok)
			require.Equal(t, channels.PairingPhaseReady, ready.Phase)
			require.NoError(t, waitForReload(f.al, f.svc.reloadOutcome))
			f.reloadJoins.joinCurrent(t)
			state, ok := f.store.Attempt("whatsapp", ready.AttemptID)
			require.True(t, ok)
			if linked {
				require.Equal(t, channels.PairingOutcomeNoPairing, state.Outcome)
			} else {
				require.Equal(t, channels.PairingPhaseActive, state.Phase)
			}
			release()
			orderingJoin(t, done)
			require.NotNil(t, result)
			require.False(t, result.IsError, result.ForLLM)
			var envelope map[string]any
			require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &envelope))
			want := "saved but not working"
			if linked {
				want = "working"
			}
			assert.Equal(t, want, envelope["outcome"], result.ForLLM)
			assert.NotContains(t, result.ForLLM, "stopped before it started")
			if !linked {
				assert.Contains(t, result.ForLLM, "pairing not finished: scan the WhatsApp QR code")
			}
			assert.Equal(t, built+1, f.factory.count())
		})
	}
}
