Status: Final — grill-spec round 2 of 2 (fixed, final)

# Grill-Spec Review — Tool Iteration Limit (round 2, final)

**Reviewer**: architect (independent grill-spec pass, adversarial — did not author this spec)
**Input**: `docs/internal/specs/tool-iteration-limit-spec.md` @ commit `eb2d315`
**Founder record**: `docs/internal/specs/tool-iteration-limit-interview.md` (D1–D19, all final — not re-opened here)
**Round-1 review**: `docs/internal/specs/tool-iteration-limit-spec-review.md` (verdict REVISE, F1–F5)
**Round detection**: `-spec-review.md` exists, `-spec-review-round2.md` did not before this run → **round 2, final** (no round 3 regardless of this verdict).
**Tooling note**: GitNexus unavailable in this container. Every code claim below re-checked with Read/Grep on `feature/904-tool-iteration-limit` @ `eb2d315`, cited `file::symbol`. Certainty **Verified** unless marked otherwise.

## Executive summary

D16–D19 close the round-1 gaps cleanly on paper: F3 (multi-agent WARN line), F4 (toast accessibility) and F5 (assumption already verified) are genuinely fixed, and I re-verified their new claims against code (below) — both hold. F1 and F2 are **only half-fixed**: each got a correct, well-specified answer for the REST path, but the corrected spec asserts (F1) or leaves stale (F2) a second call site/section that contradicts the fix when checked against actual code or against the spec's own other sections. Neither is a new design decision — both are spec-authoring gaps the fix round can close without another founder interview.

**Verdict: REVISE** (0 CRITICAL, 3 MAJOR, 2 MINOR). No CRITICAL, so no "Escalation to the founder" heading is required by the process — but since this is the fixed, final grill round, the three MAJOR findings below must be closed by the spec author before team-lead plans, or explicitly deferred to the founder by team-lead if the author disagrees with the recommended fix.

## Findings

### G1 — MAJOR — Three sections of the spec now describe **three different** outcomes for a per-agent write conflict during the D11 lowering (Lens 3, internal contradiction)

**Failure scenario**: backend-lead implements the D11 lowering write path and has to pick one of three mutually exclusive behaviors the spec itself states, each in a different section, none of them marked as superseding the others. Whichever is picked, at least one other passage (and its associated edge case) becomes false, and if the two older passages are followed instead of the new D16 model, the shipped behavior violates D16 (a final founder decision).

