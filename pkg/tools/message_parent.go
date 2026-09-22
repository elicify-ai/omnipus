// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-053 §5.1 — message_parent is the first-class CHILD-side tool a
// delegated session uses to push a typed message into its parent's durable
// inbox: progress | checkpoint | artifact | blocker | question | handback.
// `decision_request`/`error`/`revision_entry`/`goal_status`/`steer`/`respond`
// are SessionMessage kinds this tool deliberately does NOT expose (they are
// engine/parent-only or session-internal — see generated.MessageParentRequest's
// doc comment).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/google/uuid"
)

// MessageParentLifecycleStore is the subset of *session.LifecycleStore
// message_parent needs: reading the CALLING child's own durable record (to
// resolve its parent/owner scope) and parking it in needs_input for a
// wait=true question.
//
// delegate.go's DelegateTool also declares its own lifecycle field with this
// exact type (rather than a second, narrower interface) so both tools share
// one contract — see List's own doc comment for why delegate.go needs it too.
type MessageParentLifecycleStore interface {
	Load(sessionID string) (*session.LifecycleRecord, error)
	Persist(rec *session.LifecycleRecord) error
	Lock(sessionID string) *sync.Mutex
	// Mutate is the atomic read-modify-write primitive (Correctness-MAJOR-3).
	// parkNeedsInput routes through it so the park transition holds the
	// per-session striped lock across the whole tail→decide→write RMW.
	Mutate(sessionID string, fn func(*session.LifecycleRecord) error) error
	// List returns every LifecycleRecord matching filter — signature-
	// identical to *session.LifecycleStore.List, so a real store satisfies
	// this trivially and every existing test fake that embeds
	// *session.LifecycleStore (callCountingLifecycleStore,
	// fix6FaultyLifecycleStore) inherits it for free.
	//
	// ADR-057 D8/R-13: delegate.go's executeCancel uses this to walk the
	// durable SteeringSessionID edge from the cancel target down to its own
	// descendants (collectCancelDescendantSessionIDs, delegate.go) so the
	// background-shell-kill cascade reaches a grandchild's own background
	// bash/exec work, not just the directly-named session's. Before this,
	// killChildBackgroundShells only ever killed the ONE named session's
	// shells — a live leak the UAT gap-closure report (2026-08-03) proved
	// against a real jim->ray->worker chain: cancelling ray left worker's
	// detached background HTTP server running for minutes.
	List(filter session.LifecycleFilter) ([]session.LifecycleRecord, error)
}

// MessageParentWakeEvent carries the fields needed to compose a bounded
// typed wake (ADR-053 S3). Still used as the wake-transport payload shape by
// pkg/agent/async_notifier.go's WakeParent/WakeParentAlways — this package no
// longer builds one directly (steer.UpwardDeliverer.Deliver does, from the
// steering session's own record); kept here (rather than moved to
// pkg/agent) so async_notifier.go does not need to depend on pkg/agent's own
// types for a value pkg/tools already defined, avoiding a tools<->agent
// import cycle the other way.
type MessageParentWakeEvent struct {
	Channel             string
	ChatID              string
	AgentID             string
	TranscriptSessionID string
	Content             string
}

// ContentEgressFilter redacts/filters untrusted child-authored text before
// it crosses the child->parent boundary (N-10 — the SAME content-egress
// policy the agent's own outputs obey; a child cannot exfiltrate through
// the parent's inbox what it could not send directly). Injected via
// SetContentEgressFilter; a nil filter is a pass-through (no-op), matching
// the pre-ADR-053 behavior for any caller that does not wire one.
type ContentEgressFilter func(text string) string

