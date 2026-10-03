package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

// Oracles in this pack come from the 2026-10-01 plain-history ruling §§1–4
// (FR-030/031/032). Only the external provider is scripted. Captures are made
// at Chat entry, after the REAL send normalization, before returning a response.
// not-wire-format: test receipt at the LLM seam, not a gateway/SPA wire type.
type plainHistoryRequest struct {
	Messages []providers.Message        `json:"messages"`
	Tools    []providers.ToolDefinition `json:"tools"`
	Model    string                     `json:"model"`
	Options  map[string]any             `json:"options"`
}

type plainHistoryCapture struct {
	request plainHistoryRequest
	body    []byte
}

type plainHistoryProvider struct {
	mu       sync.Mutex
	captures []plainHistoryCapture
	reply    func(int) (*providers.LLMResponse, error)
	observe  func(int, plainHistoryCapture)
}

func (p *plainHistoryProvider) Chat(_ context.Context, messages []providers.Message,
	defs []providers.ToolDefinition, model string, options map[string]any,
) (*providers.LLMResponse, error) {
	body, err := json.Marshal(plainHistoryRequest{messages, defs, model, options})
	if err != nil {
		return nil, fmt.Errorf("record provider request: %w", err)
	}
	var request plainHistoryRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, fmt.Errorf("freeze provider request: %w", err)
	}
	capture := plainHistoryCapture{request: request, body: body}
	p.mu.Lock()
	p.captures = append(p.captures, capture)
	call := len(p.captures)
	p.mu.Unlock()
	if p.observe != nil {
		p.observe(call, capture)
	}
	return p.reply(call)
}

func (*plainHistoryProvider) GetDefaultModel() string { return "test-model" }

func (p *plainHistoryProvider) recorded() []plainHistoryCapture {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]plainHistoryCapture(nil), p.captures...)
}

func plainHistoryRejectOnce(rejection error, answer string) *plainHistoryProvider {
	return &plainHistoryProvider{reply: func(call int) (*providers.LLMResponse, error) {
		if call == 1 {
			return nil, rejection
		}
		return &providers.LLMResponse{Content: answer}, nil
	}}
}

func newPlainHistoryLoop(t *testing.T, provider providers.LLMProvider) (*AgentLoop, *AgentInstance, *bus.MessageBus) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	workspace := filepath.Join(home, "mia")
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: workspace, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 1000, MaxToolIterations: 10},
		List:     []config.AgentConfig{{ID: testDefaultAgentID, Home: workspace}},
	}}
	cfg.Context = config.DefaultContextSettings()
	cfg.Context.DefaultContextWindow = intPtr(100000) // wide: rejection, not proactive pressure, must trigger relief (§3).
	cfg.Tools.Manifest.Compressed = true
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	agent := al.registry.GetDefaultAgent()
	require.NotNil(t, agent, "fixture: explicit Mia must resolve")
	return al, agent, msgBus
}

func plainHistoryExchange(n int) []providers.Message {
	// Markers follow long prose, outside the breadcrumb's source snippet.
	// Each removable pair exceeds required framing by thousands of bytes (§3).
	return []providers.Message{
		{Role: "user", Content: strings.Repeat("old user source prose ", 128) + fmt.Sprintf("USER-SOURCE-%d-END", n)},
		{Role: "assistant", Content: strings.Repeat("old assistant source prose ", 128) + fmt.Sprintf("ASSISTANT-SOURCE-%d-END", n)},
	}
}

func seedPlainHistory(t *testing.T, agent *AgentInstance, key string, history []providers.Message) {
	t.Helper()
	agent.Sessions.SetHistory(key, history)
	require.NoError(t, agent.Sessions.Save(key))
	active := agent.Sessions.GetHistory(key)
	if len(history) == 0 {
		require.Empty(t, active, "fixture: fresh scope must be empty")
	} else {
		require.Equal(t, history, active, "fixture: seeded archive must be the active history")
	}
}

func plainHistoryBody(t *testing.T, capture plainHistoryCapture) []providers.Message {
	t.Helper()
	return plainHistoryNonSystem(capture.request.Messages)
}

func plainHistoryNonSystem(messages []providers.Message) []providers.Message {
	var body []providers.Message
	for _, m := range messages {
		if m.Role != "system" {
			body = append(body, m)
		}
	}
	return body
}

