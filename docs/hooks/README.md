# Hook System Reference

This document covers the hook system as implemented at HEAD. It describes both
mounting modes — in-process Go hooks and out-of-process subprocess hooks — and
the exact wire protocol each uses.

The repository does not ship standalone example source files. The Go and Python
examples below are embedded directly in this document. Copy them into your own
files before use.

## Supported Hook Types

| Type | Interface | Stage | Can modify data |
|---|---|---|---|
| Observer | `EventObserver` | EventBus broadcast | No |
| LLM interceptor | `LLMInterceptor` | `before_llm` / `after_llm` | Yes |
| Tool interceptor | `ToolInterceptor` | `before_tool` / `after_tool` | Yes |
| Tool approver | `ToolApprover` | `approve_tool` | Returns `ApprovalDecision` |

Interfaces are defined in `pkg/agent/hooks.go`:

```
EventObserver   OnEvent(ctx, Event) error
LLMInterceptor  BeforeLLM(ctx, *LLMHookRequest)  → (*LLMHookRequest,  HookDecision, error)
                AfterLLM (ctx, *LLMHookResponse) → (*LLMHookResponse, HookDecision, error)
ToolInterceptor BeforeTool(ctx, *ToolCallHookRequest)    → (*ToolCallHookRequest,    HookDecision, error)
                AfterTool (ctx, *ToolResultHookResponse) → (*ToolResultHookResponse, HookDecision, error)
ToolApprover    ApproveTool(ctx, *ToolApprovalRequest) → (ApprovalDecision, error)
```

A single hook struct may implement any combination of these interfaces.

**Where hooks are used in production today.** The non-test `MountHook` call sites at HEAD are:

- `pkg/gateway/websocket.go:478` — WebSocket-mounted `ToolApprover` driving the interactive tool-approval flow. Every `ask`/`always-ask` policy decision in the per-agent tool policy (see `docs/internal/specs/tool-registry-redesign-spec.md` "Approval round-trip" and `pkg/policy/admin_ask_fence.go`) eventually arrives at that hook.
- `pkg/agent/hook_mount.go:153` — builtin hooks loaded from `hooks.builtins.<name>` config.
- `pkg/agent/hook_mount.go:176` — process hooks loaded from `hooks.processes.<name>` config.
- `pkg/agent/hook_process.go:507` — re-registration path inside the process-hook handshake helper.

Custom hooks can supplement these; if you replace the WebSocket `ToolApprover`, you take over the approval round-trip.

## Hook Actions

Interceptor hooks return a `HookDecision` with one of these `action` values
(`pkg/agent/hooks.go:35-41`):

| Action | Interceptor stages | Meaning |
|---|---|---|
| `continue` | all | Accept the (possibly modified) value and continue |
| `modify` | all | Accept the modified value and continue |
| `deny_tool` | `before_tool` only | Skip this tool call |
| `abort_turn` | all | Stop the current turn gracefully |
| `hard_abort` | all | Stop the current turn immediately |

An empty action string is normalised to `continue`
(`pkg/agent/hooks.go:48-53`).

`ToolApprover` returns an `ApprovalDecision` with a `Verdict` field
(`pkg/agent/hooks.go:67-72`):

```
type ApprovalDecision struct {
    Verdict ApprovalVerdict `json:"verdict"`
    Reason  string          `json:"reason,omitempty"`
}
```

Valid verdicts: `"allow"`, `"deny"`, `"always"`. `allow` and `always` both
permit execution; `always` additionally remembers the preference for the
session.

## Execution Order

`HookManager.rebuildOrdered()` (`pkg/agent/hooks.go:500-512`) sorts the hook
slice in this order:

1. In-process hooks before subprocess hooks (by `HookSource` value: 0 vs 1)
2. Lower `Priority` value first within the same source
3. Name order as the final tie-breaker

## Timeouts

Default timeout constants (`pkg/agent/hooks.go:17-30`):

| Stage | Default |
|---|---|
| Observer | 500 ms |
| Interceptor (`before_llm`, `after_llm`, `before_tool`, `after_tool`) | 5 s |
| Approval | 120 s |

The approval timeout is intentionally longer than the WebSocket approval
timeout (90 s) so that interactive UI approval has time to complete before the
hook is killed. **If a `ToolApprover` hook times out, the result is a `Deny`
verdict** (see `runApprovalHook` in `pkg/agent/hooks.go`) — fail-closed by
design.

