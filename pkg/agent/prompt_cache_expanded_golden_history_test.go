package agent

// ARCH-02 / T1 — the joined history-cache golden proof (spec row 29, FR-019, SC-001).
//
// What the existing pack lacked (architect.md::ARCH-02, pr-test-analyzer.md::T1):
// TestHistoryHit_StaticOnlyFails never makes a provider call and invents
// `prefixTokens + 1`; TestSecondCall_CostsLessNotZero returns 8000 by request
// ordinal; captureCacheWire captures real bytes but always answers 1/1. No test
// joined real tools + all front instructions + history, the fake cache's
// matching decision, and the read returned to accounting.
//
// This file joins them. Two real assembled requests from the real turn loop go
// through the official Anthropic adapter (pkg/providers/anthropic) to one
// recording fake HTTP provider. The fake is a model of a prefix cache, not an
// ordinal script:
//
//   - Content identity is CANONICAL: cache_control marker bytes are stripped
//     from every segment before it enters a chain key or a stored entry, and
//     each message segment carries its ROLE. A provider's cache identity is a
//     content prefix; the markers only say where caching writes land, and the
//     history breakpoint legitimately moves as history grows. Keys with
//     marker bits make a reused history miss exactly when an old mark moves;
//     keys without role make two identical blocks under different roles
//     collide. Both are killed here (drilled directly in
//     TestExpandedGolden_IdentityIgnoresMarksAndRoles).
//   - WRITE: entries are created only where the request is correctly marked
//     (cache_control type "ephemeral"). The write reported back is only the
//     NEW portion beyond the longest matched prefix — one number for the
//     largest new marked end, never overlapping breakpoint prefixes summed,
//     and never a full read charged again as a write.
//   - READ: the longest stored entry whose canonical chain is a prefix of
//     THIS request. The request number is never consulted; a third
//     mismatched request would earn exactly what any mismatched request
//     earns — the static front only.
//   - The volatile tail is never stored, mirroring markCache's own Volatile
//     exclusion (pkg/providers/anthropic/cache_marks.go::markCache).
//
// Token model (documented, deterministic, not an unlabeled character count):
// the fake measures every chain with the product's own labelled estimator
// "omnipus-json-runes/2.5" — UTF-8 runes × 2 / 5 over the canonical segment
// JSON — exactly the formula recorded by
// pkg/agent/prompt_cache_state.go::RecordTokenSizes.
//
// The SC-001 threshold covers the FULL pre-history prefix: tools plus front
// instructions. The boundary is set after tools AND system blocks, and the
// golden independently recomputes it from the decoded body (never from the
// shared helper's own bookkeeping) plus a raises-bound check: adding a known
// front instruction must raise the bound. The read returned by the adapter is
// parsed by the real response path (messageUsage -> promptcache.ParseUsage)
// and recorded by a pass-through provider wrapper, so the proof is checked on
// the normalized read the loop actually received. Cost-lower-not-zero stays
// with the existing body test; this golden derives only read/write usage.
//
// FR-021 is respected: the D22 live threshold stays at "second-call read > 0";
// this stronger history-covering comparison exists only in this fake suite.
//
// Baseline expectation is Unknown until the parent runs; the production
// mutation plan that proves this instrument can fail is in handoff.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	anthropicprovider "github.com/elicify-ai/omnipus/pkg/providers/anthropic"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// expandedGoldenTokens is the fake's documented token estimator: the product's
// own labelled estimate "omnipus-json-runes/2.5" (runes × 2 / 5 over canonical
// JSON), matching pkg/agent/prompt_cache_state.go::RecordTokenSizes. It is a
// named, deterministic token estimate — never an unlabeled character count.
func expandedGoldenTokens(data []byte) int {
	return utf8.RuneCount(data) * 2 / 5
}

const expandedGoldenChainSep = "\x00"

