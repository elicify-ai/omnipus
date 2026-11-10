# Adversarial Review: The Omnipus system agent specification — round 2

**Verdict: REVISE — 0 CRITICAL, 7 MAJOR, 1 MINOR and 1 OBSERVATION.** The revised specification carries the decided workspace boundary, durable Submit, versioned replacement, mandatory channel binding, browser warm-up and scoped no-password exception. It is not ready for implementation planning: private-secret selection, crash recovery, shared-write synchronization, environment promises, masking persistence and independently landable bundles still need correction.

| Review header | Value |
|---|---|
| Document reviewed | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec.md` |
| Mode / round | **SPEC MODE — round 2 of 2, final; no round 3** |
| Reviewer | Independent architect; not the spec author |
| Date / lane | 2026-10-13 / `grill-osa-spec-r2` |
| Branch / reviewed commit | `work/omnipus-agent-design-20261012` / `3f195b0a33a5ea7647a84af985f0c5836adf542f` |
| Spec SHA-256 | `4007d5a9d84409a80a9e62667df126d5a6000d25234196faf0a01525ae6c4691` |
| Architectural authority | **The Omnipus system agent: one technical partner, workflows as skills, a narrow ask list and a secure secret field**, including both dated founder-answer amendments |
| Later authority | Complete founder-answer record, including OQ-1–OQ-4, Q-R1-1 A and Q-R1-2 B; later answers override older ADR/draft text |
| Boundary | Only this report is written and committed. No spec, ADR, contract, production or test edit; no build, product test, server, stash or push |
| Method | The Skill invocation returned candidates for this same round. This report independently checked their source mechanisms, completed all twelve lenses and the inheritance/decision audits, and did not start another grill |
| Instrument limitation | GitNexus MCP is not exposed; CLI status reports this checkout unindexed and branch-scoped detection rejects a stale sibling index. Source reads, controlled searches and Git diff are the fallback. No graph-derived impact or runtime acceptance is claimed |

## Executive summary

Frontend received the same scrutiny as backend: generated contracts, real frame consumers, attach/navigation, helper cards, recovery states, keyboard journeys, component reuse and documentation were checked. The round-1 screen, picker and keyboard corrections are present as plans; the remaining secret-state defects also affect what those screens can truthfully show. No rendered accessibility or visual PASS is claimed.

The current document contains **98 functional requirements, 129 behaviour-driven scenarios and 83 planned test rows**, not the original 61. All declared requirements and scenarios occur in the traceability matrix, and every scenario is assigned in the test-order table. This establishes inventory presence, not meaningful coverage or executed tests.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 7 |
| MINOR | 1 |
| OBSERVATION | 1 |
| **Total** | **9** |

**Certainty:** omissions, contradictions and cited source mechanisms are **Verified (high confidence)**. Future failure scenarios are **Inferred**, with confidence stated per finding; none is an executed exploit. Evidence IDs resolve to the absolute source citations in the final table.

**Decisions not reopened:** OQ-1–OQ-4, Q-R1-1 A, Q-R1-2 B, the instruction-only Go gate, accepted risks R-A1–R-A3 and OQ-5. There are **no new founder interview questions** in this report.

## Findings

### CRITICAL findings

None. R2-MAJ-006 is a mandatory sequencing correction: the remaining permission boundary must exist before the first landable bundle, not a risk accepted for interim delivery. None of the accepted R-A1–R-A3 risks is reissued as a new critical finding.

### MAJOR findings

#### R2-MAJ-001 — Stored and selected private-secret versions have no usable selection contract

| Field | Review |
|---|---|
| Lens | 1 Ambiguity; 3 Inconsistency; 7 Reachability; 8 UI states and journey |
| Affected section | FR-064, FR-067, FR-072, FR-076; §6.5/§6.7; BDD-6.5, BDD-7.8; tests 65/70/73 |
| Failure scenario | Mia@Sales has private version `.v1`, then submits `.v2`. Injecting the newest saved version activates it without the separate selection FR-064 requires. Keeping `.v1` selected leaves no specified permitted path for Mia to select `.v2`: her management tools are denied. Separately, after an integration has selected `.v2`, Stop on a later step cannot make that reference unused without contradicting the retained partial-application design. |
| Evidence | FR-064 permits selection only through an approved change tool; §6.7 repeats that rule. §6.5 gives ordinary agents `request_secret` and `list_secrets`, but denies the management family and defines no private selection/active-reference consumer. BDD-7.8 nevertheless promises that Mia's next command sees the value. FR-067 and BDD-6.5 unconditionally say Stop after Submit leaves it **unused**, without a not-yet-selected precondition. The founder decided new-version storage and delayed integration selection; the ADR's OSA-4/OSA-6 retains earlier completed effects after interruption (E01–E03). |
| Certainty | **Verified (high confidence):** incompatible/omitted selection and termination rules. **Inferred (high confidence):** premature activation, unusable replacement or false unused status under a literal implementation. |
| Recommendation | Distinguish stored, selected and actually used references. Name the owner-scoped active private reference and the permitted consuming path that selects a particular version, without granting ordinary agents management authority or adding a policy resolver exception. Qualify Cancel/Stop: **a not-yet-selected reference remains unused; selected references and completed changes remain in place and are reported truthfully**. Add first-use/private replacement, cancelled replacement, concurrent selection and Stop-after-selection scenarios. Preserve Q-R1-1 A; do not replace durable storage with staging or rollback. |

#### R2-MAJ-002 — Durable Submit and recovered request state have no common commit oracle

| Field | Review |
|---|---|
| Lens | 2 Incompleteness; 5 Contract-first gaps; 8 UI states and journey; 11 Testability |
| Affected section | §5; FR-063, FR-066, FR-074, FR-079, FR-084; K4/K5; tests 9/30/55/70 |
| Failure scenario | The secret bundle is durably published, then the gateway exits before submitted-request metadata is durable. On restart the pending record is marked interrupted and the card implies that nothing was stored, although the secret exists. A retry can create a second version or orphan the first; GET/snapshot cannot reliably determine which references committed. |
| Evidence | The spec promises atomic multi-field storage and readable `submitted` state after restart, but does not define their shared commit point or a recoverable request-to-reference receipt. BDD-9.6 tests selection versus configuration failure, **not** Submit publication versus request-state persistence. The existing credential store publishes its file separately from the question registry's session metadata/resume persistence; `persistTerminal` can fail after the in-memory question is consumed (E01, E04). Reusing park/resume does not make these separate publications atomic. |
| Certainty | **Verified (high confidence):** missing recovery invariant and distinct existing persistence mechanisms. **Inferred (high confidence):** incorrect interruption/saved reporting at the unowned crash cut. |
| Recommendation | Define the authoritative durable fact that means Submit committed and links the request to its original versioned references. Publish it with the bundle in the existing atomic store operation, or specify an equally deterministic recovery rule. On restart after that commit, state reads/snapshots recover `submitted` even when HTTP/WS/resume delivery failed. Resume never replays plaintext. Assign fault cuts immediately before and after publication, failed terminal persistence, failed resume dispatch and lost response/event. This is secret safety metadata, not a workflow transaction engine. |

```text
secret bundle durably published
              |
              | crash before durable submitted receipt
              v
pending request recovered as interrupted
              |
              v
