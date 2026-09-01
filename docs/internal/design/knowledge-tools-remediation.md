# Knowledge tools — remediation design

**Status:** design, pre-implementation · **Date:** 2026-09-01
**Author:** architect · **Input:** `docs/internal/uat/uat-findings-knowledge-tools-2026-09-01-run2.md`,
`docs/internal/uat/uat-report-knowledge-tools-2026-09-01.md`,
`docs/internal/design/knowledge-write-path-type-safety.md`

**What this document is.** Nine UAT findings reduced to **four problem classes**, one
ruling each, plus a dependency-ordered plan and an explicit list of what not to do.
It changes no code.

**Method note.** Every root cause below was re-derived from source in this worktree
rather than taken from the findings. Three of the findings' claims needed correcting
and the corrections are stated where they occur — two of them make the finding
**worse**, not better.

---

## 1. The cross-cutting cause

The findings read as nine unrelated defects. They are not. **Six of the nine are one
mistake made in four different packages:**

> **An absent measurement is encoded as the number zero, and then consumed by a
> caller that cannot tell it apart from a measured zero.**

Four independent instances, all verified:

| where | the absent thing | encoded as | consumed as | finding |
|---|---|---|---|---|
| `knowledgefind/find.go::findRecords` | a text index that was never built | zero hits | "the vault contains nothing matching that" — `Complete: true` | **F-9** |
| `knowledge/manifest.go::LoadManifest` | a manifest file that does not exist | `NewManifest(root)` + **nil error** | `ManifestKnown = true`, `ManifestCount = 0` → "indexed and empty" | **F-3, F-6** |
| `config/meminfo_other.go::readMemAvailableBytes` | no memory probe on this OS | `return 0` | `0 / 3.5 MiB` → floor of 2 → `effective_max_parallel_agents: 2` | **F-2** |
| `vaultprops/find_tool.go::openFindStore` | a properties index that was never built | `(nil, nil)` | Store-nil, which `find` then partly papers over | **F-9** (contributing) |

In every case the type had no way to say "I did not look". `uint64` has no absent.
`*Manifest` with a nil error has no absent. `len(hits) == 0` has no absent. So the
caller, correctly reading a zero, states a fact that was never established — and in
three of the four it attaches a completeness or accuracy guarantee to it.

This is the same failure shape `docs/internal/false-green-patterns.md` exists to
catalogue, arriving through the product surface instead of the test surface. It is
also the shape the write-path design already named as *"this project's most expensive
one: a check that silently did not run looks exactly like a check that passed"*
(`knowledge-write-path-type-safety.md` §1.3, on G3/G4). That design fixed it for
**writes**. Nobody fixed it for **reads**, and reads are where it does more damage:
a refused write is visible to the agent in the next line; a false zero is believed.

**The codebase already knows the rule and applies it four lines away.**
`knowledgefind/near.go::nearReachable` refuses when relation resolution is unwired,
with this comment:

> *"Falling through silently here would answer EVERY near/hops query with an empty
> neighbourhood forever, indistinguishable from 'that note has no relations' —
> precisely the quiet degradation Deps.Text is required, not optional, to prevent."*

`findRecords`' `words` path is the one place in the same file where that reasoning is
not applied. This is not a missing principle. It is a principle with a hole in it.

### 1.1 Verdict on the stated hypothesis

The brief proposed that **F-1, F-3 and F-6 are one thing**: two unreconciled answers to
"which collections exist" — `knowledge.ResolveScope` walking the filesystem versus
`gateway/knowledge_lifecycle.go::AttachAllMounts` reading only the mount store.

**Verified, and the split is: yes for F-3 and F-6, no for F-1.**

*Confirmed for F-3 and F-6.* `knowledge.ResolveScope` seeds its roots from
`workspace.SafeWorkDir(home, workspaceID)` **and** `workspace.AllowedMountRoots`, then
walks for `.omnipus-vault/` markers. `KnowledgeLifecycle.AttachAllMounts` reads
`workspace.MountStoreDir(kl.home)` and nothing else. Indexing is reachable **only**
through `AttachMount` — I checked every caller: `AttachAllMounts` (boot) and
`rest_workspace_mounts.go` (mount create). There is no third entry point, no REST
re-index verb and no CLI re-index verb. F-6 is the same defect wearing a different
face: `describe` reads the manifest, `configure`'s cascade walks the notes, and for a
work-tree collection those two are guaranteed to disagree.

*Not confirmed for F-1.* F-1's cause is independent and mechanical:
`vaultprops/find_tool.go::(*FindTool).Execute` calls `scope.Select("")` **before it
decodes its arguments**, and `knowledgefind.AcceptedParameters` declares no
`collection` at all. That defect exists in a workspace with two mounts and no work
tree involved. What the scope/index split did was **create the two-collection
condition that made F-1 fatal in this run**. Shared trigger, separate causes, and both
need fixing on their own terms.

