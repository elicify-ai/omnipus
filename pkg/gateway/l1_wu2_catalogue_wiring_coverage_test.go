package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Use the real shipped ceiling, seed and coverage-repair boot stages before
// constructing the loop. A minimal cfg with no ceiling would invent a policy
// failure unrelated to executable wiring. Only the model edge is scripted.
func l1WU2WiredPermissionAPI(t *testing.T) *restAPI {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = home
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: "scripted-model"}
	coreagent.SeedConfig(cfg)
	workerTools := coreagent.NewCustomAgentToolsCfg()
	coreagent.RestrictForWorkerTier(workerTools)
	cfg.Agents.List = append(cfg.Agents.List,
		config.AgentConfig{ID: "coverage-main", Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()},
		config.AgentConfig{ID: "coverage-worker", Type: config.AgentTypeWorker, Tools: workerTools},
	)
	require.Empty(t, repairAndValidateToolPolicyCoverage(cfg), "real boot ceiling reconciliation and validation")
	seedSystemAgentEagerSouls(cfg)
	require.NoError(t, workspace.SaveRecord(home, workspace.Workspace{
		ID: "default", Name: "Default", Status: "active", IsDefault: true, CoreTeam: []string{"omnipus"},
	}))
	store := agentstore.New(home)
	for _, ac := range cfg.Agents.List {
		copyAgent := ac
		require.NoError(t, store.Create(ac.ID, &copyAgent))
	}
	data, encodeErr := json.Marshal(cfg)
	require.NoError(t, encodeErr)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), data, 0o600))
	al, loopErr := agent.NewAgentLoop(cfg, bus.NewMessageBus(), &restMockProvider{})
	require.NoError(t, loopErr)
	t.Cleanup(al.Close)
	return &restAPI{agentLoop: al, homePath: home}
}

// F03 / FR-021/024 / spec §6.5: the implemented permission tool MUST be in
// the central catalogue itself. A static-name/Auto table union is NOT an oracle.
// Intended mutants: M16/M19/M20 (same catalogue-only omission).
func TestL1WU2PermissionMetadata_ActualCentralBuilderAndRESTRequirePermissionTool(t *testing.T) {
	api := l1WU2WiredPermissionAPI(t)
	rc := &runContextWithOptions{
		homePath: api.homePath, configPath: api.configPath(), cfg: api.agentLoop.GetConfig(), agentLoop: api.agentLoop,
		runningServices: &services{restAPIRef: api}, centralMCPReg: tools.NewMCPRegistry(),
	}
	metadata, _ := buildCentralBuiltinRegistry(nil)
	rc.wireSystemTools() // actual boot producer builds the second, live-deps catalogue
	for _, row := range []struct {
		name string
		reg  *tools.BuiltinRegistry
	}{{"metadata_pass", metadata}, {"live_deps_pass", rc.centralBuiltinReg}} {
		t.Run(row.name, func(t *testing.T) {
			_, control := row.reg.Get("get_agent")
			require.True(t, control, "known central-catalogue positive control")
			count := 0
			for _, entry := range row.reg.Describe() {
				if entry.Name == "set_agent_permissions" {
					count++
					assert.Equal(t, "builtin", entry.Source)
					assert.Equal(t, tools.ScopeCore, entry.Scope)
				}
			}
			assert.Equal(t, 1, count, "F03: central catalogue itself must contain the permission tool exactly once")
		})
	}
	t.Run("real_rest_registry", func(t *testing.T) {
		require.Same(t, rc.centralBuiltinReg, api.builtinRegistry, "the REST carrier must be wired to the actual central catalogue, not its default-agent fallback")
		srv := httptest.NewServer(http.HandlerFunc(api.HandleToolsRegistry))
		defer srv.Close()
		response, callErr := srv.Client().Get(srv.URL + "/api/v1/tools")
		require.NoError(t, callErr)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		var entries []gen.ToolRegistryEntry
		require.NoError(t, json.NewDecoder(response.Body).Decode(&entries))
		permission, controls := 0, 0
		for _, entry := range entries {
			if entry.Name == "get_agent" {
				controls++
			}
			if entry.Name == "set_agent_permissions" {
				permission++
				assert.Equal(t, gen.ToolRegistryEntrySourceBuiltin, entry.Source)
				assert.Equal(t, gen.ToolRegistryEntryScopeCore, entry.Scope)
			}
		}
		assert.Equal(t, 1, controls, "nonempty central REST-carrier instrument")
		assert.Equal(t, 1, permission, "F03: static-name coverage cannot replace actual central REST presence")
	})
}

