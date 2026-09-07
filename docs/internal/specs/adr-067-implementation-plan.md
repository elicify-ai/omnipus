# ADR-067 — implementation plan

- **Date:** 2026-08-22
- **Delivers:** [the spec](adr-067-knowledge-base-and-preview-spec.md) — 117 requirements, ~130 tests, five stages
- **Branch:** `feat/library-improvements`
- **Status:** plan, not started

---

## 1. What actually limits parallelism

**Six concurrent agents per workflow** on this machine — `min(16, cores − 2)`, and there are 8
logical cores (4 physical). Excess agents queue rather than fail. Total agents per workflow is a
separate, configurable guideline (`/config` → Dynamic workflow size, currently "medium" ≈ 15).

**But the six is rarely the binding constraint, and it is important to know which constraint is
biting**, because the answer changes what to parallelise:

| Work | Bound by | Parallelises? |
|---|---|---|
| Reading, reasoning, writing code | **Model latency** — the agent is waiting, not computing | **Yes, well.** Several workflows at six each is fine |
| Go build / test | **CPU and RAM.** This project's own notes warn the suite can exhaust memory and say CI is the authority | **No.** Serialise. One at a time, or push and let CI run it |
| Browser install / Playwright runs | CPU, disk, network | **No.** Today five agents each installed their own browser copy; that, not the agent cap, is why it felt slow |

**So: many code-writing agents in parallel, one build at a time.** Load average was 7.12 on 8
cores while browser agents ran — saturated. The same six agents editing files would barely
register.

### The constraint that actually bit, twice

**Two agents editing one file collide.** It happened twice in a single session — two agents
independently claiming the same test numbers, and one edit block applied twice. Neither was
caught by the tooling; both were caught by reading.

**Every wave below therefore partitions by *file ownership*, not by subject.** No two agents in
a wave may write the same file. Where a stage's work spans a file another stage owns, the stages
are sequenced rather than the file shared.

### Worktree isolation — insurance, not a replacement

Implementing agents run with **`isolation: "worktree"`**: each gets its own checkout, and its
work is merged back rather than written in place.

**What it buys.** It converts a *silent* collision into a *visible* merge conflict. Both of this
session's collisions corrupted a file quietly and were caught by reading it, not by any tool. A
conflict marker cannot be missed. That is the whole argument, and it is enough.

**What it does not buy.** If the ownership partition holds, worktrees change nothing — the value
appears precisely when the partition turns out to be wrong, which is the case that keeps
happening. So ownership stays the primary mechanism and isolation is the backstop. Ranking them
the other way round produces agents that merge cleanly into the wrong design.

**Where it is the wrong tool: the spec documents.** Both collisions were two agents in one
markdown table, on adjacent rows. Git merges that into a mess or conflicts on nearly every line.
For prose, one owner beats any merge strategy. Worktree isolation is for **code** — different
functions, different packages — where merges are clean.

> **Gotcha, from this project's own notes.** The GitNexus index is **per-checkout**, and every
> checkout registers under the same repository name. An agent in a fresh worktree therefore
> either has no index, or resolves by name and silently reads **another checkout's graph** —
> a wrong-branch answer with no error. Agents running in worktrees MUST be told to use direct
> file reading and to treat the graph as unavailable.

**Cost, stated:** this checkout is ~144 MB, so a wave of six is roughly 900 MB of transient disk
and eighteen agents about 2.6 GB. The Go build cache is shared by default and is not duplicated.

---

## 2. Dependency reality

The spec's five stages are not five independent parcels.

```
Wave 1  Foundations ─────────────► everything
                                    │
Wave 2  Stage 0 backend ───┐        │  (pkg/pathsafe, pkg/library, rest_library create paths)
        Stage 1 frontend ──┤        │  (src/ only — file-disjoint from Stage 0)
                           ▼        ▼
Wave 3  Stage 1 backend ───┐   (rest_library inline mode — needs Stage 0 to release the file)
        Stage 2 backend ───┤   (pkg/knowledge — new package, disjoint)
                           ▼
Wave 4  Stage 2 frontend ──┐   (reading surface)
        Stage 3 backend ───┤   (write path, concurrency)
                           ▼
Wave 5  Quality gates → fixes → re-review
```

