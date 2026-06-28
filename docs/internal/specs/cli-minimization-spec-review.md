# Adversarial Review — CLI Minimization Spec

**Spec reviewed**: `docs/internal/specs/cli-minimization-spec.md`
**Mode**: plan-spec (full BDD/TDD with FR-xxx, SC-xxx, traceability matrix)
**Reviewer stance**: Adversarial, read-only, codebase-grounded
**Date**: 2026-06-28

---

## Executive Summary

The spec is structurally complete and well-traced, and its scoping (P0/P1) is sane. But it ships **two CRITICAL defects that would make an implementing agent build a CLI that hangs on every approval and falsely claims audit attribution it cannot deliver** — both grounded in the actual gateway code, not spec prose:

1. **The approval handshake is wired to the wrong registry (CR-1).** The spec's central FR-005 design — "CLI sends `exec_approval_response{id, decision:'deny'}` over WS" — resolves the **legacy** `wsApprovalRegistry`, NOT the `approvalRegistryV2` that the live gateway boot path actually blocks an ask-policy tool on. The real gate is resolved **only** by `POST /api/v1/tool-approvals/{id}` (REST). A literal implementation of FR-005 hangs the full 90 s on every `ask` tool — the exact failure mode the spec says it exists to prevent.

2. **`user=cli` audit attribution is unachievable as specified (CR-2).** `audit.Entry` has **no user/username field**, and the WS-authenticated `userID` is not threaded into the agent loop's audit emissions. SC-004 / US-5-AC-3 ("audit attributes `user=cli`") cannot pass against today's code without new plumbing the spec does not scope.

Beyond those: the **removals list is materially incomplete** (3 kept commands undeclared, ~12 doc/source files with removed-verb references unlisted, grep-guard regex misses `model`/`skills`), and the **P1 health-poll targets a `/ready` that goes green before the WS listener binds** (race). Several FRs have no negative test.

**Findings**: 2 CRITICAL, 7 MAJOR, 6 MINOR, 3 OBSERVATION.

**Verdict: BLOCK.** CR-1 and CR-2 invalidate the spec's two headline guarantees (no-hang approvals, `cli`-attributable audit). Both are fixable with bounded changes, but they must be corrected before any agent implements from this spec.

---

## Findings Table

