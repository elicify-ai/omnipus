# Observability: session history and the event stream

Every action an Omnipus agent takes is visible. There are two complementary surfaces.

The **session transcript** is the persistent, on-disk record of a conversation. The server builds chat history from a filtered view of it. Private continuation inputs remain in the archive for model delivery and recovery; they are not displayed as messages from you.

The **event stream** is the live runtime feed of typed events emitted as the agent loop runs. It powers the SPA in real time, drives subprocess hooks, and feeds the audit log.

You don't enable these. They run on every install, by default, with no extra services.

## Session transcript

Each session has a folder at `$OMNIPUS_HOME/sessions/<session-id>/` (`~/.omnipus/sessions/<session-id>/` by default). All sessions live in this one shared store; there are no per-agent session folders. The history is one append-only stream of entries (one line per entry, atomic writes via temp-file-rename), split into one file per UTC day: `transcript.jsonl` holds the current day, and earlier days are frozen as `<YYYY-MM-DD>.jsonl` beside it (a small `transcript.day` file records which day `transcript.jsonl` holds). A script that reads a whole session must read all of these files in date order. Paged APIs project requested ranges. Clients that need all retained chat history must explicitly enumerate pages.

### Entry types (`pkg/session/daypartition.go::EntryType`)

| `type` value | What it records |
|---|---|
| `message` (or empty) | A user or assistant chat turn |
| `tool_call` | A tool invocation — name, args, status, duration, result |
| `compaction` | A context-compression summary written when token budget is exceeded |
| `system` | System-level event (boot, shutdown, sandbox state change) |
| `turn_canceled` | A turn that was cancelled mid-stream, with cancel method + descendants |
| `provider_fallback` | The note that a fallback model answered, with the models involved, written once per session and model pair |
| `judge_verdict` | The Judge agent's verdict on a task's completion claim |
| `context_window_notice` | A context-window diagnostic shown only in Verbose chat; never part of the model's history |
| `saved_input` | A private, deferred continuation input. Retained for model delivery and recovery, but excluded from REST chat history and WebSocket snapshot replay. |
| `model_ref`, `tool_call_effect` | Internal records that point at, or correct, an earlier entry. They are written into the same files and are not shown in chat |

### What's on every entry (`pkg/session/daypartition.go::TranscriptEntry`)

```json
{
  "id":         "01HMXG…",          // ULID
  "type":       "tool_call",         // see table above
  "role":       "assistant",         // user | assistant | system
  "content":    "…",
  "timestamp":  "2026-05-21T13:45:02.123Z",
  "tokens":     247,
  "cost":       0.0019,
  "status":     "ok",                // ok | error | interrupted
  "attachments": [/* media */],
  "tool_calls": [
    {
      "id":         "call_…",
      "tool":       "search_web",
      "status":     "success",      // success | error | pending | denied
      "duration_ms": 1230,
      "parameters": {"query": "…"},
      "result":     {"…"}
    }
  ],
  "agent_id":   "mia"
}
```

Every entry carries `agent_id` so multi-agent transcripts can be filtered to a single agent's contribution (FR-002).

### Tool call fidelity

Each saved `ToolCall` retains its parameters and available result. A confirmed cancellation of a dispatched native call records the original call ID, tool, parameters, turn, owner and known duration even when hard Stop prevents normal model-result admission or the controller abandons the execution. The terminal status is `cancelled`; absent admitted output stays `null`, not invented success text. Completed calls remain completed, queued calls are not fabricated as executed, and a late return cannot replace that cancellation. REST pages, detail and snapshot replay preserve the same saved call. When sub-agents are involved, `parent_tool_call_id` correlates child calls to their parent spawn (FR-H-001), so a deeply nested `subagent` → `spawn` → tool chain is reconstructable.

Each `ToolCall` preserves its recorded parameters and known result facts; a cancelled call can have no result. Its original call ID and `cancelled` outcome survive projection separately from the interrupted assistant reply. A `turn_canceled` marker alone is not a replacement tool row, and a call missing from an old archive cannot be recreated from that marker. When sub-agents are involved, `parent_tool_call_id` correlates child calls to their parent spawn (FR-H-001), so a deeply nested `subagent` → `spawn` → tool chain is reconstructable.

