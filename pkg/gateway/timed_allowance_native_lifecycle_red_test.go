//go:build goolm && stdjson

package gateway

// CHECK F17/F21, C-26 End/S-122. Real unlocked entities, both native delete
// entry paths, the loop-owned grant store, a wired production EgressProxy,
// and exact issued tokens. Legacy Record* is ONLY a selective-clear
// instrument, not a timed answer. No whole delete/restart/result seam.
// Native setupAndStartServices/shutdown and same-home startup are driven
// below, first as a legacy clearing instrument.
//
// Part H (2026-11-26) replaced the last loud placeholder ("duration-bearing
// delete and gateway restart acceptance") with two real timed rows:
//
//   - C-26 End, "when the agent is deleted": both delete entries (REST and the
//     delete_agent tool, wired by the production composition root) end the
//     deleted agent's timed allowances of every kind and revoke exactly its
//     live egress token, while a peer's allowances and token run to the exact
//     end on the injected clock. The timed answers are recorded with the
//     store's duration-bearing calls (RecordFor, RecordPrefixGrantFor,
//     RecordPathGrantFor, RecordNetworkGrantFor).
//   - S-122, "A restart ends every allowance and nothing is written": the
//     real composition root is stopped and started over one home; a real
//     "Allow for 3 hours" answer over the real HTTP handler plus the narrow
//     kinds are gone in the new process, the next actual model call asks, and
//     config.json, every agent file and the system folder (without the audit
//     log, which records the answers, as timedCardHomeTree already excludes
//     it) are byte-identical before and after.
//
// Still not pinned here: that the gateway STARTS AgentLoop.RunAllowanceExpirySweep
// (the one-line launch inside runContextWithOptions.startBackgroundServices). A
// test cannot call that method without leaving the retention goroutines that
// read the package-level retention*SweepFn variables other tests write, a data
// race under -race; it needs a seam in production code (reported to team-lead).
// The loop's own sweep is pinned in pkg/agent's TestTimedAllowance_SweepRevokesLiveEgressToken.
// This file does not certify OS kernel enforcement: ST-29/ST-30 remain
// UNVERIFIED. GREEN/mutation proof belongs to independent CHECK.

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimedAllowance_NativeLifecycle(t *testing.T) {
	for _, via := range []string{"REST", "delete_agent tool"} {
		t.Run("C-26 "+via+" drops only the deleted agent and exact live token", func(t *testing.T) {
			h := newTimedCardHarness(t)
			proxy, request, host := newLifecycleProxy(t)
			h.api.agentLoop.WireTier13Deps(agent.Tier13Deps{EgressProxy: proxy})
			store := h.api.agentLoop.ApprovalGrants()
			for _, id := range []string{"test-agent", "peer-agent"} {
				recordLifecycleScopes(t, store, id, h.api.homePath, host)
				assertLifecycleScopes(t, store, id, h.api.homePath, host, true)
			}
			token := store.SessionEgressToken("old-closed-chat", "test-agent")
			peerToken := store.SessionEgressToken("old-closed-chat", "peer-agent")
			require.NotEmpty(t, token)
			require.NotEmpty(t, peerToken)
			require.NotEqual(t, token, peerToken)
			proxy.GrantRunHosts(token, []string{host})
			proxy.GrantRunHosts(peerToken, []string{host})
			request(token, true)
			request(peerToken, true)
			deleteLifecycleAgent(t, h, via)
			assertLifecycleScopes(t, store, "test-agent", h.api.homePath, host, false)
			assertLifecycleScopes(t, store, "peer-agent", h.api.homePath, host, true)
			assert.NotContains(t, store.NetworkTokensForSession("old-closed-chat"), token, "the deleted agent's exact issued token is removed")
			assert.Contains(t, store.NetworkTokensForSession("old-closed-chat"), peerToken, "unaffected peer's token survives")
			request(token, false)
			request(peerToken, true)
		})
		t.Run("C-26 timed allowances: "+via+" ends the deleted agent's and keeps the peer's to the exact end", func(t *testing.T) {
			testTimedDeleteKeepsPeerToTheExactEnd(t, via)
		})
	}
	t.Run("instrument native same-home gateway restart does not restore legacy consent", testTimedNativeGatewayRestart)
	t.Run("S-122 native gateway restart ends every timed allowance and writes nothing", testTimedNativeGatewayRestartEndsTimed)
}

