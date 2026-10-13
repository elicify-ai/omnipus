# Adversarial Review: The Omnipus system agent specification

**Verdict: BLOCK — 1 CRITICAL, 13 MAJOR, 2 MINOR findings and 1 OBSERVATION.** The private-secret requirement omits the decided workspace boundary. The secret lifecycle and channel-routing rules retain superseded behaviour, and parts of the test plan could report success without proving the requirements assigned to them.

| Review header | Value |
|---|---|
| Document reviewed | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec.md` |
| Mode / round | **SPEC MODE — round 1 of 2**, independent reviewer; not the spec author |
| Review date | 2026-10-12 |
| Receipt lane | `grill-osa-spec-r1` |
| Branch / reviewed commit | `work/omnipus-agent-design-20261012` / `5ac3ddb64d8c5b7cc2165d4e1d3f9c981d30a2d6` |
| Input fingerprint | SHA-256 `b4e3754d552fdcc0b1de44db83839e3ac472e26882e030a1f5b3d4d7983c418e` |
| Later authority | Founder answers, **Spec open questions OQ-1..OQ-4**, in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-adr-founder-answers-20261012.md`; fingerprint `ad88fc5b45ada967946343bcf91e190a600343c3104903db89523c5a6cea6c99` |
| Review boundary | Only this report is written/committed. No spec/ADR/contract/production changes, builds, product tests, servers, stash or push. |
| Method | Complete spec, corrected **The Omnipus system agent** ADR and its original grill, founder records and workflow-draft source; relevant architecture, decision titles, contracts, user docs and direct source reads. The Skill invocation returned candidates for this **same round**; findings retained here were independently checked against the commissioned tree. No additional grill round was started. |
| Instrument limitations | GitNexus MCP is not exposed. Local status says this checkout is not indexed; the branch-scoped change detector rejected a stale sibling index. Direct reads/search and scoped Git diff replace graph evidence. Jev's batched severity judgments escalated for low confidence; the reviewer decided those. No runtime or rendered-accessibility PASS is claimed. |

## Executive summary

The review applied all twelve lenses with equal backend and frontend scrutiny. Every one of the 86 functional requirements and 96 declared behaviour-driven scenarios occurs in the traceability matrix, but several links do not test the claimed behaviour; the 61 planned test rows are not 61 verified proofs. Screens beyond the secret card lack complete state/keyboard contracts, ordinary-agent secret-tool access is not wired in the plan, and the picker must retain existing team-eligibility exclusions. Omnipus's editable helper self-row also needs UI acceptance coverage; its exclusion from the Team list does not require exclusion from the graph.

OQ-1..OQ-4 are **decided**, not interview questions. R-A1–R-A3 remain founder-accepted risks; this review does not reopen them, reinstate a code Go gate, add a third policy layer, or commission a workflow engine.

| Severity | Count |
|---|---:|
| CRITICAL | 1 |
| MAJOR | 13 |
| MINOR | 2 |
| OBSERVATION | 1 |
| **Total** | **17** |

**Certainty:** document omissions/contradictions and cited source mechanisms are **Verified (high confidence)**. Future failure scenarios are **Inferred**, with confidence stated per finding. They are not executed exploits. Evidence IDs resolve to absolute `file::symbol` citations in the final table. Proposed tests/components are plans, not existing implementations.

## Findings

### CRITICAL findings

#### CRIT-001 — Agent-private secrets omit the decided workspace boundary

| Field | Review |
|---|---|
| Lens | 6 — Security; also 3 — Inconsistency |
| Affected section | FR-076; US-7; BDD-6.3 / BDD-7.1–7.4; dataset D1; K1/K2/K6 |
| Failure scenario | Mia uses `WEATHER_TOKEN` in Sales and Marketing. Storage keyed only by Mia plus variable name replaces or exposes Sales's value in Marketing, even though the founder expressly forbids Mia in the second workspace using it. Concurrent turns make an agent-wide cached environment especially unsafe. |
| Evidence | FR-076 says **“bound to (agent, variable name)”**. BDD-6.3 distinguishes Mia from Jim, not Mia@Sales from Mia@Marketing. Authoritative OQ-1 requires **workspace + agent** ownership and adding the value again to share it. Existing command-environment composition is per authorized turn root, not per-agent state (E02, E12). |
| Certainty | **Verified (high confidence):** incompatible storage tuple and insufficient fixtures. **Inferred (high confidence):** cross-workspace disclosure under the literal implementation. |
| Recommendation | Require `(workspace_id, agent_id, variable_name)` for storage, listing, replacement/removal, descriptor comparison and child injection. Resolve scope from the trusted execution context/authorized setup target, never the active UI workspace or an unchecked tool argument. Make workspace/agent ownership visible on the private-secret card and its generated display contract. Add same-agent/two-workspace fixtures with identical variable names and different values, alternating and concurrent turns, plus another-agent and wrong-workspace consumption refusals. |

### MAJOR findings

#### MAJ-001 — Secret staging, expiry and Cancel still implement the superseded lifecycle

| Field | Review |
|---|---|
| Lens | 3 — Inconsistency; 2 — Incompleteness; 12 — Overcomplexity |
| Affected section | US-3 AS-2; US-6 AS-5; sections 4–6; FR-063–067/069/072; BDD-3.2 / BDD-6.4–6.5/6.9–6.10; tests 7–10; WU-4 |
| Failure scenario | A user submits a card, believes the secret was saved, then cancels the setup plan or spends more than 30 minutes discussing it. The specified promotion/expiry design deletes the value, contrary to the decided durable-save behaviour. Cancel's unconditional “nothing was written” also becomes a false success statement. |
| Evidence | OQ-3 now says **stored on Submit into the encrypted store, no plan confirmation and no staging expiry**; an unused saved entry remains listable/removable after workflow cancellation. FR-063/064 still stage and promote; FR-066 expires values; FR-067 deletes staged values on Cancel/Stop. Sections 5/6 and the planned store tests repeat that design (E01, E02). |
| Certainty | **Verified (high confidence):** superseded lifecycle throughout the spec. **Inferred (high confidence):** saved-value loss or incorrect cancellation report. |
| Recommendation | Rewrite the complete state machine, tests, work unit and documentation together: Submit durably saves all fields or none; Cancel before Submit saves nothing; workflow Cancel/Stop after Submit does not delete saved values. Distinguish a pending request or single-use authorization handle from the durable secret: safety metadata may terminate without deleting the secret. Remove staging-until-Go promotion and saved-value expiry, not merely rename them. Report saved-but-unused entries and the normal list/remove path. **Q-R1-1 below separately asks when replacing an already-used secret takes live effect; storage timing is not reopened.** |

#### MAJ-002 — Named-agent legacy channels still bypass mandatory workspace binding

| Field | Review |
|---|---|
| Lens | 3 — Inconsistency; 7 — Reachability |
| Affected section | US-13 AS-1; FR-121/127; K14; section 5; D3 row 6; OQ-2 |
| Failure scenario | An existing channel has a legacy wildcard binding naming Mia but no workspace. The spec's “no binding **and no named agent**” failure predicate allows it to continue routing, or the Settings panel creates another such configuration. It violates the decided one workspace/agent pair and can select an unintended main. |
| Evidence | Authoritative OQ-2 removes **all** unbound modes, including those naming an agent. Current `validateUnbound`/`persistUnbound` and the panel's omitted `workspace_id` implement that mode; current ChannelRouting permits both fields to be omitted. The spec expressly leaves the named-agent case open (E02, E04). |
| Certainty | **Verified (high confidence):** reachable legacy mode and undecided spec predicate. **Inferred (high confidence):** continued unbound routing if only the global fallback is removed. |
| Recommendation | Require nonblank workspace and agent on routing writes and the Settings selector. Reject inbound traffic for every existing unbound instance before any peer/wildcard/global fallback can run, with **“bind this channel to an agent in a workspace”**, instance-specific degraded status, error log and audit event. Update K14 and D3 row 6; test REST, UI and runtime for an explicit agent without a workspace, omitted/blank/null fields, deleted/non-member agents and a valid bound positive control. Do not automatically re-home legacy traffic. |

#### MAJ-003 — Submission-unknown recovery cannot learn a resolved request's state

