package agent

// pdf_present.go — the per-attempt PDF presenter of issue #1968 (spec
// FR-10 to FR-15). runProviderAttempt calls it on the request copy of every
// provider attempt: chat PDFs (data URLs chat resolution encoded), tool
// PDFs (the turn-private InspectionPDFs of read_file/library_read) and
// preview_document PDFs (attached by attachTurnDocumentPreviewsFor) are
// presented natively or not, decided per attempt from the attempt's own
// catalogue capability and engine-configured limits — the one PDF decision
// and recovery state for every source (founder decision 2026-11-28). A PDF
// that is not native becomes its text, or its page images on a model that
// sees images (pdf_present_forms.go). Canonical messages are never
// mutated; tool PDF bytes exist only in the returned request copy.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/common"
	"github.com/elicify-ai/omnipus/pkg/providers/protocoltypes"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// pdfDecision is why a PDF candidate is (not) native on one attempt.
type pdfDecision int

const (
	pdfNative       pdfDecision = iota // admitted: travels as a native document
	pdfNotCapable                      // FR-1 says the attempt's row has no inline PDF route
	pdfBudgetSpent                     // FR-17: the turn's one PDF retry is used
	pdfAccessDenied                    // the tool item's access recheck failed
	pdfOverLimit                       // FR-10/FR-12: a document limit of the row
)

// pdfCandidate is one PDF of the request copy, in wire order.
type pdfCandidate struct {
	msg      int    // index of the message in the request copy
	media    int    // chat: index into the message's Media
	dataURL  string // chat: the resolved data URL; tool: encoded on demand
	tool     bool
	item     tools.InspectionPDF
	chat     *chatPDF
	preview  *previewOrigin // a preview_document PDF the stage attached
	bytes    int64          // decoded size
	decision pdfDecision
	note     string // L2 for pdfOverLimit
	denied   error  // the access recheck's refusal, for pdfAccessDenied
}

// pages is the candidate's known page count (0 = unknown).
func (c *pdfCandidate) pages() int64 {
	if c.tool {
		return int64(c.item.Pages)
	}
	if c.preview != nil {
		return int64(c.preview.doc.Record.PageCount)
	}
	c.chat.inspect(c.dataURL)
	return int64(c.chat.pages)
}

// toolDataURL is the native media entry of a tool candidate.
func (c *pdfCandidate) toolDataURL() string {
	if c.dataURL == "" {
		c.dataURL = protocoltypes.PDFDataURLPrefix + base64.StdEncoding.EncodeToString(c.item.Bytes)
	}
	return c.dataURL
}

// pdfAttemptInput is what one attempt's presentation depends on.
type pdfAttemptInput struct {
	provider providers.LLMProvider
	name     string // catalogue provider id of the attempt
	model    string
	tools    []providers.ToolDefinition
	opts     map[string]any
	stream   bool // whether the attempt streams (its body differs)
}

// collectPDFCandidates lists every PDF of messages in wire order: chat data
// URLs in their message's media order, then tool items of a tool result in
// the order the tool returned them. A PDF data URL in a message the stage
// attached a preview_document PDF to is that preview's candidate.
func (rt *agentLoopRunTurn) collectPDFCandidates(messages []providers.Message, previews map[int]previewOrigin) []*pdfCandidate {
	var out []*pdfCandidate
	for i := range messages {
		for j, mediaURL := range messages[i].Media {
			payload, ok := pdfPayload(mediaURL)
			if !ok {
				continue // not a PDF, or malformed: the transport shows its note
			}
			if origin, isPreview := previews[i]; isPreview {
				out = append(out, &pdfCandidate{
					msg: i, media: j, dataURL: mediaURL, preview: &origin, bytes: pdfDecodedLen(payload),
				})
				continue
			}
			out = append(out, &pdfCandidate{
				msg: i, media: j, dataURL: mediaURL,
				chat: rt.chatPDFs.lookup(mediaURL), bytes: pdfDecodedLen(payload),
			})
		}
		if id := messages[i].ToolCallID; id != "" {
			for _, item := range rt.inspectionPDFs[id] {
				out = append(out, &pdfCandidate{msg: i, tool: true, item: item, bytes: int64(len(item.Bytes))})
			}
		}
	}
	return out
}

