// #1537 order 6. Oracle: FR-005/009/010, US2/3/4 and composition dataset.
import { beforeEach, afterEach, expect, it, vi } from 'vitest'
import { act } from '@testing-library/react'
import { reset, feed, retry, status, active, mount, named, lines, roster, AT } from '../../../tests/helper-model-notices/harness'

beforeEach(reset)
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers() })

it.each([false, true])('named retry is visible whether root is writing=%s', writing => {
  if (writing) feed(active())
  feed(retry())
  const { container } = mount()
  const row = named(container, 'Researcher', 'openrouter is rate-limiting requests right now. Retrying automatically in 0:30 (attempt 2 of 6).')
  expect(row, 'FR-005: idle main chat must show the same named sentence').toHaveLength(1)
  expect(row[0]).toBeVisible()
})

it.each(['missing', 'loading', 'failed'])('unknown roster (%s) uses Helper, never an id', kind => {
  roster.data = kind === 'missing' ? [] : undefined
  roster.error = kind === 'failed'
  feed(retry('secret-session-id', 't', { agent_id: 'secret-agent-id' }))
  const { container } = mount()
  expect(named(container, 'Helper', 'Retrying automatically')).toHaveLength(1)
  expect(container.textContent).not.toContain('secret-agent-id')
  expect(container.textContent).not.toContain('secret-session-id')
})

it.each([
  [0, 'waiting', ''], [1, 'waiting', ''], [2, 'waiting', ''], [3, 'waiting', ''],
  [4, 'waiting', '1 more helper has a model notice'],
  [5, 'waiting', '2 more helpers have model notices'],
  [4, 'recovered', '1 more helper has a model notice'],
  [4, 'mixed', '1 more helper has a model notice'],
] as const)('%i %s helpers: three oldest rows and truthful overflow', (count, kind, summary) => {
  roster.data = Array.from({ length: count }, (_, i) => ({ id: `a${i}`, name: `Helper ${i}` }))
  for (let i = 0; i < count; i++) {
    const f = kind === 'recovered' || (kind === 'mixed' && i > 1)
      ? status('recovered', `c${i}`, `t${i}`, { agent_id: `a${i}` })
      : retry(`c${i}`, `t${i}`, { agent_id: `a${i}` })
    feed(f)
  }
  const { container } = mount()
  for (let i = 0; i < count; i++) {
    const clause = kind === 'recovered' || (kind === 'mixed' && i > 1) ? 'after 1 retry.' : 'Retrying automatically'
    expect(named(container, `Helper ${i}`, clause), `first-notice order, row ${i}`).toHaveLength(i < 3 ? 1 : 0)
  }
  if (summary) expect(lines(container, summary)).toHaveLength(1)
  else expect(container.textContent).not.toContain('more helper')
  expect(container.textContent).not.toContain('helpers are waiting')
})

it('updates keep first-notice order; a cleared first row promotes the fourth', () => {
  roster.data = [0, 1, 2, 3].map(i => ({ id: `a${i}`, name: `Helper ${i}` }))
  for (let i = 0; i < 4; i++) feed(retry(`c${i}`, `t${i}`, { agent_id: `a${i}` }))
  feed(status('recovered', 'c0', 't0', { agent_id: 'a0' }))
  const { container } = mount()
  expect(named(container, 'Helper 0', 'after 1 retry.')).toHaveLength(1)
  expect(named(container, 'Helper 3', 'Retrying automatically')).toHaveLength(0)
  const first = named(container, 'Helper 0', 'after 1 retry.')[0]
  const second = named(container, 'Helper 1', 'Retrying automatically')[0]
  const third = named(container, 'Helper 2', 'Retrying automatically')[0]
  expect(first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
  expect(second.compareDocumentPosition(third) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
  feed(status('ended', 'c0', 't0', { agent_id: 'a0' }))
  const promoted = named(container, 'Helper 3', 'Retrying automatically')
  expect(promoted).toHaveLength(1)
  expect(third.compareDocumentPosition(promoted[0]) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
  expect(lines(container, '1 more helper has a model notice')).toHaveLength(0)
})

it('the unchanged own notice renders above the separate named helper rows', () => {
  feed(active(), retry('', 'own-turn'), retry())
  const { container } = mount()
  const helper = named(container, 'Researcher', 'Retrying automatically')
  expect(helper).toHaveLength(1)
  const own = lines(container, 'Retrying automatically').filter(el => !el.textContent?.trim().startsWith('Researcher:'))
  expect(own).toHaveLength(1)
  expect(own[0].compareDocumentPosition(helper[0]) & Node.DOCUMENT_POSITION_FOLLOWING).toBe(Node.DOCUMENT_POSITION_FOLLOWING)
})

it('one polite region owns frozen named state changes; countdown ticks never change it', () => {
  vi.useFakeTimers(); vi.setSystemTime(new Date(AT))
  feed(retry())
  const { container } = mount()
  const row = named(container, 'Researcher', '0:30')
  expect(row).toHaveLength(1)
  const polite = [...container.querySelectorAll<HTMLElement>('[aria-live="polite"]')]
    .filter(el => el.textContent?.includes('Researcher:'))
  expect(polite, 'FR-010: exactly one owner for named notices').toHaveLength(1)
  expect(polite[0].textContent?.trim()).toBe('Researcher: openrouter is rate-limiting requests right now. Retrying automatically in 0:30 (attempt 2 of 6).')
  expect(polite[0].querySelectorAll('[aria-live], [role="status"]')).toHaveLength(0)
  const initial = polite[0].textContent
  act(() => vi.advanceTimersByTime(1000))
  expect(named(container, 'Researcher', '0:29')).toHaveLength(1)
  expect(polite[0].textContent).toBe(initial)
  feed(status('waiting', 'writer-chat', 'writer-turn', { agent_id: 'writer' }))
  expect(polite[0].textContent).toContain('Writer:')
  expect(named(container, 'Writer', "hasn't responded")).toHaveLength(1)
})
