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
- Final frontend checks, mutation proof, deployed Linux keyboard test and
  physical German Mac keyboard confirmation are pending.

The live fixture models the logical Option-L chord through native viewer events;
it does not constitute a test using physical German keyboard hardware. Dead-key
and IME composition support is outside this change.

Game reference: https://ioa.fun/asteroid-game
