// @ts-nocheck
// Fragment — concatenated into schemas.ts by _gen-ts.sh. Do not import directly.

// ── AsyncAPI WebSocket frame schemas ─────────────────────────────────────────
// Auto-generated from contracts/asyncapi.yaml components.schemas.
// Do not edit directly — re-run: node scripts/_gen-asyncapi-types.mjs
// These extend the REST schemas above with all WS frame types.

export const WsFrameType = z.enum(["auth", "message", "cancel", "ping", "attach_session", "device_pairing_response", "session_close", "session_started", "token", "done", "error", "tool_call_start", "tool_call_result", "tool_result_projection", "subagent_start", "subagent_message", "subagent_state", "subagent_end", "task_status_changed", "task_run_status", "replay_message", "replay_error", "rate_limit", "media", "agent_switched", "tool_approval_required", "session_state", "system_overload", "replay_warning", "cancel_stage", "pong", "session_close_ack", "device_pairing_request", "whatsapp_pairing", "whatsapp_pairing_subscribe", "notification", "browser_attach", "browser_input", "browser_control", "browser_detach", "browser_status", "browser_tab_action", "browser_tabs", "browser_viewport", "browser_webrtc_offer", "browser_webrtc_answer", "browser_webrtc_state", "browser_capture_hello", "browser_capture_offer", "browser_capture_answer", "browser_capture_control", "goal_status", "loop_status", "plan_status", "judge_verdict"]);

export const AuthFrame = z
  .object({
    type: z.literal("auth"),
    token: z.string().min(72).max(81).regex(/^omnipus_([a-f0-9]{8}_)?[a-f0-9]{64}$/),
  })
  .strict();

export const MessageFrameBase = z
  .object({
    type: z.literal("message"),
    content: z.string().max(5242880),
    session_id: z.string().min(1).max(128).optional(),
    agent_id: z.string().min(1).max(128).optional(),
    media: z.array(z.string().min(1).max(256)).max(16).optional(),
    metadata: z
    .object({
      model_name: z.string().min(1).max(256).optional(),
      workspace_id: z.string().min(1).max(128).optional(),
      workspace_setup_kickoff: z.boolean().optional(),
    })
    .passthrough().optional(),
  })
  .strict();

export const MessageFrame = MessageFrameBase.refine((v) => ((typeof v["content"] === "string" && v["content"].length >= 1)) || ((Array.isArray(v["media"]) && v["media"].length >= 1)), {
  message: "does not satisfy the schema's anyOf constraint",
});

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
    producing_session_id: z.string().min(1).optional(),
  })
  .strict();

export const TokenFrame = z
  .object({
    type: z.literal("token"),
    session_id: z.string().min(1).max(128),
    content: z.string().max(65536),
    agent_id: z.string().optional(),
    producing_session_id: z.string().min(1).optional(),
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
    producing_session_id: z.string().min(1).optional(),
  })
  .strict();

export const LLMError = z
  .object({
    code: z.enum(["media_unsupported", "provider_rejected", "request_too_large", "provider_auth_failed", "rate_limited", "network", "content_policy", "context_too_long", "tool_args", "schema", "agent_not_configured", "workspace_unavailable", "model_unavailable", "needs_provider", "model_unassigned", "turn_canceled", "turn_timed_out", "context_unrecoverable", "context_window_unknown", "unknown"]),
    message: z.string().min(1).max(4096),
    retryable: z.boolean(),
    detail: z.string().max(2048).optional(),
  })
  .strict();

export const LLMErrorReplay = z
  .object({
    code: z.enum(["media_unsupported", "provider_rejected", "request_too_large", "provider_auth_failed", "rate_limited", "network", "content_policy", "context_too_long", "tool_args", "schema", "agent_not_configured", "workspace_unavailable", "model_unavailable", "needs_provider", "model_unassigned", "turn_canceled", "turn_timed_out", "context_unrecoverable", "context_window_unknown", "unknown"]),
    message: z.string().min(1).max(4096),
    retryable: z.boolean(),
  })
  .strict();

