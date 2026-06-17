# ADR-020 — Synchronous In-Loop LLM Call for Switch-Time Summarization

**Status:** Accepted
**Date:** 2026-06-17
**Deciders:** backend-lead, architect, security-lead

> **Numbering note:** The Wave 2 dispatch plan referenced this as "ADR-005" but
> that ID is already taken by [ADR-005: Embedded-SPA E2E Test Pipeline & CI
> Gateway Contract](ADR-005-ci-e2e-gateway-contract.md). The next free ID at the
> time of writing is ADR-020.

---

## Context

`AgentLoop.handleModelSwitch` (Wave 3 / FR-011 in
`docs/internal/specs/phase-1-chat-model-and-errors.md`) must compress the
conversation when the user switches to a smaller-context model mid-thread.
The compression strategy has two layers:

1. **Truncation** — drop the oldest turns that don't fit the new window
   (`splitForSwitchCompress`).
2. **Summarization** — call an LLM to produce a prose summary of the
   dropped turns, so the new model retains semantic continuity across the
   gap.

The summarization step is the new bit. Until now, the agent loop's
provider calls were all **user-facing**: a turn boundary came in, the
loop asked the provider for a completion, the completion was streamed
back to the user. Provider calls happened *on behalf of* a turn, and the
loop held no state of its own that an LLM-generated string would
mutate.

`summarizeDroppedTurns` changes the dependency direction. The loop now
calls the provider on its own behalf — to ask the new model "what did
we just talk about, in ≤N tokens?" — and the resulting string is
inserted into the history as a synthetic system message (FR-012) that
the next user-facing turn will see. The next outgoing turn **must** be
the new model, not the old one; this is the "next-turn-uses-new-model
invariant" the picker drives (see Wave 1A / §18 Q2 in the spec).

This is a new pattern. There is no other call site in the loop that
asks the LLM to *compress* history on its own behalf. Three
consequences follow:

- The call has a hard latency budget. A user is mid-session; they expect
  the next message to come from the new model in well under a second of
  added latency, not the 30+ seconds a long-context chat can take.
- The call has a failure mode that the rest of the loop must handle. If
  the provider is down, rate-limited, or returns an empty response, the
  loop cannot simply bubble the error to the user — there's no in-flight
  user request to attach it to. The fallback is the existing
  `forceCompression` (drop the turns, no summary).
- The call has a new failure surface that the audit log must see. The
  replay path (FR-002 / ReplayMessageFrame) needs to be able to render
  a "switch-time summarization failed" event so a replay isn't silently
  missing the gap.

We considered two alternatives (see below) and rejected both. The
synchronous in-loop call is the only design that keeps the
next-turn-uses-new-model invariant.

---

## Decision

`summarizeDroppedTurns` is a **synchronous in-loop LLM call** invoked
from `handleModelSwitch`. The pattern is normative for any future
self-compression case the loop needs (e.g. mid-turn budget exhaustion,
session-level retention compression).

### Contract

```go
// pkg/agent/loop.go
func (al *AgentLoop) summarizeDroppedTurns(
    ctx context.Context,
    agent *AgentInstance,
    dropped []providers.Message,
    newContextWindow int,
) (string, error)
```

- **Caller MUST wrap with a timeout.** The contract requires callers to
  pass a `ctx` derived from `context.WithTimeout(ctx, 15*time.Second)`
  (or another bounded value). The function does not impose its own
  timeout — the caller's deadline is the authoritative one, so future
  call sites can tune the budget for their use case (e.g. a periodic
  background compression could afford 60s; a per-turn hot-path call
  cannot).
- **On error or empty response, the caller MUST fall back to
  `forceCompression`** (the legacy truncation path) and emit a
  `EventKindError` frame to the bus. The fallback log line is at WARN
  level so the audit log captures it. The `EventKindError` payload
  carries the failure reason (provider error / empty response / timeout)
  so the SPA can render the gap distinctly in the replay view.
- **The provider call is read-only w.r.t. the agent.** The summary
  string is returned to the caller; the function does not mutate
  `agent.Model`, `agent.Provider`, or the session's history. Mutation
  happens in the caller (`handleModelSwitch`), which is the layer that
  already owns the switch semantics.
- **The provider is the agent's currently configured one.** We do not
  introduce a separate "summarizer" provider. The summary is cheap
  because credentials are already resolved and the model is already
  hot. Spec direction: Wave 3 / §13 FR-011.

