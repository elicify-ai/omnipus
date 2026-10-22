# #1332 room history paging — SPA RED pack oracle

Source of every oracle: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/designs/ROOM-HISTORY-PAGING-1332-20261018.md`
(design rows T18–T24, rules D2/D4/D6) and the generated contract
(`getRoomHistory`, `RoomHistoryResponse` — contract 8a23e8b8b, present in this tree).
Expected values are the design's fixture arithmetic, never current code. Issue:
elicify-ai/omnipus#1332 (open).

## RED receipts (HEAD 35998d756, logs in tests/l1-1332-red/logs/)

| File | Cases | RED reason (observed) | Design row |
|---|---|---|---|
| `src/store/rooms.history.test.ts` | 10/10 FAILED, exit 1 | `BLOCKED: src/store/rooms.ts::loadOlder not implemented — required by …D6` — every case fails on the missing store surface | T18–T22 |
| `src/components/rooms/RoomScreen.history.test.tsx` | 4/5 FAILED, exit 1 | scroll-up fires no request (`fetchRoomHistory` called 0 times — no wiring in RoomTranscript/RoomScreen); no Retry button exists (no error UI) | T23, T24, T22 composer clause |
| same file | 1/5 PASSED at RED | the mount no-auto-walk guard — a keep-green guard (the absence already holds; it forbids GREEN introducing a mount walk) | T23 "no automatic unbounded walk" |
| `tests/e2e/room-history-paging.spec.ts` | CI only (shard stubs-1), not run locally | fails in CI until the SPA half lands: today no older page loads, so the hint never clears and `pager-message-1` is unreachable | T23 reader-offset + walk-to-beginning clause |

`npm run typecheck` exit 0 on this pack's final content (`logs/typecheck.log`).

## Planned public surface this pack pins (RED's naming decision, GREEN must match)

- `src/lib/api/rooms.ts::fetchRoomHistory(roomId, opts?: { beforeSeq?: number; limit?: number })`
  → GET `getRoomHistory`; `opts.limit` OMITTED by default (server default 100); `beforeSeq`
  → `before_seq` (exclusive cursor).
- `src/store/rooms.ts`: action `loadOlder(roomId): Promise<void>`; per-room
  `historyStatus: 'idle' | 'loading' | 'error'` and `olderBoundary?: number`
  (the next `before_seq`; undefined once the old end is reached).
- Screen UI: scroll-up on the transcript log fires the request; a loading status;
  an inline error with a keyboard-operable Retry button; the existing
  "Earlier messages aren't shown" hint is the continuation hint, removed only at
  terminal false.

## Case → design row map

| Test (file-local) | Row |
|---|---|
| merge by seq `[5,9]` + `[2]` → `[2,5,9]`; terminal closes continuation | T18 |
| duplicate/overlap retry idempotence; existing representation wins collisions | T18 |
| live `seq`/members/mutes/pause/generation/cost/snapshotReceived + chat buckets untouched | T18 invariance row |
| live entry 12 + newer member-state during flight; cursor stays at WS high-water; embedded deliveries cannot overwrite the newer authoritative state | T19 |
| truncated reconnect: in-flight page ignored on success; on failure; navigation invalidation; fresh boundary = new window's first entry | T20 |
| non-truncated reconnect preserves unresolved hasOlder + boundary; terminal stays false | T21 |
| history load/success/failure never touches `snapshotReceived` (store) and never blocks the composer (screen) | T22 |
| mount no-auto-walk (guard); exactly one request, oldest-seq cursor, default limit; terminal hint removal, no further requests | T23 |
| failed GET keeps rows/hint, inline error + keyboard Retry repeats the boundary; terminal clears; sending stays enabled | T24 |

## Not testable here, and why

- **Reader-offset preservation across a prepend (T23)** needs real layout/scrolling —
  jsdom has none. Covered by the CI-only e2e spec above.
- **Rows T01–T17** are the server/contract lane (chief's Train 3 test author); the SPA
  pack pins only the client-observable contract via the generated types.
- **T25** (reachability + docs) is a landing-gate row, not a RED row.
