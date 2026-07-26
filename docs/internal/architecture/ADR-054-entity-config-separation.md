# ADR-054: Entity / config separation — per-entity files for agents

- **Status:** **Accepted (v3)** — after two adversarial reviews (v1: REVISE/4
  CRITICAL; v2: REVISE/3 CRITICAL). The three residual questions are decided in
  §0 below; all three are reversible and scoped conservatively.
- **Date:** 2026-07-25
- **Deciders:** Operator (Daniel Piatkowski); Albert (architecture)
- **Supersedes:** Nothing. This is a new decision.
- **Precedent followed:** ADR-035 / ADR-037 established *no back-compat, no
  migration* as an acceptable posture for pre-1.0 data-model changes. The
  operator has locked the same posture here.
- **Evidence level (highest used):** 1 (operator-locked constraints) +
  codebase `[FACT]` grounding gathered 2026-07-25.

---

## 0. The three residual decisions (closing v3)

**R1 — SCOPE: agents only. `channels` and `providers` stay in `config.json`.**
The operator's requirement is *"every agent should be its own file"*. `providers`
has **no identity field** (`ModelConfig` has only the renameable `ModelName`,
`config.go:2409-2450`) so `providers/<id>.json` is not implementable without
inventing one; `channels` is a map, operator-written and cold. Neither is a hot
parallel-write path — agents are. **Narrowing to agents removes two unimplementable
sub-designs and shrinks the blast radius of a no-migration change.** If either
later becomes hot, it follows the same pattern. Confidence: **High**.

**R2 — `bindings`, `mailboxes`, `channel_policies` remain SETTINGS.**
All three are keyed-by-agent *maps*, not independently-addressable records with
their own lifecycle — they describe how the system routes to and provisions for
agents. They are the agent foreign-key surface D6 governs (a dangling agent id in
any of them is a dangling reference, repaired per D6 rules 1-3/5), but they are
not themselves entities. `mailboxes` additionally carries `password_ref`
credential keys, so moving it out of `Config` would silently shrink the
`collectSensitive` redaction walk (`security.go:110-167`) — an argument to leave
it in place. Confidence: **High**.

**R3 — an unparseable DEFAULT agent record degrades, it does not black-hole
traffic.** D7's fail-closed rule applies to a **binding naming a skipped agent**,
because re-routing that to Mia is a *privilege change*. The global default is a
different case: it is an availability floor, not a privilege grant. If the record
named by `default_agent_id` is unparseable, resolution continues down the existing
ladder (`registry.go:241-280`) and the system is marked **degraded** in `/health`
with an ERROR log. Rationale: fail-closed here means one corrupt file drops ALL
inbound traffic with no in-product repair — strictly worse than routing to a
working agent and saying so loudly. Confidence: **Medium** — this is an
availability-vs-strictness call and is the one an operator may wish to invert;
it is a single branch to flip.

---

## 1. Context

Omnipus stores all configuration in a single `config.json`. The agent roster
dominates it, but **the ratio is install-dependent and v1/v2 cited one install
without naming it**. Measured 2026-07-25, compact-JSON bytes `[FACT: both re-measured]`:

| Install | total | `agents` | `agents.list` (what moves) |
|---|---|---|---|
| `/tmp/omnipus-preview-home` (8 agents) | 61,051 | 31,050 (51%) | 30,454 |
| `~/.omnipus` (8 agents) | 17,856 | 5,969 (33%) | 5,338 |

The earlier "41 KB / 31 KB / 75%" figure was accurate for the preview install *at
the moment it was taken* and is no longer even true of that file. **The argument
does not rest on the ratio**: what matters is that creating one agent rewrites
the ENTIRE file (17.8–61 KB) instead of writing one ~0.6–1.3 KB record.

Every mutation of any config key — creating an agent, flipping a sandbox
setting, adding a channel — takes one global write lock (`al.mu`) and rewrites
the **entire file** via `WriteFileAtomic` (`config.go:3567`, `:3599`) `[FACT]`.

The in-process path is correct. `Deps.WithConfig`
(`pkg/sysagent/tools/deps.go:239`) holds `al.mu` across mutate-and-persist,
snapshots for rollback, and fails loudly if `SaveConfigLocked` is unwired
`[FACT]`. This ADR does **not** claim that path is buggy.

Three structural problems remain:

1. **Global serialization.** N agents concurrently creating sub-agents queue on
   one lock — not because they conflict, but because they share a file. The
   operator expects *massive* parallelism as agent fan-out grows.
2. **Write amplification.** One agent added = the whole file rewritten
   (17.8-61 KB depending on install).
3. **No cross-process protection.** `al.mu` is in-process only. Nothing stops
   the CLI, a second gateway, or an external tool from clobbering `config.json`.
   This is **not hypothetical** — it was demonstrated accidentally during this
   session by a `jq` read-modify-write against a live gateway `[FACT]`.

Meanwhile `pkg/task`, `pkg/plan`, `pkg/session` and `pkg/workspace` already use
per-entity files `[FACT]`. **Agents are the outlier**, and they are the hottest
and largest structure.

A related integrity failure was observed: a workspace `core_team` referenced
three agents that do not exist, and `validateCoreTeamMembers`
(`rest_workspaces.go:1382`) then rejected *every* subsequent write naming the
first missing one — **permanently wedging the workspace with no repair path**
`[FACT]`.

## 2. Decision drivers

