import { makeApi, Zodios, type ZodiosOptions } from "@zodios/core";
import { z } from "zod";

type OnboardingCompleteResponse = LoginResponse;
type LoginResponse = {
  token: string;
  role: "admin" | "user";
  username: string;
  warning?: string | undefined;
};
type Session = {
  id: string;
  type?: ("chat" | "task" | "channel") | undefined;
  agent_id: string;
  title: string;
  status: "active" | "archived" | "interrupted";
  created_at: string;
  updated_at: string;
  model?: string | undefined;
  provider?: string | undefined;
  stats: SessionStats;
  project_id?: string | undefined;
  task_id?: string | undefined;
  channel: string;
  partitions: Array<string>;
  last_compaction_summary?: string | undefined;
  agent_ids?: Array<string> | undefined;
  active_agent_id?: string | undefined;
  compaction_summaries?: {} | undefined;
};
type SessionStats = {
  tokens_in: number;
  tokens_out: number;
  tokens_total: number;
  cost: number;
  tool_calls: number;
  message_count: number;
};
type SessionDetail = {
  session: Session;
  messages: Array<Message>;
};
type Message = {
  id: string;
  type?: ("message" | "compaction" | "system") | undefined;
  role?: ("user" | "assistant" | "system") | undefined;
  content?: string | undefined;
  summary?: string | undefined;
  timestamp: string;
  tokens?: number | undefined;
  cost?: number | undefined;
  status?: ("ok" | "error" | "interrupted") | undefined;
  attachments?: Array<Attachment> | undefined;
  tool_calls?: Array<ToolCall> | undefined;
  agent_id: string;
  messages_compacted?: number | undefined;
};
type Attachment = {
  type: string;
  path: string;
  size: number;
  mime_type: string;
};
type ToolCall = {
  id: string;
  tool: string;
  status: "success" | "error" | "pending" | "denied" | "running" | "cancelled";
  duration_ms?: number | undefined;
  parameters?: {} | undefined;
  result?: {} | undefined;
  parent_tool_call_id?: string | undefined;
};
type Agent = {
  id: string;
  name: string;
  type: "core" | "custom" | "system";
  locked: boolean;
  color?: string | undefined;
  icon?: string | undefined;
  model?: string | undefined;
  description?: string | undefined;
  status: "active" | "idle" | "draft" | "error";
  soul: string;
  heartbeat: string;
  instructions: string;
  warning?: string | undefined;
  timeout_seconds: number;
  max_tool_iterations: number;
  steering_mode: string;
  tool_feedback: boolean;
  heartbeat_enabled: boolean;
  heartbeat_interval: number;
  tools_cfg?: AgentToolsCfg | undefined;
  sandbox_profile?:
    | ("workspace" | "workspace+net" | "host" | "off")
    | undefined;
  shell_policy?:
    | Partial<{
        enable_deny_patterns: boolean;
        custom_deny_patterns: Array<string>;
      }>
    | undefined;
};
type AgentToolsCfg = Partial<{
  builtin: Partial<{
    default_policy: "allow" | "ask" | "deny";
    policies: {};
  }>;
  mcp: Partial<{
    servers: Array<{
      id: string;
      tools?: Array<string> | undefined;
    }>;
  }>;
}>;
type AgentCreateRequest = {
  name: string;
  description?: string | undefined;
  model?: string | undefined;
  color?: string | undefined;
  icon?: string | undefined;
  tools_cfg?: AgentToolsCfg | undefined;
};

export const LoginRequest = z
  .object({ username: z.string().min(1), password: z.string().min(1) })
  .passthrough();
export const LoginResponse: z.ZodType<LoginResponse> = z
  .object({
    token: z.string(),
    role: z.enum(["admin", "user"]),
    username: z.string(),
    warning: z.string().optional(),
  })
  .passthrough();
export const ErrorResponse = z
  .object({
    error: z.string(),
    code: z.string().optional(),
    details: z.object({}).partial().passthrough().optional(),
  })
  .passthrough();
export const RegisterAdminRequest = z
  .object({
    username: z
      .string()
      .min(2)
      .max(63)
      .regex(/^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$/),
    password: z.string().min(8),
  })
  .passthrough();
export const ChangePasswordRequest = z
  .object({
    current_password: z.string().min(1),
    new_password: z.string().min(8),
  })
  .passthrough();
export const OnboardingCompleteRequest = z
  .object({
    provider: z
      .object({
        id: z.string(),
        api_key: z.string().min(1),
        model: z.string().optional(),
      })
      .passthrough(),
    admin: z
      .object({
        username: z
          .string()
          .min(2)
          .max(63)
          .regex(/^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$/),
        password: z.string().min(8),
      })
      .passthrough(),
  })
  .passthrough();
export const ProbeProviderRequest = z
  .object({
    id: z.string(),
    api_key: z.string().min(1),
    endpoint: z.string().optional(),
  })
  .passthrough();
export const ProbeProviderResponse = z
  .object({
    success: z.boolean(),
    models: z.array(z.string()).optional(),
    error: z.string().optional(),
  })
  .passthrough();
export const SessionStats: z.ZodType<SessionStats> = z
  .object({
    tokens_in: z.number().int(),
    tokens_out: z.number().int(),
    tokens_total: z.number().int(),
    cost: z.number(),
    tool_calls: z.number().int(),
    message_count: z.number().int(),
  })
  .passthrough();
export const Session: z.ZodType<Session> = z
  .object({
    id: z.string(),
    type: z.enum(["chat", "task", "channel"]).optional(),
    agent_id: z.string(),
    title: z.string(),
    status: z.enum(["active", "archived", "interrupted"]),
    created_at: z.string().datetime({ offset: true }),
    updated_at: z.string().datetime({ offset: true }),
    model: z.string().optional(),
    provider: z.string().optional(),
    stats: SessionStats,
    project_id: z.string().optional(),
    task_id: z.string().optional(),
    channel: z.string(),
    partitions: z.array(z.string()),
    last_compaction_summary: z.string().optional(),
    agent_ids: z.array(z.string()).optional(),
    active_agent_id: z.string().optional(),
    compaction_summaries: z.record(z.string()).optional(),
  })
  .passthrough();
export const SessionCreateRequest = z
  .object({ agent_id: z.string(), type: z.enum(["chat", "task", "channel"]) })
  .partial()
  .passthrough();
export const Attachment: z.ZodType<Attachment> = z
  .object({
    type: z.string(),
    path: z.string(),
    size: z.number().int(),
    mime_type: z.string(),
  })
  .passthrough();
export const ToolCall: z.ZodType<ToolCall> = z
  .object({
    id: z.string(),
    tool: z.string(),
    status: z.enum([
      "success",
      "error",
      "pending",
      "denied",
      "running",
      "cancelled",
    ]),
    duration_ms: z.number().int().optional(),
    parameters: z.object({}).partial().passthrough().optional(),
    result: z.object({}).partial().passthrough().optional(),
    parent_tool_call_id: z.string().optional(),
  })
  .passthrough();
