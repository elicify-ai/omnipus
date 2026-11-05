# RED plan — #1449 group chat reuses the normal chat

Behaviour under test: a group is the normal chat with more than one agent. Messages, files, the message box, history, participants, invitation context, and approval cards follow `docs/internal/specs/1449-group-chat-spec.md`. Expected values come from that spec (scenario ids S-nn, datasets DS-n, machine-verifiable constraints), not from the current implementation.

## Specification sources

| Expectation | Source |
|---|---|
| Display, composer, live lines, header, safety, isolation | User stories 1, 2, 5, 6, 7, 11, 12, 13 and scenarios S-01..S-19, S-35..S-58, S-98..S-111, S-129, S-130 |
| Posting rules, size clamp, media | DS-1, S-18, S-21, S-24..S-26, S-118, S-150, machine-verifiable constraints |
| Agent answer size and media | US-4, S-29, S-31, S-34, S-119, T-06 |
| Card origin only for one group's work | D23, D28, DS-8, S-79, S-83, S-151 |
| Invitation status on the room record | Room schema, S-61 |
| Contract field limits | Contract changes table, T-51, T-55 |
| Library delete confirmation | D26, S-147, S-148 (US-14 is P2; only the constraint and confirmation rows are in this pack) |
| History page size | S-44, DS-9 rows 4–5 |

## Unit boundary

```
REAL  : room service, answer_request tool, mergeApprovalOrigin, RoomScreen,
        room store loadOlder, LibraryExplorer, contract YAML on disk
MOCK  : process edges only — room Deps (already the rooms test double),
        answer_request reply router (existing stub), fetchRoomHistory,
        Library HTTP client, browser WebSocket in the l1-room-ui harness
```

## What this pack expects to fail on, and what may already hold

See TRACEABILITY.md. New behaviour (media, clamp above 65,536, shared renderer, participants button, invitation status, D23/D28 origin) is expected RED. Rules the current room service already enforces at the default limit are included as oracles and may already pass; they are not claimed as red-before-green proof.

## Mutations (deferred to CHECK)

Proof-of-failability items 2 and 3, and the gate's proof checklist, are deferred to CHECK. Item 1 (see it red) is deferred to team-lead's frozen run: founder HOLD forbids go test, vitest, typecheck and Playwright in this authoring turn.

## Deliberate gaps

- US-14 / DS-13 full deletion lifecycle (T-63) except the contract wording and the Library confirmation (S-147, S-148). P2, and the association does not exist to drive yet.
- Invitation filter dataset DS-5 and delivery dataset DS-4 beyond the missing `invitation_context_status` oracle. No summary function exists to call; a test that only reflects on a missing name would stay red after a differently named correct implementation.
- S-20 / S-27 happy path (a real image reaches the model). The generated request has no media field, so a success oracle cannot be posted. The negative oracle (a bad media reference must not be posted) is in the gateway test.
- Two-browser approval race (S-80) and question provenance (S-74, S-75) need the agent engine's card pipeline; not covered by a compilable call that fails for the right reason without new production symbols. UAT rows UAT-10, UAT-13, UAT-23, UAT-24 carry them.
- Playwright T-47, T-48, T-49, T-50 need a running gateway. T-56 is written; the run script records BLOCKED when `OMNIPUS_URL` is unset rather than skipping inside the test.
