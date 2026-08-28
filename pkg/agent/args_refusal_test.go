// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// argsRecordingTool is a tool that accepts one "content" argument and
// records whether it ran — the oracle for "the tool did not execute".
type argsRecordingTool struct {
	tools.BaseTool
	calls atomic.Int32
}

func (a *argsRecordingTool) Name() string        { return "args_tool" }
func (a *argsRecordingTool) Description() string { return "records executions (T066-15)" }
func (a *argsRecordingTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"content": map[string]any{"type": "string"}}}
}
func (a *argsRecordingTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (a *argsRecordingTool) Execute(_ context.Context, _ map[string]any) *tools.ToolResult {
	a.calls.Add(1)
	return &tools.ToolResult{ForLLM: "args_tool executed"}
}

// argsJSONOfSize returns `{"content":"aaa…"}` whose json.Marshal form is
// exactly size characters — the bound is defined on the SERIALISED
// arguments, so the fixture targets that, not the raw string.
func argsJSONOfSize(t *testing.T, size int) string {
	t.Helper()
	const frame = len(`{"content":""}`)
	require.Greater(t, size, frame)
	s := `{"content":"` + strings.Repeat("a", size-frame) + `"}`
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(s), &m))
	encoded, err := json.Marshal(m)
	require.NoError(t, err)
	require.Len(t, encoded, size, "fixture defect: serialised size must be exact")
	return s
}

// runArgsTurn drives one real ProcessScheduled turn whose first LLM response
// calls args_tool with argsJSON, then says "done".
type argsTurn struct {
	al        *AgentLoop
	agent     *AgentInstance
	provider  *testutil.ScenarioProvider
	stub      *argsRecordingTool
	sessionID string
	reply     string
}

func runArgsTurn(t *testing.T, capChars int, argsJSON string) argsTurn {
	t.Helper()
	al, home := schedTestLoop(t)
	if capChars > 0 {
		al.cfg.Context.BuiltinSuccessCap = capChars
	}
	provider := testutil.NewScenario().
		WithToolCall("args_tool", argsJSON).
		WithText("done")
	mia := registerAgent(t, al, home, "mia", provider, false)
	stub := &argsRecordingTool{}
	mia.Tools.Register(stub)
	mia.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"args_tool": "allow"}})

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	require.NoError(t, err)
	reply, err := al.ProcessScheduled(context.Background(), "mia", meta.ID, "use args_tool", "scheduled", meta.ID)
	require.NoError(t, err)
	return argsTurn{al: al, agent: mia, provider: provider, stub: stub, sessionID: meta.ID, reply: reply}
}

// toolMessagesOf returns the role:"tool" messages the provider saw on its
// LAST call — what the model reads after the dispatch decision.
func toolMessagesOf(p *testutil.ScenarioProvider) []providers.Message {
	var out []providers.Message
	for _, m := range p.LastMessages() {
		if m.Role == "tool" {
			out = append(out, m)
		}
	}
	return out
}

