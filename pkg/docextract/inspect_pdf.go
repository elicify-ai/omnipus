package docextract

// inspect_pdf.go — structural PDF inspection for read_file's PDF path
// (issue #1968, spec FR-6). InspectPDF reports the page count and the
// extractable text, and classifies every failure with a typed error so no
// caller ever matches a reason string.

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/elicify-ai/omnipus/pkg/docextract/internal/pdf"
)

// PDFInfo is what InspectPDF learns from a readable PDF.
type PDFInfo struct {
	// Pages is the page count the page tree states (/Count); 0 = unknown.
	Pages int
	// Text is the extractable text, pages separated by a newline; empty
	// when the PDF has no text layer (for example a scanned document).
	Text string
}

// Typed InspectPDF failures (spec FR-6). InspectPDF can also fail with
// ErrPDFBusy or ErrPDFTimedOut from the shared parse boundary
// (pdf_admission.go); those describe the process's load, not the file.
var (
	// ErrPDFEncrypted: the document is password or owner protected.
	ErrPDFEncrypted = errors.New("pdf is encrypted")
	// ErrPDFUnsupportedVersion: the %PDF- header names a version this
	// build does not read (anything outside 1.0-1.7).
	ErrPDFUnsupportedVersion = errors.New("pdf version is not supported")
	// ErrPDFMalformed: anything structurally broken.
	ErrPDFMalformed = errors.New("pdf is malformed")
)

// pdfMagic is the header every PDF starts with.
const pdfMagic = "%PDF-"

// maxPageTreeDepth bounds the page-tree walk so a hostile, deeply nested
// or cyclic tree ends as ErrPDFMalformed instead of exhausting the stack.
// It is a parser safety bound, not a provider limit.
const maxPageTreeDepth = 256

// Parser safety budgets (security finding F1). They bound the work and
// memory one untrusted PDF can cost this process, whatever its structure;
// they are properties of the parser, never provider document limits, which
// come from catalogue data only.
const (
	// pdfWorkBudget bounds the parsing work of one PDF, in roughly
	// byte-sized units (bytes decoded, bytes lexed, objects resolved,
	// glyph-table lookups). Measured legit documents use a small fraction;
	// a hostile one stops here instead of looping or decompressing forever.
	pdfWorkBudget = 1 << 29
	// pdfTextBudget bounds InspectPDF's extracted text: a text layer larger
	// than the largest PDF the reader accepts is kept up to this size and
	// marked truncated.
	pdfTextBudget = MaxDocBytes
	// pdfTruncatedMarker ends text the parser budgets cut short.
	pdfTruncatedMarker = "\n…[truncated]"
)

// errPDFWalkStop ends the page walk early after a budget ran out.
var errPDFWalkStop = errors.New("pdf page walk stopped at a parser budget")

// isPDFBudgetStop reports whether err is a parser budget running out (as
// opposed to a malformed structure).
func isPDFBudgetStop(err error) bool {
	return errors.Is(err, pdf.ErrWorkLimit) || errors.Is(err, pdf.ErrTextLimit)
}

// IsPDFMagic reports whether data starts with the PDF magic bytes.
func IsPDFMagic(data []byte) bool { return bytes.HasPrefix(data, []byte(pdfMagic)) }

// InspectPDF parses data as a PDF without rendering it. Its work and text
// are bounded (pdfWorkBudget, pdfTextBudget): a document that runs out of
// work before any text is ErrPDFMalformed; one that runs out after some text
// keeps that text, marked truncated.
//
// The parse runs under the package's shared PDF admission and timeout
// boundary (runPDFParse), the same one the extractor uses, so all PDF parses
// in the process share its slots, stuck count and outer timeout. When that
// boundary refuses or abandons the parse, the error wraps ErrPDFBusy or
// ErrPDFTimedOut: the state of this process, not a verdict on the file.
func InspectPDF(data []byte) (PDFInfo, error) {
	if headerErr := checkPDFHeader(data); headerErr != nil {
		return PDFInfo{}, headerErr
	}
	type result struct {
		info PDFInfo
		err  error
	}
	res, admissionErr := runPDFParse(func() result {
		info, err := inspectPDFParse(data)
		return result{info, err}
	})
	if admissionErr != nil {
		return PDFInfo{}, admissionErr
	}
	return res.info, res.err
}

