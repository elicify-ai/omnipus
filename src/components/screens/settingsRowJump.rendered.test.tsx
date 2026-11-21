/**
 * settingsRowJump.rendered.test.tsx — Settings redesign #1519 (CHECK R4-05).
 *
 * FR-075 rendered (spec docs/internal/specs/settings-redesign-spec.md :2001,
 * team-lead clarification 2026-11-21): the row highlight is the short flash
 * on the setting row a person reaches from a Settings search result or a
 * deep link — the section opens, the row scrolls into view and flashes once
 * (normal motion); under reduced motion the row is still scrolled into view
 * and marked, without animation. The frontend round 15 build (e717a2e3e7)
 * supplies the names used here ONLY as selectors — `data-settings-row`,
 * `data-settings-row-target`, `aria-current` and the `?row=` parameter —
 * every expected behaviour is FR-075's, never the code's.
 *
 * Harness: the REAL routeTree in a REAL router (the settingsSearch.test.tsx
 * pattern), so the jump a person makes — type in the search box, click the
 * result link, or open a `?row=` deep link — is the screen's own navigation,
 * and the section that opens is the real GatewaySection with its real reads.
 * The unit under test is the real shell + section; ONLY process edges are
 * test doubles: the `@/lib/api` reads are mocked (the search-jump case's
 * GatewaySection reads resolve immediately, so the row exists for the
 * MutationObserver to mark), `scrollIntoView` is stubbed on the Element
 * prototype (jsdom does not implement it) to witness "the row scrolls into
 * view", and `matchMedia` is stubbed per test (jsdom evaluates no media
 * queries) for the reduced-motion arm.
 *
 * What jsdom cannot do, stated plainly: jsdom loads no stylesheet, so the
 * flash itself (a CSS animation on `[data-settings-row-target]`) cannot be
 * computed here. The stylesheet-side oracle is asserted against the REAL
 * stylesheet source (src/styles/globals.css, read from disk — vitest's
 * `?raw` returns '' for a .css import): the mark must be mapped to an
 * animation that runs once (FR-075 "flashes once"), and the
 * `prefers-reduced-motion: reduce` block must disable that animation
 * (FR-075 "without animation"). The DOM side (the mark on exactly that row)
 * is asserted rendered. The browser-run witness for the same legs is the
 * e2e suite's FR-075 tests (typecheck-only here; the authorized
 * exact-commit lane executes them).
 *
 * RED note: the production build landed (a94f859e3a merged e717a2e3e7)
 * before this part, so these cases have no pre-change red to show — the
 * commissioned failability proof is the mutation set in the part-20 report
 * (row not marked; flash runs under reduced motion; wrong row marked; each
 * killed in a scratch clone).
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createRouter, createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import userEvent from '@testing-library/user-event'
import { routeTree } from '@/routeTree.gen'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// The REAL stylesheet the SPA ships, read from disk: vitest's `?raw` returns
// '' for a .css import, and jsdom computes no CSS — the stylesheet-side
// oracles below need the source text of the mapping. (Path resolution via
// import.meta.dirname, the pattern the WU-11 guard tests use; import.meta.url
// is an http: URL under the jsdom environment, not a file: URL.)
const globalsCss = readFileSync(join(import.meta.dirname, '..', '..', 'styles', 'globals.css'), 'utf8')

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    // App shell reads (the settingsSearch.test.tsx set).
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    validateToken: vi.fn(async () => ({})),
    fetchNotifications: vi.fn(async () => ({ notifications: [], unread_count: 0 })),
    fetchTasks: vi.fn(async () => []),
    fetchAgents: vi.fn(async () => []),
    fetchVersion: vi.fn(async () => ({ version: 'test', build_sha: 'test' })),
    fetchWorkspaces: vi.fn(async () => [
      { id: 'ws-1', name: 'UAT Build', status: 'active', is_default: true },
    ]),
    fetchSessions: vi.fn(async () => []),
    fetchKnowledgeBaseInfo: vi.fn(async () => ({
      workspace_id: 'ws-1',
      root_path: '',
      is_knowledge_base: false,
      marker: 'none',
    })),
    // The jumped-to section's own reads (Gateway), resolved so the Port row
    // renders and the shell's MutationObserver can mark it. Config shape is
    // the GatewaySection.test.tsx fixture's (the section renders its rows
    // from it); no write is made, so no write seam is needed.
    fetchConfig: vi.fn(async () => ({
      gateway: {
        bind_address: '127.0.0.1',
        port: 5000,
        token: 'test-token',
        hot_reload: true,
        log_level: 'info',
        dev_mode_bypass: false,
        preview_enabled: undefined,
      },
      agents: { defaults: { default_agent_id: '' } },
      security: {
        daily_cost_cap: 10,
        exec_timeout_seconds: 0,
        max_background_seconds: 0,
        rate_limits: {
          max_agent_llm_calls_per_hour: null,
          max_agent_tool_calls_per_minute: null,
        },
      },
    })),
    fetchGatewayStatus: vi.fn(async () => ({ daily_cost: 0, uptime_seconds: 0 })),
    fetchPendingRestart: vi.fn(async () => []),
    fetchGodMode: vi.fn(async () => ({
      enabled: false,
      available: true,
      supported: true,
      persisted: false,
    })),
  }
})

/** Stubbed into `window.matchMedia` for the test at hand. */
function stubMatchMedia(reduce: boolean): void {
  Object.defineProperty(window, 'matchMedia', {
    configurable: true,
    writable: true,
    value: (query: string) => ({
      matches: reduce && query.includes('prefers-reduced-motion') && query.includes('reduce'),
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => false,
    }),
  })
}

