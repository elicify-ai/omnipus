# Connectors (UI)

The screen is Connectors — route `/#/connectors` (`src/routes/_app/connectors.tsx`),
body `src/components/screens/ConnectorsScreen.tsx`. `/#/channels` is a 404
(negation test `src/routes/_app/-channels.test.tsx`).

## Design system

Read `.claude/skills/omnipus-design-system/SKILL.md` before adding or changing any
control, color, spacing, or type value here — it states the CI-enforced rules and cites
the script or test for each.

## The rename stops at the UI — everything below is still "channel"

The UI screen was renamed Channels → Connectors (commit `1b95ced6`); NOTHING
below it was renamed. The Go package is `pkg/channels`, the interface is
`Channel`, the `config.json` key is `channels`, the wire type is
`ChannelRouting`, the REST path is `/api/v1/channels/{id}/routing`, and the
react-query key is `['channels']`. Read "Connectors" as the screen and
"channel" as the thing it configures — do not "finish" the rename from the UI
side.

## This folder is nearly empty on purpose

- Only `EmailMailboxPanel.tsx` lives here — and email is a TOOL, not a channel:
  a mailbox belongs to exactly one (agent, workspace) pair (see its header).
  Do not model it like a channel row.
- Per-channel config UI is `ChannelConfigPanel.tsx` in
  `src/components/skills/`. ConnectorsScreen imports it from there; searching
  only this folder for channel config finds nothing.

## WhatsApp pairing

`whatsapp_native` pairing renders inline: the `whatsapp_pairing` WS frame feeds
`src/store/whatsappPairing.ts`, which `WhatsAppNativeNotice` (in
`src/components/skills/`) renders as a QR in the Configure panel. The old
"check the gateway terminal" notice is gone — do not bring it back.

## Tests

CI group `components-misc` (pattern includes `src/components/connectors/`).
Local: `npx vitest run src/components/connectors/`.
