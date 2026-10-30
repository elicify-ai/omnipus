// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skills

// Office-document preview — the merged office skill and the package
// inventory (office-preview spec AD-14, §6, FR-023/FR-028/FR-046;
// TDD rows T-27, T-54, T-74; BDD B-30).
//
// Oracles from the spec (AD-14 layout list), never from the code:
//   - one skill id "elicify-office"; the four old folders are deleted,
//     no aliases (§9 prohibitions: "keep the old skill names");
//   - SKILL.md opens with the AD-14 decision table; its rows name files
//     that exist in the package;
//   - layout: knowledge/{docx,xlsx,pptx,pdf}-{create,edit}.md, shared
//     knowledge/verify.md, knowledge/inspector-contract.md, the four
//     scripts, merged evals/evals.json re-labelled to elicify-office,
//     LICENSE;
//   - the ADR-090 package inventory carries the new adaptation statement
//     and checksums (T-74: "updated deliberately, still asserting
//     integrity").
//
// RED reasons on the pre-change code: the four old folders are still
// embedded and elicify-office does not exist (verified with ls before
// writing).

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const officeSkillID = "elicify-office"

// embeddedFS returns the compiled-in skill tree (embed.go:
// //go:embed all:embedded) rooted at embedded/ — the same tree boot seeds
// fresh installs from.
func embeddedFS() fs.FS {
	sub, err := fs.Sub(embeddedSkills, "embedded")
	if err != nil {
		panic(err) // the embed root is static; a failure is a package bug
	}
	return sub
}

func readEmbeddedFile(t *testing.T, path string) ([]byte, error) {
	t.Helper()
	return fs.ReadFile(embeddedFS(), path)
}

// readRepoFile reads a file relative to the repo root (go test runs with
// the package dir as CWD).
func readRepoFile(t *testing.T, rel string) ([]byte, error) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(root, rel))
}

// stripFrontmatter removes a leading `---`-fenced YAML frontmatter block.
func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---") {
		return s
	}
	rest := s[3:]
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		rest = rest[nl+1:]
	}
	end := strings.Index(rest, "\n---")
	if end == -1 {
		return rest
	}
	return strings.TrimPrefix(rest[end+4:], "\n")
}

