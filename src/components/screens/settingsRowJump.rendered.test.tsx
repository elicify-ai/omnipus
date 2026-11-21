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
 * stylesheet's effective cascade (src/styles/globals.css, read from disk —
 * vitest's `?raw` returns '' for a .css import): the flash must be a
 * NON-ZERO-duration, ONE-iteration animation whose keyframes change a
 * paintable property between frames (FR-075 "flashes once"), and the LAST
 * equal-specificity rule for the row target in document order — the one the
 * cascade applies to a reduced-motion user — must sit in the
 * `prefers-reduced-motion: reduce` block and set no animation (FR-075
 * "without animation"). What this still cannot see: custom-property values
 * (`--motion-duration-slow: 0s` would hide from a static read),
 * cross-specificity overrides, and rendered pixels — the authorized
 * exact-commit browser lane owns those. The DOM side (the mark on exactly
 * that row) is asserted rendered. The browser-run witness for the same legs
 * is the e2e suite's FR-075 tests (typecheck-only here; the authorized
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
import { SETTINGS_SEARCH_INDEX, sectionInfo, type SettingsSectionId } from '@/components/settings/settingsSections'

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
    // The jumped-to section's own reads resolve so its rows render and the
    // shell's MutationObserver can mark them. Gateway's config shape is the
    // GatewaySection.test.tsx fixture's; the other sections' shapes are the
    // settingsMap harness's (see the reach sweep's mock comment below).
    fetchGatewayStatus: vi.fn(async () => ({ daily_cost: 0, uptime_seconds: 0 })),
    fetchPendingRestart: vi.fn(async () => []),
    fetchGodMode: vi.fn(async () => ({
      enabled: false,
      available: true,
      supported: true,
      persisted: false,
    })),
    // The reach sweep (FR-075: "every setting the search index names must be
    // reachable this way") jumps into EVERY indexed section, so every
    // section's own reads must resolve — the settingsMap harness's set,
    // whose shapes the FR-073 sweep already renders real section content
    // from. No write is made anywhere, so no write seam is needed.
    fetchAboutInfo: vi.fn(async () => ({
      version: '1.2.3',
      go_version: 'go1.24',
      os: 'linux',
      arch: 'amd64',
      uptime_seconds: 42,
      device_pairing_enabled: true,
    })),
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
      data: { session_retention_days: 90 },
    })),
    getContextSettings: vi.fn(async () => ({
      mcp_result_cap: 20,
      builtin_success_cap: 5,
      model_overrides: [],
    })),
    fetchPerformanceSettings: vi.fn(async () => ({
      max_parallel_agents: 4,
      effective_max_parallel_agents: 4,
      max_parallel_agents_configured: true,
      tools_on_demand: false,
      goal_max_rounds: 20,
      goal_reminder_quiet_seconds: 60,
      goal_stalled_checkin_enabled: true,
      goal_stalled_checkin_minutes: 10,
      max_tool_iterations: 50,
    })),
    fetchMemorySettings: vi.fn(async () => ({})), // formFromSettings applies defaults
    fetchProviders: vi.fn(async () => [
      {
        status: 'connected',
        auth_method: 'api_key',
        dependents: [],
        backs_default: false,
        id: 'anthropic',
        name: 'anthropic',
        display_name: 'Anthropic',
        has_models_endpoint: true,
        models: ['claude-sonnet-4-5'],
      },
    ]),
    fetchProvidersCatalog: vi.fn(async () => ({
      schema_version: '2.0.0',
      version: 'v2026.1.1',
      updated_at: '2026-01-01T00:00:00Z',
      providers: [
        {
          id: 'anthropic',
          name: 'anthropic',
          company: 'Anthropic',
          api: 'https://api.anthropic.com',
          region: 'us-east-1',
          auth_methods: ['api_key'],
          models: [
            {
              id: 'claude-sonnet-4-5',
              name: 'claude-sonnet-4-5',
              context_window: 200000,
              max_output_tokens: 64000,
              input_modalities: ['text'],
              tool_call: true,
              status: 'active',
            },
          ],
        },
      ],
    })),
    getDefaultModel: vi.fn(async () => ({
      provider: 'openrouter',
      model: 'z-ai/glm-5.2',
      context_window: 1048576,
      window_source: 'catalog',
    })),
    fetchIntegrationProviders: vi.fn(async () => ({
      search: [
        { id: 'brave', kind: 'search', display_name: 'Brave', configured: true, requires_key: true, usable: true, active: true },
      ],
      voice: [
        { id: 'elevenlabs', kind: 'voice', display_name: 'ElevenLabs', configured: true, requires_key: true, usable: true, active: true },
      ],
      active_search: 'brave',
      active_voice: 'elevenlabs',
      default_search: 'brave',
      fallback_search: null,
    })),
    getThirdPartyAgentSettings: vi.fn(async () => ({
      clis: {
        codex: { path: '', preset_args: '', detected: { installed: false } },
        'claude-code': { path: '', preset_args: '', detected: { installed: false } },
        opencode: { path: '', preset_args: '', detected: { installed: false } },
      },
    })),
    fetchSandboxStatus: vi.fn(async () => ({
      backend: 'landlock',
      available: true,
      kernel_level: true,
      policy_applied: true,
      seccomp_enabled: true,
      bind_ports_count: 0,
    })),
    fetchSandboxConfig: vi.fn(async () => ({
      mode: 'permissive',
      allowed_paths: [],
      ssrf: { allow_internal: [] },
      requires_restart: false,
    })),
    fetchCredentials: vi.fn(async () => []),
    fetchRetentionSettings: vi.fn(async () => ({
      session_days: 90,
      memory_retros_days: 180,
      memory_retros_forever: false,
      keep_everything_forever: false,
    })),
    fetchStorageStats: vi.fn(async () => ({
      workspace_size_bytes: 10,
      session_count: 3,
      memory_entry_count: 5,
      warnings: [],
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
 * The row element the spec names, resolved by its row identity. Waited for,
 * because the section's reads resolve after the shell paints — the same wait
 * the shell's own MutationObserver performs before it marks the row
 * (FR-075: the mark lands when the row exists).
 */
async function rowFor(rowId: string, premise: string): Promise<HTMLElement> {
  let found: HTMLElement | null = null
  await waitFor(() => {
    found = document.querySelector<HTMLElement>(`[data-settings-row="${rowId}"]`)
    expect(found, premise).not.toBeNull()
  })
  return found as unknown as HTMLElement
}

async function portRow(): Promise<HTMLElement> {
  return rowFor('port', 'FR-075 premise: the Port setting row must render in the opened section')
}

/**
 * Regex-escape a label for an accessible-name match. The result link's
 * accessible name is its label and section label read together ("Model
 * overridesLimits" — jsdom's accname concatenates without separators), so
 * the match stays an unescaped-label substring, exactly the shape the
 * working Port case uses ({ name: /Port/ }); within the results panel the
 * typed label names one entry.
 */
function labelPattern(label: string): RegExp {
  return new RegExp(label.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'))
}

/**
 * The search jump of FR-075's first arm for any indexed entry: type the
 * entry's name, click THAT entry's result link (the panel is the scope —
 * the sidebar always lists the same section names).
 */
async function jumpViaEntry(label: string): Promise<void> {
  const box = await screen.findByRole('searchbox', { name: /search/i })
  await userEvent.type(box, label)
  const panel = await screen.findByTestId('settings-search-results')
  await userEvent.click(within(panel).getByRole('link', { name: labelPattern(label) }))
}

/**
 * FR-075: "a row inside a group that is closed opens that group first" —
 * the named group's own toggle must be expanded by the jump. The toggle is
 * located by its accessible NAME — the group header's label, the same name
 * the search index entry carries — never "the group's one button": once the
 * jump has opened the group, its body legitimately carries more buttons
 * (the overrides group's Add-override action), and an unnamed locator
 * cannot say which button it means (the part-22 reconciliation at
 * c12cbeffe0: round 16's opening made that locator ambiguous).
 */
async function expectGroupOpen(testId: string, headerName: string): Promise<void> {
  const toggle = within(screen.getByTestId(testId)).getByRole('button', { name: headerName })
  await waitFor(
    () =>
      expect(
        toggle,
        'RED (FR-075): a row inside a group that is closed — the jump must open that group first',
      ).toHaveAttribute('aria-expanded', 'true'),
    { timeout: 5_000 },
  )
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
//
// Effective-cascade oracle (CHECK R5-02): the required behaviour is visual —
// a short visible flash once, and NO animation under reduced motion — so the
// oracle reads the cascade the browser would apply, not a source declaration.
// jsdom computes no animation, so "effective" here means: the rules for the
// row target, in document order, each tagged with whether it sits inside the
// reduce media block. At equal specificity the LAST matching rule decides,
// which is exactly the cascade outcome for both arms.

interface RowTargetRule {
  inReduceMedia: boolean
  body: string
}

/** Index of the brace that closes the block opened at `open` (-1 if unbalanced). */
function balancedClose(css: string, open: number): number {
  let depth = 0
  for (let i = open; i < css.length; i++) {
    if (css[i] === '{') depth++
    else if (css[i] === '}') {
      depth--
      if (depth === 0) return i
    }
  }
  return -1
}

/**
 * Every rule for `selector` anywhere in the sheet, in document order, each
 * tagged with whether the rule sits inside a `prefers-reduced-motion: reduce`
 * block. Strings and comments carrying braces would confuse the scanner;
 * the shipped globals.css carries none (the same simplification the
 * source-presence helpers this replaces already made).
 */
function rowTargetRules(css: string, selector: string): RowTargetRule[] {
  const hits: RowTargetRule[] = []
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const re = new RegExp(`${escaped}\\s*\\{`, 'g')
  let m: RegExpExecArray | null
  while ((m = re.exec(css))) {
    const open = m.index + m[0].length - 1
    const close = balancedClose(css, open)
    if (close === -1) break
    hits.push({ inReduceMedia: insideReduceMedia(css, m.index), body: css.slice(open + 1, close) })
    re.lastIndex = close + 1
  }
  return hits
}

/** Whether position `at` sits inside a prefers-reduced-motion: reduce block. */
function insideReduceMedia(css: string, at: number): boolean {
  const headers: string[] = []
  let header = ''
  for (let i = 0; i < at; i++) {
    const c = css[i]
    if (c === '{') {
      headers.push(header)
      header = ''
    } else if (c === '}') {
      headers.pop()
      header = ''
    } else {
      header += c
    }
  }
  return headers.some((h) => h.includes('prefers-reduced-motion') && h.includes('reduce'))
}

/** Split a value into its top-level tokens (whitespace outside parentheses). */
function shorthandTokens(value: string): string[] {
  const tokens: string[] = []
  let current = ''
  let depth = 0
  for (const ch of value) {
    if (ch === '(') depth++
    if (ch === ')') depth--
    if (ch === ' ' && depth === 0) {
      if (current) tokens.push(current)
      current = ''
    } else {
      current += ch
    }
  }
  if (current) tokens.push(current)
  return tokens
}

/**
 * Whether a duration token means zero time. A literal `0s`/`0ms`, a calc
 * whose numeric factors are all zero, or NO duration token at all (the CSS
 * default is 0s) all animate nothing. What this cannot see: a custom
 * property that itself resolves to zero (`--motion-duration-slow: 0s`) —
 * jsdom resolves no variables; the browser lane owns computed values.
 */
function isZeroDuration(token: string): boolean {
  if (/^(?:0+(?:\.0+)?|\.0+)(?:s|ms)$/.test(token)) return true
  if (token.startsWith('calc(')) {
    const numbers = token.match(/\d+(?:\.\d+)?/g) ?? []
    return numbers.length === 0 || numbers.every((n) => Number(n) === 0)
  }
  return !/(?:s|ms)$/.test(token) || /^(?:0+(?:\.0+)?)$/.test(token)
}

/** The `@keyframes <name>` block's body (balanced), or null. */
function keyframesBody(css: string, name: string): string | null {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const m = new RegExp(`@keyframes\\s+${escaped}\\s*\\{`).exec(css)
  if (!m) return null
  const open = css.indexOf('{', m.index)
  const close = balancedClose(css, open)
  return close === -1 ? null : css.slice(open + 1, close)
}

/** Properties whose change between frames a person can see. */
const PAINTABLE_PROPERTIES = new Set([
  'background-color',
  'background',
  'color',
  'opacity',
  'box-shadow',
  'border-color',
  'outline-color',
  'outline',
  'fill',
])
const INVISIBLE_VALUES = new Set(['transparent', 'none', '0', '0px', 'rgba(0,0,0,0)'])

/**
 * Whether the keyframes' frames actually change a paintable property to a
 * different value (at least one end visible). Identical frames — the
 * transparent-to-transparent flash — animate nothing.
 */
function framesChangeVisibleProperty(body: string): boolean {
  const frames = [...body.matchAll(/(?:from|to|\d+(?:\.\d+)?%)\s*\{([^}]*)\}/g)].map((m) => m[1])
  if (frames.length < 2) return false
  const decls = frames.map((frame) => {
    const map = new Map<string, string>()
    for (const decl of frame.split(';')) {
      const at = decl.indexOf(':')
      if (at > 0) map.set(decl.slice(0, at).trim().toLowerCase(), decl.slice(at + 1).trim())
    }
    return map
  })
  for (let a = 0; a < decls.length; a++) {
    for (let b = a + 1; b < decls.length; b++) {
      for (const [prop, valueA] of decls[a]) {
        const valueB = decls[b].get(prop)
        if (valueB === undefined || valueA === valueB) continue
        if (!PAINTABLE_PROPERTIES.has(prop)) continue
        if (!INVISIBLE_VALUES.has(valueA) || !INVISIBLE_VALUES.has(valueB)) return true
      }
    }
  }
  return false
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
    // "flashes once": the cascade's rule for the marked row must name an
    // animation with a NON-ZERO duration and exactly one iteration, and the
    // named keyframes must change a paintable property between frames —
    // a zero-duration animation (N65) or identical transparent frames (N66)
    // satisfy a declaration but flash nothing. Replaces the old oracle that
    // accepted any body merely ENDING in the count 1 (FR-075 "flashes
    // once" — CHECK R5-02); jsdom computes no animation, so this reads the
    // effective top-level rule and its keyframes (see the section comment).
    const normalRules = rowTargetRules(globalsCss, '[data-settings-row-target]').filter((r) => !r.inReduceMedia)
    const animationValue = [...normalRules].reverse().map((r) => r.body.match(/animation:\s*([^;]+);/)?.[1]?.trim() ?? '').find(Boolean)
    expect(
      animationValue,
      'RED (FR-075): the mark must be mapped to an animation — the flash has no referent',
    ).toBeTruthy()
    const tokens = shorthandTokens(animationValue as string)
    const keyframesName = tokens[0] ?? ''
    expect(
      /^[a-z][a-z0-9-]*$/i.test(keyframesName),
      `FR-075: the animation shorthand must name its keyframes first, got: ${animationValue}`,
    ).toBe(true)
    // The duration is the first time/calc token; no token means the CSS
    // default 0s — no flash.
    const duration = tokens.find((t) => /^(?:\d+(?:\.\d+)?(?:s|ms)|calc\()/.test(t)) ?? '0s'
    expect(
      isZeroDuration(duration),
      `FR-075 (CHECK R5-02): the flash must have a non-zero duration — got duration token: ${duration}`,
    ).toBe(false)
    const iteration = [...tokens].reverse().find((t) => /^\d+(?:\.\d+)?$/.test(t))
    expect(
      iteration,
      `FR-075: the flash must run exactly once (animation iteration count 1), got: ${animationValue}`,
    ).toBe('1')
    const keyframes = keyframesBody(globalsCss, keyframesName)
    expect(keyframes, `FR-075: the named keyframes (${keyframesName}) must exist in the stylesheet`).toBeTruthy()
    expect(
      framesChangeVisibleProperty(keyframes as string),
      `FR-075 (CHECK R5-02): the flash keyframes must change a visible property between frames — identical frames flash nothing`,
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
    // "without animation": the cascade's LAST word for the row target must
    // be the reduce rule setting no animation. At equal specificity the
    // last matching rule decides — a later normal rule that re-declares the
    // flash (N74) would undo a reduce rule the sheet merely CONTAINS, which
    // is what the replaced oracle accepted (any reduce-block rule saying
    // `none`, a source declaration). FR-075's "without animation" is the
    // effective outcome for a reduced-motion user (CHECK R5-02).
    const allRules = rowTargetRules(globalsCss, '[data-settings-row-target]')
    expect(allRules.length, 'premise: the stylesheet styles the marked row').toBeGreaterThan(0)
    expect(
      allRules.some((r) => r.inReduceMedia),
      'the stylesheet must carry a prefers-reduced-motion: reduce rule for the marked row',
    ).toBe(true)
    const effective = allRules[allRules.length - 1]
    expect(
      effective.inReduceMedia,
      'RED (FR-075, CHECK R5-02): under prefers-reduced-motion: reduce the LAST cascade rule for the row must be the reduce rule — a later equal-specificity rule would undo it',
    ).toBe(true)
    expect(
      /animation:\s*none/.test(effective.body),
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

  // R5-01 (CHECK round 5): the indexed Model-overrides row lives in a group
  // that starts CLOSED (FR-072), and the search index names it — FR-075:
  // "a row inside a group that is closed opens that group first; every
  // setting the search index names must be reachable this way." RED on
  // landing until the frontend wires the row target through the group
  // (round 16); both legs below name their FR when they fail.
  it('a search result for a row inside a closed group opens that group and marks exactly that row', async () => {
    await openSettings()
    await jumpViaEntry('Model overrides')
    // "the section opens": the row's own section.
    expect(await screen.findByRole('heading', { name: 'Limits', level: 2 })).toBeInTheDocument()
    // "a row inside a group that is closed opens that group first."
    await expectGroupOpen('limits-group-overrides', 'Model overrides')
    // And the marked row is that one row, with its aria-current identity.
    await expectExactlyThisRowMarked(
      await rowFor('model-overrides', 'RED (FR-075): the indexed Model-overrides row must render — its group opened and its identity reached the DOM'),
    )
  })

  it('a deep link to a row inside a closed group opens that group and marks exactly that row', async () => {
    await openSettings('?tab=limits&row=model-overrides')
    expect(await screen.findByRole('heading', { name: 'Limits', level: 2 })).toBeInTheDocument()
    await expectGroupOpen('limits-group-overrides', 'Model overrides')
    await expectExactlyThisRowMarked(
      await rowFor('model-overrides', 'RED (FR-075): the indexed Model-overrides row must render — its group opened and its identity reached the DOM'),
    )
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

// ── FR-075 reach sweep (CHECK R5-01) ─────────────────────────────────────────

/**
 * "Every setting the search index names must be reachable this way"
 * (FR-075, team-lead clarification 2026-11-21, CHECK R5-01). One data-driven
 * case per SETTING-ROW entry of the index — the entries that carry a `row`.
 * The index's section-level entries name a section, and its moved/removed
 * entries (a note or an outward href) answer where a setting went or why it
 * is gone; neither carries a row, so neither is a setting row FR-075 can
 * reach — they are FR-006/FR-007's answers, already watched by
 * settingsSearch.test.tsx. Expected RED on landing for every entry whose
 * row wiring is missing (round 16); each failing case names its entry.
 */
const ROW_ENTRIES: Array<{ label: string; row: string; section: SettingsSectionId }> =
  SETTINGS_SEARCH_INDEX.flatMap((e) => (e.section && e.row ? [{ label: e.label, row: e.row, section: e.section }] : []))

describe('FR-075 reach — every setting the search index names is reachable and marks exactly its row', () => {
  it.each(ROW_ENTRIES)('search reaches "$label" and marks exactly that row', async (entry) => {
    await openSettings()
    await jumpViaEntry(entry.label)
    // "the section opens": the entry's own section is the one shown.
    expect(
      await screen.findByRole('heading', { name: sectionInfo(entry.section).label, level: 2 }),
      `FR-075: the search result for "${entry.label}" must open its own section (${sectionInfo(entry.section).label})`,
    ).toBeInTheDocument()
    // "marks exactly that row" — the whole mark shape, scoped to this entry.
    await expectExactlyThisRowMarked(
      await rowFor(
        entry.row,
        `RED (FR-075): the setting row the index names for "${entry.label}" (${entry.row}) must be reachable — the row must render in its opened section`,
      ),
    )
  }, 20_000)
})