- Support many concurrent writers without lock convoy (operator, locked)
- Entities must be structurally separated from settings (operator, locked)
- Every agent its own file (operator, locked)
- No back-compat, no migrations (operator, locked)
- Single Go binary, pure Go, file-based JSON/JSONL only (CLAUDE.md constraints)
- Contract-first wire formats (Constraint #8)
- Reuse a proven in-repo pattern rather than inventing one

## 3. The entity / settings boundary — D1

**Decision.** An *entity* is an independently-addressable record with an
identity, created and mutated at runtime, potentially in parallel. A *setting*
is operator-tuned scalar/struct state that describes system behaviour.

| Key | Classification | Rationale |
|---|---|---|
| `agents.list` | **Entity** → per-file | Identity, runtime-created, hot, parallel `[FACT: 31 KB]` |
| `channels[]` | **Entity** → per-file | Identity, but operator-written (cold) |
| `providers[]` | **Entity** → per-file | Identity, operator-written (cold) |
| `agents.defaults` | Setting | Describes behaviour, no identity |
| `tools`, `sandbox`, `gateway`, `session`, `performance`, `planning`, `storage`, `voice`, `session_messaging`, `schedules`, `devices`, `hooks`, `build_info`, `version` | Setting | No identity `[FACT: shapes inspected]` |

`schedules` is a **setting** here (`{max_concurrent_runs, retry_backoff_ms,
run_timeout_seconds}`) — the *scheduled jobs* themselves already live in
`workspace/cron/jobs.json`, separate from config `[FACT]`.

**Confidence: High.** Basis: every key's actual shape was inspected on a live
install. Missing evidence: none material.

## 4. On-disk layout — D2 (REVISED — v1 was a security defect)

**Decision.** Entity records live OUTSIDE any agent-writable tree:

```
$OMNIPUS_HOME/entities/agents/<id>.json
$OMNIPUS_HOME/entities/channels/<id>.json
```

**v1 proposed `agents/<id>/agent.json` (co-located with SOUL.md). That was wrong
and is withdrawn.** `agents/<id>/` is `AgentConfig.Home` — the agent's own
WorkDir when a turn has no bound workspace (`pkg/fspolicy/policy.go:167-170`).
`IsCarveOut`'s own-tree exception *deliberately* exempts an agent's own home from
the `agents/` carve-out (`pkg/fspolicy/carveout.go:41-49`) `[FACT: verified]`, and
the metadata guard is a **closed four-name whitelist** — soul/heartbeat/memory/
agent (`pkg/tools/metadata_guard.go:32-37`) `[FACT: verified]`, which `agent.json`
is not in.

`AgentConfig` carries `Tools` (the Constraint #6 policy map), `ShellPolicy`,
`Locked`, `Default`, and `Home` (`config.go:776-838`). Co-location would let any
agent with `write_file` rewrite its own security record — grant itself every
tool, clear `locked`, set `default:true` to hijack routing — **at
`FSScopeConfined`, the most restrictive setting.** That collapses "may write
files" into "may administer itself". The extra nesting is far cheaper than the
three new security controls co-location would require.

**MANDATORY — `entities` must be added to the carve-out list.** `buildCarveOuts`
(`pkg/fspolicy/carveout.go:16-23`) returns exactly four roots: `master.key`,
`credentials.json`, `agents`, `workspaces` `[FACT: verified]`. **`entities` is not
among them.** Without adding it, under `FSScopeUnrestricted` an agent can write
EVERY agent's record — strictly worse than the v1 layout this replaced, which at
least had the `agents/` carve-out shielding other agents. `RestrictToWorkspace`
defaults true (`config/defaults.go:32`) so this is an opt-out posture, not the
default — but D2's claim is unqualified and must not be.

**RESIDUAL — `bash` bypasses this layer entirely.** Tool-exec children get
`DefaultPolicy` = RWX on all of `$OMNIPUS_HOME` (`pkg/sandbox/sandbox.go:313-320`);
the narrowed `DefaultChildPolicy` is documented "**NOT yet active**"
(`sandbox.go:192`) with zero production callers. `guardCommand`
(`shell.go:633-698`) is a regex over command text and does not match
`$HOME/.omnipus/entities/…`. So for an agent with `bash: allow` (Jim, on a fresh
install) the FS-policy protection above is **not** the last line of defence. This
is not a regression versus v1 — it was equally true there — but D2 must not be
read as closing the escalation hole at every layer. Routes to the
`DefaultChildPolicy` wiring work (v0.3 #156).

**Listing: `os.ReadDir` + explicit `sort.Slice`.** The `pkg/task` precedent sorts
(`store.go:262`) and never trusts directory order `[FACT]`. Order matters here:
`resolveDefaultAgentID`'s "first chat-target agent" (`route.go:356-370`),
`firstChatTargetAgentID` (`rest.go:1046-1057`), and a spec'd CLI contract
(`cmd/omnipus/main.go:53`, "ordering mirrors cfg.Agents.List") all depend on it.
Unsorted ReadDir would flip the built-in roster mia,jim,ava,ray → ava,jim,mia,ray
and demote the starred default to third.

**Ordering contract — sort by `(created_at, id)`. NO persisted `sort_index`.**
A v2 draft proposed `sort_index`; **withdrawn.** Assigning it requires
`max(existing)+1` — a read-all-then-write on the CREATE path, i.e. it
re-serializes precisely the operation this ADR exists to parallelize, and it is
an unowned global invariant, which D6 rule 4 forbids in this same document.
`grep sort_index` → zero hits in repo; `pkg/task/store.go:262` sorts *derived*
fields (`EffectivePriority`, `CreatedAt`), never a persisted index `[FACT]`.
`AgentConfig` has `UpdatedAt` but **no `CreatedAt`** (`config.go:774-838`) — that
field must be added. A monotonic timestamp has no allocation race. If curated
roster order is ever genuinely needed, it belongs in settings
(`agents.defaults.roster_order: [id…]`), which is D6.4's own argument applied
consistently.

**Confidence: High** on the location (security-forced). **High** on sorting
(precedent + three verified dependents).

## 5. Concurrency contract — D3 (REVISED — v1's cross-process claim was false)

**Decision.** Per-entity store with **striped in-process mutex + a sidecar
lockfile**.

**v1 claimed flock gives cross-process safety. That is false and is withdrawn.**
The repo's own comment documents why: *"WithFlock locks the inode rather than the
path, so os.Rename swaps the inode and lets two writers run WriteFileAtomic
concurrently"* (`pkg/fileutil/file.go:62-67`) `[FACT: verified]`. Worse, the read
side is never locked at all — `pkg/task/store.go:117` `load()` is a bare
`os.ReadFile` `[FACT: verified]`, so the load→mutate→write cycle is protected
only in-process. The three packages "already shipping this contract" replicate
the same defect; replication is not validation.

**READ PATH — reads do NOT touch disk (this was unmodelled in v2 and is
load-bearing).** `fileutil.WithFlock` is always `LOCK_EX`
(`flock_unix.go:34-58`) — there is no shared-lock path `[FACT]`. Meanwhile
`pickAgentID`/`resolveDefaultAgentID` scan agents **per inbound message**
(`route.go:298-375`), and today that is `al.GetConfig()` — an RLock and a pointer
return, **zero syscalls** (`loop.go:3976-3980`) `[FACT]`. Naively flocking every
read would take the routing hot path from 0 syscalls/message to ~5N and put a
GLOBAL EXCLUSIVE lock on the hottest record (the default agent) on every message
— which would make this ADR net-negative on the very axis it is justified by.

**Therefore: the entity store is read through an in-memory cache, not the disk.**
`AgentRegistry` (`registry.go:38-99`) already is that cache. Disk reads happen at
load/reload and after a write; the lock protects the *write* path and the initial
load. A write updates the record, then the registry — the registry-invalidation
design is part of THIS decision, not deferred to the spec.

Guarantees, stated honestly:
- **In-process:** two goroutines mutating the *same* entity serialize on its
  stripe. Different entities proceed **fully in parallel**. This is the property
  that delivers the operator's parallelism requirement, and it is real.
- **Cross-process:** requires a **sidecar lockfile at a stable path that is never
  renamed** (`entities/agents/<id>.lock`), flocked across BOTH read and write —
  not the data file, which `WriteFileAtomic` replaces by inode. Plus a
  single-instance gateway pidfile (`O_EXCL`).
- **Sidecar lifecycle — the lockfile is NEVER unlinked, for any reason,
  including entity deletion.** `WithFlock` opens `O_RDWR|O_CREATE`
  (`flock_unix.go:35`), so a deleted-and-recreated lockfile is a different inode
  and two processes can hold "the" lock simultaneously. D6 rule 5's delete
  contract must NOT remove it. The single-instance pidfile must handle staleness
  — do not reinvent it: `pkg/tools/browser/coordinator_lock_other.go:5-11` ships
  the pattern (O_EXCL create + marker/pid-alive staleness check, remove and retry
  once).
- **Never guaranteed:** protection from a non-participating writer. `flock` is
  advisory — `jq`, `sed`, and shell redirection do not take it. The incident that
  motivated this ADR was exactly such a writer, so **no lock design prevents it**;
  only the single-instance pidfile plus a documented "do not hand-edit while
  running" contract mitigate it.
- **Not guaranteed:** cross-entity transactions (see D6).

Platform note: `WithFlock` is a documented **no-op on Windows**
(`pkg/fileutil/flock_windows.go:34-40`) `[FACT]`. Cross-process safety there
rests solely on the single-instance pidfile.

**Confidence: High** on the in-process guarantee. **Medium** on the sidecar
design — it is sound in principle but unimplemented and unbenchmarked here.

### 5.1 Testing-gap closure and Windows posture (qa-lead, 2026-07-26)

A post-implementation review found the cross-process claim above was
**asserted but never tested** — no test anywhere forked a second OS process
— and worse, made three things concrete:

1. In-process, the striped mutex ALONE satisfies every existing test.
   Deleting `fileutil.WithFlock` from the `Store.Update`/`Create`/`Delete`
   call sites did not make
   `TestSameEntityContention_ConcurrentUpdatesSerializeNoLostUpdate`
   (`pkg/entity/store_test.go`) fail — nothing isolated the flock's own
   contribution.
2. The inherited flock suite (`pkg/fileutil/flock_test.go`) is vacuous for
   mutual exclusion: `TestAdvisoryFileLockWrites` only asserts a counter
   total (passes with `WithFlock` as a pure pass-through), and
   `TestConcurrentConfigWrites` only asserts the final file is valid JSON
   (guaranteed by `WriteFileAtomic`'s atomic rename alone, no lock needed).
3. On Windows, `WithFlock` is an unconditional pass-through and `pkg/entity`
   has no single-writer-goroutine (or any other) compensation — unlike the
   general pattern CLAUDE.md's Storage section describes.

**Closed by two new tests, both `!windows`-gated:**

- `pkg/entity/store_crossprocess_test.go` —
  `TestCrossProcess_ConcurrentUpdatesSerializeNoLostUpdate` re-execs the test
  binary as 6 independent OS processes (mirrors the
  `pkg/sandbox/*_subprocess_test.go` env-var-marker + exit-code convention),
  all racing `Store.Update` read-modify-write cycles on one shared entity
  counter, each sleeping between read and write to widen the race window
  (the same technique `TestAdvisoryFileLockWrites` already uses in-process).
  **Proven not vacuous**: with `fileutil.WithFlock` temporarily stubbed out
  of `Store.Update`, this test reliably failed (final counter 15 of an
  expected 90 — most updates lost); restoring the call made it reliably
  pass again.
- `pkg/entity/flock_isolation_test.go` —
  `TestUpdate_HoldsExclusiveFlockOnSidecarDuringCriticalSection` closes
  finding #1 directly: it holds one `Update` call open mid-critical-section
  and, from an independent `os.File` handle on the exact same sidecar path
  (`Store.lockPath`), attempts a non-blocking `flock`. Because flock binds to
  the open file description rather than the process
  (`pkg/tools/browser/coordinator_lock_unix.go` documents and relies on the
  same property), this bypasses the Go-level mutex entirely — a pass can
  only be explained by `Store.Update` genuinely holding the OS lock. Also
  proven not vacuous by the same stub/restore method (the probe acquired the
  lock immediately when the flock was stubbed out; it correctly blocked with
  `EWOULDBLOCK` once restored).

**Windows decision: POSIX-only, documented — not implemented.** Two options
were on the table: (a) implement a real Windows compensation (e.g.
`LockFileEx`, or the `pkg/tools/browser/coordinator_lock_other.go` `O_EXCL`
pattern already used elsewhere in this repo for the identical problem), or
(b) state plainly that the cross-process guarantee is POSIX-only and Windows
relies on the in-process striped mutex alone. **(b) was chosen.** Reasoning:

- No package in this repo actually implements a dedicated single-writer
  goroutine (a channel-fed serialization goroutine) for its Windows path —
  `pkg/task`, `pkg/plan`, `pkg/session`, `pkg/credentials`, `pkg/auth`, and
  `pkg/agentstore` all pair the same in-process mutex with the same
  `fileutil.WithFlock` call as `pkg/entity` does, and none has Windows-
  specific LOCKING code (`grep runtime.GOOS` across all six returns only
  unrelated hits: `pkg/auth/oauth.go`'s browser-opener switch and a
  `pkg/credentials` test permission guard — nothing touching write
  serialization). CLAUDE.md's
  "Windows uses the single-writer goroutine pattern" line describes an
  aspiration, not a shipped mechanism, for the whole file-store family — not
  a regression specific to `pkg/entity`.
- Verified separately: the "single-instance gateway pidfile" this ADR's own
  D3/D4 text leans on for the Windows story is **also not implemented**
  as a hard boot-time gate anywhere in `pkg/gateway`/`cmd/omnipus`. The
  closest thing, `daemon.Status` (`pkg/daemon/daemon.go`), is a soft,
  advisory check used only by the CLI's auto-start convenience path, not a
  gate the gateway's own boot sequence enforces. So the Windows gap is real
  today, not merely hypothetical.
- Implementing a genuine Windows-specific lock is a production-code change
  to `pkg/entity`'s locking logic, which is explicitly out of scope for the
  work that surfaced this gap (test-writing) and belongs with a design
  owner (backend-lead / this ADR's next revision), not folded in silently
  here.
- This documents a limitation honestly rather than asserting an untested,
  unimplemented mechanism. Per hard constraint 4 (graceful degradation),
  falling back to in-process-only protection on a platform without kernel
  primitives is an accepted posture elsewhere in this codebase (Landlock/
  seccomp fall back the same way); making the fallback explicit here is
  consistent with that precedent, not a new exception.

**Follow-up (NOT yet filed as a GitHub issue — this ADR text is currently the only record; see Consequences before treating it as scheduled):** implement a real Windows
cross-process lock for `pkg/entity` — either a `golang.org/x/sys/windows`
`LockFileEx` call behind `fileutil.WithFlock`'s existing signature, or the
`O_EXCL`-based sidecar pattern `coordinator_lock_other.go` already ships —
so the guarantee stops being POSIX-only. Until then, `pkg/entity`'s Go doc
comments (`store.go`, `lock.go`, `entity.go`) and CLAUDE.md's Storage section
state the POSIX-only scope explicitly so no future reader assumes Windows
parity that was never built.

**Confidence: High** — both new tests were verified to fail without the
flock and pass with it (see the qa-lead session transcript), and the "no
package actually has a Windows single-writer goroutine" claim was verified
by direct code search, not inferred.

### 5.1.1 §5.1 itself overclaimed — corrected (qa-lead, 2026-07-26, same-day follow-up)

A second, independent review found that the paragraph above — "Closed by two
new tests, both `!windows`-gated" — was **itself an overclaim**. It reproduced
the problem directly: with `fileutil.WithFlock` stubbed to a pass-through at
**both** the `Create` and `Delete` call sites in `store.go` simultaneously,
the entire pre-existing `pkg/entity` suite (including the two tests §5.1
lists above) still passed. Neither
`TestCrossProcess_ConcurrentUpdatesSerializeNoLostUpdate` nor
`TestUpdate_HoldsExclusiveFlockOnSidecarDuringCriticalSection` exercises
`Create` or `Delete` at all — both are Update-only, despite
`flock_isolation_test.go`'s own doc comment and this section's original text
claiming the gap was closed for "the Update/Create/Delete call sites."
`Create` is the higher-severity miss of the two: it guards the
write-then-verify existence check D6 relies on as the durable fix for this
ADR's motivating failure class (a roster naming an entity that was never
actually persisted) — exactly the call site with no cross-process test at
all until now.

**Closed by two more tests, both in `pkg/entity/store_crossprocess_test.go`,
both `!windows`-gated, both proven non-vacuous the identical stub/restore
way:**

- `TestCrossProcess_CreateRaceOnSameID_ExactlyOneWinner` — races 10
  independent OS processes, barrier-synchronized (a shared ready/go
  marker-file handshake, tighter than relying on `exec.Cmd.Start()` timing
  alone) to call `Store.Create` on the exact same entity ID at effectively
  the same instant. A correctly serialized store permits exactly one winner
  (nil error) and `ErrAlreadyExists` for every other process, with the
  persisted record exactly matching the winner's own payload. **Proven not
  vacuous**: with `fileutil.WithFlock` removed from `Store.Create` only (via
  a `go test -overlay` scratch copy, never the repo), the test reliably
  observed 9–10 of 10 processes reporting a successful `Create` for the same
  ID — not merely "more than one," effectively all of them, since with no
  lock at all every racer's existence check runs unserialized. Restoring the
  call made exactly one winner the only observed outcome across repeated
  runs, including under `-race`.
- `TestCrossProcess_DeleteRacesUpdate_NeverResurrectsDeletedEntity` — races
  five barrier-synchronized `Store.Update` processes (each widening its own
  read-modify-write window with the same in-mutate-sleep technique the
  original Update test uses) against one `Store.Delete` process on the same
  entity ID. `Update` and `Delete` share the same sidecar lock path for a
  given ID, so the only coherent outcome under any scheduling order is that
  the entity is gone once every process finishes — nothing can recreate a
  file after a successful `Delete` in a correctly serialized store. **Proven
  not vacuous**: with `fileutil.WithFlock` removed from `Store.Delete` ONLY
  (`Store.Update`'s own flock call left intact — mirroring how the review
  reproduced the original gap by stubbing `Create`/`Delete` but not
  `Update`), the test reliably observed the entity still existing after the
  race: an in-flight `Update` that had already read pre-delete state
  recreated the file on its write, resurrecting an entity `Delete` had
  already reported as successfully removed. Restoring the call made "gone
  after the race" the only observed outcome across repeated runs, including
  under `-race`.

**Accounting of what is ACTUALLY covered, precisely, so this section cannot
be over-read again:**

| Call site | In-process (goroutines) | Cross-process (POSIX, real forked OS processes) |
|---|---|---|
| `Create` | Covered generically by every `Store` test that calls `Create` | `TestCrossProcess_CreateRaceOnSameID_ExactlyOneWinner` |
| `Update` | `TestSameEntityContention_ConcurrentUpdatesSerializeNoLostUpdate` | `TestCrossProcess_ConcurrentUpdatesSerializeNoLostUpdate` (no-lost-update) + `TestUpdate_HoldsExclusiveFlockOnSidecarDuringCriticalSection` (isolates the flock's own contribution from the mutex) |
| `Delete` | Covered generically (no dedicated in-process contention test — `Delete` has no read-modify-write cycle to race, only an existence check + remove) | `TestCrossProcess_DeleteRacesUpdate_NeverResurrectsDeletedEntity` (specifically: Delete racing concurrent Updates, not Delete racing Delete) |

Four related fixes to the test suite's own robustness, found during the same
pass (not concurrency-contract gaps, but silent-failure risks in the test
code itself, closed under the same "no deferrals" instruction):

- `flock_isolation_test.go`'s `TestUpdate_HoldsExclusiveFlockOnSidecarDuringCriticalSection`
  had two `t.Fatal` exit paths that fired before its explicit `close(release)`,
  which would leave the blocked `Update` goroutine holding both the
  process-global striped-mutex shard (`lock.go` — 64 shards shared by every
  `Store[T]` in the binary) and the sidecar flock forever, hanging any later
  test whose entity ID happens to hash to the same shard. Fixed with a
  once-guarded `defer close(release)` registered immediately after the
  goroutine is launched, so every exit path (including `t.Fatal`'s
  `runtime.Goexit`, which does run deferred functions) releases it.
- `store_crossprocess_test.go`'s child processes had no `-test.timeout` (a
  directly re-exec'd test binary has none by default — only the `go test`
  wrapper injects the familiar 10m), so a child stuck on a leaked flock could
  hang forever, and a `t.Fatalf` mid-`Start`-loop left already-started
  siblings killed but never `Wait`-ed (POSIX zombies until the test binary's
  own process exits). Fixed with a per-child `-test.timeout=60s` plus a
  parent-side `context.WithTimeout` (belt-and-suspenders — the context
  force-kills the child even if its own internal timeout somehow fails to
  fire) and a `reapChildren` helper that both kills and waits every started
  process from `t.Cleanup`.
- A `fmt.Sscanf(s, "%d", &got)` counter-parse in the same file accepted
  trailing garbage without error (`"15x"` parses as `15`, `err == nil`),
  which would silently truncate a corrupted counter to a wrong number instead
  of failing loudly. Replaced with `strconv.Atoi` on a trimmed string, which
  rejects trailing garbage.
- `flock_isolation_test.go`'s same-process-contends-via-independent-open-file-description
  property (the mechanism its whole probe technique relies on) holds on
  local Linux/macOS/BSD filesystems, but Linux emulates `flock(2)` over NFS
  using POSIX record locks scoped per-process rather than per open file
  description — on an NFS-mounted test directory the probe would not block.
  This fails closed (a spurious failure, not a missed regression), which is
  documented as an acceptable, known limitation rather than left unstated.

**Confidence: High** — every new/changed claim in this subsection was
verified directly (overlay-based before/after runs, repeated and under
`-race`), not inferred.

## 6. `config.json` cross-process lock — D4 (REVISED — v1 did not fix the cited incident)

**Decision.** Add the sidecar lockfile + single-instance pidfile. **Do not claim
this fixes the `jq` incident.**

v1 said "wrap SaveConfig in WithFlock… closes a proven hole." It does not: flock
is advisory, `jq` never takes it, and `WriteFileAtomic`'s rename defeats
inode-flock anyway. The honest mitigation for an external hand-editor is the
**single-instance pidfile** (so a second *gateway* cannot race) plus operator
documentation. An arbitrary external process editing live state cannot be
prevented by cooperative locking, only detected — optionally via an mtime/size
guard that refuses to write over a file changed underneath us.

**Confidence: High** that v1 was wrong. **Medium** on the replacement.

## 7. Audit-log chain scoping — D5 (REVISED — rationale was factually wrong; now deferred)

**Decision: DEFER. Out of scope for this ADR.**

v1 kept the global chain and justified it on tamper evidence: *"an attacker who
deletes an entire scope's chain file leaves no gap."* **That property does not
exist today**, so it cannot be an argument for the status quo:

- Whole-log deletion **verifies clean** — `pkg/audit/verify.go:302-310` returns
  `Valid:true` for zero files `[FACT: review-verified]`.
- Tail truncation **self-heals across restart** — `audit.go:717-722` re-seeds
  from the last surviving line `[FACT]`.
- **No tip anchor exists**; `ChainResult.FinalHMAC` is `json:"-"`
  (`verify.go:62`), and `Entry` has no sequence number `[FACT]`.

And `pkg/plan/intent_log` already ships per-scope chains on the *same* audit
machinery with a domain-separated HKDF subkey (`intent_log_hmac.go:30-36`)
`[FACT]` — so "recovery data, not a security trail" was a label, not a technical
distinction.

Deferring for lack of a benchmark remains right, but as **YAGNI, not security**.
Audit-chain scoping is orthogonal to entity/config layout; carrying it here
risked ratifying a Medium-confidence security decision unexamined.

**Two follow-ups filed out of this ADR:**
1. The truncation / whole-file-deletion blind spot is a real audit gap → its own
   v0.2 security issue.
2. If throughput ever proves to be the ceiling: per-entry sequence numbers + a
   periodically persisted signed **tip** anchor (extending `checkpoint.go`, which
   is already a start-seed anchor). Split chains then record each scope's tip as
   an anchor entry in the global chain — global appends scale with checkpoints,
   not events. That design gets **both** parallel append and total-order evidence.

**Confidence: High** that deferral is correct. The v1 rationale is retracted.

## 8. Referential integrity and repair — D6 (EXTENDED)

**Rules:**

1. **Validate the delta, not the world.** A write validates only the references
   it *introduces*. Pre-existing dangling references must never reject an
   unrelated write. This alone un-wedges the observed failure `[FACT]`.
2. **Dangling is surfaced, never fatal.** Reads resolve what exists and report
   what does not.
3. **Explicit repair path.** A first-class "drop dangling members" operation,
   reachable from the UI. No config-file surgery.
4. **NEW — global invariants need a separate rule.** Delta-only validation is
   *insufficient* for cross-entity singletons. Concrete failure: `AgentConfig.Default`
   is an at-most-one global invariant enforced today only because every agent
   write shares `al.mu`. Split into per-entity files, two concurrent writes to
   two different agents can each set `default:true` — each delta valid, the
   composition broken `[FACT: review-verified]`.
   **Second uncaught global invariant: `coreagent.SeedConfig`** — `core.go:1091-1093`
   builds an `existing` set from the roster then appends missing core agents. One
   `al.mu` makes that atomic today; split per-entity, two concurrent boots (or a
   boot racing a create) can each see "Mia missing" and both seed her. Needs an
   owning lock. Resolve together with the filename/record-ID mismatch question
   (§12) — they are the same uniqueness invariant from two sides.

   **Resolution: move the default flag OUT of the entity into settings**
   (`agents.defaults.default_agent_id`). **This field ALREADY EXISTS**
   (`config.go:1247`, env `OMNIPUS_DEFAULT_AGENT_ID`) — this promotes it, it does
   not invent it. `AgentRegistry.GetDefaultAgent` (`registry.go:241-280`) has a
   FOUR-priority ladder: (1) `IsRoutingDefault`, (2) this setting, (3) the
   `"main"` sentinel, (4) lexicographically-first non-worker. D6.4 deletes
   priority 1; **the spec must state what happens to 2-4**, and that
   `RepairMultipleDefaults` (`validate.go:325-367`) and
   `AgentInstance.IsRoutingDefault` (`instance.go:341`) become dead code.
   Honest correction: the harm today is **UX (a user's ★ silently demoted), not a
   broken invariant** — all three consumers already tie-break deterministically
   (`registry.go:248-257` sorts; `route.go:349` first-wins; `validate.go` repairs).
   Confidence on the diagnosis is therefore **Medium**, not High. Any *remaining* global invariant must be named explicitly and
   given an owning lock.
5. **NEW — delete contract.** Deletion is the operation that *creates* dangling
   references, and "validate the delta" makes it trivially valid. Deletes must:
   remove the entity record first, then best-effort clean the agent's directory;
   and dangling referrers (bindings, mailboxes, workspace `core_team`) are
   surfaced for repair per rule 2, not silently pruned.

**Corollary — write-then-verify.** Entity creation must read back and confirm
persistence before reporting success. Any roster referencing newly-created
entities must be written from *verified* IDs. This is the durable fix for the
observed class of failure where a roster named entities that were never
persisted.

**Confidence: High** on rules 1-3 and 5. **High** on rule 4's diagnosis;
**Medium** on moving `default` to settings (clean, but touches routing).

## 9. Boot / load semantics — D7 (REVISED — v1's security claim was wrong)

**Decision. Skip-and-warn per entity for availability — but absence is
SECURITY-RELEVANT, and the fallbacks it triggers must fail closed.**

v1 claimed *"An absent entity grants nothing."* **Withdrawn.** Two verified
paths turn absence into a privilege change:

1. **Delegation grants the parent's policy.** `pkg/agent/subturn.go:700-712` —
   target not in registry → WARN → `execSource = baseAgent`, then `:817`
   `StoreToolPolicy(execSource.LoadToolPolicy())`. Delegating to a locked-down
   worker whose file was skipped runs the sub-turn **with the delegating parent's
   tool policy** `[FACT: review-verified]` — inverting the reason to delegate,
   and contradicting the principle documented eight lines below it.
2. **Routing falls back to the default agent.** `pkg/routing/route.go:331-336` —
   a binding naming an absent agent WARNs and returns `resolveDefaultAgentID()`.
   Traffic bound to a restrictive agent re-routes to Mia `[FACT]`.

**Required changes (part of this work, not follow-ups):**
- `subturn.go:709-711` must **abort the sub-turn** when the delegation target is
  absent — never substitute the parent's identity or policy.
- `pickAgentID` must **refuse** rather than re-route when a binding's target was
  skipped. This requires distinguishing "skipped due to load failure" from
  "never existed" — the loader must record skipped IDs.

**Boot taxonomy (v1 was incomplete):**
| Case | Behaviour |
|---|---|
| Unparseable record | Skip + ERROR + mark degraded in `/health` |
| Parseable, incomplete tool-policy coverage | `RepairIncompleteToolPolicyCoverage` → **deny**, then persist. NOTE: this means N entity writes at boot — v1 never mentioned this |
| Filename / record-ID mismatch | **Unspecified today** — must be decided in the spec |

**Constraint #6 note.** The narrow claim holds — a skipped agent resolves no tool
policy, so the *resolver* creates no implicit allow, and `validate.go:478-479`
still aborts boot on a coverage gap for loaded agents. But a skipped agent never
reaches the validator, so "boot validates every agent × tool" silently becomes
"every *loadable* agent × tool". That narrowing must be stated, not glossed.

**Confidence: Medium.** Availability reasoning holds; the fail-closed fallbacks
are newly required and unimplemented. Flagged for explicit operator ratification.

## 10. Consequences

**Positive**
- Agent writes parallelize per-agent; the global config lock stops being the
  fan-out ceiling.
- Write amplification drops from a whole-file rewrite (17.8-61 KB, install-
  dependent) to one ~0.6-1.3 KB record. See §1 for the measured range.
- Cross-process safety for entities and settings **against cooperating writers
  only** — never against a non-participating writer (`jq`, `sed`), and `flock` is
  a no-op on Windows (`flock_windows.go:34-40`). See D3/D4; do not quote this
  line without them.
- Agent identity is one directory PLUS one record in `entities/` — the record was
  moved out for security (D2). Slightly *less* portable than a single directory;
  this is the price of not letting an agent rewrite its own tool policy.
- A wedged workspace becomes structurally impossible (D6.1).

**Negative / cost**
- `List` is O(N) file reads. Acceptable at expected N; would need revisiting at
  thousands of agents `[INFERENCE]`.
- More inodes; many small files instead of one.
- Every read path touching `cfg.Agents.List` must move to the store API — a
  broad, mechanical change across `pkg/gateway`, `pkg/agent`, `pkg/sysagent`.
- No migration means **existing installs lose their agents** on upgrade. The
  operator has explicitly accepted this.

**Neutral**
- Wire types are unaffected *if* the REST surface keeps returning the same
  `Agent` shape. Any change there is Constraint #8 territory — contracts first.

## 11. Risks and the removal checklist (REVISED — v1's primary mitigation was false)

**v1's risk table said "remove the field entirely so the compiler finds every
site." That is false and is withdrawn.** The entire agent CRUD write path is
**string-keyed `map[string]any` mutation**, invisible to the compiler — all via
`safeUpdateConfigJSON` → `updateConfigJSONLocked` (`rest.go:3924`)
`[FACT: review-verified]`.

**Mandatory conversion checklist** (each site still compiles after removal and
silently writes into a key nothing reads):

| # | Site | What it does |
|---|---|---|
| 1 | `rest.go:2491` | agent create |
| 2 | `rest.go:2712` | agent delete |
| 3 | `rest.go:3208` | agent PUT |
| 4 | `rest.go:6965` | tools / policies |
| 5 | `rest_mailbox.go:647` | email grants |

Plus:
6. **Scope to `agents.list` ONLY.** Do NOT delete the `"agents."` prefix from
   `knownConfigPrefixes` (`sysagent/tools/config.go:139`) — that gates the whole
   `agents.` namespace, and removing it breaks every `agents.defaults.*` key
   including D6.4's new `default_agent_id`. Reject `agents.list` specifically.
7. Add **`agents.list`** (NOT `agents`) to `blockedPaths`
   (`blocked_paths.go:25-31`). `matchBlockedPath` matches exact recorded paths,
   so blocking `agents` would also block `{"agents":{"defaults":…}}` and make
   agent defaults unwritable via `PUT /api/v1/config`.
8. **Explicitly strip `agents.list` on load — NOT the whole `agents` key**
   (`agents.defaults` stays, per D1). Not doing so corrupts data two
   different ways:
   - Removing all of `Agents` → `agents` becomes an *unknown* top-level key,
     stashed by `detectUnknownConfigFields` (`migration.go:59-86`) and re-emitted
     verbatim forever. **Proven live: `heartbeat` is being round-tripped as a
     ghost key right now** `[FACT: review-verified]`. The 31 KB blob would stay in
     config.json and be rewritten on every settings save — **negating this ADR's
     headline benefit**.
   - Removing only `AgentsConfig.List` → `agents` stays *known*, `list` is
     dropped on unmarshal, and the next struct-based `SaveConfigLocked` **erases
     the roster with no error**, triggerable by any `set_config` call.
9. Add a lint/test asserting no `m["agents"]` string access survives.

| Risk | Severity | Mitigation |
|---|---|---|
| A raw-map site is missed | **High** | The 9-point checklist above + the lint in #9. The compiler will NOT help |
| Ghost `agents` key inflates config forever | **High** | Checklist #8 (strip on load) |
| Partial cutover → two sources of truth | High | No back-compat: one atomic change, no dual-read window |
| Absence-triggered privilege change | **High** | D7's fail-closed fallbacks |
| Cross-store torn read breaks Constraint #6 coverage | Medium | `ValidateToolPolicyCoverage` reads settings AND entity (`validate.go:491-518`); needs a defined consistency point |
| Credential redaction shrinks | Medium | `collectSensitive` reflection-walks `Config` (`security.go:110-167`); moving channels out removes their `SecureString`s from the redactor with **no compile error**. Redactor must walk entities too |
| `GET /api/v1/config` breaks silently | Medium | It marshals the whole Config and is **not in `contracts/openapi.yaml`** — `make verify-contracts` structurally cannot catch this. Contract it first |
| Orphan directories become live agents | Medium | 122 dirs exist for 8 agents on the reference install `[FACT]`; an orphan gaining a record becomes a live agent. Records live in `entities/`, so this is mitigated by D2's revision |
| Nested lock deadlock | Medium | `pkg/workspace/lock.go:62-70` striped locks are non-reentrant, 1/64 collision. Need a documented lock-ordering rule (workspace before agent) |
| O(N) listing on a hot path | Medium | `pickAgentID`/`resolveDefaultAgentID` scan **per inbound message** (`route.go:298-375`). `AgentRegistry` is already a cache — define how a per-entity write invalidates it |

**D1 additions (M6).** Three keys were unclassified and must be decided in the
spec: `bindings` (`[]AgentBinding` — the channel→agent routing table,
runtime-mutated), `mailboxes` (`map[agentID]map[workspaceID]` carrying
`password_ref`), `channel_policies`. The first two are exactly the agent
foreign-key surface D6 governs. Also: **`providers` has no identity field** —
`ModelConfig` has only the renameable `ModelName`, so `providers/<id>.json` is
not implementable as written and needs an ID introduced or must stay a settings
collection. And `Channels` is a `map`, not an array as v1's table said.

## 12. Gaps and ambiguities

- `[UNKNOWN]` Real contention numbers for the config lock and audit mutex. The
  perf gate at the end of this work closes this.
- `[UNKNOWN]` Expected steady-state agent count — drives whether O(N) listing
  ever needs the existing `AgentRegistry` cache to be formalised.
- `[UNKNOWN]` Filename / record-ID mismatch behaviour on load. Undecided.
- `[UNKNOWN]` Whether external tooling reads `config.json`'s `agents` key.
- `[UNDECIDED]` `bindings`, `mailboxes`, `channel_policies` classification.
- `[UNDECIDED]` `providers` has no identity field — needs one, or stays settings.
- `[ASSUMPTION]` `channels`/`providers` are cold paths (inspected on one install,
  not measured under load).

## 13. Review history

**v1 → v2 after adversarial review (verdict REVISE, 4 CRITICAL).** Changes:

| Finding | Outcome |
|---|---|
| C1 — co-located record inside agent-writable WorkDir = privilege escalation | **Accepted.** D2 moved to `entities/agents/<id>.json`. Verified independently: `metadata_guard.go:32-37` whitelist, `carveout.go:41-49` own-tree exception |
| C2 — "absence grants nothing" false; delegation + routing fall back permissively | **Accepted.** D7 rewritten; fail-closed changes now in scope |
| C3 — flock/rename inode defeat; reads unlocked; Windows no-op | **Accepted.** D3 cross-process claim withdrawn. Verified independently: `fileutil/file.go:62-67`, `task/store.go:117` |
| C4 — "compiler finds every site" false (5 raw-map sites); ghost-key corruption | **Accepted.** Replaced with a 9-point checklist |
| M1 — D5 tamper rationale factually wrong | **Accepted.** D5 retracted and deferred as YAGNI; audit blind spot filed separately |
| M2 — two valid deltas compose invalidly (`default`) | **Accepted.** D6 rule 4; `default` moves to settings |
| M3 — D6 silent on DELETE | **Accepted.** D6 rule 5 |
| M4 — ReadDir ordering breaks 3 dependents | **Accepted.** Explicit sort + `sort_index` |
| M5 — O(N) on a per-message hot path; registry cache exists | **Accepted.** In risk table; invalidation design owed to spec |
| M6 — D1 incomplete; `providers` has no ID | **Accepted.** Listed as undecided |
| M7 — credential redaction shrinks silently | **Accepted.** Risk table |
| M8 — cross-store consistency for Constraint #6 | **Accepted.** Risk table |
| M9 — `GET /api/v1/config` uncontracted | **Accepted.** Risk table |

Two CRITICAL claims (C1, C3) were independently re-verified against the code
before acceptance rather than taken on trust.

### v2 → v3 (second review: REVISE, 3 CRITICAL)

| Finding | Outcome |
|---|---|
| C-1 — `entities/` is NOT in `buildCarveOuts`; under `FSScopeUnrestricted` v2 was WORSE than v1 (whole-roster vs self admin) | **Accepted.** Independently verified `carveout.go:16-23` returns only 4 roots. D2 now mandates adding `entities` |
| C-2 — `bash`/`DefaultChildPolicy` bypasses the FS layer entirely; "NOT yet active" | **Accepted.** Residual stated explicitly in D2; routed to v0.3 #156 |
| C-3 — checklist items 6/7/8 scoped to `agents`, breaking `agents.defaults` and contradicting D1 | **Accepted.** All three re-scoped to `agents.list` |
| M-1 — `sort_index` re-serializes the create path and is an unowned global invariant | **Accepted.** Withdrawn; sort by `(created_at, id)`; `CreatedAt` field must be added |
| M-2 — `LOCK_EX`-per-read × per-message routing scan would make the ADR net-negative | **Accepted — design-shaping.** Reads now go through the existing `AgentRegistry` cache; disk+lock is the WRITE path. Invalidation is part of this decision, not deferred |
| M-3 — sidecar unlink race; D6.5 delete contract broke it | **Accepted.** Lockfile never unlinked; pidfile staleness cites `coordinator_lock_other.go` |
| M-4 — headline metric does not reproduce | **Accepted.** Both installs re-measured and named; argument restated so it does not rest on the ratio |
| M-5 — §10 Consequences contradicted §5/§6/D2 | **Accepted.** All three bullets rewritten |
| M-6 — `default_agent_id` already exists; 4-priority ladder unaddressed; invariant harm overstated | **Accepted.** Confidence lowered to Medium; dead code named |
| M-7 — `SeedConfig` is an uncaught global invariant | **Accepted.** Named in D6.4 |
| m-1 — fail-closed has a shipped precedent (`ResolvedRoute.Drop`) | Noted for the spec |
| m-2 — D6.4 × D7: who is default when the default's record is unparseable | **OPEN — must be decided before plan-spec** |

**Still open after v3:** m-2 (D6.4 × D7 interaction), and the three D1
classifications (`bindings`, `mailboxes`, `channel_policies`) plus `providers`
identity from §12.

## 14. Next steps

1. **Re-grill this revision.** v2 changes four decisions; it has not been
   adversarially reviewed in its current form.
2. **`/plan-spec`** once v2 passes — the spec owes: the 9-point conversion
   checklist as executable tasks, the registry-invalidation design, the
   lock-ordering rule, and the three undecided D1 classifications.
3. Parallel and independent: the slash-command palette fix (in flight).
4. Perf gate: N concurrent entity writers + audit-append benchmark.
