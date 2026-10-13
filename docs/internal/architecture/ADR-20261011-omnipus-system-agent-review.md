Correction: said the pairing handler was `createReplayAndStatusFramesSlice`, wrong because that symbol does not exist in this tree, correct is `handleReplayAndStatusFrame`. The citation is corrected below. Also, the CLI change detector selected a stale sibling index; its “No changes detected” result was rejected, not treated as evidence of this worktree's scope.

# Adversarial Review — The Omnipus system agent

**Verdict: BLOCK.** Two critical findings invalidate the ADR's claim that secrets and host execution remain behind the proposed Ask list. Fourteen major findings concern confirmation, secret handling, cross-workspace ownership, verification, approval visibility and frontend delivery.

| Review header | Value |
|---|---|
| Document reviewed | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md` |
| Mode / round | ADR mode — the one fixed grill round; independent reviewer, not the design author |
| Review date | 2026-10-12 |
| Receipt lane | `grill-osa-adr` |
| Reviewed branch / commit | `work/omnipus-agent-design-20261012` / `4b298976266ac5e5e13c55dceba694227b743507` |
| Scope | Report only. No ADR correction, production change, build, test, remote run or push. |
| Method | Complete ADR, founder record and BRD reads; workflow-draft source inspection; relevant prior decisions, contracts, user documentation and direct code tracing. The Skill tool supplied a provisional candidate review; this report verifies and completes that same grill across all twelve lenses, not a second grill. |
| Source limitation | GitNexus MCP was unavailable; local `gitnexus status` reported `Repository not indexed.` The CLI change detector selected a stale sibling index and returned “No changes detected” before this new report was tracked; that result was rejected. No index was built and no graph result supports this review. Direct Read/search and scoped Git diff are the fallback. Jev MCP was not connected; no remote judge was invoked under this dispatch's no-remote-run boundary. |
| Certainty | **Verified (high confidence)** means document or source behavior was read first-hand here. Future failure scenarios are **Inferred**, with confidence stated per finding; no exploit or runtime acceptance was demonstrated. |

## Executive summary

All D1–D27 have identifiable coverage, but coverage is not the same as faithful resolution: D7 still conflicts with editable per-agent Ask, D10's ordinary-agent usability is deferred, and D27's approvals cannot rely on the current active-workspace-only modal. The architectural gaps must be corrected before the specification can elaborate them. Frontend lenses received source-level scrutiny equal to backend lenses; no rendered-product or accessibility PASS is claimed.

| Severity | Count |
|---|---:|
| CRITICAL | 2 |
| MAJOR | 14 |
| MINOR | 1 |
| OBSERVATION | 3 |
| **Total** | **20** |

Evidence IDs below resolve to absolute `file::symbol` citations in the final evidence table. A missing future implementation is not itself a finding against a proposed ADR. The findings identify contradictory promises, insufficient architectural invariants or verified mismatches in the proposed reuse.

## Findings

### CRITICAL findings

#### CRIT-001 — Allowed reading plus public web access permits direct disclosure

| Field | Review |
|---|---|
| Lens | 6 — Security; also 3 — Inconsistency |
| Affected section | OSA-1 role boundary; OSA-3 “accepted risk”, M-B and FQ-4; Consequences / Risks |
| Failure scenario | Untrusted page text or a peer request persuades Omnipus to read an accessible private document and include its contents in a query to an attacker-controlled public URL. Neither call is a configuration mutation. No secret handle, host command or human Go is needed. A confirmation check only in change tools does not prevent it. |
| Evidence | The ADR says the residual is “a persistent foothold, not direct exfiltration” and that “Secrets and host execution stay behind the ask list.” OSA-1 grants file reads and `fetch_url` Allow. E03 verifies open ordinary reads outside the protected set; E04 verifies model-selected public URLs are sent. E02 distinguishes registered credentials from arbitrary private documents and warns that personal secret folders are not protected. |
| Certainty | **Verified (high confidence):** grants, quoted assurance and source mechanisms. **Inferred (high confidence):** disclosure if the model obeys the injection; not an executed exploit. |
| Recommendation | Correct the risk statement and FQ-4's premise. Have security-lead disposition the combined read/outbound surface, not merely whether web text can inject instructions. Use existing policy/capability controls or explicitly accept disclosure; do not claim M-B closes it. FQ-11 is the new product choice. |

#### CRIT-002 — Allow creation of an external worker can plant an executable outside the Ask fence

| Field | Review |
|---|---|
| Lens | 6 — Security |
| Affected section | OSA-3 `create_agent: allow`; “host execution stays behind the ask list”; security-review item 2 |
| Failure scenario | An injected request creates a `subagent_3p` worker pointing `cli_path` at a malicious executable or wrapper and adds an authorized delegation path. A later Jim delegation executes that program. The version probe alone runs the supplied binary; native `bash` approval and the MCP-install Ask do not govern this launch. |
| Evidence | E05 verifies that create validates the CLI family but persists the selected path without confining it. E06 verifies verbatim path resolution and the executable version probe, with environment scrubbing but no sandbox installation in that probe. E07 verifies external permission requests are automatically approved without a native approval frame. The ADR acknowledges the path as unverified; source inspection now resolves that question. |
| Certainty | **Verified (high confidence):** selectable path, executable probe and native-consent boundary. **Inferred (high confidence):** malicious execution after an authorized handoff. Not an assertion that creating the record executes it immediately. |
| Recommendation | Put external-worker creation/runtime selection into the consent analysis. If normal native creation stays Allow, use a distinct Ask-gated external-creation capability or another explicitly approved, enforceable restriction; argument-sensitive hardcoded Allow/Ask fallback is not an option. Also cover edits to executable selection wherever the retained editor/API permits them. FQ-12 asks the additional founder choice. |

### MAJOR findings

#### MAJ-001 — M-B cannot read an answered Go from the pending registry as described

| Field | Review |
|---|---|
| Lens | 4 — Infeasibility; 3 — Inconsistency |
| Affected section | OSA-3 M-B, FQ-4 B; OSA-8 parallel apply helpers |
| Failure scenario | The user confirms plan A, which starts helper A. Submit removes that card from the pending indexes. The main then interviews plan B, replacing the single metadata slot and adding a later human message. M-B either refuses A's still-authorized writes or treats the latest unrelated question as A's authority. The answer itself is a human-role resume entry, so “after the latest human message” also needs an exact definition. |
| Evidence | E08: `Submit` removes the answered set; `PendingForSession` sees pending entries only; `persist` uses one `PendingAskJSON` slot; answers are persisted as a user-role resume record. `fireDefaultSafeSet` can also produce an answered card without a human, contradicting the unqualified OSA-3 assurance that only a human answers cards. |
| Certainty | **Verified (high confidence):** retention, automatic-answer and resume behavior. **Inferred (high confidence):** wrong/refused authorization under the proposed check. |
| Recommendation | Do not recommend the current M-B wording as a cheap existing check. Retain the founder's instruction-enforced model unless the founder chooses an amended design. If M-B is chosen, define trusted per-batch confirmation identity, human-versus-automatic provenance, scope and lifetime in existing session/lifecycle metadata, including later interviews, Stop and restart. Never allow a Go confirmation to become a default-safe automatic answer. No separate workflow store or third policy resolver is justified. |

#### MAJ-002 — Staged secrets lack a durable, single-use consumption and resume contract

| Field | Review |
|---|---|
| Lens | 2 — Incompleteness; 6 — Security |
| Affected section | OSA-7 diagram and points 1–6; OSA-6 interruption behavior |
| Failure scenario | Two consumers both read the same staged value before either deletes it. Alternatively, a crash occurs after canonical promotion but before staged deletion/configuration completion. The same handle remains usable, or the operation reports failure while a live credential has changed. A multi-field card can partly promote. Copying the question-answer path can persist plaintext or falsely promise a delayed tool result. |
| Evidence | OSA-7 promises single use but specifies “copies the value … → deletes the staged entry.” E09 verifies `Get`, `Set` and `Delete` are separately locked/persisted operations, not one promotion transaction. E10 verifies that question parking returns a stub immediately and the answer starts a new turn, never a delayed tool result. E11 verifies redaction registration replaces the complete set rather than appending a newly staged value. |
| Certainty | **Verified (high confidence):** architectural omission and existing primitives. **Inferred (high confidence):** race/crash and unsafe-resume consequences of composing those primitives literally. |
| Recommendation | Specify an authoritative encrypted staging envelope with request/agent/destination/expiry/state, and an atomic store operation for promotion plus consumption under the existing store lock and atomic file publication. Define bundle partial outcomes and the boundary with separate configuration persistence; do not promise a cross-resource transaction. Define Cancel/Stop/expiry/restart and duplicate submission/consumption. Resume with sanitized handles-only content through the existing park/resume seam; never store plaintext answers in question metadata/transcripts. Register the complete live plus staged sensitive set before probes/connector activity, without treating best-effort redaction as the primary no-leak guarantee. |

#### MAJ-003 — A slot identity does not bind a secret to its effective recipient

| Field | Review |
|---|---|
| Lens | 6 — Security; 1 — Ambiguity |
| Affected section | OSA-7 point 3 and the secret-card contract row |
| Failure scenario | The user supplies a key for provider X at its intended endpoint. The consuming call uses the same provider ID with a different `api_base`. Slot equality passes, but the key reaches another host. A mailbox keeps its agent/workspace while its servers or username change; an MCP server keeps its name/env key while its command/arguments/transport change. |
| Evidence | OSA-7 defines destination as kind plus provider ID, server name/key, channel instance/field or agent/workspace. E12 verifies mutable provider endpoint, mail server/account and MCP executable/transport fields. The proposed card sends a destination label/kind, not a defined effective-recipient descriptor. |
| Certainty | **Verified (high confidence):** descriptor and mutable recipient fields. **Inferred (high confidence):** wrong-recipient use if slot matching is the whole check. Separate Ask may expose a changed endpoint to the user, but does not establish D10's destination guarantee. |
| Recommendation | Bind to the effective recipient the protected card actually shows: relevant endpoint/account identity or executable/transport/arguments, and the field being written. The server must retain the authoritative descriptor, compare it at consumption, and require a new secret request after a recipient change. The specification must publish its safe display representation through generated contracts; model-written purpose text is not authority. |

#### MAJ-004 — The selected provider self-check can test the wrong endpoint

| Field | Review |
|---|---|
| Lens | 11 — Testability / false-green risk; 3 — Inconsistency |
| Affected section | C5; OSA-4 `configure_provider` self-check |
| Failure scenario | A key works against the catalog endpoint while the configured custom endpoint is unavailable. Reusing `list_models` as proposed reports Working even though the actual saved provider cannot serve requests. A keyless local provider instead fails the proposed check because the reused path requires a key. |
| Evidence | E13: `ModelsListTool.Execute` takes its base from `providers.APIBaseFor`, not configured `APIBase`, skips an empty key and calls an OpenAI-style fetch. E14 verifies the existing protocol-dispatched model-list helper. E12 verifies configuration accepts and saves a custom base. |
| Certainty | **Verified (high confidence):** endpoint and key selection. **Inferred (high confidence):** false Working/unnecessary failure when used as the new oracle. |
| Recommendation | Resolve the saved effective provider entry, endpoint, credential and protocol exactly as runtime does; reuse the appropriate shared validation helpers and URL safety policy. Name what Working proves: listing availability is not proof of selected-model execution, quota or tool support. Include the negative control “catalog succeeds, configured endpoint fails,” plus keyless local and non-OpenAI protocol cases. |

#### MAJ-005 — Cross-workspace setup needs more than a target argument on create_agent

| Field | Review |
|---|---|
| Lens | 3 — Inconsistency; 5 — Contract-first gaps; 7 — Reachability |
| Affected section | OSA-1 fixed main; OSA-9 W1/W18/W8; OSA-11 workflow-start metadata |
| Failure scenario | From Omnipus's fixed main, the user configures workspace B. Project-skill editing/removal still resolves the main/helper's workspace A, not B. The intended skill is unreachable or an identically named A skill becomes the candidate. Reusing the old `workspace_setup_kickoff` frame with the existing Omnipus session is rejected because that precedent is mint-only; using the target as session-binding metadata instead risks moving the anchor. |
| Evidence | E15 verifies project-shelf resolution from `ToolWorkspaceID` then the default workspace, and project-first edit/remove. **Skill activation and loading**::D6.1 requires writing the actual project shelf. E16 verifies helper metadata inherits its steering session's workspace. E17 verifies the existing message metadata means session workspace and the kickoff rejects a supplied existing session ID. |
| Certainty | **Verified (high confidence):** implicit scoping, inheritance and kickoff constraint. **Inferred (high confidence):** wrong/unreachable target under unchanged reuse. |
| Recommendation | Separate the acted-on workspace from the execution/session anchor. Inventory every implicitly scoped setup verb, including skill discovery/edit/remove, environment setup and membership/readback; carry an explicit validated target where needed without changing the main's workspace. Define workflow-start metadata as target data, not session binding, and explicitly amend the kickoff's existing-main dispatch contract/producer rather than calling it unchanged reuse. Keep ordinary mount/authority checks; do not silently create a global skill override. |

#### MAJ-006 — Per-workspace helper serialization does not define shared-agent reuse safety

| Field | Review |
|---|---|
| Lens | 1 — Ambiguity; 2 — Incompleteness |
| Affected section | OSA-8 parallel-workflow rule; OSA-9 W18 reuse; Consequences / Risks |
| Failure scenario | Helpers for workspaces A and B both tailor the same reused agent or shared skill. They are permitted by “one helper per workspace.” A helper can re-read a fresh revision and still make an intentional global change that alters the other workspace's behavior. Revisions prevent stale writes, not unannounced global reconfiguration or a wrong plan-level dependency assumption. |
| Evidence | BRD §3 defines one global identity/configuration. OSA-9 says apply “reused agents” without saying whether this joins unchanged or reconfigures globally. E18 verifies update mutates that agent's global record and returns a conflict only for its revision. E15 verifies global and project skill shelves are distinct shared resources. |
| Certainty | **Verified (high confidence):** ownership and revision scope. **Inferred (medium confidence):** harmful interpretation of the unspecified reuse action. |
| Recommendation | Default to reuse unchanged when it fits. Show any global change separately with before/after and all affected workspaces. At each resource write, validate the relevant resource revision and confirmed assumptions, not only the target workspace revision; changed meaning returns to the main for a new summary. Preserve the existing locks/revision mechanisms, without a workflow transaction engine. FQ-14 asks the product boundary. |

#### MAJ-007 — The existing approval modal hides Omnipus requests from another active workspace

| Field | Review |
|---|---|
| Lens | 7 — Reachability; 8 — UI states / journey gaps |
| Affected section | C13; OSA-8 helper approvals; OSA-10 U1 “does not switch the active workspace”; FQ-8 |
| Failure scenario | Workspace B stays active while the user opens Omnipus's main anchored in A. An Omnipus or helper Ask arrives stamped with A. The global queue stores it, but the only approval modal filters it out because B is active. Omnipus waits without an actionable card in the promised chat. Reload/reconnect retains the same scope. An operator tightening an Allow tool to Ask makes this a legitimate helper case, not just a “stray” call. |
| Evidence | E19 verifies gateway workspace stamping/parent walk. E20 verifies `isApprovalInScope` and `ToolApprovalModal` filtering by active workspace; no root-Omnipus exception exists there. D27 requires helper questions/approvals to reach the user in Omnipus's chat. |
| Certainty | **Verified (high confidence):** current routing/filter mismatch. **Inferred (high confidence):** hidden card when the proposed no-workspace-switch entry reuses it. |
| Recommendation | Commission the frontend scope adaptation explicitly. Resolve the validated Omnipus root/helper relationship, preserve the acting session/agent in the approval, and render those requests while Omnipus is foreground irrespective of the retained active workspace. Preserve existing queue, expiry, reconnect-stub and resolution behavior. Do not erase `workspace_id` or expose every unrelated approval globally. Attention and actionable rendering are separate requirements. |

#### MAJ-008 — Channel Working and QR-in-chat require an observable connection contract

| Field | Review |
|---|---|
| Lens | 5 — Contract-first gaps; 11 — Testability; 8 — UI journey |
| Affected section | OSA-4 outcomes; OSA-5 connect tools; OSA-7 S7 / contract table |
| Failure scenario | A token authenticates but a webhook/routing path does not work, or one mail transport fails; the tool still reports Working because “live test” has no defined proof. During QR pairing the user sees no code: the tool waits for pairing, while the existing frame updates the Settings pairing store rather than chat. Showing only the final tool result cannot unblock the pairing that result is waiting for. |
| Evidence | OSA-5 promises a chat pairing code and completion when paired. E21 verifies pairing updates route to a dedicated Settings store; the current frame/subscription is channel-scoped and has no session/request association. E22 verifies mailbox configuration carries both mail-server identities. OSA-7's contract inventory omits pairing progress/correlation. |
| Certainty | **Verified (high confidence):** current pairing wire and consumer. **Inferred (medium confidence):** false Working or invisible pairing under the unspecified new connection path. |
| Recommendation | Name platform-specific proven facts and “not tested” limits; do not equate stored credentials, token authentication or model claims with usable routing. Reuse pairing events, but define subscription ownership, channel/request/session correlation, progress presentation, timeout/Cancel/Stop and terminal results before consumer code. If new fields are necessary, backend-lead adds/regenerates the shared contract first; no hand-written QR/status payload in tool prose. Distinguish mailbox read and send readiness without actually sending mail as an unapproved probe. |

#### MAJ-009 — Credential removal can block gateway restart, not merely one consumer

| Field | Review |
|---|---|
| Lens | 2 — Incompleteness; 3 — Inconsistency |
| Affected section | OSA-3 secret-removal reason; OSA-7 S3; dependence on Credential Boot Contract |
| Failure scenario | The user approves deleting a credential still referenced by an enabled channel. The store deletion commits; reload is refused and can retain the old live service. The next gateway start fails before the Settings recovery UI exists. “Removed” is neither proof of live revocation nor an adequate warning about the whole application becoming unavailable. |
| Evidence | E23 verifies an enabled missing reference is fatal at boot, while provider NotFound has a distinct degraded path. E24 verifies raw deletion commits before reload and reports reload warnings without undoing it. The ADR says a removed credential “silently breaks every consumer,” understating this boot boundary. |
| Certainty | **Verified (high confidence):** boot/deletion behavior. **Inferred (high confidence):** restart failure following the proposed raw removal of an enabled reference. |
| Recommendation | Enumerate consumers before removal. Refuse while enabled consumers still reference it, or perform an explicitly confirmed disconnect/reference-clear sequence first. Separately report durable deletion, live revocation/application and boot-valid configuration. Do not reuse raw deletion without this guard. FQ-13 chooses the user-facing removal behavior. |

#### MAJ-010 — “Failed: nothing saved” excludes existing partial-save outcomes

| Field | Review |
|---|---|
| Lens | 3 — Inconsistency; 2 — Incompleteness |
| Affected section | OSA-4 three outcomes; OSA-6 retry/undo; OSA-7 promotion sequence |
| Failure scenario | Agent entity creation succeeds but home/membership publication fails, or a provider key is saved before wiring fails. Classifying the operation as Failed: nothing saved hides durable changes. Retrying may collide with an existing agent or lose track of a replaced live key. Re-reading only the visible roster is insufficient when a saved entity is not yet published. |
| Evidence | E25 verifies post-entity-save home/heartbeat failures and partial membership/seed results. E12 verifies provider key storage before configuration and reload. E18 returns actual persistence/activation/error-stage fields on save failure. These are deliberate truthful partial results, not a cross-resource rollback guarantee. |
| Certainty | **Verified (high confidence):** existing partial states and contradiction with the three-outcome assertion. **Inferred (high confidence):** misreport/retry damage if the new result wrapper discards them. |
| Recommendation | Keep persistence and activation/verification as separate facts. Add a partial outcome with saved identity/revision, completed parts and unsaved/unverified parts, or retain the existing structured partial fields under a truthful presentation. “Nothing saved” is allowed only when no side effect is proven. Recovery reads durable affected resources, not just the current live list. |

#### MAJ-011 — The new secret-card journey has no submission-uncertainty or recovery state

| Field | Review |
|---|---|
| Lens | 8 — UI states and journey gaps |
| Affected section | OSA-7 protected input / Submit / resolved frame / snapshot; OSA-10 chat |
| Failure scenario | The user enters three secrets, submits, then loses the response. A renderer that treats disconnect as failure asks for them again; one that treats the click as success advances before durable staging. Reload may show a pending or resolved card with no defined safe reconciliation, while locally held plaintext remains in shared chat/draft state. |
| Evidence | The complete OSA-7 text names submitted/cancelled/expired but not submitting, rejected, result-unknown, reconnect or partial-field outcomes. E10 verifies answers need an explicit server-driven resume. E26 documents the existing UI distinction between connection loss, retained state and confirmed effects; the protected field is a new control, not an existing question textbox. |
| Certainty | **Verified (high confidence):** missing state/ownership rules in the proposed new surface. **Inferred (medium confidence):** unsafe retry/false success under the unspecified renderer. |
| Recommendation | State the minimum journey now and put detailed copy in the specification: request loading/unavailable; editable pending; submitting; rejected with safe retry; submission unknown with same-request reconciliation; submitted; cancelled/expired; and cancelled-by-Stop/interrupted recovery. Values live only in the isolated control until submission/clear, never chat drafts/shared persistence. No automatic replay of plaintext; clear values on terminal resolution/navigation/disposal. Server request state, not a click or model sentence, owns completion. |

#### MAJ-012 — Secret entry and new Team actions lack a keyboard/focus contract

| Field | Review |
|---|---|
| Lens | 9 — Accessibility and keyboard |
| Affected section | OSA-7 multi-secret card; OSA-10 U1/U6/U7; W18 graph preview |
| Failure scenario | A keyboard or screen-reader user reaches Omnipus but cannot identify which of Slack's two inputs is required, discover a submission error, return to the composer after expiry, or invoke a Team action that appears only on hover. A graph-only confirmation prevents that user from checking delegation changes before Go. |
| Evidence | The ADR describes these new interactions but specifies no labels/focus/error announcement or non-graph equivalent. E27 verifies the catalogue has reusable labeled controls and composites. E28 shows the existing security approval surface deliberately defines focus, dismissal and safe keyboard defaults; those semantics cannot be assumed for a new secret card. |
| Certainty | **Verified (high confidence):** omission for new interactive surfaces. **Inferred (medium confidence):** inaccessible implementations permitted by it; no browser/assistive-technology test was run. |
| Recommendation | Require keyboard access, agent/purpose/destination and field labels, field-error associations, announced submission/expiry state, safe focus placement/return and no Go auto-selection. Team menus must work on focus/touch, not hover only. W18 must provide readable agents/edges text equivalent to its graph. Reuse kit control behavior, but specify the new composite's responsibilities and verify them in the later specification's accessibility acceptance. |

#### MAJ-013 — The documentation inventory is not a change-to-page TODO map and misses Tools

| Field | Review |
|---|---|
| Lens | 2 — Incompleteness; 6 — User promises |
| Affected section | Consequences / User-facing documentation |
| Failure scenario | The implementation updates roster names and its generated catalogue, yet users following Tools still open the deleted Agents screen to tighten permissions and read an outdated policy/approval explanation. Other pages are listed without a specific behavior TODO, so a name sweep can be mistaken for documenting secret cancellation, partial application and shared-agent impact. |
| Evidence | E29 verifies the ADR's list lacks the user Tools page and only annotates Security/reference tools, rather than mapping changed behaviors to concrete updates. E02 verifies Tools::How to change an agent's tool access starts from Agents and explains the policy/Auto model. E30 requires named pages **and specific TODOs** at design time. |
| Certainty | **Verified (high confidence):** missing page, current instructions and requirement. **Inferred (high confidence):** users lose their permission-management path after removal without a matching update. |
| Recommendation | Add `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/tools.md`. Map UI navigation/Team/creation, workflow routing, confirmation/Ask/God Mode, all-agent secret usability and safe lifecycle, connection verification, helper visibility/Stop and shared edits to named sections/pages. Keep the existing broad mention sweep as supplementary work. Implementing leads draft same-change updates; docs-verifier checks actual behavior. This is an Important documentation defect, not a demand to edit user docs in this review. |

#### MAJ-014 — No per-agent Ask seed does not guarantee prompt-free God Mode after user edits

| Field | Review |
|---|---|
| Lens | 3 — Inconsistency with founder decisions and policy constraints; 1 — Ambiguity |
| Affected section | OSA-1 editable capability defaults; OSA-3 global-only Ask / God Mode; FQ-5 |
| Failure scenario | An operator tightens an Omnipus tool to per-agent Ask using the retained editor. Later God Mode is enabled. The normal merge preserves that Ask, so a prompt appears despite the unconditional “nothing asks” promise. Re-seeding away the operator's Ask would violate the preserved-user-choice rule; bypassing the merge would violate the agreed policy boundary. |
| Evidence | E31 verifies God Mode floors only the global side and retains per-agent Ask/Deny. **Built-in agent configuration, skills, and visual file reading**::§2.6 preserves editable ordinary-role capabilities and user choices. D7 is unconditional; OSA-3 removes seed asks only. FQ-5 addresses environment_setup's shipped seed, not future operator tightening on any tool. |
| Certainty | **Verified (high confidence):** merge/editability and unmatched guarantee. **Inferred (high confidence):** prompt after the stated operator change. |
| Recommendation | Preserve the two-layer compositor. Qualify prompt-free behavior as the shipped Omnipus posture with no operator per-agent Ask, or explicitly ask the founder whether Omnipus's editor should restrict Ask to global settings while preserving Deny. Do not clear user data or introduce an Omnipus-only resolver bypass. FQ-16 asks this new lifecycle question, distinct from FQ-5. |

### MINOR findings

#### MIN-001 — Revision-conflict recovery names the wrong existing literal

| Field | Review |
|---|---|
| Lens | 3 — Inconsistency |
| Affected section | C9; OSA-8 stale-write handling |
| Failure scenario | A workflow skill written to recognize only `REVISION_CONFLICT` misses an agent/skill stale-write result named `CONFLICT`, and retries or reports the wrong failure. |
| Evidence | E18 verifies `AgentUpdateTool.mutateErrorResult` emits `CONFLICT`; E15 verifies `SkillRemoveTool.Execute` does likewise. C9 generalizes the workspace spelling to every change tool. |
| Certainty | **Verified (high confidence):** literal mismatch. **Inferred (medium confidence):** incorrect instruction/result interpretation. |
| Recommendation | Describe the semantic stale-revision outcome and inventory the actual per-tool codes. Do not commission a code-family rename unless explicitly needed. |

### Observations — non-gating suggestions

| ID | Lens / affected section | Scenario or cost; evidence and certainty | Recommendation |
|---|---|---|---|
| OBS-001 | 10 — Design-system reuse and brand; OSA-7/OSA-10 | A new secret card and Team list can duplicate controls already in the kit. No raw-control/color violation is prescribed by the ADR, so this is not a proven defect. E27 verifies Input/Field/Label/Button, errors, confirms, tabs and AgentIcon; E32 defines catalogue-first reuse and the four-part publication contract. **Inferred (medium confidence)** maintenance/build risk if the visual draft is copied literally. | Add a short specification handoff mapping the card/list to these components. New domain composites own composition, not primitive look/focus. Publish genuinely reusable new components through catalogue, export, style registration and manifest; keep the dark-first/chat-first brand. |
| OBS-002 | 11 — Testability / false-green risk; Evidence and release boundary | The ADR has no architectural proof checklist. A full behavior-driven scenario/test matrix belongs in the later specification; its absence alone does not block this ADR. **Verified (high confidence)** document scope; **Inferred (medium confidence)** false-green risk if design claims become acceptance without the negative controls below. | Carry the proof table below into the specification and exact-commit acceptance. Keep code correctness and reachability separate, with CI authority and independent hands-on validation. |
| OBS-003 | 12 — Overcomplexity; OSA-8 main/helper apply split | Requiring every Ask call to run in the main creates helper → main → helper handshakes for workflows alternating skills, connector setup and agent creation. Slow verification/setup coordination can keep the main busy despite D27. E10/E19/E20 identify existing owner-question relay and approval delivery seams. **Inferred (medium confidence)** extra sequencing burden, not proof that all background setup blocks the main. | Consider letting a helper execute Ask calls while the user approves them through the one Omnipus surface. Keep interviews/secrets/summary in the main and reuse ordinary control/approval mechanisms. If the current split stays, specify its phase boundaries, handbacks and Stop/restart semantics. FQ-15 is about execution ownership, not FQ-8's attention choice. |

## Structural integrity

| ADR-mode check | Result | Notes |
|---|---|---|
| Status / Date / Deciders | Present | Proposed and explicitly not implementation-ready. |
| Context / Decision / Consequences | Present | OSA-1–OSA-12; positive, negative and neutral consequences. |
| Related decisions by title | Present, with affected-clause table | Review checked the cited policy, skills, credential, session and navigation decisions; code wins over historical descriptions. Mailbox ownership also depends on **Per-(Agent, Workspace) Email Mailboxes**, not only the channel-binding decision. |
| Open architectural questions named | Present | FQ-1–FQ-10 are genuinely open; merely having them is not a finding. Recommendations are not founder decisions. |
| Documentation impact | Incomplete | Page inventory exists; specific TODO mapping and Tools are missing, MAJ-013. |
| Contracts / ownership | Direction present; gaps found | Architect shapes, backend-lead edits/regenerates. Missing effective-destination/pairing/target-versus-session decisions are MAJ-003/005/008, not a demand to generate code in an ADR. |
| Scope / supersession | Explicit | Revised D6 wins over the visual trade-off table and BRD. Greenfield Ava/Admin removal and no forwarding are not reopened. |

### Founder-decision coverage — D1–D27

**Authority:** `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-system-agent-decisions-20261012.md`::Decisions Log. Every row below was compared first-hand, not inferred from an ADR citation.

| Founder ID | ADR coverage | Fidelity / correction requirement |
|---|---|---|
| D1 | OSA-1 exclusions; Neutral consequences; FQ-9 | Core security and Judge/Planner configuration redesign stay out of scope. Moving existing controls to preserve reachability must not quietly widen that scope; FQ-9 already asks this. |
| D2 | OSA-9 W1 | Combined in-workspace creation, existing-agent exception, capability mapping and visible gaps retained. Target handling needs MAJ-005. |
| D3 | OSA-9 Dependencies; OSA-4 | Up-front checks and permitted-agent self-service retained; setup success is not readiness. Omnipus-only God Mode seed departure remains FQ-5, not an answered decision. |
| D4 | OSA-8 point 1 | Normal peer message, no delegation/pointer; user interaction in Omnipus retained. |
| D5 | OSA-8 point 1 | Read-only questions use the same messaging path. |
| D6 revised | OSA-3; superseded-BRD table | Instruction-enforced summary + Go and narrow Ask proposal retained. M-B/single-tool alternative are recommendations only. Security assurances need CRIT-001/002 and MAJ-001; don't silently turn the accepted risk into a new code gate. |
| D7 | OSA-3 mode note / no seed Ask | Summary message and no shipped prompts intended. Literal guarantee after user edits is unresolved, MAJ-014 / new FQ-16. Secret entry and genuinely missing interview information are not approvals; the specification must distinguish them from the skipped Go. |
| D8 | OSA-1/OSA-2 | One agent; ordinary skills on demand, not a workflow engine. |
| D9 | OSA-4 | Existing-tool verification retained. Wrong endpoint/overbroad Working and partial persistence require MAJ-004/008/010. |
| D10 | OSA-7; FQ-7 | S1–S4, later S5/S6, S7 in connect_channel retained. Destination and lifecycle promises need MAJ-002/003/011/012. A rendered field without a usable ordinary-agent consumer is not faithful completion of “ALL agents”; FQ-7 A needs an explicit scope disposition. |
| D11 | OSA-5 | Both connect tools and manual forms retained. Live verification/QR route needs MAJ-008. |
| D12 | OSA-6/OSA-7 | No workflow record; expire staged handles and re-read configuration. Safety metadata for a secret is not a workflow engine. |
| D13 | OSA-2/OSA-11 | Named-button routing, ambiguity question, no-match no-change and sub-workflows retained. Record says routing table in workflow skills; OSA-2 puts a compact table in the core prompt, as visual §5 also does. Treat that placement as implementation organization, not a different routing rule; avoid divergent copies. |
| D14 | OSA-2 lifetime | Later-turn retention/no unload retained; current Skill description verifies the wording change, E33. |
| D15 | OSA-10 U1 | Fixed global entry/one conversation retained. Active workspace stays unchanged; approval and attachment adaptations cannot be left implicit, MAJ-007. |
| D16 | OSA-8/OSA-10 U2 | Existing main attention retained. Pending-secret extension and helper aggregation remain FQ-8, not silently accepted changes. |
| D17 | OSA-10 U3 | Add-agent opens Omnipus; Settings forms remain. |
| D18 | OSA-10 U4; FQ-10 | Manual creation removed; editor retained; no-model consequence accepted. API exception is proposed, not already authorized by “Only Omnipus creates agents.” |
| D19 | OSA-10 U5 | Agents routes/menu/links removed, no forwarding. This is intentional, not a finding to restore the roster. |
| D20 | OSA-10 U6 | List first, graph second; cards/actions/impact list; workers and external workers retained. Keyboard/touch and shared-edit impact need MAJ-006/012. |
| D21 | OSA-10 U7 | Orphan marker in add-existing picker retained; no extra roster. |
| D22 | OSA-1/OSA-9 W8 / migration | Ava/chats removed; greenfield/no migration; Omnipus welcome retained. Existing-main kickoff requires MAJ-005. |
| D23 | OSA-1/OSA-10 | Admin removed/replaced by fixed Omnipus entry; inherited authority is explicitly identified. |
| D24 | OSA-9 W18/W8; U8 | Offer on creation and empty Team, skippable. Do not turn welcome into forced creation. |
| D25 | OSA-9 W18 | Tailored lead/specialists/workers; graph summary; templates later retained. Accessible graph equivalent needs MAJ-012. |
| D26 | OSA-9 W18 | Offer reused identities and show other workspaces retained. Whether reuse reconfigures globally is not settled, MAJ-006 / new FQ-14. |
| D27 | OSA-8 | Real self-helper, main interview/summary and no workspace overwrites addressed in principle. FQ-1 correctly exposes the anchoring dependency. Main-only Ask execution is an additional restriction, OBS-003 / new FQ-15; helper visibility and global-resource concurrency require MAJ-006/007. |

**No D-number was dropped.** D7/D10/D27 are conditional or narrowed in ways that must remain visible rather than being labelled fully faithful. Release v0.1.1 matches the root release rule; the separate R1 A interview receipt remains relayed rather than first-hand verified by this review.

### Existing founder questions — dispositions, not duplicates

| Existing question | Review position |
|---|---|
| FQ-1 | Agree with B as the lowest-exception option. If selected, validate the one main address, exclude Omnipus from ordinary workspace-entry lead selection, and preserve an operator path to edit/remove its ordinary self-row; don't re-add it at boot. Depth 1 consumes the inherited budget, E16; ordinary caps/queue admission still apply. |
| FQ-2 | Agree with A. These two Ask departures still need security-lead review. CRIT-002 adds a different executable-creation path, not a repeat of this MCP/provider choice. |
| FQ-3 | Agree with A as a two-layer-compatible tool split. Include connector assignment; creation-time requested grants must also be reviewed, not merely the conservative seed. E34 verifies current new-custom management denies, resolving the ADR's seed uncertainty; that seed is not an immutable prohibition on user-approved changes. |
| FQ-4 | **Disagree with B as written.** MAJ-001 shows it is not the promised existing-registry check, and CRIT-001/002 disprove its limited residual premise. A redesigned B may be offered after stating its state/cost/scope; the founder's already-accepted instruction-only risk is not silently revoked. |
| FQ-5 | Agree with A for the shipped Omnipus environment_setup posture, if the founder authorizes the cited amendment. This does not resolve operator-added per-agent Ask; FQ-16 is new. |
| FQ-6 | **Disagree with B's broad “only changes are ask tools” condition.** Several Ask calls provide separate permissions, not one Go covering the whole plan; a denied later call can leave earlier changes applied. If the founder chooses this alternative, limit it to exactly one effectful call whose approval represents the entire summarized change, or retain a combined Go for multi-call work. This changes revised D6 and cannot be treated as already decided. |
| FQ-7 | **Disagree with A being reported as all-agent delivery.** It explicitly leaves ordinary agents without a consuming destination, and OSA-7 binds handles to the requesting agent, so handing one to Omnipus is not automatically a valid solution. The founder must explicitly narrow first-version usability or authorize a real usable destination for the intended ordinary agents. Do not auto-grant management tools, add plaintext to bash or broaden hidden engine capabilities as a shortcut. No duplicate question is added. |
| FQ-8 | **Disagree with A's reliance on “ask calls stay in the main.”** Operator tightening/reload can make an intended helper tool Ask; D27 still requires that card to reach the user. B is the safer attention recommendation, subject to founder approval of the existing main-only attention amendment. Actionable display is mandatory whichever attention option wins, MAJ-007. |
| FQ-9 | Agree with A to relocate existing capabilities rather than lose them. Preserve default-agent routing and editable engine instructions without adding the out-of-scope tuning redesign. E35 confirms the lost surfaces; Omnipus model recovery must remain reachable without a working model. |
| FQ-10 | A is technically useful, but **the recommendation's existing-consumer rationale is UNVERIFIED** here: this review did not prove headless/test dependence on that endpoint. The founder must explicitly authorize the API exception to D18. This gap is a warning, not a block. |

## Twelve-lens coverage

| Lens | Result |
|---|---|
| 1 Ambiguity | MAJ-003/006/014: effective destination, reused-global changes and God Mode promise. |
| 2 Incompleteness | MAJ-002/009/010/013: secret lifecycle, consumer-safe removal, partial writes and documentation TODOs. |
| 3 Inconsistency / prior decisions / AS-IS | MAJ-001/004/005/010/014 and MIN-001; dated AS-IS policy descriptions were not substituted for current code. |
| 4 Infeasibility | MAJ-001: proposed pending-registry authority cannot work as described. No new database, CGo or security-critical shell implementation is proposed; those constraints remain. |
| 5 Contract-first | MAJ-003/005/008: recipient representation, target-vs-anchor/kickoff and pairing progress. OSA-7 has a legitimate shape/owner handoff, not permission for hand-written boundary types. |
| 6 Security / user promises | CRIT-001/002, MAJ-002/003/009/014; STRIDE table below. |
| 7 Reachability | MAJ-005/007; FQ-7/FQ-9 disposition; controlled catalogue search below. |
| 8 UI states / journey | MAJ-007/008/011: scoped approvals, visible pairing and secret submission uncertainty; Team first-load/empty/error/partial behavior carries into the specification. |
| 9 Accessibility / keyboard | MAJ-012: labels, announcements, focus, Team actions and graph alternative. |
| 10 Design-system / brand | OBS-001: catalogue inspected; no invented styling required. No measured visual PASS. |
| 11 Testability / false greens | MAJ-004/008; OBS-002; concrete negative controls below. |
| 12 Overcomplexity | OBS-003: main/helper handshakes compared against ordinary helper approvals. No workflow engine/vault/verification service proposed. |

## Reachability check

| Surface | Source check / required delivery |
|---|---|
| New tools | Controlled search finds existing `AskUserQuestion` in the same three trees, exit 0; proposed `request_secret`, `connect_channel`, `connect_mailbox` have no matches, exit 1. This is expected for a design-only ADR, not a current feature failure. The specification must list metadata/live registration, static catalogue, ceiling, role/custom policies, Auto classification and handle consumers together; registration alone is not enough, E36. |
| Every intended native agent | Enumerate permitted destinations and actually consume a handle from an ordinary native agent as well as Omnipus. Fixed hidden roles/external CLI limits must not be silently widened. FQ-7 cannot be closed by rendering the widget alone. |
| Global Omnipus entry | Validate/attach the one server-owned main, preserve active workspace and target intent, block sending on unresolved attachment, and show Retry. Existing `useSelectSession` changes active workspace for a known foreign session, so it is not an unchanged drop-in, E37. |
| Main/helper user decisions | Human questions remain owner-card/parent-relay; approvals preserve the acting helper ID. Open Omnipus from another workspace, reload during a pending card and prove the request remains actionable, MAJ-007. |
| Removing Agents | Editor, workers, orphan picker, global default, engine instructions and Omnipus model recovery need valid homes before deleting old navigation. FQ-9/FQ-10 already own those product choices, E35. |
| W1/W18 | Actually create/join, read back, set delegation and open the intended conversation agent's validated main; a graph-valid/readback-only result is not proof every required handoff is allowed. Include missing self-edge, depth-zero and concurrency-queued cases. |

**Code correct and tested:** not claimed — design review only; no product tests/builds/remote gates executed.

**Reachable by a user/agent:** not claimed — proposed behavior, with reachability proof obligations stated above.

## Test coverage assessment — handoff to the specification, not executed here

| Category | Required proof / negative or boundary control |
|---|---|
| Approval / policy | Shipped Allow/Ask/Deny, Auto on/off, God Mode, operator per-agent tightening and mid-helper policy reload. Native approved calls and unapproved requests must differ. |
| Injection | Private-file read → public URL, external-worker path creation → authorized delegation, agent/skill persistence, forged peer Go and default-safe auto-answer. Distinguish untrusted content from authenticated human submissions. |
| Confirmation | Plan A applying while B's interview/card replaces metadata; later human message, cancellation, Stop/restart, human answer versus automatic answer. A source-text check is not proof of enforcement. |
| Secret lifecycle | Concurrent consumers; same-ID changed endpoint/account/command; wrong agent/field; duplicate/missing/extra field; multi-secret partial failure; crash between promotion and configuration; expiry, Cancel/Stop, closed/reloaded tab and submission unknown. Check retained metadata/transcript/model input/tool arguments/WS/log/error paths contain no plaintext. |
| Secret removal | Enabled consumer, multiple consumers, reload failure, live stale value and next boot. Provider degradation is not the enabled-channel boot result. |
| Provider verification | Catalog endpoint succeeds while custom endpoint fails; keyless local; other protocol; listing succeeds while selected-model execution is unavailable; refused URL and sanitized error. |
| Channel/mail | Valid credential with wrong binding/webhook, only one required mail transport available, QR/timeout/error delivered to intended chat, subscription teardown and reconnect, no unapproved probe email. |
| Scope / concurrency | Two workspaces with same project skill slug; shared agent/skill across two helpers; stale revision and changed plan assumptions; interrupted partially published agent; old kickoff with supplied main ID refused until the contract adaptation exists. |
| Frontend / accessibility | Real shell/sidebar and chat, not isolated handler mocks: no-model recovery, empty/failed/stale Team, secret request submitting/unknown/expired, keyboard-only interaction, focus return, screen-reader labels/status, touch/narrow display and textual graph equivalent. |
| Regression | Keep ordinary Ask registry park/resume, approval queue/reconnect/resolution/stub, policy/reconciliation user-value preservation, project-shelf authority, shared editor field protections and exact chat-owner/target navigation. |
| Acceptance authority | CI is authoritative for build/Go gates; no full local suite. The specification names `npm run typecheck` and relevant design-system checks, not bare `tsc`. Engine-touching landing requires hands-on tester plus independent validator on the exact candidate. These are future gates, not runs claimed by this review, E30/E38. |

### STRIDE threat summary

STRIDE means spoofed identity, tampered state, missing action attribution, information disclosure, resource exhaustion and privilege escalation. “Gap” below identifies the design concern, not a live penetration-test result.

| Component / flow | S | T | R | I | D | E | Design disposition |
|---|---|---|---|---|---|---|---|
| Peer/web/skill content → Omnipus | Gap | Gap | Existing audit to preserve | Gap | Existing intake limits to preserve | Gap | Preserve trusted sender provenance; CRIT-001/002 invalidate the narrow residual. |
| Go → helper apply | Gap | Gap | Gap | — | Gap | Gap | M-B needs trusted per-batch authority if selected; ordinary depth/admission/Stop still apply. |
| Secret request → submit → consume | Gap | Gap | Gap | Gap | Bound required | Gap | Server-owned request/agent/recipient and atomic single use; value-free audit identifiers, bounded pending count/body/fields/expiry. Decide whether staged writes use narrow destination-gated setup semantics or existing vault re-auth scope; don't reuse raw vault mutation silently, E39. |
| Provider/channel/mail setup | Existing policy | Gap | Existing audit to preserve | Gap | Gap | Existing Ask, with listed holes | Verify effective destination and specific live facts; do not leak values through upstream errors or QR misrouting. |
| Shared agent/skill setup | — | Gap | Impact in summary/audit | — | — | Gap | Resource revisions plus disclosed shared effects; workspace serialization alone is insufficient. |
| Global chat and helper approvals | Preserve owner/acting IDs | — | Preserve resolution receipt | Scope must remain bounded | Gap | No extra authority from viewing | MAJ-007: existing workspace filter can strand requests. |
| Credential removal | Existing Ask | Gap | Preserve durable/live distinction | — | Gap | — | MAJ-009: enabled missing refs can stop boot; choose consumer-safe removal. |

### UX heuristic check

**Several issues to address.** H1 visibility of status applies to submission unknown, partial application and hidden approvals; H3 user control applies to Cancel/Stop/retry; H5 error prevention applies to shared edits and destination matching; H7 keyboard efficiency applies to the new secret/Team interactions; H10 documentation applies to the removed permission-management route. No visual-design decision is made here.

| Priority action | Findings |
|---|---|
| Make the one Omnipus decision surface genuinely actionable from any active workspace. | MAJ-007/008 |
| Give protected input explicit server-confirmed states and keyboard/focus behavior. | MAJ-002/011/012 |
| Make shared effects and recovery instructions visible before applying changes. | MAJ-006/010/013 |

## Unasked questions for the author — technical correction work

These are not additional founder interviews and do not reopen FQ-1–FQ-10.

| Topic | Correction needed |
|---|---|
| Confirmation | What existing authoritative record can retain a chosen human Go for batch A while the main handles batch B, if M-B is selected? |
| Staging | What atomic credential-store operation, durable envelope and handles-only resume preserve single use without a separate vault/workflow record? |
| Binding | Which effective endpoint/account/executable facts are shown and frozen in the server-owned destination descriptor? |
| Target context | Which setup operations use the acted-on workspace, and how is an existing-main kickoff represented without re-binding the main? |
| Verification | What exactly does each Working result prove, and which durable partial fields must never be discarded? |
| Wire / UI | Which generated live/snapshot/pairing/submit representations, components and state owners implement the new card? |

## Questions for the founder — NEW only

The ADR's FQ-1–FQ-10 remain in its own interview list. These six additions are distinct; answer, for example, `FQ-11 A, FQ-12 B`. Recommendations are not decisions.

### FQ-11 — What outbound-disclosure risk should Omnipus carry?

**Context:** Omnipus's allowed file reads plus public URL fetches can send private document content outside the machine. The proposed confirmation check in change tools does not cover this path; this is broader than FQ-4's persistent-configuration risk.

**Impact:** Determines whether technical look-ups may disclose readable data without a human approving that request.

| Option | Choice |
|---|---|
| A | Keep the grants and explicitly accept read-to-web disclosure in the threat model and user documentation. |
| B | Keep necessary reads, but require approval for Omnipus's outbound web calls using the existing global policy/Auto-classification mechanism; deny web calls on other agents where not intended. Disclose the install-wide effect of any global change. |
| C | Remove outbound web capabilities from Omnipus's shipped role and use existing local/catalogue look-ups. |

**Recommendation: B**, with security-lead defining the exact existing-policy changes and scope. This reduces unapproved egress without inventing content-sensitive policy rules; it does not claim to eliminate injection or all disclosure channels.

### FQ-12 — Should creating an external-program worker require approval?

**Context:** `create_agent` can save a caller-chosen executable path. A later delegation runs that executable, including a version probe, outside native tool-approval enforcement. FQ-2 covers MCP installation and provider routing, not this worker-creation path.

**Impact:** Determines whether an injected Allow creation can plant later host execution, and how many approvals ordinary native-agent creation needs.

| Option | Choice |
|---|---|
| A | Keep native creation Allow; put external-worker creation/executable selection behind a distinct existing-model Ask tool, including equivalent executable-changing operations. |
| B | Make all agent creation Ask through the existing policy layers. |
| C | Keep creation Allow and explicitly accept the external executable-selection risk. |

**Recommendation: A.** It isolates the host-execution trust decision while keeping the common native-agent workflow low-friction; security-lead must review the resulting complete Ask surface.

### FQ-13 — What happens when a secret is still used by an enabled integration?

**Context:** Removing an enabled channel's referenced credential can refuse reload and stop the whole gateway on the next start, rather than merely disconnecting that channel.

**Impact:** Determines whether secret removal can leave the application unable to start and whether disconnect is included in the removal workflow.

| Option | Choice |
|---|---|
| A | Refuse removal while an enabled consumer references it; show the consumers and ask the user to disconnect or replace first. |
| B | Offer one explicitly confirmed disconnect-and-remove sequence covering every affected consumer, then remove only after its references are safely cleared. |
| C | Permit raw removal after warning about live reload and gateway-start failure. |

**Recommendation: A** for the first version. It is the smallest safe behavior; B can be chosen if the combined disconnect is required now.

### FQ-14 — May team setup reconfigure an agent reused from another workspace?

**Context:** A reused agent's model, tools, skills and instructions are global. Tailoring it for the new team changes it in its existing workspaces too; resource revision checks do not make that change local.

**Impact:** Determines whether W18 is a membership-only reuse step or may change other teams' working behavior.

| Option | Choice |
|---|---|
| A | Reuse unchanged when it fits. Any global change is a separate shared-impact proposal showing before/after and every affected workspace. |
| B | Allow global tailoring inside W18, but its one summary must explicitly show the shared changes and affected workspaces. |
| C | Reuse only agents that need no changes; otherwise create a separate agent rather than alter the existing identity. |

**Recommendation: A.** It preserves reuse without quietly changing another workspace and still allows a deliberate shared update when necessary.

### FQ-15 — Must every Ask operation execute in Omnipus's main chat?

**Context:** D27 keeps interview and summary in the main and requires helper approvals to reach the user there. The ADR additionally moves every Ask operation back into the main, creating helper-to-main handbacks for mixed workflows. FQ-8 asks about attention, not where those operations execute.

**Impact:** Determines whether long connector/provider application and its verification can stay in a helper while Omnipus remains available for conversation.

| Option | Choice |
|---|---|
| A | Keep the main-only Ask execution rule and specify the phase handbacks and interruption behavior. |
| B | Let an authorized helper execute Ask tools; show the human approval in the Omnipus surface with the acting helper identified. Keep interview, secret entry and summary in the main. |

**Recommendation: B.** It uses ordinary helper execution and approval delivery without another workflow coordinator; policy tightening must still reach the same human decision surface.

### FQ-16 — What does prompt-free God Mode mean after the operator tightens Omnipus?

**Context:** Removing shipped per-agent Ask entries satisfies the fresh-default case. A later operator-set per-agent Ask still survives God Mode under the required two-layer merge. This is separate from FQ-5's environment_setup seed choice.

**Impact:** Determines whether D7 is a shipped-default promise or a permanent restriction on Omnipus's per-agent policy editor.

| Option | Choice |
|---|---|
| A | Preserve editable per-agent Ask and the normal merge; qualify D7 as prompt-free for shipped defaults, with operator tightening still respected. |
| B | Preserve the merge but disallow per-agent Ask for Omnipus; Ask is configured globally and per-agent Deny remains available. Explicitly amend the ordinary-built-in editability rule and its UI/documentation. |

**Recommendation: A.** It retains operator control and the existing policy contract. Do not override per-agent Ask in code, erase it on reload or add a third policy layer to make the promise appear true.

## Verdict rationale and next action

**BLOCK** follows from CRIT-001 and CRIT-002: the proposed trust boundary excludes callable disclosure and executable-creation paths. This does not revoke the founder's accepted instruction-enforced Go risk. It requires describing its real consequences and obtaining a disposition of the additional exposure before accepting the design. The major findings identify architectural contracts needed before a specification can safely elaborate the behavior.

| Next action | Owner |
|---|---|
| Interview on the ADR's existing FQ-1–FQ-10 plus only the six new questions above, using the corrected premises/disagreements. | team-lead / founder |
| Review the complete Ask surface, including read/outbound combination and external-worker creation; explicitly dispose of retained risks. | security-lead |
| Make exactly one correction to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md` after the interview. | architect author |

