// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-5 gateway-surface RED tests: the K49 options route (PATCH
// /api/v1/secrets/{reference}/options — "handler arrives with WU-5" per
// contracts/openapi.yaml), the K6 listing shape (the route the contract
// assigns to WU-6; kept in this pack because the version lifecycle below is
// what makes its state field falsifiable), private-version selection
// semantics (FR-086, FR-072, BDD-6.19/6.20/6.22) and BDD-7.8's validated
// Omnipus target.
//
// Cards are raised through the real request_secret tool against the real
// registry; submits, the options flip and the listing go through the real
// production routes. A stdlib "404 page not found" is the missing endpoint,
// reported as BLOCKED with the spec reference — never a skip.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// wu5 rest constants (values are the tests', lengths asserted in the agent
// pack; see pkg/agent/wu5_private_secret_fixture_test.go).
const (
	wu5Variable = "WEATHER_TOKEN"
	wu5ValueV1  = "WEATHER_SALES_V1"
	wu5ValueV2  = "WEATHER_SALES_V2"
	wu5Purpose  = "weather API token for the sales agent"
	// wu5Owner is the identity the fixture ACTUALLY authenticates with:
	// DevModeBypass admits every request as auth.go's `_dev_bypass` synthetic
	// user, and the K5 submit resolves its owner from that identity — the
	// session ancestry's owner must be the same string or every REST submit
	// misses its record.
	wu5Owner      = "_dev_bypass"
	wu5OptionsURL = "/api/v1/secrets/%s/options"
)

// wu5GatewayProvider is a per-session scripted provider (the model edge).
type wu5GatewayProvider struct {
	mu      sync.Mutex
	calls   map[string]int
	scripts map[string][]providers.LLMResponse
	prompts map[string][][]providers.Message
}

func newWU5GatewayProvider() *wu5GatewayProvider {
	return &wu5GatewayProvider{calls: map[string]int{}, scripts: map[string][]providers.LLMResponse{}, prompts: map[string][][]providers.Message{}}
}

// setScript installs the response sequence for one transcript session and
// RESTARTS that session's call counter — every phase of a test scripts its
// own segment from index 0, never inheriting an earlier phase's offset.
func (p *wu5GatewayProvider) setScript(sid string, script []providers.LLMResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scripts[sid] = script
	p.calls[sid] = 0
}

func (p *wu5GatewayProvider) GetDefaultModel() string { return "test-model" }

func (p *wu5GatewayProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	sid := tools.ToolTranscriptSessionID(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.calls[sid]
	p.calls[sid] = n + 1
	p.prompts[sid] = append(p.prompts[sid], append([]providers.Message(nil), messages...))
	if n >= len(p.scripts[sid]) {
		return &providers.LLMResponse{Content: "WU5 SCRIPT EXHAUSTED"}, nil
	}
	return &p.scripts[sid][n], nil
}

func (p *wu5GatewayProvider) toolResults(sid string) []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, msgs := range p.prompts[sid] {
		for _, m := range msgs {
			if m.Role == "tool" {
				out = append(out, m.Content)
			}
		}
	}
	return out
}

