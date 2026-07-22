// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-053 Phase 2 on-ramp — boot-wires the S2/S3 session-control plane into
// the live agent loop. This file is the SINGLE landing site for three things
// the Phase-1 substrate built but left inert (the architecture reviewer's
// "one coordinated landing" verdict):
//
//  1. STORE INJECTION — SetSessionMessagingStores installs the durable
//     MessageInboxStore + LifecycleStore and re-wires the delegate +
//     message_parent tool surface for every registered agent with the REAL
//     stores (mirroring SetPlanStore's late-binding discipline). Until this
//     runs, every session-control path in delegate/message_parent fail-closes
//     on nil stores.
//
//  2. BUS CONSUMER — StartSessionMessageConsumer drains the bus's 4th channel
//     (bus.SessionMessageChan, session_message.go) and dispatches by `kind`:
//     steer/respond → DeliverSessionMessage; child→parent kinds → inbox Append
//     + bounded typed wake; engine kinds → WS frame. Until this runs, the
//     channel has no consumer.
//
//  3. EGRESS FILTER — buildContentEgressFilter wires the SAME credential-store
//     SensitiveDataReplacer the agent's own outputs obey (N-10 — a child
//     cannot exfiltrate through the parent inbox what it could not send
//     directly).
//
// The content-egress filter (N-10) and the bounded typed wake reuse the
// EXISTING substrate hooks (config.FilterSensitiveData, asyncNotifierImpl.
// WakeParent) — no parallel path (DoD-11). The kill switch
// (session_messaging.enabled) is read LIVE per event (FR-196): the consumer
// no-ops and the tools fail-closed when an operator flips it to false without
// a restart.
package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// SetSessionMessagingStores installs the durable S3 MessageInboxStore and the
// S2 LifecycleStore, then re-wires the delegate + message_parent tool surface
// for every CURRENTLY-registered agent with the real stores — mirrors
// SetPlanStore's late-binding discipline exactly (the stores are constructed
// by the gateway AFTER NewAgentLoop returns, so the first registerSharedTools
// pass registered both tools fail-closed against nil stores). Called once at
// boot by the gateway, right where session.NewMessageInboxStore constructs
// the inbox. Idempotent and safe on hot-reload (registerSharedTools already
// re-runs wireSessionMessagingForAgent on every reload).
func (al *AgentLoop) SetSessionMessagingStores(inbox *session.MessageInboxStore, lifecycle *session.LifecycleStore) {
	al.mu.Lock()
	al.messageInboxStore = inbox
	al.sessionLifecycleStoreForTools = lifecycle
	al.mu.Unlock()

	if inbox == nil {
		logger.ErrorCF("agent", "SetSessionMessagingStores: installed a nil inbox store — "+
			"message_parent will remain fail-closed", nil)
	}
	if lifecycle == nil {
		logger.ErrorCF("agent", "SetSessionMessagingStores: installed a nil lifecycle store — "+
			"delegate inbox/steer/cancel will remain fail-closed", nil)
	}

	reg := al.GetRegistry()
	if reg == nil {
		logger.ErrorCF("agent", "SetSessionMessagingStores: no agent registry available — "+
			"session-messaging tool surface not re-wired", nil)
		return
	}
	for _, agentID := range reg.ListAgentIDs() {
		inst, ok := reg.GetAgent(agentID)
		if !ok || inst == nil {
			continue
		}
		al.wireSessionMessagingForAgent(inst)
	}
	logger.InfoCF("agent", "Session-messaging stores installed + tool surface re-wired",
		map[string]any{"inbox_nil": inbox == nil, "lifecycle_nil": lifecycle == nil})
}

// GetMessageInboxStore returns the installed durable inbox store (may be nil
// in tests or before boot wiring completes).
func (al *AgentLoop) GetMessageInboxStore() *session.MessageInboxStore {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.messageInboxStore
}

