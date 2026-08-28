# The trash convention

**Status:** design note for review · **Date:** 2026-08-28 · **Author:** architect (Stage 3, agent 6)
**Authority it assembles:** ADR-068 revision 11 (D8, D15.3, D20, D23.4), spec Draft 9
(FR-038a, FR-048, FR-048a, FR-048b, §4.1.5)
**Deliverable this satisfies:** spec §11 W4 — *"assemble FR-048 / FR-048a / FR-048b / FR-038a /
§4.1.5's refusal rows into one reviewable convention document, and have that document reviewed,
before W5 exposes the operation."*

---

## What this is, in one paragraph

When an agent throws a note away, it must not actually be thrown away. It moves into a dated
folder tucked inside the vault's own bookkeeping directory, keeping the path it came from, so
putting it back is unambiguous. Everything that pointed at it now points at nothing, and the
system says so at the moment of the throw and keeps saying so — honestly, and without pretending
a deliberate act was an accident. The note stops appearing in search immediately, because a
search that returns a note the user just deleted is the same confidently-wrong answer this whole
project exists to remove. Thirty days later it is gone for good, and nothing an agent can call
makes that happen sooner.

**Most of this was already decided.** Six behaviours are normative in the specification today,
and a reviewer should know that going in: the storage location, the link policy, the index
policy, retention, restore semantics, and what a second trash of the same path does. What this
document adds is (a) the assembly, so a person can read it end to end, (b) answers to four
questions nobody had asked yet, and (c) **five findings against the current specification and
code, one of which is a way to permanently delete a note today without going through trash at
all.**

---

## The convention at a glance

| Question | Answer | Status |
|---|---|---|
| Where does it go? | `<vault>/.omnipus-vault/trash/<colon-free timestamp>/<original relative path>` | already normative (FR-048) |
| What happens to inbound links? | Not repaired. Counted and listed at trash time; annotated, not re-classified, in every later health check | half normative (FR-048), **half new here** |
| Does the index forget it? | Yes, immediately, both indexes, driven by the trash operation itself | normative (FR-048); **its mechanism is new here** |
| Does it become an "orphan row"? | No. The derived properties row is deleted with it | already normative (the `index_epoch` ruling) |
| How do I get it back? | `vault_restructure` with `op: restore` and the note's original path | normative (FR-048a); **its addressing rule is new here** |
| Which tool? | `vault_restructure`. Never `vault_edit` | already normative (FR-070d, FR-070e) |
| Can an agent delete permanently? | No. Never. Not by any operation of any tool | **new here** |
| Who deletes permanently? | The retention sweep, after 30 days, and only files it can prove it wrote | **new here** |

---

## 1. Where a trashed note goes

**Decision: it moves to `<vault>/.omnipus-vault/trash/<timestamp>/<original relative path>`, and
the note's own bytes are not touched.**

A note at `Deals/Acme Corp.md`, trashed on 26 August, becomes the file
`.omnipus-vault/trash/20260826T120000Z/Deals/Acme Corp.md`. It is still a plain markdown file.
Open it in any text editor and it reads exactly as it did. Nothing was added to it, nothing was
rewritten, and no marker was stamped into it.

**Why this location and not another — the reason is verified, not assumed.** The vault is walked
by two separate pieces of code, and both of them refuse to descend into a fixed set of directory
names. That set is declared once, in `pkg/knowledge/scan.go::scanSkippedDirNames`, and it holds
`.obsidian`, `.omnipus-vault`, `.git` and `.trash`. The containment walker
(`pkg/knowledge/contain.go::WalkContained`) reads that same map rather than keeping its own copy,
and the comment above it records why in the bluntest possible terms: when the two walkers
disagreed, *"notes in `.trash` were opened and resurfaced as live backlinks on real notes."*

So the invisibility a trashed note needs is not something this convention has to build. It
already exists, it is shared by both walkers, and it was earned by a bug. Putting trash inside
`.omnipus-vault/` means a trashed note is not found by search, is not a link target, is not an
orphan, and is not a phantom attachment — for free, on day one.

**One consequence must be written down as a rule, because it is currently only an accident:**

> **The trash location is fixed and MUST NOT become configurable.**

