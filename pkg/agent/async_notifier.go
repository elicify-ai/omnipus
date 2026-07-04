// Omnipus - Ultra-lightweight personal AI agent
// Built on Omnipus's foundation. See CLAUDE.md for project lineage.
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// asyncNotifyMaxContentBytes mirrors pkg/tools/session.go's maxOutputBufferSize
// (1MB) exactly (FR-N8, async-notifier-spec.md). This is deliberately the SAME
// cap `exec`'s existing background-output buffering already uses — NOT the
// unrelated 50,000-byte pkg/tools/web.go defaultMaxChars constant, which
// bounds web-fetch content and has nothing to do with async notifications.
const asyncNotifyMaxContentBytes = 1 * 1024 * 1024 // 1MB

// asyncNotifyTruncateMarker mirrors pkg/tools/session.go's outputTruncateMarker
// exactly, so a truncated async notification reads identically to a truncated
// background shell buffer.
const asyncNotifyTruncateMarker = "\n... [output truncated, exceeded 1MB]\n"

// AsyncNotifyEvent describes a single "background work finished, wake the
// conversation" event. It is the structured payload AsyncNotifier.Notify
// accepts, generalized from pkg/agent/loop.go's formerly-inline asyncCallback
// closure (the only place this behavior existed before this extraction).
//
// Metadata is an intentionally open, currently-unused bag (FR-N4): it exists
// so a future producer or consumer (the not-yet-specced Goals feature) can
// carry additional context without changing Notify's signature. No producer
// populates it today and Notify does not read from it — see async-notifier-
// spec.md's Assumptions section ("ships with an empty Metadata bag").
type AsyncNotifyEvent struct {
	// Channel is the originating channel (e.g. "telegram", "cli", "slack").
	Channel string
	// ChatID is the originating chat/conversation identifier on Channel.
	ChatID string
	// AgentID is the agent whose turn produced the background work.
	AgentID string
	// SourceKind identifies the producer (e.g. "spawn", "bash", "delegate").
	// Composed into the synthetic inbound message's sender CanonicalID as
	// "async:<SourceKind>", exactly matching today's convention.
	SourceKind string
	// Content is the text to relay back into the conversation as a new turn.
	// May be empty (e.g. a silent kill with nothing to report) — Notify does
	// not treat empty Content as an error, only empty Channel/ChatID are
	// rejected (FR-N7).
	Content string
	// Metadata is an open, currently-unused extension point (FR-N4). See the
	// type-level doc comment above.
	Metadata map[string]any
}

// AsyncNotifier is the reusable "wake the conversation when background work
// finishes" primitive extracted from pkg/agent/loop.go's inline asyncCallback
// closure (async-notifier-spec.md). It is purely a message-delivery
// mechanism: Notify performs NO authorization or capability-granting of its
// own (FR-N10) — the only capability gate is the receiving turn's normal
// tool-policy evaluation, which is entirely unaffected by whether a turn was
// triggered by a genuine user message or by a Notify-originated one. Treat
// this as a load-bearing security property: no code path here or in a
// Notify-triggered turn may ever treat "this turn came from AsyncNotifier" as
// a reason to widen, grant, or bypass any tool's policy verdict.
type AsyncNotifier interface {
	// Notify delivers event as a new turn on event.Channel/event.ChatID. It
	// rejects an empty Channel or ChatID before attempting any publish
	// (FR-N7), truncates Content to 1MB with a truncation notice (FR-N8),
	// offers a copy of the event to every registered observer independent of
	// the publish outcome (FR-N5/FR-N6), and returns any publish error to the
	// caller rather than swallowing it.
	Notify(ctx context.Context, event AsyncNotifyEvent) error
}

