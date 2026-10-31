// Gate I2: independent oracles from chat-history-paging-spec D5,
// D-G1b/c and B07. Integration lifecycle/real virtualizer are separate tests.
import { afterEach, describe, expect, it } from 'vitest'
import {
  forgetHistoryGeometry, historyAttachSizing, historyPageSizing,
  historyPixelTrigger, HistoryRowMeasurements, recordHistoryGeometry,
} from './chat-history-sizing'

const SESSION = 'I2-direct-sizing'
afterEach(() => forgetHistoryGeometry(SESSION))

describe('I2 D5 latest100 distinct stable drawn-row samples', () => {
  it('uses80 only until a stable positive measurement exists', () => {
    const samples = new HistoryRowMeasurements()
    expect([samples.count, samples.average], 'D5 empty generation starts at80').toStrictEqual([0, 80])
    expect(samples.observe('first:text', 40), 'first stable row changes the estimator').toBe(true)
    expect([samples.count, samples.average], 'measured40 replaces the bootstrap80').toStrictEqual([1, 40])
  })

  it('retains2..101 for51.5, then replaces101 with201 for52.5 without appending a101st sample', () => {
    const samples = new HistoryRowMeasurements()
    for (let height = 1; height <= 101; height++) samples.observe(`row-${height}:text`, height)
    // Independent arithmetic: sum(2..101)=5150; count100 =>51.5.
    expect([samples.count, samples.average], 'D-G1c101 distinct rows evict only the oldest sample').toStrictEqual([100, 51.5])
    expect(samples.observe('row-101:text', 201), 'existing row gets a replacement measurement').toBe(true)
    // Replace101 with201 adds100, not a new denominator:5250/100=52.5.
    expect([samples.count, samples.average], 'replacement preserves100 distinct rows').toStrictEqual([100, 52.5])
    expect(samples.observe('row-101:text', 201), 'identical re-observation is not a new sample').toBe(false)
    expect([samples.count, samples.average], 'unchanged re-observation preserves the exact mean').toStrictEqual([100, 52.5])
  })

  it.each([0, -1, Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY])('excludes invalid height%s rather than poisoning or replacing stable samples', height => {
    const samples = new HistoryRowMeasurements()
    expect(samples.observe('invalid:text', height), 'D5 only finite positive heights are samples').toBe(false)
    expect([samples.count, samples.average], 'invalid-only generation stays empty').toStrictEqual([0, 80])
    samples.observe('stable:text', 40)
    expect(samples.observe('stable:text', height), 'invalid observation cannot replace a known stable height').toBe(false)
    expect([samples.count, samples.average], 'stable measurement survives invalid re-observation').toStrictEqual([1, 40])
  })

  it('excludes live-changing rows even when their height is finite and positive', () => {
    const samples = new HistoryRowMeasurements()
    expect(samples.observe('live:text', 400, false), 'D5 unstable bubbles are not samples').toBe(false)
    expect([samples.count, samples.average], 'unstable-only rows leave the80 seed').toStrictEqual([0, 80])
    samples.observe('settled:text', 40)
    samples.observe('another-live:text', 1600, false)
    expect([samples.count, samples.average], 'live row does not bias a stable40px mean').toStrictEqual([1, 40])
  })

  it('floors a positive subpixel sample mean at1 without treating it as a zero sample', () => {
    const samples = new HistoryRowMeasurements()
    samples.observe('first:text', 0.25)
    samples.observe('second:text', 0.75)
    expect([samples.count, samples.average], 'D5 max(1,(0.25+0.75)/2)').toStrictEqual([2, 1])
  })

  it('reset removes both accumulated heights and identities before the next generation', () => {
    const samples = new HistoryRowMeasurements()
    samples.observe('first:text', 40)
    samples.observe('second:text', 120)
    samples.reset()
    expect([samples.count, samples.average], 'D5 new generation has no samples').toStrictEqual([0, 80])
    samples.observe('first:text', 160)
    expect([samples.count, samples.average], 'reset clears old identity and total, not only the count').toStrictEqual([1, 160])
  })

  it('drops every narration/tool sample for a released source but preserves distinct retained tool rows', () => {
    const samples = new HistoryRowMeasurements()
    samples.observe('keep:text', 20)
    samples.observe('keep:tool:one', 40)
    samples.observe('remove:text', 120)
    samples.retainPositions(new Set(['keep']))
    expect([samples.count, samples.average], 'two retained drawn rows average(20+40)/2').toStrictEqual([2, 30])
    samples.retainPositions(new Set())
    expect([samples.count, samples.average], 'D9 no measurement survives its source release').toStrictEqual([0, 80])
  })
})

