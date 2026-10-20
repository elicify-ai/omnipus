// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

// expanded_thinking_replay_test.go — CR-02 RED pack (prompt caching).
//
// Spec oracle (independent of the implementation): with a manual thinking
// level configured and the first reply carrying signed thinking plus
// redacted-thinking blocks, the provider's tool-turn contract requires the
// continuation request's assistant turn to begin with the SAME complete,
// unmodified thinking and redacted-thinking blocks, before the tool-use
// block. Sources: Anthropic "Thinking — preserving thinking blocks" and
// "Extended thinking — turn structure in manual mode", as cited in
// /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/caching-gate/code-reviewer.md
// (CR-02). A plain reasoning-text field is NOT sufficient: signatures and
// redacted blobs are opaque provider bytes that must survive the round trip
// byte-exact.
//
// The oracles below are FINAL WIRE only: the decoded HTTP request body the
// official adapter sends to the local fake endpoint. No internal carrier
// field name is referenced or assumed — the implementation may carry the
// blocks however it likes (private struct field, codec, anything) as long as
// the outgoing continuation request carries the exact original blocks, in
// order, ahead of the tool-use block. No paid request is made; the fixture
// signature is a fake sentinel the fake server echoes back.
//
// Expected RED reason on the pre-fix tree (never an import/compile error):
// the agent records the assistant round as text + tool calls only
// (loop_run_turn_response.go::recordToolCalls) and the adapter rebuilds
// assistant turns from text and tool-use blocks
// (pkg/providers/anthropic/provider.go::buildParams), so the continuation
// assistant turn carries NO thinking block and the exact-block assertions
// below fail with expected-vs-actual. GREEN requires the exact original
// blocks on the wire for BOTH the plain (non-streaming) and the streamed
// response path.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	anthropicprovider "github.com/elicify-ai/omnipus/pkg/providers/anthropic"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	// expandedThinkingAPIKeyRef is the credential-store env reference the
	// factory row reads (config.ModelConfig.APIKey resolves via the
	// environment; the factory demands a key for a cloud anthropic row).
	expandedThinkingAPIKeyRef = "EXPANDED_THINKING_PARITY_KEY"
	// expandedThinkingModel is a real catalog row model under "anthropic" so
	// provider/model resolution and pricing lookups stay on catalog data.
	expandedThinkingModel = "claude-sonnet-4-5"

	// The fake server's scripted tool call and the sentinels it returns. The
	// continuation must carry these values back byte-exact.
	expandedThinkingCallID      = "expanded-call-1"
	expandedThinkingText        = "expanded-thinking-oracle-line"
	expandedThinkingSignature   = "expanded-signature-oracle-sentinel"
	expandedThinkingRedacted    = "expanded-redacted-oracle-sentinel"
	expandedThinkingArgsPath    = "expanded-oracle.md"
	expandedThinkingToolResult  = "expanded-oracle-result"
	expandedThinkingFinalAnswer = "expanded-final-answer"
)

// expandedThinkingOracleTool replaces read_file in the fixture agent's
// registry. read_file is a full-manifest-tier builtin, so an allowed entry is
// genuinely offered in the request's tool defs and the loop's offered-tool
// gate lets the scripted call through. It counts executions: the tool must
// run exactly once, and its result must be the one paired with the scripted
// call on the continuation.
type expandedThinkingOracleTool struct {
	runCount atomic.Int64
}

func (t *expandedThinkingOracleTool) Name() string { return "read_file" }
func (t *expandedThinkingOracleTool) Description() string {
	return "expanded thinking oracle fixture tool"
}
func (t *expandedThinkingOracleTool) Parameters() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *expandedThinkingOracleTool) Scope() tools.ToolScope { return tools.ScopeGeneral }
func (t *expandedThinkingOracleTool) Category() tools.ToolCategory {
	return tools.CategoryCore
}

func (t *expandedThinkingOracleTool) Execute(context.Context, map[string]any) *tools.ToolResult {
	t.runCount.Add(1)
	return tools.SilentResult(expandedThinkingToolResult)
}

// expandedThinkingServer is the local fake Messages endpoint. It records every
// request body (the final wire under assertion), answers the scripted
// tool-call turn first and the final text turn second, and refuses any third
// call so an unexpected extra round fails loudly instead of looping.
type expandedThinkingServer struct {
	mu       sync.Mutex
	t        *testing.T
	streamed bool
	bodies   []map[string]any
}

