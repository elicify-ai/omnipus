# Adversarial Review: External-Executor CLI Path Detection, Prefill & Validation (Round 4)

**Spec reviewed**: `docs/internal/specs/external-executor-cli-path-detection-spec.md`
**Review date**: 2026-07-02
**Verdict**: BLOCK

## Executive Summary

Three grill rounds resolved most first-order issues, but the Round-2/Round-3 decision flips (admin-only → create-parity; identity-match → no-identity-match; add basename constraint) were applied to the normative body **without updating the derived artifacts** (TDD plan, Success Criteria, datasets, traceability, regression section). The result is a spec that contradicts itself on authorization and on the meaning of "wrong binary," and that has introduced a pre-spawn rejection path (FR-013) with **no defined `reason`, response, or reconciliation** against symlink-resolved and Windows paths. Implementing to the concrete verification artifacts (tests + SC) would ship the wrong authorization model and false-reject legitimately-detected CLIs.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 6 |
| MINOR | 5 |
| OBSERVATION | 3 |
| **Total** | **16** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Authorization model contradicts itself: create-parity (FR-013) vs admin-only (tests + SC + matrix)

- **Lens**: Inconsistency / Incorrectness
- **Affected section**: FR-013; Integration Boundaries ("create-parity with `createAgent`"); vs. TDD Test 21 `TestValidate_AdminOnly` ("Non-admin `user` → 403"); SC-008 ("returns 403 for a non-admin `user` role"); Traceability Matrix FR-013 row ("validate is admin-only").
- **Description**: FR-013 and Integration Boundaries state, per an explicit operator decision ("anyone who can add the assistant"), that `cli-validate` is gated `withAuth` at **create-parity** and is **"not an admin route."** But the TDD plan, SC-008, and the traceability matrix all assert the endpoint is **admin-only** and returns **403 to a non-admin `user`**. Verified against code: `createAgent` is registered `a.withAuth(a.HandleAgents)` (`rest.go:3929`) with **no** `RequireAdmin` (admin uses `adminWrap` = `withAuth → RequireAdmin → RequireNotBypass`, `rest.go:351`), and `withAuth` (`rest.go:340`) checks authentication only, not role. `config.UserRoleUser` ("user") is a real role (`config/config.go:2434`). Therefore create-parity means a non-admin `user` **must be allowed** (2xx/warn), and a test asserting `403` for that user is not merely stale — it encodes the opposite of the operator's decision.
- **Impact**: A developer implements to the tests/SC (the executable artifacts), gates the endpoint admin-only, and returns 403 to exactly the non-admin operator the operator wanted to enable. The e2e/UX ships blocking the create flow for `user`-role operators. The contradiction is invisible until a non-admin hits it in production.
- **Recommendation**: Delete the admin-only framing everywhere. Rename Test 21 `TestValidate_AdminOnly` → `TestValidate_RequiresAuth`, and change its assertion to: unauthenticated → 401; authenticated `user` → **allowed (2xx)**; keep the "non-regular/non-executable/basename-mismatch target rejected" assertion but see MAJ-001 for its reason/status. Rewrite SC-008 to "`cli-validate` returns 401 unauthenticated and is reachable by an authenticated non-admin `user` (create-parity); returns 429 past its rate limit (threshold per MAJ-004)." Fix the FR-013 traceability row to drop "admin-only."

