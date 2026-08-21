package agent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
)

// msgUser creates a user message.
func msgUser(content string) providers.Message {
	return providers.Message{Role: "user", Content: content}
}

// msgAssistant creates a plain assistant message (no tool calls).
func msgAssistant(content string) providers.Message {
	return providers.Message{Role: "assistant", Content: content}
}

// msgAssistantTC creates an assistant message with tool calls.
func msgAssistantTC(toolIDs ...string) providers.Message {
	tcs := make([]providers.ToolCall, len(toolIDs))
	for i, id := range toolIDs {
		tcs[i] = providers.ToolCall{
			ID:   id,
			Type: "function",
			Name: "tool_" + id,
			Function: &providers.FunctionCall{
				Name:      "tool_" + id,
				Arguments: `{"key":"value"}`,
			},
		}
	}
	return providers.Message{Role: "assistant", ToolCalls: tcs}
}

// msgTool creates a tool result message.
func msgTool(callID, content string) providers.Message {
	return providers.Message{Role: "tool", ToolCallID: callID, Content: content}
}

func TestParseTurnBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		history []providers.Message
		want    []int
	}{
		{
			name:    "empty history",
			history: nil,
			want:    nil,
		},
		{
			name: "simple exchange",
			history: []providers.Message{
				msgUser("q1"),
				msgAssistant("a1"),
				msgUser("q2"),
				msgAssistant("a2"),
			},
			want: []int{0, 2},
		},
		{
			name: "tool-call Turn",
			history: []providers.Message{
				msgUser("search"),
				msgAssistantTC("tc1"),
				msgTool("tc1", "result"),
				msgAssistant("found it"),
				msgUser("thanks"),
				msgAssistant("welcome"),
			},
			want: []int{0, 4},
		},
		{
			name: "chained tool calls in single Turn",
			history: []providers.Message{
				msgUser("save and notify"),
				msgAssistantTC("tc_save"),
				msgTool("tc_save", "saved"),
				msgAssistantTC("tc_notify"),
				msgTool("tc_notify", "notified"),
				msgAssistant("done"),
			},
			want: []int{0},
		},
		{
			name: "no user messages",
			history: []providers.Message{
				msgAssistant("a1"),
				msgAssistant("a2"),
			},
			want: nil,
		},
		{
			name: "leading non-user messages",
			history: []providers.Message{
				msgAssistantTC("tc1"),
				msgTool("tc1", "r1"),
				msgAssistant("greeting"),
				msgUser("hello"),
				msgAssistant("hi"),
			},
			want: []int{3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTurnBoundaries(tt.history)
			if len(got) != len(tt.want) {
				t.Errorf("parseTurnBoundaries() = %v, want %v", got, tt.want)
				return
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseTurnBoundaries()[%d] = %d, want %d", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestEstimateMessageTokens(t *testing.T) {
	tests := []struct {
		name string
		msg  providers.Message
		want int // minimum expected tokens (exact value depends on overhead)
	}{
		{
			name: "plain user message",
			msg:  msgUser("Hello, world!"),
			want: 1, // at least some tokens
		},
		{
			name: "empty message still has overhead",
			msg:  providers.Message{Role: "user"},
			want: 1, // message overhead alone
		},
		{
			name: "assistant with tool calls",
			msg:  msgAssistantTC("tc_123"),
			want: 1,
		},
		{
			name: "tool result with ID",
			msg:  msgTool("call_abc", "Here is the search result with lots of content"),
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateMessageTokens(tt.msg)
			if got < tt.want {
				t.Errorf("estimateMessageTokens() = %d, want >= %d", got, tt.want)
			}
		})
	}
}

func TestEstimateMessageTokens_ToolCallsContribute(t *testing.T) {
	plain := msgAssistant("thinking")
	withTC := providers.Message{
		Role:    "assistant",
		Content: "thinking",
		ToolCalls: []providers.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Name: "search_web",
				Function: &providers.FunctionCall{
					Name:      "search_web",
					Arguments: `{"query":"omnipus agent framework","max_results":5}`,
				},
			},
		},
	}

	plainTokens := estimateMessageTokens(plain)
	withTCTokens := estimateMessageTokens(withTC)

	if withTCTokens <= plainTokens {
		t.Errorf("message with ToolCalls (%d tokens) should exceed plain message (%d tokens)",
			withTCTokens, plainTokens)
	}
}