This is the ADR's one fixed grill round. Next: team-lead interviews the founder on “Questions for the founder”, then architect makes the one correction round. **No second grill round runs on this ADR revision.** Any blocking finding still open after that correction goes to the founder, not another grill. This report does not perform or authorize the correction.

skills: omnipus-shared-rules, grill-spec, gitnexus-exploring, ux-heuristics-review, jev-use:jev-use, commit-messages

## Evidence table — grill-osa-adr

| Claim | Evidence | Certainty |
|---|---|---|
| E01 — Reviewed the commissioned tree, not a stale baseline | `git rev-parse HEAD`, exit 0 → `4b298976266ac5e5e13c55dceba694227b743507`; `git branch --show-current`, exit 0 → `work/omnipus-agent-design-20261012`; `git diff --name-status 5054b1455b0c8b108fdb2b9ff314c612246a8cc7 4b298976266ac5e5e13c55dceba694227b743507`, exit 0 → only the input ADR added. Initial `git status --porcelain=v1`, exit 0 → empty. | Verified, high confidence |
| Founder requirements traced, not adopted from the stale trade-off table | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-system-agent-decisions-20261012.md`::Decisions Log D1–D27; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/omnipus-workflows/index.html`::workflow skeleton, W1/W18, dependencies, delegation, routing, screens and §3 trade-offs; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/BRD-omnipus-system-agent-20261011.md`::§3/§5/§8/§9. Source inspection, not browser acceptance. | Verified, high confidence |
| E02 — Tools and Security expose the changed user promises | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/tools.md`::How to change an agent's tool access, Allow ask and deny; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/security.md`::What reading searching and listing can reach, God Mode, Limits and things to watch. | Verified, high confidence |
| E03 — Ordinary reads are open outside protected paths | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/fspolicy/policy.go`::FSPolicy.ReadConfined; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/resolvepath.go`::resolvePath.resolveValidatedPath. | Verified source, high confidence |
| E04 — Public fetch sends the selected URL | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/web_fetch.go`::webFetchParseArgs, WebFetchTool.doFetch. | Verified source; injected disclosure Inferred, high confidence |
| E05 — Creation persists caller-selected external CLI path | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/agent.go`::agentCreateToolExecute.validate, agentCreateToolExecute.prepareConfig. | Verified source, high confidence |
| E06 — Executable selection and version probe are real host execution | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/runner/executor_opts.go`::resolveCLIBinary; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/runner/driver_stream.go`::detectCLIVersion. | Verified source; malicious launch scenario Inferred, high confidence |
| E07 — External permissions bypass native popup consent | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/external_dispatch.go`::policyApproverConsent.RequestConsent. | Verified source, high confidence |
| E08 — Answered cards are removed; terminal metadata is one slot; auto-default answers exist | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/askuser/registry.go`::Registry.Submit, PendingForSession, removeLocked, persist, persistTerminal, persistUserResumeRecord, fireDefaultSafeSet. | Verified source; proposed M-B failure Inferred, high confidence |
| E09 — Store primitives do not form one single-use promotion | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/credentials/store.go`::Store.Get, Store.Set, Store.Delete. | Verified source; composed-operation race Inferred, high confidence |
| E10 — Question parking returns a stub; answers start a new turn | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/ask_user_question.go`::AskUserQuestionTool.Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/askuser/registry.go`::Registry.dispatchResume, persistUserResumeRecord. | Verified source, high confidence |
| E11 — Sensitive-value registration replaces the full set | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/config/security.go`::Config.RegisterSensitiveValues, sensitiveReplacementPairs. | Verified source, high confidence; no claim redaction alone protects every value |
| E12 — Recipient details are mutable behind stable slots | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/provider.go`::ProviderConfigureTool.Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/mcp.go`::mcpAddToolExecute.prepareEntry; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_mailbox.go`::restAPISetAgentMailbox.persistConfig. | Verified source; wrong-recipient consumption Inferred, high confidence |
| E13 — list_models ignores configured base and keyless providers | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/provider.go`::ModelsListTool.Execute, fetchProviderModels. | Verified source, high confidence |
| E14 — A shared protocol-aware listing seam exists | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/providers/validate.go`::ListModels. | Verified source, high confidence; no claim that listing proves execution |
| E15 — Project authoring/removal uses calling workspace; conflict literal differs | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/skill_authoring.go`::resolveProjectShelfWithCollisions, SkillEditTool.Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/skill.go`::SkillRemoveTool.Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-072-skill-activation-and-loading.md`::Skill activation and loading / D6.1. | Verified source, high confidence |
| E16 — Helper workspace and depth inherit the verified parent | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/steer_launcher.go`::SteerLauncher.launchSteered; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::Session core with an agent address book / D4. | Verified source/design, high confidence |
| E17 — Message workspace is session binding; setup kickoff is mint-only | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/asyncapi.yaml`::MessageFrame.metadata; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/websocket_chat.go`::wsHandlerHandleChatMessage.validateFrameIDs. | Verified source/contract, high confidence |
| E18 — Agent writes are global/revision-checked and expose partial state / CONFLICT | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/agent_update_adr090.go`::AgentUpdateTool.executeADR090, mutateErrorResult. | Verified source, high confidence |
| E19 — Approval workspace is stamped from acting session / ancestry | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/ws_tool_approval.go`::WSHandler.approvalWorkspaceID, broadcastToolApprovalRequired, sessionStateBytes. | Verified source, high confidence |
| E20 — The approval display only matches active workspace | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/store/toolApproval.ts`::isApprovalInScope, reconcileWithSessionState; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/agents/ToolApprovalModal.tsx`::ToolApprovalModal. | Verified source; global-chat failure Inferred, high confidence |
| E21 — QR update wire/consumer is channel/Settings-scoped | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/asyncapi.yaml`::WhatsAppPairingFrame, WhatsAppPairingSubscribeFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/store/chat/slices/replay-and-status-frames.ts`::handleReplayAndStatusFrame (whatsapp_pairing case). | Verified source/contract, high confidence |
| E22 — Mail ownership/read-send identities and non-model inbox addressing are established | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/openapi.yaml`::getAgentMailbox, setAgentMailbox; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-033-per-pair-mailboxes.md`::Per-(Agent, Workspace) Email Mailboxes / §2/§4; E12 mailbox server fields. | Verified source/design, high confidence; future live oracle Unknown |
| E23 — Enabled missing refs can abort gateway boot | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/gateway_boot_credentials.go`::buildEnabledRefMap, bootCredentials; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-004-credential-boot-contract.md`::Credential Boot Contract / Failure Semantics, compared with current provider degradation code. | Verified source, high confidence |
| E24 — Delete commits before reload, with retained-state warnings | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_settings.go`::restAPI.deleteCredential. | Verified source; later restart scenario Inferred, high confidence |
| E25 — Creation can partly save before home/join/publication | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/agent.go`::agentCreateToolExecute.persistAndJoin. | Verified source, high confidence |
| E26 — Existing user journeys distinguish confirmed state from connection/restore uncertainty | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/using-omnipus-ui.md`::The sidebar, The chat area, If the gateway restarts during a reply; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/mail.md`::attachment save-result-unknown guidance. | Verified documentation, high confidence; new secret renderer behavior Unknown |
| E27 — Reusable controls/composites already exist | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/design-system/catalog.json`::entries (Button, Field, Input, Label, FormError, ConfirmDialog, EmptyState, QueryErrorState, Tabs, ViewSwitch, AgentIcon); JSON catalogue inspection, exit 0 → named entries printed. | Verified catalogue, high confidence; not rendered-component acceptance |
| E28 — Current approval surface deliberately defines safe keyboard/focus behavior | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/agents/ToolApprovalModal.tsx`::Accessibility comment / ToolApprovalModal. | Verified source description, high confidence; actual assistive-technology behavior Unknown |
| E29 — Specific doc TODO mapping/Tools are missing from the ADR inventory | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md`::User-facing documentation, complete section read; E02's known-present Tools instructions establish the affected page. | Verified text/coverage gap, high confidence |
| E30 — Documentation and exact-commit acceptance are mandatory | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/CLAUDE.md`::Definition of Done, Hard Constraints #6/#8, Contract regeneration, Build test and quality gates. | Verified instruction, high confidence |
| E31 — God Mode retains per-agent Ask / Deny | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/compositor.go`::resolveEffectivePolicyWith; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-090-built-in-agents-skills-and-visual-reading.md`::Built-in agent configuration, skills, and visual file reading / §2.6/§6.5. | Verified source/design, high confidence |
| E32 — Catalogue-first / publication and brand constraints apply | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/ui/CLAUDE.md`::No hand-built controls, Publishing a component is a four-part contract; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/design/design-system-definition.md`::Operating model, The rules in plain language; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/brand/brand-guidelines.md`::Brand Foundation, Brand Voice and Tone. | Verified instructions, high confidence |
| E33 — Current Skill wording retains later turns | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/skill.go`::SkillTool.Description; controlled search, exit 0 → “where they stay for later turns”. | Verified source text, high confidence |
| E34 — Current custom seeds explicitly deny management tools | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent/seed.go`::NewCustomAgentToolsCfg; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/agent.go`::agentCreateToolExecute.prepareConfig; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent/role_policies_adr090.go`::ADR090RolePolicyInventory, adr090SparseRolePolicies. | Verified seed source, high confidence; no claim of immutable management denial |
| E35 — Removed roster exposes default and engine editing today | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/screens/AgentListScreen.tsx`::AgentsLibraryView (onSetDefault, systemAgents); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/routes/_app/agents.$agentId.tsx`::AgentProfileRoute. | Verified source, high confidence |
| E36 — Reachability search has a positive control and finds no proposed new tools | `rg -l '"AskUserQuestion"' /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/config /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools`, exit 0 → current seed/default/tool entries; `rg -l -e '"request_secret"' -e '"connect_channel"' -e '"connect_mailbox"'` with those same three absolute roots, exit 1 → no matches. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/central_builtin_registry.go`::buildCentralBuiltinRegistry; current metadata/live families read. | Verified bounded pre-implementation search, high confidence; no delivery PASS |
| E37 — Existing known-workspace selection switches active workspace | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/chat/useSelectSession.ts`::useSelectSession. | Verified source, high confidence |
| E38 — False-green guidance was read; no product run inferred from source | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/false-green-patterns.md`::Checklist before you claim something passes, A substring scan is not a behavioural test, The generated validator can be weaker than the contract. No test/build/UAT/CI results claimed. | Verified review boundary, high confidence; proposed runtime outcomes Unknown |
| E39 — Raw vault write/delete has a narrower enumerated re-auth boundary to preserve or distinguish | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-022-credential-vault-reauth.md`::Re-auth Consent Gate: Credential-Vault Scope Expansion to v0.1.0 / Decision; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_settings.go`::restAPI.deleteCredential. Not claimed to require re-auth on every existing tool-mediated secret write. | Verified source/design, high confidence |
| Existing-policy constraints and reconciliation were independently checked | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/config/defaults.go`::defaultToolPoliciesSysagent, defaultToolPoliciesGeneral; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/auto_approve.go`::autoApproveClasses; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/config/validate.go`::ReconcileToolPolicyCeiling; **Reconcile the global tool-policy ceiling against shipped defaults on every load**, **Two-layer tool-policy model: the reconciled global ceiling IS the default; remove the fail-closed per-agent backfill**, **Shell permission modes: Ask / Auto / God Mode; drop the block list; one rule format**, all read in this worktree. | Verified source/design, high confidence |
| GitNexus change-detection green was rejected | `gitnexus status`, exit 0 → `Repository not indexed.` `gitnexus detect-changes --scope all`, exit 0 → `No changes detected.`, but stderr identifies a sibling index 3571 commits behind this checkout. The new report was also untracked at that point. This cannot establish scope; the staged Git diff is the publication authority. | Verified instrument limitation, high confidence; no graph-derived impact claimed |
| Architecture references used as dated context, not overriding code | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/AS-IS-architecture.md`::Agent Loop, Tools System, Skills, Contract-first wire-format pipeline; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/plugin-extensibility-assessment.md`::Tools, Skills, MCP; all prior ADR titles inventoried, relevant decisions read. | Verified document review, high confidence; no claim all historical statements remain current |
| Report-only scope and human commit identity | Report path was absent before writing; initial index/worktree clean. `git config user.email`, exit 0 → `10800669+daniel-piatkowski-ai@users.noreply.github.com`; `git var GIT_AUTHOR_IDENT` and `git var GIT_COMMITTER_IDENT`, each exit 0 → Daniel Piatkowski with that address. No active commit hooks found in the resolved hook directory. No production symbol edited; graph impact is not claimed. | Verified pre-publication checks, high confidence; final commit receipt accompanies hand-back |
| **Self-check** | Rechecked the source mechanisms, all 27 founder rows, twelve-lens coverage, twenty unique findings, existing FQ disagreements and six genuinely new questions against the dispatch. Reviewed the complete report diff before commit; confined output to this report, with no ADR/code edit or runtime-success claim. Input ADR SHA-256 pinned as `7e983f4e5dfff53f22390dc1e57b1d86be7b42b31945f8f610c94263a60116c6`; final scope/hash/identity receipt accompanies hand-back. | Verified document/source self-check; runtime behavior not tested |