#### [CRIT-002] The "wrong binary rejected" case is internally contradictory and has no correct outcome

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: Dataset D-2.6 (`cli_path=/usr/bin/node`, `cli=claude-code` → `handshake-failed`); BDD "Wrong binary rejected — claude-code → /usr/bin/node → handshake-failed `[FR-015]`"; vs. FR-015 / Test 24 `TestValidate_RunsNoVersion` ("a runnable binary returning any valid version passes"); vs. F-03 ("a mis-pointed but runnable binary is accepted").
- **Description**: FR-015 (operator decision, F-03) removed identity matching: the handshake passes for **any** binary that runs and emits a valid version. `node --version` prints `v20.x` — a valid version shape — so per FR-015/Test 24, `/usr/bin/node` **passes** (→ `ok`/`unauthenticated`). Yet D-2.6 and the "Wrong binary rejected" BDD assert `/usr/bin/node` → `handshake-failed` and tag it `[FR-015]`. The only mechanism that could reject `node` is the FR-013 **basename** constraint (`node` ≠ `claude`) — but that rejects *pre-spawn*, so the reason is **not** `handshake-failed`, and the basename-rejection reason is undefined (see MAJ-001). The flagship "reject the wrong binary" example thus has **no self-consistent outcome**: FR-015 says accept, D-2.6/BDD say handshake-failed, FR-013 says pre-spawn-reject-with-undefined-reason.
- **Impact**: `conntest` is reused as-is (`conntest.go:146`), which returns `handshake-failed` only when `--version` fails to run or parse (`probeVersion`, `conntest.go:213`). A runnable versioned binary (node, python, any wrapper) will **not** produce `handshake-failed`. A test written from D-2.6 (`node → handshake-failed`) will **fail** against the reused code, or force the implementer to add the very identity check F-03 forbade. Either way the spec cannot be implemented as written.
- **Recommendation**: Pick one. If the intent is "any wrong binary that runs is accepted" (F-03 literally), then **remove** D-2.6 and the "Wrong binary rejected" BDD, and change US-3 AC-2 to "a binary that does **not** print a version → `handshake-failed`" (drop "non-CLI/incompatible"). If the intent is to actually reject `node`, that rejection is the **basename** path (FR-013) — then define its reason/status (MAJ-001), retag the BDD/dataset to that path (not FR-015), and use a target whose basename is `claude` but which isn't the real CLI for the true handshake-failed case (e.g. a shell script named `claude` that prints no version).

---

### MAJOR Findings

#### [MAJ-001] FR-013 pre-spawn rejections have no `reason`, `ok`, `detail`, or HTTP status defined

- **Lens**: Incompleteness / Ambiguity
- **Affected section**: FR-013 ("reject a target that is not a regular executable file, or whose basename does not match the selected CLI's expected binary … before spawn"); Wire schema `CliValidateResponse.reason enum[ok, missing-binary, handshake-failed, unauthenticated, unknown-cli]`; Per-reason response table.
- **Description**: FR-013 adds two rejection conditions (non-regular-file; basename mismatch) evaluated **before** the conntest spawn. Neither maps to any member of the `reason` enum, and the per-reason table has no row for them. Is a basename mismatch `handshake-failed`? `missing-binary`? A raw `400`? What `ok`/`detail`/`resolved_path` do these carry? Undefined. Test 21 references "non-regular/non-executable target rejected" but never states the expected response.
- **Impact**: Two competent engineers implement two different behaviours (one returns 400, one returns `missing-binary`, one invents `invalid-target`). The SPA's blocking logic (gate on `reason ∈ {missing-binary, handshake-failed}`, FR-018) will not fire for an out-of-enum reason, so a rejected-but-unclassified target may silently **allow** Create — the opposite of the intent.
- **Recommendation**: Add an explicit reason for pre-spawn policy rejection (e.g. extend the enum with `invalid-target`) and add a per-reason table row: `ok=false`, `resolved_path=null`, `version=null`, `detail="path is not the expected <cli> executable"`, HTTP 200 (classified, like the others) — and add it to FR-008/FR-018's blocking set so the SPA blocks Create. Add a dataset row for a basename-mismatch and a directory target with the definitive reason.

#### [MAJ-002] Basename constraint (FR-013) collides with symlink resolution (FR-001) — false-rejects npm-global installs, the exact case US-2 targets

