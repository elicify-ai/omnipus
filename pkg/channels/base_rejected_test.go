package channels

// T-04 (part 1) — rejected-sender memory (channels-spec-r8, T-04;
// FR-060, FR-061; S-53, S-55, S-56; SC-006).
//
// Oracles (spec, never code):
//   FR-060: recordRejected, behind a mutex — increments the counter; a sender
//     not among the last 20 recorded is logged ONCE at info ("channel ignored
//     a sender not on the allowed list", fields instance_id + sender + hint
//     "add this id to allow_from to admit it", never the text) and put first;
//     an already-recorded sender is moved first without a second log line.
//     No reply is sent.
//   S-53: sender 9 writes twice → exactly one info log line contains the id;
//     the diagnostic record contains no message text; count 2, recent [id];
//     nothing sent to the chat.
//   S-55: 21 distinct senders, then sender 1 again → recent holds 20 ids,
//     most recent first; sender 1 is logged a second time.
//   S-56: empty allow list → everyone admitted, nothing recorded.
//
// Log lines are captured through the REAL logger via its file sink
// (OMNIPUS_LOG_FILE), the same real-logger capture the SC-006 rule requires —
// not a fake logger, not process-wide stderr scraping.
//
// RED expectation on the WP-0 shell: recordRejected does nothing and
// RejectedSenders returns (0, nil), so every count/order/log assertion below
// fails.

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// captureRealLoggerFile switches the real logger's file sink on for this test
// and returns the JSONL path. Restores the previous sink and level afterwards.
func captureRealLoggerFile(t *testing.T) string {
	t.Helper()
	logPath := t.TempDir() + "/channels-test.jsonl"
	previousHandler := slog.Default()
	previousLevel := logger.GetLevel()
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(previousLevel)
		slog.SetDefault(previousHandler)
	})
	t.Setenv("OMNIPUS_LOG_FILE", logPath)
	logger.SetLevel(logger.INFO)
	logger.ConfigureFromEnv()
	return logPath
}

// logFileLines returns every non-empty JSONL line of the captured log.
func logFileLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	require.NoError(t, sc.Err())
	return lines
}

// newRejectedFixture returns a mock channel with a non-empty allow list on a
// live bus, ready for HandleMessage-driven rejection recording.
func newRejectedFixture(t *testing.T, allowList []string) (*mockChannel, *bus.MessageBus) {
	t.Helper()
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	ch := &mockChannel{}
	ch.BaseChannel = *NewBaseChannel("discord", nil, mb, allowList)
	ch.instanceID = "discord"
	return ch, mb
}

func rejectedSenderInfo(canonical string) bus.SenderInfo {
	return bus.SenderInfo{CanonicalID: canonical, PlatformID: strings.TrimPrefix(canonical, "discord:"), Platform: "discord"}
}

func TestRejectedSender_TwoWritesOneLogLineCountTwo(t *testing.T) {
	// S-53: two writes from an unlisted sender → exactly one info log line
	// names the id; count 2; recent holds the id most-recent-first; the
	// record (and every log line) carries no message text.
	logPath := captureRealLoggerFile(t)
	ch, _ := newRejectedFixture(t, []string{"discord:1"})

	ch.HandleMessage(context.Background(), bus.Peer{}, "m1", "9", "c1",
		"SENTINEL-TEXT-551", nil, nil, rejectedSenderInfo("discord:9"))
	ch.HandleMessage(context.Background(), bus.Peer{}, "m2", "9", "c1",
		"SENTINEL-TEXT-551", nil, nil, rejectedSenderInfo("discord:9"))

	lines := logFileLines(t, logPath)
	idLines := 0
	for _, line := range lines {
		if strings.Contains(line, "discord:9") {
			idLines++
		}
		if strings.Contains(line, "SENTINEL-TEXT-551") {
			t.Fatalf("FR-060/NB-9: the message text must never reach the log; got line %q", line)
		}
	}
	require.Equal(t, 1, idLines,
		"S-53: exactly one info log line may contain the sender id (first recording only)")
	require.True(t, strings.Contains(strings.Join(lines, "\n"), "channel ignored a sender not on the allowed list"),
		"FR-060: the info line must use the normative message")

	count, recent := ch.RejectedSenders()
	require.Equal(t, 2, count, "S-53: both writes are counted")
	require.Equal(t, []string{"discord:9"}, recent, "S-53: recent holds the sender once, most recent first")
}

