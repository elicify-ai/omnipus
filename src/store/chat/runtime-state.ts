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

// Founder ruling 2026-10-07 (UAT row S7): a /stop-redirect turn is a redirect,
// not an interruption. Session ids this client has sent a `redirect` frame for
// and not yet seen the server's answer to (the next typed `error` frame for the
// session — turn_canceled when the redirect stopped the turn, anything else
// when it was refused). The turn_canceled frame itself carries no cause, so the
// sender's own knowledge is what lets the live view finalise the streaming
// bubble as a normal answer (partial text kept, no "(interrupted)") instead of
// treating it as an error. Cleared on socket drop with pendingCancelAckSids.
export const pendingRedirectSids = new Set<string>()

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

// #823 catch-up redesign, Opus review round 3 item N4 (LOW-MEDIUM): a
// failed rebuild (`done{stats.replay_error:true}`, the gateway unbinding
// instead of ever reaching catch_up_complete) must re-attach WITHOUT a
// since_seq/boot_id — the whole point is that the cursor this client had
// (if any) is untrustworthy for a snapshot that never actually finished
// rebuilding. Retried with backoff, not immediately, so a gateway that
// keeps failing this same rebuild doesn't get hammered in a tight loop.
// Keyed per session so an unrelated session's failure/retry timing never
// interferes with this one's.
export const replayErrorRetryAttempts: Record<string, number> = {}
export const replayErrorRetryTimers: Record<string, ReturnType<typeof setTimeout>> = {}
export const REPLAY_ERROR_BASE_DELAY_MS = 1_000
export const REPLAY_ERROR_MAX_DELAY_MS = 30_000

// Safety hardening (frontend-lead dispatch, subagent-control-plane stream):
// applySeqGate's gap branch (frames.ts) sends a single, fire-once
// `attach_session{since_seq, boot_id}` to recover from a sequence gap. If
// that frame's response is lost — e.g. raced against a concurrent
// attach_session for a DIFFERENT session sharing the same connection — the
// session was left permanently stuck: inFlightReattachSids blocks a second
// send, and nothing ever retries. Mirrors replayErrorRetryAttempts/Timers'
// own pattern immediately above (same backoff shape, same per-session
// keying), but — unlike the replay_error retry, whose re-schedule trigger is
// an external "it failed again" `done` frame — this one has no external
// retrigger signal (a lost ack is lost silently), so scheduleGapReattachRetry
// (frames.ts) re-schedules ITSELF on each firing rather than waiting for a
// caller to invoke it again. Cleared the same way replayErrorRetryAttempts/
// Timers are: the timer on disconnect (clearCatchUpSideChannelsOnDisconnect,
// outbound-lifecycle.ts — a stale timer would resend over a dead
// connection), both timer and attempt count once the gap genuinely resolves
// (inFlightReattachSids.delete, applySeqGate's own 'apply'/cursor-minting
// branches).
export const gapReattachRetryAttempts: Record<string, number> = {}
export const gapReattachRetryTimers: Record<string, ReturnType<typeof setTimeout>> = {}
export const GAP_REATTACH_BASE_DELAY_MS = 1_000
export const GAP_REATTACH_MAX_DELAY_MS = 30_000
// Silent-failure fix (8-reviewer gate finding): an unanswered gap re-attach
// used to retry forever with no user-facing signal. After this many attempts
// the user is told once per stuck episode that the session may be out of
// sync. Own constant, not UNKNOWN_FRAME_TOAST_THRESHOLD — the two thresholds
// govern unrelated failure modes. Value 5 against this retry's own backoff
// curve (1+2+4+8+16s) warns at ~31s of a stuck session: late enough to ride
// out every transient hiccup (a healthy re-attach resolves within the first
// 1-2 retries), early enough to matter when the failure never self-heals.
export const GAP_REATTACH_TOAST_THRESHOLD = 5

export const EMPTY_BUCKET = emptySessionState()

// F-S1: all server→client frames that must carry session_id.
// Frames in this set without a session_id are routing errors.
// Global frames (error, auth_*, ping, pong, device_pairing_*) are intentionally absent.
export const SESSION_SCOPED_FRAME_TYPES = new Set([
  'token', 'done', 'tool_call_start', 'tool_call_result',
  'subagent_start', 'subagent_end', 'replay_message', 'replay_done',
  'task_status_changed',
  'tool_approval_required', 'rate_limit', 'media', 'session_started',
  'system_overload', 'cancel_stage',
  'message_status',
  // ADR-092: SessionModeUpdatedFrame.session_id is required (min length 1) —
  // same "drop in production when missing" contract as cancel_stage above.
  'session_mode_updated',
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
  // Q33: live and replayed context diagnostics require their owning session.
  'context_window_notice',
  // ADR-091 D7/I-4 (FR-E-002): subagent_message/subagent_state always carry
  // a required `session_id` — the producing span's own session, reduced
  // onto the span record (frames.ts's `case 'subagent_message'`/
  // `'subagent_state'`). Session-scoped like subagent_start/_end above.
  'subagent_message', 'subagent_state',
])

// F-S3: frame types that can carry a turn-cancellation acknowledgment
// ("Error processing message: turn canceled" and similar) — the only type
// eligible for the pendingCancelAckSids disambiguation fallback below.
//
// ADR-091 D7/FR-E-002 (cross-family review finding 18): this used to also
// list 'token' and 'done', but both are session-scoped
// (SESSION_SCOPED_FRAME_TYPES) — reassigning a session-scoped frame to a
// guessed session instead of dropping it is exactly the bug the finding
// reported (an untagged 'done' could be silently attributed to whatever
// session had a lone pending cancel, before the mandatory drop check ever
// ran). handleFrame now drops every session-scoped frame missing
// session_id at the very top, unconditionally, before this set is ever
// consulted — so only 'error' (a global frame type) remains eligible here.
export const CANCEL_ACK_FRAME_TYPES = new Set(['error'])

export const UNKNOWN_FRAME_TOAST_THRESHOLD = 5