| Field | Review |
|---|---|
| Lens | 5 — Contract-first gaps; 8 — UI states and journey |
| Affected section | US-6 AS-7; FR-074; K3/K4/K5; BDD-6.7; tests 25/55 |
| Failure scenario | Submit commits, but both its HTTP response and terminal WS frame are lost. On reload K4 returns only pending requests, so absence cannot distinguish submitted from cancelled, stopped, missing or unavailable. The UI either requests the value again or invents success. |
| Evidence | K3 defines a live resolved frame; K4 names **pending** requests; K5 returns a state only from Submit. No state-reading operation or terminal snapshot/replay retention is defined. Current SessionState and SessionSnapshot contracts do not supply terminal secret state automatically (E01, E05). |
| Certainty | **Verified (high confidence):** missing recovery representation. **Inferred (high confidence):** ambiguous completion after lost response plus lost event. |
| Recommendation | Name a generated request-state read contract **or** a complete authoritative terminal snapshot/replay representation. Define value-free retention, unknown/unavailable handling and same-request reconciliation after gateway restart. The UI must never re-submit plaintext automatically. Test losing both responses, reloading a fresh client, a second tab resolving the request, stale snapshots crossing terminal frames, and missing/expired metadata without treating absence as success. Stored-value lifetime follows OQ-3, not the request-state retention period. |

#### MAJ-004 — K1–K17 omit a wire change and runtime-validation proof

| Field | Review |
|---|---|
| Lens | 5 — Contract-first gaps; 11 — Testability |
| Affected section | Section 7; K1–K5/K8/K14/K16; FR-062/071/122; SC-010; WU-0 |
| Failure scenario | Backend rejects a chat without an agent but the generated MessageFrame still says that field is optional. Alternatively, secret limits/field exclusivity or new WS frames compile in generated types but are never enforced by the actual transport validators. Contract regeneration passes while the security or recovery invariant remains false. |
| Evidence | FR-122 requires agent selection for **WS and REST**, but K16 changes only REST's required `agent_id`; K8 names workflow metadata, not the WS requirement. Current inline MessageFrame requires only type/content. Backend body/frame validation is optional; `decodeAndValidate`'s disabled path is plain decoding. SPA `parseFrameSafe` validates against the generated frame union. Generator output also includes the inbound-schema mirror, absent from section 7's artifact inventory (E05, E06, E21). |
| Certainty | **Verified (high confidence):** named wire omission and actual validator seams. **Inferred (high confidence):** compile/drift green without correct runtime enforcement. |
| Recommendation | Add the nonblank MessageFrame agent rule to K8 and its inline/shared mirrors. Specify actual REST paths/methods, required fields, Submit-versus-Cancel exclusivity, field-key matching, UTF-8 byte versus schema-character limits, array nil-safety and safe error envelopes. Include the generated inbound mirrors and real WS/REST schema dispatch/consumer integration. Critical semantic checks must hold with optional inbound validation **on and off**. Add passing controls plus malformed/missing/extra/null/oversize payload tests through the real parsers/handlers, including nested generated-validator limits. Keep schema → reference → generation → atomic generated commit → consumer order and the inline-union rule from **Discriminated-union create contract for agents**. |

#### MAJ-005 — Default removal has no complete per-caller replacement oracle

| Field | Review |
|---|---|
| Lens | 1 — Ambiguity; 2 — Incompleteness; 3 — AS-IS/code consistency |
| Affected section | Sections 2/10 K; FR-120–126; BDD-13.4/13.6; tests 22/23/46 |
| Failure scenario | Removing the getter leaves anonymous system messages without a defined visible disposition, channel-recording state uninitialized, or skill/tool listing empty. A goal with deleted owner Mia but another retained participant Jim can still be reassigned to Jim before the default-getter branch is reached. Symbol-absence checks remain green. |
| Evidence | FR-124 says internal look-ups “MUST work” but does not choose their data/root owners. The amended ADR explicitly delegates its per-caller decisions to the spec. `resolveSystemMessageAgent` and `publishResponseIfNeeded` are not dispositioned there. `resolveGoalAgent` tries `AgentIDs` after canonical owner; REST/WS also have `firstChatTargetAgentID` beyond the getter. Test 22 is a registry test, not a gateway boot/catalogue/readiness proof (E03, E07). |
| Certainty | **Verified (high confidence):** caller mechanisms and missing replacement oracles. **Inferred (high confidence):** remaining ownership fallback or dead internal service after mechanical deletion. |
| Recommendation | Add the per-caller table below to the corrected spec with exact replacement data owners and visible failures. Refuse stale/absent canonical execution owners before secondary participants or first-agent candidates are considered. Keep nil-agent error publication independent of borrowing another agent's tools. Test real boot and reload, tool/skill endpoints, addressed/anonymous system input, stale owner with another valid participant and empty registry. Existing `default_agent_id` is **ignore + warning**, not a migration, permission reset or guessed re-routing; test load and reload of that configuration. |

#### MAJ-006 — Presence in the matrix is mistaken for meaningful test coverage

| Field | Review |
|---|---|
| Lens | 11 — Testability and false-green risk |
| Affected section | Sections 9.2/9.5/12/13; FR-005/006/060/073; F:D3; SC-002 |
| Failure scenario | Tests reject agent-less requests, operate a directly constructed secret helper and show a consumer list. The plan reports coverage for singleton Omnipus chat, all-native access and secret-name listing even though explicitly creating a second Omnipus chat or denying `list_secrets` would not fail them. Empty captures can also make a zero-hit secret scan pass without observing the protected path. |
| Evidence | FR-005 → BDD-13.3/test 45 tests **no agent**, not a second explicit Omnipus chat. FR-006 → target-skill test 14 does not establish exclusive cross-workspace authority. FR-073 → removal test 48 does not call `list_secrets`. F:D3 has no named matrix source/explicit up-front-readiness scenario. BDD-1.4 and 1.5 are not assigned in section 9.2. Current REST create calls NewSession for any explicit chat target. The controlled static inventory verifies 86 rows/96 scenarios/61 tests, with no missing matrix scenario after proper range expansion (E01, E07, E09, E24). |
| Certainty | **Verified (high confidence):** semantically wrong assignments and real session minting. **Inferred (high confidence):** false coverage if the proposed oracles are followed. |
| Recommendation | Add explicit second-chat tests on REST and WS mint paths, with ordinary-agent extra-chat controls; actual scoped `list_secrets` calls; exclusive authority tests; and up-front dependency/readiness versus installation-success scenarios for D3. Assign REST removal/membership filtering cases explicitly. Give every mandatory ADR-grill negative control a requirement/scenario/test owner, not an unassigned prose list. Capture actual model requests, decode serialized values before searching, assert nonempty expected capture/sink counts, and prove a deliberately exposed sentinel is detectable. Prompt-text tests prove wording only; a live-model pass does not prove the instruction-only Go gate cannot be bypassed. Preserve the accepted private-command masking limit. |

#### MAJ-007 — God Mode suppresses genuinely missing information as well as approvals

| Field | Review |
|---|---|
| Lens | 1 — Ambiguity; 3 — Contradiction; 4 — Infeasibility |
| Affected section | US-3 opening/AS-4; section 4 Boundary conditions; BDD-3.4; tests 47/60 |
| Failure scenario | In God Mode the user says “add a connector” without choosing its server or credential. The no-question-card rule forces Omnipus to guess, fail, or violate the acceptance scenario instead of asking for the missing information or showing secret entry. |
| Evidence | The corrected **The Omnipus system agent**::OSA-3 expressly preserves **genuinely missing interview information and secret entry** in God Mode. The spec says no question card appears and uses an end-to-end W10 scenario without a completeness precondition. The existing question tool distinguishes clarification from approval (E03, E15). |
| Certainty | **Verified (high confidence):** contradictory card prohibition. **Inferred (high confidence):** guessed or blocked setup from an underspecified request. |
| Recommendation | Say God Mode skips permission approvals and the Go/Change/Cancel confirmation, not necessary clarification or secret entry. Split fully specified zero-confirmation tests from missing-information/secret tests. Also resolve BDD-2.2's “either load W3 or ask” against FR-013: use a genuinely ambiguous fixture that must ask, and a separate unambiguous W3-with-W10-subpart fixture. Do not let an either/or oracle pass a violation of the decided routing rule. |

#### MAJ-008 — Changed screens lack complete state and keyboard journeys

| Field | Review |
|---|---|
| Lens | 8 — UI states and journey; 9 — Accessibility and keyboard |
| Affected section | US-5/US-12; FR-100–110; BDD-12.*; tests 26–29/52/54; WU-10 |
| Failure scenario | A failed membership fetch is rendered as an empty team and offers setup against an already-configured workspace. A failed mutation/refresh silently shows membership as current. Keyboard users can operate secret/Team actions but cannot reliably reach the new Add choices, list/graph switch, System agents navigation or W18 completion path. Unknown attention is mistaken for zero. |
| Evidence | FR-074 gives the secret card a state list; Team, picker, System agents, global entry/attention and team-completion do not each name loading/empty/error/partial states. AS-6/FR-109 cover secret and Team actions only. Existing user docs explicitly distinguish first-load failure, cached stale data, main-chat unavailability and unknown attention; existing Team code has distinct loading/error/cached paths (E01, E10, E18). |
| Certainty | **Verified (high confidence):** missing per-surface contracts and existing recovery expectations. **Inferred (medium confidence):** incorrect empty/saved state or inaccessible new navigation. No rendered failure is claimed. |
| Recommendation | Add a screen/state table for every changed surface: initial loading, true empty, unavailable model/catalogue/main, cached/stale/partial data, denied or uncertain mutation, success, Retry and disabled actions. Preserve sending and message-retry blocks during unresolved attach/navigation. Extend keyboard/focus/announcement acceptance to navigation, picker/search, tabs, shared-editor entry and completion/Open chat. Specify secret-card initial focus, submission keys, no chat-composer propagation, safe terminal focus, error association and cleared-value behaviour. Test real shell/sidebar integration, touch/narrow reflow and non-colour/reduced-motion attention cues. |

