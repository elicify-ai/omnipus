# Omnipus — As-Is Architecture (Evidence-Based)

**Date:** 2026-04-27
**Method:** Source-only walkthrough of `pkg/` and `cmd/`. Docs, BRDs, and `CLAUDE.md` were intentionally excluded. Every claim cites `file:line`. Anything not present in the code is flagged as a gap, not assumed.
**Scope:** Agent loop, memory & sessions, tools system (incl. MCP and skills), channels & bus.

---

## 1. Agent Loop

### 1.1 Entry points and turn engine

- `AgentLoop.Run(ctx)` is the top-level dispatcher: an infinite `select` over the message bus that hands each inbound message to `processMessage` — `pkg/agent/loop.go:1037`, `pkg/agent/loop.go:1051`, `pkg/agent/loop.go:1114`.
- A *turn* is the unit of work the loop performs for one inbound message. The turn engine is `AgentLoop.runTurn(ctx, ts *turnState)` — `pkg/agent/loop.go:2701`.
- Turn state (phase, iteration counter, parent/child correlation, cancellation handles) is defined in `pkg/agent/turn.go:49`.

### 1.2 Per-turn control flow

The loop is a classic ReAct-style tool-using loop, implemented inline rather than as a state machine:

1. **Context build** — Load history + summary from `Sessions.GetHistory` / `GetSummary`, then `ContextBuilder.BuildMessages` produces the LLM-ready message list. If the budget is exceeded, compression runs first. `pkg/agent/loop.go:2772-2820`.
2. **User-message persist** — `pkg/agent/loop.go:2823-2835`.
3. **Iterate up to `2 * MaxIterations`** — `pkg/agent/loop.go:2865-2875`. Soft limit is `agent.MaxIterations` (default 20, `pkg/agent/instance.go:140`); hard ceiling is the doubled value.
4. **Model selection** — `selectCandidates` picks primary + fallbacks, optionally routing to a light model via `routing.Router` (`pkg/agent/loop.go:2838`, `pkg/agent/instance.go:183-213`).
5. **LLM call** — `provider.Chat` or `ChatStream`, with a `FallbackChain` retrying across candidates on timeout / context overflow / auth / rate-limit errors. `pkg/agent/loop.go:3134-3191`, `pkg/agent/loop.go:3216-3242`.
6. **Tool dispatch** — for each `response.ToolCalls`:
   - normalize name (`.` → `_`), `pkg/agent/loop.go:3714`
   - run before/approval hooks, `pkg/agent/loop.go:3718-3794`
   - rate-limit check (per-agent-per-minute), `pkg/agent/loop.go:3906-3959`
   - registry execute via `Tools.ExecuteWithContext(...)`, `pkg/agent/loop.go:3966`
   - sanitize untrusted results with `PromptGuard.Sanitize` (web_search, web_fetch, browser_*, read_file), `pkg/agent/loop.go:4079-4128`
   - append result message with `ToolCallID` correlation, `pkg/agent/loop.go:4130-4164`
7. **Termination** — exit when no tool calls remain, when iteration ceiling is hit, when graceful interrupt has been requested, or on hard abort. Final response persisted, summarization optionally fired. `pkg/agent/loop.go:4337-4393`.

### 1.3 Agent construction

- `AgentInstance` (`pkg/agent/instance.go:25-64`) is the runtime agent. Fields: `ID`, `Name`, `Model`, `Fallbacks`, `Workspace`, `MaxIterations`, `MaxTokens`, `Temperature`, `ThinkingLevel`, `ContextWindow`, plus injected dependencies `Provider`, `Sessions`, `ContextBuilder`, `Tools`, optional `Router`/`LightProvider`. Constructed by `NewAgentInstance` at `pkg/agent/instance.go:67`.
- `AgentRegistry` (`pkg/agent/registry.go:35`) holds all agents in a normalized-ID map. `GetAgent` (`:88`) and `ResolveRoute` (`:98`) are the lookup paths.
- **Core / Custom differentiation is runtime-thin:** the same `AgentInstance` struct is used for both. The differences are:
  - Core agents (Jim, Ava, Mia, Ray, Max) are seeded with `Locked=true` and have their prompts compiled into the binary via the `prompts` map (`pkg/coreagent/core.go:24-150`, prompts at `:86`, seed at `:109-128`). They receive a seeded `system.*: allow` policy.
  - Custom agents come from `config.Agents.List` and use the same pipeline with no identity locks. They receive a seeded `system.*: deny` default policy.
  - There is no separate "system agent" type at runtime. The old `ScopeSystem` / `IsSystemAgent` distinction has been replaced by the per-agent `ToolPolicyCfg` filter (see §3.2).

### 1.4 Provider abstraction

- `LLMProvider` (`pkg/providers/types.go:24-52`): `Chat(ctx, messages, tools, model, options) (*LLMResponse, error)` + `GetDefaultModel()`.
- Optional capabilities expressed as separate interfaces: `StreamingProvider.ChatStream(...)`, `ThinkingCapable.SupportsThinking()`. Detected via type assertion at call sites (`pkg/agent/loop.go:3062`, `:3172`).
- Concrete providers: `claude_provider.go`, `factory_provider.go` (OpenAI-compatible incl. OpenRouter), `legacy_provider.go`.

### 1.5 Streaming

- When `StreamingProvider` is implemented and the bus has a streamer, `ChatStream` is invoked with an `onChunk(accumulated)` callback that pushes deltas to `streamer.Update(...)` — `pkg/agent/loop.go:3167-3187`.
- The streamer is finalized once at turn end (`finalizeStreamer`, `pkg/agent/turn.go:305-321`) so a single "done" frame is emitted per turn even when intermediate tool loops continue.

### 1.6 Termination, cancellation, error handling

