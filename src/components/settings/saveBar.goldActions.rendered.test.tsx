/**
 * saveBar.goldActions.rendered.test.tsx — Settings redesign #1519 (CHECK R3-01).
 *
 * FR-017 rendered on the REAL Settings sections — this file REPLACES the
 * synthetic Probe proof in saveBar.counting.test.tsx (same author; the
 * replaced assertions are cited per the spec that replaces them).
 *
 * Spec oracle (never the code): FR-017 — "While the Save bar shows, Save
 * changes MUST be the only gold (primary) button on the screen; other primary
 * buttons MUST render outlined." And its BDD scenario "Only one gold button
 * while the Save bar shows" (spec :909): Given Providers with no changes shows
 * Connect a provider as the gold button; When the person changes the default
 * model; Then Save changes is the only gold button and Connect a provider is
 * outlined. The inverse arm is FR-017's "while the Save bar shows" scoping:
 * with the bar hidden the other primary actions are the gold default again.
 *
 * Unit boundary: the REAL section composition the shell renders for
 * `tab=providers` — the real `SettingsSectionBody` (ProvidersSection +
 * IntegrationsSection inside the real SectionSaveScope). The ONLY harness
 * piece is the shell context around the body: it holds the REAL
 * `saveBarVisible` state with the exact wiring `SettingsScreen.tsx` uses
 * (a section draft → useShellDrafts → setSaveBarVisible → the shell value the
 * sections' own primary actions read), so the demotion path under test is the
 * screen's own, not a stub's. Reads are mocked at `@/lib/api`; nothing is
 * saved, so no write seam is needed.
 *
 * Why a rendered proof (CHECK R3-01): the old Probe rendered
 * `usePrimaryVariant()` inside its own well-behaved consumer, so it stayed
 * green while a REAL primary action kept its gold fill (check mutant N31 —
 * the real voice "Save key" hardcoded to default — survived). Here the
 * asserted buttons are the sections' real actions: the bar's Save changes,
 * ProvidersSection's "Connect a provider" (data-testid connect-provider-btn),
 * IntegrationsSection's voice-key sheet "Save key" (save-<voice>) and the
 * search-key panel's "Save key" (save-<search>).
 *
 * RED expected on landing until frontend round 13 wires the real actions
 * (connect-provider-btn and the search panel's Save key carry no variant and
 * stay gold with the bar visible); the voice sheet's Save key already consumes
 * the hook and its assertions pass here. The failing runs are the production
 * findings, not harness errors.
 */

import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import * as React from 'react'
import { useCallback, useMemo, useState } from 'react'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { SettingsSectionBody } from './SettingsSectionBody'
import { SettingsShellContext, type SettingsShellApi } from './SettingsShellContext'

// cmdk (inside the ModelSelector's Command) needs the jsdom stubs the other
// ProvidersSection harnesses use (formSavedStateCheck.test.tsx): ResizeObserver
// and scrollIntoView; the Radix Popover content renders inline so the
// default-model options are reachable.
beforeAll(() => {
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class ResizeObserver {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  }
  if (!Element.prototype.scrollIntoView) {
    Element.prototype.scrollIntoView = () => {}
  }
})

vi.mock('@/components/ui/popover', () => ({
  Popover: ({ children }: { children: React.ReactNode }) => React.createElement(React.Fragment, null, children),
  PopoverTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild && React.isValidElement(children) ? children : React.createElement('div', null, children),
  PopoverContent: ({ children }: { children: React.ReactNode }) => React.createElement('div', null, children),
  PopoverAnchor: ({ children }: { children: React.ReactNode }) => React.createElement('div', null, children),
}))

const addToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(() => ({ addToast })),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAppState: vi.fn(async () => ({ onboarding_complete: true })),
    fetchProviders: vi.fn(async () => [ANTHROPIC_CONNECTED]),
    fetchProvidersCatalog: vi.fn(async () => ANTHROPIC_CATALOG),
    getDefaultModel: vi.fn(async () => DEFAULT_MODEL_BEFORE),
    putDefaultModel: vi.fn(),
    configureProvider: vi.fn(),
    testProvider: vi.fn(),
    deleteProvider: vi.fn(),
    signOutProvider: vi.fn(),
    checkEntitlement: vi.fn(async () => undefined),
    fetchSignInStatus: vi.fn(async () => undefined),
    fetchIntegrationProviders: vi.fn(async () => INTEGRATIONS_BEFORE),
    configureIntegrationProvider: vi.fn(),
  }
})

