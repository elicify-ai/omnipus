package skills

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestDefaultSkills_EmbeddedAndSeeded verifies FR-9.3: the default skill set is
// embedded into the binary and seeded into an empty skills dir on first boot.
//
// Traces to: Spec-6 BDD "Default skills embedded and seeded on fresh install".
func TestDefaultSkills_EmbeddedAndSeeded(t *testing.T) {
	want := []string{"daily-briefing", "define-done", "plan", "skill-authoring", "summarize"}

	// The embed FS must contain exactly the default set.
	got := DefaultSkillNames()
	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("DefaultSkillNames() = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DefaultSkillNames() = %v; want %v", got, want)
		}
	}

	// Seeding into an empty dir writes every default skill with a SKILL.md.
	dest := t.TempDir()
	res, err := SeedDefaults(dest)
	if err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}
	if len(res.Seeded) != len(want) {
		t.Fatalf("seeded %v; want %d skills", res.Seeded, len(want))
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("expected no skips on empty seed, got %v", res.Skipped)
	}
	for _, name := range want {
		skillFile := filepath.Join(dest, name, "SKILL.md")
		data, rerr := os.ReadFile(skillFile)
		if rerr != nil {
			t.Fatalf("seeded skill %q missing SKILL.md: %v", name, rerr)
		}
		// Each seeded skill must pass the loader's validation contract.
		if verr := ValidateSkillMarkdown(name, string(data)); verr != nil {
			t.Errorf("seeded skill %q fails validation: %v", name, verr)
		}
	}

	// A loader rooted at the seeded dir must list all of them.
	loader := NewSkillsLoader("", dest, "")
	listed := loader.ListSkills()
	if len(listed) != len(want) {
		t.Fatalf("loader listed %d skills after seed; want %d", len(listed), len(want))
	}
}

// TestSeedDefaults_Idempotent verifies that an existing skill dir is never
// overwritten (FR-9.3 edge case: "already present in the seed dir → not
// overwritten").
func TestSeedDefaults_Idempotent(t *testing.T) {
	dest := t.TempDir()

	// Pre-create a user-edited "plan" skill.
	planDir := filepath.Join(dest, "plan")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userContent := "---\nname: plan\ndescription: My customized plan skill that must survive seeding.\n---\n# Plan\nUser edited.\n"
	if err := os.WriteFile(filepath.Join(planDir, "SKILL.md"), []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := SeedDefaults(dest)
	if err != nil {
		t.Fatalf("SeedDefaults: %v", err)
	}

	// plan must be reported skipped, not seeded.
	if !contains(res.Skipped, "plan") {
		t.Errorf("expected 'plan' in Skipped, got Skipped=%v Seeded=%v", res.Skipped, res.Seeded)
	}
	if contains(res.Seeded, "plan") {
		t.Errorf("'plan' must not be re-seeded, got Seeded=%v", res.Seeded)
	}

	// The user content must be intact.
	data, rerr := os.ReadFile(filepath.Join(planDir, "SKILL.md"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(data) != userContent {
		t.Errorf("user-edited plan SKILL.md was overwritten by seeding")
	}

	// A second seed run is a no-op (everything skipped).
	res2, err := SeedDefaults(dest)
	if err != nil {
		t.Fatalf("second SeedDefaults: %v", err)
	}
	if len(res2.Seeded) != 0 {
		t.Errorf("second seed should write nothing, got Seeded=%v", res2.Seeded)
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
