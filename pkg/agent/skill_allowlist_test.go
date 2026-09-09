package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSkill creates a valid SKILL.md under workspace/skills/<name>/.
func writeSkill(t *testing.T, workspace, name string) {
	t.Helper()
	dir := filepath.Join(workspace, "skills", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: A test skill with a sufficiently long description to validate.\n---\n\n# " + name + "\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSkillAllowlist_PerAgent_DefaultDeny verifies FR-9.4: a skill that exists
// on disk but is NOT in the agent's allowlist cannot be resolved (and therefore
// cannot be invoked via /use or loaded into context). This is the negative test
// (M-5): default-DENY at tool-resolution.
func TestSkillAllowlist_PerAgent_DefaultDeny(t *testing.T) {
	workspace := t.TempDir()
	// Three skills exist on disk.
	writeSkill(t, workspace, "summarize")
	writeSkill(t, workspace, "plan")
	writeSkill(t, workspace, "daily-briefing")

	t.Run("allowed skill resolves", func(t *testing.T) {
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"summarize"})
		got, ok := cb.ResolveSkillName("summarize")
		if !ok || got != "summarize" {
			t.Fatalf("allowlisted skill must resolve: got (%q,%v)", got, ok)
		}
	})

	t.Run("non-allowlisted skill is DENIED", func(t *testing.T) {
		// Agent's allowlist is [summarize]; "plan" exists on disk but is NOT allowed.
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"summarize"})
		if _, ok := cb.ResolveSkillName("plan"); ok {
			t.Fatal("non-allowlisted skill 'plan' must NOT resolve (default-DENY)")
		}
		// daily-briefing also denied.
		if _, ok := cb.ResolveSkillName("daily-briefing"); ok {
			t.Fatal("non-allowlisted skill 'daily-briefing' must NOT resolve")
		}
	})

	t.Run("progressive disclosure hides non-allowlisted skills", func(t *testing.T) {
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{"summarize"})
		names := cb.ListSkillNames()
		if len(names) != 1 || names[0] != "summarize" {
			t.Fatalf("ListSkillNames must show only allowlisted skills, got %v", names)
		}
	})

	// ADR-072 D5: absence of a grant list means NO skills, matching the
	// shipped contract (Agent.yaml's "opt-in, default none" — absence of the
	// field and an empty array are semantically identical). This replaces the
	// pre-ADR-072 "nil allowlist is unrestricted" behavior, which was the
	// exact inversion D5 exists to remove: "no list ⇒ unrestricted" punished
	// the specific operator who set an allowlist while rewarding the one who
	// never configured one at all.
	t.Run("nil allowlist denies everything (ADR-072 D5)", func(t *testing.T) {
		cb := NewContextBuilder(workspace) // no WithSkillAllowlist → nil
		for _, name := range []string{"summarize", "plan", "daily-briefing"} {
			if _, ok := cb.ResolveSkillName(name); ok {
				t.Errorf("with no allowlist configured, %q must NOT resolve (ADR-072 D5 default-none)", name)
			}
		}
		if names := cb.ListSkillNames(); len(names) != 0 {
			t.Fatalf("with no allowlist configured, ListSkillNames must show no registry skills, got %v", names)
		}
	})

	t.Run("empty allowlist denies all", func(t *testing.T) {
		cb := NewContextBuilder(workspace).WithSkillAllowlist([]string{})
		if _, ok := cb.ResolveSkillName("summarize"); ok {
			t.Fatal("empty (non-nil) allowlist must deny every skill")
		}
		if names := cb.ListSkillNames(); len(names) != 0 {
			t.Fatalf("empty allowlist must list no skills, got %v", names)
		}
	})
}
