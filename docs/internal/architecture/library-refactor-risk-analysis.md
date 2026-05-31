# Library Refactor — Risk & Regression Analysis

**Status:** Historical companion to `library-refactor-impact-assessment.md`. The original multi-edition framing (Community / Desktop / Enterprise) is no longer the plan — Omnipus today ships as a single open-source Go binary. References to "Desktop" or "Enterprise" below refer to hypothetical second consumers the original analysis assumed. The risks called out around code hygiene (`os.Exit` in the logger, hand-maintained `src/lib/api.ts` drift) still apply independently.

**Audience:** Product owner, engineering lead, QA — anyone who needs to know what could break
**Written in:** Plain English, not architect-ese
**Base commit:** `origin/main@da790c5`

> The impact assessment explained *what* needs to change. This document explains *what could break* while changing it, and how bad each break would be.

---

## The honest short answer

**If the refactor is done carefully — 8 small PRs instead of 1 big-bang PR — the risk to existing features is low-to-moderate.** Most of the work is *moving code around*, not changing how things behave.

The danger is concentrated in **two or three specific PRs**, not spread evenly across the whole refactor. If you know where to be careful, most of the code is safe.

---

## Risk by feature area

### 🟢 Low risk

**Channels (Slack, Telegram, Discord, Signal, and the other 12).**
These already sit behind a clean interface. They're built from a factory, they receive config and secrets explicitly, they talk to the rest of the system only through the message bus. Moving the code around them doesn't change their behaviour.
*One small gotcha:* moving the channel `_ "..."` imports out of `pkg/gateway/gateway.go:31–45` into `cmd/omnipus/` will silently break the build if any channel package quietly relies on being imported from gateway. I don't expect any to — but I'd find out on day one if they did. Hours to fix, not days.

**The agent loop, LLM routing, tools, skills.**
All already DI-shaped. They accept their dependencies as constructor arguments today. The refactor doesn't change that; it just means the constructor call site moves from `pkg/gateway` to `cmd/omnipus`.

**Sessions, task store, state.**
Self-contained subsystems. Nothing in the refactor touches their internals.

**Adding `pkg/ext` interfaces around existing packages.**
This is additive — the concrete types stay, interfaces wrap them. Community keeps using the concrete defaults; only a future Enterprise implementation would swap. Zero behaviour change for current users.

---

### 🟡 Medium risk

**Startup sequence and shutdown — the single most dangerous area.**
`pkg/gateway/gateway.go:231` (`Run()`) currently owns, in a very specific order:

1. Credential unlock
2. Config load
3. Provider setup with env injection
4. Agent loop construction
5. HTTP server start
6. Signal handling
7. Banner + status output
8. File logger setup
9. Reload loop

When you pull these apart into library vs. app code, it's easy to get the **order wrong**. Classic symptoms:

- Credentials unlock before config is read → secrets resolve against an empty config → silent failure
- HTTP server starts before the agent loop is ready → first request hits a nil pointer
- Signal handler registered before the thing it's supposed to shut down exists → Ctrl+C does nothing

**Why this is real, not theoretical:** the existing file is 1,186 lines because every line got added in an order that works. Moving them around without careful testing will re-introduce bugs someone already fixed, possibly months ago.

**Mitigation:** the startup extraction PR needs **end-to-end tests** running against it. Start the gateway, log in, send a message, receive a reply, shut down cleanly. If those pass, you're probably fine.

---

**Logging behaviour — subtle, one-line change, widespread effect.**
Removing `os.Exit(1)` from `pkg/logger/logger.go:274` changes one thing: today, logging at FATAL level kills the process immediately. After the change, the process logs FATAL and keeps running.

**The risk:** some caller somewhere may be relying on FATAL-means-die. If it is, removing the exit means the process limps along in a broken state instead of dying cleanly — which is harder to debug than dying cleanly.

**Mitigation:** grep every `.Fatal(` and `FATAL` call site (there are likely a handful). For each, ask: does this really mean "process must die right now"? If yes, the call site in `cmd/omnipus` should explicitly call `os.Exit` — libraries aren't allowed to, but apps are. If no, leave it logging and carrying on.

