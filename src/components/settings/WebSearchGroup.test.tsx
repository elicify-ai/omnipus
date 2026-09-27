/**
 * WebSearchGroup.test.tsx — ADR-096 Settings screen (FR-012, FR-028, FR-031).
 *
 * The Web Search group must render roles and usability honestly from the
 * wire, drawn the way the spec draws it (spec § "Settings screen"): ONE row
 * per provider, the default radio and the fallback radio live ON the
 * provider's row, "No fallback" is a visible distinct choice, and the two
 * radio groups exist semantically across rows. The default row's fallback
 * radio is disabled with its reason visible; the R3 automatic fallback is
 * labelled; the FR-031 native-search notice names no provider; SearXNG is
 * offered as no new choice (D10).
 *
 * Expected values derive from docs/internal/specs/web-search-provider-model-spec.md
 * ("Settings screen", "Resolution", R1–R9) and the generated contract
 * (src/lib/api/generated/openapi-types.ts), not from observed output.
 */

import { describe, it, expect, vi } from 'vitest'
import { cleanup, render, screen, fireEvent } from '@testing-library/react'
import type { IntegrationProvider } from '@/lib/api'
import {
  WebSearchGroup,
  WebSearchRowRoles,
  WebSearchNoFallbackChoice,
} from './WebSearchGroup'

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

const BRAVE = row({ id: 'brave' })
const TAVILY = row({ id: 'tavily' })
const DUCKDUCKGO = row({ id: 'duckduckgo', requires_key: false, fallback: true, fallback_automatic: true })

function renderRoles(
  overrides: {
    provider?: IntegrationProvider
    defaultSearch?: string
    fallbackSearch?: string | null
    saving?: boolean
  } = {},
) {
  const props = {
    provider: BRAVE,
    defaultSearch: 'tavily' as string | undefined,
    fallbackSearch: 'duckduckgo' as string | null | undefined,
    saving: false,
    onSetDefault: vi.fn(),
    onSetFallback: vi.fn(),
    ...overrides,
  }
  // A second render in one test must replace the first. Testing Library only
  // unmounts after the test, so getByTestId would otherwise see two radios.
  cleanup()
  const view = render(<WebSearchRowRoles {...props} />)
  return { ...props, ...view }
}

