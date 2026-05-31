# Omnipus System Agent Specification

> **SUPERSEDED — Historical context only.**
> This appendix describes the original design intent for an `omnipus-system` agent. That design was never implemented. The "system agent" fiction was formally retired by the **central tool registry redesign** (completed 2026-04-28).
>
> The 41 `system.*` tools described here are ordinary builtins registered on the central `BuiltinRegistry` at boot. There is no distinct `omnipus-system` agent at runtime. Per-agent `ToolPolicyCfg` (allow/ask/deny) governs which agents can call which tools.
>
> - Canonical contract: `docs/internal/specs/tool-registry-redesign-spec.md`
> - Current as-is state: `docs/internal/architecture/AS-IS-architecture.md` (§§3, 9)
>
> **Do not use this appendix for implementation guidance.**

## Appendix D — The Omnipus Meta-Agent

**Version:** 1.0 DRAFT  
**Date:** March 28, 2026  
**Parent Document:** Omnipus BRD v1.0  
**Related:** Appendix C (UI/UX Spec)  
**Status:** For Review

-----

## D.1 Purpose

This appendix specifies the Omnipus system agent — a special built-in agent that operates the Omnipus system itself. Unlike user-facing agents (General Assistant, Code Developer, etc.) that perform tasks for the user, the Omnipus agent manages the application: creating agents, configuring channels, setting up projects, teaching users how to use the system, and providing agentic guidance.

The Omnipus agent is the bridge between conversational interaction and system administration. Anything the user can do in the UI, the Omnipus agent can do via conversation.

-----

## D.2 Agent Identity

| Property | Value |
|---|---|
| ID | `omnipus-system` |
| Name | Omnipus |
| Icon | Omnipus octopus mascot (simplified head variant). Not a Phosphor icon — custom brand asset. |
| Color | Brand color (to be defined) |
| Type | System (🔒 system) |
| Prompt storage | Hardcoded in Go binary |
| Prompt visibility | Hidden from user |
| Deletable | No |
| Deactivatable | No |
| Always available | Yes — persistent across all sessions |
| Model | Uses the user's configured default provider/model |
| Workspace | None — the Omnipus agent does not have a workspace. It operates the system, not files. |
| Memory | System-level memory only (user preferences for the system, onboarding state, tour progress). Not shared with user agents. |

-----

## D.3 Roles

### D.3.1 System Operator

The Omnipus agent can perform any system operation the user can perform in the UI. It translates natural language into system actions.

**Agent management:**

| User says | Omnipus does |
|---|---|
| "Create a new agent called Financial Analyst" | Creates custom agent with name, default model, default tools. Confirms with card preview. |
| "Switch Code Developer to DeepSeek" | Changes model on the Code Developer agent. Confirms. |
| "Disable the exec tool for Personal Agent" | Updates tool allow/deny on the agent. Confirms. |
| "Activate the Research Analyst" | Enables the core Research Analyst agent. |
| "Delete my Financial Analyst agent" | Asks for confirmation (lists affected data). Deletes on confirm. |
| "Rename Work Agent to Enterprise Assistant" | Renames. Confirms. |
| "Show me all my agents" | Lists agents with status, model, task count. |

**Project management:**

| User says | Omnipus does |
|---|---|
| "Create a project called Q2 Launch" | Creates project with name. Asks for optional color and agent assignment. |
| "Add a task to Infrastructure: migrate the database" | Creates task card in the Infrastructure project Inbox. |
| "Move the migration task to Active" | Updates task status on the board. |
| "What's the status of my Q2 Launch project?" | Lists tasks by column, shows progress, flags stale items. |
| "Assign the database migration to DevOps agent" | Updates task assignment. |

**Channel configuration:**

| User says | Omnipus does |
|---|---|
| "Connect my Telegram bot, here's the token: 123..." | Configures Telegram channel, tests connection, confirms. |
| "Set up Discord" | Walks through Discord bot setup step by step. |
| "Disable the Signal channel" | Disables. Confirms. |
| "Which channels are connected?" | Lists channels with status. |

**Skill and tool management:**

| User says | Omnipus does |
|---|---|
| "Install the aws-cost-analyzer skill" | Searches ClawHub, shows the skill, asks for credentials and agent assignment, installs. |
| "What skills are available for code review?" | Searches ClawHub, presents results. |
| "Remove the git-workflow skill" | Asks for confirmation, removes. |
| "Add the GitHub MCP server" | Walks through MCP server configuration (transport, command, env vars). |

**System configuration:**

| User says | Omnipus does |
|---|---|
| "Change my default model to Sonnet" | Updates default model. Confirms. |
| "Add my OpenAI key" | Prompts for key, tests connection, saves encrypted. |
| "Run a security check" | Executes `omnipus doctor`, presents results and recommendations. |
| "Back up my setup" | Creates backup, confirms location and size. |
| "What's my total cost this week?" | Queries cost data, presents breakdown by agent with chart. |

### D.3.2 Knowledge Base

The Omnipus agent contains embedded knowledge about:

**Omnipus features and functionality:**
- How every screen works
- How to configure every setting
- How tools, skills, and MCP servers work
- How the GTD task board works
- How projects, agents, and sessions relate
- Security features and best practices
- Troubleshooting common issues

**Agentic concepts and best practices:**
- How AI agents work (context windows, tool use, memory)
- Prompting best practices for different use cases
- Model selection guidance (when to use Opus vs Sonnet vs local models)
- Cost optimization strategies
- Agent design patterns (single agent vs multi-agent, specialist vs generalist)
- Security considerations for AI agents (prompt injection, data exfiltration, exec safety)
- Heartbeat and automation design patterns

