// delegate_status.go: Poll and report on a delegation — session status, live activity, child inbox messages, and checkpoint peek.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ResolvableSessionIDs implements tools.JobSessionResolver (#583). It used
// to report which delegate session ids were still resolvable in this
// process's in-memory legacy task-state index (t.tasks/t.sessionIndex,
// keyed by the now-deleted task_id) — the signal list_jobs' collectSubagentRows
// once used to decide whether a subagent row's session_id could actually be
// acted on. ADR-091's launcher migration deleted that index outright
// (delegate.go's package doc comment has the full history); the durable
// session.LifecycleRecord is now the only store a session's liveness is
// read from, and collectSubagentRows (pkg/tools/list_jobs_sources.go)
// derives Actionable from the record's own status instead of calling this
// method's result at all — "whether an old in-memory delegate index
// happens to contain the id cannot make a running or parked session
// unactionable" (see that function's own comment).
//
// Kept only so *DelegateTool continues to satisfy tools.JobSessionResolver
// for pkg/agent's existing wiring (loop_wire.go's SetSessionResolver); every
// id is now reported unresolvable, which is the honest answer for an index
// that no longer exists, and matches the value this method already
// returned for every id once the launcher migration stopped writing it.
func (t *DelegateTool) ResolvableSessionIDs(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = false
	}
	return out
}

// ResolvableLabels implements tools.JobLabelResolver (UAT M3, 2026-08-03).
// It used to report the caller-supplied `delegate(label=...)` value from
// the same now-deleted in-memory task-state index ResolvableSessionIDs'
// doc comment describes; list_jobs' collectSubagentRows never actually
// passed a label resolver into that read path even before the index was
// deleted (there is no labelResolver parameter on collectSubagentRows), so
// this was already unreachable from production.
//
// Kept only so *DelegateTool continues to satisfy tools.JobLabelResolver
// for pkg/agent's existing wiring (loop_wire.go's SetLabelResolver); a
// session id absent from the returned map means no custom label is
// available, which is now unconditionally true — list_jobs' label_contains
// filter falls back to the row's agent display name for every subagent row
// (see JobLabelResolver's doc comment).
func (t *DelegateTool) ResolvableLabels(ids []string) map[string]string {
	return map[string]string{}
}

// delegateTaskVisibleToCaller decides whether the caller identified by
// (callerSessionID, callerChannel, callerChatID) may see/act on task via
// action:"status" (C3, UAT 2026-07-31).
//
// pkg/gateway/websocket.go:615 mints a brand-new chatID ("webchat:" +
// uuid.New().String()) on EVERY WebSocket connection — a page refresh, a
// network blip, or any client that opens one connection per message all
// rotate it. Worse, for webchat specifically Channel is a fixed literal
// ("webchat", websocket.go:1707) shared by every webchat conversation, so
// chatID was the ONLY thing giving that channel any per-conversation
// isolation at all — and it is exactly what a reconnect breaks. The result
// (pre-fix): a status lookup for a task dispatched in a prior turn, on the
// SAME durable conversation, reported "No subagent found" the instant the
// browser reconnected, even though the task was very much alive (peek/
// list_jobs, which key off the durable session id, kept reporting it
// correctly the whole time).
//
// executeStatus implements action:"status". session_id is the only way to
// address a child (ADR-091 deleted the legacy task_id/in-memory-task-index
// addressing scheme this action used to also accept — see delegate.go's
// package doc comment for the history): it resolves the child's own durable
// session.LifecycleRecord directly, which survives a process restart and a
// caller's WebSocket reconnect alike, so there is no separate reconnect- or
// conversation-scoping question left for this action to answer — ownership
// is verified against the record itself (verifyCallerOwnsSession, called
// inside executeDurableStatus below).
func (t *DelegateTool) executeStatus(ctx context.Context, args map[string]any) *ToolResult {
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	return t.executeDurableStatus(ctx, sessionID)
}

type delegateInboxLatestStore interface {
	Latest(ownerKey, childSessionID string) (*generated.SessionMessage, error)
}

