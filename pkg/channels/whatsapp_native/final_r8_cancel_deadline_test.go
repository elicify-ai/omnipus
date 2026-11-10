//go:build (linux || darwin || windows) && !mipsle

package whatsapp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

func finalNativeStartedResource(t *testing.T) (*nativeFixture, *whatsmeow.Client) {
	t.Helper()
	server := fix1NewNoiseServer(t)
	f := newNativeFixture(t, t.TempDir())
	var captured *whatsmeow.Client
	previous := nativeClientHook
	nativeClientHook = func(c *whatsmeow.Client) {
		captured = c
		c.EnableAutoReconnect = false
		c.MessengerConfig = &whatsmeow.MessengerConfig{BaseURL: server.server.URL, WebsocketURL: "ws" + strings.TrimPrefix(server.server.URL, "http")}
	}
	t.Cleanup(func() { nativeClientHook = previous })
	a := f.admitReady(t, channels.PairingAdmissionConnect, ownerA)
	require.NoError(t, f.ch.Start(channels.WithPairingStart(context.Background(), a, false)))
	require.NotNil(t, captured)
	require.True(t, captured.IsConnected(), "instrument: actual native client connected through a real Noise handshake")
	t.Cleanup(captured.Disconnect)
	select {
	case <-server.connected:
	case err := <-server.errors:
		t.Fatalf("HARNESS: real handshake failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: real native handshake not acknowledged")
	}
	return f, captured
}

func finalNativeRevision(t *testing.T, d *systools.Deps) string {
	t.Helper()
	res := systools.NewChannelListTool(d).Execute(context.Background(), nil)
	require.False(t, res.IsError, res.ForLLM)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(res.ForLLM), &doc))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc["channels"], &rows))
	for _, row := range rows {
		var id, revision string
		require.NoError(t, json.Unmarshal(row["id"], &id))
		if id == "whatsapp" {
			require.NoError(t, json.Unmarshal(row["revision"], &revision))
			require.NotEmpty(t, revision)
			return revision
		}
	}
	t.Fatal("HARNESS: configured native instance missing from real list tool")
	return ""
}

// A positive blocked setup stack, not expiry of a watchdog, proves that the
// already-expired caller is still waiting before stopNative's ctx select.
func finalNativeCancelBoundary(t *testing.T, done <-chan struct{}) bool {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-done:
			return true
		case <-deadline:
			t.Fatal("HARNESS: captured Cancel reached neither return nor native setup lock")
		default:
		}
		stack := make([]byte, 1<<20)
		n := runtime.Stack(stack, true)
		for _, g := range strings.Split(string(stack[:n]), "\n\n") {
			if strings.Contains(g, "finalNativeDeadlineCase.func") && strings.Contains(g, "(*WhatsAppNativeChannel).stopNative") && strings.Contains(g, "[sync.Mutex.Lock]") {
				t.Logf("actual expired Cancel blocked in synchronous native setup before its context select:\n%s", g)
				return false
			}
		}
		runtime.Gosched()
	}
}

