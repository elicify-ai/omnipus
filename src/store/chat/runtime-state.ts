// runtime-state.ts: Module-scoped replay, cancellation, routing, and diagnostics state used by the store

import { emptySessionState } from './session'

// Module-scoped handle for the 60s auto-clear timer on rate-limit events, keyed per session.
export const rateLimitClearTimers: Record<string, ReturnType<typeof setTimeout>> = {}

export const RATE_LIMIT_CLEAR_MS = 60_000

// Tracks when isReplaying was most recently set to true per session, keyed by session_id.
export const replayingStartedAt: Record<string, number> = {}

// Pending setTimeout handles that will flip isReplaying=false after
// MIN_REPLAY_DISPLAY_MS - elapsed. Tracked per-session so a new
// setReplaying(true) (e.g. re-attach to the same session) can cancel the
// stale timer before it stomps the freshly-started replay window.
export const replayingClearTimers: Record<string, ReturnType<typeof setTimeout>> = {}

// Diagnostic flag per session — true when at least one replay_message was processed this turn.
export const sawReplayMessageThisTurn: Record<string, boolean> = {}

// UAT (browser-panel "Take over"): session ids with an explicit
// cancelStream(sessionId) sent to the server but no terminal (done/error)
// frame acknowledging it yet. Populated by cancelStream(), drained by the
// 'done'/'error' handlers once a frame is attributed to that session (by any
// means — matched session_id or the fallback below), and swept on socket
// disconnect. Used ONLY to disambiguate an untagged cancellation-ack-shaped
// frame (token/done/error missing session_id) that would otherwise
// misattribute to whatever session happens to be foreground — see F-S3 below.
export const pendingCancelAckSids = new Set<string>()

// #823 catch-up redesign, Opus review round 2 item 7 (LOW): applySeqGate's
// gap branch (frames.ts) sends `attach_session{S, cursor}` to recover from a
// sequence gap. Without a guard, EVERY subsequent gapped frame that arrives
// before the server responds — a burst of tokens, for instance — re-sends
// the same attach_session again, once per frame. Session ids in this set
// already have a re-attach in flight; the gap branch skips sending a second
// one while a session is a member. Cleared once the session's cursor is
// healthy again — either a normal 'apply' decision (gateFrameBySeq.ts) or a
// cursor-minting frame (session_snapshot/catch_up_complete/session_started)
// resolves it.
export const inFlightReattachSids = new Set<string>()

export const EMPTY_BUCKET = emptySessionState()

// F-S1: all server→client frames that must carry session_id.
// Frames in this set without a session_id are routing errors.
// Global frames (error, auth_*, ping, pong, device_pairing_*) are intentionally absent.
export const SESSION_SCOPED_FRAME_TYPES = new Set([
  'token', 'done', 'tool_call_start', 'tool_call_result',
  'subagent_start', 'subagent_end', 'replay_message', 'replay_done',
  'agent_switched', 'task_status_changed',
  'tool_approval_required', 'rate_limit', 'media', 'session_started',
  'system_overload', 'session_close_ack', 'cancel_stage',
  'message_status',
  // ADR-049 R3: goal_status/loop_status always carry `session_id` (schema
  // `min(1)`, required) — session-scoped like rate_limit. plan_status
  // deliberately does NOT carry session_id (correlated by plan_id instead,
  // not any specific chat thread) and is handled as a GLOBAL frame below
  // (like notification/whatsapp_pairing) — do not add it here.
  //
  // judge_verdict deliberately is ALSO not added here even though it now
  // OPTIONALLY carries session_id (task/goal scope, JudgeVerdictFrame.yaml):
  // this set means "session_id is REQUIRED; drop the frame in production
  // when it's missing" (see the targetSid resolver below), which is the
  // wrong semantic for an OPTIONAL field — a plan-scope verdict (and any
  // legacy path) legitimately has none, and must keep routing to the
  // GLOBAL ActivityPanel, not get dropped with a connection-error toast.
  // `case 'judge_verdict'` below reads `frame.session_id` directly and
  // handles both cases itself.
  'goal_status', 'loop_status',
  // askuserquestion-tool-spec v3 §3: session-scoped — the session id rides
  // on card.session_id (required, min(1)); the routing resolver below
  // falls back to it when no top-level session_id exists.
  'ask_user_question',
  // ADR-085 BROWSER-FR-042/FR-044 (wave B8): BrowserHandoverNoticeFrame
  // carries a required, min(1) `session_id` (contracts/components/schemas/
  // BrowserHandoverNoticeFrame.yaml) — session-scoped like goal_status.
  'browser_handover_notice',
  // Goal outcome line (founder decision 2026-09-14): GoalOutcomeFrame carries
  // a required, min(1) `session_id` — session-scoped like goal_status.
  'goal_outcome',
])

// F-S3: frame types that can carry a turn-cancellation acknowledgment
// ("Error processing message: turn canceled" and similar) — the only types
// eligible for the pendingCancelAckSids disambiguation fallback below. This
// is deliberately a NARROW subset of SESSION_SCOPED_FRAME_TYPES (plus
// 'error', a global type): every other session-scoped frame missing
// session_id keeps its existing strict drop-in-production behaviour, and
// every other global frame keeps falling back to the active session.
export const CANCEL_ACK_FRAME_TYPES = new Set(['token', 'done', 'error'])

// F-S2: FALLBACK_SID exists only in test mode so tests that don't establish a session
// still route frames to a consistent bucket. In production getActiveSid() returns null
// when no session is active; frame writers must early-return on null.
export const FALLBACK_SID = import.meta.env.MODE === 'test' ? '__default' : null

export const UNKNOWN_FRAME_TOAST_THRESHOLD = 5