- **Iteration**: hard ceiling `2 × MaxIterations` (`pkg/agent/loop.go:2865`).
- **Per-turn timeout**: `context.WithTimeout` from `agent.TimeoutSeconds` (`pkg/agent/instance.go:218-221`, `pkg/agent/loop.go:2709-2714`).
- **Graceful interrupt**: `ts.requestGracefulInterrupt()` (`pkg/agent/turn.go:329`) suppresses tool execution; the next assistant text is treated as final (`pkg/agent/loop.go:3017`, `:3046`).
- **Hard abort**: `ts.requestHardAbort()` (`pkg/agent/turn.go:352`) cancels both `turnCancel` and `providerCancel`, then cascades to all `childTurnIDs` (`pkg/agent/turn.go:505-514`); session is rolled back to a restore point (`pkg/agent/loop.go:4380`).
- **Context overflow**: triggers compression (`pkg/agent/loop.go:2795-2819`); other recoverable errors fall through `FallbackChain`.

### 1.7 Audit and rate limiting

- `audit.Logger` is wired into the tool registry and the loop (`pkg/agent/loop.go:259-289`); policy decisions, prompt-guard mutations, and rate-limit denials are logged. `cfg.Sandbox.AuditLog=false` disables logging but **not** policy enforcement.
- Per-agent token budgets and per-minute LLM/tool call counts are tracked via `rateLimiter.GetOrCreate` (`pkg/agent/loop.go:2883-2890`); a daily cost cap is enforced at `:2913-2935`.

---

## 2. Memory & Session

### 2.1 What "memory" actually is in code

Memory in this codebase is **conversation history + a rolling summary**. There is **no** RAG, vector index, semantic retrieval, or per-fact memory layer. Retrieval is linear playback of stored messages.

- `pkg/memory/store.go:9-42` defines the `Store` interface: `AddMessage`, `AddFullMessage`, `GetHistory`, `SetHistory`, `GetSummary`, `SetSummary`, `Truncate`, `Compact`.
- `JSONLStore` (`pkg/memory/jsonl.go`) is the canonical implementation: append-only JSONL with a sidecar `meta.json` containing summary, skip offset, and counts.
- `pkg/session/manager.go` (`SessionManager`, RWMutex over an in-memory map, JSON snapshots on `Save`) is the legacy path; new sessions use `UnifiedStore` (`pkg/session/unified.go:51-100`) which delegates message storage to a `JSONLStore` under `.context/`.

### 2.2 On-disk layout

- Per-agent root: `~/.omnipus/agents/{agentID}/sessions/`.
- Per session:
  - `meta.json` — `SessionMeta`: ID, status (`StatusActive` | `StatusArchived` | `StatusInterrupted`, `pkg/session/daypartition.go:40-50`), timestamps, per-agent compaction summaries.
  - `.context/{sanitized_key}.jsonl` — one message per line.
  - `.context/{sanitized_key}.meta.json` — `sessionMeta` (`pkg/memory/jsonl.go:35-43`) with summary, skip offset, message count.
  - `transcript.jsonl` — day-partitioned record with `EntryType` ∈ {`Message`, `Compaction`, `System`, `ToolCall`} (`pkg/session/daypartition.go:26-38`).
- Path sanitization replaces `:`, `/`, `\` with `_` for cross-platform safety (`pkg/memory/jsonl.go:92-97`).
- `~/.omnipus/agents/{agentID}/memory/daily/` is **created** by `pkg/datamodel/init.go:140-172` but **not written to** by any code in `pkg/memory` or `pkg/session` — gap.

### 2.3 Token counting and compression

- Token counting is a single chars-per-token heuristic regardless of model: `tokens = chars × 2 / 5` plus 256/media item and 12/message overhead (`pkg/agent/context_budget.go:89-131`). No provider-specific tokenizer.
- `isOverContextBudget` is checked before each LLM call (`pkg/agent/context_budget.go:161-176`).
- `windowTrim` (`pkg/agent/loop.go::windowTrim`) is the only compaction path: it evicts the oldest whole turn(s) from the in-memory window on a token budget (boundaries detected by `parseTurnBoundaries`, `pkg/agent/context_budget.go::parseTurnBoundaries`) and deletes nothing on disk. The retired LLM summariser (`maybeSummarize` / `summarizeSession` / `forceCompression`) no longer exists (`pkg/agent/window_trim_test.go` asserts those methods are never redefined).
- `Compact` (`pkg/memory/jsonl.go:405-442`) physically rewrites the JSONL to discard skipped lines.

### 2.4 Persistence and concurrency

- Atomic writes everywhere (`fileutil.WriteFileAtomic`, `os.Rename`) — `pkg/memory/jsonl.go:119-125`, `:444-459`; `pkg/session/manager.go:219-254`.
- JSONLStore uses a **64-shard mutex pool** keyed by FNV hash of session key (`pkg/memory/jsonl.go:21-77`), giving O(1) memory regardless of session count.
- `UnifiedStore` has a coarse `sync.Mutex` for directory ops (`pkg/session/unified.go:56-60`).
- Crash safety: meta is written *before* JSONL rewrite, so a crash mid-Compact leaves `Skip=0` and the old JSONL intact — readers may see "extra" messages but never lose data (`pkg/memory/jsonl.go:383-396, 427-441`).

### 2.5 Retention

- `RetentionSweep` (`pkg/session/retention_sweep.go:18-89`) deletes JSONL files older than `storage.retention.session_days` (default in `pkg/datamodel/init.go:56-60`). Default 90 days. No per-message TTL.

### 2.6 Heartbeat

- `pkg/heartbeat/service.go` is unrelated to memory: it polls a `TaskQueueChecker` and emits via the `MessageBus`. No memory/session interaction.

### 2.7 Memory gaps (in-code reality vs. apparent intent)

1. No RAG / embeddings / semantic recall.
2. No per-provider tokenizer.
3. The `memory/daily/` directory is provisioned but never used.
4. No per-message TTL; only session-level retention.
5. No memory export/backup API surfaced through the gateway.

---

## 3. Tools System

### 3.1 The `Tool` interface and registry

```go
// pkg/tools/base.go:22-30
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any
    Execute(ctx context.Context, args map[string]any) *ToolResult
    Scope() ToolScope
    RequiresAdminAsk() bool   // added by central tool registry redesign
    Category() ToolCategory   // added by central tool registry redesign
}
```

The central tool registry redesign (completed; see `docs/internal/specs/tool-registry-redesign-spec.md`) replaced the old per-agent `ToolRegistry` instances with **two shared registries**:

- **`BuiltinRegistry`** (`pkg/tools/builtin_registry.go:42`) — a single shared `map[string]Tool` (RWMutex-guarded) populated once at boot by `registerSharedTools` (`pkg/agent/loop.go:677`). All 35 native builtins live here, including the `system.*` group.
- **`MCPRegistry`** (`pkg/tools/mcp_registry.go:73`) — a separate dynamic registry populated by `MCPRegistry.RegisterServerTools` / `RegisterServerToolsWithOpts` as MCP servers connect. Eviction runs on disconnect (`EvictServer`, `:271`). Name-collision detection against builtins happens at registration time.

Registration is still explicit — `registerSharedTools` calls `New*Tool()` constructors — but each call is a one-time boot-time operation against the shared `BuiltinRegistry`, not per-agent.

- **Hidden tools have a TTL**; core tools persist (`pkg/tools/registry.go:141-149`). MCP tools begin visible immediately on `MCPRegistry`; skills promote them via `PromoteTools` (TTL=1).
- **Async tools**: `AsyncExecutor` interface (`pkg/tools/types.go:133-139`); `ExecuteWithContext` detects and dispatches async tools with a callback.

### 3.2 Central registry: per-agent policy filter

The `FilterToolsByPolicy` function (`pkg/tools/compositor.go:143`) is the primary runtime filter applied **before each LLM call**. It resolves:

```
{BuiltinRegistry.All()} ∪ {MCPRegistry.All()}
    → scope gate (ScopeCore blocks custom agents from core-only tools)
    → policy filter (GlobalPolicies then per-agent Policies map; deny > ask > allow)
    → tools[] sent to model