**Evidence** — three passages, all present verbatim in the current spec, describing the identical failure (an agent's own-value write conflicts during the D11 lowering):
1. Edge Cases (unchanged since round 1): *"Agent PUT races the D11 lowering → the lowering reads each agent's current revision and retries once on revision conflict; an agent that still conflicts is reported as **not lowered** (it is then in the D1 capped state — effective is still the new global, so the ceiling holds)."* — a **partial-success, retry-once, soft-skip** model; no error status implied.
2. Integration Boundaries → "Gateway ↔ agent store / config.json" (unchanged since round 1): *"D11 writes agents first, then the global; any agent write failure aborts before the global is written and returns **500** listing which agents were already lowered (**each already audited**)."* — a **partial-commit, 500** model: some agents are durably written and audited, others are not, in the same request.
3. "D11/D16 write order" (new in the round-2 fix): *"(5) lower each confirmed agent via `agentstore` `MutateState` (revision-checked; **a revision conflict here is drift → 409, never a silent retry onto an unseen value**), auditing each"* — an **all-or-nothing, 409, nothing written** model, matching D16's own wording ("nothing is saved").

These three cannot all be true of the same code path. (2) further conflicts with (3) on the HTTP status for the same failure (500 vs. 409) and on whether a partial write can ever be durable (yes vs. never). The round-2 fix, in adding passage 3 to satisfy D16/F2, never revisited passages 1 and 2, which were written before D16 existed and describe the pre-D16 model D16 was created to replace.

**Recommendation**: delete or rewrite Edge Cases bullet 1 and the Integration Boundaries "On failure" bullet to state the single model D16 mandates: a `MutateState` revision conflict discovered during step 5 is drift, exactly like a discrepancy found at step 4 — 409, nothing written for any agent in this request, the caller reloads the dialog. This also simplifies (2): the "500 listing which agents were already lowered" case no longer exists once the write is atomic-on-drift.

### G2 — MAJOR — F1's fix is verified correct for the REST path but the spec's claim about the system-agent path is false against current code (Lens 2, Lens 11)

**Failure scenario**: `update_agent` is called with `max_tool_iterations: null` (BDD Scenario "System agent clears an own value", FR-008). Backend-lead trusts the spec's own claim that this already works "the same" as the REST fix and does not add special-case handling. The call returns an `INVALID_INPUT`-style error ("max_tool_iterations must be an integer") instead of clearing the value — the scenario fails, silently contradicting D9 for the one call path (chat/system-agent) D15 says must have no second, different behavior from the REST path.

**Evidence**: the round-2 fix's new "Explicit `null` vs omitted on agent PUT (grill F1)" bullet states: *"The system-agent `update_agent` path gets the same distinction from its `map[string]any` args (key present with `nil` = clear)."* This is asserted, not verified against code, and it is wrong today: `pkg/sysagent/tools/agent_apply_args.go` (the `update_agent` arg-application function) does:
```
if raw, present := args["max_tool_iterations"]; present {
    n, err := jsonInt(raw, "max_tool_iterations")   // raw == nil here for JSON null
    ...
}
```
and `jsonInt` (`pkg/sysagent/tools/agent_apply_args.go::jsonInt`) is `f, ok := raw.(float64); if !ok { return 0, fieldErr(field, field+" must be an integer") }` — a nil `raw` fails the type assertion and returns exactly that error. The map-level `present` check does correctly distinguish "absent" from "present" (unlike the REST generated struct, which was F1's actual problem), but nothing downstream of `present` special-cases `raw == nil` before handing it to `jsonInt`. The spec's claim that the two paths "get the same distinction" already holds is not true; it holds only for detecting presence, not for turning that presence into a clear action.

**Recommendation**: add an explicit line (parallel to the REST windowPeek instruction) naming the fix: in `agent_apply_args.go`, before calling `jsonInt`, check `if raw == nil { a.MaxToolIterations = 0; return nil-branch-of-clear }` (clear, mirroring the REST "own value key removed" outcome) and only fall through to `jsonInt` for a non-nil `raw`. Add this to test row 11 (`TestSysagentAgentTools_MaxToolIterations`) explicitly, the way row 13b was added for the REST leg.

### G3 — MAJOR — The new 409 `MaxToolIterationsLoweringConflict` body's SPA-side consumption mechanism is unspecified, despite a directly analogous precedent already in the codebase (Lens 2, Lens 5)

**Failure scenario**: frontend-lead implements the D11 confirm dialog's drift-handling ("on a 409 drift answer... replaces its list with the 409's `preview`") against the generic error-handling the SPA already has, which does not expose a `preview` field: `ApiError.fromResponse` (`src/lib/api-error.ts`) parses a non-2xx body only as `{code?, error?, message?}` and stores the rest as an opaque `.body` string explicitly documented "Never display this directly to end users." Without a dedicated typed-error class, the dialog cannot reach the `preview` array at all through the normal `request()`/`ApiError` path, and a developer under time pressure is likely to either silently swallow the field or hand-parse `err.body` ad hoc, inconsistent with how this exact problem (a 409 body carrying extra structured recovery data) is already solved twice in this codebase.

**Evidence**: `src/lib/api/library.ts::LibraryVersionConflictError` and `::KnowledgeRecordConflictError` are both `extends ApiError`, each with its own `is*Conflict()` type guard and a dedicated `*ConflictErrorFromResponse`/`*ConflictErrorFromApiError` function that re-parses the raw 409 body against a **generated** Zod schema (`LibraryConflictError`/`KnowledgeConflictError`, both auto-generated from `contracts/components/schemas/*ConflictError.yaml` into `src/lib/api/generated/schemas.ts`) and exposes the extra fields (`expectedVersion`, `actualVersion`, `path`) as typed properties. The spec's contract table for `MaxToolIterationsLoweringConflict` is correctly schema-first (a real `.yaml` file, referenced from `openapi.yaml`'s 409 response — the Zod schema generation is automatic, same pipeline). But nowhere in "Contract Changes," "UI Screens and States," "Accessibility and Keyboard," or "Integration Boundaries → Gateway ↔ SPA" does the spec name the required `MaxToolIterationsLoweringConflictError`-style class, its type guard, or that `PerformanceSection`'s confirm-save code must call it — the exact three things F1's fix bothered to spell out for its own (structurally simpler) problem.

**Recommendation**: add one bullet under "Integration Boundaries → Gateway ↔ SPA" or "Contract Changes" naming the pattern explicitly: a `MaxToolIterationsLoweringConflictError extends ApiError` (mirroring `LibraryVersionConflictError`), an `isMaxToolIterationsLoweringConflict()` guard, and a re-parse function using the generated Zod schema for `MaxToolIterationsLoweringConflict`; the confirm-save code branches on the guard to reload the dialog from `.preview`, falling back to the generic error path for a 409 that doesn't match (mirroring the existing "unexpected shape... still surfaces as a real 409 ApiError" fallback rule in `library.ts`).

### G4 — MINOR — "same ids, same old values" for `confirmed_lowering` does not say whether array order matters (Lens 1)

**Failure scenario**: the SPA and the server independently order the confirmed-agents array differently (e.g. SPA preserves display order, server iterates a Go map), and an implementer represents the "equals `confirmed_lowering` exactly" check in `contracts/components/schemas/PerformanceSettingsUpdate.yaml`'s description as a literal slice-order comparison, causing spurious 409s on a legitimate, unchanged confirmation purely because of iteration-order non-determinism (Go map iteration is explicitly randomized).

**Evidence**: `PerformanceSettingsUpdate` row: *"the PUT succeeds only if that set equals `confirmed_lowering` exactly (same ids, same old values)"* — the word "set" implies order-independence, but "exactly" and the array wire type could as easily be read as ordered-sequence equality by an implementer skimming for the bound check.

**Recommendation**: state explicitly "compared as a set — keyed by `agent_id`, order-independent" in the same sentence, and add one dataset row (or extend test 13a) with the preview and the confirm bodies in deliberately different orders, asserting success.

### G5 — MINOR — Functional Requirements list FR-019/FR-020 before FR-018, breaking numeric scan order (Lens 1)

**Failure scenario**: qa-lead or a reviewer scanning the Functional Requirements section top-to-bottom for "did every FR get a test" stops at FR-020 assuming the list is exhausted and misses FR-018 sitting after it — a low-probability but real path to an FR silently getting no scenario check (the Traceability Matrix does list FR-018 correctly, so this is presentation-only, not a coverage gap today).

**Evidence**: current order in "Functional Requirements": `...FR-017, FR-019, FR-020, FR-018` (verified: `grep -n '^\- \*\*FR-0' docs/internal/specs/tool-iteration-limit-spec.md` shows FR-018 as the last line in the section, after FR-020).

**Recommendation**: move FR-018 to sit between FR-017 and FR-019, or renumber the two new requirements FR-018a/b — either is a one-line edit.

## Verification of round-1 findings F1–F5

| ID | Round-1 finding | Status this round | Evidence |
|---|---|---|---|
| F1 | Nullable/omitted trap on `AgentUpdateRequest.max_tool_iterations` | **Partially resolved** — see G2. REST path genuinely fixed (windowPeek named explicitly, RED-test guidance added: raw JSON body, no marshalled struct). System-agent path claimed fixed but verified false against `pkg/sysagent/tools/agent_apply_args.go::jsonInt`. | Verified |
| F2 | D11 preview/confirm race, no snapshot in the PUT | **Resolved in principle** (D16, `confirmed_lowering`, pre-check + authoritative recheck under `configMu`) but see G1 — two other spec sections were never reconciled with the new model. | Verified |
| F3 | Ambiguous single- vs. multi-agent WARN line | **Resolved.** D19 + updated scenario (agents A/B/C) + FR-009 wording ("exactly one startup WARN line... names every capped agent") + test row 13 updated. | Verified |
| F4 | D11 toast/inline-summary accessibility unspecified | **Resolved and verified against code.** Spec now cites `src/components/ui/toast-container.tsx` (`role={toast.variant === 'error' ? 'alert' : 'status'}`, confirmed) and `src/store/ui.ts::addToast`'s `duration` override (confirmed: `const duration = toast.duration ?? 4000`), plus a persistent inline `role="status"` summary. | Verified |
| F5 | Assumption stated as "confirm in GREEN" was already true | **Resolved** — moved into Clarifications as a verified fact citing the same evidence round 1 found (`AgentsConfig.List` `json:"-"`). Not a defect either round. | Verified |

## Questions for the founder

**None from this round.** All three MAJOR findings (G1–G3) are spec-authoring/implementation-detail gaps with a single correct answer already implied by an existing final decision (G1: D16's "nothing is saved" already dictates the reconciliation) or an existing codebase precedent (G2: mirror the REST fix; G3: mirror `LibraryVersionConflictError`) — none require a founder choice among real alternatives. Team-lead does not need to schedule a founder interview before the fix round on this spec's account; the fix can proceed straight to the spec author.

## Next action

Verdict: REVISE

Review written to: `docs/internal/specs/tool-iteration-limit-spec-review-round2.md`

This was grill round 2 of 2 (fixed, final). Next: team-lead confirms with the founder that no interview is needed this round (see "Questions for the founder" above — none), then the spec author fixes round-2 findings G1–G5. Any of G1–G3 left unresolved after that fix is a CRITICAL-adjacent gap for the D11 write path and the D9 chat-clear path respectively and should be escalated to the founder rather than shipped ambiguous, per the fixed-two-round process (no round 3 runs regardless). Once resolved, team-lead plans the implementation (RED/GREEN/CHECK, the 8-reviewer gate).