// outcomeForKind maps a message_parent kind (plus, for handback, its
// ResultSoFar) onto the I-5 Outcome steer.UpwardDeliverer.Deliver needs
// (ADR-091 landing order I-5, "the event IS an existing SessionMessage ...
// every Outcome maps onto a kind the inbox already stores"). message_parent
// deliberately never emits kind=error itself (see this file's package doc
// comment), so OutcomeFailed/OutcomeTimedOut/OutcomeInterrupted never
// originate here — those are turn-outcome events, not tool calls.
//
// artifact has no dedicated Outcome constant (pkg/steer is WP-A's; landing
// order §2 forbids this lane changing it) — it is neither terminal nor
// wake-eligible, exactly like checkpoint, so it reuses OutcomeCheckpoint.
// handback mode=pause is likewise not one of I-5's five terminal outcomes
// (the session keeps running); it reuses OutcomeBlocker, the closest
// existing "wake-eligible, not terminal" shape.
func outcomeForKind(kind string, sm generated.SessionMessage) steer.Outcome {
	switch kind {
	case "progress":
		return steer.OutcomeProgress
	case "checkpoint", "artifact":
		return steer.OutcomeCheckpoint
	case "blocker":
		return steer.OutcomeBlocker
	case "question":
		return steer.OutcomeParkedQuestion
	case "handback":
		v, err := sm.AsSessionMessageHandback()
		if err != nil {
			return steer.OutcomeCheckpoint
		}
		if v.Mode != generated.SessionMessageHandbackModeFinal {
			return steer.OutcomeBlocker
		}
		if strings.TrimSpace(v.ResultSoFar) == "" {
			return steer.OutcomeEmptyAnswer
		}
		return steer.OutcomeFinalAnswer
	default:
		return steer.OutcomeCheckpoint
	}
}

// MessageParentTool implements the `message_parent` child tool (ADR-053
// §5.1). See NewMessageParentTool for the wiring contract.
type MessageParentTool struct {
	BaseTool

	lifecycle MessageParentLifecycleStore
	deliverer steer.UpwardDeliverer
	egress    ContentEgressFilter

	// sessionMessagingEnabled, when set via SetSessionMessagingEnabled, is the
	// live-read FR-196 kill switch (session_messaging.enabled) for the SYNC
	// tool path. The async consumer honors the same switch per event; without
	// this guard a disabled plane still accepted direct inbox.Append calls
	// (arch-M2 review).
	//
	// sessionMessagingWired tracks whether SetSessionMessagingEnabled was
	// EVER called, so an unwired tool fails CLOSED on the kill switch rather
	// than fail-open (silent-failure hunter #12 — fix B.5). The FR-196
	// kill switch is a security boundary; an unwired production tool is a
	// configuration bug, not a permission grant.
	sessionMessagingEnabled func() bool
	sessionMessagingWired   atomic.Bool

	needsInputTTL time.Duration
	now           func() time.Time
}

// NewMessageParentTool constructs a MessageParentTool. deliverer and
// lifecycle are required for the tool to function (Execute returns a clear
// error when either is nil, matching DelegateTool's own "no spawner
// configured" fail-closed convention); egress is optional (a nil egress
// filter is a pass-through).
//
// ADR-091 I-5: deliverer replaces the former separate inbox+waker pair —
// steer.UpwardDeliverer.Deliver is now the ONLY upward path (Append, the
// deterministic terminal id, and the wake-eligibility-aware wake all live
// there; see steer_audience.go::SteerUpwardDeliverer, owned by this same
// lane). "it replaces that interface" — pkg/steer's own published doc
// comment for steer.UpwardDeliverer.
func NewMessageParentTool(deliverer steer.UpwardDeliverer, lifecycle MessageParentLifecycleStore) *MessageParentTool {
	return &MessageParentTool{
		deliverer:     deliverer,
		lifecycle:     lifecycle,
		needsInputTTL: session.DefaultNeedsInputTTL,
		now:           time.Now,
	}
}

// SetContentEgressFilter installs the content-egress policy filter (N-10).
func (t *MessageParentTool) SetContentEgressFilter(f ContentEgressFilter) { t.egress = f }

// SetSessionMessagingEnabled installs the live FR-196 kill-switch reader for
// the sync tool path (arch-M2 review): when the returned bool is false,
// Execute fails closed with a clear "plane disabled" error instead of
// bypassing the kill switch the async consumer already honors on the bus path.
// Wired live (re-reads config per call) by wireSessionMessagingForAgent.
func (t *MessageParentTool) SetSessionMessagingEnabled(fn func() bool) {
	t.sessionMessagingEnabled = fn
	// Mark the tool as wired regardless of whether fn is nil — once the
	// gateway has explicitly installed a reader (even one that always
	// returns false), the wiring has been acknowledged and we honor the
	// closure's verdict rather than falling through to the fail-closed
	// default below.
	t.sessionMessagingWired.Store(true)
}

