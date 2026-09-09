# ADR-077 — Two-layer tool-policy model: the reconciled global ceiling IS the default; remove the fail-closed per-agent backfill

- **Status:** Accepted (2026-09-06)
- **Deciders:** Daniel Piatkowski (operator, ratifying decision); architect (design)
- **Supersedes-in-part:** [[ADR-076]]. ADR-076's `ReconcileToolPolicyCeiling` stands unchanged and becomes load-bearing. What ADR-076 framed as still-valid — `RepairIncompleteToolPolicyCoverage` as a "true backstop… fires only for a genuine drift" (ADR-076 Consequences; D1 step 3) — is retired here. ADR-076 D3/D5 (additive-only, in-memory-only) still hold for Reconcile.
- **Extends:** [[ADR-071]] §5.3.5 (legacy key migration, unchanged and still runs first).

## Operator decision (verbatim)

> "there must be no safety, there must be always the global setting which is the default. remove the safety. there should only be the global and per agent policy and that's it."

## Context

CLAUDE.md hard constraint 6 removed the `DefaultPolicy`/`GlobalDefaultPolicy` code-branch fallback: every static builtin tool must resolve from an explicit, literal, wildcard-free policy entry. To keep that true on upgraded installs whose on-disk `config.json` predates a newly-added tool, the load path grew a chain of compensations that, taken together, reintroduced a code-branch default by the back door:

1. **`ReconcileToolPolicyCeiling` (ADR-076)** — backfills the GLOBAL ceiling (`cfg.Sandbox.ToolPolicies`) with each missing catalog tool's *shipped default* from `DefaultConfig()`. Correct, operator-visible, value-preserving.
2. **`RepairIncompleteToolPolicyCoverage` (2026-07-07, the "safety")** — for any `(agent, tool)` still uncovered on *both* sides, backfills an explicit **per-agent `deny`** onto that agent's own map. Fail-closed by construction, and — because from its point of view a global-ceiling gap and a genuine uncovered tool look identical — it decides new-tool posture for every agent whenever the ceiling was incomplete. This is a code-branch default in all but name: the answer is "deny", chosen by code, not by seeded data.
3. **`ValidateToolPolicyCoverage`** — the hard tripwire; aborts boot if any `(agent, tool)` is uncovered on both sides.
4. **`ValidateAgentOwnToolPolicyCoverage`** — a boot-time **ERROR** log (`gateway.go` `repairAndValidateToolPolicyCoverage`, near the old "gateway.go:1381" reference) naming every agent that has its own policy map but no explicit entry for some tool, so that tool "resolves from the ceiling alone".

With ADR-076 in place, mechanism 2's premise is gone: the reconciled ceiling is always complete for the static catalog (guaranteed one-for-one by `TestCatalog_MatchesGlobalCeilingEntryForEntry`, which asserts `defaults.go` carries a ceiling entry for every name in `coreagent.AllStaticToolNames()`). A both-sides gap for a catalog tool is therefore impossible after Reconcile. Mechanism 2 can no longer fire for its intended purpose — it can only misfire, silently denying a newly-shipped `allow` tool on some code path Reconcile didn't precede.

The operator ratifies the model this implies: **two layers, no third.** The reconciled global ceiling is *the default*; per-agent entries *tighten*. The fail-closed backfill is the "safety" to remove.

### Resolution semantics this rests on (verified, `pkg/tools/compositor.go` `resolveEffectivePolicyWith`)

The global×agent merge is **strictest-wins** (`deny > ask > allow`). When only one side has an entry, that side alone decides. Because Reconcile keeps the global side complete for every catalog tool, every tool always has a global entry — so a per-agent entry can only ever make a tool *stricter*, never looser. This is exactly a ceiling-and-tightening model, and it is what makes "two layers, nothing else" coherent rather than aspirational.

The compositor's own `g == "" && a == ""` → Error-log-and-`deny` branch is a *resolution-time* structural assertion, not the config-load "safety" being removed. It stays: it is unreachable for catalog tools once the ceiling is complete, and a fail-closed bug-signal is the correct thing to keep at the resolution boundary. Removing the load-time backfill does not touch it.