Overriding defaults via `config.json`:

```json
{
  "hooks": {
    "defaults": {
      "observer_timeout_ms": 500,
      "interceptor_timeout_ms": 5000,
      "approval_timeout_ms": 120000
    }
  }
}
```

Per-process-hook `timeout_ms` is **not supported**. Timeouts are global only.

## Quick Start

To validate the hook flow with minimal effort, use the Python process-hook:

1. Set `hooks.enabled: true` in config.
2. Save the Python example from this document to `/tmp/review_gate.py`.
3. Add the process hook block below to your `~/.omnipus/config.json`.
4. Restart the gateway.
5. Watch `tail -f /tmp/omnipus-hook-review-gate.log`.

```json
{
  "hooks": {
    "enabled": true,
    "processes": {
      "py_review_gate": {
        "enabled": true,
        "priority": 100,
        "transport": "stdio",
        "command": ["python3", "/tmp/review_gate.py"],
        "observe": ["tool_exec_start", "tool_exec_end", "tool_exec_skipped"],
        "intercept": ["before_tool", "approve_tool"],
        "env": {
          "OMNIPUS_HOOK_LOG_FILE": "/tmp/omnipus-hook-review-gate.log"
        }
      }
    }
  }
}
```

To also validate the in-process chain, use the Go example below and call
`al.MountHook(...)` after `AgentLoop` is initialized.

## Go In-Process Example

A minimal logging hook that implements all four interfaces. It only records
activity; it never rewrites or denies.

Save as e.g. `pkg/myhooks/example_logger.go`:

```go
package myhooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/logger"
)

type ExampleLoggerHookOptions struct {
	LogFile   string `json:"log_file,omitempty"`
	LogEvents bool   `json:"log_events,omitempty"`
}

type ExampleLoggerHook struct {
	logFile   string
	logEvents bool
	mu        sync.Mutex
}

func NewExampleLoggerHook(opts ExampleLoggerHookOptions) *ExampleLoggerHook {
	return &ExampleLoggerHook{
		logFile:   strings.TrimSpace(opts.LogFile),
		logEvents: opts.LogEvents,
	}
}

// OnEvent implements agent.EventObserver.
func (h *ExampleLoggerHook) OnEvent(ctx context.Context, evt agent.Event) error {
	_ = ctx
	if !h.logEvents {
		return nil
	}
	h.record("event", evt.Meta, map[string]any{
		"event":   evt.Kind.String(),
		"payload": evt.Payload,
	}, nil)
	return nil
}

// BeforeLLM implements agent.LLMInterceptor.
func (h *ExampleLoggerHook) BeforeLLM(
	ctx context.Context,
	req *agent.LLMHookRequest,
) (*agent.LLMHookRequest, agent.HookDecision, error) {
	_ = ctx
	h.record("before_llm", req.Meta, req, agent.HookDecision{Action: agent.HookActionContinue})
	return req, agent.HookDecision{Action: agent.HookActionContinue}, nil
}

// AfterLLM implements agent.LLMInterceptor.
func (h *ExampleLoggerHook) AfterLLM(
	ctx context.Context,
	resp *agent.LLMHookResponse,
) (*agent.LLMHookResponse, agent.HookDecision, error) {
	_ = ctx
	h.record("after_llm", resp.Meta, resp, agent.HookDecision{Action: agent.HookActionContinue})
	return resp, agent.HookDecision{Action: agent.HookActionContinue}, nil
}

// BeforeTool implements agent.ToolInterceptor.
func (h *ExampleLoggerHook) BeforeTool(
	ctx context.Context,
	call *agent.ToolCallHookRequest,
) (*agent.ToolCallHookRequest, agent.HookDecision, error) {
	_ = ctx
	h.record("before_tool", call.Meta, call, agent.HookDecision{Action: agent.HookActionContinue})
	return call, agent.HookDecision{Action: agent.HookActionContinue}, nil
}

// AfterTool implements agent.ToolInterceptor.
func (h *ExampleLoggerHook) AfterTool(
	ctx context.Context,
	result *agent.ToolResultHookResponse,
) (*agent.ToolResultHookResponse, agent.HookDecision, error) {
	_ = ctx
	h.record("after_tool", result.Meta, result, agent.HookDecision{Action: agent.HookActionContinue})
	return result, agent.HookDecision{Action: agent.HookActionContinue}, nil
}

// ApproveTool implements agent.ToolApprover.
func (h *ExampleLoggerHook) ApproveTool(
	ctx context.Context,
	req *agent.ToolApprovalRequest,
) (agent.ApprovalDecision, error) {
	_ = ctx
	decision := agent.ApprovalDecision{Verdict: agent.VerdictAllow}
	h.record("approve_tool", req.Meta, req, decision)
	return decision, nil
}

func (h *ExampleLoggerHook) record(stage string, meta agent.EventMeta, payload any, decision any) {
	logger.InfoCF("hooks", "Example hook observed", map[string]any{"stage": stage})
	if h.logFile == "" {
		return
	}

	entry := map[string]any{
		"ts":       time.Now().UTC(),
		"stage":    stage,
		"meta":     meta,
		"payload":  payload,
		"decision": decision,
	}
	body, err := json.Marshal(entry)
	if err != nil {
		logger.WarnCF("hooks", "Example hook log encode failed", map[string]any{
			"stage": stage, "error": err.Error(),
		})
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if dir := filepath.Dir(h.logFile); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.WarnCF("hooks", "Example hook log mkdir failed", map[string]any{
				"stage": stage, "path": h.logFile, "error": err.Error(),
			})
			return
		}
	}

	file, err := os.OpenFile(h.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		logger.WarnCF("hooks", "Example hook log open failed", map[string]any{
			"stage": stage, "path": h.logFile, "error": err.Error(),
		})
		return
	}
	defer func() { _ = file.Close() }()

	if _, err := file.Write(append(body, '\n')); err != nil {
		logger.WarnCF("hooks", "Example hook log write failed", map[string]any{
			"stage": stage, "path": h.logFile, "error": err.Error(),
		})
	}
}
```

