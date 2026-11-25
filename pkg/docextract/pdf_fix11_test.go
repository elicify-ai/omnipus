package docextract

// #1968 FIX11 (fix round 6 through the public entry points ExtractBytes and
// InspectPDF). Probes that could panic or hang the test binary on the old
// code run as cases of one parent test that re-executes the test binary as a
// disposable child process, so an old-code panic shows as a test failure of
// that case and not as a dead test binary. Expected values come from the
// contracts of the findings (100,000-rune text cap, the truncation marker,
// the incomplete notice, page-local font names, the 1 KiB error message
// bound), derived by arithmetic on the fixtures, never read off the output.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	fix11ChildEnv = "PDF1968_FIX11_CASE"
	pptxMIME      = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	fix11Cap      = 100_000 // the extracted-text cap in runes
)

type fix11Entry struct{ name, body string }

func fix11Zip(t *testing.T, entries ...fix11Entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fix11SheetN(n int, rowsXML string) fix11Entry {
	return fix11Entry{
		fmt.Sprintf("xl/worksheets/sheet%d.xml", n),
		`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` + rowsXML + `</sheetData></worksheet>`,
	}
}

// fix11Inline is one inline-string cell.
func fix11Inline(text string) string {
	return `<c t="inlineStr"><is><t>` + text + `</t></is></c>`
}

func fix11SlideBody(paragraphs ...string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><p:cSld><p:spTree><p:sp><p:txBody>`)
	for _, p := range paragraphs {
		sb.WriteString(p)
	}
	sb.WriteString(`</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`)
	return sb.String()
}

func fix11Para(text string) string { return `<a:p><a:r><a:t>` + text + `</a:t></a:r></a:p>` }

func fix11SlideN(n int, body string) fix11Entry {
	return fix11Entry{fmt.Sprintf("ppt/slides/slide%d.xml", n), body}
}

// tail keeps the head (the failure's own message) and the tail of child
// output. It lives here, in an untagged file, because pdf_fix10_test.go and
// this file use it on every platform.
func tail(b []byte) string {
	if len(b) > 3000 {
		return string(b[:1500]) + "\n...\n" + string(b[len(b)-1500:])
	}
	return string(b)
}

func fix11Tail(s string) string { return s[max(0, len(s)-30):] }

type fix11Case struct {
	name     string
	deadline time.Duration
	run      func(t *testing.T)
}

var fix11Cases = []fix11Case{
	{"xlsx-100001-empty-rows-then-cell", 60 * time.Second, func(t *testing.T) {
		// 100,000 row breaks fill the cap exactly; the 100,001st break has no
		// room and the cell x after it finds no room.
		data := fix11Zip(t, fix11SheetN(1, strings.Repeat("<row/>", 100_001)+"<row>"+fix11Inline("x")+"</row>"))
		text, ok, reason := ExtractBytes(data, xlsxMIME, "x.xlsx")
		if !ok {
			t.Fatalf("readable sheet must extract, got reason %q", reason)
		}
		if !strings.HasSuffix(text, pdfFix6Marker) {
			t.Fatalf("a sheet over the cap must end with the truncation marker, tail %q", fix11Tail(text))
		}
		if strings.Contains(text, "x") {
			t.Fatalf("the cell after the cap must be absent, tail %q", fix11Tail(text))
		}
		// The kept body is only row breaks and never exceeds the cap; the
		// marker's own leading newline is not part of the body (T7-1).
		body := strings.TrimSuffix(text, fix12Marker)
		if strings.Trim(body, "\n") != "" || len([]rune(body)) > fix11Cap {
			t.Fatalf("kept body must be at most %d row breaks and nothing else, got %d runes, tail %q", fix11Cap, len([]rune(body)), fix11Tail(body))
		}
	}},
	{"xlsx-row-fills-cap-before-cell-b", 60 * time.Second, func(t *testing.T) {
		// 3 x 30,000 + 9,997 characters + 3 tabs = 100,000: the tab before B
		// has no room, so neither the tab nor B is written.
		row := "<row>" + fix11Inline(strings.Repeat("a", 30_000)) + fix11Inline(strings.Repeat("b", 30_000)) +
			fix11Inline(strings.Repeat("c", 30_000)) + fix11Inline(strings.Repeat("d", 9_997)) + fix11Inline("BEE") + "</row>"
		text, ok, reason := ExtractBytes(fix11Zip(t, fix11SheetN(1, row)), xlsxMIME, "x.xlsx")
		if !ok {
			t.Fatalf("readable sheet must extract, got reason %q", reason)
		}
		if strings.Contains(text, "BEE") {
			t.Fatalf("cell B starts past the cap and must be absent, tail %q", fix11Tail(text))
		}
		if !strings.HasSuffix(text, pdfFix6Marker) {
			t.Fatalf("the cut must be marked, tail %q", fix11Tail(text))
		}
		// Exactly the four cells that fit, joined by tabs, then the marker
		// (T7-1): 30,000 + 30,000 + 30,000 + 9,997 characters + 3 tabs = 100,000.
		want := strings.Repeat("a", 30_000) + "\t" + strings.Repeat("b", 30_000) + "\t" +
			strings.Repeat("c", 30_000) + "\t" + strings.Repeat("d", 9_997) + fix12Marker
		if text != want {
			t.Fatalf("want the first four cells and the marker (%d runes), got %d runes, tail %q", len([]rune(want)), len([]rune(text)), fix11Tail(text))
		}
	}},
	{"xlsx-row-near-miss-cell-b-fits", 60 * time.Second, func(t *testing.T) {
		// Near miss: 3 x 30,000 + 9,992 + 3 tabs = 99,995; a tab and "B" make
		// 99,997, the row break 99,998 and the sheet separator 99,999: under
		// the cap, so everything is kept and nothing is marked.
		row := "<row>" + fix11Inline(strings.Repeat("a", 30_000)) + fix11Inline(strings.Repeat("b", 30_000)) +
			fix11Inline(strings.Repeat("c", 30_000)) + fix11Inline(strings.Repeat("d", 9_992)) + fix11Inline("B") + "</row>"
		text, ok, reason := ExtractBytes(fix11Zip(t, fix11SheetN(1, row)), xlsxMIME, "x.xlsx")
		if !ok {
			t.Fatalf("readable sheet must extract, got reason %q", reason)
		}
		if !strings.HasSuffix(text, "d\tB") {
			t.Fatalf("cell B fits and must be kept after a tab, tail %q", fix11Tail(text))
		}
		if strings.Contains(text, "truncated") {
			t.Fatalf("nothing was cut, so there must be no marker, tail %q", fix11Tail(text))
		}
		if want := 99_997; len([]rune(text)) != want {
			t.Fatalf("kept %d runes, want %d", len([]rune(text)), want)
		}
	}},
	{"pptx-first-paragraph-fills-cap", 60 * time.Second, func(t *testing.T) {
		body := fix11SlideBody(fix11Para(strings.Repeat("a", fix11Cap)), fix11Para("QQQ"))
		text, ok, reason := ExtractBytes(fix11Zip(t, fix11SlideN(1, body)), pptxMIME, "x.pptx")
		if !ok {
			t.Fatalf("readable slide must extract, got reason %q", reason)
		}
		if strings.Contains(text, "QQQ") {
			t.Fatalf("paragraph B starts past the cap and must be absent, tail %q", fix11Tail(text))
		}
		// Exactly the first paragraph (the cap) and the marker (T7-1).
		if want := strings.Repeat("a", fix11Cap) + fix12Marker; text != want {
			t.Fatalf("want %d 'a' and the marker, got %d runes, tail %q", fix11Cap, len([]rune(text)), fix11Tail(text))
		}
	}},
	{"pptx-empty-paragraphs-after-99999", 60 * time.Second, func(t *testing.T) {
		// 99,999 characters leave room for one break; the 10 empty paragraphs
		// after that must not push the count past the cap.
		body := fix11SlideBody(fix11Para(strings.Repeat("a", fix11Cap-1)), strings.Repeat("<a:p/>", 10), fix11Para("QQQ"))
		text, ok, reason := ExtractBytes(fix11Zip(t, fix11SlideN(1, body)), pptxMIME, "x.pptx")
		if !ok {
			t.Fatalf("readable slide must extract, got reason %q", reason)
		}
		if strings.Contains(text, "QQQ") {
			t.Fatalf("paragraph B starts past the cap and must be absent, tail %q", fix11Tail(text))
		}
		// The 99,999 characters are kept whole; only paragraph breaks may follow
		// them, within the cap, and the cut is marked (T7-1).
		kept := strings.TrimSuffix(text, fix12Marker)
		if kept == text {
			t.Fatalf("the cut must be marked, tail %q", fix11Tail(text))
		}
		if !strings.HasPrefix(kept, strings.Repeat("a", fix11Cap-1)) ||
			strings.Trim(kept[fix11Cap-1:], "\n") != "" || len([]rune(kept)) > fix11Cap {
			t.Fatalf("want 99,999 'a' then at most one break, got %d runes, tail %q", len([]rune(kept)), fix11Tail(kept))
		}
	}},
}

func TestPDFFix11_OfficeLimits(t *testing.T) {
	if name := os.Getenv(fix11ChildEnv); name != "" {
		for _, c := range fix11Cases {
			if c.name == name {
				t.Log("PDF_FIX11_STARTED", name)
				c.run(t)
				return
			}
		}
		t.Fatalf("unknown case %q", name)
	}
	for _, c := range fix11Cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), c.deadline)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPDFFix11_OfficeLimits$", "-test.v")
			cmd.Env = append(os.Environ(), fix11ChildEnv+"="+c.name, "GOTRACEBACK=none")
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("%s did not finish within %v (child killed)", c.name, c.deadline)
			}
			if !bytes.Contains(out, []byte("PDF_FIX11_STARTED")) {
				t.Fatalf("the child never reached the case: %v\n%s", err, tail(out))
			}
			if err != nil {
				t.Fatalf("%s failed: %v\n%s", c.name, err, tail(out))
			}
		})
	}
}

// A small, normal workbook is unchanged by the separator accounting.
func TestExtractBytes_Fix11_SmallXlsxUnchanged(t *testing.T) {
	data := fix11Zip(t, fix11SheetN(1, "<row>"+fix11Inline("a")+fix11Inline("b")+"</row><row>"+fix11Inline("c")+"</row>"))
	text, ok, reason := ExtractBytes(data, xlsxMIME, "x.xlsx")
	if !ok || text != "a\tb\nc" {
		t.Fatalf("want exactly %q, got ok=%v text=%q reason=%q", "a\tb\nc", ok, text, reason)
	}
}

// A sheet or slide that cannot be read is named in the result, not dropped.
func TestExtractBytes_Fix11_UnreadableEntryIsNamed(t *testing.T) {
	const notice = "\n[incomplete: 1 %s(s) could not be read and are missing; first: %s: "
	cases := []struct {
		name, mime, file, kind, entry string
		data                          []byte
	}{
		{"xlsx", xlsxMIME, "x.xlsx", "sheet", "xl/worksheets/sheet2.xml", fix11Zip(t,
			fix11SheetN(1, "<row>"+fix11Inline("good")+"</row>"),
			fix11Entry{"xl/worksheets/sheet2.xml", `<?xml version="1.0"?><worksheet><sheetData><row><c t="inlineStr"><is><t>bad`})},
		{"pptx", pptxMIME, "x.pptx", "slide", "ppt/slides/slide2.xml", fix11Zip(t,
			fix11SlideN(1, fix11SlideBody(fix11Para("good"))),
			fix11Entry{"ppt/slides/slide2.xml", `<?xml version="1.0"?><p:sld><a:p><a:r><a:t>bad`})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, ok, reason := ExtractBytes(c.data, c.mime, c.file)
			if !ok || !strings.Contains(text, "good") {
				t.Fatalf("the readable entry must survive, got ok=%v text=%q reason=%q", ok, text, reason)
			}
			want := fmt.Sprintf(notice, c.kind, c.entry)
			if i := strings.Index(text, want); i < 0 || !strings.HasSuffix(text, "]") || strings.Contains(text[i+1:], "\n[incomplete:") {
				t.Fatalf("want one notice starting %q at the end, got %q", want, text)
			}
		})
	}
}

// With no readable entry the reason names the failure, not "no text content".
func TestExtractBytes_Fix11_AllEntriesUnreadableNamesFailure(t *testing.T) {
	cases := []struct {
		name, mime, file, prefix, entry string
		data                            []byte
	}{
		{"xlsx", xlsxMIME, "x.xlsx", "xlsx: no sheet could be read", "xl/worksheets/sheet1.xml", fix11Zip(t,
			fix11Entry{"xl/worksheets/sheet1.xml", `<?xml version="1.0"?><worksheet><sheetData><row><c t="inlineStr"><is><t>bad`})},
		{"pptx", pptxMIME, "x.pptx", "pptx: no slide could be read", "ppt/slides/slide1.xml", fix11Zip(t,
			fix11Entry{"ppt/slides/slide1.xml", `<?xml version="1.0"?><p:sld><a:p><a:r><a:t>bad`})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, ok, reason := ExtractBytes(c.data, c.mime, c.file)
			if ok || text != "" {
				t.Fatalf("nothing is readable, want ok=false and no text, got ok=%v text=%q", ok, text)
			}
			if !strings.HasPrefix(reason, c.prefix) || !strings.Contains(reason, c.entry) || strings.Contains(reason, "no text content found") {
				t.Fatalf("reason must start %q, name %q and not say \"no text content found\", got %q", c.prefix, c.entry, reason)
			}
		})
	}
}

// An entry the 25 MiB decompression budget cut off keeps the budget's own
// messages and is not also reported as an unreadable entry.
func TestExtractOffice_Fix11_BudgetCutIsNotReportedAsUnreadable(t *testing.T) {
	const budgetReasonX = "xlsx: archive exceeds 25 MiB decompression budget before any sheet text could be read"
	const budgetReasonP = "pptx: archive exceeds 25 MiB decompression budget before any slide text could be read"
	const budgetNotice = "[truncated: archive exceeds 25 MiB decompression budget]"
	sheet1, sheet2 := xlsxSheetXML("SheetOneRecognizableCell"), xlsxSheetXML("SheetTwoCellThatMustNeverAppear")
	slide1, slide2 := pptxSlideXML("Slide one recognizable text."), pptxSlideXML("Slide two text that must never appear.")
	notIncomplete := func(t *testing.T, s string) {
		t.Helper()
		if strings.Contains(s, "incomplete") || strings.Contains(s, "could not be read") {
			t.Fatalf("a budget cut must not be reported as an unreadable entry, got %q", s)
		}
	}
	t.Run("xlsx cut inside sheet 2", func(t *testing.T) {
		budget := &archiveReadBudget{remaining: int64(len(sheet1) + len(sheet2)/2)}
		text, ok, reason := extractXlsx(buildXlsxZipReader(t, sheet1, sheet2), budget)
		if !ok || !strings.Contains(text, "SheetOneRecognizableCell") || strings.Contains(text, "MustNeverAppear") || !strings.Contains(text, budgetNotice) {
			t.Fatalf("want sheet 1 and the budget notice, got ok=%v text=%q reason=%q", ok, text, reason)
		}
		notIncomplete(t, text)
	})
	t.Run("pptx cut inside slide 2", func(t *testing.T) {
		budget := &archiveReadBudget{remaining: int64(len(slide1) + len(slide2)/2)}
		text, ok, reason := extractPptx(buildPptxZipReader(t, slide1, slide2), budget)
		if !ok || !strings.Contains(text, "Slide one recognizable text.") || strings.Contains(text, "must never appear") || !strings.Contains(text, budgetNotice) {
			t.Fatalf("want slide 1 and the budget notice, got ok=%v text=%q reason=%q", ok, text, reason)
		}
		notIncomplete(t, text)
	})
	t.Run("xlsx budget before any text", func(t *testing.T) {
		_, ok, reason := extractXlsx(buildXlsxZipReader(t, sheet1), &archiveReadBudget{remaining: 1})
		if ok || reason != budgetReasonX {
			t.Fatalf("want exactly %q, got ok=%v %q", budgetReasonX, ok, reason)
		}
	})
	t.Run("pptx budget before any text", func(t *testing.T) {
		_, ok, reason := extractPptx(buildPptxZipReader(t, slide1), &archiveReadBudget{remaining: 1})
		if ok || reason != budgetReasonP {
			t.Fatalf("want exactly %q, got ok=%v %q", budgetReasonP, ok, reason)
		}
	})
}

// fix11TwoPagePDF builds two pages that both show (A) in /F1. Object 3 maps
// A to B and object 4 maps A to C; page 1 reaches object 3 as /F1 and page 2
// reaches page2Font (3 or 4).
func fix11TwoPagePDF(page2Font int) []byte {
	font := func(name string) string {
		return "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding << /Differences [65 /" + name + "] >> >>"
	}
	show := pdfFix6Stream("", []byte("BT /F1 12 Tf (A) Tj ET\n"))
	return pdfFix6Assemble([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 2 /Kids [5 0 R 7 0 R] >>",
		font("B"),
		font("C"),
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents 6 0 R >>",
		show,
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 %d 0 R >> >> /Contents 8 0 R >>", page2Font),
		show,
	})
}

// Font resource names are local to a page: the same name on two pages is two
// fonts when they are two objects.
func TestPDFFix11_FontNameIsScopedToItsPage(t *testing.T) {
	data := fix11TwoPagePDF(4)
	info, err := InspectPDF(data)
	if err != nil || info.Text != "B\nC" {
		t.Fatalf("InspectPDF: want %q (page 1 /F1 maps A to B, page 2 /F1 maps A to C), got err=%v text=%q", "B\nC", err, info.Text)
	}
	text, ok, reason := ExtractBytes(data, "application/pdf", "x.pdf")
	if !ok || strings.TrimSpace(text) != "BC" {
		t.Fatalf("ExtractBytes: want %q, got ok=%v text=%q reason=%q", "BC", ok, text, reason)
	}
}

// Control: one shared font object keeps its decoding on both pages.
func TestPDFFix11_SharedFontObjectDecodesOnBothPages(t *testing.T) {
	data := fix11TwoPagePDF(3)
	info, err := InspectPDF(data)
	if err != nil || info.Text != "B\nB" {
		t.Fatalf("InspectPDF: want %q, got err=%v text=%q", "B\nB", err, info.Text)
	}
	text, ok, reason := ExtractBytes(data, "application/pdf", "x.pdf")
	if !ok || strings.TrimSpace(text) != "BB" {
		t.Fatalf("ExtractBytes: want %q, got ok=%v text=%q reason=%q", "BB", ok, text, reason)
	}
}

// An object the error message cannot show whole ends in the ellipsis, and
// the message stays near 1 KiB plus its fixed prefix.
func TestPDFFix11_ErrorMessageCutIsMarked(t *testing.T) {
	var utf16 strings.Builder
	utf16.WriteString("<FEFF")
	for range 1024 {
		utf16.WriteString("0041")
	}
	utf16.WriteString(">")
	var arr, dict strings.Builder
	arr.WriteString("[")
	dict.WriteString("<<")
	for i := range 40 {
		fmt.Fprintf(&arr, "(%s) ", strings.Repeat("B", 100))
		fmt.Fprintf(&dict, "/K%02d (%s) ", i, strings.Repeat("C", 100))
	}
	arr.WriteString("]")
	dict.WriteString(">>")
	cases := []struct{ name, obj string }{
		{"utf-16 string, 1,024 code units", utf16.String()},
		{"long ascii string", "(" + strings.Repeat("A", 3000) + ")"},
		{"array whose later elements are omitted", arr.String()},
		{"dict whose later entries are omitted", dict.String()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var buf bytes.Buffer
			buf.WriteString("%PDF-1.4\n")
			off := buf.Len()
			fmt.Fprintf(&buf, "1 0 obj\n%s\nendobj\nstartxref\n%d\n%%%%EOF\n", c.obj, off)
			data := buf.Bytes()
			_, ok, reason := ExtractBytes(data, "application/pdf", "x.pdf")
			if ok || !strings.Contains(reason, "cross-reference table not found") {
				t.Fatalf("fixture must fail at the xref with its message, got ok=%v reason=%.200q", ok, reason)
			}
			if !strings.Contains(reason, "…") {
				t.Fatalf("the message shows only part of the object and must say so with an ellipsis, got %q", reason)
			}
			if len(reason) > 1300 {
				t.Fatalf("the message carries %d bytes, want near 1 KiB plus the prefix", len(reason))
			}
			_, err := InspectPDF(data)
			if !errors.Is(err, ErrPDFMalformed) || !strings.Contains(err.Error(), "…") || len(err.Error()) > 1300 {
				t.Fatalf("InspectPDF must fail typed with a marked message of about 1 KiB at most, got %v", err)
			}
		})
	}
}

// ---- FIX12 (fix round 7): raw-text cuts and exact-fit office text ----------
//
// Contracts (spec docs/specs/read-file-native-pdf.md): the text cap is
// 100,000 runes; a raw text file is read up to 400,001 bytes; whatever is cut
// is marked with "\n…[truncated]"; text that exactly fits is never marked;
// invalid UTF-8 is refused. Expected values are derived by arithmetic on the
// fixtures.

const fix12Marker = "\n" + pdfFix6Marker

func fix12Text(t *testing.T, data []byte, mime, name string) string {
	t.Helper()
	text, ok, reason := ExtractBytes(data, mime, name)
	if !ok {
		t.Fatalf("%s must extract, got reason %q", name, reason)
	}
	return text
}

// "A" + 400,000 tabs + "B" is 400,002 bytes: the byte limit cuts the file
// inside the tabs; the tabs are control characters and vanish, so only "A"
// is left, and the cut must be marked.
func TestExtractBytes_Fix12_TextByteCutOverControlCharsIsMarked(t *testing.T) {
	data := []byte("A" + strings.Repeat("\t", 400_000) + "B")
	text := fix12Text(t, data, "text/plain", "x.txt")
	if text != "A"+fix12Marker {
		t.Fatalf("want exactly %q, got %d bytes, tail %q", "A"+fix12Marker, len(text), fix11Tail(text))
	}
}

// 100,001 four-byte characters is 400,004 bytes: the byte cut at 400,001
// moves back to the boundary after 100,000 characters, so the cut must be
// marked and the text must not be judged invalid.
func TestExtractBytes_Fix12_TextMultibyteOverCapIsMarked(t *testing.T) {
	data := []byte(strings.Repeat("\U0001F600", fix11Cap+1))
	if len(data) != 400_004 {
		t.Fatalf("fixture is %d bytes, want 400004", len(data))
	}
	text, ok, reason := ExtractBytes(data, "text/plain", "x.txt")
	if !ok {
		t.Fatalf("valid text must extract, got reason %q", reason)
	}
	if want := strings.Repeat("\U0001F600", fix11Cap) + fix12Marker; text != want {
		t.Fatalf("want %d emoji and the marker, got %d bytes, tail %q", fix11Cap, len(text), fix11Tail(text))
	}
}

// Control: exactly 100,000 four-byte characters (400,000 bytes) fits; it is
// returned unchanged and unmarked.
func TestExtractBytes_Fix12_TextMultibyteExactFitIsUnmarked(t *testing.T) {
	want := strings.Repeat("\U0001F600", fix11Cap)
	if len(want) != 400_000 {
		t.Fatalf("fixture is %d bytes, want 400000", len(want))
	}
	text := fix12Text(t, []byte(want), "text/plain", "x.txt")
	if text != want {
		t.Fatalf("exact fit must be returned unchanged, got %d bytes, tail %q", len(text), fix11Tail(text))
	}
}

// Invalid input is still refused, small or large.
func TestExtractBytes_Fix12_InvalidUTF8StillRefused(t *testing.T) {
	big := append([]byte(strings.Repeat("x", 100)), 0xFF)
	big = append(big, bytes.Repeat([]byte("y"), 400_100)...) // over 400,002 bytes, 0xFF inside the first 400,001
	cases := []struct {
		name string
		data []byte
	}{
		{"small", append([]byte(strings.Repeat("x", 10)), 0xFF)},
		{"large-bad-byte-before-the-cut", big},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text, ok, reason := ExtractBytes(c.data, "text/plain", "x.txt")
			if ok || reason != "file is not valid UTF-8" {
				t.Fatalf("want refusal %q, got ok=%v reason=%q text tail %q", "file is not valid UTF-8", ok, reason, fix11Tail(text))
			}
		})
	}
}

// One slide with one paragraph of exactly the cap (or one less) fits: nothing
// was cut, so nothing is marked.
func TestExtractBytes_Fix12_PptxExactFitIsUnmarked(t *testing.T) {
	for _, n := range []int{fix11Cap, fix11Cap - 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			data := fix11Zip(t, fix11SlideN(1, fix11SlideBody(fix11Para(strings.Repeat("A", n)))))
			text := fix12Text(t, data, pptxMIME, "x.pptx")
			if text != strings.Repeat("A", n) {
				t.Fatalf("want exactly %d A and no marker, got %d bytes, tail %q", n, len(text), fix11Tail(text))
			}
		})
	}
}

// After an exact fit, more content is a cut (marked); an empty next slide is
// not content (unmarked).
func TestExtractBytes_Fix12_PptxExactFitThenMore(t *testing.T) {
	full := strings.Repeat("A", fix11Cap)
	t.Run("more-content-is-marked", func(t *testing.T) {
		data := fix11Zip(t, fix11SlideN(1, fix11SlideBody(fix11Para(full))), fix11SlideN(2, fix11SlideBody(fix11Para("Z"))))
		text := fix12Text(t, data, pptxMIME, "x.pptx")
		if text != full+fix12Marker {
			t.Fatalf("want %d A then the marker, got %d bytes, tail %q", fix11Cap, len(text), fix11Tail(text))
		}
	})
	t.Run("empty-next-slide-is-unmarked", func(t *testing.T) {
		data := fix11Zip(t, fix11SlideN(1, fix11SlideBody(fix11Para(full))), fix11SlideN(2, fix11SlideBody()))
		text := fix12Text(t, data, pptxMIME, "x.pptx")
		if text != full {
			t.Fatalf("want exactly %d A and no marker, got %d bytes, tail %q", fix11Cap, len(text), fix11Tail(text))
		}
	})
}

// One sheet whose single cell is exactly the cap fits; a second sheet with
// content is a cut.
func TestExtractBytes_Fix12_XlsxExactFit(t *testing.T) {
	full := strings.Repeat("A", fix11Cap)
	sheet1 := fix11SheetN(1, "<row>"+fix11Inline(full)+"</row>")
	t.Run("one-sheet-unmarked", func(t *testing.T) {
		text := fix12Text(t, fix11Zip(t, sheet1), xlsxMIME, "x.xlsx")
		if text != full {
			t.Fatalf("want exactly %d A and no marker, got %d bytes, tail %q", fix11Cap, len(text), fix11Tail(text))
		}
	})
	t.Run("second-sheet-is-marked", func(t *testing.T) {
		data := fix11Zip(t, sheet1, fix11SheetN(2, "<row>"+fix11Inline("Z")+"</row>"))
		text := fix12Text(t, data, xlsxMIME, "x.xlsx")
		if text != full+fix12Marker {
			t.Fatalf("want %d A then the marker, got %d bytes, tail %q", fix11Cap, len(text), fix11Tail(text))
		}
	})
}

// The gzip text path has the same contract as plain text: a decoded body over
// the byte limit is cut and marked.
func TestExtractBytes_Fix12_GzipTextByteCutIsMarked(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write([]byte("A" + strings.Repeat("\t", 400_000) + "B")); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	text := fix12Text(t, buf.Bytes(), "application/gzip", "x.txt.gz")
	if text != "A"+fix12Marker {
		t.Fatalf("want exactly %q, got %d bytes, tail %q", "A"+fix12Marker, len(text), fix11Tail(text))
	}
}

// ---- FIX13 (fix round 8): the " operator, gzip failures, truncated tar.gz ---
//
// Contracts: PDF 1.7 section 9.4.3 (the " operator is aw ac string " and means
// aw Tw ac Tc string '; ' is T* string Tj; both start a new line);
// docs/troubleshooting.md "A document's text is incomplete or marked
// truncated" (a failed gzip read ends in "gzip stream exceeds 25 MiB cap"; a
// compressed tar keeps the entries found in the first 25 MiB). Cases run in a
// child process (fix13Env) so an old-code panic or hang is one failed case.

const (
	fix13Env = "PDF1968_FIX13_CASE"
	fix13Cap = "\n[truncated: gzip stream exceeds 25 MiB cap]"
)

func fix13Page(content string) []byte { return pdfFix10Page(pdfFix10PlainFont, content) }

// fix13Extracts: both entry points return want (outer whitespace ignored).
func fix13Extracts(t *testing.T, content, wantInspect, wantGeneric string) {
	t.Helper()
	data := fix13Page(content)
	text, ok, reason := ExtractBytes(data, "application/pdf", "x.pdf")
	if !ok || strings.TrimSpace(text) != wantGeneric {
		t.Fatalf("ExtractBytes(%q): want %q, got ok=%v text=%q reason=%.120q", content, wantGeneric, ok, text, reason)
	}
	info, err := InspectPDF(data)
	if err != nil || strings.TrimSpace(info.Text) != wantInspect {
		t.Fatalf("InspectPDF(%q): want %q, got err=%v text=%q", content, wantInspect, err, info.Text)
	}
}

func fix13Refused(t *testing.T, content string) {
	t.Helper()
	data := fix13Page(content)
	text, ok, reason := ExtractBytes(data, "application/pdf", "x.pdf")
	if ok || text != "" || !strings.Contains(reason, `bad " operator`) {
		t.Fatalf("ExtractBytes(%q): want a refusal naming bad \" operator, got ok=%v text=%q reason=%.160q", content, ok, text, reason)
	}
	if _, err := InspectPDF(data); !errors.Is(err, ErrPDFMalformed) || !strings.Contains(err.Error(), `bad " operator`) {
		t.Fatalf("InspectPDF(%q): want ErrPDFMalformed naming bad \" operator, got %v", content, err)
	}
}

