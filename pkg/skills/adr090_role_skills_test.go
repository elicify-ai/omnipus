package skills

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestADR090_PromptSkillToolReferences_RoleSkillPackagesAreComplete(t *testing.T) {
	// FR-010 (WU-9): the Ava/Admin-era skills are gone; the granted packages
	// are the Omnipus workflow skills plus the shared helpers the core roles
	// hold (pkg/coreagent/seed.go::coreAgentSkills). Workflow skills must
	// carry the full structure; the two reference skills define-goal and
	// provider-config are granted without the Prerequisites/Steps structure
	// by design. The four Elicify document packages are pinned byte-identical
	// to their import and are not role-authored here.
	workflow := []string{
		"interview", "inbox-triage", "orchestrate", "plan", "deep-research",
		"create-agent", "change-agent", "remove-agent", "write-skill", "install-skill",
		"team-delegation", "welcome-workspace", "add-connector", "remove-connector",
		"add-provider", "connect-channel", "disable-channel", "doctor-usage",
		"shell-diagnosis", "team-setup", "capability-plan", "dependency-check",
	}
	reference := []string{"define-goal", "provider-config"}
	required := []string{"description:", "## Prerequisites", "## Steps", "## Expected output", "## Stop and handoff"}
	for _, name := range append(append([]string{}, workflow...), reference...) {
		data, err := fs.ReadFile(embeddedSkills, embeddedRoot+"/"+name+"/SKILL.md")
		if err != nil {
			t.Fatalf("read embedded role skill %q: %v", name, err)
		}
		body := string(data)
		if err := ValidateSkillMarkdown(name, body); err != nil {
			t.Errorf("role skill %q validation: %v", name, err)
		}
		for _, retired := range []string{"system.skill.create", "system.skill.edit", "system.workspace.create", "spawn", "subagent"} {
			// Match complete names: subagent_3p remains a valid agent type.
			if regexp.MustCompile(`\b` + regexp.QuoteMeta(retired) + `\b`).MatchString(body) {
				t.Errorf("role skill %q references retired operation %q", name, retired)
			}
		}
	}
	for _, name := range workflow {
		data, err := fs.ReadFile(embeddedSkills, embeddedRoot+"/"+name+"/SKILL.md")
		if err != nil {
			t.Fatalf("read embedded role skill %q: %v", name, err)
		}
		for _, marker := range required {
			if !strings.Contains(string(data), marker) {
				t.Errorf("role skill %q missing required marker %q", name, marker)
			}
		}
	}
}
