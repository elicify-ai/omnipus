package skills

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestADR090_PromptSkillToolReferences_RoleSkillPackagesAreComplete(t *testing.T) {
	want := []string{"interview", "handoff", "orchestrate", "deep-research", "agent-authoring", "tool-mapping", "skill-mapping", "delegation-graph", "workspace-team", "mcp-install", "provider-setup", "channel-setup", "doctor", "verify", "inbox-triage"}
	required := []string{"description:", "## Prerequisites", "## Steps", "## Expected output", "## Stop and handoff"}
	for _, name := range want {
		data, err := fs.ReadFile(embeddedSkills, embeddedRoot+"/"+name+"/SKILL.md")
		if err != nil {
			t.Fatalf("read embedded role skill %q: %v", name, err)
		}
		body := string(data)
		if err := ValidateSkillMarkdown(name, body); err != nil {
			t.Errorf("role skill %q validation: %v", name, err)
		}
		for _, marker := range required {
			if !strings.Contains(body, marker) {
				t.Errorf("role skill %q missing required marker %q", name, marker)
			}
		}
		for _, retired := range []string{"system.skill.create", "system.skill.edit", "system.workspace.create", "spawn", "subagent"} {
			// Match complete names: subagent_3p remains a valid agent type.
			if regexp.MustCompile(`\b` + regexp.QuoteMeta(retired) + `\b`).MatchString(body) {
				t.Errorf("role skill %q references retired operation %q", name, retired)
			}
		}
	}
}