// asyncNotifierImpl is the concrete, process-wide AsyncNotifier implementation
// held as a single instance on AgentLoop (async-notifier-spec.md
// Clarifications: "a single AsyncNotifier instance held on AgentLoop,
// matching how d0f65482's ApprovalGrantStore is scoped to the loop rather
// than per-connection"). loop is used to reach the message bus, the current
// config (for sensitive-data filtering), and event emission; it may be nil in
// tests that construct an asyncNotifierImpl directly, in which case Notify
// degrades to observer-only delivery and reports a publish error (there is no
// bus to publish to).
type asyncNotifierImpl struct {
	loop *AgentLoop

	mu        sync.RWMutex
	observers []func(AsyncNotifyEvent)
}

var _ AsyncNotifier = (*asyncNotifierImpl)(nil)

// newAsyncNotifier constructs the process-wide AsyncNotifier for loop.
func newAsyncNotifier(loop *AgentLoop) *asyncNotifierImpl {
	return &asyncNotifierImpl{loop: loop}
}

// registerObserver lets one or more observers receive a copy of every Notify
// event, independent of the bus-publish outcome (FR-N5). This is deliberately
// provisional, unexported infrastructure for a future Goals feature (FR-N4's
// caveat applies here too) — nothing outside this package calls it yet.
// Registration is additive: registering the same observer twice calls it
// twice per event (documented caller responsibility, not deduplicated).
// Safe to call concurrently with Notify and with other registerObserver
// calls.
func (n *asyncNotifierImpl) registerObserver(fn func(AsyncNotifyEvent)) {
	if fn == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.observers = append(n.observers, fn)
}

// notifyObservers offers event to every registered observer. Each observer
// call is individually recovered so one observer's panic never affects
// another observer or the core wake behavior (FR-N6). When no observers are
// registered this is a no-op single RLock/RUnlock pair — behavior is
// identical to the mechanism not existing at all.
func (n *asyncNotifierImpl) notifyObservers(event AsyncNotifyEvent) {
	n.mu.RLock()
	observers := n.observers
	n.mu.RUnlock()

	for _, obs := range observers {
		n.callObserver(obs, event)
	}
}

// callObserver invokes a single observer with panic recovery isolated to
// that call.
func (n *asyncNotifierImpl) callObserver(obs func(AsyncNotifyEvent), event AsyncNotifyEvent) {
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorCF("agent", "AsyncNotifier observer panicked; recovered",
				map[string]any{
					"source":  event.SourceKind,
					"channel": event.Channel,
					"panic":   fmt.Sprintf("%v", r),
				})
		}
	}()
	obs(event)
}

// truncateAsyncNotifyContent bounds content to asyncNotifyMaxContentBytes,
// appending asyncNotifyTruncateMarker when truncation occurs — the exact
// convention pkg/tools/session.go's background-output buffering already
// uses: content at or under the cap is returned untouched; content over the
// cap is cut to the cap and the marker is appended once.
func truncateAsyncNotifyContent(content string) string {
	if len(content) <= asyncNotifyMaxContentBytes {
		return content
	}
	return content[:asyncNotifyMaxContentBytes] + asyncNotifyTruncateMarker
}

// asyncNotifyMetaKeyType is the context key type for asyncNotifyEventMetaFromContext.
type asyncNotifyMetaKeyType struct{}

var asyncNotifyMetaKey = asyncNotifyMetaKeyType{}

// withAsyncNotifyEventMeta lets a call site with an active turn scope (e.g.
// loop.go's asyncCallback, which knows the originating turnEventScope) supply
// the exact EventMeta (TurnID/SessionKey/Iteration included) that Notify's
// EventKindFollowUpQueued emission should carry — preserving today's event
// shape byte-for-byte across the extraction. This mirrors the existing
// withSpawnToolCallID context-injection pattern (turn.go) rather than adding
// turn-scoped fields to AsyncNotifyEvent itself, which FR-N4 deliberately
// keeps generic/stable for producers (bash, delegate) that have no turn scope
// of their own to offer.
func withAsyncNotifyEventMeta(ctx context.Context, meta EventMeta) context.Context {
	return context.WithValue(ctx, asyncNotifyMetaKey, meta)
}