type delegateStatusMessageEnvelope struct {
	CreatedAt   time.Time `json:"created_at"`
	Kind        string    `json:"kind"`
	Text        string    `json:"text"`
	Summary     string    `json:"summary"`
	ResultSoFar string    `json:"result_so_far"`
	Condition   string    `json:"condition"`
	Note        string    `json:"note"`
}

func (t *DelegateTool) executeDurableStatus(ctx context.Context, sessionID string) *ToolResult {
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, err := t.lifecycle.Load(sessionID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("No subagent found with session ID: %s", sessionID))
	}
	if err := t.verifyCallerOwnsSession(ctx, rec); err != nil {
		return ErrorResult(fmt.Sprintf("No subagent found with session ID: %s", sessionID))
	}

	state := string(rec.State)
	// extra is the G1-fix trailing annotation (live tool-call-argument
	// progress plus recent transcript activity) for a running child — see
	// delegateStatusExtra's own doc comment. Computed once and appended to
	// whichever of the return points below fires; "" for every non-running
	// state, so it is a no-op append there.
	extra := t.delegateStatusExtra(rec, sessionID)
	if t.inbox == nil {
		return NewToolResult(fmt.Sprintf("%s, no message yet, started %s ago", state, formatDelegateStatusAge(t.now().Sub(rec.CreatedAt))) + extra)
	}

	var latest *generated.SessionMessage
	if store, ok := t.inbox.(delegateInboxLatestStore); ok {
		latest, err = store.Latest(rec.SteeringSessionID(), sessionID)
	} else {
		var msgs []generated.SessionMessage
		msgs, _, _, err = t.inbox.Drain(rec.SteeringSessionID(), sessionID, "", 0)
		if len(msgs) > 0 {
			latest = &msgs[len(msgs)-1]
		}
	}
	if err != nil {
		return ErrorResult(fmt.Sprintf("delegate: status: %v", err)).WithError(err)
	}
	if latest == nil {
		return NewToolResult(fmt.Sprintf("%s, no message yet, started %s ago", state, formatDelegateStatusAge(t.now().Sub(rec.CreatedAt))) + extra)
	}

	raw, err := json.Marshal(latest)
	if err != nil {
		return ErrorResult(fmt.Sprintf("delegate: status: encode latest inbox entry: %v", err))
	}
	var envelope delegateStatusMessageEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ErrorResult(fmt.Sprintf("delegate: status: decode latest inbox entry: %v", err))
	}
	line := firstNonBlank(envelope.Text, envelope.Summary, envelope.ResultSoFar, envelope.Condition, envelope.Note, envelope.Kind)
	return NewToolResult(fmt.Sprintf("%s, %s, %s ago", state, line, formatDelegateStatusAge(t.now().Sub(envelope.CreatedAt))) + extra)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "message received"
}

func formatDelegateStatusAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	seconds := int(age / time.Second)
	switch {
	case seconds < 60:
		return fmt.Sprintf("%d s", seconds)
	case seconds < 60*60:
		return fmt.Sprintf("%d min", seconds/60)
	case seconds < 24*60*60:
		return fmt.Sprintf("%d h", seconds/(60*60))
	default:
		return fmt.Sprintf("%d d", seconds/(24*60*60))
	}
}

// maxStatusActivityLines caps how many of a running child's most recent
// transcript entries action:"status" surfaces (W2). Fixed at ~5 per spec —
// enough to convey what the delegate is currently doing without flooding
// the calling LLM's context on every poll.
const maxStatusActivityLines = 5

// delegate3PStatusNote is the fixed action:"status" annotation for a running
// external-CLI (subagent_3p) session — see session.LifecycleRecord.Is3P's
// own doc comment for why no live snapshot is attempted for these.
const delegate3PStatusNote = "  note:   external agent — no live progress; results on completion"

