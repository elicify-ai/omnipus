# Catch-up fixtures

`F1.json` … `F8.json` are Lane A's REAL gateway-recorded fixtures
(`pkg/gateway/catchup_fixtures_test.go`, committed on `squad/be-lane-a-gateway` in `1889e1aa5`,
see that lane's `SQUAD-REPORT-BEA.md`). They replaced this lane's earlier hand-derived
(`F1-live-turn.json`/`F2-reconnect-incremental.json`/`F3-reconnect-snapshot.json`) provisional
fixtures once Lane A's gateway hub landed.

Each file: `scenario`, `title`, `description`, `expect` (values taken from the scenario
definition, not from any reducer), and `tabs[]` with every frame in both directions
(`client→server` / `server→client`) plus `note` events marking what the test did between frames
(connection dropped, page reloaded, switched chat). Ids/boot ids/timestamps/durations are
normalized consistently within a file; seq numbers are real.

Consumed by `src/store/__tests__/chat.catchup-fixtures.test.ts`, which feeds every
`server→client` frame through the real `handleFrame` and asserts against each fixture's own
`expect` block (oracle-independence, per the squad brief's test-writing rule — never asserting
against the reducer's own output).

Regenerate from the gateway side:
`OMNIPUS_UPDATE_FIXTURES=1 go test -tags goolm,stdjson -run '^TestCatchUpFixtures$' ./pkg/gateway/`
(without the variable the recorder test fails on drift).
