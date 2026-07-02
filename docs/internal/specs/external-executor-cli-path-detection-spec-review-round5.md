# Adversarial Review: External-Executor CLI Path Detection, Prefill & Validation

**Spec reviewed**: `docs/internal/specs/external-executor-cli-path-detection-spec.md`
**Review date**: 2026-07-02
**Verdict**: REVISE

## Executive Summary

This is the fifth grill pass on a spec that has already resolved a CRITICAL and most of the round-1 majors; the security posture and traceability are now largely sound. However, verification against the *actual* target code surfaces six new MAJOR defects the prior rounds did not catch — all grounded in real symbols on the branch, not speculation: a prescribed limiter name that collides with an existing symbol, a "per-caller concurrency cap" that no existing primitive provides, a handshake guarantee the reused-as-is `conntest` demonstrably does not deliver, a pre-spawn file check that false-blocks valid bare-name overrides, an XFF-spoofable rate limiter guarding a subprocess-spawning endpoint, and an under-counted regression blast radius. No CRITICAL (the endpoint is authenticated and shell-free), so the verdict is REVISE.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 6 |
| MINOR | 6 |
| OBSERVATION | 2 |
| **Total** | **14** |

---

## Findings

### MAJOR Findings

#### [MAJ-001] Prescribed limiter name `validateLimiter` collides with an existing, different-purpose symbol

- **Lens**: Inconsistency / Incorrectness (codebase context)
- **Affected section**: FR-013 — "It MUST apply a **dedicated** rate limiter (a `validateLimiter` modeled on the existing config limiter)".
- **Description**: `validateLimiter` **already exists** — `pkg/gateway/rest_auth.go:181`: `validateLimiter = newAPIRateLimiter(30, 1*time.Minute)` — bound to `/api/v1/auth/validate` (`HandleValidateToken`, `rest.go:4031`) for auth-**token** validation. The spec names the new CLI-validate limiter `validateLimiter`. An implementer following the spec literally either (a) gets a Go redeclaration compile error, or (b) "reuses the existing one," silently coupling CLI-validation throttling to auth-token throttling (a shared 30/min budget across two unrelated endpoints — exhausting one throttles the other).
- **Impact**: Build break, or a subtle shared-budget coupling that neither endpoint's tests would catch. Directly contradicts the word "dedicated" in the same sentence.
- **Recommendation**: Rename to `cliValidateLimiter` (or `cliValidateRateLimiter`) throughout FR-013 and TDD test 22. State the concrete config (e.g. `newAPIRateLimiter(10, 1*time.Minute)`) and confirm it is a *new* var, not the existing `validateLimiter`.

#### [MAJ-002] "Cap in-flight validations per caller" has no supporting primitive and leaves "caller" undefined

- **Lens**: Infeasibility / Ambiguity
- **Affected section**: FR-013 — "**cap in-flight validations per caller** (small, e.g. 2 — *per-caller*, not global)"; SC-008; Integration Boundaries.
- **Description**: The spec claims this is "modeled on the existing config limiter," but the existing limiter (`apiRateLimiter` / `withRateLimit`, `rest_auth.go:107-223`) is (1) a sliding-window **request-rate** limiter, not a **concurrency** (in-flight semaphore) limiter, and (2) keyed by **IP** (`clientIP(r)`), not by caller identity. Neither an in-flight semaphore nor per-caller keying exists in the codebase. "Caller" is never defined — authenticated user ID? session? IP? `withAuth` does inject a user/role into context, but the spec never says to key off it, and the referenced limiter cannot.
- **Impact**: An engineer cannot implement FR-013 from existing building blocks; they must invent a new per-identity semaphore, and the identity dimension (IP vs user) changes the security property materially. TDD test 22 ("concurrency cap holds") is untestable until "caller" is pinned.
- **Recommendation**: Either (a) drop the concurrency cap and rely on the rate limiter + 15s timeout (see OBS-002), or (b) specify it concretely: a `map[callerKey]int` in-flight counter guarded by a mutex, where `callerKey` = the authenticated user ID from context (fall back to `clientIP` for bypass/dev), cap = 2, 429 when exceeded, decremented in a `defer`. Name the type and where it lives.

