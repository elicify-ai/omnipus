# ADR-024 CLI-Minimization — Adversarial Review (grill-spec)

**Mode:** generic-markdown (ADR ratification review)
**Reviewed:** `docs/internal/architecture/ADR-024-cli-minimization.md`
**Reviewer stance:** Read-only adversarial. Direction is operator-decided; this stress-tests
mechanism, evidence, and hidden dependencies before `/plan-spec`.

---

## Executive Summary

The ADR's strategic direction (thin one-shot client over the engine, kill the divergent
in-process REPL, remove dead-data subcommands) is well-grounded and survives scrutiny. The
two-writers-one-datadir inference that kills Option B is **correct and in fact understated** —
the real session/message files have *no* cross-process lock at all.

But the ADR ships with **two Blockers** that change the decision's shape, not just its
implementation:

1. **G3 is a false choice.** The `/chat` REST endpoint cannot satisfy FR-2 (named agent) or
   FR-3 (`--model`) — its request body carries only `message`, and it is explicitly legacy/no-stats.
   Only the WS frame supports the chosen requirements. The ADR's D2 evidence cites REST as a
   viable transport; it is not. The execute path is WS-only, which materially changes "Medium
   complexity."
2. **No-new-wire-types is true for transport but false for the *one-shot lifecycle*.** A
   one-shot run hits `tool_approval_required`/`exec_approval_request` frames and the
   `nopPolicyApprover` deny-all / 90 s-timeout path with no terminal answer channel. The ADR
   declares approval-UX out of scope without specifying what the run *does* when it occurs —
   silent per-tool denial or a 90 s hang. That is an undefined failure mode on the happy path
   of any agent with `ask`-policy tools (which is the default for Jim's shell).

Plus a **Major** on the scale of the back-compat break (D6/R2): the removals and the `gateway`
alias deletion break Docker entrypoints, two CI workflows, the CI worker, the desktop launcher,
`scripts/install.sh`, and six documented command families — far past "a release note."

**Findings:** 2 Critical, 5 Major, 4 Minor, 2 Observations.

**Verdict: PASS-WITH-CONDITIONS (REVISE).** The direction stands; ratify it. But three items
must be folded into the ADR (or explicitly carried as gated `/plan-spec` decisions) before it
is "spec-ready": fix the REST-vs-WS evidence (G3), define the approval-frame behavior on a
one-shot run, and right-size the back-compat plan. D7 (local-auth) is correctly flagged Low and
correctly gated to security-lead — it stays a condition, not a blocker, *provided* the ADR
commits to the token-file direction over loopback-trust (see CR-1).

---

## Findings

| ID | Severity | Lens | Section | Finding | Fix / Decision needed |
|----|----------|------|---------|---------|----------------------|
| CR-1 | Critical | Insecurity / Infeasibility | D7, G1, R1 | Local-auth handshake is undesigned and the ADR leaves "loopback-trust vs token-file" open. Loopback-trust = re-creating `dev_mode_bypass`. WS/HTTP auth **fails closed** when no users + no env token + `bypass=false` (`websocket.go:686-695`, `auth.go:117-119`). So the auto-started gateway *will reject its own CLI* unless something grants a token. | **Decide now: CLI-owned 0600 token file, not loopback-trust.** `onboard` must mint a CLI token (a real RBAC user token or a dedicated principal), persist its plaintext to `$OMNIPUS_HOME/cli.token` (0600), store the bcrypt hash in `config.Gateway.Users` like any bearer. CLI reads the file → sends it in `auth`/`Authorization`. This reuses the existing `VerifyToken` path, needs no `RequireNotBypass` exception, and never trusts "localhost = admin." Loopback-trust must be explicitly rejected in the ADR, not left as a coin-flip for `/plan-spec`. |
| CR-2 | Critical | Incompleteness / Incorrectness | D2, G3, "Out of scope" | One-shot lifecycle vs approval frames is undefined. Default tool policy is `ask`; with no approver the `nopPolicyApprover` denies every request (`external_dispatch.go:575`) and the WS approval path waits `wsApprovalTimeout` (90 s, `pkg/agent/ws_approval.go` / `hooks.go:20`) before resolving. A one-shot CLI sends no approval response, so any `ask`-policy tool either silently fails or stalls 90 s then fails. Jim's seed forces shell-on with sandbox = `ask`-adjacent flows. "Tool-approval-over-the-wire UX … out of scope" describes the *UI*, not the *run behavior*. | **Specify the one-shot policy contract.** Either: (a) the CLI sends a default-deny answer immediately on every `*_approval_request` frame (fail-fast, print which tool was blocked to stderr, continue), or (b) a documented `--yes`/`--auto-approve` flag maps to auto-allow for that run, gated by the agent's own policy. Pick one in the ADR; it changes FR-11's "scriptable" promise and the streaming-client design. |
| MA-1 | Major | Incorrectness | D2, G3 | `/chat` REST cannot back the chosen requirements. `SseChatRequest` has only `message` (`contracts/components/schemas/SseChatRequest.yaml:7-14`) — no `agent_id` (FR-2), no model override (FR-3). The handler is explicitly **legacy**: `partitions: nil` always, no session recording, not the bus stream delegate (`sse.go:73,78,81`); its `done` event is `{}` with **no stats** (`sse.go:46`) vs the WS `done.stats` (tokens/cost). So G3 ("WS-stream vs `/chat` REST for v1") is not a real fork — REST is unusable for FR-2/FR-3/usage. | **Correct D2's evidence and G3.** State the execute path is **WS-only** (`asyncapi.yaml` MessageFrame carries `agent_id` + `metadata.model_name`; SSE does not). Either retire G3 (decided: WS) or scope a new `agent_id`/`model` extension to `SseChatRequest` as an explicit contract change — which would *break* the "no new wire types" claim. Recommend: WS, retire G3, drop the REST citation from D2. |
| MA-2 | Major | Inoperability / Incompleteness | D6, R2 | Back-compat blast radius is far larger than "a release note." `gateway`-alias deletion + subcommand removals break: Docker `CMD ["gateway",…]` (`docker/Dockerfile:83`, `Dockerfile.heavy:94`, `Dockerfile.full:44`) and `docker/entrypoint.sh:11` (`exec omnipus gateway`); CI (`.github/workflows/cross-platform.yml:83`, `pr.yml:616`); the CI worker (`deploy/ci-worker/runci.sh:189`); the desktop launcher (`cmd/omnipus-launcher-tui/ui/gateway.go:88,90`); `scripts/install.sh:180`; and six documented command families in `docs/using-omnipus-cli.md`. Per Constraint #7 ("fix everything"), shipping a removal that red-lines our own CI is not acceptable as a "note." | **Two changes.** (1) The implementation MUST include co-ordinated updates to all internal callers (Docker, CI, worker, launcher, install.sh, docs) in the same change — make this an explicit ADR deliverable / taskify item, not a release note. (2) **Keep the `gateway` alias** (D6 cost ≈ one line; it back-stops external scripts and our own containers) OR provide a one-release deprecation shim that prints a redirect to `start` and exits 0 — see MA-3. |
| MA-3 | Major | Incorrectness | D6, R2 | The ADR treats hard-removal of *executing* commands (`auth`, `status`, `cron`, `migrate`, `agent`) the same as the cosmetic `gateway`-alias rename. A hard "unknown command" break of `agent`/`status`/`auth` mid-script gives no migration path; these are documented user surfaces (`docs/using-omnipus-cli.md` §4–11). | **Decide: deprecation-shim release vs hard cut.** Recommend one v0.x release where removed verbs become thin stubs that print "removed — use `omnipus <agent> \"…\"` / Settings → …" and exit non-zero. Costs ~6 tiny cobra stubs; converts a silent break into a guided migration. If hard-cut is chosen, the ADR must say so and own the docs rewrite as a deliverable. |
| MA-4 | Major | Incompleteness | D7, G2 | Daemon lifecycle is entirely greenfield — **no PID/detach logic exists anywhere in `cmd/`** (confirmed: empty grep for daemon/fork/setsid/pid). `omnipus stop` + PID file + portable detach is hand-waved as "spike." Orphaned daemons, stale PID files (PID reuse), and the Windows path (no `setsid`/`nohup`) are unaddressed. The desktop launcher already writes a PID via `nohup … & echo $! > pidPath` (`launcher-tui/ui/gateway.go:90`) — a competing lifecycle the ADR doesn't reconcile. | **Resolve before spec-ready, or descope auto-start to Option A for v1.** Minimum: define PID-file location + staleness check (PID alive AND is an omnipus process), `omnipus stop` semantics, and the Windows detach story (the ADR can't punt this given Constraint #4 cross-platform). Reconcile with the launcher's existing PID handling so two mechanisms don't fight. **Strongly consider:** ephemeral foreground gateway that dies after the task as the *default*, with "leave running" opt-in — see Unasked Q4. |
| MA-5 | Major | Incompleteness | D2, FR-5, G4 | Auto-start happy path is underspecified beyond "hard-fail on port contention." Missing: (1) **readiness/health-poll contract** — how long does the CLI wait for the freshly-spawned gateway's `/health` before first call, and what's the timeout/UX (R5 "first-call latency" is `[UNKNOWN]`, NFR-5 admits exact boot time unknown)? (2) **race on concurrent auto-start** — two `omnipus <agent>` invocations racing to spawn one gateway both see "port free," both spawn, one loses the bind. (3) **credential unlock** — the auto-started gateway needs the master key; if `master.key` is absent and unlock is interactive-TTY-only, headless auto-start deadlocks. | Specify: health-poll budget + message; a spawn lock (e.g. flock on a `gateway.lock`) so only one auto-start wins; and the unlock precondition (auto-start requires a non-interactive unlock mode — `master.key`/`OMNIPUS_KEY_FILE`/`OMNIPUS_MASTER_KEY` — else fail with guidance, mirroring G5). |
| MI-1 | Minor | Incompleteness | D4, "Out of scope" | D4 ("chat-target agents only") is *correct* for the chat API, but the operator's stated intent in the problem framing is **task-delegation/execution semantics**. Excluding the worker/specialists means the operator immediately loses "run an isolated, memory-free, least-privilege task" from the CLI — exactly what a one-shot task-runner suggests. The capability gap is acknowledged only as "Out of scope: per-agent worker task endpoint," with no signal of *when* it returns. | Add a one-line forward note: the worker/specialist execute path is a deliberate v-next item (needs a non-session delegation endpoint), and confirm with the operator that chat-target-only is acceptable for v1. Low risk, but it's the most likely "I immediately wanted X" gap. |
| MI-2 | Minor | Ambiguity | FR-6, D3 | "list agents + usage (from local config.json)" — `usage` is undefined for an offline read. Usage/token data is per-session aggregate (`pkg/session`), not in `config.json`. Offline `omnipus` cannot show real usage without the gateway/session store. | Clarify FR-6: either drop "usage" from the offline bare-`omnipus` listing, or define it as "last-known usage from session store if present, else omit." Don't promise usage from `config.json` (it isn't there). |
| MI-3 | Minor | Ambiguity | D3 | "an agent may not shadow `onboard`/`start`/`credentials` (cobra resolves subcommands first)" — true, but the ADR doesn't state what happens if a user *names an agent* `onboard`. cobra will silently run the subcommand, not the agent → confusing "why won't my agent run." | Add a validation: reject/ warn on agent creation if the name collides with a reserved verb, or document that reserved names shadow agents and the CLI prints a hint. |
| MI-4 | Minor | Inconsistency | §1 [INFERENCE] vs code | The two-writers inference says `unix.Flock` + the shard pool "guard intra-process writes … not two OS processes." Code shows it's *worse*: `context.jsonl` (agent LLM history) is appended via `JSONLStore.addMsg` with **only** the striped mutex + `O_APPEND` and **no flock at all** (`pkg/memory/jsonl.go:214-238`); `transcript.jsonl` via `fileutil.AppendJSONL` also has **no flock** (`unified.go:418-430`, `fileutil/file.go:151+`). `WithFlock` covers only `meta.json` (`unified.go:412,826`). | Strengthen the inference to a near-fact: the *message* files have no cross-process serialization; only metadata is flocked. This makes the Option-B rejection *stronger*, not weaker. (Correct in the ADR's favor.) |
| OB-1 | Observation | Overcomplexity | D2 vs Option A | The hybrid (auto-start + daemon lifecycle + local-auth handshake + spawn race + health poll) is the single largest complexity sink in the ADR — and most of it (MA-4, MA-5, CR-1) exists *only because the gateway is left running*. Option A (require `omnipus start` first) deletes the daemon lifecycle, the orphan problem, the spawn race, and shrinks the auth problem. The "just works" delta is one error message ("no gateway running — run `omnipus start`"). | Not a blocker — operator chose C. But worth a sentence acknowledging that ~60% of D7/G2/G4/R3/R5 risk is bought by the auto-start convenience, and that an ephemeral-per-task gateway (Unasked Q4) recovers most of A's simplicity while keeping "just works." |
| OB-2 | Observation | Inoperability | NFR-2 | NFR-2 ("bearer MUST NOT leave the host unless explicit `--url`+TLS") is sound but untested-as-stated. With CR-1's token file, ensure the CLI never sends the local token to a `--url` remote, and never sends a remote token to localhost. | Add a test in `/plan-spec`: token scoping by endpoint (local token ≠ remote token); reject `--url http://` (non-TLS) for any token send. |

