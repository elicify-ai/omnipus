/**
 * WebSearchResult.test.tsx — the provider/role/hop lines of a search result
 * (verify item: "does the parser bury 'Search provider: <id> (<role>)' and
 * hop notes into the last result's snippet, hiding who answered?").
 *
 * Wire format (pkg/tools/web_search.go::successText): the provider's result
 * text first, then "\n\nSearch provider: <id> (<role>)" — roles "default",
 * "fallback", "chosen" — then note lines ("- <id> (<role>): not called:
 * <reason>", "Note: <id> (<role>) failed and was not used for these
 * results. ..."). The old parser split only on numbered blocks, so every
 * line from the provider header onward was glued into the LAST result's
 * snippet and hidden behind line-clamp-2.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

type RenderFn = (props: {
  args: unknown
  result: unknown
  status: { type: string }
}) => React.ReactNode

// vi.hoisted runs before vi.mock factory and before all imports.
const captured = vi.hoisted((): Record<string, RenderFn> => ({}))

vi.mock('@assistant-ui/react', async (importOriginal) => {
  const original = await importOriginal<typeof import('@assistant-ui/react')>()
  return {
    ...original,
    makeAssistantToolUI: (config: Record<string, unknown>) => {
      if (typeof config.toolName === 'string') {
        captured[config.toolName] = config.render as RenderFn
      }
      return config
    },
  }
})

// Static import: vi.mock intercepts makeAssistantToolUI before these run.
import { WebSearchCanonicalUI } from './WebSearchResult'

// Byte-shaped like the real wire text: two numbered results, the blank
// separator, the provider header, then one note of each format.
const RESULTS_WITH_PROVIDER = [
  '1. Example Site',
  '    https://example.com',
  '   Example snippet text.',
  '',
  '2. Another Site',
  '    https://another.com',
  '   Another snippet.',
  '',
  'Search provider: exa (default)',
  '- tavily (fallback): not called: provider is not usable',
  'Note: tavily (fallback) failed and was not used for these results. network: 500: boom',
].join('\n')

// Expand the collapsed row, then hand back the expanded panel (root
// children[1] — DisclosureRow renders a single <button> header, see
// WebFetchAndSearch.edge.test.tsx's equivalent helper).
function renderExpanded(result: unknown) {
  const renderFn = captured['search_web']
  if (!renderFn) {
    // Reference the import binding before failing: an unreferenced import
    // can be treeshaken out of the SSR transform, in which case the module
    // never evaluates and nothing is captured — the edge test's guards do
    // the same, which is why its captures always work.
    expect(WebSearchCanonicalUI).toBeDefined()
    throw new Error('search_web render fn not captured')
  }
  const utils = render(
    renderFn({ args: { query: 'who answered' }, result, status: { type: 'complete' } }) as React.ReactElement,
  )
  fireEvent.click(utils.container.querySelector('button')!)
  return utils
}

describe('WebSearchBlock — provider/role/hop lines visible, not buried in a snippet', () => {
  it('renders the provider/role header as its own visible element when expanded', () => {
    renderExpanded(RESULTS_WITH_PROVIDER)
    expect(screen.getByTestId('web-search-provider')).toHaveTextContent('Search provider: exa (default)')
  })

  it('keeps the provider header and every hop note out of the result snippets', () => {
    renderExpanded(RESULTS_WITH_PROVIDER)
    const snippets = Array.from(document.querySelectorAll('p.line-clamp-2'))
    expect(snippets.length).toBeGreaterThan(0)
    for (const snippet of snippets) {
      expect(snippet.textContent).not.toContain('Search provider:')
      expect(snippet.textContent).not.toContain('not called:')
    }
  })

  it('renders the last result snippet as exactly its own text — nothing glued after it', () => {
    renderExpanded(RESULTS_WITH_PROVIDER)
    const snippets = Array.from(document.querySelectorAll('p.line-clamp-2'))
    const last = snippets[snippets.length - 1]
    expect(last.textContent).toBe('Another snippet.')
  })

  it('renders hop notes as their own lines below the results', () => {
    renderExpanded(RESULTS_WITH_PROVIDER)
    expect(screen.getByTestId('web-search-notes')).toHaveTextContent(
      '- tavily (fallback): not called: provider is not usable',
    )
    expect(screen.getByTestId('web-search-notes')).toHaveTextContent(
      'Note: tavily (fallback) failed and was not used for these results. network: 500: boom',
    )
  })

  it('shows the true result count (2) even with the trailer present', () => collapsedShowsCount())

  it('a result text without the provider header keeps the structured list and renders no provider line', () => {
    renderExpanded('1. Example Site\n    https://example.com\n   Site description here.')
    expect(screen.queryByTestId('web-search-provider')).not.toBeInTheDocument()
    expect(screen.queryByTestId('web-search-notes')).not.toBeInTheDocument()
    expect(document.querySelectorAll('p.line-clamp-2').length).toBe(1)
  })
})

// The collapsed header count must reflect only parsed results — the trailer
// must not inflate it. (Separated helper: assertion runs before expansion.)
function collapsedShowsCount() {
  const renderFn = captured['search_web']
  if (!renderFn) {
    expect(WebSearchCanonicalUI).toBeDefined()
    throw new Error('search_web render fn not captured')
  }
  render(
    renderFn({ args: { query: 'who answered' }, result: RESULTS_WITH_PROVIDER, status: { type: 'complete' } }) as React.ReactElement,
  )
  expect(screen.getByText('2 results')).toBeInTheDocument()
  // And the header row itself must not leak the trailer: the collapsed row
  // shows the query, not the provider line.
  expect(screen.queryByTestId('web-search-provider')).not.toBeInTheDocument()
}
