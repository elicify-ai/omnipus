package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

type historyPageMeasurement struct { // not-wire-format: private Section-H CI evidence.
	Message        string `json:"message"`
	Case           string `json:"case"`
	SampleCount    int    `json:"sample_count"`
	Limit          int    `json:"limit"`
	MinRows        int    `json:"min_rows"`
	AllocatedBytes int64  `json:"allocated_bytes"`
}

func historyMeasurementOperation(ctx context.Context, fixture historyMeasurementFixture) (preparedHistoryPage, error) {
	request, err := newHistoryPageRequest(fixture.sid, session.ChatHistoryPageOptions{Limit: 40, MinRows: 20}, historyPageProjectionDeps{})
	if err != nil {
		return preparedHistoryPage{}, err
	}
	return request.Read(ctx, fixture.store)
}

func historyMeasurementSmallControl(t *testing.T) {
	t.Helper()
	store, storeErr := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, storeErr)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	meta, createErr := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, createErr)
	require.NoError(t, store.AppendTranscript(meta.ID, session.TranscriptEntry{ID: "measurement-instrument", Role: "user", Content: "controlled real page", AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}))
	require.NoError(t, store.Close(), "join the small control's flusher before any measurement")
	prepared, readErr := historyMeasurementOperation(context.Background(), historyMeasurementFixture{store: store, sid: meta.ID})
	if errors.Is(readErr, session.ErrChatHistoryNotImplemented) {
		t.Fatal("BLOCKED: complete gateway page preparation not implemented — required by spec T18 / seams H-O")
	}
	require.NoError(t, readErr, "actual full gateway preparation must work before heavy fixture setup or allocation claims")
	require.Len(t, prepared.Response.Entries, 1)
	require.Equal(t, "measurement-instrument", prepared.Response.Entries[0].Id)
	historyMeasurementValidateCarrier(t, prepared)
	require.Equal(t, 40, prepared.Response.PageInfo.Limit)
	require.Equal(t, 20, prepared.Response.PageInfo.MinRows)
}

func historyMeasurementPositivePage(t *testing.T, fixture historyMeasurementFixture) {
	t.Helper()
	prepared, readErr := historyMeasurementOperation(context.Background(), fixture)
	require.NoError(t, readErr, "real reader/projection/encoded generated carrier, not a core-only or buffer proxy")
	require.Len(t, prepared.Response.Entries, 40)
	for i, entry := range prepared.Response.Entries {
		require.Equal(t, fmt.Sprintf("equal-suffix-%03d", 160+i), entry.Id)
		require.NotNil(t, entry.Content)
		require.Equal(t, "identical measured suffix", *entry.Content)
		require.NotNil(t, entry.HistoryPosition)
		require.Regexp(t, `^hp1_[A-Za-z0-9_-]{43}$`, *entry.HistoryPosition)
	}
	require.True(t, prepared.Response.HasEarlier)
	require.False(t, prepared.Response.HasLater)
	require.Equal(t, 40, prepared.Response.PageInfo.SourceGroupCount)
	require.Equal(t, 40, prepared.Response.PageInfo.RowCount)
	require.Equal(t, 40, prepared.Response.PageInfo.Limit)
	require.Equal(t, 20, prepared.Response.PageInfo.MinRows)
	historyMeasurementValidateCarrier(t, prepared)
}

// Validate the actual generated response independently, outside the interval.
// Do not depend on a GREEN-only private encoded buffer: the agreed preparation
// declaration exposes Response/ReplayFrames and must compile on the RED base.
// The timed real Read still performs its production carrier validation/encoding.
func historyMeasurementValidateCarrier(t *testing.T, prepared preparedHistoryPage) {
	t.Helper()
	encoded, encodeErr := json.Marshal(prepared.Response)
	require.NoError(t, encodeErr)
	require.NotEmpty(t, encoded)
	require.NotEmpty(t, prepared.ReplayFrames)
	message, serverError := validateBodyAgainstSchema("SessionHistoryResponse", encoded)
	require.False(t, serverError)
	require.Empty(t, message)
}

func historyMeasureCompletePage(t *testing.T, fixture historyMeasurementFixture, prefix int, fullReadControl bool) testing.BenchmarkResult {
	t.Helper()
	var benchmarkErr error
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if fullReadControl {
				// A real deliberately wrong full-read composition: this should fail
				// the SAME prefix-allocation comparator, without mutating production.
				entries, readErr := fixture.store.ReadTranscript(fixture.sid)
				if readErr != nil {
					benchmarkErr = fmt.Errorf("real full-read instrument failed: %w", readErr)
					b.Fatal(benchmarkErr)
				}
				if len(entries) != prefix+200 {
					benchmarkErr = fmt.Errorf("real full-read instrument count=%d, expected=%d", len(entries), prefix+200)
					b.Fatal(benchmarkErr)
				}
				runtime.KeepAlive(entries)
			}
			prepared, readErr := historyMeasurementOperation(context.Background(), fixture)
			if readErr != nil {
				benchmarkErr = fmt.Errorf("real complete gateway page operation: %w", readErr)
				b.Fatal(benchmarkErr)
			}
			if len(prepared.Response.Entries) != 40 || len(prepared.ReplayFrames) == 0 || prepared.Response.PageInfo.Limit != 40 || prepared.Response.PageInfo.MinRows != 20 {
				benchmarkErr = fmt.Errorf("measurement did not execute the validated40/20 complete prepared page: groups=%d frames=%d", len(prepared.Response.Entries), len(prepared.ReplayFrames))
				b.Fatal(benchmarkErr)
			}
			runtime.KeepAlive(prepared)
		}
	})
	require.NoError(t, benchmarkErr, "a failed benchmark operation must fail the owning test too")
	require.Positive(t, result.N, "actual benchmark samples, never missing evidence as0")
	require.Positive(t, result.AllocedBytesPerOp(), "full actual operation has observable allocation; projected bytes are not B/op")
	return result
}

