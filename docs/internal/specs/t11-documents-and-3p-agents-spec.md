# Feature Specification: T11 — Shell-guard fix and third-party agent settings

Status: Approved for building (founder 2026-10-29: simple version)

| Header | Value |
|---|---|
| Founder direction | "do not overcomplicate it, it is a simple setting in the ui for t11, nothing special" |
| Full version | `t11-documents-and-3p-agents-spec-full.md` (two grill rounds, reference only; this file wins where they differ) |
| Code base | `origin/release/v0.1.1` at `b7d4483c9` |
| Slices | **A** — shell-guard fix and person-only "Check CLI" route; lands first, independent of T8. **B** — Settings → Third-party agents; lands in the same train as T8's backend. |
| T8 interface | T8 tool-parity design rev 5.3 (as accepted by the cloud chief): `ThirdPartyAgentSettings` stored under `sandbox.third_party_agents`, `GET`/`PUT /settings/third-party-agents`, agent read fields `effective_cli_path` and `program_warning`, error code `cli_path_invalid`. T8 owns the agent-side parity rows, env masking and the merge/remove contract. |
| Certainty | **Verified** = read in the pinned code or document; **Inferred** = follows from what was read, not run; **Unknown** = not established |

## 1. What T11 delivers

1. **#882 guard fix (A).** A quoted absolute literal such as `'/api/v1/users'` in script text is no longer refused when it stands alone and its top folder cannot exist or be created. Everything else is refused as today, including `../`.
2. **Check CLI route (A).** `POST /system/cli-validate` (it runs the typed program to read its version) accepts only a signed-in person. It runs the shared program-file check before anything starts.
3. **Settings → Third-party agents (B).** One row per CLI (Claude Code, Codex, opencode). Each row has a program path, pre-filled from detection, and free-text parameters. One Save button saves the section. The path is validated on save. The agent editor shows the CLI's path and parameters read-only. A run with no saved path is refused with a clear repair message. Using preset parameters writes an audit note. Agents cannot edit presets. Per-agent env and flags work as today (T8).

## 2. Decisions log

| # | Who, when | Decision |
|---|---|---|
| F-1 | Founder, 2026-10-29 | T11 = #1489, #882 narrowed to the shell-guard false blocks, third-party agent setup parity "A: full parity", plus fix the greyed-out screen fields. #1067 closed. |
| F-5/F-6 | Founder, ~05:05Z | **"C: Full parity, no extra ask"**; to the chief: **"yes full parity"** |
| F-10 | Founder via chief, 05:27Z | Agents pick only the CLI. One person-edited Settings value per CLI, pre-filled by filesystem-only detection, validated on save. |
| R1-CRIT | Founder | **"A: Close it"**: the Check CLI route accepts only a signed-in person. It runs the same location, permission and link-chain checks as saving a path, before starting anything. |
| R1-FQ1 | Founder, own words | **"when a new 3rd party agent is configured i need to choose either open code, codex or claude code, the path to the executable is preconfigured under settings including standard parameter like bypasspermissions and similar, additional parameters and environment variables can be added still freely"**. Confirmed with **"A: Yes, exactly"**. |
| R1-FQ3 | Founder | **"A: Narrow fix only"**: keep refusing `../` and record the limitation. |
| R1-FQ4 | Founder | Encrypted private variables for third-party CLIs: **"not now, file issue"** |
| R1-FQ5 | Founder | **"A: Refuse the run"** when no Settings path is saved; ignore legacy per-agent paths; explain the repair. |
| R1-FQ6 | Founder | **"B: No password"**: a signed-in person is enough. |
| R1-FQ7 | Founder | **"A: Hands-on per landing"**: macOS evidence on the exact candidate; no new CI job. |
| R2-FQA | Founder ~08:05Z; chief relay 08:26Z | **"yes all parameters are supported, no warning"**; **"it was just an example, any CLI parameter as free text"** |
| R2-Q3 | Founder ~09:25Z, after the corrected facts | **"A: Allow, I accept the risk"**: any preset parameter, no UI warning, the exposure stated plainly (§5) |
| R2-Q2 | Founder | **"A: Both in part A"**: Slice A builds the person-only gate and the shared file check; T8 reuses both. |
| H-1 | Founder | #1489 converter on hold: **"the dependency to LibreOffice is not good, we need something lightweight"** |
| S-1 | Founder, approving T11 for building | **"do not overcomplicate it, it is a simple setting in the ui for t11, nothing special"** |

