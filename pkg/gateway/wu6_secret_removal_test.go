// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// WU-6 RED tests: secret listing (list_secrets tool, FR-073; GET /api/v1/secrets,
// K6 — the contract assigns this handler to WU-6) and removal (remove_secret
// tool, FR-077: consumers listed before the approval; separate result facts;
// with #1277 landed an in-use removal is approved, applies live and leaves
// the next boot degraded).
//
// Consumers are REAL: the #1277 fixture boots the real credential store, an
// enabled LINE channel instance holding ChannelSecretRef/ChannelAccessTokenRef
// and a custom provider holding APIKeyRef, with the real channel manager,
// real reload machinery and real bootCredentials. Removal rides the real
// approval machinery (approvalRegistryV2 + policy approver adapter), resolved
// in-process exactly like task_run_ask_approval_test.go does.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/gateway/ctxkey"
	"github.com/elicify-ai/omnipus/pkg/health"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/elicify-ai/omnipus/pkg/session"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	wu6UnusedRef    = "WU6_UNUSED_REF"
	wu6UnusedValue  = "wu6-unused-value-at-least-4-bytes"
	wu6VariableName = "WEATHER_TOKEN"
	wu6PrivateValue = "WU6_PRIVATE_V1_VALUE"
	wu6PrivateAlt   = "WU6_PRIVATE_V2_VALUE"
	wu6Owner        = "_dev_bypass" // the identity DevModeBypass authenticates as (auth.go); the session ancestry owner must match it
)

// wu6ScriptedProvider returns a fixed script of responses per transcript
// session, falling back to one default script when a session has none (the
// ProcessDirect turn path's transcript id is resolved inside the loop).
type wu6ScriptedProvider struct {
	mu            sync.Mutex
	calls         map[string]int
	scripts       map[string][]providers.LLMResponse
	defaultScript []providers.LLMResponse
	prompts       map[string][][]providers.Message
}

func newWU6ScriptedProvider() *wu6ScriptedProvider {
	return &wu6ScriptedProvider{calls: map[string]int{}, scripts: map[string][]providers.LLMResponse{}, prompts: map[string][][]providers.Message{}}
}

func (p *wu6ScriptedProvider) setDefaultScript(script []providers.LLMResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.defaultScript = script
}

// appendDefaultScript extends the default script in place. The model-call
// counter is cumulative across a session's resumed turns, so a mid-flow
// step (the connect after two card resumes) must be APPENDED — replacing
// the script would leave the counter past its end and the next model call
// would read "script exhausted".
func (p *wu6ScriptedProvider) appendDefaultScript(entries []providers.LLMResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.defaultScript = append(p.defaultScript, entries...)
}

func (p *wu6ScriptedProvider) GetDefaultModel() string { return "test-model" }

func (p *wu6ScriptedProvider) Chat(ctx context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	sid := tools.ToolTranscriptSessionID(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.calls[sid]
	p.calls[sid] = n + 1
	p.prompts[sid] = append(p.prompts[sid], append([]providers.Message(nil), messages...))
	script := p.scripts[sid]
	if len(script) == 0 {
		script = p.defaultScript
	}
	if n >= len(script) {
		return &providers.LLMResponse{Content: "WU6 SCRIPT EXHAUSTED"}, nil
	}
	return &script[n], nil
}

func (p *wu6ScriptedProvider) allToolResults() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, turns := range p.prompts {
		for _, msgs := range turns {
			for _, m := range msgs {
				if m.Role == "tool" {
					out = append(out, m.Content)
				}
			}
		}
	}
	return strings.Join(out, "\n")
}

// lastToolResult returns the most recent tool message the model was shown —
// the final tool step's report. The honest-result oracles judge THIS text:
// a ToolSearch promotion result embeds whole tool schemas, whose
// descriptions legitimately contain words like "working", so the
// working/not-working word checks must read the step's own report, not the
// catalog dump.
func (p *wu6ScriptedProvider) lastToolResult() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out string
	for _, turns := range p.prompts {
		for _, msgs := range turns {
			for _, m := range msgs {
				if m.Role == "tool" {
					out = m.Content
				}
			}
		}
	}
	return out
}