| User says | Omnipus responds with |
|---|---|
| "How does the heartbeat system work?" | Explanation of HEARTBEAT.md, interval config, HEARTBEAT_OK, async spawn for long tasks. |
| "What model should I use for code review?" | Guidance on model strengths: Opus for complex reasoning, Sonnet for speed, local for privacy. Cost comparison. |
| "What's the best agent setup for a startup team of 3?" | Suggested agent architecture: general assistant per person + shared DevOps agent. Routing configuration guidance. |
| "How do I prevent prompt injection?" | Explanation of the security layers, prompt injection defense config, best practices for untrusted input. |
| "What's the difference between a skill and a tool?" | Clear explanation: tools are built-in capabilities, skills are installable extensions from ClawHub. |

### D.3.3 Interactive Tour Guide

The Omnipus agent can provide guided tours of the application. Tours use inline rich components with navigation actions.

**Available tours:**

| Tour | Triggered by | Content |
|---|---|---|
| Welcome tour | First launch (automatic after onboarding) | Overview of main screens: Chat, Command Center, Agents, Skills & Tools |
| Chat features tour | "Show me what I can do in chat" | Tool call badges, file uploads, pin/expand, approval prompts, slash commands |
| Command Center tour | "How does the command center work?" | GTD board explanation, projects, attention section, agent monitoring |
| Agent setup tour | "Help me set up an agent" | Walk through creating a custom agent with personality, tools, heartbeat |
| Security tour | "How do I secure my setup?" | Policy modes, exec approval, credential encryption, SSRF protection, doctor |
| Skill installation tour | "How do I install skills?" | Browse ClawHub, install flow, agent assignment, credential setup |

**Tour format:**

Tours are delivered as a series of chat messages with inline cards containing:
- Explanation text
- UI element descriptions with navigation links (`[→ Open Command Center]`)
- Choice buttons to go deeper or skip ahead
- Contextual tips and best practices

Tours are not overlays, popups, or highlight animations. They are conversational — the Omnipus agent explains things and the user reads, asks questions, or clicks through. Consistent with the chat-first philosophy.

### D.3.4 Onboarding Guide

The Omnipus agent handles the post-provider-setup onboarding flow:

1. Present preconfigured agents for activation
2. Offer to set up channels, create custom agents, or start chatting
3. If user asks for help → switch to interactive tour
4. If user starts chatting → exit gracefully, switch to General Assistant

After onboarding, the Omnipus agent remains accessible for ongoing system help. User can always switch to the Omnipus session from the agent/session hierarchy.

### D.3.5 Troubleshooter

The Omnipus agent can diagnose and fix common issues:

| User says | Omnipus does |
|---|---|
| "My Telegram bot isn't responding" | Checks channel config, tests connection, reports status, suggests fixes. |
| "Why is my agent costing so much?" | Queries cost data, identifies expensive sessions, suggests model optimization. |
| "The heartbeat keeps saying OK but I expected alerts" | Reviews HEARTBEAT.md, checks if the checks are specific enough, suggests improvements. |
| "I'm getting rate limit errors" | Checks rate limit config, current usage, suggests adjustments or model fallback. |
| "My agent seems to have forgotten everything" | Checks memory files, context window usage, session state. Explains memory system. |

-----

## D.4 System Tools

The Omnipus agent has access to 41 system tools that are exclusively available to it. These tools are NOT available to user agents, not exposed via MCP, and not configurable in tool allow/deny lists. All system tool invocations are logged to the audit trail (SEC-15).

### D.4.1 Tool Overview

| Tool | Purpose |
|---|---|
| `system.agent.create` | Create a new agent |
| `system.agent.update` | Update agent config |
| `system.agent.delete` | Delete an agent |
| `system.agent.list` | List all agents with status |
| `system.agent.activate` | Activate a core agent |
| `system.agent.deactivate` | Deactivate a core agent |
| `system.project.create` | Create a project |
| `system.project.update` | Update project |
| `system.project.delete` | Delete a project |
| `system.project.list` | List projects |
| `system.task.create` | Create a task on the board |
| `system.task.update` | Update task status, assignment, column |
| `system.task.delete` | Delete a task |
| `system.task.list` | List tasks with filters |
| `system.channel.enable` | Enable a bundled or installed channel |
| `system.channel.configure` | Configure an enabled channel |
| `system.channel.disable` | Disable a channel |
| `system.channel.list` | List all channels with status and tier |
| `system.channel.test` | Test channel connection |
| `system.skill.install` | Install a skill from ClawHub |
| `system.skill.remove` | Remove an installed skill |
| `system.skill.search` | Search ClawHub |
| `system.skill.list` | List installed skills |
| `system.mcp.add` | Add an MCP server |
| `system.mcp.remove` | Remove an MCP server |
| `system.mcp.list` | List MCP servers with status |
| `system.provider.configure` | Add/update a provider |
| `system.provider.list` | List providers with status |
| `system.provider.test` | Test provider connection |
| `system.pin.list` | List pinned artifacts with filters |
| `system.pin.create` | Pin a chat response |
| `system.pin.delete` | Delete a pin |
| `system.config.get` | Read a configuration value |
| `system.config.set` | Update a configuration value |
| `system.doctor.run` | Run security diagnostics |
| `system.backup.create` | Create a backup |
| `system.cost.query` | Query cost data |
| `system.navigate` | Navigate the UI to a specific screen |

### D.4.2 Agent Management Schemas