The collection marker already lets an operator relocate one directory — templates, via
`pkg/knowledge/marker.go::Marker.TemplatesDir`, defaulting to `DefaultTemplatesDirName`. There is
no equivalent for trash, and there must never be one. Everything above depends on the trashed
note sitting under a directory whose *name* is in the skip set. Point trash at `archive/` and
every trashed note instantly becomes a live, searchable, linkable note again — and the failure is
silent, because nothing errors. A configurable trash directory is a configuration option whose
wrong value quietly un-deletes the user's deletions.

### What this does to the three things a vault gets put through

**Opened in Obsidian.** Obsidian's own soft-delete writes to `.trash` at the vault root, which is
the same shape of answer, so this convention is a sibling of the host application's rather than a
stranger to it. Omnipus writes to its own `.omnipus-vault/trash/` rather than into Obsidian's
`.trash`, and that separation is deliberate — see the rejected options below. `[INFERRED]`
Obsidian does not surface dot-directories in its file explorer or its search; I verified the
skip behaviour of *our* walkers by reading them, and I did not verify Obsidian's from this
repository.

**Synced across devices.** A trashed note is an ordinary file move. Any sync tool that moves
files moves it. Two devices that trash the same note at different moments produce two
timestamped copies rather than a conflict, because the timestamp is in the path — the same
property that FR-048a already relies on for a second trash of one path on one machine.

**Put in git.** The move shows up as a rename, which is exactly what it is, and the note's
history follows it. The user can recover it with ordinary git commands, with no knowledge of
Omnipus at all. This is the D8 test — *"would this survive us?"* — answered in the strongest
form: the survival path does not even require our tool.

### Options rejected, and what killed each

**A frontmatter tombstone, note left in place** (`omni_trashed: true` in the note's own
frontmatter). **Killed by D8, directly and unarguably.** D8's rule is that a field meaningless
without Omnipus is our bookkeeping, and the no-lock-in promise is that *"if Omnipus is
uninstalled, the vault is still a working set of notes."* A tombstone inverts that: uninstall
Omnipus and every note you deleted comes back, because nothing else in the world knows that key
means "deleted". Worse, it does not even work *while* Omnipus is installed — Obsidian search,
Dataview, `grep` and the user's own eyes all still see a live note. It fails the promise and
fails the feature.

**Obsidian's own `.trash`.** Tempting, and rejected on three counts. It is another
application's private state, and writing into it makes our correctness depend on their layout.
It has no room for a timestamp segment, so two trashes of one path collide — FR-048a's
second-copy rule becomes unimplementable. And it is user-facing in Obsidian's own settings, where
the user can switch the destination to the system trash or to permanent deletion; our soft-delete
would then silently become a hard delete because of a preference set for a different reason
entirely.

**The OS trash.** Killed by scope and by portability. The OS trash is outside the vault, so it
leaves the vault directory — sync, git, backup and all — and restoring it correctly requires
per-platform trash APIs. Hard Constraint #2 (pure Go, no CGo, no shelling out) makes the macOS
and Windows implementations awkward at best, and there is no OS trash at all on a headless Linux
server or in Termux, so the feature would exist on some installs and not others. Constraint #4's
graceful degradation does not help here: there is no degraded soft-delete, only a hard one.

**A sibling directory outside the vault** (`~/.omnipus/trash/`). Killed by D8 and by the sync
story. A trashed note would leave the user's own folder and enter ours, so uninstalling Omnipus
would strand it somewhere the user does not look, and it would not travel with the vault to
another machine. The vault is the user's directory; things deleted from it should stay in it.

---

## 2. What happens to inbound links

**Decision, in two parts.**

**Part one, already normative and confirmed: the links are not repaired, and the trash response
names the count and lists the notes that now dangle** (FR-048, AC-X1). There is nothing to repair
them to. A rename can rewrite inbound links because there is a new name to write; a trash has no
new name, and inventing one — pointing them at the trash path — would create links that break
again in thirty days.

**Part two, new: a link pointing at a trashed note is NOT a third integrity category. It is one
of the two existing categories, carrying a new, distinct REASON, annotated at the report layer.**

The health sweep in `pkg/knowledge/integrity.go` already separates `broken link` (an ordinary
wikilink in a note's body that resolves to nothing) from `unresolved` (a *typed relation* whose
target resolves to nothing). Those two are declared in `IntegrityCategories` alongside
`wrong type`, `orphan`, `orphan row` and `duplicate id`.

