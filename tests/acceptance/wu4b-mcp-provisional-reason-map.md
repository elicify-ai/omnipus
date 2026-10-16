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

Execution status at publication: exact Q4 rows UNEXECUTED here; no green claim. The implementing lead runs one exact named slice per process at its functional checkpoint through the guarded shared runner; fresh CHECK verifies GREEN and mutates the relevant branches. Existing primary baseline RED receipts remain unchanged.

| Claim | Evidence | Certainty |
|---|---|---|
| Four old strings translated exactly under Q4-A, no original status/scope/not-tested oracle removed | Original table read, direct symbol/caller search, four exact prefix concatenations and staged diff | Verified source/ruling |
| Attachments explicitly exclude create wording | Independent new four-subcase test plan above; source row asserts exact original reasons with real supplied refs | Verified source; runtime Unknown |
| **Self-check** | Re-read old table and four approved strings; unchanged remaining assertions, tests-only staged scope, gofmt/whitespace check; no weakened equality, skip, production edit or claimed unexecuted run | Verified source; GREEN/mutation deferred to fresh CHECK |

skills: omnipus-shared-rules, elicify-test-writing, gitnexus-impact-analysis