// delegateStatusExtra computes action:"status"'s trailing annotation for the
// child addressed by sessionID, whose own durable record is rec (W2, G1).
// Only a running session gets anything:
//   - a native session gets up to maxStatusActivityLines of its own recent
//     transcript activity (recentActivityLines), plus live tool-call-argument
//     progress when a progress reader is wired, or "" if neither has
//     anything yet;
//   - an external-CLI (Is3P) session gets the fixed delegate3PStatusNote
//     instead of any attempted snapshot (batch/report-on-completion by
//     design).
//
// Every non-running session (queued/needs_input/completed/failed/canceled)
// returns "" — the durable inbox message executeDurableStatus's caller
// already renders carries whatever there is to say for those.
//
// sessionID doubles as the ADR-053 durable session_id every consumer here
// keys its own state by — the progress reader's ProgressForSession and
// recentActivityLines' own transcript read both address a child by this
// exact id, so no separate "delegate session id" field is needed once
// session_id is the only way to address a child (ADR-091).
func (t *DelegateTool) delegateStatusExtra(rec *session.LifecycleRecord, sessionID string) string {
	if rec.State != session.LifecycleRunning {
		return ""
	}
	if rec.Is3P {
		return "\n" + delegate3PStatusNote
	}

	var sb strings.Builder

	// G1 fix: check LIVE tool-call-argument progress first, before falling
	// back to the persisted-transcript snapshot below. A model mid-stream on
	// a large tool-call argument writes NOTHING to the persisted transcript
	// until its LLM round completes (see DelegateProgressReader's doc
	// comment) — this is precisely the window recentActivityLines alone
	// cannot see, and precisely the window that got a genuinely-working
	// child killed as "hung" in production.
	if t.progressReader != nil {
		if snap, ok := t.progressReader.ProgressForSession(sessionID); ok {
			sb.WriteString(formatToolCallProgressLine(snap))
		}
	}

	// rec.Origin.CallID is the durable equivalent of the pre-ADR-091
	// DelegateTaskState.SpawnCallID: the originating delegate tool-call's own
	// id, stamped once at launch time (steer.Origin's own doc comment) and
	// carried on the record itself rather than in a per-process index that
	// cannot survive a restart. Empty on a record written before ADR-091.
	spawnCallID := ""
	if rec.Origin != nil {
		spawnCallID = rec.Origin.CallID
	}
	lines := t.recentActivityLines(sessionID, spawnCallID, maxStatusActivityLines)
	if len(lines) == 0 {
		if sb.Len() == 0 {
			return ""
		}
		return "\n" + sb.String()
	}
	if sb.Len() > 0 {
		sb.WriteString("\n")
	}
	sb.WriteString("  recent activity:")
	for _, line := range lines {
		sb.WriteString("\n    - ")
		sb.WriteString(line)
	}
	return "\n" + sb.String()
}

// maxToolCallProgressStaleness caps how long ago a recorded tool-call
// progress update may be while still rendering as "generating" rather than
// "stale" (G1 fix). Generous on purpose: the underlying callback only fires
// on argument GROWTH (see protocoltypes.OnToolCallProgress's doc comment),
// so a model that paused between deltas — a slow provider round-trip,
// network jitter — should not read as hung just because its LAST delta was
// a while ago. It exists only so a turnState whose progress was never
// cleared (a crash mid-stream, rather than a clean turn end) does not
// masquerade as live forever; delegateStatusExtra only ever renders this
// note for a task whose Status is still "running" in the first place.
const maxToolCallProgressStaleness = 5 * time.Minute