## Decision

### D1 — Ratify the two-layer model

Tool policy has exactly two layers and no implicit third:

- **Layer 1 — the GLOBAL ceiling** (`cfg.Sandbox.ToolPolicies`). Kept COMPLETE for the whole static catalog by `ReconcileToolPolicyCeiling` on every load (ADR-076). This layer **is the default** for every tool: a tool an operator never mentions per-agent resolves to its ceiling value.
- **Layer 2 — PER-AGENT overrides** (`AgentConfig.Tools.Builtin.Policies`). Deliberately sparse. Under strictest-wins these only ever *tighten* below the ceiling; a per-agent value looser than the ceiling has no effect. An agent with no entry for a tool rides the ceiling — that is the intended, normal state, not a defect.

There is no code-branch default and no fail-closed backfill between these two layers. The seed (`pkg/config/defaults.go` + `pkg/coreagent/core.go`) chooses every default value; Reconcile carries that seed forward on upgrade; the operator edits either layer freely afterward.

### D2 — Reconcile-to-shipped-default is intended, including `bash = allow`

A tool governed *only* by the global ceiling resolves to its shipped default. For `bash` that is `allow` (Constraint 6: `bash` is registered for every agent and the kernel sandbox is the protective layer). This is correct and accepted under the operator's decision. Consequences that are now explicitly ratified, not bugs:

- A deliberately-emptied `sandbox.tool_policies` **re-populates to shipped defaults** on the next load (Reconcile adds every missing catalog entry). "Always the global setting which is the default" is the operator's stated intent; there is no config state that produces "no default".
- To lock a tool down, an operator sets an explicit `deny` — per-agent (tighten one agent) or global (tighten the ceiling for everyone). Reconcile **never overwrites an existing entry** (ADR-076 D2), so an operator `deny` is permanent until the operator changes it.

The security trade-off is stated honestly in *Risks* below; the operator has accepted it.

### D3 — Remove `RepairIncompleteToolPolicyCoverage` from the load path, and delete it

`config.RepairIncompleteToolPolicyCoverage` is removed from `repairAndValidateToolPolicyCoverage` (`pkg/gateway/gateway.go`) and **deleted** — function and its dedicated tests — rather than left dead.

Rationale for delete over leave-dead: a fail-closed deny-backfill sitting in `pkg/config` is exactly the kind of "safety" a future reader re-wires "to be safe", silently reintroducing the code-branch default the operator removed. Deleting it makes the two-layer model the only reachable behavior. The retirement note pattern already used for `RepairMultipleDefaults` (`pkg/config/validate.go`, the ADR-054 D6.4 comment) is the template: leave a short comment block where it stood explaining it is retired, not renamed, and why re-adding it is a regression.

The post-load sequence in `repairAndValidateToolPolicyCoverage` becomes:

1. `config.MigrateLegacyToolPolicyKeys` (ADR-071, unchanged) — rename retired keys forward.
2. `config.ReconcileToolPolicyCeiling` (ADR-076, unchanged) — keep the global ceiling complete at shipped defaults.
3. `config.ValidateToolPolicyCoverage` (kept as tripwire — D4) — assert completeness; should never fire.

### D4 — Keep `ValidateToolPolicyCoverage` and the boot abort, as a never-firing correctness tripwire

`ValidateToolPolicyCoverage` and its hard boot-abort stay. This is **not** a third policy layer and does not contradict "only global and per-agent":

