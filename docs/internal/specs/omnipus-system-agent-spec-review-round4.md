# Adversarial Review: The Omnipus system agent specification — round 4

**Correction: said Save-to-Library and reply-context were global, body-pair mail endpoints, wrong because the current OpenAPI puts them under the workspace-mail message route already covered by K44, correct is that the omitted global preview-token selectors, watcher-summary identity and mailbox-bound reference carriers still need correction. R4-MAJ-007 is narrowed accordingly; the finding count is unchanged.**

**Verdict: REVISE — 0 CRITICAL, 10 MAJOR, 2 MINOR and 2 OBSERVATIONS.** The previous corrections are largely retained at specification level. Room recovery and wire contracts still have residual gaps; the newly added connector and multiple-mailbox scope needs a complete identity, provisioning and screen-state handoff before implementation planning.

| Review header | Value |
|---|---|
| Document reviewed | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec.md` |
| Mode / round | **SPEC MODE — founder-approved EXTRA round 4 of 4, the final GPT round** |
| Reviewer / receipt lane | Independent architect, not the author / `grill-osa-spec-r4` |
| Date | 2026-10-13 |
| Branch / reviewed commit | `work/omnipus-agent-design-20261012` / `4ea52386be20640f0bfa452a89b2d4c1f98e5fbb` |
| Spec SHA-256 | `2eb1ef0adc9c1dc9bf1b448856d1957582cc13eacbac7b03d0c5f4cd6c6b9556` |
| ADR SHA-256 | `bed416387fb6b44084997a58905e77f31df90456419603819944e660b33615b2` |
| Authority | **The Omnipus system agent: one technical partner, workflows as skills, a narrow ask list and a secure secret field**, including OSA-14–16 and EM-3; later founder answers override earlier clauses and wireframes |
| Review exception | Founder answers::After spec fix round 2 authorizes rounds 3 and 4. The latest Extra review answer separately commissions a fresh independent Opus review after round 4's fix; this report neither launches it nor invents another GPT or ADR grill |
| Method | Whole-spec read, all three previous finding sets, founder records, linked issues, architecture sources, current contracts, user documentation, UI guidance and first-hand source inspection; all twelve lenses applied |
| Instrument limits | GitNexus MCP is not exposed; CLI status reports this checkout unindexed. Direct reads/searches are the fallback. No graph impact, build, product test, browser, visual acceptance or runtime PASS is claimed |
| Write boundary | Only this report is written and committed. No spec, ADR, contract, code, test or user-documentation changes; no builds, tests, services, stash or push |

## Executive summary

The review gives frontend contracts, states, journeys, keyboard access and component reuse the same scrutiny as backend routing, ownership, concurrency and recovery. **187 functional requirements, 234 declared behaviour-driven scenarios, 168 planned test rows and 42 active contract rows** resolve in the inventory. The brief's 139 tests was the round-3 count; K30 and K31 are intentionally removed. Reference completeness is not behavioural coverage or executed testing.

The decided architecture can continue to reuse one request/reply engine. The remaining problems are its integration obligations, not a reason to restore per-member room sessions, self hand-off, another workflow engine, a second mail pool or a third tool-policy layer.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 10 |
| MINOR | 2 |
| OBSERVATION | 2 |
| **Total** | **14** |

**Certainty convention:** cited contradictions, omissions and source mechanisms are **Verified (high confidence)**. Consequences of a future implementation are **Inferred**, not reproduced incidents; each finding states that distinction. Evidence IDs resolve to first-hand reads in the final table. Proposed test/component names remain plans, not existing features.

**Decisions preserved:** no default agent; mandatory workspace binding; main-chat mode by default; connection-isolated per-person histories; shared-main steering and its warning; individual command/resume entitlement; closed-by-default connections and explicit Open to anyone; user-only rooms with two-agent minimum; main-session room execution; reduced room commands and running work untouched by Pause; automatic digests scoped to the workspace, deliberate private notes agent-wide; email as an agent tool; T1/T2/T6/T8 only; EQ-1 **A**. **GC-5's unlimited replies and FQ-11/FQ-12's accepted risks are not reopened.**

## Findings

### CRITICAL findings

None. The defects below concern contradictory or incomplete integration of explicit requirements. No finding reclassifies an accepted risk, and this is not security approval of an implementation.

### MAJOR findings

#### R4-MAJ-001 — Shared revisions still omit new admission and mailbox meaning

| Field | Review |
|---|---|
| Severity / lenses | **MAJOR** — 2 Incompleteness/concurrency; 3 Inconsistency; 6 Security |
| Affected section | §6.6; FR-085/088/224–226; BDD-11.9; K19/K39–41; tests 75/88 |
| Failure scenario | A confirmed channel plan has Open to anyone on. The operator closes the connection without changing its People rows. The specified revision projection does not change, so the older helper plan can reopen it instead of being refused. Similarly, editing one mailbox's signature or adding/removing a sibling mailbox is not represented by the still pair-shaped mailbox projection; a stale setup can overwrite changed meaning |
| Evidence | §6.6 expressly enumerates channel binding, enabled, references, People/group rows, mode, mention settings and rules, but **not `open_to_anyone`**. Its mailbox projection remains **“hosts, username, reference name”**, and its resource row is still “Mailbox (agent, workspace)”. EM-3 adds mailbox ID, label, address, signature, per-mailbox identity and a list per pair. The round-3 fix claim also says BDD-11.9 rows were added for channel fields; its actual rows still contain only the generic channel/other-helper case. E01/E02 |
| Certainty | Verified omission, high confidence; stale overwrite/admission widening Inferred, high confidence |
| Recommendation | Define one canonical revision projection shared by Settings and tools. Include Open to anyone and every editable routing/entitlement field. Extend the existing mailbox precondition to the complete pair list and the selected mailbox's non-secret fields, including any membership that changes the confirmed operation; one pair-scoped token can do this without adding a second revision system. Define which existing read tool returns mailbox IDs and tokens to Omnipus—current `get_agent` does not, and no mailbox lookup is in the exact grant set. Add contention fixtures where **only** Open to anyone, signature or list membership changes. Keep the established config-lock → loop-lock order; no new transaction framework |

#### R4-MAJ-002 — The People handoff does not reconcile adapter prefilters and typed identities

| Field | Review |
|---|---|
| Severity / lenses | **MAJOR** — 1 Ambiguity; 3 Code consistency; 7 Reachability; 11 False-green risk |
| Affected section | §6.10/6.15; FR-140–145/147/210/212/221/224; K18/K32/K36; BDD-16.2/15/17/19/22/23 |
| Failure scenario | Carl is admitted through a listed Telegram group, but an adapter's sender-only allow check returns before the new central group admission sees him. A direct stranger is dropped before the refusal counter/reply. A Telegram row using `@username` passes UI validation but is stored as a peer ID although routing receives the numeric ID; it admits Anna but fails to select her agent. Forum-topic or Slack-thread IDs can also become distinct group keys contrary to §6.9 |
| Evidence | Telegram `handleMessage`, Discord `handleMessage` and Slack `handleMessageEvent`/`handleAppMention`/`handleSlashCommand` perform sender-only checks before BaseChannel. Telegram emits numeric direct peers and composite group/topic peers; its `parent_peer_id` is the topic ID, not the group ID. `RouteResolver.findPeerMatch` compares normalized adapter peer IDs, not usernames, and the existing RouteInput has no separate sender-person field. The new spec names a generic admission chain and per-type **format** validation, but not the mapping of person aliases, canonical person IDs, group/guild/team IDs and threads to that chain. E05/E06 |
| Certainty | Verified source/representation gap, high confidence; silent drops/wrong fallback/key selection Inferred, high confidence |
| Recommendation | Name the per-adapter entry points that must call the same admission/entitlement decision **before** early rejection and media work. Supply the typed mapping table: canonical sender used for person routing and entitlement; group/guild/team identity used for group admission/routing; thread retained for reply destination but normalized to the decided group conversation. Define alias normalization and duplicate matching using authenticated platform data, not labels. Preserve the stated routing priorities with explicit person/group overlap fixtures. Test group-only members, direct strangers, username versus numeric rows, forum topics, native slash commands and two same-type instances through real adapters—not only a constructed BaseChannel or resolver |

#### R4-MAJ-003 — The pre-ladder main-agent check contradicts person-first routing

| Field | Review |
|---|---|
| Severity / lenses | **MAJOR** — 3 Inconsistency; 1 Ambiguity; 7 Reachability |
| Affected section | FR-121/142/143/210; §6.4/6.10; US-13/16; BDD-13.2/13 versus 16.17 |
| Failure scenario | tg-1's main agent Mia is deleted, but Anna still has a valid Jim rule and Jim is a member of Sales. Following FR-121 drops Anna **before** the person table; following §6.10 resolves Anna to Jim and checks Jim. Both are literal implementations of this spec, with different observable results |
| Evidence | FR-121 says an instance not bound to an **available agent** must drop **“before the routing ladder runs”**. §6.10 instead orders destination person/group row → else main → unavailable → loud drop. OSA-14's route order and FR-142 likewise apply the person table first. Current `admitBoundConnectorInputCtx` checks the bound main's eligibility before capture, so leaving that old check is a concrete implementation trap, not a hypothetical new mechanism. E01/E02/E04 |
| Certainty | Verified contradiction and existing early gate, high confidence; valid ruled traffic being lost Inferred, high confidence |
| Recommendation | Reconcile FR-121 with the later person-routing decision: validate connection/workspace binding and a named main configuration, resolve the instance's matched destination, then validate that selected agent. A failed **matched** destination never falls back; a failed main drops an unmatched sender. Add the dead-main/valid-person-rule case and the inverse to the real intake fixture. Do not ask again whether to keep a global fallback; none exists |

#### R4-MAJ-004 — Stable room IDs do not yet define the receiver-write crash cut

| Field | Review |
|---|---|
| Severity / lenses | **MAJOR** — 2 Recovery/concurrency; 3 Code consistency; 11 False-green risk |
| Affected section | §6.11 Delivery identity/Restart; FR-216; K26/K34; BDD-17.27; test 144; residual R3-MAJ-007 |
| Failure scenario | A delivery ID is saved in the room record. Mia's receiver capture and transcript append succeed, then the gateway exits before the room records admission. Retry sees no recorded admission and calls the reused router again, minting a second request and repeating work. Recording admission first instead can lose a receiver append. The corresponding answer-append/main-mirror/mention-fan-out cuts can duplicate or lose part of an answer |
| Evidence | The new rule stores the delivery ID **before** admission and retries members **“without a recorded admission”**, but does not make that ID the existing capture/entry identity or state how separate publications are reconciled. `admitRequest` mints a new UUID on every call, writes the ledger, appends the receiver transcript and then publishes the wake. `replyIntoConversation` also mints a new answer ID per call; ledger Resolve does not durably settle an answer occurrence. The new example tests a partial fan-out after Mia was admitted, not explicitly the receiver-commit-before-room-receipt cut. E07/E08 |
| Certainty | Verified unowned commit boundary, high confidence; duplicate/lost work under the literal retry rule Inferred, high confidence |
| Recommendation | State the stable ID's relationship to the receiver capture and transcript entry; reuse it on retry and reconcile those durable facts before another append/wake. Define recoverable progress for the room answer, main mirror and mention deliveries. A committed receiver append with a missing room acknowledgment is already admitted, not a new admission. Exercise cuts before/after capture, receiver append, room acknowledgment, answer append, mirror and individual mention admissions; count captures and model starts independently. Keep consumed interrupted work explicit and non-replayed; do not promise exactly-once external tool effects or add a workflow transaction engine |

#### R4-MAJ-005 — Room requirements still cannot be carried by the listed contracts

| Field | Review |
|---|---|
| Severity / lenses | **MAJOR** — 5 Contract-first; 7 Reachability; 8 UI states; residual R3-MAJ-006/013 |
| Affected section | K7/K23–27/K33–35; §7.2; FR-201/213/215–217; tests 143/150–152 |
| Failure scenario | Jim refuses admission or fails during work. A generated K27 consumer rejects the required state or leaves him queued. The room's member list/cost/paused state never converges after reconnect because an outbound RoomSnapshot exists but no room attach/cursor operation is defined. A room send uses REST while mention IDs are only defined on MessageFrame; implementing its body from text can undo stable picker selection |
| Evidence | K27 still permits only **queued / held / working / waiting_on_you / answered / cancelled**, although FR-213 and BDD-17.26 require refused, failed, interrupted, unavailable and unknown. K23 now correctly makes a Room **not a Session**, but K35 still says **changed Session (room)**. K25 defines mention IDs on MessageFrame while K26 sends room messages through POST and names no RoomMessageRequest shape. K26 names RoomSnapshot/catch-up but not the inbound attach/subscription, cursor/completion or room-metadata update carrier. Current `handleAttachSessionWithAck` and its snapshot read resolve an ordinary session store/transcript. Pre-capture receiver Ask also needs a stated approval location/correlation; K7 only extends Omnipus-rooted helper approvals. E01/E07/E09 |
| Certainty | Verified contract contradictions/missing carrier map, high confidence; dropped/stale/unreachable state Inferred, high confidence |
| Recommendation | Finish the room operation map before consumers: REST message body with client ID and stable mentions; complete request-state vocabulary; one Room paused/origin/read-only representation; initial read, live membership/cost/pause updates and ordered reconnect/catch-up. Name how room viewing reuses the existing hub protocol without pretending a Room has an agent session. Specify where pre-admission Ask appears, its request/room/member identity and cancellation/reconnect behavior. Carry room refs consistently through live main entries, history and replay. Add **outbound union/parser and real reconnect tests**, not just malformed inbound writes. Preserve required agent ownership for ordinary Sessions |

#### R4-MAJ-006 — Multiple-mailbox identity stops before the existing runtime owners

| Field | Review |
|---|---|
| Severity / lenses | **MAJOR** — 2 Incompleteness; 3 Code consistency; 6 Isolation; 11 Testability |
| Affected section | US-21; FR-225–228/234; K40; WU-18; tests 159/161/165 |
| Failure scenario | Two mailboxes for Mia@Sales get separate config and identity sidecars, but both use the existing agent/workspace transport, credential resolver, watcher key or read-flight scope. One replaces the other, a watcher skips its sibling, or a delayed result from one is published into the other's view. Deleting/rotating one can also invalidate or erase both |
| Evidence | `registerEmailToolsForAgent` builds workspace → one transport and injects a generation resolver taking agent/workspace only. `gatewayMailRuntime.resolveCredentials`, `mailScope` and `mailGenerationForPair` select one config row by that pair. `MailboxWatcherSet.CycleAll` claims a cycle under agent + workspace, and the watcher state is pair-keyed. **Mail live access: pooled connections, folder discovery and a bounded cache** already requires full logical identity/generation for result sharing and preserves a separate physical-account capacity cap. K40 extends config and sidecar identity; the spec does not inventory these runtime adapters or their disable/remove/rotation/presence lifecycles. E10/E11 |
| Certainty | Verified existing pair-keyed owners and missing adaptation inventory, high confidence; collisions/cross-mailbox results Inferred, high confidence |
| Recommendation | Inventory the complete trusted tuple (agent, workspace, mailbox ID) through transport resolution, credential lookup, pool scope, generation/epoch, read-flight sharing, cache subtree, watcher/backoff/summary, panel presence, preview grants and cleanup. Preserve the shared account/global connection caps; do not build a pool per mailbox. Specify omission based on the pair's configured mailbox choices rather than silently shrinking to whichever transports are healthy; explicit unavailable/unknown/ambiguous choices refuse, never fall back. Add same-pair concurrent fixtures, including shared server/account, identical folder UID epochs, different mappings, one unavailable sibling, and selective rotate/disable/delete while another is active |

#### R4-MAJ-007 — Email contracts omit selectors and cannot bind today's message references

| Field | Review |
|---|---|
| Severity / lenses | **MAJOR** — 5 Contract-first; 4 Feasibility; 6 Isolation; 11 False-green risk |
| Affected section | FR-226–228; K40–44; BDD-21.2/4; tests 160/161; WU-0/18 |
| Failure scenario | Invoices and Support both contain UID 42 under UIDVALIDITY 7. A reference from Invoices is presented to a Support attachment/draft operation; today's `uid:7:42` cannot identify its issuing mailbox and can address Support's different message. HTML/attachment preview requests still carry only agent + workspace, so selecting a second mailbox either produces a 400 or opens the first. Watcher badges likewise cannot distinguish siblings |
| Evidence | The single reference issuer renders **`uid:<uidvalidity>:<uid>`**; `refEpochMismatch` compares the folder epoch, not mailbox provenance. FR-228 explicitly requires rejecting another mailbox's reference, but K40–44 do not name a new reference binding/carrier. K44 covers routes under `/workspaces/{id}/mail/{agentId}/…`, including Save-to-Library and reply-context. It does not cover the global HTML/attachment **preview-token** request schemas, which carry the pair in their body, or the workspace-level summary where `MailboxNewMailSummary` identifies only the agent. K41 does not name the changed configure/create request body. BDD-21.4 passes a reference to `read_message`, whose current and retained parameters accept **`uid`**, not `message_ref`. WU-0 explicitly owns **K1–K39**, not K40–44. E01/E10/E12 |
| Certainty | Verified representational gaps and incompatible test input, high confidence; wrong-mailbox access or dead preview Inferred, high confidence |
| Recommendation | Enumerate **every** mail request/response carrier: retain K44's selector on workspace attachment/save/reply-context routes, and add the missing selector/identity to global preview-token bodies, watcher summary, list/configure/create, cleanup and panel/full-screen selection context. Update their generated shapes before consumers. Define one issuer/validator binding mailbox identity to references (and the retained folder/config generation), with same-UID/epoch collision tests; a query selector alone cannot prove reference origin. Test that rule using a tool that actually consumes `message_ref`; preserve `read_message`'s UID interface unless an explicit further amendment authorizes changing it. Cover all nine tools with real registry dispatch and exact From/target/ref assertions. Assign K40–44 and missing carriers to contract preparation; follow schema → reference → generation → atomic commit → consumer |

#### R4-MAJ-008 — Add Email cannot invoke the required secret card without an agent turn

| Field | Review |
|---|---|
| Severity / lenses | **MAJOR** — 7 Reachability; 2 Incompleteness; 5 Contract-first; 8 Journey |
| Affected section | FR-061/065/075/230; §6.7; K1/K2/K5/K41/K42; US-21 AS-5; BDD-21.7/8; WU-18 |
| Failure scenario | A user opens Connectors → Agent tools → Add Email, selects Gmail and has no active Omnipus turn. The wizard demands a secret card, but the only request origin is an agent's request_secret call tied to its session. An implementer either cannot continue, secretly starts a model turn, or sends plaintext through a new test/configure argument outside the card's allowed value path |
| Evidence | FR-230 says the password is entered **only on the secret card**. §6.7 derives acting agent/session/recipient from the request record and refuses a missing/stopped session. K5 defines submit/state-read, not authenticated human initiation of an unbound setup request. The existing EmailMailboxPanel has a local write-only password input and submits a MailboxConfigureRequest; it does not mint an agent request. A new mailbox's frozen recipient also needs its stable mailbox ID before the password/test, not only after creation. E01/E13 |
| Certainty | Verified missing end-to-end request origin, high confidence; blocked or improvised unsafe setup Inferred, medium confidence |
| Recommendation | Specify the Settings-origin secure-entry path explicitly, using the same encrypted store and value-isolated control: how an authenticated user gets a value-free, destination-bound request/reference without an agent turn, how the target pair/mailbox ID is fixed before entry/test, where the card renders and where focus returns, and how Cancel/lost response/reload are reconciled. Keep the user's password out of model/tool arguments and shared drafts. Separate its lifetime/ownership from an agent park/resume; do not create a fake session or another secrets store. Name its generated request/response contracts and test with no model configured. EQ-1 A is settled and does not supply this missing UI entry path |

#### R4-MAJ-009 — Test Connection has contradictory save truth and no tested-input fence

| Field | Review |
|---|---|
| Severity / lenses | **MAJOR** — 1 Ambiguity; 2 Incompleteness; 3 Inconsistency; 8 UI truth; 11 Testability |
| Affected section | FR-030/063/066/082/231; K42; BDD-8.6/21.8/9; tests 38/163/166 |
| Failure scenario | The user submits a password on the card, then SMTP authentication fails. BDD-21.8 says nothing was saved even though the password was durably saved; treating it literally could delete it in breach of OQ-3. Separately, a good test can complete for mailbox settings A, then Save publishes edited settings B or a delayed A success enables B's Save without a check |
| Evidence | BDD-21.8 explicitly starts with **“a password on the secret card”** and concludes **“nothing is saved”**; FR-063/066 require durable Submit and no expiry, and FR-030 permits “nothing saved” only with no proven side effect. FR-231 requires testing before saving, but the separate Test endpoint/result has no defined association with the exact candidate, credential reference and revision subsequently saved. The new connect-mailbox save-only-on-success rule also needs to reconcile the earlier split-read/send fixture and truthful outcomes. E01/E02/E13 |
| Certainty | Verified save-truth contradiction and missing test/save association, high confidence; misleading success or unchecked configuration Inferred, high confidence |
| Recommendation | State **no mailbox configuration/activation is saved on a failed test; already-submitted credentials remain stored, unused, listed and removable**. Report both facts. Bind successful validation to the exact target, endpoint/ports, username, credential reference/version and relevant settings, or rerun it inside Save; any edit invalidates a previous success and late results cannot enable a different candidate. Distinguish failure before config publication from partial failure after successful test/publication. Add failure/no-config plus retained-secret, edit-after-test, out-of-order test completion, denied Save and lost-Save-response cases, with zero probe emails. This preserves durable Submit and the decided Test-before-save requirement, not a new rollback system |

#### R4-MAJ-010 — New mailbox and Configure journeys lack state and keyboard contracts

| Field | Review |
|---|---|
| Severity / lenses | **MAJOR** — 8 UI states/journey; 9 Accessibility/keyboard; 11 Testability |
| Affected section | §6.3/6.8/6.15; FR-109/112/221/226/229–231; BDD-12.16/21.2/6–9; tests 148/160/162/167 |
| Failure scenario | The template list fails and looks like Generic IMAP; a timed-out test looks ready to save; switching between Mia's two mailboxes drops a dirty draft or lets an older response populate the new selection. A keyboard user completes the old four-tab journey but cannot complete Gmail's wizard/secret/test/save path or tell which mailbox an error belongs to |
| Evidence | §6.3's Configure row covers routing save/no workspace, but there are **no rows for the new category/add picker, Email template/wizard/Test Connection, Mail switcher or multi-mailbox editor**. BDD-12.16 ends with the four chat tabs; the new mail tests mainly prove populated views and template/link presence. Existing Mail already promises selected From identity, cached/partial/unknown states, no automatic background retries and unsaved-change protection. Its current identity/view/query context is agent/workspace-oriented; US-21 adds a new selectable identity without a switch/race/dirty-input contract. E01/E14/E15 |
| Certainty | Verified missing per-surface handoff, high confidence; lost edits/wrong-context rendering/inaccessible journey Inferred, medium confidence; no rendered failure claimed |
| Recommendation | Extend the screen-state table and real-shell journeys: initial load, true empty/no match, partial/unavailable mailbox, testing, read-only success/send failure, timeout, rejected save, uncertain save and recovery. Define stale-result rejection and dirty-compose/draft handling when only mailbox ID changes, including full-screen/back-to-chat context. Specify initial/return focus, labelled steps/switcher/checkbox, safe Enter behavior, live announcements and touch/narrow reflow for Gmail and Test Connection. Preserve the existing Mail guards rather than replacing them. Add these journeys to exact-commit tester/independent-validator acceptance, not just primitive/link assertions |

### MINOR findings

#### R4-MIN-001 — Decided questions and current scope still appear open or contradictory

| Field | Review |
|---|---|
| Severity / lenses | **MINOR** — 3 Inconsistency; 2 Documentation/process |
| Affected section | §0.13/§1/§6.1/§10/§14/§17; FR-222; residual R3-MIN-001 |
| Failure scenario | Team-lead interviews EQ-1 again or a writer treats “tools unchanged” as forbidding the already-decided mailbox parameter. A user-documentation writer follows the remaining bare “Stop” TODO for rooms rather than their reduced command set |
| Evidence | §0.13 calls EQ-1 an interpretation to confirm and §17 still presents it as the new open question; the authority file already answers **EQ-1 A**. FR-222 still says email tools/connect_mailbox stay unchanged, while EM-3 amends them. §1's in-scope summary omits rooms/awareness/EM-3, and the group-chat documentation row retains bare “Stop”. Historical fix logs may remain, but these are current handoff sections. E01/E02 |
| Certainty | Verified stale handoff text, high confidence; repeated interview/misdirected work Inferred, medium confidence |
| Recommendation | Record EQ-1 A as answered, qualify unchanged behavior by EM-3, update the current scope and room documentation instructions, and distinguish historical “not grilled” notes from the current review status. Do not erase review history or re-ask the decided risk questions |

#### R4-MIN-002 — Old room-cost oracles still use full-turn sums

| Field | Review |
|---|---|
| Severity / lenses | **MINOR** — 3 Inconsistency; 11 Testability; residual R3-MAJ-012 |
| Affected section | BDD-17.7; SC-017; test 114 versus §6.14/FR-171/BDD-17.30 |
| Failure scenario | QA follows “sum of room-originated turns” and expects the entire $0.03 mixed-input turn in each room, whereas the new allocation fixture requires $0.01 each. Separate tests can encode incompatible expected cost values |
| Evidence | §6.14 and BDD-17.30 supply an independent equal-input allocation formula and unpriced flag, correctly closing the design choice. BDD-17.7 and SC-017 still describe **sum of room-originated turns** without a room-only-input precondition. E01 |
| Certainty | Verified inconsistent oracle wording, high confidence; conflicting expected values Inferred, medium confidence |
| Recommendation | Point every cost scenario/success criterion at the allocated-cost formula, or explicitly make the simple-sum fixture consume only that room's inputs. Keep failure/interrupted spend, deduplication and unknown-price checks. Do not add a loop cap |

### OBSERVATIONS — non-gating

#### R4-OBS-001 — Keep the reuse boundary explicit while removing duplicate descriptions

| Field | Review |
|---|---|
| Lenses | **12 Overcomplexity**; 3 Consistency |
| Evidence / suggestion | §6.11 now owns the room state machine and Pause fence; §6.9/6.10 own conversation control/routing; §6.12 owns memory scope. Keep these normative owners and refer to them from stories/contracts instead of rebuilding the rules in competing enums and Session/Room fields. New work is room records/fan-out/projection and narrow adapters around the existing ledger, FIFO, hub and mail owners—not a second runner, workflow log, pool or awareness inspector. E01/E07/E09/E11 |
| Certainty | Verified existing owners and duplication, high confidence; simplification recommendation Inferred, high confidence |

#### R4-OBS-002 — Extend the public-component mapping, not the visual design

| Field | Review |
|---|---|
| Lenses | **10 Design-system reuse/brand**; 9 Keyboard |
| Evidence / suggestion | The inspected catalogue has 62 entries, including public Checkbox, Field, Command, Dialog, Sheet, Switch, Tabs and DisclosureRow. Map Open to anyone, categorized/grouped connector lists, mailbox switcher and Gmail steps to those jobs. Presence in the catalogue is not itself a public import: the FormError entry has no public exports. Keep field/error composition through its sanctioned application/public owner; do not export a domain helper into the UI library just because §6.8 names it. Keep real-shell keyboard/reflow evidence separate from catalogue/manifest/Storybook compliance. E15 |
| Certainty | Verified catalogue/public-boundary facts, high confidence; no new visual choice or rendered PASS |

## Structural integrity and twelve-lens coverage

| Spec-mode check | Result | Evidence / limitation |
|---|---|---|
| Status and architectural authority | Present | Draft; authoritative ADR and later-answer precedence supplied; stale EQ-1 status is R4-MIN-001 |
| Contract-first order | Present, shape map incomplete | Correct five-step order; room/mail carrier gaps R4-MAJ-005/007/008; WU-0 misses K40–44 |
| API/data ownership | Partial | Room delivery commit and full mailbox runtime identity need R4-MAJ-004/006 |
| Every changed screen's loading/empty/error/partial states | **Incomplete for new scope** | R4-MAJ-010; old screen contract remains |
| End-to-end user journeys | Partial | Settings-origin secret entry and tested candidate/save boundary need R4-MAJ-008/009 |
| Accessibility/keyboard | Generic obligations and old composites present | New Gmail/mailbox/checkbox journeys require R4-MAJ-010; no browser acceptance run |
| Catalogue-first components/brand | Present, extend mapping | R4-OBS-002; no copied draft styling or brand redesign |
| Security and user promises | Earlier secret controls retained | New admission/revision/mailbox identity boundaries and save truth need correction |
| BDD and requirement → scenario → test | Complete **for reference presence** | 187/234/168 inventory; adequacy below is not established by those counts |
| Reachability | Named tools/screens, missing bridges | Real adapter handling, room transport and Settings card initiation remain incomplete |
| User-facing documentation TODOs | Specific pages retained | Same-change/docs-verifier rule stands; new identity/control/secret truth must be added, not deferred |
| Work units and landing | One complete landing retained | WU-14–18 Feature; extend contract ownership and actual runtime adaptation inventories before decomposing |

| Lens | Applied result |
|---|---|
| 1 Ambiguity | Person/group/alias mapping, selected-agent availability and tested/saved facts: R4-MAJ-002/003/009 |
| 2 Incompleteness | Shared revisions, crash cuts, mailbox lifecycle, secure entry and new UI states: R4-MAJ-001/004/006/008–010 |
| 3 ADR/code/internal consistency | Latest founder answers preserved; early gates, identities, contract remnants and stale handoffs: R4-MAJ-001–007/009, R4-MIN-001/002 |
| 4 Infeasibility | Today's mailbox reference cannot prove new provenance; read_message's reference fixture is not its retained interface: R4-MAJ-007 |
| 5 Contract-first | Room operations/state/live recovery; every mail carrier and secure-entry origin: R4-MAJ-005/007/008 |
| 6 Security/user promises | Reopening admission via stale revisions and logical mailbox isolation; durable-secret truth: R4-MAJ-001/006/007/009 |
| 7 Reachability | Early adapter rejection, selected target, actual room connection and no-model Settings secure entry: R4-MAJ-002/003/005/008 |
| 8 UI states/journey | Room failure/reconnect, setup/testing and mailbox selection/error recovery: R4-MAJ-005/008–010 |
| 9 Accessibility/keyboard | New composed wizard/switcher/checkbox journeys and focus/announcement ownership: R4-MAJ-010, R4-OBS-002 |
| 10 Design-system/brand | Existing public kit and Sovereign Deep guidance inspected; new mapping rather than new primitives: R4-OBS-002 |
| 11 Testability/false greens | Real adapter, cross-publication cuts, same-UID mailbox collisions, supported tool inputs, delayed results and independent cost oracles |
| 12 Overcomplexity | Keep one request/reply/queue/hub and application-owned mail managers; remove duplicated normative representations: R4-OBS-001 |

### UX heuristic check — several issues to address

| Heuristic | Relevant result |
|---|---|
| H1 Visibility of status | Failure/unknown room states and per-mailbox testing/partial/uncertain state must remain truthful |
| H3 User control | Retry must not repeat admitted work; mailbox switches must preserve or explicitly discard dirty input; room Pause remains distinct from agent Stop |
| H4 Consistency | One room representation, one tested candidate, one canonical logical mailbox identity |
| H5 Error prevention | Canonical person IDs, complete revisions and mailbox-bound references prevent wrong destination/configuration |
| H7 Keyboard operation | Complete Gmail/setup/switcher/checkbox journeys need composed focus and announcement acceptance |
| H9/H10 Recovery/help | Explain saved unused credentials after failed testing; remove answered questions and stale room Stop instructions |

| Priority action | Findings |
|---|---|
| Complete room commit reconciliation and transport/state map | R4-MAJ-004/005 |
| Scope mailbox runtime and every wire carrier to its logical identity | R4-MAJ-006/007 |
| Make secure setup, Test-before-save and keyboard/state journeys complete | R4-MAJ-008–010 |

## Verification of rounds 1 and 2

**Retained** means the requirement and its named planned proof remain in the current spec. It does not mean implemented, run, mutation-proven or landed. All previous reports were read; closure is checked against current text, not copied from §0's tally.

| Round-1 finding | Current disposition / evidence in the spec |
|---|---|
| CRIT-001 private workspace boundary | Retained: FR-065/076; K1/K2; same-agent/two-workspace D1/D6 and concurrent BDD-7.5/6 |
| MAJ-001 superseded staging | Retained with round-2 version/selection correction: FR-063–067/072/086. New mail Test wording contradicts saved truth, R4-MAJ-009; staging/expiry are not restored |
| MAJ-002 unbound named-agent channels | Retained: FR-127/128 and BDD-13.1/8. Main-availability ordering after person-routing expansion is R4-MAJ-003 |
| MAJ-003 submission-unknown recovery | Retained: K4/K5, FR-079/087, BDD-6.24 and receipt recovery |
| MAJ-004 contract/runtime validation | Earlier corrections retained: K8 and §7.2, validation on/off. Added-scope residuals R4-MAJ-005/007/008 |
| MAJ-005 no-default caller oracle | Retained: §6.4's sixteen callers plus secondary/boot/browser replacements |
| MAJ-006 meaningful coverage | Earlier controls retained: singleton, exclusive authority, real list/dispatch and nonempty sentinel control; new-scope adequacy remains partial |
| MAJ-007 God Mode versus clarification | Retained: FR-023 and BDD-3.4/7/8; genuine ambiguity versus nested routing stays separate |
| MAJ-008 old screen states/keyboard | Retained: §6.3, FR-109/112 and old real-shell journeys. Newly added mailbox/UI scope needs R4-MAJ-010 |
| MAJ-009 ordinary/custom grants | Retained: §6.5, executable boot/reload wiring and test 65; private selection and read_conversation are included |
| MAJ-010 inputs/environment/masking | Retained with round-2 corrections: byte/NUL/grammar/protected managed layers, per-child injection and complete sensitive-set rebuild |
| MAJ-011 shared resources | Lock order retained; **partial under expanded scope**, R4-MAJ-001 |
| MAJ-012 picker/self-row | Retained: eligibility/nonmembers, hidden identities excluded; FR-111 and BDD-12.12 preserve self-row controls |
| MAJ-013 secret trust | Retained: §6.7/FR-083/084; authenticated recipient-bound consent, bounds, zero-write refusals and security-lead verification |
| MIN-001 browser warm-up | Retained: Default workspace selection and no-manager behavior, BDD-13.11 |
| MIN-002 component mapping | Retained for original surfaces; extend new-scope public mapping, R4-OBS-002 |
| OBS-001 landing bundles | Retained: preparation only; one complete landing, permission split committed with grants |

| Round-2 finding | Current disposition / evidence in the spec |
|---|---|
| R2-MAJ-001 private selection/Stop truth | Retained: FR-064/067/086, first-version versus replacement, scoped Ask selector; new mail failure wording needs R4-MAJ-009 |
| R2-MAJ-002 Submit common commit fact | Retained: same atomic credential publication contains receipt, references-only recovery, FR-087/BDD-6.24 |
| R2-MAJ-003 shared synchronization | Correct config → loop lock order retained; projection/read path **partial after expanded scope**, R4-MAJ-001 |
| R2-MAJ-004 gateway environment | Retained: private values per child only; integration credentials keep existing boot/reload resolution; child inheritance prohibited |
| R2-MAJ-005 masking rebuild | Retained: FR-089 and tests 89/90 cover complete set, all kinds/versions/states and serialized rebuilds |
| R2-MAJ-006 unsafe independent landing | Retained: stages are not landings; one whole feature and Ask boundary with grants |
| R2-MAJ-007 protected environment families | Retained: prefixes and setup/document layers, post-composition injection, real-composer drift test 91 |
| R2-MIN-001 inherited controls | Retained: §9.5 listing-only, shared skill, multi-consumer reload/boot, policy reload and target verbs have owners |
| R2-OBS-001 composite evidence | Retained: §13 separates screen/focus/touch/reduced-motion evidence from kit/static build compliance |

## Verification of round 3 and post-round-3 additions

| Round-3 finding | Actual current disposition |
|---|---|
| R3-MAJ-001 shared main/control conflict | Closed at definition level by Q-R3-1 A: §6.9/FR-209 and shared-main BDD-19.1/10; founder's choice, not the grill's former recommendation |
| R3-MAJ-002 pre-ladder capture/return | Adaptation owned by §6.10/FR-210/211 and real test 140; **partial end-to-end** because adapter identity/prefilter and main-availability handoff still needs R4-MAJ-002/003 |
| R3-MAJ-003 linked histories/key | Closed by Q-R3-2 B: canonical tuple/D8/FR-147 and test 153; links recognize, never merge histories |
| R3-MAJ-004 early steering | Definition retained: one pre-control/rewrite check, canonical sender, duplicates/revocation, test 142. Adapter entry/matching coverage extends this in R4-MAJ-002 |
| R3-MAJ-005 revision fields | Existing new People/mode fields added; **partial after Open to anyone and EM-3**, R4-MAJ-001 |
| R3-MAJ-006 room state/admission/reply | State machine, no live-turn steering and no room-owner wake now explicit; **partial wire/state/UI correlation**, R4-MAJ-005 |
| R3-MAJ-007 partial retry | Stable IDs and deduplication intent added; **receiver commit versus room receipt still open**, R4-MAJ-004 |
| R3-MAJ-008 Pause fence | Retained at definition level: durable generation, approval/admission/dequeue ordering, after-pause survives Resume, member/delete races; tests 145/BDD-17.28/29. No runtime ordering proof claimed |
| R3-MAJ-009 memory scope | Retained: immutable workspace/agent operation scope, all workspace-owned paths inventoried, concurrency test 146. **Private remember notes intentionally remain agent-wide under FQ-R3-B** |
| R3-MAJ-010 lifetime-once recap | Retained correction: progress-keyed repeated idle/skip/out-of-order rule and test 147; existing lifetime gates are implementation work, not a new founder question |
| R3-MAJ-011 mentions | Stable picker IDs authoritative and typed duplicate/full-name/reserved-all rules added in §6.13/D7/155; K25/REST representation remains part of R4-MAJ-005 |
| R3-MAJ-012 cost oracle | Formula and mixed-input/unpriced fixture added; residual older oracles R4-MIN-002 |
| R3-MAJ-013 wire carriers | Room entity, visibility PATCH, team-mute exception and main live/replay metadata added; **partial room map**, R4-MAJ-005; newly added mail map is R4-MAJ-007 |
| R3-MIN-001 stale handoff | Partly swept; current remaining text R4-MIN-001/002 |
| R3-MIN-002 exact Omnipus tools | Closed at definition level: read_conversation is in the exact set and policy table |
| R3-OBS-001 inspector | Adopted: K30 removed; no new awareness inspector; existing audit consumer retained |
| R3-OBS-002 composite keyboard | Earlier additions retained in §6.8/BDD-12.16; extend newly added email/checkbox scope under R4-MAJ-010/R4-OBS-002 |

| Added/answered scope | Fidelity check |
|---|---|
| EM-1/EM-2 | Email remains an agent tool, no routing tabs/awareness/reachability rows; Chat channels and Agent tools categories present. Wireframe's old email channel/task-drainer descriptions are not current authority |
| Per-type Configure | Four chat tabs and per-type Setup/identifiers/group options specified; real adapter identity and early filtering still need R4-MAJ-002. No extra channel SDK or plugin framework required |
| FQ-R3-A | Default closed/empty admits nobody, explicit Open to anyone, individual command/resume rows all present; complete revision/testing consequences need R4-MAJ-001 |
| FQ-R3-B | Deliberate private memory agent-wide, automatic digests/shared/carry-forward workspace-scoped. No private-note isolation finding is issued |
| Q-R3-1 A / Q-R3-2 B | Shared-main warning/control and connection-isolated histories explicitly retained |
| EM-3 | T1/T2/T6/T8 adopted; nine tools enumerated; T3/T4/T5/T7 deferred; identity/contracts/setup/UI obligations need R4-MAJ-006–010 |
| EQ-1 A | Omnipus user-driven connect_mailbox remains; other agents cannot create. Already answered, not a new interview question |

## Contract completeness — K1–K44

These are shape/coverage assessments, not claims that future schemas already exist. Backend-lead alone edits/regenerates contracts. The current source contracts are a baseline, not expected to implement this draft already.

| Entry | Assessment |
|---|---|
| K1 | Acting/root/owner identity and bounded value-free request display retained; Settings-origin mailbox request needs R4-MAJ-008 |
| K2 | Five recipient kinds/private tuple retained; mailbox target ID must be frozen before entry/test |
| K3 | Terminal value-free notification retained; readable receipt-backed state, not frame arrival, owns success |
| K4 | Bounded never-null pending/recent terminal snapshot entries retained |
| K5 | Submit/state read and no plaintext replay retained; initiation for no-model Settings path missing |
| K6 | Stored/selected/superseded names and consumers retained; ordinary scope/Omnipus listing rules still apply |
| K7 | Omnipus helper root/acting-name extension retained; room pre-admission Ask correlation/location needs explicit mapping |
| K8 | Ordinary new-session agent requirement and workflow target metadata retained; no anonymous room model runner |
| K9 | Server kickoff to existing Omnipus main and client refusal retained |
| K10 | QR request/session correlation retained; per-platform teardown/reconnect and real consumer acceptance remain required |
| K11 | Agent default removal retained; consumers must be complete before the one landing |
| K12 | Default removal and Omnipus per-agent Ask refusal retained |
| K13 | Tools-write refusal retained; no resolution bypass |
| K14 | Required bound write versus truthful invalid legacy read retained; selected-agent ordering needs R4-MAJ-003 |
| K15 | Instance-specific routing degradation retained; clearing routing failure must not clear credential/transport failure |
| K16 | Ordinary explicit agent and Omnipus singleton retained |
| K17 | Old default config ignore/warning retained, not a migration |
| K18 | Instance-person/group/guild/team/account shape named; canonical adapter mapping still needed |
| K19 | Main/per-person and default-false Open to anyone named; revision includes the checkbox, R4-MAJ-001 |
| K20 | Stranger status named; count means events in BDD-16.3, not distinct people. Specify retention/rate-bookkeeping bounds and reset behavior during correction |
| K21 | Derived reachability endpoint named; include open/closed/group admission truth without a second stored allow list |
| K22 | Instance match config adaptation/global session-scope retirement retained; thread/sender identities need the explicit mapping |
| K23 | Correct separate Room entity and derived attention/cost named; one authoritative room representation |
| K24 | User-only create/membership/delete and team mute-only exception present; preserve two-agent/read-only lifecycle |
| K25 | Stable IDs defined only on ordinary MessageFrame; room POST body and authoritative picker semantics need R4-MAJ-005 |
| K26 | Room entries, client message ID and snapshot named; initial/live/reconnect operation and body map incomplete |
| K27 | Required failure/refusal/interruption/unknown states missing from enum |
| K28 | Stopped conversation status named; key it to the §6.9 canonical session, not an unkeyed connection aggregate |
| K29 | Visibility PATCH now has an explicit request; preserve reload and History access |
| K30 | Intentionally removed inspector; do not restore |
| K31 | Intentionally removed self hand-off; do not restore |
| K32 | Individual steering/default false named; reconcile `steering` versus later `can_use_commands` at schema ownership, not two settings |
| K33 | Main request/answer room refs now cover live/history/replay carriers; actual generated parser/reconnect proof remains needed |
| K34 | Room-origin ledger tag retained; receiver delivery and answer occurrence reconciliation need R4-MAJ-004 |
| K35 | Stale Session(room) entry contradicts K23; fold fields into Room/carriers, R4-MAJ-005 |
| K36 | One People-row shape named; reconcile earlier agent_id/steering and later talks_to/can_use_commands and canonical typed IDs |
| K37 | Requests read/Add-to-People named; specify bounded recent list and failure/empty state; real adapter refusals must reach it |
| K38 | Categories named; explicitly identify channel list versus mailbox list carriers so no hand-written unified connector type appears |
| K39 | Bound read/revision named; require the write precondition on participating generated writes, including new fields |
| K40 | Mailbox config/response/sidecar identity named; full runtime ownership and message-ref binding incomplete |
| K41 | New list/create/item routes and cleanup ID named; request schemas, selector/ID validation and lost-create-response behavior need complete shapes |
| K42 | Test failure classes named; secure reference input, exact tested candidate and save truth need R4-MAJ-008/009 |
| K43 | Template endpoint named; safe links/defaults, empty/unavailable state and wizard journey need acceptance |
| K44 | Workspace mail query selector named; global body-carried operations and summaries also need selectors/identity |
| Across entries | Expand WU-0 from K1–K39; keep generated Go/TypeScript/Zod/inbound mirrors, unconditional semantic validation, real outbound parsing/catch-up and the inline-union rule from **Discriminated-union create contract for agents** |

## Room reuse, routing and memory assessment

```text
Authenticated channel input
  -> canonical person/group admission + entitlement
  -> instance rule, else main; validate selected agent
  -> decided session key/control scope
  -> capture (source connection separate from responder)
  -> existing turn path
  -> final current route/admission check -> original connection

