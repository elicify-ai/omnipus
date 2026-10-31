---
name: omnipus-planning-orchestration
description: Planning and orchestration procedure for team-lead — the dependency-graph planning loop with safely-parallel waves and the three change sizes, the cross-session coordination ledger (claims, holds, landing lock, landing announcements, chief naming and vacancy), the machine-capacity check before every dispatch wave, the idle-time playbook, and event-driven status reporting with batched landing asks. MANDATORY at session start and re-opened before creating every new plan.
---

# Omnipus planning and orchestration

**Audience:** team-lead coordinating workers directly — including a separate, directly
addressable team-lead session running an independent stream. There is no separate
squad-lead role (founder, 2026-10-27).
Topology authority: root `CLAUDE.md` ("Subagent Workflow"). Loading is mandatory: preloaded at session start via the
`skills:` frontmatter field, re-opened before creating every new plan. A plan that does
not cite this skill is a review finding; the skills acknowledgement line (shared-skill
rule 13) proves the load.

Last reviewed: 2026-10-29 — trunk-based landing branches, chief meetings
Process version: 2026-10-29.1
Design source: `docs/internal/design/dev-team-setup-design-2026-09-25.md` (sections 5.6–5.9, 7.6, 7.7).
Read that design with its 2026-10-23 amendment; current rules are in root `CLAUDE.md`
("Subagent Workflow", "Build, test, and quality gates", "Running CLI agents").

## Operating stance

Impatient with idle time, patient with quality: hate waiting and serial work; always ask
"what else can run now?"; never trade a quality gate for speed; stay calm and factual in
reports; own problems whatever their origin. Three principles, binding on team-lead and
every team-lead session:

1. **Maximise safe parallelism** — the dependency graph and safe-parallel detection
   below decide what runs together; nothing is serialised "to be safe".
2. **Never wait idle** — waiting time is worked (the idle-time playbook, §4).
3. **Speed never overrides a quality gate** — a blocking gate is escalated and worked
   (findings fixed, or explicitly deferred with a tracked issue and founder approval),
   never skipped, narrowed or silently deferred.

## 1. Planning — run before every new plan

Full procedure: `knowledge/parallel-planning.md`. The loop:

1. **Dependency graph** — list every unit of work and what depends on what.
2. **Safe-parallel detection** — assign one owner per feature/file area and one test
   author per feature before cutting waves; separate CHECK and UAT validation stay
   independent. Keep related scope together; do not decompose by finding. Resolve
   overlapping ownership with the chief before dispatch (`knowledge/parallel-planning.md`).
3. **Waves** — dispatch independent features together, in one message. Each writer has
   its own worktree. Backend/frontend implementation may overlap after the contract
   when separate areas have their named owners.
4. **Size and prepare** — use root `CLAUDE.md` ("Change sizes and the review gate") for
   the reviewer set and combined scope. Record each UI/behaviour documentation TODO,
   its `docs-verifier` audit and the spec-time UAT rows.
5. **Capacity check** — run the monitor (§3) before dispatching the wave, and again
   before widening an existing wave.
6. **Dispatch and watch** — every worker starts with streamed output and a control
   channel; arm a live watcher at dispatch for result, error or no output for 15 minutes.
   Give no deadline or cap in the brief: workers run until done; the 15-minute signal
   prompts a check, never kills a worker. Steer through the control channel on drift,
   hangs or overreach; resume a stopped or disconnected worker in its own session with
   the same context, rather than replacing it. Never fire-and-forget. Read
   `knowledge/dispatch-and-watch.md` before launching or resuming.
7. **Name the landing branch** — the chief names `landing/<train>` at planning (a
   standalone feature gets its own, for example `landing/effort`) and repeats it in every
   dispatch brief; the release branch is the integration target the lock covers. Neither
   is hard-coded in a repo asset except as an example. Re-confirm the release branch
   with the founder at each landing.
8. **Riders and conflicts** — add the open hardening issues that touch the package's
   files (riders) to its scope, confirmed with the architect before the design is final,
   and check the coordination repo's file-conflict table (`LANDING-PLAN.md`) before
   cutting waves (`knowledge/parallel-planning.md`).

## 2. Coordination — streams, chief, ledger

Formats and procedures: `knowledge/coordination-ledger.md`. The core rules:

- The **coordination ledger** — a directory outside every checkout and worktree
  (`$OMNIPUS_COORDINATION_DIR`; terms and per-machine paths in
  `knowledge/coordination-ledger.md`) — is the single coordination state. Read `CHIEF.md`, `HOLDS.md` and the stream files at session start
  and before every landing; update your own stream row on every status change.
- **Claims record the plan** — check them before assigning an owner; they are not a
  machine lock. Resolve an overlapping area with the chief, not another same-area lane.
- **Trains and standalone landing branches** — read `knowledge/landing-trains.md` for
  landing branches, direct pushes, the cut, combined reviews, cut CI, paired hands-on
  testing and chief-owned merges. Use `knowledge/coordination-ledger.md` for the
  approval/lock/push sequence and exact ledger formats. No PR and no full CI before the
  cut, and no per-author gates.