// wu6Fixture: the #1277 fixture (real consumers, store, reload, boot) plus a
// scripted-provider runtime, the real approval machinery, the production
// route table and the loop's bus consumer running.
type wu6Fixture struct {
	t              *testing.T
	f1277          *issue1277Fixture
	api            *restAPI
	svc            *services
	al             *agent.AgentLoop
	bus            *bus.MessageBus
	mux            *http.ServeMux
	provider       *wu6ScriptedProvider
	reg            *approvalRegistryV2
	wsConns        []*wsConn
	store          *credentials.Store
	removeTurnDone chan error
}

// wireWU6ScriptedProviderPool replaces only the fixture agents' named
// qa1277 connection, not model-selection rules or the HTTP protocol fixture.
// A reload rebuilds the registry/pools, so newWU7LiveFixture calls this again
// inside its reload executor before the completion flag is released.
func wireWU6ScriptedProviderPool(al *agent.AgentLoop, provider *wu6ScriptedProvider) error {
	registry := al.GetRegistry()
	ids := registry.ListAgentIDs()
	if len(ids) == 0 {
		return fmt.Errorf("WU6 scripted provider wiring: no fixture agents are registered")
	}
	for _, id := range ids {
		inst, exists := registry.GetAgent(id)
		if !exists {
			return fmt.Errorf("WU6 scripted provider wiring: agent %q disappeared", id)
		}
		inst.StoreProviderPool(map[string]providers.LLMProvider{"qa1277": provider})
	}
	return nil
}

func newWU6Fixture(t *testing.T) *wu6Fixture {
	t.Helper()
	f := newIssue1277Fixture(t)
	// The WU-6 agents are REAL entity records (ADR-054: config.json's
	// agents.list is stripped on every load; the roster comes from the agent
	// entity store), and the two tools ride the ceiling entries (list_secrets
	// allow, remove_secret ask — FR-021) set on the saved config.
	astore := agentstore.New(f.home)
	require.NoError(t, astore.Create("omnipus", &config.AgentConfig{ID: "omnipus", Name: "Omnipus", Type: config.AgentTypeCore}))
	require.NoError(t, astore.Create("mia", &config.AgentConfig{ID: "mia", Name: "Mia", Type: config.AgentTypeCustom}))
	// REST-surface tests drive the production route table, whose /api/v1/secrets
	// handler sits behind withAuth (rest.go::registerAdditionalEndpoints).
	// DevModeBypass is what admits the fixture's header-less do() requests:
	// checkBearerAuth authenticates them as the synthetic _dev_bypass user
	// (auth.go::checkBearerAuth) — the same identity wu6Owner names, so
	// mainSession's owner stamps match the caller the way production sees them.
	f.cfg.Gateway.DevModeBypass = true
	if f.cfg.Sandbox.ToolPolicies == nil {
		f.cfg.Sandbox.ToolPolicies = map[string]string{}
	}
	f.cfg.Sandbox.ToolPolicies["list_secrets"] = "allow"
	f.cfg.Sandbox.ToolPolicies["remove_secret"] = "ask"
	f.cfg.Sandbox.ToolPolicies["request_secret"] = "allow"
	f.cfg.Sandbox.ToolPolicies["bash"] = "allow"
	require.NoError(t, config.SaveConfig(f.configPath, f.cfg))

	provider := newWU6ScriptedProvider()
	cfg, bundle, store := f.boot(t)
	require.NoError(t, populateAgentsListFromEntityStoreStrict(cfg, f.home), "the entity roster must populate the loop's config")
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	al := mustAgentLoop(t, cfg, msgBus, provider)
	// #1340: turns use the agent's pinned (qa1277, health-probe)
	// connection, never the injected default client. Keep the #1277 real
	// HTTP fixture for integration probes, but route model turns to the
	// session-aware script through that named connection explicitly.
	require.NoError(t, wireWU6ScriptedProviderPool(al, provider))
	al.SetCredentialResolver(store.Get)
	mgr, err := channels.NewManager(cfg, bundle, msgBus, nil)
	require.NoError(t, err, "credential-degraded channels must not abort manager construction")
	hs := health.NewServer("127.0.0.1", 0)
	mgr.SetupHTTPServer("127.0.0.1:0", hs)
	al.SetChannelManager(mgr)
	api := &restAPI{agentLoop: al, homePath: f.home, credStore: store}
	svc := &services{
		homePath: f.home, ChannelManager: mgr, HealthServer: hs, bundle: bundle, credStore: store,
		restAPIRef: api, reloadOutcome: &reloadOutcomeTracker{}, manualReloadChan: make(chan struct{}, 1),
	}
	api.reloadOutcome = svc.reloadOutcome
	hs.SetDegradedFunc(svc.reloadDegradedStatus)
	svc.loadConfigForSwap = func() (*config.Config, error) { return config.LoadConfigWithStore(f.configPath, store) }
	// Match the production config-write dependencies. Journeys that need a
	// real reload opt in through newWU7LiveFixture; the secret-listing,
	// attention and pairing fixtures keep their existing runtime boundary.
	al.WireSysagentDeps(&systools.Deps{
		Home:             f.home,
		ConfigPath:       f.configPath,
		GetCfg:           al.GetConfig,
		MutateConfig:     al.MutateConfig,
		SaveConfigLocked: func(c *config.Config) error { return config.SaveConfig(f.configPath, c) },
		CredStore:        store,
	})
	t.Cleanup(func() { stopAndCleanupServices(svc, 5*time.Second, false) })
	require.NoError(t, mgr.StartAll(context.Background()), "the shared listener and healthy channels must start")

	// The production route table for REST-surface tests.
	mux := http.NewServeMux()
	api.registerAdditionalEndpoints(&testMuxRegistrar{mux: mux})

	// The real approval machinery, wired production-style, with capture conns.
	reg := newApprovalRegistryV2(64, 10*time.Minute)
	wsHandler := &WSHandler{agentLoop: al, approvalRegV2: reg}
	al.SetToolApprover(newPolicyApproverAdapter(reg, wsHandler))
	conns := apprResAttachConns(t, wsHandler, 1)
	t.Cleanup(func() {
		for _, e := range reg.pendingApprovals() {
			reg.resolve(e.ApprovalID, ApprovalActionCancel, false)
		}
	})

	// The loop's real inbound consumer (submit continuations and turns).
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go al.Run(runCtx)

	return &wu6Fixture{t: t, f1277: f, api: api, svc: svc, al: al, bus: msgBus, mux: mux, provider: provider, reg: reg, wsConns: conns, store: store}
}