// sessionMessagingPlaneEnabled reports whether the session-messaging plane is
// live for this tool's sync path. An UNWIRED tool (SetSessionMessagingEnabled
// never called — e.g. a bare unit test that did not configure the kill switch)
// fails CLOSED, matching the FR-196 security boundary's "no silent default"
// posture (silent-failure hunter #12 — fix B.5). The wired-but-nil case is
// the explicit "always disabled" sentinel the gateway uses to ship a build-
// time kill.
func (t *MessageParentTool) sessionMessagingPlaneEnabled() bool {
	if !t.sessionMessagingWired.Load() {
		// Unwired = fail closed (no silent fail-open on a security boundary).
		return false
	}
	if t.sessionMessagingEnabled == nil {
		// Wired with a nil closure = explicit "always disabled" sentinel.
		return false
	}
	return t.sessionMessagingEnabled()
}

// SetNeedsInputTTL overrides the default 24h needs_input park TTL
// (session_messaging.needs_input_ttl, FR-195/FR-126).
func (t *MessageParentTool) SetNeedsInputTTL(d time.Duration) {
	if d > 0 {
		t.needsInputTTL = d
	}
}

// SetClock overrides the tool's time source for deterministic tests.
func (t *MessageParentTool) SetClock(now func() time.Time) {
	if now != nil {
		t.now = now
	}
}

func (t *MessageParentTool) filterText(s string) string {
	if t.egress == nil || s == "" {
		return s
	}
	return t.egress(s)
}

// filterStringSlice applies f to each element of in, returning a new slice.
// Used for path-like fields (artifact.paths, handback.artifacts) so the
// content-egress policy covers them too (Security-MINOR-1) — a child must
// not be able to exfiltrate via a filename carrying a secret.
func filterStringSlice(f func(string) string, in []string) []string {
	if f == nil || len(in) == 0 {
		return in
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = f(s)
	}
	return out
}

func (t *MessageParentTool) Name() string { return "message_parent" }

func (t *MessageParentTool) Description() string {
	return "Push a typed message into your parent session's inbox. kind=\"progress\" for a lightweight " +
		"narration line, \"checkpoint\" for a durable 1-3 sentence summary of work so far, \"artifact\" to " +
		"report output file paths, \"blocker\" to flag something stopping progress, \"question\" to ask your " +
		"parent something (wait=true pauses you until answered — native sessions only), or \"handback\" to " +
		"report a final result or a cooperative pause. Every call is delivered at-least-once and deduped by " +
		"message_id; a rejected call (rate/ceiling/size limit) always returns a clear error, never a silent drop."
}

func (t *MessageParentTool) Scope() ToolScope       { return ScopeCore }
func (t *MessageParentTool) Category() ToolCategory { return CategoryDelegation }

