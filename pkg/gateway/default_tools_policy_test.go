package gateway

import (
	"maps"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDefaultToolSets_Founder20261022 traces the approved default-tools spec's
// Table, Definitions and Global ceiling changes. Each expected policy is
// literal spec data in defaultToolsSpecRows, independent of the implementation.
//
// Real: both creation paths, entity persistence, shipped ceiling, built-in
// seeding, central metadata catalog, config adapter and effective compositor.
// Only the model provider is scripted by the existing gateway fixture; no
// model call, browser action, email send or external CLI execution occurs.
//
// Deliberate boundaries: hidden system agents have no Main/Worker column;
// built-in role-specific extras are preserved ('at least' the column core).
// The approved live-instance upgrade requires a separate human-gated run.
// GREEN and implementation mutation probes belong to a fresh CHECK instance.
func TestDefaultToolSets_Founder20261022(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	catalog, _ := buildCentralBuiltinRegistry(nil)
	_, control := catalog.Get("read_file")
	require.True(t, control, "catalog lookup positive control must see a known real tool")
	rows := defaultToolsSpecRows(t, catalog)
	defaultToolsReportCatalogGaps(t, catalog, rows)

	t.Run("ceiling", func(t *testing.T) { defaultToolsAssertCeiling(t, rows) })
	t.Run("builtins", func(t *testing.T) { defaultToolsAssertBuiltins(t, rows) })
	t.Run("preview_schema", defaultToolsAssertPreviewSchema)
	t.Run("system_policy_baseline", defaultToolsAssertSystemBaseline)
	t.Run("compact_contract", defaultToolsAssertCompactContract)

	paths := []struct {
		name   string
		create func(*testing.T, *restAPI, string) *config.AgentConfig
	}{
		{name: "REST", create: defaultToolsCreateREST},
		{name: "create_agent", create: defaultToolsCreateTool},
	}
	for _, path := range paths {
		for _, kind := range []string{"Main", "Subagent", "subagent_3p"} {
			t.Run(path.name+"/"+kind, func(t *testing.T) {
				api := l1WU2WiredPermissionAPI(t)
				api.builtinRegistry = catalog
				stored := path.create(t, api, kind)
				require.Equal(t, kind, string(coreagent.ToWireType(*stored)), "persisted agent must really be the requested kind")
				require.NotNil(t, stored.Tools, "created agent must carry real seeded tool configuration")
				policy, _ := defaultToolsPolicies(api.agentLoop.GetConfig(), stored)
				for _, row := range rows {
					want := defaultToolsWant(row, kind)
					t.Run("effective/"+row.name, func(t *testing.T) {
						got := tools.ResolveEffectivePolicy(policy, row.name)
						assert.Equal(t, want, got, "Table %s/%s: effective policy (ceiling=%q, agent=%q)",
							kind, row.name, policy.GlobalPolicies[row.name], policy.Policies[row.name])
					})
				}
				for _, name := range defaultToolsAdminOnlyNames() {
					t.Run("admin_off/"+name, func(t *testing.T) {
						assert.Equal(t, "deny", tools.ResolveEffectivePolicy(policy, name),
							"Table Admin-only: %s must stay off for a new %s", name, kind)
					})
				}
				t.Run("operator_deny_wins", func(t *testing.T) {
					defaultToolsAssertOperatorDeny(t, policy, rows, kind)
				})
				t.Run("preview", func(t *testing.T) {
					defaultToolsAssertPreview(t, api, stored, policy, rows, kind)
				})
			})
		}
	}
}

func defaultToolsWant(row defaultToolsPolicyCase, kind string) string {
	if kind == "Main" {
		return row.main
	}
	return row.worker
}

func defaultToolsAssertCeiling(t *testing.T, rows []defaultToolsPolicyCase) {
	t.Helper()
	ceiling := config.DefaultConfig().Sandbox.ToolPolicies
	// Exact changes from Global ceiling changes, plus the unchanged bash row.
	for _, row := range []struct{ name, want string }{
		{"send_email", "ask"}, {"reply", "ask"}, {"download_email_attachment", "allow"},
		// SPEC.md Q22=B: skill authoring asks per agent; the global ceiling remains Allow.
		{"create_skill", "allow"}, {"edit_skill", "allow"}, {"bash", "ask"},
	} {
		t.Run("exact/"+row.name, func(t *testing.T) {
			assert.Equal(t, row.want, ceiling[row.name], "Global ceiling changes: %s", row.name)
		})
	}
	// The last ceiling requirement covers the WHOLE table. In particular, a
	// per-agent allow is not sufficient if a stricter global ceiling blocks it.
	for _, row := range rows {
		for _, column := range []struct{ name, want string }{{"Main", row.main}, {"Worker", row.worker}} {
			t.Run("permits/"+column.name+"/"+row.name, func(t *testing.T) {
				actual, ok := ceiling[row.name]
				require.True(t, ok, "table tool %s must have a global ceiling entry", row.name)
				assert.GreaterOrEqual(t, defaultToolsPermissionRank(t, actual), defaultToolsPermissionRank(t, column.want),
					"Global ceiling changes: %s=%s must permit %s column's %s", row.name, actual, column.name, column.want)
			})
		}
	}
}

func defaultToolsAssertBuiltins(t *testing.T, rows []defaultToolsPolicyCase) {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = t.TempDir()
	cfg.Agents.List = nil
	coreagent.SeedConfig(cfg)
	// SPEC.md Founder exclusion: Omnipus, Judge and Plan Supervisor keep their baseline system policies.
	// Only the five non-system built-ins receive the column core sets.
	for _, builtin := range []struct{ id, column string }{
		{"mia", "Main"}, {"jim", "Main"},
		{"worker", "Worker"}, {"planner", "Worker"}, {"researcher", "Worker"},
	} {
		t.Run(builtin.id, func(t *testing.T) {
			var found *config.AgentConfig
			for i := range cfg.Agents.List {
				if cfg.Agents.List[i].ID == builtin.id {
					found = &cfg.Agents.List[i]
					break
				}
			}
			require.NotNil(t, found, "built-in %s must really be seeded", builtin.id)
			policy, _ := defaultToolsPolicies(cfg, found)
			for _, row := range rows {
				want := row.main
				if builtin.column == "Worker" {
					want = row.worker
				}
				// 'At least' the column's core set does not remove an existing
				// specialist grant for a row whose default is Off.
				if want != "deny" {
					t.Run(row.name, func(t *testing.T) {
						got := tools.ResolveEffectivePolicy(policy, row.name)
						assert.GreaterOrEqual(t, defaultToolsPermissionRank(t, got), defaultToolsPermissionRank(t, want),
							"built-in %s must have at least %s column's %s=%s; effective=%s (ceiling=%q, agent=%q)",
							builtin.id, builtin.column, row.name, want, got, policy.GlobalPolicies[row.name], policy.Policies[row.name])
					})
				}
			}
			if builtin.id == "jim" {
				t.Run("bash_stays_denied", func(t *testing.T) {
					assert.Equal(t, config.ToolPolicyDeny, policy.Policies["bash"], "Jim keeps his deliberate per-agent shell denial")
					assert.Equal(t, "deny", tools.ResolveEffectivePolicy(policy, "bash"))
				})
			}
		})
	}
}

func defaultToolsAssertOperatorDeny(t *testing.T, baseline *tools.ToolPolicyCfg, rows []defaultToolsPolicyCase, kind string) {
	t.Helper()
	beforeAgent, beforeCeiling := maps.Clone(baseline.Policies), maps.Clone(baseline.GlobalPolicies)
	for _, row := range rows {
		if defaultToolsWant(row, kind) != "deny" {
			t.Run(row.name, func(t *testing.T) {
				// Definitions: a default is NOT locked. This is the policy-input
				// boundary, not a claim about a Settings editor for external CLIs.
				// Change only this agent's one override; keep the real ceiling.
				changed := *baseline
				changed.Policies = maps.Clone(baseline.Policies)
				changed.Policies[row.name] = config.ToolPolicyDeny
				assert.Equal(t, "deny", tools.ResolveEffectivePolicy(&changed, row.name),
					"Definitions / Hard Constraint #6: operator's %s deny must beat the %s default", row.name, kind)
				assert.Equal(t, tools.ResolveEffectivePolicy(baseline, row.name),
					tools.ResolveEffectivePolicy(&tools.ToolPolicyCfg{Policies: beforeAgent, GlobalPolicies: beforeCeiling}, row.name),
					"another agent's original policy must not change with a private override")
			})
		}
	}
	assert.Equal(t, beforeAgent, baseline.Policies, "individual deny must not mutate another agent's seed")
	assert.Equal(t, beforeCeiling, baseline.GlobalPolicies, "individual deny must not mutate the global ceiling")
}

func defaultToolsPermissionRank(t *testing.T, policy string) int {
	t.Helper()
	// Existing agents, spec: deny < ask < allow. No ordering comes from code.
	switch policy {
	case "deny":
		return 0
	case "ask":
		return 1
	case "allow":
		return 2
	default:
		t.Fatalf("invalid policy %q; spec permits only deny, ask, allow", policy)
		return -1
	}
}
