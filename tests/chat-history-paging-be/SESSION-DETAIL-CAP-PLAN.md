# SessionDetail cap oracle hardening — dispatcher-expanded test scope

The initial batch was check-only for `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/pkg/api/generated/contract_test.go`. Team-lead explicitly expanded the scope to the single `TestContract_SessionDetail_TooManyMessages` fixture after the source-verified false-green finding. No production schema, generated type or lint configuration may change. The oversized grandfathered test file may only shrink; extract this one test to a small new `_test.go` while retaining its exact test name and original100001 negative.

## Oracle and cases, before assertions

Authority: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/squad-1266-red-be/contracts/components/schemas/SessionDetail.yaml`::messages (maxItems100, required session/history_state), plus the spec D10/D15 and team-lead's explicit approved shape. The old100000 comment is stale; it is not the current cap.

| Case | Expected | Why |
|---|---|---|
|100 messages | The otherwise-valid available detail validates | Current maximum100 |
|101 messages | Exactly one leaf error at messages / maxItems | First value above the maximum |
|100001 messages | The original large negative remains; same exact messages/maxItems error | Preserve original regression input while updating the correct cap |
| In-memory no-cap copy, each case | The same document validates when only messages.maxItems is removed from an independent in-memory schema resource | Instrument control proving no unrelated missing fields/type errors can satisfy the negative |

The no-cap resource is freshly loaded/compiled with existing portable test helpers and real referenced schemas. No file is edited; shared schema caches are not mutated. This is a fixture-validity instrument control, **not** the independent CHECK production-mutation proof. Removing the actual production cap must be verified by a fresh CHECK instance, not this RED author.

This is a structural component-schema test, not a real history reader/performance receipt. For over-cap negative documents the page's source_group_count remains at its independently constrained maximum100 so only the messages array's structural cap can reject. Such a no-cap control does not certify a semantically coherent101/100001-group producer page; D10's real producer/consumer count-consistency tests own that separate invariant.

## Staged evidence

First attach the stronger no-cap/specific-error instrument to the existing legacy-shaped fixture and run the exact100-message case. It must be RED for the fixture's real missing session/history_state/required message fields, exposing the former generic-error false green. Then correct the fixture to the new contract and run exact100/101/100001 controls. Those are test-instrument results, not feature GREEN. Do not fabricate a product defect when the current production schema already enforces100.

Expected breakages for fresh CHECK: remove/raise messages.maxItems, move the bound to an unrelated field, or reintroduce a generic assert.Error without a valid positive/no-cap control. Every expected value is derived from the canonical contract/spec, never observed producer output. Exact tagged/CGO0 commands, raw exits, SHA and named results are required; no full local suite or implementation mutation by QA.