| ID | Severity | Lens | Section | Description | Fix |
|----|----------|------|---------|-------------|-----|
| CR-1 | CRITICAL | Incorrectness / Insecurity | FR-005, US-4, Ref-Patterns L19, BDD "denied-and-continue", Integration Boundaries | Approval design uses the wrong frame pair and the wrong registry — the CLI as specified cannot resolve the blocking ask-policy approval and will hang 90 s. | Re-design FR-005 to: listen for `tool_approval_required` WS frame, extract `approval_id`, and resolve via `POST /api/v1/tool-approvals/{id}` `{"action":"deny"\|"approve"}`. This is REST + WS, not a single WS frame. |
| CR-2 | CRITICAL | Incorrectness / Repudiation | FR-006, SC-004, US-5-AC-3, BDD "WS auth audited as cli" | `audit.Entry` has no user field; WS `userID` is not propagated to agent-loop audit entries. The `user=cli` attribution requirement cannot pass against current code. | Either (a) add a `User` field to `audit.Entry` + thread `wsConn.userID` through the turn into audit emission (new work — scope it), or (b) weaken SC-004 to what is verifiable today (the `cli` *user* authenticated; token never printed) and file a tracked issue for true attribution. Do not claim attribution the code can't deliver. |
| MAJ-1 | MAJOR | Incompleteness | FR-013, US-11, Impact Assessment | Removal list omits that `audit`, `doctor`, `version` subcommands EXIST and are kept — and omits ~10 doc files + ~7 source files (provider error strings) referencing removed verbs. | Add the kept-verb list explicitly; enumerate all doc/source files (list below) in the FR-013 update set. |
| MAJ-2 | MAJOR | Infeasibility | FR-013, SC-005, BDD "grep-guard" | Grep-guard regex `omnipus (agent\|auth\|status\|cron\|migrate)\b` MISSES `model` and `skills` (both removed). Guard would pass while dead `omnipus model`/`omnipus skills` references survive. | Use `omnipus (agent\|auth\|status\|cron\|migrate\|model\|skills)\b`. Also handle path-prefixed forms (`./omnipus`, `/usr/local/bin/omnipus`, `$BIN`). |
| MAJ-3 | MAJOR | Incorrectness / Inoperability | FR-015, US-12-AC-1, BDD "auto-start spawns" | P1 health-poll targets `/health`/`/ready`, but `/ready` flips to 200 inside `SetupHTTPServer` BEFORE the WS listener's `ListenAndServe` goroutine binds. Polling it can return ready → CLI connects → WS refused. | Poll for actual WS acceptance: TCP-connect + WS upgrade (or a real `auth` round-trip), not `/ready`. Optionally move `SetReady(true)` after `StartAll()` — but the CLI must not trust `/ready` for "WS up". |
| MAJ-4 | MAJOR | Insecurity (EoP) | FR-006, US-5, A5 | A second admin principal (`cli`) is minted with full admin role and a 0600 plaintext token. `UserConfig.Role` is NOT enforced for tool/approval gating at the gateway (cosmetic). "admin-equivalent" therefore grants the CLI the full blast radius with no scoping and no per-key revocation story beyond deleting the user. | Acceptable for v1 *if* the spec states explicitly that `Role` is currently unenforced (so admin ≈ user at the tool layer) and that revocation = remove `cli` user + rotate token. Add a negative test that a tampered/rotated token is rejected. Track scoped-role as the real EoP mitigation. |
| MAJ-5 | MAJOR | Incompleteness | Test plan #8/#11/#12, NFR/CI | Spec calls #8/#11/#12 "integration tests against a real in-process gateway" but does not state they must use a mock provider, run `-p 1`, and live where they don't link the full suite. Without this an agent will write LLM-dependent tests (need API key + network) or trip the OOM trap. | State: integration tests inject `restMockProvider`-style fake (no real LLM), run with `-p 1`, and acknowledge the single-scoped-test OOM guidance from CLAUDE.md. No real-model dependency at the integration level. |
| MAJ-6 | MAJOR | Incompleteness | FR-007, US-5-AC-4, Edge Cases | `--url` remote scoping is under-specified: spec forbids "a local token to a `--url` remote" and "any token over http://" but never says what token (if any) a remote run uses, nor whether `--url` is even in P0 scope. The single test (`TestAuth_CliTokenAuthsAndAudits` negative case) conflates two distinct rules. | Either drop `--url` from P0 (it has no happy-path story, no token source) or fully specify: remote token source, TLS enforcement, and split the negative tests (http→refuse; local-token-to-remote→refuse). Add an explicit FR for the `--url` happy path or mark it out of scope. |
| MAJ-7 | MAJOR | Inconsistency | FR-005 vs Edge Cases vs handleApprovalResponse | The `exec_approval_response` schema has no `session_id`, but the live legacy handler validates session_id when present; the V2 REST path keys on `approval_id` from the broadcast frame. Spec's `{id, decision}` shape is ambiguous about which id (the spec's `id` ≠ V2's `approval_id`/`tool_call_id`). | After adopting CR-1's fix, specify the exact field: V2 uses `approval_id` from the `tool_approval_required` frame as the REST path param. Remove references to `exec_approval_response`/`id`. |
| MIN-1 | MINOR | Ambiguity | Edge Cases, US-1-AC-3 | "Empty prompt → reject with usage; exit non-zero" but no test in the plan or matrix. | Add `TestRun_EmptyPrompt_Rejected` unit test; add to matrix under FR-001/FR-002. |
| MIN-2 | MINOR | Incompleteness | Edge Cases ("corrupt cli.token", 0644 token) | Dataset row 3 (mode 0644 → warn/refuse) has no test; corrupt/truncated token content has no handling spec. | Add a unit test for over-permissive token mode and a malformed-token-content path (auth fails → clear guidance, not a panic). |
| MIN-3 | MINOR | Incompleteness | Edge Cases | "port held by a DIFFERENT omnipus" and "multiple gateways" only covered for P1 auto-start; P0 run path doesn't say what happens if the configured port answers but is a *different* service (or a stale gateway with a mismatched token). | Add a P0 edge: WS connects but auth fails (wrong/old `cli` token) → distinguish from "gateway down"; give token-mismatch guidance, not the `omnipus start` message. |
| MIN-4 | MINOR | Ambiguity | FR-003, A-assumptions | "metadata.model_name omitted → agent's configured model" — confirmed `metadata` is freeform `map[string]any` in the contract, so a typo'd key silently no-ops. | Note that `metadata` is an open map (no schema validation of `model_name`); the CLI must set exactly `model_name`. Add an assertion in `TestWSFrameCodec`. |
| MIN-5 | MINOR | Overcomplexity | FR-016, gateway.lock | `gateway.lock` (flock) to serialize auto-start is redundant with the TCP port bind (only one process can bind the port) and flock is a **no-op on Windows** in this repo (`fileutil/flock_windows.go`). | Drop the separate lock or justify it as a *pre-spawn fast-path* only; rely on port-bind as the true mutex; document the Windows no-op. Don't test a guarantee flock can't give on Windows. |
| MIN-6 | MINOR | Infeasibility | Test #15 grep-guard as a Go test | A Go test shelling `grep` over the repo depends on CWD/repo-root and is fragile cross-platform; precedent for this exact pattern is thin. | Implement the guard as a CI/shell step (pr.yml + runci.sh) primarily; if a Go test mirror is wanted, resolve repo root via go.mod walk and gate it `//go:build linux`. |
| OBS-1 | OBSERVATION | — | US-9 LAN-IP | "first non-loopback IPv4" can pick a Docker/VPN/virtual NIC; the URL may be unreachable. | Prefer a default-route-bound IP heuristic; or print all and label none "primary". Minor UX. |
| OBS-2 | OBSERVATION | — | Reserved names (A3) | Reserving `onboard/start/stop/credentials` against agent IDs is fine, but `gateway`/`g` (kept alias) and the to-be-removed verbs are also cobra-shadowing surface today. | Note that an agent literally named `gateway`/`credentials`/`onboard` is unreachable via the positional run path; the creation-time reject must cover the full reserved set, not just four. |
| OBS-3 | OBSERVATION | — | Provider error strings | Removed-verb references live in `pkg/providers/*_provider.go` user-facing error strings (`run omnipus auth login`). These break the UX *after* removal even if they don't break the build. | Update provider error strings to point at the new credential flow (`omnipus credentials set` / onboard) as part of FR-013. |

