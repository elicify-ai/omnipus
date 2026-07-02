# Grill Report (Round 2): External-Executor CLI Path Detection

- **Spec:** `external-executor-cli-path-detection-spec.md` (revised post-Round-1) · **Mode:** plan-spec · **Verdict: BLOCK**
- **Findings:** 1 CRITICAL, 5 MAJOR, 5 MINOR, 2 OBSERVATION. All code-verified.

## Executive summary
Round-1 fixes were bolted on as a "Post-Grill Revisions" preamble claiming to *supersede conflicting text below* — but the conflicting text was left in place, so the spec contradicts itself on audit and the empty-path classification. FR-013…FR-018 were never wired into the Traceability Matrix, TDD plan, or SCs → the security-critical additions are **specified but untested**. F-01 got 2 of its 3 mitigations (audit+rate-limit) but not target-constraint/RBAC — and a non-admin `user` role can reach the plain-`withAuth` endpoint.

## Findings
- **G2-01 CRITICAL** — `cli-validate` executes a caller-supplied absolute path (`execve(<any>, "--version")`) reachable by non-admin `UserRoleUser` (roles at `config.go:2433-2434`; high-blast endpoints use `RequireAdmin`, this uses bare `withAuth`). Rate-limit caps request rate, not concurrent held processes (blocking FIFO / interactive binary hangs 15s). FR-015 identity matcher runs *after* the arbitrary exec. **Fix:** `RequireAdmin` (or agent-create's authz), reject non-regular/non-executable targets pre-spawn, cap concurrent in-flight; state the RBAC role.
- **G2-02 MAJOR** — Preamble says audit "detection only" but Non-Behaviors L192 + Integration L206 still say validation is "unaudited". Edit the body lines.
- **G2-03 MAJOR** — FR-014 says empty→missing-binary but D-3.1 still says empty→"$PATH default"; and `resolveCLIBinary` (`executor_opts.go:50`) $PATH-falls-back on empty. Handler must short-circuit empty→missing-binary before delegating.
- **G2-04 MAJOR** — FR-015 identity matcher requires modifying `conntest`'s handshake (`probeVersion`/`extractVersion`), but spec claims conntest "reused verbatim/unchanged" in 5 places; matchers undefined. Decide: extend conntest (drop verbatim claims, runner-test gains identity) or capture banner in the handler; define each matcher string.
- **G2-05 MAJOR** — FR-013…FR-018 absent from matrix (stops at FR-012), TDD plan (still 20 tests), and SCs. SC-007 "all 20 tests" cannot cover rate-limit/audit/identity/HOME-unset/detail/ReasonOK. Add tests + matrix + SC rows.
- **G2-06 MAJOR** — "sole consumer = AgentListScreen" undercounts: `src/lib/api.ts` (`fetchCliDetect`, `CliDetectSchema`) + `src/lib/api.cli-detect.test.ts` (asserts boolean shape) also break. Add to impact + regression.
- **G2-07/08 MINOR** — `ok` field undefined per reason (trap: wiring button to `!ok` reintroduces the bug); `resolved_path`/`version`/`detail` per-reason population undefined. Add a 5-row per-reason table or drop `ok`.
- **G2-09 MINOR** — SC-001 still "across the D-1 matrix" (circular/fakes); SC-003 ungrammatical. Rewrite to real-host integration + countable.
- **G2-10 MINOR** — FR-013 names no limiter/bucket; detect throttling dropped. Specify a tight dedicated limiter + concurrency cap; state detect posture.
- **G2-11 MINOR** — `EvalSymlinks` error path (dangling link) unspecified. Recommend Abs of raw hit, still installed:true; add test.
- **G2-12 OBS** — `source` label convention for a symlinked PATH hit (how-located vs where-resolved).
- **G2-13 OBS** — validate (identity) is stricter than runtime (no identity) → acknowledge validate is fail-closed, not a symmetry guarantee.

## Verdict: BLOCK
Priority: G2-01 (gate + constrain), G2-04/G2-03 (handshake/empty-path story + conntest claims), then G2-02/G2-05/G2-06 (edit contradicted lines; wire FRs into matrix/TDD/SC; fix consumer list). Re-grill after.
