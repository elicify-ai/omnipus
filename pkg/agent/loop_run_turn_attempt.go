// loop_run_turn_attempt.go — ONE provider attempt for callProviderOnce
// (§7.4 D3 migration, gate finding F1). Extracted so the plain path and the
// fallback-chain closure (one candidate or several) run the SAME body:
// inspection-image attach, then streaming when the provider supports it and a
// streamer exists for the channel, plain Chat otherwise. Several candidates
// stream too (effort-levels S-88/S-95, which need the primary's streamed
// usage); the chain ends instead of falling back once an attempt showed bytes
// (C-10 across candidates, pkg/providers/fallback.go).
//
// streamed is the C-10 counter the fallback chain consults through its
// ctx-carried StreamedBytesCheck: the caller resets it at attempt entry and
// this body adds every visible delta BEFORE forwarding it to the streamer,
// so "bytes already flowed this attempt" is fail-closed.

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// runProviderAttempt runs ONE attempt against p/model: image attach, then
// streaming when supported, plain Chat otherwise. providerName feeds the
// image resize budget (the candidate's pinned provider name); model is the
// model this attempt calls with. streamed must be non-nil.
func (rt *agentLoopRunTurn) runProviderAttempt(
	ctx context.Context,
	p providers.LLMProvider,
	providerName string,
	model string,
	messagesForCall []providers.Message,
	toolDefsForCall []providers.ToolDefinition,
	streamed *atomic.Int64,
	opts map[string]any,
) (response *providers.LLMResponse, err error) {
	sentPDF := false
	defer func() {
		rt.accountProviderAttempt(providerName, model, response, err)
		rt.recordPDFAttempt(providerName, model, sentPDF, err)
	}()
	// Use streaming if the provider supports it and we have a streamer for this channel.
	// U8 (FR-028): never stream to a connector from a main - which sender would it be?
	// The lookup runs before the request is prepared because the PDF
	// presenter measures the exact body this attempt sends (issue #1968
	// FR-12), and a streamed body differs from a plain one.
	var streamer bus.Streamer
	hasStreamer := false
	sp, canStream := p.(providers.StreamingProvider)
	if canStream && rt.al.bus != nil && !rt.al.mainConnectorTurn(rt.ts.channel, rt.ts.transcriptSessionID) {
		logger.DebugCF("agent", "Provider supports streaming, checking for streamer", map[string]any{"channel": rt.ts.channel, "chat_id": rt.ts.chatID})
		streamer, hasStreamer = rt.al.bus.GetStreamer(ctx, rt.ts.streamerChannel(), rt.ts.chatID, rt.ts.transcriptSessionID)
		rt.streamerLookupDeclined = !hasStreamer
	}
	prepared, err := rt.prepareAttemptRequest(ctx, p, providerName, model, messagesForCall, toolDefsForCall, opts, hasStreamer)
	if err != nil {
		return nil, err
	}
	messagesForCall, sentPDF = prepared.messages, prepared.sentPDF
	rt.ts.setLastProducedModel(model)
	rt.ts.setLastProducedProvider(providerName)
	if hasStreamer {
		logger.InfoCF("agent", "Using streaming for response", map[string]any{"channel": rt.ts.channel, "chat_id": rt.ts.chatID})
		// FIX 5a/5c: stamp the TRUE per-turn producer and this turn's own
		// ID before any token can flow — see stampStreamerProducerAgentID
		// and stampStreamerTurnID's doc comments. stampStreamerParentSpawnCallID
		// additionally stamps this turn's delegation-nesting correlation
		// (empty for a root turn) so a delegate's own streamed final
		// response round-trips through Finalize with the same
		// ParentSpawnCallID its non-streaming siblings carry — see its
		// own doc comment.
		rt.ts.stampStreamerProducerAgentID(streamer)
		rt.ts.stampStreamerTurnID(streamer)
		rt.ts.stampStreamerParentSpawnCallID(streamer)
		// session-core FR-039 / C-GOAL: stamp the turn's captured goal id
		// so the live TokenFrame/DoneFrame and the persisted entry join to
		// the EXACT keyed goal criteria. Empty (no proven goal) is a valid
		// no-op — see stampStreamerGoalID's doc comment.
		rt.ts.stampStreamerGoalID(streamer)
		// #823: mint (or, for an ADR-087 D6 auto-continue round, reuse)
		// this round's message id BEFORE any token can flow — mirrors the
		// three stamps immediately above. nextRoundMessageID must run
		// before the stamp so the freshly-obtained streamer and this
		// round's later appendIntermediateAssistantTranscript call (if
		// this round ends in tool calls) agree on the SAME id. Under the
		// chain (F1) each attempt re-runs this: a retried attempt mints a
		// fresh id only after a failed attempt that streamed nothing
		// (C-10 blocks any retry once bytes flowed), so no id was ever
		// visible to anyone.
		rt.ts.nextRoundMessageID()
		rt.ts.stampStreamerMessageID(streamer)
		// #1534: register this round's streamer as the turn's last one
		// BEFORE any token flows, so a Stop claimed mid-stream
		// (claimCancel → CutoffAtStop) cuts the streamer that is
		// actually receiving the deltas, and the tail flush below runs
		// after that cut.
		rt.ts.setLastStreamer(streamer)
		var lastChunk string
		// #1534: the visible text this attempt showed before a Stop
		// claim — the part of a stopped answer the turn must keep.
		var shownBeforeStop string
		// Residual native tool-call markup must never reach the
		// live view. This is not only a rendering concern: the
		// gateway streamer PERSISTS what it accumulated from these
		// Update calls (wsStreamer.Finalize prefers its own buffer
		// over the turn's final content), so anything forwarded
		// here also lands in transcript.jsonl. Filtering at this
		// seam is what keeps the live bubble and the persisted
		// entry identical — and both clean. See
		// providers.StreamTextFilter.
		var streamFilter providers.StreamTextFilter
		resp, streamErr := sp.ChatStream(ctx, messagesForCall, toolDefsForCall, model, opts, func(accumulated string) {
			// B4: if the turn has been abandoned (stuck-goroutine detach),
			// suppress further frame emits so a zombie goroutine cannot
			// push frames to disconnected clients.
			if rt.ts.abandoned.Load() {
				abandonedWritesSuppressed.Add(1)
				return
			}
			visible := streamFilter.Visible(accumulated)
			// Send only the new delta (visible minus what we already sent).
			//
			// Defensive: this slice panics with index-out-of-range if a
			// provider ever emits an accumulated string SHORTER than its
			// predecessor. The contract is monotonic growth, but a provider
			// bug, a block reorder, or an SDK revision changing accumulation
			// semantics would otherwise take down the whole turn. Treat a
			// non-growing value as "nothing new" and skip it. The filter
			// upholds the same non-shrinking contract on its own output.
			if len(visible) < len(lastChunk) {
				logger.DebugCF("agent", "Streaming callback emitted a shorter accumulated string; ignoring", map[string]any{
					"previous_len": len(lastChunk),
					"new_len":      len(visible),
				})
				return
			}
			delta := visible[len(lastChunk):]
			lastChunk = visible
			if delta != "" {
				// This attempt-local counter is independent of the concrete
				// streamer. Some channels expose no buffer-length method, and
				// WebSocket creates a fresh streamer per round. Count before
				// Update so an attempted partial emit fails closed even if the
				// client disconnects during the write.
				streamed.Add(int64(len(delta)))
				rt.ts.recordAttemptShown(&shownBeforeStop, delta)
				if err := streamer.Update(ctx, delta); err != nil {
					logger.DebugCF("agent", "Streaming update error (client may have disconnected)", map[string]any{"error": err.Error()})
				}
			}
		}, rt.onToolCallProgress)
		// Reconcile against the provider's final text. Two things
		// need this: the few bytes the filter holds back mid-stream
		// in case they start a marker split across SSE chunks, and
		// a provider that returned content without ever invoking
		// the callback. Without it those bytes would be dropped
		// silently — the streamer's buffer is what gets persisted.
		if streamErr == nil && resp != nil && !rt.ts.abandoned.Load() {
			finalVisible := resp.Content
			if om, isOrphan := providers.DetectOrphanToolCallMarkup(finalVisible); isOrphan {
				finalVisible = om.Prose
			}
			if len(finalVisible) > len(lastChunk) && strings.HasPrefix(finalVisible, lastChunk) {
				rt.ts.recordAttemptShown(&shownBeforeStop, finalVisible[len(lastChunk):])
				if err := streamer.Update(ctx, finalVisible[len(lastChunk):]); err != nil {
					logger.DebugCF("agent", "Streaming tail flush error (client may have disconnected)", map[string]any{"error": err.Error()})
				}
			}
		}
		// Do NOT finalize here — the turn may continue with tool calls.
		// Store the streamer so the turn-level code can finalize once,
		// after the last LLM call, preventing premature "done" frames
		// that tell the frontend the response is complete mid-turn.
		// (Registered as the turn's last streamer before streaming.)
		rt.ts.setLastProducedModel(model)
		rt.ts.setLastProducedProvider(providerName)
		// FR-013: also push to the streamer so Finalize stamps the
		// per-turn Model field on the streamed assistant entry.
		rt.ts.markLastStreamerProducedModel(model)
		rt.ts.setStopShown(shownBeforeStop, streamErr == nil)
		return resp, streamErr
	}
	return p.Chat(ctx, messagesForCall, toolDefsForCall, model, opts)
}

