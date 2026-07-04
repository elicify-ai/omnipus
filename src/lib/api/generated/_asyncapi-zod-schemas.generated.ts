// @ts-nocheck
// Fragment — concatenated into schemas.ts by _gen-ts.sh. Do not import directly.

// ── AsyncAPI WebSocket frame schemas ─────────────────────────────────────────
// Auto-generated from contracts/asyncapi.yaml components.schemas.
// Do not edit directly — re-run: node scripts/_gen-asyncapi-types.mjs
// These extend the REST schemas above with all WS frame types.

export const WsFrameType = z.enum(["auth", "message", "cancel", "ping", "attach_session", "device_pairing_response", "session_close", "session_started", "token", "done", "error", "tool_call_start", "tool_call_result", "subagent_start", "subagent_end", "task_status_changed", "replay_message", "replay_error", "rate_limit", "media", "agent_switched", "tool_approval_required", "session_state", "system_overload", "replay_warning", "cancel_stage", "pong", "session_close_ack", "device_pairing_request", "whatsapp_pairing", "whatsapp_pairing_subscribe", "notification"]);

export const AuthFrame = z
  .object({
    type: z.literal("auth"),
    token: z.string().min(72).max(81).regex(/^omnipus_([a-f0-9]{8}_)?[a-f0-9]{64}$/),
  })
  .strict();

export const MessageFrame = z
  .object({
    type: z.literal("message"),
    content: z.string().min(1).max(5242880),
    session_id: z.string().min(1).max(128).optional(),
    agent_id: z.string().min(1).max(128).optional(),
    media: z.array(z.string().min(1).max(256)).max(16).optional(),
    metadata: z
    .object({
      model_name: z.string().min(1).max(256).optional(),
    })
    .passthrough().optional(),
  })
  .strict();

export const CancelFrame = z
  .object({
    type: z.literal("cancel"),
    session_id: z.string().min(1).max(128),
  })
  .strict();

export const PingFrame = z
  .object({
    type: z.literal("ping"),
  })
  .strict();

export const PongFrame = z
  .object({
    type: z.literal("pong"),
  })
  .strict();

export const AttachSessionFrame = z
  .object({
    type: z.literal("attach_session"),
    session_id: z.string().min(1).max(128),
    since: z.string().optional(),
  })
  .strict();

export const DevicePairingResponseFrame = z
  .object({
    type: z.literal("device_pairing_response"),
    device_id: z.string().min(1),
    decision: z.enum(["approve", "reject"]),
  })
  .strict();

export const SessionStartedFrame = z
  .object({
    type: z.literal("session_started"),
    session_id: z.string().min(1),
    agent_id: z.string().optional(),
  })
  .strict();

export const TokenFrame = z
  .object({
    type: z.literal("token"),
    session_id: z.string().min(1).max(128),
    content: z.string().max(65536),
  })
  .strict();

export const DoneStats = z
  .object({
    tokens: z.number().min(0).optional(),
    cost: z.number().min(0).optional(),
    duration_ms: z.number().min(0).optional(),
    tokens_dropped: z.number().min(0).optional(),
    frames_emitted: z.number().min(0).optional(),
    orphan_count: z.number().min(0).optional(),
    duplicate_tool_call_id_count: z.number().min(0).optional(),
    truncated_result_count: z.number().min(0).optional(),
    replay_error: z.boolean().optional(),
    turn_failed: z.boolean().optional(),
  })
  .passthrough();

export const DoneFrame = z
  .object({
    type: z.literal("done"),
    session_id: z.string().min(1),
    stats: DoneStats.optional(),
  })
  .strict();

export const ErrorFrame = z
  .object({
    type: z.literal("error"),
    session_id: z.string().max(128).optional(),
    message: z.string().min(1).max(4096),
  })
  .strict();

export const ToolCallStartFrame = z
  .object({
    type: z.literal("tool_call_start"),
    session_id: z.string().min(1).max(128),
    tool: z.string().min(1).max(128),
    call_id: z.string().min(1),
    params: z.record(z.unknown()),
    parent_call_id: z.string().optional(),
    agent_id: z.string().optional(),
  })
  .strict();

export const TruncatedResult = z
  .object({
    _truncated: z.literal(true),
    original_size_bytes: z.number().int(),
    preview: z.string(),
  })
  .strict();

export const MarshalErrorResult = z
  .object({
    _marshal_error: z.string().min(1),
  })
  .strict();

