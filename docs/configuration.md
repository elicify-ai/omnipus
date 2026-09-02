# ⚙️ Configuration Guide

> Back to [README](../README.md)

## ⚙️ Configuration

Config file: `~/.omnipus/config.json`

### Environment Variables

You can override default paths using environment variables. This is useful for portable installations, containerized deployments, or running omnipus as a system service. These variables are independent and control different paths.

| Variable          | Description                                                                                                                             | Default Path              |
|-------------------|-----------------------------------------------------------------------------------------------------------------------------------------|---------------------------|
| `OMNIPUS_CONFIG` | Overrides the path to the configuration file. This directly tells omnipus which `config.json` to load, ignoring all other locations. | `~/.omnipus/config.json` |
| `OMNIPUS_HOME`   | Overrides the root directory for omnipus data. This changes the default location of the `workspace` and other data directories.          | `~/.omnipus`             |

**Examples:**

```bash
# Run omnipus using a specific config file
# The workspace path will be read from within that config file
OMNIPUS_CONFIG=/etc/omnipus/production.json omnipus start

# Run omnipus with all its data stored in /opt/omnipus
# Config will be loaded from the default ~/.omnipus/config.json
# Workspace will be created at /opt/omnipus/workspace
OMNIPUS_HOME=/opt/omnipus omnipus start

# Use both for a fully customized setup
OMNIPUS_HOME=/srv/omnipus OMNIPUS_CONFIG=/srv/omnipus/main.json omnipus start
```

### Gateway Log Level

`gateway.log_level` controls Gateway log verbosity and is configurable in `config.json`.

```json
{
  "gateway": {
    "log_level": "fatal"
  }
}
```

When omitted, the default is `fatal`. Supported values: `debug`, `info`, `warn`, `error`, `fatal`.

You can also override this with the environment variable `OMNIPUS_LOG_LEVEL`.

### Workspace Layout

Omnipus stores data in your configured workspace (default: `~/.omnipus/workspace`):

```
~/.omnipus/workspace/
├── sessions/          # Conversation sessions and history
├── memory/           # Long-term memory (MEMORY.md)
├── state/            # Persistent state (last channel, etc.)
├── cron/             # Scheduled jobs database
├── skills/           # Custom skills
├── AGENT.md          # Agent behavior guide
├── HEARTBEAT.md      # Periodic task prompts (checked every 30 min)
├── IDENTITY.md       # Agent identity
├── SOUL.md           # Agent soul
└── USER.md           # User preferences
```

> **Note:** Changes to `AGENT.md`, `SOUL.md`, `USER.md` and `memory/MEMORY.md` are automatically detected at runtime via file modification time (mtime) tracking. You do **not** need to restart the gateway after editing these files — the agent picks up the new content on the next request.

### Skill Sources

By default, skills are loaded from three locations in priority order: the workspace directory (`~/.omnipus/workspace/skills`), the global directory (`~/.omnipus/skills`), and the binary-embedded path (`<binary-embedded-path>/skills`, set at build time).

For advanced/test setups, you can override the builtin skills root with:

```bash
export OMNIPUS_BUILTIN_SKILLS=/path/to/skills
```

### Using Skills From Chat Channels

Once skills are installed, you can inspect and force them directly from a chat channel.

`/list skills` shows the installed skill names available to the current agent.

`/use <skill> <message>` forces a specific skill for a single request.

`/use <skill>` arms that skill for your next message in the same chat session.

`/use clear` cancels a pending skill override created by `/use <skill>`.

Examples:

```text
/list skills
/use git explain how to squash the last 3 commits
/use italiapersonalfinance
dammi le ultime news
```

### Unified Command Execution Policy

Generic slash commands are executed through a single path in `pkg/agent/loop.go` via `commands.Executor`. Channel adapters no longer consume generic commands locally; they forward inbound text to the bus/agent path. Telegram still auto-registers supported commands at startup.

An unknown slash command (for example `/foo`) passes through to normal LLM processing. A registered but unsupported command on the current channel (for example `/show` on WhatsApp) returns an explicit user-facing error and stops further processing.

### Agent Bindings (Route messages to specific agents)

