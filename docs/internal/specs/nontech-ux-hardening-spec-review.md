# Grill Review — Non-Technical-User UX Hardening of Configuration Surfaces

**Reviewed spec:** `docs/internal/specs/nontech-ux-hardening-spec.md`
**Review date:** 2026-06-03 (third grill pass — spec already revised twice + carries a live §0 verification log)
**Reviewer mode:** adversarial / read-only
**Detected input mode:** `structured-spec` (US-xxx user stories + AC-numbered acceptance criteria + a TDD matrix + a traceability table, but not full plan-spec FR-/SC-/scenario format)

---

## Executive Summary

This is a mature spec that has already survived two `/grill-spec` passes plus a live as-is verification (§0), and most of its load-bearing technical claims hold up against the running code. I re-verified the headline claims and **the B-1 root cause (`SecuritySection.tsx:156` filters `scope !== 'system'` while `system.*` tools carry `scope:"core"`), the MCP-modal bug (`canSubmit` requires `command`, no URL field, line 55), the WhatsApp 5-state wire enum (`waiting|code|linked|timeout|error`), the SkillBrowser silent-swallow, and the schedule trigger model are all confirmed exactly as written.** The findings below are therefore not "the spec is wrong about the system" — they are residual gaps, a cluster of **incorrect file paths** that will cost implementer time, and a handful of edge/operability holes the prior grills missed.

**Findings:** 0 CRITICAL · 4 MAJOR · 7 MINOR · 4 OBSERVATION
**Verdict: REVISE** — the four MAJOR findings (wrong component paths, the `session_mode` wire-contract retention claim, the unverified read-back source behind the standing badge on three of four controls, and the unverified "cron in gateway local time" timezone claim) should be fixed before `/taskify`, because each one will mislead an implementer who otherwise trusts the spec's precision.

---

## Findings Table