The entry-level `parent_spawn_call_id` is different: it is private archive provenance for internal readers, **not a public `Message` property**. Paged REST messages and session detail omit that key, including for older archives that retain it. The tagged entry and its public narration still appear; this is field projection, not an entry filter. API clients must not depend on that private key. Contracted event relationships such as `parent_call_id`, `span_id` and `child_session_id` keep their existing meanings; none is an alias for the omitted archive field.

### Cancel context

When a turn is cancelled, a dedicated `turn_canceled` entry is appended carrying `turn_id`, `canceled_by_user`, `canceled_by_channel`, `cancel_method` (graceful or hard), and any `descendants_canceled` (sub-agent IDs that were cascade-cancelled). Plus the assistant entry that got interrupted is marked `truncated: true`. This marker is separate from the cancelled tool's saved terminal outcome: the marker adds no drawn row, while the tool remains one logical tool row. Reload never executes it again. An older archive that never saved the started call/outcome cannot be repaired from the interrupted annotation; no historical tool evidence is fabricated.

### Replay

REST history and WebSocket snapshot replay use the bounded `UnifiedStore.ReadChatHistoryPage` path. The latest page stops at the last exact `/clear` marker, applies input-disposition and correction rules, and excludes private continuation inputs. Earlier segments require explicit paging across the marker. A page does not read the whole transcript before selecting its recent end.

`UnifiedStore.ReadTranscriptView(sessionID)` remains a full current-segment helper for internal callers and tests: its display-oriented `Entries` start at the last `/clear` marker, apply input-disposition labels and exclude private continuation inputs. `UnifiedStore.ReadTranscript(sessionID)` returns the complete merged transcript for internal readers, including saved inputs. These readers are not interchangeable, and neither full reader is the paged API implementation. The archive remains the single on-disk content record.

### Recorded session usage

The optional generated `session_tokens_total` on a `SessionHistoryResponse`, session-scoped `SessionStateFrame`, or live turn's `DoneStats` is the absolute recorded session consumption, with the same provider/cache accounting as `SessionStats.tokens_total`. It is independent of the selected page's size or contents. Existing message usage and `DoneStats.tokens` retain their per-entry/per-turn meanings; `frames_emitted` is replay diagnostics, never a token baseline.

Consumers merge known totals for the addressed session without lowering a newer observation or adding the turn delta again. Repeated pages, overlapping snapshot/live delivery and replay terminators must not double-count usage. A genuine recorded zero is valid; omission means unknown, not zero, and preserves an already-known total. The UI shows **Usage unavailable** until a total is established. View-generation and transport-cursor fences still apply. This uses bounded aggregate metadata, not a hidden lifetime transcript scan. If a historical aggregate is lost, or its recorded total is absent/null, later user messages and provider calls do not establish the missing lifetime baseline. Their per-call/per-turn usage remains recorded, but the absolute total stays unknown across flush and reopen until authoritative accounting is restored; a partial new total is not promoted to a whole-session total.

### Derived paging index and recovery

Paged history keeps content in the canonical archive. Its separate, content-free index stores occurrence locations and correction references, not another copy of messages or tool results. Fresh sessions publish a durable empty baseline before their identity becomes readable. Each accepted archive or discarded-input record is saved before the derived checkpoint advances. A derived write failure does not make an already saved message unsaved. A checkpoint with a missing/null root, or a zero root despite nonempty occurrence coverage, is inconsistent and produces unavailable history—not a successful empty page or snapshot. A genuinely empty, valid checkpoint may use root zero. If initial creation stops after that empty baseline is durable but before identity publication, retrying the same session creation can resume it only after verifying absent identity, empty canonical/disposition state and the intact empty commit footer. This does not adopt historical content or replace a conflicting identity; ambiguous or inconsistent state is refused without rewriting it.

