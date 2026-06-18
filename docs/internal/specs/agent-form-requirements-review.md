# Review — `agent-form-requirements.md`

**Reviewer:** grill-spec (read-only)
**Date:** 2026-06-18
**Spec:** `docs/internal/specs/agent-form-requirements.md` (971 lines)
**Companion:** `docs/internal/specs/agent-config-matrix-spec.md`
**Mode:** `structured-spec` (formal requirement matrix + BDD-style acceptance tables; no `FR-xxx`/`SC-xxx` IDs or `## Traceability Matrix` section, so not full plan-spec)

---

## Executive Summary

The spec is detailed, internally consistent on its central design choices, and grounded in real code (`AVATAR_COLORS_BY_NAME`, `avatarColorName()`, `SessionPanel.tsx` references, etc.). However, the proposed wire contract change **silently alters existing locked-core behaviour for built-in agents**, drops `tool_feedback` from the public contract, and changes the `type` enum values mid-release-cycle on `hotfix/v0.1.1` without a concrete rollback procedure beyond "restore DB rows from a backup." There are also several **ambiguities** (description "required" semantics, dropped-vs-rejected behaviour for hidden fields, voice dropdown source of truth) and **missing acceptance scenarios** for built-in edit, conflict between current `Core / Custom / System / Worker` enum and the proposed `core / system / Main / Subagent / subagent_3p`, and an unverified claim about the `Subagent (External)` CLI invocation path against Codex.

**Verdict: BLOCK** (1 CRITICAL, 6 MAJOR findings; the spec must be revised before implementation).

| Severity | Count |
|---|---|
| CRITICAL | 1 |
| MAJOR | 6 |
| MINOR | 7 |
| OBSERVATION | 4 |
| **Total** | **18** |

---

## Structural Integrity Results

Because the spec is `structured-spec` mode (not full `plan-spec`), structural checks are adapted to the document's own structure:

| Check | Result | Notes |
|---|---|---|
| Stated goals have acceptance criteria | **PARTIAL** | Goals §1.1–§1.5 are each exercised somewhere in §9, but no per-goal cross-reference table. |
| Cross-references consistent | **PASS** | Backlinks to `agent-config-matrix-spec.md §5` resolve. The reference to `phase-1-chat-model-and-errors.md` Q1/Q7 (object-form `[{model, provider}]`) is consistent with `AgentCreateRequest.yaml` line 53–60. |
| Scope boundaries explicit | **PASS** | §1.2 "Non-goals" lists bulk import, templates, per-channel routing UI, full delegation_policy editor — clear out-of-scope items. |
| Success criteria measurable | **PARTIAL** | §9 has measurable tables for wire/backend/frontend, but §9.1 has 7 boolean checklist items rather than per-requirement coverage and §9.4 "Visual regression" lists 5 surfaces with one-line "tests" that are not actually acceptance criteria (no Playwright locator, no assertion). |
| Requirements that reference each other are consistent | **FAIL** | §3 matrix uses `description: O` for built-in Main (row 3) and `ro` for built-in Subagent (row 3) — but the matrix header (§3 convention) does not define whether `ro` includes "server-set via seed" vs "locked after create." §4.3 says `description` is `R` for workers; §9.2 row 12 says `PUT Main with description: ""` is **200 OK** (empty string valid for Main) — this is fine, but row 13 says `PUT Subagent with description: ""` is **400**. The wire schema (§7 diff) does not add `minLength: 1` for `description`; the spec says "required" but doesn't say "non-empty after trim" — implementation is free to interpret. |
| Error/failure scenarios addressed for each requirement | **FAIL** | No failure scenarios for: drop detection (silent drop vs 400), voice provider outage mid-edit, schema-mismatch from concurrent update, agent deletion while the edit slide-over is open, recovery from interrupted autosave (e.g. 200 then 5xx), and the `[×]` on the type chip ("closes wizard" — is unsaved input lost? confirmed with browser dialog?). |
| Dependencies between requirements identified | **PARTIAL** | §8 lists files changed, but does not call out the dependency on `voice-provider-detect.ts` (NEW, listed but not specified) or the runtime consequence that removing `tool_feedback` from the wire requires `pkg/agent/loop.go` to key off the channel rather than the agent. The wire diff says `REMOVE tool_feedback` but `Agent.yaml` line 19 still has `tool_feedback` in `required:` and line 131 still has the property — this is a hidden cross-file dependency the spec author has not addressed. |

---

## Findings

### CRITICAL