func (s *expandedThinkingServer) handler(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "fixture: undecodable request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	index := len(s.bodies)
	s.bodies = append(s.bodies, body)
	s.mu.Unlock()

	if index > 1 {
		s.t.Errorf("fixture: unexpected provider call #%d; the scripted turn is exactly two calls", index+1)
		http.Error(w, "fixture: no such scripted turn", http.StatusInternalServerError)
		return
	}

	if s.streamed {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(expandedThinkingStreamEvents(index)))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(expandedThinkingMessageJSON(index)))
}

// expandedThinkingMessageJSON is the non-streaming reply for call index.
// Call 0: signed thinking + redacted thinking + tool_use (stop_reason
// tool_use). Call 1: the final text answer (stop_reason end_turn).
func expandedThinkingMessageJSON(call int) string {
	if call == 0 {
		return fmt.Sprintf(`{"id":"msg_expanded_1","type":"message","role":"assistant","model":%q,`+
			`"content":[`+
			`{"type":"thinking","thinking":%q,"signature":%q},`+
			`{"type":"redacted_thinking","data":%q},`+
			`{"type":"tool_use","id":%q,"name":"read_file","input":{"path":%q}}],`+
			`"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`,
			expandedThinkingModel, expandedThinkingText, expandedThinkingSignature,
			expandedThinkingRedacted, expandedThinkingCallID, expandedThinkingArgsPath)
	}
	return fmt.Sprintf(`{"id":"msg_expanded_2","type":"message","role":"assistant","model":%q,`+
		`"content":[{"type":"text","text":%q}],"stop_reason":"end_turn",`+
		`"usage":{"input_tokens":4,"output_tokens":3}}`,
		expandedThinkingModel, expandedThinkingFinalAnswer)
}