**Stage 4 (`ev` lock interoperation) is not in this plan.** It gates on a contract that has to be
agreed on another project's side. It ships separately or not at all.

### Why Foundations must be first, and is not optional

Three things in Wave 1 are the difference between a green suite and a *meaningful* green suite:

1. **`src/components/library/` matches no vitest group.** Eleven existing test files there run
   nowhere in CI, and it is the exact directory this feature modifies. Every frontend test
   written before this is fixed would report green without executing.
2. **Contracts before code** is a hard project constraint. Wire types written after the handler
   fail the lint gate and get retrofitted badly.
3. **Tool-policy seeding.** The knowledge tools must be enumerated explicitly or they arrive
   *silently denied* — boot succeeds, the feature is dead, and a log line is the only evidence.

Building anything before these three is building on an instrument that cannot read.

---

## 3. The waves

Each wave is one workflow. Agents inside a wave run concurrently, up to six.

### Wave 1 — Foundations (6 agents)

| # | Agent | Owns | Done when |
|---|---|---|---|
| 1 | vitest coverage | `.github/workflows/pr.yml` (vitest matrix), `scripts/check-vitest-coverage.mjs` | Tests 97, 98 pass; the 11 library files run |
| 2 | Playwright matrix | `playwright.config.ts`, `tests/e2e/shards.json`, CI install lines | Test 120 passes; five projects at `retries: 0`; **every** spec on disk matched |
| 3 | Windows CI | `.github/workflows/`, `Makefile` | Windows job runs `pathsafe` tests; `GOOS=windows go vet` on the Linux leg; the false `cross-platform-extra.yml` comment corrected |
| 4 | Contracts | `contracts/**`, generated artefacts | `make verify-contracts` clean; all wire types from the spec's contract table exist |
| 5 | Tool policy | `pkg/config/defaults.go`, `pkg/coreagent/core.go` | Tests 34, 35 pass **including 35's positive control** |
| 6 | **Verifier** | nothing — read-only | Each of 1–5 checked against its acceptance criteria, independently |

> **Agent 2 carries a trap.** Adding a `projects` array makes any spec *not matched by a project*
> stop running. A shard with five specs where four match runs four and reports green — the same
> false-green as the vitest gap, arriving inside its own fix. Test 120 asserts every spec on disk
> is matched by exactly one project.

### Wave 2 — Stage 0 backend ∥ Stage 1 frontend (two workflows, 6 + 6)

File-disjoint by construction: **2A is `pkg/`, 2B is `src/`.**

**Workflow 2A — Stage 0 (CRITICAL-risk).** `pathsafe.ValidateComponent` has 17 dependents.

| # | Agent | Owns |
|---|---|---|
| 1 | Rule-set value + `GOOS` selection files | `pkg/pathsafe/rules*.go` |
| 2 | Split control-chars from Windows-shape in **both** the validating and sanitising paths | `pkg/pathsafe/pathsafe.go` |
| 3 | `.` / `..` rejected independently, surfacing the empty-name sentinel | same file — **sequenced after 2, not concurrent** |
| 4 | `ValidateCreateName` on the resolved root + the five create/rename handlers | `pkg/library/root.go`, `pkg/gateway/rest_library.go` |
| 5 | RFC 6266 disposition + length-by-unit | `pkg/gateway/rest_library.go` — **sequenced after 4** |
| 6 | **Verifier** — runs each named mutation and confirms the guard dies | read-only |

> Agents 2/3 and 4/5 share a file, so they run as two sequenced pairs. Effective concurrency here
> is **4**, not 6. Claiming 6 would be a number, not a plan.

