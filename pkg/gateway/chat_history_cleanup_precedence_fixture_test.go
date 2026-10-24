package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

type r1CleanupFixture struct {
	api                  *restAPI
	store                *session.UnifiedStore
	sid, rolled, retired string
}

func r1CleanupGeneration(t *testing.T, store *session.UnifiedStore, sid string) string {
	t.Helper()
	dir := filepath.Join(store.BaseDir(), sid, session.ChatHistoryIndexDirectory)
	body, err := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	require.NoError(t, err)
	var private map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &private))
	var file string
	require.NoError(t, json.Unmarshal(private["file"], &file))
	require.NotEmpty(t, file)
	require.Equal(t, filepath.Base(file), file)
	return filepath.Join(dir, "generations", file)
}

// Existing section-G native utility, not a second archive/index encoder. The
// producer is closed before Run/Wait; the actual gateway opens only afterwards.
func r1CleanupBuildFixture(t *testing.T, driver string) r1CleanupFixture {
	t.Helper()
	home, err := os.MkdirTemp(os.TempDir(), "omnipus-e2e-gwproc-r1-")
	require.NoError(t, err)
	home, err = filepath.EvalSymlinks(home)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(home)) })
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), []byte("{}\n"), 0o600))
	producer, err := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
	require.NoError(t, err)
	meta, err := producer.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	require.NoError(t, producer.Close(), "producer stats/owned work joined before fixture utility")
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	days := []map[string]any{
		{"day": "2026-01-01", "entries": []session.TranscriptEntry{{ID: "r1-old", Role: "user", AgentID: "mia", Content: "R1_BODY_CANARY", Timestamp: at}}},
		{"day": "2026-01-02", "entries": []session.TranscriptEntry{{ID: "r1-current", Role: "user", AgentID: "mia", Content: "R1_RETAINED_BODY_CANARY", Timestamp: at.AddDate(0, 0, 1)}}},
	}
	plan, err := json.Marshal(map[string]any{"schema_version": 1, "data_root": home, "action": "seed_days", "session_id": meta.ID, "days": days})
	require.NoError(t, err)
	planPath := filepath.Join(t.TempDir(), "r1-seed.json")
	require.NoError(t, os.WriteFile(planPath, plan, 0o600))
	command := exec.Command(driver, "-test.run", "^TestChatHistoryFixtureDriver$", "-test.v")
	command.Env = historyMeasurementEnvironment(planPath, home)
	output, err := command.CombinedOutput() // Real native utility Run/Wait, never a fabricated day/index receipt.
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(planPath), "r1-driver.log"), output, 0o600))
	require.NoError(t, err, "actual seed utility failed: %s", output)
	require.Contains(t, string(output), "--- PASS: TestChatHistoryFixtureDriver ")
	count := 0
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		if !bytes.HasPrefix(line, []byte("CHAT_HISTORY_FIXTURE_RESULT=")) {
			continue
		}
		count++
		var receipt map[string]any // not-wire-format: existing private section-G test-utility receipt.
		require.NoError(t, json.Unmarshal(bytes.TrimPrefix(line, []byte("CHAT_HISTORY_FIXTURE_RESULT=")), &receipt))
		require.Equal(t, map[string]any{"schema_version": float64(1), "action": "seed_days", "session_id": meta.ID, "written_records": float64(2), "rolled_partitions": float64(1), "removed_partitions": float64(0)}, receipt)
	}
	require.Equal(t, 1, count)
	cfg := &config.Config{Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080}, Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: home, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096}}}
	seedTestAgents(cfg)
	loop := mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})
	api := &restAPI{agentLoop: loop, allowedOrigin: "http://localhost:3000", homePath: home}
	store := loop.GetSessionStore()
	require.NoError(t, session.VerifyChatHistoryIndex(context.Background(), filepath.Join(store.BaseDir(), meta.ID)))
	rolled := filepath.Join(store.BaseDir(), meta.ID, "2026-01-01.jsonl")
	require.FileExists(t, rolled, "real append-day rollover, not a backdated content timestamp")
	return r1CleanupFixture{api: api, store: store, sid: meta.ID, rolled: rolled, retired: r1CleanupGeneration(t, store, meta.ID)}
}

func r1CleanupRetirePinned(t *testing.T, f r1CleanupFixture) {
	t.Helper()
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(f.rolled, old, old))
	removed, err := f.store.RetentionSweep(90)
	require.NoError(t, err)
	require.Equal(t, 1, removed, "normal retention actually removes the old rolled partition")
	require.NoFileExists(t, f.rolled)
	require.NotEqual(t, f.retired, r1CleanupGeneration(t, f.store, f.sid), "real retention compaction changes generation file")
	require.FileExists(t, f.retired, "instrument: ONLY the actual post-pin reader can keep this retired generation alive")
}

func r1CleanupCanaries(f r1CleanupFixture, position string) []string {
	return []string{f.sid, f.retired, f.rolled, "R1_BODY_CANARY", "R1_RETAINED_BODY_CANARY", "R1_QUERY_CANARY", "R1_USER_CANARY", position, "permission denied", "sharing violation"}
}

func r1CleanupQuery(kind string) string {
	query := "private_probe=R1_QUERY_CANARY"
	if kind == "unknown_position" {
		query += "&before=hp1_" + strings.Repeat("A", 43) // Valid D2 shape, no occurrence in this new session.
	}
	return query
}

func r1CleanupExpectedStorageBody() map[string]any {
	return map[string]any{"error": "This session could not be read because session storage failed. Check disk space and permissions, then retry. Details are in the server log."}
}