| Tool | Parameters | Returns |
|---|---|---|
| `system.agent.create` | `name` (string, required), `description` (string), `model` (string), `provider` (string), `color` (string), `icon` (string) | `{ id, name, type: "custom", status: "active" }` |
| `system.agent.update` | `id` (string, required), any of: `name`, `description`, `model`, `provider`, `color`, `icon`, `tools_allow[]`, `tools_deny[]`, `heartbeat_enabled` (bool), `heartbeat_interval` (string) | `{ id, updated_fields[] }` |
| `system.agent.delete` | `id` (string, required), `confirm` (bool, required — must be true) | `{ id, deleted: true }` |
| `system.agent.list` | `status` (string, optional: "active"/"inactive"/"all", default "all") | `{ agents: [{ id, name, type, status, model, task_count, cost_today }] }` |
| `system.agent.activate` | `id` (string, required) | `{ id, status: "active" }` |
| `system.agent.deactivate` | `id` (string, required) | `{ id, status: "inactive" }` |

### D.4.3 Project Management Schemas

| Tool | Parameters | Returns |
|---|---|---|
| `system.project.create` | `name` (string, required), `description` (string), `color` (string), `agent_ids[]` (string[]) | `{ id, name, color, task_count: 0 }` |
| `system.project.update` | `id` (string, required), any of: `name`, `description`, `color`, `agent_ids[]` | `{ id, updated_fields[] }` |
| `system.project.delete` | `id` (string, required), `confirm` (bool, required) | `{ id, deleted: true, tasks_deleted: count }` |
| `system.project.list` | none | `{ projects: [{ id, name, color, agent_ids[], task_count, tasks_by_status: { inbox, next, active, waiting, done } }] }` |

### D.4.4 Task Management Schemas

| Tool | Parameters | Returns |
|---|---|---|
| `system.task.create` | `name` (string, required), `description` (string), `project_id` (string), `agent_id` (string), `status` (string, default "inbox") | `{ id, name, status, project_id, agent_id }` |
| `system.task.update` | `id` (string, required), any of: `name`, `description`, `status` ("inbox"/"next"/"active"/"waiting"/"done"), `agent_id`, `project_id`, `waiting_reason` (string), `waiting_followup_date` (ISO date) | `{ id, updated_fields[] }` |
| `system.task.delete` | `id` (string, required), `confirm` (bool, required) | `{ id, deleted: true }` |
| `system.task.list` | `project_id` (string, optional), `agent_id` (string, optional), `status` (string, optional) | `{ tasks: [{ id, name, status, project_id, agent_id, cost, created_at, updated_at }] }` |

### D.4.5 Channel Management Schemas

| Tool | Parameters | Returns |
|---|---|---|
| `system.channel.enable` | `id` (string, required: "telegram"/"discord"/"whatsapp"/...) | `{ id, tier, implementation, status: "ready_to_configure" }` |
| `system.channel.configure` | `id` (string, required), plus channel-specific: `token`, `phone_number`, `bot_id`, `app_id`, `app_secret`, `mode` (for WhatsApp: "personal"/"business"), etc. | `{ id, status: "connected"/"error", error_message? }` |
| `system.channel.disable` | `id` (string, required) | `{ id, status: "disabled" }` |
| `system.channel.list` | none | `{ channels: [{ id, name, tier, implementation, enabled, status, connected_since, error? }] }` |
| `system.channel.test` | `id` (string, required) | `{ id, status: "ok"/"error", latency_ms?, error_message? }` |

Channels use a hybrid in-process/bridge architecture. Go channels are compiled into the binary and communicate via the internal `MessageBus`. Non-Go channels (Signal/Java, Teams/Node.js) and community channels run as local managed child processes using the bridge protocol (JSON over stdin/stdout). From the system agent's perspective, all channels are managed identically via the same `system.channel.*` tools — the integration model (compiled-in vs. bridge) is transparent. Community channels are installed locally by the user via `omnipus channel install` or the system agent, at the user's own risk.

### D.4.6 Skill Management Schemas

| Tool | Parameters | Returns |
|---|---|---|
| `system.skill.install` | `name` (string, required), `agent_ids[]` (string[], optional), `credentials` (object, optional: key-value pairs) | `{ name, version, verified, tools_provided[], agents_assigned[] }` |
| `system.skill.remove` | `name` (string, required), `confirm` (bool, required) | `{ name, removed: true, agents_affected[] }` |
| `system.skill.search` | `query` (string, required), `sort` (string: "trending"/"new"/"popular", default "popular"), `limit` (int, default 10) | `{ results: [{ name, description, author, version, stars, verified }] }` |
| `system.skill.list` | none | `{ skills: [{ name, version, verified, tools_provided[], agents_assigned[], installed_at }] }` |

### D.4.7 MCP Server Management Schemas

| Tool | Parameters | Returns |
|---|---|---|
| `system.mcp.add` | `name` (string, required), `transport` (string: "stdio"/"sse"/"http"), `command` (string, for stdio), `args[]` (string[], for stdio), `url` (string, for sse/http), `env` (object, optional key-value), `agent_ids[]` (string[]) | `{ name, status: "connected"/"error", tools_discovered: count, error_message? }` |
| `system.mcp.remove` | `name` (string, required), `confirm` (bool, required) | `{ name, removed: true, agents_affected[] }` |
| `system.mcp.list` | none | `{ servers: [{ name, transport, status, tools_count, agents_assigned[] }] }` |

### D.4.8 Provider Management Schemas

| Tool | Parameters | Returns |
|---|---|---|
| `system.provider.configure` | `name` (string, required: "anthropic"/"openai"/"deepseek"/"groq"/"openrouter"/"ollama"/...), `api_key` (string, for cloud providers), `api_base` (string, optional) | `{ name, status: "connected"/"error", models_available[], error_message? }` |
| `system.provider.list` | none | `{ providers: [{ name, status, models_available[] }] }` |
| `system.provider.test` | `name` (string, required) | `{ name, status: "ok"/"error", latency_ms?, error_message? }` |

**Note:** `system.provider.list` never returns API keys. Credentials are write-only — the agent can set them but cannot read them back.