// expandedGoldenSegment is one canonical wire segment. Content identity
// excludes cache_control (markers describe write positions, not content) and
// carries the message role, so identical block bytes under different roles are
// never the same provider prefix. Kind pins the section boundary.
type expandedGoldenSegment struct {
	Kind  string          `json:"kind"`            // "tool" | "system" | "message"
	Role  string          `json:"role,omitempty"`  // message segments only
	Block cacheWireBlock  `json:"block,omitempty"` // cache_control stripped
	Tool  json.RawMessage `json:"tool,omitempty"`  // cache_control stripped
}

func expandedGoldenCanonical(seg expandedGoldenSegment) string {
	raw, err := json.Marshal(seg)
	if err != nil {
		return "" // cacheWireBlock/RawMessage marshal cannot fail on these shapes
	}
	return string(raw)
}

func expandedGoldenCanonicalChain(segments []expandedGoldenSegment) string {
	parts := make([]string, len(segments))
	for i, seg := range segments {
		parts[i] = expandedGoldenCanonical(seg)
	}
	return strings.Join(parts, expandedGoldenChainSep)
}

func expandedGoldenChainTokens(segments []expandedGoldenSegment) int {
	return expandedGoldenTokens([]byte(expandedGoldenCanonicalChain(segments)))
}

func expandedGoldenStripped(block cacheWireBlock) cacheWireBlock {
	block.CacheControl = nil
	return block
}

// expandedGoldenToolSegment canonicalizes one captured tool definition: same
// content with cache_control removed (map re-marshal sorts keys, so identity
// is deterministic), plus the original marker flag for write boundaries.
func expandedGoldenToolSegment(raw json.RawMessage) (expandedGoldenSegment, bool) {
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return expandedGoldenSegment{Kind: "tool", Tool: raw}, expandedGoldenRawMarked(raw)
	}
	_, isMarked := generic["cache_control"]
	delete(generic, "cache_control")
	stripped, err := json.Marshal(generic)
	if err != nil {
		return expandedGoldenSegment{Kind: "tool", Tool: raw}, isMarked
	}
	return expandedGoldenSegment{Kind: "tool", Tool: stripped}, isMarked
}

func expandedGoldenRawMarked(raw json.RawMessage) bool {
	var decoded struct {
		CacheControl *providers.CacheControl `json:"cache_control"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	return decoded.CacheControl != nil && decoded.CacheControl.Type == "ephemeral"
}

// expandedGoldenSegments flattens a captured final-wire request into ordered
// canonical segments: each tool, then each front-instruction (system) block,
// then every message block with its role, in wire order. marked[i] reports
// the request's own cache_control marker for write boundaries.
//
// staticCount is the SC-001 pre-history boundary and is set AFTER tools AND
// system blocks: segments[:staticCount] is exactly "tools plus front
// instructions". Setting it any earlier would silently shrink the proof's
// threshold and let a system-only hit look history-sized.
func expandedGoldenSegments(body cacheWireBody) (segments []expandedGoldenSegment, marked []bool, staticCount int) {
	segments = make([]expandedGoldenSegment, 0, len(body.Tools)+len(body.System)+8)
	marked = make([]bool, 0, cap(segments))
	for _, raw := range body.Tools {
		seg, isMarked := expandedGoldenToolSegment(raw)
		segments = append(segments, seg)
		marked = append(marked, isMarked)
	}
	for _, block := range body.System {
		segments = append(segments, expandedGoldenSegment{Kind: "system", Block: expandedGoldenStripped(block)})
		marked = append(marked, expandedGoldenBlockMarked(block))
	}
	staticCount = len(segments) // tools + front instructions, and nothing after
	for _, msg := range body.Messages {
		for _, block := range msg.Content {
			segments = append(segments, expandedGoldenSegment{Kind: "message", Role: msg.Role, Block: expandedGoldenStripped(block)})
			marked = append(marked, expandedGoldenBlockMarked(block))
		}
	}
	return segments, marked, staticCount
}

func expandedGoldenBlockMarked(block cacheWireBlock) bool {
	return block.CacheControl != nil && block.CacheControl.Type == "ephemeral"
}

// expandedGoldenPreHistoryFromBody recomputes the pre-history boundary from
// the decoded body fields directly — final-wire tools plus every front
// instruction — without touching expandedGoldenSegments' bookkeeping, so a
// boundary bug in the shared helper cannot certify itself.
func expandedGoldenPreHistoryFromBody(t *testing.T, body cacheWireBody) (tokens, boundary int) {
	t.Helper()
	chain := make([]expandedGoldenSegment, 0, len(body.Tools)+len(body.System))
	for _, raw := range body.Tools {
		seg, _ := expandedGoldenToolSegment(raw)
		chain = append(chain, seg)
	}
	for _, block := range body.System {
		chain = append(chain, expandedGoldenSegment{Kind: "system", Block: expandedGoldenStripped(block)})
	}
	return expandedGoldenChainTokens(chain), len(chain)
}

const (
	expandedGoldenModeFull       = "full"
	expandedGoldenModeStaticOnly = "static-only"
)

// expandedGoldenFakeCache is the recording fake provider. It stores cache
// entries where requests are correctly marked and answers every request from
// those stored prefixes — never from a call counter.
type expandedGoldenFakeCache struct {
	mode    string
	mu      sync.Mutex
	entries map[string]int // canonical chain key -> its labelled token estimate
	bodies  [][]byte       // raw request bodies in arrival order
	reads   []int          // read returned per request
	writes  []int          // NEW-portion write returned per request
	preHist []int          // tokens(tools + front instructions) per request
}

func (f *expandedGoldenFakeCache) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body cacheWireBody
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		read, write := f.derive(raw, body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"msg_expanded_golden","type":"message","role":"assistant","model":"claude-sonnet-4-5",`+
			`"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],`+
			`"usage":{"input_tokens":200,"output_tokens":9,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d}}`,
			read, write)
	}
}

