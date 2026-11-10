# CHECK2 Scope B gap closure — RED lane, tests only

Source audit read in full: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-check2/CHECK2-REPORT.md (Scope B confidence, F2–F6, complete mutation table, legacy ledger and limitations). Healthy control is pinned to **eb050d46e9de413d0326a6fc2c90f8de945a5c2b**; no production mutation is performed by this author. CHECK owns the fresh mutation kills.

## Correction to prior claims

The previous banner oracle `/Jim|UAT-T2/` was weaker than its original known-workspace requirement: it accepted the right actor with the wrong workspace. The prior "no weakening" statement was therefore partial, not a pass. Restore both independent target identities. The earlier store-resolution declaration ledger also omitted a template-literal declaration: there were nine original declarations, not eight. Neither correction deletes a test.

## Independent authorities — frozen before assertions

| Behavior | Oracle source |
|---|---|
| Eager secret clearing, regardless of eventual response | FR-075 plus founder-approved S2 / 380c7976c and current task: values leave inputs/local state at handoff to the one submit, on success or failure. §6.3 submitting is disabled; FR-062 prohibits shared-state/frame/log/cache leaks; no automatic resend |
| Root/acting navigation and workspace preservation | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/L1-AMENDMENTS-20261013.md §1.2 R6: root chat owns approval; Open run keeps the acting-session target; elsewhere notice points to the root. Current user docs promise returning to Sales returns to Sales's own conversation |
| Protected refusal explanation | Same amendment §2 D6: protected path is "refused outright (never prompted, never grantable)"; D7: "God Mode never lifts the protection". Do not encourage approval/retry. Unit 5 is the protected tool-result row, not an approval card |
| Accurate elsewhere identity | R6 / BDD-11.3 add the chat identity without dropping the original known workspace. Actor Jim **and** workspace UAT-T2 must remain independently visible |

Real boundary: rendered components, generated frames/types, real stores, real QueryClient, real transport/validators and in-memory router. Doubles only at fetch/socket/output boundaries. Input handles and every observable sink are held through handoff/refusal. No spies on navigation/store helpers. Unit-5 expected meaning is written in the test from D6/D7, never imported from production.

## Survivor → strengthened/new predicate map; old → new observation

| Surviving predicate | Old observation | Spec-derived strengthening |
|---|---|---|
| B-S2-submit-success | Empty only after eventual successful disposal | Hold response unresolved; field empty immediately at submit handoff; then preserve successful reference/state/disposal assertions |
| B-S2-submit-cancel | Empty only after eventual cancellation | Hold cancel response unresolved; field empty immediately on cancel handoff; cancel body contains no value; eventual cancellation preserved |
| B-S2-submit-failure | Visible 400 message/no text leak, not password value | Hold both field handles; immediate empty plus empty after explicit 400; retry without new input cannot POST; new input is required |
| B-S2-submit-inflight | Disabled button/one POST/cache secrecy | Also assert password control empty before any response while the request is in flight |
| B-S2-refusal-branch | Lost-response clearing did not observe known refusal | 400 and CSRF 403 retain empty controls and no reusable local draft; every existing shared-sink assertion remains |
| B-S2-refusal-inflight | Same selective omission survived in-flight test | Immediate two-field handoff clearing and exact one original values map; repeated actions do not replay |
| B-S2-refusal-lost-control | Selective clear-on-uncertainty still passed existing lost-response control | Add sequence: explicit refusal, require new values, second submit loses response, value-free GET; immediate and post-refusal values remain empty, no automatic resend |
| B3-open-run-navigation | Correct data-session-id without clicking | Preserve attribute/single-card checks; click real Open run under real router and require acting-session route, never root |
| B3-banner-root | Helper absent from cache let wrong resolver fall back to targetChatId | Seed both helper and root with conflicting workspace/agent descriptions; root wins for notice, navigation and attach target |
| B3-banner-workspace | Only active workspace/id were checked | Seed full real remembered descriptor and persisted counterpart; Omnipus navigation preserves both exactly, in addition to root and active-workspace guards |
| B3-banner-label | Actor OR known workspace accepted | Actor AND exact known workspace; singular and plural both guard identity |
| B5-copy | Expected value imported from changed production constant | Preserve producer classification/error/result/ordinary-negative cases; independently require never-grantable, God-Mode-inclusive, no-approval/no-retry meaning and unchanged server text |

No old behavior/assertion is removed except the explicitly coupled production-copy equality, which is replaced by an independent semantic contract. Added field-value checks are not replaced by DOM text checks or eventual-disposal checks. No production copy constant is used as the expected explanation. Mutation predicates will be re-selected by the independent CHECK against the strengthened test names in the hand-back map.

**Approved wording-oracle correction:** the first literal `never grantable` matcher failed at healthy eb because the displayed equivalent is `Nothing can approve this`. D6 specifies meaning, not one sentence. Team-lead approved bounded alternatives (`never grantable` / `nothing can approve` / `cannot be approved`), with the God-Mode clause, original server text and no approval/retry action still required. The local oracle instrument tests three valid formulations plus counterexamples that offer approval/retry or omit God Mode. This is not production mutation; CHECK still owns rendered-mutant kills.

## Cases and controls

| Area | Inputs / ordering | Expected |
|---|---|---|
| Submit/cancel success | Deferred response, one secret, two rapid attempts | Synchronous empty control; exact one body; correct terminal after response |
| Refusal | Two labelled fields; HTTP 400 and CSRF 403 | Both empty before and after refusal; not Saved; one POST; no shared-sink sentinel; no-send on blank or partial retry; fresh two-field input needed |
| Refusal then uncertainty | First submit 400, explicit fresh input, second response lost | No reuse of first values; second body has only fresh values; GET only for recovery; no third automatic POST |
| Open run | Root shown, acting helper differs | Attribute and actual route both name acting helper; queue remains pending and no approval POST |
| Resolver | Helper/root both known but conflicting metadata | Root workspace/name and root session target; not helper's workspace/name |
| Remembered workspace | Nonempty descriptor id/type/title/agentId, persisted using real store | Full in-memory and persisted representation unchanged after opening Omnipus |
| Refusal prose | Shell/file producer texts through real GenericToolCall; result/error path, ordinary failure control | Independent permanent/never-grantable explanation, God Mode included, no approval/retry affordance/advice; original server text preserved |

## Limits

The exact secret implementation's function bodies and its own Unit-2 tests are not read. Public interfaces and written spec/docs supply fixtures. Existing Unit-5 test is absent on this RED branch but present at the pinned feature head: carry its classification/ordinary-failure/server-text assertions unchanged, replacing only the implementation-owned copy oracle. Landing may require resolving that test-only add/add against the feature's original file; do not change production or suppress the conflicting test.

Base missing-implementation RED receipts may be useful, but healthy eb controls are mandatory. Any test that fails there is a candidate actual defect or oracle dispute, not silently called a missing test. No whole-head GREEN, mutation kill, browser heap erasure, gateway delivery, full local suite or UAT acceptance is claimed. Fresh CHECK supplies kills; browser/UAT rows S03, A01, A04 and P01 remain required.
