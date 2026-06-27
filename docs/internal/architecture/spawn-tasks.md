# 🔄 Spawn & Async Tasks

> Back to [README](../../../README.md)

Omnipus supports **asynchronous task execution** via the `spawn` tool. This is primarily used by the **Heartbeat** system to run long-running tasks without blocking the main agent loop.

## Heartbeat

The heartbeat system periodically checks `workspace/HEARTBEAT.md` for scheduled tasks. On first run, a default template is auto-generated. You can customize it to define quick tasks (handled inline) and long tasks (delegated via `spawn`).

**Example `HEARTBEAT.md`:**

```markdown
## Quick Tasks (respond directly)

- Report current time

## Long Tasks (use spawn for async)

- Search the web for AI news and summarize
- Check email and report important messages
```

**Key behaviors:**

| Feature                 | Description                                               |
| ----------------------- | --------------------------------------------------------- |
| **spawn**               | Creates async subagent, doesn't block heartbeat           |
| **Independent context** | Subagent has its own context, no session history          |
| **message tool**        | Subagent communicates with user directly via message tool |
| **Non-blocking**        | After spawning, heartbeat continues to next task          |

#### How Subagent Communication Works

```
Heartbeat triggers
    ↓
Agent reads HEARTBEAT.md
    ↓
For long task: spawn subagent
    ↓                           ↓
Continue to next task      Subagent works independently
    ↓                           ↓
All tasks done            Subagent uses "message" tool
    ↓                           ↓
Respond HEARTBEAT_OK      User receives result directly
```

The subagent has access to tools (message, web_search, etc.) and can communicate with the user independently without going through the main agent.

**Configuration:**

Heartbeat is configured per-agent. The legacy top-level `"heartbeat"` config block and the `OMNIPUS_HEARTBEAT_ENABLED` / `OMNIPUS_HEARTBEAT_INTERVAL` environment variables have been removed. Use the **Agents screen** (Settings → Agents → select agent → Heartbeat) or the REST API (`PUT /api/v1/agents/{id}`) to set `heartbeat_enabled` and `heartbeat_interval` per agent.

| Field                | Default | Description                        |
| -------------------- | ------- | ---------------------------------- |
| `heartbeat_enabled`  | `true`  | Enable/disable heartbeat           |
| `heartbeat_interval` | `30`    | Check interval in minutes (min: 5) |