// derive applies the write/read model and records what it answered.
func (f *expandedGoldenFakeCache) derive(raw []byte, body cacheWireBody) (read, write int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bodies = append(f.bodies, raw)
	segments, marked, staticCount := expandedGoldenSegments(body)
	preHistory := expandedGoldenChainTokens(segments[:staticCount])

	// READ: the longest stored chain that is a canonical content-prefix of
	// this request. Marker bytes are not identity; the request number is
	// never consulted.
	readMatch := 0
	for k := len(segments); k >= 1; k-- {
		if tokens, ok := f.entries[expandedGoldenCanonicalChain(segments[:k])]; ok {
			read, readMatch = tokens, k
			break
		}
	}

	// WRITE: entries are created only at marked boundaries. In static-only
	// mode the fake models a deployment where only the static front is ever
	// marked. The volatile tail is never a cache entry.
	limit := len(segments)
	if f.mode == expandedGoldenModeStaticOnly {
		limit = staticCount
	}
	lastMarkedEnd := 0
	for i := 0; i < limit; i++ {
		if !marked[i] || strings.Contains(expandedGoldenCanonical(segments[i]), "Current Time") {
			continue
		}
		key := expandedGoldenCanonicalChain(segments[:i+1])
		if _, ok := f.entries[key]; !ok {
			f.entries[key] = expandedGoldenChainTokens(segments[:i+1])
		}
		if i+1 > lastMarkedEnd {
			lastMarkedEnd = i + 1
		}
	}
	// The reported write is only the NEW portion beyond the matched prefix —
	// the single largest new marked end, never overlapping breakpoint
	// prefixes summed, and never a full read hit charged again as a write.
	if lastMarkedEnd > readMatch {
		write = expandedGoldenChainTokens(segments[readMatch:lastMarkedEnd])
	}
	f.reads = append(f.reads, read)
	f.writes = append(f.writes, write)
	f.preHist = append(f.preHist, preHistory)
	return read, write
}

// expandedGoldenHistoryProof is SC-001's comparison in exactly one place: the
// fake provider's derived read must exceed the FULL pre-history prefix — tools
// plus front instructions — measured with the labelled estimator.
func expandedGoldenHistoryProof(read, preHistory int) error {
	if preHistory <= 0 {
		return fmt.Errorf("SC-001 proof unusable: pre-history prefix (tools plus front instructions) is %d, want > 0", preHistory)
	}
	if read <= preHistory {
		return fmt.Errorf("SC-001 history-cache proof failed: fake-provider cache read %d does not exceed the full pre-history prefix of %d tokens (tools plus front instructions, omnipus-json-runes/2.5)", read, preHistory)
	}
	return nil
}