// prepareAttemptRequest builds the request copy one attempt sends: this
// candidate's inspection images attached and fitted to its budget, then its
// PDFs presented natively or as text (issue #1968, pdf_present.go). §7.4
// (read paths): a round reuses the preparation for the same candidate — the
// in-place retry re-sends the same request, so the attach (and its per-call
// Reauthorize) runs once per candidate, not once per call (the
// inspection-media boundary test pins 2 candidates → 2 rechecks). The key
// also holds the PDF budget state and the catalogue snapshot, so a changed
// decision input never reuses a stale request. Reading a nil cache is safe;
// a nil cache (delegated retry path) prepares per call. A failed
// preparation is not cached.
func (rt *agentLoopRunTurn) prepareAttemptRequest(
	ctx context.Context,
	p providers.LLMProvider,
	providerName, model string,
	messages []providers.Message,
	toolDefs []providers.ToolDefinition,
	opts map[string]any,
	stream bool,
) (attemptPresentation, error) {
	cat := rt.al.getCapabilityCatalog()
	var snapshot *catalog.Document
	if cat != nil {
		snapshot = cat.Document()
	}
	attachKey := fmt.Sprintf("%s/%s|budget=%t|snapshot=%p", providerName, model, rt.ts.mediaRetryDone.Load(), snapshot)
	stage, cached := rt.providerCallAttachCache[attachKey]
	if !cached {
		var err error
		if stage, err = rt.prepareAttemptStage(ctx, cat, providerName, model, messages); err != nil {
			return attemptPresentation{}, err
		}
		if rt.providerCallAttachCache != nil {
			rt.providerCallAttachCache[attachKey] = stage
		}
	}
	// PDFs are presented on EVERY call, in-place retries included, and
	// never cached: a tool PDF's access recheck is an authorization
	// decision for this one send (FR-10/FR-13; security finding F2). A
	// retry after access was revoked sends the access-denied note instead
	// of the bytes captured before. The presenter never mutates its input,
	// so the cached image stage is reused safely.
	messages, sentPDF, err := rt.presentAttemptPDFs(ctx, pdfAttemptInput{
		provider: p, name: providerName, model: model, tools: toolDefs, opts: opts, stream: stream,
	}, stage)
	if err != nil {
		return attemptPresentation{}, err
	}
	return attemptPresentation{messages: messages, sentPDF: sentPDF}, nil
}