incorrect card state / duplicate replacement
```

#### R2-MAJ-003 — Expected-before assumes a shared configuration lock that does not exist

| Field | Review |
|---|---|
| Lens | 2 Incompleteness; 3 ADR/AS-IS consistency; 4 Infeasibility; 6 Security |
| Affected section | §2.1; §6.6; FR-085; BDD-11.9; test 75; WU-3 |
| Failure scenario | A helper validates a provider plan against the loop's snapshot while a Settings write updates the saved configuration under a different lock. The two writes can each pass against their own before-state and overwrite a confirmed shared change. Alternatively, acquiring the named REST mutex inside the existing system-tool mutation path restores the documented reversed-lock-order deadlock. |
| Evidence | The spec explicitly names `restAPI.configMu` as the expected-before seam. REST uses `safeUpdateConfigJSON`/`writeConfigJSONLocked` under that mutex; system tools use `Deps.WithConfig` → `AgentLoop.MutateConfig` and a directly wired `config.SaveConfig`, without it. `Deps` documents why acquiring both locks in opposite order was removed. Also, current `ProviderListTool.Execute` returns only provider name/credential-presence status, not the saved endpoint/protocol/reference projection FR-085 expects the summary to carry (E05–E06). |
| Certainty | **Verified (high confidence):** actual lock domains, lock-order warning and read projection. **Inferred (high confidence):** stale write or deadlock if the stated reuse is followed literally. |
| Recommendation | Name one actual synchronized read/check/publication boundary for participating Settings and tool writers, with a stated lock order. Define a secret-free before-state projection or revision token, including absent-entry semantics, through an existing lookup surface. Never put resolved values into summaries/preconditions. Cover tool-versus-Settings contention and provider, MCP server, channel and mailbox cases, not merely two provider helpers. Reuse ordinary synchronization; no global workflow transaction or speculative locking framework. |

#### R2-MAJ-004 — The universal gateway-environment prohibition contradicts retained integration resolution

| Field | Review |
|---|---|
| Lens | 3 Inconsistency; 4 Infeasibility; 6 Security and user promises |
| Affected section | §6.1; FR-062 versus FR-076; WU-3/WU-4/WU-5; test 74 |
| Failure scenario | A provider/mailbox value arrives safely through the card, then an approved tool selects its reference. Boot or reload runs the retained credential pipeline and puts it in the gateway process environment, violating FR-062. Implementing the blanket prohibition instead without replacing its consumers can make the provider/mailbox unusable. |
| Evidence | FR-062 and §6.1 prohibit every submitted kind from entering the gateway environment. `credentials.InjectFromConfig` deliberately calls `os.Setenv` for provider and mailbox references. `bootCredentials` and `executeReload` call it; `ProviderConfigureTool.Execute` explicitly relies on that boot/reload resolution. FR-076's private-kind-only, per-child rule is the decided narrower boundary. The work units do not inventory a wholesale replacement of integration credential resolution (E01–E03, E07). |
| Certainty | **Verified (high confidence):** blanket text and retained source mechanism. **Inferred (high confidence):** violated guarantee or broken integration if the mismatch is left to implementation. |
| Recommendation | Make the private-secret prohibition explicit and consistent across FR-062/FR-076, card copy and documentation while truthfully describing retained provider/mailbox resolution. If the author intends a broader prohibition, it must instead name the replacement resolver, all affected consumers, boot/reload proof and authorized scope; do not silently commission that redesign. F:D10 forbids chat/model/tool-argument disclosure; it is not evidence that today's gateway never resolves integration values in its own environment. No private secret may be placed there under either interpretation. |

#### R2-MAJ-005 — Submit-time masking can be erased by ordinary refresh and sign-in

| Field | Review |
|---|---|
| Lens | 2 Incompleteness; 6 Security and user promises; 11 Testability and false greens |
| Affected section | §6.7; FR-069, FR-076; tests 31/35/74; WU-4/WU-5 |
| Failure scenario | With secret filtering enabled, Mia submits a private secret, and immediate exact-output masking passes. A Settings save, reload or provider sign-in rebuilds the complete masking set from currently configured references/OAuth, omitting private values and saved-but-unused versions. Mia's next approved command prints the exact value; it is no longer masked. Concurrent registrations can similarly evict a value registered by another path. |
| Evidence | `Config.RegisterSensitiveValues` is **replace, not append**. `refreshConfigAndRewireServices`, `executeReload`, `reRegisterOAuthSensitiveValues` and the OAuth registration callback each reconstruct and replace the complete set. The existing enumeration is configuration-reference-driven, not an inventory of new private/unused versioned entries. §6.7 specifies adding on Submit but does not disposition these complete-set rebuilds. Planned immediate leak/masking tests do not name those refresh/restart transitions (E01, E08). |
| Certainty | **Verified (high confidence):** replacement semantics, rebuild callers and missing lifecycle ownership. **Inferred (high confidence):** loss of exact-value masking after a normal transition. |
| Recommendation | Give WU-4 explicit ownership of every full-set rebuild: boot, reload, Settings refresh, sign-in/token refresh and connector-sensitive registration. Keep currently retained private and unused submitted values represented; synchronized registration must not lose another update. Extend the sentinel/exact-value tests across these transitions and concurrent registrations, with non-empty capture and known-leak controls. Keep transformed-output and disabled-filter limits honest; this does not reopen the accepted ability of a permitted private command to reveal a value. |

#### R2-MAJ-006 — B1 widens callable authority before B2 installs its required Ask boundary

| Field | Review |
|---|---|
| Lens | 3 Inconsistency; 6 Security; 7 Reachability; 12 Overcomplexity |
| Affected section | §15 B1–B3; WU-2/WU-3/WU-9/WU-10; FR-024/025/045/100 |
| Failure scenario | B1 lands Omnipus's role grants, including Allow `update_agent`. The still-existing tool accepts permission changes and connector assignments, so Omnipus can change Jim's permissions without the founder-required Ask tool; B2 is the bundle that removes those arguments. The fixed entry, workflow skills and setup kickoff are deferred to B3, so B1 also cannot pass the stated user-facing reachability gate. |
| Evidence | §15 calls B1/B2/B3 **landing bundles**, each independently gated. B1 contains WU-2 grants; B2 contains WU-3 permission split/refusal; B3 contains the workflow and screen entry consumers. Current `AgentUpdateTool.Parameters` exposes `tool_policy_changes`/`mcp_servers`; `applyCapabilityArgs` persists both. FQ-3 A and the corrected ADR require that remaining Ask boundary while retaining the accepted instruction-only gate (E01–E03, E09). |
| Certainty | **Verified (high confidence):** bundle allocation and callable arguments. **Inferred (high confidence):** incomplete/security-inconsistent B1 if landed as specified. No such landing has occurred. |
| Recommendation | Move the permission split, rejection paths and required executable/UI entry consumers into the first genuinely landable bundle, or make B1–B3 preparation stages and land one complete protected/reachable feature bundle. Keep contract-first preparation commits in the work branch. Require CI, all five reviewers, same-change docs and exact-commit tester plus independent validator before the executable landing. Do not add stubs, a new feature flag, a resolver exception or a third policy layer merely to make an incomplete stage appear landable. |

#### R2-MAJ-007 — Private-variable protection omits inherited families and document-runtime keys

| Field | Review |
|---|---|
| Lens | 2 Incompleteness; 4 Infeasibility; 6 Security; 11 Testability |
| Affected section | D6 protected-set derivation; FR-076; §2.1; tests 34/72/74 |
| Failure scenario | A private name such as `PYTHONPATH`, `NODE_PATH` or `OSL_SOCKET_PATH` passes the stated derivation but is then composed with the managed document runtime. Injecting it last replaces a runtime-controlled library/socket setting and breaks the next document command; injecting it earlier can silently discard/overwrite the promised private value. `LC_`/`XDG_` names also change inherited locale/configuration locations without being in the named fixed-key set. |
| Evidence | D6 derives protection from `allowedChildEnvKeys` plus names written by `setupEnv`/`composeExecutionEnv`, but does not name `allowedChildEnvPrefixes` or the transitive `documentruntime.ChildEnvironment` layer. The scrubber allows `LC_` and `XDG_` families. `composeExecutionEnv` invokes `ChildEnvironment`, which replaces/appends `PYTHONPATH`, `NODE_PATH`, `OSL_SOCKET_PATH` and other controlled variables. Those are not all visible as direct literals in the two functions named by the proposed drift test (E01, E10). |
| Certainty | **Verified (high confidence):** incomplete derivation and actual environment composition. **Inferred (high confidence):** runtime override or loss of the requested variable. |
| Recommendation | Include inherited prefix families and all composed-layer keys in the protected-name source of truth; reserve runtime configuration inputs without broadening the child allowlist. Add `LC_ALL`, `XDG_CONFIG_HOME`, `PYTHONPATH`, `NODE_PATH` and `OSL_SOCKET_PATH` refusal fixtures plus a valid private-variable control. Verify injection order with the managed document environment enabled, all three child paths and supported-platform case semantics. A scan of immediate function literals alone is not a sufficient drift oracle. |

### MINOR findings

#### R2-MIN-001 — The inherited-negative-control ownership claim is still incomplete

| Field | Review |
|---|---|
| Lens | 2 Incompleteness; 11 Testability and false greens |
| Affected section | §0.1 MAJ-006 closure; §9.4/§9.5; ADR-grill Test coverage assessment |
| Failure scenario | The author/qa-lead treats the complete scenario inventory and the “every negative control owned” claim as acceptance coverage. Model-listing success with an unavailable selected model, shared-skill contention, multi-consumer removal with failed reload/stale live value/next boot, or a policy reload during helper execution is never exercised by a named test. |
| Evidence | These cases are explicit in the inherited ADR-grill proof table. §9.5 owns several controls, but not those specific cases. §9.4 cites broad groups such as “revision tests” or “approval tests,” not concrete existing test symbols with their expected outcomes. §9.2 assigns all declared scenarios; undeclared inherited controls are outside that inventory (E01–E03, E11). |
| Certainty | **Verified (high confidence):** incomplete named ownership despite the closure claim. **Inferred (medium confidence):** omissions survive implementation because an inventory green is mistaken for coverage. |
| Recommendation | Complete the inherited control-to-requirement/scenario/test map. Reuse existing tests where adequate, but name their exact symbols and the outcome they prove. Include target-shelf create/remove/discovery and target environment setup where the broader FR-040 promise currently has only create/edit/private-target examples. Remove only genuinely superseded staging-expiry/promotion oracles, with the superseding founder answer noted. |

### Observation — non-gating

#### R2-OBS-001 — Keep composite accessibility and visual evidence separate from kit compliance

| Field | Review |
|---|---|
| Lens | 9 Accessibility and keyboard; 10 Design-system reuse and brand |
| Affected section | §6.3/§6.8; FR-109/110/112; BDD-12.9/12.13/12.14; WU-4/WU-10/WU-13 |
| Scenario / evidence | The corrected specification now supplies a full screen-state table, secret focus/Enter/error/terminal rules, keyboard journeys, non-colour attention, reduced motion, component composition and static Storybook acceptance. Those are adequate design handoffs, not rendered proof. Existing kit controls and keyboard tests do not by themselves prove the composed card does not submit into the composer, that the global entry preserves focus after attach failure, or that a status is announced in the actual chat shell (E01, E12–E14). |
| Certainty | **Verified (high confidence):** specified handoff and available components. **Inferred (medium confidence):** a primitive-only acceptance pack would miss screen-level failures; no current rendered defect is asserted. |
| Recommendation | Make the UAT evidence pack distinguish actual screen keyboard/focus/announcements, touch/narrow reflow and reduced-motion checks from catalogue/manifest/static Storybook checks. Use the real shell and composite; do not copy draft HTML styling or introduce a new control family. This is an acceptance reminder, not a new design choice or blocker. |

## Round-1 fix verification — actual text, not the closure tally

“Closed” below means the **specification obligation** is now present and has a named planned proof. It does not mean production code passed a test. Six prior MAJOR findings and the landing observation are **not fully resolved**; §0.1's unconditional “13 major fixed; no finding overridden” tally overstates closure.

| Round-1 finding | Assessment | Verified correction / residual |
|---|---|---|
| CRIT-001 workspace boundary | **Closed** | FR-065/076, K1/K2, D1/D6, BDD-7.5/7.6 and tests 34/71 now use trusted workspace + agent + variable, including concurrent same-agent/different-workspace fixtures |
| MAJ-001 staging lifecycle | **Partial — still open** | No staging/expiry and versioned durable Submit implement OQ-3/Q-R1-1 A. Private activation and blanket Stop-as-unused remain R2-MAJ-001; commit/recovery cut remains R2-MAJ-002 |
| MAJ-002 legacy unbound channels | **Closed** | FR-121/127–129, K14, BDD-13.1/13.8, D3 row 6 and tests 20/44/64/81 include an explicit agent without a workspace, required UI/REST pair and loud refusal |
| MAJ-003 submission-unknown recovery | **Partial — still open** | GET request state and bounded terminal snapshots are added. Durable storage versus durable terminal receipt remains R2-MAJ-002 |
| MAJ-004 contract/validation omissions | **Closed at definition level** | K8 documents the new-session agent rule; REST paths, generated inbound mirrors, unconditional semantics and real-handler validation-on/off tests 62–64 are named. No generated-validator runtime PASS is claimed |
| MAJ-005 no-default caller oracle | **Closed at definition level** | §6.4 dispositions all 16 getter calls plus secondary fallbacks; FR-131/132 and tests 77/78/80 add system-message, publication and boot/catalogue proofs |
| MAJ-006 meaningful test coverage | **Partial — still open** | Singleton/exclusive-authority/listing/D3 cases and non-empty sentinel control are added. Masking transitions and inherited unowned controls remain R2-MAJ-005/R2-MIN-001 |
| MAJ-007 God Mode/routing | **Closed** | FR-023 and BDD-3.4/3.7/3.8 preserve clarification/secrets. BDD-2.2 must ask; BDD-2.5 is a distinct nested-workflow fixture, no either/or oracle |
| MAJ-008 screen states/keyboard | **Closed at definition level** | §6.3, FR-109/112, BDD-12.13/12.14 and real-shell tests supply the missing journeys; observation R2-OBS-001 preserves the execution caveat |
| MAJ-009 all-agent grants/execution | **Partial — still open** | §6.5/FR-060 now name ordinary/custom grants, hidden/external exclusions and executable boot/reload wiring. Test 65 reaches request/save/list; permitted private-version consumption remains unspecified, R2-MAJ-001 |
| MAJ-010 private inputs/execution/masking | **Partial — still open** | Byte limits, NUL rejection, uppercase grammar, private wording and three child paths are corrected. Gateway-environment overpromise, masking refresh and omitted protected layers remain R2-MAJ-004/005/007 |
| MAJ-011 shared writes | **Partial — still open** | Resource table/FR-085 and provider fixture exist; actual shared lock/read-before seam is still incorrect/incomplete, R2-MAJ-003 |
| MAJ-012 picker/self-row | **Closed** | FR-104 preserves eligibility/non-member exclusions; FR-111/BDD-12.12/test 54 preserve graph depth edit/removal/reload and refused next helper launch |
| MAJ-013 secret endpoint trust | **Closed at definition level** | §6.7/FR-083/084, bounded metadata, audit and tests 68/69 supply authenticated scoped consent and zero-write/no-resume refusal, with security-lead gate. Q-R1-2 B is explicitly applied |
| MIN-001 browser warm-up | **Closed** | FR-126/BDD-13.11/test 79 choose Default's browser and log the no-manager case |
| MIN-002 component mapping | **Closed** | §6.8/FR-110 map catalogued parts and require the design-system/static Storybook gate |
| OBS-001 landing bundles | **Partial — still open** | WU-10/11 UAT, Feature sizing and reviewer-only security ownership corrected; independent B1 landing still fails its dependencies, R2-MAJ-006 |

## Structural integrity and twelve-lens coverage

| Spec-mode check | Result | Evidence / limitation |
|---|---|---|
| Status and linked architectural authority | Present | Draft; corrected **The Omnipus system agent** and both later-answer sections linked |
| Contract-first order | Present | K1–K17 and five-step order precede implementation; backend-lead owns generation |
| API/data semantics | Partial | Shapes present; private selection, committed recovery and expected-before projection need correction |
| Screen loading/empty/error/partial states | Present | §6.3 includes uncertain mutations; state truth depends on R2-MAJ-001/002 |
| End-to-end journeys | Present | W1/W18/secret/connect/setup/operator recovery stories; no implementation acceptance claimed |
| Accessibility/keyboard | Present | Labels, field errors, composer isolation, terminal focus, navigation and graph text equivalent |
| Catalogue-first component mapping | Present | §6.8, actual catalogue and design-system skill checked |
| Security/user promises | Partial | Trust/consent/bounds/audit recorded; environment/masking/collision mismatches remain |
| BDD acceptance / traceability | Complete for presence, partial for adequacy | 98 FR definitions/98 matrix rows; 129 BDD scenarios/83 test rows; no declared scenario missing |
| Reachability | Partial | Registration/screen consumers named; private selection and B1 landing incomplete |
| User-facing documentation | Present as TODOs | §14 names changed pages and behaviours; same-change updates and docs-verifier required, not deferred to B4 |
| Success criteria | Measurable, not sufficient alone | Non-zero captures/control and observable results supplied; missing crash/refresh/permission-stage oracles remain |

| Lens | Applied result |
|---|---|
| 1 Ambiguity | R2-MAJ-001: stored versus selected private versions and Stop after selection |
| 2 Incompleteness | R2-MAJ-002/003/005/007 and R2-MIN-001: crash cut, shared writes, full masking rebuilds, environment layers and inherited proof |
| 3 Inconsistency / ADR / AS-IS | R2-MAJ-001/003/004/006; later founder amendments used over superseded ADR/draft text, source over dated AS-IS claims |
| 4 Infeasibility | R2-MAJ-003/004/007: named lock/resolution/environment mechanisms cannot deliver the literal promises unchanged |
| 5 Contract-first gaps | R2-MAJ-002/003: recovered-state truth and expected-before read projection; all K1–K17 reviewed below, generated types/validators treated separately |
| 6 Security / user promises | R2-MAJ-003/004/005/006/007; no third policy layer, backfill or accepted-risk redesign recommended |
| 7 Reachability | R2-MAJ-001/006: private consumer and executable first landing; new-tool grant/metadata/executable distinction verified |
| 8 UI states / journey | R2-MAJ-001/002: a Saved/unused/interrupted card must reflect committed/selected state; round-1 screen-state contract otherwise present |
| 9 Accessibility / keyboard | R2-OBS-001: whole-journey and composite checks distinct from primitive checks; no rendered PASS |
| 10 Design-system / brand | R2-OBS-001: actual catalogue checked, four-part publication/static build specified; draft colours/layout not production authority |
| 11 Testability / false greens | R2-MAJ-002/005/007 and R2-MIN-001: falsifiable crash/refresh/collision/inheritance oracles, not inventory green |
| 12 Overcomplexity | R2-MAJ-006: remove artificial independently gated incomplete stages; do not add a workflow engine, second vault, rollback framework or feature flag |

### UX heuristic check

**Several issues to address.** H1 (visible system status) and H3 (user control) require truthful committed/selected/unused state after Stop and restart. H5 (error prevention) requires safe stale-plan refusal and protected runtime names. H7 (keyboard operation) and H10 (help) are now explicit in the planned UI/documentation handoff; their execution remains unverified.

| Priority action | Findings |
|---|---|
| Make Saved, unused and interrupted reflect authoritative state | R2-MAJ-001/002 |
| Preserve masking and reject configuration/environment collisions through normal refresh | R2-MAJ-003/005/007 |
| Land a complete protected and reachable flow, with composite UI evidence | R2-MAJ-006; R2-OBS-001 |

## Contract completeness — K1–K17

These are specification assessments, not claims that the future schemas already exist. Generated Go/TypeScript/Zod types and committed inbound mirrors are required; no hand-written parallel wire type is authorized.

| Entry | Round-2 assessment |
|---|---|
| K1 SecretRequestFrame | Acting/root/owner identities, bounded fields and purpose named. Root/owner are server-derived. Test actual new-frame parsing plus recipient display, not just a mocked component |
| K2 SecretRecipientDisplay | All five kinds and private owner pair named; frozen effective recipient remains authority, display/purpose does not. R2-MAJ-001 must define private selection consistently |
| K3 SecretRequestResolvedFrame | Value-free terminal states named; live notification alone is not recovery. R2-MAJ-002 |
| K4 state/snapshot requests | Pending plus bounded terminal entries and never-null arrays named. Preserve full-snapshot ordering and stale-terminal convergence; durable committed truth needs R2-MAJ-002 |
| K5 Submit + state read | POST/GET paths, exclusivity, references-only recovery and safe failures named. Link original request to its committed references and test lost responses/crash cuts |
| K6 secret-name listing | Name/kind/version/recipient/owner/consumers/in-use named; real listing planned via tests 65/60. Active/unused private-version semantics need R2-MAJ-001. GET consumers must use the generated schema |
| K7 helper approval | Root/acting-name extension retains acting session/workspace and Deny-only reconnect stub. Real live/snapshot/foreground-main cases named |
| K8 MessageFrame | Workflow id/target and required agent for new session named; tests 63/66 distinguish anonymous refusal, singleton Omnipus and ordinary extra chat |
| K9 kickoff | Explicit server-initiated existing-main amendment, client-with-session refusal and producer test 43; not unchanged reuse of today's mint-only route |
| K10 pairing frames/subscription | Request/session correlation, Stop/reconnect/teardown and tests 37/BDD-8.8 supplied. Current consumer is channel/Settings-scoped, so WU-7 must wire the chat consumer and real parser, not only add optional fields |
| K11 Agent.default | Removal named; consumer changes must be buildable in the executable landing, not a generation-only landing |
| K12 AgentUpdateRequest | Default removal/Omnipus Ask refusal named. The agent-tool permission split is a separate callable boundary and must precede the grant, R2-MAJ-006 |
| K13 AgentToolsUpdateRequest | Omnipus write-time Ask refusal named; live compositor unchanged, Deny still allowed |
| K14 channel routing | Required nonblank write pair versus truthful invalid legacy read distinguished. Current same-schema GET/PUT reuse must become a shape that can express both without admitting an unbound write. Add `bound` to the generated read shape before returning it |
| K15 channel degraded state | Routing reason/drop time and clearing on repaired binding named; test real instance-specific status, not token-authentication success |
| K16 SessionCreateRequest | Required agent and singleton/ordinary control named; old optional-default description must go with it |
| K17 old default setting | Loader/settings sanitiser ignore-with-warning on load/reload. This is not a data migration or guessed destination |
| Across entries | §7.2 names handler semantics with validation on/off, SPA `parseFrameSafe`, generated artifacts/inbound mirror and inline discriminated unions under **Discriminated-union create contract for agents**. Drift green is not runtime validation. Add missing lifecycle/read projections to the same contract-first order |

## No-default-agent and existing-config audit

A controlled production search found **16 non-comment `.GetDefaultAgent()` calls**. §6.4 now assigns all sixteen a replacement owner/failure, including the previously omitted system-message/publication cases. The code still contains these calls because this branch is design-only; their current existence is not an implementation defect against this spec.

| Caller / former fallback | Required replacement now stated | Planned proof |
|---|---|---|
| WS `resolveTargetAgent` and `firstChatTargetAgentID` | Explicit frame agent for new chat, canonical session owner otherwise; no first-agent substitute | 45/63/66 |
| REST `createSessionHTTP` and first-chat fallback | Explicit agent; second Omnipus chat refused with main id | 45/66 |
| `resolveMessageRoute` | Instance's valid workspace/agent pair only, before any secondary resolution | 20/21/44/64 |
| `resolveSystemMessageAgent` | Named async origin; anonymous origin discarded with error/audit | 77 |
| `resolveGoalAgent` | Canonical owner only, not another `AgentIDs` participant | 23 |
| `processTaskDirect` | Task's agent, visible pre-dispatch failure | 23 |
| `agentForSession` | Canonical session owner or refused disposition | 23 |
| `enqueueSteeringItemWithStatus` | Session owner when agent omitted; no invented sender | 23 |
| `publishResponseIfNeeded` | Publish nil-agent error without another agent's send state | 78 |
| `initializeCore` | Install-level state root and visible recording failure | 80 |
| `MCPWorkspacePath` | Install-level configured root | FR-124/test 80; make relative-path expectation explicit during test authoring |
| `extractProvider` | Shared provider ownership, not routing to an agent | FR-124/test 80 |
| `GetStartupInfo` | Registered-agent iteration, explicit empty info | 80 |
| `ListSkillsDetailed` | Shared skill loader, not arbitrary agent grants | 80 |
| `HandleTools` | Central builtin catalogue; 503 while unwired | 80 |
| `HandleToolsRegistry` fallback | Same central catalogue; no agent proxy | 80 |
| Singleton seed/override/health/writer/tool/editor/default field | Deleted; old `agents.defaults.default_agent_id` ignored with warning, no rerouting/migration | 22/46/53; BDD-13.7 |
| Browser selection | Default workspace browser; no lexicographic substitute | 79 |

OQ-2 B is now faithful: an existing unbound channel with a named agent still loads, but every message is dropped loudly until explicitly bound; no channel config is automatically re-homed. OQ-5 is already with the founder and is not asked again.

## Founder-decision trace review

References are to **planned proofs**, never executed tests. Every founder row has identifiable coverage; the residual fidelity gaps are kept visible.

| Founder decision | Requirement → scenario/test | Assessment |
|---|---|---|
| D1 scope exclusions | FR-007/106; role 3/editor 29 | Relocation only; no Judge/Planner/core-security redesign |
| D2 capability planning/in-workspace creation | FR-040–046/053 → BDD-4.* → 13–15/57 | Present; target and coverage/readiness explicit |
| D3 up-front dependency checks/self-service | FR-053 → BDD-4.7 → 57/83; unchanged self-service | Explicitly added; installed is not readiness |
| D4 ordinary agent request | FR-090 → BDD-10.1/10.4 → 41 | No delegation edge required |
| D5 read-only ordinary message | FR-090 → BDD-10.5 → 41 | Explicit no-change control added |
| D6 summary + narrow Ask | FR-020–027 → BDD-3.* → 4–6/57/60 | Instruction-only risk retained; B1 boundary must be fixed |
| D7 God Mode | FR-015/023 → BDD-3.4/3.7/3.8 → 47/60 | Missing-information and secret exceptions corrected |
| D8 one agent/skills | FR-001/010/011 → 1/51/57–59 | Present; actual first landing incomplete |
| D9 checks inside change tools | FR-030–033 → BDD-9.* → 12/15/16/36/76 | Partial outcomes preserved; inherited proof mapping incomplete |
| D10 S1–S4 secret field | FR-060–079/083/084 → BDD-6/7/15 → 7–11/30–35/48/55/62/65/68–74 | Scope/consent corrected; selection, commit and masking remain open |
| D11 channel/mailbox tools | FR-080–082 → BDD-8.* → 36–38 | One-call binding/verification, correlated pairing and no email probe present |
| D12 no workflow record | §6.6/§19; FR-063/066 | No workflow engine; OQ-3 supersedes value staging/expiry; secret receipt is still needed |
| D13 routing | FR-011–013 → BDD-2.* → 51/52/58/83 | Ambiguous versus nested cases separated |
| D14 skill lifetime | FR-011 → BDD-2.4 → 58 | Reload after trimming, no new unload |
| D15 fixed entry/one conversation | FR-005/100 → BDD-1.7/12.1 → 52/63/66 | Singleton proofs corrected; entry deferred past B1 |
| D16 attention | FR-091/101 → BDD-10.2/10.3/10.6 → 40/82 | Unknown is not zero; helper decisions included |
| D17 Add-agent entry / Settings forms stay | FR-045/108 → 52/53 | Present; no-model fix stays in Settings |
| D18 only Omnipus UI creation | FR-108 → BDD-12.8 → 53 | Kept REST is decided FQ-10 exception |
| D19 Agents removal/no forwarding | FR-108 → BDD-12.7 → 53 | No route resurrection or forwarding proposed |
| D20 Team list/cards/workers/actions | FR-102–105/112 → BDD-12.* → 26/54 | Complete state/keyboard plans now present |
| D21 legitimate orphans in picker | FR-104 → BDD-12.4 → 27/54 | Hidden/member exclusions preserved |
| D22 Ava removal/greenfield/welcome | FR-008/047/048 → 1/43/53 | No data migration requested |
| D23 Admin replaced by fixed entry | FR-008/100/108 → 1/52/53 | Present; existing setup authority only |
| D24 skippable setup offer | FR-047/052 → BDD-5.1/5.2/5.7/5.9 → 26/43/54/59 | Failed load no longer treated as empty |
| D25 tailored team/graph/no templates | FR-010/049–051 → BDD-5.5/5.6 → 59 | Graph text equivalent retained |
| D26 reuse | FR-049 → BDD-5.3/5.4 → 59 | Unchanged reuse; separate all-workspace shared-change proposal |
| D27 helpers/main available/no overwrite | FR-091–095/085 → BDD-11.* → 24/39/42/50/56/59/75 | Decision surface present; actual shared-write seam remains open |
| FQ-1 | FR-003/004/094/111 → 19/42/54 | Hidden default-workspace member; ordinary editable/removable self-row |
| FQ-2/3/6/16; implied FQ-5 | FR-020–027 → 4–6/17/18/47/57/61 | Ask list, separate permission tool, both confirmations, no Omnipus per-agent Ask; implication not misreported as a new direct answer |
| FQ-4/11/12 | R-A1–R-A3; §14 documentation | Accepted risks recorded, not re-decided or silently mitigated |
| Later FQ-7/18; OQ-1 | FR-065/076 → BDD-7.1–7.9 → 34/35/71–74 | Private secrets in scope, per workspace + agent, narrower wording confirmed; consumer gap remains |
| FQ-8/15 | FR-078/091–093 → 33/39/40/56/59 | Every helper decision in Omnipus main; acting helper identified |
| FQ-9/10 | FR-106/108 → 29/53 | System agents home; REST create retained/documented |
| FQ-13 | FR-077 → BDD-15.* → 48/49; separate #1277 | Prerequisite carried; not proof the bug is fixed here |
| FQ-14 | FR-049/095 → 50/59 | Reuse unchanged/shared-impact proposal retained |
| FQ-17; OQ-2 | FR-120–129/131/132 → BDD-13.* → 20–23/44–46/63/64/77/78/80/81 | No default at all; mandatory binding, loud failure, old-key warning |
| OQ-3 | FR-063/066/067 → BDD-3.2/6.5 → 7/9/30/55/60 | Durable Submit/no expiry; selected-versus-unused and commit cut still need correction |
| OQ-4 B | FR-126 → BDD-13.11 → 79 | Warm default workspace browser |
| Q-R1-1 A | FR-072 → BDD-6.14 → 8/70 | Versioned replacement; no overwrite-before-selection |
| Q-R1-2 B | FR-083/084 → BDD-6.15/6.16 → 68/69/55 | Scoped authenticated Submit/no password; raw vault re-auth unchanged, security-lead verification gate |

## Every inherited ADR-grill finding

Authority: the original ADR grill and the corrected **The Omnipus system agent**::Grill findings → disposition. Superseded tests are not restored.

| Inherited finding | Requirement / planned proof | Round-2 disposition |
|---|---|---|
| CRIT-001 read-to-web disclosure | R-A2; §14 Security TODO | Founder-accepted risk preserved; docs-verifier audits actual wording |
| CRIT-002 executable planting | R-A3; Agents/Security TODOs | Founder-accepted risk preserved |
| MAJ-001 infeasible code Go check | FR-014/026 → 51/57; forged peer control 41 | Prompt-only decision; default-safe prohibition present, wording not code enforcement |
| MAJ-002 secret lifecycle | FR-063–069/072/079 → 7–10/30/70 | Durable storage replaces staging; R2-MAJ-001/002/005 remain |
| MAJ-003 effective recipient | FR-065 → D1/test 10 | Full descriptor/owner pair retained; selection consistency R2-MAJ-001 |
| MAJ-004 provider endpoint | FR-031 → BDD-9.1–9.3/test 12 | Custom/keyless/protocol controls present; listing-versus-execution control unowned, R2-MIN-001 |
| MAJ-005 target versus anchor | FR-040–043/047 → 13/14/43/73 | Explicit targets retained; complete verb proof mapping R2-MIN-001 |
| MAJ-006 shared reuse | FR-049/085/095 → 50/59/75 | Product boundary faithful; shared-lock/projection R2-MAJ-003 |
| MAJ-007 hidden approvals | FR-091/092 → 24/39/40/56 | Root/acting/session/snapshot/ordinary-scope plans present |
| MAJ-008 channel/QR proof | FR-080–082 → 36–38 | Wrong binding, separate mail readiness, QR teardown/reconnect now named; actual chat consumer must be wired |
| MAJ-009 dangerous removal | FR-077 → 48/49; #1277 | Prerequisite/refusal carried; multi-consumer/stale-live/failed-reload ownership R2-MIN-001 |
| MAJ-010 partial save | FR-030/033 → 15/76 | Four truthful outcomes and selection/config-failure cut present; different Submit cut is R2-MAJ-002 |
| MAJ-011 card recovery | FR-074/075/079 → 25/55 | State read/terminal snapshots added; committed truth R2-MAJ-002 |
| MAJ-012 keyboard/focus | FR-050/109/112 → 25/26/52/54/55/59/82 | Contract expanded; execution caveat R2-OBS-001 |
| MAJ-013 docs map | FR-130; §14 | Specific pages/TODOs, Tools included; same-change obligation remains |
| MAJ-014 operator Ask/God Mode | FR-022/023 → 6/47/60 | Write-time refusal, no resolver bypass; pending mid-helper policy-reload control R2-MIN-001 |
| MIN-001 conflict literals | §5/§6.6; BDD-11.5/test 50 | CONFLICT versus REVISION_CONFLICT preserved |
| OBS-001 kit reuse | FR-110; §6.8 | Actual composition/static acceptance named |
| OBS-002 architectural proof table | §9.4/§9.5 | Partial mapping, not all inherited controls owned, R2-MIN-001 |
| OBS-003 main-only Ask handbacks | FR-092/093; helper tests | Founder allowed helper execution; no additional main/helper coordinator needed |

## Test coverage assessment — planned, not executed

| Category | Required correction / negative control |
|---|---|
| State/activation | Private `.v1` → saved `.v2` → explicit permitted selection; Cancel before selection; Stop after completed selection; no rollback lie |
| Crash/recovery | Submit publication versus submitted receipt; lost HTTP plus WS; failed resume dispatch; before/after commit restart; original references recovered, no plaintext replay |
| Concurrency | Shared tool-versus-Settings write with one safe comparison/publication boundary; each resource kind; concurrent private scopes, registrations and selections |
| Masking | Private and unused versioned values across Settings refresh, reload, sign-in/callback and restart; non-empty decoded model/log/error captures and known-leak control |
| Environment | Protected prefix families/transitive document layer, injection order with managed runtime enabled, supported-platform semantics and three child execution paths |
| Levels/reachability | Real ordinary/custom dispatch and permitted consumption, actual gateway parsers and chat shell; metadata/source scans alone do not prove execution |
| Frontend | Preserve §6.3's complete state/keyboard journeys and real-shell acceptance; Saved/interrupted/unused must be server-confirmed; primitive Storybook evidence is not composed-screen proof |
| Contracts | Valid control plus malformed/missing/extra/null/oversize transport data; both validation modes; new outbound union/parser and inbound mirrors; K14 read/write asymmetry |
| Regression inheritance | Named listing-success/model-unavailable, shared-skill, removal-reload-live-boot and mid-helper policy-reload controls; reused tests need exact symbols |
| Landing | Permission split/refusal and UI/workflow consumers on the exact first landable commit; CI + all five reviewers + tester/independent validator, not later B2/B3 receipts |

The static scanner proved it can expand full and abbreviated ranges, find a known-present scenario and expose a deliberately absent scenario before its zero-omission result was used. Its result is **document inventory only**. No test suite, mutation, build, browser, live provider or CI gate was run by this review. Future tests must retain RED-before-GREEN evidence, meaningful mutation controls, race/platform coverage, `npm run typecheck`, design-system gates and the agreed UAT provider/model.

## STRIDE threat summary

STRIDE groups identity spoofing, state tampering, missing action attribution, disclosure, resource exhaustion and privilege escalation. “Gap” is a specification obligation, not an executed penetration test.

| Flow | Identity / authority | Ordering / attribution | Disclosure / bounds | Disposition |
|---|---|---|---|---|
| Peer/web/skill content → Omnipus | Existing provenance/policy retained | Instruction-only Go accepted | Read-to-web/executable risks accepted | R-A1–R-A3 not reopened |
| Secret request → Submit → recovery | Server-owned account/session/recipient checks named | Durable commit receipt gap | Values only in scoped REST; pending/body/retention limits named | R2-MAJ-002 |
| Private store → child | Trusted workspace/agent scope corrected; selector missing | Stored/selected distinction incomplete | Exact masking rebuild and controlled-env collisions | R2-MAJ-001/005/007 |
| Shared integration config | Existing tool/Settings authority retained | Compare/publication synchronization incomplete | Precondition must be value-free | R2-MAJ-003 |
| Provider/mailbox resolution | Existing credential references | Boot/reload retained | Blanket environment promise false as written | R2-MAJ-004 |
| Helper approval/attention | Root/acting identities preserved, unrelated scope retained | Ordinary queue/terminal/stub rules kept | Bounds inherited, no global scope erasure | Definition supplied; execution pending |
| First landing | Omnipus grants precede permission-tool refusal | Bundle order wrong | Remaining Ask barrier must not be temporarily lost | R2-MAJ-006 |
| Channel input | Required available bound pair, no fallback | Instance-specific log/audit/status | No accidental rerouting into management agent | Corrected specification; runtime proof pending |
| Secret removal | Ask/consumers and #1277 prerequisite | Durable/live/boot facts separate | Multi-consumer recovery proof unowned | R2-MIN-001; prerequisite retained |

## Reachability check and documentation

| Capability | Review result |
|---|---|
| Six new tools | Pre-implementation absence is expected. §6.5 now requires static names, shipped ceiling, role/custom grants, Auto classification, central metadata and executable boot/reload registry. The actual metadata-only registry distinction was checked |
| Ordinary/custom secret card | Test 65 now dispatches request/save/list with Judge refused. Private active-version consumption is not yet defined, R2-MAJ-001 |
| Omnipus fixed entry | Named consumer/state/keyboard/attention paths, existing-main attach and active-workspace preservation; not deliverable in B1 as allocated |
| Helpers | Main question relay, root-correlated approval/secret cards, attention and live/snapshot rendering from another workspace; preserve ordinary queue/stub/scope and Stop semantics |
| Team/picker/graph/System agents/Channel settings | Valid homes, state and keyboard contracts supplied; self-row controls preserved, hidden engine and existing members excluded |
| New behaviour documentation | §14 names specific affected pages. Durable Submit/versioning/private scope/no-password exception, accepted risks, mandatory routing, permissions, removed navigation, helper visibility and model recovery must ship with their behaviour, audited by docs-verifier. WU-12/B4 is a sweep, not permission to defer updates |

**Code correct and tested:** not claimed — specification/source review only.

**Reachable by a user or agent:** not claimed — implementation and exact-commit acceptance remain pending.

## Unasked questions for the author — technical obligations

| Obligation | Answer needed in the final correction |
|---|---|
| Private selection | Which permitted consuming path selects the active version for one workspace/agent/variable? |
| Submit recovery | Which durable receipt proves the original save committed when later metadata/response/resume delivery is lost? |
| Shared writes | Which real lock/publication boundary and value-free read projection covers Settings and tool writers without reversed lock order? |
| Environment wording | How is the decided private per-child prohibition distinguished from retained integration resolution, without silently expanding scope? |
| Masking | Which complete-set rebuilds retain all still-stored private/unused values and cannot evict a concurrent registration? |
| Protected names | Which inherited families and composed environment layers feed the protected-name/drift contract? |
| Landing | Which exact commit first contains every required permission safeguard and user/agent entry consumer? |

## Questions for the founder — NEW only

**None.** The remaining issues are technical corrections under decisions already made. Do not interview OQ-1–OQ-4, Q-R1-1 or Q-R1-2 again. Do not duplicate OQ-5, which is already with the founder.

| Existing question | Round-2 position |
|---|---|
| OQ-1 | Per workspace + agent and narrower wording A confirmed; implemented in text. No disagreement with the decided recommendation |
| OQ-2 | B confirmed; mandatory pair including legacy named-agent/unbound refusal now explicit |
| OQ-3 | Immediate durable Submit/no expiry confirmed; old staging recommendation removed. Remaining stored-versus-selected defects are corrections, not a new storage-timing choice |
| OQ-4 | B confirmed; Default workspace warm-up now explicit, no no-warm disagreement remains |
| Q-R1-1 / Q-R1-2 | A/B applied; versioned integration replacement and scoped authenticated consent retained |
| OQ-5 | No new disagreement is established by this review. Already pending with founder; not repeated |

## Verdict rationale and final-round escalation

**REVISE** follows from seven MAJOR specification defects. The blanket round-1 closure claim is not supported for six prior major findings, and the newly explicit independent-bundle promise exposes a dependency/security gap. The MINOR inheritance-map defect and non-gating accessibility observation do not independently block.

### Escalation to the founder

The final correction has **not happened yet**; this table is the escalation register **if an item remains unresolved after that one fix**, not a claim that the author already failed it. No third grill and no second ADR grill are authorized.

| Blocking correction | Why it remains open at this review | Disposition if still open after fix round 2 |
|---|---|---|
| R2-MAJ-001 | Private-version selector/active mapping and truthful Stop state missing | Founder explicitly dispositions unresolved behaviour; do not silently activate on Submit or grant management tools |
| R2-MAJ-002 | Secret publication/request receipt crash invariant missing | Escalate remaining recovery gap; do not label interrupted as proof of no stored value |
| R2-MAJ-003 | Actual shared write/read seam undefined or misidentified | Escalate remaining concurrency/deadlock gap, with source evidence |
| R2-MAJ-004 | Universal promise conflicts with retained resolution | Escalate any unresolved promise/scope contradiction; no unapproved resolver redesign |
| R2-MAJ-005 | Masking full-set rebuilds unowned | Escalate unclosed exact-value masking obligation with transition evidence |
| R2-MAJ-006 | First executable landing lacks required barriers/entry consumers | Do not land that incomplete bundle; founder decides scope/disposition, not an automatic bypass |
| R2-MAJ-007 | Protected-set derivation misses active environment layers | Escalate unresolved collision/injection-order contract |

| Next action | Owner |
|---|---|
| Use existing founder answers; no new questions were discovered. Apply the one final specification correction at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec.md` | team-lead / spec author |
| Verify the complete remaining Ask/secret trust surface without re-deciding R-A1–R-A3 or the no-password exception | security-lead, reviewing only |
| Escalate any blocking finding still open after the final fix; do not run round 3 | team-lead / founder |
| Once resolved, plan RED/GREEN/CHECK and the five-reviewer gate with genuinely buildable/protected/reachable landing scope | team-lead |