---

## CR-1 — Approval handshake resolves the wrong registry (full evidence)

The spec's approval design (Reference Patterns L19; FR-005; US-4-AC-1/2; BDD "Ask-policy tool denied-and-continue"; Integration Boundaries "Data in: `exec_approval_response`") instructs the CLI to:

> on `tool_approval_required`, send `exec_approval_response {id, decision:"deny"}` → run continues to `done`, no 90 s wait.

**This does not work against the actual gateway.** There are TWO independent approval mechanisms:

- **Legacy hook path** — `pkg/gateway/ws_approval.go`. `wsApprovalHook.ApproveTool` (line 132) sends an **`exec_approval_request`** frame (line 188-202) and blocks on a channel in its own `wsApprovalRegistry`. That registry is resolved by `websocket.go:1747` (`h.approvalRegistry.resolve(id, d)`) when an **`exec_approval_response`** frame arrives. Note the frame the server sends is `exec_approval_request`, NOT `tool_approval_required` — so the spec's *listen* target and *send* target are mismatched even for this legacy path.

- **Live policy path (the one that actually gates ask-policy tools)** — wired at boot:
  - `pkg/gateway/gateway.go:1536`: `agentLoop.SetToolApprover(newPolicyApproverAdapter(approvalReg, wsHandler))`.
  - `pkg/gateway/policy_approver.go:48-83`: `RequestApproval` creates a `approvalRegistryV2` entry, calls `broadcastToolApprovalRequired(entry)` (a **one-way** `tool_approval_required` frame — `ws_tool_approval.go:47-102`, no response frame defined), then **blocks on `entry.resultCh`** (line 72-76).
  - The agent loop reaches this via `pkg/agent/loop.go:6553` (`approver.RequestApproval(...)`).
  - `entry.resultCh` is resolved **only** by `approvalRegistryV2.resolve(...)`, whose sole production caller is **`pkg/gateway/rest_tool_registry.go:415`** — `HandleToolApprovals`, the REST endpoint `POST /api/v1/tool-approvals/{approval_id}` with body `{"action":"approve"|"deny"|"cancel"}` (registered `rest.go:3971`).