#### [MAJ-003] The handshake guarantee ("runs + valid version-shaped response") is stronger than the reused-as-is `conntest` delivers

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: FR-015, F-03, SC-010, D-2.2, US-3 AC-2, BDD "Non-CLI binary fails the handshake and blocks Create"; "reuse `conntest` **as-is**".
- **Description**: The spec asserts the handshake confirms a "valid version-shaped response" and that "a binary that emits no version → `handshake-failed`," while mandating `TestConnectionWithPath` is reused verbatim. But `extractVersion` (`pkg/agent/runner/conntest.go:245-257`) returns the **entire first line** when no version-shaped token is found ("still a valid handshake signal — the binary ran"), and `probeVersion` (`:213-238`) only errors when the process exits non-zero/times out **or output is empty**. Reused-as-is, the handshake passes for *any* binary that runs, exits 0, and prints *any non-empty line*. Concretely: pointing `cli_path` at `/bin/ls` or `/bin/cat` → `--version` prints the GNU coreutils banner → `extractVersion` returns it as the "version" → classified `ok`/`unauthenticated` → **Create allowed**. That contradicts US-3 AC-2 / D-2.2 ("present, no `--version` → handshake-failed → blocked") — which holds only for binaries that print *literally nothing*.
- **Impact**: US-3 AC-2 and D-2.2 fail against a real host with a common mis-pointed binary; the "block a non-CLI binary" promise is unmet. Unit test `TestValidate_HandshakeFailed` passes with a fake but does not reflect real conntest behavior — a green test masking a false guarantee.
- **Recommendation**: Reconcile wording with code. Since the operator accepts a "mis-pointed but runnable binary" (F-03), change FR-015/SC-010/D-2.2/US-3 AC-2 to "runs and exits 0 with **non-empty** output → pass; won't run / non-zero / timeout / **empty** output → handshake-failed," and delete "valid version-shaped" (it is not enforced). If version-shape enforcement is genuinely wanted, that requires changing `extractVersion` — which contradicts "reused as-is" and must be stated as a `conntest` change with its own regression coverage.

#### [MAJ-004] Pre-spawn "regular executable file" check false-blocks valid bare-name overrides

- **Lens**: Incorrectness / Ambiguity
- **Affected section**: FR-013 — "**reject a target that is not a regular, executable file before spawn** (classified `missing-binary`)"; US-4 (manual override is authoritative); Edge Cases.
- **Description**: The runtime spawn resolves a bare name via `$PATH` — `resolveCLIBinary` (`executor_opts.go:50-55`) returns the value verbatim and `exec.LookPath` resolves `claude` → `/usr/local/bin/claude`. US-4 keeps the field free-text, so an operator may type a bare name (`claude`). If FR-013's check runs `os.Stat` on the **raw** `cli_path` *before* `LookPath`, `os.Stat("claude")` fails and validation returns `missing-binary` → Create blocked — even though the exact value runs at runtime. The spec never states the check must run on the LookPath-**resolved** path.
- **Impact**: A valid, runnable configuration is blocked at create-time, contradicting US-4 and creating the "detected == runs" asymmetry the spec tries to avoid (F-02). Silent to the operator ("not found" for a binary plainly on `$PATH`).
- **Recommendation**: Specify order: resolve via `LookPath` (honoring `$PATH` for bare names, `PATHEXT` on Windows) **first**, then apply the regular-file/eligibility check to the *resolved* absolute path; classify `missing-binary` only if `LookPath` fails or the resolved path is a directory/non-regular file. Add a dataset row for a bare-name override that resolves on `$PATH`.

#### [MAJ-005] The DoS control for a subprocess-spawning endpoint is a per-IP limiter that trusts a spoofable header

