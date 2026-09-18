// adr090_environment_guidance_test.go — ADR-090 §6.5 / ES-FR-05 embedded
// skill guidance: the former FR-011 Admin handoff and finalization route must
// be gone from Jim's orchestration skill and Admin's doctor skill. The
// working agent recovers its own missing document dependencies through the
// Ask-gated environment_setup tool; skills stay portable (no tool policy
// baked into portable packages — the elicify-* bodies are hash-pinned
// upstream bytes and are deliberately NOT exercised here).
//
// Oracles are spec phrases (ADR-090 §6.5 line "Any permitted native agent →
// environment_setup → actual user approval → application setup"), never
// values read off the embedded files.

package skills

import (
	"strings"
	"testing"
)

func embeddedSkillText(t *testing.T, slug string) string {
	t.Helper()
	body, err := embeddedSkills.ReadFile("embedded/" + slug + "/SKILL.md")
	if err != nil {
		t.Fatalf("embedded skill %s: %v", slug, err)
	}
	return string(body)
}

func TestADR090_OrchestrateSkillHasNoAdminDependencyHandoff(t *testing.T) {
	body := embeddedSkillText(t, "orchestrate")
	if strings.Contains(body, "Hand document dependencies to Admin") {
		t.Fatalf("orchestrate must drop the Admin dependency handoff: %s", body)
	}
	if !strings.Contains(body, "notool:environment_setup") {
		t.Fatalf("orchestrate (jim-only) must mark environment_setup as a tool jim does not hold: %s", body)
	}
	if !strings.Contains(body, "environment_setup") {
		t.Fatalf("orchestrate must still name the workers' recovery route: %s", body)
	}
}

func TestADR090_DoctorSkillHasNoInstallOrFinalizeRoute(t *testing.T) {
	body := embeddedSkillText(t, "doctor")
	if strings.Contains(body, "finalize") {
		t.Fatalf("doctor must drop finalizer language: %s", body)
	}
	if strings.Contains(body, "Admin-side install") {
		t.Fatalf("doctor must drop the Admin-side install step: %s", body)
	}
	if !strings.Contains(body, "environment_setup") {
		t.Fatalf("doctor must point missing document dependencies at environment_setup: %s", body)
	}
	if !strings.Contains(body, "re-probe") {
		t.Fatalf("doctor must keep the requester re-probe discipline: %s", body)
	}
}
