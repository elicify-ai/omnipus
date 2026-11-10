/**
 * ModelFooter — extracted from the duplicated JSX that lived in
 * MessageItem.tsx (live render) and ChatScreen.tsx VirtualAssistantMessageRow
 * (replay). This component renders the per-turn model slug in the message
 * footer when present and renders nothing otherwise.
 *
 * FR-014: per-turn model record. Spec §18 Q6: legacy turns (no model
 * field) MUST NOT show any model info — no placeholder text, no
 * "(model not recorded)" string.
 */

import { describe, it, expect, beforeAll } from 'vitest'
import { act, fireEvent, render, screen } from '@testing-library/react'
import { ModelFooter } from './ModelFooter'

beforeAll(() => {
  // jsdom shims the repo's Radix suites use (tooltip.radix.test.tsx).
  Element.prototype.hasPointerCapture ??= () => false
  Element.prototype.scrollIntoView ??= () => {}
  globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver
})

describe('ModelFooter', () => {
  it('renders the model slug in a monospace span with the model test id', () => {
    render(<ModelFooter model="z-ai/glm-5-turbo" />)
    const span = screen.getByTestId('message-model')
    expect(span).toBeInTheDocument()
    expect(span.textContent).toBe('z-ai/glm-5-turbo')
    expect(span.className).toMatch(/font-mono/)
    expect(span.className).toMatch(/text-\[var\(--color-muted\)\]/)
    expect(span.className).toMatch(/truncate/)
  })

  it('renders nothing when the model field is absent', () => {
    const { container } = render(<ModelFooter model={undefined} />)
    expect(screen.queryByTestId('message-model')).toBeNull()
    expect(container.firstChild).toBeNull()
  })

  it('renders nothing when the model field is an empty string', () => {
    const { container } = render(<ModelFooter model="" />)
    expect(screen.queryByTestId('message-model')).toBeNull()
    expect(container.firstChild).toBeNull()
  })

  it('renders nothing when the model field is only whitespace', () => {
    const { container } = render(<ModelFooter model="   " />)
    expect(screen.queryByTestId('message-model')).toBeNull()
    expect(container.firstChild).toBeNull()
  })

  it('trims surrounding whitespace from the displayed model slug', () => {
    render(<ModelFooter model="  z-ai/glm-5-turbo  " />)
    const span = screen.getByTestId('message-model')
    expect(span.textContent).toBe('z-ai/glm-5-turbo')
  })
})

/**
 * Turn statistics tooltip (effort levels US-3 AS6, US-7, FR-024/FR-036,
 * FR-058) on the Radix Tooltip (src/components/ui/tooltip.tsx). Expected
 * values come from the spec lines quoted in ModelFooter's doc comment
 * ("Effort: High", "Thinking: N tokens (reported by the provider)",
 * "Thinking: not reported") and from the old Tooltip's `label` prop, which
 * named the trigger "<model>, turn statistics" (preserved across the Radix
 * migration). Touch does not open a Radix tooltip (founder Q2 = A).
 */
describe('ModelFooter — turn statistics tooltip', () => {
  const MODEL = 'z-ai/glm-5-turbo'
  const TRIGGER_NAME = `${MODEL}, turn statistics`

  it('gives the statistics trigger the accessible name "<model>, turn statistics" and makes it a tab stop', () => {
    render(<ModelFooter model={MODEL} effort={{ chosen: 'high', sent: 'high' }} turnReasoningTokens={120} />)
    const trigger = screen.getByLabelText(TRIGGER_NAME)
    expect(trigger.tabIndex).toBe(0)
    // The visible slug stays inside the trigger, unchanged.
    expect(trigger).toContainElement(screen.getByTestId('message-model'))
    expect(screen.getByTestId('message-model').textContent).toBe(MODEL)
  })

  it('shows the effort line and the reported thinking figure on keyboard focus, and hides them on blur', () => {
    render(<ModelFooter model={MODEL} effort={{ chosen: 'high', sent: 'high' }} turnReasoningTokens={120} />)
    const trigger = screen.getByLabelText(TRIGGER_NAME)
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
    act(() => trigger.focus())
    const bubble = screen.getByRole('tooltip')
    expect(bubble).toHaveTextContent('Effort: High')
    expect(bubble).toHaveTextContent('Thinking: 120 tokens (reported by the provider)')
    expect(bubble).not.toHaveTextContent(/output/i)
    act(() => trigger.blur())
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  it('shows "Thinking: not reported" on mouse hover when the turn has an effort record but no thinking figure', async () => {
    render(<ModelFooter model={MODEL} effort={{ chosen: 'high', sent: 'high' }} />)
    fireEvent.pointerMove(screen.getByLabelText(TRIGGER_NAME), { pointerType: 'mouse', pointerId: 1 })
    const bubble = await screen.findByRole('tooltip')
    expect(bubble).toHaveTextContent('Effort: High')
    expect(bubble).toHaveTextContent('Thinking: not reported')
  })

  it('shows only the thinking line when the turn has a thinking figure but no effort record', () => {
    render(<ModelFooter model={MODEL} turnReasoningTokens={1} />)
    act(() => screen.getByLabelText(TRIGGER_NAME).focus())
    const bubble = screen.getByRole('tooltip')
    expect(bubble).toHaveTextContent('Thinking: 1 token (reported by the provider)')
    expect(bubble).not.toHaveTextContent('Effort:')
  })

  it('does not open on a touch pointer (founder Q2 = A: Radix ignores touch)', async () => {
    render(<ModelFooter model={MODEL} effort={{ chosen: 'high', sent: 'high' }} turnReasoningTokens={120} />)
    fireEvent.pointerMove(screen.getByLabelText(TRIGGER_NAME), { pointerType: 'touch', pointerId: 1 })
    await act(() => new Promise((resolve) => setTimeout(resolve, 5)))
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })

  it('renders a pre-feature turn (no effort record, no thinking figure) as the plain slug: no tab stop, no statistics name', () => {
    render(<ModelFooter model={MODEL} />)
    expect(screen.queryByLabelText(TRIGGER_NAME)).toBeNull()
    const slug = screen.getByTestId('message-model')
    expect(slug.hasAttribute('tabindex')).toBe(false)
    act(() => slug.focus())
    expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
  })
})