// deleteLifecycleAgent deletes test-agent through one of the two native entries and requires the
// real entity to be gone (the peer stays).
func deleteLifecycleAgent(t *testing.T, h *timedCardHarness, via string) {
	t.Helper()
	if via == "REST" {
		w := httptest.NewRecorder()
		r := revisionedDeleteRequest(t, h.api, "test-agent")
		h.api.HandleAgents(w, withAdminRole(r))
		require.Equal(t, http.StatusOK, w.Code, "real unlocked custom-agent deletion: %s", w.Body.String())
		var response gen.ConfigurationMutationState
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
		assert.Equal(t, gen.ConfigurationMutationStatePersistenceStatusComplete, response.PersistenceStatus)
	} else {
		// Drive the actual gateway composition root, not a hand-invented
		// ClearAgent callback on test-created tool dependencies.
		rc := &runContextWithOptions{ctx: context.Background(), homePath: h.api.homePath,
			configPath: filepath.Join(h.api.homePath, "config.json"), agentLoop: h.api.agentLoop,
			runningServices: &services{restAPIRef: h.api}}
		rc.wireSystemTools()
		peer, ok := h.api.agentLoop.GetRegistry().GetAgent("peer-agent")
		require.True(t, ok)
		tool, ok := peer.Tools.Get("delete_agent")
		require.True(t, ok, "native delete_agent is actually registered by gateway wiring")
		state, err := agentstore.New(h.api.homePath).ReadState("test-agent")
		require.NoError(t, err)
		result := tool.Execute(context.Background(), map[string]any{"id": "test-agent", "revision": state.Revision, "confirm": true})
		require.NotNil(t, result)
		require.False(t, result.IsError, "native delete entry failed: %s", result.ForLLM)
	}
	require.NoFileExists(t, filepath.Join(h.api.homePath, "entities", "agents", "test-agent.json"), "real deleted entity, not a boolean")
	require.FileExists(t, filepath.Join(h.api.homePath, "entities", "agents", "peer-agent.json"))
}

// testTimedDeleteKeepsPeerToTheExactEnd is C-26 "End" (the agent is deleted) with real timed
// allowances: every agent holds one of every kind for 3 hours from the injected clock's start
// (12:00 on 2026-11-22, so the end is 15:00); the deleted agent's allowances and exact live token
// end at the delete, the peer's run to the exact end and not one nanosecond longer. The key is the
// agent id, so a lookup that is false after the delete is also what a new agent that reuses the id
// would see.
func testTimedDeleteKeepsPeerToTheExactEnd(t *testing.T, via string) {
	t.Helper()
	const length = 3 * time.Hour
	wantEnd := time.Date(2026, time.November, 22, 15, 0, 0, 0, time.UTC)
	h := newTimedCardHarness(t)
	clock := controlledTimedCardClock(t, h)
	proxy, request, host := newLifecycleProxy(t)
	h.api.agentLoop.WireTier13Deps(agent.Tier13Deps{EgressProxy: proxy})
	store := h.api.agentLoop.ApprovalGrants()
	tokens := map[string]string{}
	for _, id := range []string{"test-agent", "peer-agent"} {
		for kind, end := range recordTimedLifecycleScopes(t, store, id, h.api.homePath, host, length, true) {
			assert.Equal(t, wantEnd, end, "C-26 Length: %s's %s allowance ends 3 hours after 12:00 on the injected clock", id, kind)
		}
		assertLifecycleScopes(t, store, id, h.api.homePath, host, true)
		tokens[id] = store.SessionEgressToken("old-closed-chat", id)
		require.NotEmpty(t, tokens[id])
		proxy.GrantRunHosts(tokens[id], store.NetworkHostsFor("old-closed-chat", id))
	}
	require.NotEqual(t, tokens["test-agent"], tokens["peer-agent"])
	request(tokens["test-agent"], true)
	request(tokens["peer-agent"], true)

	deleteLifecycleAgent(t, h, via)

	assertLifecycleScopes(t, store, "test-agent", h.api.homePath, host, false)
	assertLifecycleScopes(t, store, "peer-agent", h.api.homePath, host, true)
	assert.NotContains(t, store.NetworkTokensForAgent("test-agent"), tokens["test-agent"], "the deleted agent's exact issued token is removed")
	assert.Contains(t, store.NetworkTokensForAgent("peer-agent"), tokens["peer-agent"], "the peer's token survives the delete")
	request(tokens["test-agent"], false)
	request(tokens["peer-agent"], true)

	clock.advance(length - time.Nanosecond)
	assertLifecycleScopes(t, store, "peer-agent", h.api.homePath, host, true)
	assertLifecycleScopes(t, store, "test-agent", h.api.homePath, host, false)
	clock.advance(time.Nanosecond)
	assertLifecycleScopes(t, store, "peer-agent", h.api.homePath, host, false)
}

