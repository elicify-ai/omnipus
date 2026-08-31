import { makeApi, Zodios, type ZodiosOptions } from "@zodios/core";
import { z } from "zod";

type LoginResponse = {
  token: BearerToken;
  username: string;
  warning?: string | undefined;
};
type BearerToken = string;
type OnboardingCompleteResponse = LoginResponse;
type ProbeProviderResponse = {
  success: boolean;
  models?: Array<string> | undefined;
  error?: string | undefined;
  validation?: ProviderValidation | undefined;
};
type ProviderValidation = {
  outcome: "valid" | "invalid_key" | "no_credit" | "unreachable" | "restricted";
  message?: string | undefined;
};
type Session = {
  id: string;
  type?:
    | (
        | "chat"
        | "task"
        | "channel"
        | "scheduled"
        | "heartbeat"
        | "verifier"
        | "delegate"
      )
    | undefined;
  protected?: boolean | undefined;
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
  parent_session_id?: string | undefined;
  child_count?: number | undefined;
};
type SessionStats = {
  tokens_in: number;
  tokens_out: number;
  tokens_total: number;
  cost: number;
  tool_calls: number;
  message_count: number;
  tokens_cache_read?: number | undefined;
  tokens_cache_write?: number | undefined;
  by_model?: {} | undefined;
};
type ModelTokens = {
  in?: number | undefined;
  out?: number | undefined;
  cache_read?: number | undefined;
  cache_write?: number | undefined;
  total: number;
};
type SessionDetail = {
  session: Session;
  messages: Array<Message>;
  agent_removed?: boolean | undefined;
};
type Message = {
  id: string;
  type?:
    | (
        | "message"
        | "compaction"
        | "system"
        | "tool_call"
        | "turn_canceled"
        | "judge_verdict"
      )
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
  verdict?: JudgeVerdict | undefined;
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
  status:
    | "success"
    | "error"
    | "pending"
    | "denied"
    | "running"
    | "cancelled"
    | "interrupted"
    | "parked";
  duration_ms?: number | undefined;
  error?: string | undefined;
  parameters?: {} | undefined;
  result?: {} | undefined;
  parent_tool_call_id?: string | undefined;
};
type JudgeVerdict = {
  id: string;
  scope: "task" | "plan" | "goal";
  task_id?: string | undefined;
  plan_id?: string | undefined;
  round: number;
  met: boolean;
  per_criterion: Array<CriterionVerdict>;
  model: string;
  judged_at: string;
  judge_agent_id: string;
};
type CriterionVerdict = {
  criterion_id: string;
  met: boolean;
  reason: string;
};
type SessionPage = {
  sessions: Array<Session>;
  next_cursor?: string | undefined;
  partial_errors?: Array<string> | undefined;
};
type HostFolderListing = {
  path: string;
  parent?: string | undefined;
  entries: Array<HostFolderEntry>;
};
type HostFolderEntry = {
  name: string;
  path: string;
  mountable: boolean;
  broad?: boolean | undefined;
  reason?: string | undefined;
};
type LibraryEntry = {
  name: string;
  path: string;
  is_dir: boolean;
  is_hidden: boolean;
  size: number;
  modified_at: string;
  mime?: string | undefined;
  mount?: LibraryEntryMount | undefined;
  is_text_editable: boolean;
};
type LibraryEntryMount = {
  name: string;
  host_path: string;
  broad: boolean;
};
type LibraryUploadResponse = {
  entries: Array<LibraryEntry>;
};
type KnowledgeSearchResponse = {
  collection_id: string;
  hits: Array<KnowledgeSearchHit>;
  incompleteness: KnowledgeSearchIncompleteness;
  limit_applied: number;
  limit_clamped: boolean;
  limit_requested?: number | undefined;
};
type KnowledgeSearchHit = {
  path: string;
  title: string;
  score: number;
  kind: "note" | "attachment";
  excerpt?: string | undefined;
  excerpt_unavailable?:
    | (
        | "file_unreadable"
        | "file_missing"
        | "match_moved"
        | "budget_exhausted"
        | "attachment_not_read"
      )
    | undefined;
  byte_offset?: number | undefined;
};
type KnowledgeSearchIncompleteness = {
  complete: boolean;
  total_known: boolean;
  statement: string;
  indexed_files?: number | undefined;
  total_files?: number | undefined;
};
type KnowledgeGraphResponse = {
  collection_id: string;
  kind: "links" | "backlinks" | "unresolved" | "orphans" | "neighbourhood";
  source_path?: string | undefined;
  nodes: Array<KnowledgeGraphNode>;
  edges: Array<KnowledgeGraphEdge>;
  skipped: Array<KnowledgeGraphSkip>;
  truncated: boolean;
  hop_limit_applied?: number | undefined;
  node_limit_applied?: number | undefined;
};
type KnowledgeGraphNode = {
  path: string;
  title?: string | undefined;
  exists: boolean;
};
type KnowledgeGraphEdge = {
  from_path: string;
  to_path: string;
  link_text?: string | undefined;
  alias?: string | undefined;
  heading?: string | undefined;
  resolution:
    | "exact_path"
    | "unique_basename"
    | "shortest_path"
    | "lexicographic"
    | "unresolved";
  ambiguous: boolean;
  candidates?: Array<string> | undefined;
  embed?: boolean | undefined;
};
type KnowledgeGraphSkip = {
  path: string;
  reason:
    | "symlink"
    | "outside_root"
    | "unreadable"
    | "not_addressable"
    | "node_limit"
    | "hop_limit";
  detail?: string | undefined;
};
type KnowledgeOutline = {
  path: string;
  is_knowledge_base: boolean;
  collection_id?: string | undefined;
  headings: Array<KnowledgeOutlineHeading>;
  frontmatter_malformed?: boolean | undefined;
};
type KnowledgeOutlineHeading = {
  level: number;
  text: string;
  slug: string;
  line?: number | undefined;
  byte_offset?: number | undefined;
};
type RecordSchema = {
  types: Array<RecordType>;
  problems: Array<RecordProblem>;
};
type RecordType = {
  schema_version: number;
  type: string;
  label?: string | undefined;
  identity_prefix?: string | undefined;
  properties: Array<PropertyDef>;
  source_path?: string | undefined;
};
type PropertyDef = {
  name: string;
  type:
    | "text"
    | "enum"
    | "relation"
    | "date"
    | "integer"
    | "decimal"
    | "person"
    | "checkbox";
  many: boolean;
  required: boolean;
  label?: string | undefined;
  values?: Array<EnumValueDef> | undefined;
  to?: string | undefined;
  inverse?: string | undefined;
  unit?: string | undefined;
  formula?: string | undefined;
};
type EnumValueDef = {
  value: string;
  label?: string | undefined;
  position: number;
  group?: ("open" | "done" | "cancelled") | undefined;
};
type RecordProblem = {
  code:
    | "missing_schema_version"
    | "duplicate_type_declaration"
    | "unknown_property"
    | "unknown_enum_value"
    | "missing_required"
    | "arity_violation"
    | "enum_violation"
    | "type_mismatch"
    | "dangling_relation"
    | "relation_type_mismatch"
    | "cardinality_violation"
    | "duplicate_id"
    | "integer_not_whole"
    | "integer_out_of_range"
    | "candidate_cap_exceeded"
    | "hop_limit_exceeded"
    | "hop_traversal_bound_exceeded"
    | "page_size_clamped"
    | "scope_truncated"
    | "text_search_truncated"
    | "aggregate_refused"
    | "index_unavailable"
    | "evaluation_bound_exceeded"
    | "unsupported_operator"
    | "unsupported_parameter"
    | "empty_like_pattern"
    | "empty_in_list"
    | "literal_type_mismatch"
    | "ordering_on_many_property"
    | "comparison_undefined"
    | "date_format_ambiguous"
    | "decimal_scale_exceeded"
    | "stale_record"
    | "orphan_row"
    | "stale_cursor"
    | "unknown_view"
    | "unknown_record_type";
  reason: string;
  records: Array<string>;
  property?: string | undefined;
  expected?: string | undefined;
  fix?: string | undefined;
  permitted?: Array<string> | undefined;
  paths?: Array<string> | undefined;
};
type VaultRecord = {
  id: string;
  type: string;
  path: string;
  title?: string | undefined;
  version_token?: string | undefined;
  properties: Array<RecordPropertyValue>;
};
type RecordPropertyValue = {
  property: string;
  type?:
    | ("text" | "enum" | "relation" | "date" | "integer" | "decimal" | "person")
    | undefined;
  values: Array<RecordValue>;
};
type RecordValue = {
  type:
    | "text"
    | "enum"
    | "relation"
    | "date"
    | "integer"
    | "decimal"
    | "person"
    | "checkbox";
  text?: string | undefined;
  enum?: string | undefined;
  relation?: RecordRef | undefined;
  date?: string | undefined;
  integer?: string | undefined;
  decimal?: string | undefined;
  person?: RecordRef | undefined;
  checkbox?: boolean | undefined;
};
type RecordRef = {
  link: string;
  resolved: boolean;
  id?: string | undefined;
  type?: string | undefined;
  title?: string | undefined;
};
type RecordQueryRequest = {
  type: string;
  filters?: Array<RecordFilter> | undefined;
  group_by?: Array<string> | undefined;
  sort?: Array<RecordSort> | undefined;
  aggregates?: Array<RecordAggregate> | undefined;
  select?: Array<string> | undefined;
  limit?: number | undefined;
  cursor?: string | undefined;
  hops?: number | undefined;
};
type RecordFilter = {
  property: string;
  op: "eq" | "lt" | "lte" | "gt" | "gte" | "contains" | "is_absent";
  values?: Array<RecordValue> | undefined;
  negate?: boolean | undefined;
  include_absent?: boolean | undefined;
  via?: Array<string> | undefined;
};
type RecordSort = {
  property: string;
  direction: "asc" | "desc";
};
type RecordAggregate = {
  op:
    | "count"
    | "sum"
    | "min"
    | "max"
    | "avg"
    | "median"
    | "stddev"
    | "range"
    | "earliest"
    | "latest"
    | "checked"
    | "unchecked"
    | "empty"
    | "filled"
    | "unique";
  property?: string | undefined;
};
type RecordQueryResponse = {
  records: Array<VaultRecord>;
  complete: boolean;
  problems: Array<RecordProblem>;
  refused: boolean;
  groups?: Array<RecordGroup> | undefined;
  aggregates?: Array<RecordAggregateResult> | undefined;
  limit_applied: number;
  limit_clamped: boolean;
  limit_requested?: number | undefined;
  total_matched?: number | undefined;
  next_cursor?: string | undefined;
};
type RecordGroup = {
  keys: Array<RecordGroupKey>;
  count: number;
  record_ids: Array<string>;
  aggregates?: Array<RecordAggregateResult> | undefined;
};
type RecordGroupKey = {
  property: string;
  absent: boolean;
  value?: RecordValue | undefined;
  label?: string | undefined;
};
type RecordAggregateResult = {
  op:
    | "count"
    | "sum"
    | "min"
    | "max"
    | "avg"
    | "median"
    | "stddev"
    | "range"
    | "earliest"
    | "latest"
    | "checked"
    | "unchecked"
    | "empty"
    | "filled"
    | "unique";
  property?: string | undefined;
  refused: boolean;
  count?: number | undefined;
  value?: RecordValue | undefined;
  excluded_records?: number | undefined;
};
type RecordWriteRequest = {
  type: string;
  id?: string | undefined;
  path?: string | undefined;
  version_token?: string | undefined;
  properties: Array<RecordPropertyValue>;
};
type ViewDef = {
  name: string;
  type?: string | undefined;
  label?: string | undefined;
  filter?: VaultFilterNode | undefined;
  grouping?: Array<ViewGroupBy> | undefined;
  sort?: Array<RecordSort> | undefined;
  properties?: Array<string> | undefined;
  property_config?: {} | undefined;
  layout?:
    | ("table" | "cards" | "board" | "calendar" | "gallery" | "map")
    | undefined;
  formulas?: {} | undefined;
  aggregates?: Array<RecordAggregate> | undefined;
  limit?: number | undefined;
  disabled?: boolean | undefined;
  source?: string | undefined;
  untranslated?: Array<string> | undefined;
};
type ViewGroupBy = {
  property: string;
  direction?: ("asc" | "desc") | undefined;
};
type ViewPropertyConfig = Partial<{
  display_name: string;
}>;
type VaultFindRequest = Partial<{
  words: string;
  type: string;
  kind: "note" | "record" | "task" | "attachment";
  filter: VaultFilterNode;
  view: string;
  near: string;
  hops: number;
  join: Array<string>;
  group_by: Array<VaultFindGroupBy>;
  sort: Array<VaultFindSort>;
  select: Array<string>;
  aggregate: Array<VaultFindAggregate>;
  explain: boolean;
  limit: number;
  cursor: string;
  detail: "minimal" | "standard";
}>;
type VaultFindGroupBy = {
  property: string;
  direction?: ("asc" | "desc") | undefined;
};
type VaultFindSort = {
  property: string;
  direction?: ("asc" | "desc") | undefined;
};
type VaultFindAggregate = {
  op:
    | "count"
    | "sum"
    | "min"
    | "max"
    | "avg"
    | "median"
    | "stddev"
    | "range"
    | "earliest"
    | "latest"
    | "checked"
    | "unchecked"
    | "empty"
    | "filled"
    | "unique";
  property?: string | undefined;
};
type VaultFilterNode = Partial<{
  all: Array<VaultFilterNode>;
  any: Array<VaultFilterNode>;
  not: VaultFilterNode;
  property: string;
  op:
    | "="
    | "<>"
    | "<"
    | "<="
    | ">"
    | ">="
    | "LIKE"
    | "IN"
    | "IS NULL"
    | "IS NOT NULL";
  value: string;
  values: Array<string>;
}>;
type VaultFindResponse = {
  complete: boolean;
  complete_reason?: string | undefined;
  refused: boolean;
  counts: VaultFindCounts;
  query_echo: string;
  index?: VaultIndexState | undefined;
  rows: Array<VaultFindRow>;
  elided?: number | undefined;
  elided_summary?: string | undefined;
  groups?: Array<VaultFindGroup> | undefined;
  totals: Array<VaultFindTotal>;
  problems: Array<RecordProblem>;
  next: Array<VaultFindAction>;
  nearest_terms?: Array<VaultTermCount> | undefined;
  plan?: Array<VaultFindPlanStep> | undefined;
  next_cursor?: string | undefined;
  limit_applied?: number | undefined;
  limit_clamped?: boolean | undefined;
  limit_requested?: number | undefined;
};
type VaultFindCounts = {
  selected: number;
  evaluated: number;
  shown: number;
};
type VaultIndexState = {
  returned: number;
  agreeing: number;
  epoch?: number | undefined;
};
type VaultFindRow = {
  id?: string | undefined;
  path: string;
  title: string;
  line?: number | undefined;
  status?: ("open" | "done") | undefined;
  text?: string | undefined;
  cells: Array<VaultFindCell>;
  joins: Array<VaultFindJoin>;
  stale?: boolean | undefined;
};
type VaultFindCell = {
  property: string;
  value: string;
};
type VaultFindJoin = {
  relation: string;
  target: string;
  cells: Array<VaultFindCell>;
};
type VaultFindGroup = {
  property: string;
  key: string;
  absent?: boolean | undefined;
  count: number;
  paths: Array<string>;
  subgroups?: Array<VaultFindSubgroup> | undefined;
};
type VaultFindSubgroup = {
  property: string;
  key: string;
  absent?: boolean | undefined;
  count: number;
  paths: Array<string>;
};
type VaultFindTotal = {
  op:
    | "count"
    | "sum"
    | "min"
    | "max"
    | "avg"
    | "median"
    | "stddev"
    | "range"
    | "earliest"
    | "latest"
    | "checked"
    | "unchecked"
    | "empty"
    | "filled"
    | "unique";
  label: string;
  value: string;
  scope: string;
  refused?: boolean | undefined;
};
type VaultFindAction = {
  label: string;
  call: string;
};
type VaultTermCount = {
  term: string;
  documents: number;
};
type VaultFindPlanStep = {
  stage:
    | "scope"
    | "narrow"
    | "retrieve"
    | "compare"
    | "join"
    | "group"
    | "sort"
    | "aggregate"
    | "render";
  property?: string | undefined;
  source?:
    | ("properties_index" | "text_index" | "go_comparator" | "schema" | "none")
    | undefined;
  detail: string;
};
type ValidationReport = {
  complete: boolean;
  problems: Array<RecordProblem>;
  records_checked: number;
  types_checked: number;
  types?: Array<string> | undefined;
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
  warning?: string | undefined;
  timeout_seconds: number;
  max_tool_iterations: number;
  tools_cfg?: AgentToolsCfg | undefined;
  shell_policy?: AgentShellPolicy | undefined;
  fallback_models?: Array<FallbackModel> | undefined;
  model_params?: AgentModelParams | undefined;
  rate_limits?: AgentRateLimits | undefined;
  stats?: AgentStats | undefined;
  default?: boolean | undefined;
  skills?: Array<string> | undefined;
  updated_at?: string | undefined;
  voice?: (string | null) | undefined;
  executor?: ExecutorConfig | undefined;
  memory_enabled?: boolean | undefined;
};
type AgentToolsCfg = Partial<{
  builtin: {
    policies: {};
  };
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
  cli: ExternalCliTool;
  cli_path: string;
  env_overrides: {};
  cli_args: string;
}>;
type ExternalCliTool = "claude-code" | "codex" | "opencode";
type AgentCreateRequest =
  | AgentCreateRequestMain
  | AgentCreateRequestSubagent
  | AgentCreateRequestSubagent3p;
