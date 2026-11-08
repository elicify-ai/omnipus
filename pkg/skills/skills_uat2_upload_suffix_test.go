package skills

// UAT run 2 R2-3, chief ruling option B: the upload handler renames a repeated
// same-named file to <base>_<unixnano>.<ext> (19 digits). StageUploadedSkill
// strips exactly that suffix before the slug check. The slug rule and the rule
// that the frontmatter name equals the slug are unchanged.

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func uat2WriteZip(t *testing.T, path, skillMD string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte(skillMD)); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStageUploadedSkill_StripsNineteenDigitRenameSuffix(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "good-skill_1730000000000000000.zip")
	uat2WriteZip(t, p, validFor("good-skill"))
	slug, stage, err := StageUploadedSkill(p, t.TempDir())
	if err != nil {
		t.Fatalf("renamed repeat upload refused: %v", err)
	}
	if slug != "good-skill" {
		t.Fatalf("slug=%q, want good-skill (stage %q)", slug, stage)
	}
	if _, statErr := os.Stat(filepath.Join(stage, "SKILL.md")); statErr != nil {
		t.Fatalf("staged SKILL.md missing: %v", statErr)
	}
}

func TestStageUploadedSkill_StripsSuffixFromMarkdownUploadToo(t *testing.T) {
	p := filepath.Join(t.TempDir(), "good-skill_1730000000000000000.md")
	if err := os.WriteFile(p, []byte(validFor("good-skill")), 0o644); err != nil {
		t.Fatal(err)
	}
	slug, _, err := StageUploadedSkill(p, t.TempDir())
	if err != nil || slug != "good-skill" {
		t.Fatalf("slug=%q err=%v", slug, err)
	}
}

// The frontmatter-name-equals-slug refusal is enforced at publish, not staging;
// it is covered at the gateway level (skills_uat2_upload_refusals_test.go).
func TestStageUploadedSkill_OnlyExactSuffixIsStrippedOthersStayRefused(t *testing.T) {
	cases := []struct {
		name, file, frontmatterName string
	}{
		{"fewer than 19 digits", "good-skill_123.zip", "good-skill"},
		{"twenty digits", "good-skill_17300000000000000000.zip", "good-skill"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), tc.file)
			uat2WriteZip(t, p, validFor(tc.frontmatterName))
			slug, stage, err := StageUploadedSkill(p, t.TempDir())
			if err == nil {
				t.Fatalf("accepted as %q (stage %q), want a refusal", slug, stage)
			}
			if err.Error() == "" {
				t.Fatal("refusal has no text")
			}
		})
	}
}