func assertPlainHistoryFirstAttempt(t *testing.T, agent *AgentInstance, p *plainHistoryProvider, markers ...string) {
	t.Helper()
	captures := p.recorded()
	require.NotEmpty(t, captures, "fixture: must reach the real provider seam")
	for _, marker := range markers {
		require.Contains(t, string(captures[0].body), marker, "fixture: first actual request must contain seeded source")
	}
	request := captures[0].request
	require.LessOrEqual(t, requestTokens(request.Messages, request.Tools), agentContextBudget(agent), "fixture: local total must fit before provider rejects")
	window, _, _ := agent.windowSnapshot()
	require.LessOrEqual(t, toolResultShareTokens(request.Messages), toolResultShareLimit(config.DefaultContextSettings(), window), "fixture: local result share must fit")
	t.Logf("instrument: first request bytes=%d, locally fitting, seed markers=%v", len(captures[0].body), markers)
}

func assertPlainHistoryShrink(t *testing.T, captures []plainHistoryCapture, removed ...string) {
	t.Helper()
	require.Len(t, captures, 2, "FR-032: one changed retry must recover")
	require.Less(t, len(captures[1].body), len(captures[0].body), "§3: actual serialized provider request must strictly shrink, including required framing")
	for _, marker := range removed {
		require.Contains(t, string(captures[0].body), marker, "source-removal instrument positive control")
		require.NotContains(t, string(captures[1].body), marker, "FR-030: original seeded source must leave the actual request, not merely change a notice")
	}
	t.Logf("instrument: serialized provider request %d -> %d bytes; removed=%v", len(captures[0].body), len(captures[1].body), removed)
}

func assertPlainHistoryRejection(t *testing.T, err, actualRejection error) {
	t.Helper()
	require.Error(t, err, "FR-032: expose the actual provider rejection")
	var classified *providers.FailoverError
	require.ErrorAs(t, err, &classified, "must retain the typed provider classification, not a local size-only error")
	require.Equal(t, providers.FailoverContextOverflow, classified.Reason)
	require.ErrorIs(t, err, actualRejection, "must expose the last actual rejection cause")
	require.Contains(t, err.Error(), actualRejection.Error())
}

func runPlainHistoryEntryRecovery(t *testing.T, direct bool) {
	t.Helper()
	answer := "Recovered from context error"
	const trigger = "Trigger message"
	rejection := errors.New("InvalidParameter: Total tokens of image and text exceed max message tokens")
	if !direct {
		rejection = errors.New("context_window_exceeded")
		answer = "Recovered from overflow"
	}
	p := plainHistoryRejectOnce(rejection, answer)
	al, agent, _ := newPlainHistoryLoop(t, p)
	// Ruling §3: an explicit agent key is preserved along the FULL routing chain.
	key := "agent:" + agent.ID + ":plain-history-entry"
	history := append(plainHistoryExchange(1), plainHistoryExchange(2)...)
	seedPlainHistory(t, agent, key, history)
	var response string
	var err error
	if direct {
		response, err = al.ProcessDirectWithChannel(context.Background(), trigger, key, "test", "plain-entry-chat")
	} else {
		var routed *AgentInstance
		response, routed, err = al.processMessage(context.Background(), bus.InboundMessage{
			Channel: "test", ChatID: "plain-entry-chat", Sender: bus.SenderInfo{CanonicalID: "user1"}, SessionKey: key, Content: trigger,
		})
		require.Same(t, agent, routed, "fixture: seeded Mia must execute")
	}
	assertPlainHistoryFirstAttempt(t, agent, p, "USER-SOURCE-1-END", "ASSISTANT-SOURCE-1-END", "ASSISTANT-SOURCE-2-END")
	require.NoError(t, err, "FR-030/032: seeded older plain history must authorize real relief, not stop at no progress")
	require.Equal(t, answer, response)
	captures := p.recorded()
	assertPlainHistoryShrink(t, captures, "USER-SOURCE-1-END", "ASSISTANT-SOURCE-1-END")
	require.Equal(t, append(plainHistoryExchange(2), providers.Message{Role: "user", Content: trigger}), plainHistoryBody(t, captures[1]), "newest assistant and initiating user survive exactly")
}
