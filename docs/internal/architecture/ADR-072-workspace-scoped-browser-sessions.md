# ADR-072 — Browser tools: workspace-scoped, and usable by an agent

- **Status:** Proposed
- **Date:** 2026-08-31
- **Supersedes:** nothing. **Replaces** an earlier, unpushed draft that also
  claimed ADR-072 ("Region-aware transport for the live browser") — deleted in
  the same commit as this file lands. §7 records why.
- **Amends:** **[[ADR-043]]** (one shared Chrome + per-agent browser contexts,
  Accepted 2026-07-14). ADR-043's D2 — "the load-bearing decision" — keys each
  CDP browser context by **agent**. This ADR **re-keys it to the workspace**.
  The isolation primitive, its strength and its implementation are unchanged;
  only the key changes. D1.0 states exactly what is preserved and what is not.
- **Related:** ADR-038 (live browser panel + take-the-wheel, D6), ADR-041 (tabs),
  ADR-057 (routing vs transcript session split),
  ADR-069 (universal live-browser connectivity)

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
can be reviewed on its own. (Review round 1 recommended splitting them into two
ADRs, on the grounds that D2 is uncontested and D1 is not. **Operator ruling
2026-08-31: keep as one document.** The split-shipping property is preserved by
the D1/D2 separation itself.) — D1 is an ownership change with real concurrency
consequences, D2 is additive surface.

---

## D1 — Ownership

**Signed-in state belongs to the workspace. Isolation is preserved in full — it
moves from the agent axis to the workspace axis.**

### D1.0 Reconciling with ADR-043 — read this alongside §1.2 (above)

§1.2 above describes the per-agent split as the mechanism behind the reported
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
| Keyed by | the **agent** | the **workspace** |

**What ADR-043 was protecting still holds — and is better expressed.** Its
concern is that one agent's logged-in state must not leak into unrelated work.
Under workspace-keying, a login obtained in workspace X is invisible in
workspace Y. That is the same protection drawn at the boundary that actually
separates unrelated work: different clients, different projects.

**This ADR overrides a named ADR-043 limitation, deliberately.** ADR-043's
"Accepted limitations" says: *"If an operator specifically wants agents to
share a login (e.g. Jim logs in, Ray reuses it), that becomes a deliberate
per-context opt-in (future), not the default."* D1 makes exactly that the
default within a workspace. The authority is the operator ruling of
2026-08-31. Stated explicitly because an amendment that silently reverses a
named limitation of the ADR it amends is how the next reader concludes the
reversal was accidental.

**Why the agent is the wrong unit.** The human logs in first and *then* decides
who to talk to. Case 3 (§3, case 3) is not an edge case; it is the ordinary way a person
uses a shared browser. Keying on the agent makes the browser's contents depend
on which colleague happened to be on screen at the moment a tab was opened —
an implementation detail the operator has no reason to model, and, per §1.1,
one the agents themselves misreport.

**Why the workspace and not the conversation.** An earlier revision of this ADR
keyed on the conversation. That fixed handover but produced a surprise: the
same agent, asked the same thing in a new chat, would be logged out — because
the login stayed behind in the old one. The workspace removes it. A
conversation is short and a project produces many of them; **the workspace *is*
the unit of related work**, so two agents on one team sharing a login is
correct behaviour rather than leakage. Operator ruling, 2026-08-31.

**Both properties therefore hold, and no surprise is accepted:**

| | Handover works | No leak between unrelated work | No surprise logout |
|---|---|---|---|
| Per agent (today) | ✗ | ✓ | ✓ |
| Per conversation (earlier draft) | ✓ | ✓ | ✗ |
| **Per workspace (this ADR)** | **✓** | **✓** | **✓** |

**Two consequences that must be stated rather than discovered:**

1. **Every agent on a workspace inherits every signed-in session on it.** Adding
   an agent to a team grants it those logins. That follows from the team being
   the trust boundary, and it is the same judgement an operator already makes
   when choosing a roster — but it is a grant, and should read as one.
2. **Unattended delegated work does NOT share the jar.** Operator ruling
   (2026-08-31): a background agent starts signed out, so it cannot act as the
   operator on a live site with no human present — no purchase, post or message
   sent as them by a process nobody is watching. The cost is accepted: a
   background agent cannot reach a site behind a login, and the operator asks it
   in a normal chat instead. D1.2 is therefore a **default**, not an escape
   hatch.