// prepareAttemptStage is the cached part of one candidate's request: its
// inspection images; for a model that sees images but does not read PDFs,
// the uploaded PDFs' page images (taken first from the request's 32 MiB
// document budget); this turn's preview_document forms for this model
// (office-preview spec section 4.1); and the ordinary image fit.
func (rt *agentLoopRunTurn) prepareAttemptStage(
	ctx context.Context, cat *catalog.Catalog, providerName, model string, messages []providers.Message,
) (attemptStage, error) {
	budget := resizeBudgetForModel(cat, providerName, model, int(catalog.DefaultResizeLimits.MaxBytes))
	images := modelSupportsImage(cat, providerName, model)
	attached, err := attachTurnInspectionImagesWithBudget(ctx, messages, rt.inspectionImages, images, budget)
	if err != nil {
		return attemptStage{}, err
	}
	attached, rt.turnUploadMedia.pageImageBytes = rt.attachUploadPageImages(ctx, attached, cat, providerName, model, budget)
	live, previews, docBudget, err := attachTurnDocumentPreviewsFor(ctx, attached, rt.documentPreviews, rt.previewForms(cat, providerName, model, budget))
	if err != nil {
		return attemptStage{}, err
	}
	// Issue #1435 R1: ordinary inline image media (fresh tool results
	// and history replays alike) must fit THIS candidate's budget at
	// the actual send boundary — admission fitting used the tool
	// round's selection, which can be looser than the candidate now
	// being attempted. Request-local copy; canonical history bytes are
	// untouched (the R1 send matrix pins that).
	return attemptStage{
		messages:  fitOrdinaryImageMediaToBudget(live, budget),
		previews:  previews,
		docBudget: docBudget,
		images:    images,
		resize:    budget,
	}, nil
}
