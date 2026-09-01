# ADR-072 — Browser tools: workspace-scoped, and usable by an agent

- **Status:** Proposed
- **Date:** 2026-08-31. **Consolidated 2026-09-01** — see "About this revision".
- **Decider for every ruling in this document:** Daniel Piatkowski (operator).
  Recorded once here so the individual "operator ruling" citations below have a
  named authority; a spec cannot resolve its own provenance.
- **Replaces:** an earlier, unpushed draft that also claimed ADR-072
  ("Region-aware transport for the live browser") — deleted in the same commit
  as this file lands. §7 records why that work stopped.
- **Amends:**
  - **[[ADR-043]]** (one shared Chrome + per-agent CDP browser contexts,
    Accepted 2026-07-14) — **D1, D2 and D3**. D1 (one Chrome for the
    coordinator's whole lifetime) is replaced by a browser-process pool; D2's
    isolation primitive is replaced (CDP browser context → Chrome process +
    `--user-data-dir`) and its key moves from the agent to the workspace; D3's
    live-view binding is amended. **D4** (hot-reload survival) is unchanged.
  - **[[ADR-048]]** (live-browser capture requires the DEFAULT browser context,
    Accepted 2026-07-18) — **reverses its ratified v1 choice.** ADR-048 ratified
    Option A (default-context capture, no isolation) and recorded Option B
    (per-agent `--user-data-dir` Chrome instances) as an escape hatch tracked in
    [#509](https://github.com/elicify-ai/omnipus/issues/509). D1.4 below **is**
    Option B, with *workspace* where #509 says *agent*. **#509 was closed
    `NOT_PLANNED` on 2026-08-19** — so this ADR revives a deliberately declined
    option, which is why §9.2 makes reopening it an explicit action rather than
    a footnote. ADR-048's own header must be amended in place too (§9.3).
- **Related:** ADR-038 D6 (live browser panel + take-the-wheel), ADR-041 (tabs),
  ADR-057 (routing vs transcript session split), ADR-061 (JPEG screencast
  removal), ADR-069 (universal live-browser connectivity), ADR-071 (manifest
  tiers).

### About this revision

The direction changed twice during review, and the first two revisions answered
findings by **appending** — leaving the sections those additions falsified in
place. Three rows of the document's own corrections log were still asserted as
true elsewhere in the same file, one of them on line 11.

This revision is a **consolidation**. Every decision is stated once, in its
current form; superseded text is deleted from the body and survives only in §8.
Two previously-open items are decided (whole-Chrome idle close, crash
containment); one acceptance criterion is withdrawn as unreachable (§3.1). §9
names what remains inconsistent downstream, so the conflict is tracked rather
than silent.

---

## 1. Context

### 1.1 What the operator hit

Reported 2026-08-31 on `omnipus-uat-swimlane`, verbatim:

> hey jim check the browser tools and what i have open in the tab
>
> **Jim:** There's nothing open right now — the browser has zero tabs. The
> session is empty.
>
> it is mias tab
>
> **Jim:** The browser session is shared across the workspace — there's no
> separate "Mia's tab." Right now the shared browser is completely empty with
> zero tabs open.

The operator was correct and the agent was wrong, twice: the tab existed, and
the browser is **not** shared across the workspace.

The sequence: the operator opened the chat with Mia (the default agent), opened
the browser panel, browsed — so the tab was recorded against Mia — then switched
the chat to Jim. Nothing moved the tab.

### 1.2 What the code actually does

`AgentLoop.BrowserManagers()` returns a **slice** — one `BrowserManager` per
agent (`pkg/agent/loop.go`). Each manager owns its own `sessions` map
(`pkg/tools/browser/manager.go`), and every browser tool reads from its own
manager under one hardcoded key:

```go
const DefaultSessionID = "default"          // pkg/tools/browser/tools.go
tabs, activeIdx, err := t.mgr.ListTabs(defaultSessionID)   // tabs.go
```

In coordinator mode all managers share **one Chrome** — `allocCtx` is the
coordinator's `rootCtx`, and managers create chromedp child contexts against
that single CDP pipe. So the tabs physically coexist in one browser while each
manager tracks only the ones it created. The live panel confirms the binding in
its own log prefix: `browser-webrtc[mia]`.

Jim was not looking at an empty shared browser. He was looking at his own empty
book, in a library that had the page open on the next desk.

### 1.3 Why he stated it so confidently

Two independent reasons, both in the code rather than in the model.

**The tool descriptions assert the wrong model.** `ListTabsTool.Description()`:

> "List every open tab in **the shared browser session**"

Every browser tool says "shared". The agent reported what its tools told it.

**And the failure is unobservable.** `BrowserManager.ListTabs`:

```go
se, ok := m.sessions[sessionID]
if !ok {
    return nil, 0, nil        // "no browsing context" == "no tabs open"
}
```

"I have no browsing context" and "the browser has no tabs" return the same
value. Nothing downstream can distinguish them, so no agent — and no test — can
notice the difference. This is the `docs/internal/false-green-patterns.md`
shape: a wrong answer that is indistinguishable from a right one.

### 1.4 The existing adoption path does not help

`ReconcileTabs` looks like a discovery mechanism and is not one. It adopts an
untracked target only when that target's **opener is already ours**:

```go
if info.OpenerID == "" {
    continue // not opened by a page — a top-level target, not ours to adopt
}
if _, openerIsOurs := tracked[info.OpenerID]; !openerIsOurs {
    continue // opened by a target outside this browsing context
}
```

That is deliberately scoped to `target="_blank"` pop-ups from an agent's own
tabs. It cannot adopt another agent's tab, and relaxing its `len(se.tabs) == 0`
precondition would not make it able to. Recorded because that precondition looks
like the fix and isn't.

---

## 2. Decision

Two decision areas. **D1** fixes who owns the browser; **D2** fixes what an
agent can do with it. They ship independently and are separated here so each can
be reviewed on its own. (Review round 1 recommended splitting them into two
ADRs. **Operator ruling 2026-08-31: keep as one document.** The split-shipping
property is preserved by the D1/D2 separation itself.)

---

## D1 — Ownership

### D1.1 The decision, in four statements

1. **Signed-in state belongs to the workspace**, not to whichever agent happened
   to be on screen when a tab was opened.
2. **One `BrowserManager` per workspace**, shared by every agent on it, instead
   of one per agent.
3. **The isolation primitive is a Chrome process with its own
   `--user-data-dir`** — one per workspace — not a CDP browser context (D1.4).
4. **Every agent on the workspace shares that browser and its logins, including
   unattended delegated work** (D1.10).

### D1.2 Why the workspace — and why not the agent or the conversation

**Why not the agent.** The human logs in first and *then* decides who to talk
to. §3.1 case 3 is not an edge case; it is the ordinary way a person uses a
shared browser. Keying on the agent makes the browser's contents depend on which
colleague was on screen at the moment a tab was opened — an implementation
detail the operator has no reason to model, and, per §1.1, one the agents
themselves misreport.

**Why not the conversation.** An earlier revision of this ADR keyed on the
conversation. It fixed handover and produced a new surprise: the same agent,
asked the same thing in a new chat, would be logged out, because the login
stayed behind in the old one. A conversation is short and a project produces
many of them.

**The workspace is the unit of related work.** Two agents on one team sharing a
login is correct behaviour, not leakage; a login obtained in workspace X stays
invisible in workspace Y. That is the boundary that actually separates unrelated
work: different clients, different projects.

| | Handover works | No leak between unrelated work | No surprise logout |
|---|---|---|---|
| Per agent (today) | ✗ | ✓ | ✓ |
| Per conversation (earlier draft) | ✓ | ✓ | ✗ |
| **Per workspace (this ADR)** | **✓** | **✓** | **✓** |

Operator ruling, 2026-08-31.

### D1.3 What this changes in ADR-043 and ADR-048

**Isolation is not being moved. It is being turned on.**

On a default install there is no isolation boundary to move:

- `pkg/config/defaults.go:671` seeds **`CaptureSharedContext: true`**.
- In that mode `BrowserCoordinator.Register`
  (`pkg/tools/browser/coordinator.go:349-359`) returns an **empty**
  `browserCtxID` and logs its own warning verbatim: *"shared default-context
  capture mode is ON (tools.browser.capture_shared_context) — per-agent
  browser-context isolation is OFF"*.
- So ADR-043 D2's per-agent CDP browser context — the decision ADR-043 itself
  calls "load-bearing" — **does not exist on a fresh install today**.

That is ADR-048's ratified Option A, working exactly as ADR-048 intended: it
traded ADR-043 D2's isolation away so that `chrome.tabCapture` could reach the
agent's tab. ADR-048 recorded the price and recorded the escape hatch.

**This ADR pays the price back by taking the escape hatch.** It does three
things, and it is worth being precise about which is which:

| | Before this ADR | After |
|---|---|---|
| Is cookie/login isolation on? | **No** — off by default (`CaptureSharedContext: true`) | **Yes** |
| Isolation primitive | CDP browser context (unused by default) | **Chrome process + `--user-data-dir` profile** |
| Keyed by | the agent (when enabled at all) | **the workspace** |
| Can the live panel capture an isolated tab? | **No** — the reason isolation was turned off | **Yes** — each Chrome carries its own extension (D1.4) |
| Chromes per gateway | exactly one, for the coordinator's lifetime | **N, bounded by a cap** (D1.5–D1.8) |

**ADR-043's named limitation is deliberately reversed.** ADR-043's "Accepted
limitations" says: *"If an operator specifically wants agents to share a login
(e.g. Jim logs in, Ray reuses it), that becomes a deliberate per-context opt-in
(future), not the default."* D1 makes exactly that the default **within a
workspace**. Authority: the operator ruling of 2026-08-31. Stated explicitly
because an amendment that silently reverses a named limitation of the ADR it
amends is how the next reader concludes the reversal was accidental.

**ADR-048's ratified v1 choice is deliberately reversed too** — same reasoning,
recorded in the header's Amends block and in §9.3.

**What does not change:** ADR-038 D6's take-the-wheel model within a context
(**not** ADR-040 — that citation is wrong in ADR-043's header and is corrected
here so it stops propagating); ADR-041's tab-set model; **ADR-043 D4** only
(hot-reload survival), now delivered by the on-disk profile rather than by
context re-adoption.

**One stale comment, filed separately.** `defaults.go`'s comment tells an
operator who wants real isolation to set `CaptureSharedContext` false because
"the JPEG browser_screencast fallback keeps working either way". ADR-061 deleted
the JPEG screencast path in full, with a CI guard
(`scripts/check-no-jpeg-screencast.sh`). So the comment directs an operator to a
trade-off that no longer exists: today you can have cross-agent cookie
isolation, or a live video panel, but not both. This ADR removes that dilemma;
the comment is a separate defect.

### D1.4 The mechanism: one Chrome process + one profile per workspace

**Isolation by CDP browser context is unusable for this product, for two
independent reasons.** Both were verified against real Chrome 150 (ADR-048,
Wave-3 e2e, commit `687c7c6e`):

1. **`chrome.tabCapture` hard-fails** with *"Invalid tab specified."* for any tab
   in a CDP-created context (`pkg/tools/browser/coordinator.go:330-348`). So an
   isolated tab cannot be streamed to the live panel.
2. **`chrome-extension://` pages will not load in such a context at all** —
   navigation fails `net::ERR_BLOCKED_BY_CLIENT`, even with
   `enableInIncognito: true`, so the encoder page is forced into the default
   context regardless (ADR-048 Context, `capture_session.go`).

The second is the more final of the two: the first says a tab cannot be
captured, the second says the capture machinery cannot even be hosted there.
Together they close the CDP-context route rather than merely making it awkward.
Both are properties of the extension-based capture path, **not of isolation
itself** — which is why replacing the primitive, rather than abandoning
isolation, is the way out.

**Decision: isolate by Chrome process and profile directory, one per workspace**
— a separate `--user-data-dir`, not a CDP browser context.

**Cost, honestly.** Every tab is already its own renderer process regardless of
how tabs are grouped, so the dominant cost does not change; what multiplies is
the per-Chrome *browser* process. ADR-043 rejected "one Chrome per agent" on
≈4–5 GB at ten agents — an estimate labelled in its own text as "rough
order-of-magnitude, not measured", and per **agent**. Workspaces are far fewer
than agents, and only a workspace actively being browsed needs a live Chrome.
The real figure is unmeasured; D1.5 makes measuring it a gate.

**The one sandbox objection is gone.** ADR-043 rejected multiple Chromes partly
because the debug port was pinned at 9223 and "a dynamic per-manager port cannot
be followed" by the compiled Landlock/seccomp allow-lists. Chrome is now driven
over `--remote-debugging-pipe` on inherited fds 3/4 —
`pkg/tools/browser/exec_resolver.go:60` states it plainly: *"There is NO
`--remote-debugging-port` — CDP flows over the inherited fd 3/4"* — and
`pkg/gateway/sandbox_apply.go:414` records that the allow-list entry was removed
along with that port. N Chromes mean N pipes and nothing to allow-list.

#### G-2 — the load-bearing assumption, and it is not yet proven

D1.4 asserts each Chrome carries its own extension so `chrome.tabCapture` works.
The reasoning is sound and worth recording: `LoadExtension` installs via CDP
`Extensions.loadUnpacked` (`coordinator.go:892-896`), which **scopes to the
DEFAULT browser context** — and under D1.4 a workspace's tabs live in its
Chrome's default context, which is precisely the case that works today. The
ADR-048 failure was specific to *CDP-created* contexts, where
`WithEnableInIncognito(true)` grants visibility but, as `coordinator.go:951-958`
records, "**VISIBILITY only, not capturability**".

**But reasoning is what failed last time.** "An isolated tab can be captured" was
equally plausible until it was tested against real Chrome. So this is a **gate,
not a footnote**:

> **G-2.** Prove `chrome.tabCapture` succeeds against a **second Chrome's
> default context** before the pool is built. If it fails, D1.4 does not stand
> and the ADR-048 trade-off returns.

G-2 is mechanical and the CI machinery for it already exists. The `browser-e2e`
job (`.github/workflows/pr.yml:400-480`) sets `OMNIPUS_BROWSER_E2E: "1"`
(`:416`) and **fails the job** if either skip path fires (`:469` greps the log
for `skipping browser E2E test` and `no managed Chrome for`). What is missing is
the test itself — no test exercises a second Chrome instance today. G-2 is
satisfied when that test exists, runs in that job, and its receipt is captured
without a pipe (`cmd > log 2>&1; echo "exit=$?"`).

**G-1 — cost.** Per-Chrome memory is unmeasured and it sizes the cap (D1.5).
Measure the **marginal PSS** of the Nth Chrome on `about:blank` before setting
any default. G-1 is a human review gate: its artefact is the raw `smem` output
for N = 1…4, the host's total RAM, and the arithmetic from there to the shipped
cap, in the implementing PR's body. **PSS, not RSS** — see §8 row 3, and §9.1,
where the downstream spec still specifies RSS.

### D1.5 Sizing the pool

**Measured on `omnipus-uat-swimlane` (2 dedicated cores, 3916 MB), one Chrome,
12 processes, a handful of tabs:**

```
total RSS  1118 MB      <- what an earlier revision of this ADR reported
total PSS   434 MB      <- the honest figure
```

**RSS over-counts by 2.6× here** and the earlier figure is retracted (§8).
Chrome's program code is file-backed and mapped once physically, but RSS charges
it to every process — twelve times over in this sample. PSS divides it fairly,
and PSS is what a capacity formula must use.

#### The unit is the renderer process

Per-tab is meaningless as a planning unit, for two independent reasons:

- **Page type varies more than 20×.** An idle Wikipedia article ≈ 15 MB PSS; a
  Reddit feed ≈ 54 MB; Gmail ≈ 120–180 MB; YouTube at 1080p ≈ 222–341 MB. Our
  own repo's measured 74–268 MB per renderer sits in the middle of that range,
  not at its ceiling. *(These external figures are uncited and are used for
  shape, not for sizing — the shipped number comes from G-1.)*
- **A tab is not a process.** Under site-per-process every cross-site embed can
  claim its own renderer; same-site reuse collapses many embeds into few. The
  count is neither 1 nor the embed count — it must be observed.

Renderer count is the only term we can bound in advance, so it is the term the
cap is expressed in.

#### Chrome computes its own limit, and that limit does not compose

Chromium's `render_process_host_impl.cc` uses
`kEstimatedWebContentsMemoryUsage = 85` MB and half of physical RAM as the
budget, clamped to at least 3 renderers. On the measured box that yields
`3916 / 2 / 85 = 23` renderers — **per Chrome**. Four workspaces would each
independently permit 23, i.e. ~92 renderers on a 3.9 GB machine, every one of
them sanctioned by Chrome. **The pool must impose its own bound; Chrome's does
not add up.** This is the single most important consequence of going from one
Chrome to N, and nothing in ADR-043 anticipates it.

#### Decision

1. **Bound renderers per instance** with `--renderer-process-limit=R`. **R is a
   floor derived from site isolation, not a memory knob** — see D1.6.
2. **Derive the instance cap:**

   ```
   budget        = min(host_RAM, cgroup_limit) × 0.5      // Chrome's own policy
   pool_budget   = budget − gateway_reserve
   per_instance  = FIXED_FLOOR + (R × 85 MB) + encoder_page
   max_browsers  = clamp(pool_budget / per_instance, 1, operator_ceiling)
   ```

   Terms, all of which must be defined before the formula is evaluated:

   | Term | Definition |
   |---|---|
   | `gateway_reserve` | The gateway process's own steady-state PSS plus 25% headroom, measured on the same host in the same G-1 pass. Not a constant. |
   | `FIXED_FLOOR` | The **marginal** PSS of a second instance on `about:blank`. Unmeasured — G-1. It will be materially below the first instance's, because code pages are already resident. No trustworthy published figure exists for modern headless Chrome. |
   | `encoder_page` | +1 renderer on any **watched** instance. The WebRTC encoder page (`pkg/tools/browser/capture_session.go:500`) is excluded from the visible tab budget but is **not** excluded from `--renderer-process-limit`. It must be budgeted explicitly or a watched workspace silently loses one of its R renderers to infrastructure. |
   | `operator_ceiling` | The config key `tools.browser.max_browsers`. Adopts `max_total_tabs`' shape: `<= 0` means unlimited. Reload-applied, no restart. |

   **No hardcoded default ships.** The shipped default is computed from the G-1
   measurement on the installing host and recorded in the PR body. Any worked
   example in this document (`≈3 browsers` on the measured box at R=4 with a
   200 MB placeholder floor) is **illustrative arithmetic, not a default** — it
   is built on the placeholder `FIXED_FLOOR` G-1 exists to replace.

3. **Gate admission on real pressure where the platform provides it.** On Linux
   under a cgroup limit, refuse to grow the pool when
   `memory.current / memory.max > 0.85`. See D1.9 for what stands in for this
   on the platforms that have no such signal — the honest answer is "nothing",
   and the compensation is a conservative `operator_ceiling`.

4. **`max_total_tabs` stays a global budget across all N Chromes**
   (`coordinator.go:118-128`, `<= 0` means unlimited), not a per-instance one.
   It bounds total renderers across the pool; `--renderer-process-limit` bounds
   them within one instance. The two are different guards and neither replaces
   the other.

#### Cold start, and the four unknowns

`FIXED_FLOOR`, whether Chromium reads cgroup limits at all, whether Linux
memory-pressure signalling still fires, and **cold-start latency with a warm
profile on disk** are all unmeasured. All four are cheap, and all four are
required before the pool is built. ADR-042's ~30–60 s figure covers a fresh
install including a Chromium download and is not the relevant number for a
relaunch from an existing profile.

### D1.6 Site isolation is a floor, not a knob

`--renderer-process-limit` has a security cost: over-limit navigations reuse
same-site processes, weakening site isolation for the pages beyond the limit.

An earlier revision called that "acceptable for agent-driven browsing of
semi-trusted destinations". **That adjective describes nothing the code
enforces.** `BrowserManager.ValidateURL` (`pkg/tools/browser/manager.go:685-708`)
blocks exactly five schemes (`blockedSchemes`, `:675-683`) and private/metadata
addresses via the SSRF checker (SEC-24). Every other public `http(s)` URL is
permitted. There is no allow-list anywhere in `pkg/tools/browser/`. And
`browser_navigate`'s URL comes from model output, which is itself influenced by
page content the agent just read.

Under D1.10, a workspace's browser is the one place the operator's live logins
sit. A memory knob must not be allowed to place the operator's signed-in bank
tab and a third-party page an agent found into the same renderer.

**Decision: R is derived from site isolation and the instance cap is derived
from R — not the other way round.**

- **R must be at least the number of tabs a workspace may hold concurrently.**
  That is the only bound we have on "how many distinct sites are open at once",
  and it is a real one: a tab count is enforced (`maxTotalTabs`), a site count
  is not.
- If the resulting `max_browsers` comes out below 1 on a small host, the honest
  answer is that the host cannot run the pool at the configured tab budget.
  **Lower the tab budget. Do not lower R.**
- If a navigation allow-list is ever introduced, this decision may be revisited
  — with the allow-list as the stated control, not an adjective.

Acceptance criterion: §3.2 case P8.

### D1.7 The cap manages itself

**Operator ruling, 2026-08-31: there is no "pool full" error surface and no UI
change.** An earlier draft proposed a REST path and a close button; both are
withdrawn. (The implementation spec still specifies the withdrawn design —
§9.1.) The ruling governs the **normal** path: an operator never sees a capacity
concept, because eviction handles it silently. The one exception is the hard
ceiling below, which the ruling did not contemplate because the draft it ruled
on had no ceiling — that case is stated as an exception rather than folded in.

**What makes this safe: closing a browser is not destructive.** The logins live
in the profile directory on disk, not in the process. A closed browser reopens
signed in.

**Policy.** When a workspace needs a browser and the cap is reached, evict the
**least recently used** instance and start the new one. The evicted workspace
reopens on next use, still signed in, paying only start-up latency.

Two guards:

- **Never evict an instance with a viewer attached** — someone is watching it.
- **Never evict an instance with a tool call in flight** — an agent is mid-action.

**A viewer pins an instance, so viewer-attach needs a staleness timeout.** The
attach counter already makes a context with a viewer "NEVER idle"
(`manager.go:248`); under the pool it also makes that instance un-evictable. An
abandoned panel would pin a slot indefinitely. A viewer whose transport has been
silent past the existing WebRTC liveness window is treated as detached for both
purposes.

#### Ten workspaces on a three-browser machine

The cap counts **concurrently browsing** workspaces, not existing ones. Ten
workspaces with one or two active at a time never reach a cap of three; the rest
hold no process at all. Switching to an evicted workspace reopens it, still
signed in, at the cost of start-up latency only.

#### Thrash must be visible

If more workspaces browse concurrently than the cap allows, each new request
evicts one that is about to be needed again — every workspace paying a cold
start, continuously, with no error and nothing on screen to explain it. That is
precisely the "slow for no visible reason" shape this ADR exists to eliminate
elsewhere.

**Decision:** the pool tracks evict-then-reopen cycles per workspace. If a
workspace is reopened within a short window of being evicted, more than a small
number of times in a rolling period, the pool logs **one** WARN naming the cap,
the contending workspaces, and the remedy (raise `tools.browser.max_browsers`,
or browse fewer workspaces at once). Thrash is a capacity mis-sizing an operator
can fix, and must be reported as one rather than experienced as latency. The
exact window and threshold depend on cold-start latency, which is unmeasured
(D1.5) — they are set from that measurement, not guessed here.

#### When nothing is evictable, the overshoot is bounded

Every instance watched **and** busy: **exceed the cap by one, and one only.**

- The overshoot ceiling is `max_browsers + 1`, **total, not per request.** A
  memory guard that grants +1 per concurrent request settles at `cap + k` under
  the normal shape of a team demoing, which is not a guard.
- At the ceiling, the request **waits** for the first instance to become
  evictable, up to the tool call's own deadline, and only then fails with a
  named error naming the workspace and the cap. **This is the one place an
  operator can perceive capacity**, and it is deliberate: a refusal is bad, and
  unbounded growth on a 3.9 GB box is worse. It is not a UI, a button or a REST
  path — it is a tool error, reached only when every instance is simultaneously
  watched and busy.
- The single overshoot logs a WARN naming the cap and the workspace.

**The cap is therefore a soft target with a hard ceiling, and both must be
documented as such** — a config field described as a hard limit that silently
overshoots would be its own defect.

### D1.8 Pool lifecycle

**Whole-Chrome idle close — decided.** `ReapIdleSessions` reaps tabs, not
Chromes; closing a whole Chrome is a new operation and it is specified here.

- Config key `tools.browser.idle_close_ttl`, default **15 minutes** — 3× the
  per-tab `DefaultIdleTTL` of 5 minutes (`manager.go:134`), so a Chrome is only
  considered for closure well after its last tab was reaped.
- Caller: the existing 1-minute browser-reaper sweep
  (`pkg/gateway/gateway.go:5331-5351`), **after** its `ReapIdleSessions` loop in
  the same tick.
- An instance is closable when it has zero tabs, zero attached viewers, and no
  tool call in flight, for longer than the TTL.
- Post-close state: the pool entry and the Chrome process are gone; the
  `*BrowserManager` is **retained**; the next tool call relaunches from the
  profile, still signed in. Idle close **never** deletes a profile.
- The reaper cancelling `se.browserCancel` must never leave a key the pool
  reports live but nothing can drive.

**Crash containment — decided.** ADR-043 accepted "one Chrome crash takes down
all browsing" when there was one Chrome. That is no longer acceptable and it is
not what the code does today: `watchForCrash`
(`pkg/tools/browser/coordinator.go:1357-1402`) clears **every** context, resets
**every** connector manager and relaunches the single Chrome.

- Under the pool, `watchForCrash` becomes **per instance**. One Chrome's death
  invalidates exactly one key's manager and clears exactly that key's state.
- Recovery relaunches that workspace's Chrome from its own profile, so the login
  survives the crash.
- No other workspace's manager is reset, and no other workspace's panel drops.

**Boot reconciles orphans — and must not shoot a live neighbour.** After a
`kill -9`, a Chrome can survive with no gateway managing it, consuming memory
outside the cap, invisibly — which makes the cap meaningless. But "terminate
what the marker names" is the wrong rule: the marker records the **Chrome's**
pid (`readOwnershipMarker`, `coordinator.go:1552-1562`), and that pid is alive
in both the orphan case and the case where a second gateway is running normally
on the same `$OMNIPUS_HOME`.

**The per-key launch lock is the discriminator, not the marker.** Today
`takeLaunchLock` (`coordinator.go:1442-1480`) already gets this right for the
single-Chrome case: on Unix the flock auto-releases when the holding process
dies, so a *held* lock plus a live Chrome pid means another gateway is genuinely
running, and it **refuses to launch** rather than killing anything. The pool
keeps that:

| Per-key launch lock | Marker's Chrome pid | Action |
|---|---|---|
| acquirable | dead / absent | clear the stale marker and lock (INFO) |
| acquirable | **alive**, owner is omnipus | **orphan** — terminate it, clear the marker (WARN, naming workspace and pid) |
| **held** | alive | **another live gateway** — refuse to launch this key, name the other gateway. Never terminate. |

Identity of the process to terminate is confirmed via `/proc/<pid>/exe`. See D1.9
for where this guarantee degrades.

**Deleting a workspace deletes its browser profile.** The signed-in sessions of a
deleted workspace do not linger on disk. This is the **only** trigger — idle
close, roster change, hot reload and crash recovery never delete. It is
irreversible and has no undo: deleting a workspace by mistake loses those logins.
Accepted; the alternative (retain plus a separate purge action) leaves a departed
client's live sessions on disk until somebody remembers, which is worse.

**Upgrade: the existing profile is not inherited.** Today there is a **single
global** profile at `~/.omnipus/browser/profiles/default/`
(`pkg/tools/browser/manager.go:125`), holding whatever the operator is signed
into. Under D1.4 it becomes N per-workspace profiles at
`~/.omnipus/browser/profiles/ws-<workspace-id>/`, created `0700`.

**Decision: no workspace inherits the existing profile.** Every workspace starts
with a fresh profile and agents sign in again. Copying it to all workspaces would
pool logins across workspaces that never shared them — defeating §3.1 case 5b on
the first boot after upgrade — and adopting it into one arbitrarily-chosen
workspace is a silent, unexplainable grant. The old `profiles/default/` directory
is **left on disk, untouched and unused**: deleting it would destroy logins the
operator may still want and no code can tell whether they matter. A release-note
line states that agents need to sign in again after upgrade.

**A stuck dialog may be cleared even while a human holds control.**
`browser_handle_dialog` is exempt from the write lease **and** from
`controlledResult`. The reason is not convenience: a modal blocks every CDP
command on that tab, including the live panel's own input injection — so the
human at the wheel is equally stuck and has no button that works. Gating the one
tool that can clear it leaves both parties frozen. This narrows ADR-038 D6's
exclusivity by exactly one tool, on a tool that cannot act on page content.

**Boot-warm warms one instance, not N.** `WarmAtBoot`, `WarmTabAtBoot` and
`WarmCaptureAtBoot` all ship `true` (`pkg/config/defaults.go:679, :685, :692`)
and were written for one shared Chrome. Under the pool, boot warms **exactly the
resolved workspace of the default agent** — one instance, one tab, one capture
pipeline. If no workspace resolves, boot warms nothing and logs one INFO (not a
WARN: it is a missed optimisation, not a fault). Warming every workspace would
make every workspace "concurrently browsing" at t=0, erasing the distinction
D1.7's cap rests on, and would multiply `WarmCaptureAtBoot`'s continuous encoder
CPU — which runs for `WarmCaptureIdleSec` (300s, `:695`) — by N on a box §7
measures at 85–99% utilisation.

### D1.9 Platform posture

The pool's guarantees are not uniform across platforms. Stated because a partial
guarantee documented as a full one is worse than no guarantee.

| | Linux | macOS | Windows |
|---|---|---|---|
| Memory-pressure admission gate (D1.5 item 3) | **Yes**, under a cgroup limit. Meaningless on a non-containerised host where `memory.max` is `max`. | **No** — no cgroups. The formula plus a conservative `operator_ceiling` is the entire control. | **No.** |
| Orphan termination (D1.8) | **Yes** — `/proc/<pid>/exe` confirms identity. | **Partial** — no pure-Go equivalent of `/proc/<pid>/exe`, so macOS clears the marker **without** terminating. An orphan Chrome survives outside the cap. | **No.** |
| Second-gateway protection (D1.8) | **Yes** — flock auto-releases on death, so a held lock proves a live neighbour. | **Yes** — same flock semantics. | **No.** `fileutil.WithFlock` is a documented no-op (`pkg/fileutil/flock_windows.go`), the fallback lock is `O_EXCL` which does not clear on crash, and `pidAlive` returns `true` unconditionally (`coordinator.go:1569-1575`). Two gateways on one `$OMNIPUS_HOME` have in-process protection only. |
| Process sandbox around the Chromes | Landlock + seccomp | Seatbelt (children only) | **None** — `selectBackendPlatform` returns `FallbackBackend` (ADR-062 §4.3). |

**The honest summary:** on Linux the pool is bounded by the formula, the
pressure gate and orphan reclamation. On macOS it is bounded by the formula
alone, so `operator_ceiling` should default conservatively there. On Windows it
is bounded by the formula alone **and** has no cross-process guard; the pool is
supported there in the same degraded sense as the rest of the file-store family
(ADR-054 §5), not in the same sense as on Linux.

### D1.9a Tabs stay per agent; the operator's tab is the shared one

**Operator ruling, 2026-09-01 (Daniel Piatkowski), restated here for
correction if misread:**

> the default is they open a new tab — we have in the current version that an
> agent has its own tab, we should maintain that. Only if the user starts a tab
> are the agents able to see it and take control on request.

**The model.**

| | Owner | Who can see it | Who can drive it |
|---|---|---|---|
| A tab an **agent** opens | that agent | that agent | that agent |
| A tab the **operator** opens | the workspace | **every agent on the workspace** | the operator; an agent **on request** |

One Chrome per workspace, one cookie jar (D1.3, D1.10) — **and inside it, tab
ownership stays per agent**, exactly as today.

**This is a preservation, not a new feature — but it does not survive the
re-key by itself.** Today's tab set belongs to the *browsing context*
(ADR-041 D1: `Session(defaultSessionID)` returns the active tab of that
context's `[]*tabEntry`), and agents are separated only because each has its
own manager. Collapse the managers to one per workspace, as D1.1 does, and a
single tab set would be shared by everyone — losing the separation this ruling
requires. **The agent dimension must therefore be carried explicitly on the tab
set.** An implementation that only re-keys the manager silently deletes this.

**What it fixes, and it is §1.1's actual defect.** The operator opened the
panel, browsed, and the tab was attributed to whichever agent's panel happened
to be on screen — so Jim could not see it. Under this ruling an
operator-opened tab is **not owned by an agent at all**; it belongs to the
workspace and is visible to every agent on it. Jim sees it because it was
never Mia's.

**What it removes from this ADR.** Concurrency was scoped as "two agents
sharing one tab set" and answered with a write lease (previously D2.10). Two
agents working on **their own tabs do not contend**, so the general case
disappears. What remains is narrow and already built:

- **Operator vs agent** on the operator's tab — `LiveViewRegistry.TakeControl`
  / `IsControlled` (`pkg/tools/browser/live.go:1236-1310`), ADR-038 D6, the
  "on request" in the ruling. Unchanged.
- **Agent vs agent** on the operator's tab — the only genuine contention left.
  A lease is justified *here*, on one shared tab, not across the whole surface.

**Superseding the C1 question this ruling answered.** The grill asked whether a
losing writer should retry-then-error or return a non-error "deferred". The
operator answered by removing the premise. The retry-vs-defer decision now
applies **only** to the operator's tab, and the D1 spec's §14 must be rescoped
from "every action tool" to that case — a much smaller change than either
answer implied.

**Open, and not decided here:** what "take control on request" looks like to an
agent — an explicit tool, or an implicit acquisition on first write to the
operator's tab. The ruling says "on request", which reads as explicit; the
mechanism is a D2 tool-surface decision and belongs in §6 until ruled.

### D1.10 Everyone on the workspace shares it — including unattended delegated work

**Operator ruling, 2026-08-31: every agent on a workspace shares that
workspace's browser and its logins, including unattended delegated work.**

This reverses an earlier ruling made the same day (unattended work gets its own
signed-out context), and the reversal is informed rather than a contradiction:
when the isolation boundary was a CDP browser context inside one Chrome, a second
jar was nearly free. Under D1.4 it is **another whole Chrome process per
background job**. Weighed against that cost, the operator chose sharing.

**Two consequences, both grants, both stated rather than discovered:**

1. **Adding an agent to a workspace grants it every live signed-in session on
   that workspace.** That follows from the team being the trust boundary, and it
   is the same judgement an operator already makes when choosing a roster — but
   it is a grant and must read as one at the point of adding (D2.11).
2. **An unattended agent can act as the operator on any site the workspace is
   signed into** — a purchase, a post, a message sent by a process nobody is
   watching. `browser_upload_file`'s global `ask` seed (D2.9) is the one place
   this is still gated, and issue #659 is its hard prerequisite.

**What this deletes:**

- The attended/unattended discriminator. It does not need to exist.
  `spawnSubTurn` inheriting the parent's `WorkspaceID`
  (`pkg/agent/subturn.go:1323`) is **correct behaviour**, not a gap.
- The per-key browser-context map. `BrowserManager.browserCtxID` staying a
  single field is **correct**: one context per manager, one manager per
  workspace.
- `tools.ToolTranscriptSessionID` as a browsing key. Unused, and not a fallback.
- The acceptance criterion "a delegated sub-agent does not hijack the operator's
  tab". It describes behaviour the operator has ruled desirable.

### D1.11 The key, and the turn that has no workspace label

**The browsing key is the workspace id**, read from
`tools.ToolWorkspaceID(ctx)` (`pkg/tools/base.go:250-256`). It already reaches
every tool, so this needs no new plumbing. It survives switching agent and
switching chat, which is exactly the property §1.1 needed and did not have.

Every browser tool already takes a session id on every call; `defaultSessionID`
is threaded through `tools.go` and `tabs.go` and is currently wasted on a
constant. The tool **descriptions** that claim "the shared browser session" must
change with the behaviour, not after it.

**There is no "no workspace" fallback.** An earlier revision proposed falling
back to the `"default"` constant when `ToolWorkspaceID(ctx)` is empty. **That is
rejected.** It would merge every workspace-less agent into one shared cookie jar
— an isolation regression against today's per-agent keying, and against the
guarantee §3.1 case 5b exists to prove.

**Operator ruling, 2026-08-31: every turn runs in a workspace context; a
scheduled run outside one is not a state the product supports.**

The code agrees, and the emptiness is narrower than it looks.
`pkg/tools/resolvepath.go:695-709` records that a scheduled/heartbeat turn **is**
rooted in a CoreTeam workspace — its work dir is re-rooted — and that
`ToolWorkspaceID(ctx)` is nevertheless empty because `workspace_reroot.go`
deliberately does not set it (an FR-030 memory-room-routing decision, unrelated
to the browser). The workspace exists; only the label on the turn is missing.

**Decision: resolve it the same way the work dir already is.** `resolvepath.go`
hit this exact gap for filesystem mounts and solved it with
`FindForAgentPreferring` rather than a fallback constant, explicitly so "the two
never disagree about which workspace this turn is rooted in". The browser uses
the same resolution, for the same reason. If resolution genuinely yields nothing,
the browser tool **fails with a named error** naming the agent and the turn — it
does not silently join a shared context. A wrong-jar failure is invisible; a
refusal is not.

### D1.12 The silent zero — and the agent that cannot see the browser at all

**`ListTabs` must distinguish two states, not one:** "this workspace has no
browsing context yet" and "the browsing context has no tabs". Both are
legitimate; reporting them identically is what made §1.1 unobservable
(`ListTabs` returns `nil, 0, nil` for both). The tool's description must also
stop claiming "shared" unless and until it is true.

**A third state — "you are not permitted to see the browser" — was specified in
an earlier revision and is withdrawn, because `ListTabs` cannot produce it.**

The seeded policy grants the browser surface to Jim (`pkg/coreagent/core.go:1052-1064`)
and Ray (`:910-921`) among the base roster; Mia (`:848`) and Ava (`:794`) hold
no `browser_*` override and `denyAllThenOverride` (`:466`) therefore stamps an
explicit `deny` for every browser tool. And `FilterToolsByPolicy`
(`pkg/tools/compositor.go:429-444`) **removes every deny-verdict tool from the
definitions sent to the model** (`:436-439`, `continue`). A policy-denied agent
is never shown `browser_list_tabs`, never calls it, and answers from absence.

**Ruling: drop the third state from the `ListTabs` contract, and drop the
acceptance criterion that depended on it.** No change inside a tool can be
observed by an agent that does not have that tool. The two-state work stands and
is worth doing on its own merits, for the agents that *hold* the tool.

**The underlying problem is real and this ADR does not solve it.** Mia is the
default agent and the agent in §1.1's own repro; asked what is open, she will
still answer from absence, now with a different cause and the same output.
Solving it means telling an agent, outside the tool-result path, that a browser
exists on this workspace and that it is not permitted to drive it — a
system-prompt or manifest-note surface, not a tool. The operator has confirmed
(2026-08-31) that Mia's and Ava's deny stays, so widening the policy is not the
answer. This is recorded in §6 as genuinely open rather than specified as
behaviour that cannot happen.

### D1.13 Live-panel resolution and the wire contract

Today the live panel resolves a manager by **agent id** at three gateway call
sites: `pkg/gateway/browser_webrtc.go:279`, `pkg/gateway/browser_ws.go:1252`,
`pkg/gateway/browser_inspect.go:73` — all `BrowserManagerForAgent(frame.AgentId)`.
With one manager per workspace there is no per-agent manager to resolve, and an
agent on two workspaces makes `agent_id` ambiguous. This is why ADR-043 D3 is
amended rather than preserved.

**Decision: resolve agent → workspace server-side; the wire keeps `agent_id`.**

- The gateway prefers the **attaching session's** `workspace_id`
  (`pkg/session/unified_meta_files.go:60`), which is unambiguous even for an
  agent on several workspaces, and falls back to the agent's single workspace
  when the session carries none.
- If neither resolves — the agent is on no workspace, or on several with no
  session context — the frame **fails with a named error** identifying the
  agent. It never picks one.
- The `browser-webrtc[<agent>]` log label is cosmetic and stays.

**Hard Constraint #8 applies, and this is the part that is easy to miss.**
`agent_id` is a **generated contract field** in three schemas —
`contracts/components/schemas/BrowserAttachFrame.yaml`,
`BrowserWebRTCOfferFrame.yaml` and `BrowserInspectRequest.yaml`. No field is
added, removed or retyped, so no new schema file is needed. But all three
schemas' **descriptions currently assert that `agent_id` selects the browsing
context** (`BrowserAttachFrame.yaml:8` and `:35`,
`BrowserWebRTCOfferFrame.yaml:7` and `:38`). After this change it selects a
*workspace* by way of the agent. **That description edit is a semantic reversal
of a documented wire contract**: it goes through the 5-step process
(`scripts/gen-contracts.sh`, generated diff committed in the same commit) and
must be reviewed as a behavioural contract change, not as prose.

---

## D2 — Capability

**The agent-facing tool surface is completed: how an agent finds an element,
when it is safe to act on it, and the verbs it can use.**

### D2.0 What we ship today

Eleven tools: `browser_navigate`, `browser_click`, `browser_type`,
`browser_get_text`, `browser_screenshot`, `browser_evaluate`, `browser_wait`,
and the four tab tools (`list`/`switch`/`open`/`close`).

A prior playwright-go evaluation (cited in issue #569) concluded that swapping
the engine was not worth it, and that **role selectors and auto-waiting were the
only benefits that survived scrutiny — both implementable on chromedp**. This
ADR takes that conclusion and adds what a survey of the actual tool surface
shows is also missing.

### D2.1 Element selection by role and accessible name — issue #569

Today an agent resolves a target by **CSS selector** or by our own visible-text
matcher (`pkg/tools/browser/text_selector.go`). The CDP Accessibility domain is
unused: `getFullAXTree` / `queryAXTree` appear nowhere in `pkg/` or `src/`.

An LLM reasons about a page as *"the Submit button"*, i.e. role + accessible
name. Making it translate that into CSS is where avoidable action failures and
retries come from, and CSS is the least stable thing on a page that A/B tests.

**Decision:** add role + accessible-name selection alongside CSS and text
(additive, not a replacement), sourced from the CDP Accessibility domain rather
than a reimplemented accname computation, exposed through the same
target-resolution seam the text selectors already use so every action tool
inherits it. Deterministic ordering plus an index affordance on multi-match,
mirroring the existing text-selector behaviour.

### D2.2 Actionability — currently one quarter built, unfiled

Playwright waits for an element to be **visible, stable, enabled and
hit-testable** before acting. We do the first only: `tools.go:257` (click) and
`:461` (type) prepend `chromedp.WaitVisible`; `:685` uses `WaitReady` for text.
Nothing checks enabled-ness, nothing checks that the element has stopped moving,
nothing checks that the click will land on it rather than on an overlay.

Issue #569 says this deserves its own issue. **It was never filed** — verified
against all open and closed issues. It is folded in here.

**Decision:** complete the actionability contract in the shared pre-action path,
so every action tool inherits it: visible → stable (two consecutive identical
bounding boxes) → enabled → hit-testable. On timeout the failure must name
**which** condition was not met — "not clickable, covered by another element" is
actionable by an agent; "timeout" is not.

### D2.3 Missing verbs

Verified absent from `pkg/tools/browser/` (non-test): zero occurrences of
`select_option`, `file_upload`, `hover`, `press_key`, or dialog handling.

| Tool | Why an agent needs it |
|---|---|
| `browser_select_option` | **A dropdown cannot be used at all today.** `<select>` does not respond to click+type |
| `browser_press_key` | No Enter to submit, no Tab to advance, no Escape to dismiss |
| `browser_hover` | Menus that open on hover are unreachable |
| `browser_upload_file` | Any flow with an attachment dead-ends |
| `browser_handle_dialog` | A page calling `alert()`/`confirm()` blocks the whole session with no way out |

**Decision:** add the four verbs and dialog handling. Each is small
individually; together they are the difference between an agent that can read a
page and one that can complete a form.

**Where the dialog listener must live.** `installTargetListenerLocked`
(`manager.go:2578-2590`) attaches to `se.tabs[0]` only — correct for its own
purpose, because `Target` discovery is browser-global. But
`Page.javascriptDialogOpening` is **per target**. A tab-0-only listener misses a
dialog on tab 2 entirely, and that tab is then wedged with no record of why. The
listener must be per-tab.

**`browser_upload_file` cannot use the `PathHandle` mediation the other
filesystem tools use.** `SetUploadFiles` hands Chrome an absolute host path and
*Chrome* performs the read, so the `os.Root` TOCTOU-hardness is structurally
unavailable — only `RealPath()` applies. The residual window is stated rather
than hidden, and `AllowedRoots` confinement is the control that remains.

**Dialog handling carries a specific hazard:** a modal blocks every subsequent
CDP command on that tab. Whatever is built must guarantee the session cannot be
left wedged — the failure mode is not a bad result, it is a browser that stops
answering.

### D2.4 Reading a page as structure

An agent reads a page today via `browser_screenshot` (pixels, needing vision) or
`browser_get_text` (requires already knowing a CSS selector). Neither gives the
structure an agent needs to decide **what is on the page and what it can do
next**.

**Decision:** add an accessibility-tree snapshot — roles, accessible names, and
the handles needed to act on them — as the default way an agent reads a page.
This shares its source with D2.1: the same AX tree that answers "what is here"
answers "which node did you mean". Build them together or the second one is
built twice.

### D2.5 Telling the agent the supported route — issue #242

> **Name correction:** the tool is registered as **`serve_web`**
> (`pkg/tools/web_serve.go:46`, `const ToolNameWebServe = "serve_web"`;
> corroborated by `manifest.go:152`). This ADR, its round-1 review **and root
> `CLAUDE.md`** all said `web_serve`. Shipping that string in the error would
> have sent agents hunting for a tool that does not exist — the precise failure
> D2.5 exists to remove. The acceptance criterion asserts the literal. The SPA
> still dispatches on the retired name; see §8.

`browser_navigate` rejects `file://` deliberately (`manager.go:675-683`). The
agent is told only:

> `browser: file:// URLs are blocked for security reasons`

That is a dead end. The supported route exists one tool away — `serve_web` mints
a `/preview/<agent>/<token>/` http URL that `browser_navigate` accepts — and
nothing in the tool surface mentions it.

**Decision:** name the supported route in **the error message**, not only in the
tool description. The error is the moment the agent needs the answer; a
description read hundreds of tokens earlier is not.

### D2.6 Explicitly out of scope

- **Replacing chromedp with playwright-go.** The prior evaluation found only two
  benefits worth having, and D2.1/D2.2 deliver both without an engine swap or a
  new runtime dependency (Hard Constraint #1).
- **Network interception, frame targeting, drag-and-drop, cookie/storage
  manipulation.** Real Playwright features, no demonstrated agent need yet.
  Deliberately deferred rather than forgotten.

### D2.7 Capability gating — issue #456 is closed as redundant

#456 asked that `browser_*` be gated out of the manifest when no Chromium is
available. **Chromium is not fetched on demand in any shipping configuration:**
the Linux and macOS release archives bundle it (`scripts/install.sh` verifies
`chrome.sha256` and aborts on mismatch), and `docker/Dockerfile.heavy` bakes
Chrome-for-Testing in at build time. The managed download in `exec_resolver.go`
is step 4 of the resolver ladder — the fallback for bare-binary and hand-built
installs, not the normal path.

ADR-071 independently removed most of the cost the issue cited: all eleven
`browser_*` tools are Tier 3 / search-only, so they occupy no line in the
compressed manifest.

**Decision: close #456 for x86-64 Linux, macOS and the Docker heavy image** — on
those, Chromium is always present and a gate has no payer. Closed 2026-08-31 on
the operator's instruction; this ADR's Proposed status does not gate that.

**The exception, stated rather than glossed:** linux/arm64 *is* a shipping
install that enters exactly the state #456 described, permanently. The archives
carry no `chromium/` payload because Chrome-for-Testing publishes no linux-arm64
build (`scripts/install.sh:26-29`), the managed download fetches from that same
upstream so it cannot rescue it, and the `$PATH` escape is gated behind
`TrustPathChrome`, seeded `false`. On those hosts every `browser_*` tool is
registered and guaranteed to fail. This wants a build-and-distribution answer,
not a manifest gate; filed as **#665**. Until it lands, the tools remain
visible-but-failing on ARM Linux. That is the accepted trade-off, not an absence
of the problem.

### D2.8 Tier assignment (ADR-071)

ADR-071's *prose* describes Tier 3 as a closed enumerated list; the *code*
treats it as the residual — `ToolManifestVisibility`
(`pkg/tools/manifest.go:243-251`) returns `ManifestSearchOnly` for anything lazy
outside the 7-name previewed set (`previewedLazyToolNames`, `:149-157`). So five
of the six new tools become Tier 3 with **zero production edits**. The count is
also **62**, not 63 (`write_agent_metadata` retired), so ADR-071's prose is
already one ahead of its own fixture. The real edit sites are the pinned
literals in `manifest_test.go`.

| Tool | Tier | Why |
|---|---|---|
| `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_upload_file`, `browser_handle_dialog` | 3 — search-only | Same posture as the eleven existing browser tools |
| `browser_snapshot` (D2.4) | **open question** — §6 | D2.4 calls it "the default way an agent reads a page". A tool reached only through search is a poor default. Either it earns Tier 2 (and `previewedLazyToolNames` grows from 7 to 8) or D2.4's wording overclaims. |

### D2.9 Tool policy seeding (Hard Constraint #6)

**Boot aborts on any `agent × tool` policy gap.** Six new static builtin tools
therefore need an explicit, literal, wildcard-free entry for **every** agent.
Without this D2 does not boot — it is not a follow-up.

`pkg/coreagent/core.go` seeds **ten** agents (`All()` at `:141-152`: Mia, Jim,
Ava, Ray, Worker, Planner, Explorer, Researcher; plus Judge and PlanSupervisor
via `SystemAgents()`). The ones with a browser posture:

| Agent | Mechanism | `select_option`, `press_key`, `hover`, `handle_dialog`, `snapshot` | `upload_file` |
|---|---|---|---|
| Jim (`:1052-1064`) | `denyAllThenOverride` | allow | **ask** |
| Ray (`:910-921`) | `denyAllThenOverride` | allow | **ask** |
| Explorer (`:756-760`) | `denyAllThenOverride` | allow | **ask** |
| Researcher (`:782-786`) | `denyAllThenOverride` | allow | **ask** |
| Mia (`:848`), Ava (`:794`) | `denyAllThenOverride` | **deny**¹ | **deny**¹ |
| **Worker** (`:606`) | **`tightenGlobalCeiling`** — sparse map | **inherits the global seed**² | **inherits the global `ask`**² |

¹ **Erratum, 2026-08-31.** The ruling is that `browser_upload_file` is `ask` in
the **global** policy for every agent, and that stands. But Mia and Ava resolve
**`deny`** regardless, and an earlier version of this table showed `ask` for
them, which was wrong. `denyAllThenOverride` (`:466`) writes an *explicit
agent-level* `deny` for every catalog name an agent does not override, and
`compositor.go::resolveEffectivePolicyWith` merges **deny > ask > allow** — the
most restrictive wins. So a global `ask` cannot loosen an explicit per-agent
`deny`. The practical effect is what the operator intended (nothing uploads
without a human saying yes) reached by a stricter route than the table implied.

² **Worker is the agent the upload ruling is actually about, and it behaves
unlike every other agent here.** It uses `tightenGlobalCeiling` (`:606`), a
sparse override map, so every tool it does not name **inherits the global
ceiling** — today `allow` for all eleven browser tools
(`pkg/config/defaults.go:276-287`). "Mia and Ava get deny for free" therefore
does **not** apply to Worker. The global `ask` for `browser_upload_file` does
reach it, which is precisely the operator's stated intent ("not `deny` for
delegation-tier workers") — and precisely why #659 is a blocker.

**Coverage vs posture.** Because `ValidateToolPolicyCoverage` is OR-based, the
single `pkg/config/defaults.go:276-287` edit closes the **boot-abort** risk for
every agent. The per-agent edits are **posture**, not coverage. A spec that
treats them as coverage will over-scope the work and under-test the posture.

**Operator ruling, 2026-08-31: `browser_upload_file` is `ask` in the GLOBAL tool
policy, for every agent** — not per-agent, and not `deny` for delegation-tier
workers. It is the only browser tool that carries a local file across the
boundary into a remote site; every other browser tool moves data inward. That
asymmetry is worth one confirmation from whoever is driving.

**#659 is a hard prerequisite, not a related nicety.** An `ask` reaching an
agent with no human attached needs a defined answer. `AutoDenyAsk`
(`pkg/agent/loop.go:594-599`) provides it for headless scheduled runs, but it is
set only there (`loop.go:6958`) — `spawnSubTurn`'s `processOptions` never sets
it. **Issue #659 (open) records that `AutoDenyAsk` is not inherited by delegated
subagents**, so a delegated Worker that tries to upload a file today blocks on
an approval nobody can answer. Shipping the seed without #659 converts a clean
refusal into a hung turn.

### D2.10 Concurrency — one writer per browsing context

**What exists:** `controlledResult` (`pkg/tools/browser/tools.go:962`) checks
only `mgr.Live().IsControlled(...)` — a *human viewer* holding the live view.
Its own doc comment records two further limits: read-only tools
(`browser_screenshot`, `browser_get_text`, `browser_wait`) are deliberately not
gated, and the mechanism is "cooperative, not preemptive… no mid-tool preemption
in v1". Workspace-keying makes interleaved writes from two agents, or two chats,
the normal case rather than an exception.

**Decision: a write lease per browsing context**, held for the duration of one
action tool call. Read-only tools stay ungated, as today.

**A deferred write must become latency, not a silent no-op.** A deferred
`browser_navigate` did not navigate; a model that reads a non-error result as
success continues against a stale page. `controlledResult`'s existing
`{"deferred": true, "reason": …}` shape was designed for the human-control case,
where a human is present and the agent is meant to stop. Agent-vs-agent has no
such reader. So:

- The loser **retries inside the tool**, with backoff, up to a bounded number of
  attempts within the tool's own deadline.
- Only after the bound does it return — and then as a **named error**, not as
  `deferred`.
- The `deferred` shape is retained for the human-holds-control case, unchanged,
  so no prompt needs rewriting.

Deliberately not solved here: mid-tool preemption, and fairness between two
agents contending steadily (§6). Both are ADR-043-era limitations this ADR does
not widen.

### D2.11 Security

D1 changes who can act as a signed-in user, so the change needs this section
rather than a line under Consequences.

**Elevation of privilege.** Adding an agent to a workspace grants it every live
session on that workspace. **Decision:** the team-editing UI must state this at
the point of adding, before confirmation — not only in release notes.

**Repudiation — first-use auditing does not answer the question.** With a shared
context, "which agent acted as the signed-in user" must remain answerable. An
event on *first* use of a context an agent did not establish fires once per
agent per workspace and says nothing about the tenth action, or about which
agent made the purchase. **Decision: audit per action, for the write-class tools
only.**

- One event on **browser instance creation** for a workspace.
- One event per **write-class browser tool call** — the `controlledResult`-gated
  set — carrying workspace id, agent id, tool name and target host.
- Read-only tools are not audited per call; they do not act as the signed-in
  user.
- **Event names must match `^[a-z_]+$`** — the pattern the Audit Log viewer's
  contract enforces (`contracts/components/schemas/AuditEntry.yaml:17`). A
  dotted name blanks the whole viewer.

**Information disclosure — the snapshot is a widening, not an inheritance.** An
earlier revision said the snapshot "inherits `browser_get_text`'s redaction
posture and passes through the same `RegisterSensitiveValues` path". **There is
no such posture to inherit.** `RegisterSensitiveValues` appears **zero** times in
`pkg/tools/browser/`; `browser_get_text`'s entire treatment is a
64,000-character cap. And the replacer only substitutes registered *credential
plaintexts* — it would not touch account identifiers or form values even if it
were wired in.

It is worse than a missing inheritance. `browser_get_text` uses `chromedp.Text`
(innerText, which never contains input values), whereas an accessibility node
carries `Value` (`cdproto/accessibility/types.go:461`) — which **is** the field's
value. The snapshot can expose what a user typed into a form, including a card
number or a password field, where the existing text tool structurally could not.

**Operator ruling, 2026-08-31: the snapshot returns field values by default.**
Omit-by-default with an `include_values` opt-in was offered and declined; an
agent cannot verify a form is correctly filled before submitting it — one of the
main things this panel is for — without seeing what is in the fields.

**The risk is accepted, not absent.** A snapshot of a signed-in page can carry a
card number, a partially typed password, or an account identifier into the
conversation and into the stored transcript (`sessions/<id>/<YYYY-MM-DD>.jsonl`,
90-day default retention). Two mitigations follow, and the second one is weaker
than an earlier revision claimed:

- **The sensitive-value replacer is wired in as defence in depth.** It does not
  cover form values, but it costs nothing and closes the credential-plaintext
  case.
- **The snapshot renders as a visible tool call in the chat thread by default,
  and that is the inspection surface.** Browser tools are not in
  `src/lib/toolVisibility.ts`'s hidden set, so they render. **The ActivityPanel
  is not a fallback for this** — an earlier revision named it as the durable
  surface and that was wrong: `useRunningActivity.ts` aggregates only subagent
  delegation spans, background `bash` sessions and judge verdicts (`:1-15`),
  capped at `RECENTLY_FINISHED_CAP = 8` (`:148`). A `browser_snapshot` never
  appears there.

**And the visible-in-thread mitigation does not hold for unattended delegated
work — the population D1.10's ruling widened the risk to.** A delegated
sub-agent's tool calls render inside a SubagentBlock span, and
`shouldRenderSubagentSpan` (`src/lib/toolVisibility.ts:230-236`) returns
`verboseChatEnabled`, whose store default is **`false`**
(`src/store/chatPreferences.ts:19`). So with default settings, a delegated
agent's `browser_snapshot` — including whatever field values it captured — is
written to the 90-day transcript and rendered **nowhere** in the parent chat
thread, and it does not appear in the ActivityPanel either. The operator has no
default surface on which to see it.

Recorded honestly rather than closed, because closing it is real work and not
this ADR's: it needs either (a) a narrow exception that forces browser tool
calls inside a delegation span to render in the thread regardless of verbose
chat, or (b) extending the ActivityPanel to carry them. Both are UI decisions
with their own scope. §6 carries it.

---

## 3. Acceptance criteria

### 3.1 D1 — ownership

| # | Case | Required behaviour |
|---|---|---|
| 1 | Mia browses; operator watches and takes over | Works as today — unchanged |
| 2 | Operator switches the chat from Mia to Jim mid-session | Jim sees and drives the same tabs. No handover step, no command |
| 3 | Operator browses first, **then** asks an agent to take over | Any agent on that workspace **whose tool policy allows the browser surface** sees the tab |
| 5 | `browser_list_tabs` with no browsing context | Says so. Must not be indistinguishable from an empty tab set |
| 5b | **Isolation exists and is workspace-scoped.** Log in to a site in workspace X; open the same site in workspace Y | Y is **logged out**. The test that proves isolation was turned on and keyed correctly |
| 5c | **No surprise logout.** Log in during one chat; start a **new chat in the same workspace** and visit the same site | Still **logged in**. This is what the workspace axis buys over the conversation axis |
| 17 | **Unattended work shares the workspace browser (D1.10).** A delegated sub-turn on a workspace the operator is signed into | **Is signed in**, and reaches the same tab set. Asserted deliberately: this is the ruled behaviour, and a future change that silently isolates delegated work must fail here |
| 18 | **A workspace-less turn resolves, never merges (D1.11).** A scheduled/heartbeat turn | Reaches the same browsing context as its re-rooted work dir — never a shared constant, never another agent's |
| 19 | **Live-panel resolution (D1.13).** An agent on two workspaces attaches the panel from a session on one of them | The frame resolves to that session's workspace browser. With no session workspace and no unique agent workspace, it **fails with a named error** rather than picking one |

Cases 2 and 3 are the ones broken today; 1 works. Cases 5b and 5c are the
guarantees this design must not lose.

**Withdrawn: "a policy-denied agent says it is not permitted."** Specified in an
earlier revision; unreachable — `FilterToolsByPolicy` strips the tool from the
model's definitions, so the agent never calls it (D1.12). The underlying problem
is in §6.

**Withdrawn: "a delegated sub-agent does not hijack the operator's tab."** The
D1.10 ruling makes sharing the intended behaviour; criterion 17 asserts the
opposite of the old criterion, deliberately.

### 3.2 D1 — the pool

Each of these blocks on G-1/G-2 (D1.4) landing first.

| # | Case | Required behaviour |
|---|---|---|
| P1 | Cap reached, an evictable instance exists | The **least recently used** instance is closed and the new one starts. No error surfaces to the agent or the operator |
| P2 | An evicted workspace is reopened | It is **still signed in** from its on-disk profile. This is the load-bearing claim that makes eviction acceptable |
| P3 | Cap reached, **every** instance watched **and** busy | The pool starts one extra instance — **exactly one**, at `max_browsers + 1` — and logs a WARN naming the cap and the workspace. A second such request waits, then fails with a named error. The pool never reaches `cap + 2` |
| P4 | k evict-then-reopen cycles inside the window | **Exactly one** WARN, naming the cap, the contending workspaces and the remedy |
| P5 | Idle close (D1.8) | An instance with zero tabs, zero viewers and no call in flight past `idle_close_ttl` is closed by the reaper sweep; its profile survives; the next tool call relaunches it, still signed in, on the **same** `*BrowserManager` |
| P6 | Crash containment (D1.8) | `kill -9` one workspace's Chrome: that workspace recovers from its profile; **every other workspace's manager is not reset and its panel keeps streaming** |
| P7 | `kill -9` the gateway, then boot | Zero orphan Chromes and zero stale markers remain |
| P8 | **Site isolation (D1.6)** | Two cross-site tabs in one workspace occupy **distinct renderer processes** at the shipped `--renderer-process-limit` |
| P9 | **A second live gateway on the same `$OMNIPUS_HOME`** | The first gateway's Chromes **survive**. The second refuses to launch that key and names the other gateway. This is the test that distinguishes "reconcile orphans" from "kill the neighbour" (POSIX only — D1.9) |
| P10 | Workspace deleted | Its profile directory is gone from disk, and no other workspace's profile is touched. Negative cases required: the profile is **present** after idle close, roster change, reload, and crash recovery |
| P11 | Upgrade from an install with the existing global profile (D1.8) | No workspace inherits it. Every workspace starts logged out. `profiles/default/` is still on disk and unused |
| P12 | Boot with N workspaces and the warm defaults on (D1.8) | Exactly **one** instance is warmed — the default agent's resolved workspace — or none, with one INFO |
| P13 | **G-2 itself** | `chrome.tabCapture` succeeds against a **second Chrome's default context**, in the `browser-e2e` job, with a receipt that a skip cannot satisfy |
| P14 | Soft-target documentation (D1.7) | `tools.browser.max_browsers`' config documentation states it is a soft target with a hard ceiling of `+1`, not a hard limit |

### 3.3 D2 — capability

| # | Requirement |
|---|---|
| 6 | An agent can target an element as role + accessible name, on a page whose CSS classes are generated/unstable |
| 7 | Every action tool inherits the same actionability wait. A click on a disabled, moving, or covered element **fails naming which condition was unmet** — not "timeout" |
| 8 | An agent can complete a form containing a `<select>` — impossible today |
| 9 | An agent can press Enter, Tab and Escape as discrete key events |
| 10 | An agent can open a hover-triggered menu |
| 11 | An agent can attach a file to a file input |
| 12 | A page calling `alert()`/`confirm()` does not wedge the session. **The tab must still answer CDP afterwards** — that is the test, not that the dialog was dismissed |
| 13 | An agent can read a page as structure (roles + names + actionable handles) without vision and without already knowing a CSS selector |
| 14 | `browser_navigate` on a `file://` URL returns an error **naming the literal `serve_web`** as the supported route |
| 15 | **Boot survives the new tools (Hard Constraint #6).** A fresh install boots with all six D2 tools registered and **no policy-coverage abort** |
| 16 | **Two writers, one context (D2.10).** Two agents on one workspace issue `browser_navigate` concurrently — **both eventually complete**. Asserting only "neither errors" would pass when nothing happened |
| 20 | **A delegated `Worker` turn calls `browser_upload_file` with no human attached** — it is **denied**, not hung (D2.9, blocked on #659) |
| 21 | **Audit (D2.11).** A write-class browser tool call emits an audit event carrying workspace, agent, tool and host, and that event **renders in Settings → Security → Audit Log** with a name matching `^[a-z_]+$` |
| 22 | **Actionability budget.** The actionability pre-check adds **≤150 ms p95** to a click on an already-actionable element, measured on the `performance-2x` profile §7 uses |

Criteria 7 and 12 are the two whose failure mode is silent or wedging rather
than a wrong answer, and are worth writing first.

---

## 4. Consequences

### D1 — gained

- Switching agents, or opening a new chat, stops silently stranding the browser.
- The tool descriptions become true, so agents stop asserting a false model to
  operators.
- **Cookie/login isolation exists at all** — today it is off by default (D1.3).
- Isolation now survives a reload, because it lives in a profile on disk rather
  than in a CDP context that dies with the process.

### D1 — lost / risked

- **N Chrome processes instead of one.** Bounded by a cap whose default is
  computed per host (D1.5) and by a hard ceiling of `+1` (D1.7). ADR-043's
  "~10 browser-using agents ≈ 1.5–2 GB" sizing is **obsolete**, not "better" —
  it was per agent, unmeasured, and describes a single-Chrome topology that no
  longer exists.
- **Eviction latency is user-visible.** A workspace whose browser was evicted
  pays a cold start on next use. Thrash makes that continuous; D1.7 makes thrash
  visible, it does not remove it.
- **Per-workspace profile disk growth.** N profiles with browser caches inside
  them, on a host whose root volume this project has already filled twice. No
  quota is specified (§6).
- **Site isolation is now a shipped parameter.** `--renderer-process-limit`
  weakens it above its bound; D1.6 makes it a floor rather than a tuning knob,
  which means a small host gets fewer browsers rather than weaker isolation.
- **Crash blast radius shrinks but changes shape.** One workspace's crash no
  longer takes down all browsing (D1.8) — but there are now N processes that can
  crash.
- **Concurrency.** Two agents on one workspace can reach the same tab, and so
  can two concurrent chats. Today they are isolated by accident. D2.10 decides
  the rule; fairness under sustained contention is open (§6).
- **Blast radius in code.** Collapsing per-agent managers touches every browser
  tool call site, the coordinator's single-Chrome lifetime, the live panel's
  session resolution, and three contract schema descriptions (D1.13).
- **The existing test suite encodes the model being replaced.**
  `d2_spike_test.go`, `stress_5agents_test.go`, `tab_adoption_e2e_test.go`,
  `shared_control_test.go` and `tools_control_test.go` all assert the per-agent
  model — `stress_5agents_test.go` asserts five agents share one Chrome PID with
  five isolated CDP contexts, which D1.4 makes false by construction. **A green
  run after this change means nothing unless those five are rewritten**, not
  extended.
- **Adding an agent to a workspace grants it that workspace's logins.** Release
  notes, and the team-editing disclosure (D2.11).
- **Per-tab idle reaping is kept unchanged** (`ReapIdleSessions`,
  `manager.go:2986`, `DefaultIdleTTL` 5 min at `:134`) and is now joined by a
  whole-Chrome idle close (D1.8). Two interactions are new: a viewer attached in
  any one chat pins the whole **workspace's** instance (`:248`), which D1.7
  bounds with a staleness timeout; and the zero-tab branch (`:242-244`) now
  governs a context that outlives every agent that touched it.

### D2 — gained

- An agent can complete a form, not merely read a page (D2.3).
- Failures name a cause an agent can act on rather than "timeout" (D2.2).
- Targeting survives a CSS redesign (D2.1).

### D2 — lost / risked

- **A wedged tab is the new worst case.** A modal blocks every subsequent CDP
  command on that tab; dialog handling that half-works leaves the session unable
  to answer at all. Hence criterion 12.
- **Per-action cost.** Full actionability plus AX-tree resolution adds round
  trips to every click, on a box §7 measures at 85–99% utilisation. Budgeted at
  ≤150 ms p95 (criterion 22).
- **Surface growth: 11 → 17.** The six added are `browser_select_option`,
  `browser_press_key`, `browser_hover`, `browser_upload_file`,
  `browser_handle_dialog` and `browser_snapshot`. Enumerated because both the
  tier assignment (D2.8) and the policy seeding (D2.9) depend on the exact set.
- **A `browser_snapshot` in unattended delegated work is invisible by default**
  while its contents reach a 90-day transcript (D2.11). Recorded, not closed.

---

## 5. Alternatives rejected

**Explicit "hand over the browser" command.** Makes the operator do bookkeeping
for an implementation detail. Fails case 3 outright — at the moment the operator
starts browsing, there is no agent to hand over *from*.

**Keep per-agent browsers, fix only the wording.** Honest, and leaves cases 2
and 3 broken. Rejected as documenting a defect rather than removing it.

**Let `ReconcileTabs` adopt any untracked target.** No ownership model at all,
and it still cannot cross a workspace boundary — so it fixes nothing the design
needs and breaks the guarantee 5b exists to prove. §1.4 has the mechanics.

**One shared cookie jar for the whole install — no partition at all.** Simplest,
and handover becomes trivial. Rejected: a scraping agent's login on one client's
work would surface on another's. The operator confirmed isolation is wanted;
only the axis was wrong. (This is also what ships today by default, per D1.3 —
so "rejected" here means "not carried forward", not "never tried".)

**Keep CDP browser contexts as the primitive and re-key those.** This was the
design until `chrome.tabCapture`'s hard failure against CDP-created contexts was
verified against real Chrome 150 (D1.4). Rejected because an isolated tab that
cannot be streamed to the live panel is not a usable product.

**Replace chromedp with playwright-go (D2).** Would deliver D2.1 and D2.2 for
free. Rejected on Hard Constraint #1 (no new runtime dependency) and on the prior
evaluation's own finding: only those two benefits survived scrutiny, and both are
implementable on chromedp.

**Ship the missing verbs without actionability (D2).** Cheaper and visibly
useful. Rejected: `select_option` and `press_key` on an element that is not yet
enabled fail intermittently, and intermittent failure is the most expensive kind
for an agent — it retries, succeeds sometimes, and learns nothing.

---

## 6. Open questions

Everything below is genuinely undecided. Anything decided lives in D1/D2 and is
not repeated here.

- **How does a policy-denied agent learn a browser exists?** D1.12 withdraws the
  unreachable `ListTabs` third state. The real fix is a system-prompt or
  manifest-note surface telling an agent that the workspace has a browser it is
  not permitted to drive. Until that exists, Mia answers §1.1's question from
  absence. **This is the ADR's own headline defect surviving in a narrower
  form** and it should not be lost.
- **How does an operator see a delegated agent's browser tool calls?** D2.11's
  visibility mitigation does not hold for unattended delegated work
  (`shouldRenderSubagentSpan` → `verboseChatEnabled`, default false). Closing it
  is either a thread-render exception for browser tools inside a span, or
  ActivityPanel work. Both are scoped UI decisions.
- **Is `browser_snapshot` Tier 3?** D2.4 calls it the default way an agent reads
  a page; a search-only default is a contradiction. D2.8 flags it rather than
  guessing.
- **Does the cap count instances or bytes?** D1.7 counts instances; D1.5 budgets
  bytes. A workspace with four heavy renderers and one with a blank tab are one
  instance each and differ by ~1 GB. If the cap is really a memory guard,
  counting instances may be the wrong unit — the same objection D1.5 raises
  about counting tabs.
- **What happens to the single-browser-agent capture fence under a pool?**
  #509 records that today's v1 is *"fenced to effectively single-browser-agent
  use — capture start is denied when another agent has live tabs"*. A pool of N
  browsers changes what that fence means: two workspaces browsing at once is the
  normal case the pool exists to serve, and if the fence is real in the form
  #509 describes, one of them cannot start capture. **NOT YET VERIFIED against
  code.** Searching for it found only an adjacent comment — window focus is *"a
  shared, global resource in the shared-Chrome model"* and stealing it *"would
  fight other agents' captures"* (`pkg/tools/browser/capture_session.go:838-840`)
  — which is about focus contention, not a deny. The precise enforcement path
  was not located. Recorded with that provenance: the fence is neither asserted
  to exist nor asserted not to. Both implementation specs carry the equivalent
  note (commit `df921fd52`); find the path before the pool is built, because the
  answer changes what the pool can deliver.
- **Is the capture session per workspace or per viewer?** `NewCaptureSession`'s
  doc (`capture_session.go:360`) still describes `mgr` as "the **agent's**
  BrowserManager". Under one manager per workspace this determines both the
  renderer budget (D1.5) and whether two operators watching one workspace share
  a stream.
- **Who bounds per-workspace profile disk?** No quota, no reaping of a
  deleted-then-recreated workspace's leftovers.
- **Fairness under sustained contention.** D2.10's lease is per-action and
  first-come with bounded retry. Two agents browsing steadily on one workspace
  will interleave; nothing guarantees fair progress, only eventual progress per
  call.
- **Does the preview URL need re-keying?** `serve_web` mints
  `/preview/<agent>/<token>/` per **agent** while the browsing context is per
  **workspace**. Whether a preview minted by one agent should be reachable from
  a tab another agent drives is not decided; the token is the credential
  (`ADR-044-preview-on-main-listener.md`, FR-023), so this is a security
  question, not a routing one.
- **Should Jim keep `browser_evaluate` under the new sharing model?** Jim holds
  it and Ray does not (`core.go:1058` vs `:910-921`). Under D1.10 his arbitrary
  JS now runs against a browser carrying the operator's logins for every site
  the workspace has visited. The grant predates the sharing model and has not
  been re-examined against it.
- **What binds first on the measured host — memory or CPU?** §7's whole argument
  is that a bound solved a CPU problem on a 2-core box at 85–99% utilisation
  with **one** Chrome. D1.5 multiplies browser processes, GPU processes and
  encoder pages by N on that same class of box, and bounds only memory. Worth
  one measurement before the cap default is chosen.

---

## 7. What this file replaces, and why that work stopped

An earlier draft under this number proposed **region-aware transport**: media
segment forwarding for `<video>`, dirty-rectangle tiles for static UI, and
per-rectangle encoding for canvas. It is deleted rather than superseded, because
it was never ratified — two independent reviews and a test-integrity audit all
declined it as a build authorisation, on the grounds that cheaper fixes had not
been tried.

They were right. On 2026-08-31, `/proc` sampling on `omnipus-uat-swimlane`
(performance-2x, 2 dedicated cores, no GPU) measured, with a viewer attached:

```
idle, no capture                 chrome   0.1% of a core, machine  0.4%
boot warm capture, NO viewer     chrome  57.0% of a core, machine 29.1%
operator watching + interacting  chrome 150-192% of a core, machine 85-99%
```

The machine was full. Video looked fine (one-way media tolerates a busy box)
while input — which is round-trip — was starved: scrolling arrived in bursts,
clicks were dropped, and ICE consent checks missed their deadline. A single
change, capping the capture pixel budget (commit `08d21393`), roughly halved the
warm-capture cost, and the operator's verdict on the next run was "scrolling
video audio is great".

The before/after, from the on-box `/proc` sampler in the same condition (boot
warm capture, no viewer attached, same machine) — **single samples, 33 minutes
apart, on an otherwise unconstrained shared box:**

```
04:21:25  pre-fix    chrome=57.0% of a core   machine=29.1%
04:54:24  post-fix   chrome=29.2% of a core   machine=15.2%
```

The deploy landed between them (`08d21393`, 04:52). Cited in full because commit
`08d21393`'s own message predates the post-fix sample and carries only the
pre-fix row — a reviewer checking the commit alone finds 57.0 and 29.1 side by
side in one row and reasonably concludes the figures were misread. They are two
chrome-column readings 33 minutes apart. This is also the baseline criterion 22
measures against, so its precision matters downstream: n=1 versus n=1.

**The expensive rebuild was for a problem a bound solved.** The research behind
it is not lost — `docs/internal/spikes/browser-streaming/` keeps the measured
evidence (media replication works on YouTube with audio in sync; Safari's AV1 gap
is solvable), which stands on its own and can be reopened under its own ADR if
in-panel media playback ever becomes a requirement.

Recorded so it is not re-derived: the industry has already run this experiment.
Browserbase, Steel.dev and Kernel each built DOM-mirroring session replay and
each replaced it with pixels. Menlo Security patented DOM Mirroring and then
built a compositor display-list engine to replace it. Cloudflare's draw-command
path requires a patched Chromium; Chrome moves to a two-week release cadence on
2026-09-08, and Playwright and Puppeteer both drive Chromium in production with
zero Chromium patches. Nothing above this ADR's scope should be attempted
without re-reading that.

---

## 8. Corrections log

Claims made in this ADR or its commit messages that were later falsified. Kept
because a retracted claim that leaves no trace gets re-derived. **The "Swept"
column names where the body was corrected** — a row with no sweep target is a
row nobody has acted on.

| Claim | Status | Evidence | Swept |
|---|---|---|---|
| "`ReapIdleSessions`' only removal is `delete(m.sessions, sessionID)` — it never disposes a browser" (commit `3667c06a`, twice in the D1 spec marked *verified*) | **FALSE** | It collects `se.browserCancel` into `reapedBrowsers` (`manager.go:3027-3030`), executes the cancels (`:3122-3124`), and reaches `coord.ReleaseTab` via `releaseGlobalTab()`. Whole-Chrome idle close is genuinely new work, but the disposal machinery is not absent | D1.8, §4 |
| "isolation exists (ADR-043 D2); this ADR re-keys it" | **FALSE by default** | `CaptureSharedContext: true` (`pkg/config/defaults.go:671`) makes `Register` return an empty context id and log *"per-agent browser-context isolation is OFF"* (`coordinator.go:349-359`) | Header, D1.3 |
| "Chrome's footprint is 1.15 GB; a second instance costs 400–500 MB" (stated to the operator, 2026-08-31) | **INFLATED 2.6×** | That was RSS, which charges shared program code to every process — 12 times over in that sample. Re-measured **PSS** on the same box, same moment: **434 MB**, not 1118 | D1.5 |
| **The RSS retraction had a downstream consumer that still specifies RSS** | **OPEN** | `browser-workspace-ownership-spec.md`'s G-1 (line 56) mandates *"the marginal **RSS** of the Nth Chrome"* — the metric this log retracted, on the same day, in the same worktree. Its artefact list (SC-012) names `ps`, which cannot produce PSS; `smem` can | §9.1 |
| "the tool is `web_serve`" | **FALSE** | `const ToolNameWebServe = "serve_web"` (`pkg/tools/web_serve.go:46`). Wrong in this ADR, its round-1 review, and root `CLAUDE.md` | D2.5 |
| **And the SPA still dispatches on the retired name** | **OPEN** | `src/components/chat/tools/WebServeUI.tsx:310` registers `makeWebServeUI('web_serve')` and `src/components/chat/ChatScreen.tsx:1037` matches `tc.tool === 'web_serve'`. Doc-only correction; the live defect is unfixed and unfiled | — |
| "`browser_snapshot` inherits `browser_get_text`'s redaction posture" | **FALSE** | `RegisterSensitiveValues` appears zero times in `pkg/tools/browser/` | D2.11 |
| "the ActivityPanel is the durable surface for inspecting a snapshot" | **FALSE** | `useRunningActivity.ts:1-15` aggregates only subagent spans, background `bash` and judge verdicts, capped at 8 (`:148`). A `browser_snapshot` never appears there | D2.11 |
| "ADR-043 D3 is unchanged" | **FALSE** | Three gateway sites resolve a manager by agent id: `browser_webrtc.go:279`, `browser_ws.go:1252`, `browser_inspect.go:73` | Header, D1.13 |
| "ADR-043 D1 is unchanged" | **FALSE** | D1 is one Chrome for the coordinator's whole lifetime. The pool replaces it | Header, D1.4 |
| "N Chromes need N debug ports, blocked by the compiled sandbox allow-list" | **FALSE** | No `--remote-debugging-port` exists; CDP runs over inherited fds (`exec_resolver.go:60`), and the allow-list entry was removed (`sandbox_apply.go:414`) | D1.4 |
| **"Supersedes: nothing"** | **FALSE** | D1.4 is ADR-048's deferred Option B and reverses its ratified "Option A for v1". The previous revision cited ADR-048 once, only for the `tabCapture` evidence, and never named the reversal | Header, D1.3, §9.3 |
| **"#509 is the tracking issue this ADR closes"** (stated in the first draft of this consolidation) | **FALSE as written** | #509 is **closed, `NOT_PLANNED`, since 2026-08-19** — so there is nothing open to close. This ADR revives a declined option, which is a different and more visible act than completing a tracked one | §9.2 |
| **"Unattended delegated work does NOT share the jar"** (operator ruling, 2026-08-31) | **SUPERSEDED the same day** | The superseding ruling (D1.10) is that every agent on a workspace shares its browser, including unattended delegated work. Both statements stood in the document at once, in two sections, with the same date and decider | D1.10 |
| **G-2 "is guarded by a test that skips unless `OMNIPUS_BROWSER_E2E=1` is set, and a skip reports green"** | **FALSE — and it is a §8-pattern error inside the note about the §8 pattern** | `OMNIPUS_BROWSER_E2E: "1"` **is** set, in a dedicated `browser-e2e` job (`.github/workflows/pr.yml:416`), and `:469` already fails the job if **either** skip path fires — `skipIfNoBrowser`'s CI branch (`browser_e2e_test.go:66-67`) or `resolveTestBinary`'s no-Chrome skip (`coordinator_test.go:68`). The CI gate is real. **The actual gap is simpler and worse: the test does not exist.** Nothing exercises a second Chrome instance | D1.4 (G-2), §3.2 P13 |

**The pattern is worth naming:** almost every entry above is a claim asserted
from a plausible reading and not tested. Most were caught only because a reviewer
or spec-writer re-derived them from source. The one that cost most (isolation
being off by default) was invisible for the entire first half of the design, and
would have shipped acceptance criteria that pass over a product with no isolation
at all.

**And a second pattern, from this consolidation:** a corrections log that nobody
sweeps reproduces the failure it records. Three of the rows above were still
asserted as true elsewhere in this file when round 2 reviewed it, one of them on
line 11. That is why this table now has a "Swept" column, and why a row without
one is an open item rather than a historical note.

---

## 9. Downstream documents that must change

Recorded so the conflicts are tracked rather than discovered by whoever
implements from the wrong document. **These are not edited by this ADR.**

### 9.1 `docs/internal/specs/browser-workspace-ownership-spec.md`

Marked "Draft for implementation… all design questions are decided", written the
same day in the same worktree, and it **specifies the opposite cap policy to
D1.7**. Whichever document an implementer picks up, the other is wrong, and both
read as authoritative. D1.7's operator ruling is the current one; the spec is the
document that moved. Specifically:

| Spec location | Says | Must become |
|---|---|---|
| FR-039, line 135, line 330, line 633, US-15/AC1–AC4, scenarios at 910 and 953 | **Refusal** at the cap; *"the pool NEVER evicts a live browser"*; `errBrowserPoolFull` and its error literal *"Close another workspace's browser"* | **LRU eviction** (D1.7), no error surface, no `errBrowserPoolFull` |
| FR-046, line 137, line 562, line 563, US-18 | An operator-facing close action — `POST /api/v1/workspaces/{id}/browser/close` plus a SPA control | **Withdrawn** (D1.7). This also removes the spec's single `contracts/openapi.yaml` path addition and restores SC-007's original "no `contracts/` diff outside `description:`" condition |
| Invariant P-2 (line 479), `len(live) <= cap` | A hard bound | **Soft target with a hard ceiling of `cap + 1`** (D1.7) |
| G-1 (line 56) and SC-012's artefact list | Measure the marginal **RSS**; artefacts from `ps` | **PSS**, from `smem` (§8 row 3, D1.4) |
| Absent throughout | *"thrash"*, *"LRU"*, *"least recently"*, *"cgroup"*, *"renderer-process-limit"*, *"85 MB"*, *"FIXED_FLOOR"* appear **zero** times | Thrash detection (D1.7), the renderer floor (D1.6) and the sizing terms (D1.5) must be represented |

Where the spec goes **further** than the ADR and does not conflict — FR-040a
(idle close), FR-041 (crash containment), FR-042a (boot marker reconciliation),
FR-043a (profile deletion), FR-016b (boot warm) — D1.8 has been written to match
it, so those need no change. The one exception is FR-042a's *"live
omnipus-owned pid ⇒ terminate it"*: D1.8 makes the **per-key launch lock**, not
the marker's pid, the discriminator between an orphan and a second live
gateway's browser, because a live Chrome pid is present in both cases.

### 9.2 Issue #509 — reopen now, close when this lands

**[#509](https://github.com/elicify-ai/omnipus/issues/509) — "Per-agent browser
isolation compatible with WebRTC capture (ADR-048 Option B)" — is CLOSED, reason
`NOT_PLANNED`, since 2026-08-19.** Its body asks for *"one Chrome instance (own
user-data-dir) per browser-capable agent, so each agent's tabs sit in that
instance's own default context (capturable) while remaining isolated from other
agents"*.

**That is D1.4**, with *workspace* where #509 says *agent*. This ADR therefore
revives an option the project deliberately declined, and that must be visible in
the tracker as well as in this document.

Two actions, both required:

1. **Reopen #509 now** — or file a successor that references it — so the
   reversal is trackable while the work is built. Leaving it closed while
   building exactly what it asked for makes the tracker wrong in one direction.
2. **Close #509 when this lands**, citing the implementing PR. Leaving it
   closed-as-`NOT_PLANNED` after shipping it makes the tracker wrong in the
   other direction: the next person to ask "did we ever do per-instance browser
   isolation?" reads "not planned" and rebuilds the analysis.

#509 also supplies two facts this ADR had not recorded, both now folded in:
the second, more final reason CDP contexts cannot host capture (D1.4), and the
existing single-browser-agent capture fence, which the pool must reconcile with
and which is **not yet verified against code** (§6).

### 9.3 `ADR-048-live-browser-capture-default-context.md`

Its header still reads *"Option A ratified for v1"* with Option B *"tracked for
later in #509"*. It must be amended in place to record that ADR-072 D1.4 adopts
Option B at the workspace axis — the same disclosure-in-both-places treatment
ADR-047 D2 received when ADR-048 amended it.

### 9.4 Root `CLAUDE.md`

Line 266 still names the tool `web_serve`. The registered name is `serve_web`
(§8).