**The categories split on what kind of link it is, not on why it failed.** That is the whole
argument. A link to a trashed note can be either kind — a sentence in someone's meeting note, or
a declared `client:` relation on a record. If "points at trashed" became its own category, a
report would have to drop the wikilink-versus-relation distinction for exactly the links where
the remedy differs most: a dangling relation is a typed-data problem the record's owner must
resolve, a dangling body link is prose. FR-033 and FR-042 both depend on that distinction, and a
new category would collapse it.

**"Why it failed" already has its own axis, and the file already argues for using it.**
`pkg/knowledge/links.go` declares `UnresolvedReason` as a closed set — `no_match`,
`empty_target`, `absolute_target`, `outside_collection` — and `integrity.go`'s
`unresolvedReasonText` renders each differently, with this reasoning written into the code: *"the
reasons are not equivalent — 'no note resolves' is a typo the operator can fix, 'the link leaves
the collection' is a different problem entirely — and collapsing them into one message loses the
distinction FR-042 records."* A trashed target is exactly that: same fault, different remedy. A
typo is fixed by editing the link. A trashed target is fixed by restoring the note, or by
accepting the deletion and removing the link. Two different actions, so two different messages.

**Where the annotation lives, and why not in the resolver.** The obvious move is to add a fifth
`UnresolvedReason` and teach the link resolver about trash. I rejected that. `NoteIndex.Resolve`
answers one question — *does this name a note in this collection?* — and a trashed note is not in
the collection, by construction. Teaching it otherwise changes the meaning of `graph.Unresolved()`
for every caller in order to serve one, and it makes the link graph depend on trash state.
Instead the annotation is added where the remedy is already composed: `integrity.go` already
appends a `; nearest: <path>` clause to a finding when a case-only match exists, and the reasoning
given there is the same standard this annotation meets — *"a case-only difference is a fact, not a
guess."* So is a trashed note. It is at a known path, on disk, one command from returning.

**A finding therefore reads:**

```
Deals/Q3 review.md -> [[Acme Corp]] — no note resolves (ordinary wikilink, not a relation);
  trashed 20260826T120000Z, restorable
```

**Two properties make this safe against the cry-wolf failure.**

*It never creates a finding.* The annotation only decorates findings that the sweep would have
produced anyway. It cannot inflate a count, so it cannot train a reader to skip the section.

*It cannot let a mass trash starve the report.* The per-category clamp is 500
(`IntegrityFindingsPerCategory`), and one folder-wide trash could plausibly fill `broken link`
with 500 findings of one cause, hiding genuine typos elsewhere. `CategoryResult` already reports
`Total` before the clamp, on the stated principle that *"a clamp that does not say what it hid is
a truncation, not a bound."* The same principle extends one step: **the category must report how
many of its findings point at trashed notes**, so the clamp line reads *"500 of 2,140 shown;
1,890 of them point at notes trashed in the last 30 days"* rather than leaving a reader to
assume the vault has fallen apart.

**Bounding the annotation.** The map from original path to trashed-at timestamp is built from the
trash receipts (§7), not by walking the trash tree, so its cost is one small directory read per
trash entry and it is naturally bounded by the 30-day retention. If the receipt count exceeds the
sweep's own bound, the annotation is **skipped and the report says it was skipped** — never
silently omitted. That is the `NotRun`-versus-zero rule this file already enforces: *"'0 findings'
and 'not checked' are opposite verdicts."*

### Options rejected

**Make "points at trashed" a third category.** Killed above: it collapses the
wikilink-versus-relation distinction, which is the distinction that decides the remedy.

**Suppress these findings entirely for 30 days.** Killed by honesty and by precedent. The link
really is broken; a reader following it gets nothing. Suppression would also make the report's
output depend on a state the reader cannot see — the exact reasoning the specification already
used to reject suppression for the orphan-row case, where it chose to delete the row rather than
hide the finding.

**Repair inbound links by pointing them at the trash path.** Killed by retention. Those links
break again at purge, and in the meantime they point at a path that the walkers deliberately
cannot see, so they would resolve to nothing anyway while *looking* repaired. That is worse than
an honest break.

