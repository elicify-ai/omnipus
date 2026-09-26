# The coordination ledger — squads, chief, holds, landing lock

Detail behind SKILL.md §2. Design source: sections 5.4, 5.7, 5.9 and 7.6 of
`docs/internal/design/dev-team-setup-design-2026-09-25.md`.

## Directory layout

`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/` — a sibling of the
checkouts and worktrees, **outside every repo checkout**, so no branch can capture or
conflict on it. The exact path is founder-adjustable; always name it by absolute path.
If the directory is missing, cross-session coordination is broken: report it and ask
the founder before proceeding with cross-session work.

Every line below is a strict, machine-parseable format — the pre-push hook and the
capacity monitor parse them. This table is a summary; **the exact line formats are the
next section, not this one** — write to the ledger from that section, never from memory
of this prose.

| File | Contents (one line per record) | Write rule |
|---|---|---|
| `CHIEF.md` | current chief session · named by · named-at — or `VACANT since <ts>` | Overwritten on handover or when a vacancy is noticed |
| `squads/<squad-id>.md` | owning session (or "in-session") · lead · worktree (absolute path) · branch · claim (trees/files) · status · last-updated (timestamp + who). The row also carries the squad's GOAL line, and its landing announcement when one is pending | The squad's own file; updated on every status change |
| `HOLDS.md` | what (branch or tree) · held by · why · since · released-at | Append on hold; edit the line on release |
| `LANDING-LOCK` | squad · branch · taken-at (absent or empty = free) | Created atomically (one writer wins); released right after the push |
| `LANDING-LOG.md` | squad · branch · commit · checks evidence · founder-yes note · landed-at | Append only — never edited |
| `MESSAGES.md` | Urgent calls only ("hold pushes", "integration red") — the durable record of the message channel | Append only |

Squad status vocabulary (exact): `planned` / `in-flight` / `gated` / `handed-back` /
`waiting-for-founder` / `landed` / `blocked` / `released`.

## Exact line formats (case-sensitive, pipe-delimited `key=value` — parsed literally)

These strings are normative, copied from their own template header comments in
`.claude/templates/coordination/`, and from `scripts/hooks/pre-push-ledger-check`'s own
header comment (the hook's normative-parser copy, kept in sync with these by governance
section 9). A field name, an `=`, a space, or a `|` out of place is not "close enough" —
`scripts/dev-machine-capacity.sh` and the pre-push hook parse these by exact match; a
malformed hold is silently ignored, a malformed lock blocks everything, and a malformed
squad row undercounts the capacity monitor to zero (this broke exactly this way in test
T5 — never write a ledger line as free-form prose with `·` separators; that is this
table above, for humans to read, not what you write to disk).

**Every `<ISO8601>` below is UTC, written only with `date -u +%FT%TZ`** — never local
time with a `Z` suffix (squads have written exactly that, hours off).