| ID | Severity | Lens | Section | Finding | Recommended fix |
|---|---|---|---|---|---|
| F-G01 | MAJOR | Incorrectness | §6.5 components header; US-E1; US-E4; §7 | **Named component paths are wrong.** Spec implies `McpServerModal.tsx`, `MCPServerPicker.tsx`, `SkillTrustSection.tsx` are Settings components. Verified actual paths: `src/components/skills/McpServerModal.tsx`, `src/components/agents/MCPServerPicker.tsx`, `src/components/settings/SkillTrustSection.tsx`. An implementer grepping the implied `settings/` path for the first two finds nothing. | Correct all three paths. `McpServerModal` is under `skills/`, `MCPServerPicker` under `agents/`, `SkillTrustSection` under `settings/`. |
| F-G02 | MAJOR | Incompleteness | US-E4 ("Mount `SkillTrustSection`") | **`SkillTrustSection.tsx` already exists** (`src/components/settings/`, with a `.test.tsx`) and is **currently imported nowhere** (verified: zero non-self references). E4 is framed as if specifying a new component, but the real task is mounting an existing one — and the spec never confirms the existing component's props/tri-state match the "Block / Warn-unverified / Allow-all" contract it describes. | Reframe US-E4: the component exists. Read it, confirm its existing tri-state + persistence match the spec, and scope the task as "mount into Settings→Security + wire trust-mode persistence," not "build." |
| F-G03 | MAJOR | Incorrectness | line 6; line 314/§9; D18 | **The "no wire-contract change" claim is imprecise and invites a generated-file edit.** D18 removes `session_mode:'main'` from the UI, but the generated zod schema (`schemas.ts:322,359,371`) still types `session_mode` as `"isolated"\|"continue"\|"main"`, and the backend path is only *deprecated* (kept). The spec never states the enum must retain `main`; an implementer "tidying up" could delete it from a generated file — a Hard-Constraint-#8 violation. | Add an explicit non-behavior in §9: "the `main` value REMAINS in the generated `session_mode` wire enum; D18 removes only the UI control that emits it. Do not edit `src/lib/api/generated/`." Also handle reverse-parsing a pre-existing `main` schedule on edit (it can no longer be re-offered). |
| F-G04 | MAJOR | Incompleteness | US-B2 AC4/AC5; §4 edge cases | **The standing-badge "read back from persisted config" source is asserted but located for only one of four controls.** `policyMode` is reachable (SecuritySection loads policies). For `auth_mode`, `bind_address`, and sandbox-profile the spec never names the query/field exposing the *saved* (not pending-restart) value to the badge. AC4 ("survives reload, fires on config-import") and AC5 ("clears on save of the safe value, not on restart-pending") are untestable until that source is named — and if any control only exposes a local/pending value, AC4 needs a read that may collide with "no new wire type." | For each of the four controls, name the existing config field/query the badge reads. Flag any control whose saved value isn't already client-readable. |
| F-G05 | MAJOR | Infeasibility | US-A2 timezone (M-1) | **"`cron` and `at` evaluate in the gateway's local time" is asserted without §0 verification** — unlike every other load-bearing claim, which §0 verified live. The preview copy "Next run: … server time" is a lie to the user if `gronx` actually parses in UTC. The `at` ISO string also carries its own offset, so "local time" is ambiguous between cron and at. | Add a §0 verification line for the scheduler timezone, or soften the preview copy until verified. Specify whether the form emits local-ISO or UTC-ISO for `at`. |
| F-G06 | MINOR | Ambiguity | §2.1 "minus system (M-3)" + `CATEGORY_LABELS` | `CATEGORY_LABELS` (`toolCategories.ts:4-16`) contains a `system` key labeled "System". §2.1 says presets apply to "`CATEGORY_LABELS` minus system." Since `groupByCategory` buckets by the tool's `category` and `system.*` tools have `category:"system"`, they form a `system` *group* that the Advanced disclosure claims via `category==='system'`. The prose conflates removing the *label entry* with not rendering the *group* — same key, but readable two ways. | State precisely: the Advanced/system disclosure renders the `groupByCategory` result whose key is exactly `'system'`; the primary grid renders all other keys including `'other'`. |
| F-G07 | MINOR | Insecurity | US-E1 / M-10 SSRF caution | The SSRF "caution-not-block" posture is intentional, but the SPA **cannot resolve a hostname** to detect RFC1918/link-local. "A URL resolving to an internal host gets a caution" implies DNS resolution the browser doesn't do; a hostname that resolves privately at connect-time escapes the SPA check. | Clarify the SPA check is a **literal-IP/hostname heuristic** (`10.`/`192.168.`/`172.16-31.`/`169.254.`/`localhost`/`*.local`), and that the real guard is the backend connect path. |
| F-G08 | MINOR | Insecurity | US-C2 M-13; US-E1 transport switch | The "clear deselected group on switch" rule prevents a *submitted* stale secret (M-13 AC2), but no AC asserts the abandoned field's **state value** is cleared (not merely excluded from payload). A switch-back could resurrect a stale secret still held in `useState`. | Add to the test: after switching method/transport, assert the abandoned field's state value is empty, so switch-back cannot resurrect it. |
| F-G09 | MINOR | Overcomplexity | §2 `<RiskySettingControl>` across 5 sites | The control now wraps 4 Security controls **plus** the stdio MCP gate — 5 consumers of different shapes (toggle/radio/dropdown/saved-list-item). The standing badge is uniform but confirm-copy and safe-value differ per site; risk of one over-general component with 5 prop permutations. The spec itself hedges ("the `<RiskySettingControl>` *pattern*") for the MCP case. | Confirm the component takes copy + safe-value + onConfirm as props (no internal per-site branching). Decide explicitly whether the MCP stdio badge reuses the *component* or only the *pattern*. |
| F-G10 | MINOR | Incompleteness | US-A2 AC3 / M-8 | The client structural check accepts structurally-valid-but-semantically-invalid cron (e.g. `99 99 * * *`), deferring to server 4xx. No AC covers the **stale-preview window**: a novice could see a plausible "Next run" then get a server error. | Add an AC: on Custom input passing the structural check, the preview shows "checking…" (or the structural human-readable) and the server 4xx maps inline without leaving a misleading "Next run." |
| F-G11 | MINOR | Inoperability | US-A2 O-3; H8 | F-13's silent-no-op is mitigated at create-time (warn), but the **runtime audit line** is left as "confirm; if not, file a follow-up (out of sprint)." A schedule whose policy drifts *after* creation gets no create-time warning and (unconfirmed) no runtime trace — a genuine repudiation gap. | Acceptable to defer, but the deferral must be a **tracked issue whose acceptance criterion is the backend audit confirmation**, named in §11 — not prose "if not, file a follow-up." |
| F-G12 | MINOR | Inconsistency | §6.5 E3 deletions vs §10 row 14 | US-E3 deletes `agentToolPresets.ts` (selection-presets, dead code); §10 row 14 references `ToolsAndPermissions.tsx:47-83 POLICY_PRESETS` (policy-presets, *replaced* not deleted). Two "preset" systems share the word; a reader can conflate them. | Add a one-line glossary distinguishing **selection-presets** (deleted, E3) from **policy-presets** (`POLICY_PRESETS`, replaced by `<ToolPolicyEditor>`, D2). |
| F-G13 | OBSERVATION | Overcomplexity | §10 row 14 `oldPresetCompat.test` | Good defensive test, but verify whether the four legacy presets ever persisted a *preset name* in config or only `default_policy`+overrides. If only the latter, there is nothing to migrate and the test asserts a non-event. | Confirm; if no name was persisted, simplify to "arbitrary saved policy loads/saves unchanged." |
| F-G14 | OBSERVATION | Incorrectness | US-D3 sandbox standing badge | Badge fires "when a weakened profile **or any shell-deny exception** is active." A shell-deny pattern usually *strengthens* (shrinks the allow surface) — flagging its presence as "weakened" may be backwards. | Clarify the intended semantics; ensure the badge isn't penalizing a hardening action. |
| F-G15 | OBSERVATION | Incompleteness | §1 / §5 signal absent | `signal` is correctly skipped (absent from descriptor), but CLAUDE.md lists Signal as an in-flight channel. If it lands mid-sprint, its Configure panel ships with none of the C1–C4 helper treatment. | Note in §9 that channels added after this spec inherit the helper-metadata contract by default, or explicitly defer Signal's helper treatment to its own PR. |

