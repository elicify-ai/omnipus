# Feature Specification: T11 — Documents and third-party agents

Status: Reference only — superseded for building by the slim spec `t11-documents-and-3p-agents-spec.md` (founder 2026-10-29: "do not overcomplicate it, it is a simple setting in the ui for t11, nothing special")

| Header | Value |
|---|---|
| Round | Round 2 corrections applied 2026-10-29 — the final correction (grill round 2 of 2 was final: BLOCK, 1 CRITICAL, 11 MAJOR, 3 MINOR — `t11-documents-and-3p-agents-spec-review-round2.md`). No third grill. Items still open after this correction go to the founder (§21). Previous versions: `.bak-round1`, `.bak-round2`. |
| Author | architect (spec written with the `plan-spec` skill) |
| Train | T11 (Mac squad). **Slice A** = #882 narrow guard fix + candidate-validation protection (person-only gate **and** the shared program-file check) — lands first, independent of T8; T8 reuses both. **Slice B** = third-party Settings/editor parity, developed after T8's contract commit and landing **in the same train as T8's backend**. **#1489 (`convert_document`) is ON HOLD** and in neither slice (§2.4). |
| Issues | #1489 (on hold); #882 (narrowed 2026-10-29 to the shell guard false blocks); third-party (`subagent_3p`) setup parity "A: full parity". #1067 closed. Document companion files ride T9. |
| Code base | `origin/release/v0.1.1` at `b7d4483c9`, read only. Nothing executed against code. |
| T8 interface | T8 tool-parity design revision 5.1 read (`f2b69d2`); the chief relayed acceptance of CH-1…CH-4 into **revision 5.3** (not read by me — §21.2). Names used: `ThirdPartyAgentSettings`, `sandbox.third_party_agents`, `GET`/`PUT /settings/third-party-agents`, `ExecutorConfig.effective_cli_path`, `program_warning`, `ErrorResponse` code `cli_path_invalid`, `env_overrides_remove`. |
| Binding security review | `(maintainer-local path)` |
| Certainty labels | **Verified** = read in the pinned code or document in this task; **Inferred** = follows from what was read, not run; **Unknown** = not established. |

## 0. Bottom line

