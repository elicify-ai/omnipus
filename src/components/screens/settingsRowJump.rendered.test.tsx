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
 * folding every declaration for the row target in cascade order — an
 * `animation` shorthand is parsed by token TYPE per the CSS grammar (the
 * components may appear in ANY order: the first time is the duration, the
 * second the delay, the iteration count is a number or `infinite`, and the
 * name is whatever remains — so an explicit `infinite` is never the omitted
 * one-iteration default, CHECK R7-01/N64, and name-last order is valid,
 * CHECK R7-W01/N104), a `calc()` duration is EVALUATED, so an expression
 * that resolves to zero counts as zero (CHECK R6-01/N90; leading-decimal
 * terms like `.3s` parse, CHECK R7-02/N98), a later `animation` shorthand or
 * `animation-duration` longhand overrides an earlier duration within a rule
 * or across rules (CHECK R6-01/N91); under reduced motion the fold runs over
 * ALL matching rules, so the animation the cascade would apply must resolve
 * to none — a later declaration that re-enables it fails (CHECK R6-01/N92)
 * (FR-075 "without animation"). The model's own limits are asserted against
 * the shipped sheet, not assumed (expectCascadeModelApplies): no selector
 * variant that would out-rank the fold, no `!important`, and no `@layer` or
 * other conditional block around a row-target rule. Custom-property VALUES
 * resolve too, from the tokens file the sheet imports (the same resolution
 * the browser's cascade does), so the shipped `calc(var(--motion-duration-slow) * 4)`
 * evaluates to 1200ms; an expression the reader STILL cannot evaluate fails
 * the test with a clear message instead of guessing a value (CHECK R7-02).
 * What this still cannot see: computed styles and rendered pixels — the
 * authorized exact-commit browser lane owns those. The DOM side (the
 * mark on exactly that row) is asserted rendered: exactly one marked element
 * carrying the search index entry's row id, the jump's scroll receiver on or
 * inside it, and non-empty row content (team-lead rulings on CHECK R6-02,
 * 2026-11-22: the index label is a search name, not a row heading, and a
 * read-only row carries no control — FR-075 requires neither, so neither is
 * asserted). The browser-run witness for the same
 * legs is the e2e suite's FR-075 tests (typecheck-only here; the authorized
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
// oracles below need the source text of the mapping. The tokens file it
// imports comes too: the browser's cascade resolves var() ACROSS both files,
// so the duration reader may resolve a custom property the same way (CHECK
// R7-02: an expression it still cannot evaluate must fail the test, not
// guess — and the shipped sheet's duration calc() must be decidable).
// (Path resolution via import.meta.dirname, the pattern the WU-11 guard
// tests use; import.meta.url is an http: URL under the jsdom environment,
// not a file: URL.)
const globalsCss = readFileSync(join(import.meta.dirname, '..', '..', 'styles', 'globals.css'), 'utf8')
const tokensCss = readFileSync(join(import.meta.dirname, '..', '..', 'styles', 'tokens.generated.css'), 'utf8')

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
 * The spec-named WITNESS phrases per indexed setting row (CHECK R8-01:
 * bind the marked, scrolled row to the requested setting's own control or
 * value, located independently of the mark). Sources, never the code:
 * the 88-row map of design-notes §2 (Dataset D-9's basis: "Bind address"
 * row 52, "Port" 53, "Log level" 57, "Rotate master key" 49, "Add key" 50,
 * "View log" 48, "Rate limits per agent" 47, "MCP tool result cap" 14,
 * "Change (model + effort)" 1, the Edit-per-agent row 22, program paths 23,
 * the Memory link row 72/9, "Clear sessions" 64), "Badge corrections from
 * the code" ("wording names chat history: Clear all chat history"), D-4
 * row 7 (gateway.host is "Bind address"), FR-108, and the spec's
 * accessibility naming rules ("Every control is named by its visible
 * label"; "icon buttons name their target"; "Program fields name the
 * program").
 *
 * 'model-providers' is witnessed by its row's own heading "Providers" per
 * the team-lead ruling on the part-25 report (FR-001 names the section
 * "Providers" and the index entry "Model providers" opens that row, so the
 * heading is spec-named, not taken from the implementation; the marker
 * stays where it is in production).
 *
 * The locator (findSpecWitness, CHECK R9-01; ownership per CHECK R10-01)
 * binds each phrase by the row's WITNESS_KIND: a control by accessible role
 * and EXACT accessible name only; a heading row by its own heading (the
 * FR-001 section-title h2 is the only match tolerated outside the marked
 * row); a read-only value or name line by exact own visible text — never
 * for a control row. Ownership is all-inside: every perceivable element in
 * the document with that exact name (and role, for a control) must sit
 * inside the marked row; hidden text and prefix matches never witness.
 */
const WITNESS_PHRASES: Record<string, string[]> = {
  'model-providers': ['Providers'], // FR-001 names the section "Providers" — the row heading is spec-named (team-lead ruling, part 25)
  'default-model': ['Change'],
  'active-transcriber': ['Active transcriber'],
  'voice-input': ['Voice input'],
  'auto-recap': ['Auto recap'],
  'bootstrap-recap': ['Bootstrap recap'],
  'summarization-model': ['Summarization model'],
  'recap-limits': ['Recap limits'],
  'how-long-memory-is-kept': ['How long memory is kept', 'Data & retention'],
  judge: ['Edit Judge'],
  'plan-supervisor': ['Edit Plan Supervisor'],
  omnipus: ['Edit Omnipus'],
  'claude-code-program-path': ['Claude Code program path'],
  'codex-program-path': ['Codex program path'],
  'opencode-program-path': ['OpenCode program path'],
  'max-parallel-agents': ['Max parallel agents'],
  'tries-per-goal': ['Tries per goal'],
  'idle-reminder-quiet-window': ['Idle reminder quiet window'],
  'stalled-work-check-ins': ['Stalled-work check-ins'],
  'max-tool-calls-per-turn': ['Max tool calls per turn'],
  'mcp-tool-result-cap': ['MCP tool result cap'],
  'default-context-window': ['Default context window'],
  'model-overrides': ['Model overrides'],
  'security-health': ['Security health'],
  'auto-approve': ['Auto-approve'],
  'god-mode': ['God Mode'],
  'prompt-injection-defense': ['Prompt injection defense'],
  'rate-limits': ['Rate limits per agent'],
  'audit-log': ['View log'],
  'sandbox-mode': ['Sandbox mode'],
  'filesystem-model': ['Filesystem model'],
  'shell-workspace-limit': ['Shell workspace limit'],
  'allowed-folders': ['Allowed folders'],
  'internal-network-addresses': ['Internal network addresses'],
  'network-proxy-for-shell-commands': ['Network proxy for shell commands'],
  'stored-keys': ['Add key'],
  'master-key': ['Rotate master key'],
  'bind-address': ['Bind address'],
  port: ['Port'],
  'gateway-token': ['Gateway token'],
  'preview-server': ['Preview server'],
  'log-level': ['Log level'],
  'restart-gateway': ['Restart gateway'],
  'remote-access': ['Remote access'],
  storage: ['Storage'],
  'keep-chat-history-for': ['Keep chat history for'],
  'keep-memory-for': ['Keep memory for'],
  'clear-all-chat-history': ['Clear all chat history'],
  version: ['Version'],
}

/**
 * The ARIA roles a spec-named CONTROL can carry. The words are the spec's
 * own control vocabulary, not an inventory of this build: design-notes §2
 * names switches, radios, Select fields, number and text fields, buttons
 * and link rows per setting, and spec:544 adds that every control is named
 * by its visible label, icon buttons name their target and program fields
 * name the program; a `fieldset`/`group` carries a named radio group
 * ("Sandbox mode"). Testing Library role queries ignore hidden elements by
 * default (`hidden: false`), which is the perceivability rule the witness
 * needs (CHECK R9-01). Row headings witness through the separate heading
 * leg, never through this set.
 */
const CONTROL_WITNESS_ROLES = [
  'button',
  'switch',
  'checkbox',
  'radio',
  'combobox',
  'textbox',
  'spinbutton',
  'slider',
  'searchbox',
  'link',
  'group',
] as const

/**
 * The witness KIND per indexed row — what the SPEC names as that row's own
 * witness (never what the build renders):
 *  - `control` — the spec names an interactive control (design-notes §2's
 *    switch / radio group / Select / number / text field / button /
 *    disclosure for that row; spec:544's naming rules). Its witness leg is
 *    role + exact accessible name ONLY — the own-text leg never applies
 *    (CHECK R10-01).
 *  - `heading` — the spec names the row by a heading: ruling B's
 *    "Providers" section heading for Model providers (FR-001), and the
 *    protection/network/storage rows the map names by their row heading.
 *  - `name-line` — the spec names the row by a read-only value or the
 *    row's/group's own name line; these are the ONLY rows the own-text leg
 *    may witness, each cited below (CHECK R10-01).
 */
type WitnessKind = 'control' | 'heading' | 'name-line'

const WITNESS_KINDS: Record<string, WitnessKind> = {
  // control rows — design-notes §2 names the control each phrase belongs to
  'default-model': 'control', // row 1: the "Change (model + effort)" action
  'active-transcriber': 'control', // row 32 (R30a): "Active transcriber (Select)"
  'auto-recap': 'control', // row 65: the Auto recap switch
  'bootstrap-recap': 'control', // row 67: the Bootstrap recap switch
  judge: 'control', // row 22: Edit; spec:544 icon buttons name their target
  'plan-supervisor': 'control', // row 22
  omnipus: 'control', // row 22
  'claude-code-program-path': 'control', // row 23; spec:544 program fields name the program
  'codex-program-path': 'control', // row 23
  'opencode-program-path': 'control', // row 23
  'max-parallel-agents': 'control', // row 78: number
  'tries-per-goal': 'control', // row 80: number
  'idle-reminder-quiet-window': 'control', // row 81: number
  'stalled-work-check-ins': 'control', // row 82: switch
  'max-tool-calls-per-turn': 'control', // row 84: number
  'mcp-tool-result-cap': 'control', // row 14: cap number
  'default-context-window': 'control', // row 19: number
  'auto-approve': 'control', // row 36: the Auto-approve switch
  'audit-log': 'control', // row 48: the "View log" action
  'sandbox-mode': 'control', // row 42: 3 radios — the named fieldset group
  'filesystem-model': 'control', // row 43: 2 radios — the named fieldset group
  'stored-keys': 'control', // row 50: "Add key"
  'master-key': 'control', // row 49: "Rotate master key"
  'bind-address': 'control', // row 52: the Bind address select
  port: 'control', // row 53; D-4 row 6: the Port number field
  'preview-server': 'control', // row 56: switch
  'log-level': 'control', // row 57: Select field
  'remote-access': 'control', // row 60: the disclosure control
  'keep-chat-history-for': 'control', // row 61: number field
  'keep-memory-for': 'control', // row 73: number field
  'clear-all-chat-history': 'control', // row 64 + badge corrections: the button
  // heading rows — the map/ruling names the row by its heading
  'model-providers': 'heading', // ruling B, FR-001: the row's own "Providers" heading
  'security-health': 'heading', // row 34: "Security health"
  'god-mode': 'heading', // row 59; D-9 row 8: "God Mode" (the action button carries "…")
  'prompt-injection-defense': 'heading', // row 41: the row's own h3 name
  'rate-limits': 'heading', // row 47: "Rate limits per agent"
  'network-proxy-for-shell-commands': 'heading', // row 40: h3
  storage: 'heading', // §2 read-only displays (line 170): the "Storage" heading
  // name-line rows — the row's/group's own name line or a read-only value;
  // the ONLY rows the own-text leg may witness (CHECK R10-01)
  'shell-workspace-limit': 'name-line', // row 44: the row's own name line
  'allowed-folders': 'name-line', // row 45: the row's own name line
  'internal-network-addresses': 'name-line', // row 46: the row's own name line
  'gateway-token': 'name-line', // rows 54/55: the row's own name line
  'restart-gateway': 'name-line', // row 58: the row's own name line
  version: 'name-line', // §2 read-only displays (line 170): system info value
  'voice-input': 'name-line', // D-9 row 5: the group's own name
  'recap-limits': 'name-line', // row 71: the collapsed group's own name
  'summarization-model': 'name-line', // row 69: the row's own name line
  'how-long-memory-is-kept': 'name-line', // rows 61/72/73; spec:399: the row's own name line
}

/**
 * Whole-name, case-insensitive accessible-name pattern: the phrase must be
 * the element's COMPLETE accessible name (whitespace runs fold), never a
 * prefix of it — "Portable charger status" is not the spec-named "Port"
 * (CHECK R9-01/N133), and "Restart gateway…" is not "Restart gateway".
 */
function exactNamePattern(phrase: string): RegExp {
  const escaped = phrase.trim().replace(/[.*+?${}()|[\]\\]/g, '\\$&')
  return new RegExp(`^${escaped.replace(/\s+/g, '\\s+')}$`, 'i')
}

/**
 * Perceivable in this jsdom harness: no `hidden` attribute, no
 * `aria-hidden="true"`, no inline `display:none`/`visibility:hidden` on the
 * element itself or any ancestor. Hidden elements never witness a row
 * (CHECK R9-01/N131, N132). Stated limit: jsdom applies no stylesheet in
 * this file, so stylesheet-driven hiding is invisible here — the
 * real-browser test (tests/e2e/settings-row-jump-motion.spec.ts) owns the
 * rendered truth for FR-075.
 */
function isPerceivable(el: Element): boolean {
  for (let node: Element | null = el; node !== null; node = node.parentElement) {
    if (node.hasAttribute('hidden')) return false
    if ((node.getAttribute('aria-hidden') ?? '').trim().toLowerCase() === 'true') return false
    const inline = node.getAttribute('style') ?? ''
    if (/(?:^|;)\s*display\s*:\s*none/i.test(inline)) return false
    if (/(?:^|;)\s*visibility\s*:\s*hidden/i.test(inline)) return false
  }
  return true
}

/** The element's OWN text (direct text nodes only), whitespace-folded, lowercased. */
function ownText(el: Element): string {
  let text = ''
  for (const child of el.childNodes) {
    if (child.nodeType === child.TEXT_NODE) text += child.textContent ?? ''
  }
  return text.replace(/\s+/g, ' ').trim().toLowerCase()
}

/**
 * The exact own-text matches for a phrase, document-wide, perceivable only —
 * the name-line matcher, shared by the name-line leg and the shared-name
 * allowance below.
 */
function exactTextMatches(phrase: string): Element[] {
  const needle = phrase.replace(/\s+/g, ' ').trim().toLowerCase()
  return Array.from(document.body.querySelectorAll('*')).filter(
    (el) => isPerceivable(el) && ownText(el) === needle,
  )
}

/**
 * Genuine duplicate CONTROL names, handled explicitly (CHECK R10-01's
 * strengthening move) — the DEFAULT is the absolute rule: every perceivable
 * match with that exact role and name must sit inside the marked row. An
 * entry names the rare row whose spec-named control shares its exact
 * accessible name with another rendered surface, and adds a CO-WITNESS the
 * marked row must also own under the all-inside name-line rule, so a
 * stand-in carrying only the control still fails:
 *  - 'default-model': its spec-named "Change" action (design-notes §2 row 1)
 *    shares the exact name with the Web & Search group's own "Change"
 *    toggle — a moved-out surface still rendered inside Providers until
 *    FR-060 lands (web search moves to Skills & Tools; CHECK round 9's
 *    retained caveat: "Providers is pre-landing state"). The co-witness is
 *    the card's own "Default model" name line (the search index entry's
 *    label, spec:544's visible-label rule).
 */
const WITNESS_SHARED_CONTROL_NAMES: Record<string, { phrase: string; coWitness: string }> = {
  'default-model': { phrase: 'Change', coWitness: 'Default model' },
}

/**
 * The ownership rule every witness leg answers (CHECK R10-01, replacing the
 * mutable-id tie): the row OWNS its witness only when every perceivable
 * element in the document carrying that exact name — and role, for a
 * control — sits inside the marked row. One match outside the marked row
 * means the real setting is elsewhere, and the leg yields no witness.
 */
function legWitness(matches: Element[], row: Element): Element | null {
  const inside = matches.filter((m) => row.contains(m))
  if (inside.length === 0) return null
  return matches.length === inside.length ? inside[0] : null
}

/**
 * The heading leg's duplicate handling for ruling B: the row must carry its
 * own heading match, and an additional match outside the marked row is
 * acceptable only at SECTION-TITLE level (an h2 — FR-001's eleven section
 * titles render as h2), never a competing row heading (h3 and below) or any
 * other stray match.
 */
function headingWitness(matches: Element[], row: Element): Element | null {
  const inside = matches.filter((m) => row.contains(m))
  if (inside.length === 0) return null
  for (const m of matches) {
    if (!row.contains(m) && m.tagName !== 'H2') return null
  }
  return inside[0]
}

/**
 * Locate the requested setting's own spec-named witness for the marked row
 * (CHECK R9-01, ownership per CHECK R10-01). The row's WITNESS_KIND decides
 * the leg, and the leg decides what may witness:
 *  - control: Testing Library role queries over CONTROL_WITNESS_ROLES with
 *    the EXACT accessible name (whole name, case-insensitive, never a
 *    prefix). EVERY perceivable match in the document must sit inside the
 *    marked row — a copy outside it fails, because the real setting is
 *    elsewhere (R10-P02, R10-P03). The own-text leg never applies to a
 *    control row.
 *  - heading: the heading role with the exact name; the row must carry its
 *    own heading, and the only tolerated outside match is the section-title
 *    h2 (ruling B's "Providers" against the FR-001 section title).
 *  - name-line: the element whose OWN visible text is exactly the phrase —
 *    again all matches inside the marked row. Restricted to the rows
 *    WITNESS_KINDS lists as value/name-line rows, each citing its spec
 *    source.
 * Hidden text witnesses nothing, and a longer word that merely starts with
 * the phrase witnesses nothing. Matching is case-insensitive: the sheet's
 * typographic casing may differ from the spec's ("Remote Access", "View
 * Log", "opencode program path").
 */
function findSpecWitness(row: Element, phrases: string[], kind: WitnessKind, entryRowId?: string): Element | null {
  for (const phrase of phrases) {
    const pattern = exactNamePattern(phrase)
    if (kind === 'heading') {
      const witness = headingWitness(screen.queryAllByRole('heading', { name: pattern }), row)
      if (witness) return witness
      continue
    }
    if (kind === 'control') {
      for (const role of CONTROL_WITNESS_ROLES) {
        const matches = screen.queryAllByRole(role, { name: pattern })
        const inside = matches.filter((m) => row.contains(m))
        if (inside.length === 0) continue
        if (matches.length === inside.length) return inside[0]
        // Genuine duplicate, allowed only through the explicit map: the row
        // keeps an exact-name match inside it AND also owns its co-witness
        // name line under the all-inside rule — so a stand-in carrying only
        // the control (the real control elsewhere) still fails.
        const allowance = entryRowId ? WITNESS_SHARED_CONTROL_NAMES[entryRowId] : undefined
        if (allowance && allowance.phrase === phrase) {
          const coMatches = exactTextMatches(allowance.coWitness)
          const coInside = coMatches.filter((m) => row.contains(m))
          if (coMatches.length === coInside.length) return inside[0]
        }
        return null // a same-named copy outside the marked row: not owned
      }
      continue
    }
    const witness = legWitness(exactTextMatches(phrase), row)
    if (witness) return witness
  }
  return null
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
 *
 * Returns that toggle: it is the group WITNESS — located independently of
 * any mark, by the name FR-058 gives the group — which the mark-shape
 * oracle binds the marked row to (CHECK R7-03: a wrong-but-real group
 * opening elsewhere while another node agrees with the implementation's id
 * is not enough).
 */
async function expectGroupOpen(testId: string, headerName: string): Promise<HTMLElement> {
  const toggle = within(screen.getByTestId(testId)).getByRole('button', { name: headerName })
  await waitFor(
    () =>
      expect(
        toggle,
        'RED (FR-075): a row inside a group that is closed — the jump must open that group first',
      ).toHaveAttribute('aria-expanded', 'true'),
    { timeout: 5_000 },
  )
  return toggle
}

/**
 * Wait for the mark, then pin its whole shape — FR-075 "marks" names that
 * one setting row, not a class of them, so the marked element must BE the
 * row the person asked for (team-lead rulings on CHECK R6-02, 2026-11-22):
 *
 * 1. Identity: exactly one element carries the mark (`data-settings-row-target`
 *    / `aria-current`), and it is the element carrying the requested
 *    `data-settings-row` id — the search index entry's row id, including the
 *    overrides the index declares (rowFor resolved it; marked[0] === row).
 *    CHECK R6-02 (N77): the id alone moved WITH the mark onto an unrelated
 *    empty element and satisfied the old oracle. The id element is located
 *    independently of the mark — the index declares it, rowFor queries the
 *    document for it — and the mark must sit on it (and on nothing else:
 *    exactly one marked element), so the mark never sits on any element
 *    outside it, and the scroll receiver is on or inside it.
 * 2. The row itself, not a stand-in:
 *    - the jump's `scrollIntoView` receiver is the marked element or a
 *      descendant of it (FR-075: "the row scrolls into view"), and
 *    - the element is not empty: it carries non-empty visible text, so an
 *      empty or unrelated sibling fails both legs, and
 *    - when the caller hands in a group WITNESS (a closed-group case:
 *      expectGroupOpen located the named group's own header toggle by
 *      accessible name — FR-058's group, independently of any mark), that
 *      witnessed header must lie ON or INSIDE the marked row. A
 *      wrong-but-real group marked and scrolled while the requested group
 *      opens elsewhere (CHECK R7-03/N100), or a non-empty unrelated
 *      sibling carrying the id (CHECK R7-03/N99), puts the mark where the
 *      witnessed header is not — the id moving WITH the mark no longer
 *      satisfies the oracle.
 *    Two legs the rulings dropped, with their reasons: the search index's
 *    label is a search name, not a row heading (FR-075 does not require the
 *    row to repeat it), so the row is NOT required to contain its search
 *    label; and a read-only row legitimately carries no heading, label or
 *    interactive control (FR-075 does not require one), so no such
 *    furniture is required either. A text-only stand-in stays bounded by
 *    the identity, only-one-mark and scroll-receiver legs — and, for the
 *    closed-group cases, by the witnessed group header above.
 * 3. The requested setting's own witness (CHECK R8-01/N114, N115; strength-
 *    ened R9-01/N131–N133; ownership per R10-01/R10-P02/R10-P03): when the
 *    caller names the entry's row id, the marked row must carry the
 *    spec-named control or value of THAT setting (WITNESS_PHRASES +
 *    WITNESS_KINDS, from Dataset D-9's map, never from the implementation),
 *    and must not carry another listed setting's witness while that
 *    setting's own row sits elsewhere — the id, mark and scroll moving
 *    TOGETHER onto a wrong real row (the Log-level row answering a
 *    Bind-address or Port jump) is the fault this leg kills. Ownership is
 *    independent of the mark and of the mutable row id: for a control row
 *    the witness is the role + exact-accessible-name match, and EVERY
 *    perceivable element in the document with that exact role and name must
 *    sit inside the marked row — a copy outside it fails, because the real
 *    setting is elsewhere; the own-text leg exists only for the rows
 *    WITNESS_KINDS lists as value/name-line rows, under the same
 *    all-inside rule; a heading row carries its own heading, with only the
 *    FR-001 section-title h2 tolerated outside (ruling B). Hidden text and
 *    "Portable charger status" do not witness a Port jump. A real nested
 *    row is not a foreign mark: the other row genuinely lying inside (or
 *    around) the marked one passes.
 */
async function expectExactlyThisRowMarked(
  row: HTMLElement,
  witnesses?: { groupWitness?: HTMLElement; entryRowId?: string },
): Promise<void> {
  await waitFor(() => {
    expect(row, 'RED (FR-075): the reached row must be marked').toHaveAttribute('data-settings-row-target')
    expect(row, 'RED (FR-075): the reached row must be marked to assistive tech').toHaveAttribute('aria-current', 'true')
  })
  const marked = document.querySelectorAll('[data-settings-row-target]')
  expect(marked.length, 'FR-075: exactly that row — one marked row on the page').toBe(1)
  expect(marked[0], 'FR-075: the one marked row is the reached row itself').toBe(row)
  const scrolled = (Element.prototype.scrollIntoView as ReturnType<typeof vi.fn>).mock
    .contexts as HTMLElement[]
  expect(
    scrolled.some((el) => el === row || row.contains(el)),
    'RED (FR-075): the jump must scroll the reached row into view — the scroll ran on an element that is neither the marked row nor inside it',
  ).toBe(true)
  expect(
    (row.textContent ?? '').trim().length,
    'FR-075: the marked row must carry its own content — it must not be empty',
  ).toBeGreaterThan(0)
  if (witnesses?.groupWitness) {
    expect(
      row === witnesses.groupWitness || row.contains(witnesses.groupWitness),
      'RED (FR-075, CHECK R7-03): the marked row must be the reached group setting\'s own row — the group header witnessed by name must lie on or inside the marked row, not elsewhere while the id agrees with the implementation',
    ).toBe(true)
  }
  if (witnesses?.entryRowId) {
    const phrases = WITNESS_PHRASES[witnesses.entryRowId]
    const kind = WITNESS_KINDS[witnesses.entryRowId]
    if (phrases && kind) {
      expect(
        findSpecWitness(row, phrases, kind, witnesses.entryRowId),
        `RED (FR-075, CHECK R8-01/R9-01/R10-01): the marked row must own the requested setting's witness — no ${kind} leg for "${witnesses.entryRowId}" (${phrases.join(' / ')}) holds with every exact-name match inside the marked row, while the id agrees with the implementation`,
      ).not.toBeNull()
      for (const [otherId, otherPhrases] of Object.entries(WITNESS_PHRASES)) {
        if (otherId === witnesses.entryRowId) continue
        const other = findSpecWitness(row, otherPhrases, WITNESS_KINDS[otherId])
        if (!other) continue
        const otherRow = document.querySelector<HTMLElement>(`[data-settings-row="${otherId}"]`)
        if (otherRow && (row === otherRow || row.contains(otherRow) || otherRow.contains(row))) continue
        expect(
          other,
          `FR-075 (CHECK R8-01): the marked row for "${witnesses.entryRowId}" carries another listed setting's witness (${otherId}: ${otherPhrases.join(' / ')}) while that setting's own row is elsewhere — the mark may not sit on a foreign row`,
        ).toBeNull()
      }
    }
  }
}

// ── Stylesheet-side oracle (jsdom loads no stylesheet; see the header) ────────
//
// THE GRAMMAR THIS READER FULLY UNDERSTANDS (CHECK round 8 strategy ruling:
// stop modelling more CSS — fail closed instead). Anything on the row target
// that could change the flash and falls outside this grammar FAILS the test,
// naming the declaration, as "unsupported by the FR-075 oracle":
// unsupported means the row is NOT PROVEN to flash once.
//   - Properties: the `animation` shorthand and the longhands animation-name,
//     animation-duration, animation-delay, animation-iteration-count,
//     animation-play-state. Any other `animation-*` property is unsupported.
//     Non-animation properties (colors, spacing, …) do not change the flash
//     and are ignored.
//   - Shorthand components, in ANY order, one each (the css-animations
//     grammar): a time — the FIRST is the duration, the SECOND the delay;
//     the iteration count — a number or the keyword `infinite` (an explicit
//     infinite is never the omitted one-iteration default, R7-01/N64); a
//     play-state keyword (`running`, `paused` — a paused flash does not
//     flash, R8-04); the reserved easing/direction/fill-mode keywords
//     (consumed — the name may sit in any position, R7-W01/N104); and the
//     keyframes name — the first remaining ident; a SECOND remaining ident
//     is unsupported. Predefined keywords match ASCII-case-insensitively,
//     as CSS does (INFINITE is infinite, R8-02/N107); a custom name keeps
//     its case.
//   - Times: a literal `<number>(ms|s)` (leading decimal point allowed,
//     R7-02) or `calc()` over times, numbers, `+ - * /`, parentheses, nested
//     `calc()` and `var()` references. The calc text must be consumed
//     COMPLETELY — any unrecognised character or leftover is unsupported,
//     never silently dropped (R8-05/N120).
//   - var(): resolves from the row-target rules' OWN custom-property
//     declarations (last wins in fold order — the element-local cascade,
//     R8-03/N113) and then from the tokens file the sheet imports (the same
//     cascade resolution the browser does). A referenced property declared
//     by ANY OTHER selector in the sheet — an ancestor or sibling rule — is
//     unsupported: the value on the marked element would depend on cascade
//     details the reader does not model.
//
// KNOWN LIMITS of this reader (CHECK round 9, R9-02; team-lead stop rule:
// the hand-written reader is not extended again). Named boundary challenges
// it does NOT model, each with CHECK's receipt: the `all` shorthand resets
// the animation-name (N121); CSS-escaped property names
// (`anim\61 tion-duration`, N122); a selector other than the exact
// row-target attribute that also matches the row (`html body *` with
// !important, N123); transitive custom-property dependency chains — only
// the row-target rules' own declarations and the tokens file are sources,
// never an alias that resolves outside them (N124); an unknown function
// token in the shorthand (N125); duplicate shorthand components (N126); a
// media condition beyond the exact `prefers-reduced-motion: reduce` header
// (N127). The shorthand's own reset (R9-03/N128) IS modeled — play-state
// resets to running — but only that component: a later shorthand's reset of
// the other omitted components follows the fold lines above. Stated
// precisely (CHECK round 10): a declaration the reader RECOGNIZES as
// animation-related but cannot evaluate is rejected as unsupported — "not
// proven to flash once"; the challenges above are forms the reader does not
// recognize at all, so they are named limits, not rejections. THE
// REAL-BROWSER TEST IS THE AGREED ROUTE for the FR-075 motion claim:
// tests/e2e/settings-row-jump-motion.spec.ts reads the marked row's
// element.getAnimations() in real Chromium under normal and reduced motion;
// its first executed run belongs to the hands-on/CI lane (the author's run
// is blocked at the e2e key preflight), so it is a pending route here, not
// an executed result. This jsdom oracle stays for the named kills the
// retained ledger records for this row test (CHECK round 10: 39 raw KILLED
// classifications) against the shipped sheet.
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
 * The tokens file the sheet imports defines the custom properties its
 * durations reference (`--motion-duration-slow: 300ms`), tracked in git
 * like the sheet itself. The map is the cascade's last-wins lookup the
 * browser performs when var() resolves — read once, beside the sheet.
 */
const CUSTOM_PROPERTIES = new Map<string, string>()
for (const m of tokensCss.matchAll(/(--[a-zA-Z0-9-]+)\s*:\s*([^;{}]+)[;]/g)) {
  CUSTOM_PROPERTIES.set(m[1], m[2].trim())
}

/**
 * A duration the reader resolved, in milliseconds — or 'unknown' when the
 * expression cannot be evaluated. Since CHECK R7-02 the reader resolves
 * var() from the tokens file (the same resolution the browser's cascade
 * does), so an 'unknown' is a real hole in the reader's reach, not a
 * routine run-time value — and the oracle FAILS the test on it with a
 * clear message instead of guessing (FR-075 promises a decidable short
 * flash; an undecidable one proves nothing).
 */
type DurationMs = number | 'unknown'

/** Depth limit for var() chains, so a self-referencing property terminates. */
const VAR_DEPTH_LIMIT = 8

/** Custom-property values the cascade consults for var() resolution. */
type CustomProps = ReadonlyMap<string, string>

/** No element-local declarations — resolution falls through to the tokens file. */
const NO_LOCAL_PROPS: CustomProps = new Map()

/** One evaluated calc operand: a time in ms, a bare number, or unresolved. */
type TimeOperand = { kind: 'ms' | 'n'; value: number } | { kind: 'unknown' }

/** Index of the first comma outside parentheses (-1 if none) — var(name, fallback). */
function topLevelComma(s: string): number {
  let depth = 0
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (c === '(') depth++
    else if (c === ')') depth--
    else if (c === ',' && depth === 0) return i
  }
  return -1
}

/** One duration operand from a VALUE the cascade supplies (a var's resolved value or fallback). */
function timeOperandOfValue(value: string, depth: number, props: CustomProps): TimeOperand {
  const trimmed = value.trim()
  const literal = /^((?:\d+(?:\.\d+)?|\.\d+))(ms|s)$/i.exec(trimmed)
  if (literal) return { kind: 'ms', value: Number(literal[1]) * (literal[2].toLowerCase() === 's' ? 1000 : 1) }
  if (/^(?:\d+(?:\.\d+)?|\.\d+)$/.test(trimmed)) return { kind: 'n', value: Number(trimmed) }
  const resolved = evalTimeExpr(trimmed, depth, props)
  return resolved === 'unknown' ? { kind: 'unknown' } : { kind: 'ms', value: resolved }
}

/**
 * Resolve `var(name, fallback?)` the way the cascade does: the custom
 * property's value if defined — the row-target rules' own declarations
 * first (the element-local cascade, R8-03), then the tokens file's root
 * values — itself evaluated (it may be a time, a number, a calc() or a
 * further var()); else the fallback; else unresolved. Depth-limited so a
 * self-referencing property terminates as unresolved.
 */
function resolveVarArgs(args: string, depth: number, props: CustomProps): TimeOperand {
  if (depth <= 0) return { kind: 'unknown' }
  const at = topLevelComma(args)
  const name = (at === -1 ? args : args.slice(0, at)).trim()
  const custom = props.get(name) ?? CUSTOM_PROPERTIES.get(name)
  if (custom !== undefined) return timeOperandOfValue(custom, depth - 1, props)
  const fallback = at === -1 ? '' : args.slice(at + 1).trim()
  if (!fallback) return { kind: 'unknown' }
  return timeOperandOfValue(fallback, depth - 1, props)
}

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
 * over times (`300ms`, `1s`, `.3s` — a leading decimal point is part of the
 * number, CHECK R7-02/N98), bare numbers, parentheses, nested `calc()` and
 * `var()` references. A `var()` resolves from the row-target rules' own
 * custom properties (or the tokens file's, or its fallback) — the same
 * resolution the browser's cascade does — and survives as unresolved only
 * where it cannot hide a zero (multiplying it by a zero factor annihilates
 * the product, so `calc(var(--x) * 0)` IS zero). A negative result is an
 * invalid duration, which the browser drops — unresolved here, and
 * unresolved FAILS the oracle rather than guessing (see DurationMs). The
 * lexer consumes the WHOLE text (CHECK R8-05/N120): a scan that skipped
 * unrecognised characters once projected `calc(300ms + 1ms rubbish)` onto a
 * 301ms duration — so any character that is not whitespace, a var
 * placeholder, a number with an optional unit, a nested `calc(` or an
 * operator makes the whole expression unresolved, and leftover after the
 * grammar does the same. Depth-limited so var() chains terminate.
 */
function evalTimeExpr(src: string, depth: number = VAR_DEPTH_LIMIT, props: CustomProps = NO_LOCAL_PROPS): DurationMs {
  if (depth <= 0) return 'unknown'
  const varArgs: string[] = []
  const replaced = src.replace(/var\(\s*([^)]*)\)/gi, (_all, args: string) => {
    varArgs.push(args.trim())
    return ' @var '
  })
  const tokens: string[] = []
  let at = 0
  while (at < replaced.length) {
    const rest = replaced.slice(at)
    if (rest.startsWith('@var')) {
      tokens.push('@var')
      at += 4
      continue
    }
    const ws = /^\s+/.exec(rest)
    if (ws) {
      at += ws[0].length
      continue
    }
    const number = /^(?:\d+(?:\.\d+)?|\.\d+)(?:ms|s)?/i.exec(rest)
    if (number) {
      tokens.push(number[0])
      at += number[0].length
      continue
    }
    const nestedCalc = /^calc\(/i.exec(rest)
    if (nestedCalc) {
      tokens.push('calc(')
      at += nestedCalc[0].length
      continue
    }
    if (/^[()+\-*/]/.test(rest)) {
      tokens.push(rest[0])
      at += 1
      continue
    }
    return 'unknown' // an unrecognised character: the text is not a calc this reader understands
  }
  const list: string[] = tokens // narrowed alias: TS does not carry the guard into the closures below
  let pos = 0
  let varIndex = 0 // @var tokens appear in the varArgs capture order
  const peek = (): string | undefined => list[pos]
  function factor(): TimeOperand {
    const token = list[pos++]
    if (token === undefined) return { kind: 'unknown' }
    if (token === '@var') return resolveVarArgs(varArgs[varIndex++] ?? '', depth, props)
    if (token === '(' || token === 'calc(') {
      const inner = expr()
      if (list[pos++] !== ')') return { kind: 'unknown' }
      return inner
    }
    if (token === '-') {
      const operand = factor()
      return operand.kind === 'unknown' ? operand : { kind: operand.kind, value: -operand.value }
    }
    if (token === '+') return factor()
    const literal = /^((?:\d+(?:\.\d+)?|\.\d+))(ms|s)?$/i.exec(token)
    if (!literal) return { kind: 'unknown' }
    return literal[2]
      ? { kind: 'ms', value: Number(literal[1]) * (literal[2].toLowerCase() === 's' ? 1000 : 1) }
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
  if (pos !== list.length) return 'unknown' // leftover after the grammar: not evaluated
  if (result.kind === 'unknown' || result.kind === 'n') return 'unknown'
  return result.value < 0 ? 'unknown' : result.value
}

/**
 * Evaluate one duration token the way the browser would: a literal time
 * (`1s`, `300ms`, `0s`, `.3s` — CHECK R7-02), a `calc()` over times,
 * numbers and `var()` references, or a bare `var()` (an
 * `animation-duration` longhand may carry one). FR-075's flash requires
 * time that is actually non-zero, so every expression that RESOLVES to
 * zero counts as zero — `calc(0s)`, `calc(300ms * 0)` (CHECK R6-01/N90),
 * `calc(.3s - 300ms)` (CHECK R7-02/N98) — and an expression the reader
 * cannot evaluate returns 'unknown', which the oracle FAILS on instead of
 * guessing a value (CHECK R7-02; see DurationMs).
 */
function durationMs(token: string, props: CustomProps = NO_LOCAL_PROPS): DurationMs {
  const literal = /^((?:\d+(?:\.\d+)?|\.\d+))(ms|s)$/i.exec(token)
  if (literal) return literal[2].toLowerCase() === 's' ? Number(literal[1]) * 1000 : Number(literal[1])
  const calc = /^calc\((.*)\)$/i.exec(token)
  if (calc) return evalTimeExpr(calc[1], VAR_DEPTH_LIMIT, props)
  const varRef = /^var\(([^)]*)\)$/.exec(token)
  if (varRef) {
    const operand = resolveVarArgs(varRef[1], VAR_DEPTH_LIMIT, props)
    return operand.kind === 'ms' ? operand.value : 'unknown' // a bare number is no duration
  }
  return 'unknown' // an alien token: not evaluated
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
  /** The shorthand's SECOND time token (the first is the duration); no oracle reads it yet. */
  delay: DurationMs
  iteration: string
  /** `running` (the CSS initial) or `paused` — a paused flash does not flash (R8-04). */
  playState: string
  /**
   * The first declaration the reader could not classify or evaluate, named
   * in full, or null. FAIL-CLOSED (CHECK round 8 strategy ruling): the
   * reader understands only the grammar stated at the top of this section,
   * and anything outside it that could change the flash means the row is
   * NOT PROVEN to flash once — both arms fail on it.
   */
  unsupported: string | null
}

/**
 * The `animation` shorthand's reserved keywords that this oracle only
 * CONSUMES (easing, direction, fill-mode): a bare token matching one of
 * these is its component, NEVER the keyframes name. The iteration count
 * (`infinite`, numbers) and play-state (`running`, `paused`) components are
 * tracked, not consumed. The function-valued components (cubic-bezier(),
 * steps()) arrive as function tokens and are classified by their trailing
 * ')'. All matching is ASCII-case-insensitive, as CSS predefined keywords
 * are (INFINITE is infinite, R8-02); a custom name keeps its case.
 */
const SHORTHAND_KEYWORDS = new Set([
  'linear', 'ease', 'ease-in', 'ease-out', 'ease-in-out', // easing functions
  'normal', 'reverse', 'alternate', 'alternate-reverse', // direction
  'forwards', 'backwards', 'both', // fill-mode
])
const PLAY_STATE_KEYWORDS = new Set(['running', 'paused'])
const TIME_TOKEN = /^(?:\d+(?:\.\d+)?|\.\d+)(?:ms|s)$/i
const NUMBER_TOKEN = /^(?:\d+(?:\.\d+)?|\.\d+)$/

/** The animation longhands the grammar models; any other animation-* is unsupported. */
const MODELED_LONGHANDS = new Set([
  'animation-name',
  'animation-duration',
  'animation-delay',
  'animation-iteration-count',
  'animation-play-state',
])

/**
 * Fold the rules' declarations in cascade order — document order within a
 * rule, rule order across the sheet; the later declaration of the same
 * longhand wins (CHECK R6-01/N91). An `animation` shorthand sets all the
 * tracked longhands at once (its omitted components reset to the CSS
 * initials: duration 0s, one iteration, running), so a later shorthand
 * overrides earlier longhands of any of them, and a later longhand
 * overrides an earlier shorthand's component.
 *
 * Custom properties declared by the rules THEMSELVES are the element-local
 * cascade (R8-03/N113): collected first (all declarations, so an earlier
 * rule's var() sees a later rule's value, as computed-value time does),
 * then layered over the tokens file by resolveVarArgs. A var() referenced
 * by an animation declaration must not be declared by any OTHER selector in
 * the sheet — such a declaration would change the element's value through
 * cascade details the reader does not model, so it fails closed.
 */
function effectiveAnimation(rules: RowTargetRule[], css: string): AnimationState {
  const state: AnimationState = { name: '', duration: 0, delay: 0, iteration: '1', playState: 'running', unsupported: null }
  const failWith = (prop: string, value: string, reason: string): void => {
    if (state.unsupported === null) state.unsupported = `"${prop}: ${value}" ${reason} — unsupported by the FR-075 oracle`
  }
  const evaluate = (token: string, declaration: { prop: string; value: string }): DurationMs => {
    const ms = durationMs(token, customProps)
    if (ms === 'unknown') failWith(declaration.prop, declaration.value, 'carries a time the reader cannot evaluate')
    return ms
  }
  // Pass 1 — the element's own custom-property values and the animation
  // declarations, in cascade order.
  const customProps = new Map<string, string>()
  const animated: Array<{ prop: string; value: string }> = []
  const varNames = new Set<string>()
  for (const rule of rules) {
    for (const declaration of parseDeclarations(rule.body)) {
      const { prop, value } = declaration
      if (prop.startsWith('--')) {
        customProps.set(prop, value)
      } else if (prop === 'animation' || MODELED_LONGHANDS.has(prop)) {
        animated.push(declaration)
        for (const m of value.matchAll(/var\(\s*(--[a-zA-Z0-9-]+)/g)) varNames.add(m[1])
      } else if (prop.startsWith('animation')) {
        failWith(prop, value, 'is an animation property the reader does not model')
      }
      // properties outside the animation grammar do not change the flash
    }
  }
  // A referenced custom property must be declared only by the arm's own
  // rules (or nowhere): a declaration on any other selector is unsupported.
  for (const name of varNames) {
    const pattern = new RegExp(`${name}\\s*:`, 'g')
    const everywhere = (css.match(pattern) ?? []).length
    const inRules = rules.reduce((n, rule) => n + (rule.body.match(pattern) ?? []).length, 0)
    if (everywhere > inRules) {
      failWith(name, '(custom property)', 'is declared outside the row-target rules, so its value on the marked element depends on cascade details the reader does not model')
    }
  }
  // Pass 2 — classify and evaluate the animation declarations in order.
  for (const declaration of animated) {
    if (state.unsupported !== null) break // the first unsupported declaration decides the failure
    const { prop, value } = declaration
    if (prop === 'animation') {
      // The shorthand's components may appear in ANY order (css-animations
      // grammar: each at most once, the two times in appearance order).
      // Classify each token by TYPE, not position (the grammar above).
      // Shorthand reset (CHECK R9-03/N128): a shorthand omits a component
      // resets it to its initial value (CSS Animations). The fold already
      // resets the other omitted components below — name (`?? ''`), the
      // times (`times[0] ? … : 0`), iteration (`?? '1'`) — and the omitted
      // play-state resets to the initial `running` here, before the tokens
      // are classified: a later complete shorthand turns an earlier
      // `animation-play-state: paused` back into a running animation.
      state.playState = 'running'
      let name: string | null = null
      let sawNone = false
      const times: string[] = []
      let iteration: string | null = null
      for (const token of shorthandTokens(value)) {
        const lower = token.toLowerCase()
        if (TIME_TOKEN.test(token) || /^calc\(/i.test(token)) {
          times.push(token)
        } else if (NUMBER_TOKEN.test(token) || lower === 'infinite') {
          iteration = lower // an explicit infinite is never the omitted default (R7-01); case-insensitive (R8-02)
        } else if (PLAY_STATE_KEYWORDS.has(lower)) {
          state.playState = lower // a paused flash does not flash (R8-04)
        } else if (SHORTHAND_KEYWORDS.has(lower)) {
          // its own component; none of them is tracked
        } else if (token.endsWith(')')) {
          // cubic-bezier(), steps(): a function component, never the name
        } else if (lower === 'none') {
          if (name === null && !sawNone) name = 'none'
          sawNone = true
        } else if (name === null) {
          name = token // the keyframes name, in any position; its case is kept
        } else {
          failWith(prop, value, 'carries a second token that is no known shorthand component')
        }
      }
      state.name = name ?? ''
      state.duration = times[0] ? evaluate(times[0], declaration) : 0
      state.delay = times[1] ? evaluate(times[1], declaration) : 0
      state.iteration = iteration ?? '1'
    } else if (prop === 'animation-name') {
      state.name = value
    } else if (prop === 'animation-duration') {
      state.duration = evaluate(value, declaration)
    } else if (prop === 'animation-delay') {
      state.delay = evaluate(value, declaration)
    } else if (prop === 'animation-iteration-count') {
      state.iteration = value
    } else if (prop === 'animation-play-state') {
      const v = value.trim().toLowerCase()
      if (PLAY_STATE_KEYWORDS.has(v)) state.playState = v
      else failWith(prop, value, 'carries a play-state the reader does not know')
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
 * computed styles and rendered pixels — the authorized exact-commit browser
 * lane owns those. Custom-property VALUES resolve from the imported tokens
 * file (CUSTOM_PROPERTIES, the cascade's own last-wins lookup), so the
 * shipped `calc(var(--motion-duration-slow) * 4)` evaluates to 1200ms; an
 * expression the reader still cannot evaluate fails the test with a clear
 * message instead of guessing (CHECK R7-02).
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
    await expectExactlyThisRowMarked(row, { entryRowId: 'port' })
    // "the row scrolls into view": the scroll ran on THIS row — the exact
    // receiver (stricter than the oracle's on-or-inside leg above).
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
    const normal = effectiveAnimation(allRules.filter((rule) => !rule.inReduceMedia), globalsCss)
    // FAIL CLOSED (CHECK round 8 strategy ruling): a declaration the reader
    // cannot classify or evaluate means the row is NOT PROVEN to flash
    // once — it fails here, naming the declaration, instead of guessing.
    expect(
      normal.unsupported,
      normal.unsupported
        ? `FR-075 (CHECK round 8, fail closed): the flash is NOT PROVEN to flash once — the effective cascade for the marked row carries ${normal.unsupported}`
        : 'no unsupported declaration',
    ).toBeNull()
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
      normal.duration,
      `FR-075 (CHECK R6-01, R7-02): the flash must have a decidable, NON-ZERO duration — the effective cascade resolves the duration to ${String(normal.duration)}ms`,
    ).not.toBe(0)
    expect(
      normal.playState,
      `FR-075 (CHECK R8-04): the flash must actually RUN — a paused animation never flashes, got play-state "${normal.playState}"`,
    ).toBe('running')
    expect(
      normal.iteration,
      `FR-075: the flash must run exactly once (animation iteration count 1 — the keyword infinite never flashes once), got: ${normal.iteration}`,
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
    await expectExactlyThisRowMarked(row, { entryRowId: 'port' })
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
    const reduced = effectiveAnimation(allRules, globalsCss)
    // FAIL CLOSED under reduced motion too: an unsupported declaration
    // could re-enable or otherwise change what a reduced-motion user gets.
    expect(
      reduced.unsupported,
      reduced.unsupported
        ? `FR-075 (CHECK round 8, fail closed): the reduced-motion cascade carries ${reduced.unsupported}`
        : 'no unsupported declaration',
    ).toBeNull()
    expect(
      reduced.name === '' || reduced.name === 'none',
      `RED (FR-075): under prefers-reduced-motion: reduce the row must have NO animation — the effective cascade applies "${reduced.name}"`,
    ).toBe(true)
  })

  it('a deep link with row marks the row in its own section', async () => {
    await openSettings('?tab=gateway&row=port')
    expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeInTheDocument()
    await expectExactlyThisRowMarked(await portRow(), { entryRowId: 'port' })
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
    await expectExactlyThisRowMarked(await portRow(), { entryRowId: 'port' })
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
    // "a row inside a group that is closed opens that group first." The
    // named header is the group WITNESS (FR-058), bound to the mark below.
    const groupToggle = await expectGroupOpen('limits-group-overrides', 'Model overrides')
    // And the marked row is that one row, with its aria-current identity.
    await expectExactlyThisRowMarked(
      await rowFor('model-overrides', 'RED (FR-075): the indexed Model-overrides row must render — its group opened and its identity reached the DOM'),
      { groupWitness: groupToggle, entryRowId: 'model-overrides' },
    )
  })

  it('a deep link to a row inside a closed group opens that group and marks exactly that row', async () => {
    await openSettings('?tab=limits&row=model-overrides')
    expect(await screen.findByRole('heading', { name: 'Limits', level: 2 })).toBeInTheDocument()
    const groupToggle = await expectGroupOpen('limits-group-overrides', 'Model overrides')
    await expectExactlyThisRowMarked(
      await rowFor('model-overrides', 'RED (FR-075): the indexed Model-overrides row must render — its group opened and its identity reached the DOM'),
      { groupWitness: groupToggle, entryRowId: 'model-overrides' },
    )
  })

  it('the jump does not move focus to the row — focus stays on the section', async () => {
    await openSettings()
    await jumpViaSearchResult()
    expect(await screen.findByRole('heading', { name: 'Gateway', level: 2 })).toBeInTheDocument()
    const row = await portRow()
    await expectExactlyThisRowMarked(row, { entryRowId: 'port' })
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
    // "marks exactly that row" — the whole mark shape (identity, the row
    // itself scrolled into view, its own content), scoped to this entry —
    // and bound to the setting's own spec-named control or value (CHECK
    // R8-01; every indexed row is mapped in WITNESS_PHRASES).
    await expectExactlyThisRowMarked(
      await rowFor(
        entry.row,
        `RED (FR-075): the setting row the index names for "${entry.label}" (${entry.row}) must be reachable — the row must render in its opened section`,
      ),
      { entryRowId: entry.row },
    )
  }, 20_000)
})
