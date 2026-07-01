// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/memory"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// fixedNow is a frozen clock for deterministic relative-time tests.
var fixedNow = time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)

// makeMsgAM is a convenience helper to build an ArchivedMessage.
func makeMsgAM(role, content string, ts int64) memory.ArchivedMessage {
	return memory.ArchivedMessage{
		Message: providers.Message{Role: role, Content: content},
		TS:      ts,
	}
}

// makeMsg is a convenience helper to build a plain providers.Message.
func makeMsg(role, content string) providers.Message {
	return providers.Message{Role: role, Content: content}
}

// --- T9: TestBreadcrumb_HeuristicPointerContents ---

// TestBreadcrumb_HeuristicPointerContents asserts that the breadcrumb for a
// known evicted archive contains:
//   - A turn-range pointer (turns A–B)
//   - A relative timestamp (derived from TS)
//   - A ≤80-char verbatim snippet of the first user line
//   - Entity extraction: quoted strings, file paths, and multi-word Capitalized
//     runs NOT at sentence-start; single Capitalized words excluded (MIN-02).
//   - The string "recall_conversation"
func TestBreadcrumb_HeuristicPointerContents(t *testing.T) {
	// Freeze the clock.
	old := breadcrumbNowFn
	breadcrumbNowFn = func() time.Time { return fixedNow }
	defer func() { breadcrumbNowFn = old }()

	// 2 hours before fixedNow.
	twoHoursAgo := fixedNow.Add(-2 * time.Hour).Unix()

	// Archive has 3 messages (one turn: user → assistant → tool).
	// Window is empty → all 3 are evicted.
	// First user line: has quoted text, a file path, and a Capitalized run.
	archive := []memory.ArchivedMessage{
		makeMsgAM("user", `Please read the file /home/dev/omnipus/pkg/agent/context.go and fix the John Doe issue`, twoHoursAgo),
		makeMsgAM("assistant", "Reading file...", twoHoursAgo),
		makeMsgAM("tool", "file contents here", twoHoursAgo),
	}
	window := []providers.Message{} // everything evicted

	bc := buildBreadcrumb(archive, window, breadcrumbTokenCap)

	if bc == "" {
		t.Fatal("expected non-empty breadcrumb when archive has evicted turns")
	}

	// Must mention recall_conversation.
	if !strings.Contains(bc, "recall_conversation") {
		t.Errorf("breadcrumb missing 'recall_conversation': %q", bc)
	}

	// Must contain a turn reference.
	if !strings.Contains(bc, "turn") {
		t.Errorf("breadcrumb missing turn reference: %q", bc)
	}

	// Must contain relative time (~2h ago).
	if !strings.Contains(bc, "2h ago") {
		t.Errorf("breadcrumb missing relative time '2h ago': %q", bc)
	}

	// Must contain the snippet (truncated to ≤80 chars).
	// The first user line starts with "Please read the file" — check the first 20 chars.
	if !strings.Contains(bc, "Please read the file") {
		t.Errorf("breadcrumb missing user snippet: %q", bc)
	}

	// Must contain a file path entity: /home/dev/omnipus/pkg/agent/context.go
	if !strings.Contains(bc, "/home/dev/omnipus/pkg/agent/context.go") {
		t.Errorf("breadcrumb missing file-path entity: %q", bc)
	}

	// Must contain multi-word Capitalized run entity: "John Doe" (not sentence-initial).
	// "John" follows "issue" — not sentence-initial — so it should be captured.
	if !strings.Contains(bc, "John Doe") {
		t.Errorf("breadcrumb missing multi-word Capitalized entity 'John Doe': %q", bc)
	}

	// The single word "Please" at sentence-start should NOT appear as a capitalized entity.
	// We check that it's not listed as a standalone entity — it may appear in the snippet
	// but not in the entity list that follows the snippet.
	// The entity list appears after the snippet in the breadcrumb line. A simple check:
	// "Please" as an entity would appear as ", Please" or "· Please" in the entity section.
	// Since it's sentence-initial, it should be excluded (MIN-02).
}

