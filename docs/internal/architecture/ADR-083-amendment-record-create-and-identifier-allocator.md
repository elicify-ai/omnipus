# ADR-083 amendment: record creation over REST, and the `.seq` identifier allocator

- **Status:** Accepted (founder-approved; keep the create door, govern it and write it down)
- **Date:** 2026-09-11
- **Amends:** [ADR-083](./ADR-083-embedded-content-in-knowledge-base-notes.md) (CW-7, the record write door)
- **Related:** [ADR-068](./ADR-068-vault-records-typed-record-layer.md) (D7.1 identity
  allocation, D14 the write contract, FR-036), CLAUDE.md Hard Constraint #6
  (tool policy), Hard Constraint #8 (contract-first)

## Why this document exists

`handleRecordCreate` and its `.omnipus-vault/records/<type>.seq` identifier
allocator exist, work, are tested, and — until the create/update contract
split landed alongside this amendment — **were called by nothing in the SPA**.

They were never scoped by ADR-083. They arrived because the ADR-068 contract
shape implied them: `RecordWriteRequest` made `id` optional, "no `id`" had to
mean something, and the only sensible meaning was *create*. So a whole
create path, including a persistent counter that allocates permanent
identifiers, entered the codebase as an inference from a field's optionality
rather than as a decision anyone recorded.

A capability nobody decided to add is a capability nobody decided how to
govern. This amendment records the decision that was missing.

**The founder's ruling: KEEP them.** Record creation is a core agent
capability — an agent must be able to create notes and whole knowledge bases,
and UI gaps are acceptable where agent gaps are not. Deleting a working create
door to tidy an unscoped surface would remove a capability the product needs.
What was missing was not the code; it was the governance statement and the
record of it.

## What exists

### The create door

`POST /api/v1/library/{workspace_id}/knowledge/records` with `mode: create`
(before the split: with `id` absent). It:

1. requires `path`, the vault-relative location of the new note;
2. resolves the workspace's knowledge-base scope and requires it to name
   **exactly one** collection — zero is a 404, more than one is a 400
   (`ambiguous_collection`), because the server never guesses which vault a
   new record belongs in;
3. loads the record type's schema and refuses an undeclared type;
4. validates every property through the shared guards
   (`records.CheckRecordPropertyWrites`) — the same rules an update runs;
5. **mints an identifier** (below);
6. assembles the frontmatter in memory through the same `NoteEdit`
   primitives an update splices with, so the two doors cannot disagree about
   what a valid write looks like;
7. writes via `knowledge.CreateNote` — an `O_EXCL` create, so a note already
   at `path` is refused (`path_exists`, a caller-fixable 400) rather than
   overwritten;
8. audits the outcome and answers **201** with the new record and its first
   version token.

A create is **not** a compare-and-swap. There is no prior version to compare,
so `version_token` is meaningless on it — which is exactly why the contract
split now rejects the field there instead of silently ignoring it.

### The `.seq` allocator

`allocateRecordID` mints `<identity_prefix>-NNNN` (four digits minimum,
widening rather than wrapping past 9999). State lives in one file per record
type:

```
<vault>/.omnipus-vault/records/<type>.seq
```

- A missing or empty file starts the sequence at 1.
- A file whose contents do not parse as a non-negative integer is a **fault**,
  reported — never silently reset to zero, because a reset counter re-issues
  identifiers already handed out.
- The counter is persisted with an atomic write (`fileutil.WriteFileAtomic`, 0600).
- A schema with no `identity_prefix` mints a bare zero-padded number.

**Collision scan.** An operator can hand-write a note carrying an `id:` the
counter has not reached (imported data, a restored trash copy — FR-038a's own
scenario). The allocator therefore takes **one** walk of the collection to
collect the live identifier set, then scans forward **in memory** past any
collision, up to 10,000 attempts. It never descends into `.omnipus-vault/`,
so a trashed copy cannot block an identifier. A note whose bytes cannot be
read is an error, not a skip: "I could not look" must never be treated as
"it is free".

### What the allocator's lock actually excludes

This is worth stating precisely, because the natural reading of the code is
wrong. The counter is advanced under `knowledge.WithNoteWriteLock` — the same
locking **primitive** every note write uses — but keyed on the `.seq` path,
`.omnipus-vault/records/<type>.seq`, a key ordinary note writes never pass.
`WithNoteWriteLock` keys a 64-shard striped mutex on `(collectionRoot, rel)`,
so *the same function* is not *the same lock*.