// recordTimedLifecycleScopes records one timed allowance of every kind for agent id with the
// store's duration-bearing calls, the ones the answered cards use (C-26 "Which card offers what"):
// the ordinary tool as a whole (when withTool), one exact shell command, one command family, one
// path with its access, one named address. It returns the end the store reported for each kind.
func recordTimedLifecycleScopes(t *testing.T, s *security.ApprovalGrantStore, id, home, host string, length time.Duration, withTool bool) map[string]time.Time {
	t.Helper()
	ends := map[string]time.Time{}
	record := func(kind string, end time.Time, ok bool) {
		t.Helper()
		require.True(t, ok, "the store takes %s's timed %s answer", id, kind)
		ends[kind] = end
	}
	if withTool {
		end, ok := s.RecordFor(id, "delete_task", map[string]any{"task_id": "original"}, length)
		record("tool", end, ok)
	}
	end, ok := s.RecordFor(id, "bash", map[string]any{"command": "npm run test"}, length)
	record("shell_exact", end, ok)
	end, ok = s.RecordPrefixGrantFor(id, "bash", security.ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}, length)
	record("shell_family", end, ok)
	end, ok = s.RecordPathGrantFor(id, fspolicy.PathGrant{Path: filepath.Join(home, "out.pdf"), Access: fspolicy.PathGrantAccessWrite}, length)
	record("path", end, ok)
	end, ok = s.RecordNetworkGrantFor(id, []string{host}, length)
	record("network_hosts", end, ok)
	return ends
}

func recordLifecycleScopes(t *testing.T, s *security.ApprovalGrantStore, id, home, host string) {
	t.Helper()
	const sid = "old-closed-chat"
	require.True(t, s.Record(sid, id, "delete_task", map[string]any{"task_id": "original"}))
	require.True(t, s.Record(sid, id, "bash", map[string]any{"command": "npm run test"}))
	require.True(t, s.RecordPrefixGrant(sid, id, "bash", security.ShellPrefixGrant{Binary: "/usr/bin/npm", ArgPrefix: "run test"}))
	require.True(t, s.RecordPathGrant(sid, id, fspolicy.PathGrant{Path: filepath.Join(home, "out.pdf"), Access: fspolicy.PathGrantAccessWrite}))
	require.True(t, s.RecordNetworkGrantWithHosts(sid, id, []string{host}))
}

func assertLifecycleScopes(t *testing.T, s *security.ApprovalGrantStore, id, home, host string, active bool) {
	t.Helper()
	const sid = "old-closed-chat"
	assert.Equal(t, active, s.IsAllowed(sid, id, "delete_task", map[string]any{"task_id": "original"}), "%s ordinary grant", id)
	assert.Equal(t, active, s.IsAllowed(sid, id, "bash", map[string]any{"command": "npm run test"}), "%s exact shell grant", id)
	assert.Equal(t, active, s.IsPrefixAllowed(sid, id, "bash", "/usr/bin/npm", []string{"run", "test", "--", "--watch"}, false), "%s family grant", id)
	assert.Equal(t, active, s.HasNetworkGrant(sid, id), "%s network grant", id)
	if active {
		assert.Equal(t, []fspolicy.PathGrant{{Path: filepath.Join(home, "out.pdf"), Access: fspolicy.PathGrantAccessWrite}}, s.PathGrantsFor(sid, id))
		assert.Equal(t, []string{host}, s.NetworkHostsFor(sid, id))
	} else {
		assert.Empty(t, s.PathGrantsFor(sid, id), "%s path grant ended", id)
		assert.Empty(t, s.NetworkHostsFor(sid, id), "%s named addresses ended", id)
	}
}