#### MAJ-009 — The reachability plan grants new tools only to Omnipus and proves metadata, not execution

| Field | Review |
|---|---|
| Lens | 7 — Reachability; 6 — Two-layer policy |
| Affected section | FR-007/021/060/073; section 13 new-tools row; tests 33–35/61; WU-2/WU-4 |
| Failure scenario | `request_secret` is in the static catalogue and ceiling, and Omnipus can call it. Mia/new custom agents still get explicit Deny from their inventories/constructors. Or the tool exists only in the metadata catalogue and is absent from the executable registry. The grep count and permissions E2E pass, while the promised all-agent feature cannot be invoked. |
| Evidence | The reachability row names **Omnipus per-agent seed** and “≥ 3 files per tool”; test 61 concerns permission changes, not all six tools. `ADR090RolePolicyInventory` starts every ungranted static name at Deny, sparse seeding retains the difference from Allow, and `NewCustomAgentToolsCfg` uses `denyAllThenOverride`. Central metadata instances are expressly never executed (E11). |
| Certainty | **Verified (high confidence):** constructor/metadata distinction and omitted grant/wiring inventory. **Inferred (high confidence):** ordinary/custom access remains denied or inert. |
| Recommendation | Name static names, shipped ceiling, intended ordinary-role grants, custom-constructor grants, hidden-engine/external exclusions, general scope for all-agent request/list tools, executable wiring and reload wiring. Inventory Omnipus's exact supporting role names, not only grouped ADR labels. Prove actual dispatch from a fresh ordinary built-in and a fresh custom native agent through request → card → durable save → permitted consumption; prove excluded callers/management tools refuse. This is seeded role data in the existing two layers, **not** restored per-agent coverage backfill or a hardcoded resolver exception. |

#### MAJ-010 — Private-secret inputs exceed execution and masking capabilities

| Field | Review |
|---|---|
| Lens | 4 — Infeasibility; 6 — Security/user promises |
| Affected section | FR-069/071/076; US-6/US-7; BDD-7.3; D2 rows 2/8; tests 34/35; SC-002 |
| Failure scenario | A one-byte secret is accepted and printed exactly, but the named sensitive-value mechanism never masks it. A NUL-containing value is saved as valid and cannot be passed to a child command. A private variable collides with PATH, a proxy or setup-control variable and overrides execution/security configuration; Windows treats differently cased names as one key. |
| Evidence | FR-071 allows one byte; D2 accepts NUL; the spec supplies no variable-name/reserved-key/collision rule. `sensitiveReplacementPairs` and `buildAndPopulateSensitiveCache` skip values of three bytes or fewer. Go's `dedupEnvCase` rejects NUL and folds case on Windows. `setupEnv` owns proxy/prefix/cache variables; `composeExecutionEnv` preserves the scrubbed baseline (E12, E13). |
| Certainty | **Verified (high confidence):** redactor threshold, command-environment rules and unspecified input grammar. **Inferred (high confidence):** unmasked exact output or failed/misconfigured command. |
| Recommendation | Define variable grammar, protected names/prefixes, maximum byte sizes, NUL rejection for environment destinations and deterministic case/collision behaviour. Never broaden the inherited child allowlist or place private values in the gateway process environment. Inject only per authorized child execution, including foreground/background/setup paths. Either implement explicitly scoped short-value masking or reject unsupported values truthfully; test exact-value and transformed-output limits separately. Qualify US-6's universal “never enters model context” for the decided private-command exception, on the card and in user docs. OQ-1's narrower promise is confirmed, not pending. |

#### MAJ-011 — Per-workspace helper limits do not protect shared provider/connector writes

| Field | Review |
|---|---|
| Lens | 2 — Incompleteness/concurrency; 3 — Code consistency |
| Affected section | Section 2.2; FR-095; BDD-11.5; test 50; section 9.5 |
| Failure scenario | One helper for Sales and one for Marketing both plan changes to the same install-wide provider/connector. Both satisfy the one-helper-per-workspace rule. The second write executes a stale plan after the first and changes a shared endpoint/configuration without the intended conflict/re-confirmation. |
| Evidence | OSA-8 claims every resource write checks its revision. C9's actual cited revision backstop covers agents/skills/workspaces; `ProviderConfigureTool.Parameters` has no revision argument and Execute changes global provider entries. A configuration mutex serializes writes, but does not validate the stale plan's assumptions. The planned conflict scenario touches Jim only (E03, E14). |
| Certainty | **Verified (high confidence):** global provider write and missing precondition. **Inferred (high confidence):** logically stale shared setup under concurrent workflows. |
| Recommendation | Inventory each shared mutable setup resource and its existing revision/confirmed-assumption check. Add a narrow equivalent where needed or explicitly serialize those shared-resource operations with existing admission/locking mechanisms. On changed meaning, return a revised summary to the main for a new Go. Test two target workspaces competing over one provider/connector as well as a shared agent/skill. No workflow transaction engine or cross-resource atomicity is required. |

#### MAJ-012 — The picker explicitly includes agents that cannot join a team

| Field | Review |
|---|---|
| Lens | 7 — Reachability; 3 — ADR/code consistency; related 9 — Keyboard acceptance |
| Affected section | US-12 AS-3; FR-104; BDD-12.4; test 27; WU-10 |
| Failure scenario | Implementing “every agent except Omnipus” offers Judge and Plan Supervisor as addable choices. Selecting either is refused by the unchanged membership boundary. A happy-path orphan test passes while the user-facing picker offers guaranteed failing actions. Already-present members can also be offered unnecessarily if the literal all-agent rule replaces the existing candidate filter. |
| Evidence | FR-104 and US-12 AS-3 name only Omnipus as excluded. Current `AddAgentPicker.candidates` excludes system agents and existing members; `ExcludedFromWorkspaceTeams` bars Judge/Plan Supervisor. The feature expressly leaves hidden-engine capabilities unchanged (E01, E18). |
| Certainty | **Verified (high confidence):** incompatible candidate requirement and existing eligibility rules. **Inferred (high confidence):** guaranteed failing choices if implemented literally. |
| Recommendation | Specify all **team-eligible, not-already-member** agents, excluding Omnipus and hidden engine identities; mark legitimate orphans. Test actual candidate rendering and successful/refused membership actions through the real shell. Separately preserve the ADR's default-workspace graph controls for Omnipus's editable/removable self-row: FR-004 excludes the **list**, not the graph. Add a UI edit/delete/reload/refused-next-helper test rather than relying only on backend test 42. |

#### MAJ-013 — Secret endpoints lack a complete trust, audit and resource-bound contract

| Field | Review |
|---|---|
| Lens | 6 — Security; 2 — Incompleteness |
| Affected section | Section 6.2; K1–K6; FR-061–075/078; tests 30–33; missing Security and user promises section |
| Failure scenario | An implementer reuses an optional-auth setup route or treats a known request ID as sufficient authority. A stale/foreign/changed-owner request writes a value or resumes a stopped session. Many sessions can each hold one request, with no specified global pending/terminal bounds; a value-bearing validation error reaches logs. The happy-path card and storage tests do not prove rejection before writes/resume. |
| Evidence | The spec defines “foreign request → 404” but not what authenticated principal/session/acting-agent state makes it foreign, request creation/terminal audit obligations, global pending/terminal retention bounds or safe store-error behaviour. Existing authenticated credential routes and question-registry owner/global-cap/persist-before-park rules are explicit, not automatic properties of a new route (E01, E15, E16). |
| Certainty | **Verified (high confidence):** absent spec obligations and existing trust seams. **Inferred (medium confidence):** unsafe or unbounded implementation permitted by the omission; no existing endpoint exploit is asserted. |
| Recommendation | Add a Security and user promises table: authenticated web-only entry, server-owned session/agent/workspace/recipient, authorized consumer and live policy checks, request-ID non-authority, Stop/deletion/changed-destination handling, bounded pending/terminal metadata and sanitized value-free audit/errors. Reuse existing auth/admission/store primitives; do not add an independent authorization service. Require negative tests for unauthenticated, forged/foreign/stale IDs, changed owner/recipient, saturated registry, persistence failure and terminal races, proving zero unauthorized store writes/resume. **Password re-confirmation for the new scoped card is Q-R1-2 below; that undecided choice is not itself a blocking finding.** |

