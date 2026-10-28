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
  | "redirect"
  | "ping"
  | "attach_session"
  | "device_pairing_response"
  | "session_started"
  | "message_status"
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
  | "replay_provider_fallback"
  | "rate_limit"
  | "provider_retry"
  | "context_window_notice"
  | "provider_fallback"
  | "media"
  | "tool_approval_required"
  | "tool_approval_resolved"
  | "session_state"
  | "system_overload"
  | "replay_warning"
  | "cancel_stage"
  | "pong"
  | "session_mode_update"
  | "session_mode_updated"
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
  | "browser_video_health"
  | "goal_status"
  | "loop_status"
  | "plan_status"
  | "judge_verdict"
  | "ask_user_question"
  | "ask_user_answer"
  | "browser_input_offer"
  | "browser_input_answer"
  | "browser_input_state"
  | "browser_input_control_ack"
  | "browser_handover_notice"
  | "goal_outcome"
  | "helper_event"
  | "library_changed"
  | "session_snapshot"
  | "catch_up_complete"
  | "user_message"
  | "agent_created"
  | "mail_panel_observer"
  | "mail_panel_observer_ack"
  | "mail_panel_observer_error"
  | "secret_request"
  | "secret_request_resolved"
  | "attach_room"
  | "room_snapshot"
  | "room_entry"
  | "room_member_state"
  | "room_meta";

// ── Frame payload types ─────────────────────────────────────────────────────

export interface HistoryEntryOrigin {
  position: string;
  entry_id?: string;
}

export interface SessionHistoryState {
  status: "available" | "unavailable";
  reason?: "index_missing" | "index_inconsistent" | "catchup_limit" | "storage_error" | "projection_error";
  message?: string;
}

export interface RoomRef {
  room_id: string;
  room_name: string;
  request_id: string;
  role: "request" | "answer";
}

export interface WorkflowStart {
  workflow: "W1" | "W18";
  target_workspace_id: string;
}

export interface SecretRecipientDisplay {
  kind: "provider_key" | "mcp_env" | "channel_credential" | "mailbox_password" | "agent_private" | "unbound";
  lines: Array<string>;
}

export interface SecretRequestField {
  key: string;
  label: string;
}

export interface SecretRequestFrame {
  type: "secret_request";
  request_id: string;
  session_id: string;
  root_session_id?: string;
  acting_agent_id: string;
  acting_agent_name: string;
  origin_label?: string;
  workspace_id?: string;
  owner?: {
    workspace_id: string;
    agent_id: string;
  };
  purpose: string;
  recipient: SecretRecipientDisplay;
  fields: Array<SecretRequestField>;
}

export interface SecretRequestResolvedFrame {
  type: "secret_request_resolved";
  request_id: string;
  state: "submitted" | "cancelled" | "stopped" | "interrupted" | "refused";
  session_id?: string;
  root_session_id?: string;
}

export interface SessionStateSecretRequest {
  request_id: string;
  session_id: string;
  root_session_id?: string;
  state: "pending" | "submitted" | "cancelled" | "stopped" | "interrupted" | "refused";
  continuation_unconfirmed?: boolean;
  acting_agent_id: string;
  acting_agent_name: string;
  origin_label?: string;
  workspace_id?: string;
  owner?: {
    workspace_id: string;
    agent_id: string;
  };
  purpose: string;
  recipient: SecretRecipientDisplay;
  fields: Array<SecretRequestField>;
  reference_names?: Array<string>;
  updated_at: string;
}

export interface RoomEntryAuthor {
  kind: "user" | "agent";
  agent_id?: string;
}

export interface RoomDeliveryState {
  agent_id: string;
  request_id: string;
  state: "recorded" | "awaiting_approval" | "admitted" | "held" | "working" | "waiting_on_user" | "answered" | "refused" | "failed" | "interrupted" | "unavailable" | "cancelled" | "unknown";
  reason?: string;
}

export interface RoomEntry {
  id: string;
  room_id: string;
  seq: number;
  client_message_id?: string;
  author: RoomEntryAuthor;
  text: string;
  addressees: Array<string>;
  after_pause: boolean;
  deliveries: Array<RoomDeliveryState>;
  timestamp: string;
}

export interface RoomEntryFrame {
  type: "room_entry";
  room_id: string;
  seq: number;
  entry: RoomEntry;
}

