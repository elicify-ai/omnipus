# Squad X report — issue #771 boot log noise

## Outcome

Expected boot and hot-reload wiring is quiet at WARN level. The plan tools still fail closed while the plan store is absent, and strict `ToolRegistry.Register` collisions still emit the original warning and preserve the incumbent tool.

- Code correct and tested: yes — focused regression tests, the existing fail-closed test, all email wiring tests, guards, and budgets pass.
- Reachable by a user/agent: yes — the changed wiring runs automatically during initial agent-loop construction, config reload, and fast agent upsert.

No gateway wire format changed, so contract regeneration was not required.

## RED receipt

Added these behavioral tests before changing product code:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/x-log-noise/pkg/agent/log_noise_test.go::TestWirePlanToolsForAgent_NilStoreDoesNotWarnPerAgent`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/x-log-noise/pkg/agent/log_noise_test.go::TestRegisterEmailToolsForAgent_ExpectedRewireDoesNotWarn`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/x-log-noise/pkg/agent/log_noise_test.go::TestToolRegistry_UnexpectedDuplicateStillWarns`

Command:

```text
CGO_ENABLED=0 go test -tags goolm,stdjson -run '^(TestWirePlanToolsForAgent_NilStoreDoesNotWarnPerAgent|TestRegisterEmailToolsForAgent_ExpectedRewireDoesNotWarn|TestToolRegistry_UnexpectedDuplicateStillWarns)$' -count=1 -v -p 1 github.com/elicify-ai/omnipus/pkg/agent
```

Failure output on the original implementation:

```text
=== RUN   TestWirePlanToolsForAgent_NilStoreDoesNotWarnPerAgent
    log_noise_test.go:25: expected the documented nil-store boot pass to emit no per-agent WARNs, got 2
--- FAIL: TestWirePlanToolsForAgent_NilStoreDoesNotWarnPerAgent (0.00s)
=== RUN   TestRegisterEmailToolsForAgent_ExpectedRewireDoesNotWarn
    log_noise_test.go:38: expected normal email-tool re-wiring to emit no duplicate-registration WARNs, got 5
--- FAIL: TestRegisterEmailToolsForAgent_ExpectedRewireDoesNotWarn (0.00s)
=== RUN   TestToolRegistry_UnexpectedDuplicateStillWarns
--- PASS: TestToolRegistry_UnexpectedDuplicateStillWarns (0.00s)
FAIL
exit=1
```

The control test passing in the RED run proves the harness could see a genuine strict-registration warning before the fix.

## Root cause

1. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/x-log-noise/pkg/agent/loop_wire.go::AgentLoop.wirePlanToolsForAgent` documents the first nil-store pass as normal boot ordering, but emitted its status through `logger.WarnCF` once for every agent. The tool constructors already received the nil store and failed closed at execution time; the warning level added noise without adding protection.
2. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/x-log-noise/pkg/agent/email_tools.go::registerEmailToolsForAgent` rebuilds five first-party email tools during every shared-tool rewire but used strict `Register`. `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/x-log-noise/pkg/tools/registry.go::ToolRegistry.registerToolLocked` correctly treats strict same-name registration as unexpected: it preserves the incumbent and emits WARN. Applying that strict path to expected config rewiring produced five warnings per agent and discarded the refreshed tool instances.

GitNexus impact analysis, refreshed at base commit `8300ff4`, rated `wirePlanToolsForAgent` LOW risk and `registerEmailToolsForAgent` MEDIUM risk. Change detection after the fix found exactly those two changed symbols, zero affected indexed processes, and LOW aggregate risk.

## Fix

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/x-log-noise/pkg/agent/loop_wire.go::AgentLoop.wirePlanToolsForAgent`: demoted only the documented first-pass nil-plan-store message from WARN to DEBUG. Tool construction and nil dependencies are unchanged.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/x-log-noise/pkg/agent/email_tools.go::registerEmailToolsForAgent`: changed only the known first-party email rewire loop to `RegisterReplacing`, so refreshed tool instances replace their expected predecessors without WARN noise.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/x-log-noise/pkg/tools/registry.go::ToolRegistry.Register` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus2/.squads/x-log-noise/pkg/tools/registry.go::ToolRegistry.registerToolLocked` were not changed. Unexpected collisions remain strict, visible, and non-overwriting.

## GREEN receipt

Restored implementation after the revert-proof mutation:

```text
=== RUN   TestWirePlanToolsForAgent_NilStoreDoesNotWarnPerAgent
--- PASS: TestWirePlanToolsForAgent_NilStoreDoesNotWarnPerAgent (0.00s)
=== RUN   TestRegisterEmailToolsForAgent_ExpectedRewireDoesNotWarn
--- PASS: TestRegisterEmailToolsForAgent_ExpectedRewireDoesNotWarn (0.00s)
=== RUN   TestToolRegistry_UnexpectedDuplicateStillWarns
--- PASS: TestToolRegistry_UnexpectedDuplicateStillWarns (0.00s)
PASS
ok  github.com/elicify-ai/omnipus/pkg/agent  5.340s
exit=0
```

All 10 pre-existing `TestRegisterEmailTools_*` tests plus the three new regression tests also passed in one scoped run (`exit=0`).

The existing fail-closed behavioral test passed:

```text
=== RUN   TestWirePlanTools_ExecutePlanFailsClosedBeforeSetPlanStore
Tool execution failed error="execute_plan failed: plan store is not available" component=tool tool=execute_plan
Tool execution failed error="create_plan failed: plan store is not available" component=tool tool=create_plan
--- PASS: TestWirePlanTools_ExecutePlanFailsClosedBeforeSetPlanStore (0.25s)
PASS
exit=0
```

Repository gates:

```text
GUARD RUNNER: all 27 guards passed
exit=0

make lint-budgets
exit=0
```

The budget gate reported existing warnings only; it reported no failure and no new over-budget function.

## Revert-proof receipt

Both production changes were manually reverted while leaving the tests intact. The exact focused command then failed for the original reasons:

```text
=== RUN   TestWirePlanToolsForAgent_NilStoreDoesNotWarnPerAgent
    log_noise_test.go:25: expected the documented nil-store boot pass to emit no per-agent WARNs, got 2
--- FAIL: TestWirePlanToolsForAgent_NilStoreDoesNotWarnPerAgent (0.00s)
=== RUN   TestRegisterEmailToolsForAgent_ExpectedRewireDoesNotWarn
    log_noise_test.go:42: expected normal email-tool re-wiring to emit no duplicate-registration WARNs, got 5
--- FAIL: TestRegisterEmailToolsForAgent_ExpectedRewireDoesNotWarn (0.00s)
=== RUN   TestToolRegistry_UnexpectedDuplicateStillWarns
--- PASS: TestToolRegistry_UnexpectedDuplicateStillWarns (0.00s)
FAIL
exit=1
```

The fix was restored and the same command returned GREEN as shown above. This proves both noise assertions die when the fix is removed, while the genuine unexpected-collision warning remains independently visible.

## Honest gaps

- The full Go suite was not run locally, as prohibited by the squad constraints; CI remains authoritative for the complete suite.
- A live 66-agent gateway boot was not performed. The focused tests reproduce the two underlying per-agent multipliers directly (two plan WARNs for two agents; five duplicate WARNs for one rewire) and mutation-prove their removal.
- No PR was opened and nothing was pushed, per the squad brief.
