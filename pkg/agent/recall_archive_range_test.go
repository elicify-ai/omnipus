// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// RED plan -- ADR-066 MAJ-CW-003 + MIN-001; overflow spec B-58/test 63.
// Oracles are the literal fixture bytes, inclusive zero-based indexes, rune
// arithmetic, structured errors, unchanged live call identities and actual Skip.
// The tool, JSONL store, decoder and assembly stay real. Reachable heap and the
// external span sink are observed. No production symbol is changed. Post-page
// cancellation/deadline read-progress proof is loudly BLOCKED: the current raw
// reader has no observable checkpoint. Do not substitute context-call counts.
// Cases: schema; range/offset/length boundaries; all four conflicting modes;
// raw spelling; cap changes; Unicode chunk/newline boundaries; early/late faults;
// retained-memory growth; cancellation/deadline; breadcrumb identities; DATA-only
// delivery. Error prose and the new framing's exact wording are NOT specified:
// assert field/cause identity and exact metadata, not invented whole sentences.
// CHECK mutations (not performed in RED): materialize all selected records;
// count bytes instead of runes; omit inclusive last line/newline; swallow a late
// decode fault; stop checking context after page collection; use len subtraction
// for Skip; install historical calls as an executable recall span.
// Deliberate gaps: whole-turn slide/reload/abort and pressure-projection delivery
// are the sibling runtime lane's work. GREEN, mutations and complete CI are
// deferred to a DIFFERENT qa-lead CHECK instance.

// One exact-name, narrowly scoped invocation runs this sub-unit, not pkg/agent's
// full suite. No t.Parallel: the retained-memory observer needs a quiet process.
func TestRecallConversation_ArchiveRange(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{"schema", cwRangeSchema},
		{"inclusive_verbatim_lines", cwRangeInclusiveVerbatim},
		{"rune_offset_boundaries", cwRangeOffsetBoundaries},
		{"validation", cwRangeValidation},
		{"mutually_exclusive_modes", cwRangeExclusiveModes},
		{"out_of_range_is_error", cwRangeOutOfRange},
		{"cap_and_live_settings", cwRangeCapAndSettings},
		{"utf8_chunk_and_record_boundaries", cwRangeUTF8Boundaries},
		{"adjacent_pages_reconstruct_literal_jsonl", cwRangeReconstructPages},
		{"early_and_late_decode_errors", cwRangeDecodeErrors},
		{"early_and_late_read_errors", cwRangeReadErrors},
		{"current_session_only", cwRangeSessionScope},
		{"quoted_data_not_historical_calls", cwRangeDataOnly},
		{"memory_observer_control", cwRangeMemoryObserverControl},
		{"range_independent_retained_memory", cwRangeBoundedMemory},
		{"cooperative_cancellation_and_deadline", cwRangeCancellation},
		{"breadcrumbs_use_actual_skip", cwRangeBreadcrumbs},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

// This is a sink at the tool's external boundary, not a mock of the recall unit.
// Keeping it separate from stubSpanSetter avoids changing global drop counters.
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
	s.spans[key] = span
}
func (s *cwRangeSink) dropRecallSpan(key, _ string) {
	s.drops++
	delete(s.spans, key)
}

