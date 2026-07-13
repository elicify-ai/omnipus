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
  | "subagent_start"
  | "subagent_end"
  | "task_status_changed"
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
  | "browser_screencast"
  | "browser_status"
  | "browser_tab_action"
  | "browser_tabs";

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
}

export interface TokenFrame {
  type: "token";
  session_id: string;
  content: string;
  agent_id?: string;
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
}

export interface ErrorFrame {
  type: "error";
  session_id?: string;
  message: string;
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
}

export interface SubagentStartFrame {
  type: "subagent_start";
  session_id: string;
  span_id: string;
  parent_call_id: string;
  task_label: string;
  agent_id?: string;
}

export interface SubagentEndFrame {
  type: "subagent_end";
  session_id: string;
  span_id: string;
  status: "success" | "error" | "cancelled" | "interrupted" | "timeout";
  duration_ms?: number;
  final_result?: string;
  reason?: "parent_timeout" | "parent_cancelled" | "parent_done_early" | "unknown";
  agent_id?: string;
  parent_call_id?: string;
  message?: string;
}

export interface TaskStatusChangedFrame {
  type: "task_status_changed";
  session_id: string;
  task_id: string;
  status: "inbox" | "next" | "planning" | "in_progress" | "blocked" | "done" | "failed";
  agent_id?: string;
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
    retry_after_seconds?: number;
    policy_rule?: string;
    scope?: string;
    resource?: string;
    tool?: string;
    stage?: string;
  };
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
}

export interface AgentSwitchedFrame {
  type: "agent_switched";
  session_id: string;
  agent_id?: string;
  message?: string;
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
  emitted_at: string;
}

export interface SystemOverloadFrame {
  type: "system_overload";
  session_id: string;
  message?: string;
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
}

export interface SessionCloseAckFrame {
  type: "session_close_ack";
  session_id: string;
  id?: string;
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
  kind: "mouse_move" | "mouse_down" | "mouse_up" | "wheel" | "key_down" | "key_up" | "text" | "navigate";
  x?: number;
  y?: number;
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

export interface BrowserScreencastFrame {
  type: "browser_screencast";
  session_id: string;
  seq: number;
  data: string;
  width: number;
  height: number;
  page_scale?: number;
  offset_top?: number;
  scroll_offset_x?: number;
  scroll_offset_y?: number;
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
  | TaskStatusChangedFrame
  | ReplayMessageFrame
  | ReplayErrorFrame
  | RateLimitFrame
  | MediaFrame
  | AgentSwitchedFrame
  | ToolApprovalRequiredFrame
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
  | BrowserScreencastFrame
  | BrowserStatusFrame
  | BrowserTabActionFrame
  | BrowserTabsFrame;

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
  | BrowserAttachFrame
  | BrowserInputFrame
  | BrowserControlFrame
  | BrowserDetachFrame;

// ── ClientFrameTypes constant — generated from spec, not hand-written ─────────
// Import this in ws.ts to build CLIENT_FRAME_TYPES set. Never edit directly.

export const ClientFrameTypes = ["auth", "message", "cancel", "ping", "attach_session", "device_pairing_response", "session_close", "whatsapp_pairing_subscribe", "browser_attach", "browser_input", "browser_control", "browser_detach"] as const

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
  | TaskStatusChangedFrame
  | ReplayMessageFrame
  | ReplayErrorFrame
  | RateLimitFrame
  | MediaFrame
  | AgentSwitchedFrame
  | ToolApprovalRequiredFrame
  | SessionStateFrame
  | SystemOverloadFrame
  | ReplayWarningFrame
  | CancelStageFrame
  | SessionCloseAckFrame
  | DevicePairingRequestFrame
  | WhatsAppPairingFrame
  | NotificationFrame
  | BrowserScreencastFrame
  | BrowserStatusFrame
  | BrowserTabActionFrame
  | BrowserTabsFrame;
