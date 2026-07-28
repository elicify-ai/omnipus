# UAT Report — release/v0.1.1 on a real Fly deployment (2026-07-27)

**Build under test:** `release/v0.1.1` @ `0d2962f9` (full CI green: 10/10 non-e2e gates + 6/6 e2e shards)
**Artifact:** `docker/Dockerfile.heavy` — the shipped image, built by Fly's remote builder
**Target:** `https://uat-omnipus.fly.dev` — Fly machine, `shared-cpu-2x`, 2 GB, region `fra`, 3 GB volume
**Method:** 9 subagents impersonating human testers, driving real browsers (Playwright), against the
deployed container. **No local gateway was tested.**

---

## 0. Findings that blocked the UAT itself

These were hit *before* any tester ran. Both are invisible to local development and to CI, and both
are defects in the shipped artifact rather than the test environment.

### B1 — BLOCKER: the Docker images could not build from `release/v0.1.1`

`docker/Dockerfile`, `Dockerfile.heavy` and `Dockerfile.full` pinned `golang:1.26.3` / `1.26.0`
while `go.mod` requires `go 1.26.5`. With `GOTOOLCHAIN=local` the build hard-fails:

```
go: go.mod requires go >= 1.26.5 (running go 1.26.3; GOTOOLCHAIN=local)
ERROR: process "/bin/sh -c go mod download" did not complete successfully: exit code: 1
```

**Why CI never caught it:** `deploy/ci-worker/Dockerfile` uses the *floating* `golang:1.26` tag, so
the green gate compiles with a different Go than the artifact a user installs.

**Fixed here:** all three bumped to `golang:1.26.5-alpine`. Proven by the image building and deploying.

### B2 — BLOCKER: the built-in browser is dead on arrival in the shipped image

Three individually-correct decisions combine into a non-functional flagship feature:

| Layer | Correct in isolation |
|---|---|
| `Dockerfile.heavy` installs Chromium on `$PATH` | ✓ installed *specifically* so the browser works |
| `TrustPathChrome: false` (`pkg/config/defaults.go:470`, SEC-ADR052-002) | ✓ defensible security default |
| `exec_resolver.go` honours that default | ✓ working as designed |

Server-side evidence from the deployed gateway:

```
WARN-BROWSER-007: system Chrome on $PATH ignored — operator must set tools.browser.trust_path_chrome
chromium resolution failed — negative-caching for the TTL
Tool execution failed: browser_navigate: browser: shared Chrome unavailable
```

The user-visible result is the agent reporting "the headless Chromium binary isn't available in this
environment" — which is false; Chromium 149 was verified present at `/usr/bin/chromium-browser`.
`Dockerfile.heavy`'s own comment ("the PATH lookup already resolves; no symlink needed") is stale
relative to the security default.

Residual, and legitimate: `WARN-BROWSER-003` — no full-Chrome build, so the live panel falls back to
JPEG frames instead of WebRTC video.

**Getting this feature working took three separate fixes.** Each is its own defect; only the first
was visible before deployment.

**B2a — the default refuses the image's own Chromium.** As above. Needs a real fix in the artifact
(seed `trust_path_chrome` in the heavy image, or have the resolver trust a Chromium the image itself
installed), not an operator workaround.

**B2b — persisted `config.json` silently overrides the documented env var.** Setting
`OMNIPUS_TOOLS_BROWSER_TRUST_PATH_CHROME=true` (the binding named in `pkg/config/config.go:2927`)
had **no effect**, because `config.json` on the mounted volume already carried
`"trust_path_chrome": false` and file config wins. There is no warning that an explicitly-set env
var was ignored. Verified on the live machine:

```
ENV=true
config.json:  trust_path_chrome": false     <- this is what the gateway used
```

An operator configuring this in `fly.toml`, compose, or a k8s manifest gets silence and a still-dead
feature. This is also what fooled the lead of this UAT: checking that the variable was *set* is not
the same as checking it was *effective*.

**B2c — negative caching outlives the fix, and the agent then reports a false cause.** The resolver
logs `chromium resolution failed — negative-caching for the TTL to avoid re-probing`. After the
underlying cause is fixed, the feature stays dead until the TTL expires or the process restarts —
and in the meantime the agent narrates:

