// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

// Restored behavioural pack for recall_conversation's archive_range mode.
//
// WHY this file exists: the original 17-case pack (recall_archive_range_test.go,
// recall_archive_range_runtime_test.go, recall_archive_range_breadcrumb_test.go)
// was deleted with the retired JSONL content backend, while the production mode
// stayed live (QA-CHECK-final-gate Q01 / S1 H1). This file re-homes the same
// behaviours onto the canonical archive.
//
// ORACLE SOURCES (independent of the implementation under test):
//   - ADR-066 MAJ-CW-003 + MIN-001 and overflow spec B-58: an archive_range page
//     is the INCLUSIVE, zero-based rune slice of the literal archived bytes,
//     framed with total runes and the next offset.
//   - The raw-range amendment: the quoted representation is the literal stored
//     model-payload JSON, not the whole envelope. The pack therefore takes its
//     expected bytes from the STORE's own bounded scan (a different call from
//     the tool's), never from a re-encoding.
//   - Rune arithmetic is derived here with []rune, never read off the tool.
//
// UNIT BOUNDARY: the tool, the real session.UnifiedStore and its scanner are
// real. Only the loop's span state (cwRangeSink) is a boundary double, because
// that is where the tool's effect lands.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/require"
)

// cwRangeSink is the tool's external boundary — the loop's recall-span state.
// It is not a mock of the unit under test: the tool, the store and the scanner
// all stay real.
type cwRangeSink struct {
	cs    config.ContextSettings
	spans map[string]*RecallSpan
	drops int
}

func cwNewRangeSink(capRunes int) *cwRangeSink {
	cs := config.DefaultContextSettings()
	if capRunes > 0 {
		cs.BuiltinSuccessCap = capRunes
	}
	return &cwRangeSink{cs: cs, spans: make(map[string]*RecallSpan)}
}

func (s *cwRangeSink) contextSettings() config.ContextSettings { return s.cs }
func (s *cwRangeSink) setRecallSpan(key string, span *RecallSpan) {
	if span == nil {
		delete(s.spans, key)
		return
	}
	s.spans[key] = span
}
func (s *cwRangeSink) dropRecallSpan(key, _ string) {
	s.drops++
	delete(s.spans, key)
}

// cwRangeArchive appends one model record per message through the CHECKED model
// seam (so every record takes a real ordinal) and returns the store plus the
// literal payload bytes the archive now holds, read back through the store's own
// bounded scan.
func cwRangeArchive(t *testing.T, key string, msgs ...providers.Message) (*session.UnifiedStore, string) {
	t.Helper()
	store, err := session.NewUnifiedStore(t.TempDir())
	require.NoError(t, err, "fixture: open a real archive store")
	t.Cleanup(func() { _ = store.Close() })

	var lastAssistant *session.ArchiveAddress
	for i, m := range msgs {
		in := session.ModelAppend{
			Message:        m,
			ViewMembership: session.ViewMembershipBoth,
			Source:         session.EntrySource{Kind: "user"},
			AgentID:        "mia",
		}
		if m.Role == "tool" {
			require.NotNil(t, lastAssistant, "fixture: a tool result needs its issuing assistant at index %d", i)
			in.ToolResultFor = lastAssistant
		}
		slot, _, aerr := store.AppendModelMessage(context.Background(), key, in)
		require.NoError(t, aerr, "fixture: checked model append at index %d", i)
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			addr := slot.Addr
			lastAssistant = &addr
		}
	}
	return store, cwRangeWhole(t, store, key)
}

// cwRangeOrdinals reports how many model slots the archive holds.
func cwRangeOrdinals(t *testing.T, store *session.UnifiedStore, key string) int {
	t.Helper()
	view, err := store.WindowView(context.Background(), key)
	require.NoError(t, err, "fixture: read the window")
	return view.State.Count
}

// cwRangeLiteral is the archive's own concatenated literal payload bytes for
// the inclusive ordinal range [from,to].
func cwRangeLiteral(t *testing.T, store *session.UnifiedStore, key string, from, to int) string {
	t.Helper()
	if to < from {
		return ""
	}
	var b strings.Builder
	err := store.ScanArchiveRange(context.Background(), key, from, to,
		func(_ int, raw []byte, _ memory.ArchivedMessage) error {
			b.Write(raw)
			return nil
		})
	require.NoError(t, err, "fixture: read the archive's literal payload bytes")
	return b.String()
}

