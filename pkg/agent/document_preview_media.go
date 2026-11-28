// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media/resize"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Live-turn model media budgets for preview_document (office-preview spec
// AD-6).
const (
	previewTurnBudget    = 64 << 20
	previewGatewayBudget = 256 << 20
	previewRequestBudget = 32 << 20
	previewMaxImages     = 20

	previewMarkerEvicted   = "\n[preview_document content evicted from this turn's media budget; call preview_document again]"
	previewMarkerRefused   = "\n[preview_document content not kept: larger than the 64 MiB per-turn media budget]"
	previewMarkerNotResent = "\n[pages not re-sent; call preview_document again]"
)

// previewGatewayBytes counts the live preview bytes held by every running
// turn of this gateway. It changes only through reservePreviewGatewayBytes
// (a compare-and-swap reservation, so two turns cannot both take the last
// room) and releasePreviewGatewayBytes.
var previewGatewayBytes atomic.Int64

// reservePreviewGatewayBytes takes n bytes of the gateway budget in one
// atomic step, or nothing when they do not fit (AD-6, SF W-01): a separate
// load, check and add lets two turns that both see room overshoot together.
func reservePreviewGatewayBytes(n int64) bool {
	for {
		used := previewGatewayBytes.Load()
		if used+n > previewGatewayBudget {
			return false
		}
		if previewGatewayBytes.CompareAndSwap(used, used+n) {
			return true
		}
	}
}

func releasePreviewGatewayBytes(n int64) { previewGatewayBytes.Add(-n) }

// turnDocumentPreviews holds one turn's live preview_document forms. Safe
// for concurrent use (parallel tool calls record into it).
//
// mu serializes recording (add), request assembly (attachTurnDocumentPreviews)
// and release. acct guards the accounting (held, markers, bytes, and the byID
// entries eviction frees) and is also taken alone by the lazy-page charge,
// which runs inside request assembly while mu is held (SF-10).
type turnDocumentPreviews struct {
	mu      sync.Mutex
	acct    sync.Mutex
	order   []string // tool call ids, oldest first
	byID    map[string]*tools.DocumentPreview
	held    map[string]int // live bytes per call
	markers map[string]string
	bytes   int
}

// add records a call's preview, evicting the oldest batches to stay within
// the per-turn and per-gateway budgets; an item larger than the per-turn
// budget is refused (its forms freed, a marker kept). Page images a lazy
// renderer (read_file) produces later are charged when they arrive.
func (p *turnDocumentPreviews) add(id string, d *tools.DocumentPreview) {
	if p == nil || d == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.acct.Lock()
	defer p.acct.Unlock()
	if p.byID == nil {
		p.byID, p.held, p.markers = map[string]*tools.DocumentPreview{}, map[string]int{}, map[string]string{}
	}
	p.order = append(p.order, id)
	p.byID[id] = d
	n := d.LiveBytes()
	if n > previewTurnBudget {
		d.FreeLiveForms()
		p.markers[id] = previewMarkerRefused
		return
	}
	if !p.reserveLocked(id, n) {
		d.FreeLiveForms()
		p.markers[id] = previewMarkerEvicted
		return
	}
	p.held[id] = n
	if render := d.RenderPages; render != nil {
		d.RenderPages = func(ctx context.Context) ([]tools.DocumentPreviewPage, string, string) {
			pages, unavailable, note := render(ctx)
			if len(pages) > 0 && !p.chargeLazyPages(id, pages) {
				return nil, previewLazyPagesNotKept, note
			}
			return pages, unavailable, note
		}
	}
}

// previewLazyPagesNotKept is the "page images unavailable" reason when
// lazily rendered pages do not fit the live media budgets.
const previewLazyPagesNotKept = "the page images do not fit this turn's live media budget"

// reserveLocked takes n bytes of the per-turn and gateway budgets for keep,
// evicting this turn's oldest other batches while they do not fit. False
// when they cannot fit; nothing is taken then. Caller holds acct.
func (p *turnDocumentPreviews) reserveLocked(keep string, n int) bool {
	for {
		if p.bytes+n <= previewTurnBudget && reservePreviewGatewayBytes(int64(n)) {
			p.bytes += n
			return true
		}
		if !p.evictOldestLocked(keep) {
			return false
		}
	}
}