func (p *wu5GatewayProvider) waitFor(sid string, n int, what string, t *testing.T) {
	t.Helper()
	for i := 0; i < 200; i++ { // ~20 s at 100 ms
		p.mu.Lock()
		got := p.calls[sid]
		p.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	p.mu.Lock()
	got := p.calls[sid]
	p.mu.Unlock()
	t.Fatalf("turn never reached model call %d (%s); reached %d", n, what, got)
}

// wu5GatewayFixture is one restAPI over a real loop with the real credential
// store wired (production boot passes the shared store through
// WireSysagentDeps), the loop's real bus consumer running (so submit
// continuations and channel turns flow the production path) and a scripted
// provider.
type wu5GatewayFixture struct {
	t        *testing.T
	api      *restAPI
	mux      *http.ServeMux
	al       *agent.AgentLoop
	bus      *bus.MessageBus
	provider *wu5GatewayProvider
	home     string
}

func newWU5GatewayFixture(t *testing.T, bypass bool) *wu5GatewayFixture {
	t.Helper()
	withEdition(t, config.EditionCore)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	// OMNIPUS_HOME is pinned BEFORE the loop is built: the audit logger
	// resolves its `system/` directory at construction, so the env must
	// already point at the fixture home (the K49 audit assertion reads it).
	t.Setenv("OMNIPUS_HOME", tmpDir)
	require.NoError(t, os.WriteFile(tmpDir+"/config.json", []byte(`{"version":1,"agents":{"defaults":{},"list":[]},"providers":[]}`), 0o600))
	// Membership for the workspace the tests use (the real resume turn's
	// admission resolves the agent's workspace membership).
	seedWU5Membership(t, tmpDir)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: bypass},
		Sandbox: config.OmnipusSandboxConfig{
			// Audit ON: the K49 contract ("audited") is asserted against the
			// real audit log under the data home.
			AuditLog: true,
			ToolPolicies: map[string]string{
				"bash": "allow", "request_secret": "allow", "environment_setup": "allow",
			},
		},
		Agents: config.AgentsConfig{
			// The PRODUCTION shape: Defaults.Home sits UNDER the data home
			// (<OMNIPUS_HOME>/workspace). The loop's own home is
			// Dir(Defaults.Home) (loop_construct.go) — the audit log's
			// system/ directory — so a flat tmpDir here would put the audit
			// log in tmpDir's PARENT.
			Defaults: config.AgentDefaults{Home: filepath.Join(tmpDir, "workspace"), DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096, MaxToolIterations: 8},
			List:     []config.AgentConfig{{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom}, {ID: "omnipus", Name: "Omnipus", Type: config.AgentTypeCore}},
		},
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	provider := newWU5GatewayProvider()
	al := mustAgentLoop(t, cfg, msgBus, provider)
	home := tmpDir
	store := credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, store.UnlockWithKey(bytes.Repeat([]byte{0x39}, 32)))
	t.Cleanup(store.Close)
	al.WireSysagentDeps(&systools.Deps{Home: home, GetCfg: al.GetConfig, CredStore: store})
	api := &restAPI{agentLoop: al, homePath: home, allowedOrigin: "http://localhost:3000"}
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})
	// The loop's real inbound consumer (startSecretCardLoop runs it the same
	// way): submit continuations and channel-triggered turns flow it.
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go al.Run(runCtx)
	return &wu5GatewayFixture{t: t, api: api, mux: mux, al: al, bus: msgBus, provider: provider, home: home}
}

// do issues a request through the production route table.
func (f *wu5GatewayFixture) do(method, path, body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = uniqueTestSourceIP()
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, f.api.agentLoop.GetConfig())
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// mainSession stamps the owner the registry derives record ownership from.
func (f *wu5GatewayFixture) mainSession(workspace, agentID string) string {
	f.t.Helper()
	s, err := f.al.GetSessionStore().GetOrCreateMainSession(workspace, agentID)
	require.NoError(f.t, err)
	owner := wu5Owner
	require.NoError(f.t, f.al.GetSessionStore().SetMeta(s.ID, session.MetaPatch{Owner: &owner}))
	return s.ID
}

// chatSession creates an ADDITIONAL real chat session for one (workspace,
// agent) pair — a main session is computed from the pair (one per pair), so
// a second card for the SAME pair (BDD-6.22's two concurrent submits) needs
// its own chat session carrying the same workspace and owner.
func (f *wu5GatewayFixture) chatSession(workspace, agentID string) string {
	f.t.Helper()
	s, err := f.al.GetSessionStore().NewSession(session.SessionTypeChat, "webchat", agentID)
	require.NoError(f.t, err)
	ws := workspace
	owner := wu5Owner
	require.NoError(f.t, f.al.GetSessionStore().SetMeta(s.ID, session.MetaPatch{WorkspaceID: &ws, Owner: &owner}))
	return s.ID
}

