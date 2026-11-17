// #1537 round 8 N1/N2. Oracles: FR-005/006, copy table row 3, and
// API and Data: "No retry count is invented"; own-path contract-violation parity.
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import { reset, feed, retry, status, active, mount, named, lines, bucket, SID, AT } from '../../../tests/helper-model-notices/harness'
import * as diagnostics from '@/lib/telemetry'

beforeEach(() => { reset(); vi.useFakeTimers(); vi.setSystemTime(new Date(AT)) })
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

it.each([
  [1, 'Researcher: Connection came back — the model answered after 1 retry.'],
  [2, 'Researcher: Connection came back — the model answered after 2 retries.'],
] as const)('N1 main recovered retries=%i is the exact named sentence', (retries, sentence) => {
  feed(retry())
  const { container } = mount()
  expect(named(container, 'Researcher', 'Retrying automatically'), 'N1 instrument: the real retry row is visible before recovery').toHaveLength(1)
  feed(status('recovered', 'child-a', 'turn-a', { retries }))
  const rows = named(container, 'Researcher', 'Connection came back')
  expect(rows, 'N1: exactly one visible recovered row').toHaveLength(1)
  expect(rows[0].textContent, 'N1: exact name, retry count and singular/plural').toBe(sentence)
  expect(rows[0]).toBeVisible()
  expect(bucket().messageOrder, 'FR-005: named recovery is not an unnamed main-chat system message').toEqual([])
})

it.each([
  [1, 'Connection came back — the model answered after 1 retry.'],
  [2, 'Connection came back — the model answered after 2 retries.'],
] as const)('N1 helper own recovered retries=%i is the exact unprefixed note', (retries, sentence) => {
  feed(active('helper-turn'), retry('', 'helper-turn'))
  const { container } = mount()
  expect(lines(container, 'Retrying automatically'), 'N1 instrument: own notice is visible before recovery').toHaveLength(1)
  feed(status('recovered', '', 'helper-turn', { retries }))
  expect(bucket().messageOrder.map(id => bucket().messagesById[id].content), 'N1: exact own-chat persisted-in-memory system-note content').toEqual([sentence])
  const notes = lines(container, sentence)
  expect(notes, 'N1: the helper sees its own recovered note').toHaveLength(1)
  expect(notes[0].textContent, 'N1: exact own sentence, no invented helper prefix').toBe(sentence)
  expect(notes[0]).toBeVisible()
  expect(container.textContent).not.toContain('Researcher:')
})

it.each([
  ['absent', undefined], ['zero', 0], ['negative', -1], ['fractional', 1.5], ['NaN', Number.NaN],
] as const)('N2 invalid recovered count %s clears only its named row and is diagnosed', (_label, retries) => {
  feed(retry(), status('waiting', 'child-b', 'turn-b', { agent_id: 'writer' }))
  const { container } = mount()
  expect(named(container, 'Researcher', 'Retrying automatically'), 'N2 positive control: addressed row exists before invalid recovery').toHaveLength(1)
  const writer = named(container, 'Writer', "hasn't responded")
  expect(writer, 'N2 positive control: independent helper exists').toHaveLength(1)
  const writerText = writer[0].textContent
  const logged = vi.spyOn(diagnostics, 'logDiagnostic')
  const frame = status('recovered', 'child-a', 'turn-a', { retries })
  if (retries === undefined) delete frame.retries
  feed(frame)
  expect(named(container, 'Researcher', ''), 'N2: malformed recovered clears the old named row, not an invented recovery').toHaveLength(0)
  expect(named(container, 'Writer', "hasn't responded").map(row => row.textContent), 'N2: unrelated helper is unchanged').toEqual([writerText])
  expect(lines(container, 'Connection came back'), 'No retry count is invented').toHaveLength(0)
  expect(container.textContent).not.toContain('undefined retries')
  expect(bucket().messageOrder, 'N2: malformed helper recovery invents no own-chat system message').toEqual([])
  expect(logged, 'N2: contract violation has exactly one diagnostic').toHaveBeenCalledTimes(1)
  expect(logged, 'N2: contract violation is logged with helper identity classification').toHaveBeenCalledWith('chatProviderStatusSkipped', { sessionId: SID, frameState: 'recovered', helper: true })
})

it('N2 unknown helper status renders no invented row and is diagnosed', () => {
  feed(status('waiting', 'child-b', 'turn-b', { agent_id: 'writer' }))
  const { container } = mount()
  expect(named(container, 'Writer', "hasn't responded"), 'N2 instrument: real helper notices render').toHaveLength(1)
  const before = named(container, 'Writer', "hasn't responded")[0].textContent
  const logged = vi.spyOn(diagnostics, 'logDiagnostic')
  feed(status('silent', 'child-a', 'turn-a'))
  expect(named(container, 'Researcher', ''), 'N2: unknown state with valid paired identity creates no row').toHaveLength(0)
  expect(named(container, 'Writer', "hasn't responded").map(row => row.textContent), 'N2: unknown state cannot change another helper').toEqual([before])
  expect(lines(container, 'Connection came back'), 'N2: unknown state invents no recovery').toHaveLength(0)
  expect(bucket().messageOrder).toEqual([])
  expect(logged, 'N2: unknown state has exactly one diagnostic').toHaveBeenCalledTimes(1)
  expect(logged, 'N2: exact unknown-state diagnostic, not silent discard').toHaveBeenCalledWith('chatProviderStatusSkipped', { sessionId: SID, frameState: 'silent', helper: true })
})