// do issues a request through the production route table.
func (w *wu6Fixture) do(method, path, body string) *httptest.ResponseRecorder {
	w.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = uniqueTestSourceIP()
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), ctxkey.ConfigContextKey{}, w.api.agentLoop.GetConfig())
	rec := httptest.NewRecorder()
	w.mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

// mainSession stamps the owner the registry derives record ownership from.
func (w *wu6Fixture) mainSession(workspace, agentID string) string {
	w.t.Helper()
	s, err := w.al.GetSessionStore().GetOrCreateMainSession(workspace, agentID)
	require.NoError(w.t, err)
	owner := wu6Owner
	require.NoError(w.t, w.al.GetSessionStore().SetMeta(s.ID, session.MetaPatch{Owner: &owner}))
	return s.ID
}

// commitPrivateEntry stores an agent_private-shaped entry through the REAL
// card-commit path (the same Store.CommitCard the K5 submit calls). The WU-6
// subject is listing/removal, not card admission (WU-5's), so the tests
// commit their fixture entries directly and stay independent of WU-5.
var wu6CommitSeq atomic.Int64

func (w *wu6Fixture) commitPrivateEntry(t *testing.T, sid, workspace, agentID, variable, value string) []string {
	t.Helper()
	recipient := credentials.CardRecipient{Kind: "agent_private", WorkspaceID: workspace, AgentID: agentID, Field: variable}
	values := []credentials.CardValue{{BaseName: secretrequest.ReferenceBase(recipient), Recipient: recipient, Value: value}}
	requestID := fmt.Sprintf("wu6-%s-%s-%d", sid, variable, wu6CommitSeq.Add(1))
	receipt, err := w.store.CommitCard(requestID, sid, values, time.Now())
	require.NoError(t, err, "instrument: the real card-commit path must store the private entry")
	require.NotEmpty(t, receipt.References)
	return receipt.References
}

