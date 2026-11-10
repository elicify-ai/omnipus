// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-5 (agent-private secrets) RED fixture: a real AgentLoop with a
// per-transcript-session scripted provider, real sessions, the real secret
// card registry and the real bash / environment_setup wiring. Every WU-5 test
// drives real entry points: bus-shaped inbound via processMessage, the real
// request_secret card flow, the real K5 submit, the real wake continuation and
// real child processes.
//
// Oracles come from the spec (US-7, BDD-7.1–7.12, BDD-6.19–6.22, D6, FR-064,
// FR-065, FR-072, FR-076, FR-086, FR-241, K6, K49), never from running the
// implementation. See tests/plans/train2-wu5-2026-10-18.md.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/documentruntime"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// seedWU5WorkspaceMembership writes a real workspace membership file (the
// shape pkg/workspace.FindForAgent reads), claiming the given agents for the
// workspace — the resume/continuation turn admission resolves membership.
func seedWU5WorkspaceMembership(t *testing.T, home, workspace string, agentIDs ...string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	team, err := json.Marshal(map[string]any{"id": workspace, "core_team": agentIDs})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, workspace+".json"), team, 0o644))
}

// wu5RequestPrivateSecret is the model's real request_secret call for the
// agent-private kind: purpose + the variable name (US-7; the recipient is the
// caller's own (workspace, agent) pair, FR-065/FR-076).
func wu5RequestPrivateSecret(variable string) providers.LLMResponse {
	return providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: "wu5-req-" + variable, Type: "function", Name: "request_secret",
		Arguments: map[string]any{"purpose": wu5Purpose, "variable": variable},
	}}}
}

// wu5Bash is the model's real bash call with a unique tool-call id (unique
// ids are what lets the result deduper count each call exactly once).
func wu5Bash(command string) providers.LLMResponse {
	return wu5BashCall(fmt.Sprintf("wu5-bash-%d", wu5CallSeq.Add(1)), command)
}

// wu5BashCall is the model's real bash call under an explicit tool-call id.
func wu5BashCall(id, command string) providers.LLMResponse {
	return providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: id, Type: "function", Name: "bash",
		Arguments: map[string]any{"command": command},
	}}}
}

// wu5Stop ends a turn.
func wu5Stop(text string) providers.LLMResponse {
	return providers.LLMResponse{Content: text, FinishReason: "stop"}
}

// Spec-fixed values (US-7's own example variable; values and lengths are the
// tests' constants, not derived from any implementation).
const (
	wu5Variable     = "WEATHER_TOKEN"
	wu5SalesValue   = "WEATHER_SALES_V1" // 16 bytes (BDD-7.1 prints the length)
	wu5SalesValueV2 = "WEATHER_SALES_V2" // 16 bytes (BDD-6.20 replacement)
	wu5MktValue     = "WEATHER_MKT_V1"   // 14 bytes (BDD-7.5: B, different length from A)
	wu5Purpose      = "weather API token for the sales agent"
	wu5Owner        = "wu5-owner"
)

// wu5ScriptedProvider returns a fixed script of responses per transcript
// session, recording every prompt it is shown. Scripts are the model edge of
// the real loop; the loop executes the tool calls for real.
type wu5ScriptedProvider struct {
	mu      sync.Mutex
	calls   map[string]int
	scripts map[string][]providers.LLMResponse
	prompts map[string][][]providers.Message
}

// wu5CallSeq mints unique tool-call ids for scripted bash calls.
var wu5CallSeq atomic.Int64

func newWU5ScriptedProvider() *wu5ScriptedProvider {
	return &wu5ScriptedProvider{
		calls:   make(map[string]int),
		scripts: make(map[string][]providers.LLMResponse),
		prompts: make(map[string][][]providers.Message),
	}
}

// setScript installs the response sequence for one transcript session.
// setScript installs the response sequence for one transcript session and
// RESTARTS that session's call counter — every phase of a test scripts its
// own segment from index 0, never inheriting an earlier phase's offset.
func (p *wu5ScriptedProvider) setScript(sessionID string, script []providers.LLMResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scripts[sessionID] = script
	p.calls[sessionID] = 0
}

func (p *wu5ScriptedProvider) GetDefaultModel() string { return "test-model" }

