# Spec — Browser ownership: workspace-scoped browsers (ADR-072 **D1**)

- **Source ADR:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions.md`, **consolidated revision of 2026-09-01** (commits `22ceff6f1` "consolidating rewrite — one document, each decision stated once", `809555fcf` "D1.9a", `86666daaa` "D1.9b — four operator rulings closing the open questions" and **`c1f21da69` "D1.9c tabs belong to sessions (supersedes D1.9a); D1.9d delete the decoy flag"**). **D1 only — D1.1 … D1.13** in the consolidated numbering, plus the write lease the ADR files under D2.10 (see §14) and the two D2 sections that place obligations here (D2.9, D2.11).
  ⚠️ **The consolidation RENUMBERED every D1 section.** Revision 3 of this spec cited the pre-consolidation numbers, several of which now resolve to *different* content. **§0.0 carries the full old→new map and is the only place it lives.** Do not re-derive it by find-and-replace.
- **Round-1 ADR review folded in:** `docs/internal/architecture/ADR-072-workspace-scoped-browser-sessions-review.md`.
- **Round-1 spec review folded in:** `docs/internal/specs/browser-workspace-ownership-spec-review.md` — verdict **BLOCK**, 29 findings. Every one is dispositioned in **§15**; one is rejected with evidence and says so there.
- **Round-2 spec review folded in:** `docs/internal/specs/browser-workspace-ownership-spec-review-round2.md` — verdict **BLOCK**, 29 findings (4 CRITICAL / 14 MAJOR / 8 MINOR / 3 OBSERVATION). Every one is dispositioned in **§16**; three are rejected or narrowed with evidence and say so there. *(Both review files use the same finding-id prefixes; round-2 numbers them from 101 to keep them distinguishable. §15 and §16 are separate tables and neither supersedes the other.)*
- **Round-3 spec review folded in:** `docs/internal/specs/browser-workspace-ownership-spec-review-round3.md` — verdict **BLOCK**, 14 findings (C-301…C-305, M-301…M-309). Dispositioned in **§17**. That review was written against the *pre-consolidation* ADR (it cites `D1.1b`/`D1.1c`/`D1.1d`), so §17 restates each finding against the current section numbers.
- **Round-4 spec review folded in:** verdict **BLOCK**, 24 findings (4 CRITICAL / 11 MAJOR / 5 MINOR / 4 OBSERVATION), raised against the consolidated ADR. Dispositioned in **§17.1–§17.2**. *(Ids `C1…C4` / `M1…M11`.)*
- **Round-4 second pass folded in (2026-09-02):** a further grill of this document against the D1.9b/D1.5e revision, returned **in-session and not written to disk** — cite it as *"round-4 second pass, 2026-09-02, returned in-session"*, and do not go looking for a file. Ids `C-401…C-403` / `M-401…M-406` / `m-401…m-406`, deliberately prefixed to stay distinguishable from round 4's own `C1…C4`/`M1…M11`. Dispositioned in **§17.4**. **Its C-402 is the finding ADR D1.9c answers**, and the answer this document gives is narrower than the ADR's own — see §17.4.
- **Amends:** **ADR-043 D1** (one shared Chrome for the process — *this spec replaces it with a pool*), **ADR-043 D2** (per-agent CDP browser context — *replaced by per-workspace Chrome profiles*) and **ADR-043 D3** (live-view binding). Read ADR-043 first; D1 has the largest blast radius of anything in ADR-072.
- **Sibling spec:** D2 (capability). **This spec owns the write lease — §14 is its single normative definition.** The D2 spec must delete its own lease FR/US/stream/test and reference §14 (operator ruling, 2026-08-31).
- **Worktree:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/wt-browser-perf` · **Branch:** `feat/browser-streaming-performance`
- **Status:** Draft for implementation, **gated on six measurements** (§0.3). Operator rulings postdating revision 3 are absorbed here (D1.7's eviction policy, ~~D1.9a's tab ownership~~ **D1.9c's session-owned tabs**, D1.5a–D1.5d's memory rulings, and **D1.9b's two rulings that land on this document — §0.7 and §0.8**); the design questions they reopened are closed, and the ones this spec cannot close are listed in §0.5 as escalations rather than assumptions. **After D1.9b, E-1 and §12 A24(b) are ruled and one new escalation is opened (E-9, the continuously-driven profile cache).**
- **Operator rulings folded in (Daniel Piatkowski):**
  - *2026-08-31* — workspace is the isolation axis, not the agent and not the conversation (**D1.2**); **isolation is by Chrome process + profile directory, not by CDP browser context** (**D1.4**); **every agent on a workspace shares its browser and its logins, including unattended delegated work** (**D1.10**, superseding the earlier same-day ruling); every turn runs in a workspace, no workspace-less fallback (**D1.11**); the browser seed stays Jim + Ray only; the write lease belongs to this spec.
  - *2026-08-31, later* — **the cap manages itself (D1.7): at the cap the pool evicts the least-recently-used instance and launches. There is no "pool full" error surface and no UI change.** This **reverses** revision 3's refusal design in full (§17 C1/M1).
  - *2026-09-01* — **tabs stay per agent; the operator's tab is the shared one (D1.9a).** Verbatim: *"the default is they open a new tab — we have in the current version that an agent has its own tab, we should maintain that. Only if the user starts a tab are the agents able to see it and take control on request."* This rescopes the write lease from "every action tool" to "agent-vs-agent on the operator's shared tab" (§14).
  - *2026-09-01, later* — **the memory gate is the ONLY admission control; idle tabs and idle browsers are closed; every other limit is deleted from the codebase (ADR D1.5a).** This is not a config change: `tools.browser.max_tabs`, `tools.browser.max_total_tabs` and the reservation machinery that enforces them are **removed from the code**, and the proposed `max_browsers`/`operator_ceiling` and `--renderer-process-limit` are never built. **§0.6 is the single place this ruling is stated**; everything downstream of it is re-derived or tombstoned there and in the sections it names.
  - *2026-09-01, later still* — **ADR D1.9b, four rulings.** Two land here: **taking the operator's shared tab is IMPLICIT — an agent acquires it by acting on it, there is no "take control" tool** (§0.7, closing E-1), and **profile disk is bounded by PERIODIC CACHE TRIMMING, not a quota and not deletion alone — logins preserved, disposable cache trimmed on a schedule** (§0.8, closing §12 A24(b) and §16 MAJ-111's "no quota" clause). The other two — `browser_evaluate` seeded **enabled**, and `browser_snapshot` at **Tier 3** — land on the sibling **D2 capability spec**; this document updates its references to them and creates no requirement for either (§0.7 opening).
  - *2026-09-02* — **tabs are owned by the SESSION, not the agent (ADR D1.9c), superseding D1.9a.** Verbatim: *"it should not be per agent but per session, no matter which agent is on it. Tabs are owned by sessions."* One browser per workspace and one cookie jar are unchanged; **only the tab-ownership key moves, agent → session** (§0.2a, FR-080, tombstoning FR-048). The operator's own tab stays **workspace**-owned and acquisition stays implicit — both halves of D1.9a that D1.9c preserves. **The residual it names is established here from code rather than inherited: two concurrent turns in one session CAN occur** (three paths), so §14's general lease case is **rewritten, not deleted** (FR-081), and the new escalation **E-10** records that this spec must not depend on issue #505 fixing it.
  - *2026-09-01, on FR-068a* — **"the windows refusal is fine for now, we are not supporting windows yet."** The browser pool's refusal on Windows is **accepted on the platform's unsupported status**, not softened and not re-argued technically. The same ruling forces the distinction §0.9 records: **one `ok=false` predicate, two consumers, two different correct responses** — the pool refuses to grow, agent admission holds at the conservative floor of 2 — and **gVisor is Linux, supported, and not the Windows case**.

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
| `D2.8`, `D2.9`, `D2.10`, `D2.11` | — | Tier, policy seed, concurrency, security | **unchanged** — but see **D2.9a** (2026-09-02, commit `0be8988ac`), which CORRECTS a widely-repeated claim about the policy seed: a registered tool with **no** seeded policy does **not** abort boot. §0.7's table below records what that changes here |

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
| ~~**D1.9a**~~ | ~~**Tabs stay per agent; the operator's tab is the shared one**~~ — **SUPERSEDED by D1.9c** for the agent half. Its **second** half stands: the operator's tab is workspace-owned | ~~FR-048~~, ~~FR-049~~, §14, §17 C1 |
| **D1.9c** | **Tabs are owned by the SESSION, not the agent** — supersedes D1.9a's first half; one browser per workspace and one cookie jar unchanged. **The residual it defers to this spec — two concurrent turns in one session — is answered YES from code (§0.2a), so §14's general lease case survives** | **FR-080**, **FR-081**, §0.2a, §14, §0.5 E-10 |
| **D1.9d** | The dead `Tools.Browser.EvaluateEnabled` field, its JSON tag and its env var are deleted outright (one live switch, not two) | **Lands on the sibling D2 capability spec** — this document creates no requirement for it; noted so a reader does not go looking for one here |
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

### 0.2a Tabs are owned by the SESSION — the re-key that D1.9c replaced, and the arbiter it does NOT remove

**Operator ruling, 2026-09-02 (ADR D1.9c), verbatim:** *"it should not be per agent but per
session, no matter which agent is on it. Tabs are owned by sessions."*

**This supersedes D1.9a**, whose per-agent ownership this section used to specify and which
FR-048 carried. One Chrome per workspace and one cookie jar (D1.3, D1.10) are **unchanged**.
**Only the tab-ownership key moves: agent → session.**

| | Owner | Who can see it | Who can drive it |
|---|---|---|---|
| A tab an **agent** opens | the **session** the turn ran in | every agent on that session — *the tab stays with the chat, not with whoever is on it* | any agent on that session |
| A tab the **operator** opens | the **workspace** | **every agent on the workspace** | the operator; an agent **on request** |
| — *superseded* — a tab an agent opens | ~~that agent~~ | ~~that agent alone~~ | ~~that agent alone~~ |

**This fixes ADR §1.1's defect twice over, and the second half is new.** §1.1 records the operator
browsing in the panel, switching the chat from Mia to Jim, and Jim reporting zero tabs. D1.9a fixed
only the *operator's* tab, by making it workspace-owned. **D1.9c also fixes the agent's tab**:
switching the chat from Mia to Jim does not change the session, so **Jim sees the tab Mia opened in
that chat** — it was never Mia's. Under D1.9a it still was, and the operator would have got the
right answer about their own tab and the wrong one about Mia's, in the same reply.

#### The trap, re-derived under the session key rather than carried forward

Today's tab set already belongs to a **session**, nominally: `BrowserManager.sessions` is
`map[string]*sessionEntry` (`manager.go:338`) and each `sessionEntry` owns `tabs []*tabEntry`
(`manager.go:203-204`), reached by session id. **The field name was right and the value was a
constant** — every tool hardcodes `DefaultSessionID = "default"` (`tools.go:63`, ADR-041 D1), so
the map has exactly one live key and agents are separated **only** by each having its own
manager.

So the shape of the trap is unchanged from D1.9a's, and only its remedy is simpler. FR-001
collapses managers to one per workspace; a re-key that leaves `DefaultSessionID` in place gives
every session on the workspace **one shared tab set**, silently. **The difference is that D1.9c's
remedy needs no new dimension** — it needs the map's existing key to carry the turn's real session
id instead of a constant, which is work FR-002b is already doing for all 37 consumers.
**FR-080** states it; **FR-048 is tombstoned** (§9) rather than amended, because its subject —
the agent dimension — no longer exists.

#### Which session id, and this is not a free choice

**`transcriptSessionID`, never `routingSessionID`.** Both exist on a turn and a delegated child
carries different values for them (ADR-057 D2/FR-011): `spawnSubTurn` gives the child its own
`TranscriptSessionID: childID` (`pkg/agent/subturn.go:1282`, whose comment reads *"FR-007: the
child's OWN session id, not the parent's"*) while **inheriting** `childTS.routingSessionID =
parentTS.routingSessionID` (`:1339`).

Two reasons, and the first is a prohibition already written into the code:

1. **`routingSessionID`'s doc comment forbids exactly this use.** `pkg/agent/turn.go:348-353`
   states it *"MUST NOT be read for any purpose other than routing/interrupt scoping — never as a
   session store key … **ownership predicate** … uploads-directory key"*. Tab ownership is an
   ownership predicate, by name.
2. **It would re-merge a whole delegation subtree.** `routingSessionID` is *"inherited VERBATIM
   through an entire delegation subtree — for a grandchild it equals the ROOT's own session id"*
   (`pkg/agent/turn.go:322-364`). Keying tabs on it would give a root turn and every descendant
   one shared tab set — and `delegate`'s `async` defaults to **true** (`pkg/tools/delegate.go:1298`)
   with `executeAsync` detaching each spawn onto its own goroutine (`:1853-1856`), so N `delegate`
   calls in one turn produce N children running **concurrently** on that one merged set. That is
   the very collision D1.9c is credited with closing, reintroduced by picking the wrong id.

Keyed on `transcriptSessionID`, **parallel delegation is safe by construction**: siblings hold
distinct ids and therefore distinct tab sets.

#### The residual D1.9c names — established from code, and the answer is YES

The ADR says the residual *"must be specified rather than assumed away"*: two concurrent turns
**in the same session** would still contend, and *"whether that can occur is a question about
session-level turn serialisation, not about the browser."*

> **It can occur. Three live paths do it, and none of them is exotic.**

**There is no per-session turn lock.** `runAgentLoop` (`pkg/agent/loop.go:7749-7791`) contains no
admission check, no lock and no session-busy test — anything that reaches it starts a turn on
whatever session id it was handed. What serialisation exists is one **`sessionWorker` goroutine
per scope** (`pkg/agent/session_worker.go:29-32` — the `sessionWorker` doc comment; `:33` is the
struct it documents — scope resolved at `pkg/agent/loop.go:7546-7550`),
and it is structural rather than a lock: `runLoop` calls `w.processTurn` inline (`:197`; `:206` is
`processTurn`'s own declaration), and a
message arriving mid-turn is folded into the running turn as steering rather than run beside it
(`:117-124`). **It only protects the bus path.** Three paths bypass it and call `runAgentLoop`
directly:

| Path | Session id it runs on | Why it collides | Evidence |
|---|---|---|---|
| **`/loop`** | **the user's own live chat session id** | The chat id is stored as the cron payload and handed back to `ProcessScheduled` on every tick, while the user may be mid-turn in that same session through the worker. Cron's overlap guard checks `job.State.Running` (`pkg/cron/service.go:604-613`) and knows nothing about a user turn | `pkg/agent/loop_command.go:90`, `pkg/agent/loop_scheduler.go:118`, `:215` |
| **Async system-notify** (a delegate completion notifying its origin) | **the origin chat session id** | Dispatched as a bare `go func()` before the worker logic is reached; binds `transcriptSessionID` from the message and calls `runAgentLoop` | `pkg/agent/loop.go:3510` (the `msg.Channel == "system"` branch) → `:3516` (the bare `go func()`), then `:7640-7643`, `:7734`. *(`:3512` is a comment line inside the `activeRequests` note, not the dispatch — corrected round-5.)* |
| **cron `SessionModeMain`** | `"sched-main-" + owner` — **shared across different jobs** | The overlap guard is strictly per job, and dispatch spawns a goroutine per due job (`pkg/cron/service.go:568-590`), so two `main`-mode jobs owned by one agent run together on one id | `pkg/gateway/schedules.go:548` (`:546` is blank — corrected round-5) |

**The second row is not an inference — the code says so, and files it as a known defect.**
`pkg/agent/loop.go:3491-3510`, verbatim:

> *"…this goroutine **CAN run a real turn concurrently against the SAME origin session as a live
> user turn** (unlike every other inbound message, which IS serialized per session via the
> sessionWorker pool below). … **the single-writer-per-session invariant other turn types rely on
> no longer holds for this specific path.** See #505 for the suggested follow-up."*

**Two consequences, and they point in opposite directions from the ADR's expectation.**

**(a) The collisions D1.9c was credited with closing ARE closed.** A heartbeat runs on a
dedicated standing session minted per (workspace, agent) and reused every fire
(`pkg/gateway/rest_workspaces.go:1236` mints it via `pkg/session/unified.go:696-697`'s
`NewHeartbeatSession`, one per (workspace, agent); `pkg/gateway/heartbeat_schedule.go:216-217`
makes the heartbeat job **`SessionModeContinue` with that id pre-set**, and
`pkg/gateway/schedules.go:516` → `:530` → `:543-545` looks it up and reuses it on every fire), so a
heartbeat firing while a chat is live is now **two sessions, two tab sets**. *(Round-5 F-1: this
cited `schedules.go:527-543`, which spans the wrong sub-branch — `:517-529` is the `job.SessionID
== ""` fresh-mint case, not the reuse path a heartbeat takes — and named no source for the
continue-mode wiring that makes the reuse happen at all.)* Parallel delegation is two sessions per §"Which session id" above.
Both of C-402's named cases are gone by construction, exactly as the ADR says.

**(b) The general lease case is NOT dissolved, and §14's scope table must say so.** Its third row
currently reads *"nothing — it cannot occur"* on the premise that no two turns can address one tab
set. Under D1.9a that premise was false for a different reason (one agent, two turns); under
D1.9c it is false for this one (one session, two turns, three paths). **The row is rewritten, not
deleted** — §14 — and its supporting scenario is rewritten with it, because the scenario currently
*asserts the premise* and therefore passes while the hole is open.

> **Citation receipt (round-5, re-verified against this worktree 2026-09-02).** Four citations in
> the argument above had drifted and are corrected in place: the heartbeat standing session
> (`schedules.go:527-543` named the `job.SessionID == ""` sub-branch, not the reuse path),
> `"sched-main-" + owner` (`:548`, not `:546`), the async system-notify dispatch (`loop.go:3510` →
> `:3516`; `:3512` is a comment), and `session_worker.go`'s worker doc (`:29-32`, not `:33-36`) and
> its inline `processTurn` call (`:197`, not `:206`). **The ruling's foundation is unaffected** —
> `loop.go:3491-3510` (#505's own comment), `turn.go:348-353`, `subturn.go:1282`/`:1339` and
> `delegate.go:1298` each reproduce exactly. *Recorded rather than silently fixed because a
> citation that resolves cleanly to the wrong lines is the failure mode §0.0 warns about, and
> three of these four sat inside the argument a reader is asked to check.*

**What the arbiter is.** Not a new mechanism: **§14's existing write lease, applied to the
session's tab set as well as the workspace's** (FR-081). The lease is mutual exclusion per
**(`BrowsingKey`, `TabOwner`) pair** — never per browser, which would make two unrelated chats on
one workspace block each other (§14.1's `acquireWrite`; test 99(b)) —
held for the duration of one action-tool call; widening *when it is consulted* costs nothing at
the call sites and no new primitive.

**What this spec does NOT do about #505.** Routing those three paths through the `sessionWorker`
would remove the contention at its source and is the right long-term fix — it is #505's own
proposal. **It is not in this spec's scope**, it is not a browser change, and this document must
not depend on it: the lease has to hold whether or not #505 is ever done. Recorded in §0.5 as
**E-10** so the dependency is visible rather than assumed.

#### What this ruling does NOT change — stated explicitly, because two halves of D1.9a survive it

- **The operator's own tab stays WORKSPACE-owned** and visible to every agent on the workspace,
  not session-owned. `TabOwnerWorkspace()` is untouched. **This is the half that fixes §1.1's
  reported defect** and D1.9c preserves it deliberately: a tab the operator opened is not the
  property of whichever chat happened to be on screen.
- **Acquisition of that tab stays IMPLICIT** (D1.9b ruling 1) — no tool, no policy entry, no wire
  field (§0.7, FR-070, FR-071). D1.9c touches the ownership key, not the acquisition mechanism.

**Second-order consequence, and it is now moot for the same reason it was under D1.9a.** The
per-agent tab cap (`BrowserConfig.MaxTabs`, default **5** at `manager.go:36` and `:124`; config
key `tools.browser.max_tabs`, `config.go:3633`) was enforced by `totalTabCountLocked`
(`manager.go:1549-1555`), which sums `len(se.tabs)` across **every** session in the manager. Under
one manager per workspace that would have become 5 tabs for the whole team — the tightening
**FR-049** was written to prevent. **`tools.browser.max_tabs` is DELETED from the code** by ADR
D1.5a, so there is no cap to re-own and FR-049 stays tombstoned (§0.6, §9). Ownership — *whose tab
set is this* — is a correctness property and is untouched by that ruling; only capacity moved.

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

- one manager per workspace, with **per-SESSION tab sets inside it and a workspace-owned tab set for the operator** (FR-001, FR-002, FR-002a, FR-002b, FR-002c, **FR-080**; ~~FR-048~~ re-keyed by D1.9c) — **this alone fixes ADR criteria 2 and 3**, because handover is broken by the *manager* split, not by any partition, and FR-080 is what stops the fix from merging every session's tabs on the workspace;
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
**Three are live** (E-3, E-4, E-5), **plus one that D1.9b's ruling 4 opens in the act of closing another — E-9**; **E-1, E-2, E-6, E-7 and E-8 are ruled and kept struck through**
so a reader arriving from an earlier review sees each answered rather than dropped. **E-6 was the
most consequential of them** — it asked whether the one control that remains can run at all on two
of the three supported platforms — and **ADR D1.5b (2026-09-01) answers it: the macOS reader is in
scope and must be written; Windows is foreseen and explicitly NOT in scope. See §0.6a.**
**E-7 was E-6's own consequence** — writing that reader also moved a shipped default outside the
browser — and **ADR D1.5c and D1.5d (2026-09-01) rule it, by dissolving both of the shapes it
offered rather than choosing between them.** See §0.6b. **E-8 was E-7's successor and it lived for
part of one revision:** it asked who ratifies a cross-domain change a browser ruling authorised, and
the operator answered directly — **scope sign-off 2026-09-01, ADR commit `ddd9789a4`** — so the
memory mechanism is **one deliverable serving both consumers**, not a browser change with an
external dependency. *This spec briefly carried an "independently landable" hedge against that
ratification; the sign-off deletes it, because a hedge that half-adopts one mechanism is two
mechanisms, which is the thing D1.5c ruled against.*

| # | The question | Where it sits now | Who must answer |
|---|---|---|---|
| ~~**E-1**~~ | ~~**What does "take control on request" look like to an agent?**~~ **RULED — no longer an escalation (ADR D1.9b ruling 1, operator, 2026-09-01), and it is ruled the CHEAPER way.** D1.9a left the acquisition mechanism — an explicit tool, or implicit acquisition on first write — as a D2 tool-surface decision, and this row priced the explicit reading. **The ruling picks implicit:** *"An agent acquires control of the operator's shared tab by acting on it; there is no explicit 'take control' call."* So the seventh tool, the seventh per-agent policy entry, the Tier-3 fixture row and the manifest arithmetic are all **withdrawn rather than paid** — the surface stays **11 → 17**, not 18. **§14 does not change shape:** what this row described as an assumption (*"§14 assumes the second shape … only as the lease's contended case"*) is now the ruling, and the annex's scope note says *is ruled* instead of *assumes*. | **§0.7 specifies it, and two new FRs carry it.** **FR-070** is the absence of a surface (no tool, no policy entry, no wire field, no result key) plus a structural assertion that a `browser_take_control`-shaped registration never appears. **FR-071** is the mitigation, and it is the load-bearing half: implicit acquisition may occur **only when no human holds the live-view lock** (`controlledResult`, `pkg/tools/browser/tools.go:962`, ADR-038 D6), asserted in the **blocked** direction because the allowed direction alone is green on a build with no lock at all. **§12 A25's open sub-question resolves with it** — closing the operator's tab *is* acting on it (`browser_close_tab` is control-gated at `tabs.go:171`), so it acquires implicitly under the same gate. | **Answered** (operator, ADR D1.9b ruling 1) |
| ~~**E-2**~~ | ~~**Pressure gate vs eviction, when they disagree.**~~ **RULED — no longer an escalation.** D1.5a settles it: *"the memory gate is the ONLY admission control"*, and the ADR states plainly that **the pressure gate is a hard stop and the cap is soft — and with the cap deleted, only the hard stop remains.** So when pressure is high and nothing is evictable, the request is **refused**, with an error naming **memory** and not a cap (the remedies differ, and an operator told the wrong one raises a ceiling that is not the constraint — and there is no ceiling left to raise). | FR-057 is rewritten to state the ruled answer instead of recording a collision; **test 72, which deliberately did not assert that case, now asserts it** (§10). | **Answered** (operator, ADR D1.5a) |
| **E-3** | **Is `browser_snapshot` reachable at all under D1.9c?** Out of this spec's scope (it is a D2 tool), but the tab-ownership ruling changes what it reads: a snapshot taken in session S sees **S's** tab set, not the workspace's. *(Re-stated 2026-09-02: under the superseded D1.9a this read "agent A now sees A's own tab". The escalation is unchanged in substance — the snapshot reads one owner's set rather than everything on the workspace — only the owner is now the session.)* | **Recorded, not decided — and D1.9b ruling 3 does NOT answer it, which is worth saying because it looks as though it might.** Ruling 3 places `browser_snapshot` at **Tier 3 (searchable only)**, consistent with the other eleven browser tools, and in doing so **falsifies D2.4's claim** that the snapshot is *"the default way an agent reads a page"* — a tool an agent must search for is not a default. That is a **reachability** ruling on the D2 tool surface. E-3 asks a different question — *what a snapshot READS* once D1.9a gives each agent its own tab set — and that is still open. **Reference updated, no requirement created here:** a tool's tier does not change whether it takes the write lease, so §14 rule 3's row for `browser_snapshot` (read-only ⇒ exempt) is unaffected. | D2 spec |
| **E-4** | **Three ADR §6 "open" questions this document has already answered.** See §12 A24 — capture-session-per-workspace (FR-016a), per-workspace profile disk (§16 MAJ-111), and instances-vs-bytes (**closed by D1.5a: bytes, and nothing else**). **Two of the three are now closed by ruling and only ONE is still live.** (a) capture-session-per-workspace remains an outstanding **ADR edit**, not a design question — §6 should close it citing FR-016a. (b) **per-workspace profile disk is RULED by D1.9b ruling 4** — periodic cache trimming, logins preserved, a hard size cap rejected by name (§0.8, FR-072…FR-074); A24(b) and §16 MAJ-111 are updated in place and the ADR's §6 row can be closed citing it. (c) instances-vs-bytes was already closed by D1.5a. | §12 A24, §0.8 | ADR owner (**three** §6 rows to sweep; no design question left) |
| **E-5** | **Is a per-tab headroom floor needed, and what is it?** FR-060 puts the pressure gate in the tab-open path, expressed as a **ratio** with no per-tab byte constant — because the ADR withdrew the 85 MB constant on measured evidence (30 MB → 327 MB in one snapshot, an 11× spread). A *browser* launch has a measured floor (`PER_BROWSER_COST` ≈182 MB, FR-062); a *tab* has none, and this spec declines to invent one. | Recorded, not decided. A ratio-only tab gate is what FR-060 specifies; if measurement later shows the ratio moves too slowly to catch a fast tab loop, a floor is a design change, not a tuning change. | Operator / G-1's Linux pass |
| ~~**E-6**~~ | ~~**The only admission control there is cannot run on macOS or Windows.**~~ **RULED — no longer an escalation (ADR D1.5b, operator, 2026-09-01).** The finding stands exactly as written: `readMemAvailableBytes` returns **0** off Linux by deliberate design (`pkg/config/meminfo_other.go`, `//go:build !linux` at `:5`, `return 0` at `:43`) and `readCgroupMemoryAvailableBytes` returns `(0, false)` (`:48-50`), so `availableRAMBytes` (`pkg/config/config.go:655`) is **0** on every non-Linux host — and D1.5a's deletion of the counters turned that from a degraded limit into **no limit at all**, on the very platform where `PER_BROWSER_COST` was measured. **The ruling picks shape (a), narrowed:** *writing the macOS reader is IN SCOPE and must work on macOS and Linux; Windows must be foreseen but is explicitly NOT in scope.* | **§0.6a specifies it, and three new FRs carry it.** Shape (b) (browser support Linux-only) is declined outright; shape (c) (an unbounded pool, said so in the release notes) is declined for macOS and **accepted for Windows alone, with three obligations attached** so the gap is declared rather than defaulted through — a code placeholder (FR-066), a release note and a config-doc line (FR-066, SC-023). **FR-064** is the Darwin reader; **FR-065** inverts the undeterminable case — where availability cannot be determined the pool **refuses to grow and logs why**, treating an unmeasurable host as full rather than empty; **FR-066** is Windows, foreseen and declared. **AC11a is re-derived** from "recorded, not decided" to the ruled state, and test 72's third case stops being red-until-ruled. | **Answered** (operator, ADR D1.5b) |
| ~~**E-7**~~ | ~~**Writing the Darwin reader moves `performance.max_parallel_agents`' DEFAULT on macOS from 2 to 2000.**~~ **RULED — no longer an escalation (ADR D1.5c + D1.5d, operator, 2026-09-01), and ruled by DISSOLUTION rather than by choosing.** The finding was arithmetically correct against the code as it stood: `readMemAvailableBytes`' one existing consumer, `autoDetectMaxParallel` (`pkg/config/config.go:614-618`), sized the default as `availableRAMBytes() / bytesPerAgent` (3.5 MB, `:608`) clamped by `clampParallel` (`:557-566`), so a real macOS reading moved the default from the floor of **2** (`:558`) to the ceiling of **2000** (`:586`). E-7 offered two shapes and asked the operator to pick. **Both are gone.** **D1.5c declined shape (b) by name** — a browser-only accessor was offered and refused: *"Do not create multiple mechanisms; we need one for managing limits and memory constraints."* **D1.5d then removed shape (a)'s subject**: `bytesPerAgent` and the division are deleted, so there is no computed number left to move. | **§0.6b specifies it, and three new FRs carry it — FR-067** (the deletion, and `EffectiveMaxParallelAgents` becomes `(n, capped)`), **FR-068** (agent admission consults the same live gate, shape 2, no per-unit cost), **FR-069** (the announcement is corrected from *"the default moved"* to *"there is no longer a computed default"*). **§0.6a's side-effect subsection is superseded and now points here.** | **Answered** (operator, ADR D1.5c + D1.5d) |
| **E-10** | **This spec's arbiter for two concurrent turns in one session is the write lease. Issue #505 proposes removing the contention at its source instead — routing `/loop`, async system-notify and cron `SessionModeMain` through the `sessionWorker` so the single-writer-per-session invariant holds again. Which is the intended long-term shape, and does the lease stay if #505 lands?** *(Opened 2026-09-02 in the act of answering ADR D1.9c's residual.)* **This spec does not wait on the answer and must not.** #505 is an agent-loop change, not a browser one; it is unowned and undated; and the code that documents the breach (`pkg/agent/loop.go:3491-3510`) has carried the note since it was written without the invariant being restored. **FR-081 therefore holds whether or not #505 is ever done** — a lease on a session's own tab set is correct under both futures, merely uncontended under the second, and the cost of being right twice is one uncompensated mutex acquisition per gated call. **What is escalated is not a blocker, it is a duplication question**: if #505 lands, is FR-081's widened trigger then removed, or kept as defence in depth? *Recorded rather than left implicit, because the reverse mistake has already been made once in this document — D1.9a's rescope deleted a lease call site on a premise that was true about agents and false about turns, and nothing failed.* | **Open — an agent-loop owner's call, not this spec's.** Nothing here blocks on it | **OPEN** (2026-09-02) |
| ~~**E-8**~~ | ~~**Who ratifies an agent-concurrency change that a BROWSER ruling authorised — and what happens on a host the gate cannot measure?**~~ **RULED — operator scope sign-off, 2026-09-01, ADR commit `ddd9789a4` (*"docs(adr): D1.5d scope signed off — one deliverable, both consumers"*).** **Part (a), ratification: answered directly.** Agent concurrency's existing behaviour is **in scope to change**, including deleting `bytesPerAgent` (`pkg/config/config.go:608`) and `autoDetectMaxParallel`'s division (`:614-618`), *and* the tests and documented defaults that depend on them — those are **deliverables to enumerate**, not collateral to discover during implementation (§0.6b's deliverable table). The browser pool and agent admission **ship together**; the "independently landable" split this row previously demanded is withdrawn. **Part (b), the unmeasurable host: DECIDED IN-SPEC by FR-068a, not escalated.** The sharp question was real — FR-065's *undeterminable ⇒ refuse* is right for a browser (a refused browse costs one tool call) and would, read literally, refuse **every** agent turn on Windows and on `/proc`-less Linux, which stops the product rather than degrading it. **The resolution is already in the ruled text and needed reading, not a new ruling:** FR-065 says the pool refuses to **grow**, and §13 holdout 24 already specifies that on Windows *the first browser opens* and only growth is refused. Applied to agents, *growth* means concurrency above the floor. | **Answered** (operator, ADR D1.5c + D1.5d + the `ddd9789a4` scope sign-off). **FR-068a carries part (b)**: on an `ok=false` host the agent gate admits up to the existing conservative floor of **2** and refuses beyond it, naming memory. The floor is a *floor*, not a computed default, so D1.5d's objection does not reach it, and `meminfo_other.go:25-33` already documents 2 as the deliberate no-signal posture. **The consequence is announced, not discovered** — FR-069 carries it into the release note — and holdout 26 is the human check. |
| **E-9** | **What bounds the profile cache of a workspace under CONTINUOUS drive?** D1.9b ruling 4 bounds profile disk by trimming a profile whose Chrome is **not live** (§0.8). A workspace driven without a 20-minute gap never becomes eligible — its tab TTL (5m) and idle-close TTL (15m) never both elapse — so its cache grows for as long as it is driven. **This is the residual the ruling leaves, not a defect in it:** the ruling deliberately refuses any mechanism that discards data mid-session, and every remaining option does exactly that or needs a number nobody has measured. | **Declared, not defaulted through (FR-074)** — config doc, release note and an operator-visible log name the unbounded case, on the FR-066 pattern. **Not decided here**, because both candidate fixes are design changes: **(i)** bound Chromium's own cache at launch with `--disk-cache-size=<bytes>` — the right *shape* (Chromium evicts only its own cache entries and can never reach a login, so it is **not** the per-workspace size cap ruling 4 rejected), but its value is a measurement nobody here has, and shipping a launch flag on a guess is how `--renderer-process-limit` arrived (§12 A23); **(ii)** trim mid-session, which requires closing a browser someone is using — the outcome the ruling exists to prevent. | Operator / a measurement pass on a continuously-driven profile |

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
  to own. *(The tab **sets** are untouched — that is ownership, not capacity. D1.9c later re-keys them from the agent to the session: ~~FR-048~~ → FR-080.)*
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
and `applyReconcileOutcome` (`pkg/tools/browser/tools.go:321`, whose reason switch is `:346-356`) turns it into text the model
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

### 0.6a The memory reader is in scope, and must work on macOS as well as Linux (operator ruling, 2026-09-01)

**Verbatim:** *"writing the macOS memory reader is IN SCOPE. It must work on macOS and Linux.
Windows must be foreseen but is explicitly NOT in scope."* Recorded in the ADR as **D1.5b**.

**This closes E-6** (§0.5), which revision 5 escalated rather than assumed. The escalation was
correct and its finding is unchanged: with D1.5a deleting every counter, and `availableRAMBytes`
returning **0** on every non-Linux host, macOS would have shipped with **no browser limit at all**
— on the one platform where `PER_BROWSER_COST` was actually measured. The ruling does not soften
that; it removes the cause.

#### It is buildable with no CGo and no new dependency — verified, not assumed

| Claim | Verified where |
|---|---|
| `golang.org/x/sys` is already a **direct** dependency at **v0.47.0** | `go.mod:200` — the line carries no `// indirect` marker |
| It is already used in this tree, so the import is not novel | **20 files** under `pkg/` and `cmd/` import `golang.org/x/sys/unix` (e.g. `pkg/tools/browser/coordinator_lock_unix.go:12`, `pkg/fileutil/flock_unix.go`, `pkg/sandbox/self_hardening_linux.go`) |
| It provides the sysctl readers **for Darwin**, in pure Go | `unix/syscall_bsd.go:5` is `//go:build darwin \|\| dragonfly \|\| freebsd \|\| netbsd \|\| openbsd`; `SysctlUint32` at `:433`, `SysctlUint64` at `:454`, `SysctlRaw` at `:471` |
| No C, no shelling out | The three functions above wrap the `sysctl(2)` syscall directly; nothing invokes `sysctl(8)`, `vm_stat` or `top` |

**Hard Constraint #2 (pure Go, no CGo) and #1 (single binary, no new runtime deps) both hold.**
This is the specific objection `meminfo_other.go:9-13` records against writing the reader
(*"this project takes on no per-OS memory-query code… that real implementation is future work"*),
and it is answered by a dependency the project already ships rather than overruled.

#### The parity is APPROXIMATE, and this spec says so rather than implying equivalence

Linux exposes `MemAvailable` **directly** — a single kernel-computed estimate that already
accounts for reclaimable page cache and slab (`pkg/config/meminfo_linux.go:33-44`). **macOS has no
such field.** The analogue must be assembled, and it must contend with two things the Linux figure
does not:

- **Memory compression.** macOS compresses inactive anonymous pages in place rather than paging
  them out. A compressed page is neither free nor straightforwardly reclaimable, and no counter
  reports "bytes that would be freed by decompressing nothing".
- **Purgeable pages.** Volatile allocations the kernel may discard under pressure without writing
  them anywhere. They are available in a sense Linux's `MemAvailable` has no equivalent for.

**So the macOS number is a considered approximation of the same idea, not the same measurement,
and the two platforms will differ by some margin on comparable hardware.** That margin is expected
and is not a defect in either reader.

**What sysctl actually exposes on Darwin — verified on a real host** (macOS 26.5.2, Darwin
25.5.0, x86_64, `sysctl -n <name>`), because the formula may only be built from keys that exist:

| Key | Present | Note |
|---|---|---|
| `hw.memsize` | **yes** (`34359738368` on the test host) | Total physical bytes. **This is `readMemTotalBytes`'s source and the test oracle** |
| `hw.pagesize`, `vm.pagesize` | **yes** (`4096`) | Page size for every `vm.page_*` count below |
| `vm.pages` | **yes** (`8137872`) | Total *managed* pages — **smaller than `hw.memsize / hw.pagesize` (`8388608`)** because firmware- and kernel-reserved pages are excluded. Do **not** derive total from it |
| `vm.page_free_count` | **yes** | Free pages |
| `vm.page_purgeable_count` | **yes** | Discardable under pressure |
| `vm.page_speculative_count` | **yes** | Read-ahead file cache, freely reclaimable |
| `vm.page_pageable_external_count` | **yes** | File-backed pageable — the closest analogue to Linux's reclaimable cache |
| `vm.page_pageable_internal_count` | **yes** | Anonymous pageable; **not** available memory |
| `vm.compressor_bytes_used` | **yes** | The compressor's own footprint, in bytes (not pages) |
| `vm.swapusage` | **yes** | `struct xsw_usage` via `SysctlRaw` |
| `vm.page_active_count`, `vm.page_inactive_count`, `vm.page_wire_count`, `vm.page_free_target` | **NO — `unknown oid`** | These are `vm_stat`'s fields, sourced from Mach `host_statistics64(HOST_VM_INFO64)`, **not** from sysctl. **The formula therefore cannot simply mirror `vm_stat`'s output**, and an implementation that assumes it can will not compile against reality |

**The composition and the double-counting question, stated as a question rather than settled by
assertion.** The natural starting composition is
`(page_free_count + page_purgeable_count + page_speculative_count + page_pageable_external_count) × pagesize`,
but **speculative pages are themselves file-backed**, so whether `page_pageable_external_count`
already includes them is a per-release kernel detail this spec does not know and may not guess.
On the test host the two readings bracket **8.56 GB** (no overlap assumed) and **7.63 GB** (full
overlap assumed) out of 32 GB — a **12 % spread**, which is the size of the error the choice
carries. **FR-064 requires the implementation to determine which it is, cross-check the answer
against `vm_stat`'s "Pages free / speculative / purgeable / File-backed pages" on a real host, and
record that cross-check in the PR body (SC-023)** — not to pick one and move on. §12 A26 records
the ambiguity so it is not re-litigated silently.

#### The formula must be documented AT THE CALL SITE (FR-064)

Not in this spec, not in a commit message, not in an ADR. **In the doc comment on the Darwin
reader itself**, naming every sysctl it sums, stating the compression and purgeable caveats, and
stating plainly that the figure is an approximation of Linux's `MemAvailable` rather than the same
measurement. **The reason is concrete:** a future reader comparing the two platforms will find them
disagreeing by some margin, and the only two conclusions available are *"this is the documented
approximation"* and *"one of these is broken"*. Without the comment they will reach the second one
and go looking for a bug that is not there.

#### The undeterminable case REFUSES; it does not admit (FR-065)

**This is the part that is easiest to get backwards, and getting it backwards is a false green.** A
gate that cannot measure must never answer *"plenty of room"* — that reads as success at every
call site while admitting without limit, which is the precise failure shape
`docs/internal/false-green-patterns.md` catalogues and the one D1.5a's own text warns against
(*"Do not let this become a silent no-op"*).

**Rule: where availability cannot be determined, the pool refuses to grow and logs why.** An
unmeasurable host is treated as **full**, not empty. This deliberately inverts the usual
fail-soft default, and it is a deliberate inversion rather than an oversight: a refused browse
costs one tool call; an unbounded browser pool costs the gateway and every session on it.

#### "Refuses to GROW" means growth from a FLOOR, and the floor is ONE — stated here because this document said two opposite things (FR-082, round-4 C-401)

**The contradiction, which was live in five places.** *"Refuse to grow"* has no meaning until
*"grow from what"* is fixed, and this document fixed it in two incompatible ways. **AC17, FR-065
and test 83 read as refusing the FIRST launch** — AC17's *"When a browser launch or a tab open is
requested, Then it is refused"*, and test 83's *"a launch is refused … the run **FAILS** if either
succeeds"*, neither of which says *second*. **§13 holdout 24, US-15/AC23, SC-027 and FR-068a read
the opposite** — holdout 24: *"the first browser opens, and an attempt to grow the pool is
**refused**"*, and FR-068a cites that reading by name as *"the same reading of FR-065 that lets the
first browser open on Windows"*. An implementer could satisfy either and fail the other's test.

> **FR-082 — the floor is ONE browser and, inside it, ONE tab.** On a host where the accessor
> reports `ok=false`, the pool admits the **first** Chrome and the **first** tab in it, and
> refuses the **second** of each, naming memory. Refuse to **grow**, never refuse to **run**.

**Why one and not zero.** A floor of zero is not a degraded mode, it is *no browser tooling on
this host at all* — and it would land hardest on the deployment §0.9 spends a whole section
establishing is **supported**: a `/proc`-less Linux sandbox such as gVisor or GKE Sandbox reaches
`ok=false` through `meminfo_linux.go`'s fallback (and will reach it explicitly once FR-078 lands),
so a zero floor **removes browsing entirely from a supported Linux deployment** on the strength of
a reading the host declines to give. That is the shape §0.9's rule exists to forbid — *"a refused
turn is a gateway that cannot answer a message"* — applied one consumer over. **On Windows the
practical difference is small** and the ruling accepts the posture either way; on gVisor it is the
difference between degraded and broken.

**Why one and not two.** The agent consumer's floor is 2 because a floor of 1 serialises every
turn on the host. The browser has no such argument: one Chrome per host still serves every
workspace **in sequence**, via idle close and relaunch from the surviving profile (FR-040,
FR-040a), and a second Chrome is the first thing that is genuinely unpriced when nothing can be
measured — `PER_BROWSER_COST` ≈182 MB is a *measured* figure this host cannot check itself
against. **The two consumers' floors differ on purpose, and FR-075's pairing already requires
them to differ**; FR-082 only says by how much on this side.

**The floor is per HOST, not per workspace**, and this is the clause an implementation is most
likely to get wrong: "one browser" means `len(pool.LiveKeys()) == 1`, not one per key. A
per-workspace reading admits N Chromes on an unmeasurable host and passes any test written only
about the first launch.

**Two consequences follow, and both are requirements rather than implementation taste:**

1. **The availability signal must become two-valued.** `availableRAMBytes()` returns a bare
   `uint64` (`pkg/config/config.go:655-661`), so **`0` cannot be distinguished from "unknown"** —
   and today `0` *is* the unknown sentinel on every non-Linux host. FR-057 already requires this
   accessor to be **exported** for `pkg/tools/browser` to call at all; FR-065 fixes its **shape**
   at the same time: `(bytes uint64, ok bool)`, with `ok=false` meaning *not measurable here*.
2. **A fallback constant is not a measurement either.** On Linux with an unreadable
   `/proc/meminfo`, `readMemAvailableBytes` returns `readMemTotalBytes()/2` and
   `readMemTotalBytes` returns the hardcoded `fallbackTotalRAMBytes` of 4 GB
   (`pkg/config/meminfo_linux.go:16,26-30,40-45`) — a fabricated **2 GiB** with no relationship to
   the machine, which is exactly the MAJOR-2 defect `meminfo_other.go:15-23` records having been
   fixed *off* Linux while it survives *on* it. For the browser gate that constant would read as
   *"2 GiB free, launch away"*. **So `ok=false` covers the fallback path too** (gVisor and other
   `/proc`-less Linux sandboxes reach it). **⚠ SUPERSEDED IN PART by D1.5c/D1.5d (§0.6b).** This
   paragraph used to close *"`autoDetectMaxParallel`'s behaviour is deliberately unchanged — it keeps
   consuming the fallback, because sizing a default conservatively and refusing a launch are
   different jobs"*. **D1.5c refused that separation** (one mechanism, several consumers) and
   **D1.5d deletes `autoDetectMaxParallel` outright** (FR-067), so there is no caller left whose
   answer could stay unchanged. What survives verbatim is everything above this sentence: `ok=false`
   still covers the Linux fallback path, for the browser gate, exactly as written. **What the agent
   path does on an `ok=false` host is answered by FR-068a, not by this paragraph** — it admits to the
   conservative floor of 2 and refuses to grow beyond it. That question only arose because the
   *"different jobs"* reasoning deleted here had been the sole thing answering it.

#### Windows: foreseen, NOT scoped — and it is not simply "the Darwin job again" (FR-066)

Windows keeps returning the unmeasurable signal, **therefore Windows has no memory-derived limit**
and, under FR-065, no browser or tab may be admitted through the gate there at all. That is
consistent with the platform's existing posture in this codebase — no sandbox backend
(`selectBackendPlatform` returns `FallbackBackend`, `pkg/sandbox/sandbox_other.go`),
`fileutil.WithFlock` a documented no-op (`pkg/fileutil/flock_windows.go`), `pidAlive`
unconditionally `true` — but **consistency is not an excuse for silence.**

**Why it is a separate piece of work rather than the same one twice, verified:**
`golang.org/x/sys/windows` **v0.47.0 contains no `GlobalMemoryStatusEx` wrapper and no
`MEMORYSTATUSEX` type** — `grep -rn "GlobalMemoryStatusEx\|MEMORYSTATUSEX"` over the module's
`windows/` directory returns **nothing**. The path is therefore a hand-rolled
`NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")` call
(`windows/dll_windows.go:234,249`) with a struct layout to get right — still pure Go and still
CGo-free, but not the two-line sysctl read Darwin gets. That difference is the reason the operator
scoped one and not the other, and it belongs in the record.

**Three obligations, none optional:**

1. **A code placeholder**, in a `meminfo_windows.go` of its own, so the gap is visible **at the
   point someone would fix it** rather than only in a spec nobody reads while editing. It returns
   the same `ok=false` FR-065 defines, and its doc comment names `GlobalMemoryStatusEx` and the
   `LazyDLL` route above.
2. **A release-note line** stating that the browser pool has no memory-derived limit on Windows.
3. **A config-documentation line** in the same place the browser keys are documented.

**Windows browser support is specified as `degraded-unsupported` until that reader exists** — the
tools register and run, but the admission gate refuses to grow the pool, so the platform is
usable for a single browser and explicitly not supported for the workspace pool. It is a stated
posture, not a discovered one.

#### The side effect this reader has OUTSIDE the browser — SUPERSEDED, see §0.6b

**Kept as a pointer, because two later rulings changed both the answer and the question.** This
subsection used to compute that giving macOS a real reading moves `performance.max_parallel_agents`'
default from **2** to **2000**, and escalated that as **E-7**. The arithmetic was right for the code
as it stood; the code it depended on no longer survives the ruling.

**ADR D1.5c** (one mechanism, several consumers) declined the narrower alternative this subsection
offered — a browser-only accessor — so the browser gate and agent concurrency are now inseparable by
decision. **ADR D1.5d** then deleted the term the 2 → 2000 arithmetic multiplied through:
`bytesPerAgent` and `autoDetectMaxParallel`'s division. With no per-unit constant there is no
computed number to move, so **nothing jumps** — and E-7's two shapes (let the default move, or give
the browser its own accessor) are **both** dissolved rather than one of them chosen.

**§0.6b specifies what replaces it, and §0.5 E-7 is marked RULED.** What survives from this
subsection unchanged is the reason it existed: a browser change must not alter agent-concurrency
behaviour unremarked. That obligation is now carried by **FR-069** (§0.6b) as release-note and
config-documentation text, with the announcement corrected from *"the macOS default moved"* to
*"there is no longer a computed default"*.

#### New requirements this ruling adds

| FR | What it requires |
|---|---|
| **FR-064** | A **Darwin** implementation of `readMemAvailableBytes` and `readMemTotalBytes`, in a new `pkg/config/meminfo_darwin.go` sibling to `meminfo_linux.go`, with `meminfo_other.go` narrowed to `//go:build !linux && !darwin`; pure Go via `golang.org/x/sys/unix`; the formula and its caveats documented **at the call site** |
| **FR-065** | **The undeterminable case refuses.** The availability accessor becomes two-valued (`bytes, ok`); where `ok` is false the pool **refuses to grow — at launch and at tab open — and logs why**, once per platform-lifetime rather than per call; an unmeasurable host is treated as full |
| **FR-066** | **Windows is foreseen and declared, not defaulted through.** A `meminfo_windows.go` placeholder returning the unmeasurable signal and naming the fix route; a release-note line; a config-documentation line; and Windows browser support specified as **degraded-unsupported** |

### 0.6b One mechanism, several consumers — and the limit is live, not computed (operator rulings, 2026-09-01)

**Two rulings, recorded in the ADR as D1.5c and D1.5d, and they only make sense together.**

> **D1.5c, verbatim:** *"the memory reader for linux and mac should have multiple use cases — the
> browser limits but also the concurrent agent limit. Do not create multiple mechanisms; we need one
> for managing limits and memory constraints."*
>
> **D1.5d, verbatim:** *"the number must be dynamic based on realtime memory consumption."*

**D1.5c rules E-7.** §0.5 E-7 offered two shapes and asked the operator to pick; one of them —
*"give the browser gate its own accessor so `autoDetectMaxParallel` keeps the floor of 2 on macOS"* —
was **offered and declined by name**. There is one reader, one set of constraints, and several
consumers. The browser pool and agent concurrency are not two mechanisms that happen to share a
number; they are one mechanism asked the same question twice.

**D1.5d then rules what that one mechanism computes: nothing.** It observes.

#### Why `bytesPerAgent` goes, in the same terms D1.5a used against the browser cap

`bytesPerAgent` (`pkg/config/config.go:608`) is a compile-time constant of 3.5 MB, and
`autoDetectMaxParallel` (`:614-618`) turns it into a concurrency ceiling by dividing live
availability by it. Every objection D1.5a raised against the browser's counters applies to that
shape without adjustment:

| D1.5a's objection to the browser cap | The same objection, against `bytesPerAgent` |
|---|---|
| It is an assumed per-unit cost, not a measured one | Its own doc comment (`config.go:588-589`) calls it *"the **assumed** marginal memory cost of one concurrent agent"* |
| Real cost varies by more than an order of magnitude — renderers measured 30 MB → 327 MB | The same constant's doc concedes the spread itself (`:602-607`): it *"deliberately does NOT budget for the separate, stochastic ~500 MB Chromium/browser-tool event"*. An agent doing bookkeeping and an agent driving a browser differ by **~140×** against a 3.5 MB unit — by the constant's own citation, not by inference |
| A number fixed at boot is wrong in both directions as load changes | It is worse than fixed-at-boot: the *constant* is fixed at compile time and only the *dividend* moves, so the answer tracks free memory while pretending per-agent cost is invariant |
| A cap that overshoots its own stated bound is a defect (ADR criterion P14) | `clampParallel` (`:557-566`) then floors the result at **2** and caps it at **2000**, so on most hosts the division's answer is discarded at one end or the other — the arithmetic in the middle is decorative on exactly the hosts where it matters |

**One asymmetry is worth stating rather than glossing, because it cuts the other way.** Unlike the
browser cap, `bytesPerAgent` has real measurement behind it: `config.go:590-601` cites two UAT
documents (`docs/internal/uat/parallelism-cost-browser-bash-2026-08-04.md`,
`parallelism-cost-measurement-2026-08-04.md`) recording 2.0–3.2 MB gateway-RSS deltas per agent at
N=4/8/16. That evidence is not being called wrong. What D1.5d rejects is the **shape** — freezing
*any* per-unit figure, however well measured, when the thing it prices varies by two orders of
magnitude with workload. The measurement stays true of the workload it measured and stops being
true the moment an agent opens a browser.

#### The deletion, as implementation scope (FR-067)

Same treatment FR-059 gives the browser counters: a code deletion with its call sites named, not a
configuration change.

| Deleted | Where, verified on this worktree 2026-09-01 | Status before this ruling |
|---|---|---|
| `bytesPerAgent` (3.5 MB) | `pkg/config/config.go:608` (the `const`), plus its 19-line doc block `:588-607` | **Shipped**; one consumer |
| `autoDetectMaxParallel`'s division | `pkg/config/config.go:614-618` — the whole function: `avail := availableRAMBytes()`, `val := int(float64(avail) / bytesPerAgent)`, `return clampParallel(val)` | **Shipped**; one consumer (`EffectiveMaxParallelAgents`, `:467`) |
| `clampParallel` and `autoDetectFloorParallel` | `pkg/config/config.go:557-566` — the floor-2/ceiling-2000 clamp that exists **only** to bound the deleted division. `clampParallelExplicit` (`:486`) is **NOT** deleted: it serves the explicit-operator path, which survives intact | **Shipped**; one consumer (`autoDetectMaxParallel`) |
| The doc claims that describe the deleted shape | `config.go:430-439` (`MaxParallelAgents`' own doc: *"0 means 'use the auto-detected default', sized from available memory"*), `:448-449` (`EffectiveMaxParallelAgents` step 3), `:611-613` (`autoDetectMaxParallel`'s own summary), `meminfo_other.go:19,25-33` (which explains at length why returning 0 lands on the floor of 2) | **Shipped**; each states a mechanism that will not exist |

**`physicalConcurrencySafetyCeiling` = 2000 (`config.go:586`) is NOT deleted, and the distinction
matters.** It is not a memory guess: its doc (`:568-586`) derives it from Go's hard abort at 10,000
OS threads, a measured ~999 threads at ~1000 concurrent fsyncing goroutines, and a deliberate 5×
margin for every other thread-consuming subsystem in the process. Memory availability says nothing
about OS-thread exhaustion, so the live gate cannot replace it. It stays, as a **physical backstop**
rather than a capacity default — see FR-067's answer below.

#### What `EffectiveMaxParallelAgents` returns when nothing is set (FR-067)

An explicit operator setting still wins outright, unchanged: env `OMNIPUS_MAX_PARALLEL_AGENTS`
first, then `p.MaxParallelAgents > 0`, both through `clampParallelExplicit` (`config.go:454-467`).
Absent one, **there is no number — there is a gate**, and the accessor has to say so in a shape its
callers can act on. Its callers do expect an `int`, and three of them would break on a bare sentinel:

| Caller | What it does with the value | What a bare `0` would do |
|---|---|---|
| `newTaskExecutor` (`pkg/agent/task_executor.go:250-260`) | `newDispatchSemaphore(capacity)` | A zero-capacity semaphore — **every** task dispatch blocks forever |
| `getSubTurnConfig` (`pkg/agent/subturn.go::getSubTurnConfig` — this file churns, so the symbol is the citation) | in-turn fan-out cap | Same, for delegation |
| `ResolveRootDelegationCap` (`pkg/agent/admission.go:216-218`) | the root-delegation admission cap | Same |
| `getPerformance` / `putPerformance` (`pkg/gateway/rest_performance.go:71-78, 149-154`) | `effective_max_parallel_agents` on the wire; `wireMaxParallelAgents` (`:62-66`) substitutes it whenever the configured value is below the schema floor | `PerformanceSettings.yaml` declares `minimum: 1`, so a `0` on the wire is **schema-invalid** — the exact MAJOR-3 shape `:57-61` records having already been fixed once |

**So the answer is two-valued, the same shape FR-065 gives the availability accessor** — one pattern
used twice rather than a second convention:

```go
// (n, capped) — capped=false means "n is not the constraint; the live gate is".
func (p PerformanceConfig) EffectiveMaxParallelAgents() (int, bool)
```

- env override set and valid → `(clampParallelExplicit(v), true)`
- `p.MaxParallelAgents > 0` → `(clampParallelExplicit(p.MaxParallelAgents), true)`
- otherwise → `(physicalConcurrencySafetyCeiling, false)`

**Why the physical backstop and not a sentinel.** Returning 2000 with `capped=false` keeps every
semaphore constructible, keeps the wire schema-valid, and keeps the one bound that is still true —
the OS-thread ceiling — in force. Returning `0`, or `math.MaxInt`, hands the callers above a value
that is either a deadlock or a thread-exhaustion abort. **`capped=false` is the load-bearing half:**
it tells a caller that the integer it just received is a backstop, not a capacity claim, which is
what FR-069 needs in order to stop the UI presenting it as one.

#### Which shape: observed running cost, or no per-unit cost (FR-068)

D1.5d names two and requires the spec to choose with its reasoning visible.
**This spec chooses shape 2 — no per-unit cost.** It is also the ADR's recommendation, but the
consistency argument is the weakest of the four reasons below, so it is listed last.

1. **Shape 1 cannot attribute, and this is the decisive objection.** Shape 1 derives a marginal
   figure from *this process's* footprint divided by live agent count. But this process also hosts
   ~14 channels, the HTTP server, the browser coordinator's bookkeeping and the Go runtime — and,
   critically, **Chrome is a child process**. Its memory is absent from our footprint and present in
   the host availability figure the gate reads. So the derived per-agent cost would charge channel
   traffic to agents and charge browser memory — the single largest, most variable term — to
   nothing. The number would be systematically wrong in a direction no operator could audit, and the
   only visible symptom would be admissions that feel arbitrary.
2. **Shape 1's warm-up window is exactly when it is needed.** A gateway restart with resumed tasks
   is the canonical burst: agent count goes 0 → N in seconds, with no history to derive from. A
   marginal figure is meaningless at N=0 and unstable at N=1..2 — so the mechanism is at its least
   trustworthy at the only moment admission control does real work.
3. **Shape 1 misreads Go's memory behaviour on the way down.** The Go runtime does not return heap
   promptly to the OS (scavenger + `MADV_FREE`), so as agents finish, footprint stays flat while the
   denominator falls — and "cost per agent" *rises* during idle recovery. A gate consuming that
   figure refuses admission most firmly when the host is emptiest. That is not a tuning problem; it
   is the arithmetic.
4. **Shape 2 is what the browser pool already does.** FR-060's tab gate is a ratio with no per-tab
   byte constant, for the same reason: a tab has no measured floor and inventing one was declined
   (§0.5 E-5). One rule applied twice, and no constant to drift.

**The cost of shape 2, stated rather than glossed.** With no per-unit cost, the reserve absorbs the
marginal agent — so the reserve must be big enough for one more agent of the heaviest realistic
kind, and the gate can admit an agent that then turns out to be expensive. **What bounds that
exposure is the browser gate itself:** the expensive tail D1.5c named (the ~500 MB
Chromium event, `config.go:602-607`) cannot arrive unmetered, because an agent reaching for a
browser must pass FR-057's launch gate at `PER_BROWSER_COST` and FR-060's tab gate first. The
agent gate handles the ordinary case; the browser gate handles the tail. That is the whole reason
one mechanism with two call sites is safer here than two mechanisms with one each.

**Mechanically, FR-068 is FR-060's gate at a different call site.** Same exported two-valued
accessor (FR-065), same ratio, same `ok=false` ⇒ refuse rule, same "logged once, not per call"
discipline. No new threshold constant is introduced by this FR. **If the agent path is later found to need a
different threshold from the browser path, that is a tuning value on one mechanism — never a second
mechanism**, and it must be argued from a measurement, in this spec, not chosen at the call site.

#### The host the gate cannot measure — refuse to GROW, not refuse to RUN (FR-068a)

**This is the one place where reading FR-065 literally would have been wrong, and it is worth
stating why rather than quietly not doing it.** FR-065's rule is *availability undeterminable ⇒
refuse*, and it is right for a browser: a refused browse costs one tool call. Applied word-for-word
to agent admission it refuses **every** agent turn on Windows and on any Linux host that falls
through to the 4 GB fallback (gVisor and other `/proc`-less sandboxes, `meminfo_linux.go:16`). That
is not a degraded platform — it is a gateway that cannot answer a message.

**The resolution was already in the ruled text and needed reading, not a new ruling.** FR-065 says
the pool refuses **to grow**, and §13 holdout 24 already specifies the consequence on Windows
precisely: *the first browser opens*, and an attempt to **grow** the pool is refused. Growth is what
is refused; existence is not. Applied to agents, growth means concurrency **above the floor**.

**So: on an `ok=false` host, agent admission admits up to 2 concurrent agents and refuses beyond
it**, with FR-063's reason code and a message naming memory.

**Why 2, and why this is not the thing D1.5d deleted.** It is a **floor**, not a computed default:
no availability figure is divided by anything, no per-unit constant survives, nothing is precomputed
at boot. It is also not invented here — `meminfo_other.go:25-33` documents 2 as the deliberate
no-signal posture already shipped on exactly these platforms, chosen *because* it fails conservative
rather than open. FR-067 deletes the arithmetic that produced it as an *answer*; FR-068a keeps it as
a *floor*, which is the one shape D1.5d's objections do not reach.

**And this branch must be able to fail a test.** *"Admits when memory is free"* passes against a
stub that always admits, so the assertion that carries this requirement is the **refusal**: with the
accessor forced to `ok=false`, the **third** concurrent admission is refused, the refusal names
memory, and the test **fails if it succeeds** (see test 87, AC21).

#### The release-note item changes (FR-069)

**§0.6a's earlier text — and D1.5c's own — announced *"the macOS default moves 2 → 2000"*. That
announcement is dissolved and must not ship.** Nothing jumps, because nothing is precomputed:
`bytesPerAgent` is gone, so there is no division whose result could move.

**What must be announced instead, and it is smaller and truer:**

- **There is no longer a computed default for `performance.max_parallel_agents`.** An explicit
  setting is honoured exactly as before. Absent one, concurrency is bounded by **live available
  memory** at the moment of each admission decision, plus the unchanged physical OS-thread backstop.
- **On a host where availability cannot be determined** — Windows, or a `/proc`-less Linux sandbox —
  **the gate has nothing to read, so concurrency holds at the conservative floor of 2 and does not
  grow** (FR-068a). This is the same posture those hosts have shipped with; what changes is that it
  is now stated rather than emerging from a stubbed reader.

**And the number an operator can see does change, which is a different claim from "the default
moved" and must not be conflated with it.** `GET /api/v1/performance` returns
`effective_max_parallel_agents`, and with nothing set it will now report the physical backstop
(2000) on every platform instead of a memory-derived figure — 2 on macOS today, `avail / 3.5 MB`
clamped on Linux. Reporting 2000 as a capacity would be a fresh instance of the defect this project
keeps catching: *a displayed number that is not the constraint*. FR-069 therefore requires the
`capped=false` case to be surfaced as **"automatic — bounded by available memory"**, not as the
integer 2000. That is a contract-and-SPA change (`contracts/components/schemas/PerformanceSettings.yaml`)
and it is inside this deliverable, not adjacent to it — the operator's scope sign-off
(`ddd9789a4`) covers it, and §0.6b's deliverable table names the SPA and schema artefacts it
touches.

#### Scope, and it is signed off

**This reaches beyond the browser into agent concurrency, and that is ratified, not proposed.**
Operator scope sign-off **2026-09-01**, ADR commit `ddd9789a4` — *"D1.5d scope signed off — one
deliverable, both consumers"*. §0.5 **E-8 is RULED** on the strength of it.

**Two things follow, and both are requirements on how this ships:**

1. **One deliverable, one set of tests.** The memory reader (FR-064/065/066), the browser pool's
   admission (FR-057/060) and agent admission (FR-067/068/068a) are **one piece of work**. An earlier
   revision of this section split them, keeping the browser side independently landable so it would
   not wait behind someone else's approval. **That hedge is withdrawn**, and not merely as
   unnecessary: shipping the reader with one of its two consumers adopted is precisely the split
   D1.5c ruled against — *one mechanism half-adopted is two mechanisms*, with the second one arriving
   later as a second set of thresholds nobody reconciled.
2. **Agent concurrency's existing behaviour is in scope to change**, not adjacent to it. Its tests
   and its documented defaults change with it. They are enumerated below with `file:line`, the same
   treatment FR-059 gives the browser counters, so an implementer inherits a list rather than a
   discovery exercise.

#### The agent-side deliverables, enumerated (FR-067)

**Nothing in this table is collateral.** Each row is work the change owes, and each is verified on
this worktree 2026-09-01.

**Tests that assert the deleted formula and must be re-derived or deleted with it:**

| Test | Where | What it asserts today, and why it cannot survive |
|---|---|---|
| `TestEffectiveMaxParallelAgents_Auto_MatchesMemoryFormula` | `pkg/config/parallel_clamp_test.go:113-121` | Asserts the auto default **equals** `clampParallel(int(float64(availableRAMBytes()) / bytesPerAgent))` — it is the formula, written twice. **Deleted with the formula**, not adapted: adapting it to the new answer would leave a test named after an arithmetic that no longer exists |
| `TestEffectiveMaxParallelAgents_Auto` | `:98-108` | Asserts the auto value is `>= 2` and `<= physicalConcurrencySafetyCeiling`. **Re-derived**, not deleted: it becomes the FR-067 shape assertion — unset returns `(physicalConcurrencySafetyCeiling, false)`, and the test **fails if `capped` is true** |
| `TestClampParallel_AutoFloorsAtTwo` (table) | `:13-36` | Exercises `clampParallel`'s floor/ceiling directly — its 11 cases ARE the deleted clamp. **Deleted with `clampParallel`.** `TestClampParallelExplicit_HonoursOne` (`:45-65`) and `TestClampParallelExplicit_NeverLowersLargeValue` (`:165-171`) are **kept unchanged** — the explicit path survives intact |
| `TestEffectiveMaxParallelAgents_ExplicitOverridesAuto_BothDirections` | `:144-160` | Computes `autoDefault := PerformanceConfig{MaxParallelAgents: 0}.EffectiveMaxParallelAgents()` and picks values around it. **Re-derived** against the backstop, keeping its real assertion (an explicit value wins in **both** directions) |
| `TestAutoDetectMaxParallel_NonLinux_DefensiveFloorNotFictitious` | `pkg/config/meminfo_other_test.go:48-55` | Asserts `autoDetectMaxParallel()` returns the floor of 2 off Linux. **The function is deleted**; the assertion's *intent* — no signal must not fail open — moves to FR-068a's floor test, which is the same property re-expressed against the gate. *(Its two siblings, `TestReadMemAvailableBytes_NonLinux_FailsConservativeNotFictitious` `:37-46` and `TestReadCgroupMemoryAvailableBytes_NonLinux_AlwaysAbsent` `:60`, survive — they test the reader, not the formula, and FR-064 narrows their build tag rather than their assertions)* |
| `TestAutoDetectMaxParallel_WarmPageCacheContainerDoesNotCollapseToFloor` | `pkg/config/meminfo_linux_test.go:288-311` (the call at `:307`, the comment at `:238`) | Asserts `autoDetectMaxParallel() > 2` when a container has real headroom. **Deleted with the function**; FR-068's gate test replaces the property it was protecting |
| `TestGetPerformance_ZeroConfig_SchemaValid` | `pkg/gateway/rest_performance_test.go:27-54` (the assertions at `:40-49`) | Asserts unconfigured `max_parallel_agents >= 2` and `effective >= 2`. **It would still pass** against FR-067 (the backstop is 2000) — **which is the problem**: it would pass while the field's *meaning* changed underneath it. **Re-derived to assert the `capped=false` representation** FR-069 requires, so it can fail on the change it is supposed to notice. *(Note its comment is already stale — it says "minimum 2"; `PerformanceSettings.yaml:12` says `minimum: 1`.)* |
| `TestPerformancePUT_ZeroResetsToAutoDetectedDefault` + `TestPerformancePUT_Zero_ResponseBodySchemaValid` | `pkg/gateway/rest_performance_ceiling_test.go:94-121` and `:136-158` | PUTs `max_parallel_agents: 0` ("reset to auto") and asserts the response is schema-valid. **Kept, and it is the regression guard for FR-067's shape choice** — it is the test that fails if a bare `0` sentinel is ever returned |

**Call sites that break on the signature change** — `EffectiveMaxParallelAgents()` becomes
two-valued, so every single-value use is a compile error. **29 non-test uses** across `pkg/agent`
and `pkg/gateway` and **54 test-side uses across 9 files** (`pkg/config/parallel_clamp_test.go`,
`config_adr057_test.go`; `pkg/agent/admission_test.go`, `wiring_adr057_fix_test.go`,
`admission_adr057_test.go`, `fanout_cap_test.go`, `admission_consolidation_test.go`,
`admission_failclosed_adr057_test.go`; `pkg/gateway/rest_performance_ceiling_test.go`). **This is
mechanical but it is not free, and it is in the same commit** — the compiler finds every one, which
is exactly why the two-valued shape is safer than an int sentinel: a sentinel would have compiled
everywhere and been wrong at four call sites silently.

**Documented defaults that describe the deleted mechanism:**

| Artefact | Where | What it says that stops being true |
|---|---|---|
| `MaxParallelAgents`' own field doc | `pkg/config/config.go:430-439` | *"0 means 'use the auto-detected default', sized from available memory"* |
| `EffectiveMaxParallelAgents`' doc, step 3 | `:448-449` | *"An auto-detect DEFAULT: availableMemory / bytesPerAgent"* |
| `meminfo_other.go`'s rationale block | `pkg/config/meminfo_other.go:19, 25-33` | Nine lines explaining why returning 0 lands on the floor of 2 via `clampParallel` |
| **The SPA's "Live system recommendation"** | `src/components/settings/PerformanceSection.tsx:218-229` | Presents `effective_max_parallel_agents` to the operator as a *recommendation*, with a comment naming the deleted formula verbatim: *"availableRAM / ~3.5 MB per concurrent agent, floored at 2 — see autoDetectMaxParallel"*. **This is the single most visible artefact in the list** — left alone it would recommend **2000** to every operator, in the UI, as a number the system chose for them. FR-069 fixes it |
| the wire schema | `contracts/components/schemas/PerformanceSettings.yaml:10-16, 27-29` | `max_parallel_agents`' description explains the auto-detect contract; `minimum: 1` on both fields is what forbids a bare-`0` sentinel |
| the UAT record `bytesPerAgent` cites | `docs/internal/uat/parallelism-cost-measurement-2026-08-04.md`, `parallelism-cost-browser-bash-2026-08-04.md`, `docs/internal/architecture/max-parallel-formula-research-2026-08-04.md` | **Not edited — they are records of a measurement that was correctly taken.** They are named here so a future reader finds the constant's provenance rather than concluding it was invented. What changed is the *shape*, not the arithmetic they contain |

#### New requirements these rulings add

| FR | What it requires |
|---|---|
| **FR-067** | **`bytesPerAgent` and the computed default are deleted**, with call sites; `EffectiveMaxParallelAgents` becomes `(n int, capped bool)`; unset returns `(physicalConcurrencySafetyCeiling, false)` — a backstop, not a capacity |
| **FR-068** | **Agent admission consults the same live headroom gate as the browser pool**, shape 2 (no per-unit cost), same exported accessor, same ratio, same refusal rule, same log-once discipline. One mechanism, two call sites |
| **FR-068a** | **On a host the gate cannot measure, agent concurrency holds at the floor and refuses to grow.** `ok=false` ⇒ admit up to **2** concurrent agents, refuse the third, naming memory. *Refuse to grow*, not *refuse to run* — the same reading of FR-065 that lets the first browser open on Windows (§13 holdout 24) |
| **FR-069** | **The announcement is corrected.** Release note and config documentation say *"there is no longer a computed default"*, **not** *"the macOS default moved 2 → 2000"*; and `capped=false` is surfaced as "automatic — bounded by available memory", never as the integer 2000 |

---

### 0.7 Taking the operator's tab is IMPLICIT — and the control lock is the whole mitigation (operator ruling, ADR **D1.9b** ruling 1, 2026-09-01)

**ADR D1.9b carries four rulings; two land on this document and two do not.** Rulings **1** (implicit acquisition, below) and **4** (profile disk, §0.8) are absorbed here. Rulings **2** (`sandbox.browser_evaluate_enabled` seeded **true**, so the capability matches the policy surface an operator reads) and **3** (`browser_snapshot` is **Tier 3 — searchable only**, which falsifies D2.4's *"the default way an agent reads a page"*) land on the **sibling D2 capability spec**. This document's references to those two tools — §14 rule 3's table rows for `browser_evaluate` and `browser_snapshot`, and §0.5 **E-3** — are updated to point at the rulings, and **this spec creates no requirement for either**: `browser_evaluate`'s lease membership is unchanged by being enabled, and a tool's tier does not change whether it takes the lease.

**Verbatim (D1.9b ruling 1):** *"An agent acquires control of the operator's shared tab by acting on it; there is no explicit 'take control' call."*

#### It closes E-1, and it closes it by SIMPLIFYING

§0.5 **E-1** asked what D1.9a's *"take control on request"* looks like to an agent, and priced the explicit reading — a seventh browser tool. The ruling picks the other shape, and every cost E-1 anticipated is withdrawn rather than paid:

| What E-1 priced | Under the ruling |
|---|---|
| A seventh browser tool in the D2 surface | **None.** The surface stays **11 → 17**, not 18 |
| A seventh policy entry in every agent's `tools.builtin.policies` block | **None.** Hard Constraint #6's explicit per-agent coverage arithmetic is untouched. **⚠ And under ADR D2.9a (2026-09-02) that is a stronger reason to keep it at none than it looks** — see the note below |
| A Tier-3 fixture row and a tool-manifest count change | **Neither.** Both are unaffected |
| Rework of the work stream planned around the current tool set | **None** |

#### A missing policy seed is a SILENT per-agent deny, not a boot abort (ADR **D2.9a**, absorbed 2026-09-02)

**The claim this project has repeated — *"a registered tool with no seeded policy aborts boot"* — is FALSE, and it was the safety net behind "someone would notice".** `repairAndValidateToolPolicyCoverage` (`pkg/gateway/gateway.go:1318`, called at boot `:2521` and on hot reload `:4017`) runs `config.RepairIncompleteToolPolicyCoverage` **before** `ValidateToolPolicyCoverage` (`:1335` then `:1346`). The repair **backfills an explicit `deny` for every uncovered (agent, tool) pair**, logs one WARN, and validation then finds nothing to report. The function's own comment says so, and names the failure shape verbatim: *"the first post-upgrade boot would silently deny both on every agent — boot succeeds with no gap and no abort"* (`:1319-1328`).

**What that changes for this document — nothing it relies on, and one thing it strengthens.** This spec adds **no** tool (FR-070 is an absence; §5 forbids `browser_take_control`) and removes no policy key, so no requirement here rests on the abort. What it does change is the *argument*: the reason to keep the browser surface at 11 → 17 is no longer "a new tool without a seed would be caught loudly" — it would be caught **silently, as a deny on every agent**, and the operator's first symptom is a tool the model never sees.

**Two obligations follow, and the second is the one that binds an implementer.**

1. **The cross-spec obligation on D2 is sharper.** D2 adds six tools (§14 rule 3). Each needs an explicit seed in `pkg/coreagent/core.go`'s per-agent enumeration; a tool shipped without one is not a boot failure to fix in the morning, it is a **quiet per-agent deny** that looks exactly like a policy the operator chose.
2. **The ordering discipline becomes ONE ATOMIC COMMIT, never a sequence.** Wherever this spec says a registration and its seed (or a deletion and its replacement) must land together, "together" now means *the same commit* rather than *adjacent commits with a loud failure in between* — because there is no loud failure in between. This is the same rule §3.2 Stream A already applies to FR-080/FR-081, FR-002c and the FR-059/FR-060 pair; D2.9a removes the last reason to think a gap between commits would announce itself.

#### What "acting on it" means, precisely — and it is not a new mechanism

An agent addresses the operator's tab set the way it addresses any tab set: with the resolved `BrowsingKey` plus `TabOwnerWorkspace()` (FR-080). **Acquisition is the ordinary execution of a `controlledResult`-gated tool against that owner.** There is no acquisition verb, no state an agent can request, and no result field reporting *"you now hold it"* — the tool result is the tool's own result. That is **FR-070**.

#### The word "driver" is DELETED from this document — decided, round-4 C-403

**The finding was exact.** Six sites asserted that an agent *"becomes the driver"* or *"is the workspace tab's driver"*, and **no site defined the term or named anything a test could read.** That is not a wording problem, it is a contradiction with the requirement in the same sentence: **FR-070 forbids every representation a driver could have** — no tool, no policy entry, no wire field, no result key. A property with no representation has no observable, so *"Then A is the driver"* is unfalsifiable, and its negative twin *"no driver state changed"* is **vacuously true** — it passes on a build that never had such state, which is every build this spec permits.

**D1.9c was the obvious place for the term to acquire a referent, and it does not give it one.** The re-key moves the **agent** tab set to the **session**; it leaves `TabOwnerWorkspace()` exactly as it was (§0.2a, "What this ruling does NOT change"). The operator's tab is still owned by nobody who can be named, and the lease — the only per-call exclusion that exists — is held **for the duration of one action-tool call** and released by `defer` (§14 rule 4). **Nothing survives the call for a later assertion to read.** So the term is not rescued by the newer ruling; it was never anything but a narrative flourish.

**Decision: delete it at all six sites and assert what is actually observable in its place** — (1) the call **succeeded** and its effect is visible on the workspace-owned tab, (2) the tab's owner is **still `TabOwnerWorkspace()`** afterwards, i.e. nothing transferred, and (3) **no acquisition call was made**. All three can fail. *Recorded as a decision rather than a copy-edit because "the agent becomes the driver" is a sentence a reader will reintroduce — it is the natural way to describe what happens — and reintroducing it re-creates an assertion nothing can check.*

**One consequence is worth stating rather than leaving to be discovered:** §12 **A25** left open *"whether an agent may **close** a workspace-owned tab it did not open"*, and answered it provisionally — *"treat closing the operator's tab as requiring the same acquisition as writing to it."* Under this ruling that provisional answer resolves: `browser_close_tab` is `controlledResult`-gated (`tabs.go:171`) and therefore leased (§14 rule 3), so closing the operator's tab **is** acting on it and acquires implicitly, under the same lock gate and the same lease as a write. **FR-080's** prohibition is unaffected — it forbids closing another **session's** tab, which remains impossible. *(This sentence said "FR-048's … another **agent's** tab" until round-5 m-502. FR-048 is tombstoned by D1.9c and the prohibition it carried was re-keyed into FR-080: within one session the tab is shared by design, so "another agent's tab" is not a thing the product has.)* A25 is updated in place to record this as the ruling's consequence rather than as a separate decision.

#### The mitigation is an EXISTING control, and it must be ASSERTED rather than assumed

Implicit acquisition may occur **only when no human holds the live-view lock**. `controlledResult` (`pkg/tools/browser/tools.go:962`) already defers an agent while a human is driving (ADR-038 D6), and §14.2 rule 1 already orders the gates so it runs **before** the lease. **The ADR states this because it IS the whole mitigation:** without that lock, an agent's first write to the operator's tab is a **silent takeover** of a tab a human is using at that moment.

That ordering is now load-bearing in a way it was not before the ruling, and there are **two ways it can silently stop working — both already known defects in this document, now promoted to mitigation-critical:**

1. **`controlledResult` asks the wrong key today.** It calls `mgr.Live().IsControlled(defaultSessionID)` (`pkg/tools/browser/tools.go:963`). Once the live registry is re-keyed, that constant resolves nothing and the function returns `false` **forever** — the lock is intact, populated, and never consulted. This is **FR-002c**, and the mitigation now depends on it. A regression there is not "a stale key": it is the takeover the ruling says cannot happen.
2. **A plausible future edit removes it without failing a test that names it.** Moving the lease ahead of the control check, or skipping `controlledResult` on `TabOwnerWorkspace()` *"because the lease already arbitrates"*, deletes the mitigation while every lease test stays green — the lease still serialises two agents perfectly with a human locked out of their own tab.

#### Therefore the requirement is a test that FAILS when the lock stops gating acquisition (FR-071)

**The passing direction alone is worthless.** A test asserting *"an agent can act on the operator's tab"* is green on a build with no lock at all, on a build with `IsControlled` hard-wired to `false`, and on a build with `controlledResult` deleted from every call site. It cannot fail for the defect it exists to catch.

**The assertion that carries the mitigation is the BLOCKED one**, and FR-071 requires both directions in one test, driven through the **real registered tool path** and against the **resolved** key:

| Case | Setup | Required outcome | What its failure means |
|---|---|---|---|
| **Blocked (the mitigation)** | A human holds the live-view control lock on the resolved key `ws:W`; agent A calls a `controlledResult`-gated tool against `TabOwnerWorkspace()` | The ADR-038 D6 deferral, **the lease is never acquired**, and **the page is unchanged** — asserted on the page, not on an ownership field, because there is no ownership field to read (C-403) | The lock has stopped gating implicit acquisition — a silent takeover |
| **Blocked, via the key** | The lock is held on `ws:W`; `controlledResult` is invoked from a tool whose manager resolved `ws:W` | Same as above — proving the lock was consulted **against the resolved key**, not against a constant | FR-002c has regressed and case 1 would pass vacuously |
| **Allowed** | No human holds the lock; agent A acts on `TabOwnerWorkspace()` | The call proceeds; A becomes the contender at §14's lease with no acquisition call of any kind | Implicit acquisition does not work |
| **Ordering** | The lock is held **and** another agent holds the write lease | The deferral is the **ADR-038 D6** text, not the lease's — proving `controlledResult` ran first | The gates have been reordered, which removes the mitigation |

#### §14, FR-020 and FR-021 re-read under the ruling — they hold, and here is the check

§14's scope note says the lease *"assumes the second shape (implicit acquisition on first write to the shared tab) **only** as the lease's contended case, and says so."* **The ruling makes that assumption a ruling, and nothing in the annex changes shape.** Checked line by line:

| Where | Reads correctly under implicit acquisition? | Why |
|---|---|---|
| §14 scope table, row *"Agent vs agent, on the operator's workspace-owned tab"* | **Yes** | The lease still arbitrates exactly that case. Implicit acquisition is *how an agent becomes a contender in it*, not a step before it |
| §14.2 rule 1's composition order (ownership → `controlledResult` → `leaseWrite`) | **Yes, and it is now the mitigation** — restated as such in place | Step 2 running before step 3 is what stops a takeover. It was correct before for a different reason (a human outranks an agent queue); it is now correct for two |
| **FR-020** (the loser retries inside the tool; both writers eventually complete) | **Yes** | It describes what happens *once two agents are contending*. Neither writer asked for the tab; both are there by having acted |
| **FR-021** (no tool is leased when it addresses an agent's own tab set) | **Yes** | The trigger is the resolved `TabOwner`, which is a property of the call, not of a prior acquisition |
| **US-9/AC1**, scenario *two-writers-shared-tab* | **Yes** | Two agents simply issue `browser_navigate` against the workspace-owned tab. There was never an acquisition step in the scenario to remove |
| **FR-019a** / §14 rule 3's biconditional | **Yes, and unchanged** | Membership follows `controlledResult`. A seventh tool would have needed a row and a classification; there is no seventh tool |

**Nothing in §14 is renumbered, rewritten or withdrawn by this ruling.** The only edits are the scope note's *"assumes"* becoming *"is ruled"*, and rule 1 naming itself as the mitigation.

#### New requirements this ruling adds

| FR | What it requires |
|---|---|
| **FR-070** | **Acquisition of the operator's shared tab is implicit and has no surface.** No tool, no policy entry, no wire field, no result key. An agent acts on `TabOwnerWorkspace()` by executing a `controlledResult`-gated tool against it, and the ownership of that tab **does not change** as a result. A structural assertion forbids a `browser_take_control`-shaped registration ever appearing |
| **FR-071** | **The control lock gates implicit acquisition, and the BLOCKED case is asserted.** One test, four cases (blocked; blocked-via-the-resolved-key; allowed; ordering), through the real registered tool path. It must be **red** against a build where `IsControlled` always returns `false` — that is its falsifiability receipt |

---

### 0.8 Profile disk is bounded by PERIODIC CACHE TRIMMING (operator ruling, ADR **D1.9b** ruling 4, 2026-09-01)

**Verbatim (D1.9b ruling 4):** *"Profile disk is bounded by periodic cache trimming, not by a quota and not by deletion alone. Logins are preserved; the disposable cache is trimmed on a schedule."*

#### What this closes

**§12 A24(b)** and the *"no quota"* clause of **§16 MAJ-111**. Both are now answered rather than reopened.

MAJ-111 originally closed the question with *"live profiles are bounded by `max_browsers`, and dead ones are removed by the deletion path, so the unbounded case is closed by deletion rather than by a ceiling."* A24(b) withdrew that closure, correctly and for two independent reasons: **`max_browsers` no longer exists** (FR-056 tombstoned by D1.5a), and **it never bounded bytes anyway** — it bounded live *processes*, while a profile's cache grows during the instance's life and is deliberately **not** reclaimed when that instance is idle-closed or evicted, because surviving is the entire point of the profile (FR-043a, §5). So N workspaces browsed once each left N unbounded cache directories, on a host this project's own notes record filling **twice**. The ruling supplies the bound the withdrawal left missing.

#### A hard per-workspace size cap is REJECTED — recorded with its reason so it is not re-proposed

*"When it binds, something must be discarded mid-session, and the only large items are the cache **and the logins** — discarding the logins is the one outcome this whole design exists to prevent. A trim that only ever removes regenerable data cannot cause that failure."* (D1.9b ruling 4.)

Read that as a **structural** objection, not a tuning one: a size cap over a directory whose contents are *cache + credentials* has no safe action at the moment it binds. Raising the number postpones the moment; it does not change what happens at it. **This is now a §5 non-behaviour** so a later reviewer meeting "just cap the profile directory at 2 GB" can see it was considered and why it was refused, rather than re-deriving it.

#### The rule for what may be removed — a criterion, not a list

A path under `<profileRoot>/ws-<id>/` is **trimmable if and only if** the **browser** wrote it as a performance cache of data it can re-fetch or re-derive, and **no site wrote it through a web storage API**. Everything else is kept. The list below is the derivation of that criterion over the profile layout Chromium ships today; the criterion is what governs when a future Chromium adds a directory nobody here has seen.

**Trimmed (the closed allow-list). Chromium recreates every one of these on the next launch:**

| Path under `<profileRoot>/ws-<id>/` | What it is |
|---|---|
| `Default/Cache/` | the HTTP disk cache — the largest and fastest-growing item in the profile |
| `Default/Code Cache/` | compiled JS and WASM bytecode, re-derived from source on next execution |
| `Default/GPUCache/`, `GrShaderCache/`, `ShaderCache/` | compiled shader and GPU program caches |
| `Default/DawnCache/`, `Default/DawnGraphiteCache/`, `Default/DawnWebGPUCache/` | WebGPU pipeline caches |
| `Default/Service Worker/ScriptCache/` | cached service-worker **script** bodies (the registration itself lives in `Service Worker/Database/` and is **not** trimmed) |
| `Default/optimization_guide_*` | downloaded optimisation-hint models, re-fetched on demand |
| `component_crx_cache/` | downloaded component payloads |

**Never trimmed (the protected set) — and the list is exhaustive only in the sense that everything absent from the allow-list above is protected:**

| Path | Why |
|---|---|
| `Default/Cookies`, `Default/Network/Cookies` | the session cookies. The whole reason the profile survives eviction |
| `Default/Login Data`, `Default/Login Data For Account` | saved credentials |
| `Default/Local Storage/`, `Default/Session Storage/` | web storage — where a great many sites keep the auth token that a cookie alone does not carry |
| `Default/IndexedDB/`, `Default/Service Worker/CacheStorage/`, `Default/Service Worker/Database/` | **origin-owned quota storage, written deliberately by the site.** Chrome's own UI files `CacheStorage` under "cached files"; this spec does **not**, because the criterion above is about *who wrote it*, and a site did |
| `Default/Preferences`, `Default/Web Data`, `Default/Trust Tokens`, `Local State` | profile identity and settings |
| **anything not named in the allow-list** | **the default is KEEP.** The trim is allow-list-driven and must never be deny-list-driven: a Chromium version that adds a new directory must be untouched until someone classifies it |

That last row is a requirement, not a formatting note. A deny-list trim silently widens itself with every Chromium upgrade, and the first thing it would widen into is whatever new place credentials move to.

#### What triggers it

| # | Trigger | Why this one |
|---|---|---|
| **1** | **Immediately after `pool.Close(k)` returns** — idle close, eviction, roster change, gateway `Close()` | **The load-bearing trigger.** It is the exact moment the key stops being live, which is the only moment trimming is both *safe* (nothing holds the files) and *free* (the cache is already cold and no relaunch is provoked). It needs no interval and no measurement |
| **2** | **At boot**, over every `<profileRoot>/ws-*` directory with no live Chrome | Catches profiles orphaned by a `kill -9` (where trigger 1 never ran) and profiles left by any earlier version that never trimmed. Runs after FR-042a's marker reconciliation, so liveness is already established |
| **3** | **On a schedule** — `tools.browser.cache_trim_interval`, default **1 hour**, over every eligible profile | The ADR's *"on a schedule"*. It is the **net**, not the primary path: it catches keys whose close-time trim failed, was interrupted, or was skipped because the key was still live at the time |

**Never against a live profile, and eligibility reuses an existing discriminator rather than inventing one.** A key is eligible iff the pool holds **no live instance** for it **and** its per-key launch lock is **acquirable** — the same two-part test FR-042a uses to tell an orphan from a live neighbour (`takeLaunchLock`, `pkg/tools/browser/coordinator.go:1442-1483`). The reason is not tidiness: Chromium holds these files open, deleting an open file **fails outright on Windows**, and unlinking one under a running Chromium on POSIX frees the bytes only when it closes while leaving its cache index describing entries that are gone.

**Where the 1-hour default comes from, stated as what it is.** It is a **reasoned default, not a measurement** — the same disclosure §12 A22 makes for `idle_close_ttl`, and for the same reason: a number nobody re-derives becomes load-bearing by accident. The derivation is 4 × `idle_close_ttl`'s 15 minutes, so a key that closes just after a sweep is trimmed within one interval of becoming eligible; and a pass is a directory walk plus `os.RemoveAll` over at most N directories, so a pass that removes nothing costs a stat walk. **The correctness of the design does not rest on this value** — trigger 1 does the work and fires within milliseconds of a close. A wrong value here delays a reclaim; it does not lose one. *(Contrast `idle_close_ttl`, where a too-generous value is indistinguishable from having no idle close at all — FR-061. The trim interval has no such failure mode, because it is not the only trigger.)*

**The cost, stated rather than implied:** the next launch on a trimmed profile has a cold cache, so its first page load re-fetches assets and is slower. That is the definition of *regenerable*, and it is the price the ruling accepts in exchange for never touching a login.

#### What this bounds — and what it does NOT

**Bounded: every workspace not under continuous drive**, which is every workspace almost all of the time. It composes with the two TTLs already specified (§12 A22): a tab idles out after `tools.browser.idle_ttl` (5m) and the browser closes after `tools.browser.idle_close_ttl` (15m), so **about twenty minutes after its last action** a workspace's Chrome closes and trigger 1 fires. Its steady-state disk footprint is then its login-bearing data — kilobytes to a few megabytes — not its cache.

**NOT bounded: a workspace under continuous drive.** Its browser never closes, so it is never eligible, and its cache grows for as long as it is driven. **This residual is DECLARED, not defaulted through** (FR-074) — a config-doc line on `tools.browser.cache_trim_interval`, a release-note line, and an operator-visible log — because the failure it leads to is a full root volume, which this project has hit twice and which presents as something other than a browser problem.

**It is escalated as E-9 rather than solved here**, because both available solutions are design changes and neither is a number this spec may invent:

- **Bound Chromium's own cache at launch** (`--disk-cache-size=<bytes>`). This has the right shape — Chromium evicts its **own** cache entries and can never reach a login, so it is *not* the per-workspace size cap the ruling rejected. But its value is a measurement nobody here has, and adding a launch flag on the strength of a guess is how `--renderer-process-limit` got shipped (§12 A23).
- **Trim mid-session**, which requires closing a browser someone is using — the thing the ruling exists to prevent.

#### New requirements this ruling adds

| FR | What it requires |
|---|---|
| **FR-072** | **The periodic profile cache trim**: the allow-list criterion, the three triggers, the live-profile eligibility test, `tools.browser.cache_trim_interval` (default 1h, reload-applied), and one INFO per pass naming the key and the bytes reclaimed |
| **FR-073** | **The protected set survives the trim, proved behaviourally and structurally.** A real-Chrome test logs in, closes the key, trims, relaunches and asserts **still logged in**; a structural test asserts the allow-list contains no path under the protected set and that the implementation is allow-list-driven, not deny-list-driven |
| **FR-074** | **The continuously-driven residual is DECLARED.** Config doc, release note, and a log line naming the unbounded case — the FR-066 pattern, applied to disk |

---

### 0.9 One predicate, two responses — and Windows' refusal is ACCEPTED, not argued (operator ruling, 2026-09-01)

**Verbatim:** *"the windows refusal is fine for now, we are not supporting windows yet."*

This ruling does **two** things, and they are separable. It **accepts** the browser pool's refusal on Windows, on the ground that Windows is not a supported platform rather than on any technical argument — so nothing in FR-065 or FR-066 is softened on Windows' account. And it forces a distinction this document had been carrying implicitly: **`ok=false` is ONE predicate reaching TWO consumers whose correct responses are DIFFERENT.** D1.5c ruled one *mechanism*; it did not rule one *response*, and an implementer who collapses the two has broken a supported deployment. **FR-075** makes the divergence assertable.

#### Windows and gVisor are not the same case, and grouping them is the mistake

Both reach `ok=false`, which is why this document has repeatedly written *"Windows and gVisor"* in one breath. They arrive there by different roads and carry different support status:

| | Windows | gVisor (and other `/proc`-less Linux sandboxes) |
|---|---|---|
| Which file compiles | `meminfo_other.go` (`//go:build !linux`) | **`meminfo_linux.go` — this is Linux** |
| Why the reading fails | the platform has **no reader**; none is written (FR-066) | `/proc/meminfo` is **unreadable**, so `readMeminfoFieldBytesAt` returns `ok=false` and `readMemAvailableBytes` falls through to `readMemTotalBytes()/2` = a fabricated **2 GiB** (`pkg/config/meminfo_linux.go:16`, `:26-30`, `:40-45`) |
| Support status | **not supported yet** — no sandbox backend either (root `CLAUDE.md`: `selectBackendPlatform` returns `FallbackBackend`) | **a supported Linux deployment.** A container platform an operator can legitimately be running on |
| Browser pool response | **refuse to grow. Accepted by this ruling**, and the reason recorded is the platform's unsupported status | **refuse to grow.** Same response, but on the technical ground of FR-065 — a fabricated constant is not a measurement |
| Agent admission response | **hold at the conservative floor of 2** (FR-068a) | **hold at the conservative floor of 2** (FR-068a) — and here it matters, because refusing every turn would break a supported deployment |

**So the divergence is by consumer, not by platform.** Both platforms get the same answer from each consumer; the two consumers give different answers from the same reading. Writing it the other way round — *"Windows refuses, gVisor degrades"* — would be wrong in both halves.

#### The agent floor is EXISTING SHIPPED BEHAVIOUR, and the code says why

FR-068a's floor of 2 is not a concession invented to keep the product working on an awkward host. It is what ships today, and `pkg/config/meminfo_other.go:15-33` records the decision and the regression that produced it, in the code, verbatim:

> *"…that fictitious value produced a default of **585 concurrent agents on ANY** macOS/Windows/BSD box (**or a Linux box whose `/proc/meminfo` is unreadable, e.g. gVisor**) regardless of its actual hardware — a 'fails open' default, replacing the old (conservative-by-construction) default of 2."* (`:20-23`)
>
> *"This now deliberately returns 0: with no real signal … lands on the same **conservative floor of 2** … — failing CONSERVATIVE and saying so, rather than failing open on a number invented from a constant that was never meant to model availability in the first place."* (`:25-33`)

Two things follow that this spec must not undo. **The 585-agent history is the argument against fail-open**, and it is the same argument FR-065 makes for the browser — so the two consumers agree about the *predicate* and about *never fabricating a number*, and disagree only about what to do next. And **the file names gVisor itself**, which is why the Linux-versus-Windows distinction above is the code's own distinction rather than one introduced here.

#### The rule, stated once so an implementer cannot collapse it (FR-075)

> **One exported accessor. One threshold. One `ok=false` predicate. TWO responses, and they are not interchangeable.**
>
> - **Browser pool, `ok=false` ⇒ refuse to grow.** Everywhere, Windows included. A refused browse costs one tool call (FR-065, FR-053, FR-063).
> - **Agent admission, `ok=false` ⇒ hold at the conservative floor of 2 and refuse beyond it.** Everywhere, gVisor included. A refused turn is a gateway that cannot answer a message (FR-068a).

**And it must be able to fail.** A test that stubs the accessor to `ok=false` and asserts only *"the pool refused"* passes on a build that refuses everything, agent turns included — which is the exact defect this section exists to prevent. **FR-075's assertion is the pair, in one test, off one stub:** the pool refuses to grow **and**, in the same run, the agent gate still admits two turns and refuses the third. Either half alone is green on a build that has collapsed the two responses into one.

#### A THIRD case exists, and it is ABSORBED — in §0.10 (ADR D1.5e, 2026-09-01, commit `969a90ffc`)

This section is about the `ok=false` predicate — a host that **says it cannot be measured**. **ADR D1.5e names a case that is neither of the two above:** a host that returns `ok=true` with a **confident, large, wrong** number. A Kubernetes pod with no `limits.memory` reads `max` from its cgroup, so `readCgroupV2LimitBytes` correctly returns `(0, false)` (`pkg/config/meminfo_linux.go:226-240`, verified), `availableRAMBytes` (`pkg/config/config.go:655-661`, verified: it takes the **smaller** of the two figures) therefore falls through to `/proc/meminfo` — **which inside a pod reports the whole node**.

**Why it matters to this section rather than being a separate topic:** every case §0.9 arbitrates fails **conservative**. This one **fails OPEN**, which is the failure mode `pkg/config/meminfo_other.go:20-23` records having already shipped once. So the sentence *"an unmeasurable host is treated as full"* (FR-065) is true and **not sufficient on its own** — a host that is measurable but lying is not covered by it, in either consumer.

**Absorbed in §0.10, which is the next section.** D1.5e's decision is a **startup WARN naming the condition and the remedy** (not a refusal — a bare-metal Linux host also has no cgroup limit and is correct there), and it requires the implementation to detect **containerisation independently of the limit**. §0.10 carries that as **FR-076** and **FR-077**. *A previous revision recorded this paragraph as an open gap rather than absorbing it, which was the right call at the time and is noted here so a reader arriving from that revision sees it answered rather than dropped.* **§0.10 also adds a SECOND fail-open case D1.5e does not cover** — the Linux reader fabricates an invented 4 GB rather than reporting undeterminable (**FR-078**, **FR-079**) — and **corrects one row of D1.5e's own deployment matrix**, which files that case under §0.9's conservative `ok=false` where it does not belong.

#### What changes in the requirements — nothing is rewritten, one row is added

FR-065, FR-066 and FR-068a are **correct as written** and are not amended; this ruling adds the citation for Windows' acceptance and one new row.

| FR | What it requires |
|---|---|
| **FR-075** | **One predicate, two responses — asserted as a pair.** From a single stubbed `ok=false` accessor, in one test: the **browser pool refuses to grow**, and **agent admission still admits up to 2 and refuses the third naming memory**. Plus a doc assertion that Windows' browser refusal is recorded as **accepted because Windows is not yet a supported platform** (operator ruling 2026-09-01), not as a technical limitation, and that the release note distinguishes Windows (unsupported) from a `/proc`-less **Linux** host (supported, same response) |

---

### 0.10 The third case — a host that is measurable and WRONG, and a reader that invents a number (ADR **D1.5e**, absorbed here; 2026-09-01, commit `969a90ffc`)

§0.9 arbitrates **one** predicate: a host that says *"I cannot be measured"* (`ok=false`). **ADR D1.5e names a case that is neither of §0.9's two** — a host that answers with a **confident, large, wrong** number. This section absorbs it. It also carries a **second** instance of the same shape, found while verifying D1.5e and **not** covered by it.

**What makes these two different from everything else in this document:** every other unmeasurable case ends **conservative** — the pool refuses to grow, agents hold at the floor of 2. **These two end with the gate believing there is room that does not exist**, which is the failure `pkg/config/meminfo_other.go:20-23` records this project having already shipped once.

#### The deployment matrix (D1.5e, re-verified against the code on this worktree)

| Deployment | Where the reading comes from | Correct? |
|---|---|---|
| Linux bare metal | `/proc/meminfo` | ✓ |
| Linux + Docker | cgroup v2 `memory.max` / v1 `memory.limit_in_bytes` | ✓ |
| Docker Desktop on macOS or Windows | the Linux VM's cgroup — the container **is** Linux | ✓ |
| Fly.io (Firecracker microVM) | the VM's own `/proc/meminfo` | ✓ |
| Kubernetes, `limits.memory` **set** | the pod's cgroup | ✓ |
| **Kubernetes, no `limits.memory`** | **the NODE's memory** | ✗ — **case 1**, fails OPEN |
| macOS native | the D1.5b reader (FR-064) | ✓ once written |
| **Linux with an unreadable `/proc/meminfo`** (gVisor / GKE Sandbox, a masked or bind-mounted-over `/proc`) | **an invented 4 GB constant, halved** | ✗ — **case 2**, fails OPEN |
| Windows native | no reader | unsupported (FR-066) |

**The last row is the one D1.5e gets wrong**, and it is the only correction this pass makes to the ADR's matrix. D1.5e records the gVisor row as *"falls back (D1.5b)"* — i.e. as an instance of §0.9's conservative `ok=false`. It is not. See case 2.

#### Case 1 — a Kubernetes pod with no `limits.memory` sizes itself against the whole node

The chain, verified end to end:

1. A pod without `limits.memory` has no cgroup memory limit, so `memory.max` reads the literal string `max`.
2. `readCgroupV2LimitBytes` (`pkg/config/meminfo_linux.go:226-240`) **correctly** returns `(0, false)` for `max` (`:232-234`). There is no defect here — the reader is right.
3. `readCgroupMemoryAvailableBytesAt` therefore has no cgroup figure to offer, and `availableRAMBytes` (`pkg/config/config.go:655-661`) falls through to `/proc/meminfo` alone.
4. **Inside a pod, `/proc/meminfo` reports the whole node.** So the gateway sizes itself against a 64 GB node while the scheduler may never let it have a fraction of that.

The consequence is not a slow degradation. The pod launches browsers and agent turns until the **node's** OOM killer intervenes, and Kubernetes kills **the pod** — not the browser that caused it. The operator sees a restarting pod, not a memory limit doing its job.

**This is a deployment misconfiguration, not a code defect**, and `limits.memory` is optional — plenty of manifests set only `requests`. That is exactly why it is worth a requirement: nothing in the product tells the operator.

**Ruled response: WARN at startup, do NOT refuse (D1.5e).** Reading `max` is an unambiguous signal that no container limit exists — but a bare-metal Linux host also has no cgroup limit and is **perfectly correct**, so refusing would break the ordinary case.

#### The distinction that makes the warning worth having — and that makes it worthless if it is got wrong

> **"No cgroup limit" is *correct* on bare metal and *dangerous* in a pod. The two are indistinguishable from the limit alone.**

So **containerisation must be detected independently of the limit** (FR-076). Without that independent signal the warning has only two possible shapes, and both are useless:

- **Keyed on the limit alone ⇒ it fires on every bare-metal start.** A warning that always fires is not a warning; operators filter it, and it is gone by the time it matters.
- **Keyed on the limit being *present* ⇒ it never fires in the case it exists for**, because the case *is* the absence of a limit.

**And the obvious reuse in this repo does not work.** `isRunningInDocker` (`pkg/gateway/sandbox_apply.go:185-201`) already answers "am I in a container?" from two signals: `OMNIPUS_IN_DOCKER=1`, and the presence of `/.dockerenv` (`:179`). **`/.dockerenv` is a Docker runtime marker.** Kubernetes runs containerd or CRI-O, neither of which drops that file — so wiring the warning to this predicate produces the *never fires* shape above, **in precisely the deployment D1.5e is about**. FR-076 states the signal set that does cover it, and names the residual it still does not (A29).

#### Case 2 — the Linux reader FABRICATES rather than reporting "unmeasurable" (NOT in D1.5e; verified in this pass)

`pkg/config/meminfo_linux.go:14-16`:

```go
// fallbackTotalRAMBytes is the conservative assumption used when
// /proc/meminfo cannot be read or parsed at all.
const fallbackTotalRAMBytes = 4 * 1024 * 1024 * 1024 // 4 GB
```

`readMemTotalBytes` (`:26-31`) returns that constant when `/proc/meminfo` cannot be read; `readMemAvailableBytes` (`:40-45`) then returns `readMemTotalBytes() / 2`. **So on a Linux host whose `/proc/meminfo` is unreadable, the reader does not report "unmeasurable" — it reports an invented 2 GiB, and it reports it as a determinable figure.** There is no `ok` flag on either function; `0` is the only value the rest of the system reads as *undeterminable* (`pkg/config/meminfo_other.go:42-44`), and this path never returns it.

**Verified figures, because the brief asked for them rather than assuming them:**

| Input | `readMemTotalBytes()` | `readMemAvailableBytes()` | Reads as undeterminable? |
|---|---|---|---|
| `/proc/meminfo` unreadable (open fails) | `fallbackTotalRAMBytes` = **4 GiB** (`:30`) | `4 GiB / 2` = **2 GiB** (`:44`) | **No** |
| `MemTotal` present, `MemAvailable` absent (pre-3.14 kernel) | the real `MemTotal` | **real `MemTotal` / 2** | No — **and correctly so** |
| `MemTotal` present, `MemAvailable` malformed | the real `MemTotal` | real `MemTotal` / 2 | No — correctly so |
| non-Linux | *(deleted; Linux-only symbol)* | **0** (`meminfo_other.go:43`) | **Yes** |

**Two things follow, and the second is why this belongs in this document at all.**

**(a) It defeats FR-065 and FR-068a on that path.** Both branch on *undeterminable*. This path never says undeterminable — it says *"2 GiB"* with confidence. On a 512 MB container the pool cheerfully grows and agent admission cheerfully admits.

**(b) It is the exact pattern this codebase already condemned, in writing, for macOS — and fixed there.** `pkg/config/meminfo_other.go:15-33` records the previous fabricated constant producing *"a default of **585 concurrent agents on ANY** macOS/Windows/BSD box … regardless of its actual hardware — a 'fails open' default"* (`:20-23`), and states the deliberate replacement: *"failing CONSERVATIVE and saying so, rather than failing open on a number invented from a constant that was never meant to model availability in the first place"* (`:25-33`). **The same comment names this very case in parentheses** — *"or a Linux box whose `/proc/meminfo` is unreadable, e.g. gVisor"* — so the code already knows the Linux path has the bug, and fixed only the non-Linux one.

**The arithmetic is not merely analogous, it is identical.** With `bytesPerAgent = 3.5 MB` (`pkg/config/config.go:608`), `autoDetectMaxParallel` (`:614-618`) computes `2 GiB / 3.5 MiB = 585` — **the same 585** the comment condemns, still shipping today on any Linux host with an unreadable `/proc/meminfo`.

**What the fabrication does AFTER this spec's other changes land, which is different and must be said.** FR-067 deletes `bytesPerAgent` and the division, so the *585-agent default* disappears on its own. **The fail-open does not.** The invented 2 GiB then flows into `availableRAMBytes` → the live pressure gate → **both** consumers (FR-068), so the browser pool and agent admission both believe there is 2 GiB of headroom on a host that may have 512 MB. Deleting the division fixes the symptom that was measured in 2026-08; it does not fix the reader.

**Resolution (FR-078), consistent with D1.5b's rule:** an unreadable or unparseable `/proc/meminfo` must be reported as **undeterminable**, so the refuse/floor behaviour §0.9 already specifies engages. **The legitimate half-of-total heuristic is preserved** — when `MemTotal` is real and only `MemAvailable` is missing, half of a real total is a real estimate and stays.

#### What else changes, and for which deployments — flagged rather than discovered

**One behaviour change outside gVisor, and it is a regression the fix would introduce if left unspecified.** `availableRAMBytes` (`pkg/config/config.go:655-661`) combines the two signals like this:

```go
avail := readMemAvailableBytes()
if cgAvail, ok := readCgroupMemoryAvailableBytes(); ok && cgAvail < avail {
    avail = cgAvail
}
return avail
```

It takes the **smaller** of the two. Once FR-078 makes the meminfo half return `0` for undeterminable, `cgAvail < 0` is **never true**, so **a perfectly good cgroup reading is discarded** and the host reads as unmeasurable. That is conservative, not fail-open, so it is not a safety defect — but it is wrong, and it lands on a real deployment: **a container with an unreadable `/proc/meminfo` that DOES set `limits.memory`** (a GKE Sandbox pod with limits is exactly this). Such a host is fully measurable through its cgroup and would nonetheless be held at the floor of 2 for no reason. **FR-079 fixes the combination rule: `min` over the *determinable* signals only; `ok=false` only when *neither* is determinable.**

**No other deployment changes.** Bare metal, Docker, Fly and Kubernetes-with-limits all read `/proc/meminfo` or a cgroup successfully today and continue to.

**And one test currently pins the bug**, which is why FR-078 cannot be delivered as a quiet edit: `TestReadMemAvailableBytes_MissingFile` (`pkg/config/meminfo_linux_test.go:55-65`) asserts the returned value **is** `fallbackTotalRAMBytes / 2`. It is a green test whose oracle is the defect. §10.1 lists it as a **deliberate semantics change** with its new oracle, so it is not mistaken for collateral damage during implementation.

#### Two items this pass deliberately leaves as they are

- **E-9** (a workspace under *continuous* drive never becomes trimmable, so its cache is unbounded) stays an **escalation**, unchanged. Both candidate fixes are design changes — a `--disk-cache-size` value nobody here has measured, or a mid-session trim that closes a browser someone is using — and this document does not get to pick either. §0.5 E-9, FR-074.
- **§12 A25's consequence** — *an agent may close the operator's tab* — stays recorded plainly where it is. It follows from D1.9b ruling 1 (closing is acting, and `browser_close_tab` is `controlledResult`-gated at `pkg/tools/browser/tabs.go:171`) rather than being new, and nothing in this pass touches it.

#### What changes in the requirements — four new rows, nothing renumbered or rewritten

FR-064 … FR-075 are **correct as written** and are not amended.

| FR | What it requires |
|---|---|
| **FR-076** | **Containerisation is detected INDEPENDENTLY of the memory limit.** A `pkg/config` predicate, with test-overridable path/env seams on the existing `procMeminfoPath` / `cgroupRoot` / `dockerenvPath` pattern. **`/.dockerenv` alone is explicitly insufficient** — Kubernetes uses containerd/CRI-O and drops no such file, so the Docker-only reuse never fires in the target case |
| **FR-077** | **The node-memory WARN fires in the containerised-and-unlimited case and is SILENT everywhere else.** Once at startup, naming the condition and the remedy (`resources.limits.memory`). **A WARN, never a refusal** — bare metal has no cgroup limit and is correct. Asserted in **three** directions, and the two silent ones are load-bearing |
| **FR-078** | **The Linux reader stops fabricating.** An unreadable or unparseable `/proc/meminfo` reports **undeterminable**, not `fallbackTotalRAMBytes` or half of it; the constant is deleted as a symbol. The pre-3.14 `MemTotal`-real / `MemAvailable`-absent heuristic is **preserved** |
| **FR-079** | **One undeterminable signal does not discard the other.** `availableRAMBytes` takes the minimum over the **determinable** signals only, and answers `ok=false` only when **neither** is determinable |

---

### §0.10 Absorbing ADR D1.5e — and a second fail-open path it does not cover

**Two requirements are D1.5e's ruling.** A Kubernetes pod with no
`resources.limits.memory` reads a *confident, wrong* number — the node's — and
is the only case in this design that ends with **more** work admitted rather
than less. **FR-076** detects containerisation *independently of the limit*;
**FR-077** warns once at startup, and is **silent** on bare metal and in a
limited pod. A warning that always fires is not a warning, so two of its three
assertion directions are the silent ones.

**Two are not in D1.5e**, and they close a second path to the same failure,
found while verifying the first.

`readMemTotalBytes` returns a fabricated `fallbackTotalRAMBytes` = **4 GB**
when `/proc/meminfo` cannot be read (`pkg/config/meminfo_linux.go:14-16`), and
`readMemAvailableBytes` returns **half of it — 2 GB** (`:40-45`). Neither
reports failure — and the reason is sharper than "the flag is set wrong":
**neither function has a flag.** Both are declared `func …() uint64`
(`pkg/config/meminfo_linux.go:26`, `:40`), so there is no `ok` to be true or
false; the fabricated number is returned through the same single-valued
signature a real reading uses, and nothing downstream can tell them apart.
*(This document said "`ok` is true" here until this pass. It is wrong in a way
that matters: it implies a two-valued reader whose flag is set incorrectly —
a one-line fix — when the actual work is FR-078's change to the signature and
every caller of it. §0.10's case-2 body already stated this correctly —
"There is no `ok` flag on either function" — so the document contradicted
itself across two pages.)*

> **⚠️ The ADR carries the same error and this spec does not get to fix it.**
> ADR-072 **D1.5e**'s deployment matrix describes the gVisor row in the same
> `ok`-is-true terms. **Flagged for the ADR owner, alongside §0.10's other
> correction to that matrix** (the row is filed under §0.9's conservative
> cases and belongs among the fail-open ones). Both are ADR edits; neither is
> made here.

**That defeats FR-065 and FR-068a entirely on that path.** Both branch on
*undeterminable*; this path never says undeterminable. On a gVisor container —
a **supported** Linux deployment where that file is unreadable — a 512 MB
container is told it has 2 GB and admits accordingly.

**It is also the exact pattern this codebase already condemned, on the other
platform.** `pkg/config/meminfo_other.go:20-33` records that a previous
fabricated constant produced *"585 concurrent agents on ANY box regardless of
its actual hardware — a fails-open default"*, and that returning `0` instead
was deliberate: *"failing CONSERVATIVE and saying so, rather than failing open
on a number invented from a constant that was never meant to model
availability."* **The macOS path was fixed. The Linux path still does it.**

**FR-078** deletes the fabrication — an unreadable `/proc/meminfo` reports
undeterminable. **FR-079** ensures one dead signal does not discard a live one:
`availableRAMBytes` takes the minimum over the **determinable** signals only.

**And this corrects one row of D1.5e's own deployment matrix.** That table files
gVisor under "falls back (D1.5b)", i.e. among the conservative cases. It does
not belong there — until FR-078 lands it is a **fail-open** case, in the same
class as the unlimited pod, not in the class of hosts that refuse. **The ADR row
needs the same correction.**

---

## 1. Overview / Actors / Scope

**Problem.** The browser — its tab set *and* its logins — is owned by the **agent**, so it strands the moment the operator switches who they are talking to. `AgentLoop.browserMgrs` is `map[agentID]*browser.BrowserManager` (`pkg/agent/loop.go::AgentLoop`), populated by a **per-agent** registration loop (`loop.go::registerSharedTools`) that calls `browser.RegisterTools` and then `mgr.AttachSharedChrome(coordinator, agentID)`. `RegisterTools` (`pkg/tools/browser/register.go:41-84`) constructs a manager and **binds it into eleven tool structs** — `&NavigateTool{mgr: mgr}` at `:65` through `&OpenTabTool{mgr: mgr}` at `:81`. Every tool then addresses its tabs through one hardcoded key, `DefaultSessionID = "default"` (`pkg/tools/browser/tools.go:63`). The operator browses with Mia, switches the chat to Jim, and Jim — correctly, for his own manager — reports zero tabs while telling the operator the browser is "shared across the workspace", because five model-visible strings say exactly that (`tabs.go:32,86,143,206`; `tools.go:415`).

**Solution (ADR-072 D1).** Move ownership from the **agent** to the **workspace**:

1. **One Chrome process and one on-disk profile per workspace** (D1.4), replacing ADR-043's single process-wide Chrome and its per-agent CDP browser contexts. This is what isolates cookies, and it is what keeps the live panel capturable.
2. **One `BrowserManager` per workspace**, shared by every agent on that workspace's team, resolved **per tool call** from the turn's context — never captured at registration time.
3. **Every agent on the workspace shares the browser and its logins**, delegated sub-turns included (D1.10).
4. **Inside it, tabs belong to the SESSION** (D1.9c, superseding D1.9a): a tab opened in a chat belongs to that chat — *"no matter which agent is on it"* — and an **operator**-opened tab belongs to the workspace and is visible to every agent on it. Point 2 collapses the managers, so this separation has to be carried explicitly on the tab set or it is silently deleted (§0.2a, FR-080).

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
- **The signal that gate reads, on every supported platform (new with ADR D1.5b).** A **Darwin** implementation of the memory readers (FR-064), so macOS — where `PER_BROWSER_COST` was measured — stops being blind; a **two-valued** availability accessor under which an **unmeasurable host refuses to grow rather than admitting** (FR-065); and **Windows declared** `degraded-unsupported` for the pool, with a code placeholder, a release-note line and a config-doc line rather than a silent gap (FR-066).
- **That signal's SECOND consumer: agent concurrency (new with ADR D1.5c/D1.5d, scope signed off `ddd9789a4`).** Not browser work, and in scope by operator ruling rather than by domain: `bytesPerAgent` and the computed `max_parallel_agents` default are **deleted** and `EffectiveMaxParallelAgents` becomes two-valued (FR-067); agent admission consults the **same** live gate with **no per-unit cost** (FR-068); an unmeasurable host **holds at the floor and refuses to grow, never refuses to run** (FR-068a); and the announcement is *"there is no longer a computed default"*, with the Settings panel no longer recommending a number the system did not choose (FR-069). **One mechanism, two consumers, one deliverable** — §0.6b.
- **The pool's lifecycle edges**, which the previous draft left to inference and which are the bulk of round-2's MAJOR findings: the admission edge semantics that survive the counters' deletion (FR-038a); the whole-Chrome idle window as a named config key with a named caller and a specified post-close state (FR-040a); boot reconciliation of the N ownership markers so orphan Chromes cannot sit outside the cap (FR-042a); per-key stale-singleton cleanup, without which FR-043's "the profile survives" is false after a crash (FR-042b); the profile directory's **deletion** path (FR-043a); the profile root's relationship to `cfg.ProfileDir` and to the managed-Chromium install root (FR-037a); and the boot preprovision path, which a lazily-created pool silently breaks (FR-016c).
- **Per-session tab ownership and the operator's shared tab** (FR-080) — D1.9c. *(The per-agent enforcement of `cfg.MaxTabs` that used to fall out of this is **FR-049, tombstoned**: the key is deleted — §0.6.)*
- **The team-membership elevation-of-privilege disclosure** decided in ADR D2.11 (FR-047): the Workspace → Team editing UI must state, at the point of adding an agent, that the agent gains every live browser session on that workspace. **Claimed here rather than left ownerless.** §1's out-of-scope list excludes only D2.11's *information-disclosure* bullet, so the *elevation-of-privilege* bullet was in scope by wording and owned by neither spec; D1.10 makes it strictly worse than when the ADR wrote it, because unattended delegated work now inherits those logins too.
- One `BrowserManager` per workspace, with **per-`Execute` manager resolution** replacing registration-time binding (FR-002a).
- Every `DefaultSessionID` consumer — **37 non-comment references**, enumerated in §2 — re-pointed at the resolved key; the constant deleted (FR-002b), including `controlledResult` (FR-002c).
- Workspace resolution ladder with **no constant fallback**, a named failure, and a distinguishable gateway/panel reason (FR-007, FR-008, FR-008a).
- Reload-prune liveness keyed by browsing key; per-key idempotent registration (FR-026a, FR-026b).
- **Two-state** `browser_list_tabs` (D1.12 — the third, "not permitted" state is **withdrawn by the ADR** and its whole downstream stack is tombstoned here; see §17 C3), plus the five model-visible description strings with their **replacement literals specified** (FR-013, FR-014, FR-015, FR-034).
- The **write lease, rescoped to writer-vs-writer — two TURNS — on the operator's shared tab *and* on a session's own tab set** (D1.9c, FR-081) — §14 is the single normative definition (FR-019…FR-024, FR-019a). *(It read "agent-vs-agent … the operator's shared tab" under D1.9a; the contender is a second turn, not a second agent, §0.2a.)*
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
| `BrowserManager.sessions` (`manager.go:338`) | **modifies** | `map[string]*sessionEntry`. **NOT one entry per manager.** Under **D1.9c** the manager holds **one entry per SESSION that has browsed, plus one workspace-owned entry for the operator's tabs** — the map is where the session dimension is carried (FR-080). A design that keeps a single entry per manager silently merges every session's tabs on the workspace. *(Under the superseded D1.9a this row said "one entry per agent". The map's key type does not change — it was always a session id; what changes is that it must hold the turn's real `transcriptSessionID` instead of the `DefaultSessionID` constant.)* |
| `sessionEntry.tabs` (`manager.go:203-204`, `tabs []*tabEntry`) | **reuses** | **The trap, §0.2a.** The tab set belongs to the *sessionEntry* — nominally a browsing context (ADR-041 D1), in practice one constant key. Agents are separated today only because each has its own manager. FR-001 removes that separation; **FR-080** replaces it with the real session key. *(Also the site of a fact FR-059 changes and this row must not be read past: `ReapIdleSessions` reaches `coord.ReleaseTab` via `m.releaseGlobalTab()` at `manager.go:3118` **today** — FR-059 deletes both, which is why no post-change AC or scenario may name them; round-4 M-404.)* |
| `BrowserManager.totalTabCountLocked` (`manager.go:1549-1555`) | **RETIRES as an enforcement point** | Sums `len(se.tabs)` across **every** session in the manager, and is the `cfg.MaxTabs` enforcement point at **five** sites: `:1139` (`createFirstTab`), `:2005` and `:2047` (`OpenTab`), `:2216` and `:2286` (`adoptTarget`). Revision 4's FR-049 re-scoped it to the agent's own tab set; **D1.5a deletes the cap instead, so FR-049 is tombstoned** (§0.6). **Those same five sites are where FR-060 puts the memory gate** — the tab-open path a runaway loop never leaves. The helper itself may survive as a count for logging/telemetry; what must not survive is a *refusal* derived from a counter |
| `BrowserConfig.MaxTabs` (`manager.go:36`, default **5** at `:124`; config key `tools.browser.max_tabs`, `config.go:3633`, applied `loop.go:2314-2315`) | **RETIRES** | Revision 4 gave it back an owner (FR-049) after the re-key would have turned a 5-tab per-**agent** cap into 5 for the whole team. **ADR D1.5a deletes the key, the field, the default and all 18 executable references instead** (FR-059). Its config doc — *"the per-agent courtesy cap … the guard most operators actually want"* (`config.go:3662-3663`) — is deleted with it; a doc for a key that no longer exists is worse than none |
| `LiveViewRegistry.TakeControl` / `IsControlled` (`live.go::TakeControl` `:1241`, `::ReleaseControl` `:1287`, `::Controller` `:1298`, `::IsControlled` `:1313`) | **reuses (must NOT be replaced)** | ADR-038 D6's take-the-wheel lock, and **the whole of operator-vs-agent arbitration under D1.9a**. §14's lease is agent-vs-agent only and never substitutes for this. *(Citation corrected: the round-4 brief gave `live.go:1236-1310`, which stops three lines short of `IsControlled` at `:1313`.)* |
| `BrowserManager.AttachSharedChrome` (`manager.go:537`) | **modifies** | Sets `m.agentID` (`:375`) — the coordinator's Register/Release/RemoveAgent key. Becomes the browsing key |
| `BrowserManager.ListTabs` (`manager.go:1605`) | **modifies** | `return nil, 0, nil` on a missing session (`:1609-1611`) — the two-state collapse (FR-013) |
| `BrowserManager.sessionExists` (`manager.go:2378`) | **reuses** | Already backs `browser_started` (`tabs.go:58`) — the existing half of D1.12 |
| `BrowserManager.ViewerAttached` / `ViewerDetached` (`manager.go::ViewerAttached`, `::ViewerDetached`; `se.viewers++` / `--` in their bodies) | **extends** | No exported count accessor exists → FR-010 adds `Viewers()`, used by the **reaper and the pool's idle-close**, not for attendance (§0.2) |
| `BrowserManager.ReapIdleSessions` (`manager.go:2986`) | **extends** | Per-tab TTL, `se.viewers > 0` pin, zero-tab `emptySince` branch — all implemented. **CORRECTED (round-2 CRIT-102):** the previous draft claimed here and in §15 that this method "deletes `m.sessions` entries only; it never touches the coordinator and never closes a browser", marked *verified*. **That was false.** It collects `se.browserCancel` into `reapedBrowsers` in **both** removal branches (`:3027-3032` stranded-empty-session, `:3073-3078` all-tabs-idle), executes those cancels after unlocking (`:3123-3125`) — cancelling the **browser-owning** chromedp context — cancels per-tab contexts at `:3106-3107`, and reaches the coordinator through `m.releaseGlobalTab()` (`:3118` → `:3358-3365` → `coord.ReleaseTab(agentID)`). **The narrow claim that survives, and it is the only one this spec relies on:** `ReapIdleSessions` never calls `RemoveAgent` and never calls `disposeBrowserContextRaw`, so the coordinator's own per-key state and the Chrome **process** are untouched. Whole-Chrome close is genuinely new work (FR-040), but the disposal machinery is not absent, and the reaper↔pool contract is therefore a real interaction that FR-040a must specify — not a greenfield hook |
| the idle sweep goroutine (`gateway.go:5321-5355`, `const reapInterval = time.Minute`) | **extends** | The **named caller** the previous draft never identified. It ranges `agentLoop.BrowserManagers()` and calls `mgr.ReapIdleSessions()` on a 1-minute ticker, each tick individually `recover()`ed. FR-040a hangs the whole-Chrome idle close off this same tick, after the per-manager loop. The interval doc (`:5310-5320`) states the invariant a second TTL must also respect: the sweep interval must stay well under the TTL or the TTL becomes a floor |
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
| `tools.ToolTranscriptSessionID` (`pkg/tools/base.go:199-203`) | **reuses — it is the TAB-OWNERSHIP key** | **Corrected round-5 (M-502): this row said *"not used"*.** It is not a *browsing* key — that is still §0.2, and the browser is `ws:<workspaceID>` — but under **D1.9c it is the `TabOwner` key** (FR-080): `TabOwnerSession(tools.ToolTranscriptSessionID(ctx))` is what every ordinary browser tool call addresses. **It returns `""` when unset** (`:199`, and the accessor cannot distinguish unset from empty), which is an ordinary reachable state on several turn types — see §5's non-behaviour and `ErrNoTabOwner` (§3.1). It is **never** `routingSessionID` (§5, FR-080) |
| `workspace.FindForAgentPreferring` (`find_for_agent.go:176`) | **reuses** | Preferred-id fast path → `FindForAgent` (`:83`); sorted-first tie-break + WARN documented at `:45-48` |
| `ensureDefaultWorkspace` (`rest_workspaces.go:468`) / `defaultWorkspaceTeam` (`rest_workspace_delegation.go:359-379`) | **reuses** | Seeds "My Workspace" with `coreagent.All() ∩ configured agents` — Jim and Ray included — on every boot (`gateway.go:5013`) |
| `pkg/agent/tool_denial.go:206-210` | **NOT modified — and this row is retained to say so** | `policy_denied` → `ModelMessage: "Tool execution denied by policy."`, generic for every tool. Revision 3 listed it as **modifies** for FR-014a. **FR-014a is withdrawn (ADR D1.12, §17 C3)** and this file is untouched: `FilterToolsByPolicy` (`pkg/tools/compositor.go:436-438`) `continue`s past a deny verdict, so a browser tool a policy denies is never sent to the model and this message has **no production caller** for it |
| `AgentLoop` `AutoDenyAsk` (`loop.go:594-599`, honoured at `loop.go::runTurn`'s `ts.opts.AutoDenyAsk` branch) | **reuses** | Set true only for headless/scheduled runs (`loop.go:6958`); **not inherited by delegated sub-turns — issue #659** (FR-032) |
| `BrowserAttachFrame.yaml`, `BrowserWebRTCOfferFrame.yaml`, `BrowserInspectRequest.yaml`, **`PerformanceSettings.yaml`** | **modifies (prose)** | See FR-016/FR-017 and §15 MAJ-004 — one of the first three is a *semantic* reversal, not a cosmetic edit. **`PerformanceSettings.yaml:10-16, 27-29` is the FOURTH** and it arrived with **FR-069** (D1.5d): the `capped=false` case must present as *automatic — bounded by available memory* rather than as the integer 2000. **SC-007 still holds** — it is a `description:` change like the other three, adds no path and retypes no field — but this inventory said *three* until round-5 m-501, and three is what a reviewer would have checked the `contracts/` diff against |

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
| **`pkg/config` availability accessor** (unexported `uint64` → exported `(bytes, ok)`; new `meminfo_darwin.go`; `meminfo_other.go` narrowed to `!linux && !darwin`; new `meminfo_windows.go`) | **MEDIUM** | `availableRAMBytes` (`config.go:655`) and its one existing consumer `autoDetectMaxParallel` (`config.go:614-615`); `meminfo_other_test.go`'s `//go:build !linux` must narrow with the file it tests, or it stops compiling on Darwin | **Shipped concurrency sizing** — `EffectiveMaxParallelAgents` feeds `pkg/agent`'s `AdmissionController` and `TaskExecutor.syncDispatchCapacity`. **REWRITTEN by D1.5c/D1.5d (§0.6b): the row used to end here with "the macOS default moves 2 → 2000, flagged not absorbed". It no longer does, and the consumer is no longer external.** `bytesPerAgent` and `autoDetectMaxParallel`'s division are **deleted** (FR-067), so no computed default exists to move on any platform. The accessor change is no longer shape-only for this consumer: `EffectiveMaxParallelAgents` itself becomes `(n int, capped bool)` and, absent an explicit setting, returns `(physicalConcurrencySafetyCeiling, false)` — a backstop, not a capacity. **That is a HIGH-impact surface and it is tracked as its own row below**, because it changes five call sites outside `pkg/config` and a wire field's meaning — but it is **one deliverable with this row**, not a separate landing. This row covers the reader; the next covers its second consumer. **§0.5 E-7 and E-8 are both ruled; neither is a live escalation** |
| **`EffectiveMaxParallelAgents`' signature and its computed default** (`pkg/config/config.go:454-467`; deletion of `bytesPerAgent` `:608`, `autoDetectMaxParallel` `:614-618`, `clampParallel` `:557-566`) — **FR-067, and NOT browser work** | **HIGH** | Five call sites, none of them in `pkg/tools/browser`: `pkg/agent/task_executor.go:250-260` (dispatch semaphore), `pkg/agent/subturn.go::getSubTurnConfig` (in-turn fan-out), `pkg/agent/admission.go:216-218` (`ResolveRootDelegationCap`), `pkg/gateway/rest_performance.go:71-78` and `:149-154` (the wire). Plus `contracts/components/schemas/PerformanceSettings.yaml:27-29`, whose `minimum: 1` makes a bare-`0` sentinel schema-invalid | **Every agent-concurrency admission path in the process.** A wrong answer here is not a slow browser — it is a deadlocked dispatch semaphore (capacity 0) or a thread-exhaustion abort (unbounded). **In scope, and it ships in the SAME deliverable as the browser gate** (operator scope sign-off `ddd9789a4`, §0.5 E-8 RULED) — the "independently landable browser side" hedge an earlier revision carried here is withdrawn, because half-adopting one mechanism is two mechanisms |
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
// A BrowsingKey names a BROWSER, not a tab set. Under D1.9c (2026-09-02) the
// tab sets live one level down, inside the manager this key resolves to: one
// per SESSION that has browsed, plus one owned by the workspace for the
// operator's own tabs.
// See TabOwner and FR-080 — a key alone does not tell you whose tabs you are
// looking at, and code that assumes it does merges every session's tabs.
//
// The key is the SESSION's, not the agent's: switching a chat from Mia to Jim
// does not move, hide or duplicate a tab, and two sessions on one workspace
// never see each other's. Anything reading "per agent" here is pre-D1.9c.
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
// browser a BrowsingKey names (ADR-072 D1.9c, operator ruling 2026-09-02).
// This type is the explicit carrier of the SESSION dimension that today lives
// only in the accident of one BrowserManager per agent — FR-080.
//
// Two shapes, and deliberately no third:
//
//   TabOwnerSession(transcriptSessionID)
//                           the tabs opened in that chat.  Visible and
//                           drivable by whichever agent the chat is currently
//                           on — switching Mia to Jim moves nothing.  Never
//                           visible to another session.
//   TabOwnerWorkspace       the tabs the OPERATOR opened through the live
//                           panel.  Visible to every agent on the workspace;
//                           drivable by the operator, and by an agent that
//                           simply ACTS on it — acquisition is IMPLICIT and
//                           has no surface (FR-070, §0.7; §0.5 E-1 is RULED,
//                           not open).
//
// It resolves to the manager's sessions-map key, so the map holds one entry
// per SESSION that has browsed plus at most one workspace entry. There is no
// "all tabs" owner: a tool that wants both sets asks for both and says which
// is which, because "whose tab is this" is exactly the question ADR-072 §1.1
// records an agent getting wrong.
//
// There is deliberately NO TabOwnerAgent. Keying on the agent is the
// SUPERSEDED D1.9a shape (~~FR-048~~ → FR-080); reintroducing it splits one
// chat's tabs across the agents that took turns in it.
//
// The id is transcriptSessionID and NEVER routingSessionID (§5, FR-080):
// routingSessionID is inherited verbatim through a delegation subtree
// (pkg/agent/subturn.go:1339), so it would merge every descendant's tabs into
// the root's.
//
// TabOwnerSession("") IS NOT AN OWNER — it is a named failure. An empty
// transcript session id is an ordinary, reachable state on several turn types
// (§5's non-behaviour, FR-080), and minting an owner from it gives every
// transcript-less turn on the workspace one shared tab set, which is the
// silent merge this type exists to prevent. Constructing it returns
// ErrNoTabOwner rather than a usable value; the browser tool reports it and
// opens nothing.
type TabOwner struct{ s string }

// ErrNoTabOwner is returned when the turn carries no transcriptSessionID and
// therefore has no tab set of its own. Like ErrNoBrowsingContext it must be
// RETURNED, never swallowed into a shared or workspace-owned set.
var ErrNoTabOwner = errors.New(
    "browser: this turn has no transcript session, so it has no tabs of its own")

func TabOwnerSession(transcriptSessionID string) (TabOwner, error)
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
    // It also returns the TabOwner this turn addresses:
    // TabOwnerSession(tools.ToolTranscriptSessionID(ctx)) for every ordinary
    // tool call. A tool that must reach the operator's shared tab asks for
    // TabOwnerWorkspace() explicitly (FR-080). When the turn carries no
    // transcript session id this returns ErrNoTabOwner — it does NOT fall
    // back to the workspace-owned set, which would let a transcript-less turn
    // drive the operator's tabs.
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
//                stated on the gate rather than on the number: no instance is
//                launched, and no tab is opened, while the memory gate refuses
//                (FR-057, FR-060) OR cannot measure the host (FR-065 -- past
//                the floor, an unmeasurable host is FULL, not empty).
//
//                THE FLOOR IS PART OF THE INVARIANT, not an exception to it
//                (FR-082, round-4 C-401). "Refuses to GROW" is meaningless
//                until "grow from what" is fixed, and it is fixed at ONE
//                browser and ONE tab in it, per HOST. On an unmeasurable host
//                the FIRST launch and the FIRST tab SUCCEED; the SECOND of
//                each is refused, naming memory. An earlier wording of this
//                invariant said NO instance and NO tab while unmeasurable,
//                full stop -- a pool built from it removes browsing entirely
//                from gVisor and GKE Sandbox, which §0.9 establishes as
//                SUPPORTED Linux deployments (they reach ok=false through
//                meminfo_linux.go's fallback, not through the non-Linux
//                stub), and test 83 fails if either the first browser or the
//                first tab is refused.
//
//                Revision 3 asserted len(live) <= cap and
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
//
// If the gate cannot MEASURE the host at all -- Headroom's measurable == false
// -- Acquire admits the FIRST instance on the host (FR-082's floor:
// len(p.LiveKeys()) == 0) and refuses every launch past it. Read that as a
// floor and not as an exception: a floor of ZERO removes browsing from gVisor
// and GKE Sandbox, /proc-less Linux deployments §0.9 establishes as SUPPORTED,
// on the strength of a reading the host declines to give; a floor of TWO is
// unpriced, because the second Chrome's ~182 MB PER_BROWSER_COST is exactly
// the figure an unmeasurable host cannot check itself against. The same floor
// applies one level down at the tab gate: the first tab in that instance
// opens, the second is refused (FR-060, FR-082). Test 83 asserts BOTH halves
// and fails if either the first browser or the first tab is refused.
//
// Past the floor Acquire refuses immediately and does NOT evict first: there
// is nothing to re-ask, and evicting a live browser to make room a blind gate
// would not see costs a workspace its Chrome for no gain (FR-065). The refusal names memory,
// carries the memory_pressure reason code, and its log line says availability
// could not be determined rather than that the host is short -- they are
// different problems with different remedies. This is the ONE case where an
// unmeasurable host is treated as FULL rather than empty, deliberately: a gate
// that answers "room available" because it has nothing to read reports success
// while admitting without limit, which is the shape D1.5a's own text forbids.
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
//
// measurable is FALSE when the host cannot be measured at all -- Windows,
// which has no reader (FR-066), or Linux fallen through to its 4 GB fallback
// constant (meminfo_linux.go's fallbackTotalRAMBytes). It is a SEPARATE bit
// from admits, and conflating them is the defect D1.5b exists to prevent:
// when measurable is false, admits MUST be false (FR-065) -- an unmeasurable
// host is treated as FULL, not empty -- but the caller still needs to tell
// "the host is genuinely short of memory" from "this platform cannot say",
// because the two produce different log lines and only one of them is a
// capacity problem. availableBytes and pressureRatio are meaningless when
// measurable is false and MUST NOT be rendered as figures.
//
// On macOS availableBytes comes from the Darwin reader (FR-064) and is an
// APPROXIMATION of Linux's MemAvailable assembled from vm.page_* counters,
// not the same measurement. Expect the two platforms to disagree by some
// margin on comparable hardware; that is documented, not broken.
func (p *BrowserPool) Headroom() (availableBytes uint64, pressureRatio float64, admits, measurable bool)

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
// existing 1-minute idle sweep (gateway.go:5321-5355) AFTER its per-manager
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
Owns `key.go`, `resolve.go` (new); `TabOwner` and `sessionKey` (**FR-080** — `TabOwnerSession(transcriptSessionID)` / `TabOwnerWorkspace()`, D1.9c); **the counter deletion (FR-059)** — `tools.browser.max_tabs`, `tools.browser.max_total_tabs`, `TryOpenTab`/`ReleaseTab`/`reservedTabs`/`totalOpenTabsLocked`/`SetMaxTotalTabs`, `reserveGlobalTab`/`releaseGlobalTab`, their call sites and their 59 test-side references (§0.6); `ManagerResolver` and the `RegisterTools` signature change plus all 11 tool structs (FR-002a); the `browserMgrs` re-key and `BrowserManagerForKey`/`BrowserManagerForAgent` (`loop.go`); **`controlledResult`'s re-key (FR-002c — it is on the resolution path, not the lease path)**; all 37 `DefaultSessionID` consumers (FR-002b) and the constant's deletion; the reload-prune predicate and per-key idempotent registration (FR-026a, FR-026b); the `loop.go:270-279` comment's replacement (FR-002d).
**FR-080 is not separable from FR-001 and must land in the same commits.** A commit that re-keys `browserMgrs` without carrying the **session** dimension on the tab set ships a state D1.9c forbids — every session on a workspace sharing one tab set — and it ships it *silently*, because every map-level test still passes (§0.2a). **FR-081 lands with it**, for the reason §14's scope table gives: the widened lease trigger is the only arbiter for two turns on one session, and shipping the session key without it is shipping the shared set without the lock.
**FR-002c is not separable from FR-001 either — the third atomicity rule, added round-4 M-401.** `controlledResult` asks `IsControlled(defaultSessionID)` today (`pkg/tools/browser/tools.go:963`). **The moment `browserMgrs` and the live-view registry are re-keyed, that call asks about a key nobody takes, so it returns `false` forever.** The lock is still there, still populated, still shown in the panel — and never consulted. **Between the re-key commit and the FR-002c commit the human control lock is SILENTLY DEAD:** an agent can drive a tab a human is holding, every existing lease test stays green (none of them takes the lock), and nothing in the product says anything. That window is also the one in which the mitigation for implicit acquisition does not exist — D1.9b ruling 1's *"the lock is the whole mitigation"* (§0.7, FR-071) describes a control that, in that window, is not running. **FR-002c lands in the SAME commit as the re-key**, never the commit after, and the commit message names it. *This is the ADR-037 anti-pattern by another route — a security control that reports working and changes nothing — and it is the one this project has already shipped once (root `CLAUDE.md`'s default-agent release blocker).*

**FR-059 is not separable from FR-001 either, and for the mirror-image reason.** A commit that re-keys `browserMgrs` while `cfg.MaxTabs` is still enforced ships a 5-tab cap for a whole team; a commit that deletes the counters while the managers are still per-agent removes the only bound before the memory gate exists. **FR-059 lands with FR-001, and FR-060's gate lands in the same commit as FR-059's deletion** — the five `totalTabCountLocked` call sites are vacated and re-occupied in one change, never left empty across a commit boundary.
Depends on: nothing. Interface out: §3.1.

**Stream P — Browser-process pool [GATED on G-1 + G-2 + G-6; largest].**
Owns `pool.go`; the coordinator's decomposition into per-key instances; per-key profile dirs (**flat `ws-<id>`**), launch locks and ownership markers (FR-037, FR-037a, FR-042); the admission edge semantics (FR-038, FR-038a) and the **LRU eviction policy with its two guards** (FR-050, FR-051, FR-052); the **memory refusal** and its one named error (FR-053); thrash detection (FR-054); the **memory-pressure admission gate — the only admission control there is** — at launch *and* at tab open, its two Chromium unknowns, its no-silent-no-op requirement and the measured launch minimum (FR-057, FR-057a, **FR-060**, **FR-061**, **FR-062**); **the D1.5b platform work in `pkg/config` — the Darwin reader (FR-064), the two-valued exported accessor and the refuse-when-unmeasurable rule (FR-065), and the Windows placeholder with its release-note and config-doc lines (FR-066)** — which lives in `pkg/config` but belongs to this stream because it is the gate's own signal, and **must land before or with FR-057, never after**: a gate shipped ahead of its reader is the blind gate D1.5b exists to prevent; *(no `max_browsers`, no operator ceiling, no `--renderer-process-limit`, no per-renderer constant — FR-055 and FR-056 are tombstoned by D1.5a, §0.6)*; whole-Chrome idle close with its config key, caller and post-close state (FR-040, FR-040a); boot marker reconciliation via the **launch lock** (FR-042a) and per-key stale-singleton cleanup (FR-042b); per-Chrome crash containment, replacing `watchForCrash`'s reset-everything behaviour (FR-041); profile-based reload survival replacing ADR-043 CRIT-002's context re-adoption (FR-043), the profile's deletion path (FR-043a) and the **upgrade rule** — no workspace inherits the existing global profile (FR-043b); boot preprovision decoupled from `BrowserManagers()` (FR-016c); retirement of `capture_shared_context`, `disposeBrowserContextRaw`, `contextCount()` and the CDP-context branch of `Register` (FR-031).
**Also owns FR-034a — the final description literals** (§3.3), which must land in the same commit as FR-037 and not before.
Depends on: A's key type, **A's `TabOwner`** — a pool whose instances hold merged tab sets cannot be un-merged later without touching every call site again — **and A's FR-059/FR-060 pair**, because between the counters' deletion and the gate's arrival there is no bound at all. **Do not start before G-2 passes.**

**Stream C — Two-state tabs + descriptions (D1.12) [depends on A].**
Owns `ListTabsState` + `ListTabs` delegation (`manager.go:1605-1613`); `ListTabsTool.Execute` (`tabs.go:48-68`); the five model-visible strings, their **interim** replacement literals (FR-015, FR-034 — §3.3), and the two Go comments (`tabs.go:19,186`). Its payload must also say **whose** tabs it is reporting (FR-080): this session's set and the workspace's operator-owned set are distinguishable, because "whose tab is this" is the question ADR §1.1 records being answered wrong.
Does **not** own a "not permitted" state — **the ADR withdrew it** (D1.12) and §17 C3 tombstones the whole stack that served it. `browser_list_tabs` has **two** states.
**Does NOT own the final literals.** FR-034a's isolation-asserting text lands in Stream P's commit, not this one (§3.3, MAJ-107).

**Stream D — Write lease, rescoped [depends on A].** Owns `lease.go` per **§14**, the call pairs in every mutating tool, and composition with `controlledResult`. **§14 is normative; this stream implements it and the D2 spec references it.** **Under D1.9c the lease arbitrates two TURNS on one tab set** — the operator's workspace-owned set, **or a session's own** (FR-081). Two turns in *different* sessions cannot contend, so the primitive is unchanged. **What D must not inherit is D1.9a's `TabOwnerWorkspace()`-only trigger**, which was correct about agents and wrong about writers: `/loop`, async system-notify (#505) and cron `SessionModeMain` each put two turns on one session id (§0.2a, §14). D owns test 99 and **SC-028's mutation receipt** — a build restoring that trigger must be RED.

**Stream E — Gateway resolution + contracts [depends on A].**
Owns the three `BrowserManagerForAgent` call sites; server-side agent→workspace resolution preferring the attaching session's `workspace_id` (`pkg/session/unified_meta_files.go:60`); the capture registry's re-keying and the ADR-048 conflict rule's collapse (FR-016a); the boot warm path (FR-016b); the panel's failure messages (FR-008a); the three schema description edits, **one of which is a semantic reversal and must be reviewed as one** (FR-016, MAJ-004 in §15); and the Workspace → Team elevation-of-privilege disclosure (FR-047).
**FR-047 does not depend on Stream P** and should land in §0.4: it is true the moment Stream A lands, because adding an agent to a workspace already grants it that workspace's browser.
**Stream E no longer owns any `contracts/openapi.yaml` path.** Revision 3's `POST /api/v1/workspaces/{id}/browser/close` and its SPA control went with FR-046 (D1.7); the only `contracts/` diff D1 produces is `description:` text in **four** existing schemas — Stream E's three (one of which is a semantic reversal) plus **`PerformanceSettings.yaml`**, which belongs to **FR-069** and lands with the agent-side deliverable, not with Stream E (§0.6b). *(This said "three" until round-5 m-501.)*

**Stream F — Audit + lifecycle [depends on A, P].**
Owns the **per-action** write-class audit events (FR-027), their name constraint (FR-058) and their provenance assertion (FR-035); disposal on workspace deletion and roster change (FR-026); the reaper interactions (FR-025) and the pool's idle-close hook (FR-040); `#659`'s auto-deny requirement for delegated sub-turns (FR-032).
**FR-027's audit does not wait for Stream P** — the events are emitted from the tool path, not the pool, and per-action repudiation is exactly what D1.10's sharing ruling makes urgent.

**Stream G — Tests + regression (cross-cutting).** Owns §10. **Does not own the 364-reference test migration** — that is Stream A's, in Stream A's own commits, because it is a compile dependency of FR-002b rather than a test-quality task (§2.2a).

**Stream M — the one memory mechanism, both consumers (cross-cutting; owns files outside `pkg/tools/browser`).** Owns **FR-067** (`bytesPerAgent` and the computed default deleted; `EffectiveMaxParallelAgents` returns `(n, capped)`), **FR-068** and **FR-068a** (agent admission on the same live accessor and threshold as the pool; the unmeasurable host holds at the floor of 2), **FR-069** (the corrected announcement and the SPA's backstop text) and **FR-075** (the two `ok=false` responses, asserted as a pair). Files: `pkg/config/meminfo_*.go`, `pkg/config/config.go`, `pkg/config/parallel_clamp*.go`, the agent admission path, `pkg/gateway/rest_performance*.go`, and the Performance settings component. **Recorded here because the previous pass left FR-067…FR-069 with no stream at all** — they were ruled into this document by ADR D1.5c/D1.5d and signed off as scope (`ddd9789a4`) after §3.2 was last written, and an unassigned requirement is one nobody is holding.
Depends on: **FR-064/FR-065's two-valued accessor** (Stream P owns the Darwin reader itself). **Ships in the same deliverable as the browser gate** — the sign-off withdrew the "independently landable" split. **Its own regression surface is larger than the browser's** and is enumerated in §10.1's second out-of-package list.
**⚠️ M's gating, stated because M and P disagreed about it (round-4 M-405).** Stream P is **GATED on G-1 + G-2 + G-6 and lands last**; Stream M carried **no gate at all** — and the two edit the **same files**: `pkg/config/meminfo_linux.go` (P's FR-064 makes a Darwin sibling of it, M's FR-078 changes its signature), `pkg/config/config.go` (P's exported accessor, M's FR-079 `availableRAMBytes`), and the `pkg/config/meminfo_*.go` set generally. Two streams on one file with different landing conditions is a merge collision by construction, and the ungated one lands first. **The rule splits M rather than gating all of it.** **M's `pkg/config` half is NOT gated and lands WITH P's FR-064/FR-065** — one accessor, written once, in one commit; FR-078 and FR-079 are part of that accessor's *shape*, not consumers of it, and nothing in `pkg/config` needs a measurement to be correct. **M's consumer half (FR-067, FR-068, FR-068a, FR-069) is likewise ungated, but lands AFTER that accessor commit and BEFORE P's gate opens** — P's FR-057 reads the same accessor, and a gate shipped ahead of its reader is the blind gate D1.5b exists to prevent, which is the rule P's own entry already states. **G-1/G-2/G-6 gate the POOL, not the memory mechanism:** they measure browser cost and eviction behaviour, and nothing in FR-067…FR-069 or FR-075…FR-079 consumes any of them. *Gating M on P's measurements would hold a correctness fix to `pkg/config` — the fabricating Linux reader, FR-078 — behind a browser benchmark: the wrong dependency, in the wrong direction.*

**Where D1.9b's six new FRs land — no new stream needed for any of them.**

| FR | Stream | Why there |
|---|---|---|
| **FR-070** (implicit acquisition has no surface) | **D** | It is a statement about the tool registry and the `controlledResult`/lease composition D already owns. The requirement is an *absence*, so it costs D a structural test and no new file |
| **FR-071** (the control lock gates acquisition; the blocked case is asserted) | **D** | §14.2 rule 1's ordering is D's, and this is that ordering's mitigation reading. **It also depends on Stream A's FR-002c** — the lock must be asked about the resolved key or the assertion passes vacuously — so D cannot land it before A |
| **FR-072** (periodic cache trimming) | **P** | P already owns the per-key profile directories, the launch locks that decide eligibility, and idle close, which is trigger 1. Trimming from anywhere else would need a second liveness test |
| **FR-073** (the protected set survives) | **P** | Same files; its real-Chrome half shares Stream P's gating |
| **FR-074** (the continuously-driven residual is declared) | **P** | Config doc, release note and log line, alongside FR-066's Windows declaration in the same artefacts |
| **FR-075** (one predicate, two responses) | **M** | It is an assertion *about both consumers at once*, so it belongs with the mechanism rather than with either caller |
| **FR-076** (containerisation detected without the limit) | **M** | It is a property of the one memory mechanism's inputs, not of either consumer |
| **FR-077** (the node-memory WARN) | **M** | Same mechanism; its config-doc and release-note obligations ride with FR-066's and FR-074's in the same artefacts |
| **FR-078** (the reader stops fabricating) | **M** | `pkg/config/meminfo_linux.go` — the same file FR-064 makes a Darwin sibling of; **land it with FR-065/FR-068a or those two have no reachable `ok=false` branch on Linux** |
| **FR-079** (one dead signal does not discard the other) | **M** | `pkg/config/config.go`'s `availableRAMBytes` — **must land in the same commit as FR-078**, which is what makes the discard reachable |

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
9a. **Whose tabs.** The answer names ownership: **this session's** tabs — whichever agent the chat is currently on — and, separately labelled, the tabs the **operator** opened on this workspace, which every agent on it can see. A session never sees another session's tabs (D1.9c, FR-080).
10. ~~**Not permitted.**~~ **WITHDRAWN by ADR D1.12.** `FilterToolsByPolicy` (`pkg/tools/compositor.go:436-438`) `continue`s past a deny verdict, so a policy-denied agent is never shown `browser_list_tabs`, never calls it, and answers from absence. There is no artefact for a denial that never runs. The underlying defect — an agent that cannot tell "I may not" from "there is nothing" — is **real, unfixed, and in ADR §6** as the headline problem surviving in a narrower form; it is not solvable inside a tool. §17 C3.
11. **One writer, on the one tab that can contend.** Two agents acting on the **operator's shared tab** concurrently: neither observes the other's mid-action state; the loser retries within the tool and, only past the bound, receives a **non-error** `{"deferred": true, "reason": …}` naming the holder. Two turns in **different sessions**, each on its own session's tab set, never interact at all — different `sessionEntry` values, so the case genuinely cannot arise (FR-080). **Two turns in the SAME session CAN**, by three shipped paths (`/loop`, async system-notify #505, cron `SessionModeMain` — §0.2a), and they take the lease on that session's own set (**FR-081**). *This item read "two agents acting on their own tabs … cannot arise under D1.9a" until round-5 M-506: the contender is a second **turn**, not a second agent, and D1.9a is superseded.*
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
25. **The operator's tab is acted on directly, and never while the operator is at the wheel.** An agent's write to the workspace-owned tab simply takes effect — no acquisition step, no extra tool, no extra permission, **and no change of ownership: the tab is the workspace's before and after** — *unless a human currently holds the live-view control lock*, in which case the agent defers with ADR-038 D6's existing reason and acquires nothing (D1.9b ruling 1; FR-070, FR-071).
26. **A closed workspace's browser cache is trimmed; its logins are not.** When a workspace's Chrome closes — idle, evicted, or at shutdown — the profile's disposable caches are removed and everything a site or a login wrote is kept, so the workspace reopens signed in with a cold cache. Nothing is ever trimmed while a Chrome is live (D1.9b ruling 4; FR-072, FR-073).
27. **A host whose memory cannot be measured refuses to GROW the browser pool and holds agent concurrency at 2.** The same unmeasurable reading reaches both consumers and they answer differently on purpose: a refused browse costs one tool call, a refused turn is a gateway that cannot answer a message (FR-065, FR-068a, FR-075).
28. **A container with no memory limit is told so at startup, once, and starts anyway.** A pod without `resources.limits.memory` sizes itself against the node it happens to land on, which is the one failure in this design that ends with more work admitted rather than less. The gateway names the condition and the remedy and then carries on — a bare-metal host has no cgroup limit either and is perfectly correct, so this can never become a refusal. **On a host whose `/proc/meminfo` cannot be read at all, the answer is "unmeasurable", never a number** (FR-076, FR-077, FR-078, FR-079).

---

## 5. Explicit non-behaviors

- The system must **not** fall back to `DefaultSessionID`, `""`, the agent id, or any other constant when workspace resolution fails. There is no default browser.
- The system must **not** apply `FindForAgent`'s sorted-first tie-break to a *browsing* key. It selects live credentials; for a filesystem mount the worst case is the wrong directory, here it is acting as the wrong signed-in identity (FR-033).
- The system must **not** give a delegated sub-turn a separate browser, a separate key, or a signed-out jar. That was reversed by ruling; reintroducing it is a design change requiring a new ADR entry, not an optimisation.
- The system must **not** address a tab set by `BrowsingKey` alone. Every tab operation names a `TabOwner` (FR-080). A call site that resolves a browser and then reads "the" tab set has merged the workspace's tabs, and it does so without failing any map-level test — which is why this is a non-behaviour rather than a note (§0.2a).
- The system must **not** let a turn in one session see, list, switch to, drive or close another **session's** tab. Only the workspace-owned (operator-opened) set crosses sessions (D1.9c). *Conversely it must **not** hide a session's tab from another agent on that same session — that is the ruling, and hiding it is D1.9a's superseded behaviour.*
- The system must **not** mint a `TabOwner` from an **empty** transcript session id. `tools.ToolTranscriptSessionID(ctx)` returns `""` whenever the turn carries no transcript (`pkg/tools/base.go:199-203`), and that is an ordinary handled state the agent loop tests for by name in at least five places — `/goal` and its post-turn hook (`pkg/agent/goal_loop.go:74`, `:555`), `/loop` (`pkg/agent/loop_command.go:78`), the active-agent resolver (`pkg/agent/loop.go:7779`), the history repair (`:8804`) — with a sixth, `pkg/agent/tool_manifest.go:12-20`, documenting the fallback for *"when the transcript is disabled"*. **`TabOwnerSession("")` is therefore a NAMED FAILURE, not an owner**: it returns `ErrNoTabOwner` (§3.1), the browser tool reports it, and nothing opens. Minting an owner from it instead gives **every transcript-less turn on the workspace one shared tab set** — the silent merge §0.2a exists to prevent, arriving through the one input nobody validates. *`BrowsingKey` has `IsZero()`, a single-construction rule, its own §5 non-behaviour and `ErrNoBrowsingContext`; `TabOwner` had none of the four, which is the asymmetry round-5 M-501 caught.* **It must NOT fall back to `TabOwnerWorkspace()`** — that would let a turn with no transcript drive the operator's own tabs. Asserted as case (d) of test 97.
- The system must **not** key tab ownership on `routingSessionID`. It is inherited verbatim through a delegation subtree (`pkg/agent/subturn.go:1339`) and its own doc comment forbids reading it as an *"ownership predicate"* (`pkg/agent/turn.go:348-353`). The key is `transcriptSessionID` (FR-080).
- The system must **not** keep, reintroduce, or replace any tab or browser **counter**. `tools.browser.max_tabs`, `tools.browser.max_total_tabs` and the `TryOpenTab`/`ReleaseTab`/`reservedTabs` machinery are **deleted from the code**, and `max_browsers`/`operator_ceiling` are never built (D1.5a, FR-059). A refusal derived from a count — of tabs, of browsers, of renderers — is forbidden anywhere on the admission path; the only refusal is memory's.
- The system must **not** leave the five vacated `cfg.MaxTabs` enforcement sites empty. `createFirstTab` (`manager.go:1139`), `OpenTab` (`:2005`, `:2047`) and `adoptTarget` (`:2216`, `:2286`) are exactly where a runaway tab loop lives, and FR-060's memory gate takes the counter's place there **in the same commit** (FR-059).
- The system must **not** ship the memory gate or the idle reapers disabled, "best effort", or behind an off-by-default flag on any platform. They are the entire defence (FR-061); a gap in either is not a degraded limit, it is no limit. Where a platform provides no pressure signal, that must be **stated** the way D1.9 states the orphan-termination asymmetry — never papered over with a conservative constant, because there is no constant left to be conservative with.
- The system must **not** treat a host it cannot measure as a host with room. Where the availability accessor reports `ok=false` — Windows, or Linux fallen through to its 4 GB fallback constant — the pool **refuses to grow and logs why**; it does not admit, does not "best effort", and does not fall back to a constant (FR-065). *An unmeasurable host is treated as **full**, not empty. This inverts the usual fail-soft default on purpose: a gate that answers "room available" because it has nothing to read reports success at every call site while admitting without limit.*
- The system must **not** reintroduce a **per-unit memory cost** for anything the gate admits — not per renderer, not per tab, not per agent, under any name. `bytesPerAgent` is deleted and no successor is permitted: the gate observes live headroom at the moment of the decision, for both consumers (FR-062, FR-068). *Every such constant this project has shipped has been wrong by more than an order of magnitude in at least one direction, and each was defended by the measurement that produced it.*
- The system must **not** hold **two** admission mechanisms, or two thresholds that happen to agree. One exported accessor, one threshold, two call sites (FR-068). *Two numbers that match today are two mechanisms tomorrow — the split D1.5c ruled against, and the reason the browser side is no longer shippable on its own.*
- The system must **not** refuse an agent turn outright on a host it cannot measure. `ok=false` means **refuse to grow**, not refuse to run: concurrency holds at the floor of 2 and the gateway still answers (FR-068a). *The browser reading of this rule — a refused browse costs one tool call — does not transfer; taken literally on the agent path it stops the product on Windows and gVisor.*
- The system must **not** present the physical OS-thread backstop as a capacity, a recommendation, or a default the system chose. Where `capped=false`, the operator-facing surface says *automatic — bounded by available memory* (FR-069). *A displayed number that is not the constraint is a defect this project has shipped before; here it would arrive by changing nothing.*
- The system must **not** present the macOS availability figure as equivalent to Linux's `MemAvailable`, in code, comment, log line or documentation. It is an approximation assembled from `vm.page_*` counters, contending with memory compression and purgeable pages that Linux's kernel-computed figure does not expose, and the two platforms will differ by some margin on comparable hardware (FR-064). *The doc comment at the call site is what stops a future reader concluding one of the two is broken.*
- The system must **not** construct a `BrowsingKey` anywhere except `ResolveBrowsingKey`, and must **not** add a second key shape.
- The system must **not** let any tool struct hold a `*BrowserManager` captured at registration (FR-002a).
- The system must **not** leave any consumer of `DefaultSessionID` behind; the constant is **deleted, not deprecated** (FR-002b).
- The system must **not** call `chromedp.WithNewBrowserContext` or `target.CreateBrowserContext` on any path. CDP browser contexts are retired outright (FR-031); ADR-043 CRIT-003 is preserved by deletion rather than by discipline.
- **INVERTED BY RULING (D1.7).** Revision 3 read: *"the system must not evict a live workspace browser to satisfy a new request; refuse at the cap."* The requirement is now its opposite: **the system must not refuse a browse because the target is reached.** It evicts the least recently used evictable instance and launches (FR-050). It must **not** evict an instance with a live viewer or an in-flight tool call, and must **not** delete a profile on eviction — the latter is what makes eviction acceptable at all. *(Round-5 M-508: a third clause here — *"must not exceed the target by more than one in total"* — is DELETED. **There is no target.** D1.5a deleted every counter and §3.1's `Acquire` deletes the "+1 overshoot" by name; the prohibition named a quantity that no longer exists, and a test asserting it would pass vacuously against a pool with no gate at all.)*
- The system must **not** ship a "pool full" error surface, REST path, or UI control on the normal path (D1.7). The only capacity error is **FR-053's memory refusal**, reached when the live-memory gate refuses **and nothing is evictable** — every live instance simultaneously holding a viewer or an in-flight tool call. *(Round-5 M-508: this read *"FR-053's ceiling"*. FR-053 is not a ceiling and names no cap — D1.5a deleted them all, and FR-053's own text forbids the error advising an operator to raise one. The trigger is memory plus unevictability, and calling it a ceiling is what sends the reader looking for the setting that does not exist.)*
- The system must **not** destroy a browser on hot reload. Only workspace deletion, roster change, idle close, **eviction**, or gateway `Close()` (FR-026a, FR-040, FR-050).
- The system must **not** delete a workspace's **profile directory** when its Chrome is closed for idleness, for a roster change, for a reload, for a crash, or **by eviction** — the logins are the point of the profile, and eviction is only tolerable because they survive it (FR-040, FR-043, FR-050). **Workspace deletion is the sole exception and it MUST delete** (FR-043a): a departed client's cookies and tokens must not outlive the workspace that named them.
- The system must **not** interpolate a workspace id into a filesystem path without validating it as a single path segment. It happens to be safe today — ids are server-minted ULIDs (`rest_workspaces.go:495` for the default workspace, `:848` for created ones) — but the path depends on a property nothing records, so a future id-format change (an operator-chosen slug, an import, a migration) would silently turn `<profileRoot>/ws-<id>/` into a path-traversal surface — and the flat form makes this **more** important, not less, since `ws-` is a bare prefix rather than a directory boundary. The invariant is written down and enforced in `ResolveBrowsingKey`, before the key is constructed, with any refusal treated as `ErrNoBrowsingContext` (FR-037, round-2 MIN-106) — **and it is checked on the RENDERED SEGMENT, not on the bare id** (round-5 m-506):

  1. `id != ""`, `id != "."`, `id != ".."`, and `filepath.Base(id) == id`; and
  2. `seg := "ws-" + id` satisfies `filepath.Base(seg) == seg` and contains no `/`, no backslash and no NUL.

  **Why (2) is not redundant, and why the earlier one-line form was weaker than the threat it stated.** That form validated `id` alone, which is exactly right under revision 3's *nested* `ws/<id>` layout — there the id **is** the path segment. Under D1.8's **flat** layout the segment is a *concatenation*, so validating the id is no longer validating the thing that reaches the filesystem, and this bullet's own text already says the flat form makes the exposure **more** important rather than less. Checking the rendered segment closes the gap in one line and costs nothing.

  **One residual is recorded rather than fixed, because it is a different problem with a different remedy:** on a case-insensitive filesystem (macOS, and Windows) two ids differing only in case render to two names that resolve to **one** profile directory — a cross-workspace isolation break, which is the property this whole design exists to deliver, and no path-segment check can see it. It cannot arise from server-minted ULIDs (`rest_workspaces.go:495`, `:848`), which is the same premise the traversal bullet rests on; it is listed here so that a future id-format change is understood to reopen **two** questions, not one.
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
- The system must **not** give implicit acquisition of the operator's tab a **surface**. No `browser_take_control` tool, no policy entry, no wire field, no result key announcing a transfer. An agent's write simply takes effect, by executing a `controlledResult`-gated tool against `TabOwnerWorkspace()` and by nothing else, and `TabOwnerWorkspace()` still owns the tab afterwards (D1.9b ruling 1, FR-070). *(This bullet said "becomes the driver" until round-4 C-403; the term had no definition and, under FR-070, no possible representation — see §0.7.)*
- The system must **not** let an agent acquire the operator's tab **while a human holds the live-view control lock**, and must not reorder or bypass `controlledResult` on the workspace-owned owner "because the lease already arbitrates". That gate is the entire mitigation for implicit acquisition; removing it turns acting-on-a-tab into a silent takeover, and it fails no lease test on the way out (FR-071, §14.2 rule 1).
- The system must **not** impose a hard per-workspace **profile size cap**, under any name or value. **Rejected by ruling, structurally rather than by tuning:** at the moment such a cap binds, something must be discarded mid-session, and the only large items in the directory are the cache **and the logins** — discarding logins is the one outcome this design exists to prevent, and raising the number only postpones the moment (D1.9b ruling 4, §0.8, §12 A28). *Recorded here because "just cap the profile at N GB" is the first thing a reader proposes.*
- The system must **not** trim a profile whose Chrome is **live**, and must **not** implement the trim as a deny-list. Eligibility is the FR-042a discriminator — no live instance for the key **and** the per-key launch lock acquirable (`pkg/tools/browser/coordinator.go:1442-1483`). A deny-list widens itself with every Chromium upgrade, and the first place it widens into is wherever credentials moved to (FR-072, FR-073).
- The system must **not** delete a workspace's profile **directory** as part of the trim. The trim removes named cache subdirectories; the directory, the cookies, the web storage and the origin-owned quota storage stay. Directory deletion has exactly one trigger and it is unchanged: workspace deletion (FR-043a).
- The system must **not** collapse the two `ok=false` responses into one. **One accessor and one predicate (FR-068), two consumers, two different correct answers**: the browser pool refuses to **grow**; agent admission holds at the conservative floor of **2**. Refusing every agent turn breaks a supported Linux deployment (gVisor and other `/proc`-less sandboxes reach `ok=false` through `meminfo_linux.go`, not through the non-Linux stub), and admitting an unmeasured browser pool is the fail-open regression `pkg/config/meminfo_other.go:20-23` records having already shipped once — 585 concurrent agents on any box (FR-075, §0.9).
- The system must **not** key the node-memory warning on the memory limit alone. "No cgroup limit" is **correct** on bare metal and **dangerous** in a pod, and the limit cannot tell them apart — so containerisation is detected independently (FR-076) or the warning is worthless in one of two ways: it fires on every bare-metal start, or it never fires in the case it exists for (FR-077, ADR D1.5e).
- The system must **not** answer a memory question with an invented constant. An unreadable `/proc/meminfo` reports **undeterminable**; it does not report 4 GiB, half of 4 GiB, or any other figure the machine did not supply. *`pkg/config/meminfo_other.go:15-33` records what the same pattern already cost once — 585 concurrent agents on any box — and fixed it for macOS only* (FR-078). **And it must not throw a good signal away in the process:** an undeterminable `/proc/meminfo` alongside a finite cgroup limit is a **measurable** host (FR-079).

---

## 6. Integration boundaries

- **Chrome processes / CDP.** The count of live Chromes now scales with **workspaces being actively browsed**, bounded by **live memory and nothing else** (FR-057; D1.5a deleted every counter). Each is launched over the pipe transport (`exec_resolver.go`, `cdppipe`) with its own `--user-data-dir`. A launch failure surfaces as a tool error naming the workspace — never a silent join to another workspace's browser. **CDP browser contexts are no longer created at all** (FR-031).
- **Sandbox (Landlock/seccomp).** No new network surface: CDP flows over inherited fds 3/4, and the fixed DevTools port allow-rule was already removed (`pkg/gateway/sandbox_apply.go:412-417`). What *is* new is **N profile directories**, so the filesystem allow-list must cover `<profileRoot>/` as a subtree — the per-key dirs are `ws-<id>` siblings of `cfg.ProfileDir` under it, not a `ws/` sub-tree (`profileRoot = filepath.Dir(cfg.ProfileDir)`; §3.1). Verify against `sandbox_apply.go`'s path rules before the pool lands — this is the one sandbox interaction that is genuinely new.
- **Host memory, and why there is no planning unit at all any more.** The binding cost, and now the *only* control. **`PER_BROWSER_COST` is measured at ≈182 MB** (Chrome for Testing, macOS, idle, non-capturing — carry the scope, §0.6) and is the launch-headroom minimum (FR-062). Everything else is read live: page type varies more than 20× (an idle article ≈15 MB PSS, a mail client 120–180 MB, video at 1080p 222–341 MB), the measured renderer spread was **30 MB → 327 MB in one snapshot**, and a tab is not a renderer (2 tabs, 13 renderer processes). **So no per-renderer or per-tab constant is used, and no count is derived from memory** — FR-057 gates on the live figure and FR-060 puts the same gate in the tab-open path. ADR-043's "≈4–5 GB at ten" is unmeasured and per-agent; neither it nor the retracted 1118 MB RSS reading (ADR §8) may be quoted as a figure.
- **Chrome computes its own renderer limit, and that limit does not compose.** Chromium's `render_process_host_impl.cc` budgets `85 MB` per renderer against half of physical RAM, clamped to at least 3. On the measured 3916 MB box that is `3916 / 2 / 85 = 23` renderers **per Chrome** — four workspaces would each independently permit 23, i.e. ~92 renderers, every one sanctioned by Chrome. **The pool must therefore impose its own bound, and under D1.5a that bound is the memory gate — not a counter of our own** (FR-057, FR-060). This is the single largest consequence of going from one Chrome to N and nothing in ADR-043 anticipates it. Whether Chromium even reads a cgroup limit when computing that budget is **unverified in either direction** — gate G-3, and a negative answer makes our gate the only real one, which is exactly what D1.5a already assumes.
- **`pkg/config`'s memory readers are the reuse, they are unexported, and D1.5b changes both their shape and their platform coverage.** `autoDetectMaxParallel` already sizes a concurrency cap as `availableRAMBytes() / bytesPerAgent`, clamped, and `availableRAMBytes` (`config.go:655-661`) already takes `min(/proc/meminfo MemAvailable, cgroup limit − usage)` over `readMemAvailableBytes` / `readCgroupMemoryAvailableBytes` (`pkg/config/meminfo_linux.go`, with a `meminfo_other.go` non-Linux stub and existing fixture tests). **All of it is unexported, so `pkg/tools/browser` cannot call it as written** — FR-057 requires `pkg/config` to export a live memory-availability accessor rather than assuming an unexported symbol is reachable. **Three D1.5b changes land on the same surface and must land together:** (1) the exported accessor is **two-valued**, `(bytes uint64, ok bool)`, because a bare `uint64` cannot distinguish "unknown" from "zero free" and `0` is today's unknown sentinel (FR-065); (2) a new **`meminfo_darwin.go`** implements both readers for macOS, narrowing `meminfo_other.go` (and its test file) to `!linux && !darwin` (FR-064); (3) a **`meminfo_windows.go`** placeholder returns the unmeasurable signal explicitly and names its fix route (FR-066). **A fourth change lands on the same surface and it is NOT browser work: `autoDetectMaxParallel` is deleted** (FR-067, §0.6b). D1.5b's revision of this paragraph said its answer was *"deliberately unchanged — sizing a default conservatively and refusing a browser launch are different jobs"*; **D1.5c refused that separation and D1.5d deleted the function**, so the boundary this paragraph drew no longer exists. **The three D1.5b changes above still land together as browser work; FR-067 lands separately, behind E-8's ratification** — see §0.6b's stream split, whose whole point is that the browser's three do not wait on the fourth. *(Revision 4 hung the export on FR-056's formula; the formula is gone, the export requirement is not: the gate reads the same numbers, on more platforms, with one more bit of information.)*
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
- **Host DISK, which was not a boundary in any previous revision and is one now (D1.9b ruling 4).** Per-key profile directories under `<profileRoot>/ws-<id>/` accumulate browser cache for as long as their instance is live, and **survive idle close and eviction by design** (FR-043a) — that survival is the whole point, and it is also what makes disk a boundary. The bound is FR-072's trim. **The one existing surface it touches is the reaper goroutine's sweep** (`pkg/gateway/gateway.go:5321-5355`, the goroutine at `:5321` whose `const reapInterval = time.Minute` is at `:5322` and whose per-tick `recover` is deliberate): the trim's **scheduled** pass is a *different, slower* cadence (`tools.browser.cache_trim_interval`, default 1h) and must not be folded into the 1-minute sweep, because a trim pass walks directories while the reap sweep is a map scan. **The trim's primary trigger is not a ticker at all** — it is `pool.Close(k)` returning, which needs no interval and fires within milliseconds. *Relevant beyond this feature: this project's notes record the host root volume filling twice, and a full root volume presents as an unrelated failure everywhere else in the product.*
- **Filesystem eligibility reuses the launch lock, and adds no second liveness notion.** A key is trimmable iff the pool holds no live instance for it **and** its per-key launch lock is acquirable (`takeLaunchLock`, `pkg/tools/browser/coordinator.go:1442-1483`) — the same two-part discriminator FR-042a uses at boot to tell an orphan from a live neighbour. **A separate "is it running?" check here would be a second answer to a question the pool already answers**, and the two would disagree first in exactly the case that matters: a second gateway on the same `$OMNIPUS_HOME` (§12 A11).
- **The live-view control lock is now load-bearing for a second reason.** `LiveViewRegistry.IsControlled` (`pkg/tools/browser/live.go:1313`) and `TakeControl` (`:1241`) were an ADR-038 D6 courtesy boundary — a human at the wheel outranks an agent. Under D1.9b ruling 1 the same lock is **the sole barrier to an agent walking onto a tab a human is using**, because acquisition is implicit. **Nothing in the registry changes**; what changes is that a regression there is a security-shaped defect rather than an ergonomic one, which is why FR-071 carries a mutation receipt (SC-025) and why FR-002c's re-key is a prerequisite rather than a tidy-up.

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
- **AC5 (re-keyed by D1.9c): the payload says whose tabs.** The result carries **this session's** tab set and, separately labelled, the workspace-owned set the **operator** opened. A session's set never contains another session's tab. *(Under D1.9a this read "the calling agent's own tab set … never another agent's tab". The AC survives the re-key with its number because its subject — the payload names an owner — is unchanged; only the owner is.)* (FR-080.)

**US-22 (P0) Tabs stay with the chat; the operator's tab is everyone's.** As an operator, a tab opened in one of my chats stays in that chat whichever agent I am talking to, no other chat sees it, and only the tab *I* opened is one my whole team can see and be asked to take over.
- *Why P0:* ADR **D1.9c**, 2026-09-02, superseding D1.9a — and it is the ruling that actually fixes §1.1, **twice**: the operator's tab is workspace-owned *and* the agent's tab follows the chat, so switching from Mia to Jim changes neither answer. It is also the requirement most easily deleted by accident: FR-001's manager collapse merges every session's tabs on the workspace unless FR-080 carries the session key explicitly (§0.2a).
- ~~**AC1**~~ ~~**AC2**~~ ~~**AC3**~~ **RE-KEYED by D1.9c and replaced by AC6/AC7/AC8 below — tombstoned rather than edited, because AC1 and AC3 are now FALSE as written.** They asserted that agents A and B on one workspace hold **separate** tab sets. Under D1.9c two agents in the **same session** share one set (that is the ruling), and two agents in **different** sessions still hold separate ones (that is AC8) — so the old wording is true or false depending on a variable it never named. *AC2's content survives verbatim as AC7.* Their scenario and `TestTabs_TwoAgentsDoNotMerge` are tombstoned with them (§9, FR-048).
- **AC6 (the chat keeps its tabs across an agent switch — NEW, D1.9c): Given** session S on workspace W in which **Mia** opened `https://example.com/a`, **When** the operator switches the chat to **Jim** — the same session id — and Jim calls `browser_list_tabs`, **Then** Jim's result contains that tab, in **this session's** set, and Jim can switch to, drive and close it. *This is ADR §1.1's own conversation. Under D1.9a Jim would correctly have reported not seeing it, which is the answer the operator filed the defect about.*
- **AC7 (the operator's tab is the workspace's — carried forward verbatim from AC2): Given** agents A and B on workspace W, **When** the **operator** opens a tab through the live panel, **Then** both A and B see it, labelled as the workspace's, **whatever session either is running in.**
- **AC8 (the regression guard, re-keyed — NEW, D1.9c): Given** one `BrowserManager` per workspace and **two different sessions** S1 and S2 on it, **When** a turn in each opens a tab, **Then** the manager holds **two distinct** `sessionEntry` values and neither session's `tabs` slice contains the other's `tabEntry`; and a turn in S2 can neither list, switch to, drive nor close S1's tab. *A test that asserts only "both turns resolved the same manager" passes with the tab sets merged — which is the state this AC exists to fail. It must key on `transcriptSessionID`: a test written against `routingSessionID` passes with a whole delegation subtree merged into one set (§0.2a).*
- ~~**AC4 (`MaxTabs`)**~~ **WITHDRAWN with FR-049 (ADR D1.5a).** It required a five-tab cap to stay per **agent** rather than silently becoming five for the team. `tools.browser.max_tabs` is deleted from the code, so there is no cap to keep per-agent and nothing to assert. **The story is not weakened:** AC1 and AC3 carry the whole of US-22, and they are about *ownership* — whose tab set a tab lands in — which is untouched. Capacity moved to US-15/AC13, where a runaway tab loop is stopped by memory rather than by a number.
- **AC5 (acquisition is implicit — NEW, D1.9b ruling 1): Given** the operator has opened a tab on workspace W and **no human holds the live-view control lock**, **When** agent A executes a `controlledResult`-gated tool (say `browser_navigate`) against `TabOwnerWorkspace()` through the **real registered tool path**, **Then** the call **proceeds and its effect is visible on that tab**, the tab's owner is **still `TabOwnerWorkspace()`** afterwards, and there was **no acquisition call, no additional policy check and no result field reporting a transfer**. *There is nothing for an agent to ask for; the ask IS the act (FR-070). All three assertions can fail; the phrase this AC used to carry — "A is the workspace tab's driver" — could not, because FR-070 forbids every representation of a driver (round-4 C-403, §0.7).*

**US-8 (P1) A denied agent cannot reach the tool at all — and that is as far as this spec goes.**
- *Status:* **AC2 and AC3 are WITHDRAWN by ADR D1.12** (§17 C3). AC1 survives, restated, because it is true and testable; the story's original goal is not reachable from inside a tool.
- **AC1: Given** an agent whose policy denies `browser_list_tabs`, **When** the turn's tool definitions are built, **Then** the tool is **absent from them** — `FilterToolsByPolicy` `continue`s past a deny verdict (`pkg/tools/compositor.go:436-438`) — so no call is made, `ListTabsTool.Execute` is never entered, and no tab payload exists.
- ~~**AC2**~~ **WITHDRAWN.** It required `tool_denial.go`'s `ModelMessage` to name the browser surface. That message has **no production caller** for a tool the model was never shown, so the assertion would test a string nothing emits.
- ~~**AC3**~~ **WITHDRAWN with AC2.** Holdout 4 is rewritten (§13) to record the honest outcome rather than the desired one.
- **The unfixed consequence, stated rather than closed.** Mia is the default agent and the agent in §1.1's own repro. Asked what is open, she still answers from absence — same output, different cause. Fixing it means telling an agent, **outside** the tool-result path, that its workspace has a browser it is not permitted to drive: a system-prompt or manifest-note surface. The operator has confirmed Mia's and Ava's deny stays, so widening policy is not the answer. **ADR §6 owns this as its own headline defect surviving in a narrower form**, and this spec does not claim it.

**US-9 (P0) Two writers, one tab set.** Concurrent browser work by two turns on the same tab set — the operator's, or one session's own — neither corrupts a page nor errors. *(Rescoped by D1.9a, then re-keyed by D1.9c: contention is impossible **across** sessions and possible **within** one — AC0, AC7.)*
- ~~**AC0**~~ **REPLACED by AC7 — and it is worth reading why, because AC0 was green while the hole it guarded was open.** It read: *"Given agents A and B on workspace W each driving their own tab, When both issue `browser_navigate` concurrently, Then both complete, neither defers, and no lease is acquired by either."* **The premise was a statement about agents used as a statement about writers.** Two *agents* never share a tab set — true under D1.9a and under D1.9c — but two **turns** can, and under D1.9a one agent's heartbeat beside its own chat turn shared one per-agent set with no arbiter (round-4 **C-402**). Because AC0 asserted the premise rather than the property, it passed. Its scenario (*two agents on their OWN tabs never contend*) is tombstoned with it (§8).
- **AC7 (the general case, re-derived — NEW, D1.9c, FR-081): Given** two turns running **concurrently on one `transcriptSessionID`** — reachable today by `/loop`, by an async system-notify completing into a live chat (`pkg/agent/loop.go:3491-3510`, filed as **#505**), or by two cron `SessionModeMain` jobs — **When** both issue `browser_navigate` against that session's tab set, **Then** exactly one navigation is observed by Chrome at any instant, **both eventually complete**, and neither returns `IsError=true`; **and Given** instead two turns in **different** sessions, **Then** both complete, neither defers, and **neither blocks the other** — no lease taken on one owner is ever waited on by the other. *Both halves are required. The first alone is satisfiable by a lease that serialises everything, including calls that never needed to be serialised; the second alone is the assertion AC0 already made and is green while the first is broken.*
- **AC1:** two agents issuing `browser_navigate` **against the workspace-owned tab** concurrently — neither observes the other's mid-navigation state, neither returns `IsError=true`, **both eventually complete**, and at most one reports a deferral. *Asserting only "neither errors" would pass when nothing happened, which is why "both eventually complete" is the assertion (ADR criterion 16).*
- **AC2:** a human holding the live-view control lock outranks the lease; the deferral reason is ADR-038 D6's text and the lease was never acquired.
- **AC3:** an action tool that panics or is cancelled while holding the lease does not prevent the next acquire within `leaseWaitTimeout`.
- **AC4:** exempt tools are never deferred. The exempt set is **six** (§14 rule 3 is the normative count): four read-only tools shipped today — `browser_screenshot`, `browser_get_text`, `browser_wait`, **`browser_list_tabs`** — plus `browser_snapshot` (read-only, D2 FR-018) and `browser_handle_dialog` (**recovery** — D2 FR-035).
- **AC4a (the omission that made the headline demo defer):** `browser_list_tabs` is exempt. It is registered (`register.go:76`), it is read-only, and its own file says so (`tabs.go:20`: *"Read-only — NOT gated by controlledResult"*). The previous draft's closed set of five put it in neither category, so under AC5 it would have taken the **write** lease — making Jim's `browser_list_tabs`, the literal call in behavioural contract 1, US-1/AC1 and the headline BDD scenario, return `{"deferred": true}` whenever another agent held the lease for a long `browser_navigate`. **The feature's headline demo would have deferred behind an unrelated agent.**
- **AC5:** a `browser_*` tool takes the write lease **if and only if** it is gated by the ADR-038 D6 human-control lock (`controlledResult`). That biconditional — not a hand-written list — is the rule, and it holds exactly over the eleven tools shipped today (§14 rule 3's table). A tool that is leased but not control-gated, or control-gated but not leased, fails the gate (FR-019a).
- **AC6 (the mitigation for implicit acquisition, and it is asserted in the BLOCKED direction — NEW, D1.9b ruling 1): Given** a human holds the live-view control lock **on the resolved key** `ws:W`, **When** agent A acts on `TabOwnerWorkspace()`, **Then** A receives the **ADR-038 D6** deferral, **`acquireWrite` is never called**, and **the page is unchanged**; **and Given** the lock is held *and* another agent holds the write lease, **Then** the reason text is D6's and not the lease's, proving the gates ran in the specified order. *The allowed direction (AC5) is green on a build with no lock at all, with `IsControlled` hard-wired to `false`, and with `controlledResult` deleted — so **this** AC, not AC5, is what carries the mitigation (FR-071, and it depends on FR-002c: a `controlledResult` still asking `IsControlled(defaultSessionID)` returns `false` forever and makes AC6 pass vacuously, which is why the key is asserted too).*

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
- **AC6:** the reaper and the pool do not fight. `ReapIdleSessions` cancels a session's `browserCancel` in its own removal branches (`manager.go:3027-3032`, `:3073-3078`, executed at `:3123-3125`) and cancels the per-tab contexts (`:3106-3107`), so a sweep can leave a manager whose browsing context is cancelled while the pool still lists that key as live. *(**Corrected, round-4 M-404:** this AC also required `coord.ReleaseTab` (`:3118`) to have been called. **FR-059 deletes that call and test 78 asserts the symbol is absent repo-wide**, so an AC requiring it cannot hold in the shipped tree. The contract does not depend on it — the cancellation alone produces the state this AC is about.)* **Given** that state, **Then** the next `Acquire` for the key produces a working browser rather than a live-but-undrivable one, and `LiveKeys()` never counts a Chrome nothing can drive (FR-040a).

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
- **AC11a (the platform gap — RE-DERIVED; it was "recorded, not decided" and D1.5b decides it): Given** macOS, **Then** the gate has a **real signal**: `readMemAvailableBytes` and `readMemTotalBytes` are implemented for Darwin (FR-064) and `availableRAMBytes` returns a live figure, not `0`. **Given** Windows, **Then** the signal is absent and the gate **refuses to grow** rather than admitting (FR-065, FR-066) — the platform is `degraded-unsupported` for the pool and says so in the release notes and the config documentation. *Revision 5 wrote this AC as an open escalation because a spec may not choose its own scope; the operator has now chosen (§0.6a). **What may NOT be written here, in any revision:** "the gate is a no-op off Linux, accepted" — a gate that cannot measure and answers "room available" is not a weaker limit, it is a false green, and it was only ever tolerable while a counter stood behind it.* (FR-064, FR-065, FR-066.)
- **AC12 (the counters are gone from the CODE, not merely unset): Given** the shipped binary, **Then** `MaxTabs`, `MaxTotalTabs`, `TryOpenTab`, `ReleaseTab`, `reservedTabs`, `reserveGlobalTab`, `releaseGlobalTab` and `maxTabsReachedErr` do not exist as symbols, a `config.json` carrying `tools.browser.max_tabs` or `max_total_tabs` is **rejected at load with a named error** rather than silently ignored, and no `_test.go` still asserts a cap. *A key that loads and does nothing is worse than a deleted one: an operator who sets it believes they have a limit they do not have.* (FR-059.)
- **AC13 (a runaway tab loop is stopped by memory, at the tab): Given** an agent looping `browser_open_tab` inside an already-running browser, **When** the host's headroom falls below the gate's threshold, **Then** the **next tab open is refused** — the check runs at all five sites the deleted cap was checked at (`manager.go:1139, 2005, 2047, 2216, 2286`) — and the agent receives FR-063's memory reason code. *A launch-only gate never sees this loop: it opens no browser. And no counter remains to catch it.* (FR-060.)
- **AC14 (neither remaining control may silently do nothing): Given** idle close and the gate are the entire defence, **Then** each carries a test that **fails if the control is a no-op** — a reaper that never closes anything, a gate that always answers "room available" — and neither ships disabled, "best effort", or behind an off-by-default flag on a supported platform. *Previously a counter caught a runaway before memory did; that backstop is gone **by decision**, so a gap in either control is not a weaker limit but no limit.* (FR-061.)
- **AC15 (a memory refusal is legible to the agent): Given** a tab that could not be opened because the host is out of memory, **When** the agent reads the tool result, **Then** the message names **memory** as the cause and a remedy that exists (*close tabs or browsers you are done with, or wait*), and names **no limit and no config key**. *Today the deleted cap produced an actionable sentence; dropping it without a replacement reason code lands every memory refusal in the `default:` arm — "it could not be adopted" — with no reason and no remedy, and an agent that cannot tell "out of memory" from "something went wrong" **retries**, straight back into the loop this ruling accepts the risk of.* (FR-063.)
- **AC16 (macOS can measure, and the figure is tied to the real machine — NEW): Given** a real Darwin host, **When** `readMemTotalBytes` is called, **Then** it returns exactly `hw.memsize`; **and when** `readMemAvailableBytes` is called, **Then** the result is **strictly greater than 0 and strictly less than total** — never a constant, never `total`, never `0`. *Why phrased this way: "returns non-zero" would pass against a stub returning `1`. Tying total to `hw.memsize` gives an oracle the implementation cannot satisfy by accident, and bounding available strictly inside `(0, total)` rejects both the fabricated-constant shape (MAJOR-2) and the "everything is available" shape.* **And** the reader's doc comment names every sysctl it sums and states the compression and purgeable caveats (FR-064).
- **AC17 (an unmeasurable host refuses to GROW, and says why — boundary corrected in place, round-4 C-401): Given** a host where availability cannot be determined — the accessor returns `ok=false`, whether because the platform has no reader (Windows) or because Linux fell through to its 4 GB fallback constant (`meminfo_linux.go:16`, e.g. an unreadable `/proc/meminfo` under gVisor) — **When** the **first** browser launch and the **first** tab in it are requested, **Then** both **succeed**; **and When** a **second** browser launch or a **second** tab is requested, **Then** it is **refused**, the refusal names memory (FR-053, FR-063), and the reason *"memory availability cannot be determined on this platform"* is logged **once**, not per call. **The pool never treats an unmeasurable host as empty — and never as unusable either.** *Amended in place rather than renumbered because the subject is unchanged: this AC has always been "the unmeasurable host refuses". What it never stated was the boundary, and stating it one way here and the other way in §13 holdout 24 is what round-4 C-401 caught. The floor is FR-082's. The "both succeed" half is not decoration — an implementation that refuses everything satisfies the refusal half perfectly, and would remove browsing from gVisor, a supported Linux deployment (§0.9).* *A gate that answers "room available" because it has nothing to read reports success while admitting without limit — the exact false-green shape this project documents, and the one thing D1.5a's own text forbids.* (FR-065.)
- **AC18 (Windows is declared, not discovered — NEW): Given** the shipped release, **Then** (a) `pkg/config/meminfo_windows.go` exists, returns the unmeasurable signal explicitly, and its doc comment names the fix route — `GlobalMemoryStatusEx` via `NewLazySystemDLL` (`golang.org/x/sys/windows/dll_windows.go:234,249`), since x/sys v0.47.0 wraps neither that call nor `MEMORYSTATUSEX`; (b) the release notes state that the browser pool has **no memory-derived limit on Windows**; and (c) the browser section of the config documentation says the same. **Windows browser support is `degraded-unsupported` for the pool until the reader exists.** *An operator must be able to learn this from the release notes, not from an OOM.* (FR-066.)
- **AC19 (there is no computed default left, and the accessor says so — NEW): Given** a `PerformanceConfig` with no explicit `MaxParallelAgents` and no `OMNIPUS_MAX_PARALLEL_AGENTS`, **When** `EffectiveMaxParallelAgents()` is called, **Then** it returns `(physicalConcurrencySafetyCeiling, false)` — the `false` is the assertion that matters, because it is what tells every caller the integer is a **backstop and not a capacity**. **And** `bytesPerAgent`, `autoDetectMaxParallel` and `clampParallel` do not exist as symbols anywhere in the repo, `_test.go` included. **And given** an explicit value (config **or** env), **Then** the result is `(clampParallelExplicit(v), true)` — unchanged from today in both value and precedence. *Why the second half is stated: a change that deleted the auto path and quietly also changed the explicit path would pass any test written only about the auto path.* (FR-067.)
- **AC20 (agent admission asks the same live question the browser asks — NEW): Given** a measurable host and the same fixture readings the browser gate is tested against, **When** an agent admission is requested at the 0.84 / 0.85 / 0.86 pressure ratios, **Then** the answers are the **same three answers** the browser gate gives at those ratios, produced by the **same** exported accessor and the **same** threshold — **and** no per-unit agent cost appears anywhere in the decision. *Fails if:* a second threshold constant exists, a per-agent byte figure is reintroduced under any name, or the two consumers disagree on the same reading. (FR-068.)
- **AC21 (an unmeasurable host holds at the floor and refuses to grow — NEW): Given** the accessor forced to `ok=false` (Windows, or Linux fallen through to its 4 GB fallback), **When** concurrent agent admissions are requested, **Then** the first **two** are admitted and the **third is REFUSED**, the refusal names **memory**, and the reason is logged **once** rather than per call. **Both halves are required and the refusal is the load-bearing one:** *"admits when memory is free"* passes against a stub that always admits, so the test asserts the third admission **fails**, and the run fails if it succeeds. **And** the gateway is still able to serve a turn on such a host — *refuse to grow, not refuse to run*. (FR-068a.)
- **AC22 (the announcement is the true one, and the UI does not recommend a backstop — NEW): Given** the shipped release, **Then** (a) the release note says **there is no longer a computed default** for `performance.max_parallel_agents` and does **not** say any default "moved" or "changed from 2 to 2000"; (b) the config documentation for that key says the same; and (c) with nothing set, the Settings → Performance panel presents the value as **"automatic — bounded by available memory"** and **not** as the integer `2000` under the words *"Live system recommendation"* (`src/components/settings/PerformanceSection.tsx:218-229`). *Fails if:* any of the three is absent, **or** the SPA renders `2000` as a recommendation — which is the "displayed number is not the constraint" defect this project keeps catching, and it would be shipped by doing nothing. (FR-069.)
- **AC23 (one predicate, two responses — NEW, operator ruling 2026-09-01): Given** a single stubbed availability accessor returning `ok=false`, **When** the browser pool is asked to launch a **second** instance **and**, in the same run, three concurrent agent turns are requested, **Then** the pool **refuses to grow** (naming memory) **and** agent admission **admits two turns and refuses the third** (naming memory) — the two answers differ from one reading, on purpose. *Asserting either half alone is green on a build that has collapsed them: "the pool refused" passes when everything refuses, and "two turns ran" passes when nothing is gated. **Windows and gVisor are the same on this AC and different in status**: Windows' browser refusal is accepted because Windows is not a supported platform (operator ruling); gVisor is a supported **Linux** host reaching the same predicate through `meminfo_linux.go`'s fallback, which is why the agent half may not be turned into a refusal (FR-075, §0.9).*
- **AC24 (containerisation is known without asking about the limit — NEW, ADR D1.5e): Given** three fixture hosts — (a) a Kubernetes pod (`KUBERNETES_SERVICE_HOST` set, `/proc/self/cgroup` showing a `kubepods` path, **no** `/.dockerenv`), (b) a Docker container (`/.dockerenv` present), (c) a bare-metal host (none of those, `/proc/self/cgroup` showing a `system.slice` path) — **When** the containerisation predicate is evaluated, **Then** it answers **true, true, false**, and it does so **without reading `memory.max`, `memory.limit_in_bytes`, or `/proc/meminfo`**. *Case (a) is the one that matters and the one the obvious implementation fails: it has no `/.dockerenv`, so a predicate built on `isRunningInDocker` (`pkg/gateway/sandbox_apply.go:185-201`) answers **false** there and the FR-077 warning is never emitted in the deployment it exists for. Case (c) is what stops the predicate from being `return true`. The "without reading the limit" half is asserted at the seam — the limit readers are stubbed to panic — because a predicate that consults the limit has re-merged the two questions D1.5e separated.* (FR-076.)
- **AC25 (the node-memory warning fires in the pod and NOWHERE else — NEW, ADR D1.5e): Given** the same three fixture hosts, each crossed with limit-present and limit-absent, **When** the gateway starts, **Then** a WARN naming *no container memory limit set*, *sizing against node memory* and the remedy `resources.limits.memory` is emitted **exactly once** for **containerised + no limit**, and **no such line is emitted** for **containerised + limit present** or for **bare metal + no limit**. **And** startup **succeeds in all four cases** — this is a warning, never a refusal. *Both silent cases are load-bearing and neither may be dropped. A test asserting only "it warns in a pod without limits" passes on a build that warns unconditionally, which is the shape D1.5e names as worthless: bare-metal-without-a-limit is the correct, ordinary case, and a line that appears on every start is filtered before the day it means something. The "exactly once" clause is what fails a per-request implementation.* (FR-077.)
- **AC26 (an unreadable `/proc/meminfo` says UNMEASURABLE, and does not invent 2 GiB — NEW; not in D1.5e): Given** a Linux host whose `/proc/meminfo` cannot be opened, **When** the memory reader is called, **Then** the result is **undeterminable** — it is **not** `fallbackTotalRAMBytes` (4 GiB), **not** half of it (2 GiB), and not any other constant — **and** `fallbackTotalRAMBytes` does not exist as a symbol anywhere in the repo, `_test.go` included. **And given** a host whose `/proc/meminfo` is readable with a real `MemTotal` but **no** `MemAvailable` line (a pre-3.14 kernel), **Then** the result is **half of the real `MemTotal`** and is **determinable** — that heuristic is preserved, not collateral. *The second half is what makes this AC able to fail in the direction that matters. A change that made every fallback undeterminable would pass the first half and silently break a legitimate case; a change that kept the fabrication would pass the second. Only the pair distinguishes "a real total, halved" from "a constant, halved" — and the code today cannot tell them apart either, which is the defect.* (FR-078.)
- **AC27 (one undeterminable signal does not throw away the other — NEW; not in D1.5e): Given** a host whose `/proc/meminfo` is unreadable **but** whose cgroup reports a finite `memory.max` and a usage figure, **When** `availableRAMBytes` is called, **Then** it returns the **cgroup-derived figure** and reports **determinable** — not `0`, and not `ok=false`. **And given** a host where **both** signals fail, **Then** and only then is the answer `ok=false`. **And given** a host where both succeed, **Then** the **smaller** is returned, unchanged from today. *This is the regression FR-078 would otherwise introduce, not a pre-existing bug: today's combination is `if ok && cgAvail < avail` (`pkg/config/config.go:655-661`), so once the meminfo half returns `0` for undeterminable, `cgAvail < 0` is never true and a valid cgroup reading is discarded. A container with an unreadable `/proc/meminfo` that DOES set `limits.memory` is fully measurable and would be held at the floor of 2 for no reason. The third clause is what stops a fix from quietly inverting the min into a max.* (FR-079.)

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

**US-25 (P1) Profile disk stays bounded, and the logins survive the bounding.** As an operator running many client workspaces on one host, the browser profiles do not fill my disk — and nothing that bounds them ever logs a client out.
- *Why P1 and not lower:* this project's own notes record the host root volume filling **twice**, and browser caches are the fastest-growing thing this design adds to it. *Why not P0:* nothing is broken on day one; the failure arrives with time and workspace count, and presents as a full disk rather than as a browser fault, which is exactly why it needs a requirement rather than vigilance.
- **AC1 (what is removed): Given** a workspace whose Chrome has closed (idle, evicted, or at shutdown), **When** the trim runs, **Then** every path on FR-072's allow-list is gone from `<profileRoot>/ws-<id>/` and the profile **directory itself still exists**.
- **AC2 (what is never removed — the assertion that matters): Given** the same profile carried a session cookie, saved credential, `Local Storage` entry and an `IndexedDB` database before the trim, **When** the trim runs, **Then** all four are byte-identical afterwards. *An AC1-only test passes against an implementation that deletes the whole directory, which is the one outcome this story exists to prevent.*
- **AC3 (the behavioural proof, not the file-level one): Given** a workspace logged into a real site, **When** its Chrome is closed, the trim runs, and the workspace's next browser call relaunches it, **Then** it is **still logged in** — and its first page load re-fetches assets, which is the accepted cost.
- **AC4 (what triggers it): Given** a key whose Chrome is closed by `pool.Close(k)`, **Then** the trim for that key runs **immediately**, without waiting for any interval; **and Given** a profile left behind by a `kill -9` where no close ever ran, **Then** the **boot** pass trims it; **and Given** a key whose close-time trim returned an error, **Then** the **scheduled** pass (`tools.browser.cache_trim_interval`, default 1h) trims it on its next tick.
- **AC5 (never against a live profile): Given** a key with a **live** Chrome, **When** any of the three triggers fires, **Then** that key is **skipped** and nothing under its profile is touched — eligibility being FR-042a's discriminator (no live instance **and** the per-key launch lock acquirable). *A trim that ran here would fail outright on Windows, where an open file cannot be deleted, and would leave a live Chromium's cache index describing entries that are gone.*
- **AC6 (allow-list, not deny-list): Given** a profile containing a directory name the implementation has never seen, **When** the trim runs, **Then** that directory is **kept**. *The default is KEEP. A deny-list trim widens itself with every Chromium upgrade, and the first place it widens into is wherever credentials moved to.*
- **AC7 (the unbounded case is declared, not hidden): Given** a workspace under continuous drive whose Chrome never closes, **Then** it is never eligible and its cache grows — **and** the config documentation for `tools.browser.cache_trim_interval`, the release note, and an operator-visible log all say so. *Escalated as **E-9**; declared rather than defaulted through, on the FR-066 pattern (FR-074).*

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

~~**Scenario: an agent's tabs are its own; the operator's are the workspace's — US-22/AC1+AC2+AC3, FR-048**~~ **TOMBSTONED by ADR D1.9c.**
- It asserted that agent B never sees agent A's tab. Under D1.9c that is true only when A and B are in **different sessions**, and **false by design** when they are the same chat — which is the ruling. Replaced by the two scenarios below rather than edited, because the Given it needs (*two sessions*, not *two agents*) is a different setup.

**Scenario: a session's tabs are the chat's, whichever agent is on it (*session-tabs-are-the-chats-operator-tab-is-the-workspaces*) (Happy Path) — US-22/AC6+AC7, FR-080**
- **Given** session S on workspace W, in which **Mia** has opened `https://example.com/a`, and the **operator** has opened `https://example.com/op` through the live panel
- **When** the operator switches the chat to **Jim** — the **same** `transcriptSessionID` — and Jim calls `browser_list_tabs`
- **Then** Jim's result contains **both** tabs: `…/a` in **this session's** set and `…/op` in the workspace-owned set, labelled distinctly
- **And** Jim can `browser_switch_tab` to `…/a`, drive it and close it — it is the session's tab, not Mia's
- **And** a turn running in a **different** session S2 on W sees `…/op` and **not** `…/a`
- *The last two steps are the ones that can fail. The first passes against an implementation that merged every tab set on the workspace — which is the state FR-001 produces if FR-080's key is not carried (§0.2a).*

**Scenario: two sessions on one workspace never merge (*two-sessions-never-contend*) (Happy Path) — US-22/AC8, US-9/AC7, FR-080, FR-081**
- **Given** one `*BrowserManager` for workspace W and two sessions S1 and S2 on it, a turn in each having opened a tab
- **Then** the manager holds **two distinct `sessionEntry` values** and neither one's `tabs` slice contains the other's `tabEntry`
- **And** a turn in S2 can neither list, switch to, drive nor close S1's tab
- **And when** both turns call `browser_navigate` on their own tabs within the same millisecond, **then** both complete, neither defers, and **neither waits on the other's lease** — asserted at the lease seam, not inferred from both succeeding
- **And** the ownership key read at every one of those operations is **`transcriptSessionID`** — a build reading `routingSessionID` instead fails this scenario only when the two sessions are a delegation parent and its child, so that case is in the fixture *(`pkg/agent/subturn.go:1282` vs `:1339`)*

~~**Scenario: the per-agent tab cap survives the re-key (Edge Case) — US-22/AC4, FR-049**~~ **TOMBSTONED by ADR D1.5a.**
- It drove `tools.browser.max_tabs = 5` and asserted A's sixth tab was refused while B could still open five of its own. **The key, the refusal and `maxTabsReachedErr` are all deleted from the code** (FR-059), so every step of it asserts machinery that will not exist.
- **Nothing about tab OWNERSHIP is lost with it** — that is *session-tabs-are-the-chats-operator-tab-is-the-workspaces* (and, for the cross-session half, *two-sessions-never-contend*), which are untouched. *(This named *agent's-tabs-are-its-own* until round-5 m-504 — a scenario D1.9c tombstoned, so the reassurance pointed at nothing.)* What is lost is the only scenario in which a tab open was ever refused, and its replacement is *runaway-tab-loop-is-stopped-by-memory* below.

~~**Scenario: two agents on their OWN tabs never contend — US-9/AC0, FR-048**~~ **TOMBSTONED by ADR D1.9c — and this one is worth reading, because it was GREEN while the hole it was written to guard was open.**
- It asserted *"the write lease was never acquired by either — under D1.9a this contention does not exist"*. **It asserted the premise, not the property.** Two *agents* never share a tab set; two *turns* can — one agent's heartbeat beside its own chat turn did, under D1.9a, with no arbiter (round-4 **C-402**). A scenario whose Then is *"the thing we assumed cannot happen did not happen"* cannot fail for the case it exists to cover.
- Replaced by *two-turns-in-one-session-contend-for-the-lease* (below) and by the cross-session half of *two-sessions-never-contend* (above), which assert the **property** — who blocks whom — rather than restating the assumption.

**Scenario: two turns in ONE session contend, and both finish (*two-turns-in-one-session-contend-for-the-lease*) (Edge Case) — US-9/AC7, FR-081**
- **Given** two turns running **concurrently on one `transcriptSessionID`** — the fixture drives the async system-notify path, a delegate completion notifying a live chat session (`pkg/agent/loop.go:3510` → `:3516` → `:7640-7643` → `:7734`), because it is the path the code itself documents as breaking the single-writer invariant (`:3491-3510`, **#505**)
- **When** both call `browser_navigate` against that session's tab set within the same millisecond
- **Then** exactly one navigation is observed by Chrome at any instant
- **And** **both calls eventually complete** — the loser retries inside the tool, within its own deadline
- **And** `acquireWrite` **was** called by both, asserted at the seam — a build that skips the lease on a non-workspace owner fails **here** and passes every other lease scenario
- **And** neither result is a Go error
- *This scenario is the whole reason §14's third scope row was rewritten rather than deleted. Run it against a build carrying FR-021's original `TabOwnerWorkspace()`-only trigger and it must be **RED**.*

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
- **And given** macOS, **then** the gate reads a **real** figure through the Darwin reader (FR-064) and the same three fixture ratios produce the same three answers as on Linux — the platform is no longer blind. **And given** Windows, **then** availability is **undeterminable**, and the gate **refuses to grow** rather than admitting (FR-065): the run **fails** if the pool launches or opens a tab on an unmeasurable host, and it also **fails** if the refusal is silent. *This scenario is the one that made §0.5 **E-6** visible instead of latent; **E-6 is now RULED** (ADR D1.5b, §0.6a), so it asserts a decided behaviour rather than staying red pending a ruling.*

**Scenario: the counters are gone from the code, not merely unset (*counters-are-gone*) — US-15/AC12, FR-059**
- **Given** the shipped binary and a repo-wide search
- **Then** `MaxTabs`, `MaxTotalTabs`, `TryOpenTab`, `ReleaseTab`, `reservedTabs`, `reserveGlobalTab`, `releaseGlobalTab` and `maxTabsReachedErr` resolve to **nothing** — in production code **and** in `_test.go` files
- **And given** a `config.json` carrying `tools.browser.max_tabs: 5`, **when** the gateway loads it, **then** it is **rejected with a named error** identifying the removed key
- *A deleted key that still loads and quietly does nothing is worse than the cap it replaced: an operator who sets it believes they have a limit they do not have. This is the same failure shape as the ADR-037 "saved, changed nothing" anti-pattern this project bans.*
- **And** `totalTabCountLocked` **still exists** — FR-080's per-session tab sets are still counted for listing and for the gate's telemetry; only its use as an enforcement point is removed

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
- **When** `applyReconcileOutcome` (`pkg/tools/browser/tools.go:321`, whose reason switch is `:346-356`) builds the model-visible note
- **Then** the outcome's reason is `tabAdoptReasonMemoryPressure` (`"memory_pressure"`), it matches its **own** arm of the switch, and the text names the host being out of memory **and** a remedy that exists
- **And** the text names **no limit and no config key**
- **And** no adoption refusal reaches the `default:` arm — *"it could not be adopted"*, with no reason and no remedy — which is where every memory refusal would land if the deleted `tabAdoptReasonMaxTabs` were removed without a replacement
- *An agent that cannot distinguish "the host is out of memory" from "something went wrong" **retries**, straight back into the runaway loop this ruling accepts the risk of. Found by the D2 spec.*

**Scenario: the Darwin reader returns the real machine's memory (*darwin-memory-reader-is-tied-to-the-machine*) — US-15/AC16, FR-064**
- **Given** a real Darwin host (the test is `//go:build darwin` and is skipped nowhere else)
- **When** `readMemTotalBytes()` is called
- **Then** it equals `hw.memsize` read independently through `unix.SysctlUint64` — **not** a constant, and **not** `vm.pages × pagesize`, which is smaller because firmware- and kernel-reserved pages are excluded (`8137872` vs `8388608` pages on the reference host)
- **And when** `readMemAvailableBytes()` is called, **then** `0 < available < total`, **strictly** at both ends
- **And** two calls separated by real allocation do **not** return an identical constant
- **And** the reader's doc comment names every sysctl it sums and states the **compression** and **purgeable** caveats
- *Why the bounds are strict: "returns non-zero" passes against a stub returning `1`, and "≤ total" passes against a reader that returns `total`. Tying total to `hw.memsize` is an oracle the implementation cannot satisfy by accident; the strict upper bound rejects "everything is available"; the strict lower bound rejects the `0` this platform returns today.*

**Scenario: an unmeasurable host refuses to grow, and says why (*unmeasurable-host-refuses-to-grow*) — US-15/AC17, FR-065, FR-082**
- **Given** an availability accessor returning `ok=false` — the Windows placeholder, or Linux fallen through to its 4 GB fallback constant (`pkg/config/meminfo_linux.go:16`) — and an **empty** pool
- **When** the **first** browser launch is requested, **then** it **succeeds**, and the **first** tab in it opens; a run in which either is refused **FAILS** (FR-082's floor)
- **And when** a **second** launch is requested, **then** it is **refused**, naming memory (FR-053)
- **And when** a **second** tab is requested inside that browser, **then** it is **refused** with FR-063's `memory_pressure` reason code — the refusal covers **both** paths, not only the launch path
- **And** the reason *"memory availability cannot be determined on this platform"* is logged **exactly once**, not once per call
- **And** a run in which the pool **grows past the floor** on an `ok=false` reading **FAILS**
- **And** a run in which the pool refuses but logs **nothing** also **FAILS** — the assertion is on the refusal *and* its explanation, not on the absence of a crash
- *This is a deliberate inversion of the usual fail-soft default: past the floor, an unmeasurable host is treated as **full**, not empty. A gate that cannot measure and answers "room available" reports success while admitting without limit — the false-green shape `docs/internal/false-green-patterns.md` catalogues, and the one D1.5a's own text forbids. **Both directions are required (round-4 C-401):** refusal-only is green on a build that refuses everything, which takes browsing away from gVisor — a **supported** Linux deployment (§0.9) — and admit-only is green on a build with no gate. This is the boundary §13 holdout 24, US-15/AC23, SC-027 and test 93 already assert; until this pass AC17 and test 83 asserted the opposite one.*

**Scenario: Windows is declared, not discovered (*windows-gap-is-declared*) — US-15/AC18, FR-066**
- **Given** the shipped tree
- **Then** `pkg/config/meminfo_windows.go` exists and returns the FR-065 unmeasurable signal explicitly — it does **not** fall through to a shared non-Linux stub that also serves BSD
- **And** its doc comment names the fix route: `GlobalMemoryStatusEx` via `NewLazySystemDLL("kernel32.dll")` (`golang.org/x/sys/windows/dll_windows.go:234,249`), since x/sys v0.47.0 wraps neither that call nor `MEMORYSTATUSEX`
- **And** the release notes contain a line stating the browser pool has **no memory-derived limit on Windows**
- **And** the browser section of the config documentation states the same
- **And** Windows browser support is recorded as **degraded-unsupported** for the pool
- *The placeholder is not decoration: it puts the gap at the point someone would fix it. A note in a spec is read by whoever reads the spec; a file named `meminfo_windows.go` is read by whoever goes looking for why Windows has no limit.*

**Scenario: the computed default is gone, and the accessor says the number is a backstop (*no-computed-default-remains*) — US-15/AC19, FR-067**
- **Given** a `PerformanceConfig` with `MaxParallelAgents: 0` and no `OMNIPUS_MAX_PARALLEL_AGENTS` in the environment
- **When** `EffectiveMaxParallelAgents()` is called
- **Then** it returns `(physicalConcurrencySafetyCeiling, false)`
- **And** the test **fails if the second value is `true`** — a backstop reported as a capacity is the whole defect FR-069 exists to prevent
- **And given** `MaxParallelAgents: 40`, **then** it returns `(40, true)`; **and given** `OMNIPUS_MAX_PARALLEL_AGENTS=50` alongside `MaxParallelAgents: 8`, **then** it returns `(50, true)` — the env-over-config precedence is unchanged
- **And** a repo-wide symbol search for `bytesPerAgent`, `autoDetectMaxParallel` and `clampParallel` returns **zero** hits, `_test.go` included
- **And** `clampParallelExplicit` **still exists** and still never lowers a large explicit value — *a deletion sweep that took the explicit path with it would satisfy every other assertion here*

**Scenario: one gate, two consumers, same answer (*one-gate-two-consumers*) — US-15/AC20, FR-068**
- **Given** the exported two-valued availability accessor stubbed to the same fixture readings the browser gate is tested against, at pressure ratios 0.84, 0.85 and 0.86
- **When** a browser launch and an agent admission are each requested at each ratio
- **Then** the two consumers return the **same** admit/refuse answer at all three ratios
- **And** both reached it through the **same** exported accessor and the **same** threshold value — asserted by seam, not by coincidence of outcome
- **And** a structural search finds **no** per-agent byte constant anywhere in the admission path, under any name
- **And** the run **fails** if a second threshold constant exists — *two numbers that happen to be equal today are two mechanisms tomorrow, which is what D1.5c ruled against*

**Scenario: an unmeasurable host holds at the floor and refuses to grow (*unmeasurable-host-holds-at-the-floor*) — US-15/AC21, FR-068a**
- **Given** the availability accessor forced to `ok=false` — the Windows case **and**, as a second fixture, Linux fallen through to `fallbackTotalRAMBytes/2`
- **When** three concurrent agent admissions are requested
- **Then** the first two are **admitted**
- **And** the third is **REFUSED**, and the refusal names **memory** and carries FR-063's reason code
- **And** the test **fails if the third is admitted** — *this is the assertion; "admits when memory is free" passes against a stub that always admits and proves nothing*
- **And** the explanation is logged **exactly once** for the process — the run fails on zero log lines **and** on one-per-call
- **And** the gateway still completes an ordinary turn on that host — *refuse to grow, never refuse to run; a test that only asserted the refusal would be satisfied by a gateway that admits nothing at all*

**Scenario: the announcement is the true one, and the UI does not recommend a backstop (*no-computed-default-is-what-is-announced*) — US-15/AC22, FR-069**
- **Given** the shipped release with nothing set for `performance.max_parallel_agents`
- **Then** the release note states **there is no longer a computed default**
- **And** it contains **no** claim that a default "moved", "changed", or went "from 2 to 2000" — the assertion is on the **absence** of that sentence, because it is the sentence an earlier revision of this spec and of ADR D1.5c both prescribed
- **And** the config documentation for that key says the same
- **And** the Settings → Performance panel renders **"automatic — bounded by available memory"**, and the rendered output contains **no** `2000` presented as a recommendation (`src/components/settings/PerformanceSection.tsx:218-229`)
- **And given** an explicit value of 40 instead, **then** the panel renders `40` exactly as it does today — *the unset case is what changed; the set case must not*

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
- **When** the 1-minute sweep runs (`gateway.go:5321-5355`, after its per-manager `ReapIdleSessions` loop)
- **Then** W's Chrome process is gone and `pool.LiveKeys()` no longer contains `ws:W`
- **And** W's profile directory still exists on disk

**Scenario: an idle-closed workspace relaunches, still logged in — US-12/AC4a, FR-040a**
- **Given** `ws:W` was idle-closed and its `browserMgrs` entry and `*BrowserManager` still exist
- **When** an agent on W calls `browser_navigate`
- **Then** `pool.Acquire(ws:W)` relaunches Chrome from `<profileRoot>/ws-W/` — the **flat** form (FR-037a, D1.8) — `LiveKeys()` grows by one, and the site is **still logged in**
- **And** no second `*BrowserManager` was created and no re-registration occurred
- **And** the relaunch succeeded even though the previous exit left a `SingletonLock` in that profile — `cleanStaleSingletons` ran against W's own directory, not just `cfg.ProfileDir`

**Scenario: the reaper cancels a browser context while the pool still lists the key (Edge Case) — US-12/AC6, FR-040a**
- **Given** `ws:W` is live in the pool and its manager's session hits `ReapIdleSessions`' all-tabs-idle branch, which cancels `se.browserCancel` (`manager.go:3073-3078`, executed at `:3123-3125`) and cancels the per-tab contexts (`:3106-3107`)
- *(**Corrected, round-4 M-404.** This Given used to end *"and calls `releaseGlobalTab` → `coord.ReleaseTab`"*. **FR-059 deletes both symbols and test 78 asserts they resolve to nothing, `_test.go` included** — so the precondition named machinery that will not exist in the tree this scenario runs against, and a fixture written to it does not compile. The cancellation is the whole of what survives, and the whole of what FR-040a's contract is about. Today's `releaseGlobalTab` call at `manager.go:3118` is a fact about the tree **before** this change; §2.1 is where it belongs, and it is stated there.)*
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
- **Then** exit 0, and the `contracts/` diff touches only `description:` text in the **four** schemas D1 edits — `BrowserAttachFrame.yaml`, `BrowserWebRTCOfferFrame.yaml`, `BrowserInspectRequest.yaml` and `PerformanceSettings.yaml` (FR-069) — with no added path, no retyped field and no `properties:`/`required:`/`enum:` change *(three until round-5 m-501)*
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

**Scenario: the operator's tab is taken by ACTING on it (*operator-tab-is-taken-by-acting*) (Happy Path) — US-22/AC5, FR-070**
- **Given** the operator opened a tab on workspace W through the live panel, and **no** human holds the control lock
- **When** agent Jim executes `browser_navigate` against `TabOwnerWorkspace()` through the real registered tool path
- **Then** the navigation happens, its effect is visible on that tab, and the tab's owner is **still `TabOwnerWorkspace()`**
- **And** no acquisition tool was called, because the registry contains **no** `browser_take_control`-shaped tool at all
- **And** no additional policy decision was consulted beyond `browser_navigate`'s own
- **And** the result body contains no field announcing a transfer of control
- *This scenario alone proves nothing about safety — it is green on a build with no control lock. Its partner below is the one that can fail.*

**Scenario: implicit acquisition is blocked while a human drives (*implicit-acquisition-is-blocked-while-a-human-drives*) (Error) — US-9/AC6, FR-071, FR-002c**
- **Given** a human viewer holds the live-view control lock, taken under the **resolved** key `ws:W`
- **When** agent Jim executes `browser_navigate` against `TabOwnerWorkspace()`
- **Then** the result is the existing ADR-038 D6 deferral — `IsError=false`, body `{"deferred": true, "reason": …}` with D6's text, **not** the lease's
- **And** `acquireWrite` was **never called** — asserted at the seam, not inferred from the outcome
- **And** the page is **unchanged** and no owner or tab-set state changed — *asserted on the page, because "no driver state changed" is vacuously true on every build this spec permits (round-4 C-403)*
- **And Given** the lock is held *and* Ray concurrently holds the write lease, **Then** Jim's reason text is still **D6's**, proving `controlledResult` ran before `leaseWrite`
- **And** the whole scenario is **red** against a build where `IsControlled` always returns `false` — the falsifiability receipt (SC-025)

**Scenario: a closed profile's cache is trimmed and its logins are not (*cache-trim-removes-only-regenerable-data*) — US-25/AC1+AC2+AC6, FR-072, FR-073**
- **Given** workspace W's profile holds a session cookie, a saved credential, a `Local Storage` entry, an `IndexedDB` database, a populated `Default/Cache/`, a `Default/Code Cache/`, and a directory name the implementation has never seen
- **And** W's Chrome has been closed by `pool.Close(ws:W)`
- **When** the trim runs
- **Then** every allow-listed path is gone and `<profileRoot>/ws-W/` itself still exists
- **And** the cookie, the credential, the `Local Storage` entry and the `IndexedDB` database are **byte-identical**
- **And** the unrecognised directory is **untouched** — the default is KEEP
- **And** one INFO records the key and the bytes reclaimed
- *The last three assertions are the ones that can fail; the first passes against an implementation that deletes the whole directory.*

**Scenario: a trimmed workspace reopens still logged in (*login-survives-the-trim*) — US-25/AC3, FR-073**
- **Given** workspace W is logged into a real site and its Chrome has been idle-closed
- **When** the trim runs and W's next browser call relaunches it
- **Then** the site reports the session as still authenticated
- **And** the first page load re-fetched assets from the network — the accepted cost of a cold cache

**Scenario: the trim fires on close, at boot, and on the sweep (*trim-fires-on-close-and-on-the-sweep*) — US-25/AC4, FR-072**
- **Given** three keys: one closed by `pool.Close`, one whose profile survived a `kill -9` with no close ever running, and one whose close-time trim returned an error
- **When** the close happens, the gateway boots, and one `tools.browser.cache_trim_interval` tick elapses, respectively
- **Then** the first is trimmed **immediately**, without waiting for any interval
- **And** the second is trimmed by the boot pass, after FR-042a's marker reconciliation has established liveness
- **And** the third is trimmed by the scheduled pass
- **And** a run in which the schedule never ticks still leaves the first key trimmed — *the interval is the net, not the mechanism*

**Scenario: a live profile is never trimmed (*trim-never-touches-a-live-profile*) (Edge Case) — US-25/AC5, FR-072**
- **Given** workspace W has a **live** Chrome and therefore a **held** per-key launch lock
- **When** each of the three triggers fires
- **Then** W is skipped every time and nothing under `<profileRoot>/ws-W/` is modified — not even a stat-visible mtime change on an allow-listed directory
- **And** eligibility was decided by FR-042a's discriminator (no live instance **and** the launch lock acquirable), not by a separate liveness test

**Scenario: the unbounded case is declared, not discovered (*the-continuously-driven-case-is-declared*) — US-25/AC7, FR-074**
- **Given** the shipped config documentation, the release note and the trim's own logging
- **When** each is inspected
- **Then** all three state that a workspace under continuous drive is never eligible for trimming and its cache is not bounded
- **And** the config doc for `tools.browser.cache_trim_interval` does **not** imply the interval bounds it
- *This is FR-066's pattern applied to disk: a gap that is declared is a decision; a gap that is silent is a defect waiting to present as a full root volume.*

**Scenario: one unmeasurable reading, two different correct answers (*one-predicate-two-responses*) (Edge Case) — US-15/AC23, FR-075, FR-065, FR-068a**
- **Given** one stubbed availability accessor returning `ok=false`, shared by both consumers
- **When** the pool is asked for a **second** browser instance and three concurrent agent turns are requested, in the same run
- **Then** the pool **refuses to grow**, naming memory
- **And** agent admission **admits two turns and refuses the third**, naming memory
- **And** the refusal messages come from the same reason code (FR-063) with different remedies
- *Either assertion alone is green on a build that collapsed the two responses: "the pool refused" passes when everything refuses, "two turns ran" passes when nothing is gated. The pair is the assertion.*

**Scenario: the gateway knows it is in a container without asking about the limit (*containerisation-is-detected-without-the-limit*) (Edge Case) — US-15/AC24, FR-076**
- **Given** a Kubernetes-pod fixture: `KUBERNETES_SERVICE_HOST` set, `/proc/self/cgroup` showing a `kubepods` path, and **no** `/.dockerenv`
- **And** a Docker fixture with `/.dockerenv` present, and a bare-metal fixture with neither and a `system.slice` cgroup path
- **And** the cgroup-limit and `/proc/meminfo` readers stubbed to **panic if called**
- **When** the containerisation predicate is evaluated on each
- **Then** it answers **true** for the pod, **true** for Docker, and **false** for bare metal
- **And** no stub was reached, so the answer did not depend on the memory limit
- *The pod case is the one a `/.dockerenv`-only predicate gets wrong — Kubernetes runs containerd or CRI-O and drops no such file, so reusing `isRunningInDocker` (`pkg/gateway/sandbox_apply.go:185-201`) answers false in exactly the deployment D1.5e is about. The bare-metal case is what stops the predicate from being `return true`.*

**Scenario: the node-memory warning fires in a pod and nowhere else (*node-memory-warning-fires-only-when-it-means-something*) (Edge Case) — US-15/AC25, FR-077, FR-076**
- **Given** four startup fixtures: containerised without a limit, containerised **with** a limit, bare metal without a limit, and bare metal with a limit
- **When** the gateway starts on each
- **Then** the first emits **exactly one** WARN naming the condition (*no container memory limit set; sizing against node memory*) and the remedy (`resources.limits.memory`)
- **And** the other three emit **no such line at all**
- **And** all four start successfully — the warning never becomes a refusal
- *Assert the silent cases or do not bother asserting anything: "it warns in a pod without limits" is green on a build that warns unconditionally, and a line on every bare-metal start is filtered long before the day it matters. Bare-metal-without-a-limit is the ordinary, correct case, which is why refusing was rejected outright.*

**Scenario: an unreadable `/proc/meminfo` is undeterminable, not an invented 2 GiB (*unreadable-meminfo-is-undeterminable-not-two-gigabytes*) (Edge Case) — US-15/AC26, FR-078, FR-065, FR-068a**
- **Given** a Linux host whose `/proc/meminfo` cannot be opened
- **When** the memory reader is called
- **Then** the result is **undeterminable**, not 4 GiB and not 2 GiB, and `fallbackTotalRAMBytes` exists nowhere in the repo
- **And** the browser pool therefore **refuses to grow** and agent admission **holds at the floor of 2** — §0.9's ruled responses engage, which they cannot today
- **And given** a readable `/proc/meminfo` with a real `MemTotal` and **no** `MemAvailable` line
- **Then** the result is **half of the real `MemTotal`** and is **determinable**
- *The two halves are one assertion. Today both inputs produce "a number, halved" and nothing downstream can tell a real total from a constant — which is how a 512 MB container reads as having 2 GiB free. Making every fallback undeterminable would pass the first half and break the pre-3.14 case; keeping the constant passes the second.*

**Scenario: a cgroup reading survives an unreadable `/proc/meminfo` (*one-dead-signal-does-not-discard-the-other*) (Edge Case) — US-15/AC27, FR-079**
- **Given** a host whose `/proc/meminfo` is unreadable **and** whose cgroup reports a finite `memory.max` and a usage figure
- **When** `availableRAMBytes` is called
- **Then** it returns the **cgroup-derived** figure and reports **determinable**
- **And given** both signals fail, **Then** and only then is the answer `ok=false`
- **And given** both succeed, **Then** the **smaller** is returned, unchanged from today
- *This is the regression FR-078 introduces if nobody specifies it: the combination is `if ok && cgAvail < avail` (`pkg/config/config.go:655-661`), so a `0` meaning "undeterminable" makes `cgAvail < 0` permanently false and throws the good reading away. A GKE Sandbox pod that DOES set `limits.memory` is fully measurable and would sit at 2 agents for no reason. The third clause stops a fix that inverts the min into a max.*

---

## 9. Traceability matrix (FR ↔ US ↔ BDD ↔ test ↔ ADR/review)

| FR | Requirement | US | BDD | Test (TDD) | Source |
|---|---|---|---|---|---|
| FR-001 | One `BrowserManager` per browsing key; `browserMgrs` re-keyed | US-1 | handover | `TestLoop_BrowserManagerForKey_OnePerKey` | D1.1(2) |
| FR-002 | Every browser tool addresses the resolved key **and an explicit `TabOwner`**, not `DefaultSessionID` | US-1, US-22 | handover, session-tabs-are-the-chats-operator-tab-is-the-workspaces | `TestTools_UseResolvedKeyNotConstant` | D1.1(2), **D1.9c** |
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
| **FR-009** | **A delegated sub-turn uses its workspace's browser and its logins — no separate key, manager, Chrome or profile.** It addresses **its own session's** tab set (D1.9c, FR-080) — the child's `transcriptSessionID`, not the parent's, and not the target agent's | US-5/AC1+AC2 | delegated-shares-browser | `TestSubTurn_UsesWorkspaceBrowser` | D1.10 (superseding ruling), D1.9a |
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
| FR-019 | Write lease held for one action-tool call, **scoped to the resolved `TabOwner` — the workspace-owned set or a session's own** (**§14**; the "workspace-owned only" scoping D1.9a gave this row is superseded by **FR-081**) | US-9/AC1+AC7 | two-writers-shared-tab, two-turns-in-one-session-contend-for-the-lease | `TestWriteLease_OneWriterOnSharedTab` | D2.10, rescoped by D1.9a, **re-widened by D1.9c** |
| **FR-019a** | A `browser_*` tool takes the write lease **iff** it is gated by `controlledResult`; the exempt set is **six** (4 read-only shipped incl. `browser_list_tabs` + `browser_snapshot` + `browser_handle_dialog`); the check enumerates the **registry** and compares the two gates behaviourally | US-9/AC5 | lease-membership-follows-control-gate | `TestWriteLease_EveryActionToolIsLeased` | MAJ-008, round-2 CRIT-104/MAJ-101/MAJ-102 |
| **FR-023a** | `tools.browser.lease_wait` is **clamped** against `tools.browser.page_timeout` at config load and on reload, with a WARN naming both keys and values. **Its purpose is restated:** it bounds the *retry window* so a contended call still finishes inside its own CDP deadline (FR-020), rather than — as revision 3 had it — guaranteeing a deferral instead of an error. Under FR-020 a deferral is the **outcome past the bound**, not the goal | US-9 | lease-wait-clamped | `TestConfig_LeaseWaitClampedAgainstPageTimeout` | round-2 MAJ-112, D2.10 |
| FR-020 | The loser **retries inside the tool with backoff**, within its own deadline; **both writers eventually complete**. Only past the bound does it return a non-error `{"deferred":true,"reason":…}` naming the holder. `deferred` is retained unchanged for the human-holds-control case | US-9/AC1 | two-writers-shared-tab | `TestWriteLease_BothWritersEventuallyComplete`, `TestWriteLease_LoserDefersPastBound` | D2.10, ADR crit 16 |
| FR-021 | Read-only tools ungated. ⚠️ **Its second clause — *"no tool of any class is leased when it addresses an agent's own tab set; the lease is reached only for `TabOwnerWorkspace()`"* — is SUPERSEDED by FR-081 (D1.9c).** It rested on "no second writer can reach that set", true across sessions and false within one (§0.2a, §14). **The read-only half stands unchanged.** | US-9/AC4~~+AC0~~ | read-only-never-deferred | `TestWriteLease_ReadOnlyToolsUngated`, ~~`TestWriteLease_OwnTabNeverAcquires`~~ | D2.10, D1.9a |
| | ⚠️ **`TestWriteLease_OwnTabNeverAcquires` is TOMBSTONED with the superseded clause (round-5 M-503).** Its oracle — *the lease was never acquired for a session's own tab* — is the exact statement **FR-081 inverts**, and test **99(c)** `TestLease_TakenOnSessionOwnerNotOnlyWorkspace` asserts `acquireWrite` **was** called for a `TabOwnerSession()` write. Leaving both named would have made the suite unsatisfiable, and the one that survives is the one that can fail for the right reason. *This is the same trap as the tombstoned US-9/AC0: a test whose Then restates the premise.* | | | | |
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
| **FR-040a** | The idle window and the reaper↔pool contract are **named**: config key `tools.browser.idle_close_ttl` (default **15m** = 3× the per-tab `idle_ttl`; §12 A22 gives the derivation), caller = the existing 1-minute sweep (`gateway.go:5321-5355`) **after** its `ReapIdleSessions` loop, post-close state = pool entry and Chrome gone, `browserMgrs` entry and `*BrowserManager` **retained**, next call relaunches from the profile. `ReapIdleSessions` cancelling `se.browserCancel` must never leave a key the pool reports live but nothing can drive | US-12/AC4a+AC6 | idle-close-relaunch, reaper-cancels-while-pool-live | `TestPool_RelaunchAfterIdleClose`, `TestReaper_CancelDoesNotStrandPoolEntry` | round-2 CRIT-102, MAJ-108 |
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
| ~~FR-048~~ | **SUPERSEDED by ADR D1.9c (2026-09-02) — tombstone, not a hole, and the reason is a re-key rather than a withdrawal.** It made tab ownership explicit with `TabOwnerAgent(agentID)` for an agent's own tabs and `TabOwnerWorkspace()` for the operator's. **The `TabOwnerWorkspace()` half survives verbatim in FR-080**; the `TabOwnerAgent` half is replaced by `TabOwnerSession(transcriptSessionID)`, because the operator ruled tabs belong to sessions *"no matter which agent is on it"*. **Read this as a key changing, not a requirement being dropped** — an explicit owner on every tab operation is still mandatory, and FR-080's test is stronger than this row's was. Its scenario (*agent-tabs-are-own-operator-tab-is-shared*), `TestTabs_TwoAgentsDoNotMerge` and US-22/AC1+AC2+AC3 are tombstoned with it; US-7/AC5 and `TestTabs_WorkspaceOwnedSetIsVisibleToAll` **carry forward to FR-080 unchanged**. | — | — | — | **D1.9c** (superseding D1.9a) |
| **FR-080** | **Tab ownership is explicit and keyed on the SESSION (D1.9c).** A `TabOwner` accompanies the browsing key at every tab operation: **`TabOwnerSession(transcriptSessionID)`** for the tabs opened in that chat, `TabOwnerWorkspace()` for the tabs the **operator** opened. The manager holds one `sessionEntry` per session that has browsed plus at most one workspace entry. **Every agent on a session sees and can drive that session's whole tab set — switching the chat from Mia to Jim does not move, hide or duplicate a tab.** No session ever sees another session's tabs. Every agent on the workspace sees the workspace-owned set. `browser_list_tabs`' payload labels which is which. **The id is `transcriptSessionID` and never `routingSessionID`** — the latter's own doc comment forbids reading it as an *"ownership predicate"* (`pkg/agent/turn.go:348-353`) and it is inherited verbatim through a delegation subtree (`pkg/agent/subturn.go:1339`), which would merge every descendant's tabs into the root's. **An EMPTY `transcriptSessionID` is a named failure, not an owner** (round-5 M-501): `TabOwnerSession("")` returns `ErrNoTabOwner`, never a usable owner and never a fall-through to `TabOwnerWorkspace()`. The id is `""` on every turn with no transcript — an ordinary handled state (`pkg/agent/goal_loop.go:74`, `:555`; `pkg/agent/loop_command.go:78`; `pkg/agent/loop.go:7779`, `:8804`; `pkg/agent/tool_manifest.go:12-20`) — so treating it as an owner gives every transcript-less turn on the workspace one shared tab set | US-22/AC6+AC7+AC8, US-7/AC5 | session-tabs-are-the-chats-operator-tab-is-the-workspaces | `TestTabs_TwoSessionsDoNotMerge`, `TestTabs_AgentSwitchWithinASessionSeesTheSameTabs`, `TestTabs_OwnerKeyIsTranscriptNotRouting`, `TestTabs_WorkspaceOwnedSetIsVisibleToAll`, `TestTabs_EmptyTranscriptSessionIsNamedFailure` | **D1.9c** |
| **FR-082** | **The unmeasurable host's floor is ONE browser and ONE tab in it (round-4 C-401).** Where the accessor reports `ok=false`, the pool admits the **first** Chrome and the **first** tab and refuses the **second** of each, naming memory. **"Refuse to grow" is meaningless until "from what" is fixed**, and this document fixed it both ways: AC17/FR-065/test 83 read as refusing the first launch, while §13 holdout 24, US-15/AC23, SC-027 and FR-068a read the first as succeeding. **The floor is per HOST — `len(pool.LiveKeys()) == 1`, not one per workspace key.** A floor of **zero would remove browsing entirely from gVisor and GKE Sandbox**, `/proc`-less **Linux** deployments §0.9 establishes as supported; a floor of **two** is unpriced, because the second Chrome's ≈182 MB `PER_BROWSER_COST` is precisely the figure an unmeasurable host cannot check itself against. **The two consumers' floors differ on purpose** — agents hold at 2 (FR-068a), the browser at 1 — and FR-075 already requires the responses to differ; this row says by how much on the browser side | US-15/AC17, US-15/AC23 | unmeasurable-host-refuses-to-grow | test 83 — `TestPool_UnmeasurableHostRefusesToGrow` (both halves), `TestManager_UnmeasurableHostRefusesTabOpen` | **round-4 C-401**, §0.6a, D1.5b |
| **FR-081** | **The write lease is consulted on a SESSION's tab set too, not only the operator's (D1.9c's residual, answered from code).** FR-021's *"only when the resolved `TabOwner` is `TabOwnerWorkspace()`"* trigger is **superseded**: every `controlledResult`-gated call takes the lease on **its own resolved owner**, session or workspace. **Why it is a requirement and not defensive coding:** the second writer is a second *turn*, not a second *agent*, and three shipped paths start a turn on an already-live `transcriptSessionID` — `/loop` (`pkg/agent/loop_command.go:90` → `pkg/agent/loop_scheduler.go:118`, `:215`), async system-notify (`pkg/agent/loop.go:3510` → `:3516` → `:7640-7643` → `:7734`, which the code's own comment at `:3491-3510` documents and files as **#505**), and cron `SessionModeMain` (`pkg/gateway/schedules.go:548`, dispatched per-job in parallel at `pkg/cron/service.go:568-590`). **A lease taken on one owner never blocks the other**, so cross-session calls still never contend. **This spec must not depend on #505** — §0.5 E-10 | US-9/AC7 | two-turns-in-one-session-contend-for-the-lease, two-sessions-never-contend | `TestLease_TwoTurnsOneSessionSerialise`, `TestLease_TwoSessionsNeverBlockEachOther`, `TestLease_TakenOnSessionOwnerNotOnlyWorkspace` | **D1.9c** (superseding FR-021's trigger clause) |
| ~~FR-049~~ | **WITHDRAWN by operator ruling (ADR D1.5a) — tombstone, not a hole, and the REASON matters.** It gave `cfg.MaxTabs` an owner after the re-key, so a per-**agent** cap of 5 did not silently become 5 for the whole team once managers became per-workspace. That was the **right answer** — §16 M7(c) and §17 record it as a CRITICAL resolution — **to a question that no longer exists**: `tools.browser.max_tabs` is deleted from the code (FR-059), so there is no cap to own. **Read this as a resolution being dissolved, not a regression.** The tab **sets** are untouched — that is ownership, not capacity. *(They are no longer per **agent** either: ADR D1.9c re-keys them to the session — FR-048 tombstoned, FR-080. Neither ruling touches capacity.)* Its scenario (*per-agent-max-tabs*), its dataset rows and `TestMaxTabs_IsPerAgentNotPerWorkspace` are tombstoned with it. | — | — | — | **D1.5a** (superseding D1.9a / §17 M7c) |
| **FR-050** | **LRU eviction under memory pressure, with two guards — RE-DERIVED: the trigger is the gate, not a target.** When FR-057's live-memory gate refuses, `Acquire` closes the **least recently used evictable** instance and **re-asks the gate**; nothing surfaces to agent or operator; the evicted profile survives and its workspace reopens signed in. **Never evict an instance with a live viewer** (FR-010, FR-052) **or with a browser tool call in flight** (FR-051). "Least recently used" is by last tool call or viewer activity. **D1.5a changes only what invokes eviction** — previously *"the pool is at its target"*, now *"the host is out of headroom"*; D1.7's two guards are unchanged, and so is the rule that the profile survives (FR-043a) | US-15/AC1+AC2+AC3+AC4 | pool-evicts-lru-under-pressure, eviction-skips-viewer, eviction-skips-inflight | `TestPool_EvictsLRUAndRelaunches`, `TestPool_EvictionSkipsViewer`, `TestPool_EvictionSkipsInFlight` | **D1.7**, D1.5a |
| **FR-051** | **`BrowserManager.InFlight()`** — a counter incremented by **every** `browser_*` tool's `Execute`, leased and lease-exempt alike, released by `defer`, consulted by eviction. The write lease cannot serve as this signal: §14's exempt set is six tools, so a `browser_screenshot` holds none. Incremented under the same `pool.mu` eviction selection holds, so a call starting during selection cannot be evicted (§3.1 locking discipline) | US-15/AC4 | eviction-skips-inflight | `TestPool_InFlightBlocksEviction`, `TestPool_EvictionRaceWithExemptCall` (`-race`) | **D1.7** |
| **FR-052** | **Viewer staleness.** A viewer whose transport has been silent past the existing WebRTC liveness window counts as **detached** for both eviction and idle close. Without it one abandoned panel pins a slot for the process's lifetime — and under eviction makes that slot permanently unreclaimable, which is a deadlock rather than a leak | US-15/AC5, US-12/AC2 | stale-viewer-unpins | `TestPool_StaleViewerDoesNotPin` | **D1.7** |
| **FR-053** | **Refusal, naming memory — REWRITTEN; the bounded overshoot it specified is deleted.** Its old form let the pool exceed a soft target by exactly one and WARN, on the reasoning that a soft cap is cheaper to break than a browse is to refuse. **There is no target to overshoot.** D1.5a rules the memory gate a **hard** stop, so when the gate refuses and nothing is evictable (every instance pinned by a live viewer or an in-flight call), the request **waits** for an instance to become evictable up to the tool call's own deadline, then **fails with a named error identifying the workspace and naming MEMORY as the binding constraint** — never a cap, because there is no cap left and an operator told otherwise would go looking for a ceiling to raise. The error carries FR-063's reason code so callers can branch on it. **The pool never launches past the gate:** overshooting real available memory invokes the OOM killer, which does not stop at the browser and can take the gateway with it, ending every session on the host | US-15/AC6+AC7 | nothing-is-evictable-then-refuse | `TestPool_NothingEvictableWaitsThenRefusesNamingMemory`, `TestPool_NeverLaunchesPastTheGate` | **D1.5a** (superseding D1.7's `target + 1`) |
| **FR-054** | **Thrash detection (gated on G-5) — RE-DERIVED, because its remedy named a key that no longer exists.** The pool counts evict-then-reopen cycles per key over a rolling window. Past the configured threshold it logs **exactly one** WARN naming the contending workspaces and **memory** as the binding constraint. **Its remedy changes:** *"raise `tools.browser.max_browsers`"* dies with the key, and the only true remedies are *give the host more memory* or *run fewer workspaces concurrently at once* — the WARN must name one of those. The target-vs-ceiling trap old SC-022 warned about is **gone with both terms**: there is exactly one binding constraint now, so the WARN cannot name the wrong one. The window and threshold are derived from cold-start latency with a warm profile (**G-5**) and are configuration until it runs — ADR-042's ~30–60 s covers a fresh install including a Chromium download and is not that number | US-15/AC8+AC8a | thrash-warns-once | `TestPool_ThrashWarnsOnce`, `TestPool_ThrashWarnNamesMemoryNotACap` | **D1.7**, D1.5a |
| ~~FR-055~~ | **WITHDRAWN by operator ruling (ADR D1.5a) — tombstone, not a hole, and its withdrawal is a SECURITY IMPROVEMENT.** It set `--renderer-process-limit=R` on every per-key Chrome with `R >= tools.browser.max_tabs` as a site-isolation **floor**. That was the **right answer** — §17 C4 records it as a CRITICAL resolution — **to a question that no longer exists**, and it is moot twice over: the key it derived from is deleted (FR-059) and the flag it configured is now **never set at all**. The flag *weakens* site isolation above its bound (over-limit navigations reuse same-site processes), justified as acceptable for *"agent-driven browsing of semi-trusted destinations"* — an adjective `ValidateURL` (`pkg/tools/browser/manager.go:685-708`) does not support: it blocks five schemes (`blockedSchemes`, `:675-683`) plus private and metadata addresses via the SSRF checker, and permits **every other public `http(s)` URL**, with no allow-list anywhere in `pkg/tools/browser/`. Not setting it retains Chrome's **default site-per-process isolation in full**, so **C-303 / C4 / C206 are DISSOLVED, not mitigated** — no residual trade-off to accept, no compensating control to remember, nothing for a future reviewer to re-litigate (§0.6, ADR P8). `TestPool_LaunchArgvCarriesRendererLimit` is **inverted** into `TestPool_LaunchArgvHasNoRendererLimit` under FR-062; `TestPool_CrossSiteTabsGetDistinctRenderers` is **kept unchanged** there, since it now asserts the stronger property. | — | — | — | **D1.5a** (superseding D1.6 / §17 C4) |
| ~~FR-056~~ | **WITHDRAWN by operator ruling (ADR D1.5a) — tombstone, not a hole.** It derived `target = clamp((min(host_RAM, cgroup_limit) × 0.5 − gateway_reserve) / (FIXED_FLOOR + R×85MB + encoder_page), 1, operator_ceiling)` and made `tools.browser.max_browsers` its ceiling. **Never built, and now unbuildable:** the ADR withdrew the 85 MB per-renderer constant on measured evidence (real renderers spanned **30 MB → 327 MB in one snapshot**, an 11× spread, and renderer count is not tab count — 2 tabs against 13 renderers, ~6 processes per tab), `R` goes with FR-055, `FIXED_FLOOR`/`gateway_reserve` go with FR-044's narrowing, and `operator_ceiling` is deleted. Capacity uses **live measurement and no constant** (FR-057, FR-062). Its scenario (*derived-target-not-a-constant*), its dataset rows and both its tests are tombstoned. **One live consequence survives it and moves to FR-057:** `availableRAMBytes` (`pkg/config/config.go:655`) and the `meminfo_linux.go` readers are **unexported**, so `pkg/tools/browser` cannot call them — that export question now blocks the only gate that remains. | — | — | — | **D1.5a** |
| **FR-057** | **The live-memory admission gate — the ONLY admission control, and a HARD stop (REWRITTEN; §0.5 E-2 is RULED).** Before any browser launch — and, per FR-060, before any tab open — the pool asks what is **actually free** and refuses to grow when the answer is short: `headroom >= PER_BROWSER_COST` for a launch (FR-062), and a pressure **ratio** with no per-tab byte constant for a tab (§0.5 E-5). On Linux under a cgroup limit the ratio is `memory.current / memory.max > 0.85`, read through the existing `readCgroupV2LimitBytes` / `readCgroupPlainUintBytes` / `readMemAvailableBytes` (`pkg/config/meminfo_linux.go`), which must first be **exported** (FR-056's surviving consequence). **The collision with FR-050 is RESOLVED, not escalated:** D1.5a rules this gate a hard stop and deletes the soft cap, so when pressure is high **and** every instance is pinned, the request is **refused** (FR-053) with an error naming **memory**. *(Revision 4 left this open as E-2 and told the implementation not to pick silently; the operator has now picked.)* **No counter stands behind this gate** — a gap in it is not a degraded limit but no limit (FR-061). **⚠ Off-Linux, E-6 is RULED (ADR D1.5b, §0.6a): macOS gets a real reader (FR-064); Windows keeps no signal and the gate therefore REFUSES rather than admits (FR-065, FR-066). The export this FR requires also changes shape — `(bytes, ok)`, so "unknown" is distinguishable from "zero free"** | US-15/AC11, AC11a | pressure-gate-thresholds, nothing-is-evictable-then-refuse | `TestPool_PressureGateAt084_085_086` (fixture-driven), `TestPool_PressureGateIsHardWhenNothingEvictable`, `TestPool_PressureGateOffLinuxIsNotSilent` | **D1.5a**, D1.5 item 3 |
| **FR-057a** | **Gates G-3 and G-4 (mechanical).** Before FR-057 ships: (G-3) does Chromium read a cgroup memory limit, or size itself against host RAM inside a capped container? (G-4) does Linux memory-pressure signalling fire for Chrome at all? Both are one Chrome inside a `memory.max`-capped cgroup, reading back its own renderer limit and whether a notification arrives. **D1.5a raises the stakes on both**: G-3 negative means Chrome is sizing itself against the host while our gate reads the cgroup, and there is **no pool counter left** to compensate; G-4 negative means Chrome never self-discards under pressure, so every byte it takes is a byte our gate must have already refused. Neither can be answered in prose | — | — | receipts in the PR body (SC-019) | **D1.5**, D1.5a |
| **FR-058** | **Audit event names match `^[a-z_]+$`** — the pattern `contracts/components/schemas/AuditEntry.yaml:17` enforces. A dotted name blanks the **entire** Audit Log viewer, not just its own row (#667). The test asserts a deliberately dotted fixture name **fails**, so the check cannot pass vacuously | US-13/AC4 | audit-event-name-is-viewer-safe | `TestAudit_EventNamesMatchViewerPattern` | **D2.11**, #667 |
| **FR-043b** | **Upgrade inherits nothing.** No workspace adopts the existing global `~/.omnipus/browser/profiles/default/` (`manager.go:125`); every workspace starts with a fresh `ws-<id>` profile and is logged out. `profiles/default/` is **left on disk, untouched and unused** — deleting it would destroy logins the operator may still want and no code can tell whether they matter. A release-note line states that agents sign in again, per workspace | US-23 | upgrade-inherits-nothing | `TestPool_UpgradeInheritsNoProfile` | **D1.8**, ADR crit P11 |
| **FR-059** | **The deletion itself, as implementation scope (Stream A), not a config change.** Remove from the code: `BrowserConfig.MaxTabs` (`pkg/tools/browser/manager.go:36`, default 5 at `:124`), `ToolsBrowserConfig.MaxTabs` (`pkg/config/config.go:3633`) and its application (`pkg/agent/loop.go:2314-2315`); `max_total_tabs` (`pkg/config/config.go:3678`) with the coordinator field (`coordinator.go:128`), its ctor arg (`:226-233`), `SetMaxTotalTabs` (`:635-644`), its `ApplyRuntimeConfig` arm (`:659-660`), its gate (`:785-792`) and the threading at `loop.go:2452,2455`; the reservation machinery `reservedTabs` (`coordinator.go:137`), `TryOpenTab` (`:782-804`), `ReleaseTab` (`:806-812`), `totalOpenTabsLocked` (`:818`), `reserveGlobalTab` (`manager.go:3343-3352`), `releaseGlobalTab` (`:3358-3366`) and their call sites (`tabs.go:180,249,260`; `manager.go:3118`); the five `totalTabCountLocked() >= m.cfg.MaxTabs` checks (`manager.go:1139, 2005, 2047, 2216, 2286`) and `maxTabsReachedErr` (`:1379`); and the log lines that print the caps (`coordinator.go:250, 257`). **`totalTabCountLocked` itself survives** — FR-080's per-session tab sets still need to be counted for listing and for FR-060's gate telemetry; only its use as an enforcement point goes. `tabAdoptReasonMaxTabs` (`manager.go:2108`) is **replaced**, not deleted — FR-063. **Test migration is in scope and in the same commit:** 59 references across 10 `_test.go` files in `pkg/tools/browser` name these keys; none may be left asserting a cap that no longer exists, and no test count may drop without the replacement named in its commit message | US-15/AC12 | counters-are-gone | `TestNoResidualTabCap` (repo-wide structural, incl. `_test.go`), `TestConfig_MaxTabsKeyIsRejected` | **D1.5a**, §0.6 |
| **FR-060** | **The pressure gate sits in the TAB-OPEN path, not only the browser-launch path.** FR-057's check runs before every tab is created, at **exactly the five sites `cfg.MaxTabs` is checked today** — `manager.go::createFirstTab` (`:1139`), `::OpenTab` (`:2005`, `:2047`) and `::adoptTarget` (`:2216`, `:2286`) — so the deletion and the replacement land at the same lines, in the same commit. **Why this is not optional:** a runaway agent looping `browser_open_tab` inside an already-running browser never reaches a launch decision, so a launch-only gate would let it run unchecked to the OOM killer, **and no counter remains to catch it** (FR-038, FR-049 are tombstoned). The tab gate is a **ratio** with no per-tab byte constant — the 85 MB constant is withdrawn and a tab has no measured floor (§0.5 E-5). A refusal here returns FR-063's reason code, not a bare failure | US-15/AC13 | runaway-tab-loop-is-stopped-by-memory | `TestManager_TabOpenChecksPressureAtAllFiveSites`, `TestManager_RunawayTabLoopIsRefusedNamingMemory` | **D1.5a**, §0.6 |
| **FR-061** | **Idle close and the pressure gate are the ENTIRE defence; neither may silently no-op.** Tab reaping already ships — `ReapIdleSessions` (`manager.go:2986`) with `DefaultIdleTTL` **5 minutes** (`manager.go:130-134`); whole-browser idle close is new work (FR-040, FR-040a). Under D1.5a both stop being housekeeping and become load-bearing: previously a counter caught a runaway before memory did, and **that backstop is gone by decision**, so **a gap in either control is not a degraded limit — it is no limit.** Consequences that are requirements, not advice: neither may ship disabled, "best effort", or behind an off-by-default flag on any supported platform; each carries a test that **fails if the control silently does nothing** (a reaper that never fires, a gate that always returns "room available"); and a platform on which either cannot run must be declared, not defaulted through — **E-6 is ruled and this is now specified rather than escalated: macOS gets a real reader (FR-064), an undeterminable host REFUSES (FR-065), and Windows is declared (FR-066)**. §0.3's withdrawn carve-out applies here in full | US-15/AC14 | idle-close-actually-closes, gate-cannot-vacuously-pass | `TestReaper_FailsIfNothingIsEverClosed`, `TestPool_GateFailsIfItAlwaysAdmits`, `TestPool_IdleCloseIsNotBehindAFlag` | **D1.5a**, §0.6 |
| **FR-062** | **`PER_BROWSER_COST` ≈ 182 MB is the launch-headroom minimum, quoted with its scope — and NO per-renderer or per-tab constant is used anywhere.** The launch gate admits only when live headroom ≥ this figure. **Its scope travels with it, every time it is quoted:** one machine, one snapshot, **macOS**, `top`'s physical-footprint column, Chrome for Testing, and an **idle, non-capturing** instance — a *capturing* browser costs this plus the injected capture extension plus the encoding work, and that delta is **unmeasured** (FR-044/G-1's remaining job). **Nothing is priced per renderer or per tab:** the 85 MB Chromium constant is withdrawn on measured evidence (30 MB → 327 MB in one snapshot, an 11× spread), and renderer count is not tab count (2 tabs against 13 renderer processes — ~6 per tab), so `FIXED_FLOOR + (R × 85MB) + encoder_page` is an arithmetic this spec **may not perform**. **`--renderer-process-limit` appears nowhere in the launch flags** (FR-055's tombstone), which is what preserves full site isolation | US-15/AC9+AC9b | no-constant-in-the-capacity-path | `TestPool_LaunchHeadroomUsesMeasuredCost` (test 51, re-derived), `TestPool_LaunchArgvHasNoRendererLimit`, `TestPool_CrossSiteTabsGetDistinctRenderers`, `TestPool_NoPerRendererConstantInCapacityPath` (structural) | **D1.5a**, ADR P8, ADR "85 MB withdrawn" |
| **FR-063** | **A reason code naming MEMORY, so the model-visible message can branch.** `tabAdoptReasonMaxTabs` (`manager.go:2108`, `"max_tabs_reached"`) is **replaced** by `tabAdoptReasonMemoryPressure` (`"memory_pressure"`), returned at the two `adoptTarget` refusal points (`manager.go:2223`, `:2287`) and by FR-060's other three sites, and `applyReconcileOutcome`'s switch (`pkg/tools/browser/tools.go:346-356`, in the function declared at `:321`) gains the matching arm. **Why this is a requirement and not a rename:** today the `max_tabs` arm produces an actionable sentence — *"the maximum concurrent tabs limit was reached… Close a tab with `browser_close_tab` and retry"* — and deleting the cap without a replacement code drops every memory refusal into the `default:` arm, *"it could not be adopted"*, **with no reason and no remedy**. An agent that cannot tell "the host is out of memory" from "something went wrong" **retries**, which is precisely the runaway this ruling accepts the risk of. The memory arm must state the cause and a remedy that exists (*close tabs or browsers you are done with, or wait — the host is out of memory*), and must **not** name a limit or a config key, because none exists. FR-053's launch-refusal error carries the same code | US-15/AC15 | memory-refusal-is-legible-to-the-agent | `TestTools_MemoryRefusalMessageNamesMemoryAndARemedy`, `TestTools_NoAdoptRefusalFallsToDefaultArm` | **D1.5a**; defect found by the D2 spec |

| **FR-064** | **The Darwin memory reader — in scope, and it must work (ADR D1.5b).** A new `pkg/config/meminfo_darwin.go`, sibling to `meminfo_linux.go`, implementing `readMemAvailableBytes()` and `readMemTotalBytes()`; `meminfo_other.go`'s constraint narrows from `//go:build !linux` (`:5`) to `//go:build !linux && !darwin`, and `meminfo_other_test.go`'s narrows with it. **Pure Go, no CGo, no new dependency — verified:** `golang.org/x/sys` is already direct at v0.47.0 (`go.mod:200`, no `// indirect`), 20 files under `pkg/`/`cmd/` already import `golang.org/x/sys/unix`, and `SysctlUint32`/`SysctlUint64`/`SysctlRaw` are built for Darwin (`unix/syscall_bsd.go:5,433,454,471`). **Total is `hw.memsize`** — not `vm.pages × pagesize`, which excludes firmware/kernel-reserved pages. **Available is an APPROXIMATION of Linux's `MemAvailable`, not the same measurement**, assembled from `vm.page_free_count` + `vm.page_purgeable_count` + `vm.page_speculative_count` + `vm.page_pageable_external_count` (all verified present; `vm.page_active_count`/`inactive`/`wire_count` are **not** sysctls — they are Mach `host_statistics64` fields, so the formula cannot mirror `vm_stat`). **Whether `page_pageable_external_count` already includes speculative pages is a per-release kernel detail the implementation MUST determine and cross-check against `vm_stat` on a real host, not guess** — the two readings bracket a 12 % spread (§12 A26, SC-023). **The formula, its exact terms, and the memory-compression and purgeable-page caveats are documented in the reader's own doc comment**, so a reader comparing platforms concludes "documented approximation" rather than "one of these is broken". **⚠ This reader has one existing consumer outside the browser** — `autoDetectMaxParallel` (`config.go:614-618`). **The 2 → 2000 consequence this row used to carry is DISSOLVED (D1.5c/D1.5d, §0.6b): that consumer is deleted by FR-067**, so a real macOS figure moves no default anywhere. Nothing about this FR changes — **the reader is written either way, and it was never blocked on its consumer set** | US-15/AC16 | darwin-memory-reader-is-tied-to-the-machine | `TestReadMemTotalBytes_Darwin_MatchesHwMemsize`, `TestReadMemAvailableBytes_Darwin_StrictlyInsideZeroAndTotal`, `TestMeminfoDarwin_FormulaIsDocumentedAtCallSite` | **D1.5b**, §0.6a |
| **FR-065** | **The undeterminable case REFUSES; it never admits.** The availability accessor `pkg/config` exports for FR-057 is two-valued — `(bytes uint64, ok bool)` — because `availableRAMBytes()`'s bare `uint64` (`pkg/config/config.go:655-661`) **cannot distinguish `0` from "unknown"**, and `0` is precisely today's unknown sentinel off Linux. Where `ok` is false the pool **refuses to grow at BOTH gates** — launch (FR-057) and tab open (FR-060) — with FR-053's error and FR-063's reason code, and logs *"memory availability cannot be determined on this platform"* **once per process, not per call**. **`ok=false` also covers Linux's fallback constant**: an unreadable `/proc/meminfo` yields `fallbackTotalRAMBytes / 2` = a fabricated 2 GiB (`meminfo_linux.go:16,26-30,40-45`), which would read to the gate as *"launch away"* — the same MAJOR-2 shape `meminfo_other.go:15-23` records fixing off Linux while it survives on it. **The clause this row used to carry here — *"`autoDetectMaxParallel` is deliberately unchanged and keeps consuming the fallback"* — is REMOVED by D1.5c/D1.5d:** that function is deleted (FR-067) and the single-mechanism ruling forbids the two-jobs separation it rested on. **This FR's scope is unchanged and stays browser-side**; what the *agent* path does on an `ok=false` host is FR-068's un-ratified branch (E-8), not this row's. **Past the floor, an unmeasurable host is treated as FULL, not empty** — a deliberate inversion, because a gate that answers "room available" because it has nothing to read reports success while admitting without limit. **"Grow" is growth from FR-082's floor of one browser and one tab** — that boundary is FR-082's, not this row's, and until round-4 C-401 this row read as refusing the first launch while four other places read the first as succeeding | US-15/AC17 | unmeasurable-host-refuses-to-grow | `TestPool_UnmeasurableHostRefusesToGrow`, `TestManager_UnmeasurableHostRefusesTabOpen`, `TestPool_UnmeasurableRefusalIsLoggedOnce` | **D1.5b**, §0.6a |
| **FR-066** | **Windows is foreseen and DECLARED, not defaulted through.** Windows keeps returning the unmeasurable signal, so under FR-065 the gate refuses there and **Windows browser support is specified as `degraded-unsupported` for the pool** until a reader exists. Consistent with the platform's existing posture (no sandbox backend — `selectBackendPlatform` returns `FallbackBackend`, `pkg/sandbox/sandbox_other.go`; `fileutil.WithFlock` a documented no-op, `pkg/fileutil/flock_windows.go`; `pidAlive` unconditionally true) — **and consistency is not a reason for silence.** Three artefacts, none optional: (1) a **`pkg/config/meminfo_windows.go` placeholder** of its own — not a shared non-Linux stub — returning `ok=false` explicitly, its doc comment naming the fix route (`GlobalMemoryStatusEx` via `NewLazySystemDLL`, `golang.org/x/sys/windows/dll_windows.go:234,249`; **x/sys v0.47.0 wraps neither that call nor `MEMORYSTATUSEX` — verified by grep over the module's `windows/` directory**, which is why Windows is separate work rather than the Darwin job repeated); (2) a **release-note line**; (3) a **config-documentation line** in the browser section. *The placeholder is the artefact that matters most: it puts the gap where someone would fix it, rather than only in a spec they are not reading at that moment* | US-15/AC18 | windows-gap-is-declared | `TestMeminfoWindows_ReportsUnmeasurableExplicitly`, `TestDocs_WindowsBrowserGapIsDocumented` | **D1.5b**, §0.6a |
| **FR-067** | **`bytesPerAgent` and the computed default are DELETED, and the accessor becomes two-valued (ADR D1.5d; scope signed off `ddd9789a4`).** Remove from the code: `bytesPerAgent` (`pkg/config/config.go:608`) with its doc block (`:588-607`); `autoDetectMaxParallel` in full (`:614-618`); `clampParallel` and its `autoDetectFloorParallel` (`:557-566`). **`clampParallelExplicit` (`:486`) and `physicalConcurrencySafetyCeiling` (`:586`) are NOT deleted** — the first serves the surviving explicit-operator path, the second is a *physical* OS-thread bound (`:568-586`: Go aborts past 10,000 threads; ~999 threads measured at ~1000 fsyncing goroutines; 5× margin) that memory availability says nothing about. **`EffectiveMaxParallelAgents` becomes `(n int, capped bool)`** (`:454-467`): explicit config or env → `(clampParallelExplicit(v), true)`; unset → `(physicalConcurrencySafetyCeiling, false)`. **A bare `0` sentinel is forbidden and the reason is mechanical, not stylistic:** it deadlocks `newDispatchSemaphore` (`pkg/agent/task_executor.go:250-260`) and is schema-invalid on the wire (`PerformanceSettings.yaml:12,29`, `minimum: 1`) — the MAJOR-3 shape `pkg/gateway/rest_performance.go:55-61` records already fixing once. **29 non-test call sites and 54 test-side uses across 9 files migrate in the same commit**; the tests and documented defaults that assert the deleted formula are enumerated in §0.6b and are deliverables, not collateral | US-15/AC19 | no-computed-default-remains | test 85 — `TestEffectiveMaxParallelAgents_UnsetIsBackstopNotCapacity`, `TestNoResidualPerAgentConstant` | **D1.5d**, §0.6b |
| **FR-068** | **Agent admission consults the SAME live headroom gate as the browser pool — one mechanism, two consumers (ADR D1.5c + D1.5d).** The gate is FR-057/FR-060's, unchanged: the same exported two-valued accessor (FR-065), the same pressure ratio, the same log-once discipline, the same FR-063 reason code. **Shape 2 — no per-unit cost — is chosen, and §0.6b states the four reasons**; the decisive one is that shape 1 cannot attribute (Chrome is a *child* process, absent from this process's footprint and present in the host figure the gate reads, so a derived per-agent figure charges browser memory to nothing). **No per-agent byte constant may reappear under any name**, and **no second threshold constant may exist** — two numbers that agree today are two mechanisms tomorrow, which is precisely what D1.5c ruled against. The exposure shape 2 accepts (the reserve absorbs the marginal agent) is bounded by the browser gate itself: the ~500 MB Chromium tail (`config.go:602-607`) cannot arrive unmetered, because an agent reaching for a browser passes FR-057 and FR-060 first | US-15/AC20 | one-gate-two-consumers | test 86 — `TestAgentAdmission_UsesSameAccessorAndThresholdAsPool`, `TestAgentAdmission_NoPerAgentConstantInPath` | **D1.5c**, **D1.5d**, §0.6b |
| **FR-068a** | **An unmeasurable host holds at the FLOOR and refuses to GROW — it does not refuse to run.** Where the accessor reports `ok=false` (Windows per FR-066; Linux fallen through to `fallbackTotalRAMBytes/2`, `meminfo_linux.go:16`), agent admission admits up to **2** concurrent agents and refuses beyond, naming memory. **This is FR-065 read the way §13 holdout 24 already reads it for the browser** — *the first browser opens; growth is refused* — applied to agents, where growth means concurrency above the floor. **Taken word-for-word instead, FR-065 would refuse every agent turn on Windows and on `/proc`-less Linux**, which stops the product rather than degrading it, and no browser ruling authorises that. **2 is a FLOOR, not a computed default:** nothing is divided, no per-unit constant survives, nothing is fixed at boot — and it is the posture `meminfo_other.go:25-33` already documents for these hosts, chosen because it fails conservative rather than open. **The refusal is the assertion**; an "admits when free" test passes against a stub that always admits | US-15/AC21 | unmeasurable-host-holds-at-the-floor | test 87 — `TestAgentAdmission_UnmeasurableHoldsAtFloorAndRefusesThird`, `TestAgentAdmission_UnmeasurableStillServesATurn` | **D1.5d**, §0.6b, §0.5 E-8 |
| **FR-069** | **The announcement is corrected, and the UI stops recommending a backstop.** Three artefacts. **(1) Release note:** *"there is no longer a computed default for `performance.max_parallel_agents`"* — and **not** *"the macOS default moves 2 → 2000"*, which is what §0.6a and ADR D1.5c both prescribed before D1.5d dissolved it. Nothing jumps, because nothing is precomputed. **(2) Config documentation** for that key, saying the same, plus FR-068a's floor on an unmeasurable host. **(3) The SPA**: `src/components/settings/PerformanceSection.tsx:218-229` presents `effective_max_parallel_agents` to the operator under the words *"Live system recommendation"*, with a comment naming the deleted formula verbatim (*"availableRAM / ~3.5 MB per concurrent agent, floored at 2 — see autoDetectMaxParallel"*). **Left alone it would recommend 2000 to every operator, in the UI, as a number the system chose for them** — the "displayed number is not the constraint" defect, shipped by inaction. The `capped=false` case renders **"automatic — bounded by available memory"**; the `capped=true` case is unchanged. This is a contract-and-SPA change (`contracts/components/schemas/PerformanceSettings.yaml:10-16, 27-29`) and it is inside this deliverable | US-15/AC22 | no-computed-default-is-what-is-announced | test 88 — `TestDocs_NoComputedDefaultIsAnnounced`, `PerformanceSection.autoDefault.test.tsx` | **D1.5d**, §0.6b |
| **FR-070** | **Acquisition of the operator's shared tab is IMPLICIT and has no surface (ADR D1.9b ruling 1).** An agent **acts on** `TabOwnerWorkspace()` by executing a `controlledResult`-gated tool against it, and **the tab is still the workspace's afterwards** — **no tool, no policy entry, no wire field, no result key** announcing a transfer. *(This row said "becomes the driver of" until round-5 M-507. §0.7 decided to DELETE the term at all six sites — it has no definition and, under this very requirement, no possible representation; five sites were swept and the normative FR row was not.)* The tool surface stays **11 → 17**, not 18; Hard Constraint #6's per-agent policy enumeration is unchanged; the Tier-3 fixture and manifest counts are untouched. A structural assertion forbids a `browser_take_control`-shaped registration ever appearing in `pkg/tools/browser`. **§14 is unchanged in shape** — its scope note's *"assumes implicit acquisition"* becomes *"is ruled"* and nothing else moves | US-22/AC5 | operator-tab-is-taken-by-acting | `TestTabs_OperatorTabIsAcquiredByActing`, `TestRegister_NoTakeControlTool` | **D1.9b ruling 1** (closing §0.5 E-1) |
| **FR-071** | **The control lock gates implicit acquisition, and the BLOCKED case is what is asserted.** Implicit acquisition may occur **only when no human holds the live-view lock** — `controlledResult` (`pkg/tools/browser/tools.go:962`) defers the agent while a human drives (ADR-038 D6), and §14.2 rule 1's ordering (`controlledResult` **before** `leaseWrite`) is the mitigation, not housekeeping. **One test, four cases:** blocked; blocked with the lock asserted against the **resolved** key (FR-002c — a `controlledResult` still asking `IsControlled(defaultSessionID)` at `tools.go:963` returns `false` forever and makes the blocked case pass vacuously); allowed; and ordering (the reason text is D6's, not the lease's, when both gates would fire). **Falsifiability receipt: the test must be RED against a build where `IsControlled` always returns `false`** — the allowed case alone is green on a build with no lock at all | US-9/AC6 | implicit-acquisition-is-blocked-while-a-human-drives | `TestImplicitAcquisition_BlockedWhileHumanControls` | **D1.9b ruling 1**, ADR-038 D6 |
| **FR-072** | **Periodic profile cache trimming (ADR D1.9b ruling 4).** A path under `<profileRoot>/ws-<id>/` is trimmable **iff the browser wrote it as a performance cache of data it can re-fetch or re-derive, and no site wrote it through a web storage API** — §0.8 derives the closed allow-list from that criterion. **Allow-list-driven, never deny-list-driven:** an unrecognised directory is KEPT. **Three triggers:** immediately after `pool.Close(k)` (the load-bearing one), at boot over every profile with no live Chrome, and on `tools.browser.cache_trim_interval` (default **1h**, reload-applied, a *reasoned* default and labelled as one). **Never against a live profile** — eligibility is FR-042a's discriminator: no live instance for the key **and** the per-key launch lock acquirable (`pkg/tools/browser/coordinator.go:1442-1483`). One INFO per pass naming key and bytes reclaimed. The profile **directory** is never removed here; that stays FR-043a's single trigger | US-25/AC1+AC4+AC5+AC6 | cache-trim-removes-only-regenerable-data, trim-fires-on-close-and-on-the-sweep, trim-never-touches-a-live-profile | `TestTrim_RemovesAllowListOnly`, `TestTrim_FiresOnCloseBootAndSchedule`, `TestTrim_SkipsLiveProfile` | **D1.9b ruling 4** (closing §12 A24(b), §16 MAJ-111) |
| **FR-073** | **The protected set survives the trim — proved behaviourally AND structurally.** Behavioural: a real-Chrome test logs into a site, closes the key, trims, relaunches and asserts **still logged in** (AC3). Structural: the allow-list contains **no** path under the protected set (`Cookies`, `Network/Cookies`, `Login Data*`, `Local Storage`, `Session Storage`, `IndexedDB`, `Service Worker/CacheStorage`, `Service Worker/Database`, `Preferences`, `Web Data`, `Trust Tokens`, `Local State`), and the implementation is allow-list-driven — a fixture directory the code has never seen is **kept**. *An AC1-only test passes against an implementation that deletes the whole directory, which is the outcome this FR exists to prevent* | US-25/AC2+AC3+AC6 | cache-trim-removes-only-regenerable-data, login-survives-the-trim | `TestTrim_ProtectedSetIsByteIdentical`, `TestTrim_LoginSurvivesRelaunch`, `TestTrim_AllowListContainsNoProtectedPath` | **D1.9b ruling 4** |
| **FR-074** | **The continuously-driven residual is DECLARED, not defaulted through.** A workspace under continuous drive never closes, so it is never eligible and its cache is not bounded. Three artefacts, on the FR-066 pattern: **(1)** the config doc for `tools.browser.cache_trim_interval` states it and does **not** imply the interval bounds it; **(2)** a release-note line; **(3)** an operator-visible log. **Escalated as §0.5 E-9** — the two candidate fixes (`--disk-cache-size`, whose value nobody has measured; or a mid-session trim, which requires closing a browser in use) are design changes, not tuning | US-25/AC7 | the-continuously-driven-case-is-declared | `TestDocs_ContinuousDriveGapIsDocumented` | **D1.9b ruling 4**, §0.5 E-9 |
| **FR-075** | **One `ok=false` predicate, TWO consumer responses, asserted as a pair (operator ruling 2026-09-01).** D1.5c ruled one **mechanism**, not one **response**: from a single stubbed accessor, the **browser pool refuses to grow** (FR-065 — correct everywhere, **Windows included, and accepted there because Windows is not yet a supported platform**, not on a technical argument) while **agent admission holds at the conservative floor of 2 and refuses the third** (FR-068a — existing shipped behaviour, whose rationale and the 585-agent fail-open regression it replaced are recorded in `pkg/config/meminfo_other.go:20-33`). **Both halves in one test off one stub**, because either alone is green on a build that collapsed them. Plus a doc assertion that the release note distinguishes **Windows** (unsupported) from a `/proc`-less **Linux** host such as gVisor (**supported**, reaching the same predicate through `pkg/config/meminfo_linux.go`'s fallback, same response) | US-15/AC23 | one-predicate-two-responses | `TestUnmeasurable_PoolRefusesWhileAgentsHoldAtFloor`, `TestDocs_WindowsAcceptedGVisorSupported` | operator ruling 2026-09-01, D1.5c |
| **FR-076** | **Containerisation is detected INDEPENDENTLY of the memory limit (ADR D1.5e).** A predicate in `pkg/config`, with test-overridable seams on the existing `procMeminfoPath` / `cgroupRoot` / `dockerenvPath` pattern, answering "am I in a container?" **without reading `memory.max`, `memory.limit_in_bytes` or `/proc/meminfo`**. Signals: an explicit operator override env var; `KUBERNETES_SERVICE_HOST`; `/.dockerenv`; a container path in `/proc/self/cgroup` (`kubepods`, `docker`, `lxc`, `containerd`). **`/.dockerenv` alone is explicitly insufficient and reusing `isRunningInDocker` (`pkg/gateway/sandbox_apply.go:185-201`) is explicitly rejected** — Kubernetes runs containerd/CRI-O and drops no such file, so that predicate answers false in the one deployment the requirement exists for. **A bare cgroup-v2 `0::/` is NOT taken as containerisation** (ambiguous; see §12 A29 for the residual it leaves and the override that covers it) | US-15/AC24 | containerisation-is-detected-without-the-limit | `TestContainerDetect_PodDockerAndBareMetal`, `TestContainerDetect_DoesNotConsultTheLimit` | ADR D1.5e |
| **FR-077** | **The node-memory WARN fires in the containerised-and-unlimited case and is SILENT everywhere else (ADR D1.5e).** When the process is containerised (FR-076) **and** no cgroup memory limit is found, log **once at startup** a WARN naming the condition (*no container memory limit set; sizing against node memory*) and the remedy (`resources.limits.memory`). **A WARN, never a refusal** — a bare-metal Linux host also has no cgroup limit and is perfectly correct, so refusing would break the ordinary case. **Three assertion directions, and the two silent ones are load-bearing:** fires for containerised + no limit; **silent** for containerised + limit; **silent** for bare metal + no limit. *A test asserting only the firing case is green on a build that warns unconditionally, which D1.5e names as worthless* | US-15/AC25 | node-memory-warning-fires-only-when-it-means-something | `TestNodeMemoryWarn_FiresOnlyInAnUnlimitedContainer` | ADR D1.5e |
| **FR-078** | **The Linux reader stops FABRICATING and reports undeterminable (not in D1.5e; found verifying it).** `readMemAvailableBytes` (`pkg/config/meminfo_linux.go:40-45`) returns `readMemTotalBytes() / 2`, and `readMemTotalBytes` (`:26-31`) returns `fallbackTotalRAMBytes` = 4 GiB (`:14-16`) when `/proc/meminfo` cannot be read — so an unreadable `/proc/meminfo` yields **an invented 2 GiB reported as determinable**, defeating FR-065 and FR-068a on that path and reproducing the exact fail-open `pkg/config/meminfo_other.go:15-33` condemned and fixed for macOS (**the same 585**: `2 GiB / bytesPerAgent 3.5 MB`, `pkg/config/config.go:608`, `:614-618`). **Requirement:** an unreadable or unparseable `/proc/meminfo` reports **undeterminable**; `fallbackTotalRAMBytes` is deleted as a symbol; **the pre-3.14 `MemTotal`-real / `MemAvailable`-absent heuristic is PRESERVED** and stays determinable | US-15/AC26 | unreadable-meminfo-is-undeterminable-not-two-gigabytes | `TestReadMemAvailable_UnreadableMeminfoIsUndeterminable`, `TestReadMemAvailable_PreMemAvailableKernelStillHalvesRealTotal`, `TestNoSymbol_FallbackTotalRAMBytes` | this spec, verified 2026-09-01 |
| **FR-079** | **One undeterminable signal does not discard the other.** `availableRAMBytes` (`pkg/config/config.go:655-661`) today takes `min` as `if ok && cgAvail < avail`; once FR-078 makes the meminfo half `0`-for-undeterminable, `cgAvail < 0` is never true and **a valid cgroup reading is thrown away**, turning a fully measurable container (unreadable `/proc/meminfo` **with** `limits.memory` set — a GKE Sandbox pod) into an `ok=false` host held at the floor of 2. **Requirement:** minimum over the **determinable** signals only; `ok=false` **only** when neither is determinable; both-determinable returns the smaller, unchanged. *Conservative rather than fail-open, so not a safety defect — but a regression FR-078 introduces if left unspecified* | US-15/AC27 | one-dead-signal-does-not-discard-the-other | `TestAvailableRAM_UndeterminableHalfDoesNotDiscardCgroup`, `TestAvailableRAM_BothUndeterminableIsNotDeterminable` | this spec, verified 2026-09-01 |

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

**The three FRs D1.9c and C-401 add all have BDD scenarios and tests, so none joins this list either** — **FR-080** (*session-tabs-are-the-chats-operator-tab-is-the-workspaces* and *two-sessions-never-contend*, tests 97–98), **FR-081** (*two-turns-in-one-session-contend-for-the-lease* and the cross-session half of *two-sessions-never-contend*, test 99), **FR-082** (*unmeasurable-host-refuses-to-grow*, test 83). **FR-081 carries a mutation receipt (SC-028)** and FR-082's scenario asserts both directions of its floor, because each half alone is green on a build that got the other half wrong.

**The three FRs D1.5b adds also all have BDD scenarios and tests, so none joins this list** — FR-064 (*darwin-memory-reader-is-tied-to-the-machine*, test 82), FR-065 (*unmeasurable-host-refuses-to-grow*, test 83), FR-066 (*windows-gap-is-declared*, test 84). **FR-064's test is platform-gated (`//go:build darwin`), which is not the same as exempt:** §10 states the platform it must be RUN on and SC-023 requires the receipt, precisely because this repo's CI is Linux-only and a `darwin`-tagged test that is never executed is the failure mode `meminfo_other_test.go:10-21` already documents about itself.

**The four FRs D1.5c/D1.5d add also all have BDD scenarios and tests, so none joins this list either** — FR-067 (*no-computed-default-remains*, test 85), FR-068 (*one-gate-two-consumers*, test 86), FR-068a (*unmeasurable-host-holds-at-the-floor*, test 87), FR-069 (*no-computed-default-is-what-is-announced*, test 88). **Two of the four carry a gate CI cannot fully close, and each says which.** Test 88's SPA half runs under vitest and is fully mechanical; its **doc half is a text assertion over the release note and config documentation**, which only exists once someone writes those artefacts — so a green CI before they are written proves nothing, and **SC-024's condition (4) is where that is caught**. Test 87's Linux fixture (the `fallbackTotalRAMBytes/2` fall-through) **is** reachable on CI; its Windows fixture is not, for the same reason FR-066's is not — this repo's CI is Linux-only. *Neither is an excuse to skip: both are fake-accessor tests, so the platform is simulated, not required. What CI cannot verify is the two written artefacts, and that is a human gate, stated as one.*

**The five FRs D1.5a adds all have BDD scenarios and tests, so none joins this list** — FR-059 (*counters-are-gone*, test 78), FR-060 (*runaway-tab-loop-is-stopped-by-memory*, test 79), FR-061 (*idle-close-actually-closes* / *gate-cannot-vacuously-pass*, test 80), FR-062 (*no-constant-in-the-capacity-path*, tests 51/74), FR-063 (*memory-refusal-is-legible-to-the-agent*, test 81). **The five it tombstones leave rows with `—` in every column**, matching FR-039's and FR-046's shape: a tombstone is not an exemption, and a reader must be able to tell "deleted by ruling" from "not verifiable".

**The six FRs D1.9b and the 2026-09-01 Windows ruling add also all have BDD scenarios and tests, so none joins this list either** — FR-070 (*operator-tab-is-taken-by-acting*, test 89), FR-071 (*implicit-acquisition-is-blocked-while-a-human-drives*, test 90), FR-072 (*cache-trim-removes-only-regenerable-data* / *trim-fires-on-close-and-on-the-sweep* / *trim-never-touches-a-live-profile*, tests 91–92), FR-073 (*login-survives-the-trim*, tests 91–92), FR-074 (*the-continuously-driven-case-is-declared*, test 92), FR-075 (*one-predicate-two-responses*, test 93). **FR-074's scenario is a doc assertion and FR-070's is a happy path that cannot fail for a safety defect** — both say so in place, and neither is offered as the guard on its ruling.

**The four FRs ADR D1.5e and this pass's verification add also all have BDD scenarios and tests, so none joins this list either** — FR-076 (*containerisation-is-detected-without-the-limit*, test 94), FR-077 (*node-memory-warning-fires-only-when-it-means-something*, test 95), FR-078 (*unreadable-meminfo-is-undeterminable-not-two-gigabytes*, test 96), FR-079 (*one-dead-signal-does-not-discard-the-other*, test 96). **Two of the four have a doc obligation that no Go test can discharge** — FR-077's config-doc line and the §12 A29 residual — and those sit in **SC-030** as PR-body conditions rather than being claimed as test coverage.

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
| 57 | `TestPool_StaleSingletonClearedPerKey` | Unit | FR-042b | A `SingletonLock` planted in `<profileRoot>/ws-W/` — the **flat** form (FR-037a) — is removed before W's launch; one planted in `cfg.ProfileDir` does **not** satisfy the assertion. *This row said `…/ws/W/` until this pass: revision 3's **nested** layout, which FR-037a replaced because nesting was the sole cause of INVARIANT P-5 (test 52 asserts the nested form is never constructed). A fixture written to the nested path plants the lock where the implementation will never look, so the test passes without the cleanup ever running.* |
| 58 | `TestPool_DeleteProfileOnWorkspaceDeletionOnly` | Integration | FR-043a | Profile removed on workspace deletion (after `Close` returns); **present** after idle close, **eviction**, roster change, reload and crash recovery — **four** negative cases, because the positive one alone would pass a "delete always" bug. *(Revision 3 had five; the operator-close case went with FR-046 and eviction takes its place — the more important of the two, since eviction is only acceptable because the profile survives it.)* |
| ~~59~~ | ~~`TestGateway_CloseWorkspaceBrowser` / `_Idempotent`~~ | — | ~~FR-046~~ | **DELETED with FR-046 (D1.7).** The REST path it exercises is withdrawn |
| 60 | ~~`TestPool_RefusalRemedyIsEffective`~~ → ~~`TestPool_OvershootIsExactlyOneTotal`~~ → **`TestPool_NothingEvictableWaitsThenRefusesNamingMemory`** | Integration | FR-053, FR-057, FR-063 | **Replaced a second time, by ADR D1.5a.** Round-2 replaced `TestPool_RefusalRemedyIsEffective` with an overshoot assertion when D1.7 removed the refusal; **D1.5a now removes the overshoot and brings the refusal back — but keyed to memory, not to a cap.** With the gate refusing and **every** instance pinned by a live viewer **and** an in-flight call: the call waits to its own deadline, then fails with an error naming the workspace and naming **memory**, carrying the `memory_pressure` reason code; `LiveKeys()` is **unchanged** — no instance is started past the gate, not even one. The test asserts the message names **no limit and no config key**. The pinned-everywhere setup is retained through all three revisions because it is the only state in which the refusal path is reachable at all |
| 61 | `TestPool_PreprovisionAtBootWithNoLiveKeys` | Unit | FR-016c | Resolution/download starts at boot with `len(LiveKeys()) == 0` and no `*BrowserManager` in existence |
| 62 | `TestResolveBrowsingKey_RejectsNonSegmentWorkspaceID` | Unit | FR-037 | `../`, `a/b`, `.`, `..` and an empty id are refused as `ErrNoBrowsingContext` before any path is built |
| 63 | `TestPool_ConcurrentAcquireUnderPressure` | Unit (fake launcher, `-race`) | FR-050, FR-053, FR-057 | **Re-derived: the boundary is the gate, not a target.** *(a) evictable path:* two goroutines `Acquire` **different** keys while the gate refuses and the LRU is idle — **both succeed**, the LRU is evicted exactly once, and **the gate is re-asked between them**, so the second never launches on a stale reading. *(b) all-pinned path:* the same with every instance pinned — **neither grows the pool**, both wait to their deadlines, and both are refused naming memory; `LiveKeys()` is unchanged. *(Revision 4's path (b) asserted `LiveKeys()` reaches exactly `target+1` with one WARN and **no refusal**. D1.5a deletes the `+1` overshoot and rules the gate a hard stop, so that assertion is now the inverse of correct — this is the second time this row's boundary assertion has had to be inverted by a ruling, which is why it names the gate rather than a number.)* |
| ~~65~~ | ~~`TestTabs_TwoAgentsDoNotMerge`~~ → **tests 97–99** | — | ~~FR-048~~ → FR-080, FR-081 | **RE-KEYED by ADR D1.9c.** `TestTabs_TwoAgentsDoNotMerge` is tombstoned: two agents in one **session** now share a tab set by design, so its assertion is false half the time and its Given never said which half. **`TestTabs_WorkspaceOwnedSetIsVisibleToAll` survives unchanged** and moves to test 97 — the workspace-owned set is exactly what D1.9c leaves alone |
| ~~66~~ | ~~`TestMaxTabs_IsPerAgentNotPerWorkspace`~~ | — | ~~FR-049~~ | **DELETED by ADR D1.5a — tombstone, not a hole.** It drove `max_tabs=5` and asserted A's sixth tab is refused while B still opens five of its own. **`tools.browser.max_tabs` is deleted** (FR-059), so there is no cap to be per-agent. **The property it protected is not lost** — that tab *sets* stay per agent is test 65 (`TestTabs_TwoAgentsDoNotMerge`), which is about ownership and is untouched. Only the *capacity* half goes |
| 67 | `TestPool_EvictionSkipsViewer` / `TestPool_EvictionSkipsInFlight` / `TestPool_StaleViewerDoesNotPin` | Unit (fake launcher) | FR-050, FR-051, FR-052 | **The guards exercised where they can fail.** LRU has a live viewer ⇒ the **second**-LRU is evicted. LRU has a lease-**exempt** call in flight ⇒ second-LRU evicted, the call completes. A viewer silent past the liveness window ⇒ counted detached by both eviction and `CloseIdle`. *(Driving the guards only all-pinned cannot distinguish "the guard works" from "nothing was evictable".)* |
| 68 | `TestPool_EvictionRaceWithExemptCall` | Unit (fake launcher, `-race`) | FR-051 | A long lease-exempt read on the LRU instance, started concurrently with an `Acquire` of a new key at the target: the read completes and its instance is not closed. Exempt deliberately — the leased case is the one the lease would have covered anyway |
| 69 | `TestPool_UpgradeInheritsNoProfile` | Integration | FR-043b | A populated `profiles/default/` with a live cookie: two workspaces both come up **logged out**, neither `ws-<id>` profile is a copy of it, and `profiles/default/` is unmodified afterwards |
| 70 | `TestPool_BootWarmsOneInstanceNotN` | Unit | FR-016b | Four workspaces, warm defaults on: exactly one `LiveKeys()` entry and one capture pipeline; no resolvable default-agent workspace ⇒ zero and one INFO |
| 71 | `TestPool_ThrashWarnsOnce` + **`TestPool_ThrashWarnNamesMemoryNotACap`** | Unit (fake launcher, fake clock) | FR-054 | `2 × threshold` evict-reopen cycles inside one window ⇒ **exactly one** WARN, carrying the contending workspace ids, **memory** as the binding constraint, and a remedy string. The second test asserts the **absence** of any config-key name in that string — revision 4's remedy was *"raise `tools.browser.max_browsers`"*, and a WARN naming a deleted key is the exact defect SC-022 exists to catch, which prose review has already missed twice (round-2 CRIT-103, round-3 M-309) |
| 72 | `TestPool_PressureGateAt084_085_086` / **`TestPool_PressureGateIsHardWhenNothingEvictable`** / **`TestPool_PressureGateOffLinuxIsNotSilent`** | Unit (fixture-driven) | FR-057, FR-053, FR-061 | Fixture cgroup values at 0.84 / 0.85 / 0.86 across the boundary. **The pinned-and-pressured case is now ASSERTED** — refuse, naming memory — where revision 4 deliberately left it out because "refuse to grow" (D1.5 item 3) and "always evict-and-launch" (D1.7) gave opposite answers and a test picking one would have ratified a decision nobody made. **D1.5a rules it** (§0.5 E-2, now struck through), so the case that was unassertable is the one that now matters most. **The third test replaces `TestPool_PressureGateIsNoOpOffLinux`, inverts it, and is now RESOLVABLE rather than red-pending-a-ruling.** Revision 5 left it *"red until §0.5 E-6 is ruled"*; **D1.5b rules it** (§0.6a), so the test asserts a decided behaviour: on **macOS** the gate reads a real figure through the Darwin reader (FR-064) and answers the same three fixture ratios the same way Linux does; on **Windows** availability is undeterminable and the gate **refuses to grow** (FR-065). It **fails** if the pool grows on an unmeasurable reading, and it **fails** if the gate no-ops quietly — a "no-op" gate is not a degraded limit but **no limit** (FR-061) |
| ~~73~~ | ~~`TestPool_TargetIsDerivedFromMemoryFixtures` / `TestPool_CeilingClampsDerivedTarget`~~ | — | ~~FR-056~~ | **BOTH DELETED by ADR D1.5a — tombstone, not a hole.** They exercised `clamp((min(host_RAM, cgroup_limit) × 0.5 − gateway_reserve) / (FIXED_FLOOR + R×85MB + encoder_page), 1, operator_ceiling)` against fixture hosts. **Every term is gone or withdrawn** — `R` with FR-055, the 85 MB constant on measured evidence (30 MB → 327 MB, 11× in one snapshot), `FIXED_FLOOR`/`gateway_reserve` with FR-044's narrowing, `operator_ceiling` with FR-056 — and there is no target to derive. **Their fixture pattern survives** in test 51 and test 72, which read the same `meminfo_*_test.go` fixtures for the one check that remains |
| 74 | ~~`TestPool_LaunchArgvCarriesRendererLimit`~~ → **`TestPool_LaunchArgvHasNoRendererLimit`** / `TestPool_CrossSiteTabsGetDistinctRenderers` | Unit + Integration (real Chrome) | FR-062 | **The first test is INVERTED, not deleted:** every per-key launch argv must contain **no** `--renderer-process-limit` at any value. **The second is kept unchanged and now asserts something stronger** — two cross-site tabs in one workspace occupy distinct renderer processes (ADR criterion P8) for *every* tab rather than only those below a bound, because Chrome's default site-per-process isolation is retained in full. *This pair is the mechanical record that removing the flag was a security **improvement**: C-303 / C4 / C206 are dissolved, not mitigated (FR-055's tombstone).* |
| 75 | `TestAudit_EveryWriteClassCallIsRecorded` / `TestAudit_ReadOnlyCallsAreNotRecorded` / `TestAudit_EventNamesMatchViewerPattern` | Integration + Unit | FR-027, FR-058 | Ten write-class calls ⇒ ten events with workspace/agent/tool/host, **including the tenth**; five read-only calls ⇒ zero per-call events; every event name matches `^[a-z_]+$` and a dotted fixture name **fails** the assertion |
| 76 | `TestPool_ReconcileRefusesWhenLockHeld` | Unit | FR-042a | Marker pid alive **and** launch lock held ⇒ refuse to launch that key, name the other gateway, terminate nothing. The test that distinguishes "reconcile orphans" from "kill the neighbour" (ADR criterion P9) |
| 77 | ~~`TestConfig_MaxBrowsersDocSaysSoftTarget`~~ → **`TestConfig_NoBrowserCountKeyExists`** | Unit (doc assertion) | FR-059 | **Re-derived.** The old test required `max_browsers`' doc comment to admit it was a *soft* target with a `+1` hard bound — ADR criterion P14's rule that a field described as a hard limit which silently overshoots is its own defect. D1.5a satisfies P14 by the shortest route: **the field is gone, so it cannot lie.** The replacement asserts the absence — no `max_browsers`, no `max_tabs`, no `max_total_tabs` in `ToolsBrowserConfig`, and no doc string anywhere claiming a browser or tab bound exists |
| 78 | `TestNoResidualTabCap` / `TestConfig_MaxTabsKeyIsRejected` | Unit (repo-wide structural) | FR-059 | Repo-wide, **including `_test.go`**: zero hits for `MaxTabs`, `MaxTotalTabs`, `TryOpenTab`, `ReleaseTab`, `reservedTabs`, `reserveGlobalTab`, `releaseGlobalTab`, `maxTabsReachedErr`. A `config.json` carrying `tools.browser.max_tabs` or `max_total_tabs` is **rejected at load with a named error**, never silently ignored — a key that loads and does nothing tells an operator they have a limit they do not have. `totalTabCountLocked` must **still exist** (counting for FR-048's tab sets and the gate's telemetry), so the search is by symbol, not by substring |
| 79 | `TestManager_TabOpenChecksPressureAtAllFiveSites` / `TestManager_RunawayTabLoopIsRefusedNamingMemory` | Unit + Integration | FR-060, FR-063 | The gate is consulted at **all five** tab-open sites — `createFirstTab` (`manager.go:1139`), `OpenTab` (`:2005`, `:2047`), `adoptTarget` (`:2216`, `:2286`) — the same five the deleted cap was checked at. A loop opening tabs inside one already-running browser is **refused at tab *k+1*** once the fixture reading crosses the threshold, with the `memory_pressure` reason code. *This is the only thing standing between that loop and the OOM killer: it never reaches a launch decision, and no counter remains* |
| 80 | `TestReaper_FailsIfNothingIsEverClosed` / `TestPool_GateFailsIfItAlwaysAdmits` / `TestPool_IdleCloseIsNotBehindAFlag` | Unit | FR-061 | **Each fails if its control silently does nothing.** A sweep in which nothing is ever closed fails; a gate that answers "room available" for every fixture input fails; neither control is reachable behind a disabled flag, a "best effort" path or an off-by-default setting on a supported platform. *These exist because "the control ran and did nothing" is the exact false-green shape `docs/internal/false-green-patterns.md` catalogues, and under D1.5a a gap in either is not a weaker limit — it is no limit* |
| 81 | `TestTools_MemoryRefusalMessageNamesMemoryAndARemedy` / `TestTools_NoAdoptRefusalFallsToDefaultArm` | Unit | FR-063 | `applyReconcileOutcome` (`pkg/tools/browser/tools.go:321`, whose reason switch is `:346-356`) has a `memory_pressure` arm; its text names the host being out of memory **and** a remedy that exists, and names **no limit and no config key**. The second test asserts **no** adoption refusal reaches the `default:` arm — *"it could not be adopted"*, no reason, no remedy — which is where every memory refusal lands if `tabAdoptReasonMaxTabs` (`manager.go:2108`) is removed without a replacement. *An agent that cannot tell "out of memory" from "something went wrong" retries.* Defect found by the D2 spec |
| 82 | `TestReadMemTotalBytes_Darwin_MatchesHwMemsize` / `TestReadMemAvailableBytes_Darwin_StrictlyInsideZeroAndTotal` / `TestMeminfoDarwin_FormulaIsDocumentedAtCallSite` | Unit (`//go:build darwin`, **run on a real Darwin host**) | FR-064 | **The oracle is `hw.memsize`, read independently through `unix.SysctlUint64` in the test** — so total cannot be satisfied by a constant, and **not** `vm.pages × pagesize`, which is smaller (firmware/kernel-reserved pages: `8137872` vs `8388608` on the reference host). Available is asserted **strictly** inside `(0, total)` at both ends: *"returns non-zero" would pass against a stub returning `1`*, and *"≤ total" would pass against a reader returning `total`*. The third test asserts the doc comment names every sysctl summed and states the compression and purgeable caveats. **⚠ This repo's CI is Linux-only, so a `darwin`-tagged test is NEVER executed by it** — the exact gap `pkg/config/meminfo_other_test.go:10-21` documents about itself. **SC-023 requires the run receipt from a real Darwin host in the PR body**; cross-compilation proves it type-checks and proves nothing else |
| 83 | `TestPool_UnmeasurableHostRefusesToGrow` / `TestManager_UnmeasurableHostRefusesTabOpen` / `TestPool_UnmeasurableRefusalIsLoggedOnce` | Unit (fake accessor) | FR-065, FR-053, FR-063 | **The assertion is a PAIR — the floor admits and the growth refuses — and asserting only the refusal is the defect (round-4 C-401, FR-082).** With the accessor returning `ok=false`: the **first** launch and the **first** tab in it **succeed**, and the run **FAILS** if either is refused; the **second** launch is refused naming memory, the **second** tab open is refused with `memory_pressure`, and the run **FAILS** if either succeeds. *This row previously asserted the first launch was refused, which contradicted §13 holdout 24, US-15/AC23, SC-027 and FR-068a — four places reading the boundary the other way. Both halves are load-bearing: the refusal half alone is green on a build that refuses everything (which removes browsing from gVisor, a supported Linux deployment), and the admit half alone is green on a build with no gate at all. This test and test 93 now assert the same boundary and can no longer disagree.* The third test asserts the explanation is logged **exactly once per process**, and **fails** on both zero log lines and one-per-call. **Fixture inputs must include Linux's fallback path** (`/proc/meminfo` unreadable ⇒ `fallbackTotalRAMBytes/2` = a fabricated 2 GiB, `meminfo_linux.go:16`), not only the Windows platform case — otherwise the test proves nothing about the shape that actually reads as *"launch away"* |
| 84 | `TestMeminfoWindows_ReportsUnmeasurableExplicitly` / `TestDocs_WindowsBrowserGapIsDocumented` | Unit (structural + doc assertion) | FR-066 | `pkg/config/meminfo_windows.go` exists as its **own** file (not a shared non-Linux stub also serving BSD), returns `ok=false` explicitly, and its doc comment names `GlobalMemoryStatusEx` and the `NewLazySystemDLL` route (`golang.org/x/sys/windows/dll_windows.go:234,249`). The second test asserts the release-note line and the config-documentation line both exist and both say the pool has **no memory-derived limit on Windows** — *a doc assertion because the artefact IS documentation; the failure mode being guarded is an operator learning this from an OOM* |
| 85 | `TestEffectiveMaxParallelAgents_UnsetIsBackstopNotCapacity` / `TestEffectiveMaxParallelAgents_ExplicitPathUnchanged` / `TestNoResidualPerAgentConstant` | Unit (`pkg/config`, + repo-wide structural) | FR-067 | **The `capped` bool is the assertion, not the integer.** Unset must return `(physicalConcurrencySafetyCeiling, false)` and the test **fails if `capped` is true** — an implementation that returns the right number with the wrong flag ships the FR-069 defect intact. The second test pins the surviving path in both directions (config 40 → `(40, true)`; env 50 over config 8 → `(50, true)`), because a sweep that deleted `clampParallelExplicit` along with `clampParallel` would satisfy every assertion written only about the auto path. The third is repo-wide **including `_test.go`**: zero hits for `bytesPerAgent`, `autoDetectMaxParallel`, `clampParallel`, `autoDetectFloorParallel`. **Deleted with the formula, not adapted:** `TestEffectiveMaxParallelAgents_Auto_MatchesMemoryFormula` (`pkg/config/parallel_clamp_test.go:113-121`), `TestClampParallel_AutoFloorsAtTwo` (`:13-36`), `TestAutoDetectMaxParallel_NonLinux_DefensiveFloorNotFictitious` (`pkg/config/meminfo_other_test.go:48-55`), `TestAutoDetectMaxParallel_WarmPageCacheContainerDoesNotCollapseToFloor` (`pkg/config/meminfo_linux_test.go:288-311`) — §0.6b's table says which of the four are re-derived and which simply go |
| 86 | `TestAgentAdmission_UsesSameAccessorAndThresholdAsPool` / `TestAgentAdmission_NoPerAgentConstantInPath` | Unit (fake accessor) + structural | FR-068 | Drives **both** consumers off one stubbed accessor at the **same three fixture ratios test 72 uses** (0.84 / 0.85 / 0.86) and asserts the two return the **same** admit/refuse answer at each. **Sameness is asserted at the seam, not inferred from equal outcomes** — the accessor and threshold are injected once and both consumers are proved to have read that injection, so two independent constants that happen to agree cannot pass. The structural half fails on any per-agent byte figure in the admission path under any name, and on the existence of a **second** threshold constant. *A test that only checked "both refuse at 0.86" would pass against exactly the two-mechanism split D1.5c forbids* |
| 87 | `TestAgentAdmission_UnmeasurableHoldsAtFloorAndRefusesThird` / `TestAgentAdmission_UnmeasurableRefusalIsLoggedOnce` / `TestAgentAdmission_UnmeasurableStillServesATurn` | Unit (fake accessor) | FR-068a, FR-063 | **The REFUSAL is the assertion.** With `ok=false`, admissions 1 and 2 succeed and admission **3 must fail**, naming memory and carrying the `memory_pressure` code — the run **fails if the third succeeds**. Fixtures must include **both** `ok=false` shapes: the Windows platform case and Linux's `fallbackTotalRAMBytes/2` fall-through (`meminfo_linux.go:16`), since only the second is reachable on CI. The third test is the inverse guard and it is not redundant: it asserts an ordinary turn **completes** on such a host, so a change that made the gate refuse everything — passing the refusal test perfectly — is caught. *Two tests that can only fail in opposite directions are how "refuse to grow, not refuse to run" is made falsifiable at all* |
| 88 | `TestDocs_NoComputedDefaultIsAnnounced` / `PerformanceSection.autoDefault.test.tsx` | Unit (doc assertion + vitest) | FR-069 | The doc test asserts the release note and the config documentation **contain** *"no longer a computed default"* **and do NOT contain** any "2 → 2000" / "default moved" sentence — **the negative is the load-bearing half**, because that exact sentence is what §0.6a and ADR D1.5c prescribed before D1.5d dissolved it, and it is what a careless writer will copy forward. The vitest renders `PerformanceSection` with `capped=false` and asserts the panel shows **"automatic — bounded by available memory"** and that `2000` appears **nowhere** in the rendered output; then renders the explicit-40 case and asserts it still shows `40`. *Without the second render this test would pass against a panel that had stopped showing the operator's own setting* |
| 64 | `TeamAddAgent.disclosure.test.tsx` | Unit (vitest) | FR-047 | The disclosure renders before the confirm action, not after it |
| 89 | `TestTabs_OperatorTabIsAcquiredByActing` / `TestRegister_NoTakeControlTool` | Unit + Unit (structural) | FR-070 | The first drives a `controlledResult`-gated tool against `TabOwnerWorkspace()` through the **real registered tool path** with the lock free, and asserts the navigation **took effect** and `TabOwnerWorkspace()` **still owns** the tab afterwards, with **no acquisition call**. *Not "the agent is the driver": FR-070 forbids every representation of one, so that assertion could not fail (C-403).* The second enumerates the registry and fails on any tool whose name or schema matches a take-control shape. **Neither is a safety assertion** — test 90 is |
| 90 | `TestImplicitAcquisition_BlockedWhileHumanControls` | Unit | FR-071, FR-002c, FR-022 | **The mitigation, and the only test here that can fail for it.** Four cases in one test: (a) lock held on the resolved key ⇒ D6 deferral and `acquireWrite` **never called**, asserted at the seam; (b) the lock is asserted to have been consulted **against the resolved key**, so a `controlledResult` still asking `IsControlled(defaultSessionID)` (`tools.go:963`) fails here rather than passing vacuously; (c) lock free ⇒ the call proceeds; (d) lock held **and** the lease held elsewhere ⇒ the reason text is **D6's**, proving the gate order; **(e)** *(round-4 M-402)* with the lock **held**, `browser_handle_dialog` against `TabOwnerWorkspace()` **succeeds** — it is exempt from both gates by design — **and** the tab's owner is `TabOwnerWorkspace()` both before and after, with `acquireWrite` **never called**. *Case (e) makes the exemption's price visible: it is the one tool that reaches the operator's tab while a human is driving, and the assertion is that it clears the modal and changes nothing else. Without it, a change giving the dialog tool a leased tool's side effects passes (a)–(d) unaltered.* **Falsifiability receipt (SC-025): the test is run against a build with `IsControlled` forced to `false` and must be RED.** *Test 6 and test 18 are adjacent but neither covers this: test 6 asserts the key, test 18 asserts lease membership, and both are green on a build where an agent can walk onto a tab a human is driving* |
| 91 | `TestTrim_RemovesAllowListOnly` / `TestTrim_ProtectedSetIsByteIdentical` / `TestTrim_AllowListContainsNoProtectedPath` / `TestTrim_SkipsLiveProfile` | Unit (fixture profile tree, no Chrome) | FR-072, FR-073 | A fixture `<profileRoot>/ws-W/` carrying every allow-listed path, every protected path, **and one directory name the implementation has never seen**. Asserts: allow-listed gone; protected byte-identical; unknown **kept** (the allow-list-not-deny-list assertion); the directory itself still present. `SkipsLiveProfile` holds the per-key launch lock and asserts **nothing** is modified — *a trim that skipped for the wrong reason would still pass a "protected set intact" check, so this one asserts the allow-listed paths survive too* |
| 92 | `TestTrim_FiresOnCloseBootAndSchedule` / `TestTrim_LoginSurvivesRelaunch` / `TestDocs_ContinuousDriveGapIsDocumented` | Unit (fake clock) + Integration (real Chrome) + doc assertion | FR-072, FR-073, FR-074 | The trigger test drives all three paths and includes the case that separates them: **a run in which the schedule never ticks still leaves a `pool.Close`d key trimmed**, so a schedule-only implementation fails. `LoginSurvivesRelaunch` is the behavioural proof — log in, close, trim, relaunch, still authenticated — and is the assertion a file-level check cannot make. The doc test asserts the config doc, the release note and the log all name the continuously-driven gap, **and** that the config doc does not imply the interval bounds it |
| 93 | `TestUnmeasurable_PoolRefusesWhileAgentsHoldAtFloor` / `TestDocs_WindowsAcceptedGVisorSupported` | Unit (one fake accessor, both consumers) + doc assertion | FR-075, FR-065, FR-068a | **One stub, two assertions, in one test body** — the pool refuses a second instance **and** agent admission admits two turns and refuses the third, both naming memory. *Splitting them into two tests is the defect: each passes alone on a build that collapsed the responses.* The doc test asserts the release note records Windows' browser refusal as **accepted because Windows is not yet supported** (operator ruling 2026-09-01) and separately records a `/proc`-less **Linux** host as **supported** with the same response — grouping them as one platform class fails it. Extends test 86's one-accessor guarantee to the `ok=false` branch, which test 86 does not reach |
| 94 | `TestContainerDetect_PodDockerAndBareMetal` / `TestContainerDetect_DoesNotConsultTheLimit` | Unit (three fixture roots + fake env) | FR-076 | Three fixtures in one table: **pod** (`KUBERNETES_SERVICE_HOST` set, `kubepods` in `/proc/self/cgroup`, **no** `/.dockerenv`), **Docker** (`/.dockerenv` only), **bare metal** (neither, `system.slice` cgroup path). Expected **true / true / false**. *The pod row is the whole point — it is the row a `/.dockerenv`-based implementation fails, and the bare-metal row is the one that fails `return true`.* The second test stubs the cgroup-limit and `/proc/meminfo` readers to **panic if called** and asserts the predicate still answers, which is the only way to prove independence rather than infer it from outcomes |
| 95 | `TestNodeMemoryWarn_FiresOnlyInAnUnlimitedContainer` | Unit (captured `slog` handler, four fixtures) | FR-077, FR-076 | **Four cases in one test body, and three of them assert SILENCE.** containerised+no-limit ⇒ exactly one WARN carrying both the condition and `resources.limits.memory`; containerised+limit ⇒ **none**; bare-metal+no-limit ⇒ **none**; bare-metal+limit ⇒ **none**. Plus: startup **succeeds** in all four (this is not a refusal), and a second call in the same process emits nothing further (the "once" clause, which fails a per-request implementation). *Split the firing case out into its own test and it passes on a build that warns unconditionally — the failure mode D1.5e names by hand* |
| 96 | `TestReadMemAvailable_UnreadableMeminfoIsUndeterminable` / `TestReadMemAvailable_PreMemAvailableKernelStillHalvesRealTotal` / `TestNoSymbol_FallbackTotalRAMBytes` / `TestAvailableRAM_UndeterminableHalfDoesNotDiscardCgroup` / `TestAvailableRAM_BothUndeterminableIsNotDeterminable` | Unit (`//go:build linux`, fixture `procMeminfoPath` + `cgroupRoot`) | FR-078, FR-079, FR-065, FR-068a | **The pair is the assertion, in both halves of this row.** Reader: a missing meminfo file reports undeterminable — asserted as *not 4 GiB, not 2 GiB, not any constant* — **while** a `MemTotal`-only fixture still returns half of the **real** total and stays determinable; a change that flattened all fallbacks to undeterminable passes the first and fails the second. `TestNoSymbol_FallbackTotalRAMBytes` is a repo-wide grep assertion (`_test.go` included) so the constant cannot return under a caller nobody re-read. Combination: an unreadable meminfo **plus** a finite cgroup returns the **cgroup** figure, determinable; both-failed returns `ok=false`; both-succeeded returns the **smaller** — the third case is what fails a fix that inverts the min into a max. **Rewrites `TestReadMemAvailableBytes_MissingFile` (`pkg/config/meminfo_linux_test.go:55-65`), whose current oracle IS the defect** — see §10.1 |
| 97 | `TestTabs_TwoSessionsDoNotMerge` / `TestTabs_AgentSwitchWithinASessionSeesTheSameTabs` / `TestTabs_WorkspaceOwnedSetIsVisibleToAll` | Unit | FR-080 | **Three assertions, and the middle one is the ruling.** (a) Two **sessions** on one manager: two distinct `sessionEntry` values, neither's `tabs` slice containing the other's `tabEntry`, and a turn in S2 refused when it tries to switch to, drive or close S1's tab. (b) **One session, two agents:** Mia opens a tab, the turn is re-run as Jim on the **same** `transcriptSessionID`, and Jim's `browser_list_tabs` returns that tab and can drive it — *this is the assertion that is RED on a D1.9a build, and it is ADR §1.1's own conversation.* (c) The workspace-owned entry appears in both sessions' results, labelled. **(d) The empty-id case (round-5 M-501):** a turn whose `tools.ToolTranscriptSessionID(ctx)` is `""` gets **`ErrNoTabOwner`** from every browser tool — it does **not** open a tab, does **not** join another transcript-less turn's set, and does **not** fall through to `TabOwnerWorkspace()`. Both negative halves are asserted, because a build that quietly maps `""` to one shared owner passes (a)–(c) untouched. **Red today** in the sense that matters: written against a re-keyed manager with no `TabOwner`, all four fail |
| 98 | `TestTabs_OwnerKeyIsTranscriptNotRouting` | Unit | FR-080 | A delegation fixture: a parent turn and its child hold **different** `transcriptSessionID`s and the **same** `routingSessionID` (`pkg/agent/subturn.go:1282`, `:1339`). The child opens a tab; the parent's `browser_list_tabs` must **not** contain it. *An implementation keyed on `routingSessionID` passes every other tab test in this suite and fails only this one — which is why it is a separate test and not a case inside 97. It is also the only test that can catch the shape §0.2a warns about: N async delegates (`pkg/tools/delegate.go:1298`, `:1853-1856`) merged onto the root's tab set.* |
| 99 | `TestLease_TwoTurnsOneSessionSerialise` / `TestLease_TwoSessionsNeverBlockEachOther` / `TestLease_TakenOnSessionOwnerNotOnlyWorkspace` | Unit (`-race`, fake clock) | FR-081 | **The pair is the assertion, and the third test is the seam.** (a) Two turns on **one** `transcriptSessionID` both call `browser_navigate`: exactly one holds the lease at any instant, **both eventually complete**, neither is a Go error. (b) Two turns in **different** sessions: both complete and **neither waits** — asserted at the lease seam, so a lease that serialises everything fails here rather than passing (a) and looking correct. (c) `acquireWrite` is asserted **called** for a `TabOwnerSession()` write — the direct guard on FR-021's superseded trigger, and the one assertion a build carrying that trigger fails. **Falsifiability receipt (SC-028):** the suite is run against a build restoring the `TabOwnerWorkspace()`-only trigger and test (c) must be RED |

### 10.1 Regression requirements (MANDATORY — this change modifies shipped behaviour)

**Must keep passing, with SEMANTICS unmodified — read this bar before reading the list.**

The previous draft said "unmodified" and that was **unsatisfiable, at compile level** (round-2 CRIT-101). FR-002b **deletes** `DefaultSessionID` and `defaultSessionID`; four of the files below reference them **115 times** (`tab_adoption_e2e_test.go` 41, `idle_reaper_test.go` 33, `reaper_edge_test.go` 26, `reaper_lifecycle_test.go` 15), and repo-wide the test surface is **364 references across 25 files** (§2.2a). Those files do not fail an assertion when the constant goes — they fail to build, and take their whole package's test binary with them. "Keep passing unmodified" and "the constant is deleted" could not both hold, and the only way to honour both would have been a test-only alias, which re-creates the exact `"default"` constant SC-013 counts to zero and leaves the reaper suite asserting against a key nothing in production uses.

**The bar, therefore, is behavioural rather than textual.** For every file in this list:

1. The **only** permitted edit is mechanically re-pointing the session-id argument at a browsing key built through `newTestBrowsingKey`. No other line changes.
2. **No assertion is weakened, deleted, inverted, or guarded by `t.Skip`/`t.Short`.** Assertion count per file is unchanged or higher.
3. Test-function count per file is unchanged or higher.
4. The migration lands in the **same commit** as FR-002b (§2.2a item 4) — a commit that deletes the constant and defers the tests leaves `pkg/tools/browser` and `pkg/gateway` unbuildable, at which point every other gate's verdict is meaningless.
5. The reviewer checks the diff against 1–3 directly. A diff that touches anything else in these files is a finding, not a judgement call.

**Deliberately CHANGED semantics — one test, listed here so it is not mistaken for collateral damage.**

- `pkg/config/meminfo_linux_test.go:55-65` — **`TestReadMemAvailableBytes_MissingFile` asserts the defect.** Its oracle is `want := uint64(fallbackTotalRAMBytes) / 2` (`:61`): it passes today **because** the reader fabricates 2 GiB from an unreadable `/proc/meminfo`. FR-078 makes that value undeterminable, so this test must be **rewritten, not repaired** — the new oracle is *reports undeterminable*, and the constant it referenced no longer exists. **Its sibling `TestReadMemAvailableBytes_FallsBackToHalfTotal` (`:43-50`) keeps its semantics exactly** — a real `MemTotal` halved is a real estimate, and that is the case FR-078 preserves. *A green test whose expected value is the bug is the reason this is called out in its own subsection: a reviewer who sees only "a test changed" cannot tell the difference between this and a weakened assertion, and the two look identical in a diff.*

**Files (semantics unmodified):**

- `pkg/tools/browser/tab_adoption_e2e_test.go` — all **nine** ADR-041 tab tests (`:77`–`:569`). Tab-set semantics inside one browser are untouched. **41 references to migrate.**
- `pkg/tools/browser/shared_control_test.go` — **eight** tests (`:35, :55, :74, :92, :109, :138, :157, :186`), not nine as the previous draft claimed. **Correction that matters more than the count:** *this file is not the FR-022 guard.* None of its eight tests calls `controlledResult`; they exercise `LiveView` input dispatch, rate limiting and viewport failure streaks. A green result here proves nothing about the human control lock surviving the re-key.
- **`pkg/tools/browser/tools_control_test.go` — the actual `controlledResult` guard, and absent from the previous draft entirely.** Three tests: `TestExecute_ControlLock_InteractiveToolsDeferWhileControlled` (`:59`), `TestExecute_ControlLock_ReadOnlyToolsAreNotGated` (`:106`), `TestExecute_ControlLock_ReleaseUngatesInteractiveTools` (`:153`). These **must be re-run against the re-keyed lock** and must pass without weakening any assertion. If they need editing, FR-002c is wrong.
- `pkg/tools/browser/idle_reaper_test.go` (**33 refs**), `reaper_edge_test.go` (**26**), `reaper_lifecycle_test.go` (**15**) — FR-025 asserts per-tab reaping is not rewritten. (Whole-Chrome close is *new* behaviour tested separately, in tests 41/54/55, not a change to these.) **Note what these files already cover, which the previous draft's `ReapIdleSessions` claim implied they did not:** the reaper's own browser-context cancellation path (`manager.go:3027-3032`, `:3073-3078`, `:3123-3125`). FR-040a's new behaviour composes with that path; it does not replace it. **⚠ Its `releaseGlobalTab` → `coord.ReleaseTab` call (`manager.go:3118` → `:3358-3365`) is NOT coverage to preserve — FR-059 DELETES both symbols**, and test 78 asserts zero repo-wide hits for them, `_test.go` included. Any assertion in these three files that names either symbol goes with them, and its deletion is an executed ruling rather than the assertion-stripping rule 2 of the bar above forbids. *(Round-5 M-505: round-4's M-404 swept this correction through US-12/AC6 and the *reaper-cancels-while-pool-live* Given and stopped there, leaving §10.1 telling a reviewer to protect coverage of two deleted symbols.)*
- **The other 21 files in §2.2a's table** — `tabs_test.go` (100 refs), `switch_tab_same_index_recapture_test.go` (26), `focus_emulation_test.go` (24), `switch_tab_activation_test.go` (20), `switch_tab_capture_chain_test.go` (18), `browser_ws_test.go` (15) and the rest — are held to the same bar. They were entirely absent from the previous draft's regression list, which is how a ~364-edit item was budgeted at zero.
- `pkg/tools/browser/coordinator_test.go` — `TestCoordinator_OwnershipMarker_RoundTrip` (`:379`) only. **⚠ CORRECTED BY D1.5a — the other four moved from "must keep passing" to "must be deleted":** `TestCoordinator_UnlimitedDefault_AllowsPastOldCap` (`:429`), `TestCoordinator_PositiveCap_StillRejectsAtBoundary` (`:448`), `TestCoordinator_ConcurrentOpeners_PositiveCap_ExactlyOneWinner` (`:472`) and `TestCoordinator_SetMaxTotalTabs_ReloadRestoresUnlimited` (`:506`) all exercise `max_total_tabs` and the `TryOpenTab`/`ReleaseTab` reservation machinery, **which FR-059 removes from the code**. Revision 4's note that *"the tab budget is orthogonal to the browser cap and must not regress"* was correct then and is void now: **there is no tab budget.** They are listed under *Must be DELETED* below, so that the reviewer sees four disappearing test functions as an executed ruling rather than as the assertion-deletion this project treats as a finding.

**Must be rewritten, not extended:**

- `pkg/tools/browser/coordinator_test.go:154` `TestCoordinator_TwoAgents_OneChrome_TwoContexts` → `TestPool_TwoWorkspaces_TwoChromes`. Its per-agent assertion is now the wrong assertion, and its *"one Chrome"* premise is exactly what D1.4 replaces. Leaving it green while the model changed underneath is the `docs/internal/false-green-patterns.md` stale-green shape.
- `pkg/tools/browser/coordinator_test.go:203` `TestManager_Shutdown_DropsConnectionNotProcess` and `:244` `TestCoordinator_Shutdown_IsSoleKill` → re-scope to *"…for the key's own Chrome"*. Both encode the single-process model.
- `pkg/tools/browser/stress_5agents_test.go:267` `TestFiveAgents_ConcurrentStress` → **five agents on one workspace** (contention — the new normal case) **plus** five agents across five workspaces (isolation, admitted or refused by the **memory gate**, since there is no cap to bound them — D1.5a). Five agents on five implicit per-agent jars is no longer a scenario the product has.

**Must be DELETED, and this is a finding rather than a rename:**

- **The four `max_total_tabs` / reservation tests in `coordinator_test.go`** — `TestCoordinator_UnlimitedDefault_AllowsPastOldCap` (`:429`), `TestCoordinator_PositiveCap_StillRejectsAtBoundary` (`:448`), `TestCoordinator_ConcurrentOpeners_PositiveCap_ExactlyOneWinner` (`:472`), `TestCoordinator_SetMaxTotalTabs_ReloadRestoresUnlimited` (`:506`). Each asserts a behaviour of `max_total_tabs` and `TryOpenTab`/`ReleaseTab`, deleted by FR-059. **The third is the one to note in the PR body:** it is the in-flight-race test (*exactly one winner among concurrent openers*), and its subject — a reservation race — ceases to exist along with the reservation, rather than becoming untested. **Rule 2 of the bar above ("no assertion is deleted") does not cover these**: it governs migration of tests whose subject survives, and these four have no surviving subject. Deleting them without this paragraph is indistinguishable from assertion-stripping, which is why the paragraph is here.

- `pkg/tools/browser/coordinator_test.go:328` `TestCoordinator_Register_SharedContextMode_ReturnsRootCtxAndEmptyBrowserCtxID`. It asserts that `Register` returns an **empty** `browserCtxID` and that `coord.contextCount() == 0` — i.e. **it is the test that proves the isolation this spec promises did not exist.** The previous draft asked only to "re-key its Register argument", which would have left a green test asserting the absence of the product's headline guarantee. Under FR-031 the mode, the flag and `contextCount()` are all retired, so the test has nothing left to assert and is deleted with them. Its replacement is test 35, `TestNoCDPBrowserContextIsEverCreated`, which asserts the opposite property: no CDP browser context is created **anywhere**, because isolation is now the profile directory.

**One regression is OUTSIDE `pkg/tools/browser`, and it is the one most likely to be missed (D1.5b).** `pkg/config/meminfo_other_test.go` is gated `//go:build !linux` (`:1`) and tests `readMemAvailableBytes() == 0` (`:38-41`) and `readCgroupMemoryAvailableBytes() ok == false` (`:61-64`). **FR-064 makes the first assertion FALSE on Darwin**, so the file's build constraint must narrow to `!linux && !darwin` **in the same commit as `meminfo_other.go`'s** — otherwise the package stops compiling on macOS, and this repo's Linux-only CI would not notice. **The second assertion stays true everywhere** (cgroups are Linux-only) and must be preserved for Darwin by test 82's file, not dropped. *(**Round-5 M-504: a "keep it as it is" sentence about `pkg/config/parallel_clamp_test.go:111-120` stood here and is DELETED.** It told the reviewer that test must keep deriving its expectation from `availableRAMBytes()`, six lines above the list below that requires the **same** test — `TestEffectiveMaxParallelAgents_Auto_MatchesMemoryFormula`, `:113-121` — to be **deleted with the formula**. Two opposite instructions for one test. Its justification was stale twice over: **§0.5 E-7 is RULED**, and **FR-067 deletes `autoDetectMaxParallel`**, the function whose default the sentence was protecting. The delete instruction is the surviving one.)*

**A SECOND set of regressions is outside `pkg/tools/browser`, in `pkg/config`, `pkg/agent` and `pkg/gateway` (D1.5c/D1.5d).** It is larger than the browser's own and it is **in the same deliverable** — the operator's scope sign-off (`ddd9789a4`) put it there, and §0.6b enumerates every row with `file:line`. In summary:

- **Deleted with the formula, not adapted** — `TestEffectiveMaxParallelAgents_Auto_MatchesMemoryFormula` (`pkg/config/parallel_clamp_test.go:113-121`, which *is* the formula written twice), `TestClampParallel_AutoFloorsAtTwo` (`:13-36`), `TestAutoDetectMaxParallel_NonLinux_DefensiveFloorNotFictitious` (`pkg/config/meminfo_other_test.go:48-55`), `TestAutoDetectMaxParallel_WarmPageCacheContainerDoesNotCollapseToFloor` (`pkg/config/meminfo_linux_test.go:288-311`). **Each is deleted with a replacement named in the commit message** — the §10.1 diff bar applies here in full, and the two `AutoDetectMaxParallel` tests' *intent* (no signal must not fail open) is what test 87 carries forward.
- **Re-derived, keeping their numbers and their real assertions** — `TestEffectiveMaxParallelAgents_Auto` (`parallel_clamp_test.go:98-108`) becomes the FR-067 shape assertion; `TestEffectiveMaxParallelAgents_ExplicitOverridesAuto_BothDirections` (`:144-160`) recomputes its bracket against the backstop.
- **Kept UNCHANGED, and they are the guard on the deletion's blast radius** — `TestClampParallelExplicit_HonoursOne` (`:45-65`), `TestClampParallelExplicit_NeverLowersLargeValue` (`:165-171`), and the PUT-0 round trip (`pkg/gateway/rest_performance_ceiling_test.go:94-121`, `:136-158`). *If any of these needs relaxing to make the change pass, the change took the explicit-operator path with it and is wrong.*
- **Passes today and would still pass, which is why it is listed** — `TestGetPerformance_ZeroConfig_SchemaValid` (`pkg/gateway/rest_performance_test.go:27-54`) asserts unconfigured `>= 2`; the backstop is 2000, so it stays green while the field's *meaning* changes underneath it. **Re-derived to assert FR-069's `capped=false` representation**, so it can fail on the change it exists to notice. *(Its comment is already stale — it says "contract minimum 2"; `PerformanceSettings.yaml:12` says `minimum: 1`.)*
- **Mechanical, compiler-found, same commit** — 29 non-test and 54 test-side single-value uses of `EffectiveMaxParallelAgents()` across 9 test files (§0.6b). **No test count may drop and no assertion may weaken** to absorb the signature change; a call site that becomes `n, _ :=` must be one where the bool genuinely does not matter, and the four that do matter are named in §0.6b.

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
| ~~2 concurrent `browser_navigate` by 2 agents on **their own** tabs → both complete; 0 deferrals; lease never acquired~~ | **TOMBSTONED with US-9/AC0 (D1.9c).** Its expected value — *"lease never acquired"* — is now wrong whenever the two turns share a session, and its input never said whether they did. Replaced by the two rows above | ~~FR-021, FR-048~~ → FR-081 |
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
| the gate on **macOS** | a **real** reading via the Darwin reader (FR-064); the same 0.84 / 0.85 / 0.86 ratios give the same three answers as on Linux — **no longer `0`, no longer blind** | FR-057, FR-064 |
| the gate on **Windows** | availability is **undeterminable** (`ok=false`); the pool **refuses to grow** at launch and at tab open, and logs the reason once — the run **fails** if it grows, and **fails** if the refusal is silent | FR-065, FR-066 |
| real Darwin host: `hw.memsize` = 34359738368, `hw.pagesize` = 4096, `vm.pages` = 8137872 | `readMemTotalBytes` = **34359738368** (`hw.memsize`), **not** `8137872 × 4096` = 33332248576 — `vm.pages` excludes firmware/kernel-reserved pages | FR-064 |
| real Darwin host: free 94356 / purgeable 87104 / speculative 227206 / pageable_external 1680476 pages | `0 < available < total`, **strictly**; the no-overlap and full-overlap compositions bracket **8.56 GB** and **7.63 GB** of 32 GB — a **12 %** spread the implementation must resolve against `vm_stat`, not guess (§12 A26) | FR-064 |
| Linux with an unreadable `/proc/meminfo` (e.g. gVisor) | `ok=false` — **not** the fabricated 2 GiB the `fallbackTotalRAMBytes/2` path yields (`meminfo_linux.go:16`); the **pool** refuses to grow. *The "and `autoDetectMaxParallel` still consumes the fallback, deliberately" half of this row is deleted with that function (FR-067)* | FR-065 |
| `PerformanceConfig{MaxParallelAgents: 0}`, no `OMNIPUS_MAX_PARALLEL_AGENTS` | `(2000, false)` — the physical backstop **and `capped=false`**. Expected **fails** if `capped` is true, and fails if the value is `0`, `2`, or `MaxInt` | FR-067 |
| `PerformanceConfig{MaxParallelAgents: 40}` | `(40, true)` — unchanged from today | FR-067 |
| `OMNIPUS_MAX_PARALLEL_AGENTS=50` with `MaxParallelAgents: 8` | `(50, true)` — env wins, precedence unchanged | FR-067 |
| `OMNIPUS_MAX_PARALLEL_AGENTS=0` (invalid, `v >= 1` guard at `config.go:457`) with `MaxParallelAgents: 0` | `(2000, false)` — an invalid env value falls through to the unset case, it does not become an explicit `0` | FR-067 |
| accessor stubbed to ratios 0.84 / 0.85 / 0.86, **agent** admission | the **same three answers** the browser gate gives at those ratios (test 72's fixtures), through the same accessor and threshold | FR-068 |
| accessor `ok=false`, three concurrent agent admissions | admit, admit, **REFUSE** — the third names memory and carries `memory_pressure`; the run **fails if the third is admitted** | FR-068a |
| accessor `ok=false`, one ordinary (non-concurrent) turn | **completes** — the gateway is degraded, not stopped. *The inverse guard: a gate that refused everything would pass the row above* | FR-068a |
| release-note text, config-doc text | **contains** *"no longer a computed default"*; **contains no** *"2 → 2000"* / *"the default moved"* sentence — the negative assertion is the one that catches a copy-forward | FR-069 |
| `PerformanceSection` rendered with `capped=false` | *"automatic — bounded by available memory"*; `2000` appears **nowhere** in the output. With `capped=true, n=40`: renders `40`, unchanged | FR-069 |
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
| session S1 opens a tab; a turn in session S2 lists tabs; operator opens a tab | S2 sees the operator's tab, labelled as the workspace's, and **not** S1's | FR-080 |
| Mia opens a tab in session S; the chat is switched to Jim (**same** `transcriptSessionID`); Jim lists tabs | Jim sees that tab, in **this session's** set, and can switch to / drive / close it | FR-080 |
| a delegation parent and its child (**different** `transcriptSessionID`, **same** `routingSessionID`); the child opens a tab | the parent's `browser_list_tabs` does **not** contain it | FR-080 |
| two turns concurrently on one `transcriptSessionID`, both `browser_navigate` | one navigation observed at any instant; **both eventually complete**; `acquireWrite` called by **both** | FR-081 |
| two turns in **different** sessions, both `browser_navigate` on their own tabs | both complete; **neither waits on the other's lease**, asserted at the seam | FR-081 |
| two goroutines `Acquire` two **different** keys while the gate refuses, LRU idle | **both** succeed; the LRU is evicted exactly once; the gate is re-asked between them, so the second does not launch on a stale reading | FR-050, FR-057 |
| the same, every instance pinned | **no growth at all**; both callers wait, then both are refused naming memory; `LiveKeys()` unchanged | FR-053, FR-057 |
| W idle-closed, then a tool call arrives | Chrome relaunches from W's profile, login intact, `LiveKeys()` +1, **same** manager | FR-040a |
| `ReapIdleSessions` cancels `se.browserCancel` while `ws:W` is live in the pool | next `Acquire(ws:W)` yields a drivable browser; no live-but-dead key | FR-040a, CRIT-102 |
| stale `SingletonLock` in `<profileRoot>/ws-W/` (the **flat** form, FR-037a) after a crash | W's next launch succeeds; the file is removed first | FR-042b |
| boot with 3 stale `ws-*.pid` markers (2 dead pids, 1 live Chrome) | 0 orphan Chromes, 0 stale markers, 0 stale locks; INFO + WARN | FR-042a |
| workspace W deleted with a live browser | `pool.Close(ws:W)` once, **then** `<profileRoot>/ws-W/` removed (the **flat** form, FR-037a) | FR-026, FR-043a |
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
| profile tree, key **not live**: `Default/Cache/`, `Default/Code Cache/`, `Default/Cookies`, `Default/Local Storage/`, `Default/IndexedDB/`, `Default/Zzz-Unrecognised/` | `Cache/` and `Code Cache/` **gone**; `Cookies`, `Local Storage/`, `IndexedDB/` **byte-identical**; `Zzz-Unrecognised/` **kept**; `<profileRoot>/ws-W/` still exists | FR-072, FR-073 |
| the same tree, key **live** (per-key launch lock held) | **nothing modified**, allow-listed paths included | FR-072 |
| a key closed by `pool.Close`, in a run where the schedule never ticks | trimmed anyway — *the assertion that fails a schedule-only implementation* | FR-072 |
| a profile surviving `kill -9`, no close ever ran | trimmed by the **boot** pass, after FR-042a's reconciliation | FR-072 |
| `IsControlled(ws:W) == true`; agent acts on `TabOwnerWorkspace()` | ADR-038 D6 deferral; **`acquireWrite` never called**; no state change | FR-071 |
| accessor `ok=false`; empty pool; first launch, then first tab | **both succeed** — the floor is one browser and one tab | FR-082 |
| accessor `ok=false`; one live browser with one tab; second launch, then second tab | **both refused**, naming memory / `memory_pressure`; `LiveKeys()` still 1 | FR-082, FR-065 |
| `IsControlled(ws:W) == false`; agent acts on `TabOwnerWorkspace()` | the call proceeds and takes effect; the owner is **still** `TabOwnerWorkspace()`; no acquisition tool exists in the registry | FR-070 |
| `IsControlled` mutated to return `false` unconditionally | **test 90 must FAIL** — the falsifiability receipt | FR-071, SC-025 |
| availability accessor `ok=false`; a **second** browser instance requested | refused to grow, naming memory | FR-075, FR-065 |
| availability accessor `ok=false`; three concurrent agent turns requested | two admitted, the **third** refused naming memory — *not* all three refused | FR-075, FR-068a |
| `KUBERNETES_SERVICE_HOST` set, `kubepods` cgroup path, **no** `/.dockerenv` | containerised = **true** — *the row a `/.dockerenv`-only predicate fails* | FR-076 |
| `/.dockerenv` present, no Kubernetes env | containerised = **true** | FR-076 |
| neither; `/proc/self/cgroup` = a `system.slice` path | containerised = **false** — *the row that fails `return true`* | FR-076 |
| containerised **and** `memory.max` reads `max` | **exactly one** startup WARN naming the condition and `resources.limits.memory`; startup succeeds | FR-077 |
| containerised **and** `memory.max` reads a finite value | **no** such WARN | FR-077 |
| **not** containerised **and** no cgroup limit (bare metal) | **no** such WARN — *the ordinary, correct case* | FR-077 |
| `/proc/meminfo` unreadable | **undeterminable** — not 4 GiB, not 2 GiB, not any constant | FR-078 |
| `/proc/meminfo` readable, real `MemTotal`, **no** `MemAvailable` line | **half the real `MemTotal`**, determinable — *the pre-3.14 heuristic, preserved* | FR-078 |
| repo-wide grep for `fallbackTotalRAMBytes`, `_test.go` included | **zero hits** | FR-078 |
| `/proc/meminfo` unreadable **and** a finite cgroup `memory.max` + usage | the **cgroup** figure, determinable — *not `ok=false`* | FR-079 |
| both signals fail | `ok=false` — the only input that produces it | FR-079 |
| both signals succeed, cgroup tighter | the **smaller** (cgroup), unchanged from today | FR-079 |

---

## 11. Functional requirements & success criteria

- **FR-001 … FR-082** as enumerated in §9. All MUST. **Counts: 107 rows in the §9 matrix, 11 of them withdrawn tombstones (FR-011, FR-012, FR-014a, FR-038, FR-038a, FR-039, FR-046, **FR-048**, FR-049, FR-055, FR-056), so 96 live FRs** — re-counted mechanically against the matrix, not carried forward. Movement in **this** revision (the D1.9c pass) is stated first, then the D1.9b pass's, then the D1.5c/D1.5d pass's, then D1.5b's, then the D1.5a subtraction pass, then revision 4's, each amending the one below it:
  - **D1.9c (2026-09-02) + round-4's C-401: +3 rows added, 1 withdrawn, none renumbered or reused.** **FR-080** (tab ownership keyed on `transcriptSessionID`), **FR-081** (the lease is consulted on a session's own tab set, superseding FR-021's trigger clause), **FR-082** (the browser floor on an unmeasurable host). **FR-048 is withdrawn** — a *re-key*, not a deletion: its `TabOwnerWorkspace()` half survives verbatim in FR-080. **Live count 90 → 93 → 96** (the D1.5e pass's FR-076…FR-079 took it to 94 first; 94 − 1 + 3 = 96). **Two of these three are net-neutral in scope and one ADDS work:** FR-080 substitutes one key for another, FR-082 states a floor the document already implied in two places and contradicted in three, and **FR-081 genuinely widens implementation scope** — it restores a lease call site that D1.9a's rescope had removed, because the premise that removed it was about agents and the contender is a turn (§0.2a, §14).
  - **D1.9b + the 2026-09-01 Windows ruling: +6 rows added, none withdrawn, none rewritten in place** — **FR-070** (implicit acquisition has no surface), **FR-071** (the control lock gates it, asserted in the blocked direction), **FR-072** (periodic profile cache trimming), **FR-073** (the protected set survives it), **FR-074** (the continuously-driven residual is declared), **FR-075** (one `ok=false` predicate, two consumer responses). **Live count 84 → 90.** **Two of these SUBTRACT work rather than adding it:** FR-070 records that D1.9b ruling 1 withdrew the seventh tool, the seventh policy entry and the Tier-3/manifest arithmetic that §0.5 E-1 had priced — the requirement is the *absence* of a surface. **FR-072…FR-074 close §12 A24(b) and §16 MAJ-111**, which had been reopened when `max_browsers` was deleted. **No existing FR is renumbered, rewritten in place, or reused**, and FR-065/FR-066/FR-068a are explicitly left as written (§0.9).
  - **ADR D1.5e + this pass's own verification: +4 rows added, none withdrawn, none rewritten in place** — **FR-076** (containerisation detected independently of the limit), **FR-077** (the node-memory WARN, silent everywhere else), **FR-078** (the Linux reader stops fabricating an invented 2 GiB), **FR-079** (an undeterminable signal does not discard a determinable one). **Live count 90 → 94.** **Two are D1.5e's ruling (FR-076, FR-077); two are NOT in D1.5e** — they close a second fail-open path found while verifying it, and they correct one row of D1.5e's own deployment matrix, which files an unreadable `/proc/meminfo` under §0.9's conservative `ok=false` where it does not belong. **No existing FR is renumbered, rewritten in place, or reused**, and FR-064…FR-075 are explicitly left as written (§0.10).
  - **D1.5c + D1.5d: +4 rows added, none withdrawn, none rewritten in place** — **FR-067** (`bytesPerAgent` and the computed default deleted; `EffectiveMaxParallelAgents` becomes `(n, capped)`), **FR-068** (agent admission on the same live gate, shape 2), **FR-068a** (an unmeasurable host holds at the floor and refuses to grow), **FR-069** (the announcement corrected, and the SPA stops recommending a backstop). **Live count 80 → 84.** *FR-068a is a lettered sibling of FR-068, as FR-057a is of FR-057: same subject, split so the unmeasurable branch is independently citable and independently testable.* **These four are NOT browser requirements**, and they are in this document because the operator ruled them here (ADR D1.5c/D1.5d) and signed off the scope (`ddd9789a4`) — see §0.6b. **They ship in the same deliverable as the browser gate**; an earlier revision of this list carried them as gated on a pending ratification, and that hedge is withdrawn. **No existing FR is renumbered and no number is reused.**
  - **D1.5b: +3 rows added, none withdrawn, one rewritten in place** — FR-064 (the Darwin memory reader), FR-065 (the undeterminable case refuses, and the accessor becomes two-valued), FR-066 (Windows foreseen, declared, and `degraded-unsupported`). **FR-057 is amended, not renumbered**: its off-Linux warning changes from *"this spec cannot resolve it"* to the ruled answer, and the export it already required gains a shape — `(bytes, ok)`. **FR-061's "must be declared, not defaulted through" now names the FRs that declare it.** **Live count 77 → 80.** *Nothing is subtracted by this ruling: D1.5b adds a capability the previous revision had escalated rather than assumed, and every FR it touches was written by that escalation.*
  - **D1.5a: +5 rows added** — FR-059 (the deletion, as implementation scope), FR-060 (the gate in the tab-open path), FR-061 (idle close + gate are the entire defence), FR-062 (`PER_BROWSER_COST` and the absence of every constant), FR-063 (a reason code naming memory). **+5 withdrawn to tombstones** — FR-038, FR-038a, FR-049, FR-055, FR-056. **5 rewritten in place** — FR-044 (narrowed: the number is measured; Linux + capture is what remains), FR-050 (eviction's trigger is the gate, not a target), FR-053 (refusal naming memory; the bounded overshoot is deleted), FR-054 (the WARN's remedy re-derived), FR-057 (the sole hard stop; E-2 ruled). **Live count was unchanged at 77** — five in, five out — which was a coincidence, not a design property.
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
- **SC-023 (the Darwin reader was RUN on Darwin, and the Windows gap is declared — NEW with D1.5b).** Three conditions, all required. **(1)** The PR body carries the **run receipt** for test 82 from a real macOS host — the host's `hw.memsize`, the value `readMemTotalBytes` returned, the value `readMemAvailableBytes` returned, and the `go test` exit code. *Fails if:* the only evidence is a cross-compile (`GOOS=darwin go vet` / `go test -c`), which proves the code type-checks and proves nothing about the numbers. **This condition exists because this repo's CI is Linux-only**, so a `//go:build darwin` test is never executed by it — the gap `pkg/config/meminfo_other_test.go:10-21` documents about itself, and the reason MAJOR-2 shipped with nothing able to catch it. **(2)** The PR body records the **`vm_stat` cross-check** that resolved whether `vm.page_pageable_external_count` already includes `vm.page_speculative_count` — the two compositions differ by ~12 % on a 32 GB host (§12 A26). *Fails if:* the formula ships with the overlap assumed in either direction and no cross-check. **(3)** The release notes and the browser section of the config documentation each contain a line stating the pool has **no memory-derived limit on Windows**, and `pkg/config/meminfo_windows.go` exists. *Fails if:* any of the three artefacts is absent — the gap is then discoverable only by running out of memory.
- **SC-024 (one mechanism, and the announcement is the true one — NEW with D1.5c/D1.5d).** Four conditions, all required. **(1) One accessor, one threshold.** Test 86 passes: the browser gate and agent admission are proved — at the injection seam, not by matching outcomes — to read the **same** exported accessor and the **same** threshold. *Fails if:* a second threshold constant exists anywhere in the admission path, or a per-agent byte figure reappears under any name. **(2) The refusal side is asserted.** Test 87 passes **and** test 87's third case (`TestAgentAdmission_UnmeasurableStillServesATurn`) passes. *Fails if:* only the admit side is exercised — *"admits when memory is free"* passes against a stub that always admits, and a refusal test with no inverse guard passes against a gate that refuses everything. **Both directions or neither.** **(3) The deletion is total and the explicit path is untouched.** Repo-wide, `_test.go` included: zero `bytesPerAgent`, `autoDetectMaxParallel`, `clampParallel`, `autoDetectFloorParallel`; and `TestClampParallelExplicit_HonoursOne`, `TestClampParallelExplicit_NeverLowersLargeValue` and the PUT-0 round trip pass **with no assertion relaxed**. *Fails if:* any of the three needed weakening — that means the sweep took the operator's own setting with it. **(4) The announcement does not carry the dissolved sentence.** The release note and config documentation say *"there is no longer a computed default"* and contain **no** "2 → 2000" or "the default moved" claim, and the SPA renders no `2000` as a recommendation. *Fails if:* the 2 → 2000 sentence ships — **and it is the likeliest single failure of this whole pass**, because it is what §0.6a and ADR D1.5c both prescribed in writing before D1.5d dissolved it, and it is one copy-paste away.
- **SC-025 (implicit acquisition is GATED, and the gate is proved by a test that can fail — NEW with D1.9b ruling 1).** Two conditions, both required. **(1)** Test 90 passes with all four cases — blocked, blocked-via-the-resolved-key, allowed, and gate ordering. **(2) The falsifiability receipt:** the PR body records test 90 run against a build with `LiveViewRegistry.IsControlled` forced to return `false`, and the **exit code is non-zero**. *Fails if:* condition (2) is absent, or the mutated build still passes. **Condition (2) is the whole criterion.** Condition (1) alone is satisfiable by a test that only ever exercises the allowed direction, and *that* test is green on a build with no control lock at all, with `IsControlled` hard-wired to `false`, and with `controlledResult` deleted from every call site — which is precisely the silent takeover ADR D1.9b names as the thing the lock prevents. *This criterion exists because the ruling's own text says the lock **is** the whole mitigation; a mitigation with no failing test is a sentence.*
- **SC-026 (profile disk is bounded, and the bounding never logs anyone out — NEW with D1.9b ruling 4).** Three conditions, all required. **(1)** Test 91 passes: after a trim of a non-live profile, every allow-listed path is gone, every protected path is **byte-identical**, an unrecognised directory is **kept**, and the profile directory still exists. **(2)** Test 92's `TestTrim_LoginSurvivesRelaunch` passes **against real Chrome** — log in, close, trim, relaunch, still authenticated — under the shipped default configuration, no flag flip. **(3)** The trigger test includes the run in which the schedule **never ticks** and the `pool.Close`d key is trimmed anyway. *Fails if:* any protected path changes; if the unrecognised directory is removed (that is a deny-list implementation, and it will widen into wherever credentials move next); if the relaunched workspace is signed out; or if (3) is missing, which would let a schedule-only implementation — the one that leaves a busy host unreclaimed for up to an hour after every close — pass. **Not satisfied by a green `go test ./...`:** condition (2) is real-Chrome and shares SC-002's gating.
- **SC-027 (one predicate, two responses, and the platform statuses are not merged — NEW with the 2026-09-01 Windows ruling).** Two conditions, both required. **(1)** Test 93's first test passes with **both** assertions in one body off **one** stubbed `ok=false` accessor: the pool refuses to grow, **and** agent admission admits two turns and refuses the third. **(2)** The release note and config documentation record Windows' browser refusal as **accepted because Windows is not yet a supported platform**, and separately record a `/proc`-less **Linux** host (gVisor) as a **supported** deployment reaching the same predicate through `pkg/config/meminfo_linux.go`'s fallback. *Fails if:* the two assertions are split into separate tests (each passes alone on a build that collapsed the responses — "the pool refused" is green when everything refuses, "two turns ran" is green when nothing is gated); or if the documentation presents Windows and gVisor as one platform class, which is what would license an implementer to make the agent path refuse outright and break a supported Linux deployment. *`pkg/config/meminfo_other.go:20-33` already records what that costs in the other direction — 585 concurrent agents on any box — so both failure modes have precedent.*
- **SC-028 (the lease is actually taken on a session's own tab set — NEW with D1.9c, FR-081).** Two conditions, both required. **(1)** Test 99 passes in all three parts: two turns on one session serialise and **both complete**; two turns in different sessions **neither block**, asserted at the lease seam; and `acquireWrite` is asserted **called** for a `TabOwnerSession()` write. **(2) The falsifiability receipt:** the PR body records test 99 run against a build restoring FR-021's `TabOwnerWorkspace()`-only trigger, and the **exit code is non-zero**. *Fails if:* condition (2) is absent, or the restored-trigger build still passes. **Condition (2) is the whole criterion**, for a reason this document has already been caught by once: the tombstoned US-9/AC0 asserted *"the lease was never acquired"* and was green **because** the lease was never acquired — the assertion and the defect were the same statement. A criterion that cannot distinguish "correctly not needed" from "wrongly skipped" is not a criterion. *Not satisfied by a green `go test ./...`.*
- **SC-029 (the write lease has exactly ONE acquire symbol, repo-wide, and the sibling spec cannot reintroduce a second under a new name — NEW, round-4 M-406).** One condition. A **repo-wide structural assertion** (`_test.go` included) that `pkg/tools/browser` declares exactly one lease-acquisition symbol, matched **by shape rather than by name**: any method or function on `*BrowserManager` — or any package-level function taking a `BrowsingKey` — that returns a `func()` release closure together with a boolean or a holder string, and that is not `acquireWrite`, **fails the assertion**. *Fails if:* the check is a grep for the literal string `acquireWrite` (which passes against a second primitive called anything else), or if it is scoped to non-test files (the D2 spec's `TestLeaseWrite_SecondWriterDeferred` is itself a `_test.go` symbol this spec requires deleted). **Why this exists:** §14's *"required action in the D2 spec"* — delete its Stream F lease scope, its FR-023, its US-14, its BDD scenario and its `TestLeaseWrite_SecondWriterDeferred` — is a **request to another document**, and no success criterion in this one failed if it was ignored. The named consequence is not hypothetical: §14 records that if both landed, *"the seven action tools would have taken two unrelated mutexes and mutual exclusion would have been lost for whichever tool took only one"*, which ADR §5 calls the most expensive failure class in this design. *A cross-document obligation with no local gate is a sentence; this is the gate.*
- **SC-030 (the fail-open cases are closed, and the two silent cases are proved — NEW with ADR D1.5e and this pass's verification).** *(Numbered **SC-028** until round-5's C-504: two different requirements carried that number — this one and FR-081's lease receipt — and both were cited bare, from §3.2 Stream D and from §9. §11's own rule forbids reusing an FR number; the same rule applies to an SC. The lease receipt keeps SC-028 because five sites cite it; this one moves, and it is the only renumbering in this pass.)* Four conditions, all required. **(1)** Test 95 passes with **all four** fixtures, three of which assert **silence** — a run that only demonstrates the warning firing does not satisfy this. **(2)** Test 96's reader half passes as a **pair**: an unreadable `/proc/meminfo` reports undeterminable **and** a `MemTotal`-only fixture still returns half of the **real** total; `fallbackTotalRAMBytes` returns zero hits repo-wide, `_test.go` included. **(3)** Test 96's combination half passes all three cases, including both-determinable returning the **smaller** — the case that catches a fix which inverted the min. **(4)** The config documentation for the browser and performance sections, and the release note, state (a) that a container without `resources.limits.memory` sizes itself against node memory and what to set, and (b) the residual §12 A29 names — a cgroup-v2 pod detectable by none of FR-076's signals gets no warning, and the override env var is how an operator covers it. *Fails if:* any silent case is missing (the warning is then unfalsifiable), the pre-3.14 heuristic was deleted along with the constant (a legitimate case broken as collateral), or the documentation describes the warning without describing what it does not catch — which is the shape that turns a declared gap back into a silent one.
- **SC-012 (G-1: the memory measurement — a HUMAN gate, NARROWED by D1.5a).** `PER_BROWSER_COST` is now measured — **≈182 MB** on macOS, Chrome for Testing, idle and non-capturing (FR-062) — so this gate no longer certifies a *ceiling*, because there is none. What the implementing PR body must contain is what that measurement does **not** cover: the raw **PSS** for one and two Chromes **on Linux**, the same figure **with capture running** (the injected extension plus encoding, unmeasured today), the gateway's own steady-state PSS, and the tool and command that produced them (`smem`, or `/proc/<pid>/smaps_rollup`'s `Pss:` summed over each instance's process tree — **`ps` reports RSS and cannot produce this**). *Fails if:* the figures are **RSS** (the metric ADR §8 retracts, over-counting by 2.6× on the measured box — approving an RSS-derived figure is a gate certifying the specific error it exists to catch); **or** ≈182 MB is shipped as the Linux figure without a Linux run behind it; **or** the capture delta is left unmeasured **and unstated**; **or** any place quoting ≈182 MB omits its scope. **Owner: the implementing PR's human reviewer**, who must not approve without seeing the numbers. §0.3.1 explains why no mechanical form is honest. Test 51 is the partial mechanical half. *(Revision 4 required arithmetic "through D1.5's formula to the shipped `operator_ceiling`". The formula and the ceiling are both deleted — D1.5a — so that half of the gate is satisfied by there being nothing to compute.)*
- **SC-012a (G-2: the capture spike — a MECHANICAL gate, corrected against the shipped workflow).** `TestSpike_CaptureAgainstSecondChrome` **ran and passed** before Stream P's first commit. Four conditions, all required: (1) the test uses `requireBrowserOrFail`, and `grep -c skipIfNoBrowser` in its file returns 0 for that test; (2) it runs as **its own step** in the existing `browser-e2e` job (`.github/workflows/pr.yml:392`), with its own `-run '^TestSpike_CaptureAgainstSecondChrome$' -count=1` — **not** folded into the existing step, whose `passes -lt 180` floor (`:481`) a single-test invocation would trip; (3) the step runs under `set -euo pipefail`, and the PR-body receipt is captured as `cmd > log 2>&1; echo "exit=$?"` reading `exit=0`; (4) the log contains exactly one `--- PASS` and neither `--- SKIP` nor `no tests to run`. *Fails if:* any of the four is missing. **A skipped result is a FAILED gate** — this is the single load-bearing assumption of D1.4, and the equivalent claim for CDP contexts proved false against real Chrome 150 (`coordinator.go:330-348`).
  **Two corrections to revision 3's version of this criterion, both verified on this worktree.** (a) It asserted the CI gate *"always skips without the env var"* and routed G-2 to the Fly worker on that basis. **`OMNIPUS_BROWSER_E2E: "1"` IS set** — job-level, at `:416`, with the comment *"Set ONLY here"* — and `:468-472` already fails the job if either skip path fires. The gate machinery exists; **only the test does not**. (b) Its blanket "never through a pipe" would have required rewriting a **correct** shipped step: `go test … 2>&1 | tee /tmp/browser-e2e.log` under `set -euo pipefail` propagates `go test`'s status, so the failure mode the rule guards against (`cmd | tail` reporting *tail's* status) cannot occur there. The rule is restated as one about the **author's PR-body receipt**, which is where it bites.
- **SC-013 (no residual constant, tests included).** Repo-wide references to `DefaultSessionID`/`defaultSessionID` are **zero**, in production **and** test code — down from 57 non-test hits (37 executable + 2 declarations + 18 comments) and **364 test-side references across 25 files**. *Fails if:* any survives, including a test-only alias. A test-only alias would leave this criterion reading zero while the constant is alive, the reaper suite still asserting against `"default"`, and the measurement meaningless — which is why the count is repo-wide rather than non-test (§2.2a).
- **SC-019 (G-3 + G-4: the two Chromium unknowns — MECHANICAL).** The PR body carries receipts from one Chrome launched inside a `memory.max`-capped cgroup: the renderer limit Chrome computed for itself (does it read the cgroup limit, or host RAM?) and whether a memory-pressure notification was ever delivered. *Fails if:* either is answered in prose rather than from a run, **or** FR-057 ships while G-3 is negative without stating that D1.5's `min(host_RAM, cgroup_limit)` describes a policy Chrome is not following.
- **SC-020 (G-5: cold start with a warm profile — a HUMAN gate).** The PR body carries the measured time from `Acquire` on an idle-closed key to a drivable page, over ≥5 runs, and the arithmetic from it to FR-054's window and threshold. *Fails if:* the two thrash constants are shipped with no measurement behind them — that is the failure mode §0.3 exists to prevent for the target, applied to the two constants most likely to be guessed because they look small. ADR-042's ~30–60 s covers a fresh install *including a Chromium download* and does not satisfy this.
- **SC-021 (G-6: memory or CPU — a HUMAN gate, and D1.5a makes it MORE load-bearing, not less).** The PR body states which resource binds first on the measured host class with N instances live and one watched, with the sampling method. *Fails if:* the feature ships without it. ADR §7's entire argument is that a **CPU** bound solved the problem on a 2-core box at 85–99 % utilisation with **one** Chrome; the pool multiplies browser, GPU and encoder processes by N on that same box **and now bounds memory only, with nothing else bounding anything**. *(Revision 4 phrased the failure as "the ceiling default ships without it". There is no ceiling — so the escape hatch of "set a conservative ceiling and move on" is gone too. If CPU binds first, the one control that exists is watching the wrong resource, and no counter is standing behind it.)*
- **SC-014 (gates).** `gofmt -l . | wc -l` = 0; `golangci-lint run --build-tags=goolm,stdjson` exit 0; CI `go test -tags goolm,stdjson -count=1 ./...` exit 0; `govulncheck` 0 vulnerabilities; `npm run typecheck` exit 0; `npx vitest run` exit 0. **Not sufficient on its own:** SC-012, SC-012a, SC-019, SC-020, SC-021, **SC-023**, **SC-024's condition (3)**, **SC-025's condition (2)** (a mutated-build run, whose receipt is a non-zero exit code), **SC-026's condition (2)** (real Chrome, SC-002's gating) and **SC-027's condition (2)** (a documentation assertion) are separate and none is satisfied by a green CI run. *SC-024's other three conditions ARE mechanical (tests 86, 87, 88); condition (3)'s "with no assertion relaxed" is a reviewer's judgment on a diff, which no test can make — the surviving explicit-path tests stay green whether they were left alone or quietly weakened to accommodate the sweep.* **SC-023 is the sharpest case:** CI here is Linux-only, so it never compiles, let alone runs, FR-064's `//go:build darwin` tests — a fully green CI is compatible with a Darwin reader that returns nonsense. Note also that `golangci-lint` caps findings at 3 per message by default — read `docs/internal/false-green-patterns.md` before reporting a clean lint.
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
| **A20** | **What does boot reconciliation do with a stale ownership marker whose pid is still alive?** | **DECIDED — and the discriminator is CORRECTED (D1.8).** Revision 3's answer began "terminate it", reasoning that nothing can distinguish an orphan from a second gateway. **Something can: the per-key launch lock.** On Unix a flock auto-releases when its holder dies, so a *held* lock plus a live Chrome pid proves a live neighbour, and `takeLaunchLock` (`coordinator.go:1442-1483`) already gets this right for the single-Chrome case by refusing to launch rather than killing. The pool keeps that: **lock acquirable + live pid ⇒ orphan, terminate; lock held + live pid ⇒ second gateway, refuse and name it, terminate nothing.** Revision 3's rule shoots the neighbour, and ADR §9.1 names this as the one correction the pool FRs need. The rest of revision 3's reasoning stands: **terminate, and the trade-off is recorded rather than buried.** A live pid named by a marker under **this** `$OMNIPUS_HOME` is either an orphan of a crashed gateway on this home (the common case) or a second gateway on the same home (already unsupported — §12 A11 — and already refused outright by the shipped single-Chrome path, `coordinator.go:1458-1467`). Nothing can distinguish them, and leaving it alive means unbounded host memory outside the cap, which defeats FR-038's only purpose. **Pid reuse is a real hazard** — a dead gateway's Chrome pid can be reused by an unrelated process — and the shipped code has it too (`pidAlive(pid)` plus an owner string read from the *marker file*, not from the process). Mitigation, stated honestly by platform: on Linux, confirm `/proc/<pid>/exe` resolves to the resolved Chrome binary before terminating. **On macOS and elsewhere there is no pure-Go equivalent** (Hard Constraint #2 forbids shelling out here), so on those platforms the marker is **removed without terminating** and a WARN names the pid so an operator can act — a smaller guarantee, stated rather than implied. |
| **A21** | **`max_browsers`' 0/negative semantics, and how three caps now relate.** | **⚠️ WHOLLY SUPERSEDED BY D1.5a — there are no caps to relate, and no edge semantics to define.** All three guards this row arbitrated are gone: `--renderer-process-limit` is never set (FR-055 tombstoned, and its removal is a **security improvement** — see A23), the derived instance target is deleted (FR-056 tombstoned), and `max_total_tabs` is removed from the code (FR-059). **The row is kept, not deleted, because two of its findings survive their subject.** (1) Its rejection of revision 3's memory argument still stands and now applies more widely: *a tab is not a process* — a cross-site embed can claim its own renderer, same-site embeds collapse into one — so **nothing may be priced per tab**, which is FR-062. The measured evidence has since gone further: **2 tabs against 13 renderer processes**, ~6 per tab. (2) Its "surprise" argument — that a configured ceiling must not be silently multiplied by N — is void only because there is no configured ceiling left to multiply; the *principle* survives as AC7 and SC-022, which forbid any message or doc string implying a setting that does not exist. Test 53 is tombstoned with the row's subject. |
| **A22** | **The whole-Chrome idle window's value — and what it costs once eviction exists.** | **DECIDED: `tools.browser.idle_close_ttl`, default 15 minutes — a reasoned default, not a measurement, and the spec says which.** Derivation: 3× the per-tab `tools.browser.idle_ttl` default of 5m (`manager.go:134`), so a Chrome closes only after its last tab has been gone for a further two tab-TTLs. The asymmetry is deliberate: closing the browser process reclaims a smaller fixed cost than reaping a renderer and pays a relaunch on the next use, so it should be less eager than tab reaping. *(Revision 3 supported this with "renderers are the dominant cost at 74–268 MB **RSS** each" — the metric ADR §8 retracts. The **conclusion** does not depend on the magnitude, only on the ordering: a renderer costs more than the marginal browser process. Restated on that basis, and the retracted figure is not quoted.)* **The sweep interval invariant applies:** the existing 1-minute ticker (`gateway.go:5322`) must stay well under this TTL, or the TTL becomes a floor rather than the lifetime. **⚠️ And one consequence that was harmless before eviction, and is load-bearing under D1.5a:** idle close is now **half the entire defence** (FR-061), not housekeeping — the two TTLs compose. A workspace browsed once holds its slot for the 5-minute tab TTL **plus** the 15-minute idle-close TTL — **about twenty minutes of occupancy after the last action**. **Under D1.5a that composition is no longer about a target — it is about how long memory stays occupied.** A workspace browsed once holds its memory for ~20 minutes after its last action, so the host's effective capacity is set by workspaces browsed *within any ~20-minute window*, not by workspaces browsing *right now*; D1.7's "ten workspaces on a three-browser machine" arithmetic reads optimistically if that is missed. With no counter behind it, a too-generous TTL now shows up as **memory refusals**, not as a slower reclaim. It also sets the floor for FR-054's thrash window: a workspace evicted before its idle TTL expires was, by this definition, still occupying its slot legitimately. **This is not FR-044's kind of number** and §5's "no value derived from an estimate" non-behaviour does not apply to it: that rule was about the target, whose wrong value costs host memory. This one's wrong value costs a relaunch — **but under D1.5a it is one of only two controls that exist**, so "input to capacity" understates it: a TTL long enough to never fire is indistinguishable from having no idle close at all, which FR-061 requires a test to catch. |
| **A23** | **`--renderer-process-limit` weakens site isolation above its bound. Is that acceptable here?** | **⚠️ REWRITTEN BY D1.5a — the question is DISSOLVED, not answered, and that is the better outcome.** Revision 4 answered *"acceptable, because R is a site-isolation **floor** derived from the tab count and never lowered for memory"*, having first retired the earlier justification (*"acceptable for agent-driven browsing of semi-trusted destinations"*). **D1.5a deletes the flag entirely**, so no bound exists and Chrome's default site-per-process isolation applies to every page. **The finding this row records is worth keeping verbatim, because it is what made the flag indefensible:** `BrowserManager.ValidateURL` (`manager.go:685-708`) blocks five schemes (`blockedSchemes`, `:675-683`) and private/metadata addresses via the SSRF checker (SEC-24), and **permits every other public `http(s)` URL, with no allow-list anywhere in `pkg/tools/browser/`.** An LLM-driven `browser_navigate` is by construction pointed at arbitrary URLs, from model output influenced by page content the agent just read. So *"semi-trusted destinations"* described nothing the code enforces. Under D1.10 the workspace browser holds the operator's live logins and under D1.9a an agent can be asked onto the operator's own tab — a signed-in bank tab and a page an agent found must never share a renderer. **Not setting the flag guarantees that unconditionally**, where revision 4's floor only guaranteed it below R. **C-303 / C4 / C206 are therefore DISSOLVED — no residual trade-off, no compensating control, nothing to revisit if an allow-list ever lands.** |
| **A24** | **Three ADR §6 "open" questions that this spec has already answered. Which way does each go?** The ADR lists them as undecided; a spec that quietly decides them leaves two documents disagreeing, which is the failure this whole round exists to end. | **One is REOPENED; two are KEPT with the ADR named as owner of closing them. Stated per item rather than in aggregate.** <br>**(a) "Is the capture session per workspace or per viewer?" — KEPT.** FR-016a decides per workspace, and the decision is **forced**, not chosen: one manager per workspace means one browser to capture, and ADR-048's "requesting agent" conflict rule has no referent once agents no longer have disjoint tab sets. `NewCaptureSession`'s doc (`capture_session.go:360`) still calls `mgr` "the **agent's** BrowserManager" and must change with it. **Outstanding ADR edit:** §6 should close this citing FR-016a. <br>**(b) "Who bounds per-workspace profile disk?" — RULED, and the row is CLOSED (ADR D1.9b ruling 4, operator, 2026-09-01).** **The reopening was correct and is now answered.** §16 MAJ-111 had closed it with *"live profiles are bounded by `max_browsers`, and dead ones are removed by the deletion path"* — a closure that lost its subject when FR-056 was tombstoned, and that never held anyway: `max_browsers` bounded live **processes**, not bytes, while a profile's cache grows during the instance's life and is deliberately **not** reclaimed on idle close or eviction (FR-043a). N workspaces browsed once each left N unbounded caches on a host this project has filled twice. **The answer is periodic cache trimming** — §0.8 specifies it and **FR-072 / FR-073 / FR-074** carry it: an allow-list criterion (*the browser wrote it as a re-fetchable cache, and no site wrote it through a web storage API*), three triggers (on `pool.Close`, at boot, and on `tools.browser.cache_trim_interval`), never against a live profile, and the protected set proved byte-identical **and** proved behaviourally by a login that survives a relaunch. **A hard per-workspace size cap was REJECTED by name, structurally rather than by tuning** — see **A28**, where the reason is recorded so it is not re-proposed. **What remains open is narrower than the original question and is escalated rather than assumed:** a workspace under **continuous** drive never becomes eligible, so its cache is not bounded (§0.5 **E-9**, declared by FR-074). **Outstanding ADR edit:** §6 should close this row citing D1.9b ruling 4. <br>**(c) "Does the cap count instances or bytes?" — CLOSED by D1.5a: bytes, and nothing else.** Revision 4 answered "both" — instances for the target (FR-056), bytes for admission (FR-057) — which was D1.5's item 2 / item 3 split. **D1.5a deletes item 2.** Nothing counts instances anywhere; the live-memory gate is the only admission control and it reads bytes. **Outstanding ADR edit:** §6 should close this row citing D1.5a, not D1.5 items 2 and 3. <br>All three are escalated in §0.5 E-4 rather than left to be discovered. |
| **A25** | **Does the operator's shared tab set need a lifecycle of its own?** A session's tab set dies with the session; the workspace-owned set does not belong to anyone who can leave. *(Re-keyed 2026-09-02 by D1.9c — this row read "an agent's tab set dies with the agent's membership". **The re-key makes the row's answer stronger, not weaker**: a session's tab set has a definite end, whereas an agent's membership could be restored.)* | **DECIDED, narrowly, and the boundary is stated.** The workspace-owned `sessionEntry` is reaped by the **same** per-tab `ReapIdleSessions` rules as any other (FR-025) — it holds tabs, and an idle tab is idle regardless of who opened it. It is **not** deleted when an agent leaves the roster (it was never that agent's), and it **is** disposed with the workspace (FR-026, FR-043a). **⚠️ The one thing this row left undecided is now DECIDED — by consequence of ADR D1.9b ruling 1, not by a separate decision.** It read: *"whether an agent may **close** a workspace-owned tab it did not open … is part of E-1's take-control mechanism — a D2 tool-surface decision. Until then, treat closing the operator's tab as requiring the same acquisition as writing to it."* **That provisional rule turns out to be the permanent one, and E-1 is ruled.** Acquisition is implicit (§0.7, FR-070), and `browser_close_tab` **is** `controlledResult`-gated (`pkg/tools/browser/tabs.go:171`) and therefore leased (§14 rule 3). So closing the operator's tab **is** acting on it: it acquires implicitly, it is refused while a human holds the live-view lock (FR-071), and it contends for the write lease like any other write. **No new rule, no exception, and nothing for D2 to decide.** FR-080's prohibition is untouched — a turn still cannot close another **session's** tab, which is a different tab set entirely. *(Under the superseded D1.9a this said "another agent's tab"; under D1.9c an agent on the **same** session may close that session's tabs, which is the ruling, and the prohibition is between sessions.)* *Recorded plainly rather than left implicit, because "an agent may close the tab I opened" is a real consequence an operator should meet in a document rather than in a session.* |
| **A27** | **Why is FR-068a's floor 2, and what would justify a different number?** It is carried forward, not measured: `meminfo_other.go:25-33` documents 2 as the deliberate no-signal posture already shipped on exactly these hosts, and `clampParallel`'s `autoDetectFloorParallel` (`config.go:558`) is where it lived. **This spec keeps it because changing it would need evidence nobody has, and inventing a different number would be the shape D1.5d just deleted** — a per-unit assumption wearing a new name. **What is NOT claimed:** that 2 is right for any particular workload. It is the smallest number that keeps the product working (delegation needs a parent and a child concurrently) on a host where nothing can be measured. **If a measurement ever contradicts it**, that is a tuning change to one constant on one mechanism, argued here — never a second mechanism, and never a return to dividing availability by an assumed cost. *Recorded because a floor that nobody re-derives is exactly how `bytesPerAgent` became load-bearing.* |
| **A26** | **What exactly is macOS's `MemAvailable` analogue, and does `vm.page_pageable_external_count` already include `vm.page_speculative_count`?** Linux hands the gate one kernel-computed number; macOS hands it a set of page counters and no guidance on how they overlap. | **DECIDED HOW TO DECIDE, and deliberately NOT decided in prose — which is the honest answer here.** The composition is `(page_free_count + page_purgeable_count + page_speculative_count + page_pageable_external_count) × pagesize`, and **every term is verified present via `sysctl` on a real Darwin host** (macOS 26.5.2 / Darwin 25.5.0); `vm.page_active_count`, `vm.page_inactive_count` and `vm.page_wire_count` are **not** sysctls at all — they are Mach `host_statistics64` fields — so the formula cannot mirror `vm_stat`'s output no matter how much it resembles it. **The open part is the overlap:** speculative pages are file-backed, so they may already be inside `page_pageable_external_count`. On the reference host the two readings bracket **8.56 GB** (no overlap) and **7.63 GB** (full overlap) of 32 GB — a **12 % error either way. This spec declines to guess, and FR-064 makes resolving it implementation scope with a named method** (cross-check against `vm_stat`'s "Pages free / speculative / purgeable / File-backed pages" on a real host) **and a named artefact** (SC-023 condition 2). *A spec that picked one and asserted it would be inventing a kernel detail, which is the failure mode §0.3 exists to prevent for every other number in this document. **What this does NOT license is shipping either choice undocumented** — FR-064 requires the chosen terms in the reader's doc comment, so the next reader inherits a decision rather than a mystery.* |
| **A28** | **Why not simply cap each workspace's profile directory at N gigabytes?** It is the first thing a reader proposes, it is what most software does, and it would give a hard bound rather than the soft one §0.8 delivers. | **REJECTED BY RULING, and the reason is structural rather than a tuning disagreement (ADR D1.9b ruling 4).** *"When it binds, something must be discarded mid-session, and the only large items are the cache **and the logins** — discarding the logins is the one outcome this whole design exists to prevent."* **Read that as: a size cap over this particular directory has no safe action at the moment it binds.** The directory's contents are cache plus credentials; a cap that fires must choose between them, and choosing wrong logs a client out mid-session — the failure ADR-072 D1.10 and FR-043a exist to prevent, arriving from the mechanism meant to protect the host. **Raising N postpones the moment; it does not change what happens at it**, which is why this is not answerable by picking a bigger number. **What a trim does instead:** it only ever removes data the browser can re-fetch or re-derive, so its worst outcome is a slower first page load. A mechanism whose failure mode is *slower* cannot produce the failure mode *signed out*. **What is NOT claimed:** that trimming bounds everything. It does not bound a continuously-driven workspace (§0.5 **E-9**, FR-074), and one of E-9's candidate fixes — Chromium's own `--disk-cache-size` — is *not* this rejected cap, because Chromium evicts only its own cache entries and can never reach a login. **The rejection is of a cap over the PROFILE, not of a bound on the CACHE.** *Recorded because a rejection whose reason is not written down is re-proposed at the next review.* |
| **A29** | **What containerisation signal does FR-076 use, and what does it still miss?** D1.5e says *"e.g. the presence of a container cgroup path"* and leaves the set open; a spec that leaves it open ships a predicate whose failure mode is silence. | **DECIDED, with the residual named rather than papered over.** The set is: an explicit operator override env var, `KUBERNETES_SERVICE_HOST`, `/.dockerenv`, and a container path in `/proc/self/cgroup` (`kubepods`, `docker`, `lxc`, `containerd`). **`isRunningInDocker` (`pkg/gateway/sandbox_apply.go:185-201`) is rejected as the implementation**, not merely extended: `/.dockerenv` (`:179`) is a Docker runtime marker and Kubernetes runs containerd or CRI-O, so that predicate answers **false** inside the pod D1.5e is about — the *never fires* shape, in the exact target case. **What is still missed, stated plainly:** a cgroup-v2 pod in its own cgroup namespace reads `0::/` from `/proc/self/cgroup`, which a bare-metal process outside any slice also reads. **`0::/` alone is deliberately NOT taken as containerisation** — treating it as such would fire on ordinary hosts, and *a warning that always fires is not a warning* (D1.5e's own words). So a pod with **no** `KUBERNETES_SERVICE_HOST`, **no** `/.dockerenv` and **no** named cgroup path goes undetected and gets no warning. **That case is covered by the override env var and by the FR-077 config-doc line, not by inference** — the same declare-the-gap pattern as FR-066 and FR-074. *Recorded because the alternative is a predicate whose coverage nobody can state, guarding a warning nobody will trust.* |


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
19. **(happy, tab ownership — the ruling's own scenario, REWRITTEN by D1.9c and the answer INVERTS)** Operator asks Mia to look something up; Mia opens a tab. The operator then opens a tab of their own in the panel and browses. They switch the chat to Jim — **the same chat, so the same session** — and ask "what's open?" **Jim names BOTH: the operator's tab, labelled as the workspace's, and the tab opened in this chat.** He can be asked to drive either. Then the operator opens a **different** chat and asks there — **that chat sees the operator's tab and not this one's.** *This is ADR §1.1's conversation with the right answer, and it is the holdout that fails if FR-080 was skipped.* **⚠️ Under the superseded D1.9a this holdout read *"Jim names the operator's tab and **not Mia's**"*. That is now the WRONG answer — it is the very behaviour §1.1 reports as the defect, half-fixed. A tester working from an older copy of this document will pass the run that should fail; the second half (a different chat sees neither) is what distinguishes a correct implementation from one that simply merged everything.*
20. **(edge, tab ownership — REWRITTEN; it was missed by the D1.5a sweep)** Mia opens five tabs. Jim, on the same workspace, opens one — **it succeeds**. *(Revision 5's form opened this with "With `tools.browser.max_tabs` at its default of 5…" and cited **FR-049**. Both the key and the FR were deleted by D1.5a; the sweep that removed them did not reach this line. The scenario survives because its concern does: the re-key must not turn a per-agent tab budget into a per-team one.)* **Two failures to watch for, and they are different.** (a) Jim's open fails naming a **tab limit** or a **config key** — that means a cap survived the deletion somewhere, or a stale `max_tabs_reached` string is still being emitted (FR-059, FR-063). (b) Jim's open fails naming **memory** on a host that plainly has plenty — that is the gate misreading, not an ownership bug, and it belongs to holdout 23. *A refusal naming memory on a genuinely loaded host is the correct outcome and is not a failure of this holdout.*
21. **(edge, upgrade)** An operator upgrading an install where they were signed into a site: after the upgrade, every workspace is **logged out** and the release note said so. `~/.omnipus/browser/profiles/default/` is still on disk, untouched. *Nobody inherited anyone else's session.*
22. **(edge, audit)** After a delegated agent completes a task involving ten browser actions on a signed-in site, the operator opens Settings → Security → Audit Log. **The log renders** (it is not blank), and it shows ten entries naming the agent, the tool and the host — not one. *A blank viewer here is #667's failure mode and means an event name carried a dot.*
18. **(edge, disclosure)** Operator adds an agent to a workspace that is signed into a real site. Before confirming, the UI tells them the agent will be able to act as that signed-in user, including on turns nobody is watching. They can decide with that in front of them.
23. **(edge, platform — macOS, the D1.5b ruling's own scenario)** On a Mac, open browsers in several workspaces until the machine is genuinely under memory pressure (a large video call and a few Xcode builds will do it), then ask an agent to browse in one more workspace. **It is refused, and the refusal says the host is out of memory** — not "it could not be adopted", and not a setting to raise. *Before D1.5b this scenario had no failure mode to observe at all: the gate read `0` and admitted every request, so the observable outcome was the machine swapping until something died.*
24. **(edge, platform — Windows, the declared gap)** On Windows, before browsing anything, read the release notes and the browser section of the config documentation. **Both say the pool has no memory-derived limit on this platform.** Then browse: the first browser opens, and an attempt to grow the pool is **refused** naming memory rather than admitted. *Two failures to watch for and they are opposite: the pool growing without limit (the gate defaulted through), and an operator who could only have learned this by running out of memory (the gap was never declared).*
25. **(edge, agent concurrency — the announcement, and the number the operator actually sees)** On a fresh install with `performance.max_parallel_agents` never set, open Settings → Performance. **The panel says the value is automatic and bounded by available memory. It does not display 2000, and it does not display it under the words "Live system recommendation."** Then read the release note for this version: it says there is **no longer a computed default**, and it says nothing about a default moving from 2 to 2000. Then set the value to 12 explicitly: the panel shows **12**, and the concurrency actually in use is 12. *Three failures, in descending likelihood: the release note carries the dissolved "2 → 2000" sentence (it was prescribed in writing, in this spec and in the ADR, before D1.5d removed it — it is one copy-paste away); the panel recommends 2000 to an operator who never asked for a number; and the explicit path breaks, which would mean the deletion sweep took `clampParallelExplicit` with it.* (FR-067, FR-069.)
26. **(error, agent concurrency — the host that cannot be measured)** On Windows, or on Linux inside a `/proc`-less sandbox (gVisor), start the gateway with nothing set and **send it an ordinary message. It answers.** Then ask for enough concurrent work to need a third agent at once: **the third is refused, and the refusal names memory.** *This holdout exists because the two failure modes are opposite and each looks like success from one side. If nothing is ever refused, the gate defaulted through and the host is unbounded — the exact false green FR-065 was written against. If the **first** message is refused, the refusal rule was read as "refuse to run" instead of "refuse to grow", and the product is dead on that platform while every refusal test passes perfectly.* (FR-068a.)

27. **(happy, and the one that proves implicit acquisition is real to a person)** Operator opens the live panel in workspace W, navigates to a page, and **releases control**. Asks Jim to fill in a field on it. **Jim does it, with no "take control" step and no permission prompt.** Then the operator **takes control again** and, while holding it, asks Jim to click something. **Jim declines with ADR-038 D6's existing wording, and the page does not change under the operator's hands.** *Both halves are required. The first alone is what a demo shows; the second is what an operator needs to be true, and it is the one an implementation can quietly lose (FR-070, FR-071).*
28. **(edge, disk — the failure that presents as something else)** Browse in five workspaces until each has a populated cache; record `du -sh` per profile. Leave the host idle for 25 minutes (past the 5m tab TTL plus the 15m idle-close TTL), then re-measure. **Each profile has shrunk to roughly its login-bearing size, and each workspace reopens still signed in.** Then drive **one** workspace continuously for an hour and measure it again: **it has not shrunk, and the operator can find that stated in the config documentation rather than having to deduce it** (FR-072, FR-073, FR-074, E-9). *This is a holdout rather than a test because the thing being checked is whether an operator can predict their own disk usage from what the product tells them.*

---

## 14. Annex — the write lease (NORMATIVE; the D2 spec references this, and must not restate it)

**Scope, and it has now changed twice — read this before the API.** On 2026-09-01 ADR **D1.9a** rescoped this annex from "every action tool" to "the operator's shared tab", on the premise that two agents can never address one tab set. On 2026-09-02 ADR **D1.9c** re-keyed ownership from the agent to the **session**. **The two moves do not compose the way the earlier draft of this note assumed, and the difference is the whole point of this paragraph.**

**The premise was never true, and D1.9c does not make it true.** It said *"two agents never share a tab set"* — correct, and irrelevant, because the contender is not a second **agent**, it is a second **turn**. Under D1.9a one agent could have two concurrent turns (a heartbeat beside a chat; the same agent dispatched twice by parallel delegation) sharing one per-agent tab set with no arbiter at all — that is round-4 blocker **C-402**. Under D1.9c those two cases are genuinely gone: a heartbeat runs on its own standing session and delegated siblings hold distinct `transcriptSessionID`s (§0.2a). **But three other paths start a second turn on an already-live session id** — `/loop`, async system-notify (the code's own comment at `pkg/agent/loop.go:3491-3510` says so and files it as **#505**), and cron `SessionModeMain` — so a session's tab set has exactly the same exposure the per-agent set had. **The general case is therefore rewritten, not deleted** (FR-081).

| Contention | Arbiter | Status |
|---|---|---|
| **Operator vs agent**, any tab | `LiveViewRegistry.TakeControl` / `IsControlled` (`live.go::TakeControl` `:1241`, `::IsControlled` `:1313`), ADR-038 D6 | **Unchanged.** Not this annex's business. The `{"deferred": true}` shape and its reason text are untouched, so no prompt needs rewriting |
| **Two turns**, on the **operator's** workspace-owned tab | **§14's write lease** | Arbitrated. Reached whenever two turns address `TabOwnerWorkspace()`, whatever agent or session each is running as |
| **Two turns in the SAME session**, on that session's own tab set | **§14's write lease** — same primitive, wider trigger (**FR-081**) | **REWRITTEN 2026-09-02.** This row previously read *"nothing — it cannot occur"*, and both the premise and its supporting scenario were wrong. Three named paths reach it (§0.2a); one is a filed defect, **#505**. **This spec must not depend on #505 being fixed** — §0.5 **E-10** |
| **Two turns in DIFFERENT sessions**, each on its own session's tab set | *nothing — it cannot occur* | Structurally impossible under D1.9c (FR-080): different sessions hold different `sessionEntry` values. **US-22/AC8** asserts it, so a change that re-merges the tab sets fails the concurrency suite as well as US-22/AC6 |

**The practical effect is that the lease is reached less often than before D1.9a, and more often than the D1.9a-only draft of this annex claimed.** The primitive below is unchanged; what changes is *when* `leaseWrite` is consulted — **FR-081** widens FR-021's `TabOwnerWorkspace()`-only trigger to include a session's own tab set, because "no second writer can reach this set" is true only across sessions, never within one.

> **⚠️ Do not re-narrow this on the reasoning that the three paths are rare or are somebody else's bug.** That is exactly the reasoning that produced the deleted row: a true statement about agents was read as a statement about writers, and the scenario written to defend it asserted the premise rather than the property, so it stayed green while the hole was open. **The lease costs one uncontended mutex acquisition per gated call.** A wrong-way error here is two turns interleaving CDP commands on one page, which ADR §5 names the most expensive failure class in this design.

**How an agent BECOMES a contender here is now RULED, and the annex does not change shape (ADR D1.9b ruling 1, 2026-09-01).** Earlier revisions said this annex *assumed* implicit acquisition on first write *"only as the lease's contended case, and says so"*, with the alternative — an explicit take-control tool — left to the D2 spec (§0.5 E-1). **The operator ruled implicit:** an agent acquires the operator's shared tab **by acting on it**; there is no acquisition call, no seventh tool and no seventh policy entry (§0.7, FR-070). **So the lease is entered by acting, not by asking**, and nothing in §14.1 or §14.2 is rewritten: `acquireWrite`, `leaseWrite`, FR-019…FR-024 and FR-019a all describe what happens *once two agents are contending*, which is a question of the resolved `TabOwner` on each call and never of a prior acquisition. **What the ruling DOES add to this annex is a load-bearing reading of rule 1**, below: the `controlledResult` step is no longer only "a human outranks an agent queue" — it is the sole thing preventing implicit acquisition from being a silent takeover, and FR-071 requires it to be asserted in the **blocked** direction.

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
// It is mutual exclusion per (BrowsingKey, TabOwner) PAIR — not per browser
// and not per manager-mutex — held for the duration of ONE action-tool call.
// It is NOT m.mu and must never be taken while m.mu is held: an action tool
// blocks on CDP for seconds, and the ADR-038 "no lock across a blocking call"
// discipline forbids it.
//
// THE PAIR IS THE WHOLE POINT, and a per-BROWSER lease is a DEFECT (D1.9c,
// FR-081). A BrowsingKey is "ws:<workspaceID>" — ONE key for every session on
// the workspace — so a lease scoped to the key alone makes two turns in two
// unrelated chats block each other on a tab neither can see. That is what
// test 99(b) TestLease_TwoSessionsNeverBlockEachOther fails on, and the
// mistake is invisible under load: it looks like contention, not like a bug.
// Revision 5 of this annex said "per-BROWSER" while requiring exactly that
// test, which is why the owner is now in the signature rather than in prose.
// SC-029 forbids a second acquire symbol, so this cannot be corrected later
// by adding an owner-aware variant beside it.
//
// It is reached whenever the resolved TabOwner is TabOwnerWorkspace() — the
// operator's shared tab — OR a TabOwnerSession() set, i.e. on every leased
// tool call (D1.9c, FR-080, FR-081). It is NOT reached across sessions,
// because two sessions hold different sessionEntry values and cannot address
// one another's tabs at all.
//
// FR-021's earlier "TabOwnerWorkspace() only" trigger is SUPERSEDED. It rested
// on "no second writer can reach an agent's own tab set", which is true across
// sessions and false WITHIN one: /loop, async system-notify (pkg/agent/
// loop.go:3491-3510, filed as #505) and cron SessionModeMain each start a
// second turn on an already-live session id. See §14's scope table.
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
    ctx context.Context, key BrowsingKey, owner TabOwner, holderAgentID string,
) (release func(), ok bool, holder string)

// leaseWrite is the convenience wrapper every action tool actually calls. It
// adopts the D2 spec's pre-built-*ToolResult* shape so D2's FOUR leased new
// action tools (select_option, press_key, hover, upload_file — its other two,
// snapshot and handle_dialog, are exempt; see §14 rule 3's normative counts)
// do not hand-roll the deferral body, while keeping D1's stronger primitive
// underneath (cancellable, bounded, names the holder).
//
//   deferred, release := leaseWrite(ctx, mgr, key, owner, agentID, "browser_click")
//   if deferred != nil { return deferred, nil }
//   defer release()
//
// owner is the TabOwner the call already resolved (§14.2 rule 1 step 1) — the
// session's own set or the workspace's. It is a PARAMETER and not re-derived
// here: the lease must be taken on the same set the tool is about to write,
// and a wrapper that resolved its own owner could disagree with its caller.
//
// deferred is nil iff the lease was acquired. When non-nil it is a NON-error
// result whose body is {"deferred": true, "reason": <text naming the holder>}.
func leaseWrite(
    ctx context.Context, m *BrowserManager, key BrowsingKey, owner TabOwner,
    agentID, toolName string,
) (deferred *tools.ToolResult, release func())
```

### 14.2 Rules

1. **Composition order is fixed, and one branch is now short-circuited by ownership.**
   1. **Ownership first, and it now selects the SCOPE of the lease rather than skipping it (D1.9c, FR-080, FR-081).** The resolved `TabOwner` decides *which* set is being written — `TabOwnerSession(transcriptSessionID)` or `TabOwnerWorkspace()` — and both are leased. `controlledResult` applies to both (a human can take the wheel on any tab, ADR-038 D6). **Two owners are never the same set**, so a lease taken on one never blocks the other, and cross-session contention cannot arise at all.
      **⚠️ This step used to read *"if the resolved `TabOwner` is an agent's own set, neither gate is reached"*, and that was the deleted general case in disguise.** It is wrong for the same reason: the second writer is a second *turn*, not a second *agent*, and three paths put two turns on one session id (§0.2a, §14's scope table, FR-081). Restoring the skip reintroduces C-402 under a new key.
   2. **`controlledResult` next.** A human holding the wheel outranks an agent queue (ADR-038 D6). When a human holds control the lease is **never acquired** (FR-022), and the result is the existing `{"deferred": true, "reason": …}` shape with its existing text — **unchanged, so no prompt needs rewriting.**
      **⚠️ This step is now the mitigation for implicit acquisition, not only a precedence rule (D1.9b ruling 1).** Because an agent acquires the operator's tab **by acting on it**, this check is the only thing standing between "an agent drove the tab I had finished with" and "an agent drove the tab I am using right now". **Do not reorder it behind `leaseWrite`, and do not skip it on `TabOwnerWorkspace()` on the reasoning that the lease already arbitrates** — the lease serialises two *agents* perfectly while a human sits locked out of their own tab, so every lease test stays green as the mitigation leaves. It also depends on **FR-002c**: `controlledResult` asks `IsControlled(defaultSessionID)` today (`pkg/tools/browser/tools.go:963`), which returns `false` forever once the registry is re-keyed — an intact, populated lock that is never consulted. **FR-071 and test 90 assert the blocked case, the resolved key, and this ordering**, and carry a mutation receipt (SC-025) because the allowed direction alone is green on a build with no lock at all.
   3. **`leaseWrite` last**, on the **resolved `TabOwner`** — session or workspace (**FR-081**). Not "only on `TabOwnerWorkspace()`": that was D1.9a's trigger, it is **superseded**, and step 1 above forbids it. The lease is taken on the `(BrowsingKey, TabOwner)` pair the call already resolved, so a lease on one owner never blocks the other. **SC-028's receipt requires test 99(c) to be RED against a build restoring the `TabOwnerWorkspace()`-only trigger** — a normative rule that still carries that trigger makes the receipt unobtainable from the spec an implementer types from.
   **The two gates' outcomes are no longer symmetrical, and that is deliberate.** The human-control branch defers immediately, because a human is present and the agent is meant to stop. The writer-vs-writer branch — two **turns**, on the operator's tab or on one session's own set — **retries and expects to succeed** (FR-020), because there is no such reader: a model that treats a non-error `deferred` as success continues against a stale page, which is the silent-no-op failure D2.10 exists to prevent. Same shape, different semantics, and the reason text must distinguish them.
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
   | `browser_evaluate` | yes (`tools.go:879`) | **leased** | arbitrary JS. **Reference update only (ADR D1.9b ruling 2):** `sandbox.browser_evaluate_enabled` is seeded **true**, so the capability now matches the `allow` an operator already reads at `pkg/coreagent/core.go:1058` and `pkg/config/defaults.go:282`. **That lands on the sibling D2 spec and changes nothing here** — being enabled does not change whether a tool is control-gated, and this row was already derived from `controlledResult` |
   | `browser_switch_tab` | yes (`tabs.go:113`) | **leased** | mutates tab state |
   | `browser_close_tab` | yes (`tabs.go:171`) | **leased** | mutates tab state |
   | `browser_open_tab` | yes (`tabs.go:239`) | **leased** | mutates tab state |
   | `browser_screenshot` | no | **exempt** | read-only; ungated today |
   | `browser_get_text` | no | **exempt** | read-only; ungated today |
   | `browser_wait` | no | **exempt** | read-only; ungated today |
   | **`browser_list_tabs`** | **no** (`tabs.go:20`: *"Read-only — NOT gated by controlledResult"*) | **exempt** | **The omission that would have broken the headline demo.** It is registered (`register.go:76`), read-only, and was in neither of the previous draft's categories — so under the old AC5 it would have taken the **write** lease, making Jim's `browser_list_tabs` (behavioural contract 1, US-1/AC1, the headline BDD scenario) return `{"deferred": true}` behind another agent's long `browser_navigate`. Round-2 CRIT-104 |
   | `browser_snapshot` (D2 FR-018) | no (D2 must keep it so) | **exempt** | read-only — falls out of the rule, needs no exception. **Reference update only (ADR D1.9b ruling 3):** the snapshot is **Tier 3 — searchable only**, consistent with the other eleven browser tools, which falsifies D2.4's *"the default way an agent reads a page"*. **That lands on the sibling D2 spec and changes nothing here** — a tool's tier does not affect its lease membership, which follows `controlledResult` alone |
   | `browser_handle_dialog` (D2 FR-035) | **no — D2 must register it ungated** | **exempt** | **Recovery, exempt from BOTH gates and that is what makes it coherent.** A JS modal blocks the renderer, so the panel's own `Input.dispatch*` injection is blocked too: a human holding the wheel is **equally** unable to clear it, and only `Page.handleJavaScriptDialog` can. Exempting it from the lease but not the control lock would leave the tab wedged for the human as well. See §12 A17. **⚠️ What it does to OWNERSHIP on the operator's tab — stated because it is the one tool that reaches that tab through neither gate (round-4 M-402): nothing.** Clearing a modal on `TabOwnerWorkspace()` leaves the tab owned by the workspace, takes no lease and consults no lock; and since acquisition is implicit and has no representation at all (FR-070, §0.7's C-403 note), it leaves nothing for a later call to observe either. **It is not an implicit-acquisition path and must not become one.** If it ever carried the side effects a leased tool's success implies, it would be the single tool by which an agent could act on a tab a human is actively driving — which is the exemption's price, and exactly what FR-071's mitigation does not cover. Asserted as case (e) of test 90 |
   | `browser_select_option`, `browser_press_key`, `browser_hover`, `browser_upload_file` (D2) | yes (D2 must gate them) | **leased** | mutate the page; automatically in scope by the rule, with no edit here |

   **What test 18 checks, and why it can now fail for the right reason.** It enumerates the **registry** and exercises each registered `browser_*` tool twice — once with a human holding the control lock, once with another agent holding the write lease — asserting the two deferral answers **agree** for every tool. That is the biconditional, checked behaviourally against shipped code. It is no longer possible to make it green by editing a list in this document; making it green requires changing a tool's actual gating.

   **The one cross-spec obligation is now an ADR ruling, not a request.** D2 must register `browser_handle_dialog` ungated by `controlledResult`, and must gate its four action tools. **And each of D2's six new tools needs an explicit policy seed in the same commit that registers it (ADR D2.9a, §0.7):** a registered tool with no seeded policy does **not** abort boot — `repairAndValidateToolPolicyCoverage` (`pkg/gateway/gateway.go:1318`) backfills an explicit `deny` at `:1335` before validating at `:1346`, so the tool is **silently denied on every agent** and the model never sees it. **ADR-072 D1.8 decides the first directly** — *"`browser_handle_dialog` is exempt from the write lease **and** from `controlledResult`… a modal blocks every CDP command on that tab, including the live panel's own input injection, so the human at the wheel is equally stuck… This narrows ADR-038 D6's exclusivity by exactly one tool"* — with the same reasoning §12 A17 reached independently, plus the ADR-038 narrowing that only the ADR can authorise. So the biconditional's one awkward member is ruled, not assumed, and D2 registering it gated would contradict the ADR rather than this spec.

   **The lease is reached on EVERY owner — the operator's tab and a session's own (D1.9c, FR-081).** The table above says which tools *would* take the lease; **FR-081** says *when* the question is asked at all, and the answer is now "always, on the resolved owner". **This paragraph used to read *"the lease is reached only on the operator's tab (D1.9a) … on an agent's own tab set no `browser_*` tool consults the lease, because no other agent can address that set"*, and that reasoning is the deleted general case in miniature** — no other *agent* can, but a second *turn* in the same session can, by three shipped paths (§0.2a). What does **not** change is test 18's setup: the biconditional is still exercised against `TabOwnerWorkspace()`, because that is where **both** gates are live and the classification is actually visible. Running it against a session's own tabs would exercise the lease but never `controlledResult`'s human-held branch, and would pass with the classification half-checked.
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
| **MAJ-108** — the idle window is named five times and defined nowhere | **ACCEPTED IN FULL.** Config key **`tools.browser.idle_close_ttl`**, default **15m** with its derivation stated (3× the per-tab `idle_ttl` at `manager.go:134`, and why the asymmetry is right: per-tab reaping already reclaims the renderers, the dominant cost). **Caller named:** the existing 1-minute sweep goroutine (`gateway.go:5321-5355`), after its `ReapIdleSessions` loop — a caller the previous draft never identified. **Post-close state specified, and the tension the review found is resolved by naming it:** FR-026a's liveness is about the **key** (does this workspace still warrant a browser), the pool's is about the **process**; they are different questions, so a live key with no running Chrome is a legal, described state. `browserMgrs` entry and `*BrowserManager` survive; `pool.Acquire` relaunches from the profile; login intact | **STANDS, with one addition eviction makes load-bearing.** The 15-minute default and its 3× derivation are unchanged, but the two TTLs **compose**: a workspace browsed once occupies its slot for the 5-minute tab TTL **plus** the 15-minute idle-close TTL — ~20 minutes after the last action. The target therefore bounds workspaces browsed *within any ~20-minute window*, not workspaces browsing *right now*, and it sets the floor for FR-054's thrash window. §12 A22 carries it | FR-040a, §12 A22, US-12/AC4a, BDD *idle-close-relaunch*, tests 54 + 55, §10.2 |
| **MAJ-109** — `max_browsers` has no edge semantics and no stated relation to the tab budget | **ACCEPTED IN FULL.** Verified `maxTotalTabs <= 0` means unlimited (`coordinator.go:785-788`). **(a)** `max_browsers <= 0` means unlimited too — same shape, so an operator who knows one key is not surprised by the other. **(b)** `max_total_tabs` stays **global** across all N Chromes; making it per-Chrome would silently multiply a configured ceiling by N. The starvation consequence is stated: it is today's behaviour across agents and the pool does not worsen it. **(c)** on the default being measured on one box — that is exactly what FR-044/G-1 is, and §12 A21 states plainly that it is a fixed conservative constant operators on larger hosts must raise, rather than a function of host memory (auto-sizing from RAM would need its own measurement to be honest, and G-1 has not run yet) | FR-038a, §12 A21, US-15/AC5+AC6, test 53, §10.2 **⚠️ SUPERSEDED BY ADR D1.5a — both keys are deleted, so neither has edge semantics and there is no relation to state.** FR-038a and test 53 are tombstoned. **The finding's principle survives as AC7 and SC-022:** no shipped setting may imply a bound that does not exist, and no message may name one. |
| **MAJ-110** — no boot reconciliation; orphan Chromes sit outside the cap | **ACCEPTED, with the shipped behaviour described accurately.** The review implies orphans are silently ignored; in fact the marker is consulted **at launch** and the shipped path **refuses to launch** when a marker's pid is alive (`coordinator.go:1448-1467`) — a reasonable single-Chrome story that is not a boot-time story. The review's real point stands: orphans consume the host memory the cap exists to bound while `LiveKeys()` cannot see them. FR-042a adds the boot pass (dead pid ⇒ remove marker + stale lock, INFO with a count; live omnipus-owned pid ⇒ terminate, WARN with workspace and pid). **§12 A20 records what the review did not raise:** pid reuse is a real hazard, the shipped code has it too, and the `/proc/<pid>/exe` mitigation is **Linux-only** — on macOS the marker is removed without terminating and a WARN names the pid, a smaller guarantee stated rather than implied | FR-042a, §12 A20, §6, US-19, SC-016, test 56, §10.2 |
| **MAJ-111** — the profile directory has creation but never deletion | **ACCEPTED IN FULL; decided as delete-on-workspace-deletion.** FR-043a: **workspace deletion is the sole trigger**, after `pool.Close(k)` returns; idle close, roster change, reload, operator close and crash recovery all leave it — five negative cases in test 58, because the positive case alone would pass a "delete always" bug. Directory mode **0700**, stated rather than inherited (matching `coordinator.go:1232`, `manager.go:799`), because these now hold per-client session cookies. **No quota, and the reason is given** rather than omitted: live profiles are bounded by `max_browsers`, and dead ones are removed by the deletion path, so the unbounded case the review worries about is closed by deletion rather than by a ceiling. A release-note line is required | **PARTLY SUPERSEDED.** The delete-on-workspace-deletion decision stands and eviction joins the negative cases (four, not five — the operator-close trigger went with FR-046). **But the "no quota" reasoning is WITHDRAWN:** `max_browsers` bounds live processes, not bytes, and an idle-closed or evicted profile's cache is deliberately *not* reclaimed, so N browsed-once workspaces leave N unbounded caches. Reopened in §12 A24(b), matching ADR §4 and §6 | FR-043a, US-20, SC-017, test 58, §5, §12 A24 **⚠️ ONE CLAUSE SUPERSEDED BY ADR D1.5a.** Its "no quota, and the reason is given" clause rested on *"live profiles are bounded by `max_browsers`"* — a key that is now deleted, and which bounded **processes** rather than bytes even when it existed. **The quota question is therefore reopened, not closed** (§12 A24(b)): idle-closed and evicted profiles keep their caches by design, so N workspaces browsed once each leave N unbounded directories. The rest of the row — workspace deletion as the sole trigger, 0700, the negative cases (now **four**, with eviction replacing the withdrawn operator close) — is unchanged. **✅ THE QUOTA HALF IS NOW RULED AND THIS ROW IS CLOSED (ADR D1.9b ruling 4, operator, 2026-09-01).** The withdrawal above was correct and the answer is **periodic cache trimming**, not a quota and not deletion alone: logins preserved, disposable cache trimmed on a schedule (§0.8, **FR-072 / FR-073 / FR-074**). The original *"no quota, and the reason is given"* clause stays withdrawn — its reasoning rested on `max_browsers`, which is gone and bounded processes rather than bytes even when it existed — **and it is replaced by a mechanism rather than by a justification.** **A hard per-workspace size cap was rejected by name**, structurally: at the moment such a cap binds, the only large items in the directory are the cache **and the logins**, so it must discard one of them mid-session (§12 **A28**). **What is left open is narrower and is escalated, not assumed:** a workspace under continuous drive never becomes eligible for trimming (§0.5 **E-9**, declared by FR-074). §12 A24(b) records the same closure. |
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

## 17. Round-3, round-4 and round-5 review dispositions

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

### 17.4 Round-4 second pass (2026-09-02, in-session) — all 15 findings

| ID | Disposition | Where |
|---|---|---|
| **C-401** — the spec says two opposite things about the first browser on an unmeasurable host | **ACCEPTED IN FULL, and the fix is a definition rather than a choice of sides.** The diagnosis was exact: AC17, FR-065 and test 83 read as refusing the **first** launch (*"a launch is refused … the run FAILS if either succeeds"*), while §13 holdout 24, US-15/AC23, SC-027 and FR-068a read the first as **succeeding** (*"the first browser opens, and an attempt to grow the pool is refused"*) — with FR-068a citing that reading **by name** as the basis for the agent floor. An implementer could satisfy either and fail the other's test. **`"Refuse to grow"` has no meaning until `"grow from what"` is fixed, and this document never fixed it.** **FR-082 fixes it at ONE browser and ONE tab, per HOST.** AC17 is amended in place (its subject is unchanged; only the boundary is stated) and **test 83 now asserts the SECOND launch and SECOND tab**, so it and test 93 can no longer disagree. **The reviewer's own gVisor finding is recorded as the reason for one rather than zero:** a `/proc`-less **Linux** host — a deployment §0.9 spends a section establishing as **supported** — reaches `ok=false` through `meminfo_linux.go`'s fallback, so a floor of zero removes browsing from it entirely on the strength of a reading the host declines to give | **§0.6a** (the floor, with both bounds argued), **FR-082**, US-15/AC17, scenario *unmeasurable-host-refuses-to-grow* (both directions), test 83, two §10.2 rows |
| **C-402** — §14's scope table deleted the general lease case on a false premise | **ACCEPTED IN FULL — and the operator resolved it by ruling, but NOT entirely "by construction" as ADR D1.9c expects.** The diagnosis was exact: the row read *"nothing — it cannot occur"* on the premise that two agents never share a tab set — true, and irrelevant, because **the contender is a second TURN, not a second agent**. **ADR D1.9c closes the two cases the finding named** — a heartbeat runs on its own standing session (`pkg/gateway/rest_workspaces.go:1236` → `pkg/session/unified.go:696-697`, reused every fire via `pkg/gateway/heartbeat_schedule.go:216-217` → `pkg/gateway/schedules.go:516`, `:530`, `:543-545`) and delegated siblings hold distinct `transcriptSessionID`s (`pkg/agent/subturn.go:1282`) — so those two are gone by construction, exactly as the ADR says. **The ADR then asks this spec to establish the residual from code rather than inherit it, and the answer is YES: two turns CAN run on one session id**, by three shipped paths — `/loop` (`pkg/agent/loop_command.go:90` → `loop_scheduler.go:118`, `:215`), async system-notify (`pkg/agent/loop.go:3510` → `:3516` → `:7640-7643` → `:7734`, which the code documents as breaking the single-writer invariant at `:3491-3510` and files as **#505**), and cron `SessionModeMain` (`pkg/gateway/schedules.go:548`). **So §14's third row is REWRITTEN, not deleted**, and FR-081 widens the lease trigger. **The finding's second half — that the supporting scenario ASSERTS the premise and therefore passes while the hole is open — is the sharper half and is accepted with it:** US-9/AC0 asserted *"the lease was never acquired"* and was green **because** it was never acquired. That AC and its scenario are tombstoned rather than edited, with the reasoning recorded in place | **§0.2a** (the code trace and the three paths), **§14** scope table row 3 + rule 1 step 1 + `acquireWrite`'s doc comment, **FR-080**, **FR-081**, US-22/AC6-AC8, US-9/AC7, tests 97–99, **SC-028**, **§0.5 E-10** |
| **C-403** — "driver" is asserted six times with no definition and no observable | **ACCEPTED IN FULL, and the ruling asked for is: DELETE it at all six sites.** D1.9c was the obvious place for the term to acquire a referent and **does not give it one** — the re-key moves the *agent* tab set to the *session* and leaves `TabOwnerWorkspace()` exactly as it was, so the operator's tab is still owned by nobody nameable; and the lease, the only per-call exclusion that exists, is released by `defer` at the end of the call (§14 rule 4), so **nothing survives for a later assertion to read.** The contradiction the finding names is real: **FR-070 forbids every representation a driver could have** — no tool, no policy entry, no wire field, no result key — so *"Then A is the driver"* is unfalsifiable and its negative twin *"no driver state changed"* is **vacuously true on every build this spec permits.** **Replaced by three assertions that can all fail:** the call took effect on the tab, the owner is **still** `TabOwnerWorkspace()` afterwards, and no acquisition call was made | **§0.7** (the decision, recorded so the phrase is not reintroduced), FR-070, behavioural contract 25, §5, US-22/AC5, US-9/AC6, scenarios *operator-tab-is-taken-by-acting* and *implicit-acquisition-is-blocked-while-a-human-drives*, tests 89 + 90, §10.2 |
| **M-401** — no atomicity rule for FR-002c | **ACCEPTED IN FULL.** Stream A carried atomicity rules for **FR-080** (FR-048's D1.9c successor — this line cited the tombstone until round-5 m-503) and FR-059 and none for FR-002c, and the gap is the worst-shaped of the three: between the re-key commit and the FR-002c commit, `controlledResult` asks `IsControlled(defaultSessionID)` about a key nobody takes, so **the human control lock is silently dead** — an agent can drive a tab a human is holding, and every existing lease test stays green because none of them takes the lock. That window is also the one in which D1.9b ruling 1's *"the lock is the whole mitigation"* describes a control that is not running. **FR-002c lands in the same commit as the re-key**, and the commit message names it | **§3.2 Stream A**, third atomicity rule; §17 step 2 |
| **M-402** — `browser_handle_dialog`'s effect on ownership is unstated | **ACCEPTED IN FULL.** It is the one tool that reaches the operator's tab through **neither** gate, so "what does it do to ownership" is the question its exemption most obviously raises and the document never answered. **The answer is: nothing** — no lease, no lock, no ownership change, and (since acquisition has no representation at all, C-403) nothing observable left behind. **Stated with the prohibition that gives it teeth:** it must not become an implicit-acquisition path, because it would then be the single tool by which an agent could act on a tab a human is actively driving — the exemption's price, and exactly what FR-071 does not cover. **Asserted as case (e) of test 90** | **§14 rule 3** table row, **test 90 case (e)** |
| **M-403** — three live citations use the superseded nested profile path | **ACCEPTED IN FULL.** The BDD relaunch step, test 57 and one §10.2 row all said `<profileRoot>/ws/W/` — revision 3's **nested** layout, which FR-037a replaced with the flat `ws-<id>` because the nesting was the sole cause of INVARIANT P-5, and which **test 52 asserts is never constructed**. Test 57 is the one that mattered: a fixture planting a `SingletonLock` at the nested path plants it where the implementation will never look, so **the test passes without the cleanup running at all** | Scenario *idle-close-relaunch*, **test 57**, §10.2 |
| **M-404** — two live preconditions name symbols FR-059 deletes | **ACCEPTED IN FULL.** The BDD Given for *reaper-cancels-while-pool-live* and **US-12/AC6** both required `releaseGlobalTab` → `coord.ReleaseTab` to have been called, while **FR-059 deletes both and test 78 asserts they resolve to nothing repo-wide, `_test.go` included**. A fixture written to that Given does not compile in the tree it is meant to run against. **The contract does not depend on it** — the `browserCancel` cancellation alone produces the state both are about — and the pre-change call at `manager.go:3118` is a fact about the *current* tree, which is where §2.1 already records it | US-12/AC6, scenario *reaper-cancels-while-pool-live*, §2.1 |
| **M-405** — Streams M and P share files and disagree about gating | **ACCEPTED IN FULL.** P is **gated on G-1 + G-2 + G-6 and lands last**; M carried **no gate**; and the two edit `pkg/config/meminfo_linux.go`, `pkg/config/config.go` and the `meminfo_*.go` set between them — a merge collision by construction, with the ungated stream landing first. **Resolved by splitting M rather than gating it:** its `pkg/config` half lands **with** P's FR-064/FR-065 (one accessor, one commit — FR-078/FR-079 are that accessor's *shape*, not consumers of it), and its consumer half lands after that commit and before P's gate opens. **G-1/G-2/G-6 gate the POOL, not the memory mechanism** — nothing in FR-067…FR-069 or FR-075…FR-079 consumes any of them, and gating M on them would hold a `pkg/config` correctness fix behind a browser benchmark | **§3.2 Stream M**, gating note |
| **M-406** — nothing fails if the sibling D2 spec reintroduces a lease under a different symbol | **ACCEPTED IN FULL, and it is the cleanest of the six to close.** §14's *"required action in the D2 spec"* is a **request to another document** with no local gate, and §14 itself states the cost of it being ignored: *"the seven action tools would have taken two unrelated mutexes and mutual exclusion would have been lost for whichever tool took only one"* — the nondeterministic interleaving ADR §5 calls the most expensive failure class here. **SC-029 is the gate, and it matches by SHAPE rather than by name** — any `*BrowserManager` method or `BrowsingKey`-taking function returning a release closure plus a bool or holder string, other than `acquireWrite`, fails it. A grep for the literal `acquireWrite` would pass against a second primitive called anything else, which is precisely the case | **SC-029** |
| **m-401 … m-406** — six code facts that do not reproduce | **ALL SIX ACCEPTED, and the set was re-derived from source in this pass rather than taken on trust** (the review was in-session, so its list could not be re-read). **(1)** `readMemTotalBytes` and `readMemAvailableBytes` are declared `func …() uint64` (`pkg/config/meminfo_linux.go:26`, `:40`) — **neither returns a bool at all**, so §0.10's *"`ok` is true"* is wrong in a way that matters: it implies a two-valued reader whose flag is set incorrectly, a one-line fix, when the work is FR-078's signature change and every caller. §0.10's own case-2 body already said this correctly, so the document contradicted itself across two pages. ⚠️ **The ADR's D1.5e matrix carries the same error — flagged for the ADR owner, not fixed here**, alongside §0.10's other correction to that matrix. **(2)** The reaper goroutine spans **`gateway.go:5321-5355`** (`go func() {` at `:5321`, closing `}()` at `:5355`); the document said `:5321-5352` in five places and `:5321-5356` in one — **internally inconsistent, and both wrong**. **(3)** `applyReconcileOutcome` is declared at **`tools.go:321`**; `:346-356` is its reason switch, which is what three citations actually pointed at. **(4)** `TestReadMemAvailableBytes_MissingFile` spans **`:55-65`**, not `:55-64`. **(5)** Its oracle `want := uint64(fallbackTotalRAMBytes) / 2` is at **`:61`**, not `:60`. **(6)** `TestReadMemAvailableBytes_FallsBackToHalfTotal` spans **`:43-50`**, not `:43-49`. *(4)–(6) are one systematic slip — each span excludes the closing brace.* **What DID reproduce, recorded so a later pass does not re-check it:** `controlledResult` at `tools.go:962` with its `IsControlled(defaultSessionID)` at `:963` (both citations correct, not a contradiction); `manager.go:36`/`:124`/`:203-204`/`:338`/`:134`/`:2108`/`:2216`/`:2223`/`:2286`/`:2287`/`:1605-1613`; `config.go:608`/`:614-618`/`:655-661`/`:3633`/`:3678`/`:3682`; `meminfo_other.go:15-33`/`:20-23`/`:25-33`/`:42-44`/`:43`; `meminfo_other_test.go:10-21`; `meminfo_linux.go:226-240`/`:232-234`; `sandbox_apply.go:179`/`:185-201`; `live.go:1241`/`:1313`; `compositor.go:436-438`; `register.go:41-84`/`:65`/`:76`/`:81`; `tabs.go:19`/`:20`/`:32`/`:86`/`:143`/`:171`/`:186`/`:206`; `tools.go:63`/`:415`; `exec_resolver.go:50`; `defaults.go:282`/`:671`; `coreagent/core.go:466`/`:848`/`:1058`; `AuditEntry.yaml:17`; `unified_meta_files.go:60`; `x/sys@v0.47.0/windows/dll_windows.go:234`/`:249` | §0.10, §2.1, §3.1, §10, §10.2, §16, FR-040a, FR-063, test 81, test 96 |

### 17.5 Round-5 (2026-09-02, in-session) — all 18 findings

**The verdict this pass answers: every finding was a sweep-completeness failure on ONE ruling, not a design failure.** D1.9c was swept through the prose, ACs, scenarios, FR rows and tests — and **not** through the two normative code artefacts an implementer types from, §3.1's interface and §14.1's signatures. That is the whole shape of the CRITICALs.

| ID | Disposition | Where |
|---|---|---|
| **C-501** — §3.1's normative interface still specifies PER-AGENT ownership | **ACCEPTED IN FULL, and it was the worst-placed of the five:** it is Stream A's **first commit** and it ships inside Go doc comments, where a wrong sentence outlives the document. `BrowsingKey`'s doc said *"one per agent, plus one owned by the workspace"* **while citing D1.9c in the same sentence** — a citation that resolves cleanly to the wrong content, the exact trap §0.0 warns about. `TabOwnerAgent(agentID)` is now `TabOwnerSession(transcriptSessionID)`, the map is *one entry per SESSION*, the "no third shape" note gains a **"no `TabOwnerAgent`"** clause, and the acquisition verb is no longer *"a D2 decision — §0.5 E-1"*: **E-1 is RULED and acquisition is implicit** (FR-070) | **§3.1** — `BrowsingKey`'s doc, `TabOwner`'s doc, its constructors, `ManagerResolver.ManagerFor` |
| **C-502** — §14.1's `acquireWrite`/`leaseWrite` cannot implement FR-081 | **ACCEPTED IN FULL.** Neither took a `TabOwner`, and the doc said *"per-BROWSER mutual exclusion"* — but a `BrowsingKey` is `ws:<workspaceID>`, **one key for every session on the workspace**, so a per-browser lease makes two unrelated chats block each other. Contradicted by name in five places, test 99(b) `TestLease_TwoSessionsNeverBlockEachOther` included. Both signatures gain `owner TabOwner`; the scope is restated as **per (`BrowsingKey`, `TabOwner`) pair** at §14.1 and at §0.2a. **Fixed now rather than later because SC-029 forbids a second acquire symbol** — a correction could not have been additive | **§14.1** `acquireWrite`, `leaseWrite`; **§0.2a** *What the arbiter is* |
| **C-503** — §14.2 rule 1 step 3 carries the superseded trigger | **ACCEPTED IN FULL.** Step 3 read *"`leaseWrite` last, and only on `TabOwnerWorkspace()`"* **inside the rule whose step 1 forbids exactly that**, and SC-028's receipt requires test 99(c) to be RED against a build restoring it. Now: *on the resolved `TabOwner` — session or workspace (FR-081)* | **§14.2** rule 1 step 3 |
| **C-504** — two different requirements are both numbered SC-028 | **ACCEPTED IN FULL.** The FR-081 lease receipt and the fail-open closure shared a number and were **both cited bare**, from §3.2 Stream D and from §9. §11's own rule forbids reusing an FR number; it applies to an SC. The **fail-open one moves to SC-030** (five sites cite the lease receipt, one cites this), and the §9 citing site is swept | **§11** SC-028/SC-030; **§9** the D1.5e paragraph |
| **C-505** — INVARIANT P-2 and `Acquire` contradict FR-082's floor | **ACCEPTED IN FULL.** Both said **NO** instance and **NO** tab while the host is unmeasurable; FR-082/AC17/AC23/SC-027/holdout 24/test 83 require the **first** browser and the **first** tab to succeed, and test 83 **fails** if either is refused. **C-401's sweep fixed the ACs and left the two statements an implementer codes against** — a pool built from P-2 removes browsing from gVisor and GKE Sandbox, which §0.9 establishes as supported. Both now carry the floor, with both bounds argued in place | **§3.1** INVARIANT P-2, `Acquire`'s unmeasurable branch |
| **M-501** — `TabOwnerSession("")` is unspecified and reachable | **ACCEPTED IN FULL, and it is the clearest "question the document should have answered".** `opts.TranscriptSessionID == ""` is an ordinary handled state — re-verified at `pkg/agent/goal_loop.go:74`, `:555`; `pkg/agent/loop_command.go:78`; `pkg/agent/loop.go:7779`, `:8804`; and `pkg/agent/tool_manifest.go:12-20`. `BrowsingKey` had `IsZero()`, a single-construction rule, a §5 non-behaviour and `ErrNoBrowsingContext`; `TabOwner` had **none of the four**. It is now a **named failure** (`ErrNoTabOwner`), with a §5 non-behaviour that also forbids falling through to `TabOwnerWorkspace()`, and case (d) of test 97 | **§3.1**, **§5**, **§9** FR-080, **§10** test 97 |
| **M-502** — §2.1 says `tools.ToolTranscriptSessionID` is *"not used"* | **ACCEPTED IN FULL.** It is the **tab-ownership key** (FR-080), and §2.1 is the symbol inventory — the first place anyone looks. Re-verified at `pkg/tools/base.go:199-203` | **§2.1** |
| **M-503** — FR-021's test column names a test FR-081 inverts | **ACCEPTED IN FULL.** `TestWriteLease_OwnTabNeverAcquires` asserts the lease was **never** acquired on a session's own tab; test 99(c) asserts `acquireWrite` **was** called. Both named, the suite is unsatisfiable. The former is **tombstoned in place**, with the reason recorded — it is the tombstoned US-9/AC0's trap again: a Then that restates the premise | **§9** FR-021 |
| **M-504** — §10.1 gives opposite instructions for one test, six lines apart | **ACCEPTED IN FULL.** *Keep* `parallel_clamp_test.go:111-120` deriving from `availableRAMBytes()`, then *delete* `TestEffectiveMaxParallelAgents_Auto_MatchesMemoryFormula` (`:113-121`) — the same test. Its justification was stale twice: **E-7 is RULED** and **FR-067 deletes `autoDetectMaxParallel`**, the function the "keep" was protecting. The keep sentence is deleted, with a note so its removal is not read as coverage-stripping | **§10.1** |
| **M-505** — round-4's M-404 was not swept into §10.1 | **ACCEPTED IN FULL.** M-404 fixed US-12/AC6 and the *reaper-cancels-while-pool-live* Given and stopped there; §10.1 still told a reviewer to preserve coverage of `releaseGlobalTab`/`coord.ReleaseTab`, which **FR-059 deletes and test 78 asserts absent**. The reaper row now says so, and marks those assertions' removal an executed ruling rather than assertion-stripping | **§10.1** |
| **M-506** — behavioural contract 11 still says the case *"cannot arise under D1.9a"* | **ACCEPTED IN FULL.** Rewritten to the D1.9c reading: **different sessions** genuinely cannot contend (FR-080); **the same session** can, by three shipped paths, and takes the lease on its own set (FR-081) | **§4** contract 11 |
| **M-507** — FR-070's own requirement text still says *"driver"* | **ACCEPTED IN FULL.** §0.7/C-403 decided to delete the term at all six sites; five were swept and **the normative FR row was not**. Replaced with what is observable: the agent **acts on** the tab and the tab is **still the workspace's** afterwards | **§9** FR-070 |
| **M-508** — §5 forbids exceeding a target that no longer exists, and mis-states FR-053 | **ACCEPTED IN FULL, both halves.** The `+1` overshoot prohibition names a quantity D1.5a deleted and §3.1's `Acquire` deletes by name — a test asserting it passes vacuously against a pool with no gate. And FR-053 is **not a ceiling**: its trigger is *memory refuses AND nothing is evictable*, and its own text forbids advising an operator to raise a cap. Calling it a ceiling sends the reader looking for the setting that does not exist | **§5** |
| **m-501** — the contracts inventory says three schemas | **ACCEPTED.** **FR-069 makes `PerformanceSettings.yaml` a fourth.** SC-007 still holds — it is a `description:` change, adds no path, retypes no field — but *three* is what a reviewer would have checked the diff against. Swept at §2.1, §3.2 Stream E and the `verify-contracts` scenario | **§2.1**, **§3.2**, **§8** |
| **m-502 / m-503** — two sites cite tombstoned FR-048 where FR-080 is meant | **ACCEPTED, both.** §0.7's A25 consequence (*"FR-048's prohibition … another **agent's** tab"* — within one session that is not a thing the product has) and §17.2's M-401 disposition | **§0.7**, **§17.2** |
| **m-504** — dangling scenario name | **ACCEPTED.** *agent's-tabs-are-its-own* was tombstoned by D1.9c, so a reassurance pointed at nothing. Now names *session-tabs-are-the-chats-operator-tab-is-the-workspaces* and *two-sessions-never-contend* | **§8** |
| **m-505** — 13 unexplained test-number gaps; only 73 has a tombstone | **REJECTED — it does not reproduce, and the check is mechanical.** §10's TDD plan covers **1–99 with no gap**: 93 single-number rows plus **one range row, `12–17`** (the `TestWriteLease_*` family), which a per-row scan reads as six missing numbers. And **seven** numbers are tombstoned, not one — 10, 26, 53, 59, 65, 66, 73 — each with its own row stating the withdrawing ruling. §13's holdouts are likewise **1–28 complete**. *Recorded rather than silently dropped: the reproduction is `grep` over the row prefixes, and the next reviewer should run it before re-raising.* | **§10**, **§13** — no change |
| **m-506** — §5's path invariant is weaker than its stated threat | **ACCEPTED.** `filepath.Base(id) == id` validates the **id**, which is exactly right under revision 3's *nested* `ws/<id>` — there the id **is** the segment. Under D1.8's **flat** layout the segment is a **concatenation**, so the check no longer covers what reaches the filesystem, in the very bullet that says the flat form makes this *more* important. Now checked on the **rendered segment** as well, and a case-insensitive-filesystem collision residual is recorded as the different problem it is | **§5** |

**Four citations were re-verified and corrected in this pass, and one claim was re-derived rather than trusted** — see §0.2a's citation receipt. `loop.go:3491-3510` (#505's own comment), `turn.go:348-353`, `subturn.go:1282`/`:1339` and `delegate.go:1298` all reproduce exactly; the ruling's foundation is sound and only its supporting line numbers had drifted.

**ADR D2.9a (2026-09-02, commit `0be8988ac`) is absorbed in the same pass** — §0.7. Nothing in this spec relied on the falsified *"a registered tool with no seeded policy aborts boot"*, but the co-landing discipline is restated as **one atomic commit** rather than a sequence, because there is no loud failure in between.

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
| **C-305** — `max_browsers` shipped as a constant vs derived | D1.1b | **D1.5** | Accepted in full — same as round-4 **M7(b)**. FR-056 respecifies it as a derivation with `tools.browser.max_browsers` as `operator_ceiling`; test 51 is rewritten to catch a hardcoded target; test 73 tests the **formula** against fixture memory values on the established `meminfo_*_test.go` pattern; **`gateway_reserve` is added as a named quantity** (it was in the ADR formula and nowhere in this spec); and §6 states which way the reuse resolves — `pkg/config` **exports** a memory-budget accessor, because `availableRAMBytes` and the `meminfo_linux.go` readers are unexported and a spec that assumes an unexported symbol is callable is a plan that does not compile **⚠️ SUPERSEDED BY ADR D1.5a — there is no `max_browsers`, constant or derived.** FR-056 and test 73 are tombstoned; test 51 is re-derived onto the one figure that survives (`PER_BROWSER_COST` ≈182 MB, FR-062). **One part of this disposition survives and now blocks the only remaining control:** `availableRAMBytes` (`pkg/config/config.go:655`) and the `meminfo_linux.go` readers are still **unexported**, so `pkg/tools/browser` cannot call them — the export is now a prerequisite of FR-057, not of a deleted formula. **And a gap this row could not have seen:** off Linux those readers return **0** by design (`meminfo_other.go`), so the gate had no signal at all on macOS or Windows — §0.5 **E-6**, **since RULED by ADR D1.5b**: macOS gets a real reader (FR-064), an undeterminable host **refuses** rather than admits (FR-065), and Windows is declared `degraded-unsupported` (FR-066). **The export this row identified stands, and gains a shape** — `(bytes, ok)`, so "unknown" is distinguishable from "zero free". |
| **M-301** — thrash detection entirely missing | D1.1c | **D1.7** | Accepted. **FR-054**, test 71, US-15/AC8+AC8a, and **G-5** added to §0.3 with its constants explicitly gated on it |
| **M-302** — the cap is soft; three places assert it is hard | D1.1c | **D1.7** | Accepted. INVARIANT P-2 restated as `target + overshoot`; **SC-010 rewritten**; **test 63 split** into the evictable and all-pinned paths; the soft-target wording carried into the config doc comment as **FR-053 + test 77**, since ADR criterion P14 makes it a stated requirement |
| **M-303** — one eviction guard has no mechanism | D1.1c | **D1.7** | Accepted, with the grill's arithmetic: §14's exempt set is **six**, so a `browser_screenshot`/`get_text`/`wait`/`list_tabs`/`snapshot`/`handle_dialog` holds no lease and the pool would evict Chrome out from under it. **FR-051** adds `InFlight()`, incremented by **every** `browser_*` `Execute` and released by `defer`; the selection race is specified in §3.1's locking discipline (increment under the same `pool.mu` selection holds); test 68 is the `-race` case, deliberately on a lease-**exempt** call |
| **M-304** — the admission pressure gate is absent | D1.1b | **D1.5 item 3** | Accepted. **FR-057** with fixture tests at 0.84/0.85/0.86. **Two parts of this disposition are now superseded, and both by later rulings rather than by revision.** (1) *"a non-Linux no-op"* — **withdrawn.** ADR D1.5a made the gate the only control and D1.5b writes the missing reader: macOS measures (FR-064), an unmeasurable host **refuses** (FR-065), Windows is declared (FR-066). **A no-op gate is no longer an acceptable disposition of any finding in this document**, because there is no counter behind it. (2) *"the contradiction is escalated, not resolved"* — **resolved.** D1.5a rules the gate a hard stop, §0.5 E-2 is struck through, and **test 72 now asserts the case it deliberately left unasserted** |
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
2. **Land Stream A**, including **FR-080 and FR-081 in the same commits as FR-001** — a commit that re-keys the manager without carrying the **session** dimension on the tab set ships a state D1.9c forbids and ships it silently — and **FR-002c in that same commit** (round-4 M-401: between the re-key and FR-002c the human control lock is dead and every test stays green) — plus the §2.2a 364-reference migration in the same commits as FR-002b, and the rest of the §0.4 set with FR-047 among it.
3. **Then build Stream P**, behind G-1/G-2/G-6, with FR-034a's final description literals in the same commit as FR-037.

**Cross-document obligations that must be settled before implementation:**

- The **D2 spec** deletes its lease and references §14. §14's scope is writer-vs-writer on the operator's tab **and** on a session's own set (D1.9c, FR-081) — wider than the D1.9a-era "operator's tab only" this line used to claim, and the reference is still required.
- **`browser_handle_dialog` ungated by `controlledResult`** is no longer a request from this spec to D2 — **ADR D1.8 rules it**, with the ADR-038 D6 narrowing stated explicitly (§12 A17). D2 implements it.
- **§0.5's escalations need answers, and the list has now moved five times.** **E-2 is RULED** by ADR D1.5a — the pressure gate is a hard stop, the cap is soft, and with the cap deleted only the hard stop remains — so the implementation no longer has to avoid picking, and test 72 asserts the case revision 4 deliberately left unasserted. **E-6 is RULED** by ADR D1.5b: revision 5 escalated that the live-memory gate had **no signal at all** on macOS or Windows (`readMemAvailableBytes` returns 0 by design, `pkg/config/meminfo_other.go`), so on two of three supported platforms the only admission control that exists could not run — and D1.5a had deleted the counter that used to bound the pool there. **The operator ruled: write the macOS reader (in scope, FR-064); refuse rather than admit where availability is undeterminable (FR-065); foresee Windows but do not scope it, and declare the gap in code, release notes and config documentation (FR-066).** **Four remain live**, all of them the operator's or a downstream document's: **E-1** the "take control on request" verb (D2 surface), **E-3** `browser_snapshot`'s reachability under D1.9a (D2), **E-4** the three ADR §6 rows (ADR owner), and **E-5** whether a per-tab headroom floor is needed (operator / G-1's Linux pass). **E-7 and E-8 both opened and closed inside this pass, and the sequence is worth recording because it is not a spec changing its mind.** E-7 asked which consumers should see the Darwin reader, given it moves `performance.max_parallel_agents`' macOS default from 2 to 2000. **ADR D1.5c declined one of its two shapes by name** — a browser-only accessor was offered and refused, *"Do not create multiple mechanisms"* — and **D1.5d removed the other shape's subject**, deleting `bytesPerAgent` and the division, so nothing is precomputed and no default moves. E-8 then asked who ratifies a cross-domain change a browser ruling authorised, and **the operator answered it directly: scope sign-off 2026-09-01, ADR commit `ddd9789a4`** — the memory mechanism is **one deliverable serving both consumers**, agent concurrency's tests and documented defaults are **deliverables rather than collateral**, and the "independently landable browser side" hedge this spec briefly carried is withdrawn. **E-8's second half — what an unmeasurable host does to agent admission — is decided in-spec by FR-068a** (hold at the floor, refuse to grow, never refuse to run) rather than escalated, because the answer was already in FR-065's own wording and in holdout 24's reading of it. **Four FRs carry all of it: FR-067, FR-068, FR-068a, FR-069** (§0.6b). **⚠️ The fifth move is ADR D1.9b (2026-09-01), and it both closes and opens.** **E-1 is RULED** — taking the operator's shared tab is **implicit**, an agent acquires it by acting on it, so the seventh tool, the seventh per-agent policy entry and the Tier-3/manifest arithmetic this row had priced are **withdrawn rather than paid**; the mitigation is the pre-existing control lock, and it is required to be asserted in the **blocked** direction (§0.7, FR-070, FR-071, SC-025). **E-4's middle item — per-workspace profile disk — is RULED** by the same ADR: periodic cache trimming (§0.8, FR-072…FR-074), closing §12 A24(b) and §16 MAJ-111 and leaving E-4 with no design question at all — only **three** ADR §6 rows to sweep (capture-session, instances-vs-bytes, and now profile disk). **E-9 is OPENED in the act of closing A24(b)**, and it is deliberately narrower than what it replaces: a workspace under **continuous** drive never becomes trimmable, and both candidate fixes — Chromium's own `--disk-cache-size` (a number nobody here has measured) or a mid-session trim (which closes a browser in use) — are design changes, not tuning. It is **declared** by FR-074 rather than left silent. **⚠️ The sixth move is ADR D1.9c (2026-09-02), and it opens one.** **E-10 is OPENED** in the act of answering D1.9c's residual: this spec's arbiter for two concurrent turns in one session is the write lease (FR-081), while issue **#505** proposes removing the contention at its source. **Nothing here blocks on #505** — FR-081 is correct under both futures — but which shape is intended, and whether FR-081's widened trigger survives #505 landing, is an agent-loop owner's call. **So the live list is now E-3, E-4 (ADR-edit only), E-5, E-9 and E-10.**

**Three ADR edits are outstanding and belong to the ADR's owner** (this spec does not edit the ADR):

1. ~~**D1.6 names the wrong tab count.**~~ **DISCHARGED BY D1.5a — do not re-raise.** It derived R from *"a tab count is enforced (`maxTotalTabs`)"*, which is never seeded (`grep MaxTotalTabs pkg/config/defaults.go` → nothing; `coordinator.go:240-253` logs *"global tab budget: UNLIMITED"* when `<= 0`). The correction was `max_tabs`. **D1.5a then deleted both keys and the flag R configured**, so the ADR edit this row asked for is void: D1.6 is marked withdrawn in the ADR rather than corrected.
2. **ADR §6 should close THREE of its own open rows** (it was two before D1.9b), citing FR-016a (capture session per workspace) and — **corrected by D1.5a** — **D1.5a itself** for the instances-vs-bytes row: the answer is now **bytes and nothing else**, since D1.5 item 2 (instances for a target) is deleted and only item 3's gate remains. The third row — per-workspace profile disk — is **reopened** here and correctly stays open (§12 A24(b), §16 MAJ-111). **⚠️ UPDATED BY D1.9b: the third row is no longer the outstanding one.** This item used to end by noting that the per-workspace profile-disk row stayed open because this spec's closure of it had been withdrawn. **D1.9b ruling 4 closes it** — periodic cache trimming, a hard size cap rejected by name — so ADR §6 should now close **all three** rows: capture-session-per-workspace citing FR-016a, instances-vs-bytes citing D1.5a, and per-workspace profile disk citing **D1.9b ruling 4** and this spec's §0.8 / FR-072…FR-074. **One genuinely new open item replaces it, and it is narrower:** ADR §6 should carry the continuously-driven cache as an open question (§0.5 **E-9**), because the trim bounds every workspace except the one being driven right now.
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