---

## 3. Does the index forget it

**Decision: yes, immediately, in both indexes, driven by the trash operation itself — and that
last clause is the part that needs building.**

**Confirmed, and it produces no orphan row.** The specification already ruled that the derived
properties row is deleted when a note moves into trash, precisely so that a trashed record does
not generate a permanent integrity finding for thirty days. The row is derived and disposable, a
restore re-indexes and the row returns, and — the valuable half — **an orphan row therefore keeps
its meaning**: it means a note vanished *without* going through trash, which is exactly the
condition the finding exists to report. The alternative, keeping the row and suppressing the
finding, was rejected because a suppression rule is a second thing to get wrong.

**The mechanism is where the current design is thinner than it reads.** A trashed note is no
longer in the scanned set, because the scan skips `.omnipus-vault/`. The existing reconciliation
already handles that case correctly — `pkg/knowledge/index.go::(*Index).SyncWith` removes entries
"that were in the manifest but no longer on disk", calling `Manifest.Remove` and
`batchState.delete`, and counting them in `SyncStats.Removed`.

**But that is drift-driven, and FR-048 says "immediately".** Those are different guarantees. If
trash relies on the next reconciliation pass, there is a window — however short — in which the
user has deleted a note and search still returns it. That is the precise failure question 3 was
asked to prevent, and it would pass every test that runs a scan before asserting.

> **Requirement:** the trash operation MUST itself drive the removal from both indexes and the
> `index_epoch` bump, in the same operation, before it returns. **The acceptance test MUST assert
> that a search performed immediately after trash, with no intervening scan, does not return the
> note.** A test that scans first proves nothing about this requirement.

**Restore is the mirror.** It re-indexes the note and re-derives its properties row in the same
operation, and bumps the epoch for the same reason.

### Options rejected

**Keep the note in the index, flagged as trashed, and filter it out at query time.** Killed by
the filter being opt-in in practice. Every query path would have to remember the filter, and the
one that forgets returns a deleted note to the user with no error anywhere. A record that is not
in the index cannot be returned by a path that forgot.

**Leave removal to the next scan.** Killed above. It is a real window in which the product lies
to the user, and it is invisible to any test that scans first.

---

## 4. Restore

**The one sentence, which is the test this section had to pass:**

> **`vault_restructure` with `op: restore` and the note's original path puts the most recently
> trashed copy of that note back where it came from.**

That is the whole model an agent needs. `restore` with `path: "Deals/Acme Corp.md"` restores the
note. Everything below is what happens when reality is less tidy.

**Addressing is by ORIGINAL path, newest copy by default, with an optional timestamp.** The
agent names the note it wants back, using the path it remembers — the same string it would have
passed to `trash`. When a path has been trashed more than once, `restore` takes the most recent
copy, and the response says which one it took and lists the older timestamps. An optional
`trashed_at` parameter selects an older copy explicitly.

**This resolves a contradiction in the current specification rather than adding to it** — see
finding F3. FR-048b says restore *"takes a trashed path"*; §4.1.5's refusal row says
`no trashed note at Deals/Acme.md`, which is an original path. They cannot both be the interface.
Original path wins, for three reasons: it is the only address the agent can produce without first
listing the trash, it is the address the refusal message already assumes, and a
trash-path interface would let the agent name a location inside `.omnipus-vault/` — which is
precisely what §6 forbids.

**Four refusals, all already ruled, restated so they sit in one place:**

- **Nothing there.** `no trashed note at Deals/Acme.md; vault_describe reports the trash contents`
  (§4.1.5).
- **The identifier is taken.** If a *live* record already holds the trashed record's identifier,
  restore is refused naming both paths (FR-038a). This can only happen if a note was created at
  that path in the interval. The counter is never lowered in either case.
- **A restored identifier below the counter is NOT a duplicate** (FR-038a). The identifier was
  never reissued, so this is the same record returning, not a second one wearing its name.
- **The destination must be contained.** The reconstructed path is resolved through
  `pkg/knowledge/contain.go::Root.ResolveContained` and refused on escape, on `..` after
  normalisation, on an out-of-root symlink, or on a collection not currently in scope — and the
  refusal is audited (FR-048b, FR-077).