Startup, open and Retry may reconcile **at most 64** combined records after a valid checkpoint: archive records (including private model records), discarded-input records, and confirmed missing rolled partitions. Zero pending records is a no-op. A gap of 65 or more records is not repaired in repeated batches. Within that bound, complete checksummed derived node frames after the published mutation end but without any commit footer are provably unpublished: they can be discarded and rebuilt from canonical facts. The limit counts unique mutations, not the number of tree nodes they wrote. Checksummed committed transactions are reused; checksum damage and foreign commit identity remain refusals rather than permission to trim. Separately, a page read gets at most three total attempts when retention changes its cut (the initial attempt plus two reselects). A third moved cut fails visibly with the existing `index_inconsistent` unavailable-history reason; every discarded attempt remains counted in `retention_retries`. This is a safety bound, not a sizing or performance setting. Missing, incompatible or inconsistent baselines require the separate offline operator repair; ordinary sending does not build a newest-only replacement index.

Positions identify occurrences, not message IDs, timestamps, model positions or live WebSocket sequence numbers. Treat them as opaque strings; do not sort or decode them. A known expired position remains an ordering boundary. Retention keeps only its opaque token and private rank, not its former ID, timestamp, size, content or disk address. This ledger can grow with expired occurrences on disk; deleting the session removes it. Rich expired index fragments are retired when old-cut readers close. A real removal failure remains pending and is surfaced as a storage failure to the releasing operation, rather than logged and forgotten or hidden by a reselect. Every normal read/reconcile retries retirement even without new expiry, including discovering obsolete generations after restart. An already absent file is successful cleanup. Restore the required filesystem access or release a blocking handle before retrying; canonical accepted content is not rewritten to hide the error.

A page captures a short immutable read cut. Content reads then open and close one bounded record handle at a time, outside session and append locks. Later writes are excluded from that cut. If retention changes the retained set during a read, the result is discarded and selected again. On Windows a genuine sharing violation postpones only that present partition's removal; later eligible partitions are still processed, and the next ordinary sweep retries after the handle closes. Other permission or storage failures are not treated as expiry.

### Paged session API

The messages response is a **`SessionHistoryResponse` envelope**, not a bare array. Read `entries` together with `page_info`, `has_earlier`, `has_later`, and their optional `next_before` / `next_after` positions. The old `X-Has-Earlier-Messages` header is not paging authority. Matching server and browser assets must be deployed together; reload old browser tabs after deployment.

| Read | Request example | Meaning |
|---|---|---|
| Latest | `GET /api/v1/sessions/{session_id}/messages` | Default target 40 drawn-row units, ascending results from the recent end, stopping at the latest exact clear marker if reached. |
| Older | `GET /api/v1/sessions/{session_id}/messages?limit=20&before={next_before}` | Nearest predecessors, excluding the supplied position. Use the actual position returned by the preceding response. |
| Newer | `GET /api/v1/sessions/{session_id}/messages?limit=20&after={next_after}` | Nearest successors, excluding the supplied position. This reloads a newer edge dropped while browsing backward. |
| Refresh a loaded interval | `GET /api/v1/sessions/{session_id}/messages?window_start={first_position}&window_end={last_position}&limit=100` | The two bounds are inclusive. Continue backward within this same interval with `before={next_before}` until its own continuation ends; this is not a message-ID lookup. |

For an all-retained-history operation, start with latest and follow `next_before` while `has_earlier` is true. Deliberately continue before reached clear markers when that operation really intends to include pre-clear history. Require progress and valid envelopes on every request; a failed or wrongly shaped response is not an empty page. Do not infer continuation from a full page, a sequence gap or the lexical order of opaque positions. A window-refresh page describes only its requested interval, not the whole conversation.