**The real guarantee is: two concurrent creators of the SAME record type in
the same collection cannot be handed the same next value.** They pass an
identical key and contend for an identical lock. That is all.

It does **not** serialise against unrelated note writes in the collection, and
it does not need to: nothing about writing some other note changes what the
next free identifier for this type is — except adding a note that already
carries one, which the collision scan handles by reading the vault rather than
by excluding the writer.

Cross-process exclusion rides the advisory file lock half of
`WithNoteWriteLock`, which is **POSIX-only**: `fileutil.WithFlock` is a
documented no-op on Windows, so on Windows two gateway processes against one
vault have in-process protection only. That is the file-store family's
standing posture (ADR-054 §5), not a property of this allocator.

## How it is governed

The create door carries the same posture as every comparable write surface in
the Library family, and it already did before this amendment:

| Control | Mechanism |
|---|---|
| Authentication | `withUploadAuth` → `withAuthAndBodyLimit` on the `/api/v1/library` registration — an authenticated Bearer caller, same as every Library write |
| Transport rate limit | outer `withRateLimit(configLimiter, …)` on the same registration |
| Knowledge rate limit | `allowKnowledgeRetrieval(w, workspaceID)`, the **first statement** of `handleKnowledgeRecordWrite`, before the body is read — so both create and update are metered on the per-workspace knowledge limiter |
| Attribution | `resolveRecordWriteActor` runs before any file is opened: `user:<username>` when authenticated, the literal token `anonymous` under `dev_mode_bypass`, and a **403 refusal** when neither — an UNATTRIBUTED write is permitted, an UNATTRIBUTABLE one is not |
| Scope confinement | only collections in the calling workspace's own scope are reachable; an ambiguous or empty scope refuses rather than guessing |
| Write guards | `records.CheckRecordPropertyWrites` — derived (FR-046), relation/person (FR-045), arity and list-shrink (§4.6), identity keys (D1/D7), enum conformance |
| Audit | every outcome, applied and refused alike, under the registered `knowledge.note.write` event with a specific refusal reason token |

### Tool policy does not apply here, and that is not a gap

Hard Constraint #6 governs the **builtin tool catalog** — per-agent entries
under a reconciled global ceiling. This is a REST door, not a tool. No agent
tool calls it: an agent reaching a vault does so through `knowledge_edit`,
`knowledge_base_create` and the rest of the `knowledge_*` family, each of
which carries its own tool-policy entry and is governed by the two-layer
ceiling in the ordinary way.

So there is no `record_write` policy key to add, and adding one would be
worse than useless — it would imply a control surface that governs nothing,
which is the ADR-037 anti-pattern this project explicitly bans (a setting that
saves successfully and changes no behaviour).

**The governance boundary is therefore: the REST door is governed by
authentication, rate limiting, attribution and audit; the agent doors are
governed by tool policy.** A future agent-facing record write is a TOOL and
inherits Constraint #6 in full — it needs a catalog entry, a shipped default
in `pkg/config/defaults.go`, and per-agent seeding. That work is deliberately
out of scope here.

## Two review findings, already closed before this amendment

Both were carried into this work as open items. Both were already fixed at the
base commit (`42e9a6846`) and are recorded here so they are not re-opened:

1. **The allocator's doc comment no longer misdescribes its lock.** It
   previously claimed the counter advanced "under the SAME per-collection
   write lock every note write takes". That string does not appear anywhere in
   the tree; the comment now states the real guarantee (same-record-type
   creators only), under the heading "WHAT THE LOCK ACTUALLY EXCLUDES,
   precisely".
2. **The collision loop is already hoisted.** `liveRecordIDs` is called once
   before the scan and the loop probes an in-memory map. The pathological
   shape the finding describes — up to 10,000 full collection walks under the
   lock — is documented in the code as the defect that *was* fixed
   ("ONE WALK, THEN AN IN-MEMORY SCAN").

## Consequences

- Record creation over REST is a supported, recorded capability rather than an
  accident of an optional field, and the SPA now has a contract that names it
  (`mode: create`) rather than one that produces it by omission.
- The `.seq` files are **vault state**. They live inside the vault, under
  `.omnipus-vault/records/`, and they travel with it. An operator copying a
  vault copies its counters; an operator deleting one restarts numbering at 1
  and the collision scan is what stops that re-issuing a live identifier.
- The allocator's mutual exclusion is per `(collection, record type)`. Two
  vaults, or two record types, never contend.