export const ToolResultRef = z
  .object({
    _ref: z.literal(true),
    ref: z.string().min(1).max(128),
    original_size_bytes: z.number().int().min(0),
    preview: z.string(),
  })
  .strict();

export const DelegationFailure = z
  .object({
    error: z.literal("delegation_denied"),
    reason: z.string().min(1),
    policy: z.enum(["trust_set", "mode", "depth"]),
    tool: z.string().min(1),
    target_agent_id: z.string().optional(),
  })
  .strict();

export const ToolCallResultFrame = z
  .object({
    type: z.literal("tool_call_result"),
    session_id: z.string().min(1).max(128),
    tool: z.string().min(1).max(128),
    call_id: z.string().min(1),
    result: z.unknown(),
    status: z.enum(["success", "error"]),
    duration_ms: z.number().int().optional(),
    error: z.string().optional(),
    parent_call_id: z.string().optional(),
    agent_id: z.string().optional(),
  })
  .strict();

export const SubagentStartFrame = z
  .object({
    type: z.literal("subagent_start"),
    session_id: z.string().min(1),
    span_id: z.string().min(1),
    parent_call_id: z.string().min(1),
    task_label: z.string().max(100),
    agent_id: z.string().optional(),
  })
  .strict();

export const SubagentEndFrame = z
  .object({
    type: z.literal("subagent_end"),
    session_id: z.string().min(1),
    span_id: z.string().min(1),
    status: z.enum(["success", "error", "cancelled", "interrupted", "timeout"]),
    duration_ms: z.number().int().optional(),
    final_result: z.string().optional(),
    reason: z.enum(["parent_timeout", "parent_cancelled", "parent_done_early", "unknown"]).optional(),
    agent_id: z.string().optional(),
    parent_call_id: z.string().optional(),
    message: z.string().optional(),
  })
  .strict();

export const TaskStatusChangedFrame = z
  .object({
    type: z.literal("task_status_changed"),
    session_id: z.string().min(1),
    task_id: z.string().min(1),
    status: z.enum(["inbox", "next", "planning", "in_progress", "blocked", "done", "failed"]),
    agent_id: z.string().optional(),
  })
  .strict();

export const ReplayMessageFrame = z
  .object({
    type: z.literal("replay_message"),
    session_id: z.string().min(1),
    content: z.string(),
    role: z.enum(["user", "assistant", "system", "turn_canceled"]),
    id: z.string().optional(),
    timestamp: z.string().optional(),
    agent_id: z.string().optional(),
    model: z.string().max(256).optional(),
  })
  .strict();

export const ReplayErrorFrame = z
  .object({
    type: z.literal("replay_error"),
    session_id: z.string().min(1),
    entry_id: z.string(),
    timestamp: z.string(),
    kind: z.enum(["rate_limit", "error"]),
    message: z.string(),
    agent_id: z.string().optional(),
    payload: z
    .object({
      retry_after_seconds: z.number().optional(),
      policy_rule: z.string().optional(),
      scope: z.string().optional(),
      resource: z.string().optional(),
      tool: z.string().optional(),
      stage: z.string().optional(),
    })
    .strict().optional(),
  })
  .strict();

export const RateLimitFrame = z
  .object({
    type: z.literal("rate_limit"),
    session_id: z.string(),
    scope: z.enum(["agent", "channel", "global"]),
    resource: z.string().min(1),
    policy_rule: z.string().min(1),
    retry_after_seconds: z.number().min(0),
    agent_id: z.string().optional(),
    tool: z.string().max(128).optional(),
  })
  .strict();

export const MediaPart = z
  .object({
    type: z.enum(["image", "audio", "video", "file"]),
    url: z.string().min(1),
    filename: z.string().min(1),
    content_type: z.string().min(1),
    caption: z.string().optional(),
  })
  .strict();

export const MediaFrame = z
  .object({
    type: z.literal("media"),
    session_id: z.string().min(1),
    parts: z.array(MediaPart).min(1).max(32),
  })
  .strict();

export const AgentSwitchedFrame = z
  .object({
    type: z.literal("agent_switched"),
    session_id: z.string().min(1),
    agent_id: z.string().optional(),
    message: z.string().optional(),
  })
  .strict();

