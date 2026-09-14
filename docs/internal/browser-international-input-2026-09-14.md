# International text input and binary browser gestures

## Intended behavior

The local operating system and viewer browser own layout processing and text
composition. A real local editable target receives dead keys, input-method
composition, committed Unicode and plain-text paste. Provisional candidates stay
local; finished text goes to the remote browser exactly once. Cancelling or
leaving the current input session must not insert pending text into another page.

Ordinary physical key-down/key-up events retain their locally resolved character
on key-down. This allows remote game/shortcut handlers to cancel the browser's
text default action, including Space. Candidate-selection keys belong to the
local input method while composition is active. Outside composition, Escape
keeps its existing release-control behavior.

The user separately authorized compact binary input packets. The dedicated
reliable and latest-position WebRTC channels retain their current responsibilities.
A versioned binary encoding replaces JSON for human gestures; signaling and
browser commands retain their existing representation. There is no WebSocket
input fallback. Malformed or incompatible packets must fail explicitly before
normal identity, geometry, ordering and control admission.

## Validation contract

Expected outcomes derive from the requested behavior, not observed implementation:

- Space fires once and inserts no space when the page cancels its default action.
- Physical A, arrows and German Mac logical Option-L retain exact events/releases.
- Provisional composition inserts nothing remotely; committed `日本é🙂` inserts
  once, including its exact Unicode. Cancellation inserts nothing.
- Native committed text without a physical key reaches the remote field once.
- Focus loss, navigation, control release and source replacement invalidate
  pending composition; late events cannot target the next document/session.
- Plain-text paste preserves Unicode and newlines. Unsupported oversized input
  is refused visibly, without partial replay.
- TypeScript and Go use independently specified binary golden vectors, with
  truncation, invalid UTF-8, nonfinite numbers, unsupported version and trailing
  bytes rejected. Existing admission and transport-routing checks remain active.
- Live evidence requires binary dedicated gestures, exact rendered output and
  zero held keys or unexpected errors. A real Chromium composition test verifies
  browser event handling; it does not emulate every operating-system input method.

## Initial evidence and limits

A native local Chromium probe using `Input.imeSetComposition` and
`Input.insertText` emitted its final composing input before `compositionend`.
Cancelling emitted an empty composition end and removed provisional text.
Implementation must also handle browsers that emit a final input after the end.

The old Amsterdam runtime `47179a4c3` reproduced the missing composition path:
ordinary `a@` arrived, but the expected composed `日本é🙂` did not. The native
composition-start assertion passed; rendered text stayed `a@` with two rather
than three committed insertions.

The real binary peer and codec tests passed, including independent bit-position
goldens for all 20 fields, Unicode/BOM preservation and malformed framing.
Gateway schema/channel admission and live-source release checks passed (4.064s).
Three isolated Go faults (version, trailing bytes, integer guard) were caught;
unmodified peer/codec tests passed again (1.204s). Three isolated frontend faults
(JSON sender, swapped coordinate fields, BOM stripping) were also caught.

Example encoded packet sizes with a 64-byte capture identity were hover 139 vs
245 JSON bytes, wheel 155 vs 268, key 143 vs 279 and composed text 129 vs 234.
These are packet-size comparisons, not measured latency or CPU improvements.
The decoder fills the existing typed frame directly; a single internal JSON
marshal retains the existing schema-validation boundary.

The final 240 composition/routing/focus regressions and 65 binary transport/codec
checks passed. Relevant lint and the production frontend build passed. Three
isolated composition faults (duplicate commit, stale-focus commit and candidate
key leakage) were caught. Two independent composition reviews found no remaining
concrete defect in the tested browser event-order scope. Deployed verification
remains pending.

References: [UI Events](https://www.w3.org/TR/uievents/),
[Input Events](https://www.w3.org/TR/input-events-2/),
[beforeinput behavior](https://developer.mozilla.org/en-US/docs/Web/API/Element/beforeinput_event).
