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
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/google/uuid"
)

// MessageParentInboxStore is the subset of *session.MessageInboxStore
// message_parent needs. Defined as an interface (mirroring
// DelegateSessionStore/DelegateAgentRegistry in delegate.go) so tests can
// inject a fake without touching disk.
type MessageParentInboxStore interface {
	// Append persists msg into ownerKey's durable inbox. A non-nil error is
	// always one of pkg/session's sentinel Err* values — never-silent-drop
	// (FR-125): the caller MUST surface it as a tool error to the child.
	Append(ownerKey string, msg generated.SessionMessage) (*session.AppendResult, error)
}

// MessageParentLifecycleStore is the subset of *session.LifecycleStore
// message_parent needs: reading the CALLING child's own durable record (to
// resolve its parent/owner scope) and parking it in needs_input for a
// wait=true question.
type MessageParentLifecycleStore interface {
	Load(sessionID string) (*session.LifecycleRecord, error)
	Persist(rec *session.LifecycleRecord) error
	Lock(sessionID string) *sync.Mutex
	// Mutate is the atomic read-modify-write primitive (Correctness-MAJOR-3).
	// parkNeedsInput routes through it so the park transition holds the
	// per-session striped lock across the whole tail→decide→write RMW.
	Mutate(sessionID string, fn func(*session.LifecycleRecord) error) error
}

// MessageParentWakeEvent carries the fields needed to compose a bounded
// typed wake (ADR-053 S3) without this package depending on pkg/agent's
// AsyncNotifyEvent (avoids a tools<->agent import cycle — pkg/agent already
// imports pkg/tools, so the dependency can only run this direction).
type MessageParentWakeEvent struct {
	Channel             string
	ChatID              string
	AgentID             string
	TranscriptSessionID string
	Content             string
}

// MessageParentWaker abstracts the bounded typed wake
// (question/blocker/error/handback only, debounced >=15s, rate-capped
// <=4/h) that pkg/agent's AsyncNotifier implements (async_notifier.go's
// WakeParent). A wake failure/suppression is never fatal to the tool call —
// the message is already durably in the inbox by the time WakeParent is
// consulted.
type MessageParentWaker interface {
	WakeParent(ctx context.Context, kind string, event MessageParentWakeEvent) error
}

// ContentEgressFilter redacts/filters untrusted child-authored text before
// it crosses the child->parent boundary (N-10 — the SAME content-egress
// policy the agent's own outputs obey; a child cannot exfiltrate through
// the parent's inbox what it could not send directly). Injected via
// SetContentEgressFilter; a nil filter is a pass-through (no-op), matching
// the pre-ADR-053 behavior for any caller that does not wire one.
type ContentEgressFilter func(text string) string

// messageParentWakeableKinds is the closed set of kinds that attempt a
// bounded typed wake after a successful Append.
//
// Comments-MAJOR-2: this set is INTENTIONALLY A SUBSET of
// async_notifier.go's wakeableSessionMessageKinds, NOT a mirror of it. The
// notifier's set is {question, blocker, error, handback}; this list omits
// `error` because message_parent deliberately does not EMIT kind=error
// (error is an engine/parent-only SessionMessage kind — see this file's
// package doc comment — so a child can never append one and thus can never
// wake on it). The two were never equal; the prior comment's "mirrors"
// claim was false. Kept as a local copy so this package does not need to
// import pkg/agent, and so a caller wiring a DIFFERENT MessageParentWaker
// still gets the same kind-gating at the call site.
var messageParentWakeableKinds = map[string]bool{ //nolint:gochecknoglobals
	"blocker":  true,
	"question": true,
	"handback": true,
}

// MessageParentTool implements the `message_parent` child tool (ADR-053
// §5.1). See NewMessageParentTool for the wiring contract.
type MessageParentTool struct {
	BaseTool

	inbox     MessageParentInboxStore
	lifecycle MessageParentLifecycleStore
	waker     MessageParentWaker
	egress    ContentEgressFilter

	// sessionMessagingEnabled, when set via SetSessionMessagingEnabled, is the
	// live-read FR-196 kill switch (session_messaging.enabled) for the SYNC
	// tool path. The async consumer honors the same switch per event; without
	// this guard a disabled plane still accepted direct inbox.Append calls
	// (arch-M2 review). Nil = fail open (unwired, e.g. unit tests), matching
	// the consumer's nil-config posture.
	sessionMessagingEnabled func() bool

	needsInputTTL time.Duration
	now           func() time.Time
}