## 3. Slice A

### 3.1 Shell-guard narrow fix (#882)

Today `pkg/tools/shell_path_guard.go::guardCommandClassified` refuses ordinary script text such as `'/api/v1/users'` and `'/%s/%d'` as "a path outside the working folder". A real write to `/etc` is correctly refused (Verified: re-check log `882a-guard.log`, exit 0).

**Rule.** In the outside-working-folder branch, before refusing, the guard does **not** refuse a candidate only when **all six** conditions hold:

1. The candidate is the entire content between one matching pair of quotes (the byte before is `'` or `"`, the byte after is the same quote).
2. It contains none of `* ? [ ] { } % $ ` ` ~ \` and no `..` segment.
3. It is not expansion-derived (existing rule).
4. Its first component below the filesystem root does not exist.
5. The gateway's effective user cannot write to the root. On Unix this uses the effective-access check from `golang.org/x/sys/unix`. On Windows the root always counts as writable, so the rule is off.
6. **It stands alone.** Skipping all whitespace including newlines, the nearest character before the opening quote is one of `( , = [ { :` or the start of the shell word. The nearest character after the closing quote is one of `) , ] } ;` or the end of the shell word. This refuses adjacent literals (`'/tm' 'p/x'`, also across a newline), `+`, `%`, `.format(...)`, `*`, prefixed literals (`f'…'`, `r'…'`, `b'…'`) and `\` continuations.

The protected-internal-file check, the `../` refusal, command-position handling and every other refusal stay unchanged and run first.

**Recorded limitation.** These stay refused: f-strings, templates, concatenations, globs, `../`, everything on Windows, and everything when the gateway runs as root. Workaround: write the script with `write_file`, then run `python3 script.py`. The guard reads command text only.

### 3.2 Check CLI route: person only, file check first

Today the route runs a caller-supplied path. It is open to anyone who can create an agent, and in development-bypass mode it does not even require a signed-in person (Verified: `pkg/gateway/rest_clivalidate.go::validateCLI`; ADR-030 §11).

| New shared piece (Slice A; T8 reuses both) | Behaviour |
|---|---|
| `pkg/gateway/middleware/person_session.go::RequirePersonSession` | Lives beside `bypass_gate.go::RequireNotBypass` and uses `pkg/gateway/ctxkey` (Verified: that import already exists there). Returns 403 before reading the body for: development-bypass requests, requests carrying `ctxkey.CLITokenContextKey`, and requests with no user. Applied as `a.withAuth(middleware.RequirePersonSession(h))`. |
| `pkg/clitrust/check.go::CheckProgramPath(path, forbidden)` → resolved path | The path must be absolute. Resolves the link chain one link at a time. Refuses when the final file, any link, or any link's folder lies inside a forbidden root, or is writable by others. Also refuses when the final file is not a regular executable for the gateway user. On Windows: absolute drive or UNC path, `.exe`/`.cmd`/`.bat` only, no 8.3 short names, no alternate data streams. Callers launch the returned file, never a second resolution. Platform files: `check_unix.go`, `check_windows.go`. |
| `pkg/clitrust/roots.go::AgentWritableRoots(home, cfg)` | `$OMNIPUS_HOME`, every workspace folder and every workspace mount root. |

The route runs: person gate → `CheckProgramPath` → existing version handshake on the returned file. A refusal returns the new reason `path-not-allowed` and starts nothing. The existing rate limit, in-flight cap, scrubbed environment and audit stay as they are. Detection (`GET /system/cli-detect`) stays filesystem-only.

## 4. Slice B: Settings → Third-party agents

| Element | Behaviour |
|---|---|
| Rows | Claude Code, Codex, opencode |
| Program path | Saved value, or "Not set". Detection (`GET /system/cli-detect`, filesystem-only, never starts a program) pre-fills the field only when nothing is saved and the field is untouched. A "Found at …" hint shows what detection found. |
| Parameters | Free text: any CLI parameter, shown in full, no warning (R2-FQA, R2-Q3) |
| Check CLI | Optional button per row. Runs the Slice A route on the **typed** path and saves nothing. |
| Save | One Save for the whole section → `PUT /settings/third-party-agents`. A signed-in person only (`RequirePersonSession`), no password. Each path is validated with `CheckProgramPath` + `AgentWritableRoots`. A refusal (`cli_path_invalid`, naming the CLI) shows under that row's field, and nothing is saved. The path is stored as typed; the check runs on save and again before every run (T8). |
| Agent editor (Runtime tab) | Shows the CLI's saved path and parameters **read-only**, with a link to Settings. Per-agent environment values and extra flags stay editable as today. T8 provides the descriptor rows and env masking that fix the greyed-out fields. The editor no longer runs a sign-in test before saving: the current pre-save `testAgentRunner` gate in `AgentProfile.tsx` is removed (Verified that it exists), so a person can always save the key that fixes a broken sign-in. |
| No saved path | A run of that CLI is refused: "No program set for Codex. A person sets it in Settings → Third-party agents." Legacy per-agent paths are ignored (R1-FQ5). |
| Run order | Driver's own flags → agent flags (filtered as today by `pkg/agent/runner/argsafety.go`) → preset parameters (not filtered, applied last) |
| Audit | Every run that applies preset parameters writes one audit entry saying in plain text "person preset used; flag filter bypassed for preset parameters". The parameter text passes through the sensitive-data filter. |
| Agents | Cannot edit presets by any route. `update_agent`/`create_agent` have no path or preset fields, and `set_config` already refuses `sandbox.*` (Verified: `pkg/sysagent/tools/config.go::blockedConfigKeys`). Agents may read the effective path. |
| Smoke test / command preview (X4) | Remove the free `cli_path` field from both requests. The routes use the saved Settings path for the request's CLI. |

UI states (kept minimal):
- **Loading:** skeleton.
- **Load error:** error state with Retry.
- **Saving:** the Save button is busy.
- **Saved:** announced.
- **Refused:** the message appears under the field; the draft is kept.
- **Signed out:** "sign in again"; the draft is kept.

Keyboard and screen reader:
- Every control is reachable by keyboard.
- Names are qualified by CLI: "Codex program path", "Codex parameters", "Check Codex CLI".
- Results are announced politely.
- When a save is refused, focus moves to the refused field.

Use catalogue components only (Field, Input, Button, Skeleton, QueryErrorState). Gates: `npm run typecheck`, `npm run lint:design-system-locks`.

## 5. Security points (founder-decided)

| Point | Statement |
|---|---|
| Who can run a candidate program | Only a signed-in person. CLI-token and development-bypass requests are refused before any work. |
| What can be run | Only a file that passes `CheckProgramPath`: not in the Omnipus home, a workspace or a mount, not reachable through a link placed there, and not writable by others. |
| Who sets program paths and parameters | Only a person, in Settings. |
| **Accepted risk (R2-Q3)** | **Omnipus does not sandbox external CLIs.** They start directly in their working folder with a scrubbed environment and an egress proxy, not under an Omnipus filesystem confiner (`pkg/agent/external_dispatch.go::prepareRunOptions`, Verified). A working folder is not a boundary. Claude Code already runs with its own permissions bypassed by the driver (Verified, `driver_claude.go`). A Codex preset such as `--dangerously-bypass-approvals-and-sandbox` removes Codex's own sandbox; Codex can then read and write anything the operating-system user account running Omnipus can. The founder accepted this; no UI warning. |
| Env values | Masked and redacted per T8. No encrypted CLI secrets yet (issue to be filed per R1-FQ4). |

## 6. Contract changes (Hard Constraint #8)

| ID | Slice | File | Change |
|---|---|---|---|
| C-A1 | A | `contracts/openapi.yaml` `POST /system/cli-validate` | Add a `403` response: requires a signed-in person |
| C-A2 | A | `contracts/components/schemas/CliValidateResponse.yaml` | Add reason `path-not-allowed` |
| C-B1 | B | `ExecutorSmokeTestRequest.yaml`, `ExecutorCommandPreviewRequest.yaml` | Remove `cli_path` |
| T8 | B | `ThirdPartyAgentSettings` (path and parameters per CLI), routes, `effective_cli_path`, `program_warning`, `cli_path_invalid` | Owned by T8 (rev 5.3) |

Order: (1) edit the schemas → (2) edit `openapi.yaml` → (3) run `scripts/gen-contracts.sh` → (4) make one atomic commit with `pkg/api/generated/` and `src/lib/api/generated/` → (5) handlers and SPA use generated types only. `make verify-contracts` must be green. C-B1 lands after T8's contract commit.

## 7. Reachability

| Capability | Who | How it is reached | Proof |
|---|---|---|---|
| Guard fix | Any agent with `bash` | Existing `bash` tool (registered for every agent; ceiling `ask`, ADR-092) | U-1, U-2 |
| Check CLI | Signed-in person | Existing route; the CLI-path hint in the agent editor (Slice A) and the Settings row (Slice B) | U-3 |
| Settings section | Signed-in person | New `src/components/settings/ThirdPartyAgentsSection.tsx` in the Settings navigation | U-4 |
| Read-only preset in editor | Person | Workspace → Team → worker → Runtime tab (`src/components/agents/AgentProfile.tsx`) | U-5 |
| Run with preset / refusal | Agents delegating to a third-party worker | Existing delegation; T8 spawn resolution | U-6 |

Delivery is reported as two separate lines: *code correct and tested* and *reachable by a user/agent*.

## 8. Requirements, acceptance and tests

| FR | Requirement | Acceptance (Given / When / Then) | Tests (written to fail first) |
|---|---|---|---|
| FR-1 | Guard does not refuse a candidate meeting the six §3.1 conditions | Given `python3 -c "print('/api/v1/users')"` on macOS or non-root Linux / When checked / Then not refused | `TestGuard_StandAloneQuotedLiteralsNotRefused` |
| FR-2 | Guard refuses everything else as today | Given `open('/etc/x','w')`, `echo x > ~/x`, `cp a /tmp/x`, `'/tm' 'p/x'`, `'/tm' + 'p/x'`, `f'{b}/api'`, `'/%s/%d'`, `'/Us*/x'`, `'../shared/x'` / When checked / Then refused | `TestGuard_RealOutsideWritesStillRefused`, `TestGuard_FragmentsConcatenationTraversalRefused`, existing guard tests unchanged |
| FR-3 | Rule off on Windows and for a root-run gateway | Given the root is writable / When `'/api/v1/users'` is checked / Then refused | `TestGuard_RuleOffWhenRootWritable`, `TestRootWritableProbe_PerPlatform` |
| FR-4 | Check CLI refuses non-person callers with 403 before any work | Given a CLI-token or bypass request / When it calls the route / Then 403 and no program starts | `TestPersonSessionMiddleware_Matrix`, `TestCliValidate_NonPersonRefusedZeroSpawn` |
| FR-5 | Check CLI runs `CheckProgramPath` before starting anything and launches the returned file | Given a person and a candidate inside a workspace, or a link into one, or one writable by others / When checked / Then `path-not-allowed` and no marker file | `TestCheckProgramPath_Unix`, `TestCheckProgramPath_Windows`, `TestCliValidate_SharedCheckBeforeSpawn` |
| FR-6 | Settings shows three rows with path, parameters and detection pre-fill; detection never starts a program | Given `claude` and `codex` installed / When the section opens / Then paths are pre-filled, nothing is saved and no program has started | `ThirdPartyAgentsSection.test.tsx`; e2e `settings-third-party-agents.spec.ts` (fake `codex` writes a marker if run) |
| FR-7 | Save validates each path; a refusal shows under the field and nothing is saved | Given a path inside a workspace / When Save / Then the refusal shows and the stored settings are unchanged | `ThirdPartyAgentsSection.test.tsx`; T8 PUT tests |
| FR-8 | The editor shows the preset read-only, keeps env and flags editable, and has no pre-save sign-in gate | Given a Codex worker / When Runtime opens / Then the preset line is read-only; saving a new key sends no runner test first | `AgentProfile.presetLine.test.tsx`, `AgentProfile.noPreSaveGate.test.tsx` |
| FR-9 | No saved path → the run is refused with the repair message | Given no Codex path / When a Codex worker is delegated to / Then it is refused with the message from §4 | T8 resolution test; U-6 |
| FR-10 | Preset parameters are applied last and not filtered; agent flags are filtered; one audit note per run | Given the Codex preset `--dangerously-bypass-approvals-and-sandbox` and a denied agent flag / When it runs / Then argv ends with the preset parameter, the agent flag is dropped, and the audit note is present | `TestPresetParams_AppliedLastUnfiltered_AgentFlagsFiltered`, `TestPresetAudit_RecordsPresetUse` |
| FR-11 | Agents cannot change presets | Given Omnipus calls `update_agent` with path or preset fields, or `set_config` on `sandbox.third_party_agents` / When validated / Then refused | T8 A-T27/A-T30 |
| FR-12 | Smoke test and preview take no free path | Given a request with `cli_path` / When received / Then 400 | `TestSmokeAndPreview_RejectFreePath` |
| FR-13 | User docs updated in the same change (§10) | — | docs-verifier |

Linux runs all tests in candidate CI. Windows adds the guard, `clitrust` and `cli-validate` tests to the existing Windows job (named `-run` per package; fake programs built from Go, not shell scripts). macOS adds them to the existing `phase3-macos.yml` core job. No new CI job.

## 9. Hands-on rows (macOS, exact candidate)

Provider/model for agent-driven rows: `openrouter` + `deepseek/deepseek-v4.1-flash`. One tester and an independent validator. Slice B touches runner paths, so the agent-engine hands-on rule applies.

| Row | Slice | Steps | PASS evidence |
|---|---|---|---|
| U-1 | A | The agent runs `python3 -c "print('/api/v1/users')"` and a heredoc script whose dict holds `"api": "/api/v1"` | Not refused |
| U-2 | A | The agent tries to write `~/t11-outside.txt` and runs `open('/tm' 'p/t11probe','w')` | Write refused or escalated (tester declines); concatenation refused; neither file exists |
| U-3 | A | Validator calls Check CLI with the CLI token and in bypass mode; as a person, checks a marker-writing program inside a workspace and an allowed program | 403 twice; `path-not-allowed`; marker absent; version shown for the allowed program |
| U-4 | B | Settings → Third-party agents on a Mac with Claude and Codex installed: save both pre-filled paths, add the Codex parameter `--dangerously-bypass-approvals-and-sandbox`, try a workspace path | Pre-filled; parameter shown in full with no warning; workspace path refused; saved values persist after reload |
| U-5 | B | Workspace → Team → Codex worker → Runtime: add a key, save, reload | Preset shown read-only; key saved (no pre-save test); value masked after reload |
| U-6 | B | Delegate to the Codex worker with the preset; then clear the Codex path and delegate again | First run's audit has the preset note; second run refused with the repair message |

## 10. User-facing documentation TODO

| Page | Slice | TODO |
|---|---|---|
| `docs/tools.md`, `docs/troubleshooting.md` | A | Which quoted literals the guard no longer refuses, what stays refused, and the `write_file` workaround |
| `docs/security.md` | A + B | Check CLI is person-only and checks the file first. Presets are person-only and may hold any CLI parameter. **External CLIs are not sandboxed by Omnipus; a Codex bypass preset lets Codex read and write anything your user account can.** Preset use is audited. |
| `docs/operations/sandbox-limitations.md` | A + B | Guard rule is off on Windows and for a root-run gateway; `../` limitation; external CLIs run outside the Omnipus sandbox |
| `docs/settings.md` | B | Third-party agents section: rows, detection, parameters, Check CLI, Save |
| `docs/agents.md` | B | Third-party workers take their program and parameters from Settings (read-only in the editor); env and flags are per agent; repair message when no path is set |

## 11. Work plan

| Package | Owner | Depends on |
|---|---|---|
| A-tests (FR-1…FR-5) | qa-lead (one test author; CHECK by a different instance) | this spec |
| A-code: guard rule; `RequirePersonSession`; `pkg/clitrust`; route wiring; C-A1/C-A2; CI additions; docs | backend developer | A-tests |
| A-UI: the CLI-path hint shows `path-not-allowed` | frontend developer | C-A1/C-A2 commit |
| A-gate | one 5-reviewer gate over Slice A, then hands-on U-1…U-3 | — |
| B-tests (FR-6…FR-12) | qa-lead | T8 contract commit |
| B-code: C-B1; preset end-to-end and audit tests over T8's resolver | backend developer | T8 contract and backend |
| B-UI: Settings section; Runtime read-only preset line; remove the pre-save gate | frontend developer (loads `omnipus-design-system`) | T8 contract (mocks until the backend lands) |
| B-gate | one gate over the combined T8-backend + T11-B candidate, then U-4…U-6 | same train as T8's backend |
| ADR amendments | architect: ADR-030 §11 (person-only + file check), ADR-068 (§3.1 rule and `../` limitation), ADR-032 (preset parameters unfiltered, accepted risk) — dated corrections | A, B |

## 12. Not in T11

| Item | Where it goes |
|---|---|
| #1489 PowerPoint→PDF (`convert_document`) | On hold; being redesigned as `preview_document` with installer-managed LibreOffice + pdfium |
| `test_agent_runner` chat tool | Later; the existing Runtime-tab test buttons remain |
| Three-part sign-in status and smoke-outcome reshaping | Later |
| Diagnostic snapshot redesign (workspace authority, result identity, late-result ordering) | Later |
| Per-row save with revision/409 conflicts | Not needed: one whole-section save |
| Smoke-test/preview contract changes beyond removing `cli_path` | Later |
| Windows receipt script beyond the normal named tests | Not needed |
| Encrypted private variables for CLIs | Issue to be filed (R1-FQ4) |
| `../` traversal resolution | Not planned (R1-FQ3) |

## 13. Evidence

| Claim | Evidence | Certainty |
|---|---|---|
| Guard false blocks and the correct `/etc` block | `shell_path_guard.go::guardCommandClassified`, `absolutePathPattern`, `checkPathSegment`; re-check log `882a-guard.log` | Verified |
| Adjacent-literal bypass of the earlier rule | Grill round 2 R2-MAJ-001 (`ast.parse` counterexample) | Verified (reviewer) |
| Check CLI is create-parity and spawns without a location check | `rest_clivalidate.go::validateCLI`; ADR-030 §11 | Verified |
| CLI-token marker and bypass middleware exist; middleware imports `ctxkey` | `auth.go::AuthResult.ViaCLIToken`, `ctxkey/ctxkey.go`, `middleware/bypass_gate.go` | Verified |
| Editor pre-save runner gate | `AgentProfile.tsx` (`testAgentRunner(agentId)`, "Fix the runner before saving") | Verified |
| `set_config` refuses `sandbox.*` | `sysagent/tools/config.go::blockedConfigKeys` | Verified |
| External CLIs run outside an Omnipus filesystem confiner | `external_dispatch.go::prepareRunOptions`; grill round 2 E9 (`driver_codex.go`) | Verified / reviewer-verified |
| T8 rev 5.3 contents | Relayed by the coordinator | Unknown until its contract commit exists |
