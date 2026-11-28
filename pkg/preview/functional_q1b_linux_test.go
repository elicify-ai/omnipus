//go:build linux

package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/preview/component"
	"github.com/elicify-ai/omnipus/pkg/preview/protocol"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
	"github.com/elicify-ai/omnipus/tests/officepreview/realprograms"
	pdfreader "github.com/ledongthuc/pdf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Founder Q1 B. Source strings and font names are the fixture oracle, taken
// from the decision's measured case (Lato, WenQuanYi Zen Hei, Latin + CJK),
// not from a converter run. Forced re-exec is the trace's deterministic
// inherited boot layer: the converter start thread already carries
// DefaultPolicyForModel before its own rules are stacked.
const (
	q1bLatin     = "Q1B Lato Latin"
	q1bHan       = "汉字样本"
	q1bKana      = "かな"
	q1bFontLatin = "Lato"
	q1bFontCJK   = "WenQuanYi Zen Hei"
)

func TestPreviewOffice_Q1B_ForcedConfinedBootMatchesOpen(t *testing.T) {
	if os.Getenv("OMNIPUS_TEST_Q1B_ROLE") == "convert" {
		q1bConvertChild(t)
		return
	}
	// §3.9 fixture move (sanctioned): the Office each child registers and
	// uses is a copy of the provisioned install inside the RUNNING
	// PROGRAM's own folder. The staging happens in q1bForceReexec, because
	// the registering process is the RE-EXEC'D binary copy under the home,
	// whose own folder the parent cannot know yet; see there.
	_, _ = realprograms.Roots(t, true, false)
	functionalPinDataOutsideTmp(t)
	q1bRequireMeasurementFonts(t)
	out := t.TempDir()
	openHome := filepath.Join(out, "open-home")
	confinedHome := filepath.Join(out, "confined-home")
	require.NoError(t, os.Mkdir(openHome, 0700))
	require.NoError(t, os.Mkdir(confinedHome, 0700))
	openFacts := q1bRunChild(t, "open", openHome)
	confinedFacts := q1bRunChild(t, "confined", confinedHome)
	q1bAssertOpenControl(t, openFacts)
	q1bAssertConfinedMatches(t, openFacts, confinedFacts)
}

func q1bRequireMeasurementFonts(t *testing.T) {
	t.Helper()
	out, err := exec.Command("fc-list", "-f", "%{file}\t%{family}\n").Output()
	require.NoError(t, err, "BLOCKED: fc-list is required to prove the image has the Q1 B fonts")
	lato, cjk := false, false
	for _, line := range strings.Split(string(out), "\n") {
		file, family, ok := strings.Cut(line, "\t")
		if !ok || !strings.HasPrefix(file, "/usr/share/fonts/") {
			continue
		}
		if strings.Contains(family, q1bFontLatin) {
			lato = true
		}
		if strings.Contains(family, q1bFontCJK) {
			cjk = true
		}
	}
	if !lato || !cjk {
		t.Fatalf("BLOCKED: Q1 B needs %s and %s under /usr/share/fonts (lato=%v cjk=%v). The pinned image office-red-functional-pinned:20261116 does not provide them. Packages: fonts-lato and fonts-wqy-zenhei (confined-trace/image/Dockerfile).", q1bFontLatin, q1bFontCJK, lato, cjk)
	}
}

func q1bRunChild(t *testing.T, model, home string) q1bFacts {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self, "-test.v", "-test.count=1", "-test.timeout=20m", "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "OMNIPUS_TEST_Q1B_") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env,
		"OMNIPUS_TEST_Q1B_ROLE=convert",
		"OMNIPUS_TEST_Q1B_MODEL="+model,
		"OMNIPUS_TEST_Q1B_HOME="+home,
	)
	output, runErr := cmd.CombinedOutput()
	t.Logf("Q1B %s child:\n%s", model, output)
	require.NoError(t, runErr, "Q1 B %s child failed before a quality comparison is meaningful", model)
	raw, err := os.ReadFile(filepath.Join(home, "q1b-facts.json"))
	require.NoError(t, err, "Q1 B %s child wrote no facts", model)
	var facts q1bFacts
	require.NoError(t, json.Unmarshal(raw, &facts))
	return facts
}