export const ErrorFrame = z
  .object({
    type: z.literal("error"),
    session_id: z.string().max(128).optional(),
    message: z.string().min(1).max(4096),
    payload: z
    .object({
      llm_error: LLMError,
    })
    .strict().optional(),
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
    producing_session_id: z.string().min(1).optional(),
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

export const FileExistsRefusal = z
  .object({
    error: z.literal("file_exists"),
    reason: z.string().min(1),
    tool: z.string().min(1),
    path: z.string().min(1),
  })
  .strict();

export const PermissionDenied = z
  .object({
    error: z.literal("permission_denied"),
    message: z.string().min(1),
    tool: z.string().min(1),
    reason: z.string().min(1),
    permanent: z.boolean(),
  })
  .strict();

export const ToolAssemblyDuplicate = z
  .object({
    error: z.literal("tool_assembly_duplicate"),
    message: z.string().min(1),
  })
  .strict();

export const ToolArgumentRefusal = z
  .object({
    error: z.literal("tool_arguments_too_large"),
    reason: z.string().min(1),
    tool: z.string().min(1).max(128),
    size_chars: z.number().int().min(1),
    cap_chars: z.number().int().min(1),
  })
  .strict();

export const ToolResultRecallMark = z
  .object({
    error: z.literal("tool_result_recall_mark"),
    tool: z.string().min(1).max(64),
    tool_call_id: z.string().min(1).max(64),
    archive_line: z.number().int().min(0),
    size_chars: z.number().int().min(1),
    turn: z.number().int().min(1),
    content_state: z.enum(["capped", "emptied"]),
    hint: z.string().min(1),
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
    producing_session_id: z.string().min(1).optional(),
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
    producing_session_id: z.string().min(1).optional(),
  })
  .strict();

export const SubagentEndFrame = z
  .object({
    type: z.literal("subagent_end"),
    session_id: z.string().min(1),
    span_id: z.string().min(1),
    status: z.enum(["success", "error", "cancelled", "interrupted", "timeout", "parked"]),
    duration_ms: z.number().int().optional(),
    final_result: z.string().optional(),
    reason: z.enum(["parent_timeout", "parent_cancelled", "parent_done_early", "unknown"]).optional(),
    agent_id: z.string().optional(),
    parent_call_id: z.string().optional(),
    message: z.string().optional(),
    producing_session_id: z.string().min(1).optional(),
  })
  .strict();

export const SubagentMessageFrame = z
  .object({
    type: z.literal("subagent_message"),
    session_id: z.string().min(1),
    span_id: z.string().min(1),
    message_id: z.string().min(1),
    kind: z.enum(["progress", "checkpoint", "artifact", "blocker", "question", "decision_request", "error", "handback", "steer", "respond"]),
    text: z.string().optional(),
    pct: z.number().int().min(0).max(100).optional(),
    correlation_id: z.string().optional(),
    sender_identity: z.string().min(1),
    untrusted_origin: z.boolean(),
    created_at: z.string(),
  })
  .strict();

export const SubagentStateFrame = z
  .object({
    type: z.literal("subagent_state"),
    session_id: z.string().min(1),
    span_id: z.string().min(1),
    state: z.enum(["queued", "running", "needs_input", "paused", "completed", "failed", "cancelled", "timed_out"]),
    steering_receipt: z
    .object({
      correlation_id: z.string(),
      applied_at: z.string(),
    })
    .strict().optional(),
    created_at: z.string(),
  })
  .strict();

export const TaskStatusChangedFrame = z
  .object({
    type: z.literal("task_status_changed"),
    session_id: z.string().min(1),
    task_id: z.string().min(1),
    status: z.enum(["inbox", "next", "in_progress", "blocked", "done", "failed"]),
    agent_id: z.string().optional(),
    producing_session_id: z.string().min(1).optional(),
  })
  .strict();

export const TaskRunStatusFrame = z
  .object({
    type: z.literal("task_run_status"),
    task_id: z.string().min(1),
    run_id: z.string().min(1),
    occurrence_ms: z.number().int().optional(),
    status: z.enum(["in_progress", "done", "failed", "skipped"]),
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
    turn_id: z.string().optional(),
  })
  .strict();

export const ReplayErrorFrame = z
  .object({
    type: z.literal("replay_error"),
    session_id: z.string().min(1),
    entry_id: z.string(),
    timestamp: z.string(),
    kind: z.enum(["rate_limit", "error"]),
    message: z.string().min(1).max(4096),
    agent_id: z.string().optional(),
    payload: z
    .object({
      llm_error: LLMErrorReplay,
    })
    .strict().optional(),
  })
  .strict();

export const ToolResultProjectionFrame = z
  .object({
    type: z.literal("tool_result_projection"),
    session_id: z.string().min(1).max(128),
    tool_call_id: z.string().min(1),
    archive_line: z.number().int().min(0),
    content_state: z.enum(["capped", "emptied"]),
    mark: z.string().max(2048).optional(),
    producing_session_id: z.string().min(1).optional(),
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
    producing_session_id: z.string().min(1).optional(),
  })
  .strict();

export const AgentSwitchedFrame = z
  .object({
    type: z.literal("agent_switched"),
    session_id: z.string().min(1),
    agent_id: z.string().optional(),
    message: z.string().optional(),
    producing_session_id: z.string().min(1).optional(),
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
    producing_session_id: z.string().min(1).optional(),
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
    producing_session_id: z.string().min(1).optional(),
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
    producing_session_id: z.string().min(1).optional(),
  })
  .strict();

export const SessionCloseAckFrame = z
  .object({
    type: z.literal("session_close_ack"),
    session_id: z.string().min(1),
    id: z.string().optional(),
    producing_session_id: z.string().min(1).optional(),
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

export const BrowserAttachFrame = z
  .object({
    type: z.literal("browser_attach"),
    session_id: z.string().min(1).max(128),
    agent_id: z.string().min(1).max(128),
  })
  .strict();

export const BrowserInputFrame = z
  .object({
    type: z.literal("browser_input"),
    kind: z.enum(["mouse_move", "mouse_down", "mouse_up", "wheel", "key_down", "key_up", "text", "navigate", "navigate_back", "reload"]),
    x: z.number().optional(),
    y: z.number().optional(),
    capture_width: z.number().min(1).max(16384).optional(),
    capture_height: z.number().min(1).max(16384).optional(),
    button: z.enum(["none", "left", "middle", "right", "back", "forward"]).optional(),
    delta_x: z.number().optional(),
    delta_y: z.number().optional(),
    key: z.string().max(64).optional(),
    code: z.string().max(64).optional(),
    key_code: z.number().int().min(0).max(255).optional(),
    text: z.string().max(8192).optional(),
    modifiers: z.number().int().min(0).max(15).optional(),
    url: z.string().max(2048).optional(),
  })
  .strict();

export const BrowserControlFrame = z
  .object({
    type: z.literal("browser_control"),
    action: z.enum(["take", "release"]),
  })
  .strict();

export const BrowserDetachFrame = z
  .object({
    type: z.literal("browser_detach"),
    session_id: z.string().max(128).optional(),
  })
  .strict();

export const BrowserStatusFrame = z
  .object({
    type: z.literal("browser_status"),
    state: z.enum(["attached", "idle", "controlling", "released", "detached", "error"]),
    message: z.string().max(512).optional(),
    controller: z.string().max(128).optional(),
    controlled_by_other: z.boolean().optional(),
    control_only: z.boolean().optional(),
    session_id: z.string().optional(),
  })
  .strict();

export const BrowserViewportFrame = z
  .object({
    type: z.literal("browser_viewport"),
    session_id: z.string().optional(),
    agent_id: z.string().optional(),
    width: z.number().int().min(1).max(8192),
    height: z.number().int().min(1).max(8192),
    device_scale_factor: z.number().min(1).max(3).optional(),
  })
  .strict();

export const BrowserTabActionFrame = z
  .object({
    type: z.literal("browser_tab_action"),
    session_id: z.string().max(128).optional(),
    agent_id: z.string().max(128).optional(),
    action: z.enum(["switch", "close", "open"]),
    index: z.number().int().min(0).optional(),
  })
  .strict();

export const BrowserTabsFrame = z
  .object({
    type: z.literal("browser_tabs"),
    session_id: z.string().max(128).optional(),
    active_index: z.number().int().min(0),
    tabs: z.array(z
    .object({
      index: z.number().int().min(0),
      title: z.string().max(512).optional(),
      url: z.string().max(4096).optional(),
      active: z.boolean().optional(),
    })
    .strict()).max(32),
  })
  .strict();

export const BrowserWebRTCOfferFrame = z
  .object({
    type: z.literal("browser_webrtc_offer"),
    agent_id: z.string().min(1).max(128),
    session_id: z.string().min(1).max(128),
    sdp: z.string().min(1).max(131072),
  })
  .strict();

export const BrowserWebRTCAnswerFrame = z
  .object({
    type: z.literal("browser_webrtc_answer"),
    session_id: z.string().max(128).optional(),
    sdp: z.string().min(1).max(131072),
  })
  .strict();

export const BrowserWebRTCStateFrame = z
  .object({
    type: z.literal("browser_webrtc_state"),
    session_id: z.string().max(128).optional(),
    available: z.boolean(),
    reason: z.enum(["disabled", "not_capable", "lite_build", "error", "multi_agent_capture_denied", "ingest_timeout"]).optional(),
    reason_detail: z.string().max(512).optional(),
    has_audio: z.boolean().optional(),
    active: z.boolean().optional(),
    ice_servers: z.array(z
    .object({
      urls: z.array(z.string().max(256)).max(4),
      username: z.string().max(256).optional(),
      credential: z.string().max(256).optional(),
    })
    .strict()).max(8).optional(),
  })
  .strict();

export const BrowserCaptureHelloFrame = z
  .object({
    type: z.literal("browser_capture_hello"),
    token: z.string().min(16).max(256),
    ext_version: z.string().min(1).max(32),
  })
  .strict();

export const BrowserCaptureOfferFrame = z
  .object({
    type: z.literal("browser_capture_offer"),
    sdp: z.string().min(1).max(131072),
  })
  .strict();

export const BrowserCaptureAnswerFrame = z
  .object({
    type: z.literal("browser_capture_answer"),
    sdp: z.string().min(1).max(131072),
  })
  .strict();

export const BrowserCaptureControlFrame = z
  .object({
    type: z.literal("browser_capture_control"),
    action: z.enum(["recapture", "shutdown", "ping", "adapt_reset", "set_bitrate"]),
    reason: z.string().max(512).optional(),
    max_bitrate: z.number().int().min(50000).max(40000000).optional(),
    expected_width: z.number().int().min(1).max(16384).optional(),
    expected_height: z.number().int().min(1).max(16384).optional(),
    capture_scale: z.number().min(1).max(4).optional(),
  })
  .strict();

export const GoalStatusFrame = z
  .object({
    type: z.literal("goal_status"),
    session_id: z.string().min(1),
    goal_id: z.string().min(1).optional(),
    condition: z.string(),
    round: z.number().int().min(0),
    max_rounds: z.number().int().min(1),
    latest_reason: z.string(),
    active_loops: z.number().int().min(0),
    cap: z.number().int().min(1),
    state: z.enum(["queued", "active", "waiting_on_user", "judge_unavailable", "re-planning", "judging", "done", "failed", "cleared"]),
    producing_session_id: z.string().min(1).optional(),
  })
  .strict();

export const LoopStatusFrame = z
  .object({
    type: z.literal("loop_status"),
    session_id: z.string().min(1),
    mode: z.enum(["interval", "self_paced"]),
    run: z.number().int().min(0),
    max_runs: z.number().int().min(1),
    next_delay: z.number().int().optional(),
    state: z.string(),
    producing_session_id: z.string().min(1).optional(),
  })
  .strict();

export const PlanStatusFrame = z
  .object({
    type: z.literal("plan_status"),
    plan_id: z.string(),
    state: z.enum(["draft", "approved", "running", "done", "failed"]),
    plan_phase: z.enum(["dispatching", "judging", "synthesizing", "idle", "awaiting_supervision", "stalled"]),
    progress: z.number().min(0).max(1),
    paused_reason: z.string().optional(),
  })
  .strict();

export const JudgeVerdictFrame = z
  .object({
    type: z.literal("judge_verdict"),
    id: z.string(),
    scope: z.enum(["task", "plan", "goal"]),
    task_id: z.string().optional(),
    plan_id: z.string().optional(),
    round: z.number().int().min(1),
    met: z.boolean(),
    per_criterion: z.array(z
    .object({
      criterion_id: z.string().min(1),
      met: z.boolean(),
      reason: z.string(),
    })
    .strict()),
    model: z.string(),
    judged_at: z.string(),
    judge_agent_id: z.string(),
  })
  .strict();

export const ErrorPayload = z
  .object({
    llm_error: LLMError,
  })
  .strict();

export const ReplayErrorPayload = z
  .object({
    llm_error: LLMErrorReplay,
  })
  .strict();

// ── WS frame discriminated union ─────────────────────────────────────────────

export const WsFrame = z.discriminatedUnion("type", [
  AuthFrame,
  MessageFrameBase,
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
  SubagentMessageFrame,
  SubagentStateFrame,
  TaskStatusChangedFrame,
  TaskRunStatusFrame,
  ReplayMessageFrame,
  ReplayErrorFrame,
  ToolResultProjectionFrame,
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
  BrowserAttachFrame,
  BrowserInputFrame,
  BrowserControlFrame,
  BrowserDetachFrame,
  BrowserStatusFrame,
  BrowserViewportFrame,
  BrowserTabActionFrame,
  BrowserTabsFrame,
  BrowserWebRTCOfferFrame,
  BrowserWebRTCAnswerFrame,
  BrowserWebRTCStateFrame,
  BrowserCaptureHelloFrame,
  BrowserCaptureOfferFrame,
  BrowserCaptureAnswerFrame,
  BrowserCaptureControlFrame,
  GoalStatusFrame,
  LoopStatusFrame,
  PlanStatusFrame,
  JudgeVerdictFrame,
]);

export type WsFrameType = z.infer<typeof WsFrameType>;
export type WsFrame = z.infer<typeof WsFrame>;