// issueRemove drives one real remove_secret call through the loop
// (ProcessDirectWithChannelAs, the exported production entry) in a goroutine
// — the ask-gated tool parks the turn — and returns the pending approval.
// approveRemove then resolves it and waits for the turn to finish.
func (w *wu6Fixture) issueRemove(ref string) *approvalEntry {
	w.t.Helper()
	w.provider.setDefaultScript([]providers.LLMResponse{
		// remove_secret is an ADR-071 lazy (Tier-3) tool: callable only once
		// ToolSearch promotes it into the offered set
		// (pkg/agent/loop_run_turn_tools.go::toolNotOfferedRefusal). The
		// promotion step is part of the real flow — ToolSearch's own
		// description says "After loading, call the tool directly".
		{ToolCalls: []providers.ToolCall{{ID: "wu6-load-" + ref, Type: "function", Name: "ToolSearch",
			Arguments: map[string]any{"names": []any{"remove_secret"}}}}},
		{ToolCalls: []providers.ToolCall{{ID: "wu6-remove-" + ref, Type: "function", Name: "remove_secret",
			// reference_name is the tool schema's parameter
			// (pkg/sysagent/tools/secret_remove.go::Parameters, required,
			// additionalProperties false); the older "reference" spelling
			// was only ever a developer-side fallback being removed.
			Arguments: map[string]any{"reference_name": ref}}}},
		{Content: "done", FinishReason: "stop"},
	})
	w.removeTurnDone = make(chan error, 1)
	go func() {
		_, err := w.al.ProcessDirectWithChannelAs(context.Background(), "omnipus", "remove the secret "+ref, "wu6-turn", "webchat", "wu6-chat")
		w.removeTurnDone <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		pending := w.reg.pendingApprovals()
		if len(pending) == 1 && pending[0].ToolName == "remove_secret" {
			return pending[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	w.t.Fatalf("BLOCKED: no pending remove_secret approval appeared — the tool is forward-reserved only (pkg/tools/manifest.go), required by FR-077, BDD-15.1")
	return nil
}

// approveRemove resolves a pending removal and waits for the parked turn to
// finish.
func (w *wu6Fixture) approveRemove(entry *approvalEntry) {
	w.t.Helper()
	resolved, _ := w.reg.resolve(entry.ApprovalID, ApprovalActionApprove, false)
	require.True(w.t, resolved, "the pending removal approval must resolve")
	select {
	case err := <-w.removeTurnDone:
		require.NoError(w.t, err, "the approved removal turn must complete")
	case <-time.After(15 * time.Second):
		w.t.Fatal("the removal turn never finished after the approval")
	}
}

// denyRemove resolves a pending removal with deny and waits for the turn.
func (w *wu6Fixture) denyRemove(entry *approvalEntry) {
	w.t.Helper()
	resolved, _ := w.reg.resolve(entry.ApprovalID, ApprovalActionDeny, false)
	require.True(w.t, resolved, "the pending removal approval must resolve")
	select {
	case err := <-w.removeTurnDone:
		require.NoError(w.t, err, "the denied removal turn must complete")
	case <-time.After(15 * time.Second):
		w.t.Fatal("the denial turn never finished")
	}
}

// loopTool looks a builtin tool up on the acting agent's live registry.
func (w *wu6Fixture) loopTool(agentID, name string) tools.Tool {
	w.t.Helper()
	agentInstance, ok := w.al.GetRegistry().GetAgent(agentID)
	require.True(w.t, ok, "fixture agent %s must exist", agentID)
	tool, ok := agentInstance.Tools.Get(name)
	if !ok {
		return nil
	}
	return tool
}

// --- listing -----------------------------------------------------------------

// TestWU6ListSecrets_ToolRegistered is FR-073's surface deliverable: the
// list_secrets tool exists in the central builtin catalog.
func TestWU6ListSecrets_ToolRegistered(t *testing.T) {
	registry, _ := buildCentralBuiltinRegistry(nil)
	for _, entry := range registry.Describe() {
		if entry.Name == "list_secrets" {
			return
		}
	}
	t.Fatal("BLOCKED: list_secrets is not in the central builtin catalog — required by FR-073, BDD-6.18 (ordinary and custom agents list their stored secrets)")
}

// TestWU6ListSecrets_ShapeAndNeverValues is FR-073: entries carry name, kind,
// version, recipient display, consumers and the in-use flag; never a value.
func TestWU6ListSecrets_ShapeAndNeverValues(t *testing.T) {
	w := newWU6Fixture(t)
	sid := w.mainSession("sales", "mia")
	refs := w.commitPrivateEntry(t, sid, "sales", "mia", wu6VariableName, wu6PrivateValue)
	_ = refs

	ctx := tools.WithAgentID(
		tools.WithTranscriptSessionID(
			tools.WithToolContext(context.Background(), "webchat", sid),
			sid),
		"mia")
	tool := w.loopTool("mia", "list_secrets")
	require.NotNil(t, tool, "BLOCKED: list_secrets is not registered on the agent's tool surface — required by FR-073, BDD-6.18")
	result := tool.Execute(ctx, map[string]any{})
	require.False(t, result.IsError, "list_secrets must run for the owning agent (FR-073): %s", result.ForLLM)
	require.Contains(t, result.ForLLM, wu6VariableName, "the stored private variable is listed")
	require.Contains(t, result.ForLLM, "agent_private", "the entry carries its kind (FR-073)")
	require.NotContains(t, result.ForLLM, wu6PrivateValue, "list_secrets must never return a value (FR-073)")
}

// TestWU6ListSecrets_AgentScoping is FR-073: an ordinary agent sees only its
// own private names in the current workspace; Omnipus sees all.
func TestWU6ListSecrets_AgentScoping(t *testing.T) {
	w := newWU6Fixture(t)
	miaSales := w.mainSession("sales", "mia")
	omniSales := w.mainSession("sales", "omnipus")

	miaRefs := w.commitPrivateEntry(t, miaSales, "sales", "mia", wu6VariableName, wu6PrivateValue)
	omniRefs := w.commitPrivateEntry(t, omniSales, "sales", "omnipus", wu6PrivateAlt, "wu6-omnipus-private-value")

	listFor := func(sid, agentID string) string {
		ctx := tools.WithAgentID(
			tools.WithTranscriptSessionID(
				tools.WithToolContext(context.Background(), "webchat", sid),
				sid),
			agentID)
		tool := w.loopTool(agentID, "list_secrets")
		require.NotNil(t, tool, "BLOCKED: list_secrets is not registered — required by FR-073")
		result := tool.Execute(ctx, map[string]any{})
		require.False(t, result.IsError, "list_secrets must run: %s", result.ForLLM)
		return result.ForLLM
	}

	miaOut := listFor(miaSales, "mia")
	require.Contains(t, miaOut, miaRefs[0], "Mia sees her own private name (FR-073)")
	require.NotContains(t, miaOut, omniRefs[0], "Mia must not see another pair's private name (FR-073 scoping)")

	omniOut := listFor(omniSales, "omnipus")
	require.Contains(t, omniOut, miaRefs[0], "Omnipus sees all stored names (FR-073)")
	require.Contains(t, omniOut, omniRefs[0], "Omnipus sees all stored names (FR-073)")
}

// TestWU6SecretsList_ConsumersFromConfig is K6/FR-073 on the web surface:
// GET /api/v1/secrets lists the stored references with consumers resolved
// from the live configuration, never a value.
func TestWU6SecretsList_ConsumersFromConfig(t *testing.T) {
	w := newWU6Fixture(t)
	rec := w.do(http.MethodGet, "/api/v1/secrets", "")
	if rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "page not found") {
		t.Fatalf("BLOCKED: GET /api/v1/secrets is not registered — the handler the contract assigns to WU-6 (K6, FR-073, FR-086, FR-241)")
	}
	require.Equal(t, http.StatusOK, rec.Code, "got %d %s", rec.Code, rec.Body.String())
	var entries []gen.SecretNameEntry
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &entries), "body must decode into SecretNameEntry values: %s", rec.Body.String())

	var healthy *gen.SecretNameEntry
	for i := range entries {
		if entries[i].Reference == issue1277HealthyRef {
			healthy = &entries[i]
		}
	}
	require.NotNil(t, healthy, "the stored ref consumed by the enabled channel must be listed (K6)")
	require.NotEmpty(t, healthy.Consumers, "consumers is never null and lists what uses this version (K6)")
	joined := strings.Join(healthy.Consumers, "\n")
	require.Contains(t, joined, "line.healthy", "the enabled channel instance consuming the ref must be listed (BDD-15.1's consumer fact)")
	require.NotContains(t, rec.Body.String(), "qa1277-healthy-secret", "the listing must never contain a value (K6)")
}