// cwRangeWhole is the whole archive's literal bytes.
func cwRangeWhole(t *testing.T, store *session.UnifiedStore, key string) string {
	t.Helper()
	n := cwRangeOrdinals(t, store, key)
	if n == 0 {
		return ""
	}
	return cwRangeLiteral(t, store, key, 0, n-1)
}

func cwRangeArgs(from, to int) map[string]any {
	return map[string]any{"archive_range": map[string]any{"from": from, "to": to}}
}

func cwRangeExecute(t *testing.T, tool *RecallConversationTool, ctx context.Context, args map[string]any) *tools.ToolResult {
	t.Helper()
	res := tool.Execute(ctx, args)
	require.NotNil(t, res, "MAJ-CW-003: Execute returned nil instead of a structured result")
	require.False(t, res.IsError, "MAJ-CW-003: valid archive_range must return a literal page, got: %s", res.ForLLM)
	return res
}

// The framing vocabulary is fixed by the implementation's own contract
// (archiveRangePageFraming): total_runes and next_offset. The pack asserts the
// NUMBERS and the payload independently, never an invented sentence.
var (
	cwRangeTotalField = regexp.MustCompile(`\btotal_runes\s*=\s*([0-9]+)\b`)
	cwRangeNextField  = regexp.MustCompile(`\bnext_offset\s*=\s*(end|[0-9]+)\b`)
)

type cwRangePage struct {
	header  string
	payload string
	total   int
	next    int // -1 denotes the explicitly stated end
}

func cwReadRangePage(t *testing.T, res *tools.ToolResult) cwRangePage {
	t.Helper()
	header, payload, ok := strings.Cut(res.ForLLM, "\n")
	require.True(t, ok, "MAJ-CW-003: page must separate framing from literal JSONL: %q", res.ForLLM)
	total := cwRangeTotalField.FindStringSubmatch(header)
	next := cwRangeNextField.FindStringSubmatch(header)
	require.Len(t, total, 2, "MAJ-CW-003: framing must state total runes: %q", header)
	require.Len(t, next, 2, "MAJ-CW-003: framing must state the next offset: %q", header)
	n, err := strconv.Atoi(total[1])
	require.NoError(t, err, "invalid total-rune metadata: %v", err)
	p := cwRangePage{header: header, payload: payload, total: n, next: -1}
	if next[1] != "end" {
		p.next, err = strconv.Atoi(next[1])
		require.NoError(t, err, "invalid next-offset metadata: %v", err)
	}
	require.True(t, utf8.ValidString(res.ForLLM), "MIN-001: response splits a UTF-8 encoding")
	return p
}

// cwAssertRangePage derives the expected page from the raw bytes with plain
// rune arithmetic — never from the tool.
func cwAssertRangePage(t *testing.T, page cwRangePage, raw string, offset, length int) {
	t.Helper()
	runes := []rune(raw)
	start := min(offset, len(runes))
	end := min(offset+length, len(runes))
	want := string(runes[start:end])
	require.Equal(t, want, page.payload,
		"MIN-001: literal rune slice [%d:%d] of the archive bytes", start, end)
	require.Equal(t, len(runes), page.total,
		"MIN-001: total must count runes of the literal archive bytes, newlines included")
	wantNext := -1
	if end < len(runes) {
		wantNext = end
	}
	require.Equal(t, wantNext, page.next, "MIN-001: next offset (-1 means end)")
}

func cwAssertRangeError(t *testing.T, res *tools.ToolResult, fields ...string) {
	t.Helper()
	require.NotNil(t, res, "MAJ-CW-003: want a structured result")
	require.True(t, res.IsError, "MAJ-CW-003: want structured IsError=true, got %+v", res)
	for _, field := range fields {
		require.Contains(t, res.ForLLM, field, "MAJ-CW-003: visible error must identify %q", field)
	}
	require.NotEmpty(t, res.ForLLM, "MAJ-CW-003: error must be visible")
	require.False(t, res.Async, "MAJ-CW-003: error must be synchronous")
}