**Why FR-048b exists is worth restating for the reviewer, because it is the thing that makes the
receipt in §7 worth building.** The trash directory is ordinary files in the user's own vault,
editable by anything. A hand-edited trash path — or a note trashed from a collection since
unmounted — would otherwise restore wherever the path says. Containment is one defence. The
receipt is a cheaper second one: the receipt records the destination, so a hand-edited path
disagrees with its own receipt and is refused before containment is even consulted.

**If restore were hard, the trash design would be wrong.** It is one operation, one required
argument, one default, and four refusals that each name what to do next. That is the test passed.

---

## 5. What an agent may do unsupervised

**Decision: `trash` and `restore` are both operations of `vault_restructure`. Neither ever
appears in `vault_edit`. This is confirmation of an existing ruling, not a new one, and the
existing reasoning is sound.**

The tool split turns on whether an operation touches only the file the agent named or reaches
files it never named. Trash reaches: one call can break the relations of twenty notes that the
agent never mentioned and will never write to. The specification's own analysis is precise about
this — trash writes bytes into no file the caller did not name, yet *"breaks N existing notes'
relations without writing them"* — and it notes the uncomfortable consequence that rename, which
at least *repairs* inbound links, is the gentler of the two. **Trash is the worse cascade, and it
is the one with nothing to repair to.**

The mechanical rule that settles it: `vault_configure` is the control plane and writes
`.omnipus-vault/` and nothing else; trash moves a *note*. Putting a note-destroying operation
behind the schema-authoring tool would mean an operator who grants type authoring also grants
deletion.

**Hard Constraint #6 makes this final at the tool boundary, and that cuts in our favour twice.**
Policy resolves on tool name alone — there is no per-argument escape, so no operator can write
"allow `vault_restructure` except trash". Two consequences follow, and both are good:

*An operator can grant editing without granting deletion.* `vault_edit: allow` plus
`vault_restructure: deny` is a coherent, useful posture: the agent can fill in properties and
rewrite sections all day and cannot delete anything (AC-X2).

*Trash and restore cannot be split.* Because they share a tool name, there is no configuration in
which an agent may trash but not restore. That asymmetry would be the dangerous one — it would
turn a soft delete into a hard delete from the agent's point of view, leaving it able to destroy
and unable to undo. The tool boundary makes that state unreachable, which is a stronger guarantee
than a policy note asking operators not to do it.

**One thing this section must say plainly.** Trash is an `ask`-worthy operation, but *what* the
policy defaults to for `vault_restructure` is D18's seeding decision, not this convention's. This
document asserts only the placement.

---

## 6. Permanent deletion

**Decision: no agent-facing tool may ever permanently delete a note. Not `vault_edit`, not
`vault_restructure`, not `vault_configure`. There is no `purge` operation, no `force` flag, and
no argument to `trash` that skips the trash.**

**Recommendation and reasoning.** Every argument for an agent-facing permanent delete is an
argument about disk space, and disk space is served by retention, which runs without an agent.
Every argument against it is an argument about irreversibility, and irreversibility is the one
property software cannot apologise for afterwards. The trade is not close.

There is also a specific, local reason to be firm here. **This project has already destroyed
committed work today** by deleting a file that looked like abandoned garbage, in a shared
directory, where the file's tracking status changed between the check and the delete. The lesson
is not "be careful". The lesson is that *a delete decided from an inference about a file's
importance is a delete that will eventually be wrong*, and an agent trashing a note is making
exactly that inference every time. Soft-delete is what makes being wrong survivable, and a
permanent-delete operation is the hole through which that protection drains.

**Three paths to permanent deletion remain, and all three are the human's.**

1. **Retention.** The 30-day sweep. Automatic, uniform, and not addressable by any agent.
2. **The file manager.** The trash is a plain folder of plain files in the user's own vault. They
   can delete it, or one entry of it, with any tool they already use. Nothing here is hidden from
   them and nothing requires our software.
3. **git.** If the vault is a repository, the note is recoverable long after purge, and that is
   the user's own safety net, not ours.

**Two rules make the retention sweep safe, and they come directly from today's incident.**

> **Purge deletes only what it can prove it wrote.** It removes a trash entry when that entry has
> a valid receipt (§7) whose recorded timestamp is older than the retention window. **Anything
> else it finds under `.omnipus-vault/trash/` is reported and left alone** — never deleted on the
> strength of the directory's name or its shape.

