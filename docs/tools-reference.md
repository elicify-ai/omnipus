# Tools reference

Omnipus exposes three kinds of tools to agents.

**Built-in tools** are compiled into the binary, registered on per-agent `ToolRegistry` instances by `pkg/agent/loop.go` and `pkg/agent/instance.go`. Names use snake_case (`read_file`) for the original set and dotted prefixes (`workspace.shell`, `browser.navigate`) for newer namespaced families.

**System tools (`system.*`)** are 39 administrative tools defined in `pkg/sysagent/tools/`. Available to any agent whose per-agent policy allows them; custom agents have `system.*: deny` seeded by default. There is no separate "system agent" runtime — these are ordinary builtins governed entirely by policy.

**MCP tools** are registered at runtime from Model Context Protocol servers configured under `mcp_servers` in `config.json`. Each tool is namespaced by the server name; the registry merges them into the same per-agent surface that the builtins occupy.

The canonical tool name for any builtin is whatever `Name() string` returns on its concrete type. To find a tool, grep for `Name() string` in `pkg/tools/`, `pkg/tools/browser/`, or `pkg/sysagent/tools/`. The same name is used for policy matches (`pkg/tools/compositor.go:94-106`), audit logging, and `tool_call` frames over the WebSocket.

## Built-in tools

### Files

| Tool | What it does | Args (key) | Notes |
|---|---|---|---|
| `read_file` | Read a file from the agent workspace; truncates at 64 KiB with continuation offsets. | `path`, optional `offset` | `pkg/tools/filesystem.go:332`. |
| `write_file` | Atomic write (temp + rename) of full file contents. | `path`, `content` | `pkg/tools/filesystem.go:567`. |
| `edit_file` | Block-anchored find/replace edit. Fails if the anchor is ambiguous or missing. | `path`, `find`, `replace` | `pkg/tools/edit.go:28`. |
| `append_file` | Append to the end of an existing file. | `path`, `content` | `pkg/tools/edit.go:94`. |
| `list_dir` | List a directory's entries (file/dir, name, size). | `path` | `pkg/tools/filesystem.go:639`. |

All five route through the per-agent workspace path guard. On Linux with sandbox `enforce`, Landlock filesystem rules pin the writable area to `$OMNIPUS_HOME` plus any `sandbox.allowed_paths` entries.

### Code execution

