package coreagent

import (
	"github.com/elicify-ai/omnipus/pkg/config"
	"testing"
)

// The expected matrix follows the founder's ADR-090 decision: Jim, Mia and
// General Purpose can edit knowledge with confirmation; other working roles
// can read it, including Admin. Hidden engine roles remain denied.
func TestADR090KnowledgeDefaultsMatchRoleResponsibilities(t *testing.T) {
	readTools := []string{"knowledge_describe", "knowledge_find", "knowledge_read", "knowledge_list"}
	writeTools := []string{"knowledge_edit", "knowledge_restructure", "knowledge_configure", "knowledge_base_create"}
	cases := []struct {
		id          CoreAgentID
		read, write config.ToolPolicy
	}{
		{IDMia, config.ToolPolicyAllow, config.ToolPolicyAsk},
		{IDJim, config.ToolPolicyAllow, config.ToolPolicyAsk},
		{IDAva, config.ToolPolicyAllow, config.ToolPolicyDeny},
		{IDPlanner, config.ToolPolicyAllow, config.ToolPolicyDeny},
		{IDResearcher, config.ToolPolicyAllow, config.ToolPolicyDeny},
		{IDWorker, config.ToolPolicyAllow, config.ToolPolicyAsk},
		{IDAdmin, config.ToolPolicyAllow, config.ToolPolicyDeny},
		{IDJudge, config.ToolPolicyDeny, config.ToolPolicyDeny},
		{IDPlanSupervisor, config.ToolPolicyDeny, config.ToolPolicyDeny},
	}
	for _, tc := range cases {
		t.Run(string(tc.id), func(t *testing.T) {
			got := ADR090RolePolicyInventory(tc.id)
			for _, name := range readTools {
				if got[name] != tc.read {
					t.Errorf("%s=%q, want %q", name, got[name], tc.read)
				}
			}
			for _, name := range writeTools {
				if got[name] != tc.write {
					t.Errorf("%s=%q, want %q", name, got[name], tc.write)
				}
			}
		})
	}
}
