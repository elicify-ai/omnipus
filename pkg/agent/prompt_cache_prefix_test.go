package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// secondsInPrefix is the UTC clock the dynamic context prints in front of
// history today (2006-01-02 15:04:05Z). A seconds clock in front of history
// makes two turns differ. Putting that clock back in front must fail.
var secondsInPrefix = regexp.MustCompile(`\d{2}:\d{2}:\d{2}Z`)

// historyBoundary must identify the SAME history item in both captures. A
// missing sentinel is an error, never permission to include the volatile tail.
func historyBoundary(messages []providers.Message, role, content string) (int, error) {
	found := -1
	for i, msg := range messages {
		if msg.Role == role && msg.Content == content {
			if found >= 0 {
				return -1, fmt.Errorf("history boundary %s %q is ambiguous", role, content)
			}
			found = i
		}
	}
	if found < 0 {
		return -1, fmt.Errorf("history boundary %s %q is missing", role, content)
	}
	return found, nil
}

func prefixThrough(messages []providers.Message, role, content string) (string, error) {
	end, err := historyBoundary(messages, role, content)
	if err != nil {
		return "", err
	}
	return prefixBytes(messages[:end+1])
}

func mustPrefixThrough(t *testing.T, messages []providers.Message, role, content string) string {
	t.Helper()
	prefix, err := prefixThrough(messages, role, content)
	if err != nil {
		t.Fatal(err)
	}
	return prefix
}

func prefixBytes(messages []providers.Message) (string, error) {
	// Compare all serialized message fields, not just Role/Content. Moving cache
	// breakpoints are metadata, not rendered prompt bytes (Anthropic caching
	// guide, "Verifying cache hits"). Do not mutate the captured request.
	// ToolCall.Name/Arguments have json:"-" in the shared type but adapters
	// consume them. Include those fields explicitly so their mutations count.
	type prefixCall struct {
		providers.ToolCall
		Name      string         `json:"name,omitempty"`
		Arguments map[string]any `json:"arguments,omitempty"`
	}
	type prefixMessage struct {
		providers.Message
		ToolCalls []prefixCall `json:"tool_calls,omitempty"`
	}
	copyMessages := make([]prefixMessage, len(messages))
	for i, msg := range messages {
		copyMessages[i].Message = msg
		copyMessages[i].SystemParts = append([]providers.ContentBlock(nil), msg.SystemParts...)
		for j := range copyMessages[i].SystemParts {
			copyMessages[i].SystemParts[j].CacheControl = nil
		}
		for _, call := range msg.ToolCalls {
			copyMessages[i].ToolCalls = append(copyMessages[i].ToolCalls, prefixCall{call, call.Name, call.Arguments})
		}
	}
	raw, err := json.Marshal(copyMessages)
	return string(raw), err
}

func messageTexts(messages []providers.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Role)
		b.WriteByte(':')
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestPrefixStable_ClockAndScratchpadMove is TDD row 13. Two built requests.
// The clock advances and the scratchpad changes. Bytes through the previous
// turn's history match, and the clock is not a system message in front.
func TestPrefixStable_ClockAndScratchpadMove(t *testing.T) {
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
	}
	provider := &cacheOptProvider{}
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)

	meta, sessionErr := al.sharedSessionStore.NewSession(session.SessionTypeChat, "webchat", "mia")
	if sessionErr != nil {
		t.Fatalf("NewSession: %v", sessionErr)
	}
	send := func(content string) {
		t.Helper()
		_, _, err := al.processMessageAsPrimaryForTest(context.Background(), bus.InboundMessage{
			Channel:   "webchat",
			SessionID: meta.ID,
			Sender:    bus.SenderInfo{CanonicalID: "user-web-1"},
			ChatID:    "chat-prefix-1",
			Content:   content,
		})
		if err != nil {
			t.Fatalf("processMessage(%q): %v", content, err)
		}
	}

	send("TURN-ONE")
	first := append([]providers.Message(nil), provider.last...)
	if key, _ := provider.lastOpts["prompt_cache_key"].(string); key != meta.ID {
		t.Errorf("prompt_cache_key = %q, want session id %q, not the agent id", key, meta.ID)
	}

	if al.taskStore == nil {
		t.Fatal("task store is nil; scratchpad placement cannot be driven")
	}
	if err := al.taskStore.Create(&task.Task{
		Title:           "SCRATCH-ONE",
		Action:          task.ActionLLM,
		AgentID:         "mia",
		CreatedBy:       "mia",
		WorkspaceID:     "ws-1",
		Status:          task.StatusInProgress,
		Scratchpad:      true,
		OriginSessionID: meta.ID,
		Todos:           []task.Todo{{Text: "first todo", Status: task.TodoPending}},
	}); err != nil {
		t.Fatalf("create scratchpad: %v", err)
	}

	time.Sleep(1100 * time.Millisecond)
	send("TURN-TWO")
	second := append([]providers.Message(nil), provider.last...)

	// TURN-ONE is the last history item actually SENT in the first capture.
	// Its assistant response did not exist yet. Cut BOTH requests at TURN-ONE.
	before := mustPrefixThrough(t, first, "user", "TURN-ONE")
	after := mustPrefixThrough(t, second, "user", "TURN-ONE")
	if before != after {
		t.Errorf("bytes through shared history differ after the clock advanced\nfirst:\n%s\nsecond:\n%s", before, after)
	}

	assertVolatileTail(t, second, "TURN-TWO", "SCRATCH-ONE")

	// Change the scratchpad and build again. Dataset row 2.
	tasks, err := al.taskStore.List(task.Filter{AgentID: "mia"})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	title := "SCRATCH-TWO"
	for i := range tasks {
		if tasks[i].Scratchpad && tasks[i].OriginSessionID == meta.ID {
			if _, err := al.taskStore.Update(tasks[i].ID, task.Patch{Title: &title}); err != nil {
				t.Fatalf("update scratchpad: %v", err)
			}
		}
	}
	time.Sleep(1100 * time.Millisecond)
	send("TURN-THREE")
	third := append([]providers.Message(nil), provider.last...)
	before = mustPrefixThrough(t, second, "user", "TURN-TWO")
	after = mustPrefixThrough(t, third, "user", "TURN-TWO")
	if before != after {
		t.Errorf("bytes through shared history differ after the scratchpad changed\nsecond:\n%s\nthird:\n%s", before, after)
	}
	assertVolatileTail(t, third, "TURN-THREE", "SCRATCH-TWO")
}