// Raw files deliberately include spelling that decode-and-reencode loses.
// Fixture writing is incremental; large tests never construct a whole range.
func cwRangeStore(t *testing.T, key string, lines ...string) (*memory.JSONLStore, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := memory.NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("fixture store: %v", err)
	}
	path := filepath.Join(dir, key+".jsonl") // fixture keys need no sanitization
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("fixture archive: %v", err)
	}
	for _, line := range lines {
		if _, err := f.WriteString(line); err != nil {
			_ = f.Close()
			t.Fatalf("write fixture record: %v", err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close fixture archive: %v", err)
	}
	return store, path
}

func cwRangeArgs(from, to int) map[string]any {
	return map[string]any{"archive_range": map[string]any{"from": from, "to": to}}
}

func cwRangeExecute(t *testing.T, tool *RecallConversationTool, ctx context.Context, args map[string]any) *tools.ToolResult {
	t.Helper()
	res := tool.Execute(ctx, args)
	if res == nil {
		t.Fatal("MAJ-CW-003: Execute returned nil instead of a structured result")
	}
	if res.IsError {
		t.Fatalf("MAJ-CW-003: valid archive_range must return a literal page, got error: %s", res.ForLLM)
	}
	return res
}

// The existing page vocabulary uses total/next_offset. Accept total_runes too;
// no new header sentence is prescribed by the amendment. Numeric values and the
// entire payload are asserted independently against the raw fixture.
var (
	cwRangeTotalField = regexp.MustCompile(`\btotal(?:_runes)?\s*[=:]\s*([0-9]+)\b`)
	cwRangeNextField  = regexp.MustCompile(`\bnext_offset\s*[=:]\s*(end|[0-9]+)\b`)
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
	if !ok {
		t.Fatalf("MAJ-CW-003: page must separate deterministic framing from verbatim JSONL: %q", res.ForLLM)
	}
	total := cwRangeTotalField.FindStringSubmatch(header)
	next := cwRangeNextField.FindStringSubmatch(header)
	if len(total) != 2 || len(next) != 2 {
		t.Fatalf("MAJ-CW-003: framing must state total runes and next offset: %q", header)
	}
	n, err := strconv.Atoi(total[1])
	if err != nil {
		t.Fatalf("invalid total-rune metadata: %v", err)
	}
	p := cwRangePage{header: header, payload: payload, total: n, next: -1}
	if next[1] != "end" {
		p.next, err = strconv.Atoi(next[1])
		if err != nil {
			t.Fatalf("invalid next-offset metadata: %v", err)
		}
	}
	if !utf8.ValidString(res.ForLLM) {
		t.Fatal("MIN-001: response splits a UTF-8 encoding")
	}
	return p
}

func cwAssertRangePage(t *testing.T, page cwRangePage, raw string, offset, length int) {
	t.Helper()
	runes := []rune(raw) // small test oracle only; never used for the large range
	end := min(offset+length, len(runes))
	start := min(offset, len(runes))
	want := string(runes[start:end])
	if page.payload != want {
		t.Fatalf("MIN-001: literal rune slice [%d:%d] = %q, got %q", start, end, want, page.payload)
	}
	if page.total != len(runes) {
		t.Fatalf("MIN-001: total = %d, want %d runes (including literal JSONL newlines)", page.total, len(runes))
	}
	wantNext := -1
	if end < len(runes) {
		wantNext = end
	}
	if page.next != wantNext {
		t.Fatalf("MIN-001: next offset = %d, want %d (-1 means end)", page.next, wantNext)
	}
}

func cwAssertRangeError(t *testing.T, res *tools.ToolResult, fields ...string) {
	t.Helper()
	if res == nil || !res.IsError {
		t.Fatalf("MAJ-CW-003: want structured IsError=true, got %+v", res)
	}
	for _, field := range fields {
		if !strings.Contains(res.ForLLM, field) {
			t.Errorf("MAJ-CW-003: visible error must identify %q, got %q", field, res.ForLLM)
		}
	}
	if res.ForLLM == "" || res.Async {
		t.Errorf("MAJ-CW-003: error must be visible and synchronous, got %+v", res)
	}
}

func cwRangeSchema(t *testing.T) {
	tool := NewRecallConversationTool(nil, cwNewRangeSink(0))
	props, ok := tool.Parameters()["properties"].(map[string]any)
	if !ok {
		t.Fatal("recall_conversation schema must have object properties")
	}
	rng, ok := props["archive_range"].(map[string]any)
	if !ok {
		t.Fatal("BLOCKED: archive_range mode not implemented — required by ADR-066 MAJ-CW-003 + MIN-001 / B-58")
	}
	if rng["type"] != "object" {
		t.Fatalf("archive_range schema type = %v, want object", rng["type"])
	}
	bounds, ok := rng["properties"].(map[string]any)
	if !ok {
		t.Fatal("archive_range schema must expose from/to")
	}
	for _, name := range []string{"from", "to"} {
		field, ok := bounds[name].(map[string]any)
		if !ok || field["type"] != "integer" || fmt.Sprint(field["minimum"]) != "0" {
			t.Errorf("archive_range.%s must be an integer with minimum 0: %v", name, field)
		}
	}
	// Schema representation may use []any or []string; decode only its list,
	// never execute the tool to derive required names.
	encoded, err := json.Marshal(rng["required"])
	if err != nil {
		t.Fatalf("encode required schema: %v", err)
	}
	var required []string
	if err := json.Unmarshal(encoded, &required); err != nil {
		t.Fatalf("archive_range required names: %v", err)
	}
	if !reflect.DeepEqual(required, []string{"from", "to"}) && !reflect.DeepEqual(required, []string{"to", "from"}) {
		t.Errorf("archive_range required = %v, want exactly from and to", required)
	}
	for _, tc := range []struct {
		name string
		min  int
	}{{"offset", 0}, {"length", 1}} {
		field, ok := props[tc.name].(map[string]any)
		if !ok || field["type"] != "integer" || fmt.Sprint(field["minimum"]) != strconv.Itoa(tc.min) {
			t.Errorf("%s schema must be integer, minimum %d: %v", tc.name, tc.min, field)
		}
	}
}

func cwRangeInclusiveVerbatim(t *testing.T) {
	const key = "cw-verbatim"
	lines := []string{
		" { \"content\" : \"zero \\u00e9\", \"role\" : \"user\", \"ts\" : 17 }\n",
		"{\"role\":\"assistant\",\"content\":\"one 中 é\", \"ts\":18}\n",
		"{ \"role\" : \"tool\", \"tool_call_id\" : \"orphan-history\", \"content\" : \"two 𝄞\\nquoted\" }\n",
	}
	store, _ := cwRangeStore(t, key, lines...)
	tool := NewRecallConversationTool(store, cwNewRangeSink(0))
	for _, bounds := range [][2]int{{0, 0}, {0, 1}, {1, 1}, {1, 2}, {2, 2}, {0, 2}} {
		t.Run(fmt.Sprintf("%d_through_%d", bounds[0], bounds[1]), func(t *testing.T) {
			raw := strings.Join(lines[bounds[0]:bounds[1]+1], "")
			res := cwRangeExecute(t, tool, makeCtx(key), cwRangeArgs(bounds[0], bounds[1]))
			cwAssertRangePage(t, cwReadRangePage(t, res), raw, 0, utf8.RuneCountInString(raw))
		})
	}
	// Integral JSON numbers arrive as float64, whereas Go callers also pass int.
	t.Run("integral_json_numbers", func(t *testing.T) {
		args := map[string]any{"archive_range": map[string]any{"from": float64(0), "to": float64(1)}}
		res := cwRangeExecute(t, tool, makeCtx(key), args)
		raw := lines[0] + lines[1]
		cwAssertRangePage(t, cwReadRangePage(t, res), raw, 0, utf8.RuneCountInString(raw))
	})
}

func cwRangeOffsetBoundaries(t *testing.T) {
	const key = "cw-offsets"
	const raw = "{\"role\":\"assistant\",\"content\":\"Aé中𝄞éZ\"}\n"
	store, _ := cwRangeStore(t, key, raw)
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

func cwRangeValidation(t *testing.T) {
	const key = "cw-invalid"
	store, _ := cwRangeStore(t, key, "{\"role\":\"user\",\"content\":\"zero\"}\n")
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

func cwRangeExclusiveModes(t *testing.T) {
	const key = "cw-exclusive"
	store, _ := cwRangeStore(t, key,
		"{\"role\":\"user\",\"content\":\"exclusive nonce\"}\n",
		"{\"role\":\"assistant\",\"tool_calls\":[{\"id\":\"past-call\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{}\"}}]}\n",
		"{\"role\":\"tool\",\"tool_call_id\":\"past-call\",\"content\":\"past result\"}\n")
	cases := []struct {
		name string
		key  string
		val  any
	}{
		{"query", "query", "exclusive nonce"},
		{"turn_range", "turn_range", "1-1"},
		{"time", "time", map[string]any{"from": 0}},
		{"tool_call_id", "tool_call_id", "past-call"},
		// A named second mode must not be silently discarded, even if empty.
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
			if len(sink.spans) != 0 || sink.drops != 0 {
				t.Errorf("ambiguous request must not mutate recall state: spans=%d drops=%d", len(sink.spans), sink.drops)
			}
		})
	}
}

func cwRangeOutOfRange(t *testing.T) {
	const key = "cw-outside"
	lines := []string{
		"{\"role\":\"assistant\",\"content\":\"zero\"}\n",
		"{\"role\":\"assistant\",\"content\":\"one\"}\n",
		"{\"role\":\"assistant\",\"content\":\"two\"}\n",
	}
	store, _ := cwRangeStore(t, key, lines...)
	tool := NewRecallConversationTool(store, cwNewRangeSink(0))
	for _, b := range [][2]int{{0, 3}, {2, 3}, {3, 3}, {4, 5}} {
		t.Run(fmt.Sprintf("%d_through_%d", b[0], b[1]), func(t *testing.T) {
			cwAssertRangeError(t, tool.Execute(makeCtx(key), cwRangeArgs(b[0], b[1])), "archive_range")
		})
	}
	t.Run("empty_archive", func(t *testing.T) {
		empty, _ := cwRangeStore(t, "cw-empty")
		emptyTool := NewRecallConversationTool(empty, cwNewRangeSink(0))
		cwAssertRangeError(t, emptyTool.Execute(makeCtx("cw-empty"), cwRangeArgs(0, 0)), "archive_range")
	})
}

func cwRangeCapAndSettings(t *testing.T) {
	const key = "cw-cap"
	// Far beyond any tested cap, but below D10's per-record bound.
	raw := "{\"role\":\"assistant\",\"content\":\"" + strings.Repeat("é中𝄞", 20_000) + "\"}\n"
	store, _ := cwRangeStore(t, key, raw)
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
			if whole := utf8.RuneCountInString(res.ForLLM); whole > capRunes {
				t.Fatalf("page plus deterministic framing = %d runes, cap = %d", whole, capRunes)
			}
			// The amendment does not prescribe framing prose or a numeric
			// worst-width reserve. Verify the effective upper bound and live
			// cap dependence without inventing a maximum padding allowance.
			if n == 0 {
				t.Fatal("nonempty range and sufficient result cap must return a nonempty page")
			}
			if previousLength != 0 && n <= previousLength {
				t.Fatalf("increasing the live cap must increase this non-final page: previous=%d current=%d", previousLength, n)
			}
			previousLength = n
			args := cwRangeArgs(0, 0)
			args["length"] = utf8.RuneCountInString(raw) + 1
			clamped := cwReadRangePage(t, cwRangeExecute(t, tool, makeCtx(key), args))
			if clamped != page {
				t.Fatalf("oversized length must clamp to the same deterministic default page: default=%+v clamped=%+v", page, clamped)
			}
			args["offset"], args["length"] = 7, 11
			cwAssertRangePage(t, cwReadRangePage(t, cwRangeExecute(t, tool, makeCtx(key), args)), raw, 7, 11)
		})
	}
}