func (t *MessageParentTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind": map[string]any{
				"type": "string",
				"enum": []string{"progress", "checkpoint", "artifact", "blocker", "question", "handback"},
				"description": "Which kind of message to send. Determines which of the other " +
					"fields are required.",
			},
			"message_id": map[string]any{
				"type":        "string",
				"description": "Optional dedupe key. Server-generated when omitted.",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Required for progress/blocker/question. Untrusted narration/question text.",
			},
			"pct": map[string]any{
				"type":        "integer",
				"description": "Optional (progress only): completion percentage estimate, 0-100.",
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "Required for checkpoint: a 1-3 sentence summary of work so far.",
			},
			"result_so_far": map[string]any{
				"type": "string",
				"description": "Optional for checkpoint, required (may be empty) for handback: " +
					"accumulated result text.",
			},
			"commit_ref": map[string]any{
				"type":        "string",
				"description": "Optional (checkpoint only): the go-git boundary commit this checkpoint corresponds to.",
			},
			"paths": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Required for artifact: output file paths produced.",
			},
			"note": map[string]any{
				"type":        "string",
				"description": "Optional (artifact only): free-text note about the artifact.",
			},
			"severity": map[string]any{
				"type":        "string",
				"enum":        []string{"low", "medium", "high"},
				"description": "Required for blocker: severity, used to prioritize the parent's response.",
			},
			"correlation_id": map[string]any{
				"type":        "string",
				"description": "Optional (question only): server-generated when omitted; routes the eventual answer.",
			},
			"wait": map[string]any{
				"type": "boolean",
				"description": "Required for question: true parks THIS session in needs_input awaiting an " +
					"answer (native sessions only). false is fire-and-forget.",
			},
			"authority": map[string]any{
				"type": "string",
				"enum": []string{"self_ok", "owner_required"},
				"description": "Optional (question only): your own assessment of whether your parent may " +
					"answer directly (self_ok) or must escalate to the human owner (owner_required). " +
					"Untrusted — an omitted value defaults to owner_required (fail-closed) and the runtime " +
					"may upgrade self_ok to owner_required on its own judgment; it can never be downgraded.",
			},
			"mode": map[string]any{
				"type":        "string",
				"enum":        []string{"final", "pause"},
				"description": "Required for handback: final (terminal) or pause (cooperative, resumable via delegate follow_up).",
			},
			"artifacts": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional (handback only): output file paths. May be empty.",
			},
			"open_questions": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Optional (handback only): unresolved questions outstanding at handback time.",
			},
		},
		"required": []string{"kind"},
	}
}

// ownerKeyFor resolves the DURABLE chat/plan id (D16) this child's parent
// inbox is keyed to, from the child's own lifecycle record. OwnerScopeID
// follows the wire contract's convention of being empty when
// OwnerScopeKind==human, which would break inbox routing for a human-owned
// top-level parent — neither of the two sources below has that problem.
//
// ADR-091 D2: SteeredBy is the sole authoritative parent edge.
func ownerKeyFor(rec *session.LifecycleRecord) string {
	if rec == nil {
		return ""
	}
	return strings.TrimSpace(rec.SteeringSessionID())
}

func stringArg(args map[string]any, key string) (string, bool) {
	v, present := args[key]
	if !present || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func stringSliceArg(args map[string]any, key string) ([]string, error) {
	v, present := args[key]
	if !present || v == nil {
		return nil, nil
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be an array of strings", key)
		}
		out = append(out, s)
	}
	return out, nil
}

// messageParentToolExecute carries the shared state of Execute across its stages.
type messageParentToolExecute struct {
	t               *MessageParentTool
	ctx             context.Context
	args            map[string]any
	kind            string
	childSessionID  string
	rec             *session.LifecycleRecord
	err             error
	ownerKey        string
	messageID       string
	now             time.Time
	parentSessionID *string
	sm              generated.SessionMessage
	correlationID   string
	waitParks       bool
	delivery        steer.Delivery
}

func (t *MessageParentTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	mt := &messageParentToolExecute{t: t, ctx: ctx, args: args}

	if r0, stop := mt.validateContext(); stop {
		return r0
	}

	mt.rec, mt.err = mt.t.lifecycle.Load(mt.childSessionID)
	if r0, stop := mt.prepareMessage(); stop {
		return r0
	}

	if r0, stop := mt.encodeMessage(); stop {
		return r0
	}

	// ADR-091 I-5: the ONLY upward path — Append, the deterministic terminal
	// id and the wake-eligibility-aware wake all live inside Deliver
	// (steer_audience.go::SteerUpwardDeliverer, owned by this same lane).
	mt.delivery, mt.err = mt.t.deliverer.Deliver(mt.ctx, steer.UpwardEvent{
		ChildSessionID: mt.childSessionID,
		Outcome:        outcomeForKind(mt.kind, mt.sm),
		Message:        mt.sm,
	})
	return mt.finishDelivery()
}

