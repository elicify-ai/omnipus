# Spec — Browser ownership: workspace-scoped browsers (ADR-072 **D1**)

- **Source ADR:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions.md`, **consolidated revision of 2026-09-01** (commits `22ceff6f1` "consolidating rewrite — one document, each decision stated once" and `809555fcf` "D1.9a"). **D1 only — D1.1 … D1.13** in the consolidated numbering, plus the write lease the ADR files under D2.10 (see §14) and the two D2 sections that place obligations here (D2.9, D2.11).
  ⚠️ **The consolidation RENUMBERED every D1 section.** Revision 3 of this spec cited the pre-consolidation numbers, several of which now resolve to *different* content. **§0.0 carries the full old→new map and is the only place it lives.** Do not re-derive it by find-and-replace.
- **Round-1 ADR review folded in:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions-review.md`.
- **Round-1 spec review folded in:** `docs/internal/specs/browser-workspace-ownership-spec-review.md` — verdict **BLOCK**, 29 findings. Every one is dispositioned in **§15**; one is rejected with evidence and says so there.
- **Round-2 spec review folded in:** `docs/internal/specs/browser-workspace-ownership-spec-review-round2.md` — verdict **BLOCK**, 29 findings (4 CRITICAL / 14 MAJOR / 8 MINOR / 3 OBSERVATION). Every one is dispositioned in **§16**; three are rejected or narrowed with evidence and say so there. *(Both review files use the same finding-id prefixes; round-2 numbers them from 101 to keep them distinguishable. §15 and §16 are separate tables and neither supersedes the other.)*
- **Round-3 spec review folded in:** `docs/internal/specs/browser-workspace-ownership-spec-review-round3.md` — verdict **BLOCK**, 14 findings (C-301…C-305, M-301…M-309). Dispositioned in **§17**. That review was written against the *pre-consolidation* ADR (it cites `D1.1b`/`D1.1c`/`D1.1d`), so §17 restates each finding against the current section numbers.
- **Round-4 (final) spec review folded in:** verdict **BLOCK**, 24 findings (4 CRITICAL / 11 MAJOR / 5 MINOR / 4 OBSERVATION), raised against the consolidated ADR. Dispositioned in **§17**.
- **Amends:** **ADR-043 D1** (one shared Chrome for the process — *this spec replaces it with a pool*), **ADR-043 D2** (per-agent CDP browser context — *replaced by per-workspace Chrome profiles*) and **ADR-043 D3** (live-view binding). Read ADR-043 first; D1 has the largest blast radius of anything in ADR-072.
- **Sibling spec:** D2 (capability). **This spec owns the write lease — §14 is its single normative definition.** The D2 spec must delete its own lease FR/US/stream/test and reference §14 (operator ruling, 2026-08-31).
- **Worktree:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf` · **Branch:** `feat/browser-streaming-performance`
- **Status:** Draft for implementation, **gated on six measurements** (§0.3). Two operator rulings postdate revision 3 and are absorbed here (D1.7's eviction policy, D1.9a's tab ownership); the design questions they reopened are closed, and the ones this spec cannot close are listed in §0.5 as escalations rather than assumptions.
- **Operator rulings folded in (Daniel Piatkowski):**
  - *2026-08-31* — workspace is the isolation axis, not the agent and not the conversation (**D1.2**); **isolation is by Chrome process + profile directory, not by CDP browser context** (**D1.4**); **every agent on a workspace shares its browser and its logins, including unattended delegated work** (**D1.10**, superseding the earlier same-day ruling); every turn runs in a workspace, no workspace-less fallback (**D1.11**); the browser seed stays Jim + Ray only; the write lease belongs to this spec.
  - *2026-08-31, later* — **the cap manages itself (D1.7): at the cap the pool evicts the least-recently-used instance and launches. There is no "pool full" error surface and no UI change.** This **reverses** revision 3's refusal design in full (§17 C1/M1).
  - *2026-09-01* — **tabs stay per agent; the operator's tab is the shared one (D1.9a).** Verbatim: *"the default is they open a new tab — we have in the current version that an agent has its own tab, we should maintain that. Only if the user starts a tab are the agents able to see it and take control on request."* This rescopes the write lease from "every action tool" to "agent-vs-agent on the operator's shared tab" (§14).
  - *2026-09-01, later* — **the memory gate is the ONLY admission control; idle tabs and idle browsers are closed; every other limit is deleted from the codebase (ADR D1.5a).** This is not a config change: `tools.browser.max_tabs`, `tools.browser.max_total_tabs` and the reservation machinery that enforces them are **removed from the code**, and the proposed `max_browsers`/`operator_ceiling` and `--renderer-process-limit` are never built. **§0.6 is the single place this ruling is stated**; everything downstream of it is re-derived or tombstoned there and in the sections it names.

**Citation policy.** `pkg/agent/loop.go`, `turn.go` and `subturn.go` are ~11k-line files under constant churn; per the root `CLAUDE.md` this spec cites them as `file::symbol`. Line numbers appear only where the file is stable or where the exact line *is* the evidence (a config seed, a literal string, a hardcoded constant). Every `file:line` below was re-verified on this worktree on 2026-08-31.

> **Why the word "verified" is no longer used as a shortcut.** The previous draft marked two statements about `ReapIdleSessions` *verified* and both were false (round-2 CRIT-102; the ADR now carries it in its own §8 corrections log). The claim had never been run against the code — it was a plausible reading that survived because the label discouraged re-checking. **Every code claim in this draft was re-derived from source on 2026-08-31, including the ones the previous draft had already labelled verified.** Where a claim is narrower than it sounds, the narrowing is written into the claim rather than left to the reader.

---

## 0. What changed since the previous draft, and what still gates it

### 0.0 The ADR was renumbered — the citation map, in one place

The 2026-09-01 consolidation restated every ADR decision once and **renumbered the whole
D1 series**. Revision 3 of this spec cited the old numbers 73 times. Three of those old
numbers no longer exist at all; three others still exist but now point at **different
content**, which is the dangerous case — a citation that resolves cleanly to the wrong
section is worse than one that dangles, because nothing surfaces it.

**Every citation in this document was re-derived by reading the consolidated ADR.** The map
below is the record of that pass, kept so a reader arriving from revision 3 (or from the
round-3 review, which also predates the renumber) can translate rather than guess.

| Old citation | Uses in rev-3 | Old content | Current section |
|---|---|---|---|
| `D1.0` | 3 | The workspace-is-the-axis ruling | **D1.2** — *Why the workspace — and why not the agent or the conversation* |
| `D1.0a` | 2 | Isolation is OFF by default (`CaptureSharedContext: true`) | **D1.3** — *What this changes in ADR-043 and ADR-048* |
| `D1.1` | 2 | The decision in N statements | **D1.1** — *The decision, in four statements* (unchanged name; statements renumbered — see the note below) |
| `D1.1a` | 29 | Chrome process + `--user-data-dir` profile as the isolation primitive | **D1.4** — *The mechanism: one Chrome process + one profile per workspace* (G-1 and G-2 live here) |
| `D1.1a item 1` (the cap) | — | — | **D1.5** *Sizing the pool* (derivation) + **D1.7** *The cap manages itself* (policy) |
| `D1.1a item 2` (refusal) | — | — | **D1.7** — and the refusal is **withdrawn**; see §17 C1 |
| `D1.1a item 3` (idle close) | — | — | **D1.8** — *Pool lifecycle* |
| `D1.1a item 4` (crash containment) | — | — | **D1.8** |
| `D1.2` | 24 | Everyone on the workspace shares it, delegated work included | **D1.10** — *Everyone on the workspace shares it* |
| `D1.3` | 2 | The browsing key, its table, and the tool descriptions | **D1.11** — *The key, and the turn that has no workspace label*. **The key table is gone**; see §16 MIN-108, now discharged |
| `D1.4` | 8 | The browsing-key resolution ladder and its named failure | **D1.11** |
| `D1.5` | 7 | Three-state `ListTabs` | **D1.12** — *The silent zero* — and the **third state is withdrawn** there; see §17 C3 |
| `D2.8`, `D2.9`, `D2.10`, `D2.11` | — | Tier, policy seed, concurrency, security | **unchanged** |

**`D1.1`'s statements were renumbered too.** The consolidated D1.1 reads (1) signed-in state
belongs to the workspace, (2) one `BrowserManager` per workspace, (3) the isolation primitive
is a Chrome process with its own `--user-data-dir`, (4) every agent on the workspace shares it
including unattended delegated work. Revision 3's `D1.1(1)` and `D1.1(2)` both meant *"one
manager per unit"* and now both map to **D1.1(2)**.

**Sections with no predecessor — all new in the consolidation, and all of them place
requirements on this spec:**

| New section | What it decides | Where it lands here |
|---|---|---|
| **D1.5** | Sizing the pool. **Largely superseded by D1.5a** — its derived cap, `operator_ceiling`, `--renderer-process-limit=R` and global `max_total_tabs` are all deleted. What survives is its **item 3**, the live-memory admission gate, which is now the only control | FR-057, FR-060, §0.6 |
| **D1.5a** | **Memory is the ONLY limit — every counter is deleted from the code.** Idle tabs and idle browsers are closed; eviction under pressure is LRU with D1.7's guards | **§0.6**, FR-059, FR-060, FR-061; tombstones FR-049, FR-055, FR-056 |
| **D1.6** | R was a **floor derived from site isolation**. **Withdrawn by D1.5a**: `--renderer-process-limit` is never set, so full site isolation is retained and the concern is *dissolved* rather than mitigated | §0.6, §12 A23 (rewritten), ~~FR-055~~ |
| **D1.7** | **LRU eviction, no error surface, no UI**; two eviction guards; viewer-staleness timeout; thrash detection. Its *bounded `+1` overshoot* was an overshoot of a **cap**, and D1.5a deleted the cap — see §0.6 for what FR-053 becomes | FR-050…FR-054, §5, §17 C1/M1 |
| **D1.8** | Idle close, crash containment, boot orphan reconciliation via the **launch lock** (not the marker's pid), profile deletion, the upgrade rule, `browser_handle_dialog`'s double exemption, boot-warm | FR-040a, FR-041, FR-042a, FR-043a, FR-043b, §12 A17 |
| **D1.9** | Platform posture — orphan termination is **Linux-only**; no cross-process guard on Windows | US-19/AC2, SC-016, §12 A20 |
| **D1.9a** | **Tabs stay per agent; the operator's tab is the shared one** | FR-048, FR-049, §14, §17 C1 |
| **D1.13** | Live-panel resolution: agent → workspace **server-side**, wire keeps `agent_id`, three schema descriptions are a semantic reversal | FR-016, FR-017, US-10 |

**These numbers ship inside Go doc comments** (`key.go`, `resolve.go`, `pool.go` — §3.1), so a
stale one is not a documentation nit: it sends the next reader to a section that says something
else. §3.1's comments carry the current numbers.

### 0.1 The mechanism changed: process isolation, not context isolation

The previous draft was built on "the isolation primitive is unchanged — one CDP browser context per unit, only the key changes." **That premise was false, and the correction changed the design rather than the wording.**

| Fact | Evidence |
|---|---|
| The shipping default creates **no** per-unit browser context at all | `pkg/config/defaults.go:671` seeds `CaptureSharedContext: true`; in that mode `BrowserCoordinator.Register` returns an **empty** `browserCtxID` (`pkg/tools/browser/coordinator.go:349-359`) and logs *"per-agent browser-context isolation is OFF"* |
| An empty id means one cookie partition process-wide | `manager.go:1360-1370` — `bootstrapBrowserCtx` omits `chromedp.WithExistingBrowserContext` when the id is empty |
| The cause is a hard Chrome constraint, not a preference | `coordinator.go:330-348` — `chrome.tabCapture` *"hard-fails with `Invalid tab specified.` for ANY tab living in a CDP-created browser context … verified against real Chrome 150"*. **A CDP-isolated tab cannot be video-streamed at all.** |
| The documented escape hatch is gone | `pkg/config/config.go:3800-3849` still tells operators the *"JPEG `browser_screencast` fallback keeps working either way"*. ADR-061 deleted that path and `scripts/check-no-jpeg-screencast.sh` prevents its return |

So CDP browser contexts cannot deliver isolation **and** a live panel. **The decision (ADR D1.4) is to isolate by Chrome process and `--user-data-dir` profile directory instead — one Chrome per workspace.** Cookies are isolated because the profiles are; the panel still captures because each Chrome carries its own extension; and, unlike a CDP context, the profile is on disk and therefore survives a reload.

**Consequence for this spec: it now specifies a browser-process pool** — the escape hatch ADR-043 deliberately did not build. That is §3's Stream A and FR-037…FR-045, and it is the largest single piece of work here.

**One prior blocker, checked and dismissed — do not re-raise it.** ADR-043 rejected multiple Chromes partly because the DevTools port was pinned at 9223 and a dynamic per-manager port could not be followed by the compiled Landlock/seccomp allow-list. That reason has evaporated: Chrome is driven over `--remote-debugging-pipe` on inherited fds 3/4 (`pkg/tools/browser/exec_resolver.go:60` — *"There is NO `--remote-debugging-port` — CDP flows over the inherited fd 3/4"*) and the allow-list entry was removed with the port (`pkg/gateway/sandbox_apply.go:412-417` — the removal NOTE; `:405-409` is the `enforcePortRules` computation above it, which the previous draft's `:405-417` span wrongly swept in. Round-2 MIN-104). **N Chromes are N pipes and nothing to allow-list.** (`pkg/tools/browser/manager.go:1330-1345` still describes a pinned port; that comment governs the legacy no-coordinator fallback, not the coordinator path this spec changes. It should be corrected in passing.)

### 0.2 The scope shrank: unattended delegated work is no longer a separate case

ADR D1.10 was rewritten by a superseding ruling: **every agent on a workspace shares that workspace's browser and its logins, including unattended delegated work.** The earlier same-day ruling (background work starts signed out) is reversed, informedly: when a second jar was a CDP context it was nearly free; under D1.4 it is another whole Chrome process per background job.

**This deletes the hardest part of the previous draft.** Not deferred — deleted:

- the attended/unattended discriminator (`tools.WithSubTurn`/`ToolSubTurn`) is **not built**. `spawnSubTurn` inheriting the parent's `WorkspaceID` (`pkg/agent/subturn.go:1323`) is now **correct behaviour**;
- `BrowserManager.browserCtxID` stays a **single field** (`manager.go:381`) — one browser per manager, one manager per workspace. The per-key map the previous draft called its "highest-value single item" is not built;
- `tools.ToolTranscriptSessionID` is **not** a browsing key;
- `BrowsingKey` has exactly **one** shape, `ws:<workspaceID>`. `BrowsingKeyKind` is not built;
- a `ViewerCounter` seam for attendance is not built. (`BrowserManager.Viewers()` is still added — FR-010 — but for the *reaper* and the pool's idle-close decision, not for attendance.)

**The accepted risk, stated once and only once:** an unattended agent can act as the operator on any site its workspace is signed into — a purchase, a post, a message sent by a process nobody is watching. The single remaining gate is `browser_upload_file`'s global `ask` seed (ADR D2.9), and **issue #659 is its prerequisite** (FR-032).

### 0.2a Tabs stay per agent — and that does not survive the re-key by itself

**Operator ruling, 2026-09-01 (ADR D1.9a), verbatim:** *"the default is they open a new tab — we
have in the current version that an agent has its own tab, we should maintain that. Only if the
user starts a tab are the agents able to see it and take control on request."*

| | Owner | Who can see it | Who can drive it |
|---|---|---|---|
| A tab an **agent** opens | that agent | that agent | that agent |
| A tab the **operator** opens | the workspace | **every agent on the workspace** | the operator; an agent **on request** |

One Chrome per workspace, one cookie jar (D1.3, D1.10) — and **inside it, tab ownership stays
per agent, exactly as today**.

**This is what actually fixes ADR §1.1's defect.** The operator opened the panel, browsed, and
the tab was attributed to whichever agent's panel happened to be on screen — so Jim could not
see it. Under this ruling an operator-opened tab is **not owned by an agent at all**: it belongs
to the workspace. Jim sees it because it was never Mia's.

**The trap, verified against source rather than inferred.** Today's tab set belongs to the
*browsing context*, not to the agent: `BrowserManager.sessions` is
`map[string]*sessionEntry` (`manager.go:338`) and each `sessionEntry` owns
`tabs []*tabEntry` (`manager.go:203-204`), reached by session id — which every tool
hardcodes to `DefaultSessionID` (ADR-041 D1). Agents are separated **only** because each has
its own manager. FR-001 collapses managers to one per workspace, so a re-key alone gives every
agent on the workspace **one shared tab set** and silently deletes the separation this ruling
requires. **The agent dimension must therefore be carried explicitly on the tab set** —
FR-048 — and FR-048's test fails if two agents' tabs merge.

**Second-order consequence nobody had owned: `cfg.MaxTabs` — and it is now moot.** The
per-agent tab cap (`BrowserConfig.MaxTabs`, default **5** at `manager.go:36` and `:124`; config
key `tools.browser.max_tabs`, `config.go:3633`) is enforced by `totalTabCountLocked`
(`manager.go:1549-1555`), which sums `len(se.tabs)` across **every session in the manager** and
is checked at `:1139` (`createFirstTab`), `:2005` and `:2047` (`OpenTab`), `:2216` and `:2286`
(`adoptTarget`). One manager per agent makes that per-agent today; one manager per **workspace**
would have made it 5 tabs shared across the whole team — a silent tightening nobody had
noticed. Revision 4 answered that with **FR-049** (enforce `MaxTabs` per agent tab set).

**The operator ruling of 2026-09-01 (ADR D1.5a) removes the question instead of answering it:
`tools.browser.max_tabs` is DELETED from the code.** There is no per-agent tab cap and no
per-workspace one; capacity is the memory gate, and idleness is the reaper. **FR-049 is
tombstoned** with that reason (§9) — it was the right answer to a question that no longer
exists. FR-048's per-agent tab *sets* are unaffected and still required: ownership ("whose tab
is this") is a correctness property, not a capacity one, and the ruling touched only capacity.
See §0.6.

**What this ruling removes from the lease.** §14's write lease was scoped as "two agents
sharing one tab set". Two agents on **their own** tabs do not contend, so the general case is
gone. What remains is narrow: **agent-vs-agent on the operator's shared tab**. Operator-vs-agent
is the existing `LiveViewRegistry.TakeControl` / `IsControlled` (`live.go::TakeControl` at
`:1241`, `::IsControlled` at `:1313`, ADR-038 D6) and is untouched. §14 is rescoped
accordingly; the change is smaller than either answer to the old retry-vs-defer question
implied, because the operator removed the premise.

### 0.3 What still gates implementation — six measurements

None is a design question. All six are numbers nobody has, and each one either sizes the pool,
validates it, or sets a constant this spec otherwise has to guess. Revision 3 listed two and
sequenced the pool behind them; the consolidated ADR names four more, all under the heading
*"all four are required before the pool is built"* (D1.5) plus one from D1.7.

| Gate | Question | Kind | FR / SC |
|---|---|---|---|
| **G-1** | **Narrowed by measurement (ADR, 2026-09-01).** `PER_BROWSER_COST` is now measured at **≈182 MB** for Chrome for Testing, and D1.5a deleted the formula it was an input to. What remains: the marginal cost of a *second* Chrome **on Linux, with capture running** | human review gate | FR-044 / SC-012 |
| **G-2** | Does `chrome.tabCapture` succeed against a **second Chrome's default context**? | mechanical | FR-045 / SC-012a |
| **G-3** | Does Chromium read a **cgroup memory limit**, or does it size itself against host RAM inside a capped container? | mechanical | FR-057a / SC-019 |
| **G-4** | Does Linux **memory-pressure signalling** still fire for Chrome at all? | mechanical | FR-057a / SC-019 |
| **G-5** | **Cold-start latency with a warm profile on disk** | human review gate | FR-054 / SC-020 |
| **G-6** | What binds first on the measured host — **memory or CPU** — once browser, GPU and encoder processes are multiplied by N? **Sharper under D1.5a, not softer:** with every counter deleted, the memory gate is the *only* admission control, so if CPU binds first there is nothing at all in front of it | human review gate | FR-057 / SC-021 |

- **G-1 — per-Chrome memory (FR-044). PARTLY DISCHARGED by measurement, and its remit is now narrower.** The ADR's own measurement pass of 2026-09-01 puts **`PER_BROWSER_COST` ≈ 182 MB** for Chrome for Testing — the binary Omnipus ships — as **125 MB browser + utility processes and 57 MB GPU**. **Carry its scope every time it is quoted:** one machine, one snapshot, **macOS**, `top`'s physical-footprint column, and an **idle, non-capturing** instance. A *capturing* browser costs that **plus the injected capture extension plus the encoding work**, and that delta is **unmeasured** — the operator's own 470 MB of personal extensions is *not* a proxy for it (five extensions at ~94 MB each, against our one lightweight encoder shim; borrowing it would overstate by roughly an order of magnitude). The 57 MB GPU term is a macOS headful-capable figure; on a headless Linux server we pass `--disable-gpu` (`exec_resolver.go:161-178`), so it may vanish, shrink, or move into the renderers. **G-1's remaining job is therefore the marginal cost of a second Chrome for Testing on Linux with capture running** — the number FR-062's launch-headroom minimum needs. ADR-043's "≈4–5 GB at ten agents" is labelled in its own text as a rough, unmeasured order-of-magnitude estimate, and it was per *agent*; it must not be quoted as the figure.
  ⚠️ **PSS, not RSS — and this correction is the ADR's own.** Revision 3 of this spec mandated *"the marginal **RSS**"*, and ADR §8's corrections log carries that as an **open downstream defect** (*"The RSS retraction had a downstream consumer that still specifies RSS"*), pointing at this line. RSS charges Chrome's file-backed program code to every one of its ~12 processes: on the measured box `omnipus-uat-swimlane` (2 cores, 3916 MB) the same sample read **1118 MB RSS** and **434 MB PSS** — RSS over-counts by **2.6×**. A cap sized from RSS is a cap sized from a number the ADR has retracted. **`ps` cannot produce PSS**; use `smem`, or sum the `Pss:` line of `/proc/<pid>/smaps_rollup` over the instance's process tree.
- **G-2 — capture against a second Chrome's default context (FR-045).** ADR D1.4's claim that "each Chrome carries its own extension, so `chrome.tabCapture` works" is the same *class* of claim that proved false for CDP contexts, and its falsification cost a whole design. **Prove it with a spike against real Chrome before the pool is built** — two Chromes, distinct `--user-data-dir`s, extension loaded in each, capture a tab in the second. If it fails, stop and re-open D1.4; do not build the pool first.
- **G-3 / G-4 — the two Chromium-behaviour unknowns (FR-057a).** Both are binary and both are cheap: run one Chrome inside a `memory.max`-capped cgroup and read back the renderer limit it computes for itself. If Chromium sizes against host RAM regardless of the cap, D1.5's `budget = min(host_RAM, cgroup_limit) × 0.5` is describing a policy Chrome is not following, and the pool's own bound is the only one. If pressure signalling never fires, Chrome will never self-discard and the same conclusion follows harder. **Neither may stay prose** — §0.3.1's whole argument is that a gate without a failing check is not a gate.
- **G-5 — cold start from a warm profile (FR-054).** D1.7 defers thrash detection's window and threshold to this number explicitly. ADR-042's ~30–60 s figure covers a *fresh install including a Chromium download* and is not the relevant number. Without G-5 the two constants get guessed, which is the failure mode §0.3 exists to prevent for the cap.
- **G-6 — memory or CPU (FR-057).** ADR §7's whole argument is that a **CPU** bound solved the problem on a 2-core box at 85–99 % utilisation with **one** Chrome. The pool multiplies browser processes, GPU processes and encoder pages by N on that same class of box and bounds **memory only**. **D1.5a makes this gate more load-bearing, not less:** revision 4 needed it "before the ceiling default is chosen", and there is no ceiling any more — so if CPU is what binds, the product has *no* admission control in front of it at all. One measurement before FR-057 ships.

**G-1, G-2 and G-6 gate Stream P. G-3, G-4 and G-5 gate the specific FRs they size (FR-057, FR-054) and not the whole stream** — thrash detection may land behind conservative constants provided it says so. None of the six blocks the §0.4 work.

⚠️ **One carve-out revision 4 allowed is WITHDRAWN by D1.5a.** Revision 4 permitted the pool to land "with the pressure gate behind a config default of *off on unverified platforms*". **The pressure gate is now the only admission control there is**, so shipping it off is shipping no limit — see §0.6's second consequence and FR-061. Where the platform gives no pressure signal, the honest posture is the one D1.9 already takes for orphan termination: say so, and do not pretend the gate is present.

#### 0.3.1 How each gate is enforced — three mechanical, three named human gates

Revision 3 declared both of its gates "blocking" and gave neither a failing check. **G-2's was
worse than absent: it was a test that reports green without running.** Test 37 was specified
with `skipIfNoBrowser`, which has **two** skip paths (`pkg/tools/browser/browser_e2e_test.go:57-112`),
each of which ends the test as a PASS:

1. `if os.Getenv("CI") != "" && os.Getenv("OMNIPUS_BROWSER_E2E") == "" { t.Skip(...) }` (`:66-68`) — in CI, without the opt-in env var, it always skips;
2. no probeable Chrome/Chromium on `$PATH`, in the macOS `.app` locations, or in the managed install root — the probe ladder at `:69-110` falls through to `resolveTestBinary(t)` at `:111`, which the function's own comment says *"calls `t.Skipf` … when even the managed/download path comes up empty"*. It skips rather than fails.

G-2 guards the single load-bearing assumption of D1.4, and the equivalent claim for CDP
contexts proved **false** against real Chrome 150 (`coordinator.go:330-348`). A skip standing in
for that proof is exactly the shape `docs/internal/false-green-patterns.md` exists to catch.

##### The CI claim revision 3 made about this was itself false — corrected

Revision 3 argued that *"the CI gate always skips without the env var"* and routed G-2 to the
Fly worker on that basis. **Verified false on this worktree.** `.github/workflows/pr.yml` carries
a dedicated `browser-e2e` job (`:392`) whose job-level env sets **`OMNIPUS_BROWSER_E2E: "1"`**
(`:416`, with the comment *"Opts past skipIfNoBrowser's CI branch. Set ONLY here."*), installs
Chrome as a declared dependency, verifies it resolves under one of the four names
`skipIfNoBrowser` probes, and then **fails the job on either skip path** (`:468-472`, grepping
the log for `skipping browser E2E test` and `no managed Chrome for`). The CI gate is real, it is
GitHub Actions, and it already does most of what SC-012a asks for. **The genuine gap is
simpler and worse: the test does not exist** — nothing in the tree exercises a second Chrome
instance. ADR §8 records the same correction against itself.

Three consequences, all of which change what the implementing PR must actually do:

1. **G-2's home is the existing `browser-e2e` job, not the Fly worker.** The Fly `e2e` gate
   stays available as a second runner, but specifying the primary home as somewhere other than
   the job that already sets the variable would build a second gate beside a working one.
2. **The "receipt without a pipe" rule cannot be applied literally to that job's existing
   step, and must not be.** The shipped step is
   `go test -v … ./pkg/tools/browser 2>&1 | tee /tmp/browser-e2e.log` under
   `set -euo pipefail` (`:465-466`). `pipefail` makes the pipeline's status the *first*
   failing command's, so `go test`'s exit code is already what fails the step — the trap the
   no-pipe rule exists to catch (`cmd | tail` reporting **tail's** status) does not apply here.
   **The rule as this spec states it is a rule about the receipt an author pastes into a PR
   body, not a demand to rewrite a correct CI step.** SC-012a is restated to say exactly that:
   under `set -euo pipefail`, `| tee` satisfies it; a bare pipe without `pipefail` does not.
3. **G-2 needs its own step in that job, and the reason is a number.** The same step asserts a
   pass floor — `if [ "$passes" -lt 180 ]` (`:481`), with a comment saying the true count is
   ≥180 and never to lower it without re-verifying. A `-run '^TestSpike_CaptureAgainstSecondChrome$'`
   invocation **inside** that step produces one pass and trips the floor, failing the job for a
   reason unrelated to the gate. G-2 therefore runs as an **additional step** in the
   `browser-e2e` job, with its own `-run` filter, its own receipt and **no** pass floor of its
   own beyond "exactly one PASS, zero SKIP".

**G-2's mechanical conditions (FR-045, SC-012a), restated against that reality.** Four, all
required before Stream P's first commit:

- Test 37 is **not** `skipIfNoBrowser`. It uses a new helper, `requireBrowserOrFail(t)`, which resolves a browser through the **same** three-source ladder `skipIfNoBrowser` uses and calls `t.Fatal` — never `t.Skip` — when it finds none. A missing browser on the G-2 runner is a gate failure, not a pass.
- It runs as its **own step** in the `browser-e2e` job (which already exports `OMNIPUS_BROWSER_E2E=1` at `:416`), with `-run '^TestSpike_CaptureAgainstSecondChrome$' -count=1`, so it does not enter the `>= 180` floor's accounting.
- The step runs under `set -euo pipefail`, so the receipt may be captured through `| tee`; the author's PR-body receipt is captured as `cmd > log 2>&1; echo "exit=$?"`.
- The log is asserted to contain **exactly one** `--- PASS`, and neither `--- SKIP` nor `no tests to run`. A gate whose own log says SKIP has not run, and a `-run` pattern that matches nothing exits 0.

**G-3 and G-4 are mechanical too, and cheaper than G-2.** Each is one Chrome launched inside a
`memory.max`-capped cgroup, reading back the renderer limit Chrome computes for itself and
whether a pressure notification is ever delivered. Their receipts go in the same PR body, with
the same no-bare-pipe discipline. *Fails if:* either question is answered in prose rather than
from a run (SC-019).

**G-1, G-5 and G-6 are human gates, and this spec says so rather than pretending otherwise.**
There is no honest mechanical form: each is a measurement on one host, and a unit test asserting
"a measurement file is non-empty and dated" would pass on a fabricated file — a check that
cannot distinguish a measurement from a plausible number is not a gate, it is a second place to
write the guess. **Their owner is the implementing PR's human reviewer, and their artefacts are
named in SC-012, SC-020 and SC-021:** for G-1 — **narrowed by D1.5a, because the number it
existed to find is now measured (`PER_BROWSER_COST` ≈182 MB, FR-062)** — the raw `smem` (or
summed `/proc/<pid>/smaps_rollup` `Pss:`) output for one and two Chromes **on Linux**, the same
figure **with capture running**, the host's total RAM, and the gateway's own steady-state PSS.
*(Revision 4 asked for N = 1…4 and "the arithmetic from those to the shipped `operator_ceiling`".
There is no ceiling and no formula — D1.5a — so what remains is the measurement itself, on the
platform and in the state the shipped figure does **not** cover: Linux, and capturing.)* A
reviewer who cannot see the numbers must not approve. The one mechanical half that *is* real is
negative: `TestPool_LaunchHeadroomUsesMeasuredCost` (test 51, re-derived from
`TestConfig_MaxBrowsersCeilingIsNotZeroOrRound`) fails if the shipped figure is a round guess, if
any literal browser count appears in `pkg/config/defaults.go`, or if the constant's doc comment
omits its scope — which does not prove a measurement happened but does make the most common
failure visible.

**No gate is satisfied by a green CI run alone.** SC-012, SC-012a, SC-019, SC-020 and SC-021
state each one's failure condition separately.

### 0.4 What is not gated

Everything that is about *ownership* rather than *partitioning* — and this is the part that fixes the reported defect:

- one manager per workspace, with **per-agent tab sets inside it and a workspace-owned tab set for the operator** (FR-001, FR-002, FR-002a, FR-002b, FR-002c, **FR-048**) — **this alone fixes ADR criteria 2 and 3**, because handover is broken by the *manager* split, not by any partition, and FR-048 is what stops the fix from deleting the per-agent separation D1.9a preserves;
- the resolution ladder, its named failure, and a distinguishable panel failure reason (FR-007, FR-008, FR-008a);
- the reload prune and per-key idempotent registration (FR-026a, FR-026b);
- the **two-state** tab answer, the **deletion** of the false shared-browser claim and its **interim** replacement literal (FR-013, FR-014, FR-015, FR-034);
- the write lease, **rescoped to the operator's shared tab** (§14);
- audit — now **per write-class action**, with a viewer-safe event name (FR-016, FR-016a, FR-016b, FR-017, FR-018, FR-027, **FR-058**);
- the team-membership disclosure (FR-047) — it does not depend on the pool's existence, and it is true the moment Stream A lands.

- **the deletion of every tab counter** (**FR-059**) — `tools.browser.max_tabs`, `tools.browser.max_total_tabs` and the reservation machinery. It is code deletion in `pkg/tools/browser` and `pkg/config`, it depends on nothing the pool builds, and leaving it until Stream P would leave a per-workspace manager silently enforcing a per-agent cap across a whole team in the interim (§0.6).

**Sequence: land §0.4 first (FR-059 included), run G-1, G-2, G-3 and G-4 in parallel, then build the pool behind G-1/G-2/G-6.**

**What LEFT §0.4 since revision 3, and why.** FR-046's operator-facing browser-close action
(REST path + SPA control) was in this list as *"the remedy the pool-full refusal names"*.
D1.7 withdrew both the refusal and the close, so the remedy has nothing to remedy: it is
**tombstoned** (§9), not deferred. Its departure also removes this spec's only
`contracts/openapi.yaml` **path** addition, which restores SC-007's original condition (2)
and deletes §5's added-path carve-out.

**One thing is deliberately NOT in §0.4, and the previous draft had it there.** The **final** description literal — the one that tells the model each workspace has its own browser and its own logins — asserts isolation, and isolation does not exist until Stream P ships FR-037. Shipping it early would make the product state a false ownership claim to the model and to the operator, which is the precise defect ADR-072 §1.1 exists to fix; the previous draft's justification ("the intermediate state is exactly today's behaviour, so it is not a regression") is true of the cookie partitioning and false of the sentence describing it. **FR-034 therefore has two literals with two landing points** (§3.3): an interim one that claims only what Stream A makes true, and a final one that lands in the same commit as FR-037. §5 records the general form as a non-behaviour.

### 0.5 What the two post-revision-3 rulings changed, and what this spec escalates

Revision 3's header said *"all design questions are decided"*, and by the time it was reviewed
that was no longer true: two operator rulings had landed against sections it recorded as
settled, and revision 3 carried no ADR revision pin, so a reader could not detect the gap. Both
rulings are absorbed above. This section records the places where absorbing them — and then
absorbing D1.5a on top (§0.6) — left something that **this spec cannot decide on its own** — written as escalations rather than as
assumptions, because an assumption in a spec reads as a decision to whoever implements it.
**Five are live** (E-1, E-3, E-4, E-5, E-6); **E-2 is ruled and kept struck through** so a reader
arriving from an earlier review sees it answered rather than dropped. **E-6 is new with D1.5a and
is the most consequential of them** — it asks whether the one control that remains can run at all
on two of the three supported platforms.

| # | The question | Where it sits now | Who must answer |
|---|---|---|---|
| **E-1** | **What does "take control on request" look like to an agent?** D1.9a rules that an agent may drive the operator's tab *on request*; the ADR states plainly that the mechanism — an explicit tool, or implicit acquisition on first write — is a **D2 tool-surface decision** and leaves it in its §6. | FR-048 specifies the **ownership and visibility** model, which is D1's part and is complete without this. The *acquisition verb* is not specified here. §14 assumes the second shape (implicit acquisition on first write to the shared tab) **only** as the lease's contended case, and says so; if the ruling is an explicit tool, §14 changes shape but not substance. | Operator, via the D2 spec |
| ~~**E-2**~~ | ~~**Pressure gate vs eviction, when they disagree.**~~ **RULED — no longer an escalation.** D1.5a settles it: *"the memory gate is the ONLY admission control"*, and the ADR states plainly that **the pressure gate is a hard stop and the cap is soft — and with the cap deleted, only the hard stop remains.** So when pressure is high and nothing is evictable, the request is **refused**, with an error naming **memory** and not a cap (the remedies differ, and an operator told the wrong one raises a ceiling that is not the constraint — and there is no ceiling left to raise). | FR-057 is rewritten to state the ruled answer instead of recording a collision; **test 72, which deliberately did not assert that case, now asserts it** (§10). | **Answered** (operator, ADR D1.5a) |
| **E-3** | **Is `browser_snapshot` reachable at all under D1.9a?** Out of this spec's scope (it is a D2 tool), but D1.9a changes what it reads: a snapshot taken by agent A now sees A's own tab, not the workspace's. | Recorded, not decided. | D2 spec |
| **E-4** | **Three ADR §6 "open" questions this document has already answered.** See §12 A24 — capture-session-per-workspace (FR-016a), per-workspace profile disk (§16 MAJ-111), and instances-vs-bytes (**closed by D1.5a: bytes, and nothing else**). Two are kept with the ADR named as needing to close them; **one is reopened** because this spec's closure of it did not hold. | §12 A24 | ADR owner |
| **E-5** | **Is a per-tab headroom floor needed, and what is it?** FR-060 puts the pressure gate in the tab-open path, expressed as a **ratio** with no per-tab byte constant — because the ADR withdrew the 85 MB constant on measured evidence (30 MB → 327 MB in one snapshot, an 11× spread). A *browser* launch has a measured floor (`PER_BROWSER_COST` ≈182 MB, FR-062); a *tab* has none, and this spec declines to invent one. | Recorded, not decided. A ratio-only tab gate is what FR-060 specifies; if measurement later shows the ratio moves too slowly to catch a fast tab loop, a floor is a design change, not a tuning change. | Operator / G-1's Linux pass |
| **E-6** | **The only admission control there is cannot run on macOS or Windows.** `readMemAvailableBytes` returns **0** off Linux by deliberate design (`pkg/config/meminfo_other.go`, `//go:build !linux`: *"this project takes on no per-OS memory-query code… that real implementation is future work"*), and `readCgroupMemoryAvailableBytes` returns `(0, false)`, so `availableRAMBytes` (`pkg/config/config.go:655`) is **0** on every non-Linux host. Revision 4 could accept a non-Linux no-op because a **counter** still bounded the pool there. **D1.5a deletes the counter, so a no-op gate on macOS/Windows is not a degraded limit — it is no limit at all (FR-061), on the platform where `PER_BROWSER_COST` was actually measured.** | Recorded, **not decided**. FR-057 states the gap and FR-061 forbids defaulting through it silently. Three shapes exist and this spec may not choose between them: (a) implement a real macOS/Windows memory reader (crosses the "no per-OS memory-query code" line that file draws, though `golang.org/x/sys/unix` keeps it pure-Go and CGo-free on Darwin); (b) ship browser support **Linux-only** until one exists; (c) accept an unbounded browser pool off Linux and **say so in the release notes**. | **Operator** — this is a scope ruling, not a spec detail |

### 0.6 Memory is the only limit — every counter is deleted (operator ruling, 2026-09-01)

**Verbatim:** *"the memory gate is the ONLY admission control. Idle tabs and idle browsers are
closed. Every other limit is deleted from the codebase."* Recorded in the ADR as **D1.5a**,
which supersedes D1.5's cap arithmetic and D1.6's renderer floor.

**This is a code deletion, not a configuration change.** Revision 4 specified two shipped
counters and proposed two more. All four go, and the machinery whose only purpose is enforcing
the shipped two goes with them.

| Deleted | Where, verified on this worktree 2026-09-01 | Status before this ruling |
|---|---|---|
| `tools.browser.max_tabs` (default 5) | `pkg/config/config.go:3633` (`MaxTabs int json:"max_tabs"`); default 5 at `pkg/tools/browser/manager.go:36`,`:124`; applied `pkg/agent/loop.go:2314-2315`; **18 executable references** in `pkg/tools/browser` (see the count note below) | **Shipped and enforced** |
| `tools.browser.max_total_tabs` | `pkg/config/config.go:3678`; coordinator field `:128`, ctor `:226-233`, `SetMaxTotalTabs` `:635-644`, `ApplyRuntimeConfig` `:659-660`, gate `:785-792`; threaded from `loop.go:2452`,`:2455` | **Shipped, never seeded** — `grep MaxTotalTabs pkg/config/defaults.go` returns nothing, and `coordinator.go:246-247` logs *"global tab budget: UNLIMITED"* when it is `<= 0` |
| `TryOpenTab` / `ReleaseTab` / `reservedTabs`, and the in-flight race handling that exists only for them | `coordinator.go:137` (`reservedTabs`), `:782-804` (`TryOpenTab`), `:806-812` (`ReleaseTab`), `:818` (`totalOpenTabsLocked`); manager side `manager.go:3343-3352` (`reserveGlobalTab`), `:3358-3366` (`releaseGlobalTab`), called from `tabs.go:249`, `:180`, `:260` and `manager.go:3118` | **Shipped**; its entire purpose is enforcing the two keys above |
| `max_browsers` / `operator_ceiling` | This spec's FR-056 and its `Target()` formula | **Proposed, never built** |
| `--renderer-process-limit` and the renderer floor `R` | This spec's FR-055 and the `R >= max_tabs` derivation | **Proposed, never built** |

**A note on the count, so the number is checkable rather than asserted.** ADR D1.5a says "17
references in `pkg/tools/browser/`". Re-counted here as
`grep -rn "MaxTabs\|max_tabs" pkg/tools/browser --include="*.go" | grep -v _test.go` minus
comment-only lines: **18**. The one-line difference is a classification question (whether the
`MaxTabs int` field declaration at `manager.go:36` counts as a reference), not a disagreement
about what exists. Test-side: **59 references across 10 `_test.go` files**, which is migration
work FR-059 owns and nobody had budgeted.

#### Removing `--renderer-process-limit` is a security IMPROVEMENT, and the finding is dissolved

D1.6 introduced the flag to bound per-instance cost for a formula, and recorded that it weakens
site isolation for pages beyond the cap — over-limit navigations reuse same-site processes. That
was justified as acceptable for *"agent-driven browsing of semi-trusted destinations"*, and
**`ValidateURL` (`pkg/tools/browser/manager.go:685-708`) does not support that adjective**: it
blocks five schemes (`blockedSchemes`, `:675-683`) plus private and metadata addresses via the
SSRF checker (SEC-24), and permits **every other public `http(s)` URL**. There is no allow-list
anywhere in `pkg/tools/browser/`, and `browser_navigate`'s URL comes from model output that page
content the agent just read has already influenced.

With capacity governed by live memory there is **no reason to set the flag at all**, so Chrome's
default site-per-process isolation is retained in full. **The round-2 / round-3 finding (C-303,
and the C4/C206 concern the ADR names) is therefore DISSOLVED, not mitigated** — there is no
residual trade-off to accept, no compensating control to remember, and nothing for a future
reviewer to re-litigate. This is the rare case where deleting a feature removes a security cost
rather than adding one, and it should be said plainly in the PR body.

#### FR-049 and FR-055 were correct answers to a question that no longer exists

Both are **tombstoned with that reason**, not silently dropped — §16 and §17 cite them as
CRITICAL resolutions (M7(c) and C4), so a reader arriving from those reviews would otherwise
read the removal as a regression.

- **FR-049** gave `cfg.MaxTabs` an owner after the re-key, so that a per-agent cap did not
  silently become a per-team one. Correct, and moot: `max_tabs` is deleted, so there is no cap
  to own. *(FR-048's per-agent tab **sets** are untouched — that is ownership, not capacity.)*
- **FR-055** derived `R` from `max_tabs` as a site-isolation floor. Correct, and moot twice
  over: the flag it configured is never set, and the key it derived from is deleted.
- **FR-056** (`max_browsers` derived, `tools.browser.max_browsers` as ceiling) is tombstoned
  with them. It was never built.

#### What replaces all of it

1. **Admission on live memory only.** With no counter anywhere, the pressure gate (FR-057) is
   the sole hard stop, and the ADR rules it a **hard** stop: refuse, naming memory.
2. **Idle close, for tabs AND for whole browsers.** Tabs already reap — `ReapIdleSessions`
   (`manager.go:2986`) with `DefaultIdleTTL` **5 minutes** (`manager.go:130-134`). Whole-browser
   close is new work (FR-040, FR-040a). **Both are now load-bearing rather than housekeeping**,
   and FR-061 states what that changes about their criteria.
3. **Eviction under pressure** — least-recently-used, with D1.7's two guards, **unchanged**
   (FR-050, FR-051, FR-052). Eviction is what happens when memory says stop and something is
   reclaimable; refusal is what happens when nothing is.

#### The risk this accepts, and the two consequences the ADR names as non-optional

**Stated plainly: with no counter anywhere, a runaway agent opening tabs in a loop is bounded
only by memory pressure.** Two things follow, and neither is optional.

- **The pressure check must sit in the TAB-OPEN path, not only the browser-launch path
  (FR-060).** A loop that opens tabs inside an already-running browser never reaches a launch
  decision. The tab-open sites are `manager.go::createFirstTab` (`:1139`), `::OpenTab`
  (`:2005`, `:2047`) and `::adoptTarget` (`:2216`, `:2286`) — **which are exactly the five sites
  `cfg.MaxTabs` is checked at today.** Deleting the counter without putting the gate there
  removes the only thing standing between that loop and the OOM killer, and **no counter remains
  to catch it.**
- **Idle close and the pressure gate are now the entire defence (FR-061).** Previously a counter
  caught a runaway before memory did. That backstop is gone **by decision**. So a gap in either
  control is **not a degraded limit — it is no limit**, and neither may ship disabled, "best
  effort", or behind an off-by-default flag on platforms where it is unverified (§0.3's
  withdrawn carve-out).

#### `PER_BROWSER_COST` is measured — ≈182 MB — and its scope travels with it

**≈182 MB** for **Chrome for Testing** (the binary Omnipus ships and launches): **125 MB browser
+ utility processes, 57 MB GPU**; 301 MB total across 9 processes with light pages.

**Scope, which must be quoted whenever the number is:** one machine, one snapshot, **macOS**,
`top`'s physical-footprint column, and an **idle, non-capturing** instance. A *capturing* browser
costs this **plus the injected capture extension plus the encoding work**, and that delta is
**unmeasured**. It is neither the 120 MB first proposed (too low — an instance would launch and
then fail to load a page) nor the 400–500 MB an earlier revision estimated from the inflated RSS
reading ADR §8 retracts. FR-062 carries it as the launch-headroom minimum; G-1's remaining job is
the same figure **on Linux, with capture running**.

#### Anything priced per renderer, or per tab, is gone

The 85 MB constant Chromium uses internally is **withdrawn from this spec's capacity path** on
measured evidence: real renderers spanned **30 MB → 327 MB in one snapshot** — an 11× spread — so
**no per-renderer constant works**, and `FIXED_FLOOR + (R × 85MB) + encoder_page` is not an
arithmetic this spec may perform. Renderer count is also not tab count: the measured Playwright
instance reported **2 tabs against 13 renderer processes** — cross-origin iframes, spare
renderers and app windows, roughly **six processes per tab** — so anything priced per tab is
wrong by about that factor. **The capacity path uses live measurement and no constant.**

#### `max_total_tabs` staying global across N Chromes is moot

Revision 4's FR-038a, §12 A21 and test 53 all carried the rule that the global tab budget stays
global rather than becoming per-Chrome. There is no global tab budget: the key and its
enforcement are deleted. The rule, its scenario, its dataset row and that half of test 53 are
tombstoned, not re-derived.

#### The message defect this ruling creates, and the reason code that fixes it (FR-063)

Found by the D2 spec, not by this one. `adoptTarget` returns a machine-readable reason when a
genuinely new tab was detected but not adopted (`tabAdoptReason`, `pkg/tools/browser/manager.go:2097-2113`),
and `applyReconcileOutcome` (`pkg/tools/browser/tools.go:346-356`) turns it into text the model
reads. Today the `max_tabs` arm is **actionable**:

> *"the maximum concurrent tabs limit was reached, so it could not be adopted. Close a tab with
> `browser_close_tab` and retry, or tell the user a tab could not be opened."*

`tabAdoptReasonMaxTabs` (`manager.go:2108`, returned at `:2223` and `:2287`) **dies with the
cap**. Without a replacement code, every memory refusal falls to the `default:` arm — *"it could
not be adopted"* — **no reason, no remedy**. An agent that cannot distinguish *the host is out of
memory* from *something went wrong* **retries**, which is exactly the runaway loop this ruling
accepts the risk of, and the retry lands on the same gate.

**FR-063 therefore specifies `tabAdoptReasonMemoryPressure` (`"memory_pressure"`) as a
replacement rather than a deletion**, with its own arm in the switch. Two constraints on the
text: it must name a remedy that exists (*close tabs or browsers you are done with, or wait — the
host is out of memory*), and it must **not** name a limit or a config key, because there is none
left to raise — SC-022's rule, applied to the one operator- and model-facing capacity message
that survives.

#### New requirements this ruling adds

| FR | What it requires |
|---|---|
| **FR-059** | The deletion itself, as implementation scope: both config keys, the reservation machinery, every call site, and the test migration |
| **FR-060** | The pressure gate sits in the **tab-open** path as well as the launch path |
| **FR-061** | Idle close and the pressure gate are the entire defence — neither may silently no-op, and each carries a test that fails if it does |
| **FR-062** | `PER_BROWSER_COST` ≈182 MB is the **launch-headroom minimum**, quoted with its scope; no per-renderer or per-tab constant is used anywhere, and `--renderer-process-limit` appears nowhere in the launch flags |
| **FR-063** | A reason code naming **memory** replaces `tabAdoptReasonMaxTabs`, so the model-visible refusal message can branch instead of falling to *"it could not be adopted"* — see the message defect below |

---

## 1. Overview / Actors / Scope

**Problem.** The browser — its tab set *and* its logins — is owned by the **agent**, so it strands the moment the operator switches who they are talking to. `AgentLoop.browserMgrs` is `map[agentID]*browser.BrowserManager` (`pkg/agent/loop.go::AgentLoop`), populated by a **per-agent** registration loop (`loop.go::registerSharedTools`) that calls `browser.RegisterTools` and then `mgr.AttachSharedChrome(coordinator, agentID)`. `RegisterTools` (`pkg/tools/browser/register.go:41-84`) constructs a manager and **binds it into eleven tool structs** — `&NavigateTool{mgr: mgr}` at `:65` through `&OpenTabTool{mgr: mgr}` at `:81`. Every tool then addresses its tabs through one hardcoded key, `DefaultSessionID = "default"` (`pkg/tools/browser/tools.go:63`). The operator browses with Mia, switches the chat to Jim, and Jim — correctly, for his own manager — reports zero tabs while telling the operator the browser is "shared across the workspace", because five model-visible strings say exactly that (`tabs.go:32,86,143,206`; `tools.go:415`).

**Solution (ADR-072 D1).** Move ownership from the **agent** to the **workspace**:

1. **One Chrome process and one on-disk profile per workspace** (D1.4), replacing ADR-043's single process-wide Chrome and its per-agent CDP browser contexts. This is what isolates cookies, and it is what keeps the live panel capturable.
2. **One `BrowserManager` per workspace**, shared by every agent on that workspace's team, resolved **per tool call** from the turn's context — never captured at registration time.
3. **Every agent on the workspace shares the browser and its logins**, delegated sub-turns included (D1.10).
4. **Inside it, tabs stay per agent** (D1.9a): an agent-opened tab belongs to that agent; an **operator**-opened tab belongs to the workspace and is visible to every agent on it. Point 2 collapses the managers, so this separation has to be carried explicitly on the tab set or it is silently deleted (§0.2a, FR-048).

A login obtained in workspace X is invisible in workspace Y because they are different Chrome profiles. A new chat in the same workspace is still logged in. An agent switch changes nothing.

> **Three claims the previous draft made that are now wrong, recorded so a reader of the git history does not resurrect them.**
> 1. *"The isolation primitive is unchanged — only its key changes."* False (§0.1). The primitive changes from CDP browser context to Chrome process + profile.
> 2. *"Every browser tool passes a session id today, so this is a parameter change."* False. The session id is a parameter; the **manager** is a captured struct field (`register.go:65-81`). Re-keying `browserMgrs` alone leaves handover broken while every map-level test passes (FR-002a).
> 3. *"An unattended delegated sub-turn gets its own signed-out jar."* Reversed by ruling (§0.2). It shares the workspace's browser.

**Actors:**

| Actor | Where | Role under D1 |
|---|---|---|
| `AgentLoop` | `pkg/agent/loop.go` | Owns `browserMgrs`, the coordinator/pool, turn context injection (`::runTurn` sets `tools.WithWorkspaceID` **only when `ts.opts.WorkspaceID != ""`**), the per-agent registration loop (`::registerSharedTools`), the reload prune (`loop.go:2849-2871`) |
| `browser.RegisterTools` | `register.go:41-84` | **Constructs the manager and binds it into 11 tools.** Must stop doing the first half (FR-002a) |
| `BrowserCoordinator` | `coordinator.go:114` (struct), `:226` (ctor) | Today: singular. One `rootCtx`, one `rootCancel`, one `cmd`, one `launched/launching/launchDone`, one launch lock, one ownership marker, one `watchForCrash`, `PID() int`. **Becomes a pool** (FR-037) |
| `BrowserManager` | `manager.go` | Owns the connection, `sessions` (`:338`), the single `browserCtxID` (`:381`, now unused — FR-031), viewer counters (`::ViewerAttached`/`::ViewerDetached`), the reaper (`::ReapIdleSessions`) |
| The eleven browser tools | `register.go:65-81` | Each holds `mgr *BrowserManager`; all eleven must resolve per `Execute` |
| Gateway live surfaces | `browser_webrtc.go`, `browser_ws.go:1252`, `browser_inspect.go:73` | All call `BrowserManagerForAgent(agentID)` — one argument today |
| Capture registry | `browser_webrtc.go:70-78` | `sessions map[string]*browser.CaptureSession // keyed by agentID` (FR-016a) |
| Boot warm path | `gateway.go:3373` `pickWarmBrowserManager`, called `:3562` | Selects by `mgr.AgentID()` and `agents.defaults.default_agent_id` (FR-016b) |
| `controlledResult` | `tools.go:962-963` | `mgr.Live().IsControlled(defaultSessionID)` — hardcoded (FR-002c) |
| `workspace.FindForAgentPreferring` | `find_for_agent.go:176` → `FindForAgent` `:83` | The resolution ladder; sorted-first tie-break documented `:45-48` |
| `ensureDefaultWorkspace` | `rest_workspaces.go:468`, called `gateway.go:5013` | Seeds "My Workspace" with the full built-in roster on **every** boot. This is why a fresh install is **not** workspace-less (§15, MAJ-003 rejected) |

**In scope (D1):**

- **The browser-process pool** — one Chrome + profile dir per workspace browsing key, admitted on **live memory only** (D1.5a), with **LRU eviction** under pressure (D1.7: no error surface, no UI), thrash detection, whole-Chrome idle close, per-Chrome crash containment, and per-Chrome launch locks and ownership markers (FR-037…FR-043, FR-050…FR-054, FR-057…FR-062).
- **The deletion of every counter** (**FR-059**, ADR D1.5a) — `tools.browser.max_tabs`, `tools.browser.max_total_tabs` and the `TryOpenTab`/`ReleaseTab`/`reservedTabs` reservation machinery are **removed from the code**, with their call sites and their 59 test-side references. `max_browsers`/`operator_ceiling` and `--renderer-process-limit` are never built. **This is in §0.4 and belongs to Stream A**, not to the pool. See §0.6.
- **The memory gate as the sole admission control** — the live-pressure check at browser launch *and* at **tab open** (FR-057, FR-060), the measured `PER_BROWSER_COST` launch minimum (FR-062), and the requirement that neither the gate nor idle close may silently no-op, since together they are now the entire defence (FR-061).
- **The pool's lifecycle edges**, which the previous draft left to inference and which are the bulk of round-2's MAJOR findings: the admission edge semantics that survive the counters' deletion (FR-038a); the whole-Chrome idle window as a named config key with a named caller and a specified post-close state (FR-040a); boot reconciliation of the N ownership markers so orphan Chromes cannot sit outside the cap (FR-042a); per-key stale-singleton cleanup, without which FR-043's "the profile survives" is false after a crash (FR-042b); the profile directory's **deletion** path (FR-043a); the profile root's relationship to `cfg.ProfileDir` and to the managed-Chromium install root (FR-037a); and the boot preprovision path, which a lazily-created pool silently breaks (FR-016c).
- **Per-agent tab ownership and the operator's shared tab** (FR-048) — D1.9a. *(The per-agent enforcement of `cfg.MaxTabs` that used to fall out of this is **FR-049, tombstoned**: the key is deleted — §0.6.)*
- **The team-membership elevation-of-privilege disclosure** decided in ADR D2.11 (FR-047): the Workspace → Team editing UI must state, at the point of adding an agent, that the agent gains every live browser session on that workspace. **Claimed here rather than left ownerless.** §1's out-of-scope list excludes only D2.11's *information-disclosure* bullet, so the *elevation-of-privilege* bullet was in scope by wording and owned by neither spec; D1.10 makes it strictly worse than when the ADR wrote it, because unattended delegated work now inherits those logins too.
- One `BrowserManager` per workspace, with **per-`Execute` manager resolution** replacing registration-time binding (FR-002a).
- Every `DefaultSessionID` consumer — **37 non-comment references**, enumerated in §2 — re-pointed at the resolved key; the constant deleted (FR-002b), including `controlledResult` (FR-002c).
- Workspace resolution ladder with **no constant fallback**, a named failure, and a distinguishable gateway/panel reason (FR-007, FR-008, FR-008a).
- Reload-prune liveness keyed by browsing key; per-key idempotent registration (FR-026a, FR-026b).
- **Two-state** `browser_list_tabs` (D1.12 — the third, "not permitted" state is **withdrawn by the ADR** and its whole downstream stack is tombstoned here; see §17 C3), plus the five model-visible description strings with their **replacement literals specified** (FR-013, FR-014, FR-015, FR-034).
- The **write lease, rescoped to agent-vs-agent on the operator's shared tab** — §14 is the single normative definition (FR-019…FR-024, FR-019a).
- Gateway server-side agent→workspace resolution (FR-016, FR-017, FR-018), capture registry re-keying (FR-016a), boot warm path (FR-016b).
- Audit: one event on browser creation and **one per write-class browser tool call** (FR-027, D2.11's ruling of 2026-09-01 — first-use-only is rejected by name), with an event name matching `^[a-z_]+$` (FR-058) and provenance asserted (FR-035).
- Retirement of `tools.browser.capture_shared_context` and the CDP-context machinery it gated (FR-031).
- The six measurement gates (FR-044, FR-045, FR-054, FR-057, FR-057a — §0.3).

**Out of scope (explicitly):**

- Everything in **D2** — role/accessible-name selection, actionability, `browser_select_option` / `browser_press_key` / `browser_hover` / `browser_upload_file` / dialog handling / `browser_snapshot`, tier assignment (D2.8), policy seeding (D2.9), and **only** the D2.11 *information-disclosure* bullet (the snapshot's form-value exposure; D2.11's *elevation-of-privilege* bullet is claimed here as FR-047, and its *repudiation* bullet is already FR-027). **But:** D2's new action tools inherit §14's lease *by rule* (FR-019a), and D2.9's `ask` seed has a prerequisite that D1 makes dangerous (FR-032). **One requirement runs the other way:** §14 rule 3's exemption rule only holds if D2 registers `browser_handle_dialog` ungated by `controlledResult` as well as unleased — see §12 A17, which is the single cross-spec obligation D1 places on D2 beyond deleting its duplicate lease.
- Mid-tool preemption and sustained-contention fairness beyond §14's bounded wait (ADR §6, open).
- Re-keying the `serve_web` preview URL (ADR §6, open; `/preview/<agent>/<token>/` stays agent-scoped). **The registered tool name is `serve_web`** (`pkg/tools/web_serve.go:46`, `const ToolNameWebServe = "serve_web"`); an earlier revision of this spec wrote `web_serve` twice, which is the *file's* subject, not a tool any agent can call.
- Changing the seeded browser policy roster. Jim (`pkg/coreagent/core.go:1052-1064`) and Ray (`:910-921`) keep it; Mia (`:848`) and Ava (`:794`) stay deny-by-default. Operator-confirmed 2026-08-31.
  ⚠️ **One grant inside that roster was made under a premise D1.10 changed, and neither this spec nor §14's table raises it.** Jim holds `browser_evaluate` (`core.go:1058`) and Ray does not (`:910-921`). Before D1.10, Jim's arbitrary JS ran against Jim's own browsing context; under D1.10 it runs against a browser carrying **the operator's live logins for every site the workspace has visited**, and under D1.9a it can reach the operator's own shared tab. **ADR §6 carries this as an open question and this spec does not decide it** — "the seed is unchanged" is a correct statement of scope, not a re-examination of the grant. Recorded so the scope line stops reading as an endorsement.
- Multi-process safety for two gateways on one `$OMNIPUS_HOME` beyond the per-workspace launch lock (§12, A11).
- Migrating existing per-agent browser state (§12, A9 — *discard*; under today's default there is usually nothing to discard).

---

## 2. Existing Codebase Context

### 2.1 Symbols involved

| Symbol | Role | Context (verified 2026-08-31) |
|---|---|---|
| `AgentLoop.browserMgrs` (`loop.go`, field decl ~`:279`) | **modifies** | `map[string]*browser.BrowserManager`, agent-keyed → **browsing-key-keyed**. Its standing comment (`loop.go:270-279`) forbids reverting to a single shared field *"the gateway's live-view WS handler needs a specific agent's manager, not 'whichever agent registered last'"* — that instruction survives the re-key and its replacement text is specified in FR-002d |
| `browser.RegisterTools` (`register.go:41-84`) | **modifies** | Calls `NewBrowserManager` at `:49`, then binds the result into 11 tool structs at `:65-81`. **The single most important omission from the previous draft** (FR-002a) |
| The 11 tool structs (`register.go:65-81`) | **modifies** | `NavigateTool`, `ClickTool`, `TypeTool`, `ScreenshotTool`, `GetTextTool`, `WaitTool`, `EvaluateTool`, `ListTabsTool`, `SwitchTabTool`, `CloseTabTool`, `OpenTabTool` — every one carries `mgr *BrowserManager` |
| `AgentLoop.BrowserManagerForAgent` (`loop.go::BrowserManagerForAgent`) | **modifies** | **One argument today** (`browser_inspect.go:73`, `browser_ws.go:1252`). Gains `preferredWorkspaceID` (FR-017) |
| `AgentLoop.BrowserManagers()` | **modifies** | Snapshot slice; membership changes with the re-key. **Three consumers, not one:** the 1-minute idle sweep (`gateway.go:5342`), gateway `Close()`, and — the one the previous draft missed — boot `Preprovision` (`gateway.go:2286`). See the `Preprovision` row below and FR-016c |
| reload prune (`loop.go:2849-2871`) | **modifies** | `registeredAgentIDs := registry.ListAgentIDs()` at `:2849`; `coord.RemoveAgent(id)` at `:2866`. **Its membership predicate is agent ids** — a workspace-keyed map matches nothing and every browser is destroyed on every Settings save (FR-026a) |
| registration loop (`loop.go::registerSharedTools`) | **modifies** | Runs `RegisterTools` + `AttachSharedChrome` + `prior`/`Release`/`Shutdown` **once per agent**. N agents on one workspace ⇒ N teardown/replace cycles against one key (FR-026b) |
| `BrowserCoordinator` (`coordinator.go:114-226`) | **modifies (structural)** | Singular by construction: one `rootCtx`/`rootCancel`/`cmd`, one `launched`/`launching`/`launchDone` single-flight, `PID() int` (`:849`), one `watchForCrash` (`:1357`), one launch lock at `cfg.ProfileDir/shared-chrome.lock` (`:1424`), one ownership marker at `$OMNIPUS_HOME/browser/shared-chrome.pid` (`:1527`). **Becomes a pool** (FR-037, FR-042) |
| `BrowserCoordinator.Register` (`:311`) | **modifies** | `(ctx, agentID, mgr) → (rootCtx, browserCtxID, err)`; the `capture_shared_context` short-circuit at `:349-359` and the CDP-context branch below it both **retire** (FR-031) |
| `BrowserCoordinator.RemoveAgent` (`:542`), `disposeBrowserContextRaw` (`:585`), `contextCount()` (`:1111`) | **modifies / retires** | Disposal becomes "close this workspace's Chrome", not "dispose a CDP context" (FR-031, FR-040) |
| `BrowserCoordinator.launchChrome` (`:1212`) / `ensureLaunched` (`:1127`) | **modifies** | Per-key, single-flight per key |
| `BrowserCoordinator.watchForCrash` (`:1357`) | **modifies** | Today resets **every** connector manager and relaunches the one Chrome. Must become per-Chrome (FR-041) |
| `pipeLaunchConfig.userDataDir` (`exec_resolver.go:385` `UserDataDir: cfg.userDataDir`) | **modifies** | The seam D1.4's isolation rides on: one profile dir per workspace (FR-037) |
| `maxTotalTabs` / `TryOpenTab` / `ReleaseTab` / `reservedTabs` (`coordinator.go:128, 137, 635-644, 782-812, 818`) | **RETIRES** | A global cross-agent **tab** budget, unlimited by default and **never seeded**, plus the reservation machinery that exists only to enforce it (and `max_tabs`), including its in-flight race handling. **Deleted outright by ADR D1.5a** — not joined by a browser cap, because there is no cap (FR-059, §0.6). The manager-side halves `reserveGlobalTab` (`manager.go:3343-3352`) and `releaseGlobalTab` (`:3358-3366`) and their four call sites (`tabs.go:180, 249, 260`; `manager.go:3118`) go with them |
| `BrowserManager.browserCtxID` (`manager.go:381`) | **retires** | Stays a single field and becomes permanently empty — every manager drives its own Chrome's default context (FR-031). **Not a map** (§0.2) |
| `BrowserManager.sessions` (`manager.go:338`) | **modifies** | `map[string]*sessionEntry`. **NOT one entry per manager.** Under D1.9a the manager holds **one entry per agent that has browsed, plus one workspace-owned entry for the operator's tabs** — the map is where the agent dimension is carried (FR-048). A design that keeps a single entry per manager silently merges every agent's tabs on the workspace |
| `sessionEntry.tabs` (`manager.go:203-204`, `tabs []*tabEntry`) | **reuses** | **The verified trap (§0.2a).** The tab set belongs to the *sessionEntry*, i.e. to the browsing context (ADR-041 D1) — not to the agent. Agents are separated today only because each has its own manager. FR-001 removes that separation; FR-048 restores it explicitly |
| `BrowserManager.totalTabCountLocked` (`manager.go:1549-1555`) | **RETIRES as an enforcement point** | Sums `len(se.tabs)` across **every** session in the manager, and is the `cfg.MaxTabs` enforcement point at **five** sites: `:1139` (`createFirstTab`), `:2005` and `:2047` (`OpenTab`), `:2216` and `:2286` (`adoptTarget`). Revision 4's FR-049 re-scoped it to the agent's own tab set; **D1.5a deletes the cap instead, so FR-049 is tombstoned** (§0.6). **Those same five sites are where FR-060 puts the memory gate** — the tab-open path a runaway loop never leaves. The helper itself may survive as a count for logging/telemetry; what must not survive is a *refusal* derived from a counter |
| `BrowserConfig.MaxTabs` (`manager.go:36`, default **5** at `:124`; config key `tools.browser.max_tabs`, `config.go:3633`, applied `loop.go:2314-2315`) | **RETIRES** | Revision 4 gave it back an owner (FR-049) after the re-key would have turned a 5-tab per-**agent** cap into 5 for the whole team. **ADR D1.5a deletes the key, the field, the default and all 18 executable references instead** (FR-059). Its config doc — *"the per-agent courtesy cap … the guard most operators actually want"* (`config.go:3662-3663`) — is deleted with it; a doc for a key that no longer exists is worse than none |
| `LiveViewRegistry.TakeControl` / `IsControlled` (`live.go::TakeControl` `:1241`, `::ReleaseControl` `:1287`, `::Controller` `:1298`, `::IsControlled` `:1313`) | **reuses (must NOT be replaced)** | ADR-038 D6's take-the-wheel lock, and **the whole of operator-vs-agent arbitration under D1.9a**. §14's lease is agent-vs-agent only and never substitutes for this. *(Citation corrected: the round-4 brief gave `live.go:1236-1310`, which stops three lines short of `IsControlled` at `:1313`.)* |
| `BrowserManager.AttachSharedChrome` (`manager.go:537`) | **modifies** | Sets `m.agentID` (`:375`) — the coordinator's Register/Release/RemoveAgent key. Becomes the browsing key |
| `BrowserManager.ListTabs` (`manager.go:1605`) | **modifies** | `return nil, 0, nil` on a missing session (`:1609-1611`) — the two-state collapse (FR-013) |
| `BrowserManager.sessionExists` (`manager.go:2378`) | **reuses** | Already backs `browser_started` (`tabs.go:58`) — the existing half of D1.12 |
| `BrowserManager.ViewerAttached` / `ViewerDetached` (`manager.go::ViewerAttached`, `::ViewerDetached`; `se.viewers++` / `--` in their bodies) | **extends** | No exported count accessor exists → FR-010 adds `Viewers()`, used by the **reaper and the pool's idle-close**, not for attendance (§0.2) |
| `BrowserManager.ReapIdleSessions` (`manager.go:2986`) | **extends** | Per-tab TTL, `se.viewers > 0` pin, zero-tab `emptySince` branch — all implemented. **CORRECTED (round-2 CRIT-102):** the previous draft claimed here and in §15 that this method "deletes `m.sessions` entries only; it never touches the coordinator and never closes a browser", marked *verified*. **That was false.** It collects `se.browserCancel` into `reapedBrowsers` in **both** removal branches (`:3027-3032` stranded-empty-session, `:3073-3078` all-tabs-idle), executes those cancels after unlocking (`:3123-3125`) — cancelling the **browser-owning** chromedp context — cancels per-tab contexts at `:3106-3107`, and reaches the coordinator through `m.releaseGlobalTab()` (`:3118` → `:3358-3365` → `coord.ReleaseTab(agentID)`). **The narrow claim that survives, and it is the only one this spec relies on:** `ReapIdleSessions` never calls `RemoveAgent` and never calls `disposeBrowserContextRaw`, so the coordinator's own per-key state and the Chrome **process** are untouched. Whole-Chrome close is genuinely new work (FR-040), but the disposal machinery is not absent, and the reaper↔pool contract is therefore a real interaction that FR-040a must specify — not a greenfield hook |
| the idle sweep goroutine (`gateway.go:5321-5352`, `const reapInterval = time.Minute`) | **extends** | The **named caller** the previous draft never identified. It ranges `agentLoop.BrowserManagers()` and calls `mgr.ReapIdleSessions()` on a 1-minute ticker, each tick individually `recover()`ed. FR-040a hangs the whole-Chrome idle close off this same tick, after the per-manager loop. The interval doc (`:5310-5320`) states the invariant a second TTL must also respect: the sweep interval must stay well under the TTL or the TTL becomes a floor |
| `browser.BrowserBuiltinMetadata` (`metadata.go:36-51`) | **modifies** | **Absent from the previous draft entirely (round-2 MAJ-105).** Constructs **all eleven tool structs a second time**, with a nil manager, for the central `BuiltinRegistry`. FR-002a's struct-shape change breaks this file too, and it is the second construction site nobody would find from `register.go` |
| `CaptureSession` (`capture_session.go:258`), `LiveViewRegistry` (`live.go:322`), `LiveView` (`live.go:1324`) | **reuses (must NOT be "fixed")** | Three **non-tool** exported structs that legitimately hold a `*BrowserManager` field. They are not tools and must keep it; test 4's predicate is scoped to types implementing `tools.Tool` for exactly this reason (FR-002a). `BrowserCoordinator.managers` (`coordinator.go:186`) is a fourth. `LiveViewRegistry`'s doc comment (`live.go:316-320`) additionally asserts *"a BrowserManager is itself scoped to one agent (pkg/agent/loop.go's per-agent manager map, ADR-038 D4)"* — false after the re-key, and corrected with it (FR-002d) |
| `BrowserManager.Preprovision` (`manager.go:3218-3223`) / `AgentLoop.BrowserManagers()` (`loop.go::BrowserManagers`) | **modifies** | **The boot role the previous draft's `BrowserManagers()` row omitted (round-2 MAJ-104).** `gateway.go:2286` ranges `BrowserManagers()` in a goroutine per manager to call `Preprovision`, which starts the managed-Chromium download **at boot** instead of at the first browser tool call — the explicit purpose recorded in `BrowserManagers()`' own doc comment. Under a lazily-created per-workspace pool that snapshot is **empty at boot**, so `Preprovision` never fires and a fresh install's first browser call blocks on a multi-hundred-megabyte download. Fixable cleanly: `Preprovision`'s entire body is `if m.cfg.CDPURL != "" { return "", nil }; return m.resolveExecPath(ctx)` — it resolves a binary path from config and needs **no** key, no manager and no live Chrome (FR-016c) |
| `cleanStaleSingletons` (`coordinator.go:1488-1498`, called `:1235`) | **modifies** | **Zero mentions in the previous draft (round-2 MAJ-103).** Removes Chromium's stale `SingletonLock`/`SingletonCookie`/`SingletonSocket` after an ungraceful exit, and is called with `c.cfg.ProfileDir` **only**. Each per-key profile gets its own singleton files; nothing would clean them, so after a crash that workspace's next Chrome launch fails and **FR-043's "the profile survives, so the login survives" is false in the exact case it exists for** (FR-042b) |
| `BrowserCoordinator.ApplyRuntimeConfig`'s ProfileDir branch (`coordinator.go:681-687`) | **reuses** | Today a `profile_dir` change on reload logs a WARN and is **not applied** to the running Chrome (*"applies after gateway restart"*). FR-037a preserves that behaviour per key rather than inventing a relocation (§12 A18) |
| `resolveScheduleWorkspaceID` (`pkg/gateway/schedules.go:581-639`) → `stampScheduledSessionWorkspace` (called from `pickSession`, `:513`+`:527-576`, itself called on every fire at `:141`) | **reuses** | **The shipped write path the previous draft did not cite, and whose absence made FR-033's premise wrong (round-2 MAJ-113).** A fired schedule's workspace is resolved and stamped onto the session meta **before** `ProcessScheduled` runs, and `ProcessScheduled` reads it back into `processOptions.WorkspaceID` (`loop.go:6934-6946`, assigned `:6957`). **Heartbeats are workspace-scoped by construction:** the reconciler names each job `heartbeat:<workspaceID>:<agentID>` (`heartbeat_schedule.go:30-33`) and `workspaceIDFromHeartbeatJobName` (`schedules.go:654`) parses it back out — so a heartbeat turn reaches `ResolveBrowsingKey` **rung 1**, never rung 2's ambiguity refusal. See §6 and §16 MAJ-113 for what this leaves as the real residual case |
| `BrowserConfig.PageTimeout` (`manager.go:35`, default 30s `:123`, config key `tools.browser.page_timeout` → `PageTimeoutSec`, `config.go:3632`, applied `loop.go:2311-2312`) | **reuses** | The "shortest action-tool timeout" §14.1 required `leaseWaitTimeout` to be strictly less than, and never named. Both values are operator-settable and nothing validates the relationship (FR-023a) |
| `maxTotalTabs`' unlimited semantics (`coordinator.go:785-788`) | **RETIRES with the key** | `if c.maxTotalTabs <= 0 { reserve; return true }` — 0 and negative meant **unlimited**, guarded by `TestCoordinator_UnlimitedDefault_AllowsPastOldCap`. Revision 4 used it as the *shape* precedent for `max_browsers`' ceiling; **there is no `max_browsers` and no ceiling** (FR-056 tombstoned), so the precedent has nothing left to precede. The guard test is deleted with the key (§10.1) |
| `controlledResult` (`tools.go:962-963`) | **modifies** | `mgr.Live().IsControlled(defaultSessionID)` — hardcoded. Silently returns `false` forever once the live registry is re-keyed (FR-002c) |
| `ListTabsTool` (`tabs.go:28-68`) | **modifies** | Already returns `browser_started` (`:58`, `:66`); description at `:31-39` |
| `captureRegistry` (`browser_webrtc.go:70-78`) | **modifies** | `sessions map[string]*browser.CaptureSession // keyed by agentID` (FR-016a) |
| `pickWarmBrowserManager` (`gateway.go:3373`, called `:3562`) | **modifies** | Selects by `mgr.AgentID()`, preferring `agents.defaults.default_agent_id`, else lexicographically-first agent id. Both halves break under the re-key (FR-016b) |
| `config.CaptureSharedContext` (`config.go:3849`, seeded `defaults.go:671`) | **retires** | FR-031. Its doc comment (`config.go:3800-3849`) also directs operators to the ADR-061-deleted JPEG fallback and must go with it |
| `tools.ToolWorkspaceID` (`pkg/tools/base.go:250`) | **reuses** | Set only when `ts.opts.WorkspaceID != ""` |
| `tools.ToolTranscriptSessionID` (`base.go:200`) | **not used** | §0.2 — not a browsing key |
| `workspace.FindForAgentPreferring` (`find_for_agent.go:176`) | **reuses** | Preferred-id fast path → `FindForAgent` (`:83`); sorted-first tie-break + WARN documented at `:45-48` |
| `ensureDefaultWorkspace` (`rest_workspaces.go:468`) / `defaultWorkspaceTeam` (`rest_workspace_delegation.go:359-379`) | **reuses** | Seeds "My Workspace" with `coreagent.All() ∩ configured agents` — Jim and Ray included — on every boot (`gateway.go:5013`) |
| `pkg/agent/tool_denial.go:206-210` | **NOT modified — and this row is retained to say so** | `policy_denied` → `ModelMessage: "Tool execution denied by policy."`, generic for every tool. Revision 3 listed it as **modifies** for FR-014a. **FR-014a is withdrawn (ADR D1.12, §17 C3)** and this file is untouched: `FilterToolsByPolicy` (`pkg/tools/compositor.go:436-438`) `continue`s past a deny verdict, so a browser tool a policy denies is never sent to the model and this message has **no production caller** for it |
| `AgentLoop` `AutoDenyAsk` (`loop.go:594-599`, honoured at `loop.go::runTurn`'s `ts.opts.AutoDenyAsk` branch) | **reuses** | Set true only for headless/scheduled runs (`loop.go:6958`); **not inherited by delegated sub-turns — issue #659** (FR-032) |
| `BrowserAttachFrame.yaml`, `BrowserWebRTCOfferFrame.yaml`, `BrowserInspectRequest.yaml` | **modifies (prose)** | See FR-016/FR-017 and §15 MAJ-004 — one of the three is a *semantic* reversal, not a cosmetic edit |

### 2.2 Every `DefaultSessionID` consumer (the enumeration the previous draft was missing)

**37 non-comment references**, all of which must address the resolved browsing key (FR-002b). Counted with `grep -rn "DefaultSessionID\|defaultSessionID" pkg --include "*.go" | grep -v "_test\.go"` and filtering comment lines:

| File | Executable references | Count |
|---|---|---|
| `pkg/tools/browser/tools.go` | `:127, :236, :295, :433, :517, :659, :776, :883`, plus `controlledResult` at `:963` | 9 |
| `pkg/tools/browser/tabs.go` | `:58, :59, :117, :175, :258, :278` | 6 |
| `pkg/tools/browser/inspect.go` | `:158` (payload field), `:164`, `:194` | 3 |
| `pkg/tools/browser/capture_session.go` | `:518`, `:727` | 2 |
| `pkg/tools/browser/live.go` | `:380` (the empty-session-id default) | 1 |
| `pkg/gateway/browser_ws.go` | `:1266` Attach, `:1396` Input, `:1549` TakeControl, `:1564` ReleaseControl, `:1622` Controller, `:1637` SwitchTab, `:1647` CloseTab, `:1652` OpenTab, `:1724` Controller, `:1725` Detach, `:1858` Controller, `:1907` SetViewport, `:1945` CSSViewport | 13 |
| `pkg/gateway/browser_webrtc.go` | `:561` CSSViewport, `:1026` Input | 2 |
| `pkg/gateway/gateway.go` | `:3609` (warm path `mgr.Session`) | 1 |
| **Total** | | **37** |

**The arithmetic, corrected (round-2 MIN-101).** The quoted command returns **57** lines, not 37, and the previous draft's "12 comment references" was wrong:

> **57 grep hits = 37 usages + 2 declarations + 18 comments.**

- **2 declarations:** `tools.go:63` (`DefaultSessionID`) and `tools.go:69` (the package-private alias `defaultSessionID`).
- **18 comments:** `tools.go:50, :62, :65, :68`; `tabs.go` — none; `inspect.go:116`; `capture_session.go:708`; `live.go:376`; `browser_ws.go:154, :1207, :1214, :1437, :1526, :1579, :1619, :1722, :1832`; `gateway.go:3602`; and **`pkg/config/config.go:3892`**, a file the previous draft's inventory omitted entirely (it is comment-only — one hit, no executable reference — which is why the per-file table above still has no row for it).

Per-file the raw hits are: `tools.go` 15 (9 exec + 2 decl + 4 comment), `browser_ws.go` 22 (13 exec + 9 comment), `tabs.go` 6, `inspect.go` 4 (3 + 1), `capture_session.go` 3 (2 + 1), `live.go` 2 (1 + 1), `browser_webrtc.go` 2, `gateway.go` 2 (1 + 1), `config.go` 1 (comment). The 37 executable line numbers in the table above were **each re-checked individually and all 37 are correct**.

**Stream E's previous scope named three call sites.** It was short by an order of magnitude, and the three it named were the `BrowserManagerForAgent` calls, not these.

### 2.2a The test surface — 364 references, and the previous draft budgeted none of them

Round-2 CRIT-101/MIN-102. `grep -rc "DefaultSessionID\|defaultSessionID" pkg --include "*_test.go"` returns **364 references across 25 test files** — roughly **ten times** the non-test surface SC-013 measures, and none of it appeared anywhere in the previous draft's estimate, streams or regression list.

| File | Refs | | File | Refs |
|---|---|---|---|---|
| `pkg/tools/browser/tabs_test.go` | 100 | | `pkg/tools/browser/live_tabchange_coordination_test.go` | 7 |
| `pkg/tools/browser/tab_adoption_e2e_test.go` | 41 | | `pkg/tools/browser/live_deadlock_test.go` | 7 |
| `pkg/tools/browser/idle_reaper_test.go` | 33 | | `pkg/tools/browser/tools_control_test.go` | 6 |
| `pkg/tools/browser/switch_tab_same_index_recapture_test.go` | 26 | | `pkg/tools/browser/inspect_test.go` | 5 |
| `pkg/tools/browser/reaper_edge_test.go` | 26 | | `pkg/tools/browser/coordinator_review_test.go` | 4 |
| `pkg/tools/browser/focus_emulation_test.go` | 24 | | `pkg/tools/browser/live_test.go` | 3 |
| `pkg/tools/browser/switch_tab_activation_test.go` | 20 | | `pkg/gateway/browser_ws_fixwaveb_test.go` | 3 |
| `pkg/tools/browser/switch_tab_capture_chain_test.go` | 18 | | 5 files × 2 refs | 10 |
| `pkg/tools/browser/reaper_lifecycle_test.go` | 15 | | 5 files × 1 ref | 5 |
| `pkg/gateway/browser_ws_test.go` | 15 | | **Total** | **364** |

**This is the finding that made §10.1 unsatisfiable, and it is a compile error, not a style point.** FR-002b **deletes** both symbols. §10.1 then required four of these files — `tab_adoption_e2e_test.go` (41), `idle_reaper_test.go` (33), `reaper_edge_test.go` (26), `reaper_lifecycle_test.go` (15), **115 references between them** — to keep passing **unmodified**. A file referencing a deleted identifier does not fail an assertion; it fails to build, and takes the whole package's test binary with it. The two requirements could not both hold.

**Resolution (FR-002e, and §10.1's wording is corrected to match).** The migration is **mechanical and in scope**, and it is not permitted to be done with an alias:

1. Every test-side `DefaultSessionID`/`defaultSessionID` argument is re-pointed at a browsing key the test constructs through the same seam production uses. Tests get a `newTestBrowsingKey(t, workspaceID)` helper in the package's existing test support file — **not** an exported literal constructor, which §5 forbids.
2. **No `defaultSessionID` alias survives in test code either.** SC-013 counts non-test references, so a test-only alias would leave the constant alive, the reaper suite asserting against a key nothing in production uses, and SC-013 unmeasurable while reading zero. The structural test (test 5) is therefore **repo-wide including `_test.go` files**, with exactly one allowed exception: the migration helper's own doc comment.
3. **Semantics unmodified, not text unmodified.** §10.1's regression bar for these files is now: *the session-id argument is mechanically re-pointed at the browsing key; no assertion is weakened, deleted, skipped or `t.Skip`-guarded; the test count per file is unchanged or higher.* A diff that changes anything else in these files is a finding.
4. **Ordering:** the migration lands in the **same commit** as FR-002b's deletion. A commit that deletes the constant and leaves the tests for a follow-up leaves `pkg/tools/browser` and `pkg/gateway` unbuildable, so CI's verdict on every other gate becomes meaningless.

Effort, stated rather than implied: ~364 mechanical edits across 25 files, of which `tabs_test.go` alone is 100. This is the single largest line-count item in the change and it belongs to **Stream A**, not to Stream G.

### 2.3 Impact assessment

| Symbol modified | Risk | Direct dependents (d=1) | Indirect (d=2) |
|---|---|---|---|
| `BrowserCoordinator` → pool | **CRITICAL** | `Register`, `Release`, `RemoveAgent`, `ensureLaunched`, `launchChrome`, `watchForCrash`, `PID`, launch lock, ownership marker | crash recovery, hot reload, boot warm, every live surface, host memory |
| **Counter deletion** (`max_tabs`, `max_total_tabs`, `TryOpenTab`/`ReleaseTab`/`reservedTabs`) | **HIGH** | `coordinator.go` (7 sites), `manager.go` (13 sites incl. 5 enforcement points), `tabs.go` (3), `config.go` (2 fields), `loop.go` (3); **59 test-side references across 10 files** | **The only remaining bound becomes memory** — a gap in FR-057/FR-060/FR-061 is not a looser limit, it is none (§0.6) |
| `browserMgrs` keying | **CRITICAL** | `BrowserManagerForAgent`, `BrowserManagers`, reload prune, `Close` | all 3 gateway surfaces, every browser tool, reaping |
| `RegisterTools` manager binding | **CRITICAL** | 11 tool structs, `registerSharedTools` | **handover itself** — the reported defect |
| reload prune predicate | **CRITICAL** | `RemoveAgent` → browser close | every login on every workspace, on every Settings save |
| session-id argument at every consumer | **HIGH** | 37 sites (§2.2) | live view attach, control lock, tab strip, capture target |
| `controlledResult` | **HIGH** | 7 gated action tools | **ADR-038 D6 take-the-wheel — a shipped safety property** |
| gateway `BrowserManagerForAgent` | **HIGH** | `browser_webrtc.go`, `browser_ws.go:1252`, `browser_inspect.go:73` | capture registry, panel attach |
| `ListTabs` return shape | MEDIUM | `ListTabsTool`, `browser_inspect` | model-visible answer — the §1.1 defect |
| `capture_shared_context` retirement | MEDIUM | `Register`, `SetCaptureSharedContext`, `CaptureSharedContextEnabled`, `ApplyRuntimeConfig`, `coordinator_test.go:328` | operator config compatibility |

**Verified numeric corrections to the ADR:** the phrase "the shared browser session" occurs **8** times in the tree — **5** model-visible (`tabs.go:32,86,143,206` descriptions + `tools.go:415` parameter description), **2** Go comments (`tabs.go:19,186`), **1** unrelated SPA comment (`src/store/ui.ts:135`). The ADR says "six tool descriptions". Use the enumerated list, not the count.

---

## 3. Implementation Streams

Seven streams (A, P, C, D, E, F, G). **Stream A is the critical path and must land its interface first**; the rest code against it. **Stream P (the pool) is gated on G-1 and G-2 (§0.3)** and is the largest.

### 3.1 Shared interface contract (Stream A's first commit)

```go
// pkg/tools/browser/key.go (new)

// BrowsingKey is the identity of ONE browser: one Chrome process, one profile
// directory, one BrowserManager, one tab set. It replaces the DefaultSessionID
// constant as the thing every browser tool addresses. Constructed ONLY by
// ResolveBrowsingKey — there is deliberately no exported literal constructor
// and no zero-value default, so a caller cannot mint a shared browser by
// accident (ADR-072 D1.11).
//
// There is exactly ONE shape: "ws:<workspaceID>". The D1.10 ruling (2026-08-31)
// removed the unattended shape; do not reintroduce a second kind without
// reopening that ruling.
//
// A BrowsingKey names a BROWSER, not a tab set. Under D1.9a (2026-09-01) the
// tab sets live one level down, inside the manager this key resolves to: one
// per agent, plus one owned by the workspace for the operator's own tabs.
// See TabOwner and FR-048 — a key alone does not tell you whose tabs you are
// looking at, and code that assumes it does merges every agent's tabs.
//
// The "ws:" prefix is RETAINED DELIBERATELY, and not as future-proofing for a
// second shape — §5 forbids adding one (round-2 MIN-103). It is a namespace
// marker: a workspace id is a bare ULID, and a bare ULID appearing in an audit
// event, a WARN line or a LiveKeys() dump is indistinguishable from a session
// id, a task id or an agent id, all of which are ULIDs in this system and all
// of which appear in the same log lines. "ws:01J..." is self-describing at the
// point a human reads it, which is the only place it matters. WorkspaceID()
// exists for the two consumers that need the bare id back — the profile
// directory path (FR-037) and the audit event's workspace field (FR-027).
type BrowsingKey struct{ s string }

func (k BrowsingKey) String() string { return k.s }
func (k BrowsingKey) IsZero() bool   { return k.s == "" }

// WorkspaceID returns the workspace this key names, without the prefix. Used
// by audit and by the profile-directory path; never a branch in isolation
// logic. Its result is a path segment (FR-037), so §5's path-segment
// invariant applies to it.
func (k BrowsingKey) WorkspaceID() string

// ErrNoBrowsingContext is the D1.11 named failure. It MUST be returned — never
// swallowed into a shared browser, never mapped to a constant, never
// nil-with-empty. Its Error() text is a behavioural contract (FR-008).
var ErrNoBrowsingContext = errors.New(
    "browser: this turn is not rooted in a workspace, so it has no browser of its own; " +
        "add this agent to a workspace's team, or run the request in a workspace chat")

// TabOwner names WHOSE tab set a browser operation addresses, inside the one
// browser a BrowsingKey names (ADR-072 D1.9a, operator ruling 2026-09-01).
// This type is the explicit carrier of the agent dimension that today lives
// only in the accident of one BrowserManager per agent — FR-048.
//
// Two shapes, and deliberately no third:
//
//   TabOwnerAgent(agentID)  the tabs that agent opened.  Visible and drivable
//                           by that agent only.
//   TabOwnerWorkspace       the tabs the OPERATOR opened through the live
//                           panel.  Visible to every agent on the workspace;
//                           drivable by the operator, and by an agent on
//                           request (the acquisition verb is a D2 decision —
//                           §0.5 E-1).
//
// It resolves to the manager's sessions-map key, so the map holds one entry
// per agent that has browsed plus at most one workspace entry. There is no
// "all tabs" owner: a tool that wants both sets asks for both and says which
// is which, because "whose tab is this" is exactly the question ADR-072 §1.1
// records an agent getting wrong.
type TabOwner struct{ s string }

func TabOwnerAgent(agentID string) TabOwner
func TabOwnerWorkspace() TabOwner
func (o TabOwner) IsWorkspace() bool

// sessionKey is the manager-level lookup: one BrowsingKey plus one TabOwner.
// It is what replaces DefaultSessionID at every call site (FR-002b) — NOT the
// BrowsingKey on its own, which would merge the team's tabs (§0.2a).
func sessionKey(k BrowsingKey, o TabOwner) string

// NOTE — there is deliberately NO errBrowserPoolFull and NO errPoolFull.
//
// Revision 3 of this spec specified a hard refusal at the cap, a sentinel
// error, a formatted message naming two remedies, and an operator-facing REST
// close action to make one of those remedies real. ALL OF IT IS WITHDRAWN by
// the operator ruling recorded in ADR-072 D1.7: "there is no 'pool full' error
// surface and no UI change" — at the cap the pool evicts the least recently
// used instance and launches. Closing a browser is not destructive, because
// the logins live in the profile directory on disk, so an evicted workspace
// reopens still signed in and pays only start-up latency.
//
// The ONE place an operator can still perceive capacity is FR-053's named
// error, and under ADR-072 D1.5a it names MEMORY rather than a cap: memory
// pressure is above the gate's threshold AND nothing is evictable, so the
// request waits for its own deadline and then fails. There is no cap to
// overshoot and no ceiling to raise -- every counter is deleted (§0.6). That
// error is a tool error, not a UI, not a REST path and not a sentinel anyone
// else tests for. Do not reintroduce a pool-full error surface on the normal
// path; see §5 and §17 C1.
```

```go
// pkg/tools/browser/resolve.go (new) — the SINGLE resolution point.

// ResolveBrowsingKey decides which browser this turn's tools address. It is the
// ONLY function permitted to construct a BrowsingKey. Deterministic, pure apart
// from the workspace-file read FindForAgentPreferring performs.
//
// Ladder (ADR-072 D1.11), evaluated in order — three rungs, no fourth:
//   1. tools.ToolWorkspaceID(ctx) != ""   -> ws:<that id>
//   2. workspace.FindForAgentPreferring(home, tools.ToolAgentID(ctx), "")
//      resolves UNAMBIGUOUSLY                -> ws:<resolved id>
//   3. otherwise                             -> zero key + ErrNoBrowsingContext
//
// "Unambiguously" is load-bearing and is FR-033: when the agent is on the
// CoreTeam of two or more workspaces and no preferred id was supplied, the
// sorted-first tie-break that FindForAgent applies for FILESYSTEM re-rooting is
// NOT applied here, because here it would silently choose which set of live
// logins the turn acts with. Rung 2 refuses instead, with ErrNoBrowsingContext,
// and logs a WARN naming the candidates.
//
// Rung 1+2 are the pkg/tools/resolvepath.go:713 precedent so the browser and
// the work dir never disagree about which workspace a scheduled/heartbeat turn
// is rooted in. There is NO rung 4: a fallback constant re-creates the exact
// isolation regression ADR D1.11 rejects.
func ResolveBrowsingKey(ctx context.Context, home string) (BrowsingKey, error)
```

```go
// pkg/tools/browser/register.go — the manager is RESOLVED, not captured.
// This is FR-002a and it is the fix for the reported defect.

// ManagerResolver hands a tool the BrowserManager for the turn it is executing
// in. Implemented in pkg/agent over ResolveBrowsingKey + BrowserManagerForKey;
// faked in pkg/tools/browser tests. The interface lives in pkg/tools/browser
// and is implemented in pkg/agent because the import direction forbids the
// reverse — pkg/tools/browser cannot import pkg/agent.
type ManagerResolver interface {
    // ManagerFor resolves this turn's browser, launching it on first use —
    // evicting another workspace's instance when memory pressure says stop and
    // something is reclaimable (FR-050). Returns ErrNoBrowsingContext, FR-053's
    // memory-refusal error, or a launch error. It NEVER returns "pool full" —
    // there is no pool cap at all (D1.5a, §0.6).
    //
    // It also returns the TabOwner this turn addresses: TabOwnerAgent(agentID)
    // for every ordinary tool call. A tool that must reach the operator's
    // shared tab asks for TabOwnerWorkspace() explicitly (FR-048).
    ManagerFor(ctx context.Context) (*BrowserManager, BrowsingKey, TabOwner, error)
}

// RegisterTools takes a resolver instead of constructing a manager. No tool
// struct holds a *BrowserManager after this change (FR-002a's structural test).
func RegisterTools(
    registry *tools.ToolRegistry,
    res ManagerResolver,
    evaluateEnabled bool,
    agentHome string,
    restrict bool,
) error
```

```go
// pkg/agent/loop.go — manager lookup replaces the per-agent map.

// BrowserManagerForKey returns (creating on first use) the manager that owns
// key's browser. Exactly one manager and one Chrome per key, process-wide.
// Under memory pressure it EVICTS the least recently used evictable instance
// and launches (FR-050); it does not refuse while something is reclaimable. It
// errors only for ErrNoBrowsingContext, a launch failure, or FR-053's memory
// refusal (pressure high AND nothing evictable). There is no cap.
func (al *AgentLoop) BrowserManagerForKey(ctx context.Context, k browser.BrowsingKey) (*browser.BrowserManager, error)

// BrowserManagerForAgent is RETAINED for the gateway. It resolves
// agentID -> BrowsingKey server-side using preferredWorkspaceID (from the
// attaching chat session's meta, FR-017) and delegates to BrowserManagerForKey.
// The second return distinguishes the failure reasons the panel must show
// differently (FR-008a) — it is NOT a bare bool.
func (al *AgentLoop) BrowserManagerForAgent(
    ctx context.Context, agentID, preferredWorkspaceID string,
) (*browser.BrowserManager, BrowserResolveOutcome)

// BrowserResolveOutcome is a closed enum. "not registered" and "no workspace"
// are DIFFERENT operator-facing problems and were indistinguishable before
// (browser_inspect.go:75-77 reported the former for both).
type BrowserResolveOutcome int
const (
    BrowserResolveOK BrowserResolveOutcome = iota
    BrowserResolveNoWorkspace   // ErrNoBrowsingContext
    BrowserResolveAmbiguous     // FR-033: >1 candidate workspace, no preference
    BrowserResolveNotRegistered // browser tools genuinely not registered
)

// NO BrowserResolvePoolFull. Revision 3 had one; D1.7 withdrew the state it
// named and D1.5a deleted the cap that would have produced it. Under eviction
// the panel never has a capacity reason to render, and FR-053's memory refusal
// is a tool error on an agent's turn, not a panel state.
```

```go
// pkg/tools/browser/pool.go (new) — ADR-072 D1.4 (mechanism), D1.5a (memory is
// the only limit), D1.7 (eviction policy), D1.8 (lifecycle). Stream P.
//
// D1.5's cap arithmetic and D1.6's renderer floor are SUPERSEDED by D1.5a and
// are not implemented: there is no max_browsers, no operator_ceiling, no
// --renderer-process-limit and no per-renderer constant. See §0.6.

// BrowserPool owns N Chrome processes, one per BrowsingKey, replacing the
// coordinator's single-Chrome fields (rootCtx/rootCancel/cmd/launched/
// launching/launchDone/watchForCrash/PID and the launch lock + ownership
// marker, all of which become per-key).
//
// Isolation is the PROFILE DIRECTORY, not a CDP browser context: each Chrome
// launches with its own --user-data-dir, threaded through the existing
// pipeLaunchConfig.userDataDir seam (exec_resolver.go:385).
//
// PATHS — the layout is ADR-072 D1.8's, which is FLAT ("ws-<id>", one
// directory level), not revision 3's nested "ws/<id>". cfg.ProfileDir KEEPS
// its current meaning: it is itself a Chrome user-data-dir (default
// ~/.omnipus/browser/profiles/default/, manager.go:125). Per-key profiles are
// its SIBLINGS:
//
//   profileRoot   = filepath.Dir(cfg.ProfileDir)     // ~/.omnipus/browser/profiles
//   Profile dir:    <profileRoot>/ws-<workspaceID>/          (0700)
//   Launch lock:    <profileRoot>/ws-<workspaceID>/chrome.lock
//   Ownership marker: $OMNIPUS_HOME/browser/ws-<workspaceID>.pid
//
// WHY FLAT, and why this is a correction rather than a preference. The
// managed-Chromium install root is path arithmetic over the PARENT of a
// profile dir: InstallRootForProfileDir(p) = Clean(Join(Dir(Clean(p)), "..",
// "chromium")) (exec_resolver.go:50, verified). Evaluate it on each layout:
//
//   default today   .../profiles/default    -> .../browser/chromium   CORRECT
//   D1.8 flat       .../profiles/ws-<id>    -> .../browser/chromium   CORRECT
//   rev-3 nested    .../profiles/ws/<id>    -> .../profiles/chromium  WRONG
//
// So the nesting was the whole source of revision 3's INVARIANT P-5, and the
// ADR's flat form dissolves it: a per-key path resolves to the SAME install
// root as cfg.ProfileDir. FR-037a still resolves the exec path ONCE, from
// cfg.ProfileDir, but it is now belt-and-braces rather than load-bearing, and
// test 52 asserts the arithmetic on both forms so a future re-nesting fails.
//
// INVARIANT P-1: one live Chrome per key, enforced by per-key single-flight
//                (the existing launched/launching/launchDone triple, per entry).
// INVARIANT P-2: there is NO bound on len(live) expressed as a count. Under
//                D1.5a the only bound is live memory, so the invariant is
//                stated on the gate rather than on the number: NO instance is
//                launched, and NO tab is opened, while the memory gate refuses
//                (FR-057, FR-060). Revision 3 asserted len(live) <= cap and
//                revision 4 asserted len(live) <= target+1; both named a
//                quantity that no longer exists, and a test asserting either
//                would now pass vacuously against a pool with no gate at all.
// INVARIANT P-3: no manager path ever calls chromedp.WithNewBrowserContext or
//                target.CreateBrowserContext. CDP browser contexts are retired
//                entirely (FR-031); ADR-043 CRIT-003 is preserved by deletion.
// INVARIANT P-4: one Chrome's death affects exactly one key (FR-041).
// INVARIANT P-6: the pool never evicts an instance with a live viewer or an
//                in-flight browser tool call (FR-050's two guards). Both are
//                observable through accessors the pool owns: Viewers() and
//                InFlight() (FR-051).
type BrowserPool struct{ /* ... */ }

// Acquire returns the live Chrome for k, launching it if absent.
//
// There is no target (D1.5a). Admission is the live-memory gate and nothing
// else. When the gate refuses, Acquire does NOT refuse immediately (D1.7): it
// selects the least recently used EVICTABLE instance — least recent by last
// tool call or viewer activity, and evictable means Viewers() == 0 AND
// InFlight() == 0 — closes it, and RE-ASKS the gate before launching k, so no
// launch rides a stale reading. The evicted workspace keeps its profile and
// reopens signed in on next use. Nothing surfaces to the agent or the operator
// (FR-050).
//
// When the gate still refuses and nothing is evictable, Acquire waits to the
// caller's deadline and then returns an error naming MEMORY (FR-053), carrying
// the memory_pressure reason code (FR-063). It never launches past a refusing
// gate — not even by one. The D1.7-era "+1 overshoot" is DELETED: a soft cap is
// cheaper to break than a browse is to refuse, but real free memory is not, and
// the OOM killer does not stop at the browser.
//
// If the memory gate refuses AND nothing is evictable, Acquire WAITS for an
// instance to become evictable, up to the caller's own deadline, and only then
// returns a named error identifying the workspace and naming MEMORY as the
// constraint (FR-053). That error is the single place capacity is perceivable
// and it is deliberate. It must NOT name a cap or advise raising one: D1.5a
// deleted every counter, so an operator told to raise a limit would go looking
// for a setting that does not exist.
//
// Before the launch it runs cleanStaleSingletons against k's OWN profile dir
// (FR-042b) — the shipped call passes cfg.ProfileDir only
// (coordinator.go:1235), so without this a crash leaves a stale SingletonLock
// in every per-key profile and Chrome refuses to relaunch, which would make
// FR-043's "the profile survives so the login survives" false in the one case
// it exists for.
//
// Admission is gated on real memory pressure — and under D1.5a that is the
// ONLY admission control there is (FR-057). Where the gate and eviction
// disagree — pressure high AND nothing evictable — the ADR RULES it: the
// pressure gate is a HARD stop and refuses. §0.5 E-2 is answered, not open.
//
// A launch additionally requires PER_BROWSER_COST of headroom (FR-062, ~182 MB
// measured on macOS for an IDLE Chrome for Testing; a capturing instance costs
// more by an unmeasured delta). Launching an instance that cannot then load a
// page is a refusal disguised as a success.
func (p *BrowserPool) Acquire(ctx context.Context, k BrowsingKey) (*chromeInstance, error)

// NO Target(). Revision 4 specified
//
//   target = clamp((min(host_RAM, cgroup_limit)*0.5 - gateway_reserve)
//                  / (FIXED_FLOOR + R*85MB + encoder_page), 1, operator_ceiling)
//
// and ADR-072 D1.5a deletes every term of it: max_browsers and
// operator_ceiling were never built, R and --renderer-process-limit are never
// built, and the 85 MB per-renderer constant is WITHDRAWN on measured evidence
// (30 MB -> 327 MB in one snapshot, an 11x spread, so no constant works). Do
// not reintroduce a derived instance count. FR-056 is tombstoned (§0.6).
//
// What the pool DOES expose is the live figure the gate actually reads, so
// "why was I refused?" is answerable from outside:
//
// Headroom reports the current available-memory reading and the gate's
// verdict, from the same source FR-057 admits on. It is a report, never a
// budget: nothing derives an instance count from it.
func (p *BrowserPool) Headroom() (availableBytes uint64, pressureRatio float64, admits bool)

// Close shuts down k's Chrome and releases its pool entry. Idempotent. The
// process-disposal path for idle close, eviction, workspace deletion, roster
// change and gateway Close(). (FR-040, FR-026a, FR-026c, FR-050.)
//
// It does NOT delete k's profile directory (§5, FR-043) and it does NOT remove
// k's browserMgrs entry or its BrowserManager — see the liveness distinction
// in FR-040a. Deleting the profile is a separate, narrower operation:
// DeleteProfile, called only on workspace deletion (FR-043a).
//
// There is no operator-facing caller. Revision 3's FR-046 REST path and SPA
// control are withdrawn by D1.7 and tombstoned in §9.
func (p *BrowserPool) Close(k BrowsingKey)

// CloseIdle closes every key whose Chrome has zero tabs and zero LIVE viewers
// and has been in that state for longer than idleCloseTTL. Called from the
// existing 1-minute idle sweep (gateway.go:5321-5352) AFTER its per-manager
// ReapIdleSessions loop, so the tabs a sweep reaps are already gone when the
// browser-level decision is made on the same tick (FR-040a). Returns the keys
// it closed, for the sweep's log line.
//
// "LIVE" is FR-052: a viewer whose transport has been silent past the existing
// WebRTC liveness window counts as detached, for BOTH this and eviction.
// Without that, one abandoned panel pins an instance for the process's
// lifetime — and under eviction it also makes that slot permanently
// unreclaimable, which is the difference between a leak and a deadlock.
func (p *BrowserPool) CloseIdle(now time.Time) []BrowsingKey

// DeleteProfile removes k's profile directory from disk. Called ONLY on
// workspace deletion, after Close(k) has returned (FR-043a). Separate from
// Close precisely so that idle close, EVICTION, roster change, reload and
// crash recovery cannot reach it — the logins are the point of the profile,
// and eviction is only acceptable BECAUSE they survive it.
func (p *BrowserPool) DeleteProfile(k BrowsingKey) error

// ReconcileMarkers runs ONCE at boot, before any Acquire, over
// $OMNIPUS_HOME/browser/ws-*.pid. With one marker there was one adoption story;
// with N, a kill -9'd gateway leaves N markers and N orphan Chromes. Under
// D1.5a this matters MORE, not less: the only control left reads the HOST's
// live memory, and an orphan consumes host memory while being invisible to
// every in-process accounting we have. Reconciliation is what keeps the gate's
// reading and the host's reality the same thing (FR-042a).
//
// THE DISCRIMINATOR IS THE PER-KEY LAUNCH LOCK, NOT THE MARKER'S PID (D1.8).
// The marker records the CHROME's pid (readOwnershipMarker,
// coordinator.go:1552-1562) and that pid is alive in BOTH the orphan case and
// the case where a second gateway is running normally on the same
// $OMNIPUS_HOME. On Unix a flock auto-releases when its holder dies, so:
//
//   lock acquirable + pid dead/absent -> stale: clear marker and lock   (INFO)
//   lock acquirable + pid ALIVE       -> orphan: terminate, clear       (WARN)
//   lock HELD       + pid alive       -> another live gateway:
//                                        refuse to launch this key, name it.
//                                        NEVER terminate.               (WARN)
//
// Platform posture is NOT uniform and FR-042a says so rather than implying a
// guarantee: identity before termination is confirmed via /proc/<pid>/exe,
// which is LINUX-only. On macOS the marker is cleared WITHOUT terminating and
// a WARN names the pid; on Windows neither the flock nor pidAlive is real
// (coordinator.go:1569-1575, fileutil/flock_windows.go). See D1.9.
func (p *BrowserPool) ReconcileMarkers() (reclaimed, orphaned, refused int)

// Preprovision resolves — and on a fresh install downloads — the managed
// Chromium binary, ONCE, at boot, with no live key and no launch. It is the
// pool-level replacement for gateway.go:2286's range over BrowserManagers(),
// which under a lazily-created pool iterates an EMPTY slice at boot and
// silently moves a multi-hundred-megabyte download to the first tool call
// (FR-016c). Safe to call with zero live keys; that is the normal case.
// BrowserManager.Preprovision's whole body is a config check plus
// resolveExecPath (manager.go:3218-3223), so nothing here needs a key.
func (p *BrowserPool) Preprovision(ctx context.Context) (string, error)

// LiveKeys / PID are the test and audit accessors. PID is per key now; the
// coordinator's PID() int (coordinator.go:849) does not survive.
func (p *BrowserPool) LiveKeys() []BrowsingKey
func (p *BrowserPool) PID(k BrowsingKey) (int, bool)
```

```go
// pkg/tools/browser/manager.go — the two eviction guards need observable state.

// Viewers reports attached viewers whose transport is LIVE (FR-010, FR-052).
// A viewer silent past the WebRTC liveness window is not counted.
func (m *BrowserManager) Viewers() int

// InFlight reports browser tool calls currently executing against this
// manager (FR-051). It is incremented by EVERY browser_* tool's Execute —
// leased and lease-exempt alike — and released by defer.
//
// The write lease cannot stand in for this, and the reason is arithmetic:
// §14's exempt set is SIX tools, so a browser_screenshot, browser_get_text,
// browser_wait, browser_list_tabs, browser_snapshot or browser_handle_dialog
// executing against a Chrome holds NO lease. Under revision 3 that did not
// matter, because nothing evicted. Under D1.7 it means the pool would close
// Chrome out from under a running read. FR-051 is therefore a separate
// counter, not a lease query.
func (m *BrowserManager) InFlight() int
```

**Locking discipline (load-bearing).** The pool's bookkeeping mutex is never held across a Chrome launch, a CDP call, or a `Close`; per-key single-flight uses the existing `launching`/`launchDone` condition-variable pattern rather than holding the pool lock. Lock order is `writeLease → pool.mu → m.mu`, never the reverse, and `m.mu` is never held across `acquireWrite` (§14) or any CDP call — the ADR-038 discipline, unchanged.

**And one ordering rule eviction adds, which is a race and not a style point (FR-051).** Eviction *selects* a victim by reading `Viewers()` and `InFlight()` across candidates, then closes it. A tool call that starts on the selected instance *between* the read and the close is evicted mid-flight — the exact failure FR-050's second guard exists to prevent, arrived at by interleaving rather than by a missing check. **The rule:** `InFlight()` is incremented under the same `pool.mu` that eviction selection holds, so a call that begins during selection either is seen by it (and its instance is not selected) or begins after the victim is already chosen and closed (and therefore addresses a relaunched instance, which is correct). A `-race` test drives a long lease-**exempt** read on the LRU instance against a concurrent `Acquire` of a new key and asserts the read completes (test 68) — exempt deliberately, because the leased case is the one the lease would have covered anyway.

### 3.2 Streams

**Stream A — Key + tab ownership + resolution + per-Execute manager binding [CRITICAL PATH, not gated].**
Owns `key.go`, `resolve.go` (new); `TabOwner` and `sessionKey` (**FR-048**); **the counter deletion (FR-059)** — `tools.browser.max_tabs`, `tools.browser.max_total_tabs`, `TryOpenTab`/`ReleaseTab`/`reservedTabs`/`totalOpenTabsLocked`/`SetMaxTotalTabs`, `reserveGlobalTab`/`releaseGlobalTab`, their call sites and their 59 test-side references (§0.6); `ManagerResolver` and the `RegisterTools` signature change plus all 11 tool structs (FR-002a); the `browserMgrs` re-key and `BrowserManagerForKey`/`BrowserManagerForAgent` (`loop.go`); **`controlledResult`'s re-key (FR-002c — it is on the resolution path, not the lease path)**; all 37 `DefaultSessionID` consumers (FR-002b) and the constant's deletion; the reload-prune predicate and per-key idempotent registration (FR-026a, FR-026b); the `loop.go:270-279` comment's replacement (FR-002d).
**FR-048 is not separable from FR-001 and must land in the same commits.** A commit that re-keys `browserMgrs` without carrying the agent dimension on the tab set ships a state D1.9a forbids — every agent on a workspace sharing one tab set — and it ships it *silently*, because every map-level test still passes (§0.2a).
**FR-059 is not separable from FR-001 either, and for the mirror-image reason.** A commit that re-keys `browserMgrs` while `cfg.MaxTabs` is still enforced ships a 5-tab cap for a whole team; a commit that deletes the counters while the managers are still per-agent removes the only bound before the memory gate exists. **FR-059 lands with FR-001, and FR-060's gate lands in the same commit as FR-059's deletion** — the five `totalTabCountLocked` call sites are vacated and re-occupied in one change, never left empty across a commit boundary.
Depends on: nothing. Interface out: §3.1.

**Stream P — Browser-process pool [GATED on G-1 + G-2 + G-6; largest].**
Owns `pool.go`; the coordinator's decomposition into per-key instances; per-key profile dirs (**flat `ws-<id>`**), launch locks and ownership markers (FR-037, FR-037a, FR-042); the admission edge semantics (FR-038, FR-038a) and the **LRU eviction policy with its two guards** (FR-050, FR-051, FR-052); the **memory refusal** and its one named error (FR-053); thrash detection (FR-054); the **memory-pressure admission gate — the only admission control there is** — at launch *and* at tab open, its two Chromium unknowns, its no-silent-no-op requirement and the measured launch minimum (FR-057, FR-057a, **FR-060**, **FR-061**, **FR-062**); *(no `max_browsers`, no operator ceiling, no `--renderer-process-limit`, no per-renderer constant — FR-055 and FR-056 are tombstoned by D1.5a, §0.6)*; whole-Chrome idle close with its config key, caller and post-close state (FR-040, FR-040a); boot marker reconciliation via the **launch lock** (FR-042a) and per-key stale-singleton cleanup (FR-042b); per-Chrome crash containment, replacing `watchForCrash`'s reset-everything behaviour (FR-041); profile-based reload survival replacing ADR-043 CRIT-002's context re-adoption (FR-043), the profile's deletion path (FR-043a) and the **upgrade rule** — no workspace inherits the existing global profile (FR-043b); boot preprovision decoupled from `BrowserManagers()` (FR-016c); retirement of `capture_shared_context`, `disposeBrowserContextRaw`, `contextCount()` and the CDP-context branch of `Register` (FR-031).
**Also owns FR-034a — the final description literals** (§3.3), which must land in the same commit as FR-037 and not before.
Depends on: A's key type, **A's `TabOwner`** — a pool whose instances hold merged tab sets cannot be un-merged later without touching every call site again — **and A's FR-059/FR-060 pair**, because between the counters' deletion and the gate's arrival there is no bound at all. **Do not start before G-2 passes.**

**Stream C — Two-state tabs + descriptions (D1.12) [depends on A].**
Owns `ListTabsState` + `ListTabs` delegation (`manager.go:1605-1613`); `ListTabsTool.Execute` (`tabs.go:48-68`); the five model-visible strings, their **interim** replacement literals (FR-015, FR-034 — §3.3), and the two Go comments (`tabs.go:19,186`). Its payload must also say **whose** tabs it is reporting (FR-048): the agent's own set and the workspace's operator-owned set are distinguishable, because "whose tab is this" is the question ADR §1.1 records being answered wrong.
Does **not** own a "not permitted" state — **the ADR withdrew it** (D1.12) and §17 C3 tombstones the whole stack that served it. `browser_list_tabs` has **two** states.
**Does NOT own the final literals.** FR-034a's isolation-asserting text lands in Stream P's commit, not this one (§3.3, MAJ-107).

**Stream D — Write lease, rescoped [depends on A].** Owns `lease.go` per **§14**, the call pairs in every mutating tool, and composition with `controlledResult`. **§14 is normative; this stream implements it and the D2 spec references it.** Under D1.9a the lease only ever arbitrates **agent-vs-agent on the operator's workspace-owned tab set** — two agents on their own tabs cannot contend, so the primitive is unchanged but the contended path is now narrow enough to be exercised deterministically.

**Stream E — Gateway resolution + contracts [depends on A].**
Owns the three `BrowserManagerForAgent` call sites; server-side agent→workspace resolution preferring the attaching session's `workspace_id` (`pkg/session/unified_meta_files.go:60`); the capture registry's re-keying and the ADR-048 conflict rule's collapse (FR-016a); the boot warm path (FR-016b); the panel's failure messages (FR-008a); the three schema description edits, **one of which is a semantic reversal and must be reviewed as one** (FR-016, MAJ-004 in §15); and the Workspace → Team elevation-of-privilege disclosure (FR-047).
**FR-047 does not depend on Stream P** and should land in §0.4: it is true the moment Stream A lands, because adding an agent to a workspace already grants it that workspace's browser.
**Stream E no longer owns any `contracts/openapi.yaml` path.** Revision 3's `POST /api/v1/workspaces/{id}/browser/close` and its SPA control went with FR-046 (D1.7); the only `contracts/` diff D1 produces is `description:` text in three existing schemas, one of which is a semantic reversal.

**Stream F — Audit + lifecycle [depends on A, P].**
Owns the **per-action** write-class audit events (FR-027), their name constraint (FR-058) and their provenance assertion (FR-035); disposal on workspace deletion and roster change (FR-026); the reaper interactions (FR-025) and the pool's idle-close hook (FR-040); `#659`'s auto-deny requirement for delegated sub-turns (FR-032).
**FR-027's audit does not wait for Stream P** — the events are emitted from the tool path, not the pool, and per-action repudiation is exactly what D1.10's sharing ruling makes urgent.

**Stream G — Tests + regression (cross-cutting).** Owns §10. **Does not own the 364-reference test migration** — that is Stream A's, in Stream A's own commits, because it is a compile dependency of FR-002b rather than a test-quality task (§2.2a).

**Parallelization:** A lands its interface first, with the §2.2a migration in the same commits. C/D/E/F fan out on disjoint files. P runs behind its gates and lands last.

**Why "last" is safe for the mechanism but not for the words (round-2 MAJ-107).** Until P lands the product has one Chrome and therefore one cookie partition — which is exactly today's behaviour, so the intermediate state is not a *behavioural* regression. The previous draft stopped there and used that to justify shipping the description corrections in Stream C. **That was wrong**, because FR-034's replacement literal asserted the browser is *"shared across this workspace"* — a sentence whose implicature is that it is **not** shared beyond it, while `capture_shared_context: true` (`defaults.go:671`) still means one partition across **all** workspaces until P lands. That is a false ownership claim made to the model and shown to the operator: the exact defect class ADR-072 §1.1 exists to fix, reintroduced by the fix for it.

The split in §3.3 removes the problem rather than accepting it: Stream C's literals assert only tab-set sharing (true after Stream A), Stream P's assert isolation (true only after FR-037), and §5 records the general rule so a future edit cannot re-merge them.

**And if G-2 fails after §0.4 has landed** (round-2's unasked question 10): the rollback is now nothing, because nothing shipped in §0.4 asserts isolation. Stream C's interim literals stay true whether or not the pool is ever built. That is the reason the split is worth its cost.

### 3.3 The description literals (FR-015, FR-034, FR-034a) — specified, in two stages

The previous draft's FR-034 said the replacement literals were "**specified here**" and then specified none of them anywhere in the document. They are below. Test 9 asserts the exact strings.

**The five model-visible occurrences of "the shared browser session"** — `tabs.go:32` (`browser_list_tabs`), `:86` (`browser_switch_tab`), `:143` (`browser_close_tab`), `:206` (`browser_open_tab`), and `tools.go:415` (a parameter description). Plus two Go comments (`tabs.go:19,186`), which are not model-visible and change with the code.

| Stage | Lands with | Replacement for "the shared browser session" | What it claims, and why that is true at that point |
|---|---|---|---|
| **Interim (FR-034)** | Stream C, §0.4 | **"the browser this workspace's agents share"** | Claims only that every agent on the workspace addresses one tab set — which is exactly what Stream A's `ManagerResolver` makes true, and is the reported defect's fix. Says nothing about other workspaces, so it cannot be false while one Chrome is still shared process-wide. |
| **Final (FR-034a)** | Stream P, same commit as FR-037 | **"this workspace's browser"**, plus one added sentence in `browser_list_tabs`' and `browser_open_tab`' descriptions: **"Each workspace has its own browser, with its own logins; you cannot see or use another workspace's."** | Claims isolation. True only once FR-037 gives each workspace its own Chrome process and profile directory. |

The parameter description at `tools.go:415` takes the interim form at both stages — a parameter doc is the wrong place for a scoping claim, and it is the one occurrence where "the browser this workspace's agents share" reads correctly in both worlds.

**Test 9 runs twice with different expectations,** and this is deliberate rather than a table-driven convenience: `TestToolDescriptions_NoFalseSharedClaim` asserts (a) the phrase "the shared browser session" appears zero times in `pkg/tools/browser` after Stream C, and (b) the stage-appropriate literal is present verbatim. Its stage-P form additionally asserts the isolation sentence is **absent** before FR-037's commit — an assertion that can only be written as a build-tag-free check of the two literals against the presence of `pool.go`, so it is stated plainly in §10 as an ordering requirement the reviewer checks rather than a test that can enforce its own ordering.

---

## 4. Behavioral contract (observable)

1. **Handover.** The operator browses in a workspace chat with Mia and switches the chat to Jim; Jim's `browser_list_tabs` returns the same tabs, with no handover command and no re-navigation.
2. **Human-first.** The operator opens the live panel and browses before addressing any agent; any browser-policy-allowed agent on that workspace then sees the tab.
3. **Cross-workspace isolation.** A site login established in workspace X is absent when the same site is opened in workspace Y, because X and Y are different Chrome processes with different profile directories.
4. **No surprise logout.** A login established in one chat is still present in a new chat in the same workspace.
5. **Delegated work shares the browser.** A delegated sub-turn — attended or not — uses its workspace's browser and its logins. (D1.10. This **inverts** the pre-ruling draft's contract item 5.)
6. **Workspace-less turn.** A scheduled or heartbeat turn whose `ToolWorkspaceID(ctx)` is empty but whose work dir was re-rooted into a CoreTeam workspace reaches **that same workspace's** browser.
7. **Genuine no-workspace.** An agent on no workspace at all gets `ErrNoBrowsingContext`'s named text from every browser tool — never a shared browser, never an empty success.
8. **Ambiguous workspace.** An agent on two or more workspaces, on a turn carrying no workspace id, is **refused** with the ambiguity named — the sorted-first tie-break is not applied to a browser (FR-033).
9. **Two states.** `browser_list_tabs` distinguishes "no browser here yet" from "a browser with these tabs" (and reports an empty set as such) without inference. **Two, not three:** ADR D1.12 withdrew the "not permitted" state as unreachable, and §17 C3 tombstones the stack that served it.
9a. **Whose tabs.** The answer names ownership: the calling agent's own tabs, and — separately labelled — the tabs the **operator** opened on this workspace, which every agent on it can see. An agent never sees another agent's tabs (D1.9a, FR-048).
10. ~~**Not permitted.**~~ **WITHDRAWN by ADR D1.12.** `FilterToolsByPolicy` (`pkg/tools/compositor.go:436-438`) `continue`s past a deny verdict, so a policy-denied agent is never shown `browser_list_tabs`, never calls it, and answers from absence. There is no artefact for a denial that never runs. The underlying defect — an agent that cannot tell "I may not" from "there is nothing" — is **real, unfixed, and in ADR §6** as the headline problem surviving in a narrower form; it is not solvable inside a tool. §17 C3.
11. **One writer, on the one tab that can contend.** Two agents acting on the **operator's shared tab** concurrently: neither observes the other's mid-action state; the loser retries within the tool and, only past the bound, receives a **non-error** `{"deferred": true, "reason": …}` naming the holder. Two agents acting on **their own** tabs never interact at all — that case cannot arise under D1.9a, and §14 says so rather than leaving a lease to arbitrate a contention that does not exist.
12. **Human outranks agent.** While a human holds the live-view control lock, an agent action tool defers with the ADR-038 D6 reason, not the lease reason.
13. **No wedge.** An action tool that panics, times out or is cancelled while holding the lease does not prevent the next one from acquiring it.
14. **Live panel.** Every gateway surface resolves the manager that owns the browser that agent's turns use for the attaching chat session — and when it cannot, says *which* reason (FR-008a).
15. **Reload.** A Settings save mid-browse leaves each workspace's Chrome pid unchanged and its login intact.
16. **Audit, per action.** Browser creation is recorded once; **every write-class browser tool call** is recorded with workspace id, agent id, tool name and target host. Read-only tools are not audited per call. First-use-only auditing is **rejected by name** in D2.11 — it fires once per agent per workspace and says nothing about the tenth action, or about which agent made the purchase (FR-027). Every event name matches `^[a-z_]+$`; a dotted name blanks the entire Audit Log viewer (FR-058, issue #667).
17. **Memory is the only limit, and it manages itself.** There is no cap of any kind — no `max_tabs`, no `max_total_tabs`, no `max_browsers` (D1.5a, §0.6). When memory pressure says stop and something is reclaimable, a request for a **new** workspace's browser **evicts the least recently used evictable instance and launches**. Nothing surfaces to the agent or the operator. The evicted workspace reopens on next use **still signed in**, from its profile on disk, paying start-up latency only. An instance with a live viewer, or with a browser tool call in flight, is never the victim (FR-050, FR-051, FR-052).
17a. **The one place capacity is visible, and it names memory.** If memory pressure is above the gate's threshold **and** nothing is evictable, the request **waits** for an instance to become evictable, up to the tool call's own deadline, and only then fails with a named error identifying the workspace and naming **memory** as the constraint (FR-053). It must not name a cap or advise raising one — there is none to raise, and an operator sent looking for a setting that does not exist is worse off than one told the truth. *(The pressure gate is a **hard** stop: ADR D1.5a, answering §0.5 E-2.)*
17b. **Thrash is reported, not experienced.** If more workspaces browse concurrently than live memory allows, each one pays a cold start continuously with nothing on screen to explain it. The pool detects the evict-then-reopen cycle and logs **one** WARN naming the contending workspaces, the memory figure that forced the eviction, and what the operator can actually do — **run fewer workspaces concurrently, or give the host more memory.** There is no configuration remedy (FR-054).
17c. **A tab loop is stopped by the same gate.** Opening a tab inside an already-running browser goes through the **same** memory check as launching one. There is no counter left to catch a runaway `browser_open_tab` loop, and it never reaches a launch decision, so the gate is at the tab-open sites too (FR-060).
18. **Crash containment.** One workspace's Chrome dying leaves every other workspace's browsing unaffected — its tabs, its panel and its logins survive.
19. **Idle close, at both levels, and it is load-bearing.** An idle **tab** is reaped after `tools.browser.idle_ttl` (default 5 minutes, `manager.go:130-134`). A workspace **browser** with no tabs and no live viewers for longer than `tools.browser.idle_close_ttl` (default 15 minutes) is closed entirely, releasing its process; its profile — and therefore its logins — remains on disk, and the next tool call for that workspace relaunches Chrome from it **still logged in**. **Under D1.5a these two are not housekeeping.** With every counter deleted, idle close and the memory gate are the entire defence, so a reaper that silently stops running is not a leak to clean up later — it is the removal of one of two controls (FR-061).
20. **Unattended work cannot hang.** An `ask`-policy tool reached from a delegated sub-turn is **denied**, not queued against an operator who is not there (FR-032, #659).
21. ~~**A full pool has a way out.**~~ **WITHDRAWN with FR-046 (D1.7), and now doubly so (D1.5a).** There is no refusal to escape from on the normal path: the pool evicts. Revision 4 left "raise `tools.browser.max_browsers`" as the residual advice; **that key is never built** (FR-056 tombstoned), so the only honest remedies are fewer concurrent workspaces or more host memory, and FR-054's thrash WARN is the one place the product says so.
22. **A crashed gateway leaves nothing behind.** After a `kill -9`, the next boot leaves zero orphan Chromes and zero stale ownership markers under `$OMNIPUS_HOME`, and every workspace's next browser call relaunches cleanly from its own profile — no stale `SingletonLock` refusal.
23. **Deleting a workspace deletes its logins.** Deleting a workspace closes its Chrome **and removes its profile directory**. The client's cookies and tokens do not outlive the workspace. Idle close, roster change, reload and crash recovery never delete a profile.
24. **Adding an agent to a team says what it grants.** The Workspace → Team UI states, at the point of adding, that the agent gains every live browser session on that workspace — including on turns nobody is watching.

---

## 5. Explicit non-behaviors

- The system must **not** fall back to `DefaultSessionID`, `""`, the agent id, or any other constant when workspace resolution fails. There is no default browser.
- The system must **not** apply `FindForAgent`'s sorted-first tie-break to a *browsing* key. It selects live credentials; for a filesystem mount the worst case is the wrong directory, here it is acting as the wrong signed-in identity (FR-033).
- The system must **not** give a delegated sub-turn a separate browser, a separate key, or a signed-out jar. That was reversed by ruling; reintroducing it is a design change requiring a new ADR entry, not an optimisation.
- The system must **not** address a tab set by `BrowsingKey` alone. Every tab operation names a `TabOwner` (FR-048). A call site that resolves a browser and then reads "the" tab set has merged the team's tabs, and it does so without failing any map-level test — which is why this is a non-behaviour rather than a note (§0.2a).
- The system must **not** let one agent see, list, switch to, drive or close another **agent's** tab. Only the workspace-owned (operator-opened) set crosses agents (D1.9a).
- The system must **not** keep, reintroduce, or replace any tab or browser **counter**. `tools.browser.max_tabs`, `tools.browser.max_total_tabs` and the `TryOpenTab`/`ReleaseTab`/`reservedTabs` machinery are **deleted from the code**, and `max_browsers`/`operator_ceiling` are never built (D1.5a, FR-059). A refusal derived from a count — of tabs, of browsers, of renderers — is forbidden anywhere on the admission path; the only refusal is memory's.
- The system must **not** leave the five vacated `cfg.MaxTabs` enforcement sites empty. `createFirstTab` (`manager.go:1139`), `OpenTab` (`:2005`, `:2047`) and `adoptTarget` (`:2216`, `:2286`) are exactly where a runaway tab loop lives, and FR-060's memory gate takes the counter's place there **in the same commit** (FR-059).
- The system must **not** ship the memory gate or the idle reapers disabled, "best effort", or behind an off-by-default flag on any platform. They are the entire defence (FR-061); a gap in either is not a degraded limit, it is no limit. Where a platform provides no pressure signal, that must be **stated** the way D1.9 states the orphan-termination asymmetry — never papered over with a conservative constant, because there is no constant left to be conservative with.
- The system must **not** construct a `BrowsingKey` anywhere except `ResolveBrowsingKey`, and must **not** add a second key shape.
- The system must **not** let any tool struct hold a `*BrowserManager` captured at registration (FR-002a).
- The system must **not** leave any consumer of `DefaultSessionID` behind; the constant is **deleted, not deprecated** (FR-002b).
- The system must **not** call `chromedp.WithNewBrowserContext` or `target.CreateBrowserContext` on any path. CDP browser contexts are retired outright (FR-031); ADR-043 CRIT-003 is preserved by deletion rather than by discipline.
- **INVERTED BY RULING (D1.7).** Revision 3 read: *"the system must not evict a live workspace browser to satisfy a new request; refuse at the cap."* The requirement is now its opposite: **the system must not refuse a browse because the target is reached.** It evicts the least recently used evictable instance and launches (FR-050). It must **not** evict an instance with a live viewer or an in-flight tool call, must **not** exceed the target by more than one in total, and must **not** delete a profile on eviction — the last is what makes the first acceptable at all.
- The system must **not** ship a "pool full" error surface, REST path, or UI control on the normal path (D1.7). The only capacity error is FR-053's ceiling, reached only when every instance is simultaneously watched and busy.
- The system must **not** destroy a browser on hot reload. Only workspace deletion, roster change, idle close, **eviction**, or gateway `Close()` (FR-026a, FR-040, FR-050).
- The system must **not** delete a workspace's **profile directory** when its Chrome is closed for idleness, for a roster change, for a reload, for a crash, or **by eviction** — the logins are the point of the profile, and eviction is only tolerable because they survive it (FR-040, FR-043, FR-050). **Workspace deletion is the sole exception and it MUST delete** (FR-043a): a departed client's cookies and tokens must not outlive the workspace that named them.
- The system must **not** interpolate a workspace id into a filesystem path without validating it as a single path segment. It happens to be safe today — ids are server-minted ULIDs (`rest_workspaces.go:495` for the default workspace, `:848` for created ones) — but the path depends on a property nothing records, so a future id-format change (an operator-chosen slug, an import, a migration) would silently turn `<profileRoot>/ws-<id>/` into a path-traversal surface — and the flat form makes this **more** important, not less, since `ws-` is a bare prefix rather than a directory boundary. The invariant is written down and enforced: `filepath.Base(id) == id` and `id != "." && id != ".."`, checked in `ResolveBrowsingKey` before the key is constructed, with the refusal treated as `ErrNoBrowsingContext` (FR-037, round-2 MIN-106).
- The system must **not** ship a model-visible description that asserts cross-workspace isolation before FR-037 lands. A description is a claim to the model and to the operator, and shipping the claim ahead of the behaviour is the defect ADR-072 §1.1 exists to fix, not a harmless ordering (§3.3, round-2 MAJ-107). The general rule: **no description may assert a property the current commit does not implement**, even when the wrong intermediate behaviour is identical to today's.
- The system must **not** compute the managed-Chromium install root from a per-key profile directory. `InstallRootForProfileDir` (`exec_resolver.go:50`) is path arithmetic over the *parent* of what it is given, so a per-key path yields a different, wrong install root per workspace and N downloads of the same binary (FR-037a).
- The system must **not** let the browser cap bound only this process's Chromes. Orphans from a crashed gateway consume the same host memory the cap exists to bound, so they are reconciled at boot rather than ignored (FR-042a).
- The system must **not** return `nil, 0, nil` from `ListTabs` for a missing browser once `ListTabsState` exists.
- The system must **not** add, remove or retype any field in an **existing** `contracts/` schema for D1, **and must not add a path**. Descriptions change and nothing else, and **one of those description changes is a semantic reversal** that must be reviewed as a behavioural contract change, not as prose (FR-016, §15 MAJ-004, ADR D1.13). *(Revision 3 carved out one added path for FR-046. FR-046 is withdrawn, so the carve-out goes with it and SC-007's condition (2) reverts to its unamended form: no `contracts/` diff outside `description:`.)*
- The system must **not** widen the seeded browser tool policy. Mia and Ava stay denied.
- The system must **not** hold `m.mu` or `pool.mu` across `acquireWrite`, a Chrome launch, or any CDP call.
- The system must **not** change the `browser-webrtc[<agent>]` log label to a workspace label — cosmetic, and the agent is still the useful identity in a log line (round-1 review O3).
- The system must **not** re-key the `serve_web` preview URL. Out of scope, ADR §6 open.
- The system must **not** ship `tools.browser.max_browsers` at all — not as a value, not as a ceiling. FR-056 is tombstoned by D1.5a; there is no derived instance target and no clamp on one (§0.6).
- The system must **not** pass `--renderer-process-limit` on any launch. Chrome's default site-per-process isolation is retained in full, which is why the round-2/round-3 finding about it is **dissolved rather than mitigated** (§0.6). Reintroducing the flag as a memory knob would put the operator's signed-in bank tab and a page an agent found into the same renderer, against a `ValidateURL` (`manager.go:685-708`) that permits any public URL.
- The system must **not** derive any capacity quantity from a per-renderer or per-tab byte constant. The 85 MB figure is withdrawn on measured evidence — 30 MB to 327 MB in one snapshot — and a tab is not a renderer (2 tabs against 13 renderer processes in the same snapshot). Capacity is read live (FR-057, FR-062).
- The system must **not** count a viewer as attached once its transport has been silent past the WebRTC liveness window. Under eviction that is not a leak but a deadlock: one abandoned panel makes a slot permanently unreclaimable (FR-052).
- The system must **not** emit an audit event whose name fails `^[a-z_]+$`. The Audit Log viewer's contract enforces the pattern (`contracts/components/schemas/AuditEntry.yaml:17`) and a dotted name blanks the **whole** viewer, not just its own row (FR-058, #667).

---

## 6. Integration boundaries

- **Chrome processes / CDP.** The count of live Chromes now scales with **workspaces being actively browsed**, bounded by **live memory and nothing else** (FR-057; D1.5a deleted every counter). Each is launched over the pipe transport (`exec_resolver.go`, `cdppipe`) with its own `--user-data-dir`. A launch failure surfaces as a tool error naming the workspace — never a silent join to another workspace's browser. **CDP browser contexts are no longer created at all** (FR-031).
- **Sandbox (Landlock/seccomp).** No new network surface: CDP flows over inherited fds 3/4, and the fixed DevTools port allow-rule was already removed (`pkg/gateway/sandbox_apply.go:412-417`). What *is* new is **N profile directories**, so the filesystem allow-list must cover `<profileRoot>/` as a subtree — the per-key dirs are `ws-<id>` siblings of `cfg.ProfileDir` under it, not a `ws/` sub-tree (`profileRoot = filepath.Dir(cfg.ProfileDir)`; §3.1). Verify against `sandbox_apply.go`'s path rules before the pool lands — this is the one sandbox interaction that is genuinely new.
- **Host memory, and why there is no planning unit at all any more.** The binding cost, and now the *only* control. **`PER_BROWSER_COST` is measured at ≈182 MB** (Chrome for Testing, macOS, idle, non-capturing — carry the scope, §0.6) and is the launch-headroom minimum (FR-062). Everything else is read live: page type varies more than 20× (an idle article ≈15 MB PSS, a mail client 120–180 MB, video at 1080p 222–341 MB), the measured renderer spread was **30 MB → 327 MB in one snapshot**, and a tab is not a renderer (2 tabs, 13 renderer processes). **So no per-renderer or per-tab constant is used, and no count is derived from memory** — FR-057 gates on the live figure and FR-060 puts the same gate in the tab-open path. ADR-043's "≈4–5 GB at ten" is unmeasured and per-agent; neither it nor the retracted 1118 MB RSS reading (ADR §8) may be quoted as a figure.
- **Chrome computes its own renderer limit, and that limit does not compose.** Chromium's `render_process_host_impl.cc` budgets `85 MB` per renderer against half of physical RAM, clamped to at least 3. On the measured 3916 MB box that is `3916 / 2 / 85 = 23` renderers **per Chrome** — four workspaces would each independently permit 23, i.e. ~92 renderers, every one sanctioned by Chrome. **The pool must therefore impose its own bound, and under D1.5a that bound is the memory gate — not a counter of our own** (FR-057, FR-060). This is the single largest consequence of going from one Chrome to N and nothing in ADR-043 anticipates it. Whether Chromium even reads a cgroup limit when computing that budget is **unverified in either direction** — gate G-3, and a negative answer makes our gate the only real one, which is exactly what D1.5a already assumes.
- **`pkg/config`'s memory readers are the reuse, and they are unexported.** `autoDetectMaxParallel` already sizes a concurrency cap as `availableRAMBytes() / bytesPerAgent`, clamped, and `availableRAMBytes` (`config.go:656-661`) already takes `min(/proc/meminfo MemAvailable, cgroup limit − usage)` over `readMemTotalBytes` / `readCgroupV2LimitBytes` / `readCgroupV1LimitBytes` (`pkg/config/meminfo_linux.go`, with a `meminfo_other.go` non-Linux stub and existing fixture tests). **All of it is unexported, so `pkg/tools/browser` cannot call it as written.** **FR-057 states which way that is resolved** — `pkg/config` exports a live memory-availability accessor — rather than assuming a helper is reachable, because a spec that assumes an unexported symbol is callable is a plan that does not compile. *(Revision 4 hung this on FR-056's formula; the formula is gone, the export requirement is not: the gate reads the same numbers.)*
- **Workspace store** (`pkg/workspace/find_for_agent.go`): read-only. `FindForAgent` tie-breaks by sorted-first id (`:45-48`); `FindForAgentPreferring`'s fast path suppresses the ambiguity WARN (`:168-176`). FR-033 declines that tie-break for browsing keys and requires the WARN on **both** paths whenever it would have arbitrated.
- **Fresh install.** A fresh install is **not** workspace-less: `ensureDefaultWorkspace` (`rest_workspaces.go:468`, called at `gateway.go:5013` on every boot) creates "My Workspace" with `defaultWorkspaceTeam(cfg)` = `coreagent.All() ∩ configured agents` (`rest_workspace_delegation.go:359-379`), which includes Jim and Ray — the two browser-policy-allowed agents. So the default path resolves. **The residual case is a custom agent**: the system deliberately never auto-adds a custom/pre-existing agent to any workspace team (ADR-046 FR-008, stated at `gateway.go:5018-5025`), so a custom browser-allowed agent resolves to nothing and must be told why (FR-008a, US-14). **That condition already has a shipped boot-time surface and US-14 reuses it rather than inventing a second vocabulary** (round-2 MIN-107): `logWorkspacelessAgents(homePath, cfg)` (`gateway.go:5026`, immediately after `ensureDefaultWorkspace` at `:5013`) exists precisely to list, once at boot, the agents that *"silently cannot execute at all until manually added via a workspace's Team tab"* (`gateway.go:5015-5025`). FR-008a's panel reason and `ErrNoBrowsingContext`'s text must name the **same** remedy in the **same** words as that log line, so an operator who sees both does not think they are two problems.
- **Host memory and orphan Chromes.** The shipped ownership marker is consulted at **launch** time, not at boot: `acquireLaunchLockWithMarker` (`coordinator.go:1448-1482`) reads the marker and, if its pid is alive and owned by omnipus, **refuses to launch** with a named error rather than adopting or killing; if the pid is dead it clears the stale lock and retries. That is a reasonable single-Chrome story and it is not a boot-time story. With N markers it leaves N orphan Chromes consuming host memory that `LiveKeys()` cannot see, so the cap would bound this *process's* Chromes and not the *host's* — which is the only thing the cap is for. FR-042a adds the boot pass; §12 A20 records the kill-vs-warn trade-off it makes.
- **Session store** (`pkg/session/unified_meta_files.go:60`): the gateway reads `workspace_id` off the attaching chat session's meta. A session without one degrades to `FindForAgentPreferring(home, agentID, "")` — same ladder, same FR-033 refusal on ambiguity.
- **Scheduled and heartbeat turns already carry a workspace, and the previous draft did not know it** (round-2 MAJ-113). Rung 2 and FR-033's refusal were designed against the premise that these turns arrive with an empty `ToolWorkspaceID`. **The shipped code contradicts that as the normal case for the turns that matter most.** `pickSession` (`pkg/gateway/schedules.go:490`, called on every fire at `:141`) resolves the job's workspace via `resolveScheduleWorkspaceID` (`:581-639`) and stamps it onto the session meta *before* the run; `ProcessScheduled` then reads it back (`loop.go:6934-6946`) into `processOptions.WorkspaceID` (`:6957`). **Heartbeats are workspace-scoped by construction:** the reconciler names each job `heartbeat:<workspaceID>:<agentID>` (`heartbeat_schedule.go:30-33`) and that `(workspace, agent)` pairing never changes for the job's lifetime; `workspaceIDFromHeartbeatJobName` (`schedules.go:654`) parses it back. **So a heartbeat turn resolves at rung 1 and never reaches FR-033.** The round-2 review's stated consequence — *"the first time an operator adds Ray to a second workspace, every Ray heartbeat permanently loses the browser"* — **is false**: enabling a heartbeat on a second workspace creates a *distinct* job with a distinct name and its own workspace, so each of Ray's heartbeats resolves to its own workspace deterministically. See §16 MAJ-113. **What is left, and it is real:** a *plain, operator-created* schedule resolves to `""` — `resolveScheduleWorkspaceID` returns only the heartbeat-name parse, because ADR-065 FR-8 removed the channel plumbing that used to be its second source (`schedules.go:632-639`). So a plain schedule owned by an agent on two or more workspaces **does** hit rung 2 and **is** refused by FR-033. That case is narrow, it is the case where "which client's logins?" genuinely has no answer, and refusing it is the ruling FR-033 already makes. §12 A19 records the alternative (a per-agent browsing-home workspace) as considered and declined, with the reason.
- **Lease wait vs the action-tool timeout** (`manager.go:35`, `:123`; `config.go:3632`): §14.1 required `leaseWaitTimeout` to be strictly less than "the shortest action-tool timeout" and never named it. It is `BrowserConfig.PageTimeout`, default **30s**, operator-settable as **`tools.browser.page_timeout`** (JSON `page_timeout`, field `PageTimeoutSec`, env `OMNIPUS_TOOLS_BROWSER_PAGE_TIMEOUT`, applied at `loop.go:2311-2312`). *(The round-2 review called this key `page_timeout_sec`; that is the Go field's suffix, not the config key — §16 MIN-109.)* Both values are operator-configurable and **nothing validates the relationship**, so an operator can set `lease_wait` above `page_timeout` and turn every contended call into a CDP timeout **error** where FR-020 requires a non-error deferral. FR-023a adds the validation.
- **Policy engine** (`pkg/agent/tool_denial.go:206-210`): **no longer an integration boundary for D1, and the reason is a verified code fact rather than a scope decision.** Revision 3 required a browser-specific denial message here so ADR criterion 3b would have a testable artefact. `FilterToolsByPolicy` (`pkg/tools/compositor.go:429-444`) removes every deny-verdict tool from the definitions sent to the model — `if verdict == config.ToolPolicyDeny { …; continue }` at `:436-438` — so a policy-denied agent is **never shown** `browser_list_tabs` and never calls it. `tool_denial.go`'s message has no production caller for this case, and a test asserting its string would assert something nothing emits. ADR D1.12 withdraws the state and its criterion; §17 C3 tombstones FR-014a. **The problem is real and unsolved:** telling an agent that a browser exists which it may not drive needs a system-prompt or manifest surface, not a tool result — ADR §6 owns it.
- **Delegation and `ask` policies (#659).** D1 establishes delegated sub-turns that browse a signed-in workspace with no operator present. `AutoDenyAsk` (`loop.go:594-599`) makes an `ask`-policy tool auto-deny, but it is set only for headless/scheduled runs (`loop.go:6958`) and **is not inherited by delegated sub-turns — issue #659, open**. D2.9 seeds `browser_upload_file` as `ask` for every agent. **If D2.9 ships without #659, the first delegated sub-turn to reach it hangs on an approval nobody can answer.** FR-032 makes the auto-deny a D1 requirement rather than a D2 assumption, because D1 is what creates the unattended browsing.
- **Capture / ADR-048** (`browser_webrtc.go:70-78`, `config.go:3826-3844`): the registry is `map[agentID]*CaptureSession`, and ADR-048's conflict rules ("bring the requesting agent's tab to front", "deny while another agent's session is actively viewed") assume agents have disjoint tab sets. Under D1 they share one. FR-016a re-keys the registry to the browsing key and collapses "requesting agent" — **one capture session per workspace browser**.
- **Audit** (`pkg/audit`): two new event types, existing severity/format conventions, no new sink.
- **SPA**: no wire field change, so no required SPA change. The `browser_list_tabs` payload changes from `browser_started: bool` to `state: enum`, which alters JSON persisted in `sessions/<id>/<YYYY-MM-DD>.jsonl`; verified safe — `grep -rn "browser_started" src/` returns nothing, so the SPA does not read it. The `src/store/ui.ts:135` comment wording is cosmetic.
- **Platform (Windows/POSIX):** the write lease is an in-process `sync` primitive, real everywhere, and deliberately **not** `fileutil.WithFlock` (a documented no-op on Windows, `pkg/fileutil/flock_windows.go`). The **pool's per-key launch lock is a different matter**: it *is* an on-disk lock, and it inherits the Windows no-op. Two gateway processes on one `$OMNIPUS_HOME` are already unprotected for every other file store on Windows (ADR-054 §5.1); the pool does not make that worse and does not fix it (§12, A11).

---

## 7. User stories & acceptance criteria

**US-1 (P0) Agent handover.** As an operator, when I switch the chat from one agent to another mid-session, the new agent sees and drives the browser I was just using, with no handover step.
- *Why P0:* the reported defect (ADR §1.1); ADR criterion 2.
- **AC1: Given** workspace W's browser holds one tab, **When** an agent on W's team with `browser_list_tabs: allow` calls it, **Then** that tab is returned regardless of which agent opened it.
- **AC2: Given** the same, **When** the operator issues no handover command of any kind, **Then** AC1 still holds.
- **AC3 (the seam that actually carries this): Given** two agents on W, **When** both call a browser tool, **Then** both `Execute` calls resolve to the **same** `*BrowserManager` instance, obtained through `ManagerResolver` at call time — not one captured when their tools were registered.

**US-2 (P0) Human browses first.** As an operator, I open the browser panel and browse before deciding which agent to ask, and an agent still takes over.
- **AC1: Given** the operator browsed via the live panel in workspace W, **When** a browser-allowed agent on W is asked what is open, **Then** it sees the operator's tab.
- **AC2:** the panel path and the tool path resolve the same manager for the same (agent, chat session) pair.

**US-3 (P0) Cross-workspace isolation.** As an operator with two clients in two workspaces, a login in one is invisible in the other.
- *Why P0:* ADR criterion 5b.
- **AC1: Given** a login established in workspace X, **When** the same site is opened from workspace Y, **Then** Y is logged out **and** X's and Y's Chrome **pids differ** **and** their `--user-data-dir` paths differ.
- **AC2: Given** the same, **Then** neither Chrome was created via `target.CreateBrowserContext` — `contextCount()` no longer exists and no CDP context is created anywhere (FR-031).

**US-4 (P0) No surprise logout.** A new chat in the same workspace is still logged in.
- **AC1: Given** a login established in chat C1 of workspace W, **When** a new chat C2 in W opens the same site, **Then** it is still logged in **and** both resolve to the same Chrome pid. (ADR criterion 5c.)

**US-5 (P0) Delegated work shares the workspace browser.** As an operator, work I delegate uses the same signed-in browser as the chat I delegated from.
- *Why P0:* ADR criterion 17, **inverted** by the D1.10 ruling — it now asserts sharing, so a future change that silently isolates delegated work fails here.
- **AC1: Given** workspace W's browser holds a login, **When** a delegated sub-turn under `spawnSubTurn` opens that site, **Then** it is **logged in** and its resolved key is `ws:W`.
- **AC2: Given** the same, **Then** no second Chrome, no second manager and no second profile directory was created for the sub-turn.
- **AC3 (the risk this accepts, made testable): Given** a delegated sub-turn reaches a tool whose policy is `ask`, **When** no operator is attached, **Then** the call is **denied** with the headless auto-deny reason — never queued for an approval nobody can answer. (FR-032; depends on #659.)

**US-6 (P0) A workspace-less turn resolves, never merges.** A heartbeat turn's browser is the same workspace's browser as its files.
- **AC0 (corrected premise — round-2 MAJ-113): Given** a **heartbeat** turn, **Then** `ToolWorkspaceID(ctx)` is **not** empty and rung 1 resolves it. Heartbeat jobs are named `heartbeat:<workspaceID>:<agentID>` and that workspace is stamped onto the session meta before the run (`schedules.go:581-639` → `loop.go:6934-6957`), so a heartbeat never reaches rung 2 and an agent on several workspaces gets one deterministic browser per heartbeat job. The previous draft designed rung 2 against the belief that these turns arrive bare; they do not, and the requirement is asserted here so a future change to the stamping path fails this AC rather than silently re-routing every heartbeat through the ambiguity refusal.
- **AC1: Given** a turn with `ToolWorkspaceID(ctx) == ""` — in practice a **plain, operator-created** schedule, which resolves to `""` because ADR-065 FR-8 removed `resolveScheduleWorkspaceID`'s channel source (`schedules.go:632-639`) — whose work dir was re-rooted into workspace W, **When** it calls a browser tool, **Then** it reaches W's browser — the same id `FindForAgentPreferring` gave `resolvepath.go:713`.
- **AC2: Given** two agents on **no** workspace, **When** each opens the same site, **Then** neither sees the other's session, because **both calls fail** with `ErrNoBrowsingContext`.
- **AC3: Given** an agent on the CoreTeams of workspaces A and B and a turn carrying no workspace id, **When** it calls a browser tool, **Then** it is **refused** with the ambiguity named and both candidates logged at WARN — **not** silently given A. (FR-033.)

**US-7 (P0) Distinguishable tab states.** As an agent, I can tell "no browser here yet" from "a browser with nothing open".
- **AC1:** no browser for the resolved key → `state: "no_context"`, and the model-visible text says so.
- **AC2:** a live browser with ≥1 tab → `state: "open"` with the tabs.
- **AC3:** a live browser momentarily with 0 tabs → `state: "empty"`, distinct from AC1.
- **AC4:** the three values are the complete closed set; no fourth value is emitted for any input. **There is no "denied" member** — ADR D1.12 withdrew it as unreachable, §17 C3.
- **AC5 (D1.9a): the payload says whose tabs.** The result carries the calling agent's own tab set and, separately labelled, the workspace-owned set the **operator** opened. An agent's own set never contains another agent's tab. (FR-048.)

**US-22 (P0) Tabs stay mine; the operator's tab is everyone's.** As an operator, an agent opens its own tab by default, and only the tab *I* opened is one my whole team can see and be asked to take over.
- *Why P0:* ADR D1.9a, 2026-09-01 — and it is the ruling that actually fixes §1.1. It is also the requirement most easily deleted by accident: FR-001's manager collapse removes the per-agent separation unless FR-048 carries it explicitly (§0.2a).
- **AC1: Given** agents A and B on workspace W, **When** A opens a tab and B lists tabs, **Then** B does **not** see A's tab in its own set.
- **AC2: Given** the same, **When** the **operator** opens a tab through the live panel, **Then** both A and B see it, labelled as the workspace's.
- **AC3 (the regression guard): Given** one `BrowserManager` per workspace, **When** A and B each open a tab, **Then** the manager holds **two distinct** `sessionEntry` values and neither agent's `tabs` slice contains the other's `tabEntry`. *A test that asserts only "both agents resolved the same manager" passes with the tab sets merged — which is the state this AC exists to fail.*
- ~~**AC4 (`MaxTabs`)**~~ **WITHDRAWN with FR-049 (ADR D1.5a).** It required a five-tab cap to stay per **agent** rather than silently becoming five for the team. `tools.browser.max_tabs` is deleted from the code, so there is no cap to keep per-agent and nothing to assert. **The story is not weakened:** AC1 and AC3 carry the whole of US-22, and they are about *ownership* — whose tab set a tab lands in — which is untouched. Capacity moved to US-15/AC13, where a runaway tab loop is stopped by memory rather than by a number.

**US-8 (P1) A denied agent cannot reach the tool at all — and that is as far as this spec goes.**
- *Status:* **AC2 and AC3 are WITHDRAWN by ADR D1.12** (§17 C3). AC1 survives, restated, because it is true and testable; the story's original goal is not reachable from inside a tool.
- **AC1: Given** an agent whose policy denies `browser_list_tabs`, **When** the turn's tool definitions are built, **Then** the tool is **absent from them** — `FilterToolsByPolicy` `continue`s past a deny verdict (`pkg/tools/compositor.go:436-438`) — so no call is made, `ListTabsTool.Execute` is never entered, and no tab payload exists.
- ~~**AC2**~~ **WITHDRAWN.** It required `tool_denial.go`'s `ModelMessage` to name the browser surface. That message has **no production caller** for a tool the model was never shown, so the assertion would test a string nothing emits.
- ~~**AC3**~~ **WITHDRAWN with AC2.** Holdout 4 is rewritten (§13) to record the honest outcome rather than the desired one.
- **The unfixed consequence, stated rather than closed.** Mia is the default agent and the agent in §1.1's own repro. Asked what is open, she still answers from absence — same output, different cause. Fixing it means telling an agent, **outside** the tool-result path, that its workspace has a browser it is not permitted to drive: a system-prompt or manifest-note surface. The operator has confirmed Mia's and Ava's deny stays, so widening policy is not the answer. **ADR §6 owns this as its own headline defect surviving in a narrower form**, and this spec does not claim it.

**US-9 (P0) Two writers, one *shared* tab.** Concurrent browser work by two agents on the **operator's** tab neither corrupts a page nor errors. *(Rescoped by D1.9a: on their own tabs two agents cannot contend at all — AC0.)*
- **AC0 (the case that no longer exists): Given** agents A and B on workspace W each driving **their own** tab, **When** both issue `browser_navigate` concurrently, **Then** both complete, neither defers, and no lease is acquired by either. Contention is not merely rare here — it is structurally impossible, and asserting it is how a future change that re-merges the tab sets gets caught by the concurrency suite as well as by US-22/AC3.
- **AC1:** two agents issuing `browser_navigate` **against the workspace-owned tab** concurrently — neither observes the other's mid-navigation state, neither returns `IsError=true`, **both eventually complete**, and at most one reports a deferral. *Asserting only "neither errors" would pass when nothing happened, which is why "both eventually complete" is the assertion (ADR criterion 16).*
- **AC2:** a human holding the live-view control lock outranks the lease; the deferral reason is ADR-038 D6's text and the lease was never acquired.
- **AC3:** an action tool that panics or is cancelled while holding the lease does not prevent the next acquire within `leaseWaitTimeout`.
- **AC4:** exempt tools are never deferred. The exempt set is **six** (§14 rule 3 is the normative count): four read-only tools shipped today — `browser_screenshot`, `browser_get_text`, `browser_wait`, **`browser_list_tabs`** — plus `browser_snapshot` (read-only, D2 FR-018) and `browser_handle_dialog` (**recovery** — D2 FR-035).
- **AC4a (the omission that made the headline demo defer):** `browser_list_tabs` is exempt. It is registered (`register.go:76`), it is read-only, and its own file says so (`tabs.go:20`: *"Read-only — NOT gated by controlledResult"*). The previous draft's closed set of five put it in neither category, so under AC5 it would have taken the **write** lease — making Jim's `browser_list_tabs`, the literal call in behavioural contract 1, US-1/AC1 and the headline BDD scenario, return `{"deferred": true}` whenever another agent held the lease for a long `browser_navigate`. **The feature's headline demo would have deferred behind an unrelated agent.**
- **AC5:** a `browser_*` tool takes the write lease **if and only if** it is gated by the ADR-038 D6 human-control lock (`controlledResult`). That biconditional — not a hand-written list — is the rule, and it holds exactly over the eleven tools shipped today (§14 rule 3's table). A tool that is leased but not control-gated, or control-gated but not leased, fails the gate (FR-019a).

**US-10 (P1) The wire contract holds, and its one semantic change is visible.** As a maintainer, no field changes — and the meaning change that *does* happen is not smuggled through as prose.
- **AC1:** `make verify-contracts` exits 0 with no `properties:`/`required:`/`enum:`/`type:` change.
- **AC2:** `BrowserAttachFrame.session_id` and `BrowserWebRTCOfferFrame.session_id` change from correlation-only to **workspace-resolving**. Their current text says the server binds *"regardless of the value sent here"* and *"agent_id is the binding key"*; the replacement text is quoted verbatim in FR-016 and reviewed as a behavioural change.
- **AC3:** `BrowserInspectRequest.session_id` is a **browser** session id, not a chat session id (`"Browser session id (context/correlation; the live tab is the agent's default)"`). It therefore does **not** gain workspace semantics; `browser_inspect` resolves from the agent alone, subject to FR-033's ambiguity refusal, and its description is corrected to say so.
- **AC4:** a test asserts the reversal — a client sending a `session_id` belonging to a different workspace's chat is routed to that workspace or refused, **never** silently served the agent's default. Without this, the gate cannot fail for the change it polices.

**US-11 (P1) An agent on two workspaces is unambiguous in the panel.** The panel and the agent show the same browser.
- **AC1: Given** an agent on the CoreTeam of workspaces A and B, **When** the operator chats in a session stamped `workspace_id=B` and opens the panel, **Then** both the turn's tools and the panel resolve to B.
- **AC2: Given** no session `workspace_id`, **Then** both refuse identically (FR-033) — they never disagree, and they never both silently pick A.

**US-12 (P1 → effectively P0 under D1.5a) Memory stays bounded; browsers get closed.** Workspace-keying must not leak Chrome processes.
- *What D1.5a changed about this story:* nothing in its ACs, and everything about what they are worth. Idle close used to be **housekeeping** behind a counter that bounded the pool anyway; it is now **half the entire defence** (US-15/AC14, FR-061). Its priority label is left at P1 rather than renumbered, because other documents cite the numbering — read it as P0 in practice.
- **AC1:** tabs idle past `IdleTTL` with no viewer are reaped, per-tab, unchanged.
- **AC2:** one viewer attached in one chat of workspace W pins W's whole browser — the documented, accepted consequence of the re-key.
- **AC3:** a workspace is deleted, or loses every browser-policy-allowed agent from its CoreTeam → its Chrome is closed and its pool entry released.
- **AC4:** a workspace browser with zero tabs and zero viewers for longer than `tools.browser.idle_close_ttl` is **closed entirely** (process gone, `LiveKeys()` shrinks) while its **profile directory survives** on disk.
- **AC4a (the state the previous draft never described):** after AC4's close, the workspace's `browserMgrs` entry and its `*BrowserManager` **still exist** — FR-026a's liveness predicate says the *key* is live while the workspace exists, and that is deliberately a different question from whether a *Chrome process* is running. **When** a tool call for that workspace arrives, **Then** `pool.Acquire` relaunches Chrome from the surviving profile, `LiveKeys()` grows by one, and the session that was logged in **is still logged in**. No error, no second manager, no re-registration.
- **AC5:** running K delegated sub-turns to completion returns `len(LiveKeys())` and the manager count to their pre-run values — sub-turns create no browser of their own (US-5/AC2) and therefore leak none.
- **AC6:** the reaper and the pool do not fight. `ReapIdleSessions` already cancels a session's `browserCancel` and calls `coord.ReleaseTab` in its own removal branches (`manager.go:3027-3032`, `:3073-3078`, `:3118`, `:3123-3125`), so a sweep can leave a manager whose browsing context is cancelled while the pool still lists that key as live. **Given** that state, **Then** the next `Acquire` for the key produces a working browser rather than a live-but-undrivable one, and `LiveKeys()` never counts a Chrome nothing can drive (FR-040a).

**US-13 (P1) Repudiation, per action.** As an operator, I can answer "which agent made that purchase" — not merely "which agents have ever touched this browser".
- *Why this changed:* **ADR D2.11 rejects first-use-only auditing by name** — *"An event on first use of a context an agent did not establish fires once per agent per workspace and says nothing about the tenth action, or about which agent made the purchase."* Revision 3 shipped exactly that and cited D2.11 while doing so, i.e. cited the section that decides against it (§17 C2). D1.10's sharing ruling is what makes the difference load-bearing: every agent on the workspace can act as the signed-in user.
- **AC1:** a browser's creation records key, workspace and establishing agent. **One event per instance creation.**
- **AC2 (replaces first-use-only):** **every write-class browser tool call** emits an audit event carrying **workspace id, agent id, tool name and target host**. The write-class set is the `controlledResult`-gated set — the same classification §14 rule 3 uses for the lease, so there is one list, not two.
- **AC3:** read-only tools are **not** audited per call. They do not act as the signed-in user, and auditing them would bury AC2's events in the ones that do not matter.
- **AC4 (the viewer, and it is not cosmetic):** every event name matches `^[a-z_]+$` — the pattern `contracts/components/schemas/AuditEntry.yaml:17` enforces. A dotted name does not merely render oddly; it **blanks the whole Audit Log viewer** (issue #667). An audit trail nobody can read is not a mitigation. (FR-058.)
- **AC5 (volume, stated rather than discovered):** per-action auditing on a browsing agent produces materially more events than per-first-use. That is the point, and it is bounded by the same thing that bounds tool calls. No sampling, no coalescing, no "first N per turn" — any of those reintroduces the gap D2.11 rejected.

**US-14 (P1) An agent with no workspace is told the truth.** As an operator, when the browser will not work I learn *why*.
- *Context:* a fresh install **is** covered — `ensureDefaultWorkspace` seeds "My Workspace" with Jim and Ray (§6). The gap is a **custom** agent, which is deliberately never auto-added to a team.
- **AC1: Given** a custom browser-allowed agent on no workspace, **When** it calls a browser tool, **Then** the error is `ErrNoBrowsingContext`'s text, which names the remedy ("add this agent to a workspace's team").
- **AC2: Given** the same agent, **When** the operator attaches the live panel, **Then** the panel shows a reason distinguishing *"this agent is not on a workspace"* from *"browser tools are not registered for this agent"* — today both render as the latter (`browser_inspect.go:75-77`, `browser_ws.go:1252-1262`). (FR-008a.)
- **AC3:** the **ambiguous** and **not-registered** cases each render their own distinct reason (`BrowserResolveOutcome`, **three values**). *(Revision 3 wrote "the pool-full and ambiguous cases"; `BrowserResolvePoolFull` was deleted with FR-039 — §3.1's interface comment says so explicitly — and this AC was not swept at the time. D1.5a brings a refusal **back** at FR-053, but it is a **tool-call error naming memory**, not a resolve outcome: the panel's question is "which browser does this agent get?", which memory pressure does not change. **The enum stays at three** and no `contracts/` surface is added — SC-007 condition (2) depends on that.)*

**US-15 (P0) Capacity manages itself, and when it cannot, it says *memory*.** As an operator on a sized host, opening an eleventh workspace's browser must not make me do anything, must not tell me anything, and must not lose my logins — and on the day the host genuinely cannot take another, what I am told must be *the host is out of memory*, not the name of a setting I could raise.
- *Why P0, and why this story has now been rewritten twice:* **ADR D1.7** removed the refusal surface (*"there is no 'pool full' error surface and no UI change"*; at the cap, *"evict the least recently used instance and start the new one"*), tombstoning revision 3's design at §17 C1/M1. **ADR D1.5a then removed the cap itself** — *"the memory gate is the ONLY admission control… every other limit is deleted from the codebase"* — so the *target*, the *ceiling*, the *`+1` overshoot* and the *global tab budget* this story used to specify are all gone (§0.6). What survives is eviction, its two guards, and one gate. **Nothing below is an amendment of the withdrawn ACs; each states why it went**, because a silent drop of AC9/AC10 would read as the cap quietly becoming unbounded by oversight rather than by ruling.
- **AC1 (eviction is silent and non-destructive — its TRIGGER is now the gate): Given** the live-memory gate refuses a launch and at least one instance is evictable, **When** a turn resolves to a new workspace, **Then** the **least recently used** evictable instance is closed, the gate is **re-asked**, and the new instance launches. No error reaches the agent; nothing reaches the operator; no `contracts/` surface is involved. *(Revision 4's trigger was "the target is reached". There is no target — D1.5a. The behaviour is unchanged; what invokes it is not, and a test that drives eviction by setting a cap no longer has a cap to set.)*
- **AC2 (the claim that makes AC1 acceptable): Given** an evicted workspace, **When** it is next used, **Then** it relaunches from its own profile directory and is **still signed in**. It pays start-up latency and nothing else. *If AC2 fails, AC1 is a data-loss bug, not a capacity policy — this is the load-bearing assertion of the whole eviction design.*
- **AC3 (guard 1 — a viewer): Given** the least recently used instance has a **live** attached viewer, **When** eviction selects a victim, **Then** it is **not** that instance; the second-least-recently-used evictable one is chosen instead. *(This is the case revision-3-era tests never exercised: the guards were only ever driven all-pinned, which cannot distinguish "guard works" from "nothing was evictable anyway".)*
- **AC4 (guard 2 — a call in flight): Given** the least recently used instance has a browser tool call executing — **including a lease-exempt read-only one** — **Then** it is not evicted, and the call completes. The write lease cannot serve as this signal: the exempt set is six tools (§14 rule 3), so a `browser_screenshot` holds no lease at all (FR-051).
- **AC5 (guard 3 — an abandoned panel is not a viewer): Given** an attached viewer whose transport has been silent past the WebRTC liveness window, **Then** it counts as detached for both eviction and idle close. Without this one abandoned panel pins a slot for the process's lifetime, and under eviction that is a **deadlock** rather than a leak (FR-052).
- **AC6 (nothing is evictable — REWRITTEN; the `+1` overshoot is deleted): Given** the gate refuses **and** every instance is simultaneously watched **and** busy, **When** a new workspace is requested, **Then** the request **waits** for an instance to become evictable up to its own tool deadline and then **fails with an error naming the workspace and naming MEMORY as the constraint** — carrying FR-063's reason code. **No instance is started past the gate, not even one.** *(Revision 4 granted exactly one overshoot, `target + 1`, on the reasoning that breaking a soft cap is cheaper than refusing a browse. That trade only exists when the thing being exceeded is a **number we chose**. Here it is the host's real free memory, and exceeding it invokes the OOM killer, which does not stop at the browser — it can take the gateway and every session on it. D1.5a rules the gate a hard stop; between a refused browse and a dead gateway there is no trade to make.)* (FR-053.)
- **AC7 (no setting claims to bound browsers — REWRITTEN):** **Given** the shipped config, **Then** `tools.browser` contains **no key that bounds the number of browsers or tabs** — no `max_browsers`, no `max_tabs`, no `max_total_tabs` — and no documentation string anywhere claims one exists. *(Revision 4's AC7 required `max_browsers`' documentation to admit it was a soft target with a `+1` hard bound, on the principle that a field described as a hard limit which silently overshoots is its own defect. D1.5a satisfies that principle by the shortest route available: the field is gone, so it cannot lie. The principle survives as **AC15** and SC-022 — every capacity message names something real — applied to the only messages that remain.)* (FR-059.)
- **AC8 (thrash is a report, not a symptom — its REMEDY is re-derived): Given** more workspaces browsing concurrently than the host's memory allows, **When** a workspace is reopened within the configured window of being evicted, more than the configured number of times in a rolling period, **Then** the pool logs **exactly one** WARN naming the contending workspaces, naming **memory** as the binding constraint, and naming a remedy that exists — *give the host more memory*, or *browse fewer workspaces at once*. Not one per cycle. *(Revision 4's remedy was "raise `tools.browser.max_browsers`"; the key is deleted, so that sentence would name an action nobody can take — the exact failure SC-022 exists to catch, which prose review has already missed twice.)* (FR-054.)
- **AC8a (its constants are gated, not guessed):** the window and threshold are derived from **G-5**, cold-start latency with a warm profile on disk. Until G-5 runs they are configuration with conservative values and the spec says so; ADR-042's ~30–60 s covers a fresh install *including a Chromium download* and is not the relevant number.
- **AC9 (the launch minimum is measured, and travels with its scope — REWRITTEN): Given** any host, **When** a launch is considered, **Then** it is admitted only if live headroom is at least `PER_BROWSER_COST` ≈ **182 MB**, and every place that figure appears carries its scope: **one machine, one snapshot, macOS, Chrome for Testing, `top`'s physical-footprint column, idle and non-capturing.** A *capturing* browser costs that plus the injected extension plus encoding, and that delta is **unmeasured** (FR-044/G-1). *(Revision 4 computed a **target** from `clamp((min(host_RAM, cgroup_limit) × 0.5 − gateway_reserve) / (FIXED_FLOOR + R×85MB + encoder_page), 1, operator_ceiling)`. Every term in that expression is now gone or withdrawn — see AC9b — and there is no target to compute. Its underlying point still holds and is why nothing is hardcoded: a single measured integer would ship the 3916 MB box's answer to a 32 GB machine.)* (FR-062.)
- ~~**AC9a (`<= 0` on the ceiling)**~~ **WITHDRAWN with FR-038a (ADR D1.5a).** It fixed the edge semantics of a ceiling that is deleted. There is no ceiling, so there is no `<= 0` case, and §17 M7b's finding is dissolved rather than resolved.
- **AC9b (nothing in the capacity path is priced per renderer or per tab — NEW): Given** the launch and tab gates, **Then** no per-renderer and no per-tab byte constant appears anywhere in the capacity arithmetic, and **`--renderer-process-limit` appears nowhere in the launch flags**. *Why:* real renderers spanned **30 MB → 327 MB in one snapshot** — an 11× spread — so no single per-renderer value works; and renderer count is not tab count (a measured instance reported **2 tabs against 13 renderer processes**, ~6 per tab), so anything priced per tab is wrong by roughly that factor. **Not setting the renderer flag is also what preserves full site isolation** — it *weakens* isolation above its bound, and `ValidateURL` (`manager.go:685-708`) permits any public URL, so the trade it asked for was never justified (FR-055's tombstone). (FR-062.)
- ~~**AC10 (the tab budget is a different guard)**~~ **WITHDRAWN with FR-038a (ADR D1.5a).** It required `max_total_tabs` to stay a **global** budget across all N Chromes rather than silently becoming 30 × N. The key is deleted from the code, so there is no global budget to keep global and no per-Chrome mistake to make. Its scenario, its dataset rows and its half of test 53 go with it.
- **AC11 (admission under real pressure — the collision is now RULED): Given** Linux and a cgroup memory limit, **When** `memory.current / memory.max > 0.85`, **Then** the pool does not grow. **The collision with AC1 is resolved, not escalated:** when pressure is high *and* nothing is evictable, the gate wins and the request is **refused** (AC6) — D1.5a rules the gate a hard stop and there is no soft cap left for it to disagree with. *(Revision 4 left this open at §0.5 E-2 and instructed the implementation not to pick silently; the operator has picked.)*
- **AC11a (⚠ the platform gap this ruling opens — RECORDED, NOT DECIDED): Given** macOS or Windows, **Then** there is **no memory signal at all**: `readMemAvailableBytes` returns 0 by deliberate design (`pkg/config/meminfo_other.go`) and `readCgroupMemoryAvailableBytes` returns `(0, false)`, so `availableRAMBytes` (`config.go:655`) is 0 on every non-Linux host. Revision 4 could accept that because a **counter** still bounded the pool there. **With every counter deleted, a blind gate off Linux is not a degraded limit — it is no limit (AC14), on the platform where `PER_BROWSER_COST` was actually measured.** This spec may not choose between implementing a real Darwin/Windows reader, shipping browser support Linux-only, or accepting an unbounded pool off Linux and saying so in the release notes. **§0.5 E-6 escalates it to the operator.**
- **AC12 (the counters are gone from the CODE, not merely unset): Given** the shipped binary, **Then** `MaxTabs`, `MaxTotalTabs`, `TryOpenTab`, `ReleaseTab`, `reservedTabs`, `reserveGlobalTab`, `releaseGlobalTab` and `maxTabsReachedErr` do not exist as symbols, a `config.json` carrying `tools.browser.max_tabs` or `max_total_tabs` is **rejected at load with a named error** rather than silently ignored, and no `_test.go` still asserts a cap. *A key that loads and does nothing is worse than a deleted one: an operator who sets it believes they have a limit they do not have.* (FR-059.)
- **AC13 (a runaway tab loop is stopped by memory, at the tab): Given** an agent looping `browser_open_tab` inside an already-running browser, **When** the host's headroom falls below the gate's threshold, **Then** the **next tab open is refused** — the check runs at all five sites the deleted cap was checked at (`manager.go:1139, 2005, 2047, 2216, 2286`) — and the agent receives FR-063's memory reason code. *A launch-only gate never sees this loop: it opens no browser. And no counter remains to catch it.* (FR-060.)
- **AC14 (neither remaining control may silently do nothing): Given** idle close and the gate are the entire defence, **Then** each carries a test that **fails if the control is a no-op** — a reaper that never closes anything, a gate that always answers "room available" — and neither ships disabled, "best effort", or behind an off-by-default flag on a supported platform. *Previously a counter caught a runaway before memory did; that backstop is gone **by decision**, so a gap in either control is not a weaker limit but no limit.* (FR-061.)
- **AC15 (a memory refusal is legible to the agent): Given** a tab that could not be opened because the host is out of memory, **When** the agent reads the tool result, **Then** the message names **memory** as the cause and a remedy that exists (*close tabs or browsers you are done with, or wait*), and names **no limit and no config key**. *Today the deleted cap produced an actionable sentence; dropping it without a replacement reason code lands every memory refusal in the `default:` arm — "it could not be adopted" — with no reason and no remedy, and an agent that cannot tell "out of memory" from "something went wrong" **retries**, straight back into the loop this ruling accepts the risk of.* (FR-063.)

~~**US-18 (P0) The operator can close a workspace's browser.**~~ **WITHDRAWN — operator ruling, ADR D1.7.**
- The story existed for one reason, stated in its own P0 justification: *"it is the only mechanism that frees a pool slot while people are working, so US-15/AC4 has nothing behind it otherwise."* Under eviction, freeing a slot is not a job anyone does. The REST path, the SPA control, the viewer-notification frame, the idempotent 204 and the `RequireNotBypass` gating all go with it (FR-046, test 59, the *close-is-not-deletion* scenario, SC-018, and Stream E's ownership of the path).
- **`pool.Close(k)` itself survives** and has four callers — idle close, eviction, workspace deletion and gateway `Close()`. Only the operator-facing surface is withdrawn.
- **FR-047 is unaffected.** The team-membership disclosure is a D2.11 obligation about a *grant*, not a pool control, and it is in §0.4.

**US-19 (P1) A crashed gateway leaves nothing behind — on Linux; less, elsewhere, and it says which.** As an operator who `kill -9`'d the gateway (or whose host lost power), the next start is clean.
- **AC1: Given** three stale `$OMNIPUS_HOME/browser/ws-*.pid` markers whose pids are dead **and whose per-key launch locks are acquirable**, **When** the gateway boots, **Then** all three markers are removed, their stale locks are cleared, and one INFO line reports the count.
- **AC2 (ON LINUX): Given** a marker whose pid is **alive**, whose per-key launch lock is **acquirable**, and whose `/proc/<pid>/exe` resolves to the Chrome binary this install launched, **When** the gateway boots, **Then** that process is terminated and its marker removed, with a WARN naming the workspace and pid — so it cannot consume host memory outside the target.
- **AC2a (ON macOS AND WINDOWS — the qualification revision 3 omitted): Given** the same state, **Then** the marker is removed and a WARN names the surviving pid, and **the process is NOT terminated.** There is no pure-Go equivalent of `/proc/<pid>/exe` (Hard Constraint #2 forbids shelling out here), so identity cannot be confirmed, and killing an unidentified pid is worse than leaking one. **The residual exposure is stated rather than implied:** an orphan Chrome survives outside the target's accounting, and the operator is told which pid it is. ADR D1.9 records the same asymmetry. *(Revision 3's AC2 carried no platform qualifier and SC-016 asserted "zero orphan Chromes" unconditionally — both false on macOS by this spec's own §12 A20, on a project that ships a macOS Seatbelt backend and whose operator develops on Darwin. §17 M9.)*
- **AC2b (the discriminator is the LOCK, not the marker's pid — D1.8): Given** a marker whose pid is alive and whose per-key launch lock is **held**, **Then** this is a **second live gateway** on the same `$OMNIPUS_HOME`, not an orphan: the pool **refuses to launch that key**, names the other gateway, and **terminates nothing**. A live Chrome pid is present in both cases and cannot tell them apart; on Unix a flock auto-releases when its holder dies, so a *held* lock proves a live neighbour. *(Revision 3's FR-042a rule was "live omnipus-owned pid ⇒ terminate it", which shoots the neighbour. ADR §9.1 names this as the one change the five otherwise-compatible pool FRs need.)*
- **AC2c (Windows has neither guarantee):** `fileutil.WithFlock` is a documented no-op (`pkg/fileutil/flock_windows.go`), the fallback `O_EXCL` lock does not clear on crash, and `pidAlive` returns `true` unconditionally (`coordinator.go:1569-1575`). Boot reconciliation there clears markers, terminates nothing, and warns — the same degraded sense as the rest of the file-store family (ADR-054 §5).
- **AC3: Given** workspace W's profile directory contains a stale `SingletonLock` from an ungraceful exit, **When** W's next browser tool call runs, **Then** Chrome launches successfully from that profile and the login is intact. *Without this, FR-043's promise fails in exactly the case it exists for.*

**US-20 (P1) A departed client's logins depart with them.** As an operator who deletes a client's workspace, I can answer "are their logins gone?" with yes.
- *Why P1 but security-relevant:* ADR D2.11's data-at-rest case; the profile holds session cookies and tokens for a named third party.
- **AC1: Given** workspace W has a browser with a live login, **When** W is deleted, **Then** W's Chrome closes **and** `<profileRoot>/ws-<W>/` no longer exists on disk.
- **AC2: Given** the same, **Then** deletion of the directory happens only after `pool.Close(ws:W)` returns, so no Chrome is writing into a directory being removed.
- **AC3 (the negative cases, now FOUR not five):** idle close, roster change, reload and **eviction** each leave the profile directory **present**. Only workspace deletion removes it. *(Revision 3 listed five, including "the operator's explicit close". FR-046 is withdrawn, so that trigger no longer exists; **eviction** takes its place in the list, and it is the more important of the two — eviction is only acceptable BECAUSE the profile survives it. SC-017 and test 58 carry the corrected arithmetic.)*
- **AC4:** per-key profile directories are created `0700`, matching the mode the shipped code already uses for profile dirs (`coordinator.go:1232`, `manager.go:799`) — stated rather than inherited, because these now hold per-client session cookies.

**US-21 (P1) Adding an agent to a team says what it grants.** As an operator, I learn that adding an agent to a workspace hands it that workspace's live logins **before** I add it, not in a release note afterwards.
- *Why:* ADR D2.11's elevation-of-privilege decision — *"the team-editing UI must state this at the point of adding, not only in release notes"* — which §1's out-of-scope wording left in this spec's scope and which no spec had claimed (round-2 MAJ-114). D1.10 makes it worse than when the ADR wrote it: unattended delegated work now inherits those logins too.
- **AC1: Given** the Workspace → Team editing surface, **When** the operator opens the add-agent control, **Then** the disclosure is visible **before** confirming — not in a tooltip, not only after the fact.
- **AC2:** the text names the concrete consequence, not the mechanism: that the agent will be able to act as whoever this workspace is signed in as, on any site it is signed into, including on turns nobody is watching.
- **AC3:** the same disclosure appears in the release note for this change.

**US-16 (P0) One crash is one workspace.** As an operator, a Chrome crash in one workspace does not stop work in another.
- *Why P0:* ADR-043 accepted "one crash takes down all browsing" when there was one Chrome. With N that acceptance no longer holds.
- **AC1: Given** workspaces W1 and W2 each with a live browser and an attached viewer, **When** W1's Chrome is killed, **Then** W2's pid, tabs, panel stream and login are unaffected.
- **AC2: Given** the same, **When** W1's next browser tool runs, **Then** it relaunches W1's Chrome from W1's **profile directory** — so W1's login survives the crash, which a CDP context could not do (`coordinator.go`'s `watchForCrash` comment: *"recovery is into FRESH empty contexts — prior per-agent cookies/login are lost by definition"*).
- **AC3:** no other workspace's managers are reset by W1's crash — today `watchForCrash` resets **every** connector manager.

**US-17 (P1) Reload preserves every workspace's login.** A Settings save mid-browse changes nothing an operator can see.
- **AC1: Given** two agents on workspace W and a live login, **When** `ReloadProviderAndConfig` runs, **Then** W's Chrome pid is unchanged, the login persists, and `Close`/`disposeBrowserContextRaw` was called **zero** times.
- **AC2: Given** N agents on one workspace, **When** one reload runs, **Then** exactly **one** register/release cycle occurs for that key — not N (FR-026b).

**US-23 (P1) Upgrading does not silently pool anyone's logins.** As an operator upgrading an install that already has a browser profile, I am logged out once, deliberately, rather than having some workspace inherit sessions it never established.
- *Why:* ADR D1.8's upgrade decision. Today there is a **single global** profile at `~/.omnipus/browser/profiles/default/` (`manager.go:125`) holding whatever the operator is signed into. Copying it to every workspace would pool logins across workspaces that never shared them — falsifying US-3 on the first boot after upgrade — and adopting it into one arbitrarily chosen workspace is a silent, unexplainable grant.
- **AC1: Given** an install with a populated `profiles/default/`, **When** the pool first runs, **Then** **no** workspace inherits it: every workspace starts with a fresh `ws-<id>` profile and is logged out.
- **AC2: Given** the same, **Then** `profiles/default/` is **left on disk, untouched and unused.** Deleting it would destroy logins the operator may still want, and no code can tell whether they matter.
- **AC3:** a release-note line states that agents need to sign in again, per workspace, after upgrade. (FR-043b.)

**US-24 (P1) Boot warms one browser, not N.** As an operator on a small host, starting the gateway does not launch a Chrome per workspace.
- *Why:* `WarmAtBoot`, `WarmTabAtBoot` and `WarmCaptureAtBoot` all ship `true` (`pkg/config/defaults.go:679, :685, :692`) and were written for one shared Chrome. Warming every workspace would make every workspace "concurrently browsing" at t=0 — erasing the distinction the target rests on — and multiply `WarmCaptureAtBoot`'s continuous encoder CPU, which runs for `WarmCaptureIdleSec` (300 s, `:695`), by N on a box ADR §7 measures at 85–99 % utilisation.
- **AC1: Given** N workspaces and the warm defaults on, **When** the gateway boots, **Then** **exactly one** instance is warmed: the resolved workspace of the default agent — one instance, one tab, one capture pipeline.
- **AC2: Given** no workspace resolves for the default agent, **Then** boot warms nothing and logs one **INFO** (not a WARN: a missed optimisation, not a fault). (FR-016b.)

---

## 8. BDD scenarios

**Scenario: handover across an agent switch (Happy Path) — US-1/AC1+AC2, FR-001, FR-002, FR-006**
- **Given** workspace W's browser has one tab on `https://example.com/a`
- **And** Mia and Jim are both on W's CoreTeam, and Jim has `browser_list_tabs: allow`
- **When** Jim calls `browser_list_tabs` in a chat stamped `workspace_id=W`
- **Then** the result is `state:"open"` with one tab whose url is `https://example.com/a`
- **And** no handover tool was called

**Scenario: two agents resolve one manager through the real registration path (Happy Path) — US-1/AC3, FR-002a**
- **Given** agents Mia and Jim were registered through `registerSharedTools`, each with their own tool registry
- **And** both are on workspace W's CoreTeam
- **When** Mia's `browser_navigate` opens a tab and Jim's `browser_list_tabs` runs
- **Then** both `Execute` calls resolved the **same** `*BrowserManager` pointer
- **And** no tool struct in `pkg/tools/browser` has a `mgr *BrowserManager` field

**Scenario: human browses first, then an agent takes over (Happy Path) — US-2/AC1+AC2, FR-016, FR-017**
- **Given** no agent has used the browser in workspace W
- **When** the operator attaches the live panel with `agent_id=mia`, `session_id=S` where S's meta carries `workspace_id=W`, and navigates to `https://example.com/a`
- **And** Jim is then asked, in a chat also stamped `workspace_id=W`, to list tabs
- **Then** Jim sees `https://example.com/a`
- **And** the panel and Jim resolved the same manager

**Scenario: cross-workspace isolation by profile (Error-free isolation) — US-3, FR-003, FR-004, FR-037**
- **Given** a login cookie set on `example.com` in workspace X
- **When** `example.com` is opened from workspace Y
- **Then** the document reports no session cookie
- **And** `pool.PID(ws:X) != pool.PID(ws:Y)` and their `--user-data-dir` paths differ
- **And** no `target.CreateBrowserContext` call was made anywhere in the run

**Scenario: new chat, same workspace, still logged in — US-4/AC1, FR-005**
- **Given** a login cookie set on `example.com` in chat C1 of workspace W
- **When** a new chat C2 in W opens `example.com`
- **Then** the session cookie is present **and** `pool.PID(ws:W)` is the same for both

**Scenario: a delegated sub-turn shares the workspace browser (the inverted criterion) — US-5/AC1+AC2, FR-009**
- **Given** workspace W's browser holds a login on `example.com`
- **And** a delegated sub-turn is running under `spawnSubTurn` from a chat stamped `workspace_id=W`
- **When** the sub-turn navigates to `example.com`
- **Then** its resolved key is `ws:W`, the session cookie **is** present
- **And** `len(pool.LiveKeys())` did not increase and no second manager was created

**Scenario: an unattended sub-turn is denied, not hung, on an ask-policy tool (Error) — US-5/AC3, FR-032**
- **Given** a delegated sub-turn with no operator attached
- **And** a tool whose resolved policy is `ask`
- **When** the sub-turn calls it
- **Then** the call is denied with the headless auto-deny reason within the turn
- **And** no approval request was created and the turn did not block

**Scenario: scheduled turn resolves to its re-rooted workspace — US-6/AC1, FR-007**
- **Given** a heartbeat turn for agent `ray`, member of exactly one workspace W, with `ToolWorkspaceID(ctx) == ""`
- **When** it calls `browser_navigate`
- **Then** `ResolveBrowsingKey` returns `ws:W`, the same id `FindForAgentPreferring(home,"ray","")` returns
- **And** the key is never `"default"` and never the agent id

**Scenario: a genuinely workspace-less agent is refused, not merged (Error) — US-6/AC2, FR-008**
- **Given** agents `solo-a` and `solo-b`, neither on any workspace CoreTeam
- **When** each calls `browser_navigate`
- **Then** each result is an error whose text is `ErrNoBrowsingContext`'s
- **And** no browser was launched for either, and `len(pool.LiveKeys())` is unchanged

**Scenario: an ambiguous multi-workspace turn is refused, not arbitrated (Error) — US-6/AC3, US-11/AC2, FR-033**
- **Given** agent `ray` on the CoreTeams of workspaces A and B (A sorts before B)
- **And** a turn carrying no `workspace_id` and no preferred id
- **When** it calls `browser_navigate`
- **Then** the result is an error naming both candidate workspaces
- **And** a WARN was emitted **on the preferring path as well as the plain one**
- **And** neither A's nor B's browser was launched

**Scenario: three tab states are distinguishable (Edge Case) — US-7, FR-013**
- **Given** a resolved key with no browser, **When** `browser_list_tabs` runs, **Then** `state:"no_context"` with an empty `tabs` array
- **And given** a live browser with two tabs, **Then** `state:"open"` with two entries
- **And given** a live browser whose tab set is momentarily empty (post-`CloseTab`, pre-`createFirstTab`), **Then** `state:"empty"` with an empty array
- **And** the three payloads are pairwise unequal

**Scenario: a denied agent never reaches the tool (Error) — US-8/AC1, FR-014**
- **Given** agent `mia`, whose seed (`pkg/coreagent/core.go:848`) grants no `browser_*` entry, so `denyAllThenOverride` (`:466`) stamps an explicit `deny`
- **When** her turn's tool definitions are built by `FilterToolsByPolicy`
- **Then** `browser_list_tabs` is **absent from the definitions** — the deny verdict hits `continue` at `pkg/tools/compositor.go:436-438`
- **And** `ListTabsTool.Execute` is never entered and no tab payload is produced
- **And** *(the assertion revision 3 had and this one deliberately does not)* **no** `ModelMessage` is asserted, because the denial path has no production caller for a tool the model was never shown — ADR D1.12 withdrew that state and §17 C3 tombstones FR-014a

**Scenario: an agent's tabs are its own; the operator's are the workspace's (Happy Path) — US-22/AC1+AC2+AC3, FR-048**
- **Given** agents A and B on workspace W, resolving to the **same** `*BrowserManager`
- **When** A opens `https://example.com/a` and the **operator** opens `https://example.com/op` through the live panel
- **Then** A's `browser_list_tabs` returns its own tab **and** the workspace-owned tab, labelled distinctly
- **And** B's `browser_list_tabs` returns the workspace-owned tab **and not** A's
- **And** the manager holds **two distinct `sessionEntry` values** and neither one's `tabs` slice contains the other's `tabEntry` — the assertion that fails if the re-key merged the sets
- **And** B cannot switch to, drive or close A's tab

~~**Scenario: the per-agent tab cap survives the re-key (Edge Case) — US-22/AC4, FR-049**~~ **TOMBSTONED by ADR D1.5a.**
- It drove `tools.browser.max_tabs = 5` and asserted A's sixth tab was refused while B could still open five of its own. **The key, the refusal and `maxTabsReachedErr` are all deleted from the code** (FR-059), so every step of it asserts machinery that will not exist.
- **Nothing about tab OWNERSHIP is lost with it** — that is *agent's-tabs-are-its-own*, which is untouched. What is lost is the only scenario in which a tab open was ever refused, and its replacement is *runaway-tab-loop-is-stopped-by-memory* below.

**Scenario: two agents on their OWN tabs never contend (Happy Path) — US-9/AC0, FR-048**
- **Given** Jim and Ray on workspace W, each with a tab of their own
- **When** both call `browser_navigate` within the same millisecond
- **Then** both navigations complete
- **And** neither result carries `deferred`
- **And** the write lease was never acquired by either — under D1.9a this contention does not exist, and a run in which it does means the tab sets merged

**Scenario: two agents write to the OPERATOR's tab; both eventually complete — US-9/AC1, FR-019, FR-020**
- **Given** Jim and Ray on workspace W and one workspace-owned tab the operator opened
- **When** both call `browser_navigate` against **that** tab within the same millisecond
- **Then** exactly one navigation is observed by Chrome at any instant
- **And** **both calls eventually complete** — the loser retries inside the tool, within its own deadline, and succeeds
- **And** if the bound is exhausted the loser returns `IsError=false` with a body parsing to `{"deferred": true, "reason": <non-empty>}` naming the holder
- **And** neither result is a Go error
- *Asserting only "neither errors" would pass when nothing happened; "both eventually complete" is the assertion (ADR criterion 16).*

**Scenario: human control outranks the lease — US-9/AC2, FR-022, FR-002c**
- **Given** a human viewer holds the control lock on W's browser, taken under the key `ws:W`
- **When** an agent calls `browser_click`
- **Then** `controlledResult` detected the lock — having asked `IsControlled(ws:W)`, not `IsControlled("default")`
- **And** the deferral reason is ADR-038 D6's human-control text and the lease was never acquired

**Scenario: a panicking action tool does not wedge the browser — US-9/AC3, FR-024**
- **Given** an action tool acquires the lease and then panics (or its ctx is cancelled)
- **When** another action tool runs on the same browser
- **Then** it acquires the lease within `leaseWaitTimeout` and completes normally

**Scenario: read-only tools are never deferred, including list_tabs — US-9/AC4+AC4a, FR-021**
- **Given** Jim holds the write lease on W's browser for a long navigation
- **When** Ray calls `browser_screenshot`, `browser_get_text`, `browser_wait` and **`browser_list_tabs`**
- **Then** all four execute; none returns a `deferred` body
- **And** in particular `browser_list_tabs` returns the current tab set rather than deferring — the handover answer must not queue behind an unrelated agent's navigation

**Scenario: lease membership follows the control-lock gate, not a list — US-9/AC5, FR-019a**
- **Given** the tool registry after `RegisterTools`
- **When** each registered `browser_*` tool is exercised twice: once with a human holding the control lock, once with another agent holding the write lease
- **Then** for every tool the two answers agree — it defers under both, or under neither
- **And** the tools that defer under both are exactly `{browser_navigate, browser_click, browser_type, browser_evaluate, browser_switch_tab, browser_close_tab, browser_open_tab}`
- **And** the tools that defer under neither are exactly `{browser_screenshot, browser_get_text, browser_wait, browser_list_tabs}`
- **And** no registered `browser_*` tool falls outside those two sets

**Scenario: the pool evicts the LRU and launches, silently (*pool-evicts-lru-under-pressure*) — US-15/AC1+AC2, FR-050, FR-057**
- **Given** live browsers for W1 (least recently used, idle, no viewer) and W2, W1 holding a login, and a **fixture memory reading under which the gate refuses a third launch** *(revision 4's Given was "a derived target of 2"; there is no target — D1.5a. The observable behaviour below is unchanged; only what triggers it is)*
- **When** a turn resolves to workspace W3
- **Then** W1's Chrome is closed, W3's launches, and `LiveKeys()` is `{ws:W2, ws:W3}`
- **And** the tool result is a **success** — no error reaches the agent and nothing reaches the operator
- **And** `<profileRoot>/ws-W1/` still exists on disk
- **And when** W1 is next used, **then** it relaunches from that profile and **its cookie is still present**
- *The last two steps are the load-bearing ones: without them eviction is data loss, not a capacity policy.*

**Scenario: eviction skips the instance with a viewer and takes the next one — US-15/AC3, FR-050, FR-010**
- **Given** a fixture memory reading under which the gate refuses, and live browsers for W1 (least recently used, **live viewer attached**) and W2 (second-least, idle)
- **When** a turn resolves to W3
- **Then** **W2** is evicted and W1 is untouched — its pid, its tabs and its viewer stream all survive
- *The all-pinned case cannot distinguish "the guard works" from "nothing was evictable anyway"; this scenario is the one that can.*

**Scenario: eviction skips the instance with a lease-EXEMPT call in flight — US-15/AC4, FR-051**
- **Given** a fixture memory reading under which the gate refuses, W1 least recently used with a long `browser_screenshot` executing against it, and W2 idle
- **When** a turn resolves to W3
- **Then** W2 is evicted, W1 is untouched, and the `browser_screenshot` completes normally
- **And** the write lease was **not** consulted — `browser_screenshot` holds none (§14 rule 3's exempt set is six), which is precisely why `InFlight()` exists

**Scenario: an abandoned panel stops pinning a slot — US-15/AC5, FR-052**
- **Given** W1 has an attached viewer whose transport has been silent past the WebRTC liveness window
- **When** eviction selects a victim, and separately when `CloseIdle` sweeps
- **Then** W1 counts as having **zero** live viewers in both
- **And** W1 is evictable and idle-closable
- *Without this, one abandoned panel makes a slot permanently unreclaimable — under eviction that is a deadlock, not a leak.*

**Scenario: nothing is evictable — wait, then refuse, naming memory (*nothing-is-evictable-then-refuse*) — US-15/AC6, FR-053, FR-057**
- **Given** a fixture memory reading under which the gate refuses, and live browsers for W1 and W2, **each with a viewer attached and a tool call in flight**
- **When** a turn resolves to W3
- **Then** the call **waits** for an instance to become evictable, up to its own tool deadline
- **And** on that deadline it returns an error that identifies W3 and names **memory** as the constraint, carrying FR-063's `memory_pressure` reason code
- **And** `len(LiveKeys())` is still `2` — **no third instance was started**, not even one
- **And** the error text names **no limit and no config key**, because none exists to raise
- *Revision 4 asserted the opposite of the middle step: a third instance started (`target + 1`, total) with a WARN, and only a fourth request was refused. **D1.5a deletes the soft cap and rules the gate a hard stop**, so there is nothing left to overshoot — exceeding real free memory invokes the OOM killer, which can take the gateway with it.*

**Scenario: thrash is reported once, with a remedy that exists (*thrash-warns-once*) — US-15/AC8, FR-054**
- **Given** a configured thrash window and threshold, and more workspaces browsing concurrently than the host's memory allows
- **When** a workspace is evicted and reopened more than `threshold` times within `window`
- **Then** **exactly one** WARN is emitted, naming the contending workspaces, naming **memory** as the binding constraint, and naming a remedy that exists — *give the host more memory*, or *browse fewer workspaces at once*
- **And** driving a further `2 × threshold` cycles inside the same rolling period emits **no additional** WARN
- **And** the WARN names **neither `tools.browser.max_browsers` nor any other config key** — the assertion is on the absence, because revision 4's remedy was "raise `max_browsers`" and that key is deleted (SC-022's rule: trace every named action to the function that performs it, or reject)

~~**Scenario: the ceiling's edge values, and what they do NOT mean — US-15/AC9a+AC10, FR-038a, FR-056**~~ **TOMBSTONED by ADR D1.5a.**
- It asserted `max_browsers <= 0` means "no operator ceiling" rather than "unlimited", and that `max_total_tabs` stays a **global** budget across N Chromes rather than becoming 30 × N. **Both keys are deleted** (FR-059): there is no ceiling to interpret, no derived target behind it, and no global tab budget to keep global.
- **Not re-derived.** These were edge semantics *of a cap*; with no cap there is no edge. §17 M7b's finding is dissolved rather than resolved, and both of its tests go with it.

**Scenario: the capacity path holds no constant, and no target (*no-constant-in-the-capacity-path*) — US-15/AC9+AC9b, FR-062** *(replaces the tombstoned *the target is derived from memory, not shipped as a constant*, which computed `clamp((min(host_RAM, cgroup_limit) × 0.5 − gateway_reserve) / (FIXED_FLOOR + R×85MB + encoder_page), 1, operator_ceiling)` — every term of which is now gone or withdrawn)*
- **Given** fixture memory values for a 3916 MB host and separately for a 32 GB host
- **When** a launch is considered on each
- **Then** the decision is `headroom >= PER_BROWSER_COST` (≈182 MB) on both — **the same check**, giving different answers because the hosts differ, not because a target was computed
- **And** the source contains **no per-renderer byte constant and no per-tab byte constant** anywhere in the capacity path — asserted structurally, because the measured spread was **30 MB → 327 MB in one snapshot** (11×) and renderer count is not tab count (**2 tabs, 13 renderers**)
- **And** no literal browser-count value appears in `pkg/config/defaults.go`
- **And** every doc comment quoting ≈182 MB also states its scope: **macOS, one snapshot, Chrome for Testing, idle and non-capturing**

**Scenario: no per-key Chrome carries a renderer limit, and site isolation is full — US-15/AC9b, FR-062** *(this is revision 4's *every per-key Chrome carries the renderer floor* **inverted**, not deleted — the flag it asserted is now forbidden, and the property that mattered is asserted more strongly)*
- **When** any per-key Chrome is launched
- **Then** its argv contains **no** `--renderer-process-limit` at any value
- **And** two cross-site tabs opened in one workspace occupy **distinct renderer processes** (ADR criterion P8) — which now holds for *every* tab, not only for those below a bound
- *The flag **weakens** site isolation above its bound: over-limit navigations reuse same-site processes. It was justified as acceptable for "semi-trusted destinations", and `ValidateURL` (`manager.go:685-708`) permits any public URL, so that adjective was never earned. **Not setting it dissolves C-303 / C4 / C206 rather than mitigating them** — there is no residual trade-off left to record.*

**Scenario: admission stops under real memory pressure (*pressure-gate-thresholds*) (Edge Case) — US-15/AC11+AC11a, FR-057**
- **Given** Linux with a cgroup memory limit, and fixture `memory.current / memory.max` values of 0.84, 0.85 and 0.86
- **When** a turn resolves to a workspace with no live browser and **at least one instance is evictable**
- **Then** at 0.84 and 0.85 the pool proceeds; at 0.86 it evicts the LRU instance and re-asks the gate
- **And when** pressure is 0.86 **and nothing is evictable**, **then** the request is **refused** — this case is now **asserted**, where revision 4 deliberately left it out. *(It was omitted because "refuse to grow" (D1.5 item 3) and "always evict-and-launch, never refuse" (D1.7) gave opposite answers, the ADR had not decided, and a test picking one would have ratified a decision nobody made. **D1.5a decides it**: the gate is a hard stop, the cap is soft, and with the cap deleted only the hard stop remains — §0.5 E-2, now struck through.)*
- **And given** macOS or Windows, **then** the gate has **no signal to read at all** — `readMemAvailableBytes` returns 0 by design (`pkg/config/meminfo_other.go`) — **and the run must fail rather than pass quietly**, because with every counter deleted a blind gate there is not a weaker limit but no limit. *This scenario is the one that makes §0.5 **E-6** visible instead of latent; it stays red until the operator rules on which of E-6's three shapes ships.*

**Scenario: the counters are gone from the code, not merely unset (*counters-are-gone*) — US-15/AC12, FR-059**
- **Given** the shipped binary and a repo-wide search
- **Then** `MaxTabs`, `MaxTotalTabs`, `TryOpenTab`, `ReleaseTab`, `reservedTabs`, `reserveGlobalTab`, `releaseGlobalTab` and `maxTabsReachedErr` resolve to **nothing** — in production code **and** in `_test.go` files
- **And given** a `config.json` carrying `tools.browser.max_tabs: 5`, **when** the gateway loads it, **then** it is **rejected with a named error** identifying the removed key
- *A deleted key that still loads and quietly does nothing is worse than the cap it replaced: an operator who sets it believes they have a limit they do not have. This is the same failure shape as the ADR-037 "saved, changed nothing" anti-pattern this project bans.*
- **And** `totalTabCountLocked` **still exists** — FR-048's per-agent tab sets are still counted for listing and for the gate's telemetry; only its use as an enforcement point is removed

**Scenario: a runaway tab loop is stopped by memory, at the tab (*runaway-tab-loop-is-stopped-by-memory*) — US-15/AC13, FR-060**
- **Given** one live browser for workspace W and an agent looping `browser_open_tab` against it
- **And** a fixture memory reading that crosses the gate's threshold at the *k*-th tab
- **When** the loop reaches tab *k+1*
- **Then** that open is **refused**, with FR-063's `memory_pressure` reason code, and the loop does not proceed to *k+2* while the reading holds
- **And** the gate was consulted at **all five** tab-open sites — `createFirstTab` (`manager.go:1139`), `OpenTab` (`:2005`, `:2047`) and `adoptTarget` (`:2216`, `:2286`) — the same five the deleted cap was checked at
- *A launch-only gate never sees this loop: it opens no browser, so it reaches no launch decision. **And no counter remains to catch it** — this scenario is the whole of what stands between the loop and the OOM killer.*

**Scenario: neither remaining control may silently do nothing (*idle-close-actually-closes*, *gate-cannot-vacuously-pass*) — US-15/AC14, FR-061**
- **Given** a browser with zero tabs and zero live viewers held past `tools.browser.idle_close_ttl`
- **Then** the sweep **closes it**, and a run in which nothing is ever closed **fails** — the assertion is on the close having happened, not on the absence of an error
- **And given** a fixture memory reading below the gate's threshold, **when** a launch is requested, **then** the gate **refuses**; a gate that answers "room available" for every input **fails** this scenario
- **And** neither control is reachable behind a disabled flag, a "best effort" path, or an off-by-default setting on any supported platform
- *Previously a counter caught a runaway before either of these did. That backstop is gone **by decision**, so a gap in either is not a degraded limit — it is no limit. These two assertions exist because "the control ran and did nothing" is the exact shape of the false green `docs/internal/false-green-patterns.md` catalogues.*

**Scenario: a memory refusal is legible to the agent (*memory-refusal-is-legible-to-the-agent*) — US-15/AC15, FR-063**
- **Given** a `browser_click` whose `target="_blank"` spawns a new tab, and a memory reading under which the gate refuses to adopt it
- **When** `applyReconcileOutcome` (`pkg/tools/browser/tools.go:346-356`) builds the model-visible note
- **Then** the outcome's reason is `tabAdoptReasonMemoryPressure` (`"memory_pressure"`), it matches its **own** arm of the switch, and the text names the host being out of memory **and** a remedy that exists
- **And** the text names **no limit and no config key**
- **And** no adoption refusal reaches the `default:` arm — *"it could not be adopted"*, with no reason and no remedy — which is where every memory refusal would land if the deleted `tabAdoptReasonMaxTabs` were removed without a replacement
- *An agent that cannot distinguish "the host is out of memory" from "something went wrong" **retries**, straight back into the runaway loop this ruling accepts the risk of. Found by the D2 spec.*

**Scenario: the managed-Chromium download still starts at boot — FR-016c**
- **Given** a fresh install with no Chromium on `$PATH` and no managed install, and **zero** live browsing keys
- **When** the gateway boots
- **Then** the managed-Chromium resolution/download has started, in the background, before any browser tool call
- **And** it started without any `BrowserManager` existing — the previous boot path ranged `BrowserManagers()`, which is empty at boot under a lazy pool

**Scenario: one Chrome crash is one workspace — US-16, FR-041**
- **Given** live browsers for W1 and W2, each with an attached viewer
- **When** W1's Chrome process is killed
- **Then** `pool.PID(ws:W2)` is unchanged, W2's tabs and viewer stream survive, and W2's manager was not reset
- **And when** W1's next browser tool runs, **then** W1's Chrome relaunches from W1's profile dir and W1's login is still present

**Scenario: idle close frees the process but keeps the profile — US-12/AC4, FR-040**
- **Given** workspace W's browser has zero tabs and zero viewers, and `tools.browser.idle_close_ttl` has elapsed since it reached that state
- **When** the 1-minute sweep runs (`gateway.go:5321-5352`, after its per-manager `ReapIdleSessions` loop)
- **Then** W's Chrome process is gone and `pool.LiveKeys()` no longer contains `ws:W`
- **And** W's profile directory still exists on disk

**Scenario: an idle-closed workspace relaunches, still logged in — US-12/AC4a, FR-040a**
- **Given** `ws:W` was idle-closed and its `browserMgrs` entry and `*BrowserManager` still exist
- **When** an agent on W calls `browser_navigate`
- **Then** `pool.Acquire(ws:W)` relaunches Chrome from `<profileRoot>/ws/W/`, `LiveKeys()` grows by one, and the site is **still logged in**
- **And** no second `*BrowserManager` was created and no re-registration occurred
- **And** the relaunch succeeded even though the previous exit left a `SingletonLock` in that profile — `cleanStaleSingletons` ran against W's own directory, not just `cfg.ProfileDir`

**Scenario: the reaper cancels a browser context while the pool still lists the key (Edge Case) — US-12/AC6, FR-040a**
- **Given** `ws:W` is live in the pool and its manager's session hits `ReapIdleSessions`' all-tabs-idle branch, which cancels `se.browserCancel` (`manager.go:3073-3078`, `:3123-3125`) and calls `releaseGlobalTab` → `coord.ReleaseTab`
- **When** the next browser tool call for `ws:W` runs
- **Then** it gets a working browser — not a live-but-undrivable one
- **And** `pool.LiveKeys()` never reports a key whose Chrome cannot be driven

**Scenario: a crashed gateway leaves no orphans (Linux) — US-19/AC1+AC2, FR-042a**
- **Given** three `$OMNIPUS_HOME/browser/ws-*.pid` markers survive a `kill -9`: two whose pids are dead, one whose Chrome is still running — and **all three per-key launch locks are acquirable**
- **When** the gateway boots on Linux
- **Then** all three markers are gone, the two stale locks are cleared, and the surviving Chrome — identity confirmed via `/proc/<pid>/exe` — has been terminated
- **And** `len(pool.LiveKeys())` is 0 and no Chrome from the previous run remains on the host
- **And** one INFO names the reclaimed count and one WARN names the terminated workspace and pid

**Scenario: the same boot on macOS leaves the orphan and says so (Edge Case) — US-19/AC2a, FR-042a**
- **Given** the same three markers on macOS
- **When** the gateway boots
- **Then** all three markers are gone and the two stale locks are cleared
- **And** the live Chrome is **still running** — it was not terminated, because `/proc/<pid>/exe` has no pure-Go equivalent and an unidentified pid must not be killed
- **And** a WARN names the surviving pid and states that it consumes host memory the gate cannot attribute to any live key
- *An SC that asserts "zero orphan Chromes" unconditionally cannot pass here, which is why SC-016 is platform-qualified.*

**Scenario: a second live gateway is refused, not shot (Error) — US-19/AC2b, FR-042a**
- **Given** a marker whose Chrome pid is alive **and whose per-key launch lock is HELD** by another running gateway on the same `$OMNIPUS_HOME`
- **When** this gateway boots and later tries to acquire that key
- **Then** it **refuses to launch that key**, names the other gateway, and terminates **nothing**
- **And** the first gateway's Chrome, tabs and panel are unaffected
- *This is the test that distinguishes "reconcile orphans" from "kill the neighbour" (ADR criterion P9; POSIX only — D1.9).*

**Scenario: deleting a workspace deletes its logins — US-20, FR-043a**
- **Given** workspace W has a live browser with a session cookie on `example.com`
- **When** W is deleted
- **Then** `pool.Close(ws:W)` is called exactly once **and returns before** `<profileRoot>/ws-W/` is removed
- **And** `<profileRoot>/ws-W/` no longer exists
- **And given** instead that W was idle-closed, **evicted**, roster-emptied, or reloaded, **then** `<profileRoot>/ws-W/` still exists in **all four** cases
- *Four negatives, not revision 3's five: the operator-close trigger went with FR-046, and **eviction** replaces it — the more important of the two, since eviction is only acceptable because the profile survives it.*

**Scenario: upgrading inherits nothing — US-23, FR-043b**
- **Given** an install whose `~/.omnipus/browser/profiles/default/` holds a live login on `example.com`, and two workspaces
- **When** the pool first runs and each workspace opens `example.com`
- **Then** **both** are logged out
- **And** neither workspace's `ws-<id>` profile is a copy of `profiles/default/`
- **And** `profiles/default/` still exists on disk, unmodified and unused

**Scenario: boot warms exactly one instance — US-24, FR-016b**
- **Given** four workspaces and `WarmAtBoot`/`WarmTabAtBoot`/`WarmCaptureAtBoot` all true (`defaults.go:679, :685, :692`)
- **When** the gateway boots
- **Then** `len(pool.LiveKeys()) == 1`, and the one key is the resolved workspace of the default agent
- **And** exactly one capture pipeline is warmed, not four
- **And given** the default agent resolves to no workspace, **then** `len(pool.LiveKeys()) == 0` and one **INFO** (not WARN) is logged

**Scenario: adding an agent to a team discloses what it grants — US-21, FR-047**
- **Given** workspace W is signed into a site and the operator opens Workspace → Team → add an agent
- **When** the add control is shown
- **Then** the disclosure is visible before confirming, and names the consequence (the agent can act as whoever W is signed in as, including on turns nobody is watching)
- **And** it is not deferred to a tooltip or to a post-hoc toast

**Scenario: lease_wait cannot exceed the action-tool timeout (Error) — FR-023a**
- **Given** a config with `tools.browser.lease_wait = 45s` and `tools.browser.page_timeout = 30s`
- **When** the config loads, and again when it is hot-reloaded
- **Then** `leaseWaitTimeout` is clamped to at most half of `PageTimeout` and a WARN names both keys and both values
- **And** a contended action tool still returns a non-error `{"deferred": true, …}` rather than a CDP timeout error

**Scenario: tabs reap per-tab and a viewer pins the whole browser — US-12/AC1+AC2, FR-025**
- **Given** W's browser has two tabs, one idle past `IdleTTL`, and one attached viewer
- **When** the reaper sweeps
- **Then** the idle tab is reaped, the other is not, and W's browser is not closed

**Scenario: K delegated sub-turns leak nothing — US-12/AC5, FR-026c**
- **Given** baseline `len(pool.LiveKeys())` and manager count recorded
- **When** K delegated sub-turns each perform a browser action and terminate
- **Then** both counts equal their baselines

**Scenario: disposal on workspace deletion — US-12/AC3, FR-026**
- **Given** workspace W has a live browser
- **When** W is deleted and the reload prune runs
- **Then** `pool.Close(ws:W)` is called exactly once and `LiveKeys()` shrinks by one

**Scenario: reload preserves every workspace's login, once per key — US-17, FR-026a, FR-026b, FR-028**
- **Given** **two** agents on workspace W, a live login, and `pool.PID(ws:W)==P`
- **When** `ReloadProviderAndConfig` runs
- **Then** `pool.PID(ws:W)==P`, the login persists
- **And** `pool.Close` was called **zero** times during the reload
- **And** exactly **one** register/release cycle occurred for `ws:W`, not two

**Scenario: the panel names the real reason — US-14/AC2+AC3, FR-008a**
- **Given** a custom browser-allowed agent on no workspace
- **When** the live panel attaches with that `agent_id`
- **Then** the rendered reason says the agent is not on a workspace
- **And** it is **not** the string "browser tools may not be registered for this agent"
- **And given** the pool is full instead, **then** the reason names the cap; **and given** two candidate workspaces, **then** it names the ambiguity

**Scenario: the wire meaning change is caught, not smuggled — US-10, FR-016, FR-029**
- **Given** the implementation is complete
- **When** `make gen-contracts` then `make verify-contracts` run
- **Then** exit 0, and the `contracts/` diff touches only `description:` text in the three browser schemas
- **And** the two reversed descriptions match FR-016's verbatim replacement text
- **And** a client sending an attach frame whose `session_id` belongs to workspace B's chat, with `agent_id` on both A and B, resolves to **B** — proving `session_id` is now binding

**Scenario: audit answers "which agent made THAT purchase" — US-13, FR-027, FR-035, FR-058**
- **Given** Mia establishes W's browser and Jim then performs **ten** write-class actions in it — navigate, click, type — and **five** read-only ones
- **Then** one audit event records the instance creation (key, workspace, agent=mia)
- **And** **ten** events record Jim's write-class calls, each carrying workspace id, agent id, tool name and target host
- **And** the five read-only calls produce **no** per-call events
- **And** the tenth write event is present and attributable — *this is the assertion first-use-only auditing fails, and the reason D2.11 rejects it by name*
- **And** every emitted event name matches `^[a-z_]+$`
- **And** every `pool.Acquire` call in the run carried a key returned by `ResolveBrowsingKey` in the same turn

**Scenario: an audit event name cannot blank the viewer — FR-058**
- **Given** the full set of audit event names this change introduces
- **When** each is matched against `^[a-z_]+$` (the pattern `contracts/components/schemas/AuditEntry.yaml:17` enforces)
- **Then** all match
- **And** a deliberately dotted name in the test fixture **fails** the assertion — a check that cannot fail for a dotted name is not checking anything (#667)

---

## 9. Traceability matrix (FR ↔ US ↔ BDD ↔ test ↔ ADR/review)

| FR | Requirement | US | BDD | Test (TDD) | Source |
|---|---|---|---|---|---|
| FR-001 | One `BrowserManager` per browsing key; `browserMgrs` re-keyed | US-1 | handover | `TestLoop_BrowserManagerForKey_OnePerKey` | D1.1(2) |
| FR-002 | Every browser tool addresses the resolved key **and an explicit `TabOwner`**, not `DefaultSessionID` | US-1, US-22 | handover, agent-tabs-stay-separate | `TestTools_UseResolvedKeyNotConstant` | D1.1(2), D1.9a |
| **FR-002a** | **No tool holds a `*BrowserManager` captured at registration; every tool resolves its manager per `Execute` via `ManagerResolver`** | US-1/AC3 | two-agents-real-registration | `TestRegisterTools_NoBoundManagerField`, `TestHandover_ThroughRealRegistrationPath` | CRIT-002 |
| **FR-002b** | Every one of the 37 `DefaultSessionID` consumers (§2.2) addresses the resolved key; the constant and its alias are **deleted** | US-1, US-9 | human-outranks-lease | `TestNoResidualDefaultSessionID` (repo-wide structural, **including `_test.go`**) | CRIT-005 |
| **FR-002e** | The **364** test-side references across **25** files (§2.2a) are mechanically re-pointed at the browsing key **in the same commit as FR-002b's deletion**, with no alias in test code, no assertion weakened and no test count reduced | US-1 | — | test 5 (repo-wide incl. tests) + the §10.1 diff bar | round-2 CRIT-101/MIN-102 |
| **FR-002c** | `controlledResult` resolves the control lock against the browsing key | US-9/AC2 | human-outranks-lease | `TestControlledResult_UsesResolvedKey` + `tools_control_test.go` re-run | CRIT-005 |
| **FR-002d** | `loop.go:270-279`'s standing "do NOT reintroduce a single shared field" comment is **replaced**, not deleted: the map stays a map, keyed by browsing key, and the comment says why | US-1 | — | `TestLoop_BrowserMgrsCommentIsCurrent` (doc-comment assertion) | MIN-008 |
| FR-003 | Cross-workspace cookie/storage isolation, by Chrome profile | US-3 | cross-workspace-isolation | `TestBrowsingContext_CrossWorkspaceIsolation` | D1.4 / ADR crit 5b |
| FR-004 | Login in X invisible in Y | US-3 | cross-workspace-isolation | same | ADR crit 5b |
| FR-005 | New chat in same workspace stays logged in | US-4 | new-chat-same-workspace | `TestBrowsingContext_NewChatSameWorkspaceSamePID` | ADR crit 5c |
| FR-006 | Agent switch requires no handover step | US-1 | handover | `TestHandover_NoCommandRequired` | ADR crit 2 |
| FR-007 | Resolution ladder: workspace ctx → unambiguous `FindForAgentPreferring` → fail | US-6 | scheduled-turn-resolves | `TestResolveBrowsingKey_Ladder` | D1.11 |
| FR-008 | No workspace ⇒ `ErrNoBrowsingContext`, never a shared browser | US-6/AC2 | workspace-less-refused | `TestResolveBrowsingKey_NoWorkspaceFailsByName` | D1.11 |
| **FR-008a** | The gateway/panel failure reason distinguishes no-workspace / ambiguous / not-registered (`BrowserResolveOutcome`). **Three values, not four** — `BrowserResolvePoolFull` is deleted with the refusal (D1.7) | US-14 | panel-names-real-reason | `TestGateway_ResolveOutcomes_AreDistinct` | MAJ-003 (residual), D1.7 |
| **FR-009** | **A delegated sub-turn uses its workspace's browser and its logins — no separate key, manager, Chrome or profile.** It addresses the **target agent's** own tab set (D1.9a), not the parent's | US-5/AC1+AC2 | delegated-shares-browser | `TestSubTurn_UsesWorkspaceBrowser` | D1.10 (superseding ruling), D1.9a |
| FR-010 | `BrowserManager.Viewers() int` accessor, consumed by the reaper, the pool's idle-close **and eviction's first guard**; counts **live** viewers only (FR-052) | US-12, US-15/AC3 | idle-close, viewer-pin, eviction-skips-viewer | `TestManager_Viewers_ReflectsAttachDetach` | §12 A3, D1.7 |
| ~~FR-011~~ | **WITHDRAWN.** Per-key browser-context map. The D1.10 ruling removed the second key shape; `browserCtxID` stays a single field and is retired entirely by FR-031. No behaviour is specified. | — | — | — | D1.10 |
| ~~FR-012~~ | **WITHDRAWN.** Unattended login-wall failure text. There is no unattended jar to fail against. | — | — | — | D1.10 |
| FR-013 | `ListTabsState` returns a closed 3-value state — `{no_context, open, empty}`, with **no** "denied" member | US-7 | three-tab-states | `TestListTabsState_ThreeDistinctStates` | D1.12 |
| FR-014 | A policy-denied agent **never receives the tool definition** (`compositor.go:436-438`), so `Execute` is never entered and no tab payload exists | US-8/AC1 | denied-agent | `TestListTabs_DeniedAgentNeverReachesTool` | D1.12 |
| ~~FR-014a~~ | **WITHDRAWN by ADR D1.12 — tombstone, not a hole.** It required `tool_denial.go:206-210`'s `ModelMessage` to name the browser surface, so that ADR criterion 3b would have a testable artefact. **Criterion 3b is withdrawn by the ADR and the requirement is unreachable:** `FilterToolsByPolicy` `continue`s past a deny verdict (`pkg/tools/compositor.go:436-438`), so a denied agent is never shown the tool, the denial path has no production caller for it, and test 10 would assert a string nothing emits. The **underlying defect is real and unfixed** — it needs a system-prompt or manifest surface, which ADR §6 owns. See §17 C3. | — | — | — | D1.12 (withdrawing MAJ-005) |
| FR-015 | The 5 model-visible "shared browser session" strings are corrected | US-1 | — | `TestToolDescriptions_NoFalseSharedClaim` | D1.11 |
| **FR-034** | The **interim** replacement literals are specified verbatim in **§3.3** and claim only tab-set sharing; they land with Stream C | US-1 | — | test 9 (stage C), asserting the new literal | MIN-005 |
| **FR-034a** | The **final** literals (§3.3), which assert cross-workspace isolation, land in the **same commit as FR-037** and not before | US-3 | — (an ordering requirement; see the exemption table below) | test 9 (stage P) + the §3.3 ordering check | round-2 MAJ-107 |
| FR-016 | Gateway resolves agent→workspace server-side; no wire field added; the two reversed descriptions use FR-016's verbatim text | US-10 | wire-meaning-change-caught | `TestGateway_SessionIDIsBinding` | ADR-043 D3 / MAJ-004 |
| **FR-016a** | The capture registry is keyed by browsing key; **one capture session per workspace browser**; ADR-048's "requesting agent" conflict rule collapses | US-2 | human-browses-first | `TestCaptureRegistry_OnePerBrowsingKey` | MAJ-007 |
| **FR-016b** | Boot warms **exactly one** instance — the resolved workspace of the default agent, one tab, one capture pipeline — never N; skipped with one INFO (not WARN) when nothing resolves | US-24 | boot-warms-one | `TestPickWarmBrowser_UsesResolvedKey`, `TestPool_BootWarmsOneInstanceNotN` | MAJ-006, D1.8 |
| **FR-016c** | Boot **preprovision** is decoupled from `BrowserManagers()`: `pool.Preprovision(ctx)` resolves/downloads the managed Chromium once at boot with **zero live keys**, replacing `gateway.go:2286`'s range over a snapshot that is empty under a lazy pool | — | boot-preprovision | `TestPool_PreprovisionAtBootWithNoLiveKeys` | round-2 MAJ-104 |
| FR-017 | Gateway prefers the attaching session's `workspace_id` | US-2, US-11 | human-browses-first | `TestGateway_PrefersSessionWorkspaceID` | round-1 C4 |
| FR-018 | Multi-workspace agent: turn and panel agree, including agreeing to refuse | US-11 | ambiguous-refused | `TestMultiWorkspaceAgent_TurnAndPanelAgree` | §6 Q2 |
| FR-019 | Write lease held for one action-tool call, **scoped to the workspace-owned (operator) tab set only** (**§14**) | US-9/AC1 | two-writers-shared-tab | `TestWriteLease_OneWriterOnSharedTab` | D2.10, **rescoped by D1.9a** |
| **FR-019a** | A `browser_*` tool takes the write lease **iff** it is gated by `controlledResult`; the exempt set is **six** (4 read-only shipped incl. `browser_list_tabs` + `browser_snapshot` + `browser_handle_dialog`); the check enumerates the **registry** and compares the two gates behaviourally | US-9/AC5 | lease-membership-follows-control-gate | `TestWriteLease_EveryActionToolIsLeased` | MAJ-008, round-2 CRIT-104/MAJ-101/MAJ-102 |
| **FR-023a** | `tools.browser.lease_wait` is **clamped** against `tools.browser.page_timeout` at config load and on reload, with a WARN naming both keys and values. **Its purpose is restated:** it bounds the *retry window* so a contended call still finishes inside its own CDP deadline (FR-020), rather than — as revision 3 had it — guaranteeing a deferral instead of an error. Under FR-020 a deferral is the **outcome past the bound**, not the goal | US-9 | lease-wait-clamped | `TestConfig_LeaseWaitClampedAgainstPageTimeout` | round-2 MAJ-112, D2.10 |
| FR-020 | The loser **retries inside the tool with backoff**, within its own deadline; **both writers eventually complete**. Only past the bound does it return a non-error `{"deferred":true,"reason":…}` naming the holder. `deferred` is retained unchanged for the human-holds-control case | US-9/AC1 | two-writers-shared-tab | `TestWriteLease_BothWritersEventuallyComplete`, `TestWriteLease_LoserDefersPastBound` | D2.10, ADR crit 16 |
| FR-021 | Read-only tools ungated. **And no tool of any class is leased when it addresses an agent's own tab set** — the lease is reached only for `TabOwnerWorkspace()` | US-9/AC0+AC4 | own-tabs-never-contend, read-only-never-deferred | `TestWriteLease_ReadOnlyToolsUngated`, `TestWriteLease_OwnTabNeverAcquires` | D2.10, D1.9a |
| FR-022 | `controlledResult` evaluated before the lease | US-9/AC2 | human-outranks-lease | `TestWriteLease_HumanControlTakesPrecedence` | ADR-038 D6 |
| FR-023 | Bounded wait before declaring contention; `leaseWaitTimeout` and its clock seam named in §14 | US-9 | two-writers | `TestWriteLease_BoundedWait` | §14 / MIN-007 |
| FR-024 | Lease always released on panic/cancel/timeout | US-9/AC3 | panic-does-not-wedge | `TestWriteLease_ReleasedOnPanicAndCancel` | D2.10 |
| FR-025 | Per-tab reaping semantics asserted, not rewritten; viewer pins the browser | US-12/AC1+AC2 | tabs-reap-viewer-pins | `TestReap_PerTabTTLAndViewerPin` | round-1 M4 |
| FR-026 | Disposal on workspace deletion / roster removal | US-12/AC3 | disposal-on-workspace-deletion | `TestDispose_OnWorkspaceDeletion` | §6 Q4/Q5 |
| **FR-026a** | The reload prune's liveness predicate is the set of **live browsing keys** — a workspace key is live while the workspace exists and has ≥1 browser-policy-allowed agent on its CoreTeam. It is **never** `registry.ListAgentIDs()` | US-17/AC1 | reload-preserves-login | `TestReload_PruneUsesBrowsingKeys` | CRIT-003 |
| **FR-026b** | Registration is idempotent per key: N agents on one workspace produce exactly one register/release pair per reload | US-17/AC2 | reload-preserves-login | `TestReload_OneCyclePerKeyNotPerAgent` | CRIT-003 |
| **FR-026c** | A delegated sub-turn creates no browser of its own, so K sub-turns return the pool and manager counts to baseline | US-12/AC5 | k-subturns-leak-nothing | `TestSubTurns_NoPoolGrowth` | CRIT-006 (as re-scoped by D1.10) |
| FR-027 | **Audit PER ACTION for write-class tools.** One event on browser-instance creation; **one event per write-class (`controlledResult`-gated) browser tool call**, carrying workspace id, agent id, tool name and target host. Read-only tools are not audited per call. **First-use-only is rejected by name in D2.11** and is what revision 3 shipped while citing that section | US-13 | audit-per-write-action | `TestAudit_EveryWriteClassCallIsRecorded`, `TestAudit_ReadOnlyCallsAreNotRecorded` | D2.11 (ruling 2026-09-01), §17 C2 |
| FR-028 | Reload preserves pid + profile + login | US-17 | reload-preserves-login | `TestReload_PreservesPIDAndLogin` | ADR-043 CRIT-002, re-mechanised |
| FR-029 | `make verify-contracts` green; prose-only schema diff | US-10/AC1 | wire-meaning-change-caught | `make verify-contracts` | Hard Constraint #8 |
| FR-030 | No new platform-conditional **behaviour**; the lease is in-process `sync`, never `fileutil.WithFlock` | US-9 | — | `TestLease_IsInProcessOnly_NoFlock` | §6 platform |
| **FR-031** | `tools.browser.capture_shared_context` is **retired**, with `Register`'s CDP-context branch, `disposeBrowserContextRaw`, `contextCount()`, `SetCaptureSharedContext`, `CaptureSharedContextEnabled` and the stale ADR-061 JPEG reference in its doc comment | US-3/AC2 | cross-workspace-isolation | `TestNoCDPBrowserContextIsEverCreated` | D1.3 / D1.4 |
| **FR-032** | An `ask`-policy tool reached from a delegated sub-turn is **auto-denied**, never queued. #659 is a prerequisite | US-5/AC3 | subturn-ask-denied | `TestSubTurn_AskPolicyIsAutoDenied` | MAJ-010 / ADR D2.9 |
| **FR-033** | Ambiguous multi-workspace resolution **refuses** for a browsing key; the WARN fires on the preferring path as well as the plain one | US-6/AC3, US-11/AC2 | ambiguous-refused | `TestResolveBrowsingKey_AmbiguousRefuses` | MAJ-011 |
| **FR-035** | Every `pool.Acquire` in a run carried a key returned by `ResolveBrowsingKey` in the same turn (behavioural, not just structural) | US-13 | audit-repudiation | `TestAcquire_KeyProvenance` | MIN-009 |
| **FR-036** | Cancelling a parent turn cancels its delegated sub-turns' in-flight browser work via the inherited `routingSessionID`; no browser is closed by the cancel (the browser belongs to the workspace, not the turn) | US-5 | — | `TestCancel_CascadesWithoutClosingBrowser` | §6 Q6 |
| **FR-037** | One Chrome process and one `--user-data-dir` profile directory per browsing key, via `pipeLaunchConfig.userDataDir`, at the **flat** path `<profileRoot>/ws-<workspaceID>/` (0700); the workspace id is validated as a single path segment before it becomes one | US-3 | cross-workspace-isolation | `TestPool_OneChromePerKey`, `TestResolveBrowsingKey_RejectsNonSegmentWorkspaceID` | D1.4, D1.8, round-2 MIN-106 |
| **FR-037a** | Per-key profiles are **flat siblings** of `cfg.ProfileDir` (`<profileRoot>/ws-<id>/`, D1.8) — **not** revision 3's nested `<profileRoot>/ws/<id>/`. Under the flat form `InstallRootForProfileDir` (`exec_resolver.go:50`) resolves a per-key path to the **same** install root as `cfg.ProfileDir`, so the nesting was the sole cause of revision 3's INVARIANT P-5. The exec path is still resolved **once** from `cfg.ProfileDir` (belt-and-braces), and test 52 asserts the arithmetic on **both** layouts so a future re-nesting fails | US-3 | — | `TestPool_InstallRootIsKeyIndependent` | round-2 MAJ-103, D1.8, §17 M7a |
| ~~FR-038~~ | **WITHDRAWN by operator ruling (ADR D1.5a) — tombstone, not a hole.** It required *"a bound on concurrently live Chromes, enforced by `pool.Acquire`"* — a soft target with a hard ceiling of `+1`, its operator ceiling supplied by `tools.browser.max_browsers`. **There is no bound on the number of live Chromes.** D1.5a rules the live-memory gate the only admission control, and `max_browsers` was never built. **Replaced by FR-057** (that gate, now the sole hard stop) and **FR-050** (eviction, now triggered by the gate rather than by a target). Its "reload-applied without a restart" clause needs no replacement: the gate reads live memory per request, so there is no value to reload. Its scenario (*pool-evicts-lru-at-target*) is re-derived as *pool-evicts-lru-under-pressure* under FR-050; `TestPool_TargetIsEnforcedByEviction` is tombstoned. | — | — | — | **D1.5a** |
| ~~FR-038a~~ | **WITHDRAWN by operator ruling (ADR D1.5a) — tombstone, not a hole.** It fixed the `<= 0` edge semantics of `tools.browser.max_browsers` and carried D1.5 item 4's rule that `max_total_tabs` stays a **global** budget across all N Chromes rather than silently becoming per-Chrome. **Both keys are deleted from the code** (FR-059), so neither edge exists: there is no ceiling to interpret and no global tab budget to keep global. Its scenario (*ceiling-edge-values*), its dataset rows and both its tests (`TestPool_ZeroCeilingStillHonoursDerivedTarget`, `TestPool_TabBudgetStaysGlobalAcrossChromes`) are tombstoned with it, **not re-derived** — §0.6. | — | — | — | **D1.5a** |
| ~~FR-039~~ | **WITHDRAWN by operator ruling (ADR D1.7) — tombstone, not a hole.** It required refusal at the cap, `errBrowserPoolFull`/`errPoolFull`, a message naming two remedies, and `BrowserResolvePoolFull`. D1.7 rules the opposite: *"there is no 'pool full' error surface and no UI change"*; at the cap the pool evicts the least recently used instance. **Replaced by FR-050** (eviction + guards), **FR-051/FR-052** (the guards' signals), **FR-053** (the bounded overshoot and its one named error) and **FR-054** (thrash). Round-2 CRIT-103's disposition, which accepted the refusal and added FR-046 to make its remedy real, is **mooted** — see §16 CRIT-103's superseding note and §17 C1. | — | — | — | D1.7 (superseding round-2 CRIT-103) |
| **FR-040** | Whole-Chrome idle close: zero tabs, zero **live** viewers (FR-052) and no call in flight past `tools.browser.idle_close_ttl` closes the process; the **profile directory survives** | US-12/AC4 | idle-close-keeps-profile | `TestPool_IdleCloseKeepsProfile` | D1.8 |
| **FR-040a** | The idle window and the reaper↔pool contract are **named**: config key `tools.browser.idle_close_ttl` (default **15m** = 3× the per-tab `idle_ttl`; §12 A22 gives the derivation), caller = the existing 1-minute sweep (`gateway.go:5321-5352`) **after** its `ReapIdleSessions` loop, post-close state = pool entry and Chrome gone, `browserMgrs` entry and `*BrowserManager` **retained**, next call relaunches from the profile. `ReapIdleSessions` cancelling `se.browserCancel` must never leave a key the pool reports live but nothing can drive | US-12/AC4a+AC6 | idle-close-relaunch, reaper-cancels-while-pool-live | `TestPool_RelaunchAfterIdleClose`, `TestReaper_CancelDoesNotStrandPoolEntry` | round-2 CRIT-102, MAJ-108 |
| **FR-041** | Crash containment: `watchForCrash` becomes **per instance**; one Chrome's death invalidates exactly one key's manager and clears exactly that key's state; no other workspace's manager is reset and no other workspace's panel drops; recovery relaunches from the profile so the login survives | US-16 | one-crash-one-workspace | `TestPool_CrashIsContained` | D1.8 |
| **FR-042** | Per-key launch lock and ownership marker (`<profileRoot>/ws-<id>/chrome.lock`, `$OMNIPUS_HOME/browser/ws-<id>.pid`) replacing the singletons at `coordinator.go:1424,1527` | US-19 | second-gateway-not-shot | `TestPool_PerKeyLockAndMarker` | D1.4, D1.8 |
| **FR-042a** | **Boot reconciliation of the N markers, discriminated by the LAUNCH LOCK and not by the marker's pid** (D1.8 — the one correction the ADR names for this FR). Before any `Acquire`, scan `$OMNIPUS_HOME/browser/ws-*.pid`: **lock acquirable + pid dead** ⇒ clear marker and lock (INFO, with a count); **lock acquirable + pid alive, identity confirmed** ⇒ orphan: terminate and clear (WARN, naming workspace and pid); **lock HELD + pid alive** ⇒ **another live gateway**: refuse to launch that key, name it, **terminate nothing**. A live Chrome pid is present in the last two cases and cannot tell them apart. Identity confirmation is `/proc/<pid>/exe`, **Linux only**: on macOS the marker is cleared without terminating and a WARN names the pid; on Windows neither flock nor `pidAlive` is real (`coordinator.go:1569-1575`) | US-19/AC1+AC2+AC2a+AC2b+AC2c | crashed-gateway-leaves-no-orphans, macos-orphan-survives-and-warns, second-gateway-not-shot | `TestPool_ReconcileMarkersAtBoot`, `TestPool_ReconcileRefusesWhenLockHeld` | round-2 MAJ-110, D1.8, D1.9 |
| **FR-042b** | `cleanStaleSingletons` runs against **each per-key profile dir** before that key's launch, not only `cfg.ProfileDir` (`coordinator.go:1235`). Without it a crash leaves a stale `SingletonLock` per profile and Chrome refuses to relaunch — which makes FR-043 false in the exact case it exists for | US-19/AC3 | idle-close-relaunch | `TestPool_StaleSingletonClearedPerKey` | round-2 MAJ-103 |
| **FR-043** | Reload survival is by **profile on disk**, replacing ADR-043 CRIT-002's context re-adoption | US-17/AC1 | reload-preserves-login | `TestReload_PreservesPIDAndLogin` | D1.4 |
| **FR-043a** | The profile directory has a **deletion** path and exactly one trigger: **workspace deletion**, after `pool.Close(k)` returns. Idle close, **eviction**, roster change, reload and crash recovery never delete — **four** negative cases now, not five: the operator-close trigger went with FR-046, and eviction replaces it as the more important one, since eviction is only acceptable because the profile survives it. Directories are created `0700`. A release-note line states the consequence | US-20 | workspace-deletion-deletes-logins | `TestPool_DeleteProfileOnWorkspaceDeletionOnly` | round-2 MAJ-111, ADR D1.8, D2.11 |
| **FR-044** | **Gate G-1 (human gate — §0.3.1), NARROWED by D1.5a: the number it existed to find is measured.** `PER_BROWSER_COST` ≈ **182 MB** — Chrome for Testing (the binary Omnipus ships), macOS, `top`'s physical-footprint column: 125 MB browser + utility, 57 MB GPU; 301 MB across 9 processes with light pages. FR-062 carries it and its scope. **What G-1 still owes** is the same figure **on Linux, with capture running**, plus the gateway's own steady-state PSS — because the shipped figure is one machine, one snapshot, macOS, and an **idle, non-capturing** instance, and a capturing browser adds the injected extension plus encoding work (unmeasured). **PSS, not RSS:** RSS charges Chrome's shared program code to every process and over-counts by 2.6× on the measured box (1118 MB RSS vs 434 MB PSS); ADR §8 records revision 3's RSS mandate as an open downstream defect against this very line. `ps` cannot produce PSS — use `smem` or `/proc/<pid>/smaps_rollup`'s `Pss:`. **`FIXED_FLOOR`, `gateway_reserve`, the derived target and the shipped ceiling all go with FR-056** — there is no cap arithmetic left for this gate to feed, only FR-062's launch-headroom minimum | US-15/AC9 | — | SC-012 (review gate) + `TestPool_LaunchHeadroomUsesMeasuredCost` (test 51, re-derived) | §0.3, §0.6, ADR §8, **D1.5a** |
| **FR-045** | **Gate G-2 (mechanical gate — §0.3.1).** Before the pool is built, a spike proves `chrome.tabCapture` succeeds for a tab in a **second Chrome's default context** with its own `--user-data-dir`. The test uses `requireBrowserOrFail`, **never** `skipIfNoBrowser`. It runs as its **own step** in the existing `browser-e2e` job — which already exports `OMNIPUS_BROWSER_E2E: "1"` (`.github/workflows/pr.yml:416`) and already fails on either skip path (`:468-472`) — with its own `-run` filter, because that job's step asserts a `>= 180` pass floor (`:481`) that a single-test invocation would trip. The log must contain exactly one `--- PASS` and neither `--- SKIP` nor `no tests to run` | US-16 | — | `TestSpike_CaptureAgainstSecondChrome` (real Chrome, **fails** without one) | §0.3, round-2 MAJ-106, §17 M10/O4 |
| ~~FR-046~~ | **WITHDRAWN by operator ruling (ADR D1.7) — tombstone, not a hole.** The REST path `POST /api/v1/workspaces/{id}/browser/close`, its SPA control, the viewer-notification frame, the idempotent 204 and the `RequireNotBypass` gating are all deleted. Its entire P0 justification was *"the only mechanism that frees a pool slot while people are working"* — a job that does not exist once eviction is automatic. **Two consequences beyond the deletion:** this removes D1's only `contracts/openapi.yaml` **path** addition, so §5's carve-out goes and **SC-007 condition (2) reverts** to *"no `contracts/` diff outside `description:`"*; and `pool.Close(k)` survives with four internal callers (idle close, eviction, workspace deletion, gateway `Close()`) — only the operator-facing surface goes. FR-047 is unaffected: it is a D2.11 obligation about a grant, not a pool control. | — | — | — | D1.7 (superseding round-2 CRIT-103) |
| **FR-047** | The Workspace → Team add-agent surface **states, before confirmation**, that adding an agent grants it every live browser session on that workspace, including on unattended turns. Traced to ADR D2.11's elevation-of-privilege decision, which §1's wording placed in this spec's scope and which no spec had claimed | US-21 | team-add-discloses-grant | `TeamAddAgent.disclosure.test.tsx` (vitest) | round-2 MAJ-114, ADR D2.11 |
| **FR-048** | **Tab ownership is explicit (D1.9a).** A `TabOwner` accompanies the browsing key at every tab operation: `TabOwnerAgent(agentID)` for an agent's own tabs, `TabOwnerWorkspace()` for the tabs the **operator** opened. The manager holds one `sessionEntry` per agent that has browsed plus at most one workspace entry; an agent never sees, lists, switches to, drives or closes another agent's tab; every agent on the workspace sees the workspace-owned set. `browser_list_tabs`' payload labels which is which | US-22/AC1+AC2+AC3, US-7/AC5 | agent-tabs-are-own-operator-tab-is-shared | `TestTabs_TwoAgentsDoNotMerge`, `TestTabs_WorkspaceOwnedSetIsVisibleToAll` | **D1.9a** |
| ~~FR-049~~ | **WITHDRAWN by operator ruling (ADR D1.5a) — tombstone, not a hole, and the REASON matters.** It gave `cfg.MaxTabs` an owner after the re-key, so a per-**agent** cap of 5 did not silently become 5 for the whole team once managers became per-workspace. That was the **right answer** — §16 M7(c) and §17 record it as a CRITICAL resolution — **to a question that no longer exists**: `tools.browser.max_tabs` is deleted from the code (FR-059), so there is no cap to own. **Read this as a resolution being dissolved, not a regression.** FR-048's per-agent tab **sets** are untouched — that is ownership, not capacity, and an agent still never sees, lists, drives or closes another agent's tab. Its scenario (*per-agent-max-tabs*), its dataset rows and `TestMaxTabs_IsPerAgentNotPerWorkspace` are tombstoned with it. | — | — | — | **D1.5a** (superseding D1.9a / §17 M7c) |
| **FR-050** | **LRU eviction under memory pressure, with two guards — RE-DERIVED: the trigger is the gate, not a target.** When FR-057's live-memory gate refuses, `Acquire` closes the **least recently used evictable** instance and **re-asks the gate**; nothing surfaces to agent or operator; the evicted profile survives and its workspace reopens signed in. **Never evict an instance with a live viewer** (FR-010, FR-052) **or with a browser tool call in flight** (FR-051). "Least recently used" is by last tool call or viewer activity. **D1.5a changes only what invokes eviction** — previously *"the pool is at its target"*, now *"the host is out of headroom"*; D1.7's two guards are unchanged, and so is the rule that the profile survives (FR-043a) | US-15/AC1+AC2+AC3+AC4 | pool-evicts-lru-under-pressure, eviction-skips-viewer, eviction-skips-inflight | `TestPool_EvictsLRUAndRelaunches`, `TestPool_EvictionSkipsViewer`, `TestPool_EvictionSkipsInFlight` | **D1.7**, D1.5a |
| **FR-051** | **`BrowserManager.InFlight()`** — a counter incremented by **every** `browser_*` tool's `Execute`, leased and lease-exempt alike, released by `defer`, consulted by eviction. The write lease cannot serve as this signal: §14's exempt set is six tools, so a `browser_screenshot` holds none. Incremented under the same `pool.mu` eviction selection holds, so a call starting during selection cannot be evicted (§3.1 locking discipline) | US-15/AC4 | eviction-skips-inflight | `TestPool_InFlightBlocksEviction`, `TestPool_EvictionRaceWithExemptCall` (`-race`) | **D1.7** |
| **FR-052** | **Viewer staleness.** A viewer whose transport has been silent past the existing WebRTC liveness window counts as **detached** for both eviction and idle close. Without it one abandoned panel pins a slot for the process's lifetime — and under eviction makes that slot permanently unreclaimable, which is a deadlock rather than a leak | US-15/AC5, US-12/AC2 | stale-viewer-unpins | `TestPool_StaleViewerDoesNotPin` | **D1.7** |
| **FR-053** | **Refusal, naming memory — REWRITTEN; the bounded overshoot it specified is deleted.** Its old form let the pool exceed a soft target by exactly one and WARN, on the reasoning that a soft cap is cheaper to break than a browse is to refuse. **There is no target to overshoot.** D1.5a rules the memory gate a **hard** stop, so when the gate refuses and nothing is evictable (every instance pinned by a live viewer or an in-flight call), the request **waits** for an instance to become evictable up to the tool call's own deadline, then **fails with a named error identifying the workspace and naming MEMORY as the binding constraint** — never a cap, because there is no cap left and an operator told otherwise would go looking for a ceiling to raise. The error carries FR-063's reason code so callers can branch on it. **The pool never launches past the gate:** overshooting real available memory invokes the OOM killer, which does not stop at the browser and can take the gateway with it, ending every session on the host | US-15/AC6+AC7 | nothing-is-evictable-then-refuse | `TestPool_NothingEvictableWaitsThenRefusesNamingMemory`, `TestPool_NeverLaunchesPastTheGate` | **D1.5a** (superseding D1.7's `target + 1`) |
| **FR-054** | **Thrash detection (gated on G-5) — RE-DERIVED, because its remedy named a key that no longer exists.** The pool counts evict-then-reopen cycles per key over a rolling window. Past the configured threshold it logs **exactly one** WARN naming the contending workspaces and **memory** as the binding constraint. **Its remedy changes:** *"raise `tools.browser.max_browsers`"* dies with the key, and the only true remedies are *give the host more memory* or *run fewer workspaces concurrently at once* — the WARN must name one of those. The target-vs-ceiling trap old SC-022 warned about is **gone with both terms**: there is exactly one binding constraint now, so the WARN cannot name the wrong one. The window and threshold are derived from cold-start latency with a warm profile (**G-5**) and are configuration until it runs — ADR-042's ~30–60 s covers a fresh install including a Chromium download and is not that number | US-15/AC8+AC8a | thrash-warns-once | `TestPool_ThrashWarnsOnce`, `TestPool_ThrashWarnNamesMemoryNotACap` | **D1.7**, D1.5a |
| ~~FR-055~~ | **WITHDRAWN by operator ruling (ADR D1.5a) — tombstone, not a hole, and its withdrawal is a SECURITY IMPROVEMENT.** It set `--renderer-process-limit=R` on every per-key Chrome with `R >= tools.browser.max_tabs` as a site-isolation **floor**. That was the **right answer** — §17 C4 records it as a CRITICAL resolution — **to a question that no longer exists**, and it is moot twice over: the key it derived from is deleted (FR-059) and the flag it configured is now **never set at all**. The flag *weakens* site isolation above its bound (over-limit navigations reuse same-site processes), justified as acceptable for *"agent-driven browsing of semi-trusted destinations"* — an adjective `ValidateURL` (`pkg/tools/browser/manager.go:685-708`) does not support: it blocks five schemes (`blockedSchemes`, `:675-683`) plus private and metadata addresses via the SSRF checker, and permits **every other public `http(s)` URL**, with no allow-list anywhere in `pkg/tools/browser/`. Not setting it retains Chrome's **default site-per-process isolation in full**, so **C-303 / C4 / C206 are DISSOLVED, not mitigated** — no residual trade-off to accept, no compensating control to remember, nothing for a future reviewer to re-litigate (§0.6, ADR P8). `TestPool_LaunchArgvCarriesRendererLimit` is **inverted** into `TestPool_LaunchArgvHasNoRendererLimit` under FR-062; `TestPool_CrossSiteTabsGetDistinctRenderers` is **kept unchanged** there, since it now asserts the stronger property. | — | — | — | **D1.5a** (superseding D1.6 / §17 C4) |
| ~~FR-056~~ | **WITHDRAWN by operator ruling (ADR D1.5a) — tombstone, not a hole.** It derived `target = clamp((min(host_RAM, cgroup_limit) × 0.5 − gateway_reserve) / (FIXED_FLOOR + R×85MB + encoder_page), 1, operator_ceiling)` and made `tools.browser.max_browsers` its ceiling. **Never built, and now unbuildable:** the ADR withdrew the 85 MB per-renderer constant on measured evidence (real renderers spanned **30 MB → 327 MB in one snapshot**, an 11× spread, and renderer count is not tab count — 2 tabs against 13 renderers, ~6 processes per tab), `R` goes with FR-055, `FIXED_FLOOR`/`gateway_reserve` go with FR-044's narrowing, and `operator_ceiling` is deleted. Capacity uses **live measurement and no constant** (FR-057, FR-062). Its scenario (*derived-target-not-a-constant*), its dataset rows and both its tests are tombstoned. **One live consequence survives it and moves to FR-057:** `availableRAMBytes` (`pkg/config/config.go:655`) and the `meminfo_linux.go` readers are **unexported**, so `pkg/tools/browser` cannot call them — that export question now blocks the only gate that remains. | — | — | — | **D1.5a** |
| **FR-057** | **The live-memory admission gate — the ONLY admission control, and a HARD stop (REWRITTEN; §0.5 E-2 is RULED).** Before any browser launch — and, per FR-060, before any tab open — the pool asks what is **actually free** and refuses to grow when the answer is short: `headroom >= PER_BROWSER_COST` for a launch (FR-062), and a pressure **ratio** with no per-tab byte constant for a tab (§0.5 E-5). On Linux under a cgroup limit the ratio is `memory.current / memory.max > 0.85`, read through the existing `readCgroupV2LimitBytes` / `readCgroupPlainUintBytes` / `readMemAvailableBytes` (`pkg/config/meminfo_linux.go`), which must first be **exported** (FR-056's surviving consequence). **The collision with FR-050 is RESOLVED, not escalated:** D1.5a rules this gate a hard stop and deletes the soft cap, so when pressure is high **and** every instance is pinned, the request is **refused** (FR-053) with an error naming **memory**. *(Revision 4 left this open as E-2 and told the implementation not to pick silently; the operator has now picked.)* **No counter stands behind this gate** — a gap in it is not a degraded limit but no limit (FR-061). **⚠ Off-Linux this gate has no signal at all — see E-6 (§0.5), which this spec cannot resolve on its own** | US-15/AC11 | pressure-gate-thresholds, nothing-is-evictable-then-refuse | `TestPool_PressureGateAt084_085_086` (fixture-driven), `TestPool_PressureGateIsHardWhenNothingEvictable`, `TestPool_PressureGateOffLinuxIsNotSilent` | **D1.5a**, D1.5 item 3 |
| **FR-057a** | **Gates G-3 and G-4 (mechanical).** Before FR-057 ships: (G-3) does Chromium read a cgroup memory limit, or size itself against host RAM inside a capped container? (G-4) does Linux memory-pressure signalling fire for Chrome at all? Both are one Chrome inside a `memory.max`-capped cgroup, reading back its own renderer limit and whether a notification arrives. **D1.5a raises the stakes on both**: G-3 negative means Chrome is sizing itself against the host while our gate reads the cgroup, and there is **no pool counter left** to compensate; G-4 negative means Chrome never self-discards under pressure, so every byte it takes is a byte our gate must have already refused. Neither can be answered in prose | — | — | receipts in the PR body (SC-019) | **D1.5**, D1.5a |
| **FR-058** | **Audit event names match `^[a-z_]+$`** — the pattern `contracts/components/schemas/AuditEntry.yaml:17` enforces. A dotted name blanks the **entire** Audit Log viewer, not just its own row (#667). The test asserts a deliberately dotted fixture name **fails**, so the check cannot pass vacuously | US-13/AC4 | audit-event-name-is-viewer-safe | `TestAudit_EventNamesMatchViewerPattern` | **D2.11**, #667 |
| **FR-043b** | **Upgrade inherits nothing.** No workspace adopts the existing global `~/.omnipus/browser/profiles/default/` (`manager.go:125`); every workspace starts with a fresh `ws-<id>` profile and is logged out. `profiles/default/` is **left on disk, untouched and unused** — deleting it would destroy logins the operator may still want and no code can tell whether they matter. A release-note line states that agents sign in again, per workspace | US-23 | upgrade-inherits-nothing | `TestPool_UpgradeInheritsNoProfile` | **D1.8**, ADR crit P11 |
| **FR-059** | **The deletion itself, as implementation scope (Stream A), not a config change.** Remove from the code: `BrowserConfig.MaxTabs` (`pkg/tools/browser/manager.go:36`, default 5 at `:124`), `ToolsBrowserConfig.MaxTabs` (`pkg/config/config.go:3633`) and its application (`pkg/agent/loop.go:2314-2315`); `max_total_tabs` (`pkg/config/config.go:3678`) with the coordinator field (`coordinator.go:128`), its ctor arg (`:226-233`), `SetMaxTotalTabs` (`:635-644`), its `ApplyRuntimeConfig` arm (`:659-660`), its gate (`:785-792`) and the threading at `loop.go:2452,2455`; the reservation machinery `reservedTabs` (`coordinator.go:137`), `TryOpenTab` (`:782-804`), `ReleaseTab` (`:806-812`), `totalOpenTabsLocked` (`:818`), `reserveGlobalTab` (`manager.go:3343-3352`), `releaseGlobalTab` (`:3358-3366`) and their call sites (`tabs.go:180,249,260`; `manager.go:3118`); the five `totalTabCountLocked() >= m.cfg.MaxTabs` checks (`manager.go:1139, 2005, 2047, 2216, 2286`) and `maxTabsReachedErr` (`:1379`); and the log lines that print the caps (`coordinator.go:250, 257`). **`totalTabCountLocked` itself survives** — FR-048's per-agent tab sets still need to be counted for listing and for FR-060's gate telemetry; only its use as an enforcement point goes. `tabAdoptReasonMaxTabs` (`manager.go:2108`) is **replaced**, not deleted — FR-063. **Test migration is in scope and in the same commit:** 59 references across 10 `_test.go` files in `pkg/tools/browser` name these keys; none may be left asserting a cap that no longer exists, and no test count may drop without the replacement named in its commit message | US-15/AC12 | counters-are-gone | `TestNoResidualTabCap` (repo-wide structural, incl. `_test.go`), `TestConfig_MaxTabsKeyIsRejected` | **D1.5a**, §0.6 |
| **FR-060** | **The pressure gate sits in the TAB-OPEN path, not only the browser-launch path.** FR-057's check runs before every tab is created, at **exactly the five sites `cfg.MaxTabs` is checked today** — `manager.go::createFirstTab` (`:1139`), `::OpenTab` (`:2005`, `:2047`) and `::adoptTarget` (`:2216`, `:2286`) — so the deletion and the replacement land at the same lines, in the same commit. **Why this is not optional:** a runaway agent looping `browser_open_tab` inside an already-running browser never reaches a launch decision, so a launch-only gate would let it run unchecked to the OOM killer, **and no counter remains to catch it** (FR-038, FR-049 are tombstoned). The tab gate is a **ratio** with no per-tab byte constant — the 85 MB constant is withdrawn and a tab has no measured floor (§0.5 E-5). A refusal here returns FR-063's reason code, not a bare failure | US-15/AC13 | runaway-tab-loop-is-stopped-by-memory | `TestManager_TabOpenChecksPressureAtAllFiveSites`, `TestManager_RunawayTabLoopIsRefusedNamingMemory` | **D1.5a**, §0.6 |
| **FR-061** | **Idle close and the pressure gate are the ENTIRE defence; neither may silently no-op.** Tab reaping already ships — `ReapIdleSessions` (`manager.go:2986`) with `DefaultIdleTTL` **5 minutes** (`manager.go:130-134`); whole-browser idle close is new work (FR-040, FR-040a). Under D1.5a both stop being housekeeping and become load-bearing: previously a counter caught a runaway before memory did, and **that backstop is gone by decision**, so **a gap in either control is not a degraded limit — it is no limit.** Consequences that are requirements, not advice: neither may ship disabled, "best effort", or behind an off-by-default flag on any supported platform; each carries a test that **fails if the control silently does nothing** (a reaper that never fires, a gate that always returns "room available"); and a platform on which either cannot run must be declared, not defaulted through (FR-057, E-6). §0.3's withdrawn carve-out applies here in full | US-15/AC14 | idle-close-actually-closes, gate-cannot-vacuously-pass | `TestReaper_FailsIfNothingIsEverClosed`, `TestPool_GateFailsIfItAlwaysAdmits`, `TestPool_IdleCloseIsNotBehindAFlag` | **D1.5a**, §0.6 |
| **FR-062** | **`PER_BROWSER_COST` ≈ 182 MB is the launch-headroom minimum, quoted with its scope — and NO per-renderer or per-tab constant is used anywhere.** The launch gate admits only when live headroom ≥ this figure. **Its scope travels with it, every time it is quoted:** one machine, one snapshot, **macOS**, `top`'s physical-footprint column, Chrome for Testing, and an **idle, non-capturing** instance — a *capturing* browser costs this plus the injected capture extension plus the encoding work, and that delta is **unmeasured** (FR-044/G-1's remaining job). **Nothing is priced per renderer or per tab:** the 85 MB Chromium constant is withdrawn on measured evidence (30 MB → 327 MB in one snapshot, an 11× spread), and renderer count is not tab count (2 tabs against 13 renderer processes — ~6 per tab), so `FIXED_FLOOR + (R × 85MB) + encoder_page` is an arithmetic this spec **may not perform**. **`--renderer-process-limit` appears nowhere in the launch flags** (FR-055's tombstone), which is what preserves full site isolation | US-15/AC9+AC9b | no-constant-in-the-capacity-path | `TestPool_LaunchHeadroomUsesMeasuredCost` (test 51, re-derived), `TestPool_LaunchArgvHasNoRendererLimit`, `TestPool_CrossSiteTabsGetDistinctRenderers`, `TestPool_NoPerRendererConstantInCapacityPath` (structural) | **D1.5a**, ADR P8, ADR "85 MB withdrawn" |
| **FR-063** | **A reason code naming MEMORY, so the model-visible message can branch.** `tabAdoptReasonMaxTabs` (`manager.go:2108`, `"max_tabs_reached"`) is **replaced** by `tabAdoptReasonMemoryPressure` (`"memory_pressure"`), returned at the two `adoptTarget` refusal points (`manager.go:2223`, `:2287`) and by FR-060's other three sites, and `applyReconcileOutcome`'s switch (`pkg/tools/browser/tools.go:346-356`) gains the matching arm. **Why this is a requirement and not a rename:** today the `max_tabs` arm produces an actionable sentence — *"the maximum concurrent tabs limit was reached… Close a tab with `browser_close_tab` and retry"* — and deleting the cap without a replacement code drops every memory refusal into the `default:` arm, *"it could not be adopted"*, **with no reason and no remedy**. An agent that cannot tell "the host is out of memory" from "something went wrong" **retries**, which is precisely the runaway this ruling accepts the risk of. The memory arm must state the cause and a remedy that exists (*close tabs or browsers you are done with, or wait — the host is out of memory*), and must **not** name a limit or a config key, because none exists. FR-053's launch-refusal error carries the same code | US-15/AC15 | memory-refusal-is-legible-to-the-agent | `TestTools_MemoryRefusalMessageNamesMemoryAndARemedy`, `TestTools_NoAdoptRefusalFallsToDefaultArm` | **D1.5a**; defect found by the D2 spec |

**Withdrawn rows are kept, not renumbered,** so that a reader arriving from an earlier review can see that a requirement was deleted by ruling rather than lost in an edit. They carry no design content. **Ten tombstones now:** FR-011 and FR-012 (D1.10's sharing ruling), **FR-014a** (D1.12 withdrew criterion 3b as unreachable — §17 C3), **FR-039 + FR-046** (D1.7 withdrew the refusal and the operator close — §17 C1/M1), and **FR-038, FR-038a, FR-049, FR-055, FR-056** (D1.5a deleted every counter — §0.6). Other documents cite these numbers; **none is reused, and none is renumbered.** Two of the five D1.5a tombstones (FR-049, FR-055) were recorded by earlier reviews as CRITICAL *resolutions*, so each states **why** it is dissolved rather than reverted — a silent drop would read as a regression to anyone arriving from §16 M7(c) or §17 C4.

**Traceability completeness (the round-1 structural PARTIALs closed; round-2's re-checked and its two remaining PARTIALs closed here).** Every US has ≥1 BDD scenario; every AC in §7 is reachable from one; every BDD scenario names its US/AC and FRs inline and has a §10 row. **Thirteen FRs deliberately carry no BDD scenario**, and each is a structural, build-time, ordering or measurement requirement rather than an observable behaviour — a Given/When/Then for them would be theatre. Two that were on this list are now off it, because the round-2 revision gave each a check that can fail:

| FR | Why no BDD | How it is verified instead |
|---|---|---|
| FR-002d | A doc comment | Test 9-style doc assertion (`TestLoop_BrowserMgrsCommentIsCurrent`) |
| FR-002e | A build-time migration, not a behaviour | Test 5 (repo-wide, tests included) + §10.1's diff bar |
| FR-016b | Boot-time best-effort warm-up, invisible to any user-facing flow | Test 24 |
| FR-029 | A build gate | `make verify-contracts` |
| FR-030 | A structural import assertion | Test 19 |
| FR-034a | A commit-ordering requirement, not an observable state | Test 9 (stage P) + the §3.3 ordering check, which a reviewer performs |
| FR-035 | A whole-run provenance invariant, not a single interaction | Test 34 |
| FR-036 | Cancellation cascade, covered as a unit-level property | Test 36 |
| FR-037a | Path arithmetic | Test 52 |
| FR-042 | Path construction | Test 27 |
| FR-044 | A measurement (gate G-1) — a **human** gate, §0.3.1 | Recorded in the PR body (SC-012); test 51 (`TestPool_LaunchHeadroomUsesMeasuredCost`) catches the round-number shape and the missing-scope shape only |
| FR-043b | An upgrade-time one-shot with no user-facing flow of its own | BDD *upgrade-inherits-nothing* + test 69 |
| FR-057a | Two measurements (gates G-3, G-4) — mechanical, but their artefact is a receipt, not an assertion in this repo | Receipts in the PR body (SC-019) |

**FR-019a is no longer on this list.** The previous draft exempted it as "a structural rule over the registry", which was the honest description of a test that could only check membership of a hand-written list. It now has a real BDD scenario (*lease membership follows the control-lock gate*) because the rule became behavioural: exercise each registered tool under a held control lock and under a held write lease, and assert the two answers agree. **FR-045 is also off this list** — §0.3.1 makes it a mechanical gate with a failing check, so it has a test that can fail rather than a note in a PR body.

**The five FRs D1.5a adds all have BDD scenarios and tests, so none joins this list** — FR-059 (*counters-are-gone*, test 78), FR-060 (*runaway-tab-loop-is-stopped-by-memory*, test 79), FR-061 (*idle-close-actually-closes* / *gate-cannot-vacuously-pass*, test 80), FR-062 (*no-constant-in-the-capacity-path*, tests 51/74), FR-063 (*memory-refusal-is-legible-to-the-agent*, test 81). **The five it tombstones leave rows with `—` in every column**, matching FR-039's and FR-046's shape: a tombstone is not an exemption, and a reader must be able to tell "deleted by ruling" from "not verifiable".

Everything else in §9 has all four of US, BDD, test and source.

---

## 10. TDD plan (ordered; Unit → Integration → E2E)

| # | Test | Level | Traces to | Notes |
|---|---|---|---|---|
| 1 | `TestResolveBrowsingKey_Ladder` | Unit | FR-007 | Table-driven over three rungs. **Write first** |
| 2 | `TestResolveBrowsingKey_NoWorkspaceFailsByName` | Unit | FR-008 | `errors.Is(err, ErrNoBrowsingContext)` **and** key `IsZero()` |
| 3 | `TestResolveBrowsingKey_AmbiguousRefuses` | Unit | FR-033 | Two candidate workspaces, no preference → refusal, both named, WARN on both paths |
| 4 | `TestRegisterTools_NoBoundManagerField` | Unit (structural) | FR-002a | **Predicate stated precisely (round-2 MAJ-105):** reflects over every type in `pkg/tools/browser` **that implements `tools.Tool`**; fails on any `*BrowserManager` field. Not "every exported struct" — `CaptureSession` (`capture_session.go:258`), `LiveViewRegistry` (`live.go:322`), `LiveView` (`live.go:1324`) and `BrowserCoordinator.managers` (`coordinator.go:186`) legitimately hold one and must **not** be "fixed". **Red today** (all eleven tool structs hold `mgr`) |
| 4a | `TestBrowserBuiltinMetadata_ConstructsAllEleven` | Unit (compile-level) | FR-002a | `metadata.go:36-51` builds all eleven tool structs a **second** time with a nil manager. Absent from the previous draft; a compile-level assertion that the metadata list and the registry list have the same names and length |
| 5 | `TestNoResidualDefaultSessionID` | Unit (structural) | FR-002b, FR-002e | **Repository-wide, INCLUDING `_test.go` files:** zero references to `DefaultSessionID`/`defaultSessionID`, with one allowed exception (the migration helper's doc comment). Baseline today: **57 non-test hits (37 executable + 2 declarations + 18 comments) and 364 test-side references across 25 files** (§2.2a). Counting only the non-test side would let a test-only alias keep the constant alive and SC-013 reading zero |
| 6 | `TestControlledResult_UsesResolvedKey` | Unit | FR-002c | Control lock taken under `ws:W`; `controlledResult` must see it. **Red today** (it asks `IsControlled("default")`) |
| 7 | `TestLoop_BrowserManagerForKey_OnePerKey` | Unit | FR-001 | Concurrent callers for one key get one manager; different keys, different managers |
| 8 | `TestListTabsState_ThreeDistinctStates` | Unit | FR-013 | All three states constructed directly; pairwise-distinct payloads; state set is exactly `{no_context, open, empty}` — **no fourth, and no "denied" member** (D1.12) |
| 9 | `TestToolDescriptions_NoFalseSharedClaim` | Unit | FR-015, FR-034 | Asserts the old phrase is gone **and** the new literal is present, verbatim from FR-034 |
| ~~10~~ | ~~`TestToolDenial_BrowserSurfaceIsNamed`~~ | — | ~~FR-014a~~ | **DELETED with FR-014a (§17 C3).** It would assert a `ModelMessage` string that has **no production caller**: `FilterToolsByPolicy` `continue`s past a deny verdict (`compositor.go:436-438`), so the tool is never sent to the model and the denial path is never reached for it. A test asserting a string nothing emits is a green that means nothing |
| 11 | `TestListTabs_DeniedAgentNeverReachesTool` | Unit | FR-014 | Policy-filtered registry; **the tool is absent from the definitions** and `Execute` is not entered. Asserts the absence, not a message |
| 12–17 | `TestWriteLease_*` (OneWriterOnSharedTab, **BothWritersEventuallyComplete**, LoserDefersPastBound, ReadOnlyToolsUngated, HumanControlTakesPrecedence, ReleasedOnPanicAndCancel, BoundedWait) | Unit | FR-019…FR-024 | Per **§14**; fake clock via §14's named seam. **`BothWritersEventuallyComplete` replaces `LoserGetsDeferredNotError` as the primary assertion** — ADR criterion 16 states that asserting only "neither errors" would pass when nothing happened |
| 18 | `TestWriteLease_EveryActionToolIsLeased` | Unit (behavioural, registry-enumerated) | FR-019a | **Not a list check.** Enumerates every registered `browser_*` tool and exercises each **twice** — once with a human holding the control lock, once with another agent holding the write lease — asserting the two deferral answers **agree** for every tool. That biconditional is §14 rule 3's rule and it is checkable against shipped code, unlike "does it mutate", which no test can evaluate. Must include `browser_list_tabs` in the never-defers set (round-2 CRIT-104) |
| 19 | `TestLease_IsInProcessOnly_NoFlock` | Unit (structural) | FR-030 | `lease.go` imports no `fileutil`/`unix` locking |
| 20 | `TestManager_Viewers_ReflectsAttachDetach` | Unit | FR-010 | The accessor the reaper and idle-close consume |
| 21 | `TestGateway_ResolveOutcomes_AreDistinct` | Unit | FR-008a | **Three** `BrowserResolveOutcome` values produce different operator-facing reasons. *(Revision 4's row said "all four", which was stale: `BrowserResolvePoolFull` was deleted with FR-039 — §3.1's interface comment says so — and this row was not swept. D1.5a brings a refusal back at FR-053, but it is a **tool-call error naming memory**, not a resolve outcome; the enum stays at three and `contracts/` gains nothing — SC-007 condition (2) depends on that.)* |
| 22 | `TestGateway_PrefersSessionWorkspaceID` | Unit | FR-017 | Session meta `workspace_id=B` beats the plain ladder |
| 23 | `TestMultiWorkspaceAgent_TurnAndPanelAgree` | Unit | FR-018 | Including agreeing to refuse |
| 24 | `TestPickWarmBrowser_UsesResolvedKey` | Unit | FR-016b | Warmed session id equals the resolved key; no-workspace ⇒ skipped with INFO |
| 25 | `TestCaptureRegistry_OnePerBrowsingKey` | Unit | FR-016a | Two agents, one workspace ⇒ one capture session |
| ~~26~~ | ~~`TestPool_CapIsEnforced` / `TestPool_RefusesNeverEvicts`~~ → **`TestPool_EvictsLRUAndRelaunches`** | Unit (fake launcher) | FR-038, **FR-050** | **`TestPool_RefusesNeverEvicts` is DELETED, not renamed** — it asserts the exact behaviour D1.7 forbids, and a rename would carry the old assertion forward under a new label. The replacement asserts the LRU instance is closed, the new one launches, **no error is returned**, and the evicted profile survives. Uses the existing injectable `pipeLauncher` seam (`coordinator.go:149`) — no real Chrome |
| 27 | `TestPool_PerKeyLockAndMarker` | Unit | FR-042 | Distinct lock and marker paths per key |
| 28 | `TestPool_CrashIsContained` | Unit (fake launcher) | FR-041 | Kill one instance; assert the other's manager was not reset |
| 29 | `TestSubTurn_UsesWorkspaceBrowser` | Unit | FR-009 | Sub-turn ctx resolves to `ws:W`; no second manager |
| 30 | `TestSubTurn_AskPolicyIsAutoDenied` | Unit | FR-032 | Denied, not queued; no approval request created |
| 31 | `TestSubTurns_NoPoolGrowth` | Unit | FR-026c | K sub-turns; counts return to baseline |
| 32 | `TestReload_PruneUsesBrowsingKeys` | Unit | FR-026a | Prune with a workspace-keyed map disposes **nothing**. **Red today** |
| 33 | `TestReload_OneCyclePerKeyNotPerAgent` | Unit | FR-026b | **Two agents on one workspace**; exactly one register/release pair |
| 34 | `TestAcquire_KeyProvenance` | Unit | FR-035 | Every `Acquire` key traced to a `ResolveBrowsingKey` return in the same turn |
| 35 | `TestNoCDPBrowserContextIsEverCreated` | Unit (structural + behavioural) | FR-031 | No call to `target.CreateBrowserContext`/`WithNewBrowserContext` anywhere non-test |
| 36 | `TestCancel_CascadesWithoutClosingBrowser` | Unit | FR-036 | Parent cancel stops the sub-turn's work; `LiveKeys()` unchanged |
| 37 | **`TestSpike_CaptureAgainstSecondChrome`** | **Spike (real Chrome, MUST run)** | FR-045 | **Gate G-2. Run before Stream P starts.** Two Chromes, distinct `--user-data-dir`s, extension loaded in each, capture a tab in the second. **Uses `requireBrowserOrFail`, NOT `skipIfNoBrowser`** — the latter skips in CI without `OMNIPUS_BROWSER_E2E=1` (`browser_e2e_test.go:66-68`) **and** skips when no Chrome probes successfully (the probe ladder at `:69-110` falling through to `resolveTestBinary(t)`'s own `t.Skipf` at `:111`), and both skips report green. Gate job sets `OMNIPUS_BROWSER_E2E=1`; receipt captured without a pipe; log asserted to contain no `--- SKIP` and no `no tests to run` (§0.3.1) |
| 38 | `TestHandover_ThroughRealRegistrationPath` | Integration (real Chrome) | FR-002a, FR-006 | **The headline defect.** Two agents registered via `registerSharedTools`, one workspace, one tab opened by A and listed by B. **Red today** |
| 39 | `TestBrowsingContext_CrossWorkspaceIsolation` | Integration (real Chrome) | FR-003, FR-004, FR-037 | Cookie set in X, absent in Y; distinct pids and profile dirs |
| 40 | `TestBrowsingContext_NewChatSameWorkspaceSamePID` | Integration | FR-005 | What the workspace axis buys over the conversation axis |
| 41 | `TestPool_IdleCloseKeepsProfile` | Integration | FR-040 | Process gone, profile dir present, login survives the next launch |
| 42 | `TestReload_PreservesPIDAndLogin` | Integration | FR-028, FR-043 | Two agents on W; pid unchanged; `pool.Close` called **zero** times |
| 43 | `TestReap_PerTabTTLAndViewerPin` | Integration | FR-025 | Existing semantics survive the re-key |
| 44 | `TestDispose_OnWorkspaceDeletion` | Integration | FR-026 | Counts `pool.Close` calls |
| 45 | `TestAudit_EveryWriteClassCallIsRecorded` | Integration | FR-027 | **Renamed and re-scoped with FR-027 (§17 C2).** Ten write-class calls ⇒ ten events, each with workspace/agent/tool/host, **including the tenth** — the assertion first-use-only auditing fails. Five read-only calls ⇒ zero per-call events. `TestAudit_CreateAndFirstCrossAgentUse` is **not renamed to this** — it asserted a behaviour D2.11 rejects by name, so it is deleted and replaced |
| 46 | `TestGateway_SessionIDIsBinding` | Integration | FR-016 | Attach frame's `session_id` selects the workspace — the assertion that makes the contract gate falsifiable |
| 47 | `TestWriteLease_TwoAgentsRealChrome` | E2E | FR-019, FR-020 | Real navigations, no interleaved DOM |
| 48 | `TestPool_CrashContainment_RealChrome` | E2E | FR-041 | Kill one Chrome; the other workspace's panel keeps streaming |
| 49 | `make verify-contracts` | Build | FR-029 | Exit 0 |
| 50 | `TestConfig_LeaseWaitClampedAgainstPageTimeout` | Unit | FR-023a | `lease_wait=45s` + `page_timeout=30s` → clamped, WARN naming both keys, at load **and** on reload. Asserts the WARN, not only the value — a silent clamp is a config the operator thinks they set |
| 51 | ~~`TestConfig_MaxBrowsersCeilingIsNotZeroOrRound`~~ → **`TestPool_LaunchHeadroomUsesMeasuredCost`** | Unit | FR-044, FR-062 | **Re-derived, not deleted.** The old test guarded a shipped **ceiling** against the shapes a guess takes (0, 5, 10, 100) and forbade a literal target in `pkg/config/defaults.go`. **There is no ceiling and no target** (D1.5a). Its discipline moves to the figure that survives: the launch gate compares live headroom against `PER_BROWSER_COST` ≈182 MB; the test fails if that figure is a round guess, if any literal **browser count** appears in `pkg/config/defaults.go`, or if the constant's doc comment omits its scope (macOS, one snapshot, Chrome for Testing, **idle and non-capturing**). **Still does not prove a measurement happened** (§0.3.1); SC-012 remains the real gate |
| 52 | `TestPool_InstallRootIsKeyIndependent` | Unit | FR-037a | Table-driven over **both** layouts: `…/profiles/default` and the flat `…/profiles/ws-<id>` both resolve to `…/browser/chromium`; the nested `…/profiles/ws/<id>` resolves to `…/profiles/chromium` and the test asserts that form is **never** constructed. Plus: `InstallRootForProfileDir` is called with `cfg.ProfileDir` exactly once, and N keys resolve to one exec path |
| ~~53~~ | ~~`TestPool_ZeroCeilingStillHonoursDerivedTarget` / `TestPool_TabBudgetStaysGlobalAcrossChromes`~~ | — | ~~FR-038a, FR-056~~ | **BOTH DELETED by ADR D1.5a — tombstone, not a hole.** The first asserted `max_browsers` 0/−1 removes the *ceiling* while the derived target still binds; the second, that `max_total_tabs=3` stays 3 across two Chromes rather than 3 each. **Both keys are deleted from the code** (FR-059), so each test would have to construct configuration that no longer parses. **Not re-derived:** they were assertions *about a cap's edges*, and there is no cap. §17 M7b's finding is dissolved with them |
| 54 | `TestPool_RelaunchAfterIdleClose` | Integration | FR-040a | Idle-close, then a tool call: relaunch from the profile, login intact, `LiveKeys()` +1, **same** `*BrowserManager`, no re-registration |
| 55 | `TestReaper_CancelDoesNotStrandPoolEntry` | Unit | FR-040a, CRIT-102 | Drive `ReapIdleSessions` into its all-tabs-idle branch so `se.browserCancel` is cancelled while the pool entry is live; the next `Acquire` must yield a drivable browser |
| 56 | `TestPool_ReconcileMarkersAtBoot` | Unit | FR-042a | 3 markers (2 dead pids, 1 live): all removed, stale locks cleared, live one terminated, INFO + WARN emitted, `LiveKeys()` = 0 |
| 57 | `TestPool_StaleSingletonClearedPerKey` | Unit | FR-042b | A `SingletonLock` planted in `<profileRoot>/ws/W/` is removed before W's launch; one planted in `cfg.ProfileDir` does **not** satisfy the assertion |
| 58 | `TestPool_DeleteProfileOnWorkspaceDeletionOnly` | Integration | FR-043a | Profile removed on workspace deletion (after `Close` returns); **present** after idle close, **eviction**, roster change, reload and crash recovery — **four** negative cases, because the positive one alone would pass a "delete always" bug. *(Revision 3 had five; the operator-close case went with FR-046 and eviction takes its place — the more important of the two, since eviction is only acceptable because the profile survives it.)* |
| ~~59~~ | ~~`TestGateway_CloseWorkspaceBrowser` / `_Idempotent`~~ | — | ~~FR-046~~ | **DELETED with FR-046 (D1.7).** The REST path it exercises is withdrawn |
| 60 | ~~`TestPool_RefusalRemedyIsEffective`~~ → ~~`TestPool_OvershootIsExactlyOneTotal`~~ → **`TestPool_NothingEvictableWaitsThenRefusesNamingMemory`** | Integration | FR-053, FR-057, FR-063 | **Replaced a second time, by ADR D1.5a.** Round-2 replaced `TestPool_RefusalRemedyIsEffective` with an overshoot assertion when D1.7 removed the refusal; **D1.5a now removes the overshoot and brings the refusal back — but keyed to memory, not to a cap.** With the gate refusing and **every** instance pinned by a live viewer **and** an in-flight call: the call waits to its own deadline, then fails with an error naming the workspace and naming **memory**, carrying the `memory_pressure` reason code; `LiveKeys()` is **unchanged** — no instance is started past the gate, not even one. The test asserts the message names **no limit and no config key**. The pinned-everywhere setup is retained through all three revisions because it is the only state in which the refusal path is reachable at all |
| 61 | `TestPool_PreprovisionAtBootWithNoLiveKeys` | Unit | FR-016c | Resolution/download starts at boot with `len(LiveKeys()) == 0` and no `*BrowserManager` in existence |
| 62 | `TestResolveBrowsingKey_RejectsNonSegmentWorkspaceID` | Unit | FR-037 | `../`, `a/b`, `.`, `..` and an empty id are refused as `ErrNoBrowsingContext` before any path is built |
| 63 | `TestPool_ConcurrentAcquireUnderPressure` | Unit (fake launcher, `-race`) | FR-050, FR-053, FR-057 | **Re-derived: the boundary is the gate, not a target.** *(a) evictable path:* two goroutines `Acquire` **different** keys while the gate refuses and the LRU is idle — **both succeed**, the LRU is evicted exactly once, and **the gate is re-asked between them**, so the second never launches on a stale reading. *(b) all-pinned path:* the same with every instance pinned — **neither grows the pool**, both wait to their deadlines, and both are refused naming memory; `LiveKeys()` is unchanged. *(Revision 4's path (b) asserted `LiveKeys()` reaches exactly `target+1` with one WARN and **no refusal**. D1.5a deletes the `+1` overshoot and rules the gate a hard stop, so that assertion is now the inverse of correct — this is the second time this row's boundary assertion has had to be inverted by a ruling, which is why it names the gate rather than a number.)* |
| 65 | `TestTabs_TwoAgentsDoNotMerge` / `TestTabs_WorkspaceOwnedSetIsVisibleToAll` | Unit | FR-048 | Two agents on one manager: two distinct `sessionEntry` values; neither's `tabs` slice contains the other's `tabEntry`; the workspace-owned entry appears in both agents' results, labelled. **Red today** in the sense that matters: written against a re-keyed manager with no `TabOwner`, it fails, which is the point — it is the guard against FR-001 silently deleting the separation |
| ~~66~~ | ~~`TestMaxTabs_IsPerAgentNotPerWorkspace`~~ | — | ~~FR-049~~ | **DELETED by ADR D1.5a — tombstone, not a hole.** It drove `max_tabs=5` and asserted A's sixth tab is refused while B still opens five of its own. **`tools.browser.max_tabs` is deleted** (FR-059), so there is no cap to be per-agent. **The property it protected is not lost** — that tab *sets* stay per agent is test 65 (`TestTabs_TwoAgentsDoNotMerge`), which is about ownership and is untouched. Only the *capacity* half goes |
| 67 | `TestPool_EvictionSkipsViewer` / `TestPool_EvictionSkipsInFlight` / `TestPool_StaleViewerDoesNotPin` | Unit (fake launcher) | FR-050, FR-051, FR-052 | **The guards exercised where they can fail.** LRU has a live viewer ⇒ the **second**-LRU is evicted. LRU has a lease-**exempt** call in flight ⇒ second-LRU evicted, the call completes. A viewer silent past the liveness window ⇒ counted detached by both eviction and `CloseIdle`. *(Driving the guards only all-pinned cannot distinguish "the guard works" from "nothing was evictable".)* |
| 68 | `TestPool_EvictionRaceWithExemptCall` | Unit (fake launcher, `-race`) | FR-051 | A long lease-exempt read on the LRU instance, started concurrently with an `Acquire` of a new key at the target: the read completes and its instance is not closed. Exempt deliberately — the leased case is the one the lease would have covered anyway |
| 69 | `TestPool_UpgradeInheritsNoProfile` | Integration | FR-043b | A populated `profiles/default/` with a live cookie: two workspaces both come up **logged out**, neither `ws-<id>` profile is a copy of it, and `profiles/default/` is unmodified afterwards |
| 70 | `TestPool_BootWarmsOneInstanceNotN` | Unit | FR-016b | Four workspaces, warm defaults on: exactly one `LiveKeys()` entry and one capture pipeline; no resolvable default-agent workspace ⇒ zero and one INFO |
| 71 | `TestPool_ThrashWarnsOnce` + **`TestPool_ThrashWarnNamesMemoryNotACap`** | Unit (fake launcher, fake clock) | FR-054 | `2 × threshold` evict-reopen cycles inside one window ⇒ **exactly one** WARN, carrying the contending workspace ids, **memory** as the binding constraint, and a remedy string. The second test asserts the **absence** of any config-key name in that string — revision 4's remedy was *"raise `tools.browser.max_browsers`"*, and a WARN naming a deleted key is the exact defect SC-022 exists to catch, which prose review has already missed twice (round-2 CRIT-103, round-3 M-309) |
| 72 | `TestPool_PressureGateAt084_085_086` / **`TestPool_PressureGateIsHardWhenNothingEvictable`** / **`TestPool_PressureGateOffLinuxIsNotSilent`** | Unit (fixture-driven) | FR-057, FR-053, FR-061 | Fixture cgroup values at 0.84 / 0.85 / 0.86 across the boundary. **The pinned-and-pressured case is now ASSERTED** — refuse, naming memory — where revision 4 deliberately left it out because "refuse to grow" (D1.5 item 3) and "always evict-and-launch" (D1.7) gave opposite answers and a test picking one would have ratified a decision nobody made. **D1.5a rules it** (§0.5 E-2, now struck through), so the case that was unassertable is the one that now matters most. **The third test replaces `TestPool_PressureGateIsNoOpOffLinux`, and inverts it:** off Linux `availableRAMBytes` is **0** (`pkg/config/meminfo_other.go`), so a "no-op" gate is not a degraded limit but **no limit** (FR-061) — the test **fails** rather than passing quietly, and **stays red until §0.5 E-6 is ruled** |
| ~~73~~ | ~~`TestPool_TargetIsDerivedFromMemoryFixtures` / `TestPool_CeilingClampsDerivedTarget`~~ | — | ~~FR-056~~ | **BOTH DELETED by ADR D1.5a — tombstone, not a hole.** They exercised `clamp((min(host_RAM, cgroup_limit) × 0.5 − gateway_reserve) / (FIXED_FLOOR + R×85MB + encoder_page), 1, operator_ceiling)` against fixture hosts. **Every term is gone or withdrawn** — `R` with FR-055, the 85 MB constant on measured evidence (30 MB → 327 MB, 11× in one snapshot), `FIXED_FLOOR`/`gateway_reserve` with FR-044's narrowing, `operator_ceiling` with FR-056 — and there is no target to derive. **Their fixture pattern survives** in test 51 and test 72, which read the same `meminfo_*_test.go` fixtures for the one check that remains |
| 74 | ~~`TestPool_LaunchArgvCarriesRendererLimit`~~ → **`TestPool_LaunchArgvHasNoRendererLimit`** / `TestPool_CrossSiteTabsGetDistinctRenderers` | Unit + Integration (real Chrome) | FR-062 | **The first test is INVERTED, not deleted:** every per-key launch argv must contain **no** `--renderer-process-limit` at any value. **The second is kept unchanged and now asserts something stronger** — two cross-site tabs in one workspace occupy distinct renderer processes (ADR criterion P8) for *every* tab rather than only those below a bound, because Chrome's default site-per-process isolation is retained in full. *This pair is the mechanical record that removing the flag was a security **improvement**: C-303 / C4 / C206 are dissolved, not mitigated (FR-055's tombstone).* |
| 75 | `TestAudit_EveryWriteClassCallIsRecorded` / `TestAudit_ReadOnlyCallsAreNotRecorded` / `TestAudit_EventNamesMatchViewerPattern` | Integration + Unit | FR-027, FR-058 | Ten write-class calls ⇒ ten events with workspace/agent/tool/host, **including the tenth**; five read-only calls ⇒ zero per-call events; every event name matches `^[a-z_]+$` and a dotted fixture name **fails** the assertion |
| 76 | `TestPool_ReconcileRefusesWhenLockHeld` | Unit | FR-042a | Marker pid alive **and** launch lock held ⇒ refuse to launch that key, name the other gateway, terminate nothing. The test that distinguishes "reconcile orphans" from "kill the neighbour" (ADR criterion P9) |
| 77 | ~~`TestConfig_MaxBrowsersDocSaysSoftTarget`~~ → **`TestConfig_NoBrowserCountKeyExists`** | Unit (doc assertion) | FR-059 | **Re-derived.** The old test required `max_browsers`' doc comment to admit it was a *soft* target with a `+1` hard bound — ADR criterion P14's rule that a field described as a hard limit which silently overshoots is its own defect. D1.5a satisfies P14 by the shortest route: **the field is gone, so it cannot lie.** The replacement asserts the absence — no `max_browsers`, no `max_tabs`, no `max_total_tabs` in `ToolsBrowserConfig`, and no doc string anywhere claiming a browser or tab bound exists |
| 78 | `TestNoResidualTabCap` / `TestConfig_MaxTabsKeyIsRejected` | Unit (repo-wide structural) | FR-059 | Repo-wide, **including `_test.go`**: zero hits for `MaxTabs`, `MaxTotalTabs`, `TryOpenTab`, `ReleaseTab`, `reservedTabs`, `reserveGlobalTab`, `releaseGlobalTab`, `maxTabsReachedErr`. A `config.json` carrying `tools.browser.max_tabs` or `max_total_tabs` is **rejected at load with a named error**, never silently ignored — a key that loads and does nothing tells an operator they have a limit they do not have. `totalTabCountLocked` must **still exist** (counting for FR-048's tab sets and the gate's telemetry), so the search is by symbol, not by substring |
| 79 | `TestManager_TabOpenChecksPressureAtAllFiveSites` / `TestManager_RunawayTabLoopIsRefusedNamingMemory` | Unit + Integration | FR-060, FR-063 | The gate is consulted at **all five** tab-open sites — `createFirstTab` (`manager.go:1139`), `OpenTab` (`:2005`, `:2047`), `adoptTarget` (`:2216`, `:2286`) — the same five the deleted cap was checked at. A loop opening tabs inside one already-running browser is **refused at tab *k+1*** once the fixture reading crosses the threshold, with the `memory_pressure` reason code. *This is the only thing standing between that loop and the OOM killer: it never reaches a launch decision, and no counter remains* |
| 80 | `TestReaper_FailsIfNothingIsEverClosed` / `TestPool_GateFailsIfItAlwaysAdmits` / `TestPool_IdleCloseIsNotBehindAFlag` | Unit | FR-061 | **Each fails if its control silently does nothing.** A sweep in which nothing is ever closed fails; a gate that answers "room available" for every fixture input fails; neither control is reachable behind a disabled flag, a "best effort" path or an off-by-default setting on a supported platform. *These exist because "the control ran and did nothing" is the exact false-green shape `docs/internal/false-green-patterns.md` catalogues, and under D1.5a a gap in either is not a weaker limit — it is no limit* |
| 81 | `TestTools_MemoryRefusalMessageNamesMemoryAndARemedy` / `TestTools_NoAdoptRefusalFallsToDefaultArm` | Unit | FR-063 | `applyReconcileOutcome` (`pkg/tools/browser/tools.go:346-356`) has a `memory_pressure` arm; its text names the host being out of memory **and** a remedy that exists, and names **no limit and no config key**. The second test asserts **no** adoption refusal reaches the `default:` arm — *"it could not be adopted"*, no reason, no remedy — which is where every memory refusal lands if `tabAdoptReasonMaxTabs` (`manager.go:2108`) is removed without a replacement. *An agent that cannot tell "out of memory" from "something went wrong" retries.* Defect found by the D2 spec |
| 64 | `TeamAddAgent.disclosure.test.tsx` | Unit (vitest) | FR-047 | The disclosure renders before the confirm action, not after it |

### 10.1 Regression requirements (MANDATORY — this change modifies shipped behaviour)

**Must keep passing, with SEMANTICS unmodified — read this bar before reading the list.**

The previous draft said "unmodified" and that was **unsatisfiable, at compile level** (round-2 CRIT-101). FR-002b **deletes** `DefaultSessionID` and `defaultSessionID`; four of the files below reference them **115 times** (`tab_adoption_e2e_test.go` 41, `idle_reaper_test.go` 33, `reaper_edge_test.go` 26, `reaper_lifecycle_test.go` 15), and repo-wide the test surface is **364 references across 25 files** (§2.2a). Those files do not fail an assertion when the constant goes — they fail to build, and take their whole package's test binary with them. "Keep passing unmodified" and "the constant is deleted" could not both hold, and the only way to honour both would have been a test-only alias, which re-creates the exact `"default"` constant SC-013 counts to zero and leaves the reaper suite asserting against a key nothing in production uses.

**The bar, therefore, is behavioural rather than textual.** For every file in this list:

1. The **only** permitted edit is mechanically re-pointing the session-id argument at a browsing key built through `newTestBrowsingKey`. No other line changes.
2. **No assertion is weakened, deleted, inverted, or guarded by `t.Skip`/`t.Short`.** Assertion count per file is unchanged or higher.
3. Test-function count per file is unchanged or higher.
4. The migration lands in the **same commit** as FR-002b (§2.2a item 4) — a commit that deletes the constant and defers the tests leaves `pkg/tools/browser` and `pkg/gateway` unbuildable, at which point every other gate's verdict is meaningless.
5. The reviewer checks the diff against 1–3 directly. A diff that touches anything else in these files is a finding, not a judgement call.

**Files (semantics unmodified):**

- `pkg/tools/browser/tab_adoption_e2e_test.go` — all **nine** ADR-041 tab tests (`:77`–`:569`). Tab-set semantics inside one browser are untouched. **41 references to migrate.**
- `pkg/tools/browser/shared_control_test.go` — **eight** tests (`:35, :55, :74, :92, :109, :138, :157, :186`), not nine as the previous draft claimed. **Correction that matters more than the count:** *this file is not the FR-022 guard.* None of its eight tests calls `controlledResult`; they exercise `LiveView` input dispatch, rate limiting and viewport failure streaks. A green result here proves nothing about the human control lock surviving the re-key.
- **`pkg/tools/browser/tools_control_test.go` — the actual `controlledResult` guard, and absent from the previous draft entirely.** Three tests: `TestExecute_ControlLock_InteractiveToolsDeferWhileControlled` (`:59`), `TestExecute_ControlLock_ReadOnlyToolsAreNotGated` (`:106`), `TestExecute_ControlLock_ReleaseUngatesInteractiveTools` (`:153`). These **must be re-run against the re-keyed lock** and must pass without weakening any assertion. If they need editing, FR-002c is wrong.
- `pkg/tools/browser/idle_reaper_test.go` (**33 refs**), `reaper_edge_test.go` (**26**), `reaper_lifecycle_test.go` (**15**) — FR-025 asserts per-tab reaping is not rewritten. (Whole-Chrome close is *new* behaviour tested separately, in tests 41/54/55, not a change to these.) **Note what these files already cover, which the previous draft's `ReapIdleSessions` claim implied they did not:** the reaper's own browser-context cancellation path (`manager.go:3027-3032`, `:3073-3078`, `:3123-3125`) and its `releaseGlobalTab` → `coord.ReleaseTab` call (`:3118` → `:3358-3365`). FR-040a's new behaviour composes with that path; it does not replace it.
- **The other 21 files in §2.2a's table** — `tabs_test.go` (100 refs), `switch_tab_same_index_recapture_test.go` (26), `focus_emulation_test.go` (24), `switch_tab_activation_test.go` (20), `switch_tab_capture_chain_test.go` (18), `browser_ws_test.go` (15) and the rest — are held to the same bar. They were entirely absent from the previous draft's regression list, which is how a ~364-edit item was budgeted at zero.
- `pkg/tools/browser/coordinator_test.go` — `TestCoordinator_OwnershipMarker_RoundTrip` (`:379`) only. **⚠ CORRECTED BY D1.5a — the other four moved from "must keep passing" to "must be deleted":** `TestCoordinator_UnlimitedDefault_AllowsPastOldCap` (`:429`), `TestCoordinator_PositiveCap_StillRejectsAtBoundary` (`:448`), `TestCoordinator_ConcurrentOpeners_PositiveCap_ExactlyOneWinner` (`:472`) and `TestCoordinator_SetMaxTotalTabs_ReloadRestoresUnlimited` (`:506`) all exercise `max_total_tabs` and the `TryOpenTab`/`ReleaseTab` reservation machinery, **which FR-059 removes from the code**. Revision 4's note that *"the tab budget is orthogonal to the browser cap and must not regress"* was correct then and is void now: **there is no tab budget.** They are listed under *Must be DELETED* below, so that the reviewer sees four disappearing test functions as an executed ruling rather than as the assertion-deletion this project treats as a finding.

**Must be rewritten, not extended:**

- `pkg/tools/browser/coordinator_test.go:154` `TestCoordinator_TwoAgents_OneChrome_TwoContexts` → `TestPool_TwoWorkspaces_TwoChromes`. Its per-agent assertion is now the wrong assertion, and its *"one Chrome"* premise is exactly what D1.4 replaces. Leaving it green while the model changed underneath is the `docs/internal/false-green-patterns.md` stale-green shape.
- `pkg/tools/browser/coordinator_test.go:203` `TestManager_Shutdown_DropsConnectionNotProcess` and `:244` `TestCoordinator_Shutdown_IsSoleKill` → re-scope to *"…for the key's own Chrome"*. Both encode the single-process model.
- `pkg/tools/browser/stress_5agents_test.go:267` `TestFiveAgents_ConcurrentStress` → **five agents on one workspace** (contention — the new normal case) **plus** five agents across five workspaces (isolation, admitted or refused by the **memory gate**, since there is no cap to bound them — D1.5a). Five agents on five implicit per-agent jars is no longer a scenario the product has.

**Must be DELETED, and this is a finding rather than a rename:**

- **The four `max_total_tabs` / reservation tests in `coordinator_test.go`** — `TestCoordinator_UnlimitedDefault_AllowsPastOldCap` (`:429`), `TestCoordinator_PositiveCap_StillRejectsAtBoundary` (`:448`), `TestCoordinator_ConcurrentOpeners_PositiveCap_ExactlyOneWinner` (`:472`), `TestCoordinator_SetMaxTotalTabs_ReloadRestoresUnlimited` (`:506`). Each asserts a behaviour of `max_total_tabs` and `TryOpenTab`/`ReleaseTab`, deleted by FR-059. **The third is the one to note in the PR body:** it is the in-flight-race test (*exactly one winner among concurrent openers*), and its subject — a reservation race — ceases to exist along with the reservation, rather than becoming untested. **Rule 2 of the bar above ("no assertion is deleted") does not cover these**: it governs migration of tests whose subject survives, and these four have no surviving subject. Deleting them without this paragraph is indistinguishable from assertion-stripping, which is why the paragraph is here.

- `pkg/tools/browser/coordinator_test.go:328` `TestCoordinator_Register_SharedContextMode_ReturnsRootCtxAndEmptyBrowserCtxID`. It asserts that `Register` returns an **empty** `browserCtxID` and that `coord.contextCount() == 0` — i.e. **it is the test that proves the isolation this spec promises did not exist.** The previous draft asked only to "re-key its Register argument", which would have left a green test asserting the absence of the product's headline guarantee. Under FR-031 the mode, the flag and `contextCount()` are all retired, so the test has nothing left to assert and is deleted with them. Its replacement is test 35, `TestNoCDPBrowserContextIsEverCreated`, which asserts the opposite property: no CDP browser context is created **anywhere**, because isolation is now the profile directory.

**Verification receipt discipline:** run each scoped test as `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/tools/browser/ > log 2>&1; echo "exit=$?"` — never through a pipe (a piped `tail` reports tail's status). Do not run the full suite locally; push and read CI.

### 10.2 Test datasets

| Input | Expected | Traces to |
|---|---|---|
| ctx: `workspace_id=W` | key `ws:W` | FR-007 |
| ctx: `workspace_id=""`, agent on exactly one CoreTeam (W) | key `ws:W` | FR-007 |
| ctx: `workspace_id=""`, agent on **no** CoreTeam | `ErrNoBrowsingContext`, zero key | FR-008 |
| ctx: `workspace_id=""`, agent on **two** CoreTeams (A, B) | `ErrNoBrowsingContext` naming A and B; WARN on both resolution paths | FR-033 |
| ctx: sub-turn, `workspace_id=W` inherited (`subturn.go:1323`) | key `ws:W`; no new manager, no new Chrome | FR-009, FR-026c |
| ctx: sub-turn, tool policy `ask`, no operator | denied with the headless reason; no approval request | FR-032 |
| agent on A and B; session meta `workspace_id=B` | `ws:B` from **both** turn and gateway | FR-018 |
| two agents, one workspace, real `registerSharedTools` | one `*BrowserManager` pointer for both | FR-002a |
| manager with no `sessions` entry for its key | `TabStateNoContext`, empty tabs | FR-013 |
| manager, browser live, 2 tabs | `TabStateOpen`, 2 tabs | FR-013 |
| manager, browser live, `len(se.tabs)==0` | `TabStateEmpty`, empty tabs | FR-013 |
| `mia`'s turn is built | `browser_list_tabs` **absent from the tool definitions**; `Execute` not entered; no `ModelMessage` asserted | FR-014 |
| 2 concurrent `browser_navigate` by 2 agents on **their own** tabs | both complete; 0 deferrals; lease never acquired | FR-021, FR-048 |
| 2 concurrent `browser_navigate` by 2 agents on the **workspace-owned** tab | 1 at a time in Chrome; **both eventually complete**; ≤1 deferral; 0 errors | FR-019, FR-020 |
| 8 concurrent action tools on the **workspace-owned** tab | 1 at a time; 0 errors; 0 deadlocks; **all 8 eventually complete or defer with a named holder** | FR-019, FR-020, FR-023 |
| lease holder panics mid-action | next acquire succeeds ≤ `leaseWaitTimeout` | FR-024 |
| human holds control lock (key `ws:W`) + agent action | ADR-038 D6 reason; lease never acquired | FR-002c, FR-022 |
| gate refuses, 2 live (LRU idle), request for a 3rd workspace | LRU **evicted**, gate re-asked, 3rd launched, **no error**; LRU's profile dir still present; LRU relaunches logged in | FR-050, FR-057 |
| gate refuses, LRU has a **live viewer**, request for a 3rd | **second**-LRU evicted; LRU pid/tabs/stream unaffected | FR-050, FR-010 |
| gate refuses, LRU has a lease-**exempt** call in flight, request for a 3rd | second-LRU evicted; the exempt call completes | FR-051 |
| viewer attached but silent past the WebRTC liveness window | counted **detached** by both eviction and `CloseIdle` | FR-052 |
| gate refuses, **all** instances viewed **and** busy, request for a 3rd | waits to its deadline, then an error naming the **workspace and MEMORY**, reason code `memory_pressure`; **still exactly 2 live** — no overshoot | FR-053, FR-063 |
| ~~the same, then a 4th request~~ | ~~exactly 3 live (`target+1`), one WARN; the 4th waits then errors~~ **TOMBSTONED — D1.5a deletes the `+1` overshoot; the row above is the whole behaviour now** | ~~FR-053~~ |
| `2 × threshold` evict-reopen cycles in one window | **exactly one** WARN naming the contending workspaces, **memory** as the constraint, and a remedy that exists; names **no config key** | FR-054 |
| fixture host 3916 MB vs 32 GB | the **same** check, `headroom >= PER_BROWSER_COST` (≈182 MB), giving different answers; no literal browser count in `defaults.go`; no per-renderer or per-tab constant anywhere in the capacity path | FR-062 |
| cgroup `memory.current/memory.max` = 0.84 / 0.85 / 0.86, **something evictable** | admit / admit / evict-then-re-ask | FR-057 |
| the same at 0.86, **nothing evictable** | **refuse**, naming memory — the case revision 4 deliberately left unasserted (E-2, now ruled) | FR-057, FR-053 |
| the gate on macOS or Windows | `availableRAMBytes` is **0** (`meminfo_other.go`), so the gate has no signal — the run **fails** rather than passing quietly; **red until §0.5 E-6 is ruled** | FR-057, FR-061 |
| any per-key Chrome launch | argv contains **no** `--renderer-process-limit` at any value; two cross-site tabs still occupy **distinct** renderer processes | FR-062 |
| W's browser: 0 tabs, 0 viewers, past idle window | process closed; `LiveKeys()` shrinks; profile dir present | FR-040 |
| W1's Chrome killed, W2 live with a viewer | W2 pid/tabs/stream unaffected; W2's manager not reset | FR-041 |
| W1 relaunched after kill | logged in (profile survived) | FR-041, FR-043 |
| reload, 2 agents on W, live login | pid unchanged; `pool.Close` count 0; 1 register/release pair | FR-026a, FR-026b, FR-028 |
| workspace W deleted with a live browser | exactly one `pool.Close(ws:W)` | FR-026 |
| K sub-turns run to completion | pool and manager counts equal baseline | FR-026c |
| attach frame `session_id` from B's chat, agent on A and B | resolves to `ws:B` | FR-016, FR-017 |
| ~~`tools.browser.max_browsers = 0` (and `-1`)~~ | ~~no operator ceiling; the derived target still binds~~ **TOMBSTONED — the key is deleted (D1.5a); there is no ceiling and no edge case** | ~~FR-038a, FR-056~~ |
| ~~`max_browsers = 2`, `max_total_tabs = 3`, browsers live for W1+W2~~ | ~~3 tabs **total** across both, not 3 each~~ **TOMBSTONED — `max_total_tabs` is deleted; there is no global tab budget to keep global** | ~~FR-038a~~ |
| ~~agent A opens 5 tabs at `max_tabs=5`; agent B opens 1~~ | ~~A's 6th refused; B's 1st succeeds~~ **TOMBSTONED — `max_tabs` is deleted; the per-agent tab SETS survive (row below), the per-agent CAP does not** | ~~FR-049~~ |
| `config.json` carrying `tools.browser.max_tabs` or `max_total_tabs` | **rejected at load** with a named error identifying the removed key — never silently ignored | FR-059 |
| repo-wide search for `MaxTabs`, `MaxTotalTabs`, `TryOpenTab`, `ReleaseTab`, `reservedTabs`, `reserveGlobalTab`, `releaseGlobalTab`, `maxTabsReachedErr` | **zero** hits, production **and** `_test.go`; `totalTabCountLocked` **survives** (counting, not enforcing) | FR-059 |
| agent loops `browser_open_tab`; fixture memory crosses the threshold at tab *k* | tab *k+1* **refused** with `memory_pressure`; the gate was consulted at all five sites (`manager.go:1139, 2005, 2047, 2216, 2286`) | FR-060, FR-063 |
| a reaper sweep in which nothing is ever closed / a gate that admits every input | **both FAIL** — the assertion is on the control having acted, not on the absence of an error | FR-061 |
| `browser_click` spawns a tab; gate refuses to adopt it | reason `tabAdoptReasonMemoryPressure` (`"memory_pressure"`); its **own** switch arm; text names memory **and** a real remedy; names **no config key**; **never** the `default:` arm | FR-063 |
| agent A opens a tab; agent B lists tabs; operator opens a tab | B sees the operator's tab, labelled as the workspace's, and **not** A's | FR-048 |
| two goroutines `Acquire` two **different** keys while the gate refuses, LRU idle | **both** succeed; the LRU is evicted exactly once; the gate is re-asked between them, so the second does not launch on a stale reading | FR-050, FR-057 |
| the same, every instance pinned | **no growth at all**; both callers wait, then both are refused naming memory; `LiveKeys()` unchanged | FR-053, FR-057 |
| W idle-closed, then a tool call arrives | Chrome relaunches from W's profile, login intact, `LiveKeys()` +1, **same** manager | FR-040a |
| `ReapIdleSessions` cancels `se.browserCancel` while `ws:W` is live in the pool | next `Acquire(ws:W)` yields a drivable browser; no live-but-dead key | FR-040a, CRIT-102 |
| stale `SingletonLock` in `<profileRoot>/ws/W/` after a crash | W's next launch succeeds; the file is removed first | FR-042b |
| boot with 3 stale `ws-*.pid` markers (2 dead pids, 1 live Chrome) | 0 orphan Chromes, 0 stale markers, 0 stale locks; INFO + WARN | FR-042a |
| workspace W deleted with a live browser | `pool.Close(ws:W)` once, **then** `<profileRoot>/ws/W/` removed | FR-026, FR-043a |
| W idle-closed / **evicted** / roster-emptied / reloaded / crash-recovered | profile directory **present** in all **four** distinct cases | FR-043a |
| upgrade from an install with a populated `profiles/default/` | both workspaces logged out; neither profile is a copy; `profiles/default/` unmodified | FR-043b |
| 10 write-class + 5 read-only browser calls by one agent | 10 audit events (incl. the **tenth**) with workspace/agent/tool/host; 0 from the read-only calls | FR-027 |
| a deliberately dotted audit event name in a fixture | the name-pattern assertion **fails** | FR-058 |
| boot with 4 workspaces and warm defaults on | exactly 1 live key, 1 capture pipeline | FR-016b |
| boot: marker pid alive **and** per-key launch lock **held** | refuse to launch that key, name the other gateway, terminate nothing | FR-042a |
| boot on macOS: marker pid alive, lock acquirable | marker cleared, WARN names the pid, **process survives** | FR-042a |
| `lease_wait = 45s`, `page_timeout = 30s` | clamped at load and reload; WARN names both keys; contended call still returns `deferred`, not a CDP error | FR-023a |
| boot, fresh install, **zero** live keys | managed-Chromium resolution/download has started | FR-016c |
| workspace id `../evil`, `a/b`, `.`, `..`, `""` | `ErrNoBrowsingContext`; no path constructed | FR-037, MIN-106 |
| every registered `browser_*` tool, under a held control lock vs a held write lease | the two deferral answers agree for all of them; `browser_list_tabs` defers under neither | FR-019a |

---

## 11. Functional requirements & success criteria

- **FR-001 … FR-063** as enumerated in §9. All MUST. **Counts: 87 rows in the §9 matrix, 10 of them withdrawn tombstones (FR-011, FR-012, FR-014a, FR-038, FR-038a, FR-039, FR-046, FR-049, FR-055, FR-056), so 77 live FRs.** Movement in **this** revision (the D1.5a subtraction pass) is stated first, then revision 4's, which it amends:
  - **D1.5a: +5 rows added** — FR-059 (the deletion, as implementation scope), FR-060 (the gate in the tab-open path), FR-061 (idle close + gate are the entire defence), FR-062 (`PER_BROWSER_COST` and the absence of every constant), FR-063 (a reason code naming memory). **+5 withdrawn to tombstones** — FR-038, FR-038a, FR-049, FR-055, FR-056. **5 rewritten in place** — FR-044 (narrowed: the number is measured; Linux + capture is what remains), FR-050 (eviction's trigger is the gate, not a target), FR-053 (refusal naming memory; the bounded overshoot is deleted), FR-054 (the WARN's remedy re-derived), FR-057 (the sole hard stop; E-2 ruled). **Live count is unchanged at 77** — five in, five out — which is a coincidence, not a design property.
  - **Revision 4: +13 rows added:** FR-043b, FR-048, FR-049, FR-050, FR-051, FR-052, FR-053, FR-054, FR-055, FR-056, FR-057, FR-057a, FR-058 — **eleven new requirement areas**, since FR-057a is a lettered sibling of FR-057 and FR-043b of FR-043a. *(Four of these thirteen — FR-049, FR-055, FR-056 and, in effect, FR-038a — were withdrawn by D1.5a one revision later; each states why, because two of them were recorded by earlier reviews as CRITICAL resolutions.)*
  - **Revision 4: −3 withdrawn to tombstones:** FR-014a (D1.12), FR-039 and FR-046 (D1.7). **No FR is renumbered and no number is reused** — other documents cite them.
  - **7 rewritten in place, keeping their numbers because their subject is unchanged and only their content moved:** FR-008a (three outcomes, not four), FR-016b (one instance, not the resolved key generically), FR-019/FR-020/FR-021/FR-023a (lease rescoped to the operator's tab; retry-then-error), FR-027 (per action, not first use), FR-037a (flat path), FR-038/FR-038a (soft target, ceiling not cap), FR-042a (lock, not pid), FR-043a (four negatives), FR-044 (PSS, not RSS).

Every criterion below states **what would make it fail.** Round-1 found four gates that could not fail for the defect they named; each was rewritten and its failure mode spelled out. **Round-2 found three more** — test 37 behind a skip, SC-012 with no executable form, and SC-015 with no mechanical gate — and rewrote or resolved each: SC-012a is mechanical with four stated conditions, SC-012 is declared a human gate with a named owner and a named artefact (§0.3.1), and SC-015 is satisfied by ADR-072's blanket attribution (its header block — see SC-015 for the corrected citation). **A criterion with no failing check is now stated as a human gate rather than left to look like a test.**

- **SC-001 (headline, the reported defect).** Browse as Mia in workspace W; switch the chat to Jim; Jim's `browser_list_tabs` returns the tab. Measured by test 38, `TestHandover_ThroughRealRegistrationPath`, which goes through `registerSharedTools` — **not** a hand-built manager. *Fails if:* the two agents resolve different managers. **This test is red today**; if it is green before the change, it is not exercising the real path.
- **SC-002 (isolation exists and is by profile).** `TestBrowsingContext_CrossWorkspaceIsolation` passes against real Chrome, asserting a missing cookie **and** distinct pids **and** distinct `--user-data-dir` paths, under the **shipped default configuration** — no flag flip, no env var. *Fails if:* the cookie is present, the pids match, or the test needs a non-default config to pass. **The previous SC-002 could only pass with `capture_shared_context=false`, i.e. it proved a property of a configuration nobody ships.** FR-031 removes the flag so this criterion has no configuration to hide behind.
- **SC-003 (no silent merge, behavioural not just structural).** Zero `pool.Acquire` calls in a full run carried a key that did not come from a `ResolveBrowsingKey` return in the same turn (test 34). *Fails if:* any acquire's key is untraceable. The old SC-003 asserted only that `BrowsingKey`'s field was unexported — which constrains key *construction*, never *use*, and would not have caught a parent's key being passed inside a sub-turn.
- **SC-004 (delegated work shares, and leaks nothing).** `TestSubTurn_UsesWorkspaceBrowser` asserts the sub-turn is logged in on `ws:W`, and `TestSubTurns_NoPoolGrowth` asserts K sub-turns return the pool and manager counts to baseline. *Fails if:* a sub-turn gets its own key, or the counts grow. **This inverts the previous SC-004** (which asserted a distinct context for the sub-turn) because the ruling inverted the requirement — recorded so the reversal is visible rather than looking like a deleted test.
- **SC-005 (concurrency is deterministic).** Eight concurrent action tools on one workspace browser, repeated 50× under `-race`: zero errors, zero deadlocks, exactly one executing writer at any instant. *Fails if:* any interleaving, error, or hang.
- **SC-006 (three states).** A table-driven test enumerates all three `ListTabsState` values and asserts pairwise-distinct model-visible payloads; a fourth value is a compile-time impossibility. *Fails if:* any two payloads are equal, or a new value is added without updating the test.
- **SC-007 (contract intact, including its meaning).** Three conditions, all required. (1) `make verify-contracts` exits 0. (2) **REVERTED to its unamended form:** the `contracts/` diff contains **nothing outside `description:` text** — no `properties:`, `required:`, `enum:` or `type:` change in any existing schema, **and no added path**. *(Revision 3 amended this condition to allow "exactly one added path" for FR-046. FR-046 is withdrawn by D1.7, so D1 adds no path and the amendment is removed — §17 C-302.)* (3) **`TestGateway_SessionIDIsBinding` passes** — an attach frame whose `session_id` belongs to workspace B's chat resolves to B for an agent on both A and B. *Fails if:* the resolution ignores `session_id`. **Condition (3) is the fix for the round-2 finding that this gate could not fail:** conditions (1) and (2) are shape checks, and the change FR-016 makes is a *semantic reversal* of a documented guarantee (`BrowserAttachFrame.yaml`: the server binds *"regardless of the value sent here"*). A shape check passes a reversal cleanly. Additionally, the two replacement description strings must be reviewed against FR-016's verbatim text, and `BrowserInspectRequest` must be confirmed **not** to have gained chat-session semantics (US-10/AC3).
- **SC-008 (nothing green by accident).** Every rewritten test in §10.1 is confirmed to **fail** against the pre-change code and **pass** after. **Extended to the four tests round-1 identified as unfalsifiable**, each of which must be red today: test 4 (`TestRegisterTools_NoBoundManagerField`), test 6 (`TestControlledResult_UsesResolvedKey`), test 32 (`TestReload_PruneUsesBrowsingKeys`), test 38 (`TestHandover_ThroughRealRegistrationPath`). A test that passes both ways is not evidence. **Round-2 OBS-101 is accepted and this criterion is no longer phrased as an intention:** each of the four is *expressible* against current code (test 4 structurally — all eleven structs hold `mgr`; test 6 by taking the control lock under a non-`"default"` id; test 32 by seeding `al.browserMgrs` with a `"ws:W"`-shaped key and running the prune; test 38 through `registerSharedTools`), so "must be confirmed to fail" costs four commands. *Fails if:* the four `exit=` receipts are not pasted into the PR **before** the implementing commits — captured without a pipe, per §10.1's receipt discipline. A red-then-green claim made after the fix has landed is not reproducible and does not satisfy this criterion.
- **SC-009 (the control lock is provably alive).** `tools_control_test.go`'s three tests pass against the **re-keyed** lock, with no assertion weakened. *Fails if:* any assertion is relaxed to accommodate the re-key. **`shared_control_test.go` is explicitly not this criterion's guard** — it never calls `controlledResult`.
- **SC-010 (the pool is bounded by MEMORY and honest — REWRITTEN AGAIN; revision 4's form asserted an overshoot D1.5a forbids).** With a fixture memory reading under which the gate refuses beyond N live instances, and a run that requests N+3 workspaces, all of them evictable: **all N+3 calls succeed**, exactly three evictions occurred, the gate was **re-asked after each** (so no launch rode a stale reading), each evicted workspace's profile directory still exists, and **zero** errors reached any caller. Separately, with every instance pinned: **the pool does not grow at all**, each further caller waits to its own deadline and is then refused with an error naming **memory** and carrying the `memory_pressure` reason code. *Fails if:* any caller is refused on the evictable path; **any instance is launched past a refusing gate, including one**; an eviction deletes a profile; or the all-pinned refusal names a cap, a config key, or nothing. **This criterion has now been inverted by two successive rulings** — revision 3 required refusal at a cap (D1.7 forbade it, §17 M1), revision 4 required `N+1` overshoot with a WARN (D1.5a forbids it) — which is why its assertions are now written against *the gate's answer* rather than against any number.
- **SC-011 (a crash is contained).** Killing one workspace's Chrome leaves every other workspace's pid, tab set, viewer stream and cookies intact, and does not reset their managers. *Fails if:* any other key is affected — which is today's behaviour (`watchForCrash` resets every connector manager).
- **SC-016 (the pool leaves nothing on the host — PLATFORM-QUALIFIED).** After a `kill -9` of the gateway with three workspace browsers live, the next boot leaves **zero** `$OMNIPUS_HOME/browser/ws-*.pid` markers and zero stale per-key launch locks **on every platform**, and each of the three workspaces' next browser call launches successfully from its own profile. **Orphan Chrome processes: zero on Linux** (identity confirmed via `/proc/<pid>/exe` before termination). **On macOS and Windows the surviving processes are NOT terminated** — one WARN per surviving pid is emitted instead, and the residual host-memory exposure outside the target's accounting is accepted and stated (D1.9, §12 A20). *Fails if:* any marker or stale lock survives anywhere; an orphan Chrome survives **on Linux**; **no WARN names a surviving pid on macOS**; or any workspace's relaunch fails on a stale `SingletonLock` — the last silently falsifies FR-043's "the login survives" promise. **Revision 3 asserted zero orphans unconditionally, which its own §12 A20 already said was false on macOS** — a criterion that must fail on a supported platform is the inverse of the "gate that cannot fail" shape rounds 1 and 2 both flagged (§17 M9).
- **SC-017 (a departed client's data departs).** After deleting a workspace that had a live browser with a session cookie, `<profileRoot>/ws-<id>/` does not exist. *Fails if:* it does — and separately, if the directory is removed on **any** of idle close, **eviction**, roster change, reload or crash recovery, because that logs the operator out in **four** situations where the profile is the whole point. *(Four, not revision 3's five: the operator-close trigger went with FR-046, and eviction takes its place. Eviction is the one that matters most — it is only an acceptable policy **because** the profile survives it, so a profile deleted on eviction turns a capacity mechanism into data loss.)*
- ~~**SC-018 (a full pool is escapable).**~~ **WITHDRAWN with FR-039 and FR-046 (D1.7).** There is no refusal to escape on the normal path. Its concern — *"an operator-facing message must name an action that actually works"* — survives as **SC-022**, applied to the only operator-facing capacity text that remains.
- **SC-022 (every capacity message names something real — REWRITTEN; its own example became the defect it warns about).** **Three** capacity texts survive: FR-054's thrash WARN, FR-053's refusal error, and FR-063's model-visible tab-refusal note. Each must name **memory** as the constraint and an action that exists — *give the host more memory*, *browse fewer workspaces at once*, *close tabs or browsers you are done with, or wait*. **None may name a config key**, because no key bounds browsers or tabs any more. *Fails if:* an action named in any of the three cannot be traced, **by the reviewer**, to the function that performs it; **or** any of them names `max_browsers`, `max_tabs`, `max_total_tabs` or any other setting. **Prose review has failed this twice** (round-2 CRIT-103, round-3 M-309), so the requirement is on the reviewer's method, not only on the text: trace each named action to its implementing function, or reject the PR. *And note what happened here: revision 4's own statement of this criterion prescribed naming `tools.browser.max_browsers` and warned about the target-vs-ceiling trap — advice that, one ruling later, would have shipped a message naming a deleted key. **The criterion caught itself only because this pass swept it.** That is the argument for the tombstone discipline, not a footnote to it.*
- **SC-012 (G-1: the memory measurement — a HUMAN gate, NARROWED by D1.5a).** `PER_BROWSER_COST` is now measured — **≈182 MB** on macOS, Chrome for Testing, idle and non-capturing (FR-062) — so this gate no longer certifies a *ceiling*, because there is none. What the implementing PR body must contain is what that measurement does **not** cover: the raw **PSS** for one and two Chromes **on Linux**, the same figure **with capture running** (the injected extension plus encoding, unmeasured today), the gateway's own steady-state PSS, and the tool and command that produced them (`smem`, or `/proc/<pid>/smaps_rollup`'s `Pss:` summed over each instance's process tree — **`ps` reports RSS and cannot produce this**). *Fails if:* the figures are **RSS** (the metric ADR §8 retracts, over-counting by 2.6× on the measured box — approving an RSS-derived figure is a gate certifying the specific error it exists to catch); **or** ≈182 MB is shipped as the Linux figure without a Linux run behind it; **or** the capture delta is left unmeasured **and unstated**; **or** any place quoting ≈182 MB omits its scope. **Owner: the implementing PR's human reviewer**, who must not approve without seeing the numbers. §0.3.1 explains why no mechanical form is honest. Test 51 is the partial mechanical half. *(Revision 4 required arithmetic "through D1.5's formula to the shipped `operator_ceiling`". The formula and the ceiling are both deleted — D1.5a — so that half of the gate is satisfied by there being nothing to compute.)*
- **SC-012a (G-2: the capture spike — a MECHANICAL gate, corrected against the shipped workflow).** `TestSpike_CaptureAgainstSecondChrome` **ran and passed** before Stream P's first commit. Four conditions, all required: (1) the test uses `requireBrowserOrFail`, and `grep -c skipIfNoBrowser` in its file returns 0 for that test; (2) it runs as **its own step** in the existing `browser-e2e` job (`.github/workflows/pr.yml:392`), with its own `-run '^TestSpike_CaptureAgainstSecondChrome$' -count=1` — **not** folded into the existing step, whose `passes -lt 180` floor (`:481`) a single-test invocation would trip; (3) the step runs under `set -euo pipefail`, and the PR-body receipt is captured as `cmd > log 2>&1; echo "exit=$?"` reading `exit=0`; (4) the log contains exactly one `--- PASS` and neither `--- SKIP` nor `no tests to run`. *Fails if:* any of the four is missing. **A skipped result is a FAILED gate** — this is the single load-bearing assumption of D1.4, and the equivalent claim for CDP contexts proved false against real Chrome 150 (`coordinator.go:330-348`).
  **Two corrections to revision 3's version of this criterion, both verified on this worktree.** (a) It asserted the CI gate *"always skips without the env var"* and routed G-2 to the Fly worker on that basis. **`OMNIPUS_BROWSER_E2E: "1"` IS set** — job-level, at `:416`, with the comment *"Set ONLY here"* — and `:468-472` already fails the job if either skip path fires. The gate machinery exists; **only the test does not**. (b) Its blanket "never through a pipe" would have required rewriting a **correct** shipped step: `go test … 2>&1 | tee /tmp/browser-e2e.log` under `set -euo pipefail` propagates `go test`'s status, so the failure mode the rule guards against (`cmd | tail` reporting *tail's* status) cannot occur there. The rule is restated as one about the **author's PR-body receipt**, which is where it bites.
- **SC-013 (no residual constant, tests included).** Repo-wide references to `DefaultSessionID`/`defaultSessionID` are **zero**, in production **and** test code — down from 57 non-test hits (37 executable + 2 declarations + 18 comments) and **364 test-side references across 25 files**. *Fails if:* any survives, including a test-only alias. A test-only alias would leave this criterion reading zero while the constant is alive, the reaper suite still asserting against `"default"`, and the measurement meaningless — which is why the count is repo-wide rather than non-test (§2.2a).
- **SC-019 (G-3 + G-4: the two Chromium unknowns — MECHANICAL).** The PR body carries receipts from one Chrome launched inside a `memory.max`-capped cgroup: the renderer limit Chrome computed for itself (does it read the cgroup limit, or host RAM?) and whether a memory-pressure notification was ever delivered. *Fails if:* either is answered in prose rather than from a run, **or** FR-057 ships while G-3 is negative without stating that D1.5's `min(host_RAM, cgroup_limit)` describes a policy Chrome is not following.
- **SC-020 (G-5: cold start with a warm profile — a HUMAN gate).** The PR body carries the measured time from `Acquire` on an idle-closed key to a drivable page, over ≥5 runs, and the arithmetic from it to FR-054's window and threshold. *Fails if:* the two thrash constants are shipped with no measurement behind them — that is the failure mode §0.3 exists to prevent for the target, applied to the two constants most likely to be guessed because they look small. ADR-042's ~30–60 s covers a fresh install *including a Chromium download* and does not satisfy this.
- **SC-021 (G-6: memory or CPU — a HUMAN gate, and D1.5a makes it MORE load-bearing, not less).** The PR body states which resource binds first on the measured host class with N instances live and one watched, with the sampling method. *Fails if:* the feature ships without it. ADR §7's entire argument is that a **CPU** bound solved the problem on a 2-core box at 85–99 % utilisation with **one** Chrome; the pool multiplies browser, GPU and encoder processes by N on that same box **and now bounds memory only, with nothing else bounding anything**. *(Revision 4 phrased the failure as "the ceiling default ships without it". There is no ceiling — so the escape hatch of "set a conservative ceiling and move on" is gone too. If CPU binds first, the one control that exists is watching the wrong resource, and no counter is standing behind it.)*
- **SC-014 (gates).** `gofmt -l . | wc -l` = 0; `golangci-lint run --build-tags=goolm,stdjson` exit 0; CI `go test -tags goolm,stdjson -count=1 ./...` exit 0; `govulncheck` 0 vulnerabilities; `npm run typecheck` exit 0; `npx vitest run` exit 0. **Not sufficient on its own:** SC-012, SC-012a, SC-019, SC-020 and SC-021 are separate and none is satisfied by a green CI run. Note also that `golangci-lint` caps findings at 3 per message by default — read `docs/internal/false-green-patterns.md` before reporting a clean lint.
- **SC-015 (attribution) — SATISFIED, and the quotation is corrected.** §12 A10 asked for the operator of record. **Revision 3 quoted a sentence the consolidated ADR does not contain** — it attributed the quote to "D1.1a's closing paragraph" and had it enumerate *"D1.0, D1.1a, D1.2, D1.4, D2.9 and D2.11"*, four of which are numbers the consolidation deleted. The attribution now lives in the ADR's **header block**, not in any D-section, and reads:
  > **Decider for every ruling in this document:** Daniel Piatkowski (operator). Recorded once here so the individual "operator ruling" citations below have a named authority; a spec cannot resolve its own provenance.
  It is **broader** than the sentence revision 3 quoted — it covers *every* ruling in the document with no enumeration to fall out of date, which is why the renumber did not break it. It carries **no date**, so the dates on individual rulings (2026-08-31 for the isolation axis, sharing, no-fallback and lease ownership; 2026-09-01 for D1.7's eviction and D1.9a's tab ownership) come from the rulings themselves. *Would fail if:* a future ruling is added to a document **other** than ADR-072 and cited here as "operator ruling" without its own attribution — the ADR's blanket only covers the ADR. §17 M3.

---

## 12. Ambiguity self-audit

Each item is **resolved here as a recorded assumption** unless marked otherwise; a different ruling changes the spec.

| # | Ambiguity | Resolution |
|---|---|---|
| **A1** | **Does a key change alone make a browser isolated?** No. Under the previous CDP-context design it did not (a second `sessions` entry reused one `browserCtxID`), and under the current design it does not either — the isolation is the **profile directory**, which only exists because a separate Chrome was launched with a separate `--user-data-dir`. | **DECIDED (FR-037):** isolation is a property of `pipeLaunchConfig.userDataDir` (`exec_resolver.go:385`), one per key. A test that asserts only distinct map keys proves nothing; every isolation test asserts distinct **pids and profile paths** (§10.2). |
| **A2** | **~~Discriminator for "unattended"~~** | **WITHDRAWN by the D1.10 ruling.** No discriminator is built. `spawnSubTurn`'s `WorkspaceID: parentTS.opts.WorkspaceID` (`subturn.go:1323`) is correct as-is. Recorded so a reader of the previous draft does not implement it. |
| **A3** | **~~Viewer-count attendance seam~~** | **PARTIALLY WITHDRAWN.** Attendance is not a concept any more. `BrowserManager.Viewers()` is still added (FR-010) because the **reaper's viewer pin** and the **pool's idle-close** both need it and no exported accessor exists today. |
| **A4** | **D1.12's "not permitted" state cannot be a `ListTabs` return value.** "Not permitted" is produced by the policy layer; a tool policy stopped from running cannot report why. | **RESOLVED BY THE ADR, more strongly than this spec had it.** Revision 3 kept the state as an "end-to-end observable" and added FR-014a to give it an artefact. **The ADR withdraws it outright (D1.12)** and this spec follows: `FilterToolsByPolicy` `continue`s past a deny verdict (`compositor.go:436-438`), so the agent never receives the tool and there is no boundary at which to observe anything. `TabState` is a closed **three**-value enum (`no_context`/`open`/`empty`) with no "denied" member; FR-014a, test 10, US-8/AC2+AC3 and holdout 4 are all withdrawn. The underlying defect is real and lives in ADR §6. §17 C3. |
| **A5** | **`TabStateEmpty` reachability.** `tabs.go:50-52` asserts a running browser with zero tabs "cannot occur", yet `ReapIdleSessions` has a real zero-tab branch and `CloseTab` can empty `se.tabs`. | **DECIDED:** `TabStateEmpty` is **reachable but transient**, and is specced (US-7/AC3). Resolution failure is **not** a `TabState` — it is an error (FR-008), because an error is the only shape a model reliably treats as "stop and report". |
| **A6** | **"No wire change" is true of the schema and false of the meaning.** `BrowserAttachFrame.yaml` says the server binds *"regardless of the value sent here"* and *"agent_id is the binding key"*. FR-017 makes `session_id` binding. | **DECIDED (FR-016, SC-007):** state the reversal plainly, quote the replacement text, and add a **behavioural** assertion (test 46) so the gate can fail. `BrowserInspectRequest.session_id` is a *browser* session id, not a chat session id, so it does **not** gain the semantics; `browser_inspect` resolves from the agent alone under FR-033. **If implementation finds resolution needs a field the frames do not carry, STOP** — that is Hard Constraint #8's 5-step process and a spec amendment, not a code change. |
| **A7** | **The write lease is filed under D2.10 but ADR §4 calls it the largest open risk in D1, and both specs specced it with incompatible APIs.** | **DECIDED (operator ruling):** **this spec owns it — §14 is the single normative definition.** The D2 spec must delete its Stream F lease, FR-023, US-14, its BDD scenario and its test 23, and reference §14's FR-019…FR-024 and FR-019a instead. §14 adopts D2's pre-built-`ToolResult` convenience so D2's new tools do not hand-roll the deferral shape. A structural test asserts exactly one lease primitive exists in `pkg/tools/browser`. |
| **A8** | **Fairness under sustained contention** is an explicit ADR open question. | **ASSUMED:** bounded wait then defer, no queue, no fairness guarantee — matching the ADR's stated scope. §14 fixes the bound, its config key and its clock seam so "unfair" is at least *bounded and testable*. A starvation-free queue is deferred, not forgotten. |
| **A9** | **Upgrade path for existing per-agent browser state.** | **DECIDED: discard, do not merge** — merging per-agent jars into a workspace jar would pool logins from agents that never shared them, a silent privilege grant at upgrade time. **And largely moot:** with `capture_shared_context` defaulting true (`defaults.go:671`), most installs have **no** per-agent CDP context to discard; what they have is one shared default-context profile. The release note must say operators re-log-in once, per workspace. |
| **A10** | **Operator of record for the rulings.** | **RESOLVED — and the citation is corrected.** The attribution is in ADR-072's **header block**, not in any D-section, and it is unenumerated: *"Decider for every ruling in this document: Daniel Piatkowski (operator)."* Revision 3 quoted a longer sentence that named six sections, four of which the consolidation deleted — a quotation the ADR no longer contains. The header form is broader and survives renumbering, which is why nothing broke. See SC-015. §17 M3. |
| **A11** | **Two gateway processes on one `$OMNIPUS_HOME`.** The write lease is in-process only (FR-030, correctly — `fileutil.WithFlock` is a documented no-op on Windows). The pool adds a per-key on-disk launch lock, which inherits the same Windows no-op. | **DECIDED: out of scope, stated rather than silent.** Two gateways on one home are already unprotected for all six file stores on Windows (ADR-054 §5.1) and the pool neither worsens nor fixes that. On POSIX the per-key `flock` gives the same single-launch guarantee the current singleton lock gives. Filed as follow-up with ADR-054's `LockFileEx` work, not solved here. |
| **A12** | **Does a workspace browser outlive the last agent that can use it?** FR-026 said "roster change" without a predicate. | **DECIDED (FR-026a):** a workspace key is live while the workspace exists **and** has ≥1 browser-policy-allowed agent on its CoreTeam. Losing the last such agent closes the browser (the profile survives). This is also the prune's liveness predicate, so the two can never disagree. |
| **A13** | **What happens to a running sub-turn's browser work when the parent is cancelled?** | **DECIDED (FR-036):** ADR-057's inherited `routingSessionID` makes chat-wide Stop cascade to sub-turns, which cancels the in-flight tool call and releases the lease (FR-024). **No browser is closed** — the browser belongs to the workspace, not the turn, so a cancel must not log the operator out. |
| **A14** | **Is `ResolveBrowsingKey` evaluated once per turn or once per tool call?** | **DECIDED: once per tool call**, inside `ManagerResolver.ManagerFor` (FR-002a). Per-call is required anyway because the manager must be resolved per `Execute`; and since there is now exactly one key shape and no viewer-dependent branch, per-call resolution is **deterministic within a turn** — it cannot change under the caller the way the withdrawn attendance check could. No caching layer is specified; if profiling later demands one, it caches per turn, never across turns. |
| **A15** | **`max_browsers`' value.** | **DECIDED — there is no key, so there is no value. ⚠️ SUPERSEDED BY D1.5a, and the row is kept because the answer moved twice.** Revision 3 waited on a measurement. D1.5 made it a *derivation* (a target computed per host, clamped by `tools.browser.max_browsers`). **D1.5a deletes both**: there is no derived target, no ceiling and no key — the live-memory gate is the only admission control (FR-057), and `PER_BROWSER_COST` ≈182 MB is the only measured input it needs (FR-062). Revision 3's own objection to a single measured integer — *it would ship the 3916 MB box's answer to a 32 GB machine* — is now satisfied by measuring **live headroom per request** rather than by deriving a number at all. FR-056 is tombstoned; FR-044 is narrowed to the Linux and capturing measurements that remain. |
| **A16** | **Does `chrome.tabCapture` actually work against a second Chrome's default context?** ADR D1.4 asserts each Chrome carries its own extension, so yes. | **UNRESOLVED — GATE G-2 (FR-045, SC-012a).** Flagged rather than assumed because this is the same *class* of claim that proved false for CDP contexts (`coordinator.go:330-348`, verified against real Chrome 150), and that falsification cost an entire design. Prove it with a spike **before** Stream P is built, under §0.3.1's four conditions — a skipped test is a failed gate. |
| **A17** | **`browser_handle_dialog` is exempt from the write lease, and §14 rule 3 claimed "membership is a rule, not a list" while listing a MUTATING tool as exempt.** The rule and its own exemption contradicted each other, so test 18 could only ever check a hand-written list — the list-driven test MAJ-008 was raised to eliminate — and that list had already been widened 3→5 the same day under pressure from the sibling spec, with no criterion distinguishing a legitimate widening from a convenient one. | **DECIDED — the rule is restated as a biconditional that actually holds, and it is not "does it mutate".** A `browser_*` tool takes the write lease **iff** it is gated by the ADR-038 D6 human-control lock (`controlledResult`). Over the eleven tools shipped today that biconditional holds **exactly**, with no exceptions: `controlledResult` is called by `browser_navigate` (`tools.go:119`), `browser_click` (`:232`), `browser_type` (`:429`), `browser_evaluate` (`:879`), `browser_switch_tab` (`tabs.go:113`), `browser_close_tab` (`:171`) and `browser_open_tab` (`:239`) — and by none of `browser_screenshot`, `browser_get_text`, `browser_wait` or `browser_list_tabs`, whose file says so outright (`tabs.go:20`). One classification, two consumers, no second list. **`browser_snapshot` falls out correctly** as read-only and needs no exception. **`browser_handle_dialog` is exempt from BOTH gates, not one** — that is what makes it coherent rather than arbitrary: a JS modal blocks the renderer, so the panel's own `Input.dispatch*` injection is blocked too and a human holding the wheel is **equally** unable to clear it; only `Page.handleJavaScriptDialog` can. Exempting it from the lease but not the control lock would leave the tab wedged for the human as well. **This obligation on the D2 spec is now RULED BY THE ADR, not merely requested by this one.** ADR-072 **D1.8** decides it directly: *"`browser_handle_dialog` is exempt from the write lease **and** from `controlledResult`… a modal blocks every CDP command on that tab, including the live panel's own input injection — so the human at the wheel is equally stuck and has no button that works. Gating the one tool that can clear it leaves both parties frozen. This narrows ADR-038 D6's exclusivity by exactly one tool, on a tool that cannot act on page content."* The reasoning is the same one this row reached independently, and the ADR states the ADR-038 narrowing explicitly, which this spec could not do on its own authority. **So it is no longer a cross-spec ask flagged for the operator: it is a decision D2 implements.** If D2 registers the tool gated anyway, that contradicts the ADR, not this spec. **Unspecified interaction, now specified:** after `browser_handle_dialog` clears a modal, the still-blocked leaseholder resumes and acts on a page that changed underneath it. Its result is **not** undefined by fiat — the tool that resumes must re-verify its own precondition before completing (a `browser_click` re-resolves its selector; a `browser_navigate` re-checks the current URL), and FR-024's `defer release()` means the lease is released either way. |
| **A18** | **A `tools.browser.profile_dir` change on reload, under N per-key directories derived from it.** | **DECIDED — preserve the shipped behaviour per key, rather than invent a relocation.** Today `ApplyRuntimeConfig` logs a WARN and does **not** apply a `profile_dir` change to a running Chrome (`coordinator.go:681-687`: *"applies after gateway restart"*). The pool keeps exactly that: the change is not applied to any live Chrome, the WARN now names how many keys are affected, and every key re-derives its path from the new `profileRoot` at the **next restart** — at which point every workspace is logged out, because the profiles it is pointed at are new empty directories. That consequence is stated in the WARN text and in the config key's doc comment. Relocating N profiles live was rejected: it means copying hundreds of megabytes per workspace while Chrome holds files open in each. |
| **A19** | **Is FR-033's refusal actually cheaper than stamping the workspace onto the turn?** The round-2 review argued the scheduled path already stamps it (`loop.go:6933-6957`) and that FR-033 therefore pays a near-certain cost — a multi-workspace agent losing its browser on every heartbeat — for a case the platform already solves. | **DECIDED — keep FR-033, and the premise the objection rested on is corrected (§16 MAJ-113).** The stamping path is real and is now cited in §6 and in US-6/AC0, but it **already covers heartbeats**: jobs are named `heartbeat:<workspaceID>:<agentID>` (`heartbeat_schedule.go:30-33`) and that workspace is parsed back and stamped before the run (`schedules.go:654`, `:513`, `:527-576`), so a heartbeat resolves at **rung 1** and never reaches FR-033. Adding Ray to a second workspace creates a *second, distinct* heartbeat job with its own workspace — it does not make his existing heartbeats ambiguous. The residual is a **plain, operator-created** schedule, which resolves to `""` because ADR-065 FR-8 removed the channel source (`schedules.go:632-639`); for a multi-workspace agent that turn is refused. **The alternative — a per-agent "browsing home workspace" — is declined:** it is a new agent-level field for a case the platform is already migrating away from (ADR-037 removed the global delegation graph for exactly the reason that "the owner's workspace" is ambiguous for a multi-workspace agent, and `resolveScheduleWorkspaceID`'s own doc rejects `CronJob.AgentID` as a source on the same grounds). Adding one back for browsing would re-introduce the global-agent-attribute shape this project deliberately removed. |
| **A20** | **What does boot reconciliation do with a stale ownership marker whose pid is still alive?** | **DECIDED — and the discriminator is CORRECTED (D1.8).** Revision 3's answer began "terminate it", reasoning that nothing can distinguish an orphan from a second gateway. **Something can: the per-key launch lock.** On Unix a flock auto-releases when its holder dies, so a *held* lock plus a live Chrome pid proves a live neighbour, and `takeLaunchLock` (`coordinator.go:1442-1480`) already gets this right for the single-Chrome case by refusing to launch rather than killing. The pool keeps that: **lock acquirable + live pid ⇒ orphan, terminate; lock held + live pid ⇒ second gateway, refuse and name it, terminate nothing.** Revision 3's rule shoots the neighbour, and ADR §9.1 names this as the one correction the pool FRs need. The rest of revision 3's reasoning stands: **terminate, and the trade-off is recorded rather than buried.** A live pid named by a marker under **this** `$OMNIPUS_HOME` is either an orphan of a crashed gateway on this home (the common case) or a second gateway on the same home (already unsupported — §12 A11 — and already refused outright by the shipped single-Chrome path, `coordinator.go:1458-1467`). Nothing can distinguish them, and leaving it alive means unbounded host memory outside the cap, which defeats FR-038's only purpose. **Pid reuse is a real hazard** — a dead gateway's Chrome pid can be reused by an unrelated process — and the shipped code has it too (`pidAlive(pid)` plus an owner string read from the *marker file*, not from the process). Mitigation, stated honestly by platform: on Linux, confirm `/proc/<pid>/exe` resolves to the resolved Chrome binary before terminating. **On macOS and elsewhere there is no pure-Go equivalent** (Hard Constraint #2 forbids shelling out here), so on those platforms the marker is **removed without terminating** and a WARN names the pid so an operator can act — a smaller guarantee, stated rather than implied. |
| **A21** | **`max_browsers`' 0/negative semantics, and how three caps now relate.** | **⚠️ WHOLLY SUPERSEDED BY D1.5a — there are no caps to relate, and no edge semantics to define.** All three guards this row arbitrated are gone: `--renderer-process-limit` is never set (FR-055 tombstoned, and its removal is a **security improvement** — see A23), the derived instance target is deleted (FR-056 tombstoned), and `max_total_tabs` is removed from the code (FR-059). **The row is kept, not deleted, because two of its findings survive their subject.** (1) Its rejection of revision 3's memory argument still stands and now applies more widely: *a tab is not a process* — a cross-site embed can claim its own renderer, same-site embeds collapse into one — so **nothing may be priced per tab**, which is FR-062. The measured evidence has since gone further: **2 tabs against 13 renderer processes**, ~6 per tab. (2) Its "surprise" argument — that a configured ceiling must not be silently multiplied by N — is void only because there is no configured ceiling left to multiply; the *principle* survives as AC7 and SC-022, which forbid any message or doc string implying a setting that does not exist. Test 53 is tombstoned with the row's subject. |
| **A22** | **The whole-Chrome idle window's value — and what it costs once eviction exists.** | **DECIDED: `tools.browser.idle_close_ttl`, default 15 minutes — a reasoned default, not a measurement, and the spec says which.** Derivation: 3× the per-tab `tools.browser.idle_ttl` default of 5m (`manager.go:134`), so a Chrome closes only after its last tab has been gone for a further two tab-TTLs. The asymmetry is deliberate: closing the browser process reclaims a smaller fixed cost than reaping a renderer and pays a relaunch on the next use, so it should be less eager than tab reaping. *(Revision 3 supported this with "renderers are the dominant cost at 74–268 MB **RSS** each" — the metric ADR §8 retracts. The **conclusion** does not depend on the magnitude, only on the ordering: a renderer costs more than the marginal browser process. Restated on that basis, and the retracted figure is not quoted.)* **The sweep interval invariant applies:** the existing 1-minute ticker (`gateway.go:5322`) must stay well under this TTL, or the TTL becomes a floor rather than the lifetime. **⚠️ And one consequence that was harmless before eviction, and is load-bearing under D1.5a:** idle close is now **half the entire defence** (FR-061), not housekeeping — the two TTLs compose. A workspace browsed once holds its slot for the 5-minute tab TTL **plus** the 15-minute idle-close TTL — **about twenty minutes of occupancy after the last action**. **Under D1.5a that composition is no longer about a target — it is about how long memory stays occupied.** A workspace browsed once holds its memory for ~20 minutes after its last action, so the host's effective capacity is set by workspaces browsed *within any ~20-minute window*, not by workspaces browsing *right now*; D1.7's "ten workspaces on a three-browser machine" arithmetic reads optimistically if that is missed. With no counter behind it, a too-generous TTL now shows up as **memory refusals**, not as a slower reclaim. It also sets the floor for FR-054's thrash window: a workspace evicted before its idle TTL expires was, by this definition, still occupying its slot legitimately. **This is not FR-044's kind of number** and §5's "no value derived from an estimate" non-behaviour does not apply to it: that rule was about the target, whose wrong value costs host memory. This one's wrong value costs a relaunch — **but under D1.5a it is one of only two controls that exist**, so "input to capacity" understates it: a TTL long enough to never fire is indistinguishable from having no idle close at all, which FR-061 requires a test to catch. |
| **A23** | **`--renderer-process-limit` weakens site isolation above its bound. Is that acceptable here?** | **⚠️ REWRITTEN BY D1.5a — the question is DISSOLVED, not answered, and that is the better outcome.** Revision 4 answered *"acceptable, because R is a site-isolation **floor** derived from the tab count and never lowered for memory"*, having first retired the earlier justification (*"acceptable for agent-driven browsing of semi-trusted destinations"*). **D1.5a deletes the flag entirely**, so no bound exists and Chrome's default site-per-process isolation applies to every page. **The finding this row records is worth keeping verbatim, because it is what made the flag indefensible:** `BrowserManager.ValidateURL` (`manager.go:685-708`) blocks five schemes (`blockedSchemes`, `:675-683`) and private/metadata addresses via the SSRF checker (SEC-24), and **permits every other public `http(s)` URL, with no allow-list anywhere in `pkg/tools/browser/`.** An LLM-driven `browser_navigate` is by construction pointed at arbitrary URLs, from model output influenced by page content the agent just read. So *"semi-trusted destinations"* described nothing the code enforces. Under D1.10 the workspace browser holds the operator's live logins and under D1.9a an agent can be asked onto the operator's own tab — a signed-in bank tab and a page an agent found must never share a renderer. **Not setting the flag guarantees that unconditionally**, where revision 4's floor only guaranteed it below R. **C-303 / C4 / C206 are therefore DISSOLVED — no residual trade-off, no compensating control, nothing to revisit if an allow-list ever lands.** |
| **A24** | **Three ADR §6 "open" questions that this spec has already answered. Which way does each go?** The ADR lists them as undecided; a spec that quietly decides them leaves two documents disagreeing, which is the failure this whole round exists to end. | **One is REOPENED; two are KEPT with the ADR named as owner of closing them. Stated per item rather than in aggregate.** <br>**(a) "Is the capture session per workspace or per viewer?" — KEPT.** FR-016a decides per workspace, and the decision is **forced**, not chosen: one manager per workspace means one browser to capture, and ADR-048's "requesting agent" conflict rule has no referent once agents no longer have disjoint tab sets. `NewCaptureSession`'s doc (`capture_session.go:360`) still calls `mgr` "the **agent's** BrowserManager" and must change with it. **Outstanding ADR edit:** §6 should close this citing FR-016a. <br>**(b) "Who bounds per-workspace profile disk?" — REOPENED, and D1.5a makes the reopening unarguable.** §16 MAJ-111 closed it with *"live profiles are bounded by `max_browsers`, and dead ones are removed by the deletion path, so the unbounded case is closed by deletion rather than by a ceiling."* **That does not hold.** **There is no `max_browsers` at all now** (FR-056 tombstoned), so the closure has lost even its nominal subject. It never held anyway: `max_browsers` bounded live **processes**, not bytes — a profile's browser cache grows while the instance is live and **is not reclaimed when it is idle-closed or evicted** — the whole point is that the directory survives. So N workspaces browsed once each leave N unbounded caches on a host this project has filled twice (root-volume exhaustion is a documented hazard in the root `CLAUDE.md`). The closure is withdrawn; this matches the ADR's own §4 ("No quota is specified") and §6. **No quota is specified here either** — it needs a measurement and a policy, and inventing one would repeat the mistake. <br>**(c) "Does the cap count instances or bytes?" — CLOSED by D1.5a: bytes, and nothing else.** Revision 4 answered "both" — instances for the target (FR-056), bytes for admission (FR-057) — which was D1.5's item 2 / item 3 split. **D1.5a deletes item 2.** Nothing counts instances anywhere; the live-memory gate is the only admission control and it reads bytes. **Outstanding ADR edit:** §6 should close this row citing D1.5a, not D1.5 items 2 and 3. <br>All three are escalated in §0.5 E-4 rather than left to be discovered. |
| **A25** | **Does the operator's shared tab set need a lifecycle of its own?** An agent's tab set dies with the agent's membership; the workspace-owned set does not belong to anyone who can leave. | **DECIDED, narrowly, and the boundary is stated.** The workspace-owned `sessionEntry` is reaped by the **same** per-tab `ReapIdleSessions` rules as any other (FR-025) — it holds tabs, and an idle tab is idle regardless of who opened it. It is **not** deleted when an agent leaves the roster (it was never that agent's), and it **is** disposed with the workspace (FR-026, FR-043a). **What is NOT decided here:** whether an agent may *close* a workspace-owned tab it did not open. FR-048 says an agent cannot close another **agent's** tab; the operator's tab is a different question, and it is part of E-1's "take control on request" mechanism — a D2 tool-surface decision. Until then, treat closing the operator's tab as requiring the same acquisition as writing to it. |


**Corrections folded in above, listed so a reviewer can check them:** the ADR's `pkg/agent/loop.go:185` is `:279`; "six tool descriptions" is 5 model-visible strings + 2 Go comments + 1 unrelated SPA comment; `pkg/tools/base.go:241-251` is `:243-252`; `pkg/tools/resolvepath.go:695-709` is prose whose call is at `:713`; the viewer counters are cited by symbol (`ViewerAttached`/`ViewerDetached`) rather than by the two disagreeing line numbers the previous draft gave; `shared_control_test.go` has **eight** tests, not nine; the registered tool name is **`serve_web`** (`pkg/tools/web_serve.go:46`), not `web_serve`; `BrowserManagerForAgent` takes **one** argument today, not two.

**Round-4 corrections, added:** every ADR citation is re-derived against the consolidated numbering (§0.0) — `D1.1a`→`D1.4`, `D1.2`→`D1.10`, `D1.3`/`D1.4`→`D1.11`, `D1.5`→`D1.12`, `D1.0`→`D1.2`, `D1.0a`→`D1.3`; the per-key profile path is the **flat** `<profileRoot>/ws-<id>/`, not `<profileRoot>/ws/<id>/`, and under the flat form `InstallRootForProfileDir` resolves correctly, which dissolves revision 3's INVARIANT P-5 rationale; G-1 measures **PSS**, not RSS (`ps` cannot produce PSS); `LiveViewRegistry.IsControlled` is at `live.go:1313`, outside the `1236-1310` span the round-4 brief gave; `.github/workflows/pr.yml` **does** set `OMNIPUS_BROWSER_E2E: "1"` (`:416`) and **does** fail on either skip path (`:468-472`), so revision 3's argument for routing G-2 elsewhere was false, and that job's `passes -lt 180` floor (`:481`) is why G-2 needs its own step; `FilterToolsByPolicy` `continue`s past a deny verdict at `pkg/tools/compositor.go:436-438`, which is why FR-014a had no production caller; `BrowserConfig.MaxTabs` (default 5, `manager.go:36`/`:124`) is enforced by `totalTabCountLocked` (`:1549-1555`) across **every session in the manager**, which is what makes the re-key silently tighten it; SC-015's quoted attribution sentence does not exist in the consolidated ADR — the header's shorter, unenumerated form does.

**Round-2 corrections, added:** `ReapIdleSessions` **does** cancel browser contexts and **does** reach the coordinator — the previous draft's *verified* claim was false in both places it appeared (§2.1, §15 CRIT-006, and ADR §8's corrections log); the `DefaultSessionID` grep returns **57** lines (37 usages + 2 declarations + **18** comments, not 12), and `pkg/config/config.go:3892` was missing from the file inventory; the test-side surface is **364 references across 25 files**, previously unbudgeted; `sandbox_apply.go`'s removal note is at **`:412-417`**, not `:405-417`; the exempt tool set is **six**, not five/three/"five D2 tools"/"four D2 tools" — four different figures appeared in the previous draft; the action-tool timeout `leaseWaitTimeout` must stay under is `BrowserConfig.PageTimeout`, config key **`tools.browser.page_timeout`** (not `page_timeout_sec`, which is the Go field's suffix); heartbeat turns **do** carry a workspace, so FR-033's stated cost was overstated (§16 MAJ-113).

**The one ADR edit this spec was tracking is DISCHARGED.** Revision 3 carried, as an outstanding item for the ADR's owner, that *"D1.3's key table still reads 'Transcript session · `tools.ToolTranscriptSessionID(ctx)` · … · Used for: Unattended delegated work'"*. **The consolidated ADR has no key table at all**, and its only mention of that key is in **D1.10's deletion list**, reading in full: *"`tools.ToolTranscriptSessionID` as a browsing key. **Unused, and not a fallback.**"* That is precisely what §2.1 says. **§16 MIN-108 is marked discharged and the item is removed from the closing "Next" paragraph** — an outstanding item that has been done, but is still recorded as outstanding, gets re-raised every round, which is what happened here.

---

## 13. Holdout evaluation scenarios (post-implementation; NOT in the TDD plan or traceability)

1. **(happy)** Operator opens the live panel in a workspace chat with Mia, logs into a real site, switches the chat to Jim, asks "what's open?" — Jim names the page and can act on it. The verbatim ADR §1.1 conversation, re-run.
2. **(happy)** Operator opens a *new* chat in the same workspace the next day and asks Ray to check the same site — still logged in, no re-auth.
3. **(edge)** Two workspaces for two clients, both logged into the same SaaS with different accounts. Each workspace's agents see only their own account, and nothing in either UI hints at the other.
4. **(error, REQUIRED UAT — and its expectation is now the HONEST one)** Operator asks Mia (policy-denied) what is open. **She answers from absence** — she has no browser tool, was never shown one, and cannot distinguish "I may not" from "there is nothing". **Transcript recorded and attached to the PR anyway**, because this is the ADR's own headline defect surviving in a narrower form (ADR §6) and the transcript is the evidence that it is still there. *Revision 3's expectation — that she says she is not permitted — is unreachable: `FilterToolsByPolicy` `continue`s past the deny verdict (`compositor.go:436-438`) and the tool never reaches her. A UAT whose pass condition cannot occur is worse than no UAT.* The one thing this holdout **does** now check for is a regression: she must **not** claim the browser is *shared across the workspace* (FR-015's literals), because that string she can still emit.
5. **(error)** A heartbeat for a custom agent on no workspace runs a browser step — it fails with `ErrNoBrowsingContext`'s named text, the log shows the refusal, and no Chrome was launched.
6. **(happy, the inverted case)** Operator asks Jim to delegate a research task to Ray and then closes the browser panel. Ray's sub-turn browses the workspace's signed-in session and completes — **it does not hit a login wall.** This is the D1.10 ruling's whole point, and it is the scenario the previous design deliberately broke.
7. **(edge)** The same delegation, but the task reaches an `ask`-policy tool. It is refused promptly with a reason, and the turn does not hang (#659).
8. **(edge)** Two agents on one workspace browse different sites simultaneously. Both complete; neither errors; the transcript shows at most one deferral apiece and no interleaved page state.
9. **(edge)** Operator saves an unrelated Setting mid-browse. Each workspace's Chrome pid is unchanged, the logins survive, and the panel keeps streaming.
10. **(edge)** Operator deletes a workspace whose browser had tabs open. That Chrome closes, other workspaces are unaffected.
11. **(edge)** An agent is added to a second workspace mid-session. Its next turn in the original chat still resolves to the original workspace (the chat's `workspace_id` wins) — no silent browser swap. Its next *heartbeat* on **each** workspace resolves to **that** workspace's browser, because a heartbeat job carries its workspace in its own name (§6, US-6/AC0) — **not** refused as ambiguous. *(This holdout previously asserted the refusal, which was the wrong expectation; round-2 MAJ-113.)* A **plain, operator-created schedule** for that same agent, which carries no workspace at all, **is** refused as ambiguous (FR-033) — that is the case worth watching for.
12. **(edge, the pool — REWRITTEN for eviction)** Operator browses in as many workspaces as the host has room for, leaves them **idle**, then starts work in one more. **Nothing is refused and nothing is said.** The new workspace opens. They then return to the workspace that had been idle longest: it reopens, **still signed in**, after a visible pause. *That pause is the entire operator-visible cost of eviction, and this holdout exists to confirm it is a pause and not a logout.*
12a. **(edge, the pool — the pinned case, REWRITTEN by D1.5a)** The same operator instead **keeps a tab and a live panel open in every one of those workspaces**, then starts work in one more, on a host with no headroom left. **Nothing opens.** After a pause the call fails with a message saying the host is out of memory and suggesting they close a browser or a panel they are done with — and naming **no setting to raise**, because there is none. Closing one panel and retrying works. *(Revision 4's holdout had the pool start one extra instance — the `+1` overshoot — and only refuse the second such request. D1.5a deletes the overshoot: exceeding real free memory risks the OOM killer taking the gateway, and there is no soft cap left to break instead.)* **Nothing they had open closed.** *(The pinned-everywhere setup is the point: with idle browsers eviction resolves it silently and the operator never sees a refusal at all.)*
12b. **(edge, the pool — thrash)** Operator moves between four workspaces on a host with room for about three browsers, browsing each in turn for a minute. Everything works, each switch pays a pause — and **exactly one** WARN appears naming the workspaces contending, naming **memory** as the constraint, and naming something they can actually do (add memory, or browse fewer at once). *A second WARN, or none, both fail this — and so does a WARN that names a config key, since none exists.*
13. **(edge, the pool — REWRITTEN by D1.5a; the holdout is now the OPPOSITE observation)** The operator goes to Settings looking for the browser or tab limit the thrash WARN implied, **and finds none** — no `max_browsers`, no `max_tabs`, no `max_total_tabs`, and no doc string claiming one exists. They then free memory on the host (quit an unrelated application) and retry: the pool grows, without a restart and without disturbing anything that was open. *(Revision 4's holdout had them raise `tools.browser.max_browsers` and watch for the case where the derived target, not the ceiling, was binding — an operator doing exactly what a WARN told them and seeing no effect. **D1.5a removes both terms**, so that specific confusion is gone; what replaces it is the risk that a message still speaks as if a setting existed. This holdout is the human check on SC-022: if the operator goes looking for a knob, some message sent them there.)*
14. **(error, the pool)** One workspace's Chrome is killed from outside (Activity Monitor / `kill`). That workspace's panel shows an error and recovers on next use with its login intact; every other workspace keeps streaming without interruption.
15. **(edge, memory)** Leave the gateway running with several workspaces browsed and then idle overnight. Chrome process count returns to zero, RSS returns to baseline, and the next morning's first browser call in any of those workspaces is still logged in.
16. **(error, crash recovery)** Operator kills the gateway with `kill -9` while three workspaces have live browsers, then starts it again. Activity Monitor shows **no** Chrome left from the previous run; each of the three workspaces' first browser call afterwards works and is still logged in — no "profile in use" failure.
17. **(edge, deletion)** Operator deletes a client's workspace, then inspects `~/.omnipus/browser/profiles/`. That workspace's `ws-<id>` directory is gone, and the other workspaces' are untouched. They can answer the client's "is my data deleted?" with yes.
19. **(happy, tab ownership — the ruling's own scenario)** Operator asks Mia to look something up; Mia opens a tab. The operator then opens a tab of their own in the panel and browses. They switch the chat to Jim and ask "what's open?" — **Jim names the operator's tab and not Mia's**, and can be asked to take it over. Mia, asked the same, names her own tab and the operator's. *This is ADR §1.1's conversation with the right answer, and it is the holdout that fails if FR-048 was skipped.*
20. **(edge, tab ownership)** With `tools.browser.max_tabs` at its default of 5, Mia opens five tabs. Jim, on the same workspace, opens one — **it succeeds**. *If it fails with a max-tabs error, the re-key turned a per-agent cap into a per-team one (FR-049).*
21. **(edge, upgrade)** An operator upgrading an install where they were signed into a site: after the upgrade, every workspace is **logged out** and the release note said so. `~/.omnipus/browser/profiles/default/` is still on disk, untouched. *Nobody inherited anyone else's session.*
22. **(edge, audit)** After a delegated agent completes a task involving ten browser actions on a signed-in site, the operator opens Settings → Security → Audit Log. **The log renders** (it is not blank), and it shows ten entries naming the agent, the tool and the host — not one. *A blank viewer here is #667's failure mode and means an event name carried a dot.*
18. **(edge, disclosure)** Operator adds an agent to a workspace that is signed into a real site. Before confirming, the UI tells them the agent will be able to act as that signed-in user, including on turns nobody is watching. They can decide with that in front of them.

---

## 14. Annex — the write lease (NORMATIVE; the D2 spec references this, and must not restate it)

**Scope, and it changed on 2026-09-01 — read this before the API.** ADR **D1.9a** rules that tabs stay per agent and only the **operator's** tab is shared. Two agents on their own tabs cannot reach the same page, so **the general contention case this annex was written for no longer exists.** What survives is one case:

| Contention | Arbiter | Status |
|---|---|---|
| **Operator vs agent**, any tab | `LiveViewRegistry.TakeControl` / `IsControlled` (`live.go::TakeControl` `:1241`, `::IsControlled` `:1313`), ADR-038 D6 | **Unchanged.** Not this annex's business. The `{"deferred": true}` shape and its reason text are untouched, so no prompt needs rewriting |
| **Agent vs agent**, on the **operator's** workspace-owned tab | **§14's write lease** | The only case the lease arbitrates |
| **Agent vs agent**, each on their own tab | *nothing — it cannot occur* | Structurally impossible under D1.9a (FR-048). US-9/AC0 asserts it, so a future change that re-merges the tab sets fails the concurrency suite as well as US-22/AC3 |

**The practical effect of the rescope is that the lease is reached far less often, not that it is weaker.** The primitive below is unchanged; what changes is that `leaseWrite` is only called when the resolved `TabOwner` is `TabOwnerWorkspace()` (FR-021), and that the contended path is now narrow enough to be exercised deterministically in a test rather than raced for.

**Ownership.** ADR-072 files the lease under D2.10, but ADR §4 calls it "the largest open risk **in D1**", and it is D1's re-key that creates the contention: before D1, two agents on one workspace had two browsers and could not collide. **Operator ruling, 2026-08-31: the lease belongs to this spec.** The sibling D2 spec had specced it independently, with an incompatible signature, over the same call sites — if both had landed, the seven action tools would have taken two unrelated mutexes and mutual exclusion would have been lost for whichever tool took only one. That is nondeterministic interleaving, which ADR §5 calls the most expensive failure class for an agent.

**Required action in the D2 spec** (tracked, not assumed): delete its Stream F lease scope, its FR-023, its US-14, its lease BDD scenario and its `TestLeaseWrite_SecondWriterDeferred`, and replace them with a reference to FR-019…FR-024 and FR-019a here.

### 14.1 API

```go
// pkg/tools/browser/lease.go (new)

// leaseWaitTimeout bounds how long an action tool waits for the browser's write
// lease before reporting contention. Config key: tools.browser.lease_wait,
// default 2s, reload-applied via ApplyRuntimeConfig. (Revision 4 said "like
// max_total_tabs"; that key is deleted by D1.5a/FR-059 — the reload MECHANISM
// survives it, only the example key is gone.)
//
// Relationship to the action-tool CDP timeout (MIN-007): it MUST be strictly
// less than the shortest action-tool timeout. If it were longer, a waiting tool
// could exhaust its own deadline inside the wait and return a CDP timeout
// instead of a deferral — an error where the contract requires a non-error.
//
// THE TIMEOUT IS NAMED, AND THE RELATIONSHIP IS ENFORCED RATHER THAN COMMENTED
// (round-2 MAJ-112). It is BrowserConfig.PageTimeout (manager.go:35, default
// 30s at :123), operator-settable as tools.browser.page_timeout
// (config.go:3632's PageTimeoutSec, applied at loop.go:2311-2312). BOTH values
// are operator-configurable and the previous draft validated neither, so an
// operator could set lease_wait=45s against page_timeout=30s and turn every
// contended call into a CDP timeout ERROR where FR-020 requires a non-error
// deferral. TestWriteLease_BoundedWait only ever asserted the defaults.
//
// FR-023a: at config load AND on reload, leaseWaitTimeout is CLAMPED to
// min(configured, PageTimeout/2) with a WARN naming both keys and both values.
// Clamping rather than rejecting, deliberately: aborting boot over a browser
// tuning key is disproportionate. The WARN is part of the requirement — a
// silent clamp leaves the operator believing a setting took effect that did
// not. Asserted by test 50, at load and at reload.
//
// WHAT THE CLAMP IS FOR, RESTATED (D2.10, and revision 3 had this inverted).
// Revision 3 said the clamp existed "to guarantee a deferral rather than an
// error". Under D2.10 that is backwards: the loser RETRIES INSIDE THE TOOL and
// BOTH WRITERS EVENTUALLY COMPLETE; a deferral is the OUTCOME PAST THE BOUND,
// not the goal. The clamp's real job is to keep the whole retry window inside
// the tool's own CDP deadline, so a contended call finishes its retries and
// SUCCEEDS rather than being killed mid-wait by PageTimeout. It is a budget
// for retrying, not a guarantee of giving up early.
var leaseWaitTimeout = 2 * time.Second

// leaseClock is the test seam for the bounded wait. Production is the real
// clock; tests substitute a fake. Named here because the previous draft
// required a "fake clock" without saying what it was attached to.
type leaseClock interface {
    Now() time.Time
    After(d time.Duration) <-chan time.Time
}

// acquireWrite is the single lease primitive in pkg/tools/browser. There is
// exactly one acquire symbol in the package; a structural test asserts it.
//
// It is a per-BROWSER (not per-manager-mutex) mutual exclusion held for the
// duration of ONE action-tool call. It is NOT m.mu and must never be taken
// while m.mu is held — an action tool blocks on CDP for seconds, and the
// ADR-038 "no lock across a blocking call" discipline forbids it.
//
// It is reached ONLY when the resolved TabOwner is TabOwnerWorkspace() — the
// operator's shared tab (D1.9a, FR-021). On an agent's own tab set there is
// nothing to arbitrate and no lease is taken.
//
// Returns:
//   ok=true                 -> caller holds the lease; MUST defer release()
//   ok=false, holder="jim"  -> the RETRY BOUND was exhausted; the caller
//                              defers via deferredResult(...)
//
// RETRY, NOT IMMEDIATE DEFERRAL (D2.10). Within leaseWaitTimeout it retries
// with backoff, cancellable by ctx so a cancelled turn parks no goroutine.
// The contract the caller relies on is that BOTH writers eventually complete
// under ordinary contention; ok=false is the bounded fallback, not the normal
// outcome. ADR criterion 16 is explicit that asserting only "neither errors"
// would pass when nothing happened, so the tests assert completion.
//
// The bound is a RETRY BUDGET, not a give-up timer — see leaseWaitTimeout's
// comment on FR-023a above.
func (m *BrowserManager) acquireWrite(
    ctx context.Context, key BrowsingKey, holderAgentID string,
) (release func(), ok bool, holder string)

// leaseWrite is the convenience wrapper every action tool actually calls. It
// adopts the D2 spec's pre-built-*ToolResult* shape so D2's FOUR leased new
// action tools (select_option, press_key, hover, upload_file — its other two,
// snapshot and handle_dialog, are exempt; see §14 rule 3's normative counts)
// do not hand-roll the deferral body, while keeping D1's stronger primitive
// underneath (cancellable, bounded, names the holder).
//
//   deferred, release := leaseWrite(ctx, mgr, key, agentID, "browser_click")
//   if deferred != nil { return deferred, nil }
//   defer release()
//
// deferred is nil iff the lease was acquired. When non-nil it is a NON-error
// result whose body is {"deferred": true, "reason": <text naming the holder>}.
func leaseWrite(
    ctx context.Context, m *BrowserManager, key BrowsingKey, agentID, toolName string,
) (deferred *tools.ToolResult, release func())
```

### 14.2 Rules

1. **Composition order is fixed, and one branch is now short-circuited by ownership.**
   1. **Ownership first.** If the resolved `TabOwner` is an agent's own set, neither gate is reached — `controlledResult` still applies (a human can take the wheel on any tab, ADR-038 D6) but the **lease is never consulted**, because no second agent can address that tab set (D1.9a, FR-021, FR-048).
   2. **`controlledResult` next.** A human holding the wheel outranks an agent queue (ADR-038 D6). When a human holds control the lease is **never acquired** (FR-022), and the result is the existing `{"deferred": true, "reason": …}` shape with its existing text — **unchanged, so no prompt needs rewriting.**
   3. **`leaseWrite` last**, and only on `TabOwnerWorkspace()`.
   **The two gates' outcomes are no longer symmetrical, and that is deliberate.** The human-control branch defers immediately, because a human is present and the agent is meant to stop. The agent-vs-agent branch **retries and expects to succeed** (FR-020), because there is no such reader: a model that treats a non-error `deferred` as success continues against a stale page, which is the silent-no-op failure D2.10 exists to prevent. Same shape, different semantics, and the reason text must distinguish them.
2. **`controlledResult` must ask about the resolved key** (FR-002c). It currently asks `IsControlled(defaultSessionID)` (`tools.go:963`); left unchanged, it returns `false` forever once the live registry is re-keyed, and the human control lock silently stops working — a regression of a shipped safety property.
3. **Membership is a rule — and this is the rule that actually holds** (FR-019a).

   > **THE NORMATIVE COUNTS, stated once and referenced from everywhere else: the exempt set is SIX. Eleven tools are leased — seven shipped today plus four of D2's six new ones. D2 adds SIX tools, of which four are leased and two are exempt.**
   >
   > *(The previous draft stated these four different ways in one document — "a closed, named set of **five**" here, "a closed set of **three**" in §15's MAJ-008 disposition, "D2's **five** new action tools" in §14.1's `leaseWrite` comment, and "D2's **four** remaining new action tools" in this rule's own closing sentence. Round-2 MAJ-101. Every one of those figures is now derived from the table below rather than restated.)*

   **The rule:** a `browser_*` tool takes the write lease **if and only if** it is gated by the ADR-038 D6 human-control lock — that is, if and only if its `Execute` calls `controlledResult`.

   **Why this rule and not "does it mutate".** The previous draft's rule was *"every tool that mutates page or tab state acquires the lease; the exemption list is exactly the five"* — and `browser_handle_dialog` **mutates page state** (it accepts or dismisses a modal) while being on the exemption list. The rule contradicted its own exemption, so no test could classify by it; test 18 could only check membership of a hand-written list living in this same document, which is the list-driven test MAJ-008 was raised to eliminate. That list had already been widened 3→5 the same day under pressure from the sibling D2 spec, with no stated criterion separating a legitimate widening from a convenient one (round-2 MAJ-102).

   The control-lock gate is a **different** and better classifier: it already exists in shipped code, it was decided in ADR-038 D6 on the same question ("may an agent do this while a human is driving?"), and it partitions the eleven shipped tools **exactly** the way the lease needs to. One classification, two consumers, no second list to drift.

   | Tool | `controlledResult`? | Lease? | Evidence / why |
   |---|---|---|---|
   | `browser_navigate` | yes (`tools.go:119`) | **leased** | mutates the page |
   | `browser_click` | yes (`tools.go:232`) | **leased** | mutates the page |
   | `browser_type` | yes (`tools.go:429`) | **leased** | mutates the page |
   | `browser_evaluate` | yes (`tools.go:879`) | **leased** | arbitrary JS |
   | `browser_switch_tab` | yes (`tabs.go:113`) | **leased** | mutates tab state |
   | `browser_close_tab` | yes (`tabs.go:171`) | **leased** | mutates tab state |
   | `browser_open_tab` | yes (`tabs.go:239`) | **leased** | mutates tab state |
   | `browser_screenshot` | no | **exempt** | read-only; ungated today |
   | `browser_get_text` | no | **exempt** | read-only; ungated today |
   | `browser_wait` | no | **exempt** | read-only; ungated today |
   | **`browser_list_tabs`** | **no** (`tabs.go:20`: *"Read-only — NOT gated by controlledResult"*) | **exempt** | **The omission that would have broken the headline demo.** It is registered (`register.go:76`), read-only, and was in neither of the previous draft's categories — so under the old AC5 it would have taken the **write** lease, making Jim's `browser_list_tabs` (behavioural contract 1, US-1/AC1, the headline BDD scenario) return `{"deferred": true}` behind another agent's long `browser_navigate`. Round-2 CRIT-104 |
   | `browser_snapshot` (D2 FR-018) | no (D2 must keep it so) | **exempt** | read-only — falls out of the rule, needs no exception |
   | `browser_handle_dialog` (D2 FR-035) | **no — D2 must register it ungated** | **exempt** | **Recovery, exempt from BOTH gates and that is what makes it coherent.** A JS modal blocks the renderer, so the panel's own `Input.dispatch*` injection is blocked too: a human holding the wheel is **equally** unable to clear it, and only `Page.handleJavaScriptDialog` can. Exempting it from the lease but not the control lock would leave the tab wedged for the human as well. See §12 A17 |
   | `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_upload_file` (D2) | yes (D2 must gate them) | **leased** | mutate the page; automatically in scope by the rule, with no edit here |

   **What test 18 checks, and why it can now fail for the right reason.** It enumerates the **registry** and exercises each registered `browser_*` tool twice — once with a human holding the control lock, once with another agent holding the write lease — asserting the two deferral answers **agree** for every tool. That is the biconditional, checked behaviourally against shipped code. It is no longer possible to make it green by editing a list in this document; making it green requires changing a tool's actual gating.

   **The one cross-spec obligation is now an ADR ruling, not a request.** D2 must register `browser_handle_dialog` ungated by `controlledResult`, and must gate its four action tools. **ADR-072 D1.8 decides the first directly** — *"`browser_handle_dialog` is exempt from the write lease **and** from `controlledResult`… a modal blocks every CDP command on that tab, including the live panel's own input injection, so the human at the wheel is equally stuck… This narrows ADR-038 D6's exclusivity by exactly one tool"* — with the same reasoning §12 A17 reached independently, plus the ADR-038 narrowing that only the ADR can authorise. So the biconditional's one awkward member is ruled, not assumed, and D2 registering it gated would contradict the ADR rather than this spec.

   **The lease is reached only on the operator's tab (D1.9a).** The table above says which tools *would* take the lease; FR-021 says *when* the question is asked at all. On an agent's own tab set no `browser_*` tool consults the lease, because no other agent can address that set. Test 18's biconditional is therefore exercised against `TabOwnerWorkspace()`, where both gates are live — running it against an agent's own tabs would show every tool "not deferring under the lease" for a reason that has nothing to do with its gating, and would pass with the classification completely wrong.
4. **`release()` is idempotent and MUST run via `defer`** in every leased tool, so a panic, a CDP timeout or a cancelled context cannot wedge the browser (FR-024).
5. **Lock order** is `writeLease → pool.mu → m.mu`, never reversed; `m.mu` is never held across `acquireWrite` or any CDP call.
6. **In-process only** (FR-030). The lease deliberately does not use `fileutil.WithFlock`, which is a documented no-op on Windows (`pkg/fileutil/flock_windows.go`) and would give a false cross-process guarantee. Two gateways on one home are out of scope — §12 A11.
7. **No fairness guarantee** under sustained contention (§12 A8). The bound is what is promised, and FR-023 tests it.

---

## 15. Round-1 review disposition (all 29 findings of `…-spec-review.md`)

| ID | Disposition | Where / evidence |
|---|---|---|
| **CRIT-001** — isolation is off by default; enabling it breaks the live panel | **Superseded by ruling, then fixed.** The operator rejected the trade; ADR D1.4 replaces CDP-context isolation with **Chrome-process + profile isolation**, which delivers both. The finding's diagnosis was correct and is preserved verbatim in §0.1 with its evidence | §0.1, FR-031, FR-037, FR-045; gate G-2 |
| **CRIT-002** — tools hold a manager bound at registration | **Fixed.** `register.go:41-84` and the 11 `mgr:`-bearing structs added to §2.1; `ManagerResolver` added to the interface contract; FR-002a; a **structural** test (no `*BrowserManager` field) and an **end-to-end** test through `registerSharedTools` that is red today | §2.1, §3.1, FR-002a, tests 4 + 38, SC-001 |
| **CRIT-003** — reload prune keys off agent ids | **Fixed.** `loop.go:2849-2871` verified; FR-026a makes the predicate the live-key set with an explicit liveness definition (§12 A12); FR-026b makes registration idempotent per key; the reload BDD now specifies **two agents** and asserts `pool.Close` count **zero** | FR-026a, FR-026b, tests 32/33/42, US-17 |
| **CRIT-004** — the lease is double-specified with incompatible APIs | **Fixed.** §14 is the single normative definition, per operator ruling. It keeps D1's stronger primitive and adopts D2's pre-built `ToolResult` convenience, so neither team rewrites call sites. A structural test asserts one acquire symbol. The required D2-spec deletions are named | §14, §12 A7, FR-019…FR-024 |
| **CRIT-005** — `controlledResult` and ~15 gateway sites still use the constant; the nominated guard cannot catch it | **Fixed, and the enumeration was larger than reported.** §2.2 counts **37** executable references (13 in `browser_ws.go`, not ~15, plus 24 elsewhere the review did not reach) plus 12 comments. FR-002b deletes the constant; FR-002c re-keys `controlledResult` and is assigned to **Stream A**, not Stream D. Regression list corrected: `shared_control_test.go` is **8** tests and is **not** the guard; `tools_control_test.go` (3 tests, `:59/:106/:153`) is | §2.2, FR-002b, FR-002c, §10.1, SC-009, SC-013 |
| **CRIT-006** — unattended contexts have no disposal path | **Re-scoped by the D1.10 ruling, and the underlying leak is closed — but this row's own evidence was false and is corrected here.** Sub-turns no longer create anything to leak (FR-009, FR-026c). ⚠️ **The previous wording of this row claimed, marked *verified*, that `ReapIdleSessions` "deletes `m.sessions` entries and never disposes a browser (its only removal is `delete(m.sessions, sessionID)`)". That was FALSE** (round-2 CRIT-102; ADR-072 §8 records it). It cancels `se.browserCancel` in both removal branches (`manager.go:3027-3032`, `:3073-3078`, executed `:3123-3125`), cancels per-tab contexts (`:3106-3107`) and reaches `coord.ReleaseTab` via `releaseGlobalTab` (`:3118` → `:3358-3365`). **The narrow true claim:** it never calls `RemoveAgent` or `disposeBrowserContextRaw`, so the Chrome **process** and the coordinator's per-key state are untouched. Whole-Chrome idle close is therefore still new work (FR-040), but it composes with an existing disposal path rather than filling a void — which is why FR-040a now specifies the reaper↔pool contract instead of assuming there is nothing to contract with | FR-009, FR-026c, FR-040, **FR-040a**, tests 31 + 41 + **55** |
| **MAJ-001** — the ladder cannot be evaluated in the order it specifies | **Fixed by deletion.** The step that could not run first (the attendance check) is withdrawn. The ladder is now three unambiguous rungs with no forward reference | §3.1 `resolve.go`, FR-007 |
| **MAJ-002** — "attended" is a proxy that does not implement the ruling | **Moot.** The ruling it failed to implement was itself reversed; there is no attendance concept | §0.2, §12 A2/A3 |
| **MAJ-003** — a fresh install has no workspaces | **REJECTED on the central claim; residual accepted.** *"Nothing seeds a workspace on a fresh install"* is false: `ensureDefaultWorkspace` (`pkg/gateway/rest_workspaces.go:468`) runs on **every** boot (`pkg/gateway/gateway.go:5013`) and creates "My Workspace" with `defaultWorkspaceTeam(cfg)` = `coreagent.All() ∩ configured agents` (`pkg/gateway/rest_workspace_delegation.go:359-379`) — which includes **Jim and Ray**, the two browser-policy-allowed agents. A fresh install resolves. **Accepted residual:** a *custom* agent is deliberately never auto-added to a team (`gateway.go:5018-5025`), so it resolves to nothing — and the panel's message for that case is misleading. FR-008a and US-14 fix the message; no workspace seeding is added | §6, US-14, FR-008a |
| **MAJ-004** — the "no wire change" claim passes only because SC-007 measures shape | **Fixed.** The claim is restated honestly; SC-007 gains a third, **behavioural** condition (test 46) that fails if `session_id` is not binding; the two replacement descriptions must match FR-016's verbatim text; `BrowserInspectRequest` is decided explicitly (its `session_id` is a *browser* session id, so it gains no chat semantics and resolves from the agent under FR-033). The `browser_started`→`state` persisted-JSON question is confirmed safe — `grep -rn "browser_started" src/` returns nothing | SC-007, US-10/AC3+AC4, FR-016, §6 SPA |
| **MAJ-005** — ADR criterion 3b has no automated coverage | **Fixed by (a) + (b).** FR-014a strengthens the artefact so the denial for a `browser_*` tool **names the browser surface** rather than the system-wide generic `"Tool execution denied by policy."` (`tool_denial.go:206-210`), and test 10 asserts that string. Holdout 4 is promoted to a **required UAT with a recorded transcript** (US-8/AC3), and §11 says plainly that no automated test observes what the model says | **SUPERSEDED — ADR D1.12 withdraws criterion 3b; see §17 C3.** FR-014a, test 10 and US-8/AC2+AC3 are tombstoned; holdout 4 is rewritten to record the honest outcome |
| **MAJ-006** — the boot warm path is broken and unmentioned | **Fixed.** `pickWarmBrowserManager` (`gateway.go:3373`, called `:3562`) added to §2.1 as **modifies**; FR-016b requires it to warm the resolved key and to skip with a single INFO (not WARN) when nothing resolves; test 24 | §2.1, FR-016b, test 24 |
| **MAJ-007** — the capture registry has scope but no requirement | **Fixed.** FR-016a re-keys `captureRegistry` (`browser_webrtc.go:70-78`) to the browsing key and states the consequence: **one capture session per workspace browser**, and ADR-048's "requesting agent" conflict rule collapses because agents no longer have disjoint tab sets | FR-016a, test 25, §6 |
| **MAJ-008** — the leased set is a closed enumeration that D2 will grow | **Fixed, and this row's own count was stale — corrected (round-2 MAJ-101).** FR-019a replaces the enumeration with a rule plus a registry-**enumerated behavioural** test. **The exemption is a closed set of SIX, not three** (§14 rule 3 holds the normative counts); D2 adds **six** tools, of which **four** are leased automatically by the rule and two are exempt. This row previously said "three" while §14 said "five" and §14.1 said "five D2 tools" — a disposition table asserting a fix the annex had since changed | FR-019a, §14 rule 3, test 18 |
| **MAJ-009** — `browserCtxIDs` as a map is dead structure | **Accepted, and then superseded.** The review's preferred fix (keep the single field, add `m.key`) was right; the D1.10 ruling then removed the second key shape entirely and FR-031 retires CDP contexts altogether, so `browserCtxID` is neither a map nor used. The map is not built | §0.2, FR-031 |
| **MAJ-010** — D1 creates the scenario that makes #659 dangerous | **Fixed, and more urgent under the new ruling.** With delegated sub-turns now sharing a *signed-in* browser, an `ask`-policy hang is worse, not better. FR-032 makes auto-deny a **D1** requirement rather than a D2 assumption; §6 records #659 as a prerequisite; US-5/AC3 and test 30 cover it | §6, FR-032, US-5/AC3, test 30 |
| **MAJ-011** — sorted-first tie-break selects a cookie jar | **Fixed, with the stronger option.** FR-033 **refuses** rather than arbitrating: for a filesystem mount the worst case is the wrong directory; for a browser it is acting as the wrong signed-in identity. The WARN is required on **both** resolution paths (`FindForAgentPreferring`'s fast path suppresses it today, `find_for_agent.go:168-176`). Cost recorded: a multi-workspace agent's heartbeat loses the browser rather than guessing | FR-033, US-6/AC3, US-11/AC2, test 3 |
| **MIN-001** — `shared_control_test.go` is 8 tests, not nine | **Fixed.** Verified: `:35, :55, :74, :92, :109, :138, :157, :186` | §10.1 |
| **MIN-002** — `tools_control_test.go` is absent from the regression list | **Fixed.** Added, with its three test names and line numbers, and made SC-009's guard | §10.1, SC-009 |
| **MIN-003** — the spec says `web_serve` twice | **Fixed.** Both occurrences now `serve_web`, with the evidence (`pkg/tools/web_serve.go:46`, `const ToolNameWebServe = "serve_web"`) | §1 out-of-scope, §5 |
| **MIN-004** — viewer-counter line numbers disagree with each other | **Fixed.** Cited by symbol (`ViewerAttached`/`ViewerDetached`) per the root `CLAUDE.md` rule, with a stated citation policy at the top of the file | §2.1, citation policy |
| **MIN-005** — FR-015 requires "corrected" strings without specifying them | **Fixed.** FR-034 specifies the replacement literals, and test 9 asserts the new text is present rather than only that the old text is gone. With one key shape, "shared across this workspace" is now accurate for every case | FR-034, test 9 |
| **MIN-006** — A10 unresolved with no gate | **Fixed.** Promoted from a table footnote to **SC-015, a blocking prerequisite** | SC-015, §12 A10 |
| **MIN-007** — `leaseWaitTimeout` has no config key, no timeout relationship, no named clock seam | **Fixed.** §14.1 names the config key (`tools.browser.lease_wait`), states the required relationship to the action-tool CDP timeout (strictly less, with the reason), and names `leaseClock` as the seam | §14.1 |
| **MIN-008** — `loop.go:270-279`'s standing comment is not addressed | **Fixed.** FR-002d requires the comment be **replaced**, not deleted: the map stays a map (the gateway still needs a specific manager), and the comment must say the key is now the browsing key | FR-002d |
| **MIN-009** — SC-003 asserts key construction, not context creation | **Fixed.** FR-035 adds the behavioural assertion — every `pool.Acquire` key traced to a `ResolveBrowsingKey` return in the same turn — and SC-003 is rewritten around it | FR-035, SC-003, test 34 |
| **OBS-001** — the three-state enum is largely a rename | **Partially accepted; enum retained with reasoning.** The observation is fair: the code already distinguishes two states via `browser_started` (`tabs.go:58,66`). **Retained anyway** because the alternative (`browser_started` + a second `tabs_empty` boolean) is two booleans with four representable combinations of which three are legal — a shape that cannot be closed, so SC-006's "no fourth value for any input" would be untestable. A closed enum makes the falsifiability claim real. Recorded rather than silently kept | US-7/AC4, SC-006 |
| **OBS-002** — three new concepts where one would do | **Accepted.** Two of the four are gone: `BrowsingKeyKind` is not built (one key shape), and the `browserCtxIDs` map is not built. `ViewerCounter` is also gone; `BrowserManager.Viewers()` survives as a plain accessor the reaper and idle-close need. Net new types: `BrowsingKey`, `TabState`, `BrowserResolveOutcome`, `ManagerResolver`, plus the pool — the last of which is new *scope*, not surplus abstraction | §0.2, §3.1 |
| **OBS-003** — the lease has three homes and should get a fourth | **Partially accepted.** The diagnosis (three homes) is right and the ruling settled it, but a separate spec file would make it a fourth. Instead it is a **§-numbered annex inside this spec** (§14), which the D2 spec references — the review's own suggested shape, kept in one file so D1 and D2 still ship independently | §14, §12 A7 |

---

## 16. Round-2 review disposition (all 29 findings of `…-spec-review-round2.md`)

Every code claim below was re-derived from source on this worktree on 2026-08-31 rather than taken from the review — which is the practice CRIT-102 exists to enforce. Where the review's own evidence is wrong or narrower than stated, this table says so with the counter-evidence.

> ⚠️ **Four rows below have been SUPERSEDED by operator rulings that postdate them** — CRIT-103, MAJ-005 (via §15), MAJ-108 and MAJ-111 in part. Each carries a superseding note **in place**. A disposition table left reading as current after its decision was reversed is how revision 3 came to say *"§5 forbids eviction and that stands"* about a design the operator had already overturned; the notes are inline for that reason, not appended.

### CRITICAL

| ID | Disposition | Where / evidence |
|---|---|---|
| **CRIT-101** — §10.1's "unmodified" vs FR-002b's deletion; 364 unbudgeted test references | **ACCEPTED IN FULL. Both numbers reproduce exactly.** `grep -rc … --include "*_test.go"` returns **364 across 25 files**; the four files §10.1 named hold **115** between them (41/33/26/15). §10.1's bar is rewritten from *unmodified* to **semantics unmodified**, with a five-point diff standard; FR-002e budgets the migration, assigns it to **Stream A** (not Stream G, because it is a compile dependency of FR-002b), requires it in the **same commit** as the deletion, and forbids a test-only alias; test 5 and SC-013 go repo-wide including `_test.go` so the alias loophole is measurable | §2.2a, FR-002e, §10.1, test 5, SC-013 |
| **CRIT-102** — the `ReapIdleSessions` claim is false, twice, marked *verified* | **ACCEPTED IN FULL, and the underlying gap is now specified rather than assumed away.** Re-verified: `reapedBrowsers` collects `se.browserCancel` at `manager.go:3027-3032` and `:3073-3078`; `cancelBounded(rb.cancel, …)` executes at `:3123-3125`; per-tab cancels at `:3106-3107`; `m.releaseGlobalTab()` at `:3118` → `coord.ReleaseTab(agentID)` at `:3364`. Both occurrences corrected (§2.1's row and §15's CRIT-006 row) to the narrow true claim: it never calls `RemoveAgent` or `disposeBrowserContextRaw`, so the **process** and the coordinator's per-key state are untouched. **The consequence the review drew is real and is now a requirement:** FR-040a specifies the reaper↔pool contract — who closes, in what order, what happens to `browserMgrs` and the manager, and how the next call relaunches — and test 55 + a §10.2 row cover "reaper cancels `browserCancel` while the pool entry is live". The word *verified* is no longer used as a shortcut anywhere (§0's citation-policy note) | §0 citation policy, §2.1, §15 CRIT-006, FR-040a, test 55, §10.2 |
| **CRIT-103** — `ErrBrowserPoolFull`'s remedy does not work | **ACCEPTED IN FULL; option (a) taken, eviction still refused.** Both named actions were ineffective — closing a panel only calls `ViewerDetached`, and FR-040 requires zero tabs **and** zero viewers, so N workspaces holding tabs made the (N+1)th refusal permanent. §5 forbids eviction and that stands (a silent logout mid-task is worse than a refusal). **FR-046 adds the missing operator action** — `POST /api/v1/workspaces/{id}/browser/close` plus its SPA control, idempotent, viewer-notifying, profile-preserving — and the error text is rewritten as a formatted error naming the cap's **value**, the close action and the reload-without-restart raise. US-15/AC4, US-18, SC-018 and test 60 all use the **hard** case (every browser pinned by a tab **and** a viewer), because the existing cap test uses idle browsers and cannot fail for this | **SUPERSEDED — see the note below this table.** |
| **CRIT-104** — the exempt set omits `browser_list_tabs`; the count appears four ways | **ACCEPTED IN FULL, and the deeper problem is fixed rather than papered over.** `browser_list_tabs` is registered (`register.go:76`), read-only, and its own file says it is not control-gated (`tabs.go:20`) — under the old AC5 it would have taken the write lease and deferred the headline demo behind an unrelated agent. It is now exempt; the set is **six**; §14 rule 3 carries the single normative count and the other three sites derive from it. **On "membership is a rule, not a list":** the review is right that the old rule was self-contradictory (`browser_handle_dialog` mutates *and* was exempt), so a rule was found that actually holds — **lease iff control-gated** — which partitions the eleven shipped tools exactly, with per-tool evidence in §14 rule 3's table, makes `browser_snapshot` fall out with no exception, and makes `browser_handle_dialog` exempt from **both** gates for one stated reason. Test 18 becomes behavioural (each tool exercised under a held control lock and a held write lease; the answers must agree) so it can no longer be made green by editing this document | §14 rule 3, US-9/AC4+AC4a+AC5, FR-019a, §12 A17, test 18, §16 MAJ-101/102 |

> **⚠️ CRIT-103 is MOOT — superseded by operator ruling (ADR D1.7), 2026-08-31, later the same day.** The row above accepted the finding and fixed it by *adding* an operator-facing close action (FR-046) so that the refusal's named remedy would be real. **The ruling removed the refusal instead:** *"there is no 'pool full' error surface and no UI change"* — at the cap the pool evicts the least recently used instance, and closing a browser is non-destructive because the logins live in the profile on disk. So the entire chain the row describes is withdrawn: FR-039, FR-046, US-15/AC4, US-18, SC-018, tests 59 and 60, the `errBrowserPoolFull` sentinel, `errPoolFull`'s message, `BrowserResolvePoolFull`, and §5's added-path carve-out. **The finding's underlying insight survives and is generalised as SC-022:** an operator-facing message must name an action the reviewer can trace to the function that performs it. That discipline is now applied to FR-054's thrash WARN and FR-053's ceiling error — and it bites there too, because "raise `max_browsers`" is a **ceiling** raise and is a no-op whenever the derived target is the binding term (FR-056). See §17 C1/M1.

### MAJOR

| ID | Disposition | Where / evidence |
|---|---|---|
| **MAJ-101** — the exempt-set size and D2 tool count stated four ways | **ACCEPTED.** §14 rule 3 now opens with a normative-counts block (**exempt = 6; leased = 11 = 7 shipped + 4 of D2's 6**); §15's MAJ-008 row is corrected from "three"; §14.1's `leaseWrite` comment from "five new D2 action tools" to four, naming the two exempt ones | §14 rule 3, §14.1, §15 MAJ-008 |
| **MAJ-102** — the rule contradicts its own exemption; the list was widened under pressure | **ACCEPTED.** See CRIT-104. Additionally, the unspecified interaction the review names is now specified: after `browser_handle_dialog` clears a modal, the resuming leaseholder **must re-verify its own precondition** (re-resolve the selector, re-check the URL) rather than completing against a page that changed underneath it — §12 A17 | §12 A17, §14 rule 3 |
| **MAJ-103** — profile-dir nesting; `cleanStaleSingletons` cleans only `cfg.ProfileDir` | **ACCEPTED, and it turned up a second defect the review did not name.** Verified: `cleanStaleSingletons` (`coordinator.go:1488-1498`) is called with `c.cfg.ProfileDir` only (`:1235`); the default ProfileDir is itself a user-data-dir (`manager.go:125`). **Decided:** `cfg.ProfileDir` keeps its meaning, per-key dirs are **siblings** under `profileRoot = filepath.Dir(cfg.ProfileDir)`, and FR-042b runs `cleanStaleSingletons` against each per-key dir before launch. **The second defect:** `InstallRootForProfileDir` (`exec_resolver.go:50`) is arithmetic over the *parent* of what it is given, so feeding it a per-key path yields a different, wrong managed-Chromium install root **per workspace** — N downloads of one binary. FR-037a resolves the exec path once from `cfg.ProfileDir`; §5 forbids the per-key call; test 52 asserts it | FR-037a, FR-042b, §3.1 INVARIANT P-5, §5, US-19/AC3, tests 52 + 57 |
| **MAJ-104** — boot `Preprovision` silently breaks under a lazy pool | **ACCEPTED IN FULL.** Verified: `gateway.go:2286` ranges `agentLoop.BrowserManagers()` per manager to call `Preprovision`; `BrowserManagers()`' own doc states the purpose is that *"a fresh install's managed Chromium download starts at boot instead of at an agent's first browser tool call"*. Under a lazy pool that snapshot is empty at boot. **Cleanly fixable, and the code says so:** `Preprovision`'s entire body is `if m.cfg.CDPURL != "" { return "", nil }; return m.resolveExecPath(ctx)` (`manager.go:3218-3223`) — no key, no manager, no live Chrome. FR-016c moves it to `pool.Preprovision(ctx)`, called once at boot; test 61 asserts it fires with zero live keys | FR-016c, §2.1, BDD *boot-preprovision*, test 61 |
| **MAJ-105** — `metadata.go`'s second construction; test 4's predicate would trip on three non-tool holders | **ACCEPTED IN FULL, plus a fourth holder.** Verified: `metadata.go:36-51` builds all eleven structs again with a nil manager — added to §2.1 as **modifies**, with test 4a. Verified the three non-tool holders (`CaptureSession` `capture_session.go:258`, `LiveViewRegistry` `live.go:322`, `LiveView` `live.go:1324`) and found a **fourth**, `BrowserCoordinator.managers` (`coordinator.go:186`). Test 4's predicate is restated precisely as *every type in the package implementing `tools.Tool`*, and all four legitimate holders are named in §2.1 so nobody "fixes" them. **Bonus finding:** `LiveViewRegistry`'s doc comment (`live.go:316-320`) asserts *"a BrowserManager is itself scoped to one agent"* — false after the re-key, corrected with FR-002d | §2.1, test 4 note, test 4a, FR-002d |
| **MAJ-106** — both gates are decorative; G-2's is a skip that reports green | **ACCEPTED IN FULL; the two gates are separated and given different, honest enforcement.** Verified `skipIfNoBrowser` has **two** green-skip paths, not one: CI without `OMNIPUS_BROWSER_E2E` (`browser_e2e_test.go:66-68`) **and** no probeable Chrome anywhere (`:69-111`, via `resolveTestBinary`'s own `t.Skipf`). **G-2 becomes mechanical:** `requireBrowserOrFail` instead of `skipIfNoBrowser`, the gate job sets the env var, the receipt is captured without a pipe and asserted to contain no `--- SKIP` and no `no tests to run` — four conditions in SC-012a, any of which failing fails the gate. **G-1 is declared a HUMAN gate and its owner is named** (the implementing PR's reviewer), because the mechanical form the review suggested — a test asserting a measurement file is non-empty and dated — passes on a fabricated file and would become a second place to write the guess. SC-012 names the artefact (raw RSS for N=1…4, host RAM, and the arithmetic to the default); test 51 is the partial mechanical half and its limits are stated | §0.3.1, FR-044, FR-045, SC-012, SC-012a, tests 37 + 51 |
| **MAJ-107** — Stream C ships the isolation claim before Stream P delivers isolation | **ACCEPTED IN FULL.** The review's diagnosis is exact: the justification was true of the cookie behaviour and false of the sentence describing it, and `capture_shared_context: true` (`defaults.go:671`) keeps one partition across all workspaces until P lands. **Also discovered while fixing it: FR-034 claimed the replacement literals were "specified here" and the previous draft specified none of them anywhere.** New **§3.3** writes both stages verbatim: interim (Stream C) *"the browser this workspace's agents share"* — claims only tab-set sharing, true after Stream A; final (FR-034a, Stream P, **same commit as FR-037**) *"this workspace's browser"* plus an explicit per-workspace-logins sentence. §5 adds the general non-behaviour. **This also answers the review's unasked question 10** (what is the rollback if G-2 fails after §0.4 lands): nothing, because nothing in §0.4 asserts isolation any more | §0.4, §3.2, §3.3, FR-034, FR-034a, §5 |
| **MAJ-108** — the idle window is named five times and defined nowhere | **ACCEPTED IN FULL.** Config key **`tools.browser.idle_close_ttl`**, default **15m** with its derivation stated (3× the per-tab `idle_ttl` at `manager.go:134`, and why the asymmetry is right: per-tab reaping already reclaims the renderers, the dominant cost). **Caller named:** the existing 1-minute sweep goroutine (`gateway.go:5321-5352`), after its `ReapIdleSessions` loop — a caller the previous draft never identified. **Post-close state specified, and the tension the review found is resolved by naming it:** FR-026a's liveness is about the **key** (does this workspace still warrant a browser), the pool's is about the **process**; they are different questions, so a live key with no running Chrome is a legal, described state. `browserMgrs` entry and `*BrowserManager` survive; `pool.Acquire` relaunches from the profile; login intact | **STANDS, with one addition eviction makes load-bearing.** The 15-minute default and its 3× derivation are unchanged, but the two TTLs **compose**: a workspace browsed once occupies its slot for the 5-minute tab TTL **plus** the 15-minute idle-close TTL — ~20 minutes after the last action. The target therefore bounds workspaces browsed *within any ~20-minute window*, not workspaces browsing *right now*, and it sets the floor for FR-054's thrash window. §12 A22 carries it | FR-040a, §12 A22, US-12/AC4a, BDD *idle-close-relaunch*, tests 54 + 55, §10.2 |
| **MAJ-109** — `max_browsers` has no edge semantics and no stated relation to the tab budget | **ACCEPTED IN FULL.** Verified `maxTotalTabs <= 0` means unlimited (`coordinator.go:785-788`). **(a)** `max_browsers <= 0` means unlimited too — same shape, so an operator who knows one key is not surprised by the other. **(b)** `max_total_tabs` stays **global** across all N Chromes; making it per-Chrome would silently multiply a configured ceiling by N. The starvation consequence is stated: it is today's behaviour across agents and the pool does not worsen it. **(c)** on the default being measured on one box — that is exactly what FR-044/G-1 is, and §12 A21 states plainly that it is a fixed conservative constant operators on larger hosts must raise, rather than a function of host memory (auto-sizing from RAM would need its own measurement to be honest, and G-1 has not run yet) | FR-038a, §12 A21, US-15/AC5+AC6, test 53, §10.2 **⚠️ SUPERSEDED BY ADR D1.5a — both keys are deleted, so neither has edge semantics and there is no relation to state.** FR-038a and test 53 are tombstoned. **The finding's principle survives as AC7 and SC-022:** no shipped setting may imply a bound that does not exist, and no message may name one. |
| **MAJ-110** — no boot reconciliation; orphan Chromes sit outside the cap | **ACCEPTED, with the shipped behaviour described accurately.** The review implies orphans are silently ignored; in fact the marker is consulted **at launch** and the shipped path **refuses to launch** when a marker's pid is alive (`coordinator.go:1448-1467`) — a reasonable single-Chrome story that is not a boot-time story. The review's real point stands: orphans consume the host memory the cap exists to bound while `LiveKeys()` cannot see them. FR-042a adds the boot pass (dead pid ⇒ remove marker + stale lock, INFO with a count; live omnipus-owned pid ⇒ terminate, WARN with workspace and pid). **§12 A20 records what the review did not raise:** pid reuse is a real hazard, the shipped code has it too, and the `/proc/<pid>/exe` mitigation is **Linux-only** — on macOS the marker is removed without terminating and a WARN names the pid, a smaller guarantee stated rather than implied | FR-042a, §12 A20, §6, US-19, SC-016, test 56, §10.2 |
| **MAJ-111** — the profile directory has creation but never deletion | **ACCEPTED IN FULL; decided as delete-on-workspace-deletion.** FR-043a: **workspace deletion is the sole trigger**, after `pool.Close(k)` returns; idle close, roster change, reload, operator close and crash recovery all leave it — five negative cases in test 58, because the positive case alone would pass a "delete always" bug. Directory mode **0700**, stated rather than inherited (matching `coordinator.go:1232`, `manager.go:799`), because these now hold per-client session cookies. **No quota, and the reason is given** rather than omitted: live profiles are bounded by `max_browsers`, and dead ones are removed by the deletion path, so the unbounded case the review worries about is closed by deletion rather than by a ceiling. A release-note line is required | **PARTLY SUPERSEDED.** The delete-on-workspace-deletion decision stands and eviction joins the negative cases (four, not five — the operator-close trigger went with FR-046). **But the "no quota" reasoning is WITHDRAWN:** `max_browsers` bounds live processes, not bytes, and an idle-closed or evicted profile's cache is deliberately *not* reclaimed, so N browsed-once workspaces leave N unbounded caches. Reopened in §12 A24(b), matching ADR §4 and §6 | FR-043a, US-20, SC-017, test 58, §5, §12 A24 **⚠️ ONE CLAUSE SUPERSEDED BY ADR D1.5a.** Its "no quota, and the reason is given" clause rested on *"live profiles are bounded by `max_browsers`"* — a key that is now deleted, and which bounded **processes** rather than bytes even when it existed. **The quota question is therefore reopened, not closed** (§12 A24(b)): idle-closed and evicted profiles keep their caches by design, so N workspaces browsed once each leave N unbounded directories. The rest of the row — workspace deletion as the sole trigger, 0700, the negative cases (now **four**, with eviction replacing the withdrawn operator close) — is unchanged. |
| **MAJ-112** — `leaseWaitTimeout`'s relationship to the action-tool timeout is a comment, not a gate | **ACCEPTED, with one correction to the finding.** The timeout is `BrowserConfig.PageTimeout` (`manager.go:35`, default 30s `:123`) as the review says — but the config key is **`tools.browser.page_timeout`**, not `page_timeout_sec`; `PageTimeoutSec` is the Go field name (`config.go:3632`, env `OMNIPUS_TOOLS_BROWSER_PAGE_TIMEOUT`, applied `loop.go:2311-2312`). FR-023a **clamps** rather than rejects — aborting boot over a browser tuning key is disproportionate, and a clamp preserves the contract the operator cares about — with a WARN naming both keys and both values, at load **and** on reload. The WARN is part of the requirement: a silent clamp leaves the operator believing a setting took effect | FR-023a, §14.1, §6, test 50, §10.2 |
| **MAJ-113** — FR-033's premise is contradicted by shipped code; Ray's heartbeats lose the browser | **PARTIALLY ACCEPTED — the general point is right, the stated consequence is WRONG, and the correction matters because it is the finding's whole force.** Accepted: the stamping path is real (`loop.go:6934-6957`) and the previous draft cited neither it nor `resolveWorkspaceIDForContinuation`; it is now cited in §6, US-6/AC0 and §12 A19. **Rejected: "adding Ray to a second workspace permanently kills browsing on all his heartbeats."** Heartbeat jobs are workspace-scoped **by construction** — the reconciler names each `heartbeat:<workspaceID>:<agentID>` (`heartbeat_schedule.go:30-33`), `resolveScheduleWorkspaceID` parses the workspace back out (`schedules.go:639`, `:654`), and `pickSession` stamps it on every fire (`:513`, `:527-576`, called `:141`). So a heartbeat reaches **rung 1** and never sees FR-033; enabling a heartbeat on a second workspace creates a *distinct job with its own workspace*, not an ambiguity. **The real residual is narrower and is now stated:** a *plain, operator-created* schedule resolves to `""` (ADR-065 FR-8 removed the channel source, `schedules.go:632-639`), so a plain schedule for a multi-workspace agent **is** refused — the one case where "which client's logins?" genuinely has no answer. The suggested alternative (a per-agent browsing-home workspace) is **declined** in §12 A19: ADR-037 removed the global delegation graph precisely because "the owner's workspace" is ambiguous for a multi-workspace agent, and `resolveScheduleWorkspaceID`'s own doc rejects `CronJob.AgentID` on the same grounds; re-adding a global agent attribute for browsing would reverse that | §6, US-6/AC0, §12 A19, §2.1 |
| **MAJ-114** — ADR D2.11's elevation-of-privilege disclosure is owned by nobody | **ACCEPTED IN FULL; CLAIMED, not handed off.** ADR D2.11 **decides** it (*"the team-editing UI must state this at the point of adding, not only in release notes"*), §1's out-of-scope list excluded only the *information-disclosure* bullet, and D1.10 makes it worse — unattended delegated work now inherits those logins. FR-047 adds it: Workspace → Team, visible **before** confirmation, naming the consequence rather than the mechanism, with the same text in the release note. §1's out-of-scope wording is tightened so the split between D2.11's three bullets is explicit (elevation → FR-047 here; repudiation → FR-027 here; information disclosure → D2) | FR-047, US-21, §1, test 64 |

### MINOR

| ID | Disposition | Where / evidence |
|---|---|---|
| **MIN-101** — the comment count is wrong and the grep does not produce the quoted number | **ACCEPTED IN FULL; reproduced exactly.** The command returns **57**: 37 usages + **2** declarations (`tools.go:63`, `:69`) + **18** comments (the draft said 12). All 18 are enumerated in §2.2, including the six the draft missed (`tools.go:50,62,65,68`, `capture_session.go:708`, `config.go:3892`), and `pkg/config/config.go` is added to the inventory with a note that it is comment-only. Re-verified independently: **all 37 executable line numbers are correct** | §2.2 |
| **MIN-102** — the test-side surface is unbudgeted | **ACCEPTED.** See CRIT-101; §2.2a carries the full per-file table and §10.1 the migration bar | §2.2a, §10.1 |
| **MIN-103** — the `"ws:"` prefix and `WorkspaceID()` are residue of `BrowsingKeyKind` | **ACCEPTED as "explain it or drop it"; kept, with the explanation the review asked for.** The prefix is retained deliberately as a **namespace marker**, not as future-proofing for a second shape: workspace ids are bare ULIDs, and a bare ULID in an audit event, a WARN or a `LiveKeys()` dump is indistinguishable from a session, task or agent id — all ULIDs, all appearing in the same lines. `WorkspaceID()` stays because two consumers need the bare id (the profile path, the audit field). Written into `key.go`'s doc comment so it is not unexplained next to a non-behaviour forbidding a second shape | §3.1 `key.go` |
| **MIN-104** — the `sandbox_apply.go` span is wrong | **ACCEPTED.** Verified: the DevTools-port removal NOTE is at `:412-417`; `:405-409` is the `enforcePortRules` computation (`:407` `KernelChildConfiner`, `:408` the ABI check). Both citations corrected | §0.1, §6 |
| **MIN-105** — a runtime `ProfileDir` change relocates every profile and the spec is silent | **ACCEPTED; resolved by preserving shipped behaviour rather than inventing one.** Verified `coordinator.go:681-687`: today the change logs a WARN and is **not applied** to a running Chrome (*"applies after gateway restart"*). §12 A18 keeps exactly that per key — not applied live, WARN now names the affected key count, paths re-derive at the next restart, and the resulting logout is stated in the WARN and in the config key's doc. Live relocation was rejected: hundreds of MB per workspace with Chrome holding files open | §12 A18, §2.1 |
| **MIN-106** — a workspace id becomes a path segment with no stated constraint | **ACCEPTED IN FULL.** Verified ids are server-minted ULIDs (`rest_workspaces.go:495` default, `:848` created). The invariant is now written down **and enforced** — `filepath.Base(id) == id`, not `.` or `..` — checked in `ResolveBrowsingKey` before the key exists, refusing as `ErrNoBrowsingContext`; added to §5 as a non-behaviour, with test 62 and a §10.2 row | §5, FR-037, test 62, §10.2 |
| **MIN-107** — the residual custom-agent case already has a shipped boot-time surface | **ACCEPTED IN FULL.** Verified `logWorkspacelessAgents(homePath, cfg)` at `gateway.go:5026`, immediately after `ensureDefaultWorkspace` at `:5013`, with the rationale at `:5015-5025`. §6 now requires FR-008a's panel reason and `ErrNoBrowsingContext`'s text to name the **same** remedy in the **same** words as that log line, so an operator seeing both does not think they are two problems | §6, US-14, FR-008a |
| **MIN-108** — ADR-072 D1.3's table still calls the transcript session id "used for unattended delegated work" | **DISCHARGED, 2026-09-01.** The consolidation deleted D1.3's key table entirely. The ADR's only remaining mention of that key is in **D1.10's deletion list**, reading *"`tools.ToolTranscriptSessionID` as a browsing key. **Unused, and not a fallback.**"* — which is exactly what §2.1 says. Removed from §12's corrections note and from the closing "Next" paragraph. *(An outstanding item that has in fact been done, but is still recorded as outstanding, gets re-raised every round — which is what happened to this one.)* | §12, §17 m4 |

### OBSERVATION

| ID | Disposition | Where / evidence |
|---|---|---|
| **OBS-101** — the four "red today" tests are plausible but no receipt is offered; SC-008 is an intention | **ACCEPTED.** The review is right that all four are expressible against current code, so confirming them costs four commands. SC-008 is rewritten with an explicit failure condition: **the four `exit=` receipts must be pasted into the PR *before* the implementing commits**, captured without a pipe. A red-then-green claim made after the fix has landed is not reproducible and does not satisfy the criterion | SC-008 |
| **OBS-102** — recording that MAJ-003's rejection is correct so it is not re-litigated | **NOTED, no action.** Re-verified independently: `ensureDefaultWorkspace` (`rest_workspaces.go:468`) runs on every boot (`gateway.go:5013`) and seeds the built-in roster via `defaultWorkspaceTeam`. The scope note is right and is already reflected: the default workspace is covered, and the **custom** agent (US-14) is the right residual concern — now also wired to the shipped `logWorkspacelessAgents` surface per MIN-107 | §15 MAJ-003, §6, US-14 |
| **OBS-103** — the surviving new concepts are defensible; no action | **NOTED, no action.** Recorded here so a later reader sees the observation was read rather than skipped. `BrowsingKey`, `TabState`, `BrowserResolveOutcome`, `ManagerResolver` and `BrowserPool` all survive this revision unchanged in shape; this round adds no new type beyond the pool's own methods (`CloseIdle`, `DeleteProfile`, `ReconcileMarkers`, `Preprovision`), each of which owns one FR | §3.1 |

**Round-2 tally: 29 findings — 25 accepted in full, 3 accepted with a correction to the finding's own evidence (MAJ-110, MAJ-112, MIN-108's ownership), 1 partially rejected with counter-evidence (MAJ-113's stated consequence).** Three findings turned up defects the review did not name: MAJ-103's install-root arithmetic (FR-037a), MAJ-105's fourth manager-holding struct and `LiveViewRegistry`'s stale doc comment, and MAJ-107's discovery that FR-034's replacement literals were never actually specified anywhere (§3.3).

---

---

## 17. Round-3 and round-4 review dispositions

Two reviews are dispositioned here. **Round 3** (`…-spec-review-round3.md`, 5 CRITICAL / 9 MAJOR)
was written against the **pre-consolidation** ADR and cites `D1.1b`/`D1.1c`/`D1.1d`; its findings
are restated below against the current section numbers, because a disposition that cites a
section number nobody can resolve is not a disposition. **Round 4** is the consolidated
three-document grill of 2026-09-01, returned in-session and **not written to disk** — cite it as
*"consolidated three-document grill, 2026-09-01, returned in-session"*, and do not go looking for
a `-round4` file.

Round 3's five CRITICALs and round 4's four are largely the same defects seen twice, once before
the ADR was consolidated and once after. They are dispositioned once each, cross-referenced.

### 17.1 Round-4 CRITICAL

| ID | Disposition | Where |
|---|---|---|
| **C1** — the lease outcome, and the ruling that superseded the question | **ACCEPTED, and resolved by the newer ruling rather than by the older one.** The grill's diagnosis was exact: §14 called itself *"the single normative definition"* and then contradicted D2.10 in four places — `leaseWrite`'s contract, §14.2 rule 1, behavioural contract 11, and FR-020/US-14/§12 A17 — all specifying an **immediate non-error `deferred`** where D2.10 rules **retry-with-backoff, then a named error**. It also caught the inversion in FR-023a: the 2 s clamp was justified *"to guarantee a deferral rather than an error"*, which is the opposite of D2.10's intent. **But the premise moved underneath both answers.** ADR **D1.9a** (2026-09-01) rules that tabs stay per agent, so agent-vs-agent contention exists **only on the operator's shared tab**. §14 is therefore **rescoped**, not merely realigned: the primitive is unchanged, FR-020 now requires **both writers eventually complete** (ADR criterion 16 — *"asserting neither errors would pass when nothing happened"*), FR-023a's clamp is restated as a **retry budget** rather than a give-up timer, `deferred` is retained **unchanged** for the human-control case so no prompt is rewritten, and US-9 gains **AC0**: two agents on their own tabs never contend at all, asserted so a future re-merge of the tab sets fails the concurrency suite | §0.2a, §14 (scope table, `acquireWrite`, rule 1, rule 3), FR-019…FR-021, FR-023a, contract 11, US-9/AC0+AC1, tests 12–17 |
| **C2** — audit is per action, not first use | **ACCEPTED IN FULL.** Revision 3 shipped first-use-only auditing (US-13/AC2, FR-027, contract 16, the *audit-repudiation* scenario, test 45) and **cited D2.11 while doing so — the section that rejects first-use-only by name**: *"An event on first use of a context an agent did not establish fires once per agent per workspace and says nothing about the tenth action, or about which agent made the purchase."* FR-027 is rewritten: one event per **instance creation**, one per **write-class tool call** carrying workspace, agent, tool and host; read-only tools not audited per call. The write-class set is the `controlledResult`-gated set — the same classification §14 rule 3 already uses, so there is one list, not two. The BDD asserts the **tenth** write event specifically, which is the assertion first-use-only fails. **D2.11's `^[a-z_]+$` constraint had zero occurrences in this spec** and is added as **FR-058** with its own test, which asserts a deliberately dotted fixture name **fails** — a dotted name blanks the whole Audit Log viewer, not just its row (#667) | FR-027, **FR-058**, US-13 (AC1–AC5), contract 16, BDD *audit-per-write-action* + *audit-event-name-is-viewer-safe*, test 75, §5 |
| **C3** — criterion 3b is withdrawn; the stack shipped for it is tombstoned | **ACCEPTED IN FULL, and independently verified before acting.** `FilterToolsByPolicy` (`pkg/tools/compositor.go:429-444`) `continue`s on a deny verdict at **`:436-438`**, so a policy-denied agent is never sent the tool definition, never calls it, and `tool_denial.go`'s message has **no production caller** for this case. Test 10 would have asserted a string nothing emits — a green with no referent. ADR **D1.12** withdraws the third `ListTabs` state and §3.1's criterion 3b explicitly. **Tombstoned with a stated reason, not deleted silently:** FR-014a (§9), test 10 (§10), US-8/AC2+AC3, behavioural contract 10, §6's policy-engine boundary, the BDD's denial assertion, the §10.2 dataset row, and §15 MAJ-005's disposition, which is annotated in place. **The defect is real and is not claimed as fixed:** an agent that cannot distinguish "I may not" from "there is nothing" needs a system-prompt or manifest surface, which ADR §6 owns as its own headline problem surviving in a narrower form. Holdout 4 is **rewritten rather than deleted** — it now records the honest outcome and checks the one regression that is still reachable (she must not claim the browser is "shared across the workspace") | ~~FR-014a~~, ~~test 10~~, US-8, contract 10, §6, §12 A4, §13 holdout 4, §15 MAJ-005 |
| **C4** — the renderer floor is undefined on a default install | **ACCEPTED, and resolved without a product change — the ADR's premise was wrong but a correct one was available.** D1.6 derives R from *"a tab count is enforced (`maxTotalTabs`)"*. **Verified false by default:** `grep MaxTotalTabs pkg/config/defaults.go` returns nothing — it is never seeded — and `coordinator.go:240-253` logs *"global tab budget: UNLIMITED (tools.browser.max_total_tabs unset or <=0)"* when the value is `<= 0`. So on a fresh install there is no `maxTotalTabs`, hence no R, hence no `per_instance`, hence no `max_browsers`, and P8 has no R to test. **But a tab count IS enforced by default, and D1.6 named the wrong one.** `BrowserConfig.MaxTabs` ships **5** (`manager.go:36`, `:124`), is enforced at `manager.go:1139`, `:2005`, `:2047` and `:2216`, and its own config documentation calls it *"the per-agent courtesy cap (tools.browser.max_tabs, default 5) … the guard most operators actually want"* (`config.go:3662-3663`). **FR-055 derives `R >= tools.browser.max_tabs`.** This is the "derive R from something that exists by default" resolution, **not** the seeded-default one: **no product change, no new config default, nothing an operator's `config.json` gains.** It also composes with D1.9a: `max_tabs` is per-agent tab set (FR-049), so R is a floor on the sites *one agent* may hold open, which is the right granularity for site isolation. **Outstanding ADR edit:** D1.6's sentence should name `max_tabs`, not `maxTotalTabs` — flagged, not actioned, since this spec does not edit the ADR | **FR-055**, FR-049, §12 A21, §12 A23, test 74, ADR crit P8 **⚠️ SUPERSEDED BY ADR D1.5a, 2026-09-01 — and the resolution is DISSOLVED rather than reverted.** This row is cited as a CRITICAL resolution, so the removal must not read as a regression. The finding was real and the answer was right: `--renderer-process-limit` needed a floor, and `max_tabs` (not the never-seeded `max_total_tabs`) was the only tab count enforced on a default install. **D1.5a deletes both terms** — `max_tabs` from the code (FR-059) and the flag from the launch flags (FR-055 tombstoned) — so the floor has neither a source nor a consumer. **Not setting the flag is strictly better than any floor**: it *weakens* site isolation above its bound, so its absence gives Chrome's default site-per-process isolation to **every** page rather than only to those below R. **There is no residual concern to carry forward**, which is why FR-055's tombstone states the reason at length and test 74's first assertion is inverted rather than deleted. |

### 17.2 Round-4 MAJOR

| ID | Disposition | Where |
|---|---|---|
| **M1** — the pool-refusal design is ~85 lines, not the ~14 ADR §9.1 names | **ACCEPTED IN FULL; the count was low and the grill's enumeration is the one that was followed.** ADR §9.1 lists FR-039, FR-046, invariant P-2 and a handful of lines. The sites actually carrying the refusal, all now withdrawn or inverted: §0.4's bullet; §1's in-scope bullet (*"a refusal (not an eviction) at the cap"*); §3.1's `errBrowserPoolFull` + `errPoolFull` block and `BrowserResolvePoolFull`; INVARIANT P-2; §4 contracts **17 and 21**; §5's two non-behaviours (eviction forbidden, and FR-046's close as one of five permitted destroy triggers) **and** its added-path carve-out; §6; US-15 (whole story) and US-18 (whole story); FR-038, FR-038a, FR-039, FR-046 in §9 plus the **traceability rows**; the BDD scenarios *pool-refuses-at-cap*, *pool-refuses-when-all-pinned* and *close-is-not-deletion*; tests 26, 59, 60 and 63; §10's list; **two §10.2 dataset rows**; SC-007(2), SC-010, SC-018; and **§16 CRIT-103's disposition**, which the eviction ruling moots. **Two arithmetic consequences the grill flagged and that are easy to miss:** FR-046's close was one of **five** profile-deletion negative cases, so **SC-017 and test 58 now carry four** — with **eviction** taking the vacated slot, which is the more important negative anyway, since eviction is only an acceptable policy *because* the profile survives it. And test 63's single assertion (*"`LiveKeys()` never exceeds the cap at any instant"*) **fails correct D1.7 behaviour** on the all-pinned path, so it is split in two. `TestPool_RefusesNeverEvicts` is **deleted, not renamed** — a rename would carry the forbidden assertion forward under a new label | §0.4, §1, §3.1, §4, §5, §6, US-15, ~~US-18~~, FR-038/038a, ~~FR-039~~, ~~FR-046~~, **FR-050…FR-054**, §8, tests 26/60/63/67/68/71, §10.2, SC-010/SC-017/SC-018→SC-022, §16 CRIT-103 |
| **M2** — every ADR citation dangles or resolves to the WRONG section | **ACCEPTED IN FULL, and re-derived by reading, not by find-and-replace.** Counts reproduce: `D1.1a` ×29, `D1.2` ×24, `D1.4` ×8, `D1.5` ×7, `D1.0` ×3, `D1.0a` ×2. `D1.0`, `D1.0a` and `D1.1a` no longer exist; the dangerous ones are the three that still resolve and now mean something else — *"D1.4"* (browsing-key ladder) is now **D1.11**, *"D1.5"* (three-state `ListTabs`) is now **D1.12**, and *"D1.2"* (the sharing ruling) is now **D1.10**. A citation that resolves cleanly to the wrong section is worse than one that dangles, because nothing surfaces it. **§0.0 carries the full map in one place** and is the only place it lives, so the next renumber has a single edit site. **The Go doc comments were treated as the priority**, because these numbers ship in `key.go`, `resolve.go` and `pool.go` and a stale one there sends a reader to a section that says something else: `key.go`'s `BrowsingKey` and `ErrNoBrowsingContext` now cite D1.11 and D1.10, `resolve.go`'s ladder cites D1.11, `pool.go` cites D1.4/D1.5/D1.6/D1.7/D1.8/D1.9. The round-3 review's own `D1.1b`/`c`/`d` references are translated in §17.3 rather than quoted forward | **§0.0**, §3.1 (all three files), §9 Source column, §12, §15, §16 |
| **M3** — SC-015 and §12 A10 quote a sentence the ADR no longer contains | **ACCEPTED IN FULL.** Revision 3 quoted, as *"D1.1a's closing paragraph"*: *"Decider for every ruling in this ADR: Daniel Piatkowski (operator), 2026-08-31. Recorded once here so the individual 'operator ruling' citations in D1.0, D1.1a, D1.2, D1.4, D2.9 and D2.11 have a named authority."* **That sentence is not in the consolidated ADR**, it was never in a section called D1.1a's closing paragraph after the rewrite, and four of the six sections it enumerated no longer exist. The current attribution is in the ADR's **header block**, is **unenumerated**, and carries **no date**: *"Decider for every ruling in this document: Daniel Piatkowski (operator). Recorded once here so the individual 'operator ruling' citations below have a named authority; a spec cannot resolve its own provenance."* It is **broader** than what revision 3 quoted, which is why the renumber did not break it. SC-015 and A10 are rewritten against it, and SC-015's failure condition is narrowed accordingly: the blanket covers ADR-072 only, so a ruling recorded in any other document needs its own attribution | SC-015, §12 A10 |
| **M4, M5, M6, M11** | **NOT THIS DOCUMENT'S — owner named, no disposition manufactured here.** M4 and M5 are ADR-side findings; M6's body and M11's body belong to the ADR and the D2 spec respectively. Writing a "fixed" row here for another document's defect is the cross-document confusion this round exists to end. **Their tails that DO land here are dispositioned as their own rows below (M6-tail, M11-tail)**, and nothing else from them is claimed | — |
| **M6-tail** — this spec's audit events carry no name constraint | **ACCEPTED.** Zero occurrences of `^[a-z_]+$` in revision 3, while FR-027's events land in the same `audit.jsonl` as names that already contain dots. Added as **FR-058** with US-13/AC4, a §5 non-behaviour, a BDD scenario and test 75's third case. It is C2's tail and is dispositioned with it | FR-058 |
| **M7** — three divergences from the ADR | **ACCEPTED IN FULL, in three parts, and (a) resolves more cleanly than the grill expected.** **(a) Profile path.** ADR D1.8 says `…/profiles/ws-<id>/`; revision 3 said `…/profiles/ws/<id>/`. Verified against `InstallRootForProfileDir` (`exec_resolver.go:50` — `Clean(Join(Dir(Clean(p)), "..", "chromium"))`): today's `…/profiles/default` → `…/browser/chromium`; the ADR's flat `…/profiles/ws-<id>` → `…/browser/chromium` (**correct**); revision 3's nested `…/profiles/ws/<id>` → `…/profiles/chromium` (**wrong**). So **the nesting was the sole cause of revision 3's INVARIANT P-5**, and adopting the flat form dissolves the invariant rather than merely renaming a directory. FR-037a keeps the resolve-once rule as belt-and-braces and test 52 becomes table-driven over **both** layouts so a future re-nesting fails. **(b) `max_browsers`.** The ADR makes it `operator_ceiling` clamping a value derived from D1.5's formula; revision 3 made it the cap itself with `<= 0` = unlimited — **unreachable under `clamp(…, 1, ceiling)`**, and a single measured integer would ship a 3916 MB box's answer to a 32 GB machine. FR-056 respecifies it as a derivation; FR-038a restates `<= 0` as *no ceiling*, not *unlimited*; test 53 and the §10.2 rows are inverted; §12 A15 records that the question was mis-framed as a measurement. **(c) `cfg.MaxTabs`.** Appears in neither document and, after the re-key, has no owner: `totalTabCountLocked` (`manager.go:1549-1555`) sums every session in the manager, so one manager per workspace silently turns a 5-tab **per-agent** cap into 5 for the team — contradicting the key's own doc (`config.go:3662-3663`). Under D1.9a the per-agent tab set **is** its right home: **FR-049**. And it is the tab count C4 needed | FR-037a, FR-038a, **FR-049**, **FR-056**, §2.1, §3.1, §12 A15/A21, tests 52/53/66/73 **⚠️ PART (b) IS SUPERSEDED BY ADR D1.5a.** M7(b) required `max_browsers` to be a **derivation** with `tools.browser.max_browsers` as its ceiling (FR-056), and M7(c) required `cfg.MaxTabs` to keep an owner after the re-key (FR-049). **Both keys are deleted from the code**, so both requirements are moot: FR-049 and FR-056 are tombstoned in §9 with their reasons stated, and FR-038/FR-038a go with them. **Part (a) — the flat `…/profiles/ws-<id>` path — is untouched** and remains the resolution. |
| **M8** — three ADR §6 "open" questions this spec has already decided | **ACCEPTED; each is FLAGGED individually rather than kept silently, and one is REOPENED.** **(a) Capture session per workspace (FR-016a) — KEPT.** The decision is forced by one-manager-per-workspace, not chosen; ADR §6 should close it citing FR-016a. **(b) Profile disk quota (§16 MAJ-111) — REOPENED.** Its closure read *"live profiles are bounded by `max_browsers` and dead ones are removed by the deletion path"*; that does not hold, because `max_browsers` bounds live **processes** and an idle-closed or evicted profile's cache is deliberately **not** reclaimed. N browsed-once workspaces leave N unbounded caches on a host this project has filled twice. The closure is withdrawn and no quota is invented in its place. **(c) Instances vs bytes (FR-038) — KEPT, and the ADR's own D1.5 already answers it:** instances for the target (item 2), bytes for the admission gate (item 3); the §6 row predates that split. All three are in §12 A24 and escalated in §0.5 E-4 | §12 A24, §0.5 E-4, §16 MAJ-111 |
| **M9** — two pool criteria are not falsifiable and three decisions have none | **ACCEPTED IN FULL, in five parts.** **P4 (thrash)** named neither `k` nor the window: FR-054 makes both configuration, gates their values on **G-5** (cold-start latency with a warm profile — ADR-042's ~30–60 s covers a fresh install *including a download* and is not that number), and test 71 drives `2 × threshold` cycles asserting **exactly one** WARN carrying all three elements. **P7 ("zero orphan Chromes")** contradicts D1.9, which states macOS clears the marker **without** terminating: US-19 gains AC2a/AC2b/AC2c and **SC-016 is platform-qualified** — zero markers everywhere, zero orphans on Linux, a WARN per surviving pid elsewhere with the residual exposure stated. An SC that must fail on a supported platform is the inverse of the "gate that cannot fail" shape rounds 1 and 2 both flagged. **Viewer staleness** had no criterion and, under eviction, is a **deadlock** rather than a leak — an abandoned panel makes a slot permanently unreclaimable: **FR-052**, US-15/AC5, test 67. **The memory-pressure gate** had none: **FR-057** with fixture tests at 0.84/0.85/0.86 and a non-Linux no-op, plus **FR-057a**'s two Chromium gates. **`max_total_tabs` staying global across N** now has test 53 and §12 A21's three-guard split. **And the guards' own gap, which the grill named precisely:** they were only ever exercised all-pinned, which cannot distinguish "the guard works" from "nothing was evictable anyway" — test 67 adds *the LRU has a viewer so the second-LRU goes* and its in-flight twin, and test 68 adds the `-race` interleaving | FR-052, FR-054, FR-057, FR-057a, US-15/AC3–AC5+AC8+AC11, US-19/AC2a–AC2c, SC-016, SC-019, SC-020, tests 53/67/68/71/72 **⚠️ PARTLY SUPERSEDED BY ADR D1.5a, and its central discipline is REINFORCED, not weakened.** **P4 (thrash)** survives: FR-054 still gates `k` and the window on G-5, but its WARN's *remedy* is re-derived — *"raise `max_browsers`"* names a deleted key, so the WARN now names **memory** and an action that exists (test 71 asserts the **absence** of any config-key name). **P7 (orphans) and the viewer-staleness part are untouched.** And the finding's own principle — a criterion that cannot fail is not a criterion — is what forced this pass to rewrite SC-010, SC-012, SC-021 and SC-022 rather than delete them: with every counter gone, **the only two controls left must each carry a test that fails when the control silently does nothing** (FR-061, test 80). |
| **M10** — the G-2 CI argument is false | **ACCEPTED IN FULL; verified on this worktree before rewriting.** `.github/workflows/pr.yml` has a dedicated `browser-e2e` job (`:392`) whose job-level env sets **`OMNIPUS_BROWSER_E2E: "1"`** (`:416`, commented *"Set ONLY here"*), installs Chrome as a declared dependency, verifies it resolves under one of the four names `skipIfNoBrowser` probes, and **fails the job on either skip path** (`:468-472`). Revision 3's §0.3.1 asserted the opposite and routed G-2 to the Fly worker on that basis. §0.3.1 and SC-012a are corrected, and G-2's home becomes that job. **Two further points the grill raised, both acted on.** *The "receipt without a pipe" demand:* the shipped step pipes `go test` into `tee` under `set -euo pipefail`, which propagates `go test`'s status — the *piped-into-tail* trap the rule guards against cannot occur there, and satisfying the rule literally would mean rewriting a working gate. The rule is restated as one about the **author's PR-body receipt**. *The `>= 180` pass floor* (`:481`, with a comment saying never to lower it without re-verifying): a `-run '^TestSpike_…$'` invocation inside that step produces one pass and trips it, so **G-2 gets its own step** with its own `-run` filter and its own receipt (this is **O4**, folded in here) | §0.3.1, SC-012a, FR-045 |
| **m2-tail** — the two idle TTLs compose, and that is now capacity | **ACCEPTED.** FR-040a's 15-minute default and its 3× derivation are sound in isolation and unchanged. But once eviction lands they become an input to capacity: a workspace browsed once holds its slot for the 5-minute tab TTL **plus** the 15-minute idle-close TTL — **~20 minutes of occupancy after the last action**. So the target bounds workspaces browsed *within any ~20-minute window*, not workspaces browsing *right now*, and D1.7's "ten workspaces on a three-browser machine" reads optimistically if that is missed. It also sets the floor for FR-054's thrash window: a workspace evicted before its idle TTL expired was still legitimately occupying its slot. Stated in FR-040a's own row and in §12 A22 | FR-040a, §12 A22, §16 MAJ-108 |
| **m4** — one "outstanding ADR edit" is already discharged | **ACCEPTED; marked done and removed from the closing paragraph.** The consolidation deleted D1.3's key table; the ADR's only mention of the transcript session id is D1.10's deletion list, reading *"Unused, and not a fallback."* §16 MIN-108 is marked **DISCHARGED** and the item is struck from "Next". An item that has been done but is still recorded as outstanding gets re-raised every round — which is exactly what happened to this one | §16 MIN-108, §12, "Next" |
| **M11-tail** — Jim's `browser_evaluate` grant was made under a premise D1.10 changed | **ACCEPTED as a flag, and deliberately NOT decided here.** §1's out-of-scope line (*"the seeded roster is unchanged; Jim and Ray keep it"*) and §14 rule 3's table (which lists `browser_evaluate` as leased, i.e. as an ordinary action tool) are each correct in isolation, and **neither raises that the grant's premise moved**: before D1.10 Jim's arbitrary JS ran against his own context; under D1.10 it runs against a browser holding the operator's live logins for every site the workspace has visited, and under D1.9a it can be asked onto the operator's own tab. One sentence is added to §1's bullet recording that the scope line is a statement of scope, not a re-examination of the grant, and pointing at ADR §6, which owns the question | §1 out-of-scope |
| **O1** (positive) — ADR §9.1's "goes further without conflicting" list is correct | **CONFIRMED, no action beyond the one exception.** FR-040a, FR-041, FR-042a, FR-043a and FR-016b are genuine extensions the ADR was written to match. The single exception §9.1 names is real and is actioned: **FR-042a's *"live omnipus-owned pid ⇒ terminate it"* becomes *the per-key launch lock is the discriminator*** (D1.8), because a live Chrome pid is present both for an orphan and for a second gateway running normally on the same `$OMNIPUS_HOME`. §12 A20 is corrected in place | FR-042a, §12 A20, US-19/AC2b |
| **m1, m3, m5, O2, O3** | **NOT DISPOSITIONED — text not available to this revision.** The consolidated grill was returned in-session and not written to disk; the brief that reached this document carried C1–C4, M1–M3, M7–M11, m2, m4 and O1/O4, and scoped M4–M6 and M11's body to other owners. **These five are recorded as outstanding rather than silently omitted**, so a reader does not read this table as complete. They should be re-run against this revision | — |

### 17.3 Round-3 dispositions, translated to the current ADR numbering

Round 3 reviewed revision 3 against the **pre-consolidation** ADR. Its five CRITICALs and nine
MAJORs largely anticipate round 4; each is mapped here so the older review is not re-run against
a document that has moved.

| Round-3 ID | Old ADR ref | Current ref | Disposition |
|---|---|---|---|
| **C-301** — admission policy inverted (refusal vs eviction) | D1.1c | **D1.7** | Same finding as round-4 **M1**. Accepted in full; see §17.2 M1 |
| **C-302** — FR-046 (REST close + SPA control) is withdrawn | D1.1c | **D1.7** | Accepted in full. FR-046, US-18, test 59, the *close-is-not-deletion* scenario and Stream E's path ownership are tombstoned; **SC-007 condition (2) is reverted** to its unamended form and §5's added-path carve-out is deleted. FR-047 confirmed unaffected |
| **C-303** — `--renderer-process-limit` absent, and its security precondition false here | D1.1b | **D1.5 + D1.6** | Accepted in full, and **the ADR has since moved further in the direction the finding argued**: D1.6 now makes R a site-isolation **floor** rather than a memory knob and retires the *"semi-trusted destinations"* premise by name. FR-055 adds the flag through the launch seam; §12 A23 records the URL posture against **this** codebase (`ValidateURL` blocks five schemes plus SSRF and nothing else; no allow-list exists in `pkg/tools/browser/`), rather than paraphrasing the ADR's assumed one. Round-4 **C4** then supplied the missing term: R's floor is `max_tabs`, not the never-seeded `max_total_tabs` **⚠️ SUPERSEDED BY ADR D1.5a — the finding is DISSOLVED, and this is the rare case where deleting a feature removes a security cost.** The URL posture this row established against **this** codebase is what made the flag indefensible and is kept verbatim in §12 A23. D1.5a stops setting the flag at all, so full site isolation applies to every page unconditionally — strictly stronger than the floor FR-055 specified. **No residual trade-off, no compensating control, nothing for a future reviewer to re-litigate.** Say so in the PR body. |
| **C-304** — G-1 measures the retracted metric (RSS) | D1.1b | **D1.4 / §8** | Accepted in full. §0.3, FR-044, SC-012 and §12 A15 all move to **PSS**, name the tools that can produce it (`smem`, `smaps_rollup`) and state that `ps` cannot. §12 A22's 74–268 MB figure is **not re-derived**: it is labelled retracted-RSS and the TTL's derivation is restated so it depends on the *ordering* (a renderer costs more than the marginal browser process), not the magnitude. ADR §8 carries this as an open row pointing at this spec's line 56, which is now closed |
| **C-305** — `max_browsers` shipped as a constant vs derived | D1.1b | **D1.5** | Accepted in full — same as round-4 **M7(b)**. FR-056 respecifies it as a derivation with `tools.browser.max_browsers` as `operator_ceiling`; test 51 is rewritten to catch a hardcoded target; test 73 tests the **formula** against fixture memory values on the established `meminfo_*_test.go` pattern; **`gateway_reserve` is added as a named quantity** (it was in the ADR formula and nowhere in this spec); and §6 states which way the reuse resolves — `pkg/config` **exports** a memory-budget accessor, because `availableRAMBytes` and the `meminfo_linux.go` readers are unexported and a spec that assumes an unexported symbol is callable is a plan that does not compile **⚠️ SUPERSEDED BY ADR D1.5a — there is no `max_browsers`, constant or derived.** FR-056 and test 73 are tombstoned; test 51 is re-derived onto the one figure that survives (`PER_BROWSER_COST` ≈182 MB, FR-062). **One part of this disposition survives and now blocks the only remaining control:** `availableRAMBytes` (`pkg/config/config.go:655`) and the `meminfo_linux.go` readers are still **unexported**, so `pkg/tools/browser` cannot call them — the export is now a prerequisite of FR-057, not of a deleted formula. **And a gap this row could not have seen:** off Linux those readers return **0** by design (`meminfo_other.go`), so the gate has no signal at all on macOS or Windows — §0.5 **E-6**. |
| **M-301** — thrash detection entirely missing | D1.1c | **D1.7** | Accepted. **FR-054**, test 71, US-15/AC8+AC8a, and **G-5** added to §0.3 with its constants explicitly gated on it |
| **M-302** — the cap is soft; three places assert it is hard | D1.1c | **D1.7** | Accepted. INVARIANT P-2 restated as `target + overshoot`; **SC-010 rewritten**; **test 63 split** into the evictable and all-pinned paths; the soft-target wording carried into the config doc comment as **FR-053 + test 77**, since ADR criterion P14 makes it a stated requirement |
| **M-303** — one eviction guard has no mechanism | D1.1c | **D1.7** | Accepted, with the grill's arithmetic: §14's exempt set is **six**, so a `browser_screenshot`/`get_text`/`wait`/`list_tabs`/`snapshot`/`handle_dialog` holds no lease and the pool would evict Chrome out from under it. **FR-051** adds `InFlight()`, incremented by **every** `browser_*` `Execute` and released by `defer`; the selection race is specified in §3.1's locking discipline (increment under the same `pool.mu` selection holds); test 68 is the `-race` case, deliberately on a lease-**exempt** call |
| **M-304** — the admission pressure gate is absent | D1.1b | **D1.5 item 3** | Accepted. **FR-057** with fixture tests at 0.84/0.85/0.86 and a non-Linux no-op. **The contradiction the finding identified is escalated, not resolved:** "refuse to grow" and "always evict-and-launch" cannot both hold when pressure is high and nothing is evictable, the ADR does not decide it, and neither does this spec — §0.5 **E-2**, recorded the way A17 and A20 are, and test 72 deliberately does **not** assert that case |
| **M-305** — the gate list is stale; four more prerequisites | D1.1b/c | **D1.5, D1.7** | Accepted. §0.3 is **six gates** (G-1…G-6) with a summary table, each mechanical or named-human with an explicit failure condition, and matching SC rows (SC-012, SC-012a, SC-019, SC-020, SC-021). The two Chromium questions are **FR-057a** and are specified as one cgroup-capped run each, not prose |
| **M-306** — `max_total_tabs` and the renderer cap guard the same resource | D1.1b | **D1.5 item 4** | Accepted. §12 A21 now states the **three-guard** split explicitly and **withdraws the memory argument** for the global tab budget: it was built on the retracted RSS figure, and D1.5 shows a tab is not a process. It stays global on a *surprise* argument (per-Chrome would silently multiply a configured ceiling by N), which is the argument that actually holds. Test 53 asserts the guards are enforced independently **⚠️ SUPERSEDED BY ADR D1.5a — all three guards are deleted.** Test 53 is tombstoned. **What survives is the finding underneath it, and it survives with stronger evidence:** *a tab is not a process*, so nothing may be priced per tab — since measured at **2 tabs against 13 renderer processes**, ~6 per tab (FR-062). §12 A21 is rewritten to record that rather than the three-guard split. |
| **M-307** — the spec asserts its own currency and is wrong about it | — | — | Accepted in full. The header now **pins the ADR revision** (consolidated 2026-09-01, commits `22ceff6f1` and `809555fcf`), the status line is corrected (six gates, two absorbed rulings, no "all design questions are decided"), **§0.5** records what the post-revision rulings changed and what is escalated, and §16 CRIT-103's *"§5 forbids eviction and that stands"* is annotated **in place** rather than left reading as current |
| **M-308** — the boot-orphan guarantee is stated three ways | D1.1d | **D1.8 + D1.9** | Accepted, and the finding's own correction is adopted: the guarantee is **Linux**-only, not "POSIX-only" (macOS *is* POSIX) — which is what §12 A20 already said and what the consolidated D1.9 now says too. US-19/AC2a and **SC-016** are platform-qualified; see round-4 **M9** |
| **M-309** — the pool-full message names an ineffective remedy | D1.1c | **D1.7** | Accepted by deletion: the message goes with FR-039. Its discipline is generalised as **SC-022** — every surviving operator-facing capacity text must name an action the reviewer traces to its implementing function. Applied to FR-054's WARN, where it bites again: "raise `max_browsers`" is a **ceiling** raise and does nothing when the derived target is binding, so the WARN must say which term binds **⚠️ AMENDED BY ADR D1.5a, and the row's own advice became the defect it warns about.** Its final sentence prescribed that FR-054's WARN *"must say which term binds"* between the ceiling and the derived target — advice that, one ruling later, would have shipped a message naming a **deleted** key. There is now exactly one binding term (memory), so the ambiguity is gone; **SC-022 is rewritten to forbid naming any config key at all**, and it now covers a third message this row did not anticipate: FR-063's model-visible tab-refusal note, which is what an *agent* reads when the host is out of memory. |

**Round-3 + round-4 tally:** 14 round-3 findings, **all accepted** (3 of them resolved further
by rulings that postdate the review). 24 round-4 findings: **17 accepted in full or with a
stated correction to the finding's own evidence**, **2 dispositioned as tails only** (M6, M11)
with the rest scoped to other owners, **4 explicitly out of this document's ownership** (M4, M5
and the bodies of M6, M11), and **5 not dispositioned because their text did not reach this
revision** (m1, m3, m5, O2, O3) — recorded as outstanding rather than omitted.

**Three defects this revision found while fixing others, which neither review named:**
`cfg.MaxTabs` becoming a per-team cap (FR-049) and then turning out to be the tab count C4
needed (FR-055); the flat profile path dissolving INVARIANT P-5 rather than merely renaming a
directory (FR-037a); and the two idle TTLs composing into ~20 minutes of slot occupancy once
eviction makes occupancy a capacity term (§12 A22).

**Next:**

1. **Run the four cheap gates in parallel** — **G-2** under §0.3.1's four conditions (as its own step in the existing `browser-e2e` job, which already exports `OMNIPUS_BROWSER_E2E=1`), **G-3** and **G-4** as one cgroup-capped Chrome run each, and **G-1** with SC-012's artefact **in PSS**.
2. **Land Stream A**, including **FR-048 in the same commits as FR-001** — a commit that re-keys the manager without carrying the agent dimension on the tab set ships a state D1.9a forbids and ships it silently — plus the §2.2a 364-reference migration in the same commits as FR-002b, and the rest of the §0.4 set with FR-047 among it.
3. **Then build Stream P**, behind G-1/G-2/G-6, with FR-034a's final description literals in the same commit as FR-037.

**Cross-document obligations that must be settled before implementation:**

- The **D2 spec** deletes its lease and references §14. §14's rescope (agent-vs-agent on the operator's tab only) makes that a smaller edit than it was, but it is still required.
- **`browser_handle_dialog` ungated by `controlledResult`** is no longer a request from this spec to D2 — **ADR D1.8 rules it**, with the ADR-038 D6 narrowing stated explicitly (§12 A17). D2 implements it.
- **§0.5's escalations need answers, and the list has moved twice.** **E-2 is now RULED** by ADR D1.5a — the pressure gate is a hard stop, the cap is soft, and with the cap deleted only the hard stop remains — so the implementation no longer has to avoid picking, and test 72 asserts the case revision 4 deliberately left unasserted. **Five remain live**, four of them the operator's or a downstream document's: **E-1** the "take control on request" verb (D2 surface), **E-3** `browser_snapshot`'s reachability under D1.9a (D2), **E-4** the three ADR §6 rows (ADR owner), **E-5** whether a per-tab headroom floor is needed (operator / G-1's Linux pass), and **E-6 — new, and the most consequential — the live-memory gate has no signal at all on macOS or Windows** (`readMemAvailableBytes` returns 0 by design, `pkg/config/meminfo_other.go`), so on two of three supported platforms the only admission control that exists cannot run. Revision 4 could accept that because a counter still bounded the pool there; D1.5a deletes the counter. **Operator ruling required** — implement a Darwin/Windows reader, ship browser support Linux-only, or accept an unbounded pool off Linux and say so in the release notes.

**Three ADR edits are outstanding and belong to the ADR's owner** (this spec does not edit the ADR):

1. ~~**D1.6 names the wrong tab count.**~~ **DISCHARGED BY D1.5a — do not re-raise.** It derived R from *"a tab count is enforced (`maxTotalTabs`)"*, which is never seeded (`grep MaxTotalTabs pkg/config/defaults.go` → nothing; `coordinator.go:240-253` logs *"global tab budget: UNLIMITED"* when `<= 0`). The correction was `max_tabs`. **D1.5a then deleted both keys and the flag R configured**, so the ADR edit this row asked for is void: D1.6 is marked withdrawn in the ADR rather than corrected.
2. **ADR §6 should close two of its own open rows**, citing FR-016a (capture session per workspace) and — **corrected by D1.5a** — **D1.5a itself** for the instances-vs-bytes row: the answer is now **bytes and nothing else**, since D1.5 item 2 (instances for a target) is deleted and only item 3's gate remains. The third row — per-workspace profile disk — is **reopened** here and correctly stays open (§12 A24(b), §16 MAJ-111).
3. **ADR §8's open row against this spec is now closed** — G-1 is specified in PSS — and the row can be swept. **A fourth edit is now outstanding:** ADR D1.5a's "17 references in `pkg/tools/browser/`" for `max_tabs` is re-counted here as **18** (§0.6's count note); the difference is a classification question, not a disagreement, but the ADR should carry the checkable command rather than the bare number.

**~~D1.3's key table~~ — DISCHARGED, do not re-raise.** Revision 3 carried this as an outstanding ADR edit for three rounds. The consolidation deleted the table; the ADR's only mention of the transcript session id is D1.10's deletion list, reading *"Unused, and not a fallback."* §16 MIN-108 is marked discharged (§17 m4).

**SC-015 is satisfied** — ADR-072's **header block** (not D1.1a, which no longer exists) names Daniel Piatkowski as decider for every ruling in that document, in an unenumerated form that survives renumbering.

---

## Relationship to issue #509 / ADR-048 Option B — MUST be closed when this lands

**This work implements what #509 describes.** That issue — *"Per-agent browser
isolation compatible with WebRTC capture (ADR-048 Option B)"* — specifies
"one Chrome instance (own user-data-dir) per browser-capable agent, so each
agent's tabs sit in that instance's own default context (capturable) while
remaining isolated". That is this design, with **workspace** where #509 says
**agent**.

**#509 was closed `NOT_PLANNED` on 2026-08-19.** This work revives a
deliberately declined option, which is a reversal that must be visible rather
than implicit.

**Required actions, both of them:**

1. **Reopen #509 now** (or file a successor referencing it) so the trail is
   honest while this is built. A design that silently revives a not-planned
   issue leaves the next reader unable to tell whether the decline still holds.
2. **Close #509 when this lands**, citing the implementing PR. It is the
   issue this work completes, and leaving it closed-as-not-planned after
   shipping the thing it asked for makes the tracker lie in the other
   direction.

**One piece of evidence from #509 that strengthens this design and was not
previously recorded here:** CDP-created browser contexts fail capture for
**two** independent reasons, not one — `chrome.tabCapture` returns "Invalid tab
specified", **and** `chrome-extension://` pages will not load in them at all
(`ERR_BLOCKED_BY_CLIENT`). Both verified on real Chrome 150 (commit
`687c7c6e`). The second reason is the more final of the two.

**And one existing constraint #509 records that this design must reconcile
with:** today's v1 is "fenced to effectively single-browser-agent use — capture
start is denied when another agent has live tabs". A pool of N browsers changes
that fence's meaning. **Not yet verified against code in this document** — the
adjacent comment is `pkg/tools/browser/capture_session.go:839`; the precise
enforcement path must be found and reconciled before implementation.
