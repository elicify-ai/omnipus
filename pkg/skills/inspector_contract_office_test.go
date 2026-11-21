// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package skills

// Office-document preview — the four document inspectors against their
// contracts (office-preview spec §6 P4/FR-046, TDD rows T-72/T-75, BDD
// B-36/B-52).
//
// Oracles are the merged elicify-office inspector contract (AD-14), never
// the scripts' observed output. Fixtures use literal XML and real python-pptx,
// pypdf and openpyxl libraries. pythonBin selects python3 from the test PATH;
// missing dependencies remain loud failures, not skips. The configured test
// virtualenv proves script behavior, not live-product runtime readiness or
// the three-platform managed-runtime acceptance required by T-72.

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	// Office-preview AD-14: all four inspectors ship under the single office ID.
	docxScript = "embedded/elicify-office/scripts/inspect_docx.py"
	pptxScript = "embedded/elicify-office/scripts/inspect_pptx.py"
	pdfScript  = "embedded/elicify-office/scripts/inspect_pdf.py"
	xlsxScript = "embedded/elicify-office/scripts/inspect_workbook.py"
	slideEMU   = 1000000 // pptx contract: fixed 1,000,000 × 1,000,000 EMU decks
)

func pythonBin(t *testing.T) string {
	t.Helper()
	if p, err := exec.LookPath("python3"); err == nil {
		return p
	}
	t.Fatal("BLOCKED: no python3 available — T-72 drives the inspectors through a managed interpreter (office-preview spec §6 P4)")
	return ""
}

// runInspector loads one inspector module and evaluates callExpr (python),
// which must return a JSON-serialisable value. A raised exception comes
// back as {"err": "TypeName: message"}; success as {"ok": ...}.
func runInspector(t *testing.T, embeddedPath, callExpr string) map[string]any {
	t.Helper()
	script := filepath.Clean(embeddedPath) // consts carry the embedded/ prefix relative to the package dir
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("BLOCKED: inspector script %s missing (T-72): %v", embeddedPath, err)
	}
	scriptLit, _ := json.Marshal(script)
	code := fmt.Sprintf(`
import importlib.util, json
spec = importlib.util.spec_from_file_location("insp", %s)
insp = importlib.util.module_from_spec(spec)
spec.loader.exec_module(insp)
try:
    print(json.dumps({"ok": (%s)}))
except Exception as exc:
    print(json.dumps({"err": type(exc).__name__ + ": " + str(exc)}))
`, scriptLit, callExpr)
	out, err := exec.Command(pythonBin(t), "-I", "-B", "-c", code).CombinedOutput()
	if err != nil {
		t.Fatalf("inspector run failed: %v\n%s", err, out)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &parsed); err != nil {
		t.Fatalf("inspector output not one JSON object: %q", string(out))
	}
	return parsed
}

func wantContractError(t *testing.T, got map[string]any, errType, msgPart string) {
	t.Helper()
	errStr, _ := got["err"].(string)
	if errStr == "" {
		t.Fatalf("expected %s (%q), got success: %v", errType, msgPart, got["ok"])
	}
	if !strings.HasPrefix(errStr, errType+":") || (msgPart != "" && !strings.Contains(errStr, msgPart)) {
		t.Fatalf("expected %s containing %q, got %q", errType, msgPart, errStr)
	}
}

func assertUnchanged(t *testing.T, path, before string) {
	t.Helper()
	if after := sha256of(t, path); after != before {
		t.Fatalf("inspector modified the input bytes (%s… → %s…) — contracts require read-only inspection", before[:12], after[:12])
	}
}