// chargeLazyPages charges page images rendered after id was recorded (the
// read_file renderer runs on first send) to the same budgets, so the
// retained forms never exceed what the accounting says. False when they do
// not fit or id is no longer held; the caller then drops the pages.
func (p *turnDocumentPreviews) chargeLazyPages(id string, pages []tools.DocumentPreviewPage) bool {
	delta := 0
	for _, page := range pages {
		delta += len(page.PNG)
	}
	p.acct.Lock()
	defer p.acct.Unlock()
	held, ok := p.held[id]
	if !ok || held+delta > previewTurnBudget || !p.reserveLocked(id, delta) {
		return false
	}
	p.held[id] = held + delta
	return true
}

// evictOldestLocked frees the oldest held batch other than keep. Caller
// holds acct.
func (p *turnDocumentPreviews) evictOldestLocked(keep string) bool {
	for _, id := range p.order {
		if id == keep {
			continue
		}
		n, ok := p.held[id]
		if !ok {
			continue
		}
		p.byID[id].FreeLiveForms()
		delete(p.held, id)
		p.markers[id] = previewMarkerEvicted
		p.bytes -= n
		releasePreviewGatewayBytes(int64(n))
		return true
	}
	return false
}

// release returns the turn's bytes to the gateway budget at turn end.
func (p *turnDocumentPreviews) release() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.acct.Lock()
	defer p.acct.Unlock()
	for id, n := range p.held {
		if d := p.byID[id]; d != nil {
			d.FreeLiveForms()
		}
		releasePreviewGatewayBytes(int64(n))
	}
	p.held, p.bytes = map[string]int{}, 0
}

// marker is id's eviction or refusal marker, if any.
func (p *turnDocumentPreviews) marker(id string) (string, bool) {
	p.acct.Lock()
	defer p.acct.Unlock()
	m, ok := p.markers[id]
	return m, ok
}

// previewModelForms is what the active model reads. pdf is the #1968
// capability decision (catalogue pdf modality plus the configured inline
// route, pdfNativeSupported) while the turn's one PDF recovery is unspent;
// maxPDF and maxPDFs come from config.Config.ResolvePDFLimits and the
// agent's media size limit, never from the catalogue. The presenter
// (pdf_present.go) still decides every PDF this attaches.
type previewModelForms struct {
	pdf    bool
	images bool
	maxPDF int
	// maxPDFs is the most native PDFs per request (0 = no configured limit).
	maxPDFs int
	resize  catalog.ResizeLimits
	// pdfRejected: the turn's one PDF recovery is spent (a provider rejected
	// a native PDF); pdf is then false and a preview without text sends the
	// PDF's extracted text.
	pdfRejected bool
	// turnUploadBytes is the decoded document media of this turn's own
	// uploads as recorded when the request was resolved (native PDFs and
	// upload page images); the assembler takes the larger of it and the
	// native PDFs it finds in the turn's user message, from the 32 MiB first
	// (AD-6, architect rule G5).
	turnUploadBytes int
	// newest marks the newest live preview of the request: when the 32 MiB
	// has no room for it, it gets its text and a note, not the not-re-sent
	// marker (architect rule G5 §5). Set per call by the assembler.
	newest bool
	// pdfCountUsedUp: the provider's per-request native-PDF count (maxPDFs)
	// is used up by other PDFs in the request, so pdf was turned off for this
	// call (AD-6). Set per call by the assembler; the note names it (G12).
	pdfCountUsedUp bool
	// demotedNote is set only when the presenter demoted this preview's
	// native PDF on one attempt (a configured limit or the request body
	// limit): the reason the forms that replace it carry.
	demotedNote string
}

// previewOrigin is one preview_document result as it was before its forms
// were attached, kept for the presenter: when the presenter does not send
// the attached PDF natively on an attempt, it rebuilds the message's page
// images or text from here.
type previewOrigin struct {
	msg   providers.Message
	doc   *tools.DocumentPreview
	forms previewModelForms
}

// noBudgetNote is the note on the newest preview when the request's 32 MiB
// document media budget has no room for its PDF or pages: this turn's
// uploads took it, or the preview alone does not fit.
func (f previewModelForms) noBudgetNote() string {
	if f.turnUploadBytes > 0 {
		return fmt.Sprintf("; this turn's uploads use the request's %d MiB media budget", previewRequestBudget>>20)
	}
	return fmt.Sprintf("; the preview's media does not fit the request's %d MiB media budget", previewRequestBudget>>20)
}

