package session

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// #1452 security gate SEC-1: a shortened display value must not keep the
// complete source allocation reachable. Every value that historyProjectEntry
// stores in the selection cache comes from historyShortenForDisplay, so the
// retained heap of its outputs must scale with the bounded previews, not with
// the sum of the full results/texts they were cut from.

const historyRetentionSources = 32
const historyRetentionSourceBytes = 1 << 20

// historyRetainedHeapDelta measures the live heap that remains reachable
// through build's return value once everything else it allocated is garbage.
func historyRetainedHeapDelta(t *testing.T, build func() []TranscriptEntry) int64 {
	t.Helper()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&before)
	kept := build()
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(kept)
	return int64(after.HeapAlloc) - int64(before.HeapAlloc)
}

func TestHistoryShortenForDisplay_DetachesPreviewFromFullAllocation(t *testing.T) {
	full := int64(historyRetentionSources * historyRetentionSourceBytes)
	// Generous bound: the bounded previews total 128 KiB (results) and
	// 2 MiB (texts); a retained full source would add 32 MiB.
	limit := full / 4

	t.Run("tool_result_preview", func(t *testing.T) {
		retained := historyRetainedHeapDelta(t, func() []TranscriptEntry {
			kept := make([]TranscriptEntry, 0, historyRetentionSources)
			for i := 0; i < historyRetentionSources; i++ {
				body := strings.Repeat(string(rune('a'+i%26)), historyRetentionSourceBytes)
				in := TranscriptEntry{ID: fmt.Sprintf("r%d", i), Role: "assistant", ToolCalls: []ToolCall{{ID: ToolCallID(fmt.Sprintf("c%d", i)), Result: map[string]any{"text": body}}}}
				out, err := historyShortenForDisplay(in)
				require.NoError(t, err)
				d, ok := out.ToolCalls[0].HistoryDisplayPreview()
				require.True(t, ok, "a result over the inline threshold is shown as a preview")
				require.LessOrEqual(t, len(d.Preview), ChatHistoryResultPreviewBytes)
				require.True(t, strings.HasPrefix(`{"text":"`+body+`"}`, d.Preview))
				kept = append(kept, out)
			}
			return kept
		})
		t.Logf("retained_heap_bytes=%d full_source_bytes=%d limit=%d", retained, full, limit)
		require.Less(t, retained, limit, "SEC-1: result previews keep their full JSON encodings reachable")
	})

	t.Run("message_text_prefix", func(t *testing.T) {
		retained := historyRetainedHeapDelta(t, func() []TranscriptEntry {
			kept := make([]TranscriptEntry, 0, historyRetentionSources)
			for i := 0; i < historyRetentionSources; i++ {
				in := TranscriptEntry{ID: fmt.Sprintf("m%d", i), Role: "assistant", Content: strings.Repeat(string(rune('a'+i%26)), historyRetentionSourceBytes)}
				out, err := historyShortenForDisplay(in)
				require.NoError(t, err)
				require.Len(t, out.Content, ChatHistoryTextDisplayMaxBytes)
				require.Equal(t, historyRetentionSourceBytes, out.DisplayContentOriginalSizeBytes())
				// Compare addresses, not pointees (testify compares pointers deeply).
				require.NotEqual(t, uintptr(unsafe.Pointer(unsafe.StringData(in.Content))), uintptr(unsafe.Pointer(unsafe.StringData(out.Content))), "SEC-1: the shortened text must not alias the full content")
				kept = append(kept, out)
			}
			return kept
		})
		t.Logf("retained_heap_bytes=%d full_source_bytes=%d limit=%d", retained, full, limit)
		require.Less(t, retained, limit, "SEC-1: shortened texts keep their full contents reachable")
	})

	t.Run("utf8_boundaries_kept", func(t *testing.T) {
		// "é" is two bytes; an odd lead byte puts every limit inside a rune.
		text := "a" + strings.Repeat("é", ChatHistoryTextDisplayMaxBytes)
		out, err := historyShortenForDisplay(TranscriptEntry{Role: "user", Content: text})
		require.NoError(t, err)
		require.True(t, utf8.ValidString(out.Content))
		require.Len(t, out.Content, ChatHistoryTextDisplayMaxBytes-1)
		require.True(t, strings.HasPrefix(text, out.Content))

		res, err := historyShortenForDisplay(TranscriptEntry{Role: "assistant", ToolCalls: []ToolCall{{ID: "c", Result: map[string]any{"text": strings.Repeat("é", ChatHistoryInlineResultMaxBytes)}}}})
		require.NoError(t, err)
		d, ok := res.ToolCalls[0].HistoryDisplayPreview()
		require.True(t, ok)
		// `{"text":"` is 9 bytes, so the 4,096-byte limit falls inside a rune.
		require.True(t, utf8.ValidString(d.Preview))
		require.Len(t, d.Preview, ChatHistoryResultPreviewBytes-1)
		require.True(t, strings.HasPrefix(`{"text":"`+strings.Repeat("é", 2100), d.Preview))
	})
}