`websocket.go:1747` resolves a **different** registry (`h.approvalRegistry`, the legacy `wsApprovalRegistry`) — it does **not** touch `approvalRegistryV2`. Therefore a CLI sending `exec_approval_response` over WS:
- does not reach the V2 `resultCh` the agent is blocked on;
- the agent stays blocked until the **V2 timeout** fires (the timeout passed to `newApprovalRegistryV2`, `approvals.go:148`), auto-denying;
- net effect: the run completes only *after* the timeout — the precise 90-s-class hang the spec's US-4 "Why this priority" exists to eliminate.

**Concrete fix (re-spec FR-005):** the deny-and-continue flow is:
1. CLI receives a `tool_approval_required` WS frame; read `approval_id` (the `ApprovalId` field, `ws_tool_approval.go:62`).
2. CLI issues `POST /api/v1/tool-approvals/{approval_id}` with `{"action":"deny"}` (or `"approve"` for `--yes`), bearer-authed with the `cli` token.
3. The V2 registry resolves, the agent injects a denied-tool result (`loop.go` deny branch — `continue`, no re-prompt, confirmed) and proceeds to `done`.

This makes the run BOTH WS (to observe) AND REST (to resolve). The spec's "WS only" Integration-Boundary claim and its "no new wire types" claim survive (the REST endpoint and `ToolApprovalActionRequest` already exist), but the *design* in FR-005 is wrong and must be rewritten. Update Reference-Patterns L19, US-4, the BDD scenario, and `TestApprovalDecision_*`/`TestApproval_OneShotDenyContinues` accordingly.

> Note for `--yes`: US-4-AC-3 ("server policy still wins") remains TRUE — the engine resolves via the same V2 entry; "approve" only releases the gate, it does not bypass `deny`-policy tools (those never reach the ask path). Keep that scenario; just change the transport.

---

## CR-2 — `user=cli` audit attribution is not deliverable

SC-004 and US-5-AC-3 require an audited run to "attribute `user=cli`". Grounding:

- `pkg/audit/audit.go` `Entry` struct has fields `Timestamp, Event, Decision, AgentID, SessionID, Tool, Command, Parameters, PolicyRule, Details` — **no `User`/`Username`**.
- At WS auth, `websocket.go:659` sets `wc.userID = user.Username`, but the agent-loop audit emissions (e.g. `loop.go` policy-deny audit, `~2823-2831`) populate `Details` with `turn_id`/`agent_id`/`reason` — the authenticated user's name is **not** in scope there. The HTTP path stashes `UserContextKey` (`rest.go:315-319`) but that context is not threaded into agent audit either.

So a test asserting `user=cli` in an audit entry **fails today**. The token-never-printed and "`cli` user exists in `Gateway.Users`" parts of SC-004 ARE verifiable; the attribution part is not. Pick (a) scope the plumbing (add `Entry.User`, thread `wc.userID` → turn → audit) as explicit work with its own FR/tests, or (b) drop the attribution claim from SC-004/US-5-AC-3 and track it. Shipping the claim without the plumbing is a silent SC failure.

