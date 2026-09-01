# UAT run 2 — knowledge tools, all six · findings with root causes

**Date:** 2026-09-01 · **Tester:** one Omnipus agent, `type: Main`, created for this run
**Model:** `z-ai/glm-5.3-flash` via OpenRouter · **Build:** the write-path fix (`51ffe681e`)
**Method:** driven over `/api/v1/chat/ws`; every result read back from the gateway or
from the vault on disk, never from the agent's narration.

Root causes were investigated by dedicated subagents against the source. Where a
root cause is stated below it was traced in code, not inferred from behaviour.

---

## F-1 · `knowledge_find` is uncallable when a workspace has more than one collection · **BLOCKER**

**Symptom.** Asked to find records, the agent made 12–24 tool calls, each returning
in ~1 ms, and burned the entire turn until timeout. Repeated at 180 s, 240 s and 420 s.
The refusal it kept receiving:

```
knowledge_find: no single knowledge base is unambiguously in scope for this
workspace (none mounted, or more than one); in scope: Omnipus Knowledge UAT Corpus, kb
```

**Root cause — verified.** `knowledge_find` has **no `collection` argument at all**.
`vaultprops.FindTool::Execute` calls `scope.Select("")` *unconditionally, before it
decodes its arguments*, and `knowledge.Scope.Select("")` succeeds only when exactly one
collection is in scope. `knowledgefind.Parameters` declares `additionalProperties: false`
over sixteen properties, none of them `collection`, and `knowledgefind.AcceptedParameters`
omits it too.

So in a two-collection workspace the tool is **literally uncallable**, and the refusal
names two collections as though naming one were the fix — with no argument in which to
name them.

**Why the agent looped rather than gave up.** An informative refusal *does* exist —
`"collection" is not an argument of knowledge_find; accepted: words, type, kind, …` —
and it would have taught the model the truth in a single call. It is unreachable,
because the scope gate runs ahead of argument decoding. **Reversing those two steps
turns a 24-call timeout into one useful refusal.** That is the smallest fix.

**Related naming defect.** Of the three plausible identifiers, only two resolve:

| passed to `describe`/`read` | resolves | why |
|---|---|---|
| `Omnipus Knowledge UAT Corpus` | **yes** | display name from `vault.json` |
| `kb` | **yes** | no `vault.json`, so display name falls back to the folder name |
| `corpus` | **no** | the folder basename is overridden by the marker's display name |

A collection with a `vault.json` is addressable *only* by a name that appears nowhere
in any path the agent ever sees. `pkg/vaultprops/find_tool.go` has no dedicated test
file, which is consistent with this shipping unnoticed.

---

## F-2 · Two abandoned turns take the whole instance down, on any Mac · **BLOCKER**

**Symptom.** After several client-side timeouts, every subsequent turn returned in
under 400 ms with zero tool calls and the text *"I'm at capacity right now — please try
again in a few seconds."* It never cleared. Only a restart recovered it.

**Root cause — three defects that compose, all verified in code.**

1. **Total concurrency is 2 on every non-Linux install, from a fabricated number.**
   `pkg/config/meminfo_other.go::readMemAvailableBytes` is literally `return 0` on
   `!linux`. The auto-detect divides that by 3.5 MiB, gets 0, and lands on
   `clampParallel`'s floor of **2** — on an 8 GB laptop and a 192 GB Mac Pro alike.
   Confirmed on the live instance: `{"effective_max_parallel_agents":2}`. Nobody
   configured this; it is an invented value presented as a real one.

2. **A client disconnect cancels nothing.** The turn runs on a `sessionWorker` whose
   context comes from `context.Background()`, deliberately detached from the request.
   The one disconnect-triggered cancellation path — the ADR-045 orphan watchdog — is
   armed with a grace of `0`, and `ArmOrphanForegroundTurnWatch` returns immediately on
   `graceSeconds <= 0`. It is fully built, tested, and **dead by default**. There is
   also no per-turn wall clock (`timeout_seconds: 0`) and no reaper of any kind.

3. **Abandoning the socket makes the turn slower.** `websocket.go::sendRawFrameBytes`
   never checks the connection's done channel, so once the client is gone every token
   frame walks a `{0, 10ms, 50ms}` backoff before being dropped — ~60 ms of sleep per
   token, on the turn's own goroutine, for the rest of the turn.