func historyMeasurementCandidate(t *testing.T) string {
	t.Helper()
	sha := os.Getenv("OMNIPUS_CANDIDATE_SHA")
	if sha == "" {
		command := exec.Command("git", "rev-parse", "HEAD")
		command.Dir = historyMeasurementRepo(t)
		output, gitErr := command.Output()
		require.NoError(t, gitErr)
		sha = strings.TrimSpace(string(output))
	}
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{40}$`), sha, "full exact candidate revision, never an invented receipt marker")
	return sha
}

// Return whether a file was written; an unset output path is deliberately a
// no-op for this writer, never a gate around the real measurements or checks.
func historyMeasurementWriteReceipt(t *testing.T, records []historyPageMeasurement) bool {
	t.Helper()
	file := os.Getenv("OMNIPUS_CHAT_HISTORY_MEASUREMENT_RECEIPT")
	if file == "" {
		return false
	}
	require.True(t, filepath.IsAbs(file), "Section O receipt location is absolute")
	require.Len(t, records, 2)
	var text strings.Builder
	_, markerErr := fmt.Fprintf(&text, "HEAD: %s\n", historyMeasurementCandidate(t))
	require.NoError(t, markerErr)
	encoder := json.NewEncoder(&text)
	for _, record := range records {
		require.NoError(t, encoder.Encode(record))
	}
	require.NoError(t, os.WriteFile(file, []byte(text.String()), 0o600))
	return true
}

func TestHistoryPageMeasurement_SectionHReceipt(t *testing.T) {
	// Exact named remote-only heavy probe. No receipt-env gate: unset still
	// constructs, measures and verifies both records in memory.
	historyMeasurementSmallControl(t)
	binary := historyMeasurementDriver(t)
	fixtures := [2]historyMeasurementFixture{
		historyMeasurementBuildFixture(t, binary, 1000),
		historyMeasurementBuildFixture(t, binary, 100000),
	}
	records := make([]historyPageMeasurement, 0, 2)
	for index, label := range []string{"short_prefix", "long_prefix"} {
		historyMeasurementPositivePage(t, fixtures[index])
		result := historyMeasureCompletePage(t, fixtures[index], []int{1000, 100000}[index], false)
		record := historyPageMeasurement{Message: "history_page_measurement", Case: label, SampleCount: result.N, Limit: 40, MinRows: 20, AllocatedBytes: result.AllocedBytesPerOp()}
		records = append(records, record)
		t.Logf("complete-page case=%s sample_count=%d allocated_bytes=%d", label, record.SampleCount, record.AllocatedBytes)
	}
	require.Len(t, records, 2)
	// Preserve actual evidence even when the performance criterion fails below.
	// The receipt is measurements, not a PASS; CI must still honor this test's exit.
	originalPath := os.Getenv("OMNIPUS_CHAT_HISTORY_MEASUREMENT_RECEIPT")
	written := historyMeasurementWriteReceipt(t, records)
	require.Equal(t, originalPath != "", written)
	var originalReceipt []byte
	if written {
		var readErr error
		originalReceipt, readErr = os.ReadFile(originalPath)
		require.NoError(t, readErr)
	}
	shortWrong := historyMeasureCompletePage(t, fixtures[0], 1000, true)
	longWrong := historyMeasureCompletePage(t, fixtures[1], 100000, true)
	negativeDelta := longWrong.AllocedBytesPerOp() - shortWrong.AllocedBytesPerOp()
	negativeDetected := float64(negativeDelta) > float64(shortWrong.AllocedBytesPerOp())*0.10
	t.Logf("full-read-control short_allocated_bytes=%d long_allocated_bytes=%d", shortWrong.AllocedBytesPerOp(), longWrong.AllocedBytesPerOp())
	if negativeDetected {
		t.Log("T18_FULL_READ_NEGATIVE_CONTROL=PASS actual full-read composition violates the adopted allocation comparator")
	}
	// Exercise unset-output behavior with these SAME real measured records. It
	// must neither bypass the earlier measurements nor alter their saved evidence.
	measuredCopy := append([]historyPageMeasurement(nil), records...)
	t.Run("unset_receipt", func(t *testing.T) {
		t.Setenv("OMNIPUS_CHAT_HISTORY_MEASUREMENT_RECEIPT", "")
		require.False(t, historyMeasurementWriteReceipt(t, records))
		require.Equal(t, measuredCopy, records)
		if written {
			after, readErr := os.ReadFile(originalPath)
			require.NoError(t, readErr)
			require.Equal(t, originalReceipt, after, "unset output cannot rewrite the existing actual receipt")
		}
		t.Log("T18_UNSET_RECEIPT_CONTROL=PASS same real measurements retained; no receipt write")
	})
	// Accumulate criterion failures at the END: never turn the unchanged10%
	// bound into a warning/pass or prevent the later controls from executing.
	delta := records[1].AllocatedBytes - records[0].AllocatedBytes
	if delta < 0 {
		delta = -delta
	}
	if float64(delta) > float64(records[0].AllocatedBytes)*0.10 {
		t.Errorf("D-A1/H: complete gateway+carrier allocations exceed unchanged10%% bound: delta=%d allowed=%g short=%d long=%d", delta, float64(records[0].AllocatedBytes)*0.10, records[0].AllocatedBytes, records[1].AllocatedBytes)
	}
	if !negativeDetected {
		t.Errorf("instrument must reject the planted REAL full-read allocation regression: delta=%d allowed=%g", negativeDelta, float64(shortWrong.AllocedBytesPerOp())*0.10)
	}
}