---

## Structural Integrity Results (plan-spec checks)

| Check | Result |
|---|---|
| Every user story has ≥1 acceptance scenario | PASS |
| Every acceptance scenario has ≥1 BDD scenario | PARTIAL — US-7 (help) and US-10 (credentials) have thin/derived BDD; acceptable |
| Every BDD scenario has `Traces to:` | PASS |
| Every BDD scenario has a TDD test | PASS (mapped in Test Order) |
| Every FR in the traceability matrix | PASS (FR-001..016 present) |
| Every BDD scenario in the matrix | PARTIAL — "Bare omnipus pre-onboard guides to onboard" (US-6-AC-3 / SC: pre-onboard exit non-zero) has **no test** in the matrix (FR-008 maps only `TestRosterListing_ExcludesWorkers`, which is the happy path). **Gap.** |
| Datasets cover boundary/edge/error | PARTIAL — token-mode 0644 (dataset row 3) and `http://` non-TLS (URL row 5) have datasets but no named tests |
| Regression impact addressed | PASS (onboard, credentials, start, multi-user auth) |
| Success criteria measurable | PARTIAL — SC-004 attribution is unmeasurable today (CR-2); SC-003 "< 5 s" is good and measurable |

**Structural gaps to fix:** (1) no test for US-6-AC-3 pre-onboard error path; (2) no test for FR-007 `http://`-refuse as a distinct case; (3) datasets without tests (token 0644, non-TLS URL).

---

## Test Coverage Assessment

- **Negative-test holes**: FR-008 (pre-onboard guide), FR-007 (http refuse vs local-token-to-remote refuse — one test conflates two rules), empty-prompt (Edge Cases, untested), corrupt/0644 `cli.token` (dataset, untested). Each FR with only a happy-path test is flagged in the table.
- **Approval test is wrong by construction (CR-1)**: `TestApprovalDecision_DefaultDenyAndYesAllow` and `TestApproval_OneShotDenyContinues` assert a `decision` mapping for `exec_approval_response`. After the CR-1 fix they must assert the REST `action` mapping (`deny`/`approve`) against `tool_approval_required.approval_id`.
- **Integration-test feasibility (MAJ-5)**: confirmed `RunContextWithOptions(ctx, RunOptions)` exists (`gateway.go:432`) and existing `pkg/gateway` tests boot in-process with a `restMockProvider{}` (no real LLM) and `httptest`. CLI integration tests should mirror this (mock provider, `-p 1`). The "scripted WS twin" for unit-level client tests is sound and has repo precedent (`websocket_test.go` `newTestWSHandler` + `httptest.NewServer`). **But** linking `pkg/gateway` pulls in goolm crypto — a single scoped test is fine per CLAUDE.md; the spec must say `-p 1` and not run the whole suite.
- **Concurrency**: only P1 (`TestAutoStart_ConcurrentLock`) — fine for P0 scope; but see MIN-5 (lock redundancy).

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E |
|---|---|---|---|---|---|---|
| `cli.token` at rest (0600 file) | token IS identity — file read = full admin (MAJ-4) | mode-check only on mint, not on read (MIN-2) | — | token never printed (good, NFR) — verify no leak via `--debug`/error wraps | — | file-readable = admin EoP; no role scoping (MAJ-4) |
| WS auth (`cli` principal) | bcrypt token match, constant-time (`config.go` VerifyToken) — sound | — | **no audit attribution of the acting user (CR-2)** | — | gateway has auth rate-limit on REST; WS auth has a 10 s deadline | second admin principal; role unenforced at tool layer (MAJ-4) |
| `--url` remote | token sent to a remote host — spec forbids http + local-token-to-remote but leaves remote token source undefined (MAJ-6) | TLS enforced for `https`; http refused | — | token to wrong host = disclosure if scoping wrong | — | — |
| Approval REST `POST /tool-approvals/{id}` | `withAuth` bearer-gated (`rest.go:3971`) | `approval_id` is a UUID path param | resolved approvals 410 (replay-safe, FR-018) | — | saturation-capped registry (`approvals.go`) | admin-only effectively (cli is admin) |

