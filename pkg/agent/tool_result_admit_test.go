// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// tool_result_admit_test.go — ADR-066 D4 choke point (spec tests 7–11,
// B-11, B-11b, B-11c, B-12, B-12b, B-13, B-16, B-16b; datasets DS-1 #1–#16).
package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// largeBudgetPolicy is the shipped caps with a budget big enough that the
// half-budget clamp never binds (W = 1,048,576-class window).
func largeBudgetPolicy() resultCapPolicy {
	return capPolicyFor(config.DefaultContextSettings(), 400_000)
}

// noMark is a mark source for tests that only care about sizes.
func noMark(string) string { return "[mark]" }

func runes(s string) int { return utf8.RuneCountInString(s) }

// assertCappedForm checks the D4 over-cap shape: total ≤ cap, the mark is
// inside, head and tail are taken from the original, no rune is split.
func assertCappedForm(t *testing.T, original, window, mark string, capChars int) {
	t.Helper()
	require.True(t, utf8.ValidString(window), "no rune may be split (E5)")
	assert.LessOrEqual(t, runes(window), capChars, "window form must not exceed the cap")
	assert.Contains(t, window, mark, "the mark is part of the window form and counts toward the cap")
	headEnd := strings.Index(window, "\n"+mark)
	require.Greater(t, headEnd, 0, "head precedes the mark")
	head := window[:headEnd]
	tail := window[headEnd+len("\n"+mark+"\n"):]
	assert.True(t, strings.HasPrefix(original, head), "head is the original's prefix")
	assert.True(t, strings.HasSuffix(original, tail), "tail is the original's suffix")
	// 50/50: head and tail within one rune of each other.
	assert.InDelta(t, runes(head), runes(tail), 1, "head-and-tail split is 50/50")
}

// Test 7 — B-11 surface table (DS-1 #1–#9). The surface decides the
// configured cap; IsError folds MCP failures onto the builtin-failure cap;
// denied/skipped/delegate/attachment have no exemption.
func TestChokePoint_PerSurfaceCap(t *testing.T) {
	policy := largeBudgetPolicy()
	cases := []struct {
		name     string
		tool     string
		isError  bool
		size     int
		wantCap  int
		wantCapd bool
	}{
		{"mcp success at cap", "mcp_srv_search", false, 62_500, 62_500, false},
		{"mcp success cap+1", "mcp_srv_search", false, 62_501, 62_500, true},
		{"mcp success incident", "mcp_srv_search", false, 1_178_522, 62_500, true},
		{"builtin success at cap", "read_file", false, 64_000, 64_000, false},
		{"builtin success cap+1", "read_file", false, 64_001, 64_000, true},
		{"builtin success 200k", "read_file", false, 200_000, 64_000, true},
		{"builtin failure at cap", "bash", true, 10_000, 10_000, false},
		{"builtin failure cap+1", "bash", true, 10_001, 10_000, true},
		{"builtin failure 50k", "bash", true, 50_000, 10_000, true},
		{"mcp isError 50k", "mcp_srv_search", true, 50_000, 10_000, true},
		{"delegate report 200k", "delegate", false, 200_000, 64_000, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			surface := toolResultSurfaceFor(tc.tool, tc.isError)
			assert.Equal(t, tc.wantCap, policy.effectiveCap(surface, 1), "configured cap for %s", surface)
			content := strings.Repeat("x", tc.size)
			window, capped := projectToolResult(content, policy.effectiveCap(surface, 1), noMark)
			assert.Equal(t, tc.wantCapd, capped)
			if !tc.wantCapd {
				assert.Equal(t, content, window, "at or under the cap the result is unmodified (E1)")
				return
			}
			assertCappedForm(t, content, window, "[mark]", tc.wantCap)
		})
	}

	t.Run("denied and skipped are the failure surface (DS-1 #7)", func(t *testing.T) {
		assert.Equal(t, surfaceBuiltinFailure, toolResultSurfaceFor("bash", true))
		assert.Equal(t, 10_000, policy.effectiveCap(surfaceBuiltinFailure, 1))
	})
	t.Run("hydrated attachment is the builtin-success surface (DS-1 #9)", func(t *testing.T) {
		assert.Equal(t, 64_000, policy.effectiveCap(surfaceBuiltinSuccess, 1))
	})
	t.Run("4-byte runes at the cut are never split (DS-1 #10)", func(t *testing.T) {
		content := strings.Repeat("😀", 62_501)
		window, capped := projectToolResult(content, 62_500, noMark)
		require.True(t, capped)
		assertCappedForm(t, content, window, "[mark]", 62_500)
	})
	t.Run("unset caps fall back to the shipped defaults, never to zero", func(t *testing.T) {
		p := capPolicyFor(config.ContextSettings{}, 400_000)
		assert.Equal(t, 62_500, p.effectiveCap(surfaceMCP, 1))
		assert.Equal(t, 64_000, p.effectiveCap(surfaceBuiltinSuccess, 1))
		assert.Equal(t, 10_000, p.effectiveCap(surfaceBuiltinFailure, 1))
	})
}