func q1bAssertOpenControl(t *testing.T, facts q1bFacts) {
	t.Helper()
	require.Empty(t, facts.Err, "open-mode control did not convert; this is not the missing-read failure: %s", facts.Err)
	require.Len(t, facts.Docs, 3, "open-mode control must convert DOCX, XLSX and PPTX")
	for _, doc := range facts.Docs {
		require.Empty(t, doc.Err, "open-mode %s failed: %s", doc.Ext, doc.Err)
		require.True(t, doc.PDF, "open-mode %s produced no PDF", doc.Ext)
		require.NotEmpty(t, doc.PDFText, "open-mode %s independent PDF text is empty; a failed parse must not pass", doc.Ext)
		for _, source := range []string{q1bLatin, q1bHan, q1bKana} {
			require.Contains(t, doc.GatewayText, source, "open-mode control %s lost source text %q; fixture or image is wrong", doc.Ext, source)
		}
		require.True(t, q1bFontsHave(doc.Fonts, q1bFontLatin), "open-mode control %s did not embed %s; fonts=%v", doc.Ext, q1bFontLatin, doc.Fonts)
		q1bAssertSockets(t, "open-mode control "+doc.Ext, facts.UID, doc.Sockets)
	}
}

func q1bAssertConfinedMatches(t *testing.T, openFacts, confined q1bFacts) {
	t.Helper()
	require.Empty(t, confined.Err, "confined conversion failed outright: %s", confined.Err)
	require.Len(t, confined.Docs, len(openFacts.Docs))
	for i, doc := range confined.Docs {
		open := openFacts.Docs[i]
		require.Equal(t, open.Ext, doc.Ext)
		require.Empty(t, doc.Err, "confined %s failed: %s", doc.Ext, doc.Err)
		assert.True(t, doc.PDF, "Q1 B: confined boot must still produce a PDF for %s", doc.Ext)
		q1bAssertSockets(t, "Q1 B confined "+doc.Ext, confined.UID, doc.Sockets)
		assert.Equal(t, open.GatewayText, doc.GatewayText, "Q1 B: confined extracted text must equal open mode for %s (missing /usr/share/fonts read garbles CJK)", doc.Ext)
		require.NotEmpty(t, open.PDFText, "Q1 B open %s independent PDF text is empty; two failed parses must not compare equal", open.Ext)
		require.NotEmpty(t, doc.PDFText, "Q1 B confined %s independent PDF text is empty; two failed parses must not compare equal", doc.Ext)
		assert.Equal(t, open.PDFText, doc.PDFText, "Q1 B: confined PDF text must equal open mode for %s", doc.Ext)
		assert.Equal(t, open.Fonts, doc.Fonts, "Q1 B: confined embedded fonts must equal open mode for %s (missing font reads substitute %s)", doc.Ext, q1bFontLatin)
		for _, source := range []string{q1bLatin, q1bHan, q1bKana} {
			assert.Contains(t, doc.GatewayText, source, "Q1 B: confined %s must contain source text %q", doc.Ext, source)
		}
	}
}

func q1bAssertSockets(t *testing.T, label string, uid int, sockets []q1bSocketFact) {
	t.Helper()
	prefix := "OSL_PIPE_" + strconv.Itoa(uid) + "_"
	assert.NotEmpty(t, sockets, "%s: no OSL_PIPE socket observed; absence is not proof", label)
	for _, socket := range sockets {
		assert.True(t, strings.HasPrefix(socket.Name, prefix), "%s: socket %s must carry uid %d", label, socket.Name, uid)
		assert.Equal(t, uint32(0755), socket.Mode, "%s: socket %s mode must be 0755, never 0777", label, socket.Name)
		assert.NotEqual(t, uint32(0777), socket.Mode, "%s: world-writable socket %s", label, socket.Name)
	}
}

func q1bFontsHave(fonts []string, name string) bool {
	for _, font := range fonts {
		if strings.Contains(font, name) {
			return true
		}
	}
	return false
}

type q1bFacts struct {
	Model string       `json:"model"`
	UID   int          `json:"uid"`
	Docs  []q1bDocFact `json:"docs"`
	Err   string       `json:"err,omitempty"`
}

type q1bDocFact struct {
	Ext         string          `json:"ext"`
	PDF         bool            `json:"pdf"`
	GatewayText string          `json:"gateway_text"`
	PDFText     string          `json:"pdf_text"`
	Fonts       []string        `json:"fonts"`
	Sockets     []q1bSocketFact `json:"sockets"`
	Err         string          `json:"err,omitempty"`
}