### MINOR findings

#### MIN-001 — Browser warm-up still proposes the rejected no-warm behaviour

| Field | Review |
|---|---|
| Lens | 3 — Founder-decision consistency |
| Affected section | FR-126; OQ-4; BDD-13.6/test 22 |
| Failure scenario | The implementation removes the default-agent lookup by warming nothing. The first default-workspace panel opens cold, although the founder chose to warm that workspace at boot. The registry test cannot distinguish the two choices. |
| Evidence / certainty | OQ-4 now selects **B**. Current `pickWarmBrowserManager` selects a workspace manager through the default agent, not an agent-private browser (E02, E17). **Verified (high confidence)** contradiction; first-open consequence **Inferred (high confidence)**. |
| Recommendation | State “warm the default workspace's browser”, with explicit no-manager/degraded behaviour and a selection test independent of any default agent. Do not warm every workspace or use lexicographic selection. |

#### MIN-002 — The promised catalogue mapping and design-system acceptance are missing

| Field | Review |
|---|---|
| Lens | 10 — Design-system reuse and brand |
| Affected section | FR-110; sections 9/10 J; WU-10 |
| Failure scenario | “Use catalogued components” is left to each implementer; a new local field/error/empty-state job duplicates kit behaviour or a new composite ships without executed keyboard/reflow/style evidence. No specific acceptance gate is assigned to catch it. |
| Evidence / certainty | OSA-10 promises the spec's mapping. The inspected catalogue contains Field/Input/Label/FormError/Button, ConfirmDialog, EmptyState/QueryErrorState/Skeleton, Tabs/ViewSwitch, AgentIcon and Popover/Sheet. The spec supplies no mapping or named design-system checks (E03, E19). **Verified (high confidence)** omission; duplication/gate cost **Inferred (medium confidence)**. |
| Recommendation | Add the card/list/state/navigation composition table using those existing entries and their public boundaries. Name the applicable design-system scanner/component/keyboard/reflow checks and static Storybook evidence if a reusable component is published. Keep the four-part publication rule and token/focus ownership; do not copy draft HTML colours/layout as production components. No brand/visual redesign is decided by this review. |

### Observations — non-gating

#### OBS-001 — Clarify buildable landing bundles and work-unit gate boundaries

| Field | Review |
|---|---|
| Lens | 12 — Overcomplexity; delivery/dependency order |
| Affected section | Sections 13/15; WU-0/WU-2/WU-4/WU-8/WU-9/WU-10/WU-11 |
| Scenario / evidence | K11/K12 generation removes fields consumed by existing Go/TS code; WU-0 is not an independently usable contract-only landing. WU-10 changes gateway kickoff/WebSocket behaviour but is omitted from the named engine-UAT unit list. WU-11's guards/removals are cross-tree/structural even though labelled Standard. FR-033 has two implementing units; the text needs one responsibility per artifact. **Verified** plan/source facts (E01, E20); independently broken landing **Inferred (medium confidence)** because the spec does not promise each unit lands separately. |
| Recommendation | Mark preparation commits versus independently landable bundles. Keep contract-first commits in the feature branch, then gate buildable/reachable bundles and the whole feature. Include every touched engine/WebSocket bundle, WU-10 included, in exact-commit tester + independent validator acceptance. Size structural/security bundles Feature under the root rules. Correct dependency arrows to distinguish UI development parallelism from live-workflow acceptance, and label security-lead as reviewer, never implementer. Keep same-change docs; WU-12 is a sweep, not permission to defer them. |

## Structural integrity

| Spec-mode check | Result | Notes |
|---|---|---|
| Valid Status field | Present | Draft. |
| Architectural ADR linked | Present | Corrected **The Omnipus system agent**, with dated FQ-17/FQ-18 precedence. Later OQ answers win. |
| Contract-first order | Direction present, incomplete shape | Section 7 precedes consumer detail; K8/validation/recovery gaps are MAJ-003/004. Existing-code impact inventory is not a licence to implement first. |
| API and data contract | Partial | Wire inventory and limits exist; lifecycle/trust/recovery gaps are MAJ-001/003/004/013. |
| Every changed screen's loading/empty/error/partial states | Incomplete | MAJ-008. |
| End-to-end user journeys | Present, with gaps | Stories cover W1/W18/secret/setup; recovery/operator graph controls need MAJ-003/008/012. |
| Accessibility and keyboard | Partial | Explicit for secret/Team actions and graph text equivalent; new navigation/state journeys need MAJ-008/012. |
| Catalogue-first component mapping | Incomplete | MIN-002. |
| Security and user promises | Incomplete | Safeguards/risk references exist, but endpoint trust/resource/audit contracts do not; MAJ-013. Narrower private promise must be consistent, MAJ-010. |
| Behaviour-driven acceptance | Present | 96 scenarios; contradictory/weak oracles are not coverage, MAJ-006/007. |
| Requirement → scenario → test | Present, semantically incomplete | 86 matrix rows; all declared scenarios present. Two scenarios unassigned in the explicit test-order table; several links test something else. |
| Reachability | Present, insufficient proof | Metadata grep and Omnipus-only policy seed are not ordinary-agent execution proof, MAJ-009. |
| User-facing documentation TODO map | Present | Specific page map exists in **section 14**, not section 13 as FR-130/SC-009 say. Correct that reference during the fix. Lifecycle/scope/recovery corrections must update the TODOs too. |

## Twelve-lens coverage

| Lens | Finding coverage |
|---|---|
| 1 Ambiguity | MAJ-005/007: caller replacements, missing-information versus confirmation cards; routing either/or oracle. |
| 2 Incompleteness | MAJ-001/005/011/013: lifecycle, caller ownership, shared writes and endpoint trust/bounds. |
| 3 Inconsistency / ADR / AS-IS | CRIT-001, MAJ-001/002/005/007/012, MIN-001. Historical AS-IS default-policy claims were checked against current code, not treated as current authority. |
| 4 Infeasibility | MAJ-007/010: unavailable information and unsupported child environment values/masking. |
| 5 Contract-first | MAJ-003/004: terminal recovery, WS agent requirement and real validator coverage. |
| 6 Security / user promises | CRIT-001, MAJ-009/010/013; accepted R-A1–R-A3 are not reopened. |
| 7 Reachability | MAJ-002/009/012: routing writes, actual ordinary-agent tool dispatch and valid picker candidates; graph-control acceptance also needs coverage. |
| 8 UI states / journey | MAJ-003/008: secret uncertainty and complete screen journeys. |
| 9 Accessibility / keyboard | MAJ-008/012: all changed navigation/controls and editable graph authority. |
| 10 Design-system / brand | MIN-002: missing explicit catalogue mapping and acceptance. No visual PASS/brand choice. |
| 11 Testability / false greens | MAJ-004/006 and coverage table below. |
| 12 Overcomplexity | MAJ-001 removes obsolete staging layers; OBS-001 preserves ordinary mechanisms/buildable delivery without new coordination systems. |

### UX heuristic check

**Several issues to address.** H1 (visible system state) concerns unknown Submit, stale Team and unknown attention; H3 (user control) concerns honest Cancel/Stop and self-row removal; H5 (error prevention) concerns scope/recipient binding and preserved picker exclusions; H7 (keyboard access) concerns new navigation/composites; H9/H10 concern retry paths and corrected user instructions.

| Priority action | Findings |
|---|---|
| Make saved secret ownership and server-confirmed state unambiguous. | CRIT-001, MAJ-001/003 |
| Preserve usable state/keyboard/recovery contracts across every changed screen. | MAJ-008/012 |
| Prove all-agent access and shared effects through the real shell and dispatch paths. | MAJ-009/011 |

## Contract completeness — K1–K17

This is a shape review, not permission for this reviewer to edit contracts. Backend-lead owns generation and consumers.