### Mounting via code

Call this after `AgentLoop` is initialized (`MountHook` is defined at `pkg/agent/loop.go:1991-1992`):

```go
hook := myhooks.NewExampleLoggerHook(myhooks.ExampleLoggerHookOptions{
    LogFile:   "/tmp/omnipus-hook-example-logger.log",
    LogEvents: true,
})

if err := al.MountHook(agent.NamedHook("example-logger", hook)); err != nil {
    panic(err)
}
```

`NamedHook` is a convenience constructor defined in `pkg/agent/hooks.go:101-107`
that sets `Source: HookSourceInProcess` with zero priority.

### Mounting via config (builtin factory)

Register the factory in an `init()` so it is available before `AgentLoop`
reads config. Place this alongside the hook definition:

```go
package myhooks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
)

func init() {
	if err := agent.RegisterBuiltinHook("example_logger", func(
		ctx context.Context,
		spec config.BuiltinHookConfig,
	) (any, error) {
		_ = ctx
		var opts ExampleLoggerHookOptions
		if len(spec.Config) > 0 {
			if err := json.Unmarshal(spec.Config, &opts); err != nil {
				return nil, fmt.Errorf("decode example_logger config: %w", err)
			}
		}
		return NewExampleLoggerHook(opts), nil
	}); err != nil {
		panic(err)
	}
}
```

Once registered the following config drives mounting automatically:

```json
{
  "hooks": {
    "enabled": true,
    "builtins": {
      "example_logger": {
        "enabled": true,
        "priority": 10,
        "config": {
          "log_file": "/tmp/omnipus-hook-example-logger.log",
          "log_events": true
        }
      }
    }
  }
}
```

### What to expect in the log

Requests that hit only the LLM path emit `before_llm` and `after_llm`. Requests that trigger tools also emit `before_tool`, `approve_tool`, and `after_tool`. With `log_events: true`, every `EventBus` broadcast appears as `"stage":"event"`.

Typical lines:

```json
{"ts":"2026-03-21T14:10:00Z","stage":"before_tool","meta":{"SessionKey":"session-1"},"payload":{"tool":"echo_text","arguments":{"text":"hello"}},"decision":{"action":"continue"}}
{"ts":"2026-03-21T14:10:00Z","stage":"approve_tool","meta":{"SessionKey":"session-1"},"payload":{"tool":"echo_text","arguments":{"text":"hello"}},"decision":{"verdict":"allow"}}
```

## Python Process-Hook Example

A minimal subprocess hook using only the Python standard library. It handles
all six RPC methods and only logs; it never rewrites or denies.

Save to e.g. `/tmp/review_gate.py`:

