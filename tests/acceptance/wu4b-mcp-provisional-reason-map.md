# WU-4b Q4-A exact provisional-create reason translation

**Authority:** chief Q4-A, relayed by backend on 2026-10-16. No keyword matching, reason-substring workaround or expected-value copying from implementation output. This is an explicit product wording ruling after the primary pack.

Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-wu4-qa/pkg/sysagent/tools/wu3_fix_effectful_outcomes_test.go::TestWU3MCPAddOutcomeFollowsLiveStatus.

Exact prefix for every nonworking, secret-free CREATE:

`server saved without credential references; credential setup is not finished if this server needs a secret. `

The trailing space separates the prefix from the original actual reason. This condition depends on missing references, not a guess based on an error keyword. A connected create still has the original working verdict. An attachment never receives this create-time prefix.

| Legacy subcase | Original exact reason | New exact reason |
|---|---|---|
| error | `connection failed: connection refused` | prefix + `connection failed: connection refused` |
| disconnected | `the server is not connected` | prefix + `the server is not connected` |
| reconcile-failed | `live reconciliation failed: boom` | prefix + `live reconciliation failed: boom` |
| unchecked | `not checked: live connection status is unavailable` | prefix + `not checked: live connection status is unavailable` |
| connected | Original working result, no nonworking reason expected | Unchanged |

Every existing outcome, connected status/tool count setup, check_scope, not_tested, changed_fields and durable-entry assertion remains unchanged. No other legacy exact string is altered.

The complementary new attachment row derives from the same ruling BEFORE reading new implementation: with a valid matching supplied reference, nonworking attachment results keep each original exact failure reason above, without the provisional CREATE prefix. The source is /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/l1-wu4-qa/pkg/sysagent/tools/wu4b_mcp_provisional_reason_test.go::TestWU4B_MCPAttachmentNonworkingReasonNeverUsesCreateTimePrefix. Real reference validation/config persistence stay inside the test boundary; runtime status/reconcile alone is the process edge. No production mutation by this author.

Execution status at initial publication was UNEXECUTED. Corrected dispatcher receipts now establish RED on exact QA source2fae8de76dd805535868403e3869fa1a4ff3f77b: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/q4-dispatcher/row1.log shows exactly four missing-prefix equality failures with connected control PASS, named exit1; /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/q4-dispatcher/row2.log shows all four attachment cases fail at the old missing-command/attachment prerequisite, named exit1. This author read both raw logs first-hand. The latter is not decisive proof of the later reason guard. No GREEN claim. The implementing lead runs exact named slices at its functional checkpoint through the shared runner; fresh CHECK verifies GREEN and mutates the relevant branches. Existing primary RED receipts remain unchanged.

| Claim | Evidence | Certainty |
|---|---|---|
| Four old strings translated exactly under Q4-A, no original status/scope/not-tested oracle removed | Original table read, direct symbol/caller search, four exact prefix concatenations and staged diff | Verified source/ruling |
| Attachments explicitly exclude create wording | Independent new four-subcase test plan above; source row asserts exact original reasons with real supplied refs; raw dispatcher row2 exit1 at missing attachment prerequisite | Verified source/RED prerequisite; later reason guard unverified |
| Four create wording changes are observed RED | Raw dispatcher row1 above: four exact expected approved-prefix + original versus actual original-only strings; connected case PASS, named exit1 | Verified (high), dispatcher-run receipt |
| **Self-check** | Re-read old table, approved strings and both raw receipts; unchanged remaining assertions/test bytes; no weakened equality, skip, production edit or inferred GREEN. Exact wording RED distinguished from missing-attachment prerequisite; GREEN/mutation deferred to fresh CHECK | Verified source/RED; GREEN/CHECK Unknown |

skills: omnipus-shared-rules, elicify-test-writing, gitnexus-impact-analysis