export const Message: z.ZodType<Message> = z
  .object({
    id: z.string(),
    type: z.enum(["message", "compaction", "system"]).optional(),
    role: z.enum(["user", "assistant", "system"]).optional(),
    content: z.string().optional(),
    summary: z.string().optional(),
    timestamp: z.string().datetime({ offset: true }),
    tokens: z.number().int().optional(),
    cost: z.number().optional(),
    status: z.enum(["ok", "error", "interrupted"]).optional(),
    attachments: z.array(Attachment).optional(),
    tool_calls: z.array(ToolCall).optional(),
    agent_id: z.string(),
    messages_compacted: z.number().int().optional(),
  })
  .passthrough();
export const SessionDetail: z.ZodType<SessionDetail> = z
  .object({ session: Session, messages: z.array(Message) })
  .passthrough();
export const SessionRenameRequest = z
  .object({ title: z.string().min(1).max(256) })
  .passthrough();
export const User = z
  .object({
    username: z
      .string()
      .min(2)
      .max(63)
      .regex(/^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$/),
    role: z.enum(["admin", "user"]),
    has_password: z.boolean(),
    has_active_token: z.boolean(),
  })
  .passthrough();
export const UserCreateRequest = z
  .object({
    username: z
      .string()
      .min(2)
      .max(63)
      .regex(/^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$/),
    role: z.enum(["admin", "user"]),
    password: z.string().min(8),
  })
  .passthrough();
export const UserCreateResponse = z
  .object({
    username: z.string(),
    role: z.enum(["admin", "user"]),
    requires_restart: z.boolean().optional(),
    warning: z.string().optional(),
  })
  .passthrough();
export const UserDeleteResponse = z
  .object({
    username: z.string(),
    deleted: z.boolean(),
    requires_restart: z.boolean().optional(),
    warning: z.string().optional(),
  })
  .passthrough();
export const UserRoleChangeRequest = z
  .object({ role: z.enum(["admin", "user"]) })
  .passthrough();
export const UserRoleChangeResponse = z
  .object({
    username: z.string(),
    role: z.enum(["admin", "user"]),
    requires_restart: z.boolean().optional(),
    warning: z.string().optional(),
  })
  .passthrough();
export const UserResetPasswordRequest = z
  .object({ password: z.string().min(8) })
  .passthrough();
export const UserResetPasswordResponse = z
  .object({
    username: z.string(),
    password_reset: z.boolean(),
    requires_restart: z.boolean().optional(),
    warning: z.string().optional(),
  })
  .passthrough();
export const AgentToolsCfg: z.ZodType<AgentToolsCfg> = z
  .object({
    builtin: z
      .object({
        default_policy: z.enum(["allow", "ask", "deny"]),
        policies: z.record(z.enum(["allow", "ask", "deny"])),
      })
      .partial()
      .passthrough(),
    mcp: z
      .object({
        servers: z.array(
          z
            .object({ id: z.string(), tools: z.array(z.string()).optional() })
            .passthrough()
        ),
      })
      .partial()
      .passthrough(),
  })
  .partial()
  .passthrough();
export const Agent: z.ZodType<Agent> = z
  .object({
    id: z.string(),
    name: z.string(),
    type: z.enum(["core", "custom", "system"]),
    locked: z.boolean(),
    color: z.string().optional(),
    icon: z.string().optional(),
    model: z.string().optional(),
    description: z.string().optional(),
    status: z.enum(["active", "idle", "draft", "error"]),
    soul: z.string(),
    heartbeat: z.string(),
    instructions: z.string(),
    warning: z.string().optional(),
    timeout_seconds: z.number().int(),
    max_tool_iterations: z.number().int(),
    steering_mode: z.string(),
    tool_feedback: z.boolean(),
    heartbeat_enabled: z.boolean(),
    heartbeat_interval: z.number().int(),
    tools_cfg: AgentToolsCfg.optional(),
    sandbox_profile: z
      .enum(["workspace", "workspace+net", "host", "off"])
      .optional(),
    shell_policy: z
      .object({
        enable_deny_patterns: z.boolean(),
        custom_deny_patterns: z.array(z.string()),
      })
      .partial()
      .passthrough()
      .optional(),
  })
  .passthrough();
export const AgentCreateRequest: z.ZodType<AgentCreateRequest> = z
  .object({
    name: z.string().min(1),
    description: z.string().optional(),
    model: z.string().optional(),
    color: z.string().optional(),
    icon: z.string().optional(),
    tools_cfg: AgentToolsCfg.optional(),
  })
  .passthrough();
export const AgentUpdateRequest = z
  .object({
    name: z.string().min(1),
    description: z.string(),
    model: z.string(),
    soul: z.string(),
    heartbeat: z.string(),
    instructions: z.string(),
    timeout_seconds: z.number().int(),
    max_tool_iterations: z.number().int(),
    steering_mode: z.string(),
    tool_feedback: z.boolean(),
    heartbeat_enabled: z.boolean(),
    heartbeat_interval: z.number().int(),
    sandbox_profile: z.enum(["workspace", "workspace+net", "host", "off"]),
    shell_policy: z
      .object({
        enable_deny_patterns: z.boolean(),
        custom_deny_patterns: z.array(z.string()),
      })
      .partial()
      .passthrough(),
  })
  .partial()
  .passthrough();
export const SessionScopeResponse = z
  .object({
    dm_scope: z.enum([
      "main",
      "per-peer",
      "per-channel-peer",
      "per-account-channel-peer",
    ]),
  })
  .passthrough();
export const SessionScopeRequest = z
  .object({
    dm_scope: z.enum([
      "main",
      "per-peer",
      "per-channel-peer",
      "per-account-channel-peer",
    ]),
  })
  .passthrough();
export const HealthResponse = z
  .object({ status: z.literal("ok") })
  .passthrough();
export const AboutResponse = z
  .object({
    version: z.string(),
    go_version: z.string(),
    os: z.string(),
    arch: z.string(),
    uptime: z.string(),
    uptime_seconds: z.number().int().gte(0),
    pid: z.number().int(),
    preview_port: z.number().int(),
    preview_listener_enabled: z.boolean(),
    preview_origin: z.string().optional(),
    warmup_timeout_seconds: z.number().int().gte(0),
    frame_ancestors_fallback: z.boolean(),
  })
  .passthrough();
export const ToolRegistryEntry = z
  .object({
    name: z.string(),
    description: z.string(),
    scope: z.enum(["system", "core", "general"]),
    category: z.string(),
    source: z.enum(["builtin", "mcp"]),
  })
  .passthrough();
export const postToolApproval_Body = z
  .object({ action: z.enum(["approve", "deny", "cancel"]) })
  .passthrough();
export const GlobalToolPolicies = z
  .object({
    default_policy: z.enum(["allow", "ask", "deny"]),
    policies: z.record(z.enum(["allow", "ask", "deny"])),
  })
  .passthrough();