func cwRangeUTF8Boundaries(t *testing.T) {
	// memory.JSONLStore's scanner starts with 64 KiB. Position the first byte
	// of a multibyte encoding at 64 KiB-1, at 64 KiB, and at 64 KiB+1. The
	// oracle never assumes which buffer size the new range reader chooses.
	const chunkBytes = 64 * 1024
	const prefix = "{\"role\":\"assistant\",\"content\":\""
	for _, lead := range []string{"é", "中", "𝄞"} {
		for _, bytePosition := range []int{chunkBytes - 1, chunkBytes, chunkBytes + 1} {
			t.Run(fmt.Sprintf("%d_byte_rune_at_byte_%d", len(lead), bytePosition), func(t *testing.T) {
				const key = "cw-utf8"
				pad := strings.Repeat("a", bytePosition-len(prefix))
				first := prefix + pad + lead + "éZ\"}\n"
				second := "{\"role\":\"assistant\",\"content\":\"next é中𝄞\"}\n"
				store, _ := cwRangeStore(t, key, first, second)
				tool := NewRecallConversationTool(store, cwNewRangeSink(0))
				raw := first + second
				// All bytes before lead are ASCII, so this bytePosition is
				// independently also lead's zero-based RUNE position.
				for _, tc := range []struct {
					name   string
					offset int
					length int
				}{
					{"multibyte_rune", bytePosition, 1},
					{"base_without_combining_accent", bytePosition + 1, 1},
					{"combining_accent_without_base", bytePosition + 2, 1},
					{"across_record_newline", utf8.RuneCountInString(first) - 2, 5},
				} {
					t.Run(tc.name, func(t *testing.T) {
						args := cwRangeArgs(0, 1)
						args["offset"], args["length"] = tc.offset, tc.length
						cwAssertRangePage(t, cwReadRangePage(t, cwRangeExecute(t, tool, makeCtx(key), args)), raw, tc.offset, tc.length)
					})
				}
			})
		}
	}
}

