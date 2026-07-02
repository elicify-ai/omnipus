# Grill Report: External-Executor CLI Path Detection, Prefill & Validation

- **Spec reviewed:** `docs/internal/specs/external-executor-cli-path-detection-spec.md`
- **Source ADR:** `docs/internal/architecture/ADR-030-external-executor-cli-path-detection.md`
- **Mode:** `plan-spec` (BDD + FR-xxx + SC-xxx + traceability matrix present)
- **Reviewer stance:** adversarial, read-only. Assumes worst-case interpretation.
- **Date:** 2026-07-02

---

## 1. Executive Summary

This spec is well-structured and traceable, but it ships a **new endpoint that
spawns an arbitrary, caller-supplied filesystem path as a subprocess — explicitly
un-audited and un-rate-limited** — and it papers over a **detect-vs-validate-vs-runtime
asymmetry** (detection scans well-known dirs; validation and the real spawn resolve
`$PATH` only) that will produce "detected but won't run" agents in the exact
deployment the feature targets (a service with a minimal `$PATH`). The `--version`
handshake is also over-claimed as CLI-identity validation when it only proves *some*
binary runs. Several contract-level values (`source` casing, `ok`-vs-`reason`,
`ReasonOK == ""`) are left undefined and will fail the contract tests or mis-drive the
Create/Save block.

**Findings:** 1 CRITICAL, 7 MAJOR, 6 MINOR, 4 OBSERVATION.

**Verdict: BLOCK** (one critical security/DoS finding; multiple major correctness and
contract gaps).

---

## 2. Findings

