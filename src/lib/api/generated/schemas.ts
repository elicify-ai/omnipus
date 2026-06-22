import { makeApi, Zodios, type ZodiosOptions } from "@zodios/core";
import { z } from "zod";

type LoginResponse = {
  token: BearerToken;
  role: "admin" | "user";
  username: string;
  warning?: string | undefined;
};
type BearerToken = string;
type OnboardingCompleteResponse = LoginResponse;
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
  workspace_id?: string | undefined;
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
  agent_removed?: boolean | undefined;
};
type Message = {
  id: string;
  type?:
    | ("message" | "compaction" | "system" | "tool_call" | "turn_canceled")
    | undefined;
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
  truncated?: boolean | undefined;
  turn_id?: string | undefined;
  canceled_by_user?: string | undefined;
  canceled_by_channel?: string | undefined;
  cancel_method?: ("graceful" | "hard") | undefined;
  descendants_canceled?: Array<string> | undefined;
  model?: string | undefined;
};
type Attachment = {
  type: "image" | "audio" | "video" | "file";
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
  type: "core" | "system" | "Main" | "Subagent" | "subagent_3p";
  locked: boolean;
  color?: string | undefined;
  icon?: string | undefined;
  model?: string | undefined;
  provider?: string | undefined;
  description?: string | undefined;
  status: "active" | "idle" | "draft" | "error";
  soul: string;
  heartbeat: string;
  instructions: string;
  warning?: string | undefined;
  timeout_seconds: number;
  max_tool_iterations: number;
  steering_mode: "one-at-a-time" | "queue-and-process";
  heartbeat_enabled: boolean;
  heartbeat_interval: number;
  tools_cfg?: AgentToolsCfg | undefined;
  sandbox_profile?: ("workspace" | "workspace+net" | "host") | undefined;
  shell_policy?: AgentShellPolicy | undefined;
  fallback_models?: Array<FallbackModel> | undefined;
  model_params?: AgentModelParams | undefined;
  rate_limits?: AgentRateLimits | undefined;
  stats?: AgentStats | undefined;
  default?: boolean | undefined;
  skills?: Array<string> | undefined;
  updated_at?: string | undefined;
  delegation_policy?:
    | Partial<{
        to: Array<{
          kind: "local" | "remote-a2a";
          id: string;
        }>;
        accept_from: Array<{
          kind: "local" | "remote-a2a";
          id: string;
        }>;
        modes: Array<"await" | "background" | "task">;
        depth: number;
        budget: Partial<{
          max_cost_usd: number;
          max_tokens: number;
        }>;
      }>
    | undefined;
  voice?: (string | null) | undefined;
  executor?: ExecutorConfig | undefined;
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
type AgentShellPolicy = Partial<{
  enable_deny_patterns: boolean;
  custom_deny_patterns: Array<string>;
}>;
type FallbackModel = {
  model: string;
  provider?: string | undefined;
};
type AgentModelParams = Partial<{
  temperature: number;
  max_tokens: number;
  top_p: number;
}>;
type AgentRateLimits = Partial<{
  use_global_defaults: boolean;
  max_llm_calls_per_hour: number;
  max_tool_calls_per_minute: number;
  max_cost_per_day: number;
}>;
type AgentStats = {
  total_sessions: number;
  total_tokens: number;
  total_cost: number;
  last_active?: string | undefined;
};
type ExecutorConfig = Partial<{
  kind: "native" | "external-cli" | "remote-a2a";
  cli: "claude-code" | "codex" | "opencode";
  cli_path: string;
  env_overrides: {};
  cli_args: string;
}>;
type AgentCreateRequest = {
  name: string;
  type?: ("Main" | "Subagent" | "subagent_3p") | undefined;
  description?: string | undefined;
  model?: string | undefined;
  provider?: string | undefined;
  color?: string | undefined;
  icon?: string | undefined;
  tools_cfg?: AgentToolsCfg | undefined;
  fallback_models?: Array<FallbackModel> | undefined;
  model_params?:
    | Partial<{
        temperature: number;
        max_tokens: number;
        top_p: number;
      }>
    | undefined;
  rate_limits?:
    | Partial<{
        use_global_defaults: boolean;
        max_llm_calls_per_hour: number;
        max_tool_calls_per_minute: number;
        max_cost_per_day: number;
      }>
    | undefined;
  skills?: Array<string> | undefined;
  soul: string;
  heartbeat?: string | undefined;
  heartbeat_enabled?: boolean | undefined;
  heartbeat_interval?: number | undefined;
  instructions?: string | undefined;
  delegation_policy?: delegation_policy | undefined;
  voice?: (string | null) | undefined;
  executor?: ExecutorConfig | undefined;
  sandbox_profile?: ("workspace" | "workspace+net" | "host") | undefined;
  shell_policy?: AgentShellPolicy | undefined;
  timeout_seconds?: number | undefined;
  max_tool_iterations?: number | undefined;
  steering_mode?: ("one-at-a-time" | "queue-and-process") | undefined;
};
type delegation_policy = Partial<{
  to: Array<{
    kind: "local" | "remote-a2a";
    id: string;
  }>;
  accept_from: Array<{
    kind: "local" | "remote-a2a";
    id: string;
  }>;
  modes: Array<"await" | "background" | "task">;
  depth: number;
  budget: Partial<{
    max_cost_usd: number;
    max_tokens: number;
  }>;
}>;
type AgentUpdateRequest = Partial<{
  updated_at: string;
  name: string;
  description: string;
  model: string;
  provider: string;
  soul: string;
  heartbeat: string;
  instructions: string;
  timeout_seconds: number;
  max_tool_iterations: number;
  steering_mode: "one-at-a-time" | "queue-and-process";
  heartbeat_enabled: boolean;
  heartbeat_interval: number;
  sandbox_profile: "workspace" | "workspace+net" | "host";
  shell_policy: Partial<{
    enable_deny_patterns: boolean;
    custom_deny_patterns: Array<string>;
  }>;
  color: string;
  icon: string;
  fallback_models: Array<FallbackModel>;
  model_params: Partial<{
    temperature: number;
    max_tokens: number;
    top_p: number;
  }>;
  rate_limits: Partial<{
    use_global_defaults: boolean;
    max_llm_calls_per_hour: number;
    max_tool_calls_per_minute: number;
    max_cost_per_day: number;
  }>;
  tools_cfg: AgentToolsCfg;
  default: boolean;
  skills: Array<string>;
  delegation_policy: delegation_policy;
  voice: string | null;
  executor: ExecutorConfig;
}>;
type ChannelEntry = {
  id: ChannelId;
  instance_id?: string | undefined;
  name: string;
  transport:
    | "websocket"
    | "webhook"
    | "bridge"
    | "native"
    | "tcp"
    | "http"
    | "serial"
    | "email";
  enabled: boolean;
  description: string;
  identity?: ChannelIdentity | undefined;
  native_available?: boolean | undefined;
  degraded?: boolean | undefined;
  degraded_reason?: string | undefined;
};
type ChannelId =
  | "webchat"
  | "telegram"
  | "discord"
  | "slack"
  | "whatsapp"
  | "feishu"
  | "dingtalk"
  | "wecom"
  | "weixin"
  | "line"
  | "qq"
  | "irc"
  | "matrix"
  | "google-chat"
  | "email";
type ChannelIdentity = {
  kind: "agent" | "user";
  id?: string | undefined;
};
type IntegrationProvidersResponse = {
  search: Array<IntegrationProvider>;
  voice: Array<IntegrationProvider>;
  active_search?: string | undefined;
  active_voice?: string | undefined;
};
type IntegrationProvider = {
  id: string;
  kind: "search" | "voice";
  display_name: string;
  configured: boolean;
  requires_key: boolean;
  active?: boolean | undefined;
};
type Task = {
  id: string;
  title: string;
  description?: string | undefined;
  prompt?: string | undefined;
  action: "llm";
  status:
    | "inbox"
    | "next"
    | "planning"
    | "in_progress"
    | "blocked"
    | "done"
    | "failed";
  agent_id?: string | undefined;
  agent_name?: string | undefined;
  priority?: number | undefined;
  blocked_by?: Array<string> | undefined;
  todos?: Array<Todo> | undefined;
  parent_task_id?: string | undefined;
  workspace_id: string;
  milestone_id?: string | undefined;
  trigger?: TaskTrigger | undefined;
  due?: string | undefined;
  surface?: ("user" | "heartbeat") | undefined;
  source_channel?: string | undefined;
  source_chat_id?: string | undefined;
  session_id?: string | undefined;
  result?: string | undefined;
  artifacts?: Array<string> | undefined;
  owner: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  started_at?: string | undefined;
  completed_at?: string | undefined;
  rollup?:
    | Array<{
        agent_id: string;
        label: string;
        status:
          | "inbox"
          | "next"
          | "planning"
          | "in_progress"
          | "blocked"
          | "done"
          | "failed";
      }>
    | undefined;
};
type Todo = {
  text: string;
  done: boolean;
};
type TaskTrigger = {
  type: "manual" | "once" | "every" | "recurring";
  config: Partial<
    {
      at_ms: number;
      every_ms: number;
      cron_expr: string;
    } & {
      [key: string]: any;
    }
  >;
};
type DoctorResult = {
  score: number;
  issues: Array<DoctorIssue>;
  checked_at: string;
};
type DoctorIssue = {
  id: string;
  severity: "high" | "medium" | "low";
  title: string;
  description: string;
  recommendation: string;
  action_link?: string | undefined;
  action_label?: string | undefined;
};
type DevicesResponse = {
  pending: Array<DevicePending>;
  paired: Array<DevicePaired>;
};
type DevicePending = {
  device_id: string;
  fingerprint: string;
  pairing_code: string;
  device_name: string;
  created_at: string;
  expires_at: string;
};
type DevicePaired = {
  device_id: string;
  fingerprint: string;
  device_name: string;
  paired_at: string;
  last_seen_at: string;
  status: "active" | "revoked";
};
type AgentToolsResponse = {
  config: AgentToolsCfg;
  tools: Array<AgentToolEntry>;
  agent_type?:
    | ("core" | "system" | "Main" | "Subagent" | "subagent_3p")
    | undefined;
};
type AgentToolEntry = {
  name: string;
  configured_policy: "allow" | "ask" | "deny";
  effective_policy: "allow" | "ask" | "deny";
  fence_applied: boolean;
  requires_admin_ask: boolean;
};
type ChannelEnabledResponse = {
  id: ChannelId;
  enabled: boolean;
};
type UploadFilesResponse = {
  files: Array<UploadedFile>;
};
type UploadedFile = {
  name: string;
  path: string;
  size: number;
  content_type: string;
  ref?: string | undefined;
};
type ActivityEventsResponse = {
  events: Array<ActivityEvent>;
  warning?: string | undefined;
};
type ActivityEvent = {
  id: string;
  type: "session_start" | "task_created" | "task_updated";
  agent_id?: string | undefined;
  agent_name?: string | undefined;
  timestamp: string;
  summary?: string | undefined;
};
type RotateTokenResponse = {
  token: BearerToken;
};
type TaskCreateRequest = {
  title: string;
  prompt?: string | undefined;
  description?: string | undefined;
  action: "llm";
  agent_id?: string | undefined;
  priority?: number | undefined;
  trigger?: TaskTrigger | undefined;
  blocked_by?: Array<string> | undefined;
  todos?: Array<Todo> | undefined;
  parent_task_id?: string | undefined;
  workspace_id: string;
  milestone_id?: string | undefined;
  due?: string | undefined;
  surface?: ("user" | "heartbeat") | undefined;
  source_channel?: string | undefined;
  source_chat_id?: string | undefined;
};
type TaskUpdateRequest = Partial<{
  title: string;
  description: string;
  prompt: string;
  status:
    | "inbox"
    | "next"
    | "planning"
    | "in_progress"
    | "blocked"
    | "done"
    | "failed";
  agent_id: string;
  priority: number;
  blocked_by: Array<string>;
  todos: Array<Todo>;
  trigger: TaskTrigger;
  due: string;
  clear_due: boolean;
  milestone_id: string;
  surface: "user" | "heartbeat";
  result: string;
  artifacts: Array<string>;
  started_at: string;
  completed_at: string;
}>;
type ChannelConfigureRequest = Partial<
  {
    instance_id: string;
    identity: ChannelIdentity;
    token: string;
    bot_token: string;
    app_id: string;
    app_secret: string;
    webhook_secret: string;
    imap_host: string;
    imap_port: number;
    smtp_host: string;
    smtp_port: number;
    username: string;
    password: string;
  } & {
    [key: string]: any;
  }
>;
type Schedule = {
  id: string;
  name: string;
  enabled: boolean;
  owner_agent_id: string;
  created_by?: string | undefined;
  trigger: ScheduleTrigger;
  message: string;
  deliver: boolean;
  session_mode: "isolated" | "continue" | "main";
  timeout_seconds: number;
  session_id?: string | undefined;
  channel?: string | undefined;
  chat_id?: string | undefined;
  state: ScheduleState;
  runs?: Array<ScheduleRunRecord> | undefined;
  created_at_ms: number;
  updated_at_ms: number;
};
type ScheduleTrigger = {
  kind: "at" | "every" | "cron";
  cron_expr?: string | undefined;
  every_ms?: number | undefined;
  at_ms?: number | undefined;
};
type ScheduleState = Partial<{
  next_run_at_ms: number;
  last_run_at_ms: number;
  last_status: string;
  last_error: string;
  consecutive_failures: number;
  running: boolean;
}>;
type ScheduleRunRecord = {
  ran_at_ms: number;
  status: "ok" | "error" | "skipped" | "timeout";
  error?: string | undefined;
  session_id?: string | undefined;
  duration_ms?: number | undefined;
};
type ScheduleCreate = {
  name: string;
  owner_agent_id: string;
  trigger: ScheduleTrigger;
  message: string;
  deliver?: boolean | undefined;
  session_mode?: ("isolated" | "continue" | "main") | undefined;
  timeout_seconds?: number | undefined;
  enabled?: boolean | undefined;
  channel?: string | undefined;
  chat_id?: string | undefined;
};
type ScheduleUpdate = Partial<{
  name: string;
  owner_agent_id: string;
  trigger: ScheduleTrigger;
  message: string;
  deliver: boolean;
  session_mode: "isolated" | "continue" | "main";
  timeout_seconds: number;
  enabled: boolean;
  channel: string;
  chat_id: string;
}>;
type ScheduleList = {
  schedules: Array<Schedule>;
};
type NotificationList = {
  notifications: Array<Notification>;
  unread_count: number;
};
type Notification = {
  id: string;
  type: "schedule_failed";
  title: string;
  body?: string | undefined;
  severity: "info" | "warning" | "error";
  read: boolean;
  created_at_ms: number;
  updated_at_ms?: number | undefined;
  schedule_id?: string | undefined;
  session_id?: string | undefined;
  agent_id?: string | undefined;
};
type WorkspaceDelegation = {
  workspace_id: string;
  edges: Array<WorkspaceDelegationEdge>;
  team?: Array<string> | undefined;
};
type WorkspaceDelegationEdge = {
  from_agent: string;
  to_agent: string;
  modes?: Array<"await" | "background" | "task"> | undefined;
  depth?: number | undefined;
};
type WorkspaceDelegationUpdateRequest = {
  edges: Array<WorkspaceDelegationEdge>;
};
type MilestoneListResponse = {
  milestones: Array<Milestone>;
  total: number;
};
type Milestone = {
  id: string;
  workspace_id: string;
  name: string;
  description?: string | undefined;
  due_date?: (string | null) | undefined;
  created_at: string;
  updated_at: string;
  owner?: string | undefined;
  progress?: number | undefined;
};
type TokenUsageSummary = {
  agents: Array<AgentTokenEntry>;
  period_start: string;
  period_end: string;
};
type AgentTokenEntry = {
  agent_id: string;
  agent_name: string;
  tokens_in: number;
  tokens_out: number;
  tokens_total: number;
};

export const LoginRequest = z.object({
  username: z.string().min(1),
  password: z.string().min(1).max(72),
});
export const BearerToken = z.string();
export const LoginResponse: z.ZodType<LoginResponse> = z.object({
  token: BearerToken.min(72)
    .max(81)
    .regex(/^omnipus_([a-f0-9]{8}_)?[a-f0-9]{64}$/),
  role: z.enum(["admin", "user"]),
  username: z.string(),
  warning: z.string().optional(),
});
export const ErrorResponse = z
  .object({
    error: z.string(),
    code: z.string().optional(),
    details: z.object({}).partial().passthrough().optional(),
  })
  .passthrough();
export const ValidateTokenResponse = z
  .object({ username: z.string(), role: z.enum(["admin", "user"]) })
  .passthrough();
export const RegisterAdminRequest = z.object({
  username: z
    .string()
    .min(2)
    .max(63)
    .regex(/^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$/),
  password: z.string().min(8).max(72),
});
export const ChangePasswordRequest = z.object({
  current_password: z.string().min(1).max(72),
  new_password: z.string().min(8).max(72),
});
export const OperationResult = z.object({
  success: z.boolean(),
  error: z.string().optional(),
});
export const ReAuthRequest = z.object({ password: z.string().min(1).max(72) });
export const ReAuthResponse = z.object({
  verified: z.boolean(),
  token: z.string(),
  expires_in: z.number().int(),
});
export const IntegrationProvider: z.ZodType<IntegrationProvider> = z.object({
  id: z.string(),
  kind: z.enum(["search", "voice"]),
  display_name: z.string(),
  configured: z.boolean(),
  requires_key: z.boolean(),
  active: z.boolean().optional(),
});
export const IntegrationProvidersResponse: z.ZodType<IntegrationProvidersResponse> =
  z.object({
    search: z.array(IntegrationProvider),
    voice: z.array(IntegrationProvider),
    active_search: z.string().optional(),
    active_voice: z.string().optional(),
  });
export const IntegrationProviderUpdateRequest = z.object({
  kind: z.enum(["search", "voice"]),
  api_key: z.string().optional(),
  active: z.boolean().optional(),
});
export const TranscribeResponse = z.object({
  text: z.string(),
  language: z.string().optional(),
  duration: z.number().optional(),
});
export const VoiceProvider = z
  .object({
    provider: z.string().nullable(),
    voices: z.array(z.string()).optional(),
    voices_endpoint: z.string().nullish(),
  })
  .passthrough();
export const OnboardingCompleteRequest = z.object({
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
});
export const ProbeProviderRequest = z.object({
  id: z.enum([
    "anthropic",
    "anthropic-messages",
    "openai",
    "openrouter",
    "gemini",
    "google",
    "ollama",
    "azure",
    "azure-openai",
    "bedrock",
    "litellm",
    "groq",
    "zhipu",
    "nvidia",
    "moonshot",
    "shengsuanyun",
    "deepseek",
    "cerebras",
    "vivgrid",
    "volcengine",
    "vllm",
    "qwen",
    "qwen-intl",
    "qwen-international",
    "dashscope-intl",
    "qwen-us",
    "dashscope-us",
    "mistral",
    "avian",
    "longcat",
    "modelscope",
    "novita",
    "coding-plan",
    "alibaba-coding",
    "qwen-coding",
    "mimo",
    "minimax",
    "coding-plan-anthropic",
    "alibaba-coding-anthropic",
    "antigravity",
    "claude-cli",
    "claudecli",
    "codex-cli",
    "codexcli",
    "github-copilot",
    "copilot",
  ]),
  api_key: z.string().min(1),
  endpoint: z.string().optional(),
});
export const ProbeProviderResponse = z
  .object({
    success: z.boolean(),
    models: z.array(z.string()).optional(),
    error: z.string().optional(),
  })
  .passthrough();
export const SessionStats: z.ZodType<SessionStats> = z
  .object({
    tokens_in: z.number().int().gte(0),
    tokens_out: z.number().int().gte(0),
    tokens_total: z.number().int().gte(0),
    cost: z.number().gte(0),
    tool_calls: z.number().int().gte(0),
    message_count: z.number().int().gte(0),
  })
  .passthrough();
export const Session: z.ZodType<Session> = z.object({
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
  workspace_id: z.string().optional(),
  task_id: z.string().optional(),
  channel: z.string(),
  partitions: z.array(z.string()).max(3650),
  last_compaction_summary: z.string().optional(),
  agent_ids: z.array(z.string()).optional(),
  active_agent_id: z.string().optional(),
  compaction_summaries: z.record(z.string()).optional(),
});
export const SessionCreateRequest = z
  .object({ agent_id: z.string(), type: z.enum(["chat", "task", "channel"]) })
  .partial();
export const Attachment: z.ZodType<Attachment> = z.object({
  type: z.enum(["image", "audio", "video", "file"]),
  path: z.string(),
  size: z.number().int(),
  mime_type: z.string(),
});
export const ToolCall: z.ZodType<ToolCall> = z.object({
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
  duration_ms: z.number().int().gte(0).optional(),
  parameters: z.object({}).partial().passthrough().optional(),
  result: z.object({}).partial().passthrough().optional(),
  parent_tool_call_id: z.string().optional(),
});
export const Message: z.ZodType<Message> = z.object({
  id: z.string(),
  type: z
    .enum(["message", "compaction", "system", "tool_call", "turn_canceled"])
    .optional(),
  role: z.enum(["user", "assistant", "system"]).optional(),
  content: z.string().optional(),
  summary: z.string().optional(),
  timestamp: z.string().datetime({ offset: true }),
  tokens: z.number().int().gte(0).optional(),
  cost: z.number().gte(0).optional(),
  status: z.enum(["ok", "error", "interrupted"]).optional(),
  attachments: z.array(Attachment).optional(),
  tool_calls: z.array(ToolCall).optional(),
  agent_id: z.string(),
  messages_compacted: z.number().int().optional(),
  truncated: z.boolean().optional(),
  turn_id: z.string().optional(),
  canceled_by_user: z.string().optional(),
  canceled_by_channel: z.string().optional(),
  cancel_method: z.enum(["graceful", "hard"]).optional(),
  descendants_canceled: z.array(z.string()).optional(),
  model: z.string().optional(),
});
export const SessionDetail: z.ZodType<SessionDetail> = z.object({
  session: Session,
  messages: z.array(Message).max(100000),
  agent_removed: z.boolean().optional(),
});
export const SessionRenameRequest = z.object({
  title: z.string().min(1).max(256),
});
export const User = z.object({
  username: z
    .string()
    .min(2)
    .max(63)
    .regex(/^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$/),
  role: z.enum(["admin", "user"]),
  has_password: z.boolean(),
  has_active_token: z.boolean(),
});
export const UserCreateRequest = z.object({
  username: z
    .string()
    .min(2)
    .max(63)
    .regex(/^[A-Za-z0-9][A-Za-z0-9._-]{1,62}$/),
  role: z.enum(["admin", "user"]),
  password: z.string().min(8).max(72),
});
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
export const UserRoleChangeRequest = z.object({
  role: z.enum(["admin", "user"]),
});
export const UserRoleChangeResponse = z.object({
  username: z.string(),
  role: z.enum(["admin", "user"]),
  requires_restart: z.boolean().optional(),
  warning: z.string().optional(),
});
export const UserResetPasswordRequest = z.object({
  password: z.string().min(8).max(72),
});
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
export const AgentShellPolicy: z.ZodType<AgentShellPolicy> = z
  .object({
    enable_deny_patterns: z.boolean(),
    custom_deny_patterns: z.array(z.string()),
  })
  .partial()
  .passthrough();
export const FallbackModel: z.ZodType<FallbackModel> = z.object({
  model: z.string().max(256),
  provider: z.string().max(64).optional(),
}).strict();
export const AgentModelParams: z.ZodType<AgentModelParams> = z
  .object({
    temperature: z.number().gte(0).lte(2),
    max_tokens: z.number().int().gte(1),
    top_p: z.number().gte(0).lte(1),
  })
  .partial()
  .passthrough();
export const AgentRateLimits: z.ZodType<AgentRateLimits> = z
  .object({
    use_global_defaults: z.boolean(),
    max_llm_calls_per_hour: z.number().int().gte(0),
    max_tool_calls_per_minute: z.number().int().gte(0),
    max_cost_per_day: z.number().gte(0),
  })
  .partial()
  .passthrough();
export const AgentStats: z.ZodType<AgentStats> = z
  .object({
    total_sessions: z.number().int().gte(0),
    total_tokens: z.number().int().gte(0),
    total_cost: z.number().gte(0),
    last_active: z.string().datetime({ offset: true }).optional(),
  })
  .passthrough();
export const ExecutorConfig: z.ZodType<ExecutorConfig> = z
  .object({
    kind: z.enum(["native", "external-cli", "remote-a2a"]),
    cli: z.enum(["claude-code", "codex", "opencode"]),
    cli_path: z.string(),
    env_overrides: z.record(z.string()),
    cli_args: z.string(),
  })
  .partial();
export const Agent: z.ZodType<Agent> = z
  .object({
    id: z.string(),
    name: z.string().min(1).max(100),
    type: z.enum(["core", "system", "Main", "Subagent", "subagent_3p"]),
    locked: z.boolean(),
    color: z
      .string()
      .regex(/^#[0-9A-Fa-f]{6}$/)
      .optional(),
    icon: z.string().max(50).optional(),
    model: z.string().max(256).optional(),
    provider: z.string().max(64).optional(),
    description: z.string().optional(),
    status: z.enum(["active", "idle", "draft", "error"]),
    soul: z.string(),
    heartbeat: z.string(),
    instructions: z.string(),
    warning: z.string().optional(),
    timeout_seconds: z.number().int().gte(0),
    max_tool_iterations: z.number().int().gte(0),
    steering_mode: z.enum(["one-at-a-time", "queue-and-process"]),
    heartbeat_enabled: z.boolean(),
    heartbeat_interval: z.number().int().gte(0),
    tools_cfg: AgentToolsCfg.optional(),
    sandbox_profile: z.enum(["workspace", "workspace+net", "host"]).optional(),
    shell_policy: AgentShellPolicy.optional(),
    fallback_models: z.array(FallbackModel).max(2).optional(),
    model_params: AgentModelParams.optional(),
    rate_limits: AgentRateLimits.optional(),
    stats: AgentStats.optional(),
    default: z.boolean().optional(),
    skills: z.array(z.string()).optional(),
    updated_at: z.string().datetime({ offset: true }).optional(),
    delegation_policy: z
      .object({
        to: z.array(
          z.object({ kind: z.enum(["local", "remote-a2a"]), id: z.string() })
        ),
        accept_from: z.array(
          z.object({ kind: z.enum(["local", "remote-a2a"]), id: z.string() })
        ),
        modes: z.array(z.enum(["await", "background", "task"])),
        depth: z.number().int().gte(0),
        budget: z
          .object({ max_cost_usd: z.number(), max_tokens: z.number().int() })
          .partial(),
      })
      .partial()
      .optional(),
    voice: z.string().nullish(),
    executor: ExecutorConfig.optional(),
  })
  .passthrough();
export const delegation_policy: z.ZodType<delegation_policy> = z
  .object({
    to: z.array(
      z.object({ kind: z.enum(["local", "remote-a2a"]), id: z.string() })
    ),
    accept_from: z.array(
      z.object({ kind: z.enum(["local", "remote-a2a"]), id: z.string() })
    ),
    modes: z.array(z.enum(["await", "background", "task"])),
    depth: z.number().int().gte(0),
    budget: z
      .object({ max_cost_usd: z.number(), max_tokens: z.number().int() })
      .partial(),
  })
  .partial();
export const AgentCreateRequest: z.ZodType<AgentCreateRequest> = z.object({
  name: z.string().min(1),
  type: z.enum(["Main", "Subagent", "subagent_3p"]).optional().default("Main"),
  description: z.string().optional(),
  model: z.string().optional(),
  provider: z.string().max(64).optional(),
  color: z
    .string()
    .regex(/^#[0-9A-Fa-f]{6}$/)
    .optional(),
  icon: z.string().max(50).optional(),
  tools_cfg: AgentToolsCfg.optional(),
  fallback_models: z.array(FallbackModel).max(2).optional(),
  model_params: z
    .object({
      temperature: z.number(),
      max_tokens: z.number().int(),
      top_p: z.number(),
    })
    .partial()
    .passthrough()
    .optional(),
  rate_limits: z
    .object({
      use_global_defaults: z.boolean(),
      max_llm_calls_per_hour: z.number().int(),
      max_tool_calls_per_minute: z.number().int(),
      max_cost_per_day: z.number(),
    })
    .partial()
    .passthrough()
    .optional(),
  skills: z.array(z.string()).optional(),
  soul: z.string().min(1),
  heartbeat: z.string().optional(),
  heartbeat_enabled: z.boolean().optional(),
  heartbeat_interval: z.number().int().gte(0).optional(),
  instructions: z.string().optional(),
  delegation_policy: delegation_policy.optional(),
  voice: z.string().nullish(),
  executor: ExecutorConfig.optional(),
  sandbox_profile: z.enum(["workspace", "workspace+net", "host"]).optional(),
  shell_policy: AgentShellPolicy.optional(),
  timeout_seconds: z.number().int().gte(0).optional(),
  max_tool_iterations: z.number().int().gte(0).optional(),
  steering_mode: z.enum(["one-at-a-time", "queue-and-process"]).optional(),
});
export const AgentUpdateRequest: z.ZodType<AgentUpdateRequest> = z
  .object({
    updated_at: z.string().datetime({ offset: true }),
    name: z.string().min(1),
    description: z.string(),
    model: z.string(),
    provider: z.string().max(64),
    soul: z.string().min(1),
    heartbeat: z.string(),
    instructions: z.string(),
    timeout_seconds: z.number().int(),
    max_tool_iterations: z.number().int(),
    steering_mode: z.enum(["one-at-a-time", "queue-and-process"]),
    heartbeat_enabled: z.boolean(),
    heartbeat_interval: z.number().int(),
    sandbox_profile: z.enum(["workspace", "workspace+net", "host"]),
    shell_policy: z
      .object({
        enable_deny_patterns: z.boolean(),
        custom_deny_patterns: z.array(z.string()),
      })
      .partial()
      .passthrough(),
    color: z.string().regex(/^#[0-9A-Fa-f]{6}$/),
    icon: z.string().max(50),
    fallback_models: z.array(FallbackModel).max(2),
    model_params: z
      .object({
        temperature: z.number(),
        max_tokens: z.number().int(),
        top_p: z.number(),
      })
      .partial()
      .passthrough(),
    rate_limits: z
      .object({
        use_global_defaults: z.boolean(),
        max_llm_calls_per_hour: z.number().int(),
        max_tool_calls_per_minute: z.number().int(),
        max_cost_per_day: z.number(),
      })
      .partial()
      .passthrough(),
    tools_cfg: AgentToolsCfg,
    default: z.boolean(),
    skills: z.array(z.string()),
    delegation_policy: delegation_policy,
    voice: z.string().nullable(),
    executor: ExecutorConfig,
  })
  .partial();
export const AgentOwnershipUpdateRequest = z
  .object({ owner_username: z.string() })
  .partial();
export const AgentOwnerUpdateResponse = z.object({
  success: z.boolean(),
  agent_id: z.string(),
  owner_username: z.string(),
});
export const AgentToolEntry: z.ZodType<AgentToolEntry> = z
  .object({
    name: z.string(),
    configured_policy: z.enum(["allow", "ask", "deny"]),
    effective_policy: z.enum(["allow", "ask", "deny"]),
    fence_applied: z.boolean(),
    requires_admin_ask: z.boolean(),
  })
  .passthrough();
export const AgentToolsResponse: z.ZodType<AgentToolsResponse> = z.object({
  config: AgentToolsCfg,
  tools: z.array(AgentToolEntry),
  agent_type: z
    .enum(["core", "system", "Main", "Subagent", "subagent_3p"])
    .optional(),
});
export const AgentToolsUpdateRequest = z
  .object({
    builtin: z
      .object({
        default_policy: z.enum(["allow", "ask", "deny"]),
        policies: z.record(z.enum(["allow", "ask", "deny"])),
        mode: z.enum(["explicit", "inherit"]),
        visible: z.array(z.string()),
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
  .partial();
export const RunnerTestResponse = z.object({
  ok: z.boolean(),
  reason: z.enum([
    "",
    "missing-binary",
    "handshake-failed",
    "unauthenticated",
    "unknown-cli",
    "not-external-cli",
  ]),
  message: z.string(),
  cli: z.string().optional(),
  cli_version: z.string().optional(),
});
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
export const SessionScopeRequest = z.object({
  dm_scope: z.enum([
    "main",
    "per-peer",
    "per-channel-peer",
    "per-account-channel-peer",
  ]),
});
export const SessionScopeUpdateResponse = z
  .object({
    saved: z.boolean(),
    requires_restart: z.boolean(),
    applied_dm_scope: z.string(),
    warning: z.string().optional(),
  })
  .passthrough();
export const HealthResponse = z.object({ status: z.literal("ok") });
export const AboutResponse = z.object({
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
});
export const VersionResponse = z.object({
  version: z.string().regex(/^\d+\.\d+\.\d+(?:[-+].*)?$/),
  build_sha: z.string().regex(/^([0-9a-f]{7,40}|dev)$/),
});
export const ToolRegistryEntry = z
  .object({
    name: z.string(),
    description: z.string(),
    scope: z.enum(["system", "core", "general"]),
    category: z.string(),
    source: z.enum(["builtin", "mcp"]),
  })
  .passthrough();
export const ToolApprovalActionRequest = z.object({
  action: z.enum(["approve", "deny", "cancel"]),
});
export const ToolApprovalResponse = z
  .object({
    approval_id: z.string(),
    action: z.enum(["approve", "deny", "cancel"]),
    status: z.literal("ok"),
  })
  .passthrough();
export const GlobalToolPolicies = z.object({
  default_policy: z.enum(["allow", "ask", "deny"]),
  policies: z.record(z.enum(["allow", "ask", "deny"])),
});
export const ExecAllowlist = z.object({
  allowed_binaries: z.array(z.string().min(1).max(256)).max(256),
  approval: z.string().optional(),
  restart_required: z.boolean().optional(),
});
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
export const SkillTrustResponse = z.object({
  level: z.enum(["block_unverified", "warn_unverified", "allow_all"]),
});
export const SkillTrustUpdateRequest = z.object({
  level: z.enum(["block_unverified", "warn_unverified", "allow_all"]),
});
export const SkillTrustUpdateResponse = z.object({
  saved: z.boolean(),
  requires_restart: z.boolean(),
  applied_level: z.enum(["block_unverified", "warn_unverified", "allow_all"]),
  warning: z.string().optional(),
});
export const PromptGuardResponse = z.object({
  level: z.enum(["low", "medium", "high"]),
  requires_restart: z.boolean(),
});
export const PromptGuardUpdateRequest = z.object({
  level: z.enum(["low", "medium", "high"]),
});
export const PromptGuardUpdateResponse = z.object({
  saved: z.boolean(),
  requires_restart: z.boolean(),
  applied_level: z.enum(["low", "medium", "high"]),
  warning: z.string().optional(),
});
export const RateLimitsResponse = z
  .object({
    enabled: z.boolean(),
    daily_cost_usd: z.number().gte(0),
    daily_cost_cap: z.number().gte(0),
    max_agent_llm_calls_per_hour: z.number().int().gte(0),
    max_agent_tool_calls_per_minute: z.number().int().gte(0),
  })
  .passthrough();
export const RateLimitsUpdateRequest = z
  .object({
    daily_cost_cap_usd: z.number().gte(0),
    max_agent_llm_calls_per_hour: z.number().int().gte(0),
    max_agent_tool_calls_per_minute: z.number().int().gte(0),
  })
  .partial();
export const RateLimitsUpdateResponse = z
  .object({
    saved: z.boolean(),
    requires_restart: z.boolean(),
    applied: z
      .object({
        daily_cost_cap_usd: z.number().gte(0),
        max_agent_llm_calls_per_hour: z.number().int().gte(0),
        max_agent_tool_calls_per_minute: z.number().int().gte(0),
      })
      .partial()
      .passthrough()
      .optional(),
    warning: z.string().optional(),
  })
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
    default_profile: z.enum(["", "none", "workspace", "workspace+net", "host"]),
    god_mode: z.boolean(),
    god_mode_available: z.boolean(),
    shell_deny_patterns: z.array(z.string()),
    requires_restart: z.boolean(),
    saved: z.boolean(),
  })
  .partial()
  .passthrough();
export const SandboxConfigUpdate = z
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
    event: z.string().regex(/^[a-z_]+$/),
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
export const AuditLogToggle = z.object({ enabled: z.boolean() });
export const AuditLogToggleRequest = z.object({ enabled: z.boolean() });
export const AuditLogUpdateResponse = z.object({
  saved: z.boolean(),
  requires_restart: z.boolean(),
  applied_enabled: z.boolean(),
});
export const RetentionConfig = z
  .object({ session_days: z.number().int().gte(0), disabled: z.boolean() })
  .partial()
  .passthrough();
export const RetentionUpdateResponse = z
  .object({
    saved: z.boolean(),
    requires_restart: z.boolean(),
    session_days: z.number().int().gte(0),
    disabled: z.boolean(),
  })
  .passthrough();
export const RetentionSweepResult = z
  .object({
    removed: z.number().int().gte(0),
    skipped_reason: z.string().optional(),
  })
  .passthrough();
export const PerformanceSettings = z
  .object({
    max_parallel_agents: z.number().int().gte(2).lte(16),
    effective_max_parallel_agents: z.number().int().gte(2).lte(16),
  })
  .partial();
export const PerformanceSettingsUpdate = z
  .object({ max_parallel_agents: z.number().int().gte(2).lte(16) })
  .partial();
export const ChannelId = z.enum([
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
  "irc",
  "matrix",
  "google-chat",
  "email",
]);
export const ChannelIdentity: z.ZodType<ChannelIdentity> = z.object({
  kind: z.enum(["agent", "user"]),
  id: z.string().optional(),
});
export const ChannelEntry: z.ZodType<ChannelEntry> = z.object({
  id: ChannelId,
  instance_id: z.string().optional(),
  name: z.string(),
  transport: z.enum([
    "websocket",
    "webhook",
    "bridge",
    "native",
    "tcp",
    "http",
    "serial",
    "email",
  ]),
  enabled: z.boolean(),
  description: z.string(),
  identity: ChannelIdentity.optional(),
  native_available: z.boolean().optional(),
  degraded: z.boolean().optional(),
  degraded_reason: z.string().optional(),
});
export const ChannelEnabledResponse: z.ZodType<ChannelEnabledResponse> =
  z.object({ id: ChannelId, enabled: z.boolean() });
export const ChannelTestResponse = z
  .object({ success: z.boolean(), message: z.string() })
  .passthrough();
export const ChannelRouting = z
  .object({ default_agent_id: z.string() })
  .partial();
export const Mailbox = z.object({
  agent_id: z.string(),
  enabled: z.boolean(),
  workspace_id: z.string().optional(),
  imap_host: z.string().optional(),
  imap_port: z.number().int().optional(),
  smtp_host: z.string().optional(),
  smtp_port: z.number().int().optional(),
  username: z.string().optional(),
  configured: z.boolean(),
});
export const MailboxConfigureRequest = z.object({
  enabled: z.boolean(),
  workspace_id: z.string(),
  imap_host: z.string(),
  imap_port: z.number().int().optional(),
  smtp_host: z.string(),
  smtp_port: z.number().int().optional(),
  username: z.string(),
  password: z.string().optional(),
});
export const RotateTokenResponse: z.ZodType<RotateTokenResponse> = z.object({
  token: BearerToken.min(72)
    .max(81)
    .regex(/^omnipus_([a-f0-9]{8}_)?[a-f0-9]{64}$/),
});
export const PendingRestartEntry = z
  .object({
    key: z.string(),
    persisted_value: z.unknown(),
    applied_value: z.unknown(),
  })
  .passthrough();
export const GatewayRestartResponse = z
  .object({
    status: z.literal("restarting"),
    restart_id: z.string().min(1),
    drain_seconds: z.number().int().gte(0),
    message: z.string().optional(),
  })
  .passthrough();
export const GodModeStatus = z.object({
  enabled: z.boolean(),
  available: z.boolean(),
});
export const GodModeUpdateRequest = z.object({ enabled: z.boolean() });
export const CredentialSetRequest = z.object({
  key: z.string(),
  value: z.string(),
});
export const BackupCreateResponse = z.object({
  path: z.string(),
  size_bytes: z.number().int().gte(0),
  created_at: z.string().datetime({ offset: true }),
});
export const RestoreBackupRequest = z.object({ filename: z.string() });
export const StorageStats = z
  .object({
    workspace_size_bytes: z.number().int().gte(0),
    session_count: z.number().int().gte(0),
    memory_entry_count: z.number().int().gte(0),
    oldest_session_date: z.string().datetime({ offset: true }).optional(),
    warnings: z.array(z.string()).optional(),
  })
  .passthrough();
export const GatewayStatus = z.object({
  online: z.boolean(),
  agent_count: z.number().int().gte(0),
  channel_count: z.number().int().gte(0),
  daily_cost: z.number().gte(0),
  version: z.string().optional(),
});
export const Provider = z.object({
  id: z.string(),
  name: z.string(),
  display_name: z.string().optional(),
  status: z.enum(["connected", "disconnected", "error"]),
  models: z.array(z.string()),
  has_models_endpoint: z.boolean().optional(),
  has_api_key: z.boolean().optional(),
  warning: z.string().optional(),
  error: z.string().optional(),
});
export const ProviderUpdateRequest = z
  .object({
    api_key: z.string(),
    model: z.string(),
    models: z.array(z.string().min(1).max(256)).max(500),
  })
  .partial();
export const Skill = z.object({
  id: z.string(),
  name: z.string(),
  version: z.string(),
  description: z.string().optional(),
  author: z.string().optional(),
  source: z.enum(["builtin", "global", "workspace"]).optional(),
  verified: z.boolean(),
  status: z.enum(["active", "disabled", "inactive", "error"]),
  agent_assignment: z.string().optional(),
});
export const SkillSearchResult = z.object({
  slug: z.string(),
  display_name: z.string().optional(),
  summary: z.string().optional(),
  version: z.string().optional(),
  score: z.number().optional(),
  registry_name: z.string().optional(),
  owner_handle: z.string().optional(),
});
export const SkillMarketplaceStatus = z.object({
  enabled: z.boolean(),
  registries: z.array(z.object({ name: z.string(), enabled: z.boolean() })),
});
export const SkillInstallRequest = z.object({
  slug: z
    .string()
    .min(1)
    .max(128)
    .regex(/^[a-z0-9][a-z0-9._-]*$/),
  version: z.string().max(64).optional(),
});
export const SseChatRequest = z.object({ message: z.string() });
export const ActivityEvent: z.ZodType<ActivityEvent> = z
  .object({
    id: z.string(),
    type: z.enum(["session_start", "task_created", "task_updated"]),
    agent_id: z.string().optional(),
    agent_name: z.string().optional(),
    timestamp: z.string().datetime({ offset: true }),
    summary: z.string().optional(),
  })
  .passthrough();
export const uploadFiles_Body = z
  .object({ session_id: z.string(), files: z.array(z.instanceof(File)) })
  .partial()
  .passthrough();
export const UploadedFile: z.ZodType<UploadedFile> = z.object({
  name: z.string(),
  path: z.string(),
  size: z.number().int().gte(0),
  content_type: z.string(),
  ref: z.string().optional(),
});
export const UploadFilesResponse: z.ZodType<UploadFilesResponse> = z
  .object({ files: z.array(UploadedFile) })
  .passthrough();
export const AppState = z.object({
  onboarding_complete: z.boolean(),
  last_doctor_run: z.string().datetime({ offset: true }).optional(),
  last_doctor_score: z.number().int().gte(0).lte(100).optional(),
  god_mode_available: z.boolean().optional(),
  god_mode_opted_in: z.boolean().optional(),
  dev_mode_bypass: z.boolean().optional(),
});
export const AppStatePatchRequest = z
  .object({ onboarding_complete: z.boolean() })
  .partial();
export const UserContextResponse = z.object({ content: z.string() });
export const UserContextRequest = z.object({ content: z.string().max(262144) });
export const MeInfo = z.object({ role: z.enum(["admin", "user"]) });
export const DoctorIssue: z.ZodType<DoctorIssue> = z.object({
  id: z.string(),
  severity: z.enum(["high", "medium", "low"]),
  title: z.string(),
  description: z.string(),
  recommendation: z.string(),
  action_link: z.string().optional(),
  action_label: z.string().optional(),
});
export const DoctorResult: z.ZodType<DoctorResult> = z.object({
  score: z.number().int().gte(0).lte(100),
  issues: z.array(DoctorIssue).max(100),
  checked_at: z.string().datetime({ offset: true }),
});
export const DevicePending: z.ZodType<DevicePending> = z.object({
  device_id: z.string(),
  fingerprint: z.string(),
  pairing_code: z.string(),
  device_name: z.string(),
  created_at: z.string().datetime({ offset: true }),
  expires_at: z.string().datetime({ offset: true }),
});
export const DevicePaired: z.ZodType<DevicePaired> = z.object({
  device_id: z.string(),
  fingerprint: z.string(),
  device_name: z.string(),
  paired_at: z.string().datetime({ offset: true }),
  last_seen_at: z.string().datetime({ offset: true }),
  status: z.enum(["active", "revoked"]),
});
export const DevicesResponse: z.ZodType<DevicesResponse> = z.object({
  pending: z.array(DevicePending).max(100),
  paired: z.array(DevicePaired).max(100),
});
export const Todo: z.ZodType<Todo> = z.object({
  text: z.string().min(1).max(500),
  done: z.boolean(),
});
export const TaskTrigger: z.ZodType<TaskTrigger> = z.object({
  type: z.enum(["manual", "once", "every", "recurring"]),
  config: z
    .object({
      at_ms: z.number().int(),
      every_ms: z.number().int().gte(1000),
      cron_expr: z.string(),
    })
    .partial()
    .passthrough(),
});
export const Task: z.ZodType<Task> = z
  .object({
    id: z.string(),
    title: z.string().min(1).max(200),
    description: z.string().max(2000).optional(),
    prompt: z.string().max(10000).optional(),
    action: z.literal("llm"),
    status: z.enum([
      "inbox",
      "next",
      "planning",
      "in_progress",
      "blocked",
      "done",
      "failed",
    ]),
    agent_id: z.string().optional(),
    agent_name: z.string().optional(),
    priority: z.number().int().gte(1).lte(5).optional().default(3),
    blocked_by: z.array(z.string()).optional(),
    todos: z.array(Todo).optional(),
    parent_task_id: z.string().optional(),
    workspace_id: z.string(),
    milestone_id: z.string().optional(),
    trigger: TaskTrigger.optional(),
    due: z.string().datetime({ offset: true }).optional(),
    surface: z.enum(["user", "heartbeat"]).optional().default("user"),
    source_channel: z.string().optional(),
    source_chat_id: z.string().optional(),
    session_id: z.string().optional(),
    result: z.string().max(50000).optional(),
    artifacts: z.array(z.string()).optional(),
    owner: z.string(),
    created_by: z.string(),
    created_at: z.string().datetime({ offset: true }),
    updated_at: z.string().datetime({ offset: true }),
    started_at: z.string().datetime({ offset: true }).optional(),
    completed_at: z.string().datetime({ offset: true }).optional(),
    rollup: z
      .array(
        z.object({
          agent_id: z.string(),
          label: z.string(),
          status: z.enum([
            "inbox",
            "next",
            "planning",
            "in_progress",
            "blocked",
            "done",
            "failed",
          ]),
        })
      )
      .optional(),
  })
  .passthrough();
export const TaskCreateRequest: z.ZodType<TaskCreateRequest> = z.object({
  title: z.string().min(1).max(200),
  prompt: z.string().max(10000).optional(),
  description: z.string().max(2000).optional(),
  action: z.literal("llm"),
  agent_id: z.string().optional(),
  priority: z.number().int().gte(1).lte(5).optional().default(3),
  trigger: TaskTrigger.optional(),
  blocked_by: z.array(z.string()).optional(),
  todos: z.array(Todo).optional(),
  parent_task_id: z.string().optional(),
  workspace_id: z.string(),
  milestone_id: z.string().optional(),
  due: z.string().datetime({ offset: true }).optional(),
  surface: z.enum(["user", "heartbeat"]).optional().default("user"),
  source_channel: z.string().optional(),
  source_chat_id: z.string().optional(),
});
export const TaskUpdateRequest: z.ZodType<TaskUpdateRequest> = z
  .object({
    title: z.string().min(1).max(200),
    description: z.string().max(2000),
    prompt: z.string().max(10000),
    status: z.enum([
      "inbox",
      "next",
      "planning",
      "in_progress",
      "blocked",
      "done",
      "failed",
    ]),
    agent_id: z.string(),
    priority: z.number().int().gte(1).lte(5),
    blocked_by: z.array(z.string()),
    todos: z.array(Todo),
    trigger: TaskTrigger,
    due: z.string().datetime({ offset: true }),
    clear_due: z.boolean(),
    milestone_id: z.string(),
    surface: z.enum(["user", "heartbeat"]),
    result: z.string().max(50000),
    artifacts: z.array(z.string()),
    started_at: z.string().datetime({ offset: true }),
    completed_at: z.string().datetime({ offset: true }),
  })
  .partial();
export const McpServer = z
  .object({
    id: z.string(),
    name: z.string(),
    transport: z.enum(["stdio", "sse", "http"]),
    status: z.enum(["connected", "disconnected", "error"]),
    tool_count: z.number().int().gte(0),
    tools: z.array(z.string()).optional(),
  })
  .passthrough();
export const McpServerCreate = z.object({
  name: z.string(),
  command: z.string().optional(),
  url: z.string().optional(),
  args: z.array(z.string()).optional(),
  transport: z.enum(["stdio", "sse", "http"]),
  env: z.record(z.string()).optional(),
});
export const McpServerToolsResponse = z.object({ tools: z.array(z.string()) });
export const McpToolsListResponse = z.array(
  z
    .object({
      id: z.string(),
      name: z.string(),
      enabled: z.boolean(),
      command: z.string().optional(),
      args: z.array(z.string()).optional(),
    })
    .passthrough()
);
export const McpToolCallRequest = z.object({
  server_id: z.string(),
  tool_name: z.string(),
  arguments: z.object({}).partial().passthrough().optional(),
});
export const McpToolCallResponse = z.object({
  result: z.unknown(),
  error: z.string().optional(),
});
export const ScheduleTrigger: z.ZodType<ScheduleTrigger> = z.object({
  kind: z.enum(["at", "every", "cron"]),
  cron_expr: z.string().optional(),
  every_ms: z.number().int().gte(1000).optional(),
  at_ms: z.number().int().optional(),
});
export const ScheduleState: z.ZodType<ScheduleState> = z
  .object({
    next_run_at_ms: z.number().int(),
    last_run_at_ms: z.number().int(),
    last_status: z.string(),
    last_error: z.string(),
    consecutive_failures: z.number().int(),
    running: z.boolean(),
  })
  .partial();
export const ScheduleRunRecord: z.ZodType<ScheduleRunRecord> = z.object({
  ran_at_ms: z.number().int(),
  status: z.enum(["ok", "error", "skipped", "timeout"]),
  error: z.string().optional(),
  session_id: z.string().optional(),
  duration_ms: z.number().int().optional(),
});
export const Schedule: z.ZodType<Schedule> = z.object({
  id: z.string(),
  name: z.string().min(1),
  enabled: z.boolean(),
  owner_agent_id: z.string().min(1),
  created_by: z.string().optional(),
  trigger: ScheduleTrigger,
  message: z.string().min(1),
  deliver: z.boolean(),
  session_mode: z.enum(["isolated", "continue", "main"]),
  timeout_seconds: z.number().int(),
  session_id: z.string().optional(),
  channel: z.string().optional(),
  chat_id: z.string().optional(),
  state: ScheduleState,
  runs: z.array(ScheduleRunRecord).optional(),
  created_at_ms: z.number().int(),
  updated_at_ms: z.number().int(),
});
export const ScheduleList: z.ZodType<ScheduleList> = z.object({
  schedules: z.array(Schedule),
});
export const ScheduleCreate: z.ZodType<ScheduleCreate> = z.object({
  name: z.string().min(1),
  owner_agent_id: z.string().min(1),
  trigger: ScheduleTrigger,
  message: z.string().min(1),
  deliver: z.boolean().optional(),
  session_mode: z.enum(["isolated", "continue", "main"]).optional(),
  timeout_seconds: z.number().int().gte(0).optional(),
  enabled: z.boolean().optional(),
  channel: z.string().optional(),
  chat_id: z.string().optional(),
});
export const ScheduleUpdate: z.ZodType<ScheduleUpdate> = z
  .object({
    name: z.string().min(1),
    owner_agent_id: z.string().min(1),
    trigger: ScheduleTrigger,
    message: z.string().min(1),
    deliver: z.boolean(),
    session_mode: z.enum(["isolated", "continue", "main"]),
    timeout_seconds: z.number().int().gte(0),
    enabled: z.boolean(),
    channel: z.string(),
    chat_id: z.string(),
  })
  .partial();
export const ScheduleRunResult = z.object({
  schedule_id: z.string(),
  status: z.enum(["ok", "error", "skipped", "timeout"]),
  session_id: z.string().optional(),
  error: z.string().optional(),
});
export const Notification: z.ZodType<Notification> = z.object({
  id: z.string(),
  type: z.literal("schedule_failed"),
  title: z.string().min(1),
  body: z.string().optional(),
  severity: z.enum(["info", "warning", "error"]),
  read: z.boolean(),
  created_at_ms: z.number().int(),
  updated_at_ms: z.number().int().optional(),
  schedule_id: z.string().optional(),
  session_id: z.string().optional(),
  agent_id: z.string().optional(),
});
export const NotificationList: z.ZodType<NotificationList> = z.object({
  notifications: z.array(Notification),
  unread_count: z.number().int(),
});
export const Workspace = z
  .object({
    id: z.string(),
    name: z.string().min(1),
    description: z.string().optional(),
    status: z.enum(["active", "archived"]),
    pinned: z.boolean(),
    pin_order: z.number().int(),
    core_team: z.array(z.string()).max(20).optional(),
    repository: z.string().optional(),
    task_count: z.number().int(),
    is_default: z.boolean().optional(),
    created_at: z.string().datetime({ offset: true }),
    updated_at: z.string().datetime({ offset: true }),
    owner: z.string().optional(),
  })
  .passthrough();
export const WorkspaceCreateRequest = z
  .object({
    name: z.string().min(1).max(200),
    description: z.string().max(2000).optional(),
    core_team: z.array(z.string()).max(20).optional(),
    repository: z.string().optional(),
  })
  .passthrough();
export const WorkspaceUpdateRequest = z
  .object({
    name: z.string().min(1).max(200),
    description: z.string().max(2000),
    status: z.enum(["active", "archived"]),
    pinned: z.boolean(),
    pin_order: z.number().int(),
    core_team: z.array(z.string()).max(20),
    repository: z.string(),
  })
  .partial()
  .passthrough();
export const WorkspaceSessionLink = z
  .object({
    session_id: z.string(),
    created_at: z.string().datetime({ offset: true }),
  })
  .passthrough();
export const WorkspaceDelegationEdge: z.ZodType<WorkspaceDelegationEdge> =
  z.object({
    from_agent: z.string().min(1),
    to_agent: z.string().min(1),
    modes: z.array(z.enum(["await", "background", "task"])).optional(),
    depth: z.number().int().gte(0).optional(),
  });
export const WorkspaceDelegation: z.ZodType<WorkspaceDelegation> = z.object({
  workspace_id: z.string(),
  edges: z.array(WorkspaceDelegationEdge),
  team: z.array(z.string()).optional(),
});
export const WorkspaceDelegationUpdateRequest: z.ZodType<WorkspaceDelegationUpdateRequest> =
  z.object({ edges: z.array(WorkspaceDelegationEdge) });
export const Milestone: z.ZodType<Milestone> = z
  .object({
    id: z.string(),
    workspace_id: z.string(),
    name: z.string().min(1).max(200),
    description: z.string().max(2000).optional(),
    due_date: z.string().nullish(),
    created_at: z.string().datetime({ offset: true }),
    updated_at: z.string().datetime({ offset: true }),
    owner: z.string().optional(),
    progress: z.number().gte(0).lte(1).optional(),
  })
  .passthrough();
export const MilestoneListResponse: z.ZodType<MilestoneListResponse> = z
  .object({ milestones: z.array(Milestone), total: z.number().int() })
  .passthrough();
export const MilestoneCreateRequest = z
  .object({
    name: z.string().min(1).max(200),
    description: z.string().max(2000).optional(),
    due_date: z.string().nullish(),
  })
  .passthrough();
export const MilestoneUpdateRequest = z
  .object({
    name: z.string().min(1).max(200),
    description: z.string().max(2000),
    due_date: z.string().nullable(),
  })
  .partial()
  .passthrough();
export const AgentTokenEntry: z.ZodType<AgentTokenEntry> = z
  .object({
    agent_id: z.string(),
    agent_name: z.string(),
    tokens_in: z.number().int(),
    tokens_out: z.number().int(),
    tokens_total: z.number().int(),
  })
  .passthrough();
export const TokenUsageSummary: z.ZodType<TokenUsageSummary> = z
  .object({
    agents: z.array(AgentTokenEntry),
    period_start: z.string().datetime({ offset: true }),
    period_end: z.string().datetime({ offset: true }),
  })
  .passthrough();
export const CliDetect = z
  .object({
    hasClaude: z.boolean(),
    hasCodex: z.boolean(),
    hasOpencode: z.boolean(),
  })
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
export const BackupEntry = z.object({
  filename: z.string(),
  size_bytes: z.number().int().gte(0),
  created_at: z.string().datetime({ offset: true }),
});
export const OnboardingStatusResponse = z
  .object({ onboarding_complete: z.boolean() })
  .passthrough();
export const ActivityEventsResponse: z.ZodType<ActivityEventsResponse> =
  z.object({ events: z.array(ActivityEvent), warning: z.string().optional() });
export const ChannelConfigureRequest: z.ZodType<ChannelConfigureRequest> = z
  .object({
    instance_id: z.string(),
    identity: ChannelIdentity,
    token: z.string(),
    bot_token: z.string(),
    app_id: z.string(),
    app_secret: z.string(),
    webhook_secret: z.string(),
    imap_host: z.string(),
    imap_port: z.number().int(),
    smtp_host: z.string(),
    smtp_port: z.number().int(),
    username: z.string(),
    password: z.string(),
  })
  .partial()
  .passthrough();
export const RetentionUpdateRequest = z
  .object({ session_days: z.number().int().gte(0), disabled: z.boolean() })
  .partial();

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
    path: "/activity",
    alias: "getActivity",
    description: `Returns up to 50 activity events from the last 24 hours, sorted reverse-chronological.
Includes session_start events from all agent stores and task lifecycle events.
`,
    requestFormat: "json",
    response: z.array(ActivityEvent),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
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
    description: `Updates the specified agent. All fields are optional (only provided fields change). Locked core agents reject mutations to name, description, soul, heartbeat, instructions (403). Writing soul/heartbeat/instructions triggers a config reload. Model, timeout, max_tool_iterations, steering_mode, heartbeat_enabled, heartbeat_interval changes do NOT trigger a reload.
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
    method: "delete",
    path: "/agents/:id",
    alias: "deleteAgent",
    description: `Removes a custom (non-core, non-system) agent from config.json and reloads the live config. Built-in core/system agents (locked) and the &#x60;omnipus-system&#x60; agent CANNOT be deleted (403, code &#x60;agent_locked&#x60;). Deleting an agent also clears its session history and on-disk workspace artifacts via the cascade pipeline. Audited (severity INFO, event &#x60;agent.delete&#x60;).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
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
    method: "patch",
    path: "/agents/:id",
    alias: "patchAgentOwnership",
    description: `Updates the owner_username field on a custom agent. Only admins may call this endpoint. System and core agents cannot have an owner assigned (400). Clearing the owner (empty string) requires the X-Confirm-Demote: 1 header.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ owner_username: z.string() }).partial(),
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: AgentOwnerUpdateResponse,
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
    path: "/agents/:id/mailbox",
    alias: "getAgentMailbox",
    description: `Returns the email mailbox account configured for the specified agent. Email is a TOOL surface (read_inbox, search_email, read_message, send_email, reply), not a conversational channel. The mailbox password is never returned; the &#x60;configured&#x60; flag reports whether a password is on file in the credential store.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Mailbox,
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Agent not found, or no mailbox configured for the agent.`,
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
    path: "/agents/:id/mailbox",
    alias: "setAgentMailbox",
    description: `Configures the email mailbox account for the specified agent. The password, when present, is routed into the encrypted credential store and persisted only as a reference — it is never written to config.json. Per-(agent, workspace) cap-1 applies: an agent owns one mailbox, and at most one mailbox may be bound to a given workspace (returns 422 on violation).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: MailboxConfigureRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Mailbox,
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
        description: `Agent not found.`,
        schema: ErrorResponse,
      },
      {
        status: 422,
        description: `Cap-1 violation (a mailbox already exists for the workspace).`,
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
    path: "/agents/:id/mailbox",
    alias: "deleteAgentMailbox",
    description: `Removes the agent&#x27;s mailbox account from config and deletes the stored mailbox password from the credential store. The email tools are de-registered from the agent on the next reload.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: OperationResult,
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Agent not found, or no mailbox configured.`,
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
    path: "/agents/:id/runner/test",
    alias: "testAgentRunner",
    description: `Connection/health check for the agent&#x27;s configured external-CLI runner (Spec-4 FR-4.2). Validates the CLI binary is present, runs, and is authenticated WITHOUT running real work (no tokens spent). Returns distinct reasons for missing-binary vs unauthenticated. Fails with reason &quot;not-external-cli&quot; when the agent&#x27;s executor is native/remote-a2a (no runner to test).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: RunnerTestResponse,
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
    response: AgentToolsResponse,
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
        schema: AgentToolsUpdateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: AgentToolsResponse,
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
    response: OperationResult,
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
    path: "/auth/reauth",
    alias: "reAuth",
    description: `Single-user consent primitive (FR-12.2). Re-verifies the authenticated user&#x27;s one password and mints a short-lived consent token the SPA replays in the X-Reauth-Token header on the immediately-following sensitive request (e.g. configuring an integration provider). This is NOT the dev-mode bypass guard (RequireNotBypass returns 503 in dev mode and is unrelated). Requires authentication. Rate-limited.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ password: z.string().min(1).max(72) }),
      },
    ],
    response: ReAuthResponse,
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
    response: ValidateTokenResponse,
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
    response: BackupCreateResponse,
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
    response: ChannelEnabledResponse,
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
    response: ChannelEnabledResponse,
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
    path: "/channels/:id/routing",
    alias: "getChannelRouting",
    description: `Returns the routing configuration for the specified channel, including which agent handles its inbound messages.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.object({ default_agent_id: z.string() }).partial(),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Channel ID not found.`,
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
    path: "/channels/:id/routing",
    alias: "setChannelRouting",
    description: `Sets the routing configuration for the specified channel. The default_agent_id field controls which agent handles inbound messages arriving on this channel. Omit it or leave it empty to fall back to the global default agent.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ default_agent_id: z.string() }).partial(),
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.object({ default_agent_id: z.string() }).partial(),
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
        description: `Channel ID not found.`,
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
    response: ChannelTestResponse,
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
    path: "/chat",
    alias: "postChat",
    description: `Sends a user message to the agent and streams the response via Server-Sent Events. The connection stays open until the agent finishes responding or the client disconnects.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ message: z.string() }),
      },
    ],
    response: z.void(),
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
    ],
  },
  {
    method: "post",
    path: "/config/gateway/rotate-token",
    alias: "rotateGatewayToken",
    description: `Generates a new cryptographically random 64-hex-character gateway bearer token, persists it to config.json, triggers a hot reload, and returns the new token. The previous token is immediately invalidated. Admin-only; secured by RequireAdmin + RequireNotBypass chain. Use after security incidents or on a regular rotation schedule.
`,
    requestFormat: "json",
    response: RotateTokenResponse,
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
        status: 405,
        description: `Method not allowed.`,
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
        schema: CredentialSetRequest,
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
    path: "/devices",
    alias: "listDevices",
    description: `Returns pending pairing requests and already-paired devices. Admin-only.
`,
    requestFormat: "json",
    response: DevicesResponse,
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
    ],
  },
  {
    method: "get",
    path: "/doctor",
    alias: "getDoctorResults",
    description: `Returns the most recent health check results. Returns null when no health check has been run yet.
`,
    requestFormat: "json",
    response: DoctorResult,
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/doctor",
    alias: "runDoctor",
    description: `Runs a fresh health check and returns the results.`,
    requestFormat: "json",
    response: DoctorResult,
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/gateway/god-mode",
    alias: "getGodMode",
    description: `Returns whether god mode (&quot;bypass-permissions&quot;) is currently enabled and whether it is available in this build/boot. Admin-only.
`,
    requestFormat: "json",
    response: GodModeStatus,
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
        status: 503,
        description: `Unavailable (dev_mode_bypass active).`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/gateway/god-mode",
    alias: "setGodMode",
    description: `Flips the global god-mode (&quot;bypass-permissions&quot;) switch and applies or reverts the override live (no restart). When enabled, every agent&#x27;s tool policy is floored at &quot;allow&quot;, the kernel sandbox is off, network egress is open, and the shell guard is off — regardless of per-agent profiles. Audit logging, the prompt-injection guard, and rate limiting stay on. High blast radius — admin-only, secured by RequireNotBypass (dev_mode_bypass returns 503) AND a single-use password re-auth consent token (X-Reauth-Token header; call POST /api/v1/auth/reauth first, 403 otherwise). Returns 403 when enabling while god mode is unavailable (nogodmode build or --allow-god-mode not passed). Every toggle is audit-logged with the acting user.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ enabled: z.boolean() }),
      },
    ],
    response: GodModeStatus,
    errors: [
      {
        status: 400,
        description: `Invalid request body.`,
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
      {
        status: 503,
        description: `Unavailable (dev_mode_bypass active).`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/gateway/restart",
    alias: "restartGateway",
    description: `Triggers a graceful self-restart: the gateway replies immediately, then drains in-flight work and re-execs the process (or exits cleanly for a supervisor). Used to apply restart-gated settings from the UI without a manual process bounce. The response gives the SPA a status + drain estimate so it can poll /health (and reconnect the WS) to detect the gateway going down and coming back up. High blast radius — admin-only, secured by RequireAdmin + RequireNotBypass; dev_mode_bypass returns 503.
`,
    requestFormat: "json",
    response: GatewayRestartResponse,
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
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Restart unavailable (dev_mode_bypass active).`,
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
    method: "get",
    path: "/integrations/providers",
    alias: "getIntegrationProviders",
    description: `Returns every configurable non-LLM integration provider — web-search engines (SearchProvider) and voice-input transcribers (Transcriber) — plus which provider is active for each kind (FR-12.1). API keys are never returned; configured reflects whether a key is present. Requires authentication.
`,
    requestFormat: "json",
    response: IntegrationProvidersResponse,
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
    method: "put",
    path: "/integrations/providers/:id",
    alias: "updateIntegrationProvider",
    description: `Sets the API key and/or selects a provider as active for its kind (FR-12.1). Keys are stored encrypted (AES-256-GCM) in credentials.json; only the credential reference is written to config.json. This is a sensitive settings change: the caller must first obtain a re-auth token (POST /auth/reauth) and replay it in the X-Reauth-Token header — requests without a valid, unexpired token are rejected 403. Requires authentication.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: IntegrationProviderUpdateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: IntegrationProvidersResponse,
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
    path: "/mcp-servers",
    alias: "listMcpServers",
    description: `Returns all configured MCP servers and their connection status.`,
    requestFormat: "json",
    response: z.array(McpServer),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/mcp-servers",
    alias: "addMcpServer",
    description: `Adds a new MCP server to the gateway config. Admin-only.`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: McpServerCreate,
      },
    ],
    response: McpServer,
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
    ],
  },
  {
    method: "delete",
    path: "/mcp-servers/:id",
    alias: "deleteMcpServer",
    description: `Removes an MCP server from the gateway config. Admin-only.`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
    errors: [
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
    method: "get",
    path: "/mcp-servers/:id/tools",
    alias: "listMcpServerTools",
    description: `Returns the list of tool names exposed by a specific MCP server.`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: McpServerToolsResponse,
    errors: [
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
    method: "get",
    path: "/me",
    alias: "getMe",
    description: `Returns the authenticated user&#x27;s RBAC role. Used for SPA gating.`,
    requestFormat: "json",
    response: MeInfo,
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/media/:ref_id",
    alias: "serveMedia",
    description: `Resolves a media:// URI (e.g. media://abc123) and streams the underlying file with the correct Content-Type. Used by the chat UI to display screenshots and other agent-generated media. Returns 403 if path traversal is detected in ref_id, 404 if the ref is unknown.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "ref_id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Forbidden — invalid or path-traversal ref_id.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
      {
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Media store not available.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/notifications",
    alias: "listNotifications",
    requestFormat: "json",
    response: NotificationList,
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
    path: "/notifications/:id/read",
    alias: "markNotificationRead",
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
    errors: [
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
    method: "post",
    path: "/notifications/read-all",
    alias: "markAllNotificationsRead",
    requestFormat: "json",
    response: z.void(),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
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
    path: "/performance",
    alias: "getPerformanceSettings",
    description: `Returns the max-parallel-agents cap and the effective (clamped) value currently in use. Admin-only.
`,
    requestFormat: "json",
    response: PerformanceSettings,
    errors: [
      {
        status: 403,
        description: `Admin role required.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "put",
    path: "/performance",
    alias: "updatePerformanceSettings",
    description: `Updates max_parallel_agents. The effective value is clamped to [2, min(NumCPU-2, RAM_GB/1.5)] with a ceiling of 16. Requires a gateway restart to take effect (requires_restart: false — the semaphore is resized in-memory on PUT). Admin-only.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z
          .object({ max_parallel_agents: z.number().int().gte(2).lte(16) })
          .partial(),
      },
    ],
    response: PerformanceSettings,
    errors: [
      {
        status: 400,
        description: `Invalid value.`,
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
    method: "get",
    path: "/providers",
    alias: "listProviders",
    description: `Returns all configured LLM providers with connection status and available model list.
Model lists are fetched live from each provider&#x27;s upstream /models endpoint when an API key is present.
`,
    requestFormat: "json",
    response: z.array(Provider),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
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
    method: "put",
    path: "/providers/:id",
    alias: "updateProvider",
    description: `Adds or updates an LLM provider entry. On new providers, api_key is required. On existing providers, api_key may be omitted to keep the current key. The API key is stored encrypted (AES-256-GCM) in credentials.json. Available before and after onboarding.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: ProviderUpdateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Provider,
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
        description: `api_key required for new providers.`,
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
    path: "/providers/:id/test",
    alias: "testProvider",
    description: `Verifies that an API key is configured for the given provider without making an upstream call. Returns success&#x3D;false with an error message if no key is configured. Available before and after onboarding.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: OperationResult,
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
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
        schema: z.object({ filename: z.string() }),
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
    path: "/schedules",
    alias: "listSchedules",
    description: `Returns all schedules visible to the authenticated user (#264).`,
    requestFormat: "json",
    response: ScheduleList,
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
    path: "/schedules",
    alias: "createSchedule",
    description: `Creates a schedule owned by the given agent. The owner must be an agent the caller is permitted to use (403 otherwise).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: ScheduleCreate,
      },
    ],
    response: Schedule,
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
    path: "/schedules/:id",
    alias: "getSchedule",
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Schedule,
    errors: [
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
    path: "/schedules/:id",
    alias: "updateSchedule",
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: ScheduleUpdate,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Schedule,
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
    ],
  },
  {
    method: "delete",
    path: "/schedules/:id",
    alias: "deleteSchedule",
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
    errors: [
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
    method: "post",
    path: "/schedules/:id/pause",
    alias: "pauseSchedule",
    description: `Flips enabled (pause/resume) and returns the updated schedule (#264).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Schedule,
    errors: [
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
    method: "post",
    path: "/schedules/:id/run",
    alias: "runSchedule",
    description: `Fires the schedule immediately, respecting the overlap guard and concurrency cap (#264).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: ScheduleRunResult,
    errors: [
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
    method: "get",
    path: "/security/audit-log",
    alias: "getAuditLogToggle",
    description: `Returns whether audit logging is currently enabled. Note: this is distinct from GET /api/v1/audit-log which returns entries. This endpoint controls the audit_log config flag.
`,
    requestFormat: "json",
    response: z.object({ enabled: z.boolean() }),
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
        schema: z.object({ enabled: z.boolean() }),
      },
    ],
    response: AuditLogUpdateResponse,
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
        schema: PromptGuardUpdateRequest,
      },
    ],
    response: PromptGuardUpdateResponse,
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
    response: RateLimitsResponse,
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
        schema: RateLimitsUpdateRequest,
      },
    ],
    response: RateLimitsUpdateResponse,
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
    response: RetentionUpdateResponse,
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
        schema: SandboxConfigUpdate,
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
    response: SessionScopeUpdateResponse,
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
        schema: SkillTrustUpdateRequest,
      },
    ],
    response: SkillTrustUpdateResponse,
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
        schema: z.object({ title: z.string().min(1).max(256) }),
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
    method: "get",
    path: "/sessions/:session_id/tool-results/:ref",
    alias: "getToolResult",
    description: `Returns the full JSON body of a tool result that was emitted as a ToolResultRef sentinel on the WebSocket. The result is scoped to the session that produced it — a ref from session A cannot be fetched under session B&#x27;s path. The SPA fetches this lazily when the user expands a clamped tool call, keeping main-thread memory bounded during high-volume agent activity.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "session_id",
        type: "Path",
        schema: z.string().min(1).max(128),
      },
      {
        name: "ref",
        type: "Path",
        schema: z.string().min(1).max(64),
      },
    ],
    response: z.unknown(),
    errors: [
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
    path: "/skills",
    alias: "listSkills",
    description: `Returns all skills installed in ~/.omnipus/skills/. Returns an empty array when no skills are installed.
`,
    requestFormat: "json",
    response: z.array(Skill),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
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
    method: "delete",
    path: "/skills/:name",
    alias: "deleteSkill",
    description: `Removes an installed skill from the local skills directory. The skill must not be the default skill, in use by any agent, or required by a seeded dependency. On success the gateway removes the skill directory and returns 204 No Content.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "name",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
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
        status: 409,
        description: `Conflict — e.g. resource already exists, or last-admin guard triggered.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/skills/install",
    alias: "installSkill",
    description: `Installs a skill by its slug from the ClawHub registry. The slug is the identifier returned in a SkillSearchResult. An optional version pins the installed version; when omitted the latest version is installed.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: SkillInstallRequest,
      },
    ],
    response: Skill,
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
        status: 409,
        description: `Conflict — e.g. resource already exists, or last-admin guard triggered.`,
        schema: ErrorResponse,
      },
      {
        status: 502,
        description: `Bad gateway — an upstream dependency (e.g. a skill registry) is unreachable or returned an error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/skills/marketplace",
    alias: "getSkillMarketplaceStatus",
    description: `Reports whether any skill marketplace registry is enabled. The SPA gates its skill-browse UI on this: when enabled is false, search and install-by-slug are unavailable (those endpoints return 409) and the UI offers only &quot;install from file&quot;.
`,
    requestFormat: "json",
    response: SkillMarketplaceStatus,
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/skills/search",
    alias: "searchSkills",
    description: `Searches configured skill registries (e.g. ClawHub) for skills matching the query. Returns an array of marketplace results (not installed skills). Install a result by its slug via POST /skills/install.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "q",
        type: "Query",
        schema: z.string().min(1).max(256),
      },
      {
        name: "limit",
        type: "Query",
        schema: z.number().int().gte(1).lte(50).optional().default(20),
      },
    ],
    response: z.array(SkillSearchResult),
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
        status: 409,
        description: `Conflict — e.g. resource already exists, or last-admin guard triggered.`,
        schema: ErrorResponse,
      },
      {
        status: 502,
        description: `Bad gateway — an upstream dependency (e.g. a skill registry) is unreachable or returned an error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/state",
    alias: "getAppState",
    description: `Returns onboarding status and optional diagnostic metadata. Available to all authenticated users.
`,
    requestFormat: "json",
    response: AppState,
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "patch",
    path: "/state",
    alias: "patchAppState",
    description: `Partial update to application state. Currently only supports marking onboarding complete. CSRF-protected.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ onboarding_complete: z.boolean() }).partial(),
      },
    ],
    response: AppState,
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
    ],
  },
  {
    method: "get",
    path: "/stats/tokens",
    alias: "getTokenStats",
    description: `Aggregates token usage from SessionMeta.Stats across all session files for the requested period. period&#x3D;month means the current calendar month UTC. No dollar estimates — token counts only.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "period",
        type: "Query",
        schema: z.literal("month").optional().default("month"),
      },
    ],
    response: TokenUsageSummary,
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
    ],
  },
  {
    method: "get",
    path: "/status",
    alias: "getGatewayStatus",
    description: `Returns online status, agent/channel counts, daily cost, and the binary version.
Polled by the SPA StatusBar every 15 seconds.
`,
    requestFormat: "json",
    response: GatewayStatus,
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
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
    path: "/storage/stats",
    alias: "getStorageStats",
    description: `Returns session count, workspace size, and memory entry count. May include partial warnings if some agent stores could not be read.
`,
    requestFormat: "json",
    response: StorageStats,
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
    path: "/system/cli-detect",
    alias: "getHostCliDetect",
    description: `Reports whether the gateway process can locate each of the three external-CLI runners on its PATH. Read-only, idempotent, and unaudited — the SPA roster uses this to grey-out CLIs the host cannot run instead of letting the operator hit a wizard failure at runtime. Pure Go probe (no shell-out).
`,
    requestFormat: "json",
    response: CliDetect,
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/tasks",
    alias: "listTasks",
    description: `Returns tasks in a workspace, filterable by status, agent, milestone, and surface. This is the unified task surface (Sprint 2) — it subsumes the former GTD /board/tasks listing. By default only top-level tasks (parent_task_id absent) and &#x60;surface: user&#x60; tasks are returned; use the filters to widen. Workspace-scoped. Admin-only.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "workspace_id",
        type: "Query",
        schema: z.string().optional(),
      },
      {
        name: "status",
        type: "Query",
        schema: z
          .enum([
            "inbox",
            "next",
            "planning",
            "in_progress",
            "blocked",
            "done",
            "failed",
          ])
          .optional(),
      },
      {
        name: "agent_id",
        type: "Query",
        schema: z.string().optional(),
      },
      {
        name: "milestone_id",
        type: "Query",
        schema: z.string().optional(),
      },
      {
        name: "surface",
        type: "Query",
        schema: z.enum(["user", "heartbeat"]).optional(),
      },
      {
        name: "parent_task_id",
        type: "Query",
        schema: z.string().optional(),
      },
      {
        name: "limit",
        type: "Query",
        schema: z.number().int().lte(1000).optional().default(200),
      },
      {
        name: "offset",
        type: "Query",
        schema: z.number().int().optional().default(0),
      },
    ],
    response: z.array(Task),
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
    ],
  },
  {
    method: "post",
    path: "/tasks",
    alias: "createTask",
    description: `Creates a new task. Lands in &#x60;inbox&#x60; regardless of input (Detail #8 landing rule). Workspace-scoped (workspace_id required in the body). Admin-only.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: TaskCreateRequest,
      },
    ],
    response: Task,
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
    ],
  },
  {
    method: "get",
    path: "/tasks/:id",
    alias: "getTask",
    description: `Returns a single task by ID, including read-time rollup. Admin-only.`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Task,
    errors: [
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
    method: "patch",
    path: "/tasks/:id",
    alias: "updateTask",
    description: `Partially updates task fields (PATCH semantics — only provided fields change). Dragging a card to &#x60;in_progress&#x60; / Run is a status PATCH; there is no separate /start endpoint. Admin-only.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: TaskUpdateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Task,
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
        status: 409,
        description: `Conflict — e.g. resource already exists, or last-admin guard triggered.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "delete",
    path: "/tasks/:id",
    alias: "deleteTask",
    description: `Deletes a task by ID. Admin-only.`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
    errors: [
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
    path: "/tasks/:id/dependencies",
    alias: "setTaskDependencies",
    description: `Replaces the task&#x27;s &#x60;blocked_by&#x60; set atomically. A write-time DAG cycle validator rejects self-edges and cycles (max depth 50). Returns the updated task. Admin-only.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.array(z.string()),
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Task,
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
        status: 409,
        description: `Conflict — e.g. resource already exists, or last-admin guard triggered.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/tasks/:id/subtasks",
    alias: "listSubtasks",
    description: `Returns all subtasks (children with this parent_task_id). Admin-only.`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.array(Task),
    errors: [
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
    path: "/tasks/:id/todos",
    alias: "setTaskTodos",
    description: `Replaces the task&#x27;s &#x60;todos&#x60; array atomically (Tier-1 checklist; Detail #3 of the three-tier model). Returns the updated task. Admin-only.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.array(Todo),
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Task,
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
        schema: ToolApprovalActionRequest,
      },
      {
        name: "approval_id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: ToolApprovalResponse,
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
    path: "/tools/mcp",
    alias: "listMcpTools",
    description: `Returns all configured MCP servers with their status and tool metadata for the agent tool picker UI. Requires authentication.
`,
    requestFormat: "json",
    response: z.array(
      z
        .object({
          id: z.string(),
          name: z.string(),
          enabled: z.boolean(),
          command: z.string().optional(),
          args: z.array(z.string()).optional(),
        })
        .passthrough()
    ),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
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
    method: "post",
    path: "/tools/mcp",
    alias: "callMcpTool",
    description: `Calls a named tool on a specific MCP server and returns the result. Requires authentication. The tool and server must already be configured and enabled. Arguments are tool-specific.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: McpToolCallRequest,
      },
    ],
    response: z.object({ result: z.unknown(), error: z.string().optional() }),
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
    path: "/upload",
    alias: "uploadFiles",
    description: `Streams multipart file uploads to disk under ~/.omnipus/uploads/{session_id}/.
Max file size per part: 100 MB. Data is streamed directly to disk; the full file is never buffered in memory.
session_id may be supplied as a query parameter or as a form field before the file parts.
Returns HTTP 201 on success.
`,
    requestFormat: "form-data",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: uploadFiles_Body,
      },
      {
        name: "session_id",
        type: "Query",
        schema: z.string().optional(),
      },
    ],
    response: UploadFilesResponse,
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
        status: 405,
        description: `Method not allowed.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/uploads/:session_id/:filename",
    alias: "serveUpload",
    description: `Serves a previously uploaded file by session ID and filename. Authentication is optional — browsers must be able to load image URLs directly from chat messages. Returns the file content with the appropriate Content-Type header.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "session_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "filename",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
    errors: [
      {
        status: 400,
        description: `Bad request — missing or invalid field.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Resource not found.`,
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
    path: "/user-context",
    alias: "getUserContext",
    description: `Returns the current content of USER.md from the default workspace. This file holds workspace-level context about the user (background, preferences, etc.) that is prepended to agent prompts. Returns empty string when the file does not exist. Requires authentication.
`,
    requestFormat: "json",
    response: z.object({ content: z.string() }),
    errors: [
      {
        status: 401,
        description: `Authentication required or credentials invalid.`,
        schema: ErrorResponse,
      },
      {
        status: 405,
        description: `Method not allowed.`,
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
    path: "/user-context",
    alias: "putUserContext",
    description: `Replaces the entire content of USER.md in the default workspace with the provided string. Passing an empty string clears the file. The new content is returned in the response. Requires authentication.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ content: z.string().max(262144) }),
      },
    ],
    response: z.object({ content: z.string() }),
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
        status: 405,
        description: `Method not allowed.`,
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
        schema: z.object({ password: z.string().min(8).max(72) }),
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
    path: "/version",
    alias: "getVersion",
    description: `Returns the gateway&#x27;s version string and embedded VCS revision SHA. Used by the frontend to detect version drift and prompt &quot;New version available&quot; (#110). No authentication required.
`,
    requestFormat: "json",
    response: VersionResponse,
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
    path: "/voice/provider",
    alias: "getVoiceProvider",
    description: `Reads the active voice provider config from the gateway and returns a small descriptor so the SPA can decide which voice widget variant to render in the agent edit slide-over (dropdown / free-text / disabled). Requires authentication. Returns 503 when the provider is misconfigured (e.g. credentials unavailable) — the SPA falls back to a &quot;Voice provider unavailable&quot; disabled widget per voice-provider-detect.ts.
`,
    requestFormat: "json",
    response: VoiceProvider,
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
      {
        status: 503,
        description: `Service unavailable — e.g. credential store locked.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/voice/transcribe",
    alias: "transcribeAudio",
    description: `Accepts a multipart/form-data audio file (field &quot;audio&quot;) captured by the chat composer mic and returns the transcribed text via the active Transcriber (FR-12.1). Responds 503 when no transcriber is configured. Requires authentication.
`,
    requestFormat: "form-data",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ audio: z.instanceof(File) }).passthrough(),
      },
    ],
    response: TranscribeResponse,
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
  {
    method: "get",
    path: "/workspaces",
    alias: "listWorkspaces",
    description: `Returns all workspaces, newest-first. Excludes archived workspaces by default. Use ?status&#x3D;archived to list archived workspaces or ?status&#x3D;all for everything. task_count is computed live from the GTD task store.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "status",
        type: "Query",
        schema: z.enum(["active", "archived", "all"]).optional(),
      },
    ],
    response: z.array(Workspace),
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
    ],
  },
  {
    method: "post",
    path: "/workspaces",
    alias: "createWorkspace",
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: WorkspaceCreateRequest,
      },
    ],
    response: Workspace,
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
    ],
  },
  {
    method: "get",
    path: "/workspaces/:id",
    alias: "getWorkspace",
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Workspace,
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
    path: "/workspaces/:id",
    alias: "updateWorkspace",
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: WorkspaceUpdateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Workspace,
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
    method: "delete",
    path: "/workspaces/:id",
    alias: "deleteWorkspace",
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
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
    method: "get",
    path: "/workspaces/:id/delegation",
    alias: "getWorkspaceDelegation",
    description: `Returns the per-workspace delegation graph (M5): the directed delegation edges plus the computed team node set. Returns 200 with an empty edges array when the workspace exists but has no delegation configured. Returns 404 when the workspace does not exist.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: WorkspaceDelegation,
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
    path: "/workspaces/:id/delegation",
    alias: "updateWorkspaceDelegation",
    description: `Replaces the workspace&#x27;s delegation edge set wholesale (full replace). Validates that every from_agent / to_agent resolves to a known agent, rejects self-edges, and rejects depths above the global subturn ceiling. Returns the updated graph.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: WorkspaceDelegationUpdateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: WorkspaceDelegation,
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
    method: "get",
    path: "/workspaces/:id/milestones",
    alias: "listWorkspaceMilestones",
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: MilestoneListResponse,
    errors: [
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
    method: "post",
    path: "/workspaces/:id/milestones",
    alias: "createWorkspaceMilestone",
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: MilestoneCreateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Milestone,
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
    method: "get",
    path: "/workspaces/:id/milestones/:milestoneId",
    alias: "getWorkspaceMilestone",
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "milestoneId",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Milestone,
    errors: [
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
    path: "/workspaces/:id/milestones/:milestoneId",
    alias: "updateWorkspaceMilestone",
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: MilestoneUpdateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "milestoneId",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Milestone,
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
    method: "delete",
    path: "/workspaces/:id/milestones/:milestoneId",
    alias: "deleteWorkspaceMilestone",
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "milestoneId",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
    errors: [
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
    method: "get",
    path: "/workspaces/:id/sessions",
    alias: "listWorkspaceSessions",
    description: `Returns sessions that were auto-linked when an agent created or updated a GTD task with this workspace_id. Returns 200 with empty array when workspace exists but has no links. Returns 404 when workspace does not exist.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.array(WorkspaceSessionLink),
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
]);