```

Per-agent policy lives in a `ToolPolicyCfg` struct (`pkg/tools/compositor.go:109`) with `Policies map[string]string`, `DefaultPolicy`, `GlobalPolicies`, `GlobalDefaultPolicy`. Each `AgentInstance` holds an `atomic.Pointer[tools.ToolPolicyCfg]` (`pkg/agent/instance.go:66`) updated hot by `ReloadProviderAndConfig` without rebuilding the registry.

**Admin-ask fence** (`pkg/policy/admin_ask_fence.go:56` — `ApplyAdminAskFence`): tools whose `RequiresAdminAsk()` returns `true` (all `system.*` tools) route through an approval state machine in `pkg/gateway/approvals.go`. `ApprovalState` transitions: `pending → approved | denied_*` (`approvals.go:37-65`). The agent loop pauses at the fence, the gateway emits `tool_approval_required` over the WebSocket bus, and execution resumes or a `permission_denied` result is injected into the LLM context.

### 3.3 Tool catalog and JSON schema

- Tool catalog is derived at runtime from `BuiltinRegistry.Describe()` (`pkg/tools/builtin_registry.go:112`), replacing the deleted static `builtinCatalog` slice (`pkg/tools/catalog.go`).
- File implementations include: `shell.go`, `filesystem.go`, `web.go`, `browser/`, `send_file.go`, `edit.go`, `handoff.go`, `message.go`, `mcp_tool.go`, `build_static.go`, `cron.go`, `spawn.go`, `subagent.go`, `task.go`, `skills_install.go`, `skills_search.go`, `skills_remove.go`.
- Schema export: `ToolToSchema` (`pkg/tools/types.go:141-150`) emits OpenAI/Anthropic function format `{type:"function", function:{name, description, parameters}}`.

### 3.4 System tools (`pkg/sysagent/tools`)

All 35 `system.*` tools are present and implemented in `pkg/sysagent/tools/`:
- `agent.go` (6), `project.go` (4), `task.go` (4), `channel.go` (5), `skill.go` (4), `mcp.go` (3), `provider.go` (4), `pin.go` (3), `config.go` (2), `diag.go` (4).
- These are **ordinary builtins** registered on the central `BuiltinRegistry` at boot. There is no dedicated `BuildRegistry()` for a "system agent". Per-agent policy (default `system.*: deny` seeded on every new custom agent) governs exposure. Core agents receive `system.*: allow` via their seeded policy.
- `RequiresAdminAsk()` returns `true` for all `system.*` tools, routing them through the admin-ask fence for custom-agent callers.

### 3.5 Permissions / sandboxing wiring

- Per-agent filtering runs via `FilterToolsByPolicy` (`pkg/tools/compositor.go:143`) at LLM-call assembly time, not at registry build time.
- Sandbox enforcement (Landlock/seccomp) is set up in the boot path at `pkg/agent/loop.go:672+`, independently of the tool filter.

### 3.6 Plugin loading — what's actually there

- **No `plugin.Open`** for `.so`/`.dll`.
- **No subprocess execution** of tools (the only `exec.Command` in channel/tool code is Weixin's SILK voice transcoder, a media codec — `pkg/channels/weixin/media.go`).
- **No RPC tool transport.**
- The only out-of-process extension is **MCP** (see §3.7).

---

## 4. MCP

- `pkg/mcp/manager.go:100-114` — `Manager` holds `servers map[string]*ServerConnection`; each `ServerConnection` wraps an `*mcp.Client`, a `*mcp.ClientSession`, and a discovered `[]*mcp.Tool` list.
- **Configuration**: `MCPServerConfig` with `Name`, `URL`, `Command`, `Args`, `Headers`, `EnvFile`, `Enabled` (`pkg/mcp/manager.go:124-237`). Transport is auto-detected: `Command` → stdio, `URL` → HTTP/SSE.
- **Lifecycle**: `LoadFromConfig` connects all enabled servers concurrently → `ConnectServer` initializes a session → `GetAllTools` returns `map[serverName][]*mcp.Tool` → `CallTool(server, tool, args)` invokes (`pkg/mcp/manager.go:239-330+`).
- **Unification with the native registry**: `MCPTool` wrapper (`pkg/tools/mcp_tool.go`) implements `Tool` with name `serverName:toolName` (sanitized, `:57-101`). At compose time, native tools and MCP tools merge under one registry (`pkg/tools/compositor.go:30-75`); on name collision, MCP wins. MCP tools enter as **hidden** until a skill's `allowed-tools` promotes them via TTL.

---

## 5. Skills

- **Format**: `SKILL.md` with YAML/JSON frontmatter + Markdown body. Frontmatter fields: `name`, `description`, `argument-hint`, `context`, `allowed-tools`, `model-hint`, `extra:*` (`pkg/skills/loader.go:28-39`).
- **Discovery**: `SkillsLoader` (`pkg/skills/loader.go:99-187`) scans, in priority order: workspace `{workspace}/skills/`, global `~/.omnipus/skills/`, then compiled-in builtins. `ListSkills` walks directories for `SKILL.md`.
- **Skills are not tools.** They are prompt-fragments + a declared tool allow-list. `DiscoverAllTools` (`pkg/skills/discovery.go:16-44`) extracts the `allowed-tools` field across loaded skills; the compositor then registers (or unhides) those tools subject to policy.
- **Registry**: `SkillRegistry` interface with `Search`, `GetSkillMeta`, `DownloadAndInstall` (`pkg/skills/registry.go:49-117`). `RegistryManager` aggregates registry sources; ClawHub is the only implementation (REST + hash verification, surfaces `IsMalwareBlocked` / `IsSuspicious` / `Verified` flags).
- **Promotion to runtime**: hidden tools declared by a skill are promoted via `PromoteTools` (TTL=1) so they become visible to the LLM only when the skill is active.

---

## 6. Channels & Bus

### 6.1 The `Channel` interface

```go
// pkg/channels/base.go:47-56
type Channel interface {
    Name() string
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Send(ctx context.Context, msg bus.OutboundMessage) error
    IsRunning() bool
    IsAllowed(senderID string) bool
    IsAllowedSender(sender bus.SenderInfo) bool
    ReasoningChannelID() string
}
```

Capability extensions are opt-in interfaces: `TypingCapable`, `MessageEditor`, `MessageDeleter`, `ReactionCapable`, `PlaceholderCapable`, `StreamingCapable`, `CommandRegistrarCapable` (`pkg/channels/interfaces.go:13-70`). Detected via type assertion.

### 6.2 Implementations present

- **In-process Go channels**: telegram, discord, feishu, matrix, line, qq, dingtalk, slack, irc, wecom, weixin, googlechat, onebot, maixcam, whatsapp_native.
- **External bridge**: whatsapp (WebSocket to a separate process, configured via `BridgeURL` — `pkg/channels/whatsapp/whatsapp.go:31-46`).
- All are compiled into the binary; the WhatsApp bridge is a config-driven WebSocket client, not a generalized `BridgeAdapter` type. **No separate `BridgeAdapter` interface exists in the code.**

### 6.3 Registration pattern

```go
// pkg/channels/registry.go (paraphrased)
type ChannelFactory func(*config.Config, credentials.SecretBundle, *bus.MessageBus) (Channel, error)
func RegisterFactory(name string, f ChannelFactory) // sync.Mutex-guarded map
```

Each subpackage calls `RegisterFactory` from its `init.go`:

```go
// pkg/channels/telegram/init.go:10-16
func init() {
    channels.RegisterFactory("telegram", func(...) (channels.Channel, error) {
        return NewTelegramChannel(cfg, secrets, b)
    })
}
```

This is **compile-time open** (anyone implementing the interface and importing the package gets registered), but **runtime closed** (no plugin load, no `.so`, no subprocess discovery).

### 6.4 Activation is hardcoded

`Manager.initChannels()` (`pkg/channels/manager.go:433-530`) is a **fixed if-ladder over typed config fields**:

```go
if channels.Telegram.Enabled && channels.Telegram.TokenRef != "" {
    m.initChannel("telegram", "Telegram")
}
// ... 16 more branches, including the WhatsApp Native vs Bridge fork
```

Each branch references a hand-written field in `ChannelsConfig` (`pkg/config/config.go:673-690`). Adding a new channel requires editing both `ChannelsConfig` and `initChannels()` — even though the factory map itself would accept a new name without changes.

### 6.5 Bus model

- `MessageBus` (`pkg/bus/bus.go:33-44`) carries three buffered Go channels: `inbound`, `outbound`, `outboundMedia` (default buffer 64, `:15`).
- Methods: `PublishInbound` / `PublishOutbound` / `PublishOutboundMedia` and matching read-only chan accessors (`pkg/bus/bus.go:79-99`).
- Topic model: each message has a `Channel` field (`pkg/bus/types.go:31, 45, 62`); routing by name happens in `Manager.dispatchOutbound` (`pkg/channels/manager.go:1050-1070`), which forwards to per-channel worker queues (`runWorker` at `:900+`).

### 6.6 End-to-end flow

```
Channel.HandleMessage  →  bus.PublishInbound
            ↓
