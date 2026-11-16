// #1537 order 5, FR-006/007: real frame reducer, observed via own-slot
// invariants and real screen (no assumption about a future internal map name).
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { reset, feed, retry, status, mount, named, bucket, active, SID } from '../../../tests/helper-model-notices/harness'
import { useChatStore } from './store'
import * as diagnostics from '@/lib/telemetry'

beforeEach(reset)
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers() })

describe('#1537 per-helper-turn notice reduction', () => {
  it('helper recovery cannot clear or restart the own retry slot or add an unnamed system message', () => {
    feed(retry('', 'own-turn'))
    const own = structuredClone(bucket().providerRetryEvent)
    feed(status('recovered'))
    expect(bucket().providerRetryEvent, 'FR-007: helper never clears the own countdown').toEqual(own)
    expect(bucket().messageOrder, 'FR-005: no extra unnamed root system message').toEqual([])
    const { container } = mount()
    expect(named(container, 'Researcher', 'after 1 retry.')).toHaveLength(1)
  })

  it('two helper chats for the same agent remain independent; ending one removes only its turn', () => {
    feed(retry('child-a'), retry('child-b', 'turn-b'))
    const { container } = mount()
    expect(named(container, 'Researcher', 'Retrying automatically')).toHaveLength(2)
    feed(status('ended', 'child-b', 'turn-b'))
    expect(named(container, 'Researcher', 'Retrying automatically')).toHaveLength(1)
    feed(status('ended', 'child-a'))
    expect(named(container, 'Researcher', 'Retrying automatically')).toHaveLength(0)
  })

  it('a late old-turn ended cannot clear the same chat newer turn', () => {
    // T8 split B: this is the pre-publication race in legal server order,
    // not an already-sequenced old frame injected behind a browser cursor.
    feed(retry('child-a', 'turn-a', { seq: 1, attempt: 2 }),
      retry('child-a', 'turn-new', { seq: 2, attempt: 3 }))
    const { container } = mount()
    expect(named(container, 'Researcher', 'Retrying automatically'), 'FR-005: both affected turns exist before the old end').toHaveLength(2)
    expect(named(container, 'Researcher', '(attempt 2 of 6)')).toHaveLength(1)
    expect(named(container, 'Researcher', '(attempt 3 of 6)')).toHaveLength(1)
    feed(status('ended', 'child-a', 'turn-a', { seq: 3 }))
    expect(named(container, 'Researcher', 'Retrying automatically'), 'FR-007: late old end removes exactly the old turn').toHaveLength(1)
    expect(named(container, 'Researcher', '(attempt 2 of 6)'), 'FR-006: the old turn is gone').toHaveLength(0)
    expect(named(container, 'Researcher', '(attempt 3 of 6)'), 'FR-007: the specifically newer turn survives the old end').toHaveLength(1)
    feed(status('ended', 'child-a', 'turn-new', { seq: 4 }))
    expect(named(container, 'Researcher', 'Retrying automatically')).toHaveLength(0)
  })

  it.each(['responding', 'ended'])('%s clears only the addressed helper, not Writer or own waiting', state => {
    feed(active(), status('waiting', '', 'own-turn'), retry(), status('waiting', 'child-b', 'turn-b', { agent_id: 'writer' }))
    const own = structuredClone(bucket().providerStatusEvent)
    feed(status(state))
    expect(bucket().providerStatusEvent).toEqual(own)
    const { container } = mount()
    expect(named(container, 'Researcher', 'Retrying automatically')).toHaveLength(0)
    expect(named(container, 'Writer', "hasn't responded")).toHaveLength(1)
  })

  it('root done leaves named helper rows; a later same-turn retry supersedes recovered', () => {
    feed(active(), retry())
    const { container } = mount()
    expect(named(container, 'Researcher', 'Retrying automatically'), 'M-done positive control: helper retry exists before root done').toHaveLength(1)
    feed({ type: 'done', session_id: SID, turn_id: 'own-turn', stats: {} })
    expect(named(container, 'Researcher', 'Retrying automatically'), 'FR-008: root done must preserve the helper retry before any later notice').toHaveLength(1)
    feed(status('recovered'))
    expect(named(container, 'Researcher', 'after 1 retry.')).toHaveLength(1)
    feed(retry('child-a', 'turn-a', { attempt: 3 }))
    expect(named(container, 'Researcher', '(attempt 3 of 6)')).toHaveLength(1)
    expect(named(container, 'Researcher', 'Connection came back')).toHaveLength(0)
  })

  it.each([
    { child_session_id: 'child-a', agent_id: undefined },
    { child_session_id: undefined, agent_id: 'researcher' },
  ])('malformed paired identity cannot overwrite own retry (%j)', fields => {
    feed(retry('', 'own-turn'))
    const own = structuredClone(bucket().providerRetryEvent)
    const logged = vi.spyOn(diagnostics, 'logDiagnostic')
    feed(retry('child-a', 'turn-a', fields))
    expect(bucket().providerRetryEvent, 'contract violation ignored, not misclassified as own').toEqual(own)
    expect(logged, 'wire rules: malformed identity is diagnosed, not silently dropped').toHaveBeenCalled()
  })

  it('ended without child fields is ignored without clearing own slots', () => {
    feed(retry('', 'own-turn'))
    const own = structuredClone(bucket().providerRetryEvent)
    const logged = vi.spyOn(diagnostics, 'logDiagnostic')
    feed(status('ended', '', 'own-turn'))
    expect(bucket().providerRetryEvent).toEqual(own)
    expect(logged, 'wire rules: illegal own ended is diagnosed').toHaveBeenCalled()
    expect(useChatStore.getState().messages).toEqual([])
  })
})
