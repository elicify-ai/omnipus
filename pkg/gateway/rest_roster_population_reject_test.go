// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RELEASE BLOCKER security-fix regression test (F3 follow-up): every
// REST-triggered config write goes through restAPI.refreshConfigAndRewireServices
// (agent create/update/delete, channel configure, tool-policy write, mailbox
// grant, god-mode toggle — all of it), and that function used to call the
// LENIENT populateAgentsListFromEntityStore (log-and-continue on failure)
// instead of the strict, fail-closed variant pkg/gateway/gateway.go's own
// boot/manual-reload/file-watcher paths already used. A genuine entity-store
// List() failure (EMFILE/EACCES/EIO, or — as here — entities/agents shadowed
// by a regular file) would silently swap in a config whose Agents.List came
// back empty. pkg/agent/registry.go's NewAgentRegistry ALWAYS registers an
// unrestricted "main" sentinel with no Tools/Policies, and
// pkg/tools/compositor.go's global×agent policy merge falls through to the
// permissive global floor (pkg/config/defaults.go seeds bash/write_file/
// edit_file/delegate/send_email all "allow") for any agent with no per-agent
// policy entry — so an emptied roster silently promotes ALL routed traffic to
// that unrestricted floor. See roster_robustness_test.go's
// TestEmptyAgentRosterWouldResolveToPermissiveGlobalFloor for the generic
// proof of that mechanism using the real compositor/defaults code; THIS file
// proves the REST write path specifically now refuses to ever reach that
// state — it rejects the write instead of silently swapping in the broken
// config.
package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestRESTWrite_EntityStoreListFailure_RejectsRatherThanEmptyingRoster is the
// DoD test: a store-list failure during a REST-triggered reload must NOT
// leave an empty in-memory roster (which would let tool policy fall back to
// the permissive global floor for the unrestricted "main" sentinel).
//
// BDD: Given a healthy gateway with two real agents, and entities/agents/ is
// then shadowed by a regular file (a genuine, non-transient entity-store
// failure — mirrors roster_robustness_test.go's own List()-failure fixture),
//
//	When a REST write that goes through safeUpdateConfigJSON is issued
//	(PUT /api/v1/security/audit-log — chosen because its own mutate closure
//	never touches the agent entity store, isolating the roster-population
//	check inside refreshConfigAndRewireServices from any OTHER entity-store
//	interaction the handler itself might have),
//	Then the request fails (500), the sandbox.audit_log setting is NOT
//	applied (the in-memory config was never swapped), and — the critical
//	assertion — the in-memory agent roster (al.GetConfig().Agents.List)
//	is NOT emptied; it still lists both real agents exactly as before the
//	failed write.
func TestRESTWrite_EntityStoreListFailure_RejectsRatherThanEmptyingRoster(t *testing.T) {
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	tmpDir := t.TempDir()
	cfgPath := tmpDir + "/config.json"

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 20,
			},
			List: []config.AgentConfig{
				{ID: "agent-a", Name: "Agent A"},
				{ID: "agent-b", Name: "Agent B"},
			},
		},
	}
	require.NoError(t, os.WriteFile(cfgPath, marshalConfigForDisk(t, cfg), 0o600))

	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	api := &restAPI{agentLoop: al, homePath: tmpDir}
	seedRoutingAgentEntities(t, tmpDir, cfg.Agents.List)

	// Sanity: the healthy roster is really there before we break anything.
	require.Len(t, al.GetConfig().Agents.List, 2, "precondition: both agents must be present before corruption")
	require.False(t, al.GetConfig().Sandbox.AuditLog, "precondition: audit_log starts disabled")

	// Corrupt the entity store: shadow entities/agents with a regular file, so
	// any subsequent agentstore.New(homePath).List() fails with a genuine
	// (non-NotExist) error — os.ReadDir on a file returns ENOTDIR. This is the
	// exact technique roster_robustness_test.go's
	// TestPopulateAgentsListFromEntityStoreStrict_GenuineListErrorRejectsAndPreservesRoster
	// uses, applied here against a REST write instead of a direct unit call.
	entitiesDir := filepath.Join(tmpDir, "entities", "agents")
	require.NoError(t, os.RemoveAll(entitiesDir))
	require.NoError(t, os.WriteFile(entitiesDir, []byte("not a directory"), 0o600))

	// A REST-triggered write whose OWN mutate closure never touches the agent
	// entity store — isolates the failure to refreshConfigAndRewireServices's
	// roster-population step, not some other pre-existing entity-store call
	// (e.g. updateAgent's own store.Update on the target agent, which would
	// fail for an unrelated reason first).
	body := `{"enabled": true}`
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPut, "/api/v1/security/audit-log", strings.NewReader(body))
	api.HandleSandboxAuditLog(w, r)

	assert.Equal(t, http.StatusInternalServerError, w.Code,
		"a REST write must fail loudly when the entity store cannot be trusted, not silently succeed")

	// The critical assertion: the roster must still be exactly what it was —
	// never swapped to an empty (or partially-empty) one.
	liveCfg := al.GetConfig()
	assert.Len(t, liveCfg.Agents.List, 2,
		"THE BUG: a rejected write must never leave the in-memory roster empty — an empty roster "+
			"silently promotes ALL routed traffic to the unrestricted \"main\" sentinel's permissive "+
			"global tool-policy floor (see roster_robustness_test.go's "+
			"TestEmptyAgentRosterWouldResolveToPermissiveGlobalFloor)")
	ids := map[string]bool{}
	for _, a := range liveCfg.Agents.List {
		ids[a.ID] = true
	}
	assert.True(t, ids["agent-a"] && ids["agent-b"], "both original agents must still be present")

	// The rejected write's OWN payload must also not have taken effect — proof
	// that refreshConfigAndRewireServices really did reject before SwapConfig,
	// not merely fail some unrelated step after already swapping.
	assert.False(t, liveCfg.Sandbox.AuditLog,
		"the audit_log change must not be observably applied — the config generation carrying it "+
			"was rejected wholesale, not partially applied")
}
