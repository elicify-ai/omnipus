package skills

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if got, readErr := os.ReadFile(filepath.Join(stage, "SKILL.md")); readErr != nil || string(got) != content {
		t.Fatalf("staged markdown=%q err=%v", got, readErr)
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

func TestStageUploadedSkillRejectsAggregateExpansionAndCleansStaging(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "expanded-limit.zip")
	entries := map[string][]byte{"SKILL.md": []byte(validFor("expanded-limit"))}
	for i := 0; i < 11; i++ {
		entries["asset-"+string(rune('a'+i))] = bytes.Repeat([]byte{'x'}, 5*1024*1024)
	}
	writeUploadZip(t, archive, entries)

	stagingRoot := t.TempDir()
	installedTarget := filepath.Join(t.TempDir(), "installed", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(installedTarget), 0o755); err != nil {
		t.Fatal(err)
	}
	const installed = "installed revision remains unchanged"
	if err := os.WriteFile(installedTarget, []byte(installed), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, err := StageUploadedSkill(archive, stagingRoot)
	if err == nil || !strings.Contains(err.Error(), "expanded size") {
		t.Fatalf("error=%v, want expanded size rejection", err)
	}
	if entries, readErr := os.ReadDir(stagingRoot); readErr != nil || len(entries) != 0 {
		t.Fatalf("failed upload left staging entries=%v err=%v", entries, readErr)
	}
	if got, readErr := os.ReadFile(installedTarget); readErr != nil || string(got) != installed {
		t.Fatalf("installed target changed to %q err=%v", got, readErr)
	}
}

func TestStageUploadedSkillRejectsExcessiveEntryCountAndCleansStaging(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "entry-limit.zip")
	entries := make(map[string][]byte, 1025)
	entries["SKILL.md"] = []byte(validFor("entry-limit"))
	for i := 1; i < 1025; i++ {
		entries[fmt.Sprintf("assets/%04d.txt", i)] = []byte("x")
	}
	writeUploadZip(t, archive, entries)

	stagingRoot := t.TempDir()
	_, _, err := StageUploadedSkill(archive, stagingRoot)
	if err == nil || !strings.Contains(err.Error(), "too many entries") {
		t.Fatalf("error=%v, want entry-count rejection", err)
	}
	if entries, readErr := os.ReadDir(stagingRoot); readErr != nil || len(entries) != 0 {
		t.Fatalf("failed upload left staging entries=%v err=%v", entries, readErr)
	}
}

func TestStageUploadedSkillRejectsMalformedZipAndCleansStaging(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "malformed.zip")
	if err := os.WriteFile(archive, []byte("not a zip archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	stagingRoot := t.TempDir()

	_, _, err := StageUploadedSkill(archive, stagingRoot)
	if err == nil || !strings.Contains(err.Error(), "invalid ZIP") {
		t.Fatalf("error=%v, want invalid ZIP rejection", err)
	}
	if entries, readErr := os.ReadDir(stagingRoot); readErr != nil || len(entries) != 0 {
		t.Fatalf("malformed upload left staging entries=%v err=%v", entries, readErr)
	}
}

func writeUploadZip(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, createErr := zw.Create(name)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if _, writeErr := w.Write(content); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}
