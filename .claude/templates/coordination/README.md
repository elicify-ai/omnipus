# Coordination ledger — templates

Templates for the live coordination ledger (design
`docs/internal/design/dev-team-setup-design-2026-09-25.md` section 5.9,
"The coordination ledger (G2)"). **These templates are authoring source
only — nothing loads them at runtime**, the same non-skill-asset pattern
the design uses for `.claude/templates/agent-discipline.md` (4.5).

## Where the live ledger lives (not here)

The live ledger is a directory **outside every repo checkout and
worktree**, so no branch can capture or conflict on it (design 5.9):

```
/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/
```

This is a convention, not a hard-coded requirement — the design calls the
exact path "founder-adjustable" — but it is the default every script in
this repo assumes unless overridden by an environment variable
(`DEV_CAPACITY_LEDGER_DIR` for `scripts/dev-machine-capacity.sh`,
`OMNIPUS_COORDINATION_DIR` for `scripts/hooks/pre-push-ledger-check`).

**This commit does not create that directory.** It is bootstrapped at
rollout (design 10.1 stage 1e: "the coordination directory is bootstrapped
(empty files)"), which is a founder-timed activation step, not something a
single lane creates ahead of time.

## Layout to bootstrap at rollout

```
coordination/
├── CHIEF.md            one line: current chief, or VACANT
├── HOLDS.md             one line per active hold (append, edit-in-place on release)
├── LANDING-LOCK          absent/empty = free; one line while a landing is in flight
├── LANDING-LOG.md        append-only landed-commit record
├── MESSAGES.md           append-only durable record of urgent cross-session calls
└── squads/
    └── <squad-id>.md    one file per squad: its row, plus a pending landing
                          announcement when one is in flight
```

## The templates in this directory

| Template | Becomes (at rollout, in the live ledger) |
|---|---|
| `CHIEF.md.template` | `CHIEF.md` |
| `HOLDS.md.template` | `HOLDS.md` (start empty — no lines — meaning nothing is held) |
| `LANDING-LOCK.template` | `LANDING-LOCK` (start absent — do not create it with content; the file's mere existence-with-content is the lock) |
| `LANDING-LOG.md.template` | `LANDING-LOG.md` (start empty) |
| `MESSAGES.md.template` | `MESSAGES.md` (start empty) |
| `squads/SQUAD-ID.md.template` | `squads/<real-squad-id>.md`, one per squad, created when the squad starts |

Each template's header comment documents its exact line format and cites
the design section that specifies its fields. Two of those formats are also
the **normative parse targets** of code in this repo, so a format change
must land together with a matching code change in the same commit:

- `HOLDS.md` and `LANDING-LOCK` line formats — parsed by
  `scripts/hooks/pre-push-ledger-check`.
- `squads/*.md` `status=in-flight` field — counted by
  `scripts/dev-machine-capacity.sh` (active-dispatch signal, design 5.6).

## Claims are information, not ownership (design 5.9, C2)

Every field in `squads/<squad-id>.md` — including `claim` — is informative:
it tells other sessions what is in flight, it never blocks anyone. The only
two things this repo enforces mechanically are **holds** (`HOLDS.md`) and
the **landing lock** (`LANDING-LOCK`), both via the pre-push hook. Disputes
over a claim go to the chief, then the founder (design 5.9); nothing in
these templates or in the hook resolves a claim dispute automatically.

## Read before acting

Every session reads `CHIEF.md`, `HOLDS.md` and the `squads/` files at start
and before every landing (design 5.9, "Read before acting"). A session that
undergoes context compaction or resumes from a pause re-reads the ledger
directory before its next write (G4) — the ledger, not any session's
memory, is the coordination state.