`limit` is a digits-only integer from 20 through 100; omission means 40. `min_rows` is a digits-only integer from 1 through the effective limit; omission means 20. Duplicates, explicit zero, blank, signed, fractional, exponent and overflowing values are rejected, not clamped. Position syntax, multiplicity and range combinations are also strict. Authentication and addressed-session access precede paging validation. A malformed unknown raw query key or value returns `400` with `invalid history query`; malformed recognized fields retain their field-specific errors. A well-formed unknown or foreign position returns `404`, not a latest-page fallback.

Whole source groups stay together. A finished tool's start/result is one drawn tool row, not two. Cancellation entries remain retained source metadata but add no drawn row; a visible clear divider counts as one. Source-group count and drawn-row count can therefore differ. A group's drawn weight can exceed the requested target. The server's current **4 MiB soft projected-byte target** uses the larger of prepared REST and historical WebSocket encodings, including ownership and relevant context. Once the estimated floor is met, unnecessary additional groups can stop at the budget. The floor and whole groups win: `byte_budget_exceeded` and `budget_override=height_floor|atomic_group` explain an intentional overshoot; `stop_reason` distinguishes byte, row, source-cap, clear, end and range-end stops. The advertised budget is a positive integer, not a fixed-value enum.

`min_rows` is an authenticated caller's advisory height estimate, not proof of viewport geometry or a denial-of-service defense. Setting it equal to a limit of 100 can require 100 large admitted groups despite the soft byte target. This is not a hard response-size or memory ceiling. Clients measure actual drawn height and request contiguous top-ups where needed; they must not split tools to fit bytes.

Session detail remains useful when history is unavailable. An available detail contains `messages`, `page_info` and continuation fields with `history_state.status=available`. An unavailable detail retains authorized `session` / optional `agent_removed` metadata and has `history_state.status=unavailable` with a fixed `reason` / `message`; it **omits** messages and every page/continuation field. Do not interpret it as `messages: []`, deletion or successful history loading. The messages endpoint instead returns the fixed `500` index-unavailable error after missing, inconsistent or over-64 recovery. Storage/projection failures retain the redacted storage-error response. Current authorized live controls can remain usable independently of historical reconstruction. In usage accounting, a per-model cache-hit ratio with no recorded input denominator remains `null` (unknown), not an invented zero; it is not a session-detail history field.

When authoritative usage is available, the response also includes `session_tokens_total`: the absolute session-wide consumed-token total already recorded in `SessionStats.tokens_total`, independent of the selected page. It is not the sum of `entries`, a context-window size or a last-turn delta. Detail exposes the same authority through `session.stats.tokens_total`; current session-scoped WebSocket state and live turn `done` statistics publish the absolute field too, including current state when history is unavailable. Replay-only `done` and unscoped connection state omit it. Missing or unreadable accounting means an omitted field, not a manufactured zero; a genuinely unused session can report its real zero. No lifetime transcript scan is used to reconstruct missing usage. Matching clients must merge absolute observations for the addressed session without lowering a newer total or adding page/replay repeats. Existing `Message.tokens` and live `DoneStats.tokens` keep their per-entry and per-turn meanings; `frames_emitted` is only a replay diagnostic.

The response entries keep `history_position` and their recorded `id`. `next_before` and `next_after` exist exactly when their corresponding continuation flag is true and equal the selected first and last positions. Empty and exactly-full terminal pages have no phantom cursor. URL-encode the returned position when using it as a query value.

`page_info` preserves effective sizing, logical `row_count`, `source_group_count`, mode, positions, clear/context boundaries, retention generation and bounded public dependencies. Source groups and drawn rows are different: a `turn_canceled` metadata entry keeps its occurrence identity but adds no narration row; a visible clear divider is a system row. The browser normalizes payloads but preserves metadata keys. It validates metadata before committing rows or flags: an invalid individual message with a usable position gets a stable visible placeholder, while contradictory page metadata fails the read.

The browser measures actual drawn height and tops up contiguous pages toward four screens on opening and two new screens during prefetch, stopping at a clear boundary, retained end or visible failure. Range refreshes may need more than five physical pages. A shorter byte-limited page is not by itself proof that the screen-height goal was met.

