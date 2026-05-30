# Omnipus Data Model Specification

## Appendix E — Data Model & Storage

**Version:** 1.0 DRAFT  
**Date:** March 28, 2026  
**Parent Document:** Omnipus BRD v1.0  
**Related:** Appendix B (Features), C (UI), D (System Agent)  
**Status:** For Review

-----

## E.1 Purpose

This appendix defines the complete data model for Omnipus: all entities, their relationships, storage format, and directory structure. Omnipus uses a file-based storage approach — JSON and JSONL files on disk, no database — consistent with Omnipus's zero-dependency philosophy.

-----

## E.2 Storage Philosophy

| Principle | Rule |
|---|---|
| No database dependency | All Omnipus data stored as JSON/JSONL files. No PostgreSQL or Redis. Exception: WhatsApp session uses SQLite via whatsmeow with `modernc.org/sqlite` (pure Go, no CGo). SQLite is isolated to WhatsApp session storage only — never used for Omnipus's own data model. |
| Human-readable | All config and data files are readable and editable with a text editor. |
| Atomic writes | Write to temp file, then rename. Prevents corruption on crash. |
| Append-only where possible | Memory and audit logs use JSONL (append-only). No in-place mutation. |
| Credentials separated | API keys and tokens stored in `credentials.json` (AES-256-GCM encrypted, Argon2id KDF), never in `config.json`. Config references credentials by name (`_ref` suffix). See main BRD SEC-23a–e for full key management specification. |
| Per-entity files for high-contention data | Entities that are frequently updated by multiple agents or concurrent UI sessions use one file per entity (e.g., `tasks/<id>.json`, `pins/<id>.json`). This eliminates contention — two agents updating different tasks never conflict. |
| Single-writer serialization for shared files | Low-contention shared files (`config.json`, `credentials.json`) are protected by a single-writer goroutine pattern: all writes are serialized through a dedicated Go channel. Concurrent reads are allowed; writes are queued and executed in order. |
| Advisory file locking as defense-in-depth | All file write operations acquire an OS-level advisory lock (`flock` on Linux, `LockFileEx` on Windows) before writing. This protects against external tools editing files concurrently and provides a safety net beyond the single-writer pattern. |

-----

## E.3 Directory Structure

```
~/.omnipus/
├── config.json                        # Main configuration
├── credentials.json                   # Credentials (AES-256-GCM encrypted, Argon2id KDF)
│
├── agents/
│   ├── general-assistant/             # Core agent workspace
│   │   ├── sessions/
│   │   │   ├── session_abc123/        # One directory per session
│   │   │   │   ├── meta.json          # Session metadata (id, title, status, stats)
│   │   │   │   ├── 2026-03-28.jsonl   # Transcript partition (one per day)
│   │   │   │   └── 2026-03-29.jsonl   # Next day's partition
│   │   │   └── session_def456/
│   │   │       ├── meta.json
│   │   │       └── 2026-03-27.jsonl
│   │   ├── memory/
│   │   │   ├── memory.jsonl           # Long-term memory
│   │   │   └── daily/
│   │   │       ├── 2026-03-28.md
│   │   │       └── 2026-03-27.md
│   │   ├── skills/                    # Agent-specific skills
│   │   ├── HEARTBEAT.md
│   │   └── USER.md
│   │
│   ├── researcher/                    # Core agent workspace
│   │   └── (same structure)
│   │
│   ├── content-creator/               # Core agent workspace
│   │   └── (same structure)
│   │
│   └── my-custom-agent/               # Custom agent workspace
│       ├── SOUL.md                    # Only custom agents have these
│       ├── AGENTS.md
│       └── (same structure)
│
├── projects/
│   ├── infrastructure-q2/
│   │   ├── project.json               # Project metadata
│   │   ├── memory/
│   │   │   └── memory.jsonl           # Project-scoped memory
│   │   └── files/                     # Project files
│   │
│   └── product-launch/
│       └── (same structure)
│
├── tasks/
│   ├── task_abc123.json               # One file per task (per-entity for concurrency)
│   ├── task_def456.json
│   └── ...
│
├── pins/
│   ├── pin_abc123.json                # One file per pin (per-entity for concurrency)
│   ├── pin_def456.json
│   └── ...
│
├── channels/
│   └── whatsapp/
│       └── session.db                 # WhatsApp session (SQLite, managed by whatsmeow)
│
├── skills/
│   └── (globally installed skills)
│
├── backups/
│   └── (backup archives)
│
└── system/
    ├── omnipus-session.json            # Omnipus system agent session
    ├── devices.json                   # Paired devices
    ├── audit.jsonl                    # Audit log
    ├── channels.json                  # Installed add-on channel metadata
    └── state.json                     # Gateway state, onboarding status
```

-----

## E.4 Entity: Agent

```json
{
  "id": "general-assistant",
  "type": "core",
  "name": "General Assistant",
  "description": "Versatile helper for everyday tasks, research, writing, and analysis",
  "icon": "robot",
  "color": "green",
  "status": "active",
  "created_at": "2026-03-28T10:00:00Z",

  "model": {
    "provider": "anthropic",
    "model": "claude-opus",
    "fallback_1": {
      "provider": "openai",
      "model": "gpt-5.4"
    },
    "fallback_2": null,
    "parameters": {
      "temperature": 0.7,
      "max_tokens": 8192,
      "top_p": 1.0
    }
  },

  "agent_loop": {
    "max_tool_iterations": 20,
    "tool_timeout_seconds": 30,
    "bootstrap_max_chars": 20000,
    "compaction": {
      "enabled": true,
      "reserve_tokens": 20000,
      "preserve_recent": 10,
      "memory_flush": true
    }
  },

  "tools": {
    "allow": ["web_search", "web_fetch", "file.read", "file.write", "file.list", "spawn", "cron", "memory", "message", "image.analyze"],
    "deny": [],
    "overrides": {
      "exec": {
        "enabled": true,
        "approval": "ask",
        "timeout": 60
      }
    }
  },

  "heartbeat": {
    "enabled": true,
    "interval": "30m",
    "active_hours": {
      "start": "08:00",
      "end": "22:00"
    }
  },

  "skills": ["aws-cost-analyzer", "git-workflow"],

  "workspace": "~/.omnipus/agents/general-assistant/"
}
```

