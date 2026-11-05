package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FIX ROUND 4 / FR3-SFH-1: adopt the review's explicit partial-summary shape.
// This contract comes from the brief/review, not observed implementation output.
const retentionGatewayPartialSummary = "retention_sweep: completed with session sweep errors"

// Observe inside each real cleanup callback as well as after the tick: a
// terminal summary must not be published before both cleanups have returned.
func retentionGatewayRequireSummaryCount(t *testing.T, logs *syncLogBuffer, want int) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(logs.String()))
	decoder.UseNumber()
	var summaries []map[string]any
	for {
		var event map[string]any
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err, "summary capture must not silently drop malformed JSON")
		// Match both event identities AND message identities: an unexpected
		// message on the ordinary event must not evade the whole-shape check.
		if event["event"] == "retention_sweep" || event["event"] == "retention_sweep_partial" ||
			event["msg"] == "retention_sweep: completed" || event["msg"] == retentionGatewayPartialSummary {
			summaries = append(summaries, event)
		}
	}
	require.Len(t, summaries, want, "exactly one terminal summary per FINISHED tick; none during either cleanup")
	return summaries
}

func retentionGatewayRequireTickSummary(t *testing.T, logs *syncLogBuffer, reason string, ticks int) {
	t.Helper()
	summaries := retentionGatewayRequireSummaryCount(t, logs, ticks)
	// Each fixture tick actually removes ONE aged session and ONE aged tool
	// result, and visits ONE persisted task. Counts are not invented by hooks.
	want := map[string]any{
		"level":               "INFO",
		"msg":                 "retention_sweep: completed",
		"event":               "retention_sweep",
		"removed":             json.Number("1"),
		"tool_result_removed": json.Number("1"),
		"task_runs_visited":   json.Number("1"),
	}
	if reason == "storage_error" {
		want["level"] = "WARN"
		want["msg"] = retentionGatewayPartialSummary
		want["event"] = "retention_sweep_partial"
		want["session_sweep_failed"] = true
	}
	for i, event := range summaries {
		timestamp, ok := event["time"].(string)
		require.True(t, ok, "summary %d must retain the structured-log timestamp", i+1)
		_, err := time.Parse(time.RFC3339Nano, timestamp)
		require.NoError(t, err, "summary %d timestamp is valid log metadata", i+1)
		duration, ok := event["duration_ms"].(json.Number)
		require.True(t, ok, "summary %d duration_ms must remain an integer", i+1)
		millis, err := duration.Int64()
		require.NoError(t, err, "summary %d duration_ms must remain an integer", i+1)
		require.GreaterOrEqual(t, millis, int64(0), "summary duration is metadata, not a speed bound")
		fields := make(map[string]any, len(event))
		for key, value := range event {
			if key != "time" && key != "duration_ms" {
				fields[key] = value
			}
		}
		// Whole-map equality also forbids error/cause/path/identifier/content
		// extras. A partial tick cannot emit even ONE ordinary healthy summary.
		assert.Equal(t, want, fields, "tick summary %d for %s: exact content-free outcome; storage errors require partial, healthy stays unchanged", i+1, reason)
	}
}