// pdfPayload returns the base64 payload of a PDF data URL in the standard
// base64 form chat resolution produces. Any other PDF data URL is left to
// the transport, which shows PDFMalformedNote for it (FR-18).
func pdfPayload(mediaURL string) (string, bool) {
	if !strings.HasPrefix(mediaURL, protocoltypes.PDFDataURLPrefix) {
		return "", false
	}
	payload := strings.TrimPrefix(mediaURL, protocoltypes.PDFDataURLPrefix)
	return payload, payload != ""
}

// presentAttemptPDFs returns the request copy of one attempt with every PDF
// presented natively or as text, and whether any PDF travels natively.
func (rt *agentLoopRunTurn) presentAttemptPDFs(
	ctx context.Context, in pdfAttemptInput, stage attemptStage,
) ([]providers.Message, bool, error) {
	messages := stage.messages
	cands := rt.collectPDFCandidates(messages, stage.previews)
	if len(cands) == 0 {
		return messages, false, nil
	}
	cat := rt.al.getCapabilityCatalog()
	if cat == nil || cat.Document() == nil {
		// callProviderOnce refuses this state before any attempt (FR-3);
		// reaching here means a caller skipped that gate.
		return nil, false, &pdfModelListUnavailableError{Provider: in.name, Model: in.model}
	}
	handle := cat.Resolve(in.name, in.model)
	rt.al.mu.RLock()
	limits := rt.al.cfg.ResolvePDFLimits(in.name, in.model)
	rt.al.mu.RUnlock()
	if err := rt.decidePDFEligibility(ctx, cands, pdfNativeSupported(handle, limits)); err != nil {
		return nil, false, err
	}
	owner := in.name + "/" + in.model
	admitPDFsNewestFirst(cands, limits, owner, pdfWireGroups(in.provider, messages, limits))
	build := func() ([]providers.Message, error) { return rt.buildPDFRequest(ctx, messages, cands, stage) }
	out, err := build()
	if err != nil {
		return nil, false, err
	}
	out, err = fitPDFRequestBytes(in, out, cands, limits, owner, build)
	if err != nil {
		return nil, false, err
	}
	sent := false
	for _, c := range cands {
		sent = sent || c.decision == pdfNative
	}
	return out, sent, nil
}

// decidePDFEligibility marks every candidate that cannot be native on this
// attempt regardless of limits: the row is not capable (FR-1), the turn's
// PDF retry is spent (FR-17), or a tool item's access recheck fails. The
// recheck runs once per item per attempt that could admit it.
func (rt *agentLoopRunTurn) decidePDFEligibility(ctx context.Context, cands []*pdfCandidate, capable bool) error {
	spent := rt.ts.mediaRetryDone.Load()
	for _, c := range cands {
		switch {
		case !capable:
			c.decision = pdfNotCapable
		case spent:
			c.decision = pdfBudgetSpent
		case c.tool:
			if c.item.Reauthorize == nil {
				return fmt.Errorf("inspection PDF %q access cannot be rechecked", c.item.Name)
			}
			if err := c.item.Reauthorize(ctx); err != nil {
				logger.WarnCF("agent", "tool PDF access recheck denied; the PDF is not sent natively", map[string]any{
					"name":  c.item.Name,
					"error": err.Error(),
				})
				c.decision, c.denied = pdfAccessDenied, err
			}
		case c.preview != nil:
			// The same per-send recheck as a tool PDF (security finding
			// F2): the preview's bytes go only while access holds.
			if c.preview.doc.Reauthorize == nil {
				return fmt.Errorf("preview_document source access cannot be rechecked")
			}
			if err := c.preview.doc.Reauthorize(ctx); err != nil {
				logger.WarnCF("agent", "preview_document access recheck denied; the PDF is not sent", map[string]any{
					"source": c.preview.doc.Record.SourceName,
					"error":  err.Error(),
				})
				c.decision, c.denied = pdfAccessDenied, err
			}
		}
	}
	return nil
}