func (p *wu5ScriptedProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	sid := tools.ToolTranscriptSessionID(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.calls[sid]
	p.calls[sid] = n + 1
	p.prompts[sid] = append(p.prompts[sid], append([]providers.Message(nil), messages...))
	script := p.scripts[sid]
	if n >= len(script) {
		// A missing script tail is a test bug; make it loud instead of
		// silently finishing the turn.
		return &providers.LLMResponse{Content: fmt.Sprintf("WU5 SCRIPT EXHAUSTED after %d calls", n+1)}, nil
	}
	return &script[n], nil
}

func (p *wu5ScriptedProvider) callCount(sessionID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[sessionID]
}

// toolResultsFor returns the content of every tool-role message the provider
// was shown for a session, in order, labelled with the tool call id.
func (p *wu5ScriptedProvider) toolResultsFor(sessionID string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, msgs := range p.prompts[sessionID] {
		for _, m := range msgs {
			if m.Role == "tool" {
				out = append(out, m.Content)
			}
		}
	}
	return out
}

// uniqueToolResultsFor returns each distinct tool call's result exactly once
// (keyed by tool_call_id, first occurrence, in first-seen order). Every later
// prompt re-includes earlier results as history, so the raw walk multiplies
// each result by the number of subsequent model rounds; counting oracles
// must read this, not the raw walk.
func (p *wu5ScriptedProvider) uniqueToolResultsFor(sessionID string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := make(map[string]bool)
	var out []string
	for _, msgs := range p.prompts[sessionID] {
		for _, m := range msgs {
			if m.Role != "tool" || m.ToolCallID == "" || seen[m.ToolCallID] {
				continue
			}
			seen[m.ToolCallID] = true
			out = append(out, m.Content)
		}
	}
	return out
}

// resultForCall returns the tool result addressed to one tool_call_id —
// the plumbing equivalent of the model reading the result under its own
// call id (two background-spawning tools both answer with ExecResponse
// JSON, so the id, not the shape, tells them apart).
func (p *wu5ScriptedProvider) resultForCall(sessionID, callID string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, msgs := range p.prompts[sessionID] {
		for _, m := range msgs {
			if m.Role == "tool" && m.ToolCallID == callID {
				return m.Content
			}
		}
	}
	return ""
}

// wu5Fixture is one real loop with the WU-5 agents registered.
type wu5Fixture struct {
	t        *testing.T
	al       *AgentLoop
	bus      *bus.MessageBus
	provider *wu5ScriptedProvider
	home     string
}

func newWU5Fixture(t *testing.T) *wu5Fixture {
	t.Helper()
	return newWU5FixtureOpts(t, false)
}

// newWU5FixtureWithDocRuntime builds the fixture with a HEALTHY provisioned
// document runtime (BDD-7.10): the layout is provisioned before the loop is
// built, so the loop's own resolve+provision pass finds it and wires the doc
// layer onto the exec tool exactly as production does.
func newWU5FixtureWithDocRuntime(t *testing.T) *wu5Fixture {
	t.Helper()
	return newWU5FixtureOpts(t, true)
}

func newWU5FixtureOpts(t *testing.T, provisionDocRuntime bool) *wu5Fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OMNIPUS_HOME", home)
	if provisionDocRuntime {
		layout, err := documentruntime.ResolveLayout(home, documentruntime.ManifestRevision, "shared")
		require.NoError(t, err)
		_, err = documentruntime.ProvisionFirstParty(layout)
		require.NoError(t, err)
	}
	// Membership for the workspaces the tests use (the resume/continuation
	// turn admission resolves the agent's workspace membership).
	seedWU5WorkspaceMembership(t, home, "sales", "mia")
	seedWU5WorkspaceMembership(t, home, "marketing", "mia")
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	cfg := &config.Config{Agents: config.AgentsConfig{
		Defaults: config.AgentDefaults{Home: home + "/workspace", DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 24},
		List:     []config.AgentConfig{{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom}, {ID: "ray", Name: "Ray", Type: config.AgentTypeCustom}},
	}}
	// The WU-5 subject is injection scoping, not the approval machinery: the
	// tools under test ride allow so a turn exercises them without the
	// Train-3 approval surface. Ray's BDD-7.2 "approved in the fixture" is
	// this grant (deviation recorded in the acceptance map).
	cfg.Sandbox.ToolPolicies = map[string]string{
		"bash":              "allow",
		"request_secret":    "allow",
		"environment_setup": "allow",
		"list_secrets":      "allow",
	}
	provider := newWU5ScriptedProvider()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	// The real credential store the card flow commits into (the same wiring
	// the WU-4 acceptance fixtures use; the registry resolves its store
	// through sysagentDeps.CredStore).
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x39}, 32)))
	t.Cleanup(store.Close)
	al.WireSysagentDeps(&systools.Deps{Home: home, GetCfg: al.GetConfig, CredStore: store})
	// The shipped default (pkg/config/defaults.go) filters stored sensitive
	// values out of tool output; the raw hand-built fixture leaves it off.
	// Exercise the real masking path — the WU-4 pre-claim fixture made the
	// same call (c6e0de4d3).
	al.GetConfig().Tools.FilterSensitiveData = true
	// The loop-wired agents (from cfg.Agents.List, carrying the loop's exec
	// deps and full tool surface) run the turns with the fixture's scripted
	// provider — deliberately NO registerAgent here: a bare AgentInstance
	// replacement drops request_secret and the loop's exec-tool deps and
	// makes every turn refuse mid-turn.
	return &wu5Fixture{t: t, al: al, bus: msgBus, provider: provider, home: home}
}

