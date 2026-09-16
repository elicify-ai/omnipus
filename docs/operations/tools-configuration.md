# Tools Configuration

Omnipus's tools configuration is located in the `tools` field of `config.json`.

## Directory Structure

```json
{
  "tools": {
    "web": {
      ...
    },
    "mcp": {
      ...
    },
    "cron": {
      ...
    },
    "skills": {
      ...
    }
  }
}
```

## Sensitive Data Filtering

Before tool results are sent to the LLM, Omnipus can filter sensitive values (API keys, tokens, secrets) from the output. This prevents the LLM from seeing its own credentials.

See [Security for users](../security.md) for the user-facing limits and [operator security considerations](security-considerations.md#sensitive-value-filtering) for configuration details.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `filter_sensitive_data` | bool | `true` | Enable/disable filtering |
| `filter_min_length` | int | `8` | Minimum content length to trigger filtering |

## Web Tools

Web tools are used for web search and fetching.

### Web Fetcher
General settings for fetching and processing webpage content.

| Config              | Type   | Default       | Description                                                                                   |
|---------------------|--------|---------------|-----------------------------------------------------------------------------------------------|
| `enabled`           | bool   | true          | Enable the webpage fetching capability.                                                       |
| `fetch_limit_bytes` | int    | 10485760      | Maximum size of the webpage payload to fetch, in bytes (default is 10MB).                     |
| `format`            | string | "plaintext"   | Output format of the fetched content. Options: `plaintext` or `markdown` (recommended).       |

### Brave

| Config        | Type   | Default | Description                                                |
|---------------|--------|---------|------------------------------------------------------------|
| `enabled`     | bool   | false   | Enable Brave search                                        |
| `api_key_ref` | string | -       | Env-var name of the Brave Search credential (see below)    |
| `max_results` | int    | 5       | Maximum number of results                                  |

### DuckDuckGo

| Config        | Type | Default | Description               |
|---------------|------|---------|---------------------------|
| `enabled`     | bool | true    | Enable DuckDuckGo search  |
| `max_results` | int  | 5       | Maximum number of results |

### Baidu Search

Baidu Search uses the [Qianfan AI Search API](https://cloud.baidu.com/doc/qianfan-api/s/Wmbq4z7e5), which is AI-powered and optimized for Chinese-language queries.

| Config          | Type   | Default                                                | Description                                                |
|-----------------|--------|--------------------------------------------------------|------------------------------------------------------------|
| `enabled`       | bool   | false                                                  | Enable Baidu Search                                        |
| `api_key_ref`   | string | -                                                      | Env-var name of the Qianfan credential                     |
| `base_url`      | string | `https://qianfan.baidubce.com/v2/ai_search/web_search` | Baidu Search API URL                                       |
| `max_results`   | int    | 5                                                      | Maximum number of results                                  |

```json
{
  "tools": {
    "web": {
      "baidu_search": {
        "enabled": true,
        "api_key_ref": "BAIDU_QIANFAN_API_KEY",
        "max_results": 10
      }
    }
  }
}
```

### Perplexity

| Config        | Type   | Default | Description                                              |
|---------------|--------|---------|----------------------------------------------------------|
| `enabled`     | bool   | false   | Enable Perplexity search                                 |
| `api_key_ref` | string | -       | Env-var name of the Perplexity credential                |
| `max_results` | int    | 5       | Maximum number of results                                |

### Tavily

| Config        | Type   | Default | Description                                              |
|---------------|--------|---------|----------------------------------------------------------|
| `enabled`     | bool   | false   | Enable Tavily search                                     |
| `api_key_ref` | string | -       | Env-var name of the Tavily credential                    |
| `base_url`    | string | -       | Custom Tavily API base URL                               |
| `max_results` | int    | 5       | Maximum number of results                                |

### SearXNG

| Config        | Type   | Default                 | Description               |
|---------------|--------|-------------------------|---------------------------|
| `enabled`     | bool   | false                   | Enable SearXNG search     |
| `base_url`    | string | `http://localhost:8888` | SearXNG instance URL      |
| `max_results` | int    | 5                       | Maximum number of results |

### GLM Search

| Config          | Type   | Default                                           | Description                              |
|-----------------|--------|---------------------------------------------------|------------------------------------------|
| `enabled`       | bool   | false                                             | Enable GLM Search                        |
| `api_key_ref`   | string | -                                                 | Env-var name of the GLM API credential   |
| `base_url`      | string | `https://open.bigmodel.cn/api/paas/v4/web_search` | GLM Search API URL                       |
| `search_engine` | string | `search_std`                                      | Search engine type                       |
| `max_results`   | int    | 5                                                 | Maximum number of results                |

> **Note:** `api_key_ref` stores the name of an environment variable whose value is resolved from the encrypted credential store. The legacy plaintext `api_key` and older `api_keys[]` forms are dropped by the loader. See [providers and models](../providers-and-models.md) for the user-facing provider model.

### Additional Web Settings

| Config                   | Type     | Default | Description                                                    |
|--------------------------|----------|---------|----------------------------------------------------------------|
| `prefer_native`          | bool     | true    | Prefer provider's native search over configured search engines |
| `private_host_whitelist` | string[] | `[]`    | Private/internal hosts allowed for web fetching                |

### `search_web` tool parameters

At runtime, the `search_web` tool accepts the following parameters:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `query` | string | yes | Search query string |
| `count` | integer | no | Number of results to return. Default: `10`, max: `10` |
| `range` | string | no | Optional time filter: `d` (day), `w` (week), `m` (month), `y` (year) |

If `range` is omitted, Omnipus performs an unrestricted search.

### Example `search_web` call

```json
{
  "query": "ai agent news",
  "count": 10,
  "range": "w"
}
```

## Browser Tools

Each workspace has its own browser: a separate Chrome process with its own profile directory on disk, holding its own cookies and its own logins. A workspace cannot see or use another workspace's. Agents on one workspace share that workspace's browser.

All keys below are **whole seconds, written as a plain number** — a duration string such as `"15m"` is a config file that will not load at all.

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `tools.browser.idle_ttl` | int (seconds) | `300` (5 minutes) | How long one TAB may sit with nobody watching it and no tool touching it before it is closed. A negative value turns per-tab reaping off. |
| `tools.browser.idle_close_ttl` | int (seconds) | `900` (15 minutes) | How long a whole BROWSER may sit with no tabs, nobody watching and nothing running before the Chrome process itself is closed. The profile stays on disk, so the workspace is still signed in next time. There is no way to switch this off: `0` and any negative value both mean "use the default", never "never close". |
| `tools.browser.cache_trim_interval` | int (seconds) | `3600` (1 hour) | How often closed profiles are swept for disposable browser cache. This is a sweep frequency, not a size limit — see [browser](../browser.md). |

Both `tools.browser.idle_close_ttl` and `tools.browser.cache_trim_interval` take effect when you save settings; the gateway does not need a restart. Changing `tools.browser.idle_close_ttl` does not disturb a browser that is already open — it changes how long the next idle one is given. A change to `tools.browser.cache_trim_interval` is picked up by the running sweep within about fifteen seconds, including when you shorten it below the time already elapsed since the last sweep — in that case the next sweep runs almost immediately rather than waiting out the old, longer interval.

## Bash tool

`bash` is the shell-command tool. It has no `tools.exec` feature switch: it is registered for every agent and governed by the global and per-agent Allow, Ask, or Deny policies. Set its policy to Deny to block shell commands, or Ask to require approval. See [sandbox configuration](sandbox-config.md) for the process boundary and [tools](../tools.md) for policy behavior.

## Cron Tool

The cron tool is used for scheduling periodic tasks.

| Config                 | Type | Default | Description                                    |
|------------------------|------|---------|------------------------------------------------|
| `exec_timeout_minutes` | int  | 5       | Execution timeout in minutes, 0 means no limit |
| `allow_command`        | bool | false   | Allow cron tasks to execute shell commands      |

## MCP Tool

The MCP tool enables integration with external Model Context Protocol servers.

### Tool Discovery (Lazy Loading)

When connecting to multiple MCP servers, exposing hundreds of tools simultaneously can exhaust the LLM's context window
and increase API costs. The **Discovery** feature solves this by keeping MCP tools *hidden* by default.

Instead of loading all tools, the LLM is provided with a lightweight search tool (using BM25 keyword matching or Regex).
When the LLM needs a specific capability, it searches the hidden library. Matching tools are then temporarily "unlocked"
and injected into the context for a configured number of turns (`ttl`).

### Global Config

| Config      | Type   | Default | Description                                  |
|-------------|--------|---------|----------------------------------------------|
| `enabled`   | bool   | false   | Enable MCP integration globally              |
| `discovery` | object | `{}`    | Configuration for Tool Discovery (see below) |
| `servers`   | object | `{}`    | Map of server name to server config          |

### Discovery Config (`discovery`)

| Config               | Type | Default | Description                                                                                                                       |
|----------------------|------|---------|-----------------------------------------------------------------------------------------------------------------------------------|
| `enabled`            | bool | false   | Global default: if `true`, all MCP tools are hidden and loaded on-demand via search; if `false`, all tools are loaded into context. Individual servers can override this with the per-server `deferred` field. |
| `ttl`                | int  | 5       | Number of conversational turns a discovered tool remains unlocked                                                                 |
| `max_search_results` | int  | 5       | Maximum number of tools returned per search query                                                                                 |
| `use_bm25`           | bool | true    | Let `ToolSearch` match natural-language and keyword queries. **Warning**: consumes more resources than regex search |
| `use_regex`          | bool | false   | Let `ToolSearch` match regular-expression queries |

> **Note:** If `discovery.enabled` is `true`, you MUST enable at least one search engine (`use_bm25` or `use_regex`),
> otherwise the application will fail to start.

### Per-Server Config

| Config     | Type    | Required | Description                                                                                                                                                     |
|------------|---------|----------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `enabled`  | bool    | yes      | Enable this MCP server                                                                                                                                          |
| `deferred` | *bool   | no       | Override deferred mode for this server only. `true` = tools are hidden and discoverable via search; `false` = tools are always visible in context. The field is a `*bool` — **omitted/null** (`nil`) means *use the global `discovery.enabled` setting*; an explicit `true` or `false` overrides the global for this server only. |
| `type`     | string  | no       | Transport type: `stdio`, `sse`, `http`                                                                                                                          |
| `command`  | string  | stdio    | Executable command for stdio transport                                                                                                                          |
| `args`     | array   | no       | Command arguments for stdio transport                                                                                                                           |
| `env`      | object  | no       | Environment variables for stdio process                                                                                                                         |
| `env_file` | string  | no       | Path to environment file for stdio process                                                                                                                      |
| `url`      | string  | sse/http | Endpoint URL for `sse`/`http` transport                                                                                                                         |
| `headers`  | object  | no       | HTTP headers for `sse`/`http` transport                                                                                                                         |

### Transport Behavior

If `type` is omitted, transport is auto-detected: a `url` being set implies `sse`; a `command` being set implies `stdio`. Both `http` and `sse` use `url` plus optional `headers`. The `env` and `env_file` fields are only applied to `stdio` servers.

### Configuration Examples

#### 1) Stdio MCP server

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "filesystem": {
          "enabled": true,
          "command": "npx",
          "args": [
            "-y",
            "@modelcontextprotocol/server-filesystem",
            "/tmp"
          ]
        }
      }
    }
  }
}
```

#### 2) Remote SSE/HTTP MCP server

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "remote-mcp": {
          "enabled": true,
          "type": "sse",
          "url": "https://example.com/mcp",
          "headers": {
            "Authorization": "Bearer YOUR_TOKEN"
          }
        }
      }
    }
  }
}
```

