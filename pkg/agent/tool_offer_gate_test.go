// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Regression coverage for the offered-tool gate (tool_offer_gate.go, wired in
// runTurn's per-tool loop) and for the "More tools" manifest note being left
// off an ADR-081 D3 narrowed request.
//
// UAT B-10 run 1: on the narrowed goal request that offered only set_goal and
// AskUserQuestion, a ToolSearch call still executed. The loop resolved every
// call against the agent's whole registry and whole-turn policy map, never
// against what the request offered, and the compressed manifest note on that
// same request told the model to "call ToolSearch".
package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// manifestNoteHeader is the first line BuildCompressedManifest writes.
const manifestNoteHeader = "# More tools (load before use)"

// offerGateCaptureProvider returns scripted responses in order (then a plain
// "done") and records each request's messages and tool definitions.
type offerGateCaptureProvider struct {
	mu        sync.Mutex
	responses []*providers.LLMResponse
	msgs      [][]providers.Message
	defs      [][]providers.ToolDefinition
}

func (p *offerGateCaptureProvider) Chat(
	_ context.Context, messages []providers.Message, toolDefs []providers.ToolDefinition, _ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := len(p.msgs)
	p.msgs = append(p.msgs, append([]providers.Message(nil), messages...))
	p.defs = append(p.defs, append([]providers.ToolDefinition(nil), toolDefs...))
	if idx < len(p.responses) {
		return p.responses[idx], nil
	}
	return &providers.LLMResponse{Content: "done"}, nil
}

func (p *offerGateCaptureProvider) GetDefaultModel() string { return "offer-gate-capture" }

func (p *offerGateCaptureProvider) request(n int) ([]providers.Message, []providers.ToolDefinition, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 0 || n >= len(p.msgs) {
		return nil, nil, false
	}
	return p.msgs[n], p.defs[n], true
}

// toolResultFor returns the content of the tool-role message answering callID.
func toolResultFor(msgs []providers.Message, callID string) (string, bool) {
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == callID {
			return m.Content, true
		}
	}
	return "", false
}

func scriptedToolCall(id, name, args string) providers.ToolCall {
	return providers.ToolCall{
		ID: id, Type: "function", Name: name,
		Function: &providers.FunctionCall{Name: name, Arguments: args},
	}
}

// TestToolNotOfferedMessage_PreviewVsSearchOnlyWording pins the refusal text
// for the two lazy-tier visibility classes (pkg/tools/manifest.go's
// ManifestVisibility). BuildCompressedManifest only ever renders a "More
// tools" preview line for a ManifestPreviewed (Tier 2) name — a
// ManifestSearchOnly (Tier 3) name, e.g. AskUserQuestion/set_goal/
// find_skills, gets zero preview text. Before this fix toolNotOfferedMessage
// told the model "It is listed under More tools" for BOTH classes, which was
// false for Tier 3 and could send the model hunting a listing that never
// existed instead of just calling ToolSearch with the name it already has.
func TestToolNotOfferedMessage_PreviewVsSearchOnlyWording(t *testing.T) {
	t.Run("previewed lazy tool is told it is listed under More tools", func(t *testing.T) {
		require.Equal(t, tools.ManifestPreviewed, tools.ToolManifestVisibility("bash"),
			"test precondition: bash must be a Tier 2 previewed lazy tool")
		msg := toolNotOfferedMessage("bash", goalForcingDecision{}, true)
		assert.Contains(t, msg, "It is listed under More tools", "bash: %q", msg)
		assert.Contains(t, msg, "load it with ToolSearch first", "bash: %q", msg)
	})

	t.Run("search-only lazy tool is NOT told it is listed under More tools", func(t *testing.T) {
		require.Equal(t, tools.ManifestSearchOnly, tools.ToolManifestVisibility("find_skills"),
			"test precondition: find_skills must be a Tier 3 search-only lazy tool")
		msg := toolNotOfferedMessage("find_skills", goalForcingDecision{}, true)
		assert.NotContains(t, msg, "It is listed under More tools",
			"find_skills has zero preview text in the manifest block — telling the model it is "+
				"\"listed under More tools\" is false, got %q", msg)
		assert.Contains(t, msg, "ToolSearch", "must still point the model at ToolSearch, got %q", msg)
		assert.Contains(t, msg, `"find_skills"`, "must name the exact tool to load, got %q", msg)
	})
}