describe('WebSearchRowRoles — one row, both radios (spec "Same row")', () => {
  it('checks the default radio on the row whose id default_search names', () => {
    renderRoles({ provider: TAVILY, defaultSearch: 'tavily' })
    expect(screen.getByTestId('default-radio-tavily')).toHaveAttribute('data-state', 'checked')
  })

  it('checks no default radio when roles are undecided (fields absent) — distinct from a stored none', () => {
    renderRoles({ provider: TAVILY, defaultSearch: undefined })
    expect(screen.getByTestId('default-radio-tavily')).toHaveAttribute('data-state', 'unchecked')
  })

  it('checks the fallback radio on the row whose id fallback_search names', () => {
    renderRoles({ provider: DUCKDUCKGO, fallbackSearch: 'duckduckgo' })
    expect(screen.getByTestId('fallback-radio-duckduckgo')).toHaveAttribute('data-state', 'checked')
  })

  it('checks no fallback radio when fallback_search is absent or null — undecided vs explicit none', () => {
    renderRoles({ provider: BRAVE, fallbackSearch: undefined })
    expect(screen.getByTestId('fallback-radio-brave')).toHaveAttribute('data-state', 'unchecked')
    renderRoles({ provider: BRAVE, fallbackSearch: null })
    expect(screen.getByTestId('fallback-radio-brave')).toHaveAttribute('data-state', 'unchecked')
  })

  it('disables the fallback radio on the default row and states the reason under it (spec "Same row")', () => {
    renderRoles({ provider: TAVILY, defaultSearch: 'tavily' })
    expect(screen.getByTestId('fallback-radio-tavily')).toBeDisabled()
    expect(screen.getByTestId('fallback-disabled-reason-tavily')).toHaveTextContent(
      'A provider cannot fall back to itself.',
    )
  })

  it('enables the fallback radio on every non-default row', () => {
    renderRoles({ provider: BRAVE, defaultSearch: 'tavily' })
    expect(screen.getByTestId('fallback-radio-brave')).toBeEnabled()
  })

  it('labels the R3 automatic fallback on the row that is the fallback without a stored pick', () => {
    renderRoles({ provider: DUCKDUCKGO, fallbackSearch: 'duckduckgo' })
    expect(screen.getByTestId('automatic-fallback-duckduckgo')).toHaveTextContent('Automatic fallback')
  })

  it('does not label an operator-chosen fallback as automatic', () => {
    renderRoles({ provider: BRAVE, fallbackSearch: 'brave' })
    expect(screen.queryByTestId('automatic-fallback-brave')).not.toBeInTheDocument()
  })

  it('does not label the default row automatic even if its payload flags fallback_automatic', () => {
    const tavily = row({ id: 'tavily', fallback_automatic: true })
    renderRoles({ provider: tavily, defaultSearch: 'tavily' })
    expect(screen.queryByTestId('automatic-fallback-tavily')).not.toBeInTheDocument()
  })

  it('clicking an unchecked default radio reports that id; clicking the checked one reports nothing', () => {
    // The checked radio is the one default_search already names. A click that
    // changes nothing must not report: the spec's save is a role change, and
    // the section test "clicking the already-selected default fires no gated
    // save" locks the same rule. The previous assertion clicked that checked
    // radio and expected a call, which contradicted this test's own title.
    const props = renderRoles({ provider: TAVILY, defaultSearch: 'tavily' })
    fireEvent.click(screen.getByTestId('default-radio-tavily'))
    expect(props.onSetDefault).not.toHaveBeenCalled()

    const braveProps = renderRoles({ provider: BRAVE, defaultSearch: 'tavily' })
    fireEvent.click(screen.getByTestId('default-radio-brave'))
    expect(braveProps.onSetDefault).toHaveBeenCalledTimes(1)
    expect(braveProps.onSetDefault).toHaveBeenCalledWith('brave')
  })

  it('clicking an already-selected fallback radio fires nothing — a no-change selection must not open a gated save', () => {
    const props = renderRoles({ provider: DUCKDUCKGO, fallbackSearch: 'duckduckgo' })
    fireEvent.click(screen.getByTestId('fallback-radio-duckduckgo'))
    expect(props.onSetFallback).not.toHaveBeenCalled()
  })

  it('clicking an already-selected default radio fires nothing', () => {
    const props = renderRoles({ provider: TAVILY, defaultSearch: 'tavily' })
    fireEvent.click(screen.getByTestId('default-radio-tavily'))
    expect(props.onSetDefault).not.toHaveBeenCalled()
  })

  it('offers SearXNG no radios at all (D10 — descoped): the row renders no cells', () => {
    const { container } = renderRoles({ provider: row({ id: 'searxng', requires_key: false, configured: false, usable: false }) })
    expect(container.querySelector('[data-testid^="default-radio-"]')).toBeNull()
    expect(container.querySelector('[data-testid^="fallback-radio-"]')).toBeNull()
    expect(screen.queryByTestId('default-radio-searxng')).not.toBeInTheDocument()
    expect(screen.queryByTestId('fallback-radio-searxng')).not.toBeInTheDocument()
  })
})

