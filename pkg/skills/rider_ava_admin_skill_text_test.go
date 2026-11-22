//go:build goolm && stdjson

package skills

// Rider #1920 (S-75, Test Implementation Order 32): the built-in skills of
// this release name no retired agent. Ava and Admin are retired (FR-008,
// spec "Retired surfaces"); no embedded SKILL.md may carry either word as an
// agent name. The scan reads the embed — the bytes granted agents actually
// receive (SeedDefaults never overwrites an existing skill dir) — and each
// failure names the file and line so the fix is mechanical. Oracle: S-75's
// Given/When/Then, not any production constant.

import (
	"bufio"
	"io/fs"
	"regexp"
	"testing"
)

func TestRiderS75_BuiltinSkillsNameNoRetiredAgent(t *testing.T) {
	// "the words Ava and Admin as agent names": the retired agents are
	// spelled with a capital initial; bare-word case-sensitive matching
	// leaves ordinary prose ("administrative", "admin console") out, exactly
	// as FR-008's guard scopes usernames and roles out.
	re := regexp.MustCompile(`\b(Ava|Admin)\b`)
	paths, err := fs.Glob(embeddedSkills, embeddedRoot+"/*/SKILL.md")
	if err != nil {
		t.Fatalf("glob embedded skills: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("BLOCKED: no embedded SKILL.md files found — the S-75 scan target is missing from pkg/skills/embedded")
	}
	for _, path := range paths {
		f, err := embeddedSkills.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		sc := bufio.NewScanner(f)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			if hit := re.FindString(sc.Text()); hit != "" {
				t.Errorf("%s:%d: embedded skill names retired agent %q — required by S-75 (rider #1920, FR-008)", path, lineNo, hit)
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			t.Fatalf("scan %s: %v", path, err)
		}
	}
}