The canonical [OpenAPI contract](https://github.com/elicify-ai/omnipus/blob/main/contracts/openapi.yaml) defines the complete generated shapes. For offline index preparation and restoration, follow [Repairing a saved chat history index](https://github.com/elicify-ai/omnipus/blob/main/docs/operations/chat-history-index.md). This is a separate operator procedure, not an automatic API repair loop.

### Content-free history diagnostics

One structured `message=history_page`, `event=history_page` summary records each authorized page attempt, including rejected input, failure or cancellation. Carriers are `rest_messages`, `rest_detail` and `ws_attach`. A servable journal-only attach performs no page attempt and emits no such summary. `success` means public page preparation completed, not that the socket, browser rendering or user acceptance completed. `duration_ms` stops when preparation and validation finish, before HTTP response delivery or WebSocket history/control delivery can block; it is not an end-to-end delivery timer.

| Field group | Recorded fields |
|---|---|
| Classification / timing | `carrier`, `mode`, `outcome`, `duration_ms`, `soft_byte_budget`; validated `limit` / `min_rows`, and `requested_limit` only after successful explicit parsing. |
| Actual archive work | `selected_archive_records` / `selected_archive_bytes`, `lookahead_archive_records` / `lookahead_archive_bytes`, `dependency_archive_records` / `dependency_archive_bytes`, `recovery_archive_records` / `recovery_archive_bytes`, `discarded_archive_records` / `discarded_archive_bytes`; `archive_records_read` / `archive_bytes_read` are their disjoint sums. |
| Other actual work | `disposition_records_read`, `disposition_bytes_read`, `index_bytes_read`, `dependency_count`, `recovery_items`, `recovery_anchor_records`, `recovery_overflow_records`, `retention_retries`. An overflow probe reports 65 recovery items, not a lifetime recount. |
| Successful pages only | `source_group_count`, `row_count`, `projected_bytes`, `has_earlier`, `has_later`, `byte_budget_exceeded`, `stop_reason`, `retention_generation`; `budget_override` only for an exceeded budget. |
| Failure classification | `abort`, `invalid_request`, `protocol`, `storage`, `index_unavailable`, `projection_error`; failures do not invent an empty successful page. |

These server summaries contain no session, agent or user IDs, bodies, parameters, results, secrets, raw query text, positions, disk addresses, paths or underlying error causes. Byte counters are actual encoded bytes read, including partial and discarded work, not decoded text size. Zero means measured zero work.

The production browser also records a rejected snapshot commit through the existing rate-limited console diagnostic sink: `event=chatHistorySnapshotRejected`, `phase=snapshot_commit`, and a fixed `kind`. Expected kinds are `schema`, `source_origin`, `protocol_metadata`, and `row_count`; an unclassified exception is `unexpected_exception`, not a fabricated protocol error. The record contains no exception message or stack, source body, ID or opaque position. The history area keeps its visible rejection and Retry; a row-count contradiction cannot replace the prior reader. Valid commits do not emit a rejection record. Ordinary development/test builds suppress this production console sink. These browser diagnostics are not server page-attempt summaries or external telemetry.

Allocation measurements are separate, isolated test evidence: `message=history_page_measurement`, `case=short_prefix|long_prefix`, positive `sample_count`, validated `limit` / `min_rows`, and nonnegative `allocated_bytes` per complete real reader-plus-carrier operation. Fixture construction and unrelated activity are outside that measurement. There is no concurrent production per-request heap-delta claim. A missing receipt is unverified, not zero allocation or a pass.

## Live event stream

While a turn is running, the agent loop emits typed events into the in-process event bus (`pkg/agent/events.go`). The event kinds in `pkg/agent/events.go::EventKind` (the names come from `eventKindNames`, which is the complete list):

| Category | Events |
|---|---|
| **Turn lifecycle** | `turn_start`, `turn_end`, `turn_timeout` |
| **LLM lifecycle** | `llm_request`, `llm_delta`, `llm_response`, `llm_retry`, `empty_response_retry`, `provider_retry`, `provider_fallback` |
| **Context management** | `context_compress`, `compaction_retry` |
| **Tool execution** | `tool_exec_start`, `tool_exec_end`, `tool_exec_skipped`, `tool_result_projection` |
| **Sub-turns (sub-agents, spawn)** | `subturn_spawn`, `subturn_end`, `subturn_result_delivered`, `subturn_orphan` |
| **Steering / interrupt** | `steering_injected`, `follow_up_queued`, `interrupt_received` |
| **System / errors** | `error`, `rate_limit`, `background_process_kill` |
| **Channel / user-facing** | `whatsapp_pairing`, `notification` |
| **Tasks, plans and goals** | `task_status_changed`, `plan_status_changed`, `goal_status_changed`, `loop_status_changed`, `task_run_status`, `goal_outcome`, `judge_verdict` |
| **Helpers** | `subagent_message`, `subagent_state` |

### Who consumes the events

#### WebSocket subscribers (SPA chat view)

Every event is fanned out to connected clients. The chat UI shows the live progression: "Tool call: search_web" → "Tool call complete" → "LLM response streaming" → "Turn end". This is what gives the chat its real-time feel.

#### Subprocess hooks

External processes can subscribe to the same event feed via JSON-RPC over stdin/stdout. Every event becomes a `hook.event` notification. Authors observe (read-only), or in-process hooks can intercept and rewrite. See [hooks](../hooks/README.md). The wire format uses the canonical string name of each `EventKind` so authors don't carry private int → name tables (per issue #164).

#### Audit log

A subset of security-relevant events (tool calls, denials, sandbox state changes, cancel events, rate limits) are mirrored into `~/.omnipus/system/audit.jsonl` with HMAC chain integrity (#155 hardening, item 1). See [Security for users](../security.md).

Stop and cancel requests write these audit events:

| Event | Level | When |
|---|---|---|
| `turn.cancel.attempt` | INFO | Every Stop request, including duplicates and requests that find nothing running. `was_fired` says whether it interrupted a turn. One row per request. |
| `turn.cancelled` | INFO | A cancelled turn has fully exited; carries `cancel_method` (`graceful` or `hard`) and the descendants cancelled with it. |
| `turn.cancel.stuck` | WARN | The turn did not exit within 3 seconds of the forced stop and was detached. |
| `turn.cancel.background_killed` | INFO | The cancel killed the session's background jobs (the count may be 0). Written whether or not a turn was running. |
| `cancel.abuse_pattern` | WARN | One person and channel sent 10 or more cancel requests within 60 seconds. |

### Event payload shape

Every event carries:

```go
type Event struct {
    Kind    EventKind                  // canonical string on the wire
    Time    time.Time                  // JSON tag "Time"
    Meta    EventMeta                  // JSON tag "Meta" — see below
    Payload any                        // event-specific fields
}

type EventMeta struct {
    AgentID      string `json:"AgentID"`
    TurnID       string `json:"TurnID"`
    ParentTurnID string `json:"ParentTurnID"`
    SessionKey   string `json:"SessionKey"` // NOT "SessionID"
    Iteration    int    `json:"Iteration"`
    TracePath    string `json:"TracePath"`
    Source       string `json:"Source"`
}
```

Note: the Go struct uses the field name `SessionKey` (not `SessionID`) on the `Meta` substruct. The legacy `SessionID` field on the top-level `Event` struct is no longer present — call sites that read correlation IDs use `Meta.SessionKey`. The wire field is a JSON object (`"Meta": {...}`), not flat top-level keys.

For `tool_exec_start` the payload includes the tool name, arguments preview, and iteration. For `llm_request` the payload includes the model, message count, token budget, and tool list size. Full reference: read the call sites of `eventBus.Publish` in `pkg/agent/loop.go`.

### Buffer sizes

The observer subscription buffer is 1024 events (`pkg/agent/hooks.go::hookObserverBufferSize`, raised from 64 after a sustained-burst load test in #165). A slow consumer that falls behind drops events past the buffer rather than blocking the agent loop (a few critical event kinds get one short bounded wait first). The event bus keeps a drop count per event kind, `EventBus.Dropped(kind)` (`pkg/agent/eventbus.go`), for monitoring.

## Putting it together

A normal turn produces, in order:

1. `turn_start` event → SPA shows the spinner
2. Transcript entry: `type=message, role=user, content="..."` lands on disk
3. `llm_request` → SPA shows model + tool count
4. `llm_response` → SPA shows assistant message streaming in
5. `tool_exec_start` for each tool call → SPA renders tool-call chips
6. Transcript entry: `type=tool_call` per tool, with full params + result
7. `tool_exec_end` per tool
8. Final assistant text → second LLM round → `llm_response`
9. Transcript entry: `type=message, role=assistant, content="..."`
10. `turn_end` → spinner clears

Everything is captured. Nothing is silent. An operator can audit any turn after the fact by reading the JSONL; a developer can subscribe live via a subprocess hook to watch every event as it happens.

## Model-call cache accounting

The existing **Usage** screen shows recorded token totals and cache-hit percentages
by agent and provider. There is no caching settings page. For call-by-call details
from the agent turn loop, use the application log's `llm_usage` line.

### Enable the info line

The shipped application log level is `warn`, so this line is hidden by default.
Set the existing `gateway.log_level` to `info`, or start the gateway with the
existing environment override:

```sh
OMNIPUS_LOG_LEVEL=info omnipus gateway
```

Restart a running service with that environment variable to apply the override.
This changes the application-wide level; there is no separate cache log level.

Each successfully completed model request made through the agent turn loop emits
one info line, including requests that report no usage or zero cache tokens. This
is not a log of every provider request made by Omnipus: auxiliary calls such as
automatic recaps, fallback goal compilation and audio-model transcription run
outside this accounting path. A provider error remains an error; it is not
converted into a successful, zero-token call. Within the turn loop, if a failed request already
reported billed usage, those tokens count once against that request's provider,
even when another provider answers successfully afterward. A failed streamed
request keeps the input and cache counts already received, plus the latest reported
output count. Missing usage is not guessed. These failed attempts do not emit a
successful-call `llm_usage` line.

For Anthropic-protocol HTTP errors, the connector retains at most 8 KiB of the
response for error classification and uses at most the first 512 bytes, followed
by an ellipsis when needed, in the error preview. This is a size limit, not prompt
redaction: a provider can still echo text within that preview. Retry timing and
request identifiers from the response headers remain available to the retry path.

An example successful-call accounting message is:

```text
llm_usage provider="anthropic" model="claude-sonnet-4-5" agent="mia" session="session-example" call="call-example" uncached=1000 cache_read=8000 cache_write=2000 output=500 total=11500 billing=priced cost_usd=$0.0204
```

The amounts above are illustrative: actual prices come from the current catalog,
not a price list maintained inside Omnipus.

| Field | Meaning |
|---|---|
| `provider`, `model`, `agent`, `session`, `call` | Application-written identifiers that correlate the request with its conversation and producer |
| `uncached` | Input tokens not read from or written into the provider cache |
| `cache_read` | Input tokens reused from the provider cache |
| `cache_write` | Input tokens written into a cache on this request |
| `output`, `total` | Output tokens, and the sum of all four token portions |
| `write_5m`, `write_1h` | Separate write counts when the provider reports the lifetime split |
| `billing` | `priced`, `unpriced`, or `subscription` |
| `cost_usd` | Catalog-priced portions only; absent for subscription and fully unpriced calls |
| `unpriced_tokens` | The portion that has no price; present when positive |
| `one_hour_rule` | `cache_write_1h`, `2x catalog input`, or `unpriced`, when a one-hour write needs that rule |

The line contains only normalized numeric counters and application-written
identifiers. Unknown usage fields, strings, arrays and objects from the provider
are excluded. Fewer than 32 counters are emitted. Prompt text, tool arguments,
provider error bodies and credentials are not part of this line. The existing
credential scrubber still runs before the application log is written.

Recognized token counters must be non-negative integers within the supported
integer range. Invalid individual values are ignored. Omnipus also checks the
combined lifetime-write count and the full token total: if either sum cannot be
represented, it rejects that usage response rather than storing wrapped or
negative totals. This is input validation, not a token or spending limit.

### Read the hit percentage and billing classification

```text
cache-hit ratio = cache_read / (uncached + cache_read + cache_write)
```

A ratio of `0.8` is **80%**. When the denominator is zero there is no percentage;
that is not a measured 0% hit rate. Output tokens are not in this denominator.
The existing `GET /api/v1/stats/tokens` route exposes `cache_hit_ratio`,
`by_provider`, `unpriced_tokens`, and `subscription_tokens`. Cached totals on the
Usage screen include both reads and writes; the hit percentage uses reads only.

These figures use Omnipus's normalized usage counters. Where a provider has not
documented whether cached tokens are already included in its reported prompt
count, Omnipus preserves that prompt count rather than guessing a subtraction
rule. This currently applies to Google's optional cache counts. Treat those rows
as normalized accounting figures, not a provider-confirmed reuse percentage.

| Classification | Interpretation |
|---|---|
| `priced` | At least part of the call is priced from the catalog. Published zero prices remain real zero prices. A missing ordinary cache price uses that band's input price. |
| `unpriced` | The catalog has no usable input price, or none of the call's consumed portions can be priced. Tokens still count. No old fallback dollar rate is substituted. |
| `subscription` | A ChatGPT sign-in call counts cached and uncached tokens but records no API dollar cost, even when its model has a catalog API price. |

A call can be mixed: for example, a provider with a priced five-minute cache
write but no published one-hour write price and no documented one-hour multiplier
records the five-minute cost, stays `billing=priced`, and separately reports the
one-hour tokens as `unpriced_tokens`. It does not discard the known cost or call
the unpriced portion free. Native Anthropic's documented one-hour multiplier is
2 times catalog input when the catalog does not supply `cache_write_1h`.

### Request layout

Caching does not change who supplied a message. Current-sender, session and
runtime metadata remain in the system context—the request's instruction prefix.
Only the changing clock, tool-summary, scratchpad and goal-checklist notes follow
the conversation in one user-role text block. Skill-use guidance remains in the
stable instructions and Skills menu; no per-request skill reminder is added.
When a turn is assembled, its current user message retains its caption and
attachments immediately before that block. Tool-call follow-ups keep complete
call/result groups ahead of the notes. Removing old history from the live window
does not remove the original system instructions.

On thinking-enabled Anthropic tool turns, the original signed thinking and
redacted-thinking blocks are retained in private model history and replayed
before the tool call on the continuation request. Their signatures and redacted
data are not rewritten or included in the accounting log. A synthesized answer
assembled from several truncated replies does not inherit one reply's signature.

### Stored accounting for each call

Each successfully completed model call made through the agent turn loop has its
own assistant accounting entry in that session's transcript. A failed turn-loop
attempt also has an accounting entry when the provider reported billed usage.
These entries store the provider and model identifiers, billing classification,
token portions, and the five-minute and one-hour write counts when reported. A
call that returns no visible text can have an entry with empty content; that entry
does not create an extra chat bubble. The auxiliary calls listed above are not
included in this accounting path.

A turn can contain several accounted calls. Its summary totals are not an
additional charge, and a later engine-generated notice does not count those
tokens again. Each accounted call is priced once at its provider boundary.
Reloading a session uses the stored cost and classification; it does not reprice
earlier calls from a newer catalog.

A successful request with zero cache reads is an ordinary miss. For a live cache
proof, however, send the same conversation twice: the second request must report
more than zero cache-read tokens. HTTP success alone is not proof of caching.

## See also

[Hooks](../hooks/README.md) explains how to write a subprocess hook that consumes the event stream.

[Security for users](../security.md) covers the audit log and sensitive-value filtering.

[Memory](../memory.md) describes how transcripts become memory via auto-recap at session close.
