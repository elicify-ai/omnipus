# ADR-090 — Environment setup specification

- **Status:** Implemented and verified, 2026-09-18; see the [verification record](adr-090-environment-setup-verification.md) for executed evidence and platform boundaries. Not released by this work.
- **Decision:** [ADR-090 §6.5](../architecture/ADR-090-built-in-agents-skills-and-visual-reading.md).
- **Companions:** [Agent configuration FR-011](adr-090-agent-configuration-and-skills-spec.md), [visual file reading](adr-090-visual-file-reading-spec.md).
- **Scope:** One setup tool using existing Ask permission, workspace-local packages and shared runtimes, including agent Office rendering. No new approval workflow, Admin handoff, delegation requirement, library-preview UI or worktree isolation.

## 1. Decision

Add `environment_setup` as an ordinary native tool and set its permission to **Ask**. The existing tool-approval mechanism is the approval. Do not introduce plan/approval tokens, separate confirmation screens, approval-expiry logic, a second confirmation, or special God-mode behavior. Existing policy resolution, authentication, cancellation and audit behavior apply exactly as for other tools.

Workspace-local installation is the default. Omnipus manages shared runtimes/native tools; agents use them without write access to the shared installation. Any permitted native agent, including a new custom agent with no delegation edges, can use the tool. Assigning a skill does not grant setup, execution, network or file-reading permissions.

## 2. Requirements

### ES-FR-01 — Registration and existing approval

Register environment_setup in the static catalog and deferred discovery tier; keep the 37-tool upfront set unchanged. Ship Ask for the global policy; per-agent defaults follow the founder-confirmed matrix below. Judge and Plan Supervisor retain Deny and their fixed capability sets. Preserve operator policies through the existing reconciliation rules. Use the existing policy resolver and approval execution path; do not implement a second approval check inside the tool. Normal Deny/Ask/Allow and God-mode behavior remain unchanged.

**Founder-confirmed setup permission matrix:**

| Agent | environment_setup default |
|---|---|
| Mia | Ask |
| General Purpose | Ask |
| Admin | Ask |
| Jim | Deny |
| Ava | Deny |
| Planner | Deny |
| Researcher | Deny |
| Judge / Plan Supervisor | Locked Deny |
| Custom native agent | Explicit per-agent Ask, with global Ask |

Founder clarification: use the existing two policy levels. Ship global environment_setup Ask and explicit per-agent Ask for Mia, General Purpose, Admin and new custom native agents. Retain explicit Deny for the other roles in the matrix. Do not derive setup permission from bash permission, skills or agent type. REST creation, create_agent and get_agent_tools previews must agree. Keep the explicit Ask posture when reconciling sparse ordinary-role policies even when equal to the global value. Preserve saved user overrides; no automatic overwrite on updates. Ordinary settings remain user-editable through the existing resolver, and hidden denial stays locked. Setup permission does not grant execution permission. Global Ask/Deny cannot be weakened by a local Allow; if global later becomes Allow, stored local Ask remains Ask until explicitly changed or removed.

The tool's description and normal approval display show the agent-supplied command/script, stated purpose, workspace and installation scope. Dependency names may appear in the script or purpose; the tool does not parse them into a package catalogue. The user approves or rejects the tool call through the existing mechanism. Rejection does not run installation. Do not treat agent conversation or delegation as approval. All ordinary setup calls under the shipped Ask policy require the existing user approval; already-installed components are normally detected by the agent's readiness check so no setup call is needed to reuse them.

### ES-FR-02 — Inputs and installation scope

An approved installation starts asynchronously and promptly returns a session_id, following the existing Bash background-process pattern. Inputs are the agent-supplied installation command or inline script, a short purpose, scope defaulting to workspace, an optional authorized target workspace, and existing-style timeout/session actions. The command/script itself and destination are visible in the normal approval display. This is executable installation input, not a request looked up in a hardcoded package catalogue. No package-specific recipes, library names or version pins belong in the tool implementation. Skills and the agent's task plan supply installation knowledge; standard package managers, download tools and scripts can be used within the approved setup boundaries.

Canonical tool inputs (one schema shared by discovery, execution, approval preview and tests):

