// SettingsScreen.test.tsx — about-info fetch-failure message (Wave 1 frontend-findings-fix).
//
// SettingsScreen (src/components/screens/SettingsScreen.tsx) fetches
// `['about']` via useQuery and derives `devicePairingEnabled` from it via
// `isDevicePairingEnabled`. A fetch failure must not silently look identical
// to "device pairing is disabled" (both collapse `aboutInfo` to a falsy-ish
// state) — the component renders an inline error message
// (data-testid="settings-about-fetch-error") whenever `isError` is true.
//
// Traces to: src/components/screens/SettingsScreen.tsx L17-24 (aboutInfoError
// derivation), L43-51 (rendered message). No pre-existing BDD scenario in a
// wave spec for this component — inferred from the reviewer findings
// (pr-test-analyzer, code-simplifier, code-reviewer) that flagged the missing
// coverage during the Wave 1 7-reviewer gate; every sibling fix in the same
// wave (GodModeControl, PerformanceSection, chat.ts, session.ts) shipped a
// paired regression test.
// CLARIFY: no BDD Given/When/Then exists for this message in any wave spec —
// tests below are written directly against the implemented behavior.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { AboutInfo } from '@/lib/api'

// SettingsScreen mounts real settings/*Section components under its default
// "providers" tab (Radix Tabs only renders the active TabsContent). Those
// sections carry their own useQuery/useMutation network calls unrelated to
// the about-info fetch-error message under test here. Stub every section out
// as a black box — keeps this test scoped to SettingsScreen's own isError
// branch and its Tabs/ScreenHeader shell, consistent with how
// src/test/screens.test.tsx scopes heavy screen trees in this repo.
vi.mock('@/components/settings/ProvidersSection', () => ({ ProvidersSection: () => null }))
vi.mock('@/components/settings/IntegrationsSection', () => ({ IntegrationsSection: () => null }))
vi.mock('@/components/settings/SecuritySection', () => ({ SecuritySection: () => null }))
vi.mock('@/components/settings/GatewaySection', () => ({ GatewaySection: () => null }))
vi.mock('@/components/settings/DataSection', () => ({ DataSection: () => null }))
vi.mock('@/components/settings/AboutSection', () => ({ AboutSection: () => null }))
vi.mock('@/components/settings/DevicesSection', () => ({ DevicesSection: () => null }))
vi.mock('@/components/settings/PerformanceSection', () => ({ PerformanceSection: () => null }))
vi.mock('@/components/settings/ChatSection', () => ({ ChatSection: () => null }))
vi.mock('@/components/settings/MemorySection', () => ({ MemorySection: () => null }))
// T068-29 (ADR-066 D9): the Models tab hosts ContextSection; stub it so the
// tab/prefill plumbing is tested without its network calls.
vi.mock('@/components/settings/ContextSection', () => ({
  ContextSection: (props: { prefillOverride?: { provider: string; model: string } }) => (
    <div data-testid="context-section-stub">
      {props.prefillOverride ? `${props.prefillOverride.provider}/${props.prefillOverride.model}` : 'no-prefill'}
    </div>
  ),
}))

// Mock only the fetch/derivation SettingsScreen touches directly.
vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchAboutInfo: vi.fn(),
  }
})

import * as api from '@/lib/api'
import { SettingsScreen } from './SettingsScreen'

const ABOUT_INFO_OK: AboutInfo = {
  version: '0.1.0',
  go_version: 'go1.24',
  os: 'linux',
  arch: 'amd64',
  uptime_seconds: 100,
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false } } })
}

function renderScreen(props: React.ComponentProps<typeof SettingsScreen> = {}) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <SettingsScreen {...props} />
    </QueryClientProvider>,
  )
}

describe('SettingsScreen — about-info fetch-error message', () => {
  it('renders normally without the fetch-error message when the about fetch succeeds', async () => {
    vi.mocked(api.fetchAboutInfo).mockResolvedValue(ABOUT_INFO_OK)

    renderScreen()

    await waitFor(() => {
      expect(api.fetchAboutInfo).toHaveBeenCalled()
    })
    await waitFor(() => {
      expect(screen.queryByTestId('settings-about-fetch-error')).not.toBeInTheDocument()
    })
    // The screen itself still renders its real heading/tabs regardless of the
    // about-info outcome — proves this isn't a blank/crashed render.
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Settings')
  })

  it('renders the settings-about-fetch-error message when the about fetch fails (isError)', async () => {
    vi.mocked(api.fetchAboutInfo).mockRejectedValue(new Error('network error'))

    renderScreen()

    await waitFor(() => {
      expect(screen.getByTestId('settings-about-fetch-error')).toBeInTheDocument()
    })
    const message = screen.getByTestId('settings-about-fetch-error')
    expect(message).toHaveTextContent(/could not fetch gateway build info/i)
    expect(message).toHaveTextContent(/gateway may be offline/i)
    // devicePairingEnabled derives false on fetch failure (same as "disabled"),
    // so the Devices tab must stay hidden — the fetch-error message is the one
    // and only signal that the state is unknown rather than confirmed off.
    expect(screen.queryByRole('tab', { name: /devices/i })).not.toBeInTheDocument()
  })

  it('does not show the fetch-error message while the about fetch is still loading', () => {
    // Never-resolving promise — the query stays in the loading state for the
    // lifetime of this test; isError must remain false throughout.
    vi.mocked(api.fetchAboutInfo).mockReturnValue(new Promise<AboutInfo>(() => {}))

    renderScreen()

    expect(screen.queryByTestId('settings-about-fetch-error')).not.toBeInTheDocument()
  })
})

// T068-29 — ADR-066 FR-037: Settings → Models tab; `?tab=models&provider=&model=`
// (the ADR-068 X-08 link target) opens the tab with a pre-filled override row.
describe('SettingsScreen — Models tab (ADR-066 D9)', () => {
  it('exposes a Models tab that hosts ContextSection', async () => {
    vi.mocked(api.fetchAboutInfo).mockResolvedValue(ABOUT_INFO_OK)
    renderScreen({ initialTab: 'models' })
    expect(screen.getByRole('tab', { name: /^models$/i })).toBeInTheDocument()
    await waitFor(() => expect(screen.getByTestId('context-section-stub')).toHaveTextContent('no-prefill'))
  })

  it('passes the provider/model pre-fill through to ContextSection', async () => {
    vi.mocked(api.fetchAboutInfo).mockResolvedValue(ABOUT_INFO_OK)
    renderScreen({ initialTab: 'models', prefillOverride: { provider: 'ollama', model: 'qwen3:8b' } })
    await waitFor(() =>
      expect(screen.getByTestId('context-section-stub')).toHaveTextContent('ollama/qwen3:8b'),
    )
  })
})