// Test 8 — B-11b / B-11c (DS-1 #14, #15): effective_cap =
// min(configured, floor(0.5 × B × 2.5)); N parallel calls that would not
// fit split it /N.
func TestChokePoint_ClampToHalfBudget(t *testing.T) {
	policy := capPolicyFor(config.DefaultContextSettings(), 3_000)

	t.Run("single result clamps to half the budget", func(t *testing.T) {
		assert.Equal(t, 3_750, policy.effectiveCap(surfaceBuiltinSuccess, 1))
		assert.Equal(t, 3_750, policy.effectiveCap(surfaceMCP, 1))
		assert.Equal(t, 3_750, policy.effectiveCap(surfaceBuiltinFailure, 1), "clamp applies to every surface; failure cap 10,000 > 3,750")
		window, capped := projectToolResult(strings.Repeat("y", 64_000), policy.effectiveCap(surfaceBuiltinSuccess, 1), noMark)
		require.True(t, capped)
		assert.LessOrEqual(t, runes(window), 3_750)
	})
	t.Run("three parallel calls split the effective cap", func(t *testing.T) {
		assert.Equal(t, 1_250, policy.effectiveCap(surfaceBuiltinSuccess, 3))
		// 3 × 1,250 chars ≈ 1,500 tokens — the three together fit in B = 3,000.
		assert.LessOrEqual(t, 3*1_250*2/5, 3_000)
	})
	t.Run("parallel calls that already fit are not split", func(t *testing.T) {
		// 2 × 3,750 × 0.4 = 3,000, not > B → no split.
		assert.Equal(t, 3_750, policy.effectiveCap(surfaceBuiltinSuccess, 2))
	})
	t.Run("large budget: configured cap wins, parallel never binds", func(t *testing.T) {
		p := largeBudgetPolicy()
		assert.Equal(t, 64_000, p.effectiveCap(surfaceBuiltinSuccess, 1))
		assert.Equal(t, 64_000, p.effectiveCap(surfaceBuiltinSuccess, 3))
	})
	t.Run("unknown budget (exempt provider) means no clamp", func(t *testing.T) {
		p := capPolicyFor(config.DefaultContextSettings(), 0)
		assert.Equal(t, 64_000, p.effectiveCap(surfaceBuiltinSuccess, 3))
	})
	t.Run("the mark itself counts toward the cap", func(t *testing.T) {
		mark := strings.Repeat("M", 500)
		window, capped := projectToolResult(strings.Repeat("z", 10_000), 1_250, func(string) string { return mark })
		require.True(t, capped)
		assert.LessOrEqual(t, runes(window), 1_250)
		assert.Contains(t, window, mark)
	})
}