type q1bSocketFact struct {
	Name string `json:"name"`
	Mode uint32 `json:"mode"`
}

func q1bConvertChild(t *testing.T) {
	if os.Getenv("OMNIPUS_TEST_Q1B_FORCED") != "1" {
		q1bForceReexec(t)
		return
	}
	q1bConvertForced(t)
}

func q1bForceReexec(t *testing.T) {
	t.Helper()
	home := os.Getenv("OMNIPUS_TEST_Q1B_HOME")
	model := os.Getenv("OMNIPUS_TEST_Q1B_MODEL")
	require.NotEmpty(t, home)
	require.Contains(t, []string{"open", "confined"}, model)
	scratch := filepath.Join(home, "scratch")
	require.NoError(t, os.MkdirAll(scratch, 0700))
	require.NoError(t, os.Setenv("TMPDIR", scratch))
	// §3.9 fixture move (sanctioned): the GRANDCHILD registers and uses the
	// Office through its own default program path — the re-exec'd binary
	// copy under the home — so the office fixture is a copy of the
	// provisioned install inside THAT binary's own folder, and the boot
	// layer below grants exactly the path the grandchild registers and
	// uses. The old raw-install hand-down (CI's RUNNER_TEMP vs /opt) is
	// replaced by this staging: any provisioned source lands inside own
	// before the grandchild runs. The recorded wrapper returns exit 73
	// unless its receipt file is planted, and that wrapper is not the
	// decision's oracle.
	source := os.Getenv("OMNIPUS_TEST_OFFICE_ROOT")
	require.NotEmpty(t, source, "BLOCKED: OMNIPUS_TEST_OFFICE_ROOT is required by founder Q1 B")
	self, err := os.Executable()
	require.NoError(t, err)
	binary := filepath.Join(home, "q1b.test")
	q1bCopyFile(t, self, binary, 0755)
	ownGC := filepath.Clean(filepath.Join(filepath.Dir(binary), "..", "share", "omnipus"))
	require.NoError(t, os.MkdirAll(ownGC, 0o755))
	office := filepath.Join(ownGC, "q1b-office")
	// RemoveAll first: the open and confined children share this own
	// folder, and a cp -a over an existing destination nests the fixture.
	require.NoError(t, os.RemoveAll(office))
	functionalCopyTree(t, source, office)
	require.NoError(t, os.Setenv("OMNIPUS_TEST_Q1B_OFFICE", office))
	runtime.LockOSThread()
	backend, ok := sandbox.NewLinuxBackend()
	require.True(t, ok, "BLOCKED: enforcing Linux Landlock unavailable — required by founder Q1 B")
	// Execute the installed LibreOffice wherever the job put it. CI uses
	// RUNNER_TEMP, not only /opt. Without this grant the confined boot
	// cannot start soffice.
	policy := sandbox.DefaultPolicyForModel(q1bFilesystemModel(model), home, nil, []string{office}, nil, nil)
	require.NoError(t, backend.ApplyWithMode(policy, sandbox.ModeEnforce))
	// The re-exec'd process inherits the boot layer. This flag stops it
	// from applying that layer and copying the running binary again.
	require.NoError(t, os.Setenv("OMNIPUS_TEST_Q1B_FORCED", "1"))
	err = unix.Exec(binary, os.Args, os.Environ())
	t.Fatalf("Q1 B forced re-exec under %s boot: %v", model, err)
}

