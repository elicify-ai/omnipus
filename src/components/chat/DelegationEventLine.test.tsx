/**
 * One muted delegation line. AC-8: [open] navigates to the child session.
 * AC-10 render half: a refusal shows its reason and no [open].
 */
import { describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { DelegationEvent, DelegationEventKind } from '@/lib/delegationEvents.types'
import { DelegationEventLine } from './DelegationEventLine'

const mockNavigate = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => mockNavigate,
}))

function event(overrides: Partial<DelegationEvent> & Pick<DelegationEvent, 'kind'>): DelegationEvent {
  return {
    id: overrides.id ?? `${overrides.kind}:1`,
    sessionId: 'parent',
    at: 1,
    ...overrides,
  }
}

const SUBAGENT_KINDS: DelegationEventKind[] = [
  'delegated',
  'started',
  'finished',
  'stopped',
  'steered',
  'answered',
  'cancelled',
  'follow_up',
]

describe('DelegationEventLine', () => {
  it('renders the sentence and nothing else from the event', () => {
    render(
      <DelegationEventLine
        event={event({
          kind: 'finished',
          agentName: 'Mia',
          title: 'Fix the gate',
          childSessionId: 'child-9',
        })}
      />,
    )
    const line = screen.getByTestId('delegation-event-line')
    expect(line).toHaveTextContent('Mia finished · Fix the gate')
    expect(line).not.toHaveTextContent('→')
    expect(line).not.toHaveTextContent('✓')
  })

  it('AC-8: every subagent line\'s [open] navigates to that child session', async () => {
    const user = userEvent.setup()
    render(
      <>
        {SUBAGENT_KINDS.map((kind, index) => (
          <DelegationEventLine
            key={kind}
            event={event({
              id: kind,
              kind,
              agentName: 'Mia',
              title: 'Task',
              childSessionId: `child-${index}`,
            })}
          />
        ))}
      </>,
    )

    const lines = screen.getAllByTestId('delegation-event-line')
    expect(lines).toHaveLength(SUBAGENT_KINDS.length)
    for (let index = 0; index < lines.length; index += 1) {
      const open = within(lines[index]).getByRole('button', { name: '[open]' })
      await user.click(open)
      expect(mockNavigate).toHaveBeenLastCalledWith({
        to: '/sessions/$sessionId',
        params: { sessionId: `child-${index}` },
      })
    }
  })

  it('AC-10: a refusal shows its reason and no [open]', () => {
    render(
      <>
        <DelegationEventLine
          event={event({ kind: 'delegated', agentName: 'Mia', title: 'Task', childSessionId: 'child-1' })}
        />
        <DelegationEventLine
          event={event({
            id: 'refused:1',
            kind: 'refused',
            reason: 'depth limit',
            childSessionId: 'should-not-open',
          })}
        />
      </>,
    )
    // Non-vacuity: [open] can render on this screen, so its absence below is real.
    expect(screen.getByRole('button', { name: '[open]' })).toBeInTheDocument()
    const refusal = screen.getByText('Delegation refused · depth limit').closest('[data-testid="delegation-event-line"]')
    expect(refusal).not.toBeNull()
    expect(within(refusal as HTMLElement).queryByRole('button', { name: '[open]' })).toBeNull()
  })

  it('a background command line has no [open]', () => {
    render(
      <DelegationEventLine event={event({ kind: 'bash_failed', command: 'npm test', exitCode: 1 })} />,
    )
    expect(screen.getByText('npm test failed (exit 1)')).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '[open]' })).toBeNull()
  })
})
