package agent

// pdf_notes.go — the request-only PDF notices of issue #1968 (spec §5.6)
// and the chat PDF registry the per-attempt presenter reads. The content of
// each notice is fixed by the spec; none of them is ever persisted: they
// live only in the request copy one provider attempt sends.

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/docextract"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	// pdfNoteNotCapable is N2: the attempt's model has no inline PDF route
	// and the PDF has no extractable text to show instead.
	pdfNoteNotCapable = "[this model does not accept PDF input here, so this PDF's content is unavailable]"
	// pdfNoteBudgetText is P1: the turn's one PDF retry is used; the
	// extracted text is shown instead.
	pdfNoteBudgetText = "[native PDF rejected by the provider; extracted text shown]"
	// pdfNoteBudgetNoText is P2: as P1, for a PDF without extractable text.
	pdfNoteBudgetNoText = "[native PDF rejected by the provider; no extractable text, content unavailable]"
	// pdfNoteBudgetNotInspected is P2 for a PDF that was not inspected
	// because the shared PDF parser was busy or timed out: whether it has
	// text is unknown, so the note keeps that reason instead of claiming
	// it has none. Its one %s is PDFTemporaryFailureReason's text.
	pdfNoteBudgetNotInspected = "[native PDF rejected by the provider; it could not be read locally (%s), content unavailable]"
	// pdfNoteAccessDenied is FR-13's note for a tool PDF whose access recheck
	// failed before this attempt.
	pdfNoteAccessDenied = "[PDF view unavailable: access denied]"
	// pdfFallbackName names a PDF whose original file name is unknown.
	pdfFallbackName = "attachment.pdf"
)

// pdfLimitWording names each configured PDF limit in plain language for
// L2 (spec §5.6, S10: "100 pages per file"), never as its data key. The
// value is the limit itself, from config.json pdf_limits
// (config.Config.ResolvePDFLimits).
var pdfLimitWording = map[string]string{
	"max_file_bytes":            "file size limit of %d bytes",
	"max_total_file_bytes":      "total file size limit of %d bytes per request",
	"max_request_bytes":         "request size limit of %d bytes",
	"max_pages_per_file":        "limit of %d pages per file",
	"max_pages_per_request":     "limit of %d pages per request",
	"max_documents_per_request": "limit of %d documents per request",
	"max_documents_per_message": "limit of %d documents per message",
}

// pdfNoteLimit is L2: a PDF demoted to text because admitting it would break
// the named limit config.json pdf_limits resolves for the attempt's
// provider/model.
func pdfNoteLimit(owner, limit string, value int64) string {
	wording, ok := pdfLimitWording[limit]
	if !ok {
		// Every caller passes a key of the table above; an unknown key
		// still names the limit rather than dropping the notice.
		return fmt.Sprintf("[PDF sent as text: over %s's %s limit of %d]", owner, limit, value)
	}
	return fmt.Sprintf("[PDF sent as text: over %s's "+wording+"]", owner, value)
}

// pdfNoteNative is M1: the live content of a tool result whose PDF rides
// this attempt natively. The bytes are never retained.
func pdfNoteNative(name string, size int, sha string) string {
	return fmt.Sprintf("[document: %s | native PDF attached | %d bytes | sha256 %s | not retained]", name, size, sha)
}

// chatPDF is what the presenter needs to show a chat PDF as text: its file
// name and, computed once per turn on first use, its extracted text
// injection and page count (0 = unknown).
type chatPDF struct {
	name      string
	once      sync.Once
	injection string
	hasText   bool
	pages     int
	// localFailure is non-empty when the PDF was not inspected because the
	// shared PDF parser was busy or timed out (tools.PDFTemporaryFailureReason):
	// hasText is then false without meaning the file has no text.
	localFailure string

	// source and sink locate the uploaded file and the turn's work folder,
	// so an attempt that sends its page images can name a workspace copy
	// preview_document accepts for later pages (office-preview spec
	// section 4.1). The copy is made once, on first need.
	source   string
	sink     *offloadSink
	copyOnce sync.Once
	copyDest string
	copyErr  error
}

// workspaceCopy copies the uploaded PDF into the turn's work folder once
// and returns the copy's path, or why there is none.
func (c *chatPDF) workspaceCopy() (string, error) {
	c.copyOnce.Do(func() {
		if c.source == "" {
			c.copyErr = errNoOffloadSink
			return
		}
		c.copyDest, c.copyErr = c.sink.offloadPath(c.source, c.name)
	})
	return c.copyDest, c.copyErr
}