```python
#!/usr/bin/env python3
from __future__ import annotations

import json
import os
import signal
import sys
from datetime import datetime, timezone
from typing import Any

LOG_EVENTS = os.getenv("OMNIPUS_HOOK_LOG_EVENTS", "1").lower() not in {"0", "false", "no"}
LOG_FILE = os.getenv("OMNIPUS_HOOK_LOG_FILE", "").strip()


def append_log(entry: dict[str, Any]) -> None:
    if not LOG_FILE:
        return
    payload = {"ts": datetime.now(timezone.utc).isoformat(), **entry}
    try:
        log_dir = os.path.dirname(LOG_FILE)
        if log_dir:
            os.makedirs(log_dir, exist_ok=True)
        with open(LOG_FILE, "a", encoding="utf-8") as handle:
            handle.write(json.dumps(payload, ensure_ascii=True) + "\n")
    except OSError as exc:
        log_stderr(f"failed to write hook log file {LOG_FILE}: {exc}")


def send_response(message_id: int, result: Any | None = None, error: str | None = None) -> None:
    payload: dict[str, Any] = {"jsonrpc": "2.0", "id": message_id}
    if error is not None:
        payload["error"] = {"code": -32000, "message": error}
    else:
        payload["result"] = result if result is not None else {}

    append_log({
        "direction": "out",
        "id": message_id,
        "response": payload.get("result"),
        "error": payload.get("error"),
    })

    try:
        sys.stdout.write(json.dumps(payload, ensure_ascii=True) + "\n")
        sys.stdout.flush()
    except BrokenPipeError:
        raise SystemExit(0) from None


def log_stderr(message: str) -> None:
    try:
        sys.stderr.write(message + "\n")
        sys.stderr.flush()
    except BrokenPipeError:
        raise SystemExit(0) from None


def handle_shutdown_signal(signum: int, _frame: Any) -> None:
    raise KeyboardInterrupt(f"received signal {signum}")


def handle_before_tool(params: dict[str, Any]) -> dict[str, Any]:
    _ = params
    return {"action": "continue"}


def handle_approve_tool(params: dict[str, Any]) -> dict[str, Any]:
    _ = params
    # ApprovalDecision.Verdict — valid values: "allow", "deny", "always"
    return {"verdict": "allow"}


def handle_request(method: str, params: dict[str, Any]) -> dict[str, Any]:
    if method == "hook.hello":
        return {"ok": True, "name": "python-review-gate"}
    if method == "hook.before_tool":
        return handle_before_tool(params)
    if method == "hook.approve_tool":
        return handle_approve_tool(params)
    if method == "hook.before_llm":
        return {"action": "continue"}
    if method == "hook.after_llm":
        return {"action": "continue"}
    if method == "hook.after_tool":
        return {"action": "continue"}
    raise KeyError(f"method not found: {method}")


def main() -> int:
    try:
        for raw_line in sys.stdin:
            line = raw_line.strip()
            if not line:
                continue

            try:
                message = json.loads(line)
            except json.JSONDecodeError as exc:
                log_stderr(f"failed to decode request: {exc}")
                append_log({"direction": "in", "decode_error": str(exc), "raw": line})
                continue

            method = message.get("method")
            message_id = message.get("id", 0)
            params = message.get("params") or {}
            if not isinstance(params, dict):
                params = {}

            append_log({
                "direction": "in",
                "id": message_id,
                "method": method,
                "params": params,
                "notification": not bool(message_id),
            })

            if not message_id:
                if method == "hook.event" and LOG_EVENTS:
                    # params['Kind'] is the canonical string name
                    # (e.g. "tool_exec_start"). See §"Observable Event Kinds".
                    log_stderr(f"observed event kind: {params.get('Kind')}")
                continue

            try:
                result = handle_request(str(method or ""), params)
            except KeyError as exc:
                send_response(int(message_id), error=str(exc))
                continue
            except Exception as exc:
                send_response(int(message_id), error=f"unexpected error: {exc}")
                continue

            send_response(int(message_id), result=result)
    except KeyboardInterrupt:
        return 0
    return 0


if __name__ == "__main__":
    signal.signal(signal.SIGINT, handle_shutdown_signal)
    signal.signal(signal.SIGTERM, handle_shutdown_signal)
    raise SystemExit(main())
```

### Environment variables

