// loop_run_turn_attempt.go — ONE provider attempt for callProviderOnce
// (§7.4 D3 migration, gate finding F1). Extracted so the plain path and the
// single-candidate fallback-chain closure run the SAME body: inspection-image
// attach, then streaming when the provider supports it and a streamer exists
// for the channel, plain Chat otherwise. The multi-candidate chain closure
// stays non-streaming p.Chat — streaming a fallback candidate was never
// designed, and that path must stay byte-identical to pre-F1.
//
// streamed is the C-10 counter the fallback chain consults through its
// ctx-carried StreamedBytesCheck: the caller resets it at attempt entry and
// this body adds every visible delta BEFORE forwarding it to the streamer,
// so "bytes already flowed this attempt" is fail-closed.

package agent

import (
	"context"
	"strings"
	"sync/atomic"

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
) (*providers.LLMResponse, error) {
	var imageErr error
	cat := rt.al.getCapabilityCatalog()
	budget := resizeBudgetForModel(cat, providerName, model, int(catalog.DefaultResizeLimits.MaxBytes))
	// §7.4 (read paths): reuse this round's attach for this candidate — the
	// in-place retry re-sends the same request, so the attach (and its
	// per-call Reauthorize) runs once per candidate, not once per call
	// (the inspection-media boundary test pins 2 candidates → 2 rechecks).
	// Reading a nil cache is safe; a nil cache (delegated retry path) keeps
	// the attach-per-call behavior. Cache-miss on ERROR: a failed attach is
	// re-attempted on the next call, as before.
	attachKey := providerName + "/" + model
	if cached, ok := rt.providerCallAttachCache[attachKey]; ok {
		messagesForCall = cached
	} else {
		messagesForCall, imageErr = attachTurnInspectionImagesWithBudget(ctx, messagesForCall, rt.inspectionImages, modelSupportsImage(cat, providerName, model), budget)
		if imageErr != nil {
			return nil, imageErr
		}
		if rt.providerCallAttachCache != nil {
			rt.providerCallAttachCache[attachKey] = messagesForCall
		}
	}
	// Use streaming if the provider supports it and we have a streamer for this channel.
	if sp, ok := p.(providers.StreamingProvider); ok && rt.al.bus != nil {
		logger.DebugCF("agent", "Provider supports streaming, checking for streamer", map[string]any{"channel": rt.ts.channel, "chat_id": rt.ts.chatID})
		if streamer, hasStreamer := rt.al.bus.GetStreamer(ctx, rt.ts.channel, rt.ts.chatID, rt.ts.transcriptSessionID); hasStreamer {
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
			var lastChunk string
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
			resp, streamErr := sp.ChatStream(ctx, messagesForCall, toolDefsForCall, model, rt.llmOpts, func(accumulated string) {
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
					if err := streamer.Update(ctx, finalVisible[len(lastChunk):]); err != nil {
						logger.DebugCF("agent", "Streaming tail flush error (client may have disconnected)", map[string]any{"error": err.Error()})
					}
				}
			}
			// Do NOT finalize here — the turn may continue with tool calls.
			// Store the streamer so the turn-level code can finalize once,
			// after the last LLM call, preventing premature "done" frames
			// that tell the frontend the response is complete mid-turn.
			rt.ts.setLastStreamer(streamer)
			rt.ts.setLastProducedModel(model)
			// FR-013: also push to the streamer so Finalize stamps the
			// per-turn Model field on the streamed assistant entry.
			rt.ts.markLastStreamerProducedModel(model)
			return resp, streamErr
		}
	}
	rt.ts.setLastProducedModel(model)
	return p.Chat(ctx, messagesForCall, toolDefsForCall, model, rt.llmOpts)
}