| Entry | Assessment / correction |
|---|---|
| K1 request frame | Named; require owning workspace/agent for private kind, trusted helper-root derivation, bounded display/field metadata and real parser dispatch. Card/request expiry is not saved-value expiry. |
| K2 recipient display | Named; include workspace + agent + variable for private kind and the full effective endpoint/account/executable facts needed to understand the frozen recipient. Display is not authorization. |
| K3 resolved frame | Named; terminal state alone is not replay/read recovery. Address MAJ-003. |
| K4 pending state/snapshot | Named; nil-safe bounded arrays, inline/shared mirrors and terminal-state convergence must be defined. Current full snapshot's replay protocol must remain authoritative. |
| K5 submit | Named; actual path/method/security/error states, values-versus-cancel exclusivity and same-request state recovery are incomplete. No plaintext automatic replay. |
| K6 name listing | Named; workspace/agent scope, pagination/bounds as applicable and actual list invocation/REST parser test are needed. Values never returned. |
| K7 helper approval fields | Named; preserve workspace/acting session and derive root on server. Carry both live and reconnect cases. Do not turn a reconnect Deny-only stub with unknown arguments into an invented approvable call. |
| K8 workflow metadata | Target-versus-anchor intent is explicit; WS nonblank agent selection required by FR-122 is not named. Add it. |
| K9 kickoff | Intentional server-initiated existing-main amendment is explicit; test old client-with-session refusal and actual empty-team producer together. |
| K10 pairing | Correlation fields named; subscription owner, cancel/stop/timeout/reconnect/teardown and safe requesting-chat presentation still need exact tests. |
| K11 Agent.default removal | Named; adapt all consumers in a buildable feature bundle, no generated-file hand edits. |
| K12 update default / Omnipus Ask | Named; raw/normalized/null/unknown-field handling and rejection must hold with validation off as well as on. |
| K13 tools-write Ask rule | Named; test direct REST plus agent-tool writes, editor suppression and persisted values. |
| K14 channel routing | Must require both workspace and agent under decided OQ-2; no legacy unbound path. GET must truthfully represent an existing invalid config, not invent a bound destination. |
| K15 degraded status | Named; map failure to the correct channel instance. Define recovery/reset after repair; status must not merely reflect token construction success. |
| K16 session create | Required agent named; test missing/null/blank/unknown/worker plus ordinary extra-chat and singleton Omnipus paths. |
| K17 old default setting | Ignore + warning is specified, consistent with greenfield. No agent re-homing or settings/permission migration is commissioned. Name remaining settings-schema/sanitizer consumers and load/reload evidence. |
| Across all entries | Generated Go/TS/Zod and inbound schema mirrors, exact five-step order, real parser/handler tests, nested-validator negative controls and drift gate are separate proof obligations. SC-010 alone does not certify runtime validation. |

## No-default-agent caller audit

The controlled source sweep finds the 16 production getter call sites described by the ADR. This table records **spec coverage**, not completed removal. Each absolute source is in E07; current getter/first-agent fallbacks still exist at the reviewed commit.

| Caller | Spec coverage / correction needed |
|---|---|
| resolveTargetAgent (WS) | FR-122/test 45; also delete first-chat-target fallback and define required wire field. |
| createSessionHTTP (REST) | FR-122/test 45; same fallback removal, plus singleton test missing from FR-005's link. |
| resolveMessageRoute | FR-121/tests 20/21/44; no secondary resolution; bound/unbound instance-specific loud drop. |
| resolveSystemMessageAgent | No explicit requirement/scenario for missing async origin; define visible refusal and preserved addressed-origin behaviour. |
| resolveGoalAgent | FR-123/test 23; test owner deletion with another valid AgentIDs participant, not only an otherwise empty fixture. |
| processTaskDirect | FR-123/test 23; preserve pre-dispatch failure and zero other executor/model starts. |
| agentForSession | FR-123/test 23; resolve canonical session owner or visible refusal, never any agent. |
| enqueueSteeringItemWithStatus | FR-123 names missing agent; test no invented sender identity and no rejected-item admission. |
| publishResponseIfNeeded | No explicit nil-agent error-publishing disposition; do not borrow another agent's send-message state or suppress the unroutable error. |
| initializeCore | FR-124; choose the channel-state manager's owner/path and test recording/reload, not just registry construction. |
| MCPWorkspacePath | FR-124; choose one stable install/config root for relative MCP environment files and test runtime/test path agreement. |
| extractProvider | FR-124; provider cleanup/upsert must use shared provider ownership without routing work to an agent. |
| GetStartupInfo | FR-124; assert real catalogue/startup content, including empty/partially loaded registry. |
| ListSkillsDetailed | FR-124; use authoritative skill source, not an arbitrary agent's grants/home; assert actual listing/validation content. |
| HandleTools | FR-125; real central-catalogue response. |
| HandleToolsRegistry fallback | FR-125; define unwired/preboot/test behaviour without an agent-proxy fallback. |

FR-120 also needs removal of the singleton seed/override/health/writer and tool/editor/default-field consumers, not only those 16 calls. The spec already specifies old config **ignored with a warning**; the correction should exercise it on load and reload without silently deleting saved chats or migrating permissions.

## Founder-decision trace review

Each requirement/test reference below is to the **current plan**, not a test execution. Grouped references show coverage without asserting that a weaker oracle proves the decision.

| Founder decision | Requirement → planned proof | Fidelity |
|---|---|---|
| D1 scope exclusions | FR-007; role test 3; System agents test 29 | Relocation is in scope; Judge/Planner redesign/core security settings remain out. |
| D2 capability planning/in-workspace creation | FR-040–046 → BDD-4.* → tests 13–15/57 | Present; up-front dependencies and ordinary execution still need proof. |
| D3 up-front dependency checks/self-service | FR-010/040 only indirect → prompt/target tests | **Missing explicit requirement/scenario/readiness oracle**, MAJ-006. |
| D4 ordinary peer request | FR-090 → BDD-10.1/10.4 → test 41 | Present; no delegation edge required. |
| D5 read-only question by ordinary message | FR-090 → test 41 | Add a read-only no-change control, not a separate path. |
| D6 summary + question / narrow Ask | FR-020–027 → BDD-3.* → tests 4–6/47/57/60/61 | Present; instruction-only risk stays accepted; wording is not code enforcement. |
| D7 God Mode | FR-015/023 → BDD-3.4 → tests 47/60 | Missing-information/secret exception contradicted, MAJ-007. |
| D8 one agent/workflow skills | FR-001/010/011 → tests 1/51/57–59 | Present; exact roster grants/skills must be executed, MAJ-009. |
| D9 built-in tool checks | FR-030–033 → BDD-9.* → tests 12/15/16/36 | Present; keep truthful partial/verification facts. |
| D10 secure field S1–S4 | FR-060–078 → BDD-6/7/15 → tests 7–11/25/30–35/48/49/55 | Workspace/lifecycle/list/trust gaps, CRIT-001 and MAJ-001/006/009/013. |
| D11 connect channel/mailbox | FR-080–082 → BDD-8.* → tests 36–38 | Present; platform-specific proof/correlation negative controls need assignment. |
| D12 no workflow record | Sections 5/19, handle lifecycle | Keep no workflow engine. OQ-3 supersedes saved-value staging expiry. |
| D13 matching/ambiguity/no match | FR-011–013 → BDD-2.* → tests 51/58 | BDD-2.2 either/or must distinguish nested workflow from actual ambiguity, MAJ-007. |
| D14 skill lifetime/reload | FR-011 → BDD-2.4 → test 58 | Present; no explicit unload introduced. |
| D15 fixed entry/one conversation | FR-005/100 → tests 45/52 | Entry present; singleton test link wrong, MAJ-006. |
| D16 attention | FR-091/101 → BDD-10.2/10.3 → test 40 | Present; unknown state and all helper decision forms need coverage. |
| D17 Add-agent entry / Settings forms stay | FR-045/108 → tests 52/53 | Present; no-model Settings recovery must run in real shell. |
| D18 only Omnipus UI creation | FR-108 → BDD-12.8 → test 53 | Present; kept REST API is the explicit FQ-10 exception. |
| D19 Agents removal/no forwarding | FR-108 → BDD-12.7 → test 53 | Present; no retired route resurrection recommended. |
| D20 Team list/cards/actions/workers | FR-102–105 → BDD-12.3/12.5/12.10 → tests 26/54 | State/keyboard gaps, MAJ-008. |
| D21 legitimate orphans in picker | FR-104 → BDD-12.4 → test 27 | Preserve hidden-engine/member exclusions, MAJ-012. |
| D22 Ava removal/greenfield/welcome | FR-008/047/048 → tests 1/43/guards | Present; no Ava data migration requested. |
| D23 Admin replacement | FR-008/100/108 → tests 1/52/53 | Present; preserve only specified setup filesystem authority. |
| D24 offered/skippable team setup | FR-047/052 → BDD-5.1/5.2/5.7 → tests 43/54/59 | Present; do not mistake failed team fetch for empty. |
| D25 tailored team/graph/no templates | FR-010/049–051 → BDD-5.5/5.6 → test 59 | Present; text graph equivalent retained. |
| D26 reuse identities | FR-049 → BDD-5.3/5.4 → test 59 | Unchanged reuse + separate shared-impact proposal retained. |
| D27 helpers/main availability/no overwrites | FR-091–095 → BDD-11.* → tests 24/39/40/42/50/56/59 | Shared-resource conflict and actual self-row/decision UI proof incomplete, MAJ-011/012. |
| FQ-1 | FR-003/004/094 → tests 19/26–28/42 | Hidden default-workspace member; preserve graph self-row controls and add UI acceptance. |
| FQ-2/3/6/16 | FR-020–027 → tests 4–6/17/18/47/57/61 | Both confirmations and write-time Omnipus Ask refusal retained; compositor unchanged. |
| FQ-4/11/12 | Accepted R-A1/R-A2/R-A3; user-doc TODOs | Do not reopen accepted injection/disclosure/external-program risks. Documentation proof only where that is the accepted mitigation. |
| FQ-5 | FR-023 / D5 matrix | Implied by FQ-16, as the authority records; not a separately observed founder answer. |
| Later FQ-7/18 and OQ-1 | FR-076 → tests 34/35 | Private kind in scope, narrower wording confirmed, **workspace + agent required**. |
| FQ-8/15 | FR-078/091–093 → tests 33/39/40/56/59 | Root decision surface explicit; preserve ordinary relay/no helper-only card. |
| FQ-9/10 | FR-106/108 → tests 29/53 | System agents editor relocation; REST create stays documented. |
| FQ-13 | FR-077 → tests 48/49 | In-use removal waits for #1277; no claim that issue is fixed here. |
| FQ-14 | FR-049 → test 59 | Separate shared-global change retained. |
| FQ-17 and OQ-2/OQ-4 | FR-120–127 → tests 20–23/44–46/53 | No default anywhere; unbound named-agent case and warm-up must use later answers. |
| OQ-3 | FR-063–067 currently contradict it | Immediate durable Submit; no saved-value staging expiry, MAJ-001. |