func TestEstimateMessageTokens_MultibyteContent(t *testing.T) {
	// Multi-byte characters (e.g. emoji, accented letters) are single runes
	// but may map to different token counts. The heuristic should still produce
	// reasonable estimates via RuneCountInString.
	msg := msgUser("caf\u00e9 na\u00efve r\u00e9sum\u00e9 \u00fcber stra\u00dfe")
	tokens := estimateMessageTokens(msg)
	if tokens <= 0 {
		t.Errorf("multibyte message should produce positive token count, got %d", tokens)
	}
}

func TestEstimateMessageTokens_LargeArguments(t *testing.T) {
	// Simulate a tool call with large JSON arguments.
	largeArgs := fmt.Sprintf(`{"content":"%s"}`, strings.Repeat("x", 5000))
	msg := providers.Message{
		Role: "assistant",
		ToolCalls: []providers.ToolCall{
			{
				ID:   "call_large",
				Type: "function",
				Name: "write_file",
				Function: &providers.FunctionCall{
					Name:      "write_file",
					Arguments: largeArgs,
				},
			},
		},
	}

	tokens := estimateMessageTokens(msg)
	// 5000+ chars → at least 2000 tokens with the 2.5 char/token heuristic
	if tokens < 2000 {
		t.Errorf("large tool call arguments should produce significant token count, got %d", tokens)
	}
}

func TestEstimateMessageTokens_ReasoningContent(t *testing.T) {
	plain := msgAssistant("result")
	withReasoning := providers.Message{
		Role:             "assistant",
		Content:          "result",
		ReasoningContent: strings.Repeat("thinking step ", 200),
	}

	plainTokens := estimateMessageTokens(plain)
	reasoningTokens := estimateMessageTokens(withReasoning)

	if reasoningTokens <= plainTokens {
		t.Errorf("message with ReasoningContent (%d tokens) should exceed plain message (%d tokens)",
			reasoningTokens, plainTokens)
	}
}

func TestEstimateMessageTokens_MediaItems(t *testing.T) {
	plain := msgUser("describe this")
	withMedia := providers.Message{
		Role:    "user",
		Content: "describe this",
		Media:   []string{"media://img1.png", "media://img2.png"},
	}

	plainTokens := estimateMessageTokens(plain)
	mediaTokens := estimateMessageTokens(withMedia)

	if mediaTokens <= plainTokens {
		t.Errorf("message with Media (%d tokens) should exceed plain message (%d tokens)",
			mediaTokens, plainTokens)
	}

	// Each media item should add exactly 256 tokens (not run through chars*2/5).
	expectedDelta := 256 * 2
	actualDelta := mediaTokens - plainTokens
	if actualDelta != expectedDelta {
		t.Errorf("2 media items should add %d tokens, got delta %d", expectedDelta, actualDelta)
	}
}

// --- estimateToolDefsTokens tests ---

func TestEstimateToolDefsTokens(t *testing.T) {
	tests := []struct {
		name string
		defs []providers.ToolDefinition
		want int // minimum expected tokens
	}{
		{
			name: "empty tool list",
			defs: nil,
			want: 0,
		},
		{
			name: "single tool with params",
			defs: []providers.ToolDefinition{
				{
					Type: "function",
					Function: providers.ToolFunctionDefinition{
						Name:        "search_web",
						Description: "Search the web for information",
						Parameters: map[string]any{
							"type": "object",
							"properties": map[string]any{
								"query": map[string]any{"type": "string"},
							},
							"required": []any{"query"},
						},
					},
				},
			},
			want: 1,
		},
		{
			name: "tool without params",
			defs: []providers.ToolDefinition{
				{
					Type: "function",
					Function: providers.ToolFunctionDefinition{
						Name:        "list_directory",
						Description: "List directory contents",
					},
				},
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateToolDefsTokens(tt.defs)
			if got < tt.want {
				t.Errorf("estimateToolDefsTokens() = %d, want >= %d", got, tt.want)
			}
		})
	}
}

func TestEstimateToolDefsTokens_ScalesWithCount(t *testing.T) {
	makeTool := func(name string) providers.ToolDefinition {
		return providers.ToolDefinition{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        name,
				Description: "A test tool that does something useful",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"input": map[string]any{"type": "string", "description": "Input value"},
					},
				},
			},
		}
	}

	one := estimateToolDefsTokens([]providers.ToolDefinition{makeTool("tool_a")})
	three := estimateToolDefsTokens([]providers.ToolDefinition{
		makeTool("tool_a"), makeTool("tool_b"), makeTool("tool_c"),
	})

	if three <= one {
		t.Errorf("3 tools (%d tokens) should exceed 1 tool (%d tokens)", three, one)
	}
}

// --- isOverContextBudget tests ---

