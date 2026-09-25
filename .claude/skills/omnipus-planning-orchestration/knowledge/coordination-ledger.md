# The coordination ledger — squads, chief, holds, landing lock

Detail behind SKILL.md §2. Design source: sections 5.4, 5.7, 5.9 and 7.6 of
`docs/internal/design/dev-team-setup-design-2026-09-25.md`.

## Directory layout

`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/` — a sibling of the
checkouts and worktrees, **outside every repo checkout**, so no branch can capture or
conflict on it. The exact path is founder-adjustable; always name it by absolute path.
If the directory is missing, cross-session coordination is broken: report it and ask
the founder before proceeding with cross-session work.

Every line below is a strict, machine-parseable format — the pre-push hook parses them.

| File | Contents (one line per record) | Write rule |
|---|---|---|
| `CHIEF.md` | current chief session · named by · named-at — or `VACANT since <ts>` | Overwritten on handover or when a vacancy is noticed |
| `squads/<squad-id>.md` | owning session (or "in-session") · lead · worktree (absolute path) · branch · claim (trees/files) · status · last-updated (timestamp + who). The row also carries the squad's landing announcement when one is pending | The squad's own file; updated on every status change |
| `HOLDS.md` | what (branch or tree) · held by · why · since · released-at | Append on hold; edit the line on release |
| `LANDING-LOCK` | squad · branch · taken-at (absent or empty = free) | Created atomically (one writer wins); released right after the push |
| `LANDING-LOG.md` | squad · branch · commit · checks evidence · founder-yes note · landed-at | Append only — never edited |
| `MESSAGES.md` | Urgent calls only ("hold pushes", "integration red") — the durable record of the message channel | Append only |

Squad status vocabulary (exact): `planned` / `in-flight` / `gated` / `handed-back` /
`waiting-for-founder` / `landed` / `blocked` / `released`.

## Read before acting

- Read `CHIEF.md`, `HOLDS.md` and the squad files **at session start and before every
  landing**.
- Update your own squad row on every status change — a row not updated is a row nobody
  believes. `last-updated` is the staleness signal: refresh your row when you pass the
  2-hour mark so a live session is never mistaken for dead.
- After any context compaction or resume, re-read the whole ledger directory before
  your next write.

## Squads — the two levels

The unit of parallel feature-size work: one squad lead, its own worktree(s), its own
feature branch, and the specialists it draws from the eleven roles.

- **Level 1 — in-session:** team-lead dispatches squad-lead subagents (Agent tool); each
  orchestrates its own specialists. An in-session squad **finishes by handing a fully
  gated branch back to team-lead**, which asks the founder and lands. In the founder's
  personal layer, where workers are started outside the Agent tool, the squad
  orchestrator must itself be a subagent — it must be able to dispatch.
- **Level 2 — across sessions:** the founder runs several sessions; each acts as a squad
  lead for its squad, aligned by the chief through the ledger plus urgent messages.
  A separate-session squad **lands itself**, under the landing lock.

Landing actors, fixed: team-lead lands its own direct work and in-session squads'
handed-back branches; a separate-session squad lands its own work; nobody else ever
lands on the integration branch.

## Claims — information, not ownership

A claim tells the other sessions what is in flight; it never blocks anyone. Two
sessions wanting the same work: the later claimant takes its own worktree and branch
(merge-later), or asks the chief to re-assign. Disputes go to the chief, then the
founder. The enforced state is exactly two things — **holds** and the **landing lock**.

## Holds — hold and release

- A hold is the one mechanism that genuinely blocks: append a `HOLDS.md` line when a
  branch or tree must not be touched by others (for example mid-landing).
- Release by editing the line to fill `released-at`.
- The pre-push hook (`scripts/hooks/pre-push-ledger-check`) mechanically fails a push to
  the integration branch that an active hold covers. The hook is a backstop, not the
  rule: bypassing it is a founder-only act, and a hook block is always visible, never
  silent.

## The landing sequence — every landing, same order

1. **Announce and take the lock.** Post the landing announcement in your squad file and
   message the other sessions; create `LANDING-LOCK` atomically — first come, first
   served; exactly one writer wins. The push happens under the lock; the lock is
   released immediately after.
2. **Merge the latest integration branch into the work branch**, then re-check **on
   that result**: CI for the affected areas (the CI tiers covering the trees the merge
   touched) plus a review of the conflict resolution (the merge's conflict hunks,
   escalating to architect only where a resolution changes a design decision). The full
   size gate is NOT re-run. This is where a parallel-merge-later overlap's conflict is
   resolved and re-checked.
3. **Founder yes in chat** — in the landing session's own chat (separate-session
   squad), or team-lead asks in the main session's chat for its direct work and
   handed-back branches (pending asks batched — SKILL.md §5).
4. **Push** the merge to the integration branch — the pre-push hook enforces the
   ledger's holds and locks.
5. **Release the lock**, post the landed commit to `LANDING-LOG.md` and to the other
   sessions, and close every resolved issue with a comment citing the commit. A landing
   is not done at "pushed" — the issue comments, the landing-log entry and the two-line
   report ("code correct and tested" / "reachable by a user or agent") close it.

**Red integration branch:** nobody lands until it is green again — landing onto red
hides the culprit. The single exception: a **fix-only landing** may land, takes the
lock like any landing, and says so in its announcement; the fix itself is dispatched
immediately, one dispatch per red check.

**Stuck lock:** a lock held past the stale window (2 hours) is treated like a stale row
— the chief asks the founder before force-releasing.

## Stale rows — the 2-hour rule

A squad row is stale after **2 hours with no update and no live session** behind it.
The chief asks the founder before releasing or re-assigning a stale claim — releasing
someone else's in-flight work is a founder decision. The 2-hour sweep is what cleans up
after a session that died without updating its row.

## Chief — naming, handover, vacancy

- **Naming:** the founder names one session chief ("you are chief"); the named session
  writes the `CHIEF.md` line with a timestamp. On succession, the outgoing chief writes
  the line when the founder announces the successor.
- **Role:** the chief is an **aligner, not a queue** — plans, holds and landing
  announcements flow through the ledger; it does not collect, sequence or perform other
  sessions' landings, and it escalates disputes it cannot settle to the founder.
- **Vacancy:** if the chief's session ends without handover, whoever notices first
  marks `VACANT since <ts>` in `CHIEF.md` and the founder is asked to name a successor
  — no squad lead appoints itself. Cross-session alignment pauses during a vacancy;
  landing does not (separate-session squads land themselves under the lock, and
  in-session landings never needed the chief).

## When a session ends

Before ending, a session updates its squad file: work released, handed to a named
successor session, or marked with its exact state for re-assignment.

## Messages — urgent calls only

Urgent calls ("hold pushes", "integration red") travel session-to-session over the
harness's session-messaging facility; `MESSAGES.md` is the durable record and the
fallback where a session cannot be messaged. Everything else is ledger — messages are
for urgent calls only.