// validateContext validates tool configuration and the delegated child context.
func (mt *messageParentToolExecute) validateContext() (*ToolResult, bool) {
	// arch-M2 (Phase-2 review): FR-196 kill switch on the SYNC tool path. The
	// async consumer honors session_messaging.enabled per event; the direct
	// inbox.Append below used to bypass it. Check first, before any store
	// touch, so a disabled plane rejects the call with a clear error.
	if !mt.t.sessionMessagingPlaneEnabled() {
		return ErrorResult("message_parent: the session-messaging plane is disabled (session_messaging.enabled = false)"), true
	}
	if mt.t.deliverer == nil || mt.t.lifecycle == nil {
		return ErrorResult("message_parent: tool not fully configured (missing deliverer/lifecycle store)"), true
	}

	mt.kind, _ = mt.args["kind"].(string)
	mt.kind = strings.TrimSpace(mt.kind)
	if mt.kind == "" {
		return ErrorResult("kind is required: one of progress, checkpoint, artifact, blocker, question, handback"), true
	}

	// The durable LifecycleRecord is persisted keyed by the child's OWN
	// ADR-053 durable session_id (ToolDelegateSessionID — see
	// WithDelegateSessionID / ToolDelegateSessionID in pkg/tools/delegate.go,
	// the canonical helpers seeded by pkg/agent/subturn.go's spawnSubTurn),
	// which is distinct from the shared parent/child transcript session id
	// (ToolTranscriptSessionID, deliberately inherited by the child per
	// pkg/agent/subturn.go's FR-6a cascade-cancel matching). Looking this up
	// under the transcript id was a 100%-reproducible miss: a freshly minted
	// UUID delegate session id can never coincidentally equal the parent's
	// transcript id.
	mt.childSessionID = strings.TrimSpace(ToolDelegateSessionID(mt.ctx))
	if mt.childSessionID == "" {
		// A native task run's root turn carries tools.WithRunningTaskID on ctx
		// (task_executor.go, set before processTaskDirect) but is never a
		// delegated child — only pkg/agent/subturn.go's spawnSubTurn calls
		// WithDelegateSessionID, and it never runs for a task dispatch. This
		// is a structural, not transient, gap: a task-dispatch session's
		// durable lifecycle record deliberately leaves SteeringSessionID empty
		// (task_executor.go's mintTaskLifecycleRecord doc comment — "a task
		// dispatch is not a delegate.run call, so there is no delegating
		// parent to attribute"), so there is no parent inbox this call could
		// ever reach even if the context plumbing were added. Name the actual
		// completion/reporting path instead of a generic session error.
		if strings.TrimSpace(ToolRunningTaskID(mt.ctx)) != "" {
			return ErrorResult("message_parent: this task run has no parent session to message — " +
				"it was dispatched directly, not delegated. Report progress in your reasoning, and report " +
				"your outcome with goal_claim (status \"met\" when verified done, \"blocked\" when something " +
				"stops you, \"waiting_on_user\" when only the operator can proceed)."), true
		}
		return ErrorResult("message_parent: no session context available for this call"), true
	}
	return nil, false
}

// prepareMessage validates the lifecycle record and prepares the message's shared routing fields.
func (mt *messageParentToolExecute) prepareMessage() (*ToolResult, bool) {
	if mt.err != nil {
		return ErrorResult(fmt.Sprintf(
			"message_parent: no durable session record for this session (%v) — message_parent may only be "+
				"called from within a delegated child session", mt.err,
		)), true
	}
	if mt.rec.Is3P {
		return ErrorResult("message_parent: not available to external-CLI (3P) sessions (D5) — " +
			"3P children never advertise this tool; use the delegate tool's normal fire-and-collect flow instead"), true
	}
	mt.ownerKey = ownerKeyFor(mt.rec)
	if mt.ownerKey == "" {
		return ErrorResult("message_parent: could not resolve this session's parent/owner scope (owner_scope_id unset)"), true
	}

	mt.messageID, _ = stringArg(mt.args, "message_id")
	if strings.TrimSpace(mt.messageID) == "" {
		mt.messageID = uuid.NewString()
	}

	mt.now = mt.t.now().UTC()

	if mt.rec.OwnerScopeKind == session.OwnerScopeParentSession && mt.rec.OwnerScopeID != "" {
		id := mt.rec.OwnerScopeID
		mt.parentSessionID = &id
	}
	return nil, false
}