**Storage:** Agent definitions stored in `config.json` under `agents.list[]`. Workspace directory auto-created on activation.

| Field | Type | Required | Notes |
|---|---|---|---|
| id | string | Yes | URL-safe slug. Hardcoded for core/system, auto-generated for custom. |
| type | enum | Yes | `"system"`, `"core"`, `"custom"` |
| name | string | Yes | Display name. Editable on core and custom. |
| description | string | No | One-line description. |
| icon | string | No | Phosphor icon name (e.g., `"robot"`, `"magnifying-glass"`, `"pencil-line"`) or uploaded image reference. Never emoji — emoji are only used in LLM chat output and translated at render time. See Appendix C §C.3.2. |
| color | string | No | Named color: "green", "purple", "yellow", "red", "blue", "orange", "gray". |
| status | enum | Yes | `"active"`, `"inactive"`. System agent is always active. |
| created_at | ISO date | Auto | |
| model | object | Yes | Provider, model name, fallbacks, parameters. |
| model.parameters | object | No | Temperature, max_tokens, top_p, top_k. Provider-specific. Inherits global defaults if not set. |
| agent_loop | object | No | Inherits global defaults if not set. |
| tools | object | No | Allow/deny lists + per-tool overrides. Inherits global defaults if not set. |
| heartbeat | object | No | Disabled by default if not specified. |
| skills | string[] | No | Installed skill names for this agent. |
| workspace | string | Auto | Path to agent workspace directory. |

-----

## E.5 Entity: Session

Sessions are stored as a directory per session, with a metadata file and day-partitioned transcript files.

### E.5.1 Session Metadata (`meta.json`)

```json
{
  "id": "session_abc123",
  "agent_id": "general-assistant",
  "title": "AWS Pricing Research",
  "status": "active",
  "created_at": "2026-03-28T10:38:00Z",
  "updated_at": "2026-03-29T14:22:00Z",
  "model": "claude-opus",
  "provider": "anthropic",

  "stats": {
    "tokens_in": 4210,
    "tokens_out": 3891,
    "tokens_total": 8101,
    "cost": 1.24,
    "tool_calls": 14,
    "message_count": 12
  },

  "project_id": "infrastructure-q2",
  "task_id": "task_xyz789",
  "channel": "webchat",

  "partitions": ["2026-03-28.jsonl", "2026-03-29.jsonl"],
  "last_compaction_summary": "User researched AWS m5 pricing, compared regions, decided on us-east-1. Agent produced a cost comparison markdown file."
}
```

**Storage:** `~/.omnipus/agents/<agent-id>/sessions/<session-id>/meta.json`

