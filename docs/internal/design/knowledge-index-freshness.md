# Index freshness — instant updates, watching, and why a missed event is survivable

**Status:** design, agreed with the operator 2026-09-02, pre-implementation
**Supersedes:** §9 of `knowledge-tools-remediation.md`, which stated the
requirements; this states how they are met.

---

## 1. The requirements

Operator rulings, verbatim in substance:

1. **A write through Omnipus's own vault tools updates the index INSTANTLY.**
2. **Startup indexing is INCREMENTAL** — reconcile, never rebuild.
3. **A file a human adds through the UI is indexed INSTANTLY.** A file added
   *outside* Omnipus may wait for the startup sweep.

Requirement 2 is already met and needs no work: `Index.SyncWith` reconciles
against the manifest and skips unchanged files, reporting `Indexed` and
`Unchanged` separately.

A fourth case emerged in discussion and is now in scope: **a file changed
outside Omnipus WHILE IT IS RUNNING.** With the vault also open in Obsidian —
the expected setup — this is not an edge case, it is the normal case.

---

## 2. The governing principle

> **The watcher is an optimisation. It is never the source of truth.**

This is the whole design, and it is the one structural difference from
Obsidian, where the watcher *is* the record of what exists — so a dropped event
means wrong-until-reload.

Here, correctness rests on the **content hash** and the **incremental sweep**,
both of which already exist and already work. A missed event therefore costs
**latency, not correctness**.

Everything below follows from that sentence.

---

## 3. Four layers, each covering what the others cannot

| layer | covers | why it cannot be dropped |
|---|---|---|
| **direct update** on agent writes | the agent's own writes | read-your-own-writes (§4) |
| **watcher** | UI adds, external edits | instant, and needs no per-caller wiring |
| **periodic sweep** | anything the watcher missed | watchers drop events (§5) |
| **startup sweep** *(exists)* | anything missed while stopped | the floor |

**The hash check is what lets these overlap safely.** Every path asks "has this
file's content actually changed?" before doing work, so the layers are
idempotent and may fire on the same file in any order without fighting. That is
also why Omnipus's own writes triggering its own watcher is harmless — the
event arrives, the hash matches, nothing happens. **No self-event suppression
is needed**, which removes an entire class of bug rather than solving it.

---

## 4. Why the watcher cannot replace the direct update

A watcher is asynchronous:

```
agent writes → tool returns → (ms later) → watcher fires → index updates
```

An agent that writes a note and immediately searches for it — an ordinary loop
— **can search before the index knows**, and get "not found" for something it
just wrote.

That is the exact failure class this project spent 2026-09-01 removing: a
confident wrong answer. Building it in deliberately would be worse than the bug
we fixed, because it would be by design.

So the agent write path updates the index **before the tool returns**.

**The UI handlers do NOT need this.** A human uploading a file does not search
for it in the same millisecond, so the watcher covers requirement 3 with no
per-handler wiring — which is the operator's own observation and it is correct.
It removes work rather than adding it, and removes the "sixth handler's author
forgot" risk with it.

---

## 5. Bursts: detect the rate, escalate — never drop

A rate LIMIT that discards events would recreate silent staleness. What is
wanted is an **escalation**:

| incoming rate | action |
|---|---|
| a few files | update each individually — fast, precise |
| a burst | stop processing events; run **one incremental sweep** |

Above a threshold the sweep is *both cheaper and more reliable* than the events:
processing N individual updates is slower than one hash-skipping pass, and the
pass is **guaranteed complete** where the event stream may already have dropped
something.

So a burst degrades into **the thing that already works**, not into brokenness.

The short quiet-period debounce is the same mechanism at small scale — three
saves in a second collapse to one update. One rule at two scales: **never do N
pieces of work when one covers them all.**

---

## 6. Not a size problem — measured, not assumed

An earlier draft claimed large vaults cannot be watched. **That was wrong.**

The founder's real vault: **3,002 files across 385 directories.** On macOS one
handle watches the whole tree, cost independent of size. On Linux watches are
per-DIRECTORY, so 385 against a typical limit of 65,536+ — two orders of
magnitude of headroom. A vault ten times this would not come close.

**The real limit is event RATE, not file count** (§5), and a burst can overflow
the queue on a small vault as easily as a large one. There is therefore **no
watch cap and no size-based degradation** in this design; that complexity was
removed once the number was measured.

---

## 7. iCloud, specifically

The vault lives in iCloud, so this is the primary environment, not an edge case.
iCloud produces spurious events, `.icloud` placeholders, conflict copies, and
atomic-replace writes that look like delete-then-create.

- **The hash check absorbs most of it.** An event whose content did not change
  is a no-op.
- **Debounce** collapses the write bursts a sync produces.
- **Sync artifacts are skipped** by name.
- **Atomic replace** is handled because delete-then-create on the same path
  resolves to "this path's content is now X", which is what the hash answers.

---

## 8. It must never degrade silently

This is Obsidian's actual failure mode: when watching stops working, nothing
says so — results go stale and users learn to reload out of habit.

Given what this project found on 2026-09-01 — a search answering *"0 results,
I checked everything"* over a vault it had never read — **the watcher must fail
loudly**:

- if watching cannot start, or stops, that is **stated**, not inferred;
- the periodic sweep interval tightens when watching is unavailable;
- and search continues to answer honestly, because its completeness guarantee
  already depends on index state rather than on the watcher.

---

## 9. Dependencies

**No new runtime dependency.** `golang.org/x/sys` is already a direct
dependency and provides the raw notification syscalls in pure Go — the same
library ADR-072's macOS memory reader relies on. `fsnotify` appears in `go.sum`
only as a stale module-graph entry and is not linked.

Windows is foreseen but out of scope, consistent with the platform's existing
posture; it falls back to the periodic sweep, and that is stated rather than
discovered.

---

## 10. What is deliberately NOT built

- **No watch cap or size-based degradation** — §6 measured it as unnecessary.
- **No self-event suppression** — §3's hash check makes it unnecessary.
- **No per-UI-handler wiring** — §4, the watcher covers them.
- **No document text extraction on any path.** Attachments are indexed by name
  with zero bytes read. An "instant" path that started reading PDFs would
  reverse the operator's ruling through a side door, so every write path must
  preserve it and be tested for it.