// expandedGoldenPassthrough delegates every call byte-for-byte to the official
// adapter and records the normalized response the loop received. It
// deliberately implements ONLY providers.LLMProvider: without ChatStream the
// loop takes its real non-stream branch (loop_run_turn_attempt.go::
// callProviderOnce final return), so the fake HTTP server speaks plain JSON.
// It never rewrites a response.
type expandedGoldenPassthrough struct {
	inner     providers.LLMProvider
	mu        sync.Mutex
	responses []*providers.LLMResponse
}

func (w *expandedGoldenPassthrough) Chat(
	ctx context.Context,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	model string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	resp, err := w.inner.Chat(ctx, messages, toolDefs, model, opts)
	if err == nil && resp != nil {
		w.mu.Lock()
		w.responses = append(w.responses, resp)
		w.mu.Unlock()
	}
	return resp, err
}

func (w *expandedGoldenPassthrough) GetDefaultModel() string { return w.inner.GetDefaultModel() }

// expandedGoldenProbeTool is a real registered tool so the outgoing request
// carries actual tool definitions (SC-001's tools-in-the-boundary requirement).
type expandedGoldenProbeTool struct {
	tools.BaseTool
}

func (p *expandedGoldenProbeTool) Name() string        { return "expanded_golden_probe" }
func (p *expandedGoldenProbeTool) Description() string { return "Golden history-cache probe stub" }
func (p *expandedGoldenProbeTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (p *expandedGoldenProbeTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (p *expandedGoldenProbeTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	return &tools.ToolResult{ForLLM: "probe-ok", IsError: false}
}

// expandedGoldenRunSession drives one real session through the real loop with
// the official adapter behind the pass-through, moving the clock (1.1s, the
// row-13 harness's own granularity) and changing the scratchpad between calls.
func expandedGoldenRunSession(t *testing.T, fakeMode string) (*expandedGoldenFakeCache, *expandedGoldenPassthrough) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmp,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmp}},
		},
		// Fixture pins the uncompressed manifest path explicitly: compressed
		// mode withholds lazy/unknown names until a ToolSearch loads them,
		// which would make the probe's wire presence a production-mode
		// accident instead of a fixture fact. No compressed-mode fallback is
		// relied on anywhere in this harness.
		Tools: config.ToolsConfig{Manifest: config.ManifestConfig{Compressed: false}},
	}
	fake := &expandedGoldenFakeCache{mode: fakeMode, entries: map[string]int{}}
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	client := anthropic.NewClient(option.WithAPIKey("test-token"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	wrapped := &expandedGoldenPassthrough{inner: anthropicprovider.NewProviderWithClient(&client)}

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), wrapped)
	t.Cleanup(al.Close)
	al.RegisterTool(&expandedGoldenProbeTool{})
	// Explicit fixture authorization: the probe is allowed BY NAME for every
	// agent, so its advertisement (FilterToolsByPolicy) and its execution
	// resolve the same explicit verdict — never a production policy fallback.
	// Mirrors the in-tree deny pattern (async_notifier_test.go), inverted.
	for _, agentID := range al.GetRegistry().ListAgentIDs() {
		ag, ok := al.GetRegistry().GetAgent(agentID)
		if !ok {
			continue
		}
		ag.StoreToolPolicy(&tools.ToolPolicyCfg{
			Policies: map[string]config.ToolPolicy{"expanded_golden_probe": config.ToolPolicyAllow},
		})
	}

	meta, err := al.sharedSessionStore.NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	send := func(content string) {
		t.Helper()
		if _, _, err := al.processMessageAsPrimaryForTest(context.Background(), bus.InboundMessage{
			Channel:   "webchat",
			SessionID: meta.ID,
			Sender:    bus.SenderInfo{CanonicalID: "user-golden-1"},
			ChatID:    "chat-expanded-golden",
			Content:   content,
		}); err != nil {
			t.Fatalf("processMessage(%q): %v", content, err)
		}
	}
	send("TURN-ONE")

	if al.taskStore == nil {
		t.Fatal("task store is nil; scratchpad placement cannot be driven")
	}
	if err := al.taskStore.Create(&task.Task{
		Title:           "SCRATCH-GOLDEN",
		Action:          task.ActionLLM,
		AgentID:         "mia",
		CreatedBy:       "mia",
		WorkspaceID:     "ws-1",
		Status:          task.StatusInProgress,
		Scratchpad:      true,
		OriginSessionID: meta.ID,
		Todos:           []task.Todo{{Text: "golden todo", Status: task.TodoPending}},
	}); err != nil {
		t.Fatalf("create scratchpad: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)
	send("TURN-TWO")
	return fake, wrapped
}

func expandedGoldenDecodeBody(t *testing.T, raw []byte) cacheWireBody {
	t.Helper()
	var body cacheWireBody
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode captured request: %v\n%s", err, raw)
	}
	return body
}