- It resolves *nothing*. It sets no policy, chooses no value, and is invisible to the running system in every normal case.
- After Reconcile guarantees ceiling completeness for the catalog (drift-guarded by `TestCatalog_MatchesGlobalCeilingEntryForEntry`), it can only fire on a genuine internal drift — a catalog tool with no `defaults.go` entry, so Reconcile had no value to supply and skipped it. That is a build-time bug in *our* seed, not an operator configuration state.
- A never-firing assertion that aborts loudly when an impossible condition becomes real is a correctness guard, categorically different from a fallback that decides policy. Keeping it is cheaper than the failure mode it catches (a catalog tool silently unresolvable → the compositor's fail-closed `deny` with only an Error log).

The write-time coverage checks (`ValidateSubmittedToolPolicyMap` at agent create/update/tools-write, `pkg/gateway/rest.go` + `rest_tool_policies.go`, 400 on an incomplete or wildcarded submitted map) are **unaffected and retained** — they guard the write boundary against a malformed body dropping an agent's tightening, which is the real defense the removed backfill was mistakenly credited with. They are out of scope of "remove the safety": the operator's decision is about the *load-time deny backfill*, not write validation.

### D5 — Remove the `ValidateAgentOwnToolPolicyCoverage` boot ERROR log

Under the two-layer model, an agent having no per-agent entry for a tool — resolving it from the ceiling — is the **normal, intended** state for most tools on most agents (Layer 2 is deliberately sparse, D1). An ERROR-level boot log naming every such `(agent, tool)` pair is therefore pure noise: on a healthy install it fires for the overwhelming majority of the agent×tool matrix, trains operators to ignore it, and asserts a per-agent-completeness expectation the ratified model explicitly rejects.

Remove the `config.ValidateAgentOwnToolPolicyCoverage` call and its Error log from `repairAndValidateToolPolicyCoverage`, and **delete the function and its tests**. The concern it named (a lost per-agent tightening) is covered at the write boundary by `ValidateSubmittedToolPolicyMap` (D4), which is where a tightening can actually be dropped; a boot-time re-derivation of the same concern, at ERROR, over an intentionally-sparse layer, is not worth its false-positive rate.

*(Conservative fallback if deletion is contested: downgrade the log from Error to Debug and keep the function callable for ad-hoc audits. Recommended path is delete — a sparse Layer 2 is the design, so there is nothing legitimate for a per-agent-completeness diagnostic to warn about.)*

### D6 — Non-reintroduction, enforced mechanically (a note cannot stop `git merge`)

The removed fail-closed per-agent deny backfill must never come back silently. Branches cut before this change still contain `RepairIncompleteToolPolicyCoverage` and its wiring, so an ordinary merge/rebase re-adds it as a conflict-free addition. **Resolve every such conflict by keeping the deletion** — re-adding any per-agent deny-backfill (or the own-coverage ERROR log) is a regression, not a conflict resolution. This mirrors the "Retired surfaces — do NOT reintroduce" precedent (JPEG screencast, Command Center) already in CLAUDE.md.

Because a directive alone cannot block a merge, backend MUST land two concrete guards alongside the deletion:

- **Guard 1 — behavioral (Go test).** The rewritten `TestRepairAndValidate_BothSidesGap_*` in `pkg/gateway/tool_policy_boot_validation_test.go` (see test-impact) asserts that an empty-ceiling + empty-agent config, after `repairAndValidateToolPolicyCoverage`, leaves the agent's own `Tools.Builtin.Policies` map **empty** and resolves `bash` to **allow** from the reconciled ceiling — i.e. no `bash: deny` was stamped onto the agent. If any deny-backfill is reintroduced into the load path, this assertion fails loudly. Name it for what it now guards, e.g. `TestRepairAndValidate_BothSidesGap_ResolvesFromReconciledCeiling_NoDenyBackfill`.

- **Guard 2 — source scan (shell script wired into CI + make),** modeled exactly on `scripts/check-no-jpeg-screencast.sh`. Add `scripts/check-no-fail-closed-backfill.sh` that fails the build if either retired symbol reappears as a *definition* or a *call* in non-comment Go source:
  - fail on `func RepairIncompleteToolPolicyCoverage` or `func ValidateAgentOwnToolPolicyCoverage` (a re-added definition);
  - fail on a non-comment call `RepairIncompleteToolPolicyCoverage(` or `ValidateAgentOwnToolPolicyCoverage(` (a re-added wiring);
  - scope to `pkg/` and `cmd/` `*.go`; exclude lines beginning with `//` (so the sanctioned retirement comments that name the symbol in prose do not trip it) and exclude `docs/`.

  Wire it into all three the way the JPEG guard is wired: `.github/workflows/pr.yml` (lint job), `deploy/ci-worker/runci.sh` (`lint` gate), and a `make lint-no-fail-closed-backfill` target (add it to the aggregate `lint` target). CI is where the guarantee actually lives — the CLAUDE.md clause points at the script, the script is what a merge cannot get past.

## Consequences

**Positive**
- The two-layer model is the only reachable behavior; no code path silently decides a tool's posture. "No code-branch default" (Constraint 6's own aim) is finally true — the reconciled seed is the sole default source.
- A newly-shipped `allow` tool (e.g. `AskUserQuestion`) resolves to `allow` on every upgraded install, from the operator-visible ceiling value, instead of being silently denied per-agent.
- Boot/reload logs go quiet on healthy installs: the removed WARN (backfill) and ERROR (own-coverage) noise is gone; a log now means something.

