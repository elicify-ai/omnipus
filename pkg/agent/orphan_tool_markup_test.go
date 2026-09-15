// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// orphan_tool_markup_test.go — regression tests for the two separable defects
// a UAT run on openrouter → z-ai/glm-5.3 exposed.
//
// What happened live: the model emits tool calls as XML-ish markup in the
// completion TEXT and relies on the hosting provider to parse that back into
// `tool_calls`. When that upstream parse did not complete, the unconsumed
// REMAINDER of the markup arrived as `content` with zero parsed tool calls.
// Omnipus then (1) rendered the remainder to the user as if it were the
// assistant's own words, and (2) treated the round as a finished answer, so
// the turn ended with no tool having run, no error, and — where the whole
// response was markup — nothing on screen at all.
//
// The strings below are VERBATIM from
// build/uat-home/sessions/session_01M2CVMBJGKMGNXHBVYGDSF430/transcript.jsonl
// (role=assistant, content a plain string). They are the oracle: each is a
// real thing a real model sent, not a constructed approximation.
//
// Two tests, one per defect, deliberately kept apart:
//   - TestOrphanToolMarkup_NeverReachesTheUser      → the leak
//   - TestOrphanToolMarkup_NeverEndsTheTurnInSilence → the stall
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestOrphanToolMarkup' -p 1 ./pkg/agent/

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// uatOrphanToolMarkupLeaks are the four assistant messages captured live.
// Note the shape: the opening <tool_call> is never present — the upstream
// parser had already consumed it before giving up — so every one of these
// starts partway through a tool call.
var uatOrphanToolMarkupLeaks = []struct {
	name    string
	content string
}{
	{
		// Cut mid-argument: the value's first line ("one") never arrived.
		name:    "cut_mid_arg_value",
		content: "two\nthree\nfour\nfive\n</arg_value><arg_key>path</arg_key><arg_value>count.txt</arg_value></tool_call>",
	},
	{
		// Real prose, then the residue glued straight onto it. The prose is
		// the assistant genuinely talking and must survive; everything from
		// the first tag must not.
		name: "prose_then_residue",
		content: "The previous write was malformed and never landed. Writing the file properly now." +
			"content</arg_key><arg_value>one\ntwo\nthree\nfour\nfive\n</arg_value>" +
			"<arg_key>path</arg_key><arg_value>count.txt</arg_value></tool_call>",
	},
	{
		// Pure residue, opening with a closing tag and nothing else.
		name:    "closing_tag_first",
		content: "</arg_key><arg_value>count.txt</arg_value></tool_call>",
	},
	{
		// The 32,768-completion-token round: the model burned the entire
		// output cap and this is all that surfaced. This one was the final
		// assistant entry of its session — the turn never spoke again.
		name:    "tool_name_list_argument",
		content: `["write_file", "read_file", "edit_file", "list_directory"]</arg_value></tool_call>`,
	},
}

// orphanMarkupTagFragments are the substrings that must never appear in
// anything a user sees. Checked as raw fragments rather than via the
// production marker list so the assertion cannot be satisfied by editing the
// production list — the test would then still fail on the real bytes.
var orphanMarkupTagFragments = []string{
	"<tool_call>", "</tool_call>",
	"<arg_key>", "</arg_key>",
	"<arg_value>", "</arg_value>",
}

func assertNoToolMarkup(t *testing.T, label, text string) {
	t.Helper()
	for _, frag := range orphanMarkupTagFragments {
		assert.NotContains(t, text, frag,
			"%s must never carry raw tool-call markup %q; got: %q", label, frag, text)
	}
}

// newOrphanMarkupLoop wires a minimal agent loop around a scripted provider.
func newOrphanMarkupLoop(t *testing.T, provider providers.LLMProvider) (*AgentLoop, *AgentInstance) {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	inst := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, inst, "default agent must be registered")
	return al, inst
}

// ---------------------------------------------------------------------------
// Defect 1 — the leak
// ---------------------------------------------------------------------------

