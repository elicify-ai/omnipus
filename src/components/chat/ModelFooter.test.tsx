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

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ModelFooter } from './ModelFooter'

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