A purge that deletes by directory-shape is a program that deletes files it did not create because
they *look* like files it created. That is the same reasoning error as this morning's, expressed
in code and running on a timer. Requiring a receipt makes the sweep's authority exactly as wide
as its knowledge.

> **The retention window is operator-configurable, and the first purge in a vault is reported.**

Deleting a user's files on a schedule they never chose is a strong action taken on their own data.
Thirty days is a sensible default and it matches the session-retention default's shape; it should
not be an unstated constant discovered by someone whose note vanished.

### Options rejected

**A `purge` operation on `vault_restructure`, gated by policy.** Killed by Hard Constraint #6 and
by blast radius. Policy resolves on tool name alone, so a `purge` op inside `vault_restructure`
is granted by any operator who granted rename — an operator enabling routine reorganisation would
be silently enabling irreversible deletion. Moving it to its own tool to fix that costs a
permanent catalog slot for an operation whose best case is saving disk that retention frees
anyway.

**Skip trash entirely when the note is empty or was just created by the same agent.** Killed by
the inference problem above. "This one does not matter" is precisely the judgement that cannot be
trusted, and the cost of being wrong is unbounded while the saving is one directory entry for
thirty days.

**No retention at all — keep trash forever.** Killed by the specification's own reasoning:
without a purge the trash grows without bound and *"the 'soft' in soft-delete costs disk
forever."* Retention with a receipt requirement is the bounded version.

---

## 7. The trash receipt

**New in this document, and it is the one piece of mechanism I am proposing rather than
assembling.** Each trash writes one small JSON file beside the trashed note:

```
.omnipus-vault/trash/20260826T120000Z/
├── entry.json
└── Deals/
    └── Acme Corp.md
```

`entry.json` records the original relative path, the collection, the timestamp, the acting agent,
the record type and identifier if the note was a record, and the count of inbound links that were
left dangling.

**It is not extra bookkeeping looking for a job. It is what six other requirements need in order
to work, and each of them would otherwise re-derive it differently:**

- **Restore addressing (§4).** Maps original path to trash copies without walking the tree, and
  gives the "trashed twice" message both timestamps.
- **Restore containment (FR-048b).** A hand-edited trash path disagrees with its own receipt and
  is refused before containment is consulted — a cheaper first defence than path resolution.
- **The identifier collision refusal (FR-038a).** The record's identifier is recorded, so restore
  can check for a live holder without opening the note.
- **The integrity annotation (§2).** Supplies the original-path map the annotation needs, bounded
  by retention rather than by tree size.
- **Purge safety (§6).** The proof that we wrote this entry, which is what limits the sweep's
  authority to what it actually created.
- **`vault_describe`'s trash report** (FR-048a). Contents and size without reading notes.

**It does not compromise D8.** The receipt lives inside `.omnipus-vault/`, which is unambiguously
our bookkeeping under D8's *"would this survive us?"* test, and it never touches the note's own
bytes. Delete Omnipus, delete the receipt, and the trashed note is still a plain markdown file at
a readable path — recoverable by dragging it back, with no tool of ours involved. **A missing or
malformed receipt must therefore degrade, not fail:** restore falls back to reconstructing the
destination from the directory layout and validating it through containment, exactly as FR-048b
specifies today, and purge leaves the entry alone and reports it.

---

## 8. Findings against the current specification and code

These are raised for review, not fixed here. Severities use the project's convention: a
**blocker** violates a hard constraint or a stated requirement.

### F1 — `move` can permanently delete a note today, bypassing trash entirely — **RESOLVED 2026-08-28**

