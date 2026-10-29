# The coordination ledger — streams, chief, holds, landing lock

Detail behind SKILL.md §2. Design source: sections 5.4, 5.7, 5.9 and 7.6 of
`docs/internal/design/dev-team-setup-design-2026-09-25.md`.

Last reviewed: 2026-10-29 — release branch vs landing branch terms, pod ledger path
Process version: 2026-10-29.1

Process authority: root `CLAUDE.md` ("Subagent Workflow", "Landing trains"). Landing
branch, cut, review, CI and UAT procedure: `knowledge/landing-trains.md`.

**Terms (2026-10-29).** *Release branch* = the integration target the founder confirms
at each landing; the landing lock, holds and the pre-push hook apply to it, and only the
chief pushes to it. *Landing branch* = `landing/<train>` (or a standalone feature's own
landing branch); teams push there directly; it has no lock. A hold with
`what=landing/<train>` is informational (the hook matches only the release branch).
"Integration branch" in older text and in the hook's variable name
(`OMNIPUS_INTEGRATION_BRANCH`) means the release branch.

## Directory layout

`$OMNIPUS_COORDINATION_DIR` — on the founder's Mac
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/`, on a pod the
coordination repo checkout (`/workspace/omnipus-coordination`). It sits **outside every
repo checkout**, so no branch can capture or conflict on it. The exact path is
founder-adjustable; always name it by absolute path. The pre-push hook defaults to the
Mac path and honours `OMNIPUS_COORDINATION_DIR`, so a pod sets that variable.
If the directory is missing, cross-session coordination is broken: report it and ask
the founder before proceeding with cross-session work.

Every line below is a strict, machine-parseable format — the pre-push hook and the
capacity monitor parse them. This table is a summary; **the exact line formats are the
next section, not this one** — write to the ledger from that section, never from memory
of this prose.

| File | Contents (one line per record) | Write rule |
|---|---|---|
| `CHIEF.md` | current chief session · named by · named-at — or `VACANT since <ts>` | Overwritten on handover or when a vacancy is noticed |
| `squads/<squad-id>.md` | owning session (or "in-session") · lead · worktree (absolute path) · branch · claim (trees/files) · status · last-updated (timestamp + who). The row also carries the stream's GOAL line, and its landing announcement when one is pending | The stream's own file; updated on every status change |
| `HOLDS.md` | what (branch or tree) · held by · why · since · released-at | Append on hold; edit the line on release |
| `LANDING-LOCK` | `squad=` · branch · taken-at (absent or empty = free) | Created atomically (one writer wins); released right after the push |
| `LANDING-LOG.md` | `squad=` · branch · commit · checks evidence · founder-yes note · landed-at | Append only — never edited |
| `MESSAGES.md` | Urgent calls only ("hold pushes", "release red") — the durable record of the message channel | Append only |

Stream status vocabulary (exact): `planned` / `in-flight` / `gated` / `handed-back` /
`waiting-for-founder` / `landed` / `blocked` / `released`.

## Exact line formats (case-sensitive, pipe-delimited `key=value` — parsed literally)

These strings are normative, copied from their own template header comments in
`.claude/templates/coordination/`, and from `scripts/hooks/pre-push-ledger-check`'s own
header comment (the hook's normative-parser copy, kept in sync with these by governance
section 9). A field name, an `=`, a space, or a `|` out of place is not "close enough" —
`scripts/dev-machine-capacity.sh` and the pre-push hook parse these by exact match; a
malformed hold is silently ignored, a malformed lock blocks everything, and a malformed
stream row undercounts the capacity monitor to zero (this broke exactly this way in test
T5 — never write a ledger line as free-form prose with `·` separators; that is this
table above, for humans to read, not what you write to disk).

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

  The stream's GOAL (SKILL.md §2, the goal judge) is its own line below the row — never
  a row field: goal text is free prose that may contain `|`. Written by whoever starts
  the stream (team-lead), kept until the stream is `landed` or
  `released`. Neither parser reads it — the capacity monitor matches only lines starting
  `squad=`, and the pre-push hook never reads stream files:

  ```
  GOAL squad=<squad-id> set-at=<ISO8601> by=<who> :: <end state, one line>
  ```

- **`HOLDS.md` line** (template: `HOLDS.md.template`) — parsed by the pre-push hook:

  ```
  what=<branch-or-ALL> | held-by=<who> | why=<reason> | since=<ISO8601> | released-at=<empty-or-ISO8601>
  ```

- **`LANDING-LOCK`** (template: `LANDING-LOCK.template`) — the whole file is one line
  when held, absent or empty when free; space-separated, not pipe-separated, and parsed
  by the pre-push hook (branch= and squad= both, since A4 — the `squad=` field proves who
  holds the lock, not only which branch it covers):

  ```
  squad=<squad-id> branch=<branch-name> taken-at=<ISO8601>
  ```

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

- Read `CHIEF.md`, `HOLDS.md` and the stream files **at session start and before every
  landing**.
- Update your own stream row on every status change — a row not updated is a row nobody
  believes. `last-updated` is the staleness signal: refresh your row when you pass the
  2-hour mark so a live session is never mistaken for dead.
- After any context compaction or resume, re-read the whole ledger directory before
  your next write.

## Features, trains and independent sessions

Retain the existing `squads/` filenames and strict row formats; `squad=` is a fixed
ledger key and file-location convention, not a role reference. For new directly
coordinated features and trains, write `lead=team-lead` and name the responsible
session. A separate independent stream is a separate team-lead session, so it also
writes `lead=team-lead`. Use one canonical ledger file per stream and record its GOAL
and current evidence.

Teams push to the train's landing branch; the chief cuts it when the leads say it is
ready, makes mechanical merges and prepares the cut evidence; workers receive only
conflicts needing a code/design choice. A separate team-lead session pushes to its own
train's or standalone feature's landing branch and hands the cut evidence to the chief
for landing, rather than running another verification cycle per author. A standalone
feature retains its own landing branch and landing; coordinate the second-lander
merge/re-check through the chief.

## Claims — planning information, not locks

Claims show who owns an area in the plan; they are not machine-enforced exclusion.
Resolve duplicate ownership with the chief before dispatch, using the ownership rule
in root `CLAUDE.md`, rather than starting another same-area writer on another branch.
Only **holds** and the **landing lock** are enforced by the repo pre-push hook. Worker
lane/worktree locking is separate controller tooling, not this ledger parser.

## Holds — hold and release

- A hold is the one mechanism that genuinely blocks: append a `HOLDS.md` line when a
  branch or tree must not be touched by others — for example, the release branch is
  red and nobody may land onto it, or the founder requested a freeze.
- **The landing lock, not a hold, covers your own landing** — never hold your own branch
  to protect your own push; take the landing lock for that (below). A hold is for
  blocking *other* sessions from a branch or tree you do not currently hold the landing
  lock for.
- Release by editing the line to fill `released-at`.
- The pre-push hook (`scripts/hooks/pre-push-ledger-check`) mechanically fails a push to
  the release branch (the branch named by `OMNIPUS_INTEGRATION_BRANCH`) that an active
  hold covers. The hook is a backstop, not the
  rule: bypassing it is a founder-only act, and a hook block is always visible, never
  silent.

## The landing sequence — cut evidence first, approval, then lock

1. **Cut the landing branch** with the chief: follow `knowledge/landing-trains.md`.
   Resolve real conflicts before the cut review, CI and hands-on campaign. Record the
   exact cut commit, diff/row scope and receipts in the stream's ledger file. No full CI
   runs before the cut.
2. **Ask.** Present the landing branch, exact-commit gate/CI/UAT evidence and the
   confirmed release branch. Batch pending asks (SKILL.md §5); only an
   explicit reply to that specific ask is landing permission. Do not hold the lock
   across checks or the founder's reply. Founder-question rules are unchanged.
3. **On a yes: take the lock.** Post the landing announcement and message peers; create
   `LANDING-LOCK` atomically, first writer wins:
   ```sh
   ( set -o noclobber; printf 'squad=%s branch=%s taken-at=%s\n' \
       "$OMNIPUS_SQUAD_ID" "$BRANCH" "$(date -u +%FT%TZ)" > "$COORD_DIR/LANDING-LOCK" )
   ```
   On a non-zero exit, wait for its owner; never overwrite a live lock.
4. **Check for target movement.** Fetch and compare with the base in the approved
   cut receipt. If unchanged, push that exact verified commit. If another
   train/standalone landed first, the second lander merges it and re-checks affected
   areas and conflict resolutions; delegate only genuine code/design choices. Release
   the lock while checks run, update the exact-version evidence (including touched-row
   independent UAT for agent-engine changes) and ask on the updated commit before
   reacquiring it. Do not repeat every gate or every successful CI job.
5. **Push the verified cut commit** to the confirmed release branch, with both hook
   variables in the command (BRANCH is the landing branch):
   ```sh
   OMNIPUS_INTEGRATION_BRANCH=<release-branch> OMNIPUS_SQUAD_ID=<train-or-stream-id> \
     git push origin landing/<train>:<release-branch>
   ```
   An unset-variable WARNING means the hook checked nothing: stop and correct the
   command, never call the warning a pass. Humans alone merge to `main`.
6. **Release the lock**, append the landed commit and receipts to `LANDING-LOG.md`,
   notify peers and close resolved issues with commit-citing comments. Finish with
   the two distinct delivery claims and their evidence; a push alone is not done.

**Red release branch:** nobody lands until it is green again — landing onto red
hides the culprit. The single exception: a **fix-only landing** may land, takes the
lock like any landing, and says so in its announcement; the fix itself is dispatched
immediately through the existing area owner, consolidating related red checks. A red
landing branch stops only its own cut until the pusher fixes or reverts the break.

**Stuck lock:** a lock held past the stale window (2 hours) is treated like a stale row
— the chief asks the founder before force-releasing.

## Stale rows — the 2-hour rule

A stream row is stale after **2 hours with no update and no live session** behind it.
The chief asks the founder before releasing or re-assigning a stale claim — releasing
someone else's in-flight work is a founder decision. The 2-hour sweep is what cleans up
after a session that died without updating its row.

## Chief — naming, handover, vacancy

- **Naming:** the founder names one session chief ("you are chief"); the named session
  writes the `CHIEF.md` line with a timestamp. On succession, the outgoing chief writes
  the line when the founder announces the successor.
- **Role:** schedule each cut when the team leads say the train is ready and do
  mechanical merges/integration directly; align standalone landing branches and escalate real design/code conflicts to their owners.
  Escalate unresolved disputes to the founder, rather than inventing a ruling.
- **Vacancy:** if the chief's session ends without handover, whoever notices first
  marks `VACANT since <ts>` in `CHIEF.md` and the founder is asked to name a successor
  — no session appoints itself. Chief-owned train cuts and mechanical integration
  wait for that appointment; retain prepared branch evidence rather than silently
  replacing the chief. Independent standalone work retains its own landing, subject
  to the same approval, lock and second-lander re-check.

## When a session ends

Before ending, a session updates its stream file: work released, handed to a named
successor session, or marked with its exact state for re-assignment.

## Messages — urgent calls only

Urgent calls ("hold pushes", "release red") travel session-to-session over the
`SendMessage` tool, addressed to the peer session named in its stream row's `session=`
field (or, for the chief, the session named in `CHIEF.md`'s `chief=` field). **Until a
dry run has actually proven `SendMessage` delivery between two live sessions in this
setup, treat cross-session messaging as unproven and default to `MESSAGES.md`**: append
the call there instead, and rely on the next ledger read (session start, before every
landing, or after a compaction/resume) to surface it to the other sessions.
`MESSAGES.md` is the durable record either way, and the fallback whenever a session
cannot be reached directly. Everything else is ledger — messages are for urgent calls
only.
