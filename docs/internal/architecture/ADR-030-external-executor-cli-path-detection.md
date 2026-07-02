# ADR-030: External-Executor CLI Path Detection, Prefill & Validation

- **Status:** Proposed
- **Date:** 2026-07-02
- **Deciders:** Daniel Piatkowski (product owner); Albert (architecture)
- **Evidence level (highest used):** 1 (user-provided decisions) + 2 (documented Go stdlib / OS install conventions), with `[EXPERT REASONING]` for the well-known-location lists
- **Branch:** `feat/external-executor-cli-path-detection` (off `hotfix/v0.1.1` @ `5d753b05`)
- **Also covers:** a companion **datepicker → shadcn** UI fix (§10), bundled on the same branch/PR per operator direction. Kept brief — it is an independent decision recorded here for convenience, not a coupling of the two.

---

## 1. Problem Understanding

When an operator creates a `subagent_3p` (an external-CLI delegation worker running `claude-code`, `codex`, or `opencode`), Omnipus must spawn the CLI at a **correct executable path** (`ExecutorConfig.cli_path`). Today three defects combine so the path is often wrong or unverified until first run:

1. **Detection discards the path.** `HandleSystemCliDetect` (`GET /api/v1/system/cli-detect`, `pkg/gateway/rest.go:2072`) calls `exec.LookPath` but drops the resolved path — `_, err := cliProbeLookPath(binary)` (`rest.go:2078`) — returning only three booleans (`hasClaude/hasCodex/hasOpencode`, `contracts/components/schemas/CliDetect.yaml`). `[FACT]`
2. **The prefill is a static placeholder.** `Step1Identity.tsx:274-276` sets the path input `value` to `payload.executor_cli_path ?? ''` (empty) with a hardcoded `placeholder="/usr/local/bin/claude-code"` — identical for all three CLIs, not detected. `[FACT]`
3. **No create-time validation.** `runner.TestConnectionWithPath` (`pkg/agent/runner/conntest.go:146`) already classifies missing-binary / handshake / unauthenticated, but its only entry point is the agent-scoped `POST /api/v1/agents/{id}/runner-test` (`rest.go:1189`) — reachable only **after** the agent is saved. `[FACT]`

**Business objective.** Make `subagent_3p` creation reliable: auto-detect each known CLI's real install location, prefill it per selected CLI, validate the tool actually runs there before save, and keep manual override.

**Stakeholders.** Operators creating/editing external-CLI subagents via the SPA wizard (`Step1Identity`) and the edit form (`AgentProfile.tsx`). **Blast radius:** Agent System (P1). No change to the agent loop, sandbox, or spawn path — only detection, wire schema, one new read-only endpoint, and SPA form behaviour. `[INFERENCE]`

## 2. Extracted Requirements

### Functional
- **FR-1** The system MUST detect the on-disk path of each known CLI, mapping the executor `cli` value to its binary (`claude-code`→`claude`, `codex`→`codex`, `opencode`→`opencode`). `[FACT — mapping per conntest.go supportedCLIs]`
- **FR-2** Detection MUST search the gateway process `$PATH` **and**, on a miss, a curated per-OS set of well-known install locations, returning an absolute path and its source. `[FACT — user decision: "PATH + well-known scan"]`
- **FR-3** The SPA MUST prefill the detected absolute path into the path field, per selected CLI, in both the create wizard and the edit form, only when the field is empty (never clobber a user value). `[FACT — user requirement]`
- **FR-4** The system MUST validate, at create-time, that the CLI at the (prefilled or overridden) path exists and runs (`--version` handshake). Missing credentials MUST be reported as a **non-blocking** warning, not a create blocker. `[FACT — user decision: "runs + --version handshake; auth non-blocking"]`
- **FR-5** The path field MUST remain a free-text override. `[FACT — field is already free-text]`
- **FR-6** Detection MUST cover Linux, macOS, and Windows. `[FACT — user decision]`

