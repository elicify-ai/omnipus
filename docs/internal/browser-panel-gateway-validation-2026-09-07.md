# Gateway capture ownership per panel

Status: factory/registry ownership verified; remaining live/handler migration pending.

The production capture factory must address the resolved panel passed by the
attachment. Calls for two panels in one workspace must produce distinct captures;
another viewer of the same panel must reuse its capture. The ingest token registry
must retain both captures under their common workspace browsing key until each
one stops. Removing one must preserve the other's token lookup and conflict
enumeration must include every capture belonging to other workspaces.

Factory tests will use the real gateway constructor, agent-loop manager resolution,
and real capture construction without starting Chrome or negotiating a viewer.
Fixed media ports are disabled in the test configuration. Registry tests use real
capture tokens and inert relay adapters, with no token value printed in assertions.
These checks prove factory and registry ownership, not independent video playback;
that remains a browser acceptance gate after caller migration.

Run 49655 reproduced both intended gateway failures (terminal exit 1, 4.892
seconds): another panel reused the first capture, and registering a second
capture lost the first token and omitted it from other-workspace enumeration.
The registry now indexes each capture with its workspace key, and the factory
uses the resolved panel API. Existing enumeration assertions are migrated to
the new map orientation while retaining exact workspace/capture checks.
Green and mutation verification remain pending.

Impact analysis: factory LOW, two direct callers, six affected symbols, one
gateway process. Registry type and methods LOW, one direct indexed caller each;
constructor affects two gateway startup processes. The handler loop orientation
is the only viewer-handler edit in this change; request admission remains in
the media lane. No wire fields or token format change.

Driver 87132 completed with terminal exit 0: initial five-case selection passed
in 4.174 seconds; all four faults (factory panel loss, sibling registration
replacement, sibling cleanup, incomplete enumeration) were caught at their
intended assertions. Restored shuffled race verification passed in 8.161 seconds
with no race warnings. Independent review then identified that factory-created
token registration and on-stopped removal were not directly asserted. Those
assertions are now added to the real factory test; their verification remains
pending. The broader outbound queue representation also changed afterward and
requires its own focused validation before these gateway edits are committed.

The added factory lifecycle assertions passed in 73687 and were proven by
78672's two dedicated faults: removing real factory registration lost lookup,
and removing its on-stopped removal retained the stopped token. The restored
race selection passed together with scoped outbound and attachment checks
(53 tests/subtests, zero failures/skips/race warnings, 27.502 seconds). Token
ownership now stays independent for panels sharing one workspace. This does
not claim that every live input/tab/viewport caller has migrated yet.