// pdfWireGroups maps each message index to its wire message (FR-11). It is
// consulted only when the row states max_documents_per_message; a transport
// without WireMessageGroups sends one internal message as one wire message.
func pdfWireGroups(p providers.LLMProvider, messages []providers.Message, limits config.PDFLimits) func(int) int {
	identity := func(i int) int { return i }
	if limit, set := limits.MaxDocumentsPerMessage(); !set || limit == 0 {
		return identity
	}
	grouper, ok := p.(providers.WireMessageGrouper)
	if !ok {
		return identity
	}
	groupOf := make(map[int]int, len(messages))
	for g, members := range grouper.WireMessageGroups(messages) {
		for _, i := range members {
			groupOf[i] = g
		}
	}
	return func(i int) int {
		if g, ok := groupOf[i]; ok {
			return g
		}
		return -1 - i
	}
}

// admitPDFsNewestFirst applies the row's document limits (FR-10): newest
// candidate first (later message first; within a message, later media
// first), a PDF is admitted only if every limit stated in data still holds.
// A limit is inclusive; 0 states no limit; an unknown page count adds no
// pages but counts its bytes and as a document.
func admitPDFsNewestFirst(cands []*pdfCandidate, limits config.PDFLimits, owner string, groupOf func(int) int) {
	if limits.Empty() {
		return
	}
	var totalBytes, totalPages, docs int64
	perGroup := map[int]int64{}
	for k := len(cands) - 1; k >= 0; k-- {
		c := cands[k]
		if c.decision != pdfNative {
			continue
		}
		var pages int64
		if pagesLimited(limits) {
			pages = c.pages()
		}
		group := groupOf(c.msg)
		checks := []struct {
			name   string
			limit  func() (int64, bool)
			amount int64
		}{
			{"max_file_bytes", limits.MaxFileBytes, c.bytes},
			{"max_pages_per_file", limits.MaxPagesPerFile, pages},
			{"max_total_file_bytes", limits.MaxTotalFileBytes, totalBytes + c.bytes},
			{"max_pages_per_request", limits.MaxPagesPerRequest, totalPages + pages},
			{"max_documents_per_request", limits.MaxDocumentsPerRequest, docs + 1},
			{"max_documents_per_message", limits.MaxDocumentsPerMessage, perGroup[group] + 1},
		}
		for _, check := range checks {
			if value, set := check.limit(); set && value > 0 && check.amount > value {
				c.decision = pdfOverLimit
				c.note = pdfNoteLimit(owner, check.name, value)
				break
			}
		}
		if c.decision != pdfNative {
			continue
		}
		totalBytes += c.bytes
		totalPages += pages
		docs++
		perGroup[group]++
	}
}

// pagesLimited reports whether any page limit is stated (only then is a
// chat PDF's page count needed).
func pagesLimited(limits config.PDFLimits) bool {
	perFile, fileSet := limits.MaxPagesPerFile()
	perRequest, requestSet := limits.MaxPagesPerRequest()
	return (fileSet && perFile > 0) || (requestSet && perRequest > 0)
}

// fitPDFRequestBytes enforces max_request_bytes (FR-12) when the row states
// it: the transport measures the serialized body; while it is over, the
// oldest native PDF is demoted to text and the body re-measured. An
// all-text body still over the limit is sent as is. A request carrying a
// native PDF through a transport that cannot measure is an error, never an
// unmeasured send.
func fitPDFRequestBytes(
	in pdfAttemptInput, out []providers.Message, cands []*pdfCandidate,
	limits config.PDFLimits, owner string, build func() ([]providers.Message, error),
) ([]providers.Message, error) {
	maxBytes, set := limits.MaxRequestBytes()
	if !set || maxBytes == 0 {
		return out, nil
	}
	measurer, measurable := in.provider.(providers.RequestBodyMeasurer)
	for {
		native := -1
		for k, c := range cands {
			if c.decision == pdfNative {
				native = k
				break
			}
		}
		if native < 0 {
			return out, nil
		}
		if !measurable {
			// A stated limit is never optional (security finding F4): a
			// transport that cannot measure its body cannot carry a native
			// PDF under max_request_bytes.
			return nil, fmt.Errorf(
				"max_request_bytes is set for %s but its transport %T cannot measure its request body",
				owner, in.provider)
		}
		size, err := measurer.SerializedRequestBytes(out, in.tools, in.model, in.opts, in.stream)
		if err != nil {
			return nil, fmt.Errorf("measure the request for max_request_bytes: %w", err)
		}
		if int64(size) <= maxBytes {
			return out, nil
		}
		cands[native].decision = pdfOverLimit
		cands[native].note = pdfNoteLimit(owner, "max_request_bytes", maxBytes)
		if out, err = build(); err != nil {
			return nil, err
		}
	}
}