// --- the restored pack ------------------------------------------------------

// TestRecallConversation_ArchiveRangeRestored re-homes the deleted 17-case
// behavioural pack onto the canonical archive. One exact-name, narrowly scoped
// invocation runs this sub-unit, not pkg/agent's full suite. No t.Parallel.
func TestRecallConversation_ArchiveRangeRestored(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"schema", cwRangeSchema},
		{"inclusive_verbatim_records", cwRangeInclusiveVerbatim},
		{"rune_offset_boundaries", cwRangeOffsetBoundaries},
		{"validation", cwRangeValidation},
		{"mutually_exclusive_modes", cwRangeExclusiveModes},
		{"out_of_range_is_error", cwRangeOutOfRange},
		{"cap_and_live_settings", cwRangeCapAndSettings},
		{"adjacent_pages_reconstruct_literal_bytes", cwRangeReconstructPages},
		{"current_session_only", cwRangeSessionScope},
		{"quoted_data_not_historical_calls", cwRangeDataOnly},
		{"cooperative_cancellation_and_deadline", cwRangeCancellation},
		{"bounded_retained_memory", cwInQuietChild(cwRangeBoundedMemory)},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

// cwRangeSchema pins the tool's published schema for the mode: an inclusive
// zero-based object range with from/to required, plus the shared paging fields.
// The expected shape comes from ADR-066 MAJ-CW-003 / the archive_range contract,
// not from running the tool.
func cwRangeSchema(t *testing.T) {
	tool := NewRecallConversationTool(nil, cwNewRangeSink(0))
	props, ok := tool.Parameters()["properties"].(map[string]any)
	require.True(t, ok, "recall_conversation schema must have object properties")
	rng, ok := props["archive_range"].(map[string]any)
	require.True(t, ok, "BLOCKED: archive_range mode not implemented — required by ADR-066 MAJ-CW-003 + MIN-001 / B-58")
	require.Equal(t, "object", rng["type"], "archive_range schema type")
	bounds, ok := rng["properties"].(map[string]any)
	require.True(t, ok, "archive_range schema must expose from/to")
	for _, name := range []string{"from", "to"} {
		field, ok := bounds[name].(map[string]any)
		require.True(t, ok, "archive_range.%s must be declared", name)
		require.Equal(t, "integer", field["type"], "archive_range.%s type", name)
		require.Equal(t, "0", fmt.Sprint(field["minimum"]), "archive_range.%s minimum", name)
	}
	encoded, err := json.Marshal(rng["required"])
	require.NoError(t, err, "encode required schema")
	var required []string
	require.NoError(t, json.Unmarshal(encoded, &required), "archive_range required names")
	require.ElementsMatch(t, []string{"from", "to"}, required, "archive_range required = from and to, exactly")
	for _, tc := range []struct {
		name string
		min  int
	}{{"offset", 0}, {"length", 1}} {
		field, ok := props[tc.name].(map[string]any)
		require.True(t, ok, "%s must be declared", tc.name)
		require.Equal(t, "integer", field["type"], "%s type", tc.name)
		require.Equal(t, strconv.Itoa(tc.min), fmt.Sprint(field["minimum"]), "%s minimum", tc.name)
	}
}