func TestGoalTurn_NarrowedRequest_RefusesUnofferedToolsAndOmitsManifestNote(t *testing.T) {
	provider := &offerGateCaptureProvider{responses: []*providers.LLMResponse{{
		ToolCalls: []providers.ToolCall{
			scriptedToolCall("call-search", "ToolSearch", `{"names":["probe_lookup"]}`),
			scriptedToolCall("call-probe", "probe_lookup", `{}`),
		},
	}}}
	al, _ := newGoalLoopTestLoop(t, provider, func(cfg *config.Config) {
		cfg.Tools.Manifest.Compressed = true
		// Preview every lazy tool (ADR-071 §4.3.1(b) revert flag) so the
		// More tools note is guaranteed non-empty for this agent: without it
		// the stub probe_lookup is not preview-visible, the note renders as ""
		// and "no note on the narrowed request" would prove nothing.
		cfg.Tools.Manifest.PreviewAllLazy = true
	})
	agentInst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok, "native-agent not registered")

	probe := &fixedResultTool{name: "probe_lookup", result: `{"found":true}`}
	al.RegisterTool(probe)
	agentInst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
		tools.SetGoalToolName:         config.ToolPolicyAllow,
		tools.AskUserQuestionToolName: config.ToolPolicyAllow,
		"ToolSearch":                  config.ToolPolicyAllow,
		"probe_lookup":                config.ToolPolicyAllow,
	}})
	store, sid := newGoalTestSession(t, al, agentInst.ID)
	setActiveGoalRecordless(t, store, sid, "goal-offer-gate-1", "organize my week")

	opts := processOptions{
		SessionKey: "goal-offer-gate-session", Channel: "webchat", ChatID: "c1",
		UserMessage:     "organize my week",
		TranscriptStore: store, TranscriptSessionID: sid,
		DefaultResponse: "done", UserInitiated: true,
	}
	ts := newTurnState(agentInst, opts, al.newTurnEventScope(agentInst.ID, opts.SessionKey))
	_, err := al.runTurn(context.Background(), ts)
	require.NoError(t, err)

	firstMsgs, firstDefs, ok := provider.request(0)
	require.True(t, ok, "provider was never called")
	offered := toolNamesOf(firstDefs)
	require.ElementsMatch(t, []string{tools.SetGoalToolName, tools.AskUserQuestionToolName}, offered,
		"test precondition: request 1 is the narrowed goal door")
	// Positive control: for this very turn state the note is non-empty, so its
	// absence below is the narrowed-request rule, not an empty manifest.
	policyFiltered, _ := tools.FilterToolsByPolicy(agentInst.Tools.GetAll(), agentInst.AgentType, agentInst.LoadToolPolicy())
	require.Contains(t, al.buildToolManifestNote(ts, policyFiltered), manifestNoteHeader,
		"test precondition: this agent has a non-empty More tools note to withhold")
	for _, m := range firstMsgs {
		assert.NotContains(t, m.Content, manifestNoteHeader,
			"the narrowed request must not carry the More tools note that tells the model to call ToolSearch")
	}

	assert.Equal(t, int32(0), probe.calls.Load(), "a tool the narrowed request did not offer must not run")
	assert.False(t, al.sessionLoadedTools(ts.manifestBucket())["probe_lookup"],
		"the un-offered ToolSearch call must not have run and loaded anything")

	secondMsgs, _, ok := provider.request(1)
	require.True(t, ok, "the turn must continue to a second request after the refusals")
	for _, callID := range []string{"call-search", "call-probe"} {
		content, found := toolResultFor(secondMsgs, callID)
		require.True(t, found, "call %s must have a paired tool result", callID)
		assert.Contains(t, content, "is not available in this request", "call %s", callID)
		assert.Contains(t, content, "register the goal with set_goal", "call %s", callID)
	}
}

func TestCompressedRequest_UnloadedLazyToolRefusedUntilToolSearchLoadsIt(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// find_skills is ManifestLazy and policy-allowed for the seeded default
	// agent (load_tool_e2e_test.go uses the same tool for the same reason).
	const lazyTool = "find_skills"
	scenario := testutil.NewScenario().
		WithToolCalls([]providers.ToolCall{scriptedToolCall("direct-1", lazyTool, `{"query":"test"}`)}).
		WithToolCalls([]providers.ToolCall{
			scriptedToolCall("load-1", "ToolSearch", `{"names":["find_skills"]}`),
			scriptedToolCall("after-load-1", lazyTool, `{"query":"test"}`),
		}).
		WithText("done")
	recorder := newPerCallRecorder(scenario)

	al := mustNewAgentLoop(t, newE2ECfg(t, workspaceDir), bus.NewMessageBus(), recorder)
	defer al.Close()
	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)
	realTool, ok := defaultAgent.Tools.Get(lazyTool)
	require.True(t, ok, "test precondition: %s must be registered", lazyTool)
	require.Equal(t, tools.ManifestLazy, tools.ToolManifestTier(lazyTool), "test precondition: %s is lazy", lazyTool)
	counter := &countingPassthroughTool{Tool: realTool}
	al.RegisterTool(counter)

	reply, err := al.ProcessDirectWithChannel(context.Background(), "find some skills", "offer-gate-lazy", "cli", "test")
	require.NoError(t, err, "reply=%q", reply)
	require.Equal(t, 3, recorder.CallCount(), "three scripted provider rounds")

	afterDirect := recorder.MessagesForCall(1)
	content, found := toolResultFor(afterDirect, "direct-1")
	require.True(t, found, "the refused direct call must have a paired tool result")
	assert.Contains(t, content, "is not available in this request")
	// find_skills is ManifestSearchOnly (Tier 3, not in previewedLazyToolNames):
	// it renders zero preview text in the "More tools" block, so the refusal
	// must not claim it is listed there (see TestToolNotOfferedMessage_PreviewVsSearchOnlyWording)
	// — it must instead point the model straight at ToolSearch with the exact name.
	assert.NotContains(t, content, "It is listed under More tools")
	assert.Contains(t, content, "ToolSearch")
	assert.Contains(t, content, `"find_skills"`)

	assert.Equal(t, int32(1), counter.calls.Load(),
		"only the call made after ToolSearch loaded the tool (same response) may run; the direct call before it must not")
	final := recorder.MessagesForCall(2)
	afterLoad, found := toolResultFor(final, "after-load-1")
	require.True(t, found)
	assert.False(t, strings.Contains(afterLoad, "is not available in this request"),
		"a lazy tool loaded by ToolSearch earlier in the same response is callable (ADR-071), got %q", afterLoad)
}