**F-01. Wire diff drops `tool_feedback` but does not acknowledge that `Agent.yaml` still requires it in the read schema**
- **Severity:** CRITICAL
- **Lens:** Inconsistency
- **Section:** §7 (Wire schema changes), §10.1 step 5
- **Issue:** §7's diff shows `REMOVE tool_feedback` from `AgentUpdateRequest.yaml`, and §10.1 step 5 says "remove from the wire." But the actual `contracts/components/schemas/Agent.yaml` (lines 19 and 131) **still declares `tool_feedback` as required** in the read-side schema, and `pkg/agent/loop.go` references `tool_feedback` in `agentMap` handling at lines 1972–1975. If the wire change ships without also removing `tool_feedback` from `Agent.yaml` (the read schema) and from `pkg/agent/loop.go`, then:
  - Existing `GET /api/v1/agents/{id}` responses would include a `tool_feedback` field that the spec says "moved to per-channel runtime behavior."
  - The validation contract becomes contradictory: the read schema REQUIRES a field the write schema REJECTS.
  - Downstream consumers (frontend, generated TS types at `src/lib/api/generated/openapi-types.ts:2922`, `src/lib/api/generated/schemas.ts:101,233`) compile against a field that the write path will never accept.
  - `make verify-contracts` would not necessarily flag this (the lint checks for hand-written wire-format structs, not for cross-schema drift).
- **Recommended fix:** Add an explicit step to §10.1 and §8.1: "Remove `tool_feedback` from `Agent.yaml` (lines 19, 131), remove from `openapi.yaml` `Agent` schema, remove `tool_feedback` from `src/test/contract.test.ts` lines 73 and 99, and remove the per-agent lookup from `pkg/agent/loop.go`. Replace with a per-channel routing check (`msg.Channel == "webchat"`). Update §7 diff to show the `Agent.yaml` deletion in addition to `AgentUpdateRequest.yaml`." List this as a precondition in §9.1: " `tool_feedback` is removed from `Agent.yaml` and `openapi.yaml` `Agent` schema."

---

### MAJOR

**F-02. `description` "required" semantics undefined — empty vs missing vs whitespace**
- **Severity:** MAJOR
- **Lens:** Ambiguity
- **Section:** §3 row 3, §4.3, §9.2 row 13
- **Issue:** §4.3 says `description` is **required for workers** ("Without it, the worker is unroutable"). §9.2 row 13 says `PUT /api/v1/agents/{id} on Subagent, description: ""` returns **400**. But §3 row 3 only marks the column `R`. The wire schema diff (§7) does NOT add `minLength: 1` to `description` — and the current `AgentCreateRequest.yaml` has no `minLength` on `description` either. Two competent implementers would:
  - (a) add `minLength: 1` (rejects empty AND whitespace-only if trimmed);
  - (b) leave the schema as-is and rely on a backend validator that rejects empty strings on the create path only, not on update;
  - (c) treat `null` and missing as "no description" (current behaviour for Main).
  The acceptance test asserts the rejection, but the implementation strategy is unconstrained. Also, "required for workers" on CREATE is implicit (§3 row 3 = `R`), but on UPDATE the matrix §3 has no column for UPDATE-only constraints; the spec only proves PUT behaviour in §9.2. There's no test that creating a `Subagent` without `description` returns 400.
- **Recommended fix:** (1) Add `description: { minLength: 1, description: "Non-empty. Required for Subagent and subagent_3p; optional for Main." }` to the `AgentCreateRequest.yaml` diff in §7. (2) Add an acceptance test for `POST /agents type: Subagent, no description` → **400** (currently only the PUT case is tested). (3) Explicitly state: "An empty string after trim is rejected as `minLength` violation; whitespace-only is also rejected."

**F-03. "Server drops `tools_cfg` silently on Subagent (External) PUT" — choose silent-drop OR 400; the spec contradicts itself**
- **Severity:** MAJOR
- **Lens:** Inconsistency
- **Section:** §3.3 row 14, §4.11, §9.2 rows 7–8
- **Issue:** §4.11 says: "the backend **silently drops it** on Subagent (External) PUTs." §9.2 row 7 confirms: "**200** but server drops `tools_cfg` silently." Row 8 same for `sandbox_profile`. But §9.1 acceptance criteria state "Wire contract" changes are the schema, and the schema is what the contract test verifies. If the backend silently drops fields, the schema doesn't enforce this — the schema must accept (or reject) the field. Currently `AgentUpdateRequest.yaml` has `additionalProperties: false` (line 9), so unknown properties are rejected. If `tools_cfg` is in the schema (it is, line 154) and the backend silently drops it for External, then the GET response shape will *not* include `tools_cfg` for External agents, but the schema says it's always available. This is fine *if* the GET response really omits it, but the spec does not say what the GET response looks like — only that PUT silently drops. The acceptance test "200 but server drops `tools_cfg` silently" has no follow-up assertion that the subsequent GET also does not contain `tools_cfg`. Worse, "silent drop" is a dangerous default — the operator UI shows the field was sent, the server swallowed it, and the user gets no error feedback. This is a classic source of "I set this and it doesn't work" bug reports.
- **Recommended fix:** Pick one of two strategies and remove the contradiction:
  - **Reject (preferred):** `400` on `PUT /api/v1/agents/{id}` of a Subagent (External) with `tools_cfg` / `sandbox_profile` / `skills` / `fallback_models` / `model_params` / `delegation_policy` set. Cleaner contract, harder to misuse, surfaces UI bugs early.
  - **Silently drop + warn:** Add a `warning` field to the response (the spec already declares `warning` is a runtime/status field at §3.2). For PUT on External, return `200` with `warning: "tools_cfg is not applicable to Subagent (External) and was ignored."` Add an acceptance test asserting the warning is present.
  The current spec's "200 silently drops" pattern (no warning, no error) is the worst of both worlds. Decide and amend §4.11, §4.14, §4.18, §9.2 rows 7–8.