// asyncNotifyEventMetaFromContext returns the EventMeta injected via
// withAsyncNotifyEventMeta, or a minimal AgentID-only EventMeta when the
// caller supplied none (a producer with no active turn scope, e.g. a future
// bash background-completion path).
func asyncNotifyEventMetaFromContext(ctx context.Context, fallbackAgentID string) EventMeta {
	if meta, ok := ctx.Value(asyncNotifyMetaKey).(EventMeta); ok {
		return meta
	}
	return EventMeta{
		AgentID:   fallbackAgentID,
		Source:    "runTurn",
		TracePath: "turn.follow_up.queued",
	}
}

// Notify implements AsyncNotifier. See the interface doc comment for the
// contract; this is the single reusable implementation of the behavior that
// used to live only inline in pkg/agent/loop.go's asyncCallback closure.
func (n *asyncNotifierImpl) Notify(ctx context.Context, event AsyncNotifyEvent) error {
	// FR-N7: reject an ambiguous destination before attempting any publish.
	if event.Channel == "" || event.ChatID == "" {
		return fmt.Errorf(
			"async notifier: refusing to publish with empty destination (channel=%q chatID=%q source=%q)",
			event.Channel, event.ChatID, event.SourceKind,
		)
	}

	// FR-N8: bound content to the shared 1MB cap before it goes anywhere.
	content := truncateAsyncNotifyContent(event.Content)

	// Sensitive-data filtering, matching the original asyncCallback's
	// behavior exactly (FR-N1) — the LLM must never see its own credentials
	// echoed back through a background tool's output.
	if n.loop != nil {
		if cfg := n.loop.GetConfig(); cfg != nil {
			content = cfg.FilterSensitiveData(content)
		}
	}
	event.Content = content

	// FR-N10: no authorization happens here. This is purely a message-
	// delivery primitive — the receiving turn's normal tool-policy
	// evaluation is the sole capability gate, unaffected by anything below.

	// FR-N5/FR-N6: observers see every event, independent of publish outcome,
	// with panic isolation. When no observers are registered this is a
	// single RLock/RUnlock with a zero-length loop — no other cost.
	n.notifyObservers(event)

	if n.loop != nil {
		n.loop.emitEvent(
			EventKindFollowUpQueued,
			asyncNotifyEventMetaFromContext(ctx, event.AgentID),
			FollowUpQueuedPayload{
				SourceTool: event.SourceKind,
				Channel:    event.Channel,
				ChatID:     event.ChatID,
				ContentLen: len(event.Content),
			},
		)
	}

	logger.InfoCF("agent", "Async notification publishing result",
		map[string]any{
			"source":      event.SourceKind,
			"content_len": len(event.Content),
			"channel":     event.Channel,
		})

	pubCtx, pubCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer pubCancel()

	var publishErr error
	if n.loop != nil && n.loop.bus != nil {
		publishErr = n.loop.bus.PublishInbound(pubCtx, bus.InboundMessage{
			Channel: "system",
			Sender: bus.SenderInfo{
				CanonicalID: fmt.Sprintf("async:%s", event.SourceKind),
			},
			ChatID:  fmt.Sprintf("%s:%s", event.Channel, event.ChatID),
			Content: event.Content,
		})
	} else {
		publishErr = fmt.Errorf("async notifier: no message bus available (source %q)", event.SourceKind)
	}

	if publishErr != nil {
		logger.ErrorCF("agent", "Failed to publish async notification; result permanently lost",
			map[string]any{
				"source":  event.SourceKind,
				"channel": event.Channel,
				"chat_id": event.ChatID,
				"error":   publishErr.Error(),
			})
		return fmt.Errorf("async notifier: publish failed for source %q: %w", event.SourceKind, publishErr)
	}

	return nil
}