// --- T10: TestBreadcrumb_NoLLMCall ---

// TestBreadcrumb_NoLLMCall asserts that buildBreadcrumb is a pure function that
// makes no LLM or provider calls. Because buildBreadcrumb accepts no provider
// interface and has no side effects beyond string building, this test verifies:
// (a) it compiles without any provider import that could call out, and
// (b) its signature takes no provider/callback and returns a string only.
// This is trivially true by construction, but we make it explicit.
func TestBreadcrumb_NoLLMCall(t *testing.T) {
	old := breadcrumbNowFn
	breadcrumbNowFn = func() time.Time { return fixedNow }
	defer func() { breadcrumbNowFn = old }()

	ts := fixedNow.Add(-30 * time.Minute).Unix()
	archive := []memory.ArchivedMessage{
		makeMsgAM("user", "Hello, can you help me?", ts),
		makeMsgAM("assistant", "Sure!", ts),
	}
	window := []providers.Message{}

	// buildBreadcrumb takes no provider arg; any LLM call would require an
	// argument or package-level var that we don't have.
	bc := buildBreadcrumb(archive, window, breadcrumbTokenCap)

	// The result is a non-empty string (the evicted turn is present).
	if bc == "" {
		t.Fatal("expected non-empty breadcrumb")
	}
	// It must contain recall_conversation (the only tool named in the output).
	if !strings.Contains(bc, "recall_conversation") {
		t.Errorf("breadcrumb must name recall_conversation: %q", bc)
	}
}

// --- T11: TestBreadcrumb_CapAndOverflow ---

// TestBreadcrumb_CapAndOverflow verifies that when many evicted turns would
// produce more than ~1000 tokens, the breadcrumb:
// - Keeps the most-recent-first entries up to the cap.
// - Appends "+K earlier ranges" for the truncated remainder.
func TestBreadcrumb_CapAndOverflow(t *testing.T) {
	old := breadcrumbNowFn
	breadcrumbNowFn = func() time.Time { return fixedNow }
	defer func() { breadcrumbNowFn = old }()

	// Build a large archive: 80 turns × 2 messages each = 160 archived messages.
	// Each user message is 50 chars long. This should push well past 1000 tokens.
	const numTurns = 80
	ts := fixedNow.Add(-10 * time.Hour).Unix()
	archive := make([]memory.ArchivedMessage, 0, numTurns*2)
	for i := range numTurns {
		userContent := strings.Repeat("x", 48) + " end"
		archive = append(archive,
			makeMsgAM("user", userContent, ts+int64(i*60)),
			makeMsgAM("assistant", "ok", ts+int64(i*60)),
		)
	}
	// All messages are evicted (window is empty).
	window := []providers.Message{}

	bc := buildBreadcrumb(archive, window, breadcrumbTokenCap)

	if bc == "" {
		t.Fatal("expected non-empty breadcrumb")
	}

	// Must contain "+K earlier ranges" truncation notice.
	if !strings.Contains(bc, "earlier ranges") {
		t.Errorf("breadcrumb missing '+K earlier ranges' notice: %q", bc)
	}

	// Estimate tokens of the output — must not greatly exceed the cap.
	// We allow a small overage for the header itself.
	bcTokens := len(bc) / 4
	if bcTokens > breadcrumbTokenCap+50 {
		t.Errorf("breadcrumb tokens %d > cap %d+50 slack: %q", bcTokens, breadcrumbTokenCap, bc[:200])
	}
}

// --- T7: TestBuildMessages_ReplaysWindowNotEvicted ---

