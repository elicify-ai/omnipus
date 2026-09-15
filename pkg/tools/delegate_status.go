// delegate_status.go: Poll and report on a delegation — task status, live activity, child inbox messages, and checkpoint peek.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ResolvableSessionIDs implements tools.JobSessionResolver (#583): it reports
// which of the given delegate session ids (ADR-053 durable session_id, the
// same id space as DelegateTaskState.DelegateSessionID/t.sessionIndex's keys)
// are still resolvable in THIS process's in-memory delegate index — the
// signal list_jobs uses to decide whether a subagent row's session_id can
// actually be acted on right now (status/inbox/steer/respond/cancel/
// follow_up/peek) rather than failing on use. A durable LifecycleRecord can
// survive a process restart while this in-memory index does not; such a
// session is correctly reported unresolvable here (FR-011).
//
// Single lock acquisition for the WHOLE batch (FR-028, matching the
// JobSessionResolver interface's own doc comment): the underlying index is
// guarded by t.mu, the same mutex every delegate status/inbox/steer/respond/
// cancel call already takes, so resolving one id per row would put a
// read-only visibility tool in contention with the live dispatch path.
func (t *DelegateTool) ResolvableSessionIDs(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range ids {
		_, ok := t.sessionIndex[id]
		out[id] = ok
	}
	return out
}