// chatPDFInspector inspects a chat PDF's bytes. It is docextract.InspectPDF;
// a variable only so this package's tests can make an inspection report a
// busy or timed-out parser without occupying the real parse slots.
var chatPDFInspector = docextract.InspectPDF

// inspect fills the text injection and page count from the PDF bytes of
// dataURL, once.
func (c *chatPDF) inspect(dataURL string) {
	c.once.Do(func() {
		_, data, ok := protocoltypes.DecodePDFDataURL(dataURL)
		if !ok {
			c.injection = "\n\n" + protocoltypes.PDFMalformedNote
			return
		}
		// The same bounded parser as read_file (FR-6) and the same
		// outcomes: text, N1 when it has none, N3 with the typed reason
		// when it cannot be read locally. A PDF InspectPDF rejected is not
		// parsed a second time by another extractor (security finding F1:
		// that doubled the parse work and could override the typed reason).
		info, inspectErr := chatPDFInspector(data)
		switch {
		case inspectErr != nil:
			c.localFailure = tools.PDFTemporaryFailureReason(inspectErr)
			c.injection = "\n\n" + tools.PDFNoticeLocalFailure(c.name, inspectErr)
		case info.Text != "":
			c.pages = info.Pages
			c.hasText = true
			c.injection = fmt.Sprintf("\n\n[Attached file %q]\n%s\n[End of attached file]", c.name, docextract.CapText(info.Text))
		default:
			c.pages = info.Pages
			c.injection = "\n\n" + tools.PDFNoticeScanned(c.name)
		}
	})
}

// chatPDFRegistry maps the data URL chat resolution produced for a PDF to
// its chatPDF, so an attempt that demotes it can name the file. One
// registry per turn; a data URL the registry never saw (for example a data
// URL already in history) gets the fallback name.
type chatPDFRegistry struct {
	mu      sync.Mutex
	entries map[string]*chatPDF
}

// register records name for dataURL.
func (r *chatPDFRegistry) register(dataURL, name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]*chatPDF)
	}
	if _, ok := r.entries[dataURL]; !ok {
		r.entries[dataURL] = &chatPDF{name: name}
	}
}

// registerSource records where dataURL's upload lives and the turn's
// offload sink (the workspace copy named in its page-image note).
func (r *chatPDFRegistry) registerSource(dataURL, localPath string, sink *offloadSink) {
	if r == nil {
		return
	}
	entry := r.lookup(dataURL)
	r.mu.Lock()
	defer r.mu.Unlock()
	if entry.source == "" {
		entry.source, entry.sink = localPath, sink
	}
}

// lookup returns the entry for dataURL, creating one with the fallback name
// on a miss.
func (r *chatPDFRegistry) lookup(dataURL string) *chatPDF {
	if r == nil {
		return &chatPDF{name: pdfFallbackName}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]*chatPDF)
	}
	entry, ok := r.entries[dataURL]
	if !ok {
		entry = &chatPDF{name: pdfFallbackName}
		r.entries[dataURL] = entry
	}
	return entry
}

// encodeChatPDF is chat resolution's PDF branch (FR-15): it no longer
// decides native versus text. A PDF within the operator's max_media_size is
// encoded as a data URL for the per-attempt presenter to decide; a larger
// one is shown as its extracted text with L1. It returns the data URL (""
// when none) and the content injection ("" when none).
func encodeChatPDF(localPath, mime, filename string, size int64, maxSize int, reg *chatPDFRegistry) (string, string) {
	if size > int64(maxSize) {
		return "", buildDocumentInjection(localPath, mime, filename, size) + "\n" + tools.PDFNoticeMediaSize(int64(maxSize))
	}
	name := sanitizeInjectedName(fallbackName(filename, filepath.Base(localPath)))
	data, err := os.ReadFile(localPath)
	if err != nil {
		logger.WarnCF("agent", "Failed to read PDF attachment — attachment unavailable", map[string]any{
			"path":  localPath,
			"error": err.Error(),
		})
		return "", "[attachment unavailable: " + name + "]"
	}
	dataURL := protocoltypes.PDFDataURLPrefix + base64.StdEncoding.EncodeToString(data)
	reg.register(dataURL, name)
	return dataURL, ""
}

// pdfDecodedLen is the decoded byte length of a standard base64 payload.
func pdfDecodedLen(payload string) int64 {
	n := int64(len(payload)) / 4 * 3
	switch {
	case strings.HasSuffix(payload, "=="):
		n -= 2
	case strings.HasSuffix(payload, "="):
		n--
	}
	return n
}