// encodeMessage validates kind-specific arguments and encodes the typed session message.
func (mt *messageParentToolExecute) encodeMessage() (*ToolResult, bool) {
	switch mt.kind {
	case "progress":
		text, ok := stringArg(mt.args, "text")
		if !ok || strings.TrimSpace(text) == "" {
			return ErrorResult("text is required and must be a non-empty string for kind=progress"), true
		}
		v := generated.SessionMessageProgress{
			MessageId:       mt.messageID,
			SessionId:       mt.childSessionID,
			ParentSessionId: mt.parentSessionID,
			CreatedAt:       mt.now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  mt.rec.AgentID,
			Text:            mt.t.filterText(text),
		}
		if pctRaw, present := mt.args["pct"]; present && pctRaw != nil {
			pct, perr := toIntArg(pctRaw)
			if perr != nil {
				return ErrorResult("pct must be an integer 0-100"), true
			}
			v.Pct = &pct
		}
		if encodeErr := mt.sm.FromSessionMessageProgress(v); encodeErr != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode progress: %v", encodeErr)), true
		}

	case "checkpoint":
		summary, ok := stringArg(mt.args, "summary")
		if !ok || strings.TrimSpace(summary) == "" {
			return ErrorResult("summary is required and must be a non-empty string for kind=checkpoint"), true
		}
		v := generated.SessionMessageCheckpoint{
			MessageId:       mt.messageID,
			SessionId:       mt.childSessionID,
			ParentSessionId: mt.parentSessionID,
			CreatedAt:       mt.now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  mt.rec.AgentID,
			Summary:         mt.t.filterText(summary),
		}
		if rsf, ok := stringArg(mt.args, "result_so_far"); ok {
			filtered := mt.t.filterText(rsf)
			v.ResultSoFar = &filtered
		}
		if cr, ok := stringArg(mt.args, "commit_ref"); ok {
			// Security-MINOR-1: commit_ref is a path-like field a child
			// could exfiltrate through (a filename carrying a secret).
			// Route it through the same content-egress filter as free text.
			filtered := mt.t.filterText(cr)
			v.CommitRef = &filtered
		}
		if encodeErr := mt.sm.FromSessionMessageCheckpoint(v); encodeErr != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode checkpoint: %v", encodeErr)), true
		}

	case "artifact":
		paths, perr := stringSliceArg(mt.args, "paths")
		if perr != nil {
			return ErrorResult(perr.Error()), true
		}
		if len(paths) == 0 {
			return ErrorResult("paths is required and must be a non-empty array of strings for kind=artifact"), true
		}
		v := generated.SessionMessageArtifact{
			MessageId:       mt.messageID,
			SessionId:       mt.childSessionID,
			ParentSessionId: mt.parentSessionID,
			CreatedAt:       mt.now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  mt.rec.AgentID,
			// Security-MINOR-1: paths are path-like fields a child could
			// exfiltrate through (a filename carrying a secret). Route each
			// through the same content-egress filter as free text.
			Paths: filterStringSlice(mt.t.filterText, paths),
		}
		if note, ok := stringArg(mt.args, "note"); ok {
			filtered := mt.t.filterText(note)
			v.Note = &filtered
		}
		if encodeErr := mt.sm.FromSessionMessageArtifact(v); encodeErr != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode artifact: %v", encodeErr)), true
		}

	case "blocker":
		text, ok := stringArg(mt.args, "text")
		if !ok || strings.TrimSpace(text) == "" {
			return ErrorResult("text is required and must be a non-empty string for kind=blocker"), true
		}
		severity, _ := stringArg(mt.args, "severity")
		switch severity {
		case "low", "medium", "high":
		default:
			return ErrorResult(`severity is required for kind=blocker and must be one of "low", "medium", "high"`), true
		}
		v := generated.SessionMessageBlocker{
			MessageId:       mt.messageID,
			SessionId:       mt.childSessionID,
			ParentSessionId: mt.parentSessionID,
			CreatedAt:       mt.now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  mt.rec.AgentID,
			Text:            mt.t.filterText(text),
			Severity:        generated.SessionMessageBlockerSeverity(severity),
		}
		if cid, ok := stringArg(mt.args, "correlation_id"); ok && cid != "" {
			v.CorrelationId = &cid
		}
		if encodeErr := mt.sm.FromSessionMessageBlocker(v); encodeErr != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode blocker: %v", encodeErr)), true
		}

	case "question":
		text, ok := stringArg(mt.args, "text")
		if !ok || strings.TrimSpace(text) == "" {
			return ErrorResult("text is required and must be a non-empty string for kind=question"), true
		}
		waitRaw, present := mt.args["wait"]
		if !present || waitRaw == nil {
			return ErrorResult("wait is required (boolean) for kind=question"), true
		}
		wait, ok := waitRaw.(bool)
		if !ok {
			return ErrorResult("wait must be a boolean for kind=question"), true
		}
		mt.correlationID, _ = stringArg(mt.args, "correlation_id")
		if strings.TrimSpace(mt.correlationID) == "" {
			mt.correlationID = uuid.NewString()
		}
		// FR-131 (M3, fail-closed default): an omitted/absent authority tag
		// defaults to owner_required. FR-139's runtime content-based upgrade
		// (deriveQuestionAuthority — credential/spend/irreversible/
		// out-of-scope detection) is Group F, Phase 2; this wave implements
		// only the mandatory fail-closed default a Phase-2 caller layers the
		// upgrade heuristic on top of.
		authority := generated.SessionMessageQuestionAuthority("owner_required")
		if a, ok := stringArg(mt.args, "authority"); ok && a == "self_ok" {
			authority = generated.SessionMessageQuestionAuthority("self_ok")
		}
		v := generated.SessionMessageQuestion{
			MessageId:       mt.messageID,
			SessionId:       mt.childSessionID,
			ParentSessionId: mt.parentSessionID,
			CreatedAt:       mt.now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  mt.rec.AgentID,
			Text:            mt.t.filterText(text),
			Wait:            wait,
			CorrelationId:   mt.correlationID,
			Authority:       &authority,
		}
		if encodeErr := mt.sm.FromSessionMessageQuestion(v); encodeErr != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode question: %v", encodeErr)), true
		}
		mt.waitParks = wait

	case "handback":
		mode, _ := stringArg(mt.args, "mode")
		switch mode {
		case "final", "pause":
		default:
			return ErrorResult(`mode is required for kind=handback and must be "final" or "pause"`), true
		}
		resultSoFar, _ := stringArg(mt.args, "result_so_far")
		artifacts, aerr := stringSliceArg(mt.args, "artifacts")
		if aerr != nil {
			return ErrorResult(aerr.Error()), true
		}
		openQuestions, oerr := stringSliceArg(mt.args, "open_questions")
		if oerr != nil {
			return ErrorResult(oerr.Error()), true
		}
		filteredOQ := make([]string, len(openQuestions))
		for i, q := range openQuestions {
			filteredOQ[i] = mt.t.filterText(q)
		}
		v := generated.SessionMessageHandback{
			MessageId:       mt.messageID,
			SessionId:       mt.childSessionID,
			ParentSessionId: mt.parentSessionID,
			CreatedAt:       mt.now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  mt.rec.AgentID,
			Mode:            generated.SessionMessageHandbackMode(mode),
			ResultSoFar:     mt.t.filterText(resultSoFar),
			// Security-MINOR-1: artifacts are path-like fields a child could
			// exfiltrate through (a filename carrying a secret). Route each
			// through the same content-egress filter as free text.
			Artifacts:     filterStringSlice(mt.t.filterText, artifacts),
			OpenQuestions: filteredOQ,
		}
		if v.Artifacts == nil {
			v.Artifacts = []string{}
		}
		if v.OpenQuestions == nil {
			v.OpenQuestions = []string{}
		}
		if encodeErr := mt.sm.FromSessionMessageHandback(v); encodeErr != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode handback: %v", encodeErr)), true
		}

	default:
		return ErrorResult(fmt.Sprintf(
			"invalid kind %q: must be one of progress, checkpoint, artifact, blocker, question, handback", mt.kind,
		)), true
	}
	return nil, false
}