// GetSessionLifecycleStore returns the installed durable lifecycle store used
// by the tool surface (may be nil in tests or before boot wiring completes).
// Distinct from the plan engine's own lifecycleStore accessor — both point at
// the same gateway-constructed instance.
func (al *AgentLoop) GetSessionLifecycleStore() *session.LifecycleStore {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.sessionLifecycleStoreForTools
}

// wireSessionMessagingForAgent wires the ADR-053 session-control hooks onto
// ONE agent's delegate + message_parent tools, reading the stores from the
// AgentLoop fields (which may still be nil on the first registerSharedTools
// pass — the tools then fail-closed at Execute, matching the Wave-1
// discipline). Called by SetSessionMessagingStores (post-boot re-wire) AND by
// registerSharedTools (per-agent registration, including hot reloads).
//
// This wires ONLY the existing substrate hooks — it does not change tool
// logic. The delegate tool keeps its own action handlers; message_parent
// keeps its own kind switch. We inject: the inbox + lifecycle stores, the
// steering sink (= the AgentLoop itself, via EnqueueSteeringMessage), the
// cancel hooks (InterruptSession / InterruptSessionHard), the waker (the
// process-wide asyncNotifier, which implements tools.MessageParentWaker), and
// the content-egress filter (the config's SensitiveDataReplacer, N-10).
func (al *AgentLoop) wireSessionMessagingForAgent(agent *AgentInstance) {
	if agent == nil || agent.Tools == nil {
		return
	}
	inbox := al.GetMessageInboxStore()
	lifecycle := al.GetSessionLifecycleStore()
	egress := al.buildContentEgressFilter()
	needInputTTL := al.sessionMessagingNeedsInputTTL()

	// --- delegate tool: inject the S2/S3 stores + the steering sink + cancel
	// hooks. SetSteeringSink/SetCancelHooks/SetMessageInbox/SetLifecycleStore
	// are idempotent in-place setters (delegate was registered by
	// registerSharedTools just above this call with nil stores; this call
	// re-wires the real stores in place once they exist). ---
	if tool, ok := agent.Tools.Get("delegate"); ok {
		if dt, dtOk := tool.(*tools.DelegateTool); dtOk && dt != nil {
			if lifecycle != nil {
				dt.SetLifecycleStore(lifecycle)
			}
			if inbox != nil {
				dt.SetMessageInbox(inbox)
			}
			// The AgentLoop satisfies tools.DelegateSteeringSink via its
			// exported EnqueueSteeringMessage wrapper (steering.go) — same sink
			// a chat steer uses, so a parent->child steer lands at the child's
			// next tool boundary exactly like a chat interrupt (INV-3).
			dt.SetSteeringSink(al)
			// Cancel hooks: soft = graceful cascade (InterruptSession), hard =
			// escalate after the cancel_grace window (InterruptSessionHard).
			// Both are session-scoped (transcriptSessionID match) and cascade
			// to descendants — the exact semantics delegate.cancel requires.
			dt.SetCancelHooks(al.InterruptSession, al.InterruptSessionHard)
		}
	}

	// --- message_parent tool: register per-agent with the REAL stores. Unlike
	// delegate (which has SetMessageInbox/SetLifecycleStore setters for in-place
	// re-wire), MessageParentTool takes inbox/lifecycle as CONSTRUCTOR args
	// with no later setter — so the live-stores injection is a RECONSTRUCTION,
	// not an in-place setter call. On the first registerSharedTools pass (nil
	// stores) this registers a fail-closed instance; SetSessionMessagingStores'
	// later re-wire reconstructs it with the real stores via RegisterReplacing
	// (the method whose doc explicitly exists for this SetPlanStore-style
	// late-binding re-wire). MessageParentTool holds no mutable task state
	// (unlike delegate's tasks map), so reconstruction is always safe. ---
	mp := tools.NewMessageParentTool(inbox, lifecycle)
	mp.SetContentEgressFilter(egress)
	mp.SetNeedsInputTTL(needInputTTL)
	if al.asyncNotifier != nil {
		mp.SetWaker(al.asyncNotifier)
	}
	// RegisterReplacing: a same-name collision is EXPECTED here (the fail-closed
	// first-pass instance is replaced by the live one) — logged at DEBUG, not
	// WARN (see registry.go's RegisterReplacing doc).
	agent.Tools.RegisterReplacing(mp)
}

