# Spec Review — Spec-4 External-Agent Runners & the Executor Tier

- **Spec reviewed:** `docs/internal/specs/v01-spec4-external-agent-runners-spec.md`
- **Mode:** `plan-spec` (full structural + 8-lens adversarial review)
- **Reviewer stance:** adversarial, read-only, grounded against the live tree at `pkg/providers`, `pkg/agent`, `pkg/sandbox`, `pkg/config`, `pkg/gateway`, `contracts/`.
- **Date:** 2026-06-13

---

## Executive Summary

This spec ships a security-critical capability (spawn arbitrary local agent CLIs with file/shell/network reach) and rests its safety story on four reuse claims that **do not hold against the current code**. Grounding the spec found:

- The "kernel-confined **per-child** Landlock/seccomp profile" (FR-5.3 / US-5 / SC-3) **is not achievable with the existing `hardened_exec` API**, which by its own documented contract gives children the *gateway's* single inherited Landlock domain "**unchanged… no narrowing**" — there is no per-child filesystem allow-list or per-child seccomp filter to hand a runner.
- The "built on the existing `claude_cli`/`codex_cli` providers" reuse claim (FR-5.2) is overstated: those providers are **one-shot, buffered, output-only batch callers** that run `--dangerously-skip-permissions` / `--dangerously-bypass-approvals-and-sandbox`. They contain **none** of the streaming, bidirectional, permission-routing, or resume machinery the runner requires — the ADR itself flags this ("Output-only one-shot — reshape needed").
- The `executor` field is asserted to be a **contract change** (`verify-contracts`), but the named struct `SubagentsConfig` (`config.go:584`) is **not in any contract** — it never crosses the gateway/SPA boundary today. US-1's UI ("when I set executor") therefore has **no wire path**, and the spec does not specify adding `subagents`/`executor` to `Agent.yaml`/`AgentCreateRequest`. The contract work is mis-located.
- The "egress allow-list" containment (US-5) is HTTP(S)-CONNECT-proxy only; **raw TCP egress is unblocked** per the existing sandbox contract. The spec presents it as a containment guarantee it does not fully provide.

**Findings:** 4 CRITICAL, 7 MAJOR, 5 MINOR, 3 OBSERVATION.

**Verdict: BLOCK.** The central security claim (per-child kernel confinement) is infeasible as written against the existing API, and the contract surface for the headline feature is mis-grounded. These are not wording fixes — they change what must be built (a new sandbox primitive and/or an explicit ADR amendment, plus a real contract addition).

---

## Findings Table