**What does not change:** ADR-038 D6's take-the-wheel model within a context (**not** ADR-040 — that
citation is wrong in ADR-043's header too and is corrected here so it stops
propagating);
ADR-041's tab-set model; ADR-043 D1/D3/D4 (single Chrome, coordinator
ownership, hot-reload survival) and its whole deferred escape hatch.

#### D1.0a — the isolation being re-keyed is OFF by default. Read this first.

Found during spec grilling, 2026-08-31. **Everything above describes moving an
isolation boundary from the agent axis to the workspace axis. On a default
install there is no boundary to move.**

- `pkg/config/defaults.go:671` seeds **`CaptureSharedContext: true`**.
- In that mode `BrowserCoordinator.Register`
  (`pkg/tools/browser/coordinator.go:349-359`) returns an **empty**
  `browserCtxID` and logs its own warning verbatim: *"shared default-context
  capture mode is ON (tools.browser.capture_shared_context) — per-agent
  browser-context isolation is OFF"*.
- So ADR-043 D2's per-agent CDP browser context — "the load-bearing decision",
  the thing D1.0 above says is preserved and merely re-keyed — **does not exist
  on a fresh install today.**

The word `capture_shared_context` appeared **zero** times in this ADR and in
both specs before this note.

**What this does and does not change.**

- It does **not** change the direction. Workspace-keyed is still the right
  axis, and the operator ruling stands.
- It **does** change the claim. D1 is not "preserve isolation, move its key".
  It is "**turn the isolation on, and key it by workspace**". The acceptance
  criteria are affected: 5b (log in on workspace X, be logged out on Y) is
  **unsatisfiable on a default install** until this is resolved, and would have
  been written as a passing test over a product that has no isolation at all.

**And the documented escape hatch no longer exists.** `defaults.go`'s own
comment says an operator "who needs real cross-agent cookie isolation can set
this false; **the JPEG browser_screencast fallback keeps working either way**".
ADR-061 deleted the JPEG screencast path in full, with a CI guard
(`scripts/check-no-jpeg-screencast.sh`) to stop it returning. So the comment
directs an operator to a trade-off that is no longer available: **today you can
have cross-agent cookie isolation, or a live video panel, but not both.**

**Consequence for this ADR:** D1 must decide the default, not inherit it. The
options are to flip `CaptureSharedContext` to false (restoring isolation and
losing capture in whatever way ADR-047/061 left it), to make WebRTC capture work
against a non-default browser context, or to state plainly that isolation is
opt-in and correct criterion 5b accordingly. **This is an operator decision and
is not taken here.** The stale comment is a separate defect and is filed
independently.

### D1.1 The two changes

1. **One `BrowserManager` per workspace**, shared by every agent, instead of
   one per agent. The manager stops being the isolation boundary; the browser
   context keyed by workspace becomes it.
2. **Key browsing contexts by the workspace id** instead of the constant
   `"default"`. `tools.ToolWorkspaceID(ctx)` already carries it to every tool
   (`pkg/tools/base.go:241-251`), so this needs no new plumbing — the same
   situation as the session keys in D1.3.

### D1.2 Unattended delegated work gets its own context

**Workspace-keyed is the default for every operator-facing turn. A delegated
sub-turn with no human attached is keyed by its transcript session id
(`tools.ToolTranscriptSessionID`) instead, and therefore starts signed out.**

"Unattended" is defined by the mechanism that already distinguishes them:
ADR-057 gives a delegated sub-turn its own `transcriptSessionID` while it
inherits the parent's `routingSessionID`. A sub-turn is unattended when it is
running under `spawnSubTurn` and no viewer is attached to the workspace's live
panel.

Trade-off, accepted: a background agent asked to check something behind a
login will fail rather than silently act as the operator. That failure must
name the reason ("this ran unattended and has no signed-in session"), or it
becomes the class of invisible failure this ADR exists to remove.

#### D1.2a — two structural changes this requires, which an earlier revision missed

Spec drafting (2026-08-31) found that "keyed by transcript session id,
therefore signed out" **does not follow from the code as it stands**. Recorded
here because the failure mode is silent: without both changes below, the
unattended sub-turn is fully signed in and every obvious test still passes.