- **Direct coordination** — team-lead dispatches specialists itself for new work. A
  separate team-lead session manages only its commissioned independent stream (pushing
  to its train's or standalone feature's landing branch) and hands the cut evidence to
  the chief for landing; on a stop, compare its reply with its
  recorded GOAL, resume unfinished work in the same session, and route founder
  questions unchanged. There is no separate squad-lead role (founder, 2026-10-27).
- **A red release branch stops all landings; a red landing branch stops its own cut
  until fixed** — with one exception: a landing that only fixes the red release branch
  may land (it takes the lock like any landing and
  says so in its announcement).
- **Stale rows**: a stream row is stale after 2 hours with no update and no live session
  behind it. A live session refreshes its row when it passes the 2-hour mark. Releasing
  or re-assigning a stale claim needs the founder's agreement, asked by the chief.
- **Chief**: named by the founder ("you are chief"); record it in `CHIEF.md`, refresh
  on handover/restart, schedule each cut when the team leads say the train is ready, and
  perform mechanical integration directly.
  Mark a vacancy and ask the founder for a successor; no session appoints itself.
- After any context compaction or resume: **re-read the ledger directory before the next
  write** — the ledger, not any session's memory, is the coordination state.

## 3. Capacity check

Before every new dispatch wave, before widening an existing one, and as the first step
of idle-playbook lane 3, run:

```bash
scripts/dev-machine-capacity.sh
```

It prints its measurements and one verdict line: `CAPACITY: OK` or
`CAPACITY: HOLD <reason>`. **HOLD queues the dispatch — it never cancels work**; retry
after the next check passes or a running lane finishes. A HOLD longer than 20 minutes
fires an event update to the founder, who can override. Signals, thresholds and
semantics: `knowledge/capacity-check.md` (the script's verdict is Mac-only; on a pod the
capacity signal is the `heavy` slot state and `drain status`). An OK is only the monitor
result; also apply the controller/load requirements in root `CLAUDE.md` ("Running CLI
agents") before launch or resume.

## 4. Idle-time playbook

While waiting — CI running, a review in flight, another session, the founder's decision —
ask: what else can run NOW? Allowed without asking, in this order; anything outside
these classes asks first:

1. Docs and cleanup work.
2. Preparing the next work: plan, RED tests, specs, impact analysis.
3. Dispatching workers on a different feature with no dependency on
   in-flight work — **capacity check first** (§3).
4. Reviews and docs checks on ready branches — and security scans on ready branches once
   the rollout test proves an agent can start them. **Not proven until this line says
   otherwise: as written, that test has not passed, so security-lead recommends the scan
   and the founder starts it via `/claude-security` — do not assume the agent-started
   path is live without checking this line first.**

Never allowed, idle time or not: landing anything without its full gate plus the
founder's yes — the landing rule has no idle-time shortcut; skipping, narrowing or
silently deferring a gate (principle 3).

## 5. Status reporting

- **Events only**: an update fires when something lands, fails, or needs the founder —
  never on a timer, no timed status tables. Exception: the chief's standup, planning and
  review meetings (§7) are scheduled by decision (2026-10-29); every other update stays
  event-driven.
- Minor event: one line. Bigger event: a status table (done / in progress / pending /
  blocked) plus one line on what comes next.
- **Batched landing asks**: pending asks go out as one event message listing every
  branch awaiting a yes; the founder answers per branch in one reply. Urgent work may
  ask immediately without waiting for a batch.
- While a failure is open, every status update carries a "who is on it" line.
- Never hand the founder a URL or link that has not been verified reachable **and**
  rendering in a browser at that moment — start the server, load the page, then report
  the link (localhost:6006 was reported as ready before any server was started).
- Reports up the chain: terse and technical, with evidence — files, commands with exit
  codes, certainty labels (shared-skill rule 14). Stream reports cap at ~40 lines plus
  the evidence table.

## 6. Founder-facing demos — handover and dispatch hygiene

- A founder-facing demo or prototype is handed over only after (a) its real-browser
  interaction test has passed and (b) team-lead has re-checked it in a browser — the
  build-side rules (interactive prototype, Playwright interaction test, static-snapshot
  serving) live in `omnipus-frontend-rules` and are not restated here. "Renders without
  errors" and screenshots are not "works".
- Filling the plugin-reviewer dispatch template
  (`.claude/templates/plugin-reviewer-dispatch.md`): fill the placeholders with a tool
  that cannot misparse the text (e.g. Python `str.replace`), never `sed` with a
  delimiter that can occur in the text — a `#` in an issue reference has broken a `sed`
  fill and the reviewer ran without its rules. Verify the filled prompt contains the
  discipline block (the `agent-discipline:shared-traits:start` marker) before
  dispatching.

## 7. Chief meetings and the three objectives (chief only)

The chief runs the daily standup (Mon–Fri 09:33 Jakarta), weekly planning (Mon 10:07)
and weekly review (Fri 16:07) with the three objectives the founder set: unblock
everyone, leave no half-built work behind, accelerate delivery and find free capacity.
Runbook, mention list and the rule against running a meeting twice:
`$OMNIPUS_COORDINATION_DIR/meetings/README.md`. A team lead answers a standup in one
message of four lines (done, doing, blocked or needs the founder, stopped or parked with
its note), and the first word of the answer is `PV <process version>`, read from the
`Process version:` line of root `CLAUDE.md`. A priority change triggers a same-day
parking pass (root `CLAUDE.md`, "Issue & Project Board Conventions").