| ID | Sev | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|---|
| C-1 | CRITICAL | Infeasibility / Incorrectness | FR-5.3, US-5, SC-3, TDD #4, H3 | **Per-child Landlock/seccomp profile does not exist.** `hardened_exec.go` header + `hardened_exec_linux.go` state children inherit the gateway's Landlock domain *unchanged, no narrowing*; `Run`/`StartLocked` only re-apply the **gateway's** domain to the spawning thread via `restrictCurrentThreadIfNeeded()`. There is no API to compose a per-run "worktree-only allow-list" or a per-run syscall filter. Landlock is also monotonic and self-applied to the calling process — the gateway cannot give a child a *different* (wider-in-some-dimensions) profile, and a child confined to *only* its worktree would also lose access to the CLI binary, its config/credentials, and temp — none of which the spec scopes. | Either (a) raise an ADR amendment defining a **new** per-child sandbox primitive (e.g. a re-exec'd confiner that calls `landlock_restrict_self` with a run-scoped ruleset before `execve` of the CLI) and spec its allow-list contents (worktree + CLI binary + creds + tmp + proxy socket) and its non-Linux/old-kernel degradation; or (b) drop the "per-child profile" claim, state the child inherits the gateway domain, and justify why that is sufficient. Do not ship US-5/SC-3 as written. |
| C-2 | CRITICAL | Incorrectness / Overcomplexity-inverse | FR-5.2, §2 Symbols, Assumptions | **"Built on `claude_cli_provider`/`codex_cli_provider`" is not real reuse.** Both providers (`claude_cli_provider.go`, `codex_cli_provider.go`) call `cmd.Run()` with **buffered** stdout, parse **after exit**, are **one-shot**, have **no stdin control channel**, **no permission handling**, **no streaming**, **no resume**, and use `--output-format json` (not `stream-json`) + `--dangerously-skip-permissions` / `--dangerously-bypass-approvals-and-sandbox`. The runner shares essentially nothing but the binary name. The ADR (line 149) admits "Output-only one-shot — reshape needed." | Reword §2/FR-5.2/Assumptions to state the drivers are **net-new** and the existing providers supply only the binary-resolution/auth precedent. Re-estimate effort: the Claude/Codex drivers are NOT cheap deltas. Remove "built on" framing that implies the streaming/bidi/resume code already exists. |
| C-3 | CRITICAL | Incompleteness / Inconsistency | FR-4.1, US-1, §2 Impact ("HIGH contract") | **The `executor` contract surface is mis-located and undefined.** `SubagentsConfig` (`config.go:584`) is a pure `config.json` struct — it is **absent from `contracts/`, `pkg/api/generated/`, `src/lib/api/generated/`, and `Agent.yaml`**, and is never serialized by `HandleAgents`. So adding `executor` to it is **not** a `verify-contracts`-visible change, yet the Impact table calls it "HIGH (contract)" and US-1 implies a UI control. The spec never says to add `subagents`/`executor` to `Agent.yaml` + `AgentCreateRequest`, which is what US-1's UI actually requires. | Decide and specify: either (a) `executor` is config-only (no UI, no contract) — then fix the Impact table, drop the UI implication in US-1, and note `verify-contracts` is unaffected; or (b) it must reach the SPA — then enumerate the new schema files (`Subagents.yaml` / `Executor.yaml`), the `Agent.yaml`/`AgentCreateRequest.yaml`/`AgentDetail` additions, the generated-type regen, and the 5-step contract process. Pick one; the current spec straddles both. |
| C-4 | CRITICAL | Insecurity (DoS / EoP) | US-5, FR-5.3, H3 | **Egress containment is overstated.** The only egress control is `EgressProxy` (`egress_proxy.go`), an HTTP/HTTPS-CONNECT host allow-list injected via `HTTP_PROXY`/`HTTPS_PROXY`. The sandbox contract explicitly says "**raw TCP egress is unblocked**." A spawned agent CLI that opens a raw socket (or ignores proxy env) exfiltrates freely. The spec sells "egress allow-list" as containment for a *hostile* agent (US-5.2, H3). | State the egress control is HTTP(S)-proxy-only and depends on the child honoring proxy env; document the raw-TCP gap as a known limitation for v0.1.0 (or scope a kernel/network-namespace egress block as ADR work). Add a non-behavior: "does NOT block raw TCP egress in v0.1.0." Adjust H3/US-5.2 claims accordingly. |
| M-1 | MAJOR | Incompleteness | FR-5.1, US-2, hook_process | **Role inversion vs the reusable stdio pattern is unaddressed.** `hook_process.go` is a genuine reusable JSON-RPC/stdio harness, but it models Omnipus as the **client** (Omnipus sends requests, child replies). A runner needs the **inverse**: the child emits **unsolicited** event streams (output/diff/tool-call) and Omnipus must **answer** child-initiated permission-requests. The correlation/lifecycle code is reusable; the message-flow direction is not. The spec cites it as "the bidirectional stdio precedent" without noting the inversion. | Specify the runner's protocol direction explicitly: who initiates, how unsolicited events are framed, how a permission-request is correlated to its decision, and which `hook_process` pieces are reused vs reimplemented. |
| M-2 | MAJOR | Incompleteness / Insecurity | US-2, FR-5.1, ws_approval | **Consent-request shape mismatch is unspecified.** The existing consent target (`ToolApprovalRequest`→`ApprovalDecision`, routed over WS `exec_approval_response`, `pkg/gateway/ws_approval.go`) is **tool-call-shaped**. An external agent's permission-requests (write file X, run command Y, fetch URL Z) have a different schema. The spec says "route to the consent layer" without defining the mapping, the wire frame, or how the SPA renders a *foreign* agent's request. | Define the permission-request payload (kind, target, args), the mapping onto `ToolApprovalRequest` or a new frame, the AsyncAPI schema, and the timeout/default-deny behavior on no response. This is a contract change (Constraint #8). |
| M-3 | MAJOR | Infeasibility / Incorrectness | US-3, FR-5.2 | **Resume / streaming / permission-prompt flags are unverified and contradict current usage.** The spec relies on `claude -p --output-format stream-json` + `--resume` + an implied `--permission-prompt-tool`; the only in-tree usage is `--output-format json --dangerously-skip-permissions` (one-shot, *permissions bypassed*). No grounding that the installed CLI versions support these flags or their exact event schema. Codex `--resume` is likewise unverified (current code uses `codex exec --json -` one-shot). | Pin the exact CLI versions and flag set per CLI in the spec; record the stream event schema as a fixture (TDD #8 already wants a recorded fixture — make it normative). Add an edge case for "CLI version lacks streaming/resume → degrade to one-shot, no resume." Note the security implication: you are moving *away* from `--dangerously-skip-permissions` toward `--permission-prompt-tool`, which is a behavior change, not reuse. |
| M-4 | MAJOR | Incompleteness | US-5 §3 / Ambiguity #3 | **Non-repo "isolated temp dir" path is hand-waved.** Ambiguity #3 "RESOLVES" to "isolated temp dir under sandbox" for non-git runs, but nothing specifies where (which root), permissions, cleanup ownership, or how it interacts with the (nonexistent, per C-1) per-child allow-list. Worktree cleanup-on-crash (FR-5.3, H4, edge case) has no owner, no orphan-reaper, no test beyond a holdout. | Specify the run-dir root (under `$OMNIPUS_HOME`?), creation/teardown lifecycle owner, crash-reaper trigger (boot scan? PID file?), and add a concrete unit/integration test for orphan cleanup, not just holdout H4. |
| M-5 | MAJOR | Incompleteness / Inoperability | FR-5.4, US-7 | **"Turn-cap" is named but never defined; no observability.** FR-5.4 requires a "turn-cap" but no scenario, test (#7 covers timeout only), dataset, or success criterion exercises it. SC-7 covers timeout exclusively. No structured-log/audit/metric is specified for runner spawn, permission-request, termination, or worktree lifecycle — on-call has no signal when a runner hangs or is killed at 3 AM. | Add a turn-cap dataset + test + SC; define the audit/log events (spawn, permission routed, decision, timeout-kill, turn-cap-kill, cleanup) reusing the existing audit JSONL. |
| M-6 | MAJOR | Insecurity (Spoofing/EoP) | Integration boundaries, FR-4.2 | **Auth/credential routing for the CLIs is asserted, not specified.** "auth via each CLI's own credentials (managed in the credential store)" — but the CLIs read their own on-disk auth (e.g. `~/.claude`, codex creds); how does the credential store inject these into a *kernel-confined, env-allowlisted* child whose Landlock domain (per C-1) may not even include the creds path? `allowedChildEnvKeys` is an explicit allow-list — CLI auth env vars are not in it. | Specify exactly which credential files/env each CLI needs, how they reach the child under the env allow-list and (eventual) per-child FS profile, and how the connection test (FR-4.2) distinguishes "no binary" from "binary present, not authed" without leaking the credential. |
| M-7 | MAJOR | Inconsistency | §6 cross-spec, Assumptions, Ambiguity #1 | **`executor` ownership is split between Spec-3 and Spec-4 with no resolution.** The spec repeatedly defers the `subagents` struct to Spec-3 ("coordinated at Phase-3.5") while also defining `executor` on it here (FR-4.1). If Spec-3 owns the struct, Spec-4 cannot land FR-4.1's schema change independently; the two specs can disagree on field shape. | Make the ownership a hard pre-condition: state that FR-4.1 lands the field in Spec-3's struct and is gated on Spec-3 merging first, or move `executor` definition wholly into one spec. The Phase-3.5 check (task #8) must verify the single shared shape. |
| m-1 | MINOR | Ambiguity | FR-5.5, US-1 | "`remote-a2a` … shares the agent-reference shape (Spec-2/3)" — the shape is never shown; "dispatch reports 'not available in v0.1.0'" doesn't say *where* (error return? audit? UI toast?). | Specify the rejection surface and the exact error string/code. |
| m-2 | MINOR | Ambiguity | Edge Cases | "Codex headless quirks (no stable stream) → best-effort + documented" — "best-effort" is subjective and untestable. | Define the minimum guaranteed behavior (e.g. "Codex runs in one-shot mode; no mid-run permission routing; documented as degraded") and test that, not "best-effort." |
| m-3 | MINOR | Incompleteness | TDD #2, #4 | Tests rely on "a fake CLI driver" / Linux gating but no fixture format or fake-CLI contract is defined; #4 ("own-process, sandboxed, worktree") cannot pass while C-1 stands. | Define the fake-CLI stdio contract; mark #4 blocked on the C-1 sandbox decision. |
| m-4 | MINOR | Inconsistency | SC-2, "CI authority" | SC-2 "build + typecheck exit 0" but there is no SPA work specified (per C-3 the UI is undefined), so what does typecheck cover? | Reconcile: if there's a UI, spec it; if not, drop the typecheck SC or scope it to the (none) frontend delta. |
| m-5 | MINOR | Insecurity (Repudiation) | FR-5.1 deny-by-default | Deny-by-default on no-handler is good, but there's no audit entry specified for a denied-by-default permission — a silent deny is hard to debug and gives no repudiation trail. | Require an audit/log entry on every default-deny. |
| O-1 | OBSERVATION | Overcomplexity | scope | Three drivers (claude/codex/opencode) for v0.1.0 where Codex+opencode are explicitly "best-effort" multiplies net-new surface. Consider shipping Claude Code first-class only and gating the other two behind a follow-up. | Consider narrowing v0.1.0 to one first-class driver. |
| O-2 | OBSERVATION | Inoperability | rollout | No feature flag / config gate to disable external runners wholesale is specified (deny-by-default for the *capability*, matching CLAUDE.md Constraint #6). | Add a top-level `subagents.external_runners_enabled` gate, default off. |
| O-3 | OBSERVATION | Incompleteness | resource | No disk-quota / worktree-size bound; a runaway agent can fill the disk inside its worktree before the timeout fires. | Consider an RLIMIT_FSIZE or quota note (the `hardened_exec` Limits already does RLIMIT_AS/CPU/NPROC — extend the pattern). |

---

## Structural Integrity Results (plan-spec mode)

| Check | Result |
|---|---|
| Every user story has ≥1 acceptance scenario | PASS |
| Every acceptance scenario has ≥1 BDD scenario | PASS |
| Every BDD scenario has `Traces to:` | PASS |
| Every BDD scenario has a TDD test | PARTIAL — "denies by default" → #3 ok; turn-cap (FR-5.4) has **no** scenario/test (M-5) |
| Every FR in traceability matrix | PASS (FR-4.1, 4.2, 5.1–5.5 all present) |
| Every BDD scenario in traceability | PARTIAL — "no-handler denies" maps under US-2 but the matrix row for FR-5.1 lists it; acceptable |
| Datasets cover boundary/edge/error | PARTIAL — turn-cap, raw-TCP-egress, non-repo-tempdir, CLI-version-mismatch missing |
| Regression impact addressed | PASS (native path unchanged; provider sibling-use) — but the provider-reuse premise is wrong (C-2) |
| Success criteria measurable | PARTIAL — SC-3 not achievable as written (C-1); "best-effort" (m-2) unmeasurable |

---

## Test Coverage Assessment

- **Missing negative/boundary tests:** turn-cap exceeded (M-5); CLI version lacks streaming/resume (M-3); raw-TCP egress attempt (C-4); credential-present-but-unauth vs missing-binary distinction (M-6); permission-request schema that doesn't map to `ToolApprovalRequest` (M-2).
- **Infeasible-as-written:** TDD #4 (`TestRunner_OwnProcess_Sandboxed_Worktree`) cannot assert "cannot read outside its allow-list" because no per-child allow-list exists (C-1). It would currently only prove the child inherits the *gateway* domain.
- **Fixture rigor:** TDD #8 (recorded stream-json fixture) should be normative and version-pinned (M-3), and a parallel codex fixture is absent.
- **Concurrency:** runner fan-out is deferred to Spec-3, but worktree-creation races / shared run-dir-root contention under parallel dispatch are not tested anywhere.

---

## STRIDE Threat Summary

| Component | Threats identified |
|---|---|
| Spawned external CLI child | **EoP** — inherits gateway Landlock domain, no per-child narrowing (C-1); **Info-disclosure / DoS** — raw TCP egress unblocked (C-4); **DoS** — no disk quota on worktree (O-3) |
| Permission-request routing | **EoP** — schema mismatch may cause mis-rendered/auto-approved requests (M-2); **Repudiation** — default-deny not audited (m-5) |
| Credential injection for CLIs | **Spoofing/Info-disclosure** — credential path vs env-allowlist vs FS-profile interaction unspecified (M-6) |
| `executor` config field | **Tampering** — if it reaches the SPA without a defined contract/validation (C-3), an unvalidated `executor` could be persisted |
| Connection test | **Info-disclosure** — must not leak credential contents in failure reasons (M-6) |

---

## Unasked Questions

1. What *exactly* goes in a per-child Landlock ruleset (worktree + CLI binary + creds + tmp + proxy socket), and how does the child reach its own binary/config if confined to "only its worktree"? (C-1)
2. Is `executor` user-facing? If yes, where is the `Agent.yaml`/`AgentCreateRequest` contract addition and the SPA control? If no, why is US-1 phrased as a UI action and the Impact table "HIGH (contract)"? (C-3)
3. Which installed CLI versions are targeted, and do they support `stream-json` / `--resume` / `--permission-prompt-tool`? What is the recorded event schema? (M-3)
4. How are the CLIs' own on-disk credentials provisioned into a kernel-confined, env-allowlisted child? (M-6)
5. Who owns worktree/temp-dir lifecycle and crash-reaping, and where is the run-dir root? (M-4)
6. What is the turn-cap, and how is it observed/tested? (M-5)
7. What happens to raw (non-HTTP) network egress from a child — is it blocked or accepted as a v0.1.0 gap? (C-4)
8. Does Spec-3 or Spec-4 land the `subagents.executor` field, and in what merge order? (M-7)

---

## Verdict

**BLOCK.** — 4 CRITICAL findings. The headline security guarantee (per-child kernel confinement + worktree allow-list, FR-5.3/US-5/SC-3) is **infeasible against the existing `hardened_exec` API** and requires either a new sandbox primitive (ADR amendment) or a scaled-back claim; the `executor` contract surface is mis-grounded (the named struct is not on the wire); the provider-reuse premise is materially overstated; and egress containment is oversold. These change *what gets built*, not just the prose.

Review written to: `docs/internal/specs/v01-spec4-external-agent-runners-spec-review.md`

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/v01-spec4-external-agent-runners-spec.md docs/internal/specs/v01-spec4-external-agent-runners-spec-review.md
```

Because C-1 and C-4 turn on a sandbox-capability decision (new per-child Landlock/seccomp primitive vs. inherited-domain acceptance vs. raw-TCP-egress gap), that decision belongs in an **ADR-019 amendment** before the spec is revised — the spec cannot resolve it unilaterally.