| Tool | What it does | Notes |
|---|---|---|
| `exec` | PTY-capable shell execution with operator approval, sandbox.Run on the hardened path, and per-binary allow-lists. Supports background sessions with poll/read/write/send-keys/kill. | `pkg/tools/shell.go:445`. Scope is `ScopeCore` — only core agents see it on the registry; custom agents need explicit policy. |
| `workspace.shell` | Foreground shell execution inside the agent workspace under the configured sandbox profile (`workspace`, `workspace+net`, `host`, `off`). Returns `exit_code`, `stdout`, `stderr`, `duration_ms`. | `pkg/tools/workspace_shell.go:140`. Gated by `experimental.workspace_shell_enabled` (default `false`; Jim's seed forces `true`). |
| `workspace.shell_bg` | Background variant of `workspace.shell` with a managed dev-server port from `sandbox.dev_server_port_range`. | `pkg/tools/workspace_shell_bg.go:183`. Same enable-gate as `workspace.shell`. |
| `build_static` | Tier 2 build command (e.g. `npm run build`) with egress proxy and audit-log fail-closed by default. | `pkg/tools/build_static.go:153`. |
| `web_serve` | Unified Tier 1 (static serve) + Tier 3 (dev-server proxy) tool. Mints a preview-origin URL on the second listener port. | `pkg/tools/web_serve.go:201`. |

Per-agent sandbox profile (`workspace`, `workspace+net`, `host`, `off`) is defined in `pkg/config/sandbox.go:85-98` and applied to the child process by the hardened-exec path.

### Web

| Tool | What it does | Notes |
|---|---|---|
| `web_search` | Search the web through one of seven configurable providers. | `pkg/tools/web.go:1052`. Provider selection priority: **Perplexity → Brave → SearXNG → Tavily → DuckDuckGo → Baidu Search → GLM Search** (`pkg/tools/web.go:944-1043`). The first one with credentials wins. |
| `web_fetch` | Fetch a URL and extract readable content (HTML → text). | `pkg/tools/web.go:1209`. |

Both route every outbound HTTP request through `SSRFChecker.SafeClient()` when `sandbox.ssrf.enabled` is true, blocking RFC1918, link-local, cloud-metadata, and IPv6 wrapping ranges.

### Tasks

| Tool | What it does | Notes |
|---|---|---|
| `task_create` | Create a task pinned to the current agent or session. | `pkg/tools/task.go:93`. |
| `task_update` | Update task status / title / notes. | `pkg/tools/task.go:223`. |
| `task_delete` | Delete a task by ID. | `pkg/tools/task.go:331`. |
| `task_list` | List the agent's tasks. | `pkg/tools/task.go:23`. |

Tasks are stored per-agent as JSON files; concurrency goes through the `fileutil` flock + atomic-write helpers.

### Agents

| Tool | What it does | Notes |
|---|---|---|
| `agent_list` | List configured agents (core and custom). | `pkg/tools/task.go:378` (lives in this file historically). |
| `subagent` | Execute a subagent task synchronously and return the result. | `pkg/tools/subagent.go:333`. |
| `spawn` | Spawn a subagent asynchronously in the background. | `pkg/tools/spawn.go:38`. |
| `spawn_status` | Get the status of spawned subagents. | `pkg/tools/spawn_status.go:24`. |
| `handoff` | Hand the conversation off to another agent. The receiving agent's prompt and tools take over. | `pkg/tools/handoff.go:99`. |
| `return_to_default` | Return control to the default agent after a handoff. | `pkg/tools/handoff.go:312`. |

### Browser

Registered by `pkg/tools/browser/register.go:39-49`. All seven are namespaced under `browser.*`:

| Tool | What it does | Notes |
|---|---|---|
| `browser.navigate` | Drive the managed Chromium to a URL. | `pkg/tools/browser/tools.go:32`. |
| `browser.click` | Click a CSS selector. | `pkg/tools/browser/tools.go:113`. |
| `browser.type` | Type into a selector. | `pkg/tools/browser/tools.go:158`. |
| `browser.screenshot` | Capture a PNG screenshot. | `pkg/tools/browser/tools.go:209`. |
| `browser.get_text` | Extract text content from the current page. | `pkg/tools/browser/tools.go:285`. |
| `browser.wait` | Wait for a selector / timeout. | `pkg/tools/browser/tools.go:338`. |
| `browser.evaluate` | Execute arbitrary JavaScript in the page context. | `pkg/tools/browser/tools.go:397`. Gated by `sandbox.browser_evaluate_enabled` (default `false`); registration is skipped when the flag is off. |

### Skills

| Tool | What it does | Notes |
|---|---|---|
| `find_skills` | Search installable skills from configured skill registries (ClawHub etc.). Returns slugs, descriptions, versions, and relevance scores. | `pkg/tools/skills_search.go:29`. |
| `install_skill` | Install a skill by slug. SHA-256 verified against `skill_trust` policy. | `pkg/tools/skills_install.go:39`. |
| `remove_skill` | Uninstall a skill by name. | `pkg/tools/skills_remove.go:22`. |

### Memory

| Tool | What it does | Notes |
|---|---|---|
| `remember` | Persist a fact, decision, reference, or lesson to long-term memory (`MEMORY.md`). | `pkg/tools/memory.go:90`. |
| `recall_memory` | Search durable memory (`MEMORY.md`), recent recaps (`LAST_SESSION.md`), and indexed session JSONL. | `pkg/tools/memory.go:262`. |
| `retrospective` | Record a session retrospective after confirming its contents with the user. | `pkg/tools/memory.go:353`. |

Note: the tool name for recall is `recall_memory`, not `recall`. The v0.3 Rooms redesign (`docs/internal/design/memory-redesign-2026-05.md`) plans a renamed three-tool surface (`remember`/`recall`/`retrospective`) but the current code ships `recall_memory`.

### Messaging

| Tool | What it does | Notes |
|---|---|---|
| `message` | Send a chat message to the user on the current channel. | `pkg/tools/message.go:21`. |
| `send_file` | Send a local file (image, document, etc.) to the user on the current channel. | `pkg/tools/send_file.go:55`. |

### Scheduling

| Tool | What it does | Notes |
|---|---|---|
| `cron` | Schedule reminders or commands. Single tool with five operations: `add`, `list`, `remove`, `enable`, `disable` — pass the operation in the `op` argument. | `pkg/tools/cron.go:68-87`. |

There is no separate `cron_list` or `cron_delete` builtin — `cron` is one tool with operation-style dispatch.

### Discovery (hidden tools)

| Tool | What it does | Notes |
|---|---|---|
| `tool_search_tool_regex` | Search hidden tools by regex against name or description. Returns JSON schemas. | `pkg/tools/search_tool.go:30`. |
| `tool_search_tool_bm25` | BM25 keyword search across the same surface. | `pkg/tools/search_tool.go:94`. |

These exist so an agent can opt into a large hidden-tool surface on demand rather than paying the context cost up front.

## System tools (`system.*`)

Defined in `pkg/sysagent/tools/registry.go:13-74` as a flat list of 39 tools. Per-agent policy decides which agent can call which one — by default `SeedConfig` ships custom agents with `"system.*": "deny"` and a more permissive set for the core operator agent. Wildcards (`system.*`, `system.agent.*`) are honored with most-specific-prefix wins (`pkg/tools/compositor.go:51-106`).

Grouped by namespace:

| Namespace | Count | Tools | Source |
|---|---|---|---|
| `system.agent.*` | 6 | `create`, `update`, `delete`, `list`, `activate`, `deactivate` | `pkg/sysagent/tools/agent.go` |
| `system.project.*` | 4 | `create`, `update`, `delete`, `list` | `pkg/sysagent/tools/project.go` |
| `system.task.*` | 4 | `create`, `update`, `delete`, `list` | `pkg/sysagent/tools/task.go` |
| `system.channel.*` | 5 | `enable`, `configure`, `disable`, `list`, `test` | `pkg/sysagent/tools/channel.go` |
| `system.skill.*` | 4 | `install`, `remove`, `search`, `list` | `pkg/sysagent/tools/skill.go` |
| `system.mcp.*` | 3 | `add`, `remove`, `list` | `pkg/sysagent/tools/mcp.go` |
| `system.provider.*` + `system.models.list` | 4 | `configure`, `list`, `test`, `models.list` | `pkg/sysagent/tools/provider.go` |
| `system.pin.*` | 3 | `list`, `create`, `delete` | `pkg/sysagent/tools/pin.go` |
| `system.config.*` | 2 | `get`, `set` | `pkg/sysagent/tools/config.go` |
| Utilities | 4 | `system.doctor.run`, `system.backup.create`, `system.cost.query`, `system.navigate` | `pkg/sysagent/tools/diag.go`, `navigate.go` |

`pkg/sysagent/tools/` is the source of truth for the exact set; the BRD's `Omnipus_BRD_AppendixD_System_Agent.md` describes the original 35 and predates the four utility additions.

## MCP tools

Configured under `mcp_servers` in `config.json`. Each server's tools are discovered at connection time and registered into the per-agent registry as `<server_name>.<tool_name>` via the `MCPRegistry` (`pkg/tools/mcp_registry.go`) and the `MCPTool` adapter (`pkg/tools/mcp_tool.go:109`). The namespacing means an MCP server called `slack` exposing a tool called `post_message` shows up to agents as `slack.post_message`, and policy matches against that full name.

MCP tools are filtered through the same `FilterToolsByPolicy` pass as builtins. There is no separate trust tier — wildcards like `slack.*` work identically.

## Per-agent policy

Every tool decision routes through `FilterToolsByPolicy` in `pkg/tools/compositor.go:181-260`. Resolution uses strictest-wins semantics, evaluated in order: first global policy (`cfg.Sandbox.ToolPolicies` plus `cfg.Sandbox.DefaultToolPolicy`, `pkg/config/sandbox.go:339-346`), then agent policy (the per-agent `Policies` map and `DefaultPolicy`).

The three legal values are `allow`, `ask`, and `deny`. `deny > ask > allow` — denial at any layer wins. Empty values default to `allow`. Trailing-`.*` wildcards are supported (`browser.*`, `system.agent.*`, `mcp_server_name.*`); exact-name matches always beat wildcards, and among wildcards the most-specific prefix wins (`pkg/tools/compositor.go:51-106`).

A separate "admin-ask fence" (FR-061) downgrades `allow` to `ask` for tools that return `RequiresAdminAsk() == true` when the agent is not a core agent. Core agents (any agent whose ID matches a `coreagent.GetPrompt(id)` entry) skip the fence entirely. The fence is intended for sysagent-style administrative tools that should always require operator approval on custom agents.

Validation of policy values runs at config-load time (`pkg/config/validate.go:192-248`): an unknown policy value (`"alow"`) is rejected at decode with a clear error, not silently treated as the zero value.
