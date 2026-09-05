/**
 * This file was auto-generated from contracts/asyncapi.yaml.
 * Do not make direct changes to the file.
 * Re-run: node scripts/_gen-asyncapi-types.mjs
 */

// ── WebSocket frame type discriminator ──────────────────────────────────────

export type WsFrameType =
  | "auth"
  | "message"
  | "cancel"
  | "ping"
  | "attach_session"
  | "device_pairing_response"
  | "session_close"
  | "session_started"
  | "token"
  | "done"
  | "error"
  | "tool_call_start"
  | "tool_call_result"
  | "tool_result_projection"
  | "subagent_start"
  | "subagent_message"
  | "subagent_state"
  | "subagent_end"
  | "task_status_changed"
  | "task_run_status"
  | "replay_message"
  | "replay_error"
  | "rate_limit"
  | "media"
  | "agent_switched"
  | "tool_approval_required"
  | "session_state"
  | "system_overload"
  | "replay_warning"
  | "cancel_stage"
  | "pong"
  | "session_close_ack"
  | "device_pairing_request"
  | "whatsapp_pairing"
  | "whatsapp_pairing_subscribe"
  | "notification"
  | "browser_attach"
  | "browser_input"
  | "browser_control"
  | "browser_detach"
  | "browser_status"
  | "browser_tab_action"
  | "browser_tabs"
  | "browser_viewport"
  | "browser_webrtc_offer"
  | "browser_webrtc_answer"
  | "browser_webrtc_state"
  | "browser_capture_hello"
  | "browser_capture_offer"
  | "browser_capture_answer"
  | "browser_capture_control"
  | "goal_status"
  | "loop_status"
  | "plan_status"
  | "judge_verdict"
  | "ask_user_question"
  | "ask_user_answer";

// ── Frame payload types ─────────────────────────────────────────────────────

export interface AuthFrame {
  type: "auth";
  token: string;
}

export interface MessageFrame {
  type: "message";
  content: string;
  session_id?: string;
  agent_id?: string;
  media?: Array<string>;
  metadata?: {
    model_name?: string;
    workspace_id?: string;
    workspace_setup_kickoff?: boolean;
    [key: string]: unknown;
  };
}

export interface CancelFrame {
  type: "cancel";
  session_id: string;
}

export interface PingFrame {
  type: "ping";
}

export interface PongFrame {
  type: "pong";
}

export interface AttachSessionFrame {
  type: "attach_session";
  session_id: string;
  since?: string;
}

export interface DevicePairingResponseFrame {
  type: "device_pairing_response";
  device_id: string;
  decision: "approve" | "reject";
}

export interface SessionStartedFrame {
  type: "session_started";
  session_id: string;
  agent_id?: string;
  producing_session_id?: string;
}

export interface TokenFrame {
  type: "token";
  session_id: string;
  content: string;
  agent_id?: string;
  producing_session_id?: string;
}

export interface DoneStats {
  tokens?: number;
  cost?: number;
  duration_ms?: number;
  tokens_dropped?: number;
  frames_emitted?: number;
  orphan_count?: number;
  duplicate_tool_call_id_count?: number;
  truncated_result_count?: number;
  replay_error?: boolean;
  turn_failed?: boolean;
  [key: string]: unknown;
}

export interface DoneFrame {
  type: "done";
  session_id: string;
  stats?: DoneStats;
  producing_session_id?: string;
}

export interface LLMError {
  code: "media_unsupported" | "provider_rejected" | "request_too_large" | "provider_auth_failed" | "rate_limited" | "network" | "content_policy" | "context_too_long" | "tool_args" | "schema" | "agent_not_configured" | "workspace_unavailable" | "model_unavailable" | "needs_provider" | "model_unassigned" | "turn_canceled" | "turn_timed_out" | "context_unrecoverable" | "context_window_unknown" | "unknown";
  message: string;
  retryable: boolean;
  detail?: string;
}

export interface LLMErrorReplay {
  code: "media_unsupported" | "provider_rejected" | "request_too_large" | "provider_auth_failed" | "rate_limited" | "network" | "content_policy" | "context_too_long" | "tool_args" | "schema" | "agent_not_configured" | "workspace_unavailable" | "model_unavailable" | "needs_provider" | "model_unassigned" | "turn_canceled" | "turn_timed_out" | "context_unrecoverable" | "context_window_unknown" | "unknown";
  message: string;
  retryable: boolean;
}

export interface ErrorFrame {
  type: "error";
  session_id?: string;
  message: string;
  payload?: {
    llm_error: LLMError;
  };
}

