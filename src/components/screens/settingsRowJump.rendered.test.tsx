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
 * paintable property between frames (FR-075 "flashes once"), resolved by
 * folding every declaration for the row target in cascade order — a `calc()`
 * duration is EVALUATED, so an expression that resolves to zero counts as
 * zero (CHECK R6-01/N90), and a later `animation` shorthand or
 * `animation-duration` longhand overrides an earlier duration within a rule
 * or across rules (CHECK R6-01/N91); under reduced motion the fold runs over
 * ALL matching rules, so the animation the cascade would apply must resolve
 * to none — a later declaration that re-enables it fails (CHECK R6-01/N92)
 * (FR-075 "without animation"). The model's own limits are asserted against
 * the shipped sheet, not assumed (expectCascadeModelApplies): no selector
 * variant that would out-rank the fold, no `!important`, and no `@layer` or
 * other conditional block around a row-target rule. What this still cannot
 * see: custom-property VALUES (`--motion-duration-slow` is defined in the
 * imported tokens file, so the shipped calc() duration reads as 'unknown' —
 * accepted as not provably zero, never silently zero) and rendered pixels —
 * the authorized exact-commit browser lane owns those. The DOM side (the
 * mark on exactly that row) is asserted rendered. The browser-run witness
 * for the same legs is the e2e suite's FR-075 tests (typecheck-only here;
 * the authorized exact-commit lane executes them).
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
// Effective-cascade oracle (CHECK R5-02; strengthened CHECK R6-01): the
// required behaviour is visual — a short visible flash once, and NO animation
// under reduced motion — so the oracle reads the cascade the browser would
// apply, not a source declaration. jsdom computes no animation, so
// "effective" here means: every rule for the row target in document order,
// each tagged with its enclosing block headers, folded DECLARATION BY
// DECLARATION — a later `animation` shorthand or `animation-duration`
// longhand overrides an earlier duration (N91), a `calc()` duration is
// evaluated so any expression that resolves to zero counts as zero (N90),
// and the reduced-motion fold runs over ALL rules, so a later declaration
// that re-enables the animation resolves the name away from none and fails
// (N92). At equal specificity the last declaration decides — exactly the
// cascade outcome for both arms while `expectCascadeModelApplies` holds.

interface RowTargetRule {
  inReduceMedia: boolean
  body: string
  /** The enclosing block headers (outermost first), e.g. `@media (…)`. */
  headers: string[]
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
    const headers = enclosingHeaders(css, m.index)
    hits.push({ inReduceMedia: headers.some(isReduceMediaHeader), body: css.slice(open + 1, close), headers })
    re.lastIndex = close + 1
  }
  return hits
}

/** The block headers enclosing position `at` (outermost first), e.g. `@media (…)`. */
function enclosingHeaders(css: string, at: number): string[] {
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
  return headers
}

/**
 * Whether a block header is exactly a prefers-reduced-motion: reduce
 * condition. Matched against the parenthesised condition, not a substring:
 * `prefers-reduced-motion: no-preference` contains the letters of "reduce"
 * and must never count as the reduce block.
 */