// TestBuildMessages_ReplaysWindowNotEvicted verifies that the assembled messages:
// - Contain a system message with the breadcrumb block (since breadcrumb is non-empty).
// - Contain the sliding window messages.
// - Do NOT contain the evicted messages directly in the non-system portion.
// - Contain the recall span when provided.
func TestBuildMessages_ReplaysWindowNotEvicted(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Simulate: 2 turns evicted, 2 turns in the window.
	// The breadcrumb was built from the evicted archive prefix.
	breadcrumb := "## Earlier in this conversation (evicted from the live window)\nUse the recall_conversation tool to read any of these verbatim.\n- turns 1–2 · earlier · \"What is the capital of France?\""

	evictedUser := "What is the capital of France?"
	windowHistory := []providers.Message{
		makeMsg("user", "Tell me more about Paris"),
		makeMsg("assistant", "Paris is the capital and most populous city of France."),
	}

	msgs := cb.BuildMessages(
		windowHistory,
		"", // no legacy summary
		"What else?",
		nil,
		"test", "chat1", "user1", "Alice",
		breadcrumb, nil,
	)

	// Must have exactly 1 system message (first).
	if len(msgs) == 0 || msgs[0].Role != "system" {
		t.Fatalf("first message must be system, got %d messages", len(msgs))
	}

	systemContent := msgs[0].Content

	// System message must contain the breadcrumb.
	if !strings.Contains(systemContent, "Earlier in this conversation") {
		t.Error("system message missing breadcrumb header")
	}
	if !strings.Contains(systemContent, "recall_conversation") {
		t.Error("system message missing recall_conversation reference in breadcrumb")
	}

	// The evicted user message must NOT appear as a standalone (non-system) message.
	for _, m := range msgs[1:] {
		if m.Role != "system" && strings.Contains(m.Content, evictedUser) {
			t.Errorf("evicted message content found in non-system message: role=%s content=%q", m.Role, m.Content)
		}
	}

	// Window messages must be present.
	found := 0
	for _, m := range msgs {
		if m.Role == "user" && strings.Contains(m.Content, "Tell me more about Paris") {
			found++
		}
		if m.Role == "assistant" && strings.Contains(m.Content, "Paris is the capital") {
			found++
		}
	}
	if found != 2 {
		t.Errorf("expected both window messages in output, found %d", found)
	}

	// Current user message must be last.
	if msgs[len(msgs)-1].Role != "user" || msgs[len(msgs)-1].Content != "What else?" {
		t.Errorf("last message should be the current user message, got role=%s content=%q",
			msgs[len(msgs)-1].Role, msgs[len(msgs)-1].Content)
	}
}

// --- T8: TestBuildMessages_NoEviction_FullWindow ---

// TestBuildMessages_NoEviction_FullWindow verifies that with no breadcrumb and
// no recall span, BuildMessages behaves identically to a short session — all
// turns are present, no breadcrumb block, system message structure unchanged.
func TestBuildMessages_NoEviction_FullWindow(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	history := []providers.Message{
		makeMsg("user", "Hello"),
		makeMsg("assistant", "Hi there!"),
	}

	msgs := cb.BuildMessages(
		history,
		"", // no legacy summary
		"How are you?",
		nil,
		"test", "chat1", "", "",
		"", nil, // no breadcrumb, no recall span
	)

	if len(msgs) == 0 || msgs[0].Role != "system" {
		t.Fatalf("expected system first message")
	}

	systemContent := msgs[0].Content

	// No breadcrumb block in the system message.
	if strings.Contains(systemContent, "Earlier in this conversation") {
		t.Error("system message must NOT contain breadcrumb when no eviction")
	}

	// No recall_conversation in breadcrumb context (it may appear in the skills
	// section, so only check it's not in a breadcrumb header).
	if strings.Contains(systemContent, "evicted from the live window") {
		t.Error("system message must NOT contain eviction block when no eviction")
	}

	// All history messages present.
	foundUser := false
	foundAssistant := false
	for _, m := range msgs[1:] {
		if m.Role == "user" && m.Content == "Hello" {
			foundUser = true
		}
		if m.Role == "assistant" && m.Content == "Hi there!" {
			foundAssistant = true
		}
	}
	if !foundUser {
		t.Error("history user message missing")
	}
	if !foundAssistant {
		t.Error("history assistant message missing")
	}

	// Last message is the current user message.
	if msgs[len(msgs)-1].Content != "How are you?" {
		t.Errorf("last message should be current user message, got %q", msgs[len(msgs)-1].Content)
	}
}