**Workflow 2B — Stage 1 frontend.** Entirely `src/`.

| # | Agent | Owns |
|---|---|---|
| 1 | PDF.js component + lazy chunk + hardening options | `src/components/library/preview/LibraryPdfPreview.tsx`, `vite.config.ts` |
| 2 | `classifyLibraryEntry` — **HIGH risk**, purely additive | `src/components/library/preview/libraryPreviewKind.ts` |
| 3 | Preview pane wiring, untrusted-content chrome | `src/components/library/LibraryPreviewPane.tsx` |
| 4 | Deep-linking `?path=` | `src/routes/_app/library.tsx`, `LibraryExplorer.tsx` |
| 5 | KB markdown composition (second composition, **not** a second pipeline) | `src/components/library/preview/LibraryMarkdownPreview.tsx` |
| 6 | **Verifier** | read-only |

### Wave 3 — Stage 1 backend ∥ Stage 2 backend (6 + 6)

**3A — Stage 1 backend:** inline disposition mode, the Library type table, the preview-token
store and route, the redaction helper, and **every** inline-serving route brought under one
policy — including the third one nobody had enumerated.

**3B — Stage 2 backend:** the new `pkg/knowledge` package — detection, index (scorch, keyed by
realpath, stored outside the vault), manifest and incremental scan, link resolution with
containment, `knowledge_search` / `knowledge_graph` scoped to the caller's workspace.

Disjoint: 3A is `pkg/gateway`, 3B is a new package.

### Wave 4 — Stage 2 frontend ∥ Stage 3 backend (6 + 6)

**4A:** search box, outline, backlinks rail, adaptive reading layout, empty-state first run.
**4B:** authoring tools, templates, link rewriting with the journal, version-token concurrency,
audit events, lifecycle (revoke refcount, broken mount, evicted files).

### Wave 5 — Quality gates

Sequential, because each depends on the last.

1. **`/code-review high`** over the whole branch diff. *(Founder choice. `large` is not a level — the
   ladder is low, medium, high, max, ultra. `high` gives broad coverage without `max`'s tail of
   uncertain findings, which on a diff this size would cost more triage than it returns. `ultra`
   is the cloud multi-agent review and is user-triggered only; I cannot launch it.)*
2. **`test-integrity-auditor`** — audits the suite for assertion weakening, over-mocking,
   suppressed tests, oracles adapted to the implementation, and stale green. Returns a weakening
   score and a block/warn/pass verdict.
3. **Fix wave** — up to 6 agents, partitioned by file, one finding-cluster each.
4. **Re-review** — `/code-review high` again on the fix diff only. A fix wave that is never
   re-reviewed is where regressions enter.

---

## 4. Quality gates that run *inside* every wave

Not only at the end.

**Every workflow's last agent is a verifier, and it is read-only.** It does not fix; it reports.
Its job is to answer one question per implementing agent: *does the acceptance criterion actually
hold, and does the test actually fail when the feature is removed?*

**Every test must name the mutation it dies on.** The spec already does this for most tests. A
test that survives deleting the thing it guards is worse than no test, because it reports safety.
The verifier runs the named mutation and confirms the red.

**No agent marks its own work done.** The verifier is a different agent with no stake in the
implementation.

**CI is the authority for Go tests.** Agents push and read the checks rather than running the
suite locally — this project's notes are explicit that the full suite can exhaust memory here.

---

## 5. Risks, and what each would cost

| Risk | Why it is real | Mitigation |
|---|---|---|
| **Two agents, one file** | Happened twice in one session | Ownership table per wave; no file has two owners; shared files sequence |
| **A wave reports green having run nothing** | Happened: 11 test files run nowhere today | Wave 1 first, and its verifier checks the gates themselves |
| **CRITICAL-rated change breaks the gateway** | `ValidateComponent` has 17 dependents across Gateway and Agent | Signature unchanged, behaviour behind a value; mutation-tested by the verifier |
| **Memory exhaustion from parallel builds** | Project notes warn the suite OOMs here | One build at a time; CI is the authority |
| **The unverified parts ship as though measured** | Three are labelled reasoned-not-measured: SVG, the SPA policy, Safari | Each is gated on a named test before its stage ships |
| **Fix wave introduces regressions** | Standard | Step 4 re-reviews the fix diff |