---

## Structural Integrity (generic-markdown narrative)

- **Scope clarity:** Strong. Command surface, removals, and explicit "Out of scope" are clear.
  Gap: the *behavioral* scope of a one-shot run (CR-2 approval frames) is not bounded.
- **Actors:** CLI, local gateway, remote gateway, operator. Missing actor: the **credential
  store / master-key unlock** as a precondition of auto-start (MA-5).
- **Success criteria:** FRs are mostly testable. FR-5 ("auto-start … leave it running") has no
  observable done-condition (when is the gateway "ready"? — MA-5). FR-6 "usage" is unmeasurable
  offline (MI-2).
- **Failure modes:** Port contention (G4) covered; **approval-frame failure (CR-2), auto-start
  race (MA-5), unlock-needed-but-headless (MA-5), orphaned/stale-PID daemon (MA-4) are not.**
- **Implementation detail:** Sufficient to start *except* the three gated unknowns (G1/D7,
  G2, G3) — and G3 is mis-resolved (MA-1), not merely open.
- **Assumptions:** The "either WS or REST" assumption (MA-1) and "approval is just UI" assumption
  (CR-2) are the two implicit beliefs that don't hold.
- **Constraints:** Constraint #8 (contract-first) is *satisfied if WS-only* and *violated if*
  the team later extends `SseChatRequest` for `agent_id`/`model` without a contract change.
  Constraint #4 (cross-platform) is under-served by the daemon story (MA-4, Windows detach).
  Constraint #7 (fix everything green) is directly threatened by MA-2.