#### 3) Massive MCP setup with Tool Discovery enabled

*In this example, the model sees `ToolSearch`. It can search for and unlock GitHub or Postgres tools
dynamically only when requested by the user.*

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "discovery": {
        "enabled": true,
        "ttl": 5,
        "max_search_results": 5,
        "use_bm25": true,
        "use_regex": false
      },
      "servers": {
        "github": {
          "enabled": true,
          "command": "npx",
          "args": [
            "-y",
            "@modelcontextprotocol/server-github"
          ],
          "env": {
            "GITHUB_PERSONAL_ACCESS_TOKEN": "YOUR_GITHUB_TOKEN"
          }
        },
        "postgres": {
          "enabled": true,
          "command": "npx",
          "args": [
            "-y",
            "@modelcontextprotocol/server-postgres",
            "postgresql://user:password@localhost/dbname"
          ]
        },
        "slack": {
          "enabled": true,
          "command": "npx",
          "args": [
            "-y",
            "@modelcontextprotocol/server-slack"
          ],
          "env": {
            "SLACK_BOT_TOKEN": "YOUR_SLACK_BOT_TOKEN",
            "SLACK_TEAM_ID": "YOUR_SLACK_TEAM_ID"
          }
        }
      }
    }
  }
}
```

#### 4) Mixed setup: per-server deferred override

*Discovery is enabled globally, but `filesystem` is pinned as always-visible while `context7` follows the global
default (deferred). `aws` explicitly opts in to deferred mode even though it is the same as the global default.*

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "discovery": {
        "enabled": true,
        "ttl": 5,
        "max_search_results": 5,
        "use_bm25": true
      },
      "servers": {
        "filesystem": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-filesystem", "/workspace"],
          "deferred": false
        },
        "context7": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "@upstash/context7-mcp"]
        },
        "aws": {
          "enabled": true,
          "command": "npx",
          "args": ["-y", "aws-mcp-server"],
          "deferred": true
        }
      }
    }
  }
}
```