---

## Unasked Questions

1. **CR-1 consequence**: given the real gate is REST, is the run still "WS-only" as the Integration Boundary claims? (Answer: no — it's WS-to-observe + REST-to-resolve. The spec must own this.)
2. **CR-2**: is adding `audit.Entry.User` in scope for this CLI epic, or deferred? If deferred, SC-004 must change.
3. **MAJ-4**: what is the `cli`-token *revocation* and *rotation* story? Deleting the user? Re-running `start`? The spec mints but never revokes.
4. Does `start` (which mints `cli.token` per FR-011) overwrite an existing token, or only create-if-absent? US-5-AC-2 says "idempotently mint the *missing*" — confirm it never silently rotates a working token out from under a remote.
5. What happens when the gateway answers the port but with a **different/old** `cli` token (auth fails)? P0 currently lumps this into "gateway down" guidance, which misdirects the user (MIN-3).
6. Are `audit`, `doctor`, `version` (kept commands) intentionally undocumented in the spec's keep-list? They affect `--help` (FR-009) content and the reserved-name set (OBS-2).
7. Should provider error strings (`run omnipus auth login`) be migrated as part of FR-013, or left to dangle post-removal (OBS-3)?

---

## Removals completeness — concrete file deltas the spec must add (MAJ-1/MAJ-2)

**Kept commands the spec fails to declare** (exist in `cmd/omnipus/main.go`): `audit`, `doctor`, `version` (in addition to the declared `onboard`/`start`/`gateway`+`g` alias/`credentials`).

**Runtime callers — all SAFE** (use kept `gateway` alias or `credentials`): `docker/Dockerfile{,.full,.heavy,.goreleaser}`, `docker/entrypoint.sh`, `.github/workflows/cross-platform.yml`, `pr.yml`, `deploy/ci-worker/runci.sh`, `cmd/omnipus-launcher-tui/ui/gateway.go`. No runtime break — but `scripts/install.sh:180` and the launcher should migrate `omnipus gateway` → `omnipus start` for consistency.

**Doc files with removed-verb references the spec omits**: `docs/ANTIGRAVITY_USAGE.md`, `docs/providers.md`, `docs/configuration.md`, `docs/channels.md`, `docs/channels/weixin.md`, `docs/channels/wecom.md`, `docs/skills.md`, `ROADMAP.md`, `docs/internal/architecture/ADR-004-credential-boot-contract.md` (plus the already-listed `docs/using-omnipus-cli.md`).

**Source error strings referencing removed verbs**: `pkg/providers/claude_provider.go`, `factory_provider.go`, `codex_provider.go`, `antigravity_provider.go`, and `cmd/omnipus/internal/auth/{helpers,weixin,wecom}.go` (the auth ones vanish with the verb, but the provider strings persist).

**Grep-guard regex fix**: `omnipus (agent|auth|status|cron|migrate|model|skills)\b` — and confirm it does NOT match the kept `omnipus gateway` (it doesn't; `gateway` isn't in the alternation).

---

## Verdict

**BLOCK.**

Two CRITICAL findings (CR-1 approval-registry mismatch → guaranteed 90 s hang; CR-2 unachievable `user=cli` audit attribution) invalidate the spec's headline guarantees and are grounded in the live gateway boot path, not prose. Seven MAJOR findings (incomplete removals, broken grep-guard, `/ready` race, unscoped admin blast radius, under-specified test/LLM boundary, `--url` ambiguity, approval id/transport inconsistency) would each force an implementing agent to guess or under-test.

Address the findings, then re-run:

```
/grill-spec docs/internal/specs/cli-minimization-spec.md
```