**F-04. The `type` enum migration contradicts the spec's own rationale**
- **Severity:** MAJOR
- **Lens:** Inconsistency
- **Section:** §7 (wire diff), §10.1 step 2, §10.2 rollback
- **Issue:** §7's wire diff replaces the `Agent.yaml` enum:
  ```
  -    - core
  -    - custom
  -    - system
  -    - worker
  +    - core       # locked core Main
  +    - system     # reserved; not user-creatable
  +    - Main       # user-creatable chat colleague
  +    - Subagent   # user-creatable worker on native
  +    - subagent_3p  # user-creatable worker on external CLI
  ```
  But the spec rationale at §2 says there is **no** user-creatable equivalent of the old `core` or `system` — only `Main` (user-creatable chat colleague), `Subagent` (user-creatable native worker), and `subagent_3p` (user-creatable external worker). The diff retains `core` and `system` because the locked roster (`Mia` / `Jim` / `Ava` / `Ray`) is shipped as `type: core`. §9.2 row 14 confirms this: `GET /api/v1/agents/{id}` on a built-in Main returns `type: Main` (not `core`) with `locked: true` indicating core lifecycle. **But the diff shows `core` remains in the enum AND `Main` is added — so the GET would return either `core` (old) or `Main` (new) for the same logical entity (a built-in). The spec claims the GET returns `type: Main` for built-ins, but the diff does not add a migration path from `type: core` to `type: Main` for built-ins.** §10.1 step 3 covers DB migration only for `custom` / `worker`; built-in agents live in code (compiled-in prompts per `pkg/coreagent/core.go:24-150`), so their `type` literal is `core` in `Agent.yaml`. The migration is missing.
- **Recommended fix:** Either:
  - (a) Add a clear statement: "Built-in agents keep `type: core` in the wire enum. The `Main` value is only for user-created agents. `GET /agents/{id}` returns `type: core, locked: true` for built-ins, NOT `type: Main`." Update §9.2 row 14 to match. Drop the contradictory claim that built-ins return `type: Main`.
  - (b) Add a migration that rewrites built-in agents to `type: Main, locked: true` (semantically equivalent, breaks downstream consumers that filter on `type === 'core'`).
  The spec must pick one. Recommend (a) — `core` is a clear signal of "this came from the seed config, treat differently," while `locked: true` is the property-based version.

**F-05. Rollback plan is not actually a rollback — it relies on "restore from backup" with no specific procedure**
- **Severity:** MAJOR
- **Lens:** Inoperability
- **Section:** §10.2
- **Issue:** §10.2: "Re-deploy the previous binary; the new `type` strings (`Main` / `Subagent` / `subagent_3p`) are unknown to the old binary and would surface as `type: 'Unknown'` in the UI. Fix: also restore the DB rows from a backup, or write a one-shot downgrade migration `Main → custom`, etc."
  - No mention of which backup. The data dir `~/.omnipus/` (per CLAUDE.md) has no documented backup procedure.
  - "Write a one-shot downgrade migration `Main → custom`, etc." — the spec acknowledges this is needed but doesn't ship it. A semver-major migration without a shippable downgrade script violates the operating principle of reversible deployment.
  - No RPO/RTO target. "Restore from backup" of a JSONL-based store (sessions/memory/agents all day-partitioned) is non-trivial — there's no `pg_dump` equivalent for the file store.
  - No mention of what happens to in-flight delegations when the gateway is downgraded. A Subagent (External) agent created under the new schema cannot be dispatched by the old gateway, but will the old gateway error cleanly or silently fail?
