export type LocalClockBracket = { before: number; wallMs: number; after: number }

// Date.now is millisecond-quantized. This yields wall-minus-monotonic bounds,
// not proof that clocks on different machines agree or that no clock step occurs.
export function localClockOffsetBounds(clock: LocalClockBracket) {
  if (![clock.before, clock.wallMs, clock.after].every(value => Number.isFinite(value) && value >= 0) || clock.after < clock.before) throw new Error('Invalid clock bracket')
  return { minimumMs: clock.wallMs - clock.after - 1, maximumMs: clock.wallMs - clock.before + 1, quantizationMarginMs: 1 }
}
