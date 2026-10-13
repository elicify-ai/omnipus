// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
)

// H5 (founder decision 2026-10-13): the empty-answer warning carries what
// actually came back — provider, model, stream event types with counts,
// output item types, status/incomplete reason, finish reason and token
// usage — and nothing of the content.
func TestEmptyAnswerLogFields_CarriesShapeNotContent(t *testing.T) {
	resp := &providers.LLMResponse{
		Content:      "",
		FinishReason: "length",
		Usage:        &providers.UsageInfo{PromptTokens: 14925, CompletionTokens: 7, TotalTokens: 14932},
		Diagnostics: &providers.ResponseDiagnostics{
			EventCounts:      map[string]int{"response.created": 1, "response.completed": 1},
			OutputItemTypes:  []string{"reasoning"},
			OutputSource:     "completed",
			Status:           "incomplete",
			IncompleteReason: "max_output_tokens",
		},
	}

	f := emptyAnswerLogFields("omnipus", 1, "openai-chatgpt", "gpt-6.1-sol", "gpt-6.1-sol", resp)

	assert.Equal(t, map[string]any{
		"agent_id":          "omnipus",
		"iteration":         1,
		"provider":          "openai-chatgpt",
		"model":             "gpt-6.1-sol",
		"finish_reason":     "length",
		"tool_calls":        0,
		"tokens_in":         14925,
		"tokens_out":        7,
		"tokens_total":      14932,
		"stream_events":     map[string]int{"response.created": 1, "response.completed": 1},
		"output_item_types": []string{"reasoning"},
		"output_source":     "completed",
		"status":            "incomplete",
		"incomplete_reason": "max_output_tokens",
	}, f)
}

func TestEmptyAnswerLogFields_UnstampedProviderIsUnknown(t *testing.T) {
	f := emptyAnswerLogFields("a", 2, "", "", "gpt-6.1-sol", &providers.LLMResponse{FinishReason: "stop"})

	assert.Equal(t, "unknown", f["provider"], "never guessed from the model id")
	assert.Equal(t, "gpt-6.1-sol", f["model"], "falls back to the requested model")
	assert.Equal(t, "unreported", f["usage"])
	assert.Equal(t, "unavailable for this provider", f["stream_diagnostics"])
	_, hasContent := f["content"]
	assert.False(t, hasContent)
}

// pairScriptedProvider answers per PROVIDER instance (pool entry), so a
// chain whose candidates share one model id can still be told apart.
type pairScriptedProvider struct {
	name  string
	mu    *sync.Mutex
	calls *[]string
	fail  bool // a provider failure (429 over the retry ceiling → next candidate)
	empty bool // a successful but empty answer
}

func (p *pairScriptedProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition, model string, _ map[string]any,
) (*providers.LLMResponse, error) {
	p.mu.Lock()
	*p.calls = append(*p.calls, p.name+"/"+model)
	p.mu.Unlock()
	switch {
	case p.fail:
		return nil, &common.ProviderError{Status: 429, Body: "rate limit exceeded for tenant", RetryAfterSeconds: 3600}
	case p.empty:
		return &providers.LLMResponse{Content: "", FinishReason: "stop"}, nil
	default:
		return &providers.LLMResponse{Content: "answered by " + p.name, FinishReason: "stop"}, nil
	}
}

func (p *pairScriptedProvider) GetDefaultModel() string { return "gpt-5.4" }

// runPairTurn runs one real turn over the given two-candidate chain with a
// per-provider pool, capturing the file log, and returns the result, the
// provider calls in order, and every logged line as decoded JSON.
func runPairTurn(
	t *testing.T, primary, fallback *pairScriptedProvider, model string,
) (turnResult, []string, []map[string]any) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	primary.mu, primary.calls = &mu, &calls
	fallback.mu, fallback.calls = &mu, &calls

	al, _ := newGoalLoopTestLoop(t, primary, nil)
	inst, ok := al.GetRegistry().GetAgent("native-agent")
	require.True(t, ok, "native-agent not registered")
	inst.Candidates = []providers.FallbackCandidate{
		{Provider: primary.name, Model: model},
		{Provider: fallback.name, Model: model},
	}
	inst.StoreProviderPool(map[string]providers.LLMProvider{primary.name: primary, fallback.name: fallback})
	store, sid := fbNoteStore(t)
	opts := processOptions{
		SessionKey: "h5-pair-session", Channel: "webchat", ChatID: "c1",
		UserMessage:     "hello",
		TranscriptStore: store, TranscriptSessionID: sid,
		DefaultResponse: "done", UserInitiated: true,
	}
	ts := newTurnState(inst, opts, al.newTurnEventScope(inst.ID, opts.SessionKey))

	logPath := filepath.Join(t.TempDir(), "log.jsonl")
	require.NoError(t, logger.EnableFileLogging(logPath))
	restoreConsole := logger.DisableConsole()
	result, err := al.runTurn(context.Background(), ts)
	restoreConsole()
	logger.DisableFileLogging()
	require.NoError(t, err)

	f, err := os.Open(logPath)
	require.NoError(t, err)
	defer f.Close()
	var lines []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var m map[string]any
		if json.Unmarshal(sc.Bytes(), &m) == nil {
			lines = append(lines, m)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	return result, append([]string(nil), calls...), lines
}

func emptyAnswerWarnLine(t *testing.T, lines []map[string]any) map[string]any {
	t.Helper()
	for _, l := range lines {
		for _, v := range l {
			if s, ok := v.(string); ok && strings.Contains(s, "LLM returned empty response after retry") {
				return l
			}
		}
	}
	t.Fatalf("the empty-answer warning was not logged; %d lines captured", len(lines))
	return nil
}

// Code-reviewer F5 / silent-failure F6: the same model id on two providers.
// The primary fails as a PROVIDER failure (so the chain legitimately moves
// on); the fallback answers empty. The warning must name the provider that
// actually produced the empty answer — not the first candidate whose model
// id matches.
func TestEmptyAnswerWarning_NamesTheAnsweringProviderForASharedModel(t *testing.T) {
	primary := &pairScriptedProvider{name: "openai", fail: true}
	fallback := &pairScriptedProvider{name: "openai-chatgpt", empty: true}

	result, _, lines := runPairTurn(t, primary, fallback, "gpt-5.4")

	assert.Equal(t, defaultResponse, result.finalContent)
	w := emptyAnswerWarnLine(t, lines)
	assert.Equal(t, "openai-chatgpt", w["provider"], "the answering provider, not the first model-id match")
	assert.Equal(t, "gpt-5.4", w["model"])
}

// Test-analyzer I3 (founder decision 2026-10-13): an EMPTY successful answer
// never walks the fallback chain — fallback is for provider failures only.
// The primary is retried once by the turn, the second provider is never
// called, and the turn ends on the canned message.
func TestEmptyPrimaryAnswer_DoesNotUseTheFallbackModel(t *testing.T) {
	primary := &pairScriptedProvider{name: "openai-chatgpt", empty: true}
	fallback := &pairScriptedProvider{name: "openrouter"}

	result, calls, lines := runPairTurn(t, primary, fallback, "gpt-6.1-sol")

	assert.Equal(t, []string{"openai-chatgpt/gpt-6.1-sol", "openai-chatgpt/gpt-6.1-sol"}, calls,
		"two primary calls (the turn's own retry), zero fallback calls")
	assert.Equal(t, defaultResponse, result.finalContent)
	assert.True(t, result.turnFailed)
	assert.Equal(t, "openai-chatgpt", emptyAnswerWarnLine(t, lines)["provider"])
}