| Variable | Default | Meaning |
|---|---|---|
| `OMNIPUS_HOOK_LOG_FILE` | _(none)_ | Append all inbound/outbound messages as JSON Lines. No file written when unset. |
| `OMNIPUS_HOOK_LOG_EVENTS` | `1` | Write `hook.event` summaries to `stderr`. Set to `0` to suppress. |

### Confirming it works

Watch two places: the **gateway logs** (confirm the process started and see `stderr` lines) and the **`OMNIPUS_HOOK_LOG_FILE`** (see exact requests and responses).

| What you see | Meaning |
|---|---|
| Only `hook.hello` | Handshake succeeded; no business hook request has arrived yet |
| `hook.event` | `observe` config is working |
| `hook.before_tool` | `intercept: ["before_tool"]` config is working |
| `hook.approve_tool` | `approve_tool` approval path is working |

Expected outbound responses from this example (observe-only, no rewrite):

```json
{"direction":"out","id":7,"response":{"action":"continue"},"error":null}
{"direction":"out","id":8,"response":{"verdict":"allow"},"error":null}
```

Full sample trace:

```json
{"ts":"2026-03-21T14:12:00+00:00","direction":"in","id":1,"method":"hook.hello","params":{"name":"py_review_gate","version":1,"modes":["observe","tool","approve"]},"notification":false}
{"ts":"2026-03-21T14:12:00+00:00","direction":"out","id":1,"response":{"ok":true,"name":"python-review-gate"},"error":null}
// ... intermediate hook.event notifications (id=0) and unrelated RPCs omitted ...
{"ts":"2026-03-21T14:12:05+00:00","direction":"in","id":0,"method":"hook.event","params":{"Kind":"tool_exec_start","Time":"2026-03-21T14:12:05Z","Meta":{"agent":"jim"},"Payload":{}},"notification":true}
{"ts":"2026-03-21T14:12:05+00:00","direction":"in","id":7,"method":"hook.before_tool","params":{"tool":"echo_text","arguments":{"text":"hello"}},"notification":false}
{"ts":"2026-03-21T14:12:05+00:00","direction":"out","id":7,"response":{"action":"continue"},"error":null}
{"ts":"2026-03-21T14:12:05+00:00","direction":"in","id":8,"method":"hook.approve_tool","params":{"tool":"echo_text","arguments":{"text":"hello"}},"notification":false}
{"ts":"2026-03-21T14:12:05+00:00","direction":"out","id":8,"response":{"verdict":"allow"},"error":null}
```

`notification: true` means no response is expected (used for `hook.event`). `id` is a monotonically increasing `uint64` per process lifetime; it resets to 1 when the process restarts. Timestamps are UTC ISO-8601.

## Process-Hook Protocol

The subprocess hook uses JSON-RPC 2.0 over stdio
(`pkg/agent/hook_process.go`):

Omnipus starts the external process via `exec.Command`. One JSON object per line is written in each direction: the host writes to the process's stdin and the process writes to its stdout.

`hook.event` is a **notification** (no `id`, no response expected). All other methods are request/response: `hook.hello`, `hook.before_llm`, `hook.after_llm`, `hook.before_tool`, `hook.after_tool`, `hook.approve_tool`.

`stderr` is drained by the host and forwarded to the gateway log at `WARN` level (`pkg/agent/hook_process.go:448-456`, started at `pkg/agent/hook_process.go:138`). The host does not currently accept RPCs initiated by the process. A subprocess hook can only respond to Omnipus calls; it cannot call back into the host.

### Handshake

On startup, before any hook request is dispatched, the host sends
`hook.hello` (`pkg/agent/hook_process.go:287-307`):

```json
{"jsonrpc":"2.0","id":1,"method":"hook.hello","params":{"name":"py_review_gate","version":1,"modes":["observe","tool","approve"]}}
```

`modes` reflects which capabilities are active for this process hook (any
combination of `"observe"`, `"llm"`, `"tool"`, `"approve"`). The process must
reply with a success result before any other RPC is sent.

### Response shapes

**Interceptor response** (`before_llm`, `after_llm`, `before_tool`,
`after_tool`):

```json
{"jsonrpc":"2.0","id":N,"result":{"action":"continue"}}
```

To modify the payload, include the mutated object under the matching key:

```json
{"jsonrpc":"2.0","id":N,"result":{"action":"modify","call":{"tool":"echo_text","arguments":{"text":"rewritten"}}}}
```

