// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Unroutable-message dispatch for the agent loop. This code moved here from
// Run's `if !ok` branch (pure move, no behavior change) so the
// check-function-budget FAIL (AgentLoop.Run 244 > 240) could be held without
// touching the ADR-093 inboundCtx wiring — pkg/agent/CLAUDE.md: new code
// belongs in a sibling file, not appended to the pinned loop.go.
package agent

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// dispatchUnroutableMessage is the single-shot dispatcher for a message with
// no resolvable steering target: processMessage, ADR-051 error translation,
// and the C8 terminal-frame guarantee, tracked in activeRequests (#265) so
// shutdown drains it. Run launches it as
//
//	al.activeRequests.Add(1)
//	go al.dispatchUnroutableMessage(runCtx, msg)
func (al *AgentLoop) dispatchUnroutableMessage(runCtx context.Context, msg bus.InboundMessage) {
	defer al.activeRequests.Done()

	var response string
	var ag *AgentInstance
	published := false

	// Outer recover — preserved from before this fix. This is the
	// only supervising recover for this bare dispatcher goroutine
	// (unlike session_worker.go's runLoop→processTurn split, there
	// is no outer layer above it), so it must keep swallowing the
	// panic (log-and-exit-goroutine) rather than re-panicking again
	// — doing so would crash the whole gateway process, not just
	// this one message.
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorCF("agent", "Panic in unroutable-message goroutine",
				map[string]any{
					"panic":   r,
					"channel": msg.Channel,
					"chat_id": msg.ChatID,
					"stack":   string(debug.Stack()),
				})
		}
	}()

	// C8 (chat-stream-hang): guarantee a terminal frame on EVERY
	// exit path, including panic-recover — mirrors the per-session
	// pattern in session_worker.go's processTurn defer. processMessage
	// can panic (e.g. a provider/tool nil-deref that escapes the inner
	// recovers); when it does, response is still "" and nothing is
	// ever published, leaving the SPA stuck "thinking" forever for
	// unroutable-path messages too.
	//
	// Registered AFTER the outer recover above so that — defers
	// being LIFO — THIS recover runs FIRST during unwinding: it
	// synthesizes an error response and force-publishes it via a
	// fresh bounded-timeout context (runCtx may be canceled during
	// panic unwinding), then re-panics so the outer recover above
	// still logs the "Panic in unroutable-message goroutine" event
	// exactly as before this fix.
	defer func() {
		if r := recover(); r != nil {
			// Log the ORIGINAL panic (with stack) BEFORE attempting the
			// force-publish below. If publishResponseIfNeeded (or anything
			// else in this block) itself panics, that new panic — not this
			// log call — is what would otherwise reach the outer recover's
			// log line, silently discarding the true root cause (the real
			// panic from processMessage) behind a confusing secondary
			// symptom. Logging first guarantees the root cause is always
			// on record, no matter what happens next.
			logger.ErrorCF("agent", "Panic in processMessage — emitting terminal error frame",
				map[string]any{
					"panic":   r,
					"channel": msg.Channel,
					"chat_id": msg.ChatID,
					"stack":   string(debug.Stack()),
				})
			if response == "" {
				response = "Error processing message: the agent turn failed unexpectedly. Please try again."
			}
			if !published {
				// Isolate the force-publish in its own recover so a panic
				// here (e.g. a bug in al.bus / publishResponseIfNeeded)
				// cannot prevent the panic(r) re-throw below — the outer
				// recover must always see the ORIGINAL panic value r, never
				// a secondary symptom from this publish attempt.
				func() {
					defer func() {
						if pr := recover(); pr != nil {
							logger.ErrorCF(
								"agent",
								"Panic while force-publishing terminal frame for unroutable-message panic",
								map[string]any{
									"panic":   pr,
									"channel": msg.Channel,
									"chat_id": msg.ChatID,
								},
							)
						}
					}()
					termCtx, termCancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer termCancel()
					al.publishResponseIfNeeded(termCtx, ag, msg.Channel, msg.ChatID, response)
				}()
				published = true
			}
			panic(r)
		}
	}()

	var err error
	response, ag, err = al.processMessage(runCtx, msg)
	if err != nil && response == "" {
		// ADR-051 §RD5: never surface raw err text in the assistant-facing
		// reply. Route through the classifier so provider-originated body /
		// status / model identity is replaced with the typed copy.
		// The raw err stays in the defer's log line for operator triage.
		//
		// TranslateTurnError, not TranslateLLMError(nil, err.Error()):
		// passing the error VALUE keeps the sentinels intact, so a turn
		// refused for a known reason (agent on no workspace) says so
		// instead of falling to the "we can't tell why" copy.
		response = TranslateTurnError(err).Message
	}
	if response != "" {
		al.publishResponseIfNeeded(runCtx, ag, msg.Channel, msg.ChatID, response)
		published = true
	}
}
