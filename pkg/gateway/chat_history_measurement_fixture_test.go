package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

type historyMeasurementFixture struct {
	store *session.UnifiedStore
	sid   string
}

func historyMeasurementRepo(t *testing.T) string {
	t.Helper()
	root, absErr := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, absErr)
	require.FileExists(t, filepath.Join(root, "go.mod"), "run the native gateway test from its package directory")
	return root
}

func historyMeasurementDriver(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("OMNIPUS_CHAT_HISTORY_FIXTURE_BINARY")
	if binary == "" {
		name := "chat-history-fixture.test"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		binary = filepath.Join(historyMeasurementRepo(t), "build", "tools", name)
	}
	require.True(t, filepath.IsAbs(binary), "Section G native fixture binary is an explicit absolute path")
	if _, statErr := os.Stat(binary); statErr != nil {
		t.Fatalf("BLOCKED: prebuilt native chat-history fixture driver unavailable — required by seams G / spec D-A1: %v", statErr)
	}
	return binary
}

func historyMeasurementEnvironment(plan, home string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "OMNIPUS_CHAT_HISTORY_FIXTURE_PLAN=") && !strings.HasPrefix(item, "OMNIPUS_HOME=") {
			env = append(env, item)
		}
	}
	return append(env, "OMNIPUS_CHAT_HISTORY_FIXTURE_PLAN="+plan, "OMNIPUS_HOME="+home)
}

func historyMeasurementFixtureDays(prefix int) []map[string]any {
	days := make([]map[string]any, 90) // D-A1: ninety ACTUAL append days.
	entries := make([][]session.TranscriptEntry, 90)
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	for i := 0; i < prefix; i++ {
		day := i * 90 / prefix
		entries[day] = append(entries[day], session.TranscriptEntry{ID: fmt.Sprintf("prefix-%06d", i), Role: "user", Content: strings.Repeat("x", 1100), AgentID: "mia", Timestamp: at})
	}
	for i := 0; i < 200; i++ {
		entries[89] = append(entries[89], session.TranscriptEntry{ID: fmt.Sprintf("equal-suffix-%03d", i), Role: "user", Content: "identical measured suffix", AgentID: "mia", Timestamp: at})
	}
	for i := range days {
		days[i] = map[string]any{"day": start.AddDate(0, 0, i).Format("2006-01-02"), "entries": entries[i]}
	}
	return days
}

func historyMeasurementBuildFixture(t *testing.T, binary string, prefix int) historyMeasurementFixture {
	t.Helper()
	home, rootErr := os.MkdirTemp(os.TempDir(), "omnipus-e2e-gwproc-t18-")
	require.NoError(t, rootErr)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(home)) })
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), []byte("{}\n"), 0o600))
	store, storeErr := session.NewUnifiedStoreWithHome(filepath.Join(home, "sessions"), home)
	require.NoError(t, storeErr)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	meta, createErr := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, createErr)
	require.NoError(t, store.Close(), "one writer: parent closes/joins before the real fixture child")
	plan := map[string]any{"schema_version": 1, "data_root": home, "action": "seed_days", "session_id": meta.ID, "days": historyMeasurementFixtureDays(prefix)}
	encoded, encodeErr := json.Marshal(plan)
	require.NoError(t, encodeErr)
	planPath := filepath.Join(t.TempDir(), "seed-days.json") // Outside canonical source.
	require.NoError(t, os.WriteFile(planPath, encoded, 0o600))
	command := exec.Command(binary, "-test.run", "^TestChatHistoryFixtureDriver$", "-test.v")
	command.Env = historyMeasurementEnvironment(planPath, home)
	output, childErr := command.CombinedOutput() // Run/Wait joins this actual child.
	logPath := filepath.Join(filepath.Dir(planPath), "driver.log")
	if receiptPath := os.Getenv("OMNIPUS_CHAT_HISTORY_MEASUREMENT_RECEIPT"); receiptPath != "" {
		require.True(t, filepath.IsAbs(receiptPath))
		logPath = filepath.Join(filepath.Dir(receiptPath), fmt.Sprintf("t18-fixture-%d.log", prefix))
	}
	require.NoError(t, os.WriteFile(logPath, output, 0o600))
	require.NoError(t, childErr, "real fixture driver failed, not allocation evidence: %s", output)
	require.Contains(t, string(output), "--- PASS: TestChatHistoryFixtureDriver ")
	found := 0
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		if !bytes.HasPrefix(line, []byte("CHAT_HISTORY_FIXTURE_RESULT=")) {
			continue
		}
		found++
		var receipt map[string]any // Private Section-G utility receipt, not a gateway DTO.
		require.NoError(t, json.Unmarshal(bytes.TrimPrefix(line, []byte("CHAT_HISTORY_FIXTURE_RESULT=")), &receipt))
		require.Equal(t, map[string]any{"schema_version": float64(1), "action": "seed_days", "session_id": meta.ID, "written_records": float64(prefix + 200), "rolled_partitions": float64(89), "removed_partitions": float64(0)}, receipt)
	}
	require.Equal(t, 1, found, "one real count receipt, never a fabricated/ambiguous fixture")
	paths, pathsErr := filepath.Glob(filepath.Join(store.BaseDir(), meta.ID, "*.jsonl"))
	require.NoError(t, pathsErr)
	require.Len(t, paths, 90, "89 real rolled partitions plus current, not backdated entry timestamps")
	var encodedBytes int64
	for _, path := range paths {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		encodedBytes += info.Size()
	}
	if prefix == 100000 {
		require.GreaterOrEqual(t, encodedBytes, int64(100*1024*1024), "D-A1 long fixture is at least100MiB encoded")
	}
	reader, readerErr := session.NewUnifiedStoreWithHome(store.BaseDir(), home)
	require.NoError(t, readerErr)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })
	// Public Close stops/joins the stats worker. The archive backend holds no
	// lifetime read handles; the unchanged real read-only path is checked below.
	require.NoError(t, reader.Close(), "all owned background work stopped/joined outside measurement")
	return historyMeasurementFixture{store: reader, sid: meta.ID}
}