- **Recommended fix:** Add a "Downgrade migration" subsection under §10.2:
  ```
  10.2.1 Downgrade migration script
  Ship deploy/migrations/v0.1.1-downgrade.sh that:
  1. Iterates ~/.omnipus/agents/<id>/AGENT.md and config.json
  2. Rewrites type: "Main" → "custom"
  3. Rewrites type: "Subagent" → "worker" (and sets executor.kind = native if absent)
  4. Rewrites type: "subagent_3p" → "worker" (and sets executor.kind = external-cli + cli from cli_path lookup)
  5. Strips executor.cli_path, executor.env_overrides, executor.cli_args (don't exist on old schema)
  6. Removes soul from agents (since old schema had soul as optional/draft; new schema requires)
  ...
  ```
  Also: document the RPO (one JSONL rotation cycle, typically one day) and the verification step ("After downgrade, run `omnipus agents list` and confirm zero agents show `type: Unknown`").

**F-06. Acceptance criteria are not BDD scenarios — they are implementation hints**
- **Severity:** MAJOR
- **Lens:** Incompleteness (testing)
- **Section:** §9.3 (Frontend behavior), §9.4 (Visual regression)
- **Issue:** §9.3 has 19 rows like "On `/agents`, three `+ Add` buttons visible | Main, Subagent, Subagent (External)". This is a checklist, not BDD. There's no `Given / When / Then`, no Playwright locator, no assertion. A tester reading this has to invent the test. §9.4's 5 rows are worse — "Renders 3 `+ Add` buttons + agents list + collapsible built-ins section" is an English description of what the page should look like, not a test. The companion `agent-config-matrix-spec.md` §9 ("Test coverage requirements") is marginally better but also not BDD.
  Also missing entirely:
  - Test that the autosave indicator changes color on error (spec mentions this at §6.1 but has no acceptance row).
  - Test for the new `voice-provider-detect.ts` — when the provider changes from enum to no-enum (or vice versa), the widget re-renders. Edge case mentioned at §11 #7 but not in §9.
  - Test that the `[×]` on the type chip confirms before discarding unsaved input.
  - Test for the 3 "+ Add (External)" sub-pickers being **disabled** when their respective CLI is not installed on the host (the spec implies it at §5.1 but doesn't say "or greyed out with tooltip").
  - Test that the Create wizard's "Skip" button (§5.2) is reachable vs hidden on step 3 (Skipping Tools on a `Subagent` worker — should be blocked because `soul` is R, but the spec doesn't say "Skip is disabled until soul is non-empty").
- **Recommended fix:** Reformat §9.3 and §9.4 as BDD scenarios in Given/When/Then, with explicit Playwright (or Vitest+@testing-library) locators. Add the missing scenarios above. A tester should be able to copy-paste each row into a `.spec.ts` file with no further interpretation.

**F-07. Spec claims `voice-provider-detect.ts` is "NEW" but does not specify its contract, error states, or polling semantics**
- **Severity:** MAJOR
- **Lens:** Incompleteness
- **Section:** §4.10, §8.3 (file list), §11 #7
- **Issue:** §8.3 lists `src/lib/agents/voice-provider-detect.ts` as **NEW** with "runtime check for voice provider's voices enum." §4.10 builds three UI branches on it (dropdown / free-text / disabled). §11 #7 raises the partial-enum case as "decision tree currently picks free-text — confirm with frontend-lead whether a partial-enum provider should render as dropdown (filtering known voices) or free-text." But:
  - No interface / signature is specified.
  - No error states: what if the provider's voices endpoint is slow, returns 500, or returns a non-array?
  - No caching / staleness: is the result fetched once per agent edit, or polled? If the operator changes the global voice provider mid-edit, does the widget re-render?
  - No mention of whether this needs a backend endpoint (the gateway already has voice provider config) or is computed in the SPA. If SPA-computed, the SPA needs to know which provider is configured — gateway config must expose this, and the spec doesn't say so.
- **Recommended fix:** Add a §4.10.1 sub-section specifying the `voice-provider-detect.ts` interface:
  ```ts
  // Proposed
  export type VoiceProviderMode = 'enum' | 'free-text' | 'disabled'
  export interface VoiceProviderDetectResult {
    mode: VoiceProviderMode
    voices?: string[]    // populated when mode === 'enum'
    reason?: string      // populated when mode === 'disabled' (for tooltip)
  }
  export async function detectVoiceProvider(): Promise<VoiceProviderDetectResult>
  ```
  Specify: backend endpoint to call (likely `GET /api/v1/voice/provider`), caching (10s SWR), error fallback (`disabled` with "Voice provider unavailable" tooltip), and behavior on configuration change (subscribe to settings change events).