> "The managed Chromium binary at `/home/omnipus/.omnipus/browser/chromium/151.0.7922.47/chrome-linux64/chrome`
> does not exist — no such file or directory. The install appears to be corrupt or incomplete."

That statement is **false**. `find` on the live machine shows the binary present with a complete
install alongside it. An operator debugging this in production is sent hunting for a missing file
that is not missing. A cached failure must not be reported as a fresh filesystem fact.

**Resolution used for UAT:** patched `trust_path_chrome` to `true` in the persisted `config.json`
*and* restarted the machine to clear the negative cache. `omnipus doctor` then reported only
WARN-BROWSER-003.

---

## 1. Release-blocking defects

### D1 — CRITICAL: god-mode can be armed but never disarmed through its own UI

The most dangerous control in the product is a one-way door.

**Root cause** (`src/components/settings/GodModeControl.tsx`): the component distinguishes
`enabled` (active in this process, line 59) from `available` (this boot was already authorized,
line 60). `requestToggle()` computes the next state as `!enabled` (line 115). While god-mode is
*authorized but pending restart*, `enabled` is still `false` — so clicking the toggle again computes
`!false = true` and re-sends `{"enabled":true}`. There is no UI path to `false` from that state.

The same split explains the contradictory display: `aria-checked={enabled}` (line 199) shows "off"
while the banner beneath reads "authorized". Both are correct about different fields.

A two-position switch cannot represent three states (off / authorized-pending / active).

**Observed impact:** the tester could only restore the shared instance by replaying the API call
manually with `enabled:false` — recourse an ordinary user does not have. They also *found* the
instance already armed by a previous tester.

**Compounding:** Settings → Security reported **Security Score 100/100, "no issues found"** while
god-mode sat authorized-pending-restart.

### D2 — MAJOR: a single transient 401 hard-logs-out the user, losing in-flight work

Console evidence: `[auth] Session validation failed (401) — redirecting to login` fires on **one**
401, with no retry or backoff. There is no "session expired" message; the app simply lands on the
login screen, and a half-typed form is silently discarded (one tester lost a task mid-creation).

Reported independently by **4 of 9 testers**.

Separately confirmed: a second login for the same user invalidates the first session
(A: 200 → B logs in → A: 401). Whether single-session-per-user is intended policy is a product
question; the **silence** is the defect either way.

*Caveat on attribution:* all 9 testers shared one account, which amplified eviction far beyond
normal use. That amplification is an artifact of this UAT's design, not of the product. The
client-side "one 401 = hard logout, no retry, no message" behaviour is a genuine defect regardless.

### D3 — MAJOR: read-only core agents are written to on view

Opening a locked core agent (Mia) fires an unsolicited `PUT /api/v1/agents/mia` ~1.5 s after the
`GET`, with her unchanged config, and shows "✓ Saved just now" to a user who touched nothing.

**Root cause** (`src/components/agents/AgentProfile.tsx`): the autosave `saveFn` guards on
`hasHydrated`, `agentId`, `conflictRef` and CLI validation — but **not** on `agent.locked`.
Hydration itself writes form state, `useAutoSave` sees `formData` change, and the debounce fires.

**Secondary:** line 430 rejects hydration when `incomingTime <= incorporatedTime` and logs
`console.error`. Equal timestamps mean "same snapshot, nothing to do" — benign — so every profile
open emits an error. Also observed on *custom* agents after a legitimate save, so the guard is
miscalibrated generally, not Mia-specific.

Reproduced independently by 2 testers with full network capture.

### D4 — HIGH: chat conversations do not restore on reload

Navigating to a workspace's `/chat` route after a reload lands on a blank "Welcome / Select agent"
screen even when populated sessions exist — the real thread is only reachable via a collapsed
per-workspace session list in the sidebar. One tester lost an in-flight conversation entirely after
a mid-response reload; another found a workspace's onboarding greeting gone after reload (reproduced
twice).