The slot is held for the turn's full duration and released correctly on exit — **this
is not a leak**. It is a duration problem against a cap far too small.

**Direct evidence**, from the gateway log rather than inference:
`{"level":"warn","component":"agent","soft_cap":2,"active":2,...,"At capacity — rejecting new session"}`

**A hypothesis I had that was WRONG, recorded because it nearly misled me.** I cited
"14 sessions all `status: active`" as evidence of 14 running turns. It is not.
`status` is stamped once at session creation and nothing flips it on ordinary
completion — the codebase says so itself, in `cancel_prearm.go`, where session status
was evaluated and explicitly rejected as a turn-liveness signal. Fourteen active
sessions is consistent with a perfectly healthy instance. It survived a restart, which
is what exposed my error.

**Mitigation applied for this run, zero code:** set `performance.max_parallel_agents`
explicitly (32). Verified: `{"effective_max_parallel_agents":32}`.

**Smallest real fix:** give non-Linux a genuine memory signal — `unix.SysctlUint64("hw.memsize")`
is pure Go and satisfies the no-CGo constraint. That file is never compiled by this
project's Linux-only CI, so the bug is invisible to it by construction.

---

## F-3 · A collection added by copy can NEVER be indexed, by anyone · **BLOCKER**

**Symptom.** 200 notes on disk, `.omnipus-vault/` present, discovered by `describe` and
readable by `read` — but `knowledge_find` refuses: *"index holds 0 notes, 200 on disk"*.
No index directory exists, zero indexing log lines, and two full restarts changed nothing.

**Root cause — verified.** Omnipus has **two independent answers to "which collections
exist", and they were never reconciled**:

- **Tool-time:** `knowledge.ResolveScope` treats the workspace work tree as a scope root
  and **walks the filesystem** for any dir containing `.omnipus-vault/`. This is why
  `describe` and `read` see the collection instantly.
- **Index-time:** `gateway/knowledge_lifecycle.go::AttachAllMounts` enumerates **only the
  mount store** (`$OMNIPUS_HOME/entities/mounts/`) and never looks at a work tree at all.

Indexing is reachable *exclusively* through `AttachMount`. A collection that is not a
registered mount is never indexed, by any path, ever.

**And it cannot become one.** `pkg/workspace/mount.go::ErrMountRefused` (FR-7.5) refuses
any target inside `$OMNIPUS_HOME` — which a workspace work tree is, by definition. The
mount-create endpoint returns **403**.

So the collection sits in a state the system has **no exit from**: permanently visible,
permanently unindexed. No agent tool, no CLI verb, and no REST endpoint can change it —
all three were searched exhaustively and none exists. `vaultprops/sync.go` even
anticipates the hole in a comment, referring to a *"future standalone re-index command"*
that was never written.

**The diagnostic is wrong too, and it misdirects.** `LoadManifest` returns a zero
manifest with a **nil error** when the file is absent, so `describe` sets
`ManifestKnown = true` and its `"NOT INDEXED yet"` branch becomes unreachable. The
never-indexed state renders as a *drift* state, and the remedy it prints — *"re-index to
reconcile"* — names an action that does not exist. The correct wording already exists in
`vaultprops/reader.go::errIndexNotBuilt` but never reaches the headline.

**Correct behaviour worth crediting:** the typed path **refuses rather than answering
"0 results"** (`knowledgefind/find.go::findRecords`, `IndexUnavailable`). Silently
returning zero would be far worse. See F-9 for where that protection does not hold.

---

## F-4 · `knowledge_restructure` rename reports "0 inbound links" while two relations point at the note · **UNDER INVESTIGATION**

Renaming `co-0028-trenholme-works-9.md` reported `CASCADE: 0 notes rewritten (inbound
wikilinks)` and `BACKLINKS (0) — none`, while two project notes carry
`company: "[[Trenholme Works 9]]"`.

**I am deliberately not calling this a defect yet.** The wikilink text never matched the
file's basename, before the rename either, and the note's `name:` property is unchanged.
So the links may resolve by identity rather than filename, in which case nothing broke
and the fixture is simply odd. Root-cause investigation is running to settle which.

Recorded this way on purpose: reporting it as corruption before establishing link
resolution would be exactly the kind of confident-and-wrong finding this process exists
to prevent.

---

## F-5 · `knowledge_configure` truncates its cascade list at 10 · **MEDIUM**