// inspectPDFParse does InspectPDF's parsing once the header is checked and
// a parse slot is held; it runs on runPDFParse's worker goroutine, so it
// recovers every parser panic itself.
func inspectPDFParse(data []byte) (info PDFInfo, err error) {
	defer func() {
		// The parser panics on some malformed inputs; a panic is a
		// structural defect of the input, reported as such.
		if r := recover(); r != nil {
			if e, ok := r.(error); ok && (isPDFBudgetStop(e) || errors.Is(e, pdf.ErrStructureLimit)) {
				info, err = PDFInfo{}, fmt.Errorf("%w: exceeds the parser's safety limits: %w", ErrPDFMalformed, e)
				return
			}
			info, err = PDFInfo{}, fmt.Errorf("%w: parser panic: %v", ErrPDFMalformed, r)
		}
	}()
	reader, openErr := pdf.NewReader(bytes.NewReader(data), int64(len(data)),
		pdf.Limits{Work: pdfWorkBudget, Text: pdfTextBudget})
	if openErr != nil {
		if errors.Is(openErr, pdf.ErrInvalidPassword) || bytes.Contains(data, []byte("/Encrypt")) {
			return PDFInfo{}, fmt.Errorf("%w: %w", ErrPDFEncrypted, openErr)
		}
		return PDFInfo{}, fmt.Errorf("%w: %w", ErrPDFMalformed, openErr)
	}
	root := reader.Trailer().Key("Root").Key("Pages")
	if root.Kind() != pdf.Dict {
		return PDFInfo{}, fmt.Errorf("%w: no page tree", ErrPDFMalformed)
	}
	// A page count above int32 is malformed (no reader indexes that many
	// pages), so it stays unknown (0) like a missing or non-positive one.
	if count := root.Key("Count"); count.Kind() == pdf.Integer {
		if n := count.Int64(); n > 0 && n <= math.MaxInt32 {
			info.Pages = int(n)
		}
	}
	walker := pageTextWalker{fonts: pdf.NewFontCache(), budget: len(data)}
	if err := walker.walkBounded(root); err != nil {
		return PDFInfo{}, err
	}
	info.Text = filterPDFTextControls(strings.Join(walker.pages, "\n"))
	if strings.TrimSpace(info.Text) == "" {
		if walker.truncated {
			// No text before the budget ran out: whether the document has
			// any is unknown, so it is not reported as text-less (scanned).
			return PDFInfo{}, fmt.Errorf("%w: exceeds the parser's safety limits before any text", ErrPDFMalformed)
		}
		info.Text = ""
	}
	if walker.truncated {
		info.Text += pdfTruncatedMarker
	}
	return info, nil
}

// CapText bounds extracted text to the same rune budget Extract applies,
// appending the same truncation marker when it cuts.
func CapText(s string) string {
	runes := []rune(s)
	if len(runes) <= maxExtractRunes {
		return s
	}
	return string(runes[:maxExtractRunes]) + "\n…[truncated]"
}

// filterPDFTextControls drops control and format characters from extracted
// PDF text but keeps line breaks and tabs, so page and line boundaries
// survive (filterControlCharacters drops them too).
func filterPDFTextControls(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, s)
}

// checkPDFHeader classifies the header: no magic → malformed; a version
// outside 1.0-1.7 → unsupported version.
func checkPDFHeader(data []byte) error {
	if !IsPDFMagic(data) {
		return fmt.Errorf("%w: missing %s header", ErrPDFMalformed, pdfMagic)
	}
	rest := data[len(pdfMagic):]
	if len(rest) < 3 || rest[1] != '.' || rest[0] < '0' || rest[0] > '9' || rest[2] < '0' || rest[2] > '9' {
		return fmt.Errorf("%w: unreadable version in header", ErrPDFMalformed)
	}
	if rest[0] != '1' || rest[2] > '7' {
		return fmt.Errorf("%w: %%PDF-%c.%c", ErrPDFUnsupportedVersion, rest[0], rest[2])
	}
	return nil
}

// pageTextWalker collects each page's text in page-tree order. budget caps
// the visited nodes at the input size, so a cyclic tree ends.
type pageTextWalker struct {
	fonts     *pdf.FontCache // shares a font between pages only by object identity
	pages     []string
	budget    int
	truncated bool // a parser budget ended the walk early
}

// walkBounded walks the page tree; when the work or text budget runs out
// mid-walk, the pages read so far stand and the walk ends truncated.
func (w *pageTextWalker) walkBounded(root pdf.Value) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok && isPDFBudgetStop(e) {
				w.truncated, err = true, nil
				return
			}
			panic(r)
		}
	}()
	err = w.walk(root, 0)
	if errors.Is(err, errPDFWalkStop) {
		return nil
	}
	return err
}

func (w *pageTextWalker) walk(node pdf.Value, depth int) error {
	if depth > maxPageTreeDepth || w.budget <= 0 {
		return fmt.Errorf("%w: page tree too deep or cyclic", ErrPDFMalformed)
	}
	w.budget--
	switch node.Key("Type").Name() {
	case "Pages":
		kids := node.Key("Kids")
		for i := 0; i < kids.Len(); i++ {
			if err := w.walk(kids.Index(i), depth+1); err != nil {
				return err
			}
		}
		return nil
	case "Page":
		page := pdf.Page{V: node}
		// Font names are local to the page's resources: each page gets its
		// own name map, so a later page's /F1 never decodes with an earlier
		// page's /F1 (gate-6 finding SF6-1).
		text, err := page.GetPlainText(w.fonts.PageFonts(page))
		if isPDFBudgetStop(err) {
			w.pages = append(w.pages, strings.Trim(text, "\r\n"))
			w.truncated = true
			return errPDFWalkStop
		}
		if err != nil {
			return fmt.Errorf("%w: page text: %w", ErrPDFMalformed, err)
		}
		// The extractor frames each page's text with line breaks; trim
		// them so the "\n" page separator is the only break between pages.
		w.pages = append(w.pages, strings.Trim(text, "\r\n"))
		return nil
	default:
		return nil
	}
}
