/**
 * restartBanner.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 24 (banner half: FR-035, FR-038, FR-039;
 * BDD "Later leaves a banner on every section", "Show changes lists running
 * and saved values", "A failed hot apply stays in the banner after Later",
 * "The restart list cannot be loaded", "The restart clears the banner",
 * "Saving the running value again clears the banner").
 *
 * Oracles (spec, never code):
 *   - FR-035: while the list has entries, EVERY section shows the banner
 *     "N saved change(s) wait(s) for a gateway restart" with Restart now and
 *     Show changes; Show changes lists each entry's label, running value
 *     (applied_value, "unknown" when null) and saved value (persisted_value),
 *     marked "needs a restart" (reason restart_required) or "saved, not
 *     applied" (reason apply_failed); after Later/Escape focus moves to the
 *     banner's Restart now.
 *   - FR-038: the banner disappears when the list becomes empty (after a
 *     restart, after saving the running value back, or a later successful
 *     apply).
 *   - FR-039: when the list cannot be loaded, the banner area says the list
 *     of waiting changes could not be loaded, with Retry.
 *
 * The RestartBanner component does not exist yet — BLOCKED-import pattern.
 * Proposed props (GREEN may adjust names, not oracles): { entries:
 * { key, label, appliedValue, persistedValue, reason }[], error?: boolean,
 * onRetry?, onRestartNow?, autofocusRestartNow? }.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
// Type-only: the runtime module loads through loadRestartBanner's BLOCKED
// RED importActual; the erased types give the fixtures and renderBanner's
// props the real RestartBanner contracts.
import type { RestartBannerEntry, RestartBannerProps } from '@/components/settings/RestartBanner'

async function loadRestartBanner(): Promise<typeof import('@/components/settings/RestartBanner')['RestartBanner']> {
  try {
    const mod = await vi.importActual<typeof import('@/components/settings/RestartBanner')>('@/components/settings/RestartBanner')
    return mod.RestartBanner
  } catch {
    throw new Error(
      'BLOCKED RED (FR-035/FR-038/FR-039, order 24): the Settings RestartBanner component does not exist yet — ' +
        'required by FR-035 (every section shows the waiting-changes banner)',
    )
  }
}

const PORT_ENTRY: RestartBannerEntry = {
  key: 'gateway.port',
  label: 'Port',
  appliedValue: 8080,
  persistedValue: 8081,
  reason: 'restart_required',
}
const DEFENSE_ENTRY: RestartBannerEntry = {
  key: 'sandbox.prompt_injection_level',
  label: 'Defense level',
  appliedValue: null, // unknown: the value still in force is not known
  persistedValue: 'high',
  reason: 'apply_failed',
}

async function renderBanner(props: { entries: RestartBannerEntry[] } & Partial<Omit<RestartBannerProps, 'entries'>> = { entries: [] }) {
  const RestartBanner = await loadRestartBanner()
  return render(
    <RestartBanner
      entries={props.entries}
      error={props.error}
      onRetry={props.onRetry ?? vi.fn()}
      onRestartNow={props.onRestartNow ?? vi.fn()}
      autofocusRestartNow={props.autofocusRestartNow}
    />,
  )
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('FR-035 — the banner names the waiting changes', () => {
  it('one entry: "1 saved change waits for a gateway restart" with Restart now and Show changes', async () => {
    await renderBanner({ entries: [PORT_ENTRY] })
    expect(await screen.findByText(/1 saved change waits for a gateway restart/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /restart now/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /show changes/i })).toBeInTheDocument()
  })

  it('two entries: "2 saved changes wait for a gateway restart" (plural)', async () => {
    await renderBanner({ entries: [PORT_ENTRY, DEFENSE_ENTRY] })
    expect(await screen.findByText(/2 saved changes wait for a gateway restart/)).toBeInTheDocument()
  })

  it('Show changes lists label, running value, saved value and the needs-a-restart mark', async () => {
    await renderBanner({ entries: [PORT_ENTRY] })
    await userEvent.click(screen.getByRole('button', { name: /show changes/i }))
    expect(await screen.findByText('Port')).toBeInTheDocument()
    expect(screen.getByText(/running/i)).toBeInTheDocument()
    expect(screen.getByText('8080')).toBeInTheDocument()
    expect(screen.getByText(/saved/i)).toBeInTheDocument()
    expect(screen.getByText('8081')).toBeInTheDocument()
    expect(screen.getByText(/needs a restart/i)).toBeInTheDocument()
  })

  it('an entry with a null running value shows "unknown" and an apply_failed entry is marked "saved, not applied"', async () => {
    await renderBanner({ entries: [DEFENSE_ENTRY] })
    await userEvent.click(screen.getByRole('button', { name: /show changes/i }))
    expect(await screen.findByText('Defense level')).toBeInTheDocument()
    expect(screen.getByText(/unknown/i)).toBeInTheDocument()
    expect(screen.getByText('high')).toBeInTheDocument()
    expect(screen.getByText(/saved, not applied/i)).toBeInTheDocument()
    expect(screen.queryByText(/needs a restart/i)).not.toBeInTheDocument()
  })

  it('clicking Restart now requests the restart once', async () => {
    const onRestartNow = vi.fn()
    await renderBanner({ entries: [PORT_ENTRY], onRestartNow })
    await userEvent.click(screen.getByRole('button', { name: /restart now/i }))
    expect(onRestartNow).toHaveBeenCalledTimes(1)
  })

  it('after Later the banner is the focus target: its Restart now button takes focus', async () => {
    await renderBanner({ entries: [PORT_ENTRY], autofocusRestartNow: true })
    expect(await screen.findByText(/1 saved change waits/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /restart now/i })).toHaveFocus()
  })
})

describe('FR-038 — the banner disappears when the list becomes empty', () => {
  it('no entries: no banner text and no actions', async () => {
    await renderBanner({ entries: [] })
    expect(screen.queryByText(/waits? for a gateway restart/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /restart now/i })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /show changes/i })).not.toBeInTheDocument()
  })
})

describe('FR-039 — the list cannot be loaded', () => {
  it('says the list of waiting changes could not be loaded, with Retry', async () => {
    const onRetry = vi.fn()
    await renderBanner({ entries: [], error: true, onRetry })
    expect(await screen.findByText(/could not be loaded/i)).toBeInTheDocument()
    const retry = screen.getByRole('button', { name: /retry/i })
    await userEvent.click(retry)
    expect(onRetry).toHaveBeenCalledTimes(1)
  })
})