> **RESOLVED in `883c26d7`, after this note was written.** The finding below is preserved as
> written because its reasoning is what found the defect. Do not act on it as an open blocker.
>
> `(*Renamer).Plan` now calls `authorRefuseReserved` on **both** `from` and `to`, inside the
> existing per-side validation loop — so a move is refused in either direction, at any depth,
> for both marker directories, with the source left untouched.
>
> **Reproduced before fixing, not assumed:** with the destination directory present the move was
> ACCEPTED, the source was removed, and the note landed where no walker descends. After the fix
> it is refused with `ErrReservedLocation`. Guard:
> `pkg/knowledge/rename_reserved_test.go::TestRename_RefusesToolStateDirectoriesInBothDirections`.
>
> **One subtlety this note's reader must keep.** Before the fix the move was *also* refused —
> by an incidental "destination directory does not exist" check. A test written without creating
> that directory therefore passes against the **unfixed** code and proves nothing. Trash lives at
> `.omnipus-vault/trash/`, so building trash removes that accidental refusal and would have opened
> the hole exactly when the guard stopped being redundant. Any future test here must create the
> destination directory first and assert on `ErrReservedLocation` specifically — never merely that
> an error occurred.
>
> A UAT agent later read this section as live and wrote two human-executed cases instructing a
> tester to back up their vault and attempt the destruction. That is the cost of leaving a
> resolved blocker marked open, and the reason for this banner.
>
> **CONSEQUENCE FOR `restore` — read this before implementing it (Stage 4).** The guard refuses
> the boundary crossing in BOTH directions, so `restore` **cannot** be implemented by calling
> `(*Renamer).Plan` with a `from` inside `.omnipus-vault/trash/` — it will be refused, correctly.
> Restore must be its own narrower operation that reads the trash receipt and writes the note back
> to its recorded original path, applying FR-048b containment and FR-038a's collision refusal
> itself. That is not a limitation introduced by the guard; it is the reason F2/F3 flagged
> `restore` as under-specified. A restore built on the generic move path would be exactly the
> unchecked reverse operation this fix closed.


The reserved-location guard `pkg/knowledge/author.go::authorRefuseReserved` refuses any path with
`.omnipus-vault` or `.obsidian` as a segment at any level. **It has exactly one non-test caller:
`pkg/knowledge/author.go::CreateNote`.** The rename and move path does not call it.
`pkg/knowledge/rename.go::(*Renamer).Plan` validates its `from` and `to` with
`Root.ResolveContained` only — and `.omnipus-vault/` is *inside* the root, so containment passes
it.

Two consequences, both live on this branch:

- **`move` into `.omnipus-vault/` is an untracked hard delete.** Both walkers skip that directory
  by name at every level, so the note vanishes from search, from the link graph, and from the
  properties index — with no trash entry, no inbound-link count, no receipt, no audit of a
  deletion, and no restore path. Every protection in this document is bypassed by an operation
  the operator granted for reorganising folders.
- **`move` out of `.omnipus-vault/trash/…` is a restore that skips every check.** It bypasses
  FR-048b's containment refusal, FR-038a's identifier-collision refusal, and the audit both
  require.

**The existing tests cannot detect this, and the way they cannot is the standing rules' rule 1
exactly.** `pkg/knowledge/author_test.go` asserts `ErrReservedLocation` for three cases — the
Omnipus marker, Obsidian's directory, and a nested `projects/.obsidian/plugins/x.md` — and all
three drive it through `CreateNote`. The guard therefore passes every fixture written for it while
being **absent from the two operations that most need it**. A reviewer reading the suite sees
"reserved locations are refused" and is not wrong about what was tested, only about what was
protected. The cheapest check that exposes it is the one the standing rules prescribe: apply the
mutation to a *real* caller rather than a fixture — delete the guard call from `CreateNote` and the
suite goes red, which tells you the guard is reachable; add a rename into `.omnipus-vault/` and
nothing goes red at all.

**Proposed remedy:** `rename`, `move` and `trash` MUST all call `authorRefuseReserved` on both
their source and destination paths, and `restore` MUST be the only operation permitted to read
out of `.omnipus-vault/trash/`. Each needs its own acceptance criterion, driven through the
rename/move path rather than through `CreateNote`.

*Note for whoever fixes this:* the guard's own comment argues it must be checked at every level,
not just the first, and the implementation honours that — it splits on `/` and tests every
segment. Both walkers match it, skipping by directory basename at any depth
(`pkg/knowledge/scan.go`'s `d.Name()` lookup and `contain.go::WalkContained`'s `name` lookup, over
the same shared map). The gap is which operations consult the guard, not how it decides.

### F2 — `restore` is missing from the normative operation table — **blocker**

