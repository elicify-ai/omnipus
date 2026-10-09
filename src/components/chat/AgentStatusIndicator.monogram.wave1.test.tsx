// The 5th figure, Monogram, in the chat feed indicator (W1-7).
// ARCH-RULING-monogram AC-16 / D3: an agent with figure=Monogram and a name
// renders its initial in the indicator; when no name is available the mark is
// suppressed and the state phrase still renders (the indicator does not own the
// phrase). Oracles are the DOM seams — data-figure / data-initial — never a
// snapshot.
import * as React from 'react'
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { AgentStatusIndicator as TypedIndicator } from './AgentStatusIndicator'

// The indicator gains a `name` prop with the Monogram work. Until then it is
// rendered through a loose view so this RED test is collected rather than
// blocked at compile time — the same loose-load idiom the agent-icon suite uses.
const AgentStatusIndicator = TypedIndicator as unknown as (
  props: {
    phase: 'thinking' | 'working' | 'waiting' | 'unavailable'
    label: string | null
    figure?: string
    role?: string
    color?: string
    name?: string
  },
) => React.ReactElement

describe('AgentStatusIndicator Monogram', () => {
  it('draws the Monogram initial from the agent name and keeps the phrase (AC-16)', () => {
    const { container } = render(
      <AgentStatusIndicator
        phase="thinking"
        label="Thinking…"
        figure="Monogram"
        role="general"
        color="#3B82F6"
        name="Daniel"
      />,
    )
    const mark = container.querySelector('[data-testid="agent-icon"]')
    expect(mark).not.toBeNull()
    expect(mark).toHaveAttribute('data-figure', 'Monogram')
    expect(container.querySelector('[data-initial]')?.getAttribute('data-initial')).toBe('D')
    expect(container.textContent).toContain('Thinking…')
  })

  it('suppresses the Monogram mark when no name is available and still shows the phrase (AC-16)', () => {
    const { container } = render(
      <AgentStatusIndicator
        phase="thinking"
        label="Thinking…"
        figure="Monogram"
        role="general"
        color="#3B82F6"
      />,
    )
    // No name → the mark is not drawn (the existing incomplete-identity
    // pattern), but the phrase the indicator renders is unaffected.
    expect(container.querySelector('[data-testid="agent-icon"]')).toBeNull()
    expect(container.querySelector('[data-initial]')).toBeNull()
    expect(container.textContent).toContain('Thinking…')
  })
})