1. **`BrowserManager.browserCtxID` is a single field** (`manager.go:381`),
   applied to every session the manager bootstraps via
   `chromedp.WithExistingBrowserContext(m.browserCtxID)` (`manager.go:1369`).
   A second entry in `m.sessions` under a transcript key therefore reuses the
   **same CDP browser context** — same cookies. **Required:** the single field
   becomes a per-key map, and the unattended key gets its own
   coordinator-owned browser context. This is a structural change, not
   configuration.

2. **There is no discriminator for "unattended".** `spawnSubTurn` *inherits*
   the parent's workspace (`pkg/agent/subturn.go:1323`,
   `WorkspaceID: parentTS.opts.WorkspaceID`), so a delegated child lands in the
   parent's jar by default. `ToolDelegationDepth` is not the signal — it is set
   only by `task_executor.go`, and is 0 for a `delegate`-spawned sub-turn. There
   is also no viewer-count accessor for the "no viewer attached" half of the
   definition. **Required:** both are new seams.

This corrects D1.3's claim that "no new identity concept is introduced". The
two *keys* exist and already reach every tool; the *discriminator* between
attended and unattended does not, and must be built.

### D1.3 The keys already exist and already mean the right thing

Tool context already carries both, and ADR-057 already gave them exactly the
semantics this needs — a delegated sub-turn **inherits** `routingSessionID`
and gets its **own** `transcriptSessionID`:

| Key | Helper | Scope it produces | Used for |
|---|---|---|---|
| **Workspace** | `tools.ToolWorkspaceID(ctx)` | The team space. Survives switching agent AND switching chat. | **The browsing context. This is the decision.** |
| Transcript session | `tools.ToolTranscriptSessionID(ctx)` | One turn/delegation. Distinct per delegated child. | Unattended delegated work — D1.2 |

The routing session id (`tools.ToolSessionKey`) is **not** used as a browsing
key. An earlier revision of this ADR chose it; D1.0 records why that was
replaced. It is listed here only so a reader who finds it in the git history
knows it was considered and rejected, not overlooked.

Both keys already exist and already reach every tool
(`pkg/tools/base.go:243-252`). **But see D1.2a:** the *discriminator* between
an attended and an unattended turn does not exist and must be built, and the
manager's single browser-context field must become per-key. An earlier
revision of this section claimed "no new identity concept is introduced",
which was wrong.

Every browser tool already takes a session id on every call: `defaultSessionID`
is passed at **9 call sites in `tools.go`** and **14 across
`pkg/tools/browser/`** (non-test), and `tabs.go` passes it to `ListTabs`.
The parameter is threaded, wired and currently wasted on a constant.

Six tool descriptions also contain the literal phrase "the shared browser
session" and must change with the behaviour, not after it.

### D1.4 There is no "no workspace" case — resolve it, never fall back

An earlier revision proposed falling back to the `"default"` constant when
`ToolWorkspaceID(ctx)` is empty. **That is rejected.** It would merge every
workspace-less agent into one shared cookie jar — an isolation regression
against today's per-agent keying, and against the very ADR-043 guarantee
criterion 5b exists to prove.

**Operator ruling (2026-08-31): every turn runs in a workspace context; a
scheduled run outside one is not a state the product supports.**

The code agrees, and the emptiness is narrower than it looks.
`pkg/tools/resolvepath.go:695-709` records that a scheduled/heartbeat turn
**is** rooted in a CoreTeam workspace — its work dir is re-rooted — and that
`ToolWorkspaceID(ctx)` is nevertheless empty because `workspace_reroot.go`
deliberately does not set it (an FR-030 memory-room-routing decision, unrelated
to the browser). So the workspace exists; only the label on the turn is
missing.

**Decision: resolve it the same way the work dir already is.**
`resolvepath.go` hit this exact gap for filesystem mounts and solved it with
`FindForAgentPreferring` rather than a fallback constant, explicitly so "the
two never disagree about which workspace this turn is rooted in". The browser
uses the same resolution, for the same reason.

If resolution genuinely yields nothing, the browser tool **fails with a named
error** — it does not silently join a shared context. A wrong-jar failure is
invisible; a refusal is not.

### D1.5 The silent zero is removed, independently

`ListTabs` must distinguish **three** states, not two: "this workspace has no
browsing context yet", "the browsing context has no tabs", and "you are not
permitted to see the browser". The third matters because the seeded policy
grants the browser surface to **Jim and Ray only** (`pkg/coreagent/core.go` —
Mia and Ava are deny-by-default least-privilege, and the operator confirmed on
2026-08-31 that this stays). Mia is the default agent and the agent in §1.1's
own repro, so without the third state that exact conversation happens again
with a different cause and the same output. Both are legitimate states; reporting
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