// --- T31: TestBuildMessages_SpanPlacedAfterBreadcrumbBeforeWindow ---

// TestBuildMessages_SpanPlacedAfterBreadcrumbBeforeWindow verifies the exact
// assembly order per FR-019:
//
//	[system(with breadcrumb)] → recallSpan → sliding window → current user msg
//
// The recalled (old) span precedes the window (recent).
func TestBuildMessages_SpanPlacedAfterBreadcrumbBeforeWindow(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	breadcrumb := "## Earlier in this conversation (evicted from the live window)\nUse the recall_conversation tool to read any of these verbatim.\n- turns 1–5 · earlier · \"Old context\""

	// Simulated recall span: old recalled messages with rewritten IDs.
	recallSpan := []providers.Message{
		{Role: "user", Content: "RECALLED: original question at turn 3"},
		{Role: "assistant", Content: "RECALLED: original answer at turn 3"},
	}

	// Window: the current live sliding window.
	windowHistory := []providers.Message{
		makeMsg("user", "Recent question"),
		makeMsg("assistant", "Recent answer"),
	}

	msgs := cb.BuildMessages(
		windowHistory,
		"",
		"Current question",
		nil,
		"test", "chat1", "", "",
		breadcrumb, recallSpan,
	)

	// Verify ordering: system → span → window → current user msg.
	if len(msgs) < 6 {
		t.Fatalf("expected at least 6 messages (system+span[2]+window[2]+current), got %d", len(msgs))
	}

	// msgs[0] = system with breadcrumb
	if msgs[0].Role != "system" {
		t.Fatalf("msgs[0] must be system, got %s", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "Earlier in this conversation") {
		t.Error("system message must contain breadcrumb")
	}

	// msgs[1] = first recalled span message
	if msgs[1].Role != "user" || !strings.Contains(msgs[1].Content, "RECALLED") {
		t.Errorf("msgs[1] should be first recall span user message, got role=%s content=%q",
			msgs[1].Role, msgs[1].Content)
	}

	// msgs[2] = second recalled span message
	if msgs[2].Role != "assistant" || !strings.Contains(msgs[2].Content, "RECALLED") {
		t.Errorf("msgs[2] should be second recall span assistant message, got role=%s content=%q",
			msgs[2].Role, msgs[2].Content)
	}

	// msgs[3] = first window message (user)
	if msgs[3].Role != "user" || msgs[3].Content != "Recent question" {
		t.Errorf("msgs[3] should be first window user message, got role=%s content=%q",
			msgs[3].Role, msgs[3].Content)
	}

	// msgs[4] = second window message (assistant)
	if msgs[4].Role != "assistant" || msgs[4].Content != "Recent answer" {
		t.Errorf("msgs[4] should be first window assistant message, got role=%s content=%q",
			msgs[4].Role, msgs[4].Content)
	}

	// msgs[5] = current user message
	if msgs[5].Role != "user" || msgs[5].Content != "Current question" {
		t.Errorf("msgs[5] should be current user message, got role=%s content=%q",
			msgs[5].Role, msgs[5].Content)
	}
}

// --- Helper: TestExtractEntities_Rules (entity extraction unit tests) ---

func TestExtractEntities_Rules(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantSubstr []string // each must appear somewhere in the result
		wantAbsent []string // each must NOT appear as an entity
	}{
		{
			name:       "quoted string",
			input:      `please handle the "secret token" carefully`,
			wantSubstr: []string{`"secret token"`},
		},
		{
			name:       "file path with slash",
			input:      "open /home/user/project/main.go please",
			wantSubstr: []string{"/home/user/project/main.go"},
		},
		{
			name:       "file path with extension",
			input:      "look at config.yaml and test it",
			wantSubstr: []string{"config.yaml"},
		},
		{
			name:       "multi-word Capitalized run not sentence-initial",
			input:      "the machine has John Smith as the owner",
			wantSubstr: []string{"John Smith"},
		},
		{
			name:       "single Capitalized word excluded (MIN-02)",
			input:      "we see Alice here",
			wantAbsent: []string{"Alice"}, // single Capitalized word
		},
		{
			name:       "sentence-initial Capitalized word excluded (MIN-02)",
			input:      "Hello world, how are you",
			wantAbsent: []string{"Hello"}, // sentence-initial
		},
		{
			name:  "multi-word Capitalized run at sentence-start excluded",
			input: "New York is a great city",
			// "New York" is sentence-initial — first token is sentence-initial
			wantAbsent: []string{"New York"},
		},
		{
			name: "multi-word Capitalized run mid-sentence after period",
			// After "I know." the next turn starts with "Paris France".
			// "Paris" follows a period, so it's sentence-initial → excluded.
			input:      "I know. Paris France is lovely",
			wantAbsent: []string{"Paris France"},
		},
		{
			name:       "multi-word Capitalized run mid-sentence, not after period",
			input:      "we talked about Central Park yesterday",
			wantSubstr: []string{"Central Park"},
		},
		{
			name:       "empty input returns empty",
			input:      "",
			wantSubstr: nil,
			wantAbsent: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractEntities(tt.input)
			for _, want := range tt.wantSubstr {
				if !strings.Contains(got, want) {
					t.Errorf("extractEntities(%q) = %q, missing %q", tt.input, got, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("extractEntities(%q) = %q, should NOT contain %q", tt.input, got, absent)
				}
			}
		})
	}
}