func newLifecycleProxy(t *testing.T) (*sandbox.EgressProxy, func(string, bool), string) {
	t.Helper()
	const body = "C26-LIFECYCLE-UPSTREAM-9Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := io.WriteString(w, body)
		assert.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	host, _, err := net.SplitHostPort(u.Host)
	require.NoError(t, err)
	proxy, err := sandbox.NewEgressProxy(nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, proxy.Close()) })
	request := func(token string, allowed bool) {
		t.Helper()
		proxyURL := &url.URL{Scheme: "http", Host: proxy.Addr(), User: url.User(token)}
		transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
		r, err := client.Get(server.URL)
		require.NoError(t, err, "transport failure is not policy refusal")
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, r.Body.Close())
		if allowed {
			assert.Equal(t, http.StatusOK, r.StatusCode)
			assert.Equal(t, body, string(raw))
		} else {
			assert.Equal(t, http.StatusForbidden, r.StatusCode, "exact deleted token opens no NEW connection")
			assert.NotContains(t, string(raw), body)
		}
	}
	return proxy, request, host
}

// nativeGatewayFixture is one same-home native gateway: the actual composition root
// (setupAndStartServices) started and stopped over one home, one credential store key and one
// scripted model edge. Nothing in it supplies a verdict, a card or an allowance.
type nativeGatewayFixture struct {
	home   string
	script *exactActionScript
	start  func() (*agent.AgentLoop, *services, func())
	// reopenCredentials reopens the same credential store under the same key and home: shutdown
	// wiped the old handle, and a restart must not create a different install.
	reopenCredentials func()
}

func newNativeGatewayFixture(t *testing.T) *nativeGatewayFixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv(config.EnvConfig, filepath.Join(home, "config.json"))
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := config.DefaultConfig()
	cfg.Gateway.Host, cfg.Gateway.Port = "127.0.0.1", 0
	cfg.Agents.Defaults.Home = filepath.Join(home, "workspace")
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "exact-action-script"}
	cfg.Agents.Defaults.MaxToolIterations = 3
	cfg.Sandbox.AutoApprove, cfg.Sandbox.GodMode = false, false
	cfg.Sandbox.ToolPolicies["delete_task"] = "ask"
	cfg.Tools.Manifest.Compressed = false // Do not turn discovery into the subject.
	cfg.Agents.List = nil
	for _, id := range []string{"test-agent", "peer-agent"} {
		record := config.AgentConfig{ID: id, Name: id, Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}
		require.NoError(t, agentstore.New(home).Create(id, &record))
		cfg.Agents.List = append(cfg.Agents.List, record)
	}
	script := &exactActionScript{row: exactActionRow{tool: "delete_task", args: map[string]any{"task_id": "original"}}}
	fx := newNativeGatewayOver(t, home, cfg, script)
	fx.script = script
	return fx
}

// newNativeGatewayOver completes a same-home gateway fixture for a home whose agents are already
// persisted and a config the caller has built: it writes config.json, gives the agents their
// workspace membership and delegation graph, unlocks the credential store and returns the fixture
// whose start() boots the actual composition root over `provider`, the only scripted edge.
// newNativeGatewayFixture (delete_task rows, S-122) and the S-113 skill rows share it.
func newNativeGatewayOver(t *testing.T, home string, cfg *config.Config, provider providers.LLMProvider) *nativeGatewayFixture {
	t.Helper()
	raw, marshalErr := json.Marshal(cfg)
	require.NoError(t, marshalErr)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))
	ensureTestWorkspaceMembership(t, cfg)
	seedDefaultDelegationGraph(t, testHarnessAgentIDs(cfg))
	creds := newUnlockedStore(t, home)
	start := func() (*agent.AgentLoop, *services, func()) {
		t.Helper()
		msgBus := bus.NewMessageBus()
		al, err := agent.NewAgentLoop(cfg, msgBus, provider)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		var rs *services
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				if rs != nil {
					omnipusGracefulShutdown(rs, al, provider, cfg)
				} else {
					al.Close()
				}
				msgBus.Close()
			})
		}
		t.Cleanup(stop)
		rs, err = setupAndStartServices(ctx, cfg, credentials.SecretBundle{}, al, msgBus, home, creds,
			&SandboxApplyResult{}, tools.NewBuiltinRegistry(), tools.NewMCPRegistry(), false)
		require.NoError(t, err, "native gateway services, not a new standalone grant store")
		require.NotNil(t, rs.restAPIRef)
		require.NotNil(t, rs.restAPIRef.approvalReg, "native boot wires the real approval registry")
		require.NotNil(t, rs.PlanEngine, "native schedulers/services really started")
		return al, rs, stop
	}
	return &nativeGatewayFixture{home: home, start: start, reopenCredentials: func() {
		creds = credentials.NewStore(filepath.Join(home, "credentials.json"))
		require.NoError(t, credentials.Unlock(creds))
	}}
}