### Non-Functional
- **NFR-1 (correctness)** Detection MUST NOT report "not installed" for a CLI that is installed but absent from the gateway's `$PATH` (the primary defect). `[INFERENCE — high]`
- **NFR-2 (footprint)** Pure Go, no CGo, **no shell-out** to `which`; no new runtime dependency (Constraints #1–#3). Detector cost bounded — fixed dir list, single-level `stat` of specific filenames, no recursive walk. `[FACT — constraint]`
- **NFR-3 (security)** Detection is read-only, unaudited, zero-token. Validation spawns only the CLI's own `--version` via `execve` (no shell interpolation), zero-token. `[FACT — conntest.go behaviour]`
- **NFR-4 (contract-first)** All wire changes defined in `contracts/` first and regenerated (Constraint #8). `[FACT]`
- **NFR-5 (latency)** Detection returns without spawning a subprocess (filesystem probes only); validation is opt-in (on blur / on demand), bounded by the existing `versionProbeTimeout` (15 s, `conntest.go`). `[FACT]`

### Constraints
- Ships on the `hotfix/v0.1.1` line (v0.1.x). `[FACT — user instruction]`
- `ExecutorConfig.cli` is locked after create; `cli_path` is **mutable** (`ExecutorConfig.yaml:48-58`) — so prefill + validation must exist on the edit form too, not only the wizard. `[FACT]`

## 3. Gaps and Ambiguities

| # | What's missing/ambiguous | Why it matters | Likely assumption if unresolved | Question to resolve |
|---|---|---|---|---|
| 1 | Exact well-known dir list per OS | Determines detection hit-rate | Use the `[EXPERT REASONING]` lists in §6 (npm-global, Homebrew, `~/.local/bin`, nvm, bun) | Confirm during plan-spec; adjust from field reports |
| 2 | Multiple installs of one CLI (npm + brew) | Which path is prefilled | First hit wins by ordered candidate list; surface path so operator can correct | Accept "first hit + editable"? |
| 3 | Does missing-binary/handshake **block** Create, or only warn? | UX strictness | Block Create on missing-binary/handshake (it cannot run); warn-only on unauthenticated | Confirm the blocking rule in plan-spec |
| 4 | Backend memoization of detection | Repeated wizard opens re-scan dirs | Compute per request; rely on SPA React-Query cache; optional short TTL | Decide in plan-spec (perf, not architecture) |
| 5 | Whether `CliDetect` is consumed by any non-SPA client | Safety of a breaking wire restructure | Internal SPA↔gateway only; both ship in one binary | Confirm no external consumer (grep shows none) |

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Correctness — no false negatives (NFR-1) | 0.35 | The entire point of the fix |
| Footprint — pure-Go, no deps, bounded cost (NFR-2) | 0.20 | Constraints #1–#3 |
| Reuse of existing, tested code | 0.15 | `conntest.TestConnectionWithPath` already classifies failures |
| Create-time UX clarity | 0.15 | Actionable feedback before save |
| Cross-platform robustness | 0.15 | Linux/macOS/Windows install conventions |

## 5. Option Analysis

The four depth/validation/OS/scope forks are **decided** (§ decisions). The remaining architectural choice is the **shape** of detection + validation + wire.

### Option A — Split cheap-detect + on-demand-validate, shared pure-Go detector *(recommended)*
A new pure-Go `pkg/clidetect` package (LookPath → well-known scan) feeds an enriched `GET /system/cli-detect` (per-CLI `{installed, path, source}`, no subprocess). A new stateless `POST /system/cli-validate {cli, cli_path}` reuses `runner.TestConnectionWithPath` for the `--version` handshake. SPA prefills from detect, validates on blur.

| Dimension | Assessment |
|---|---|
| Strengths | Cheap detection (no subprocess) separated from opt-in validation; reuses `conntest` verbatim; smallest wire surface; caches cleanly in React Query; validation available pre-save AND on the mutable edit form |
| Weaknesses | Two endpoints instead of one; a breaking `CliDetect` restructure (both ends updated together) |
| Risks | Spawning a user-supplied path in validate — mitigated: `execve` no-shell, `--version` only, admin-trusted config, and the same path is spawned at run time anyway (no new blast radius) `[INFERENCE]` |
| Complexity | Low. One new pkg (~1 file), one enriched handler, one new handler delegating to existing code |
| Cost implications | Build: small. Run: filesystem stats on detect, one short-lived `--version` on validate. Scaling: N/A (local diagnostic) |
| Operational impact | No deploy/monitoring change; read-only, unaudited endpoints |

### Option B — Single combined "detect+validate" endpoint
One endpoint runs LookPath + well-known scan **and** the `--version` handshake for all three CLIs, returning full status up front.

| Dimension | Assessment |
|---|---|
| Strengths | One round-trip; the SPA gets everything at once |
| Weaknesses | Spawns up to 3 subprocesses on **every** wizard open, even for CLIs the user won't pick; couples cheap detection to expensive validation |
| Risks | Latency spikes / process churn on the roster screen; a hanging CLI stalls the whole response |
| Complexity | Medium |
| Cost implications | Higher run cost; wasteful subprocess spawns |
| Operational impact | More process activity to reason about |

### Option C — Minimal: enrich detect, validate post-create *(rejected baseline)*
Add paths to `cli-detect`; keep validation only on the existing agent-scoped `runner-test` after save.

| Dimension | Assessment |
|---|---|
| Strengths | Smallest change; no new endpoint |
| Weaknesses | Contradicts the decided requirements (create-time validation + full mechanism); a wrong path is caught only after the agent exists |
| Risks | The reported defect persists in the create flow |
| Complexity | Lowest |
| Cost implications | Lowest build cost, highest defect cost |
| Operational impact | None |

## 6. Recommended Architecture

**Option A.** It scores highest on correctness (well-known scan fixes NFR-1), footprint (no subprocess on detect; no new deps), and reuse (`conntest` is reused unchanged). Option B lost on footprint/latency (subprocess-per-open); Option C lost on correctness (no create-time validation — contradicts the decided scope).

**Design skeleton (for `/plan-spec` to expand — not implementation):**

**(a) Detector — `pkg/clidetect` (pure Go).**
`Detect(cli string) Result{Installed bool; Path string; Source "path"|"well-known"}` and `DetectAll()`:
1. Map `cli`→binary (+ Windows lets `exec.LookPath` resolve `PATHEXT`: `claude.cmd`/`.exe`).
2. `exec.LookPath(binary)` → on hit, `filepath.EvalSymlinks` + `Abs`, `source="path"`.
3. On miss, scan ordered per-OS candidate dirs for the binary (single-level `os.Stat` of the exact filename); first hit → `Abs`, `source="well-known"`.
4. Else `Installed=false`.

Candidate dirs `[EXPERT REASONING — validate in plan-spec]`, resolved from `os.UserHomeDir()`:
- **Linux:** `/usr/local/bin`, `/usr/bin`, `~/.local/bin`, `~/.npm-global/bin`, newest `~/.nvm/versions/node/*/bin`, `~/.bun/bin`, `~/.deno/bin`, `~/.cargo/bin`, `/snap/bin`
- **macOS:** `/opt/homebrew/bin` (Apple Silicon), `/usr/local/bin` (Intel), `~/.local/bin`, + npm/nvm/bun as above
- **Windows:** `%APPDATA%\npm` (npm shims), `%LOCALAPPDATA%\Programs\<tool>`, `%ProgramFiles%\...` (LookPath already applies `PATHEXT`)

**(b) Wire — restructure `CliDetect` (contract-first).** Replace the three booleans with per-CLI objects:
```yaml
CliDetect:
  claude:   { installed: bool, path: string|null, source: "path"|"well-known"|null }
  codex:    { ... }
  opencode: { ... }
```
Both ends ship in one binary; regeneration keeps `pkg/api/generated` + `src/lib/api/generated` + zod in sync. Update the sole consumer (`AgentListScreen.tsx`).

**(c) Validation — new stateless `POST /api/v1/system/cli-validate`.**
Request `CliValidateRequest{cli, cli_path}`; response `CliValidateResponse{ok, reason, resolved_path, version?, detail}` where `reason ∈ {ok, missing-binary, handshake-failed, unauthenticated, unknown-cli}` (reuse `runner.FailureReason`). Body: `runner.TestConnectionWithPath(ctx, cli, cli_path)`. `unauthenticated` → `ok:false` but flagged non-blocking. `withAuth`, read-only, unaudited.

**(d) SPA.** On CLI select (wizard) / form open (profile), if `executor_cli_path` empty, set it to `detect[cli].path`; show "Detected at <path>" (green) / "Not found — enter manually" (amber). On path blur / "Test", call `cli-validate`: `missing-binary`/`handshake-failed` → block Create with reason; `unauthenticated` → amber "installed, not logged in" and allow save; `ok` → green.

```
CONFIDENCE: High
  Basis         : Current defects verified in code on the target branch; reuses tested conntest; user-decided the four forks
  Evidence      : rest.go:2078 (path discarded), CliDetect.yaml (booleans), Step1Identity.tsx:274-276 (placeholder), conntest.go:146 (validator exists), no cli-validate endpoint (grep)
  Missing       : Field confirmation that the well-known dir lists cover real installs across distros/managers
  Would improve : A quick spike enumerating install paths on 2–3 real hosts (npm-global, brew, nvm)
```

Sub-decision — **wire restructure vs additive path fields:**
```
CONFIDENCE: Medium
  Basis         : Internal SPA↔gateway contract, single consumer, both ends ship together
  Evidence      : grep shows only AgentListScreen.tsx consumes CliDetect
  Missing       : Absolute certainty no out-of-tree tooling reads /system/cli-detect
  Would improve : Confirm no external consumer; if any exists, fall back to additive claudePath/codexPath/opencodePath fields (non-breaking)
```

## 7. Risks and Caveats
- **R1 — well-known lists drift.** New managers/paths appear over time. Mitigation: keep the list in one `clidetect` table; `LookPath` remains the primary path, the scan is a fallback. `[INFERENCE]`
- **R2 — validate spawns a user-supplied executable.** Mitigation: `execve` (no shell), `--version` only, admin-trusted `cli_path` that is already spawned at run time — no new blast radius. Not a one-way door. `[INFERENCE]`
- **R3 — breaking `CliDetect` restructure.** Mitigation: contract-first regeneration + update the single consumer in the same PR; additive fallback documented if an external consumer surfaces. `[FACT/INFERENCE]`
- **R4 — symlink resolution surprises** (Homebrew/npm shims point elsewhere). Mitigation: prefill the resolved path but keep it editable; validation confirms it runs. `[INFERENCE]`
- **One-way doors:** none. Wire shape is regenerated; endpoints are additive/diagnostic.

## 8. Confidence Assessment
- Recommended architecture (Option A): **High** — defects and reuse are code-verified; the four design forks are user-decided.
- Wire restructure sub-decision: **Medium** — safe given a single internal consumer; additive fallback exists.
- Well-known-location coverage: **Medium** — `[EXPERT REASONING]` lists, best validated by a short host spike before/with implementation.

## 9. Validation / Next Steps
- **Red-team this ADR:** `/grill-spec docs/internal/architecture/ADR-030-external-executor-cli-path-detection.md`
- **Turn it into an implementation-ready spec:** `/plan-spec docs/internal/architecture/ADR-030-external-executor-cli-path-detection.md` (BDD for detect/prefill/validate flows; TDD datasets for the per-OS scanner: found-on-PATH, found-in-well-known, absent, symlinked, Windows `.cmd`, multiple-installs; contract tests for the new `CliDetect`/`CliValidate` schemas).
- **Optional spike to raise confidence:** enumerate actual install paths of the three CLIs on 2–3 representative hosts (Linux systemd service PATH, macOS Homebrew ARM, Windows npm) and confirm the §6(a) candidate lists resolve them.
- **Implementation shape (waves):** backend `pkg/clidetect` + enriched detect handler + new `cli-validate` (backend-lead) ∥ contracts `CliDetect`/`CliValidate` regen (backend-lead) → SPA prefill+validate in wizard & profile (frontend-lead) → qa-lead (per-OS scanner tables, contract tests) → 7-reviewer gate.

## 10. Companion Decision — Datepicker → shadcn (same branch/PR)

Included per operator direction ("just include it"). A P2 UI/UX fix riding the same branch as this ADR. Independent of the external-executor decision; recorded here for convenience, kept deliberately brief since all forks are already decided.

**Problem.** Native `<input type="datetime-local">` / `type="date"` inputs render larger and misaligned versus the shared `<Input>` (`h-11 sm:h-9`): their browser shadow-DOM parts (`::-webkit-datetime-edit`, `::-webkit-calendar-picker-indicator`) size independently of the height class, and no global CSS normalizes them. `[FACT — src/components/ui/input.tsx:12; globals.css has no date-input rules]`

**Decision.** Replace every native date/time input with a hand-vendored shadcn picker (no `components.json`, so the shadcn CLI is unavailable — components are authored by hand):
- Add dependency `react-day-picker` v9 (operates on native `Date`; reuse the existing converters in `src/components/workspaces/taskFormFields.ts` → **no `date-fns`**). `[FACT — @radix-ui/react-popover + ui/popover.tsx + ui/button.tsx + ui/select.tsx present; react-day-picker/date-fns absent]`
- New `ui/calendar.tsx` (themed Sovereign Deep: Forge Gold selected day, dark surface), `ui/date-picker.tsx` (date-only), `ui/date-time-picker.tsx` (Calendar + two shadcn `Select` for hour/minute — **no native time control**). The trigger `Button` reuses the exact `<Input>` classes (`h-11 sm:h-9 w-full rounded-md border px-3 text-sm bg-surface-1` + focus ring) so the box is pixel-identical — "the css right".
- Migrate **7 call sites**: datetime (5) `CreateTaskSlideOver.tsx:448,554`, `ScheduleFormSheet.tsx:671`, `TaskDetailPanel.tsx:601,700`; date-only (2) `MilestoneDatePopover.tsx`, `CreateMilestoneSlideOver.tsx`. Value contract unchanged (pickers wrap `toDatetimeLocalValue` / `datetimeLocalToMs` / `datetimeLocalToIso`).

**Decided forks (operator):** fully-shadcn hh/mm `Select`s (no native HTML) · all 7 sites · same branch/PR.

**Rejected (one line each):** CSS-only normalization of native inputs — operator: no native HTML · a separate ADR-031 — operator: don't complicate · `date-fns` — reuse existing converters.

```
CONFIDENCE: High
  Basis         : All forks operator-decided; existing components/deps verified in the tree
  Evidence      : popover/button/select present, react-day-picker absent, 7 call sites located
  Missing       : Real-browser confirmation of trigger-height parity (DoD — needs the SPA running)
  Would improve : Playwright screenshot of the trigger beside a sibling input, dark theme
```

**DoD.** vitest for the 3 components + updated call-site tests · `tsc -b` + vitest green · Playwright verify the trigger height matches a sibling input and the calendar renders in dark theme. **Wave:** frontend-lead builds dep + 3 components → fan-out migrate the 7 sites (parallel by file) → qa-lead → visual-qa → 7-reviewer gate.

## 11. Revisions — Post-Grill Round 1 (security hardening)

`/grill-spec` returned **BLOCK** (review: `docs/internal/specs/external-executor-cli-path-detection-spec-review.md`, 1 CRITICAL / 7 MAJOR). The findings below revise decisions in §2/§6 and **supersede** the earlier text where they conflict. All move strictly toward the project's security-first posture (Constraint #7).

- **F-01 (CRITICAL) — validate hardening.** `POST /system/cli-validate` executes a caller-supplied path; it MUST apply `withRateLimit` and MUST audit the executed-path event (`{cli, resolved_path, reason}`). This overrides NFR-3 / the "must not audit" Non-Behavior **for validation only** — detection stays unaudited (no subprocess). `[Decision]`
- **F-02 — detect↔runtime symmetry.** Prefill MUST write the **absolute** detected path; validation and the runtime spawn resolve the exact `cli_path`; an empty `cli_path` classifies `missing-binary` (never a silent `$PATH` fallback). Guarantees "detected == runs"; no spawn-resolver change. `[Decision]`
- **F-03 — CLI identity.** The `--version` handshake MUST confirm the output identifies the expected CLI (per-CLI matcher), so a wrong binary (e.g. `/usr/bin/node`) yields `handshake-failed`, not a false pass. `[Decision]`
- **F-06 — HOME-unset.** The well-known scan MUST tolerate `os.UserHomeDir()` failure (systemd, `HOME` unset): passwd-DB fallback or skip `~`-rooted candidates rather than erroring. `[Decision]`
- **F-04/F-05/F-07/F-08 + minors** are spec-level corrections (canonical `source:"path"`; concrete `CliDetect`/`CliValidate` schemas; `ReasonOK`→`"ok"` mapping; gate Create on `reason ∈ {missing-binary,handshake-failed}` not raw `!ok`; concrete well-known list promoted to acceptance with a real-install SC-001; classified `detail`, never raw stderr) — detailed in the spec's "Post-Grill Revisions (Round 1)" section.

CONFIDENCE: High — resolutions add security/correctness only; no new one-way doors. The validate endpoint is now audited + throttled like a privileged diagnostic.
