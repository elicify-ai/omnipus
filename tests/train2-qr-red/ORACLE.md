# Train 2 / WU-7 — WhatsApp pairing QR in the requesting chat: RED oracle

**LEAN pack** (founder: "do not overengineer") — exactly the dispatched rows, each at
full strength. Spec of record: `docs/internal/specs/omnipus-system-agent-spec.md`
BDD-8.4, BDD-8.5, BDD-8.8, FR-081, frame K10 (optional `request_id`/`session_id`;
status includes "stopped", contract 0e03d98ec). Behaviour oracles additionally come
from the approved frontend plan (chat mounts the SAME `WhatsAppNativeNotice` +
`whatsappPairing` store Connectors uses; notice gains optional `requestId`/
`sessionId` props sent on the subscribe frame; store keeps the request identity;
ref-counted subscription; replayed calls show a static pointer). Nothing is taken
from current code.

## Planned public surface (RED's naming decision, GREEN must match)

- `WhatsAppNativeNotice` gains optional `requestId`/`sessionId` props, sent on
  `whatsapp_pairing_subscribe`.
- The pairing store keeps `request_id`/`session_id` from frames; cards are
  request-correlated.
- New `src/components/chat/tools/ConnectChannelTool.tsx::
  ConnectChannelTool({ toolCallId, sessionId, channelId, live })` — the chat-side
  card: live WhatsApp call → the notice; replayed → the static Connectors pointer.

## RED receipts (HEAD 55d0ddd4d, logs in tests/train2-qr-red/logs/)

| Row | File · case | RED reason (observed) |
|---|---|---|
| 1 — BDD-8.4 | `WhatsAppNativeNotice.chat.test.tsx` · "subscribes with channel_id, active, request_id and session_id…" | the subscribe frame carries only `channel_id`+`active` — no `request_id`/`session_id` (props ignored at RED) |
| 2 — correlation | same file · "Row 2" | a frame for a DIFFERENT request_id **renders** the QR in this card (the store is keyed by channel only) |
| 3 — BDD-8.5 | same file · "Row 3" | no timeout state renders at all ("Unable to find …pairing timed out") — neither correlated nor with Retry |
| 4 — notice half, BDD-8.8 | same file · "Row 4" | no stopped state renders ("Unable to find …stopped") |
| 5 — BDD-8.8 reconnect | same file · "Row 5" | the re-sent subscribe lacks `request_id`/`session_id` (the resend itself already works; the identity is the delta) |
| 6 — ref-count | same file · "Row 6" | the FIRST unmount already sends `active: false` while the other site is open |
| 7 — replayed call | `ConnectChannelTool.chat.test.tsx` · "Row 7" | `BLOCKED: ConnectChannelTool not implemented` — no chat-side card exists |
| 1/4 — chat card | same file · "Row 1" | `BLOCKED: ConnectChannelTool not implemented` |
| 4 — Connectors half | same file · "WhatsAppPairingBody renders the stopped state…" | `stopped` falls through to the default spinner ("Generating your QR code…"), no stopped text |

`npm run typecheck` exit 0 (`logs/typecheck.log`). Existing neighbours stay green:
`WhatsAppNativeNotice.test.tsx` 12/12, `ChannelConfigPanel.test.tsx` 67/67.

## Kept green / not covered (deliberate, LEAN)

- The existing `WhatsAppNativeNotice.test.tsx` (instance-keyed QR) and
  `ChannelConfigPanel.test.tsx` are untouched and green — the REUSE rule means no
  second QR component or store is created.
- No anti-case sprawl per the founder's instruction: no malformed-frame, unrelated-
  channel, or duplicate-frame combinatorics beyond the two correlation rows the
  dispatched rows name.