// attachTurnDocumentPreviews builds each preview_document result's model
// form for the active model (§4.1): the whole PDF when the model reads PDF
// natively and it fits, else page images when it sees images, else the
// extracted text. The source is re-authorized before every send; newest
// calls get the request media budget first.
func attachTurnDocumentPreviews(ctx context.Context, messages []providers.Message, p *turnDocumentPreviews, forms previewModelForms) ([]providers.Message, error) {
	live, _, _, err := attachTurnDocumentPreviewsFor(ctx, messages, p, forms)
	return live, err
}

// attachTurnDocumentPreviewsFor is attachTurnDocumentPreviews that also
// returns, by message index, each preview whose native PDF it attached (the
// presenter's fallback source), and what is left of the request's 32 MiB
// document media budget.
func attachTurnDocumentPreviewsFor(ctx context.Context, messages []providers.Message, p *turnDocumentPreviews, forms previewModelForms) ([]providers.Message, map[int]previewOrigin, int, error) {
	turnBudgetLeft := max(previewRequestBudget-max(forms.turnUploadBytes, turnMessagePDFBytes(messages)), 0)
	if p == nil {
		return messages, nil, turnBudgetLeft, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.byID) == 0 {
		return messages, nil, turnBudgetLeft, nil
	}
	origins := map[int]previewOrigin{}
	live := append([]providers.Message(nil), messages...)
	// The request's 32 MiB covers this turn's document media only (AD-6,
	// architect rule G5): this turn's own uploads first, then the previews,
	// newest call first. Screenshots, inspection images and earlier turns'
	// media keep their own limits. The provider's native-document count
	// covers every PDF in the request, earlier turns included.
	forms.turnUploadBytes = max(forms.turnUploadBytes, turnMessagePDFBytes(live))
	budget := max(previewRequestBudget-forms.turnUploadBytes, 0)
	pdfsLeft := forms.maxPDFs
	if forms.maxPDFs > 0 {
		pdfsLeft = max(forms.maxPDFs-requestNativePDFCount(live), 0)
	}
	newest := true
	for i := len(live) - 1; i >= 0; i-- {
		d, ok := p.byID[live[i].ToolCallID]
		if !ok {
			continue
		}
		if marker, gone := p.marker(live[i].ToolCallID); gone {
			live[i].Content += marker
			continue
		}
		callForms := forms
		callForms.newest, newest = newest, false
		if forms.maxPDFs > 0 && pdfsLeft == 0 {
			// The provider's per-request document count is used up by newer
			// calls: this one goes as page images (or text).
			callForms.pdf, callForms.pdfCountUsedUp = false, forms.pdf
		}
		before := len(live[i].Media)
		original := live[i]
		used, err := attachDocumentPreview(ctx, &live[i], d, callForms, budget)
		if err != nil {
			return nil, nil, 0, err
		}
		budget -= used
		if callForms.pdf && len(live[i].Media) > before && strings.HasPrefix(live[i].Media[len(live[i].Media)-1], "data:application/pdf;") {
			pdfsLeft--
			origins[i] = previewOrigin{msg: original, doc: d, forms: callForms}
		}
	}
	return live, origins, budget, nil
}

// previewFallback rebuilds a preview message whose native PDF the presenter
// did not send on this attempt: its page images within budget, else its
// text, with the presenter's reason. It returns the message and the page
// image bytes it used.
func previewFallback(ctx context.Context, origin previewOrigin, budget int, note string) (providers.Message, int, error) {
	msg := origin.msg
	forms := origin.forms
	forms.pdf, forms.pdfCountUsedUp, forms.demotedNote = false, false, note
	// The newest-preview rule (G5 section 5): a demoted PDF is never left
	// with only the not-re-sent marker.
	forms.newest = true
	used, err := attachDocumentPreview(ctx, &msg, origin.doc, forms, budget)
	return msg, used, err
}

// turnMessagePDFBytes is the decoded size of the native PDFs in the turn's
// own user message (turnUserMessageIndex).
func turnMessagePDFBytes(messages []providers.Message) int {
	i := turnUserMessageIndex(messages)
	if i < 0 {
		return 0
	}
	n := 0
	for _, ref := range messages[i].Media {
		if strings.HasPrefix(ref, "data:application/pdf;") {
			n += dataURLDecodedSize(ref)
		}
	}
	return n
}

// requestNativePDFCount is how many native PDFs the messages carry.
func requestNativePDFCount(messages []providers.Message) int {
	n := 0
	for _, m := range messages {
		for _, ref := range m.Media {
			if strings.HasPrefix(ref, "data:application/pdf;") {
				n++
			}
		}
	}
	return n
}