---

## Structural Integrity Results (structured-spec mode)

| Check | Result |
|---|---|
| Every goal/US has acceptance criteria | PASS — every US-x has AC-numbered criteria |
| Cross-references consistent (no dangling IDs) | PARTIAL — file paths dangle (F-G01); two "preset" vocabularies share a word (F-G12) |
| Scope boundaries explicit (in/out) | PASS — §1 in/out + §9 explicit non-behaviors are unusually thorough |
| Success criteria measurable | PARTIAL — US-B2 AC4/AC5 reference a persisted read-back source not located for 3 of 4 controls (F-G04) |
| Requirements referencing each other consistent | PASS — §2.1 single-source-of-truth is referenced correctly by B/D/E |
| Error/failure scenarios addressed | PASS for UI; one runtime audit gap deferred (F-G11) |
| Dependencies between requirements identified | PASS — §11 sequencing (shared primitives → A/B/D/E/C) is explicit and correctly ordered |

---

## Test Coverage Assessment

The §10 TDD matrix is strong: every US maps to a named test file + level, B-1 is explicitly tested against the *real* `/tools` payload (not a mock), and rows 10–14 add defensive round-trip/orphan/scheme/back-compat tests. Gaps:

- **Standing-badge persistence source untested** for the three restart-gated controls (F-G04); row 11 presumes the read-back exists.
- **Retained-secret-after-switch untested** (F-G08) — M-13 tests "not submitted," not "not retained."
- **Custom-cron server-rejection-after-passing-structural-check untested** (F-G10) — the stale-preview window is uncovered.
- **No test that the generated `session_mode` enum retains `main`** (F-G03) — a regression there is a silent contract break.
- Holdout scenarios (§13) give good behavioral coverage; H8/H9 exercise the two trickiest decisions (auto-deny warning, isolated-but-shared-memory).

---

## STRIDE Threat Summary

| Surface / flow | Threats considered | Spec coverage |
|---|---|---|
| `helpLink` outbound links (new) | Tampering / Info-disclosure (tabnabbing, `javascript:`, stored-XSS) | **Strong** — M-4: compile-time literal, `^https://` unit assertion, `rel="noopener noreferrer"`. |
| MCP stdio add (spawns local program) | Elevation of Privilege | **Strong** — confirm dialog + standing badge. |
| MCP network URL | SSRF / Info-disclosure | **Partial** — caution-not-block intentional, but the resolution mechanism is overstated (F-G07); real guard is the backend connect path. |
| Channel secret fields on auth-group/transport switch | Info-disclosure (stale secret) | **Partial** — "not submitted" covered; "not retained in state" not asserted (F-G08). |
| Four risky Security controls | EoP / Spoofing (silent weakening) | **Strong intent** — confirm-to-weaken + standing badge; weakened by the unlocated read-back source (F-G04). |
| `system.*` tool exposure | EoP (admin tools granted by mistake) | **Strong** — B-1 fix moves all 41 into a default-deny Advanced disclosure; verified live. |
| Schedule auto-deny | Repudiation (silent no-op) | **Partial** — create-time warn covered; runtime audit deferred/unconfirmed (F-G11). |

---

## Unasked Questions (prompts for the author)

1. For `auth_mode`, `bind_address`, and sandbox-profile, **which existing query/config field exposes the *saved* value** to the standing badge (US-B2 AC4)? If only a local/pending value exists, can AC4 be met without a new read?
2. Does the scheduler's `gronx` evaluate cron in **gateway-local time or UTC**? The "server time" preview is correct for only one.
3. Does the existing `SkillTrustSection.tsx` already implement the tri-state with persistence, or does mounting it require new wiring?
4. Do the four legacy agent presets persist a **preset name** in config, or only resolved policy? (Determines whether `oldPresetCompat.test` tests a real migration.)
5. Is there any **existing schedule already saved with `session_mode:'main'`** that the edit form must reverse-parse gracefully after D18 removes the option?
6. For the MCP SSRF caution: literal-IP/hostname heuristic in the SPA, or a backend probe?

---

## Verdict

**REVISE.**

Review written to: `docs/internal/specs/nontech-ux-hardening-spec-review.md`

Address the four MAJOR findings — **F-G01** (wrong component paths), **F-G02** (`SkillTrustSection` already exists → reframe as mount), **F-G03** (`session_mode` wire-enum must retain `main`), **F-G04** (locate the standing-badge persisted read-back for all four controls) — plus the unverified timezone claim **F-G05**, then re-run:

```
/grill-spec docs/internal/specs/nontech-ux-hardening-spec.md
```

The MINOR/OBSERVATION items can fold into the same revision or be accepted-with-rationale. This spec is close: its empirical §0 verification is exemplary and its security posture on the genuinely new surfaces (`helpLink`, stdio gate) is strong. The residual risk is concentrated in (a) stale file paths that waste implementer time and (b) two claims — "no wire change" and "gateway local time" — asserted without the same live-verification rigor the rest of the spec earned.