func TestRejectedSender_LogLineFieldsAndHint(t *testing.T) {
	// FR-060: the info line's fields are instance_id, sender and the hint —
	// never the text, never the allow list.
	logPath := captureRealLoggerFile(t)
	ch, _ := newRejectedFixture(t, []string{"discord:1"})

	ch.HandleMessage(context.Background(), bus.Peer{}, "m1", "9", "c1",
		"secret-ish body", nil, nil, rejectedSenderInfo("discord:9"))

	var record map[string]any
	found := false
	for _, line := range logFileLines(t, logPath) {
		if !strings.Contains(line, "channel ignored a sender not on the allowed list") {
			continue
		}
		found = true
		require.NoError(t, json.Unmarshal([]byte(line), &record))
	}
	require.True(t, found, "FR-060: the normative info line must be present in the real log")
	require.Equal(t, "discord", record["instance_id"], "FR-060: field instance_id")
	require.Equal(t, "discord:9", record["sender"], "FR-060: field sender carries the canonical id")
	require.Equal(t, "add this id to allow_from to admit it", record["hint"],
		"FR-060: field hint is the normative operator hint")
	require.NotContains(t, record, "text", "FR-060: the record must not carry a text field")
	require.NotContains(t, record, "content", "FR-060: the record must not carry a content field")
}

func TestRejectedSender_MemoryOf20MostRecentFirstRelogAfterEviction(t *testing.T) {
	// S-55: 21 distinct senders evict the oldest from `recent`; the evicted
	// sender is logged a second time when it writes again.
	logPath := captureRealLoggerFile(t)
	ch, _ := newRejectedFixture(t, []string{"discord:1"})

	record := func(n int) {
		canonical := "discord:" + strconv.Itoa(n)
		ch.HandleMessage(context.Background(), bus.Peer{}, strconv.Itoa(n), strconv.Itoa(n), "c1",
			"body", nil, nil, rejectedSenderInfo(canonical))
	}
	for i := 2; i <= 22; i++ { // senders discord:2 … discord:22 → 21 distinct
		record(i)
	}
	count, recent := ch.RejectedSenders()
	require.Equal(t, 21, count, "S-55: every write is counted")
	require.Len(t, recent, 20, "S-55: recent holds at most 20 ids")
	require.Equal(t, "discord:22", recent[0], "S-55: most recent first")
	require.NotContains(t, recent, "discord:2", "S-55: the oldest sender was evicted from recent")

	ch.HandleMessage(context.Background(), bus.Peer{}, "again", "2", "c1",
		"body", nil, nil, rejectedSenderInfo("discord:2"))
	count, recent = ch.RejectedSenders()
	require.Equal(t, 22, count)
	require.Equal(t, "discord:2", recent[0], "S-55: the re-recorded evicted sender is first again")

	idLines := 0
	for _, line := range logFileLines(t, logPath) {
		// Anchor on the quoted JSON field: "discord:2" is a substring of
		// "discord:22", which this fixture also records.
		if strings.Contains(line, `"sender":"discord:2"`) {
			idLines++
		}
	}
	require.Equal(t, 2, idLines,
		"S-55: the evicted sender is logged a second time (once on eviction path 1st write, once after re-entry)")
}

func TestRejectedSender_RepeatMovesToFirstWithoutSecondLogLine(t *testing.T) {
	// FR-060 ordering clause: an already-recorded sender is moved first, no
	// second log line (covered end-to-end in S-53; pinned here on the unit).
	logPath := captureRealLoggerFile(t)
	ch, _ := newRejectedFixture(t, []string{"discord:1"})

	ch.HandleMessage(context.Background(), bus.Peer{}, "a", "3", "c1", "x", nil, nil, rejectedSenderInfo("discord:3"))
	ch.HandleMessage(context.Background(), bus.Peer{}, "b", "4", "c1", "x", nil, nil, rejectedSenderInfo("discord:4"))
	ch.HandleMessage(context.Background(), bus.Peer{}, "c", "3", "c1", "x", nil, nil, rejectedSenderInfo("discord:3"))

	_, recent := ch.RejectedSenders()
	require.Equal(t, []string{"discord:3", "discord:4"}, recent,
		"FR-060: the repeat sender moves first; order is recency, not insertion")

	idLines := 0
	for _, line := range logFileLines(t, logPath) {
		if strings.Contains(line, "discord:3") {
			idLines++
		}
	}
	require.Equal(t, 1, idLines, "FR-060: the repeat sender must not log a second line")
}

func TestEmptyAllowListRecordsNothingAndAdmitsEveryone(t *testing.T) {
	// S-56: an empty allow list admits everyone today and records nothing.
	_ = captureRealLoggerFile(t)
	ch, _ := newRejectedFixture(t, nil)

	err := ch.HandleMessage(context.Background(), bus.Peer{}, "m1", "77", "c1",
		"hello", nil, nil, rejectedSenderInfo("discord:77"))
	require.NoError(t, err, "S-56: with no allow list the message is admitted")

	count, recent := ch.RejectedSenders()
	require.Equal(t, 0, count, "S-56: nothing is recorded for an admitted sender")
	require.Empty(t, recent, "S-56: recent stays empty")
}