Keys per method:

| Method | Payload key to modify |
|---|---|
| `hook.before_llm` | `"request"` |
| `hook.after_llm` | `"response"` |
| `hook.before_tool` | `"call"` |
| `hook.after_tool` | `"result"` |

If the key is absent or null, the original is used unchanged.

**Approval response** (`approve_tool`):

```json
{"jsonrpc":"2.0","id":N,"result":{"verdict":"allow"}}
```

Valid verdicts: `"allow"`, `"deny"`, `"always"`.

**Error response** (any method):

```json
{"jsonrpc":"2.0","id":N,"error":{"code":-32000,"message":"reason"}}
```

A non-null `error` causes the host to treat the hook as failed. For interceptor
hooks this logs a warning and continues with the unmodified value. For approval
hooks, failure results in a `deny` verdict.

## Observable Event Kinds

All `EventKind` strings accepted by the `observe` config field
(`pkg/agent/events.go`). When events are delivered over the process-hook
wire (`hook.event` notification), `Kind` arrives as the canonical string
name (e.g. `"tool_exec_start"`) — `EventKind` implements `MarshalJSON`
to emit the stable string form rather than the underlying uint8 index.
In-process observers receive the typed `EventKind` value and can call
`evt.Kind.String()` to recover the same name.

```
turn_start            turn_end              llm_request
llm_delta             llm_response          llm_retry
context_compress      session_summarize     tool_exec_start
tool_exec_end         tool_exec_skipped     steering_injected
follow_up_queued      interrupt_received    subturn_spawn
subturn_end           subturn_result_delivered  subturn_orphan
error                 turn_timeout          empty_response_retry
compaction_retry      background_process_kill   rate_limit
whatsapp_pairing      notification
```

Use `"*"` or `"all"` in the `observe` list to subscribe to every kind.
An empty string `""` entry has the same effect.

## Configuration Reference

### `hooks` (root)

| Field | Type | Description |
|---|---|---|
| `enabled` | bool | Master switch. If false, no hooks are loaded or invoked. |
| `defaults` | object | Timeout overrides (see below). |
| `builtins` | map | Named in-process hook instances. Key is the hook name. |
| `processes` | map | Named subprocess hook instances. Key is the hook name. |

### `hooks.defaults`

Fields match `HookDefaultsConfig` in `pkg/config/config.go:326-330`:

| Field | Type | Description |
|---|---|---|
| `observer_timeout_ms` | int | Observer timeout. `0` or absent = use code default (500 ms). |
| `interceptor_timeout_ms` | int | Interceptor timeout. `0` or absent = use code default (5 s). |
| `approval_timeout_ms` | int | Approval timeout. `0` or absent = use code default (120 s). |

### `hooks.builtins.<name>`

Fields match `BuiltinHookConfig` in `pkg/config/config.go:332-336`:

| Field | Type | Description |
|---|---|---|
| `enabled` | bool | Whether this hook is mounted at startup. |
| `priority` | int | Sort order; lower fires first. |
| `config` | object | Arbitrary JSON passed as `json.RawMessage` to the factory. |

### `hooks.processes.<name>`

Fields match `ProcessHookConfig` in `pkg/config/config.go:338-347`:

| Field | Type | Description |
|---|---|---|
| `enabled` | bool | Whether this hook is started and mounted. |
| `priority` | int | Sort order; lower fires first. |
| `transport` | string | Only `"stdio"` is supported. Default: `"stdio"`. |
| `command` | []string | Command and arguments. First element is the executable. Required. |
| `dir` | string | Working directory for the subprocess. |
| `env` | map | Extra environment variables merged with the host environment. |
| `observe` | []string | Event kind strings to receive as `hook.event` notifications. Use `["*"]` for all. |
| `intercept` | []string | Hook stages to intercept. Valid values: `"before_llm"`, `"after_llm"`, `"before_tool"`, `"after_tool"`, `"approve_tool"`. |

At least one of `observe` or `intercept` must produce an active mode; a
config with neither enabled is rejected at load time
(`pkg/agent/hook_mount.go:261-263`).

## FAQ