// buildContentEgressFilter returns the N-10 content-egress policy filter: the
// SAME credential-store SensitiveDataReplacer the agent's own outputs obey
// (config.FilterSensitiveData). A child cannot exfiltrate through the parent
// inbox what it could not send directly — message_parent routes every
// free-text and path-like field through this filter before persisting (see
// message_parent.go's filterText/filterStringSlice). The returned closure
// re-reads the LIVE config on every call so a credential rotation (a reload
// re-registers sensitive values) is reflected without re-wiring the tool.
func (al *AgentLoop) buildContentEgressFilter() tools.ContentEgressFilter {
	return func(text string) string {
		cfg := al.GetConfig()
		if cfg == nil {
			return text
		}
		return cfg.FilterSensitiveData(text)
	}
}

// sessionMessagingNeedsInputTTL reads the live needs_input TTL from the
// session_messaging config (FR-126/G-6). Returns the default when unset.
func (al *AgentLoop) sessionMessagingNeedsInputTTL() time.Duration {
	cfg := al.GetConfig()
	if cfg == nil {
		return config.DefaultSMNeedsInputTTL
	}
	return cfg.SessionMessaging.EffectiveNeedsInputTTL()
}

// ====================== The SessionMessageChan kind-router consumer ======================

// sessionMessageWakeableConsumerKinds is the closed set of child->parent kinds
// that trigger a bounded typed wake AFTER a successful bus-delivered Append.
// Mirrors message_parent.go's messageParentWakeableKinds (question/blocker/
// handback — the subset a child may emit; error is engine-only). Kept local so
// this file does not depend on pkg/tools's private set.
var sessionMessageWakeableConsumerKinds = map[string]bool{ //nolint:gochecknoglobals
	"question": true,
	"blocker":  true,
	"handback": true,
}

// sessionMessageChildToParentKinds is the closed set of child->parent
// reporting kinds the consumer routes to the durable inbox.
var sessionMessageChildToParentKinds = map[string]bool{ //nolint:gochecknoglobals
	"progress":         true,
	"checkpoint":       true,
	"artifact":         true,
	"blocker":          true,
	"question":         true,
	"handback":         true,
	"decision_request": true,
}

// StartSessionMessageConsumer launches the kind-router goroutine that drains
// bus.SessionMessageChan() and dispatches by `kind`:
//
//   - steer/respond (parent->child) → DeliverSessionMessage (inject at the
//     child's next tool boundary via the steering queue).
//   - child->parent kinds (progress/checkpoint/artifact/blocker/question/
//     handback/decision_request) → MessageInboxStore.Append keyed to the
//     durable owner key (D16) + WakeParent for the wakeable kinds.
//   - engine/session_to_ui kinds (goal_status/revision_entry) → the matching
//     WS frame emission.
//
// Honors session_messaging.enabled (the global kill switch, FR-196): when
// false, the consumer drains and DISCARDS every event (the channel must still
// be drained so a publisher is never blocked) but performs no dispatch — the
// tools fail-closed independently on their own nil-store / config checks.
// Returns a cancel function that stops the goroutine (called by the gateway
// on shutdown, alongside the agent loop's own RunContext cancel).
//
// This goroutine is the consumer that was MISSING in the Phase-1 substrate
// (the bus's 4th channel had SessionMessageChan() but nothing ranged over it).
// It is started ONCE at boot by the gateway, after SetSessionMessagingStores
// has installed the inbox, so the consumer sees a non-nil store on the first
// event.
func (al *AgentLoop) StartSessionMessageConsumer(ctx context.Context) context.CancelFunc {
	consumerCtx, cancel := context.WithCancel(ctx)
	go al.runSessionMessageConsumer(consumerCtx)
	return cancel
}

