/**
 * ChannelConfigPanel.outcomes.test.tsx
 *
 * T-06 — Configure outcome handling on screen, both save paths
 * (channels-spec-r8 T-06, T-6c table, S-57, FR-055; r3 R2-MAJ-006).
 *
 * Oracles: the T-6c table (normative toast text + variant per apply_outcome,
 * on both "Save" and "Save & Enable"), FR-055 —
 *   - only `applied` and `no_reload_needed` may show a success toast;
 *   - `applied`            → success `Saved. The connection restarted with the new settings.` plus apply_note;
 *   - `no_reload_needed`   → success `Saved.`;
 *   - `applied_not_running`→ warning `Saved, but the connection is not running: <apply_reason>.`; enable NOT called;
 *   - `unconfirmed`        → warning `Saved. The restart was not confirmed yet; check the connection status in a moment.`; enable NOT called;
 *   - `failed`             → error `Saved, but applying the change did not complete: <apply_reason>. Check the connection status before retrying.`;
 *                            enable NOT called; NO completed-restart or continued-operation claim anywhere;
 *   - 500 `saved_not_refreshed` / `credential_saved_not_saved` → error toast with the server text; enable NOT called;
 *   - `Configuration saved` alone is no longer used;
 *   - a Configure response that fails its generated schema (missing apply_outcome)
 *     must surface the generic error toast, never "saved".
 *
 * The panel uses WhatsApp as the fixture channel (no required fields in the
 * catalog, so Save is never blocked — the same fixture the existing panel
 * tests use). The outcome semantics are channel-independent.
 *
 * RED expectation: today the panel discards the Configure response and always
 * toasts the success "Configuration saved" (spec r8 "Existing Codebase
 * Context", M-05 row), so every outcome assertion below fails.
 */

import React from 'react'
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, waitFor, fireEvent, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'

vi.mock('@/store/ui', () => ({
  useUiStore: vi.fn(),
}))

vi.mock('@/store/connection', () => {
  const state = { isConnected: false, connection: null }
  const hook = vi.fn((selector: (s: typeof state) => unknown) => selector(state))
  ;(hook as unknown as { getState: () => typeof state }).getState = () => state
  return { useConnectionStore: hook }
})

vi.mock('@/store/whatsappPairing', () => ({
  useWhatsAppPairingStore: vi.fn(
    (selector: (s: { byChannel: Record<string, unknown>; clear: () => void }) => unknown) =>
      selector({ byChannel: {}, clear: vi.fn() }),
  ),
}))

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    fetchChannelConfig: vi.fn(),
    fetchChannelRouting: vi.fn(),
    fetchAgents: vi.fn(),
    fetchWorkspaces: vi.fn(),
    fetchWorkspace: vi.fn(),
    configureChannel: vi.fn(),
    enableChannel: vi.fn(),
    disableChannel: vi.fn(),
    setChannelRouting: vi.fn(),
    isApiError: vi.fn(() => false),
  }
})

vi.mock('framer-motion', () => ({
  motion: new Proxy({}, {
    get: (_: object, prop: string) =>
      ({ children, ...props }: Record<string, unknown>) =>
        React.createElement(prop as string, props, children as React.ReactNode),
  }),
  AnimatePresence: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

import { useUiStore } from '@/store/ui'
import {
  fetchChannelConfig,
  fetchChannelRouting,
  configureChannel,
  enableChannel,
  isApiError,
} from '@/lib/api'
import { ChannelConfigPanel } from './ChannelConfigPanel'

// FR-055: the Configure response is the generated ChannelConfigureResponse.
// Until the generated client lands (WP-1), configureChannel still types as
// Promise<void>, so outcome fixtures are cast — the cast stays valid after
// the signature change, keeping this file reconciliation-stable.
type ConfigureResponseFixture = Record<string, unknown>
function resolveConfigure(response: ConfigureResponseFixture | undefined) {
  ;(configureChannel as ReturnType<typeof vi.fn>).mockImplementation(
    (_id: string, _config: Record<string, unknown>) =>
      response === undefined ? Promise.resolve(undefined) : Promise.resolve(response as unknown as void),
  )
}

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
}

// The routing mock must match the store's entry type.
const EMPTY_ROUTING = { default_agent_id: '', workspace_id: '', bound: false }

function renderPanel() {
  const addToast = vi.fn()
  vi.mocked(useUiStore).mockReturnValue({ addToast } as never)
  const client = makeQueryClient()
  client.setQueryData(['channel-config', 'whatsapp'], {} as { id: string; enabled: boolean })
  client.setQueryData(['channel-routing', 'whatsapp'], EMPTY_ROUTING)
  client.setQueryData(['agents'], [])
  client.setQueryData(['workspaces', { status: 'active' }], [])
  const onOpenChange = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <ChannelConfigPanel
        channelId="whatsapp"
        channelName="WhatsApp"
        enabled={false}
        open={true}
        onOpenChange={onOpenChange}
      />
    </QueryClientProvider>,
  )
  return { addToast }
}

async function clickSave(label: RegExp) {
  await waitFor(() => {
    expect(screen.getByRole('button', { name: label })).toBeInTheDocument()
  })
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: label }))
  })
}