| Field | Type | Requirement/default |
|---|---|---|
| action | string | run (default), poll, read, kill |
| command | string | Required and nonblank for run; inline multiline scripts allowed; maximum 65,536 UTF-8 bytes; reject oversize input without executing or silently truncating it |
| purpose | string | Required and nonblank for run; maximum 500 characters |
| scope | string | workspace (default) or shared; reject other values |
| target_workspace | string | Optional canonical workspace ID, resolved with existing workspace identity/authority rules; never a filesystem path |
| timeout_seconds | integer | Default 1800; 1 through 3600, matching existing Bash bounds |
| session_id | string | Required for poll/read/kill; resolved through existing owned-session access |

The script receives OMNIPUS_ENV_PREFIX as an absolute, pre-created installation destination whose path remains valid after successful publication. OMNIPUS_ENV_CACHE and OMNIPUS_ENV_TMP identify its bounded writable cache/temp directories. The tool derives these paths, not the caller. Show the full command/script in the existing scrollable approval display; never approve a silently truncated replacement. Return actual prefix and usable executable paths without claiming they prove dependency readiness.

Resolve the destination from authenticated workspace context; never accept an arbitrary host path as the installation target. Admin can select another workspace through its cross-workspace filesystem authority without team membership. Other callers require authority for the target workspace. Shared installation must be explicit. Expose the selected installation prefix to the script through a documented environment variable and return actual usable paths. Reuse existing execution/environment facilities; do not introduce another workflow engine or permission system.

The tool cannot infer from arbitrary script exit status that every requested package works. It reports exit status, output, destination and partial changes honestly. The agent verifies the requested dependency in its actual execution environment and records resolved versions/lockfiles where supported by the chosen installer. Generic success does not assert document readiness. Unsupported operating-system or privilege requirements fail visibly; no implicit sudo, system-wide installation, broader scope or unsandboxed retry. A changed installation command is a new normal tool invocation under existing policy.

### ES-FR-03 — Execution boundaries

Workspace packages, Python virtual environments, npm modules, temporary files and writable caches live within the authorized workspace. Shared runtimes and native tools use an Omnipus-managed versioned directory; only the application setup implementation publishes there. Installed shared tools are read/execute-only for agents. Each job is confined to its selected installation destination: a workspace A job cannot modify workspace B's packages. Admin may explicitly select workspace B using its cross-workspace authority, without granting that authority to install hooks or other agents. Setup cannot change an agent's tool policies or delegation graph.

The application runs the approved agent-supplied installation command/script with bounded setup access. Package build/install hooks are untrusted code and must not receive unrestricted host access, unrelated workspace files or gateway secrets. Reuse existing sandbox, network policy, secret handling, execution limits and audit infrastructure. Setup approval authorizes the requested installation, not a general shell escape. Skills/agent instructions must use trustworthy sources and verify downloads where available. Any extraction handled by the application must prevent archive traversal; arbitrary installer scripts remain confined by the setup boundary. No hardcoded package/checksum catalogue is required. Do not disable the sandbox to make a package install succeed.

Prepare shared installations before publication, keep previous working installations on failure and avoid changing paths underneath an executing task. The prefix exposed to an installation script must remain stable after publication because installers can embed absolute paths. Installing a different shared dependency must not hide unrelated earlier installations; expose published paths consistently without package-specific knowledge. Serialize installation into the same target with existing locking primitives; reuse already-compatible installations. Workspace installers must preserve unrelated files and report any partial changes after failure. Return actionable errors for failed downloads, permission denial, insufficient disk space, cancellation or unsupported installation. Reuse the existing background execution/session infrastructure and its poll/read/kill pattern for status, bounded output and cancellation; do not add a new task engine. Session access must validate caller authority and remain bound to the original target. Poll/read do not restart installation. Kill cancels the installer and its child processes, releases locks and reports partial effects. Status and cancellation follow existing tool-policy handling, without an additional installation-approval workflow. Use existing execution limits and interruption/restart behavior; a lost session must never be reported as successful or silently restarted. Installer children inherit the job’s restrictions. Cancellation/timeout must terminate the job’s child process group using existing execution machinery. Founder decision, 2026-09-18: this tool is Bash with enhanced, bounded installation permission. Reuse Bash process controls and verify ordinary child-process cleanup with a real fixture; do not introduce a separate detached-process supervisor. Deliberately detached children have the same cleanup limitations as Bash. Their inherited sandbox restrictions still apply, but command completion and shared publication do not guarantee that every detached child has stopped. Installation guidance must require commands to wait for their installation work to finish. Report this limitation honestly rather than claiming stronger containment. Workspace-local setup preserves existing direct-write Bash semantics: a simultaneously running command may observe package changes, and no transactional workspace snapshot is promised. Shared publication retains the stronger stable-path guarantees above. Retry is a new normal tool call; do not blindly rerun a possibly completed install after interruption.