func cwRangeReconstructPages(t *testing.T) {
	const key = "cw-reconstruct"
	lines := []string{
		"{ \"role\" : \"assistant\", \"content\" : \"Aé中𝄞é\" }\n",
		"{\"content\":\"B\\nC\\u00e9\",\"role\":\"assistant\"}\n",
	}
	raw := strings.Join(lines, "")
	store, _ := cwRangeStore(t, key, lines...)
	tool := NewRecallConversationTool(store, cwNewRangeSink(0))
	for _, length := range []int{1, 2, 3, 4, 7} {
		t.Run(fmt.Sprintf("length_%d", length), func(t *testing.T) {
			var reconstructed strings.Builder
			total := utf8.RuneCountInString(raw)
			offset := 0
			for pageNumber := 0; ; pageNumber++ {
				if pageNumber > total { // every non-final page must advance ≥1 rune
					t.Fatal("next offset does not terminate after at most total-runes pages")
				}
				args := cwRangeArgs(0, 1)
				args["offset"], args["length"] = offset, length
				page := cwReadRangePage(t, cwRangeExecute(t, tool, makeCtx(key), args))
				cwAssertRangePage(t, page, raw, offset, length)
				reconstructed.WriteString(page.payload)
				if page.next == -1 {
					break
				}
				if page.next <= offset {
					t.Fatalf("non-final next offset %d does not advance from %d", page.next, offset)
				}
				offset = page.next
			}
			if reconstructed.String() != raw {
				t.Fatalf("adjacent rune pages lost/doubled/rewrote literal bytes: want %q got %q", raw, reconstructed.String())
			}
		})
	}
}