// Test 9 — B-12b (DS-1 #16): 8,000,000 newline bytes encode to 16 MB of
// "\n" escapes; the archived line must be cut to ≤ 0.8 × maxLineSize and
// the session must still be readable.
func TestChokePoint_EncodedLineBound(t *testing.T) {
	content := strings.Repeat("\n", 8_000_000)
	msg := providers.Message{Role: "tool", ToolCallID: "call_big", Content: content}

	bounded, origRunes, cut := boundArchivedMessage(msg)
	require.True(t, cut, "16 MB encoded line must be cut")
	assert.Equal(t, 8_000_000, origRunes)
	encoded, err := json.Marshal(memory.ArchivedMessage{Message: bounded, TS: 1_700_000_000_000})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(encoded), memory.EncodedLineBound, "encoded line ≤ 0.8 × maxLineSize")
	assert.Equal(t, 8_388_608, memory.EncodedLineBound)
	assert.True(t, utf8.ValidString(bounded.Content))
	assert.Equal(t, "call_big", bounded.ToolCallID, "only the content is cut")

	t.Run("a line under the bound is untouched", func(t *testing.T) {
		small := providers.Message{Role: "tool", ToolCallID: "c", Content: strings.Repeat("a", 1_000_000)}
		got, _, cut := boundArchivedMessage(small)
		assert.False(t, cut)
		assert.Equal(t, small, got)
	})

	t.Run("the bounded line is what reaches the archive and GetHistory reads it back", func(t *testing.T) {
		al, ts, store := newChokePointTurn(t, 400_000)
		seedAssistantCall(t, store, ts.sessionKey, "call_big", "bash", 1)
		admitted := al.admitToolResult(ts, toolResultAdmission{
			Tool: "bash", ToolCallID: "call_big", Content: content, ParallelN: 1,
		})
		archive, err := store.ReadArchive(context.Background(), ts.sessionKey)
		require.NoError(t, err)
		last := archive[len(archive)-1]
		enc, _ := json.Marshal(last)
		assert.LessOrEqual(t, len(enc), memory.EncodedLineBound)
		assert.Equal(t, len(archive)-1, admitted.ArchiveLine)
		hist := store.GetHistory(ts.sessionKey)
		require.NotEmpty(t, hist, "GetHistory must still read the session")
		assert.Equal(t, last.Content, hist[len(hist)-1].Content)
		assert.True(t, admitted.Capped)
		assert.LessOrEqual(t, runes(admitted.Message.Content), 64_000, "window form ≤ the builtin-success cap")
	})
}

// Test 10 — B-16 (DS-1 #12, #13): the sensitive-data filter runs on the
// FULL content before the cut, so a secret straddling the head cut or the
// tail cut is redacted in both the archive and the window — no fragment.
func TestChokePoint_FilterThenCap_AtRealCuts(t *testing.T) {
	const secret = "SECRET-TOKEN-abcdef0123456789-XYZ"
	al, ts, store := newChokePointTurn(t, 400_000)
	al.GetConfig().Tools.FilterSensitiveData = true // "Given the sensitive-data filter is on" (US-3.AC10)
	al.GetConfig().RegisterSensitiveValues([]string{secret})
	seedAssistantCall(t, store, ts.sessionKey, "call_s", "read_file", 1)

	policy := capPolicyFor(al.GetConfig().Context, agentContextBudget(ts.agent))
	capChars := policy.effectiveCap(surfaceBuiltinSuccess, 1)
	require.Equal(t, 64_000, capChars)

	// Build a 100,000-char body with the secret straddling each cut. The head
	// cut lands around (cap − markLen)/2 ≈ 31,800; the tail cut mirrors it
	// from the end. Planting the secret across a ±200 band around each
	// guarantees it straddles whichever exact position the mark length yields.
	body := []byte(strings.Repeat("a", 100_000))
	plant := func(at int) { copy(body[at:], secret) }
	for at := 31_600; at < 32_200; at += len(secret) + 5 {
		plant(at)
	}
	for at := 100_000 - 32_200; at < 100_000-31_600; at += len(secret) + 5 {
		plant(at)
	}
	content := string(body)
	require.Contains(t, content, secret)

	admitted := al.admitToolResult(ts, toolResultAdmission{
		Tool: "read_file", ToolCallID: "call_s", Content: content, ParallelN: 1,
	})
	require.True(t, admitted.Capped)
	assert.NotContains(t, admitted.Message.Content, secret, "window form must carry no secret")
	assert.Contains(t, admitted.Message.Content, "[FILTERED]")
	// No fragment: a cut inside the secret would leave a prefix/suffix of it.
	for _, frag := range []string{"SECRET-TOKEN-abc", "0123456789-XYZ"} {
		assert.NotContains(t, admitted.Message.Content, frag, "no secret fragment at a cut")
	}
	archive, err := store.ReadArchive(context.Background(), ts.sessionKey)
	require.NoError(t, err)
	archived := archive[len(archive)-1].Content
	assert.NotContains(t, archived, secret, "archive holds the FILTERED full content (FR-013)")
	assert.Contains(t, archived, "[FILTERED]")
	assert.Equal(t, runes(strings.ReplaceAll(content, secret, "[FILTERED]")), runes(archived), "archive is the full filtered content, not the capped form")
}