export interface ToolCallStartFrame {
  type: "tool_call_start";
  session_id: string;
  tool: string;
  call_id: string;
  params: {
    [key: string]: unknown;
  };
  parent_call_id?: string;
  agent_id?: string;
  producing_session_id?: string;
}

export interface TruncatedResult {
  _truncated: true;
  original_size_bytes: number;
  preview: string;
}

export interface MarshalErrorResult {
  _marshal_error: string;
}

export interface ToolResultRef {
  _ref: true;
  ref: string;
  original_size_bytes: number;
  preview: string;
}

export interface DelegationFailure {
  error: "delegation_denied";
  reason: string;
  policy: "trust_set" | "mode" | "depth";
  tool: string;
  target_agent_id?: string;
}

export interface FileExistsRefusal {
  error: "file_exists";
  reason: string;
  tool: string;
  path: string;
}

export interface PermissionDenied {
  error: "permission_denied";
  message: string;
  tool: string;
  reason: string;
  permanent: boolean;
}

export interface ToolAssemblyDuplicate {
  error: "tool_assembly_duplicate";
  message: string;
}

export interface ToolArgumentRefusal {
  error: "tool_arguments_too_large";
  reason: string;
  tool: string;
  size_chars: number;
  cap_chars: number;
}

export interface ToolResultRecallMark {
  error: "tool_result_recall_mark";
  tool: string;
  tool_call_id: string;
  archive_line: number;
  size_chars: number;
  turn: number;
  content_state: "capped" | "emptied";
  hint: string;
}

export interface ToolCallResultFrame {
  type: "tool_call_result";
  session_id: string;
  tool: string;
  call_id: string;
  result: unknown;
  status: "success" | "error";
  duration_ms?: number;
  error?: string;
  parent_call_id?: string;
  agent_id?: string;
  producing_session_id?: string;
}

export interface SubagentStartFrame {
  type: "subagent_start";
  session_id: string;
  span_id: string;
  parent_call_id: string;
  task_label: string;
  agent_id?: string;
  producing_session_id?: string;
}

export interface SubagentEndFrame {
  type: "subagent_end";
  session_id: string;
  span_id: string;
  status: "success" | "error" | "cancelled" | "interrupted" | "timeout" | "parked";
  duration_ms?: number;
  final_result?: string;
  reason?: "parent_timeout" | "parent_cancelled" | "parent_done_early" | "unknown";
  agent_id?: string;
  parent_call_id?: string;
  message?: string;
  producing_session_id?: string;
}

export interface SubagentMessageFrame {
  type: "subagent_message";
  session_id: string;
  span_id: string;
  message_id: string;
  kind: "progress" | "checkpoint" | "artifact" | "blocker" | "question" | "decision_request" | "error" | "handback" | "steer" | "respond";
  text?: string;
  pct?: number;
  correlation_id?: string;
  sender_identity: string;
  untrusted_origin: boolean;
  created_at: string;
}

export interface SubagentStateFrame {
  type: "subagent_state";
  session_id: string;
  span_id: string;
  state: "queued" | "running" | "needs_input" | "paused" | "completed" | "failed" | "cancelled" | "timed_out";
  steering_receipt?: {
    correlation_id: string;
    applied_at: string;
  };
  created_at: string;
}

export interface TaskStatusChangedFrame {
  type: "task_status_changed";
  session_id: string;
  task_id: string;
  status: "inbox" | "next" | "in_progress" | "blocked" | "done" | "failed";
  agent_id?: string;
  producing_session_id?: string;
}

export interface TaskRunStatusFrame {
  type: "task_run_status";
  task_id: string;
  run_id: string;
  occurrence_ms?: number;
  status: "in_progress" | "done" | "failed" | "skipped";
}

export interface ReplayMessageFrame {
  type: "replay_message";
  session_id: string;
  content: string;
  role: "user" | "assistant" | "system" | "turn_canceled";
  id?: string;
  timestamp?: string;
  agent_id?: string;
  model?: string;
  turn_id?: string;
}

export interface ReplayErrorFrame {
  type: "replay_error";
  session_id: string;
  entry_id: string;
  timestamp: string;
  kind: "rate_limit" | "error";
  message: string;
  agent_id?: string;
  payload?: {
    llm_error: LLMErrorReplay;
  };
}

export interface ToolResultProjectionFrame {
  type: "tool_result_projection";
  session_id: string;
  tool_call_id: string;
  archive_line: number;
  content_state: "capped" | "emptied";
  mark?: string;
  producing_session_id?: string;
}