export const ToolApprovalRequiredFrame = z
  .object({
    type: z.literal("tool_approval_required"),
    approval_id: z.string().min(1),
    tool_call_id: z.string().min(1),
    tool_name: z.string().min(1).max(128),
    args: z.record(z.unknown()),
    agent_id: z.string().min(1),
    session_id: z.string().min(1),
    turn_id: z.string().min(1),
    expires_in_ms: z.number().int().min(0).max(86400000),
  })
  .strict();

export const SessionStatePendingApproval = z
  .object({
    approval_id: z.string().min(1),
    session_id: z.string().min(1),
    tool_name: z.string().min(1).max(128),
    agent_id: z.string().min(1),
    expires_in_ms: z.number().int().min(0).max(86400000),
  })
  .strict();

export const SessionStateFrame = z
  .object({
    type: z.literal("session_state"),
    user_id: z.string(),
    pending_approvals: z.array(SessionStatePendingApproval).max(1000),
    emitted_at: z.string(),
  })
  .strict();

export const SystemOverloadFrame = z
  .object({
    type: z.literal("system_overload"),
    session_id: z.string().min(1),
    message: z.string().optional(),
  })
  .strict();

export const ReplayWarningStats = z
  .object({
    duplicate_tool_call_id_count: z.number().int().optional(),
  })
  .passthrough();

export const ReplayWarningFrame = z
  .object({
    type: z.literal("replay_warning"),
    session_id: z.string().min(1),
    message: z.string().min(1),
    stats: ReplayWarningStats.optional(),
  })
  .strict();

export const CancelStageFrame = z
  .object({
    type: z.literal("cancel_stage"),
    session_id: z.string().min(1),
    stage: z.enum(["graceful", "hard", "detached"]),
  })
  .strict();

export const SessionCloseAckFrame = z
  .object({
    type: z.literal("session_close_ack"),
    session_id: z.string().min(1),
    id: z.string().optional(),
  })
  .strict();

export const DevicePairingRequestFrame = z
  .object({
    type: z.literal("device_pairing_request"),
    device_id: z.string().min(1),
    fingerprint: z.string().optional(),
    pairing_code: z.string().optional(),
    device_name: z.string().optional(),
    session_id: z.string().optional(),
  })
  .strict();

export const WhatsAppPairingFrame = z
  .object({
    type: z.literal("whatsapp_pairing"),
    channel_id: z.string().min(1),
    status: z.enum(["waiting", "code", "linked", "timeout", "error"]),
    qr: z.string().optional(),
    message: z.string().optional(),
  })
  .strict();

export const SessionCloseFrame = z
  .object({
    type: z.literal("session_close"),
    session_id: z.string().min(1),
  })
  .strict();

export const WhatsAppPairingSubscribeFrame = z
  .object({
    type: z.literal("whatsapp_pairing_subscribe"),
    channel_id: z.string().min(1),
    active: z.boolean(),
  })
  .strict();

export const NotificationFrame = z
  .object({
    type: z.literal("notification"),
    id: z.string().min(1),
    notification_type: z.literal("schedule_failed"),
    title: z.string().min(1),
    body: z.string().optional(),
    severity: z.enum(["info", "warning", "error"]),
    read: z.boolean(),
    created_at_ms: z.number().int(),
    schedule_id: z.string().optional(),
    session_id: z.string().optional(),
    agent_id: z.string().optional(),
  })
  .strict();

// ── WS frame discriminated union ─────────────────────────────────────────────

export const WsFrame = z.discriminatedUnion("type", [
  AuthFrame,
  MessageFrame,
  CancelFrame,
  PingFrame,
  PongFrame,
  AttachSessionFrame,
  DevicePairingResponseFrame,
  SessionStartedFrame,
  TokenFrame,
  DoneFrame,
  ErrorFrame,
  ToolCallStartFrame,
  ToolCallResultFrame,
  SubagentStartFrame,
  SubagentEndFrame,
  TaskStatusChangedFrame,
  ReplayMessageFrame,
  ReplayErrorFrame,
  RateLimitFrame,
  MediaFrame,
  AgentSwitchedFrame,
  ToolApprovalRequiredFrame,
  SessionStateFrame,
  SystemOverloadFrame,
  ReplayWarningFrame,
  CancelStageFrame,
  SessionCloseAckFrame,
  DevicePairingRequestFrame,
  WhatsAppPairingFrame,
  SessionCloseFrame,
  WhatsAppPairingSubscribeFrame,
  NotificationFrame,
]);

export type WsFrameType = z.infer<typeof WsFrameType>;
export type WsFrame = z.infer<typeof WsFrame>;