**Can I deny a tool call after it has started executing?** No. Denial happens
in `BeforeTool` or `ApproveTool` — both run before the tool is invoked. Once
the tool starts, `hard_abort` can cancel the entire turn (which cascades to
the tool's context), but you cannot selectively stop a running tool from
inside a hook.

**Can my hook call back into the host (e.g. to send a channel message)?** Not
today. Hooks are unidirectional: the host calls into the hook, the hook
returns a decision. To trigger an outbound action, run a separate process
that talks to the gateway's REST API.

**How do I test a hook?** See `pkg/agent/hook_mount_test.go` (in-process
mounting + protocol) and `pkg/agent/hook_process_test.go` (subprocess
handshake + RPC) for the canonical test patterns.

**Which hook stages can my interceptor block by returning `deny_tool`?** Only
`before_tool`. Other stages return `continue`, `modify`, `abort_turn`, or
`hard_abort`.

## Troubleshooting

Check these in order when a hook is not firing:

1. `hooks.enabled` is `true`.
2. The target builtin or process hook has `enabled: true`.
3. The process-hook `command` path is correct and the executable is executable.
4. You are watching the correct log file.
5. The current request actually reached the stage you care about (e.g. a
   text-only exchange will not trigger `before_tool`).
6. The `observe` or `intercept` list includes the hook point you want.

Use the Python example to validate the external protocol (confirms process startup, handshake, and event delivery). Use the Go example to validate the in-process chain (confirms `MountHook` and the synchronous stages).

If the Python side shows `hook.hello` but no subsequent business requests, the
protocol is fine — the request simply did not reach the expected stage.

### Hung handshake

`NewProcessHook` does **not** wait on the caller's context for `hook.hello`. After normalising the incoming `ctx` to `context.Background()` when nil, it derives a hard-coded 5-second deadline via `helloCtx, cancel := context.WithTimeout(parent, 5*time.Second)` and then calls `ph.hello(helloCtx)` (`pkg/agent/hook_process.go:147-156`). If the subprocess never replies within 5 s the handshake returns an error and the hook is closed. The block-level comment at `pkg/agent/hook_process.go:141-146` documents the rationale (turn-scoped contexts cannot be trusted to have a deadline, and a missing reply would otherwise stall the gateway turn that triggered `ensureHooksInitialized`). No additional kill is required; the per-process recovery is automatic.

## Scope and Limits

### Current use cases

LLM request rewriting (model, messages, options) and tool argument normalization are fully supported. Pre-execution tool approval (interactive or automated) is handled via `BeforeTool` and `ApproveTool`. Auditing and observability are available through `EventObserver`.

### Not yet supported

A subprocess hook calling back into the host to send channel messages is not yet implemented. Suspending a turn and waiting for asynchronous human approval via a separate reply path is not built in — implement this as an `ApprovalManager` that your hook delegates to. Inbound/outbound message interception at the channel level is also not supported.

### Security model for process hooks

Subprocess hooks **inherit the gateway's UID, GID, and full process environment** (including secrets in env vars). The `env` block in the hook config is merged into the host environment, not isolated from it. Process hooks are **not** sandboxed by Landlock or seccomp — they run with the same filesystem and network access as the gateway. Treat the `command` you configure with the same trust posture as any other software you choose to run; only point it at binaries you control.

**Do not re-export credential references into the hook's `env` block.** The gateway resolves secrets from `credentials.json` at boot (see [ADR-004](../internal/architecture/ADR-004-credential-boot-contract.md)); putting `OMNIPUS_MASTER_KEY` or any `*_ref`-resolved secret into a hook's `env` config re-exposes that secret to every subprocess and defeats the credential boundary.

### Protocol-corruption rules for process hooks

**stdout is the JSON-RPC channel** — write log output to stderr only. Any non-JSON-RPC text on stdout corrupts the framing and the host disconnects the hook.

Send **one JSON message per line**, newline-terminated. No pretty-printing.

If the subprocess crashes, closes stdout, or sends malformed JSON, the host marks the hook as failed and continues. The hook's events/decisions stop arriving until the gateway restarts. Subprocess hooks are **not auto-restarted**.

### Known limits

#### Observer buffer

**Observer buffer is `hookObserverBufferSize = 1024`** events per observer (`pkg/agent/hooks.go:30`, with rationale at `pkg/agent/hooks.go:24-29`). A slow `EventObserver` (e.g. a subprocess hook lagging on stdout writes) will silently drop older events when its subscription channel fills. The drop is logged at DEBUG level only. The previous value of 64 was raised to 1024 because it had a 44% drop rate under a tight 200-event producer burst (regression guard for #165).