AgentLoop.Run reads bus.InboundChan()
            ↓
runTurn → provider.Chat / tools / runTurn iterates
            ↓
agent publishes via bus.PublishOutbound
            ↓
Manager.dispatchOutbound → worker queue → Channel.Send
```

Channels publish *into* and the manager consumes *from* the bus — channels themselves do not subscribe directly (`pkg/channels/base.go:232-315`, `pkg/channels/manager.go:1050-1070`, `pkg/agent/loop.go:1058`).

### 6.7 Routing

`pkg/routing/router.go:27-82` is **model-tier routing**, not agent routing. `SelectModel(msg, history, primaryModel)` returns either the primary or a light model based on a complexity classifier. Agent selection lives in `AgentLoop.processMessage` (which agent owns this session/inbound).

---

## 7. Cross-cutting summary table

| Subsystem | Interface | Registry | Registration site | Runtime extension? |
|---|---|---|---|---|
| Tools (native) | `tools.Tool` | `tools.BuiltinRegistry` (shared, mutex map) | One-time boot in `registerSharedTools`; per-agent filtered by `FilterToolsByPolicy` | No (compile-in only) |
| Tools (MCP) | `tools.Tool` via `MCPTool` wrapper | `tools.MCPRegistry` (dynamic) | `MCPRegistry.RegisterServerTools` on connect; `EvictServer` on disconnect | **Yes — config-driven, stdio or HTTP/SSE subprocess** |
| Skills | `skills.SkillRegistry` (for sources) | `RegistryManager` + filesystem scan | `SkillsLoader` scans 3 dirs | **Yes — filesystem drop-in + ClawHub install** |
| Channels | `channels.Channel` (+ capability ifaces) | Factory map (`RegisterFactory`) | `func init()` per subpackage | No (also requires editing `ChannelsConfig` + `initChannels()` switch) |
| Memory | `memory.Store` | n/a (per-agent JSONL) | n/a | No |
| Providers | `providers.LLMProvider` (+ optional capability ifaces) | n/a (selected by name in agent config) | Hardcoded in factory | No |

---

## 8. Confirmed gaps (in-code, vs. apparent design)

1. No vector / semantic memory — only linear JSONL playback (`pkg/memory/jsonl.go`).
2. No provider-specific tokenization — single chars/token heuristic (`pkg/agent/context_budget.go:89-131`).
3. `memory/daily/` directory is provisioned but unused (`pkg/datamodel/init.go:140-172` vs. no writers).
4. No generalized `BridgeAdapter` type for external channels — WhatsApp encodes the bridge directly (`pkg/channels/whatsapp/whatsapp.go:31-46`).
5. No runtime plugin loading anywhere (no `plugin.Open`, no subprocess channels, no subprocess tools other than MCP servers).
6. Channel activation requires editing two files (`pkg/config/config.go` + `pkg/channels/manager.go:initChannels`) on top of `RegisterFactory`.
7. Streaming is opt-in per provider; non-streaming providers degrade silently to single-shot responses.
8. Per-turn iteration ceiling is hardcoded as `2 × MaxIterations` (`pkg/agent/loop.go:2865`); the soft limit is the only config knob.

---

## 9. Central tool registry redesign — implementation note

The central tool registry redesign (spec: `docs/internal/specs/tool-registry-redesign-spec.md`, revision 6) is **fully implemented** as of the `feature/iframe-preview-tier13` branch. Key seams:

| File | Role |
|------|------|
| `pkg/tools/builtin_registry.go` | `BuiltinRegistry` — shared, immutable-after-boot catalog of all native tools |
| `pkg/tools/mcp_registry.go` | `MCPRegistry` — dynamic MCP tool catalog; collision-checked against builtins |
| `pkg/tools/compositor.go::FilterToolsByPolicy` | Per-agent filter applied before each LLM call; logic preserved from pre-redesign |
| `pkg/agent/instance.go:66` | `atomic.Pointer[tools.ToolPolicyCfg]` — hot-swappable policy per agent |
| `pkg/gateway/approvals.go` | Approval state machine (`ApprovalState` transitions) for `ask`-policy tools |
| `pkg/policy/admin_ask_fence.go` | `ApplyAdminAskFence` — enforces admin confirmation for `RequiresAdminAsk()` tools |

Pre-redesign symbols `WireSystemTools`, `WireAvaAgentTools`, `ScopeSystem`, `IsSystemAgent`, `ComposeAndRegister`, and the static `builtinCatalog` slice are all removed. The `omnipus-system` agent ID is fictional and has no runtime representation.

---

## 10. Contract-first wire-format pipeline

Hard-constraint #8 makes every byte that crosses the gateway/SPA boundary schema-defined. The pipeline is fully wired as of Phase 7. This section describes the implementation seams; the design decisions behind it live in ADR-012, ADR-013, ADR-014, and ADR-015.

### Spec source of truth

| File | Role |
|---|---|
| `contracts/openapi.yaml` | REST endpoints, pinned to `openapi: 3.0.3` (see ADR-012) |
| `contracts/asyncapi.yaml` | WebSocket frame definitions |
| `contracts/components/schemas/` | Shared JSON Schema component definitions referenced from both OpenAPI and AsyncAPI |

The spec files are the single source of truth. Any wire type that isn't here doesn't exist as far as hard-constraint #8 is concerned.

### Generated artifacts (committed to repo)

| Directory | Tooling | Contents |
|---|---|---|
| `pkg/api/generated/` | `oapi-codegen` | Go request/response types, server interfaces, contract test fixtures |
| `src/lib/api/generated/` | `openapi-typescript` + `openapi-zod-client` + `scripts/_gen-asyncapi-types.mjs` | TypeScript types, Zod schemas for REST responses and WS frames, AsyncAPI Zod schemas concatenated into `schemas.ts` |
| `pkg/gateway/inboundschemas/` | `scripts/gen-contracts.sh` step 5 | Mirror of `contracts/components/schemas/`, embedded via `//go:embed *.yaml` for runtime inbound validation (ADR-013) |

