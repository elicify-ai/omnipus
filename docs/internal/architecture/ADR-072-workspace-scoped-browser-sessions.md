# ADR-072 — Browser tools: conversation-scoped, and usable by an agent

- **Status:** Proposed
- **Date:** 2026-08-31
- **Supersedes:** nothing. **Replaces** an earlier, unpushed draft that also
  claimed ADR-072 ("Region-aware transport for the live browser") — deleted in
  the same commit as this file lands. §7 records why.
- **Amends:** **[[ADR-043]]** (one shared Chrome + per-agent browser contexts,
  Accepted 2026-07-14). ADR-043's D2 — "the load-bearing decision" — keys each
  CDP browser context by **agent**. This ADR **re-keys it to the conversation**.
  The isolation primitive, its strength and its implementation are unchanged;
  only the key changes. D1.0 states exactly what is preserved and what is not.
- **Related:** ADR-038 (live browser panel, human takeover), ADR-041 (tabs),
  ADR-044 (single listener), ADR-057 (routing vs transcript session split),
  ADR-062 (WebRTC connectivity tiers)

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

The sequence that produced it: the operator opened the chat with Mia (the
default agent), opened the browser panel, browsed — so the tab was recorded
against Mia — then switched the chat to Jim. Nothing moved the tab.

### 1.2 What the code actually does

> **Read D1.0 with this section.** What follows explains the *mechanism* of the
> reported defect. The per-agent split is deliberate (ADR-043 D2, Accepted) and
> provides real cookie isolation; this ADR re-keys it, it does not delete it.


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
manager tracks only the ones it created. The live panel confirms the binding
in its own log prefix: `browser-webrtc[mia]`.

Jim was not looking at an empty shared browser. He was looking at his own
empty book, in a library that had the page open on the next desk.

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
value. Nothing downstream can distinguish them, so no agent — and no test —
can notice the difference. This is the `docs/internal/false-green-patterns.md`
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
tabs. It cannot adopt another agent's tab, and relaxing its
`len(se.tabs) == 0` precondition would not make it able to. Recorded here
because that precondition looks like the fix and isn't.

---

## 2. Decision

Two decision areas. **D1** fixes who owns the browser; **D2** fixes what an
agent can do with it. They ship independently and are separated here so each
can be reviewed on its own — D1 is an ownership change with real concurrency
consequences, D2 is additive surface.

---

## D1 — Ownership

**A browsing context belongs to a conversation. Isolation is preserved in full
— it moves from the agent axis to the conversation axis.**

### D1.0 Reconciling with ADR-043 — read this before §1.2

§1.2 below describes the per-agent split as the mechanism behind the reported
defect. That is accurate but incomplete, and on its own it reads as though the
split were an accident. **It is not.** ADR-043 D2 chose it deliberately, called
it "the load-bearing decision", and recorded operator confirmation that
"cookie/login isolation per agent is sufficient". It is implemented and working
— `pkg/tools/browser/coordinator.go:105` holds a `cdp.BrowserContextID` per
agent, created by raw CDP `CreateBrowserContext`, with `d2_spike_test.go`
covering it.

So this ADR is not removing isolation. It argues the **axis is wrong**.

| | ADR-043 | This ADR |
|---|---|---|
| Isolation primitive | CDP browser context | **unchanged** |
| Partitions cookies / localStorage / indexedDB | yes | **unchanged** |
| Keyed by | the **agent** | the **conversation** |

**What ADR-043 was protecting still holds.** Its concern is that one agent's
logged-in state must not leak into another's unrelated work. Under
conversation-keying a login obtained in chat X is invisible in chat Y — which
is the same protection, expressed against the boundary a human actually
recognises.

**Why the agent is the wrong unit.** The human logs in first and *then* decides
who to talk to. Case 3 is not an edge case; it is the ordinary way a person
uses a shared browser. Keying on the agent makes the browser's contents depend
on which colleague happened to be on screen at the moment a tab was opened —
an implementation detail the operator has no reason to model, and, per §1.1,
one the agents themselves misreport.