func sha256of(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// ---- docx fixtures: literal XML, counts derived from the fixture recipe ----

const docxNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`
const relsNS = `xmlns="http://schemas.openxmlformats.org/package/2006/relationships"`

func docxPara(text string) string {
	return `<w:p><w:r><w:t>` + text + `</w:t></w:r></w:p>`
}

func buildDocx(t *testing.T, parts map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.docx")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	names := make([]string, 0, len(parts))
	for name := range parts {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(parts[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func num(t *testing.T, got map[string]any, key string) float64 {
	t.Helper()
	result, ok := got["ok"].(map[string]any)
	if !ok {
		t.Fatalf("inspector did not return an object: %v", got)
	}
	v, ok := result[key].(float64)
	if !ok {
		t.Fatalf("result lacks numeric %s: %v", key, got)
	}
	return v
}

// TestOfficePreview_InspectorDocx_ContractCases (T-72): counts and errors
// per elicify-office/knowledge/inspector-contract.md (Word section).
func TestOfficePreview_InspectorDocx_ContractCases(t *testing.T) {
	// Empty document: all counts zero.
	parts := map[string]string{
		"word/document.xml": `<w:document ` + docxNS + `><w:body/></w:document>`,
	}
	path := buildDocx(t, parts)
	got := runInspector(t, docxScript, fmt.Sprintf("insp.inspect_docx(%q)", path))
	res, _ := got["ok"].(map[string]any)
	if res == nil {
		t.Fatalf("empty document must inspect cleanly: %v", got)
	}
	if num(t, got, "paragraphs") != 0 || num(t, got, "tables") != 0 {
		t.Fatalf("empty document counts wrong: %v (contract: zero)", res)
	}

	// Nested content: 2 body paragraphs + table with 2 nested paragraphs
	// => paragraphs counts ALL w:p including nested (contract), tables 1.
	parts = map[string]string{
		"word/document.xml": `<w:document ` + docxNS + `><w:body>` +
			docxPara("one") + docxPara("two") +
			`<w:tbl><w:tr><w:tc>` + docxPara("n1") + docxPara("n2") + `</w:tc></w:tr></w:tbl>` +
			`</w:body></w:document>`,
	}
	path = buildDocx(t, parts)
	got = runInspector(t, docxScript, fmt.Sprintf("insp.inspect_docx(%q)", path))
	if num(t, got, "paragraphs") != 4 || num(t, got, "tables") != 1 {
		t.Fatalf("nested content counts wrong: %v (contract: paragraphs=4 incl. nested, tables=1)", got["ok"])
	}

	// Revision marks + fields, including a non-document Word part.
	parts = map[string]string{
		"word/document.xml": `<w:document ` + docxNS + `><w:body>` +
			`<w:p><w:ins ` + docxNS + ` w:id="1" w:author="a"><w:r><w:t>x</w:t></w:r></w:ins>` +
			`<w:del ` + docxNS + ` w:id="2" w:author="a"><w:r><w:delText>y</w:delText></w:r></w:del>` +
			`<w:fldSimple w:instr=" PAGE "><w:r><w:t>1</w:t></w:r></w:fldSimple>` +
			`<w:r><w:instrText> DATE </w:instrText></w:r></w:p>` +
			`</w:body></w:document>`,
		"word/header1.xml": `<w:hdr ` + docxNS + `><w:p><w:ins ` + docxNS + ` w:id="3" w:author="a"><w:r><w:t>h</w:t></w:r></w:ins></w:p></w:hdr>`,
	}
	path = buildDocx(t, parts)
	got = runInspector(t, docxScript, fmt.Sprintf("insp.inspect_docx(%q)", path))
	if num(t, got, "revision_marks") != 3 {
		t.Fatalf("revision_marks = %v, want 3 (2 in document + 1 in header; contract counts all Word XML parts)", got["ok"])
	}
	if num(t, got, "field_instructions") != 2 {
		t.Fatalf("field_instructions = %v, want 2 (fldSimple + instrText)", got["ok"])
	}

	// External relationships come back sorted by (part, target).
	parts = map[string]string{
		"word/document.xml": `<w:document ` + docxNS + `><w:body>` + docxPara("x") + `</w:body></w:document>`,
		"word/_rels/document.xml.rels": `<Relationships ` + relsNS + `>` +
			`<Relationship Id="r2" Type="http://example/hyperlink" TargetMode="External" Target="http://z.example"/>` +
			`<Relationship Id="r1" Type="http://example/hyperlink" TargetMode="External" Target="http://a.example"/>` +
			`</Relationships>`,
		"word/media/":         "",
		"word/media/img1.png": "png",
	}
	path = buildDocx(t, parts)
	got = runInspector(t, docxScript, fmt.Sprintf("insp.inspect_docx(%q)", path))
	res, ok := got["ok"].(map[string]any)
	if !ok {
		t.Fatalf("relationship fixture must return an object: %v", got)
	}
	ext, ok := res["external_relationships"].([]any)
	if !ok || len(ext) != 2 {
		t.Fatalf("external_relationships = %v, want 2 entries", res["external_relationships"])
	}
	firstRelationship, ok := ext[0].(map[string]any)
	if !ok {
		t.Fatalf("first external relationship must be an object: %v", ext[0])
	}
	first, ok := firstRelationship["target"].(string)
	if !ok || first != "http://a.example" {
		t.Fatalf("external_relationships must be sorted by (part, target); first target = %v", firstRelationship["target"])
	}
	media, ok := res["media_parts"].([]any)
	if !ok {
		t.Fatalf("media_parts must be an array: %v", res["media_parts"])
	}
	if len(media) != 1 || media[0] != "word/media/img1.png" {
		t.Fatalf("media_parts = %v, want [word/media/img1.png]", media)
	}

	// Error cases, exact ValueError texts from the contract.
	junk := filepath.Join(t.TempDir(), "junk.docx")
	if err := os.WriteFile(junk, []byte("not a zip at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantContractError(t, runInspector(t, docxScript, fmt.Sprintf("insp.inspect_docx(%q)", junk)), "ValueError", "not a ZIP-based Word document")

	noDoc := buildDocx(t, map[string]string{"word/other.xml": "<x/>"})
	wantContractError(t, runInspector(t, docxScript, fmt.Sprintf("insp.inspect_docx(%q)", noDoc)), "ValueError", "missing word/document.xml")

	badXML := buildDocx(t, map[string]string{"word/document.xml": `<w:document ` + docxNS + `><w:body>`})
	wantContractError(t, runInspector(t, docxScript, fmt.Sprintf("insp.inspect_docx(%q)", badXML)), "ValueError", "malformed XML: word/document.xml")

	missing := filepath.Join(t.TempDir(), "gone.docx")
	wantContractError(t, runInspector(t, docxScript, fmt.Sprintf("insp.inspect_docx(%q)", missing)), "FileNotFoundError", "")
}

// ---- pptx: real decks via python-pptx (installed here) ----

// TestOfficePreview_InspectorPptx_ContractCases (T-72): geometry, ordering,
// error texts per elicify-office/knowledge/inspector-contract.md (PowerPoint section).
func TestOfficePreview_InspectorPptx_ContractCases(t *testing.T) {
	buildFixture := func(shapeLines []string) (string, []int) {
		lit := shapeLines // each line is already a python list literal
		code := `
from pptx import Presentation
from pptx.util import Emu
import json, sys
deck = Presentation()
deck.slide_width = Emu(` + strconv.FormatInt(slideEMU, 10) + `)
deck.slide_height = Emu(` + strconv.FormatInt(slideEMU, 10) + `)
ids = []
spec_list = [` + strings.Join(lit, ",") + `]
if spec_list:
    slide = deck.slides.add_slide(deck.slide_layouts[6])
    for spec in spec_list:
        x, y, w, h = spec
        box = slide.shapes.add_textbox(Emu(x), Emu(y), Emu(w), Emu(h))
        ids.append(box.shape_id)
path = sys.argv[1]
deck.save(path)
print(json.dumps(ids))
`
		fixture := filepath.Join(t.TempDir(), "deck.pptx")
		out, err := exec.Command(pythonBin(t), "-I", "-B", "-c", code, fixture).CombinedOutput()
		if err != nil {
			t.Fatalf("python-pptx fixture failed: %v\n%s", err, out)
		}
		var ids []int
		if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &ids); err != nil {
			t.Fatalf("fixture ids unparsable: %q", string(out))
		}
		return fixture, ids
	}

	t.Run("touching edges allowed, one EMU beyond reported once per shape", func(t *testing.T) {
		// Contract: x,y >= 0 and x+w <= 1,000,000, y+h <= 1,000,000;
		// touching allowed; -1/+1 beyond reported; one entry per shape.
		fixture, ids := buildFixture([]string{
			"[-1, 0, 100000, 100000]",    // negative x: out
			"[0, 0, 1000000, 1000000]",   // touching all edges: in
			"[999999, 0, 2, 100000]",     // right +1: out
			"[0, -1, 100000, 100000]",    // negative y: out
			"[0, 999999, 100000, 2]",     // bottom +1: out
			"[-1, -1, 1000000, 1000000]", // two edges: reported once
		})
		before := sha256of(t, fixture)
		got := runInspector(t, pptxScript, fmt.Sprintf("insp.inspect_pptx(%q)", fixture))
		res, ok := got["ok"].(map[string]any)
		if !ok {
			t.Fatalf("deck must inspect: %v", got)
		}
		if num(t, got, "slide_count") != 1 || num(t, got, "width_emu") != slideEMU || num(t, got, "height_emu") != slideEMU {
			t.Fatalf("deck facts wrong: %v", res)
		}
		type oob struct {
			Slide   int `json:"slide"`
			ShapeID int `json:"shape_id"`
		}
		raw, _ := json.Marshal(res["out_of_bounds"])
		var entries []oob
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("out_of_bounds shape wrong: %v", res["out_of_bounds"])
		}
		want := []oob{
			{1, ids[0]}, {1, ids[2]}, {1, ids[3]}, {1, ids[4]}, {1, ids[5]},
		}
		if !reflect.DeepEqual(entries, want) {
			t.Fatalf("out_of_bounds = %v, want %v (contract: touching allowed, -1/+1 beyond reported, reported once)", entries, want)
		}
		assertUnchanged(t, fixture, before)
	})

	t.Run("zero slides", func(t *testing.T) {
		fixture, _ := buildFixture(nil)
		got := runInspector(t, pptxScript, fmt.Sprintf("insp.inspect_pptx(%q)", fixture))
		if num(t, got, "slide_count") != 0 {
			t.Fatalf("slide_count = %v, want 0", got["ok"])
		}
	})

	t.Run("error texts and missing file", func(t *testing.T) {
		junk := filepath.Join(t.TempDir(), "junk.pptx")
		if err := os.WriteFile(junk, []byte("still not a zip"), 0o600); err != nil {
			t.Fatal(err)
		}
		wantContractError(t, runInspector(t, pptxScript, fmt.Sprintf("insp.inspect_pptx(%q)", junk)), "ValueError", "not a ZIP-based presentation")

		wrong := filepath.Join(t.TempDir(), "wrong.pptx")
		f, err := os.Create(wrong)
		if err != nil {
			t.Fatal(err)
		}
		w := zip.NewWriter(f)
		entry, _ := w.Create("word/document.xml")
		entry.Write([]byte("<doc/>"))
		w.Close()
		f.Close()
		wantContractError(t, runInspector(t, pptxScript, fmt.Sprintf("insp.inspect_pptx(%q)", wrong)), "ValueError", "missing ppt/presentation.xml")

		missing := filepath.Join(t.TempDir(), "gone.pptx")
		wantContractError(t, runInspector(t, pptxScript, fmt.Sprintf("insp.inspect_pptx(%q)", missing)), "FileNotFoundError", "")
	})
}

// ---- pdf: real files via pypdf (installed here) ----

// TestOfficePreview_InspectorPdf_ContractCases (T-72): page geometry in
// MediaBox points BEFORE rotation, rotation, fields, encryption, error
// texts, byte preservation — per elicify-office/knowledge/inspector-contract.md (PDF section).
func TestOfficePreview_InspectorPdf_ContractCases(t *testing.T) {
	buildPDF := func(body string) string {
		code := `
import sys
` + body
		fixture := filepath.Join(t.TempDir(), "fixture.pdf")
		out, err := exec.Command(pythonBin(t), "-I", "-B", "-c", code, fixture).CombinedOutput()
		if err != nil {
			t.Fatalf("pypdf fixture failed: %v\n%s", err, out)
		}
		return fixture
	}

	t.Run("two pages, geometry before rotation, rotation reported", func(t *testing.T) {
		fixture := buildPDF(`
from pypdf import PdfWriter
w = PdfWriter()
p1 = w.add_blank_page(72, 144)   # contract: 72x144
p2 = w.add_blank_page(144, 72)   # contract: 144x72
p2.rotation = 90
with open(sys.argv[1], "wb") as fh:
    w.write(fh)
`)
		before := sha256of(t, fixture)
		got := runInspector(t, pdfScript, fmt.Sprintf("insp.inspect_pdf(%q)", fixture))
		res, ok := got["ok"].(map[string]any)
		if !ok {
			t.Fatalf("pdf must inspect: %v", got)
		}
		if num(t, got, "page_count") != 2 {
			t.Fatalf("page_count = %v, want 2", got["ok"])
		}
		type page struct {
			Number       int     `json:"number"`
			WidthPoints  float64 `json:"width_points"`
			HeightPoints float64 `json:"height_points"`
			Rotation     int     `json:"rotation"`
		}
		raw, _ := json.Marshal(res["pages"])
		var pages []page
		if err := json.Unmarshal(raw, &pages); err != nil {
			t.Fatalf("pages shape wrong: %v", res["pages"])
		}
		want := []page{
			{1, 72, 144, 0},
			{2, 144, 72, 90}, // MediaBox points, rotation separate (contract)
		}
		if !reflect.DeepEqual(pages, want) {
			t.Fatalf("pages = %v, want %v (contract: MediaBox BEFORE rotation)", pages, want)
		}
		assertUnchanged(t, fixture, before)
	})

	t.Run("encrypted: correct, wrong, missing password", func(t *testing.T) {
		fixture := buildPDF(`
from pypdf import PdfWriter
w = PdfWriter()
w.add_blank_page(72, 72)
w.encrypt("secret")
with open(sys.argv[1], "wb") as fh:
    w.write(fh)
`)
		// correct password succeeds and reports the ORIGINAL encrypted state
		got := runInspector(t, pdfScript, fmt.Sprintf("insp.inspect_pdf(%q, 'secret')", fixture))
		res, ok := got["ok"].(map[string]any)
		if !ok {
			t.Fatalf("correct password must open the PDF: %v", got)
		}
		if res["encrypted"] != true {
			t.Fatalf("encrypted must report the original file state (true), got %v", res["encrypted"])
		}
		// wrong password
		wantContractError(t, runInspector(t, pdfScript, fmt.Sprintf("insp.inspect_pdf(%q, 'wrong')", fixture)), "ValueError", "PDF password required or incorrect")
		// missing password
		wantContractError(t, runInspector(t, pdfScript, fmt.Sprintf("insp.inspect_pdf(%q)", fixture)), "ValueError", "PDF password required or incorrect")
	})

	t.Run("fields and signature classification", func(t *testing.T) {
		// Real AcroForm with one text field and one signature field, built
		// as raw PDF bytes (pypdf 6.x has no public field-creation API);
		// names and classification are the contract's oracle.
		fixture := buildPDF(`
import sys
# Fixed object numbering: 1=font 2=contents 3=page 4=pages 5=tx 6=sig 7=acroform 8=catalog 9=info
font, stream, page, pages, tx, sig, acro, catalog, info = 1, 2, 3, 4, 5, 6, 7, 8, 9
content = b"BT /F1 12 Tf 72 720 Td (f) Tj ET"
objects = {
    1: b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
    2: b"<< /Length %d >>\nstream\n" % len(content) + content + b"\nendstream",
    3: b"<< /Type /Page /Parent 4 0 R /MediaBox [0 0 612 792] /Contents 2 0 R /Resources << /Font << /F1 1 0 R >> >> >>",
    4: b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
    5: b"<< /FT /Tx /T /client_name /Subtype /Widget /Rect [72 700 300 720] /P 3 0 R >>",
    6: b"<< /FT /Sig /T /approval_sig /Subtype /Widget /Rect [72 660 300 680] /P 3 0 R >>",
    7: b"<< /Fields [5 0 R 6 0 R] >>",
    8: b"<< /Type /Catalog /Pages 4 0 R /AcroForm 7 0 R >>",
    9: b"<< >>",
}
out = bytearray(b"%PDF-1.4\n")
offsets = {}
for i in range(1, len(objects) + 1):
    offsets[i] = len(out)
    out += b"%d 0 obj\n" % i + objects[i] + b"\nendobj\n"
xref = len(out)
out += b"xref\n0 %d\n" % (len(objects) + 1)
out += b"0000000000 65535 f \n"
for i in range(1, len(objects) + 1):
    out += b"%010d 00000 n \n" % offsets[i]
out += b"trailer\n<< /Size %d /Root 8 0 R >>\nstartxref\n%d\n%%%%EOF\n" % (len(objects) + 1, xref)
with open(sys.argv[1], "wb") as fh:
    fh.write(bytes(out))
`)
		got := runInspector(t, pdfScript, fmt.Sprintf("insp.inspect_pdf(%q)", fixture))
		res, ok := got["ok"].(map[string]any)
		if !ok {
			t.Fatalf("AcroForm PDF must inspect: %v", got)
		}
		fieldsRaw, _ := json.Marshal(res["fields"])
		if string(fieldsRaw) != `["/approval_sig","/client_name"]` {
			t.Fatalf("fields = %s, want sorted [/approval_sig /client_name] (contract)", fieldsRaw)
		}
		sigRaw, _ := json.Marshal(res["signature_fields"])
		if string(sigRaw) != `["/approval_sig"]` {
			t.Fatalf("signature_fields = %s, want [/approval_sig] (contract: /FT == /Sig)", sigRaw)
		}
	})

	t.Run("corrupt, missing file", func(t *testing.T) {
		junk := filepath.Join(t.TempDir(), "junk.pdf")
		if err := os.WriteFile(junk, []byte("definitely not a pdf"), 0o600); err != nil {
			t.Fatal(err)
		}
		wantContractError(t, runInspector(t, pdfScript, fmt.Sprintf("insp.inspect_pdf(%q)", junk)), "ValueError", "invalid PDF")
		missing := filepath.Join(t.TempDir(), "gone.pdf")
		wantContractError(t, runInspector(t, pdfScript, fmt.Sprintf("insp.inspect_pdf(%q)", missing)), "FileNotFoundError", "")
	})
}

// ---- workbook (openpyxl): the managed-interpreter leg ----

// TestOfficePreview_InspectorWorkbook_ManagedInterpreter (T-72) retains its
// original row name. Run the complete workbook contract through the configured
// document Python; a missing openpyxl dependency is still a loud BLOCKED result.
func TestOfficePreview_InspectorWorkbook_ManagedInterpreter(t *testing.T) {
	hasOpenpyxl := exec.Command(pythonBin(t), "-I", "-B", "-c", "import openpyxl").Run() == nil
	if hasOpenpyxl {
		// openpyxl available: run the contract's core cases here.
		runWorkbookCases(t)
		return
	}
	if _, err := os.Stat(filepath.Clean(xlsxScript)); err != nil {
		t.Fatalf("BLOCKED: inspector script %s missing (T-72): %v", xlsxScript, err)
	}
	t.Fatal("BLOCKED: managed interpreter with openpyxl not provisioned on this machine — office-preview spec §6 P4/T-72 requires the workbook inspector tested through the managed interpreter (contract cases: empty sheet 1x1, formula without cache, cached zero, stored and cached errors, two sheets in order, unsupported extension, corrupt package, valid ZIP without workbook part, undersized declared dimensions, macro/external-link package presence, byte non-mutation); owned by the CI/managed-interpreter leg")
}

// Workbook cases live in inspector_workbook_office_test.go so the fixture and
// full contract checks stay separate from the other three format inspectors.