This was grill round **2 of 2 (fixed, final)**. Next: team-lead uses the **Questions for the founder** section (no new interview points here), then the spec author fixes round-2 findings. Any blocking finding still open after that fix is escalated to the founder using the register above. **Do not run a third grill round.** Once resolved, team-lead plans implementation and acceptance.

skills: omnipus-shared-rules, grill-spec, gitnexus-exploring, ux-heuristics-review, omnipus-design-system, jev-use:jev-use, commit-messages

## Evidence table — grill-osa-spec-r2

| Claim | Evidence | Certainty |
|---|---|---|
| E01 — Complete spec and round-1 report checked | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec.md`::§0–§20, FR/BDD/K/test/trace/work-unit tables; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec-review.md`::all findings and inheritance/control tables. Complete first-hand reads, input hash above | Verified, high confidence |
| E02 — Later founder authority checked first-hand | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-adr-founder-answers-20261012.md`::After the correction round / Spec open questions / Spec grill round 1 questions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-system-agent-decisions-20261012.md`::Decisions Log D1–D27 | Verified, high confidence |
| E03 — Corrected design / every inherited finding checked | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md`::OSA-1–13 / both dated founder-answer amendments / Grill findings → disposition; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261011-omnipus-system-agent-review.md`::all findings / Test coverage assessment | Verified, high confidence |
| E04 — Store publication and question metadata/resume are distinct | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/credentials/store.go`::Store.Set / saveFileNoLock; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/askuser/registry.go`::Submit / persist / persistTerminal / dispatchResume. These are existing seams, not the future secret implementation | Verified source, high confidence; future crash outcome Inferred |
| E05 — REST/tool locks and lock-order warning | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_config.go`::safeUpdateConfigJSON / writeConfigJSONLocked / applyWrittenConfigLocked; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/deps.go`::Deps / WithConfig; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop_config.go`::MutateConfig; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/gateway.go`::wireSystemTools; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/config/config.go`::SaveConfig (atomic file publication, no shared config-write lock) | Verified source, high confidence |
| E06 — Provider read/write projection | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/provider.go`::ProviderListTool.Execute / ProviderConfigureTool.Parameters / Execute | Verified source, high confidence |
| E07 — Retained process-environment resolution | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/credentials/inject.go`::InjectFromConfig / ResolveAll; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/gateway_reload.go`::executeReload; boot call-site read/search in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/gateway_boot_credentials.go`::bootCredentials; E06 provider reliance | Verified source, high confidence |
| E08 — Replacement masking and complete-set rebuilds | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/config/security.go`::RegisterSensitiveValues / sensitiveReplacementPairs; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_config.go`::refreshConfigAndRewireServices; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/gateway_reload.go`::executeReload; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_sign_in.go`::reRegisterOAuthSensitiveValues; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/gateway_boot_credentials.go`::oauthSensitiveValueRegistrar | Verified source, high confidence; future eviction scenario Inferred |
| E09 — Allow update currently accepts permission/connector changes | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/agent.go`::AgentUpdateTool.Parameters / Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/agent_apply_args.go`::applyCapabilityArgs; E01 §15 bundle allocation, E02 FQ-3 A | Verified source/plan, high confidence |
| E10 — Protected families and composed document layer | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sandbox/hardened_exec.go`::allowedChildEnvKeys / allowedChildEnvPrefixes / isAllowedChildEnvKey; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/shell_env.go`::composeExecutionEnv; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/environment_setup_runner.go`::setupEnv; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/documentruntime/runtime.go`::ChildEnvironment | Verified source, high confidence |
| E11 — Controlled trace inventory sees ranges and omissions | Inline `python3 -I` static inventory, exit 0 → `instrument_controls 6 / 6 PASS`; `fr_definitions 98 matrix_rows 98 missing []`; `bdd_definitions 129 planned_test_rows 83 matrix_missing [] test_missing [] test_unknown []`. Controls include known-present `1.1`, absent `99.9`, full and abbreviated ranges. E03 inherited proof compared with E01 §9.4/9.5 | Verified document analysis, high confidence; not product tests |
| E12 — Current UI ownership/scoping and pairing consumers | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/store/toolApproval.ts`::isApprovalInScope / reconcileWithSessionState; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/chat/useSelectSession.ts`::useSelectSession; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/workspaces/team/AddAgentPicker.tsx`::candidates / disclosure; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/store/chat/slices/replay-and-status-frames.ts`::handleReplayAndStatusFrame whatsapp_pairing branch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/store/whatsappPairing.ts`::useWhatsAppPairingStore; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/skills/WhatsAppNativeNotice.tsx`::WhatsAppNativeNotice | Verified source, high confidence; no rendered acceptance |
| E13 — Components/design constraints checked, draft not treated as current authority | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/design-system/catalog.json`::entries; controlled JSON inventory, exit 0 → Field/Input/Label/Button/ConfirmDialog/EmptyState/QueryErrorState/Skeleton/Tabs/ViewSwitch/AgentIcon/Popover/Sheet entries. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/ui/CLAUDE.md`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/design/design-system-definition.md`::Operating model / D5–D8 / D16; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/brand/brand-guidelines.md`::Brand foundation / voice. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/omnipus-workflows/index.html`::full workflow/routing/screens draft source read | Verified reads, high confidence; visual appearance Unknown |
| E14 — Existing affected user documentation read | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/security.md`::secret filtering / God Mode / saved secrets; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/tools.md`::How to change an agent's tool access / policy; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/settings.md`::full settings/credential/delegation instructions; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/agents.md`::default / creation / editing; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/workspaces.md`::main chats / creation / Team; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/using-omnipus-ui.md`::sidebar / navigation | Verified reads, high confidence |
| E15 — Current contracts and validator seams checked | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/openapi.yaml`::getChannelRouting / setChannelRouting / mailbox routes; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/asyncapi.yaml`::MessageFrame / pairing frames; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/ChannelRouting.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/SessionCreateRequest.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/SessionStateFrame.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/SessionSnapshotFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_inbound_validate.go`::decodeAndValidate / ValidateInboundFrameJSON; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/lib/ws.ts`::parseFrameSafe; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/scripts/gen-contracts.sh`::Step 5 mirror | Verified source/contract reads, high confidence; no generation/validation run |
| E16 — Reachability policy/catalogue mechanisms are distinct | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent/role_policies_adr090.go`::ADR090RolePolicyInventory / adr090SparseRolePolicies; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent/seed.go`::denyAllThenOverride / NewCustomAgentToolsCfg; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/central_builtin_registry.go`::buildCentralBuiltinRegistry. Known-present AskUserQuestion source matches establish the search roots can see registered tools; no new-tool implementation is claimed | Verified source, high confidence |
| E17 — Default caller inventory and relevant fallback mechanisms | Inline `python3 -I` non-comment production sweep, exit 0 → sixteen `.GetDefaultAgent()` call sites matching E01 §6.4. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/goal_triggers.go`::resolveGoalAgent source read (owner → participants → default); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_channels.go`::validateUnbound / persistUnbound; all sixteen call-site contexts read in a second inline `python3 -I` source extraction, exit 0 → `CURRENT DEFAULT CALL CONTEXTS`, with absolute source paths for every call. Table assesses the spec's future disposition, not completed removal | Verified inventory/spec mapping, high confidence; future implementation Unknown |
| E18 — Source hierarchy / policies / re-auth / false greens | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/AS-IS-architecture.md`::agent/tools/channels/contracts sections; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/plugin-extensibility-assessment.md`::tools/skills/MCP; ADR titles inventoried and **Two-layer tool-policy model: the reconciled global ceiling IS the default; remove the fail-closed per-agent backfill**, **Discriminated-union create contract for agents**, **Re-auth Consent Gate: Credential-Vault Scope Expansion to v0.1.0** read. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_settings.go`::setCredential (authenticated caller and requireReAuth before store access); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/compositor.go`::resolveEffectivePolicyWith; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/false-green-patterns.md`::complete guidance; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/CLAUDE.md`::Hard Constraints / DoD / retired surfaces / authorship | Verified reads, high confidence; dated docs do not override current source |
| E19 — Graph limitation and severity triage honestly recorded | `gitnexus status`, exit 0 → `Repository not indexed`; `gitnexus detect-changes --scope all --branch work/omnipus-agent-design-20261012`, exit 1 → branch not indexed; stale sibling 3576 commits behind rejected. `jev-use judge` batch, exit 3 → masking severity escalated `unsure`; reviewer independently rated it MAJOR from E08/FR-069/BDD-7.3. Other seven candidate severity/question classifications returned; no new founder question. Final judgments remain grounded in the sources, not the judge alone | Verified instrument receipts, high confidence; impact Inferred, no graph run |
| E20 — Pinned baseline, human identity, clean scope | Read-only Git receipt: `git rev-parse HEAD`, exit 0 → `3f195b0a33a5ea7647a84af985f0c5836adf542f`; `git status --porcelain=v1`, exit 0 → empty; `git config user.email`, exit 0 → `10800669+daniel-piatkowski-ai@users.noreply.github.com`; author/committer identity reads each exit 0 → Daniel Piatkowski, same address. `git diff --name-status 54c38386b 3f195b0a3`, exit 0 → only spec and ADR amended. Hook inventory exit 0 → no active Git hooks. Final report-only commit receipt accompanies hand-back | Verified pre-publication receipt, high confidence |
| **Self-check** | Complete staged report and correction diff re-read against the done-criteria. Inline `python3 -I` report check, exit 0 → `report_self_check=PASS; counts=0/7/1/1; lenses=12; contracts=17; r1_dispositions=17; inherited_findings=20; new_questions=0; inputs_unchanged=true; human_identity=true; scope=report_only`. An initial greedy heading parser failed and was rejected; corrected parser controls passed 2/2 before its result was trusted. `git diff --cached --check`, exit 0 → no whitespace errors; staged scope is this report only. Final commit receipt accompanies hand-back. No product-test, reachability or visual success inferred | Verified report/source/publication self-check, high confidence; runtime acceptance Unknown |