describe('I2 D5 B07 independent request sizing', () => {
  // Raw two-screen targets:40,4,320. Remaining cases straddle both clamp edges.
  it.each([
    { height: 800, average: 40, expected: { limit: 40, min_rows: 40 } },
    { height: 800, average: 400, expected: { limit: 20, min_rows: 4 } },
    { height: 1600, average: 10, expected: { limit: 100, min_rows: 100 } },
    { height: 760, average: 80, expected: { limit: 20, min_rows: 19 } },
    { height: 800, average: 80, expected: { limit: 20, min_rows: 20 } },
    { height: 840, average: 80, expected: { limit: 21, min_rows: 21 } },
    { height: 3960, average: 80, expected: { limit: 99, min_rows: 99 } },
    { height: 4000, average: 80, expected: { limit: 100, min_rows: 100 } },
    { height: 4040, average: 80, expected: { limit: 100, min_rows: 100 } },
    { height: 1, average: 80, expected: { limit: 20, min_rows: 1 } },
  ])('H$height/A$average uses exact prefetch limit/floor', ({ height, average, expected }) => {
    expect(historyPageSizing(height, average), 'D5 clamp(ceil(2H/A),20,100), floor capped at limit').toStrictEqual(expected)
  })

  it.each([
    { height: 800, average: 40, expected: { limit: 60, min_rows: 40 } },
    { height: 800, average: 400, expected: { limit: 20, min_rows: 4 } },
    { height: 1600, average: 10, expected: { limit: 100, min_rows: 100 } },
  ])('opening H$height/A$average uses3H target but2H floor', ({ height, average, expected }) => {
    expect(historyPageSizing(height, average, 3), 'D5 opening formula is not the prefetch formula').toStrictEqual(expected)
    recordHistoryGeometry(SESSION, height, average)
    expect(historyAttachSizing(SESSION), 'actual attach consumer carries those exact sizes').toStrictEqual({ history_limit: expected.limit, history_min_rows: expected.min_rows })
  })

  it('omits both attach sizes before geometry and after forgetting it', () => {
    expect(historyAttachSizing(SESSION), 'D5 unknown geometry leaves server40/20 defaults').toStrictEqual({})
    recordHistoryGeometry(SESSION, 800, 40)
    expect(historyAttachSizing(SESSION), 'positive control: known geometry affects actual attach').toStrictEqual({ history_limit: 60, history_min_rows: 40 })
    forgetHistoryGeometry(SESSION)
    expect(historyAttachSizing(SESSION), 'forgotten session cannot reuse old dimensions').toStrictEqual({})
  })

  it.each([0, -1, Number.NaN, Number.POSITIVE_INFINITY])('omits attach sizing for unusable viewport%s', height => {
    recordHistoryGeometry(SESSION, height, 40)
    expect(historyAttachSizing(SESSION), 'D5 no attach geometry for nonpositive or nonfinite H').toStrictEqual({})
  })
})

describe('I2 D5 inclusive2H trigger and suppressed automatic reads', () => {
  it.each([
    { distance: 1601, height: 800, visible: true, expected: false },
    { distance: 1600, height: 800, visible: true, expected: true },
    { distance: 1599, height: 800, visible: true, expected: true },
    { distance: 3, height: 1, visible: true, expected: false },
    { distance: 2, height: 1, visible: true, expected: true },
    { distance: 1, height: 1, visible: true, expected: true },
    { distance: 0, height: 0, visible: true, expected: false },
    { distance: 0, height: -1, visible: true, expected: false },
    { distance: 0, height: Number.NaN, visible: true, expected: false },
    { distance: 0, height: Number.POSITIVE_INFINITY, visible: true, expected: false },
    { distance: 1600, height: 800, visible: false, expected: false },
    { distance: Number.NaN, height: 800, visible: true, expected: false },
    { distance: Number.POSITIVE_INFINITY, height: 800, visible: true, expected: false },
  ])('distance$distance H$height visible$visible triggers$expected', ({ distance, height, visible, expected }) => {
    expect(historyPixelTrigger(distance, height, visible), 'B07 inclusive2H, D-G1c hidden/H0/nonfinite guards').toBe(expected)
  })
})