This requirement governs environment_setup and official dependency workflows. It does not claim that all installation-like behavior in arbitrary code passed to an independently permitted bash tool can be recognized. Existing bash restrictions remain; no generic package-manager bypass is introduced.

### ES-FR-04 — Runtime access and results

Return the background handle at start; expose completion or failure through the session result. Return the executed command, exit status, bounded output, resolved scope and paths, and known failure/partial-change details through existing tool-result conventions. Do not invent package/version/readiness metadata that a generic script did not establish. Do not create a new externally visible setup state machine. Successful setup means the components were installed and checked, not that the user's document or visual verification is complete.

The existing execution tool makes workspace packages and approved shared runtimes available to every permitted native agent. Supply executable/package paths and writable caches/temp directories within the sandbox. Replace Mia/worker/Admin ID checks with context-based runtime availability. External CLI agents are outside this native integration.

After setup, the requesting agent verifies dependencies in its own actual execution environment before resuming its task. A host-only install, PATH entry or installer-side test is insufficient. Setup never grants bash/read_file, broadens normal execution network access or switches the model. If these capabilities are missing, report the specific limitation instead of silently granting them.

### ES-FR-05 — Office generation and visual checking

Document skills declare portable dependencies and their installation guidance; Omnipus's skill guidance points to environment_setup instead of Admin. The agent checks existing components and requests missing ones through the normal Ask-gated tool. Workspace authoring packages are the default; shared runtime/native dependencies must be explicit in the request. Include Node only when a helper actually requires it.

The following names belong to the document skills and test fixtures only; the generic tool must not contain special handling for them.

| Purpose | Dependencies |
|---|---|
| Create/edit Word, Excel, PowerPoint and PDF | Python and appropriate libraries; initially python-docx, openpyxl, python-pptx, reportlab and pypdf |
| Render Office documents | Compatible LibreOffice, its native dependencies and required fonts |
| Convert PDF pages to images | One supported, explicitly installed PDF rasterizer and its dependencies |
| Visually inspect | Existing read_file image support and a vision-capable model; no automatic permission/model changes |

Workflow: check the requesting agent's environment → call environment_setup for missing dependencies → existing Ask approval → background installation → poll/read completion result → check again in the requesting agent's sandbox → create document → convert Office to temporary PDF → render page/sheet/slide images → read and inspect images → correct and rerender → deliver the editable document separately.

Readiness must test actual generation, independent format validation, Office conversion and PDF-to-image rendering, not just imports or LibreOffice --version. Include multi-page, non-ASCII, spreadsheet calculation and slide cases. Installation or image generation alone does not prove visual inspection: real images must reach the model and known layout defects must be detected and corrected. Missing fonts, rendering dependencies, file permission or vision capability leave visual validation explicitly incomplete. An agent may deliver a structurally checked document with that limitation when generation succeeds.

Rejected or failed setup leaves the task resumable, without an Admin handoff or automatic repeated approval prompts. Unattended or scheduled execution keeps the existing automatic denial when Ask cannot be presented; do not introduce a pending-approval queue or automatic retry. Attended and delegated calls use the existing approval behavior and cannot approve themselves. PDF/images are private inspection intermediates; no document-library preview feature is included.

## 3. Implementation and delivery

Remove the superseded package-recipe engine/catalogue, structured-dependency input handling and corresponding obsolete tests/instructions; retain reusable boundaries, paths and sessions. Replace the former FR-011 Admin handoff, role-specific runtime wiring, Admin shared-prefix write grant and Admin-only finalization route. Reuse existing catalog, tool policy, approval UI, execution/sandbox, workspace authority and image-reading facilities. Before handlers, define tool/wire types through repository-standard contract sources and regenerate the corresponding types; do not hand-edit generated files.

The Omnipus Go binary remains standalone; document dependencies are optional execution prerequisites, not startup requirements. Certify installation and rendering on actual supported release platforms. Unsupported platform or installation capabilities must be explicit; simulated platform checks are not delivery evidence.

## 4. Acceptance and test plan

Write failing behavioral tests before implementation. Use real catalog, tool-policy/approval and filesystem paths, with controlled package sources at the network boundary. Derive expectations below independently from code. Mutation checks must catch execution before approval, cross-workspace installation and false readiness.

