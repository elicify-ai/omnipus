# Feature Specification: External-Executor CLI Path Detection, Prefill & Validation

- **Source ADR:** [ADR-030](../architecture/ADR-030-external-executor-cli-path-detection.md)
- **Branch:** `feat/external-executor-cli-path-detection`
- **Priority:** P1 · **Area:** Agent System
- **Scope:** External-executor mechanism only (ADR §1–9). The companion datepicker→shadcn decision (ADR §10) is an **independent frontend scope** and gets its own plan-spec — explicitly out of scope here.
- **Status:** Draft (Phase-1 gate proceeded on recommended defaults while the operator was away — see Assumptions §A-1/A-2).

## Overview

`subagent_3p` agents delegate to an external CLI (`claude-code`, `codex`, `opencode`) spawned at `ExecutorConfig.cli_path`. Today (1) detection computes the path via `exec.LookPath` then discards it, returning only booleans; (2) the SPA "prefill" is a static placeholder; (3) validation is only reachable after the agent is saved. This feature makes detection return the real path (searching `$PATH` **and** well-known install locations), prefills it per selected CLI, and validates at create-time that the CLI runs there — while preserving manual override.

## Post-Grill Revisions (Round 1)

`/grill-spec` returned **BLOCK** (review: `external-executor-cli-path-detection-spec-review.md`, 1 CRITICAL / 7 MAJOR / 6 MINOR). This section records the resolutions; the normative body below has been **edited in place** to match — no residual contradictions (Round-2 G2-02/G2-03 fixed). Round-2 additions (RBAC gating, target constraints, per-reason fields, identity matchers) are folded in here and into the body. New requirements: FR-013…FR-018.

### Design decisions (mirrored in ADR §11)
- **F-01 (CRITICAL) — validate hardening.** `POST /system/cli-validate` MUST apply `withRateLimit` and MUST audit the executed-path event `{cli, resolved_path, reason}`. Detection stays unaudited (no subprocess). Amends the Non-Behaviors ("must not audit" now applies to **detection only**) and NFR-3.
- **F-02 — detect↔runtime symmetry.** Prefill MUST write the **absolute** detected path; validation and the runtime spawn resolve the exact `cli_path`. **D-3.1 corrected:** empty `cli_path` → `missing-binary` (NOT "$PATH default"). Guarantees detected == runs.
- **F-03 — CLI identity.** The handshake MUST verify the `--version` output matches a per-CLI identity matcher; a wrong binary (e.g. `/usr/bin/node` under `claude-code`) → `handshake-failed`. **Corrects US-3 AC-2 / D-2.2.**
- **F-06 — HOME-unset.** The scan MUST tolerate `os.UserHomeDir()` failure (passwd fallback or skip `~` candidates), so it works under systemd with `HOME` unset.

### Corrections
- **F-04 — enum + schemas.** Canonical `source` value is **`"path"`** (lowercase) everywhere; all BDD/D-1/UX uses of `"PATH"` are corrected to `"path"`. Wire schemas defined below.
- **F-05 — `ok` mapping.** `runner.ReasonOK` is the empty string; the endpoint MUST map it to `reason:"ok"`. **FR-008 corrected:** gate Create on `reason ∈ {missing-binary, handshake-failed}`, never on raw `!ok` (which would wrongly block `unauthenticated`).
- **F-07 — concrete list is acceptance, not a spike.** The per-OS candidate dirs (below) are part of the spec. **SC-001 corrected** to assert detection of a **real** binary installed to `~/.local/bin` and removed from `$PATH` (integration test), not injected fakes.
- **F-08 — response hygiene.** `CliValidateResponse.detail` is a fixed, classified message per reason — **never raw stderr**. `resolved_path` is returned (`withAuth`-only).
- **Minors (F-09…F-17).** Scan uses a `LookPath`-consistent executable-eligibility check (mode/`PATHEXT`), not bare `os.Stat`; Windows cases are table-driven so they run on Linux CI; `~/.nvm/versions/node/*` selects the **highest** version; validate-on-blur is **debounced ≥400 ms** and cancels in-flight; the SPA tolerates unknown `source`/`reason` values (stale-bundle safety).

### Wire schemas (contract-first — Constraint #8)
```yaml
CliDetect:            { claude: CliDetectEntry, codex: CliDetectEntry, opencode: CliDetectEntry }   # required all 3
CliDetectEntry:       { installed: bool, path: string|null (absolute), source: enum[path, well-known]|null }
CliValidateRequest:   { cli: enum[claude-code, codex, opencode], cli_path: string }                 # required both
CliValidateResponse:  { ok: bool, reason: enum[ok, missing-binary, handshake-failed, unauthenticated, unknown-cli],
                        resolved_path: string|null, version: string|null, detail: string (classified, no stderr) }
```

### Per-reason response fields, identity matchers, blocking rule
| reason | ok | resolved_path | version | detail |
|---|---|---|---|---|
| ok | true | absolute resolved path | version string | "OK" |
| unauthenticated | true | absolute resolved path | version string | "installed; not logged in" |
| missing-binary | false | null | null | "not found" (never the raw path stderr) |
| handshake-failed | false | resolved path (if any) | null | "did not identify as \<cli\>" |
| unknown-cli | false | null | null | "unsupported cli" |