export const api = new Zodios(endpoints);

export function createApiClient(baseUrl: string, options?: ZodiosOptions) {
  return new Zodios(baseUrl, endpoints, options);
}

// ── AsyncAPI WebSocket frame schemas ─────────────────────────────────────────
// Auto-generated from contracts/asyncapi.yaml components.schemas.
// Do not edit directly — re-run: node scripts/_gen-asyncapi-types.mjs
// These extend the REST schemas above with all WS frame types.

export const WsFrameType = z.enum(["auth", "message", "cancel", "exec_approval_response", "ping", "attach_session", "device_pairing_response", "session_close", "session_started", "token", "done", "error", "tool_call_start", "tool_call_result", "subagent_start", "subagent_end", "exec_approval_request", "exec_approval_expired", "task_status_changed", "replay_message", "replay_error", "rate_limit", "media", "agent_switched", "tool_approval_required", "session_state", "system_overload", "replay_warning", "cancel_stage", "pong", "session_close_ack", "exec_approval_response_ack", "device_pairing_request", "whatsapp_pairing", "whatsapp_pairing_subscribe", "notification"]);

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

// ── Validation policy note (mirrors MessageFrame.yaml) ────────────────────────
// Outer MessageFrame is .strict(): unknown top-level keys are rejected (a
// server-added field surfaces as a visible schema failure, debuggable).
// The nested `metadata` object is intentionally .passthrough(): it is the
// wire's forward-compat extension channel. Adding a new optional metadata
// field server-side must NOT break already-shipped SPA clients — strict()
// would. Drift on a *newly required* metadata field is caught by the
// W2-29 outbound validator (src/lib/ws.ts) the moment the server starts
// requiring it. Inbound, the dev-mode console.debug in
// src/lib/ws.ts::_parseServerFrame lists any extra metadata keys so
// drift is grep-able even though strict-validation is off.
// See contracts/components/schemas/MessageFrame.yaml for the full rationale.

export const CancelFrame = z
  .object({
    type: z.literal("cancel"),
    session_id: z.string().min(1).max(128),
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

export const ExecApprovalRequestFrame = z
  .object({
    type: z.literal("exec_approval_request"),
    session_id: z.string().min(1).max(128),
    id: z.string().min(1),
    command: z.string().min(1).max(8192),
    tool: z.string().max(128).optional(),
    params: z.record(z.unknown()).optional(),
    message: z.string().optional(),
    working_dir: z.string().optional(),
    matched_policy: z.string().optional(),
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

export const ExecApprovalExpiredFrame = z
  .object({
    type: z.literal("exec_approval_expired"),
    id: z.string().min(1),
    session_id: z.string().min(1),
    message: z.string().optional(),
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
  ExecApprovalResponseFrame,
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
  ExecApprovalRequestFrame,
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
  ExecApprovalResponseAckFrame,
  DevicePairingRequestFrame,
  WhatsAppPairingFrame,
  SessionCloseFrame,
  WhatsAppPairingSubscribeFrame,
  ExecApprovalExpiredFrame,
  NotificationFrame,
]);

export type WsFrameType = z.infer<typeof WsFrameType>;
export type WsFrame = z.infer<typeof WsFrame>;
