# Browser / user-acceptance rows — NOT EXECUTED

Unit tests do not certify these rows. No live instance was contacted. Team-lead owns provisioning and dispatch; this pack requests no live-instance action.

## Lanes and evidence

| Lane | Work | Tester account | Independent validator account |
|---|---|---|---|
| S | Secret cards + agent editor | One dedicated account, to be provisioned; identity Unknown | A different dedicated account, to be provisioned; identity Unknown |
| A | Inline approvals + elsewhere navigation | One dedicated account, to be provisioned; identity Unknown | A different dedicated account, to be provisioned; identity Unknown |

Never log two tester lanes into the same account: its session cookie has one slot. Validator does not reuse a tester account, browser or session. Multi-tab tests use two tabs of their own lane account, not a second login. Each lane owns disjoint test workspaces/resources. Accounts/resource identities remain Unknown until team-lead provisions them.

Any agent-driven setup/UAT uses the founder-fixed `openrouter` + `deepseek/deepseek-v4.1-flash`. Screenshots must show the native account badge and an explicit QA evidence label `Tester · lane S/A` or `Validator · lane S/A`, with account identity and workspace visible. A QA role label is an evidence annotation, not a claim that the application renders that role.

Per-step screenshot and redacted page snapshot layout (all beneath the workspace):

| Actor | Directory pattern |
|---|---|
| Tester S | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-u23/uat-evidence/lane-S/tester/<row-id>/step-<nn>.png and step-<nn>.md |
| Validator S | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-u23/uat-evidence/lane-S/validator/<row-id>/step-<nn>.png and step-<nn>.md |
| Tester A | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-u23/uat-evidence/lane-A/tester/<row-id>/step-<nn>.png and step-<nn>.md |
| Validator A | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/l1-ui-red-u23/uat-evidence/lane-A/validator/<row-id>/step-<nn>.png and step-<nn>.md |

Do not photograph or save real secret values. Use synthetic sentinels, redact entered values from snapshots and network receipts, retain only request counts/methods/field names and value-free states.

## Campaign rows and validator re-drive

| Row / criterion | Lane | Steps | Expected result | Validator critical path (must re-drive, not reread screenshots) |
|---|---|---|---|---|
| S01 / FR-061, §6.8, §6.2 performance | S | Ask for provider, MCP, channel, mailbox and private secrets in web chat; inspect each at narrow/touch widths | Within 1 second on local gateway; actor/purpose/server destination visible; private owner pair visible; hidden inputs and catalogued field/error/button composition; no overflow | Independently request provider + private card; verify destination/owner, initial focus, narrow reflow and touch actions |
| S02 / FR-062/075/083, BDD-6.2/6.15 | S | Submit synthetic sentinel; capture REST/socket traffic, later chat turn, client storage; inspect real backend non-empty transcript/model requests/logs/audit/resume sinks through the dedicated backend evidence lane | Only authenticated submit carries value; no password step; exactly one POST; later requests and every other sink contain no sentinel; intentional leak control is detected | Own new card and sentinel; re-drive submit and later turn; independently verify all available sinks and leak control. Backend security lead supplies binding/raw-route receipts |
| S03 / FR-074/075/109, BDD-6.5/12.9 | S | Two-field Slack card using Tab/Enter; invalid submit; success/cancel/Stop; navigate away with a value; screen reader enabled | Labels and errors announced; Enter only on last field submits, never reaches composer; terminal announced; focus returns; control values cleared on terminal/navigation/disposal; no automatic replay | Own two-field card; invalid last-field Enter, cancel, Stop and navigation; verify focus, speech and clearing rather than only DOM removal |
| S04 / FR-079, BDD-6.7 | S | Lose submit response and resolved frame; reconnect/reload same browser; separately test absent snapshot request and 404 lookup | Value-free GET finds authoritative state; confirmed submission displays Saved + references; absence/404 never means success; no values requested or sent again automatically | Own lost-response/reconnect run, count POSTs, verify pending/404 control and submitted recovery. Secret snapshot path is not approval B3 |
| S05 / FR-241, §6.3, BDD-7.12 | S | Open private-secret editor; default off; enable/disable; lose PATCH response; fail list refresh; trigger a channel-person turn and an ordinary web turn | Accurate owner/version/state list; availability default off; confirmed Saved only; uncertain mutation not confirmed; cached data marked stale; channel child sees variable only when marked; web turn is positive control | Own pair/secret, re-drive off/on channel turn with a positive execution marker, web-turn control, lost PATCH and failed refresh |
| A01 / R2/R6, FR-092/240/251, BDD-11.2/11.3 | A | Omnipus helper asks while other workspace active; peer-received task-origin message; room-received request; inspect acting and root chats | Exactly one inline panel docked above composer in receiver/root chat; correct actor/origin/room label; Open run targets acting session; no modal in AppShell; no panel in helper/other chat | Own Omnipus helper + room request; visit root, acting chat and same-workspace other chat; click Open run; confirm placement and attribution |
| A02 / R6 safety floor | A | Type in real composer before panel appears; press Enter/Escape; use keyboard/touch to make explicit Approve Once/Deny/Always Allow exact/prefix/Cancel | No focus steal; composer unlocked; Enter/Escape never resolve; only explicit action sends authenticated approval-id POST; previews/segments/scopes remain correct | Own approval, keep real composer focused on arrival, send Enter/Escape, then explicit Deny and prefix grant; verify exact request bodies |
| A03 / R6 timeout/multi-tab | A | Two tabs sharing the lane's existing session; one resolves; duplicate click/late frame; separate approval left to expire for 600 seconds | First click wins; all tabs clear on server resolution; no duplicate grant; no five-minute grace approval; timeout is server deny | Own two tabs, race decision against late update, then timeout control; inspect server resolution and transcript row |
| A04 / R6 elsewhere banner | A | Pending approval in same workspace other chat, no-workspace chat, other workspace and Omnipus root; click Go there | Ambient banner labels and opens exact root, not helper; stays focus-neutral; no approval resolution from banner; disappears when resolved; Omnipus navigation keeps active workspace | Own same-workspace other-chat and Omnipus helper scenarios; click Go there and verify exact session plus workspace preservation |
| A05 / R1/R4/R5 backend integration | A | Start task from Board, Calendar Run now, chat run_task, plan, schedule/heartbeat/Judge; exercise an Auto-settled control | Unattended Ask refused visibly with fixed reason, no card; Auto-settled call runs; human message does not convert root to attended | Independently Board-start task + helper with Ask and Auto controls; verify refusal/no-card and visible blocked report. Full start-route matrix belongs to backend RED |

Approval browser-reload full-card equality/Approve-after-reload is explicitly **B3**, not tested or accepted by this pack. Run its own campaign after that atomic bundle. Raw credential-route reauthentication, trusted request/session/recipient binding, durable exactly-once backend publication, child-process injection/masking and server root ancestry are backend/security evidence requirements, not SPA unit-pass claims.