// F04 / §6.5: registration is supply-side and must reach every native role
// at boot AND reload; policy still denies invocation for non-Omnipus roles.
// The executable is taken from the live registry, never constructed directly.
// Intended mutant: M17 (wireSysagentDepsLocked omission).
func TestL1WU2PermissionExecutable_BootAndReloadWireEveryNativeRoleAndInvokeRegisteredTool(t *testing.T) {
	api := l1WU2WiredPermissionAPI(t)
	al := api.agentLoop
	rc := &runContextWithOptions{
		homePath: api.homePath, configPath: api.configPath(), cfg: al.GetConfig(), agentLoop: al,
		runningServices: &services{restAPIRef: api}, centralMCPReg: tools.NewMCPRegistry(),
	}
	rc.wireSystemTools()
	for _, phase := range []string{"boot", "reload"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "reload" {
				oldRegistry := al.GetRegistry()
				clone, cloneErr := al.GetConfig().Clone()
				require.NoError(t, cloneErr)
				require.NoError(t, al.ReloadProviderAndConfig(context.Background(), &restMockProvider{}, clone), "real registry reload must preserve the boot deps wiring")
				require.NotSame(t, oldRegistry, al.GetRegistry(), "reload must actually reconstruct the registry; retaining the boot instances cannot prove reload wiring")
			}
			for _, id := range []string{"omnipus", "mia", "jim", "worker", "planner", "researcher", "judge", "plansupervisor", "coverage-main", "coverage-worker"} {
				instance, native := al.GetRegistry().GetAgent(id)
				require.True(t, native, "the shipped native role %s must actually be in the fixture", id)
				permission, registered := instance.Tools.Get("set_agent_permissions")
				require.True(t, registered, "F04: %s registry must have the actual executable at %s, not only a central name", id, phase)
				assert.Equal(t, "set_agent_permissions", permission.Name())
				policy, resolved := al.ResolveRegisteredToolPolicy(id, permission.Name())
				require.True(t, resolved)
				want := "deny"
				if id == "omnipus" {
					want = "ask"
				}
				assert.Equal(t, want, policy, "registration itself must not grant permission to %s", id)
			}
			omnipus, found := al.GetRegistry().GetAgent("omnipus")
			require.True(t, found)
			search, searchable := omnipus.Tools.Get("ToolSearch")
			require.True(t, searchable)
			ctx := tools.WithAgentID(context.Background(), "omnipus")
			discovered := search.Execute(ctx, map[string]any{"names": []any{"set_agent_permissions"}})
			require.NotNil(t, discovered)
			require.False(t, discovered.IsError, "registered permission tool must be discoverable/loadable: %s", discovered.ForLLM)
			assert.Contains(t, discovered.ForLLM, "set_agent_permissions")
			permission, executable := omnipus.Tools.Get("set_agent_permissions")
			require.True(t, executable)
			store := agentstore.New(api.homePath)
			before, beforeErr := store.ReadState("mia")
			require.NoError(t, beforeErr)
			want := config.ToolPolicyDeny
			if phase == "reload" {
				want = config.ToolPolicyAllow
			}
			result := permission.Execute(ctx, map[string]any{
				"id": "mia", "revision": before.Revision,
				"tool_policy_changes": map[string]any{"set": map[string]any{"read_file": string(want)}},
			})
			require.NotNil(t, result)
			require.False(t, result.IsError, "F04: the BOOT/RELOAD registered instance must really mutate permissions: %s", result.ForLLM)
			after, afterErr := store.ReadState("mia")
			require.NoError(t, afterErr)
			assert.Equal(t, want, after.Agent.Tools.Builtin.Policies["read_file"])
			assert.NotEqual(t, before.Revision, after.Revision)
			live, current := al.ResolveRegisteredToolPolicy("mia", "read_file")
			require.True(t, current)
			assert.Equal(t, string(want), live, "the registry-obtained executable must publish the target's live policy")
		})
	}
}