func assertVolatileTail(t *testing.T, messages []providers.Message, history, scratch string) {
	t.Helper()
	if err := volatileTailError(messages, history, scratch); err != nil {
		t.Errorf("%v\n%s", err, messageTexts(messages))
	}
}

func volatileTailError(messages []providers.Message, history, scratch string) error {
	end, err := historyBoundary(messages, "user", history)
	if err != nil {
		return err
	}
	for i, msg := range messages[:end+1] {
		text := msg.Content
		for _, part := range msg.SystemParts {
			text += part.Text
		}
		if secondsInPrefix.MatchString(text) || strings.Contains(text, "Current Time") ||
			(scratch != "" && strings.Contains(text, scratch)) || strings.Contains(text, skillReminderNote) {
			return fmt.Errorf("volatile note at message %d before the history boundary", i)
		}
	}
	if len(messages[end+1:]) != 1 {
		return fmt.Errorf("volatile tail has %d messages, want one user-role block", len(messages[end+1:]))
	}
	tail := messages[end+1]
	if tail.Role != "user" || len(tail.ToolCalls) != 0 || tail.ToolCallID != "" {
		return fmt.Errorf("volatile tail is not a user-role text block")
	}
	if !strings.Contains(tail.Content, "Current Time") && !secondsInPrefix.MatchString(tail.Content) {
		return fmt.Errorf("clock is not in the user-role tail after history")
	}
	if scratch != "" && !strings.Contains(tail.Content, scratch) {
		return fmt.Errorf("scratchpad %q is not in the user-role tail after history", scratch)
	}
	if strings.Contains(tail.Content, "Rendering (web chat)") {
		return fmt.Errorf("web-display note is after history; it stays in front")
	}
	return nil
}

// TestPrefixStable_AfterNormalizeAndAdapter is TDD row 22. Capture real HTTP
// bytes after BOTH the window normalizer and the official Anthropic adapter.
func TestPrefixStable_AfterNormalizeAndAdapter(t *testing.T) {
	tmp := setupWorkspace(t, map[string]string{"SOUL.md": "# Agent\nWORKSPACE-INSTRUCTIONS-STAY-IN-FRONT"})
	t.Cleanup(func() { os.RemoveAll(tmp) })
	cb := NewContextBuilder(tmp)
	history := []providers.Message{
		{Role: "user", Content: "PRIOR-USER"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
			{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "b.txt"}},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "file-body-a"},
		{Role: "tool", ToolCallID: "call_2", Content: "file-body-b"},
		{Role: "assistant", Content: "PRIOR-ASSISTANT"},
	}
	capture := func(current string) cacheWireBody {
		t.Helper()
		msgs := cb.BuildMessages(0, history, current, nil, "", "webchat", "chat-1", "user-1", "Alice", "", nil)
		msgs = normalizeWindowMessages(&turnState{userMessage: current}, msgs)
		return captureCacheWire(t, msgs)
	}
	first := capture("current question one")
	time.Sleep(1100 * time.Millisecond)
	second := capture("current question two")
	before := cacheWirePrefix(t, first, "PRIOR-ASSISTANT")
	after := cacheWirePrefix(t, second, "PRIOR-ASSISTANT")
	if before != after {
		t.Errorf("adapter bytes through shared history differ after the clock advanced\nfirst:\n%s\nsecond:\n%s", before, after)
	}
	for i, body := range []cacheWireBody{first, second} {
		if err := cacheWireTailError(body); err != nil {
			t.Error(err)
		}
		wantCurrent := []string{"current question one", "current question two"}[i]
		currentCount := 0
		for _, msg := range body.Messages {
			for _, block := range msg.Content {
				if msg.Role == "user" && block.Type == "text" && block.Text == wantCurrent {
					currentCount++
				}
			}
		}
		if currentCount != 1 {
			t.Errorf("adapted current question %q occurs %d times, want exactly once before the tail", wantCurrent, currentCount)
		}
		assertCacheWireToolResults(t, body, map[string]string{"call_1": "file-body-a", "call_2": "file-body-b"})
		var front strings.Builder
		for _, block := range body.System {
			front.WriteString(block.Text)
		}
		if !strings.Contains(front.String(), "WORKSPACE-INSTRUCTIONS-STAY-IN-FRONT") {
			t.Error("workspace instructions are absent from the front of the adapted request")
		}
	}
}

