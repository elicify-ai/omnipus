---
name: omnipus-planning-orchestration
description: Planning and orchestration procedure for team-lead and squad-lead — the dependency-graph planning loop with safely-parallel waves and the three change sizes, the cross-session coordination ledger (claims, holds, landing lock, landing announcements, chief naming and vacancy), the machine-capacity check before every dispatch wave, the idle-time playbook, and event-driven status reporting with batched landing asks. MANDATORY at session start and re-opened before creating every new plan.
---

# Omnipus planning and orchestration

**Audience:** team-lead and squad-lead — both shapes: in-session squad-lead subagents and
separate-session squad leads. Loading is mandatory: preloaded at session start via the
`skills:` frontmatter field, re-opened before creating every new plan. A plan that does
not cite this skill is a review finding; the skills acknowledgement line (shared-skill
rule 13) proves the load.

Last reviewed: 2026-09-26
Design source: `docs/internal/design/dev-team-setup-design-2026-09-25.md` (sections 5.6–5.9, 7.6, 7.7).

## Operating stance

Impatient with idle time, patient with quality: hate waiting and serial work; always ask
"what else can run now?"; never trade a quality gate for speed; stay calm and factual in
reports; own problems whatever their origin. Three principles, binding on team-lead and
every squad lead:

1. **Maximise safe parallelism** — the dependency graph and safe-parallel detection
   below decide what runs together; nothing is serialised "to be safe".
2. **Never wait idle** — waiting time is worked (the idle-time playbook, §4).
3. **Speed never overrides a quality gate** — a blocking gate is escalated and worked
   (findings fixed, or explicitly deferred with a tracked issue and founder approval),
   never skipped, narrowed or silently deferred.

## 1. Planning — run before every new plan

Full procedure: `knowledge/parallel-planning.md`. The loop:

1. **Dependency graph** — list every unit of work and what depends on what.
2. **Safe-parallel detection** — per pair of units, two checks: (a) *dependencies* — a
   unit runs only after its dependencies are done; (b) *files* — disjoint file trees run
   together; a same-file overlap is allowed and marked **parallel-merge-later** (each
   stream in its own worktree and branch; the conflict is resolved at landing step 2).
   Never two writers in one working copy.
3. **Waves** — cut the graph into waves of safely-parallel units; dispatch each wave's
   independent units together, in one message, so they run concurrently.
4. **Size every unit** — small / standard / feature (table in the knowledge file).
   Urgent is a queue priority (front of the queue), never a fourth size. Feature-size
   units run as squads — own feature branch, worktree(s), **their own squad-lead from
   the first dispatch** (the full rule: §2); small and standard run as direct dispatches
   on a short-lived work branch cut from the integration branch.
5. **Capacity check** — run the monitor (§3) before dispatching the wave, and again
   before widening an existing wave.
6. **Name the integration branch** — the founder names it when commissioning; confirm at
   engagement start, repeat it in every dispatch brief that needs it, re-confirm with the
   founder at each landing. It is never hard-coded in any repo asset.

## 2. Coordination — squads, chief, ledger

Formats and procedures: `knowledge/coordination-ledger.md`. The core rules:

- The **coordination ledger** — a directory outside every checkout and worktree
  (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/`) — is the single
  coordination state. Read `CHIEF.md`, `HOLDS.md` and the squad files at session start
  and before every landing; update your own squad row on every status change.
- **Claims are information, not ownership** — a claim never blocks anyone; work proceeds
  merge-later. The only enforced state is **holds** and the **landing lock**, both
  enforced mechanically at push time by the pre-push hook.
- **Landing** (any actor, Round 18): ask the founder first (batched, §5) → on a yes,
  take the lock → merge the latest integration branch into the work branch → re-check on
  that result (affected-area CI plus a conflict-resolution review — never the full size
  gate again) → push (with both `OMNIPUS_INTEGRATION_BRANCH` and `OMNIPUS_SQUAD_ID` set
  in the push command itself) → release the lock, post the landed commit, close the
  resolved issues citing the commit. The lock is taken at landing time, after the yes —
  never held across CI or across the wait for the founder's reply. Exact sequence and
  line formats: `knowledge/coordination-ledger.md`.
- **Every feature-size squad runs under its own squad-lead from the first dispatch** —
  team-lead never leads a squad itself; small and standard work stays a direct dispatch
  with team-lead, and a squad row's `lead=<role>` is the squad's own `squad-lead`, never
  `team-lead`. If starting a squad lead needs a model choice or any other trade-off,
  team-lead asks the founder at squad start instead of silently leading the squad — the
  silent absorption is the failure this rule exists to prevent.
- **team-lead's own job** is coordination (the ledger, chief alignment, cross-squad
  overlaps), founder interviews and decisions, landing, and the final founder-facing
  browser check — never per-specialist dispatch inside a squad; when more than one or
  two squads' worth of specialist results sit unprocessed across squads, that is the
  trigger to delegate, not to work faster.
- **In-session squads never land themselves** — a squad-lead subagent finishes with a
  fully gated branch and hands it back to team-lead, which asks the founder (asks
  batched, §5) and lands. Separate-session squads land themselves under the lock.
- **A red integration branch stops all landings** — with one exception: a landing that
  only fixes the red integration branch may land (it takes the lock like any landing and
  says so in its announcement).
- **Stale rows**: a squad row is stale after 2 hours with no update and no live session
  behind it. A live session refreshes its row when it passes the 2-hour mark. Releasing
  or re-assigning a stale claim needs the founder's agreement, asked by the chief.
- **Chief**: named by the founder ("you are chief"); the named session records the
  appointment in `CHIEF.md`. The chief is an aligner, not a queue — it never collects,
  sequences or performs another session's landing. A vacancy is marked in `CHIEF.md` and
  the founder asked to name a successor; no squad lead appoints itself.
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
semantics: `knowledge/capacity-check.md`.

## 4. Idle-time playbook

While waiting — CI running, a review in flight, another squad, the founder's decision —
ask: what else can run NOW? Allowed without asking, in this order; anything outside
these classes asks first:

1. Docs and cleanup work.
2. Preparing the next work: plan, RED tests, specs, impact analysis.
3. Dispatching an independent team or squad on backlog work with no dependency on
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
  never on a timer, no timed status tables.
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
  codes, certainty labels (shared-skill rule 14). Squad reports cap at ~40 lines plus
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