async function openSettings(search = ''): Promise<void> {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [`/settings${search}`] }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}

/** The search jump of FR-075's first arm: type "port", click the result link. */
async function jumpViaSearchResult(): Promise<void> {
  // The router renders the shell asynchronously; wait for the search box
  // (the settingsSearch.test.tsx helper's reason, unchanged).
  const box = await screen.findByRole('searchbox', { name: /search/i })
  await userEvent.type(box, 'port')
  const panel = await screen.findByTestId('settings-search-results')
  // D-13 row 1: "port" finds exactly Gateway → Port.
  await userEvent.click(within(panel).getByRole('link', { name: /Port/ }))
}

/**
 * The Port row element, resolved the way the spec names it: that one setting
 * row. Waited for, because the section's reads resolve after the shell
 * paints — the same wait the shell's own MutationObserver performs before it
 * marks the row (FR-075: the mark lands when the row exists).
 */
async function portRow(): Promise<HTMLElement> {
  let found: HTMLElement | null = null
  await waitFor(() => {
    found = document.querySelector<HTMLElement>('[data-settings-row="port"]')
    expect(found, 'FR-075 premise: the Port setting row must render in the opened section').not.toBeNull()
  })
  return found as unknown as HTMLElement
}

/**
 * Wait for the mark, then pin its whole shape: the row carries the target
 * mark and aria-current, and it is the ONLY marked row on the page —
 * FR-075 "marks" names that one row, not a class of them.
 */
async function expectExactlyThisRowMarked(row: HTMLElement): Promise<void> {
  await waitFor(() => {
    expect(row, 'RED (FR-075): the reached row must be marked').toHaveAttribute('data-settings-row-target')
    expect(row, 'RED (FR-075): the reached row must be marked to assistive tech').toHaveAttribute('aria-current', 'true')
  })
  const marked = document.querySelectorAll('[data-settings-row-target]')
  expect(marked.length, 'FR-075: exactly that row — one marked row on the page').toBe(1)
  expect(marked[0], 'FR-075: the one marked row is the reached row itself').toBe(row)
}

// ── Stylesheet-side oracle (jsdom loads no stylesheet; see the header) ────────

