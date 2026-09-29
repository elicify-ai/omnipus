Status: Final — grill-spec round 1 of 2 (fixed)

# Grill-Spec Review — Tool Iteration Limit (round 1)

**Reviewer**: architect (independent grill-spec pass, adversarial — did not author this spec)
**Input**: `docs/internal/specs/tool-iteration-limit-spec.md` @ commit `87248bb`
**Founder record**: `docs/internal/specs/tool-iteration-limit-interview.md` (D1–D15, final — not re-opened here)
**ADR correction checked**: `docs/internal/architecture/ADR-066-context-budget-and-tool-result-routing.md` @ `51ed140` §14 item 6
**Mode/round detection**: path is `docs/internal/specs/*-spec.md` with `Status:`/BDD/Traceability → spec mode; no `-spec-review.md` existed before this run → **round 1**.
**Tooling note**: GitNexus unavailable in this container. Every code claim below was checked with Read/Grep on `feature/904-tool-iteration-limit`, cited `file::symbol`. Certainty is **Verified** unless marked otherwise.

## Executive summary

The spec is unusually well-grounded: nearly every code claim in "Existing Codebase Context" checked out exactly against the tree (resolver ladder, the four found bugs, the `AgentProfile.tsx` line count, the `EffectiveGoalMaxRounds`/`SelfHealWriteHook` precedents, the single `gen.Agent{}` construction site, the `requireReAuth` single-use token). No contradiction with a retired surface, a hard constraint, or the D1–D15 decisions was found. Two structural gaps are serious enough to block: a known contract trap the codebase has already hit once (nullable vs. omitted) is not addressed for the field this spec adds it to, and the D11 consent flow has a preview/confirm race that can lower an agent the admin never approved. Neither is fatal to the design; both need a stated fix before GREEN.

**Verdict: REVISE** (no CRITICAL found; two MAJOR findings and several MINOR findings, listed below).

## Findings

### F1 — MAJOR — `AgentUpdateRequest.max_tool_iterations` nullable trap not addressed (Lens 2, Lens 5)

**Failure scenario**: backend-lead implements the PUT handler the same way every other handler in this file reads a `*int` field — `if req.MaxToolIterations != nil { set it }` — and FR-008/D9's "Use global limit" (send `null` to clear) silently does nothing, because oapi-codegen collapses "field omitted" and "field explicitly null" to the same Go `nil` for a `nullable: true` integer.

**Evidence**: `pkg/gateway/rest_agents_update.go::(*restAPIUpdateAgentFlow)` already hit this exact trap for the sibling field `context_window_override` (also `nullable: true`) and had to work around it with a raw-body peek: "the generated `*int` collapses 'absent' and 'null' to nil, but the contract gives them different meanings ('send null to clear'), so peek the raw body for an explicit null exactly as the sandbox_profile sniff above does" (`rest_agents_update.go` lines around `windowPeek`). Confirmed in the generated type: `pkg/api/generated/openapi_types.gen.go::AgentUpdateRequest.ContextWindowOverride *int \`json:"context_window_override,omitempty"\``. The spec's own contract table (`AgentUpdateRequest` row) proposes the identical shape — `nullable: true`, "Omitted = unchanged; `null` = clear" — for `max_tool_iterations`, but nowhere in the spec (Contract Changes, API and Data, Edge Cases, or the Test-Driven Development Plan) is the raw-body-peek requirement named. Test row 7 (`TestAgentUpdate_MaxToolIterations_*`) would likely catch this at GREEN if qa-lead writes the null-clear case correctly against the generated type — but the generated type itself cannot express "send explicit null" in a Go test without either raw JSON or the same peek trick, so there is a real risk the RED test is written against the wrong oracle (asserting on the Go struct's nil-ness, which is true either way) and passes for the wrong reason.

**Recommendation**: add an explicit line to "API and Data" or a new sub-bullet under FR-008 naming the exact mechanism: peek the raw request body for `"max_tool_iterations": null` the same way `context_window_override` does, and require the RED test for the clear-scenario to construct the PUT from raw JSON (not the generated struct) so an implementation that only checks `!= nil` fails red.

### F2 — MAJOR — D11 preview→confirm has no concurrency guarantee; the PUT can lower agents the admin never saw (Lens 2, Lens 6 repudiation/tampering)