// buildPDFRequest returns a copy of messages presenting each candidate per
// its decision. Wire order never changes: a demoted chat PDF leaves its
// message's media and its text joins the message content; a native tool PDF
// is appended to its tool result's media; a demoted preview_document PDF is
// replaced by its page images or text. Tool and preview fallbacks take the
// request's document budget newest message first.
func (rt *agentLoopRunTurn) buildPDFRequest(
	ctx context.Context, messages []providers.Message, cands []*pdfCandidate, stage attemptStage,
) ([]providers.Message, error) {
	out := append([]providers.Message(nil), messages...)
	byMsg := map[int][]*pdfCandidate{}
	left := stage.docBudget
	for _, c := range cands {
		byMsg[c.msg] = append(byMsg[c.msg], c)
		if c.tool && c.decision == pdfNative {
			left -= int(c.bytes)
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		group, ok := byMsg[i]
		if !ok {
			continue
		}
		var used int
		var err error
		switch {
		case group[0].preview != nil:
			out[i], used, err = presentPreviewPDF(ctx, out[i], group[0], left)
		case group[0].tool:
			out[i] = presentToolPDFs(out[i], group)
			out[i], used, err = rt.attachDemotedToolPDFPages(ctx, out[i], group, stage, left)
		default:
			out[i] = presentChatPDFs(out[i], group)
		}
		if err != nil {
			return nil, err
		}
		left -= used
	}
	return out, nil
}

// presentPreviewPDF presents a preview_document PDF the stage attached: as
// attached when native; with the access-withheld marker and no media when
// its source can no longer be read; otherwise rebuilt as its page images
// (within the budget its own PDF bytes free) or its text, naming why.
func presentPreviewPDF(ctx context.Context, m providers.Message, c *pdfCandidate, left int) (providers.Message, int, error) {
	switch c.decision {
	case pdfNative:
		return m, 0, nil
	case pdfAccessDenied:
		withheld := c.preview.msg
		withheld.Content += "\n[preview_document content withheld: access to the source was denied: " + c.denied.Error() + "]"
		return withheld, 0, nil
	}
	note := c.note
	switch c.decision {
	case pdfNotCapable:
		note = "this model does not read PDFs natively"
	case pdfBudgetSpent:
		note = "the provider rejected the native PDF"
	}
	return previewFallback(ctx, *c.preview, left+len(c.preview.doc.PDF), note)
}

// attachDemotedToolPDFPages gives each tool PDF that is not native on this
// attempt (other than one whose access was refused) its page images when
// the attempt's model sees images, within the request's document budget,
// and names what the model received.
func (rt *agentLoopRunTurn) attachDemotedToolPDFPages(
	ctx context.Context, m providers.Message, group []*pdfCandidate, stage attemptStage, left int,
) (providers.Message, int, error) {
	if !stage.images {
		return m, 0, nil
	}
	used := 0
	for _, c := range group {
		if c.decision == pdfNative || c.decision == pdfAccessDenied {
			continue
		}
		pages := rt.renderToolPDFPages(ctx, c)
		next, n, line, err := attachToolPDFPages(m, pages, stage.resize, left-used)
		if err != nil {
			return m, 0, err
		}
		m = next
		m.Content += "\n" + line
		used += n
	}
	return m, used, nil
}

// presentChatPDFs presents the chat PDFs of one message (FR-13): a demoted
// PDF shows its extracted text through the per-item path, plus the note of
// why it is not native.
func presentChatPDFs(m providers.Message, group []*pdfCandidate) providers.Message {
	demoted := map[int]*pdfCandidate{}
	for _, c := range group {
		if c.decision != pdfNative {
			demoted[c.media] = c
		}
	}
	if len(demoted) == 0 {
		return m
	}
	media := make([]string, 0, len(m.Media))
	content := m.Content
	for j, mediaURL := range m.Media {
		c, gone := demoted[j]
		if !gone {
			media = append(media, mediaURL)
			continue
		}
		c.chat.inspect(c.dataURL)
		content += c.chat.injection
		if note := chatPDFNote(c); note != "" {
			content += "\n" + note
		}
	}
	m.Media = media
	m.Content = content
	return m
}

// chatPDFNote is the request-only note for a demoted chat PDF.
func chatPDFNote(c *pdfCandidate) string {
	switch c.decision {
	case pdfNotCapable:
		if c.chat.hasText {
			return ""
		}
		return pdfNoteNotCapable
	case pdfBudgetSpent:
		if c.chat.hasText {
			return pdfNoteBudgetText
		}
		if c.chat.localFailure != "" {
			return fmt.Sprintf(pdfNoteBudgetNotInspected, c.chat.localFailure)
		}
		return pdfNoteBudgetNoText
	case pdfOverLimit:
		return c.note
	case pdfAccessDenied:
		return pdfNoteAccessDenied
	}
	return ""
}

// presentToolPDFs presents the PDFs of one tool result (FR-13/FR-14): a
// native item rides the request copy as a PDF data URL with M1 as its live
// content; a demoted item keeps the persisted result plus its note. When
// every item is native, the live content is the M1 lines alone.
func presentToolPDFs(m providers.Message, group []*pdfCandidate) providers.Message {
	media := append([]string(nil), m.Media...)
	var natives, notes []string
	for _, c := range group {
		if c.decision == pdfNative {
			media = append(media, c.toolDataURL())
			natives = append(natives, pdfNoteNative(sanitizeInjectedName(fallbackName(c.item.Name, pdfFallbackName)), len(c.item.Bytes), c.item.SHA256))
			continue
		}
		if note := toolPDFNote(c); note != "" {
			notes = append(notes, note)
		}
	}
	m.Media = media
	switch {
	case len(natives) == len(group):
		m.Content = joinLines(natives)
	case len(natives)+len(notes) > 0:
		m.Content += "\n" + joinLines(append(natives, notes...))
	}
	return m
}

// toolPDFNote is the request-only note for a demoted tool PDF.
func toolPDFNote(c *pdfCandidate) string {
	switch c.decision {
	case pdfNotCapable:
		if c.item.HasText {
			return ""
		}
		return pdfNoteNotCapable
	case pdfBudgetSpent:
		if c.item.HasText {
			return pdfNoteBudgetText
		}
		if c.item.LocalFailure != "" {
			return fmt.Sprintf(pdfNoteBudgetNotInspected, c.item.LocalFailure)
		}
		return pdfNoteBudgetNoText
	case pdfAccessDenied:
		return pdfNoteAccessDenied
	case pdfOverLimit:
		return c.note
	}
	return ""
}

// joinLines joins notes one per line.
func joinLines(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}

// requestHoldsPDF reports whether a request copy would carry any PDF: a
// chat PDF data URL or a tool result with turn-private PDF items.
func (rt *agentLoopRunTurn) requestHoldsPDF(messages []providers.Message) bool {
	for _, m := range messages {
		for _, mediaURL := range m.Media {
			if protocoltypes.IsPDFDataURL(mediaURL) {
				return true
			}
		}
		if m.ToolCallID != "" && len(rt.inspectionPDFs[m.ToolCallID]) > 0 {
			return true
		}
	}
	return false
}

// refuseUncheckablePDFs is FR-3: without a loaded model list (no catalogue,
// or a catalogue without a document) nothing can say whether a model reads
// PDFs, so a request holding one is refused before any provider call.
// Requests without PDFs are unaffected.
func (rt *agentLoopRunTurn) refuseUncheckablePDFs(messages []providers.Message) error {
	if cat := rt.al.getCapabilityCatalog(); cat != nil && cat.Document() != nil {
		return nil
	}
	if !rt.requestHoldsPDF(messages) {
		return nil
	}
	provider := ""
	if len(rt.activeCandidates) > 0 {
		provider = rt.activeCandidates[0].Provider
	}
	return &pdfModelListUnavailableError{Provider: provider, Model: rt.llmModel}
}

// pdfModelListUnavailableError is FR-3's turn error: code
// model_list_unavailable with message C1 naming the model. Provider and
// Model are the attempt's identity; they become the error's facts.
type pdfModelListUnavailableError struct{ Provider, Model string }

func (e *pdfModelListUnavailableError) Error() string { return e.llmError().Message }

// llmError is the typed error the turn's error frame carries. With the
// attempt's identity known, the message is the contract's provider_message
// template with {model} filled (C1 naming the model, flagged so the bubble
// shows it, live and on replay); without it, the catalogue copy.
func (e *pdfModelListUnavailableError) llmError() LLMError {
	llm := LLMError{
		Code:      CodeModelListUnavailable,
		Message:   defaultUserMessage(CodeModelListUnavailable),
		Retryable: isRetryable(CodeModelListUnavailable),
		Provider:  e.Provider,
		Model:     e.Model,
	}
	if sentence := providerMessageFor(llm.Code, e.Provider, e.Model); sentence != "" {
		llm.Message = sentence
		llm.ProviderMessage = true
	}
	return llm
}

// pdfAttemptRecord is one provider attempt of a round (FR-16): which
// provider and model it called, whether its request carried a native PDF,
// and how it ended.
type pdfAttemptRecord struct {
	provider string
	model    string
	sentPDF  bool
	err      error
}

// recordPDFAttempt appends one attempt's record; runProviderAttempt's defer
// calls it for every attempt, in-place retries included.
func (rt *agentLoopRunTurn) recordPDFAttempt(provider, model string, sentPDF bool, err error) {
	rt.pdfAttempts = append(rt.pdfAttempts, pdfAttemptRecord{provider: provider, model: model, sentPDF: sentPDF, err: err})
}

// qualifyingPDFAttempt returns the first attempt of the round that sent a
// native PDF and failed with its own qualifying error (FR-16).
func (rt *agentLoopRunTurn) qualifyingPDFAttempt() (pdfAttemptRecord, bool) {
	for _, rec := range rt.pdfAttempts {
		if rec.sentPDF && pdfRecoveryQualifies(rec) {
			return rec, true
		}
	}
	return pdfAttemptRecord{}, false
}

// pdfRecoveryQualifies is FR-16's error rule for an attempt that sent a
// native PDF: a media rejection, a non-retriable request-format rejection
// (size, page and count rejections phrased as a 400), HTTP 413 or a context
// overflow qualify. A cancellation, a permanent model fault, auth, content
// policy, rate limit, billing and transient failures never do.
func pdfRecoveryQualifies(rec pdfAttemptRecord) bool {
	err := rec.err
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || permanentModelFailure(err) {
		return false
	}
	// A typed stream error (an error event after the provider's 200, the
	// Chat Completions stream error or a Responses content_filter refusal)
	// carries its own kind: only a request rejection may still qualify.
	// Content policy, auth, billing, rate limit and transient stream errors
	// never do (FR-16 "never policy"; security finding F3) — the generic
	// classification below maps a policy stream error to a format failure.
	var streamErr *common.StreamProviderError
	if errors.As(err, &streamErr) && streamErr.Kind() != common.StreamErrorInvalidRequest {
		return false
	}
	pe := errorToProviderError(err)
	code := classifyByProviderError(pe, err.Error())
	if pe != nil && pe.Status == 0 && pe.Body == "" {
		// No structured HTTP error is reachable: classify the error's own
		// text, so a policy, auth or rate-limit refusal named only there
		// is still excluded.
		code = classifyByMessage(err.Error())
	}
	switch code {
	case CodeContentPolicy, CodeProviderAuthFailed, CodeRateLimited, CodeQuotaBilling:
		return false
	case CodeMediaUnsupported:
		return true
	}
	if pe != nil && pe.Status == http.StatusRequestEntityTooLarge {
		return true
	}
	fe := providers.ClassifyError(err, rec.provider, rec.model)
	return fe != nil && (fe.Reason == providers.FailoverFormat || fe.Reason == providers.FailoverContextOverflow)
}

// turnChatPDFs returns the turn's chat PDF registry, creating it on first
// use (FR-15).
func (rt *agentLoopRunTurn) turnChatPDFs() *chatPDFRegistry {
	if rt.chatPDFs == nil {
		rt.chatPDFs = &chatPDFRegistry{}
	}
	return rt.chatPDFs
}