| ID | Sev | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|---|
| F-01 | CRITICAL | Insecurity / Inoperability | Integration Boundaries; FR-006/007; "unaudited" Non-Behavior | `POST /system/cli-validate {cli, cli_path}` spawns `<cli_path> --version` where `cli_path` is **fully caller-controlled from the request body**, before any agent is persisted, and the spec mandates it be **`withAuth`, read-only, unaudited** with **no rate limit** (the sibling `cli-detect` is already registered `withAuth` with no `withRateLimit`, unlike every other config endpoint — `rest.go:3912`). Any authenticated caller can (a) execute any binary on the host as `<path> --version` (info-gathering / triggering planted scripts / FIFOs / `/dev/stdin` blocking), and (b) DoS the gateway by firing many calls, each holding a process for up to 15 s. This is strictly broader than today's posture, where a spawn only happens for an already-persisted, admin-created (audited `agent.create`) config. | Add `withRateLimit` to **both** new/changed handlers. **Audit** each validate call (path + caller + outcome) — executing a user-supplied path is not "read-only". Constrain the spawned path (must be absolute or resolve to a supported binary name; reject paths outside an allowlist / reject non-regular files) OR require the same authorization level as agent-create. State the RBAC role required to reach the endpoint. If any non-admin authed role can reach it, treat as an arbitrary-exec vuln and gate accordingly. |
| F-02 | MAJOR | Incorrectness / Incompleteness | US-2; FR-002; D-3.1; runtime spawn | **Detection scans `$PATH` + well-known dirs; validation and the real run resolve `$PATH` only** (`TestConnectionWithPath`→`resolveCLIBinary`→`exec.LookPath`; drivers do the same — `driver_claude.go:100` etc.). A CLI present only in a well-known dir (the *primary* NFR-1 case) is detected and prefilled as an absolute path — fine — but if the field is ever **empty** at validate/save (US-5 AC-1 edit form opening with empty `cli_path`; operator clears it; D-3.1 "empty → falls back to `$PATH` default"), validation returns `missing-binary` **and the saved agent fails at runtime**, directly contradicting the "detected: installed" result. | Require the SPA (or the create/update handler) to **persist the resolved absolute path**, never empty, whenever detection found one. Add an FR + BDD scenario: "empty `cli_path` on a well-known-only host → save persists the detected absolute path, not empty." Reconcile D-3.1 with US-2 explicitly. |
| F-03 | MAJOR | Incorrectness | US-3 AC-2; D-2.2; Integration Boundaries | The `--version` handshake proves *a* binary ran and printed *something*; it does **not** prove it is the expected CLI. Pointing `claude-code` at `/usr/bin/node` (or any tool that supports `--version` and exits 0) yields `ok`/`unauthenticated`, **not** `handshake-failed`. US-3 AC-2 ("non-CLI/incompatible binary → `handshake-failed`") and D-2.2 are therefore false for the common case, giving operators false confidence that the wrong binary is "valid". | Either (a) narrow the AC to "a binary that does not print a version / exits non-zero / times out → `handshake-failed`" and drop the "incompatible binary" claim, or (b) add identity verification (match the version output against a per-CLI signature) and specify it. Do not claim validation catches wrong-but-runnable binaries unless it does. |
| F-04 | MAJOR | Ambiguity / Inconsistency | ADR §6b vs spec BDD & D-1 & UX | The `source` enum value is **`"path"`** in ADR §6(b)/§6(c) but **`"PATH"`** (uppercase) in the BDD scenarios ("source `PATH`", indicator "Detected (PATH)"), and D-1 mixes `PATH` (upper) with `well-known` (lower). The spec never includes the actual restructured `CliDetect` YAML, so the generated enum is undefined. Two engineers will emit different wire values; the zod schema / contract test will fail against whichever the SPA hard-codes. | Pin the exact enum in the spec (recommend lowercase `path` | `well-known` | `null` to match Go idiom) and include the concrete `CliDetect` schema block. Decouple the wire value from the UI label. |
| F-05 | MAJOR | Inconsistency / Infeasibility | FR-006; ADR §6c; conntest.go | `runner.ReasonOK` is the **empty string** (`FailureReason = ""`), not `"ok"` (conntest.go). The spec/schema require `reason: "ok"` on success (BDD "the reason is 'ok'"; enum lists `ok`). The empty-string→`"ok"` mapping is unspecified; a naive `reason: string(result.Reason)` emits `""` and fails `TestValidate_OK` and the contract enum. Additionally, `ok` (bool) vs `reason` is a trap: ADR §6c sets `unauthenticated`→`ok:false`, yet FR-008/US-3 AC-3 require Create **allowed** on `unauthenticated`. A developer who disables Create on `!ok` breaks the stated behavior. | Specify the exact `FailureReason`→wire-`reason` mapping (including `"" → "ok"`). State that Create/Save blocking keys off **`reason`**, not `ok`, and define the value of `ok` for every reason (or drop `ok` to remove the trap). Add these to the CliValidate schema block. |
| F-06 | MAJOR | Incompleteness | US-2; ADR §6a; NFR-1 target env | The whole point (NFR-1) is a gateway run as a **service with minimal `$PATH`**. On systemd, `HOME` is frequently unset, so `os.UserHomeDir()` **errors** — and every user-scoped candidate dir (`~/.local/bin`, `~/.npm-global/bin`, `~/.nvm/...`, `~/.bun/bin`) can't be built. The well-known scan then silently covers only system dirs (`/usr/local/bin`, `/opt/homebrew/bin`), defeating the feature for the exact deployment it targets. The spec never addresses `UserHomeDir()` failure or the systemd `HOME`-unset case. | Add an FR + edge case: on `UserHomeDir()` failure, fall back to `$HOME` env, then to the service user's passwd entry, and log a WARN. Add a test (`TestDetect_HomeUnset`). Consider making the home dir configurable. |
| F-07 | MAJOR | Incompleteness | FR-002; A-3; Scope | The **actual well-known directory list is not in the spec** — FR-002 says "a curated per-OS well-known-location list" and A-3 defers the exact dirs to "a host spike" that is **not part of acceptance**. The list *is* the feature; leaving it in the ADR as `[EXPERT REASONING]` and out of the testable spec means the deliverable's correctness is unbounded. SC-001 (below) then measures against injected fakes, not the real list. | Move the concrete per-OS candidate-dir list (with ordering) into the spec as a normative table, and make "the list resolves the 3 CLIs installed via npm-global / Homebrew / nvm on a representative host" a real acceptance criterion, not a deferred spike. |
| F-08 | MAJOR | Insecurity (STRIDE: Info Disclosure) / Repudiation | Integration Boundaries; Non-Behaviors | `cli-detect.path` and `cli-validate.{resolved_path, detail}` return absolute host paths (leaking usernames via `/home/<user>/...`) and `detail` echoes spawned-binary stderr (`%v` of the handshake error, per conntest `Message`). Returned to the SPA of *any* authed user with no classification statement, and the "must not audit" Non-Behavior means an executed-path event leaves no trail (repudiation). | Classify these fields (internal/admin-only), specify that `detail` is sanitized (no raw stderr passthrough), and reconcile the "unaudited" stance with F-01 (executing a path must be audited). |
| F-09 | MINOR | Inconsistency | ADR §6a step 3 vs Edge Cases | Detection's well-known scan uses **`os.Stat`** (no exec-bit, no not-a-dir check), while validation uses `exec.LookPath` (rejects non-exec and directories). A readable-but-non-executable file — or a sub*directory* named exactly `codex` — in a well-known dir → detection reports `installed:true` and prefills it, then validation returns `missing-binary`. The Edge-Cases table only describes `LookPath` semantics, not the scan's. | Have the well-known scan check the file is a regular, executable file (`Mode().IsRegular() && 0111`), matching `LookPath`, so detect and validate agree. Add a test row. |
| F-10 | MINOR | Infeasibility | TDD row 6; US-6 AC-1; SC-007 | `TestDetect_WindowsPathext` runs on the Linux CI gate. Windows `PATHEXT`/`.cmd` resolution is a real `exec.LookPath` OS behavior that a Linux runner cannot exercise; the plan concedes "(build-tagged/table)". So US-6 AC-1 is never verified in CI, yet SC-007 claims "all 20 TDD tests pass" as done-ness. Windows is effectively unvalidated. | State explicitly that Windows detection is verified by a table/logic test only (not real `LookPath`), or add a Windows CI leg. Do not let SC-007 imply Windows coverage it doesn't have. |
| F-11 | MINOR | Infeasibility / Incorrectness | SC-001 | SC-001 ("0 false negatives across the D-1 matrix") is measured against a 7-row matrix of **injected fakes** the code controls — it verifies the code does what the code does, not real-world hit-rate (the true NFR-1 risk, deferred to A-3's spike). As written it is trivially satisfiable and does not measure the objective. | Re-anchor SC-001 to a real-host check (F-07) or rename it to what it actually measures ("the detector honors PATH-then-well-known precedence for the fixtures"). |
| F-12 | MINOR | Ambiguity | Integration Boundaries; behavioral contract | `resolved_path`, `version`, and `detail` are undefined **per reason**: what is `resolved_path` on `missing-binary` (empty? raw input?)? Is `version` omitted or empty on non-`ok`? Is `resolved_path` the `EvalSymlinks` target or the raw `cli_path`? Undefined fields drive inconsistent SPA rendering. | Add a per-reason field table to the CliValidate schema (which fields are populated for each of the 5 reasons). |
| F-13 | MINOR | Incompleteness | US-2 AC-3; Edge Cases | `filepath.EvalSymlinks` can **error** (dangling/broken symlink, or a path that becomes invalid). US-2 AC-3 assumes it always resolves. No error path: does detection then report not-installed, or fall back to the un-resolved `Abs` path? | Specify: on `EvalSymlinks` error, fall back to `filepath.Abs` of the raw hit and still report installed (or not-installed) — pick one and add a test. |
| F-14 | OBSERVATION | Overcomplexity | ADR §6a Linux list | The `~/.nvm/versions/node/*/bin` "newest version" candidate is the single most complex scan element (glob + "newest" selection — by semver? by mtime? unspecified) for a speculative install pattern (operator installs the CLI under a specific nvm node version *and* runs the gateway without it on `$PATH`). It has **no TDD-plan test**. | Either drop the nvm-glob candidate, or specify the "newest" tiebreak precisely and add a test. Don't ship unspecified, untested globbing. |
| F-15 | OBSERVATION | Ambiguity | Behavioral Contract; A-4 | "validate-on-blur" lacks debounce/duplicate-call semantics: repeated blurs (each spawning a 15 s process — see F-01) with no debounce, and the behavior on blurring an **empty** field (skip validation, or validate the `$PATH` default per D-3.1?). | Specify debounce, skip-on-empty (or the intended empty-field behavior), and in-flight-request cancellation. |
| F-16 | OBSERVATION | Inoperability | FR-011; Regression | A browser holding a **stale SPA bundle** (old boolean `CliDetect`) hitting the new backend gets the restructured shape → zod drop → `cliDetectFailed=true` → `AgentListScreen` defaults all CLIs available (`defaultHostClis` all-true). Acceptable degrade, but unanalyzed; there is no version/skew note despite the single-binary embed not covering already-loaded browser tabs. | Add one line noting the stale-bundle degrade path and that it fails *open* (all CLIs shown), which is the safe direction. |
| F-17 | OBSERVATION | Incorrectness | US-2 AC-3 / D-1.4 | `source` semantics are muddy for a symlinked PATH hit: LookPath finds it on `$PATH`, `EvalSymlinks` resolves it into e.g. `/opt/homebrew/Cellar/...` (not on `$PATH`), yet `source` is reported `path`. "Where we found it" vs "where it resolves" diverge. Low impact (label only) but confusing. | Clarify that `source` reflects *how it was located* (PATH lookup vs dir scan), independent of the resolved location; state this in the schema description. |

---

## 3. Structural Integrity Results (plan-spec checks)

| Check | Result | Note |
|---|---|---|
| Every user story has ≥1 acceptance scenario | PASS | US-1..US-6 all do. |
| Every acceptance scenario has ≥1 BDD scenario | PASS (loose) | Some ACs share one vitest test (e.g. no-clobber). |
| Every BDD scenario has `Traces to:` | PASS | All 14 annotated. |
| Every BDD scenario has a TDD test | PARTIAL | "Prefill never clobbers" / "manual override" folded into `Step1Identity.prefillValidate.test`; acceptable but coarse. |
| Every FR in traceability matrix | PASS | FR-001..FR-012 present. |
| Every BDD scenario in matrix | PASS | Referenced by phrase per FR. |
| Test datasets cover boundary/edge/error | PASS | D-1/D-2/D-3 cover PATH/well-known/absent/symlink/dir/space/trim. |
| Regression impact addressed | PASS | §Regression + §Regression Impact Summary. |
| Success criteria measurable, no subjective language | FAIL | SC-001 circular (F-11); SC-003 "prefills ... in ≥ the 3 supported CLIs" is ungrammatical/unmeasurable; no SC for rate-limit/audit/home-unset. |
| **Missing FRs** | FAIL | No FR for rate limiting/audit (F-01), `UserHomeDir` failure (F-06), the concrete well-known list (F-07), or the `reason`/`ok` mapping (F-05). |

---

## 4. Test Coverage Assessment

- **Negative paths:** good on validation (D-2 covers all 5 reasons). Missing: `EvalSymlinks` error (F-13), `HOME` unset (F-06), non-executable/dir well-known hit (F-09), wrong-but-runnable binary (F-03).
- **Boundary:** D-3 covers empty/space/dir/trim; but D-3.1 (empty→`$PATH`) conflicts with US-2 and is untested against the well-known-only host (F-02).
- **Concurrency/DoS:** no test for repeated/parallel validate calls (F-01, F-15). Given each spawns a process, a resource-bound test belongs here.
- **Windows:** row 6 cannot run on the Linux gate (F-10) — coverage is table-logic only.
- **Idempotency:** N/A (read-only diagnostics) — acceptable.
- **Contract:** rows 13/14 exist but will fail unless F-04 (source casing) and F-05 (`ok`/`reason` mapping) are pinned in the schema first.

---

## 5. STRIDE Threat Summary

| Component | Threat(s) | Status in spec |
|---|---|---|
| `POST /system/cli-validate` | **Elevation/RCE-ish** (spawns caller-supplied path), **DoS** (no rate limit, 15 s hold), **Repudiation** (unaudited exec) | Unmitigated → F-01 |
| `GET /system/cli-detect` | **DoS** (unthrottled filesystem scan), **Info disclosure** (absolute paths → usernames) | Unmitigated → F-01/F-08 |
| CliValidate response `detail` | **Info disclosure** (raw stderr passthrough) | Unaddressed → F-08 |
| `pkg/clidetect` scan | **Tampering/false-positive** (os.Stat vs exec check) | Unaddressed → F-09 |
| Contract (`CliDetect`/`CliValidate`) | Spoofing/Tampering N/A (internal, authed) | OK |

---

## 6. Unasked Questions (the spec should have answered)

1. What **RBAC role** is required to reach `cli-validate`? Can a non-admin authed user spawn arbitrary paths? (F-01)
2. When detection finds a CLI **only** in a well-known dir, is the **absolute path persisted** so validation and runtime (both `$PATH`-only) succeed? (F-02)
3. What is the exact wire value of `source` (`path` vs `PATH`) and `reason` for success (`""` vs `"ok"`)? (F-04, F-05)
4. Does Create/Save block on `reason` or on `ok`? What is `ok` for `unauthenticated`? (F-05)
5. What happens when `os.UserHomeDir()` fails (systemd `HOME` unset)? (F-06)
6. What is the **normative** per-OS candidate-dir list and its ordering? (F-07)
7. How is the `--version` output tied to the *expected* CLI identity (or is it not)? (F-03)
8. Are `cli-detect`/`cli-validate` rate-limited and audited, given one executes a subprocess? (F-01, F-08)
9. What are `resolved_path`/`version`/`detail` for each of the 5 reasons? (F-12)
10. Is validate-on-blur debounced, and what does an empty-field blur do? (F-15)

---

## 7. Verdict

**BLOCK** — F-01 (arbitrary-path subprocess spawn, unaudited + unthrottled) is a
security/DoS defect; F-02/F-03/F-05/F-06/F-07 are correctness/contract gaps that will
produce "detected but won't run" agents, false-positive validations, and failing
contract tests. Address the CRITICAL and MAJOR findings, then re-grill.
