# Configuration

This page is the operator entry point for file-level configuration. User-facing choices belong in the web app and are described in [Settings](../settings.md).

## Files and overrides

By default, Omnipus reads `config.json` from `$OMNIPUS_HOME`, which defaults to `~/.omnipus`. Set `OMNIPUS_HOME` to move the whole data directory, or pass `--config` to select a particular configuration file.

Environment variables can override supported fields. They use uppercase names with underscores, such as `OMNIPUS_GATEWAY_LOG_LEVEL`. One former override is retired: `OMNIPUS_AGENTS_DEFAULTS_MAX_TOOL_ITERATIONS` is copied into `config.json` once, on the first start after upgrading, and ignored after that. The limit it set, `agents.defaults.max_tool_iterations`, is changed in Settings, Performance; see [tool calls per turn](../settings.md#tool-calls-per-turn). Keep credentials out of `config.json`; store them through Settings or the credential commands so the file contains references instead of plaintext secrets.

The gateway listens on port `5000` by default. The web app, application programming interface, WebSocket connection, and preview routes share this listener. Set `gateway.public_url` when a reverse proxy exposes a different public origin.

## Configuration areas

| Area | Operator reference |
|---|---|
| Gateway and proxy | [Reverse proxy](reverse-proxy.md) |
| Process isolation | [Sandbox configuration](sandbox-config.md) and [sandbox limitations](sandbox-limitations.md) |
| Tool backends | [Tools configuration](tools-configuration.md) |
| Credentials and filtering | [Security considerations](security-considerations.md) |
| Provider and model format | [Current provider and model configuration](../migration/model-list-migration.md) |
| Configuration versions | [Configuration versioning](config-versioning.md) |

Provider and model choices change over time. Use the live Providers screen for the available catalog and [Providers and models](../providers-and-models.md) for the user-facing explanation rather than copying a provider list into configuration documentation.

Changes that affect the listening address, public origin, or boot-time sandbox require a gateway restart. The relevant specialist page identifies restart-gated fields.

## Scheduled work

Heartbeats, chat loops and scheduled tasks are three job kinds on one timer. The only store is `cron/jobs.json` under the data directory. Older separate timer files are not read. A job whose kind is not `heartbeat`, `loop` or `task` — including an `agent_turn` job left by the removed schedules API — is dropped at startup and named in the gateway log. Heartbeats are rebuilt from workspace settings, task schedules from the tasks, and an active chat loop continues with one next step: an existing future or repeating step is kept, and a missing or overdue one is rebuilt from the chat's saved loop state (a loop that lived only in an old separate file is rebuilt the same way; the file is not read). See [Calendar](../calendar.md), [chat loops](../loops.md) and [agents](../agents.md).

`schedules.max_concurrent_runs` is removed. A config file that still contains it loads; the key has no effect and is dropped the next time the file is saved. There is no fixed number of scheduled dispatches. While memory has headroom, none are held back by a count. When memory is under pressure, or cannot be measured, a new scheduled dispatch waits while two or more dispatches are already admitted. Work admitted earlier is not cancelled, so the number running can temporarily stay above two. Here, a dispatch is the hand-off that starts the work; it is not necessarily the whole task run. The gateway logs `cron: scheduled run waiting for admission: memory limit reached` when a dispatch waits.

`schedules.retry_backoff_ms` is the only remaining schedules setting. It is the gap, in milliseconds, before each retry of a heartbeat that failed for a temporary reason. The default is one minute, two minutes, then five (`[60000, 120000, 300000]`). It is read when the gateway process starts. A settings reload does not change the running timer. `schedules.run_timeout_seconds` is still ignored: a scheduled run has no time limit.

`POST /api/v1/schedules` and the rest of that collection, and `GET /api/v1/automations`, are removed. After the normal authentication and request-protection checks, an `/api/` path with no route, including those, answers HTTP 404 and JSON `{"error":"endpoint not found"}` — not a web page. Callers that used the schedules API directly have to stop; scheduled work is a task, and running the next time early is `POST /api/v1/tasks/{id}/runs` ([Calendar](../calendar.md#run-a-scheduled-task-now)). The web address `/automations` still redirects to the Calendar.