- **Lens**: Insecurity (STRIDE: Denial of Service)
- **Affected section**: FR-013 (rate limiter as the throttle); Integration Boundaries; Non-Behaviors ("validation spawns only `<cli> --version`").
- **Description**: `cli-validate` spawns a subprocess bounded by a 15s timeout. The spec leans on `withRateLimit` as the primary DoS control, but it keys on `clientIP(r)`, and `clientIP` (`rest_auth.go:194-203`) trusts `X-Forwarded-For` **unconditionally** (returns the first XFF token, no trusted-proxy allowlist). An authenticated create-parity caller can send a fresh random `X-Forwarded-For` per request, land each in a distinct bucket, and bypass the cap — spawning many concurrent 15s processes and exhausting PIDs/FDs/RAM. The intended backstop (per-caller in-flight cap) is itself undefined/infeasible (MAJ-002), so there is no effective concurrency ceiling.
- **Impact**: A single authenticated user can DoS the gateway through a diagnostic endpoint. The ADR's blast-radius argument (R2: "the same path is spawned at run time anyway") is about *code execution*, not *volume*, and does not cover this amplification.
- **Recommendation**: Key the throttle on the authenticated identity from context (available post-`withAuth`), not on IP, for this endpoint — or document the hard deployment assumption that a trusted reverse proxy sets `X-Forwarded-For` and direct exposure is unsupported. Pair with a real concurrency ceiling (MAJ-002). Add a test asserting per-user (not per-IP) throttling.

#### [MAJ-006] The `CliDetect` wire-restructure regression list under-counts the real consumers that will break

- **Lens**: Incompleteness (regression blind spot)
- **Affected section**: FR-011 ("its **sole consumer** (`AgentListScreen`) migrated"); Regression Test Requirements §2; Symbols Involved / Impact Assessment.
- **Description**: The restructure from `{hasClaude, hasCodex, hasOpencode}` booleans to per-CLI objects breaks more than the spec lists. Verified consumers of the boolean shape: (1) `src/components/screens/AgentListScreen.tsx` — a local `HostClis` interface (`:29-31`) with a hardcoded boolean `defaultHostClis` fallback (`:35-37`), reading `hostClis.hasClaude/…` (`:167-169`); (2) `AgentListScreen.test.tsx` — mocks `{hasClaude,…}` (`:106,:432,:454`); (3) `AgentListScreen.auth.test.tsx` — returns a **raw fetch Response** `{hasClaude,…}` (`:70`), bypassing the `fetchCliDetect` mock (easy to miss); (4) `src/lib/api.cli-detect.test.ts`. The Regression section names only "AgentListScreen + `src/lib/api.cli-detect.test.ts`" and calls `AgentListScreen` the "sole consumer" — the `.auth.test.tsx` file and the `HostClis`/`defaultHostClis` adapter are unlisted.
- **Impact**: `npx vitest run` fails (SC-007 gate) on the unmigrated `auth.test.tsx`; or a reviewer trusts "sole consumer" and ships a partial migration. Contradicts SC-005/SC-007 "all green."
- **Recommendation**: Replace "sole consumer" with the enumerated list (component + `defaultHostClis` adapter + all three test files). Add each to the Symbols Involved table with role `modify`, and add a regression bullet: "grep the tree for `hasClaude|hasCodex|hasOpencode` and migrate every hit."

---

### MINOR Findings

#### [MIN-001] Stale symbol paths and line/route references

- **Lens**: Incorrectness (documentation accuracy)
- **Affected section**: Symbols Involved table; Relevant Execution Flows; Available Reference Patterns.
- **Description**: (a) `Step1Identity.tsx:274-276` — real path `src/components/agents/wizard/Step1Identity.tsx` (drops `wizard/`; line 274 content is correct). (b) `AgentListScreen.tsx` — real path `src/components/screens/AgentListScreen.tsx`. (c) The connection-test route is `POST /api/v1/agents/{id}/**runner/test**` (`rest.go:1069`), not `runner-test` as the spec/ADR write in several places. (d) `restAPI.testAgentRunner` is at `rest.go:**1142**`, not `:1189`.
- **Recommendation**: Correct the four references so implementers land on the right files/lines.

#### [MIN-002] Acceptance scenarios without a corresponding BDD scenario

