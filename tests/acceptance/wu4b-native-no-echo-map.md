# WU-4b Q3-A native no-echo pack — 2026-10-16

**This is an author RED checkpoint, not a GREEN or CHECK verdict.** Exactly configure_provider, add_mcp_server and configure_channel. Source base e6d451be181053f1cfe3fb01874d3b6ab0bc0f62 has provider reference selection implemented; both production native-turn and registry files were verified byte-identical to original9c48, before Q3 changes. Plan/oracles were recorded before test output at /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/WU4B-Q3-NO-ECHO-PLAN.md.

## Source and row map

| Source | Named row | Expected |
|---|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/q3-native-checkout-e6d451be1/pkg/gateway/secret_plaintext_native_red_test.go | `TestWU4B_NativeProviderPlaintextRefusalPrecedesEveryEcho` | allow and Ask: native rejected provider plaintext never reaches an approval, preview log, next model prompt, serialized WS frame or persisted assistant/model-window record; value-free card guidance reaches the next model call |
| Same source | `TestWU4B_NativeMCPPlaintextRefusalPrecedesEveryEcho` | Same rule for plaintext env in the actual MCP tool call |
| Same source | `TestWU4B_NativeChannelPlaintextRefusalPrecedesEveryEcho` | Same rule for plaintext token in the actual channel tool call |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/q3-native-checkout-e6d451be1/pkg/gateway/secret_plaintext_native_fixture_test.go | Real fixture/control instruments | Actual gateway web-chat intake → actual AgentLoop.Run → real tools/registry/config/store/session/WS hub/log sink; fake model/network/approval-UI process edges only |

No production scrubber, hook, API or new wire type is added. The three tools retain their public names. The original direct plaintext test and primary26-row RED pack remain unchanged.

## Why an empty capture cannot pass

| Surface | Positive instrument control / assertion |
|---|---|
| Reachable native turn | Actual completed outbound reply and exactly two model calls; exactly one tool-result refusal for the forbidden call, with request-a-secret guidance and failed-nothing-saved outcome |
| Approval boundary | Real policy resolver must report the requested allow/Ask policy; deterministic UI edge records any incorrectly requested approval and returns denial without hanging; expected count is zero and payload is value-free |
| Serialized live WS frames | Real hub translation/numbering and connection queue, not fabricated ToolExecStart payloads. The unrelated public echo must retain its exact argument map in one actual generated ToolCallStartFrame. A generated PongFrame enqueued AFTER real turn completion is a FIFO drain barrier: no quiet-window timeout can hide a delayed earlier frame |
| Logs | Real file-log sink must contain an explicit public capture control; rejected value count is exactly zero. This pack checks the real file sink; separate console/browser/UAT security verification is not claimed |
| Model prompts | Immutable bytes captured at the actual model process edge, including ToolCall.Arguments (omitted by ordinary json.Marshal because json:"-") and Function.Arguments |
| Durable state | Actual transcript contains the unrelated public control; newly opened UnifiedStore re-reads it. Whole isolated-home regular-file scan checks transcript/archive/model-window/audit/log/config files for the rejected value. The live consumer and exact config/encrypted-store byte snapshots remain unchanged; zero save/reload/reconcile/listing calls |

No test changes a timer or waits a silence interval to infer absence. Timeouts bound failed event/barrier joins only. HOME and OMNIPUS_HOME are temporary; all credential values are synthetic. External network/subprocess execution is replaced only at the process edge. No real user credential file is reachable.

## Honest execution status

**Correction:** the initial dispatcher batch was UNEXECUTED because the new QA echo control did not implement the tools.Tool Scope method. The helper now explicitly returns ScopeGeneral. No compile failure is credited as behavioral RED. Author-side runs were separately refused by the unchanged 4GiB memory guard (exit75); no bypass or full suite. Corrected dispatcher runs on frozen source HEAD62994a9cebc24b23ede27c8ef4dcc347798715c0 now provide genuine RED: all three named rows, both allow/Ask subcases each, fail with actual rejected-value exposure, exit1 per named row. The provider-fixed direct refusal still leaks before/around registry execution; this is not merely a missing MCP/channel reference implementation. Exact selectors above ran as one named gateway process each, CGO_ENABLED=0, tags goolm,stdjson, -p1,-count1,-v through shared slotrun. Raw receipts are /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/q3-dispatcher/q3r2-row1.log, /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/q3-dispatcher/q3r2-row2.log and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/q3-dispatcher/q3r2-row3.log. This author read all three first-hand; /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/q3-dispatcher-readback.log, exit0, records the sink-specific failures and confirms no compile/panic/zero-collection failure. All non-secret capture controls survived. Fresh CHECK, not this author, owns GREEN acceptance, production mutants and Proof-of-failability. A later pinned gateway lint attempt was guard-refused at1,717,665,792 bytes, exit75 UNEXECUTED, retained in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/q3-pinned-gateway-lint.log; do not infer lint success from runtime compilation.

| Claim | Evidence | Certainty |
|---|---|---|
| Approved requirement is tested at actual native entry, not just direct Execute | Chief Q3-A, FR-070/BDD-6.8; plan above; actual gateway/AgentLoop/WS hub fixture; corrected named dispatcher receipts above, each FAIL/exit1 on sink-specific no-echo assertions | Verified requirement/source/RED (high); no GREEN claim |
| Provider-fixed pre-Q3 source pinned | /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/q3-source-check.log: e6d451be181053f1cfe3fb01874d3b6ab0bc0f62; turn/registry production byte comparisons exit0 | Verified (high) |
| Initial instrument failure is not RED; corrected batch is | Initial missing-Scope batch excluded; tests-only correction in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/q3-corrected-source-check.log, exit0; corrected raw three receipts above each show named allow/Ask FAIL, actual leaked value counts and exit1 | Verified (high), dispatcher-run RED |
| Native exposure is real despite provider direct refusal | Provider allow receipt: real serialized WS frame, next model input, saved/reloaded assistant record and logs contain the rejected value; Ask receipt: approval payload/request count is1 instead of0. MCP/channel show the same early exposure and the still-unfixed old writes in allow mode | Verified (high); security fix already authorized by Q3-A |
| **Self-check** | Read all corrected raw receipts and positive capture controls, checked exact source checkpoint/unchanged test bytes, FIFO barrier, json-omitted-argument capture and absent compile/panic/zero-collection errors; readback exit0. No GREEN, lint, independent CHECK or mutation success asserted | Verified source/RED receipts; GREEN/CHECK Unknown |

skills: omnipus-shared-rules, elicify-test-writing, gitnexus-impact-analysis
