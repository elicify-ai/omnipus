/**
 * RollupBadge.test.tsx — unit tests for the parent-card delegation roll-up.
 *
 * Covers the roll-up logic the Board relies on:
 *   - empty / absent roll-up renders nothing (null guard),
 *   - the count reflects ACTIVE (in_progress) sub-agents when any are live,
 *     and the total otherwise, with correct "running" vs "delegated" wording,
 *   - the avatar row is capped at 5 with a "+N" overflow indicator.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { RollupBadge, type RollupItem } from './RollupBadge'
import type { Agent } from '@/lib/api'

// framer-motion's animate prop is irrelevant to the assertions — render a plain span.
vi.mock('framer-motion', () => ({
  motion: {
    span: ({ children, ...props }: React.HTMLAttributes<HTMLSpanElement>) => (
      <span {...props}>{children}</span>
    ),
  },
}))

function item(over: Partial<RollupItem> = {}): RollupItem {
  return { agent_id: 'a', label: 'Agent', status: 'in_progress', ...over }
}

const agents: Agent[] = []

describe('RollupBadge', () => {
  it('renders nothing for an empty roll-up (null guard)', () => {
    const { container } = render(<RollupBadge rollup={[]} agents={agents} />)
    expect(container.firstChild).toBeNull()
  })

  it('counts ACTIVE sub-agents and says "running" when any are in_progress', () => {
    render(
      <RollupBadge
        rollup={[
          item({ agent_id: 'a', status: 'in_progress' }),
          item({ agent_id: 'b', status: 'in_progress' }),
          item({ agent_id: 'c', status: 'done' }),
        ]}
        agents={agents}
      />,
    )
    // 2 active → "2 sub-agents running"
    expect(screen.getByText(/2 sub-agents running/)).toBeInTheDocument()
  })

  it('falls back to the TOTAL count and "delegated" when nothing is live', () => {
    render(
      <RollupBadge
        rollup={[
          item({ agent_id: 'a', status: 'done' }),
          item({ agent_id: 'b', status: 'failed' }),
          item({ agent_id: 'c', status: 'blocked' }),
        ]}
        agents={agents}
      />,
    )
    expect(screen.getByText(/3 sub-agents delegated/)).toBeInTheDocument()
  })

  it('uses the singular "sub-agent" for a count of one', () => {
    render(<RollupBadge rollup={[item({ status: 'done' })]} agents={agents} />)
    expect(screen.getByText(/1 sub-agent delegated/)).toBeInTheDocument()
  })

  it('caps the avatar row at 5 and shows a "+N" overflow indicator', () => {
    const rollup = Array.from({ length: 8 }, (_, i) =>
      item({ agent_id: `a${i}`, status: 'done' }),
    )
    render(<RollupBadge rollup={rollup} agents={agents} />)
    const avatarList = screen.getByRole('list', { name: 'Sub-agent avatars' })
    // 5 rendered avatars max…
    expect(within(avatarList).getAllByRole('listitem')).toHaveLength(5)
    // …plus a "+3" overflow label.
    expect(screen.getByLabelText('and 3 more')).toHaveTextContent('+3')
  })
})
