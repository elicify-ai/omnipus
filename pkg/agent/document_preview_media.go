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

// previewModelForms is what the active model reads (catalog only, AD-11).
type previewModelForms struct {
	pdf    bool
	images bool
	maxPDF int
	// maxPDFs is the most native PDFs per request (0 = no recorded limit).
	maxPDFs int
	resize  catalog.ResizeLimits
}

// attachTurnDocumentPreviews builds each preview_document result's model
// form for the active model (§4.1): the whole PDF when the model reads PDF
// natively and it fits, else page images when it sees images, else the
// extracted text. The source is re-authorized before every send; newest
// calls get the request media budget first.
func attachTurnDocumentPreviews(ctx context.Context, messages []providers.Message, p *turnDocumentPreviews, forms previewModelForms) ([]providers.Message, error) {
	if p == nil {
		return messages, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.byID) == 0 {
		return messages, nil
	}
	live := append([]providers.Message(nil), messages...)
	budget := previewRequestBudget
	pdfsLeft := forms.maxPDFs
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
		if forms.maxPDFs > 0 && pdfsLeft == 0 {
			// The provider's per-request document count is used up by newer
			// calls: this one goes as page images (or text).
			callForms.pdf = false
		}
		before := len(live[i].Media)
		used, err := attachDocumentPreview(ctx, &live[i], d, callForms, budget)
		if err != nil {
			return nil, err
		}
		budget -= used
		if callForms.pdf && len(live[i].Media) > before && strings.HasPrefix(live[i].Media[len(live[i].Media)-1], "data:application/pdf;") {
			pdfsLeft--
		}
	}
	return live, nil
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
	if forms.pdf && len(d.PDF) > 0 && len(d.PDF) <= forms.maxPDF {
		if len(d.PDF) > budget {
			msg.Content += previewMarkerNotResent
			return 0, nil
		}
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
	if forms.images && len(d.Pages) == 0 && d.RenderPages != nil {
		// Rendered on first need, once (read_file on a PDF, §4.1).
		pages, unavailable, note := d.RenderPages(ctx)
		d.Pages, d.PagesNote, d.RenderPages = pages, note, nil
		if unavailable != "" {
			d.ImagesUnavailable = unavailable
		}
	}
	if forms.images && len(d.Pages) > 0 {
		return attachPreviewPages(msg, d, forms.resize, budget, sizeNote)
	}
	reason := sizeNote
	switch {
	case d.ImagesUnavailable != "":
		reason = "; page images unavailable: " + d.ImagesUnavailable + reason
	case !forms.images:
		reason = "; this model reads text only" + reason
	}
	if d.ModelOnly {
		// The retained result text already is the text form (today's
		// read_file path); only say why images did not go.
		if forms.images && d.ImagesUnavailable != "" {
			msg.Content += "\n[model received: extracted text" + reason + d.PagesNote + "]"
		}
		return 0, nil
	}
	msg.Content += "\n[model received: extracted text" + reason + "]\n" + d.Text
	return 0, nil
}

func attachPreviewPages(msg *providers.Message, d *tools.DocumentPreview, limits catalog.ResizeLimits, budget int, sizeNote string) (int, error) {
	used := 0
	sent := 0
	notResent := false
	for _, page := range d.Pages {
		if sent == previewMaxImages {
			break
		}
		decoded, _, err := image.Decode(bytes.NewReader(page.PNG))
		if err != nil {
			return used, fmt.Errorf("invalid preview page %d: %w", page.Number, err)
		}
		normalized, err := resize.ResizeToFit(decoded, limits)
		if err != nil {
			return used, fmt.Errorf("preview page %d cannot fit model limits: %w", page.Number, err)
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
	if notResent {
		msg.Content += previewMarkerNotResent
	}
	return used, nil
}

// previewForms resolves the active model's preview forms from the catalog.
// The native PDF limit is the agent's media size limit (AD-11).
func (rt *agentLoopRunTurn) previewForms(cat *catalog.Catalog, provider, model string, limits catalog.ResizeLimits) previewModelForms {
	maxSize := 20 << 20
	if cfg := rt.al.GetConfig(); cfg != nil {
		maxSize = cfg.Agents.Defaults.GetMaxMediaSize()
	}
	maxPDF, maxDocs := nativePDFLimits(cat, provider, model, maxSize)
	return previewModelForms{
		pdf:     modelSupportsPDF(cat, provider, model),
		images:  modelSupportsImage(cat, provider, model),
		maxPDF:  maxPDF,
		maxPDFs: maxDocs,
		resize:  limits,
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

// nativePDFLimits is the native-PDF size limit and per-request document
// count for the active provider: the agent's media size limit, tightened by
// the provider's documented catalog pdf_limits (office-preview D2 A).
func nativePDFLimits(cat *catalog.Catalog, provider, model string, maxSize int) (maxBytes, maxDocs int) {
	maxBytes = maxSize
	if cat == nil {
		return maxBytes, 0
	}
	if l := cat.Resolve(provider, model).PDFLimits(); l != nil {
		maxBytes = min(maxBytes, l.MaxDocumentBytes)
		maxDocs = l.MaxDocumentsPerRequest
	}
	return maxBytes, maxDocs
}