// Test 11 — FR-009: every role:"tool" producer routes through the choke
// point. Enforced by grep: the ONLY non-test files in pkg/agent allowed to
// construct a `Role: "tool"` message literal are the choke point itself and
// the exempt repair placeholder (bounded by construction). Every former
// producer file must call the choke point instead.
func TestChokePoint_ProducerListByGrep(t *testing.T) {
	literal := regexp.MustCompile(`Role:\s*"tool"`)
	allowed := map[string]bool{
		"tool_result_admit.go": true, // the choke point
		"repair.go":            true, // exempt: orphan-repair placeholder, bounded by construction
	}
	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	violations := []string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, readErr := os.ReadFile(filepath.Join(".", name))
		require.NoError(t, readErr)
		if n := len(literal.FindAll(src, -1)); n > 0 && !allowed[name] {
			violations = append(violations, name)
		}
	}
	assert.Empty(t, violations, "files constructing role:tool messages outside the choke point (FR-009)")

	// The former producers must call the choke point.
	loopSrc, err := os.ReadFile("loop.go")
	require.NoError(t, err)
	calls := strings.Count(string(loopSrc), "al.admitToolResult(ts,")
	assert.Equal(t, 10, calls, "loop.go: success path + seven denied sites + skipped site + the T066-15 argument-refusal site (FR-016) = 10 choke-point calls")

	for _, f := range []string{"attach_hydrate.go", "recall_conversation.go"} {
		src, readErr := os.ReadFile(f)
		require.NoError(t, readErr)
		assert.Contains(t, string(src), "projectToolResult(", "%s must route its tool messages through the choke point's cap", f)
	}
	repairSrc, err := os.ReadFile("repair.go")
	require.NoError(t, err)
	assert.Contains(t, strings.ToLower(string(repairSrc)), "exempt from the choke point", "repair.go must annotate its exemption")
}

// B-12 / DS-1 #3: the incident-size MCP result enters at ≤ 62,500 chars, the
// archive line holds the full content, meta records (id, line) → capped,
// and the projection re-derives the capped form byte-identical on reload.
func TestChokePoint_IncidentResult_FullInArchiveCappedOnReload(t *testing.T) {
	al, ts, store := newChokePointTurn(t, 400_000)
	seedAssistantCall(t, store, ts.sessionKey, "call_inc", "mcp_gmail_search_email", 1)
	content := strings.Repeat("incident ", 130_947) // 1,178,523 chars
	content = content[:1_178_522]

	admitted := al.admitToolResult(ts, toolResultAdmission{
		Tool: "mcp_gmail_search_email", ToolCallID: "call_inc", Content: content, ParallelN: 1,
	})
	require.True(t, admitted.Capped)
	assert.LessOrEqual(t, runes(admitted.Message.Content), 62_500)
	assert.Equal(t, "call_inc", admitted.Message.ToolCallID)
	assert.Equal(t, "tool", admitted.Message.Role)
	assert.Contains(t, admitted.Message.Content, `"content_state":"capped"`)
	assert.Contains(t, admitted.Message.Content, `"size_chars":1178522`)

	archive, err := store.ReadArchive(context.Background(), ts.sessionKey)
	require.NoError(t, err)
	require.Len(t, archive, 3, "user, assistant, tool")
	assert.Equal(t, content, archive[2].Content, "archive holds the full content")
	assert.Equal(t, 2, admitted.ArchiveLine)

	pm := store.Projection(ts.sessionKey)
	assert.Equal(t, memory.ProjectionCapped, pm.Entries[memory.ProjectionKey{ToolCallID: "call_inc", ArchiveLine: 2}])

	// Reload: the projection function applied to GetHistory yields the same bytes.
	history := store.GetHistory(ts.sessionKey)
	skip := len(archive) - len(history)
	projected := projectMessages(history, func(i int) int { return skip + i }, pm.Entries, projectionContext{
		policy:  capPolicyFor(al.GetConfig().Context, agentContextBudget(ts.agent)),
		archive: archive,
	})
	assert.Equal(t, admitted.Message.Content, projected[2].Content, "reload renders the capped form byte-identical (B-12)")
	assert.Equal(t, content, history[2].Content, "projection never mutates its input")
}