| Field | Type | Required | Notes |
|---|---|---|---|
| id | string | Yes | Auto-generated. |
| agent_id | string | Yes | Owning agent. |
| title | string | No | Auto-generated from first message, or user-defined. |
| status | enum | Yes | `"active"`, `"archived"`. |
| created_at / updated_at | ISO date | Auto | |
| model / provider | string | Yes | Snapshot of model used (may differ from agent's current). |
| stats | object | Auto | Aggregated across all partitions. |
| project_id | string | No | Link to project context. |
| task_id | string | No | Link to task. |
| channel | string | Yes | Origin: "webchat", "telegram", "discord", etc. |
| partitions | string[] | Auto | Ordered list of transcript partition filenames. |
| last_compaction_summary | string | Auto | Most recent context compaction summary. Preserved even when old partitions are purged by retention policy. Allows the agent to retain high-level context from deleted history. |

### E.5.2 Session Transcript Partitions

Transcript entries are stored as JSONL (one JSON object per line), partitioned by day. A new partition file is created at midnight (UTC) or when the session is first used on a new day. Sessions that span multiple days produce multiple partition files.

**Storage:** `~/.omnipus/agents/<agent-id>/sessions/<session-id>/<YYYY-MM-DD>.jsonl`

```jsonl
{"id":"msg_001","role":"user","content":"Check AWS pricing for m5 instances","timestamp":"2026-03-28T10:38:00Z","attachments":[]}
{"id":"msg_002","role":"assistant","content":"I'll search for that and check the latest pricing.","timestamp":"2026-03-28T10:38:05Z","tokens":24,"cost":0.003,"tool_calls":[{"id":"tc_001","tool":"web_search","status":"success","duration_ms":340,"parameters":{"query":"AWS m5 instance pricing 2026"},"result":{"results_count":5,"results":["..."]}}]}
{"id":"compact_001","type":"compaction","summary":"User asked about AWS m5 pricing. Agent searched and found current rates...","timestamp":"2026-03-28T11:45:00Z","messages_compacted":18}
```

Transcript entries can be one of three types:

| Type | Description |
|---|---|
| **message** | A user or assistant message with optional tool calls, attachments, and cost data. Standard conversation entry. |
| **compaction** | A context compression summary entry. Created when the context window approaches the model's limit. Replaces older messages in the context window but does not delete them from the transcript. See §E.5.3. |
| **system** | System events: session resumed, context window warning, heartbeat result. |

### E.5.3 Context Window Compression

Omnipus manages the LLM context window using a two-layer approach inherited from and improving on Omnipus's dynamic context compression:

**Layer 1 — Tool result pruning (in-memory only):**
When the context window is filling up, older tool call results are truncated to summaries in the prompt sent to the LLM. The full results remain in the transcript on disk. This is transparent and does not modify stored data.

**Layer 2 — Conversation compaction (persistent):**
When pruning is insufficient and `contextTokens > contextWindow - reserveTokens`, Omnipus triggers compaction:

1. **Memory flush:** Before compacting, the agent writes important facts from the conversation to the agent's `memory.jsonl`. This preserves critical information that would otherwise be lost in summarization.
2. **Summarization:** The LLM summarizes the older portion of the conversation into a compact summary.
3. **Compaction entry:** The summary is appended to the transcript as a `compaction` entry. The original messages remain in the transcript file but are no longer sent to the LLM.
4. **Context rebuild:** Subsequent LLM calls use the compaction summary + recent messages as context.
5. **Metadata update:** `last_compaction_summary` in `meta.json` is updated so the summary survives partition retention.

**Configuration** (in `config.json` under `agent_loop`):

| Setting | Default | Description |
|---|---|---|
| `compaction.enabled` | `true` | Enable auto-compaction. |
| `compaction.reserve_tokens` | `20000` | Token headroom to maintain. Compaction triggers when free tokens drop below this. |
| `compaction.preserve_recent` | `10` | Number of recent messages always kept intact (never compacted). |
| `compaction.memory_flush` | `true` | Flush important facts to memory before compacting. |

**Error handling:** If the model returns a context length error, Omnipus retries with aggressive pruning first, then compaction. Context errors are distinguished from network errors by checking the error response structure (not substring matching — avoiding Omnipus bug #683).

### E.5.4 Session Retention

Configurable in `config.json` under `storage.retention`:

| Setting | Default | Description |
|---|---|---|
| `storage.retention.session_days` | `90` | Transcript partitions older than this are deleted. Set to `0` for no retention limit. |
| `storage.retention.archive_before_delete` | `true` | Compress old partitions to `.jsonl.gz` before deleting. |
| `storage.retention.keep_compaction_summary` | `true` | Preserve `last_compaction_summary` in `meta.json` even when all partitions are purged. Allows the agent to retain high-level context from deleted sessions. |

Retention runs on gateway startup and once daily. `omnipus doctor` warns when session storage exceeds a configurable threshold (default 500MB).
| messages[].role | enum | Yes | `"user"`, `"assistant"`, `"system"`. |
| messages[].content | string | Yes | Markdown text. |
| messages[].attachments | array | No | `[{ type, path, size, mime_type }]`. |
| messages[].tool_calls | array | No | Tool invocations within this message. |
| messages[].tool_calls[].status | enum | Yes | `"success"`, `"error"`, `"pending"`, `"denied"`. |
| messages[].tokens | int | No | Token count. |
| messages[].cost | float | No | Cost in USD. |

-----

## E.6 Entity: Project

```json
{
  "id": "infrastructure-q2",
  "name": "Infrastructure Q2",
  "description": "Migrate AWS infrastructure from EC2 to ECS by April 30",
  "color": "orange",
  "agent_ids": ["general-assistant", "researcher"],
  "created_at": "2026-03-20T09:00:00Z",
  "updated_at": "2026-03-28T10:42:00Z",

  "stats": {
    "total_cost": 14.82,
    "total_tokens": 42300,
    "tasks_total": 9,
    "tasks_done": 5
  }
}
```

**Storage:** `~/.omnipus/projects/<project-slug>/project.json`

Project directory also contains: `memory/memory.jsonl` (project-scoped), `files/` (produced files).

| Field | Type | Required | Notes |
|---|---|---|---|
| id | string | Yes | URL-safe slug from name. |
| name | string | Yes | Display name. |
| description | string | No | |
| color | string | No | Visual identifier on board and tabs. |
| agent_ids | string[] | No | Assigned agents. Empty = any agent. |
| created_at / updated_at | ISO date | Auto | |
| stats | object | Auto | Aggregated from linked tasks and sessions. |

-----

## E.7 Entity: Task

```json
{
  "id": "task_xyz789",
  "name": "Create migration plan",
  "description": "Research ECS vs EKS, compare costs, estimate timeline",
  "status": "active",
  "project_id": "infrastructure-q2",
  "agent_id": "general-assistant",
  "created_at": "2026-03-26T10:15:00Z",
  "updated_at": "2026-03-28T10:42:00Z",
  "created_by": "user",
  "sort_order": 0,

  "waiting": {
    "reason": null,
    "followup_date": null
  },

  "stats": {
    "cost": 1.24,
    "tokens": 4210,
    "tool_calls": 14,
    "sessions": ["session_abc123"]
  },

  "files_produced": [
    {
      "path": "projects/infrastructure-q2/files/migration_plan.md",
      "name": "migration_plan.md",
      "size": 4200,
      "created_at": "2026-03-28T10:40:00Z"
    }
  ]
}
```

**Storage:** `~/.omnipus/tasks/<task_id>.json` — one file per task. Per-entity files eliminate write contention when multiple agents update different tasks concurrently. To load the task board, the gateway reads all files in the `tasks/` directory. File listing is cached in memory and invalidated on filesystem change (via `fsnotify` or polling on platforms without inotify). This is consistent with how sessions already use one file per session.

| Field | Type | Required | Notes |
|---|---|---|---|
| id | string | Yes | Auto-generated. |
| name | string | Yes | Card title. |
| description | string | No | Card body. |
| status | enum | Yes | `"inbox"`, `"next"`, `"active"`, `"waiting"`, `"done"`. GTD columns. |
| project_id | string | No | Link to project. Null = no project. |
| agent_id | string | No | Assigned agent. Null = unassigned. |
| created_at / updated_at | ISO date | Auto | |
| created_by | enum | Yes | `"user"`, `"agent"`, `"system"`. |
| sort_order | int | Auto | Position within column for drag-drop. |
| waiting.reason | string | No | Why blocked (status = "waiting"). |
| waiting.followup_date | ISO date | No | When to follow up. |
| stats | object | Auto | Aggregated from linked sessions. |
| stats.sessions | string[] | Auto | Session IDs for work on this task. |
| files_produced | array | Auto | Files produced during task work. |

-----

## E.8 Entity: Pinned Artifact

```json
{
  "id": "pin_abc123",
  "title": "AWS m5 Pricing Comparison",
  "created_at": "2026-03-28T10:43:00Z",
  "updated_at": "2026-03-28T11:00:00Z",

  "source": {
    "session_id": "session_abc123",
    "message_id": "msg_003",
    "agent_id": "general-assistant",
    "agent_name": "General Assistant",
    "timestamp": "2026-03-28T10:42:00Z"
  },

  "project_id": "infrastructure-q2",
  "tags": ["aws", "pricing", "infrastructure"],

  "content": {
    "type": "assistant_message",
    "markdown": "Based on current pricing, the AWS m5.xlarge...",
    "tool_calls": [
      {
        "tool": "web_search",
        "parameters": { "query": "AWS m5 pricing" },
        "result_summary": "5 results found"
      }
    ],
    "files": [
      {
        "name": "aws_pricing_comparison.md",
        "path": "projects/infrastructure-q2/files/aws_pricing_comparison.md",
        "size": 4200,
        "mime_type": "text/markdown"
      }
    ],
    "code_blocks": [
      {
        "language": "python",
        "code": "import boto3\nclient = boto3.client('pricing')..."
      }
    ]
  }
}
```

**Storage:** `~/.omnipus/pins/<pin_id>.json` — one file per pin. Per-entity files eliminate write contention. To list all pins, the gateway reads all files in the `pins/` directory (cached, same mechanism as tasks).

Content is a **snapshot** — the original session may be archived/deleted, pins persist independently. Files referenced in pins persist in their original workspace location.

| Field | Type | Required | Notes |
|---|---|---|---|
| id | string | Yes | Auto-generated. |
| title | string | Yes | Auto-generated from first line of content. User can rename. |
| created_at | ISO date | Auto | When pinned. |
| updated_at | ISO date | Auto | When title/tags last changed. |
| source.session_id | string | Yes | Original session (may no longer exist). |
| source.message_id | string | Yes | Original message within session. |
| source.agent_id | string | Yes | Which agent produced it. |
| source.agent_name | string | Yes | Snapshot of agent name (agent may be renamed/deleted later). |
| source.timestamp | ISO date | Yes | When original message was created. |
| project_id | string | No | Optional project association for organization. |
| tags | string[] | No | User-defined tags. |
| content.type | enum | Yes | `"assistant_message"`, `"code_block"`, `"file_output"`, `"tool_result"`, `"rich_component"`. |
| content.markdown | string | No | Text content in markdown. |
| content.tool_calls | array | No | Tool calls within the pinned message. |
| content.files | array | No | File references (files persist in workspace). |
| content.code_blocks | array | No | Extracted code blocks with language. |
| content.chart_data | object | No | Chart configuration for rich components. |
| content.mermaid_source | string | No | Mermaid diagram source. |

### E.8.1 What Can Be Pinned

| Content type | What's captured in snapshot |
|---|---|
| Text response | Full markdown text |
| Code block | Language, code content, filename if any |
| File output | File metadata + path reference (file persists in workspace) |
| Rich component (chart, diagram) | Rendering data (chart config, mermaid source) |
| Tool call result | Tool name, parameters, result summary |
| Image | Image path reference |
| Entire message | All components, tool calls, text within that message |

### E.8.2 How Pins Differ from Other Persistence

| Mechanism | What it stores | Lifetime | Scope |
|---|---|---|---|
| **Session** | Metadata + day-partitioned JSONL transcript | Configurable retention (default 90 days). Last compaction summary preserved. | Per agent |
| **Memory** | Key facts and preferences | Indefinite (JSONL) | Per agent or per project |
| **Pin** | Snapshot of a specific response | Until user deletes | Global (accessible anywhere) |
| **File** | Agent-produced file | Until deleted | Per agent workspace or per project |

Pins are the cross-cutting bookmark system. Memory is what the agent remembers. Sessions are full conversation logs. Files are deliverables. Pins are "I want to find this specific response again quickly."

### E.8.3 Access Points

| Access point | Method |
|---|---|
| Chat slash command | `/pins` → inline pin list card |
| Chat action button | `[Pin]` on every assistant message |
| Fullscreen overlay | Pins browser with search, filters, previews |
| Agent profile | Pins filtered to that agent's outputs |
| Project context | Pins filtered by project_id |
| Omnipus agent | "Show me my pins tagged aws" → conversational access |

-----

## E.9 Entity: Memory Entry

```json
{
  "timestamp": "2026-03-28T10:42:00Z",
  "content": "User prefers tables over charts for reports",
  "source": "conversation",
  "session_id": "session_abc123",
  "importance": "normal"
}
```

**Storage:** JSONL (one JSON object per line, append-only).
- Agent memory: `~/.omnipus/agents/<agent-id>/memory/memory.jsonl`
- Project memory: `~/.omnipus/projects/<project-id>/memory/memory.jsonl`

| Field | Type | Required | Notes |
|---|---|---|---|
| timestamp | ISO date | Yes | |
| content | string | Yes | The memory text. |
| source | enum | Yes | `"conversation"`, `"heartbeat"`, `"manual"`, `"system"`. |
| session_id | string | No | Source session. |
| importance | enum | No | `"normal"`, `"important"`. |

-----

## E.10 Entity: Channel

### E.10.1 Channel Architecture

Omnipus uses a **hybrid in-process/bridge channel architecture** inheriting Omnipus's design. Go channels are compiled into the binary for maximum efficiency and minimal deployment complexity. Non-Go channels and community extensions use an external bridge protocol.

**Design rationale:** Omnipus and OpenClaw both keep channels in-process, validating this approach at scale (250K+ stars for OpenClaw, 25K+ for Omnipus). Compiling Go channels into the binary preserves the single-binary deployment, keeps RAM overhead minimal (critical for $10 hardware), and inherits Omnipus's working channel code. The bridge protocol exists for channels that genuinely cannot be Go (Java, Node.js runtimes) and for community extensions in any language. WhatsApp's SQLite dependency is handled via `modernc.org/sqlite` (pure Go), avoiding CGo without needing process isolation.

| Tier | Channels | Process model |
|---|---|---|
| **Built-in** | Web UI (WebChat) | Embedded in gateway process via `go:embed`. |
| **Compiled-in (Go)** | Telegram, Discord, Slack, WhatsApp, Google Chat, Matrix, IRC, Mattermost, Nostr, Twitch, LINE, WeCom, DingTalk, Microsoft Teams | Compiled into the binary. Communicates via internal `MessageBus`. Zero IPC overhead. Single process. |
| **External (non-Go)** | Signal, QQ | Managed child process. Bridge adapter in binary manages the external runtime. Uses bridge protocol (JSON over stdin/stdout). |
| **Community** | Any custom channel | Locally installed by user via `omnipus channel install`. Managed child process. Uses bridge protocol + Omnipus Channel SDK. Any language. **Installed at user's own risk.** |

### E.10.2 Bridge Protocol

Non-Go channels and community channels use the bridge protocol — JSON over stdin/stdout. This is the integration path for external channel processes. Compiled-in Go channels do NOT use the bridge protocol — they communicate via the internal `MessageBus` directly.

**Gateway → Bridge (commands):**
```json
{"type": "connect", "config": {"token": "..."}}
{"type": "send", "channel_id": "123", "message": {"text": "Hello", "attachments": []}}
{"type": "disconnect"}
{"type": "status"}
```

**Bridge → Gateway (events):**
```json
{"type": "connected", "info": {"bot_id": "...", "bot_name": "..."}}
{"type": "message", "channel_id": "123", "user_id": "456", "message": {"text": "Hi", "attachments": []}}
{"type": "error", "message": "Connection lost"}
{"type": "status", "connected": true, "uptime": 3600}
```

This enables wrapping existing non-Go implementations (e.g., OpenClaw's Node.js channel adapters, signal-cli in Java, mautrix bridges in Python) and community extensions in any language. Go channels do not use this protocol — they are compiled in and use the internal `MessageBus`.

### E.10.3 Channel Go Interface

All channels implement the same Go interface within the gateway:

```go
type ChannelProvider interface {
    ID() string
    Name() string
    Connect(config ChannelConfig) error
    Disconnect() error
    Send(message OutboundMessage) error
    OnMessage(handler func(InboundMessage))
    Status() ChannelStatus
    Capabilities() ChannelCapabilities
}
```

Compiled-in Go channels implement this interface directly within the gateway process (same as Omnipus's architecture). External channels (non-Go and community) are represented by a `BridgeAdapter` that implements `ChannelProvider` by translating to/from the stdin/stdout JSON bridge protocol. From the gateway's perspective, all channels look the same regardless of whether they are compiled-in or external.

### E.10.4 Channel Implementation Matrix

Go channels are compiled into the binary (in-process, internal MessageBus). Non-Go channels run as external processes using the bridge protocol.

| Channel | Integration | Language | Library / Runtime | Tier |
|---|---|---|---|---|
| WebChat | Built-in | Go | Gateway HTTP/WebSocket | Built-in |
| Telegram | Compiled-in | Go | `telebot` | Bundled |
| Discord | Compiled-in | Go | `discordgo` | Bundled |
| Slack | Compiled-in | Go | `slack-go` | Bundled |
| WhatsApp | Compiled-in | Go | `whatsmeow` + `modernc.org/sqlite` | Bundled |
| Google Chat | Compiled-in | Go | REST API (net/http) | Bundled |
| Matrix | Compiled-in | Go | `mautrix-go` | Bundled |
| IRC | Compiled-in | Go | Go `irc` library | Bundled |
| Mattermost | Compiled-in | Go | REST API (net/http) | Bundled |
| Nostr | Compiled-in | Go | `go-nostr` | Bundled |
| Twitch | Compiled-in | Go | IRC-based (reuses IRC) | Bundled |
| LINE | Compiled-in | Go | REST API | Bundled |
| WeCom | Compiled-in | Go | REST API | Bundled |
| DingTalk | Compiled-in | Go | REST API | Bundled |
| Signal | External (bridge) | Java | `signal-cli` | Bundled bridge adapter |
| Microsoft Teams | Compiled-in | Go | `msbotbuilder-go` (Bot Framework REST API) | Bundled |
| QQ | External (bridge) | Various | Various | Bundled bridge adapter |

### E.10.5 Channel Data Model

```json
{
  "id": "telegram",
  "name": "Telegram",
  "icon": "paper-plane-tilt",
  "tier": "bundled",
  "implementation": "compiled",
  "status": "connected",
  "enabled": true,
  "connected_since": "2026-03-28T08:00:00Z",
  "error": null,

  "config": {
    "token_ref": "TELEGRAM_BOT_TOKEN",
    "bot_username": "@omnipus_bot"
  },

  "bridge": null,

  "policies": {
    "allow_from": ["123456789", "987654321"],
    "default_agent_id": "general-assistant",
    "routing_rules": [
      {
        "match": { "user": "123456789" },
        "agent_id": "general-assistant"
      },
      {
        "match": { "user": "987654321" },
        "agent_id": "researcher"
      }
    ]
  },

  "capabilities": {
    "text": true,
    "images": true,
    "files": true,
    "voice": true,
    "groups": true,
    "reactions": false,
    "threads": false
  },

  "stats": {
    "messages_today": 24,
    "messages_week": 142,
    "last_message_at": "2026-03-28T10:38:00Z"
  }
}
```

**Bridged channel example (Signal):**

```json
{
  "id": "signal",
  "name": "Signal",
  "tier": "bundled",
  "implementation": "bridge",
  "enabled": true,
  "status": "connected",

  "config": {
    "phone_number": "+15551234567"
  },

  "bridge": {
    "command": "signal-cli",
    "args": ["--config", "~/.omnipus/channels/signal/", "daemon", "--json"],
    "health_check_interval": "30s",
    "restart_on_crash": true,
    "required_runtime": "java"
  }
}
```

**WhatsApp channel provider (dual-mode):**

```json
{
  "id": "whatsapp",
  "name": "WhatsApp",
  "tier": "bundled",
  "implementation": "compiled",
  "enabled": true,
  "status": "connected",

  "config": {
    "mode": "personal",
    "session_store": "~/.omnipus/channels/whatsapp/session.db"
  },

  "bridge": null
}
```

Or Business API mode:

```json
{
  "config": {
    "mode": "business",
    "phone_number_id": "123456",
    "business_account_id": "789",
    "access_token_ref": "WHATSAPP_BUSINESS_TOKEN"
  }
}
```

**Storage:** Channel definitions stored in `config.json` under `channels`.

| Field | Type | Required | Notes |
|---|---|---|---|
| id | string | Yes | Unique: "telegram", "discord", etc. |
| name | string | Yes | Display name. |
| icon | string | Yes | Phosphor icon name (e.g., `"paper-plane-tilt"` for Telegram, `"whatsapp-logo"` for WhatsApp). |
| tier | enum | Yes | `"builtin"`, `"bundled"`, `"addon"`. |
| implementation | enum | Yes | `"builtin"` (Web UI), `"compiled"` (Go channels in binary), `"bridge"` (external non-Go/community channels). |
| status | enum | Auto | `"connected"`, `"disconnected"`, `"error"`, `"disabled"`. |
| enabled | bool | Yes | User toggle. |
| connected_since | ISO date | Auto | |
| error | string | Auto | Error message if status is "error". |
| config | object | Yes | Channel-specific. Credentials use `_ref` to credentials.json. |
| bridge | object | No | Only for `"bridge"` implementation channels. Command, args, health check, restart policy. Null for compiled-in and built-in channels. |
| bridge.required_runtime | string | No | `"java"`, `"nodejs"`, etc. Shown in UI as "Requires: Java installed". Only for bridge channels. |
| policies.allow_from | string[] | No | Allowed user IDs. Empty = anyone (triggers security warning). |
| policies.default_agent_id | string | No | Default agent for this channel. |
| policies.routing_rules | array | No | Per-user/group routing. |
| capabilities | object | Auto | Set by channel adapter, not user. |
| stats | object | Auto | Usage tracking. |

### E.10.6 Routing Rules

```json
{
  "match": {
    "user": "123456789",
    "group": null,
    "pattern": null
  },
  "agent_id": "researcher"
}
```

Evaluated top-to-bottom. First match wins. No match → `default_agent_id` → system default agent.

### E.10.7 Community Channel Providers

Community channel providers are locally installed add-ons built with the Omnipus Channel SDK. They follow the same model as bundled providers — local child processes communicating via stdin/stdout bridge protocol. There is no remote WebSocket registration; all providers must be installed on the same machine as the gateway.

**Trust model:** Community providers are installed at the user's own risk, similar to browser extensions or desktop app plugins. Omnipus provides the SDK and bridge protocol specification; the community builds and distributes providers independently. Omnipus does not verify or sign community providers (bundled providers are signed via SEC-09).

**Installation:** Community providers are installed via `omnipus channel install <path-or-url>` or via the system agent (`system.channel.enable`). Installation copies the provider binary/script to `~/.omnipus/channels/<channel-id>/` and registers it in `config.json`. The provider must include a manifest file:

```json
{
  "protocol_version": 1,
  "channel_id": "mastodon",
  "name": "Mastodon",
  "version": "1.0.0",
  "command": "omnipus-channel-mastodon",
  "capabilities": {
    "text": true,
    "images": true,
    "files": false
  },
  "required_config": [
    { "key": "instance_url", "type": "string", "label": "Instance URL" },
    { "key": "access_token", "type": "credential", "label": "Access Token" }
  ]
}
```

**SaaS deployment:** In a future hosted/cloud version of Omnipus, all channel providers will be managed and authenticated by the platform. The SaaS trust model and authentication requirements will be specified separately when that deployment mode is designed.

-----

## E.11 Entity: Provider

```json
{
  "id": "anthropic",
  "name": "Anthropic",
  "status": "connected",
  "api_key_ref": "ANTHROPIC_API_KEY",
  "api_base": null,
  "models_available": ["claude-opus", "claude-sonnet"],
  "connected_since": "2026-03-28T08:00:00Z"
}
```

**Storage:** `config.json` under `providers`.

| Field | Type | Required | Notes |
|---|---|---|---|
| id | string | Yes | "anthropic", "openai", "deepseek", "groq", "openrouter", "ollama". |
| name | string | Yes | Display name. |
| status | enum | Auto | `"connected"`, `"error"`, `"unconfigured"`. |
| api_key_ref | string | Yes (cloud) | Reference to credential in credentials.json. Not for Ollama. |
| api_base | string | No | Custom endpoint URL. Default per provider. |
| models_available | string[] | Auto | Discovered on connection test. |
| connected_since | ISO date | Auto | |

-----

## E.12 Entity: Installed Skill

```json
{
  "id": "aws-cost-analyzer",
  "name": "AWS Cost Analyzer",
  "version": "1.2.0",
  "author": "@cloudtools",
  "description": "Analyze AWS billing data and generate cost reports",
  "verified": true,
  "hash": "sha256:abc123...",
  "source": "clawhub",
  "installed_at": "2026-03-20T14:00:00Z",
  "tools_provided": ["aws.cost_summary", "aws.cost_by_service", "aws.cost_forecast"],
  "agent_ids": ["general-assistant"],
  "required_credentials": {
    "AWS_ACCESS_KEY_ID": { "configured": true },
    "AWS_SECRET_ACCESS_KEY": { "configured": true }
  }
}
```

**Storage:** Skill metadata in `config.json` under `skills`. Skill files in `~/.omnipus/skills/<skill-name>/SKILL.md` (and supporting files).

| Field | Type | Required | Notes |
|---|---|---|---|
| id | string | Yes | Skill identifier. |
| name | string | Yes | Display name. |
| version | string | Yes | Semantic version. |
| author | string | No | ClawHub author. |
| description | string | No | |
| verified | bool | Auto | SHA-256 hash match against registry. |
| hash | string | Yes | SHA-256 hash for verification. |
| source | enum | Yes | `"clawhub"`, `"local"`, `"url"`. |
| installed_at | ISO date | Auto | |
| tools_provided | string[] | Auto | Discovered from SKILL.md. |
| agent_ids | string[] | No | Which agents have this skill active. |
| required_credentials | object | No | Credential requirements with configuration status. |

-----

## E.13 Entity: MCP Server

```json
{
  "id": "github",
  "name": "GitHub",
  "transport": "stdio",
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-github"],
  "url": null,
  "env": {
    "GITHUB_TOKEN_ref": "GITHUB_TOKEN"
  },
  "status": "connected",
  "tools_discovered": ["github.repos.list", "github.issues.create", "..."],
  "agent_ids": ["general-assistant", "researcher"],
  "connected_since": "2026-03-28T08:00:00Z"
}
```

**Storage:** `config.json` under `tools.mcp.servers`.

| Field | Type | Required | Notes |
|---|---|---|---|
| id | string | Yes | Server identifier. |
| name | string | Yes | Display name. |
| transport | enum | Yes | `"stdio"`, `"sse"`, `"http"`. |
| command | string | For stdio | Process command. |
| args | string[] | For stdio | Process arguments. |
| url | string | For sse/http | Server URL. |
| env | object | No | Environment variables. Credentials use `_ref` suffix. |
| status | enum | Auto | `"connected"`, `"disconnected"`, `"error"`. |
| tools_discovered | string[] | Auto | Tool names discovered on connection. |
| agent_ids | string[] | No | Which agents can use these tools. |
| connected_since | ISO date | Auto | |

-----

## E.14 Entity: Paired Device

```json
{
  "id": "device_abc123",
  "name": "MacBook Pro",
  "platform": "macos",
  "role": "admin",
  "paired_at": "2026-03-20T10:00:00Z",
  "last_seen": "2026-03-28T10:42:00Z",
  "token_hash": "sha256:..."
}
```

**Storage:** `~/.omnipus/system/devices.json`

| Field | Type | Required | Notes |
|---|---|---|---|
| id | string | Yes | Auto-generated. |
| name | string | Yes | User-assigned device name. |
| platform | string | No | "macos", "windows", "linux", "ios", "android". |
| role | enum | Yes | `"admin"`, `"operator"`, `"viewer"`. |
| paired_at | ISO date | Auto | |
| last_seen | ISO date | Auto | |
| token_hash | string | Yes | Hashed device token. Never stored in plaintext. |

-----

## E.15 Entity: Audit Log Entry

```json
{
  "timestamp": "2026-03-28T10:42:31Z",
  "event": "tool_call",
  "decision": "allow",
  "agent_id": "general-assistant",
  "session_id": "session_abc123",
  "tool": "web_search",
  "parameters": { "query": "AWS m5 pricing" },
  "policy_rule": "tools.allow matched 'web_search'"
}
```

**Storage:** `~/.omnipus/system/audit.jsonl` — append-only JSONL.

**Rotation:** Audit log files are rotated daily or when file size exceeds 50MB (whichever comes first). Rotated files are named `audit-YYYY-MM-DD.jsonl` and compressed to `.jsonl.gz`. Retention configurable via `storage.retention.audit_days` (default 90 days). `omnipus doctor` warns when total audit log storage exceeds 500MB.

| Field | Type | Required | Notes |
|---|---|---|---|
| timestamp | ISO date | Yes | |
| event | enum | Yes | `"tool_call"`, `"exec"`, `"file_op"`, `"auth"`, `"policy_change"`, `"channel_event"`, `"system_tool"`. |
| decision | enum | Yes | `"allow"`, `"deny"`, `"pending"`. |
| agent_id | string | No | |
| session_id | string | No | |
| tool | string | No | Tool name for tool_call events. |
| parameters | object | No | Redacted tool parameters. |
| policy_rule | string | No | Which policy matched (SEC-17 explainable decisions). |
| ~~hmac~~ | ~~string~~ | ~~No~~ | ~~HMAC-SHA256 chain hash (when tamper-evident logging enabled, SEC-18).~~ **Descoped v1.0.** |

-----

## E.16 Entity: Cron Job

```json
{
  "id": "cron_morning_briefing",
  "name": "Morning Briefing",
  "agent_id": "general-assistant",
  "schedule": "0 9 * * *",
  "timezone": "Europe/Berlin",
  "message": "Generate today's briefing: weather, calendar, top emails",
  "enabled": true,
  "channel": "telegram",
  "announce": true,
  "last_run": "2026-03-28T09:00:00Z",
  "last_status": "success",
  "next_run": "2026-03-29T09:00:00Z",
  "created_at": "2026-03-20T10:00:00Z"
}
```

**Storage:** `~/.omnipus/agents/<agent-id>/cron/jobs.json` — per-agent.

| Field | Type | Required | Notes |
|---|---|---|---|
| id | string | Yes | Auto-generated. |
| name | string | Yes | Display name. |
| agent_id | string | Yes | Which agent executes. |
| schedule | string | Yes | Cron expression. |
| timezone | string | No | IANA timezone. Defaults to user's timezone. |
| message | string | Yes | Prompt sent to agent on trigger. |
| enabled | bool | Yes | Toggle. |
| channel | string | No | Where to announce results. Null = no announcement. |
| announce | bool | No | Whether to send results to channel. |
| last_run | ISO date | Auto | |
| last_status | enum | Auto | `"success"`, `"error"`, `"skipped"`. |
| next_run | ISO date | Auto | Calculated from schedule. |
| created_at | ISO date | Auto | |

-----

## E.17 Entity: User Profile

```json
{
  "name": "Alex Chen",
  "timezone": "Europe/Berlin",
  "language": "en",
  "preferences": {
    "theme": "system",
    "font_size": "medium"
  },
  "onboarding_complete": true
}
```

**Storage:** `config.json` under `user`.

-----

## E.18 Entity: System State

```json
{
  "gateway_started_at": "2026-03-28T08:00:00Z",
  "onboarding_complete": true,
  "last_doctor_run": "2026-03-28T08:00:00Z",
  "last_doctor_score": 23,
  "version": "0.3.0"
}
```

**Storage:** `~/.omnipus/system/state.json`

-----

## E.19 Entity Relationships

```
Agent
 ├── Sessions[]
 ├── Memory[] (agent-scoped)
 ├── Skills[]
 ├── Cron Jobs[]
 └── HEARTBEAT.md

Project
 ├── Tasks[]
 ├── Memory[] (project-scoped)
 ├── Files[]
 └── Agents[] (M:N)

Task
 ├── → Agent (assigned)
 ├── → Project (belongs to)
 ├── → Sessions[] (work done)
 └── Files produced[]

Session
 ├── → Agent (owner)
 ├── → Project (optional)
 ├── → Task (optional)
 └── Messages[]
      └── ToolCalls[]

Pin
 └── → Message (snapshot copy)

Channel
 ├── → Routing rules
 └── → Default agent

Provider
 └── → Models available[]

Skill
 └── → Agents[] (assigned to)

MCP Server
 ├── → Tools discovered[]
 └── → Agents[] (assigned to)
```

-----

## E.20 Config.json Master Structure

```json
{
  "user": {
    "name": "Alex Chen",
    "timezone": "Europe/Berlin",
    "language": "en",
    "preferences": { "theme": "system", "font_size": "medium" }
  },

  "agents": {
    "defaults": {
      "model": "claude-opus",
      "provider": "anthropic",
      "workspace": "~/.omnipus/agents/",
      "restrict_to_workspace": true
    },
    "list": []
  },

  "providers": {
    "anthropic": { "api_key_ref": "ANTHROPIC_API_KEY" },
    "openai": { "api_key_ref": "OPENAI_API_KEY" },
    "ollama": { "api_base": "http://localhost:11434" }
  },

  "channels": {
    "telegram": { "enabled": true, "config": {}, "policies": {} },
    "whatsapp": { "enabled": false, "config": { "mode": "personal" } }
  },

  "tools": {
    "web": { "default_provider": "duckduckgo", "brave": { "api_key_ref": "BRAVE_API_KEY" } },
    "exec": { "approval": "ask", "timeout": 30, "dangerous_patterns": ["rm -rf", "mkfs", "shutdown"] },
    "browser": { "mode": "managed", "headless": true, "page_timeout": 30, "max_tabs": 5 },
    "mcp": { "servers": {} }
  },

  "skills": [],

  "security": {
    "default_policy": "deny",
    "ssrf": { "enabled": true, "allow_internal": [] },
    "rate_limits": { "exec": "10/min", "web_search": "30/min", "web_fetch": "20/min" },
    "prompt_injection": { "level": "medium" },
    "credentials": { "encryption": "aes-256-gcm", "kdf": "argon2id", "store": "credentials.json" },
    "audit": { "output": "file", "redaction": true, "tamper_evident": false }
  },

  "agent_loop": {
    "max_tool_iterations": 20,
    "tool_timeout_seconds": 30,
    "bootstrap_max_chars": 20000,
    "compaction": {
      "enabled": true,
      "reserve_tokens": 20000,
      "preserve_recent": 10,
      "memory_flush": true
    }
  },

  "storage": {
    "retention": {
      "session_days": 90,
      "archive_before_delete": true,
      "keep_compaction_summary": true
    }
  },

  "gateway": {
    "bind": "localhost",
    "port": 18800,
    "auth": { "mode": "token", "token_ref": "GATEWAY_TOKEN" }
  },

  "routing": {
    "default_agent_id": "general-assistant",
    "rules": []
  }
}
```

-----

*End of Appendix E*
