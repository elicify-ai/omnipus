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
  | "exec_approval_response"
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
  | "exec_approval_request"
  | "exec_approval_expired"
  | "task_status_changed"
  | "replay_message"
  | "rate_limit"
  | "media"
  | "agent_switched"
  | "tool_approval_required"
  | "session_state"
  | "system_overload"
  | "replay_warning"
  | "cancel_stage"
  | "session_close_ack"
  | "exec_approval_response_ack"
  | "device_pairing_request";

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
}

export interface CancelFrame {
  type: "cancel";
  session_id: string;
}

export interface ExecApprovalResponseFrame {
  type: "exec_approval_response";
  id: string;
  decision: "allow" | "deny" | "always";
}

export interface PingFrame {
  type: "ping";
}

export interface AttachSessionFrame {
  type: "attach_session";
  session_id: string;
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

export interface ExecApprovalRequestFrame {
  type: "exec_approval_request";
  session_id: string;
  id: string;
  command: string;
  tool?: string;
  params?: {
    [key: string]: unknown;
  };
  message?: string;
  working_dir?: string;
  matched_policy?: string;
}

export interface TaskStatusChangedFrame {
  type: "task_status_changed";
  session_id: string;
  task_id: string;
  status: string;
  agent_id?: string;
}

export interface ReplayMessageFrame {
  type: "replay_message";
  session_id: string;
  content: string;
  role: string;
  id?: string;
  timestamp?: string;
  agent_id?: string;
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

export interface ExecApprovalResponseAckFrame {
  type: "exec_approval_response_ack";
  id?: string;
  session_id?: string;
}

export interface DevicePairingRequestFrame {
  type: "device_pairing_request";
  device_id: string;
  fingerprint?: string;
  pairing_code?: string;
  device_name?: string;
  session_id?: string;
}

export interface SessionCloseFrame {
  type: "session_close";
  session_id: string;
}

export interface ExecApprovalExpiredFrame {
  type: "exec_approval_expired";
  id: string;
  session_id: string;
  message?: string;
}

// ── Union of all WS frames (discriminated by the `type` field) ──────────────

export type WsFrame =
  | AuthFrame
  | MessageFrame
  | CancelFrame
  | ExecApprovalResponseFrame
  | PingFrame
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
  | ExecApprovalRequestFrame
  | TaskStatusChangedFrame
  | ReplayMessageFrame
  | RateLimitFrame
  | MediaFrame
  | AgentSwitchedFrame
  | ToolApprovalRequiredFrame
  | SessionStateFrame
  | SystemOverloadFrame
  | ReplayWarningFrame
  | CancelStageFrame
  | SessionCloseAckFrame
  | ExecApprovalResponseAckFrame
  | DevicePairingRequestFrame
  | SessionCloseFrame
  | ExecApprovalExpiredFrame;

// ── Client → server frames ──────────────────────────────────────────────────

export type ClientFrame =
  | AuthFrame
  | MessageFrame
  | CancelFrame
  | ExecApprovalResponseFrame
  | PingFrame
  | AttachSessionFrame
  | DevicePairingResponseFrame;

// ── Server → client frames ──────────────────────────────────────────────────

export type ServerFrame =
  | SessionStartedFrame
  | TokenFrame
  | DoneFrame
  | ErrorFrame
  | ToolCallStartFrame
  | ToolCallResultFrame
  | SubagentStartFrame
  | SubagentEndFrame
  | ExecApprovalRequestFrame
  | TaskStatusChangedFrame
  | ReplayMessageFrame
  | RateLimitFrame
  | MediaFrame
  | AgentSwitchedFrame
  | ToolApprovalRequiredFrame
  | SessionStateFrame
  | SystemOverloadFrame
  | ReplayWarningFrame
  | CancelStageFrame
  | SessionCloseAckFrame
  | ExecApprovalResponseAckFrame
  | DevicePairingRequestFrame
  | SessionCloseFrame
  | ExecApprovalExpiredFrame;
