package documentruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The office-preview AD-14 layout specifies these 17 shipped assets. Pin their
// presence, not an obsolete count from the former four-package distribution.
// This list is independent of DocumentSkillIDs and the provisioner's inventory.
// Skills spec r15 §11 moved the package out of the toolchain: it ships from the
// embedded registry source, and the toolchain inventory must hold no skill.
func assertOfficePackageInventory(t *testing.T, manifest Manifest) {
	t.Helper()
	embedded := filepath.Join("..", "skills", "embedded")
	for _, path := range []string{
		"SKILL.md", "LICENSE", "evals/evals.json",
		"knowledge/docx-create.md", "knowledge/docx-edit.md",
		"knowledge/xlsx-create.md", "knowledge/xlsx-edit.md",
		"knowledge/pptx-create.md", "knowledge/pptx-edit.md",
		"knowledge/pdf-create.md", "knowledge/pdf-edit.md",
		"knowledge/verify.md", "knowledge/inspector-contract.md",
		"scripts/inspect_docx.py", "scripts/inspect_workbook.py",
		"scripts/inspect_pptx.py", "scripts/inspect_pdf.py",
	} {
		if _, err := os.Stat(filepath.Join(embedded, "elicify-office", filepath.FromSlash(path))); err != nil {
			t.Errorf("office package lacks required AD-14 asset %s: %v", path, err)
		}
	}
	for _, asset := range manifest.Assets {
		if strings.HasPrefix(asset.Path, "skills/") {
			t.Errorf("skill copy in the toolchain inventory: %s", asset.Path)
		}
	}
	for _, old := range []string{"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"} {
		if _, err := os.Stat(filepath.Join(embedded, old, "SKILL.md")); !os.IsNotExist(err) {
			t.Errorf("retired package %s survived (AD-14: no aliases): %v", old, err)
		}
	}
}
