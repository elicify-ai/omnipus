package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/require"
)

func TestT16_Dispatch_UnlistedStartIsDroppedWithoutKillingTheDispatcher(t *testing.T) {
	// S-42: deliberately bypass send_message; the bus is not an approval bypass.
	bad, good := t15Start(t, "QA refused start"), t15Start(t, "QA valid start")
	bad.ChatID = "555" // T-3/S-42: 222 listed, 555 unlisted.
	f, adapter := t13Fixture(t, "discord", "running", "agent_may_start", []string{"discord:222"})
	t15Bound(t, f)
	goodDone, finalDone := make(chan struct{}), make(chan struct{})
	adapter.send = func(_ context.Context, msg bus.OutboundMessage) error {
		if msg.Content == "QA valid start" {
			close(goodDone)
		}
		if msg.Content == "QA dispatch bus fence" {
			close(finalDone)
		}
		return nil
	}
	logPath := filepath.Join(t.TempDir(), "dispatch.jsonl")
	oldLevel := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(oldLevel) })
	require.NoError(t, logger.EnableFileLogging(logPath))
	require.NoError(t, f.bus.PublishOutbound(context.Background(), bad))
	require.NoError(t, f.bus.PublishOutbound(context.Background(), good))
	select {
	case <-goodDone:
	case <-time.After(5 * time.Second):
		t.Fatal("S-42: following valid start did not reach Send; dispatcher may have exited on refusal")
	}
	// Any wrongly derived notice is already published before dispatch took
	// good; this later bus/worker fence drains it before the exact assertion.
	require.NoError(t, f.bus.PublishOutbound(context.Background(), bus.OutboundMessage{
		Channel: "discord", ChatID: "222", Content: "QA dispatch bus fence",
	}))
	select {
	case <-finalDone:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: dispatch completion fence did not reach worker")
	}
	attempts := adapter.attemptSnapshot()
	contents := make([]string, 0, len(attempts))
	for _, msg := range attempts {
		contents = append(contents, msg.Content)
	}
	require.Equal(t, []string{"QA valid start", "QA dispatch bus fence"}, contents, "no unlisted send or fallback notice; valid dispatcher path preserved")
	file, err := os.Open(logPath)
	require.NoError(t, err)
	defer file.Close()
	rows := make([]map[string]any, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var row map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &row))
		if row["stage"] == "dispatch" {
			rows = append(rows, row)
		}
	}
	require.NoError(t, scanner.Err())
	require.Len(t, rows, 1, "FR-044: one structured dispatch refusal")
	require.Equal(t, "info", rows[0]["level"])
	values := make([]string, 0, len(rows[0]))
	for _, value := range rows[0] {
		if text, ok := value.(string); ok {
			values = append(values, text)
		}
	}
	require.Contains(t, values, "target_not_listed", "S-42: refusal code survives structured logging")
	require.Contains(t, values, "discord")
	require.Contains(t, values, "mia")
	line, err := json.Marshal(rows[0])
	require.NoError(t, err)
	require.NotContains(t, string(line), "QA refused start", "FR-044: no message text in refusal")
}