> **Tip:** `deferred` on a per-server basis is independent of `discovery.enabled`. You can keep
> `discovery.enabled: false` globally (all tools visible by default) and still mark individual
> high-volume servers as `"deferred": true` to avoid polluting the context with their tools.

## Skills Tool

The skills tool configures skill discovery and installation via registries like ClawHub.

### Registries

| Config                                 | Type   | Default              | Description                                                                                  |
|----------------------------------------|--------|----------------------|----------------------------------------------------------------------------------------------|
| `registries.clawhub.enabled`           | bool   | true                 | Enable ClawHub registry                                                                      |
| `registries.clawhub.base_url`          | string | `https://clawhub.ai` | ClawHub base URL                                                                             |
| `registries.clawhub.auth_token_ref`    | string | `""`                 | Env-var name of the Bearer token (resolved from the credential store at boot; never store the token value here) |
| `registries.clawhub.search_path`       | string | `""`                 | Search API path                                                                              |
| `registries.clawhub.skills_path`       | string | `""`                 | Skills API path                                                                              |
| `registries.clawhub.download_path`     | string | `""`                 | Download API path                                                                            |
| `registries.clawhub.timeout`           | int    | 0                    | Request timeout in seconds (0 = default)                                                     |
| `registries.clawhub.max_zip_size`      | int    | 0                    | Max skill zip size in bytes (0 = default)                                                    |
| `registries.clawhub.max_response_size` | int    | 0                    | Max API response size in bytes (0 = default)                                                 |

