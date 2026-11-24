//go:build goolm && stdjson

package config

// Order 39 — TestMcpMigration_CoverageMatrix (S-88; Dataset D8, all 11 rows;
// C-18; ST-Q5 A). Oracle: skills-tools-redesign-spec.md D8 — every column of
// every row comes from the table (global map, Mia's entry for
// mcp_github_read, God Mode, start phase, service; rows without a named
// service use github / mcp_github_read), and the test asserts all 11 rows
// ran so no dimension is fixed by the test itself.
//
// RED: the C-18 external-tools migration does not exist (the round-0 rule
// this package replaces would have written ask for every uncovered MCP
// tool — D8 row 2's note). mcpMigrationPlanOf below is that missing
// migration planner; GREEN wires it to the production start-time migration
// that joins ReconcileToolPolicyCeiling's load path. Nil keeps the RED run
// loud (BLOCKED), never a skip.
import (
	"testing"

	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
)

// mcpMigrationRow is one D8 row's inputs and expected outputs.
type mcpMigrationRow struct {
	name string
	// inputs
	global  map[string]string // the persisted global ceiling entries relevant to the row
	agent   string            // Mia's own stored entry for mcp_github_read ("" = none)
	godMode bool
	phase   string // "first" | "repeat" | "new-service"
	service string // "" = github with tool mcp_github_read
	tool    string // "" = mcp_github_read
	// expected
	writes    map[string]string // the keys the migration writes (empty = nothing)
	effective string            // Mia's effective value for the tool after start
}

func TestMcpMigration_CoverageMatrix(t *testing.T) {
	rows := []mcpMigrationRow{
		{
			name:      "D8#1: covered exact server key — nothing written, allow stands",
			global:    map[string]string{"mcp_github_*": "allow"},
			effective: "allow",
		},
		{
			name:      "D8#2: covered by the broader mcp_* key — nothing written, deny stands",
			global:    map[string]string{"mcp_*": "deny"},
			effective: "deny",
		},
		{
			name:      "D8#3: partly covered (exact deny only) — write mcp_github_* ask, exact deny wins",
			global:    map[string]string{"mcp_github_read": "deny"},
			writes:    map[string]string{"mcp_github_*": "ask"},
			effective: "deny",
		},
		{
			name:      "D8#4: uncovered — write mcp_github_* ask, tool asks",
			writes:    map[string]string{"mcp_github_*": "ask"},
			effective: "ask",
		},
		{
			name:      "D8#5: agent-only allow — write mcp_github_* ask, tool asks",
			agent:     "allow",
			writes:    map[string]string{"mcp_github_*": "ask"},
			effective: "ask",
		},
		{
			name:      "D8#6: agent-only deny — write mcp_github_* ask, tool denies",
			agent:     "deny",
			writes:    map[string]string{"mcp_github_*": "ask"},
			effective: "deny",
		},
		{
			name:  "D8#7: God Mode on, agent-only allow — write mcp_github_* ask, tool allows",
			agent: "allow", godMode: true,
			writes:    map[string]string{"mcp_github_*": "ask"},
			effective: "allow",
		},
		{
			name:    "D8#8: prefix overlap — the probe matches the shorter key; nothing written for git_hub",
			global:  map[string]string{"mcp_git_*": "deny"},
			service: "git_hub", tool: "mcp_git_hub_read",
			effective: "deny",
		},
		{
			name:      "D8#9: repeat start after row 4 — row 4's write persisted, nothing more written",
			global:    map[string]string{"mcp_github_*": "ask"},
			phase:     "repeat",
			effective: "ask",
		},
		{
			name:  "D8#10: a new service after the upgrade, uncovered — write mcp_<s>_* ask (S-59)",
			phase: "new-service", service: "notion", tool: "mcp_notion_read",
			writes:    map[string]string{"mcp_notion_*": "ask"},
			effective: "ask",
		},
		{
			name:  "D8#11: a new service covered by mcp_* deny — nothing written, deny",
			phase: "new-service", service: "notion", tool: "mcp_notion_read",
			global:    map[string]string{"mcp_*": "deny"},
			effective: "deny",
		},
	}
	if len(rows) != 11 {
		t.Fatalf("D8: the matrix must carry every row of the dataset (11), got %d — no dimension may be fixed by the test itself", len(rows))
	}

	ran := 0
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			nativebinding.Require(t, "pkg/config", mcpMigrationPlanOf,
				"C-18/S-88/D8: native startup MCP coverage migration; post-GREEN consumer re-audit required")
			global := r.global
			if global == nil {
				global = map[string]string{}
			}
			service := r.service
			if service == "" {
				service = "github"
			}
			tool := r.tool
			if tool == "" {
				tool = "mcp_github_read"
			}
			phase := r.phase
			if phase == "" {
				phase = "first"
			}
			writes, effective := mcpMigrationPlanOf(global, r.agent, r.godMode, phase, service, tool)
			ran++
			if len(writes) != len(r.writes) {
				t.Fatalf("D8: writes = %v, want %v", writes, r.writes)
			}
			for k, want := range r.writes {
				if writes[k] != want {
					t.Fatalf("D8: write %s = %q, want %q", k, writes[k], want)
				}
			}
			if effective != r.effective {
				t.Fatalf("D8: Mia's effective value for %s = %q, want %q", tool, effective, r.effective)
			}
		})
	}
	if ran != 11 {
		t.Fatalf("S-88: only %d of the 11 D8 rows ran — no dimension may be fixed by the test itself", ran)
	}
}

// mcpMigrationPlanOf is the test-local seam for the C-18 migration planner.
// GREEN wires it to the production start-time migration; nil keeps the RED
// run loud (BLOCKED), never a skip.
var mcpMigrationPlanOf func(global map[string]string, agentEntry string, godMode bool, phase, service, tool string) (writes map[string]string, effective string)

// TestMcpMigration_CoverageMatrix_UpgradeStepWritesThePlan is the native-path
// binding obligation for mcpMigrationPlanOf (CHECK round 4, false-green table
// row 4). TestMcpMigration_CoverageMatrix hands the planner its own phase
// ("first", "repeat", "new-service") and the persisted map as inputs, so it
// cannot show that the real upgrade writes the plan, writes only global server
// keys, or runs once. This can only be checked after GREEN, so it is a loud
// BLOCKED leaf (never a skip); the test author replaces it at test-side
// reconciliation with a real upgrade run on a seeded home that carries the D8
// server entries, read back from config.json after two starts, and CHECK then
// mutates the bound planner and requires it to fail. Real-path observers today:
// none for D8 rows 1-9 (the TestSkillPermissionsUpgrade_StopAtEveryStep fixture
// has "servers":{}); rows 10-11 (a new service) are also written by the
// add-service writers, whose own tests are mcp_add_ask_wildcard_red_test.go in
// pkg/gateway and pkg/sysagent/tools.
func TestMcpMigration_CoverageMatrix_UpgradeStepWritesThePlan(t *testing.T) {
	t.Fatal("BLOCKED: the C-19 upgrade (step 3, the one atomic config.json write) " +
		"is not shown to take its external-tool writes from the function " +
		"mcpMigrationPlanOf binds to, to write only global server keys, or to " +
		"write them once across two starts — required by C-18, FR-074 and S-88 " +
		"(Dataset D8); wiring the seam alone must never turn this green")
}