func cwRangeDecodeErrors(t *testing.T) {
	const good = "{\"role\":\"assistant\",\"content\":\"valid requested page\"}\n"
	const bad = "{\"role\":\"assistant\",\"content\":BROKEN}\n"
	var decoded memory.ArchivedMessage
	decodeErr := json.Unmarshal([]byte(strings.TrimSuffix(bad, "\n")), &decoded)
	if decodeErr == nil {
		t.Fatal("instrument failure: BROKEN must be an actual JSON decode error")
	}
	for _, badLine := range []int{0, 2} {
		t.Run(fmt.Sprintf("decode_failure_at_line_%d", badLine), func(t *testing.T) {
			const key = "cw-decode-error"
			lines := []string{good, good, good, good}
			lines[badLine] = bad
			store, _ := cwRangeStore(t, key, lines...)
			sink := cwNewRangeSink(0)
			tool := NewRecallConversationTool(store, sink)
			args := cwRangeArgs(0, 3)
			args["length"] = 1 // byte/rune zero is valid when badLine == 2
			res := tool.Execute(makeCtx(key), args)
			cwAssertRangeError(t, res, decodeErr.Error())
			if len(sink.spans) != 0 || strings.Contains(res.ForLLM, "next_offset=") {
				t.Errorf("MIN-001: decode failure must not publish a partial-success page/total: result=%q spans=%d", res.ForLLM, len(sink.spans))
			}
		})
	}
}

