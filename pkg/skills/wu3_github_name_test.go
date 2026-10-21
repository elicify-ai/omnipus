package skills

import (
	"path/filepath"
	"testing"
)

// #1276: a skill folder name taken from a GitHub link is validated before any
// filesystem use, and an encoded separator is refused before any component
// is derived (no network, no filesystem effect — the refusals come from pure
// functions that InstallFromGitHub calls first).
func TestWU3_1276_GitHubDerivedNames(t *testing.T) {
	for _, link := range []string{
		"https://github.com/fixture/evil%2Ffolder",
		"https://github.com/fixture/evil%5Cfolder",
		"https://github.com/fixture/evil%252Ffolder",
		"fixture/evil%2ffolder",
	} {
		if _, err := parseGitHubRef(link); err == nil {
			t.Errorf("%q: an encoded separator must be refused", link)
		}
	}
	if ref, err := parseGitHubRef("https://github.com/owner/repo/tree/main/skills/good-skill"); err != nil || ref.SubPath != "skills/good-skill" {
		t.Fatalf("control: a plain link must parse, got %+v %v", ref, err)
	}

	si := &SkillInstaller{workspace: t.TempDir()}
	for _, bad := range []string{"..", "a\\b", "CON", "nul", "Com1", "lpt1", "a.b", "a_b", "",
		"x234567890123456789012345678901234567890123456789012345678901234567"} {
		if _, err := si.derivedSkillDirectory(bad); err == nil {
			t.Errorf("%q must be refused as a skill folder name", bad)
		}
	}
	dir, err := si.derivedSkillDirectory("good-skill")
	if err != nil || dir != filepath.Join(si.workspace, "skills", "good-skill") {
		t.Fatalf("control: a valid name must map under skills/, got %q %v", dir, err)
	}
}