- **Blocking keys off `reason` only** (never raw `!ok`): block on `missing-binary`/`handshake-failed`; warn on `unauthenticated`; allow `ok`. (`ok` is retained for convenience but MUST NOT drive gating — G2-07.)
- **Per-CLI identity matcher** (case-insensitive, applied to the `--version` banner; exact tokens to be confirmed against real CLI output — spike A-3): `claude-code`→contains `claude`; `codex`→contains `codex`; `opencode`→contains `opencode`. On mismatch → `handshake-failed` (G2-04).
- **`EvalSymlinks` error** (dangling link): fall back to `Abs` of the raw hit, still `installed:true` (G2-11). **`source`** reflects *how located* (PATH lookup vs dir scan), independent of resolved location (G2-12). Validate is intentionally **stricter** than runtime (fail-closed at create), not a symmetry guarantee (G2-13).

### New functional requirements
- **FR-013**: `cli-validate` MUST be gated `withAuth → RequireAdmin`, apply a **dedicated** rate limiter, **reject non-regular / non-executable target files before spawn**, **cap concurrent in-flight validations (≤2)**, and emit an audit event `{cli, resolved_path, reason}` per call. `[F-01 / G2-01]`
- **FR-014**: Prefill MUST be absolute; validation + runtime resolve exactly `cli_path`; empty `cli_path` MUST classify `missing-binary`. `[F-02]`
- **FR-015**: The handshake MUST confirm `--version` output matches a per-CLI identity matcher; non-match → `handshake-failed`. `[F-03]`
- **FR-016**: The well-known scan MUST tolerate `os.UserHomeDir()` failure without erroring. `[F-06]`
- **FR-017**: `cli-validate` responses MUST carry a classified `detail`, never raw stderr. `[F-08]`
- **FR-018**: The endpoint MUST map `ReasonOK` ("") → `reason:"ok"`; the SPA MUST gate Create on `reason ∈ {missing-binary, handshake-failed}`. `[F-05]`