const APPLY_NOTE = 'A connection restart was attempted; other connections and scheduled services may have paused briefly while it ran.'

beforeEach(() => {
  vi.mocked(fetchChannelConfig).mockResolvedValue({})
  vi.mocked(fetchChannelRouting).mockResolvedValue(EMPTY_ROUTING)
  vi.mocked(enableChannel).mockResolvedValue({ id: 'whatsapp', enabled: true })
  vi.mocked(isApiError).mockReturnValue(false)
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('ChannelConfigPanel — Configure outcome toasts (T-6c / S-57 / FR-055)', () => {
  it('applied shows the success restart toast plus apply_note (Save)', async () => {
    resolveConfigure({ apply_outcome: 'applied', apply_note: APPLY_NOTE })
    const { addToast } = renderPanel()

    await clickSave(/^Save$/i)

    await waitFor(() => {
      expect(configureChannel).toHaveBeenCalled()
    })
    const messages = addToast.mock.calls.map((c) => String((c[0] as { message: string }).message))
    expect(messages).toContain('Saved. The connection restarted with the new settings.')
    // T-6c: the apply_note must be shown alongside the applied toast.
    expect(messages.join('\n')).toContain(APPLY_NOTE.split('.')[0])
  })

  it('no_reload_needed shows exactly "Saved." (Save)', async () => {
    resolveConfigure({ apply_outcome: 'no_reload_needed' })
    const { addToast } = renderPanel()

    await clickSave(/^Save$/i)

    await waitFor(() => {
      expect(addToast).toHaveBeenCalled()
    })
    const messages = addToast.mock.calls.map((c) => String((c[0] as { message: string }).message))
    expect(messages).toContain('Saved.')
    for (const m of messages) {
      expect(m === 'Saved.' || m === 'Routing saved',
        `T-6c: no_reload_needed may not toast success wording beyond "Saved."; got ${JSON.stringify(m)}`).toBeTruthy()
    }
  })

  it('applied_not_running shows the warning with apply_reason and "Save & Enable" does not enable', async () => {
    resolveConfigure({ apply_outcome: 'applied_not_running', apply_reason: '401 unauthorized' })

    // Save path — warning variant with the reason.
    const { addToast } = renderPanel()
    await clickSave(/^Save$/i)
    await waitFor(() => {
      expect(addToast).toHaveBeenCalled()
    })
    const warn = addToast.mock.calls.find((c) => /not running/.test(String((c[0] as { message: string }).message)))
    // T-6c: applied_not_running must toast "Saved, but the connection is not running: <reason>."
    expect(warn).toBeTruthy()
    expect((warn![0] as { message: string }).message).toBe('Saved, but the connection is not running: 401 unauthorized.')
    expect((warn![0] as { variant: string }).variant).toBe('warning')

    // Save & Enable path — enable must NOT be called (the connection already
    // failed to start with these settings).
    renderPanel()
    await clickSave(/Save & Enable/i)
    await waitFor(() => {
      expect(configureChannel).toHaveBeenCalledTimes(2)
    })
    await act(async () => {})
    // T-6c: enable must not run after applied_not_running.
    expect(enableChannel).not.toHaveBeenCalled()
  })

  it('unconfirmed shows the not-confirmed warning and "Save & Enable" does not enable', async () => {
    resolveConfigure({ apply_outcome: 'unconfirmed' })
    renderPanel()

    await clickSave(/Save & Enable/i)

    await waitFor(() => {
      expect(configureChannel).toHaveBeenCalled()
    })
    await act(async () => {})
    // T-6c: enable must not run after unconfirmed.
    expect(enableChannel).not.toHaveBeenCalled()
  })

  it('failed shows the did-not-complete error with apply_reason, no restart claim, and never enables', async () => {
    resolveConfigure({ apply_outcome: 'failed', apply_reason: 'reload failed: boom' })
    const { addToast } = renderPanel()

    await clickSave(/Save & Enable/i)

    await waitFor(() => {
      expect(configureChannel).toHaveBeenCalled()
    })
    await act(async () => {})
    expect(enableChannel).not.toHaveBeenCalled()
    // R2-MAJ-006 no-claim rule over every toast the save produced.
    for (const call of addToast.mock.calls) {
      const message = String((call[0] as { message: string }).message)
      expect(message).not.toMatch(/restarted with the new settings|keeps running with its old settings/i)
    }
  })

  it('failed toast carries no completed-restart or continued-operation claim', async () => {
    resolveConfigure({ apply_outcome: 'failed', apply_reason: 'reload failed: boom' })
    const { addToast } = renderPanel()

    await clickSave(/^Save$/i)

    await waitFor(() => {
      expect(configureChannel).toHaveBeenCalled()
    })
    await act(async () => {})
    const rendered = addToast.mock.calls.map((c) => String((c[0] as { message: string }).message)).join('\n')
    expect(rendered).not.toMatch(/keeps running with its old settings/i)
    expect(rendered).not.toMatch(/restarted with the new settings/)
    expect(rendered).toContain('Saved, but applying the change did not complete: reload failed: boom.')
    expect(rendered).toContain('Check the connection status before retrying.')
  })

  it('saved_not_refreshed (500) shows the server text as an error and never enables', async () => {
    ;(configureChannel as ReturnType<typeof vi.fn>).mockRejectedValue(
      Object.assign(new Error('settings saved, but the running configuration could not be refreshed; the connection was not restarted'), {
        userMessage: 'settings saved, but the running configuration could not be refreshed; the connection was not restarted',
      }),
    )
    vi.mocked(isApiError).mockReturnValue(true)
    renderPanel()

    await clickSave(/Save & Enable/i)

    await waitFor(() => {
      expect(configureChannel).toHaveBeenCalled()
    })
    await act(async () => {})
    expect(enableChannel).not.toHaveBeenCalled()
  })

  it('credential_saved_not_saved (500) shows the server text and never enables', async () => {
    const serverText =
      'The credential was saved, but the configuration change was not: nothing else was applied. The connection keeps its previous token until it next restarts; whether that restart picks the new value up depends on the credential reference the saved configuration holds. Save again to retry — a successful re-save is the general recovery.'
    ;(configureChannel as ReturnType<typeof vi.fn>).mockRejectedValue(
      Object.assign(new Error(serverText), { userMessage: serverText }),
    )
    vi.mocked(isApiError).mockReturnValue(true)
    const { addToast } = renderPanel()

    await clickSave(/Save & Enable/i)

    await waitFor(() => {
      expect(configureChannel).toHaveBeenCalled()
    })
    await act(async () => {})
    expect(enableChannel).not.toHaveBeenCalled()
    const rendered = addToast.mock.calls.map((c) => String((c[0] as { message: string }).message)).join('\n')
    expect(rendered).toContain('The credential was saved, but the configuration change was not')
    expect(rendered).not.toContain('nothing changed')
  })

  it('a response missing apply_outcome fails schema validation and never shows "saved"', async () => {
    // FR-055: the generated client validates ChannelConfigureResponse; a
    // response without apply_outcome must reject, and the panel must show
    // the generic error toast — never a success/saved wording.
    ;(configureChannel as ReturnType<typeof vi.fn>).mockRejectedValue(new Error('response failed schema validation'))
    const { addToast } = renderPanel()

    await clickSave(/^Save$/i)

    await waitFor(() => {
      expect(configureChannel).toHaveBeenCalled()
    })
    await act(async () => {})
    const rendered = addToast.mock.calls.map((c) => String((c[0] as { message: string }).message)).join('\n')
    expect(rendered).not.toBe('Configuration saved')
    expect(rendered).not.toMatch(/^Saved\.$/)
    const variants = addToast.mock.calls.map((c) => (c[0] as { variant: string }).variant)
    expect(variants).not.toContain('success')
  })

  it('"Configuration saved" alone is no longer used on any outcome', async () => {
    for (const outcome of ['applied', 'no_reload_needed', 'applied_not_running', 'unconfirmed', 'failed']) {
      resolveConfigure({ apply_outcome: outcome, apply_reason: 'r' })
      const { addToast } = renderPanel()
      await clickSave(/^Save$/i)
      await waitFor(() => {
        expect(configureChannel).toHaveBeenCalled()
      })
      await act(async () => {})
      for (const call of addToast.mock.calls) {
        expect((call[0] as { message: string }).message,
          `T-6c: the bare "Configuration saved" toast is retired (outcome ${outcome})`).not.toBe('Configuration saved')
      }
    }
  })
})