export interface RateLimitFrame {
  type: "rate_limit";
  session_id: string;
  scope: "agent" | "channel" | "global";
  resource: string;
  policy_rule: string;
  retry_after_seconds: number;
  agent_id?: string;
  tool?: string;
}

export interface MediaPart {
  type: "image" | "audio" | "video" | "file";
  url: string;
  filename: string;
  content_type: string;
  caption?: string;
}

export interface MediaFrame {
  type: "media";
  session_id: string;
  parts: Array<MediaPart>;
  producing_session_id?: string;
}

export interface AgentSwitchedFrame {
  type: "agent_switched";
  session_id: string;
  agent_id?: string;
  message?: string;
  producing_session_id?: string;
}

export interface ToolApprovalRequiredFrame {
  type: "tool_approval_required";
  approval_id: string;
  tool_call_id: string;
  tool_name: string;
  args: {
    [key: string]: unknown;
  };
  agent_id: string;
  session_id: string;
  turn_id: string;
  expires_in_ms: number;
  producing_session_id?: string;
}

export interface AskUserQuestionCard {
  card_id: string;
  session_id: string;
  agent_id: string;
  status: "pending" | "answered" | "cancelled";
  created_at: string;
  default_safe_at?: string;
  auto_resolved?: Array<string>;
  questions: Array<{
    header: string;
    question: string;
    options: Array<{
      label: string;
      description?: string;
    }>;
    multi_select?: boolean;
    recommended?: string;
    default_safe?: boolean;
    context?: string;
  }>;
  answers?: Array<{
    header: string;
    question: string;
    selected?: Array<string>;
    free_text?: string;
    auto_default: boolean;
  }>;
}

export interface AskUserQuestionFrame {
  type: "ask_user_question";
  card: AskUserQuestionCard;
}

export interface AskUserAnswerFrame {
  type: "ask_user_answer";
  card_id: string;
  session_id: string;
  cancel?: boolean;
  answers?: Array<{
    header: string;
    selected?: Array<string>;
    free_text?: string;
    auto_default?: boolean;
  }>;
}

export interface SessionStatePendingApproval {
  approval_id: string;
  session_id: string;
  tool_name: string;
  agent_id: string;
  expires_in_ms: number;
}

export interface SessionStateFrame {
  type: "session_state";
  user_id: string;
  pending_approvals: Array<SessionStatePendingApproval>;
  pending_asks?: Array<AskUserQuestionCard>;
  emitted_at: string;
}

export interface SystemOverloadFrame {
  type: "system_overload";
  session_id: string;
  message?: string;
  producing_session_id?: string;
}

export interface ReplayWarningStats {
  duplicate_tool_call_id_count?: number;
  [key: string]: unknown;
}

export interface ReplayWarningFrame {
  type: "replay_warning";
  session_id: string;
  message: string;
  stats?: ReplayWarningStats;
}

export interface CancelStageFrame {
  type: "cancel_stage";
  session_id: string;
  stage: "graceful" | "hard" | "detached";
  producing_session_id?: string;
}

export interface SessionCloseAckFrame {
  type: "session_close_ack";
  session_id: string;
  id?: string;
  producing_session_id?: string;
}

export interface DevicePairingRequestFrame {
  type: "device_pairing_request";
  device_id: string;
  fingerprint?: string;
  pairing_code?: string;
  device_name?: string;
  session_id?: string;
}

export interface WhatsAppPairingFrame {
  type: "whatsapp_pairing";
  channel_id: string;
  status: "waiting" | "code" | "linked" | "timeout" | "error";
  qr?: string;
  message?: string;
}

export interface SessionCloseFrame {
  type: "session_close";
  session_id: string;
}

export interface WhatsAppPairingSubscribeFrame {
  type: "whatsapp_pairing_subscribe";
  channel_id: string;
  active: boolean;
}

export interface NotificationFrame {
  type: "notification";
  id: string;
  notification_type: "schedule_failed";
  title: string;
  body?: string;
  severity: "info" | "warning" | "error";
  read: boolean;
  created_at_ms: number;
  schedule_id?: string;
  session_id?: string;
  agent_id?: string;
}

export interface BrowserAttachFrame {
  type: "browser_attach";
  session_id: string;
  agent_id: string;
}

export interface BrowserInputFrame {
  type: "browser_input";
  kind: "mouse_move" | "mouse_down" | "mouse_up" | "wheel" | "key_down" | "key_up" | "text" | "navigate" | "navigate_back" | "reload";
  x?: number;
  y?: number;
  capture_width?: number;
  capture_height?: number;
  button?: "none" | "left" | "middle" | "right" | "back" | "forward";
  delta_x?: number;
  delta_y?: number;
  key?: string;
  code?: string;
  key_code?: number;
  text?: string;
  modifiers?: number;
  url?: string;
}