---

**Regenerating the API client from OpenAPI — highest surface area.**
`src/lib/api.ts` is 1,075 lines of handwritten fetch wrappers. Every UI feature uses it. Replacing it with a generated-from-OpenAPI version means that every UI feature now depends on a slightly different client.

**The risk:** subtle differences you don't notice until a user does.
- Header shape differs → auth fails on some endpoints
- Error parsing differs → errors become silent or become loud in the wrong places
- Null vs. undefined handling differs → forms submit blank fields instead of not submitting them
- Timeout behaviour differs → slow endpoints time out in the UI but succeed on the server

These regressions scatter across the whole UI, which makes them hard to find with a targeted test.

**Mitigation (important — don't skip):**
- Do this PR **after** the gateway extraction, not during. Don't stack risks.
- Keep the handwritten file present during the transition. Feature-flag which client is used so you can roll back in production.
- Run a full UI smoke test manually: log in, chat, configure a channel, change a setting, log out. If any of those feel different, investigate before merging.

Honestly, this is the PR I would be most conservative with. It's the one that can break things no test notices.

---

**Config reload — fragile today, needs to stay fragile-but-working.**
The current reload loop inside `gateway.Run()` is intertwined with the gateway's own lifecycle. When the library extraction happens, the reload has to either stay inside the library (so that changes of config, provider, channels still work) or become a contract that the application layer honours. Getting this wrong means the next time you change a setting in the UI, it either doesn't take effect or takes effect in a way that breaks the session.

**Mitigation:** treat "change a config value in the UI and see it applied without restart" as an acceptance test for the extraction PR. If it still works, the reload survived.

---

**UI feature extraction.**
Peeling a feature (like sessions) out of `src/` into `@omnipus/feature-sessions` is mostly a move-and-rename operation. Low risk *if* the feature is self-contained.

**The risk:** a feature you assumed was self-contained turns out to reach into shared state or context. When you extract it, it breaks — or worse, appears to work but behaves subtly differently.

**Mitigation:** start with the most self-contained feature (sessions), not the most important one (chat). Chat probably has shared state with agents, messages, and streaming — save it for last.

---

### 🔴 Higher risk (but small scope)

**Turning nil-constructor panics into returned errors.**
Four places today (`pkg/policy/auditor.go:39`, `pkg/agent/audit_bridge.go:26`, `pkg/providers/fallback.go:43`, `pkg/gateway/gateway.go:282`) panic immediately if they're constructed with a nil dependency. The refactor wants them to return an error instead.

Sounds mechanical. It isn't.

**The risk:** if you return an error instead of panicking, every caller now has to handle it. Miss one caller and you've moved the panic from the constructor (which fails loudly and immediately) to some arbitrary later point when the nil field is first touched (which fails quietly, maybe minutes or hours later, in a totally unrelated-looking part of the code).

**Mitigation:** do these one at a time, not in a batch. For each panic you remove, trace every call site of that constructor. Confirm the new error is handled. Don't just grep for the function name — use the compiler, because if the return signature changed, every caller fails to compile until handled. That's your safety net.

---

## Risk by user-visible feature

A quick table for the product owner:

| Feature | Risk | What would break it |
|---|---|---|
| Chat / sending messages | **Medium** | API client regeneration |
| Streaming responses | **Medium** | SSE / WebSocket paths inside the gateway could be disturbed by extraction |
| Channel integrations (all 15) | **Low** | Each channel is self-contained behind a clean interface |
| Login / authentication | **Medium** | Touches `pkg/auth` (env var reads) and the UI client regen |
| Skills and tools | **Low** | Already interface-driven |
| Config changes / hot reload | **Medium–high** | Reload loop is tangled into gateway startup |
| Credential unlock on boot | **Medium** | Boot order is fragile; order changes bite here first |
| Graceful shutdown (Ctrl+C, SIGTERM) | **Medium** | Signal handling is moving out of the library |
| UI responsiveness and layout | **Low–medium** | Depends on how clean each feature extraction is |
| Observability / log output | **Low** | Log format doesn't change; only FATAL-means-exit behaviour changes |

---

## Risks that aren't about code breaking

Two risks the strategy doc doesn't address, which matter more than people think.

### Development velocity during the refactor

You have **four active feature branches** today (`channel-signal`, `channel-google-chat`, `channel-teams`, `sprint-c-test-first`). Every one of them touches code in or near the refactor zone.

**If the refactor lands as one big PR**, every one of those branches rebases through hell. Channel PRs re-do their blank-import wiring. Security PRs hunt for where their middleware registration moved to. Tests break in ways unrelated to the feature the branch was supposed to add.

**If the refactor lands as 8 small PRs** (as recommended in the impact assessment), each is separately mergeable and the feature branches rebase cleanly most of the time.

But the 8-small-PRs approach is **slower in wall-clock time** than the one-big-PR approach. Probably 30–40% longer. That's the trade-off. You buy lower regression risk with calendar weeks.

### Risks that won't show up in any test suite

Some behaviours only show up in production, under real conditions:

- **Credential rotation under load.** If you're rotating a Slack token *while* the gateway is reloading config, and the refactor changed the order of credential unlock vs config reload, you'll discover the race condition the hard way — when a customer reports their bot went silent.
- **Long agent sessions across a config reload.** Does an in-flight agent survive a reload? The current code does. Any refactor that touches the reload path needs to preserve this behaviour, but no unit test proves it.
- **OAuth flows.** `pkg/auth/oauth.go` reads env vars directly today. If the refactor moves this to config-based reading, users whose environment setup relied on a specific load order might see failures.

**Mitigation:** these get tested by running the refactored build against a staging instance for a week before release. No shortcut available.

---

## The biggest risk of all: not doing it

This is worth saying plainly.

The current code has **two real library-hygiene bugs** that exist today, regardless of whether any second consumer ever appears:

1. **`os.Exit(1)` in the logger** means any wrapper around Omnipus — test harnesses, hypothetical third-party embedders, future tooling — gets killed by a single FATAL log call, possibly from a subsystem that shouldn't have that authority.
2. **Hand-maintained `src/lib/api.ts`** is drifting from the Go backend today, silently, before a second UI even exists. You just don't have visibility into it yet because the UI and the backend ship as one binary, so small mismatches usually get fixed in the same PR. The moment they ship separately, drift becomes a user-facing bug.

Deferring the refactor is not zero-risk. It's just **moving the risk to a place where you can't measure it**. The handwritten API client keeps growing. Every caller relying on `os.Exit`-on-FATAL becomes harder to unwind later.

---

## What I recommend to reduce risk

1. **Do the three no-regret items first**, in this order, each as its own small PR:
   - Add a CLA mechanism. No code risk at all.
   - Remove `os.Exit(1)` from the logger. Medium risk, small scope, worth doing alone.
   - Write the OpenAPI spec and add a CI contract test (but don't regenerate `src/lib/api.ts` yet). Low risk, high value.

2. **Only then, decide whether to proceed with the full refactor.** If the strategic case for a second consumer is weak, stop here. You've already improved the code.

3. **If you do proceed, follow the 8-PR sequence** from the impact assessment. Never bundle. Never do the startup extraction and the API regen in the same PR. Never do two medium-risk items together.

4. **Treat the startup extraction (PR 3) as the riskiest PR.** Give it manual end-to-end testing, not just unit tests. If it passes a full "start → log in → chat → configure channel → shut down" flow, it's probably fine.

5. **Treat the API regen (PR 5) as the second-riskiest PR.** Keep the handwritten client available as a feature-flag fallback for one release. Don't delete it on day one.

6. **Don't freeze feature branches.** The refactor has to land incrementally alongside active work on channels, security, and agents. If it can't, it's too big — break it down more.

---

## One-sentence summary

The refactor is genuinely risky in exactly three places — **gateway startup, API client regeneration, and turning panics into errors** — and genuinely safe everywhere else, which is why the discipline of small, separately-merged PRs matters more than the speed of any individual change.

---

*End of risk analysis.*