import * as api from '@/lib/api'

// ── Fixtures (the reads the real sections start from) ────────────────────────

const ANTHROPIC_CONNECTED = {
  status: 'connected',
  auth_method: 'api_key',
  dependents: [],
  backs_default: false,
  id: 'anthropic',
  name: 'anthropic',
  display_name: 'Anthropic',
  has_models_endpoint: true,
  models: ['claude-sonnet-4-5'],
}

const DEFAULT_MODEL_BEFORE = { provider: 'openrouter', model: 'z-ai/glm-5.2', context_window: 1048576, window_source: 'catalog' }

// Minimal catalog document: the one provider/model pair the draft picks
// (ProvidersCatalog's required header fields included).
const ANTHROPIC_CATALOG = {
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
}

// One configured search provider (keeps the header's "Connect a provider" and
// gives the search row its Add-key panel), and two configured voice services
// (the Active-transcriber select needs a configured service; the deepgram row
// offers the "Edit key" opener of the voice key sheet).
const INTEGRATIONS_BEFORE = {
  search: [
    { id: 'brave', kind: 'search', display_name: 'Brave', configured: false, requires_key: true, usable: true, active: false },
  ],
  voice: [
    { id: 'elevenlabs', kind: 'voice', display_name: 'ElevenLabs', configured: true, requires_key: true, usable: true, active: true },
    { id: 'deepgram', kind: 'voice', display_name: 'Deepgram', configured: true, requires_key: true, usable: true, active: false },
  ],
  active_search: null,
  active_voice: 'elevenlabs',
  default_search: null,
  fallback_search: null,
}

// ── The shell boundary: the real saveBarVisible state wiring ─────────────────

/**
 * The shell context exactly as `SettingsScreen.tsx` holds it: `saveBarVisible`
 * is the OR over the sections' reported bars, updated through the same
 * `setSaveBarVisible(owner, visible)` callback the Save bar's useShellDrafts
 * calls when a draft appears. The sections under test read THIS value — the
 * demotion path is the screen's own end-to-end, up to the shell boundary.
 */
function ShellHarness({ children }: { children: React.ReactNode }) {
  const [barsByOwner, setBarsByOwnerState] = useState<Record<string, boolean>>({})
  const saveBarVisible = Object.values(barsByOwner).some(Boolean)
  const setSaveBarVisible = useCallback((owner: string, visible: boolean) => {
    setBarsByOwnerState((prev) => {
      if (!!prev[owner] === visible) return prev
      const next = { ...prev }
      if (visible) next[owner] = true
      else delete next[owner]
      return next
    })
  }, [])
  const shell = useMemo<SettingsShellApi>(
    () => ({
      announce: () => {},
      setDraftCount: () => {},
      offerRestart: () => {},
      refreshPendingRestart: () => {},
      requestPassword: undefined,
      saveBarVisible,
      setSaveBarVisible,
      standalone: false,
    }),
    [saveBarVisible, setSaveBarVisible],
  )
  return <SettingsShellContext.Provider value={shell}>{children}</SettingsShellContext.Provider>
}

function renderProvidersBody() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={client}>
      <ShellHarness>
        <SettingsSectionBody section="providers" />
      </ShellHarness>
    </QueryClientProvider>,
  )
}

/** Change the default model on the real card (the BDD scenario's "When"). */
async function changeDefaultModel() {
  await screen.findByTestId('default-model-provider')
  await userEvent.click(screen.getByTestId('default-model-change-btn'))
  await waitFor(() => expect(screen.getByTestId('default-model-select')).toBeInTheDocument())
  await userEvent.click(screen.getByTestId('default-model-option-claude-sonnet-4-5'))
  expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
}

// FR-017 is a claim about the gold FILL on the rendered screen, so the count
// walks the real DOM for buttons carrying the Button kit's gold variant class
// (src/components/ui/button.tsx variant "default") — not the a11y tree, which
// hides background controls while a Sheet is open.
const GOLD = 'bg-[var(--color-accent)]'
const OUTLINE = ['border', 'border-[var(--color-border)]', 'bg-transparent']

function goldButtons(): HTMLButtonElement[] {
  return Array.from(document.querySelectorAll('button')).filter((b) => b.className.includes(GOLD))
}

