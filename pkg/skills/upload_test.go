package skills

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestStageUploadedSkillSupportsMarkdownAndBoundedZip(t *testing.T) {
	content := validFor("local-skill")
	markdown := filepath.Join(t.TempDir(), "local-skill.md")
	if err := os.WriteFile(markdown, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	slug, stage, err := StageUploadedSkill(markdown, t.TempDir())
	if err != nil || slug != "local-skill" {
		t.Fatalf("slug=%q stage=%q err=%v", slug, stage, err)
	}
	if got, err := os.ReadFile(filepath.Join(stage, "SKILL.md")); err != nil || string(got) != content {
		t.Fatalf("staged markdown=%q err=%v", got, err)
	}

	archive := filepath.Join(t.TempDir(), "zip-skill.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte(validFor("zip-skill"))); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	slug, stage, err = StageUploadedSkill(archive, t.TempDir())
	if err != nil || slug != "zip-skill" {
		t.Fatalf("slug=%q stage=%q err=%v", slug, stage, err)
	}
}

func TestStageUploadedSkillRejectsUnsupportedAndTraversalZip(t *testing.T) {
	jsonPath := filepath.Join(t.TempDir(), "skill.json")
	if err := os.WriteFile(jsonPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := StageUploadedSkill(jsonPath, t.TempDir()); err == nil {
		t.Fatal("JSON skill accepted")
	}

	archive := filepath.Join(t.TempDir(), "evil.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("../outside")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("escape"))
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = StageUploadedSkill(archive, t.TempDir()); err == nil {
		t.Fatal("traversal ZIP accepted")
	}
}