**Where the dialog listener can live (found 2026-08-31).**
`installTargetListenerLocked` (`manager.go:2578-2590`) attaches to `se.tabs[0]`
only — correct for its own purpose, because `Target` discovery is
browser-global. But `Page.javascriptDialogOpening` is **per target**. A
tab-0-only listener misses a dialog on tab 2 entirely, and that tab is then
wedged with no record of why. The listener must be per-tab.

**`browser_upload_file` cannot use the `PathHandle` mediation the other
filesystem tools use.** `SetUploadFiles` hands Chrome an absolute host path and
*Chrome* performs the read, so the `os.Root` TOCTOU-hardness is structurally
unavailable — only `RealPath()` applies. The residual window is stated rather
than hidden, and `AllowedRoots` confinement is the control that remains.

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

> **Name correction (2026-08-31, found during spec drafting):** the tool is
> registered as **`serve_web`** (`pkg/tools/web_serve.go:46`,
> `const ToolNameWebServe = "serve_web"`; corroborated by
> `manifest.go:152`). This ADR, its round-1 review **and root `CLAUDE.md`** all
> said `web_serve`. Shipping that string in the error would have sent agents
> hunting for a tool that does not exist — the precise failure D2.5 exists to
> remove. The acceptance criterion asserts the literal.

`browser_navigate` rejects `file://` deliberately (`manager.go:673-681` —
"file:// would bypass Landlock restrictions"). The agent is told only:

> `browser: file:// URLs are blocked for security reasons`

That is a dead end. The supported route exists one tool away — `serve_web`
mints a `/preview/<agent>/<token>/` http URL that `browser_navigate` accepts —
and nothing in the tool surface mentions it. `grep` for `file://`, `serve_web`
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

ADR-071 independently removed most of the cost the issue cited: all eleven
`browser_*` tools are now Tier 3 / search-only, so they occupy no line in the
compressed manifest.

**Decision: close #456 for x86-64 Linux, macOS and the Docker heavy image** —
on those, Chromium is always present and a gate has no payer. Recommended for closure on acceptance; closed 2026-08-31
ahead of it, on the operator's instruction.

**Stated rather than glossed:** linux/arm64 *is* a shipping install that enters
exactly the state #456 described, permanently — no bundled payload, no upstream
build to download, and the `$PATH` escape gated behind `TrustPathChrome`, seeded
`false`. On those hosts every `browser_*` tool is registered and guaranteed to
fail. #665 addresses the distribution gap; until it lands, the tools remain
visible-but-failing on ARM Linux. That is the accepted trade-off, not an
absence of the problem.

**The one real exception, recorded so it is not rediscovered as a surprise:**
linux/arm64 archives carry no `chromium/` payload, because Chrome-for-Testing
publishes no linux-arm64 build (`scripts/install.sh:26-29`) — and the managed
download cannot rescue it for the same reason. Those hosts depend on a system
Chrome, which is itself gated behind `TrustPathChrome` (seeded `false`). The
managed download cannot rescue it either — it fetches from the same upstream
that has no linux-arm64 build. This wants a build-and-distribution answer,
not a manifest gate, and is filed separately as **#665**.

---

### D2.8 Tier assignment (ADR-071)

**Corrected 2026-08-31.** ADR-071's *prose* describes Tier 3 as a closed
enumerated list; the *code* treats it as the residual —
`ToolManifestVisibility` (`pkg/tools/manifest.go:243-251`) returns
`ManifestSearchOnly` for anything lazy outside the 7-name previewed set. So
five of the six new tools become Tier 3 with **zero production edits**. The
count is also **62**, not 63 (`write_agent_metadata` retired), so ADR-071's
prose is already one ahead of its own fixture. The real edit sites are the
pinned literals in `manifest_test.go`.

| Tool | Tier | Why |
|---|---|---|
| `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_upload_file`, `browser_handle_dialog` | 3 — search-only | Same posture as the eleven existing browser tools |
| `browser_snapshot` (D2.4) | **open question** | D2.4 calls it "the default way an agent reads a page". A tool that must be reached through search is a poor default. Either it earns Tier 2 (previewed) or D2.4's wording overclaims. Flagged rather than assumed. |