func q1bConvertForced(t *testing.T) {
	home := os.Getenv("OMNIPUS_TEST_Q1B_HOME")
	model := os.Getenv("OMNIPUS_TEST_Q1B_MODEL")
	facts := q1bFacts{Model: model, UID: os.Getuid()}
	defer func() {
		raw, err := json.Marshal(facts)
		if err != nil {
			return
		}
		_ = os.WriteFile(filepath.Join(home, "q1b-facts.json"), raw, 0600)
	}()
	backend, ok := sandbox.NewLinuxBackend()
	require.True(t, ok, "BLOCKED: enforcing Linux Landlock unavailable — required by founder Q1 B")
	// The child applies its own boot layer. Name the same office root
	// (CI's RUNNER_TEMP install included), not only /opt.
	office := os.Getenv("OMNIPUS_TEST_Q1B_OFFICE")
	policy := sandbox.DefaultPolicyForModel(q1bFilesystemModel(model), home, nil, []string{office}, nil, nil)
	require.NoError(t, backend.ApplyWithMode(policy, sandbox.ModeEnforce))
	require.True(t, backend.EnforcingChildren(), "Q1 B: boot layer must be enforcing")
	root := os.Getenv("OMNIPUS_TEST_Q1B_OFFICE")
	manifest, err := component.RegisterOffice(component.RegisterOptions{Home: home, Operator: "office-q1b", Folder: root})
	require.NoError(t, err)
	t.Logf("Q1 B forced model=%s version=%s", model, manifest.Version)
	self, err := os.Executable()
	require.NoError(t, err)
	svc, err := New(Config{Home: home, Self: self, Resolver: &component.Resolver{Home: home, Executable: self}})
	require.NoError(t, err)
	for _, doc := range q1bDocuments(t) {
		facts.Docs = append(facts.Docs, q1bOneCall(t, svc, root, doc))
	}
}

func q1bFilesystemModel(model string) sandbox.FilesystemModel {
	if model == "open" {
		return sandbox.FilesystemModelOpen
	}
	return sandbox.FilesystemModelConfined
}

func q1bOneCall(t *testing.T, svc *Service, root string, doc functionalDocument) q1bDocFact {
	t.Helper()
	fact := q1bDocFact{Ext: doc.Ext}
	finish := functionalObserve(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), CallTimeout)
	defer cancel()
	adm, err := svc.Admit(ctx, true)
	if err != nil {
		fact.Err = err.Error()
		_ = finish()
		return fact
	}
	defer adm.Release()
	result, previewErr := svc.Preview(ctx, adm, Request{Name: "q1b" + doc.Ext, Ext: doc.Ext, Data: doc.Data, Default: true})
	obs := finish()
	// An observer error means the socket facts were not collected. Logging
	// it and continuing can accept a run that never saw the ports.
	require.Empty(t, obs.Errors, "Q1 B %s: socket observation failed; fix the observer before accepting this run: %v", doc.Ext, obs.Errors)
	if previewErr != nil {
		fact.Err = previewErr.Error()
	}
	if result != nil && bytes.HasPrefix(result.PDF, []byte("%PDF-")) {
		fact.PDF = true
		fact.GatewayText = q1bJoinPages(result.Text)
		pdfText, pdfErr := q1bIndependentPDFText(result.PDF)
		if pdfErr != nil {
			msg := "independent PDF text: " + pdfErr.Error()
			if fact.Err != "" {
				fact.Err += "; " + msg
			} else {
				fact.Err = msg
			}
		}
		fact.PDFText = pdfText
		fact.Fonts = q1bPDFFonts(result.PDF)
	}
	fact.Sockets = q1bCollectSockets(obs)
	return fact
}

func q1bJoinPages(pages []protocol.TextPage) string {
	var text strings.Builder
	for _, page := range pages {
		text.WriteString(page.Text)
		text.WriteByte(' ')
	}
	return strings.Join(strings.Fields(text.String()), " ")
}

func q1bIndependentPDFText(data []byte) (string, error) {
	reader, err := pdfreader.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for page := 1; page <= reader.NumPage(); page++ {
		plain, readErr := reader.Page(page).GetPlainText(nil)
		if readErr != nil {
			return "", readErr
		}
		text.WriteString(plain)
		text.WriteByte(' ')
	}
	joined := strings.Join(strings.Fields(text.String()), " ")
	if joined == "" {
		return "", errors.New("independent PDF text is empty")
	}
	return joined, nil
}