// TestOrphanToolMarkup_NeverReachesTheUser asserts that residual tool-call
// markup is never presented as the assistant's answer.
//
// BDD: Given a model that returns residual tool-call markup as text with no
// parsed tool calls,
// When the agent loop handles that round,
// Then no tool-call tag reaches the turn's answer,
// And any genuine prose the model wrote before the residue is preserved,
// And the model is re-prompted rather than taken at its word.
func TestOrphanToolMarkup_NeverReachesTheUser(t *testing.T) {
	const cleanAnswer = "Done — count.txt now contains ONE through FIVE."

	for _, leak := range uatOrphanToolMarkupLeaks {
		t.Run(leak.name, func(t *testing.T) {
			provider := testutil.NewScenario().
				WithText(leak.content).
				WithText(cleanAnswer)

			al, inst := newOrphanMarkupLoop(t, provider)

			answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
				SessionKey:      "orphan-leak-" + leak.name,
				Channel:         "web",
				ChatID:          "chat-orphan-leak-" + leak.name,
				UserMessage:     "write count.txt with one through five",
				DefaultResponse: defaultResponse,
				SendResponse:    false,
			})
			require.NoError(t, err, "the repaired turn must succeed")

			assertNoToolMarkup(t, "the turn's answer", answer)
			assert.Equal(t, cleanAnswer, answer,
				"the answer must be the model's SECOND, well-formed response — not the residue")

			require.Equal(t, 2, provider.CallCount(),
				"the loop must re-prompt after residue instead of accepting it as a finished answer")

			// The repair note must actually be in the second request, and it
			// must not quote the residue back (that invites a repeat).
			requests := provider.AllRequests()
			require.Len(t, requests, 2)
			second := requests[1]
			require.NotEmpty(t, second)
			repair := second[len(second)-1]
			assert.Equal(t, "user", repair.Role, "the repair note is a user-role turn")
			assert.Contains(t, repair.Content, "did not arrive as a tool call",
				"the repair note must name the actual failure")
			assertNoToolMarkup(t, "the repair note", repair.Content)
		})
	}
}

// TestOrphanToolMarkup_NeverReachesTheUserAlongsideARealToolCall covers the
// variant that the repair path does NOT cover, and which also happened live
// (session_01M2CVMBJGKMGNXHBVYGDSF430, turn-40): the round DID produce a
// parsed tool call, and carried residue in its narration text as well — a
// second call the upstream parser never recovered.
//
// That round is not stalled, so it is not re-prompted; the narration goes
// straight into the conversation and into transcript.jsonl. This test
// therefore isolates the STRIP: if the strip were a no-op, the retry that
// rescues the no-tool-call case could not hide it here.
//
// BDD: Given a response carrying both a real tool call and residual markup,
// When the loop records that round's narration,
// Then the assistant message the model sees next carries no markup.
func TestOrphanToolMarkup_NeverReachesTheUserAlongsideARealToolCall(t *testing.T) {
	leak := uatOrphanToolMarkupLeaks[1] // prose + residue: both halves matter

	provider := testutil.NewScenario().
		WithTextAndToolCall(leak.content, "list_directory", `{"path":"."}`).
		WithText("Listed the workspace.")

	al, inst := newOrphanMarkupLoop(t, provider)

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:      "orphan-with-toolcall-session",
		Channel:         "web",
		ChatID:          "chat-orphan-with-toolcall",
		UserMessage:     "list the workspace",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err)
	assertNoToolMarkup(t, "the turn's answer", answer)

	requests := provider.AllRequests()
	require.GreaterOrEqual(t, len(requests), 2,
		"the tool-call round must be followed by another request")

	sawNarration := false
	for _, msg := range requests[1] {
		assertNoToolMarkup(t, "a message replayed to the model", msg.Content)
		if msg.Role == "assistant" && strings.Contains(msg.Content, "Writing the file properly now") {
			sawNarration = true
			assert.Equal(t,
				"The previous write was malformed and never landed. Writing the file properly now.",
				msg.Content,
				"the narration must keep its real prose and lose only the residue")
		}
	}
	assert.True(t, sawNarration,
		"the round's narration must still be recorded — stripping must not delete the sentence")
}

// TestOrphanToolMarkup_PreservesGenuineProse pins the one case where the model
// wrote a real sentence before the residue: the sentence is the assistant
// talking and must not be thrown away with the markup.
//
// BDD: Given a response whose prose is followed by residual markup,
// When the residue is stripped,
// Then the prose survives byte-for-byte and the residue is fully removed.
func TestOrphanToolMarkup_PreservesGenuineProse(t *testing.T) {
	const prose = "The previous write was malformed and never landed. Writing the file properly now."
	full := prose + "content</arg_key><arg_value>one\ntwo\n</arg_value></tool_call>"

	resp := &providers.LLMResponse{Content: full}
	markup, found := stripOrphanToolCallMarkup(resp)

	require.True(t, found, "the residue must be detected")
	// "content" is the write_file parameter name the upstream parser flushed
	// out mid-key, not part of the sentence — an unmatched </arg_key> proves
	// that, so it goes with the residue.
	assert.Equal(t, prose, resp.Content, "genuine prose must survive the strip unchanged")
	assertNoToolMarkup(t, "the stripped content", resp.Content)
	assert.Equal(t, "</arg_key>", markup.Marker, "the first tag in this sample is a closing arg_key")
	assert.Contains(t, markup.Markup, "</tool_call>", "the residue is retained for diagnostics only")
}