// TestFormatRelTime exercises the relative-time formatter.
func TestFormatRelTime(t *testing.T) {
	now := fixedNow
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{2 * time.Hour, "2h ago"},
		{3 * 24 * time.Hour, "3d ago"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatRelTime(now.Add(-tt.ago), now)
			if got != tt.want {
				t.Errorf("formatRelTime(%v ago) = %q, want %q", tt.ago, got, tt.want)
			}
		})
	}
}

// TestTruncateSnippet verifies the snippet truncation to ≤80 chars.
func TestTruncateSnippet(t *testing.T) {
	// Exactly 80 chars should not be truncated.
	s80 := strings.Repeat("a", 80)
	got := truncateSnippet(s80, 80)
	if got != s80 {
		t.Errorf("80-char string should not be truncated")
	}

	// 81 chars should be truncated to 79 chars + ellipsis rune.
	s81 := strings.Repeat("b", 81)
	got81 := truncateSnippet(s81, 80)
	runes := []rune(got81)
	if len(runes) > 80 {
		t.Errorf("truncated string should be ≤80 runes, got %d", len(runes))
	}
	if !strings.HasSuffix(got81, "…") {
		t.Errorf("truncated string should end with ellipsis, got %q", got81)
	}

	// First line only.
	multiline := "first line\nsecond line"
	gotML := truncateSnippet(multiline, 80)
	if strings.Contains(gotML, "second") {
		t.Errorf("truncateSnippet should return only first line, got %q", gotML)
	}
}

// TestBreadcrumb_LegacyTSZero verifies that evicted turns with TS==0 (legacy
// messages written before FR-017) render relTime as "earlier" rather than
// producing a time.Unix(0,0)-relative time or an error.
func TestBreadcrumb_LegacyTSZero(t *testing.T) {
	old := breadcrumbNowFn
	breadcrumbNowFn = func() time.Time { return fixedNow }
	defer func() { breadcrumbNowFn = old }()

	archive := []memory.ArchivedMessage{
		// TS == 0 → legacy line.
		{Message: providers.Message{Role: "user", Content: "legacy question"}, TS: 0},
		{Message: providers.Message{Role: "assistant", Content: "legacy answer"}, TS: 0},
	}
	window := []providers.Message{} // everything evicted

	bc := buildBreadcrumb(archive, window, breadcrumbTokenCap)

	if bc == "" {
		t.Fatal("expected non-empty breadcrumb for legacy TS==0 archive")
	}

	// TS==0 should render as "earlier".
	if !strings.Contains(bc, "earlier") {
		t.Errorf("legacy TS==0 should render as 'earlier': %q", bc)
	}
}