func first(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// TestOfficePreview_OfficeSkillExists_OldSkillsDeleted (FR-023, B-30).
func TestOfficePreview_OfficeSkillExists_OldSkillsDeleted(t *testing.T) {
	fs := embeddedFS()
	if _, err := fs.Open(officeSkillID + "/SKILL.md"); err != nil {
		t.Fatalf("BLOCKED: embedded %q missing — required by office-preview spec AD-14/FR-023 (T-27, B-30): %v", officeSkillID, err)
	}
	for _, old := range []string{"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"} {
		if _, err := fs.Open(old + "/SKILL.md"); err == nil {
			t.Fatalf("retired skill %q still embedded — FR-023 deletes the four old folders outright, no aliases (T-27)", old)
		}
	}
}

// TestOfficePreview_OfficeSkillLayoutComplete (AD-14 layout list).
func TestOfficePreview_OfficeSkillLayoutComplete(t *testing.T) {
	want := []string{
		"knowledge/docx-create.md", "knowledge/docx-edit.md",
		"knowledge/xlsx-create.md", "knowledge/xlsx-edit.md",
		"knowledge/pptx-create.md", "knowledge/pptx-edit.md",
		"knowledge/pdf-create.md", "knowledge/pdf-edit.md",
		"knowledge/verify.md", "knowledge/inspector-contract.md",
		"scripts/inspect_docx.py", "scripts/inspect_workbook.py",
		"scripts/inspect_pptx.py", "scripts/inspect_pdf.py",
		"evals/evals.json", "LICENSE", "SKILL.md",
	}
	for _, path := range want {
		if _, err := embeddedFS().Open(officeSkillID + "/" + path); err != nil {
			t.Fatalf("BLOCKED: %s/%s missing — AD-14 layout requires it (T-27)", officeSkillID, path)
		}
	}
}

// TestOfficePreview_SKILLmd_OpensWithDecisionTable (FR-028, T-54, B-30):
// SKILL.md opens with the AD-14 table — the three read/preview/inspect rows
// with their exact Use entries — and never names starting a converter.
func TestOfficePreview_SKILLmd_OpensWithDecisionTable(t *testing.T) {
	data, err := readEmbeddedFile(t, officeSkillID+"/SKILL.md")
	if err != nil {
		t.Fatalf("BLOCKED: %s/SKILL.md unreadable — AD-14/FR-028 (T-54): %v", officeSkillID, err)
	}
	s := string(data)
	body := stripFrontmatter(s)

	// The table must come first: it opens the skill body.
	tableIdx := strings.Index(body, "| Job | Use | Never |")
	if tableIdx == -1 {
		t.Fatalf("BLOCKED: SKILL.md body does not open with the AD-14 decision table (\"| Job | Use | Never |\") — FR-028/T-54/B-30; body starts %q", first(body, 120))
	}
	if strings.TrimSpace(body[:tableIdx]) != "" {
		t.Fatalf("SKILL.md must open with the table; text precedes it: %q", first(body, 120))
	}
	for _, row := range []string{
		"| Read an existing document | `read_file`",
		"| Check how a document looks | `preview_document` (pages in batches) | rendering with your own tools |",
		"| Check package structure | `scripts/inspect_docx.py`",
		"| Missing dependency | `environment_setup`",
		"| OCR, secure redaction, signature validation | not provided",
		"| Create / edit Excel | `openpyxl`; formulas calculate on open (Q3) | LibreOffice recalculation |",
		"| Create / edit PDF | `reportlab` create; `pypdf` merge/split/rotate/fill | Poppler, `pypdfium2` |",
	} {
		if !strings.Contains(body, row) {
			t.Fatalf("BLOCKED: AD-14 table row missing from SKILL.md: %q (T-54)", row)
		}
	}
	if strings.Contains(strings.ToLower(body), "start a converter") || strings.Contains(strings.ToLower(body), "run libreoffice") {
		t.Fatalf("SKILL.md must never route agents to starting converters (§9 prohibitions, AD-14)")
	}
}

// TestOfficePreview_SKILLmd_RowsNameExistingFiles (T-54): every file the
// table and body reference exists inside the package.
func TestOfficePreview_SKILLmd_RowsNameExistingFiles(t *testing.T) {
	data, err := readEmbeddedFile(t, officeSkillID+"/SKILL.md")
	if err != nil {
		t.Fatalf("BLOCKED: %s/SKILL.md unreadable (T-54): %v", officeSkillID, err)
	}
	body := string(data)
	for _, ref := range []string{
		"knowledge/docx-create.md", "knowledge/docx-edit.md",
		"scripts/inspect_docx.py", "scripts/inspect_workbook.py",
		"scripts/inspect_pptx.py", "scripts/inspect_pdf.py",
	} {
		if strings.Contains(body, ref) {
			if _, err := embeddedFS().Open(officeSkillID + "/" + ref); err != nil {
				t.Fatalf("SKILL.md references %s but the file does not exist in the package (T-54)", ref)
			}
		}
	}
}

// TestOfficePreview_EvalsReLabelled (AD-14 layout: "merged evals/evals.json
// (re-labelled to elicify-office)").
func TestOfficePreview_EvalsReLabelled(t *testing.T) {
	data, err := readEmbeddedFile(t, officeSkillID+"/evals/evals.json")
	if err != nil {
		t.Fatalf("BLOCKED: merged evals/evals.json missing from %s — AD-14 layout (T-27)", officeSkillID)
	}
	s := string(data)
	if !strings.Contains(s, "elicify-office") {
		t.Fatalf("BLOCKED: evals/evals.json not re-labelled to elicify-office — AD-14 layout (T-27)")
	}
	for _, old := range []string{"elicify-docx", "elicify-xlsx", "elicify-pptx", "elicify-pdf"} {
		if strings.Contains(s, old) {
			t.Fatalf("evals/evals.json still carries retired skill id %q (T-27)", old)
		}
	}
}

// TestOfficePreview_PackageInventoryAdapted (T-74, §6 last row): the ADR-090
// inventory records the adaptation (elicify-office replacing the four
// packages) with checksums, so the integrity test can still assert it.
func TestOfficePreview_PackageInventoryAdapted(t *testing.T) {
	data, err := readRepoFile(t, "docs/internal/specs/adr-090-document-package-inventory.json")
	if err != nil {
		t.Fatalf("BLOCKED: ADR-090 document package inventory unreadable (T-74): %v", err)
	}
	s := string(data)
	if strings.Contains(s, "elicify-docx") || strings.Contains(s, "elicify-xlsx") || strings.Contains(s, "elicify-pptx") || strings.Contains(s, "elicify-pdf") {
		t.Fatalf("BLOCKED: inventory still lists the four retired packages — §6 requires the new adaptation statement (T-74)")
	}
	if !strings.Contains(s, "elicify-office") {
		t.Fatalf("BLOCKED: inventory lacks elicify-office — §6/AD-14 adaptation not recorded (T-74)")
	}
	if !strings.Contains(s, "sha256") && !strings.Contains(s, "checksum") {
		t.Fatalf("inventory carries no checksums — §6 requires integrity to stay assertable (T-74)")
	}
}
