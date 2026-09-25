/**
 * Live bubble placement. assistant-ui hands ToolGroup the tool parts only,
 * with startIndex pointing into the full message content. The grey lines
 * for that call sit directly after the tool-call child.
 */
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { DelegationEvent } from '@/lib/delegationEvents.types'
import { DelegationInlineProvider, DelegationLiveTail, DelegationToolGroup, useClaimedCallIds } from './DelegationEventLine'

const messageBox = vi.hoisted(() => ({
  current: {
    id: 'm-live',
    role: 'assistant' as const,
    status: { type: 'complete' as const },
    content: [] as Array<{ type: string; text?: string; toolCallId?: string }>,
  },
}))

vi.mock('@assistant-ui/react', () => ({
  useMessage: () => messageBox.current,
}))

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
}))

function event(overrides: Partial<DelegationEvent> & Pick<DelegationEvent, 'id' | 'kind' | 'at'>): DelegationEvent {
  return {
    sessionId: 'parent',
    agentName: 'General Purpose',
    title: 'Wire the gate',
    childSessionId: 'child-gp',
    ...overrides,
  }
}

describe('DelegationToolGroup', () => {
  it('places the delegated and finished lines after the tool-call child and before the following text', () => {
    messageBox.current = {
      id: 'm-live',
      role: 'assistant',
      status: { type: 'complete' },
      content: [
        { type: 'text', text: "I'll hand this to the worker." },
        { type: 'tool-call', toolCallId: 'call-wire' },
        { type: 'text', text: 'The worker has finished. All done.' },
      ],
    }
    const byCall = new Map<string, readonly DelegationEvent[]>([
      [
        'call-wire',
        [
          event({ id: 'delegated:span-wire', kind: 'delegated', at: 1 }),
          event({ id: 'finished:span-wire', kind: 'finished', at: 2 }),
        ],
      ],
    ])

    render(
      <DelegationInlineProvider byCall={byCall}>
        <DelegationToolGroup startIndex={1} endIndex={2}>
          <span data-testid="tool-call-part">delegate call</span>
          <span>The worker has finished. All done.</span>
        </DelegationToolGroup>
      </DelegationInlineProvider>,
    )

    const tool = screen.getByTestId('tool-call-part')
    const delegated = screen.getByText('Delegated to General Purpose · Wire the gate')
    const finished = screen.getByText('General Purpose finished · Wire the gate')
    const after = screen.getByText('The worker has finished. All done.')
    const following = Node.DOCUMENT_POSITION_FOLLOWING
    expect(tool.compareDocumentPosition(delegated) & following).toBeTruthy()
    expect(delegated.compareDocumentPosition(finished) & following).toBeTruthy()
    expect(finished.compareDocumentPosition(after) & following).toBeTruthy()
  })

  it('does not also draw a line after the bubble when the tool part was rendered', () => {
    messageBox.current = {
      id: 'm-live',
      role: 'assistant',
      status: { type: 'complete' },
      content: [
        { type: 'text', text: "I'll hand this to the worker." },
        { type: 'tool-call', toolCallId: 'call-wire' },
        { type: 'text', text: 'The worker has finished. All done.' },
      ],
    }
    const byCall = new Map<string, readonly DelegationEvent[]>([
      ['call-wire', [event({ id: 'delegated:span-wire', kind: 'delegated', at: 1 })]],
    ])

    function LiveBubble() {
      const { claimed, reportMatched } = useClaimedCallIds()
      return (
        <DelegationInlineProvider byCall={byCall} reportMatched={reportMatched}>
          <DelegationToolGroup startIndex={1} endIndex={1}>
            <span data-testid="tool-call-part">delegate call</span>
          </DelegationToolGroup>
          <span>The worker has finished. All done.</span>
          <DelegationLiveTail byCall={byCall} trailing={[]} claimed={claimed} />
        </DelegationInlineProvider>
      )
    }

    render(<LiveBubble />)

    const lines = screen.getAllByText('Delegated to General Purpose · Wire the gate')
    expect(lines).toHaveLength(1)
    const tool = screen.getByTestId('tool-call-part')
    const after = screen.getByText('The worker has finished. All done.')
    const following = Node.DOCUMENT_POSITION_FOLLOWING
    expect(tool.compareDocumentPosition(lines[0]) & following).toBeTruthy()
    expect(lines[0].compareDocumentPosition(after) & following).toBeTruthy()
  })
})