func q1bCollectSockets(obs functionalObservation) []q1bSocketFact {
	var out []q1bSocketFact
	for _, socket := range obs.Sockets {
		name := filepath.Base(socket.Path)
		if !strings.HasPrefix(name, "OSL_PIPE_") {
			continue
		}
		out = append(out, q1bSocketFact{Name: name, Mode: uint32(socket.Mode.Perm())})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func q1bPDFFonts(data []byte) []string {
	seen := map[string]struct{}{}
	for _, match := range q1bBaseFont.FindAllSubmatch(data, -1) {
		name := string(match[1])
		if cut := strings.LastIndex(name, "+"); cut >= 0 {
			name = name[cut+1:]
		}
		seen[name] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

var q1bBaseFont = regexp.MustCompile(`/BaseFont\s*/([A-Za-z0-9+_.-]+)`)

func q1bCopyFile(t *testing.T, from, to string, mode os.FileMode) {
	t.Helper()
	in, err := os.Open(from)
	require.NoError(t, err)
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	require.NoError(t, err)
	_, copyErr := out.ReadFrom(in)
	closeErr := out.Close()
	require.NoError(t, copyErr)
	require.NoError(t, closeErr)
}

func q1bDocuments(t *testing.T) []functionalDocument {
	t.Helper()
	text := q1bLatin + " " + q1bHan + " " + q1bKana
	sources := []string{q1bLatin, q1bHan, q1bKana}
	return []functionalDocument{
		{ID: "Q1B-DOCX", Ext: ".docx", Text: sources, Pages: 1, Data: q1bWord(t, text)},
		{ID: "Q1B-XLSX", Ext: ".xlsx", Text: sources, Pages: 1, Data: q1bSheet(t, text)},
		{ID: "Q1B-PPTX", Ext: ".pptx", Text: sources, Pages: 1, Data: q1bSlide(t, text)},
	}
}

func q1bWord(t *testing.T, text string) []byte {
	t.Helper()
	body := `<w:p><w:r><w:rPr><w:rFonts w:ascii="` + q1bFontLatin + `" w:hAnsi="` + q1bFontLatin + `" w:cs="` + q1bFontLatin + `" w:eastAsia="` + q1bFontCJK + `"/></w:rPr><w:t>` + text + `</w:t></w:r></w:p>`
	require.Contains(t, body, q1bHan)
	return functionalZip(t, map[string]string{
		"[Content_Types].xml": functionalTypesStart + `<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         functionalRootRel("word/document.xml"),
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body + `<w:sectPr><w:pgSz w:w="12240" w:h="15840"/></w:sectPr></w:body></w:document>`,
	})
}

func q1bSheet(t *testing.T, text string) []byte {
	t.Helper()
	return functionalZip(t, map[string]string{
		"[Content_Types].xml":        functionalTypesStart + `<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/></Types>`,
		"_rels/.rels":                functionalRootRel("xl/workbook.xml"),
		"xl/workbook.xml":            `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Page1" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": functionalRelsStart + `<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`,
		"xl/styles.xml":              `<?xml version="1.0" encoding="UTF-8"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="2"><font><sz val="11"/><name val="Calibri"/></font><font><sz val="11"/><name val="` + q1bFontLatin + `"/></font></fonts><fills count="1"><fill><patternFill patternType="none"/></fill></fills><borders count="1"><border/></borders><cellStyleXfs count="1"><xf/></cellStyleXfs><cellXfs count="2"><xf fontId="0"/><xf fontId="1" applyFont="1"/></cellXfs></styleSheet>`,
		"xl/worksheets/sheet1.xml":   `<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" s="1" t="inlineStr"><is><t>` + text + `</t></is></c></row></sheetData></worksheet>`,
	})
}

func q1bSlide(t *testing.T, text string) []byte {
	t.Helper()
	slide := `<?xml version="1.0" encoding="UTF-8"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr><p:sp><p:nvSpPr><p:cNvPr id="2" name="Source"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="914400" y="914400"/><a:ext cx="10058400" cy="1828800"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" sz="2400"><a:latin typeface="` + q1bFontLatin + `"/><a:ea typeface="` + q1bFontCJK + `"/><a:cs typeface="` + q1bFontLatin + `"/></a:rPr><a:t>` + text + `</a:t></a:r></a:p></p:txBody></p:sp></p:spTree></p:cSld></p:sld>`
	return functionalZip(t, map[string]string{
		"[Content_Types].xml":             functionalTypesStart + `<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slides/slide1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/></Types>`,
		"_rels/.rels":                     functionalRootRel("ppt/presentation.xml"),
		"ppt/presentation.xml":            `<?xml version="1.0" encoding="UTF-8"?><p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldIdLst><p:sldId id="256" r:id="rId1"/></p:sldIdLst><p:sldSz cx="12192000" cy="6858000"/></p:presentation>`,
		"ppt/_rels/presentation.xml.rels": functionalRelsStart + `<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide1.xml"/></Relationships>`,
		"ppt/slides/slide1.xml":           slide,
	})
}
