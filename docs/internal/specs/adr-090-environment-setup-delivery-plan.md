# ADR-090 — Generic installation delivery plan

Status: implementation and required verification complete, 2026-09-18, within the platform boundaries in the [final verification record](adr-090-environment-setup-verification.md). Historical progress checkpoints below are retained as evidence, not current outstanding work. No commit, push or release is claimed. This plan supersedes the earlier package-recipe implementation plan. The founder selected agent-supplied installation commands/scripts (option A). [Environment setup specification](adr-090-environment-setup-spec.md) is authoritative.

## Scope

One generic deferred environment_setup tool, existing global and per-agent Ask configuration, agent-supplied command/script visible for approval, workspace default and explicit shared destination, background run/poll/read/kill, Admin cross-workspace authority without membership. Package choices and installation knowledge belong in skills/task plans. No hardcoded library names, versions, native application catalogue or installation recipes. No new approval workflow or permission engine. Existing unattended Ask auto-denial remains.

## Parallel lanes

| Lane | Ownership | Completion evidence |
|---|---|---|
| Generic storage | Generic installation area/path/publication helpers; remove obsolete recipe engine; documentruntime reusable converter integration | Path confinement, shared failure preservation, concurrent target handling, no package catalogue |
| Generic tool | Tool schema/execution/authority; reuse existing background sessions and sandbox runner | Approved generic command starts once; denial has no effects; real status/output/cancel; cross-workspace authority |
| Generic runtime | Shell helper reuse, per-turn paths, live and metadata registration; remove Admin finalizer and writable shared route | Tool discoverable and runnable; same agent in distinct workspaces isolated; shared ordinary access read-only |
| Policy configuration | Existing global/default/role policy data and tests only | Global Ask plus explicit permitted-agent Ask; Deny matrix and saved overrides preserved; custom creation/preview agreement |
| Generic frontend | Existing approval preview and tests | Exact command/script, purpose, scope and correct destination visible; existing approval controls unchanged |
| Generic guidance | Prompts, portable document skills and integration instructions | No Admin handoff or package-recipe assumptions; generic setup and actual document inspection workflow |
| Parent verification | Read-only integration checks and independent review delegation | Actual registered-tool/approval tests, sandbox commands, document generation/rendering/visual correction, honest platform evidence |

All development and fixes use claudez with glm-5.3-flash. Parent does not write product code. If the provider reaches its five-hour usage limit, use the user-authorized Terra fallback within available harness concurrency.

## Sequencing

1. Publish minimal generic storage/tool interfaces; policy, frontend and guidance work independently.
2. Connect tool to existing runner/session/sandbox facilities and register it for all native agents, with policy determining access.
3. Remove superseded package-recipe files, Admin finalizer/write route and old instructions. Retain reusable session, boundary, reader and converter work.
4. Run focused tests serially under the shared Go-test lock; frontend checks can run alongside them. No full local Go suite.
5. Delegate independent implementation review; delegate every fix back to GLM workers. Verify current source and logs, not just worker reports.
6. Exercise normal approval, unattended denial, unknown dependency/custom script, Admin target selection, cancellation, malicious writes, concurrent installs and real Office workflows. Reusing an existing runtime is not proof of fresh generic installation.

## Delivery gates

- ES-BDD-01 through 12 and ES-SC-01 through 04 remain unproven until current-state evidence is recorded.
- Generic scripts return truthful exit/output/paths. Script success is not dependency readiness or completed visual inspection.
- Document skills may specify LibreOffice, libraries and fonts; production installation tool must not special-case them.
- Preserve the existing 37 upfront tools and intentionally non-deniable ToolSearch.
- Record platform-specific evidence honestly; simulated modes/cross-compilation are not native certification.
- Preserve unrelated dirty work and user-deferred issue #731 scope. No CI/push/release is claimed by this plan.

The earlier Fable REVISE review concerned the superseded structured-recipe design; some boundary findings remain relevant, but its proposed package recipe catalogue is superseded by the founder's generic-install decision. A fresh implementation review is required.