### D2.9 Tool policy seeding (Hard Constraint #6)

**Boot aborts on any `agent × tool` policy gap.** Six new static builtin tools
therefore need an explicit, literal, wildcard-free entry for **every** agent in
`pkg/config/defaults.go` and in each per-agent block of
`pkg/coreagent/core.go`. Without this D2 does not boot — it is not a follow-up.

| Tool | Jim | Ray | Mia | Ava |
|---|---|---|---|---|
| `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_handle_dialog`, `browser_snapshot` | allow | allow | deny | deny |
| `browser_upload_file` | **ask** | **ask** | **ask** | **ask** |

**Corrected 2026-08-31:** the table above omits two agents that hold the full
browser surface today — `IDExplorer` (`pkg/coreagent/core.go:756-760`) and
`IDResearcher` (`:782-786`). They need the same parity entries.

Two further corrections from the same pass: **Mia and Ava need no edit at
all** — `denyAllThenOverride` gives them deny for free once the names are in
`allStaticToolNames`; and because `ValidateToolPolicyCoverage` is OR-based,
the single `pkg/config/defaults.go:276-287` edit closes the **boot-abort**
risk for every agent. The per-agent edits are posture, not coverage. That
distinction matters: a spec that treats them as coverage will over-scope the
work and under-test the posture.

**Operator ruling, 2026-08-31 (Daniel Piatkowski): `browser_upload_file` is
`ask` in the GLOBAL tool policy, for every agent** — not per-agent, and not
`deny` for delegation-tier workers. It is the only browser tool that carries a
local file across the boundary into a remote site; every other browser tool
moves data inward. That asymmetry is worth one confirmation from whoever is
driving.

**Dependency this creates, and it is a real one.** An `ask` reaching an agent
with no human attached needs a defined answer. `AutoDenyAsk`
(`pkg/agent/loop.go:594-599`) provides it for headless scheduled runs — every
`ask`-policy call is auto-denied rather than hanging. **But issue #659 (open)
records that `AutoDenyAsk` is not inherited by delegated subagents**, so a
delegated worker that tries to upload a file today blocks on an approval nobody
can answer.

The concern was raised before the ruling and the ruling stands; it is sound
*provided #659 lands first*. **#659 is therefore a hard prerequisite for
`browser_upload_file`, not a related nicety** — shipping the seed without it
converts a clean refusal into a hung turn.

### D2.10 Concurrency — one writer per browsing context

§4 calls this the largest open risk in D1, and it is decided here rather than
after, because the failure mode is nondeterministic and §5 already records that
intermittent failure is the most expensive kind for an agent.

**What exists:** `controlledResult` (`pkg/tools/browser/tools.go:962`) checks
only `mgr.Live().IsControlled(...)` — a *human viewer* holding the live view.
Its own doc comment records two further limits: read-only tools
(`browser_screenshot`, `browser_get_text`, `browser_wait`) are deliberately not
gated, and the mechanism is "cooperative, not preemptive… no mid-tool
preemption in v1". Workspace-keying makes interleaved writes from two agents,
or two chats, the normal case rather than an exception.

**Decision:** a **write lease per browsing context**, held for the duration of
one action tool call. A second writer does not error — it returns the same
non-error `{"deferred": true, "reason": …}` shape `controlledResult` already
produces, so the model-facing contract is unchanged and no prompt needs
rewriting. Read-only tools stay ungated, as today.

Deliberately not solved here: mid-tool preemption, and fairness between two
agents contending steadily. Both are ADR-043-era limitations this ADR does not
widen.

### D2.11 Security

D1 changes who can act as a signed-in user, so the change needs this section
rather than a line under Consequences.

- **Elevation of privilege.** Adding an agent to a workspace grants it every
  live session on that workspace. **Decision:** the team-editing UI must state
  this at the point of adding, not only in release notes. D1.0 previously said
  "arguably"; this is the decision.
- **Repudiation.** With a shared context, "which agent acted as the signed-in
  user" must remain answerable. **Decision:** an audit event on browsing-context
  creation, and on an agent's first use of a context it did not establish.