**The behaviour change this accepts, stated plainly:** one agent present in two
conversations now has **two cookie jars** — logged in to a site in one, logged
out in the other. This follows directly from isolation tracking the human's
session, and it is a deliberate consequence, not an oversight.

**What does not change:** ADR-040's take-the-wheel model within a context;
ADR-041's tab-set model; ADR-043 D1/D3/D4 (single Chrome, coordinator
ownership, hot-reload survival) and its whole deferred escape hatch.

### D1.1 The two changes

1. **One `BrowserManager` per workspace**, shared by every agent, instead of
   one per agent. The manager stops being the isolation boundary; the browser
   context keyed by conversation becomes it.
2. **Key browsing contexts by the routing session id** — the conversation —
   instead of the constant `"default"`.

### 2.1 The keys already exist and already mean the right thing

Tool context already carries both, and ADR-057 already gave them exactly the
semantics this needs — a delegated sub-turn **inherits** `routingSessionID`
and gets its **own** `transcriptSessionID`:

| Key | Helper | Scope it produces |
|---|---|---|
| Routing session | `tools.ToolSessionKey(ctx)` | The conversation. Survives switching which agent answers, and is inherited by delegated sub-turns. |
| Transcript session | `tools.ToolTranscriptSessionID(ctx)` | One turn/delegation. Distinct per delegated child. |

So the **operator-facing browser is keyed by the routing session**, and
unattended delegated work that must not touch the operator's tab can be keyed
by the transcript session instead. No new identity concept is introduced.

Every browser tool already takes a session id on every call: `defaultSessionID`
is passed at **9 call sites in `tools.go`** and **14 across
`pkg/tools/browser/`** (non-test), and `tabs.go` passes it to `ListTabs`.
The parameter is threaded, wired and currently wasted on a constant.

Six tool descriptions also contain the literal phrase "the shared browser
session" and must change with the behaviour, not after it.

### 2.2 Fallback when there is no conversation

A browser call outside any chat (a scheduled trigger, a heartbeat, a CLI
invocation) has no routing session. Those keep a context keyed by the constant,
as today. That path must be explicit, not an accidental empty string —
an empty key silently colliding every unattended run into one shared browsing
context is the same class of defect this ADR exists to remove.

### 2.3 The silent zero is removed, independently

`ListTabs` must distinguish "this conversation has no browsing context yet"
from "the browsing context has no tabs". Both are legitimate states; reporting
them identically is what made §1.1 unobservable. The tool's description must
also stop claiming "shared" unless and until it is true.

---

## D2 — Capability

**The agent-facing tool surface is completed: how an agent finds an element,
when it is safe to act on it, and the verbs it can use.**

### D2.0 What we ship today

Eleven tools: `browser_navigate`, `browser_click`, `browser_type`,
`browser_get_text`, `browser_screenshot`, `browser_evaluate`, `browser_wait`,
and the four tab tools (`list`/`switch`/`open`/`close`).