All three are committed to the repo and regenerated by a single codegen entry point. Editing generated files directly is forbidden and will be overwritten on the next run.

### Codegen entry point

```bash
make gen-contracts   # runs scripts/gen-contracts.sh
```

The script lints both specs, regenerates every artifact, and is idempotent — running it twice on a clean tree produces zero diff. CI's `make verify-contracts` job runs codegen and fails if the working tree is dirty afterward.

### SPA edge validation (server → SPA)

Every `request<T>()` call in `src/lib/queryClient.ts` validates the response payload through the matching Zod schema:

- **Success** — payload validates; caller receives the typed object.
- **Failure** — payload is dropped, `_apiSchemaErrorCount` is incremented, and (in dev mode) a toast surfaces the schema-validation error. Production neither crashes nor renders an error UI; the dropped frame counter is the observability hook.

The same edge logic applies to inbound WS frames in `src/lib/ws.ts`: every frame is matched to its `type` field's Zod schema, dropped on failure, and counted via `_droppedFrameCount` / `_unknownFrameTypeCount`. Schema-validation failures throw `ApiSchemaError`, which `queryClient.ts` handles centrally rather than letting individual handlers retry blindly.

### Backend inbound validation (SPA → server)

The backend symmetrically validates inbound traffic when `gateway.validate_inbound: true` (default `false` for v0.1, flip target v0.2 per ADR-013):