// cwRangeInclusiveVerbatim: the page for [from,to] is exactly the literal bytes
// of those records, concatenated, with no re-encoding and no dropped last line.
func cwRangeInclusiveVerbatim(t *testing.T) {
	const key = "cw-verbatim"
	msgs := []providers.Message{
		{Role: "user", Content: "zero é"},
		{Role: "assistant", Content: "one 中 é"},
		{Role: "assistant", Content: "two 𝄞\nquoted"},
	}
	store, _ := cwRangeArchive(t, key, msgs...)
	tool := NewRecallConversationTool(store, cwNewRangeSink(0))
	for _, bounds := range [][2]int{{0, 0}, {0, 1}, {1, 1}, {1, 2}, {2, 2}, {0, 2}} {
		t.Run(fmt.Sprintf("%d_through_%d", bounds[0], bounds[1]), func(t *testing.T) {
			raw := cwRangeLiteral(t, store, key, bounds[0], bounds[1])
			res := cwRangeExecute(t, tool, makeCtx(key), cwRangeArgs(bounds[0], bounds[1]))
			cwAssertRangePage(t, cwReadRangePage(t, res), raw, 0, utf8.RuneCountInString(raw))
		})
	}
	// Integral JSON numbers arrive as float64, whereas Go callers also pass int.
	t.Run("integral_json_numbers", func(t *testing.T) {
		args := map[string]any{"archive_range": map[string]any{"from": float64(0), "to": float64(1)}}
		res := cwRangeExecute(t, tool, makeCtx(key), args)
		raw := cwRangeLiteral(t, store, key, 0, 1)
		cwAssertRangePage(t, cwReadRangePage(t, res), raw, 0, utf8.RuneCountInString(raw))
	})
}

// cwRangeOffsetBoundaries walks offset and length across the whole rune length
// of one record: 0, 1, total-1, total, total+1.
func cwRangeOffsetBoundaries(t *testing.T) {
	const key = "cw-offsets"
	store, raw := cwRangeArchive(t, key, providers.Message{Role: "assistant", Content: "Aé中𝄞éZ"})
	tool := NewRecallConversationTool(store, cwNewRangeSink(0))
	total := utf8.RuneCountInString(raw)
	for _, offset := range []int{0, 1, total - 1, total, total + 1} {
		for _, length := range []int{1, 2, total - 1, total, total + 1} {
			t.Run(fmt.Sprintf("offset_%d_length_%d", offset, length), func(t *testing.T) {
				args := cwRangeArgs(0, 0)
				args["offset"], args["length"] = offset, length
				res := cwRangeExecute(t, tool, makeCtx(key), args)
				cwAssertRangePage(t, cwReadRangePage(t, res), raw, offset, length)
			})
		}
	}
}

// cwRangeValidation: every non-integer, missing, negative, inverted or
// fractional bound is refused with a field-naming error, and nothing is stored.
func cwRangeValidation(t *testing.T) {
	const key = "cw-invalid"
	store, _ := cwRangeArchive(t, key, providers.Message{Role: "user", Content: "zero"})
	tool := NewRecallConversationTool(store, cwNewRangeSink(0))
	cases := []struct {
		name       string
		rangeValue any
		field      string
	}{
		{"negative_from", map[string]any{"from": -1, "to": 0}, "from"},
		{"negative_to", map[string]any{"from": 0, "to": -1}, "to"},
		{"inverted", map[string]any{"from": 1, "to": 0}, "from"},
		{"fractional_from", map[string]any{"from": 0.5, "to": 1}, "from"},
		{"fractional_to", map[string]any{"from": 0, "to": 0.5}, "to"},
		{"missing_from", map[string]any{"to": 0}, "from"},
		{"missing_to", map[string]any{"from": 0}, "to"},
		{"null_from", map[string]any{"from": nil, "to": 0}, "from"},
		{"null_to", map[string]any{"from": 0, "to": nil}, "to"},
		{"string_from", map[string]any{"from": "0", "to": 0}, "from"},
		{"string_to", map[string]any{"from": 0, "to": "0"}, "to"},
		{"boolean_from", map[string]any{"from": false, "to": 0}, "from"},
		{"boolean_to", map[string]any{"from": 0, "to": true}, "to"},
		{"null_object", nil, "archive_range"},
		{"empty_object", map[string]any{}, "archive_range"},
		{"string_object", "0-0", "archive_range"},
		{"array_object", []any{0, 0}, "archive_range"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := tool.Execute(makeCtx(key), map[string]any{"archive_range": tc.rangeValue})
			cwAssertRangeError(t, res, "archive_range", tc.field)
		})
	}
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{"offset_negative", "offset", -1},
		{"offset_fractional", "offset", 0.5},
		{"offset_string", "offset", "0"},
		{"offset_boolean", "offset", false},
		{"length_zero", "length", 0},
		{"length_negative", "length", -1},
		{"length_fractional", "length", 1.5},
		{"length_string", "length", "1"},
		{"length_boolean", "length", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := cwRangeArgs(0, 0)
			args[tc.field] = tc.value
			cwAssertRangeError(t, tool.Execute(makeCtx(key), args), tc.field)
		})
	}
}