// runSessionMessageConsumer is the drain loop. It ranges over the bus's
// session-message channel until either the context is canceled or the channel
// is closed (bus.Close). Each event is dispatched by kind via
// dispatchSessionMessageEvent, which is individually recovered so one
// malformed event never kills the consumer for the whole process.
func (al *AgentLoop) runSessionMessageConsumer(ctx context.Context) {
	ch := al.bus.SessionMessageChan()
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				// Bus closed — drain is over. Mirrors how the RunContext
				// InboundChan consumer treats a closed channel.
				return
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						logger.ErrorCF("agent", "SessionMessage consumer: panic dispatching event; recovered",
							map[string]any{"panic": fmt.Sprintf("%v", r), "target": evt.TargetSessionID})
					}
				}()
				if err := al.dispatchSessionMessageEvent(ctx, evt); err != nil {
					logger.WarnCF("agent", "SessionMessage consumer: dispatch error",
						map[string]any{"target": evt.TargetSessionID, "error": err.Error()})
				}
			}()
		}
	}
}

// dispatchSessionMessageEvent routes one SessionMessageEvent to the right
// delivery mechanism by kind. This is the kind-router dispatch table.
func (al *AgentLoop) dispatchSessionMessageEvent(ctx context.Context, evt bus.SessionMessageEvent) error {
	// FR-196 global kill switch — read LIVE per event (never a boot snapshot)
	// so an operator flipping session_messaging.enabled takes effect without a
	// restart. When disabled, drain-and-discard: the channel stays unblocked
	// but no dispatch happens.
	cfg := al.GetConfig()
	if cfg != nil && !cfg.SessionMessaging.EffectiveEnabled() {
		return nil
	}

	kind, err := evt.Message.Discriminator()
	if err != nil {
		return fmt.Errorf("session message consumer: malformed envelope (no discriminator): %w", err)
	}

	switch {
	// --- parent->child turn injections (steer/respond) → DeliverSessionMessage.
	// The child's session_id is evt.TargetSessionID; resolve the child's
	// agentID from its lifecycle record to form the steering scope key. ---
	case kind == "steer" || kind == "respond":
		return al.deliverParentToChild(ctx, evt)

	// --- child->parent reporting kinds → durable inbox Append + bounded wake.
	// evt.TargetSessionID is the PARENT's durable owner key (D16) the inbox is
	// keyed to. ---
	case sessionMessageChildToParentKinds[kind]:
		return al.deliverChildToParent(ctx, evt, kind)

	// --- engine / session_to_ui kinds → WS frame emission. ---
	case kind == "goal_status":
		return al.emitGoalStatusFromBus(evt)
	case kind == "revision_entry":
		return al.emitRevisionEntryFromBus(evt)

	default:
		// Unknown kind — log and drop (forward-compat: a newer producer may
		// emit a kind this version doesn't route yet; never a hard failure).
		logger.DebugCF("agent", "SessionMessage consumer: unrouted kind",
			map[string]any{"kind": kind, "target": evt.TargetSessionID})
		return nil
	}
}

// deliverParentToChild routes a steer/respond event to the child's steering
// queue via DeliverSessionMessage (steering.go). The child's session_id is
// evt.TargetSessionID; the child's agentID is resolved from its lifecycle
// record (a delegated child always has one). When the record is missing
// (child already swept/reaped) the steer is logged and dropped — best-effort,
// never a hard failure (the parent's delegate.steer call already returned
// success to the parent; the bus path is the decoupled delivery).
func (al *AgentLoop) deliverParentToChild(ctx context.Context, evt bus.SessionMessageEvent) error {
	childSessionID := evt.TargetSessionID
	childAgentID := ""
	// Try to resolve the child's agentID from its durable lifecycle record.
	if ls := al.GetSessionLifecycleStore(); ls != nil && childSessionID != "" {
		if rec, err := ls.Load(childSessionID); err == nil && rec != nil {
			childAgentID = rec.AgentID
		}
	}
	// The steering scope key runTurn registers the active turn under is
	// "agent:<agentID>:<sessionID>" (see steering.go enqueueSteeringFromMessage).
	// When we could not resolve an agentID, fall back to a scope the child's
	// own dequeueScopeWithFallback may still match under (best-effort).
	scope := childSessionID
	if childAgentID != "" {
		scope = "agent:" + childAgentID + ":" + childSessionID
	}
	return al.DeliverSessionMessage(ctx, scope, childAgentID, evt.Message)
}