§4.1.5's `op` table declares exactly three rows: `rename`, `move`, `trash`. FR-048a says
*"`restore` is an operation of `vault_restructure`"*, and §4.1.5's own refusal table carries a
`restore` row. So `restore` is required by two places and declared by none: it has no parameter
list, and no entry in the cascade column that every other row must fill.

**Proposed remedy:** add the row. `restore` · `path`, optional `trashed_at` · cascade: *"the
restored note's inbound links resolve again; the count is reported."* That cascade entry matters
— restore is the mirror of `create`'s bounded, monotone repair, and stating it keeps the tier's
own rule visible.

### F3 — restore's addressing is specified two incompatible ways — **blocker**

FR-048b describes restore as taking *"a trashed path"*. §4.1.5's refusal message reads
`no trashed note at Deals/Acme.md`, which is an original path. FR-048a's second-copy rule then
makes "the trashed path" ambiguous whenever a path has been trashed twice, and nothing says which
copy a bare restore takes.

**Proposed remedy:** adopt §4 of this document — original path, newest copy by default, optional
`trashed_at`, response names the copy taken and lists the others.

### F4 — "the index forgets it immediately" has no mechanism, and the obvious test cannot detect the defect — **warning**

Covered in §3. The existing removal path (`(*Index).SyncWith`) is drift-driven; FR-048 requires
immediacy. The two differ by a window in which search returns a deleted note, and **any test that
runs a scan between the trash and the assertion will pass whether or not the requirement is
met.** This is the standing-rules family of defect: a green result from an instrument that could
not have said otherwise.

**Proposed remedy:** the requirement gains an explicit mechanism (trash drives the removal) and an
acceptance criterion that forbids the intervening scan.

### F5 — the 30-day purge has no named actor, trigger, or safety rule — **warning**

FR-048a states the retention period. Nothing states who runs the sweep, when, what it is
permitted to delete, or what it does with something in the trash directory it did not write.

**Proposed remedy:** §6 of this document — receipt-gated deletion, non-receipt entries reported
and left alone, window operator-configurable, first purge in a vault reported.

### F6 — nothing forbids trashing a path inside `.omnipus-vault/` — **warning**

Same root cause as F1. `trash` of a path inside the marker directory would move our own state
into our own trash. It should be refused by name.

---

## 9. One question that is genuinely the operator's

**Should the 30-day retention purge be enabled by default?**

It deletes the user's own files, in the user's own folder, on a timer they did not set. I have
given a recommendation rather than assuming the answer.

- **Option A — on by default, 30 days, configurable, first purge reported.** *Recommended.*
  Bounded disk, matches the session-retention default's shape, and the receipt rule (§6) means
  the sweep can only ever delete files it created. The report on first purge is what converts a
  silent default into an informed one.
- **Option B — off by default; trash grows until the operator enables retention.** Safest for
  data, and it makes the specification's own objection real: *"the 'soft' in soft-delete costs
  disk forever."* A vault where an agent trashes routinely would accumulate indefinitely, and
  nobody would notice until it mattered.
- **Option C — on by default with a longer window (90 days), matching session retention exactly.**
  Defensible; the argument against is that 90 days of an agent's deletions is a large amount of
  disk to spend on a case the user can already cover with git.

Everything else in this document is decided and does not wait on this answer. Only the default
value and the window length do.

---

## 10. Where this belongs, and what I deliberately did not do

**This is a design note, not an ADR amendment, and that is a judgement worth stating.** The six
core behaviours are *already* normative in ADR-068 revision 11 and spec Draft 9. Restating them in
a new ADR revision would create a second authority for rulings that already have one — and the
specification's own history shows what that costs: a control specified in two places gets deleted
from one and still looks present. What W4 owed was the assembly and its review, and that is what
this is.

**The six findings in §8 and §9 are amendment material, and I did not make the amendments.** Two
reasons. ADR-068 and the spec are the shared authority that five other agents in this worktree are
reading right now, and editing them mid-stage — while their revision numbers are cited in every
other agent's brief — is the shared-worktree hazard rather than the fix for it. And F1 is a code
defect, not a specification defect: the specification is right and the code does not implement it,
so it belongs to whoever owns `vault_restructure` in Stage 4, with an acceptance criterion, not to
a document edit.

**I modified no file I do not own.** This document is new. Nothing else in the worktree was
touched, moved, or removed.