**Negative / accepted**
- A dropped or absent per-agent tightening now resolves to the ceiling default rather than fail-closed `deny`. For a permissive-by-default tool like `bash`, that means "loses tightening → allowed", not "→ denied". Accepted by the operator; the write-time guard (`ValidateSubmittedToolPolicyMap`) remains the point where a tightening can be dropped, and it fails the write closed with a 400.
- `config.json` on disk can still omit a ceiling entry the runtime resolves correctly (ADR-076 D5 in-memory-only limitation, unchanged; still tracked as follow-up).

**Out of scope (unchanged from ADR-076):** disk persistence of the reconciled ceiling, retired-key cleanup, per-agent map backfill.

## Risks (flagged honestly; the decision implements the operator's ruling regardless)

- **R1 — `bash = allow` from the ceiling alone.** Any agent with no explicit `bash` entry resolves `allow`. The seed grants Jim `bash: allow` deliberately; other seeded agents that must not run shell carry an explicit per-agent `deny` (e.g. verifiers/Judge). The risk is a *custom* agent created without a `bash` tightening — but `ValidateSubmittedToolPolicyMap` forces a complete map on create/update, so a UI-created agent always has an explicit `bash` value. The exposure is narrowed to hand-edited configs, where the operator has taken the wheel. Mitigation available to any operator: set `bash: deny` at the global ceiling and grant it per-agent only where wanted — Reconcile never overwrites that.
- **R2 — emptied ceiling re-populates to permissive defaults.** An operator who empties `sandbox.tool_policies` expecting deny-all gets shipped defaults instead. This is the operator's explicit intent ("always the global setting which is the default"), but it is a foot-gun for anyone assuming empty = deny. Documented in D2; the correct lock-down is explicit `deny`, not deletion.
- **R3 — removing the ERROR log (D5) drops the only boot-time surfacing of a lost tightening.** Accepted because (a) it was noise-swamped to uselessness under the sparse-Layer-2 design, and (b) the write boundary is where the loss actually happens and is still guarded.

## Affected components

- Backend: `pkg/config/validate.go` (delete `RepairIncompleteToolPolicyCoverage`, `ValidateAgentOwnToolPolicyCoverage`; leave retirement comments), `pkg/gateway/gateway.go` (`repairAndValidateToolPolicyCoverage` — remove steps 3 and the own-coverage ERROR log; update the ordered doc comment). No wire-format change; no frontend change.
- Docs: CLAUDE.md hard constraint 6 (rewording below); this ADR; ADR-076 status note (mark its Repair framing superseded-in-part).
- Variants: identical across Open Source / Desktop / SaaS — this is load-path config logic, platform-independent.

## Verification

- `ReconcileToolPolicyCeiling` tests (ADR-076) unchanged and still pass.
- Rewrite `TestRepairAndValidate_BothSidesGap_IsRepairedNotAborted` → assert the two-layer outcome: empty ceiling + empty agent → Reconcile fills the ceiling → zero gaps → `bash` resolves `allow` from the ceiling, the agent's own map stays empty (see test-impact table).
- Seed tests that assert explicit per-agent `deny` (Judge `inspect_session`, verifier `bash`, PlanSupervisor) are unaffected — they assert `SeedConfig` output, never call Repair.
- CI is authoritative for the full suite; run `-run '^TestReconcile|^TestValidateToolPolicyCoverage'` locally only, per the OOM-avoidance rule.