// deliverChildToParent routes a child->parent reporting event to the durable
// inbox (Append keyed to the parent owner key = evt.TargetSessionID) and, for
// the wakeable kinds, fires the bounded typed wake. The Append enforces the
// same caps (rate/body/ceiling) an in-band message_parent call does, so a
// bus-delivered message can never silently bypass the per-child ceiling
// (FR-125/D15). A wake is best-effort: the message is already durable by the
// time WakeParent is consulted, so a debounce/rate-cap suppression is never
// fatal (async_notifier.go).
func (al *AgentLoop) deliverChildToParent(ctx context.Context, evt bus.SessionMessageEvent, kind string) error {
	inbox := al.GetMessageInboxStore()
	if inbox == nil {
		return errors.New("session message consumer: inbox store not configured (SetSessionMessagingStores not called)")
	}
	ownerKey := evt.TargetSessionID
	if strings.TrimSpace(ownerKey) == "" {
		// The bus event did not carry an explicit TargetSessionID. A well-
		// formed producer MUST set it to the parent's durable owner key (D16);
		// an empty key is a producer bug, surfaced as a dispatch error rather
		// than a silent drop.
		return errors.New("session message consumer: child->parent event has no target owner key (TargetSessionID unset)")
	}
	if _, err := inbox.Append(ownerKey, evt.Message); err != nil {
		// Never-silent-drop (FR-125): the consumer logs the rejection. The
		// producer (a bus publisher) does not see this as a tool error the way
		// message_parent does — the bus path is decoupled delivery, so the
		// rejection is recorded rather than surfaced back to a child.
		return fmt.Errorf("inbox append for owner %q: %w", ownerKey, err)
	}

	// Bounded typed wake for the wakeable subset, gated by the live
	// wake_enabled kill switch (FR-196).
	if sessionMessageWakeableConsumerKinds[kind] && al.asyncNotifier != nil {
		cfg := al.GetConfig()
		if cfg == nil || cfg.SessionMessaging.EffectiveWakeEnabled() {
			if err := al.fireWakeForBusMessage(ctx, evt, kind); err != nil {
				logger.DebugCF("agent", "SessionMessage consumer: wake suppressed/failed (message is durable)",
					map[string]any{"kind": kind, "error": err.Error()})
			}
		}
	}
	return nil
}

// fireWakeForBusMessage assembles the routing the bounded typed wake needs
// (Channel/ChatID/AgentID) from the durable lifecycle record of the SESSION
// that owns this inbox (the parent), then calls WakeParent. The wake's
// debounce/rate cap (async_notifier.go) is the single bound on how often a
// parent conversation is woken, regardless of how many children report in.
func (al *AgentLoop) fireWakeForBusMessage(ctx context.Context, evt bus.SessionMessageEvent, kind string) error {
	// Resolve the parent's origin routing from the lifecycle record keyed
	// under the owner key. The owner key for a human-owned top-level parent is
	// the chat/plan id; the lifecycle record may be keyed under a session id.
	// Best-effort: if no record resolves, the wake is skipped (the message is
	// already durable; the parent reads it on its next natural turn).
	var channel, chatID, agentID, content string
	if ls := al.GetSessionLifecycleStore(); ls != nil {
		if rec, err := ls.Load(evt.TargetSessionID); err == nil && rec != nil {
			channel = rec.OriginChannel
			chatID = rec.OriginChatID
			agentID = rec.AgentID
		}
	}
	if channel == "" || chatID == "" {
		return fmt.Errorf("wake skipped: no origin routing for owner %q", evt.TargetSessionID)
	}
	content = summarizeBusMessageForWake(kind, evt.Message)
	return al.asyncNotifier.WakeParent(ctx, kind, tools.MessageParentWakeEvent{
		Channel:             channel,
		ChatID:              chatID,
		AgentID:             agentID,
		TranscriptSessionID: evt.TargetSessionID,
		Content:             content,
	})
}