describe('WebSearchNoFallbackChoice — the visible "No fallback" choice (spec "Fallback")', () => {
  function renderNone(overrides: { fallbackSearch?: string | null; saving?: boolean } = {}) {
    const props = {
      fallbackSearch: 'duckduckgo' as string | null | undefined,
      saving: false,
      onSetNoFallback: vi.fn(),
      ...overrides,
    }
    cleanup()
    const view = render(<WebSearchNoFallbackChoice {...props} />)
    return { ...props, ...view }
  }

  it('renders as a visible, labelled choice — not an unselected blank', () => {
    renderNone()
    expect(screen.getByTestId('no-fallback-choice')).toHaveTextContent('No fallback')
    expect(screen.getByTestId('fallback-radio-none')).toBeInTheDocument()
  })

  it('is checked only when fallback_search is null — an explicit none', () => {
    renderNone({ fallbackSearch: null })
    expect(screen.getByTestId('fallback-radio-none')).toHaveAttribute('data-state', 'checked')
    renderNone({ fallbackSearch: 'duckduckgo' })
    expect(screen.getByTestId('fallback-radio-none')).toHaveAttribute('data-state', 'unchecked')
    renderNone({ fallbackSearch: undefined })
    expect(screen.getByTestId('fallback-radio-none')).toHaveAttribute('data-state', 'unchecked')
  })

  it('clicking it when a provider holds the fallback reports the none choice; an explicit none reports nothing', () => {
    const props = renderNone({ fallbackSearch: 'duckduckgo' })
    fireEvent.click(screen.getByTestId('fallback-radio-none'))
    expect(props.onSetNoFallback).toHaveBeenCalledTimes(1)

    const noneProps = renderNone({ fallbackSearch: null })
    fireEvent.click(screen.getByTestId('fallback-radio-none'))
    expect(noneProps.onSetNoFallback).not.toHaveBeenCalled()
  })
})

describe('WebSearchGroup — the group surface and its notices', () => {
  function renderSurface(overrides: {
    defaultSearch?: string
    fallbackIgnoredReason?: string
    nativeSearchInEffect?: boolean
  } = {}) {
    const props = {
      defaultSearch: 'tavily' as string | undefined,
      fallbackIgnoredReason: undefined as string | undefined,
      nativeSearchInEffect: false,
      ...overrides,
    }
    render(<WebSearchGroup {...props} />)
    return props
  }

  it('states that the list order does not choose who is tried first (FR-012)', () => {
    renderSurface()
    expect(screen.getByText(/The order of this list does not choose who is tried first\./)).toBeInTheDocument()
  })

  it('says roles are undecided when default_search is absent (migration deferred)', () => {
    renderSurface({ defaultSearch: undefined })
    expect(screen.getByTestId('roles-undecided')).toBeInTheDocument()
  })

  it('shows no undecided notice when a default is stored', () => {
    renderSurface({ defaultSearch: 'tavily' })
    expect(screen.queryByTestId('roles-undecided')).not.toBeInTheDocument()
  })

  it('shows the FR-031 native-search notice when native_search_in_effect is true', () => {
    renderSurface({ nativeSearchInEffect: true })
    const notice = screen.getByTestId('native-search-notice')
    expect(notice).toBeInTheDocument()
    expect(notice).toHaveTextContent(/native/i)
    expect(notice).toHaveTextContent(/not currently deciding who answers/i)
  })

  it('does not show the native-search notice when native_search_in_effect is false or absent', () => {
    renderSurface({ nativeSearchInEffect: false })
    expect(screen.queryByTestId('native-search-notice')).not.toBeInTheDocument()
    renderSurface({ nativeSearchInEffect: undefined })
    expect(screen.queryByTestId('native-search-notice')).not.toBeInTheDocument()
  })

  it('the FR-031 notice names no provider as the one that answers', () => {
    renderSurface({ nativeSearchInEffect: true })
    const notice = screen.getByTestId('native-search-notice')
    for (const name of ['Brave', 'Tavily', 'DuckDuckGo', 'SearXNG']) {
      expect(notice.textContent).not.toContain(name)
    }
  })

  it('explains the ignored fallback when fallback_ignored_reason is present (R5)', () => {
    renderSurface({ fallbackIgnoredReason: 'same_as_default' })
    const notice = screen.getByTestId('fallback-ignored-notice')
    expect(notice).toHaveTextContent(/ignored/i)
    expect(notice).toHaveTextContent(/cannot fall back to itself/i)
  })

  it('shows no ignored-fallback explanation when fallback_ignored_reason is absent', () => {
    renderSurface({ fallbackIgnoredReason: undefined })
    expect(screen.queryByTestId('fallback-ignored-notice')).not.toBeInTheDocument()
  })
})