- **Lens**: Incorrectness
- **Affected section**: FR-001 ("`path` is absolute, symlinks resolved"); §6 detector step 2 (`filepath.EvalSymlinks` + `Abs`); F-02/FR-014 ("validation resolves exactly `cli_path`"); FR-013 basename check.
- **Description**: Detection prefills the **EvalSymlinks-resolved** absolute path (FR-001, US-2 AC-3). For an npm-global install, `claude` is a shim symlink into the package (`~/.npm-global/bin/claude` → `../lib/node_modules/@anthropic-ai/claude-code/cli.js` or similar); EvalSymlinks resolves the prefilled path to a target whose **basename is not `claude`** (e.g. `cli.js`). Validation then applies FR-013's `basename == claude` check to that prefilled value and **rejects it**. The detect↔runtime "symmetry" (F-02) guarantees the prefilled path is exactly what validation sees, so this is deterministic, not incidental.
- **Impact**: The primary root-cause scenario (US-2: CLI installed via npm-global/Homebrew outside `$PATH`) auto-prefills a path that its own validation then blocks with an undefined-reason rejection (MAJ-001). Create is blocked for a correctly-installed CLI — a direct regression of the feature's stated purpose (NFR-1, SC-001).
- **Recommendation**: Reconcile the two. Either (a) validate the basename **before** symlink resolution / against the un-resolved link basename, or (b) drop symlink resolution in prefill and store the link path (basename `claude`) while resolving only at spawn, or (c) drop the basename constraint (the operator already accepted "a mis-pointed but runnable binary is accepted," F-03 — which undercuts the basename check's rationale entirely). Decide explicitly and encode a dataset row for an npm-shim symlink through validation.

#### [MAJ-003] Basename constraint ignores Windows `PATHEXT` — breaks US-6 AC-1 at validation

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: FR-013 ("basename … match the selected CLI's expected binary (`claude`/`codex`/`opencode`)"); FR-003 / US-6 AC-1 (Windows npm shim `claude.cmd` via `PATHEXT`).
- **Description**: On Windows, detection finds and prefills `claude.cmd`/`claude.exe` (US-6 AC-1, D-1.6). FR-013's basename check compares against the bare names `claude`/`codex`/`opencode` with **no** `PATHEXT`-aware stripping. `basename("claude.cmd") == "claude.cmd" != "claude"` → the prefilled Windows path is rejected by its own validation.
- **Impact**: Windows external-executor creation is blocked for every npm/shim install — the exact cross-platform case US-6 exists to support. The Windows tests are table-driven to run on Linux CI (F-11), so this fails in CI too once the basename dataset is added.
- **Recommendation**: Specify that the basename match strips OS executable extensions (`PATHEXT` on Windows, mirroring the `LookPath`-consistent eligibility check in F-09): accept `claude`, `claude.cmd`, `claude.exe`, `claude.bat`, `claude.ps1`. Add a Windows basename dataset row.

#### [MAJ-004] Rate-limit threshold and concurrency-cap scope/response are unspecified → SC-008 not measurable, self-DoS risk

- **Lens**: Ambiguity / Infeasibility / Insecurity (DoS)
- **Affected section**: FR-013 ("a **dedicated** rate limiter"; "cap concurrent in-flight validations (≤2)"); SC-008 ("429 past its dedicated rate limit"); Test 22 `TestValidate_RateLimited`.
- **Description**: No requests/window number is given for the "dedicated" limiter (contrast the existing named limiters: `validateLimiter = 30/min`, `onboardingCompleteLimiter = 3/min`, `rest_auth.go:181-191`). The concurrency cap "≤2" has **no defined scope** (global process-wide vs per-IP) and **no defined response** on the 3rd concurrent call (429? 503? block-and-wait?). Because the probe timeout is 15s (`versionProbeTimeout`, `conntest.go:38`), a **global** cap of 2 means two hung `--version` probes block **all** operators for up to 15s each — a self-inflicted DoS from a slow CLI.
- **Impact**: SC-008 ("429 past its dedicated rate limit") is unmeasurable without a threshold (AMB-03/FEA-04). Test 22 cannot be written deterministically. The concurrency cap, if global, degrades availability under concurrent legitimate use.
- **Recommendation**: State the exact limit (e.g. "10 requests/minute per IP", named `cliValidateLimiter`, distinct from the existing `validateLimiter`). State the concurrency-cap scope (recommend per-IP) and the over-cap response (429 with `Retry-After`, consistent with `withRateLimit`). Note the 15s-timeout interaction and cap accordingly.

#### [MAJ-005] Regression section contradicts the Round-3 "conntest unchanged" decision

- **Lens**: Inconsistency
- **Affected section**: Regression Test Requirements §2 ("Existing tests to update: … `conntest`/`runner-test` tests (**now identity-checked**)"); vs. Regression Impact Summary ("`conntest` / `runner-test` are **reused unchanged** (no identity extension)") and F-03/FR-015.
- **Description**: F-03/FR-015 (operator, Round 3) reverted the identity-matcher idea: `conntest` is reused as-is, `runner-test` untouched. The Regression Impact Summary and Available Reference Patterns agree. But Regression Test Requirements §2 still instructs updating conntest/runner-test tests "now identity-checked" — a stale leftover from the reverted Round-2 direction.
- **Impact**: An implementer following the regression checklist modifies `conntest`/`runner-test` (adding an identity check) that the decision explicitly says stays unchanged — reintroducing the reverted behaviour and breaking `POST /agents/{id}/runner-test` semantics for existing native/external agents.
- **Recommendation**: Rewrite Regression Test Requirements §2 to "conntest / runner-test are unchanged and their existing tests are preserved as-is (no identity extension — F-03)." Remove "now identity-checked" everywhere.

#### [MAJ-006] `detail` for `handshake-failed` asserts identity semantics that were removed; SC-012 "fixed message" is actually a per-CLI template

- **Lens**: Incorrectness / Inconsistency
- **Affected section**: Per-reason table (`handshake-failed` → detail `"did not identify as <cli>"`); SC-012 ("detail equals one of the fixed allowlisted messages … equality-checked"); vs. FR-015 (no identity match).
- **Description**: With identity matching removed (FR-015), `handshake-failed` means "did not run / emitted no parseable version" — it does **not** mean "identified as the wrong CLI." The detail string `"did not identify as claude"` therefore describes a check that no longer exists and misleads the operator (the real remedy is "the binary didn't run or print a version," not "it's the wrong tool"). Separately, SC-012 says `detail` is a **fixed** allowlisted string checked by equality, but the table value embeds a `<cli>` placeholder, making it a per-CLI template — an equality check needs the concrete per-CLI strings enumerated.
- **Impact**: SC-012's equality assertion is unimplementable against a placeholder; the operator-facing message points at the wrong diagnosis.
- **Recommendation**: Change the `handshake-failed` detail to something accurate and CLI-agnostic, e.g. `"binary did not run or report a version"`. Enumerate the exact fixed strings per reason (and per CLI where a name appears) so SC-012's equality check has a concrete allowlist.

---

### MINOR Findings

#### [MIN-001] `ok` field mapping vs `conntest.OK` is unspecified and semantically muddled

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: Per-reason table (`unauthenticated` → `ok:true`); vs. `conntest.go` (`unauthenticated` returns `OK:false`); "`ok` … MUST NOT drive gating (G2-07)."
- **Description**: The reused `TestConnectionWithPath` returns `OK:false` for `unauthenticated`, but the wire table wants `ok:true`. The handler must therefore **override** `conntest.OK`, which the spec never states — an implementer may pass `conntest.OK` through and emit `ok:false` for `unauthenticated`, contradicting the table. Also, defining `ok:true` for a warn state (`unauthenticated`) means `ok` = "create allowed," i.e. it *is* gating semantics, contradicting "`ok` must not drive gating."
- **Recommendation**: State explicitly that `ok` is **derived** (`ok = reason ∈ {ok, unauthenticated}`), not a passthrough of `conntest.OK`, or drop `ok` from the wire entirely and gate purely on `reason` (per FR-018) to remove the ambiguity.

#### [MIN-002] Acceptance scenarios without BDD coverage

- **Lens**: Incompleteness (structural)
- **Affected section**: US-1 AC-3 (not-found → empty + amber hint); US-4 AC-2 (typed path after empty detection is validated); US-5 AC-1 (edit form re-offers detected path when `cli_path` empty); US-6 AC-2 (Homebrew `/opt/homebrew/bin` scanned).
- **Description**: These acceptance scenarios have TDD-plan tests but no Given/When/Then BDD scenario, so the plan-spec structural rule "every acceptance scenario has a BDD scenario" fails for four cases.
- **Recommendation**: Add four short BDD scenarios (or explicitly fold them into existing outlines) so acceptance→BDD traceability is complete.

#### [MIN-003] "Dedicated" limiter naming collides with the existing `validateLimiter`

- **Lens**: Ambiguity / Inoperability
- **Affected section**: FR-013 "dedicated rate limiter"; existing `validateLimiter` (`rest_auth.go:181`, `/api/v1/auth/validate`, 30/min).
- **Description**: A global `validateLimiter` already exists for `/auth/validate`. The spec's "dedicated" cli-validate limiter needs a distinct symbol and value; unnamed, it invites reuse of the existing one (wrong scope/threshold) or a confusing near-duplicate name.
- **Recommendation**: Name it (e.g. `cliValidateLimiter`) with its own threshold (MAJ-004) and state it is separate from `validateLimiter`.

#### [MIN-004] SC-003 is grammatically broken and unmeasurable

- **Lens**: Infeasibility (measurability)
- **Affected section**: SC-003 ("prefills the field in a single interaction (no manual typing) in ≥ the 3 supported CLIs").
- **Description**: "in ≥ the 3 supported CLIs" is not a well-formed threshold; it cannot be evaluated pass/fail as written.
- **Recommendation**: Rewrite: "For each of the 3 supported CLIs, when its binary is present, selecting the CLI prefills the path field in a single interaction with no manual typing (3/3)."

#### [MIN-005] Whitespace-only path and directory-vs-basename precedence unspecified

- **Lens**: Incompleteness
- **Affected section**: Dataset D-3 (D-3.1 empty; D-3.4 directory; D-3.5 leading/trailing whitespace); FR-013 basename + regular-file checks.
- **Description**: A whitespace-only `cli_path` ("   ") is not enumerated (post-trim → empty → `missing-binary`, presumably). For a directory (D-3.4 `/opt/x/`), both the "not a regular executable file" check and the basename check (basename `x` ≠ `claude`) fail — the spec doesn't say which rejection wins or what reason surfaces (ties into MAJ-001).
- **Recommendation**: Add a whitespace-only row; state evaluation order for the FR-013 checks (recommend: trim → empty→`missing-binary` → regular-file → basename) and the single reason each yields.

---

### Observations

#### [OBS-001] Breaking `CliDetect` restructure has no stale-SPA story beyond enum values

- **Lens**: Inoperability
- **Affected section**: FR-011 (boolean→object restructure); Round-1 minors ("SPA tolerates unknown `source`/`reason` values").
- **Suggestion**: The stale-bundle tolerance covers unknown enum values, not the structural change (`hasClaude:bool` → `{installed,path,source}`). An already-loaded old SPA calling the new endpoint gets a shape it cannot parse. For a single-binary app this is a hard-refresh nuisance; note the expectation (or keep additive `*_path` fields per the ADR §6 fallback) so a mid-deploy open of the roster screen degrades gracefully rather than throwing.

#### [OBS-002] TOCTOU between validate and the real spawn

- **Lens**: Insecurity (Tampering)
- **Affected section**: FR-007 (validate spawns `--version` at create); runtime spawn occurs later.
- **Suggestion**: Validation proves the binary at `cli_path` ran `--version` at create time; nothing pins it for the later real spawn (the file can be swapped). Admin-trusted config makes this low-risk — worth a one-line acknowledgement that validation is a create-time diagnostic, not a runtime guarantee (the spec's G2-13 "stricter than runtime" note gestures at this but doesn't name the TOCTOU).

#### [OBS-003] No feature flag / rollback note for the new endpoint + wire change

- **Lens**: Inoperability
- **Affected section**: whole spec (deployment).
- **Suggestion**: The endpoints are additive and diagnostic, so a flag is likely overkill (avoid CPX-08 over-engineering). But the `CliDetect` wire restructure is the one not-trivially-reversible change in a running deploy; a sentence on the rollback posture (revert the binary; SPA and gateway ship together) would close the OPS-03 gap without adding a flag.

---

## Structural Integrity (Plan-Spec Format)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1…US-6 all have ACs. |
| Every acceptance scenario has BDD scenarios | FAIL | US-1 AC-3, US-4 AC-2, US-5 AC-1, US-6 AC-2 lack BDD scenarios (MIN-002). |
| Every BDD scenario has `Traces to:` reference | PASS | All BDD blocks carry a `Traces to:` comment. |
| Every BDD scenario has a test in TDD plan | PASS (with caveat) | "Wrong binary rejected" BDD maps to Test 24, but the mapping is self-contradictory (CRIT-002). |
| Every FR appears in traceability matrix | PASS | FR-001…FR-019 all present. |
| Every BDD scenario in traceability matrix | PARTIAL | Matrix is FR-keyed, not BDD-keyed; several BDDs are only implicitly covered (e.g. symlink, PATH-wins). |
| Test datasets cover boundaries/edges/errors | PARTIAL | D-1/D-2/D-3 broad, but no row for basename-mismatch, non-regular-file, npm-symlink-through-validate, or whitespace-only path (MAJ-001/MAJ-002/MIN-005). |
| Regression impact addressed | FAIL | §2 contradicts the Impact Summary and F-03 (MAJ-005). |
| Success criteria are measurable | FAIL | SC-008 lacks a threshold (MAJ-004); SC-003 malformed (MIN-004); SC-012 references a placeholder as a "fixed" string (MAJ-006). |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|--------------------|
| Authorization | No test for "authenticated non-admin `user` is **allowed**" (the create-parity contract); the only role test asserts the opposite (403). | CRIT-001, FR-013 |
| Pre-spawn rejection | No test defines the response for basename-mismatch / non-regular-file rejection; Test 21 asserts "rejected" without a reason/status. | MAJ-001 |
| Symlink × validation | No integration test drives an npm-shim symlink (resolved by detect) **through** cli-validate to prove it is accepted, not basename-rejected. | MAJ-002 |
| Windows validation | Windows detection is table-tested, but no test drives `claude.cmd` through the FR-013 basename check. | MAJ-003 |
| Concurrency | Test 22 references a concurrency cap with no defined scope/response/threshold. | MAJ-004 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| D-2 | Runnable non-CLI that DOES print a version (e.g. `node`) | Decide accept (FR-015) vs reject (basename) and encode the definitive reason (CRIT-002/MAJ-001). |
| D-3 | Basename mismatch; non-regular-file; npm-symlink target; whitespace-only | Add rows with the single reason each yields and the check-order (MIN-005). |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| `GET /system/cli-detect` | ok | ok | ok | risk | ok | ok | `withAuth` ok; unaudited by design ok; but discloses installed-binary filesystem paths to any authenticated user (low). |
| `POST /system/cli-validate` | ok | risk | ok | risk | risk | risk | Spawns caller-supplied path (no-shell + basename mitigations, but basename underspecified — MAJ-001/002/003); audited (good); `detail` sanitized (good) but `resolved_path` returned to caller discloses FS layout; DoS via unspecified rate/concurrency (MAJ-004); **E: create-parity intended but tests assert admin-only — CRIT-001**. |
| `pkg/clidetect` | ok | ok | ok | risk | ok | ok | Bounded scan (good); path disclosure only. |

**Legend**: risk = identified threat not fully mitigated in spec; ok = adequately addressed or n/a.

---

## Unasked Questions

1. **Is `cli-validate` admin-only or create-parity?** The normative FRs say create-parity (non-admin allowed); the tests, SC, and matrix say admin-only (403 for `user`). Which is it? (CRIT-001)
2. **What `reason`, `ok`, `detail`, and HTTP status does a basename-mismatch or non-regular-file rejection return?** The enum has no member for it, so the SPA's blocking logic can't classify it. (MAJ-001)
3. **How is the basename check reconciled with (a) EvalSymlinks-resolved npm-shim paths whose basename isn't the CLI name, and (b) Windows `claude.cmd`/`.exe` extensions?** As written it rejects both. (MAJ-002, MAJ-003)
4. **What is the exact rate-limit threshold (N/window) and the concurrency-cap scope (global vs per-IP) and over-cap response?** SC-008 and Test 22 are otherwise unmeasurable, and a global cap + 15s timeout is a self-DoS. (MAJ-004)
5. **Given FR-015 (no identity match), is a runnable non-CLI binary like `node` accepted or rejected?** D-2.6 says rejected; FR-015/Test 24 say accepted. (CRIT-002)
6. **Is `ok` derived by the handler or passed through from `conntest.OK`?** They disagree for `unauthenticated`. (MIN-001)
7. **During a rolling deploy, how does an already-loaded old SPA cope with the restructured `CliDetect` object shape?** (OBS-001)

---

## Verdict Rationale

BLOCK. Two CRITICAL findings make the spec non-implementable as written: the authorization model contradicts itself between the normative FRs (create-parity, verified reachable by non-admin `user` in code) and the verification artifacts (admin-only, 403) — CRIT-001 — and the flagship "wrong binary rejected" case has no self-consistent outcome across FR-015, D-2.6, and FR-013 — CRIT-002. Both are direct consequences of applying the Round-2/Round-3 decision reversals to the spec body without propagating them into the TDD plan, SC, datasets, and regression section. The MAJOR cluster (MAJ-001…MAJ-003) shows the newly-added FR-013 basename/regular-file constraint was bolted on without an owning `reason` value and without reconciling against symlink resolution or Windows extensions, so it will false-reject exactly the well-known-install and cross-platform cases the feature exists to fix. These must be resolved before implementation.

### Recommended Next Actions

- [ ] Resolve the authorization contradiction end-to-end: FR-013 wording, Test 21, SC-008, traceability all say create-parity (non-admin allowed). — CRIT-001
- [ ] Choose accept-or-reject for a runnable non-CLI binary and make FR-015 / D-2.6 / the BDD agree; retag away from `[FR-015]` if it's a basename rejection. — CRIT-002
- [ ] Add a `reason` (e.g. `invalid-target`) with full per-reason row + HTTP status for pre-spawn rejections, and add it to the SPA blocking set. — MAJ-001
- [ ] Reconcile the basename check with EvalSymlinks-resolved npm paths and `PATHEXT` extensions; add datasets driving both through validation. — MAJ-002, MAJ-003
- [ ] Specify the rate-limit threshold, the concurrency-cap scope + over-cap response, and the 15s-timeout interaction. — MAJ-004
- [ ] Rewrite Regression Test Requirements §2 to match "conntest/runner-test unchanged." — MAJ-005
- [ ] Fix the `handshake-failed` detail wording and enumerate the fixed per-reason `detail` strings for SC-012. — MAJ-006
- [ ] Address MIN-001…MIN-005 (ok-derivation, missing BDDs, limiter naming, SC-003 phrasing, path-check ordering).

---

Verdict: REVISE is **not** appropriate — the two CRITICALs make this **BLOCK**.

To address these findings, run:
  `/plan-spec --revise docs/internal/specs/external-executor-cli-path-detection-spec.md docs/internal/specs/external-executor-cli-path-detection-spec-review-round4.md`