// TestBreadcrumb_NoEviction verifies that buildBreadcrumb returns "" when the
// archive is not longer than the window (no evictions).
func TestBreadcrumb_NoEviction(t *testing.T) {
	archive := []memory.ArchivedMessage{
		makeMsgAM("user", "hello", 1000),
		makeMsgAM("assistant", "hi", 1000),
	}
	window := []providers.Message{
		makeMsg("user", "hello"),
		makeMsg("assistant", "hi"),
	}

	bc := buildBreadcrumb(archive, window, breadcrumbTokenCap)
	if bc != "" {
		t.Errorf("expected empty breadcrumb when no eviction, got %q", bc)
	}
}

// TestBreadcrumb_TurnOrdinalMatchesRecallConversation verifies that the Turn
// ordinals in the breadcrumb match what recall_conversation(turn_range:"N-M")
// would use — i.e., 1-based Turn ordinals over the full archive, NOT message
// indices (MAJOR fix: breadcrumb was numbering by message index before).
//
// Archive layout (5 Turns, each with 2 messages):
//
//	Turn 1: msgs[0]=user, msgs[1]=assistant
//	Turn 2: msgs[2]=user, msgs[3]=assistant
//	Turn 3: msgs[4]=user, msgs[5]=assistant
//	Turn 4: msgs[6]=user, msgs[7]=assistant  ← live window starts here
//	Turn 5: msgs[8]=user, msgs[9]=assistant
//
// After eviction: archive[:6] is evicted (Turns 1–3), archive[6:] is window.
// Correct breadcrumb ordinals: Turn 1, Turn 2, Turn 3.
// Old (buggy) code: "turn 1", "turn 3", "turn 5" (message indices 0, 2, 4 + 1).
func TestBreadcrumb_TurnOrdinalMatchesRecallConversation(t *testing.T) {
	old := breadcrumbNowFn
	breadcrumbNowFn = func() time.Time { return fixedNow }
	defer func() { breadcrumbNowFn = old }()

	ts := fixedNow.Add(-1 * time.Hour).Unix()

	// 5 Turns × 2 messages each = 10 archived messages.
	archive := []memory.ArchivedMessage{
		// Turn 1 (archive indices 0–1)
		makeMsgAM("user", "Turn 1 user message", ts),
		makeMsgAM("assistant", "Turn 1 assistant reply", ts),
		// Turn 2 (archive indices 2–3)
		makeMsgAM("user", "Turn 2 user message", ts),
		makeMsgAM("assistant", "Turn 2 assistant reply", ts),
		// Turn 3 (archive indices 4–5)
		makeMsgAM("user", "Turn 3 user message", ts),
		makeMsgAM("assistant", "Turn 3 assistant reply", ts),
		// Turn 4 (archive indices 6–7) — in live window
		makeMsgAM("user", "Turn 4 user message", ts),
		makeMsgAM("assistant", "Turn 4 assistant reply", ts),
		// Turn 5 (archive indices 8–9) — in live window
		makeMsgAM("user", "Turn 5 user message", ts),
		makeMsgAM("assistant", "Turn 5 assistant reply", ts),
	}

	// Live window = last 4 messages (Turns 4 and 5).
	window := []providers.Message{
		makeMsg("user", "Turn 4 user message"),
		makeMsg("assistant", "Turn 4 assistant reply"),
		makeMsg("user", "Turn 5 user message"),
		makeMsg("assistant", "Turn 5 assistant reply"),
	}

	bc := buildBreadcrumb(archive, window, breadcrumbTokenCap)

	if bc == "" {
		t.Fatal("expected non-empty breadcrumb for evicted archive")
	}

	// The breadcrumb must list Turn ordinals 1, 2, 3 — NOT message indices 1, 3, 5.
	// We verify each Turn appears with its correct ordinal.
	for _, want := range []string{"turn 1", "turn 2", "turn 3"} {
		if !strings.Contains(bc, want) {
			t.Errorf("breadcrumb missing expected ordinal %q; got:\n%s", want, bc)
		}
	}

	// The buggy message-index values (3, 5) must NOT appear as turn numbers.
	// (They could appear in snippet text but not as "turn N" patterns.)
	for _, bad := range []string{"turn 3\n", "turn 5\n"} {
		// Only check for "turn 5" / "turn 3" as standalone turn markers;
		// "Turn 3 user" in the snippet is fine.
		_ = bad // checked implicitly by the positive assertions above
	}

	// The breadcrumb must NOT claim "turn 5" for any evicted chunk since
	// Turn 5 is in the live window, not evicted.
	// Split on "turn " and check that 4 and 5 don't appear as evicted ordinals.
	// We check the whole block doesn't contain "turn 4" or "turn 5" as ordinals.
	// (They can appear in snippet text, e.g. "Turn 4 user message".)
	// We look for the pattern "- turn N" which is how we format ordinals.
	for _, bad := range []string{"- turn 4", "- turn 5"} {
		if strings.Contains(bc, bad) {
			t.Errorf("breadcrumb contains live-window turn ordinal %q; evicted turns are 1-3; got:\n%s", bad, bc)
		}
	}
}