type AgentCreateRequestMain = {
  type: "Main";
  name: string;
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
  voice?: (string | null) | undefined;
  shell_policy?: AgentShellPolicy | undefined;
  timeout_seconds?: number | undefined;
  max_tool_iterations?: number | undefined;
};
type AgentCreateRequestSubagent = {
  type: "Subagent";
  name: string;
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
  shell_policy?: AgentShellPolicy | undefined;
  timeout_seconds?: number | undefined;
  max_tool_iterations?: number | undefined;
};
type AgentCreateRequestSubagent3p = {
  type: "subagent_3p";
  name: string;
  description?: string | undefined;
  model?: string | undefined;
  provider?: string | undefined;
  color?: string | undefined;
  icon?: string | undefined;
  rate_limits?:
    | Partial<{
        use_global_defaults: boolean;
        max_llm_calls_per_hour: number;
        max_tool_calls_per_minute: number;
        max_cost_per_day: number;
      }>
    | undefined;
  soul: string;
  executor: ExecutorConfig;
  timeout_seconds?: number | undefined;
};
type AgentUpdateRequest = Partial<{
  updated_at: string;
  name: string;
  description: string;
  model: string;
  provider: string;
  soul: string;
  heartbeat: string;
  timeout_seconds: number;
  max_tool_iterations: number;
  heartbeat_enabled: boolean;
  heartbeat_interval: number;
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
  voice: string | null;
  executor: ExecutorConfig;
  memory_enabled: boolean;
}>;
type ExecutorDefaults = {
  cli: ExternalCliTool;
  auto_applied_flags: Array<string>;
  notes: string;
};
type ExecutorCommandPreviewRequest = {
  cli: ExternalCliTool;
  model?: string | undefined;
  cli_path?: string | undefined;
  cli_args?: string | undefined;
  max_tool_iterations?: number | undefined;
};
type ExecutorSmokeTestRequest = {
  cli: ExternalCliTool;
  agent_id?: string | undefined;
  model?: string | undefined;
  cli_path?: string | undefined;
  cli_args?: string | undefined;
};
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
type ChannelId = string;
type ChannelIdentity = {
  kind: "agent" | "user";
  id?: string | undefined;
};
type AuditLogResponse = {
  entries: Array<AuditEntry>;
  chain_status: "valid" | "broken" | "unknown";
  chain_broken_index?: number | undefined;
};
type AuditEntry = {
  timestamp: string;
  event: string;
  decision?: ("allow" | "deny" | "error") | undefined;
  agent_id?: string | undefined;
  session_id?: string | undefined;
  user?: string | undefined;
  tool?: string | undefined;
  command?: string | undefined;
  parameters?: {} | undefined;
  policy_rule?: string | undefined;
  details?: {} | undefined;
  actor?: string | undefined;
  resource?: string | undefined;
  old_value?: {} | undefined;
  new_value?: {} | undefined;
};
type Provider = {
  id: string;
  name: string;
  display_name?: string | undefined;
  status: "connected" | "disconnected" | "error";
  models: Array<string>;
  has_models_endpoint?: boolean | undefined;
  has_api_key?: boolean | undefined;
  warning?: string | undefined;
  error?: string | undefined;
  validation?: ProviderValidation | undefined;
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
  status: "inbox" | "next" | "in_progress" | "blocked" | "done" | "failed";
  agent_id?: string | undefined;
  cancel_reason?: ("stopped_by_user" | null) | undefined;
  agent_name?: string | undefined;
  priority?: number | undefined;
  blocked_by?: Array<string> | undefined;
  todos?: Array<Todo> | undefined;
  parent_task_id?: string | undefined;
  workspace_id: string;
  tags?: Array<string> | undefined;
  plan_id?: string | undefined;
  write_set?: Array<string> | undefined;
  stream?: string | undefined;
  is_join?: boolean | undefined;
  judge_rounds?: number | undefined;
  criteria?: Array<AcceptanceCriterion> | undefined;
  attempt_count?: number | undefined;
  max_attempts?: (number | null) | undefined;
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
          | "in_progress"
          | "blocked"
          | "done"
          | "failed";
      }>
    | undefined;
};
type Todo = {
  text: string;
  status: "pending" | "in_progress" | "completed";
};
type AcceptanceCriterion = {
  id?: string | undefined;
  kind: "check" | "prose" | "behavior";
  text: string;
  check?:
    | {
        command: string;
        expected_exit_code: number;
      }
    | undefined;
  behavior?:
    | {
        tool: string;
        min_count?: number | undefined;
        max_count?: number | undefined;
        scope?: ("attempt" | "task_session") | undefined;
      }
    | undefined;
  author: {
    kind: "agent" | "user";
    id: string;
  };
  status: "pending" | "met" | "unmet";
};
type TaskTrigger = {
  type: "manual" | "once" | "every" | "recurring";
  config: Partial<
    {
      at_ms: number;
      every_ms: number;
      cron_expr: string;
      rrule: string;
      dtstart_ms: number;
      tz: string;
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
  manifest_tier: "full" | "compressed" | "infra";
};
type ChannelEnabledResponse = {
  id: ChannelId;
  enabled: boolean;
};
type MailboxListResponse = {
  mailboxes: Array<Mailbox>;
};
type Mailbox = {
  agent_id: string;
  enabled: boolean;
  workspace_id: string;
  imap_host?: string | undefined;
  imap_port?: number | undefined;
  smtp_host?: string | undefined;
  smtp_port?: number | undefined;
  username?: string | undefined;
  configured: boolean;
};
type OperationResult = {
  success: boolean;
  error?: string | undefined;
  validation?: ProviderValidation | undefined;
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
  tags?: Array<string> | undefined;
  plan_id?: string | undefined;
  write_set?: Array<string> | undefined;
  stream?: string | undefined;
  is_join?: boolean | undefined;
  criteria?: Array<AcceptanceCriterion> | undefined;
  max_attempts?: (number | null) | undefined;
  due?: string | undefined;
  surface?: ("user" | "heartbeat") | undefined;
  source_channel?: string | undefined;
  source_chat_id?: string | undefined;
};
type TaskUpdateRequest = Partial<{
  title: string;
  description: string;
  prompt: string;
  status: "inbox" | "next" | "in_progress" | "blocked" | "done" | "failed";
  agent_id: string;
  priority: number;
  blocked_by: Array<string>;
  todos: Array<Todo>;
  trigger: TaskTrigger;
  due: string;
  clear_due: boolean;
  tags: Array<string>;
  plan_id: string;
  write_set: Array<string>;
  stream: string;
  is_join: boolean;
  criteria: Array<AcceptanceCriterion>;
  max_attempts: number | null;
  surface: "user" | "heartbeat";
  result: string;
  artifacts: Array<string>;
  started_at: string;
  completed_at: string;
}>;
type TaskOccurrenceSet = {
  task_id: string;
  occurrences_ms: Array<number>;
  day_buckets: Array<DayBucket>;
  occurrence_runs?:
    | Array<{
        occurrence_ms: number;
        status: "in_progress" | "done" | "failed" | "skipped";
        run_id: string;
        session_id: string;
        has_result: boolean;
      }>
    | undefined;
  truncated: boolean;
};
type DayBucket = {
  day_start_ms: number;
  day_end_ms: number;
  count: number;
  first_ms: number;
  interval_ms: number | null;
  run_counts?:
    | {
        scheduled: number;
        in_progress: number;
        done: number;
        failed: number;
        skipped: number;
      }
    | undefined;
};
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
type CliDetect = {
  claude: CliDetectEntry;
  codex: CliDetectEntry;
  opencode: CliDetectEntry;
};
type CliDetectEntry = {
  installed: boolean;
  path?: (string | null) | undefined;
  source?: ("path" | "well-known" | null) | undefined;
};
type CliValidateRequest = {
  cli: ExternalCliTool;
  cli_path: string;
};
type Schedule = {
  id: string;
  name: string;
  enabled: boolean;
  owner_agent_id: string;
  created_by?: string | undefined;
  trigger: ScheduleTrigger;
  message: string;
  session_mode: "isolated" | "continue" | "main";
  timeout_seconds: number;
  session_id?: string | undefined;
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
  session_mode?: ("isolated" | "continue" | "main") | undefined;
  timeout_seconds?: number | undefined;
  enabled?: boolean | undefined;
};
type ScheduleUpdate = Partial<{
  name: string;
  owner_agent_id: string;
  trigger: ScheduleTrigger;
  message: string;
  session_mode: "isolated" | "continue" | "main";
  timeout_seconds: number;
  enabled: boolean;
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
  type: "schedule_failed" | "knowledge_drift";
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
type Workspace = {
  id: string;
  name: string;
  description?: string | undefined;
  status: "active" | "archived";
  pinned: boolean;
  pin_order: number;
  core_team?: Array<string> | undefined;
  mounts?:
    | Array<{
        name: string;
        host_path: string;
        status?: ("ok" | "broken") | undefined;
      }>
    | undefined;
  task_count: number;
  is_default?: boolean | undefined;
  setup_pending?: boolean | undefined;
  created_at: string;
  updated_at: string;
  owner?: string | undefined;
  member_configs?: {} | undefined;
};
type WorkspaceMemberConfig = Partial<{
  heartbeat: WorkspaceMemberHeartbeat;
}>;
type WorkspaceMemberHeartbeat = Partial<{
  enabled: boolean;
  interval_minutes: number;
  body: string;
  session_id: string;
}>;
type MemorySettings = Partial<{
  auto_recap_enabled: boolean;
  idle_timeout_minutes: number;
  bootstrap_recap_enabled: boolean;
  bootstrap_recap_max_per_minute: number;
  recap_model: string;
  recap_fallback_models: Array<FallbackModel>;
  session_days: number;
  memory_retros_days: number;
}>;
type WorkspaceUpdateRequest = Partial<{
  name: string;
  description: string;
  status: "active" | "archived";
  pinned: boolean;
  pin_order: number;
  core_team: Array<string>;
  member_configs: {};
}>;
type WorkspaceDelegation = {
  workspace_id: string;
  edges: Array<WorkspaceDelegationEdge>;
  team?: Array<string> | undefined;
  default_depth: number;
};
type WorkspaceDelegationEdge = {
  from_agent: string;
  to_agent: string;
  modes?: Array<"direct" | "task"> | undefined;
  depth?: number | undefined;
};
type WorkspaceDelegationUpdateRequest = {
  edges: Array<WorkspaceDelegationEdge>;
};
type Plan = {
  id: string;
  workspace_id: string;
  title: string;
  goal?: string | undefined;
  description?: string | undefined;
  state: "draft" | "approved" | "running" | "done" | "failed";
  plan_phase?:
    | (
        | "dispatching"
        | "judging"
        | "synthesizing"
        | "idle"
        | "awaiting_supervision"
        | "stalled"
      )
    | undefined;
  last_unmet_terminal_signature?: string | undefined;
  owner_session_id?: string | undefined;
  failed_reason?:
    | (
        | "judge_rounds_exhausted"
        | "stopped_by_user"
        | "idle_expired"
        | "budget_exhausted"
        | "dod_unreachable"
        | "supervision_unavailable"
      )
    | undefined;
  supervision?:
    | Partial<{
        wake_at: string;
        wake_error: string;
        attempts: number;
        correction_rounds: number;
        session_id: string;
      }>
    | undefined;
  source_channel?: string | undefined;
  source_chat_id?: string | undefined;
  owner_agent_id: string;
  dod?: Array<AcceptanceCriterion> | undefined;
  rationale?: string | undefined;
  bounds?:
    | Partial<{
        plan_judge_max_rounds: number;
        idle_expiry_days: number;
        supervision_turn_timeout_seconds: number;
        supervision_max_attempts: number;
      }>
    | undefined;
  judge_rounds?: number | undefined;
  active_loop?: boolean | undefined;
  paused_reason?: string | undefined;
  last_activity_at?: string | undefined;
  progress?: number | undefined;
  owner: string;
  created_by: string;
  created_at: string;
  updated_at: string;
  approved_at?: string | undefined;
  started_at?: string | undefined;
  completed_at?: string | undefined;
};
type PlanCreateRequest = {
  workspace_id: string;
  title: string;
  goal?: string | undefined;
  description?: string | undefined;
  owner_agent_id: string;
  dod?: Array<AcceptanceCriterion> | undefined;
  rationale?: string | undefined;
  bounds?:
    | Partial<{
        plan_judge_max_rounds: number;
        idle_expiry_days: number;
        supervision_turn_timeout_seconds: number;
        supervision_max_attempts: number;
      }>
    | undefined;
};
type PlanUpdateRequest = Partial<{
  title: string;
  goal: string;
  description: string;
  state: "draft" | "approved" | "running" | "done" | "failed";
  owner_agent_id: string;
  dod: Array<AcceptanceCriterion>;
  bounds: Partial<{
    plan_judge_max_rounds: number;
    idle_expiry_days: number;
    supervision_turn_timeout_seconds: number;
    supervision_max_attempts: number;
  }>;
}>;
type PlanListResponse = {
  plans: Array<Plan>;
  total: number;
};
type AgentTokenEntry = {
  agent_id: string;
  agent_name: string;
  tokens_in: number;
  tokens_out: number;
  tokens_total: number;
  tokens_cache_read?: number | undefined;
  tokens_cache_write?: number | undefined;
  by_model?: {} | undefined;
};
type TokenUsageSummary = {
  agents: Array<AgentTokenEntry>;
  period_start: string;
  period_end: string;
  tokens_cache_read?: number | undefined;
  tokens_cache_write?: number | undefined;
  by_model?: {} | undefined;
  partial?: boolean | undefined;
  partial_error_count?: number | undefined;
};
type SessionMessage =
  | SessionMessageProgress
  | SessionMessageCheckpoint
  | SessionMessageArtifact
  | SessionMessageBlocker
  | SessionMessageQuestion
  | SessionMessageDecisionRequest
  | SessionMessageError
  | SessionMessageHandback
  | SessionMessageRevisionEntry
  | SessionMessageGoalStatus
  | SessionMessageSteer
  | SessionMessageRespond;
type SessionMessageProgress = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "child_to_parent";
  kind: "progress";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  text: string;
  pct?: number | undefined;
};
type SessionMessageCheckpoint = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "child_to_parent";
  kind: "checkpoint";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  summary: string;
  result_so_far?: string | undefined;
  commit_ref?: string | undefined;
};
type SessionMessageArtifact = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "child_to_parent";
  kind: "artifact";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  paths: Array<string>;
  note?: string | undefined;
};
type SessionMessageBlocker = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "child_to_parent";
  kind: "blocker";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  text: string;
  severity: "low" | "medium" | "high";
  correlation_id?: string | undefined;
};
type SessionMessageQuestion = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "child_to_parent";
  kind: "question";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  text: string;
  wait: boolean;
  correlation_id: string;
  authority?: ("self_ok" | "owner_required") | undefined;
};
type SessionMessageDecisionRequest = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "child_to_parent";
  kind: "decision_request";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  text: string;
  options: Array<string>;
  correlation_id: string;
  authority?: ("self_ok" | "owner_required") | undefined;
};
type SessionMessageError = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "child_to_parent";
  kind: "error";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  text: string;
  fatal: boolean;
};
type SessionMessageHandback = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "child_to_parent";
  kind: "handback";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  result_so_far: string;
  artifacts: Array<string>;
  open_questions: Array<string>;
  mode: "final" | "pause";
};
type SessionMessageRevisionEntry = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "engine";
  kind: "revision_entry";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  revision: RevisionEntry;
};
type RevisionEntry = {
  revision_id: string;
  plan_id: string;
  generation: number;
  verb: "append" | "supersede" | "targeted_retry" | "abandon";
  falsified_assumption: string;
  tail_adds: Array<{
    member_id: string;
    blocked_by?: Array<string> | undefined;
  }>;
  superseded_member_id?: string | undefined;
  retried_member_id?: string | undefined;
  reason: string;
  created_at: string;
};
type SessionMessageGoalStatus = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "session_to_ui";
  kind: "goal_status";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  condition: "met" | "waiting_on_user";
  goal_id: string;
};
type SessionMessageSteer = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "parent_to_child";
  kind: "steer";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  text: string;
  correlation_id?: string | undefined;
};
type SessionMessageRespond = {
  message_id: string;
  session_id: string;
  parent_session_id?: (string | null) | undefined;
  generation?: number | undefined;
  direction: "parent_to_child";
  kind: "respond";
  depth: number;
  created_at: string;
  sender_identity: string;
  untrusted_origin: boolean;
  text: string;
  correlation_id: string;
};
type Goal = {
  goal_id: string;
  binding_kind: "session" | "task" | "plan";
  binding_id: string;
  source: "chat_compiled" | "task_explicit" | "plan_dod";
  prompt: string;
  definition?: string | undefined;
  criteria: Array<AcceptanceCriterion>;
  attempts_max: number;
  judge_rounds_max: number;
  round: number;
  state: "active" | "done" | "failed" | "cleared";
  created_at: string;
};
type PlanRestartResponse = {
  plan: Plan;
  new_session_id: string;
  generation: number;
  resumed_from?: (string | null) | undefined;
};
type DelegateActionRequest =
  | DelegateRunAction
  | DelegateStatusAction
  | DelegateInboxAction
  | DelegateInboxAckAction
  | DelegateSteerAction
  | DelegateRespondAction
  | DelegateCancelAction
  | DelegateFollowUpAction
  | DelegatePeekAction;
type DelegateRunAction = {
  action: "run";
  target_agent_id: string;
  task: string;
  label?: string | undefined;
  launch_profile: "utility" | "specialist";
  wait?: boolean | undefined;
  allow_blocking_question?: boolean | undefined;
  critical?: boolean | undefined;
  timeout_seconds?: number | undefined;
  snapshot?:
    | Partial<{
        references: Array<string>;
        notes: string;
      }>
    | undefined;
};
type DelegateStatusAction = {
  action: "status";
  session_id: string;
  task_id?: string | undefined;
};
type DelegateInboxAction = {
  action: "inbox";
  session_id: string;
  since_cursor?: string | undefined;
  max?: number | undefined;
};
type DelegateInboxAckAction = {
  action: "inbox_ack";
  session_id: string;
  message_ids: Array<string>;
};
type DelegateSteerAction = {
  action: "steer";
  session_id: string;
  text: string;
  correlation_id?: string | undefined;
};
type DelegateRespondAction = {
  action: "respond";
  session_id: string;
  text: string;
  correlation_id: string;
};
type DelegateCancelAction = {
  action: "cancel";
  session_id: string;
  hard?: boolean | undefined;
};
type DelegateFollowUpAction = {
  action: "follow_up";
  session_id: string;
  task?: string | undefined;
};
type DelegatePeekAction = {
  action: "peek";
  session_id: string;
};
type DelegateStatusResponse = {
  session: SessionLifecycleRecord;
  last_checkpoint?:
    | Partial<{
        summary: string;
        result_so_far: string;
        commit_ref: string;
        created_at: string;
      }>
    | undefined;
  last_progress?:
    | Partial<{
        text: string;
        pct: number;
        created_at: string;
      }>
    | undefined;
  unacked_count: number;
};
type SessionLifecycleRecord = {
  session_id: string;
  generation: number;
  resumed_from?: (string | null) | undefined;
  state:
    | "queued"
    | "running"
    | "needs_input"
    | "paused"
    | "completed"
    | "failed"
    | "cancelled"
    | "timed_out";
  terminal: boolean;
  owner_scope_kind: "parent_session" | "plan" | "human";
  owner_scope_id?: string | undefined;
  owns_plan_id?: string | undefined;
  goal_ref?: string | undefined;
  workspace_id: string;
  agent_id: string;
  is_3p: boolean;
  launch_profile: "utility" | "specialist";
  last_checkpoint_ref?: string | undefined;
  undelivered_message_ids: Array<string>;
  needs_input?:
    | {
        correlation_id: string;
        ttl_deadline: string;
        reconstructable: boolean;
      }
    | undefined;
  failed_reason?: string | undefined;
  created_at: string;
  updated_at: string;
};
type DelegateInboxResponse = {
  messages: Array<SessionMessage>;
  has_more: boolean;
  next_cursor?: string | undefined;
};
type DelegateRespondResponse = {
  acknowledged: boolean;
  corrective_session?: DelegateSessionResponse | undefined;
};
type DelegateSessionResponse = {
  session_id: string;
  generation: number;
  resumed_from?: (string | null) | undefined;
  is_3p: boolean;
  state:
    | "queued"
    | "running"
    | "needs_input"
    | "paused"
    | "completed"
    | "failed"
    | "cancelled"
    | "timed_out";
};
type MessageParentRequest =
  | MessageParentProgress
  | MessageParentCheckpoint
  | MessageParentArtifact
  | MessageParentBlocker
  | MessageParentQuestion
  | MessageParentHandback;
type MessageParentProgress = {
  kind: "progress";
  message_id?: string | undefined;
  text: string;
  pct?: number | undefined;
};
type MessageParentCheckpoint = {
  kind: "checkpoint";
  message_id?: string | undefined;
  summary: string;
  result_so_far?: string | undefined;
  commit_ref?: string | undefined;
};
type MessageParentArtifact = {
  kind: "artifact";
  message_id?: string | undefined;
  paths: Array<string>;
  note?: string | undefined;
};
type MessageParentBlocker = {
  kind: "blocker";
  message_id?: string | undefined;
  text: string;
  severity: "low" | "medium" | "high";
};
type MessageParentQuestion = {
  kind: "question";
  message_id?: string | undefined;
  text: string;
  wait: boolean;
  authority?: ("self_ok" | "owner_required") | undefined;
  correlation_id?: string | undefined;
};
type MessageParentHandback = {
  kind: "handback";
  message_id?: string | undefined;
  result_so_far: string;
  artifacts?: Array<string> | undefined;
  open_questions?: Array<string> | undefined;
  mode: "final" | "pause";
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
export const BrowserInspectRequest = z.object({
  session_id: z.string().min(1).max(128),
  agent_id: z.string().min(1).max(128),
  x: z.number().gte(0),
  y: z.number().gte(0),
});
export const BrowserInspectResponse = z.object({
  ok: z.boolean(),
  tag: z.string().max(64).optional(),
  text: z.string().max(8192).optional(),
  html: z.string().max(16384).optional(),
  reason: z.string().max(256).optional(),
});
export const ValidateTokenResponse = z
  .object({ username: z.string() })
  .passthrough();
export const ChangePasswordRequest = z.object({
  current_password: z.string().min(1).max(72),
  new_password: z.string().min(8).max(72),
});
export const ProviderValidation: z.ZodType<ProviderValidation> = z.object({
  outcome: z.enum([
    "valid",
    "invalid_key",
    "no_credit",
    "unreachable",
    "restricted",
  ]),
  message: z.string().optional(),
});
export const OperationResult: z.ZodType<OperationResult> = z.object({
  success: z.boolean(),
  error: z.string().optional(),
  validation: ProviderValidation.optional(),
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
      endpoint: z.string().optional(),
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
    "z-ai",
    "zai",
    "z-ai-coding",
    "glm-coding",
    "zhipu-coding",
    "z-ai-anthropic",
    "zhipu-anthropic",
    "moonshot-anthropic",
    "moonshot-cn-anthropic",
    "minimax-anthropic",
    "minimax-cn-anthropic",
    "deepseek-anthropic",
    "nvidia",
    "moonshot",
    "moonshot-cn",
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
    "minimax-cn",
    "coding-plan-anthropic",
    "alibaba-coding-anthropic",
    "antigravity",
    "claude-cli",
    "claudecli",
    "codex-cli",
    "codexcli",
  ]),
  api_key: z.string().min(1),
  endpoint: z.string().optional(),
});
export const ProbeProviderResponse: z.ZodType<ProbeProviderResponse> = z
  .object({
    success: z.boolean(),
    models: z.array(z.string()).optional(),
    error: z.string().optional(),
    validation: ProviderValidation.optional(),
  })
  .passthrough();
export const ModelTokens: z.ZodType<ModelTokens> = z
  .object({
    in: z.number().int().gte(0).optional(),
    out: z.number().int().gte(0).optional(),
    cache_read: z.number().int().gte(0).optional(),
    cache_write: z.number().int().gte(0).optional(),
    total: z.number().int().gte(0),
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
    tokens_cache_read: z.number().int().gte(0).optional(),
    tokens_cache_write: z.number().int().gte(0).optional(),
    by_model: z.record(ModelTokens).optional(),
  })
  .passthrough();
