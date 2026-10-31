package documentruntime

import "testing"

// The office-preview AD-14 layout specifies these 17 shipped assets. Pin their
// presence, not an obsolete count from the former four-package distribution.
// This list is independent of DocumentSkillIDs and the provisioner's inventory.
func assertOfficePackageInventory(t *testing.T, manifest Manifest) {
	t.Helper()
	paths := make(map[string]bool, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		paths[asset.Path] = true
	}
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
		rel := "skills/elicify-office/" + path
		if !paths[rel] {
			t.Errorf("document inventory lacks required AD-14 asset %s", rel)
		}
	}
	for _, old := range []string{"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"} {
		if paths["skills/"+old+"/SKILL.md"] {
			t.Errorf("retired package %s survived fresh provisioning (AD-14: no aliases)", old)
		}
	}
}