### D.4.9 Pin Management Schemas

| Tool | Parameters | Returns |
|---|---|---|
| `system.pin.list` | `agent_id` (string, optional), `project_id` (string, optional), `tags[]` (string[], optional), `search` (string, optional) | `{ pins: [{ id, title, agent_name, created_at, tags, content_preview }] }` |
| `system.pin.create` | `session_id` (string, required), `message_id` (string, required), `title` (string, optional — auto-generated if omitted), `tags[]` (string[], optional), `project_id` (string, optional) | `{ id, title, created_at }` |
| `system.pin.delete` | `id` (string, required), `confirm` (bool, required) | `{ id, deleted: true }` |

### D.4.10 Configuration & Diagnostic Schemas

| Tool | Parameters | Returns |
|---|---|---|
| `system.config.get` | `key` (string, required — dot notation: "security.default_policy", "gateway.bind") | `{ key, value, source: "config"/"default" }` |
| `system.config.set` | `key` (string, required), `value` (any, required) | `{ key, value, previous_value, requires_restart: bool }` |
| `system.doctor.run` | none | `{ risk_score: 0-100, issues: [{ severity: "high"/"medium"/"low", message, recommendation }], checks_passed: count, checks_failed: count }` |
| `system.backup.create` | `encrypt` (bool, default false) | `{ path, size_bytes, encrypted, created_at }` |
| `system.cost.query` | `period` (string: "today"/"week"/"month"/"custom"), `start_date` (ISO date, for custom), `end_date` (ISO date, for custom), `agent_id` (string, optional), `group_by` (string: "agent"/"day"/"session", default "agent") | `{ total_cost, total_tokens, breakdown: [{ label, cost, tokens }] }` |
| `system.navigate` | `screen` (string: "chat"/"command-center"/"agents"/"skills"/"settings"), `agent_id` (string, optional), `section` (string, optional) | `{ navigated: true, screen }` |

### D.4.11 Schema Design Principles

| Principle | Rule |
|---|---|
| Destructive ops require `confirm: true` | Delete agent, project, task, remove skill, remove MCP. The Omnipus agent prompt instructs it to always ask the user before setting confirm=true. |
| Returns are minimal | Only what the agent needs to confirm the action in chat. No full data dumps. |
| Dot-notation for config | `system.config.get("security.default_policy")` maps to JSON config structure. |
| Channel params are type-specific | Each channel type has its own required params. The agent knows which to ask for. |
| Credentials are write-only | `system.provider.configure` accepts `api_key` but `system.provider.list` never returns it. |
| All operations are audited | Every system tool call logged to SEC-15 audit trail. |

-----

## D.5 Behavioral Rules

### D.5.1 What the Omnipus Agent Does

- Operates the Omnipus system via conversation
- Answers questions about Omnipus features and capabilities
- Provides agentic knowledge and best practices
- Guides users through setup and configuration
- Provides interactive tours of the application
- Diagnoses and helps fix issues
- Navigates the UI on behalf of the user

### D.5.2 What the Omnipus Agent Does NOT Do

| Does not | Instead |
|---|---|
| Perform user tasks (write emails, research, code) | Redirects: "That's a great question for General Assistant. [→ Switch to General Assistant]" |
| Access user agent workspaces or files | System memory only. No file.read on user data. |
| Make destructive changes without UI confirmation | Destructive operations require a UI-level confirmation dialog the LLM cannot bypass. See §D.5.3. |
| Override user agent security policies | Cannot modify Landlock/seccomp rules, cannot bypass exec approval. |
| Bypass RBAC | System tool access is gated by the caller's RBAC role (SEC-19). See §D.5.4. |
| Access user credentials or API keys | Can configure them (store encrypted) but cannot read back the values. |
| Initiate actions proactively | Only responds when addressed. Does not have heartbeat or cron. |
| Cost money when idle | No background LLM calls. Only invoked when user switches to Omnipus session. |

### D.5.3 Confirmation Requirements

Operations that modify or destroy data require a **secondary confirmation mechanism** — a UI-level confirmation dialog that the LLM cannot bypass. This protects against prompt injection attacks that attempt to use the system agent to perform destructive operations.

**How it works:** When the system agent calls a destructive system tool, the gateway intercepts the call and renders a confirmation dialog in the UI (or CLI prompt) with the operation details. The user must explicitly confirm via a UI interaction (button click, CLI "yes" response). The LLM's response to "are you sure?" is NOT accepted as confirmation — only direct user input through the confirmation mechanism counts.

**Destructive operations requiring UI-level confirmation:**

| Operation | Confirmation details shown | Minimum RBAC role |
|---|---|---|
| Delete agent | Lists affected data (sessions, memory, workspace) | `admin` |
| Delete project | Lists affected tasks | `admin` |
| Remove skill | Lists affected agents | `admin` |
| Remove MCP server | Lists affected tools and agents | `admin` |
| Clear sessions / memory | Warns irreversible, shows data volume | `admin` |
| Disable channel | Warns about active session disconnection | `operator` |
| Change default policy to `allow` | Warns about security impact | `admin` |
| Change default policy to `deny` | Warns about potential tool access loss | `operator` |
| Modify security settings (`config.set` for `security.*`) | Shows before/after diff | `admin` |

**Operations that do NOT require confirmation (safe/additive):**

| Operation | Why no confirmation | Minimum RBAC role |
|---|---|---|
| Create agent | Additive, easily undone | `operator` |
| Create project | Additive | `operator` |
| Create task | Additive | `operator` |
| Install skill | Additive, easily removed | `operator` |
| Activate core agent | Additive | `operator` |
| Change model | Easily changed back | `operator` |
| Configure channel | New connection, no data loss | `operator` |
| Configure provider | New connection, no data loss | `operator` |
| List/read operations | Read-only | `viewer` |
| Run diagnostics (`doctor`) | Read-only | `viewer` |