// dataURLDecodedSize is the payload size of a data URL: the decoded length
// of a base64 payload, the raw length otherwise.
func dataURLDecodedSize(ref string) int {
	comma := strings.IndexByte(ref, ',')
	if comma < 0 {
		return 0
	}
	payload := ref[comma+1:]
	if !strings.HasSuffix(ref[:comma], ";base64") {
		return len(payload)
	}
	return len(payload)/4*3 - (len(payload) - len(strings.TrimRight(payload, "=")))
}

// attachDocumentPreview adds one call's form and returns the decoded bytes
// it put into the request.
func attachDocumentPreview(ctx context.Context, msg *providers.Message, d *tools.DocumentPreview, forms previewModelForms, budget int) (int, error) {
	if d.Reauthorize == nil {
		return 0, fmt.Errorf("preview_document source access cannot be rechecked")
	}
	// A revoked source sends neither PDF nor images (§4.2); the turn goes on.
	if denied := previewAccessDenied(ctx, d); denied != "" {
		msg.Content += denied
		return 0, nil
	}
	noBudget := ""
	if forms.pdf && len(d.PDF) > 0 && len(d.PDF) <= forms.maxPDF && len(d.PDF) > budget {
		if !forms.newest {
			msg.Content += previewMarkerNotResent
			return 0, nil
		}
		// The newest preview is never left with only the marker: page
		// images if they fit, else its text (architect rule G5 §5).
		forms.pdf, noBudget = false, forms.noBudgetNote()
	}
	if forms.pdf && len(d.PDF) > 0 && len(d.PDF) <= forms.maxPDF {
		msg.Media = append(msg.Media, "data:application/pdf;base64,"+base64.StdEncoding.EncodeToString(d.PDF))
		if d.NativeContent != "" {
			msg.Content = d.NativeContent
		}
		if d.Record.PageCount > 0 {
			msg.Content += fmt.Sprintf("\n[model received: the whole PDF, %d pages]", d.Record.PageCount)
		} else {
			msg.Content += "\n[model received: the whole PDF]"
		}
		return len(d.PDF), nil
	}
	sizeNote := ""
	if forms.pdf && len(d.PDF) > forms.maxPDF {
		sizeNote = fmt.Sprintf("; the PDF (%.1f MiB) is larger than this model's native PDF limit (%.1f MiB)",
			float64(len(d.PDF))/(1<<20), float64(forms.maxPDF)/(1<<20))
	}
	countNote := ""
	if forms.pdfCountUsedUp && len(d.PDF) > 0 && len(d.PDF) <= forms.maxPDF {
		countNote = fmt.Sprintf("; the provider's per-request PDF count limit (%d) is used up by other PDFs in this request", forms.maxPDFs)
	}
	if forms.images && len(d.Pages) == 0 && d.RenderPages != nil {
		// Rendered on first need, once (read_file on a PDF, §4.1).
		pages, unavailable, note := d.RenderPages(ctx)
		d.Pages, d.PagesNote, d.RenderPages = pages, note, nil
		if unavailable != "" {
			d.ImagesUnavailable = unavailable
		}
	}
	if forms.images && len(d.Pages) > 0 {
		used, sent, err := attachPreviewPages(msg, d, forms.resize, budget, sizeNote, forms.newest)
		if err != nil || sent > 0 || !forms.newest {
			return used, err
		}
		noBudget = forms.noBudgetNote()
	}
	demoted := ""
	if forms.demotedNote != "" {
		demoted = "; " + forms.demotedNote
	}
	reason := sizeNote + countNote + noBudget + demoted
	switch {
	case noBudget != "":
	case d.ImagesUnavailable != "":
		reason = "; page images unavailable: " + d.ImagesUnavailable + reason
	case countNote != "" || demoted != "":
		// The model reads PDFs; a limit, not the model, is why this one
		// went as text (G12).
	case !forms.images:
		reason = "; this model reads text only" + reason
	}
	if d.ModelOnly {
		// The retained result text already is the text form (today's
		// read_file path); only say why images did not go.
		if (forms.images && (d.ImagesUnavailable != "" || noBudget != "")) || demoted != "" {
			msg.Content += "\n[model received: extracted text" + reason + d.PagesNote + "]"
		}
		return 0, nil
	}
	if d.Text == "" && len(d.PDF) > 0 && (forms.pdfRejected || noBudget != "" || countNote != "" || demoted != "") {
		// No stored text: recover with the text extracted from the same
		// bytes, as an uploaded PDF does (provider rejection, no budget for
		// the newest preview's media, or the per-request PDF count used up).
		why := "; the provider rejected the native PDF"
		if !forms.pdfRejected {
			why = reason
		}
		msg.Content += "\n[model received: extracted text" + why + "]" + pdfFallbackText(d.PDF)
		return 0, nil
	}
	msg.Content += "\n[model received: extracted text" + reason + "]\n" + d.Text
	return 0, nil
}