/** Rule bodies of `selector` at the stylesheet's top level (media blocks stripped). */
function topLevelRuleBodies(css: string, selector: string): string[] {
  const stripped = css.replace(/@media[^{]+\{(?:[^{}]|\{[^{}]*\})*\}/g, ' ')
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return [...stripped.matchAll(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`, 'g'))].map((m) => m[1])
}

/** The balanced bodies of every `@media` block whose condition names `query`. */
function mediaBlockBodies(css: string, query: string): string[] {
  const bodies: string[] = []
  let at = css.indexOf('@media')
  while (at !== -1) {
    const open = css.indexOf('{', at)
    const header = css.slice(at, open)
    let depth = 0
    let close = -1
    for (let i = open; i < css.length; i++) {
      if (css[i] === '{') depth++
      else if (css[i] === '}') {
        depth--
        if (depth === 0) {
          close = i
          break
        }
      }
    }
    if (header.includes(query) && close !== -1) bodies.push(css.slice(open + 1, close))
    at = css.indexOf('@media', at + 1)
  }
  return bodies
}

beforeEach(() => {
  window.localStorage.setItem('omnipus_auth_username', 'row-jump-tester')
  stubMatchMedia(false)
  // jsdom does not implement scrollIntoView; the stub witnesses FR-075's
  // "the row scrolls into view" (which row it ran on is asserted per test).
  Element.prototype.scrollIntoView = vi.fn() as unknown as typeof Element.prototype.scrollIntoView
})

describe('FR-075 — reaching one setting row from a search result or a deep link (CHECK R4-05)', () => {
  it('a search result opens the row\'s section, marks exactly that row, and the flash runs once (normal motion)', async () => {
    await openSettings()
    await jumpViaSearchResult()
    // "the section opens": the result's own section is the one shown.
    expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeInTheDocument()
    const row = await portRow()
    await expectExactlyThisRowMarked(row)
    // "the row scrolls into view": the scroll ran on THIS row.
    const scrolled = (Element.prototype.scrollIntoView as ReturnType<typeof vi.fn>).mock
      .contexts as HTMLElement[]
    expect(
      scrolled.some((el) => el === row),
      'RED (FR-075): the jump must scroll the reached row into view — the scroll ran on another element',
    ).toBe(true)
    // "flashes once": the stylesheet maps the mark to an animation whose
    // iteration count is 1. (jsdom computes no animation; the mapping is
    // asserted on the real stylesheet source — see the header comment.)
    const flashBodies = topLevelRuleBodies(globalsCss, '[data-settings-row-target]').filter((b) => b.includes('animation:'))
    expect(flashBodies.length, 'RED (FR-075): the mark must be mapped to an animation — the flash has no referent').toBeGreaterThan(0)
    const once = flashBodies.map((b) => b.match(/animation:\s*([^;]+);/)?.[1]?.trim() ?? '')
    expect(
      once.some((v) => /(?:^|\s)1$/.test(v)),
      `FR-075: the flash must run exactly once (animation iteration count 1), got: ${once.join(' | ')}`,
    ).toBe(true)
  })

  it('under prefers-reduced-motion: reduce the row is still scrolled into view and marked, without animation', async () => {
    stubMatchMedia(true)
    await openSettings()
    await jumpViaSearchResult()
    expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeInTheDocument()
    const row = await portRow()
    await expectExactlyThisRowMarked(row)
    // "still scrolled into view" under reduced motion too.
    const scrolled = (Element.prototype.scrollIntoView as ReturnType<typeof vi.fn>).mock
      .contexts as HTMLElement[]
    expect(
      scrolled.some((el) => el === row),
      'RED (FR-075): under reduced motion the row must still be scrolled into view',
    ).toBe(true)
    // "without animation": the reduce block disables the animation on the
    // marked row. If the override is missing, the flash runs for reduced-
    // motion users — the exact defect FR-075's reduced-motion arm exists for.
    const reduceBodies = mediaBlockBodies(globalsCss, 'prefers-reduced-motion: reduce')
    expect(reduceBodies.length, 'the stylesheet must carry a prefers-reduced-motion: reduce block').toBeGreaterThan(0)
    const disables = reduceBodies.some((body) =>
      topLevelRuleBodies(body, '[data-settings-row-target]').some((rule) => /animation:\s*none/.test(rule)),
    )
    expect(
      disables,
      'RED (FR-075): under prefers-reduced-motion: reduce the row animation must be disabled — the flash would run for reduced-motion users',
    ).toBe(true)
  })

  it('a deep link with row marks the row in its own section', async () => {
    await openSettings('?tab=gateway&row=port')
    expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeInTheDocument()
    await expectExactlyThisRowMarked(await portRow())
  })

  it('a deep link whose tab disagrees opens the row\'s own section, not the tab\'s', async () => {
    // `tab=data` names a different section; FR-075's deep link carries the
    // ROW, and the row names its own section — Data & retention must not open.
    await openSettings('?tab=data&row=port')
    expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeInTheDocument()
    expect(
      screen.queryByRole('heading', { name: 'Data & retention', level: 2 }),
      'FR-075: the row\'s own section opens — the disagreed tab must not win',
    ).not.toBeInTheDocument()
    await expectExactlyThisRowMarked(await portRow())
  })

  it('the jump does not move focus to the row — focus stays on the section', async () => {
    await openSettings()
    await jumpViaSearchResult()
    expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeInTheDocument()
    const row = await portRow()
    await expectExactlyThisRowMarked(row)
    // FR-075: the row is marked, not focused — marking must never steal
    // focus into the row (focus stays with the section).
    const focused = document.activeElement
    expect(
      focused !== row && !(row as Element).contains(focused),
      'RED (FR-075): the jump must not move focus into the marked row',
    ).toBe(true)
  })
})