// formatToolCallProgressLine renders a DelegateProgressReader snapshot as
// the leading line of action:"status"'s extra section (G1 fix) — the signal
// that lets a caller tell "still generating a large tool-call argument"
// apart from "hung", which is the whole point of this feature. See
// delegateStatusExtra's call site and DelegateProgressReader's doc comment
// for the incident this closes.
func formatToolCallProgressLine(snap ToolCallProgressSnapshot) string {
	// The most recent delta was reasoning, not a tool-call argument: the
	// model is thinking. Saying "generating tool call" here would be false —
	// there is no tool call yet (founder decision 2026-09-14).
	if snap.ArgsBytes == 0 && snap.ReasoningBytes > 0 {
		verb := "thinking"
		if snap.Age > maxToolCallProgressStaleness {
			verb = "stale — no update recently, may have stalled while thinking"
		}
		return fmt.Sprintf("  progress: %s — %d bytes of reasoning so far this round, last update %s ago",
			verb, snap.ReasoningBytes, snap.Age.Round(time.Second))
	}
	name := snap.Name
	if name == "" {
		name = "(name pending)"
	}
	verb := "generating"
	if snap.Age > maxToolCallProgressStaleness {
		verb = "stale — no update recently, may have stalled while generating"
	}
	if snap.TotalArgsBytes > snap.ArgsBytes {
		return fmt.Sprintf("  progress: %s tool call %q — %d bytes (%d bytes total this round), last update %s ago",
			verb, name, snap.ArgsBytes, snap.TotalArgsBytes, snap.Age.Round(time.Second))
	}
	return fmt.Sprintf("  progress: %s tool call %q — %d bytes, last update %s ago",
		verb, name, snap.ArgsBytes, snap.Age.Round(time.Second))
}

// maxStatusActivityLineRunes caps each surfaced activity line's length (W2).
const maxStatusActivityLineRunes = 120

// recentActivityLines reads back up to max of the most recent transcript
// entries a running NATIVE delegated sub-turn has written into its OWN
// durable session (ADR-057 FR-043 — sessionID here is the child's
// DelegateSessionID, not the shared parent transcript id; see
// delegateStatusExtra's call site), filtered to just this task's own
// activity via session.TranscriptEntry.ParentSpawnCallID == spawnCallID —
// the delegate tool call's own ID, which subturn.go stamps onto every
// intermediate/final assistant-text entry the child sub-turn produces (see
// ParentSpawnCallID's doc comment). This is a pure READ of data the child
// sub-turn already persists as a side effect of running — no new storage or
// write path is introduced by W2.
//
// Returns nil (never an error) when no session store is wired, sessionID or
// spawnCallID is empty, the transcript can't be read, or nothing has been
// written yet — delegateStatusExtra treats all of these as "no snapshot
// available" and falls back to the prompt-only summary that predates this
// feature. The last of those cases — a clean read that simply found no
// matching entries yet — is logged (ADR-057 FR-043/BDD-51): a genuinely
// empty activity path must leave a trace an operator can find, not degrade
// silently into the exact same "nothing available" shape as an unwired
// store or a transcript-read error.
func (t *DelegateTool) recentActivityLines(sessionID, spawnCallID string, maxLines int) []string {
	if t.sessionStore == nil || sessionID == "" || spawnCallID == "" {
		return nil
	}
	entries, err := t.sessionStore.ReadTranscript(sessionID)
	if err != nil {
		slog.Warn("delegate: status snapshot: failed to read transcript",
			"session_id", sessionID, "error", err)
		return nil
	}

	var lines []string
	for _, e := range entries {
		if e.ParentSpawnCallID != spawnCallID {
			continue
		}
		content := strings.TrimSpace(e.Content)
		if content == "" {
			continue
		}
		runes := []rune(content)
		if len(runes) > maxStatusActivityLineRunes {
			content = string(runes[:maxStatusActivityLineRunes]) + "…"
		}
		lines = append(lines, content)
	}
	if len(lines) == 0 {
		slog.Info("delegate: status snapshot: no recent activity found for this task yet",
			"session_id", sessionID, "spawn_call_id", spawnCallID)
		return nil
	}
	// Entries are in chronological (append) order — keep only the most
	// recent `maxLines`, preserving chronological order within that window.
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines
}