// finishDelivery checks delivery, performs follow-up bookkeeping, and returns the response.
func (mt *messageParentToolExecute) finishDelivery() *ToolResult {
	if mt.err != nil {
		// Never-silent-drop (FR-125): every rejection surfaces to the child
		// as a clear tool error.
		return ErrorResult(fmt.Sprintf("message_parent: %v", mt.err)).WithError(mt.err)
	}

	if mt.waitParks {
		if perr := mt.t.parkNeedsInput(mt.childSessionID, mt.correlationID, mt.now); perr != nil {
			return ErrorResult(fmt.Sprintf("message_parent: question accepted but failed to park session: %v", perr)).WithError(perr)
		}
	}

	// The wake itself (eligibility, identity, debounce bypass) already
	// happened inside Deliver, called from Execute above — nothing left to
	// do here. mt.delivery.Outcome is available for callers that want it
	// (none today; kept on the struct for parity with Deliver's contract).

	resp := generated.MessageParentResponse{Accepted: true, MessageId: &mt.delivery.MessageID}
	if mt.correlationID != "" {
		resp.CorrelationId = &mt.correlationID
	}
	payload, merr := json.Marshal(resp)
	var result *ToolResult
	if merr != nil {
		result = NewToolResult(fmt.Sprintf("message_parent: accepted (message_id=%s)", mt.delivery.MessageID))
	} else {
		result = NewToolResult(string(payload))
	}
	// C2 (ADR-057 UAT 2026-08-03): signal pkg/agent/loop.go's runTurn to stop
	// the calling child's turn NOW, immediately after this tool call's own
	// bookkeeping — waitParks is only ever true once parkNeedsInput (above)
	// has already succeeded, so this is reached exclusively on the genuine
	// success path. Without this flag the in-memory turn loop has no way to
	// learn its own session was just durably parked into needs_input and
	// keeps iterating past it (the exact defect this fix closes).
	if mt.waitParks {
		result.ParksTurn = true
	}
	return result
}

