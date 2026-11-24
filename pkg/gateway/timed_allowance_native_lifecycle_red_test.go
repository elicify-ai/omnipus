//go:build goolm && stdjson

package gateway

// CHECK F17/F21, C-26 End/S-122. Real unlocked entities, both native delete
// entry paths, the loop-owned grant store, a wired production EgressProxy,
// and exact issued tokens. Legacy Record* is ONLY a selective-clear
// instrument, not a timed HTTP answer. No whole delete/restart/result seam.
// Native setupAndStartServices/shutdown and same-home startup are driven
// below as a legacy clearing instrument. Duration-bearing HTTP answers and
// full RunContext startup/expiry remain separate loud obligations. This file
// does not certify OS kernel enforcement: ST-29/ST-30 remain UNVERIFIED.
// GREEN/mutation proof belongs to independent CHECK.

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
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
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
			assertLifecycleScopes(t, store, "test-agent", h.api.homePath, host, false)
			assertLifecycleScopes(t, store, "peer-agent", h.api.homePath, host, true)
			assert.NotContains(t, store.NetworkTokensForSession("old-closed-chat"), token, "the deleted agent's exact issued token is removed")
			assert.Contains(t, store.NetworkTokensForSession("old-closed-chat"), peerToken, "unaffected peer's token survives")
			request(token, false)
			request(peerToken, true)
		})
	}
	t.Run("instrument native same-home gateway restart does not restore legacy consent", testTimedNativeGatewayRestart)
	t.Run("C-26 duration-bearing delete and gateway restart acceptance", func(t *testing.T) {
		t.Fatal("BLOCKED: native duration-taking Record*/ClearAgent and full same-home RunContext restart/clock wiring are not implemented — required by C-26 End/S-122; the real legacy selective-clear and native-shutdown instruments above cannot certify timed restart, ST-29/ST-30")
	})
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

func testTimedNativeGatewayRestart(t *testing.T) {
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
	raw, marshalErr := json.Marshal(cfg)
	require.NoError(t, marshalErr)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), raw, 0o600))
	ensureTestWorkspaceMembership(t, cfg)
	seedDefaultDelegationGraph(t, testHarnessAgentIDs(cfg))
	creds := newUnlockedStore(t, home)
	script := &exactActionScript{row: exactActionRow{tool: "delete_task", args: map[string]any{"task_id": "original"}}}
	start := func() (*agent.AgentLoop, *services, func()) {
		t.Helper()
		msgBus := bus.NewMessageBus()
		al, err := agent.NewAgentLoop(cfg, msgBus, script)
		require.NoError(t, err)
		ctx, cancel := context.WithCancel(context.Background())
		var rs *services
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				if rs != nil {
					omnipusGracefulShutdown(rs, al, script, cfg)
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
	before, _, stop := start()
	for _, id := range []string{"test-agent", "peer-agent"} {
		recordLifecycleScopes(t, before.ApprovalGrants(), id, home, "example.com")
		assertLifecycleScopes(t, before.ApprovalGrants(), id, home, "example.com", true)
	}
	stop()
	// Reopen the same credential store under the same key/home. Shutdown wiped
	// its old handle; do not create a different install or a replacement result.
	creds = credentials.NewStore(filepath.Join(home, "credentials.json"))
	require.NoError(t, credentials.Unlock(creds))
	after, rs, _ := start()
	for _, id := range []string{"test-agent", "peer-agent"} {
		assertLifecycleScopes(t, after.ApprovalGrants(), id, home, "example.com", false)
	}
	inst, ok := after.GetRegistry().GetAgent("test-agent")
	require.True(t, ok)
	tool, ok := inst.Tools.Get("delete_task")
	require.True(t, ok, "fixture: the actual builtin, not a test effect sink")
	require.Equal(t, "ask", tools.EffectiveToolPolicy(inst.LoadToolPolicy(), tool.Scope(), inst.AgentType, "delete_task"))
	meta, err := after.GetSessionStore().NewSession(session.SessionTypeChat, "cli", "test-agent")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	result := make(chan error, 1)
	script.armed.Store(true)
	go func() {
		defer close(finished)
		_, err := after.ProcessDirectAs(ctx, "test-agent", "delete the original task", meta.ID)
		result <- err
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("native post-restart call did not join")
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
			t.Fatalf("post-restart matching call completed without asking; err=%v", err)
		case <-deadline.C:
			t.Fatal("fixture UNVERIFIED: post-restart call reached neither card nor completion")
		case <-tick.C:
		}
	}
	entries := reg.pendingApprovals()
	require.Len(t, entries, 1, "next actual model call asks again on the restarted gateway")
	assert.Equal(t, "test-agent", entries[0].AgentID)
	assert.Equal(t, "delete_task", entries[0].ToolName)
	assert.Equal(t, map[string]any{"task_id": "original"}, entries[0].Args)
	w := postToolApprovalRaw(t, rs.restAPIRef, entries[0].ApprovalID, `{"action":"deny"}`)
	require.Equal(t, http.StatusOK, w.Code, "%s", w.Body.String())
	select {
	case err := <-result:
		require.NoError(t, err, "denied tool result completes the scripted turn")
	case <-deadline.C:
		t.Fatal("post-restart denied call did not complete")
	}
}