// requestPrivateCard raises a private card through the real tool for a
// session (the same Execute the model's call hits).
func (f *wu5GatewayFixture) requestPrivateCard(sid, agentID string, extra map[string]any) (string, error) {
	f.t.Helper()
	args := map[string]any{"purpose": wu5Purpose, "variable": wu5Variable}
	for k, v := range extra {
		args[k] = v
	}
	ctx := tools.WithTurnOrigin(
		tools.WithAgentID(
			tools.WithTranscriptSessionID(
				tools.WithToolContext(context.Background(), "webchat", sid),
				sid),
			agentID),
		tools.NewTurnOrigin(),
	)
	tool := tools.NewRequestSecretTool(func() *secretrequest.Registry { return f.al.SecretRequests() }, nil)
	result := tool.Execute(ctx, args)
	if result.IsError {
		return "", fmt.Errorf("%s", result.ForLLM)
	}
	// Read the pending card back from the real K4 session state.
	for _, card := range f.al.SecretRequests().Snapshot(wu5Owner, sid) {
		if card.State == "pending" {
			return card.RequestId, nil
		}
	}
	return "", fmt.Errorf("tool reported success but no pending card is in session state for %s", sid)
}

// submitThroughREST submits via the real K5 route and requires 200.
func (f *wu5GatewayFixture) submitThroughREST(requestID, fieldKey, value string) {
	f.t.Helper()
	body, err := json.Marshal(gen.SecretSubmitRequest{Values: &map[string]string{fieldKey: value}})
	require.NoError(f.t, err)
	rec := f.do(http.MethodPost, "/api/v1/secret-requests/"+requestID+"/submit", string(body))
	require.Equal(f.t, http.StatusOK, rec.Code, "K5 submit must select on submit (FR-086, Q7 A); got %d %s", rec.Code, rec.Body.String())
}

// cardFieldKey reads the pending card's first field key from session state.
func (f *wu5GatewayFixture) cardFieldKey(sid string) string {
	f.t.Helper()
	for _, card := range f.al.SecretRequests().Snapshot(wu5Owner, sid) {
		if card.State == "pending" {
			require.NotEmpty(f.t, card.Fields, "the private card must declare its field (K1)")
			return card.Fields[0].Key
		}
	}
	f.t.Fatalf("no pending card in session state for %s", sid)
	return ""
}

// listSecrets decodes the real K6 listing.
func (f *wu5GatewayFixture) listSecrets() []gen.SecretNameEntry {
	f.t.Helper()
	rec := f.do(http.MethodGet, "/api/v1/secrets", "")
	require.Equal(f.t, http.StatusOK, rec.Code, "GET /api/v1/secrets must list (K6); got %d %s", rec.Code, rec.Body.String())
	var entries []gen.SecretNameEntry
	require.NoError(f.t, json.Unmarshal(rec.Body.Bytes(), &entries), "K6 list body must decode into SecretNameEntry values: %s", rec.Body.String())
	return entries
}

// entryFor finds one reference in the listing.
func (f *wu5GatewayFixture) entryFor(refs []string) gen.SecretNameEntry {
	f.t.Helper()
	for _, entry := range f.listSecrets() {
		for _, want := range refs {
			if entry.Reference == want {
				return entry
			}
		}
	}
	f.t.Fatalf("reference(s) %v not found in the K6 listing", refs)
	return gen.SecretNameEntry{}
}

// referenceNames returns the submitted reference names from the request state
// (GET /api/v1/secret-requests/{id}, FR-079).
func (f *wu5GatewayFixture) referenceNames(requestID string) []string {
	f.t.Helper()
	rec := f.do(http.MethodGet, "/api/v1/secret-requests/"+requestID, "")
	require.Equal(f.t, http.StatusOK, rec.Code, "K5 state read must work; got %d %s", rec.Code, rec.Body.String())
	var state gen.SecretRequestState
	require.NoError(f.t, json.Unmarshal(rec.Body.Bytes(), &state))
	require.NotNil(f.t, state.ReferenceNames, "a submitted card stores under versioned references (FR-072)")
	require.NotEmpty(f.t, *state.ReferenceNames, "a submitted card stores under versioned references (FR-072)")
	return *state.ReferenceNames
}