// summarizeBusMessageForWake renders a short human-readable summary of a bus
// SessionMessage for the wake's content field, mirroring message_parent.go's
// summarizeForWake (kept local to avoid a tools-package dependency on the
// shape — the two are intentionally allowed to diverge for engine-produced
// messages).
func summarizeBusMessageForWake(kind string, msg generated.SessionMessage) string {
	switch kind {
	case "question":
		if v, err := msg.AsSessionMessageQuestion(); err == nil {
			return fmt.Sprintf("A delegated session is asking: %s", v.Text)
		}
	case "blocker":
		if v, err := msg.AsSessionMessageBlocker(); err == nil {
			return fmt.Sprintf("A delegated session reported a %s-severity blocker: %s", v.Severity, v.Text)
		}
	case "handback":
		if v, err := msg.AsSessionMessageHandback(); err == nil {
			return fmt.Sprintf("A delegated session handed back (%s): %s", v.Mode, v.ResultSoFar)
		}
	}
	return "A delegated session sent a message."
}

// emitGoalStatusFromBus turns a bus goal_status event into the existing
// goal_status WS frame emission (EmitGoalStatusChanged → the SPA forwarder).
// The SessionMessageGoalStatus envelope carries Condition (the typed
// GOAL_STATUS marker outcome) + GoalId + SessionId; the SPA forwarder renders
// these as the goal_status frame the UI already consumes.
func (al *AgentLoop) emitGoalStatusFromBus(evt bus.SessionMessageEvent) error {
	gs, err := evt.Message.AsSessionMessageGoalStatus()
	if err != nil {
		return fmt.Errorf("goal_status: malformed: %w", err)
	}
	al.EmitGoalStatusChanged(GoalStatusChangedPayload{
		SessionID: coalesce(evt.TargetSessionID, gs.SessionId),
		Condition: string(gs.Condition),
		State:     string(gs.Condition), // Condition IS the state for the bus variant
	})
	return nil
}

// emitRevisionEntryFromBus turns a bus revision_entry event into an event-bus
// emission. revision_entry is the engine→UI kind that records a plan/goal
// amendment (the nested Revision carries PlanId + FalsifiedAssumption +
// Generation). Emitted via emitEvent so the WS forwarder can render it.
func (al *AgentLoop) emitRevisionEntryFromBus(evt bus.SessionMessageEvent) error {
	re, err := evt.Message.AsSessionMessageRevisionEntry()
	if err != nil {
		return fmt.Errorf("revision_entry: malformed: %w", err)
	}
	reason := re.Revision.FalsifiedAssumption
	if reason == "" {
		reason = fmt.Sprintf("plan %s generation %d revised", re.Revision.PlanId, re.Revision.Generation)
	}
	al.emitEvent(
		EventKindGoalStatusChanged, // revision notices ride the goal-status frame family
		EventMeta{Source: "SessionMessageConsumer", TracePath: "session.revision_entry", SessionKey: evt.TargetSessionID},
		GoalStatusChangedPayload{
			SessionID:    coalesce(evt.TargetSessionID, re.Revision.PlanId),
			State:        "revision",
			LatestReason: reason,
		},
	)
	return nil
}

// coalesce returns the first non-empty argument.
func coalesce(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