A breaking schema change (narrowing `company.status` to a single value) was **accepted
without confirmation**, and reported honestly: *"18 record(s) lost validity"*, each
named with its file, offending value and the expected enum.

The list stops at ten: *"... and 8 more"*. An agent asked to repair the damage cannot
see eight of the notes it must fix, and has no paging argument to retrieve them.

The acceptance-without-confirmation is arguably correct — it reported fully and the
schema is the operator's to change — but combined with truncation it means a
destructive change can be made whose full consequences are never shown.

---

## F-6 · `describe` and `configure` disagree about whether the vault has content · **MEDIUM**

Found unprompted by the tester, in the same turn:

> *"the vault reports itself as 'indexed and empty — this collection holds no indexable
> notes', yet the cascade found 38 matching notes. The describe output and the cascade
> disagree about whether the vault has content."*

One subsystem reads an empty index; the other walks the notes directly. Both are
"right" and they contradict each other in the same response. Almost certainly the same
root cause as F-3, but recorded separately because the *contradiction* is its own
defect: an agent cannot act on a tool that disagrees with itself.

---

## F-7 · An agent can repeat one failing call 20+ times with nothing stopping it · **MEDIUM**

In one turn: `knowledge_find` called **20 times consecutively**, every call returning the
same refusal in ~1 ms. Nothing detected the repetition — not the loop, not the tool, not
the turn budget until it expired.

Each individual refusal was well-formed. The defect is that a fast, deterministic,
identical failure is free to repeat until the turn dies. With F-2's cap of 2, two such
turns are an outage.

---

## F-8 · A capacity refusal is not flagged as a failed turn · **LOW**

Turns rejected with *"I'm at capacity right now"* returned `done.stats.turn_failed = false`.
No model was called and no work was done, but a harness checking only that flag scores
the turn as a success. This is precisely the field the contract tells automation to
branch on.

---

## F-9 · `knowledge_find words=` silently answers "0 results, COMPLETE" over an unindexed vault · **CRITICAL**

**The most severe finding of this run, and the only one where a tool states a false
fact with a guarantee attached.**

Ground truth: **11 notes on disk contain "Vorlex"**. Asked for exactly
`knowledge_find words="Vorlex"`, the tool returned:

```
QUERY: words="Vorlex"  limit=50
COMPLETE: yes — 0 records matched
```

**No refusal. No warning. `COMPLETE: yes`.**

The tester then reasoned exactly as designed, and was led to a false conclusion —
quoting the tool's own contract back:

> *"per the tool's own contract ('an empty answer means the vault is empty'), the vault
> contains nothing matching 'Vorlex'"*

**Root cause — verified.** `knowledgefind/find.go::(*query).textOnlyServable` deliberately
lets a bare `words=` query through on a nil properties store, answering from the bleve
text index alone. Here the bleve index has also never been built (F-3), so the search
returns zero hits and `find.go` takes the `len(wordPaths) == 0` path to
`responses.go::zeroHitResponse`, which sets `Complete: true, Refused: false`.

The typed path's `IndexUnavailable` refusal exists precisely to prevent this. **The
words path bypasses it.** Same tool, same unindexed vault, same question — one path
protects the caller and the other misleads it.

This is worse than F-1's uncallable tool and worse than F-3's dead index. A tool that
cannot be called is obvious. A tool that answers "0, and I checked everything" is
believed.

---

## What passed

| | |
|---|---|
| `knowledge_read` | correct content, correct property values |
| `knowledge_edit` `set_property` | enum violation refused, message named property, value and all permitted values |
| `knowledge_edit` misspelled property | refused, no stray property created |
| `knowledge_edit` repair of an invalid note | allowed, per the "judge the value, not the record" rule |
| `knowledge_edit` `op: create` body frontmatter | **refused** — the fix from `51ffe681e`, re-verified this run |
| `knowledge_describe` | accurate schema and integrity reporting |
| `knowledge_configure` | schema change applied and persisted; cascade specific and honest |
| Agent recovery from refusals | recovered unaided from every well-formed refusal, using only its text |

**The pattern across all eight findings:** where a tool refuses *well*, the agent copes
perfectly. Every failure here is a tool that either cannot be called at all (F-1),
cannot be fixed by the agent (F-3), or contradicts itself (F-6) — not a tool that let
bad data through.