// cwRangeExclusiveModes: archive_range cannot be combined with any other recall
// mode, even one that is empty or null, and a refused request mutates nothing.
func cwRangeExclusiveModes(t *testing.T) {
	const key = "cw-exclusive"
	store, _ := cwRangeArchive(t, key,
		providers.Message{Role: "user", Content: "exclusive nonce"},
		providers.Message{Role: "assistant", Content: "past", ToolCalls: []providers.ToolCall{{ID: "past-call", Type: "function", Function: &providers.FunctionCall{Name: "read_file", Arguments: "{}"}}}},
		providers.Message{Role: "tool", ToolCallID: "past-call", Content: "past result"},
	)
	cases := []struct {
		name string
		key  string
		val  any
	}{
		{"query", "query", "exclusive nonce"},
		{"turn_range", "turn_range", "1-1"},
		{"time", "time", map[string]any{"from": 0}},
		{"tool_call_id", "tool_call_id", "past-call"},
		{"empty_query", "query", ""},
		{"empty_turn_range", "turn_range", ""},
		{"null_time", "time", nil},
		{"empty_tool_call_id", "tool_call_id", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := cwNewRangeSink(0)
			tool := NewRecallConversationTool(store, sink)
			args := cwRangeArgs(0, 0)
			args[tc.key] = tc.val
			cwAssertRangeError(t, tool.Execute(makeCtx(key), args), "archive_range", "exactly one")
			require.Empty(t, sink.spans, "ambiguous request must not install a recall span")
			require.Zero(t, sink.drops, "ambiguous request must not drop a recall span")
		})
	}
}

// cwRangeOutOfRange: a range that leaves the archive is a visible error, and an
// empty archive has no page at all.
func cwRangeOutOfRange(t *testing.T) {
	const key = "cw-outside"
	store, _ := cwRangeArchive(t, key,
		providers.Message{Role: "assistant", Content: "zero"},
		providers.Message{Role: "assistant", Content: "one"},
		providers.Message{Role: "assistant", Content: "two"},
	)
	tool := NewRecallConversationTool(store, cwNewRangeSink(0))
	for _, b := range [][2]int{{0, 3}, {2, 3}, {3, 3}, {4, 5}} {
		t.Run(fmt.Sprintf("%d_through_%d", b[0], b[1]), func(t *testing.T) {
			cwAssertRangeError(t, tool.Execute(makeCtx(key), cwRangeArgs(b[0], b[1])), "archive_range")
		})
	}
	t.Run("empty_archive", func(t *testing.T) {
		empty, err := session.NewUnifiedStore(t.TempDir())
		require.NoError(t, err)
		t.Cleanup(func() { _ = empty.Close() })
		emptyTool := NewRecallConversationTool(empty, cwNewRangeSink(0))
		cwAssertRangeError(t, emptyTool.Execute(makeCtx("cw-empty"), cwRangeArgs(0, 0)), "archive_range")
	})
}

