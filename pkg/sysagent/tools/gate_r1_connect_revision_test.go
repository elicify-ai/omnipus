package systools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// connectRevisionFixture builds a connect_channel whose reload is driven by
// the test: reloadHook runs where the live reload would apply config.
func connectRevisionFixture(t *testing.T, reloadHook func(*config.Config)) (*ChannelConnectTool, *config.Config) {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "workspaces"), 0o700))
	for _, ws := range []string{"sales", "marketing"} {
		require.NoError(t, os.WriteFile(filepath.Join(home, "workspaces", ws+".json"),
			[]byte(`{"id":"`+ws+`","name":"`+ws+`","status":"active","core_team":["mia","ray"],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`), 0o600))
	}
	cfg := &config.Config{Agents: config.AgentsConfig{List: []config.AgentConfig{
		{ID: "mia", Name: "Mia", Type: config.AgentTypeCore}, {ID: "ray", Name: "Ray", Type: config.AgentTypeCore},
	}}}
	var mu sync.Mutex
	d := &Deps{
		Home:             home,
		GetCfg:           func() *config.Config { return cfg },
		MutateConfig:     func(fn func(*config.Config) error) error { mu.Lock(); defer mu.Unlock(); return fn(cfg) },
		SaveConfigLocked: func(*config.Config) error { return nil },
		WaitForReloadFunc: func() error {
			mu.Lock()
			defer mu.Unlock()
			reloadHook(cfg)
			return nil
		},
		ChannelLiveStatus: func(string) (bool, string, bool) { return true, "", true },
	}
	return NewChannelConnectTool(d), cfg
}

// TestConnectChannel_ConcurrentSaveIsNotCertifiedAsWorking is Train 2 gate r1
// SFH T2-SF-03: another writer saves a different binding before the reload
// applies; the running channel is then B, so the first call must not report
// its own binding A as "working".
func TestConnectChannel_ConcurrentSaveIsNotCertifiedAsWorking(t *testing.T) {
	tool, _ := connectRevisionFixture(t, func(cfg *config.Config) {
		ch := cfg.Channels["telegram"]
		ch.WorkspaceID = "marketing"
		ch.Identity = &config.ChannelIdentity{Kind: "agent", ID: "ray"}
		cfg.Channels["telegram"] = ch
	})
	res := tool.Execute(context.Background(), map[string]any{
		"id": "telegram", "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent"})
	require.NotNil(t, res)
	out := strings.ToLower(res.ForLLM)
	require.Contains(t, out, "not working", "a superseded save must not be certified: %s", res.ForLLM)
	require.NotContains(t, strings.ReplaceAll(out, "not working", ""), `"working"`, "never 'working' for a configuration this call did not save: %s", res.ForLLM)
}

// TestConnectChannel_UnchangedSaveIsWorking is the control: no concurrent
// write, the channel runs, so the result is working.
func TestConnectChannel_UnchangedSaveIsWorking(t *testing.T) {
	tool, _ := connectRevisionFixture(t, func(*config.Config) {})
	res := tool.Execute(context.Background(), map[string]any{
		"id": "telegram", "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent"})
	require.NotNil(t, res)
	require.False(t, res.IsError, res.ForLLM)
	require.NotContains(t, strings.ToLower(res.ForLLM), "not working", res.ForLLM)
}