function isReduceMediaHeader(header: string): boolean {
  return /\(\s*prefers-reduced-motion\s*:\s*reduce\s*\)/i.test(header)
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
 * A duration the reader resolved, in milliseconds — or 'unknown' when the
 * sheet only decides the value at run time (a custom property the reader
 * cannot resolve; see expectCascadeModelApplies). 'unknown' is NEVER
 * silently zero: the oracle rejects only what it can PROVE is zero, and
 * only the documented browser lane can vouch for a value the sheet defers
 * to a variable.
 */
type DurationMs = number | 'unknown'

/** One evaluated calc operand: a time in ms, a bare number, or unresolved. */
type TimeOperand = { kind: 'ms' | 'n'; value: number } | { kind: 'unknown' }

/** Addition/subtraction of like operands; a time plus a bare number is invalid and unresolved. */
function addSub(a: TimeOperand, b: TimeOperand, op: '+' | '-'): TimeOperand {
  if (a.kind === 'unknown' || b.kind === 'unknown') return { kind: 'unknown' }
  if (a.kind !== b.kind) return { kind: 'unknown' }
  return { kind: a.kind, value: op === '+' ? a.value + b.value : a.value - b.value }
}

/** Multiplication/division. A zero factor annihilates even an unresolved operand (N90). */
function mulDiv(a: TimeOperand, b: TimeOperand, op: '*' | '/'): TimeOperand {
  if (a.kind === 'unknown' || b.kind === 'unknown') {
    if (op === '*' && ((a.kind === 'n' && a.value === 0) || (b.kind === 'n' && b.value === 0))) {
      return { kind: 'ms', value: 0 }
    }
    return { kind: 'unknown' }
  }
  if (op === '*') {
    if (a.kind === 'ms' && b.kind === 'ms') return { kind: 'unknown' } // ms² is not a time
    const time = a.kind === 'ms' ? a : b.kind === 'ms' ? b : null
    if (time) return { kind: 'ms', value: time.value * (a.kind === 'n' ? a.value : b.value) }
    return { kind: 'n', value: a.value * b.value }
  }
  if (b.value === 0) return { kind: 'unknown' } // division by zero is undecidable
  if (a.kind === 'ms' && b.kind === 'n') return { kind: 'ms', value: a.value / b.value }
  if (a.kind === 'n' && b.kind === 'n') return { kind: 'n', value: a.value / b.value }
  return { kind: 'unknown' } // ms/ms is not a time
}

/**
 * Evaluate a calc()'s expression the way the browser would: a sum of terms
 * over times (`300ms`, `1s`), bare numbers and `var()` references. A `var()`
 * is an unresolved operand — it survives only where it cannot hide a zero
 * (multiplying it by a zero factor annihilates the product, so
 * `calc(var(--x) * 0)` IS zero). A negative result is an invalid duration,
 * which the browser drops — undecidable here.
 */
function evalTimeExpr(src: string): DurationMs {
  const tokens = src.replace(/var\([^)]*\)/g, ' @var ').match(/@var|\d+(?:\.\d+)?(?:ms|s)?|[()+\-*/]/g)
  if (!tokens) return 'unknown'
  const list: string[] = tokens // narrowed alias: TS does not carry the guard into the closures below
  let pos = 0
  const peek = (): string | undefined => list[pos]
  function factor(): TimeOperand {
    const token = list[pos++]
    if (token === undefined || token === '@var') return { kind: 'unknown' }
    if (token === '(') {
      const inner = expr()
      if (list[pos++] !== ')') return { kind: 'unknown' }
      return inner
    }
    if (token === '-') {
      const operand = factor()
      return operand.kind === 'unknown' ? operand : { kind: operand.kind, value: -operand.value }
    }
    if (token === '+') return factor()
    const literal = /^(\d+(?:\.\d+)?)(ms|s)?$/.exec(token)
    if (!literal) return { kind: 'unknown' }
    return literal[2]
      ? { kind: 'ms', value: Number(literal[1]) * (literal[2] === 's' ? 1000 : 1) }
      : { kind: 'n', value: Number(literal[1]) }
  }
  function term(): TimeOperand {
    let left = factor()
    while (peek() === '*' || peek() === '/') {
      const op = list[pos++] as '*' | '/'
      left = mulDiv(left, factor(), op)
    }
    return left
  }
  function expr(): TimeOperand {
    let left = term()
    while (peek() === '+' || peek() === '-') {
      const op = list[pos++] as '+' | '-'
      left = addSub(left, term(), op)
    }
    return left
  }
  const result = expr()
  if (pos !== tokens.length) return 'unknown' // unparsed remainder: undecidable
  if (result.kind === 'unknown' || result.kind === 'n') return 'unknown'
  return result.value < 0 ? 'unknown' : result.value
}

/**
 * Evaluate one duration token the way the browser would: a literal time
 * (`1s`, `300ms`, `0s`) or a `calc()` over times, numbers and `var()`
 * references. FR-075's flash requires time that is actually non-zero, so
 * every expression that RESOLVES to zero counts as zero — `calc(0s)`,
 * `calc(300ms * 0)` (CHECK R6-01/N90) — not merely a token whose numerals
 * are all zero.
 */
function durationMs(token: string): DurationMs {
  const literal = /^(\d+(?:\.\d+)?)(ms|s)$/.exec(token)
  if (literal) return literal[2] === 's' ? Number(literal[1]) * 1000 : Number(literal[1])
  const calc = /^calc\((.*)\)$/.exec(token)
  return calc ? evalTimeExpr(calc[1]) : 'unknown' // a bare var(...) or alien token: undecidable
}

