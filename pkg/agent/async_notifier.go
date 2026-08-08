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

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/tools"
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
	// TranscriptSessionID is the transcript-store session ID the originating
	// turn was persisting to (turnState.transcriptSessionID). FIX 5d: threaded
	// through so the new turn Notify triggers persists into the SAME session
	// the originating turn was writing to, instead of leaving the reconstructed
	// turn with no transcript binding at all — which made persistence depend
	// entirely on a live WebSocket connection still being open by the time the
	// async result lands (see docs/internal/architecture/subturn.md). Empty
	// when the producer had no transcript-bound turn (should not happen for a
	// live turn today, but Notify treats it the same as any other unset field
	// — best-effort, never a hard error).
	TranscriptSessionID string
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

	// wakeMu/wakeWindows back WakeParent's bounded typed-wake debounce/rate
	// cap (ADR-053 S3 — GENERALIZATION of this file's existing terminal-only
	// wake to a bounded typed wake for question/blocker/error/handback).
	// Deliberately a SEPARATE mutex from mu (observers) — WakeParent's hot
	// path never needs to touch the observer list.
	wakeMu      sync.Mutex
	wakeWindows map[string][]time.Time // key = channel + "\x00" + chatID
	wakeNow     func() time.Time       // overridable for deterministic tests
}

var _ AsyncNotifier = (*asyncNotifierImpl)(nil)

// newAsyncNotifier constructs the process-wide AsyncNotifier for loop.
func newAsyncNotifier(loop *AgentLoop) *asyncNotifierImpl {
	return &asyncNotifierImpl{
		loop:        loop,
		wakeWindows: make(map[string][]time.Time),
		wakeNow:     time.Now,
	}
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
	// single RLock/RUnlock with a zero-length loop — no other cost. This is
	// intentionally NOT gated on publish success — the observer channel is
	// documented (FR-N5/FR-N6) to receive a copy regardless of outcome,
	// unlike the event-bus EventKindFollowUpQueued emission below.
	n.notifyObservers(event)

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
			// FIX 5d: dedicated carriers so processSystemMessage can resolve
			// the TRUE originating agent and transcript session instead of
			// guessing GetDefaultAgent() / leaving the reconstructed turn with
			// no transcript binding at all. See bus.InboundMessage's doc
			// comments on these two fields for the full rationale.
			AsyncOriginAgentID:       event.AgentID,
			AsyncTranscriptSessionID: event.TranscriptSessionID,
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

	// EventKindFollowUpQueued is emitted only now, after PublishInbound has
	// actually succeeded. Previously this fired unconditionally before the
	// publish attempt, so a failed publish still left the event bus showing
	// "queued" with no compensating correction — misleading to any future
	// event-bus consumer (e.g. a Goals feature) even though the failure was
	// correctly logged and returned to the caller above.
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

	return nil
}

// ====================== ADR-053 S3: Bounded Typed Wake ======================
//
// GENERALIZATION of this file's existing terminal-only wake (BOM
// discipline, delivery brief DoD-11): WakeParent is the SAME Notify
// mechanism above, gated in front by (a) a closed kind allow-list
// (question/blocker/error/handback only — progress/checkpoint/artifact
// never wake a new turn; they are inbox-only, read on the parent's own
// schedule via delegate.inbox/peek) and (b) a debounce (>=15s between wakes
// for the same parent conversation) + rate cap (<=4/h) so a noisy child
// cannot flood the parent with a fresh turn per message. A message that is
// debounced/rate-limited is NOT lost — it is still durably appended to the
// inbox (pkg/session.MessageInboxStore, by the caller before ever reaching
// here); only the "wake a new turn right now" side effect is suppressed.

const (
	// sessionMessageWakeDebounce is the minimum interval between two wakes
	// for the same parent conversation (ADR-053 §Contract Surface).
	sessionMessageWakeDebounce = 15 * time.Second
	// sessionMessageWakeMaxPerHour is the rolling-hour wake cap per parent
	// conversation.
	sessionMessageWakeMaxPerHour = 4
)

// wakeableSessionMessageKinds is the closed set of SessionMessage `kind`
// values that may trigger a bounded typed wake.
var wakeableSessionMessageKinds = map[string]bool{ //nolint:gochecknoglobals
	"question": true,
	"blocker":  true,
	"error":    true,
	"handback": true,
}

// SetWakeClock overrides WakeParent's time source for deterministic
// (fake-clock) tests of the debounce/rate cap. Never call this outside
// tests.
func (n *asyncNotifierImpl) SetWakeClock(now func() time.Time) {
	if now != nil {
		n.wakeNow = now
	}
}