func TestIsOverContextBudget(t *testing.T) {
	systemMsg := providers.Message{Role: "system", Content: strings.Repeat("x", 1000)}
	userMsg := msgUser("hello")
	smallHistory := []providers.Message{systemMsg, msgUser("q1"), msgAssistant("a1"), userMsg}

	tools := []providers.ToolDefinition{
		{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        "test_tool",
				Description: "A test tool",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	}

	tests := []struct {
		name          string
		contextWindow int
		messages      []providers.Message
		toolDefs      []providers.ToolDefinition
		maxTokens     int
		want          bool
	}{
		{
			name:          "within budget",
			contextWindow: 100000,
			messages:      smallHistory,
			toolDefs:      tools,
			maxTokens:     4096,
			want:          false,
		},
		{
			name:          "over budget with small window",
			contextWindow: 100, // very small window
			messages:      smallHistory,
			toolDefs:      tools,
			maxTokens:     4096,
			want:          true,
		},
		{
			name:          "large max_tokens eats budget",
			contextWindow: 2000,
			messages:      smallHistory,
			toolDefs:      tools,
			maxTokens:     1800, // leaves almost no room
			want:          true,
		},
		{
			name:          "empty messages within budget",
			contextWindow: 10000,
			messages:      nil,
			toolDefs:      nil,
			maxTokens:     4096,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOverContextBudget(tt.contextWindow, tt.messages, tt.toolDefs, tt.maxTokens)
			if got != tt.want {
				t.Errorf("isOverContextBudget() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- Tests reflecting actual session data shape ---
// Session history never contains system messages. The system prompt is
// built dynamically by BuildMessages. These tests use realistic history
// shapes: user/assistant/tool only, with tool chains and reasoning content.

func TestEstimateMessageTokens_WithReasoningAndMedia(t *testing.T) {
	// Message with all fields populated — mirrors what AddFullMessage stores.
	msg := providers.Message{
		Role:             "assistant",
		Content:          "Here is the analysis.",
		ReasoningContent: strings.Repeat("Let me think about this carefully. ", 50),
		ToolCalls: []providers.ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Name: "analyze",
				Function: &providers.FunctionCall{
					Name:      "analyze",
					Arguments: `{"data":"sample","depth":3}`,
				},
			},
		},
	}

	tokens := estimateMessageTokens(msg)

	// ReasoningContent alone is ~1700 chars → ~680 tokens.
	// Content + TC + overhead adds more. Should be well above 500.
	if tokens < 500 {
		t.Errorf("message with reasoning+toolcalls should have significant tokens, got %d", tokens)
	}

	// Compare without reasoning to ensure it's counted.
	msgNoReasoning := msg
	msgNoReasoning.ReasoningContent = ""
	tokensNoReasoning := estimateMessageTokens(msgNoReasoning)

	if tokens <= tokensNoReasoning {
		t.Errorf("reasoning content should add tokens: with=%d, without=%d", tokens, tokensNoReasoning)
	}
}

func TestIsOverContextBudget_RealisticSession(t *testing.T) {
	// Simulate what BuildMessages produces: system + session history + current user.
	// System message is built by BuildMessages, not stored in session.
	systemMsg := providers.Message{
		Role:    "system",
		Content: strings.Repeat("system prompt content ", 100),
	}
	sessionHistory := []providers.Message{
		msgUser("first question"),
		msgAssistant("first answer"),
		msgUser("use tool X"),
		{
			Role:    "assistant",
			Content: "I'll use tool X",
			ToolCalls: []providers.ToolCall{
				{
					ID: "tc1", Type: "function", Name: "tool_x",
					Function: &providers.FunctionCall{
						Name:      "tool_x",
						Arguments: `{"query":"test","verbose":true}`,
					},
				},
			},
		},
		{Role: "tool", Content: strings.Repeat("result data ", 200), ToolCallID: "tc1"},
		msgAssistant("Here are the results from tool X."),
	}
	currentUser := msgUser("follow up question")

	// Assemble as BuildMessages would.
	messages := make([]providers.Message, 0, 1+len(sessionHistory)+1)
	messages = append(messages, systemMsg)
	messages = append(messages, sessionHistory...)
	messages = append(messages, currentUser)

	tools := []providers.ToolDefinition{
		{
			Type: "function",
			Function: providers.ToolFunctionDefinition{
				Name:        "tool_x",
				Description: "A useful tool",
				Parameters:  map[string]any{"type": "object"},
			},
		},
	}

	// With a large context window, should be within budget.
	if isOverContextBudget(131072, messages, tools, 32768) {
		t.Error("realistic session should be within 131072 context window")
	}

	// With a tiny context window, should exceed budget.
	if !isOverContextBudget(500, messages, tools, 32768) {
		t.Error("realistic session should exceed 500 context window")
	}
}
