package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// T2-SF-24 / R8-D2: the real consumed-start result must follow A's disposition
// when its original continuation resumes, not its earlier active/QR state.
// The existing platform adapter double remains the only lifecycle substitute.
func TestConnectTool_DelayedConsumedStartReportsTerminalDisposition(t *testing.T) {
	cases := []struct {
		name                string
		terminal            channels.PairingOutcome
		paired              bool
		wantOutcome, reason string
	}{
		{"timeout", channels.PairingOutcomeTimeout, false, "saved but not working", `(?i)(expir|timed? out|timeout)`},
		{"error", channels.PairingOutcomeError, false, "saved but not working", `(?i)(fail|error)`},
		{"fresh linked", channels.PairingOutcomeLinked, false, "working", ""},
		{"active control", "", false, "saved but not working", ""},
		{"already linked control", "", true, "working", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newPairingRESTFixture(t)
			f.endedOwnedAttempt()
			f.reloadJoins.joinCurrent(t)
			require.NoError(t, os.MkdirAll(filepath.Join(f.f1277.home, "workspaces"), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(f.f1277.home, "workspaces", "ordering.json"), []byte(`{"id":"ordering","name":"Ordering","status":"active","core_team":["mia"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`), 0o600))
			f.factory.mu.Lock()
			f.factory.paired = tc.paired
			f.factory.mu.Unlock()
			saved, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			release := func() { once.Do(func() { close(resume) }) }
			d := fix1PairingToolDeps(f)
			d.CommitPairing = f.al.CommitPairing
			d.ReleasePairing = f.al.ReleasePairing
			d.PairingAttemptState = f.al.PairingAttemptState
			d.SettlePairing = f.al.SettlePairing
			d.LockConfigWrites = func() func() { unlock := lockConfigWrites(f.svc); return func() { unlock(); close(saved); <-resume } }
			d.WaitForReloadFunc = func() error { return waitForReload(f.al, f.svc.reloadOutcome) }
			d.ChannelLiveStatus = func(id string) (bool, string, bool) {
				ch, ok := f.svc.ChannelManager.GetChannel(id)
				if !ok {
					return false, "", true
				}
				return ch.IsRunning(), "", true
			}
			revision := orderingToolRevision(t, d)
			built := f.factory.count()
			ctx := tools.WithToolCallID(tools.WithTranscriptSessionID(context.Background(), f.chatA), "final-consumed-connect")
			var result *tools.ToolResult
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
			consumed, ok := f.store.Attempt("whatsapp", ready.AttemptID)
			require.True(t, ok)
			if tc.paired {
				require.Equal(t, channels.PairingOutcomeNoPairing, consumed.Outcome)
			} else {
				require.Equal(t, channels.PairingPhaseActive, consumed.Phase)
				if tc.terminal != "" {
					var ended bool
					consumed, ended = f.store.Complete("whatsapp", ready.AttemptID, tc.terminal)
					require.True(t, ended, "real terminal transition precedes continuation")
					require.Equal(t, tc.terminal, consumed.Outcome)
					require.Equal(t, channels.PairingPhaseTerminal, consumed.Phase)
					status := channels.PairingStatusError
					if tc.terminal == channels.PairingOutcomeTimeout {
						status = channels.PairingStatusTimeout
					}
					if tc.terminal == channels.PairingOutcomeLinked {
						status = channels.PairingStatusLinked
					}
					f.al.EmitWhatsAppPairingAttempt("whatsapp", ready.AttemptID, status, "", "terminal platform disposition")
				}
			}
			ch, registered := f.svc.ChannelManager.GetChannel("whatsapp")
			require.True(t, registered)
			require.True(t, ch.IsRunning(), "native terminal pairing events do not clear the connection's running flag")
			release()
			orderingJoin(t, done)
			require.NotNil(t, result)
			require.False(t, result.IsError, result.ForLLM)
			var body map[string]any
			require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &body))
			assert.Equal(t, tc.wantOutcome, body["outcome"], result.ForLLM)
			assert.NotContains(t, result.ForLLM, "stopped before it started")
			if tc.name == "active control" {
				assert.Contains(t, result.ForLLM, "scan the WhatsApp QR code")
			} else {
				assert.NotContains(t, strings.ToLower(result.ForLLM), "scan the whatsapp qr code", "terminal/already-linked is not a still-active scan instruction")
			}
			if tc.reason != "" {
				assert.Regexp(t, tc.reason, body["reason"], result.ForLLM)
				assert.Regexp(t, `(?i)(retry|try again|new code|connect again)`, result.ForLLM, "ended QR attempt needs a fresh-attempt repair, never a scan")
			}
			assert.Equal(t, built+1, f.factory.count(), "no duplicate start for the already-consumed write")
			last, ok := f.store.Attempt("whatsapp", ready.AttemptID)
			require.True(t, ok)
			assert.Equal(t, consumed, last, "the continuation must not change its authoritative terminal result")
		})
	}
}