// expandedGoldenWirePrefixStable compares the final-wire segments of two
// captured requests through the shared history item whose text is userText
// (the wire-level twin of the row-13 byte comparison). Identity is canonical
// content — moving cache breakpoints are metadata, not prompt bytes, exactly
// as the committed prefixBytes helper already treats them. The history item
// must appear exactly once per capture; an ambiguous or missing boundary is a
// mismatch, never permission to compare less.
func expandedGoldenWirePrefixStable(t *testing.T, firstRaw, secondRaw []byte, userText string) bool {
	t.Helper()
	firstSegs, _, firstStatic := expandedGoldenSegments(expandedGoldenDecodeBody(t, firstRaw))
	secondSegs, _, secondStatic := expandedGoldenSegments(expandedGoldenDecodeBody(t, secondRaw))
	if firstStatic != secondStatic {
		return false // the tools + front-instruction boundary moved
	}
	cut, secondCut := -1, -1
	for i, seg := range firstSegs {
		if seg.Block.Text == userText {
			if cut >= 0 {
				return false // ambiguous boundary
			}
			cut = i
		}
	}
	for i, seg := range secondSegs {
		if seg.Block.Text == userText {
			if secondCut >= 0 {
				return false
			}
			secondCut = i
		}
	}
	if cut < 0 || cut != secondCut || cut >= len(secondSegs) {
		return false
	}
	for i := 0; i <= cut; i++ {
		if expandedGoldenCanonical(firstSegs[i]) != expandedGoldenCanonical(secondSegs[i]) {
			return false
		}
	}
	return true
}