// expandedThinkingStreamEvents is the SSE equivalent of
// expandedThinkingMessageJSON, in the exact event shapes the official
// adapter's accumulator consumes (mirroring the in-tree thinking/tool-use
// stream fixtures).
func expandedThinkingStreamEvents(call int) string {
	if call == 0 {
		return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_expanded_1\"," +
			"\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"" + expandedThinkingModel + "\"," +
			"\"stop_reason\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0," +
			"\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0," +
			"\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"" + expandedThinkingText + "\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0," +
			"\"delta\":{\"type\":\"signature_delta\",\"signature\":\"" + expandedThinkingSignature + "\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1," +
			"\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"" + expandedThinkingRedacted + "\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":2," +
			"\"content_block\":{\"type\":\"tool_use\",\"id\":\"" + expandedThinkingCallID + "\"," +
			"\"name\":\"read_file\",\"input\":{}}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2," +
			"\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"" + expandedThinkingArgsPath + "\\\"}\"}}\n\n" +
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"}," +
			"\"usage\":{\"output_tokens\":5}}\n\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	}
	return "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_expanded_2\"," +
		"\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"" + expandedThinkingModel + "\"," +
		"\"stop_reason\":null,\"usage\":{\"input_tokens\":4,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0," +
		"\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0," +
		"\"delta\":{\"type\":\"text_delta\",\"text\":\"" + expandedThinkingFinalAnswer + "\"}}\n\n" +
		"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}," +
		"\"usage\":{\"output_tokens\":3}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
}

// expandedThinkingStreamer / expandedThinkingStreamDelegate make the loop's
// streaming branch engage (runProviderAttempt streams when the bus returns a
// streamer for the channel/chat). A scheduled session is not a main
// connector turn, so with a delegate installed the real ChatStream path runs.
type expandedThinkingStreamer struct{}

func (s *expandedThinkingStreamer) Update(context.Context, string) error { return nil }
func (s *expandedThinkingStreamer) Finalize(context.Context, string) error {
	return nil
}
func (s *expandedThinkingStreamer) Cancel(context.Context) {}

type expandedThinkingStreamDelegate struct {
	streamer *expandedThinkingStreamer
}

func (d *expandedThinkingStreamDelegate) GetStreamer(context.Context, string, string, string) (bus.Streamer, bool) {
	return d.streamer, true
}

// expandedThinkingFixture is one wired turn: factory-created official adapter
// on a fake endpoint, a real agent loop, a real tool execution, a real
// session store — only the model HTTP boundary is fake.
type expandedThinkingFixture struct {
	al      *AgentLoop
	server  *expandedThinkingServer
	tool    *expandedThinkingOracleTool
	session string
}

func newExpandedThinkingFixture(t *testing.T, streamed bool) *expandedThinkingFixture {
	t.Helper()

	t.Setenv(expandedThinkingAPIKeyRef, "expanded-fixture-key")
	server := &expandedThinkingServer{t: t, streamed: streamed}
	httpSrv := httptest.NewServer(http.HandlerFunc(server.handler))
	t.Cleanup(httpSrv.Close)

	// The dispatch requirement: the provider under test is created by the
	// REAL factory from a config row, so the official adapter's
	// SupportsThinking()==true is what arms the thinking_level option flow.
	provider, modelID, err := providers.CreateProviderFromConfig(&config.ModelConfig{
		Provider:       "anthropic",
		Model:          expandedThinkingModel,
		APIBase:        httpSrv.URL, // unrelated local endpoint; row stays anthropic-protocol
		APIKeyRef:      expandedThinkingAPIKeyRef,
		RequestTimeout: 30,
	})
	if err != nil {
		t.Fatalf("fixture: CreateProviderFromConfig: %v", err)
	}
	if modelID != expandedThinkingModel {
		t.Fatalf("fixture: factory model id = %q, want %q (verbatim)", modelID, expandedThinkingModel)
	}
	if _, ok := provider.(*anthropicprovider.Provider); !ok {
		t.Fatalf("fixture: factory did not construct the official Anthropic adapter (got %T)", provider)
	}

	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	cfg := &config.Config{}
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Provider: "anthropic", Model: expandedThinkingModel}
	cfg.Agents.Defaults.MaxTokens = 8192 // comfortably above any low thinking budget
	cfg.Agents.Defaults.MaxToolIterations = 5
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	ag := registerAgent(t, al, home, "mia", provider)
	ag.mu.Lock()
	ag.Candidates = []providers.FallbackCandidate{{Provider: "anthropic", Model: expandedThinkingModel}}
	ag.ThinkingLevel = ThinkingLow
	ag.mu.Unlock()
	ag.StoreProviderPool(map[string]providers.LLMProvider{"anthropic": provider})

	tool := &expandedThinkingOracleTool{}
	ag.Tools.RegisterReplacing(tool)
	ag.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"read_file": config.ToolPolicyAllow},
	})

	if streamed {
		msgBus.SetStreamDelegate(&expandedThinkingStreamDelegate{streamer: &expandedThinkingStreamer{}})
	}

	meta, err := al.GetSessionStore().NewScheduledSession("mia")
	if err != nil {
		t.Fatalf("fixture: NewScheduledSession: %v", err)
	}
	return &expandedThinkingFixture{al: al, server: server, tool: tool, session: meta.ID}
}

// expandedThinkingRequireEnabledThinking pins the premise both calls must
// satisfy for the provider contract to govern: the request is a manual
// thinking request (thinking enabled, positive budget below max_tokens).
// Expected values come from the thinking_level option contract
// (prepareLLMRequest -> applyThinkingConfig), not from observed output.
func expandedThinkingRequireEnabledThinking(t *testing.T, body map[string]any, label string) {
	t.Helper()
	rawThinking, ok := body["thinking"].(map[string]any)
	if !ok {
		t.Fatalf("[%s] request carries no thinking parameter; the manual-thinking "+
			"continuation contract does not even apply, so this fixture cannot prove "+
			"block preservation. Fix the fixture, not the assertion. body=%v", label, body)
	}
	if rawThinking["type"] != "enabled" {
		t.Fatalf("[%s] thinking.type = %v, want \"enabled\"", label, rawThinking["type"])
	}
	budget, ok := rawThinking["budget_tokens"].(float64)
	if !ok || budget <= 0 {
		t.Fatalf("[%s] thinking.budget_tokens = %v, want a positive number", label, rawThinking["budget_tokens"])
	}
	maxTokens, ok := body["max_tokens"].(float64)
	if !ok || maxTokens <= 0 {
		t.Fatalf("[%s] max_tokens = %v, want a positive number", label, body["max_tokens"])
	}
	if budget >= maxTokens {
		t.Fatalf("[%s] thinking.budget_tokens %v is not < max_tokens %v", label, budget, maxTokens)
	}
}