// cwRangeCapAndSettings: the page is bounded by the LIVE result cap, an
// oversized length clamps to the same deterministic page as the default, and
// the framing reserve is charged inside the cap.
func cwRangeCapAndSettings(t *testing.T) {
	const key = "cw-cap"
	store, raw := cwRangeArchive(t, key, providers.Message{Role: "assistant", Content: strings.Repeat("é中𝄞", 20_000)})
	sink := cwNewRangeSink(1024)
	tool := NewRecallConversationTool(store, sink)
	var previousLength int
	for _, capRunes := range []int{1024, 2048, 4096} {
		t.Run(fmt.Sprintf("cap_%d", capRunes), func(t *testing.T) {
			sink.cs.BuiltinSuccessCap = capRunes // same tool, live settings per call
			res := cwRangeExecute(t, tool, makeCtx(key), cwRangeArgs(0, 0))
			page := cwReadRangePage(t, res)
			n := utf8.RuneCountInString(page.payload)
			cwAssertRangePage(t, page, raw, 0, n)
			require.LessOrEqual(t, utf8.RuneCountInString(res.ForLLM), capRunes,
				"page plus framing must fit the live result cap")
			require.NotZero(t, n, "a nonempty range with a sufficient cap must return a nonempty page")
			if previousLength != 0 {
				require.Greater(t, n, previousLength,
					"raising the live cap must raise this non-final page")
			}
			previousLength = n
			args := cwRangeArgs(0, 0)
			args["length"] = utf8.RuneCountInString(raw) + 1
			clamped := cwReadRangePage(t, cwRangeExecute(t, tool, makeCtx(key), args))
			require.Equal(t, page, clamped, "an oversized length must clamp to the same deterministic default page")
			args["offset"], args["length"] = 7, 11
			cwAssertRangePage(t, cwReadRangePage(t, cwRangeExecute(t, tool, makeCtx(key), args)), raw, 7, 11)
		})
	}
}

// cwRangeReconstructPages: walking next_offset until "end" reproduces the
// literal archive bytes exactly — nothing lost, doubled or rewritten.
func cwRangeReconstructPages(t *testing.T) {
	const key = "cw-reconstruct"
	store, raw := cwRangeArchive(t, key,
		providers.Message{Role: "assistant", Content: "Aé中𝄞é"},
		providers.Message{Role: "assistant", Content: "B\nCé"},
	)
	tool := NewRecallConversationTool(store, cwNewRangeSink(0))
	for _, length := range []int{1, 2, 3, 4, 7} {
		t.Run(fmt.Sprintf("length_%d", length), func(t *testing.T) {
			var reconstructed strings.Builder
			total := utf8.RuneCountInString(raw)
			offset := 0
			for pageNumber := 0; ; pageNumber++ {
				require.LessOrEqual(t, pageNumber, total,
					"next offset must terminate after at most total-runes pages")
				args := cwRangeArgs(0, 1)
				args["offset"], args["length"] = offset, length
				page := cwReadRangePage(t, cwRangeExecute(t, tool, makeCtx(key), args))
				cwAssertRangePage(t, page, raw, offset, length)
				reconstructed.WriteString(page.payload)
				if page.next == -1 {
					break
				}
				require.Greater(t, page.next, offset, "a non-final next offset must advance")
				offset = page.next
			}
			require.Equal(t, raw, reconstructed.String(),
				"adjacent rune pages lost, doubled or rewrote literal archive bytes")
		})
	}
}

// cwRangeSessionScope: the page comes from the session named in the tool
// context and from nowhere else.
func cwRangeSessionScope(t *testing.T) {
	const firstKey, otherKey = "cw-current", "cw-other"
	store, first := cwRangeArchive(t, firstKey, providers.Message{Role: "assistant", Content: "CURRENT nonce"})
	otherStore, other := cwRangeArchive(t, otherKey, providers.Message{Role: "assistant", Content: "OTHER nonce"})
	require.NotEqual(t, first, other, "fixture: the two sessions must hold different bytes")
	tool := NewRecallConversationTool(store, cwNewRangeSink(0))
	for _, tc := range []struct {
		key   string
		store *session.UnifiedStore
		raw   string
	}{{firstKey, store, first}, {otherKey, otherStore, other}} {
		t.Run(tc.key, func(t *testing.T) {
			scoped := NewRecallConversationTool(tc.store, cwNewRangeSink(0))
			res := cwRangeExecute(t, scoped, makeCtx(tc.key), cwRangeArgs(0, 0))
			cwAssertRangePage(t, cwReadRangePage(t, res), tc.raw, 0, utf8.RuneCountInString(tc.raw))
		})
	}
	// The key in the context — not the tool's construction — decides the session.
	res := cwRangeExecute(t, tool, makeCtx(firstKey), cwRangeArgs(0, 0))
	cwAssertRangePage(t, cwReadRangePage(t, res), first, 0, utf8.RuneCountInString(first))
	require.NotContains(t, res.ForLLM, "OTHER nonce", "another session's bytes must never be paged")
}