**Failure scenario**: Admin A opens the global field, changes it, and the SPA calls the preview endpoint, which returns `{agents: [{Agent1, 250→200}]}`. Before A clicks Confirm (or in the time between the preview call and the PUT — e.g. another admin, or an automated create/update via the system-agent tools, changes Agent2's own value to 220), A confirms. The PUT contract only carries a boolean `lower_agent_limits: true`, not the specific agent snapshot A approved. Per "Gateway ↔ agent store / config.json" ("D11 writes agents first, then the global"), the PUT presumably re-derives "which agents are above the new global" **at PUT time**, which can now include Agent2 — an agent the confirm dialog never listed. D11's stated design goal ("a confirm dialog lists each affected agent... before anything changes") is violated: an agent gets rewritten without ever appearing in what the admin approved.

**Evidence**: `contracts/components/schemas/PerformanceSettingsUpdate.yaml` (proposed) row: "`lower_agent_limits` (boolean): must be `true` when the new global is below any agent's own stored value... otherwise the PUT is refused 409" — a bare boolean carries no snapshot. Compare the spec's own edge case for the opposite race ("Agent PUT races the D11 lowering → the lowering reads each agent's current revision and retries once on revision conflict") — that edge case is handled with a revision check; the mirror case (an agent's value drifts *upward or newly-into-range* between preview and confirm) has no equivalent guard anywhere in Edge Cases, the Behavioral Contract, or the BDD scenarios.

**Recommendation**: either (a) have the PUT accept the previewed agent list (ids + expected old values) and only lower exactly that set, refusing/reporting drift for any agent whose current own value no longer matches what was previewed (mirrors the existing revision-conflict pattern already in Edge Cases), or (b) explicitly document as an accepted risk that the boolean consent is to "the global change and whatever the ceiling implies at write time," not to the specific list shown — and get the founder's sign-off on that reading, since it weakens D11 as currently worded. This is not a design decision architect can make unilaterally against a founder decision on this branch — added to "Questions for the founder" below.

### F3 — MINOR — "one warning log line at startup" is ambiguous for more than one capped agent (Lens 1)

**Failure scenario**: qa-lead writes `TestUpgradeBootLog_ListsCappedAgents` against the single-agent case (Dataset row only covers one agent, `A`). With two or more upgraded agents above the global, "one warning log line... lists every such agent" (FR-009, Requirements) could mean one line total naming all of them, or one line per agent (each "listing" its own value) — the spec uses "one...line" (singular) in FR-009 and the BDD scenario text but never states the multi-agent shape, and Ambiguity Warning #2 only discusses whether a *later reload* re-logs, not the multi-agent single-boot case.

**Evidence**: `docs/internal/specs/tool-iteration-limit-spec.md` FR-009 ("listed once in a startup WARN log"); Scenario "Upgrade keeps a stored value above the global" (single agent `A`); Dataset "Resolver" has no multi-row-per-boot case.

**Recommendation**: state explicitly, e.g. "exactly one WARN log call at startup, with every capped agent's id/stored/effective packed into that single line's fields (or message)" — and add a two-agent case to the boot-log test.

### F4 — MINOR — D11 result toast's accessibility/announcement not specified (Lens 9)

**Failure scenario**: the confirmation toast ("Lowered N agents: A 250 → 200, …") is the *only* on-screen record of exactly which agents a consequential bulk admin action touched, once the confirm dialog itself has closed. The Accessibility and Keyboard section names the dialog, the warning banner, the input labelling and the reset button, but says nothing about how the toast is announced to a screen-reader user (live region politeness, whether it persists long enough to read a multi-agent list).

**Evidence**: `docs/internal/specs/tool-iteration-limit-spec.md`, "Accessibility and Keyboard" section (5 bullets, none mention the toast); UI Screens and States table lists the toast only under "Success" for the Performance row, with no accessibility column entry.

**Recommendation**: add one line confirming the toast uses the existing accessible toast primitive with an `aria-live="polite"` region (or name the actual mechanism if different), and that a multi-agent list is fully readable before the toast auto-dismisses (or does not auto-dismiss when it lists changes).

### F5 — OBSERVATION — "Assumption... confirm in GREEN" was actually verifiable now, and is correct

**Evidence**: `pkg/config/config.go::AgentsConfig.List []AgentConfig \`json:"-"\`` — the field is not serialized to `config.json` at all (tag `"-"`), and `pkg/agentstore/state.go` persists `*config.AgentConfig` directly. This confirms the spec's "Assumptions" claim ("the agent store... is the only persistence for per-agent values; `config.json agents.list` is not a second writer") is true today, not merely an assumption to punt to GREEN. Not a defect — noting only because the spec deferred a check that Read/Grep on this same branch could have closed now, which is a general pattern worth tightening in future specs' "Existing Codebase Context" passes.

### Lens-by-lens notes not already covered above

- **Lens 3 (ADR/AS-IS consistency)**: `AS-IS-architecture.md` has zero mentions of `max_tool_iterations` today (grep, 0 hits) — no contradiction possible; note this is itself a gap the doc will need filling post-landing (spec-sync's job per the process, not a spec-mode finding).
- **Lens 4 (infeasibility)**: none found; pure-Go, single-binary, no CGo constraints are untouched by this feature.
- **Lens 6 (STRIDE)**: repudiation is well covered (audit on every D11 agent write + the global write); the `code`/`field` machine-readable columns on `ErrorResponse` are left unset by the spec's Machine-Verifiable Constraints (message-text only) — checked against precedent (`context_window_override`'s own validation error uses plain `jsonErr`, message-only) and this matches existing style, so **not** flagged as a finding.
- **Lens 7 (reachability)**: solid — verified the claim that `gen.Agent{}` is constructed in exactly one file (`pkg/gateway/rest_agents.go`), so the two-symbol impact-assessment row for the schema change is not undercounting call sites.
- **Lens 10 (design-system reuse)**: verified every cited component (`field.tsx`, `input.tsx`, `label.tsx`, `button.tsx`, `FormError.tsx`, `confirm-dialog.tsx`, `AutoSaveIndicator.tsx`, `skeleton.tsx`, `card.tsx`) is a real `design-system/catalog.json` entry — no fabricated catalog reuse.
- **Lens 12 (overcomplexity)**: the two-endpoint preview+confirm dance is justified by D11 plus the single-use step-up token constraint (verified: `reauthStoreOrInit().consume(...)` really is single-use) — removing either endpoint breaks a stated founder requirement, so this passes the "remove one layer" test.

## Questions for the founder

Team-lead: please fold these into the interview after this round, alongside the two the spec author already flagged.

1. **(New, from F2) D11 consent scope**: when an agent's own value changes between the preview the admin sees and the confirm they click, should the PUT (a) refuse/re-preview if the live set no longer matches what was shown, or (b) proceed and lower whatever is above the new global at write time regardless of what the dialog listed? The spec currently implies (b) implicitly (a bare boolean flag, no snapshot) without ever asking this question. Recommend (a) to match the "before anything changes" language of D11, but this is a founder call because D11 is a founder decision and (b) would be a real weakening of it.
2. **Q1 (spec author's, endorsed)** — negative/non-numeric values (saved global < 0, non-numeric env var, per-agent ≤ 0): I agree this needs the founder. The spec's own three tentative answers (below_min for negative saved global; "not imported" + WARN for non-numeric env; "no own value" for per-agent ≤ 0) are internally consistent with D13/D1's spirit, so a "confirm as tentatively spec'd" answer is a reasonable fast path if the founder does not want to relitigate each case individually.
3. **Q2 (spec author's, endorsed with evidence)** — does a running turn keep its starting limit? I agree this needs sign-off, but note the spec already has a real precedent for "yes": `contracts/components/schemas/PerformanceSettings.yaml`'s existing `goal_max_rounds` field states outright "**A goal keeps the value in force when it started**" for the sibling per-turn/per-goal limit on the very same screen. Recommend confirming "yes, same rule" as close to a formality given that precedent, unless the founder wants the two limits to diverge.
4. **(New, from F3)** for the startup WARN log with more than one capped agent: one line total, or one line per agent? Low-stakes but affects a test's oracle.

## Next action

Verdict: REVISE

Review written to: `docs/internal/specs/tool-iteration-limit-spec-review.md`

This is grill round 1 of 2 (fixed). Next: team-lead interviews the founder on "Questions for the founder", then the spec author fixes round-1 findings, then grill-spec runs SPEC MODE ROUND 2 on the corrected spec at `docs/internal/specs/tool-iteration-limit-spec.md` — regardless of this round's verdict.