- **Information disclosure — corrected 2026-08-31, the earlier text was
  wrong.** An earlier revision said the snapshot "inherits `browser_get_text`'s
  redaction posture and passes through the same `RegisterSensitiveValues`
  path". **There is no such posture to inherit.** `RegisterSensitiveValues`
  appears **zero** times in `pkg/tools/browser/`; `browser_get_text`'s entire
  treatment is a 64,000-character cap. And the replacer only substitutes
  registered *credential plaintexts* — it would not touch account identifiers
  or form values even if it were wired in.

  It is worse than a missing inheritance: it is a **widening**.
  `browser_get_text` uses `chromedp.Text` (innerText, which never contains
  input values), whereas an accessibility node carries `Value`
  (`cdproto/accessibility/types.go:206`) — which **is** the field's value. So
  the snapshot can expose what a user typed into a form, including a card
  number or a password field, where the existing text tool structurally could
  not.

  **Operator ruling, 2026-08-31 (Daniel Piatkowski): the snapshot returns
  field values by default.** Omit-by-default with an `include_values` opt-in
  was offered and declined; the rationale for the ruling is that an agent
  cannot verify a form is correctly filled before submitting it — one of the
  main things this panel is for — without seeing what is in the fields.

  **The risk is accepted, not absent, and is recorded here so it is not
  rediscovered as a surprise.** A snapshot of a signed-in page can carry a card
  number, a partially typed password, or an account identifier into the
  conversation and into the stored transcript
  (`sessions/<id>/<YYYY-MM-DD>.jsonl`, 90-day default retention). Two
  mitigations are therefore **not** optional and must be specced:

  - The sensitive-value replacer is wired in as defence in depth — it does not
    cover form values, but it costs nothing and closes the credential-plaintext
    case.
  - The snapshot must be reachable in the ActivityPanel / verbose-chat surfaces
    like any other tool call, so an operator can see what was captured. A
    capture nobody can inspect is the failure mode this project has
    `docs/internal/false-green-patterns.md` for.

## 3. Acceptance criteria

The operator's three cases, which are the test plan:

| # | Case | Required behaviour |
|---|---|---|
| 1 | Mia browses; operator watches and takes over | Works as today — unchanged |
| 2 | Operator switches the chat from Mia to Jim mid-session | Jim sees and drives the same tabs. No handover step, no command |
| 3 | Operator browses first, **then** asks an agent to take over | The tab was never owned by "whoever happened to be on screen"; any agent on that workspace **whose tool policy allows the browser surface** sees it |
| 3b | The operator asks a **policy-denied** agent (e.g. Mia) what is open | It says it is **not permitted** to see the browser — never "there are no tabs". Without this, §1.1's exact symptom recurs with a new cause and identical output |
| 4 | An agent delegates unattended background browsing | The sub-agent does not hijack the tab the operator is reading |
| 5 | `browser_list_tabs` with no browsing context | Says so. Must not be indistinguishable from an empty tab set |
| 5b | **Isolation survives the re-key (ADR-043 D2).** Log in to a site in workspace X; open the same site in workspace Y | Y is **logged out**. The amended ADR-043 guarantee, and the test that proves isolation moved rather than vanished |
| 5c | **No surprise logout.** Log in during one chat; start a **new chat in the same workspace** and visit the same site | Still **logged in**. This is what the workspace axis buys over the conversation axis |

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
| 14 | `browser_navigate` on a `file://` URL returns an error **naming `serve_web` as the supported route** |
| 15 | **Boot survives the new tools (Hard Constraint #6).** A fresh install boots with all six D2 tools registered and **no policy-coverage abort** |
| 16 | **Two writers, one context (D2.10).** Two agents on one workspace issue `browser_navigate` concurrently — neither observes the other's mid-navigation state, and neither errors; the loser gets `{"deferred": true, …}` |
| 17 | **Unattended work is signed out (D1.2).** A delegated sub-turn with no viewer attached opens a site the operator is signed into on that workspace — it is **logged out**, and the failure names the reason |
| 18 | **A workspace-less turn resolves, never merges (D1.4).** A scheduled/heartbeat turn reaches the same browsing context as its re-rooted work dir — never a shared constant, never another agent's |

Criteria 7 and 12 are the two whose failure mode is silent or wedging rather
than a wrong answer, and are the ones worth writing tests for first.

---

## 4. Consequences

**Gained**

- Switching agents, or opening a new chat, stops silently stranding the browser.
- The tool descriptions become true, so agents stop asserting a false model to
  operators.
- One less per-agent object; the shared Chrome gains a single owner of record.

**Lost / risked**

- **Concurrency.** Two agents on one workspace can now reach the same tab, and
  so can two concurrent chats. Today they are isolated by accident.
  `controlledResult` already arbitrates human-vs-agent control (ADR-038 D6);
  agent-vs-agent, and chat-vs-chat, are cases it was not written for and must
  be re-read against this change. **This is the largest open risk in D1** — the
  workspace axis widens it relative to the conversation-keyed draft, because
  more parties now share one tab set.
- **Blast radius.** Collapsing per-agent managers touches every browser tool
  call site and the live panel's session resolution.
- **Idle reaping — the mechanism exists; two interactions change.**
  `BrowserManager.ReapIdleSessions` (`manager.go:2986`, `DefaultIdleTTL` 5 min,
  `:134`) already reaps **per tab, gated on real activity** (`:82-96`), which is
  what an earlier draft of this ADR wrongly said "must be defined". Per-tab
  reaping is kept unchanged. Two consequences of the re-key are genuinely new:
  (a) the viewer-attach counter (`:248` — "a context with a viewer attached is
  NEVER idle") now pins an entire **workspace's** context whenever any one chat
  has the panel open; (b) the zero-tab branch keyed on session `lastActivity`
  (`:242-244`) now governs a context that outlives every agent that touched it.
  Both are accepted: per-tab reaping still frees the memory that matters
  (renderers), and a context with no tabs costs almost nothing.
- **Adding an agent to a workspace now grants it that workspace's logins**
  (D1.0). No operator sees this today, so it belongs in the release notes and
  arguably in the team-editing UI, not only in this ADR.
- **Context count scales with workspaces.** ADR-043 sized the hybrid at ~10
  browser-using agents (≈1.5–2 GB). Workspaces are fewer and longer-lived than
  conversations, so this is *better* than the conversation-keyed draft — but
  the reaping rule above still decides whether memory stays finite.

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
  Budget: the actionability pre-check must add **≤150 ms p95** to a click on an
  already-actionable element, measured on the `performance-2x` profile §7 uses.
  Unbudgeted, this lands on a box §7 itself measures at 85-99% utilisation.
- **Surface growth: 11 → 17.** The six added are `browser_select_option`,
  `browser_press_key`, `browser_hover`, `browser_upload_file`,
  `browser_handle_dialog` and `browser_snapshot`. Enumerated because both the
  tier assignment (D2.8) and the policy seeding (D2.9) depend on the exact set.
  Discoverability then rests entirely on tool search returning the right verb.

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
than amending it, and a scraping agent's login on one workspace would surface
on an unrelated one. The operator confirmed isolation was wanted; only
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

Answered since the first draft, recorded so they are not reopened: the
concurrency rule (D2.10), reaping (§4 — the mechanism already exists), the
live-panel re-key (D1.0 — server-side resolution, label stays), the
workspace-less case (D1.4), unattended delegated work (D1.2), and tool policy
(D2.9).

Genuinely open:

- **Is `browser_snapshot` Tier 3?** D2.4 calls it the default way an agent reads
  a page; a search-only default is a contradiction. D2.8 flags it rather than
  guessing.
- **Fairness under sustained contention.** D2.10's lease is per-action and
  first-come. Two agents browsing steadily on one workspace will interleave;
  nothing guarantees either makes progress. Acceptable for the human-takeover
  workload this panel exists for (ADR-038), unexamined for anything else.
- **Does the preview URL need re-keying?** `serve_web` mints
  `/preview/<agent>/<token>/` per **agent** while the browsing context is per
  **workspace**. Whether a preview minted by one agent should be reachable from
  a tab another agent drives is not decided; the token is the credential
  (ADR-044 FR-023), so this is a security question, not a routing one.

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
warm-capture cost and the operator's verdict on the next run was "scrolling
video audio is great".

The before/after, from the on-box `/proc` sampler in the **identical**
condition (boot warm capture, no viewer attached, same machine):

```
04:21:25  pre-fix    chrome=57.0% of a core   machine=29.1%
04:54:24  post-fix   chrome=29.2% of a core   machine=15.2%
```

The deploy landed between them (`08d21393`, 04:52). Cited in full because
commit `08d21393`'s own message predates the post-fix sample and carries only
the pre-fix row — a reviewer checking the commit alone finds 57.0 and 29.1 side
by side in one row and reasonably concludes the figures were misread. They are
two chrome-column readings 33 minutes apart. Per
`docs/internal/false-green-patterns.md`, a number nobody can reproduce is worse
than no number.

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