### Per-OS well-known candidate dirs (acceptance)
- **Linux:** `/usr/local/bin`, `/usr/bin`, `~/.local/bin`, `~/.npm-global/bin`, newest `~/.nvm/versions/node/*/bin`, `~/.bun/bin`, `~/.deno/bin`, `~/.cargo/bin`, `/snap/bin`
- **macOS:** `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, + npm/nvm/bun as above
- **Windows:** `%APPDATA%\npm` (`.cmd`/`.exe` via `PATHEXT`), `%LOCALAPPDATA%\Programs\<tool>`, `%ProgramFiles%\...`

### Added BDD / dataset rows
- BDD (Error Path, Traces US-3): **Wrong binary rejected** — `claude-code` → `/usr/bin/node` → `handshake-failed` `[FR-015]`.
- BDD (Error Path, Traces US-4): **Empty cli_path is missing** — empty → `missing-binary` `[FR-014]`.
- BDD (Edge, Traces US-2): **HOME unset** — scan uses passwd home, still finds well-known binaries `[FR-016]`.
- BDD (Error Path, Traces US-3): **validate is rate-limited** — N rapid calls → 429 after the limit `[FR-013]`.
- **D-2.6**: `cli_path=/usr/bin/node`, `cli=claude-code` → `handshake-failed`. **D-3.1 corrected**: empty → `missing-binary`.

---

## Actors

| Actor | Role |
|---|---|
| Operator | Creates/edits a `subagent_3p` agent via the SPA wizard (`Step1Identity`) and edit form (`AgentProfile`). |
| Gateway | Runs detection (`GET /system/cli-detect`) and stateless validation (`POST /system/cli-validate`). |
| External CLI | `claude` / `codex` / `opencode` binary; probed for presence and `--version`. |

## Problem, Scope

**In scope:** path-returning detection; per-OS well-known-location scanning; per-CLI prefill in wizard + edit form; stateless create-time validation reusing `runner.TestConnectionWithPath`; `CliDetect` wire restructure + new `CliValidate` schemas; SPA state/validation UX.

**Out of scope:** datepicker→shadcn (ADR §10, separate spec); changing the spawn/run path; changing `cli` (locked after create); auth/login flows for the CLIs; `remote-a2a` executor kind.

## Available Reference Patterns

No `docs/reference/` implementation library applies. The in-repo reusable pattern is **`pkg/agent/runner/conntest.go`** (`TestConnectionWithPath`) — the existing missing-binary → `--version` handshake → unauthenticated classifier. The new validate endpoint MUST reuse it, **extended** with a per-CLI identity matcher (FR-015) and an empty-path guard (FR-014), rather than re-implement classification. `[FACT]`

## Existing Codebase Context

### Symbols Involved
| Symbol | Role | Context |
|---|---|---|
| `restAPI.HandleSystemCliDetect` (`pkg/gateway/rest.go:2072`) | modify | Discards `LookPath` path (`:2078`); returns booleans. Extend to return resolved path + source. |
| `cliProbeLookPath` (`pkg/gateway/rest.go:2060`) | modify | Swappable `exec.LookPath`. Replaced by a `pkg/clidetect` call. |
| `gen.CliDetect` (`contracts/components/schemas/CliDetect.yaml`) | modify | Booleans → per-CLI `{installed, path, source}`. Contract-first regen. |
| `pkg/clidetect` (new) | create | Pure-Go detector: LookPath → per-OS well-known scan. |
| `runner.TestConnectionWithPath` (`pkg/agent/runner/conntest.go:146`) | call + extend | Reused and extended with a per-CLI identity matcher (FR-015); `runner-test` also gains identity checking (regression note). |
| `restAPI.testAgentRunner` (`pkg/gateway/rest.go:1189`) | reference | Existing agent-scoped consumer of `TestConnectionWithPath`; unchanged. |
| `gen.CliValidateRequest/Response` (new schemas) | create | `{cli, cli_path}` → `{ok, reason, resolved_path, version, detail}`. |
| `Step1Identity.tsx:274-276` | modify | Path input: prefill from detect; add validate-on-blur state. |
| `AgentProfile.tsx` (cli_path input) | modify | Same prefill + validate on the edit form (`cli_path` is mutable). |
| `AgentListScreen.tsx` | modify | Consumer of `CliDetect` (CLI availability greying); update for the restructured shape. |
| `src/lib/api.ts` (`fetchCliDetect`, `CliDetectSchema`) | modify | Fetch wrapper + zod for `CliDetect`; update to per-CLI objects. |
| `src/lib/api.cli-detect.test.ts` | modify | Asserts the old boolean `CliDetect` shape; update to per-CLI objects. |

### Impact Assessment
| Symbol Modified | Risk | Direct Dependents (d=1) | Indirect (d=2) |
|---|---|---|---|
| `CliDetect` schema | MEDIUM | `AgentListScreen.tsx` (enable/disable CLIs), generated Go/TS/zod types | Wizard CLI availability greying |
| `HandleSystemCliDetect` | LOW | `GET /system/cli-detect` callers (SPA only) | — |
| `Step1Identity.tsx` | LOW | Create-agent wizard | Agent creation flow |
| `AgentProfile.tsx` | LOW | Agent edit form | `cli_path` edit flow |
| new `pkg/clidetect` | LOW | detect handler; validate handler (indirectly) | — |

### Relevant Execution Flows
| Flow | Relevance |
|---|---|
| Create `subagent_3p` (wizard `Step1Identity` → `POST /agents`) | Where prefill + create-time validation occur. |
| Edit `subagent_3p` (`AgentProfile` → `PUT /agents/{id}`) | `cli_path` mutable → prefill + validate here too. |
| Roster screen CLI availability (`AgentListScreen` → `GET /system/cli-detect`) | Consumes restructured `CliDetect`. |
| Runner connection test (`POST /agents/{id}/runner-test`) | Existing; unchanged — shares `TestConnectionWithPath` with the new endpoint. |

## User Stories

### US-1 — Auto-detect & prefill the CLI path (P1)
As an operator creating a `subagent_3p`, when I pick a CLI, its real installed path is prefilled so I don't have to know or type it.
**Why this priority:** the core defect; without it the operator saves a wrong/empty path that fails at first run.
**Independent test:** with `claude` on the host, open the wizard, select `claude-code`, observe the path field prefilled with the resolved absolute path and a "Detected" indicator.
**Acceptance Scenarios:**
1. **Given** `claude` is on the gateway `$PATH`, **When** the operator selects `claude-code`, **Then** the path field is prefilled with the resolved absolute path and marked detected (source: PATH).
2. **Given** the path field already holds a user value, **When** the operator switches CLI, **Then** prefill MUST NOT overwrite the existing non-empty value.
3. **Given** a CLI is not found anywhere, **When** selected, **Then** the field stays empty with an amber "not found — enter manually" hint.

### US-2 — Detect CLIs installed outside the gateway `$PATH` (P1)
As an operator running the gateway as a service (minimal `$PATH`) with CLIs installed via npm-global/Homebrew/`~/.local/bin`, detection still finds them.
**Why this priority:** the true root cause (NFR-1) — `LookPath` alone yields false negatives.
**Independent test:** install `codex` only in `~/.local/bin` (not on the gateway `$PATH`); detection returns its path with source `well-known`.
**Acceptance Scenarios:**
1. **Given** `codex` is absent from `$PATH` but present in a well-known dir, **When** detection runs, **Then** it returns that path with source `well-known`.
2. **Given** `claude` is on both `$PATH` and a well-known dir, **When** detection runs, **Then** the `$PATH` result wins (source `PATH`).
3. **Given** a symlinked binary (Homebrew/npm shim), **When** detected, **Then** the returned path is resolved (`EvalSymlinks` + absolute).

### US-3 — Validate the CLI at create-time (P1)
As an operator, before saving I get told whether the CLI actually runs at the given path.
**Why this priority:** prevents persisting a broken configuration.
**Independent test:** enter a bogus path → validate returns `missing-binary` and Create is blocked; enter a real path → `ok` and Create is enabled.
**Acceptance Scenarios:**
1. **Given** a path with no binary, **When** validation runs, **Then** it returns `missing-binary` and Create is **blocked** with the reason.
2. **Given** a path to a non-CLI/incompatible binary, **When** validated, **Then** it returns `handshake-failed` and Create is **blocked**.
3. **Given** a valid binary with no credentials, **When** validated, **Then** it returns `unauthenticated`, shows a **non-blocking** amber warning, and Create is **allowed**.
4. **Given** a valid, authenticated binary, **When** validated, **Then** it returns `ok` with the reported version and Create is enabled.

### US-4 — Preserve manual override (P1)
As an operator, I can always type/correct the path regardless of detection.
**Why this priority:** detection can miss exotic installs; the field must remain authoritative to the operator.
**Independent test:** type a custom path over a detected one; it persists and is what gets validated/saved.
**Acceptance Scenarios:**
1. **Given** a detected path, **When** the operator edits it, **Then** the edited value is what is validated and saved.
2. **Given** an empty detection, **When** the operator types a valid path, **Then** validation runs against the typed path.

### US-5 — Prefill & validate on the edit form (P2)
As an operator editing an existing `subagent_3p`, the same detect/prefill/validate applies because `cli_path` is mutable.
**Why this priority:** `cli` is locked but `cli_path` is mutable (`ExecutorConfig.yaml:48-58`); upgrades/relocations happen post-create.
**Independent test:** open an existing external agent's profile, clear the path → it re-prefills from detection; edit → validate-on-blur runs.
**Acceptance Scenarios:**
1. **Given** an existing external agent, **When** its profile opens with an empty `cli_path`, **Then** the detected path is offered.
2. **Given** the operator edits `cli_path`, **When** the field blurs, **Then** validation runs and shows the result.

### US-6 — Cross-platform detection (P2)
As an operator on Linux, macOS, or Windows, detection uses the right binary names and well-known locations for my OS.
**Why this priority:** the gateway targets all three; install conventions differ.
**Independent test:** on Windows, an npm-installed `claude` (shim `claude.cmd`) is detected via `PATHEXT`.
**Acceptance Scenarios:**
1. **Given** Windows with an npm `claude.cmd` shim, **When** detected, **Then** it is found (LookPath honours `PATHEXT`).
2. **Given** macOS Apple Silicon with Homebrew, **When** detecting a Homebrew-installed CLI, **Then** `/opt/homebrew/bin` is scanned.

### Edge Cases
- Binary present but **not executable** (permission) → `missing-binary`/`handshake-failed` (LookPath treats non-executable as not found).
- `cli_path` points to a **directory**, not a file → treated as not-found.
- **Multiple installs** (npm + brew): first candidate-dir hit wins; value is editable (Assumption A-2).
- Path with **spaces / unicode** → handled via `execve` arg (no shell), not string-split.
- Detection while the **gateway `$PATH` is empty** → falls back entirely to the well-known scan.
- Validation **timeout** (`--version` hangs) → `handshake-failed` after 15s (existing `versionProbeTimeout`).
- Unknown `cli` value sent to validate → `unknown-cli` (400-class, no spawn).

## Behavioral Contract

- When a CLI is selected and the path field is empty, the system prefills the detected absolute path (or leaves it empty with a "not found" hint).
- When a CLI is found on `$PATH` and in a well-known dir, the system prefers the `$PATH` result.
- When the path field is non-empty, the system never overwrites it on CLI (re)selection.
- When the operator requests/triggers validation, the system returns exactly one classification: `ok` | `missing-binary` | `handshake-failed` | `unauthenticated` | `unknown-cli`.
- When validation is `missing-binary` or `handshake-failed`, the system blocks Create/Save.
- When validation is `unauthenticated`, the system warns but allows Create/Save.
- When detection runs, the system spawns no subprocess; when validation runs, the system spawns only `<cli> --version`.

## Explicit Non-Behaviors

- The system must not run any real CLI task or spend model tokens during detection or validation, because these are read-only diagnostics.
- The system must not shell out to `which`/`where` or any shell, because Constraint #2 requires pure-Go, no-shell execution.
- The system must not silently write a detected path without showing it in an editable field, because the operator must be able to see and correct it (US-4).
- The system must not overwrite a non-empty operator-entered path on re-detection, because manual override is authoritative.
- The system must not block Create on `unauthenticated`, because login is a separate later step and `cli_path` presence is what "installed" means here.
- The system must not audit-log **detection** (`cli-detect`) calls, because they spawn no subprocess and are read-only. **Validation** (`cli-validate`) MUST be audited and rate-limited (FR-013) because it spawns a caller-supplied path.
- The system must not change the `cli` field on edit, because it is locked after create.

## Integration Boundaries

**External CLI (`claude` / `codex` / `opencode`)**
- Data in: the resolved binary path + `--version` args. Data out: exit code + version string (or spawn error).
- Contract: `<binary> --version` prints a version and exits 0 within 15s.
- Failure behavior: not found → `missing-binary`; runs-but-no-version / non-zero / timeout → `handshake-failed`; version OK but no creds → `unauthenticated`.
- Dev approach: real binaries in integration/e2e where installed; unit tests inject a fake `LookPath` and a fake exec/creds probe (as `cliProbeLookPath` already allows).

**Gateway endpoints (SPA ↔ gateway)**
- `GET /system/cli-detect` → `CliDetect { claude|codex|opencode: {installed, path, source} }`.
- `POST /system/cli-validate` `{cli, cli_path}` → `CliValidate { ok, reason, resolved_path, version?, detail }`.
- `cli-detect`: `withAuth`, read-only, unaudited. `cli-validate`: `withAuth` → `RequireAdmin`, dedicated rate limiter, audited, rejects non-regular/non-executable targets before spawn, and caps concurrent in-flight validations (FR-013).

## BDD Scenarios

```gherkin
Feature: External-executor CLI path detection

  # Traces to: US-1 AC-1  (Happy Path)
  Scenario: Prefill from a CLI found on PATH
    Given the "claude" binary resolves on the gateway PATH to "/usr/local/bin/claude"
    When the operator selects the "claude-code" CLI in the wizard
    Then the path field is prefilled with "/usr/local/bin/claude"
    And the field shows a "Detected (PATH)" indicator

  # Traces to: US-1 AC-2  (Edge Case)
  Scenario: Prefill never clobbers an existing value
    Given the path field already contains "/opt/custom/claude"
    When the operator re-selects the "claude-code" CLI
    Then the path field still contains "/opt/custom/claude"

  # Traces to: US-2 AC-1  (Happy Path)
  Scenario Outline: Detect a CLI outside PATH via well-known locations
    Given "<binary>" is absent from the gateway PATH
    And "<binary>" exists at "<wellknown>"
    When detection runs for "<cli>"
    Then it returns path "<wellknown>" with source "well-known"

    Examples:
      | cli         | binary   | wellknown                    |
      | claude-code | claude   | /home/dev/.local/bin/claude  |
      | codex       | codex    | /opt/homebrew/bin/codex      |
      | opencode    | opencode | /home/dev/.npm-global/bin/opencode |

  # Traces to: US-2 AC-2  (Alternate Path)
  Scenario: PATH result wins over well-known
    Given "claude" resolves on PATH to "/usr/bin/claude"
    And "claude" also exists at "/opt/homebrew/bin/claude"
    When detection runs for "claude-code"
    Then it returns path "/usr/bin/claude" with source "PATH"

  # Traces to: US-2 AC-3  (Edge Case)
  Scenario: Symlinked binary resolves to an absolute real path
    Given "codex" on PATH is a symlink to "/opt/homebrew/Cellar/codex/1.2/bin/codex"
    When detection runs for "codex"
    Then the returned path is the absolute resolved target

  # Traces to: US-3 AC-1  (Error Path)
  Scenario: Missing binary blocks Create
    Given the operator enters cli_path "/nope/claude"
    When validation runs for "claude-code"
    Then the reason is "missing-binary"
    And the Create action is blocked

  # Traces to: US-3 AC-2  (Error Path)
  Scenario: Non-CLI binary fails the handshake and blocks Create
    Given cli_path points to a binary that does not print a version
    When validation runs
    Then the reason is "handshake-failed"
    And the Create action is blocked

  # Traces to: US-3 AC-3  (Alternate Path)
  Scenario: Unauthenticated warns but allows Create
    Given a valid "claude" binary with no credentials available
    When validation runs for "claude-code"
    Then the reason is "unauthenticated"
    And a non-blocking warning is shown
    And the Create action is allowed

  # Traces to: US-3 AC-4  (Happy Path)
  Scenario: Valid authenticated binary passes
    Given a valid, authenticated "opencode" binary at the given path
    When validation runs for "opencode"
    Then the reason is "ok"
    And the reported version is populated

  # Traces to: US-4 AC-1  (Alternate Path)
  Scenario: Manual override is what gets validated
    Given a detected path "/usr/local/bin/claude"
    When the operator overwrites it with "/opt/x/claude"
    And validation runs
    Then validation targets "/opt/x/claude"

  # Traces to: US-5 AC-2  (Happy Path)
  Scenario: Validate-on-blur on the edit form
    Given an existing subagent_3p profile is open
    When the operator edits cli_path and the field blurs
    Then validation runs and the result is shown inline

  # Traces to: US-6 AC-1  (Edge Case)
  Scenario: Windows npm shim detected via PATHEXT
    Given the OS is Windows and "claude.cmd" exists in the npm dir
    When detection runs for "claude-code"
    Then it is found

  # Traces to: US-3 (Error Path — boundary)
  Scenario: Unknown CLI is rejected without spawning
    Given a validate request with cli "gemini-cli"
    When the endpoint processes it
    Then the reason is "unknown-cli"
    And no subprocess is spawned