- **Lens**: Incompleteness (plan-spec structural)
- **Affected section**: US-1 AC-3 (not-found → amber hint), US-4 AC-2 (empty detection → typed valid path validated), US-5 AC-1 (profile opens with empty `cli_path` → detected path offered).
- **Description**: Each lacks a BDD `Scenario` block (plan-spec requires "every acceptance scenario has at least one BDD scenario"). US-5 has a BDD only for AC-2 (validate-on-blur); the re-prefill-on-open behavior (AC-1, the point of US-5) has no Gherkin.
- **Recommendation**: Add three short BDD scenarios tracing US-1 AC-3, US-4 AC-2, US-5 AC-1, and map them in the traceability matrix.

#### [MIN-003] FR-001 "symlinks resolved" contradicts BDD scenarios asserting literal unresolved paths

- **Lens**: Inconsistency
- **Affected section**: FR-001 ("`path` is absolute (symlinks resolved)") vs BDD "Prefill from a CLI found on PATH" (asserts literal `/usr/local/bin/claude`) and "PATH result wins" (asserts literal `/usr/bin/claude`).
- **Description**: If `/usr/local/bin/claude` is itself a symlink (common for npm/brew shims — the case US-2 AC-3 targets), FR-001 requires the returned path to be the `EvalSymlinks` target, so the BDD's literal assertion would fail. The scenarios silently assume the PATH hit is not a symlink.
- **Recommendation**: State that `EvalSymlinks` applies to both PATH and well-known hits, and phrase the non-symlink BDDs as "resolves to `<abs real path>`" or add a Given that the path is not a symlink.

#### [MIN-004] Immeasurable / inaccurate success-criterion wording

- **Lens**: Infeasibility (measurability)
- **Affected section**: SC-003 ("prefills the field in a single interaction … **in ≥ the 3 supported CLIs**"); US-3 Independent test ("enter a real path → `ok`").
- **Description**: SC-003's "≥ the 3 supported CLIs" is grammatically broken and, with exactly three CLIs, "≥3" just means "all 3." US-3's independent test says a real path yields `ok`, but on an installed-but-logged-out host the result is `unauthenticated` (still allowed), not `ok` — flaky by host auth state.
- **Recommendation**: Reword SC-003 to "for all 3 supported CLIs." Reword US-3's test to "a real, authenticated path → `ok`; a real logged-out path → `unauthenticated` (still allowed)."

#### [MIN-005] Empty-path guard must trim, or whitespace-only input silently falls back to the `$PATH` default

- **Lens**: Incompleteness (edge case)
- **Affected section**: FR-014 ("empty `cli_path` MUST classify `missing-binary`"), F-02 ("never a silent `$PATH` fallback"), D-3.1, D-3.5.
- **Description**: `resolveCLIBinary` trims and treats a whitespace-only value as empty → falls back to `spec.binary` (the default `$PATH` binary) inside conntest. If the handler's short-circuit checks `cli_path == ""` literally (not trimmed), a `"   "` input bypasses the guard and conntest validates the **default** binary — violating F-02's no-fallback guarantee. `createAgent` itself trims-then-checks (`rest.go:1540`) precisely to avoid this.
- **Recommendation**: FR-014 should say the handler trims `cli_path` first and classifies `missing-binary` when the trimmed value is empty (whitespace-only included). Add a D-3 row: `"   "` → `missing-binary`.

#### [MIN-006] "Regular executable file" is under-defined on Windows

- **Lens**: Ambiguity (cross-platform)
- **Affected section**: FR-013 ("reject a target that is not a **regular, executable file**"); minor F-09.
- **Description**: Windows has no Unix execute bit; `os.FileInfo.Mode()&0111` is meaningless there, and executability is decided by `PATHEXT`. F-09 gestures at a "LookPath-consistent eligibility check (mode/`PATHEXT`)" for the detector *scan*, but FR-013's pre-spawn check for *validate* does not restate it, so an implementer may apply a Unix mode check that is a no-op on Windows.
- **Recommendation**: In FR-013, define the check as "LookPath-consistent: on Unix, regular file with any exec bit; on Windows, a file whose extension is in `PATHEXT`," reusing the same predicate as the detector (MAJ-004 makes this run on the resolved path).

---

### Observations

