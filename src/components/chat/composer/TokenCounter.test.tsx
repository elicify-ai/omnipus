/**
 * TokenCounter tests.
 *
 * TokenCounter (extracted verbatim from ChatControls per the composer-redesign
 * plan — see the design assets in .preview-doc/composer-redesign/) is a
 * read-only display: `sessionTokens`/`isStreaming` from the chat store,
 * rendered through `formatTokens` (sourced from src/lib/formatTokens.ts).
 * Same testids (`session-token-counter`, `session-token-value`) as the
 * pre-extraction ChatControls implementation.
 *
 * Scaffolding ported from src/components/chat/ChatControls.test.tsx
 * (describe('formatTokens (re-exported from ChatControls)') and
 * describe('ChatControls — Token counter')).
 */
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { act } from 'react'
import { useChatStore } from '@/store/chat'
import { TokenCounter } from './TokenCounter'
import { formatTokens } from '@/lib/formatTokens'

// ── Store reset ───────────────────────────────────────────────────────────────

beforeEach(() => {
  act(() => {
    useChatStore.setState({ sessionTokens: 0, isStreaming: false })
  })
})

// ── Render + testids ───────────────────────────────────────────────────────────

describe('TokenCounter — renders testids', () => {
  it('renders the container with data-testid="session-token-counter"', () => {
    act(() => {
      useChatStore.setState({ sessionTokens: 44000 })
    })
    render(<TokenCounter />)
    expect(screen.getByTestId('session-token-counter')).toBeInTheDocument()
  })

  it('renders the value span with data-testid="session-token-value" showing the formatted count + "tokens"', () => {
    act(() => {
      useChatStore.setState({ sessionTokens: 44000 })
    })
    render(<TokenCounter />)
    const value = screen.getByTestId('session-token-value')
    expect(value.textContent).toBe('44.0k tokens')
  })

  it('DIFFERENTIATION: a different sessionTokens value renders different text (not a hardcoded display)', () => {
    act(() => {
      useChatStore.setState({ sessionTokens: 250 })
    })
    render(<TokenCounter />)
    const value = screen.getByTestId('session-token-value')
    expect(value.textContent).toBe('250 tokens')
    expect(value.textContent).not.toBe('44.0k tokens')
  })

  it('sets an aria-label on the container reflecting the current sessionTokens value', () => {
    act(() => {
      useChatStore.setState({ sessionTokens: 12345 })
    })
    render(<TokenCounter />)
    const counter = screen.getByTestId('session-token-counter')
    expect(counter.getAttribute('aria-label')).toBe('12345 tokens used')
  })

  it('DIFFERENTIATION: aria-label changes with a different sessionTokens value', () => {
    act(() => {
      useChatStore.setState({ sessionTokens: 999 })
    })
    const { rerender } = render(<TokenCounter />)
    expect(screen.getByTestId('session-token-counter').getAttribute('aria-label')).toBe('999 tokens used')

    act(() => {
      useChatStore.setState({ sessionTokens: 2_000_000 })
    })
    rerender(<TokenCounter />)
    expect(screen.getByTestId('session-token-counter').getAttribute('aria-label')).toBe('2000000 tokens used')
  })

  it('applies a custom className passed through to the container', () => {
    act(() => {
      useChatStore.setState({ sessionTokens: 0 })
    })
    render(<TokenCounter className="hidden @2xl:flex" />)
    const counter = screen.getByTestId('session-token-counter')
    expect(counter.className).toContain('hidden')
    expect(counter.className).toContain('@2xl:flex')
  })
})

// ── formatTokens (source of truth: src/lib/formatTokens.ts) ────────────────────

describe('formatTokens (source: src/lib/formatTokens.ts)', () => {
  it('formats 0 as "0"', () => {
    expect(formatTokens(0)).toBe('0')
  })
  it('formats a small value (999) as-is, with no suffix', () => {
    expect(formatTokens(999)).toBe('999')
  })
  it('formats the k-boundary (1000) as "1.0k"', () => {
    expect(formatTokens(1000)).toBe('1.0k')
  })
  it('formats a representative mid-range value (44000) as "44.0k"', () => {
    expect(formatTokens(44000)).toBe('44.0k')
  })
  it('formats a non-round k value (12345) as "12.3k" (one decimal, not rounded to an integer)', () => {
    expect(formatTokens(12345)).toBe('12.3k')
  })
  it('formats the M-boundary (1_000_000) as "1.0M"', () => {
    expect(formatTokens(1_000_000)).toBe('1.0M')
  })
  it('formats a representative large value (1_200_000) as "1.2M"', () => {
    expect(formatTokens(1_200_000)).toBe('1.2M')
  })
  it('DIFFERENTIATION: distinct inputs across all three magnitude bands produce distinct, non-hardcoded outputs', () => {
    const outputs = [0, 500, 1000, 250_000, 1_000_000, 7_500_000].map(formatTokens)
    expect(new Set(outputs).size).toBe(outputs.length)
    expect(outputs).toEqual(['0', '500', '1.0k', '250.0k', '1.0M', '7.5M'])
  })
})

// ── Streaming state behavior ────────────────────────────────────────────────────

describe('TokenCounter — streaming state', () => {
  it('when NOT streaming: the icon has no animate-spin class', () => {
    act(() => {
      useChatStore.setState({ sessionTokens: 100, isStreaming: false })
    })
    const { container } = render(<TokenCounter />)
    expect(container.querySelector('.animate-spin')).toBeNull()
  })

  it('when streaming: the icon gains the animate-spin + accent-color class', () => {
    act(() => {
      useChatStore.setState({ sessionTokens: 100, isStreaming: true })
    })
    const { container } = render(<TokenCounter />)
    const svg = container.querySelector('svg')
    expect(svg).not.toBeNull()
    expect(svg?.getAttribute('class')).toContain('animate-spin')
    expect(svg?.getAttribute('class')).toContain('text-[var(--color-accent)]')
  })

  it('when NOT streaming: the token-value span does not carry the secondary-color streaming class', () => {
    act(() => {
      useChatStore.setState({ sessionTokens: 100, isStreaming: false })
    })
    render(<TokenCounter />)
    const value = screen.getByTestId('session-token-value')
    expect(value.className).not.toContain('text-[var(--color-secondary)]')
  })

  it('when streaming: the token-value span carries the secondary-color streaming class', () => {
    act(() => {
      useChatStore.setState({ sessionTokens: 100, isStreaming: true })
    })
    render(<TokenCounter />)
    const value = screen.getByTestId('session-token-value')
    expect(value.className).toContain('text-[var(--color-secondary)]')
  })

  it('DIFFERENTIATION: toggling isStreaming with the SAME sessionTokens value changes the rendered className but not the token text', () => {
    act(() => {
      useChatStore.setState({ sessionTokens: 5000, isStreaming: false })
    })
    const { container, rerender } = render(<TokenCounter />)
    const valueBefore = screen.getByTestId('session-token-value')
    expect(valueBefore.textContent).toBe('5.0k tokens')
    expect(container.querySelector('.animate-spin')).toBeNull()

    act(() => {
      useChatStore.setState({ isStreaming: true })
    })
    rerender(<TokenCounter />)
    const valueAfter = screen.getByTestId('session-token-value')
    // Token text is unchanged (sessionTokens didn't change)...
    expect(valueAfter.textContent).toBe('5.0k tokens')
    // ...but the streaming-only visual state DID change, proving the
    // component actually reacts to isStreaming rather than ignoring it.
    expect(container.querySelector('.animate-spin')).not.toBeNull()
    expect(valueAfter.className).toContain('text-[var(--color-secondary)]')
  })
})