// NewMessageParentTool constructs a MessageParentTool. inbox and lifecycle
// are required for the tool to function (Execute returns a clear error when
// either is nil, matching DelegateTool's own "no spawner configured"
// fail-closed convention); waker and egress are optional (a nil waker skips
// the wake attempt silently — the message is still durably stored; a nil
// egress filter is a pass-through).
func NewMessageParentTool(inbox MessageParentInboxStore, lifecycle MessageParentLifecycleStore) *MessageParentTool {
	return &MessageParentTool{
		inbox:         inbox,
		lifecycle:     lifecycle,
		needsInputTTL: session.DefaultNeedsInputTTL,
		now:           time.Now,
	}
}

// SetWaker installs the bounded typed-wake implementation.
func (t *MessageParentTool) SetWaker(w MessageParentWaker) { t.waker = w }

// SetContentEgressFilter installs the content-egress policy filter (N-10).
func (t *MessageParentTool) SetContentEgressFilter(f ContentEgressFilter) { t.egress = f }

// SetSessionMessagingEnabled installs the live FR-196 kill-switch reader for
// the sync tool path (arch-M2 review): when the returned bool is false,
// Execute fails closed with a clear "plane disabled" error instead of
// bypassing the kill switch the async consumer already honors on the bus path.
// Wired live (re-reads config per call) by wireSessionMessagingForAgent.
func (t *MessageParentTool) SetSessionMessagingEnabled(fn func() bool) {
	t.sessionMessagingEnabled = fn
}