I am flagging this because fixing only the scope/index split would leave F-1 live and
looking fixed — the UAT would pass once the second collection went away, and the tool
would still be uncallable the day an operator mounts a second vault.

### 1.2 One more structural fact the findings did not surface

`knowledge.CreateInWorkspace` — the function ADR-067 **D11** ("Creating a KB:
workspace-first, movable") specifies as the *only* way Omnipus creates a knowledge
base — **has no production caller.** Only its own doc comment and an error constant
reference it.

So the work-tree half of the collection model is currently **discovery-only**: scope
finds work-tree collections, ADR-067 D11 says they are the primary creation path,
`pkg/workspace/mount.go::CheckMountTarget` refuses to mount anything inside
`$OMNIPUS_HOME` (which every work tree is, by definition), and nothing creates or
indexes one. The UAT reached that state by copying a folder in — the only route
available — and found it inescapable. That is not an edge case an operator stumbled
into. It is the ADR's intended primary path, built one third of the way.

---

## 2. Rulings

Four problem classes, one ruling each. Each ruling states the rule, the argument, what
it costs, and whether it touches a wire contract.

---

### R1 — When may this product answer "zero"? · covers **F-9**

> **RULE (applies to every retrieval surface, not only `knowledge_find`).**
> A retrieval answer may report zero results with a completeness guarantee **only when
> the mechanism that produced the zero was able to observe a non-empty population and
> found nothing in it.** If the population is empty, unknown, or was never built, the
> answer is a **refusal naming the state** — never a complete zero.
>
> Restated as the invariant to test against: **`complete: true` with zero rows is a
> claim that the corpus was searched. If nothing was searched, nothing may be claimed.**

**This is not a new rule. It is already the contract, and the code violates it.**
`contracts/components/schemas/VaultFindResponse.yaml` defines `complete` as:

> *"True only when the query covered everything it was asked to cover: nothing excluded
> for a type violation, no clamp, no refusal, no stale row, no truncated scope, **no
> unavailable index**."*

`knowledgefind/responses.go::zeroHitResponse` sets `Complete: true, Refused: false`
unconditionally. Over an unbuilt index that is a **contract violation**, not a design
gap. Nothing needs deciding; something needs fixing. `RecordProblem.code` already
carries `index_unavailable`, and the typed path already uses it.

**Correction to the finding — the blast radius is larger than reported.** F-9 describes
this as a bare-`words=` problem, attributing it to
`(*query).textOnlyServable` letting a words-only query through on a nil properties
store. That is not where the lie is produced. In `find.go::findRecords` the sequence is:

1. run `d.Text.Search`;
2. `if len(wordPaths) == 0 && !wordsTruncated { return zeroHitResponse(...) }`;
3. resolve `near`;
4. **only then** `if d.Store == nil { … }`.

Step 2 returns before step 4 is ever reached. So **any** query carrying `words` — bare,
or fully typed with `type`, `filter`, `sort` and `select` — answers `Complete: true, 0
records` over a never-built text index. `textOnlyServable` is downstream of the bug and
is not implicated. The two-doors framing from the earlier write-path finding (G1) is
exact here too: `IndexUnavailable` guards the door the tester did not use.

**Where the line falls, precisely.** Three zeros, three different answers:

| the mechanism observed | answer |
|---|---|
| a populated index, no document matched | **`complete: true`, 0 rows.** Correct today, must stay. |
| an index with zero documents, or whose population could not be read | **refuse**, `index_unavailable`, naming the collection and the state |
| a collection outside this workspace's scope | **`complete: true`, 0 rows** — deliberate, keep |

The third is the one genuine exception and it must be preserved. `VaultFindResponse`
states it: *"An out-of-scope query is not an error either — it returns no rows with
`complete: true`, deliberately indistinguishable from an empty vault."* That is
disclosure control (FR-053, MV-12), not an accuracy claim: the caller is being told
the truth about the corpus they are permitted to see. The unbuilt-index case has no
such justification — it is a capability failure being reported as a fact about the
world.

**Cost, and why this is the cheapest of the four rulings.** `knowledge.Index` already
exposes `DocCount()` (`pkg/knowledge/index.go`). `knowledge_search` already implements
the correct behaviour — it stats the manifest and returns `index_state: "not_built"`,
`incomplete: true`, *"Saying '0 results' here would be a confidently incomplete answer
(US-6, P0)"* (`pkg/knowledge/tools.go::(*SearchTool).Execute`). The fix is to give
`knowledgefind.TextSearcher` a population accessor, have
`vaultprops/find_tool.go::findTextSearcher` implement it over `Index.DocCount`, and
gate `zeroHitResponse` on it.

**Wire contract: NO CHANGE.** `TextSearcher` and `Deps` are internal Go interfaces.
`index_unavailable` is already in the `RecordProblem.code` enum. The refusal shape
(`refused: true`, remedy in `problems`) is already specified and already used by the
typed path.

**Adjacent instances to check while in there** (flagged, not asserted — I did not
confirm these produce a wrong answer):
`nearReachable` returns `map[string]bool{}` when the `near` seed does not resolve,
which reaches the same `zeroHitResponse`. "I could not find the anchor note" and
"nothing is near it" are different facts. Same test to apply.

---

### R2 — What decides which collections get indexed? · covers **F-3**, **F-6**

> **RULE.** There is **one** answer to "which collections exist", and it is
> `knowledge.ResolveScope`. The indexer consumes that answer; it does not compute a
> second one. A collection that any tool can address MUST be indexable, and there MUST
> be a named, callable action that indexes it.

**The argument.** Today the mount store is the indexer's world model and the filesystem
walk is the tools' world model, and they were never reconciled. The consequence is not
a stale index — it is a **state the product has no exit from**: permanently visible,
permanently unindexed, with no agent tool, REST endpoint or CLI verb that can change
it. `vaultprops/sync.go` already anticipates the hole in a comment referring to a
*"future standalone re-index command"* that was never written.

Two candidate fixes, and I am rejecting one of them:

*Rejected — "register work-tree collections as mounts."* This is the smaller diff and
it is wrong. It requires punching a hole in
`pkg/workspace/mount.go::CheckMountTarget`, whose refusal of any target inside
`$OMNIPUS_HOME` is a security control with a stated rationale (*"mounting it would make
config.json and master.key writable and let an agent disable its own sandbox"*, FR-7.5,
ADR-063 D6). Weakening a sandbox boundary to fix an indexing bug trades a correctness
defect for a security one. It also entrenches the mount store as the index's world
model, which is the actual defect.

*Adopted — invert the dependency.* `KnowledgeLifecycle` attaches what **scope** returns,
enumerated per workspace, rather than what the mount store returns. Mounts remain the
*grant* mechanism — `workspace.AllowedMountRoots` stays the sole security-reviewed
accessor and nothing here widens it. Only the *enumeration* changes: from "every mount
record" to "every collection those grants plus the work tree actually resolve to",
which is a strict subset of what the tools can already read. `AttachMount`'s per-mount
key becomes a per-collection key; the existing collection-identity dedup
(`FR-031`: one folder, one index) already handles a collection reachable two ways.

**Then close the diagnostic hole, which is a separate bug in the same class as R1.**
`knowledge/manifest.go::LoadManifest` returns `(NewManifest(root), nil)` when the file
is absent. `knowledge/tools.go` reads that as `ManifestKnown = true, ManifestCount = 0`,
so `knowledge_describe.go::indexFreshness`' `!d.ManifestKnown` branch — the one that
prints *"NOT INDEXED yet"* — is **unreachable in the ordinary case**. The never-indexed
state therefore renders as either *"indexed and empty"* (F-6) or *"index holds 0 notes,
200 on disk — the two disagree; re-index to reconcile"* (F-3), and the second names a
remedy that does not exist. Which of the two you get depends only on whether
`d.NotesCounted` happened to be set. **That is F-3 and F-6 being the same bug**, and it
is why ruling on them once is correct.

The fix: `LoadManifest` must distinguish absent from empty — a typed
`ErrManifestNotBuilt`, or a second return value. The correct wording already exists in
`vaultprops/reader.go::errIndexNotBuilt` and simply never reaches the headline.

**Cost.** This is the largest item and the only one whose blast radius reaches gateway
boot ordering, the `knowledge_index_progress` WS frame lifecycle, and `RevokeMount`'s
reference counting. It should be specced before it is implemented.

**Wire contract: NO CHANGE required.** The `knowledge_index_progress` frame already
carries per-collection progress and a work-tree collection produces one exactly like a
mounted one. If a re-index verb is later exposed over REST (see §4) that *is* a
contract change, and it is deliberately out of scope here.

**One consequence to accept explicitly.** Once work-tree collections are indexed, a
copied-in vault starts consuming index storage and drift-check cycles the operator did
not ask for. That is correct — it is the price of the tools being able to see it — but
it means the KB settings surface needs to show work-tree collections alongside mounted
ones, or operators will find index directories they cannot account for.

---

### R3 — How is a collection addressed? · covers **F-1**

> **RULE, in three parts.**
> **(a)** Every tool that resolves a collection accepts the same `collection` argument.
> `knowledge_find` is the only one that does not, and that is a defect, not a design.
> **(b)** Arguments are decoded and validated **before** scope is resolved. A tool must
> never refuse for a reason the caller has no argument to address.
> **(c)** A refusal that names alternatives MUST name them in a form the caller can
> paste back verbatim. An identifier the caller cannot construct is not an identifier.

**(b) first, because it is the whole of the outage.** `(*FindTool).Execute` calls
`scope.Select("")` on its first line, before `json.Marshal(args)` and before
`knowledgefind.Call` ever sees the arguments. So a caller passing `collection=...`
receives *"no single knowledge base is unambiguously in scope"* rather than
`knowledgefind/tool.go`'s existing, actionable *"`collection` is not an argument of
knowledge_find; accepted: words, type, kind, …"*. That second message would have ended
the loop in one call. **Reversing two steps turns a 24-call timeout into one useful
refusal**, and it is a one-line change with no contract impact. It should land first,
independently of everything else in this section, because it makes every remaining
defect diagnosable instead of indistinguishable.

**(c) is the generalisable rule, and the finding's "related naming defect" is real.**
`knowledge.Scope.Select` matches, in order: display name (folded), `Origin`, then
absolute real path. It does **not** match the folder basename — which is the one token
present in every path an agent ever sees. So a collection carrying a `vault.json` is
addressable only by a string that appears nowhere in the agent's world. Worse, `Origin`
is not a unique handle either: `ResolveScope` sets it to the literal constant
`WorkTreeOrigin` (`"workspace"`) for **every** work-tree collection, so once R2 lands
and work-tree collections become ordinary, two of them collide on it.

Note what is already true and undocumented: **`Select` already accepts the collection's
absolute root path**, which an agent holding `/…/work/corpus/note.md` can trivially
construct. The addressing was round-trippable all along; nothing told the caller. That
is exactly what rule (c) is about.

**The addressing model, decided:**

1. **Primary handle: the display name.** Unchanged. It is what the operator sees.
2. **Add the folder basename as a lowest-priority match**, after name, origin and path.
   When two in-scope collections share a basename, refuse for ambiguity and list both
   with their canonical ids — never pick one.
3. **Canonical identifier: `kb_<sha256(realpath)[:16]>`, which already exists.**
   `pkg/gateway/rest_knowledge.go::knowledgeCollectionID` mints it for the Library UI
   today. Adopt it on the tool surface as the unambiguous tiebreak; do not invent a
   second scheme. `describe` prints it; every tool accepts it.
4. **Every refusal and every "in scope:" list prints the paste-back-able set** — display
   name, canonical id, and root path per collection — rather than today's bare
   `strings.Join(scope.Names(), ", ")` (`vaultprops/find_tool.go::joinFindScopeNames`).

**(a) `knowledge_find` gains `collection`. THIS IS A WIRE CONTRACT CHANGE.** Files, and
they must land in one commit per Constraint #8:

- `contracts/openapi.yaml` — the `VaultFindRequest` schema, which is **hosted inline**
  in that file (not in `contracts/components/schemas/`) because it references the
  recursive `VaultFilterNode` by internal ref. Do not create a new schema file for it;
  the inline hosting is deliberate and documented at the site.
- `pkg/api/generated/` and `src/lib/api/generated/` — regenerated via
  `scripts/gen-contracts.sh`, committed in the same commit.
- `pkg/records/knowledgefind/tool.go` — `AcceptedParameters` **and** `Parameters()`.
  Note these are a hand-maintained second copy of the contract; adding a parameter to
  one and not the other reintroduces exactly the silent-drop failure the
  `UnsupportedParameter` check exists to catch.

**This is not a scope relaxation, and the ADR-068 text must not be read as forbidding
it.** `VaultFindRequest`'s description says *"Scope is not negotiable by the caller."*
That remains true: `collection` selects **within** the already-resolved scope, exactly
as `knowledge_search`, `knowledge_describe` and `knowledge_read` have always done. A
name that matches nothing in scope is indistinguishable from a name that exists
nowhere, per FR-053/MV-12.

**One sub-case to get right.** When `collection` is supplied and does not resolve, the
answer is a **refusal listing only in-scope collections** — not R1's complete-zero and
not the FR-053 empty set. Listing names the caller could already enumerate discloses
nothing, and asserts nothing about whether the named collection exists elsewhere. The
existing scope-gate refusal in `find_tool.go` already has this shape; it is the
precedent, not an exception to it.

**Testing note, from the finding and worth keeping.** `pkg/vaultprops/find_tool.go` has
no dedicated test file. A tool that is uncallable in a two-collection workspace shipped
because nothing ever constructed a two-collection workspace. Whatever else changes
here, that fixture must exist.

---

### R4 — What does a platform do when it cannot measure? · covers **F-2**

Two separate questions were asked. They get two separate answers, and one of them is
"no".

> **RULE 4a — measurement.** A probe that could not measure MUST return *unknown*,
> never a number. A caller receiving *unknown* MUST fall back to a **named, documented
> policy default**, log that it did so once, and **report the value's provenance
> wherever it reports the value**. It is never acceptable to publish a fabricated
> number through an API field whose documentation says it was measured.

`pkg/config/meminfo_other.go::readMemAvailableBytes` returns literal `0` on every
non-Linux platform. `autoDetectMaxParallel` computes `0 / 3.5 MiB` and
`clampParallel` floors it at 2. That 2 is then published as
`effective_max_parallel_agents`, whose schema
(`contracts/components/schemas/PerformanceSettings.yaml`) says it is *"the resolved
value actually in use (after applying the auto-detect memory-based heuristic…)"*. No
memory heuristic ran. **The wire documentation is a false statement about how the
number was produced**, on every Mac and every Windows box.

The file's own comment is candid about this — it explains that the previous non-Linux
behaviour fabricated ~2 GiB and produced a cap of 585 everywhere, and that returning 0
was chosen as the conservative direction. That reasoning is right about *direction* and
wrong about *kind*: 0 is as invented as 2 GiB. It just fails in the safer direction,
which is why it survived. The file also notes that neither it nor its test is ever
compiled by this project's Linux-only CI — so the bug is invisible by construction, and
will stay invisible unless CI cross-compiles and runs these paths on a non-Linux
runner. **That gap is part of the fix, not a footnote to it.**

**Two things to do, in this order:**

1. **Get a real signal. It is pure Go and adds no dependency.** `golang.org/x/sys` is
   already a direct module requirement (`go.mod`) and `golang.org/x/sys/windows` is
   already imported by `pkg/logger/panic_win.go` and
   `pkg/sandbox/hardened_exec_windows.go`. Darwin/BSD: `unix.SysctlUint64("hw.memsize")`.
   Windows: `windows.GlobalMemoryStatusEx`. No CGo, no new runtime dependency, single
   binary intact — Constraints 1 and 2 are satisfied, and the finding's suggestion is
   sound. **One caveat the finding does not make:** `hw.memsize` is *total physical*,
   not *available*. Linux feeds `MemAvailable`. Feeding total where the formula expects
   available overstates by a large factor on a loaded machine, so either derive an
   availability figure (`vm.page_free_count` × page size, plus the compressor's
   accounting) or **change the divisor's meaning and say so at the call site**. Do not
   quietly substitute one for the other — that is this document's own §1 mistake in a
   new place.
2. **Make "unknown" expressible even after (1).** A probe can still fail. Change
   `readMemAvailableBytes() uint64` to `(uint64, bool)` so the absent case has a
   representation, and have `autoDetectMaxParallel` branch on it to a named constant
   with a single WARN.

**Wire contract: CHANGE, small.** `contracts/components/schemas/PerformanceSettings.yaml`
gains a provenance field on `effective_max_parallel_agents` — an enum along the lines of
`measured | policy_default | explicit_config | env_override` — and its prose stops
claiming a memory heuristic ran when it did not. Regenerate
`pkg/api/generated/` + `src/lib/api/generated/` in the same commit. The Settings screen
should render "default (memory not measurable on this platform)" rather than a bare
number, so an operator can see the difference between a machine that was sized and one
that was guessed at.

> **RULE 4b — should an abandoned turn die?** **No.** ADR-045's default stands.

The finding presents the orphan watchdog being armed with `graceSeconds: 0` as a defect
— *"fully built, tested, and dead by default"*. It is not a defect; it is a **decision**,
recorded in `pkg/config/config.go::DefaultOrphanedTurnGraceSeconds`:

> *"Omnipus is built to run turns as background work: closing a chat tab must NOT cancel
> an in-progress turn… Auto-canceling on tab-close (the original ADR-045 5-minute
> default) contradicted that model and was reversed per operator decision."*

That decision is right and I am not reopening it. Nor were the UAT's turns orphaned in
the sense the watchdog exists to catch: they were making 24 tool calls while a *test
harness* timed out client-side. A reaper would have killed live, progressing work.

**The real defect F-2 found is not liveness. It is that a scarce resource was made
scarce by a fabricated number.** With `max_parallel_agents` correctly sized, a long turn
is an annoyance; with a cap of 2 it is an outage. Fix 4a and the failure mode described
in F-2 stops being reachable. Note the corroborating evidence in the finding itself: the
operator set the value explicitly to 32 and the instance recovered with **zero code
changes**.

**Also do not add a default per-turn wall clock.** `pkg/config/defaults.go` sets
`TimeoutSeconds: 0` with the comment *"disabled; OpenRouter queue delays make fixed
timeouts unreliable"* — that trade-off was already made deliberately, and a wall clock
cannot distinguish a stuck turn from a queued one. `MaxToolIterations` already defaults
to 200 and is the correct bound for a runaway loop; it did not fire here because the
loop was ended by the client, not by the agent.

**Two cheap, policy-free hardening items from F-2 that are worth taking:**

- `pkg/gateway/websocket.go::sendRawFrameBytes` — the non-critical send path walks a
  `{0, 10ms, 50ms}` backoff and never selects on `wc.doneCh`, which **already exists**
  on `wsConn` and is already closed by `(*wsConn).close()`. Adding `case <-wc.doneCh:`
  to those two selects removes ~60 ms of pointless sleep per frame after a client is
  gone. No policy question, no contract, no behaviour change while connected.
- The `openFindStore` best-effort `(nil, nil)` return (§1's fourth row) should
  distinguish "never built" from "could not be opened", so R1's refusal can say which.

---

## 3. Dependency-ordered plan

Severities: **P0** = a release blocker, in the sense the repo uses it — the product
states something false, or enters a state it cannot leave. **P1** = a legitimate
configuration in which a tool does not work. **P2** = degrades an agent's ability to
finish a task it could otherwise finish.

| # | item | ruling | severity | depends on | contract |
|---|---|---|---|---|---|
| 1 | Decode `knowledge_find` arguments **before** the scope gate | R3(b) | **P1** | — | no |
| 2 | Real memory probe on darwin/windows + `(uint64, bool)` unknown | R4a | **P1** | — | no |
| 3 | `find` refuses over an unbuilt/empty-population text index | R1 | **P0** | — | no |
| 4 | `LoadManifest` distinguishes absent from empty; `describe` says "NOT INDEXED" | R2 | **P0** | — | no |
| 5 | `PerformanceSettings` provenance field + honest prose | R4a | **P1** | 2 | **yes** |
| 6 | Indexer enumerates from `ResolveScope`, not the mount store | R2 | **P0** | 4 | no |
| 7 | Paste-back-able refusals; basename match; `kb_…` on the tool surface | R3(c) | **P1** | 6 | no |
| 8 | `collection` argument on `knowledge_find` | R3(a) | **P1** | 7 | **yes** |
| 9 | `system_overload` frame on the capacity path | §4, F-8 | **P2** | — | no |
| 10 | `sendRawFrameBytes` selects on `doneCh` | R4 | **P2** | — | no |
| 11 | `configure` cascade: re-derivable full list in `next` | §4, F-5 | **P2** | 3, 8 | no |

**Why this order.**

Items **1–4 are independent of each other and of everything else**, and 1–3 should be
done first regardless of what else happens, because *they are what make the rest
testable*. Item 2 in particular: on a Mac the whole UAT is unrunnable without an
explicit `max_parallel_agents`, so every later change is being validated on an instance
that is one long turn away from refusing work.

Item **3 must land before item 6**, not after. Until the indexer is fixed there will be
unindexed collections in the wild, and item 3 is the only thing standing between an
unindexed collection and a confident false zero. Shipping 6 first and 3 later would
narrow the window; shipping 3 first closes it for every collection that is still
unindexed for any reason, including reasons 6 does not fix.

Item **4 before 6** for the same reason: while 6 is in flight, the diagnostic is what
tells an operator which collections are still dark. Today it tells them the opposite.

Items **7 and 8 after 6** because the set of addressable collections is only stable once
scope and the index agree; defining the canonical identifier against a set that is about
to change means doing it twice.

**Release call.** Items 3, 4 and 6 are genuinely blocking: 3 and 4 because the product
makes false statements, 6 because it has a state with no exit. Items 1, 2, 5, 7 and 8
are P1 — the product is wrong-but-honest, and 2 has a zero-code operator workaround. I
would ship a release with 1–4 done and 6 in flight only if the release notes say which
collections can be indexed; I would not ship with 3 or 4 open.

---

## 4. The two remaining findings, ruled briefly

**F-8 — a capacity refusal is not flagged as a failed turn. The finding's framing is
wrong; the underlying complaint is right.** `turn_failed` is behaving to contract:
`contracts/components/schemas/DoneStats.yaml` defines it as *"True when the turn ended
via the engine's error/limit fallback rather than a real model response"* and lists
exactly three triggers. A capacity rejection never starts a turn, so `false` is correct
and **`turn_failed` must not be widened** — it is deliberately coarse and overloading it
would make it mean "something went wrong somewhere", which is not branchable.

The actual defect is that `pkg/agent/loop.go` delivers the rejection as an ordinary
assistant message (`bus.PublishOutbound` with *"I'm at capacity right now"*), so it is
indistinguishable from a real answer to any consumer. **The frame that should carry this
already exists and has zero producers**: `SystemOverloadFrame` / `system_overload` is
defined in `contracts/asyncapi.yaml`, generated into `pkg/api/generated/`, contract-
tested, present in `pkg/gateway/inboundschemas/`, and emitted by nothing —
`pkg/gateway/websocket.go` records the search that established this. Emit it. **No
contract change; the contract is already there and unused.** P2, because a human reading
the chat sees the message and understands it — this is an automation-honesty defect, not
a user-facing one.

**F-5 — `knowledge_configure` truncates its cascade at 10.** Confirmed:
`pkg/knowledge/knowledge_configure.go` caps `Examples` at `cascadeExampleCap` and prints
*"... and N more"* with no paging argument. **Do not raise the cap** — an unbounded list
inside a tool result is a context-budget problem, and the tool was right to bound it.
The defect is that the truncated remainder is **not re-derivable**: there is no query the
agent can issue to get the other eight. So this is not really a `configure` defect; it is
a `find` defect surfacing through `configure`. Fix it by having the cascade's `next`
action name a concrete `knowledge_find` call that returns the full set — which is only
possible once items 3 and 8 make such a call reliable and addressable. Hence its position
in the plan.

---

## 5. Judged NOT worth doing

**A repeated-identical-tool-call detector (F-7).** F-7 is real — `knowledge_find` was
called 20 times consecutively with the same 1 ms refusal and nothing noticed — but a
detector is the wrong fix and an actively risky one. This product has legitimate
high-frequency identical calls: `bash`'s background status polls and `delegate`'s
`status` polls are both enumerated in `src/lib/toolVisibility.ts` precisely *because*
they repeat. A guard tuned to catch F-7 will eventually cancel a legitimate poll, and it
will do so intermittently, which is the worst possible failure mode for a safety
mechanism. The repetition here was a *symptom* of R3(b): the agent looped because the
refusal it received was unactionable and the actionable one was unreachable. Fix the
refusal (item 1, one line) and the loop does not start. `MaxToolIterations: 200` remains
the backstop for a genuine runaway.

**Reviving the ADR-045 orphan watchdog by default.** Ruled on in R4b. It is a recorded
operator decision that turns survive tab close, and the UAT's turns were not orphaned.

**A default per-turn wall clock.** `TimeoutSeconds: 0` carries an explicit rationale
about provider queue delays. A fixed deadline cannot tell a stuck turn from a queued one,
so it would convert an occasional hang into an occasional false kill. Reconsider only if
someone produces a *progress*-based signal that is not confounded by provider latency;
elapsed wall time is not one.

**Registering work-tree collections as mounts.** Ruled out in R2 — it requires weakening
`CheckMountTarget`'s `$OMNIPUS_HOME` refusal, which is a sandbox boundary with a stated
rationale (FR-7.5, ADR-063 D6). Never trade a security control for an indexing fix.

**A "did you mean" for collection names.** The write-path design already downgraded the
equivalent improvement for *property* names to a nice-to-have after D-04 showed the
agent recovers from a complete list unaided. The same logic applies here and more
strongly: R3(c)'s paste-back-able list makes the correct string literally present in the
refusal, at which point fuzzy matching adds nothing. Skip it.

**Anything touching the retired surfaces.** No proposal here revives Command Center, the
Schedules UI, `src/components/command-center/`, raw cron entry, or the JPEG screencast
path. Item 9 uses an existing AsyncAPI frame and adds no UI.

---

## 6. Open questions for the operator

1. **Should a work-tree collection be indexed automatically, or on an explicit action?**
   R2 assumes automatic (matching mounts). Automatic means a copied-in folder starts
   consuming index storage unannounced. An explicit "index this collection" action is
   more honest but needs a REST endpoint — which *is* a contract change and is
   deliberately excluded from R2's scope. **My recommendation: automatic, with the KB
   settings surface listing work-tree collections so the storage is accountable.**
2. **`hw.memsize` (total) versus a derived availability figure on darwin.** R4a flags
   this as a decision rather than making it, because substituting total for available
   silently is the same class of error this document exists to name.
3. **Does CI need a non-Linux runner?** `meminfo_other.go` documents that neither it nor
   its test is ever executed here. Fixing the probe without fixing that leaves the next
   regression equally invisible.

---

## 7. Operator rulings, 2026-09-01

The three open questions in §6 are closed, and two of the rulings change the plan.

### O1 · Automatic indexing — YES, and it must cover more than notes

Work-tree collections index automatically, per §6's recommendation.

**Extension the plan did not anticipate:** a vault folder holds more than notes.
**PDFs and similar documents that live in vault folders must be indexed too.**

The ruling is deliberately narrow, and the narrowness is the point:

- **Full-text extraction from PDFs is NOT required.** Do not run document text
  through the index.
- **Title and whatever metadata is available ARE required**, so the file is
  findable and can be referred to.

**What this means for the work.** `pkg/docextract` already handles `.pdf`,
`.docx`, `.pptx`, `.xlsx` (`Extract`, `IsExtractable`), so the capability
exists — but this ruling means we should NOT call `Extract` at index time. The
cheap path is what is wanted: enumerate the file, take its title from the
filename and any metadata cheaply available, and index that. Running extraction
over every PDF in a vault would be the expensive answer to a question nobody
asked, and would make indexing latency depend on document size.

This changes item 6's scope: the walker admits a document set, not just `*.md`.
It does **not** change items 3 or 4.

### O2 · macOS memory — ALREADY RULED, elsewhere. Do not reinvent it.

**R4a's open question is closed by a decision that already exists.**
`ADR-072-workspace-scoped-browser-sessions.md` §D1.5b on
`feat/browser-streaming-performance`, dated the same day as this document, rules
on exactly this and rules better than R4a did:

- writing the macOS reader is **in scope**; it must work on macOS and Linux;
  **Windows is foreseen but explicitly NOT in scope** and keeps returning 0,
  therefore has no limit — to be stated in release notes, not discovered;
- it is buildable under Hard Constraint #2: `golang.org/x/sys` is already a
  direct dependency and provides `SysctlUint32`/`SysctlUint64`/`SysctlRaw` for
  Darwin in pure Go. No CGo, no new dependency;
- **the formula is `hw.memsize` + `hw.pagesize` + the `vm.page_*` counters**,
  contending with memory compression and purgeable pages — which is the precise
  answer to R4a's worry that substituting total for available would repeat this
  document's own §1 mistake;
- it must be documented at the call site and described as *a considered
  approximation of the same idea*, never as the same measurement.

D1.5b also independently identifies the same defect this UAT hit, from the
browser side: `availableRAMBytes` is 0 on macOS, so every macOS install floors
at 2.

**Consequence for this plan: item 2 is NOT ours.** It is cited, not restated,
and no second reader is written on this branch. `omnipus2-7d` has been messaged
to confirm ownership and timing. Item 5 (the provenance field) still stands and
is still ours, because it is about not *presenting* an invented number as
measured — which is true regardless of who writes the reader.

**Interim, zero code:** operators on macOS set `performance.max_parallel_agents`
explicitly. Verified on the UAT instance: 2 → 32.

### O3 · CI — the Mac runner already exists; keep normal CI on Linux

GitHub CI already has a macOS runner; this is not new capability to build.

**Ruling: normal CI continues to run on the Linux runner. macOS-specific
verification runs locally on the founder's machine.**

So §6's third question does not become a CI redesign. What it does mean is that
`meminfo_other.go`'s own note — that neither it nor its test is executed by this
pipeline — stays true for the default path, and any macOS-only change must be
verified locally and said to have been. That is a discipline, not a pipeline.

### O4 · Everything ships — but the solution is aligned before it is built

No item is dropped. P2s are not deferred out of the release.

### O5 · Build after alignment. The core feature does not work today.

Stated plainly by the operator: this cannot be delivered as it stands, because
the central retrieval surface either cannot be called, cannot be indexed, or
answers a confident false zero. Alignment first, then build — which is why this
document is being agreed rather than executed from.

---

## 8. A fifth instance of §1's pattern, and the worst-behaved one

Contributed by the session working on `feat/browser-streaming-performance`, and
**verified here before being recorded**: `pkg/config/meminfo_linux.go`.

`readMemTotalBytes` returns a hardcoded `fallbackTotalRAMBytes` — 4 GB — when
`/proc/meminfo` cannot be read or parsed, and its signature is a bare `uint64`.
There is **no `ok`, no error, no signal of any kind.** A caller cannot
distinguish "this host has 4 GB" from "I could not look, so I assumed 4 GB".
The same shape applies to the derived available figure.

**This is worse than the four instances in §1, and the difference is worth
stating.** Those encode absence as *zero* — an implausible value that a careful
caller might question, and which at least degrades toward a floor. This one
encodes absence as a **plausible number carried by a success path**. It is
unquestionable by construction: nothing downstream has any way to doubt it. On
gVisor, where `/proc/meminfo` is not readable, the product computes its
concurrency limit from a figure it invented and reports as fact.

**Two consequences for this plan:**

1. §1's cross-cutting claim gets stronger, not weaker. Five instances across
   five files is not a coincidence of four; it is a house style — no type in
   this area could say *"I did not look"*, so every one of them said something
   else instead.
2. **ADR-072 D1.5e's deletion of `fallbackTotalRAMBytes` is not cleanup on the
   way to the darwin reader — it removes an active fabrication.** Worth
   sequencing on its own merit rather than as a side effect.

Not ours to fix: `pkg/config` belongs to ADR-072's items. Recorded here because
it is the same defect this document exists to name, and because anything on this
branch that reasons about host memory on Linux will receive a confident wrong
answer rather than an error.