```

## Test-Driven Development Plan

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | `TestDetect_FoundOnPath` | Unit | Prefill from a CLI found on PATH | LookPath hit → {installed, path, source=PATH}. |
| 2 | `TestDetect_WellKnownFallback` | Unit | Detect via well-known locations | PATH miss → scan candidate dirs → source=well-known. |
| 3 | `TestDetect_PathWinsOverWellKnown` | Unit | PATH result wins over well-known | Ordering precedence. |
| 4 | `TestDetect_SymlinkResolved` | Unit | Symlinked binary resolves | EvalSymlinks + Abs. |
| 5 | `TestDetect_Absent` | Unit | (US-1 AC-3) | Not found anywhere → {installed:false, path:""}. |
| 6 | `TestDetect_WindowsPathext` | Unit | Windows npm shim detected | `.cmd` via PATHEXT (build-tagged/table). |
| 7 | `TestDetect_MultipleInstalls_FirstHit` | Unit | (Edge: multiple installs) | Ordered first hit returned. |
| 8 | `TestValidate_MissingBinary` | Unit | Missing binary blocks Create | Reuses TestConnectionWithPath → missing-binary. |
| 9 | `TestValidate_HandshakeFailed` | Unit | Non-CLI fails handshake | Fake exec: no version → handshake-failed. |
| 10 | `TestValidate_Unauthenticated` | Unit | Unauthenticated warns | Version OK, no creds → unauthenticated. |
| 11 | `TestValidate_OK` | Unit | Valid authenticated binary | ok + version populated. |
| 12 | `TestValidate_UnknownCLI` | Unit | Unknown CLI rejected | No spawn; unknown-cli. |
| 13 | `TestCliDetectContract` | Integration | (schema) | `gen.CliDetect` JSON round-trips per contract test. |
| 14 | `TestCliValidateContract` | Integration | (schema) | `gen.CliValidate*` JSON valid per contract test. |
| 15 | `TestHandleSystemCliDetect_ReturnsPaths` | Integration | Prefill scenarios | Endpoint returns per-CLI {installed,path,source}. |
| 16 | `TestHandleSystemCliValidate_Endpoint` | Integration | Validate scenarios | Stateless endpoint delegates to conntest; classifications map. |
| 17 | `AgentListScreen.cliDetect.test` | Unit (vitest) | (regression) | Consumes restructured CliDetect; greys unavailable CLIs. |
| 18 | `Step1Identity.prefillValidate.test` | Unit (vitest) | US-1/US-3/US-4 | Prefill-on-select, no-clobber, validate-on-blur, block rules. |
| 19 | `AgentProfile.cliPathValidate.test` | Unit (vitest) | US-5 | Edit-form prefill + validate-on-blur. |
| 20 | `external-executor-create.e2e` | E2E | US-1+US-3 happy | Wizard: select CLI → prefilled → validate ok → create. |
| 21 | `TestValidate_AdminOnly` | Integration | validate admin-only | Non-admin `user` → 403; non-regular/non-executable target rejected. |
| 22 | `TestValidate_RateLimited` | Integration | validate rate-limited | Past the dedicated limiter → 429; concurrency cap holds. |
| 23 | `TestValidate_AuditEmitted` | Integration | audit | One audit event `{cli, resolved_path, reason}` per call. |
| 24 | `TestValidate_WrongBinaryIdentity` | Unit | identity | `/usr/bin/node` under claude-code → handshake-failed. |
| 25 | `TestValidate_EmptyPathMissing` | Unit | empty→missing | Empty cli_path short-circuits to missing-binary. |
| 26 | `TestDetect_HomeUnset` | Unit | HOME unset | `os.UserHomeDir` error → passwd fallback / skip ~ gracefully. |
| 27 | `TestValidate_DetailSanitized` / `TestValidate_ReasonOKMapping` | Unit | detail/ok | detail carries no raw stderr; `ReasonOK` ("") → "ok". |

### Test Datasets

**Dataset D-1 — Detection (per-OS)**
| # | OS | `$PATH` has binary | Well-known has binary | Symlink | Expected `{installed, source}` | Traces to |
|---|---|---|---|---|---|---|
| D-1.1 | linux | yes (`/usr/local/bin`) | — | no | true, PATH | US-2 AC-2 |
| D-1.2 | linux | no | yes (`~/.local/bin`) | no | true, well-known | US-2 AC-1 |
| D-1.3 | linux | no | no | — | false, — | US-1 AC-3 |
| D-1.4 | linux | yes | — | yes→abs | true, PATH (resolved) | US-2 AC-3 |
| D-1.5 | macos | no | yes (`/opt/homebrew/bin`) | no | true, well-known | US-6 AC-2 |
| D-1.6 | windows | yes (`claude.cmd`) | — | — | true, PATH | US-6 AC-1 |
| D-1.7 | linux | yes AND well-known | yes | — | true, PATH (precedence) | US-2 AC-2 |

**Dataset D-2 — Validation classification**
| # | Binary state | Creds | Expected `reason` | Create allowed? | Traces to |
|---|---|---|---|---|---|
| D-2.1 | absent | — | missing-binary | no | US-3 AC-1 |
| D-2.2 | present, no `--version` | — | handshake-failed | no | US-3 AC-2 |
| D-2.3 | present, runs | none | unauthenticated | yes (warn) | US-3 AC-3 |
| D-2.4 | present, runs | present | ok | yes | US-3 AC-4 |
| D-2.5 | n/a (cli="gemini-cli") | — | unknown-cli | no (no spawn) | Unknown-CLI scenario |

**Dataset D-3 — Path input boundaries**
| # | cli_path value | Expected | Traces to |
|---|---|---|---|
| D-3.1 | "" (empty) | missing-binary (handler short-circuits before conntest) | US-4 AC-2 / FR-014 |
| D-3.2 | "/opt/x/claude" (spaces-free abs) | validated as-is | US-4 AC-1 |
| D-3.3 | "/opt/my apps/claude" (space) | execve arg, not split | Edge: spaces |
| D-3.4 | "/opt/x/" (directory) | missing-binary | Edge: directory |
| D-3.5 | "  /usr/bin/claude  " (whitespace) | trimmed then validated | Edge: trim |

### Regression Test Requirements

Modifies existing functionality (the `CliDetect` wire shape + `HandleSystemCliDetect`).
1. Behaviours to preserve: the roster screen must still grey-out CLIs the host cannot run; `POST /agents/{id}/runner-test` must be unchanged.
2. Existing tests to update: `HandleSystemCliDetect` unit tests (new shape), `AgentListScreen` + `src/lib/api.cli-detect.test.ts` (restructured `CliDetect`), and `conntest`/`runner-test` tests (now identity-checked).
3. New regression tests: `AgentListScreen.cliDetect.test` against the restructured `CliDetect`; a contract test asserting the old boolean consumers are fully migrated.
4. Regression dataset: reuse D-1 to assert "installed" truthiness matches the old boolean semantics (installed==true iff old hasX==true).

## Functional Requirements

- **FR-001**: Detection MUST return, per CLI, `{installed, path, source}` where `path` is absolute (symlinks resolved) when installed. `[US-1, US-2]`
- **FR-002**: Detection MUST search the gateway `$PATH` first, then a curated per-OS well-known-location list, and report `source` accordingly. `[US-2]`
- **FR-003**: Detection MUST map the executor `cli` to its binary (`claude-code`→`claude`, `codex`→`codex`, `opencode`→`opencode`) and honour OS executable extensions (`PATHEXT` on Windows). `[US-6]`
- **FR-004**: Detection MUST NOT spawn a subprocess or shell out. `[Non-Behaviors]`
- **FR-005**: The SPA MUST prefill the detected `path` into the path field when it is empty, per selected CLI, and MUST NOT overwrite a non-empty value. `[US-1, US-4]`
- **FR-006**: A stateless endpoint `POST /system/cli-validate {cli, cli_path}` MUST return exactly one of `ok | missing-binary | handshake-failed | unauthenticated | unknown-cli`, delegating to `runner.TestConnectionWithPath`. `[US-3]`
- **FR-007**: Validation MUST spawn only `<cli> --version`, spend zero tokens, and be bounded by the existing 15s timeout. `[US-3, Non-Behaviors]`
- **FR-008**: The SPA MUST block Create/Save on `missing-binary` and `handshake-failed`, and MUST allow it (with a non-blocking warning) on `unauthenticated`. `[US-3]`
- **FR-009**: Prefill + validate MUST be available on both the create wizard (`Step1Identity`) and the edit form (`AgentProfile`). `[US-5]`
- **FR-010**: The path field MUST remain a free-text override that is authoritative for validation and save. `[US-4]`
- **FR-011**: The `CliDetect` wire change MUST be contract-first (schemas regenerated) and its sole consumer (`AgentListScreen`) migrated in the same change. `[Regression]`
- **FR-012**: An unknown `cli` sent to validate MUST be rejected without spawning any process. `[US-3 edge]`

## Success Criteria

- **SC-001**: For each of the 3 CLIs installed in a well-known location but absent from the gateway `$PATH`, detection returns a non-empty absolute path (0 false negatives across the D-1 matrix). 
- **SC-002**: 100% of the D-2 classification rows produce the expected `reason`.
- **SC-003**: Selecting a CLI with the binary present prefills the field in a single interaction (no manual typing) in ≥ the 3 supported CLIs.
- **SC-004**: Create is blocked in 100% of `missing-binary`/`handshake-failed` cases and allowed in 100% of `unauthenticated`/`ok` cases (D-2).
- **SC-005**: `make verify-contracts` passes with the new `CliDetect`/`CliValidate` schemas; no hand-written wire types.
- **SC-006**: Detection returns without spawning a subprocess (asserted by test doubles / no `--version` call in the detect path).
- **SC-007**: All 27 TDD-plan tests pass; `tsc -b`, vitest, `CGO_ENABLED=0 go test -tags goolm,stdjson`, and `make verify-contracts` green.
- **SC-008**: `cli-validate` returns 403 for a non-admin `user` role and 429 past its dedicated rate limit. `[FR-013]`
- **SC-009**: Each `cli-validate` call emits exactly one audit event `{cli, resolved_path, reason}`. `[FR-013]`
- **SC-010**: `claude-code` pointed at `/usr/bin/node` yields `handshake-failed` (identity matcher). `[FR-015]`
- **SC-011**: With `HOME` unset, detection still resolves a binary in a passwd-home well-known dir. `[FR-016]`
- **SC-012**: `cli-validate.detail` contains no substring of the process stderr (classified message only). `[FR-017]`

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-1, US-2 | Prefill from PATH; well-known; symlink | TestDetect_FoundOnPath, _WellKnownFallback, _SymlinkResolved |
| FR-002 | US-2 | well-known; PATH-wins | TestDetect_WellKnownFallback, _PathWinsOverWellKnown |
| FR-003 | US-6 | Windows PATHEXT | TestDetect_WindowsPathext |
| FR-004 | — (Non-Behaviors) | (all detect) | TestDetect_* (no-spawn assertion), SC-006 |
| FR-005 | US-1, US-4 | Prefill; no-clobber; override | Step1Identity.prefillValidate.test |
| FR-006 | US-3 | missing/handshake/unauth/ok/unknown | TestValidate_*, TestHandleSystemCliValidate_Endpoint |
| FR-007 | US-3 | (validation scenarios) | TestValidate_OK, TestValidate_Unauthenticated |
| FR-008 | US-3 | blocks/allows | Step1Identity.prefillValidate.test, external-executor-create.e2e |
| FR-009 | US-5 | edit-form validate-on-blur | AgentProfile.cliPathValidate.test |
| FR-010 | US-4 | manual override validated | Step1Identity.prefillValidate.test |
| FR-011 | Regression | (schema) | TestCliDetectContract, AgentListScreen.cliDetect.test |
| FR-012 | US-3 edge | unknown CLI no spawn | TestValidate_UnknownCLI |
| FR-013 | US-3 | validate is admin-only + rate-limited + audited | TestValidate_AdminOnly, TestValidate_RateLimited, TestValidate_AuditEmitted |
| FR-014 | US-4 | empty cli_path is missing | TestValidate_EmptyPathMissing |
| FR-015 | US-3 | wrong binary rejected | TestValidate_WrongBinaryIdentity |
| FR-016 | US-2 | HOME unset | TestDetect_HomeUnset |
| FR-017 | US-3 | detail classified | TestValidate_DetailSanitized |
| FR-018 | US-3 | ReasonOK→ok; gate on reason | TestValidate_ReasonOKMapping |

## Ambiguity Warnings (self-audit)

| # | What's ambiguous | Likely agent assumption | Resolution |
|---|---|---|---|
| A-1 | Blocking rule for missing/handshake | Block Create; warn on unauthenticated | **Resolved (recommended default, operator away)** — confirm at review. |
| A-2 | Multiple installs of one CLI | First candidate-dir hit; editable | **Accepted as assumption** — surface path so operator can change. |
| A-3 | Exact well-known dir lists | ADR §6(a) lists | Test datasets encode representative dirs; confirm with a host spike. |
| A-4 | Detect memoization | Compute per request; React-Query cache on SPA | Deferred (perf, not correctness). |
| A-5 | Wire restructure vs additive | Restructure per-CLI objects (single internal consumer) | ADR §6 sub-decision (Medium); additive fallback if an external consumer appears. |

## Assumptions

- **A-1**: On `missing-binary`/`handshake-failed`, Create/Save is blocked; on `unauthenticated`, allowed with a warning. (Operator-away default; recommended.)
- **A-2**: Multiple installs → first ordered candidate wins, value editable.
- **A-3**: Detection is per-request (no backend cache); SPA caches via React Query.
- **A-4**: Scope is external-executor only; datepicker (ADR §10) is a separate spec.

## Holdout Evaluation Scenarios

> Holdout — for post-implementation verification only. NOT referenced in the TDD plan or traceability matrix.

- **H-1 (happy):** On a host where `claude` is only in `~/.local/bin` (not on the service `$PATH`), create a `claude-code` subagent purely by selecting the CLI and clicking through — the path should be correct with no manual typing.
- **H-2 (happy):** On macOS Apple Silicon with a Homebrew `opencode`, the wizard prefills `/opt/homebrew/bin/opencode`.
- **H-3 (happy):** Editing an existing agent, clear the path and confirm it re-offers the detected path.
- **H-4 (error):** Enter `/tmp/not-a-cli` → Create is blocked with a "not found" reason; no agent is created.
- **H-5 (error):** Point at a real but logged-out CLI → amber "installed, not logged in"; Create still succeeds.
- **H-6 (edge):** Rename the binary out of every known location mid-session → re-validation flips to blocked.
- **H-7 (edge):** A path containing a space is accepted, validated, and spawns correctly (no arg-splitting corruption).

## Regression Impact Summary

Modifies `CliDetect` wire + `HandleSystemCliDetect`, and **extends** `conntest`'s handshake with a per-CLI identity matcher — so `POST /agents/{id}/runner-test` also begins rejecting wrong binaries (intended strict improvement; update its tests). `CliDetect` consumers migrated in-change: `AgentListScreen.tsx`, `src/lib/api.ts` (`fetchCliDetect`/`CliDetectSchema`), `src/lib/api.cli-detect.test.ts`. No agent-loop, sandbox, or spawn-path changes.