// attachPreviewPages adds d's page images within budget and returns the
// bytes and pages sent. When newest and no page fits, it adds nothing (the
// caller sends the text form instead of the not-re-sent marker).
func attachPreviewPages(msg *providers.Message, d *tools.DocumentPreview, limits catalog.ResizeLimits, budget int, sizeNote string, newest bool) (int, int, error) {
	used := 0
	sent := 0
	notResent := false
	for _, page := range d.Pages {
		if sent == previewMaxImages {
			break
		}
		decoded, _, err := image.Decode(bytes.NewReader(page.PNG))
		if err != nil {
			return used, sent, fmt.Errorf("invalid preview page %d: %w", page.Number, err)
		}
		normalized, err := resize.ResizeToFit(decoded, limits)
		if err != nil {
			return used, sent, fmt.Errorf("preview page %d cannot fit model limits: %w", page.Number, err)
		}
		if used+len(normalized.Data) > budget {
			notResent = true
			break
		}
		msg.Media = append(msg.Media, "data:"+normalized.Mime+";base64,"+base64.StdEncoding.EncodeToString(normalized.Data))
		used += len(normalized.Data)
		sent++
	}
	if sent > 0 {
		if d.NativeContent != "" {
			msg.Content = d.NativeContent
		}
		msg.Content += fmt.Sprintf("\n[model received: page images %d–%d%s%s]", d.Pages[0].Number, d.Pages[sent-1].Number, sizeNote, d.PagesNote)
	}
	if notResent && (sent > 0 || !newest) {
		msg.Content += previewMarkerNotResent
	}
	return used, sent, nil
}

// previewForms resolves the active model's preview forms with the #1968
// mechanism: the PDF form is offered only when pdfNativeSupported (the
// catalogue's pdf modality and the configured inline route) holds and the
// turn's one PDF recovery is unspent; the native PDF size is the agent's
// media size limit tightened by the configured max_file_bytes, and the
// document count is the configured max_documents_per_request. The
// catalogue supplies no limit.
func (rt *agentLoopRunTurn) previewForms(cat *catalog.Catalog, provider, model string, limits catalog.ResizeLimits) previewModelForms {
	maxSize := config.DefaultMaxMediaSize
	rt.al.mu.RLock()
	cfg := rt.al.cfg
	pdfLimits := cfg.ResolvePDFLimits(provider, model)
	if cfg != nil {
		maxSize = cfg.Agents.Defaults.GetMaxMediaSize()
	}
	rt.al.mu.RUnlock()
	maxPDF := maxSize
	if v, set := pdfLimits.MaxFileBytes(); set && v > 0 && v < int64(maxPDF) {
		maxPDF = int(v)
	}
	maxDocs := 0
	if v, set := pdfLimits.MaxDocumentsPerRequest(); set && v > 0 {
		maxDocs = int(v)
	}
	spent := rt.ts != nil && rt.ts.mediaRetryDone.Load()
	capable := cat != nil && cat.Document() != nil && pdfNativeSupported(cat.Resolve(provider, model), pdfLimits)
	uploads := rt.turnUploadMedia.pageImageBytes
	if capable && !spent {
		// Otherwise this turn's uploaded PDFs go as text or page images.
		uploads += rt.turnUploadMedia.nativePDFBytes
	}
	return previewModelForms{
		pdf:             capable && !spent,
		images:          modelSupportsImage(cat, provider, model),
		maxPDF:          maxPDF,
		maxPDFs:         maxDocs,
		resize:          limits,
		pdfRejected:     spent,
		turnUploadBytes: uploads,
	}
}

// previewAccessDenied re-checks the source and returns the withheld marker
// when access is gone, "" when it is still granted.
func previewAccessDenied(ctx context.Context, d *tools.DocumentPreview) string {
	if err := d.Reauthorize(ctx); err != nil {
		return "\n[preview_document content withheld: access to the source was denied: " + err.Error() + "]"
	}
	return ""
}