**F-08. Schema diff for `AgentCreateRequest.yaml` shows `default: Main` but current contract has `default: custom`**
- **Severity:** MAJOR
- **Lens:** Inconsistency
- **Section:** §7 (wire diff)
- **Issue:** §7's diff:
  ```diff
   type:
     enum:
  -      - custom
  -      - worker
  +      - Main
  +      - Subagent
  +      - subagent_3p
     default: Main
  ```
  But the current `AgentCreateRequest.yaml` line 28 has `default: "custom"`. The diff shows `default: Main` in the context, but this is the diff hunk display — the actual change is the enum replacement; `default` was `custom` and is changing to `Main`. This is fine **but** combined with the wire contract rule from `CLAUDE.md` Constraint #8 ("Wire types are generated from `contracts/openapi.yaml`; generated types in `pkg/api/generated/` and `src/lib/api/generated/` are the only legal cross-boundary types"), the spec must explicitly run `make gen-contracts` and commit the regenerated artifacts. §8.4 says this, but the acceptance criteria (§9.1) only check `make verify-contracts` exits 0, not that the generated diff is intentional and committed atomically. There's no acceptance test that asserts the generated `openapi_types.gen.go` contains the new enum values.
- **Recommended fix:** Add to §9.1: "`grep -r 'subagent_3p' pkg/api/generated/` returns >= 1 hit (proves the generated Go contains the new value)." Add to §8.4: "The PR MUST commit the regenerated `pkg/api/generated/` and `src/lib/api/generated/` artifacts atomically with the schema changes (CLAUDE.md Constraint #8, 5-step process)."

---

### MINOR

**F-09. `description` rationale at §4.3 uses "Main" without restating the type-of-type**
- **Severity:** MINOR
- **Lens:** Ambiguity
- **Section:** §4.3
- **Issue:** "For **Main**, the description is a human-readable subtitle on the agent card (optional)." Reads naturally but a future reader might confuse "Main" (the type) with "main" (general English). Same issue at §4.9, §4.10, §4.21 ("Main only"), §4.22, §4.24.
- **Recommended fix:** When the prose is standalone, prefer the exact wire enum value: "`type: Main`" (monospace) rather than bare "Main". Or document the disambiguation in §3 convention block.

**F-10. `executor.cli` "Locked after create" lacks the wire-level enforcement**
- **Severity:** MINOR
- **Lens:** Incompleteness
- **Section:** §4.16
- **Issue:** §4.16: "**Locked after create** (to switch CLIs, the user must create a new agent)." But:
  - The wire schema does not specify immutability — `ExecutorConfig.cli` is an enum with no `readOnly: true` flag in the current or proposed schema.
  - The backend logic is implied but not specified. §8.2 says "validate `cli` + `cli_path` required when `type: subagent_3p`" but doesn't say "reject changes to `cli` on PUT."
  - Acceptance criteria §9.2 has 12 rows; none of them test "PUT on a Subagent (External) with a different `cli` value → 403."
- **Recommended fix:** Add an acceptance test to §9.2: `PUT /api/v1/agents/{id} on Subagent (External) created with cli=claude-code, body {executor: {kind: external-cli, cli: codex}}` → **400** or **403** with message `"executor.cli is immutable after create; create a new agent to use a different CLI."`

**F-11. Spec references `src/components/agents/AgentFormFields.tsx` and `CreateAgentModal.tsx` in §8.3 but the actual file names need verification**
- **Severity:** MINOR
- **Lens:** Inconsistency
- **Section:** §8.3
- **Issue:** §8.3 lists `CreateAgentModal.tsx` twice (under "replace with type-aware wizard" and "update voice field to dynamic widget behavior"). The current file is 573 lines and imports `getCreateAgentFormCopy` from `AgentFormFields.tsx`. The spec implies the wizard structure changes drastically (3 numbered steps + Advanced for 3 types) — that's a near-rewrite, not a "replace." Should be split into separate `CreateAgentWizard.tsx` and `CreateAgentStep1Identity.tsx` / `Step2Personality.tsx` / `Step3Tools.tsx`. The spec doesn't propose this; the implementer will have to invent it.
- **Recommended fix:** Either (a) propose the new file structure in §8.3, or (b) note that the implementer may split the file at their discretion.

**F-12. "Auto-save on edit" 500ms debounce — no specification of what happens on network failure**
- **Severity:** MINOR
- **Lens:** Incompleteness
- **Section:** §6.1, §1 goal #4
- **Issue:** §6.1: "every change auto-commits via the `useAutoSave` hook (500ms debounce)." §1 goal #4: same. But:
  - No mention of retry behavior (3 retries with exponential backoff? or single-shot fail-then-red-pulse?).
  - No mention of offline behavior (what if the user closes the tab mid-debounce? the PUT is dropped?).
  - The "Last saved Xs ago ●" indicator "turns red on error" but the recovery path is not specified (does the next keystroke retry? does the user have to re-focus? does the indicator stay red forever?).