func finalNativeDeadlineCase(t *testing.T) {
	f, captured := finalNativeStartedResource(t)
	a, exists := f.store.Latest("whatsapp")
	require.True(t, exists)
	selected, ok := f.store.BeginStop(a)
	require.True(t, ok)
	require.NotNil(t, selected.Cancel)
	cfg := &config.Config{Channels: map[string]config.ChannelInstanceConfig{"whatsapp": {Type: "whatsapp", Enabled: true}}}
	cfg.SetChannelOrderGeneration("whatsapp", a.Generation) // exact accepted direct-admission fixture snapshot
	al := &agent.AgentLoop{}
	al.SwapConfig(cfg)
	var writeMu sync.Mutex
	held, resume, writerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	configPath := filepath.Join(t.TempDir(), "config.json")
	d := &systools.Deps{ConfigPath: configPath, GetCfg: al.GetConfig, MutateConfig: al.MutateConfig, PairingAttempts: func() *channels.PairingAttempts { return f.store },
		LockConfigWrites: func() func() { writeMu.Lock(); return writeMu.Unlock },
		SaveConfigLocked: func(c *config.Config) error { close(held); <-resume; return config.SaveConfig(configPath, c) },
	}
	revision := finalNativeRevision(t, d)
	var writeOK bool
	go func() {
		defer close(writerDone)
		result := systools.NewChannelConfigureTool(d).Execute(context.Background(), map[string]any{"id": "whatsapp", "expected_revision": revision, "bot_id": "final-settings"})
		writeOK = !result.IsError
	}()
	t.Cleanup(func() {
		release()
		select {
		case <-writerDone:
		case <-time.After(5 * time.Second):
			t.Error("HARNESS: real settings writer did not join")
		}
	})
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: real settings save did not reach held transaction")
	}
	contended := make(chan struct{}, 1)
	f.store.SetOrderContendedHookForTest(func(id string) {
		var stack [32768]byte
		n := runtime.Stack(stack[:], false)
		if id == "whatsapp" && strings.Contains(string(stack[:n]), "(*WhatsAppNativeChannel).connectNative") {
			contended <- struct{}{}
		}
	})
	// Platform event boundary: the registered production reconnect handler and
	// worker enter connectNative; no native/preflight/Stop implementation is mocked.
	f.ch.eventHandler(&events.Disconnected{})
	t.Cleanup(func() { release(); f.ch.wg.Wait(); f.store.SetOrderContendedHookForTest(nil) })
	select {
	case <-contended:
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: actual native reconnect preflight did not contend on the writer-held order")
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded, "instrument: caller is already expired, with no sleep/stopwatch oracle")
	stopDone := make(chan struct{})
	var stopErr error
	go func() { defer close(stopDone); stopErr = selected.Cancel(ctx) }()
	t.Cleanup(func() {
		release()
		select {
		case <-stopDone:
		case <-time.After(5 * time.Second):
			t.Error("HARNESS: captured Cancel did not join")
		}
	})
	returnedBeforeWriter := finalNativeCancelBoundary(t, stopDone)
	stillStopping, exists := f.store.Attempt("whatsapp", a.AttemptID)
	require.True(t, exists)
	assert.Equal(t, channels.PairingPhaseStopping, stillStopping.Phase, "an expired wait is not completed cleanup")
	release()
	select {
	case <-writerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: settings writer did not finish after release")
	}
	select {
	case <-stopDone:
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: captured Cancel did not return after release")
	}
	assert.True(t, returnedBeforeWriter, "expired captured Cancel must return its context error without waiting for an unrelated settings transaction")
	assert.ErrorIs(t, stopErr, context.DeadlineExceeded)
	require.True(t, writeOK, "real settings save remains valid while exact old cleanup is retained")
	require.NoError(t, selected.Cancel(context.Background()), "retained exact handle eventually joins real native disposal")
	f.ch.wg.Wait()
	assert.False(t, captured.IsConnected(), "actual captured client is disconnected after release/join")
	done, finished := f.store.FinishStop("whatsapp", a.AttemptID, nil)
	require.True(t, finished)
	assert.Equal(t, channels.PairingOutcomeStopped, done.Outcome)
}

func TestNativeCancel_ContextDeadlineCoversLockedPreflightSetup(t *testing.T) {
	t.Run("selected Cancel versus held real settings and reconnect", finalNativeDeadlineCase)
	t.Run("uncontended captured Cancel control", func(t *testing.T) {
		f, captured := finalNativeStartedResource(t)
		a, exists := f.store.Latest("whatsapp")
		require.True(t, exists)
		selected, ok := f.store.BeginStop(a)
		require.True(t, ok)
		require.NoError(t, selected.Cancel(context.Background()))
		assert.False(t, captured.IsConnected())
		done, finished := f.store.FinishStop("whatsapp", a.AttemptID, nil)
		require.True(t, finished)
		assert.Equal(t, channels.PairingOutcomeStopped, done.Outcome)
	})
}