User room send / request-bound agent answer
  -> Room record + stable per-member progress                 [new]
  -> existing receiver policy/Ask + ledger                    [adapt]
  -> member's own main FIFO; not a live-turn steer            [adapt]
  -> consumed work / own-main approvals and questions         [reuse]
  -> captured room reply, collapsed main mirror, mentions     [adapt]
     Pause generation fences admission/dequeue/further sends  [new fence]
```

| Mechanism | Conclusion |
|---|---|
| Fan-out and busy members | Main sessions, existing receiver admission and one FIFO are retained. No hidden per-member room working session or self hand-off is required |
| Held/refused/failure | §6.11 now distinguishes pre-capture Ask, unavailable stopped receiver, failures and interruption; wire/UI recovery must represent them |
| Ledger/replies | Preserve trusted source/responder correlation. Stable room IDs must actually bridge the receiver-publication cut; mere room-record deduplication does not do so |
| Pause/after-pause | Durable generation and no later mention forwarding retain the founder's rule. Running main work remains untouched, even after Resume |
| Collapsed main entries | Main request and one answer mirror are required, with identical metadata through live/history/replay. A chat-only mirror must not duplicate the model's own answer context |
| Unlimited exchanges | R-A4 remains accepted; no automatic loop cap proposed. Manual Pause and honest allocated cost are the retained controls |
| Routing | Instance scope, destination-before-capture, separate return ownership and entitlement are now explicit. Real adapters and selected-main availability must match that sequence |
| Awareness | Non-room list/read-only lookup/reused recap outcomes are retained. Immutable scope, progress-keyed recaps and no agent-wide fallback for automatic data now have owners |
| Private notes | Agent-wide by deliberate founder decision; not classified as an awareness leak. Same-workspace automatic/shared/retrospective/carry-forward sentinels still need concurrency/cache/fallback coverage |

## Test coverage assessment — plan only

| Category | Required falsifiable addition/refinement |
|---|---|
| Adapter admission/routing | Pre-filter group-only admission, direct stranger reply/status, aliases/canonical duplicates, group/thread keys, native slash entry and dead-main/valid-rule target through real intake/egress |
| Steering/resume | Both modes, individual row with commands off but resume right, group-only/open participant without resume right, flag revocation, aliases and UI-only refusals before rewrite/control |
| Room crash recovery | Receiver capture/append before room admission receipt; stable request ID and zero second model start; answer/mirror/fan-out cuts and reconnect convergence |
| Room failures/contracts | Every state parsed through actual generated outbound union; snapshot/attach/cursor recovery; held admission approval location/reload/Pause denial |
| Shared revisions | Checkbox-only edit, routing-only edit, signature-only edit and sibling mailbox creation/deletion under Settings/tool contention |
| All nine email tools | One/many configured mailboxes, explicit ID/address, unknown/ambiguous/unavailable choice, actual selected From/target, no cross-workspace fallback; use valid retained tool arguments |
| Logical mailbox isolation | Same pair/account/folder UID epoch, simultaneous reads/watchers, late responses, selective rotation/disable/delete/cleanup, real pool/budget/cache/preview owners |
| Secure setup/testing | No active model/agent turn in Settings; reference-only Test input; failed test leaves credential stored but no mailbox; edited/stale test result cannot authorize Save; no probe email |
| New UI composites | Empty/error/partial/testing/uncertain state, dirty switch, full-screen/back context, keyboard wizard/switcher/checkbox, focus and announcements, touch/reflow/reduced motion |
| Existing memory corrections | Populated same-agent concurrent workspaces through real tools/cache/retro/fallback; private notes positive carry-over control; repeated idle/new progress and out-of-order recap |
| Cost and bounds | Formula-derived mixed inputs, unpriced/failed/interrupted spend, duplicates; 500/501 People rows, 20/21 room-context entries, byte/Unicode boundaries, 24-hour reply boundary and restart bookkeeping |
| Execution authority | CI, red-before-green, qa-lead CHECK and exact-commit tester plus independent validator still required; inventory/source/prompt checks cannot substitute |

**False-green controls:** the static reference instrument exercised full and abbreviated ranges, a known-present scenario and a deliberately absent one. It proves references resolve only. A resolver-only test misses early adapter rejection; a constructed transport map misses pool/watch/cache identity; a malformed unsupported `read_message` argument can refuse for the wrong reason; an append counter after a clean admission misses the receipt crash cut. Use independent IDs/capture/model-start/From assertions and passing controls. No tests, mutations or builds were run in this review.

## STRIDE threat summary

STRIDE groups identity spoofing, state tampering, missing action attribution, disclosure, resource exhaustion and privilege escalation. This table is a design audit, not penetration testing.

| Flow | Remaining concern | Disposition |
|---|---|---|
| Person/connection admission | Alias/group/thread mismatch and early silent rejection | R4-MAJ-002/003; authenticated identities and loud selected-route failure retained |
| Shared configuration | Stale plan reopens a closed connection or overwrites mailbox meaning | R4-MAJ-001; one existing write boundary, complete revision projection |
| Rooms | Duplicate work/spend and dishonest failure/reconnect state | R4-MAJ-004/005; captured authority, generation and user-only membership preserved |
| Mailboxes | Shared pair identity or ambiguous reference opens wrong sibling data | R4-MAJ-006/007; extend logical identity within existing owners and physical caps |
| Email setup | Improvised secret entry or falsely reported no-save/tested-save | R4-MAJ-008/009; durable consent and destination/reference binding preserved |
| Workspace awareness | Automatic/retro/cache/fallback leakage during concurrent turns | Previously owned corrections retained; deliberate private carry-over is allowed |
| Unlimited replies | Unbounded conversational spend | Founder-accepted R-A4; no limit requested; Pause/allocated cost must be truthful |
| Omnipus change surface | Instruction-only Go, file-to-web and external executable selection risks | Founder-accepted R-A1–R-A3; no new code Go gate or permission redesign proposed |

## Reachability, documentation, work units and next action

| Area | Assessment / required correction |
|---|---|
| Eight proposed new tools | Exact set includes request_secret, list_secrets, select_secret_version, remove_secret, connect_channel, connect_mailbox, set_agent_permissions and read_conversation. All have intended catalogue/ceiling/role/custom/Auto/executable boot/reload wiring. Current source absence is expected before implementation—not an unreachable-delivery finding |
| Screens | Omnipus entry, Team/editor/System agents, Configure/People/Requests, hidden conversations, rooms and Mail are named. Missing operational bridges are real adapter admission, room attach/live recovery and Settings-origin secure request |
| Mailbox lookups | Name the existing tool extended with mailbox choice/revision reads for Omnipus; do not rely on reading protected config or an ungranted guessed list_mailboxes tool |
| User documentation | Specific TODOs remain. Extend connector/security/mail/tool/UI pages with Open to anyone, exact key/control scope, logical mailbox selectors/lifecycle, retained unused secrets after failed Test and wizard/switch recovery. Memory docs must distinguish deliberate agent-wide private notes from workspace-owned automatic data. Fix bare room Stop wording |
| Work-unit scope | WU-14/15/16/17/18 correctly Feature. Extend WU-0 to all mail/room carriers; WU-14 owns adapter identity/prefilters; WU-15 owns durable receiver/reply reconciliation; WU-18 owns all runtime mail identities and UI states, not just config/sidecar/parameter edits |
| Delivery order | S1–S4 are preparation stages, not independent landings. WU-18 is in S4; live W15 and Mail acceptance must wait for it. Keep one complete landing, same-change docs/docs-verifier, five reviewers and exact-commit hands-on UAT. No incomplete protected/reachable stage may land |
| #1277 | Separate prerequisite remains; in-use secret removal refuses until it is available. No side fix is made here |

**Code correct and tested:** not claimed — specification/source review only.

**Reachable by a user or agent:** intended paths assessed; not demonstrated at runtime.

## Questions for the founder — NEW only

**None.** All founder answers supplied for this round, including EQ-1 A, are decided. The findings are technical contradictions and missing integration/test handoffs, not new choices about accepted risks or product scope. §17's EQ-1 must be marked answered rather than interviewed again. No context/options/recommendation question is fabricated to fill this section.

## Verdict rationale and process

**REVISE** follows from ten MAJOR specification defects. Correct the explicit shared-resource/routing/room/mail/setup/UI handoffs and the two stale-text issues before implementation planning. The retained earlier protections do not justify claiming all round-3 findings closed; the tables above identify their residuals without reopening the decided designs.

| Next action | Owner |
|---|---|
| Review dispositions; there are no new founder-question prompts to interview | team-lead / founder |
| Apply this round's corrections to the whole spec and its contract/test/docs/work-unit handoffs | spec author; architect for contract-shape decisions as authorized |
| Preserve EQ-1 A, private agent-wide notes, main-mode steering, connection-isolated histories and R-A1–R-A4 | spec author / security-lead review |
| After the round-4 fix, perform the **already founder-commissioned fresh independent Opus review**, then its founder interview/fix and approval | team-lead; this reviewer does not dispatch or add an unapproved review |
| Plan/implement only after that commissioned flow and approval, with the existing feature gates and exact-commit acceptance | team-lead / implementing leads |

This is the **last GPT grill round under the four-round exception**. It is neither implementation approval nor permission to push or land.

skills: omnipus-shared-rules, grill-spec, gitnexus-exploring, ux-heuristics-review, jev-use:jev-use, github-cli, commit-messages

## Evidence table — grill-osa-spec-r4

| Claim | Evidence | Certainty |
|---|---|---|
| E01 — Whole spec and every previous finding read | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec.md`::§0–20, all FR/BDD/K/test/trace/work-unit tables; previous reports `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec-review.md`::all findings; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec-review-round2.md`::all findings; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/specs/omnipus-system-agent-spec-review-round3.md`::complete report | Verified, high confidence; definition review only |
| E02 — Latest founder decisions, including review sequence and EQ-1 A | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-system-agent-decisions-20261012.md`::D1–D27; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/omnipus-adr-founder-answers-20261012.md`::all sections, Q-R3-1/2, FQ-R3-A/B, EM-1/2/3, EQ-1, Extra review; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/channel-person-routing-decisions-20261013.md`::R1–R5/PR-1–4 | Verified, high confidence |
| E03 — Architectural hierarchy, ordinary control and remaining policy boundaries | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md`::complete ADR/OSA-14–16/EM-3/dated notes; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/AS-IS-architecture.md`::complete dated walkthrough; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/plugin-extensibility-assessment.md`::complete assessment; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::D6–D9; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-077-two-layer-tool-policy-remove-fail-closed-backfill.md`::D1/D3–D6 | Verified reads, high confidence; current code wins over dated claims |
| E04 — Current main availability/capture precedes the ladder and return ownership is separate | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/connector_intake.go`::boundConnectorPair/admitBoundConnectorInputCtx; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop_inbound.go`::resolveMessageRoute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/routing/route.go`::ResolveRoute/filterBindings/findPeerMatch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/channels/return_route.go`::checkReturnRoute | Verified source, high confidence; future wrong-route scenario Inferred |
| E05 — Early adapters filter before central intake, with distinct typed identities | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/channels/telegram/telegram.go`::handleMessage; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/channels/discord/discord.go`::handleMessage; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/channels/slack/slack.go`::handleMessageEvent/handleAppMention/handleSlashCommand; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/channels/base.go`::IsAllowedSender/HandleMessage/ShouldRespondInGroup | Verified source, high confidence |
| E06 — Existing command gating is not a refusal automatically | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/commands/registry.go`::NewRegistry/Lookup; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/commands/executor.go`::Execute/executeDefinition; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/commands/cmd_reload.go`::configCommand (reload alias); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/commands/cmd_model.go`::modelHandler. Excluded/unknown/no-handler commands can pass through today; the new explicit refusal must cover canonical names and aliases | Verified source, high confidence |
| E07 — Receiver capture, UUID, append, wake and Ask are separate steps | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/address_router.go`::RequestAdmission/AdmitRequest/admitRequest/recheckReceiverAdmission/requestReceiverApproval/replyIntoConversation/DiscardRequest; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/session_worker.go`::enqueueOrRetry/worker inbox and steering ownership; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/loop_policy.go`::CheckGrantOrRequestApproval | Verified source, high confidence; crash-cut consequence Inferred |
| E08 — Ledger has durable capture/discard/delivery, not an answer-occurrence receipt | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/addressing/ledger.go`::Put/Get/Resolve/Discard/MarkDelivered; E07::replyIntoConversation mints answer ID before append and settles after return | Verified source, high confidence; no crash run |
| E09 — Actual publication/attach and approval projections use session-owned carriers | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/address_deps.go`::PublishGuestReply/PublishUserEntry; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/websocket_replay.go`::handleAttachSessionWithAck/resolveSession/sendSnapshot; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/ws_session_hub.go`::sessionHub/hubRegistry; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/ws_tool_approval.go`::approvalWorkspaceID/broadcastToolApprovalRequired; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/store/toolApproval.ts`::isApprovalInScope | Verified source, high confidence; no claim a current global approval is invisible |
| E10 — Current mail transport/choice/ref boundaries are pair-shaped | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/email_tools.go`::registerEmailToolsForAgent/MailGenerationResolver; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools/email.go`::EmailTransports.resolve/ReadMessageTool.Parameters/Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/email/message_ref.go`::MessageRefClaims/IssueMessageRef; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/email/view.go`::parseMailRef/refEpochMismatch | Verified source, high confidence; collision consequence Inferred |
| E11 — Runtime credential/pool/watch/sidecar identity and retained mail design | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/mail_runtime.go`::splitMailPairKey/resolveCredentials/mailScope/mailGenerationForPair/wireMailSessionSource; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/config/mail_identity.go`::MailPairIdentity/mailPairIdentityKey/MailPairIdentityPath; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/email/watcher_set.go`::CycleAll; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/email/watcher.go`::keyFor; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-033-per-pair-mailboxes.md`::Decision; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-20261001-mail-live-access-pooling-folder-discovery-and-cache.md`::P1.1/P1.2/P1.3/P1.4 | Verified reads, high confidence; new mailbox lifecycle not executed |
| E12 — Current mail contracts lack sibling selector/reference provenance | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/Mailbox.yaml`::properties; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/MailboxNewMailSummary.yaml`::required/agent_id; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/MailHtmlPreviewTokenRequest.yaml`::required/properties; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/MailAttachmentPreviewRequest.yaml`::required/properties; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/MailMessageSummary.yaml`::UID/folder fields; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/MailAttachmentSaveRequest.yaml`::route-owned source/body-only operation token; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/components/schemas/MailReplyContextRequest.yaml`::route-owned source/body-only mode; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/asyncapi.yaml`::MessageFrame/AttachSessionFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/contracts/openapi.yaml`::mail operation/schema references; **Discriminated-union create contract for agents** at `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/architecture/ADR-034-agent-create-discriminated-union.md`::Decision | Verified reads, high confidence; no contract generation/validation run |
| E13 — Human setup currently uses a write-only form, not agent request initiation; mailbox lookup is unowned | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/connectors/EmailMailboxPanel.tsx`::MailboxFormState/password input/save mutation; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/gateway/rest_mailbox.go`::setAgentMailbox/validateAccessAndRequest/storePassword/persistConfig; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/agent_read.go`::AgentGetTool.Execute; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/sysagent/tools/channel.go`::ChannelListTool.Execute; E01::§6.5/6.7/K5/K42 | Verified source/spec, high confidence |
| E14 — Existing user promises and mailbox UI identity read | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/mail.md`::complete page; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/connectors.md`::complete page; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/memory.md`::complete page; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/tools.md`::complete page; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/security.md`::tool policy/God Mode/saved secrets/mail-cache promises; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/workspaces/mail/MailPanel.tsx`::MailPanel agentId/selectedMailbox/selectedRow/presence/query keys; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/lib/api/mail.ts`::read options/mailUidRef/fetchMailFolders | Verified reads, high confidence; actual new UI quality Unknown |
| E15 — Screen authority and public kit inspected | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/navigation-menu-wireframe/requirements.md`::R34/R35; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-ux/channel-routing/README.md`::categories/per-type table/Configure; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/design/design-system-definition.md`::D4–D8; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/brand/brand-guidelines.md`::complete guide; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/src/components/ui/CLAUDE.md`::controls/publication; inline JSON catalogue inspection, exit 0 → entries=62, public Checkbox/Field/Command/Dialog/Sheet/Switch/Tabs/DisclosureRow, FormError publicExports=[]; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/scripts/design-system/package-boundary.mjs`::catalogSourceGraph/publishedPackageAllowlist | Verified, high confidence; no design-system gate/browser run |
| E16 — Current memory risk is explicitly owned, not claimed fixed in production | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/memory.go`::MemoryStore/AppendRetro/WriteLastSession/ReadLastSession/GetMemoryContext; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/memory_adapter.go`::SetWorkspaceID/SearchEntriesInRoom/SearchRetros; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/agent/session_end.go`::CloseSession; E01::§6.12/FR-218–220/BDD-20.8–10 | Verified source/spec, high confidence; isolation implementation Unknown |
| E17 — Issues traced, not accepted from titles | `gh issue view <1109,892,955,1068,1197,1277> --json number,title,state,body,url`, each exit 0 → OPEN; #1109 T1/T2/T6/T8 and never-turn decision, #892 resume, #955 channel controls, #1068 group intent, #1197 recap scope, #1277 separate degradation prerequisite. Later founder answers override older issue sketches | Verified, high confidence |
| E18 — Controlled inventory is reference completeness only | Inline `python3 -I` inventory, exit 0 → `instrument_controls=6/6`; `FR_definitions=187; trace_rows=187; missing_FR=[]`; `BDD_definitions=234; planned_test_rows=168; test_numbers=1..168`; no missing/unknown scenario references; `active_contract_rows=42; missing_in_1..44=[30,31]`. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/docs/internal/false-green-patterns.md`::complete guidance read before trusting the instrument | Verified static analysis, high confidence; not test execution |
| E19 — Reachability instrument has a known-present control | `rg -l AskUserQuestion` over `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/coreagent`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/config`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/pkg/tools`, exit 0 → seed/default/catalogue/executable matches; same roots eight proposed names, exit 1 → expected pre-implementation absence. No implemented reachability PASS | Verified controlled search, high confidence |
| E20 — Graph/typed triage limits reported honestly | `gitnexus status`, exit 0 → Repository not indexed; no GitNexus MCP exposed. `gitnexus detect-changes --scope staged --branch work/omnipus-agent-design-20261012`, exit 1 → branch not indexed; sibling index 3,586 commits behind, rejected. Scoped Git diff is the document-only fallback; no graph impact claim. Five-question `jev-use judge` batch, exit 3 → checkbox revision MAJOR, room enum MAJOR, re-ask EQ-1 NO, BaseChannel-only adapter fix NO; secret-save severity escalated at confidence 0.18. Reviewer took that uncertainty back and verified the contradiction against durable Submit; R4-MAJ-009 describes the permitted wrong behavior. Jev is triage, not evidence of the source claims | Verified instrument output; severity reasoning Inferred, high confidence |
| E21 — Mandatory gates, scope and human identity checked | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-agent-design/CLAUDE.md`::Definition of Done/Hard Constraints #6/#8/retired surfaces/human authorship. `git branch --show-current`, exit 0 → work/omnipus-agent-design-20261012; `git rev-parse HEAD`, exit 0 → reviewed SHA above; initial status/staged diff empty. `git config user.email`, exit 0 → 10800669+daniel-piatkowski-ai@users.noreply.github.com; author/committer identifiers both Daniel Piatkowski with that email. Active Git hooks inspected → none | Verified, high confidence |
| **Self-check** | Complete staged report diff re-read from `/Users/danielpiatkowski/.claude/projects/-Users-danielpiatkowski-AI-Agent-Workspace-omnipus-wt-omnipus-agent-design/76a389d8-c285-4857-bc8a-a2ca749c0942/tool-results/bnf966bf2.txt`; narrowed mail-path correction and final evidence-only edits also reviewed. Inline `python3 -I` static report audit, exit 0 → `report_static_audit=PASS; counts=0/10/2/2; lenses=12; contract_rows=44_including_removed_30_31; r1=17; r2=9; r3=17; new_founder_questions=0; input_hashes_unchanged=true; human_email=true; scope=report_only`. Every finding's scenario/evidence/certainty/recommendation, decided risks, absolute paths and no runtime claim checked against the task. Staged scope/whitespace/identity/fingerprints rechecked before commit; final author/committer/scope/no-trailer receipt accompanies dispatch. | Verified report/source self-check, high confidence; runtime acceptance Unknown |