Use `bindings` in `config.json` to route incoming messages to different agents by channel/account/context.

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.omnipus/workspace",
      "model_name": "gpt-4o-mini"
    },
    "list": [
      { "id": "main", "default": true, "name": "Main Assistant" },
      { "id": "support", "name": "Support Assistant" },
      { "id": "sales", "name": "Sales Assistant" }
    ]
  },
  "bindings": [
    {
      "agent_id": "support",
      "match": {
        "channel": "telegram",
        "account_id": "*",
        "peer": { "kind": "direct", "id": "user123" }
      }
    },
    {
      "agent_id": "sales",
      "match": {
        "channel": "discord",
        "account_id": "my-discord-bot",
        "guild_id": "987654321"
      }
    }
  ]
}
```

#### `bindings` fields

| Field | Required | Description |
|-------|----------|-------------|
| `agent_id` | Yes | Target agent id in `agents.list` |
| `match.channel` | Yes | Channel name (e.g. `telegram`, `discord`) |
| `match.account_id` | No | Channel account filter. Use `"*"` for all accounts of that channel. If omitted, only default account is matched |
| `match.peer.kind` + `match.peer.id` | No | Exact peer match (e.g. direct chat / topic / group id) |
| `match.guild_id` | No | Guild/server-level match |
| `match.team_id` | No | Team/workspace-level match |

#### Matching priority

When multiple bindings exist, Omnipus resolves in this order:

1. `peer`
2. `parent_peer` (for thread/topic parent contexts)
3. `guild_id`
4. `team_id`
5. `account_id` (non-wildcard)
6. channel wildcard (`account_id: "*"`)
7. default agent

If a binding points to a missing `agent_id`, Omnipus falls back to the default agent.

#### How matching works (step-by-step)

1. Omnipus first filters bindings by `match.channel` (must equal current channel).
2. It then filters by `match.account_id`:
   - omitted: match only the channel's default account
   - `"*"`: match all accounts on this channel
   - explicit value: exact account id match (case-insensitive)
3. From the remaining candidates, it applies the priority chain above and stops at the first hit.

In other words: **channel + account form the candidate set; peer/guild/team then decide final winner**.

#### Common recipes

**1) Route one specific DM user to a specialist agent**

```json
{
  "agent_id": "support",
  "match": {
    "channel": "telegram",
    "account_id": "*",
    "peer": { "kind": "direct", "id": "user123" }
  }
}
```

**2) Route one Discord server (guild) to a dedicated agent**

```json
{
  "agent_id": "sales",
  "match": {
    "channel": "discord",
    "account_id": "my-discord-bot",
    "guild_id": "987654321"
  }
}
```

**3) Route all remaining traffic of a channel to a fallback agent**

```json
{
  "agent_id": "main",
  "match": {
    "channel": "discord",
    "account_id": "*"
  }
}
```

#### Authoring guidelines (important)

Keep exactly one clear default agent in `agents.list` (`"default": true`). Specific rules (`peer`, `guild_id`, `team_id`) and broad rules (`account_id: "*"` only) can coexist safely because priority already guarantees specific rules win.

Avoid duplicate rules with the same specificity and match values. If duplicates exist, the first matching entry in the config array wins. Ensure every `agent_id` exists in `agents.list`; unknown IDs silently fall back to default.

#### Troubleshooting checklist

| Symptom | Check |
|---------|-------|
| Rule not taking effect | `match.channel` spelling (must be exact) |
| Expected account-specific routing but still using default | Verify `match.account_id` equals actual runtime account id |
| Wildcard catches too much traffic | Add more specific `peer/guild/team` rules for critical paths |
| Unexpected default fallback | Confirm `agent_id` exists and is not misspelled |

### 🔒 Security Sandbox

Omnipus runs in a sandboxed environment by default. The agent can only access files and execute commands within the configured workspace.

#### Default Configuration

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.omnipus/workspace"
    }
  }
}
```

| Option      | Default                | Description                     |
| ----------- | ---------------------- | ------------------------------- |
| `workspace` | `~/.omnipus/workspace` | Working directory for the agent |

#### Protected Tools

The following tools are sandboxed to the workspace by default:

| Tool          | Function         | Restriction                            |
| ------------- | ---------------- | -------------------------------------- |
| `read_file`   | Read files       | Only files within workspace            |
| `write_file`  | Write files      | Only files within workspace            |
| `list_dir`    | List directories | Only directories within workspace      |
| `edit_file`   | Edit files       | Only files within workspace            |
| `append_file` | Append to files  | Only files within workspace            |
| `exec`        | Execute commands | Command paths must be within workspace |

#### Additional Exec Protection

The `exec` tool also blocks the following dangerous commands, independent of the workspace restriction:

| Command pattern | Reason blocked |
|---|---|
| `rm -rf`, `del /f`, `rmdir /s` | Bulk deletion |
| `format`, `mkfs`, `diskpart` | Disk formatting |
| `dd if=` | Disk imaging |
| Writing to `/dev/sd[a-z]` | Direct disk writes |
| `shutdown`, `reboot`, `poweroff` | System shutdown |
| Fork bomb `:(){ :|:& };:` | Resource exhaustion |

### File Access Control

| Config Key | Type | Default | Description |
|------------|------|---------|-------------|
| `tools.allow_read_paths` | string[] | `[]` | Additional paths allowed for reading outside workspace |
| `tools.allow_write_paths` | string[] | `[]` | Additional paths allowed for writing outside workspace |

### Exec Security

| Config Key | Type | Default | Description |
|------------|------|---------|-------------|
| `tools.exec.allow_remote` | bool | `false` | Allow exec tool from remote channels (Telegram/Discord etc.) |
| `tools.exec.enable_deny_patterns` | bool | `true` | Enable dangerous command interception |
| `tools.exec.custom_deny_patterns` | string[] | `[]` | Custom regex patterns to block |
| `tools.exec.custom_allow_patterns` | string[] | `[]` | Custom regex patterns to allow |

> **Security Note:** Symlink protection is enabled by default — all file paths are resolved through `filepath.EvalSymlinks` before whitelist matching, preventing symlink escape attacks.

#### Known Limitation: Child Processes From Build Tools

The exec safety guard only inspects the command line Omnipus launches directly. It does not recursively inspect child processes spawned by allowed developer tools such as `make`, `go run`, `cargo`, `npm run`, or custom build scripts.

That means a top-level command can still compile or launch other binaries after it passes the initial guard check. In practice, treat build scripts, Makefiles, package scripts, and generated binaries as executable code that needs the same level of review as a direct shell command.

For higher-risk environments, review build scripts before execution and prefer approval/manual review for compile-and-run workflows. Run Omnipus inside a container or VM if you need stronger isolation than the built-in guard provides.

#### Error Examples

```
[ERROR] tool: Tool execution failed
{tool=exec, error=Command blocked by safety guard (path outside working dir)}
```

```
[ERROR] tool: Tool execution failed
{tool=exec, error=Command blocked by safety guard (dangerous pattern detected)}
```

#### Security Boundary Consistency

The workspace restriction applies consistently across all execution paths:

| Execution Path   | Security Boundary             |
| ---------------- | ----------------------------- |
| Main Agent       | Workspace-only by default ✅  |
| Subagent / Spawn | Inherits same restriction ✅  |
| Heartbeat tasks  | Inherits same restriction ✅  |

All paths share the same workspace restriction — there's no way to bypass the security boundary through subagents or scheduled tasks.

If you need to grant the agent access to additional paths outside the workspace, use the read/write path allow-lists instead of disabling the restriction:

```json
{
  "tools": {
    "allow_read_paths": ["/var/log/myapp"],
    "allow_write_paths": ["/srv/data"]
  }
}
```