export const ExecAllowlist = z
  .object({
    allowed_binaries: z.array(z.string().min(1).max(256)).max(256),
    approval: z.string().optional(),
    restart_required: z.boolean().optional(),
  })
  .passthrough();
export const updateExecAllowlist_Body = z
  .object({ allowed_binaries: z.array(z.string()) })
  .passthrough();
export const ExecProxyStatus = z
  .object({
    enabled: z.boolean(),
    running: z.boolean(),
    address: z.string().optional(),
  })
  .passthrough();
export const SkillTrustResponse = z
  .object({
    level: z.enum(["block_unverified", "warn_unverified", "allow_all"]),
  })
  .passthrough();
export const updateSkillTrust_Body = z
  .object({
    level: z.enum(["block_unverified", "warn_unverified", "allow_all"]),
  })
  .passthrough();
export const PromptGuardResponse = z
  .object({
    level: z.enum(["low", "medium", "high"]),
    requires_restart: z.boolean(),
  })
  .passthrough();
export const updatePromptGuard_Body = z
  .object({ level: z.enum(["low", "medium", "high"]) })
  .passthrough();
export const updateRateLimits_Body = z
  .object({
    daily_cost_cap_usd: z.number().gte(0),
    max_agent_llm_calls_per_hour: z.number().int().gte(0),
    max_agent_tool_calls_per_minute: z.number().int().gte(0),
  })
  .partial()
  .passthrough();
export const SandboxConfig = z
  .object({
    mode: z.enum(["off", "permissive", "enforce"]),
    applied_mode: z.string(),
    allow_network_outbound: z.boolean(),
    allowed_paths: z.array(z.string()),
    ssrf_enabled: z.boolean(),
    ssrf_allow_internal: z.array(z.string()),
    ssrf: z
      .object({ enabled: z.boolean(), allow_internal: z.array(z.string()) })
      .partial()
      .passthrough(),
    default_profile: z.enum([
      "",
      "none",
      "workspace",
      "workspace+net",
      "host",
      "off",
    ]),
    shell_deny_patterns: z.array(z.string()),
    requires_restart: z.boolean(),
    saved: z.boolean(),
  })
  .partial()
  .passthrough();
export const updateSandboxConfig_Body = z
  .object({
    mode: z.enum(["off", "permissive", "enforce"]),
    allow_network_outbound: z.boolean(),
    allowed_paths: z.array(z.string()),
    ssrf_enabled: z.boolean(),
    ssrf_allow_internal: z.array(z.string()),
    ssrf: z
      .object({ allow_internal: z.array(z.string()) })
      .partial()
      .passthrough(),
    default_profile: z.enum([
      "",
      "none",
      "workspace",
      "workspace+net",
      "host",
      "off",
    ]),
    shell_deny_patterns: z.array(z.string()),
  })
  .partial()
  .passthrough();
export const SandboxStatus = z
  .object({
    backend: z.string(),
    available: z.boolean(),
    kernel_level: z.boolean(),
    policy_applied: z.boolean(),
    abi_version: z.number().int().optional(),
    issue_ref: z.string().optional(),
    blocked_syscalls: z.array(z.string()).optional(),
    seccomp_enabled: z.boolean(),
    landlock_features: z.array(z.string()).optional(),
    notes: z.array(z.string()).optional(),
    mode: z.string().optional(),
    disabled_by: z.string().optional(),
    landlock_enforced: z.boolean().optional(),
    seccomp_enforced: z.boolean().optional(),
    audit_only: z.boolean().optional(),
    bind_ports_count: z.number().int().gte(0),
  })
  .passthrough();
export const AuditEntry = z
  .object({
    timestamp: z.string().datetime({ offset: true }),
    event: z.string(),
    decision: z.enum(["allow", "deny", "error"]).optional(),
    agent_id: z.string().optional(),
    session_id: z.string().optional(),
    tool: z.string().optional(),
    command: z.string().optional(),
    parameters: z.object({}).partial().passthrough().optional(),
    policy_rule: z.string().optional(),
    details: z.object({}).partial().passthrough().optional(),
  })
  .passthrough();
export const AuditLogToggle = z.object({ enabled: z.boolean() }).passthrough();
export const RetentionConfig = z
  .object({ session_days: z.number().int().gte(0), disabled: z.boolean() })
  .partial()
  .passthrough();
export const RetentionSweepResult = z
  .object({
    removed: z.number().int().gte(0),
    skipped_reason: z.string().optional(),
  })
  .passthrough();
export const ChannelEntry = z
  .object({
    id: z.enum([
      "webchat",
      "telegram",
      "discord",
      "slack",
      "whatsapp",
      "feishu",
      "dingtalk",
      "wecom",
      "weixin",
      "line",
      "qq",
      "onebot",
      "irc",
      "matrix",
      "maixcam",
    ]),
    name: z.string(),
    transport: z.enum([
      "websocket",
      "webhook",
      "bridge",
      "tcp",
      "http",
      "serial",
    ]),
    enabled: z.boolean(),
    description: z.string(),
  })
  .passthrough();
export const PendingRestartEntry = z
  .object({
    key: z.string(),
    persisted_value: z.unknown(),
    applied_value: z.unknown(),
  })
  .passthrough();
export const setCredential_Body = z
  .object({ key: z.string(), value: z.string() })
  .passthrough();
export const OnboardingCompleteResponse: z.ZodType<OnboardingCompleteResponse> =
  LoginResponse;
export const AgentSession = z
  .object({
    id: z.string(),
    title: z.string(),
    created_at: z.string().datetime({ offset: true }),
    updated_at: z.string().datetime({ offset: true }),
  })
  .passthrough();
export const AgentToolEntry = z
  .object({
    name: z.string(),
    configured_policy: z.enum(["allow", "ask", "deny"]),
    effective_policy: z.enum(["allow", "ask", "deny"]),
    fence_applied: z.boolean(),
    requires_admin_ask: z.boolean(),
  })
  .passthrough();
export const ToolPolicy = z.enum(["allow", "ask", "deny"]);
export const RateLimitConfig = z
  .object({
    enabled: z.boolean(),
    daily_cost_usd: z.number().gte(0),
    daily_cost_cap: z.number().gte(0),
    daily_cost_cap_usd: z.number().gte(0),
    max_agent_llm_calls_per_hour: z.number().int().gte(0),
    max_agent_tool_calls_per_minute: z.number().int().gte(0),
  })
  .partial()
  .passthrough();