### D.5.4 RBAC Integration

System agent operations are gated by the RBAC role (SEC-19) of the connected device/session. The system agent checks the caller's role before executing any system tool. If the caller's role lacks permission, the system agent responds with an explanation: *"That operation requires admin access. You're connected as an operator."*

| RBAC role | System tool access |
|---|---|
| `admin` | All system tools. Can perform destructive operations (with UI confirmation). |
| `operator` | All system tools except: delete agent, delete project, remove skill/MCP, clear data, modify security settings. Can create, configure, and manage day-to-day operations. |
| `viewer` | Read-only system tools only: `*.list`, `*.get`, `system.doctor.*`, `system.navigate.*`. Cannot create, modify, or delete anything. |
| `agent` | No system tool access. User agents cannot invoke system tools. |

**Note:** When no RBAC is configured (single-user mode, default), all operations are available with UI confirmation for destructive operations. RBAC restrictions only apply when SEC-19 is enabled.

-----

## D.6 Personality

The Omnipus agent's personality is hardcoded and not user-configurable.

| Trait | Expression |
|---|---|
| **Helpful** | Always provides actionable guidance. Never says "I can't help with that" without offering an alternative. |
| **Concise** | Short, clear responses. No walls of text unless the user asks for depth. |
| **Friendly but professional** | Warm tone without being overly casual. Uses the user's name if known. |
| **Proactive** | Suggests next steps after completing an action. "Done. Want me to add some tasks to this project?" |
| **Honest about limitations** | If something isn't configured yet or a feature is coming, says so directly. |
| **Non-technical by default** | Uses plain language. Introduces technical terms with explanation when needed. Adapts to the user's demonstrated technical level. |
| **Teaches through action** | When asked "how do I create a project?", creates one while explaining — doesn't just describe the steps. |

**Language:** The system agent's hardcoded prompts are in English. The agent responds in the language of the user's input — the underlying LLM handles language adaptation naturally. Localization of hardcoded prompt text is a post-v1.0 concern.

-----

## D.7 Interaction Patterns

### D.7.1 In-Chat Actions with UI Components

The Omnipus agent produces rich inline components that connect to the UI:

**Navigation actions:**
```
[→ Open Command Center]      Navigates to Command Center
[→ Chat with General Assistant]  Switches to agent session
[→ Open Settings]             Navigates to Settings
```

**Inline previews:**
```
┌──────────────────────────────────────────────────────┐
│ 🟡 📊 Financial Analyst                              │
│ "Analyzing quarterly reports and tracking stock..."   │
│ Model: claude-opus                                    │
│                                                       │
│ [→ Open Agent Profile]  [→ Chat]                     │
└──────────────────────────────────────────────────────┘
```

**Choice buttons:**
```
Want to start chatting, or set up anything else?

[→ Chat with General Assistant]
[Set up a channel]
[Create a custom agent]
[I'm good, just let me explore]
```

**Confirmation blocks:**
```
┌──────────────────────────────────────────────────────┐
│ ⚠️ Delete Financial Analyst?                          │
│                                                       │
│ This removes:                                         │
│ • 8 sessions (including active "Q1 Analysis")        │
│ • 23 memory entries                                   │
│ • Workspace files (12 files, 4.2 MB)                 │
│                                                       │
│ This cannot be undone.                                │
│                                                       │
│ [Cancel]  [Delete]                                    │
└──────────────────────────────────────────────────────┘
```

### D.7.2 Redirecting Non-System Requests

When users ask the Omnipus agent to perform user tasks:

```
[You]
Write me an email to the team about the Q2 launch

[Omnipus]
That's a task for one of your agents! General Assistant
is great for writing emails.

[→ Switch to General Assistant]

Or if you'd prefer a specialized writing agent, I can 
create one for you. [Create a writing agent]
```

The redirect is always one click away. No friction.

-----

## D.8 Technical Implementation Notes

### D.8.1 Prompt Architecture

The Omnipus agent prompt is compiled into the Go binary as a constant string. It contains:

1. **System role definition** — who the agent is and what it does
2. **System tools schema** — descriptions of all `system.*` tools
3. **Knowledge base** — embedded documentation about Omnipus features
4. **Behavioral rules** — what to do vs. not do, confirmation requirements
5. **Personality guidelines** — tone, conciseness, proactive suggestions

The prompt is substantial (estimated 10-15K tokens) but loaded only when the user opens an Omnipus session. It does not consume context in user agent sessions.

**Schema redaction for cloud providers:** When the system agent uses a cloud LLM provider (Anthropic, OpenAI, DeepSeek, etc.), the full 41-tool schema is sent to the provider's API. This exposes the complete system tool API surface to the cloud provider. To reduce this exposure:

1. **Summarized schemas for cloud providers:** When the configured LLM is a cloud provider, system tool schemas are sent in a summarized form — tool name, one-line description, and parameter names only. Full descriptions, examples, and detailed parameter schemas are omitted. This reduces the prompt from ~10-15K tokens to ~2-3K tokens and limits the information exposed.
2. **Full schemas for local providers:** When the configured LLM is a local provider (Ollama, Nemotron, etc.), full tool schemas are sent, as data does not leave the user's network.
3. **User override:** A configuration flag (`system_agent.full_schemas: true`) allows users to send full schemas to cloud providers if they prefer better tool-use accuracy over reduced exposure. Default is `false` (summarized).

### D.8.2 System Tools Implementation

System tools are implemented as Go functions that directly call Omnipus's internal APIs:

```go
// Simplified architecture
type SystemToolHandler struct {
    agentManager    *agents.Manager
    projectManager  *projects.Manager
    taskManager     *tasks.Manager
    channelManager  *channels.Manager
    skillManager    *skills.Manager
    configManager   *config.Manager
}

func (h *SystemToolHandler) Handle(tool string, params map[string]any) (any, error) {
    switch tool {
    case "system.agent.create":
        return h.agentManager.Create(params)
    case "system.project.create":
        return h.projectManager.Create(params)
    // ...
    }
}
```

These are not the same tools exposed to user agents. System tools bypass the agent-level tool policy engine (SEC-04, SEC-07) because they are system-level operations, not agent-level operations. However, three compensating controls apply:

1. **RBAC gating (SEC-19):** Every system tool invocation is checked against the caller's RBAC role. Viewers cannot modify, operators cannot destroy, only admins have full access. See §D.5.4 for the role-to-tool mapping.
2. **UI-level confirmation:** Destructive operations require a secondary confirmation mechanism (UI dialog or CLI prompt) that the LLM cannot bypass. See §D.5.3 for the full list.
3. **Audit logging (SEC-15):** All system tool invocations are logged to the audit trail with the caller's role, device ID, and operation details.

### D.8.3 Knowledge Base Updates

The embedded knowledge base is versioned with Omnipus releases. When Omnipus adds a new feature, the knowledge base is updated to explain it. This ensures the Omnipus agent always knows about the current version's capabilities.

For rapidly changing information (ClawHub trending skills, current costs), the Omnipus agent uses system tools to query live data rather than relying on embedded knowledge.

-----

## D.9 Core Agent Roster

### D.9.1 Overview

Omnipus ships with 1 system agent and 3 core agents. Users can create unlimited custom agents.

| Agent | Type | Icon | Color | Default State |
|---|---|---|---|---|
| Omnipus | System | octopus mascot | Brand (Forge Gold) | Always on — cannot deactivate |
| General Assistant | Core | `robot` | Green | Active by default |
| Researcher | Core | `magnifying-glass` | Purple | User activates |
| Content Creator | Core | `pencil-line` | Yellow | User activates |

All icons are Phosphor icon names. The UI renders them as Phosphor icon components on colored circles. No emoji in stored data or UI chrome — emoji-to-icon translation applies only to LLM chat output text.

### D.9.2 General Assistant

| Property | Value |
|---|---|
| ID | `general-assistant` |
| Name | General Assistant |
| Description | "Versatile helper for everyday tasks, research, writing, and analysis" |
| Personality | Clear, balanced, helpful. Prioritizes getting things done over perfection. Concise by default, detailed when asked. Friendly but professional. |
| Best for | Quick Q&A, email drafts, summarization, brainstorming, scheduling, daily tasks, file operations |
| Redirects to others | Deep research → Researcher. Long-form content → Content Creator. |

**Default tools:**

| Tool | Enabled | Notes |
|---|---|---|
| web_search | ✅ | |
| web_fetch | ✅ | |
| exec | ❌ | Off by default. User or Omnipus agent enables if needed. |
| file.read | ✅ | |
| file.write | ✅ | |
| file.list | ✅ | |
| browser | ❌ | |
| spawn | ✅ | |
| cron | ✅ | Everyday scheduling — reminders, briefings |
| memory | ✅ | |
| message | ✅ | Can send messages via channels |
| image.analyze | ✅ | |

### D.9.3 Researcher

| Property | Value |
|---|---|
| ID | `researcher` |
| Name | Researcher |
| Description | "Deep multi-source research with structured reports, citations, and fact-checking" |
| Personality | Thorough, methodical, evidence-driven. Cites sources. Cross-references claims. Produces structured deliverables (reports, comparisons, analyses). Pushes back when evidence is insufficient. Asks clarifying questions before starting research. |
| Best for | Competitive analysis, market research, technical comparisons, literature review, fact-checking, data gathering, structured reports |
| Redirects to others | Quick questions → General Assistant. Writing/content → Content Creator. |

**Default tools:**

| Tool | Enabled | Notes |
|---|---|---|
| web_search | ✅ | Primary tool — multi-query research |
| web_fetch | ✅ | Reads full pages for depth |
| exec | ❌ | No need to run commands |
| file.read | ✅ | Reads source documents |
| file.write | ✅ | Produces reports |
| file.list | ✅ | |
| browser | ✅ | Navigates complex sites, reads dynamic content |
| spawn | ✅ | Spawns sub-agents for parallel research |
| cron | ❌ | |
| memory | ✅ | Remembers research context, sources |
| message | ❌ | |
| image.analyze | ✅ | Analyzes charts, diagrams in research |

### D.9.4 Content Creator

| Property | Value |
|---|---|
| ID | `content-creator` |
| Name | Content Creator |
| Description | "Long-form writing, blog posts, social media, marketing copy, documentation, and storytelling" |
| Personality | Creative, audience-aware, structured writer. Thinks in outlines and drafts. Asks about tone, audience, and purpose before writing. Iterates — produces drafts, asks for feedback, revises. Adapts voice per platform (LinkedIn vs Twitter vs blog). Remembers brand voice and past content. |
| Best for | Blog posts, articles, social media content, marketing copy, documentation, newsletters, scripts, storytelling |
| Redirects to others | Quick emails → General Assistant. Research tasks → Researcher. |

**Default tools:**

| Tool | Enabled | Notes |
|---|---|---|
| web_search | ✅ | Research for writing |
| web_fetch | ✅ | Read reference material |
| exec | ❌ | No need to run commands |
| file.read | ✅ | Reads briefs, reference docs |
| file.write | ✅ | Produces content files |
| file.list | ✅ | |
| browser | ✅ | Research, competitor content analysis |
| spawn | ✅ | Spawns sub-agents for research while writing |
| cron | ❌ | |
| memory | ✅ | Remembers brand voice, style preferences, past content |
| message | ✅ | Can send drafts via channels for review |
| image.analyze | ✅ | Analyzes reference images, screenshots |

