# pkg/channels — connectors

"Connectors" is the SCREEN name only (UI rename, commit `1b95ced67`). Nothing
below the UI was renamed: package `pkg/channels`, interface `Channel`,
`channels.RegisterFactory`, config key `channels`, wire type
`ChannelRouting`, REST `/api/v1/channels/{id}/routing`. Read "Connectors" as
the screen and "channel" as the thing it configures; do not finish the rename
piecemeal.

## Adding a channel

Implement `Channel` (`base.go`) plus the opt-in capability interfaces it
supports (`interfaces.go`: `TypingCapable`, `MessageEditor`, `MessageDeleter`,
`ReactionCapable`, `PlaceholderCapable`, `StreamingCapable`,
`CommandRegistrarCapable`, …). Register the factory via
`channels.RegisterFactory(name, factory)` in a `func init()`, THEN add its
activation to the if-ladder in `manager.go::Manager.initChannels` — a
registered factory nobody activates silently never starts.

Channels reach the agent loop only through the in-process `MessageBus`
(`pkg/bus`). There is no `BridgeAdapter`, no stdio bridge protocol, no Channel
SDK (issue #151 tracks the planned plugin system) — a new channel that assumes
one will not compile against reality.

## Sidecars and the removed lite variant

Channels wrapping a non-Go runtime (Signal → `signal-cli`) spawn a sidecar from
their own `Start()` and talk over localhost HTTP. WhatsApp is the opposite:
pure-Go whatsmeow, in-process — not a sidecar example.

The lite build variant (`-tags lite`, `make build-lite`) was removed
2026-08-23: whatsmeow ships in every build and the `//go:build !lite` ladders
are gone. Do not reintroduce them (Makefile's build-lite removal note).

## Secrets never sit in config.json

`rest.go::configureChannel` detects secret fields (token / secret / password /
key / api_key), stores each in the encrypted credential store and writes a
`<field>_ref` into `config.json` (SEC-23). No plaintext channel secret is
persisted anywhere; keep new channels on that path.

## WhatsApp native pairing

`whatsapp_native` emits its pairing QR over the `whatsapp_pairing` WS frame;
the SPA renders it inline in the Configure panel (`WhatsAppNativeNotice`).
Enable & Save, then scan via WhatsApp → Linked Devices.