| Scenario | Given / When / Then | Requirements |
|---|---|---|
| ES-BDD-01 Ask | A permitted agent discovers environment_setup through ToolSearch while the upfront set stays 37; the registered tool has Ask and invoking it produces the existing approval request; rejection executes no installer; acceptance executes the requested call once without a second question. | 01 |
| ES-BDD-02 Custom agent | Custom native agent has document skills, permitted tools and no delegation edges; approved setup succeeds and it imports/uses packages from its own sandbox. | 01,02,04 |
| ES-BDD-03 Scope | Workspace-local default, explicit shared runtime and forged cross-workspace/path requests; only authorized destinations are used, shared scope is visible in the approved call, and invalid targets fail before changes. | 02,03 |
| ES-BDD-04 Isolation/failure | Install hook attempts unrelated writes/secret access, or download/archive validation/disk/cancellation fails; boundaries hold, prior shared runtime remains usable, partial workspace effects are reported honestly. | 03 |
| ES-BDD-05 Reuse/concurrency | Two calls target the same environment or an existing compatible runtime; safe serialized installation/reuse, no overwritten active runtime or unrelated user files, no cross-workspace package leakage. Two unrelated shared scripts leave both tools usable; absolute-prefix references still work after publication. | 03,04 |
| ES-BDD-06 Office | Mia, GP and a custom agent recover missing dependencies through Ask, generate/validate/render/read documents, identify known visual defects and inspect corrected output without Admin. | 04,05 |
| ES-BDD-07 Limits | Unsupported installer/platform/privilege/licence, missing fonts/rasterizer, nonvision model or denied tool; clear limitation, no unsafe retry and no false visual-completion claim. | 01–05 |
| ES-BDD-08 Existing policy | Each named built-in has its matrix default; global and permitted per-agent levels both store Ask; custom creation through REST and create_agent, with or without execution, stores Ask and get_agent_tools previews agree. A later global Allow retains local Ask unless the user changes or removes it. Skill assignment and later execution edits do not silently change saved setup permission. Ordinary edits persist; hidden Deny stays locked. The existing ceiling reconciliation adds the shipped global Ask entry when missing and preserves operator-set values; no new per-agent load-time backfill is introduced. Global/per-agent restrictions and God mode follow the existing resolver without a setup-specific approval system. | 01,04 |
| ES-BDD-09 Background lifecycle | Approval starts one background installation and returns a session_id promptly; poll/read show progress and final result without rerunning it. Unauthorized session access fails. Kill, timeout and interruption preserve boundaries, release locks and report partial effects; reconnect never silently restarts a job. | 01–04 |
| ES-BDD-10 Admin targets | Admin without workspace membership can explicitly target workspace A or B through its cross-workspace filesystem authority. A job targeting A cannot write B. An ordinary agent without B authority cannot target B. | 02,03 |
| ES-BDD-11 Unattended Ask | Unattended/scheduled execution cannot present Ask; the existing denial runs no installer, creates no approval queue and does not retry automatically. Attended/delegated approval retains existing behavior. | 01,05 |

| ES-BDD-12 Generic dependencies | An agent requests dependencies absent from the document skills and unknown to Omnipus at build time; installation needs no package-specific source change. No hardcoded library/version catalogue controls eligibility. Cover both a package-manager command and a custom multiline installation script under normal Ask approval. | 02,04 |

Datasets include oversize/nonblank/multiline commands, unknown scope and invalid timeout values, workspace A/B, absent/existing/conflicting versions, installer-reported invalid package names, install/build hooks, archive traversal/symlinks, cancelled and interrupted setup, missing rasterizer/fonts, Unicode and two-page/two-sheet/two-slide fixtures. Existing approval authentication, replay, disconnect/restart and cancellation regression tests must also pass through this registered tool; extend the shared mechanism only where wiring requires it, without introducing a parallel lifecycle.

- **ES-SC-01:** ES-BDD-01–03, 08 and 10–12 pass through the registered tool and existing authenticated approval UI, including a custom agent without delegation edges.
- **ES-SC-02:** ES-BDD-04–05 and 09 pass with observed denied side effects, preserved working shared runtimes and truthful partial-failure results.
- **ES-SC-03:** ES-BDD-06–07 pass with real sandbox rendering and live model inspection for the three agent categories; record those separately from unit/scripted checks.
- **ES-SC-04:** Record certified platform/version/source/integrity/licence evidence and unsupported cases; existing task approval/resume and affected runtime regressions pass.

No runtime tests were executed as part of authoring this specification. Implementation, automated correctness, sandbox reachability and live visual verification remain separate completion claims.