## Verification checkpoint — 2026-09-18

The approval-preview fix has completed. Its regression reproduction failed in the three intended cases before the fix; afterward, all 35 focused tests and all 604 tests in the agents UI group passed. The full frontend typecheck (`tsc -b --noEmit`) exited 0. The parent inspected the changed guard and regression assertions, the captured exit codes and logs, and the terminal CLI result confirming `glm-5.3-flash`. This proves the tested UI behavior, not live approval-to-installation reachability.

The repository guard against forbidden per-agent policy backfill also passed. Selected backend checks now pass: setup defaults and affected prompts in coreagent, effective setup permissions and document skill guidance in tools, and additive shared publication in environmentsetup. Full installation execution and workflow acceptance remain unverified. Additional parallel lanes cover invalid-input tests, security/lifecycle review, policy/guidance review, registered approval/sandbox acceptance, and Office workflow acceptance. Storage confinement, truthful publication/cleanup results and same-workspace installation serialization are required before closure.

The original storage lane passed its package tests and removed the rejected recipe engine and Admin finalizer/write APIs. Parent review rejected its completion claim because descendant symlink confinement, swallowed control-file errors, oversize truncation and workspace installation locking remained unresolved. A dedicated GLM storage-fix lane now owns those corrections and their negative tests.

## Independent review and corrective lanes

The security/lifecycle review returned BLOCK. Confirmed findings include the derived Linux sandbox policy not being passed at process launch, workspace descendant links escaping the intended installation area, premature success reporting before shared publication, hidden cleanup failures, surviving installer children, and Admin targets using a directory different from normal workspace execution. All remain open until their fixes and meaningful negative checks are verified. The application-layer ReadConfined flag is not a kernel boundary; changing it alone would not fix the launch defect.

To parallelize correction, tool-input-fix owns input validation and Admin destination resolution; tool-lifecycle-fix owns runner/session completion, cleanup and child-process behavior; storage-fix owns directory confinement, control-file handling and same-target locking. The runtime lane owns the corresponding Bash launch-policy wiring. Independent validation tests reproduced six input-contract failures. A separate policy-review-fix lane owns the reproduced catalog arithmetic failure (109 versus the actual 110 tools) and the small prompt-test review finding. Existing successful checks are retained, not treated as proof of these missing behaviors.

Policy/guidance review closure: the catalog arithmetic and unsupported negative prompt-test assertions are corrected. The previously failing catalog test now passes, along with the related catalog and worker-prompt tests. Parent source/log inspection confirms these closures; installer security findings remain separate and open. Docker is available with Linux kernel 6.12.76-linuxkit, and a dedicated GLM lane is preparing real Linux child-confinement checks in an isolated container.

### Additional verification findings — 2026-09-18

The independent runtime review found a foreground child-environment regression: when a generic runtime exists but the document layer is unavailable, composing PATH over a nil environment materializes the full host environment, bypassing the intended scrubbed environment after merge. Parent verified `composeExecutionEnv`, `setEnvValue`, `ExecTool.executeRun`, and `sandbox.mergeEnv`. A dedicated GLM runtime-review-fix lane owns the fix and a secret-sentinel regression. It also owns using the same resolved runtime paths for environment and kernel policy. This is open until source and regression evidence are reviewed.

Fresh-package-acceptance is a separate GLM lane proving a real package-manager command through registered Ask for a custom agent without delegation links, followed by actual package use from ordinary sandboxed execution. Existing prepared document-runtime fixtures do not satisfy this claim.

The parent also identified an acceptance-runner root-path error causing a wait for a lock beneath a nonexistent directory. Only that erroneous wait subprocess was stopped; the owning GLM worker remains responsible for correcting the runner and performing acceptance. No result from that wait counts as verification.

### Runtime secret-scrubbing regression — verified checkpoint