### D.9.5 How Agents Redirect

When a user asks a core agent something outside its specialty, the agent redirects gracefully:

```
User → Researcher: "Write me a blog post about AI trends"

🔬 Researcher:
That's more of a writing task — Content Creator would do a 
better job with structure, tone, and audience-awareness.

I can do the research part first if you'd like — gather 
the latest AI trends with sources — and then hand it off 
to Content Creator for writing.

[→ Switch to Content Creator]
[Research first, then hand off]
[Just do your best here]
```

The user always has the option to override and use the current agent anyway. Redirects are suggestions, not blocks.

### D.9.6 Core Agent Prompt Storage

All core agent prompts (SOUL.md and AGENTS.md equivalents) are:
- Hardcoded as Go string constants in the binary
- Not visible to the user in the UI
- Not stored as files in the workspace
- Not accessible via file.read or any tool
- Updated only through Omnipus version releases
- Not modifiable by the agent itself

```go
// internal/agents/core/roster.go
var CoreAgents = []CoreAgent{
    {
        ID:          "general-assistant",
        Name:        "General Assistant",
        Icon:        "robot",
        Color:       "green",
        Description: "Versatile helper for everyday tasks...",
        System:      generalAssistantPrompt,
        Tools:       generalAssistantTools,
        Locked:      true,
    },
    {
        ID:          "researcher",
        Name:        "Researcher",
        Icon:        "magnifying-glass",
        Color:       "purple",
        Description: "Deep multi-source research...",
        System:      researcherPrompt,
        Tools:       researcherTools,
        Locked:      true,
    },
    {
        ID:          "content-creator",
        Name:        "Content Creator",
        Icon:        "pencil-line",
        Color:       "yellow",
        Description: "Long-form writing, blog posts...",
        System:      contentCreatorPrompt,
        Tools:       contentCreatorTools,
        Locked:      true,
    },
}
```

-----

## D.10 System Tool Edge Cases & Failure Handling

### D.10.1 Error Contract

Every system tool follows the same error response format:

```json
{
  "success": false,
  "error": {
    "code": "AGENT_NOT_FOUND",
    "message": "No agent with id 'work-agent' exists",
    "suggestion": "Available agents: general-assistant, researcher, content-creator"
  }
}
```

The Omnipus agent receives this error and presents it conversationally — listing alternatives, suggesting corrections, offering next steps.

### D.10.2 Error Categories

| Error category | Code pattern | Omnipus agent behavior |
|---|---|---|
| Not found | `*_NOT_FOUND` | Lists available items. Suggests closest match. |
| Already exists | `*_ALREADY_EXISTS` | Shows existing item. Asks if user wants to update instead. |
| Invalid input | `INVALID_INPUT` | Explains what's wrong. Asks for correction. |
| Confirmation required | `CONFIRMATION_REQUIRED` | Agent asks user and retries with `confirm: true`. |
| Dependency conflict | `DEPENDENCY_CONFLICT` | Explains what depends on the item. Lists affected resources. |
| Connection failed | `CONNECTION_FAILED` | Reports error message. Suggests troubleshooting steps. |
| Permission denied | `PERMISSION_DENIED` | Explains why the operation is blocked. |
| Rate limited | `RATE_LIMITED` | Tells user to wait. Shows when next request is allowed. |
| Disk full | `DISK_FULL` | Warns user. Suggests cleanup actions. |
| Config locked | `CONFIG_LOCKED` | Explains restart requirement. Asks if user wants to proceed. |
| Dependency missing | `DEPENDENCY_MISSING` | Explains what's needed and provides installation instructions. |

### D.10.3 Agent Management Edge Cases

| Scenario | Behavior |
|---|---|
| Delete system agent | `PERMISSION_DENIED` — system agent cannot be deleted |
| Delete core agent | `PERMISSION_DENIED` — core agents can be deactivated, not deleted |
| Deactivate last active agent | Warning: "This is your only active agent. No agent can respond to messages. Are you sure?" |
| Create agent with duplicate name | `AGENT_ALREADY_EXISTS` — suggests rename or update |
| Create agent with no provider configured | Succeeds with warning: "No model provider configured. Agent can't respond until you add one." |
| Update agent during active session | Changes apply to next message. Running tool calls complete with old config. |
| Activate agent whose model provider is missing | Warning: "This agent uses deepseek-r1 but no DeepSeek provider is configured." |

### D.10.4 Project & Task Edge Cases

| Scenario | Behavior |
|---|---|
| Delete project with active tasks | Confirmation lists all tasks that will be deleted |
| Delete project with referencing sessions | Sessions NOT deleted — their `project_id` set to null |
| Move task to "active" with no agent assigned | Warning: "No agent assigned. Assign one first?" |
| Delete task with linked sessions | Sessions NOT deleted — their `task_id` set to null |
| Move task to "waiting" without reason | Succeeds but agent prompts for optional reason |

### D.10.5 Channel Edge Cases

| Scenario | Behavior |
|---|---|
| Enable channel with missing credentials | Status becomes `"awaiting_config"`. Agent prompts for credentials. |
| Configure with invalid token | `CONNECTION_FAILED` with specific API error message |
| Disable channel with active sessions | Warning lists active sessions that will be disconnected |
| Enable bridge channel without required runtime | `DEPENDENCY_MISSING` — "Signal requires signal-cli (Java). Install it first." |
| Enable community channel whose binary is missing | `PROVIDER_BINARY_MISSING` — "Mastodon channel binary not found. Re-install via `omnipus channel install`." |
| Conflicting routing rules | Agent warns: "User 123456 already routed elsewhere. New rule will override." |
| WhatsApp QR pairing timeout | QR expires after 60s. Agent shows new QR automatically. |

