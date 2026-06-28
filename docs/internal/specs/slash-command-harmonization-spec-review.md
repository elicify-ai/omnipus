# Grill Review — Slash-Command Harmonization & Surface-Aware Single Source of Truth

| | |
|---|---|
| **Spec reviewed** | `docs/internal/specs/slash-command-harmonization-spec.md` |
| **Reviewed** | 2026-06-28 |
| **Mode** | plan-spec (full structural + 8-lens) |
| **Grounding** | Re-verified against the real codebase this session (did NOT trust the spec's grounding claims) |

---

## Executive Summary

The spec is well-structured and its **core technical premise is sound and code-verified**: `Request.Channel` does carry the origin (`"webchat"` / `"cli"` / channel-type name), the executor's parse→Lookup→dispatch flow has a clean insertion point for surface gating, aliases already resolve transparently, and the `/skills` contract pattern is real. But re-verification turned up **multiple factual inaccuracies in the spec's description of current state**, two of which are load-bearing for whole user stories, plus several genuine gaps (behavior-preserving claim is false for `/clear`; a third hardcoded SPA list — `HELP_TEXT` — is undocumented; the "mirror /skills exactly" claim is wrong on the one axis that matters).

**Findings:** 0 CRITICAL · 6 MAJOR · 7 MINOR · 4 OBSERVATION.

**Verdict: REVISE.** No security/data-loss criticals, but the spec misdescribes the codebase it's grounded in (the "Replaces" column lists commands that don't exist and omits a command that does), claims behavior preservation it cannot deliver for `/clear`, and under-scopes the SPA deletion. These will mislead implementers and break the traceability the spec relies on. Fixable without re-architecting.

---

## Findings Table