export interface RoomMemberStateFrame {
  type: "room_member_state";
  room_id: string;
  seq: number;
  delivery: RoomDeliveryState;
}

export interface AttachRoomFrame {
  type: "attach_room";
  room_id: string;
  after_seq: number;
}

export interface RoomSnapshotFrame {
  type: "room_snapshot";
  room_id: string;
  seq: number;
  entries: Array<RoomEntry>;
  members: Array<string>;
  muted: Array<string>;
  member_states: Array<RoomDeliveryState>;
  paused: boolean;
  generation: number;
  read_only_reason?: string;
  allocated_cost_micro_usd: number;
  allocated_cost_unpriced: boolean;
  complete: boolean;
  has_older?: boolean;
}

export interface RoomMetaFrame {
  type: "room_meta";
  room_id: string;
  seq: number;
  members: Array<string>;
  muted: Array<string>;
  paused: boolean;
  generation: number;
  read_only_reason?: string;
  allocated_cost_micro_usd: number;
  allocated_cost_unpriced: boolean;
}

export interface ChatParticipant {
  kind: "human" | "agent";
  display_name: string;
  source?: string;
  agent?: {
    workspace_id: string;
    agent_id: string;
  };
}

export interface AuthFrame {
  type: "auth";
  token: string;
}

export interface MessageFrame {
  workflow_start?: WorkflowStart;
  type: "message";
  client_message_id?: string;
  content: string;
  session_id?: string;
  recipient?: {
    workspace_id: string;
    agent_id: string;
  };
  agent_id?: string;
  media?: Array<string>;
  auto_approve?: boolean | null;
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
  scope?: "session" | "tree";
}

export interface RedirectFrame {
  type: "redirect";
  session_id: string;
  instruction: string;
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
  history_limit?: number;
  history_min_rows?: number;
  since_seq?: number;
  boot_id?: string;
  ack_attention?: boolean;
  attention_bound?: number;
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
  client_message_id?: string;
  recovered?: boolean;
  seq?: number;
  boot_id?: string;
}

export interface MessageStatusFrame {
  type: "message_status";
  session_id: string;
  client_message_id: string;
  state: "received" | "working" | "failed" | "discarded" | "held";
  reason?: "stopped_before_delivery";
  seq?: number;
}

export interface TokenFrame {
  room_ref?: RoomRef;
  type: "token";
  session_id: string;
  content: string;
  agent_id?: string;
  reply_to_message_id?: string;
  reply_to_participant?: ChatParticipant;
  turn_id?: string;
  message_id?: string;
  goal_id?: string;
  replace?: boolean;
  seq?: number;
}

export interface DoneStats {
  tokens?: number;
  session_tokens_total?: number;
  cost?: number;
  duration_ms?: number;
  frames_emitted?: number;
  orphan_count?: number;
  duplicate_tool_call_id_count?: number;
  truncated_result_count?: number;
  replay_error?: boolean;
  turn_failed?: boolean;
  truncated?: boolean;
  truncation_reason?: "cancelled" | "max_output_tokens";
  [key: string]: unknown;
}

export interface DoneFrame {
  room_ref?: RoomRef;
  type: "done";
  session_id: string;
  stats?: DoneStats;
  turn_id?: string;
  message_id?: string;
  goal_id?: string;
  seq?: number;
}

export interface LLMError {
  code: "media_unsupported" | "provider_rejected" | "request_too_large" | "provider_auth_failed" | "rate_limited" | "quota_billing" | "network" | "provider_stalled" | "content_policy" | "context_too_long" | "tool_args" | "tool_call_truncated" | "schema" | "agent_not_configured" | "workspace_unavailable" | "model_unavailable" | "model_retired" | "needs_provider" | "model_unassigned" | "turn_canceled" | "turn_timed_out" | "judge_time_limit" | "judge_conclusion_failed" | "delegated_task_limit" | "context_window_unknown" | "bad_model" | "provider_request_rejected" | "model_route_unavailable" | "unknown";
  message: string;
  retryable: boolean;
  detail?: string;
  facts?: {
    provider?: string;
    model?: string;
    request_id?: string;
  };
  provider_message?: boolean;
}