// wu5BashGW / wu5StopGW are this package's model-edge response builders.
func wu5BashGW(command string) providers.LLMResponse {
	return providers.LLMResponse{ToolCalls: []providers.ToolCall{{
		ID: "wu5gw-bash", Type: "function", Name: "bash",
		Arguments: map[string]any{"command": command},
	}}}
}

func wu5StopGW(text string) providers.LLMResponse {
	return providers.LLMResponse{Content: text, FinishReason: "stop"}
}

// channelTurnGW publishes a real channel-person inbound (the exact carrier
// shape a channel adapter produces) to the running loop consumer.
func (f *wu5GatewayFixture) channelTurnGW(sid string) {
	f.t.Helper()
	msg := bus.InboundMessage{
		Channel: "telegram", InstanceID: "tg-sales-1", ChatID: "anna-chat", SessionID: sid,
		Content: "check the weather for me", Sender: bus.SenderInfo{DisplayName: "Anna", CanonicalID: "telegram:anna"},
	}
	require.NoError(f.t, f.bus.PublishInbound(context.Background(), msg))
}

// routeMissing fails when the production mux has no WU-5 route (the contract
// says the K49 handler arrives with WU-5; the K6 list handler with WU-6).
func routeMissing(t *testing.T, rec *httptest.ResponseRecorder, what, spec string) {
	t.Helper()
	body := rec.Body.String()
	if rec.Code == http.StatusNotFound && strings.Contains(body, "page not found") {
		t.Fatalf("BLOCKED: %s is not registered on the production mux — required by %s (the contract ships the route; the handler is missing)", what, spec)
	}
}

// storePrivateVersion raises a private card on a fresh session, pins a
// stopper script (the submit's real resume continuation consumes it through
// the running loop consumer), submits the value through the real REST route
// and returns the stored reference names once the resume has run.
func (f *wu5GatewayFixture) storePrivateVersion(t *testing.T, workspace, value string) []string {
	t.Helper()
	sid := f.mainSession(workspace, "mia")
	requestID, err := f.requestPrivateCard(sid, "mia", nil)
	require.NoError(t, err, "BLOCKED: the agent_private card kind (request_secret with variable %q) is not implemented — required by US-7, FR-061/FR-065/FR-076", wu5Variable)
	f.provider.setScript(sid, []providers.LLMResponse{wu5StopGW("store resume done")})
	f.submitThroughREST(requestID, f.cardFieldKey(sid), value)
	f.provider.waitFor(sid, 1, "the submit's real resume continuation", t)
	return f.referenceNames(requestID)
}

// TestWU5SecretOptions_UnauthenticatedIs401 is K49's "authenticated web user
// only": without authentication the options flip is 401.
func TestWU5SecretOptions_UnauthenticatedIs401(t *testing.T) {
	f := newWU5GatewayFixture(t, false)
	rec := f.do(http.MethodPatch, fmt.Sprintf(wu5OptionsURL, "WR-some-reference"), `{"available_in_channel_turns":true}`)
	routeMissing(t, rec, "PATCH /api/v1/secrets/{reference}/options", "K49, FR-241")
	require.Equal(t, http.StatusUnauthorized, rec.Code, "unauthenticated options flip must be 401; got %d %s", rec.Code, rec.Body.String())
}

// TestWU5SecretOptions_UnknownReference404 is K49's 404 row: an
// authenticated caller and an unknown reference → 404.
func TestWU5SecretOptions_UnknownReference404(t *testing.T) {
	f := newWU5GatewayFixture(t, true)
	rec := f.do(http.MethodPatch, fmt.Sprintf(wu5OptionsURL, "WR-not-a-reference"), `{"available_in_channel_turns":true}`)
	routeMissing(t, rec, "PATCH /api/v1/secrets/{reference}/options", "K49")
	require.Equal(t, http.StatusNotFound, rec.Code, "unknown reference must be 404; got %d %s", rec.Code, rec.Body.String())
}

