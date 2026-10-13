# WU-10 SPA RED plan — l1-ui-red-wu10

## Outcome and authority

Pin the approved observable Team, Add-agent, System-agents, singleton entry and workspace-entry behavior before frontend implementation. Test files and test support only; no production edits. Base: d701bcbd05bb64ce004f076f03ebf29b01675441.

| Authority | Sections read / used |
|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/docs/internal/specs/omnipus-system-agent-spec.md | Approved; US-1 AS-4; US-4 AS-1; US-5 AS-2/6; US-10 AS-2/3/5; US-12 AS-1/2/3/4/6/7/8; sections 6.3, 6.8, 8 (BDD-4.1, 5.2, 5.9, 10.2/3/6, 12.1–6, 12.10–14), 9.2 tests 26–29/52/54/82, 12 FR-100–112 trace rows, 14, 15 WU-10/WU-11/WU-20 |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/docs/internal/architecture/ADR-20261011-omnipus-system-agent.md | OSA-1.3, OSA-10 U1/U3/U6/U8/U9, OSA-11; FQ-9 A |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/PLAN-omnipus-L1-v2-20261013.md | Sections 7 and 9 amendments 3/4; System agents and Team explicitly belong to WU-10; replacements precede WU-11 removals |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/L1-AMENDMENTS-20261013.md | Binding precedence; section 1 approval panel belongs to WU-8, section 5 additive contracts available to WU-10 |
| https://github.com/elicify-ai/omnipus/issues/1066 | Required behavior 1–5; acceptance criteria: per-question confirmation, arrows, keyboard-only multi-select advance, final submit, equivalent pointer path |
| https://github.com/elicify-ai/omnipus/issues/1076 | Expected: isolate invalid agent row, keep healthy roster usable; identify id/field in a truthful diagnostic, no version-mismatch advice |

## Boundary

Real React components, catalogued controls, TanStack Query, generated schemas and stores. Network functions may be mocked at the API boundary for component tests; roster resilience uses the real HTTP wrapper and generated validator with fetch replaced. WebSocket connection.send is the transport boundary; no store action, entry resolver, selection hook, graph model, or editor is mocked. Real workspace context is used. Router navigation is a recording environment adapter, not a real browser route check. Graph-only geometry is a jsdom shim; pointer drag, rendered graph layout and persistent self-row editing require browser acceptance.

System agents' new component path is explicitly named by spec section 9.2 test 29: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/omnipus-l1-ui-red/src/components/settings/SystemAgentsSection.tsx, named export SystemAgentsSection, no required props (existing *Section convention). If it is absent, tests fail loudly with BLOCKED instead of failing collection or being skipped. The separate SettingsScreen navigation case pins actual reachability without mocking that section. Existing components retain their existing props. No new wire type is hand-written.

## Cases and independent oracles

