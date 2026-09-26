/**
 * WebSearchGroup.test.tsx — ADR-094 Settings screen (FR-012, FR-028, FR-031).
 *
 * The Web Search group must render roles and usability honestly from the
 * wire: a separate default radio stack and fallback radio stack (FR-012),
 * "No fallback" as a visible choice, the automatic-fallback label (R3),
 * the same-as-default healing explanation (R5), the FR-031 native-search
 * notice that names no provider, and SearXNG offered as no new choice (D10).
 *
 * Expected values derive from docs/internal/specs/web-search-provider-model-spec.md
 * ("Settings screen", "Resolution", R1–R9) and the generated contract
 * (src/lib/api/generated/openapi-types.ts), not from observed output.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import type { IntegrationProvider } from '@/lib/api'
import { WebSearchGroup } from './WebSearchGroup'

function row(overrides: Partial<IntegrationProvider> & Pick<IntegrationProvider, 'id'>): IntegrationProvider {
  return {
    kind: 'search',
    display_name: overrides.id.charAt(0).toUpperCase() + overrides.id.slice(1),
    configured: true,
    requires_key: true,
    active: false,
    usable: true,
    fallback: false,
    fallback_automatic: false,
    ...overrides,
  } as IntegrationProvider
}

const PROVIDERS: IntegrationProvider[] = [
  row({ id: 'brave' }),
  row({ id: 'tavily' }),
  row({ id: 'duckduckgo', requires_key: false, fallback: true, fallback_automatic: true, active: false }),
  row({ id: 'searxng', requires_key: false, configured: false, usable: false }),
]

function renderGroup(overrides: Record<string, unknown> = {}) {
  const props = {
    providers: PROVIDERS,
    defaultSearch: 'tavily',
    fallbackSearch: 'duckduckgo',
    fallbackIgnoredReason: undefined,
    nativeSearchInEffect: false,
    saving: false,
    onSetDefault: vi.fn(),
    onSetFallback: vi.fn(),
    onSetNoFallback: vi.fn(),
    ...overrides,
  }
  render(<WebSearchGroup {...props} />)
  return props
}

describe('WebSearchGroup — role stacks', () => {
  it('renders a default stack item for every choosable provider', () => {
    renderGroup()
    expect(screen.getByTestId('default-option-brave')).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByTestId('default-option-tavily')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('default-option-duckduckgo')).toBeInTheDocument()
  })

  it('checks the fallback stack item matching fallback_search and shows "No fallback" as a visible choice', () => {
    renderGroup()
    expect(screen.getByTestId('fallback-option-duckduckgo')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('fallback-option-none')).toHaveAttribute('aria-checked', 'false')
    expect(screen.getByTestId('fallback-option-none')).toHaveTextContent('No fallback')
  })

  it('checks only "No fallback" when fallback_search is null — an explicit none', () => {
    renderGroup({ fallbackSearch: null })
    expect(screen.getByTestId('fallback-option-none')).toHaveAttribute('aria-checked', 'true')
    expect(screen.getByTestId('fallback-option-duckduckgo')).toHaveAttribute('aria-checked', 'false')
  })

  it('checks nothing when roles are undecided (fields absent) and says so — distinct from an explicit none', () => {
    renderGroup({ defaultSearch: undefined, fallbackSearch: undefined })
    // Absent fields mean "not yet decided" (migration deferred), not "none":
    // no item in either stack may claim a selection.
    expect(screen.getByTestId('roles-undecided')).toBeInTheDocument()
    for (const item of screen.getAllByTestId(/^fallback-option-/)) {
      expect(item).toHaveAttribute('aria-checked', 'false')
    }
    for (const item of screen.getAllByTestId(/^default-option-/)) {
      expect(item).toHaveAttribute('aria-checked', 'false')
    }
  })

  it('disables the default provider in the fallback stack and states the reason', () => {
    renderGroup()
    const tavilyInFallback = screen.getByTestId('fallback-option-tavily')
    expect(tavilyInFallback).toBeDisabled()
    expect(tavilyInFallback).toHaveTextContent('A provider cannot fall back to itself.')
    expect(screen.getByTestId('fallback-option-brave')).toBeEnabled()
  })

  it('labels the automatic fallback (stored value absent, R3 resolved to DuckDuckGo)', () => {
    renderGroup()
    expect(screen.getByTestId('fallback-option-duckduckgo')).toHaveTextContent('Automatic fallback')
  })

  it('labels a picked fallback as operator-chosen, not automatic', () => {
    renderGroup({ fallbackSearch: 'brave' })
    expect(screen.getByTestId('fallback-option-brave')).not.toHaveTextContent('Automatic fallback')
  })

  it('states that the list order does not choose who is tried first (FR-012)', () => {
    renderGroup()
    expect(screen.getByText(/The order of this list does not choose who is tried first\./)).toBeInTheDocument()
  })

  it('clicking an unchecked default option reports that id; clicking the checked one reports nothing', () => {
    const props = renderGroup()
    fireEvent.click(screen.getByTestId('default-option-brave'))
    expect(props.onSetDefault).toHaveBeenCalledTimes(1)
    expect(props.onSetDefault).toHaveBeenCalledWith('brave')
    fireEvent.click(screen.getByTestId('default-option-tavily'))
    expect(props.onSetDefault).toHaveBeenCalledTimes(1)
  })

  it('clicking an unchecked fallback option reports that id; "No fallback" reports the none choice', () => {
    const props = renderGroup()
    fireEvent.click(screen.getByTestId('fallback-option-brave'))
    expect(props.onSetFallback).toHaveBeenCalledTimes(1)
    expect(props.onSetFallback).toHaveBeenCalledWith('brave')
    fireEvent.click(screen.getByTestId('fallback-option-none'))
    expect(props.onSetNoFallback).toHaveBeenCalledTimes(1)
  })
})

describe('WebSearchGroup — notices', () => {
  it('shows the FR-031 native-search notice when native_search_in_effect is true', () => {
    renderGroup({ nativeSearchInEffect: true })
    const notice = screen.getByTestId('native-search-notice')
    expect(notice).toBeInTheDocument()
    expect(notice).toHaveTextContent(/native/i)
    expect(notice).toHaveTextContent(/not currently deciding who answers/i)
  })

  it('does not show the native-search notice when native_search_in_effect is false or absent', () => {
    renderGroup({ nativeSearchInEffect: false })
    expect(screen.queryByTestId('native-search-notice')).not.toBeInTheDocument()
    renderGroup({ nativeSearchInEffect: undefined })
    expect(screen.queryByTestId('native-search-notice')).not.toBeInTheDocument()
  })

  it('the FR-031 notice names no provider as the one that answers', () => {
    renderGroup({ nativeSearchInEffect: true })
    const notice = screen.getByTestId('native-search-notice')
    for (const name of ['Brave', 'Tavily', 'DuckDuckGo', 'SearXNG']) {
      expect(notice.textContent).not.toContain(name)
    }
  })

  it('explains the ignored fallback when fallback_ignored_reason is present (R5)', () => {
    renderGroup({ fallbackSearch: null, fallbackIgnoredReason: 'same_as_default' })
    const notice = screen.getByTestId('fallback-ignored-notice')
    expect(notice).toHaveTextContent(/ignored/i)
    expect(notice).toHaveTextContent(/cannot fall back to itself/i)
  })

  it('shows no ignored-fallback explanation when fallback_ignored_reason is absent', () => {
    renderGroup({ fallbackIgnoredReason: undefined })
    expect(screen.queryByTestId('fallback-ignored-notice')).not.toBeInTheDocument()
  })
})

describe('WebSearchGroup — SearXNG and controls that would be ignored', () => {
  it('offers SearXNG as no new choice in either stack (D10 — descoped)', () => {
    renderGroup()
    expect(screen.queryByTestId('default-option-searxng')).not.toBeInTheDocument()
    expect(screen.queryByTestId('fallback-option-searxng')).not.toBeInTheDocument()
  })

  it('shows the SearXNG current default as text only, with no editor for it', () => {
    renderGroup({ defaultSearch: 'searxng' })
    const note = screen.getByTestId('searxng-default-note')
    expect(note).toHaveTextContent(/SearXNG/)
    expect(note).toHaveTextContent(/current default/i)
    expect(screen.queryByTestId('default-option-searxng')).not.toBeInTheDocument()
  })

  it('offers no depth or site-filter control for any provider — a control whose value would be ignored must not exist (US-5)', () => {
    const { container } = render(
      <WebSearchGroup
        providers={[row({ id: 'tavily' }), row({ id: 'duckduckgo', requires_key: false })]}
        defaultSearch="tavily"
        fallbackSearch={null}
        nativeSearchInEffect={false}
        saving={false}
        onSetDefault={vi.fn()}
        onSetFallback={vi.fn()}
        onSetNoFallback={vi.fn()}
      />,
    )
    expect(container.querySelector('[data-testid^="depth-"]')).toBeNull()
    expect(container.querySelector('[data-testid^="site-filter-"]')).toBeNull()
    expect(container.querySelector('[data-testid^="include-domains-"]')).toBeNull()
    expect(container.querySelector('[data-testid^="exclude-domains-"]')).toBeNull()
  })
})