// expandedThinkingToolTurnBlock extracts the continuation wire facts: the
// assistant content block list that carries the scripted tool_use, and the
// message that follows it. Fails loudly when the continuation has no such
// assistant turn at all.
func expandedThinkingToolTurnBlocks(t *testing.T, body map[string]any) (blocks []any, after any) {
	t.Helper()
	rawMsgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("continuation body has no messages array: %v", body)
	}
	for i, rm := range rawMsgs {
		msg, ok := rm.(map[string]any)
		if !ok || msg["role"] != "assistant" {
			continue
		}
		content, ok := msg["content"].([]any)
		if !ok {
			continue
		}
		for _, cb := range content {
			block, ok := cb.(map[string]any)
			if !ok || block["type"] != "tool_use" || block["id"] != expandedThinkingCallID {
				continue
			}
			if i+1 < len(rawMsgs) {
				return content, rawMsgs[i+1]
			}
			return content, nil
		}
	}
	raw, _ := json.Marshal(body)
	t.Fatalf("continuation request carries no assistant tool_use %q at all — "+
		"the scripted round never reached the wire as a tool turn. body=%s",
		expandedThinkingCallID, raw)
	return nil, nil
}

// runExpandedThinkingReplay drives the two-call turn and returns the captured
// continuation body for the transport-specific assertions.
func runExpandedThinkingReplay(t *testing.T, fx *expandedThinkingFixture) map[string]any {
	t.Helper()
	reply, turnErr := fx.al.ProcessScheduled(
		context.Background(), "mia", fx.session, "expanded: use the tool", "scheduled", fx.session)
	if turnErr != nil {
		t.Fatalf("real two-call turn failed: %v", turnErr)
	}
	if reply != expandedThinkingFinalAnswer {
		t.Fatalf("turn reply = %q, want %q — the continuation round did not complete", reply, expandedThinkingFinalAnswer)
	}

	fx.server.mu.Lock()
	defer fx.server.mu.Unlock()
	if got := len(fx.server.bodies); got != 2 {
		t.Fatalf("provider calls captured = %d, want exactly 2 (initial + continuation)", got)
	}
	if got := fx.tool.runCount.Load(); got != 1 {
		t.Fatalf("fixture tool executed %d times, want exactly 1", got)
	}
	return fx.server.bodies[1]
}

// TestExpandedThinking_SignedBlocksReplayOnToolContinuation_Plain is CR-02 on
// the non-streaming response path: the continuation request's assistant turn
// must carry the exact original signed thinking and redacted-thinking blocks,
// ahead of the tool-use block, byte-for-byte.
func TestExpandedThinking_SignedBlocksReplayOnToolContinuation_Plain(t *testing.T) {
	fx := newExpandedThinkingFixture(t, false)

	// Premise on the FIRST call too: thinking_level reached the provider, so
	// both requests are manual-thinking requests and the preservation contract
	// governs the continuation.
	fx.server.mu.Lock()
	if len(fx.server.bodies) != 0 {
		fx.server.mu.Unlock()
		t.Fatal("fixture: requests captured before the turn ran")
	}
	fx.server.mu.Unlock()

	body := runExpandedThinkingReplay(t, fx)
	expandedThinkingRequireEnabledThinking(t, body, "continuation")

	blocks, after := expandedThinkingToolTurnBlocks(t, body)
	if len(blocks) != 3 {
		raw, _ := json.Marshal(blocks)
		t.Fatalf("continuation assistant turn has %d content blocks, want exactly 3 "+
			"[thinking, redacted_thinking, tool_use]; blocks=%s", len(blocks), raw)
	}

	thinking, ok := blocks[0].(map[string]any)
	if !ok || thinking["type"] != "thinking" {
		t.Fatalf("continuation assistant block[0] = %v, want the thinking block FIRST "+
			"(provider contract: the turn must begin with the thinking blocks)", blocks[0])
	}
	if thinking["thinking"] != expandedThinkingText {
		t.Errorf("continuation thinking text = %v, want the exact original %q (no rewrite)", thinking["thinking"], expandedThinkingText)
	}
	if thinking["signature"] != expandedThinkingSignature {
		t.Errorf("continuation thinking signature = %v, want the exact original sentinel %q "+
			"(a re-signed or dropped signature breaks the tool-turn contract)", thinking["signature"], expandedThinkingSignature)
	}

	redacted, ok := blocks[1].(map[string]any)
	if !ok || redacted["type"] != "redacted_thinking" {
		t.Fatalf("continuation assistant block[1] = %v, want the redacted_thinking block second", blocks[1])
	}
	if redacted["data"] != expandedThinkingRedacted {
		t.Errorf("continuation redacted data = %v, want the exact original sentinel %q", redacted["data"], expandedThinkingRedacted)
	}

	toolUse, ok := blocks[2].(map[string]any)
	if !ok || toolUse["type"] != "tool_use" {
		t.Fatalf("continuation assistant block[2] = %v, want the tool_use block last", blocks[2])
	}
	if toolUse["id"] != expandedThinkingCallID || toolUse["name"] != "read_file" {
		t.Errorf("continuation tool_use id/name = %v/%v, want %q/read_file", toolUse["id"], toolUse["name"], expandedThinkingCallID)
	}
	args, _ := toolUse["input"].(map[string]any)
	if args["path"] != expandedThinkingArgsPath {
		t.Errorf("continuation tool_use input = %v, want {\"path\":%q} verbatim", toolUse["input"], expandedThinkingArgsPath)
	}

	// The executed tool's result is paired with the replayed call.
	rawAfter, err := json.Marshal(after)
	if err != nil {
		t.Fatalf("marshal continuation tail: %v", err)
	}
	if !strings.Contains(string(rawAfter), expandedThinkingCallID) || !strings.Contains(string(rawAfter), expandedThinkingToolResult) {
		t.Errorf("message after the replayed assistant turn = %s, want the tool result paired to %q with content %q",
			rawAfter, expandedThinkingCallID, expandedThinkingToolResult)
	}
}

