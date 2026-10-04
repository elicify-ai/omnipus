# Debugging Omnipus

Omnipus performs multiple complex interactions under the hood for every single request it receives — from routing messages and evaluating complexity, to executing tools and adapting to model failures. Being able to see exactly what is happening is crucial, not just for troubleshooting potential issues, but also for truly understanding how the agent operates.

## Starting Omnipus in Debug Mode

To get detailed information about what the agent is doing (LLM requests, tool calls, message routing), start the Omnipus gateway with the debug flag:

```bash
omnipus start --debug
# or
omnipus start -d
```

In this mode, the system formats logs extensively and displays previews of system prompts and tool execution results.

## Choosing the diagnostic log file

Set `OMNIPUS_LOG_FILE` before starting the gateway to send its JSON diagnostic records to a specific file instead of the normal gateway log in the data directory:

```bash
export OMNIPUS_LOG_FILE=~/omnipus-logs/gateway.jsonl
```

The gateway creates the parent directory if needed and appends to the file. A file the gateway creates is private to your account (mode `0600`); a file that already exists keeps the permissions it already has, so check an existing destination's permissions. The setting applies from the first startup record onward. An unset or empty value keeps the default destination. A home-relative value is expanded using `HOME`, when that variable is set.

The destination does not change which records are enabled. The gateway defaults to `warn`; set `OMNIPUS_LOG_LEVEL=info` before starting it to include structured agent-turn diagnostics, such as `turn_end`, or use debug mode for more detail.

Diagnostics may contain conversation and tool content, especially in debug mode. Use a private directory that only your account can read; custom destinations do not inherit the default log directory's private permissions.

Restart the gateway after changing the value. If the selected file cannot be opened, startup fails with `error enabling file logging`; it does not silently switch back to the default file. Check the destination's permissions and available disk space. This setting moves diagnostic records only; the separate panic log and security audit log keep their own destinations.

## Disabling Log Truncation (Full Logs)

By default, Omnipus truncates very long strings (such as the *System Prompt* or large JSON output results) in the debug logs to keep the console readable.

If you need to inspect the complete output of a command or the exact payload sent to the LLM model, use the `--no-truncate` flag.

**Note:** This flag *only* works when combined with `--debug` mode.

```bash
omnipus start --debug --no-truncate
```

When this flag is active, the global truncation function is disabled. This is useful for verifying the exact syntax of messages sent to the provider, reading complete output from tools such as `bash`, `fetch_url`, or `read_file`, and debugging session history.

## Tool Call Visibility in Debug Logs

When debug mode is active, the agent emits structured log entries at each stage of the tool execution lifecycle. These entries carry a `component=agent` label and use `INFO` or `DEBUG` level depending on the amount of detail:

| Log message | Level | Key fields | Description |
|---|---|---|---|
| `LLM requested tool calls` | INFO | `tools`, `count`, `iteration` | List of tool names the model decided to call |
| `Tool call: <name>(<args>)` | INFO | `tool`, `iteration` | The tool name and a preview of its arguments (truncated to 200 chars) |
| `Sent tool result to user` | DEBUG | `tool`, `content_len` | Fired when a tool result is forwarded to the chat channel |
| `TTL tick after tool execution` | DEBUG | `agent_id`, `iteration` | MCP tool-discovery TTL decrement after each tool round |
| `Async tool completed, publishing result` | INFO | `tool`, `content_len`, `channel` | Only for tools that run asynchronously in the background |

### Reading a Tool Call Log Entry

A typical synchronous tool call produces two consecutive lines in the console:

```
[...] [INFO] agent: LLM requested tool calls {tools=[search_web], count=1, iteration=1}
[...] [INFO] agent: Tool call: search_web({"query":"omnipus release notes"}) {tool=search_web, iteration=1}
```

The arguments preview is hard-capped at **200 characters** in the logs regardless of the `--no-truncate` flag, because it belongs to the `INFO`-level path. Use `--no-truncate` together with `--debug` to see the full `tools_json` field emitted by the `Full LLM request` DEBUG entry, which contains every tool definition sent to the model.

## Real-Time Tool Feedback in Chat (tool_feedback)

Debug logs are server-side only. If you want the agent to send a visible notification directly into the chat channel every time it executes a tool — useful when sharing the bot with other users or for transparency — enable the `tool_feedback` feature in `config.json`:

```json
{
  "agents": {
    "defaults": {
      "tool_feedback": {
        "enabled": true,
        "max_args_length": 300
      }
    }
  }
}
```

When `enabled` is `true`, every tool call sends a short message to the chat before the tool result is returned to the model. The message looks like:

```bash
Tool: `search_web`
{"query": "omnipus release notes"}
```

### Options

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Send a chat notification for each tool call |
| `max_args_length` | int | `300` | Maximum characters of the serialised arguments included in the notification |

### Environment Variables

Both fields can also be set via environment variables:

```bash
OMNIPUS_AGENTS_DEFAULTS_TOOL_FEEDBACK_ENABLED=true
OMNIPUS_AGENTS_DEFAULTS_TOOL_FEEDBACK_MAX_ARGS_LENGTH=300
```

> **Note:** `tool_feedback` is independent of `--debug` mode. It works in production and does not require the gateway to be started with any special flag.