// wakeWindowSweepThreshold bounds wakeWindows' key count before an
// opportunistic full-map sweep removes fully-quiescent keys (14-reviewer
// LOW finding, mirroring pkg/session/message_inbox.go's identical
// rateWindows leak and fix): every distinct channel+chatID pair that ever
// triggered even one bounded typed wake left a permanent map entry, since
// allowWake only ever prunes/rewrites the ONE key it was called for — a key
// belonging to a parent conversation that never wakes again is never
// revisited by any future call, so it survives forever holding a handful of
// now-expired timestamps. A `var` (not `const`) so an in-package test can
// lower it without needing hundreds of real distinct conversations.
var wakeWindowSweepThreshold = 512

// pruneWakeWindow returns window with every timestamp at or before cutoff
// removed, reusing window's backing array (same convention allowWake always
// used).
func pruneWakeWindow(window []time.Time, cutoff time.Time) []time.Time {
	kept := window[:0]
	for _, t := range window {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	return kept
}

// sweepQuiescentWakeWindows prunes every window in windows against cutoff
// and deletes any key whose pruned window is now empty — the actual fix for
// the "never delete quiescent keys" leak, since a single key's own
// allowWake call can only ever observe or clean up ITSELF.
func sweepQuiescentWakeWindows(windows map[string][]time.Time, cutoff time.Time) {
	for k, w := range windows {
		kept := pruneWakeWindow(w, cutoff)
		if len(kept) == 0 {
			delete(windows, k)
		} else {
			windows[k] = kept
		}
	}
}

// allowWake reports whether a wake for debounceKey (channel+"\x00"+chatID)
// is currently permitted under the debounce+rate cap, and — if so —
// records this wake so subsequent calls see it.
func (n *asyncNotifierImpl) allowWake(debounceKey string) bool {
	now := n.wakeNow()
	if now.IsZero() {
		now = time.Now()
	}
	cutoffHour := now.Add(-1 * time.Hour)

	n.wakeMu.Lock()
	defer n.wakeMu.Unlock()

	window := n.wakeWindows[debounceKey]
	kept := pruneWakeWindow(window, cutoffHour)
	var last time.Time
	for _, t := range kept {
		if t.After(last) {
			last = t
		}
	}
	if !last.IsZero() && now.Sub(last) < sessionMessageWakeDebounce {
		if len(kept) == 0 {
			delete(n.wakeWindows, debounceKey)
		} else {
			n.wakeWindows[debounceKey] = kept
		}
		return false
	}
	if len(kept) >= sessionMessageWakeMaxPerHour {
		n.wakeWindows[debounceKey] = kept
		return false
	}
	n.wakeWindows[debounceKey] = append(kept, now)

	// LOW finding: sweep OTHER quiescent keys once the map has grown past
	// the threshold — see wakeWindowSweepThreshold's doc comment. Amortized:
	// runs only every time the map crosses the threshold again after a
	// sweep brings it back down, not on every call.
	if len(n.wakeWindows) > wakeWindowSweepThreshold {
		sweepQuiescentWakeWindows(n.wakeWindows, cutoffHour)
	}
	return true
}

// WakeParent implements tools.MessageParentWaker — the interface
// pkg/tools/message_parent.go depends on (defined on the tools side to
// avoid a tools<->agent import cycle; asyncNotifierImpl satisfies it
// structurally). kind is the originating SessionMessage's discriminator
// (question/blocker/error/handback — anything else is rejected, since only
// those four kinds may wake a new turn per the ADR). event carries the
// already-resolved routing (Channel/ChatID/AgentID/TranscriptSessionID) the
// caller assembled from the ORIGINAL delegation's own OriginChannel/
// OriginChatID (DelegateTaskState / the durable SessionLifecycleRecord's
// owner scope) — this file does not itself resolve "where does the parent
// live", only whether/when to wake it.
func (n *asyncNotifierImpl) WakeParent(ctx context.Context, kind string, event tools.MessageParentWakeEvent) error {
	if !wakeableSessionMessageKinds[kind] {
		return fmt.Errorf(
			"async notifier: wake parent: kind %q is not wakeable (question/blocker/error/handback only)", kind,
		)
	}
	if event.Channel == "" || event.ChatID == "" {
		return fmt.Errorf("async notifier: wake parent: refusing to wake with empty destination (channel=%q chatID=%q)",
			event.Channel, event.ChatID)
	}

	debounceKey := event.Channel + "\x00" + event.ChatID
	if !n.allowWake(debounceKey) {
		logger.DebugCF("agent", "Bounded typed wake suppressed by debounce/rate cap",
			map[string]any{"kind": kind, "channel": event.Channel, "chat_id": event.ChatID})
		return nil
	}

	return n.Notify(ctx, AsyncNotifyEvent{
		Channel:             event.Channel,
		ChatID:              event.ChatID,
		AgentID:             event.AgentID,
		TranscriptSessionID: event.TranscriptSessionID,
		SourceKind:          "message_parent:" + kind,
		Content:             event.Content,
	})
}