### Failure mode table

| Condition                         | Behavior                                                              |
|-----------------------------------|-----------------------------------------------------------------------|
| `ctx.Err() != nil` (timeout)      | Return `ctx.Err()` wrapped. Caller falls back to `forceCompression`.  |
| `agent.Provider.Chat` error       | Return wrapped error. Caller falls back + `EventKindError`.          |
| Empty / whitespace-only response  | Return synthetic error. Caller falls back + `EventKindError`.        |
| Response > `summaryBudget` tokens | Caller truncates to `summaryBudget` (token cap was injected; the cap is a ceiling, not a guarantee). |
| Successful summary                | Returned verbatim. Caller inserts as synthetic system message (FR-012). |

### Dependency direction

This is a **new edge** in the call graph:

```
agent loop ──► provider
              (previously: only on behalf of a user turn;
               now: also on behalf of the loop itself for self-compression)
```

Future self-compression sites MUST follow the same pattern. The
inverse — a provider package that calls back into the loop to "ask"
about history — is forbidden: providers are stateless w.r.t. session
state and must not import agent-loop types.

### Audit + replay

A failed summarization produces an `EventKindError` frame with a
typed payload (`provider_chat_error` / `empty_summary` /
`timeout`). The replay path (W2-16, in flight) will render this
distinctly so a user replaying a session can see where the gap is
and why there's no prose summary covering it. Without this, a failed
summary would be invisible in the transcript and the user would see
a hard cut in the conversation with no explanation.

---

## Consequences

- **Latency budget.** Every model switch adds up to 15s of synchronous
  provider call. This is bounded and matches the user expectation of
  "next message comes from the new model" — they expect a short pause
  while the model changeover happens, not 0ms. We accept this.
- **Failure surface.** The fallback path is well-defined (`forceCompression`
  + `EventKindError`) and the audit log captures it. We do not silently
  degrade.
- **Future self-compression.** Any future "ask the LLM to summarize
  something" call from the loop MUST follow this pattern: bounded
  timeout at the caller, fallback to a known truncation path, audit
  event on failure. The shape of the function is the template.
- **Test coverage.** A future test (W2-20 / W2-27) MUST cover all four
  rows of the failure-mode table plus the happy path. The pattern is
  load-bearing — failures here mean silent context loss.
- **No new "summarizer" provider.** The agent's current provider is
  the summarizer. This keeps the dependency graph simple and avoids
  a new credential-resolution path.

---

## Alternatives considered

### A. Async LLM call (background goroutine)

Fire off the summarization in a goroutine, return immediately to the
user, and patch the synthetic system message into history when the
goroutine completes.

**Rejected.** Breaks the next-turn-uses-new-model invariant. If the
user sends "ok, continue" before the summarization goroutine returns,
the next user-facing turn goes out to the new model *without* the
summary in its context, and the goroutine eventually patches history
*after* the turn is already in flight. The new model never sees the
gap, and the conversation is incoherent. Async also has no good
failure-handling story: a goroutine that errors has no caller to
fall back to `forceCompression`.

### B. No summary — just truncation

Drop the oldest turns and rely on the new model to "figure it out"
from the remaining context. This is the `forceCompression` fallback
itself.

**Rejected as the primary path.** Loses semantic context across the
gap. A user who switches from a 200k model to a 16k model in the
middle of a debugging session loses the earlier half of the
investigation. The summary is the whole point of the FR-011
feature. (We *do* keep it as the failure-mode fallback — see
the table above.)

### C. Use a separate "summarizer" provider

Introduce a new `pkg/providers/summarizer` package that wraps a
cheap, fast model dedicated to compression.

**Rejected.** Adds a credential-resolution path, a new config slot,
and a new failure mode (what if the summarizer provider is
misconfigured?). The agent's current provider is already loaded
and hot. The added complexity is not worth the marginal latency
savings of using a smaller model for one prompt.

---

## References

- `pkg/agent/loop.go::summarizeDroppedTurns` (the function this ADR
  describes)
- `pkg/agent/loop.go::handleModelSwitch` (the caller that wraps with
  the 15s timeout and the `forceCompression` fallback)
- `docs/internal/specs/phase-1-chat-model-and-errors.md` §13 FR-011
  (switch-time compression), §18 Q2 (next-turn-uses-new-model
  invariant)
- W2-12 (timeout), W2-21 (forceCompression fallback), W2-16 (replay
  error frame) — the implementation work this ADR documents