// ResolvableLabels implements tools.JobLabelResolver (UAT M3, 2026-08-03):
// for each delegate session id given, it reports the caller-supplied `label`
// argument (delegate(..., label:"...")) recorded in THIS process's
// in-memory task-state index at dispatch time — see JobLabelResolver's own
// doc comment (pkg/tools/list_jobs_sources.go) for why no durable field
// exists to read instead (session.LifecycleRecord carries no Label at all;
// DelegateTaskState.Label plus a one-shot subagent_start WS payload are the
// only places a custom label ever lives).
//
// Mirrors ResolvableSessionIDs exactly: a single t.mu acquisition for the
// WHOLE batch (FR-028 — the same contract JobSessionResolver's own doc
// comment documents), so this read-only visibility call never contends with
// the live dispatch path over the same lock.
//
// A session id absent from t.sessionIndex, or whose task has no Label set,
// is simply omitted from the returned map — never an error; list_jobs falls
// back to the row's already-resolved agent display name for that case (see
// JobLabelResolver's doc comment).
func (t *DelegateTool) ResolvableLabels(ids []string) map[string]string {
	out := make(map[string]string, len(ids))
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range ids {
		taskID, ok := t.sessionIndex[id]
		if !ok {
			continue
		}
		task, ok := t.tasks[taskID]
		if !ok || task.Label == "" {
			continue
		}
		out[id] = task.Label
	}
	return out
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
// The durable identity that DOES survive a reconnect is the ADR-053 session
// id: the client resends the SAME session_id on every reconnect
// (websocket.go's `sessionID` local only ever gets a NEW session minted when
// the frame carries none at all — see websocket.go:1489 vs :1563), and
// DelegateTaskState.SessionID already captures it at dispatch time (this
// file's executeAsync, via ToolTranscriptSessionID(ctx) — the parent
// turn's own durable transcript session id).
//
// So: when BOTH the caller and the task carry a durable session id, that id
// is the authoritative scope check — it survives the caller's chatID
// rotating out from under it, and (being an unguessable, per-conversation
// identifier, unlike the shared "webchat" channel literal) is at least as
// strong an isolation boundary as the legacy channel/chatID pair for the
// cross-conversation case. When either side lacks a durable session id (a
// direct programmatic Execute call, a task registered before any transcript
// session was bound, or a non-webchat channel that never threads one
// through), this falls back to the pre-existing channel+chatID comparison
// unchanged — preserving check_spawn_status's original scoping exactly for
// every caller this fix does not need to touch.
func delegateTaskVisibleToCaller(callerSessionID, callerChannel, callerChatID string, task *DelegateTaskState) bool {
	if callerSessionID != "" && task.SessionID != "" {
		return callerSessionID == task.SessionID
	}
	if callerChannel != "" && task.OriginChannel != "" && task.OriginChannel != callerChannel {
		return false
	}
	if callerChatID != "" && task.OriginChatID != "" && task.OriginChatID != callerChatID {
		return false
	}
	return true
}

// executeStatus implements action:"status". It resolves against the exact
// same t.tasks map the async path writes to (FR-D2) and preserves
// check_spawn_status's channel/chatID scoping exactly: a lookup or listing is
// restricted to tasks that originated from the SAME conversation, and all
// tasks are listed only when no channel/chat context is injected at all
// (e.g. direct programmatic Execute calls). C3 (UAT 2026-07-31): the scope
// check now prefers the durable ADR-053 session id (delegateTaskVisibleToCaller)
// whenever both sides have one, since that identity survives a WebSocket
// reconnect where callerChatID does not — see that function's doc comment
// for the full rationale.
func (t *DelegateTool) executeStatus(ctx context.Context, args map[string]any) *ToolResult {
	callerChannel := ToolChannel(ctx)
	callerChatID := ToolChatID(ctx)
	callerSessionID := ToolTranscriptSessionID(ctx)

	var taskID string
	if rawTaskID, ok := args["task_id"]; ok && rawTaskID != nil {
		taskIDStr, ok := rawTaskID.(string)
		if !ok {
			return ErrorResult("task_id must be a string")
		}
		taskID = strings.TrimSpace(taskIDStr)
	}
	// session_id (ADR-053) wins when both are present (DelegateStatusAction's
	// documented precedence); task_id survives as a deprecated alias.
	if rawSessionID, ok := args["session_id"]; ok && rawSessionID != nil {
		sessionIDStr, ok := rawSessionID.(string)
		if !ok {
			return ErrorResult("session_id must be a string")
		}
		if sid := strings.TrimSpace(sessionIDStr); sid != "" {
			t.mu.Lock()
			resolved, found := t.sessionIndex[sid]
			t.mu.Unlock()
			if !found {
				return ErrorResult(fmt.Sprintf("No subagent found with session ID: %s", sid))
			}
			taskID = resolved
		}
	}

	if taskID != "" {
		taskCopy, ok := t.getTaskCopy(taskID)
		if !ok {
			// Genuine absence: log distinctly from the scope-mismatch branch
			// below so an operator can tell the two apart, even though the
			// caller-visible message is identical for both (UAT 2026-07-31 —
			// the two paths were previously indistinguishable to anyone
			// debugging a "status went blind" report).
			slog.Debug("delegate: status lookup — task not found", "task_id", taskID)
			return ErrorResult(fmt.Sprintf("No subagent found with task ID: %s", taskID))
		}

		// Restrict lookup to tasks visible to this conversation — see
		// delegateTaskVisibleToCaller's doc comment (C3 fix: the durable
		// session id takes priority over the legacy channel/chatID pair
		// whenever both sides have one, since only the session id survives a
		// WebSocket reconnect).
		if !delegateTaskVisibleToCaller(callerSessionID, callerChannel, callerChatID, &taskCopy) {
			// Deliberately the SAME caller-visible "not found" message a
			// genuine miss returns above — never confirm to an untrusted
			// caller that a task exists in a DIFFERENT conversation — but
			// logged distinctly for diagnosability.
			slog.Debug("delegate: status lookup — task exists but is not visible to this caller (scope mismatch)",
				"task_id", taskID,
				"caller_session_id", callerSessionID,
				"task_session_id", taskCopy.SessionID,
				"caller_channel", callerChannel,
				"task_channel", taskCopy.OriginChannel,
			)
			return ErrorResult(fmt.Sprintf("No subagent found with task ID: %s", taskID))
		}

		return NewToolResult(delegateFormatTask(&taskCopy, t.delegateStatusExtra(&taskCopy)))
	}

	origTasks := t.listTaskCopies()
	if len(origTasks) == 0 {
		return NewToolResult("No subagents have been spawned yet.")
	}

	taskList := make([]*DelegateTaskState, 0, len(origTasks))
	for i := range origTasks {
		cpy := &origTasks[i]

		// Filter to tasks visible to the current conversation only — see
		// delegateTaskVisibleToCaller's doc comment.
		if !delegateTaskVisibleToCaller(callerSessionID, callerChannel, callerChatID, cpy) {
			continue
		}

		taskList = append(taskList, cpy)
	}

	if len(taskList) == 0 {
		return NewToolResult("No subagents found for this conversation.")
	}

	// Order by creation time (ascending) so spawning order is preserved.
	// Fall back to ID string for tasks created in the same millisecond.
	sort.Slice(taskList, func(i, j int) bool {
		if taskList[i].Created != taskList[j].Created {
			return taskList[i].Created < taskList[j].Created
		}
		return taskList[i].ID < taskList[j].ID
	})

	counts := map[string]int{}
	for _, task := range taskList {
		counts[task.Status]++
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Subagent status report (%d total):\n", len(taskList))
	for _, status := range []string{"running", "completed", "failed", "canceled"} {
		if n := counts[status]; n > 0 {
			label := strings.ToUpper(status[:1]) + status[1:] + ":"
			fmt.Fprintf(&sb, "  %-10s %d\n", label, n)
		}
	}
	sb.WriteString("\n")

	for _, task := range taskList {
		sb.WriteString(delegateFormatTask(task, t.delegateStatusExtra(task)))
		sb.WriteString("\n\n")
	}

	return NewToolResult(strings.TrimRight(sb.String(), "\n"))
}

// getTaskCopy returns a copy of the task with the given ID, taken under the
// lock, so the caller receives a consistent snapshot with no data race.
// ADR-057 FR-045: this is action:"status"'s single-task read path, so it
// also stamps LastStatusRead — resetting the eviction clock on the task's
// own stored record (not just the returned copy) so a task under active
// polling is never reclaimed by evictStaleTasksLocked out from under the
// caller (BDD-52's "But" clause, test #93).
func (t *DelegateTool) getTaskCopy(taskID string) (DelegateTaskState, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	task, ok := t.tasks[taskID]
	if !ok {
		return DelegateTaskState{}, false
	}
	task.LastStatusRead = t.now().UnixMilli()
	return *task, true
}

// listTaskCopies returns value copies of all tasks, taken under the lock, so
// callers receive consistent snapshots with no data race. ADR-057 FR-045:
// this backs action:"status"'s list-all-tasks path (no task_id/session_id
// given).
//
// Deliberately does NOT stamp LastStatusRead, unlike getTaskCopy. getTaskCopy
// is a targeted lookup by task_id/session_id — a genuine "I am actively
// polling THIS task" signal that legitimately resets its own eviction clock
// (BDD-52's "But" clause). A bare list-all read is not scoped to any one
// task the caller is following; it previously stamped EVERY task in the
// entire map (including other conversations' — the channel/chatID filter
// executeStatus applies happens AFTER this call returns) on every plain
// action:"status" call with no target, refreshing the eviction clock on the
// whole fleet and starving evictStaleTasksLocked (and the taskCap it now
// also enforces) indefinitely regardless of the configured retention
// policy. Copies are returned exactly as stored.
func (t *DelegateTool) listTaskCopies() []DelegateTaskState {
	t.mu.Lock()
	defer t.mu.Unlock()

	copies := make([]DelegateTaskState, 0, len(t.tasks))
	for _, task := range t.tasks {
		copies = append(copies, *task)
	}
	return copies
}

// delegateFormatTask renders a single DelegateTaskState as a human-readable
// block. extra, when non-empty, is appended as a trailing "\n"+extra section
// — used by action:"status" (W2) to attach either a running native task's
// recent transcript activity or a running external-CLI task's
// no-live-progress note (see delegateStatusExtra). Pass "" for a task with
// nothing to add (a non-running task, or a running native task with no
// activity captured yet) — this keeps the function's output identical to
// its pre-W2 shape for those cases.
func delegateFormatTask(task *DelegateTaskState, extra string) string {
	var sb strings.Builder

	header := fmt.Sprintf("[%s] status=%s", task.ID, task.Status)
	if task.Label != "" {
		header += fmt.Sprintf("  label=%q", task.Label)
	}
	if task.AgentID != "" {
		header += fmt.Sprintf("  agent=%s", task.AgentID)
	}
	if task.Created > 0 {
		created := time.UnixMilli(task.Created).UTC().Format("2006-01-02 15:04:05 UTC")
		header += fmt.Sprintf("  created=%s", created)
	}
	sb.WriteString(header)

	if task.Task != "" {
		fmt.Fprintf(&sb, "\n  task:   %s", task.Task)
	}
	if task.Result != "" {
		result := task.Result
		const maxResultLen = 300
		runes := []rune(result)
		if len(runes) > maxResultLen {
			result = string(runes[:maxResultLen]) + "…"
		}
		fmt.Fprintf(&sb, "\n  result: %s", result)
	}
	if extra != "" {
		sb.WriteString("\n")
		sb.WriteString(extra)
	}

	return sb.String()
}

// maxStatusActivityLines caps how many of a running native task's most
// recent transcript entries action:"status" surfaces (W2). Fixed at ~5 per
// spec — enough to convey what the delegate is currently doing without
// flooding the calling LLM's context on every poll.
const maxStatusActivityLines = 5

// delegate3PStatusNote is the fixed action:"status" annotation for a running
// external-CLI (subagent_3p) task — see DelegateTaskState.Is3P's doc comment
// for why no live snapshot is attempted for these.
const delegate3PStatusNote = "  note:   external agent — no live progress; results on completion"

// delegateStatusExtra computes the action:"status" trailing annotation for
// task (W2). Only a "running" task gets anything:
//   - a native task gets up to maxStatusActivityLines of its own recent
//     transcript activity (recentActivityLines), or "" if the child sub-turn
//     hasn't written anything yet;
//   - an external-CLI (Is3P) task gets the fixed delegate3PStatusNote instead
//     of any attempted snapshot (batch/report-on-completion by design).
//
// Every non-running task (completed/failed/canceled) returns "" — its
// Result field already carries the final answer, so nothing is added.
func (t *DelegateTool) delegateStatusExtra(task *DelegateTaskState) string {
	if task.Status != "running" {
		return ""
	}
	if task.Is3P {
		return delegate3PStatusNote
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
		if snap, ok := t.progressReader.ProgressForSession(task.DelegateSessionID); ok {
			sb.WriteString(formatToolCallProgressLine(snap))
		}
	}

	// ADR-057 FR-043: read the child's OWN durable session (DelegateSessionID),
	// not task.SessionID (the delegating PARENT's own transcript id — see
	// its doc comment). Post-FR-007 a delegated child writes its own
	// narration into its own session, so reading task.SessionID here always
	// found nothing the moment FR-007 landed elsewhere in this change set —
	// BDD-49/BDD-50 (a sync or async delegation's status snapshot must be
	// non-empty) were silently broken until this re-point.
	lines := t.recentActivityLines(task.DelegateSessionID, task.SpawnCallID, maxStatusActivityLines)
	if len(lines) == 0 {
		return sb.String()
	}
	if sb.Len() > 0 {
		sb.WriteString("\n")
	}
	sb.WriteString("  recent activity:")
	for _, line := range lines {
		sb.WriteString("\n    - ")
		sb.WriteString(line)
	}
	return sb.String()
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

	// MEDIUM-2 (14-reviewer sign-off): key the Drain by rec.ParentDurableKey
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
	// the store key must be the target's own ParentDurableKey. executeRespond
	// already uses this correct key (see its own Drain call).
	msgs, nextCursor, hasMore, derr := t.inbox.Drain(rec.ParentDurableKey, sessionID, sinceCursor, maxMessages)
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
	// (executeInbox/executePeek) was re-keyed to rec.ParentDurableKey but the
	// ACK path was left on the calling ownerKey, so read and ack disagreed
	// for every caller that is not the target's DIRECT parent. Because
	// verifyCallerOwnsSession deliberately permits an ANCESTOR (FR-039) —
	// whose key is by definition NOT rec.ParentDurableKey — an A -> B -> C
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
	// rec.ParentDurableKey without an ownership check would let any caller
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

	result, err := t.inbox.AckDetailed(rec.ParentDurableKey, ids)
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

	// MEDIUM-2 (14-reviewer sign-off): key the Peek by rec.ParentDurableKey,
	// not the calling ownerKey — see executeInbox's identical fix above for
	// the full rationale (FR-039 grants an authorized ancestor reach beyond
	// the direct parent, but messages are always stored under the target's
	// own direct parent's key).
	snap, perr := t.inbox.Peek(rec.ParentDurableKey, sessionID)
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
