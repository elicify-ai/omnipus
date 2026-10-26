package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

func fix1TelegramRevision(t *testing.T, d *systools.Deps) string {
	t.Helper()
	result := systools.NewChannelListTool(d).Execute(context.Background(), nil)
	require.False(t, result.IsError, result.ForLLM)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(result.ForLLM), &doc))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc["channels"], &rows))
	for _, row := range rows {
		var id, revision string
		require.NoError(t, json.Unmarshal(row["id"], &id))
		if id == "telegram.r7" {
			require.NoError(t, json.Unmarshal(row["revision"], &revision))
			require.NotEmpty(t, revision)
			return revision
		}
	}
	t.Fatal("HARNESS: real list_channels omitted configured Telegram instance")
	return ""
}

// T2-R7-S1 / DESIGN §3–4: actual Telegram Start (both successful GetMe
// responses and real polling) is held before returning to the real manager.
// The real Disable tool commits B meanwhile. The exact captured native context
// must be cancelled, not just its running flag or a registry entry hidden.
func TestChannelReload_DisabledDuringSuccessfulTelegramStartDisposesCapturedResource(t *testing.T) {
	held, resume, shutdown := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resume) }) }
	var getMeCalls, getMeReplies atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			return
		}
		defer r.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			// Start's credential check succeeds first. Its later Username lookup
			// is held after polling was created, so the exact real ctx exists.
			if getMeCalls.Add(1) == 2 {
				close(held)
				<-resume
			}
			getMeReplies.Add(1)
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"R7 Bot","username":"r7_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			select {
			case <-r.Context().Done():
			case <-shutdown:
			}
		case strings.HasSuffix(r.URL.Path, "/getMyCommands"):
			fmt.Fprint(w, `{"ok":true,"result":[]}`)
		default:
			fmt.Fprint(w, `{"ok":true,"result":true}`)
		}
	}))
	t.Cleanup(func() { release(); close(shutdown); server.Close() })
	built := make(chan *TelegramChannel, 1)
	t.Cleanup(channels.SwapFactoryForTesting("telegram", func(cfg *config.Config, id string, secrets credentials.SecretBundle, b *bus.MessageBus) (channels.Channel, error) {
		ch, err := NewTelegramChannel(cfg, id, secrets, b)
		if err == nil && id == "telegram.r7" {
			built <- ch
		}
		return ch, err
	}))
	cfg := &config.Config{Channels: map[string]config.ChannelInstanceConfig{"telegram.r7": {Type: "telegram", Enabled: false, TokenRef: "FIX1_R7_TELEGRAM", BaseURL: server.URL, Proxy: server.URL}}}
	secrets := credentials.SecretBundle{credentials.SecretRef("FIX1_R7_TELEGRAM"): "123456:" + strings.Repeat("a", 35)}
	mgr, err := channels.NewManager(cfg, secrets, bus.NewMessageBus(), nil)
	require.NoError(t, err)
	al := &agent.AgentLoop{}
	al.SwapConfig(cfg)
	al.SetChannelManager(mgr)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, mgr.StartAll(ctx))
	var writeMu sync.Mutex
	configPath := filepath.Join(t.TempDir(), "config.json")
	d := &systools.Deps{ConfigPath: configPath, GetCfg: al.GetConfig, MutateConfig: al.MutateConfig, PairingAttempts: mgr.PairingAttempts,
		LockConfigWrites: func() func() { writeMu.Lock(); return writeMu.Unlock },
		SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(configPath, c) },
	}
	enabled := systools.NewChannelEnableTool(d).Execute(context.Background(), map[string]any{"id": "telegram.r7", "expected_revision": fix1TelegramRevision(t, d)})
	require.False(t, enabled.IsError, enabled.ForLLM)
	cfgA := al.GetConfig()
	done := make(chan struct{})
	var reloadErr error
	var captured *TelegramChannel
	go func() { defer close(done); reloadErr = mgr.Reload(ctx, cfgA, secrets) }()
	t.Cleanup(func() {
		release()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("HARNESS: actual manager reload did not join")
		}
		if captured != nil {
			require.NoError(t, captured.Stop(context.Background()))
			captured.commandRegWG.Wait()
		}
		require.NoError(t, mgr.StopAll(context.Background()))
	})
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: actual successful Telegram Start did not reach held lookup")
	}
	captured = <-built
	require.NotNil(t, captured.ctx)
	require.NoError(t, captured.ctx.Err(), "instrument: the actual old polling context is live before Disable")
	require.True(t, captured.IsRunning(), "instrument: the real native successful Start reached its final lookup")
	disabled := systools.NewChannelDisableTool(d).Execute(context.Background(), map[string]any{"id": "telegram.r7", "expected_revision": fix1TelegramRevision(t, d)})
	require.False(t, disabled.IsError, disabled.ForLLM)
	require.False(t, al.GetConfig().Channels["telegram.r7"].Enabled)
	release()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("HARNESS: actual successful Start/reload did not join")
	}
	require.NoError(t, reloadErr)
	require.Equal(t, int32(2), getMeReplies.Load(), "instrument: both real platform checks succeeded; no failed-Start substitute")
	_, registered := mgr.GetChannel("telegram.r7")
	assert.False(t, registered, "the exact obsolete successful resource must be disposed, not stranded without an applied hash")
	assert.False(t, captured.IsRunning(), "captured native Stop must run")
	assert.ErrorIs(t, captured.ctx.Err(), context.Canceled, "actual polling lifetime must be cancelled, not just removed from discovery")
	require.NoError(t, mgr.Reload(ctx, al.GetConfig(), secrets))
	_, registered = mgr.GetChannel("telegram.r7")
	assert.False(t, registered, "the next absent-to-absent hash comparison must not leave a permanent survivor")
	assert.False(t, captured.IsRunning())
	assert.ErrorIs(t, captured.ctx.Err(), context.Canceled)
}