| Direction | Helper | Schema lookup | Failure response |
|---|---|---|---|
| REST request body | `decodeAndValidate(w, r, "SchemaName", &dst, validateEnabled)` (ADR-015) | `pkg/gateway/inboundschemas/<SchemaName>.yaml` via boot-pre-compiled map | `400 Bad Request` with `{error, schema}` JSON envelope |
| WS inbound frame | `ValidateInboundFrameJSON` (Phase 7 fix-AD) | Same embed FS, keyed by frame `type` | Drop + counter increment (WS protocol has no clean error slot) |

`additionalProperties: false` is required on all `*Request.yaml` schemas (ADR-014) so unknown fields produce 400s rather than silent acceptance. Response schemas are closed by default with documented per-case exceptions (`PUT /config` body is the canonical exception).

### Pre-compile guard

`PreCompileAllInboundSchemas()` runs at gateway boot (`pkg/gateway/server.go`). It walks every YAML in the embed FS, compiles each into the runtime validator, and aborts boot on any compile error. The guard runs **unconditionally** — independent of `gateway.validate_inbound` — because a schema that fails to compile is a contract bug we never want to ship past, regardless of whether validation is currently enforcing verdicts.

The compile-failure count is exposed via `InboundSchemaCompileFailures()` for tests and (eventually) `/metrics`.

### Lint enforcement: no hand-written wire types

`scripts/check-no-handwritten-wire-types.sh` runs in CI and blocks two patterns:

1. **Go side** — any non-test, non-generated, package-level struct in `pkg/gateway/*.go` with ≥2 `json:` struct tags. Opt-out: a `// not-wire-format` comment on the type for internal-only structs.
2. **TS side** — any `export interface` or `export type = { ... }` (object literal) in `src/lib/api.ts` or `src/lib/ws.ts`. Opt-out: same `// not-wire-format` marker on the line above for internal callback/helper types.

Together with `make verify-contracts`, this prevents two classes of regression: hand-written Go wire structs that drift from the spec, and hand-written TS types that bypass the Zod safety net.

### Adding a new wire type — five steps

Per hard-constraint #8, the canonical sequence is:

1. Add the schema to `contracts/components/schemas/<TypeName>.yaml`.
2. Reference it from `contracts/openapi.yaml` (REST) and/or `contracts/asyncapi.yaml` (WS).
3. Run `scripts/gen-contracts.sh`.
4. Commit the spec change and the regenerated artifacts in one atomic commit.
5. Write the handler (Go) or consumer (TS) using the generated type only — no hand-rolled parallel struct/interface.

Any other order is a constraint violation and will trip `make verify-contracts` in CI.

### Cross-references

- **ADR-012** — OpenAPI 3.0.3 vs 3.1.0 trade-off (why the spec is pinned to 3.0.3).
- **ADR-013** — inbound validation strategy (opt-in flag, pre-compile boot guard, default-flip target, counter exposure).
- **ADR-014** — `additionalProperties` policy (closed requests, per-case responses, future CI lint).
- **ADR-015** — `decodeAndValidate` pipeline contract (body limits, schema-name string, error response shape, known typo risk and mitigations).

---

## 11. Unified goal / plan / subagent system

> **Added 2026-07-23 (post-original-walkthrough).** The original §1–§10 sweep was scoped to the agent loop, memory, tools, channels. This section records the as-built **unified goal / plan / subagent system** ratified by [ADR-053](ADR-053-unified-goal-plan-subagent.md) (Accepted), built per the [delivery brief](../design/unified-goal-plan-subagent-DELIVERY-GOAL.md) across Phases 0–2 and gated green. Design intent lives in the [v2.2 target design](../design/unified-goal-plan-subagent-target-design-v2.2.html) (marked Implemented); this section is the code-grounded reality and wins on any disagreement.

### 11.1 The thesis — one goal core, three bindings

A goal is `prompt + goal definition + acceptance criteria` (criteria in three kinds: machine / behavior / prose). **One core owns the goal shape, the claim-or-idle trigger, the question→pause, the evidence ladder, feedback steering, typed messaging, the cancel cascade, and the count/token bounds.** The core is bound three times — chat goal (agent-compiled from user intent, blue goal pill), standalone task (Board/List/Graph), plan Definition-of-Done (plan tile → Graph) — differing only in (a) the deterministic DAG dispatch engine (plan-only) and (b) the UI/data-model binding. Anti-parallel-systems discipline (delivery brief DoD-11): a second goal store, messaging envelope, claim-marker parser, or budget path is a blocking review finding.

### 11.2 The shared spine — S1–S6 (six built-once seams)