A prior playwright-go evaluation (cited in issue #569) concluded that swapping
the engine was not worth it, and that **role selectors and auto-waiting were
the only benefits that survived scrutiny — both implementable on chromedp**.
This ADR takes that conclusion and adds what a survey of the actual tool
surface shows is also missing.

### D2.1 Element selection by role and accessible name — issue #569

Today an agent resolves a target by **CSS selector** or by our own visible-text
matcher (`pkg/tools/browser/text_selector.go` — `resolveTextTarget`,
`has-text`/`text-is`, `data-omnipus-tsel`). The CDP Accessibility domain is
unused: `getFullAXTree` / `queryAXTree` appear nowhere in `pkg/` or `src/`.

An LLM reasons about a page as *"the Submit button"*, i.e. role + accessible
name. Making it translate that into CSS is where avoidable action failures and
retries come from, and CSS is the least stable thing on a page that A/B tests.

**Decision:** add role + accessible-name selection alongside CSS and text
(additive, not a replacement), sourced from the CDP Accessibility domain
rather than a reimplemented accname computation, exposed through the same
target-resolution seam the text selectors already use so every action tool
inherits it. Deterministic ordering plus an index affordance on multi-match,
mirroring the existing text-selector behaviour.

### D2.2 Actionability — currently one quarter built, unfiled

Playwright waits for an element to be **visible, stable, enabled and
hit-testable** before acting. We do the first only:
`tools.go:257` (click) and `:461` (type) prepend `chromedp.WaitVisible`;
`:685` uses `WaitReady` for text. Nothing checks enabled-ness, nothing checks
that the element has stopped moving, nothing checks that the click will
actually land on it rather than on an overlay.

Issue #569 says this deserves its own issue. **It was never filed** — verified
against all open and closed issues. It is folded in here.

**Decision:** complete the actionability contract in the shared pre-action
path, so every action tool inherits it: visible → stable (two consecutive
identical bounding boxes) → enabled → hit-testable (the element is what a
click at that point would reach). On timeout the failure must name **which**
condition was not met — "not clickable, covered by another element" is
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
| Dialog handling | A page calling `alert()`/`confirm()` blocks the whole session with no way out |

`browser_type` covers text entry but not discrete key events; the two are not
substitutes.

**Decision:** add the four tools and dialog handling. Each is small
individually; together they are the difference between an agent that can read
a page and one that can complete a form.

**Dialog handling carries a specific hazard** worth stating: a modal dialog
blocks every subsequent CDP command on that tab. Whatever is built must
guarantee the session cannot be left wedged — the failure mode is not a bad
result, it is a browser that stops answering.

### D2.4 Reading a page as structure

An agent reads a page today via `browser_screenshot` (pixels, needing vision)
or `browser_get_text` (requires already knowing a CSS selector). Neither gives
the structure an agent needs to decide **what is on the page and what it can
do next**.

**Decision:** add an accessibility-tree snapshot — roles, accessible names,
and the handles needed to act on them — as the default way an agent reads a
page. This shares its source with D2.1: the same AX tree that answers "what is
here" answers "which node did you mean". Build them together or the second one
is built twice.

### D2.5 Telling the agent the supported route — issue #242

`browser_navigate` rejects `file://` deliberately (`manager.go:673-681` —
"file:// would bypass Landlock restrictions"). The agent is told only:

> `browser: file:// URLs are blocked for security reasons`

That is a dead end. The supported route exists one tool away — `web_serve`
mints a `/preview/<agent>/<token>/` http URL that `browser_navigate` accepts —
and nothing in the tool surface mentions it. `grep` for `file://`, `web_serve`
or `preview` in `tools.go` returns nothing.

**Decision:** name the supported route in **the error message**, not only in
the tool description. The error is the moment the agent needs the answer; a
description read hundreds of tokens earlier is not.

### D2.6 Explicitly out of scope

- **Replacing chromedp with playwright-go.** The prior evaluation found only
  two benefits worth having, and D2.1/D2.2 deliver both without an engine swap
  or a new runtime dependency (Hard Constraint #1).
- **Network interception, frame targeting, drag-and-drop, cookie/storage
  manipulation.** Real Playwright features, no demonstrated agent need yet.
  Deliberately deferred rather than forgotten.

### D2.7 Capability gating — issue #456 is closed as redundant

#456 asked that `browser_*` be gated out of the manifest when no Chromium is
available. **Chromium is not fetched on demand in any shipping configuration:**
the Linux and macOS release archives bundle it (`scripts/install.sh` verifies
`chrome.sha256` and aborts the install on mismatch), and
`docker/Dockerfile.heavy` bakes Chrome-for-Testing in at build time. The
managed download in `exec_resolver.go` is step 4 of the resolver ladder — the
fallback for bare-binary and hand-built installs, not the normal path.

ADR-071 independently removed most of the cost the issue cited: all twelve
`browser_*` tools are now Tier 3 / search-only, so they occupy no line in the
compressed manifest.

**Decision: close #456.** A gate for a state that shipping installs do not
enter is machinery without a payer. Closed 2026-08-31.

**The one real exception, recorded so it is not rediscovered as a surprise:**
linux/arm64 archives carry no `chromium/` payload, because Chrome-for-Testing
publishes no linux-arm64 build (`scripts/install.sh:26-29`) — and the managed
download cannot rescue it for the same reason. Those hosts depend on a system
Chrome, which is itself gated behind `TrustPathChrome` (seeded `false`). The
managed download cannot rescue it either — it fetches from the same upstream
that has no linux-arm64 build. This wants a build-and-distribution answer,
not a manifest gate, and is filed separately as **#665**.

---

## 3. Acceptance criteria

The operator's three cases, which are the test plan:

| # | Case | Required behaviour |
|---|---|---|
| 1 | Mia browses; operator watches and takes over | Works as today — unchanged |
| 2 | Operator switches the chat from Mia to Jim mid-session | Jim sees and drives the same tabs. No handover step, no command |
| 3 | Operator browses first, **then** asks an agent to take over | The tab was never owned by "whoever happened to be on screen"; any agent in that conversation sees it |
| 4 | An agent delegates unattended background browsing | The sub-agent does not hijack the tab the operator is reading |
| 5 | `browser_list_tabs` with no browsing context | Says so. Must not be indistinguishable from an empty tab set |
| 5b | **Isolation survives the re-key (ADR-043 D2).** Log in to a site in conversation X; open the same site in conversation Y | Y is **logged out**. This is the amended ADR-043 guarantee and the test that proves isolation was moved rather than dropped |

Case 2 and case 3 are the ones broken today; 1 works; 4 and 5 are the
regressions this design must not introduce.

### D2 — capability

| # | Requirement |
|---|---|
| 6 | An agent can target an element as role + accessible name, on a page whose CSS classes are generated/unstable |
| 7 | Every action tool inherits the same actionability wait. A click on a disabled, moving, or covered element **fails with which condition was unmet** — not "timeout" |
| 8 | An agent can complete a form containing a `<select>` — impossible today |
| 9 | An agent can press Enter, Tab and Escape as discrete key events |
| 10 | An agent can open a hover-triggered menu |
| 11 | An agent can attach a file to a file input |
| 12 | A page calling `alert()`/`confirm()` does not wedge the session. **The tab must still answer CDP afterwards** — this is the acceptance test, not that the dialog was dismissed |
| 13 | An agent can read a page as structure (roles + names + actionable handles) without vision and without already knowing a CSS selector |
| 14 | `browser_navigate` on a `file://` URL returns an error **naming `web_serve` as the supported route** |

Criteria 7 and 12 are the two whose failure mode is silent or wedging rather
than a wrong answer, and are the ones worth writing tests for first.

---

## 4. Consequences

**Gained**

- Switching agents mid-conversation stops silently stranding the browser.
- The tool descriptions become true, so agents stop asserting a false model to
  operators.
- One less per-agent object; the shared Chrome gains a single owner of record.

**Lost / risked**

- **Concurrency.** Two agents in one conversation can now reach the same tab.
  Today they are isolated by accident. `controlledResult` already arbitrates
  human-vs-agent control (ADR-038 D6); agent-vs-agent within one conversation
  is a case it was not written for and must be re-read against this change.
- **Blast radius.** Collapsing per-agent managers touches every browser tool
  call site and the live panel's session resolution.
- **Idle reaping.** Session lifetime becomes conversation lifetime, not agent
  lifetime. Reaping rules need re-deriving, or a long chat pins a Chrome tab
  indefinitely.
- **A behaviour change operators will notice (D1.0).** One agent in two
  conversations gets two cookie jars. Someone will report this as a bug —
  "Jim was logged in yesterday and isn't today" — so it needs to be in the
  release notes, not only in this ADR.
- **Context count scales with conversations, not agents.** ADR-043 sized the
  hybrid at ~10 browser-using agents (≈1.5–2 GB). Conversations are unbounded
  and longer-lived, so the reaping rule above stops being housekeeping and
  becomes the thing that keeps memory finite.

**D2 — gained**

- An agent can complete a form, not merely read a page (D2.3).
- Failures name a cause an agent can act on rather than "timeout" (D2.2).
- Targeting survives a CSS redesign (D2.1).

**D2 — lost / risked**

- **A wedged tab is the new worst case.** A modal dialog blocks every
  subsequent CDP command on that tab; dialog handling that half-works leaves
  the session unable to answer at all. This is why criterion 12 tests that the
  tab still responds, not that the dialog was dismissed.
- **Per-action cost.** Full actionability (stability needs two consecutive
  bounding-box reads) plus AX-tree resolution adds round trips to every click.
  It must not turn a fast click into a slow one on pages that were fine.
- **Surface growth.** Eleven tools become sixteen-ish. ADR-071 puts them all in
  Tier 3 / search-only, so the manifest cost is bounded — but discoverability
  now depends entirely on tool search returning the right verb.

---

## 5. Alternatives rejected

**Explicit "hand over the browser" command.** Makes the operator do bookkeeping
for an implementation detail. Fails case 3 outright — at the moment the
operator starts browsing, there is no agent to hand over *from*.

**Keep per-agent browsers, fix only the wording.** Honest, and leaves cases 2
and 3 broken. Rejected as documenting a defect rather than removing it.

**Let `ReconcileTabs` adopt any untracked target.** Would make every agent
silently absorb every other agent's tabs, including unattended delegated ones,
with no ownership model at all. §1.4 has the mechanics.

**Drop isolation entirely — one browser context for the workspace.** Simplest,
and it would make handover trivial. Rejected: it discards ADR-043 D2 rather
than amending it, and a scraping agent's login in one conversation would
surface in an unrelated one. The operator confirmed isolation was wanted; only
the axis was wrong.

**Replace chromedp with playwright-go (D2).** Would deliver D2.1 and D2.2 for
free. Rejected on Hard Constraint #1 (no new runtime dependency) and on the
prior playwright-go evaluation's own finding: only those two benefits survived
scrutiny, and both are implementable on chromedp.

**Ship the missing verbs without actionability (D2).** Cheaper and visibly
useful. Rejected: `select_option` and `press_key` on an element that is not yet
enabled fail intermittently, and intermittent failure is the most expensive
kind for an agent — it retries, succeeds sometimes, and learns nothing.

---

## 6. Open questions

- Does `controlledResult` need an agent-vs-agent arm, or is "one conversation,
  one driver" sufficient?
- Should the live panel's WebRTC session (`browser-webrtc[<agent>]`) also
  re-key to the conversation, or is the agent label harmless once tabs are
  conversation-scoped?
- Reaping: what ends a conversation's browsing context?

---

## 7. What this file replaces, and why that work stopped

An earlier draft under this number proposed **region-aware transport**: media
segment forwarding for `<video>`, dirty-rectangle tiles for static UI, and
per-rectangle encoding for canvas. It is deleted rather than superseded,
because it was never ratified — two independent reviews and a test-integrity
audit all declined it as a build authorisation, on the grounds that cheaper
fixes had not been tried.

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
change, capping the capture pixel budget (commit `08d21393`), halved the
warm-capture cost (57% → 29% of a core in the identical condition) and the
operator's verdict on the next run was "scrolling video audio is great".

**The expensive rebuild was for a problem a bound solved.** The research
behind it is not lost — `docs/internal/spikes/browser-streaming/` keeps the
measured evidence (media replication works on YouTube with audio in sync;
Safari's AV1 gap is solvable), which stands on its own and can be reopened
under its own ADR if in-panel media playback ever becomes a requirement.

Recorded so it is not re-derived: the industry has already run this
experiment. Browserbase, Steel.dev and Kernel each built DOM-mirroring session
replay and each replaced it with pixels. Menlo Security patented DOM Mirroring
and then built a compositor display-list engine to replace it. Cloudflare's
draw-command path requires a patched Chromium; Chrome moves to a two-week
release cadence on 2026-09-08, and Playwright and Puppeteer both drive
Chromium in production with zero Chromium patches. Nothing above this ADR's
scope should be attempted without re-reading that.