### GitHub Integration

| Config             | Type   | Default | Description                                                                  |
|--------------------|--------|---------|------------------------------------------------------------------------------|
| `github.proxy`     | string | `""`    | HTTP proxy for GitHub API requests                                           |
| `github.token_ref` | string | `""`    | Env-var name of the GitHub personal access token (resolved from the credential store at boot) |

### Search Settings

| Config                    | Type | Default | Description                                |
|---------------------------|------|---------|--------------------------------------------|
| `max_concurrent_searches` | int  | 2       | Max concurrent skill search requests       |
| `search_cache.max_size`   | int  | 50      | Max cached search results                  |
| `search_cache.ttl_seconds`| int  | 300     | Cache TTL in seconds                       |

### Configuration Example

```json
{
  "tools": {
    "skills": {
      "registries": {
        "clawhub": {
          "enabled": true,
          "base_url": "https://clawhub.ai",
          "auth_token_ref": "CLAWHUB_BEARER_TOKEN"
        }
      },
      "github": {
        "proxy": "",
        "token_ref": "GITHUB_PERSONAL_ACCESS_TOKEN"
      },
      "max_concurrent_searches": 2,
      "search_cache": {
        "max_size": 50,
        "ttl_seconds": 300
      }
    }
  }
}
```

## Environment Variables

All configuration options can be overridden via environment variables with the format `OMNIPUS_TOOLS_<SECTION>_<KEY>`:

| Variable | Effect |
|---|---|
| `OMNIPUS_TOOLS_WEB_BRAVE_ENABLED=true` | Enable Brave search |
| `OMNIPUS_TOOLS_CRON_EXEC_TIMEOUT_MINUTES=10` | Set cron exec timeout |
| `OMNIPUS_TOOLS_MCP_ENABLED=true` | Enable MCP integration |

Note: Nested map-style config (for example `tools.mcp.servers.<name>.*`) is configured in `config.json` rather than
environment variables.