---

## 6. What this plan does not cover

- **Stage 4** (`ev` lock interoperation) — gated on another project's contract.
- **Form filling and signing** — proven feasible, deliberately out of scope for this release.
- **The vault design note** — the project's own design-first rule wants it before the ADR is
  ratified. This plan builds against a `Proposed` ADR.

---

## 7. Wave 1 — outcome (2026-08-22)

Six agents, zero errors. **The vitest gap was far larger than the spec knew: 117 of 427 SPA test
files (27%) matched no group and ran nowhere** — not the 11 the spec cited, and including all 57
workspace tests. Two configured patterns pointed at deleted directories, not one.

Landed and independently verified:

| Work | Evidence |
|---|---|
| Six rebuilt vitest groups + a coverage guard job | Guard exits **1** on an injected orphan file, **0** clean — verified without a pipe, since `exit=$?` after `\| tail` reports the pipe's status |
| Playwright matrix, five projects at `retries: 0` | Config written; specs are skipping placeholders until later waves |
| Windows `pathsafe` job, `GOOS=windows go vet` | Cross-compiles clean locally; **the job itself has never executed** — no Windows machine here |
| Contracts: 18 schemas, generated Go + TS | `make verify-contracts` exits 0, no drift |
| Tool-policy seeding, all three catalogs | Five named tests pass; positive control shown red |

**Assigned and fixed by the orchestrator:** the Fly CI worker installed Chromium only, so the
three-engine isolation projects would have failed with `Executable doesn't exist` — the exact
phantom-failure signature `runci.sh` already documents from 2026-07-26. Both the Dockerfile and
the runtime install now cover chromium, firefox and webkit, and the revision check verifies all
of them rather than two.

> **Done, 2026-08-22 — image rebuilt and verified by launch, not by inventory.** `fly deploy`
> rebuilt `ci-omnipus` (1.1 GB), and all three engines now launch **and render** on the worker:
> chromium-1228, firefox-1532, webkit-2311. Presence in a directory listing would not have
> proven this — WebKit needs system libraries Chromium does not, so the test was to launch each
> engine and read text back out of a rendered page.
>
> Two drifts were fixed in the same deploy. The image pinned Playwright **1.49.0** while the repo
> is on **1.61.1** — the Dockerfile's own comment says to keep them in sync, and they had
> silently diverged, which is why `runci.sh` was re-downloading browsers at runtime on every
> single run. And the pinned apt list is Chromium's; WebKit's differs by Debian release and
> Playwright version, so the install now uses `--with-deps` rather than a hand-maintained list
> nobody can audit against what is actually installed.
>
> Also: `/cache/runci.sh` (the **executing** copy — a deploy does not update it) was refreshed to
> md5 `1f64cbf6…`, matching local, and a **31-hour-stale `/tmp/runci.lock`** was cleared. That
> lock is the mutex serialising runs; left stale it is a plausible cause of a future run
> refusing to start for no visible reason.

**Process lesson, recorded because it cost real time.** Two file collisions and one broken build
came from the orchestrator **messaging an agent while it was still running** — which resumes it
into a second concurrent execution. Both instances then wrote the same files, each experiencing
the other as an intruder: identical substance, different prose. **Do not message a running
agent.** Feedback waits for completion or goes into the next brief.

A `go test` was also OOM-killed mid-mutation, leaving a shared file altered for about a minute.
The agent restored from a checksummed backup and declined to retry — so one mutation is
**argued, not measured**, and says so.