#### [OBS-001] The sibling `runner/test` endpoint is the same threat, left unhardened

- **Lens**: Insecurity (scope coherence)
- **Affected section**: "conntest / runner-test are reused unchanged"; FR-013 rationale ("it spawns a caller-supplied path").
- **Suggestion**: `POST /agents/{id}/runner/test` (`testAgentRunner`, `rest.go:1142`) already spawns the operator-supplied `executor.cli_path` via `--version`, and is `withAuth`-only — **no rate limit, no audit, no regular-file pre-check**. Every security argument FR-013 makes about `cli-validate` applies equally here, so the hardening leaves an open side door. Consider whether `runner/test` should share the new limiter/audit, or note explicitly why the create-time endpoint is hardened while the post-create one is not.

#### [OBS-002] The per-caller concurrency cap may be gold-plating

- **Lens**: Overcomplexity
- **Affected section**: FR-013 concurrency cap.
- **Suggestion**: `cli-validate` is already `withAuth` (create-parity), rate-limited, 15s-bounded, shell-free, `--version`-only, on an admin-trusted path the ADR argues has "no new blast radius" (R2). Layering a bespoke per-caller in-flight semaphore — a concept the existing infra does not support (MAJ-002) — on top of that is a new mechanism for a threat the rate limiter + timeout already largely bound. If MAJ-005 is fixed by keying the rate limiter on user identity, the concurrency cap likely earns its keep only as belt-and-suspenders; weigh dropping it to reduce net-new surface.

---

## Structural Integrity

### Variant A: Plan-Spec Format

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1…US-6 all have ACs. |
| Every acceptance scenario has BDD scenarios | FAIL | US-1 AC-3, US-4 AC-2, US-5 AC-1 have no BDD (MIN-002). |
| Every BDD scenario has `Traces to:` reference | PASS | All Gherkin blocks carry a `# Traces to:` comment. |
| Every BDD scenario has a test in TDD plan | PASS | Added rows map to tests 22/24/25/26; core scenarios to 1-20. |
| Every FR appears in traceability matrix | PASS | FR-001…FR-019 all present (FR-019 out of order but present). |
| Every BDD scenario in traceability matrix | PARTIAL | Matrix keys off FR/US rather than scenario name — acceptable but not 1:1. |
| Test datasets cover boundaries/edges/errors | PASS | D-1/D-2/D-3 cover OS, classification, path boundaries; add whitespace (MIN-005) and bare-name (MAJ-004) rows. |
| Regression impact addressed | FAIL | Under-counts consumers/tests (MAJ-006). |
| Success criteria are measurable | FAIL | SC-003 malformed; US-3 independent test host-dependent (MIN-004). |

### Test Coverage Assessment

#### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Real-conntest handshake | No test exercises the *real* `extractVersion` against a non-CLI binary that prints non-version output (e.g. `ls --version`); test 9 uses a fake that hides MAJ-003. | US-3 AC-2, D-2.2, SC-010 |
| Bare-name override | No dataset row / test for a `$PATH`-resolvable bare name (`claude`) that MAJ-004 would false-block. | US-4 |
| Per-identity throttle | Test 22 asserts "429 past the limiter" but not per-user vs per-IP keying (MAJ-005) nor a defined concurrency cap (MAJ-002). | FR-013, SC-008 |
| Whitespace path | No row for `"   "` → `missing-binary` (MIN-005). | FR-014, D-3 |

#### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| D-3 (path input) | Whitespace-only value | Add `"   "` → `missing-binary` (handler trims first). |
| D-3 (path input) | Bare name on `$PATH` | Add `"claude"` (on `$PATH`) → `ok`/`unauthenticated`, not `missing-binary`. |
| D-2 (classification) | Runnable non-CLI with non-empty output | Add `/bin/ls`-style binary → document the real result under reused-as-is conntest. |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `POST /system/cli-validate` | risk | ok | ok | ok | **risk** | ok | XFF-spoofable per-IP limiter + undefined concurrency cap → authenticated DoS via 15s subprocess spawns (MAJ-005/MAJ-002); audit + create-parity address R/E. |
| `GET /system/cli-detect` | ok | ok | ok | ok | ok | ok | Read-only, no subprocess, `withAuth`; correctly unaudited. |
| `pkg/clidetect` scanner | ok | ok | ok | ok | ok | ok | Fixed dir list, single-level stat, no shell, HOME-unset tolerant (FR-016). Bounded cost. |
| `runner/test` (sibling, untouched) | ok | ok | **risk** | ok | **risk** | ok | Spawns same user path, no audit/throttle/pre-check (OBS-001). |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable

---

## Unasked Questions

1. What identity dimension defines a "caller" for the concurrency cap and the rate limit — the authenticated user from `withAuth` context, or the (spoofable) client IP? (MAJ-002/MAJ-005)
2. Does the regular-executable-file pre-check run before or after `LookPath` resolution, and how are bare-name `$PATH` overrides handled? (MAJ-004)
3. Given `conntest` is reused verbatim, what is the *actual* pass/fail boundary — "returns a valid version" or "runs and prints any non-empty line"? Which do US-3 AC-2 and SC-010 mean? (MAJ-003)
4. Is direct (non-reverse-proxied) exposure of the gateway a supported deployment for this endpoint, given `clientIP` trusts `X-Forwarded-For`? (MAJ-005)
5. Why is `runner/test`, which spawns the same user-supplied path, exempt from the FR-013 hardening? (OBS-001)
6. After the wire restructure, does `AgentListScreen`'s `defaultHostClis` fallback (used on detect failure) map cleanly to the per-CLI object shape, and what does the greyed-out UI show when `source`/`path` are null but `installed` is false? (MAJ-006)

---

## Verdict Rationale

The spec is mature on intent, security framing, and traceability after four prior rounds, but it has not been reconciled against the code it will compile into. Three of the six majors are hard implementability blockers: MAJ-001 (the prescribed `validateLimiter` name is already taken by a different endpoint — build break or silent coupling), MAJ-002 (the "per-caller concurrency cap" reuses a primitive that provides neither concurrency limiting nor per-caller keying, and "caller" is undefined), and MAJ-003 (the reused-as-is `conntest` provably does not enforce the "valid version" handshake the spec promises, so US-3 AC-2 / D-2.2 fail on a real host). MAJ-004 and MAJ-005 are correctness/security defects that would ship a false-blocking create flow and an authenticated DoS vector, and MAJ-006 will red the vitest gate the spec itself requires (SC-007). None rises to CRITICAL because the endpoint is authenticated (create-parity) and shell-free — no unauthenticated RCE or data-loss path — hence REVISE, not BLOCK. Every major is grounded in a cited symbol on the branch and is fixable with spec-text changes (plus, for MAJ-003, an explicit decision on whether to weaken the wording or actually change `extractVersion`).

### Recommended Next Actions

- [ ] Rename the new limiter to `cliValidateLimiter` with a concrete config; verify no collision with `validateLimiter` (MAJ-001).
- [ ] Define "caller" and specify the concurrency cap as a real primitive keyed on authenticated identity, or drop it (MAJ-002 / OBS-002).
- [ ] Reconcile FR-015/SC-010/D-2.2/US-3 AC-2 with actual `conntest` behavior; state the true pass boundary and add a real-binary test (MAJ-003).
- [ ] Specify LookPath-first, then eligibility-check-on-resolved-path; add a bare-name `$PATH` override dataset row (MAJ-004).
- [ ] Key the validate throttle on authenticated identity (not spoofable IP), or document the trusted-proxy deployment assumption; add a per-identity throttle test (MAJ-005).
- [ ] Enumerate every `CliDetect` consumer (`AgentListScreen.tsx` + `defaultHostClis` adapter, `AgentListScreen.test.tsx`, `AgentListScreen.auth.test.tsx`, `api.cli-detect.test.ts`) in Symbols + Regression (MAJ-006).
- [ ] Fix stale paths/line/route refs; add BDD scenarios for US-1 AC-3, US-4 AC-2, US-5 AC-1; correct SC-003 and the US-3 independent test; add whitespace-path handling; define executable-eligibility cross-platform (MIN-001…MIN-006).
