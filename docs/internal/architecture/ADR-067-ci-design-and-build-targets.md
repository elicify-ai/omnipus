# ADR-067 — CI pipeline design and supported build targets

- **Status:** Accepted (2026-08-22) — founder decided the build-target set and the
  single-container-image rule directly; lead decided the tier structure and the
  change-detection soundness boundary.
- **Date:** 2026-08-22
- **Deciders:** founder (build targets, container image, Windows descope), lead
  (pipeline structure, change detection, dead-signal remediation)
- **Related:** [ADR-062 — Reads and execute default open](ADR-062-filesystem-read-exec-model-inversion.md)
  (§9's container sandbox default, and the stale test in §7.2);
  [docs/internal/false-green-patterns.md](../false-green-patterns.md) (the verification
  traps this design is shaped around); issues #635, #636, #637.
- **Evidence level:** claims marked **[VERIFIED]** were read from the repository at
  commit `c271781d`, parsed from `.github/workflows/`, or retrieved from the GitHub
  Actions API on 2026-08-22. Claims marked **[INFERRED]** are reasoned, not measured.
- **Supersedes in part:** the `pull_request`-only trigger on `pr.yml`; the
  `darwin/amd64` `ignore` block in `.goreleaser.yaml`; the five surplus targets in
  `Makefile::build-all`; `build-lite`, the `lite` build tag and
  `lite-build-weekly.yml`; four of the five files in `docker/`.

---

## 1. Decision

Five pipelines, four binaries, one container image.

**Pipelines** — each answers one question at one moment:

| Pipeline | Trigger | Role |
|---|---|---|
| Pull request | `pull_request` | Blocking gate |
| Release branch | `push: [release/**, main]` | Same checks; **new** |
| Nightly | `cron 0 0 * * *` | Reports; never blocks |
| Weekly | `cron 0 3 * * 0`, `0 4 * * 1` | Reports; never blocks |
| Manual | `workflow_dispatch` | Release publishing, probes |

**Build targets** — `linux/amd64`, `linux/arm64`, `darwin/arm64`, `darwin/amd64`, and
one multi-arch container image built from `docker/Dockerfile.heavy`.

**CI tests exactly what ships — no more, no less.**

## 2. Context: three problems, one shape

CI ran 31 jobs per pull request across 11 workflows **[VERIFIED]**. That was not the
problem. The problem was that **three of those workflows reported to nobody**:

| Workflow | Evidence **[VERIFIED]** | Consequence |
|---|---|---|
| `evals-nightly.yml` | 100 of 100 runs failed, 2026-05-16 → 2026-08-22 | Three months of AI evaluation producing nothing |
| `cross-platform.yml` | 6 consecutive failures across two unrelated branches | Hid a stale security test for ten days |
| `pr.yml` | `pull_request` only | 23 commits merged to a release branch ran CodeQL and nothing else |

The `cross-platform` case is the instructive one. Because the workflow was permanently
red, a test that had drifted out of line with ADR-062 went unexamined. When it was
finally read, it was **mistaken for a live path-traversal vulnerability and filed as
P1** — a false alarm that cost real time and had to be publicly corrected (#635).

**A gate nobody believes is worse than no gate.** It burns runner minutes, trains
people to scroll past red, and hides the one failure that mattered. Every decision
below is ordered by that principle: make fewer signals, make each one readable, and
only then make them fast.

## 3. Pipeline structure

### 3.1 Pull request — four tiers

Tiers are sequential. Tier *N* runs only if Tier *N−1* succeeded, so a cheap failure
short-circuits an expensive one. Budgets are targets for the slowest job in the tier.

**Tier 0 — triage (< 15s).** One job computes the change set and emits booleans every
later tier reads. Fails open: any ambiguity means run everything.

**Tier 1 — fast gates (< 3 min).** Frontend types; frontend style; backend style and
safety; API spec matches the code; no hand-written wire types; deleted surfaces stay
deleted; removed CLI verbs stay removed; test-shard plan consistency; browser tests
properly gated; shell script validity.

**Tier 2 — correctness (< 15 min).** Backend test suite; frontend test suite (6
shards); single-binary build with no CGo; known-vulnerability scan; browser automation
against real Chrome.

**Tier 3 — integration (< 25 min).** Security suite; performance smoke; full user
journeys through a real browser; macOS sandbox enforcement; the cross-platform matrix.

### 3.2 Release branch

Identical checks to §3.1. This pipeline **does not exist today** and is the single most
important addition: merging directly to a release branch currently runs almost nothing.

### 3.3 Nightly and weekly — reporting, not gating

Nightly: release artifact build; unused-code report (Go and SPA); AI evaluations.
Weekly: deep security audit.

Reporting jobs must be **informational by construction, not by `continue-on-error`**.
The dead-code scanner exits non-zero only if the analyser binary is missing — a real
infrastructure failure — while findings are written to the run summary. The SPA
equivalent uses its own `--no-exit-code` flag rather than swallowing a genuine failure
code. The distinction matters: `continue-on-error` on a gate hides real breakage;
a job that structurally cannot fail on findings is honest about what it is.

## 4. Change detection

### 4.1 The boundary

Selection is allowed **only where the skip is provable from the change set**, never
where it is merely probable.

| Layer | Mechanism | Soundness | Status |
|---|---|---|---|
| L0 | Every changed path is documentation | Provable | Implemented |
| L1 | No Go files changed → skip Go suites; no SPA files → skip frontend suites | Provable | Proposed |
| L2 | `go list -deps` transitive closure | Sound, with caveats | Deferred |
| L3 | "This directory usually relates to that suite" | **Unsound** | **Rejected** |

### 4.2 Why L3 is rejected

**Impact travels by symbol; change sets are expressed in files.** Two incidents on a
single branch **[VERIFIED]**:

- An `errcheck` sweep touched only `pkg/audit` and produced 62 new `govet` findings
  across the whole repository.
- A change to `pkg/tools/registry.go` broke judge tests in `pkg/agent`, a package it
  does not appear in.

A path-keyed selector would have skipped both. This is rejected on direct precedent,
not on general caution.

### 4.3 Why L2 is different

`go list -deps` reports the compiler's own import graph. If package *B* imports *A* and
*A* changed, *B*'s tests run. That is not a heuristic. It ships only after the suite is
reliably green, and the merge into a release branch always runs unscoped.

Four caveats ship with it:

1. **Compute per `GOOS`.** A darwin host cannot see `//go:build linux` files. Four
   `gosec` findings in `sandbox_linux.go` were invisible locally for exactly this
   reason **[VERIFIED]**.
2. **Reflection and interface dispatch are not import edges.** Tools registered by
   name, a `yaml.Unmarshaler` invoked by the library, MCP tools resolved at runtime.
3. **Non-Go inputs force the full suite** — `contracts/`, `.golangci.yaml`, `go.mod`,
   `Makefile`, `.github/`.
4. **Diff against the merge base**, never `HEAD~1`.

### 4.4 Invariants

- **Fail open.** If the change set cannot be computed, run everything.
- **Skipped is not absent.** A skipped required check must still report a conclusion.
  Use job-level `if:`, never trigger-level `paths-ignore` — a workflow that never
  triggers creates no check run, and branch protection waits forever on a job that will
  never report.
- **Log the decision.** Silent truncation reads as "everything passed".

## 5. Toolchain pinning

CI pins `golangci-lint v2.10.1` **[VERIFIED]**. A development machine ran `2.12.2`.
`G115` behaves differently between them, so a local *"0 issues"* was true and useless:
the gating version found four findings the local one did not, and running the pinned
version locally then found **four more that CI had not reported**.

**The linter version used locally must match CI's.** A green that was measured with a
different instrument is not a green. This generalises: any tool that gates must be
version-pinned in a place `make` and CI both read.

## 6. Build targets

### 6.1 What ships

| Target | Audience |
|---|---|
| `linux/amd64` | Servers, VPS, most self-hosting |
| `linux/arm64` | Raspberry Pi, ARM cloud instances |
| `darwin/arm64` | Apple Silicon desktop |
| `darwin/amd64` | Intel Mac desktop — **and the project's primary development host** |
| Container image | Multi-arch `amd64` + `arm64`, from `docker/Dockerfile.heavy` |

### 6.2 What was dropped, and why

Three inventories disagreed **[VERIFIED]**: `.goreleaser.yaml` shipped 3 binaries,
`Makefile::build-all` built 9, `docker/` held 5 Dockerfiles. Six targets were built and
never released.

**Windows** — descoped. Not a judgement that Windows is unimportant; a judgement that
half-support is the worst state. The Windows sandbox backend exists (Job Objects,
Restricted Tokens, DACL), `GOOS=windows` compiles, no CI job exercises it, no binary
ships. Revisit as its own decision with a CI leg, or delete the backend.

**`linux/arm` (v6), `linux/armv7`** — superseded by `arm64`.

**`loong64`, `riscv64`, `mipsle`** — no evidence of users, real code cost. `mipsle`
alone forces a Matrix-less build (`GOFLAGS_NO_GOOLM`) and contributes to the constraint
`!lite && !mipsle && !netbsd && !(freebsd && arm)` **[VERIFIED]**, which every
contributor must reason about to serve a platform nobody has requested.

**The `lite` variant** — saved ~58 MB by dropping whatsmeow, was never published, and
spread `//go:build !lite` across 36 files **[VERIFIED]**. The saving reached no one.

**Four Dockerfiles** — five ways to package one binary is four too many.
`Dockerfile.heavy` survives because a container is where a missing dependency is most
painful: it carries Node.js 24 for MCP servers and the browser dependencies.

### 6.3 Reversal: Intel Mac returns

`.goreleaser.yaml` excluded `darwin/amd64` **[VERIFIED]**, on two grounds that have
both expired:

1. *"Deferred to v0.1.1"* — this **is** v0.1.1.
2. *"Haven't smoke-tested it in CI"* — Intel Mac is the most heavily exercised platform
   in the project. This host is `darwin/amd64` **[VERIFIED]**; the full `pkg/agent`
   suite, every mutation test, every `golangci-lint` run and the seccomp analysis
   during the 2026-08-22 work all ran on it.

The exclusion's *mechanism* remains true and becomes an ordering constraint:
`macos-latest` is Apple Silicon, so **no CI job has ever run on Intel Mac**. The
`macos-13` leg must land **before** the exclusion is removed, or we release a binary no
automated test has touched.

## 7. Consequences

### 7.1 Cost

Net runner cost is roughly neutral: one Mac leg added, five cross-compile targets and
one weekly pipeline removed.

One measured inefficiency dominates everything else and must be fixed first: **`npm ci`
runs in 11 of 20 pull-request jobs** **[VERIFIED]**, each installing ~1,018 packages,
measured at ~5 minutes cold. Until a single `setup` job installs once and shares the
result, tiering buys little.

### 7.2 Open items this design depends on

- **#635** — the stale traversal test. Either extend the existing `escapesByDesign`
  flag to match ADR-062, or amend ADR-062 to special-case literal `../` escapes. Until
  resolved, `cross-platform` cannot go green, and until it is green it cannot block.
- **#637** — `evals-nightly`. Fix or delete; leaving it red is not an option.
- **#636** — unrelated to CI, but filed in the same pass: delegation edges are writable
  by the agent they constrain.

### 7.3 Risks accepted

**A dropped platform's users have no binary.** Mitigated by a source build being one
Go command with no CGo. **[INFERRED]** — no telemetry exists, so this is a judgement
about likely usage, not a measurement.

**Windows sandbox code becomes unowned.** It stays compiling, untested and unshipped.
This ADR does not delete it; it records that a later decision must adopt or remove it.

**Change detection could hide a real failure.** Mitigated by the L3 rejection, fail-open
behaviour, and running unscoped on the merge to a release branch.

## 8. What this design refuses

- **No baselines or ratchets.** A suppression file seeded from today's failures makes
  red green without fixing anything.
- **No `continue-on-error` on a gate.** A check either blocks or is reporting.
  Informational must be structural.
- **No heuristic test selection.** Rejected on the evidence in §4.2.
- **No trigger-level `paths-ignore`** for required checks.
- **No green measured with an unpinned tool.** See §5.

## 9. Note on the container image's sandbox default

`docker/Dockerfile.heavy` sets `ENV OMNIPUS_SANDBOX_MODE=permissive` **[VERIFIED]**.
Defensible — the container boundary is the confinement — but it means the shipped image
runs with kernel-level confinement disabled by default, and it compounds with ADR-062's
reads-open model: inside the image, both layers are permissive. Recorded here so the
choice is explicit rather than inherited.

## 10. Implementation order

Trust before speed. Optimising a suite nobody believes optimises the wrong thing.

| # | Change | Blocked by |
|---|---|---|
| 1 | Add `push: [release/**, main]` to `pr.yml` | — |
| 2 | Pin `golangci-lint` version for local use (§5) | — |
| 3 | Single `npm ci` job with shared artifact (§7.1) | — |
| 4 | Add the `macos-13` leg to `cross-platform.yml` | — |
| 5 | Remove the `darwin/amd64` `ignore` block | step 4 |
| 6 | Resolve the stale traversal test | #635 decision |
| 7 | `cross-platform` green → promote to blocking | steps 4, 6 |
| 8 | Fix or delete `evals-nightly` | #637 |
| 9 | Reduce `Makefile::build-all` to four targets | — |
| 10 | Delete `build-lite`, the `lite` tag, `lite-build-weekly.yml` | — |
| 11 | Delete four Dockerfiles; publish `Dockerfile.heavy` multi-arch | — |
| 12 | Ship L1 language-split filtering | — |
| 13 | Ship L2 `go list -deps` selection, fail-open | two weeks green |
| 14 | Follow-up: remove `!lite`, `!mipsle`, `!netbsd`, `!(freebsd && arm)` | step 10 |