// ---------------------------------------------------------------------------
// Defect 2 — the stall
// ---------------------------------------------------------------------------

// TestOrphanToolMarkup_NeverEndsTheTurnInSilence asserts that a tool call the
// system cannot parse never resolves into quiet nothing.
//
// This is the defect the tester actually sat through: seven minutes of
// spinner, then no question, no error, no recovery, and a goal record still
// reading state=active with zero criteria. Silence is the bug — not the
// malformation.
//
// BDD: Given a model that keeps returning residual tool-call markup and never
// produces a parsed tool call,
// When the loop's repair budget is spent,
// Then the turn fails with an error rather than returning an answer,
// And a typed error event reaches the live client,
// And the failure names the fault instead of the residue.
func TestOrphanToolMarkup_NeverEndsTheTurnInSilence(t *testing.T) {
	// Whole-response residue: nothing survives the strip, so before the fix
	// this turn resolved to an empty/garbage answer with err == nil.
	residue := uatOrphanToolMarkupLeaks[3].content

	provider := testutil.NewScenario().
		WithText(residue).
		WithText(residue).
		WithText(residue).
		WithText(residue)

	al, inst := newOrphanMarkupLoop(t, provider)

	sub := al.SubscribeEvents(512)
	t.Cleanup(func() { al.UnsubscribeEvents(sub.ID) })

	answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:      "orphan-stall-session",
		Channel:         "web",
		ChatID:          "chat-orphan-stall",
		UserMessage:     "make the report better",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})

	require.Error(t, err,
		"a tool call that never parses must end the turn LOUDLY, not return an answer; got answer=%q", answer)
	assert.Contains(t, err.Error(), "unparseable tool-call markup",
		"the failure must name the fault")
	assertNoToolMarkup(t, "the turn's answer", answer)

	// 1 original + maxOrphanToolMarkupRepairs re-prompts, then the error.
	assert.Equal(t, 1+maxOrphanToolMarkupRepairs, provider.CallCount(),
		"the loop must spend exactly its repair budget before failing")

	// The live client has to be told. An error the user never sees is the
	// same silence in a different costume.
	events := drainEvents(sub.C)
	var codes []string
	sawTypedError := false
	for _, e := range events {
		if e.Kind != EventKindError {
			continue
		}
		p, ok := e.Payload.(ErrorPayload)
		if !ok {
			continue
		}
		codes = append(codes, p.Code)
		if p.Code == string(CodeToolArgs) {
			sawTypedError = true
			assert.Equal(t, orphanToolMarkupStage, p.Stage,
				"the error must be attributed to the orphan-markup stage")
			assert.Equal(t, UserMessageForCode(CodeToolArgs), p.Message,
				"the user-facing copy must come from the contract catalogue")
			assertNoToolMarkup(t, "the user-facing error message", p.Message)
		}
	}
	assert.True(t, sawTypedError,
		"a typed %q error event must reach the client; saw codes=%v kinds=%v",
		CodeToolArgs, codes, eventKinds(events))
}

// truncatedMarkupProvider returns residual markup with finish_reason set, so
// the truncation-aware branch of the repair note can be exercised. The
// scripted testutil provider has no finish_reason builder.
type truncatedMarkupProvider struct {
	content      string
	finishReason string
	calls        int
	requests     [][]providers.Message
}

func (p *truncatedMarkupProvider) Chat(
	_ context.Context,
	msgs []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	p.calls++
	p.requests = append(p.requests, append([]providers.Message(nil), msgs...))
	if p.calls == 1 {
		return &providers.LLMResponse{
			Content:      p.content,
			FinishReason: p.finishReason,
			ToolCalls:    []providers.ToolCall{},
		}, nil
	}
	return &providers.LLMResponse{
		Content:      "Written in two smaller steps.",
		FinishReason: "stop",
		ToolCalls:    []providers.ToolCall{},
	}, nil
}

func (p *truncatedMarkupProvider) GetDefaultModel() string { return "scripted-model" }