---

## Test Coverage / Testability Assessment

The ADR has no test plan (expected for an ADR; flagged so `/plan-spec` carries it):

- **Negative paths needing tests:** auto-start with port in use; auto-start with no
  non-interactive unlock available; concurrent double auto-start; approval frame arrives on a
  one-shot run; remote `--url` without TLS; stale PID file with reused PID.
- **Boundary:** first-call cold latency (NFR-5 `[UNKNOWN]` — needs the spike to set a real
  timeout, not a guess); empty prompt; agent name = reserved verb (MI-3).
- **Concurrency:** the two-writers hazard (MI-4) deserves a regression test asserting the CLI
  *never* opens an in-process engine when a gateway is reachable (NFR-1 guard).
- **Regression blind spot:** the co-ordinated caller updates (MA-2) — a CI assertion that
  `grep -r 'omnipus gateway'` in Docker/CI/worker/launcher is empty after the change (or that
  the alias still resolves), so the removal can't silently red-line our own pipeline.

---

## STRIDE Threat Summary (execute path + auto-start)

| Component | Threats |
|-----------|---------|
| CLI → local gateway auth (D7) | **S/E:** loopback-trust = spoof-any-local-process-as-admin + privilege escalation (CR-1, R1). Token-file (0600) mitigates; protect against world-readable token (0600 enforced + check). |
| CLI-owned token file | **I:** token at rest on disk — same class as `master.key`; 0600 + don't log it. **T:** if writable by other users, tamper → auth bypass. |
| Auto-started gateway | **D:** spawn race (MA-5) → port thrash; orphan accumulation (R3) → resource exhaustion. **E:** auto-start inheriting a too-broad env (`allowedChildEnvKeys`) could leak secrets to the child — confirm the spawn uses the hardened env allow-list. |
| Remote `--url` send (NFR-2) | **I:** bearer over non-TLS = token disclosure. Reject `http://` (OB-2). **S:** wrong-endpoint token reuse. |
| WS chat frame (no new types) | **R:** one-shot runs still hit the audit chain via the gateway (good — that's the whole point of not embedding the engine). Confirm the CLI principal is attributable in audit (token-file user identity), not "_dev_bypass". |

---

## Unasked Questions (for the ADR author / `/plan-spec`)

1. **What does a one-shot run *do* when an `ask`-policy tool fires?** (CR-2) Auto-deny-and-continue,
   90 s-hang-then-deny, or `--auto-approve`? This is happy-path, not edge.
2. **Is the execute transport WS-only?** (MA-1) If yes, say so and retire G3. If REST is wanted,
   that's a `SseChatRequest` contract change — which breaks "no new wire types."
3. **Does the `gateway` alias survive?** (MA-2) Keeping one line back-stops our own Docker/CI/worker/
   launcher and external scripts. Why delete it at all?
4. **Ephemeral vs persistent auto-start.** (OB-1, MA-4) Why is "leave it running" the default rather
   than an ephemeral gateway that serves the one task and exits — eliminating the daemon, orphan,
   stop-command, and PID-staleness problems wholesale? The operator chose C, but "leave running"
   vs "ephemeral" is a sub-decision the ADR asserts (FR-5) without weighing.
5. **What unlock mode does headless auto-start require?** (MA-5) Auto-start can't use the
   interactive-TTY prompt; it needs `master.key`/`OMNIPUS_KEY_FILE`/`OMNIPUS_MASTER_KEY`. State the
   precondition and the failure message when absent.
6. **Is the CLI principal a real RBAC user or a synthetic one?** (CR-1, STRIDE-R) For audit
   attribution it should be a named principal, not the `_env_token`/`_dev_bypass` synthetics.

---

## Verdict

**PASS-WITH-CONDITIONS (REVISE).** The decision's direction is sound and ratifiable; do not
re-open Options A/B/C. But the ADR is **not yet spec-ready** until:

1. **MA-1 / G3 fixed:** execute path is declared WS-only (or a `SseChatRequest` contract change is
   scoped, accepting it breaks "no new wire types"). G3 is mis-resolved, not merely open.
2. **CR-2 resolved:** the one-shot run's behavior on `*_approval_request` frames is specified.
3. **CR-1 committed:** D7 commits to the **CLI-owned 0600 token-file** direction (loopback-trust
   explicitly rejected), carried as a security-lead-gated `/plan-spec` decision.
4. **MA-2 / MA-3 right-sized:** removals own their co-ordinated internal-caller updates (Constraint
   #7), and the `gateway` alias is kept or a deprecation shim is chosen — not "a release note."
5. **MA-4 / MA-5 bounded:** daemon lifecycle (PID/detach/Windows) and auto-start readiness/race/unlock
   preconditions are specified, or auto-start is descoped to Option A / ephemeral for v1.

D7 staying **Low** confidence is acceptable *as a gated condition* once CR-1 picks the token-file
direction. The other Low-risk items (G2/G4) are real but resolvable in `/plan-spec`.

---

### Next action

```
Verdict: PASS-WITH-CONDITIONS (REVISE)

Review written to: docs/internal/architecture/ADR-024-cli-minimization-review.md

Fold CR-1, CR-2, MA-1..MA-5 into the ADR (or carry CR-1/G2/G4 as explicit gated
/plan-spec decisions), then proceed:
  /plan-spec docs/internal/architecture/ADR-024-cli-minimization.md
with security-lead owning the G1/D7 local-auth handshake.
```