Reported by **3 of 9 testers**. Reads as data loss even when the data survives.

---

## 2. Other defects

| # | Severity | Finding |
|---|---|---|
| D5 | HIGH | Raw internal strings rendered as user-facing errors: `first message must be {"type":"auth","token":"..."}` and `browser_control: attach before requesting control` |
| D6 | MEDIUM | Live-browser panel shows the wrong agent's identity chip (Mia while chatting with Ray, in a workspace Mia isn't on) — suggests a global singleton session, not workspace/agent-scoped |
| D7 | MEDIUM | Team graph: adding an agent without drawing an edge silently discards it on reload (warned only in a transient banner) |
| D8 | MEDIUM | A stale `aria-hidden` overlay swallows the first click on Settings after navigating from the user menu |
| D9 | MEDIUM | Inconsistent error presentation for the same auth failure: bare unstyled full-screen text (Chat), chromed inline error with Retry (Graph/Team), or a silent bounce to login (Board/List/Calendar/Media) |
| D10 | LOW | `#/nonexistent` renders a blank void with no nav, unlike the graceful "Workspace not found" used elsewhere |
| D11 | LOW | Agent claimed it wrote `research-brief.md` to the workspace; Media tab shows no files |
| D12 | LOW | `Mcp` / `Tool_discovery` break the Title Case convention used by every other tool category |
| D13 | LOW | Feishu, DingTalk, WeCom, IRC show 2-letter monograms while 9 other channels have real icons |
| D14 | LOW | SearXNG row has no status badge or action button, unlike every other search provider |

**UX issues** (multi-tester where noted): the Agents list buries the built-in roster below an
unrelated "Sub-Agent Workers" section (2 testers independently misled); the model picker lists ~300
raw slugs with **no tool-capability indicator**, despite non-tool models breaking chat (2 testers) —
onboarding's `z-ai/glm-5.2` default currently papers over this; Settings autosave feedback is
inconsistent (Gateway shows "✓ Saved", Chat shows nothing); no true quick-capture for tasks; no
subtask hierarchy (only a flat checklist).

---

## 3. What passed — verified, not assumed

- **Cancel mid-stream.** Deliberately abused: transcript byte-identical at +0 s and +15 s, no
  resurrection, no stuck spinner, honest "(interrupted)" labelling. Independent production
  confirmation of the same-day `asyncCallback` post-cancel fix.
- **Secret handling.** Verified by response-body capture, not visual inspection: the Telegram token
  returns `"[configured]"` on save *and* on every subsequent read; the mailbox password field is
  **omitted entirely** from the response (`"configured": true` instead). No fake credential was ever
  echoed back, in two independent subsystems.
- **Delegation is real.** Evidence: a hand-drawn Jim→Ray trust edge persisted server-side; a Board
  task `Created by: jim`, `Agent: Ray — Scout`; Ray's own run history showing independent tool calls
  and an honest `TASK_STATUS: failure` rather than fabricated research; a `Delegate task` call
  measured at **69.8 s**; and a verbose-on/off diff of the *same* session proving the SubagentBlock
  card renders only under Verbose chat.
- **Input robustness.** `<script>alert(1)</script>` and SQL-ish payloads stored and rendered inert;
  double-submit produced exactly one task; empty/whitespace names rejected; malformed, oversized and
  SQL-ish workspace IDs all degraded to a graceful "Workspace not found".
- **No raw cron anywhere in the Calendar UI** — dropdown, custom recurrence builder, and event
  details all clean, satisfying the product's own hard rule.
- **No emoji in UI chrome** — confirmed by all 9 testers across every screen.
- **Create & Run is genuinely agentic** — visible `Recall memory` → `Search the web` → `Fetch URL`
  tool calls producing a real answer in under a minute.

---

## 4. Readiness

| Tester | Focus | Score | Ship? |
|---|---|---|---|
| Dana | Onboarding, first run | 4 | yes |
| Lee | Agents & roster | 3 | no |
| Yuki | Connectors & secrets | 3 | conditional |
| Priya | Tasks, board, calendar | 3 | no |
| Sam | Delegation | 3 | no |
| Marcus | Workspace IA | 2 | no |
| Alex | Edge cases | 2 | no |
| Robin | Settings & god-mode | 2 | no |
| Casey | Built-in browser | 3 (was 1 pre-fix) | no |

