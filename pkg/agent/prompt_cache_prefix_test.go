package agent

import (
	"context"
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

func prefixThrough(messages []providers.Message, lastContent string) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Role)
		b.WriteByte('\n')
		b.WriteString(msg.Content)
		b.WriteByte('\n')
		if msg.Content == lastContent {
			break
		}
	}
	return b.String()
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

	meta, err := al.sharedSessionStore.NewSession(session.SessionTypeChat, "webchat", "mia")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
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

	// Dataset row 1: clock plus about a second. Bytes through the prior turn match.
	before := prefixThrough(first, "TURN-ONE")
	after := prefixThrough(second, "Mock response")
	// The second request's prefix through the first assistant reply must equal
	// the first request through the same history. The clock moving is not allowed
	// to change those bytes.
	firstHistory := prefixThrough(first, "Mock response")
	if firstHistory != after {
		t.Errorf("bytes through prior history differ after the clock advanced\nfirst:\n%s\nsecond:\n%s", firstHistory, after)
	}
	_ = before

	assertVolatileTail(t, second, "SCRATCH-ONE")

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
	if prefixThrough(second, "Mock response") != prefixThrough(third, "TURN-TWO") &&
		!strings.Contains(messageTexts(third), "SCRATCH-TWO") {
		t.Errorf("scratchpad change was not confined to the tail\n%s", messageTexts(third))
	}
	assertVolatileTail(t, third, "SCRATCH-TWO")
}

func assertVolatileTail(t *testing.T, messages []providers.Message, scratch string) {
	t.Helper()
	var historyEnd int
	for i, msg := range messages {
		if msg.Role == "user" && (msg.Content == "TURN-ONE" || msg.Content == "TURN-TWO" || msg.Content == "TURN-THREE") {
			historyEnd = i
		}
	}
	for i, msg := range messages {
		if msg.Role == "system" && (secondsInPrefix.MatchString(msg.Content) || strings.Contains(msg.Content, "Current Time") || strings.Contains(msg.Content, scratch) || strings.Contains(msg.Content, skillReminderNote)) {
			t.Errorf("system message %d holds a volatile note (clock, scratchpad, or skill reminder); those sit in one user-role block after history", i)
		}
	}
	if historyEnd == 0 {
		t.Fatalf("history not found in:\n%s", messageTexts(messages))
	}
	var tail string
	for _, msg := range messages[historyEnd+1:] {
		if msg.Role == "user" {
			tail += msg.Content
		}
	}
	if !strings.Contains(tail, "Current Time") && !secondsInPrefix.MatchString(tail) {
		t.Errorf("clock is not in the user-role tail after history\n%s", messageTexts(messages))
	}
	if scratch != "" && !strings.Contains(tail, scratch) {
		t.Errorf("scratchpad %q is not in the user-role tail after history\n%s", scratch, messageTexts(messages))
	}
	for i, msg := range messages {
		if strings.Contains(msg.Content, "Rendering (web chat)") && i > historyEnd {
			t.Errorf("web-display note is after history; it stays in front")
		}
	}
}

// TestPrefixStable_AfterNormalizeAndAdapter is TDD row 22. The body that
// leaves the normalizer is what the provider receives. The tail is user-role
// and after history. A completed tool-call group is not split by that tail.
func TestPrefixStable_AfterNormalizeAndAdapter(t *testing.T) {
	tmp := setupWorkspace(t, map[string]string{"AGENT.md": "# Agent\nTest agent."})
	t.Cleanup(func() { os.RemoveAll(tmp) })
	cb := NewContextBuilder(tmp)
	history := []providers.Message{
		{Role: "user", Content: "PRIOR-USER"},
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{{
				ID:   "call_1",
				Name: "read",
				Arguments: map[string]any{
					"path": "a.txt",
				},
			}},
		},
		{Role: "tool", ToolCallID: "call_1", Content: "file-body"},
	}
	msgs := cb.BuildMessages(0, history, "current question", nil, "", "webchat", "chat-1", "user-1", "Alice", "", nil)
	assertToolGroupIntact(t, msgs)
	for _, msg := range msgs {
		if msg.Role == "system" && (secondsInPrefix.MatchString(msg.Content) || strings.Contains(msg.Content, "Current Time")) {
			t.Errorf("after BuildMessages the clock is still a system message in front of history")
		}
	}
	var tail string
	seenCurrent := false
	for _, msg := range msgs {
		if msg.Content == "current question" {
			seenCurrent = true
			continue
		}
		if seenCurrent && msg.Role == "user" {
			tail += msg.Content
		}
	}
	if !seenCurrent || !strings.Contains(tail, "Current Time") {
		t.Errorf("clock is not a user-role block after the conversation\n%s", messageTexts(msgs))
	}
}

func assertToolGroupIntact(t *testing.T, messages []providers.Message) {
	t.Helper()
	for i, msg := range messages {
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		if i+1 >= len(messages) || messages[i+1].Role != "tool" {
			t.Fatalf("tool call at %d is not followed by its result", i)
		}
		if strings.Contains(messages[i+1].Content, "Current Time") {
			t.Fatalf("volatile note inserted between a tool call and its result")
		}
		return
	}
	t.Fatal("history tool-call group was dropped")
}

// TestHistoryHit_StaticOnlyFails is TDD row 29. A cache read that covers only
// the pre-history prefix fails. A read beyond that prefix passes only when the
// captured prefix itself matched, which it does not while a seconds clock sits
// in front.
func TestHistoryHit_StaticOnlyFails(t *testing.T) {
	tmp := setupWorkspace(t, map[string]string{"AGENT.md": "# Agent\nStable."})
	t.Cleanup(func() { os.RemoveAll(tmp) })
	cb := NewContextBuilder(filepath.Clean(tmp))
	history := []providers.Message{{Role: "user", Content: "PRIOR-USER"}, {Role: "assistant", Content: "PRIOR-ASSISTANT"}}
	first := cb.BuildMessages(0, history, "one", nil, "", "test", "c", "u", "A", "", nil)
	time.Sleep(1100 * time.Millisecond)
	second := cb.BuildMessages(0, history, "two", nil, "", "test", "c", "u", "A", "", nil)

	pre := prefixBeforeHistory(first, "PRIOR-USER")
	matched := pre == prefixBeforeHistory(second, "PRIOR-USER")
	if !matched {
		t.Fatalf("captured prefixes differ after the clock advanced, so a history hit cannot pass\n%s", pre)
	}
	prefixTokens := tokenCountOfPrefix(pre)
	if historyHitPasses(prefixTokens, prefixTokens, matched) {
		t.Fatal("a cache read equal to the pre-history prefix was accepted; SC-001 says that read fails")
	}
	if !matched && historyHitPasses(prefixTokens+1, prefixTokens, matched) {
		t.Fatal("a read beyond the prefix passed even though the captured prefixes differ")
	}
	if matched && !historyHitPasses(prefixTokens+1, prefixTokens, matched) {
		t.Fatal("a read beyond a matching captured prefix was rejected")
	}
	if secondsInPrefix.MatchString(pre) {
		t.Fatalf("pre-history prefix still contains a seconds clock, so a history hit is impossible:\n%s", pre)
	}
}

func prefixBeforeHistory(messages []providers.Message, historyContent string) string {
	var b strings.Builder
	for _, msg := range messages {
		if strings.Contains(msg.Content, historyContent) {
			break
		}
		b.WriteString(msg.Role)
		b.WriteByte('\n')
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
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