// TestExpandedThinking_SignedBlocksReplayOnToolContinuation_Streamed is CR-02
// on the streamed response path (ChatStream): the same continuation
// assertions, with the first reply delivered as an SSE event stream.
func TestExpandedThinking_SignedBlocksReplayOnToolContinuation_Streamed(t *testing.T) {
	fx := newExpandedThinkingFixture(t, true)

	body := runExpandedThinkingReplay(t, fx)
	expandedThinkingRequireEnabledThinking(t, body, "streamed continuation")

	blocks, after := expandedThinkingToolTurnBlocks(t, body)
	if len(blocks) != 3 {
		raw, _ := json.Marshal(blocks)
		t.Fatalf("streamed-path continuation assistant turn has %d content blocks, want exactly 3 "+
			"[thinking, redacted_thinking, tool_use]; blocks=%s", len(blocks), raw)
	}

	thinking, ok := blocks[0].(map[string]any)
	if !ok || thinking["type"] != "thinking" {
		t.Fatalf("streamed-path continuation block[0] = %v, want the thinking block FIRST", blocks[0])
	}
	if thinking["thinking"] != expandedThinkingText {
		t.Errorf("streamed-path continuation thinking text = %v, want exact original %q", thinking["thinking"], expandedThinkingText)
	}
	if thinking["signature"] != expandedThinkingSignature {
		t.Errorf("streamed-path continuation signature = %v, want exact original sentinel %q", thinking["signature"], expandedThinkingSignature)
	}

	redacted, ok := blocks[1].(map[string]any)
	if !ok || redacted["type"] != "redacted_thinking" {
		t.Fatalf("streamed-path continuation block[1] = %v, want the redacted_thinking block second", blocks[1])
	}
	if redacted["data"] != expandedThinkingRedacted {
		t.Errorf("streamed-path continuation redacted data = %v, want exact original sentinel %q", redacted["data"], expandedThinkingRedacted)
	}

	toolUse, ok := blocks[2].(map[string]any)
	if !ok || toolUse["type"] != "tool_use" {
		t.Fatalf("streamed-path continuation block[2] = %v, want the tool_use block last", blocks[2])
	}
	if toolUse["id"] != expandedThinkingCallID || toolUse["name"] != "read_file" {
		t.Errorf("streamed-path continuation tool_use id/name = %v/%v, want %q/read_file", toolUse["id"], toolUse["name"], expandedThinkingCallID)
	}

	rawAfter, err := json.Marshal(after)
	if err != nil {
		t.Fatalf("marshal streamed continuation tail: %v", err)
	}
	if !strings.Contains(string(rawAfter), expandedThinkingCallID) || !strings.Contains(string(rawAfter), expandedThinkingToolResult) {
		t.Errorf("streamed-path message after the replayed turn = %s, want the tool result paired to %q with content %q",
			rawAfter, expandedThinkingCallID, expandedThinkingToolResult)
	}
}