// --- removal -----------------------------------------------------------------

// TestWU6RemoveSecret_ConsumersListedBeforeApproval is FR-077 / BDD-15.1:
// the removal's approval card names every consumer BEFORE any resolution, and
// denying leaves the reference stored.
func TestWU6RemoveSecret_ConsumersListedBeforeApproval(t *testing.T) {
	w := newWU6Fixture(t)
	ref := issue1277HealthyRef // consumed by the ENABLED line.healthy channel

	entry := w.issueRemove(ref)
	// The approval card is the approver's view: its serialized payload (args
	// and any description segments on the frame) must name the consumers.
	frame := apprResReadFrame(t, w.wsConns[0], "tool_approval_required")
	encoded, err := json.Marshal(frame)
	require.NoError(t, err)
	shown := string(encoded) + fmt.Sprintf("%v", entry.Args)
	require.Contains(t, shown, "line.healthy",
		"the approval must list the consuming channel BEFORE the approval (FR-077, BDD-15.1); card showed: %s", shown)

	// Deny: nothing was removed — listing came strictly before any deletion.
	w.denyRemove(entry)
	_, getErr := w.store.Get(ref)
	require.NoError(t, getErr, "a denied removal must not delete the reference (consumers listed BEFORE the approval; BDD-15.1 ordering)")
}