func (t *DelegateTool) executeInbox(ctx context.Context, args map[string]any) *ToolResult {
	if t.inbox == nil {
		return ErrorResult("delegate: no message inbox configured")
	}
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	ownerKey := callerOwnerKey(ctx)
	if ownerKey == "" {
		return ErrorResult("delegate: no session context available to resolve the inbox owner key")
	}
	// FAIL-CLOSED (Security-MAJOR-1): caller-ownership verification is
	// MANDATORY — a Load error (not-found, corrupt tail, I/O) MUST NOT let
	// the inbox read fall through against whatever session_id the caller
	// named (a cross-tenant read gated on an induced read error — peek/inbox
	// would leak the victim's lifecycle state + persisted messages).
	// Previously the ownership check only ran when Load SUCCEEDED, so any
	// Load error skipped it and the inbox was drained regardless. Now deny
	// when the lifecycle store is unconfigured OR when Load errors, mirroring
	// executeCancel/executeSteer/executeRespond/executeFollowUp's posture
	// (the rest of ADR-053's fail-closed contract — see delegate.go:1808).
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox: %v", verr))
	}

	sinceCursor, _ := args["since_cursor"].(string)
	maxMessages := 0
	if raw, present := args["max"]; present && raw != nil {
		n, cerr := toIntArg(raw)
		if cerr != nil {
			return ErrorResult("max must be an integer")
		}
		maxMessages = n
	}

	// MEDIUM-2 (14-reviewer sign-off): key the Drain by rec.SteeringSessionID()
	// (the target session's own DIRECT parent — the key its messages were
	// actually Appended under), NOT the calling ownerKey. verifyCallerOwnsSession
	// above already grants an authorized ANCESTOR (grandparent, etc., up to
	// SetOwnershipWalkMaxDepth — FR-039) reach into a descendant's inbox, but
	// a message is always stored under the child's own direct parent's key
	// (message_parent.go's Append call), never the calling ancestor's. Keying
	// this read by ownerKey silently returned an empty inbox for exactly the
	// authorized-ancestor case FR-039 exists to permit — the ownerKey
	// variable above and its own presence check remain (a caller must still
	// have SOME resolvable session identity to reach this far at all), but
	// the store key must be the target's own SteeringSessionID. executeRespond
	// already uses this correct key (see its own Drain call).
	msgs, nextCursor, hasMore, derr := t.inbox.Drain(rec.SteeringSessionID(), sessionID, sinceCursor, maxMessages)
	if derr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox: %v", derr)).WithError(derr)
	}

	resp := generated.DelegateInboxResponse{Messages: msgs, HasMore: hasMore}
	if nextCursor != "" {
		resp.NextCursor = &nextCursor
	}
	payload, merr := json.Marshal(resp)
	if merr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox: encode response: %v", merr))
	}
	return NewToolResult(string(payload))
}

func (t *DelegateTool) executeInboxAck(ctx context.Context, args map[string]any) *ToolResult {
	if t.inbox == nil {
		return ErrorResult("delegate: no message inbox configured")
	}
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	ids, serr := stringSliceArg(args, "message_ids")
	if serr != nil {
		return ErrorResult(serr.Error())
	}
	if len(ids) == 0 {
		return ErrorResult("message_ids is required and must be a non-empty array of strings")
	}
	ownerKey := callerOwnerKey(ctx)
	if ownerKey == "" {
		return ErrorResult("delegate: no session context available to resolve the inbox owner key")
	}
	// HIGH (nested-delegation message leak, 2026-08): the READ path
	// (executeInbox/executePeek) was re-keyed to rec.SteeringSessionID() but the
	// ACK path was left on the calling ownerKey, so read and ack disagreed
	// for every caller that is not the target's DIRECT parent. Because
	// verifyCallerOwnsSession deliberately permits an ANCESTOR (FR-039) —
	// whose key is by definition NOT rec.SteeringSessionID() — an A -> B -> C
	// chain let A drain C's question successfully and then ack it against
	// A's OWN inbox file: every id came back Unknown, nothing was actually
	// acknowledged, the messages were redelivered on every subsequent drain,
	// and they permanently consumed C's InboxUnackedMax budget until C's
	// message_parent sends started failing outright (the ceiling is enforced
	// in pkg/session/message_inbox.go::MessageInboxStore.Append against the
	// child's own owner key, which only an ack under THAT key can relieve).
	// The ack must therefore be keyed exactly like the read: by the target
	// session's own direct parent, the key its messages were actually
	// Appended under.
	//
	// Loading the record also closes a second gap this action had: keying by
	// rec.SteeringSessionID() without an ownership check would let any caller
	// ack messages in an inbox it does not own, so the same MANDATORY,
	// fail-closed verification executeInbox performs is applied here (a Load
	// error denies — it never falls through to whatever session_id the
	// caller named).
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox_ack: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox_ack: %v", verr))
	}

	result, err := t.inbox.AckDetailed(rec.SteeringSessionID(), ids)
	if err != nil {
		return ErrorResult(fmt.Sprintf("delegate: inbox_ack: %v", err)).WithError(err)
	}
	// M1 (UAT 2026-08): report the TRUTHFUL acknowledged count — a wholly
	// fabricated or already-unknown message_id must never inflate it — and
	// surface any unknown ids explicitly rather than silently folding them
	// into "success," so a caller reconciling its inbox against this count
	// notices the drift instead of trusting a number that doesn't match what
	// actually happened.
	msg := fmt.Sprintf("Acknowledged %d message(s).", len(result.Acknowledged))
	if len(result.Unknown) > 0 {
		msg += fmt.Sprintf(" %d message ID(s) were not recognized and could not be acknowledged: %s.",
			len(result.Unknown), strings.Join(result.Unknown, ", "))
	}
	return NewToolResult(msg)
}