| # | Seam | Built once as | Code |
|---|---|---|---|
| **S1** | Unified goal / criteria record | one schema, authored two ways (chat = agent-compiled; task = explicit) | `pkg/agent/goal_compile.go:265` (`compileGoalIntent` + compile-time feasibility gate, FR-111/D9); `pkg/agent/goal_loop.go:42` (`/goal` command → compile/echo/confirm/amend) |
| **S2** | Durable session record + 8-state lifecycle | one persisted per-entity JSONL record; enum `queued / running / needs_input / paused / completed / failed / cancelled / timed_out` | `pkg/session` (`LifecycleRecord`, `MessageInboxStore`); plan phase `awaiting_owner_correction` at `pkg/plan/plan.go:190` |
| **S3** | SessionMessage envelope family | one inline `oneOf` + discriminator envelope (ADR-034 precedent) over `pkg/bus`'s 4th channel | `pkg/agent/session_messaging_wire.go:61,125,228` (bus consumer, per-agent wiring, content-egress filter) |
| **S4** | Owner ↔ Judge ↔ messaging ↔ plan-engine interlock | the wiring contract: Judge feedback = a `steer`; member telemetry = owner inbox; waiting-on-owner = `question(wait=true)` | `pkg/agent/plan_engine.go:2209` (`AppendCorrection`, the S4 correction handler); `pkg/plan/plan.go:325` (`OwnerSessionID` durable linkage) |
| **S5** | Budget triple | attempts + JudgeRounds + **one app-level OVERALL token budget** (D12) | `pkg/agent/budget.go:59` (`TokenBudget`, single shared pool, one lock); brake `FailedReasonBudgetExhausted` at `:52` |
| **S6** | Claim / marker family | `[goal:evidence]` + `GOAL_STATUS: met / waiting_on_user` | `pkg/agent/goal_triggers.go:102` (`goalTriggerState`: `bareClaimStreak`, `waitingOnUser`, `idleSettling`) |

### 11.3 Claim-or-idle trigger discipline (replaces after-every-turn)

The shipped chat-goal trigger that re-adjudicated after *every* worker turn (superseded in ADR-052/049 — see both) is replaced by **claim-or-idle**: the Judge fires only on (a) an explicit completion claim (`[goal:evidence]` + `GOAL_STATUS: met`) or (b) event-driven idle settlement (claimless, quiet-window debounced, per goal-id). A `GOAL_STATUS: waiting_on_user` turn **pauses with no verdict and no round burned** (the pill goes amber). The first bare-claim bounce is free (claiming stays cheaper than idling — incentive gradient G-2/G-4). State lives in `pkg/agent/goal_triggers.go:102` (`goalTriggerState`); the idle-settlement vs no-signal-penalty precedence is resolved at the S4 interlock.

### 11.4 The evidence ladder Judge (reused from ADR-052, fail-closed)

The Judge is a real verifier agent in its own session, with a three-rung AND-combined ladder: deterministic machine-check (rung 1) and behavior-scan (rung 2) execute first, prose judgment (rung 3) only if judgment criteria remain. Single reusable entrypoint: `pkg/agent/judge.go:256` (`AgentLoop.JudgeCriteria`), rung ordering + AND-combine at `:300`. It is **fail-closed** (NFR-2): a machine check that could not run returns "unable to verify" and is re-run, never scored as absent evidence (G-3); an unknown criterion kind is a fail-closed unmet (`:294`) that DOES consume the attempt. A blocked check is honestly reported, never hidden.

### 11.5 Session-control plane — typed messaging, isolated message history

Child sessions are **isolated-but-linked**: own durable `SessionID`, a typed `SessionMessage` channel as the only bridge, a curated context snapshot at spawn, with **isolated message history**.