// scriptedCall is one actual model turn of test-agent whose scripted delete_task call has been
// admitted to the gateway's real approval registry and waits for its card to be answered.
type scriptedCall struct {
	entry  *approvalEntry
	result chan error
	rs     *services
}

// startScriptedCall runs the next actual model turn of test-agent ("delete the original task") and
// waits for its real pending card; a turn that completes without one is a failure.
func startScriptedCall(t *testing.T, al *agent.AgentLoop, rs *services, script *exactActionScript) *scriptedCall {
	t.Helper()
	meta, err := al.GetSessionStore().NewSession(session.SessionTypeChat, "cli", "test-agent")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	result := make(chan error, 1)
	script.armed.Store(true)
	go func() {
		defer close(finished)
		_, err := al.ProcessDirectAs(ctx, "test-agent", "delete the original task", meta.ID)
		result <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("native scripted call did not join")
		}
	})
	reg := rs.restAPIRef.approvalReg
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for len(reg.pendingApprovals()) == 0 {
		select {
		case err := <-result:
			t.Fatalf("the matching call completed without asking; err=%v", err)
		case <-deadline.C:
			t.Fatal("fixture UNVERIFIED: the scripted call reached neither a card nor completion")
		case <-tick.C:
		}
	}
	entries := reg.pendingApprovals()
	require.Len(t, entries, 1, "the actual model call asks once")
	return &scriptedCall{entry: entries[0], result: result, rs: rs}
}

// answer posts body for the call's card and waits for the scripted turn to finish.
func (c *scriptedCall) answer(t *testing.T, body string) {
	t.Helper()
	w := postToolApprovalRaw(t, c.rs.restAPIRef, c.entry.ApprovalID, body)
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	select {
	case err := <-c.result:
		require.NoError(t, err, "the answered tool result completes the scripted turn")
	case <-time.After(5 * time.Second):
		t.Fatal("the answered call did not complete")
	}
}

// assertNextCallAsksAgain drives the next actual model call of test-agent on the restarted
// gateway: it must ask on the real registry, naming the agent, the tool and the arguments.
func assertNextCallAsksAgain(t *testing.T, fx *nativeGatewayFixture, after *agent.AgentLoop, rs *services) {
	t.Helper()
	inst, ok := after.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	tool, ok := inst.Tools.Get("delete_task")
	require.True(t, ok, "fixture: the actual builtin, not a test effect sink")
	require.Equal(t, "ask", tools.EffectiveToolPolicy(inst.LoadToolPolicy(), tool.Scope(), inst.AgentType, "delete_task"))
	call := startScriptedCall(t, after, rs, fx.script)
	assert.Equal(t, "test-agent", call.entry.AgentID, "next actual model call asks again on the restarted gateway")
	assert.Equal(t, "delete_task", call.entry.ToolName)
	assert.Equal(t, map[string]any{"task_id": "original"}, call.entry.Args)
	call.answer(t, `{"action":"deny"}`)
}