/** A rule body's declarations, in order (comments stripped; no strings/braces — see rowTargetRules). */
function parseDeclarations(body: string): Array<{ prop: string; value: string }> {
  const declarations: Array<{ prop: string; value: string }> = []
  for (const chunk of body.replace(/\/\*[\s\S]*?\*\//g, ' ').split(';')) {
    const at = chunk.indexOf(':')
    if (at > 0) declarations.push({ prop: chunk.slice(0, at).trim().toLowerCase(), value: chunk.slice(at + 1).trim() })
  }
  return declarations
}

/** The animation the cascade's declarations resolve to (the CSS initial values, to start). */
interface AnimationState {
  name: string
  duration: DurationMs
  iteration: string
}

/**
 * Fold the rules' declarations in cascade order — document order within a
 * rule, rule order across the sheet; the later declaration of the same
 * longhand wins (CHECK R6-01/N91). An `animation` shorthand sets all three
 * tracked longhands at once (its omitted components reset to the CSS
 * initials: duration 0s, one iteration), so a later shorthand overrides
 * earlier longhands of any of them, and a later longhand overrides an
 * earlier shorthand's component.
 */
function effectiveAnimation(rules: RowTargetRule[]): AnimationState {
  const state: AnimationState = { name: '', duration: 0, iteration: '1' }
  for (const rule of rules) {
    for (const declaration of parseDeclarations(rule.body)) {
      if (declaration.prop === 'animation') {
        const tokens = shorthandTokens(declaration.value)
        state.name = tokens[0] ?? ''
        const durationToken = tokens.find((t) => /^(?:\d+(?:\.\d+)?(?:s|ms)|calc\()/.test(t))
        state.duration = durationToken ? durationMs(durationToken) : 0
        const iteration = [...tokens].reverse().find((t) => /^\d+(?:\.\d+)?$/.test(t))
        state.iteration = iteration ?? '1'
      } else if (declaration.prop === 'animation-name') {
        state.name = declaration.value
      } else if (declaration.prop === 'animation-duration') {
        state.duration = durationMs(declaration.value)
      } else if (declaration.prop === 'animation-iteration-count') {
        state.iteration = declaration.value
      }
    }
  }
  return state
}

/**
 * The cascade model the two arms read through — the last matching DECLARATION
 * in document order at equal specificity — holds only while the shipped
 * sheet keeps the row target free of the constructs that would reorder it.
 * Assert them here so a future sheet change fails HERE loudly instead of
 * silently reading through the reader (CHECK R6-01: the limit is checked,
 * not assumed):
 *   1. SPECIFICITY — every use of the row-target attribute opens its own
 *      selector and each selector list is exactly `[data-settings-row-target]`;
 *      a compound, descendant or `#id` variant would out-rank the collected
 *      rules and decide a different declaration than the fold reads.
 *   2. IMPORTance — no `!important` in any row-target rule; an important
 *      declaration beats every later normal one, inverting the fold.
 *   3. LAYERS and other conditional rules — every enclosing block header of
 *      a row-target rule is either nothing or exactly the
 *      prefers-reduced-motion: reduce condition; a `@layer`, a
 *      no-preference block or any other media query changes which rules
 *      apply per arm, which the two folds do not model.
 * What the reader still cannot model even with this guard, stated plainly:
 * a custom property's VALUE (`--motion-duration-slow` is defined in the
 * imported tokens file, so `calc(var(--motion-duration-slow) * 4)` reads as
 * 'unknown' — accepted as not provably zero, never silently zero), computed
 * styles and rendered pixels — the authorized exact-commit browser lane
 * owns those.
 */
function expectCascadeModelApplies(css: string, rules: RowTargetRule[]): void {
  const occurrences = [...css.matchAll(/\[data-settings-row-target\]/g)]
  expect(occurrences.length, 'model guard premise: the sheet references the row-target attribute').toBeGreaterThan(0)
  for (const occurrence of occurrences) {
    const start = occurrence.index ?? 0
    let selectorStart = start
    while (selectorStart > 0 && !'{};'.includes(css[selectorStart - 1])) selectorStart--
    expect(
      css.slice(selectorStart, start).trim(),
      'model guard (CHECK R6-01, specificity): a selector combines the row-target attribute with other simple selectors — the fold reads only the bare attribute selector',
    ).toBe('')
    let selectorEnd = start + occurrence[0].length
    while (selectorEnd < css.length && !'{}'.includes(css[selectorEnd])) selectorEnd++
    expect(css[selectorEnd], 'model guard (CHECK R6-01, specificity): the row-target attribute is used outside a rule selector').toBe('{')
    expect(
      css.slice(start + occurrence[0].length, selectorEnd).trim(),
      'model guard (CHECK R6-01, specificity): a selector list groups the row-target attribute with another selector — grouped specificities are outside the fold',
    ).toBe('')
  }
  for (const rule of rules) {
    expect(
      rule.body.includes('!important'),
      'model guard (CHECK R6-01, importance): a row-target rule carries !important — important beats every later normal declaration, which the fold does not model',
    ).toBe(false)
    for (const header of rule.headers) {
      expect(
        isReduceMediaHeader(header),
        `model guard (CHECK R6-01, layers): a row-target rule sits inside "${header.trim()}" — only the prefers-reduced-motion: reduce condition is modeled; a @layer, a no-preference block or any other conditional block changes which rules apply per arm`,
      ).toBe(true)
    }
  }
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
    // "flashes once": the animation the cascade APPLIES to the marked row —
    // folded declaration by declaration (see the section comment) — must
    // name an animation with a NON-ZERO duration and exactly one iteration,
    // and the named keyframes must change a paintable property between
    // frames. A calc that RESOLVES to zero is zero (N90: calc(300ms * 0)),
    // a later animation-duration longhand overrides the shorthand's
    // duration (N91), a zero literal is zero (N65) and identical transparent
    // frames paint nothing (N66) — each satisfies a mere source declaration
    // but flashes nothing (FR-075 "flashes once", CHECK R5-02 + R6-01;
    // jsdom computes no animation, so this reads the effective cascade).
    const allRules = rowTargetRules(globalsCss, '[data-settings-row-target]')
    expectCascadeModelApplies(globalsCss, allRules)
    const normal = effectiveAnimation(allRules.filter((rule) => !rule.inReduceMedia))
    expect(
      normal.name,
      'RED (FR-075): the mark must be mapped to an animation — the flash has no referent',
    ).toBeTruthy()
    expect(
      normal.name,
      `FR-075: the flash animation must be enabled in normal motion, got "${normal.name}"`,
    ).not.toBe('none')
    expect(
      /^[a-z][a-z0-9-]*$/i.test(normal.name),
      `FR-075: the animation must name its keyframes, got: ${normal.name}`,
    ).toBe(true)
    expect(
      normal.duration !== 'unknown' && normal.duration === 0,
      `FR-075 (CHECK R6-01): the flash must have a non-zero duration — the effective cascade resolves the duration to ${
        normal.duration === 'unknown' ? 'an expression the reader cannot resolve (a var() the tokens file decides)' : `${String(normal.duration)}ms`
      }`,
    ).toBe(false)
    expect(
      normal.iteration,
      `FR-075: the flash must run exactly once (animation iteration count 1), got: ${normal.iteration}`,
    ).toBe('1')
    const keyframes = keyframesBody(globalsCss, normal.name)
    expect(keyframes, `FR-075: the named keyframes (${normal.name}) must exist in the stylesheet`).toBeTruthy()
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
    // "without animation": the animation the cascade would APPLY to a
    // reduced-motion user must resolve to none. Under reduce, EVERY rule for
    // the row target applies (the reduce block only ADDS rules), so the fold
    // runs over all of them: a later declaration that re-enables the
    // animation — in the same reduce rule (N92) or in a later normal rule
    // (N74) — resolves the name away from none and fails here. Replaces the
    // old oracle that accepted a reduce-block rule whose body merely
    // CONTAINED `animation: none` and a "last rule" shape check (source
    // declarations, CHECK R5-02 → CHECK R6-01); FR-075's "without animation"
    // is the effective outcome for a reduced-motion user.
    const allRules = rowTargetRules(globalsCss, '[data-settings-row-target]')
    expect(allRules.length, 'premise: the stylesheet styles the marked row').toBeGreaterThan(0)
    expect(
      allRules.some((rule) => rule.inReduceMedia),
      'the stylesheet must carry a prefers-reduced-motion: reduce rule for the marked row',
    ).toBe(true)
    expectCascadeModelApplies(globalsCss, allRules)
    const reduced = effectiveAnimation(allRules)
    expect(
      reduced.name === '' || reduced.name === 'none',
      `RED (FR-075): under prefers-reduced-motion: reduce the row must have NO animation — the effective cascade applies "${reduced.name}"`,
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