export interface BrowserControlFrame {
  type: "browser_control";
  action: "take" | "release";
}

export interface BrowserDetachFrame {
  type: "browser_detach";
  session_id?: string;
}

export interface BrowserStatusFrame {
  type: "browser_status";
  state: "attached" | "idle" | "controlling" | "released" | "detached" | "error";
  message?: string;
  controller?: string;
  controlled_by_other?: boolean;
  control_only?: boolean;
  session_id?: string;
}

export interface BrowserViewportFrame {
  type: "browser_viewport";
  session_id?: string;
  agent_id?: string;
  width: number;
  height: number;
  device_scale_factor?: number;
}

export interface BrowserTabActionFrame {
  type: "browser_tab_action";
  session_id?: string;
  agent_id?: string;
  action: "switch" | "close" | "open";
  index?: number;
}

export interface BrowserTabsFrame {
  type: "browser_tabs";
  session_id?: string;
  active_index: number;
  tabs: Array<{
    index: number;
    title?: string;
    url?: string;
    active?: boolean;
  }>;
}

export interface BrowserWebRTCOfferFrame {
  type: "browser_webrtc_offer";
  agent_id: string;
  session_id: string;
  sdp: string;
}

export interface BrowserWebRTCAnswerFrame {
  type: "browser_webrtc_answer";
  session_id?: string;
  sdp: string;
}

export interface BrowserWebRTCStateFrame {
  type: "browser_webrtc_state";
  session_id?: string;
  available: boolean;
  reason?: "disabled" | "not_capable" | "lite_build" | "error" | "multi_agent_capture_denied" | "ingest_timeout";
  reason_detail?: string;
  has_audio?: boolean;
  active?: boolean;
  ice_servers?: Array<{
    urls: Array<string>;
    username?: string;
    credential?: string;
  }>;
}

export interface BrowserCaptureHelloFrame {
  type: "browser_capture_hello";
  token: string;
  ext_version: string;
}

export interface BrowserCaptureOfferFrame {
  type: "browser_capture_offer";
  sdp: string;
}

export interface BrowserCaptureAnswerFrame {
  type: "browser_capture_answer";
  sdp: string;
}

export interface BrowserCaptureControlFrame {
  type: "browser_capture_control";
  action: "recapture" | "shutdown" | "ping" | "adapt_reset" | "set_bitrate";
  reason?: string;
  max_bitrate?: number;
  expected_width?: number;
  expected_height?: number;
  capture_scale?: number;
}

export interface GoalStatusFrame {
  type: "goal_status";
  session_id: string;
  goal_id?: string;
  condition: string;
  round: number;
  max_rounds: number;
  latest_reason: string;
  active_loops: number;
  cap: number;
  state: "queued" | "active" | "waiting_on_user" | "judge_unavailable" | "re-planning" | "judging" | "done" | "failed" | "cleared";
  producing_session_id?: string;
  criteria?: Array<{
    id?: string;
    kind: "check" | "prose" | "behavior";
    text: string;
    check?: {
      command: string;
      expected_exit_code: number;
    };
    behavior?: {
      tool: string;
      min_count?: number;
      max_count?: number;
      scope?: "attempt" | "task_session";
    };
    author: {
      kind: "agent" | "user";
      id: string;
    };
    status: "pending" | "met" | "unmet";
  }>;
}

export interface LoopStatusFrame {
  type: "loop_status";
  session_id: string;
  mode: "interval" | "self_paced";
  run: number;
  max_runs: number;
  next_delay?: number;
  state: string;
  producing_session_id?: string;
}

export interface PlanStatusFrame {
  type: "plan_status";
  plan_id: string;
  state: "draft" | "approved" | "running" | "done" | "failed";
  plan_phase: "dispatching" | "judging" | "synthesizing" | "idle" | "awaiting_supervision" | "stalled";
  progress: number;
  paused_reason?: string;
}

export interface JudgeVerdictFrame {
  type: "judge_verdict";
  id: string;
  scope: "task" | "plan" | "goal";
  task_id?: string;
  plan_id?: string;
  round: number;
  met: boolean;
  per_criterion: Array<{
    criterion_id: string;
    met: boolean;
    reason: string;
    evidence_quote?: string;
  }>;
  model: string;
  judged_at: string;
  judge_agent_id: string;
}

export interface ErrorPayload {
  llm_error: LLMError;
}