const endpoints = makeApi([
  {
    method: "get",
    path: "/about",
    alias: "getAbout",
    description: `Returns version, runtime, uptime, PID, and preview listener fields. Used by the SPA to construct iframe preview URLs (FR-009).
`,
    requestFormat: "json",
    response: AboutResponse,
    errors: [
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/agents",
    alias: "listAgents",
    description: `Returns all agents from config.json (core + custom). Core agents return empty soul/heartbeat/instructions (compiled-in prompts are not exposed). Custom agents return SOUL.md content only (not heartbeat/instructions) for efficient list rendering.
`,
    requestFormat: "json",
    response: z.array(Agent),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/agents",
    alias: "createAgent",
    description: `Creates a new custom agent with a server-assigned UUID. Returns HTTP 201. The agent starts in &quot;draft&quot; status (no SOUL.md written yet). Triggers a config reload after successful persistence.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: AgentCreateRequest,
      },
    ],
    response: Agent,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 422,
        description: `Validation failed — semantically invalid input.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/agents/:id",
    alias: "getAgent",
    description: `Returns the full agent configuration including soul, heartbeat, and instructions. Core (locked) agents return empty soul (compiled-in prompt not exposed).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Agent,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/agents/:id",
    alias: "updateAgent",
    description: `Updates the specified agent. All fields are optional (only provided fields change). Locked core agents reject mutations to name, description, soul, heartbeat, instructions (403). Writing soul/heartbeat/instructions triggers a config reload. Model, timeout, max_tool_iterations, steering_mode, tool_feedback, heartbeat_enabled, heartbeat_interval changes do NOT trigger a reload.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: AgentUpdateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Agent,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Insufficient permissions or CSRF validation failed.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/agents/:id/sessions",
    alias: "listAgentSessions",
    description: `Returns all sessions owned by the specified agent. Returns an empty array when the agent has no session store.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.array(Session),
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/agents/:id/tools",
    alias: "getAgentTools",
    description: `Returns the agent&#x27;s current tool policy configuration (AgentToolsCfg) plus the effective per-tool policy after fence application. Used by the Tools &amp; Permissions panel in the Agent Profile UI.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z
      .object({
        config: AgentToolsCfg,
        tools: z.array(
          z
            .object({
              name: z.string(),
              configured_policy: z.enum(["allow", "ask", "deny"]),
              effective_policy: z.enum(["allow", "ask", "deny"]),
              fence_applied: z.boolean(),
              requires_admin_ask: z.boolean(),
            })
            .passthrough()
        ),
      })
      .passthrough(),
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/agents/:id/tools",
    alias: "updateAgentTools",
    description: `Replaces the agent&#x27;s tools_cfg in config.json. Locked (core/system) agents cannot have their tool policy overwritten via this endpoint (403). Triggers a config reload on success.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: AgentToolsCfg,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z
      .object({
        config: AgentToolsCfg,
        tools: z.array(
          z
            .object({
              name: z.string(),
              configured_policy: z.enum(["allow", "ask", "deny"]),
              effective_policy: z.enum(["allow", "ask", "deny"]),
              fence_applied: z.boolean(),
              requires_admin_ask: z.boolean(),
            })
            .passthrough()
        ),
      })
      .passthrough(),
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Insufficient permissions or CSRF validation failed.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/audit-log",
    alias: "getAuditLog",
    description: `Returns the last 100 audit log entries in reverse-chronological order, read from ~/.omnipus/system/audit.jsonl. Always returns an array — empty array when no entries exist.
`,
    requestFormat: "json",
    response: z.array(AuditEntry),
    errors: [
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/auth/change-password",
    alias: "changePassword",
    description: `Self-service password change. Requires the current password for verification. Different from PUT /users/{username}/password which is the admin-reset path. Requires authentication.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: ChangePasswordRequest,
      },
    ],
    response: z.object({ success: z.boolean() }).passthrough(),
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/auth/login",
    alias: "login",
    description: `Validates credentials against the bcrypt hashes in config.json. On success, issues a bearer token, an HttpOnly session cookie (omnipus-session), and a __Host-csrf cookie. CSRF-exempt (cookie cannot pre-exist before login). Rate-limited: 5 failures per IP+username per 15 minutes → 429.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: LoginRequest,
      },
    ],
    response: LoginResponse,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 429,
        description: `Rate limit exceeded.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/auth/logout",
    alias: "logout",
    description: `Clears the user&#x27;s token_hash and session_token_hash in config.json, then revokes the omnipus-session and __Host-csrf browser cookies. Requires authentication.
`,
    requestFormat: "json",
    response: z.void(),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/auth/register-admin",
    alias: "registerAdmin",
    description: `Creates the first admin user. Returns 409 if any admin already exists. The check-create sequence is atomic (TOCTOU-safe via safeUpdateConfigJSON). Issues bearer token, session cookie, and CSRF cookie on success. CSRF-exempt. Rate-limited: 3 requests per IP per minute.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: RegisterAdminRequest,
      },
    ],
    response: LoginResponse,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 409,
        description: `Conflict — e.g. resource already exists, or last-admin guard triggered.`,
        schema: ErrorResponse,
      },
      {
        status: 429,
        description: `Rate limit exceeded.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/auth/validate",
    alias: "validateToken",
    description: `Returns the authenticated user&#x27;s username and role when the token is valid. Rate-limited: 30 requests per IP per minute.
`,
    requestFormat: "json",
    response: z
      .object({ username: z.string(), role: z.enum(["admin", "user"]) })
      .passthrough(),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 429,
        description: `Rate limit exceeded.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/backup",
    alias: "createBackup",
    description: `Creates a tar.gz of ~/.omnipus/ excluding logs and backups directories. The archive is written atomically to ~/.omnipus/backups/.
`,
    requestFormat: "json",
    response: z
      .object({
        path: z.string(),
        size_bytes: z.number().int().gte(0),
        created_at: z.string().datetime({ offset: true }),
      })
      .passthrough(),
    errors: [
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/backups",
    alias: "listBackups",
    description: `Lists all .tar.gz files in ~/.omnipus/backups/ with filename, size, and creation time. Returns an empty array when no backups exist.
`,
    requestFormat: "json",
    response: z.array(
      z
        .object({
          filename: z.string(),
          size_bytes: z.number().int().gte(0),
          created_at: z.string().datetime({ offset: true }),
        })
        .passthrough()
    ),
    errors: [
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/channels",
    alias: "listChannels",
    description: `Returns the full list of communication channels with their ID, name, transport, enabled state, and description. webchat is always enabled.
`,
    requestFormat: "json",
    response: z.array(ChannelEntry),
    errors: [
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/channels/:id",
    alias: "getChannelConfig",
    description: `Returns the channel&#x27;s config with credential fields redacted (replaced with &quot;[configured]&quot; if set).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.object({}).partial().passthrough(),
    errors: [
      {
        status: 404,
        description: `Channel ID not found.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/channels/:id/configure",
    alias: "configureChannel",
    description: `Merges request body fields into the channel&#x27;s config section. Fields absent from the request body are not touched (merge, not replace). The &quot;enabled&quot; field is reserved and ignored if sent. Returns the updated config with credential fields redacted.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        description: `Channel-specific configuration fields to merge. Structure varies by channel type.
`,
        type: "Body",
        schema: z.object({}).partial().passthrough(),
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.object({}).partial().passthrough(),
    errors: [
      {
        status: 400,
        description: `Invalid JSON body.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Channel ID not found.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/channels/:id/disable",
    alias: "disableChannel",
    description: `Sets the channel&#x27;s enabled flag to false in config.`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.object({ id: z.string(), enabled: z.boolean() }).passthrough(),
    errors: [
      {
        status: 404,
        description: `Channel ID not found.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/channels/:id/enable",
    alias: "enableChannel",
    description: `Sets the channel&#x27;s enabled flag to true in config.`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.object({ id: z.string(), enabled: z.boolean() }).passthrough(),
    errors: [
      {
        status: 404,
        description: `Channel ID not found.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/channels/:id/test",
    alias: "testChannel",
    description: `Verifies required credential fields are configured without starting the channel. Returns success&#x3D;false with missing field list if required credentials are absent.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z
      .object({ success: z.boolean(), message: z.string() })
      .passthrough(),
    errors: [
      {
        status: 404,
        description: `Channel ID not found.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/config/pending-restart",
    alias: "getPendingRestart",
    description: `Returns an array of config keys whose persisted (disk) value differs from the boot-time applied value. Only RestartGatedKeys are checked; hot-reload keys never appear here. An empty array means no restart is needed. Admin-only; non-admin returns 403.
`,
    requestFormat: "json",
    response: z.array(PendingRestartEntry),
    errors: [
      {
        status: 403,
        description: `Admin role required.`,
        schema: ErrorResponse,
      },
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/credentials",
    alias: "listCredentials",
    description: `Lists all credential key names without values. Returns an empty array when no credentials are stored.
`,
    requestFormat: "json",
    response: z.array(z.string()),
    errors: [
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/credentials",
    alias: "setCredential",
    description: `Stores an encrypted credential. The key must be non-empty. Returns 201 Created on success.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: setCredential_Body,
      },
    ],
    response: z.object({ key: z.string() }).passthrough(),
    errors: [
      {
        status: 400,
        description: `Invalid JSON body.`,
        schema: ErrorResponse,
      },
      {
        status: 422,
        description: `Key field is required (empty key).`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Credential store locked.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "delete",
    path: "/credentials/:key",
    alias: "deleteCredential",
    description: `Removes a credential by key. Returns 404 if not found.`,
    requestFormat: "json",
    parameters: [
      {
        name: "key",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z
      .object({ status: z.literal("removed"), key: z.string() })
      .passthrough(),
    errors: [
      {
        status: 404,
        description: `Credential key not found.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Credential store locked.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/health",
    alias: "getHealth",
    description: `Returns HTTP 200 when the gateway is running. No authentication required.
`,
    requestFormat: "json",
    response: HealthResponse,
    errors: [
      {
        status: 404,
        description: `Not used — gateway is always healthy or not reachable.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/onboarding/complete",
    alias: "completeOnboarding",
    description: `Two-phase commit: writes the LLM provider config and admin user to config.json atomically, then marks onboarding complete in state.json. Returns 409 when onboarding is already complete. CSRF-exempt (no cookie exists yet). Rate-limited: 3 requests per IP per minute. On success, issues a __Host-csrf cookie so the SPA can immediately make CSRF-protected requests.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: OnboardingCompleteRequest,
      },
    ],
    response: LoginResponse,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 409,
        description: `Conflict — e.g. resource already exists, or last-admin guard triggered.`,
        schema: ErrorResponse,
      },
      {
        status: 429,
        description: `Rate limit exceeded.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Service unavailable — e.g. credential store locked.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/onboarding/probe-provider",
    alias: "probeProvider",
    description: `Non-persistent probe: accepts an API key in the request body, tests it against the provider&#x27;s /models endpoint, and returns the model list. Nothing is written to disk. Available only during onboarding (returns 409 after onboarding completes). CSRF-exempt.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: ProbeProviderRequest,
      },
    ],
    response: ProbeProviderResponse,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 409,
        description: `Conflict — e.g. resource already exists, or last-admin guard triggered.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/preview/:agent_id/:token/:path",
    alias: "getPreview",
    description: `Serves static files or proxies dev-server requests for the given agent and token. No bearer authentication required — the path token IS the credential (FR-023). Served on the separate preview listener (default port gateway.port + 1). Unknown or expired tokens return 404. Static files: path-traversal guard, MIME detection, buffered/streaming. Dev-server: reverse-proxied to loopback port with CSP injection.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "agent_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "token",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "path",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
    errors: [
      {
        status: 400,
        description: `Malformed URL.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Token does not match the agent.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Token not found or expired.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/restore",
    alias: "restoreBackup",
    description: `Extracts a backup tar.gz over ~/.omnipus/, skipping config.json to preserve current settings. The filename must not contain path separators or traversal sequences.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ filename: z.string() }).passthrough(),
      },
    ],
    response: z
      .object({ status: z.literal("restored"), filename: z.string() })
      .passthrough(),
    errors: [
      {
        status: 400,
        description: `Invalid filename (path traversal, missing .tar.gz).`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Backup file not found.`,
        schema: ErrorResponse,
      },
      {
        status: 422,
        description: `Filename field is required.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/security/audit-log",
    alias: "getAuditLogToggle",
    description: `Returns whether audit logging is currently enabled. Note: this is distinct from GET /api/v1/audit-log which returns entries. This endpoint controls the audit_log config flag.
`,
    requestFormat: "json",
    response: z.object({ enabled: z.boolean() }).passthrough(),
    errors: [
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/security/audit-log",
    alias: "updateAuditLogToggle",
    description: `Persists sandbox.audit_log to config.json. Requires restart — the response includes applied_enabled which reflects the value before this save (currently running state). Changes are audit-logged before disabling.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ enabled: z.boolean() }).passthrough(),
      },
    ],
    response: z
      .object({
        saved: z.boolean(),
        requires_restart: z.boolean(),
        applied_enabled: z.boolean(),
      })
      .passthrough(),
    errors: [
      {
        status: 400,
        description: `Missing or invalid enabled field.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Admin role required.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/security/exec-allowlist",
    alias: "getExecAllowlist",
    description: `Returns the current exec allowlist and approval mode.
`,
    requestFormat: "json",
    response: ExecAllowlist,
    errors: [
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/security/exec-allowlist",
    alias: "updateExecAllowlist",
    description: `Atomically updates the exec binary allowlist. Patterns are trimmed, validated, and deduplicated. Changes are audit-logged (SEC-15). Note: requires_restart&#x3D;true in the response because the in-memory agent loop uses the previous allowlist until the gateway restarts (SEC-12).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: updateExecAllowlist_Body,
      },
    ],
    response: ExecAllowlist,
    errors: [
      {
        status: 400,
        description: `Invalid pattern (empty, too long, or too many entries).`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/security/exec-proxy-status",
    alias: "getExecProxyStatus",
    description: `Returns whether the exec proxy is configured and currently bound. Operators use this to distinguish &quot;disabled by config&quot; from &quot;failed to bind&quot; from &quot;running normally&quot;.
`,
    requestFormat: "json",
    response: ExecProxyStatus,
    errors: [
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/security/prompt-guard",
    alias: "getPromptGuard",
    description: `Returns the current prompt injection detection strictness level. Default is &quot;medium&quot;. Requires restart is always false (hot-reloaded).
`,
    requestFormat: "json",
    response: PromptGuardResponse,
    errors: [
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/security/prompt-guard",
    alias: "updatePromptGuard",
    description: `Persists the new prompt injection level to config and hot-reloads. Changes take effect immediately — requires_restart is false on successful hot-reload.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: updatePromptGuard_Body,
      },
    ],
    response: z
      .object({
        saved: z.boolean(),
        requires_restart: z.boolean(),
        applied_level: z.enum(["low", "medium", "high"]),
        warning: z.string().optional(),
      })
      .passthrough(),
    errors: [
      {
        status: 400,
        description: `Invalid level value.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Admin role required.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/security/rate-limits",
    alias: "getRateLimits",
    description: `Returns the current rate-limit config and the live daily LLM cost. Hot-reloaded — requires_restart is always false on PUT.
`,
    requestFormat: "json",
    response: z
      .object({
        enabled: z.boolean(),
        daily_cost_usd: z.number().gte(0),
        daily_cost_cap: z.number().gte(0),
        max_agent_llm_calls_per_hour: z.number().int().gte(0),
        max_agent_tool_calls_per_minute: z.number().int().gte(0),
      })
      .passthrough(),
    errors: [
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/security/rate-limits",
    alias: "updateRateLimits",
    description: `Partial update — any subset of the three cap fields. Strict type validation rejects JSON strings in numeric fields, floats in integer fields, negative values, NaN/Inf, and overflow. Changes are hot-reloaded (requires_restart: false).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: updateRateLimits_Body,
      },
    ],
    response: z
      .object({
        saved: z.boolean(),
        requires_restart: z.boolean(),
        applied: z
          .object({
            daily_cost_cap_usd: z.number(),
            max_agent_llm_calls_per_hour: z.number().int(),
            max_agent_tool_calls_per_minute: z.number().int(),
          })
          .partial()
          .passthrough()
          .optional(),
        warning: z.string().optional(),
      })
      .passthrough(),
    errors: [
      {
        status: 400,
        description: `Invalid field types or values.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Admin role required.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/security/retention",
    alias: "getRetention",
    description: `Returns session_days and disabled flag from storage.retention.
`,
    requestFormat: "json",
    response: RetentionConfig,
    errors: [
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/security/retention",
    alias: "updateRetention",
    description: `Partial update — any subset of session_days and disabled. session_days must be a non-negative integer (floats and strings rejected). disabled must be a JSON boolean (string &quot;true&quot;/&quot;false&quot; rejected). Empty body is accepted as a no-op. Hot-reloaded (requires_restart: false). Admin-only (non-admin returns 403).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: RetentionConfig,
      },
    ],
    response: z
      .object({
        saved: z.boolean(),
        requires_restart: z.boolean(),
        session_days: z.number().int().gte(0),
        disabled: z.boolean(),
      })
      .passthrough(),
    errors: [
      {
        status: 400,
        description: `Type mismatch (float for session_days, string for disabled).`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Admin role required.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/security/retention/sweep",
    alias: "triggerRetentionSweep",
    description: `Immediately purges session directories older than the configured retention window. Returns 409 if a sweep is already in progress. Returns skipped_reason&#x3D;&quot;disabled&quot; when retention is disabled. Admin-only; emits audit with resource&#x3D;&quot;storage.retention.sweep&quot;.
`,
    requestFormat: "json",
    response: RetentionSweepResult,
    errors: [
      {
        status: 403,
        description: `Admin role required.`,
        schema: ErrorResponse,
      },
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
      {
        status: 409,
        description: `Sweep already in progress.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/security/sandbox-config",
    alias: "getSandboxConfig",
    description: `Returns the full sandbox configuration including mode, network settings, SSRF controls, and agent defaults.
`,
    requestFormat: "json",
    response: SandboxConfig,
    errors: [
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Agent loop not initialized.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/security/sandbox-config",
    alias: "updateSandboxConfig",
    description: `Partial update — any subset of mode, allow_network_outbound, allowed_paths, ssrf_enabled, ssrf_allow_internal, ssrf.allow_internal, default_profile, shell_deny_patterns. At least one field required. mode, allowed_paths, and default_profile are restart-gated (requires_restart&#x3D;true). SSRF and shell_deny_patterns are hot-reloaded. Admin-only; non-admin returns 403. Protected by RequireNotBypass middleware (returns 503 when dev_mode_bypass is active).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: updateSandboxConfig_Body,
      },
    ],
    response: SandboxConfig,
    errors: [
      {
        status: 400,
        description: `Validation error (invalid mode, profile, or path).`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Admin role required.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `dev_mode_bypass is active (RequireNotBypass guard).`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/security/sandbox-status",
    alias: "getSandboxStatus",
    description: `Returns the active sandbox backend, kernel capabilities, enforcement flags, and bind-port-rule count. Lets operators distinguish enforce from permissive from off states.
`,
    requestFormat: "json",
    response: SandboxStatus,
    errors: [
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Agent loop not initialized.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/security/session-scope",
    alias: "getSessionScope",
    description: `Returns the dm_scope configuration value. Controls how incoming direct messages from channels are routed to session threads. Defaults to &quot;per-channel-peer&quot; when not explicitly configured.
`,
    requestFormat: "json",
    response: SessionScopeResponse,
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/security/session-scope",
    alias: "updateSessionScope",
    description: `Persists the new dm_scope to config.json. Session routing is cached at boot so all changes require a gateway restart. The response always includes requires_restart&#x3D;true. Admin-only. Emits a security audit log entry.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: SessionScopeRequest,
      },
    ],
    response: z
      .object({
        saved: z.boolean(),
        requires_restart: z.boolean(),
        applied_dm_scope: z.string(),
        warning: z.string().optional(),
      })
      .passthrough(),
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Insufficient permissions or CSRF validation failed.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/security/skill-trust",
    alias: "getSkillTrust",
    description: `Returns the current skill trust level from sandbox.skill_trust. Default is &quot;warn_unverified&quot; when not set.
`,
    requestFormat: "json",
    response: SkillTrustResponse,
    errors: [
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/security/skill-trust",
    alias: "updateSkillTrust",
    description: `Persists the new skill trust level to config.sandbox.skill_trust. Only the three canonical values are accepted (case-sensitive). Changes are audit-logged.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: updateSkillTrust_Body,
      },
    ],
    response: z
      .object({
        saved: z.boolean(),
        requires_restart: z.boolean(),
        applied_level: z.enum([
          "block_unverified",
          "warn_unverified",
          "allow_all",
        ]),
        warning: z.string().optional(),
      })
      .passthrough(),
    errors: [
      {
        status: 400,
        description: `Invalid trust level value.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Admin role required.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/security/tool-policies",
    alias: "getGlobalToolPolicies",
    description: `Returns the current global tool policy configuration from sandbox.tool_policies and sandbox.default_tool_policy.
`,
    requestFormat: "json",
    response: GlobalToolPolicies,
    errors: [
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/security/tool-policies",
    alias: "updateGlobalToolPolicies",
    description: `Persists new global tool policies to config.json under the sandbox key. Changes are audit-logged (SEC-15). Admin-only; non-admin returns 403.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: GlobalToolPolicies,
      },
    ],
    response: GlobalToolPolicies,
    errors: [
      {
        status: 400,
        description: `Invalid policy values.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Admin role required.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/sessions",
    alias: "listSessions",
    description: `Returns all sessions visible to the authenticated user. Supports optional filtering by agent_id and type. When some agents fail to list their sessions (e.g. filesystem error), the response still returns HTTP 200 but includes a partial_errors array alongside the sessions array.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "agent_id",
        type: "Query",
        schema: z.string().optional(),
      },
      {
        name: "type",
        type: "Query",
        schema: z.enum(["chat", "task", "channel"]).optional(),
      },
    ],
    response: z.union([
      z.array(Session),
      z
        .object({
          sessions: z.array(Session),
          partial_errors: z.array(z.string()),
        })
        .passthrough(),
    ]),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/sessions",
    alias: "createSession",
    description: `Creates a new session for the specified agent. Returns HTTP 201 on success. The agent must exist (400 if not found).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: SessionCreateRequest,
      },
    ],
    response: Session,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/sessions/:id",
    alias: "getSession",
    description: `Returns the session metadata and complete ordered transcript. Used by the SPA to render the chat history.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: SessionDetail,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/sessions/:id",
    alias: "renameSession",
    description: `Updates the session title. Returns the updated session metadata.`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ title: z.string().min(1).max(256) }).passthrough(),
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Session,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "delete",
    path: "/sessions/:id",
    alias: "deleteSession",
    description: `Permanently removes the session directory including transcript and context. Returns {&quot;success&quot;: true} on success.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.object({ success: z.boolean() }).passthrough(),
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/sessions/:id/messages",
    alias: "getSessionMessages",
    description: `Returns the ordered transcript entries for a session without the metadata wrapper. Lighter than GET /sessions/{id} when only the message list is needed.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.array(Message),
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "delete",
    path: "/sessions/all",
    alias: "clearAllSessions",
    description: `Removes all session directories across all agent stores. Returns the count of removed sessions and any per-agent warnings.
`,
    requestFormat: "json",
    response: z
      .object({
        status: z.literal("cleared"),
        count: z.number().int().gte(0),
        warnings: z.array(z.string()).optional(),
      })
      .passthrough(),
    errors: [
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/storage/stats",
    alias: "getStorageStats",
    description: `Returns session count and workspace size. May include partial warnings if some agent stores could not be read.
`,
    requestFormat: "json",
    response: z
      .object({
        session_count: z.number().int().gte(0),
        workspace_size_bytes: z.number().int().gte(0),
        warnings: z.array(z.string()).optional(),
      })
      .passthrough(),
    errors: [
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/tool-approvals/:approval_id",
    alias: "postToolApproval",
    description: `Approve, deny, or cancel a pending tool call approval. For tools with RequiresAdminAsk&#x3D;true, the caller must hold the admin role (FR-015).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: postToolApproval_Body,
      },
      {
        name: "approval_id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z
      .object({
        approval_id: z.string(),
        action: z.enum(["approve", "deny", "cancel"]),
        status: z.literal("ok"),
      })
      .passthrough(),
    errors: [
      {
        status: 400,
        description: `Malformed body or unknown action.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Admin role required (RequiresAdminAsk tool).`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Approval ID not found.`,
        schema: ErrorResponse,
      },
      {
        status: 410,
        description: `Approval already resolved (FR-018).`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/tools",
    alias: "getToolRegistry",
    description: `Returns all registered tools with name, description, scope, category, and source discriminator (builtin | mcp). Always returns an array — never null even when empty.
`,
    requestFormat: "json",
    response: z.array(ToolRegistryEntry),
    errors: [
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/tools/builtin",
    alias: "getBuiltinToolsDeprecated",
    description: `This endpoint was removed in the central tool registry redesign. Always returns HTTP 404. Use GET /api/v1/tools instead.
`,
    requestFormat: "json",
    response: ErrorResponse,
    errors: [
      {
        status: 404,
        description: `Endpoint removed — use GET /api/v1/tools instead.
`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/users",
    alias: "listUsers",
    description: `Returns all registered gateway users. Admin-only. Password and token hashes are never included — only boolean presence flags. dev_mode_bypass&#x3D;true disables this endpoint (503).
`,
    requestFormat: "json",
    response: z.array(User),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Insufficient permissions or CSRF validation failed.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Service unavailable — e.g. credential store locked.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/users",
    alias: "createUser",
    description: `Creates a new user with hashed password. No bearer token is issued at creation time — the user must log in via POST /auth/login to obtain one. Admin-only. dev_mode_bypass&#x3D;true disables this endpoint (503).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: UserCreateRequest,
      },
    ],
    response: UserCreateResponse,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Insufficient permissions or CSRF validation failed.`,
        schema: ErrorResponse,
      },
      {
        status: 409,
        description: `Conflict — e.g. resource already exists, or last-admin guard triggered.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Service unavailable — e.g. credential store locked.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "delete",
    path: "/users/:username",
    alias: "deleteUser",
    description: `Permanently removes the user from config.json. The last-admin guard prevents deleting the final admin account (409). Admin-only. dev_mode_bypass&#x3D;true disables this endpoint (503).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "username",
        type: "Path",
        schema: z.string().regex(/^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$/),
      },
    ],
    response: UserDeleteResponse,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Insufficient permissions or CSRF validation failed.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 409,
        description: `Conflict — e.g. resource already exists, or last-admin guard triggered.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Service unavailable — e.g. credential store locked.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/users/:username/password",
    alias: "resetUserPassword",
    description: `Resets the target user&#x27;s password and invalidates their current bearer token. The user must log in again with the new password. This is the admin-reset path — the self-change path is POST /auth/change-password. Admin-only. dev_mode_bypass&#x3D;true disables this endpoint (503).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ password: z.string().min(8) }).passthrough(),
      },
      {
        name: "username",
        type: "Path",
        schema: z.string().regex(/^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$/),
      },
    ],
    response: UserResetPasswordResponse,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Insufficient permissions or CSRF validation failed.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Service unavailable — e.g. credential store locked.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "patch",
    path: "/users/:username/role",
    alias: "changeUserRole",
    description: `Changes the role of the target user. The last-admin guard prevents demoting the final admin (409). Admin-only. dev_mode_bypass&#x3D;true disables this endpoint (503).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: UserRoleChangeRequest,
      },
      {
        name: "username",
        type: "Path",
        schema: z.string().regex(/^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$/),
      },
    ],
    response: UserRoleChangeResponse,
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Insufficient permissions or CSRF validation failed.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 409,
        description: `Conflict — e.g. resource already exists, or last-admin guard triggered.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Service unavailable — e.g. credential store locked.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/workspace/:agent_id/:path",
    alias: "getWorkspaceFile",
    description: `Serves the file at the given path within the agent&#x27;s workspace. Requires session-cookie-or-bearer authentication and ownership check. Returns 503 when the agent has no owner (ErrAgentOrphan). Path traversal beyond the agent workspace returns 403. Security headers: Referrer-Policy, Content-Security-Policy, X-Content-Type-Options. Files &gt; 1MB are streamed; smaller files are buffered.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "agent_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "path",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
    errors: [
      {
        status: 400,
        description: `Invalid agent ID or missing file path.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Not authenticated.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Path outside agent workspace or access denied.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Agent or file not found.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Agent has no owner and must be reassigned.`,
        schema: ErrorResponse,
      },
    ],
  },
]);

export const api = new Zodios(endpoints);

export function createApiClient(baseUrl: string, options?: ZodiosOptions) {
  return new Zodios(baseUrl, endpoints, options);
}

// ── AsyncAPI WebSocket frame schemas ─────────────────────────────────────────
// Generated from contracts/asyncapi.yaml components.schemas.
// These extend the REST schemas above with all WS frame types.

export const WsFrameType = z.enum([
  "auth",
  "message",
  "cancel",
  "exec_approval_response",
  "ping",
  "attach_session",
  "device_pairing_response",
  "session_started",
  "token",
  "done",
  "error",
  "tool_call_start",
  "tool_call_result",
  "subagent_start",
  "subagent_end",
  "exec_approval_request",
  "task_status_changed",
  "replay_message",
  "rate_limit",
  "media",
  "agent_switched",
  "tool_approval_required",
  "session_state",
  "system_overload",
  "replay_warning",
  "cancel_stage",
  "session_close_ack",
  "exec_approval_response_ack",
  "device_pairing_request",
]);

export const AuthFrame = z
  .object({
    type: z.literal("auth"),
    token: z.string().min(1),
  })
  .strict();

export const MessageFrame = z
  .object({
    type: z.literal("message"),
    content: z.string().min(1).max(5242880),
    session_id: z.string().optional(),
    agent_id: z.string().optional(),
  })
  .strict();

export const CancelFrame = z
  .object({
    type: z.literal("cancel"),
    session_id: z.string().min(1),
  })
  .strict();

export const ExecApprovalResponseFrame = z
  .object({
    type: z.literal("exec_approval_response"),
    id: z.string().min(1),
    decision: z.enum(["allow", "deny", "always"]),
  })
  .strict();

export const PingFrame = z
  .object({
    type: z.literal("ping"),
  })
  .strict();

export const AttachSessionFrame = z
  .object({
    type: z.literal("attach_session"),
    session_id: z.string().min(1),
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
    session_id: z.string().min(1),
    content: z.string(),
  })
  .strict();

export const DoneStats = z
  .object({
    tokens: z.number().optional(),
    cost: z.number().optional(),
    duration_ms: z.number().optional(),
    tokens_dropped: z.number().optional(),
    frames_emitted: z.number().optional(),
    orphan_count: z.number().optional(),
    duplicate_tool_call_id_count: z.number().optional(),
    truncated_result_count: z.number().optional(),
    replay_error: z.boolean().optional(),
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
    session_id: z.string().optional(),
    message: z.string().min(1),
  })
  .strict();

export const ToolCallStartFrame = z
  .object({
    type: z.literal("tool_call_start"),
    session_id: z.string().min(1),
    tool: z.string().min(1),
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

export const ToolCallResultFrame = z
  .object({
    type: z.literal("tool_call_result"),
    session_id: z.string().min(1),
    tool: z.string().min(1),
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
    task_label: z.string(),
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
    reason: z
      .enum(["parent_timeout", "parent_cancelled", "parent_done_early", "unknown"])
      .optional(),
    agent_id: z.string().optional(),
    parent_call_id: z.string().optional(),
    message: z.string().optional(),
  })
  .strict();

export const ExecApprovalRequestFrame = z
  .object({
    type: z.literal("exec_approval_request"),
    session_id: z.string().min(1),
    id: z.string().min(1),
    command: z.string().min(1),
    working_dir: z.string().optional(),
    matched_policy: z.string().optional(),
  })
  .strict();

export const TaskStatusChangedFrame = z
  .object({
    type: z.literal("task_status_changed"),
    session_id: z.string().min(1),
    task_id: z.string().min(1),
    status: z.string().min(1),
    agent_id: z.string().optional(),
  })
  .strict();

export const ReplayMessageFrame = z
  .object({
    type: z.literal("replay_message"),
    session_id: z.string().min(1),
    content: z.string(),
    role: z.string(),
    id: z.string().optional(),
    timestamp: z.string().optional(),
    agent_id: z.string().optional(),
  })
  .strict();

export const RateLimitFrame = z
  .object({
    type: z.literal("rate_limit"),
    session_id: z.string(),
    scope: z.enum(["agent", "channel", "global"]),
    resource: z.string().min(1),
    policy_rule: z.string().min(1),
    retry_after_seconds: z.number(),
    agent_id: z.string().optional(),
    tool: z.string().optional(),
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
    parts: z.array(MediaPart).min(1),
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
    tool_name: z.string().min(1),
    args: z.record(z.unknown()),
    agent_id: z.string().min(1),
    session_id: z.string().min(1),
    turn_id: z.string().min(1),
    expires_in_ms: z.number().int(),
  })
  .strict();

export const SessionStatePendingApproval = z
  .object({
    approval_id: z.string().min(1),
    session_id: z.string().min(1),
    tool_name: z.string().min(1),
    agent_id: z.string().min(1),
    expires_in_ms: z.number().int(),
  })
  .strict();

export const SessionStateFrame = z
  .object({
    type: z.literal("session_state"),
    user_id: z.string(),
    pending_approvals: z.array(SessionStatePendingApproval),
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

export const ExecApprovalResponseAckFrame = z
  .object({
    type: z.literal("exec_approval_response_ack"),
    id: z.string().optional(),
    session_id: z.string().optional(),
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

// ── WS frame discriminated union ─────────────────────────────────────────────

export const WsFrame = z.discriminatedUnion("type", [
  AuthFrame,
  MessageFrame,
  CancelFrame,
  ExecApprovalResponseFrame,
  PingFrame,
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
  ExecApprovalRequestFrame,
  TaskStatusChangedFrame,
  ReplayMessageFrame,
  RateLimitFrame,
  MediaFrame,
  AgentSwitchedFrame,
  ToolApprovalRequiredFrame,
  SessionStateFrame,
  SystemOverloadFrame,
  ReplayWarningFrame,
  CancelStageFrame,
  SessionCloseAckFrame,
  ExecApprovalResponseAckFrame,
  DevicePairingRequestFrame,
]);

export type WsFrameType = z.infer<typeof WsFrameType>;
export type WsFrame = z.infer<typeof WsFrame>;