The runtime worker reproduced R1 with two direct environment-composition failures and two real foreground-child failures using fake sentinel secrets (no real credentials). After the fix, the same focused tests passed: `runtime-review-fix-green.log`, exit0, pkg/tools2.463s, regex `^TestComposeExecutionEnv|^TestBashForegroundChildEnv|^TestBashBackgroundChildEnv|^TestScrubbedEnvGodMode`. Parent inspected `composeExecutionEnv`: nil-base composition now uses `sandbox.ScrubGatewayEnv`, preserving the normal PATH tail and empty-composition behavior. R2 (one runtime snapshot for environment and kernel grants) and final review remain open; this checkpoint is not whole-delivery completion.

### Admin first-use and shared-prefix regression checkpoint

Parent inspected `tool-input-fix-target-green12.log`: target tests pass (pkg/tools2.426s, exit0), including valid first-use Admin target work-directory initialization and real storage visibility through the target member's `RuntimeEnvPaths.WorkspacePrefix`. The artifact is written through real storage, not a live installer process; registered Admin-install/member-execution acceptance remains separate.

After anchoring shared creation at the application data root, a relative-path regression created generation/cache/tmp beside the store. A direct existence probe reproduced it, and `beginSharedInstall` now includes `storeRelative` in the relative creation path. Parent inspected the fix and `storage-fix-repro.log`: direct prefix probe plus nonempty-tree regression pass (0.660s, exit0). Final full storage package rerun and independent closure remain pending.

### Confirmed scope — Bash process controls, 2026-09-18

The founder selected option A: Bash with enhanced installation permission. The detached-process supervision decision is resolved; no additional supervisor is required. Existing child restrictions, ordinary cancellation controls, truthful results and the remaining installation/runtime acceptance checks still apply. This supersedes the earlier requirement to guarantee cleanup of deliberately detached installer processes. See ES-FR-03 and ADR-090 §6.5.

## Current acceptance checkpoint — 2026-09-18 13:12

Code correctness and testing: partial; correction and verification remain active.

Reachable by a user/agent: scripted registered-tool document workflows are exercised, but the new installation approval-to-use workflow is not yet accepted end to end.

| Area | Verified evidence | Remaining closure |
|---|---|---|
| Configuration and approval preview | Focused policy tests, 35 preview tests, 604 agent UI tests and frontend typecheck passed | Real browser approval and denial through installation |
| Input and Admin targeting | Input validation and target tests passed, including creation of the target work directory before a first member turn | Live installation followed by member execution |
| Storage | Confined shared paths, additive publication and immediate prefix creation passed package tests | Newly identified Windows mutex deadlock and writable-prefix lock replacement; recheck after fixes |
| Ordinary execution environment | Secret-scrubbing and one-snapshot runtime tests passed; deliberate regressions were detected | Final restored-source regression run, including replacement of the obsolete recipe-layout fixture |
| Installation lifecycle | Tests expose premature/incorrect completion and cleanup reporting; corrective changes active | Successful and failed outcomes, cancellation, runtime reuse, target reads and platform controls |
| Linux confinement | Real non-root Landlock baseline reproduced unauthorized sibling/store access in the pre-fix binary | Rebuilt current-source positive and negative scenarios |
| Document visual workflow | Scripted Mia, worker and custom-agent generation/render/read workflow passed across four formats | Complete live-model final-image inspection; fresh generic dependency installation is separate |

Eight CLI GLM 5.3 Flash lanes are active: lifecycle, storage locking, runtime fixes, registered-loop acceptance, browser acceptance, Linux acceptance, fresh-package acceptance and live visual inspection. No provider usage limit has been observed. Compile failures during active multi-file edits are not final acceptance results; final evidence must identify the source used to build the tested binary. The founder's option-A decision is settled and is not an outstanding approval.

## Linux execution checkpoint — 2026-09-18 13:41

The user-authorized Terra fallback was activated only after explicit GLM five-hour quota errors. Three Terra workers continue product fixes, browser acceptance and fresh-package/registered-loop acceptance; the parent remains an orchestrator and verifier.