| Area / inputs | Required observation | Source | Planned failure / mutation |
|---|---|---|---|
| Team Mia + native worker + external worker | List initially selected; name/avatar/kind/model/other-workspaces/actions; workers separate | FR-102, BDD-12.3, OSA-10 U6 | Graph-only default; omit model or partition |
| Default team Omnipus + Mia | Omnipus absent from list; switch still reaches graph | US-1 AS-4, FR-111 | Do not filter hidden Omnipus |
| True empty Team | No agents yet; Set up a team and Add agent | section 6.3, BDD-5.2 | Count implicit Judge as a real member |
| Team first failure / refresh failure | Retry, not an empty offer; cached cards remain with may-be-out-of-date hint | FR-112, BDD-5.9/12.13 | Coerce failure to [] or erase cached list |
| Team Add agent | Create with Omnipus and Add an existing agent; W1/W18 target Sales while singleton main stays anchored in Default and Sales stays active | FR-103, US-4 AS-1, BDD-4.1/5.2, OSA-11, K8 | Open old create form; target Default; mint another main |
| Picker orphan + already-member + Omnipus + Judge + Plan Supervisor | Only eligible nonmembers, orphan marked not in any workspace; keyboard selection | FR-104, BDD-12.4/12.14 | Include Omnipus; omit orphan hint |
| Keyboard Team | Focusable Add/menu choices/list–graph/card actions/editor entry; no hover prerequisite | FR-109, BDD-12.10/12.14 | Hide actions until hover; inaccessible toggle |
| System section roster and no working provider | Exactly Judge, Plan Supervisor, Omnipus; real shared editor; Omnipus model and hidden instructions editable without a model turn | FR-106, BDD-12.6, OSA-10 U9 | Remove section; filter needs_model; disable model |
| System loading / failed / stale data / unknown save | State visible; Retry; no false Saved on uncertain write | FR-112, BDD-12.13, section 6.3 | Suppress query/write error |
| Fixed sidebar Omnipus entry from Sales | Above workspace rows; singleton opened by pointer/keyboard without switching workspace; explicit attention/unknown cue | FR-100/101, BDD-12.1/12.14, BDD-10.2/3/6 | Omit entry; use foreign-workspace selection path; unknown becomes off |
| Entry no remembered chat, shuffled sessions, ordered team | First conversation member in core_team order; skip workers and Omnipus | FR-107, BDD-12.11, dataset D4 | Use Ava/newest/lexicographic order |
| Entry valid/deleted pointer, empty/no-conversation team, failed member fetch, pending fetch | Restore valid pointer; deleted -> first conversation member; healthy empty -> empty state; failure Retry with no fallback; unresolved sending blocked | FR-107/112, section 6.3, dataset D4 | Erase pointer on transport failure; attach hidden Omnipus |
| Ask card single and multiple selections | Visible confirm per question; Arrow navigation; Enter confirms/advances; final confirm emits exact complete frame once; no preselected Go | #1066 required behavior 1–5, FR-109 | No Next; click-only advance; submit missing/duplicate selections |
| Agent list one invalid name (101 chars), malformed row, valid name boundary (100 chars) | Valid rows survive; invalid row diagnosed by id/field, not a version mismatch; top-level malformed payload still fails | #1076 Expected; generated Agent schema bounds | Whole-array parsing; disable validation; generic mismatch toast |

## Deliberate gaps and follow-ups

| Gap | Reason / owner |
|---|---|
| WU-11 old-route/manual-form removals | Replacement guard in plan section 9.4; separate unit |
| WU-20 dependency-list/delete-everything semantics | Separate unit per spec section 15; WU-10 pins action reachability only |
| Omnipus header Settings shortcut; actual global chat foreground/composer read-only/pending/reconnect UI | Real chat shell / browser follow-up; component/store receipts do not prove actual routed render |
| Omnipus self-row depth save/remove persists after reload and prevents next helper | Browser + backend integration (BDD-12.12); graph canvas gestures not faithfully observable in jsdom |
| W8 server-initiated offer into existing Omnipus main, W18 report -> Open chat, W1/W18 live execution | Backend kickoff and later workflow skills; exact-commit UAT/real WebSocket acceptance |
| Screen-reader announcements, touch, narrow reflow, reduced motion | Real screen evidence separate from primitive/catalog gates (spec section 13) |
| Secret cards/inline approvals | WU-4/WU-8, not WU-10 RED pack |

## Dispatcher-authorized legacy amendment scope (2026-10-14)

The team-lead added legacy regressions before implementation merge. The product oracle stays the spec, not the implementer's results.

| Group | Old oracle / setup | New oracle / setup; what stays |
|---|---|---|
| Original four entry files | Ava main for absent/deleted/hidden pointers; mocked main-ID seam | FR-107 / BDD-12.11: first conversation member by core_team order; generated member main ID through real seam. Exact pointer, no acknowledgement, Retry, no fallback, immutable owner, queue ordering, visibility and supersession assertions remain |
| Graph-specific Team regressions (WorkspaceTeamTab; TeamPanel load/cached/secondRetry) | Graph is implicitly the initial view | FR-102 / BDD-12.3: explicitly select Graph before the original graph-node/editing/recovery assertions. New tests independently assert List initially; no graph assertion is deleted |
| Picker and disclosure empty state | Every agent is already on this team. | Spec §6.3: No other agents. Membership exclusion/search/add callback and pre-add privilege disclosure assertions remain |
| Sidebar membership regressions | A custom Main named/id Omnipus | US-1 AS-4 / OSA-1.3: use nonreserved ordinary Ray. Missing-main, type filtering, membership and rendered-row assertions are unchanged |
| Session workspace-memory suite | No fetchAgents edge; no named colleague in a missing-main fixture | FR-107: add healthy roster and first colleague Mia with unresolved main; generic descriptor tests are unchanged |
| Partial-list entry | Direct 404 -> Ava's main, mocked seam | Direct 404 -> first colleague Mia's real member main. Direct-lookup, exact restore, no fallback on transport failure and saved-pointer assertions remain |
| API validation regressions | Invalid row crashes entire list / increments whole-response failure count | #1076: malformed top-level container still rejects with schema error/count; the separate new resilience pack asserts healthy rows survive bad rows and diagnostics. Exception is explicitly asserted instead of caught and swallowed |

