// prompts_adr090_environment_test.go — ADR-090 §6.5 / ES-FR-05 prompt
// guidance: missing document dependencies are recovered by the working agent
// itself through the Ask-gated environment_setup tool. The former FR-011
// Admin handoff (Mia's switch_agent route, Jim's hand-off instruction, Admin's
// install/finalize duty, the worker's parent routing) must be gone from every
// prompt, while each role's persona and remaining duties stay unchanged.
//
// Oracles are spec phrases and marker conventions (tool:/notool:), never
// values derived from the prompt strings under test. Marker validity against
// the catalog and effective policies is enforced by the pkg/skills lints;
// these tests pin the guidance content itself.

package coreagent

import (
	"strings"
	"testing"
)

func TestPrompt_MiaRequestsDocumentDependenciesHerself(t *testing.T) {
	prompt := GetPrompt("mia")
	if !strings.Contains(prompt, "tool:environment_setup") {
		t.Fatalf("mia prompt must direct self-serve environment_setup for missing document dependencies: %s", prompt)
	}
	if strings.Contains(prompt, "dependencies to Admin") {
		t.Fatalf("mia prompt must not route missing document dependencies to Admin: %s", prompt)
	}
	if !strings.Contains(prompt, "tool:switch_agent") {
		t.Fatalf("mia persona unchanged: switch_agent must remain for project handoff: %s", prompt)
	}
	// GENERIC INSTALL (current decision): Mia supplies the installation
	// command or script herself; the retired structured-dependency request
	// wording must not appear.
	if !strings.Contains(prompt, "installation command") {
		t.Fatalf("mia prompt must teach supplying the installation command or script herself: %s", prompt)
	}
	if strings.Contains(prompt, "ecosystem") {
		t.Fatalf("mia prompt must not carry the retired structured-dependency wording: %s", prompt)
	}
}

func TestPrompt_JimDoesNotHandDocumentDependenciesToAdmin(t *testing.T) {
	prompt := GetPrompt("jim")
	if strings.Contains(prompt, "Hand missing document dependencies to Admin") {
		t.Fatalf("jim prompt must drop the Admin handoff instruction: %s", prompt)
	}
	if !strings.Contains(prompt, "notool:environment_setup") {
		t.Fatalf("jim prompt must mark environment_setup as a tool he does not hold: %s", prompt)
	}
}

func TestPrompt_WorkerRequestsSetupItself(t *testing.T) {
	prompt := GetPrompt("worker")
	if !strings.Contains(prompt, "tool:environment_setup") {
		t.Fatalf("worker prompt must direct self-serve environment_setup: %s", prompt)
	}
	// No parent/Admin-routing absence pin here on purpose: the retired FR-011
	// handoff never existed as worker-prompt wording (the pre-ADR-090 prompt
	// carried no dependency-routing text at all, and its plain "report it to
	// the parent" completion reporting must stay), so a banned phrase would
	// be invented, not retired. The grounded guards live on the instruction
	// side — Jim's prompt ("do not route setup through Admin") — and in the
	// per-role policy tests.
	// GENERIC INSTALL (current decision): the worker supplies the installation
	// command or script itself; no structured-dependency wording.
	if !strings.Contains(prompt, "installation command") {
		t.Fatalf("worker prompt must teach supplying the installation command or script itself: %s", prompt)
	}
	if strings.Contains(prompt, "ecosystem") {
		t.Fatalf("worker prompt must not carry the retired structured-dependency wording: %s", prompt)
	}
}

func TestPrompt_AdminKeepsConnectorRoleWithoutDocumentInstallDuty(t *testing.T) {
	prompt := GetPrompt("admin")
	if strings.Contains(prompt, "Install missing document prerequisites") {
		t.Fatalf("admin prompt must drop the document prerequisite install instruction: %s", prompt)
	}
	if !strings.Contains(prompt, "connector") {
		t.Fatalf("admin persona unchanged: connector role must remain: %s", prompt)
	}
	if !strings.Contains(prompt, "tool:environment_setup") {
		t.Fatalf("admin prompt must name environment_setup for explicit cross-workspace setup requests: %s", prompt)
	}
}