After setup-policy correction, the parent executed the existing non-root Linux Landlock harness. All three scenarios passed and the wrapper exited 0: workspace installation, additive shared installation and Admin targeting another workspace. The earlier shared-store write failure is no longer reproduced by these scenarios. Two harness assertions first required correction to expect the exact trailing newlines their fixture scripts write; access-boundary assertions were unchanged. Evidence is retained in the delivery artifacts under linux-acceptance/runs/20260918-134103, with binary and runner hashes in its summary. This proves those Linux scenarios, not complete feature acceptance. Browser, fresh-package and full live document inspection remain pending.

## Latest verification checkpoint — 2026-09-18, after Terra corrections

This checkpoint supersedes the outstanding-state descriptions above; earlier failed runs remain evidence of the defects they exposed.

Code correctness and testing: installer corrections pass the full focused `TestEnvironmentSetup` group (6.993s); final workflow acceptance remains active.

Reachable by a user/agent: real browser denial, approved workspace installation, approved shared installation and cancellation pass individually. Standalone Admin chat is supported by the backend but lacks a reachable UI entry point; correction is delegated without changing Admin's prohibition on workspace membership.

| Area | Current evidence | Remaining work |
|---|---|---|
| Installation boundaries | Final Linux run `20260918-134520` passes all three scenarios after inherited write grants were stripped and only installation destinations restored | Preserve this boundary during remaining changes |
| Storage and ordinary execution | Storage regression checks and restored runtime regressions pass; Windows lock casing corrected and cross-compilation passes | Native Windows execution is not certified |
| Session results | Nonzero completion and truncated output retain honest failure/publication information; full focused installer suite passes | Independent final review and workflow evidence consolidation |
| Browser installation | R1 denial, R2 workspace approval, R4 shared approval and R5 cancellation pass | R3 standalone Admin UI reachability and execution |
| Fresh package | Actual package installation and ordinary sandbox import exercised; denial passes | Finish approval-count oracle distinguishing synthetic management callbacks from actual user prompts |
| Registered-loop workflow | Five of six scenarios pass; Admin's target installation exists | Correct S4 fixture: its non-chat-target worker message routed to Mia in another workspace; this is not evidence of a runtime product defect |
| Live document inspection | Correct subject-local images reach the real vision model | Latest run inspected all initial formats but omitted corrected DOCX output; complete independent subject coverage and final-image checks without relaxing the oracle |

Three Terra workers remain responsible for implementation and acceptance. The parent performs orchestration, documentation and verification. No new user decision is required, and no commit, push, release or complete acceptance is claimed.

Subsequent evidence: registered-loop S4 passes in retained run `3898980674` with a real chat-target member in workspace B. The target member's execution finds and runs Admin's installed executable. The earlier failure was a fixture routing error, not a product runtime defect. Full combined acceptance remains in progress.

The live DOCX failure was also a fixture error: LibreOffice automatically neutralized the intended width defect, leaving a legible page. The model correctly judged that page acceptable. Parent inspection confirmed the initial Word rendering and the corrected XLSX, PPTX and PDF renderings are legible. A replacement Word sample must visibly exhibit its intended defect before using it to judge model correction; a prompt demanding unnecessary changes would not be valid evidence.

## Live inspection closure checkpoint

Independent real-model runs completed for the custom agent (`3520291023`), Mia (`1578283771`) and General Purpose (`57029505`). Parent inspection of all twelve final images rejected the worker spreadsheet because its required word “overview” was still clipped, despite the automated transport checks passing. A targeted worker spreadsheet follow-up (`3482137074`) produced the complete, legible title and marker; the parent inspected and accepted that image. The other eleven accepted artifacts remain from their original runs. Failed attempts are retained rather than merged into a fictional single successful run.

Outgoing image checks distinguish source-file hashes from PNG re-encoding: decoded pixels must match the final file and a correlated image sent to the model. These runs prove visual inspection and correction on the tested prepared rendering runtime, not fresh installation of that runtime, pixels-only diagnosis, or universal document quality. The real model also had access to generator source. Fresh generic package installation is separately demonstrated by the installation acceptance runs.