// TestWU6RemoveSecret_UnusedDeletedAfterApproval is FR-077 / BDD-15.4 /
// BDD-15.3: an unused reference is deleted after the approval and the result
// reports durable deletion, live effect and boot validity separately.
func TestWU6RemoveSecret_UnusedDeletedAfterApproval(t *testing.T) {
	w := newWU6Fixture(t)
	require.NoError(t, w.store.Set(wu6UnusedRef, wu6UnusedValue))

	entry := w.issueRemove(wu6UnusedRef)
	w.approveRemove(entry)

	_, getErr := w.store.Get(wu6UnusedRef)
	var notFound *credentials.NotFoundError
	require.ErrorAs(t, getErr, &notFound, "the approved removal must delete the reference durably (BDD-15.4)")

	results := w.provider.allToolResults()
	require.Contains(t, results, wu6UnusedRef, "the removal result names the removed reference")
	lower := strings.ToLower(results)
	require.Contains(t, lower, "durable", "the result must report DURABLE deletion as its own fact (BDD-15.3, US-15 AS-3): %s", results)
	require.Contains(t, lower, "live", "the result must report the LIVE effect as its own fact (BDD-15.3): %s", results)
	require.Contains(t, lower, "boot", "the result must report BOOT validity as its own fact (BDD-15.3): %s", results)
}