- **`squads/<squad-id>.md` row** (template: `squads/SQUAD-ID.md.template`) — the field
  name `status` and its exact value `in-flight` are what the capacity monitor greps for:

  ```
  squad=<squad-id> | session=<session-id-or-in-session> | lead=<role> | worktree=<absolute-path> | branch=<branch-name> | claim=<trees-or-files> | status=<planned|in-flight|gated|handed-back|waiting-for-founder|landed|blocked|released> | last-updated=<ISO8601> by <who>
  ```

  A pending landing announcement is a second line below the row, removed once the
  landing lock is released and the landing is posted to `LANDING-LOG.md`:

  ```
  LANDING-ANNOUNCEMENT squad=<squad-id> branch=<branch-name> announced-at=<ISO8601> by=<who>
  ```

  The squad's GOAL (SKILL.md §2, the goal judge) is its own line below the row — never
  a row field: goal text is free prose that may contain `|`. Written by whoever starts
  the squad (team-lead, for an in-session squad), kept until the squad is `landed` or
  `released`. Neither parser reads it — the capacity monitor matches only lines starting
  `squad=`, and the pre-push hook never reads squad files:

  ```
  GOAL squad=<squad-id> set-at=<ISO8601> by=<who> :: <end state, one line>
  ```

  Before a squad starts a run on a Fly CI machine, it names that machine on its own
  line below the row, and removes the line when the run is over. Two squads have run on
  one machine at once, so check the other squad files for the machine first. After
  stopping or cancelling a run, check the machine's state (`fly machines list -a
  ci-omnipus-1`) and stop it if it is still running — a cancelled run has left one
  running. Neither parser reads this line:

  ```
  CI-MACHINE squad=<squad-id> machine=<fly-machine-id> since=<ISO8601> by=<who>
  ```

- **`HOLDS.md` line** (template: `HOLDS.md.template`) — parsed by the pre-push hook:

  ```
  what=<branch-or-ALL> | held-by=<who> | why=<reason> | since=<ISO8601> | released-at=<empty-or-ISO8601>
  ```

- **`LANDING-LOCK`** (template: `LANDING-LOCK.template`) — the whole file is one line
  when held, absent or empty when free; space-separated, not pipe-separated, and parsed
  by the pre-push hook (branch= and squad= both, since A4 — the squad field proves who
  holds the lock, not only which branch it covers):

  ```
  squad=<squad-id> branch=<branch-name> taken-at=<ISO8601>
  ```

  **Here `branch=` is the branch being pushed TO — the integration branch — never your
  work branch.** The pre-push hook compares it with `OMNIPUS_INTEGRATION_BRANCH`; a lock
  naming the work branch blocks the landing push (it has, twice). The squad row's
  `branch=` is the opposite: your own work branch.

- **`CHIEF.md`** (template: `CHIEF.md.template`) — one content line, overwritten, never
  appended:

  ```
  chief=<session-id> named-by=<founder-or-session> named-at=<ISO8601>
  ```
  or, vacant:
  ```
  VACANT since <ISO8601>
  ```

- **`LANDING-LOG.md` line** (template: `LANDING-LOG.md.template`) — append-only:

  ```
  squad=<squad-id> | branch=<branch-name> | commit=<sha> | checks=<evidence-summary> | founder-yes=<note> | landed-at=<ISO8601>
  ```

- **`MESSAGES.md` line** (template: `MESSAGES.md.template`) — append-only:

  ```
  <ISO8601> from=<session-id> to=<session-id-or-ALL> :: <message text>
  ```

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
  branch or tree must not be touched by others — for example, the integration branch is
  red and nobody may land onto it, or the founder requested a freeze.
- **The landing lock, not a hold, covers your own landing** — never hold your own branch
  to protect your own push; take the landing lock for that (below). A hold is for
  blocking *other* sessions from a branch or tree you do not currently hold the landing
  lock for.
- Release by editing the line to fill `released-at`.
- The pre-push hook (`scripts/hooks/pre-push-ledger-check`) mechanically fails a push to
  the integration branch that an active hold covers. The hook is a backstop, not the
  rule: bypassing it is a founder-only act, and a hook block is always visible, never
  silent.

## The landing sequence — every landing, same order (Round 18)

**Ask before you lock.** The lock is taken at landing time, after the founder's yes —
not held across CI or across the wait for the founder's reply. Finish your checks first.

1. **Ask.** Once your branch is fully gated (spec through its size gate, CI green),
   send the landing ask — batched into one event message with any other pending landing
   asks (SKILL.md §5) — or immediately for urgent work. State the branch, the gate
   evidence, and the integration branch you confirmed at engagement start.
2. **On a yes: take the lock.** Post the landing announcement in your squad file and
   message the other sessions; create `LANDING-LOCK` **atomically**, so the first
   announcer wins even under a race:
   ```sh
   ( set -o noclobber; printf 'squad=%s branch=%s taken-at=%s\n' \
       "$OMNIPUS_SQUAD_ID" "$OMNIPUS_INTEGRATION_BRANCH" "$(date -u +%FT%TZ)" > "$COORD_DIR/LANDING-LOCK" )
   ```
   A non-zero exit means the lock is already taken — someone else is landing; wait and
   retry, do not overwrite. The lock is held for minutes, not for the founder's reply
   time.
3. **Merge the latest integration branch into your work branch**, then re-check **on
   that result**: CI for the affected areas (the CI tiers covering the trees the merge
   touched) plus a review of the conflict resolution (the merge's conflict hunks,
   escalating to architect only where a resolution changes a design decision). The full
   size gate is NOT re-run. This is where a parallel-merge-later overlap's conflict is
   resolved and re-checked. **Every landing runs the whole test set of each affected
   area** (locally is fine, one process at a time) — never a hand-picked subset;
   skipping Fly CI never means skipping tests (a squad that ran 12 hand-picked files
   landed five red checks).
4. **Push** the merge to the integration branch, setting both required variables in the
   same command so neither is ever forgotten in an unrelated shell:
   ```sh
   OMNIPUS_INTEGRATION_BRANCH=<integration-branch> OMNIPUS_SQUAD_ID=<your-squad-id> \
     git push origin <integration-branch>
   ```
   The pre-push hook enforces the ledger's holds and locks mechanically, but it only
   enforces when both variables are set (A2) — **a WARNING about an unset integration
   branch means the hook checked nothing at all: that push is not a landing, stop and
   fix the command before retrying**, never treat the warning as a pass.
   Where the integration branch is PR-protected, the landing act is instead the merge
   of the work branch's PR through that protection (`gh pr merge <pr> --merge`) —
   never an admin or auto merge. The pre-push hook does not see a PR merge, so the lock
   is then the only guard.
5. **Log only after the landing succeeded.** A landing script that logged "landed" and
   released the lock after a failed push has happened; wrap both in the success branch:
   ```sh
   if OMNIPUS_INTEGRATION_BRANCH=<integration-branch> OMNIPUS_SQUAD_ID=<your-squad-id> \
        git push origin <integration-branch>; then   # or: if gh pr merge <pr> --merge; then
     printf 'squad=%s | branch=%s | commit=%s | checks=%s | founder-yes=%s | landed-at=%s\n' \
       "$OMNIPUS_SQUAD_ID" "<work-branch>" "$(git rev-parse --short HEAD)" "<checks>" \
       "<founder-yes-note>" "$(date -u +%FT%TZ)" >> "$COORD_DIR/LANDING-LOG.md"
     rm -f "$COORD_DIR/LANDING-LOCK"
   else
     echo "LANDING FAILED - lock kept, nothing logged; fix and retry, or release the lock deliberately"
   fi
   ```
   For a PR merge, record the merge commit (`gh pr view <pr> --json mergeCommit`) rather
   than the local `HEAD`. Then post the landed commit to the other sessions and close
   every resolved issue with a comment citing the commit.
6. **Own the landing's CI.** The lander watches the GitHub run that the landing starts
   on the integration branch until it finishes green (`gh run list --branch
   <integration-branch>` for the run id, then `gh run view <run-id> --json
   status,conclusion` in bounded waits — `omnipus-shared-rules`, "Headless dispatches
   and long gates") — an unwatched post-merge run is
   how reds went unseen for 12 attempts. **Space landings:** the integration branch's CI
   cancels an in-progress run when the next push arrives (`cancel-in-progress` in
   `.github/workflows/pr.yml`), and branch protection does not prevent that — let the
   previous landing's run start and finish, or batch the fixes into one PR, rather than
   stacking merges minutes apart. A landing is not done at "pushed": the green run, the
   issue comments, the landing-log entry and the two-line report ("code correct and
   tested" / "reachable by a user or agent") close it.

**Red integration branch:** nobody lands until it is green again — landing onto red
hides the culprit. The single exception is the **hotfix lane**
(`knowledge/parallel-planning.md` §4a): a fix-only landing takes the lock like any
landing and says so in its announcement; the fix is dispatched immediately, one
dispatch per red check.

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
`SendMessage` tool, addressed to the peer session named in its squad row's `session=`
field (or, for the chief, the session named in `CHIEF.md`'s `chief=` field). **Until a
dry run has actually proven `SendMessage` delivery between two live sessions in this
setup, treat cross-session messaging as unproven and default to `MESSAGES.md`**: append
the call there instead, and rely on the next ledger read (session start, before every
landing, or after a compaction/resume) to surface it to the other sessions.
`MESSAGES.md` is the durable record either way, and the fallback whenever a session
cannot be reached directly. Everything else is ledger — messages are for urgent calls
only.