## Every inherited ADR-grill finding

Authority: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261011-omnipus-system-agent-review.md` and the corrected **The Omnipus system agent**::Grill findings → disposition. The first two are founder-dispositioned risks, not blockers re-issued by this review.

| ADR-grill finding | Current spec requirement/proof | Round-1 assessment |
|---|---|---|
| CRIT-001 read-to-web disclosure | Accepted R-A2; Security TODO; section 9.5 | Accepted risk carried; docs-verifier must audit the actual disclosure text. |
| CRIT-002 external executable planting | Accepted R-A3; Agents/Security TODOs | Accepted risk carried; no silent mitigation redesign. |
| MAJ-001 code Go-check infeasibility | FR-014/026; tests 51/57/60 | Founder chose prompt-only. Keep human-card/forged-Go/default-safe controls; prompt text is not enforcement. |
| MAJ-002 staged lifecycle | FR-063–069; tests 7–10/30 | Was carried, now superseded by OQ-3. Preserve atomic durable save and single-use safety, remove value staging. |
| MAJ-003 effective recipient | FR-065; D1/test 10 | Carried, but workspace private scope and complete frozen descriptor/security rules need correction. |
| MAJ-004 configured provider endpoint | FR-031; test 12; SC-008 | Specific custom-endpoint/keyless/protocol controls carried. |
| MAJ-005 target versus anchor | FR-040–043/047; tests 13/14/43 | Carried; other target-scoped verbs and environment readiness still need explicit acceptance ownership. |
| MAJ-006 shared-agent reuse | FR-049/095; tests 50/59 | Reuse unchanged carried; shared provider/connector concurrency incomplete. |
| MAJ-007 hidden approval | FR-091/092; tests 24/39/40/56 | Root fields/scope adaptation carried; preserve snapshot/stub/terminal behaviour and test actual action. |
| MAJ-008 channel/QR proof | FR-080–082; tests 36–38 | Carried at headline level; per-platform scope, wrong binding and subscription recovery not fully assigned. |
| MAJ-009 dangerous removal | FR-077; tests 48/49; #1277 dependency | Carried; issue prerequisite is not implementation evidence. |
| MAJ-010 partial saves | FR-030/033; test 15; section 9.5 | Outcome vocabulary carried; crash/persistence cut and durable-resource readback controls need explicit assignments. |
| MAJ-011 card recovery states | FR-074/075; tests 25/55 | States carried; terminal read/replay contract missing, MAJ-003. |
| MAJ-012 keyboard/focus | FR-050/109; tests 25/26/55/59 | Secret/Team/graph text covered; whole navigation/state contract incomplete. |
| MAJ-013 documentation map | FR-130; section 14 | Concrete page TODO map carried; adjust it with new decisions/corrections, not a name-only sweep. |
| MAJ-014 operator Ask/God Mode | FR-022/023; tests 6/47/60 | Write-time refusal carried; runtime mode is not a new resolver layer. |
| MIN-001 conflict literals | BDD-11.5 uses CONFLICT | Agent literal corrected; workflows must still distinguish workspace REVISION_CONFLICT. |
| OBS-001 kit reuse | FR-110 | Mapping promised but not supplied, MIN-002. |
| OBS-002 proof table | Sections 9.4/9.5 | Prose inherited; not every negative control is assigned a falsifiable test, MAJ-006. |
| OBS-003 main-only Ask handbacks | FR-092/093; helper tests | Founder authorized helper execution; no need for a new main/helper execution coordinator. |

## Test coverage assessment — planned, not executed

| Category | Remaining acceptance obligation |
|---|---|
| Levels | Exercise real ordinary/custom dispatch, live REST/WS parsers and actual sidebar/Team/System agents shell. Direct tool tests, registry text and mocked transports alone cannot prove reachability. |
| Negative cases | Unauthenticated/foreign/stale/saturated secret request; named-agent unbound channel; second explicit Omnipus chat; denied ordinary grants; unknown/worker/non-member targets; provider refused URL/safe error; QR wrong subscriber/teardown. |
| Boundaries | 1/3/4-byte masking; UTF-8 bytes versus characters; NUL; invalid/reserved/case-colliding environment names; empty/null/extra/duplicate fields; exact/over-limit body; computed main IDs and scoped identifiers. |
| Concurrency | Save versus Cancel/Stop; duplicate Submit; one-use concurrent consumers; same Mia across workspaces; stale snapshots; two helpers sharing a provider/connector; main interview B while helper A waits for a decision. |
| Recovery/crash | Lose HTTP response and resolved frame; new tab/reload/gateway restart; failed store save before park/resume; durable credential save before config failure; stale live integration after removal; actual next boot validity with #1277. |
| Frontend | First load, genuine empty, partial/cached stale data, refresh failure, mutation uncertainty, model recovery, attach/send/retry block, keyboard/touch/reflow/focus/announcements and graph text equivalence. |
| False-green controls | Nonempty captures and known-leak control; decoded provider request inspection; actual tool dispatch counts; independent expected grant/recipient fixtures; source scans only for retirement/wording, not behaviour. |
| Regression | Ordinary question relay, approval queue/expiry/reconnect Deny-only stub/resolution; global ceiling reconciliation preserving operator values; project-shelf authority; fixed hidden-engine capabilities; main immutable owner and source-bound replies; existing Stop and no-UI-watchdog rules. |
| Acceptance authority | CI for Go/build/race/platform and contract gates; named TypeScript/design-system gates; exact-commit hands-on UAT tester plus independent validator for every engine/WebSocket bundle. No red/green product-test receipt was supplied or verified by this review. |

No automatic test runs were requested or performed. The controlled review inventory is document analysis, not product testing. An initial range parser was discarded because it could not expand full ranges correctly; the final cell-based scanner verifies full and abbreviated ranges and a deliberately missing control before reporting zero matrix omissions.

## STRIDE threat summary

STRIDE groups identity spoofing, state tampering, missing action attribution, information disclosure, resource exhaustion and privilege escalation. “Gap” means a design obligation, not a penetration-test result.

| Flow | Spoofing | Tampering | Attribution | Disclosure | Resource exhaustion | Privilege | Disposition |
|---|---|---|---|---|---|---|---|
| Private save/injection | Trusted pair needed | Owner/recipient comparison | Value-free audit needed | **Workspace boundary missing** | Bounded values/metadata needed | No child-env/security override | CRIT-001, MAJ-010/013 |
| Submit/state recovery | Auth/foreign request rule incomplete | Terminal races/replay incomplete | Request transition audit unspecified | No raw value in resume/error | Global pending/terminal bounds unspecified | No request-ID-only authority | MAJ-003/013 |
| Integration apply | Existing tool authority retained | Shared stale-plan gap | Preserve actual saved/live facts | Effective recipient + safe upstream error | Existing timeout/limits to preserve | Accepted R-A1–R-A3 unchanged | MAJ-011; ask-surface security pass still needed |
| Helpers/main display | Validated acting/root identity | Preserve terminal ordering | Keep acting helper visible | Do not erase workspace/audience scope | Ordinary depth/admission preserved | Seeing a card grants no new caller authority | MAJ-008/012; existing relay retained |
| Channels | Required pair incomplete | No legacy fallback | Instance log/audit specified | No cross-pair re-homing | Existing channel limits preserved | No fallback into Omnipus management | MAJ-002/005 |
| Secret removal | Existing Ask and consumers retained | Durable/live/boot facts separate | Preserve actual deletion record | Names only | #1277 boot immunity prerequisite | No raw-vault bypass commissioned | Current dependency accepted; new scoped-submit consent is Q-R1-2 |

## Reachability check

| Capability | Result of this design review |
|---|---|
| Six new tools | Not implemented at baseline; this is expected, not itself a finding. Static catalogue search is tested with present AskUserQuestion control. The plan must name metadata **and executable** registration, ceiling/role/custom grants and real dispatch proof. |
| Secret card for ordinary native agents | Intended but incompletely granted/tested; MAJ-009. Hidden engine/external workers must not gain broader capability as a shortcut. |
| Omnipus fixed entry | Screen/attach path named; unknown/main/attention/loading/retry states and singleton proof incomplete. |
| All helper decisions | Root approval/secret and parent-relayed question intent present. Live/snapshot/terminal/cancel/Stop behaviour must be proved in the foreground main from another workspace, without changing ordinary approval scope. |
| Team/picker/System agents | Homes named; complete states/keyboard and valid picker candidates need correction. The existing default graph self-row controls also need explicit UI acceptance. |
| No default agent | Broad removal intent present; all caller replacements, legacy unbound refusal and boot/catalogue behaviour need behavioural oracles. |

**Code correct and tested:** not claimed — specification/source review only.

**Reachable by a user or agent:** not claimed — future implementation and acceptance remain pending.

## Unasked questions for the author — technical corrections, not founder interviews

| Topic | Required correction |
|---|---|
| Caller/data ownership | State each no-default replacement and its explicit expected result; no silent “some agent” substitution for execution. |
| Secret lifecycle | Rewrite around durable Submit, metadata-only terminal retention and authorized workspace/agent scope; do not preserve old staging machinery by renaming it. |
| Contracts/validators | Name real endpoints/mirrors/dispatch paths and malformed positive-control pairs, including validation-off semantics. |
| UI | Supply the screen/state/component/keyboard tables and the default graph's Omnipus self-row controls. |
| Tests | Replace semantically wrong matrix links; assign every inherited negative control; prove the instrument sees populated real traffic. |
| Shared resources | Name revision/precondition or serialization for each global setup resource; no workflow transaction system. |

## Existing OQ recommendations — closed dispositions

| Existing question | Review position after the founder's answer |
|---|---|
| OQ-1 | Agree with the confirmed narrower promise, now explicitly **per workspace + agent** and on the card. It is no longer pending. |
| OQ-2 | Agree with B; remove legacy unbound routing, including an explicitly named agent. |
| OQ-3 | **Disagree with the spec's 30-minute staging recommendation.** It is superseded: save on Submit, no plan confirmation or saved-value staging expiry. |
| OQ-4 | **Disagree with the spec's “warm nothing” recommendation.** It is superseded by B: warm the default workspace browser. |

Do not interview OQ-1..OQ-4 again. Their answers are correction inputs, not new questions.

## Questions for the founder — NEW only

These are distinct live-effect/consent choices, not repeats of storage timing or workspace ownership. Recommendations are not decisions. Team-lead should interview before the fix; answer, for example, `Q-R1-1 A, Q-R1-2 B`.

### Q-R1-1 — When does replacing an already-used secret take live effect?

**Context:** Saving a submitted secret immediately is decided. What remains unstated is whether replacing a provider/channel/mailbox key overwrites its currently referenced value on Submit, or saves a new durable version/reference that the approved configuration change later selects. Existing consumers can already reference the old key; storing and activating are different effects.

**Impact:** Determines whether cancelling a setup plan can nevertheless change a working integration's credential, and what the secret card must disclose before Submit.

| Option | Choice |
|---|---|
| A | Save the new value durably on Submit under a new reference/version. Keep the current integration on its old reference until its approved change tool selects the new one. A cancelled plan leaves the new unused secret listable/removable. This is durable storage, not staging or expiry. |
| B | Submit replaces the currently referenced value and requests live application immediately. The card must show the affected consumers and explicitly say that this change happens even if the later setup plan is cancelled. |

**Recommendation: A.** It preserves immediate storage and avoids changing a working integration before its separate approval. Use the existing encrypted store and reference mechanism; do not introduce a second vault or workflow transaction.

### Q-R1-2 — Does submitting the new secret card require re-typing the password?

**Context:** The existing raw credential-vault write/delete routes require password re-confirmation. The proposed chat card is a narrower write to the exact server-bound destination the user sees. The spec does not state whether that new Submit requires the same password step or treats the authenticated card submission itself as consent. Neither the instruction-only Go gate nor the decision to save immediately answers this.

**Impact:** Determines the number of steps for every secret entry and whether this new route is explicitly narrower than the raw vault, rather than silently bypassing its protection.

| Option | Choice |
|---|---|
| A | Require the existing single-use password re-confirmation token before secret-card Submit saves any value; show the existing password dialog and its cancellation/error states. |
| B | Treat authenticated Submit of the exact destination-bound card as consent for that scoped write only. Preserve password re-confirmation on the existing raw vault routes; no arbitrary-key write or delete is exposed through the card. |

**Recommendation: B**, provided security-lead verifies the authenticated request/session/recipient binding and the exception is recorded explicitly. It avoids an extra password step after the user already entered and confirmed the secret, without weakening raw vault mutation protection.

## Verdict rationale and next action

**BLOCK** follows from CRIT-001: the literal private-secret storage boundary can disclose another workspace's credential. The thirteen major findings require correction of decided behaviour, enforceable wire/state/trust contracts, usable screens and falsifiable acceptance. The review does not claim the proposed implementation is complete or that existing accepted risks are newly resolved.

| Next action | Owner |
|---|---|
| Interview only Q-R1-1/Q-R1-2; use the already-decided OQ-1..OQ-4 answers without re-asking. | team-lead / founder |
| Fix round-1 findings in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec.md`; synchronize necessary dated decision notes with the authorized ADR owner, not this reviewer. | spec author / architect author as authorized |
| Review the complete ask/secret trust surface without re-deciding R-A1–R-A3. | security-lead |
| Run the fixed second grill on the corrected spec, irrespective of this verdict. | team-lead / independent reviewer |