func cwRangeReadErrors(t *testing.T) {
	t.Run("unreadable_archive", func(t *testing.T) {
		const key = "cw-read-error"
		store, path := cwRangeStore(t, key)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		// A directory cannot be read as a JSONL file on any supported OS.
		// Confirm the OS-side error instrument before trusting the tool error.
		if _, err := os.ReadFile(path); err == nil {
			t.Fatal("instrument failure: archive-path directory unexpectedly readable as a file")
		}
		tool := NewRecallConversationTool(store, cwNewRangeSink(0))
		res := tool.Execute(makeCtx(key), cwRangeArgs(0, 0))
		cwAssertRangeError(t, res, "archive")
		cause := regexp.MustCompile(`(?i)(directory|not (?:a )?regular file)`)
		if !cause.MatchString(res.ForLLM) {
			t.Errorf("visible error must identify the actual directory/read cause, not a missing-mode or range error: %q", res.ForLLM)
		}
	})
	t.Run("oversized_record_after_valid_page", func(t *testing.T) {
		const key = "cw-late-read-error"
		const first = "{\"role\":\"assistant\",\"content\":\"valid first page\"}\n"
		store, path := cwRangeStore(t, key, first)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		// Incrementally create a record above the existing 10-MiB scanner
		// ceiling; the fixture itself must not allocate that whole record.
		if _, err := f.WriteString("{\"role\":\"assistant\",\"content\":\""); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		chunk := strings.Repeat("x", 1024)
		for i := 0; i <= 10*1024; i++ {
			if _, err := f.WriteString(chunk); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
		}
		if _, err := f.WriteString("\"}\n"); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		tool := NewRecallConversationTool(store, cwNewRangeSink(0))
		args := cwRangeArgs(0, 1)
		args["length"] = 1
		res := tool.Execute(makeCtx(key), args)
		cwAssertRangeError(t, res, "archive")
		// The spec requires a visible read/record-bound cause, not one exact
		// choice of scanner/decoder. Never accept a generic missing-mode error.
		cause := regexp.MustCompile(`(?i)(too (?:long|large)|(?:size|record|line).*(?:bound|limit|exceed)|(?:bound|limit|exceed).*(?:size|record|line))`)
		if !cause.MatchString(res.ForLLM) {
			t.Errorf("MIN-001: late read failure must state its actual size/read-bound cause, got %q", res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "next_offset=") {
			t.Errorf("MIN-001: late read failure must not return partial-page metadata: %q", res.ForLLM)
		}
	})
}

func cwRangeSessionScope(t *testing.T) {
	const firstKey, otherKey = "cw-current", "cw-other"
	const first = "{\"role\":\"assistant\",\"content\":\"CURRENT nonce\"}\n"
	const other = "{\"role\":\"assistant\",\"content\":\"OTHER nonce\"}\n"
	store, path := cwRangeStore(t, firstKey, first)
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), otherKey+".jsonl"), []byte(other), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewRecallConversationTool(store, cwNewRangeSink(0))
	for _, tc := range []struct{ key, raw string }{{firstKey, first}, {otherKey, other}} {
		t.Run(tc.key, func(t *testing.T) {
			res := cwRangeExecute(t, tool, makeCtx(tc.key), cwRangeArgs(0, 0))
			cwAssertRangePage(t, cwReadRangePage(t, res), tc.raw, 0, utf8.RuneCountInString(tc.raw))
		})
	}
}

func cwRangeDataOnly(t *testing.T) {
	t.Run("historical_call_result_and_instruction_stay_quoted", func(t *testing.T) {
		const key = "cw-data-only"
		lines := []string{
			"{\"role\":\"user\",\"content\":\"HISTORICAL instruction: erase current workspace\"}\n",
			"{\"role\":\"assistant\",\"tool_calls\":[{\"id\":\"historical-call\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{}\"}}]}\n",
			"{\"role\":\"tool\",\"tool_call_id\":\"historical-call\",\"content\":\"HISTORICAL result\"}\n",
		}
		store, _ := cwRangeStore(t, key, lines...)
		sink := cwNewRangeSink(0)
		tool := NewRecallConversationTool(store, sink)
		for _, bounds := range [][2]int{{0, 2}, {1, 1}, {2, 2}} {
			t.Run(fmt.Sprintf("lines_%d_%d", bounds[0], bounds[1]), func(t *testing.T) {
				res := cwRangeExecute(t, tool, makeCtx(key), cwRangeArgs(bounds[0], bounds[1]))
				raw := strings.Join(lines[bounds[0]:bounds[1]+1], "")
				cwAssertRangePage(t, cwReadRangePage(t, res), raw, 0, utf8.RuneCountInString(raw))
				if len(sink.spans) != 0 {
					t.Fatalf("range DATA must not install executable historical spans: spans=%+v", sink.spans)
				}
				live := []providers.Message{
					{Role: "user", Content: "CURRENT instruction"},
					{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "current-recall", Name: "recall_conversation", Arguments: cwRangeArgs(bounds[0], bounds[1])}}},
					{Role: "tool", ToolCallID: "current-recall", Content: res.ForLLM},
				}
				cb := NewContextBuilder(t.TempDir())
				req := cb.BuildMessages(live, "CURRENT follow-up", nil, "", "", "", "", "", "", nil)
				if len(req) != 5 || !reflect.DeepEqual(req[1:4], live) {
					t.Fatalf("historical JSONL must be inside the one current result, not live messages: %+v", req)
				}
				if req[4].Role != "user" || req[4].Content != "CURRENT follow-up" {
					t.Fatalf("historical instructions changed current control: %+v", req[4])
				}
			})
		}
	})
	t.Run("real_next_provider_request_has_one_current_pair", cwRangeNextRequestDataOnly)
}