- **Recommended fix:** Add a §6.1.1 subsection: "Autosave semantics: 500ms debounce; PUT on every flush. On 5xx or network error, the indicator turns red and the next successful flush turns it green (no manual retry needed). On 4xx (validation error), the indicator turns red AND the field with the invalid input is highlighted inline; autosave is paused for that field until the input is corrected. On unmount mid-debounce, the in-flight change is committed (via `navigator.sendBeacon` or `fetch keepalive`)."

**F-13. `fallback_models` maxItems change is described as part of the schema but no test of the boundary**
- **Severity:** MINOR
- **Lens:** Incompleteness
- **Section:** §3.1 row 16, §9.2 (implicit)
- **Issue:** §4.13 says `max 2` (was 10). §9.2 has no test for "POST with 3 fallback_models → 400" or "PUT adding a 3rd fallback_model → 400." The change is breaking for existing agents with 3+ entries.
- **Recommended fix:** Add to §9.2: `POST /api/v1/agents type: Main with 3 fallback_models` → **400** `fallback_models exceeds maxItems: 2`. Also: migration rule for existing agents with > 2 fallbacks — truncate to 2 on load? Reject the load? The spec doesn't say.

**F-14. Built-in Voice field "shown but disabled" — implementation ambiguity**
- **Severity:** MINOR
- **Lens:** Ambiguity
- **Section:** §6.5, §11 #2
- **Issue:** §11 #2: "Shown but disabled for built-ins. The widget follows the global voice-provider detection... Built-ins cannot edit." §6.5 shows it in the wireframe with `(disabled — built-in)` annotation. But:
  - "Shown but disabled" — what does the user see? A greyed-out dropdown showing "alloy"? A greyed-out text input?
  - If the operator wants to set a voice on the global default (Mia), how? The spec says they can't edit Mia's voice, but the rationale at §4.10 says voice is for TTS chat output — Mia is the chat default. This is a tension the spec doesn't address.