func testTimedNativeGatewayRestart(t *testing.T) {
	fx := newNativeGatewayFixture(t)
	before, _, stop := fx.start()
	for _, id := range []string{"test-agent", "peer-agent"} {
		recordLifecycleScopes(t, before.ApprovalGrants(), id, fx.home, "example.com")
		assertLifecycleScopes(t, before.ApprovalGrants(), id, fx.home, "example.com", true)
	}
	stop()
	// Reopen the same credential store under the same key/home. Shutdown wiped
	// its old handle; do not create a different install or a replacement result.
	fx.reopenCredentials()
	after, rs, _ := fx.start()
	for _, id := range []string{"test-agent", "peer-agent"} {
		assertLifecycleScopes(t, after.ApprovalGrants(), id, fx.home, "example.com", false)
	}
	assertNextCallAsksAgain(t, fx, after, rs)
}

// testTimedNativeGatewayRestartEndsTimed is S-122 on the real composition root: a real timed
// answer ("Allow for 3 hours" over the real HTTP handler) and the narrow kinds are running, the
// gateway is stopped and started over the same home, every allowance is gone, the next actual call
// asks, and the checksums of config.json, every agent file and the system folder are unchanged by
// the answers and the restart (no storage object, R2-Q1 point 3).
func testTimedNativeGatewayRestartEndsTimed(t *testing.T) {
	const host = "example.com"
	args := map[string]any{"task_id": "original"}
	fx := newNativeGatewayFixture(t)
	before, rs, stop := fx.start()
	given := s122Scope(t, fx.home)
	require.Contains(t, given, "config.json", "instrument: the observer names config.json")
	for _, id := range []string{"test-agent", "peer-agent"} {
		require.Contains(t, given, filepath.Join("entities", "agents", id+".json"), "instrument: the observer names every agent file")
	}

	first := startScriptedCall(t, before, rs, fx.script)
	first.answer(t, `{"action":"allow_for","duration":"3h"}`)
	store := before.ApprovalGrants()
	require.True(t, store.IsAllowed("another-chat", "test-agent", "delete_task", args),
		"instrument: the real answer produced a running allowance for the agent on the card")
	recordTimedLifecycleScopes(t, store, "test-agent", fx.home, host, 3*time.Hour, false)
	recordTimedLifecycleScopes(t, store, "peer-agent", fx.home, host, 3*time.Hour, true)
	for _, id := range []string{"test-agent", "peer-agent"} {
		assertLifecycleScopes(t, store, id, fx.home, host, true)
	}
	audited := false
	for _, row := range readAllAuditEntries(t, filepath.Join(fx.home, "system")) {
		details, _ := row["fields"].(map[string]any)
		if row["event"] == audit.EventToolPolicyAskGranted && details["action"] == "allow_for" {
			audited = true
		}
	}
	require.True(t, audited, "instrument: the timed answer is audited into system/audit.jsonl, so leaving that one file out of the checksums is a real exclusion")

	stop()
	fx.reopenCredentials()
	after, restarted, _ := fx.start()
	for _, id := range []string{"test-agent", "peer-agent"} {
		assertLifecycleScopes(t, after.ApprovalGrants(), id, fx.home, host, false)
	}
	assertNextCallAsksAgain(t, fx, after, restarted)
	assert.Equal(t, given, s122Scope(t, fx.home),
		"S-122: config.json, every agent file and the system folder are unchanged by the answers and the restart")
}

// s122Scope is what S-122 checksums: config.json, every agent file and the system folder. The audit
// log inside that folder records each timed answer by design (C-26 "Audit": a timed allowance
// "writes the existing approval-decision entry with `action: allow_for`, `duration`, `expires_at`"
// and the shell kinds write `shell.grant_recorded`), which S-122's sentence about the `system`
// folder does not allow for (a spec gap, reported in the Part H pack). So, as timedCardHomeTree
// already does, only system/audit.jsonl is left out; any other new or changed file there is seen.
func s122Scope(t *testing.T, home string) map[string]timedHomeEntry {
	t.Helper()
	agents := filepath.Join("entities", "agents") + string(filepath.Separator)
	system := "system"
	scope := map[string]timedHomeEntry{}
	for rel, entry := range timedCardHomeTree(t, home) {
		if rel == "config.json" || strings.HasPrefix(rel, agents) || rel == system || strings.HasPrefix(rel, system+string(filepath.Separator)) {
			scope[rel] = entry
		}
	}
	return scope
}
