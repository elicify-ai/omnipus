/**
 * Exact sentence for one delegation event line. The leading mark is a
 * Phosphor icon in the component, not a glyph in the sentence — UI chrome
 * does not carry emoji. Words match the spec table.
 */
import { describe, expect, it } from 'vitest'
import type { DelegationEvent } from './delegationEvents.types'
import { delegationEventLineText, delegationEventShowsOpen } from './delegationEventLine'

function event(overrides: Partial<DelegationEvent> & Pick<DelegationEvent, 'kind'>): DelegationEvent {
  return {
    id: overrides.id ?? `${overrides.kind}:1`,
    sessionId: 'parent',
    at: 1,
    ...overrides,
  }
}

describe('delegationEventLineText', () => {
  it.each<[Partial<DelegationEvent> & Pick<DelegationEvent, 'kind'>, string]>([
    [{ kind: 'delegated', agentName: 'Mia', title: 'Fix the gate' }, 'Delegated to Mia · Fix the gate'],
    [{ kind: 'started', agentName: 'Mia', title: 'Fix the gate' }, 'Mia started · Fix the gate'],
    [{ kind: 'finished', agentName: 'Mia', title: 'Fix the gate' }, 'Mia finished · Fix the gate'],
    [{ kind: 'stopped', agentName: 'Mia' }, 'Mia stopped without finishing'],
    [{ kind: 'steered', agentName: 'Mia' }, 'Sent Mia a new instruction'],
    [{ kind: 'answered', agentName: 'Mia' }, "Answered Mia's question"],
    [{ kind: 'cancelled', agentName: 'Mia' }, 'Stopped Mia'],
    [{ kind: 'cancelled', agentName: 'Mia', cascadeCount: 2 }, 'Stopped Mia and 2 below it'],
    [{ kind: 'follow_up', agentName: 'Mia', title: 'Check the logs' }, 'Gave Mia follow-up work · Check the logs'],
    [{ kind: 'refused', reason: 'depth limit' }, 'Delegation refused · depth limit'],
    [{ kind: 'bash_launched', command: 'npm test' }, 'Running in background · npm test'],
    [{ kind: 'bash_finished', command: 'npm test' }, 'npm test finished'],
    [{ kind: 'bash_failed', command: 'npm test', exitCode: 2 }, 'npm test failed (exit 2)'],
  ])('%j → %s', (overrides, expected) => {
    expect(delegationEventLineText(event(overrides))).toBe(expected)
  })

  it('does not append a title the spec line does not carry', () => {
    expect(delegationEventLineText(event({ kind: 'stopped', agentName: 'Mia', title: 'SECRET-TITLE' }))).toBe(
      'Mia stopped without finishing',
    )
    expect(delegationEventLineText(event({ kind: 'cancelled', agentName: 'Mia', cascadeCount: 0 }))).toBe('Stopped Mia')
  })
})

describe('delegationEventShowsOpen', () => {
  it('is true for a subagent line that names a child session', () => {
    expect(
      delegationEventShowsOpen(event({ kind: 'finished', agentName: 'Mia', childSessionId: 'child-1' })),
    ).toBe(true)
  })

  it('is false when there is no child session', () => {
    expect(delegationEventShowsOpen(event({ kind: 'finished', agentName: 'Mia' }))).toBe(false)
  })

  it('is false for a refusal even if a child id was attached by mistake', () => {
    expect(
      delegationEventShowsOpen(event({ kind: 'refused', reason: 'depth limit', childSessionId: 'child-1' })),
    ).toBe(false)
  })

  it.each(['bash_launched', 'bash_finished', 'bash_failed'] as const)(
    'is false for %s',
    (kind) => {
      expect(
        delegationEventShowsOpen(event({ kind, command: 'npm test', exitCode: 1, childSessionId: 'child-1' })),
      ).toBe(false)
    },
  )
})