// TestWU5SecretOptions_PrivateKindOnly is K49's "private kind only; another
// kind is 422" row, driven against a real stored provider-key reference.
func TestWU5SecretOptions_PrivateKindOnly(t *testing.T) {
	f := newWU5GatewayFixture(t, true)
	// Store a real NON-private (provider endpoint) reference through the real
	// card flow, then try to flip its channel flag.
	sid := f.mainSession("sales", "mia")
	ctx := tools.WithTurnOrigin(
		tools.WithAgentID(
			tools.WithTranscriptSessionID(
				tools.WithToolContext(context.Background(), "webchat", sid),
				sid),
			"mia"),
		tools.NewTurnOrigin(),
	)
	tool := tools.NewRequestSecretTool(func() *secretrequest.Registry { return f.al.SecretRequests() }, nil)
	result := tool.Execute(ctx, map[string]any{"purpose": "OpenAI setup key", "endpoint": "https://api.openai.com/v1"})
	require.False(t, result.IsError, "the provider-key card is WU-4 behaviour and must work: %s", result.ForLLM)
	requestID := ""
	for _, card := range f.al.SecretRequests().Snapshot(wu5Owner, sid) {
		if card.State == "pending" {
			requestID = card.RequestId
		}
	}
	require.NotEmpty(t, requestID)
	f.submitThroughREST(requestID, f.cardFieldKey(sid), "sk-fake-wu5-control")
	ref := f.referenceNames(requestID)[0]

	rec := f.do(http.MethodPatch, fmt.Sprintf(wu5OptionsURL, ref), `{"available_in_channel_turns":true}`)
	routeMissing(t, rec, "PATCH /api/v1/secrets/{reference}/options", "K49")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code,
		"the channel flag is private-kind only; a provider-key reference must be 422 (K49); got %d %s", rec.Code, rec.Body.String())
}

// TestWU5SecretOptions_ChannelFlag_GatesInjection is BDD-7.12's "on" row plus
// K49's contract: the flip returns the updated entry, an audit entry names
// the change, and a channel-triggered turn on the SAME session starts seeing
// the value while the flag is on (the default-off row and the web control
// live in the agent pack, pkg/agent — BDD-7.12 rows 1 and 3).
func TestWU5SecretOptions_ChannelFlag_GatesInjection(t *testing.T) {
	f := newWU5GatewayFixture(t, true)
	refs := f.storePrivateVersion(t, "sales", wu5ValueV1)
	require.Len(t, refs, 1)

	// Default off: the channel-triggered turn must not see the value.
	sid := f.mainSession("sales", "mia")
	f.provider.setScript(sid, []providers.LLMResponse{
		wu5BashGW(`printf 'len=%s' "${#WEATHER_TOKEN}"`), wu5StopGW("off check"),
	})
	f.channelTurnGW(sid)
	f.provider.waitFor(sid, 2, "default-off channel turn", t)
	offOut := strings.Join(f.provider.toolResults(sid), "\n")
	require.Contains(t, offOut, "len=0", "instrument: with the flag default off, the channel turn must not see the value (FR-241 default)")

	// Flip the flag through the real route; the response is the updated entry.
	rec := f.do(http.MethodPatch, fmt.Sprintf(wu5OptionsURL, refs[0]), `{"available_in_channel_turns":true}`)
	routeMissing(t, rec, "PATCH /api/v1/secrets/{reference}/options", "K49")
	require.Equal(t, http.StatusOK, rec.Code, "the flip on a selected private reference must be 200; got %d %s", rec.Code, rec.Body.String())
	var updated gen.SecretNameEntry
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &updated), "the 200 body is the updated SecretNameEntry: %s", rec.Body.String())
	require.NotNil(t, updated.AvailableInChannelTurns, "the echoed entry carries available_in_channel_turns")
	require.True(t, *updated.AvailableInChannelTurns, "the echo shows the flag flipped on")
	entry := f.entryFor(refs)
	require.NotNil(t, entry.AvailableInChannelTurns, "the listing carries available_in_channel_turns")
	require.True(t, *entry.AvailableInChannelTurns, "the listing must persist the flipped flag")

	// Flag on: a NEW channel-triggered turn sees the value.
	f.provider.setScript(sid, []providers.LLMResponse{
		wu5BashGW(`printf 'len=%s' "${#WEATHER_TOKEN}"`), wu5StopGW("on check"),
	})
	f.channelTurnGW(sid)
	f.provider.waitFor(sid, 2, "flag-on channel turn", t)
	onOut := strings.Join(f.provider.toolResults(sid), "\n")
	require.Contains(t, onOut, "len=16", "with the flag on, the channel-triggered turn sees the value (BDD-7.12 row 'on'; FR-241)")

	// Audited: the audit log names the reference and the option.
	auditBytes, err := os.ReadFile(filepath.Join(f.home, "system", "audit.jsonl"))
	require.NoError(t, err, "the audit log must exist under the data home")
	require.Contains(t, string(auditBytes), refs[0], "the options flip must be audited naming the reference (K49)")
	require.Contains(t, string(auditBytes), "available_in_channel_turns", "the audit entry must name the changed option (K49)")
}