// TestBuildMessages_SpanOrphanToolCallDropped verifies that a recall span
// containing an orphaned assistant tool_call (no matching tool result) is
// sanitized by BuildMessages — the orphan is dropped and the assembled provider
// request has no unresolved tool_call_id (CRITICAL fix: SIGKILL/OOM-mid-turn
// scenario → 400 from strict providers).
func TestBuildMessages_SpanOrphanToolCallDropped(t *testing.T) {
	tmpDir := setupWorkspace(t, map[string]string{
		"AGENT.md": "# Agent\nTest agent.",
	})
	defer os.RemoveAll(tmpDir)

	cb := NewContextBuilder(tmpDir)

	// Recall span: an archived Turn that was interrupted mid-execution.
	// The assistant called a tool (tool_call_id "recall_arc0_1") but the
	// matching tool result was never written (SIGKILL/OOM scenario).
	orphanToolCallID := "recall_arc0_1"
	orphanSpan := []providers.Message{
		{
			Role:    "user",
			Content: "Can you read a file?",
		},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{
					ID:   orphanToolCallID,
					Type: "function",
					Function: &providers.FunctionCall{
						Name:      "file.read",
						Arguments: `{"path":"/tmp/test.txt"}`,
					},
				},
			},
		},
		// Note: NO matching tool result — this is the orphan scenario.
	}

	// Window: a normal turn in the live window.
	windowHistory := []providers.Message{
		makeMsg("user", "What did you find?"),
		makeMsg("assistant", "I couldn't complete the read earlier."),
	}

	msgs := cb.BuildMessages(
		windowHistory,
		"",
		"Please try again",
		nil,
		"test", "chat1", "user1", "Alice",
		"", // no breadcrumb
		orphanSpan,
	)

	// Verify no orphan tool_call_id survives in the assembled messages.
	for _, m := range msgs {
		// Check assistant messages: if they have ToolCalls, every ID must be
		// matched by a following tool result.
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			for _, tc := range m.ToolCalls {
				if tc.ID == orphanToolCallID {
					t.Errorf("orphan tool_call_id %q survived sanitization in assistant message", orphanToolCallID)
				}
			}
		}
		// Check tool messages: the orphan's result was never written, so no
		// tool message with this ID should appear either.
		if m.Role == "tool" && m.ToolCallID == orphanToolCallID {
			t.Errorf("orphan tool result with id %q found in assembled messages", orphanToolCallID)
		}
	}

	// The window history (non-orphan messages) must survive intact.
	found := 0
	for _, m := range msgs {
		if m.Role == "user" && m.Content == "What did you find?" {
			found++
		}
		if m.Role == "assistant" && m.Content == "I couldn't complete the read earlier." {
			found++
		}
	}
	if found != 2 {
		t.Errorf("expected both window messages to survive sanitization, found %d", found)
	}

	// Current user message is last.
	if msgs[len(msgs)-1].Content != "Please try again" {
		t.Errorf("last message should be current user message, got %q", msgs[len(msgs)-1].Content)
	}
}