export interface LLMErrorReplay {
  code: "media_unsupported" | "provider_rejected" | "request_too_large" | "provider_auth_failed" | "rate_limited" | "quota_billing" | "network" | "provider_stalled" | "content_policy" | "context_too_long" | "tool_args" | "tool_call_truncated" | "schema" | "agent_not_configured" | "workspace_unavailable" | "model_unavailable" | "model_retired" | "needs_provider" | "model_unassigned" | "turn_canceled" | "turn_timed_out" | "judge_time_limit" | "judge_conclusion_failed" | "delegated_task_limit" | "context_window_unknown" | "bad_model" | "provider_request_rejected" | "model_route_unavailable" | "unknown";
  message: string;
  retryable: boolean;
  provider_message?: boolean;
}

export interface ErrorFrame {
  type: "error";
  session_id?: string;
  message: string;
  client_message_id?: string;
  first_message_error?: "not_saved" | "delivery_unknown" | "answer_not_started";
  payload?: {
    llm_error: LLMError;
  };
  seq?: number;
}

export interface ToolCallStartFrame {
  history_origin?: HistoryEntryOrigin;
  type: "tool_call_start";
  session_id: string;
  tool: string;
  call_id: string;
  params: {
    [key: string]: unknown;
  };
  parent_call_id?: string;
  agent_id?: string;
  seq?: number;
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

export interface HistoryResultPreview {
  _history_preview: true;
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
  code?: "protected_internal_file";
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
  history_origin?: HistoryEntryOrigin;
  type: "tool_call_result";
  session_id: string;
  tool: string;
  call_id: string;
  result: unknown;
  status: "success" | "error" | "cancelled";
  duration_ms?: number;
  error?: string;
  parent_call_id?: string;
  agent_id?: string;
  seq?: number;
}

export interface SubagentStartFrame {
  history_origin?: HistoryEntryOrigin;
  type: "subagent_start";
  session_id: string;
  span_id: string;
  parent_call_id: string;
  task_label: string;
  agent_id?: string;
  child_session_id?: string;
  seq?: number;
}

export interface SubagentEndFrame {
  history_origin?: HistoryEntryOrigin;
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
  seq?: number;
}

export interface SubagentMessageFrame {
  history_origin?: HistoryEntryOrigin;
  type: "subagent_message";
  session_id: string;
  child_session_id?: string;
  span_id: string;
  message_id: string;
  kind: "progress" | "checkpoint" | "artifact" | "blocker" | "question" | "error" | "handback" | "steer" | "respond" | "goal_status" | "not_delivered";
  text?: string;
  pct?: number;
  correlation_id?: string;
  sender_identity: string;
  untrusted_origin: boolean;
  created_at: string;
  seq?: number;
}

export interface SubagentStateFrame {
  history_origin?: HistoryEntryOrigin;
  type: "subagent_state";
  session_id: string;
  child_session_id?: string;
  span_id: string;
  state: "queued" | "running" | "needs_input" | "stopped" | "completed" | "failed";
  control_receipt?: {
    seq: number;
    control_id: string;
    verb: "steer" | "stop" | "stop_all" | "redirect" | "resume" | "respond" | "clear_goal";
    state: "queued" | "delivered" | "applied" | "superseded";
    accepted_at: string;
    delivered_at?: string;
    applied_at?: string;
    superseded_at?: string;
    superseded_by_seq?: number;
    reason?: string;
    released_control_ids?: Array<string>;
  };
  created_at: string;
  seq?: number;
}

export interface TaskStatusChangedFrame {
  type: "task_status_changed";
  session_id: string;
  task_id: string;
  status: "inbox" | "next" | "in_progress" | "blocked" | "done" | "failed";
  agent_id?: string;
}

export interface TaskRunStatusFrame {
  type: "task_run_status";
  task_id: string;
  run_id: string;
  occurrence_ms?: number | null;
  status: "in_progress" | "done" | "failed" | "skipped";
}

export interface ReplayMessageFrame {
  history_origin?: HistoryEntryOrigin;
  room_ref?: RoomRef;
  type: "replay_message";
  session_id: string;
  content: string;
  content_original_size_bytes?: number;
  role: "user" | "assistant" | "system" | "turn_canceled";
  id?: string;
  reply_to_message_id?: string;
  participant?: ChatParticipant;
  reply_to_participant?: ChatParticipant;
  timestamp?: string;
  agent_id?: string;
  model?: string;
  turn_id?: string;
  goal_id?: string;
  truncated?: boolean;
  truncation_reason?: "cancelled" | "max_output_tokens";
  client_message_id?: string;
  input_disposition?: {
    message_id: string;
    client_message_id?: string;
    state: "discarded";
    reason: "stopped_before_delivery";
  };
  seq?: number;
  terminal_outcome?: boolean;
}

export interface ReplayErrorFrame {
  history_origin?: HistoryEntryOrigin;
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
  seq?: number;
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
  seq?: number;
}

export interface ProviderRetryFrame {
  type: "provider_retry";
  session_id: string;
  turn_id: string;
  provider: string;
  model: string;
  retry_at: string;
  retry_after_seconds: number;
  sent_at: string;
  attempt: number;
  max_attempts: number;
  error_code: string;
  seq?: number;
}

export interface ContextWindowNoticeFrameNotice {
  kind: "provider_retry" | "mid_turn";
  message: string;
}

export interface ContextWindowNoticeFrame {
  history_origin?: HistoryEntryOrigin;
  type: "context_window_notice";
  session_id: string;
  turn_id: string;
  agent_id: string;
  entry_id: string;
  timestamp: string;
  notice: ContextWindowNoticeFrameNotice;
  seq?: number;
}

export interface ProviderFallbackFrame {
  type: "provider_fallback";
  session_id: string;
  turn_id: string;
  answered_model: string;
  unavailable_model: string;
  unavailable_code: "rate_limited" | "model_retired" | "model_not_found";
  seq?: number;
}

export interface ProviderFallbackNote {
  history_origin?: HistoryEntryOrigin;
  type: "replay_provider_fallback";
  session_id: string;
  entry_id: string;
  timestamp: string;
  answered_model?: string;
  unavailable_model?: string;
  unavailable_code?: "rate_limited" | "model_retired" | "model_not_found";
  message: string;
}

export interface LibraryChangedFrame {
  type: "library_changed";
  workspace_id: string;
  path?: string;
  reason?: string;
}

export interface MediaPart {
  type: "image" | "audio" | "video" | "file";
  url: string;
  filename: string;
  content_type: string;
  caption?: string;
}

export interface MediaFrame {
  history_origin?: HistoryEntryOrigin;
  type: "media";
  session_id: string;
  parts: Array<MediaPart>;
  seq?: number;
}

export interface CommandSegmentInfo {
  segment_index: number;
  command_text: string;
  resolved_binary?: string;
  args?: Array<string>;
  classification?: "read" | "write" | "read_write" | "none";
  path?: string;
  network_required?: boolean;
  suggested_prefix?: string;
  prefix_available?: boolean;
}

export interface ToolApprovalRequiredFrame {
  root_session_id?: string;
  acting_agent_name?: string;
  origin_label?: string;
  room_ref?: RoomRef;
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
  workspace_id?: string;
  segments?: Array<CommandSegmentInfo>;
}

export interface ToolApprovalResolvedFrame {
  root_session_id?: string;
  type: "tool_approval_resolved";
  approval_id: string;
  state: "approved" | "denied_user" | "denied_timeout" | "denied_cancel" | "denied_restart" | "denied_batch_short_circuit";
  session_id?: string;
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
  history_origin?: HistoryEntryOrigin;
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
  tool_call_id: string;
  turn_id: string;
  args: {
    [key: string]: unknown;
  };
  segments?: Array<CommandSegmentInfo>;
  root_session_id?: string;
  acting_agent_name?: string;
  origin_label?: string;
  room_ref?: RoomRef;
  approval_id: string;
  session_id: string;
  tool_name: string;
  agent_id: string;
  expires_in_ms: number;
  workspace_id?: string;
}

export interface SessionStateActiveTurn {
  turn_id: string;
  agent_id: string;
  started_at: string;
}

export interface SessionStateFrame {
  history_state?: SessionHistoryState;
  secret_requests?: Array<SessionStateSecretRequest>;
  type: "session_state";
  user_id: string;
  pending_approvals: Array<SessionStatePendingApproval>;
  pending_asks?: Array<AskUserQuestionCard>;
  session_id?: string;
  session_tokens_total?: number;
  attention_bound?: number;
  auto_approve_modifier?: boolean | null;
  active_turn?: SessionStateActiveTurn;
  boot_id?: string;
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
  reached?: Array<string>;
  unreachable?: Array<{
    id: string;
    reason: string;
  }>;
  skipped_newer_generation?: Array<string>;
  skipped_terminal?: Array<string>;
  partial?: boolean;
  seq?: number;
}

export interface SessionModeUpdateFrame {
  type: "session_mode_update";
  session_id: string;
  auto_approve: boolean | null;
}

export interface SessionModeUpdatedFrame {
  type: "session_mode_updated";
  session_id: string;
  auto_approve_effective: boolean;
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
  request_id?: string;
  session_id?: string;
  type: "whatsapp_pairing";
  channel_id: string;
  status: "waiting" | "code" | "linked" | "timeout" | "error" | "stopped";
  qr?: string;
  message?: string;
}

export interface WhatsAppPairingSubscribeFrame {
  request_id?: string;
  session_id?: string;
  type: "whatsapp_pairing_subscribe";
  channel_id: string;
  active: boolean;
}

export interface NotificationFrame {
  type: "notification";
  id: string;
  notification_type: "schedule_failed" | "knowledge_drift" | "stop_notice";
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
  input_mode?: "websocket" | "dedicated";
}

export interface BrowserInputFrame {
  type: "browser_input";
  kind: "mouse_move" | "mouse_down" | "mouse_up" | "wheel" | "key_down" | "key_up" | "text" | "navigate" | "navigate_back" | "reload" | "stop_loading";
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
  capture_generation?: number;
  capture_id?: string;
  input_epoch?: number;
  control_epoch?: number;
  reliable_seq?: number;
  hover_seq?: number;
  gesture_barrier?: number;
}

export interface BrowserControlFrame {
  type: "browser_control";
  action: "take" | "release";
  input_epoch?: number;
  control_epoch?: number;
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
  operation_only?: boolean;
}

export interface BrowserViewportFrame {
  type: "browser_viewport";
  session_id?: string;
  agent_id?: string;
  width: number;
  height: number;
  device_scale_factor?: number;
  input_epoch?: number;
  control_epoch?: number;
}

export interface BrowserTabActionFrame {
  type: "browser_tab_action";
  session_id?: string;
  agent_id?: string;
  action: "switch" | "close" | "open";
  index?: number;
  input_epoch?: number;
  control_epoch?: number;
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
  capture_generation?: number;
  capture_id?: string;
  offer_id?: number;
}

export interface BrowserWebRTCAnswerFrame {
  type: "browser_webrtc_answer";
  session_id?: string;
  sdp: string;
  capture_generation?: number;
  capture_id?: string;
  offer_id?: number;
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

export interface BrowserVideoHealthFrame {
  type: "browser_video_health";
  session_id?: string;
  state: "transitioning" | "lost" | "recovering" | "recovered" | "unrecoverable";
  attempt?: number;
  max_attempts?: number;
  detail?: string;
  capture_generation?: number;
  target_id?: string;
  rtp_timestamp?: number;
  capture_id?: string;
  css_width?: number;
  css_height?: number;
}

export interface BrowserCaptureHelloFrame {
  type: "browser_capture_hello";
  token: string;
  ext_version: string;
}

export interface BrowserCaptureOfferFrame {
  type: "browser_capture_offer";
  sdp: string;
  capture_generation?: number;
  target_id?: string;
  offer_id?: number;
}

export interface BrowserCaptureAnswerFrame {
  type: "browser_capture_answer";
  sdp: string;
  offer_id?: number;
  capture_generation?: number;
  target_id?: string;
}

export interface BrowserCaptureControlFrame {
  type: "browser_capture_control";
  action: "recapture" | "shutdown" | "ping" | "adapt_reset" | "set_bitrate" | "input_pressure";
  reason?: string;
  max_bitrate?: number;
  expected_width?: number;
  expected_height?: number;
  capture_scale?: number;
  capture_health?: {
    generation: number;
    track_state: "live" | "ended" | "absent";
    track_muted: boolean;
    peer_state: "new" | "connecting" | "connected" | "disconnected" | "failed" | "closed" | "absent";
    source_frames?: number;
    encoded_frames?: number;
    packets_sent?: number;
    sample_timestamp_ms?: number;
  };
  capture_generation?: number;
  target_id?: string;
}

export interface GoalStatusFrame {
  type: "goal_status";
  session_id: string;
  goal_id?: string;
  condition: string;
  definition?: string;
  round: number;
  max_rounds: number;
  latest_reason: string;
  active_loops: number;
  cap: number;
  state: "active" | "waiting_on_user" | "judge_unavailable" | "re-planning" | "judging" | "done" | "failed" | "cleared" | "judge_cas_loss" | "blocked" | "claim_overturned" | "expired";
  criteria?: Array<{
    id?: string;
    kind: "check" | "prose" | "behavior";
    judgment: "boolean" | "quantitative" | "artifact";
    provenance?: "stated" | "workspace" | "floor" | "inferred";
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
    clause_count?: number;
  }>;
  dod?: Array<{
    id?: string;
    kind: "check" | "prose" | "behavior";
    judgment: "boolean" | "quantitative" | "artifact";
    provenance?: "stated" | "workspace" | "floor" | "inferred";
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
    clause_count?: number;
  }>;
  seq?: number;
}

export interface LoopStatusFrame {
  type: "loop_status";
  session_id: string;
  mode: "interval" | "self_paced";
  run: number;
  max_runs: number;
  next_delay?: number;
  state: string;
  seq?: number;
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
  history_origin?: HistoryEntryOrigin;
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
    evidence_source?: "diff" | "transcript" | "machine_check" | "file_read" | "session_read";
    evidence_target?: string;
    provenance?: "judge_read" | "deterministic_check" | "diff" | "transcript" | "session_read" | "none";
    evidence?: Array<{
      part: string;
      source?: string;
      target?: string;
      quote: string;
    }>;
  }>;
  model: string;
  judged_at: string;
  judge_agent_id: string;
  session_id?: string;
  seq?: number;
}

export interface BrowserHandoverNoticeFrame {
  history_origin?: HistoryEntryOrigin;
  type: "browser_handover_notice";
  session_id: string;
  message_id: string;
  text: string;
}

export interface HelperEventFrameEvent {
  helper_session_id: string;
  helper_title?: string;
  kind: "verdict_unmet" | "not_verified" | "stopped" | "restarted" | "resume_refused" | "timed_out" | "finished" | "question" | "blocker" | "failed";
  text: string;
  goal_id?: string;
  created_at: string;
}

export interface HelperEventFrame {
  history_origin?: HistoryEntryOrigin;
  type: "helper_event";
  session_id: string;
  message_id: string;
  helper_event: HelperEventFrameEvent;
  seq?: number;
}

export interface GoalOutcomeFrameOutcome {
  goal_id: string;
  goal_text: string;
  ending: "met" | "rounds_exhausted" | "stopped_by_user" | "other";
  rounds_used: number;
  max_rounds: number;
  judge_reason?: string;
  criteria_total?: number;
  ended_at: string;
}

export interface GoalOutcomeFrame {
  history_origin?: HistoryEntryOrigin;
  type: "goal_outcome";
  session_id: string;
  message_id: string;
  outcome: GoalOutcomeFrameOutcome;
  seq?: number;
}

export interface KnowledgeIndexProgressFrame {
  type: "knowledge_index_progress";
  collection_id: string;
  workspace_id: string;
  phase: "enumerating" | "indexing" | "idle" | "failed";
  indexed_files: number;
  total_known: boolean;
  total_files?: number;
  skipped_files?: number;
  error?: string;
  updated_at?: string;
}

export interface ErrorPayload {
  llm_error: LLMError;
}

export interface ReplayErrorPayload {
  llm_error: LLMErrorReplay;
}

export interface BrowserInputOfferFrame {
  type: "browser_input_offer";
  session_id: string;
  input_epoch: number;
  control_epoch: number;
  offer_id: number;
  agent_id: string;
  sdp: string;
}

export interface BrowserInputAnswerFrame {
  type: "browser_input_answer";
  session_id: string;
  input_epoch: number;
  control_epoch: number;
  offer_id: number;
  sdp: string;
}

export interface BrowserInputStateFrame {
  type: "browser_input_state";
  session_id: string;
  input_epoch: number;
  control_epoch: number;
  offer_id: number;
  state: "connecting" | "ready" | "failed" | "closed";
  reason?: string;
}

export interface BrowserInputControlAckFrame {
  type: "browser_input_control_ack";
  session_id: string;
  input_epoch: number;
  control_epoch: number;
  ok: boolean;
  reason?: string;
  capture_id?: string;
  capture_generation?: number;
}

export interface SessionHistoryContext {
  spans: Array<{
    span_id: string;
    start?: SubagentStartFrame;
    state?: SubagentStateFrame;
    message?: SubagentMessageFrame;
    end?: SubagentEndFrame;
    has_run?: boolean;
  }>;
  duplicate_ids: Array<{
    kind: "message" | "tool";
    id: string;
    canonical_position: string;
    occurrence_count: number;
  }>;
}

export interface SessionHistoryPageInfo {
  mode: "latest" | "older" | "newer" | "window_refresh";
  limit: number;
  min_rows: number;
  row_count: number;
  source_group_count: number;
  soft_byte_budget: number;
  byte_ceiling: number;
  projected_bytes: number;
  byte_budget_exceeded: boolean;
  stop_reason: "row_target" | "source_cap" | "clear" | "end" | "range_end" | "byte_budget" | "byte_ceiling";
  budget_override?: "height_floor" | "atomic_group";
  first_position?: string;
  last_position?: string;
  stopped_at_clear?: boolean;
  latest_clear?: {
    entry_id: string;
    history_position: string;
  };
  retention_generation: string;
  context: SessionHistoryContext;
}

export interface SessionSnapshotFrame {
  type: "session_snapshot";
  session_id: string;
  seq: number;
  boot_id?: string;
  has_earlier: boolean;
  has_later: boolean;
  next_before?: string;
  next_after?: string;
  page_info: SessionHistoryPageInfo;
  reason?: "cursor_ahead" | "retention_exceeded" | "unknown_position" | "boot_mismatch";
  secret_requests?: Array<SessionStateSecretRequest>;
}

export interface CatchUpCompleteFrame {
  type: "catch_up_complete";
  session_id: string;
  seq: number;
  boot_id?: string;
  mode: "incremental" | "snapshot" | "live_only";
}

export interface UserMessageFrame {
  room_ref?: RoomRef;
  type: "user_message";
  session_id: string;
  id: string;
  client_message_id?: string;
  content: string;
  attachments?: Array<{
    type: "image" | "audio" | "video" | "file";
    path: string;
    size: number;
    mime_type: string;
  }>;
  timestamp: string;
  agent_id?: string;
  participant?: ChatParticipant;
  seq?: number;
}

export interface AgentCreatedFrame {
  type: "agent_created";
  agent_id: string;
}

export interface MailPanelObserverFrame {
  type: "mail_panel_observer";
  action: "open" | "close";
  observer_id: string;
  workspace_id: string;
}

export interface MailPanelObserverAckFrame {
  type: "mail_panel_observer_ack";
  action: "open" | "close";
  observer_id: string;
  workspace_id: string;
}

export interface MailPanelObserverErrorFrame {
  type: "mail_panel_observer_error";
  observer_id?: string;
  workspace_id?: string;
  code: "unauthorized_workspace" | "malformed_frame";
  error: string;
}

// ── Union of all WS frames (discriminated by the `type` field) ──────────────

export type WsFrame =
  | SecretRequestFrame
  | SecretRequestResolvedFrame
  | RoomEntryFrame
  | RoomMemberStateFrame
  | AttachRoomFrame
  | RoomSnapshotFrame
  | RoomMetaFrame
  | AuthFrame
  | MessageFrame
  | CancelFrame
  | RedirectFrame
  | PingFrame
  | PongFrame
  | AttachSessionFrame
  | DevicePairingResponseFrame
  | SessionStartedFrame
  | MessageStatusFrame
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
  | ProviderRetryFrame
  | ContextWindowNoticeFrame
  | ProviderFallbackFrame
  | ProviderFallbackNote
  | LibraryChangedFrame
  | MediaFrame
  | ToolApprovalRequiredFrame
  | ToolApprovalResolvedFrame
  | AskUserQuestionFrame
  | AskUserAnswerFrame
  | SessionStateFrame
  | SystemOverloadFrame
  | ReplayWarningFrame
  | CancelStageFrame
  | SessionModeUpdateFrame
  | SessionModeUpdatedFrame
  | DevicePairingRequestFrame
  | WhatsAppPairingFrame
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
  | BrowserVideoHealthFrame
  | BrowserCaptureHelloFrame
  | BrowserCaptureOfferFrame
  | BrowserCaptureAnswerFrame
  | BrowserCaptureControlFrame
  | GoalStatusFrame
  | LoopStatusFrame
  | PlanStatusFrame
  | JudgeVerdictFrame
  | BrowserHandoverNoticeFrame
  | HelperEventFrame
  | GoalOutcomeFrame
  | KnowledgeIndexProgressFrame
  | BrowserInputOfferFrame
  | BrowserInputAnswerFrame
  | BrowserInputStateFrame
  | BrowserInputControlAckFrame
  | SessionSnapshotFrame
  | CatchUpCompleteFrame
  | UserMessageFrame
  | AgentCreatedFrame
  | MailPanelObserverFrame
  | MailPanelObserverAckFrame
  | MailPanelObserverErrorFrame;

// ── Client → server frames ──────────────────────────────────────────────────

export type ClientFrame =
  | AttachRoomFrame
  | AuthFrame
  | MessageFrame
  | CancelFrame
  | RedirectFrame
  | PingFrame
  | AttachSessionFrame
  | DevicePairingResponseFrame
  | AskUserAnswerFrame
  | SessionModeUpdateFrame
  | WhatsAppPairingSubscribeFrame
  | BrowserAttachFrame
  | BrowserInputFrame
  | BrowserControlFrame
  | BrowserDetachFrame
  | BrowserViewportFrame
  | BrowserTabActionFrame
  | BrowserWebRTCOfferFrame
  | BrowserInputOfferFrame
  | MailPanelObserverFrame;

// ── ClientFrameTypes constant — generated from spec, not hand-written ─────────
// Import this in ws.ts to build CLIENT_FRAME_TYPES set. Never edit directly.

export const ClientFrameTypes = ["attach_room", "auth", "message", "cancel", "redirect", "ping", "attach_session", "device_pairing_response", "ask_user_answer", "session_mode_update", "whatsapp_pairing_subscribe", "browser_attach", "browser_input", "browser_control", "browser_detach", "browser_viewport", "browser_tab_action", "browser_webrtc_offer", "browser_input_offer", "mail_panel_observer"] as const

// ── Server → client frames ──────────────────────────────────────────────────

export type ServerFrame =
  | SecretRequestFrame
  | SecretRequestResolvedFrame
  | RoomEntryFrame
  | RoomMemberStateFrame
  | RoomSnapshotFrame
  | RoomMetaFrame
  | PongFrame
  | SessionStartedFrame
  | MessageStatusFrame
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
  | ProviderRetryFrame
  | ContextWindowNoticeFrame
  | ProviderFallbackFrame
  | ProviderFallbackNote
  | LibraryChangedFrame
  | MediaFrame
  | ToolApprovalRequiredFrame
  | ToolApprovalResolvedFrame
  | AskUserQuestionFrame
  | SessionStateFrame
  | SystemOverloadFrame
  | ReplayWarningFrame
  | CancelStageFrame
  | SessionModeUpdatedFrame
  | DevicePairingRequestFrame
  | WhatsAppPairingFrame
  | NotificationFrame
  | BrowserStatusFrame
  | BrowserTabsFrame
  | BrowserWebRTCAnswerFrame
  | BrowserWebRTCStateFrame
  | BrowserVideoHealthFrame
  | BrowserCaptureHelloFrame
  | BrowserCaptureOfferFrame
  | BrowserCaptureAnswerFrame
  | BrowserCaptureControlFrame
  | GoalStatusFrame
  | LoopStatusFrame
  | PlanStatusFrame
  | JudgeVerdictFrame
  | BrowserHandoverNoticeFrame
  | HelperEventFrame
  | GoalOutcomeFrame
  | KnowledgeIndexProgressFrame
  | BrowserInputAnswerFrame
  | BrowserInputStateFrame
  | BrowserInputControlAckFrame
  | SessionSnapshotFrame
  | CatchUpCompleteFrame
  | UserMessageFrame
  | AgentCreatedFrame
  | MailPanelObserverAckFrame
  | MailPanelObserverErrorFrame;