export const Session: z.ZodType<Session> = z.object({
  id: z.string(),
  type: z
    .enum([
      "chat",
      "task",
      "channel",
      "scheduled",
      "heartbeat",
      "verifier",
      "delegate",
    ])
    .optional(),
  protected: z.boolean().optional(),
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
  parent_session_id: z.string().optional(),
  child_count: z.number().int().gte(0).optional(),
});
export const SessionPage: z.ZodType<SessionPage> = z.object({
  sessions: z.array(Session),
  next_cursor: z.string().optional(),
  partial_errors: z.array(z.string()).optional(),
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
    "interrupted",
    "parked",
  ]),
  duration_ms: z.number().int().gte(0).optional(),
  error: z.string().optional(),
  parameters: z.object({}).partial().passthrough().optional(),
  result: z.object({}).partial().passthrough().optional(),
  parent_tool_call_id: z.string().optional(),
});
export const CriterionVerdict: z.ZodType<CriterionVerdict> = z.object({
  criterion_id: z.string().min(1),
  met: z.boolean(),
  reason: z.string(),
});
export const JudgeVerdict: z.ZodType<JudgeVerdict> = z.object({
  id: z.string(),
  scope: z.enum(["task", "plan", "goal"]),
  task_id: z.string().optional(),
  plan_id: z.string().optional(),
  round: z.number().int().gte(1),
  met: z.boolean(),
  per_criterion: z.array(CriterionVerdict),
  model: z.string(),
  judged_at: z.string().datetime({ offset: true }),
  judge_agent_id: z.string(),
});
export const Message: z.ZodType<Message> = z.object({
  id: z.string(),
  type: z
    .enum([
      "message",
      "compaction",
      "system",
      "tool_call",
      "turn_canceled",
      "judge_verdict",
    ])
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
  verdict: JudgeVerdict.optional(),
});
export const SessionDetail: z.ZodType<SessionDetail> = z.object({
  session: Session,
  messages: z.array(Message).max(100000),
  agent_removed: z.boolean().optional(),
});
export const SessionRenameRequest = z.object({
  title: z.string().min(1).max(256),
});
export const AgentToolsCfg: z.ZodType<AgentToolsCfg> = z
  .object({
    builtin: z
      .object({ policies: z.record(z.enum(["allow", "ask", "deny"])) })
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
export const ExternalCliTool = z.enum(["claude-code", "codex", "opencode"]);
export const ExecutorConfig: z.ZodType<ExecutorConfig> = z
  .object({
    kind: z.enum(["native", "external-cli", "remote-a2a"]),
    cli: ExternalCliTool,
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
    warning: z.string().optional(),
    timeout_seconds: z.number().int().gte(0),
    max_tool_iterations: z.number().int().gte(0),
    tools_cfg: AgentToolsCfg.optional(),
    shell_policy: AgentShellPolicy.optional(),
    fallback_models: z.array(FallbackModel).max(2).optional(),
    model_params: AgentModelParams.optional(),
    rate_limits: AgentRateLimits.optional(),
    stats: AgentStats.optional(),
    default: z.boolean().optional(),
    skills: z.array(z.string()).optional(),
    updated_at: z.string().datetime({ offset: true }).optional(),
    voice: z.string().nullish(),
    executor: ExecutorConfig.optional(),
    memory_enabled: z.boolean().optional().default(true),
  })
  .passthrough();
export const AgentCreateRequestMain =
  z.object({
    type: z.literal("Main"),
    name: z.string().min(1),
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
    voice: z.string().nullish(),
    shell_policy: AgentShellPolicy.optional(),
    timeout_seconds: z.number().int().gte(0).optional(),
    max_tool_iterations: z.number().int().gte(0).optional(),
  }).strict() satisfies z.ZodType<AgentCreateRequestMain>;
export const AgentCreateRequestSubagent =
  z.object({
    type: z.literal("Subagent"),
    name: z.string().min(1),
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
    shell_policy: AgentShellPolicy.optional(),
    timeout_seconds: z.number().int().gte(0).optional(),
    max_tool_iterations: z.number().int().gte(0).optional(),
  }).strict() satisfies z.ZodType<AgentCreateRequestSubagent>;
export const AgentCreateRequestSubagent3p =
  z.object({
    type: z.literal("subagent_3p"),
    name: z.string().min(1),
    description: z.string().optional(),
    model: z.string().optional(),
    provider: z.string().max(64).optional(),
    color: z
      .string()
      .regex(/^#[0-9A-Fa-f]{6}$/)
      .optional(),
    icon: z.string().max(50).optional(),
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
    soul: z.string().min(1),
    executor: ExecutorConfig,
    timeout_seconds: z.number().int().gte(0).optional(),
  }).strict() satisfies z.ZodType<AgentCreateRequestSubagent3p>;
export const AgentCreateRequest =
  z.discriminatedUnion("type", [
    AgentCreateRequestMain,
    AgentCreateRequestSubagent,
    AgentCreateRequestSubagent3p,
  ]) satisfies z.ZodType<AgentCreateRequest>;
export const AgentUpdateRequest: z.ZodType<AgentUpdateRequest> = z
  .object({
    updated_at: z.string().datetime({ offset: true }),
    name: z.string().min(1),
    description: z.string(),
    model: z.string(),
    provider: z.string().max(64),
    soul: z.string().min(1),
    heartbeat: z.string(),
    timeout_seconds: z.number().int(),
    max_tool_iterations: z.number().int(),
    heartbeat_enabled: z.boolean(),
    heartbeat_interval: z.number().int(),
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
    voice: z.string().nullable(),
    executor: ExecutorConfig,
    memory_enabled: z.boolean(),
  })
  .partial();
export const AgentToolEntry: z.ZodType<AgentToolEntry> = z
  .object({
    name: z.string(),
    configured_policy: z.enum(["allow", "ask", "deny"]),
    effective_policy: z.enum(["allow", "ask", "deny"]),
    manifest_tier: z.enum(["full", "compressed", "infra"]),
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
        policies: z.record(z.enum(["allow", "ask", "deny"])),
        mode: z.enum(["explicit", "inherit"]).optional(),
        visible: z.array(z.string()).optional(),
      })
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
export const ExecutorDefaults: z.ZodType<ExecutorDefaults> = z.object({
  cli: ExternalCliTool,
  auto_applied_flags: z.array(z.string()),
  notes: z.string(),
});
export const ExecutorCommandPreviewRequest: z.ZodType<ExecutorCommandPreviewRequest> =
  z.object({
    cli: ExternalCliTool,
    model: z.string().max(256).optional(),
    cli_path: z.string().max(4096).optional(),
    cli_args: z.string().max(4096).optional(),
    max_tool_iterations: z.number().int().gte(0).optional(),
  });
export const ExecutorCommandPreviewResponse = z.object({
  binary: z.string(),
  argv: z.array(z.string()),
  command_line: z.string(),
  prompt_delivery: z.enum(["stdin", "positional argument after --"]),
  model_dropped_reason: z.string().optional(),
  dropped_args: z.array(z.object({ flag: z.string(), reason: z.string() })),
});
export const ExecutorSmokeTestRequest: z.ZodType<ExecutorSmokeTestRequest> =
  z.object({
    cli: ExternalCliTool,
    agent_id: z.string().max(128).optional(),
    model: z.string().max(256).optional(),
    cli_path: z.string().max(4096).optional(),
    cli_args: z.string().max(4096).optional(),
  });
export const ExecutorSmokeTestResponse = z.object({
  ok: z.boolean(),
  response_text: z.string().optional(),
  error: z.string().optional(),
  duration_ms: z.number().int(),
  used_agent_workspace: z.boolean(),
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
  preview_enabled: z.boolean(),
  warmup_timeout_seconds: z.number().int().gte(0),
  frame_ancestors_fallback: z.boolean(),
  device_pairing_enabled: z.boolean(),
});
export const VersionResponse = z.object({
  version: z.string().regex(/^\d+\.\d+\.\d+(?:[-+].*)?$/),
  build_sha: z.string().regex(/^([0-9a-f]{7,40}|dev)$/),
});
export const ToolRegistryEntry = z
  .object({
    name: z.string(),
    description: z.string(),
    scope: z.enum(["core", "general"]),
    category: z.string(),
    source: z.enum(["builtin", "mcp"]),
    server_id: z.string().optional(),
  })
  .passthrough();
export const ToolApprovalActionRequest = z.object({
  action: z.enum(["approve", "deny", "cancel", "always"]),
});
export const ToolApprovalResponse = z
  .object({
    approval_id: z.string(),
    action: z.enum(["approve", "deny", "cancel", "always"]),
    status: z.literal("ok"),
    grant_recorded: z.boolean().optional(),
  })
  .passthrough();
export const GlobalToolPolicies = z.object({
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
    max_agent_llm_calls_per_hour: z.number().int().gte(0),
    max_agent_tool_calls_per_minute: z.number().int().gte(0),
  })
  .passthrough();
export const RateLimitsUpdateRequest = z
  .object({
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
    filesystem_model: z.enum(["confined", "open"]),
    applied_mode: z.string(),
    allow_network_outbound: z.boolean(),
    allowed_paths: z.array(z.string()),
    ssrf_enabled: z.boolean(),
    ssrf_allow_internal: z.array(z.string()),
    ssrf: z
      .object({ enabled: z.boolean(), allow_internal: z.array(z.string()) })
      .partial()
      .passthrough(),
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
    filesystem_model: z.enum(["confined", "open"]),
    allow_network_outbound: z.boolean(),
    allowed_paths: z.array(z.string()),
    ssrf_enabled: z.boolean(),
    ssrf_allow_internal: z.array(z.string()),
    ssrf: z
      .object({ allow_internal: z.array(z.string()) })
      .partial()
      .passthrough(),
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
    filesystem_model: z.enum(["confined", "open"]).optional(),
    disabled_by: z.string().optional(),
    landlock_enforced: z.boolean().optional(),
    seccomp_enforced: z.boolean().optional(),
    audit_only: z.boolean().optional(),
    bind_ports_count: z.number().int().gte(0),
  })
  .passthrough();
export const AuditEntry: z.ZodType<AuditEntry> = z
  .object({
    timestamp: z.string().datetime({ offset: true }),
    event: z.string().regex(/^[a-z_]+$/),
    decision: z.enum(["allow", "deny", "error"]).optional(),
    agent_id: z.string().optional(),
    session_id: z.string().optional(),
    user: z.string().optional(),
    tool: z.string().optional(),
    command: z.string().optional(),
    parameters: z.object({}).partial().passthrough().optional(),
    policy_rule: z.string().optional(),
    details: z.object({}).partial().passthrough().optional(),
    actor: z.string().optional(),
    resource: z.string().optional(),
    old_value: z.object({}).partial().passthrough().optional(),
    new_value: z.object({}).partial().passthrough().optional(),
  })
  .passthrough();
export const AuditLogResponse: z.ZodType<AuditLogResponse> = z.object({
  entries: z.array(AuditEntry),
  chain_status: z.enum(["valid", "broken", "unknown"]),
  chain_broken_index: z.number().int().optional(),
});
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
    max_parallel_agents: z.number().int().gte(1),
    effective_max_parallel_agents: z.number().int().gte(1),
    tools_on_demand: z.boolean(),
  })
  .partial();
export const PerformanceSettingsUpdate = z
  .object({
    max_parallel_agents: z.number().int().gte(0),
    tools_on_demand: z.boolean(),
  })
  .partial();
export const MemorySettings: z.ZodType<MemorySettings> = z
  .object({
    auto_recap_enabled: z.boolean(),
    idle_timeout_minutes: z.number().int(),
    bootstrap_recap_enabled: z.boolean(),
    bootstrap_recap_max_per_minute: z.number().int(),
    recap_model: z.string().max(256),
    recap_fallback_models: z.array(FallbackModel),
    session_days: z.number().int(),
    memory_retros_days: z.number().int(),
  })
  .partial();
export const ChannelId = z.string();
export const ChannelIdentity: z.ZodType<ChannelIdentity> = z.object({
  kind: z.enum(["agent", "user"]),
  id: z.string().optional(),
});
export const ChannelEntry: z.ZodType<ChannelEntry> = z.object({
  id: ChannelId.regex(/^[a-z0-9-]+(\.[a-z0-9-]+)?$/),
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
export const ChannelCreateRequest = z.object({
  type: z.string(),
  slug: z.string().regex(/^[a-z0-9-]{1,32}$/),
});
export const ChannelCreateResponse = z.object({
  id: z.string(),
  type: z.string(),
  enabled: z.boolean(),
});
export const ChannelEnabledResponse: z.ZodType<ChannelEnabledResponse> =
  z.object({
    id: ChannelId.regex(/^[a-z0-9-]+(\.[a-z0-9-]+)?$/),
    enabled: z.boolean(),
  });
export const ChannelTestResponse = z
  .object({ success: z.boolean(), message: z.string() })
  .passthrough();
export const ChannelRouting = z
  .object({ default_agent_id: z.string(), workspace_id: z.string() })
  .partial();
export const Mailbox: z.ZodType<Mailbox> = z.object({
  agent_id: z.string(),
  enabled: z.boolean(),
  workspace_id: z.string(),
  imap_host: z.string().optional(),
  imap_port: z.number().int().optional(),
  smtp_host: z.string().optional(),
  smtp_port: z.number().int().optional(),
  username: z.string().optional(),
  configured: z.boolean(),
});
export const MailboxConfigureRequest = z.object({
  enabled: z.boolean(),
  imap_host: z.string(),
  imap_port: z.number().int().optional(),
  smtp_host: z.string(),
  smtp_port: z.number().int().optional(),
  username: z.string(),
  password: z.string().optional(),
});
export const MailboxListResponse: z.ZodType<MailboxListResponse> = z.object({
  mailboxes: z.array(Mailbox),
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
  persisted: z.boolean(),
  available: z.boolean(),
  supported: z.boolean(),
});
export const GodModeUpdateRequest = z.object({ enabled: z.boolean() });
export const GodModeUpdateResponse = z.object({
  enabled: z.boolean(),
  restart_required: z.boolean(),
});
export const CredentialSetRequest = z.object({
  key: z.string(),
  value: z.string(),
});
export const CredentialRotateRequest = z.object({
  new_passphrase: z.string().min(1),
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
export const ClearAllSessionsResponse = z
  .object({
    status: z.literal("cleared"),
    count: z.number().int().gte(0),
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
export const Provider: z.ZodType<Provider> = z.object({
  id: z.string(),
  name: z.string(),
  display_name: z.string().optional(),
  status: z.enum(["connected", "disconnected", "error"]),
  models: z.array(z.string()),
  has_models_endpoint: z.boolean().optional(),
  has_api_key: z.boolean().optional(),
  warning: z.string().optional(),
  error: z.string().optional(),
  validation: ProviderValidation.optional(),
});
export const ProviderUpdateRequest = z
  .object({
    api_key: z.string(),
    model: z.string(),
    models: z.array(z.string().min(1).max(256)).max(500),
  })
  .partial();
export const ModelCapabilities = z.object({
  id: z.string(),
  modalities: z.array(z.enum(["text", "image", "pdf", "audio", "video"])),
});
export const SlashCommand = z.object({
  name: z.string(),
  label: z.string(),
  description: z.string(),
  usage: z.string().optional(),
  argument_hint: z.string().optional(),
  aliases: z.array(z.string()).optional(),
  available_while_streaming: z.boolean().optional(),
  delivery: z.enum(["client", "agent"]),
});
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
  argument_hint: z.string().optional(),
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
  status: z.enum(["pending", "in_progress", "completed"]),
});
export const AcceptanceCriterion: z.ZodType<AcceptanceCriterion> = z.object({
  id: z.string().optional(),
  kind: z.enum(["check", "prose", "behavior"]),
  text: z.string().min(1).max(1000),
  check: z
    .object({
      command: z.string().min(1),
      expected_exit_code: z.number().int().gte(0).lte(255),
    })
    .optional(),
  behavior: z
    .object({
      tool: z.string().min(1),
      min_count: z.number().int().gte(0).optional().default(1),
      max_count: z.number().int().gte(0).optional(),
      scope: z
        .enum(["attempt", "task_session"])
        .optional()
        .default("task_session"),
    })
    .optional(),
  author: z.object({ kind: z.enum(["agent", "user"]), id: z.string().min(1) }),
  status: z.enum(["pending", "met", "unmet"]),
});
export const TaskTrigger: z.ZodType<TaskTrigger> = z.object({
  type: z.enum(["manual", "once", "every", "recurring"]),
  config: z
    .object({
      at_ms: z.number().int(),
      every_ms: z.number().int().gte(1000),
      cron_expr: z.string(),
      rrule: z.string().max(512),
      dtstart_ms: z.number().int(),
      tz: z.string(),
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
      "in_progress",
      "blocked",
      "done",
      "failed",
    ]),
    agent_id: z.string().optional(),
    cancel_reason: z.literal("stopped_by_user").nullish(),
    agent_name: z.string().optional(),
    priority: z.number().int().gte(1).lte(5).optional().default(3),
    blocked_by: z.array(z.string()).optional(),
    todos: z.array(Todo).optional(),
    parent_task_id: z.string().optional(),
    workspace_id: z.string(),
    tags: z.array(z.string().max(64)).max(16).optional(),
    plan_id: z.string().optional(),
    write_set: z.array(z.string()).optional(),
    stream: z.string().optional(),
    is_join: z.boolean().optional(),
    judge_rounds: z.number().int().gte(0).optional(),
    criteria: z.array(AcceptanceCriterion).optional(),
    attempt_count: z.number().int().gte(0).optional(),
    max_attempts: z.number().int().gte(1).nullish(),
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
  tags: z.array(z.string().max(64)).max(16).optional(),
  plan_id: z.string().optional(),
  write_set: z.array(z.string()).optional(),
  stream: z.string().optional(),
  is_join: z.boolean().optional(),
  criteria: z.array(AcceptanceCriterion).optional(),
  max_attempts: z.number().int().gte(1).nullish(),
  due: z.string().datetime({ offset: true }).optional(),
  surface: z.enum(["user", "heartbeat"]).optional().default("user"),
  source_channel: z.string().optional(),
  source_chat_id: z.string().optional(),
});
export const DayBucket: z.ZodType<DayBucket> = z.object({
  day_start_ms: z.number().int(),
  day_end_ms: z.number().int(),
  count: z.number().int(),
  first_ms: z.number().int(),
  interval_ms: z.number().int().nullable(),
  run_counts: z
    .object({
      scheduled: z.number().int(),
      in_progress: z.number().int(),
      done: z.number().int(),
      failed: z.number().int(),
      skipped: z.number().int(),
    })
    .optional(),
});
export const TaskOccurrenceSet: z.ZodType<TaskOccurrenceSet> = z.object({
  task_id: z.string(),
  occurrences_ms: z.array(z.number().int()),
  day_buckets: z.array(DayBucket),
  occurrence_runs: z
    .array(
      z.object({
        occurrence_ms: z.number().int(),
        status: z.enum(["in_progress", "done", "failed", "skipped"]),
        run_id: z.string(),
        session_id: z.string(),
        has_result: z.boolean(),
      })
    )
    .optional(),
  truncated: z.boolean(),
});
export const TaskUpdateRequest: z.ZodType<TaskUpdateRequest> = z
  .object({
    title: z.string().min(1).max(200),
    description: z.string().max(2000),
    prompt: z.string().max(10000),
    status: z.enum([
      "inbox",
      "next",
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
    tags: z.array(z.string().max(64)).max(16),
    plan_id: z.string(),
    write_set: z.array(z.string()),
    stream: z.string(),
    is_join: z.boolean(),
    criteria: z.array(AcceptanceCriterion),
    max_attempts: z.number().int().gte(1).nullable(),
    surface: z.enum(["user", "heartbeat"]),
    result: z.string().max(50000),
    artifacts: z.array(z.string()),
    started_at: z.string().datetime({ offset: true }),
    completed_at: z.string().datetime({ offset: true }),
  })
  .partial();
export const EvidenceRecord = z.object({
  id: z.string(),
  task_id: z.string(),
  criterion_id: z.string(),
  attempt: z.number().int().gte(1),
  command: z.string(),
  exit_code: z.number().int(),
  output: z.string(),
  truncated: z.boolean(),
  timed_out: z.boolean(),
  policy_denied: z.boolean(),
  recorded_at: z.string().datetime({ offset: true }),
});
export const TaskRun = z.object({
  run_id: z.string(),
  task_id: z.string(),
  occurrence_ms: z.number().int().nullable(),
  status: z.enum(["in_progress", "done", "failed", "skipped"]),
  result: z.string().max(50000).optional(),
  session_id: z.string(),
  kind: z.enum(["scheduled", "manual"]),
  started_at: z.string().datetime({ offset: true }),
  ended_at: z.string().datetime({ offset: true }).nullable(),
});
export const RunNowRequest = z
  .object({ occurrence_ms: z.number().int().nullable() })
  .partial();
export const McpServer = z
  .object({
    id: z.string(),
    name: z.string(),
    transport: z.enum(["stdio", "sse", "http"]),
    status: z.enum(["connected", "disconnected", "error"]),
    tool_count: z.number().int().gte(0),
    tools: z.array(z.string()).optional(),
    enabled: z.boolean().optional(),
    command: z.string().optional(),
    url: z.string().optional(),
    args: z.array(z.string()).optional(),
    env_file: z.string().optional(),
    env_keys: z.array(z.string()).optional(),
    header_names: z.array(z.string()).optional(),
  })
  .passthrough();
export const McpServerCreate = z.object({
  name: z.string(),
  command: z.string().optional(),
  url: z.string().optional(),
  args: z.array(z.string()).optional(),
  transport: z.enum(["stdio", "sse", "http"]),
  env: z.record(z.string()).optional(),
  headers: z.record(z.string()).optional(),
  env_file: z.string().optional(),
});
export const McpServerUpdate = z
  .object({
    enabled: z.boolean(),
    command: z.string(),
    url: z.string(),
    args: z.array(z.string()),
    env: z.record(z.string()),
    headers: z.record(z.string()),
    env_file: z.string(),
  })
  .partial();
export const McpServerTestResponse = z.object({
  success: z.boolean(),
  message: z.string(),
  tool_count: z.number().int().gte(0).optional(),
  tools: z.array(z.string()).optional(),
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
  session_mode: z.enum(["isolated", "continue", "main"]),
  timeout_seconds: z.number().int(),
  session_id: z.string().optional(),
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
  session_mode: z.enum(["isolated", "continue", "main"]).optional(),
  timeout_seconds: z.number().int().gte(0).optional(),
  enabled: z.boolean().optional(),
});
export const ScheduleUpdate: z.ZodType<ScheduleUpdate> = z
  .object({
    name: z.string().min(1),
    owner_agent_id: z.string().min(1),
    trigger: ScheduleTrigger,
    message: z.string().min(1),
    session_mode: z.enum(["isolated", "continue", "main"]),
    timeout_seconds: z.number().int().gte(0),
    enabled: z.boolean(),
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
  type: z.enum(["schedule_failed", "knowledge_drift"]),
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
export const WorkspaceMemberHeartbeat: z.ZodType<WorkspaceMemberHeartbeat> = z
  .object({
    enabled: z.boolean(),
    interval_minutes: z.number().int().gte(5),
    body: z.string().max(16384),
    session_id: z.string(),
  })
  .partial();
export const WorkspaceMemberConfig: z.ZodType<WorkspaceMemberConfig> = z
  .object({ heartbeat: WorkspaceMemberHeartbeat })
  .partial();
export const Workspace: z.ZodType<Workspace> = z
  .object({
    id: z.string(),
    name: z.string().min(1),
    description: z.string().optional(),
    status: z.enum(["active", "archived"]),
    pinned: z.boolean(),
    pin_order: z.number().int(),
    core_team: z.array(z.string()).optional(),
    mounts: z
      .array(
        z.object({
          name: z.string().min(1),
          host_path: z.string().min(1),
          status: z.enum(["ok", "broken"]).optional(),
        })
      )
      .optional(),
    task_count: z.number().int(),
    is_default: z.boolean().optional(),
    setup_pending: z.boolean().optional(),
    created_at: z.string().datetime({ offset: true }),
    updated_at: z.string().datetime({ offset: true }),
    owner: z.string().optional(),
    member_configs: z.record(WorkspaceMemberConfig).optional(),
  })
  .passthrough();
export const WorkspaceCreateRequest = z
  .object({
    name: z.string().min(1).max(200),
    description: z.string().max(2000).optional(),
    core_team: z.array(z.string()).optional(),
  })
  .passthrough();
export const WorkspaceUpdateRequest: z.ZodType<WorkspaceUpdateRequest> = z
  .object({
    name: z.string().min(1).max(200),
    description: z.string().max(2000),
    status: z.enum(["active", "archived"]),
    pinned: z.boolean(),
    pin_order: z.number().int(),
    core_team: z.array(z.string()),
    member_configs: z.record(WorkspaceMemberConfig),
  })
  .partial()
  .passthrough();
export const MediaLibraryEntry = z.object({
  id: z.string().uuid(),
  workspace_id: z.string().min(1).max(64),
  filename: z.string().min(1).max(256),
  mime: z.string(),
  size: z.number().int().gte(0).lte(104857600),
  sha256: z.string().regex(/^[a-f0-9]{64}$/),
  uploaded_at: z.string().datetime({ offset: true }),
  source: z.enum(["user_upload", "tool_output"]),
  refcount: z.number().int().gte(0).optional(),
  last_refcount_seen_at: z.string().datetime({ offset: true }).optional(),
  status: z.enum(["available", "stranded"]),
}).strict();
export const MediaAttachmentRequest = z.object({
  media_id: z.string().max(36).uuid(),
}).strict();
export const LibraryWorkspaceNode = z.object({
  id: z.string(),
  name: z.string(),
  entry_count: z.number().int().gte(0),
});
export const LibraryTransferRequest = z.object({
  from_workspace_id: z.string(),
  from_path: z.string().min(1),
  to_workspace_id: z.string(),
  to_path: z.string().min(1),
});
export const LibraryEntryMount: z.ZodType<LibraryEntryMount> = z.object({
  name: z.string().min(1),
  host_path: z.string().min(1),
  broad: z.boolean(),
});
export const LibraryEntry: z.ZodType<LibraryEntry> = z.object({
  name: z.string().min(1),
  path: z.string().min(1),
  is_dir: z.boolean(),
  is_hidden: z.boolean(),
  size: z.number().int().gte(0),
  modified_at: z.string().datetime({ offset: true }),
  mime: z.string().optional(),
  mount: LibraryEntryMount.optional(),
  is_text_editable: z.boolean(),
});
export const LibraryContentResponse = z.object({
  path: z.string(),
  content: z.string().optional(),
  size: z.number().int().gte(0),
  is_text: z.boolean(),
  too_large: z.boolean(),
  mime: z.string().optional(),
});
export const LibraryContentRequest = z.object({
  path: z.string().min(1),
  content: z.string().max(10485760),
});
export const uploadLibraryFiles_Body = z
  .object({ files: z.array(z.instanceof(File)) })
  .partial()
  .passthrough();
export const LibraryUploadResponse: z.ZodType<LibraryUploadResponse> = z.object(
  { entries: z.array(LibraryEntry) }
);
export const LibraryMkdirRequest = z.object({ path: z.string().min(1) });
export const LibraryRenameRequest = z.object({
  from: z.string().min(1),
  to: z.string().min(1),
});
export const LibraryPreviewTokenRequest = z.object({
  workspace_id: z.string().min(1),
  path: z.string().min(1),
  scope: z.enum(["file", "bundle"]),
  entry_path: z.string().optional(),
});
export const LibraryPreviewTokenResponse = z.object({
  token: z.string().min(43).max(43),
  url: z.string().min(1),
  expires_at: z.string().datetime({ offset: true }),
  expires_in_seconds: z.number().int().gte(1),
  scope: z.enum(["file", "bundle"]),
  scope_root: z.string().min(1),
  workspace_id: z.string().min(1).optional(),
});
export const LibraryInlineDisposition = z.object({
  path: z.string().min(1),
  extension: z.string(),
  disposition: z.enum(["inline", "attachment"]),
  content_type: z.string().min(1),
  renderer: z.enum([
    "html",
    "pdf",
    "audio",
    "video",
    "image",
    "markdown",
    "text",
    "code",
    "none",
  ]),
  requires_sandbox: z.boolean(),
  reason: z.string().optional(),
});
export const KnowledgeBaseInfo = z.object({
  workspace_id: z.string().min(1),
  root_path: z.string().min(1),
  is_knowledge_base: z.boolean(),
  marker: z.enum(["omnipus_vault", "obsidian", "none"]),
  collection_id: z.string().min(1).optional(),
  display_name: z.string().optional(),
  template_path: z.string().optional(),
  detection_error: z
    .object({
      code: z.enum([
        "marker_unreadable",
        "root_unreadable",
        "root_missing",
        "not_a_directory",
      ]),
      message: z.string().min(1),
    })
    .optional(),
});
export const KnowledgeSearchRequest = z.object({
  query: z.string().min(1).max(1024),
  collection_id: z.string().min(1),
  limit: z.number().int().gte(1).optional().default(20),
  offset: z.number().int().gte(0).optional().default(0),
  kinds: z.array(z.enum(["note", "attachment"])).optional(),
});
export const KnowledgeSearchHit: z.ZodType<KnowledgeSearchHit> = z.object({
  path: z.string().min(1),
  title: z.string(),
  score: z.number(),
  kind: z.enum(["note", "attachment"]),
  excerpt: z.string().optional(),
  excerpt_unavailable: z
    .enum([
      "file_unreadable",
      "file_missing",
      "match_moved",
      "budget_exhausted",
      "attachment_not_read",
    ])
    .optional(),
  byte_offset: z.number().int().gte(0).optional(),
});
export const KnowledgeSearchIncompleteness: z.ZodType<KnowledgeSearchIncompleteness> =
  z.object({
    complete: z.boolean(),
    total_known: z.boolean(),
    statement: z.string().min(1),
    indexed_files: z.number().int().gte(0).optional(),
    total_files: z.number().int().gte(0).optional(),
  });
export const KnowledgeSearchResponse: z.ZodType<KnowledgeSearchResponse> =
  z.object({
    collection_id: z.string().min(1),
    hits: z.array(KnowledgeSearchHit),
    incompleteness: KnowledgeSearchIncompleteness,
    limit_applied: z.number().int().gte(1),
    limit_clamped: z.boolean(),
    limit_requested: z.number().int().gte(1).optional(),
  });
export const KnowledgeGraphNode: z.ZodType<KnowledgeGraphNode> = z.object({
  path: z.string().min(1),
  title: z.string().optional(),
  exists: z.boolean(),
});
export const KnowledgeGraphEdge: z.ZodType<KnowledgeGraphEdge> = z.object({
  from_path: z.string().min(1),
  to_path: z.string().min(1),
  link_text: z.string().optional(),
  alias: z.string().optional(),
  heading: z.string().optional(),
  resolution: z.enum([
    "exact_path",
    "unique_basename",
    "shortest_path",
    "lexicographic",
    "unresolved",
  ]),
  ambiguous: z.boolean(),
  candidates: z.array(z.string()).optional(),
  embed: z.boolean().optional(),
});
export const KnowledgeGraphSkip: z.ZodType<KnowledgeGraphSkip> = z.object({
  path: z.string().min(1),
  reason: z.enum([
    "symlink",
    "outside_root",
    "unreadable",
    "not_addressable",
    "node_limit",
    "hop_limit",
  ]),
  detail: z.string().optional(),
});
export const KnowledgeGraphResponse: z.ZodType<KnowledgeGraphResponse> =
  z.object({
    collection_id: z.string().min(1),
    kind: z.enum([
      "links",
      "backlinks",
      "unresolved",
      "orphans",
      "neighbourhood",
    ]),
    source_path: z.string().optional(),
    nodes: z.array(KnowledgeGraphNode),
    edges: z.array(KnowledgeGraphEdge),
    skipped: z.array(KnowledgeGraphSkip),
    truncated: z.boolean(),
    hop_limit_applied: z.number().int().gte(1).optional(),
    node_limit_applied: z.number().int().gte(1).optional(),
  });
export const KnowledgeOutlineHeading: z.ZodType<KnowledgeOutlineHeading> =
  z.object({
    level: z.number().int().gte(1).lte(6),
    text: z.string(),
    slug: z.string().min(1),
    line: z.number().int().gte(1).optional(),
    byte_offset: z.number().int().gte(0).optional(),
  });
export const KnowledgeOutline: z.ZodType<KnowledgeOutline> = z.object({
  path: z.string().min(1),
  is_knowledge_base: z.boolean(),
  collection_id: z.string().min(1).optional(),
  headings: z.array(KnowledgeOutlineHeading),
  frontmatter_malformed: z.boolean().optional(),
});
export const WorkspaceDelegationEdge: z.ZodType<WorkspaceDelegationEdge> =
  z.object({
    from_agent: z.string().min(1),
    to_agent: z.string().min(1),
    modes: z.array(z.enum(["direct", "task"])).optional(),
    depth: z.number().int().gte(0).optional(),
  });
export const WorkspaceDelegation: z.ZodType<WorkspaceDelegation> = z.object({
  workspace_id: z.string(),
  edges: z.array(WorkspaceDelegationEdge),
  team: z.array(z.string()).optional(),
  default_depth: z.number().int().gte(0),
});
export const WorkspaceDelegationUpdateRequest: z.ZodType<WorkspaceDelegationUpdateRequest> =
  z.object({ edges: z.array(WorkspaceDelegationEdge) });
export const WorkspaceMountCreateRequest = z.object({
  name: z.string().min(1),
  host_path: z.string().min(1),
});
export const WorkspaceMountCreateResponse = z.object({
  name: z.string().min(1),
  host_path: z.string().min(1),
  status: z.enum(["ok", "broken"]),
  warning: z.string().min(1).optional(),
});
export const WorkspaceInstructionsResponse = z.object({ content: z.string() });
export const WorkspaceInstructionsRequest = z.object({
  content: z.string().max(262144),
});
export const Plan: z.ZodType<Plan> = z.object({
  id: z.string(),
  workspace_id: z.string(),
  title: z.string().min(1).max(200),
  goal: z.string().max(2000).optional(),
  description: z.string().max(2000).optional(),
  state: z.enum(["draft", "approved", "running", "done", "failed"]),
  plan_phase: z
    .enum([
      "dispatching",
      "judging",
      "synthesizing",
      "idle",
      "awaiting_supervision",
      "stalled",
    ])
    .optional()
    .default("idle"),
  last_unmet_terminal_signature: z.string().optional(),
  owner_session_id: z.string().optional(),
  failed_reason: z
    .enum([
      "judge_rounds_exhausted",
      "stopped_by_user",
      "idle_expired",
      "budget_exhausted",
      "dod_unreachable",
      "supervision_unavailable",
    ])
    .optional(),
  supervision: z
    .object({
      wake_at: z.string().datetime({ offset: true }),
      wake_error: z.string(),
      attempts: z.number().int().gte(0),
      correction_rounds: z.number().int().gte(0),
      session_id: z.string(),
    })
    .partial()
    .optional(),
  source_channel: z.string().optional(),
  source_chat_id: z.string().optional(),
  owner_agent_id: z.string(),
  dod: z.array(AcceptanceCriterion).optional(),
  rationale: z.string().max(4000).optional(),
  bounds: z
    .object({
      plan_judge_max_rounds: z.number().int().gte(1),
      idle_expiry_days: z.number().int().gte(1),
      supervision_turn_timeout_seconds: z.number().int().gte(1),
      supervision_max_attempts: z.number().int().gte(1),
    })
    .partial()
    .optional(),
  judge_rounds: z.number().int().gte(0).optional(),
  active_loop: z.boolean().optional(),
  paused_reason: z.string().optional(),
  last_activity_at: z.string().datetime({ offset: true }).optional(),
  progress: z.number().gte(0).lte(1).optional(),
  owner: z.string(),
  created_by: z.string(),
  created_at: z.string().datetime({ offset: true }),
  updated_at: z.string().datetime({ offset: true }),
  approved_at: z.string().datetime({ offset: true }).optional(),
  started_at: z.string().datetime({ offset: true }).optional(),
  completed_at: z.string().datetime({ offset: true }).optional(),
});
export const PlanListResponse: z.ZodType<PlanListResponse> = z.object({
  plans: z.array(Plan),
  total: z.number().int(),
});
export const PlanCreateRequest: z.ZodType<PlanCreateRequest> = z.object({
  workspace_id: z.string(),
  title: z.string().min(1).max(200),
  goal: z.string().max(2000).optional(),
  description: z.string().max(2000).optional(),
  owner_agent_id: z.string().min(1),
  dod: z.array(AcceptanceCriterion).optional(),
  rationale: z.string().max(4000).optional(),
  bounds: z
    .object({
      plan_judge_max_rounds: z.number().int().gte(1),
      idle_expiry_days: z.number().int().gte(1),
      supervision_turn_timeout_seconds: z.number().int().gte(1),
      supervision_max_attempts: z.number().int().gte(1),
    })
    .partial()
    .optional(),
});
export const PlanUpdateRequest: z.ZodType<PlanUpdateRequest> = z
  .object({
    title: z.string().min(1).max(200),
    goal: z.string().max(2000),
    description: z.string().max(2000),
    state: z.enum(["draft", "approved", "running", "done", "failed"]),
    owner_agent_id: z.string().min(1),
    dod: z.array(AcceptanceCriterion),
    bounds: z
      .object({
        plan_judge_max_rounds: z.number().int().gte(1),
        idle_expiry_days: z.number().int().gte(1),
        supervision_turn_timeout_seconds: z.number().int().gte(1),
        supervision_max_attempts: z.number().int().gte(1),
      })
      .partial(),
  })
  .partial();
export const PlanApproveError = z
  .object({
    error: z.string(),
    task_errors: z.array(
      z.object({ task_id: z.string(), title: z.string(), reason: z.string() })
    ),
  })
  .partial();
export const AgentTokenEntry: z.ZodType<AgentTokenEntry> = z
  .object({
    agent_id: z.string(),
    agent_name: z.string(),
    tokens_in: z.number().int(),
    tokens_out: z.number().int(),
    tokens_total: z.number().int(),
    tokens_cache_read: z.number().int().gte(0).optional(),
    tokens_cache_write: z.number().int().gte(0).optional(),
    by_model: z.record(ModelTokens).optional(),
  })
  .passthrough();
export const TokenUsageSummary: z.ZodType<TokenUsageSummary> = z
  .object({
    agents: z.array(AgentTokenEntry),
    period_start: z.string().datetime({ offset: true }),
    period_end: z.string().datetime({ offset: true }),
    tokens_cache_read: z.number().int().gte(0).optional(),
    tokens_cache_write: z.number().int().gte(0).optional(),
    by_model: z.record(ModelTokens).optional(),
    partial: z.boolean().optional(),
    partial_error_count: z.number().int().gte(0).optional(),
  })
  .passthrough();
export const HostFolderEntry: z.ZodType<HostFolderEntry> = z.object({
  name: z.string().min(1),
  path: z.string().min(1),
  mountable: z.boolean(),
  broad: z.boolean().optional(),
  reason: z.string().optional(),
});
export const HostFolderListing: z.ZodType<HostFolderListing> = z.object({
  path: z.string().min(1),
  parent: z.string().optional(),
  entries: z.array(HostFolderEntry),
});
export const CliDetectEntry: z.ZodType<CliDetectEntry> = z.object({
  installed: z.boolean(),
  path: z.string().nullish(),
  source: z.enum(["path", "well-known"]).nullish(),
});
export const CliDetect: z.ZodType<CliDetect> = z.object({
  claude: CliDetectEntry,
  codex: CliDetectEntry,
  opencode: CliDetectEntry,
});
export const CliValidateRequest: z.ZodType<CliValidateRequest> = z.object({
  cli: ExternalCliTool,
  cli_path: z.string().max(4096),
});
export const CliValidateResponse = z.object({
  ok: z.boolean(),
  reason: z.enum([
    "ok",
    "missing-binary",
    "handshake-failed",
    "unauthenticated",
    "unknown-cli",
  ]),
  resolved_path: z.string().nullish(),
  version: z.string().nullish(),
  detail: z.string().optional(),
});
export const OnboardingCompleteResponse: z.ZodType<OnboardingCompleteResponse> =
  LoginResponse;
export const KnowledgeConflictError = z.object({
  error: z.string().min(1),
  code: z.literal("knowledge_version_conflict"),
  path: z.string().min(1),
  expected_version: z.string().optional(),
  actual_version: z.string().optional(),
});
export const KnowledgeMountConflictError = z.object({
  error: z.string().min(1),
  code: z.literal("knowledge_mount_conflict"),
  existing_root_path: z.string().min(1),
  requested_root_path: z.string().min(1),
  existing_collection_id: z.string().optional(),
});
export const EnumValueDef: z.ZodType<EnumValueDef> = z.object({
  value: z.string().min(1),
  label: z.string().optional(),
  position: z.number().int().gte(0),
  group: z.enum(["open", "done", "cancelled"]).optional(),
});
export const PropertyDef: z.ZodType<PropertyDef> = z.object({
  name: z.string().min(1),
  type: z.enum([
    "text",
    "enum",
    "relation",
    "date",
    "integer",
    "decimal",
    "person",
    "checkbox",
  ]),
  many: z.boolean(),
  required: z.boolean(),
  label: z.string().optional(),
  values: z.array(EnumValueDef).optional(),
  to: z.string().min(1).optional(),
  inverse: z.string().min(1).optional(),
  unit: z.string().optional(),
  formula: z.string().min(1).optional(),
});
export const RecordType: z.ZodType<RecordType> = z.object({
  schema_version: z.number().int().gte(1),
  type: z.string().min(1),
  label: z.string().optional(),
  identity_prefix: z.string().min(1).optional(),
  properties: z.array(PropertyDef),
  source_path: z.string().optional(),
});
export const RecordProblem: z.ZodType<RecordProblem> = z.object({
  code: z.enum([
    "missing_schema_version",
    "duplicate_type_declaration",
    "unknown_property",
    "unknown_enum_value",
    "missing_required",
    "arity_violation",
    "enum_violation",
    "type_mismatch",
    "dangling_relation",
    "relation_type_mismatch",
    "cardinality_violation",
    "duplicate_id",
    "integer_not_whole",
    "integer_out_of_range",
    "candidate_cap_exceeded",
    "hop_limit_exceeded",
    "hop_traversal_bound_exceeded",
    "page_size_clamped",
    "scope_truncated",
    "text_search_truncated",
    "aggregate_refused",
    "index_unavailable",
    "evaluation_bound_exceeded",
    "unsupported_operator",
    "unsupported_parameter",
    "empty_like_pattern",
    "empty_in_list",
    "literal_type_mismatch",
    "ordering_on_many_property",
    "comparison_undefined",
    "date_format_ambiguous",
    "decimal_scale_exceeded",
    "stale_record",
    "orphan_row",
    "stale_cursor",
    "unknown_view",
    "unknown_record_type",
  ]),
  reason: z.string().min(1),
  records: z.array(z.string().min(1)),
  property: z.string().min(1).optional(),
  expected: z.string().optional(),
  fix: z.string().optional(),
  permitted: z.array(z.string()).optional(),
  paths: z.array(z.string().min(1)).optional(),
});
export const RecordSchema: z.ZodType<RecordSchema> = z.object({
  types: z.array(RecordType),
  problems: z.array(RecordProblem),
});
export const RecordRef: z.ZodType<RecordRef> = z.object({
  link: z.string().min(1),
  resolved: z.boolean(),
  id: z.string().min(1).optional(),
  type: z.string().min(1).optional(),
  title: z.string().optional(),
});
export const RecordValue: z.ZodType<RecordValue> = z.object({
  type: z.enum([
    "text",
    "enum",
    "relation",
    "date",
    "integer",
    "decimal",
    "person",
    "checkbox",
  ]),
  text: z.string().optional(),
  enum: z.string().min(1).optional(),
  relation: RecordRef.optional(),
  date: z.string().min(8).max(40).optional(),
  integer: z
    .string()
    .min(1)
    .max(20)
    .regex(/^-?(0|[1-9][0-9]*)$/)
    .optional(),
  decimal: z
    .string()
    .min(1)
    .max(128)
    .regex(/^-?(0|[1-9][0-9]*)(\.[0-9]{1,100})?$/)
    .optional(),
  person: RecordRef.optional(),
  checkbox: z.boolean().optional(),
});
export const RecordPropertyValue: z.ZodType<RecordPropertyValue> = z.object({
  property: z.string().min(1),
  type: z
    .enum(["text", "enum", "relation", "date", "integer", "decimal", "person"])
    .optional(),
  values: z.array(RecordValue),
});
export const VaultRecord: z.ZodType<VaultRecord> = z.object({
  id: z.string().min(1),
  type: z.string().min(1),
  path: z.string().min(1),
  title: z.string().optional(),
  version_token: z.string().min(1).optional(),
  properties: z.array(RecordPropertyValue),
});
export const RecordFilter: z.ZodType<RecordFilter> = z.object({
  property: z.string().min(1),
  op: z.enum(["eq", "lt", "lte", "gt", "gte", "contains", "is_absent"]),
  values: z.array(RecordValue).optional(),
  negate: z.boolean().optional(),
  include_absent: z.boolean().optional(),
  via: z.array(z.string().min(1)).max(2).optional(),
});
export const RecordSort: z.ZodType<RecordSort> = z.object({
  property: z.string().min(1),
  direction: z.enum(["asc", "desc"]),
});
export const RecordAggregate: z.ZodType<RecordAggregate> = z.object({
  op: z.enum([
    "count",
    "sum",
    "min",
    "max",
    "avg",
    "median",
    "stddev",
    "range",
    "earliest",
    "latest",
    "checked",
    "unchecked",
    "empty",
    "filled",
    "unique",
  ]),
  property: z.string().min(1).optional(),
});
export const RecordQueryRequest: z.ZodType<RecordQueryRequest> = z.object({
  type: z.string().min(1),
  filters: z.array(RecordFilter).optional(),
  group_by: z.array(z.string().min(1)).max(2).optional(),
  sort: z.array(RecordSort).optional(),
  aggregates: z.array(RecordAggregate).optional(),
  select: z.array(z.string().min(1)).optional(),
  limit: z.number().int().gte(1).optional().default(50),
  cursor: z.string().min(1).optional(),
  hops: z.number().int().gte(0).optional().default(0),
});
export const RecordGroupKey: z.ZodType<RecordGroupKey> = z.object({
  property: z.string().min(1),
  absent: z.boolean(),
  value: RecordValue.optional(),
  label: z.string().optional(),
});
export const RecordAggregateResult: z.ZodType<RecordAggregateResult> = z.object(
  {
    op: z.enum([
      "count",
      "sum",
      "min",
      "max",
      "avg",
      "median",
      "stddev",
      "range",
      "earliest",
      "latest",
      "checked",
      "unchecked",
      "empty",
      "filled",
      "unique",
    ]),
    property: z.string().min(1).optional(),
    refused: z.boolean(),
    count: z.number().int().gte(0).optional(),
    value: RecordValue.optional(),
    excluded_records: z.number().int().gte(0).optional(),
  }
);
export const RecordGroup: z.ZodType<RecordGroup> = z.object({
  keys: z.array(RecordGroupKey).min(1).max(2),
  count: z.number().int().gte(0),
  record_ids: z.array(z.string().min(1)),
  aggregates: z.array(RecordAggregateResult).optional(),
});
export const RecordQueryResponse: z.ZodType<RecordQueryResponse> = z.object({
  records: z.array(VaultRecord),
  complete: z.boolean(),
  problems: z.array(RecordProblem),
  refused: z.boolean(),
  groups: z.array(RecordGroup).optional(),
  aggregates: z.array(RecordAggregateResult).optional(),
  limit_applied: z.number().int().gte(1),
  limit_clamped: z.boolean(),
  limit_requested: z.number().int().gte(1).optional(),
  total_matched: z.number().int().gte(0).optional(),
  next_cursor: z.string().min(1).optional(),
});
export const RecordWriteRequest: z.ZodType<RecordWriteRequest> = z.object({
  type: z.string().min(1),
  id: z.string().min(1).optional(),
  path: z.string().min(1).optional(),
  version_token: z.string().min(1).optional(),
  properties: z.array(RecordPropertyValue).min(1),
});
export const RelationWriteRequest = z.object({
  id: z.string().min(1),
  version_token: z.string().min(1),
  property: z.string().min(1),
  op: z.enum(["add", "remove", "replace"]),
  targets: z.array(z.string().min(1)),
});
export const ViewGroupBy: z.ZodType<ViewGroupBy> = z.object({
  property: z.string().min(1),
  direction: z.enum(["asc", "desc"]).optional(),
});
export const ViewPropertyConfig: z.ZodType<ViewPropertyConfig> = z
  .object({ display_name: z.string().min(1) })
  .partial();
export const VaultFilterNode: z.ZodType<VaultFilterNode> = z.lazy(() =>
  z
    .object({
      all: z.array(VaultFilterNode).min(1),
      any: z.array(VaultFilterNode).min(1),
      not: VaultFilterNode,
      property: z.string().min(1),
      op: z.enum([
        "=",
        "<>",
        "<",
        "<=",
        ">",
        ">=",
        "LIKE",
        "IN",
        "IS NULL",
        "IS NOT NULL",
      ]),
      value: z.string(),
      values: z.array(z.string()).min(1),
    })
    .partial()
);
export const ViewDef: z.ZodType<ViewDef> = z.object({
  name: z.string().min(1),
  type: z.string().min(1).optional(),
  label: z.string().optional(),
  filter: VaultFilterNode.optional(),
  grouping: z.array(ViewGroupBy).max(2).optional(),
  sort: z.array(RecordSort).optional(),
  properties: z.array(z.string().min(1)).optional(),
  property_config: z.record(ViewPropertyConfig).optional(),
  layout: z
    .enum(["table", "cards", "board", "calendar", "gallery", "map"])
    .optional(),
  formulas: z.record(z.string().min(1)).optional(),
  aggregates: z.array(RecordAggregate).optional(),
  limit: z.number().int().gte(1).optional(),
  disabled: z.boolean().optional(),
  source: z.string().optional(),
  untranslated: z.array(z.string().min(1)).optional(),
});
export const VaultFindGroupBy: z.ZodType<VaultFindGroupBy> = z.object({
  property: z.string().min(1),
  direction: z.enum(["asc", "desc"]).optional(),
});
export const VaultFindSort: z.ZodType<VaultFindSort> = z.object({
  property: z.string().min(1),
  direction: z.enum(["asc", "desc"]).optional(),
});
export const VaultFindAggregate: z.ZodType<VaultFindAggregate> = z.object({
  op: z.enum([
    "count",
    "sum",
    "min",
    "max",
    "avg",
    "median",
    "stddev",
    "range",
    "earliest",
    "latest",
    "checked",
    "unchecked",
    "empty",
    "filled",
    "unique",
  ]),
  property: z.string().min(1).optional(),
});
export const VaultFindRequest: z.ZodType<VaultFindRequest> = z
  .object({
    words: z.string().min(1),
    type: z.string().min(1),
    kind: z.enum(["note", "record", "task", "attachment"]),
    filter: VaultFilterNode,
    view: z.string().min(1),
    near: z.string().min(1),
    hops: z.number().int().gte(1).lte(2),
    join: z.array(z.string().min(1)),
    group_by: z.array(VaultFindGroupBy).max(2),
    sort: z.array(VaultFindSort),
    select: z.array(z.string().min(1)),
    aggregate: z.array(VaultFindAggregate),
    explain: z.boolean(),
    limit: z.number().int().gte(1),
    cursor: z.string().min(1),
    detail: z.enum(["minimal", "standard"]),
  })
  .partial();
export const VaultFindCounts: z.ZodType<VaultFindCounts> = z.object({
  selected: z.number().int().gte(0),
  evaluated: z.number().int().gte(0),
  shown: z.number().int().gte(0),
});
export const VaultIndexState: z.ZodType<VaultIndexState> = z.object({
  returned: z.number().int().gte(0),
  agreeing: z.number().int().gte(0),
  epoch: z.number().int().gte(0).optional(),
});
export const VaultFindCell: z.ZodType<VaultFindCell> = z.object({
  property: z.string().min(1),
  value: z.string(),
});
export const VaultFindJoin: z.ZodType<VaultFindJoin> = z.object({
  relation: z.string().min(1),
  target: z.string().min(1),
  cells: z.array(VaultFindCell),
});
export const VaultFindRow: z.ZodType<VaultFindRow> = z.object({
  id: z.string().min(1).optional(),
  path: z.string().min(1),
  title: z.string(),
  line: z.number().int().gte(1).optional(),
  status: z.enum(["open", "done"]).optional(),
  text: z.string().optional(),
  cells: z.array(VaultFindCell),
  joins: z.array(VaultFindJoin),
  stale: z.boolean().optional(),
});
export const VaultFindSubgroup: z.ZodType<VaultFindSubgroup> = z.object({
  property: z.string().min(1),
  key: z.string(),
  absent: z.boolean().optional(),
  count: z.number().int().gte(0),
  paths: z.array(z.string().min(1)),
});
export const VaultFindGroup: z.ZodType<VaultFindGroup> = z.object({
  property: z.string().min(1),
  key: z.string(),
  absent: z.boolean().optional(),
  count: z.number().int().gte(0),
  paths: z.array(z.string().min(1)),
  subgroups: z.array(VaultFindSubgroup).optional(),
});
export const VaultFindTotal: z.ZodType<VaultFindTotal> = z.object({
  op: z.enum([
    "count",
    "sum",
    "min",
    "max",
    "avg",
    "median",
    "stddev",
    "range",
    "earliest",
    "latest",
    "checked",
    "unchecked",
    "empty",
    "filled",
    "unique",
  ]),
  label: z.string().min(1),
  value: z.string(),
  scope: z.string().min(1),
  refused: z.boolean().optional(),
});
export const VaultFindAction: z.ZodType<VaultFindAction> = z.object({
  label: z.string().min(1),
  call: z.string().min(1),
});
export const VaultTermCount: z.ZodType<VaultTermCount> = z.object({
  term: z.string().min(1),
  documents: z.number().int().gte(1),
});
export const VaultFindPlanStep: z.ZodType<VaultFindPlanStep> = z.object({
  stage: z.enum([
    "scope",
    "narrow",
    "retrieve",
    "compare",
    "join",
    "group",
    "sort",
    "aggregate",
    "render",
  ]),
  property: z.string().min(1).optional(),
  source: z
    .enum(["properties_index", "text_index", "go_comparator", "schema", "none"])
    .optional(),
  detail: z.string().min(1),
});
export const VaultFindResponse: z.ZodType<VaultFindResponse> = z.object({
  complete: z.boolean(),
  complete_reason: z.string().optional(),
  refused: z.boolean(),
  counts: VaultFindCounts,
  query_echo: z.string(),
  index: VaultIndexState.optional(),
  rows: z.array(VaultFindRow),
  elided: z.number().int().gte(1).optional(),
  elided_summary: z.string().optional(),
  groups: z.array(VaultFindGroup).optional(),
  totals: z.array(VaultFindTotal),
  problems: z.array(RecordProblem),
  next: z.array(VaultFindAction),
  nearest_terms: z.array(VaultTermCount).optional(),
  plan: z.array(VaultFindPlanStep).optional(),
  next_cursor: z.string().min(1).optional(),
  limit_applied: z.number().int().gte(1).optional(),
  limit_clamped: z.boolean().optional(),
  limit_requested: z.number().int().gte(1).optional(),
});
export const ValidationReport: z.ZodType<ValidationReport> = z.object({
  complete: z.boolean(),
  problems: z.array(RecordProblem),
  records_checked: z.number().int().gte(0),
  types_checked: z.number().int().gte(0),
  types: z.array(z.string().min(1)).optional(),
});
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
    max_agent_llm_calls_per_hour: z.number().int().gte(0),
    max_agent_tool_calls_per_minute: z.number().int().gte(0),
  })
  .partial()
  .passthrough();
export const ProviderCatalogEntry = z.object({
  id: z.string().min(1),
  company: z.string().min(1),
  plan: z.enum(["standard-api", "coding-plan"]),
  region: z.enum(["intl", "china", "us"]).optional(),
  wire: z.enum(["openai-compatible", "anthropic"]),
  endpointHint: z.string().min(1),
  logoSlug: z.string().min(1),
  label: z.string().min(1),
  subtitle: z.string().min(1),
  aliases: z.array(z.string()).optional(),
  anthropic_id: z.string().min(1).optional(),
});
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
export const SessionMessageProgress =
  z.object({
    message_id: z.string().min(1),
    session_id: z.string().min(1),
    parent_session_id: z.string().nullish(),
    generation: z.number().int().gte(0).optional(),
    direction: z.literal("child_to_parent"),
    kind: z.literal("progress"),
    depth: z.number().int().gte(0).lte(5),
    created_at: z.string().datetime({ offset: true }),
    sender_identity: z.string().min(1),
    untrusted_origin: z.boolean(),
    text: z.string().max(32768),
    pct: z.number().int().gte(0).lte(100).optional(),
  }) satisfies z.ZodType<SessionMessageProgress>;
export const SessionMessageCheckpoint =
  z.object({
    message_id: z.string().min(1),
    session_id: z.string().min(1),
    parent_session_id: z.string().nullish(),
    generation: z.number().int().gte(0).optional(),
    direction: z.literal("child_to_parent"),
    kind: z.literal("checkpoint"),
    depth: z.number().int().gte(0).lte(5),
    created_at: z.string().datetime({ offset: true }),
    sender_identity: z.string().min(1),
    untrusted_origin: z.boolean(),
    summary: z.string().min(1).max(500),
    result_so_far: z.string().max(32768).optional(),
    commit_ref: z.string().optional(),
  }) satisfies z.ZodType<SessionMessageCheckpoint>;
export const SessionMessageArtifact =
  z.object({
    message_id: z.string().min(1),
    session_id: z.string().min(1),
    parent_session_id: z.string().nullish(),
    generation: z.number().int().gte(0).optional(),
    direction: z.literal("child_to_parent"),
    kind: z.literal("artifact"),
    depth: z.number().int().gte(0).lte(5),
    created_at: z.string().datetime({ offset: true }),
    sender_identity: z.string().min(1),
    untrusted_origin: z.boolean(),
    paths: z.array(z.string()).min(1),
    note: z.string().max(4096).optional(),
  }) satisfies z.ZodType<SessionMessageArtifact>;
export const SessionMessageBlocker = z.object(
  {
    message_id: z.string().min(1),
    session_id: z.string().min(1),
    parent_session_id: z.string().nullish(),
    generation: z.number().int().gte(0).optional(),
    direction: z.literal("child_to_parent"),
    kind: z.literal("blocker"),
    depth: z.number().int().gte(0).lte(5),
    created_at: z.string().datetime({ offset: true }),
    sender_identity: z.string().min(1),
    untrusted_origin: z.boolean(),
    text: z.string().max(32768),
    severity: z.enum(["low", "medium", "high"]),
    correlation_id: z.string().optional(),
  }
) satisfies z.ZodType<SessionMessageBlocker>;
export const SessionMessageQuestion =
  z.object({
    message_id: z.string().min(1),
    session_id: z.string().min(1),
    parent_session_id: z.string().nullish(),
    generation: z.number().int().gte(0).optional(),
    direction: z.literal("child_to_parent"),
    kind: z.literal("question"),
    depth: z.number().int().gte(0).lte(5),
    created_at: z.string().datetime({ offset: true }),
    sender_identity: z.string().min(1),
    untrusted_origin: z.boolean(),
    text: z.string().max(32768),
    wait: z.boolean(),
    correlation_id: z.string().min(1),
    authority: z.enum(["self_ok", "owner_required"]).optional(),
  }) satisfies z.ZodType<SessionMessageQuestion>;
export const SessionMessageDecisionRequest =
  z.object({
    message_id: z.string().min(1),
    session_id: z.string().min(1),
    parent_session_id: z.string().nullish(),
    generation: z.number().int().gte(0).optional(),
    direction: z.literal("child_to_parent"),
    kind: z.literal("decision_request"),
    depth: z.number().int().gte(0).lte(5),
    created_at: z.string().datetime({ offset: true }),
    sender_identity: z.string().min(1),
    untrusted_origin: z.boolean(),
    text: z.string().max(32768),
    options: z.array(z.string()).min(2),
    correlation_id: z.string().min(1),
    authority: z.enum(["self_ok", "owner_required"]).optional(),
  }) satisfies z.ZodType<SessionMessageDecisionRequest>;
export const SessionMessageError = z.object({
  message_id: z.string().min(1),
  session_id: z.string().min(1),
  parent_session_id: z.string().nullish(),
  generation: z.number().int().gte(0).optional(),
  direction: z.literal("child_to_parent"),
  kind: z.literal("error"),
  depth: z.number().int().gte(0).lte(5),
  created_at: z.string().datetime({ offset: true }),
  sender_identity: z.string().min(1),
  untrusted_origin: z.boolean(),
  text: z.string().max(32768),
  fatal: z.boolean(),
}) satisfies z.ZodType<SessionMessageError>;
export const SessionMessageHandback =
  z.object({
    message_id: z.string().min(1),
    session_id: z.string().min(1),
    parent_session_id: z.string().nullish(),
    generation: z.number().int().gte(0).optional(),
    direction: z.literal("child_to_parent"),
    kind: z.literal("handback"),
    depth: z.number().int().gte(0).lte(5),
    created_at: z.string().datetime({ offset: true }),
    sender_identity: z.string().min(1),
    untrusted_origin: z.boolean(),
    result_so_far: z.string().max(50000),
    artifacts: z.array(z.string()),
    open_questions: z.array(z.string()),
    mode: z.enum(["final", "pause"]),
  }) satisfies z.ZodType<SessionMessageHandback>;
export const RevisionEntry: z.ZodType<RevisionEntry> = z.object({
  revision_id: z.string().min(1),
  plan_id: z.string().min(1),
  generation: z.number().int().gte(0),
  verb: z.enum(["append", "supersede", "targeted_retry", "abandon"]),
  falsified_assumption: z.string().min(1).max(2000),
  tail_adds: z.array(
    z.object({
      member_id: z.string().min(1),
      blocked_by: z.array(z.string()).optional(),
    })
  ),
  superseded_member_id: z.string().optional(),
  retried_member_id: z.string().optional(),
  reason: z.string().min(1).max(2000),
  created_at: z.string().datetime({ offset: true }),
});
export const SessionMessageRevisionEntry =
  z.object({
    message_id: z.string().min(1),
    session_id: z.string().min(1),
    parent_session_id: z.string().nullish(),
    generation: z.number().int().gte(0).optional(),
    direction: z.literal("engine"),
    kind: z.literal("revision_entry"),
    depth: z.number().int().gte(0).lte(5),
    created_at: z.string().datetime({ offset: true }),
    sender_identity: z.string().min(1),
    untrusted_origin: z.boolean(),
    revision: RevisionEntry,
  }) satisfies z.ZodType<SessionMessageRevisionEntry>;
export const SessionMessageGoalStatus =
  z.object({
    message_id: z.string().min(1),
    session_id: z.string().min(1),
    parent_session_id: z.string().nullish(),
    generation: z.number().int().gte(0).optional(),
    direction: z.literal("session_to_ui"),
    kind: z.literal("goal_status"),
    depth: z.number().int().gte(0).lte(5),
    created_at: z.string().datetime({ offset: true }),
    sender_identity: z.string().min(1),
    untrusted_origin: z.boolean(),
    condition: z.enum(["met", "waiting_on_user"]),
    goal_id: z.string().min(1),
  }) satisfies z.ZodType<SessionMessageGoalStatus>;
export const SessionMessageSteer = z.object({
  message_id: z.string().min(1),
  session_id: z.string().min(1),
  parent_session_id: z.string().nullish(),
  generation: z.number().int().gte(0).optional(),
  direction: z.literal("parent_to_child"),
  kind: z.literal("steer"),
  depth: z.number().int().gte(0).lte(5),
  created_at: z.string().datetime({ offset: true }),
  sender_identity: z.string().min(1),
  untrusted_origin: z.boolean(),
  text: z.string().max(16384),
  correlation_id: z.string().optional(),
}) satisfies z.ZodType<SessionMessageSteer>;
export const SessionMessageRespond = z.object(
  {
    message_id: z.string().min(1),
    session_id: z.string().min(1),
    parent_session_id: z.string().nullish(),
    generation: z.number().int().gte(0).optional(),
    direction: z.literal("parent_to_child"),
    kind: z.literal("respond"),
    depth: z.number().int().gte(0).lte(5),
    created_at: z.string().datetime({ offset: true }),
    sender_identity: z.string().min(1),
    untrusted_origin: z.boolean(),
    text: z.string().max(16384),
    correlation_id: z.string().min(1),
  }
) satisfies z.ZodType<SessionMessageRespond>;
export const SessionMessage = z.discriminatedUnion(
  "kind",
  [
    SessionMessageProgress,
    SessionMessageCheckpoint,
    SessionMessageArtifact,
    SessionMessageBlocker,
    SessionMessageQuestion,
    SessionMessageDecisionRequest,
    SessionMessageError,
    SessionMessageHandback,
    SessionMessageRevisionEntry,
    SessionMessageGoalStatus,
    SessionMessageSteer,
    SessionMessageRespond,
  ]
) satisfies z.ZodType<SessionMessage>;
export const SessionLifecycleRecord: z.ZodType<SessionLifecycleRecord> =
  z.object({
    session_id: z.string().min(1),
    generation: z.number().int().gte(0),
    resumed_from: z.string().nullish(),
    state: z.enum([
      "queued",
      "running",
      "needs_input",
      "paused",
      "completed",
      "failed",
      "cancelled",
      "timed_out",
    ]),
    terminal: z.boolean(),
    owner_scope_kind: z.enum(["parent_session", "plan", "human"]),
    owner_scope_id: z.string().optional(),
    owns_plan_id: z.string().optional(),
    goal_ref: z.string().optional(),
    workspace_id: z.string().min(1),
    agent_id: z.string().min(1),
    is_3p: z.boolean(),
    launch_profile: z.enum(["utility", "specialist"]),
    last_checkpoint_ref: z.string().optional(),
    undelivered_message_ids: z.array(z.string()),
    needs_input: z
      .object({
        correlation_id: z.string().min(1),
        ttl_deadline: z.string().datetime({ offset: true }),
        reconstructable: z.boolean(),
      })
      .optional(),
    failed_reason: z.string().optional(),
    created_at: z.string().datetime({ offset: true }),
    updated_at: z.string().datetime({ offset: true }),
  });
export const Goal: z.ZodType<Goal> = z.object({
  goal_id: z.string().min(1),
  binding_kind: z.enum(["session", "task", "plan"]),
  binding_id: z.string().min(1),
  source: z.enum(["chat_compiled", "task_explicit", "plan_dod"]),
  prompt: z.string().min(1).max(4000),
  definition: z.string().max(4000).optional(),
  criteria: z.array(AcceptanceCriterion),
  attempts_max: z.number().int().gte(1),
  judge_rounds_max: z.number().int().gte(1),
  round: z.number().int().gte(0),
  state: z.enum(["active", "done", "failed", "cleared"]),
  created_at: z.string().datetime({ offset: true }),
});
export const TokenBudgetStatus = z.object({
  budget: z.number().int().gte(0),
  consumed: z.number().int().gte(0),
  remaining: z.number().int(),
  exhausted: z.boolean(),
  advisory: z.string().optional(),
  by_scope: z.object({
    owner: z.number().int().gte(0),
    member: z.number().int().gte(0),
    verifier: z.number().int().gte(0),
    judge: z.number().int().gte(0),
  }),
});
export const PlanRestartResponse: z.ZodType<PlanRestartResponse> = z.object({
  plan: Plan,
  new_session_id: z.string().min(1),
  generation: z.number().int().gte(0),
  resumed_from: z.string().nullish(),
});
export const DelegateRunAction = z.object({
  action: z.literal("run"),
  target_agent_id: z.string().min(1),
  task: z.string().min(1).max(10000),
  label: z.string().max(100).optional(),
  launch_profile: z.enum(["utility", "specialist"]),
  wait: z.boolean().optional(),
  allow_blocking_question: z.boolean().optional(),
  critical: z.boolean().optional(),
  timeout_seconds: z.number().int().gte(0).optional(),
  snapshot: z
    .object({ references: z.array(z.string()), notes: z.string().max(8192) })
    .partial()
    .optional(),
}) satisfies z.ZodType<DelegateRunAction>;
export const DelegateStatusAction = z.object({
  action: z.literal("status"),
  session_id: z.string().min(1),
  task_id: z.string().optional(),
}) satisfies z.ZodType<DelegateStatusAction>;
export const DelegateInboxAction = z.object({
  action: z.literal("inbox"),
  session_id: z.string().min(1),
  since_cursor: z.string().optional(),
  max: z.number().int().gte(1).lte(200).optional(),
}) satisfies z.ZodType<DelegateInboxAction>;
export const DelegateInboxAckAction =
  z.object({
    action: z.literal("inbox_ack"),
    session_id: z.string().min(1),
    message_ids: z.array(z.string()).min(1),
  }) satisfies z.ZodType<DelegateInboxAckAction>;
export const DelegateSteerAction = z.object({
  action: z.literal("steer"),
  session_id: z.string().min(1),
  text: z.string().min(1).max(16384),
  correlation_id: z.string().optional(),
}) satisfies z.ZodType<DelegateSteerAction>;
export const DelegateRespondAction = z.object(
  {
    action: z.literal("respond"),
    session_id: z.string().min(1),
    text: z.string().min(1).max(16384),
    correlation_id: z.string().min(1),
  }
) satisfies z.ZodType<DelegateRespondAction>;
export const DelegateCancelAction = z.object({
  action: z.literal("cancel"),
  session_id: z.string().min(1),
  hard: z.boolean().optional(),
}) satisfies z.ZodType<DelegateCancelAction>;
export const DelegateFollowUpAction =
  z.object({
    action: z.literal("follow_up"),
    session_id: z.string().min(1),
    task: z.string().max(10000).optional(),
  }) satisfies z.ZodType<DelegateFollowUpAction>;
export const DelegatePeekAction = z.object({
  action: z.literal("peek"),
  session_id: z.string().min(1),
}) satisfies z.ZodType<DelegatePeekAction>;
export const DelegateActionRequest =
  z.discriminatedUnion("action", [
    DelegateRunAction,
    DelegateStatusAction,
    DelegateInboxAction,
    DelegateInboxAckAction,
    DelegateSteerAction,
    DelegateRespondAction,
    DelegateCancelAction,
    DelegateFollowUpAction,
    DelegatePeekAction,
  ]) satisfies z.ZodType<DelegateActionRequest>;
export const DelegateSessionResponse: z.ZodType<DelegateSessionResponse> =
  z.object({
    session_id: z.string().min(1),
    generation: z.number().int().gte(0),
    resumed_from: z.string().nullish(),
    is_3p: z.boolean(),
    state: z.enum([
      "queued",
      "running",
      "needs_input",
      "paused",
      "completed",
      "failed",
      "cancelled",
      "timed_out",
    ]),
  });
export const DelegateStatusResponse: z.ZodType<DelegateStatusResponse> =
  z.object({
    session: SessionLifecycleRecord,
    last_checkpoint: z
      .object({
        summary: z.string(),
        result_so_far: z.string(),
        commit_ref: z.string(),
        created_at: z.string().datetime({ offset: true }),
      })
      .partial()
      .optional(),
    last_progress: z
      .object({
        text: z.string(),
        pct: z.number().int().gte(0).lte(100),
        created_at: z.string().datetime({ offset: true }),
      })
      .partial()
      .optional(),
    unacked_count: z.number().int().gte(0),
  });
export const DelegateInboxResponse: z.ZodType<DelegateInboxResponse> = z.object(
  {
    messages: z.array(SessionMessage),
    has_more: z.boolean(),
    next_cursor: z.string().optional(),
  }
);
export const DelegateRespondResponse: z.ZodType<DelegateRespondResponse> =
  z.object({
    acknowledged: z.boolean(),
    corrective_session: DelegateSessionResponse.optional(),
  });
export const DelegatePeekResponse = z.object({
  session_id: z.string().min(1),
  state: z.enum([
    "queued",
    "running",
    "needs_input",
    "paused",
    "completed",
    "failed",
    "cancelled",
    "timed_out",
  ]),
  latest_checkpoint_summary: z.string().optional(),
  latest_progress_text: z.string().optional(),
  latest_progress_pct: z.number().int().gte(0).lte(100).optional(),
});
export const MessageParentProgress = z.object(
  {
    kind: z.literal("progress"),
    message_id: z.string().optional(),
    text: z.string().min(1).max(32768),
    pct: z.number().int().gte(0).lte(100).optional(),
  }
) satisfies z.ZodType<MessageParentProgress>;
export const MessageParentCheckpoint =
  z.object({
    kind: z.literal("checkpoint"),
    message_id: z.string().optional(),
    summary: z.string().min(1).max(500),
    result_so_far: z.string().max(32768).optional(),
    commit_ref: z.string().optional(),
  }) satisfies z.ZodType<MessageParentCheckpoint>;
export const MessageParentArtifact = z.object(
  {
    kind: z.literal("artifact"),
    message_id: z.string().optional(),
    paths: z.array(z.string()).min(1),
    note: z.string().max(4096).optional(),
  }
) satisfies z.ZodType<MessageParentArtifact>;
export const MessageParentBlocker = z.object({
  kind: z.literal("blocker"),
  message_id: z.string().optional(),
  text: z.string().min(1).max(32768),
  severity: z.enum(["low", "medium", "high"]),
}) satisfies z.ZodType<MessageParentBlocker>;
export const MessageParentQuestion = z.object(
  {
    kind: z.literal("question"),
    message_id: z.string().optional(),
    text: z.string().min(1).max(32768),
    wait: z.boolean(),
    authority: z.enum(["self_ok", "owner_required"]).optional(),
    correlation_id: z.string().optional(),
  }
) satisfies z.ZodType<MessageParentQuestion>;
export const MessageParentHandback = z.object(
  {
    kind: z.literal("handback"),
    message_id: z.string().optional(),
    result_so_far: z.string().max(50000),
    artifacts: z.array(z.string()).optional(),
    open_questions: z.array(z.string()).optional(),
    mode: z.enum(["final", "pause"]),
  }
) satisfies z.ZodType<MessageParentHandback>;
export const MessageParentRequest =
  z.discriminatedUnion("kind", [
    MessageParentProgress,
    MessageParentCheckpoint,
    MessageParentArtifact,
    MessageParentBlocker,
    MessageParentQuestion,
    MessageParentHandback,
  ]) satisfies z.ZodType<MessageParentRequest>;
export const MessageParentResponse = z.object({
  accepted: z.boolean(),
  message_id: z.string().optional(),
  correlation_id: z.string().optional(),
  error: z.string().optional(),
});

const endpoints = makeApi([
  {
    method: "get",
    path: "/about",
    alias: "getAbout",
    description: `Returns version, runtime, uptime, PID, and the live preview_enabled flag. The SPA uses preview_enabled to decide whether to surface preview links, which resolve against the main gateway origin at /preview/ (no separate preview listener/port/origin exists — ADR-044).
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
    description: `Returns all agents from config.json (core + custom). Core agents return empty soul/heartbeat (compiled-in prompts are not exposed). Custom agents return SOUL.md content only (not heartbeat) for efficient list rendering.
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
    description: `Returns the full agent configuration including soul and heartbeat. Core (locked) agents return empty soul (compiled-in prompt not exposed).
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
    description: `Updates the specified agent. All fields are optional (only provided fields change). Locked core agents reject mutations to name, description, soul, heartbeat (403). Writing soul/heartbeat triggers a config reload. Model, timeout, max_tool_iterations, heartbeat_enabled, heartbeat_interval changes do NOT trigger a reload.
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
    method: "get",
    path: "/agents/:id/mailboxes/:workspaceId",
    alias: "getAgentMailbox",
    description: `Returns the mailbox the agent holds in the given workspace. An agent can hold a different mailbox in each workspace it belongs to (different roles, different inboxes). Email is a TOOL surface (read_inbox, search_email, read_message, send_email, reply), not a conversational channel. The mailbox password is never returned; the &#x60;configured&#x60; flag reports whether a password is on file in the credential store.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "workspaceId",
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
        description: `Agent not found, or no mailbox configured for the agent in this workspace.`,
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
    path: "/agents/:id/mailboxes/:workspaceId",
    alias: "setAgentMailbox",
    description: `Configures the mailbox the agent holds in the given workspace. Every (agent, workspace) pair may have its own mailbox — an agent plays different roles in different workspaces and can have a distinct inbox in each. The password, when present, is routed into the encrypted credential store and persisted only as a reference — it is never written to config.json. Unhandled inbound mail becomes Board tasks in THIS workspace, assigned to the owning agent. The email tools resolve the active workspace from the turn context at execution time — never from a model-supplied parameter.
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
      {
        name: "workspaceId",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Mailbox,
    errors: [
      {
        status: 400,
        description: `Missing or invalid required field.`,
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
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "delete",
    path: "/agents/:id/mailboxes/:workspaceId",
    alias: "deleteAgentMailbox",
    description: `Removes the mailbox the agent holds in the given workspace from config and deletes its stored password from the credential store. The agent&#x27;s email tools for this workspace are de-registered on the next reload; mailboxes the agent holds in OTHER workspaces are untouched.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "workspaceId",
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
        description: `Agent not found, or no mailbox configured for the agent in this workspace.`,
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
    path: "/agents/executor-defaults",
    alias: "listExecutorDefaults",
    description: `Static reference data: for each supported subagent_3p external CLI (claude-code, codex, opencode), the ordered list of arguments the driver automatically applies when spawning it (ADR-032), plus a note on how the prompt itself is delivered. Read-only and not agent-scoped — used by the Agent Profile UI so operators see the REAL, currently-in-effect flags instead of static placeholder ghost-text before adding their own executor.cli_args. Sourced directly from pkg/agent/runner/driver_{claude,codex,opencode}.go and kept byte-accurate to that code. Superseded by POST /agents/executor-preview, which computes real per-agent argv instead of this hand-maintained static description — do not extend this endpoint further; new callers should use executor-preview.
`,
    requestFormat: "json",
    response: z.array(ExecutorDefaults),
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
    path: "/agents/executor-preview",
    alias: "postAgentsExecutorPreview",
    description: `Stateless, agent-agnostic: computes the REAL argv each driver would build for the given cli/model/cli_path/cli_args/max_tool_iterations by calling the same buildArgs() logic used at real dispatch time (pkg/agent/runner/driver_{claude,codex,opencode}.go), not a hand-maintained description like GET /agents/executor-defaults. Any cli_args token the safety filter (argsafety.go) would strip is excluded from the previewed argv and reported in dropped_args instead of being silently dropped, so the operator sees it before saving. Works from the create wizard (no agent id exists yet) and from an existing agent&#x27;s edit form alike — mirrors POST /system/cli-validate&#x27;s stateless, body-driven shape. No subprocess is spawned.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: ExecutorCommandPreviewRequest,
      },
    ],
    response: ExecutorCommandPreviewResponse,
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
    path: "/agents/executor-smoke-test",
    alias: "postAgentsExecutorSmokeTest",
    description: `Runs a real, bounded test turn (a trivial arithmetic prompt) through the SAME driver.Run() dispatch path a genuine subagent_3p delegation uses, in a dedicated ephemeral workspace — not the zero-token POST /agents/{id}/runner/test (binary-present → version handshake → credential-file-presence only). This spawns a real, authenticated subprocess and costs real model usage, bounded by a short timeout and a small turn cap. An explicit operator action only — never triggered automatically. Applies its own dedicated, more conservative rate limiter and per-caller in-flight cap, mirroring the pattern POST /system/cli-validate uses but independently tuned since this endpoint spends real tokens and holds a real subprocess (5/min and 1 concurrent run per caller, vs. cli-validate&#x27;s 20/min and 2), and emits one audit event {cli, resolved binary, ok} per call, since — like cli-validate — it spawns a caller-influenced binary/path. Body-driven and agent-agnostic; works from the create wizard and an existing agent&#x27;s edit form alike.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: ExecutorSmokeTestRequest,
      },
    ],
    response: ExecutorSmokeTestResponse,
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
    ],
  },
  {
    method: "get",
    path: "/audit-log",
    alias: "getAuditLog",
    description: `Returns the last 100 audit log entries in reverse-chronological order (read from ~/.omnipus/system/audit.jsonl) wrapped with the HMAC tamper-evident chain-verification result (chain_status). entries is an empty array when no entries exist.
`,
    requestFormat: "json",
    response: AuditLogResponse,
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
    description: `Self-service password change. Requires the current password for verification. Requires authentication.
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
    method: "get",
    path: "/auth/validate",
    alias: "validateToken",
    description: `Returns the authenticated user&#x27;s username when the token is valid. Rate-limited: 30 requests per IP per minute.
`,
    requestFormat: "json",
    response: z.object({ username: z.string() }).passthrough(),
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
    method: "post",
    path: "/browser/inspect",
    alias: "browserInspect",
    description: `Best-effort resolution of the element at a device-pixel point in the agent&#x27;s live browser tab, so the SPA can attach the element&#x27;s text/HTML as context when a user annotates a spot in the Live Browser panel. Requires authentication. Returns ok&#x3D;false (with a reason) when the element can&#x27;t be resolved (cross-origin frame, detached node, timeout); the SPA then falls back to the cropped-image annotation alone.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: BrowserInspectRequest,
      },
    ],
    response: BrowserInspectResponse,
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
    method: "post",
    path: "/channels",
    alias: "createChannelInstance",
    description: `Creates a new channel instance with key &quot;&lt;type&gt;.&lt;slug&gt;&quot; (ADR-029 FR-017). The instance starts disabled (enabled: false). Returns 409 if the instance key already exists, 400 if the type is unknown or the slug is malformed.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: ChannelCreateRequest,
      },
    ],
    response: ChannelCreateResponse,
    errors: [
      {
        status: 400,
        description: `Unknown channel type or malformed slug.`,
        schema: ErrorResponse,
      },
      {
        status: 409,
        description: `Instance key already exists.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Config write failure.`,
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
    method: "delete",
    path: "/channels/:id",
    alias: "deleteChannelInstance",
    description: `Deletes a channel instance: removes its config entry, credential refs, any stale channel-wildcard binding, and its per-instance state directory (e.g. WhatsApp store). Returns 404 for unknown instances and 400 for malformed ids. Bare-type keys (e.g. &quot;telegram&quot;) can be deleted; &quot;webchat&quot; is a built-in and cannot be deleted.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string().regex(/^[a-z0-9-]+(\.[a-z0-9-]+)?$/),
      },
    ],
    response: z.void(),
    errors: [
      {
        status: 400,
        description: `Malformed channel id.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `Instance not found.`,
        schema: ErrorResponse,
      },
      {
        status: 500,
        description: `Config write failure.`,
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
    response: ChannelRouting,
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
        schema: ChannelRouting,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: ChannelRouting,
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
    method: "get",
    path: "/commands",
    alias: "listCommands",
    description: `Returns the canonical slash commands available on the given surface (default web). Aliases and deprecated names are excluded. The web chat palette renders the web set as its single source of truth.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "surface",
        type: "Query",
        schema: z.enum(["web", "cli", "channel"]).optional(),
      },
    ],
    response: z.array(SlashCommand),
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
    path: "/config/gateway/rotate-token",
    alias: "rotateGatewayToken",
    description: `Generates a new cryptographically random 64-hex-character gateway bearer token, persists it to config.json, triggers a hot reload, and returns the new token. The previous token is immediately invalidated. Use after security incidents or on a regular rotation schedule.
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
    description: `Returns an array of config keys whose persisted (disk) value differs from the boot-time applied value. Only RestartGatedKeys are checked; hot-reload keys never appear here. An empty array means no restart is needed.
`,
    requestFormat: "json",
    response: z.array(PendingRestartEntry),
    errors: [
      {
        status: 405,
        description: `Method not allowed.`,
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
    method: "post",
    path: "/credentials/rotate",
    alias: "rotateCredentials",
    description: `Re-encrypts the entire credential vault under a new Argon2id key derived from new_passphrase (and a fresh salt). Sensitive change — requires a re-auth consent token in the X-Reauth-Token header (Spec-6 FR-12.2 / ADR-022). No restart is required; the in-memory key is updated in place.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ new_passphrase: z.string().min(1) }),
      },
    ],
    response: z.object({ status: z.literal("rotated") }).passthrough(),
    errors: [
      {
        status: 400,
        description: `Invalid request (e.g. empty passphrase).`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Re-auth required or invalid consent token.`,
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
    description: `Returns pending pairing requests and already-paired devices.
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
    description: `Returns whether god mode (&quot;bypass-permissions&quot;) is currently active (&#x60;enabled&#x60;), whether it is available in this build/boot (&#x60;available&#x60;), and whether this build supports it at all (&#x60;supported&#x60;).
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
    description: `Flips the global god-mode (&quot;bypass-permissions&quot;) switch. When the build supports god mode AND this boot was already authorized (see GodModeStatus.available), the toggle applies or reverts the override live (no restart) — every agent&#x27;s tool policy is floored at &quot;allow&quot;, the kernel sandbox is off, network egress is open, and the shell guard is off, regardless of per-agent profiles. When enabling from a boot that was NOT yet authorized, this call persists authorization (sandbox.god_mode_allowed) and the runtime switch (sandbox.god_mode) to config and returns restart_required&#x3D;true — the override only takes effect after the gateway restarts. Disabling is always applied live. Audit logging, the prompt-injection guard, and rate limiting stay on. High blast radius — secured by RequireNotBypass (dev_mode_bypass returns 503) AND a single-use password re-auth consent token (X-Reauth-Token header; call POST /api/v1/auth/reauth first, 403 otherwise). Returns 403 when enabling and god mode is not SUPPORTED in this build (compiled with nogodmode). Every toggle is audit-logged with the acting user.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ enabled: z.boolean() }),
      },
    ],
    response: GodModeUpdateResponse,
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
    description: `Triggers a graceful self-restart: the gateway replies immediately, then drains in-flight work and re-execs the process (or exits cleanly for a supervisor). Used to apply restart-gated settings from the UI without a manual process bounce. The response gives the SPA a status + drain estimate so it can poll /health (and reconnect the WS) to detect the gateway going down and coming back up. High blast radius, secured by RequireNotBypass; dev_mode_bypass returns 503.
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
    path: "/library/:workspace_id/content",
    alias: "getLibraryContent",
    description: `Returns the text content of the file at path for the SPA editor (library-spec.md D-5), with explicit is_text / too_large fields so the SPA falls back to GET .../download rather than guessing from the content field. Returns 403 if path resolves outside the workspace&#x27;s work tree; 404 if path does not exist or names a directory.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "path",
        type: "Query",
        schema: z.string(),
      },
    ],
    response: LibraryContentResponse,
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
    method: "put",
    path: "/library/:workspace_id/content",
    alias: "putLibraryContent",
    description: `Writes text content to the file at the given workspace-relative path (library-spec.md D-5), creating the file if it does not already exist and overwriting any existing content entirely. Returns 403 if path resolves outside the workspace&#x27;s work tree; 404 if the path&#x27;s parent directory does not exist.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: LibraryContentRequest,
      },
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: LibraryEntry,
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
    path: "/library/:workspace_id/download",
    alias: "downloadLibraryFile",
    description: `Streams the raw bytes of the file at path with a best-effort Content-Type and a Content-Disposition attachment filename. The binary counterpart to GET .../content — used for non-text files and for text files GET .../content reports as too_large. Returns 403 if path resolves outside the workspace&#x27;s work tree; 404 if path does not exist or names a directory.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "path",
        type: "Query",
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
    method: "get",
    path: "/library/:workspace_id/entries",
    alias: "listLibraryEntries",
    description: `Lists the entries directly inside the given workspace-relative directory path (library-spec.md D-2 — entries are paths, not UUIDs). Omit path or pass an empty string to list the work-tree root — the Library explorer shows the WHOLE work/ directory, not merely the reserved work/.library/ upload directory (that is just one entry inside it). By default, entries whose name begins with a dot (&quot;.&quot;) are omitted from the listing — see include_hidden to include them and LibraryEntry.is_hidden for the definition. Returns 403 if path resolves outside the workspace&#x27;s work tree (traversal or an out-of-root symlink); 404 if the workspace or the directory itself does not exist.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "path",
        type: "Query",
        schema: z.string().optional().default(""),
      },
      {
        name: "include_hidden",
        type: "Query",
        schema: z.boolean().optional().default(false),
      },
    ],
    response: z.array(LibraryEntry),
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
    path: "/library/:workspace_id/entries",
    alias: "deleteLibraryEntry",
    description: `Deletes the file or directory at the given workspace-relative path. Deleting a directory removes it and everything under it. Returns 403 if path resolves outside the workspace&#x27;s work tree; 404 if nothing exists at path.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "path",
        type: "Query",
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
    method: "get",
    path: "/library/:workspace_id/inline-disposition",
    alias: "getLibraryInlineDisposition",
    description: `Returns the server&#x27;s own answer for one file: allow-listed for inline display or not, the extension-derived Content-Type it will be served with, which SPA renderer should draw it, and whether drawing it makes the browser execute it (ADR-067 D15).

Exists so the SPA never re-derives any of that from the filename. The allow-list and the extension-to-type table are compiled into the binary and are the single source of truth (FR-015a, FR-015b); a second copy in TypeScript would be a second answer, and the two would disagree the first time an extension was added to one of them.

Returns 403 if path resolves outside the workspace&#x27;s work tree; 404 if path does not exist or names a directory.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "path",
        type: "Query",
        schema: z.string(),
      },
    ],
    response: LibraryInlineDisposition,
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
    path: "/library/:workspace_id/knowledge",
    alias: "getKnowledgeBaseInfo",
    description: `Marker-based detection (ADR-067 FR-020, FR-021): a folder is a knowledge base when its root contains .omnipus-vault/ or .obsidian/. File CONTENT is never read to decide this.

Returns 200 with is_knowledge_base&#x3D;false for an ordinary folder — that is an answer, not an error. A marker that exists but cannot be read is reported through detection_error rather than silently downgrading the folder to ordinary (E-9).

Carries no index counts. Index progress is a streaming state pushed over the WebSocket as knowledge_index_progress (FR-080); polling this endpoint for it is the mistake that contract is written to prevent.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "path",
        type: "Query",
        schema: z.string(),
      },
    ],
    response: KnowledgeBaseInfo,
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
    path: "/library/:workspace_id/knowledge/graph",
    alias: "getKnowledgeGraph",
    description: `One operation serves all five graph queries (FR-051) because they differ only in which subgraph is selected, not in what a link is.

Link resolution follows a fixed ladder — exact path, unique basename, shortest path, lexicographic (FR-040) — and an ambiguous basename is resolved by that rule AND reported as ambiguous (FR-041): resolving it is not a licence to stay quiet about it. A link with no match, or one whose target lies outside the collection root, is reported unresolved and the target is not read (FR-042, FR-043). Symbolic links are skipped and reported rather than followed (FR-044), which is also how a symlink loop terminates.

Every query is bounded by hop count and node count (FR-054) and reports its own truncation, so a small graph is never mistaken for a clipped one.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "collection_id",
        type: "Query",
        schema: z.string(),
      },
      {
        name: "kind",
        type: "Query",
        schema: z.enum([
          "links",
          "backlinks",
          "unresolved",
          "orphans",
          "neighbourhood",
        ]),
      },
      {
        name: "path",
        type: "Query",
        schema: z.string().optional(),
      },
      {
        name: "hops",
        type: "Query",
        schema: z.number().int().gte(1).optional(),
      },
      {
        name: "limit",
        type: "Query",
        schema: z.number().int().gte(1).optional(),
      },
    ],
    response: KnowledgeGraphResponse,
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
    path: "/library/:workspace_id/knowledge/outline",
    alias: "getKnowledgeOutline",
    description: `Returns the heading outline that drives the reading rail, for ANY markdown file — whether or not it belongs to a knowledge base (FR-062). An outline is parsed from the one file in hand and needs no index, which is exactly why search and backlinks stay knowledge-base-only: those do need one. The is_knowledge_base field tells the client which other rail panels it may offer.

Headings come back as a FLAT list in document order with nesting carried by level, not as a tree — a document that skips from H1 to H3 has one honest representation that way, where a tree would force the server to invent an intermediate heading the author never wrote.

Frontmatter that is not valid YAML is reported through frontmatter_malformed; the file is still outlined and still indexed for body text (E-17).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "path",
        type: "Query",
        schema: z.string(),
      },
    ],
    response: KnowledgeOutline,
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
    method: "post",
    path: "/library/:workspace_id/knowledge/search",
    alias: "searchKnowledgeBase",
    description: `Returns ranked hits with path, title and a matched excerpt (FR-050), AND — in the same response — the incompleteness statement qualifying them (FR-035). A caller cannot obtain results without also obtaining the statement, which is the point: a partial answer that looks whole is worse than no answer.

The excerpt is re-read from the file at query time and never stored in the index (FR-050a), so it always matches disk. When the re-read cannot be done — the file moved, became unreadable, or the latency budget ran out — the hit is still returned with path and title and a machine-readable excerpt_unavailable reason. Never a fabricated excerpt; never a silently dropped result.

A limit above the server cap is clamped and the clamp is reported (FR-037). A collection outside the caller&#x27;s workspace scope returns an EMPTY result set rather than a permission error (FR-052, FR-053), so the error channel cannot be used to probe for collections the caller may not see. POST rather than GET because a query plus its filters does not belong in a URL that lands in request logs.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: KnowledgeSearchRequest,
      },
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: KnowledgeSearchResponse,
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
    path: "/library/:workspace_id/mkdir",
    alias: "createLibraryDirectory",
    description: `Creates the directory at path, creating any missing intermediate directories along the way (mkdir -p semantics) — the sole directory-creation primitive the Library API exposes. Idempotent: returns 200 if a directory already exists at path; 201 if a new directory (or chain of directories) was created. Returns 403 if path resolves outside the workspace&#x27;s work tree; 409 if a regular FILE already exists at path.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ path: z.string().min(1) }),
      },
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: LibraryEntry,
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
        description: `Conflict — e.g. resource already exists.`,
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
    path: "/library/:workspace_id/rename",
    alias: "renameLibraryEntry",
    description: `Renames or moves the entry at &quot;from&quot; to &quot;to&quot; within this single workspace&#x27;s work tree (library-spec.md D-2). &quot;to&quot; may name a different parent directory than &quot;from&quot;, so this operation doubles as an in-workspace move. This is same-workspace sugar over POST /library/move (equivalent to calling it with from_workspace_id &#x3D;&#x3D; to_workspace_id &#x3D;&#x3D; {workspace_id}) — kept as a dedicated operation, alongside /library/move, so a caller doing only in-workspace renames never needs to know its own workspace id twice. Returns 400 if &quot;to&quot; begins with a &quot;..&quot; segment anywhere in the path (rejected outright as a sanity check — such a name isn&#x27;t a traversal, but this package&#x27;s hidden-entry heuristic also matches it, so it would otherwise succeed and immediately vanish from the default listing); 403 if either path resolves outside the workspace&#x27;s work tree; 404 if nothing exists at &quot;from&quot; OR &quot;to&quot;&#x27;s parent directory does not exist yet (this operation deliberately does NOT auto-create missing destination directories — the message names the specific missing directory; create it first with POST /library/{workspace_id}/mkdir); 409 if an entry already exists at &quot;to&quot;.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: LibraryRenameRequest,
      },
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: LibraryEntry,
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
        description: `Conflict — e.g. resource already exists.`,
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
    path: "/library/:workspace_id/upload",
    alias: "uploadLibraryFiles",
    description: `Streams a multipart upload directly into the given workspace-relative directory (library-spec.md D-1 — uploads land as real, named files inside the work tree, de-duplicated with a numeric suffix on collision). Omit path or pass an empty string to upload into the work-tree root. Returns 403 if path resolves outside the workspace&#x27;s work tree; 404 if the target directory does not exist.
`,
    requestFormat: "form-data",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: uploadLibraryFiles_Body,
      },
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "path",
        type: "Query",
        schema: z.string().optional().default(""),
      },
    ],
    response: LibraryUploadResponse,
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
    method: "post",
    path: "/library/copy",
    alias: "copyLibraryEntry",
    description: `Copies the entry at from_path (inside from_workspace_id&#x27;s work tree) to to_path (inside to_workspace_id&#x27;s work tree), leaving the source in place. Directory copies are recursive. Not scoped under {workspace_id} for the same reason as /library/move — see LibraryTransferRequest. Cross-workspace transfer is permitted for the authenticated UI/CLI user only — never for an agent tool; agents stay confined to their own workspace&#x27;s work tree (enforced server-side). Returns 403 if either path resolves outside its workspace&#x27;s work tree; 404 if from_workspace_id/to_workspace_id does not exist, nothing exists at from_path, OR to_path&#x27;s parent directory does not exist yet — this operation deliberately does NOT auto-create missing destination directories (matching &#x60;cp&#x60; semantics), but the 404 message names the specific missing directory rather than a bare &quot;not found&quot;; create it first with POST /library/{workspace_id}/mkdir. 409 if an entry already exists at to_path.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: LibraryTransferRequest,
      },
    ],
    response: LibraryEntry,
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
        description: `Conflict — e.g. resource already exists.`,
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
    path: "/library/move",
    alias: "moveLibraryEntry",
    description: `Moves the entry at from_path (inside from_workspace_id&#x27;s work tree) to to_path (inside to_workspace_id&#x27;s work tree). Not scoped under {workspace_id} like the other Library operations because source and destination can be different workspaces — see LibraryTransferRequest. A same-workspace move (from_workspace_id &#x3D;&#x3D; to_workspace_id) is exactly what POST /library/{workspace_id}/rename does as same-workspace sugar over this operation. Cross-workspace transfer is permitted for the authenticated UI/CLI user only — never for an agent tool; agents stay confined to their own workspace&#x27;s work tree (enforced server-side). Returns 403 if either path resolves outside its workspace&#x27;s work tree; 404 if from_workspace_id/to_workspace_id does not exist, nothing exists at from_path, OR to_path&#x27;s parent directory does not exist yet — this operation deliberately does NOT auto-create missing destination directories (matching &#x60;mv&#x60; semantics), but the 404 message names the specific missing directory rather than a bare &quot;not found&quot;; create it first with POST /library/{workspace_id}/mkdir. 409 if an entry already exists at to_path.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: LibraryTransferRequest,
      },
    ],
    response: LibraryEntry,
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
        description: `Conflict — e.g. resource already exists.`,
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
    path: "/library/preview-token",
    alias: "mintLibraryPreviewToken",
    description: `Mints the path-bearing credential a SANDBOXED preview needs (ADR-067 FR-003a, FR-003f). A document served under the isolation policy has an opaque origin, so it can send neither the SameSite&#x3D;Strict session cookie nor an Authorization header on its own &lt;link&gt;, &lt;script&gt;, font or media requests — without this token an HTML bundle simply cannot load its own subresources.

Minting is authenticated and never widens access: the caller must already be able to read the path, and the grant covers one workspace and one path only — a single file, or one bundle root and its descendants (FR-003b). The token lives 15 minutes and is also invalidated by logout, mount revoke, and deletion or move of the named path (FR-003d).

Re-minting returns a NEW token and invalidates the previous one (FR-003m). There is no renewal endpoint.

Both this endpoint and the /library-preview/&lt;token&gt;/&lt;path&gt; serving prefix are rate-limited, and a session may hold at most 8 live tokens; a 9th mint request is refused with 429 (FR-003k).

The serving prefix itself is deliberately NOT in this document: it is a bare, token-authenticated path on the main listener (ADR-044 shape) that returns file bytes or an HTML error page, carries no JSON contract, and answers GET and HEAD only — every other method is 405 with Allow: GET, HEAD (FR-003j).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: LibraryPreviewTokenRequest,
      },
    ],
    response: LibraryPreviewTokenResponse,
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
    path: "/library/workspaces",
    alias: "listLibraryWorkspaces",
    description: `Backs the Library sidebar entry point (library-spec.md D-3): every workspace the caller can browse, as a top-level node. Drilling into one node scopes subsequent Library calls to that workspace via {workspace_id} — see GET /library/{workspace_id}/entries.
`,
    requestFormat: "json",
    response: z.array(LibraryWorkspaceNode),
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
    method: "get",
    path: "/mailboxes",
    alias: "listMailboxes",
    description: `Returns every configured mailbox account (one per (agent, workspace) pair). The mailbox password is never returned; each entry&#x27;s &#x60;configured&#x60; flag reports whether a password is on file in the credential store. An empty list means no mailbox is configured — this endpoint never 404s, so the SPA can show mailbox status without per-agent probe requests.
`,
    requestFormat: "json",
    response: MailboxListResponse,
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
    description: `Adds a new MCP server to the gateway config.`,
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
    description: `Removes an MCP server from the gateway config.`,
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
    method: "patch",
    path: "/mcp-servers/:id",
    alias: "patchMcpServer",
    description: `Partially updates an MCP server config (enable/disable toggle, endpoint, env, headers, env_file). Omitted fields are preserved.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: McpServerUpdate,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
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
      {
        status: 404,
        description: `Resource not found.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/mcp-servers/:id/test",
    alias: "testMcpServer",
    description: `Attempts an on-demand connection to the configured MCP server and reports whether it succeeded and which tools it exposes. Does not change any state (the probe connection is closed immediately).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: McpServerTestResponse,
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
    path: "/media/workspace/:workspace_id/:media_id",
    alias: "serveWorkspaceMediaFile",
    description: `Resolves a media://workspace/&lt;workspace_id&gt;/&lt;media_id&gt; ref through the owning workspace&#x27;s media library and streams the underlying file with the correct Content-Type (FR-028). The split path shape keeps the workspace and media IDs independently validated while preserving the opaque ref for resolution. Returns 403 if the caller is not scoped to the owning workspace, 404 if the ref is unknown or no workspace library is available for it, and 500 if the workspace library exists but could not be opened (a genuine backend failure, distinct from a routine absent ref) or if the entry is stranded (manifest present, bytes quarantined — a server-side data-integrity fault).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "workspace_id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "media_id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.void(),
    errors: [
      {
        status: 400,
        description: `Bad request — invalid workspace_id or media_id.`,
        schema: ErrorResponse,
      },
      {
        status: 403,
        description: `Forbidden — caller workspace does not own this media ref.`,
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
        status: 500,
        description: `Internal server error.`,
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
    description: `Two-phase commit: probes the submitted provider API key against the real provider (a billable upstream call, same validator as PUT /providers/{id}), then writes the LLM provider config and admin user to config.json atomically, then marks onboarding complete in state.json. Returns 400 when the provider confirms the key is wrong (invalid_key) — nothing is persisted and the request may be retried with a corrected key. A key the provider could not verify for any other reason (unreachable, no credit, regionally restricted, or no endpoint to probe) does NOT block: onboarding still completes and the response&#x27;s &#x60;warning&#x60; field explains what could not be checked, because this endpoint is the only door into the product and a flaky network must not make it uninstallable. Returns 409 when onboarding is already complete. CSRF-exempt (no cookie exists yet). Rate-limited: 3 requests per IP per minute — a probe can take up to ~25s (model-catalog fetch + completion probe), so a mistyped key costs real wall-clock time before the caller can retry. On success, issues a __Host-csrf cookie so the SPA can immediately make CSRF-protected requests.
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
        description: `Conflict — e.g. resource already exists.`,
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
        description: `Conflict — e.g. resource already exists.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/performance",
    alias: "getPerformanceSettings",
    description: `Returns the max-parallel-agents cap and the effective (resolved, auto-detected or explicit) value currently in use.
`,
    requestFormat: "json",
    response: PerformanceSettings,
    errors: [
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
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
    method: "put",
    path: "/performance",
    alias: "updatePerformanceSettings",
    description: `Updates max_parallel_agents. An explicit value is honored exactly as given — there is no ceiling, only a floor of 1; a value is never silently lowered. Set to 0 to restore the auto-detected default (available memory / ~3.5 MB per agent, floored at 2, physically bounded around 2000). Requires a gateway restart to take effect (requires_restart: false — the semaphore is resized in-memory on PUT).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: PerformanceSettingsUpdate,
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
        status: 503,
        description: `dev_mode_bypass is active (RequireNotBypass guard).`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/plans/:id",
    alias: "getPlan",
    description: `Returns a single plan with &#x60;progress&#x60;/&#x60;plan_phase&#x60;/&#x60;failed_reason&#x60; server-computed read-time (ADR-049 D1).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Plan,
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
    path: "/plans/:id",
    alias: "updatePlan",
    description: `Partially updates plan fields (PATCH semantics — only provided fields change). &#x60;state&#x60; drives the canonical 5-value state machine; illegal transitions are rejected 400. Use POST /plans/{id}/approve for the tiered-DoD-checked draft-&gt;approved transition rather than setting &#x60;state&#x60; directly here (this endpoint applies &#x60;plan.ValidateStateTransition&#x60; with no DoD/criteria gating).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: PlanUpdateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Plan,
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
    path: "/plans/:id",
    alias: "deletePlan",
    description: `Deletes a plan by ID. A &#x60;running&#x60; plan cannot be deleted (409) — stop it first via POST /plans/{id}/stop. Deleting a non-running plan clears &#x60;plan_id&#x60; on its member tasks (best-effort, SD-A5).
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
        description: `Conflict — e.g. resource already exists.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/plans/:id/approve",
    alias: "approvePlan",
    description: `Transitions a &#x60;draft&#x60; plan to &#x60;approved&#x60; (ADR-049 D1/D5, Round-1 Grill Reconciliation R1). Runs the tiered Definition-of-Done check (strict for agent-authored plans, soft for human/UI-authored plans) and the UNCONDITIONAL member-task-criteria gate (FR-084 — every member task must carry &gt;&#x3D;1 criterion in every tier). On success the single plan-engine instance auto-advances &#x60;approved&#x60; -&gt; &#x60;running&#x60; on its next tick and begins dispatch — there is no separate &quot;start&quot; action.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Plan,
    errors: [
      {
        status: 400,
        description: `Approval rejected — either a plan-level gate (not in draft state, or a strict-tier plan with an empty Definition of Done) or the unconditional per-task criteria gate.
`,
        schema: PlanApproveError,
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
    path: "/plans/:id/restart",
    alias: "restartPlan",
    description: `Restarts a plan previously stopped by the user (&#x60;state: failed&#x60;, &#x60;failed_reason: stopped_by_user&#x60;) — the Play route (ADR-052 FR-026). Resets every non-&#x60;done&#x60; member task to &#x60;next&#x60;/&#x60;blocked&#x60; with &#x60;attempt_count&#x60; reset to 0, resets the plan&#x27;s &#x60;judge_rounds&#x60; to 0, preserves &#x60;done&#x60; members and their evidence, clears &#x60;failed_reason&#x60;, and transitions the plan to &#x60;approved&#x60; (NOT directly to &#x60;running&#x60;) via a store-level reason-aware guard that permits only &#x60;failed[stopped_by_user] -&gt; approved&#x60; — the engine then promotes &#x60;approved -&gt; running&#x60; under the global active-loop cap on its next tick, exactly like a first execute (restarting straight to &#x60;running&#x60; would skip cap admission). Rejected 409 when the plan is not &#x60;failed&#x60;, or its &#x60;failed_reason&#x60; is not &#x60;stopped_by_user&#x60; (e.g. &#x60;judge_rounds_exhausted&#x60; or &#x60;idle_expired&#x60; are not restartable — no Play offered for those). Rejected 400 on a malformed request.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Plan,
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
        description: `Conflict — e.g. resource already exists.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/plans/:id/stop",
    alias: "stopPlan",
    description: `Transitions a &#x60;running&#x60; plan to &#x60;failed&#x60; with &#x60;failed_reason: stopped_by_user&#x60; (ADR-049 D4, SD-C5 — Stop/Clear may be optimistic client-side, as this cannot validation-fail). Rejected 400 when the plan is not currently &#x60;running&#x60;.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Plan,
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
    path: "/preview/:agent_id/:token/:path",
    alias: "getPreview",
    description: `Serves static files or proxies dev-server requests for the given agent and token. No bearer authentication required — the path token IS the credential (FR-023). Served on the MAIN gateway listener at the /preview/ path prefix (ADR-044) — there is no separate preview listener/port/origin. Gated by the live gateway.preview_enabled flag: when disabled the endpoint returns 404 with no restart required. Unknown or expired tokens return 404. All HTTP methods are proxied (previewed apps may POST). Static files: path-traversal guard, MIME detection, buffered/streaming. Dev-server: reverse-proxied to loopback port with CSP injection; the proxy strips inbound Cookie/Authorization and neutralizes reserved Set-Cookie so a previewed app cannot read or plant the gateway session/CSRF cookies.
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
    method: "get",
    path: "/providers/model-capabilities",
    alias: "listModelCapabilities",
    description: `Returns the in-repo capability catalog (pkg/providers/capabilities) as a flat list of {id, modalities} pairs (D18). Model vision capability is not knowable client-side at all otherwise — the SPA resolves the target agent&#x27;s model against this list to show a non-blocking warning before sending a vision attachment (e.g. a live-browser annotation, or an image attached via the composer) to a model that cannot accept images. Returns an empty array when the catalog is not constructed (never a 500) — the catalog is optional and the server-side capability gate remains the authoritative backstop regardless.
`,
    requestFormat: "json",
    response: z.array(ModelCapabilities),
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
        status: 503,
        description: `dev_mode_bypass is active (RequireNotBypass guard).`,
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
        status: 503,
        description: `dev_mode_bypass is active (RequireNotBypass guard).`,
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
        status: 503,
        description: `dev_mode_bypass is active (RequireNotBypass guard).`,
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
    description: `Partial update — any subset of session_days and disabled. session_days must be a non-negative integer (floats and strings rejected). disabled must be a JSON boolean (string &quot;true&quot;/&quot;false&quot; rejected). Empty body is accepted as a no-op. Hot-reloaded (requires_restart: false).
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
        status: 503,
        description: `dev_mode_bypass is active (RequireNotBypass guard).`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/security/retention/sweep",
    alias: "triggerRetentionSweep",
    description: `Immediately purges session directories older than the configured retention window. Returns 409 if a sweep is already in progress. Returns skipped_reason&#x3D;&quot;disabled&quot; when retention is disabled. Emits audit with resource&#x3D;&quot;storage.retention.sweep&quot;.
`,
    requestFormat: "json",
    response: RetentionSweepResult,
    errors: [
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
      {
        status: 503,
        description: `dev_mode_bypass is active (RequireNotBypass guard).`,
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
    description: `Partial update — any subset of mode, allow_network_outbound, allowed_paths, ssrf_enabled, ssrf_allow_internal, ssrf.allow_internal, shell_deny_patterns. At least one field required. mode and allowed_paths are restart-gated (requires_restart&#x3D;true). SSRF and shell_deny_patterns are hot-reloaded. Protected by RequireNotBypass middleware (returns 503 when dev_mode_bypass is active).
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
    description: `Persists the new dm_scope to config.json. Session routing is cached at boot so all changes require a gateway restart. The response always includes requires_restart&#x3D;true. Emits a security audit log entry.
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
        status: 503,
        description: `dev_mode_bypass is active (RequireNotBypass guard).`,
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
    description: `Persists new global tool policies to config.json under the sandbox key. Changes are audit-logged (SEC-15).
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
        status: 503,
        description: `dev_mode_bypass is active (RequireNotBypass guard).`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/sessions",
    alias: "listSessions",
    description: `Returns root sessions (parent_session_id &#x3D;&#x3D; &quot;&quot;) visible to the authenticated user, paged, each carrying a child_count (ADR-057 US-19/FR-091). Subordinate (&quot;delegate&quot;) sessions are reached a page at a time via the parent_session_id filter, or all at once (roots and subordinates together) via flat&#x3D;true (FR-104). Supports optional filtering by agent_id and type. When some agents fail to list their sessions (e.g. filesystem error), the page still returns its healthy rows plus a populated partial_errors and a valid next_cursor (FR-098). Verifier-role sessions (type &quot;verifier&quot;, ADR-052 FR-036) are excluded by default regardless of the type filter unless include_verifier&#x3D;true is passed, and are never counted in child_count unless it is passed.
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
        schema: z
          .enum([
            "chat",
            "task",
            "channel",
            "scheduled",
            "verifier",
            "delegate",
          ])
          .optional(),
      },
      {
        name: "include_verifier",
        type: "Query",
        schema: z.boolean().optional().default(false),
      },
      {
        name: "parent_session_id",
        type: "Query",
        schema: z.string().optional(),
      },
      {
        name: "flat",
        type: "Query",
        schema: z.boolean().optional().default(false),
      },
      {
        name: "limit",
        type: "Query",
        schema: z.number().int().gte(1).optional(),
      },
      {
        name: "offset",
        type: "Query",
        schema: z.number().int().gte(0).optional(),
      },
    ],
    response: SessionPage,
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
    response: ClearAllSessionsResponse,
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
    path: "/settings/memory",
    alias: "getMemorySettings",
    description: `Returns the global memory/recap and retention settings (agents.defaults.* and storage.retention fields). Readable by any authenticated user. Never exposes secrets.
`,
    requestFormat: "json",
    response: MemorySettings,
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
    path: "/settings/memory",
    alias: "updateMemorySettings",
    description: `Writes the global memory/recap and retention settings. Reads/writes ONLY the MemorySettings fields — no merge of sibling config sections or secrets (A2/G-02). Writable by any authenticated user (operator decision, no admin gate). Uses safeUpdateConfigJSON server-side to preserve all other config fields including API keys.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: MemorySettings,
      },
    ],
    response: MemorySettings,
    errors: [
      {
        status: 400,
        description: `Invalid field value (e.g. negative session_days).`,
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
        description: `Conflict — e.g. resource already exists.`,
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
        description: `Conflict — e.g. resource already exists.`,
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
        description: `Conflict — e.g. resource already exists.`,
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
        schema: z
          .enum(["day", "week", "month", "all"])
          .optional()
          .default("month"),
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
    description: `Reports, per external-CLI runner (claude-code/codex/opencode), whether the gateway process can locate its binary — searching $PATH first, then a curated per-OS well-known-install-location list — and returns the resolved absolute path plus which strategy found it. Read-only, idempotent, and unaudited (no subprocess spawned). The SPA roster uses this to grey-out CLIs the host cannot run, and the create wizard / edit form use the returned path to prefill executor.cli_path. Pure Go probe (no shell-out).
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
    method: "post",
    path: "/system/cli-validate",
    alias: "postSystemCliValidate",
    description: `Stateless, create-time validation for an external-executor CLI: confirms the binary at cli_path actually runs and reports a version, before the operator saves the subagent_3p agent. Reuses runner.TestConnectionWithPath verbatim (the same handshake as POST /agents/{id}/runner/test) — the only requirement is that the binary runs and returns a valid version-shaped response; no per-CLI identity/name match is performed. Gated withAuth at create-parity (the same authorization as createAgent). Rejects a target that is not a regular, executable file before spawning it (classified &quot;missing-binary&quot;, no spawn attempt). Applies a dedicated rate limiter and a small per-caller in-flight concurrency cap, and emits one audit event {cli, resolved_path, reason} per call — validation spawns a caller-supplied path, unlike the unaudited cli-detect probe.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: CliValidateRequest,
      },
    ],
    response: CliValidateResponse,
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
    ],
  },
  {
    method: "get",
    path: "/system/folders",
    alias: "listHostFolders",
    description: `Returns the directories directly inside &#x60;path&#x60;, each with a verdict on whether it may be mounted into a workspace.
It exists because a web page cannot open the native folder picker and learn a real filesystem path — the browser withholds it — so without a server-side listing the only way to add a mount is to type an absolute path from memory.
It exposes nothing new: post-ADR-062 reading is open, so an agent can already read anywhere on this machine. This gives the OPERATOR the same view. Admin-authenticated, read-only, and deliberately not reachable from any agent tool — an agent that wants a folder asks for it and the operator approves, rather than browsing for one itself.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "path",
        type: "Query",
        schema: z.string().optional(),
      },
    ],
    response: HostFolderListing,
    errors: [
      {
        status: 400,
        description: `The path was relative, malformed, or not a directory.`,
        schema: ErrorResponse,
      },
      {
        status: 401,
        description: `Missing or invalid bearer token.`,
        schema: ErrorResponse,
      },
      {
        status: 404,
        description: `The path does not exist.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/tasks",
    alias: "listTasks",
    description: `Returns tasks in a workspace, filterable by status, agent, and surface. This is the unified task surface (Sprint 2) — it subsumes the former GTD /board/tasks listing. By default only top-level tasks (parent_task_id absent) and &#x60;surface: user&#x60; tasks are returned; use the filters to widen. Workspace-scoped.
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
          .enum(["inbox", "next", "in_progress", "blocked", "done", "failed"])
          .optional(),
      },
      {
        name: "agent_id",
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
    description: `Creates a new task. Lands in &#x60;inbox&#x60; regardless of input (Detail #8 landing rule). Workspace-scoped (workspace_id required in the body).
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
    description: `Returns a single task by ID, including read-time rollup.`,
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
    description: `Partially updates task fields (PATCH semantics — only provided fields change). Dragging a card to &#x60;in_progress&#x60; / Run is a status PATCH; there is no separate /start endpoint.
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
        description: `Conflict — e.g. resource already exists.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "delete",
    path: "/tasks/:id",
    alias: "deleteTask",
    description: `Deletes a task by ID.`,
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
    description: `Replaces the task&#x27;s &#x60;blocked_by&#x60; set atomically. A write-time DAG cycle validator rejects self-edges and cycles (max depth 50). Returns the updated task.
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
        description: `Conflict — e.g. resource already exists.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/tasks/:id/evidence",
    alias: "listTaskEvidence",
    description: `Returns every persisted EvidenceRecord for this task (ADR-049 D2, spec Part A §C) — one record per (criterion_id, attempt) machine-check execution, redacted and size-capped at write time. Read-only surface; evidence is written only by the evidence-ladder judge, never via this endpoint. Returns an empty array for a task with no machine checks.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.array(EvidenceRecord),
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
    path: "/tasks/:id/restart",
    alias: "restartTask",
    description: `Restarts a standalone (non-plan-member) task previously stopped by the user (&#x60;status: failed&#x60;, &#x60;cancel_reason: stopped_by_user&#x60;) — the Play route (ADR-052 FR-026). Resets &#x60;attempt_count&#x60; to 0, clears &#x60;cancel_reason&#x60;, and transitions the task to &#x60;next&#x60; so the goal loop picks it up again. Rejected 409 when the task belongs to a plan (restart the plan instead, via POST /plans/{id}/restart, which re-runs its non-done members) or is not in a restartable state (not &#x60;failed&#x60;, or &#x60;failed&#x60; for a reason other than &#x60;stopped_by_user&#x60;).
`,
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
      {
        status: 409,
        description: `Conflict — e.g. resource already exists.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/tasks/:id/runs",
    alias: "listTaskRuns",
    description: `Returns every execution record (TaskRun) for a task, newest first (ADR-050 / task-run-history-spec §3.6) — the authoritative history list, independent of whether the task&#x27;s current trigger can still project a run&#x27;s occurrence_ms (a series whose schedule was edited still lists every past run). Retention-bounded (day-partitioned sweep with a keep-newest-day floor); full result strings. Read-only; no state change. Rate-limited by the same dedicated taskReadLimiter (240 requests/min) as GET /tasks/occurrences.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.array(TaskRun),
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
        status: 429,
        description: `Rate limit exceeded.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "post",
    path: "/tasks/:id/runs",
    alias: "runTaskNow",
    description: `Opens a new run for the task and dispatches it immediately (ADR-050 RD7 / task-run-history-spec §3.4). With &#x60;occurrence_ms&#x60;, runs that specific recurring occurrence (materialize-on-demand); without it, re-runs a normal/once task as a fresh run (prior runs are preserved). Idempotent per (task, occurrence_ms) against a concurrent scheduler fire. Returns 202 — the run executes asynchronously; observe progress via the task_run_status WS frame or GET /tasks/{id}/runs.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z
          .object({ occurrence_ms: z.number().int().nullable() })
          .partial()
          .optional(),
      },
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
        description: `Invalid task ID or request body.`,
        schema: z.void(),
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
        status: 429,
        description: `Rate limit exceeded.`,
        schema: ErrorResponse,
      },
      {
        status: 503,
        description: `Task executor unavailable (gateway degraded).`,
        schema: z.void(),
      },
    ],
  },
  {
    method: "post",
    path: "/tasks/:id/stop",
    alias: "stopTask",
    description: `Cancels the task&#x27;s in-flight worker turn (if any, via the same cancellation path as a chat /cancel) and transitions the task to &#x60;failed&#x60; with a &#x60;stopped by user&#x60; result (SD-C5 — Stop/Clear may be optimistic client-side, as this cannot validation-fail). Rejected 400 when the task is already terminal (&#x60;done&#x60;/&#x60;failed&#x60;) or &#x60;blocked&#x60; (nothing running to stop).
`,
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
    path: "/tasks/:id/subtasks",
    alias: "listSubtasks",
    description: `Returns all subtasks (children with this parent_task_id).`,
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
    description: `Replaces the task&#x27;s &#x60;todos&#x60; array atomically (Tier-1 checklist; Detail #3 of the three-tier model). Returns the updated task.
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
    method: "get",
    path: "/tasks/:id/verdicts",
    alias: "listTaskVerdicts",
    description: `Returns every judge_verdict transcript entry recorded for this task&#x27;s goal-loop attempts (ADR-049 D2, spec Part A §C / Round-1 Reconciliation R3), oldest first. Read from the task&#x27;s session transcript (the durable carrier — the live JudgeVerdictFrame WS push is the other, ephemeral carrier of the same shape). Returns an empty array for a task that has not yet been judged (e.g. no criteria, or no attempt has completed).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.array(JudgeVerdict),
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
    path: "/tasks/occurrences",
    alias: "listTaskOccurrences",
    description: `Server-side occurrence expansion for the workspace calendar (Calendar Recurrence Redesign). Expands every recurring-capable trigger the scheduler would actually arm — non-terminal AND not &#x60;surface: heartbeat&#x60;, the same predicate &#x60;OnTaskUpserted&#x60; applies before registering a job — covering &#x60;rrule&#x60; (rrule-go, normalized per the Timezone Semantics DST policy), legacy &#x60;cron_expr&#x60; (gronx, expanded in the server&#x27;s local zone, display-only per D8), and &#x60;every_ms&#x60; (a forward-only projection off the live job&#x27;s next-run instant, FR-008a). &#x60;tz&#x60; is the viewer&#x27;s IANA zone and is the day-boundary authority for bucketing — the &gt;3-occurrences-per-day threshold and &#x60;day_start_ms&#x60; are evaluated on days in this zone for every trigger flavor, regardless of each rule&#x27;s own &#x60;tz&#x60;. Range is half-open &#x60;[from_ms, to_ms)&#x60;. Responses are bucketed: spans ≤ 8×24h return raw instants for every day (Week/Day views); spans &gt; 8×24h return one &#x60;DayBucket&#x60; per query-tz day with more than 3 occurrences, raw instants for days with 3 or fewer (Month/overview views, D6). Capped at 500 instants per task per request plus a 10,000-computed- occurrence total iteration budget per task per request (arithmetic derivation, not iteration, for provably regular triggers); &#x60;truncated&#x60; signals either cap was hit. Tasks with zero occurrences in range are omitted; the result is &#x60;[]&#x60;, never null. Read-only; no state change. Rate-limited by a dedicated &#x60;taskReadLimiter&#x60; (240 requests/min), distinct from &#x60;configLimiter&#x60; and from the unthrottled task CRUD routes.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "workspace_id",
        type: "Query",
        schema: z.string(),
      },
      {
        name: "from_ms",
        type: "Query",
        schema: z.number().int(),
      },
      {
        name: "to_ms",
        type: "Query",
        schema: z.number().int(),
      },
      {
        name: "tz",
        type: "Query",
        schema: z.string(),
      },
    ],
    response: z.array(TaskOccurrenceSet),
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
    ],
  },
  {
    method: "post",
    path: "/tool-approvals/:approval_id",
    alias: "postToolApproval",
    description: `Approve, deny, or cancel a pending tool call approval.
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
    description: `The workspace record itself is removed atomically under a per-ID lock. Task-scan and channel-unbind cascade failures abort the whole delete (500) before that removal happens. A media-library cascade failure is detected AFTER the workspace record is already gone (best-effort cleanup step) — a genuine cascade failure (the media library could not be opened, or the manifest itself could not be updated) also returns 500, but unlike the two HARD steps above, the workspace itself has already been deleted by the time it is reported; a follow-up GET on the same id returns 404. A cascade outcome where the manifest was fully and correctly updated but a final on-disk cleanup step for an already-removed entry failed is NOT reported as a failure (204): every such leftover lives under the workspace&#x27;s own directory, which this same delete unconditionally removes immediately afterward, so it never survives the request.
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
    path: "/workspaces/:id/instructions",
    alias: "getWorkspaceInstructions",
    description: `Returns the current content of the workspace&#x27;s AGENT.md (Workspace / Project Instructions) from workspaces/&lt;id&gt;/AGENT.md. This text is injected as a per-turn context layer for every agent acting in the workspace. Returns empty string when the file does not exist. Returns 404 when the workspace does not exist. Requires authentication.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.object({ content: z.string() }),
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
    path: "/workspaces/:id/instructions",
    alias: "putWorkspaceInstructions",
    description: `Replaces the entire content of the workspace&#x27;s AGENT.md (workspaces/&lt;id&gt;/AGENT.md) with the provided string. Passing an empty string clears the file. The new content is returned in the response. Returns 404 when the workspace does not exist. Requires authentication.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ content: z.string().max(262144) }),
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
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
        status: 500,
        description: `Internal server error.`,
        schema: ErrorResponse,
      },
    ],
  },
  {
    method: "get",
    path: "/workspaces/:id/media",
    alias: "listWorkspaceMedia",
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: z.array(MediaLibraryEntry),
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
    path: "/workspaces/:id/media/:media_id",
    alias: "getWorkspaceMedia",
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "media_id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: MediaLibraryEntry,
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
    method: "delete",
    path: "/workspaces/:id/media/:media_id",
    alias: "deleteWorkspaceMedia",
    description: `Removes a single media-library entry (raw bytes + manifest entry) from the workspace library. Emits a media.delete audit event (FR-033). Idempotent against a concurrently-deleted entry (404 if not found). Returns the deleted entry&#x27;s projection — including a degraded-success case where the manifest entry was committed-removed but the final on-disk unlink of the already-quarantined file failed; from the client&#x27;s perspective the item is gone (a follow-up GET 404s) even though a 500 is not returned for it, so the body is the only signal of exactly what was deleted.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
      {
        name: "media_id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: MediaLibraryEntry,
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
    method: "post",
    path: "/workspaces/:id/media/attachments",
    alias: "createWorkspaceMediaAttachment",
    description: `Verifies the referenced entry exists, increments its refcount, and returns the updated MediaLibraryEntry — the handler re-reads the entry after the increment so the response reflects the new refcount/last_refcount_seen_at rather than a stale pre-increment projection.
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: z.object({ media_id: z.string().max(36).uuid() }).strict(),
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: MediaLibraryEntry,
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
    path: "/workspaces/:id/mounts",
    alias: "createWorkspaceMount",
    description: `FR-7.1/FR-5, ADR-063 D4/D6. Validates the name shape and uniqueness (400 on an invalid name, a collision with an existing mount, or a collision with an existing entry in work/; 400 also when host_path does not resolve to an existing, real, on-disk directory), resolves host_path to its realpath, and refuses (403) a target that IS or lies INSIDE $OMNIPUS_HOME (FR-7.5 — checked on the realpath-resolved target, so a symlink to it is refused too) — this is a policy refusal, not malformed input. Otherwise creates the mount and materialises it as a symlink under the workspace&#x27;s work/ directory. A target that is broad but not refused (the operator&#x27;s own home directory, the filesystem root, or a location that CONTAINS $OMNIPUS_HOME) still succeeds (201) but returns a non-empty &#x60;warning&#x60; (FR-7.4/FR-7.6) — the secret set is subtracted from every grant regardless of where it came from, so a broad mount is allowed, but the operator must be told what it covers. Takes effect immediately for every agent on the workspace — no restart (FR-8.1).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: WorkspaceMountCreateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: WorkspaceMountCreateResponse,
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
    path: "/workspaces/:id/mounts/:name",
    alias: "deleteWorkspaceMount",
    description: `FR-7.3/FR-8.6. Removes the mount&#x27;s symlink under work/ and its record from the workspace. The operator&#x27;s real folder at the mount&#x27;s host_path, and everything inside it, is never touched — only the symlink and the mount record are removed. Returns 404 both when id does not name a known workspace and when name does not name any mount on it. Takes effect immediately for every agent on the workspace — no restart (FR-8.1).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
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
    ],
  },
  {
    method: "get",
    path: "/workspaces/:id/plans",
    alias: "listWorkspacePlans",
    description: `Returns every Plan whose workspace_id matches, newest-first by created_at, with &#x60;progress&#x60;/&#x60;plan_phase&#x60;/&#x60;failed_reason&#x60; server-computed (ADR-049 D1, mirrors the removed MilestoneListResponse).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: PlanListResponse,
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
    path: "/workspaces/:id/plans",
    alias: "createWorkspacePlan",
    description: `Creates a new Plan in &#x60;draft&#x60; state (ADR-049 D1). Member tasks are linked afterward via &#x60;Task.plan_id&#x60; (same-workspace FK, validated).
`,
    requestFormat: "json",
    parameters: [
      {
        name: "body",
        type: "Body",
        schema: PlanCreateRequest,
      },
      {
        name: "id",
        type: "Path",
        schema: z.string(),
      },
    ],
    response: Plan,
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

export const WsFrameType = z.enum(["auth", "message", "cancel", "ping", "attach_session", "device_pairing_response", "session_close", "session_started", "token", "done", "error", "tool_call_start", "tool_call_result", "subagent_start", "subagent_message", "subagent_state", "subagent_end", "task_status_changed", "task_run_status", "replay_message", "replay_error", "rate_limit", "media", "agent_switched", "tool_approval_required", "session_state", "system_overload", "replay_warning", "cancel_stage", "pong", "session_close_ack", "device_pairing_request", "whatsapp_pairing", "whatsapp_pairing_subscribe", "notification", "browser_attach", "browser_input", "browser_control", "browser_detach", "browser_status", "browser_tab_action", "browser_tabs", "browser_viewport", "browser_webrtc_offer", "browser_webrtc_answer", "browser_webrtc_state", "browser_capture_hello", "browser_capture_offer", "browser_capture_answer", "browser_capture_control", "goal_status", "loop_status", "plan_status", "judge_verdict"]);

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
    code: z.enum(["media_unsupported", "provider_rejected", "request_too_large", "provider_auth_failed", "rate_limited", "network", "content_policy", "context_too_long", "tool_args", "schema", "agent_not_configured", "workspace_unavailable", "model_unavailable", "unknown"]),
    message: z.string().min(1).max(4096),
    retryable: z.boolean(),
    detail: z.string().max(2048).optional(),
  })
  .strict();

export const LLMErrorReplay = z
  .object({
    code: z.enum(["media_unsupported", "provider_rejected", "request_too_large", "provider_auth_failed", "rate_limited", "network", "content_policy", "context_too_long", "tool_args", "schema", "agent_not_configured", "workspace_unavailable", "model_unavailable", "unknown"]),
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
    notification_type: z.enum(["schedule_failed", "knowledge_drift"]),
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
    reason: z.enum(["disabled", "not_capable", "lite_build", "error", "multi_agent_capture_denied"]).optional(),
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

export const KnowledgeIndexProgressFrame = z
  .object({
    type: z.literal("knowledge_index_progress"),
    collection_id: z.string().min(1),
    workspace_id: z.string().min(1),
    phase: z.enum(["enumerating", "indexing", "idle", "failed"]),
    indexed_files: z.number().int().min(0),
    total_known: z.boolean(),
    total_files: z.number().int().min(0).optional(),
    skipped_files: z.number().int().min(0).optional(),
    error: z.string().optional(),
    updated_at: z.string().optional(),
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
  KnowledgeIndexProgressFrame,
]);

export type WsFrameType = z.infer<typeof WsFrameType>;
export type WsFrame = z.infer<typeof WsFrame>;