// TestWU6RemoveSecret_InUse_Approved_DegradedNextBoot is FR-077 / BDD-15.3 /
// BDD-15.6 with #1277 landed (it is — see the acceptance map): an in-use
// removal is approved (never refused), lists its consumers, the live reload
// applies and degrades the affected integration, and the next real boot
// starts it degraded.
func TestWU6RemoveSecret_InUse_Approved_DegradedNextBoot(t *testing.T) {
	w := newWU6Fixture(t)
	// A second real consumer: a provider row holding the same ref.
	w.f1277.cfg.Providers = append(w.f1277.cfg.Providers, &config.ModelConfig{
		Provider: "qa1277-second", Model: "health-probe", Models: []string{"health-probe"}, Custom: true,
		Protocol: "openai-compatible", APIBase: "http://127.0.0.1:1/v1", APIKeyRef: issue1277HealthyRef,
	})
	require.NoError(w.t, config.SaveConfig(w.f1277.configPath, w.f1277.cfg))
	// Wire the real reload trigger (the #1277 deletion test drives it the
	// same way) so the approved removal's live apply is real.
	w.svc.reloadTrigger = newReloadTrigger(w.svc, w.api.agentLoop)
	w.api.agentLoop.SetReloadFunc(w.svc.reloadTrigger)
	var provider providers.LLMProvider = w.provider
	stop, drained := make(chan struct{}), make(chan struct{})
	w.t.Cleanup(func() { close(stop); <-drained })
	go func() {
		defer close(drained)
		for {
			select {
			case <-stop:
				return
			case <-w.svc.manualReloadChan:
				runReloadCycle(w.api.agentLoop, w.svc, nil, 0, func(c *config.Config) error {
					return executeReload(context.Background(), w.api.agentLoop, c, &provider, w.svc, w.bus, false)
				}, w.svc.loadConfigForSwap)
			}
		}
	}()

	entry := w.issueRemove(issue1277HealthyRef)
	frame := apprResReadFrame(t, w.wsConns[0], "tool_approval_required")
	encoded, _ := json.Marshal(frame)
	require.Contains(t, string(encoded), "line.healthy", "the in-use removal's card lists the consuming channel (BDD-15.6: names both consumers)")

	w.approveRemove(entry)

	_, getErr := w.store.Get(issue1277HealthyRef)
	var notFound *credentials.NotFoundError
	require.ErrorAs(t, getErr, &notFound, "the approved removal is durable (BDD-15.6)")

	// The live reload applied: the consuming channel stopped, degraded.
	if ch, exists := w.svc.ChannelManager.GetChannel("line.healthy"); exists {
		require.False(w.t, ch.IsRunning(), "the consuming channel must stop using its removed credential (live effect)")
	}
	failed := w.svc.ChannelManager.FailedChannels()
	require.NotEmpty(w.t, failed, "the removed credential's integration must appear degraded after the live apply (BDD-15.6)")

	// The next real boot must not resurrect the removed credential (#1277
	// keeps the boot alive; the integration starts degraded because the saved
	// config still names the ref).
	_, nextBundle, _ := w.f1277.boot(w.t)
	require.NotContains(w.t, nextBundle, issue1277HealthyRef, "the next boot must not resurrect the removed credential")
}

// TestWU6RemoveSecret_SupersededPrivateVersion_CurrentUnaffected is
// FR-072 / BDD-15.5 / US-7 AS-6 ("listed and removable"): removing the
// superseded private version deletes exactly it; the selected version stays
// selected and stays listed.
func TestWU6RemoveSecret_SupersededPrivateVersion_CurrentUnaffected(t *testing.T) {
	w := newWU6Fixture(t)
	sid := w.mainSession("sales", "mia")
	v1Refs := w.commitPrivateEntry(t, sid, "sales", "mia", wu6VariableName, wu6PrivateValue)
	v2Refs := w.commitPrivateEntry(t, sid, "sales", "mia", wu6VariableName, wu6PrivateAlt)
	require.NotEqual(t, v1Refs[0], v2Refs[0], "the replacement stored a new version (FR-072)")

	entry := w.issueRemove(v1Refs[0])
	w.approveRemove(entry)

	_, v1Err := w.store.Get(v1Refs[0])
	var notFound *credentials.NotFoundError
	require.ErrorAs(t, v1Err, &notFound, "the superseded version is deleted (BDD-15.5)")
	_, v2Err := w.store.Get(v2Refs[0])
	require.NoError(t, v2Err, "the selected version must be untouched by the removal (BDD-15.5: tg-1 keeps working)")

	// And the pair's listing still shows v2, never the removed v1.
	ctx := tools.WithAgentID(
		tools.WithTranscriptSessionID(
			tools.WithToolContext(context.Background(), "webchat", sid),
			sid),
		"mia")
	tool := w.loopTool("mia", "list_secrets")
	require.NotNil(t, tool, "BLOCKED: list_secrets is not registered — required by FR-073")
	result := tool.Execute(ctx, map[string]any{})
	require.False(t, result.IsError, "list_secrets must run: %s", result.ForLLM)
	require.Contains(t, result.ForLLM, v2Refs[0], "the selected version stays listed (FR-073)")
	require.NotContains(t, result.ForLLM, v1Refs[0], "the removed version is gone from the listing")
}