// sessionMessagingPlaneEnabled reports whether the session-messaging plane is
// live for this tool's sync path. A nil closure (unwired — e.g. a bare unit
// test that did not call SetSessionMessagingEnabled) fails OPEN, matching the
// async consumer's nil-config posture (session_messaging_wire.go dispatch).
func (t *MessageParentTool) sessionMessagingPlaneEnabled() bool {
	if t.sessionMessagingEnabled == nil {
		return true
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
// inbox is keyed to, from the child's own lifecycle record. ParentDurableKey
// (not OwnerScopeID) is authoritative here — OwnerScopeID follows the wire
// contract's convention of being empty when OwnerScopeKind==human, which
// would break inbox routing for a human-owned top-level parent.
func ownerKeyFor(rec *session.LifecycleRecord) string {
	if rec == nil {
		return ""
	}
	return strings.TrimSpace(rec.ParentDurableKey)
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

func (t *MessageParentTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	// arch-M2 (Phase-2 review): FR-196 kill switch on the SYNC tool path. The
	// async consumer honors session_messaging.enabled per event; the direct
	// inbox.Append below used to bypass it. Check first, before any store
	// touch, so a disabled plane rejects the call with a clear error.
	if !t.sessionMessagingPlaneEnabled() {
		return ErrorResult("message_parent: the session-messaging plane is disabled (session_messaging.enabled = false)")
	}
	if t.inbox == nil || t.lifecycle == nil {
		return ErrorResult("message_parent: tool not fully configured (missing inbox/lifecycle store)")
	}

	kind, _ := args["kind"].(string)
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return ErrorResult("kind is required: one of progress, checkpoint, artifact, blocker, question, handback")
	}

	childSessionID := strings.TrimSpace(ToolTranscriptSessionID(ctx))
	if childSessionID == "" {
		return ErrorResult("message_parent: no session context available for this call")
	}

	rec, err := t.lifecycle.Load(childSessionID)
	if err != nil {
		return ErrorResult(fmt.Sprintf(
			"message_parent: no durable session record for this session (%v) — message_parent may only be "+
				"called from within a delegated child session", err,
		))
	}
	if rec.Is3P {
		return ErrorResult("message_parent: not available to external-CLI (3P) sessions (D5) — " +
			"3P children never advertise this tool; use the delegate tool's normal fire-and-collect flow instead")
	}
	ownerKey := ownerKeyFor(rec)
	if ownerKey == "" {
		return ErrorResult("message_parent: could not resolve this session's parent/owner scope (owner_scope_id unset)")
	}

	messageID, _ := stringArg(args, "message_id")
	if strings.TrimSpace(messageID) == "" {
		messageID = uuid.NewString()
	}

	now := t.now().UTC()
	var parentSessionID *string
	if rec.OwnerScopeKind == session.OwnerScopeParentSession && rec.OwnerScopeID != "" {
		id := rec.OwnerScopeID
		parentSessionID = &id
	}

	var sm generated.SessionMessage
	var correlationID string
	var waitParks bool

	switch kind {
	case "progress":
		text, ok := stringArg(args, "text")
		if !ok || strings.TrimSpace(text) == "" {
			return ErrorResult("text is required and must be a non-empty string for kind=progress")
		}
		v := generated.SessionMessageProgress{
			MessageId:       messageID,
			SessionId:       childSessionID,
			ParentSessionId: parentSessionID,
			CreatedAt:       now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  rec.AgentID,
			Text:            t.filterText(text),
		}
		if pctRaw, present := args["pct"]; present && pctRaw != nil {
			pct, perr := toIntArg(pctRaw)
			if perr != nil {
				return ErrorResult("pct must be an integer 0-100")
			}
			v.Pct = &pct
		}
		if err := sm.FromSessionMessageProgress(v); err != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode progress: %v", err))
		}

	case "checkpoint":
		summary, ok := stringArg(args, "summary")
		if !ok || strings.TrimSpace(summary) == "" {
			return ErrorResult("summary is required and must be a non-empty string for kind=checkpoint")
		}
		v := generated.SessionMessageCheckpoint{
			MessageId:       messageID,
			SessionId:       childSessionID,
			ParentSessionId: parentSessionID,
			CreatedAt:       now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  rec.AgentID,
			Summary:         t.filterText(summary),
		}
		if rsf, ok := stringArg(args, "result_so_far"); ok {
			filtered := t.filterText(rsf)
			v.ResultSoFar = &filtered
		}
		if cr, ok := stringArg(args, "commit_ref"); ok {
			// Security-MINOR-1: commit_ref is a path-like field a child
			// could exfiltrate through (a filename carrying a secret).
			// Route it through the same content-egress filter as free text.
			filtered := t.filterText(cr)
			v.CommitRef = &filtered
		}
		if err := sm.FromSessionMessageCheckpoint(v); err != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode checkpoint: %v", err))
		}

	case "artifact":
		paths, perr := stringSliceArg(args, "paths")
		if perr != nil {
			return ErrorResult(perr.Error())
		}
		if len(paths) == 0 {
			return ErrorResult("paths is required and must be a non-empty array of strings for kind=artifact")
		}
		v := generated.SessionMessageArtifact{
			MessageId:       messageID,
			SessionId:       childSessionID,
			ParentSessionId: parentSessionID,
			CreatedAt:       now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  rec.AgentID,
			// Security-MINOR-1: paths are path-like fields a child could
			// exfiltrate through (a filename carrying a secret). Route each
			// through the same content-egress filter as free text.
			Paths: filterStringSlice(t.filterText, paths),
		}
		if note, ok := stringArg(args, "note"); ok {
			filtered := t.filterText(note)
			v.Note = &filtered
		}
		if err := sm.FromSessionMessageArtifact(v); err != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode artifact: %v", err))
		}

	case "blocker":
		text, ok := stringArg(args, "text")
		if !ok || strings.TrimSpace(text) == "" {
			return ErrorResult("text is required and must be a non-empty string for kind=blocker")
		}
		severity, _ := stringArg(args, "severity")
		switch severity {
		case "low", "medium", "high":
		default:
			return ErrorResult(`severity is required for kind=blocker and must be one of "low", "medium", "high"`)
		}
		v := generated.SessionMessageBlocker{
			MessageId:       messageID,
			SessionId:       childSessionID,
			ParentSessionId: parentSessionID,
			CreatedAt:       now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  rec.AgentID,
			Text:            t.filterText(text),
			Severity:        generated.SessionMessageBlockerSeverity(severity),
		}
		if cid, ok := stringArg(args, "correlation_id"); ok && cid != "" {
			v.CorrelationId = &cid
		}
		if err := sm.FromSessionMessageBlocker(v); err != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode blocker: %v", err))
		}

	case "question":
		text, ok := stringArg(args, "text")
		if !ok || strings.TrimSpace(text) == "" {
			return ErrorResult("text is required and must be a non-empty string for kind=question")
		}
		waitRaw, present := args["wait"]
		if !present || waitRaw == nil {
			return ErrorResult("wait is required (boolean) for kind=question")
		}
		wait, ok := waitRaw.(bool)
		if !ok {
			return ErrorResult("wait must be a boolean for kind=question")
		}
		correlationID, _ = stringArg(args, "correlation_id")
		if strings.TrimSpace(correlationID) == "" {
			correlationID = uuid.NewString()
		}
		// FR-131 (M3, fail-closed default): an omitted/absent authority tag
		// defaults to owner_required. FR-139's runtime content-based upgrade
		// (deriveQuestionAuthority — credential/spend/irreversible/
		// out-of-scope detection) is Group F, Phase 2; this wave implements
		// only the mandatory fail-closed default a Phase-2 caller layers the
		// upgrade heuristic on top of.
		authority := generated.SessionMessageQuestionAuthority("owner_required")
		if a, ok := stringArg(args, "authority"); ok && a == "self_ok" {
			authority = generated.SessionMessageQuestionAuthority("self_ok")
		}
		v := generated.SessionMessageQuestion{
			MessageId:       messageID,
			SessionId:       childSessionID,
			ParentSessionId: parentSessionID,
			CreatedAt:       now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  rec.AgentID,
			Text:            t.filterText(text),
			Wait:            wait,
			CorrelationId:   correlationID,
			Authority:       &authority,
		}
		if err := sm.FromSessionMessageQuestion(v); err != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode question: %v", err))
		}
		waitParks = wait

	case "handback":
		mode, _ := stringArg(args, "mode")
		switch mode {
		case "final", "pause":
		default:
			return ErrorResult(`mode is required for kind=handback and must be "final" or "pause"`)
		}
		resultSoFar, _ := stringArg(args, "result_so_far")
		artifacts, aerr := stringSliceArg(args, "artifacts")
		if aerr != nil {
			return ErrorResult(aerr.Error())
		}
		openQuestions, oerr := stringSliceArg(args, "open_questions")
		if oerr != nil {
			return ErrorResult(oerr.Error())
		}
		filteredOQ := make([]string, len(openQuestions))
		for i, q := range openQuestions {
			filteredOQ[i] = t.filterText(q)
		}
		v := generated.SessionMessageHandback{
			MessageId:       messageID,
			SessionId:       childSessionID,
			ParentSessionId: parentSessionID,
			CreatedAt:       now,
			Depth:           1,
			UntrustedOrigin: true,
			SenderIdentity:  rec.AgentID,
			Mode:            generated.SessionMessageHandbackMode(mode),
			ResultSoFar:     t.filterText(resultSoFar),
			// Security-MINOR-1: artifacts are path-like fields a child could
			// exfiltrate through (a filename carrying a secret). Route each
			// through the same content-egress filter as free text.
			Artifacts:     filterStringSlice(t.filterText, artifacts),
			OpenQuestions: filteredOQ,
		}
		if v.Artifacts == nil {
			v.Artifacts = []string{}
		}
		if v.OpenQuestions == nil {
			v.OpenQuestions = []string{}
		}
		if err := sm.FromSessionMessageHandback(v); err != nil {
			return ErrorResult(fmt.Sprintf("message_parent: encode handback: %v", err))
		}

	default:
		return ErrorResult(fmt.Sprintf(
			"invalid kind %q: must be one of progress, checkpoint, artifact, blocker, question, handback", kind,
		))
	}

	appendResult, err := t.inbox.Append(ownerKey, sm)
	if err != nil {
		// Never-silent-drop (FR-125): every rejection surfaces to the child
		// as a clear tool error.
		return ErrorResult(fmt.Sprintf("message_parent: %v", err)).WithError(err)
	}

	if waitParks {
		if perr := t.parkNeedsInput(childSessionID, correlationID, now); perr != nil {
			return ErrorResult(fmt.Sprintf("message_parent: question accepted but failed to park session: %v", perr)).WithError(perr)
		}
	}

	if t.waker != nil && messageParentWakeableKinds[kind] && !appendResult.Deduped {
		wakeContent := summarizeForWake(kind, sm)
		if werr := t.waker.WakeParent(ctx, kind, MessageParentWakeEvent{
			Channel:             rec.OriginChannel,
			ChatID:              rec.OriginChatID,
			AgentID:             rec.AgentID,
			TranscriptSessionID: childSessionID,
			Content:             wakeContent,
		}); werr != nil {
			// Best-effort: the message is already durably stored; a wake
			// failure/suppression must never fail the tool call itself.
			logMessageParentWakeFailure(kind, werr)
		}
	}

	resp := generated.MessageParentResponse{Accepted: true, MessageId: &appendResult.MessageID}
	if correlationID != "" {
		resp.CorrelationId = &correlationID
	}
	payload, merr := json.Marshal(resp)
	if merr != nil {
		return NewToolResult(fmt.Sprintf("message_parent: accepted (message_id=%s)", appendResult.MessageID))
	}
	return NewToolResult(string(payload))
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

// summarizeForWake renders a short, human-readable summary of sm for the
// bounded typed wake's AsyncNotifyEvent.Content.
func summarizeForWake(kind string, sm generated.SessionMessage) string {
	switch kind {
	case "question":
		if v, err := sm.AsSessionMessageQuestion(); err == nil {
			return fmt.Sprintf("A delegated session is asking: %s", v.Text)
		}
	case "blocker":
		if v, err := sm.AsSessionMessageBlocker(); err == nil {
			return fmt.Sprintf("A delegated session reported a %s-severity blocker: %s", v.Severity, v.Text)
		}
	case "handback":
		if v, err := sm.AsSessionMessageHandback(); err == nil {
			return fmt.Sprintf("A delegated session handed back (%s): %s", v.Mode, v.ResultSoFar)
		}
	}
	return "A delegated session sent a message."
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

// logMessageParentWakeFailure is a tiny indirection so tests can assert a
// wake failure/suppression never propagates to the tool result without
// needing a real logger.
var logMessageParentWakeFailure = func(kind string, err error) { //nolint:gochecknoglobals
	// Intentionally best-effort; see the WakeParent call site's comment.
	_ = kind
	_ = err
}