// parkNeedsInput transitions the calling child's own durable record to
// needs_input (INV-4/G-6).
//
// NOTE ON LOCKING (Comments-MAJOR-1 — the prior comment here was false): a
// naked Persist over the `rec` Execute loaded earlier (at line ~304) is NOT
// atomic — there IS a Load+Persist race window, because the unlocked work
// between Execute's Load and this Persist (encoding the message, the inbox
// Append, the wake) can be raced by a concurrent transition on the SAME
// session_id (e.g. a parent cancel landing between the Load and the park),
// and `rec` would be stale by the time it is copied here. This now routes
// the whole transition through session.LifecycleStore.Mutate — the atomic
// RMW primitive that holds the per-session striped lock across tail→decide→
// write (Correctness-MAJOR-3). Mutate RE-LOADS the current tail under the
// lock, so the park always lands against the live record, not the stale
// snapshot Execute captured. Callers MUST NOT already hold Lock(sessionID)
// (sync.Mutex is not reentrant — Mutate takes it once internally). The
// honesty template is delegate.go transitionLifecycle's doc comment.
func (t *MessageParentTool) parkNeedsInput(childSessionID string, correlationID string, now time.Time) error {
	return t.lifecycle.Mutate(childSessionID, func(cur *session.LifecycleRecord) error {
		if cur == nil {
			return session.ErrLifecycleNotFound
		}
		cur.State = session.LifecycleNeedsInput
		cur.NeedsInput = &session.NeedsInput{
			CorrelationID:   correlationID,
			Reconstructable: true, // park-time hint only (m5); boot-sweep re-derives authoritatively
			TTLDeadline:     now.Add(t.needsInputTTL),
		}
		return nil
	})
}

func toIntArg(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("not a number")
	}
}