export interface ReplayErrorPayload {
  llm_error: LLMErrorReplay;
}

// ── Union of all WS frames (discriminated by the `type` field) ──────────────

export type WsFrame =
  | AuthFrame
  | MessageFrame
  | CancelFrame
  | PingFrame
  | PongFrame
  | AttachSessionFrame
  | DevicePairingResponseFrame
  | SessionStartedFrame
  | TokenFrame
  | DoneFrame
  | ErrorFrame
  | ToolCallStartFrame
  | ToolCallResultFrame
  | SubagentStartFrame
  | SubagentEndFrame
  | SubagentMessageFrame
  | SubagentStateFrame
  | TaskStatusChangedFrame
  | TaskRunStatusFrame
  | ReplayMessageFrame
  | ReplayErrorFrame
  | ToolResultProjectionFrame
  | RateLimitFrame
  | MediaFrame
  | AgentSwitchedFrame
  | ToolApprovalRequiredFrame
  | AskUserQuestionFrame
  | AskUserAnswerFrame
  | SessionStateFrame
  | SystemOverloadFrame
  | ReplayWarningFrame
  | CancelStageFrame
  | SessionCloseAckFrame
  | DevicePairingRequestFrame
  | WhatsAppPairingFrame
  | SessionCloseFrame
  | WhatsAppPairingSubscribeFrame
  | NotificationFrame
  | BrowserAttachFrame
  | BrowserInputFrame
  | BrowserControlFrame
  | BrowserDetachFrame
  | BrowserStatusFrame
  | BrowserViewportFrame
  | BrowserTabActionFrame
  | BrowserTabsFrame
  | BrowserWebRTCOfferFrame
  | BrowserWebRTCAnswerFrame
  | BrowserWebRTCStateFrame
  | BrowserCaptureHelloFrame
  | BrowserCaptureOfferFrame
  | BrowserCaptureAnswerFrame
  | BrowserCaptureControlFrame
  | GoalStatusFrame
  | LoopStatusFrame
  | PlanStatusFrame
  | JudgeVerdictFrame;

// ── Client → server frames ──────────────────────────────────────────────────

export type ClientFrame =
  | AuthFrame
  | MessageFrame
  | CancelFrame
  | PingFrame
  | AttachSessionFrame
  | DevicePairingResponseFrame
  | SessionCloseFrame
  | WhatsAppPairingSubscribeFrame
  | AskUserAnswerFrame
  | BrowserAttachFrame
  | BrowserInputFrame
  | BrowserControlFrame
  | BrowserDetachFrame
  | BrowserWebRTCOfferFrame;

// ── ClientFrameTypes constant — generated from spec, not hand-written ─────────
// Import this in ws.ts to build CLIENT_FRAME_TYPES set. Never edit directly.

export const ClientFrameTypes = ["auth", "message", "cancel", "ping", "attach_session", "device_pairing_response", "session_close", "whatsapp_pairing_subscribe", "ask_user_answer", "browser_attach", "browser_input", "browser_control", "browser_detach", "browser_webrtc_offer"] as const

// ── Server → client frames ──────────────────────────────────────────────────

export type ServerFrame =
  | PongFrame
  | SessionStartedFrame
  | TokenFrame
  | DoneFrame
  | ErrorFrame
  | ToolCallStartFrame
  | ToolCallResultFrame
  | SubagentStartFrame
  | SubagentEndFrame
  | SubagentMessageFrame
  | SubagentStateFrame
  | TaskStatusChangedFrame
  | TaskRunStatusFrame
  | ReplayMessageFrame
  | ReplayErrorFrame
  | ToolResultProjectionFrame
  | RateLimitFrame
  | MediaFrame
  | AgentSwitchedFrame
  | ToolApprovalRequiredFrame
  | AskUserQuestionFrame
  | SessionStateFrame
  | SystemOverloadFrame
  | ReplayWarningFrame
  | CancelStageFrame
  | SessionCloseAckFrame
  | DevicePairingRequestFrame
  | WhatsAppPairingFrame
  | NotificationFrame
  | BrowserStatusFrame
  | BrowserViewportFrame
  | BrowserTabActionFrame
  | BrowserTabsFrame
  | BrowserWebRTCAnswerFrame
  | BrowserWebRTCStateFrame
  | BrowserCaptureHelloFrame
  | BrowserCaptureOfferFrame
  | BrowserCaptureAnswerFrame
  | BrowserCaptureControlFrame
  | GoalStatusFrame
  | LoopStatusFrame
  | PlanStatusFrame
  | JudgeVerdictFrame;