Browser R3 found the standalone Admin entry creates the correct workspace-free session but can remain stuck loading history. A connection-readiness correction has focused regression coverage; real browser closure remains pending. No overall completion is claimed.

## Final audit — explicit remaining acceptance requirements

Browser R3 is now closed by run `20260918-141714`: the normal standalone Admin route accepts a prompt, the approval shows the human-readable target workspace name, and the installed executable runs from that target. Mia's corrected fresh-package fixture also passes (`1909144494`), including agreement between response workspace name and installation root. The UI corrections pass 47 focused tests and typechecking.

The requirement-by-requirement audit nevertheless found missing evidence, which prevents overall closure:

1. The required deliberate mutations for execution-before-approval, unauthorized target installation and false readiness were not covered by the existing environment/PATH mutation tests. A dedicated lane must demonstrate that the appropriate real oracles reject each deliberate fault, restore source exactly, and pass again.
2. Existing rendered document fixtures are single-page, single-sheet/slide and use static spreadsheet values. A document-edge lane must execute multi-page, non-ASCII, two-sheet/two-slide and actual spreadsheet recalculation cases, plus controlled missing-rasterizer/font, nonvision and denied-document-tool cases.
3. Fresh-package installation and prepared-runtime document generation are individually proven, but do not establish the complete missing-authoring-dependency → Ask installation → ordinary sandbox document workflow for Mia, General Purpose and a custom agent. A separate integrated lane owns that chain, retaining already-installed native rendering tools as explicit prerequisites.

These are verification gaps, not assumed product defects. Existing successful runs remain valid within their recorded scope. All three lanes use the authorized Terra fallback, preserve failed evidence, and leave product changes to delegated workers only if a real defect is demonstrated.

Deliberate-fault verification is now closed: bypassing Ask denial caused the real denied request to return an installation session; bypassing the target authority check caused unauthorized allocation; suppressing the nonzero-exit error flag caused an exit-7 installation to be reported without an error. Each expected assertion failed, each source was restored byte-for-byte, and each restored check passed. The parent independently verified all original hashes. Evidence is in the delivery artifact directory `mutation-audit-20260918`.

Document-edge package smoke checks generated two-page DOCX/PDF, two-sheet XLSX with a preserved formula and recalculated value 42, and two-slide PPTX. These host checks do not close the sandbox workflow requirement. Parent visual inspection also found missing Japanese glyphs in the prepared-font fixture, which remains an explicit incomplete-rendering example. Actual registered-tool negative checks and the sandbox install-to-document chain are still active.

The negative cases now have actual production-path evidence: verbose selected tests execute real file/tool-policy denial with controls, converter-readiness failure reporting and provider image rejection without a false visual-completion claim. A real sandbox ExecTool call also reports `is_error=true` and exit 1 when the rasterizer module is unavailable. Live vision run `1181052096` received the Word page and explicitly reported that the Japanese text exists in the PDF text layer but is not visibly rendered, leaving its visual validation incomplete. These replace the earlier host-only negative probes as evidence. The complete sandbox installation-to-document chain remains the final active acceptance item.

## Final closure

Custom and Mia installation-to-document chains pass in `3189892863`. The General Purpose worker uses real attended delegation and passes in `967567453`; its scheduled entry correctly refuses unattended Ask in the separate negative case. All three demonstrate real authoring-package installation, imports from the approved prefix, ordinary sandbox generation/rendering and image reading. The final worker artifact contains the actual outgoing image bytes; the parent decoded them and verified exact pixel equality with the generated page.

Multi-page Word/PDF, two-sheet formula recalculation, two-slide output and Latin Unicode are exercised inside the sandbox. The parent inspected the pages, including the corrected spreadsheet label and embedded-font PDF output. Missing Japanese glyphs remain an explicit negative font case, not a false positive. The completed independent review, meaningful mutation checks, retained failures and supported-platform limitations are summarized in the final verification record. No required implementation or acceptance task remains in this scope; unrelated deferred defects and release/merge work remain separate.
