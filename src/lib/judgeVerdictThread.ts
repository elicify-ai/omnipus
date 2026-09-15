// Judge verdict thread card — live/replay session_id fix (2026-09-14).
//
// The `judge_verdict` WS frame (contracts/components/schemas/
// JudgeVerdictFrame.yaml) was a GLOBAL frame (correlated by task_id/plan_id
// only), fed exclusively into the ActivityPanel's useJudgeActivityStore
// (store/chat.ts's `case 'judge_verdict'`). Commit a9ab15f44 made a COLD
// REST load of the persisted `judge_verdict` transcript entry ALSO render a
// thread card (`mergeJudgeVerdictHistory`), but explicitly could not do so
// LIVE — the frame carried no session_id at all, so there was no session to
// insert into (see that commit's doc comment and this module's own git
// history for the follow-up it flagged).
//
// The frame now OPTIONALLY carries `session_id` — present for `scope: task`
// (the task's run session) and `scope: goal` (the `/goal` session itself),
// absent for `scope: plan` (a plan round has no single owning chat session;
// plan-scope verdicts also don't reach this event at all today —
// plan_engine.go writes no judge_verdict transcript entry). When present,
// this module builds the SAME thread-card shape mergeJudgeVerdictHistory
// produces for a cold load, keyed by the SAME id the backend transcript
// entry uses (task_executor.go's writeJudgeVerdictTranscript:
// `"%s-judge-%d"`; goal_loop.go's writeGoalVerdictTranscript:
// `"goal-%s-judge-%d"`) so a live push, a replay of the same round, and a
// later cold REST load of the persisted entry all converge on exactly one
// card — mergeJudgeVerdictHistory's own id-based dedup
// (`draft.messagesById[verdictMsg.id]`) recognizes a card this module
// already inserted and skips it, rather than inserting a second one under a
// different id.
//
// Appends at the current tail, mirroring buildGoalOutcomeInsertion /
// buildBrowserHandoverInsertion (src/lib/goalOutcome.ts, store/chat.ts):
// live, the judged round has just finished, so the tail IS where the card
// belongs; on replay, streamReplay (pkg/gateway/replay.go) walks the
// transcript strictly in order, so by the time it reaches this entry every
// message from the judged round is already in the bucket. This is
// deliberately NOT mergeJudgeVerdictHistory's turn-id backward-scan — that
// technique exists only to retroactively backfill an ALREADY-populated
// bucket from a separate REST array; here the frame always arrives at its
// correct chronological position already.

import type { JudgeVerdictFrame } from '@/lib/api/generated/asyncapi-types'
import type { JudgeVerdict } from '@/lib/api/generated/openapi-types'
import type { ChatMessage } from '@/store/chat'

/**
 * Derives the SAME transcript-entry id the backend mints for a task/goal
 * scope `judge_verdict` entry, so a card built from a live/replayed frame
 * converges on the exact id a REST cold-load of the persisted entry carries
 * (`Message.id`, forwarded verbatim by `rawToMessage`). Returns null when
 * the frame's scope/fields don't resolve to a known id shape — a malformed
 * frame (missing `task_id` on a `scope: task` verdict) or a scope this
 * module doesn't handle (plan verdicts never carry `session_id`, so callers
 * gate on that before reaching here).
 */
export function judgeVerdictEntryId(frame: JudgeVerdictFrame): string | null {
  if (frame.scope === 'task' && frame.task_id) {
    return `${frame.task_id}-judge-${frame.round}`
  }
  if (frame.scope === 'goal' && frame.session_id) {
    return `goal-${frame.session_id}-judge-${frame.round}`
  }
  return null
}

/**
 * Maps the frame's fields onto `Message.verdict`'s shape (the REST
 * cold-load carrier) — per JudgeVerdictFrame's own doc comment, the two are
 * field-for-field identical except for the frame's `type`/`session_id`
 * discriminators, which this drops. An explicit field-by-field mapping
 * (rather than a destructure-omit) so a future field added to one shape but
 * not the other is a compile error here, not a silent pass-through.
 */
export function frameToJudgeVerdict(frame: JudgeVerdictFrame): JudgeVerdict {
  return {
    id: frame.id,
    scope: frame.scope,
    task_id: frame.task_id,
    plan_id: frame.plan_id,
    round: frame.round,
    met: frame.met,
    per_criterion: frame.per_criterion,
    model: frame.model,
    judged_at: frame.judged_at,
    judge_agent_id: frame.judge_agent_id,
  }
}

/**
 * Builds the {messagesById, messageOrder} patch that inserts the thread
 * card for `frame` into bucket `b`, or returns null when: `frame` carries no
 * `session_id` (the pre-existing panel-only case, left unchanged); no id can
 * be derived (malformed frame); or a card with that id already exists in the
 * bucket. The last case is the idempotency guarantee — a live push, a replay
 * of the same round, or a repeat delivery on WS reconnect never inserts a
 * second card.
 */
export function buildJudgeVerdictInsertion(
  b: { messagesById: Record<string, ChatMessage>; messageOrder: string[] },
  frame: JudgeVerdictFrame,
): { messagesById: Record<string, ChatMessage>; messageOrder: string[] } | null {
  if (!frame.session_id) return null
  const id = judgeVerdictEntryId(frame)
  if (!id || b.messagesById[id]) return null

  const verdict = frameToJudgeVerdict(frame)
  const message: ChatMessage = {
    id,
    role: 'system',
    status: 'done',
    // Mirrors the persisted transcript entry's own Content (task_executor.go/
    // goal_loop.go write `string(json.Marshal(verdict))`) — renderers read
    // `msg.verdict` directly (JudgeVerdictThreadCard), never this field.
    content: JSON.stringify(verdict),
    timestamp: frame.judged_at,
    agentId: frame.judge_agent_id || undefined,
    type: 'judge_verdict',
    verdict,
  }
  return {
    messagesById: { ...b.messagesById, [id]: message },
    messageOrder: [...b.messageOrder, id],
  }
}