1. **#1489 PowerPoint→PDF is on hold.** The founder likes built-in Office-document capability but said "the dependency to LibreOffice is not good, we need something lightweight" and wants to discuss. The converter design (AD-1…AD-6) stays in this spec for reference only; it is not built in T11 until he decides. The round-2 critical finding (who may execute a converter) is open with it.
2. **Shell guard (#882), narrow fix (Slice A):** a quoted absolute literal such as `'/api/v1/users'` is no longer refused when it stands alone (no adjacent string, no `+`, no `%`, no prefix letter), contains no special characters, and its top folder does not exist and cannot be created. Everything else, including `../`, is refused as today.
3. **Candidate validation (Slice A):** the route that runs a typed program path accepts only a signed-in person, and runs the shared program-file check (location, not agent-writable, not writable by others, every link in the chain) **before** starting anything. T8 reuses both pieces.
4. **Third-party agents (Slice B, same train as T8's backend):** per CLI, Settings holds a person-entered preset — program path plus **free-text parameters** (any parameter, for example Codex's sandbox bypass; founder: "yes all parameters are supported, no warning"; "A: Allow, I accept the risk"). Preset parameters are applied last and are not filtered; agent-supplied flags still are. The audit records that a person preset was used and the filter bypassed. Real exposure, stated plainly: **Omnipus does not sandbox external CLIs**; with Codex's own sandbox switched off, Codex can read and write anything the Mac user account can.
5. Diagnostics now name the workspace they test, separate "sign-in rejected" from "test could not complete", and never show a result for an older configuration as current.
6. **Still open for the founder:** only the #1489 decision (including converter execution authority, R2-CRIT-001). Nothing else blocks.

## 1. Decisions log (binding inputs)

| # | Who, when, where | Decision (quoted where available) |
|---|---|---|
| F-1 | Founder, 2026-10-29, Mac terminal question tool | T11 = (1) #1489 PowerPoint→PDF inside the macOS sandbox, (2) #882 narrowed to the shell guard false blocks only, (3) third-party agent setup parity "A: full parity" … plus fix the greyed-out screen fields. #1067 closed. Companion files ride T9. (The "change CLI path by chat" part is superseded by F-10/R1-FQ1: a person sets presets in Settings.) |
| F-2 | Founder, same session | Hands-on proof on macOS; Linux and Windows covered by automated no-regression tests. |
| F-3 | Founder, same session | The agent installs LibreOffice itself via `environment_setup` on a fresh test instance, then converts a multi-slide deck. (On hold with #1489.) |
| F-4 | Founder, same session | Setting environment variables for a CLI touches secrets handling; security-lead reviews. Security review B is binding. |
| F-5 | Founder, ~05:05Z | **"C: Full parity, no extra ask"** — env overrides and extra CLI arguments like any other setting; no additional approval beyond the normal tool policy. |
| F-6 | Founder to the cloud chief, #team-a, 05:05Z | **"yes full parity"** |
| F-7 | Security review B (binding) | Reject LaunchServices; approve the filtered registry bound to the selected converter, with conditions. |
| F-8 | Coordinator steers | T11's tool rows are rows in T8's single field-rule table; Slice A first. |
| F-9 | Founder via chief, 05:22Z | Allow-list entities — **superseded by F-10**. |
| F-10 | Founder via chief, 05:27Z | No list entities; agents pick only the CLI; one person-edited Settings value per CLI, pre-filled by filesystem-only detection; save-time validation. Extended by R1-FQ1 and R2-FQA. |
| C-1 | Cloud chief, #cross-team, 05:37Z | T8 takes X1 (store as typed; check resolved file and every link at save and every run), X2 (env merge + remove list), X3 (descriptor rows), X5 (no soul for third-party agents), X8 (masking in the same train as T11's editor). T11 takes X4 and the X6 order. **Slice B and T8's backend land in the same train.** |
| R1-CRIT | Founder, round-1 interview, ~07:30–07:45Z | **"A: Close it"** — candidate validation accepts only a signed-in person and runs the same location/permission/link-chain checks as saving a path **before** starting anything. Amend the earlier CLI-path ADR's authorization decision. |
| R1-FQ1 | Founder, own words | **"when a new 3rd party agent is configured i need to choose either open code, codex or claude code, the path to the executable is preconfigured under settings including standard parameter like bypasspermissions and similar, additional parameters and environment variables can be added still freely"** — confirmed **"A: Yes, exactly"**. All three CLIs stay. |
| R1-FQ2 | Founder | **"A"** — one converter-bound invocation; never trust the soffice exit code. (Superseded for now by the #1489 hold, H-1.) |
| R1-FQ3 | Founder | **"A: Narrow fix only"** — fix absolute-literal false blocks; keep refusing `../`; record the limitation. |
| R1-FQ4 | Founder | Encrypted private variables for third-party CLIs: **"not now, file issue"**. |
| R1-FQ5 | Founder | **"A: Refuse the run"** — require a saved Settings path; ignore legacy per-agent paths; explain how to repair. |
| R1-FQ6 | Founder | **"B: No password"** — a signed-in person is enough. |
| R1-FQ7 | Founder | **"A: Hands-on per landing"** — exact-candidate macOS hands-on/validator evidence plus correctly wired fixture gates; no new CI job. |
| R2-FQA | Founder, Mac terminal, ~08:05Z; chief's relay 08:26Z | **"yes all parameters are supported, no warning"**; chief's relay of the founder: **"it was just an example, any CLI parameter as free text"**. Preset = program path + free-text parameters typed by a person; the run-time flag filter does not apply to them (it still applies to agent-supplied flags); applied last; shown in full; no UI warning; audit records in clear that a person preset was used and the filter bypassed; agents can never edit a preset. |
| C-2 | Cloud chief via coordinator | CH-1…CH-4 accepted: CH-1 preset parameters in T8's contract (rev 5.3); CH-2 refuse with repair message when no preset path; CH-3 `revision` + 409; CH-4 T11 names the shared functions and files. |
| H-1 | Founder via coordinator | `convert_document` **ON HOLD**: likes built-in Office capability; **"the dependency to LibreOffice is not good, we need something lightweight"**; wants to discuss. |
| R2-Q3 | Founder, ~09:25Z, Mac terminal question tool, after the corrected facts (Omnipus does not sandbox external CLIs; a person-set Codex bypass lets Codex read/write anywhere the Mac user account can) | **"A: Allow, I accept the risk"** — any preset parameter allowed; the spec states the real exposure plainly; no UI warning (earlier decision stands); recorded as the founder's accepted risk. |
| R2-Q2 | Founder, same | **"A: Both in part A"** — Slice A builds the shared file-check (location / not agent-writable / not writable by others / link chain) together with the person-only gate; T8 reuses both. |
| R2-Q1 | Founder, same | Converter execution authority: **open** — `convert_document` on hold pending the office-document / lightweight-alternative decision. |

## 2. Scope

### 2.1 In scope

| Part | What changes | Slice |
|---|---|---|
| P2 — #882 | Guard: a stand-alone quoted absolute literal whose top folder cannot exist or be created is not refused; everything else unchanged | A |
| P3 — candidate validation | `POST /system/cli-validate`: signed-in person only (`RequirePersonSession`), then the shared program-file check (`clitrust.CheckProgramPath`) before any spawn; the checked file is the one launched; ADR-030 §11 amended | A |
| P4 — third-party diagnostics | Truthful status; one snapshot with an explicit workspace; result identity; smoke test and preview lose the free path; chat tool `test_agent_runner` | B |
| P5 — screens | Settings → Third-party agents (preset row per CLI: path + free-text parameters, detection pre-fill, Check version of the typed path, Save, Clear); Runtime tab (env and flags editable, preset read-only, masked env editor, no pre-save gate, current-vs-stale diagnostics); states, journeys, accessibility | B |

### 2.2 What T8 owns (rev 5.1 + accepted CH-1…CH-4 → rev 5.3)

| Item | T8 reference | T11 role |
|---|---|---|
| `ThirdPartyAgentSettings` contract with per-CLI path **and free-text parameters**, `revision`, storage under `sandbox.third_party_agents`, `GET`/`PUT /settings/third-party-agents` | §3B D-16; CH-1, CH-3 (rev 5.3) | Screens; T8's routes wrap T11's `RequirePersonSession` and call T11's `CheckProgramPath` |
| Argv order driver flags → agent flags (filtered) → preset parameters (unfiltered, last); audit "person preset used; filter bypassed" | CH-1 (rev 5.3) | Preview shows it; tests T-B13/T-B14 assert it end to end |
| No saved preset path → run refused with repair message | CH-2 (rev 5.3, replaces D-17 fallback) | Diagnostics use the same refusal (`no-settings-path`) |
| Agents pick only `cli`; `cli_path` and preset fields unreachable from `create_agent`/`update_agent`/`set_config`/REST agent writes; `executor.kind`/`executor.cli` never client-settable | D-15, D-18, D-20 X3; A-T27, A-T30 | None |
| Spawn re-check of the stored-as-typed path and every link | D-20 X1 | T8 calls `CheckProgramPath` |
| `env_overrides` merge + `env_overrides_remove`; masking; redaction | D-12 (as superseded by D-20 X2), C9 | Editor follows the merge contract |
| Descriptor rows; no soul for `subagent_3p` | D-20 X3, X5 | Frontend consumes |
| Masking ships with the editor change | D-20 X8 | Same train |

### 2.3 Out of scope

| Item | Why |
|---|---|
| LaunchServices or any new Mach service | Rejected by review B |
| `../` handling in the guard | Kept as today (R1-FQ3); limitation recorded (AD-8) |
| Template / concatenated / glob / prefixed script literals | Stay refused (AD-7); workaround documented |
| Encrypted private variables for third-party CLIs | R1-FQ4 "not now, file issue" — team-lead files the issue after aligning wording |
| A UI warning for risky preset parameters | Founder: "no warning" (R2-FQA, R2-Q3) |
| Sandboxing external CLIs | Not part of T11; the exposure is stated and accepted (R2-Q3) |
| A new CI job | R1-FQ7 |
| #882 parts 2–4, #1067, T9 companion files | Closed, narrowed or owned elsewhere |

### 2.4 ON HOLD — #1489 converter (`convert_document`)

Everything below is **not** in Slice A or Slice B, is not built, tested or documented in T11, and stays only as reference for the founder's decision (H-1, R2-Q1): AD-1…AD-6; US-1, US-2; BDD-01…BDD-09, BDD-08b; SEC-01…SEC-07; T-A01…T-A25, T-A50, F-A01; FR-001…FR-016; SC-001, SC-002, SC-011; MV-2…MV-7; U-01, U-02, U-03, U-06; the converter rows of §11, §19 and §22. Open findings held with it: **R2-CRIT-001** (converter execution authority — "open, on hold with #1489, founder to decide"), R2-MAJ-005 (valid-PDF and rendered proof), R2-MAJ-009 (peak-memory instrument), R2-MAJ-010 (scratch cleanup), R2-MAJ-011 (spreadsheet recalculation must not be removed), R2-MIN-002 (timestamp freshness), and the converter half of R2-MIN-001. Any future converter design must answer all of them; the existing seatbelt allow-set (two Mach services) and review B stay binding. The deletion of the unwired shared-store code (FR-010) also waits.

## 3. Round 1 responses (historical; converter rows now on hold — §2.4)

| Finding | Disposition | What changed (sections) |
|---|---|---|
| CRIT-001 candidate validation bypasses the person-only boundary | **Fixed** per R1-CRIT | AD-11: `POST /system/cli-validate` requires a signed-in person (a `Gateway.Users` identity; refuses CLI-token and dev-bypass requests, 403) — Slice A; then runs T8's save-time location/permission/link-chain checks before any spawn (zero spawn on refusal) — Slice B *(superseded by R2-Q2: both pieces land in Slice A — see §3b and AD-11)*. ADR-030 §11 amendment text in §5. Contract C-A1, C-A2. Tests T-A40…T-A46 (all Slice A after R2-Q2). BDD SEC-50…SEC-53, BDD-51. |
| MAJ-001 registry bound to PATH, not the rendering converter | **Fixed** per R1-FQ2 | AD-1…AD-6 rewritten: `convert_document` tool owns selection, preparation and launch together; failure refuses that conversion before launch; no bash injection; skills switched. Two-installation, absolute-only and PATH-change cases in Dataset C and BDD-03/04/06. |
| MAJ-002 "literal" vs regex fragment | **Fixed** per R1-FQ3 | AD-7: rule applies only to a candidate that is the **entire** content of one quoted string, with no glob, brace, bracket, template, escape, expansion or `..`; fragments keep today's refusal. Dataset G adds glob/brace/bracket/escape/mixed-quote negatives. |
| MAJ-003 traversal needs physical semantics | **Resolved by scope** (R1-FQ3: keep the `../` veto) | AD-8: veto unchanged; limitation recorded; old traversal tests stay as regression controls. No traversal resolution is built. |
| MAJ-004 scope-private read not proven on macOS | **Partly fixed, partly rejected with reasons** | Fixed: the claim is now "no write and no selection of another scope's copy", proven by a real Seatbelt child (T-A17r). Rejected: kernel **read** denial of other scopes' copies is not promised. Reason: copies are filtered LibreOffice registries derived from installed bundles — not intended to contain secrets; confidentiality is not promised (filtering removes one component and keeps the other bytes, so it certifies nothing about content); source containment and the protected-inventory refusal remain mandatory; review B's threat is influence (shared selection/write), which the per-call, per-scope selection and the read-only grant close. Under the open-read Seatbelt model (`seatbelt_profile.go::renderSeatbeltProfile` emits `(allow file-read*)`) a read denial would need new deny machinery for no confidentiality gain. Recorded for security-lead (SL-2). |
| MAJ-005 memory budget and unbounded retention | **Fixed** | AD-4: caps 2 MiB per `.rdb`, 3 MiB total; one preparation at a time process-wide; peak heap measured in T-A10m (≤ 8 MiB); preparation only inside `convert_document`, never per bash call; retention: newest two generations per scope, never one in use, never the last usable one (T-A07r). |
| MAJ-006 no single authority for diagnostics | **Fixed** | AD-12: one diagnostic snapshot (agent, CLI, effective program, preset parameters, agent flags, model, env, working folder, principal); saved-agent requests reject conflicting fields (400); draft mode separated; limiter identity = initiating person (REST) or calling agent (tool), same limiter instance; per-target in-flight cap; snapshot taken at start; cancellation; audit attribution. Encrypted variables removed (R1-FQ4), so no workspace-secret authority is needed. Contract C-B1, C-B2. |
| MAJ-007 presence presented as sign-in | **Fixed** | AD-13: three fields — program runs / credentials found (from the diagnostic environment, including credential directories) / authentication `not_verified` in connection mode, `verified` or `failed` only from a smoke test. Contract C-B3. Docs reworded. Tests cover invalid non-empty key, empty override masking a gateway key, alternate credential directory, no credentials. |
| MAJ-008 pre-save check blocks saving the first credential | **Fixed** | AD-15: the editor's pre-save `testAgentRunner` gate (`AgentProfile.tsx`, autosave callback, Verified) is removed; save first, then a non-blocking diagnostic status. Journey J-2 and E-1 start from an unauthenticated worker. |
| MAJ-009 old dependency graph vs chief's answers | **Fixed** | §2.2 pinned to rev 5.1; X items marked resolved; §20 graph: development order (T8 contract → T8 backend ∥ T11 B) vs release order (one train with T8 backend). |
| MAJ-010 per-row Save vs whole-object PUT | **Fixed in T11; contract field asked of T8** | AD-14: each row's Save/Clear sends a PUT built from the **last saved server state** plus only that row's change, carrying the settings `revision`; 409 shows a conflict with reload/reapply; other rows' drafts are never sent. Needs a `revision` field in `ThirdPartyAgentSettings` (chief question CH-3). Two-tab test F-B04b. |
| MAJ-011 native gates do not run the promised tests | **Fixed within R1-FQ7 A** (no new CI job) | §18.2: existing `phase3-macos.yml` core step and existing Windows tools job get the named tests added to their selections and path filters; a required-test receipt check fails on zero matches or skips; Windows-runnable fake executable built from Go in `TestMain`; real conversion = exact-candidate hands-on U-02; missing LibreOffice = "not tested", never PASS. |
| MAJ-012 incomplete UI states and journey | **Fixed** | §12.1 state/transition matrices for Settings and Runtime; §12.2 journeys J-1…J-3 (Workspace → Team → worker → Runtime → Settings → back → repair/test); stale detection never overwrites a typed draft. |
| MAJ-013 no keyboard/focus/announcement contract | **Fixed** | §12.3 accessibility contract; tests F-B07, E-3. |
| MIN-001 catalogue-first and gates | **Fixed** | §12.4 component plan from `design-system/catalog.json`; gates `npm run typecheck`, `npm run lint:design-system-locks`. |
| MIN-002 status field | **Fixed** | `Status: In review` at the top. |
| MIN-003 ADR dispositions | **Fixed** | §5. |
| MIN-004 contract changes too late | **Fixed** | Contract section moved to §4, before decisions. |
| MIN-005 contradictory oracles and mappings | **Fixed** | Pointer presence now conditional on successful preparation (FR-005); gateway-env test uses before/after equality with a pre-existing-value fixture (T-A20r); guard regression list names no superseded test (the veto is kept); FR-054's T8-owned surfaces mapped to T8 tests A-T27/A-T30 instead of C-B1/C-B2. |
| OBS-001 short-secret sink tests | **Kept** | T-B09 drives real sinks (success text, version failure, stderr tail, logger, transcript, HTTP/tool response) with Dataset S. |
| OBS-002 a fixed path is not confinement | **Kept, wording narrowed** | AD-14 consequences and docs: the Settings path controls which program starts; it does not confine what that CLI loads under agent-set environment values (T8 S-5). |
| OBS-003 conversion vs full visual QA | **Kept** | U-03 is "blocked (T9)" when companions are unreachable; Slice A claims conversion only. |
| Reviewer's unasked A1–A6 | A1 → B (revision-checked full replacement) pending CH-3; A2 → A (T-A12 on a real bundle before GREEN); A3 → A (one snapshot); A4 → A (real sinks); A5 → A (coordinator records combined scope per candidate); A6 → A (T9 prerequisite recorded) | §§4, 12, 14, 20 |

## 3b. Round 2 responses (final correction)

| Finding | Disposition | What changed (sections) |
|---|---|---|
| R2-CRIT-001 converter execution authority | **Open — on hold with #1489; founder to decide** (H-1, R2-Q1) | §2.4; §21.1. No converter is built, so no executable is run on an agent's choice in T11. Any future design must decide execution authority through the existing two-layer tool policy (no third layer, no `write_file`-derived grant) and define Windows/no-kernel behaviour, with real-dispatch permission tests (reviewer's recommendation recorded there). |
| R2-MAJ-001 adjacent/concatenated literals | **Fixed** | AD-7 condition 6: the quoted literal must stand alone — the nearest non-whitespace character before the opening quote is one of `( , = [ { :` or the start of the shell word, and after the closing quote one of `) , ] } ;` or the end of the shell word. Adjacent literals (including across newlines), `+`, `%`, `.format`, `*`, and `f`/`r`/`b`/`u` prefixes are therefore refused. Dataset G19–G24, SEC-13 rows. |
| R2-MAJ-002 checks split across landings | **Fixed** per R2-Q2 | AD-11: Slice A builds `pkg/gateway/middleware/person_session.go::RequirePersonSession` **and** `pkg/clitrust/check.go::CheckProgramPath` (+ `roots.go::AgentWritableRoots`, platform files); candidate validation calls both before any spawn; T8 reuses both. ADR-030 amendment text is single-landing. SEC-52/53 and T-A44…T-A46 move to Slice A. |
| R2-MAJ-003 two input authorities for Check version | **Fixed** | AD-12 draft mode no longer covers Settings. Check version = person-only candidate validation of the **typed** path through the shared check; the result is tied to that path and saves nothing. BDD-51, T-A46 (saved A, typed B; and no saved path). |
| R2-MAJ-004 Codex bypass promised a boundary that does not exist | **Fixed (exposure stated; founder accepted)**; reviewer's "user-facing warning" **not adopted** — founder: "no warning" | AD-14 states the real exposure: external CLIs are started directly (`driver_codex.go::CodexDriver.Run` with `exec.CommandContext`, no Omnipus confiner — per reviewer E9); a working directory is not a boundary; with Codex's sandbox off, host-account access is limited only by the OS account. ADR-032 dated amendment text in §5. `docs/security.md` says it plainly (§19). |
| R2-MAJ-005 valid-PDF and rendered proof | **On hold with #1489** | §2.4 |
| R2-MAJ-006 no authoritative workspace for diagnostics | **Fixed** | AD-12: explicit `workspace_id` (REST) / calling turn's workspace (tool); membership validated with dispatch's own resolver (`workspace_reroot.go::resolveTurnWorkDirAndWorkspaceOrRefuse`, per reviewer E11); sole membership used when none given; several → 400 `workspace_required`; none → refused as dispatch refuses; draft mode uses an owned scratch folder; `max_tool_iterations` added to the saved-mode conflict list; smoke keeps its fixed small turn cap. C-B1/C-B2; BDD-37; T-B16. |
| R2-MAJ-007 failed smoke ≠ failed sign-in | **Fixed** | AD-13 / C-B3: `outcome` (`succeeded`, `auth_rejected`, `could_not_complete`, `timed_out`, `cancelled`) separate from `authentication` (`verified`, `rejected`, `not_verified`); `ok` redefined per response; UI wording per outcome. BDD-38 outline; T-B15. |
| R2-MAJ-008 stale results | **Fixed** | AD-18: every diagnostic response carries `agent_revision` and `settings_revision` (non-secret); the Runtime tab shows a result as current only when both match what it has loaded; otherwise "Result for an earlier configuration", not announced; late responses after unmount ignored. §12.1 states; BDD-39; F-B08. |
| R2-MAJ-009 peak-memory instrument | **On hold with #1489** | §2.4 |
| R2-MAJ-010 converter scratch lifecycle | **On hold with #1489** | §2.4 |
| R2-MAJ-011 spreadsheet recalculation | **On hold with #1489** | §2.4 — recorded constraint: a future converter must not remove the elicify-xlsx recalculation workflow. |
| R2-MIN-001 impossible/incomplete test references | **Fixed** (guard/candidate parts); converter parts on hold | §3 CRIT-001 row now cites T-A40…T-A46; FR-031 maps to T-A45/T-A46. BDD-04 and FR-014 are converter items (on hold). |
| R2-MIN-002 coarse timestamps | **On hold with #1489** | §2.4 |
| R2-MIN-003 Windows package/test mapping | **Fixed** | §18.2: one command per package with exact `-run` names; receipt list built from test IDs; absent or skipped required tests fail. |
| Reviewer ruling on MAJ-004 wording ("no secrets") | **Fixed** (text applies to the on-hold design) | §3 MAJ-004 row now says "not intended to contain secrets; confidentiality not promised". |
| Prepared changes from the R2-FQA / CH answers | **Applied** | §1 R2-FQA, C-2; AD-14; AD-11 names; §21.2 resolved. |

## 4. Contract changes (Hard Constraint #8) — stated first

T11 decides these shapes as architect; `backend-lead` edits and regenerates. T8's items (C8, C9, `ThirdPartyAgentSettings` with path, free-text parameters and `revision`, `env_overrides_remove`, `effective_cli_path`, `program_warning`, `cli_path_invalid`) are not repeated.

| ID | Slice | File | Change |
|---|---|---|---|
| C-A1 | A | `contracts/openapi.yaml` `POST /system/cli-validate` | Add `403`: "requires a signed-in person; CLI-token and development-bypass requests are refused". Description no longer says create-parity. |
| C-A2 | A | `contracts/components/schemas/CliValidateResponse.yaml` | Reason enum adds `path-not-allowed` (the shared check refused the candidate; message names the failed check, never file contents). |
| C-B1 | B | `contracts/components/schemas/ExecutorSmokeTestRequest.yaml` | Remove `cli_path`. Add optional `workspace_id`. **Saved mode** (`agent_id` present): `cli`, `model`, `cli_args` must be absent → else 400 `diagnostic_field_conflict`; workspace per AD-12. **Draft mode** (no `agent_id`): `cli` required, `model`/`cli_args` optional, runs in an owned scratch folder; program = the saved preset path for `cli`. |
| C-B2 | B | `contracts/components/schemas/ExecutorCommandPreviewRequest.yaml` | Remove `cli_path`. Same two modes; saved-mode conflict list also includes `max_tool_iterations`. Response argv shows preset path, driver flags, agent flags (with `dropped_args`), preset parameters last. |
| C-B3 | B | `contracts/components/schemas/RunnerTestResponse.yaml`, `ExecutorSmokeTestResponse.yaml` | `RunnerTestResponse`: `ok` = program runs **and** credentials found (no longer "authenticated"); adds `program_runs`, `credentials` (`found` \| `not_found` \| `unknown`), `authentication` (always `not_verified`), `agent_revision`, `settings_revision`; reason enum: `unauthenticated` replaced by `credentials-not-found`, adds `no-settings-path`, `workspace_required`, `not-a-member`. `ExecutorSmokeTestResponse`: `ok` = `outcome` is `succeeded`; adds `outcome` (`succeeded` \| `auth_rejected` \| `could_not_complete` \| `timed_out` \| `cancelled`), `authentication` (`verified` \| `rejected` \| `not_verified`), `failure_reason` (value-free text), `workspace_id`, `agent_revision`, `settings_revision`. |

No schema for `test_agent_runner` (agent tool; result in the generic tool-result frame). Audit events are not wire formats.

**Order (root `CLAUDE.md`, Contract regeneration):** (1) edit the schema files above (no new file); (2) edit `openapi.yaml` responses/descriptions (C-A1); (3) `scripts/gen-contracts.sh`; (4) one atomic commit with `pkg/api/generated/` and `src/lib/api/generated/`; (5) handlers and SPA use generated types only; `make verify-contracts` green. C-A1/C-A2 are one commit in Slice A (the existing CLI-path hint component must handle the new reason — A-5); C-B1…C-B3 land after T8's contract commit.

## 5. ADR dispositions

| ADR (title) | Disposition | Owner / when |
|---|---|---|
| **External-Executor CLI Path Detection, Prefill & Validation** (ADR-030) | **Dated amendment of §11 F-01**, single landing (Slice A). Text: "Amended 2026-10-29 (founder, T11: R1 'A: Close it', R2 'A: Both in part A'): `POST /system/cli-validate` is no longer create-parity. `pkg/gateway/middleware/person_session.go::RequirePersonSession` admits only a signed-in person — a `Gateway.Users` identity on a non-bypass request; CLI-token and development-bypass requests get 403 before any work. Before spawning, `pkg/clitrust/check.go::CheckProgramPath` resolves the full link chain and refuses the candidate unless the final file and every link are outside `$OMNIPUS_HOME`, workspaces, mounts and other agent-writable locations, are not writable by others, and the final file is a regular executable (Windows: absolute drive/UNC path, admitted extension, no 8.3 short names or alternate data streams). A refusal spawns nothing and returns `path-not-allowed`; an accepted candidate launches exactly the checked resolved file. Detection stays filesystem-only and unaudited. T8's Settings routes reuse both functions." | architect, Slice A |
| **The bash text guard is a third rule layer, and it never implemented ADR-062** (ADR-068) | **Dated amendment**: the stand-alone quoted literal rule (AD-7, six conditions) and the recorded `../` limitation (AD-8). §9.3 kept: the rule fires only where the guard can show no file is reachable from that literal. | architect, Slice A |
| **External-Agent (subagent_3p) Workspace Execution & Identity** (ADR-032) | **Dated amendment.** Text: "Amended 2026-10-29 (founder, T11: 'yes all parameters are supported, no warning'; 'A: Allow, I accept the risk'): each supported CLI has a person-entered Settings preset — program path plus free-text parameters. Preset parameters are appended after the driver's flags and the agent's flags and are **not** passed through `argsafety.go::dangerousCLIFlagsByCLI`; agent-supplied flags still are. Agents can read presets but can never change them. Every run that uses preset parameters writes an audit entry stating in clear that a person preset was used and the flag filter bypassed (parameter text passed through the sensitive-data filter). **Accepted risk:** Omnipus does not sandbox external CLIs — they are started directly in their working folder with a scrubbed environment and egress proxy, not under an Omnipus filesystem confiner; a working folder is not a boundary. A preset such as Codex's `--dangerously-bypass-approvals-and-sandbox` removes the CLI's own sandbox, after which the CLI can read and write anything the operating-system user account running Omnipus can. No UI warning is shown (founder decision)." | architect, Slice B |
| **Built-in agents, skills, and visual reading** (ADR-090) | No change while #1489 is on hold. | — |
| New ADR | **Not needed for Slices A and B**: every open choice was decided by the founder. The #1489 converter design (including execution authority, R2-CRIT-001) will need a decision record when the founder decides — not pre-approved here. | architect, after H-1 |

## 6. Existing Codebase Context

GitNexus MCP tools were **not available in this session** (no `gitnexus` tools loaded). Fallback per root `CLAUDE.md`: `git grep` / `git show` on `origin/release/v0.1.1` at `b7d4483c9`. Every impact row below is therefore **Inferred** from a caller sweep, not a graph run. Developers must run `impact` before editing (rule 9).

Go reference patterns (`docs/reference/go-implementation/`) do not exist in this repository — section N/A.

### 6.1 Verified findings that drive this spec

| # | Finding | Evidence (`file::symbol`) | Certainty |
|---|---|---|---|
| E1 | The filtered-registry code exists and works on a real bundle (MacOSXSpellChecker 0, HunSpell 1 in the copy; 1/1 in the stock file) | `pkg/documentruntime/converter_registry.go::filterMacSpellChecker`, `prepareConverterServiceRegistry`; re-check log `logs/1489-registry.log` (exit 0) | Verified |
| E2 | Nothing in production calls the registry preparation; the exported `PrepareConverterServiceRegistry` / `MaterializeConverterServiceRegistry` have no production caller | `git grep` over `*.go` excluding tests: only declarations | Verified |
| E3 | The only production use of `ConverterServiceOverride` is a readiness check whose URL is discarded | `pkg/tools/shell_env.go::documentEnvTurn` (`if _, err := documentruntime.ConverterServiceOverride(layout)`) | Verified |
| E4 | `URE_MORE_SERVICES` is never set for any child | `pkg/documentruntime/runtime.go::ChildEnvironment`; `git grep URE_MORE_SERVICES` finds comments only | Verified |
| E5 | The registry store is one folder under the shared document prefix with one global `active` pointer | `converter_registry.go::ConverterServiceRegistryDir`, `ConverterServiceOverride`, `prepareConverterServiceRegistry` (commit writes `active`) | Verified |
| E6 | A Homebrew-style wrapper (`/usr/local/bin/soffice`) is not recognised (no error, `ok=false`); the app-bundle binary is | `converter_registry.go::converterBundleServicesDir`; `logs/1489-registry.log` | Verified |
| E7 | The original crash used `/usr/local/bin/soffice`… under the Omnipus profile: `Abort trap: 6`, exit 134; `launchservicesd` alone fixes it (4/4) | jarvis-blockers `REPORT.md` §2 | Verified (by that report) |
| E8 | Workspace runtime bin directories come first on the child PATH, then shared generations, then the document prefix, then the host PATH | `pkg/environmentsetup/publish.go::RuntimeEnvPaths` (workspace view added before shared); `pkg/tools/shell_env.go::composeExecutionEnv` | Verified |
| E9 | One per-call composition point exists for env and kernel grants (foreground and background) | `pkg/tools/shell.go::executeRun` (calls `documentEnvTurn`, `runtimeEnvTurn`, `turnAllowRoots`, `composeExecutionEnv` four times by spawn path) | Verified |
| E10 | Private variables are appended after every managed layer; names are checked against a protected set that a drift test ties to the real composers | `pkg/tools/private_env.go::withPrivateVariable`; `pkg/childenv/private_names.go::CheckPrivateVariableName` (comment: "Test 91 (the drift test)") | Verified (drift test name not opened: Inferred) |
| E11 | The pptx skill invokes LibreOffice through an executable variable that is a resolved absolute path, not necessarily `soffice` from PATH | `pkg/skills/embedded/elicify-pptx/knowledge/visual-qa.md` ("`libreoffice_executable` is the approved binary"; "paths are resolved absolute project paths") | Verified |
| E12 | Seatbelt preamble allows exactly two Mach services; a test pins it | `pkg/sandbox/seatbelt_profile.go::seatbeltSystemPreamble`; `pkg/sandbox/seatbelt_deny_darwin_test.go::TestSeatbelt_OpenProfileKeepsIPCDefaultDeny` | Verified (exists); content per security review E1 |
| E13 | Guard false blocks: `'/api/v1/users'`, f-string `'/api/items'`, `'/%s/%d'`, heredoc `"/api/v1"` blocked; `'../shared/x'` blocked by the blanket traversal check; `/etc/t11probe` correctly blocked | re-check `logs/882a-guard.log` (exit 0); `pkg/tools/shell_path_guard.go::guardCommandClassified` (`strings.Contains(cmd, "../")`), `absolutePathPattern`, `checkPathSegment` | Verified |
| E14 | The guard already resolves the deepest existing ancestor of a candidate for mount matching | `shell_path_guard.go::checkPathSegment` → `resolvePathAgainstExistingAncestor` | Verified |
| E15 | Runtime-tab CLI path, environment and arguments inputs are disabled by `!isFieldEditable('executor')`; the server descriptor list never contains `executor`; test fixtures grant `executor` and hide the bug | `src/components/agents/AgentProfile.tsx` (runtimePanel); `pkg/agentmutation/policy.go::describedFields`; `src/components/agents/agentProfileTestFixtures.ts`; `logs/3p-descriptors.log` (18 descriptors, none `executor`) | Verified (code); greyed-out in a browser: Inferred high |
| E16 | The UI save path throws for any changed field whose descriptor is not editable | `src/components/agents/agentDraft.ts::buildAgentUpdate` (`ProtectedAgentFieldChangeError`) | Verified |
| E17 | REST update takes `executor.kind` from the request and only refuses a non-native kind on a non-worker; a native worker can be switched to `external-cli`, although the contract says kind is derived from type and not client-settable | `pkg/gateway/rest_agents_update.go::validateTarget` (merge block: `merged.Kind = execCfg.Kind`; check `!uf.foundAgent.IsWorker()`); `contracts/components/schemas/ExecutorConfig.yaml` (`kind` description) | Verified (code); exploitability by a live PUT: Inferred |
| E18 | REST GET echoes `env_overrides` values in clear | `pkg/gateway/rest_agent_executor.go::setAgentExecutorResponse` (`exec.EnvOverrides = &eo`) | Verified — answers T8 rev 3's open E34 ("Unknown whether REST GET masks") |
| E19 | The contract example invites an API key in `env_overrides` (`ANTHROPIC_API_KEY: "..."`) | `contracts/components/schemas/ExecutorConfig.yaml` | Verified |
| E20 | Real dispatch passes `env_overrides` to the CLI child; the smoke test does not | `pkg/agent/external_dispatch.go::startAndDrain` (`EnvOverrides: ed.execCfg.EnvOverrides`); `pkg/gateway/rest_executor_smoketest.go` run options (`Env: sandbox.ScrubGatewayEnvForRunner()`, no `EnvOverrides`) | Verified |
| E21 | The zero-token connection test decides "signed in" from the **gateway's** environment and credential files, not from the agent's configured environment | `pkg/agent/runner/conntest.go` (`hasCredsFunc: … anyEnvSet("OPENAI_API_KEY", "ANTHROPIC_API_KEY")`); `TestConnectionWithPath` takes no environment | Verified (that `anyEnvSet` reads the process environment: Inferred high) |
| E22 | No agent tool runs the runner check or smoke test; REST has `POST /agents/{id}/runner/test`, `POST /agents/executor-smoke-test`, `POST /system/cli-validate`, `GET /system/cli-detect` | `contracts/openapi.yaml` (`testAgentRunner`, `postAgentsExecutorSmokeTest`, `postSystemCliValidate`, `getHostCliDetect`); `pkg/gateway/rest_agents.go::testAgentRunner`; `pkg/gateway/rest_executor_smoketest.go::postAgentsExecutorSmokeTest` | Verified |
| E23 | Precedent for "test" tools: `test_provider` and `test_channel` ship `allow` on the ceiling, `AutoAsks` class, Omnipus supported set | `pkg/config/defaults.go`; `pkg/tools/auto_approve.go`; `pkg/coreagent/role_policies_adr090.go::omnipusSupportedTools` | Verified |
| E24 | soul for third-party agents: the REST create schema requires `soul`; `create_agent` refuses it; the runtime prepends a soul read from `<agent Home or defaults.Home>/SOUL.md`, while the agent store writes `agents/<id>/SOUL.md`; created agents do not set `Home` | `contracts/components/schemas/AgentCreateRequestSubagent3p.yaml` (`required: … soul`); `pkg/sysagent/tools/agent.go` (validate); `pkg/agent/subturn_identity.go::resolveDelegateSoul`, `composeDelegateInput`; `pkg/agentstore/state.go` (`agents/<id>/SOUL.md`); no `.Home =` in create paths | Verified (code). Consequence — the REST-supplied soul never reaches the CLI, and a `SOUL.md` in the default home would reach **every** custom third-party worker: **Inferred (medium-high)**, not run |
| E25 | Omnipus can already request an agent-private variable for another agent (`variable` + `workspace_id` + `agent_id`), stored encrypted; private variables currently reach bash and environment_setup only, not third-party CLIs | `pkg/tools/request_secret.go` (description); `pkg/secretrequest/private.go` ("only Omnipus may request a private variable for another agent"); `pkg/agent/private_env.go::privateEnvForTurn`; `external_dispatch.go::prepareRunOptions` (no private variables) | Verified |
| E26 | The agent screen hides the private secrets list for workers (third-party included) | `AgentProfile.tsx` (`showPrivateSecrets = … && !isWorkerAgent && !isSystemAgent`) | Verified |
| E27 | macOS CI (`phase3-macos.yml`, `macos-latest`) does not install LibreOffice; Windows Go tests run only selected packages in `cross-platform.yml` | `.github/workflows/phase3-macos.yml` (brew installs coreutils, jq only); `.github/workflows/cross-platform.yml` (`windows-latest`, `go test … ./pkg/daemon/`) | Verified |
| E28 | A token-authenticated request records whether it came from the CLI token (synthetic "cli" identity) rather than a `Gateway.Users` row; `RequireNotBypass` exists as middleware | `pkg/gateway/auth.go::AuthResult.ViaCLIToken`; `pkg/gateway/rest.go::withAuthAndBodyLimit` (puts `CLITokenContextKey` in context); `pkg/gateway/middleware/bypass_gate.go::RequireNotBypass` | Verified |
| E29 | ADR-030 §11 F-01 chose create-parity `withAuth` for `cli-validate` ("not an admin route") | `docs/internal/architecture/ADR-030-external-executor-cli-path-detection.md` §11 | Verified |
| E30 | The Runtime editor runs `testAgentRunner(agentId)` on the **saved** agent before saving and throws on `!result.ok` ("Fix the runner before saving") | `src/components/agents/AgentProfile.tsx` (autosave callback, `testedExecutorSig`) | Verified |
| E31 | The claude driver always passes `--dangerously-skip-permissions`; the codex driver uses `--sandbox workspace-write` + `--ask-for-approval never` and deliberately omits `--dangerously-bypass-approvals-and-sandbox`; the operator-args filter drops escalation flags per CLI | `pkg/agent/runner/driver_claude.go` header; `driver_codex.go` (`buildArgs` notes); `argsafety.go::dangerousCLIFlagsByCLI` | Verified |
| E32 | Codex credential location is resolved from the gateway process environment | `pkg/agent/runner/codex_auth.go` (the runner's Codex credential-path resolver) (no environment parameter) | Verified (signature); reads process env: Inferred high |
| E33 | T8 rev 5.1 names: `ThirdPartyAgentSettings` `{cli_paths}`, `sandbox.third_party_agents`, `GET`/`PUT /settings/third-party-agents` (replace semantics, absent key clears), `effective_cli_path`, `program_warning`, `cli_path_invalid`; X1 stored-as-typed with chain checks; X2 merge + `env_overrides_remove`; D-17 legacy/PATH fallback | `tool-parity-design.md` §3B D-16, D-17, D-19; §3C D-20 (commit `f2b69d2`) | Verified |
| E34 | Existing guard tests contain no quoted nonexistent-root literal expectation that the narrow rule would supersede | `git grep "'/api/\|\"/api/\|/%s" pkg/tools/shell_path_guard_test.go` → no match | Verified (grep; only that file checked) |

### 6.2 Symbols involved

| Symbol | Role | Context (grep, not GitNexus) |
|---|---|---|
| `pkg/tools/shell.go::executeRun` | reads (spawn path reused by `convert_document`); no behaviour change for bash | E9 |
| `pkg/tools/shell_env.go::composeExecutionEnv`, `turnAllowRoots`, `documentEnvTurn` | modifies | Env and kernel allowances; 2 production files call each |
| `pkg/documentruntime/converter_registry.go` (filter, verify, `converterBundleServicesDir`) | extends; deletes `PrepareConverterServiceRegistry`, `MaterializeConverterServiceRegistry`, `ConverterServiceOverride`, `ConverterServiceRegistryDir` | Only `shell_env.go` calls `ConverterServiceOverride` in production |
| `pkg/tools/shell_path_guard.go::guardCommandClassified`, `checkPathSegment` | modifies | 2 production files; 12 test files call `guardCommand` |
| `pkg/agentmutation/policy.go::describedFields`, `ValidateFields` | modifies (via T8 rule table) | Consumed by `rest_agents.go::applyAgentEditableFields`, `sysagent/tools/agent_read.go` |
| `pkg/gateway/rest_agents_update.go::validateTarget` | modifies (kind lock) | T8 also edits this file |
| `pkg/agent/runner/conntest.go::TestConnectionWithPath` | modifies (environment input) | Called by REST runner test and cli-validate |
| `pkg/gateway/rest_executor_smoketest.go` | refactor: extract shared core | Called only by its handler |
| `pkg/sysagent/tools/` new `agent_runner_check.go` | adds `test_agent_runner` | New file; name must not end in `_test.go` |
| [ON HOLD] `pkg/tools/` new `convert_document.go` | adds `convert_document` | #1489 on hold |
| `pkg/gateway/middleware/` new `person_session.go::RequirePersonSession` | adds (Slice A) | Beside `bypass_gate.go::RequireNotBypass`; uses `pkg/gateway/ctxkey` |
| new package `pkg/clitrust` (`check.go::CheckProgramPath`, `roots.go::AgentWritableRoots`) | adds (Slice A) | Reused by T8 Settings save and spawn re-check |
| `pkg/gateway/rest_clivalidate.go::validateCLI` | modifies (person-only, pre-spawn checks) | CRIT-001 |
| `src/components/settings/` new `ThirdPartyAgentsSection.tsx` | adds Settings screen (F-10) | Sibling sections exist (`SandboxSection.tsx`, `ProvidersSection.tsx`, `ReAuthDialog.tsx` — Verified) |
| `pkg/clidetect::DetectAll` | calls (unchanged) | Pure filesystem probe (Verified) |
| `src/components/agents/AgentProfile.tsx` (runtimePanel, `EnvironmentOverridesEditor`) | modifies | Runtime tab |

### 6.3 Impact assessment (Inferred — grep sweep)

| Symbol modified | Risk | d=1 | d=2 |
|---|---|---|---|
| `composeExecutionEnv` (only if the spawn helper is extracted for reuse) | MEDIUM (bash behaviour must stay byte-identical) | `ExecTool.Execute`, `ExecuteAsync`, background runner | agent loop tool dispatch |
| `checkPathSegment` / guard scan | **HIGH** (security text guard) | `guardCommandClassified`, `guardCommand` | 12 test files, `executeRun` |
| `validateTarget` (kind lock) | MEDIUM | `updateAgent` handler | SPA agent save, T8 tool paths |
| `TestConnectionWithPath` signature | MEDIUM | REST runner test, cli-validate, drivers' `Test` | Runtime tab buttons |
| `describedFields` | MEDIUM | REST GET agent, `get_agent` | SPA field enabling |

Both HIGH rows must be reported to team-lead before editing (root `CLAUDE.md`, GitNexus "Always Do").

### 6.4 Execution flows and cluster placement

Flows: bash tool run (foreground/background) → per-call runtime composition → sandboxed spawn; agent PUT → field validation → persistence → GET echo; external-CLI dispatch → child env → driver run; runner connection/smoke checks. Clusters: tools/sandbox (Slice A), agents/gateway/runner (Slice B). Slice A spans `pkg/tools`, `pkg/documentruntime`, `pkg/childenv` — one backend owner.

## 7. Architect decisions

> AD-1…AD-6 describe the #1489 converter and are **ON HOLD** (§2.4): reference only, not built in T11.

Context → Decision → Consequences → Alternatives rejected. Decisions marked "founder" implement a round-1 answer; the rest are the architect's, founder may veto.

### [ON HOLD — §2.4] AD-1 #1489: one converter-bound invocation — the `convert_document` tool (founder R1-FQ2 "A")

**Context.** Review B requires the registry to be bound to the converter that actually renders and delivered only to that process (F-7). The round-0 design injected a pointer into every bash child, which binds to PATH, not to the renderer (MAJ-001). The pptx skill starts LibreOffice through `subprocess.run` with an absolute path (E11). LibreOffice's exit code is unreliable (R1-FQ2 research).

**Decision.** A new built-in tool `convert_document` is the only supported conversion path, on every platform:

| Input | Rule |
|---|---|
| `input` | A file the calling agent may read under its turn policy; extensions `.pptx .ppt .potx .docx .doc .odt .odp .xlsx .xls .ods` |
| `to` | `pdf` only |
| `outdir` | Optional; default the input's folder; must be writable under the turn policy |
| `converter` | Optional absolute path to a LibreOffice the agent may execute; otherwise selected per AD-2 |
| `timeout_seconds` | Default 120, maximum 600 |

The tool selects the converter (AD-2), prepares the registry for **that** installation on macOS (AD-3/AD-4), and launches **that** executable itself — argv, no shell — through the same sandboxed spawn path and per-turn kernel policy as `bash`, with a fresh LibreOffice profile in the per-turn document cache and exactly one `URE_MORE_SERVICES` in the child environment (removing any inherited value). It then verifies the output (AD-5). The result names the converter used, the registry generation (macOS), the output path and size. The elicify-pptx/docx/xlsx render guidance and the document runtime guidance are changed to call `convert_document` instead of `soffice`.

**Consequences.** + Executable, registry and launch are bound in one call; two installations, absolute-only installs and PATH changes cannot mismatch (Dataset C). + Ordinary bash commands are untouched (no all-command injection; review B's "unrelated commands" condition holds by construction). + One place implements "never trust the exit code". − A new tool: registration and policy for every agent (§11). − `soffice` called directly from bash on macOS still crashes under the sandbox; bash gains only a one-line hint when its output shows the crash (FR-015).

**Alternatives rejected.** Pointer in every bash child (MAJ-001). A `soffice` launcher first on PATH (bypassed by absolute paths). A helper script run from bash (the gateway would have to prepare the registry by recognising the command text). LaunchServices (review B). Patching the bundle (review B). `qlmanage -t` fallback (untested; out of scope).

### [ON HOLD — §2.4] AD-2 Converter selection

**Decision.** If `converter` is given, use it. Otherwise the first `soffice` (Windows: `soffice.exe`/`soffice.com`) on the composed child PATH a bash call would see (workspace environment first, then shared generations, the document prefix and the host PATH — E8); on macOS, if none, `/Applications/LibreOffice.app/Contents/MacOS/soffice`. Resolve symlinks once; the resolved file is the one checked and launched. On macOS classify:

| Shape | Rule | Result |
|---|---|---|
| App bundle | Resolved file is `<X>.app/Contents/MacOS/soffice` and `<X>.app/Contents/Resources/services` is a directory | Converter = that file |
| Homebrew-cask wrapper | Regular file ≤ 4 KiB, text: a shebang, optional blank/comment lines, and exactly one `exec '<abs>/Contents/MacOS/soffice' "$@"` line (single or double quotes, no variables) naming a file that passes the app-bundle rule | Converter = the bundle binary named there |
| Anything else | — | Conversion refused: "this LibreOffice layout is not supported on macOS inside the sandbox; install the LibreOffice app bundle with environment_setup" (no stock run — it would crash, E7) |

On Linux and Windows any regular executable converter is used as-is (no registry). A `converter` inside the protected internal inventory is refused (same identity check as the shell guard).

### [ON HOLD — §2.4] AD-3 Per-workspace registry store with bounded retention

**Decision.** `$OMNIPUS_HOME/cache/documents/converter-registry/<scope>/<generation>/`, `<scope>` = lowercase hex SHA-256 of the turn root's real path, `<generation>` = existing content digest. No `active` pointer; the generation travels in memory from preparation to the launch of the same call. A verified identical generation is reused. Retention: after each successful preparation keep the newest **two** generations per scope; delete older ones only if no running conversion holds them (in-process reference count) and never the last usable one. The child gets a read-without-write kernel rule for exactly its generation folder. The unwired shared-store exports and the readiness re-read of the global pointer are deleted (E2, E3).

**Consequences.** Workspace A's copy is never selected for B and no child can write any copy (T-A17r with a real Seatbelt child). Reads of other scopes' copies are **not** promised denied under the open-read model — see the MAJ-004 disposition in §3 (copies hold no secrets; the threat is influence, which this closes).

### [ON HOLD — §2.4] AD-4 Source registry trust and resource bounds

**Decision.** Read `Contents/Resources/services/*.rdb` through a directory-confined handle: regular files only (no links, no special files); each ≤ **2 MiB**, total ≤ **3 MiB**, at most 32 files; bundle real path not in the protected inventory; every `component` location stays inside the installation (LibreOffice expand-macro locations relative to the running install, or a file location inside the bundle's real path). Preparation runs only inside `convert_document`, **one at a time process-wide** (excess calls wait, bounded by the call timeout). Peak extra heap during one preparation ≤ 8 MiB (Hard Constraint #3), measured by T-A10m with the largest accepted input. Copies are written read-only. One audit event per preparation: converter path, bundle real path, generation digest, scope (success) or a reason code (failure), never contents. Whether the stock bundle fits the caps and the location rule is decided by T-A12 on the real bundle **before** GREEN (reviewer A2); if it does not, team-lead escalates instead of widening silently.

### [ON HOLD — §2.4] AD-5 Output verification — never trust the exit code (R1-FQ2 research)

**Decision.** LibreOffice writes into a fresh temporary folder inside the per-turn document cache. Success requires all of: the expected file `<stem>.pdf` exists; it is a regular file; size > 0; modified after the launch; first bytes `%PDF-`; the last 1 KiB contains `%%EOF`. Only then is it moved into `outdir` (replacing a same-named file). Otherwise the tool fails and reports the exit code, whether a file appeared, and the last 2 KiB of stderr passed through the sensitive-data filter. A timeout kills the whole process group and reports "timed out". A registry preparation failure refuses **before** launch with the observed reason and the repair step (no "will abort with signal 6" prediction).

### [ON HOLD — §2.4] AD-6 Platforms and modes

**Decision.** The tool exists on macOS, Linux and Windows with identical arguments. The registry step runs only on macOS for a recognised bundle. Spawn uses the turn's existing mode (God Mode gets the same floor bash gets — A-2). Linux and Windows bash composition and kernel rules stay byte-identical (T-A16).

### AD-7 #882 narrow fix: stand-alone quoted literals that cannot exist (founder R1-FQ3 "A"; R2-MAJ-001)

**Context.** ADR-068 §9.3: judge the path the shell will actually open; where unknown, refuse. The scanner yields fragments (`/Us` from `/Us*/…`; `/tm` from `'/tm' 'p/x'`), and templates/concatenations can become existing paths at run time.

**Decision.** In the outside-working-folder branch, before refusing, the guard does **not** refuse a candidate only when **all six** hold:
1. The candidate is the **entire** content between one matching pair of quotes in the command text (the byte before is `'` or `"`, the byte after is the same quote).
2. The candidate contains none of `* ? [ ] { } % $ ` ` ~ \` and no `..` path segment.
3. It is not expansion-derived (existing rule).
4. Its first component below the filesystem root does not exist.
5. The root is not writable for the gateway's effective user (Unix effective-access check via `golang.org/x/sys/unix`; Windows always "writable" → rule off).
6. **The literal stands alone:** skipping all whitespace including newlines, the nearest character before the opening quote is one of `( , = [ { :` or the start of the enclosing shell word, and the nearest character after the closing quote is one of `) , ] } ;` or the end of the enclosing shell word. This refuses adjacent literals (`'/tm' 'p/x'`, also across a newline), `+` and `%` concatenation/formatting, method calls (`'/a'.format(...)`, `'/a'.join`), repetition `*`, prefixed literals (`f'…'`, `r'…'`, `b'…'`, `u'…'` — the character before the quote is a letter) and line continuations (`\`).
The protected-internal-file leg, the `../` veto, command-position handling and every other refusal run unchanged and first.

**Consequences.** `'/api/v1/users'` in `print(...)`, `x = '/healthz'`, and heredoc `"api": "/api/v1",` pass on macOS and non-root Linux. Stay refused (recorded limitation; workaround: write the script with `write_file`, then run `python3 gen.py` — the guard scans command text only): f-strings, templates, concatenations, globs, `../`, everything on Windows and for a root-run gateway. No interpreter parsing is added.

### AD-8 `../` veto kept (founder R1-FQ3)

**Decision.** The blanket refusal of command text containing `../` or `..\` stays exactly as today (`shell_path_guard.go::guardCommandClassified`). Recorded limitation: harmless script text such as `'../shared/x'` is refused; workaround as AD-7.

### AD-9 soul for third-party agents — decided, implemented by T8

T8 rev 5.1 D-20 X5: no Omnipus soul for `subagent_3p` on any surface (A-T9). T11 does nothing further.

### AD-10 Descriptors and kind lock — T8

T8 D-20 X3 adds the descriptor rows and A-T30. T11's frontend consumes them.

### AD-11 Candidate validation: person-only gate and shared program-file check, both in Slice A (R1-CRIT, R2-Q2)

**Decision.** Slice A builds two shared pieces that T8 reuses:

| Function / file | Behaviour |
|---|---|
| `pkg/gateway/middleware/person_session.go::RequirePersonSession(next http.HandlerFunc) http.HandlerFunc` | Beside `bypass_gate.go::RequireNotBypass` (Verified), using `pkg/gateway/ctxkey` (Verified import there). Refuses with 403, before reading the body: development-bypass requests; requests carrying `ctxkey.CLITokenContextKey`; requests with no user under `ctxkey.UserContextKey`. Used as `a.withAuth(middleware.RequirePersonSession(h))`. |
| `pkg/clitrust/check.go::CheckProgramPath(path string, forbidden []string) (resolved string, err error)` | New leaf package. Absolute path required; resolves the link chain one link at a time; refuses if the final file **or any link or any link's directory** lies inside a forbidden root, is writable by group/others (Unix mode bits) or by the effective user's peers on Windows (ACL check — S-11), or if the final file is not a regular file executable by the effective user (`faccessat` on Unix; admitted extension `.exe`/`.cmd`/`.bat` per S-11, no 8.3 short names, no alternate data streams on Windows). Returns the resolved file, which is the file callers launch (no second resolution). Platform files `check_unix.go`, `check_windows.go`. |
| `pkg/clitrust/roots.go::AgentWritableRoots(home string, cfg *config.Config) []string` | `$OMNIPUS_HOME`, every workspace folder and every workspace mount root — the forbidden list callers pass to `CheckProgramPath`. |

`POST /system/cli-validate` (Slice A): `RequirePersonSession` → `CheckProgramPath` (refusal → reason `path-not-allowed`, zero spawn) → existing handshake on the returned resolved file; existing rate limiter, in-flight cap, scrubbed environment and audit `{cli, user, requested_path, resolved_path, reason}` unchanged. T8 reuses `RequirePersonSession` for `GET`/`PUT /settings/third-party-agents` and `CheckProgramPath` + `AgentWritableRoots` for the Settings save and every spawn re-check. ADR-030 §11 amendment in §5.

### AD-12 One diagnostic snapshot with an authoritative workspace (MAJ-006, R2-MAJ-003, R2-MAJ-006)

**Decision.** Connection checks and smoke tests (REST or tool) build one snapshot at start:

| Field | Saved mode (`agent_id`) | Draft mode (no `agent_id`) |
|---|---|---|
| CLI | the agent's locked `cli` | request `cli` |
| Program | saved preset path for that CLI; none → `no-settings-path` (CH-2) | same |
| Parameters | driver flags → agent flags (filtered) → preset parameters (unfiltered, last) | driver flags → request `cli_args` (filtered) → preset parameters |
| Model | the agent's | request `model` or none |
| Environment | scrubbed runner base + agent `env_overrides` (protected names dropped as at dispatch) | scrubbed runner base only |
| Workspace | request `workspace_id` (REST) or the calling turn's workspace (tool); validated with dispatch's resolver — not a member → `not-a-member`; none given and exactly one membership → that one; several → 400 `workspace_required` listing them | owned scratch folder created and removed by the call |
| Turn cap | smoke's fixed small cap | same |
| Principal | person (REST) or calling agent (tool) | same |

Saved mode rejects request `cli`, `model`, `cli_args`, `max_tool_iterations` (400 `diagnostic_field_conflict`). **Settings → Check version is not a diagnostic mode**: it is candidate validation of the typed path (AD-11) and its result belongs to that typed path only; it saves nothing. Limiter: the existing smoke limiter instance, keyed `person:<username>` (REST) or `agent:<calling agent id>` (tool), plus one smoke run per target agent at a time. A configuration change during a run affects the next run only. Stopping the calling turn cancels the run. Audit: principal, target, workspace, mode, outcome — never values.

### AD-13 Truthful outcomes (MAJ-007, R2-MAJ-007)

**Decision.** Connection mode: `program_runs`, `credentials` (`found`/`not_found`/`unknown`, from the snapshot environment including credential directories such as a `CODEX_HOME` override — `codex_auth.go::the runner's Codex credential-path resolver` gains an environment input, E32), `authentication: not_verified`; `ok` = program runs and credentials found. Smoke mode: `outcome` and `authentication` are separate:

| `outcome` | `authentication` | When | UI text |
|---|---|---|---|
| `succeeded` | `verified` | the bounded prompt returned | "Sign-in confirmed by test" |
| `auth_rejected` | `rejected` | the driver classifies the CLI's error as an authentication error (existing per-CLI error classification, as described in `ExecutorSmokeTestResponse.yaml`) | "Sign-in rejected — replace the key" |
| `could_not_complete` | `not_verified` | start failure, provider unavailable, network error, unclassified CLI error | "Test could not complete: <reason>" |
| `timed_out` | `not_verified` | bounded timeout | "Test timed out" |
| `cancelled` | `not_verified` | caller stopped or navigated away | "Test cancelled" |

### AD-14 Settings → Third-party agents: one preset row per CLI (R1-FQ1, F-10, R1-FQ6, R2-FQA, R2-Q3)

**Decision.** Three rows (Claude Code, Codex, opencode). Each row: saved program path or "Not set"; detection result (filesystem-only `GET /system/cli-detect`); path field pre-filled from detection only when nothing is saved and the field is untouched; **parameters** — a free-text field, any CLI parameter, shown in full, no warning; **Check version** of the typed path (AD-11); **Save**; **Clear**. Save/Clear send `PUT /settings/third-party-agents` built from the last saved server state plus only that row's change, with `revision`; 409 shows "Changed elsewhere — reload" with the draft kept. Signed-in person only (`RequirePersonSession`); no password step. Server refusal (`cli_path_invalid`, field = CLI key) shows under that row's field. The agent editor shows the preset (path and parameters) read-only with a link here. Agents can read presets (through T8's `get_agent` effective view) but can never change them.

**Run-time semantics (T8 implements, T11 tests end to end).** Argv = driver flags → agent flags (filtered by `argsafety.go`) → preset parameters (not filtered, applied last). Every run that applies preset parameters writes an audit entry stating in clear "person preset used; flag filter bypassed for preset parameters", with the parameter text passed through the sensitive-data filter.

**Real exposure — founder's accepted risk (R2-Q3).** Omnipus does not sandbox external CLIs: the drivers start them directly in their working folder with a scrubbed environment and an egress proxy, not under an Omnipus filesystem confiner (`driver_codex.go::CodexDriver.Run` per reviewer E9; `external_dispatch.go::prepareRunOptions`, Verified). A working folder is not a boundary. Claude Code already runs with its permissions bypassed by the driver (E31). A Codex preset such as `--dangerously-bypass-approvals-and-sandbox` removes Codex's own sandbox; Codex can then read and write anything the operating-system account running Omnipus can. The founder accepted this with these facts ("A: Allow, I accept the risk"); no UI warning (R2-FQA). Recorded in the ADR-032 amendment (§5) and in `docs/security.md`.

### AD-15 Credential repair is reachable (MAJ-008)

**Decision.** Remove the editor's pre-save `testAgentRunner` gate (E30). Saving env values or flags never depends on a diagnostic. After a save that touched env or flags, the Runtime tab runs a connection check automatically and shows its status as non-blocking information, with **Run test** offered.

### AD-16 Chat tool `test_agent_runner`

**Decision.** Inputs `agent` (id or exact name), `mode` = `connection` (default) or `smoke`, optional `workspace_id` (default: the calling turn's workspace). Uses AD-12/AD-13/AD-18. Policy as `test_provider` (E23): ceiling `allow`, `AutoAsks` class, Omnipus supported set, explicit deny for other seeded roles. No new approval step (F-5). It runs only the saved preset program for the agent's CLI.

### AD-17 Secret-sensitive settings

**Decision.** All `env_overrides` values are secret-sensitive and handled by T8 (masked read-backs, merge plus remove, redaction). Agent flags and preset parameters are audited through the sensitive-data filter. The Runtime-tab editor shows names with a fixed mask, sends only changed names and explicit removals, and never sends a mask as a value. Encrypted private variables for CLIs are out (R1-FQ4).

### AD-18 Diagnostic result identity (R2-MAJ-008)

**Decision.** Every diagnostic response carries `agent_revision` (saved mode) and `settings_revision` — non-secret identities of the configuration that was tested. The Runtime tab keeps the revisions it last loaded. A response is shown as the **current** status and announced only when both match; otherwise it is shown as "Result for an earlier configuration" (not announced) or dropped if a newer result for the current revisions exists. Responses arriving after the tab is closed or navigated away are ignored; leaving cancels a running smoke test. States: idle, checking, current result, stale result, cancelled, failed-to-run.

## 8. User Stories & Acceptance Criteria

### [ON HOLD — §2.4] User Story 1 — Convert PowerPoint to PDF inside the macOS sandbox (P0)

An agent on a Mac that installed LibreOffice into its workspace converts a deck to PDF and checks it, with the sandbox unchanged and without a person converting by hand.

**Why this priority:** founder priority 1; document work on the Mac is blocked today.
**Independent Test:** fresh macOS instance; agent installs LibreOffice, calls `convert_document` on a 5-slide deck; the PDF has 5 pages; the profile still allows two system services.

1. **Given** LibreOffice installed as an app bundle through environment_setup, **When** the agent converts a 5-slide deck, **Then** a verified PDF is written and the result names the LibreOffice used.
2. **Given** a Homebrew-style wrapper first on the path, **When** the agent converts, **Then** the bundle the wrapper names is used and the PDF is produced.
3. **Given** a LibreOffice whose component list cannot be safely copied, **When** the agent converts, **Then** the conversion is refused before LibreOffice starts, with the reason and how to repair.
4. **Given** LibreOffice exits 0 but writes no file, **When** the agent converts, **Then** the conversion is reported as failed.
5. **Given** two workspaces with different LibreOffice installs, **When** both convert at once, **Then** each uses only its own copy.
6. **Given** an agent runs `soffice` directly in bash on macOS and it crashes, **When** the result returns, **Then** it carries a hint to use `convert_document`.

### [ON HOLD — §2.4] User Story 2 — The sandbox boundary does not move (P0)

1. **Given** any conversion, **When** its sandbox profile is rendered, **Then** it allows the same two system services as before.
2. **Given** a registry copy, **When** a conversion child runs, **Then** it can read its own copy but cannot create, change, delete or rename any copy.
3. **Given** a LibreOffice whose component list links outside the installation or into protected files, **When** preparation runs, **Then** it is refused and nothing is copied.
4. **Given** the gateway already has a value for the converter pointer in its own environment, **When** a conversion runs, **Then** the gateway environment is the same afterwards and the child received only the prepared value.

### User Story 3 — Plain quoted routes in scripts are not refused (P1)

1. **Given** `python3 -c "print('/api/v1/users')"` on macOS or non-root Linux, **When** the guard checks it, **Then** it is not refused.
2. **Given** a write to `/etc/<name>`, `~/<name>`, `/tmp/<name>` or a sibling of the working folder, **When** the guard checks it, **Then** it is refused exactly as today.
3. **Given** `'/Us*/x'`, `f'{base}/api'`, `'/%s/%d'` or `'../shared/x'`, **When** the guard checks it, **Then** it is refused exactly as today.
4. **Given** Windows or a root-run gateway, **When** the guard checks `'/api/v1/users'`, **Then** it is refused as today and the documentation says so.
5. **Given** `open('/tm' 'p/x','w')`, `'/a' + 'b'` or `'/a'.format(x)`, **When** the guard checks it, **Then** it is refused exactly as today.

### User Story 4 — A person edits a third-party worker's runtime (P1)

1. **Given** a custom Codex worker, **When** the person opens Runtime, **Then** environment values and extra flags are editable, the CLI is shown locked, and the Settings preset (path and standard parameters) is shown read-only with a link.
2. **Given** a worker with no working key, **When** the person adds the key and saves, **Then** it is saved even though sign-in had failed before, and a non-blocking status follows.
3. **Given** three saved variables, **When** the person replaces one and removes another, **Then** the third keeps its stored value and no mask is ever sent as a value.
4. **Given** no Settings path for the worker's CLI, **When** the person opens Runtime, **Then** the preset line reads "Not set — set it in Settings → Third-party agents".

### User Story 5 — Truthful sign-in check and test, by screen and by chat (P1)

1. **Given** a worker whose key is set only in its own environment values, **When** a connection check runs, **Then** it reports program runs, credentials found, sign-in not tested yet.
2. **Given** a non-empty but invalid key, **When** the smoke test runs, **Then** it reports "Sign-in rejected".
3. **Given** a saved-agent smoke request that also supplies a different CLI, **When** it arrives, **Then** it is refused 400.
4. **Given** Omnipus is asked "is the Codex worker signed in?", **When** it uses `test_agent_runner`, **Then** the answer matches the Runtime tab's status.
5. **Given** five smoke tests in a minute by the same person, **When** a sixth arrives, **Then** it is refused with the existing rate-limit reason.
6. **Given** valid credentials but the provider is unreachable, **When** the smoke test runs, **Then** it reports "Test could not complete", not a sign-in failure.
7. **Given** a worker in workspaces A and B, **When** a check runs without naming a workspace, **Then** it is refused asking which workspace; naming B runs it in B's folder.
8. **Given** a smoke test started before the person saved a new key, **When** its result arrives after the new key's check, **Then** it is shown as a result for an earlier configuration and not announced.

### User Story 6 — Omnipus changes a worker's settings by chat (P1, delivered by T8)

1. **Given** Omnipus set `DEBUG=1` and a flag by chat, **When** the person opens Runtime, **Then** the flag and the masked name show.

### User Story 7 — A person sets each CLI's preset in Settings (P1)

1. **Given** a fresh instance with `claude` and `codex` installed, **When** the person opens Settings → Third-party agents, **Then** each row shows "Not set", what detection found, and a pre-filled path; nothing is saved.
2. **Given** the Codex row, **When** the person adds `--some-standard-flag` and clicks Save, **Then** only Codex's preset changes and Claude's saved preset is kept.
3. **Given** a path inside a workspace folder, **When** Save is clicked, **Then** it is refused with the reason under the field.
4. **Given** another tab saved a change first, **When** this tab saves, **Then** a conflict is shown and this tab's draft is kept.
5. **Given** detection runs, **When** it finishes, **Then** no program was started and a typed draft was not overwritten.
6. **Given** saved path A and typed path B, **When** the person clicks Check version, **Then** B is checked (person-only, shared file check) and nothing is saved.
7. **Given** the Codex row, **When** the person types `--dangerously-bypass-approvals-and-sandbox` as a parameter and saves, **Then** it is saved and shown in full with no warning, and later runs carry it last with an audit note.

### User Story 8 — No regression on Linux and Windows (P0)

1. **Given** Linux or Windows, **When** a bash command runs, **Then** its environment and kernel rules are identical to before.
2. **Given** Windows, **When** `convert_document` runs with a fake converter, **Then** the output verification behaves as on macOS (no registry step).

### User Story 9 — Only a person can make Omnipus run a candidate program (P0, CRIT-001)

1. **Given** a CLI-token or development-bypass request, **When** it calls candidate validation, **Then** it is refused 403 and nothing runs.
2. **Given** a signed-in person and a candidate inside a workspace folder (or behind a link into one), **When** they check its version, **Then** it is refused `path-not-allowed` and nothing runs — in Slice A already.
3. **Given** a signed-in person and an allowed candidate, **When** they click Check version, **Then** the version is shown and an audit entry names the person.

## 9. Behavioral Contract, Edge Cases, Safeguards

### 9.1 Behavioral contract

- [ON HOLD — §2.4] Converter behaviour (AD-1…AD-6).
- When command text holds a stand-alone quoted absolute literal whose top folder cannot exist or be created, the guard does not refuse it; everything else, including adjacent or concatenated strings, is judged as today.
- When anyone but a signed-in person calls candidate validation, the system refuses before reading the request.
- When a person checks a candidate, the system applies the save-time checks before running it.
- When a check or test runs for a saved agent, the system uses only that agent's saved configuration and the Settings preset.
- When a person saves env values or flags, the save never waits on a sign-in check.
- When a person saves one Settings row, the system changes only that row, or reports a conflict.
- When a run uses a preset, preset parameters are applied last without filtering and the audit says so; agent flags stay filtered.
- When a diagnostic result belongs to an older configuration, the system does not present it as current.
- When a diagnostic names no workspace and the agent has several, the system asks which.

### 9.2 Edge cases

| Case | Expected |
|---|---|
| [ON HOLD] No LibreOffice anywhere | `convert_document` fails: "no LibreOffice found; install it with environment_setup" |
| [ON HOLD] Only `/Applications/LibreOffice.app` (not on PATH) | Selected by the macOS fallback |
| [ON HOLD] Two installs; agent passes `converter` | That one is used; result names it |
| [ON HOLD] Agent's command changes PATH (bash) | Irrelevant: conversion does not go through bash |
| [ON HOLD] Output file of the same name already in `outdir` | Replaced only after the new file verifies |
| [ON HOLD] LibreOffice hangs | Process group killed at timeout; "timed out" |
| [ON HOLD] Non-ASCII or spaced paths | Supported (Dataset C) |
| [ON HOLD] Same-scope concurrent conversions | Serialised per workspace; preparation one at a time process-wide |
| [ON HOLD] Registry ≥ caps | Refused with "registry too large"; T-A12 decides stock fit |
| `'/api/v1'` inside a longer string `'see /api/v1 docs'` | Not the entire quoted content → refused as today |
| Detection slow; person types meanwhile | Late detection never replaces a touched field |
| Settings PUT 409 | Row shows conflict; draft kept; Reload offered |
| Auth expires while editing Settings | Save fails with "Signed out — sign in again"; draft kept in the page |
| Save times out (unknown outcome) | Row shows "Not confirmed" with Retry and Reload; no optimistic state |
| Smoke test while config changes | Uses the start snapshot; result shown as earlier-configuration (AD-18) |
| `'/tm' 'p/x'` adjacent literals | Refused (condition 6) |
| Worker with no workspace membership | Diagnostic refused `not-a-member`, as dispatch refuses |
| Preset parameter duplicates an agent flag | Preset applied last; the CLI's own last-wins behaviour applies; preview shows both |

### 9.3 Qualitative prohibitions

- The system must not add any Mach service, network permission or general file permission to the sandbox.
- [ON HOLD — §2.4] Converter prohibitions (no bash injection, never trust the exit code, no unprotected retry).
- The guard must not exempt fragments, adjacent or concatenated strings, templates, prefixed literals, globs, escapes or `../`.
- The system must not run any candidate program for a non-person caller, nor before the save-time checks.
- The system must not add an approval step for env, flags or rate limits (F-5), a password step for presets (R1-FQ6), or a UI warning for preset parameters (R2-FQA).
- No agent surface may change a preset (path or parameters).
- The system must not describe a working folder as a boundary for external CLIs.
- The Runtime editor must not block a save on a diagnostic, nor send a mask as a value.
- A Settings row save must not send other rows' unsaved drafts.
- Connection mode must not claim "signed in".

### 9.4 Machine-verifiable constraints

| # | Constraint |
|---|---|
| MV-1 | Rendered macOS profile allows exactly two `mach-lookup` global names (existing test unchanged). |
| MV-2 | [ON HOLD] A macOS conversion child's environment contains exactly one `URE_MORE_SERVICES` when preparation succeeded; no child is launched when it failed. |
| MV-3 | [ON HOLD] Linux/Windows bash composition byte-identical before/after (golden). |
| MV-4 | [ON HOLD] The conversion child has exactly one read-without-write rule for its generation; create/overwrite/delete/rename in any generation folder fails in a real Seatbelt child. |
| MV-5 | [ON HOLD] Preparation refuses: linked/special `.rdb`, > 2 MiB file, > 3 MiB total, > 32 files, component outside the installation, bundle in protected inventory. |
| MV-6 | [ON HOLD] Peak extra heap during one preparation with the largest accepted input ≤ 8 MiB. |
| MV-7 | [ON HOLD] `convert_document` reports success only when the output passes all AD-5 checks. |
| MV-8 | Dataset G expectations hold on macOS/Linux; controls refused on all platforms. |
| MV-9 | `cli-validate` (Slice A): CLI-token and bypass requests → 403 with zero spawns; disallowed candidate (inside a forbidden root, link chain into one, others-writable) → `path-not-allowed` with zero spawns; an accepted candidate launches exactly the returned resolved file. |
| MV-10 | Saved-mode smoke/preview requests with `cli`, `model` or `cli_args` → 400 `diagnostic_field_conflict`. |
| MV-11 | Connection mode makes zero model requests and never returns `authentication: verified`. |
| MV-12 | Dataset S values appear in no result, audit, log capture, transcript or HTTP/tool response. |
| MV-13 | Settings row Save request body equals last-saved state plus that row's change, with `revision`. |
| MV-14 | The Runtime editor issues no runner-test request before `updateAgent`. |
| MV-15 | A diagnostic response whose `agent_revision` or `settings_revision` differs from the loaded ones is never rendered in the current-status slot nor announced. |
| MV-16 | A run with preset parameters has argv order driver → agent (filtered) → preset (unfiltered) and exactly one audit entry stating the preset use and filter bypass. |
| MV-17 | Saved-mode diagnostic for an agent with two memberships and no `workspace_id` → 400 `workspace_required`; with `workspace_id` → child's working directory is that workspace's folder. |

## 10. Integration Boundaries

| System | Data | Contract | Failure behaviour | Development approach |
|---|---|---|---|---|
| [ON HOLD] LibreOffice (macOS bundle; Linux/Windows install) | Reads `services/*.rdb`; launches `soffice --headless --convert-to pdf` | Bootstrap reads `URE_MORE_SERVICES` from the environment (A-1) | Refusal before launch; output verification | Fixture bundle + fake converter (Go-built, all OSes); real bundle on the dev Mac and in UAT |
| [ON HOLD] Seatbelt | Profile per child | Unchanged allow-set | Spawn refused as today | Real child on the dev Mac (narrow local run) and hands-on |
| environment_setup workspace env | `<ws>/.omnipus/env/bin` | Existing | Existing notices | Fresh UAT instance |
| Third-party CLIs | Version, credential presence, bounded prompt | Existing drivers | Reasons per C-B3 | Fake CLI (Go-built) emitting Dataset S through every sink |
| T8 settings, checks, merge, masking | `ThirdPartyAgentSettings`, check function, `env_overrides_remove` | T8 rev 5.1 (+CH-1…CH-4) | Not landed → Slice B blocked | Mocks against T8's contract commit |

## 11. Reachability (Definition of Done)

| Capability | Who | Registration / policy (Hard Constraint #6) | Screen | Check |
|---|---|---|---|---|
| [ON HOLD — §2.4] `convert_document` | (policy undecided — R2-CRIT-001) Any agent allowed to write files (Mia, General Purpose, custom) | New builtin, registered for every agent; ceiling `allow` in `pkg/config/defaults.go`; `AutoRuns` class; explicit `deny` seeded for every built-in role whose `write_file` resolves to `deny`; not in Omnipus's supported set (Omnipus gets its standard deny); label in `src/lib/humanizeToolName.ts` | Chat tool card; PDF in the workspace Library | `grep -rl '"convert_document"' pkg/coreagent/ pkg/config/ pkg/tools/` ≥ 3 files; T-A50; U-02 |
| Guard narrow rule | Same as `bash` | Existing `bash` | Chat transcript | U-04/U-05 |
| Person-only, checked candidate validation | Person (existing editor hint in Slice A; Settings row Check version in Slice B) | Existing route + `RequirePersonSession` + `clitrust.CheckProgramPath` | CLI-path hint; Settings row | T-A40…T-A46, U-11, U-12 |
| `test_agent_runner` | Omnipus by chat | Ceiling `allow`, `AutoAsks`, Omnipus supported set, deny elsewhere; label and Ask group in `src/lib/humanizeToolName.ts`, `src/lib/autoApproveAskGroups.ts` | Chat tool card | `grep -rl '"test_agent_runner"' …` ≥ 3 files; T-B10; U-09 |
| Runtime tab | Person: Workspace → Team → worker → Runtime (agents are reached through the Workspace Team, `src/components/agents/CLAUDE.md`) | REST agent update (T8 merge contract) | `AgentProfile.tsx` runtime panel | E-1, U-07 |
| Settings → Third-party agents | Person | T8 routes; person-only middleware | New `src/components/settings/ThirdPartyAgentsSection.tsx` in the Settings navigation | E-2, U-12 |

Delivery is reported in two separate lines: *code correct and tested* and *reachable by a user/agent*.

## 12. Screens: states, journeys, accessibility, components

### 12.1 State and transition matrices

**Settings → Third-party agents, per CLI row**

| State | Shown | Allowed actions | Next |
|---|---|---|---|
| Loading (settings) | Skeleton per row | none | Loaded / Load error |
| Load error | QueryErrorState with Retry | Retry | Loading |
| Loaded, not set, detection pending | "Not set"; "Looking for <CLI>…"; empty editable field | Type, Save (if typed) | Detected / Not found |
| Detected, field untouched | "Found at <path> (on your PATH / in a common install folder)"; field pre-filled | Edit, Check version, Save | Saving / Validating |
| Detection failed | "Detection unavailable" + Retry; field editable | Retry, type, Save | — |
| Field touched | Draft shown; late detection results do not change it | Save, Clear draft, Check version | Saving |
| Validating (Check version of the **typed** path) | Button busy; row live region "Checking <path>…" | Cancel | Version shown for that path / `path-not-allowed` (reason) / handshake failed; result cleared if the field is edited afterwards; nothing saved |
| Parameters field | Free text, full value visible (wraps), no warning | Edit, Save | Saving |
| Saving | Save busy; other rows stay editable | none on this row | Saved / Refused / Conflict / Signed out / Not confirmed |
| Saved | Saved path and parameters; "Saved" announced | Edit, Clear | — |
| Refused (`cli_path_invalid`) | Error text under the field (which check failed) | Edit, Save | Saving |
| Conflict (409) | "Changed elsewhere" with Reload and Reapply my change | Reload, Reapply | Loaded / Saving |
| Signed out (401) | "Signed out — sign in again"; draft kept | Sign in | Saving |
| Not confirmed (timeout/network) | "Save not confirmed" with Retry and Reload | Retry, Reload | Saving / Loaded |
| Clearing | as Saving | — | "Not set" |

**Runtime tab (third-party worker)**

| State | Shown | Notes |
|---|---|---|
| Preset line | Settings path + standard parameters, read-only, link | "Not set — set it in Settings → Third-party agents" when empty |
| Env editor | One row per name: name, fixed mask, Replace, Remove; Add variable | Replace/Remove/Add are drafts until autosave; failed save restores the row and announces the error |
| Flags | Input + `dropped_at_run` list from the server | — |
| Save | Existing autosave; never waits on a diagnostic | Conflict and refusal handling as today |
| Diagnostics | Idle → Checking → current result ("Program runs · Credentials found · Sign-in not tested yet" / not found / unknown) after a save touching env/flags; Run test → smoke outcome per AD-13 (confirmed / rejected / could not complete / timed out / cancelled) | Non-blocking. Results whose revisions differ from the loaded ones show as "Result for an earlier configuration" and are not announced (AD-18); leaving the tab cancels a running test |
| Workspace choice | When the worker belongs to several workspaces, a workspace selector (defaults to the workspace the person came from via Team) precedes Run test | Diagnostic carries `workspace_id` |
| Native agent | Runtime read-only with the server's reason | — |

### 12.2 Journeys

| ID | Journey |
|---|---|
| J-1 | First setup: Settings → Third-party agents → Save detected Codex preset (optionally add standard parameters) → ask Omnipus to create a Codex worker → Workspace → Team → worker → Runtime shows the preset read-only. |
| J-2 | Repair: Workspace → Team → worker → Runtime shows "Credentials not found" → Add variable `OPENAI_API_KEY` → autosave → status "Credentials found · Sign-in not tested yet" → Run test → "Sign-in failed: invalid key" → Replace value → autosave → Run test → "Sign-in confirmed by test" → reload page: name present, value masked. |
| J-3 | Path problem: Runtime shows "Not set" → link to Settings → save path → browser Back returns to the same worker's Runtime with the preset now shown. |

### 12.3 Accessibility and keyboard contract

| Item | Requirement |
|---|---|
| Names | Every control has a visible label and a CLI-qualified accessible name ("Save Codex preset", "Codex parameters", "Check version of typed Codex path", "Clear Claude Code preset", "Remove variable OPENAI_API_KEY") |
| Keyboard | Every action reachable and operable with Tab/Shift+Tab, Enter and Space; no keyboard trap |
| Busy | Busy buttons expose `aria-busy` and are disabled without losing focus |
| Announcements | One polite live region per row/section announces: saved, refused (with reason), conflict, signed out, check result, current diagnostic result — never a stale one |
| Focus after actions | Replace → focus the value input; Remove → focus the next row's name, or Add variable if none; Save refused → focus the field with the error; Conflict → focus Reload; returning from Settings via the link → focus the Runtime preset line |
| Focus style | Shared focus-visible rule of the design system (`omnipus-design-system` skill, "Focus") |
| Touch and reflow | Touch targets per the design system; usable at 320 px width and 200% zoom; long paths wrap or truncate with full text available to assistive technology |
| Chrome | No emoji in UI chrome |

### 12.4 Components (catalogue first, MIN-001)

| Need | Catalogue component (`design-system/catalog.json`, per review E15) |
|---|---|
| Row actions (Save, Clear, Check version, Run test, Replace, Remove, Add) | Button, IconButton |
| Fields | Field, Input |
| Loading | Skeleton |
| Load/detection error | QueryErrorState |
| Not set / no variables | EmptyState |
| Clear confirmation | ConfirmDialog |

No new shared component and no new visual design; Sovereign Deep tokens apply. Gates: `npm run typecheck`, `npm run lint:design-system-locks`, the component's vitest files.

## 13. BDD Scenarios

Format: Traces to · Category; Given / When / Then. One action per When.

### Feature: Converter-bound conversion (#1489) — [ON HOLD — §2.4]: BDD-01…BDD-09, BDD-08b, SEC-01…SEC-07 are reference only

#### Scenario BDD-01: Convert with an agent-installed app bundle
**Traces to**: US-1 AS-1 · **Category**: Happy Path
- **Given** macOS, Seatbelt active, LibreOffice copied by environment_setup into the workspace environment with `bin/soffice` linking into the bundle
- **When** the agent calls `convert_document` on a 5-slide deck
- **Then** `deck.pdf` exists, starts with `%PDF-`, ends with `%%EOF`, has 5 pages (checked by the test with a PDF page counter), and the result names that bundle

#### Scenario BDD-02: The document skill uses the tool
**Traces to**: US-1 AS-1 · **Category**: Alternate Path
- **Given** the elicify-pptx render guidance after this change
- **When** an agent follows it to render the final deck
- **Then** it calls `convert_document` (never `soffice` through bash) and checks the returned PDF

#### Scenario BDD-03: Homebrew-style wrapper
**Traces to**: US-1 AS-2 · **Category**: Alternate Path
- **Given** the first `soffice` on the path is a wrapper whose only command is `exec '<bundle>/Contents/MacOS/soffice' "$@"`
- **When** the agent converts
- **Then** the bundle binary is launched and the PDF verifies

#### Scenario Outline BDD-04: Converter selection
**Traces to**: US-1 AS-1/AS-2 · **Category**: Edge Case
- **Given** `<setup>`
- **When** the agent converts without `converter`
- **Then** the outcome is `<outcome>`

| setup | outcome |
|---|---|
| bundle reached through a two-link symlink chain | that bundle used |
| wrapper with an extra `export X=1` line (macOS) | refused: unsupported layout |
| flat fixture install (macOS) | refused: unsupported layout |
| no `soffice` on the path; `/Applications/LibreOffice.app` present | `/Applications` bundle used |
| no LibreOffice anywhere | refused: no LibreOffice found |
| two bundles, agent passes `converter` = the second | the second used; registry digest matches the second |

#### Scenario BDD-05: Preparation failure refuses before launch
**Traces to**: US-1 AS-3 · **Category**: Error Path
- **Given** a bundle whose `services.rdb` has two MacOSXSpellChecker registrations
- **When** the agent converts
- **Then** the result is a failure naming the converter and "registry could not be prepared: duplicate spell-checker registration" with the repair step
- **But** no LibreOffice process was started

#### Scenario BDD-06: Two workspaces at once
**Traces to**: US-1 AS-5 · **Category**: Edge Case
- **Given** workspaces A and B with different bundles
- **When** both convert concurrently
- **Then** each child's pointer names a generation under its own scope and both PDFs verify

#### Scenario BDD-07: Linux and Windows bash unchanged
**Traces to**: US-8 AS-1 · **Category**: Edge Case
- **Given** identical inputs on `linux` and `windows`
- **When** the bash child environment and kernel rules are composed
- **Then** they equal the pre-change golden output

#### Scenario BDD-08: Exit code 0 without output is a failure
**Traces to**: US-1 AS-4 · **Category**: Error Path
- **Given** a fake converter that exits 0 and writes nothing
- **When** the agent converts
- **Then** the result is a failure "no output file was produced (exit code 0)"

#### Scenario Outline BDD-08b: Output verification
**Traces to**: US-1 AS-4 · **Category**: Error Path
- **Given** a fake converter that `<behaviour>`
- **When** the agent converts
- **Then** the result is `<result>`

| behaviour | result |
|---|---|
| writes an empty file, exit 0 | failure: empty output |
| writes `hello` without `%PDF-`, exit 0 | failure: not a PDF |
| writes a valid PDF, exit 1 | success (file verifies), exit code reported |
| leaves an older `deck.pdf` untouched in `outdir`, writes nothing | failure; the old file is not reported as output |
| sleeps past the timeout | failure: timed out; no process left |

#### Scenario BDD-09: Bash crash hint
**Traces to**: US-1 AS-6 · **Category**: Alternate Path
- **Given** macOS and a bash command whose output contains `Fatal exception: Signal 6` and mentions `soffice`
- **When** the result returns
- **Then** it ends with "LibreOffice cannot run directly inside the macOS sandbox; use convert_document"

#### Scenario SEC-01: Profile allow-set unchanged
**Traces to**: US-2 AS-1 · **Category**: Edge Case
- **Given** a conversion child
- **When** its profile is rendered
- **Then** exactly mDNSResponder and trustd.agent are allowed `mach-lookup`; LaunchServices is not

#### Scenario SEC-02: No child can change any copy
**Traces to**: US-2 AS-2 · **Category**: Error Path
- **Given** a real Seatbelt child started for scope A, and scope B's generation created after it started
- **When** the child tries to create, overwrite, delete or rename files in A's and B's generation folders
- **Then** every attempt fails, while reading its own generation succeeds

#### Scenario Outline SEC-03: Untrusted sources refused
**Traces to**: US-2 AS-3 · **Category**: Error Path
- **Given** a bundle whose services folder has `<defect>`
- **When** preparation runs
- **Then** it is refused with `<reason>` and the store is unchanged

| defect | reason |
|---|---|
| `services.rdb` is a symlink | not a regular file |
| a named-pipe `.rdb` | not a regular file |
| one `.rdb` of 2 MiB + 1 byte | file too large |
| total 3 MiB + 1 byte | registry too large |
| 33 `.rdb` files | too many files |
| a component location `file:///tmp/evil.dylib` | component outside the installation |
| bundle real path inside `$OMNIPUS_HOME/agents/other/` | protected location |

#### Scenario SEC-05: Gateway environment unchanged
**Traces to**: US-2 AS-4 · **Category**: Edge Case
- **Given** the gateway environment already contains `URE_MORE_SERVICES=pre-existing`
- **When** a conversion runs
- **Then** the gateway environment before equals after, and the child saw only the prepared value, once

#### Scenario SEC-06: Audit without contents
**Traces to**: US-2 AS-3 · **Category**: Happy Path
- **Given** a first conversion in a scope
- **When** the copy is prepared
- **Then** one audit event carries converter path, bundle real path, digest and scope, and no registry bytes

#### Scenario SEC-07: Memory bound
**Traces to**: US-2 AS-3 · **Category**: Edge Case
- **Given** the largest accepted registry (3 MiB total)
- **When** two conversions in different scopes request preparation at once
- **Then** preparations run one at a time and peak extra heap stays ≤ 8 MiB

### Feature: Shell guard narrow fix (#882)

#### Scenario Outline BDD-10: Complete quoted literals that cannot exist
**Traces to**: US-3 AS-1 · **Category**: Happy Path
- **Given** macOS or Linux with a non-root gateway user
- **When** the guard checks `<command>`
- **Then** it is not refused

| command |
|---|
| `python3 -c "print('/api/v1/users')"` |
| `python3 -c "x = '/healthz'"` |
| heredoc writing `gen.py` containing `"/api/v1"` |

#### Scenario Outline SEC-10: Real outside writes stay refused
**Traces to**: US-3 AS-2 · **Category**: Error Path
- **Given** any supported platform (expected paths derived per OS)
- **When** the guard checks `<command>`
- **Then** it is refused with the outside-working-folder message

| command |
|---|
| `python3 -c "open('/etc/t11probe','w')"` (Windows: `C:\Windows\t11probe`) |
| `echo x > ~/t11probe` |
| write to a sibling of the working folder |
| `cp a.txt /tmp/t11probe` |

#### Scenario Outline SEC-13: Fragments, templates, globs and traversal stay refused
**Traces to**: US-3 AS-3 · **Category**: Error Path
- **Given** macOS or non-root Linux
- **When** the guard checks `<command>`
- **Then** it is refused exactly as on `b7d4483c9`

| command | why |
|---|---|
| `python3 -c "import glob; open(glob.glob('/Us*/x')[0],'w')"` | glob character after the fragment |
| `python3 -c "print(f'{base}/api/items')"` | not the entire quoted content |
| `python3 -c "print('/%s/%d' % ('a',1))"` | template character |
| `python3 -c "print('/api/[a]')"` | bracket |
| `python3 -c "print('/api\\x2fv1')"` | escape |
| `python3 -c "print('see /api/v1 docs')"` | not the entire quoted content |
| `python3 -c "print('../shared/x')"` | `../` veto |
| `python3 -c "open('/tm' 'p/t11probe','w')"` | adjacent literal (condition 6) |
| `python3 -c "open('/tm'\n 'p/t11probe','w')"` (newline inside parentheses) | adjacent literal across a newline |
| `python3 -c "open('/tm' + 'p/x','w')"` | `+` concatenation |
| `python3 -c "open('/{}'.format('tmp'),'w')"` | brace and method call |
| `python3 -c "open(r'/tmpx','w')"` with `/tmpx` absent | prefix letter before the quote |
| `python3 -c "open('/tm' \\
'p/x','w')"` | line continuation |

#### Scenario SEC-11: Rule off when the root is writable
**Traces to**: US-3 AS-4 · **Category**: Edge Case
- **Given** the root-writable probe answers writable (root user; always on Windows)
- **When** the guard checks `python3 -c "print('/api/v1/users')"`
- **Then** it is refused as today

#### Scenario SEC-12: Expansion and protected refusals unchanged
**Traces to**: US-3 AS-2 · **Category**: Error Path
- **Given** `cat $FOO/api/v1` and `cat '<OMNIPUS_HOME>/credentials.json'`
- **When** the guard checks them
- **Then** the first gets the unresolvable-path refusal and the second the protected-internal-file refusal

### Feature: Runtime tab and credential repair

#### Scenario BDD-20: Runtime editable, preset read-only
**Traces to**: US-4 AS-1 · **Category**: Happy Path
- **Given** a custom Codex worker and a saved Codex preset
- **When** the person opens Runtime
- **Then** env and flags are editable, CLI shows locked, the preset path and standard parameters show read-only with a link

#### Scenario BDD-21: Replace one, remove one, keep one
**Traces to**: US-4 AS-3 · **Category**: Happy Path
- **Given** stored variables A, B, C
- **When** the person replaces A, removes B and autosave runs
- **Then** the request carries only A's new value and `env_overrides_remove: ["B"]`; after reload A and C show masked; C's stored value is unchanged

#### Scenario BDD-22: Saving the first key is never blocked
**Traces to**: US-4 AS-2 · **Category**: Happy Path
- **Given** a worker whose connection check reports credentials not found
- **When** the person adds `OPENAI_API_KEY` and autosave runs
- **Then** the update is sent without any prior runner test, it saves, and a non-blocking status follows

#### Scenario BDD-23: Preset not set
**Traces to**: US-4 AS-4 · **Category**: Edge Case
- **Given** no saved opencode preset
- **When** the person opens an opencode worker's Runtime
- **Then** the preset line reads "Not set — set it in Settings → Third-party agents"

#### Scenario BDD-24: Native worker read-only
**Traces to**: US-4 AS-1 · **Category**: Edge Case
- **Given** a native worker
- **When** the person opens its profile
- **Then** runtime controls are read-only with the server's reason

### Feature: Diagnostics

#### Scenario BDD-30: Credentials found is not "signed in"
**Traces to**: US-5 AS-1 · **Category**: Happy Path
- **Given** a Codex worker with `OPENAI_API_KEY` only in its own env values, gateway without it
- **When** a connection check runs
- **Then** `program_runs=true`, `credentials=found`, `authentication=not_verified`, and no model request was made

#### Scenario BDD-31: Invalid key is rejected
**Traces to**: US-5 AS-2 · **Category**: Error Path
- **Given** a fake CLI that reports an authentication error for the configured key
- **When** the smoke test runs
- **Then** `outcome=auth_rejected`, `authentication=rejected`, UI "Sign-in rejected — replace the key"

#### Scenario Outline BDD-38: Operational failures are not sign-in failures
**Traces to**: US-5 AS-6 · **Category**: Error Path
- **Given** a fake CLI that `<behaviour>`
- **When** the smoke test runs
- **Then** `outcome=<outcome>`, `authentication=not_verified`, and the UI says `<text>`

| behaviour | outcome | text |
|---|---|---|
| cannot start (missing shared library) | could_not_complete | Test could not complete: <reason> |
| reports provider unavailable | could_not_complete | Test could not complete: <reason> |
| hangs past the bound | timed_out | Test timed out |
| is cancelled by the caller | cancelled | Test cancelled |
| returns the prompt's answer | succeeded (`verified`) | Sign-in confirmed by test |

#### Scenario Outline BDD-37: Workspace authority
**Traces to**: US-5 AS-7 · **Category**: Edge Case
- **Given** `<membership>` and request `<workspace>`
- **When** a saved-agent connection check runs
- **Then** `<result>`

| membership | workspace | result |
|---|---|---|
| A and B | none | 400 `workspace_required` naming A and B |
| A and B | B | runs; child's working directory is B's work folder |
| A only | none | runs in A |
| none | none | refused `not-a-member` |
| A | B | refused `not-a-member` |
| A, folder deleted | A | refused as dispatch refuses |

#### Scenario BDD-39: Late result for an older configuration
**Traces to**: US-5 AS-8 · **Category**: Edge Case
- **Given** a smoke test started at agent revision 7; the person saves a new key (revision 8) and its connection check completes
- **When** the revision-7 smoke result arrives
- **Then** it is shown as "Result for an earlier configuration", the live region does not announce it, and the current-status slot keeps revision 8's result

#### Scenario Outline BDD-36: Credential resolution uses the snapshot environment
**Traces to**: US-5 AS-1 · **Category**: Edge Case
- **Given** `<setup>`
- **When** a connection check runs
- **Then** `credentials=<result>`

| setup | result |
|---|---|
| gateway has the key; agent env sets it to empty | not_found |
| agent env `CODEX_HOME` points to a folder with `auth.json` | found |
| agent env `CODEX_HOME` points to an empty folder; gateway default has `auth.json` | not_found |
| no key, no credential file | not_found |

#### Scenario BDD-32: Native agent
**Traces to**: US-5 AS-4 · **Category**: Error Path
- **Given** Mia
- **When** Omnipus tests it
- **Then** the answer says there is no external runner to test

#### Scenario BDD-33: Shared rate limit
**Traces to**: US-5 AS-5 · **Category**: Error Path
- **Given** five smoke tests by the same person in a minute
- **When** a sixth arrives
- **Then** it is refused with the existing rate-limit reason

#### Scenario BDD-34: Chat changes show on the screen (T8)
**Traces to**: US-6 AS-1 · **Category**: Happy Path
- **Given** Omnipus set `DEBUG=1` and a flag by chat
- **When** the person opens Runtime
- **Then** the flag and the masked name `DEBUG` show

#### Scenario SEC-30: Saved-mode conflict
**Traces to**: US-5 AS-3 · **Category**: Error Path
- **Given** a smoke-test or preview request with `agent_id` and `cli`
- **When** it arrives
- **Then** 400 `diagnostic_field_conflict`; nothing spawned

#### Scenario SEC-31: No values in sinks
**Traces to**: US-5 AS-2 · **Category**: Edge Case
- **Given** a fake CLI that prints Dataset S values on stdout, in its version failure and on stderr
- **When** both test modes run via REST and via the tool
- **Then** no Dataset S value appears in responses, tool results, audit, log capture or transcript

#### Scenario SEC-32: No Settings path
**Traces to**: US-5 AS-1 · **Category**: Error Path
- **Given** no saved Codex preset
- **When** a diagnostic runs for a Codex worker
- **Then** reason `no-settings-path` with the repair link text; nothing spawned

### Feature: Settings → Third-party agents

#### Scenario BDD-40: Pre-filled, not saved
**Traces to**: US-7 AS-1 · **Category**: Happy Path
- **Given** a fresh instance with `claude` and `codex` on the PATH
- **When** the person opens the screen
- **Then** Claude Code and Codex rows show "Not set", what was found, and a pre-filled path
- **But** no save request was sent

#### Scenario BDD-41: Saving one row keeps the others
**Traces to**: US-7 AS-2 · **Category**: Happy Path
- **Given** a saved Claude preset and an unsaved draft in the opencode row
- **When** the person saves the Codex row
- **Then** the request equals the last saved state plus the Codex change with `revision`; Claude unchanged; the opencode draft not sent

#### Scenario BDD-42: Clear
**Traces to**: US-7 AS-2 · **Category**: Alternate Path
- **Given** a saved Codex preset
- **When** the person confirms Clear
- **Then** only Codex reads "Not set"

#### Scenario BDD-43: Not found
**Traces to**: US-7 AS-1 · **Category**: Edge Case
- **Given** opencode not installed
- **When** the screen opens
- **Then** the opencode row says "Not found" with an empty editable field

#### Scenario BDD-44: Two tabs
**Traces to**: US-7 AS-4 · **Category**: Error Path
- **Given** tab 1 and tab 2 loaded at the same revision; tab 1 saved Codex
- **When** tab 2 saves Claude
- **Then** tab 2 shows the conflict with Reload and Reapply; its draft is kept; tab 1's change is not lost

#### Scenario BDD-45: Late detection
**Traces to**: US-7 AS-5 · **Category**: Edge Case
- **Given** detection delayed 3 s and the person typed a path in the Codex row
- **When** detection returns
- **Then** the typed path stays; the detected path is shown only as "Found at …"

#### Scenario SEC-40: Disallowed path
**Traces to**: US-7 AS-3 · **Category**: Error Path
- **Given** a path inside a workspace folder
- **When** Save is clicked
- **Then** the server's `cli_path_invalid` reason shows under the Codex field; nothing saved

#### Scenario SEC-41: Detection starts nothing
**Traces to**: US-7 AS-5 · **Category**: Edge Case
- **Given** a fake `codex` that writes a marker file when run
- **When** detection runs
- **Then** no marker file exists

#### Scenario SEC-42: Agents cannot set a path (T8)
**Traces to**: US-7 AS-2 · **Category**: Error Path
- **Given** Omnipus calls `update_agent` with `cli_path`, or `set_config` on `sandbox.third_party_agents`
- **When** the call is validated
- **Then** both are refused

### Feature: Person-only candidate validation (CRIT-001)

#### Scenario SEC-50: CLI token refused
**Traces to**: US-9 AS-1 · **Category**: Error Path
- **Given** a request authenticated with the CLI token
- **When** it calls candidate validation for a fake executable that writes a marker
- **Then** 403; no marker; no audit "spawned" event

#### Scenario SEC-51: Development bypass refused
**Traces to**: US-9 AS-1 · **Category**: Error Path
- **Given** development-bypass mode
- **When** a request calls candidate validation
- **Then** 403; nothing spawned

#### Scenario SEC-52: Candidate inside a workspace refused
**Traces to**: US-9 AS-2 · **Category**: Error Path
- **Given** a signed-in person and a marker-writing executable inside a workspace folder
- **When** they check its version
- **Then** reason `path-not-allowed`; no marker

#### Scenario SEC-53: Link into a workspace refused
**Traces to**: US-9 AS-2 · **Category**: Error Path
- **Given** `/usr/local/bin/codex-x` is a link to a workspace file
- **When** the person checks its version
- **Then** `path-not-allowed`; no marker

#### Scenario BDD-51: Check version checks the typed path
**Traces to**: US-7 AS-6 · **Category**: Alternate Path
- **Given** saved Codex path A and typed path B in the field
- **When** the person clicks Check version
- **Then** B is validated (person gate and shared check), the result is shown for B, and the saved settings are unchanged; with no saved path the same happens

#### Scenario BDD-52: Preset parameters applied last, unfiltered, audited
**Traces to**: US-7 AS-7 · **Category**: Happy Path
- **Given** a saved Codex preset with parameter `--dangerously-bypass-approvals-and-sandbox` and an agent flag on the filter's deny list
- **When** a run starts
- **Then** argv ends with the preset parameter, the agent's denied flag is dropped and reported, the Settings row and preview show the parameter in full with no warning, and one audit entry states "person preset used; flag filter bypassed for preset parameters"

#### Scenario BDD-50: Allowed candidate
**Traces to**: US-9 AS-3 · **Category**: Happy Path
- **Given** a signed-in person and an allowed fake executable
- **When** they check its version
- **Then** the version is returned and the audit entry names the person and the resolved path

### Feature: Accessibility

#### Scenario BDD-60: Keyboard-only repair
**Traces to**: US-4 AS-2 · **Category**: Happy Path
- **Given** a keyboard-only user on Runtime
- **When** they complete journey J-2
- **Then** every step is reachable by keyboard and each result is announced

#### Scenario BDD-61: Focus after Remove
**Traces to**: US-4 AS-3 · **Category**: Edge Case
- **Given** variables A, B, C with focus on Remove B
- **When** Remove is activated
- **Then** focus moves to C's name and "Removed B" is announced

## 14. Test-Driven Development Plan

One qa-lead test author for T11; a different qa-lead instance runs CHECK. Oracles come from this spec. Red-before-green evidence is mandatory. Local runs: one narrow tagged test at a time through the slot runner.

### 14.1 Slice A

Rows T-A01…T-A25, T-A50 and F-A01 belong to the #1489 converter and are **[ON HOLD — §2.4]**; they are not written in T11. Slice A tests are T-A30…T-A35, T-A39…T-A46.

| ID | Test | Level | Traces to |
|---|---|---|---|
| T-A01 | `TestSelectConverter_AppBundleViaSymlinkChain` (fixture bundle, `goos` param) | Unit | BDD-04 |
| T-A02 | `TestSelectConverter_HomebrewWrapperTemplate` | Unit | BDD-03 |
| T-A03 | `TestSelectConverter_UnsupportedLayoutRefusedOnDarwin` (wrapper with extra line, flat) | Unit | BDD-04 |
| T-A04 | `TestSelectConverter_OrderPathThenApplicationsFallback` | Unit | BDD-04 |
| T-A05 | `TestSelectConverter_ExplicitConverterBindsRegistry` | Unit | BDD-04 |
| T-A06 | `TestPrepareScopedRegistry_FiltersMacSpellKeepsHunspell` | Unit | BDD-01 |
| T-A07r | `TestPrepareScopedRegistry_RetainsTwoNeverInUseNeverLast` | Unit | FR-004 |
| T-A08 | `TestPrepareScopedRegistry_ScopesDisjointNoActivePointer` | Unit | BDD-06 |
| T-A09 | `TestPrepareScopedRegistry_RefusesUntrustedSources` (SEC-03 outline) | Unit | SEC-03 |
| T-A10 | `TestPrepareScopedRegistry_ConcurrentPreparationsSerialised` (`-race`) | Unit | SEC-07 |
| T-A10m | `TestPrepareScopedRegistry_PeakHeapWithinBudget` (3 MiB input; `runtime.ReadMemStats` delta ≤ 8 MiB) | Unit | SEC-07, MV-6 |
| T-A11 | `TestPrepareScopedRegistry_AuditEventWithoutContents` | Unit | SEC-06 |
| T-A12 | `TestRealBundleRegistryFitsCapsAndLocationRule` (darwin; must **run**, not skip, in the hands-on receipt; records file count and sizes) | Integration | SEC-03, A-4 |
| T-A13 | `TestConvertDocument_ChildEnvExactlyOnePointer` (fake converter dumps its env) | Integration | MV-2 |
| T-A14 | `TestConvertDocument_PrepareFailureLaunchesNothing` | Integration | BDD-05 |
| T-A15 | `TestConvertDocument_OutputVerification` (BDD-08/08b outline; Go-built fake converter, all OSes) | Integration | BDD-08, BDD-08b |
| T-A16 | `TestComposeExecutionEnv_LinuxWindowsByteIdentical` (golden from `b7d4483c9`) | Unit | BDD-07 |
| T-A17r | `TestSeatbeltChild_RegistryCopiesNotWritable` (darwin; real child started before scope B is published) | Integration | SEC-02, MV-4 |
| T-A18 | `TestConvertDocument_TimeoutKillsProcessGroup` | Integration | BDD-08b |
| T-A19 | `TestBashResult_SofficeCrashHint` | Unit | BDD-09 |
| T-A20r | `TestConvertDocument_GatewayEnvUnchangedWithPreexistingValue` | Integration | SEC-05 |
| T-A22 | `TestSeatbelt_OpenProfileKeepsIPCDefaultDeny` (existing, unchanged) | Unit | SEC-01 |
| T-A23 | `TestDarwinConvertDocumentRealLibreOffice_{AppBundleSymlink,HomebrewWrapper}` (5-slide non-ASCII fixture in testdata; RED at `b7d4483c9`: tool absent / direct `soffice` under the production policy aborts with `Fatal exception: Signal 6` and no PDF; GREEN: verified PDF, 5 pages) | E2E (Go) | BDD-01, BDD-03 |
| T-A24 | `TestNoSharedConverterStoreExports` (removed exports absent; `documentEnvTurn` no longer reads a pointer) | Unit | FR-010 |
| T-A25 | `TestElicifySkillsCallConvertDocument` (embedded guidance mentions `convert_document`, not a bare `soffice` call) | Unit | BDD-02 |
| T-A30 | `TestGuard_CompleteQuotedLiteralsNotRefused` (BDD-10 outline; root-writable probe injected false) | Unit | BDD-10 |
| T-A31 | `TestGuard_RealOutsideWritesStillRefused` (SEC-10; expected paths derived per OS) | Unit | SEC-10 |
| T-A32 | `TestGuard_FragmentsTemplatesGlobsTraversalRefused` (SEC-13 outline) | Unit | SEC-13 |
| T-A33 | `TestGuard_RuleOffWhenRootWritable` (probe true; `goos` windows) | Unit | SEC-11 |
| T-A34 | `TestGuard_ExpansionAndProtectedUnchanged` | Unit | SEC-12 |
| T-A35 | `TestRootWritableProbe_PerPlatform` | Unit | FR-021 |
| T-A39 | Existing guard tests incl. `TestGuardCommand_TruePositivesStillBlocked` and `TestExecTool_AuditOnPathRejection` unchanged (veto kept; no test superseded — E34) | Regression | FR-022, FR-023 |
| T-A40 | `TestCliValidate_CLITokenRefusedZeroSpawn` | Integration | SEC-50 |
| T-A41 | `TestCliValidate_BypassRefusedZeroSpawn` | Integration | SEC-51 |
| T-A42 | `TestCliValidate_PersonAllowed` (existing behaviour kept for a person) | Integration | BDD-50 |
| T-A43 | `TestPersonSessionMiddleware_Matrix` (person / CLI token / bypass / unauthenticated) | Unit | FR-030 |
| T-A44 | `TestCheckProgramPath_Unix` (matrix: inside `$OMNIPUS_HOME`, inside a workspace, inside a mount, link chain whose middle link sits in a workspace, final file group/other-writable, parent directory other-writable, not executable, directory, relative path, allowed file; returned path equals the final resolved file) | Unit | SEC-52, SEC-53, FR-031 |
| T-A44w | `TestCheckProgramPath_Windows` (relative, UNC allowed, `.exe` allowed, `.ps1` refused, 8.3 short name refused, alternate data stream refused, file inside a workspace refused, ACL writable by Everyone refused) | Unit | FR-031 |
| T-A45 | `TestCliValidate_SharedCheckBeforeSpawn` (workspace candidate, link into workspace, others-writable; Go-built marker-writing executable; marker absent; `path-not-allowed`) | Integration | SEC-52, SEC-53 |
| T-A46 | `TestCliValidate_ChecksTypedPathNotSaved` (saved A, typed B → B checked, settings unchanged; no saved path → same) | Integration | BDD-51 |
| T-A50 | `TestConvertDocument_PolicyCoverage` (ceiling present after `ReconcileToolPolicyCeiling`; deny seeded exactly where `write_file` is deny; registered for every agent) | Unit | FR-016 |
| F-A01 | `humanizeToolName` has a label for `convert_document` (vitest) | Unit | FR-016 |

### 14.2 Slice B

| ID | Test | Level | Traces to |
|---|---|---|---|
| T-B02 | `TestDiagnosticSnapshot_SavedModeRejectsConflictingFields` (REST smoke + preview) | Integration | SEC-30 |
| T-B03 | `TestConnTest_CredentialsFromSnapshotEnv` (BDD-36 outline incl. `CODEX_HOME`) | Unit | BDD-30, BDD-36 |
| T-B04 | `TestConnTest_NeverReportsVerified` | Unit | MV-11 |
| T-B05 | `TestSmokeTest_InvalidKeyReportsRejected` (Go-built fake CLI, all OSes) | Integration | BDD-31 |
| T-B06 | `TestSmokeLimiter_SharedAcrossRESTAndTool` | Integration | BDD-33 |
| T-B07 | `TestAgentRunnerTool_NativeAgentNoRunner` | Unit | BDD-32 |
| T-B08 | `TestDiagnostics_NoSettingsPathRefused` | Integration | SEC-32 |
| T-B09 | `TestDiagnostics_NoValueInAnySink` (Dataset S through stdout, version failure, stderr; REST + tool; logger capture; transcript) | Integration | SEC-31 |
| T-B10 | `TestAgentRunnerTool_PolicyCoverage` | Unit | FR-037 |
| T-B11 | `TestSmokeAndPreview_RejectFreePath` | Integration | FR-039 |
| T-B12 | `TestDiagnosticSnapshot_TakenAtStart` (config change mid-run; response carries the start revisions) | Integration | FR-032, BDD-39 |
| T-B13 | `TestPresetParams_AppliedLastUnfiltered_AgentFlagsFiltered` (end-to-end over T8's resolver: argv order, agent deny-listed flag dropped, preset flag kept) | Integration | BDD-52, MV-16 |
| T-B14 | `TestPresetAudit_RecordsPresetUseAndFilterBypass` (one entry; parameter text through the sensitive filter; Dataset S value in a preset parameter absent) | Integration | BDD-52 |
| T-B15 | `TestSmokeTest_OutcomeClassification` (BDD-38 outline; `ok` semantics per response) | Integration | BDD-38 |
| T-B16 | `TestDiagnosticSnapshot_WorkspaceAuthority` (BDD-37 outline; observes the child's actual working directory) | Integration | BDD-37, MV-17 |
| F-B01 | `AgentProfile.runtimeEditable.test.tsx` — fixtures use server-real descriptors from T8 rows (current fixture granting `executor` to every agent corrected) | Unit | BDD-20, BDD-24 |
| F-B02 | `AgentProfile.envMergeRemove.test.tsx` — request carries only changed names and `env_overrides_remove`; never a mask | Unit | BDD-21 |
| F-B03 | `AgentProfile.noPreSaveGate.test.tsx` — no runner-test request before `updateAgent`; status after save | Unit | BDD-22, MV-14 |
| F-B04 | `ThirdPartyAgentsSection.test.tsx` — row states (§12.1), pre-fill rules, server reasons, Clear confirm | Unit | BDD-40, BDD-42, BDD-43, SEC-40 |
| F-B04b | `ThirdPartyAgentsSection.rowSave.test.tsx` — request body = last saved + row change + revision; 409 handling; drafts of other rows not sent; late detection keeps touched field | Unit | BDD-41, BDD-44, BDD-45, MV-13 |
| F-B05 | `humanizeToolName` / Ask group for `test_agent_runner` | Unit | FR-037 |
| F-B06 | `AgentProfile.presetLine.test.tsx` | Unit | BDD-20, BDD-23 |
| F-B08 | `AgentProfile.diagnosticIdentity.test.tsx` — delayed responses: save A → save B → result B → result A; smoke A during save B; settings revision change; unmount; live region announces only current results | Unit | BDD-39, MV-15 |
| F-B07 | Accessibility: names, live-region announcements, focus after Replace/Remove/refusal/conflict (`@testing-library` + axe if already in the repo, else role/name queries) | Unit | BDD-61, FR-055 |
| E-1 | `tests/e2e/agents-3p-runtime-repair.spec.ts` — journey J-2 from an unauthenticated worker, reload, replace, remove one of three | E2E | BDD-21, BDD-22 |
| E-2 | `tests/e2e/settings-third-party-agents.spec.ts` — fake `codex` in a temp PATH writing a marker if run; pre-fill; save one row; two-tab conflict | E2E | BDD-40, BDD-41, BDD-44, SEC-41 |
| E-3 | `tests/e2e/third-party-a11y.spec.ts` — keyboard-only J-2; 320 px and 200% zoom with a long path | E2E | BDD-60, FR-055 |
| T8 | A-T26…A-T31, A-T9, A-T1 rows (T8-owned; listed for traceability) | — | SEC-42, BDD-34 |

### 14.3 Datasets

**Dataset G — guard (macOS / non-root Linux; Windows column)**

| # | Command text | macOS/Linux | Windows |
|---|---|---|---|
| G1 | `python3 -c "print('/api/v1/users')"` | not refused | refused |
| G2 | `python3 -c "x = '/healthz'"` | not refused | refused |
| G3 | heredoc with `"/api/v1"` | not refused | refused |
| G4 | `f'{base}/api/items'` | refused | refused |
| G5 | `'/%s/%d' % ('a',1)` | refused | refused |
| G6 | `glob.glob('/Us*/x')` | refused | refused |
| G7 | `'/api/[a]'` | refused | refused |
| G8 | `'/api\\x2fv1'` | refused | refused |
| G9 | `'see /api/v1 docs'` | refused | refused |
| G10 | `"/api/'v1'"` (mixed quotes) | refused | refused |
| G11 | `'../shared/x'` | refused (veto) | refused |
| G12 | `open('/etc/t11probe','w')` / `C:\Windows\t11probe` | refused | refused |
| G13 | `echo x > ~/t11probe` | refused | refused |
| G14 | `cp a /tmp/x` | refused | refused |
| G15 | `cat $FOO/api/v1` | unresolvable refusal | same |
| G16 | `cat '<home>/credentials.json'` | protected refusal | same |
| G17 | G1 with root-writable probe true | refused | n/a |
| G18 | `print('a' + '/' + 'b')` | not refused (unchanged) | unchanged |
| G19 | `open('/tm' 'p/t11probe','w')` | refused | refused |
| G20 | `open('/tm'` newline `'p/t11probe','w')` | refused | refused |
| G21 | `open('/tm' + 'p/x','w')` | refused | refused |
| G22 | `open('/{}'.format('tmp'),'w')` | refused | refused |
| G23 | `open(r'/tmpx','w')` | refused | refused |
| G24 | `"api": "/api/v1",` inside a heredoc dict | not refused | refused |

**Dataset R — registry sources [ON HOLD — §2.4]**

| # | Source | Expected |
|---|---|---|
| R1 | stock-shaped, one MacOSXSpellChecker + HunSpell | copy 0 / 1 |
| R2 | no spell checker | unchanged copy |
| R3 | two registrations | refused |
| R4 | symlinked `services.rdb` | refused |
| R5 | named pipe | refused |
| R6 | one file exactly 2 MiB | accepted |
| R7 | 2 MiB + 1 | refused |
| R8 | total 3 MiB + 1 | refused |
| R9 | 33 files | refused |
| R10 | component `file:///tmp/evil.dylib` | refused |
| R11 | non-ASCII bundle path | accepted; pointer URL-escaped |
| R12 | path with spaces | accepted |
| R13 | empty services folder | refused |
| R14 | deeply nested XML (10,000 levels) | refused without unbounded memory |

**Dataset C — converter selection [ON HOLD — §2.4]**

| # | Setup | Expected |
|---|---|---|
| C1 | workspace env bundle via symlink | used |
| C2 | Homebrew wrapper (exact template) | bundle used |
| C3 | only `/Applications` | used |
| C4 | two bundles, `converter` names B | B used; digest of B |
| C5 | `converter` inside protected inventory | refused |
| C6 | `converter` is a directory | refused |
| C7 | Windows `soffice.com` | used without registry |

**Dataset S — secret values:** S1 `zq7` (≤ 3 bytes, below the masking minimum), S2 `sk-ant-` + 48 characters, S3 value with `=` and a newline, S4 `ключ-🔑`.

### 14.4 Regression

Must stay unchanged: ADR-068 read exemption; the `../` veto and its existing tests; protected-internal-file leg; unresolvable-expansion refusal; colon lists; web-URL exemption; `/dev/*`; Seatbelt two-service preamble; `ChildEnvironment` on Linux/Windows; `mergeEnvOverrides` `OMNIPUS_*` protection; runner reasons other than the renamed `unauthenticated`; existing filter tests in `converter_registry_test.go` (the orphan "Review concern 4" comment is replaced by T-A13). Regression rows: G18, R2.

## 15. Functional Requirements

| ID | Requirement |
|---|---|
| FR-001 | [ON HOLD — §2.4] The system MUST provide `convert_document` with the inputs and limits of AD-1 on macOS, Linux and Windows. |
| FR-002 | [ON HOLD — §2.4] It MUST select the converter per AD-2 and report the one used. |
| FR-003 | [ON HOLD — §2.4] On macOS it MUST accept only an app bundle or the exact wrapper template and refuse other layouts with a repair message. |
| FR-004 | [ON HOLD — §2.4] It MUST prepare the filtered copy in a per-workspace content-addressed store with no shared pointer, reuse verified generations, keep the newest two per scope, and never delete one in use or the last usable one. |
| FR-005 | [ON HOLD — §2.4] When preparation succeeds, the conversion child MUST receive exactly one `URE_MORE_SERVICES`; when it fails, no child MUST start; the gateway environment MUST NOT change. |
| FR-006 | [ON HOLD — §2.4] The child MUST get read-without-write on exactly its generation; the sandbox profile MUST gain no permission. |
| FR-007 | [ON HOLD — §2.4] A preparation failure MUST refuse that conversion with the observed reason and the repair step. |
| FR-008 | [ON HOLD — §2.4] Success MUST be decided by the AD-5 output checks, never by the exit code. |
| FR-009 | [ON HOLD — §2.4] On Linux and Windows the tool MUST skip the registry step and bash composition MUST stay byte-identical. |
| FR-010 | [ON HOLD — §2.4] The unwired shared-store exports and the pointer re-read in bash readiness MUST be deleted. |
| FR-011 | [ON HOLD — §2.4] Each preparation MUST emit one content-free audit event. |
| FR-012 | [ON HOLD — §2.4] Preparation MUST enforce 2 MiB / 3 MiB / 32-file caps, run one at a time process-wide, and stay within 8 MiB peak extra heap. |
| FR-013 | [ON HOLD — §2.4] Document skills and runtime guidance MUST direct agents to `convert_document`. |
| FR-014 | [ON HOLD — §2.4] A timeout MUST kill the process group; conversions in one workspace MUST run one at a time. |
| FR-015 | [ON HOLD — §2.4] On macOS a bash result whose output shows the LibreOffice crash SHOULD carry the `convert_document` hint. |
| FR-016 | [ON HOLD — §2.4] `convert_document` MUST be registered for every agent with a ceiling entry `allow`, `AutoRuns` class, and explicit deny where `write_file` is deny; it MUST have a UI label. |
| FR-020 | The guard MUST NOT refuse a candidate meeting all six AD-7 conditions, and MUST refuse every other outside candidate as today. |
| FR-021 | The AD-7 rule MUST be off on Windows and when the root is writable. |
| FR-022 | The `../` veto MUST stay unchanged; the limitation MUST be documented. |
| FR-023 | All other guard refusals MUST stay unchanged. |
| FR-030 | Candidate validation MUST refuse CLI-token, development-bypass and user-less requests with 403 before any work, via `middleware.RequirePersonSession` (Slice A). |
| FR-031 | Candidate validation MUST run `clitrust.CheckProgramPath` with `clitrust.AgentWritableRoots` before any spawn and launch only the returned resolved file (Slice A); it MUST check the typed candidate and save nothing. |
| FR-032 | Diagnostics MUST use one start-time snapshot per AD-12; saved mode MUST reject `cli`, `model`, `cli_args`, `max_tool_iterations`; the workspace MUST be explicit or the sole membership, validated as dispatch validates it. |
| FR-033 | The smoke limiter MUST key on the initiating person (REST) or calling agent (tool) on one limiter instance, with one run per target agent at a time. |
| FR-034 | Connection mode MUST report `program_runs`, `credentials` from the snapshot environment (including credential directories) and `authentication: not_verified`; `ok` MUST mean program runs and credentials found. |
| FR-035 | The smoke test MUST report `outcome` and `authentication` separately per AD-13; `rejected` only for an identified authentication error. |
| FR-036 | Diagnostics MUST refuse with `no-settings-path` when the CLI has no saved preset. |
| FR-037 | `test_agent_runner` MUST exist with AD-16's policy. |
| FR-038 | No environment value MAY appear in any diagnostic sink. |
| FR-039 | Smoke-test and preview requests MUST NOT accept a free path. |
| FR-040 | Runtime MUST make env and flags editable for custom third-party workers and show the preset read-only. |
| FR-041 | The env editor MUST send only changed names and explicit removals and never a mask. |
| FR-042 | Saving env or flags MUST NOT wait on a diagnostic; a non-blocking status MUST follow. |
| FR-043 | Diagnostic responses MUST carry `agent_revision` and `settings_revision`; the UI MUST present and announce only results matching the loaded revisions (AD-18). |
| FR-050 | Settings MUST show three preset rows with path, standard parameters and detection result. |
| FR-051 | Detection MUST NOT save, MUST NOT start a program, and MUST NOT overwrite a touched field. |
| FR-052 | Check version MUST be an explicit person action through candidate validation. |
| FR-053 | Row Save/Clear MUST send last-saved state plus that row's change with `revision`, and MUST show a conflict on 409. |
| FR-054 | Settings and Runtime MUST implement the §12.1 states. |
| FR-055 | New controls MUST meet §12.3. |
| FR-056 | New UI MUST use the §12.4 catalogue components and pass the named gates. |
| FR-057 | The Settings row MUST offer a free-text parameters field, shown in full, with no warning. |
| FR-058 | Runs MUST apply preset parameters last and unfiltered, agent flags filtered, and write one audit entry stating the preset use and filter bypass (T8 implements; T11 tests end to end). |
| FR-059 | User documentation MUST state that Omnipus does not sandbox external CLIs and what a Codex bypass preset exposes (founder's accepted risk). |
| FR-060 | User documentation MUST be updated in the same change (§19). |
| FR-061 | ADR-030, ADR-068 and ADR-032 MUST receive the §5 dated amendments. |

## 16. Success Criteria

| ID | Criterion |
|---|---|
| SC-001 | [ON HOLD — §2.4] On the exact candidate, a fresh macOS instance converts a 5-slide deck via an agent-installed LibreOffice to a verified 5-page PDF in 3 of 3 attempts (U-02). |
| SC-002 | [ON HOLD — §2.4] T-A22 green: two Mach services. |
| SC-003 | Dataset G holds on macOS and Linux; G12–G16 refused on Windows. |
| SC-004 | T-A16 green on Linux and Windows with a receipt showing it ran. |
| SC-005 | E-1 completes J-2 including reload, with C's stored value unchanged. |
| SC-006 | T-B04: connection mode never returns `verified`; zero model requests. |
| SC-007 | Dataset S: 0 occurrences across all sinks (T-B09). |
| SC-008 | SEC-50…SEC-53: zero spawns (marker files absent). |
| SC-009 | E-2: zero save requests before a click; zero processes from detection; two-tab conflict shown. |
| SC-010 | docs-verifier: every checkable claim on changed pages TRUE. |
| SC-012 | Dataset G19–G24 hold on macOS and Linux (adjacent/concatenated/prefixed literals refused; heredoc dict route allowed). |
| SC-013 | T-B15/F-B08: no operational failure is labelled a sign-in rejection; no stale result is announced. |
| SC-014 | T-A44/T-A44w/T-A45: every forbidden candidate → zero spawns on Unix and Windows. |
| SC-011 | [ON HOLD — §2.4] T-A10m peak extra heap ≤ 8 MiB. |

## 17. Traceability Matrix

| Requirement | Story | Scenario(s) | Test(s) |
|---|---|---|---|
| FR-001 [ON HOLD] | US-1 | BDD-01 | T-A13, T-A23 |
| FR-002 [ON HOLD] | US-1 | BDD-04 | T-A01, T-A04, T-A05 |
| FR-003 [ON HOLD] | US-1 | BDD-03, BDD-04 | T-A02, T-A03 |
| FR-004 [ON HOLD] | US-1, US-2 | BDD-06 | T-A06, T-A07r, T-A08 |
| FR-005 [ON HOLD] | US-1, US-2 | BDD-05, SEC-05 | T-A13, T-A14, T-A20r |
| FR-006 [ON HOLD] | US-2 | SEC-01, SEC-02 | T-A17r, T-A22 |
| FR-007 [ON HOLD] | US-1 | BDD-05 | T-A14 |
| FR-008 [ON HOLD] | US-1 | BDD-08, BDD-08b | T-A15, T-A18 |
| FR-009 [ON HOLD] | US-8 | BDD-07 | T-A16, T-A15 (Windows leg) |
| FR-010 [ON HOLD] | US-2 | — (structural) | T-A24 |
| FR-011 [ON HOLD] | US-2 | SEC-06 | T-A11 |
| FR-012 [ON HOLD] | US-2 | SEC-03, SEC-07 | T-A09, T-A10, T-A10m, T-A12 |
| FR-013 [ON HOLD] | US-1 | BDD-02 | T-A25, U-03 |
| FR-014 [ON HOLD] | US-1 | BDD-08b | T-A18 |
| FR-015 [ON HOLD] | US-1 | BDD-09 | T-A19 |
| FR-016 [ON HOLD] | US-1 | — (reachability) | T-A50, F-A01 |
| FR-020 | US-3 | BDD-10, SEC-13 | T-A30, T-A32 |
| FR-021 | US-3, US-8 | SEC-11 | T-A33, T-A35 |
| FR-022 | US-3 | SEC-13 | T-A32, T-A39 |
| FR-023 | US-3, US-8 | SEC-10, SEC-12, SEC-13 | T-A31, T-A32, T-A34, T-A39 |
| FR-030 | US-9 | SEC-50, SEC-51 | T-A40, T-A41, T-A43 |
| FR-031 | US-9, US-7 | SEC-52, SEC-53, BDD-50, BDD-51 | T-A42, T-A44, T-A44w, T-A45, T-A46 |
| FR-032 | US-5 | SEC-30, BDD-37 | T-B02, T-B12, T-B16 |
| FR-033 | US-5 | BDD-33 | T-B06 |
| FR-034 | US-5 | BDD-30, BDD-36 | T-B03, T-B04 |
| FR-035 | US-5 | BDD-31, BDD-38 | T-B05, T-B15 |
| FR-036 | US-5 | SEC-32 | T-B08 |
| FR-037 | US-5 | BDD-32 | T-B07, T-B10, F-B05 |
| FR-038 | US-5 | SEC-31 | T-B09 |
| FR-039 | US-5 | SEC-30 | T-B11 |
| FR-040 | US-4, US-6 | BDD-20, BDD-23, BDD-24, BDD-34 | F-B01, F-B06 |
| FR-041 | US-4 | BDD-21 | F-B02, E-1 |
| FR-042 | US-4 | BDD-22 | F-B03, E-1 |
| FR-043 | US-5 | BDD-39 | T-B12, F-B08 |
| FR-050 | US-7 | BDD-40, BDD-43 | F-B04, E-2 |
| FR-051 | US-7 | BDD-45, SEC-41 | F-B04b, E-2 |
| FR-052 | US-7, US-9 | BDD-50, BDD-51 | F-B04, T-A42, T-A46 |
| FR-053 | US-7 | BDD-41, BDD-42, BDD-44, SEC-40 | F-B04, F-B04b, E-2 |
| FR-054 | US-7, US-4 | BDD-44, BDD-45 | F-B04, F-B04b |
| FR-055 | US-4 | BDD-60, BDD-61 | F-B07, E-3 |
| FR-056 | US-4, US-7 | — | typecheck + design-system lock gates |
| FR-057 | US-7 | BDD-52 | F-B04 |
| FR-058 | US-7 | BDD-52 | T-B13, T-B14 |
| FR-059 | US-7 | — | docs-verifier |
| FR-060 | all | — | docs-verifier |
| FR-061 | US-3, US-9 | — | architect review |
| (T8) agent path surfaces | US-7 | SEC-42 | T8 A-T27, A-T30 |

## 18. Hands-on UAT and native gates

### 18.1 UAT rows (macOS, exact candidate)

Provider/model for agent-driven rows: `openrouter` + `deepseek/deepseek-v4.1-flash`. One `uat-tester`, an independent `uat-validator`. Slice B touches `pkg/agent` runner paths → agent-engine hands-on rule applies to it. Rows U-01, U-02, U-03, U-06 are **[ON HOLD — §2.4]** (converter).

| Row | Slice | Steps | PASS evidence |
|---|---|---|---|
| U-04 | A | Ask the agent to run `python3 -c "print('/api/v1/users')"` and a heredoc script whose dict holds `"api": "/api/v1"` | Not refused |
| U-05 | A | Ask the agent to write `~/t11-outside.txt`; then to run a one-liner with `open('/tm' 'p/t11probe','w')`; then one printing `f'{x}/api'` | Write refused/escalated (tester declines; file absent); adjacent-literal and f-string commands refused with the guard message; `/tmp/t11probe` absent |
| U-11 | A | Validator: call candidate validation with the CLI token (`curl`) and in dev-bypass mode; as a person, check a marker-writing executable inside a workspace and one reached through a link into a workspace; then an allowed program | 403 ×2; `path-not-allowed` ×2; markers absent; allowed program returns a version; audit names the person and resolved path |
| U-07 | B | Workspace → Team → Codex worker → Runtime: add, replace, remove variables; edit a flag; reload | J-2 behaviour; masked values; third variable intact |
| U-08 | B | Ask Omnipus to add `DEBUG=1` and a flag | One call, normal policy only; Runtime shows the same |
| U-09 | B | Ask Omnipus "is the Codex worker signed in?" then "run a quick test"; repeat with the provider blocked by the egress proxy | Wording per AD-13; the blocked run says "Test could not complete", not "rejected"; audit present; no value in transcript export |
| U-10 | B | Keyboard-only run of J-2, with a smoke test started before a save | Every step operable; VoiceOver announces only the current result; the late result is labelled earlier-configuration |
| U-12 | B | Settings → Third-party agents (Claude and Codex installed): Save Codex with parameter `--dangerously-bypass-approvals-and-sandbox`; Check version of a typed path different from the saved one; try a workspace path; save Claude from a second tab | Pre-fill; parameter shown in full with no warning; Check version names the typed path and saves nothing; workspace path refused; conflict shown; Runtime shows the preset read-only |
| U-13 | B | Run a Codex worker with that preset and an agent flag on the deny list | Validator reads the audit: "person preset used; flag filter bypassed for preset parameters"; the agent's denied flag reported dropped; argv (preview) ends with the preset parameter |
| U-14 | B | Worker in two workspaces: run a check without naming one, then naming B | First refused asking which workspace; second runs in B's folder (validator checks the run's working directory in the audit/log) |

### 18.2 Native gates without a new CI job (R1-FQ7 "A"; R2-MIN-003)

Each row is an addition to an **existing** job step. The receipt script `scripts/require-tests-ran.sh <required-list> <go-test-json>` fails if any listed test ID is absent, skipped, or the run matched zero tests. Required lists live beside the workflow as `ci/required-tests/<platform>.txt` and are written from the test IDs below, not from the `-run` expression.

| Platform | Existing job | Command(s) added | Required test IDs |
|---|---|---|---|
| macOS | `phase3-macos.yml` `jobs.core` (path filter extended to `pkg/tools/shell_path_guard*`, `pkg/clitrust/**`, `pkg/gateway/middleware/**`, `pkg/gateway/rest_clivalidate*`) | `go test -tags goolm,stdjson -count=1 -p 1 -json -run '^(TestGuard_CompleteQuotedLiteralsNotRefused\|TestGuard_RealOutsideWritesStillRefused\|TestGuard_FragmentsTemplatesGlobsTraversalRefused\|TestGuard_RuleOffWhenRootWritable\|TestGuard_ExpansionAndProtectedUnchanged\|TestRootWritableProbe_PerPlatform)$' ./pkg/tools/`; `… -run '^TestCheckProgramPath_Unix$' ./pkg/clitrust/`; `… -run '^(TestPersonSessionMiddleware_Matrix)$' ./pkg/gateway/middleware/`; `… -run '^(TestCliValidate_CLITokenRefusedZeroSpawn\|TestCliValidate_BypassRefusedZeroSpawn\|TestCliValidate_PersonAllowed\|TestCliValidate_SharedCheckBeforeSpawn\|TestCliValidate_ChecksTypedPathNotSaved)$' ./pkg/gateway/` | T-A30…T-A35, T-A40…T-A46 (except T-A44w) |
| Windows | `cross-platform.yml` existing Windows tools job | Same `./pkg/tools/` selection; plus separate commands for `./pkg/clitrust/` (`^TestCheckProgramPath_Windows$`), `./pkg/gateway/middleware/`, `./pkg/gateway/` (the `TestCliValidate_*` names above and, in Slice B, `^(TestDiagnosticSnapshot_SavedModeRejectsConflictingFields\|TestDiagnosticSnapshot_TakenAtStart\|TestDiagnosticSnapshot_WorkspaceAuthority\|TestDiagnostics_NoSettingsPathRefused\|TestDiagnostics_NoValueInAnySink\|TestSmokeAndPreview_RejectFreePath)$`), and `./pkg/agent/runner/` in Slice B (`^(TestConnTest_CredentialsFromSnapshotEnv\|TestConnTest_NeverReportsVerified\|TestSmokeTest_InvalidKeyReportsRejected\|TestSmokeTest_OutcomeClassification)$`) | Slice A: T-A30…T-A35, T-A40…T-A43, T-A44w, T-A45, T-A46. Slice B: T-B02…T-B05, T-B08, T-B09, T-B11, T-B12, T-B15, T-B16 |
| Linux | Candidate CI (normal) | All tests | All |

Fake executables are built from Go in `TestMain` (no POSIX shell scripts on Windows; the existing `writeFakeBin` Windows skip is not reused).

## 19. User-facing documentation TODO

| Page | Slice | TODO |
|---|---|---|
| `docs/tools.md`, `docs/reference/built-in-tools.md` | A + B | Bash guard: which quoted literals are no longer refused (stand-alone, top folder cannot exist), what stays refused (adjacent/concatenated/template/prefixed/glob, `../`, Windows, root), workaround. `test_agent_runner` row (B). |
| `docs/troubleshooting.md` | A | Guard refusal entry with the write-file workaround. |
| `docs/security.md` | A + B | Candidate validation: person only, program-file check before running (A). Third-party agents (B): presets are person-only and may contain any CLI parameter; **Omnipus does not sandbox external CLIs — a working folder is not a boundary; with a Codex sandbox-bypass preset, Codex can read and write anything your user account can**; preset use is audited; env values masked; no encrypted CLI secrets yet. |
| `docs/operations/sandbox-limitations.md` | A + B | Guard rule off on Windows and for a root-run gateway; `../` limitation (A). External CLIs run outside the Omnipus sandbox (B). |
| `docs/agents.md` | B | Third-party workers: preset from Settings shown read-only; env/flags editable; status wording (credentials found / sign-in confirmed / rejected / could not complete); workspace choice for tests; repair journey; asking Omnipus. |
| `docs/settings.md` | B | Third-party agents section: preset rows (path + free-text parameters), detection, Check version of the typed path, Save/Clear per row, conflict message. |
| Internal | A/B | ADR-030, ADR-068 (A) and ADR-032 (B) amendments (§5). |
| [ON HOLD] `docs/skills.md`, converter rows | — | Wait for #1489. |

## 20. Work breakdown and order

| ID | Package | Owner | Depends on |
|---|---|---|---|
| A-R | RED tests T-A30…T-A35, T-A40…T-A46 | qa-lead (T11 test author) | spec accepted |
| A-1 | Guard AD-7 (six conditions) | backend developer (one) | A-R |
| A-2 | `pkg/gateway/middleware/person_session.go::RequirePersonSession`; `pkg/clitrust` (`check.go::CheckProgramPath`, `roots.go::AgentWritableRoots`, `check_unix.go`, `check_windows.go`); `cli-validate` wiring; contracts C-A1, C-A2 | same backend developer | A-R |
| A-3 | CLI-path hint component handles `path-not-allowed` (generated type) | frontend developer | A-2 contract commit |
| A-4 | CI additions and receipt script (§18.2) | backend developer | A-1, A-2 |
| A-5 | Docs (Slice A rows) | backend developer; docs-verifier audits | A-1, A-2 |
| A-6 | ADR-030 and ADR-068 amendments | architect | A-1, A-2 |
| A-C / A-G / A-U | CHECK (other qa-lead instance); one 5-reviewer gate over Slice A's combined scope (security-lead verifies the candidate checks); UAT U-04, U-05, U-11 | team-lead | in order |
| B-R | RED tests §14.2 | qa-lead (same author) | T8 contract commit (rev 5.3) |
| B-1 | Backend: diagnostic snapshot with workspace authority, truthful outcomes, result identity, limiter identity, `test_agent_runner` + policy, contracts C-B1…C-B3; end-to-end tests over T8's preset resolver and audit | backend developer | B-R, T8 contract; runs against T8 backend |
| B-2 | Frontend: Settings screen (free-text parameters, typed-path Check version), Runtime changes (preset line, env merge/remove editor, gate removal, diagnostics states with revision identity, workspace selector), a11y, labels | frontend developer (loads `omnipus-design-system`) | B-R; mocks until T8 backend |
| B-3 | Docs (Slice B rows) incl. the exposure statement | implementing developers; docs-verifier | B-1, B-2 |
| B-4 | ADR-032 amendment | architect | B-1 |
| B-C / B-G / B-U | CHECK; one 5-reviewer gate over the combined T8-backend + T11-B candidate; UAT U-07…U-10, U-12…U-14 | team-lead | in order |

```
Slice A (independent, lands first; T8 reuses its two shared pieces)
  A-R ─► A-1 guard ──────────────┐
       └► A-2 person gate + clitrust + cli-validate ─► A-3 hint ─┤
                     A-4 CI, A-5 docs, A-6 ADRs ─────────────────┴─► A-C ─► A-G ─► train ─► A-U ─► founder yes
Slice B (development order)                 (release order)
  T8 contract commit (rev 5.3) ─► B-R ─► B-1 ∥ B-2 (mocks) ─┐
  T8 backend (uses A-2's functions) ──────────────────────── ┴─► one train: T8 backend + T11-B ─► CHECK ─► gate ─► UAT ─► founder yes
#1489 converter: ON HOLD (§2.4) — no work package
```

## 21. Open items after this final correction

### 21.1 For the founder (the only blocking items)

| # | Item | Status |
|---|---|---|
| H-1 / R2-Q1 / R2-CRIT-001 | #1489: built-in Office-document conversion without a heavy LibreOffice dependency; if a converter is kept, **who may execute it** (the two-layer tool policy with an explicit Ask/Auto posture and role defaults — not derived from `write_file`), Windows/no-kernel behaviour, and the held findings R2-MAJ-005/009/010/011, R2-MIN-002 | **Open, on hold — founder to decide.** No T11 slice depends on it. |

### 21.2 Cloud chief — resolved

| # | Resolution |
|---|---|
| CH-1 | Accepted: per-CLI free-text preset parameters in `ThirdPartyAgentSettings` (rev 5.3); argv order and audit per AD-14. |
| CH-2 | Accepted: no saved preset path → run refused with a repair message (replaces D-17's fallback). |
| CH-3 | Accepted: `revision` + 409. |
| CH-4 | Named here: `pkg/gateway/middleware/person_session.go::RequirePersonSession`; `pkg/clitrust/check.go::CheckProgramPath`; `pkg/clitrust/roots.go::AgentWritableRoots`. Built in Slice A (R2-Q2); T8 reuses. |

Not verified by me: the rev 5.3 text itself (relayed by the coordinator; the coordination repo was last read at rev 5.1). B-R starts only after rev 5.3's contract commit exists.

### 21.3 Security-lead review items

| # | Item |
|---|---|
| SL-1 | `RequirePersonSession` matrix and `CheckProgramPath` link-chain and permission rules on Unix and Windows; zero-spawn proofs. |
| SL-2 | AD-7 six conditions; residual of a privileged process creating a top-level folder between check and run. |
| SL-3 | Preset parameters bypassing the flag filter (founder-accepted); audit completeness; parameter text through the sensitive filter. |
| SL-4 | Exposure statement for external CLIs (docs and ADR-032) is accurate per platform. |

## 22. Ambiguities and assumptions

| # | Item | Basis | Certainty / handling |
|---|---|---|---|
| [ON HOLD] A-1 | LibreOffice reads `URE_MORE_SERVICES` from the environment | `converter_registry.go` comment; review B experiment | Inferred (high); T-A23 proves |
| [ON HOLD] A-2 | God Mode children on macOS run under a Seatbelt floor | jarvis report; `executeRun` comment | Inferred (medium); tested in both modes |
| [ON HOLD] A-3 | An agent can install LibreOffice into its workspace via environment_setup | jarvis report side note (Omnipus-env LibreOffice running) | Inferred (medium); U-01 dry run before RED; escalate if impossible |
| [ON HOLD] A-4 | Stock bundle registries fit 2 MiB / 3 MiB / 32 files and the location rule | 256 KB `services.rdb` in `logs/1489-registry.log`; other files unmeasured | Unknown; T-A12 decides before GREEN |
| A-5 | Windows drivers can start `.cmd` shims | — | Unknown; Windows leg tests |
| A-6 | Which built-in roles have `write_file` deny | not enumerated | Unknown; T-A50 derives from the seeds |
| A-7 | "Signed-in person" = a `Gateway.Users` identity on a non-bypass request | E28 | Verified mechanism |
| A-8 | T8 rev 5.3 contains CH-1…CH-4 as relayed | coordinator relay | Inferred; B-R waits for the rev 5.3 contract commit |
| A-9 | Each CLI driver can classify an authentication error distinctly from other failures | `ExecutorSmokeTestResponse.yaml` description distinguishes them (per reviewer E12) | Inferred; T-B15 proves per CLI; unclassifiable → `could_not_complete` (never `rejected`) |

## 23. Evaluation scenarios (holdout — not in the TDD plan or traceability)

| # | Kind | Scenario | Observable outcome |
|---|---|---|---|
| [ON HOLD] H-1 | Happy | Fresh Mac: "turn this outline into a 7-slide deck and send me the PDF" | 7-page PDF, no hand conversion |
| H-2 | Happy | "Scaffold a small Python API with routes /health and /v1/items" (agent writes files) | No guard refusal |
| H-3 | Happy | Founder saves the Codex preset, then asks Omnipus to create a Codex worker and test it | Worker runs the saved program; test result worded truthfully |
| H-4 | Error | Ask an agent to save a report to the Desktop | Refused or escalated |
| H-5 | Error | Ask Omnipus to point the Codex worker at a script in its workspace | Impossible: no path input; Omnipus explains Settings is person-only |
| [ON HOLD] H-6 | Edge | Two workspaces render with different LibreOffice versions at once | Both PDFs verify |
| H-7 | Edge | Upgrade Codex with Homebrew after saving its preset | Workers still run (T8 X1) |

## 24. Prerequisites and runtime notes

| Item | Note |
|---|---|
| Build | Tags `goolm,stdjson`, `CGO_ENABLED=0`; SPA embed stub in fresh worktrees. Pure Go (Hard Constraint #2): root-writable probe and process-group kill via `golang.org/x/sys/unix` / `windows`. |
| Footprint | Slices A/B add no long-lived memory; the converter budget is on hold (§2.4). |
| Degradation | Guard rule off on Windows/root (fails safe to today's refusal); `CheckProgramPath` has Unix and Windows implementations (Hard Constraint #4). |
| Local runs | One narrow tagged test at a time through the slot runner; T-A12/T-A23 on the dev Mac with `/Applications/LibreOffice.app`. |
| Worktrees | One writer per worktree; Slice A and Slice B separate branches. |

## 25. Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Round-2 inputs read | `t11-documents-and-3p-agents-spec-review-round2.md` (461 lines: findings, closure audit, ruling on MAJ-004) and the founder answers appended to `FOUNDER-ANSWERS-ROUND1.md` | Verified |
| Backup before editing | `cp … .bak-round2` exit 0; pre-edit SHA-256 `ad76b716…7211` equals the hash the reviewer reviewed | Verified |
| `RequirePersonSession` can sit beside `RequireNotBypass` without an import cycle | `pkg/gateway/middleware/bypass_gate.go` imports `pkg/gateway/ctxkey`; `CLITokenContextKey`/`UserContextKey` defined in `pkg/gateway/ctxkey/ctxkey.go` | Verified |
| Middleware folder has no person-session file today | `git ls-tree pkg/gateway/middleware/` | Verified |
| No `pkg/clitrust` package exists today | Not listed in round-0/1 reads; name chosen as new | Inferred (not re-listed this round) |
| External CLIs are not under an Omnipus filesystem confiner | Reviewer E9 (`driver_codex.go::CodexDriver.Run` with `exec.CommandContext`); `external_dispatch.go::prepareRunOptions` (scrubbed env + egress proxy only), read in round 0 | Verified (prepareRunOptions); driver detail reviewer-verified |
| Adjacent-literal counterexample | Reviewer E6 (`ast.parse` joins `'/tm' 'p/…'`); AD-7 condition 6 refuses it by the neighbour rule | Verified (reviewer); rule is a spec decision |
| Dispatch's workspace resolver exists | Reviewer E11 (`workspace_reroot.go::resolveTurnWorkDirAndWorkspaceOrRefuse`); referenced in round 0 via `external_dispatch.go` | Verified |
| T8 rev 5.3 content | Relayed by the coordinator; not read | Unknown (A-8) |
| GitNexus impact | Not available in this session | Inferred |
| Nothing executed; no code, tests, ADR files or ledger edited | Only this spec and its `.bak-round2` were written | Verified by construction |
| **Self-check** | Re-read against the brief: #1489 out of Slice A and marked ON HOLD everywhere (§2.4 index plus tags on AD-1…6, US-1/2, converter BDD/SEC/tests/FR/SC/MV/UAT/datasets/assumptions/holdout); R2-CRIT-001 recorded as open, on hold, founder to decide; Slice A = guard (R2-MAJ-001 fixed) + person gate + shared check (both named); prepared R2-FQA/CH changes applied; R2-MAJ-002/003/004/006/007/008 and R2-MIN-001/003 fixed; R2-MAJ-005/009/010/011 and R2-MIN-002 held; founder quotes added. Scripted checks: FR↔matrix and scenario↔matrix coverage, test IDs defined (results in the report to the coordinator). Not done: nothing executed; rev 5.3 unread. | Verified (self) |

skills: plan-spec, omnipus-shared-rules