// fix13Gzip streams prefix and then n bytes of fill into a gzip writer.
func fix13Gzip(t *testing.T, write func(w io.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	write(gw)
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fix13Fill(t *testing.T, w io.Writer, b byte, n int) {
	t.Helper()
	chunk := bytes.Repeat([]byte{b}, 1<<20)
	for n > 0 {
		k := min(n, len(chunk))
		if _, err := w.Write(chunk[:k]); err != nil {
			t.Fatal(err)
		}
		n -= k
	}
}

func fix13TarGz(t *testing.T, hugeSize int64) []byte {
	t.Helper()
	return fix13Gzip(t, func(w io.Writer) {
		tw := tar.NewWriter(w)
		put := func(name string, size int64, fill byte) {
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size}); err != nil {
				t.Fatal(err)
			}
			fix13Fill(t, tw, fill, int(size))
		}
		put("a.txt", 10, 'a')
		put("b.txt", 20, 'b')
		put("huge.bin", hugeSize, 0)
		put("after.txt", 5, 'c')
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

var fix13Cases = []fix11Case{
	{"dq-operator-integers", 20 * time.Second, func(t *testing.T) {
		fix13Extracts(t, `BT /F1 12 Tf 0 0 (Hello) " ET`, "Hello", "Hello")
	}},
	{"dq-operator-reals", 20 * time.Second, func(t *testing.T) {
		fix13Extracts(t, `BT /F1 12 Tf 1.5 0.25 (Hello) " ET`, "Hello", "Hello")
	}},
	{"dq-operator-starts-new-lines", 20 * time.Second, func(t *testing.T) {
		fix13Extracts(t, `BT /F1 12 Tf (One) Tj 0 0 (Two) " 1 2 (Three) " ET`, "One\nTwo\nThree", "OneTwoThree")
	}},
	{"quote-operator-starts-new-lines", 20 * time.Second, func(t *testing.T) {
		fix13Extracts(t, `BT /F1 12 Tf (One) Tj (Two) ' (Three) ' ET`, "One\nTwo\nThree", "OneTwoThree")
	}},
	{"dq-operator-two-operands-refused", 20 * time.Second, func(t *testing.T) {
		fix13Refused(t, `BT /F1 12 Tf 0 (Hello) " ET`)
	}},
	{"dq-operator-name-operand-refused", 20 * time.Second, func(t *testing.T) {
		fix13Refused(t, `BT /F1 12 Tf /X 0 (Hello) " ET`)
	}},
	{"control-tj-and-single-quote", 20 * time.Second, func(t *testing.T) {
		fix13Extracts(t, `BT /F1 12 Tf (Hello) Tj ET`, "Hello", "Hello")
		fix13Extracts(t, `BT /F1 12 Tf (Hello) ' ET`, "Hello", "Hello")
	}},
	{"gzip-invalid-utf8-over-cap-fails-with-cap-reason", 90 * time.Second, func(t *testing.T) {
		data := fix13Gzip(t, func(w io.Writer) {
			if _, err := w.Write([]byte{0xFF}); err != nil {
				t.Fatal(err)
			}
			fix13Fill(t, w, 'a', 26<<20)
		})
		text, ok, reason := ExtractBytes(data, "application/gzip", "x.txt.gz")
		if ok || text != "" {
			t.Fatalf("want a failure with no text, got ok=%v text=%.40q", ok, text)
		}
		for _, part := range []string{"file is not valid UTF-8", "gzip stream exceeds 25 MiB cap"} {
			if !strings.Contains(reason, part) {
				t.Fatalf("reason %q must contain %q", reason, part)
			}
		}
	}},
	{"targz-cut-lists-entries-read-and-says-so", 90 * time.Second, func(t *testing.T) {
		text, ok, reason := ExtractBytes(fix13TarGz(t, 30<<20), "application/gzip", "x.tar.gz")
		if !ok {
			t.Fatalf("a cut tar.gz keeps the entries read, got reason %q", reason)
		}
		for _, part := range []string{"a.txt (10 bytes)", "b.txt (20 bytes)", "[truncated: showing first 2 of 3 entries]"} {
			if !strings.Contains(text, part) {
				t.Fatalf("listing must contain %q, got %q", part, text)
			}
		}
		if !strings.HasSuffix(text, "[truncated: gzip stream exceeds 25 MiB cap]") {
			t.Fatalf("listing must end with the cap marker, tail %q", fix11Tail(text))
		}
	}},
	{"control-tar-cut-still-fails-unexpected-eof", 20 * time.Second, func(t *testing.T) {
		// 512 header + 512 (10 bytes padded) + 512 header = 1,536 bytes, then
		// 5,000 bytes of data cut at 2,048: an uncompressed tar is not a cut
		// stream, so the damage stays a failure.
		data := makeTestTAR(t, archiveTestEntry{"a.txt", strings.Repeat("a", 10)}, archiveTestEntry{"b.bin", strings.Repeat("b", 5000)})[:2048]
		_, ok, reason := ExtractBytes(data, "", "x.tar")
		if ok || !strings.Contains(reason, "tar manifest failed: unexpected EOF") {
			t.Fatalf("want tar manifest failed: unexpected EOF, got ok=%v reason=%q", ok, reason)
		}
	}},
	{"control-small-targz-unchanged", 20 * time.Second, func(t *testing.T) {
		tarData := makeTestTAR(t, archiveTestEntry{"a.txt", strings.Repeat("a", 10)}, archiveTestEntry{"b.txt", strings.Repeat("b", 20)})
		text, ok, reason := ExtractBytes(gzipTestBytes(t, tarData), "application/gzip", "x.tar.gz")
		if !ok || !strings.Contains(text, "a.txt (10 bytes)") || !strings.Contains(text, "b.txt (20 bytes)") || strings.Contains(text, "truncated") {
			t.Fatalf("small tar.gz must list both entries unmarked, got ok=%v text=%q reason=%q", ok, text, reason)
		}
	}},
	{"control-gzip-text-over-cap-is-cut-and-marked", 90 * time.Second, func(t *testing.T) {
		data := fix13Gzip(t, func(w io.Writer) { fix13Fill(t, w, 'a', 26<<20) })
		text, ok, reason := ExtractBytes(data, "application/gzip", "x.txt.gz")
		if !ok {
			t.Fatalf("valid text must extract, got reason %q", reason)
		}
		if want := strings.Repeat("a", fix11Cap) + fix12Marker + fix13Cap; text != want {
			t.Fatalf("want %d 'a', the text marker and the cap marker, got %d bytes, tail %q", fix11Cap, len(text), fix11Tail(text))
		}
	}},
}

func TestFix13_Cases(t *testing.T) {
	if name := os.Getenv(fix13Env); name != "" {
		for _, c := range fix13Cases {
			if c.name == name {
				t.Log("PDF_FIX13_STARTED", name)
				c.run(t)
				return
			}
		}
		t.Fatalf("unknown case %q", name)
	}
	for _, c := range fix13Cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), c.deadline)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFix13_Cases$", "-test.v")
			cmd.Env = append(os.Environ(), fix13Env+"="+c.name, "GOTRACEBACK=none")
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("%s did not finish within %v (child killed)", c.name, c.deadline)
			}
			if !bytes.Contains(out, []byte("PDF_FIX13_STARTED")) {
				t.Fatalf("the child never reached the case: %v\n%s", err, tail(out))
			}
			if err != nil {
				t.Fatalf("%s failed: %v\n%s", c.name, err, tail(out))
			}
		})
	}
}
