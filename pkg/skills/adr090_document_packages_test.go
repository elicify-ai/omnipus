package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Expected bytes come from the pinned Elicify source commit, not the copied bundle.
func TestADR090_DocumentPackagesSeedPinnedAssets(t *testing.T) {
	data, err := os.ReadFile("../../docs/internal/specs/adr-090-document-package-inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		SourceCommit string            `json:"source_commit"`
		Files        map[string]string `json:"files"`
	}
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.SourceCommit != "f5c241825045341b9b71a9c1f369c24ba6de4f9c" {
		t.Fatal("unexpected source revision")
	}
	for _, name := range []string{"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"} {
		if _, ok := inventory.Files[name+"/SKILL.md"]; !ok {
			t.Fatalf("missing inventory for %s", name)
		}
	}
	dest := t.TempDir()
	result, err := SeedDefaults(dest)
	if err != nil {
		t.Fatal(err)
	}
	seeded := map[string]bool{}
	for _, name := range result.Seeded {
		seeded[name] = true
	}
	for _, name := range []string{"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"} {
		if !seeded[name] {
			t.Errorf("fresh install did not seed %s", name)
		}
	}
	for path, expected := range inventory.Files {
		body, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(path)))
		if err != nil {
			t.Errorf("required package asset %s: %v", path, err)
			continue
		}
		digest := sha256.Sum256(body)
		if got := hex.EncodeToString(digest[:]); got != expected {
			t.Errorf("%s differs from pinned source: got %s, want %s", path, got, expected)
		}
	}
}