// TestOrphanToolMarkup_TruncatedCallGetsSizeAdvice covers the mechanism behind
// the live failure: the round that leaked had completion_tokens of exactly
// 32,768 — the output cap — so the call was cut off mid-emission. Telling the
// model to retry without telling it the call was too big just reproduces it.
//
// BDD: Given residual markup on a response whose finish_reason says the output
// was cut off,
// When the loop re-prompts,
// Then the repair note also tells the model to make the call smaller.
func TestOrphanToolMarkup_TruncatedCallGetsSizeAdvice(t *testing.T) {
	provider := &truncatedMarkupProvider{
		content:      uatOrphanToolMarkupLeaks[0].content,
		finishReason: "truncated",
	}
	al, inst := newOrphanMarkupLoop(t, provider)

	_, err := al.runAgentLoop(context.Background(), inst, processOptions{
		SessionKey:      "orphan-truncated-session",
		Channel:         "web",
		ChatID:          "chat-orphan-truncated",
		UserMessage:     "write count.txt",
		DefaultResponse: defaultResponse,
		SendResponse:    false,
	})
	require.NoError(t, err)
	require.Equal(t, 2, provider.calls)

	second := provider.requests[1]
	require.NotEmpty(t, second)
	repair := second[len(second)-1].Content
	assert.Contains(t, repair, "cut off at the output-token limit",
		"a truncated call must be told WHY it failed, not just that it failed")
	assert.Contains(t, repair, "Make this call smaller",
		"the advice must be actionable for a truncated call")
}

// TestIsTruncatedFinishReason pins the finish_reason spellings that mean
// "ran out of output tokens". OpenAI says "length"; the shared normaliser
// (providers/common.normalizeFinishReason) rewrites that to "truncated"; not
// every adapter routes through it.
func TestIsTruncatedFinishReason(t *testing.T) {
	for _, r := range []string{"truncated", "length", "max_tokens", "LENGTH", " truncated "} {
		assert.True(t, isTruncatedFinishReason(r), "%q means the output was cut off", r)
	}
	for _, r := range []string{"stop", "tool_calls", "", "unknown", "content_filter"} {
		assert.False(t, isTruncatedFinishReason(r), "%q does not mean the output was cut off", r)
	}
}

// TestStripOrphanToolCallMarkup_LeavesCleanResponsesAlone is the negative
// control: a normal answer must pass through byte-identical, and a round that
// carries no residue must not be flagged.
func TestStripOrphanToolCallMarkup_LeavesCleanResponsesAlone(t *testing.T) {
	clean := "I wrote count.txt with ONE, TWO, THREE, FOUR, FIVE — one per line.\n\n```\nONE\nTWO\n```"
	resp := &providers.LLMResponse{Content: clean, ReasoningContent: "The user wants five numbers."}

	markup, found := stripOrphanToolCallMarkup(resp)
	assert.False(t, found, "a clean response must not be flagged; got marker %q", markup.Marker)
	assert.Equal(t, clean, resp.Content, "a clean response must pass through untouched")
	assert.Equal(t, "The user wants five numbers.", resp.ReasoningContent)

	var nilResp *providers.LLMResponse
	_, found = stripOrphanToolCallMarkup(nilResp)
	assert.False(t, found, "a nil response must be handled without panicking")
}

// TestStripOrphanToolCallMarkup_CoversReasoningContent pins that the fallback
// field is stripped too. The no-tool-calls branch uses ReasoningContent as the
// answer when Content is empty, so leaving it alone would relocate the leak
// rather than close it.
func TestStripOrphanToolCallMarkup_CoversReasoningContent(t *testing.T) {
	resp := &providers.LLMResponse{
		Content:          "",
		ReasoningContent: "I should write the file." + uatOrphanToolMarkupLeaks[2].content,
	}
	markup, found := stripOrphanToolCallMarkup(resp)
	require.True(t, found)
	assert.Equal(t, "</arg_key>", markup.Marker)
	assert.Equal(t, "I should write the file.", resp.ReasoningContent)
	assertNoToolMarkup(t, "the stripped reasoning", resp.ReasoningContent)
}

// TestOrphanToolMarkupRepairMessage_DoesNotEchoTheResidue pins the deliberate
// choice not to quote the model's own malformed output back at it.
func TestOrphanToolMarkupRepairMessage_DoesNotEchoTheResidue(t *testing.T) {
	msg := orphanToolMarkupRepairMessage("stop")
	assert.Equal(t, "user", msg.Role)
	assertNoToolMarkup(t, "the repair note", msg.Content)
	assert.NotContains(t, strings.ToLower(msg.Content), "count.txt",
		"the note must be generic — it never carries the failed call's contents")
}
