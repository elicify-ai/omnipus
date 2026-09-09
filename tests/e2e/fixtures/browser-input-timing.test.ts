import { describe, expect, it } from 'vitest'
import { runInNewContext } from 'node:vm'
import { localClockOffsetBounds } from './browser-input-timing'
import { fixtureHTML } from './browser-input-probe'

describe('same-host clock uncertainty', () => {
  it('retains a quantization margin around bracket bounds', () => {
    expect(localClockOffsetBounds({ before: 10, wallMs: 1000, after: 12 })).toEqual({ minimumMs: 987, maximumMs: 991, quantizationMarginMs: 1 })
  })
  it.each([{ before: 12, wallMs: 1000, after: 10 }, { before: NaN, wallMs: 1000, after: 10 }, { before: -1, wallMs: 1000, after: 10 }])('rejects invalid bracket %j', clock => {
    expect(() => localClockOffsetBounds(clock)).toThrow('Invalid clock bracket')
  })
})

// Real generated fixture script; only platform drawing/event delivery is simulated.
// This proves timing/order preservation, not native trust or physical paint.
function runFixture(enabled: boolean, trusted = true) {
  const callbacks: Record<string, (event: unknown) => void> = {}
  let ticks = 0
  const context = {
    window: {}, Date: { now: () => 1000 },
    document: { getElementById: () => ({ getContext: () => ({ fillRect() {}, fillText() {} }) }) },
    innerWidth: 1000, innerHeight: 800,
    performance: { timeOrigin: 1000, now: () => ++ticks * 10 },
    addEventListener: (type: string, callback: (event: unknown) => void) => { callbacks[type] = callback },
  }
  const script = fixtureHTML([1, 3, 5], 123, enabled).split('<script>')[1].split('</script>')[0]
  return runInNewContext(script + `
    for (const type of ['mousedown','mouseup','click']) fire(type);
    ({timing:window.__omnipusInputTiming?.snapshot(),count,hash,last,held,firstError});`, { ...context, fire: (type: string) => callbacks[type]({ button: 0, clientX: 100, isTrusted: trusted }) })
}
it('records all handler and canvas-command boundaries with unchanged exact pixels', () => {
  const result = runFixture(true)
  expect(result).toEqual({ timing: {
    nonce: 123, timeOrigin: 1000, clockAtLoad: { before: 10, wallMs: 1000, after: 20 }, clockAfterLastEvent: { before: 150, wallMs: 1000, after: 160 }, total: 3, offset: 0, state: { count: 3, hash: 66825, last: 5, held: 0, firstError: 0 },
    columns: ['count','code','trusted','handlerAt','wallClockMs','wallBracketEndAt','paintRequestedAt','paintCommandsCompletedAt'],
    rows: [[1,1,true,30,1000,40,50,60],[2,3,true,70,1000,80,90,100],[3,5,true,110,1000,120,130,140]],
  }, count: 3, hash: 66825, last: 5, held: 0, firstError: 0 }) // ((1*257+3)*257+5)
})
it('does not expose or read target clocks when instrumentation is disabled', () => {
  expect(runFixture(false)).toEqual({ timing: undefined, count: 3, hash: 66825, last: 5, held: 0, firstError: 0 })
})
it('instrumentation preserves the untrusted-input failure signal', () => {
  const result = runFixture(true, false)
  expect(result.firstError).toBe(1)
  expect(result.timing.rows.map((row: unknown[]) => row.slice(0,3))).toEqual([[1,255,false],[2,255,false],[3,255,false]])
})