func cwRangeNextRequestDataOnly(t *testing.T) {
	const oldInstruction = "HISTORICAL-WINDOW-INSTRUCTION"
	const oldResult = "HISTORICAL-WINDOW-RESULT"
	const oldCallID = "historical-window-call"
	turns := [][]providers.Message{
		{
			{Role: "user", Content: oldInstruction},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: oldCallID, Name: "read_file", Arguments: map[string]any{"path": "historical.txt"}}}},
			{Role: "tool", ToolCallID: oldCallID, Content: oldResult},
		},
		{{Role: "user", Content: "CURRENT window"}, {Role: "assistant", Content: "CURRENT answer"}},
	}
	provider := &recallInjectionProvider{first: cwRangeArgs(0, 2)}
	al, agent := recallInjectionFixture(t, provider, 200_000, 1000, turns)
	agent.Sessions.TruncateHistory(recallInjectionSessionKey, 2)
	if live := agent.Sessions.GetHistory(recallInjectionSessionKey); len(live) != 2 || live[0].Content != "CURRENT window" {
		t.Fatalf("fixture must actually evict the three historical records: %+v", live)
	}
	if _, err := al.processTaskDirect(context.Background(), agent.ID, "CURRENT request: recall archive lines 0 through 2 as data", recallInjectionSessionKey, "cw-data-only"); err != nil {
		t.Fatalf("real recall turn: %v", err)
	}
	req := provider.request(2)
	if req == nil {
		t.Fatalf("range page must reach the very next provider request; calls=%d", provider.calls())
	}
	content, ok := toolMessageContent(req, "call_recall_1")
	if !ok {
		t.Fatal("next request is missing current recall result call_recall_1")
	}
	for _, literal := range []string{oldInstruction, oldResult, oldCallID} {
		if !strings.Contains(content, literal) {
			t.Errorf("current range result must quote literal historical DATA %q; got %q", literal, content)
		}
	}
	currentCalls, currentResults := 0, 0
	for _, msg := range req {
		for _, call := range msg.ToolCalls {
			if call.ID == "call_recall_1" && call.Name == "recall_conversation" {
				currentCalls++
			} else {
				t.Errorf("historical range must not introduce executable calls: %+v", call)
			}
		}
		if msg.Role == "tool" {
			if msg.ToolCallID == "call_recall_1" {
				currentResults++
			} else {
				t.Errorf("historical range must not introduce orphan/historical results: %+v", msg)
			}
		}
		if msg.Role == "user" && strings.Contains(msg.Content, oldInstruction) {
			t.Errorf("quoted historical instruction revived as a live user message: %+v", msg)
		}
		if msg.Role != "system" && msg.ToolCallID != "call_recall_1" && strings.Contains(msg.Content, oldResult) {
			t.Errorf("range data must occur only in the current tool result: %+v", msg)
		}
	}
	if currentCalls != 1 || currentResults != 1 {
		t.Errorf("current recall pair must remain exactly once: calls=%d results=%d", currentCalls, currentResults)
	}
}
