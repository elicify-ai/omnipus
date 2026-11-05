package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/stretchr/testify/require"
)

func t15Start(t *testing.T, content string) bus.OutboundMessage {
	t.Helper()
	return bus.OutboundMessage{Channel: "discord", ChatID: "222", Content: content,
		AgentID: "mia", WorkspaceID: "ws-t15", Operation: bus.OperationStart}
}

func t15Bound(t *testing.T, f *wu6Fixture) {
	t.Helper()
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		inst := cfg.Channels["discord"]
		inst.WorkspaceID = "ws-t15"
		inst.Identity = &config.ChannelIdentity{Kind: "agent", ID: "mia"}
		cfg.Channels["discord"] = inst
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	f.svc.ChannelManager.SetPolicySource(f.al.GetConfig)
}

// Read-only observation of the actual queue, not a fake hook. The test never
// reloads or changes this worker map while observing it. Length is a real
// channel operation; no unsafe conversion/accessor or queue replacement.
func t15QueuedCount(t *testing.T, mgr *channels.Manager) func() int {
	t.Helper()
	workers := reflect.ValueOf(mgr).Elem().FieldByName("workers")
	require.True(t, workers.IsValid() && workers.Kind() == reflect.Map, "instrument: actual manager worker map")
	worker := workers.MapIndex(reflect.ValueOf("discord"))
	require.True(t, worker.IsValid() && !worker.IsNil(), "instrument: actual discord worker")
	queue := worker.Elem().FieldByName("queue")
	require.True(t, queue.IsValid() && queue.Kind() == reflect.Chan, "instrument: actual text worker queue")
	return queue.Len
}

func t15Wait(t *testing.T, condition func() bool, failure string) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second) // S-19 safety guard; never a sleep/order oracle.
	defer deadline.Stop()
	for !condition() {
		select {
		case <-deadline.C:
			t.Fatal(failure)
		default:
			runtime.Gosched()
		}
	}
}

func t15LogRows(t *testing.T, path, message string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer file.Close()
	rows := make([]map[string]any, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var row map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &row), "instrument: real file logger emits JSON records")
		if row["message"] == message {
			rows = append(rows, row)
		}
	}
	require.NoError(t, scanner.Err())
	return rows
}

func TestT15_Dequeue_RealRESTRevocationDropsOnlyTheQueuedSecondStart(t *testing.T) {
	// S-19's oracle counts M2 only: M1 has already entered Send and is allowed.
	m1, m2 := t15Start(t, "M1"), t15Start(t, "M2")
	f, adapter := t13Fixture(t, "discord", "running", "agent_may_start", []string{"discord:222"})
	t15Bound(t, f)
	entered, release, sentinelDone, finalDone := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	adapter.send = func(ctx context.Context, msg bus.OutboundMessage) error {
		switch msg.Content {
		case "M1":
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		case "QA FIFO reply sentinel":
			close(sentinelDone)
		case "QA final bus fence":
			close(finalDone)
		}
		return nil
	}
	logPath := filepath.Join(t.TempDir(), "dequeue.jsonl")
	oldLevel := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	t.Cleanup(func() { logger.DisableFileLogging(); logger.SetLevel(oldLevel) })
	require.NoError(t, logger.EnableFileLogging(logPath))
	require.NoError(t, f.bus.PublishOutbound(context.Background(), m1))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: allowed M1 never entered real worker Send before revocation")
	}
	queued := t15QueuedCount(t, f.svc.ChannelManager)
	require.NoError(t, f.bus.PublishOutbound(context.Background(), m2))
	t15Wait(t, func() bool { return queued() == 1 }, "instrument: M2 was not in the actual blocked worker's queue")
	revoked := t14Put(t, f, "discord", "reply_only", []string{"222"}, t14Read(t, f, "discord").Revision)
	require.Equal(t, 200, revoked.Code, "S-19: real REST revocation must finish while M1 is in Send: %s", revoked.Body.String())
	t13AssertOutbound(t, f, "discord", "reply_only", []string{"discord:222"})
	// Queue this after M2. Its completed Send is a FIFO completion fence, so
	// the no-M2 assertion never relies on merely waiting for nothing to happen.
	require.NoError(t, f.bus.PublishOutbound(context.Background(), bus.OutboundMessage{
		Channel: "discord", ChatID: "222", Content: "QA FIFO reply sentinel",
	}))
	t15Wait(t, func() bool { return queued() == 2 }, "instrument: reply sentinel never queued behind M2")
	releaseOnce.Do(func() { close(release) })
	select {
	case <-sentinelDone:
	case <-time.After(5 * time.Second):
		t.Fatal("S-19: worker failed to finish M2's position and deliver the following reply")
	}
	// A second fence is published only after the worker finished M2's path.
	// Any notice that path wrongly published is already ahead of this fence
	// on the bus. Drain it too before asserting zero derived chat traffic.
	require.NoError(t, f.bus.PublishOutbound(context.Background(), bus.OutboundMessage{
		Channel: "discord", ChatID: "222", Content: "QA final bus fence",
	}))
	select {
	case <-finalDone:
	case <-time.After(5 * time.Second):
		t.Fatal("instrument: final bus/worker fence never completed")
	}
	attempts := adapter.attemptSnapshot()
	contents := make([]string, 0, len(attempts))
	for _, msg := range attempts {
		contents = append(contents, msg.Content)
	}
	require.Equal(t, []string{"M1", "QA FIFO reply sentinel", "QA final bus fence"}, contents, "zero M2 attempts and zero derived chat notices after both fences; M1 is allowed")
	rows := t15LogRows(t, logPath, "refused a queued start")
	require.Len(t, rows, 1, "S-19: one informative refusal for M2")
	require.Equal(t, "info", rows[0]["level"])
	require.Equal(t, "dequeue", rows[0]["stage"])
	values := make([]string, 0, len(rows[0]))
	for _, value := range rows[0] {
		if text, ok := value.(string); ok {
			values = append(values, text)
		}
	}
	// The spec fixes these semantic fields, not their implementation key names.
	require.Contains(t, values, "discord", "refusal identifies the instance")
	require.Contains(t, values, "mia", "refusal identifies the agent")
	require.Contains(t, values, "only_answer", "T-3 refusal code is not lost")
	targetPresent := false
	for _, value := range values {
		targetPresent = targetPresent || value == "222" || value == "discord:222"
	}
	require.True(t, targetPresent, "refusal identifies exact bare or canonical T-2 target 222")
	line, err := json.Marshal(rows[0])
	require.NoError(t, err)
	require.False(t, strings.Contains(string(line), "M2"), "FR-044: refusal never logs the message text")
}