// expandedGoldenCaptureWire is the row-22 capture with real tool definitions:
// assembled requests through normalizeWindowMessages and the official
// adapter, for the changed-prefix and front-boundary controls. Mirrors
// captureCacheWire, which cannot be reused because it hardcodes a nil tool
// list.
func expandedGoldenCaptureWire(t *testing.T, messages []providers.Message, toolDefs []providers.ToolDefinition) cacheWireBody {
	t.Helper()
	type capture struct {
		raw []byte
		err error
	}
	captured := make(chan capture, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		captured <- capture{raw, err}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_golden_capture","type":"message","role":"assistant","model":"claude-sonnet-4-5",`+
			`"stop_reason":"end_turn","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	}))
	t.Cleanup(srv.Close)
	client := anthropic.NewClient(option.WithAPIKey("test-token"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	p := anthropicprovider.NewProviderWithClient(&client)
	if _, err := p.Chat(t.Context(), messages, toolDefs, "claude-sonnet-4-5", map[string]any{"max_tokens": 64}); err != nil {
		t.Fatalf("official adapter to fake HTTP server: %v", err)
	}
	got := <-captured
	if got.err != nil {
		t.Fatalf("read provider request: %v", got.err)
	}
	return expandedGoldenDecodeBody(t, got.raw)
}

// TestExpandedGolden_HistoryCacheReadCoversHistory is the SC-001 positive
// control on the real path: after the clock moved and the scratchpad changed,
// the second in-session call's normalized cache read — which the fake derived
// from the captured, correctly marked, content-identical prefix — covers
// history including the front instructions.
func TestExpandedGolden_HistoryCacheReadCoversHistory(t *testing.T) {
	fake, wrapped := expandedGoldenRunSession(t, expandedGoldenModeFull)

	if len(fake.reads) != 2 {
		t.Fatalf("fake provider saw %d requests, want exactly 2 in-session calls; reads=%v", len(fake.reads), fake.reads)
	}
	if len(wrapped.responses) != 2 {
		t.Fatalf("pass-through recorded %d normalized responses, want 2", len(wrapped.responses))
	}
	if fake.reads[0] != 0 {
		t.Errorf("first in-session call reported cache read %d, want 0 (cold cache writes, it does not read)", fake.reads[0])
	}
	if fake.writes[0] <= 0 {
		t.Errorf("first in-session call reported write %d, want the new marked prefix (> 0)", fake.writes[0])
	}
	if fake.preHist[0] <= 0 || fake.preHist[0] != fake.preHist[1] {
		t.Errorf("pre-history prefix (tools + front instructions) must be non-empty and stable: first=%d second=%d",
			fake.preHist[0], fake.preHist[1])
	}

	// The probe tool must be a real outgoing tool definition on the final wire.
	second := expandedGoldenDecodeBody(t, fake.bodies[1])
	toolFound := false
	for _, raw := range second.Tools {
		if strings.Contains(string(raw), "expanded_golden_probe") {
			toolFound = true
		}
	}
	if !toolFound {
		t.Errorf("the registered probe tool is absent from the second request's tool list (%d tools); "+
			"the pre-history boundary must include real outgoing tools", len(second.Tools))
	}

	// Independent boundary check (SC-001: "tools plus front instructions"):
	// recomputed from the decoded body, never from the helper's bookkeeping.
	wantTokens, wantBoundary := expandedGoldenPreHistoryFromBody(t, second)
	if fake.preHist[1] != wantTokens {
		t.Errorf("fake pre-history %d != independent tools+front boundary %d", fake.preHist[1], wantTokens)
	}
	if wantBoundary != len(second.Tools)+len(second.System) {
		t.Errorf("pre-history boundary spans %d segments, want tools(%d)+front(%d) — front instructions must count toward the bound",
			wantBoundary, len(second.Tools), len(second.System))
	}
	if len(second.System) == 0 {
		t.Errorf("no front instructions on the final wire; the boundary would be tools-only")
	}

	// Existing instruments: marks/tail validity on both final requests.
	for i, raw := range fake.bodies {
		if err := cacheWireTailError(expandedGoldenDecodeBody(t, raw)); err != nil {
			t.Errorf("request %d: %v", i+1, err)
		}
	}

	// Final-wire content through the shared TURN-ONE history is identical
	// (canonical identity: the breakpoint moving is not a content change).
	if !expandedGoldenWirePrefixStable(t, fake.bodies[0], fake.bodies[1], "TURN-ONE") {
		t.Errorf("final-wire segments through shared history TURN-ONE differ between the captured requests")
	}

	// The read the loop actually received equals the fake's prefix-derived
	// read and exceeds the full pre-history prefix (SC-001). The second call
	// also writes its genuinely new marked content (TURN-TWO), not its read.
	got := wrapped.responses[1]
	if got.Usage == nil {
		t.Fatalf("second normalized response carries no usage; the fake's read never reached accounting")
	}
	if got.Usage.CacheReadTokens != fake.reads[1] {
		t.Errorf("normalized cache read returned to the loop = %d, want the fake's prefix-derived %d",
			got.Usage.CacheReadTokens, fake.reads[1])
	}
	if fake.writes[1] <= 0 {
		t.Errorf("second call reported write %d, want the genuinely new marked portion (> 0)", fake.writes[1])
	}
	if err := expandedGoldenHistoryProof(fake.reads[1], fake.preHist[1]); err != nil {
		t.Error(err)
	}
}

// TestExpandedGolden_StaticOnlyCacheReadFailsProof is the static-only negative
// control: a fake that only ever caches the static front (tools + front
// instructions) must FAIL the SC-001 proof. This is what gives the proof's
// threshold teeth — a read that merely equals the pre-history prefix is not a
// history hit, and a full static hit must not also claim a new write.
func TestExpandedGolden_StaticOnlyCacheReadFailsProof(t *testing.T) {
	fake, _ := expandedGoldenRunSession(t, expandedGoldenModeStaticOnly)

	if len(fake.reads) != 2 {
		t.Fatalf("fake provider saw %d requests, want exactly 2; reads=%v", len(fake.reads), fake.reads)
	}
	if fake.reads[1] != fake.preHist[1] {
		t.Errorf("static-only fake derived read %d on the second request, want exactly the static pre-history %d "+
			"— the derivation must cap at the static front", fake.reads[1], fake.preHist[1])
	}
	if fake.writes[1] != 0 {
		t.Errorf("static-only second call reported write %d, want 0 — a full static hit has no new content to write", fake.writes[1])
	}
	if err := expandedGoldenHistoryProof(fake.reads[1], fake.preHist[1]); err == nil {
		t.Errorf("a read covering only the static prefix (read=%d, pre-history=%d) must FAIL the SC-001 proof, but the proof passed",
			fake.reads[1], fake.preHist[1])
	}
}

// TestExpandedGolden_ChangedHistoryPrefixFailsProof is the changed-prefix
// negative control: with genuinely different history bytes in the second
// request, the fake must refuse history credit on that call too. If this ever
// passes with a history-covering read, the fake is keying on the request
// ordinal instead of the captured prefix. It also proves the boundary counts
// front instructions: adding a known front instruction must raise the bound.
func TestExpandedGolden_ChangedHistoryPrefixFailsProof(t *testing.T) {
	tmp := setupWorkspace(t, map[string]string{"SOUL.md": "# Agent\nStable front for the mismatch control."})
	t.Cleanup(func() { os.RemoveAll(tmp) })
	cb := NewContextBuilder(filepath.Clean(tmp))
	toolDefs := []providers.ToolDefinition{{
		Type: "function",
		Function: providers.ToolFunctionDefinition{
			Name:        "expanded_golden_probe",
			Description: "Golden mismatch-control probe",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}}
	captureWith := func(builder *ContextBuilder, history []providers.Message, current string) cacheWireBody {
		t.Helper()
		msgs := builder.BuildMessages(0, history, current, nil, "", "webchat", "chat-golden-mismatch", "user-golden-1", "Alice", "", nil)
		msgs = normalizeWindowMessages(&turnState{userMessage: current}, msgs)
		return expandedGoldenCaptureWire(t, msgs, toolDefs)
	}
	historyA := []providers.Message{
		{Role: "user", Content: "PRIOR-USER-A"},
		{Role: "assistant", Content: "PRIOR-ASSISTANT"},
	}
	historyB := []providers.Message{
		{Role: "user", Content: "PRIOR-USER-B"}, // the shared history genuinely changed
		{Role: "assistant", Content: "PRIOR-ASSISTANT"},
	}

	fake := &expandedGoldenFakeCache{mode: expandedGoldenModeFull, entries: map[string]int{}}
	fake.derive(nil, captureWith(cb, historyA, "mismatch question one")) // request one writes at its marked prefix
	fake.derive(nil, captureWith(cb, historyB, "mismatch question two")) // request two: changed history
	read2, preHist2 := fake.reads[1], fake.preHist[1]

	// Front-boundary instrument check: a builder over a workspace with one
	// MORE known front instruction must raise the pre-history bound. This
	// kills a tools-only boundary — the exact omission SC-001 forbids.
	tmpRich := setupWorkspace(t, map[string]string{"SOUL.md": "# Agent\nStable front for the mismatch control.\nKNOWN-EXTRA-FRONT-INSTRUCTION-BOUNDARY"})
	t.Cleanup(func() { os.RemoveAll(tmpRich) })
	cbRich := NewContextBuilder(filepath.Clean(tmpRich))
	fake.derive(nil, captureWith(cbRich, historyA, "mismatch question one"))
	if fake.preHist[2] <= preHist2 {
		t.Errorf("adding a known front instruction must raise the pre-history bound: base=%d enriched=%d "+
			"(SC-001 counts front instructions, not tools alone)", preHist2, fake.preHist[2])
	}

	if read2 != preHist2 {
		t.Errorf("changed-prefix second request derived read %d, want exactly the static pre-history %d — "+
			"a mismatched history must earn no history credit on any call number", read2, preHist2)
	}
	if err := expandedGoldenHistoryProof(read2, preHist2); err == nil {
		t.Errorf("a changed prefix (read=%d, pre-history=%d) must FAIL the SC-001 proof, but the proof passed", read2, preHist2)
	}
}

// TestExpandedGolden_IdentityIgnoresMarksAndRoles drills the fake's identity
// rules directly on synthetic bodies (instrument-level; the wire-shaped
// scenarios are the three tests above):
//
//   - a moved history breakpoint must not cost matching history its credit —
//     marker bytes are write positions, not content identity;
//   - identical block bytes under different roles are never the same prefix.
func TestExpandedGolden_IdentityIgnoresMarksAndRoles(t *testing.T) {
	markedUser := func(text string) cacheWireMessage {
		return cacheWireMessage{Role: "user", Content: []cacheWireBlock{{
			Type: "text", Text: text, CacheControl: &providers.CacheControl{Type: "ephemeral", TTL: "5m"},
		}}}
	}
	plainUser := func(text string) cacheWireMessage {
		return cacheWireMessage{Role: "user", Content: []cacheWireBlock{{Type: "text", Text: text}}}
	}
	front := []cacheWireBlock{{Type: "text", Text: "FRONT-INSTRUCTIONS", CacheControl: &providers.CacheControl{Type: "ephemeral", TTL: "1h"}}}

	// Marker moved: request one marks HISTORY-A; request two leaves HISTORY-A
	// unmarked and marks the newer HISTORY-B. The content reuse must still be
	// credited, and only HISTORY-B counts as a new write.
	movedFake := &expandedGoldenFakeCache{mode: expandedGoldenModeFull, entries: map[string]int{}}
	movedFake.derive(nil, cacheWireBody{System: front, Messages: []cacheWireMessage{markedUser("HISTORY-A")}})
	movedFake.derive(nil, cacheWireBody{System: front, Messages: []cacheWireMessage{plainUser("HISTORY-A"), markedUser("HISTORY-B")}})
	if movedFake.reads[1] <= movedFake.preHist[1] {
		t.Errorf("a moved history breakpoint cost matching history its credit: read=%d pre-history=%d — "+
			"marker bytes must not be content identity", movedFake.reads[1], movedFake.preHist[1])
	}
	if movedFake.writes[1] <= 0 {
		t.Errorf("the genuinely new marked portion reported write %d, want > 0", movedFake.writes[1])
	}

	// Role collision: the same block text under a different role is a
	// different provider prefix and earns no history credit.
	roleFake := &expandedGoldenFakeCache{mode: expandedGoldenModeFull, entries: map[string]int{}}
	roleFake.derive(nil, cacheWireBody{System: front, Messages: []cacheWireMessage{markedUser("HISTORY-A")}})
	roleFake.derive(nil, cacheWireBody{System: front, Messages: []cacheWireMessage{{
		Role: "assistant", Content: []cacheWireBlock{{Type: "text", Text: "HISTORY-A"}},
	}}})
	if roleFake.reads[1] != roleFake.preHist[1] {
		t.Errorf("identical block bytes under a different role were credited as the same prefix: read=%d pre-history=%d — "+
			"role/boundary must be part of segment identity", roleFake.reads[1], roleFake.preHist[1])
	}
}
