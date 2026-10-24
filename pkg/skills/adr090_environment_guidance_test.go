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

// WU-9 (omnipus-system-agent FR-010) split the retired Ava/Admin-era doctor
// skill: doctor-usage (W16) is the read-only health/usage skill, and
// dependency installation lives in the dependency-check workflow skill.
// ADR-090's invariants survive the split on both halves: the doctor side
// keeps no finalizer and no Admin-side install step and runs no change tool
// itself, and the recovery route — environment_setup, the Ask-gated
// self-service installer — is named where an install is actually decided
// (dependency-check), which keeps the never-installs-itself and
// readiness-proven disciplines ("exit 0 is never readiness").
func TestADR090_DoctorSkillHasNoInstallOrFinalizeRoute(t *testing.T) {
	body := embeddedSkillText(t, "doctor-usage")
	if strings.Contains(body, "finalize") {
		t.Fatalf("doctor-usage must drop finalizer language: %s", body)
	}
	if strings.Contains(body, "Admin-side install") {
		t.Fatalf("doctor-usage must drop the Admin-side install step: %s", body)
	}
	if strings.Contains(body, "environment_setup") {
		t.Fatalf("doctor-usage is read-only; dependency installation is dependency-check's route, never the doctor's: %s", body)
	}

	deps := embeddedSkillText(t, "dependency-check")
	if !strings.Contains(deps, "tool:environment_setup") {
		t.Fatalf("dependency-check must point missing document dependencies at tool:environment_setup (the ADR-090 recovery route): %s", deps)
	}
	if !strings.Contains(deps, "never installs") {
		t.Fatalf("dependency-check must keep the never-installs-itself discipline: %s", deps)
	}
	if !strings.Contains(deps, "exit 0 is never readiness") {
		t.Fatalf("dependency-check must keep the readiness discipline (exit 0 is command success, never readiness): %s", deps)
	}
}