func (t *DelegateTool) executePeek(ctx context.Context, args map[string]any) *ToolResult {
	if t.inbox == nil {
		return ErrorResult("delegate: no message inbox configured")
	}
	sessionID, err := requiredStringArg(args, "session_id")
	if err != nil {
		return ErrorResult(err.Error())
	}
	ownerKey := callerOwnerKey(ctx)
	if ownerKey == "" {
		return ErrorResult("delegate: no session context available to resolve the inbox owner key")
	}

	// FAIL-CLOSED (Security-MAJOR-1): caller-ownership verification is
	// MANDATORY — same posture as executeInbox above. The prior `if lerr == nil`
	// pattern skipped ownership verification on ANY Load error, so peek
	// leaked the victim's lifecycle state (info disclosure) whenever Load
	// errored. Now deny when the lifecycle store is unconfigured OR when Load
	// errors OR when ownership mismatches.
	if t.lifecycle == nil {
		return ErrorResult("delegate: no lifecycle store configured")
	}
	rec, lerr := t.lifecycle.Load(sessionID)
	if lerr != nil {
		return ErrorResult(fmt.Sprintf("delegate: peek: %v", lerr))
	}
	if verr := t.verifyCallerOwnsSession(ctx, rec); verr != nil {
		return ErrorResult(fmt.Sprintf("delegate: peek: %v", verr))
	}
	state := string(rec.State)

	// MEDIUM-2 (14-reviewer sign-off): key the Peek by rec.SteeringSessionID(),
	// not the calling ownerKey — see executeInbox's identical fix above for
	// the full rationale (FR-039 grants an authorized ancestor reach beyond
	// the direct parent, but messages are always stored under the target's
	// own direct parent's key).
	snap, perr := t.inbox.Peek(rec.SteeringSessionID(), sessionID)
	if perr != nil {
		return ErrorResult(fmt.Sprintf("delegate: peek: %v", perr)).WithError(perr)
	}

	resp := generated.DelegatePeekResponse{
		SessionId: sessionID,
		State:     generated.DelegatePeekResponseState(state),
	}
	if snap.HasCheckpoint {
		summary := snap.LatestCheckpointSummary
		resp.LatestCheckpointSummary = &summary
	}
	if snap.HasProgress {
		text := snap.LatestProgressText
		resp.LatestProgressText = &text
		resp.LatestProgressPct = snap.LatestProgressPct
	}
	payload, merr := json.Marshal(resp)
	if merr != nil {
		return NewToolResult(fmt.Sprintf("session %s: state=%s", sessionID, state))
	}
	return NewToolResult(string(payload))
}