// B-13 (DS-1 #11): over the warn threshold but under the cap → unmodified,
// one WARN and tool_result_large_total increments.
func TestChokePoint_WarnThresholdObserveOnly(t *testing.T) {
	al, ts, store := newChokePointTurn(t, 400_000)
	seedAssistantCall(t, store, ts.sessionKey, "call_w", "read_file", 1)
	before := ToolResultLargeTotal()
	content := strings.Repeat("w", 25_001)
	admitted := al.admitToolResult(ts, toolResultAdmission{Tool: "read_file", ToolCallID: "call_w", Content: content, ParallelN: 1})
	assert.False(t, admitted.Capped)
	assert.Equal(t, content, admitted.Message.Content)
	assert.Equal(t, before+1, ToolResultLargeTotal())
	pm := store.Projection(ts.sessionKey)
	assert.Empty(t, pm.Entries, "no projection state for an unmodified result")

	// Exactly at the threshold: no increment.
	seedAssistantCall(t, store, ts.sessionKey, "call_w2", "read_file", 1)
	al.admitToolResult(ts, toolResultAdmission{Tool: "read_file", ToolCallID: "call_w2", Content: strings.Repeat("w", 25_000), ParallelN: 1})
	assert.Equal(t, before+1, ToolResultLargeTotal())
}

// B-16b: settings are read per call — a cap lowered mid-turn applies to the
// next result.
func TestChokePoint_LiveSettingsPerCall(t *testing.T) {
	al, ts, store := newChokePointTurn(t, 400_000)
	seedAssistantCall(t, store, ts.sessionKey, "c1", "read_file", 1)
	first := al.admitToolResult(ts, toolResultAdmission{Tool: "read_file", ToolCallID: "c1", Content: strings.Repeat("a", 30_000), ParallelN: 1})
	assert.False(t, first.Capped)

	al.GetConfig().Context.BuiltinSuccessCap = 20_000
	seedAssistantCall(t, store, ts.sessionKey, "c2", "read_file", 1)
	second := al.admitToolResult(ts, toolResultAdmission{Tool: "read_file", ToolCallID: "c2", Content: strings.Repeat("a", 30_000), ParallelN: 1})
	assert.True(t, second.Capped, "the lowered cap applies to the next result")
	assert.LessOrEqual(t, runes(second.Message.Content), 20_000)
}

// --- helpers -------------------------------------------------------------

// newChokePointTurn builds a loop, a JSONL-backed session store and a
// turnState bound to it, with a context window that yields budget ≈ the
// requested value (MaxTokens and pinned overhead subtracted).
func newChokePointTurn(t *testing.T, window int) (*AgentLoop, *turnState, session.SessionStore) {
	t.Helper()
	al, cfg, _, _, cleanup := newTestAgentLoop(t)
	t.Cleanup(cleanup)
	cfg.Context = config.DefaultContextSettings()
	store := session.NewSessionManager(t.TempDir())
	agent := &AgentInstance{
		ID:            "choke-agent",
		Name:          "Choke",
		Sessions:      store,
		ContextWindow: window,
		MaxTokens:     1_000,
	}
	ts := &turnState{
		agent:      agent,
		agentID:    agent.ID,
		turnID:     "turn-choke",
		sessionKey: "agent:choke-agent:session:s1",
		opts:       processOptions{SessionKey: "agent:choke-agent:session:s1"},
	}
	return al, ts, store
}

// seedAssistantCall writes the user + assistant(tool call) lines a tool
// result always follows, so the archive has the owning call for the mark
// and the projection lookup.
func seedAssistantCall(t *testing.T, store session.SessionStore, key, callID, tool string, n int) {
	t.Helper()
	store.AddMessage(key, "user", "go")
	calls := make([]providers.ToolCall, 0, n)
	for i := 0; i < n; i++ {
		id := callID
		if i > 0 {
			id = callID + "_" + string(rune('a'+i))
		}
		calls = append(calls, providers.ToolCall{ID: id, Type: "function", Name: tool, Function: &providers.FunctionCall{Name: tool, Arguments: "{}"}})
	}
	store.AddFullMessage(key, providers.Message{Role: "assistant", ToolCalls: calls})
}
