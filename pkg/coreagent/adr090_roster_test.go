package coreagent

import (
	"reflect"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestADR090_ExactRosterAndPolicyInventory(t *testing.T) {
	wantRoster := []struct {
		id, name string
		typ      config.AgentType
	}{
		{"mia", "Mia", config.AgentTypeCore},
		{"jim", "Jim", config.AgentTypeCore},
		{"ava", "Ava", config.AgentTypeCore},
		{"admin", "Admin", config.AgentTypeCore},
		{"planner", "Planner", config.AgentTypeWorker},
		{"researcher", "Researcher", config.AgentTypeWorker},
		{"worker", "General Purpose", config.AgentTypeWorker},
		{"judge", "Judge", config.AgentTypeSystem},
		{"plansupervisor", "Plan Supervisor", config.AgentTypeSystem},
	}

	cfg := config.DefaultConfig()
	cfg.Agents.List = nil
	if !SeedConfig(cfg) {
		t.Fatal("SeedConfig returned false for a fresh empty roster")
	}
	if len(cfg.Agents.List) != len(wantRoster) {
		t.Fatalf("fresh roster length = %d, want %d", len(cfg.Agents.List), len(wantRoster))
	}
	for i, want := range wantRoster {
		got := cfg.Agents.List[i]
		if got.ID != want.id || got.Name != want.name || got.Type != want.typ {
			t.Errorf("roster[%d] = (%q, %q, %q), want (%q, %q, %q)", i, got.ID, got.Name, got.Type, want.id, want.name, want.typ)
		}
	}

	for _, retired := range []string{"ray", "explorer", "max"} {
		for _, got := range cfg.Agents.List {
			if got.ID == retired {
				t.Errorf("retired identity %q was seeded", retired)
			}
		}
	}

	ordinary := map[string]bool{"mia": true, "jim": true, "ava": true, "admin": true, "planner": true, "researcher": true, "worker": true}
	for _, got := range cfg.Agents.List {
		if ordinary[got.ID] && len(got.Tools.Builtin.Policies) >= len(AllStaticToolNames()) {
			t.Errorf("ordinary role %q policy has %d entries; want a sparse map smaller than catalog %d", got.ID, len(got.Tools.Builtin.Policies), len(AllStaticToolNames()))
		}
		if !ordinary[got.ID] && len(got.Tools.Builtin.Policies) < len(AllStaticToolNames()) {
			t.Errorf("hidden role %q policy has %d entries, want at least complete static catalog %d", got.ID, len(got.Tools.Builtin.Policies), len(AllStaticToolNames()))
		}
	}
}

func TestADR090_FreshSkillAssignmentsAndExplicitEmptySelectionsPersist(t *testing.T) {
	wantSkills := map[string][]string{
		"mia":            {"interview", "handoff", "define-goal", "inbox-triage", "elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"},
		"jim":            {"interview", "orchestrate", "plan", "define-goal"},
		"ava":            {"interview", "agent-authoring", "skill-authoring", "tool-mapping", "skill-mapping", "delegation-graph", "workspace-team"},
		"admin":          {"interview", "mcp-install", "provider-setup", "channel-setup", "doctor"},
		"planner":        {"plan", "define-goal"},
		"researcher":     {"deep-research"},
		"worker":         {"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"},
		"judge":          {"verify"},
		"plansupervisor": {"plan", "define-goal"},
	}

	cfg := config.DefaultConfig()
	cfg.Agents.List = nil
	SeedConfig(cfg)
	for _, got := range cfg.Agents.List {
		if !reflect.DeepEqual(got.Skills, wantSkills[got.ID]) {
			t.Errorf("skills for %q = %#v, want %#v", got.ID, got.Skills, wantSkills[got.ID])
		}
	}

	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "mia" {
			cfg.Agents.List[i].Skills = []string{}
			cfg.Agents.List[i].Tools.Builtin.Policies = map[string]config.ToolPolicy{}
		}
	}
	SeedConfig(cfg)
	for _, got := range cfg.Agents.List {
		if got.ID == "mia" && (len(got.Skills) != 0 || len(got.Tools.Builtin.Policies) != 0) {
			t.Fatalf("ordinary explicit empty selections were restored: skills=%#v policies=%#v", got.Skills, got.Tools.Builtin.Policies)
		}
	}
}

func TestADR090_RolePolicyRequiredExclusionsAndOutboundAsk(t *testing.T) {
	want := map[string]map[string]config.ToolPolicy{
		"mia":        {"browser_evaluate": config.ToolPolicyAllow, "browser_upload_file": config.ToolPolicyAsk, "send_email": config.ToolPolicyAsk, "reply": config.ToolPolicyAsk, "add_mcp_server": config.ToolPolicyDeny},
		"jim":        {"browser_evaluate": config.ToolPolicyAllow, "browser_upload_file": config.ToolPolicyAsk, "bash": config.ToolPolicyDeny, "serve_web": config.ToolPolicyDeny, "send_email": config.ToolPolicyAsk, "reply": config.ToolPolicyAsk, "add_mcp_server": config.ToolPolicyDeny},
		"ava":        {"create_plan": config.ToolPolicyDeny, "execute_plan": config.ToolPolicyDeny, "stop_plan": config.ToolPolicyDeny, "add_mcp_server": config.ToolPolicyDeny},
		"admin":      {"add_mcp_server": config.ToolPolicyAllow},
		"planner":    {"send_email": config.ToolPolicyAsk, "reply": config.ToolPolicyAsk},
		"researcher": {"bash": config.ToolPolicyDeny, "send_email": config.ToolPolicyAsk, "reply": config.ToolPolicyAsk},
		"worker":     {"create_plan": config.ToolPolicyDeny, "execute_plan": config.ToolPolicyDeny, "stop_plan": config.ToolPolicyDeny, "serve_web": config.ToolPolicyAllow, "send_email": config.ToolPolicyAsk, "reply": config.ToolPolicyAsk},
	}
	for id, expected := range want {
		got := ADR090RolePolicyInventory(CoreAgentID(id))
		for tool, policy := range expected {
			if got[tool] != policy {
				t.Errorf("%s policy %s = %q, want %q", id, tool, got[tool], policy)
			}
		}
	}
}

func TestADR090_RoleInventoryClassifiesEveryStaticTool(t *testing.T) {
	for _, id := range []CoreAgentID{IDMia, IDJim, IDAva, IDAdmin, IDPlanner, IDResearcher, IDWorker} {
		inventory := ADR090RolePolicyInventory(id)
		if len(inventory) != len(AllStaticToolNames()) {
			t.Fatalf("inventory for %q has %d tools, want %d", id, len(inventory), len(AllStaticToolNames()))
		}
		for _, name := range AllStaticToolNames() {
			policy, ok := inventory[name]
			if !ok {
				t.Errorf("inventory for %q does not classify %q", id, name)
			}
			if policy != config.ToolPolicyAllow && policy != config.ToolPolicyAsk && policy != config.ToolPolicyDeny {
				t.Errorf("inventory for %q classifies %q as invalid policy %q", id, name, policy)
			}
		}
	}
}