- **Recommended fix:** Either:
  - (a) Make voice editable on built-in Main agents (it's a runtime TTS config, not identity — same argument as `model` being operator-tunable per §6.5). Update §6.5 operator-tunable subset to include `voice`.
  - (b) Confirm built-in Voice is locked and add: "To change Mia's voice, change the global voice provider's default voice in Settings → Voice (Omnipus picks that up as the built-in default)."
  Pick one; current spec is ambiguous.

**F-15. Roster grid line in §13.2 cites `AgentListScreen.tsx:61,133,182` but file may not exist at that path**
- **Severity:** MINOR
- **Lens:** Incorrectness
- **Section:** §13.2
- **Issue:** §13.2 cites `AgentListScreen.tsx:61,133,182` for the roster grid. The actual file in `src/components/agents/` is `AgentCard.tsx`, not `AgentListScreen.tsx`. The agents route is `src/routes/_app/agents.tsx`. The line citations may be stale or refer to a path that doesn't exist.
- **Recommended fix:** Verify the file paths before publishing. If `AgentListScreen.tsx` exists, cite the correct route/component. If it doesn't, update to `src/routes/_app/agents.tsx` or `src/components/agents/AgentCard.tsx`.

---

### OBSERVATIONS

**F-16. Wire spec uses inconsistent casing for type values across the doc**
- **Severity:** OBSERVATION
- **Lens:** Style
- **Section:** Throughout (§2, §3, §4, §5, §7, §9, §13)
- **Issue:** `Main` and `Subagent` are PascalCase (matches JSON convention), `subagent_3p` is snake_case. The spec mostly uses display form (`Subagent (External)`) in prose and wire form (`subagent_3p`) in code blocks. This is fine for human reading but a JS/TS linter or generated TS type would emit `type: "Main" | "Subagent" | "subagent_3p"` — three different casings in one union. Consider whether `external-subagent` or `subagent_external` would be more consistent (snake_case throughout, or PascalCase throughout). Not a blocker; flag for the spec author.
- **Recommended fix:** Pick a casing convention for all wire enum values (recommend snake_case throughout to match the rest of the codebase's identifiers, e.g. `one-at-a-time`, `queue-and-process`, `claude-code`, `subagent_3p`).

**F-17. Section 13 (Mobile layout) is unusually long and may belong in a separate spec**
- **Severity:** OBSERVATION
- **Lens:** Overcomplexity
- **Section:** §13 (250+ lines)
- **Issue:** §13 is ~27% of the entire spec and is grounded in real app conventions. It's high-quality, but inlining mobile layout into the same spec as wire contract and backend behaviour makes the doc hard to navigate. The companion `agent-config-matrix-spec.md` doesn't have a mobile section at all.
- **Recommended fix:** Consider extracting §13 to `docs/internal/specs/agent-form-mobile-layout-spec.md` and link from this spec. Improves focus; allows mobile-specific review/iteration without touching the data model spec.

**F-18. Built-in roster disclosure state differs across §5.1 (expanded) and §13.2 (collapsed on phone)**
- **Severity:** OBSERVATION
- **Lens:** Inconsistency
- **Section:** §5.1 vs §13.2
- **Issue:** §5.1's wireframe shows the built-in roster **expanded** by default (Mia/Jim/Ava/Ray visible). §13.2 says "collapsed by default on phone (default disclosure); expanded on desktop." This is an inconsistency — what's the desktop default in §5.1? Expanded (per wireframe) or collapsed (per mobile disclosure rule)? The two sections disagree.
- **Recommended fix:** State the default disclosure state once, in §5.1: "Built-in roster is expanded by default on desktop, collapsed by default on phone/tablet." Drop the §13.2 mention (or reverse — drop the §5.1 wireframe's "expanded" appearance and replace with "▾" disclosure).

---

## STRIDE Threat Summary

| Component | Threat | Mitigation in spec | Adequate? |
|---|---|---|---|
| `POST /api/v1/agents` | **Tampering**: client sends `type: Main` + `executor.kind: external-cli` to confuse dispatcher | §9.2 row 5: server overrides `executor.kind` to `native` for Main | Yes |
| `POST /api/v1/agents` with `subagent_3p` | **Spoofing**: client sends `cli: claude-code` but `cli_path: /tmp/malicious_binary` | §9.2 row 4 requires `cli_path`; no validation that the binary is what it claims to be | **NO** — spec doesn't say "server verifies `cli_path` is executable and matches the named CLI." A worker could be created pointing at `/tmp/evil`, and the gateway would spawn it on delegation. Recommend adding a startup check or runtime validation. |
| `executor.cli_args` | **Injection**: client sends `; rm -rf /` | §4.19: "warns on shell-injection chars... but does not reject" | **NO** — warns without rejecting is a foot-gun. Either reject with 400 or document that the spawn layer uses execve (no shell interpolation). |
| `executor.env_overrides` | **Elevation of Privilege**: client injects `OMNIPUS_BYPASS_AUTH=1` | §4.18: "merged into spawned process's env alongside Omnipus's own" — does NOT say "Omnipus's env vars take precedence and cannot be overridden" | **NO** — if the spawn layer lets `env_overrides` override Omnipus's own env (e.g. `OMNIPUS_MASTER_KEY`), this is a critical bypass. Recommend: "Omnipus-internal env vars (matching `OMNIPUS_*` and the master key vars) are not overridable; user-supplied vars take precedence only for non-Omnipus keys." |
| `delegation_policy.to[]` | **Elevation of Privilege**: Main agent delegates to a worker it shouldn't | §4.21: `to[]` is ENFORCED | Yes |
| `delegation_policy.accept_from`, `budget` | **Repudiation**: orchestrator claims it didn't accept a delegation but `accept_from` would have permitted it | §4.21: "schema present, NOT enforced in v0.1.0" with startup WARN | Acceptable for v0.1.0, but spec must ensure the WARN is actually emitted and surfaced in `/health` or operator UI. |
| Auto-save on edit slide-over | **Tampering**: user edits Mia's `model` field (should be operator-tunable per §6.5) but the gateway accepts `description` too | §6.5 lists the operator-tunable subset; backend rejects non-tunable fields on locked agents | Acceptable if backend enforces; spec doesn't show the rejection test. Add §9.2 row. |
| Subagent (External) CLI invocation | **Denial of Service**: long-running CLI never returns | §4.23 `timeout_seconds` bounds Omnipus wait | Yes |
| Built-in Voice field "shown but disabled" | **Repudiation**: operator claims they configured Mia's voice but it didn't take | §11 #2 / §6.5 | Ambiguous (see F-14) |
| Mobile `[×]` on type chip | **Tampering**: user cancels wizard mid-edit, input lost | §11 #3: "single behaviour across all three types" — same as Cancel. But does it confirm before discarding? | Not specified. Recommend browser confirm dialog if any input is dirty. |

---

## Test Coverage Assessment

| Aspect | Status | Notes |
|---|---|---|
| Wire schema | Partial | `make verify-contracts` checked (§9.1) but no per-field generated-type assertion (F-08). |
| Backend happy path | Adequate | §9.2 covers Main, Subagent, External create with each required-field set. |
| Backend error path | Partial | §9.2 covers 400s for missing required fields, but no 4xx tests for type mismatches, invalid color hex, invalid icon length, fallback_models > 2 (F-13), `cli` immutability on PUT (F-10), or `delegation_policy.accept_from` being non-empty (the WARN should fire). |
| Backend derived field override | Adequate | §9.2 row 5, 6 cover executor.kind and steering_mode overrides. |
| Backend silent drop | Partial | §9.2 rows 7–8 cover silent drop of `tools_cfg` / `sandbox_profile` for External, but no assertion that the GET response shape matches (F-03). |
| Frontend component behavior | Incomplete | §9.3 is a checklist, not BDD (F-06). Missing tests: autosave error path (F-12), Voice widget edge cases, type-chip cancel confirmation, dynamic widget re-render on provider change (F-07). |
| Visual regression | Incomplete | §9.4 lists surfaces, not assertions (F-06). |
| Mobile-specific | Adequate | §13.7 has 14 measurable tests in tabular form. |
| Concurrency / ordering | Missing | No tests for: concurrent edit of the same agent from two tabs, concurrent delete + edit, in-flight delegation when the agent is deleted, autosave racing with explicit Save (there is no Save, but debounce-then-delete race?). |
| Idempotency | Missing | No test for "PUT the same body twice → second call is a no-op." |
| Migration | Missing | No tests for: the DB migration script (§10.1 step 3) actually running on a fixture with `type: "custom"` and `type: "worker"` agents; the downgrade script (F-05). |

---

## Unasked Questions

1. **What happens if the user clicks `[×]` on the type chip in the wizard while fields are dirty?** §11 #3 says it closes the wizard "the same as Cancel" — but unsaved input is discarded silently? With a confirm dialog?
2. **What happens if a Subagent (External) agent's `cli_path` points at a binary that isn't installed?** Runtime error at first delegation? Startup check? Tooltip on the form?
3. **What happens if the operator changes the voice provider from enum to no-enum while an edit slide-over is open?** Does the widget re-render? Does the existing value get clobbered? (F-07 gap.)
4. **What happens if `description` is whitespace-only ("   ") for a worker?** Is that a 400 (treated as empty) or 200 (non-empty)?
5. **What is the response body when the backend silently drops a field?** (F-03 gap.) Is there a `warning` array in the response? Just HTTP 200 with no indication?
6. **The spec says `fallback_models` was 10, now 2. What happens to existing agents with 3+ entries at load time?** Truncated silently? Error? Migration script?
7. **`Subagent (External)` agents get `executor.cli` locked at create. The spec says "create a new agent to switch CLIs." But the `cli` field exists on the wire — is it visible (disabled) in the edit slide-over's Runtime tab?** §6.4 shows it as "locked" but doesn't confirm whether it appears.
8. **The spec removes `tool_feedback` from the wire and moves it to "per-channel runtime routing." Where does that routing decision live — `pkg/agent/loop.go`? A new `pkg/agent/feedback_router.go`? The spec says replace in loop.go but doesn't say which channel check to use.** (§10.1 step 5, partial.)
9. **The "Default" toggle (row 26) is `O` for Main but the wire schema's `default` field is `boolean` with no enum. What happens if two Main agents have `default: true`?** §9.2 has no test; `pkg/config` is mentioned in CLAUDE.md as repairing multi-default at load, but the spec doesn't reference this.
10. **The `description` field is "required for routing" for workers. Is it surfaced to the orchestrator's delegation prompt verbatim, or wrapped with "Worker X's purpose: <description>"? The spec says "Without it, the worker is unroutable" but doesn't say how it's used.**

---

## Verdict: BLOCK

**Review written to:** `docs/internal/specs/agent-form-requirements-review.md`

### CRITICAL finding
- **F-01:** `tool_feedback` removal is incomplete — `Agent.yaml` still requires it; `pkg/agent/loop.go` still reads it.

### MAJOR findings (must address)
- **F-02:** `description` "required" semantics undefined (empty string handling, CREATE-vs-UPDATE).
- **F-03:** Silent-drop of `tools_cfg` / `sandbox_profile` on External PUT is dangerous — choose 400-reject OR warn-with-200, not silent-drop.
- **F-04:** Migration for built-in `type: core` agents contradicts §9.2 row 14 (which says built-ins return `type: Main`).
- **F-05:** Rollback plan relies on "restore from backup" without a shippable downgrade script.
- **F-06:** §9.3 and §9.4 are checklists, not BDD — rewrite as Given/When/Then with locators.
- **F-07:** `voice-provider-detect.ts` is listed as NEW but has no interface, no error states, no caching semantics.
- **F-08:** Wire diff lacks acceptance test for generated artifacts (`make gen-contracts` output).

### To address these findings, run:
```
/plan-spec --revise docs/internal/specs/agent-form-requirements.md docs/internal/specs/agent-form-requirements-review.md
```