### Built-in browser — the operator's headline requirement

After B2a/B2b/B2c were resolved, **all four requested sub-tests pass**, evidenced:

| Sub-test | Verdict | Evidence |
|---|---|---|
| Side panel | **works** | Real Wikipedia pages rendered across 4 browsing tasks; update-over-time proven by two independent before/after screenshot pairs showing genuinely different content. JPEG-frame cadence (~5–10 s, matching tool-call rate) — honest fallback per WARN-BROWSER-003, reads as live |
| Fullscreen / pop-out | **works**, stream survived **both** transitions | Pop-out tab correctly scoped to the live agent, content updated 6 s apart (Order-taxonomy → Blue_whale); closing it left the main panel showing the same live content |
| Annotation | **works**, coordinates **pixel-accurate** | Rectangle dragged (1035,440)–(1250,470) landed exactly around the "Blue whale" heading — no offset, no scale error. Crop reaches the agent; correctly gated while the agent is busy |
| Take the wheel | **works** | "Take over" interrupted the agent's turn and switched to an explicit "You're driving" state; a manual click navigated the **real** page to a genuine hyperlink target — proof input lands on the actual page, not a decorative surface |

Remaining browser defects (polish on a working foundation, not blockers):

| # | Severity | Finding |
|---|---|---|
| D15 | MEDIUM | Reconnecting after a page reload attaches to a **stale, wrongly-scoped global session** (showed "Mia — Assistant"/`about:blank` while chatting with Ray) instead of the current agent's live one. Correct only within one continuous session — the live-browser attach point behaves as a single global resource, not per workspace/agent |
| D16 | MEDIUM | **Every sent annotation renders as a broken image** in the user's own chat transcript ("annotation.png / IMAGE UNAVAILABLE" + a red toast), regardless of whether the model accepted it — the user can never see what they sent |
| D17 | LOW | Live-frame render size is inconsistent between sessions (fills the panel, or shrinks to a ~320×155 letterboxed rectangle in the corner) with no visible trigger — a user annotating the small state can miss the content entirely |
| D18 | LOW | No **proactive** warning that the active model lacks vision before sending an annotation; the limitation surfaces only after the round-trip, via the agent's own narration (graceful, not silent — but not pre-emptive) |

**Verdict: NO-GO for release as-is.**

The pattern across every low score is consistent and worth stating plainly: **the features are
repeatedly described as premium; the floor underneath them is not.** Testers independently praised
the tool-policy editor ("best-in-class"), the Calendar recurrence editor ("on par with
Linear/Asana"), the Team delegation graph, the connector secret handling, and keyboard-accessible
drag-and-drop. Every score below 4 was driven by auth/session instability, the god-mode defect, or
the browser being dead — not by feature quality.

**Minimum bar to ship:** B1, B2, D1, D2, D3, D4.

---

## 5. Method notes and honest limitations

- **Parallelism.** Six SPA personas ran concurrently, each with an isolated browser context. The
  project's own verified rule was followed: a single Playwright MCP server cannot be shared by
  parallel agents (one persistent profile → one browser). Isolated MCP servers were registered but
  bind only at session start, so the documented fallback (P3, Playwright library per subagent) was
  used for the fan-out, with MCP for lead smoke checks. Every report records its `driver`.
- **Browser groups were serialized** — the agent's live Chromium is a single process on fixed debug
  port 9223, so concurrent browser UATs would collide.
- **Shared account was a design flaw of this UAT.** All testers logged in as `dana`, so logins
  evicted each other continuously and amplified D2 far past normal usage. Findings attributable to
  that amplification are marked. The underlying client defect is real; its *frequency* here is not
  representative.
- **Not covered:** channel connectors beyond Telegram; live channel delivery; MCP server addition;
  mobile/responsive; multi-hop delegation depth; Remote Access settings; ActivityBar (one tester
  could not locate it — reported as unverified, not absent).
