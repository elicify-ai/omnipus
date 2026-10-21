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
	require.NotEmpty(t, prepared.encodedResponse)
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
	require.NotEmpty(t, prepared.encodedResponse)
	message, serverError := validateBodyAgainstSchema("SessionHistoryResponse", prepared.encodedResponse)
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
				if readErr != nil || len(entries) != prefix+200 {
					benchmarkErr = fmt.Errorf("real full-read instrument failed: count=%d err=%v", len(entries), readErr)
					b.Fatal(benchmarkErr)
				}
				runtime.KeepAlive(entries)
			}
			prepared, readErr := historyMeasurementOperation(context.Background(), fixture)
			if readErr != nil {
				benchmarkErr = fmt.Errorf("real complete gateway page operation: %w", readErr)
				b.Fatal(benchmarkErr)
			}
			if len(prepared.Response.Entries) != 40 || len(prepared.encodedResponse) == 0 || prepared.Response.PageInfo.Limit != 40 || prepared.Response.PageInfo.MinRows != 20 {
				benchmarkErr = fmt.Errorf("measurement did not execute the validated40/20 complete encoded page: groups=%d bytes=%d", len(prepared.Response.Entries), len(prepared.encodedResponse))
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
	delta := records[1].AllocatedBytes - records[0].AllocatedBytes
	if delta < 0 {
		delta = -delta
	}
	require.LessOrEqual(t, float64(delta), float64(records[0].AllocatedBytes)*0.10, "D-A1/H: complete gateway+carrier allocations vary at most10%% with equal suffixes")
	shortWrong := historyMeasureCompletePage(t, fixtures[0], 1000, true)
	longWrong := historyMeasureCompletePage(t, fixtures[1], 100000, true)
	require.Greater(t, float64(longWrong.AllocedBytesPerOp()-shortWrong.AllocedBytesPerOp()), float64(shortWrong.AllocedBytesPerOp())*0.10, "instrument must reject the planted REAL full-read allocation regression")
	t.Log("T18_FULL_READ_NEGATIVE_CONTROL=PASS actual full-read composition violates the adopted allocation comparator")
	require.Len(t, records, 2)
	file := os.Getenv("OMNIPUS_CHAT_HISTORY_MEASUREMENT_RECEIPT")
	if file != "" {
		require.True(t, filepath.IsAbs(file), "Section O receipt location is absolute")
		var text strings.Builder
		_, markerErr := fmt.Fprintf(&text, "HEAD: %s\n", historyMeasurementCandidate(t))
		require.NoError(t, markerErr)
		encoder := json.NewEncoder(&text)
		for _, record := range records {
			require.NoError(t, encoder.Encode(record))
		}
		require.NoError(t, os.WriteFile(file, []byte(text.String()), 0o600))
	}
}
