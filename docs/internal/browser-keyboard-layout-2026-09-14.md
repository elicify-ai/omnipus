# German Mac layout and game keyboard input

## Confirmed behavior

On Amsterdam candidate `88f12e6c3`, physical Space was sent as an `Input.insertText`
operation. The controlled game/chat fixture received one space in its focused
textarea, zero key-down events and zero shots. The real game's document-level
Space key-down handler therefore could not prevent insertion or fire.

The sender also classified every Alt/Option character as a shortcut. A German
Mac Option-L event with `key=@` was forwarded without its composed text.

## Correction

Physical printable keys now use ordered key-down/key-up events. The key-down
includes the character produced by the local layout. Chrome can deliver the
key event to the page and perform its text default action only when the page
does not cancel it. Separate text insertion remains available for explicit
text input operations; it is no longer the physical-key route.

Mac Option and explicit AltGraph character input are distinguished from
ordinary Alt/Ctrl/Meta shortcuts. Key/code identities and modifier bits remain
intact. Stored key-release frames contain no text. Transport and wire schema
are unchanged; the backend already supports key-down text.

## Validation

- Frontend RED reproduced missing physical transitions and missing Option/AltGraph
  characters before the correction.
- Real local Chrome accepted key-down `@`, code `KeyL`, text `@` with Alt=1
  and Ctrl+Alt=3; each inserted one character and delivered key-down/key-up.
- Existing backend behavior was characterized at the real live-dispatch command
  boundary for Option-L, Space, Shift-Space, Ctrl/Meta shortcuts and shared
  held-key release. The focused test passed in 3.222 seconds. This does not
  itself establish browser DOM behavior.
- Live old-candidate RED confirmed the Space/text-field problem with exact pixels.
- All 237 focused frontend tests passed. Three isolated broken-code variants
  (text-only dispatch, missing Mac Option eligibility and missing held-key
  tracking) were caught by the regression tests. Relevant lint passed.
- The production frontend build passed after correcting the helper callback
  type; the final 130 routing/helper tests passed again after that typing fix.
- Amsterdam runtime `47179a4c3a90ca9ad035e6c38e80a7affb132e0f` was deployed,
  with installed/running binary SHA256
  `fc233488ba5d50cb66f1f0b35bf1e8da46a8487a50846a6af748ded13f366f34`
  verified. Machine configuration was preserved.
- The live Linux regression passed in 24.2 seconds: one Space shot with no
  inserted space, normal A input, ArrowRight movement and the complete logical
  Option-L chord producing `a@`. Exactly five key-downs and five key-ups were
  observed, with no held keys, fixture errors, viewer errors or WebSocket gestures.
- Physical German Mac keyboard confirmation remains with the user.

The live fixture models the logical Option-L chord through native viewer events;
it does not constitute a test using physical German keyboard hardware. Dead-key
and IME composition support is outside this change.

Game reference: https://ioa.fun/asteroid-game