This is grill round **1 of 2 (fixed)**. Next: team-lead interviews the founder on **Questions for the founder**, then the spec author fixes round-1 findings, then grill-spec runs **SPEC MODE ROUND 2** on `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec.md` — regardless of this round's verdict. No third grill round is permitted. This report does not correct or re-grill the ADR.

skills: omnipus-shared-rules, grill-spec, gitnexus-exploring, ux-heuristics-review, omnipus-design-system, jev-use:jev-use, commit-messages

## Evidence table — grill-osa-spec-r1

| Claim | Evidence | Certainty |
|---|---|---|
| E01 — Complete commissioned spec/source baseline | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec.md`::sections 1–20, FR-001–130, BDD-1.1–15.4, K1–K17, test/trace/work-unit tables; complete first-hand read. Input SHA-256 above. `git log -3 --format='%H %s'`, exit 0 → HEAD `5ac3ddb64d8c5b7cc2165d4e1d3f9c981d30a2d6`; resumed worktree/staged status, exits 0 → empty. | Verified, high confidence |
| E02 — New decisions override conflicting spec text | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-adr-founder-answers-20261012.md`::After the correction round / Spec open questions OQ-1..OQ-4; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-system-agent-decisions-20261012.md`::Decisions Log D1–D27. | Verified, high confidence |
| E03 — Corrected design and every inherited grill disposition read | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md`::OSA-1–13 / Founder answers after correction / Grill findings → disposition; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261011-omnipus-system-agent-review.md`::CRIT-001–OBS-003 / Test coverage assessment. | Verified, high confidence |
| E04 — Legacy unbound routing exists in backend, UI and schema | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_channels.go`::setChannelRouting, validateUnbound, persistUnbound, getChannelRouting; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/skills/ChannelConfigPanel.tsx`::filteredAgents / doSaveRouting; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/ChannelRouting.yaml`. | Verified source, high confidence |
| E05 — Current transport shapes do not provide secret terminal recovery or required WS agent | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/asyncapi.yaml`::MessageFrame / WhatsAppPairingFrame / WhatsAppPairingSubscribeFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/SessionStateFrame.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/SessionSnapshotFrame.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/SessionCreateRequest.yaml`. | Verified source, high confidence |
| E06 — Runtime validation and schema generation are separate from type compilation | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_inbound_validate.go`::decodeAndValidate, ValidateInboundFrameJSON; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/websocket.go`::readLoop validation branch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/lib/ws.ts`::parseFrameSafe; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/scripts/gen-contracts.sh`::Step 5 inbound mirror. | Verified source, high confidence |
| E07 — Sixteen default getter callers and secondary fallbacks inspected | Controlled production search over `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg` found the named seams; a non-comment `.GetDefaultAgent()` Python inventory, exit 0 → `non_comment_production_getter_calls 16`; source reads: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/websocket_chat.go`::resolveTargetAgent; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_sessions.go`::createSessionHTTP, firstChatTargetAgentID; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop_inbound.go`::resolveMessageRoute, resolveSystemMessageAgent; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/goal_triggers.go`::resolveGoalAgent; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/task_executor.go`::processTaskDirect; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/steering.go`::agentForSession, enqueueSteeringItemWithStatus; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop_publish.go`::publishResponseIfNeeded; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop_construct.go`::initializeCore; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop_mcp.go`::MCPWorkspacePath; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop.go`::extractProvider; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop_accessors.go`::GetStartupInfo, ListSkillsDetailed; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_tools.go`::HandleTools; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_tool_registry.go`::HandleToolsRegistry. | Verified source, high confidence; future removal impact Inferred, no graph run |
| E08 — Architecture docs are dated, not substituted for current source | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/AS-IS-architecture.md`::Agent Loop / Memory & Session / Tools System / Contract-first pipeline; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/plugin-extensibility-assessment.md`::Tools / Skills / MCP. Relevant ADR titles inventoried; current source wins. | Verified reads, high confidence |
| E09 — Source scans/empty captures can produce false greens | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/false-green-patterns.md`::Checklist / substring scan / generated validator / coverage traps; input spec::FR-005/006/060/073 mappings and SC-002. | Verified plan gaps, high confidence; future false PASS Inferred |
| E10 — Existing user expectations distinguish unavailable/empty/stale state | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/using-omnipus-ui.md`::The sidebar / chat; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/workspaces.md`::How to set who may delegate / main chat; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/agents.md`::creation/default/editor; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/security.md`::God Mode / secret filtering; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/tools.md`::How to change access / policy merge. | Verified reads, high confidence |
| E11 — Metadata does not grant/execute ordinary-agent tools | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent/role_policies_adr090.go`::ADR090RolePolicyInventory, adr090SparseRolePolicies; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent/seed.go`::allStaticToolNames, denyAllThenOverride, NewCustomAgentToolsCfg; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/general_builtin_catalog.go`::GeneralBuiltinMetadata; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/central_builtin_registry.go`::buildCentralBuiltinRegistry. | Verified source, high confidence |
| E12 — Child environment is per turn and preserves controlled proxy/setup inputs | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/shell_env.go`::composeExecutionEnv, setEnvValue; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/environment_setup_runner.go`::setupEnv, startSession; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sandbox/hardened_exec.go`::allowedChildEnvKeys, ScrubGatewayEnv. | Verified source, high confidence |
| E13 — Short values are not replaced; NUL/case rules are real | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/config/security.go`::RegisterSensitiveValues, sensitiveReplacementPairs, buildAndPopulateSensitiveCache; `/usr/local/Cellar/go/1.27.1/libexec/src/os/exec/exec.go`::dedupEnv, dedupEnvCase. GOROOT lookup exit 0 → `/usr/local/Cellar/go/1.27.1/libexec`; implementation read, not a runtime test. | Verified source, high confidence |
| E14 — Provider write lacks revision precondition | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/provider.go`::ProviderConfigureTool.Parameters, Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest.go`::restAPI.configMu. | Verified source, high confidence; stale-plan consequence Inferred |
| E15 — Question registry's bounds/relay/persist rules are explicit | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/ask_user_question.go`::AskUserQuestionTool.Description, Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/askuser/registry.go`::Registry.CreatePending, relaySteeredQuestions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261004-steering-commands-no-person-question.md`::C2. | Verified source, high confidence |
| E16 — Raw vault consent is not implicitly new-card consent | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_settings.go`::HandleCredentials, setCredential; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest.go`::registerSettingsAndAccountRoutes; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-022-credential-vault-reauth.md`::Decision. Q-R1-1/Q-R1-2 answers not present in supplied sources. | Verified existing rule, high confidence; new product choice Unknown |
| E17 — Browser selection names workspace manager | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/gateway_boot_browser.go`::pickWarmBrowserManager; authoritative OQ-4. | Verified source/decision, high confidence |
| E18 — Team graph controls/eligibility and current picker exclusions read | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/workspaces/WorkspaceTeamTab.tsx`::graph, handleSetDepth, handleDeleteEdge, loading/error/render branches; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/workspaces/team/teamGraphModel.ts`::buildTeamGraphModel; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/workspaces/team/AddAgentPicker.tsx`::candidates; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent/team_eligibility.go`::ExcludedFromWorkspaceTeams. | Verified source, high confidence; hidden-control failure Inferred |
| E19 — Catalogue and brand constraints checked, not visually certified | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/design-system/catalog.json`::entries (controlled parser exit 0 → 62 entries, named exports printed); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/design/design-system-definition.md`::Operating model / D1–D2; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/brand/brand-guidelines.md`::Brand foundation / colour / voice; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/ui/CLAUDE.md`; loaded design-system skill::gate/publication rules. | Verified reads, high confidence; rendered quality Unknown |
| E20 — Delivery/review/UAT authority and accepted session boundaries | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/CLAUDE.md`::Hard Constraints #6/#8 / Definition of Done / Change sizes / Retired surfaces / human authorship; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::D1/D4/D6/D7/D11; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-077-two-layer-tool-policy-remove-fail-closed-backfill.md`::D1/D3–D5; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/compositor.go`::resolveEffectivePolicyWith. | Verified reads, high confidence |
| E21 — Wire schema references and union shape checked | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/openapi.yaml`::getChannelRouting, setChannelRouting, mailbox routes; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/ChannelEntry.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/ToolApprovalRequiredFrame.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/SessionStatePendingApproval.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-034-agent-create-discriminated-union.md`::Decision. | Verified reads, high confidence |
| E22 — Helper decision scope/reconnect mechanisms need preservation | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/ws_tool_approval.go`::approvalWorkspaceID, broadcastToolApprovalRequired, broadcastToolApprovalResolved, sessionStateBytes; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/store/toolApproval.ts`::isApprovalInScope, reconcileWithSessionState. | Verified source, high confidence |
| E23 — Workflow draft examined only as background | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/omnipus-workflows/index.html`::shared skeleton / W1/W18 / dependency flow / routing / screens / superseded trade-offs; full source read. No browser/server/render run. | Verified source, high confidence; visual appearance Unknown |
| E24 — Inventory instrument can see omissions and ranges | Cell-based Python static scan, exit 0 → `parser_controls=4/4`; `bdd_declared 96 trace_presence_missing []`; `fr_declared 86 matrix_rows 86 fr_missing []`; `planned_test_rows 61 bdd_unassigned_in_test_table ['1.4', '1.5']`; `named_D3_source_in_matrix False`. Controls cover full/abbreviated ranges, known BDD and deliberately absent `99.9`. Earlier incomplete parsers were rejected. | Verified static analysis, high confidence; not product test evidence |
| E25 — New-tool absence search has positive control | `rg -l 'AskUserQuestion'` over `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/config` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools`, exit 0 → seed/default/catalogue/executable matches; same-tree proposed six-tool exact-name search, exit 1 → no matches. Expected at design baseline, not proof of a current implementation defect. | Verified search, high confidence |
| E26 — Graph output rejected; Jev low-confidence severity not adopted | `gitnexus status`, exit 0 → `Repository not indexed`; branch-scoped `gitnexus detect-changes --scope all --branch work/omnipus-agent-design-20261012`, exit 1 → commissioned branch not indexed, stale sibling warning. `jev-use judge` batch, exit 3 → two severity answers `escalate: true`, two new-question classifications not escalated. Reviewer resolved security-scope severity from the read boundary/failure scenario. | Verified instrument limits, high confidence |
| E27 — Commit scope/identity rules checked before writing | `git config user.email`, exit 0 → `10800669+daniel-piatkowski-ai@users.noreply.github.com`; `git config user.name` → `Daniel Piatkowski`; resumed status/staged diff empty. Hook inventory has sample hooks only. No co-author trailer, push, stash, server, build or product test is authorized. | Verified preflight, high confidence; final commit receipt supplied in dispatch reply |
| **Self-check** | Re-read the complete staged report diff at `/Users/danielpiatkowski/.claude/projects/-Users-danielpiatkowski-AI-Agent-Workspace-omnipus-wt-omnipus-agent-design/bab15162-804b-4d21-ae1a-0a56720c2024/tool-results/be74ea3hd.txt` and the subsequent correction diff. Static report check, exit 0 → `report_self_check=PASS; counts=1/13/2/1; lenses=12; contracts=17; new_questions=2; inputs_unchanged=true; human_identity=true`. Checked every finding's scenario/evidence/severity/recommendation/certainty, inherited ADR findings, closed OQ answers and final evidence row. `git diff --cached --check`, exit 0; staged scope is this report only. No implementation/acceptance success claimed. Final commit receipt is supplied separately in the dispatch reply. | Verified report self-check, high confidence |