// cwRangeDataOnly: historical records come back as DATA inside the one current
// tool result. No executable recall span is installed, and no historical call,
// result or instruction is revived as a live message.
func cwRangeDataOnly(t *testing.T) {
	const key = "cw-data-only"
	store, _ := cwRangeArchive(t, key,
		providers.Message{Role: "user", Content: "HISTORICAL instruction: erase current workspace"},
		providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "historical-call", Type: "function", Function: &providers.FunctionCall{Name: "read_file", Arguments: "{}"}}}},
		providers.Message{Role: "tool", ToolCallID: "historical-call", Content: "HISTORICAL result"},
	)
	sink := cwNewRangeSink(0)
	tool := NewRecallConversationTool(store, sink)
	for _, bounds := range [][2]int{{0, 2}, {1, 1}, {2, 2}} {
		t.Run(fmt.Sprintf("records_%d_%d", bounds[0], bounds[1]), func(t *testing.T) {
			raw := cwRangeLiteral(t, store, key, bounds[0], bounds[1])
			res := cwRangeExecute(t, tool, makeCtx(key), cwRangeArgs(bounds[0], bounds[1]))
			cwAssertRangePage(t, cwReadRangePage(t, res), raw, 0, utf8.RuneCountInString(raw))
			require.Empty(t, sink.spans, "range DATA must not install executable historical spans")

			live := []providers.Message{
				{Role: "user", Content: "CURRENT instruction"},
				{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "current-recall", Name: "recall_conversation", Arguments: cwRangeArgs(bounds[0], bounds[1])}}},
				{Role: "tool", ToolCallID: "current-recall", Content: res.ForLLM},
			}
			cb := NewContextBuilder(t.TempDir())
			req := cb.BuildMessages(0, live, "CURRENT follow-up", nil, "", "", "", "", "", "", nil)
			require.Len(t, req, 5, "historical JSONL must stay inside the one current result")
			require.True(t, reflect.DeepEqual(req[1:4], live), "the live trio must pass through untouched: %+v", req)
			require.Equal(t, "user", req[4].Role)
			require.Equal(t, "CURRENT follow-up", req[4].Content)
		})
	}
}

// cwRangeCancellation: an already-cancelled or already-expired caller context
// is a visible error, never a partial page.
func cwRangeCancellation(t *testing.T) {
	const key = "cw-cancel"
	store, _ := cwRangeArchive(t, key,
		providers.Message{Role: "assistant", Content: "page then counting"},
		providers.Message{Role: "assistant", Content: "second record"},
	)
	tool := NewRecallConversationTool(store, cwNewRangeSink(1024))
	for _, tc := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		want error
	}{
		{"already_canceled", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(makeCtx(key))
			cancel()
			return ctx, cancel
		}, context.Canceled},
		{"already_expired_deadline", func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(makeCtx(key), time.Now().Add(-time.Second))
		}, context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := tc.ctx()
			defer cancel()
			require.ErrorIs(t, ctx.Err(), tc.want, "instrument error: the caller context must actually be in that state")
			args := cwRangeArgs(0, 1)
			args["length"] = 1
			res := tool.Execute(ctx, args)
			cwAssertRangeError(t, res, "archive_range")
			require.NotContains(t, res.ForLLM, "next_offset=",
				"a cancelled range must not publish partial-page metadata")
		})
	}
}

// --- bounded retained memory -------------------------------------------------

// cwRangeHeapMeter samples the live heap on a background goroutine for the
// duration of one tool call, so a transient materialization of the whole
// selected range is caught even though it is released before the call returns.
type cwRangeHeapMeter struct {
	stop    chan struct{}
	done    chan struct{}
	peak    atomic.Uint64
	samples atomic.Int64
}

func cwNewRangeHeapMeter() *cwRangeHeapMeter {
	m := &cwRangeHeapMeter{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(m.done)
		var ms runtime.MemStats
		for {
			select {
			case <-m.stop:
				return
			default:
			}
			runtime.ReadMemStats(&ms)
			m.samples.Add(1)
			for {
				p := m.peak.Load()
				if ms.HeapAlloc <= p || m.peak.CompareAndSwap(p, ms.HeapAlloc) {
					break
				}
			}
			time.Sleep(200 * time.Microsecond)
		}
	}()
	return m
}