### D.10.6 Skill & MCP Edge Cases

| Scenario | Behavior |
|---|---|
| Skill fails SHA-256 verification | Warning: "Hash doesn't match registry. May have been modified. Install anyway?" |
| Skill requires unconfigured credentials | Installs as inactive. Agent explains what credentials are needed. |
| Remove skill used in active session | Warning: "Agent is currently using this skill. Removing may cause errors." |
| ClawHub unreachable | `CONNECTION_FAILED` — "Can't reach ClawHub. Check internet connection." |
| MCP server crashes 3 times in 5 minutes | Auto-disabled. Agent reports: "Server crashed repeatedly and has been disabled." |
| MCP server discovers 0 tools | Warning: "Connected but no tools discovered. Server may be misconfigured." |

### D.10.7 Provider Edge Cases

| Scenario | Behavior |
|---|---|
| Invalid API key | `CONNECTION_FAILED` — "API returned 401 Unauthorized. Check your key." |
| Remove provider that agents depend on | Warning lists affected agents |
| Ollama not running | `CONNECTION_FAILED` — "Can't reach Ollama at localhost:11434. Start with: `ollama serve`" |
| All providers rate-limited at once | "All model providers are rate-limited. Try again in a few minutes." |

### D.10.8 Pin, Config & System Edge Cases

| Scenario | Behavior |
|---|---|
| Pin from deleted session | `SESSION_NOT_FOUND` |
| Pin already-pinned message | `PIN_ALREADY_EXISTS` — shows existing pin title |
| Config change requires restart | Returns `requires_restart: true`. Agent asks: "Restart now?" |
| Invalid config value | `INVALID_INPUT` with valid options listed |
| Backup with insufficient disk space | `DISK_FULL` — shows required vs available space |
| Doctor finds critical issues | Returns risk score + issues list with recommendations |

### D.10.9 Rate Limiting on System Operations

| Category | Limit | Rationale |
|---|---|---|
| Create operations | 30/min | Prevents runaway creation |
| Delete operations | 10/min | Prevents accidental mass deletion |
| Config changes | 10/min | Prevents config thrashing |
| List/query operations | 60/min | Read-heavy, less risky |
| Channel operations | 5/min | External connections, rate-sensitive |
| Backup creation | 1 per 5 min | Disk I/O intensive |

If rate limit is hit, tool returns `RATE_LIMITED` with `retry_after_seconds`.

### D.10.10 Concurrency

Omnipus uses a layered concurrency strategy: per-entity files for high-contention data (tasks, pins), single-writer goroutine serialization for shared files (config, credentials), and advisory file locking (`flock`/`LockFileEx`) as defense-in-depth on all writes. See Appendix E §E.2 for full specification.

| Scenario | Behavior |
|---|---|
| Two agents update different tasks simultaneously | No conflict — each task is a separate file (`tasks/<id>.json`). Both writes succeed independently. |
| Two browser tabs editing the same agent | Writes to `agents/<id>/agent.json` are serialized via single-writer goroutine. Second write queues behind first. No data loss. |
| Two browser tabs editing the same task | Writes to `tasks/<id>.json` are serialized via advisory file lock. Second write queues behind first. No data loss. |
| Simultaneous creation with same name | Second one fails with `ALREADY_EXISTS`. |
| Agent receives message while being deactivated | In-flight message completes. Next message gets deactivation notice. |
| Backup during active session writes | Backup captures state at moment of copy. Atomic operations prevent partial reads. |
| Project deleted while agent has active task in it | Task's `project_id` set to null. Current session continues, loses project context on next load. |
| External tool edits a file while gateway holds lock | External tool blocks until advisory lock is released. If external tool ignores advisory lock, gateway detects stale read via file modification timestamp and re-reads before next write. |
| Config write during high load | Config writes are queued in the single-writer goroutine channel. Under sustained load, queue depth is bounded (default 100); excess writes return a retry-after error. |

### D.10.11 Graceful Degradation

| Failure | System behavior |
|---|---|
| Audit log locked/corrupted | Log to stderr as fallback. Warning in doctor report. |
| Pins file corrupted | Load what's parseable. Log warning. Offer repair via doctor. |
| Agent workspace missing | Auto-recreate on next access. Log warning. |
| config.json parse error | Refuse to start. Print error with line number. Suggest `omnipus config validate`. |
| credentials.json unreadable or corrupted | Refuse to start. Never fall back to plaintext credentials. Log error with instructions to provide master key via `OMNIPUS_MASTER_KEY` env var or `OMNIPUS_KEY_FILE`. |
| credentials.json missing | Start normally with no credentials. Providers requiring API keys will be unavailable until credentials are configured. |
| Master key not available (no env var, no key file, no TTY) | Start with credentials inaccessible. Log warning. Providers requiring credentials fail gracefully until key is provided. |

-----

## D.11 Open Items

| Topic | Status |
|---|---|
| Full prompt text for the Omnipus system agent | To be written when features stabilize |
| Full prompt text for 3 core agents | To be written |
| Interactive tour scripts — detailed conversation flows for each tour | To be designed |
| Knowledge base content — structured documentation for embedding | To be authored |
| Agentic best practices content — model selection, prompt design, multi-agent patterns | To be authored |
| System tool parameter schemas | ✅ Defined in D.4.2–D.4.10 |
| Core agent roster | ✅ Defined in D.9 |
| Edge cases and failure handling | ✅ Defined in D.10 |
| Telemetry — should the Omnipus agent track which features users ask about to inform roadmap? | To be decided |

-----

*End of Appendix D*
