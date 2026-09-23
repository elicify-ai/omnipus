# Catch-up fixtures — PROVISIONAL

**Status: hand-derived, not gateway-recorded.** BE-DESIGN.md §8.2 specifies these fixtures
should be produced by `pkg/gateway/catchup_fixtures_test.go::TestCatchUpFixtures`, which drives
the *real* `WSHandler` (Lane A's `ws_session_hub.go`) through scripted scenarios and records the
exact wire bytes each connection received. That gateway test — and the hub it exercises — had
not landed on this branch's base (`origin/feat/823-seq-redo`) as of Squad BE-C's work (2026-09-23).

Per the squad brief ("use frame orders produced by the REAL gateway... until it exists, derive
orders from the design and mark them provisional"), the three fixtures in this directory
(`F1-live-turn.json`, `F2-reconnect-incremental.json`, `F3-reconnect-snapshot.json`) are
hand-derived directly from BE-DESIGN.md §6.4's own worked examples ("The actual frame orders the
gateway sends") — not invented, but also not verified against a running hub.

**When Lane A's fixtures land:** replace every file in this directory with the real
`OMNIPUS_UPDATE_FIXTURES=1`-generated output, delete this README's provisional notice, and re-run
`src/store/__tests__/chat.catchup-fixtures.test.ts` — it is written to be indifferent to fixture
provenance (it reads the JSON files and feeds them through the real `handleFrame`, asserting
against the SCENARIO DEFINITION per §8.2, never against the reducer's own output), so no test code
should need to change, only these three JSON files themselves (and probably grow to cover the
full F1–F8 set the design lists).