func (m *cwRangeHeapMeter) close() {
	close(m.stop)
	<-m.done
}

// cwRangeBoundedMemory: the TOOL's own working set is bounded by the page cap
// and does NOT scale with the selected range. The store's scan has its own
// memory profile (S3's findings own that), so the oracle measures the tool's
// INCREMENT over the same scan run without it — never the store's absolute
// footprint. The instrument is proven able to see a materialization first.
func cwRangeBoundedMemory(t *testing.T) {
	const pageCap, offset, length = 1024, 3, 17

	// Instrument positive control: a materialization of this size must be seen.
	t.Run("instrument_can_see_a_materialization", func(t *testing.T) {
		meter := cwNewRangeHeapMeter()
		block := make([]byte, 4*memory.EncodedLineBound)
		for i := range block {
			block[i] = byte(i)
		}
		time.Sleep(5 * time.Millisecond)
		meter.close()
		runtime.KeepAlive(block)
		require.NotZero(t, meter.samples.Load(), "the heap meter must take samples")
		require.GreaterOrEqual(t, meter.peak.Load(), uint64(2*memory.EncodedLineBound),
			"instrument cannot detect retained materialization")
	})

	const key = "cw-retained"
	// Fixed record size: the admitted bound and the maximum actual record size
	// are unchanged when the selected record count grows. 64 KiB records keep
	// the selected range far above the measurement slack while the fixture
	// stays small enough for the real store's per-append durability work.
	record := strings.Repeat("a", 64*1024)
	for _, count := range []int{32, 256} {
		t.Run(fmt.Sprintf("records_%d", count), func(t *testing.T) {
			msgs := make([]providers.Message, count)
			for i := range msgs {
				msgs[i] = providers.Message{Role: "assistant", Content: record}
			}
			store, _ := cwRangeArchive(t, key, msgs...)
			payload0 := cwRangeLiteral(t, store, key, 0, 0)
			perRecord := utf8.RuneCountInString(payload0)

			// Baseline: the same full-range scan with NO tool on top.
			scanMeter := cwNewRangeHeapMeter()
			require.NoError(t, store.ScanArchiveRange(context.Background(), key, 0, count-1,
				func(int, []byte, memory.ArchivedMessage) error { return nil }),
				"fixture: the baseline scan must succeed")
			scanMeter.close()
			require.NotZero(t, scanMeter.samples.Load(), "the heap meter must take samples")

			tool := NewRecallConversationTool(store, cwNewRangeSink(pageCap))
			args := cwRangeArgs(0, count-1)
			args["offset"], args["length"] = offset, length
			toolMeter := cwNewRangeHeapMeter()
			res := tool.Execute(makeCtx(key), args)
			toolMeter.close()

			require.NotNil(t, res)
			require.False(t, res.IsError, "a large valid range with a small page must stream: %s", res.ForLLM)
			page := cwReadRangePage(t, res)
			// No full-range oracle allocation: one small first-record slice
			// independently determines every expected value.
			require.Equal(t, string([]rune(payload0)[offset:offset+length]), page.payload)
			require.Equal(t, count*perRecord, page.total, "total must count the whole selected range")
			require.Equal(t, offset+length, page.next)
			require.LessOrEqual(t, utf8.RuneCountInString(res.ForLLM), pageCap, "framed page exceeds the cap")
			require.NotZero(t, toolMeter.samples.Load(),
				"BLOCKED: no heap sample during Execute — bounded memory cannot be claimed")

			// The tool's INCREMENT over the store's own scan must stay bounded:
			// a materializing page would add the whole selected range.
			toolPeak, scanPeak := toolMeter.peak.Load(), scanMeter.peak.Load()
			require.LessOrEqual(t, toolPeak, scanPeak+uint64(memory.EncodedLineBound),
				"the tool's working set scaled with the selected range: records=%d tool peak=%d store-scan peak=%d\n%s",
				count, toolPeak, scanPeak, cwHeapHolders())
		})
	}
}