Binding transitional ruling from team-lead: keep the legacy Admin exclusion/search guards until WU-11; add Omnipus/Judge/Plan Supervisor coverage in the new picker suite instead of removing Admin coverage. The old default-agent deletion guards in the graph regression file belong to WU-1 bundle B1 and remain unchanged in this pack.

Interface handoff (not additional product requirements): System agents Settings tab `system-agents`; Team ViewSwitch radio labels List/Graph; picker optional `workspaces` input supplies orphan labels; singleton navigation goes to `/omnipus`; resolver input becomes `team: {coreTeam, memberConfigs, agents}`. Pure resolver tests dynamically load its unchanged module path so the pre-change signature does not cause TypeScript/collection failure. The real-store entry tests verify the result independently. Normal user message prose is not pinned to the implementer's wording; the generated W1/W18 target metadata is exact.

## Founder-added #215 plan (2026-10-14, after the first two checkpoints)

Authority: GitHub #215 Expected, read with `gh issue view 215` (exit 0), plus the founder's team-lead dispatch explicitly extending it to canonical `create_agent`, `update_agent`, `delete_agent` and workspace team data. The old issue's Ava/system.agent.* names are historical; the test uses the current canonical tools named in the dispatch.

Real boundary: `useChatStore.handleFrame` receives generated `tool_call_start` and `tool_call_result` frames; the real application `queryClient` owns actual cached agents, active/archived workspace lists, the visible workspace's detail and delegation/team data. No query, reducer, helper or invalidation method is mocked/spied. Fixtures seed actual cached records; outcomes are exact cache invalidation flags and unchanged values on failure. A control uses the public QueryClient invalidation operation to prove the observation can detect an invalidation.

| Cases | Expected | Independent source |
|---|---|---|
| create/update/delete success | Actual result reaches the real tool-call bucket; agents and workspace team caches become invalidated, without navigation or an extra agent_created broadcast | #215 Expected; founder-added WU-10 brief |
| create/update/delete error | Actual error result recorded; every seeded query remains not invalidated and data unchanged | founder brief: failed call does not invalidate |
| Mutation success in a helper not foreground | Same global freshness while active session stays unchanged | #215 user-visible immediacy and Omnipus helper workflow |
| Ordinary read-tool success | No mutation-cache invalidation; manual public invalidation then flips the detector (instrument control only) | Scope of #215 is create/update/delete, not arbitrary tool results |

Mutations for independent CHECK: remove one mutation tool from the refresh set; ignore success/error status; omit workspace detail/team prefix; gate refresh on the foreground session. Live follow-up: **#215: new agent appears without navigation**, including Team, sidebar and picker after Omnipus applies W1.

## Execution and gate

Each final-state test file runs serially, with a saved verbose log and raw exit code. Initial instrument-correction runs are retained separately; final receipts never reuse a result from an edited file. No full suite, remote CI, live instance or production data. A passing control is reported separately, never called observed RED. Missing implementation fatals are reported separately from behavioral assertion failures. GREEN, mutation probes and the Proof-of-failability checklist are deferred to an independent CHECK instance (qa-lead RED override). Only the local scanner/type gate may run additionally, if permitted. Final diff scope, authorship, staged files and push are independently checked. GitNexus MCP is unavailable; the test-only Git diff and direct test-helper caller sweeps are the documented fallback, Inferred rather than a graph result.