| ID | Sev | Lens | Section | Finding | Fix |
|---|---|---|---|---|---|
| F-1 | MAJOR | Incorrectness | §5 map; US-1; DS-2 | `/session new` and `/new` **do not exist** in the codebase. There is no `/session` command and no `/new` command in `BuiltinDefinitions()` (only: start, help, show, list, use, switch, check, clear, subagents, reload, cancel). They exist **only in the SPA's hardcoded `SLASH_COMMANDS`** (client-side). The spec's "`/new`, `/session new` → `/clear` aliases" mixes a backend rename with SPA-only labels that have no backend definition to alias. | Drop `/new`/`/session new` from the backend alias set. Treat them as SPA client labels being deleted (US-4), not backend aliases (US-1). Remove the `new`→`clear`, `session new`→`clear` rows from DS-2. |
| F-2 | MAJOR | Incorrectness | §5 map; US-3/AS-1; SC-001 | The spec's web set lists `/model` as one of the 5 web commands with `delivery: client`. **`/model` does not exist** anywhere — not in the backend registry, not in `SLASH_COMMANDS`, not in MessageInput. The spec is renaming `/switch`→`/model`, but `/switch` is a **sub-command multiplexer** (`switch model`, `switch channel`), not a top-level `/model` with an arg. Today the model is changed via the header `ModelSelector`, not a slash command. So "the 5 web commands" and SC-001 assert a command that has to be **built**, not renamed — contradicting the §1 "no brand-new commands" non-goal and the §5 "behavior preserved" rule. | Either (a) descope `/model` to "show current model" only (matching `switch model`'s read path) and document that the `[name]` setter is **new behavior**, or (b) explicitly add `/model` as new scope and drop the "no new commands / behavior-preserving" claim for it. Update SC-001's "exactly 5" accordingly. |
| F-3 | MAJOR | Incorrectness | §5; FR-013; §6 H6 | **`/clear` is NOT behavior-preserving across surfaces.** Backend `/clear` (`cmd_clear.go`) calls `rt.ClearHistory()` — it **clears the server-side session history**. SPA `/clear` (`executeSlashCommand`) only calls `setMessages([])` + drops the local query cache — **purely local, server untouched**. The spec marks `/clear` web delivery `client` and FR-013 asserts "each command's effect is unchanged." Same name, **materially different effect** (local-only vs server-truncating). H6 even celebrates this as intended ("same name, surface-appropriate effect") which directly contradicts FR-013. | Acknowledge the divergence explicitly: state that `/clear`'s web effect (local reset) ≠ its CLI/channel effect (server `ClearHistory`). Either reconcile them or carve `/clear` out of FR-013's "effect unchanged" guarantee with a documented rationale. |
| F-4 | MAJOR | Incompleteness | §4 symbols; US-4/AS-4; FR-008; SC-005 | There is a **third** hardcoded SPA source the spec never mentions: `HELP_TEXT` (a multi-line const at `ChatScreen.tsx:958`) rendered client-side by `/help`. The spec deletes `SLASH_COMMANDS` and the MessageInput array but leaves `HELP_TEXT` — so after this change `/help` still shows a hardcoded, now-stale command list that the API is supposed to be the single source of. SC-005 only greps `SLASH_COMMANDS`, so this drift passes the gate undetected. | Add `HELP_TEXT` to the deletion/replacement scope: web `/help` must render from `fetchCommands('web')`, not a hardcoded string. Extend SC-005's grep to assert `HELP_TEXT` (or any hardcoded command list) is gone. |
| F-5 | MAJOR | Incorrectness | §3; §4 (`/commands` handler); FR-006 | "Mirror `GET /api/v1/skills` **exactly**" is wrong on the one axis the feature needs: **`/skills` has NO query parameters** (verified in `openapi.yaml`). `/commands` requires `?surface=`. So this is not a mirror — it's `/skills` + a new query param + surface-filtering logic in the handler (which `listSkills` has none of). The "5 contract touch-points" enumeration is otherwise correct, but the path definition must add a `parameters: [surface]` block that `/skills` doesn't have. | Reword §3/§4 to "follow the `/skills` GET shape, **adding** a `surface` query parameter (enum web|cli|channel, default web)." Confirm an existing query-param GET (e.g. the ones at `openapi.yaml:938`) as the parameter-block reference, not `/skills`. |
| F-6 | MAJOR | Incorrectness / Inconsistency | §5 alias mechanics; §4 (`/switch`); US-1 | The alias claim `/switch`→`/model` is not a clean 1:1 rename. `/switch` is a **multiplexer with sub-commands** `switch model` and `switch channel` (and `switch channel` already emits a "moved to /check channel" redirect). You cannot make `/switch` a flat alias of a flat `/model` — the sub-command surface (`switch channel`) would be orphaned. The spec lumps `/switch` into the top-level "1:1 renames" group but it belongs with the `/show`/`/list`/`/check` multiplexer group. | Move `/switch` into the "verb multiplexer → retained hidden/deprecated" group, not the 1:1-alias group. Define what `switch model` and `switch channel` map to (e.g. `switch model`→`/model`, `switch channel`→`/channels`) and whether they keep working for one release. |
| F-7 | MINOR | Incompleteness | §4; US-5/AS-2; FR-011 | The `/help` surface-filter (FR-011) requires passing the caller surface into the help handler. Verified: `formatHelpMessage(defs []Definition)` takes **no surface**, and the help handler sources defs from `rt.ListDefinitions()` (= `cmdRegistry.Definitions`, a **zero-arg** func with no `req.Channel`). The surface IS available via the handler's `req.Channel`, but the spec's symbol note ("Accept the caller's surface") understates the change: `formatHelpMessage` signature changes AND the handler must stop using `rt.ListDefinitions()` and instead filter `Definitions()` by `surfaceForChannel(req.Channel)`. | Spell out: help handler reads `req.Channel`, maps to surface, filters `reg.Definitions()` by surface + drops aliases/hidden, passes the filtered list (or the surface) to `formatHelpMessage`. Note that `rt.ListDefinitions` (zero-arg) is insufficient and may need a surface-aware variant or to be bypassed. |
| F-8 | MINOR | Ambiguity | §5; US-3/AS-2 | "all 11 canonical commands" is asserted repeatedly but the canonical set after F-1/F-2 corrections is unstable: the map lists clear, help, model, skill, cancel, agents, tasks, skills, channels, status, config = 11, but `/model` (F-2) and the `/new`-derived `/clear` (F-1) are shaky, and `/status` is "bare `/show`" — `/show` with no arg currently prints a usage error (executor.go:70-72), not a status. So "bare `/show` → `/status`" invents new behavior. | Enumerate the canonical 11 in one authoritative list and reconcile each against an existing handler. Define what `/status` actually outputs (it has no current implementation — bare `/show` errors). |
| F-9 | MINOR | Ambiguity | Edge cases; FR-015 vs §6 edge | Self-contradiction on unknown `?surface=`: the §6 edge case says "**400** (or default to web — pick one; spec says default to web)" — it states both then resolves to default-web (FR-015, A6). The parenthetical "400" should be removed so an implementer doesn't ship the wrong branch. | Delete the "400" alternative from the §6 edge bullet; keep only "unknown surface → default web (FR-015)." |
| F-10 | MINOR | Incompleteness | §7 integration; US-4 | `delivery: agent` web path ("insert `/skill ` as text → forward via message frame") relies on the message reaching the **backend executor**, where `/skill` (`use`) is a **handler-less registry entry** → `OutcomePassthrough` (executor.go:60-63). That's correct today, but the spec never states that the agent-delivery commands must remain registry entries that passthrough (if someone later gives `/skill` a backend handler with web in Surfaces, the "forward as text" path would execute server-side instead of reaching the LLM). | Add an invariant: web `delivery:agent` commands MUST resolve to passthrough on the backend (no web-surfaced handler), so forwarded text reaches the model. Add a test asserting `Execute(webchat, "/skill x")` → Passthrough. |
| F-11 | MINOR | Inconsistency | §6 (`availableWhileStreaming`) vs schema | Field-name drift: the SPA uses `availableWhileStreaming` (camelCase); the spec's `SlashCommand` schema field is `available_while_streaming` (snake_case, per wire convention). Both appear in the spec. The generated TS type will be snake_case; the existing component code is camelCase. Not wrong, but the mapping must be called out or the deletion/rewrite will mismatch. | State that the wire field is `available_while_streaming` and the SPA reads it as-is from the generated type (drop the camelCase `availableWhileStreaming` interface along with `SLASH_COMMANDS`). |
| F-12 | MINOR | Overcomplexity | §5; US-3 | `delivery` as an optional-with-implied-default wire field is a known codegen pitfall: OpenAPI response schemas have no `default:`, so "optional but always present for web" becomes undocumented contract debt (TS gets `delivery?: ...`, forcing `?? 'agent'` at call sites). For a 5-row web response where `delivery` is always meaningful, optionality buys nothing. | Make `delivery` **required** in `SlashCommand` (always populated by the handler). Simpler contract, no nullable handling in the SPA dispatch (FR-009). |
| F-13 | MINOR | Ambiguity | §3; naming | Schema name `SlashCommand`: existing schema convention is singular noun (`Skill`, `Agent`, `Task`). `Command` would be idiomatic; `SlashCommand` is acceptable only to disambiguate from another "command" concept. No such collision found. | Either rename to `Command` for convention, or add a one-line schema description justifying `SlashCommand`. Minor, but pick deliberately since the name is permanent in the generated types. |
| F-14 | OBSERVATION | Inoperability | §7 | The `/commands` fetch-error behavior ("palette shows nothing + counter, non-blocking") is good, but the SPA palette is moving from a guaranteed-present hardcoded list to a network-dependent one. On a slow/failed first fetch the user sees an **empty** `/` palette where they previously saw 5 commands. Consider a tiny built-in fallback (clear/cancel) OR an explicit "loading…" state so the palette isn't silently empty. | Add a UX note: empty-palette-on-error is acceptable, but `/cancel` (the streaming-safe command) arguably should have a client fallback so a user can always stop a turn even if `/commands` failed. |
| F-15 | OBSERVATION | Insecurity (STRIDE) | US-3 | `GET /commands` returns only static, non-sensitive command metadata (names/descriptions) behind `withAuth` — low risk. The one note: if `?surface=` is ever extended with `?agent_id=` (§15 future work), that becomes an authz surface (can user X enumerate agent Y's commands?). Out of current scope but flag for the future extension. | No change now; note in §15 that the future `?agent_id=` extension needs an ownership/authz check. |
| F-16 | OBSERVATION | Overcomplexity | §5; A5 | Retaining `/show`/`/list`/`/check` as hidden deprecated multiplexers "for one release" — verify the user pain justifies the maintenance. These are CLI/channel-only (not web). If telemetry/usage is unknown, "one release" of dead-but-maintained code + tests has a cost. Cheap to keep; just confirm it's a real back-compat need, not reflexive caution. | Confirm (or note as assumption) that real users invoke `/show`/`/list`/`/check` today, justifying the deprecation window over a hard rename. |
| F-17 | MINOR | Incompleteness | TDD §9; regression | Test #14 greps for `SLASH_COMMANDS` only. Given F-4 (HELP_TEXT) and F-1 (`/new`,`/session new` live only in the SPA), the grep guard must also catch the MessageInput built-in array and `HELP_TEXT`. As written, test #14 passes while two hardcoded sources survive. | Broaden test #14 / SC-005 to assert removal of: `SLASH_COMMANDS`, the MessageInput built-in suggestion array, and `HELP_TEXT` (or any inline command-list literal in `src/components/chat/`). |

---

## Structural Integrity Results (plan-spec mode)

| Check | Result |
|---|---|
| Every user story has ≥1 acceptance scenario | PASS (US-1..US-5 all have AS) |
| Every acceptance scenario has ≥1 BDD scenario | PASS |
| Every BDD scenario has a `Traces to:` | PASS |
| Every BDD scenario has a corresponding TDD test | PASS (mapped in §9/§12) |
| Every FR appears in the traceability matrix | PASS (FR-001..FR-015 in §12) |
| Every BDD scenario appears in traceability | PARTIAL — matrix maps FR↔US↔BDD↔test but a couple of edge scenarios (web-alias passthrough, hardcoded-lists-gone) are folded into rows rather than listed individually; acceptable |
| Test datasets cover boundary/edge/error | PASS for gating/alias; **GAP**: no dataset row exercises the `/clear` cross-surface effect divergence (F-3) or the empty-palette-on-error path (F-14) |
| Regression impact explicitly addressed | PARTIAL — §9 Regression covers executor passthrough + chat tests, but **omits** `HELP_TEXT` (F-4), the `MessageInput.slash.test.tsx` and `ChatScreen.test.tsx` T15 suites that assert the current palette (verified present — they WILL break), and the `/switch channel`→`/check channel` redirect path (F-6) |
| Success criteria measurable, no subjective language | PASS — SC-001..SC-007 are all greppable/assertable |

**Key structural defect:** the "Replaces (hidden aliases)" column (§5) is the spine of US-1/DS-2/SC-004, and it is **factually wrong** in two rows (`/new`, `/session new` don't exist as backend commands; `/model` isn't a current command to rename into). Traceability is internally consistent but built on an inaccurate current-state model.

---

## Test Coverage Assessment

- **Surface-gating unit tests (#1–#4):** well-designed, table-driven, include the critical normal-text regression (#4). Good. Verified `OutcomePassthrough`/`OutcomeHandled` are the real enum constants.
- **Alias resolution (#5):** correct mechanism (registry index maps alias→canonical), but DS-2's pairs include non-existent `/new`, `/session new` (F-1) and the non-1:1 `/switch` (F-6). Fix the dataset.
- **Missing negative/edge coverage:**
  - No test for the `/clear` cross-surface effect difference (F-3) — the most likely production surprise (user expects local clear, CLI truncates server history).
  - No test that web `delivery:agent` (`/skill`) actually reaches passthrough on the backend (F-10).
  - No test that `HELP_TEXT` / the MessageInput array are gone (F-4, F-17) — #14 greps only `SLASH_COMMANDS`.
  - Existing SPA suites `ChatScreen.test.tsx` (T15) and `MessageInput.slash.test.tsx` assert the current hardcoded palette and `startNewSession` wiring — **verified present and will fail** on deletion; §9 must list them as must-rewrite, not just "existing chat tests."
- **E2E (#16):** the `/agents`-passes-through assertion is sound and the most valuable holdout; keep it. Add an E2E for `/clear` web-vs-CLI effect if F-3 is reconciled rather than carved out.

---

## STRIDE Threat Summary

| Component | Threats | Notes |
|---|---|---|
| `GET /api/v1/commands?surface=` | **Info disclosure** (low): exposes command names/descriptions only — all static, no secrets. **DoS** (negligible): tiny static list, behind `withAuth`. **Elevation** (none now): authz becomes relevant only with the §15 future `?agent_id=` extension (F-15). | `withAuth` (mandatory bearer) confirmed as the `/skills` middleware — same gate applies. 401 path covered (US-3/AS-6). |
| Executor surface gating | **Tampering** (none): pure in-process string→enum map, no external input beyond `Channel` which is server-set (verified: `websocket.go` hardcodes `"webchat"`, loop hardcodes `"cli"`, channels use `c.name`). **Spoofing** (none): client cannot set `Channel`. | Safe — the surface decision is derived from a server-controlled origin, not a client-supplied value. |
| SPA palette dispatch | **Repudiation/Tampering** (n/a): client-side UX only. Failure mode is empty palette (F-14), not a security event. | Non-blocking fetch error handling is correct. |

No CRITICAL security findings.

---

## Unasked Questions

1. **Where does `/model [name]`'s setter behavior come from?** It doesn't exist today (F-2). Is the chat-model selector state writable from a slash command, and does it persist or reset on reload? (A4 asserts "does not change server default" — but `switch model` today *does* touch config; reconcile.)
2. **What does `/status` output?** "Bare `/show`" currently returns a usage error, not a status (F-8). Is `/status` new behavior, or a rename of an existing read? If new, it violates the "no new commands" non-goal.
3. **Do `/show`/`/list`/`/check` users actually exist?** (F-16) The one-release deprecation window has a real cost; is there evidence of usage, or is hard-rename acceptable?
4. **Is the `/clear` divergence intentional product behavior or a bug to fix?** (F-3) H6 frames it as a feature; FR-013 forbids it. Product must decide before implementation, not during.
5. **After deletion, what is the canonical source for the web `/help` body?** (F-4) If `HELP_TEXT` stays, the API is not the single source of truth it claims to be.
6. **Should `delivery` be required or optional in the wire schema?** (F-12) This is a one-way contract decision; pick before generating types.

---

## Verdict

**REVISE** — 0 critical, 6 major. The architecture is sound and the executor/contract/origin claims are code-verified true, but the spec's current-state model is wrong in load-bearing places (`/new`, `/session new`, `/model`, `/status`, the `/switch` multiplexer, the `/clear` cross-surface effect, the third `HELP_TEXT` list, and "mirror /skills exactly"). Fix the §5 command map against the real `BuiltinDefinitions()`, reconcile or carve out the `/clear` behavior claim, add `HELP_TEXT` to scope, and reword the `/commands` endpoint as "`/skills` shape + a new `surface` param." None require re-architecting.

Review written to: `docs/internal/specs/slash-command-harmonization-spec-review.md`

To address these findings, run:
  `/plan-spec --revise docs/internal/specs/slash-command-harmonization-spec.md docs/internal/specs/slash-command-harmonization-spec-review.md`