See [File Access Control](#file-access-control) above.

### Heartbeat (Periodic Tasks)

Omnipus can perform periodic tasks automatically. Create a `HEARTBEAT.md` file in your workspace:

```markdown
# Periodic Tasks

- Check my email for important messages
- Review my calendar for upcoming events
- Check the weather forecast
```

The agent will read this file every 30 minutes (configurable) and execute any tasks using available tools.

#### Async Tasks with Spawn

For long-running tasks (web search, API calls), use the `spawn` tool to create a **subagent**:

```markdown
# Periodic Tasks

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

Heartbeat is configured per-agent via the **Agents screen** (Settings → Agents → select agent → Heartbeat). There is no top-level `"heartbeat"` config block; the legacy global `heartbeat.enabled` / `heartbeat.interval` fields and the `OMNIPUS_HEARTBEAT_ENABLED` / `OMNIPUS_HEARTBEAT_INTERVAL` environment variables have been removed.

| Field                 | Default | Description                                   |
| --------------------- | ------- | --------------------------------------------- |
| `heartbeat_enabled`   | `true`  | Enable/disable heartbeat for this agent        |
| `heartbeat_interval`  | `30`    | Check interval in minutes (min: 5)             |

These fields live inside each agent's entry under `agents.list[*]` in `config.json`, and are managed via the Agents screen or the REST API (`PUT /api/v1/agents/{id}`).

### Providers

> [!NOTE]
> Groq provides free voice transcription via Whisper. If configured, audio messages from any channel will be automatically transcribed at the agent level.

| Provider     | Purpose                                 | Get API Key                                                  |
| ------------ | --------------------------------------- | ------------------------------------------------------------ |
| `gemini`     | LLM (Gemini direct)                     | [aistudio.google.com](https://aistudio.google.com)           |
| `zhipu`      | LLM (Zhipu direct)                      | [bigmodel.cn](https://bigmodel.cn)                           |
| `volcengine` | LLM (Volcengine direct)                 | [volcengine.com](https://www.volcengine.com/activity/codingplan?utm_campaign=Omnipus&utm_content=Omnipus&utm_medium=devrel&utm_source=OWO&utm_term=Omnipus) |
| `openrouter` | LLM (recommended, access to all models) | [openrouter.ai](https://openrouter.ai)                       |
| `anthropic`  | LLM (Claude direct)                     | [console.anthropic.com](https://console.anthropic.com)       |
| `openai`     | LLM (GPT direct)                        | [platform.openai.com](https://platform.openai.com)           |
| `deepseek`   | LLM (DeepSeek direct)                   | [platform.deepseek.com](https://platform.deepseek.com)       |
| `qwen`       | LLM (Qwen direct)                       | [dashscope.console.aliyun.com](https://dashscope.console.aliyun.com) |
| `groq`       | LLM + **Voice transcription** (Whisper) | [console.groq.com](https://console.groq.com)                 |
| `cerebras`   | LLM (Cerebras direct)                   | [cerebras.ai](https://cerebras.ai)                           |
| `vivgrid`    | LLM (Vivgrid direct)                    | [vivgrid.com](https://vivgrid.com)                           |

### Model Configuration

The `providers` key holds an array of model entries, each shaped as `{"model_name": "<alias>", "model": "<vendor>/<model-id>"}` (e.g. `zhipu/glm-4.7`). Note: `providers` has always been the JSON key name — in the legacy v0 schema it was an *object* keyed by vendor; in the current v1 schema it is an *array* of model entries. The new shape supports per-entry credential references, multi-key failover, and per-agent model selection.

This design also enables **multi-agent support** with flexible provider selection. Each agent can use its own LLM provider. You can configure primary and fallback models for resilience, distribute requests across multiple endpoints for load balancing, and manage all providers in one place through centralized configuration.

#### 🔒 Security Configuration (Recommended)

Omnipus supports separating sensitive data (API keys, tokens, secrets) from your main configuration by storing them in a `.security.yml` file.

Sensitive data is never stored in your main config file, which makes it safe to share `config.json` without exposing API keys. Add `.security.yml` to `.gitignore` to keep it out of version control. Different environments can use different security files for flexible deployment.

**Quick Setup:**

1. Create `~/.omnipus/.security.yml` with your API keys:
```yaml
model_list:
  gpt-5.4:
    api_keys:
      - "sk-proj-your-actual-openai-key"
  claude-sonnet-4.6:
    api_keys:
      - "sk-ant-your-actual-anthropic-key"
channels:
  telegram:
    token: "your-telegram-bot-token"
web:
  brave:
    api_keys:
      - "BSAyour-brave-api-key"
  glm_search:
    api_key: "your-glm-search-api-key"
```

2. Set proper permissions:
```bash
chmod 600 ~/.omnipus/.security.yml
```

3. Remove sensitive fields from `config.json` (recommended):
```json
{
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4"
      // api_key loaded from .security.yml
    }
  ],
  "channels": {
    "telegram": {
      "enabled": true,
      // token loaded from .security.yml
    }
  }
}
```

**How it works:**
Values from `.security.yml` are automatically mapped to config fields. No special syntax is needed — just omit sensitive fields from `config.json`. If a field exists in both files, the `.security.yml` value takes precedence. You can mix direct values in `config.json` with security values.

For complete documentation, see [`security_configuration.md`](security_configuration.md).

#### All Supported Vendors

| Vendor                  | `model` Prefix    | Default API Base                                    | Protocol  | API Key                                                          |
| ----------------------- | ----------------- | --------------------------------------------------- | --------- | ---------------------------------------------------------------- |
| **OpenAI**              | `openai/`         | `https://api.openai.com/v1`                         | OpenAI    | [Get Key](https://platform.openai.com)                           |
| **Anthropic**           | `anthropic/`      | `https://api.anthropic.com/v1`                      | Anthropic | [Get Key](https://console.anthropic.com)                         |
| **智谱 AI (GLM)**       | `zhipu/`          | `https://open.bigmodel.cn/api/paas/v4`              | OpenAI    | [Get Key](https://open.bigmodel.cn/usercenter/proj-mgmt/apikeys) |
| **DeepSeek**            | `deepseek/`       | `https://api.deepseek.com/v1`                       | OpenAI    | [Get Key](https://platform.deepseek.com)                         |
| **Google Gemini**       | `gemini/`         | `https://generativelanguage.googleapis.com/v1beta`  | OpenAI    | [Get Key](https://aistudio.google.com/api-keys)                  |
| **Groq**                | `groq/`           | `https://api.groq.com/openai/v1`                    | OpenAI    | [Get Key](https://console.groq.com)                              |
| **通义千问 (Qwen)**     | `qwen/`           | `https://dashscope.aliyuncs.com/compatible-mode/v1` | OpenAI    | [Get Key](https://dashscope.console.aliyun.com)                  |
| **NVIDIA**              | `nvidia/`         | `https://integrate.api.nvidia.com/v1`               | OpenAI    | [Get Key](https://build.nvidia.com)                              |
| **Ollama**              | `ollama/`         | `http://localhost:11434/v1`                         | OpenAI    | Local (no key needed)                                            |
| **OpenRouter**          | `openrouter/`     | `https://openrouter.ai/api/v1`                      | OpenAI    | [Get Key](https://openrouter.ai/keys)                            |
| **LiteLLM Proxy**       | `litellm/`        | `http://localhost:4000/v1`                          | OpenAI    | Your LiteLLM proxy key                                           |
| **VLLM**                | `vllm/`           | `http://localhost:8000/v1`                          | OpenAI    | Local                                                            |
| **Cerebras**            | `cerebras/`       | `https://api.cerebras.ai/v1`                        | OpenAI    | [Get Key](https://cerebras.ai)                                   |
| **VolcEngine (Doubao)** | `volcengine/`     | `https://ark.cn-beijing.volces.com/api/v3`          | OpenAI    | [Get Key](https://www.volcengine.com/activity/codingplan?utm_campaign=Omnipus&utm_content=Omnipus&utm_medium=devrel&utm_source=OWO&utm_term=Omnipus) |
| **神算云**              | `shengsuanyun/`   | `https://router.shengsuanyun.com/api/v1`            | OpenAI    | —                                                                |
| **Vivgrid**             | `vivgrid/`        | `https://api.vivgrid.com/v1`                        | OpenAI    | [Get Key](https://vivgrid.com)                                   |
| **LongCat**             | `longcat/`        | `https://api.longcat.chat/openai`                   | OpenAI    | [Get Key](https://longcat.chat/platform)                         |
| **ModelScope (魔搭)**   | `modelscope/`     | `https://api-inference.modelscope.cn/v1`            | OpenAI    | [Get Token](https://modelscope.cn/my/tokens)                     |

#### Basic Configuration

```json
{
  "model_list": [
    {
      "model_name": "ark-code-latest",
      "model": "volcengine/ark-code-latest",
      "api_key": "sk-your-api-key"
    },
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_key": "sk-your-openai-key"
    },
    {
      "model_name": "claude-sonnet-4.6",
      "model": "anthropic/claude-sonnet-4.6",
      "api_key": "sk-ant-your-key"
    },
    {
      "model_name": "glm-4.7",
      "model": "zhipu/glm-4.7",
      "api_key": "your-zhipu-key"
    }
  ],
  "agents": {
    "defaults": {
      "model_name": "gpt-5.4"
    }
  }
}
```

> **Security Note**: You can remove `api_key` fields from your config and store them in `.security.yml` instead. See [Security Configuration](#-security-configuration-recommended) above for details.

#### Vendor-Specific Examples

> **Tip**: You can omit `api_key` fields and store them in `.security.yml` for better security. See [Security Configuration](#-security-configuration-recommended).

<details>
<summary><b>OpenAI</b></summary>

```json
{
  "model_name": "gpt-5.4",
  "model": "openai/gpt-5.4"
  // api_key: set in .security.yml
}
```

</details>

<details>
<summary><b>VolcEngine (Doubao)</b></summary>

```json
{
  "model_name": "ark-code-latest",
  "model": "volcengine/ark-code-latest"
  // api_key: set in .security.yml
}
```

</details>

<details>
<summary><b>智谱 AI (GLM)</b></summary>

```json
{
  "model_name": "glm-4.7",
  "model": "zhipu/glm-4.7"
  // api_key: set in .security.yml
}
```

</details>

<details>
<summary><b>DeepSeek</b></summary>

```json
{
  "model_name": "deepseek-chat",
  "model": "deepseek/deepseek-chat"
  // api_key: set in .security.yml
}
```

</details>

<details>
<summary><b>Anthropic</b></summary>

```json
{
  "model_name": "claude-sonnet-4.6",
  "model": "anthropic/claude-sonnet-4.6"
  // api_key: set in .security.yml
}
```

> Set your key with: `omnipus credentials set ANTHROPIC_API_KEY <your-key>`

For direct Anthropic API access or custom endpoints that only support Anthropic's native message format:

```json
{
  "model_name": "claude-opus-4-6",
  "model": "anthropic-messages/claude-opus-4-6",
  "api_key": "sk-ant-your-key",
  "api_base": "https://api.anthropic.com"
}
```

> Use `anthropic-messages` when the endpoint requires Anthropic's native `/v1/messages` format instead of OpenAI-compatible `/v1/chat/completions`.

</details>

<details>
<summary><b>Ollama (local)</b></summary>

```json
{
  "model_name": "llama3",
  "model": "ollama/llama3"
}
```

</details>

<details>
<summary><b>Custom Proxy / LiteLLM</b></summary>

```json
{
  "model_name": "my-custom-model",
  "model": "openai/custom-model",
  "api_base": "https://my-proxy.com/v1"
  // api_key: set in .security.yml
}
```

Omnipus strips only the outer `litellm/` prefix before sending the request, so `litellm/lite-gpt4` sends `lite-gpt4`, while `litellm/openai/gpt-4o` sends `openai/gpt-4o`.

</details>

#### Load Balancing

Configure multiple endpoints for the same model name — Omnipus will automatically round-robin between them:

**Option 1: Multiple API Keys in .security.yml (Recommended)**

```yaml
# .security.yml
model_list:
  gpt-5.4:
    api_keys:
      - "sk-proj-key-1"
      - "sk-proj-key-2"
```

```json
// config.json
{
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_base": "https://api.openai.com/v1"
      // api_keys loaded from .security.yml
    }
  ]
}
```

**Option 2: Multiple Model Entries**

```json
{
  "model_list": [
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_base": "https://api1.example.com/v1",
      "api_key": "sk-key1"
    },
    {
      "model_name": "gpt-5.4",
      "model": "openai/gpt-5.4",
      "api_base": "https://api2.example.com/v1",
      "api_key": "sk-key2"
    }
  ]
}
```

#### Migration from Legacy `providers` Config

The old `providers` configuration is **deprecated** but still supported for backward compatibility. See [docs/migration/model-list-migration.md](migration/model-list-migration.md) for the full guide.

### Provider Architecture

Omnipus routes providers by protocol family. The **OpenAI-compatible** protocol covers OpenRouter, Groq, Zhipu, vLLM-style endpoints, and most others. The **Anthropic** protocol handles Claude-native API behavior. The **Codex/OAuth** path covers the OpenAI OAuth/token authentication route.

This keeps the runtime lightweight while making new OpenAI-compatible backends mostly a config operation (`api_base` + `api_key`).

<details>
<summary><b>Zhipu (legacy providers format)</b></summary>

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.omnipus/workspace",
      "model_name": "glm-4.7",
      "max_tokens": 32768,
      "temperature": 0.7,
      "max_tool_iterations": 50
    }
  },
  "providers": {
    "zhipu": {
      "api_key": "Your API Key",
      "api_base": "https://open.bigmodel.cn/api/paas/v4"
    }
  }
}
```

> **Note**: The `providers` format is deprecated. Use the new `model_list` format with `.security.yml` for better security.

</details>

<details>
<summary><b>Full config example</b></summary>

```json
{
  "agents": {
    "defaults": {
      "model_name": "anthropic/claude-opus-4-6"
    }
  },
  "session": {
    "dm_scope": "per-channel-peer"
  },
  "channels": {
    "telegram": {
      "enabled": true,
      // token: set in .security.yml
      "allow_from": ["123456789"]
    }
  },
  "tools": {
    "web": {
      "duckduckgo": {
        "enabled": true,
        "max_results": 5
      }
    }
  },
  "heartbeat": {
    "enabled": true,
    "interval": 30
  }
}
```

> **Note**: Sensitive fields (`api_key`, `token`, etc.) can be omitted and stored in `.security.yml` for better security.

</details>

### Scheduled Tasks / Reminders

Omnipus supports cron-style scheduled tasks via the `cron` tool. The agent can set, list, and cancel reminders or recurring jobs that trigger at specified times.

```json
{
  "tools": {
    "cron": {
      "enabled": true,
      "exec_timeout_minutes": 5
    }
  }
}
```

Scheduled tasks persist across restarts and are stored in `~/.omnipus/workspace/cron/`.

### Agent Concurrency and Memory

```json
{
  "performance": {
    "max_parallel_agents": 0
  }
}
```

**There is no longer a computed default for `performance.max_parallel_agents`.**
Earlier versions divided the machine's available memory by an assumed per-agent
cost (~3.5 MB) to pick a number once, at startup. That number was one of two
memory mechanisms in the process — the browser tooling sized itself from the
same host's *live* headroom, so the two disagreed about the same machine, each
defensibly.

Concurrency is now bounded by **live available memory at the moment each agent
turn is admitted**. `0` (the default) means *not configured*, not *auto-detect*:
nothing computes a cap for you, and the memory gate refuses to grow when the
host is short. Set a positive value to impose an explicit cap of your own; an
explicit value is always honoured exactly as configured and is never silently
lowered.

Settings → Performance shows **"automatic — bounded by available memory"** when
nothing is configured, rather than an integer. The integer the API reports
alongside it in that state is a physical OS-thread safety backstop, not a
recommendation, and the UI deliberately does not show it.

#### When memory cannot be measured

Some hosts cannot report their available memory at all. Concurrency then holds
at a floor of **two** concurrent agent turns and the third is refused, naming
memory. The system refuses to *grow*, never to *run* — a host that cannot
measure itself can still do work.

Two different situations produce this, and they are not the same:

| Situation | Status |
| --- | --- |
| **Linux with an unreadable `/proc/meminfo`** — gVisor, a distroless image with no procfs mount, a hardened seccomp profile | **Supported deployment.** It works, at the floor. This is a consequence of a deployment choice, documented here so it is expected rather than surprising. |
| **Windows** | **Degraded — unsupported.** No memory reader exists for Windows in this codebase. It is not a deployment choice you can undo, and no amount of physical RAM on the machine will raise the floor. Browser support on Windows is degraded-unsupported for the same reason. |

On either, set `performance.max_parallel_agents` explicitly to get the
concurrency you want — an explicit value is never overridden by any memory
reading.

#### Running in a container

If Omnipus is running inside a container with **no memory limit set**, it logs
one warning at startup: it is sizing against the *node's* memory, not the
container's share. On a large Kubernetes node this means it sees far more
headroom than it actually has and can be OOM-killed. Set
`resources.limits.memory` (Kubernetes) or `--memory` (Docker) and the limit is
picked up automatically, with no configuration change here. Startup is never
refused over this.

Container detection uses `KUBERNETES_SERVICE_HOST`, `/.dockerenv`, and the
container runtime named in `/proc/self/cgroup`. **One case escapes all three:** a
cgroup-v2 pod in its own cgroup namespace, with service links disabled and no
`/.dockerenv`, looks identical to a bare-metal host from the inside. Set
`OMNIPUS_CONTAINERIZED=1` to declare it — that is the only coverage for that
shape, and without it the warning above will not fire even though the condition
applies.

### The Browser

Each workspace has its own browser: a separate Chrome process with its own
profile directory on disk, holding its own cookies and its own logins. A
workspace cannot see or use another workspace's. Agents on one workspace share
that workspace's browser, which is what lets them hand work to each other.

All three keys below are **whole seconds, written as a number** — `900`, not
`"15m"`. A duration string here is a config file that will not load at all.

| Key | Default | What it does |
| --- | --- | --- |
| `tools.browser.idle_ttl` | `300` (5 minutes) | How long one TAB may sit with nobody watching it and no tool touching it before it is closed. A negative value turns per-tab reaping off. |
| `tools.browser.idle_close_ttl` | `900` (15 minutes) | How long a whole BROWSER may sit with no tabs, nobody watching and nothing running before the Chrome process itself is closed. The profile stays on disk, so the workspace is still signed in next time. There is no way to switch this off: `0` and any negative value both mean "use the default", never "never close". |
| `tools.browser.cache_trim_interval` | `3600` (1 hour) | How often closed profiles are swept for disposable browser cache. See the warning below — this is a sweep frequency, not a size limit. |

Both of the last two take effect when you save settings; the gateway does not
need a restart. Changing `idle_close_ttl` does not disturb a browser that is
already open — it changes how long the next idle one is given.

**How many browsers can run at once.** There is no setting for this, and there
is deliberately none to raise. A browser starts only if the machine has room
for it, measured at the moment it starts. When it does not, the least recently
used workspace's browser is closed to make room — that workspace stays signed
in and its browser comes back the next time an agent uses it. If nothing can be
freed, the request is refused with a message naming memory. Any message that
told you to raise a limit would be sending you after a setting that does not
exist.

**`cache_trim_interval` does not bound how large a profile gets.** Nothing is
ever trimmed while a browser is running — trimming a running browser's cache
would mean closing a browser somebody is using — so a workspace that is driven
continuously, with no idle gap, keeps growing its cache for as long as it is
driven, whatever this interval is set to. The gateway logs this once at
startup. If disk is your binding constraint, the thing that actually reclaims
space is a workspace's browser going idle, not this interval.

**What the trim removes, and what it never touches.** It removes only what
Chrome wrote as a performance cache and can fetch or rebuild: the HTTP cache,
compiled JavaScript, shader and WebGPU caches, downloaded hint models. It never
touches cookies, saved passwords, Local Storage, Session Storage, IndexedDB, a
site's own Cache Storage, or your profile settings. It works from a fixed list
of what may go, not a list of what must stay, so a future Chrome version that
adds a directory nobody here has classified is left alone.

**Windows is degraded and unsupported for the browser.** This codebase has no
way to read available memory on Windows, and the browser will not start a
second instance without one. The floor is one browser, whatever the machine's
physical RAM. This is a gap that is declared rather than worked around; see the
`Known limitations` section of the changelog.

### Advanced Topics

| Topic | Description |
| ----- | ----------- |
| [Security Configuration](security_configuration.md) | Store API keys and secrets in separate `.security.yml` file |
| [Sensitive Data Filtering](sensitive_data_filtering.md) | Filter API keys and tokens from tool results before sending to LLM |
| [Hook System](hooks/README.md) | Event-driven hooks: observers, interceptors, approval hooks |
| [Steering](internal/architecture/steering.md) | Inject messages into a running agent loop between tool calls |
| [SubTurn](internal/architecture/subturn.md) | Subagent coordination, concurrency control, lifecycle |
| [Context Management](internal/agent-refactor/context.md) | Context boundary detection, proactive budget check, compression |