// TestRunTurn_ArgsRefusal_TurnContinues is spec test 35 (ADR-066 D4,
// FR-016, B-19, DS-3 #2, SC-005's second clause) plus the enforcement half
// of test 12: serialised arguments over the cap → the tool does not run, the
// model receives the ToolArgumentRefusal payload through the choke point as
// an ordinary tool result, and the loop proceeds to a further LLM call.
func TestRunTurn_ArgsRefusal_TurnContinues(t *testing.T) {
	for _, size := range []int{64_001, 300_000} {
		t.Run(fmt.Sprintf("%d refused", size), func(t *testing.T) {
			r := runArgsTurn(t, 0, argsJSONOfSize(t, size))
			provider, stub, reply := r.provider, r.stub, r.reply

			assert.Equal(t, int32(0), stub.calls.Load(), "the tool must not execute")
			assert.Equal(t, 2, provider.CallCount(), "the refusal is followed by a further LLM call")
			assert.Equal(t, "done", reply, "the turn continues to its natural end")

			toolMsgs := toolMessagesOf(provider)
			require.Len(t, toolMsgs, 1, "exactly one tool message — the refusal — reached the model")
			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(toolMsgs[0].Content), &payload),
				"the refusal must be the structured family payload, got %q", toolMsgs[0].Content)
			assert.Equal(t, tools.ToolArgumentsTooLargeCode, payload["error"])
			assert.Equal(t, "args_tool", payload["tool"])
			assert.EqualValues(t, size, payload["size_chars"])
			assert.EqualValues(t, config.DefaultBuiltinSuccessCap, payload["cap_chars"])
		})
	}

	// DS-3 #1 / B-20: exactly at the cap → executes.
	t.Run("64000 executes", func(t *testing.T) {
		r := runArgsTurn(t, 0, argsJSONOfSize(t, config.DefaultBuiltinSuccessCap))
		provider, stub, reply := r.provider, r.stub, r.reply
		assert.Equal(t, int32(1), stub.calls.Load(), "at the bound the tool runs")
		assert.Equal(t, 2, provider.CallCount())
		assert.Equal(t, "done", reply)
		toolMsgs := toolMessagesOf(provider)
		require.Len(t, toolMsgs, 1)
		assert.Equal(t, "args_tool executed", toolMsgs[0].Content)
	})

	// B-20 / US-5.AC2: a retry under the cap executes.
	t.Run("retry 10000 executes", func(t *testing.T) {
		r := runArgsTurn(t, 0, argsJSONOfSize(t, 10_000))
		provider, stub := r.provider, r.stub
		assert.Equal(t, int32(1), stub.calls.Load())
		assert.Equal(t, 2, provider.CallCount())
	})

	// US-5.AC3 / FR-009: the refusal passes the choke point like any result —
	// it is archived on the session like a real tool result.
	t.Run("refusal archived through the choke point", func(t *testing.T) {
		r := runArgsTurn(t, 0, argsJSONOfSize(t, 64_001))
		// ProcessScheduled keys the agent's session as agent:<id>:session:<sid>.
		history := r.agent.Sessions.GetHistory("agent:mia:session:" + r.sessionID)
		var archived *providers.Message
		for i := range history {
			if history[i].Role == "tool" {
				archived = &history[i]
			}
		}
		require.NotNil(t, archived, "the refusal must be archived as a tool line")
		assert.Contains(t, archived.Content, tools.ToolArgumentsTooLargeCode)
		assert.Equal(t, "args_tool-0", archived.ToolCallID, "re-paired with the call it answers")
	})

	// The cap is the LIVE builtin-success cap (the bound tracks it): with the
	// cap at 50,000 a 50,001-char argument set is refused quoting 50,000.
	t.Run("tracks live cap", func(t *testing.T) {
		r := runArgsTurn(t, 50_000, argsJSONOfSize(t, 50_001))
		provider, stub := r.provider, r.stub
		assert.Equal(t, int32(0), stub.calls.Load())
		toolMsgs := toolMessagesOf(provider)
		require.Len(t, toolMsgs, 1)
		var payload map[string]any
		require.NoError(t, json.Unmarshal([]byte(toolMsgs[0].Content), &payload))
		assert.EqualValues(t, 50_000, payload["cap_chars"])
		assert.EqualValues(t, 50_001, payload["size_chars"])
	})
}

// TestSerialisedToolArgsChars pins the measure: serialised JSON, in runes.
func TestSerialisedToolArgsChars(t *testing.T) {
	assert.Equal(t, 0, serialisedToolArgsChars(nil))
	assert.Equal(t, 0, serialisedToolArgsChars(map[string]any{}))
	assert.Equal(t, len(`{"a":"b"}`), serialisedToolArgsChars(map[string]any{"a": "b"}))
	assert.Equal(t, len([]rune(`{"a":"héé"}`)), serialisedToolArgsChars(map[string]any{"a": "héé"}))
	assert.Equal(t, 0, serialisedToolArgsChars(map[string]any{"c": make(chan int)}),
		"unmarshallable arguments are the tool's problem, not the bound's")
}