> **Superseded by ADR-057 (verified against code at `d364a5f8`).** This paragraph previously stated that "the transcript *session id* IS shared — a child inherits its parent's `transcriptSessionID` (FR-6a) so the chat-wide `/cancel` cascade can reach sub-turns." **That is now the exact inverse of shipped behaviour**, and the shared-namespace design it described is retired. ADR-057 splits the one overloaded id into **two independent fields**:
>
> | Field | Delegated child gets | Purpose | Code |
> |---|---|---|---|
> | `transcriptSessionID` | its **OWN** (`childID`) — *not* the parent's | persistence: the child owns a real store-backed session and its own `transcript.jsonl`; the parent's transcript is deliberately **empty** of the child's writes | `pkg/agent/subturn.go:1113` (`TranscriptSessionID: childID`) |
> | `routingSessionID` | **inherited verbatim** from the parent, through the whole subtree | cancel/interrupt reachability — this, not the transcript id, is what lets a chat-wide Stop reach sub-turns | `pkg/agent/subturn.go:1130` (`childTS.routingSessionID = parentTS.routingSessionID`) |
>
> So the cascade guarantee the old wording attributed to a *shared transcript id* is real, but it is carried by `routingSessionID`. `subturn.go:1130` looks like a copy-paste bug and is load-bearing: delete it and every chat-wide Stop silently stops reaching delegated sub-turns. Defaulting happens at `pkg/agent/turn.go:406` (a root turn's `routingSessionID` = its own transcript id); the full contract is in `routingSessionID`'s doc comment at `turn.go:251-288`.
>
> Consequently the transcript **visibility filter is deleted, not replaced**, at all four former read sites (FR-034), and FR-038 forbids reintroducing one at any read boundary — there is no longer a shared transcript for a filter to act on. See `pkg/gateway/replay.go`'s `streamReplay` contract and `session.TranscriptEntry.ParentSpawnCallID`'s doc comment.

What is isolated is therefore both the message *history* (the child's turn content lives in an ephemeral in-memory store and never persists into the parent's session history) **and** the transcript *namespace*. The bus gained a 4th channel (`bus.SessionMessageChan`); `pkg/agent/session_messaging_wire.go:61` (`SetSessionMessagingStores`) wires the inbox + lifecycle stores, `:125` (`wireSessionMessagingForAgent`) wires per-agent, and `:228` (`filterSessionMessage`) applies the content-egress filter to every free-text/path field of every bus-delivered message. Ad-hoc `delegate` inboxes are keyed to the durable chat/plan id (survives a parent Stop/Play, D16); message ceiling is per-child (D15).

### 11.6 Git evidence layer (go-git, spike-gated GO)

Real per-attempt, write-set-scoped diffs back the Judge via an embedded **go-git** repository at `<workspace>/work/.git` (no cgo; the Phase-0 footprint spike returned GO at +3.04 MiB stripped). `pkg/gitevidence` owns: `Repo.Commit` at `pkg/gitevidence/commit.go:89` (boundary + write-set-scoped commit), `Repo.CheckIntegrity` at `pkg/gitevidence/integrity.go:46` (detects HEAD divergence from the last-known hash), `OpenIsolatedCheckout` at `pkg/gitevidence/isolation.go:141` (the D10 isolation ladder: system-git worktree → go-git clone → subdir), diff evidence (`pkg/gitevidence/diff.go`), and a media size-guard (`pkg/gitevidence/sizeguard.go`). Plan-lint **write-set disjointness** rejects overlapping parallel members **at approve** (not silently): `pkg/plan/lint.go:123` (`Lint`), violation kind `write_set_overlap` at `:49`.

### 11.7 Boot recovery — boot sweep + intent-log replay

A `kill -9` mid-plan no longer wedges: `pkg/agent/plan_engine.go:70` (`PlanEngine.runBootSweep`, FR-118/G-13/INV-9) reconciles every persisted non-terminal session that has no live turn under a bounded budget. Transactional multi-file corrections (append N members + edges + revision entry + plan-record patch across several files) get an all-or-nothing guarantee from the **write-ahead intent-log**, not from per-file temp+rename: `pkg/plan/intent_log.go:469` (`IntentLog.CommitCorrection`) sequences the self-contained intent through AppendIntent → MarkCommitted (fsync, the linearization point) → Apply → MarkDone; `ReplayAtBoot` classifies every intent as discard (uncommitted) / replay-forward (committed-not-done, idempotent) / already-done.

### 11.8 Correction + Play — append-only, SUPERSEDE, targeted retry; resume from last commit

Owner correction (`pkg/agent/plan_engine.go:2209`, `AppendCorrection`) supports three verbs — **append** (tail + revision entry), **supersede** (mark a done member's outcome ignored-by-Judge; the record stays immutable), **targeted retry** (retry a transient/frozen member without a full Stop/Play, D4) — each recording a revision entry, committed transactionally via the intent-log (INV-6/N-8). After commit, append/supersede auto-reset all live-round failed members (excludes frozen/done, G-10); the durable unmet signature is cleared (INV-7); the DoD stays immutable (G-11). **Play = a new `resumed_from` generation** (`pkg/agent/plan_engine.go:2499`, D13/G-12): a cancelled/failed member resumes from its **last git commit** (JudgeRounds reset to 0); a no-commit member falls back to a fresh attempt. Plan members have **no individual start/cancel/resume** (D7) — the plan owns lifecycle.

### 11.9 Token budget — one app-level OVERALL pool (D12)

SEC-26's app-level USD cap is converted to **one app-level OVERALL token budget** covering ALL workloads including core agents (D12 removes the `IsPrivilegedAgent` exemption). `pkg/agent/budget.go:59` (`TokenBudget`): every debit is a single read-modify-write under one lock (FR-173); usage is debited POST-turn from provider-reported counts, so `Consumed` may exceed `Cap` by the sum of in-flight turn costs. The ceiling is **restart-gated** (FR-177, `SetCap` at `:89` — called once at boot from config; a live ceiling change would straddle two budgets, the N-15 hazard); the live runaway-spend lever is the existing Stop/cancel cascade (per-goal-id or global), not a live token cut. Brake = `failed(budget_exhausted)` at `:52`, applied at the next turn/adjudication boundary (never mid-turn, FR-174).

### 11.10 The 9-action delegate set + message_parent

`delegate` (`pkg/tools/delegate.go`) was expanded from `run | status` to a **9-action set**: `run, status, inbox, inbox_ack, steer, respond, cancel, follow_up, peek` (enum at `:598`). A subagent can now ask its parent a question, report a checkpoint, hand back structured results, and be steered mid-run — delegation is no longer fire-and-collect-only. The child-side counterpart is **`message_parent`** (`pkg/tools/message_parent.go:5`, ADR-053 §5.1) — the first-class tool a child uses to post to its parent's durable inbox (the parent routes `correlation_id`; only a direct session/plan owner asks the human, conversationally in chat — D2). 3P (external-CLI) workers are honest fire-and-collect: `respond` = corrective re-dispatch (a new session), `needs_input`/`question` never advertised to 3P (D5). Delegation depth is configurable with a shipped backstop of 3 (D6).

### 11.11 Sandbox guard — `.git` denied by operation (D17)

Because a go-git repo is a byte-identical `.git` the real `git` CLI could `--amend`, D17 denies `.git` **by operation, not by path**: `pkg/sandbox/gitguard.go` allows `log / blame / show / diff` and denies `commit / amend / rebase / rm` (plus a kernel/Landlock + bash-policy `.git/` block so the bypass through `bash`/exec is denied, not just the tool surface). This is the security-lead Phase-1 dependency that makes the in-scope git layer safe.

### 11.12 As-built deferrals (accepted-with-issue, tracked)

Four deferrals were accepted with tracked issues rather than blocking delivery (zero live callers today; each is safe until its trigger-to-fix): the AppendCorrection **owner-authority gate** (sec-MAJOR-2), **per-member work-tree checkout** for Play-from-commit (the D13 baseline is persisted; the per-member git checkout/restore defers to the D10 worktree-isolation rung), **SetCap one-shot enforcement** (sec-MINOR-2), and the intent-log **tamper-evidence/HMAC + dir 0700 + fsync** hardening (sec-MINOR-3). See the issues linked from the delivery brief.