// TestWU5PrivateVersion_SelectedOnSubmit_ReplacementSupersedes is BDD-6.19 +
// BDD-6.20 + BDD-7.11 (FR-086, FR-072, Q7 A): the first version is selected
// on submit; a replacement submits to selected with no second approval; the
// previous version becomes superseded and stays listed.
func TestWU5PrivateVersion_SelectedOnSubmit_ReplacementSupersedes(t *testing.T) {
	f := newWU5GatewayFixture(t, true)
	v1Refs := f.storePrivateVersion(t, "sales", wu5ValueV1)
	require.Len(t, v1Refs, 1)
	v1 := f.entryFor(v1Refs)
	require.Equal(t, gen.SecretNameEntryStateSelected, v1.State, "the first version is selected at once (BDD-6.19; FR-086)")
	require.Equal(t, "agent_private", string(v1.Kind), "the kind is agent_private (K6)")
	require.NotNil(t, v1.Owner, "the entry carries the owner pair (K6, CRIT-001 scope)")
	require.Equal(t, "mia", v1.Owner.AgentId, "owner pair names the agent")
	require.Equal(t, "sales", v1.Owner.WorkspaceId, "owner pair names the workspace")
	require.NotNil(t, v1.AvailableInChannelTurns, "available_in_channel_turns is present (K6)")
	require.False(t, *v1.AvailableInChannelTurns, "available_in_channel_turns defaults false (K6, FR-241)")

	// Replacement: selected on submit, no approval step anywhere (Q7 A).
	v2Refs := f.storePrivateVersion(t, "sales", wu5ValueV2)
	require.Len(t, v2Refs, 1)
	require.NotEqual(t, v1Refs[0], v2Refs[0], "a replacement stores a NEW versioned reference (FR-072)")
	v2 := f.entryFor(v2Refs)
	require.Equal(t, gen.SecretNameEntryStateSelected, v2.State, "the replacement is selected at once (BDD-6.20)")
	require.Equal(t, int64(2), v2.Version, "the replacement is version 2 (FR-072)")
	v1 = f.entryFor(v1Refs)
	require.Equal(t, gen.SecretNameEntryStateSuperseded, v1.State, "the previous version becomes superseded (FR-072)")

	// K6 lists names and metadata, never a value.
	rec := f.do(http.MethodGet, "/api/v1/secrets", "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), wu5ValueV1, "the listing must never contain a value (K6: names and metadata only)")
	require.NotContains(t, rec.Body.String(), wu5ValueV2, "the listing must never contain a value (K6)")
}

// TestWU5PrivateVersion_ConcurrentSubmits_ExactlyOneSelected is BDD-6.22:
// two cards for the SAME variable submitted at the same time resolve to
// exactly one selected version (the last committed); the other is superseded.
func TestWU5PrivateVersion_ConcurrentSubmits_ExactlyOneSelected(t *testing.T) {
	f := newWU5GatewayFixture(t, true)

	// Two pending cards for the SAME (sales, mia, WEATHER_TOKEN) pair — two
	// chats of one pair, the world BDD-6.22 describes.
	type pending struct {
		sid, requestID, fieldKey string
	}
	cards := make([]pending, 0, 2)
	for i := 0; i < 2; i++ {
		sid := f.chatSession("sales", "mia")
		requestID, err := f.requestPrivateCard(sid, "mia", nil)
		require.NoError(t, err, "BLOCKED: the agent_private card kind is not implemented (US-7, FR-076)")
		cards = append(cards, pending{sid: sid, requestID: requestID, fieldKey: f.cardFieldKey(sid)})
	}

	// Submit both at the same time through the real route.
	var wg sync.WaitGroup
	for _, c := range cards {
		wg.Add(1)
		go func(c pending) {
			defer wg.Done()
			body, err := json.Marshal(gen.SecretSubmitRequest{Values: &map[string]string{c.fieldKey: wu5ValueV1}})
			require.NoError(t, err)
			rec := f.do(http.MethodPost, "/api/v1/secret-requests/"+c.requestID+"/submit", string(body))
			require.Equal(t, http.StatusOK, rec.Code, "both concurrent submits commit (BDD-6.22); got %d %s", rec.Code, rec.Body.String())
		}(c)
	}
	wg.Wait()

	// Exactly one selected, the other superseded.
	states := map[gen.SecretNameEntryState]int{gen.SecretNameEntryStateSelected: 0, gen.SecretNameEntryStateSuperseded: 0}
	refs := 0
	for _, entry := range f.listSecrets() {
		if entry.Kind != gen.SecretNameEntryKindAgentPrivate {
			continue
		}
		refs++
		states[entry.State]++
	}
	require.Equal(t, 2, refs, "both committed versions are listed (K6)")
	require.Equal(t, 1, states["selected"], "exactly one version ends selected (BDD-6.22)")
	require.Equal(t, 1, states["superseded"], "the other version is listed as superseded (BDD-6.22)")
}

// seedWU5Membership writes real workspace membership files (the shape
// pkg/workspace.FindForAgent reads): sales claims mia; marketing claims only
// ray — so a target of (marketing, mia) is a target where Mia is not a member.
func seedWU5Membership(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "workspaces")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sales.json"),
		[]byte(`{"id":"sales","core_team":["mia"]}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "marketing.json"),
		[]byte(`{"id":"marketing","core_team":["ray"]}`), 0o644))
}

// TestWU5PrivateSecret_OmnipusTargetValidated is BDD-7.8 / US-7 AS-1
// (alternate path): Omnipus requests a private secret with target Sales/mia —
// the card shows "Mia in Sales", the first version is selected on submit and
// Mia's next Sales command sees it; a target where Mia is not a member is
// refused (FR-065: the validated target; CRIT-001 scope).
func TestWU5PrivateSecret_OmnipusTargetValidated(t *testing.T) {
	f := newWU5GatewayFixture(t, true)
	seedWU5Membership(t, f.home)

	// Omnipus raises the card for the validated target (sales, mia).
	omniSession := f.mainSession("sales", "omnipus")
	requestID, err := f.requestPrivateCard(omniSession, "omnipus", map[string]any{"workspace_id": "sales", "agent_id": "mia"})
	require.NoError(t, err, "BLOCKED: request_secret cannot request a private secret for a target pair — required by BDD-7.8, FR-065/FR-076")

	// The card shows the target pair: "Mia in Sales" plus the variable (K2's
	// display lines for the private kind: workspace + agent + variable).
	var card gen.SessionStateSecretRequest
	for _, c := range f.al.SecretRequests().Snapshot(wu5Owner, omniSession) {
		if c.RequestId == requestID {
			card = c
		}
	}
	shown := card.Purpose + "\n" + strings.Join(card.Recipient.Lines, "\n")
	require.Contains(t, shown, "Mia in Sales", "the card must show the target pair (BDD-7.8 quotes the wording); got %q", shown)
	require.Contains(t, shown, wu5Variable, "the card must show the variable name (K2)")
	require.NotNil(t, card.Owner, "the target pair is the card's owner pair (K1/K4)")
	require.Equal(t, "mia", card.Owner.AgentId)
	require.Equal(t, "sales", card.Owner.WorkspaceId)

	// First version is selected on submit, and Mia's next Sales command sees it.
	f.provider.setScript(omniSession, []providers.LLMResponse{wu5StopGW("store resume done")})
	f.submitThroughREST(requestID, f.cardFieldKey(omniSession), wu5ValueV1)
	refs := f.referenceNames(requestID)
	require.Len(t, refs, 1)
	entry := f.entryFor(refs)
	require.Equal(t, gen.SecretNameEntryStateSelected, entry.State, "as the first version it is selected at once (BDD-7.8)")

	miaSession := f.mainSession("sales", "mia")
	f.provider.setScript(miaSession, []providers.LLMResponse{
		wu5BashGW(`printf 'len=%s' "${#WEATHER_TOKEN}"`), wu5StopGW("seen"),
	})
	f.webTurnGW(miaSession)
	f.provider.waitFor(miaSession, 2, "Mia's next Sales command", t)
	require.Contains(t, strings.Join(f.provider.toolResults(miaSession), "\n"), "len=16",
		"Mia's next Sales command sees the Omnipus-set value (BDD-7.8)")

	// A target where Mia is not a member is refused (marketing claims ray).
	badSession := f.mainSession("marketing", "omnipus")
	_, refusErr := f.requestPrivateCard(badSession, "omnipus", map[string]any{"workspace_id": "marketing", "agent_id": "mia"})
	require.Error(t, refusErr, "a target where Mia is not a member must be refused (BDD-7.8)")
}

// webTurnGW drives a real web-app turn through the running loop consumer.
func (f *wu5GatewayFixture) webTurnGW(sid string) {
	f.t.Helper()
	msg := bus.InboundMessage{Channel: "webchat", ChatID: sid, SessionID: sid, Content: "check the weather"}
	require.NoError(f.t, f.bus.PublishInbound(context.Background(), msg))
}

// TestWU5PrivateVersion_OmnipusRollbackOnly is BDD-6.23 (FR-086): ordinary
// agents have no select_secret_version; only Omnipus may select an older
// version of a validated target pair, and only through its Ask-gated tool.
//
// Train-2 scope note (recorded in the acceptance map): the approval-driven
// rollback leg rides the approval machinery the founder assigned to Train 3
// (plan §11, 2026-10-18: approvals.go, ws_tool_approval.go, policy_approver.go
// are Train-3 files), so the legs asserted here are the surface fact — the
// tool exists in the central catalog (WU-5's deliverable) — plus the
// ordinary-agent refusal; the approval-round-trip leg is driven in Train 3's
// pack.
func TestWU5PrivateVersion_OmnipusRollbackOnly(t *testing.T) {
	f := newWU5GatewayFixture(t, true)

	// The tool exists (WU-5's surface deliverable) — absence in the catalog
	// is the missing implementation, reported BLOCKED. The catalog is built
	// by the same builder gateway boot uses.
	registry, _ := buildCentralBuiltinRegistry(nil)
	found := false
	for _, entry := range registry.Describe() {
		if entry.Name == "select_secret_version" {
			found = true
		}
	}
	if !found {
		t.Fatal("BLOCKED: select_secret_version is not in the central builtin catalog — required by BDD-6.23 / FR-086 (Omnipus-only rollback of a private version)")
	}

	// An ordinary agent (mia) has no select_secret_version: a real turn call
	// must be refused by the execution gate, never executed.
	sales := f.mainSession("sales", "mia")
	f.provider.setScript(sales, []providers.LLMResponse{
		{ToolCalls: []providers.ToolCall{{ID: "wu5-rollback-attempt", Type: "function", Name: "select_secret_version",
			Arguments: map[string]any{"reference": "WR-any", "version": int64(1)}}}},
		wu5StopGW("after attempt"),
	})
	f.webTurnGW(sales)
	f.provider.waitFor(sales, 2, "mia's rollback attempt", t)
	for _, r := range f.provider.toolResults(sales) {
		if strings.Contains(r, "selected") && strings.Contains(r, "WR-any") {
			t.Fatalf("an ordinary agent's select_secret_version attempt must be refused (BDD-6.23), got a selection result: %q", r)
		}
	}
}