// mainSession returns the agent's real main session in a workspace, with a
// fixed owner stamped (the WU-4 acceptance fixtures stamp the owner the same
// way; the registry derives the record owner from the session ancestry).
func (f *wu5Fixture) mainSession(workspace, agentID string) string {
	f.t.Helper()
	s, err := f.al.GetSessionStore().GetOrCreateMainSession(workspace, agentID)
	require.NoError(f.t, err)
	owner := wu5Owner
	require.NoError(f.t, f.al.GetSessionStore().SetMeta(s.ID, session.MetaPatch{Owner: &owner}))
	return s.ID
}

// pendingPrivateCard reads the pending private card from the real K4 session
// state (Registry.Snapshot is the state the SPA renders) and asserts the
// agent_private contract the spec fixes: state pending, owner pair present
// and equal to the requesting agent's own pair (FR-065/FR-076, K1/K2/K4).
func (f *wu5Fixture) pendingPrivateCard(sessionID, agentID, workspace string) gen.SessionStateSecretRequest {
	f.t.Helper()
	for _, card := range f.al.SecretRequests().Snapshot(wu5Owner, sessionID) {
		if card.State != "pending" {
			continue
		}
		require.NotNil(f.t, card.Owner, "BLOCKED: pending private card carries no owner pair — required by K1/K4 (owner pair, agent_private kind), FR-065")
		require.Equal(f.t, agentID, card.Owner.AgentId, "card owner pair must name the acting agent")
		require.Equal(f.t, workspace, card.Owner.WorkspaceId, "card owner pair must name the acting workspace (CRIT-001 scope)")
		return card
	}
	f.t.Fatalf("BLOCKED: no pending secret card in session state for %s — the agent_private card kind (request_secret with variable %q) is not implemented, required by US-7 AS-1, BDD-7.8, FR-061/FR-065/FR-076", sessionID, wu5Variable)
	return gen.SessionStateSecretRequest{}
}

// webTurn drives one real web-app turn on a session (BDD-7.12's web control
// carrier: the webchat channel, no channel instance).
func (f *wu5Fixture) webTurn(sessionID, text string) {
	f.t.Helper()
	msg := bus.InboundMessage{Channel: "webchat", ChatID: sessionID, SessionID: sessionID, Content: text}
	_, _, err := f.al.processMessage(context.Background(), msg)
	require.NoError(f.t, err)
}

// channelTurn drives one real channel-person turn on a session (BDD-7.12's
// carrier: a channel instance + a person, exactly what the adapters publish).
func (f *wu5Fixture) channelTurn(sessionID, text string) {
	f.t.Helper()
	msg := bus.InboundMessage{
		Channel: "telegram", InstanceID: "tg-sales-1", ChatID: "anna-chat", SessionID: sessionID,
		Content: text, Sender: bus.SenderInfo{DisplayName: "Anna", CanonicalID: "telegram:anna"},
	}
	_, _, err := f.al.processMessage(context.Background(), msg)
	require.NoError(f.t, err)
}

// waitForCalls polls until the scripted provider has served n calls for a
// session (the turn advanced that many model rounds) or fails after a
// deadline. A stalled turn is a miss, never a pass.
func (f *wu5Fixture) waitForCalls(sessionID string, n int, what string) {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if f.provider.callCount(sessionID) >= n {
			return
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("turn never reached model call %d (%s); reached %d", n, what, f.provider.callCount(sessionID))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// takeWake takes the secret-resume wake the real submit path published for a
// request id (the same manual drain the WU-4 acceptance fixtures use: the
// wake is a real bus input processed through processMessage).
func (f *wu5Fixture) takeWake(requestID string) bus.InboundMessage {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-f.bus.InboundChan():
			if msg.Metadata[secretResumeMetadataKey] == requestID {
				return msg
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	f.t.Fatalf("BLOCKED: no secret-resume wake was published for request %s — required by the WU-5 submit flow (submit → selected → continuation), spec FR-086/FR-072", requestID)
	return bus.InboundMessage{}
}

// submitPrivate submits the card's field value under the card's own
// server-chosen field key (plumbing, not oracle — the spec fixes the state
// machine, not the field key spelling) and returns the submitted record.
func (f *wu5Fixture) submitPrivate(rec secretrequest.Record, value string) secretrequest.Record {
	f.t.Helper()
	require.NotEmpty(f.t, rec.Card.Fields, "the private card must declare its field before submit (K1 fields[1..8])")
	key := rec.Card.Fields[0].Key
	values := map[string]string{key: value}
	submitted, err := f.al.SecretRequests().Submit(rec.Card.RequestId, rec.Owner, gen.SecretSubmitRequest{Values: &values})
	require.NoError(f.t, err, "submitting the private variable value must store and select it (FR-086: selected on submit)")
	return submitted
}

// resumeAfterSubmit drains the real wake and drives the continuation turn to
// the next scripted response.
func (f *wu5Fixture) resumeAfterSubmit(requestID string) {
	f.t.Helper()
	wake := f.takeWake(requestID)
	_, _, err := f.al.processMessage(context.Background(), wake)
	require.NoError(f.t, err)
}