// TestHistoryHit_StaticOnlyFails is TDD row 29. A cache read that covers only
// the pre-history prefix fails. A read beyond that prefix passes only when the
// captured prefix itself matched, which it does not while a seconds clock sits
// in front.
func TestHistoryHit_StaticOnlyFails(t *testing.T) {
	tmp := setupWorkspace(t, map[string]string{"SOUL.md": "# Agent\nStable."})
	t.Cleanup(func() { os.RemoveAll(tmp) })
	cb := NewContextBuilder(filepath.Clean(tmp))
	history := []providers.Message{{Role: "user", Content: "PRIOR-USER"}, {Role: "assistant", Content: "PRIOR-ASSISTANT"}}
	first := cb.BuildMessages(0, history, "one", nil, "", "test", "c", "u", "A", "", nil)
	time.Sleep(1100 * time.Millisecond)
	second := cb.BuildMessages(0, history, "two", nil, "", "test", "c", "u", "A", "", nil)

	pre, err := prefixBeforeHistory(first, "PRIOR-USER")
	if err != nil {
		t.Fatal(err)
	}
	// A matching static front alone is not a matching history prefix.
	before := mustPrefixThrough(t, first, "assistant", "PRIOR-ASSISTANT")
	after := mustPrefixThrough(t, second, "assistant", "PRIOR-ASSISTANT")
	matched := before == after
	if !matched {
		t.Errorf("captured shared-history prefixes differ after the clock advanced\nfirst:\n%s\nsecond:\n%s", before, after)
	}
	prefixTokens := tokenCountOfPrefix(pre)
	for _, tc := range []struct {
		name    string
		read    int
		matched bool
		want    bool
	}{
		{"no read", 0, true, false},
		{"below static", prefixTokens - 1, true, false},
		{"static only", prefixTokens, true, false},
		{"beyond matching history", prefixTokens + 1, matched, true},
		{"beyond mismatched history", prefixTokens + 1, false, false},
	} {
		if got := historyHitPasses(tc.read, prefixTokens, tc.matched); got != tc.want {
			t.Errorf("%s: historyHitPasses(%d, %d, %v) = %v, want %v (SC-001)", tc.name, tc.read, prefixTokens, tc.matched, got, tc.want)
		}
	}
	if secondsInPrefix.MatchString(pre) {
		t.Errorf("pre-history prefix still contains a seconds clock, so a history hit is impossible:\n%s", pre)
	}
}

func prefixBeforeHistory(messages []providers.Message, historyContent string) (string, error) {
	end, err := historyBoundary(messages, "user", historyContent)
	if err != nil {
		return "", err
	}
	return prefixBytes(messages[:end])
}

// tokenCountOfPrefix is the pre-history size the fake proof compares against.
// The spec compares cache-read tokens with that prefix. Until a named estimator
// is recorded, the comparison uses the prefix's rune count only as the size
// the proof is given, not as the product's estimator.
func tokenCountOfPrefix(prefix string) int {
	if prefix == "" {
		return 0
	}
	return len([]rune(prefix))
}

// historyHitPasses is the SC-001 rule: the read must be greater than the
// pre-history prefix, and the captured prefix must have matched.
func historyHitPasses(read, prefixTokens int, prefixMatched bool) bool {
	return prefixMatched && read > prefixTokens
}

type cacheOptProvider struct {
	last     []providers.Message
	lastOpts map[string]any
}

func (p *cacheOptProvider) Chat(
	_ context.Context,
	messages []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	opts map[string]any,
) (*providers.LLMResponse, error) {
	p.last = append([]providers.Message(nil), messages...)
	p.lastOpts = opts
	return &providers.LLMResponse{Content: "Mock response"}, nil
}

func (p *cacheOptProvider) GetDefaultModel() string { return "mock-model" }