/** Distinguishable failure: the names of every gold button on the screen. */
function goldNames(): string[] {
  return goldButtons().map((b) => (b.textContent ?? '').trim())
}

function expectOutlined(button: HTMLElement, name: string) {
  expect(
    button,
    `RED (FR-017): with the Save bar visible, ${name} must render outlined — a second gold action must drop to outline`,
  ).toHaveClass(...OUTLINE)
  expect(button, `RED (FR-017): the outlined ${name} must not carry the gold fill`).not.toHaveClass(GOLD)
}

function expectGold(button: HTMLElement, name: string) {
  expect(button, `FR-017: with the bar hidden, ${name} returns to the gold default`).toHaveClass(GOLD)
  expect(button, `FR-017: the gold ${name} is not the outline variant`).not.toHaveClass('bg-transparent')
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('FR-017 — with the Save bar visible, Save changes is the only gold button on the screen', () => {
  it('BDD "Only one gold button…": no change → Connect a provider is gold; after changing the default model, Save changes is the only gold button and Connect a provider is outlined', async () => {
    renderProvidersBody()
    // Given: Providers with no changes — "Connect a provider" is the gold
    // button (the bar is hidden; FR-017 scopes the demotion to the bar).
    const connect = await screen.findByTestId('connect-provider-btn')
    expectGold(connect, 'Connect a provider')
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()

    // When: the person changes the default model — the real Save bar shows.
    await changeDefaultModel()

    // Then: Save changes is the ONLY gold button on the screen, and Connect a
    // provider is outlined.
    const gold = goldButtons()
    expect(
      goldNames(),
      'FR-017: exactly one gold action must remain while the bar shows',
    ).toEqual(['Save changes'])
    expect(gold[0], 'FR-017: the one gold action is the bar’s Save changes').toMatch(/save changes/i)
    expectOutlined(screen.getByTestId('connect-provider-btn'), 'Connect a provider')
  })

  it('the section’s real primary actions are outlined too: the row’s Edit, the voice key sheet’s and the search key panel’s Save keys — a real action staying gold (N31) fails here', async () => {
    renderProvidersBody()
    await changeDefaultModel()

    // The voice key sheet's real "Save key" first (the N31 target — the hook
    // consumer whose real wiring a synthetic probe cannot see), then the
    // search key panel's, then the row's own action.
    await userEvent.click(screen.getByTestId('addkey-deepgram'))
    const sheet = await screen.findByTestId('voice-key-sheet')
    expectOutlined(await within(sheet).getByTestId('save-deepgram'), 'the voice sheet’s Save key')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByTestId('voice-key-sheet')).not.toBeInTheDocument())
    await userEvent.click(screen.getByTestId('addkey-brave'))
    expectOutlined(await screen.findByTestId('save-brave'), 'the search panel’s Save key')
    // The provider row's own primary action (Edit on a connected row,
    // Configure on an unconfigured one) is demoted with the bar showing.
    expectOutlined(screen.getByTestId('configure-btn-anthropic'), 'the provider row’s Edit')

    // And the screen total: Save changes alone keeps the gold fill.
    expect(goldNames(), 'FR-017: exactly one gold action must remain while the bar shows').toEqual(['Save changes'])
  })

  it('the inverse: with the bar hidden the other primary actions are the gold default again', async () => {
    renderProvidersBody()
    await screen.findByTestId('connect-provider-btn')
    // Both panels open, no draft anywhere — the bar is hidden (FR-011: zero
    // changes → the bar is hidden).
    expectGold(screen.getByTestId('configure-btn-anthropic'), 'the provider row’s Edit')
    await userEvent.click(screen.getByTestId('addkey-deepgram'))
    const sheet = await screen.findByTestId('voice-key-sheet')
    expectGold(await within(sheet).getByTestId('save-deepgram'), 'the voice sheet’s Save key')
    await userEvent.click(within(sheet).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByTestId('voice-key-sheet')).not.toBeInTheDocument())
    await userEvent.click(screen.getByTestId('addkey-brave'))
    expectGold(await screen.findByTestId('save-brave'), 'the search panel’s Save key')
    expectGold(screen.getByTestId('connect-provider-btn'), 'Connect a provider')
    // The premise: no bar, so no demotion scope.
    expect(screen.queryByRole('button', { name: /save changes/i })).not.toBeInTheDocument()
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    void api
  })
})
