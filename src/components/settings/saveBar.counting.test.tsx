/**
 * saveBar.counting.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 19 (FR-011, FR-013 + D-10, FR-018; BDD
 * "A changed value shows the Save bar and stores nothing", "A value changed
 * back by hand is no longer counted", "An invalid value blocks saving and
 * names the range" (D-10 rows 1, 2, 13), "A value inside its range can be
 * saved" (D-10 row 4), "The saved signal stays long enough to read").
 *
 * Oracles (spec, never code):
 *   - FR-011: the bar shows "N unsaved change(s)", "N need(s) a gateway
 *     restart", "N need(s) your password" for the CHANGED values; a value
 *     typed back to its saved value stops counting; no changes → hidden.
 *   - FR-013 + D-10: invalid value → the field's exact range text, the bar
 *     says "Fix 1 value before saving", Save changes carries
 *     aria-disabled="true" and stays focusable; pressing it moves focus to
 *     the field and sends nothing.
 *   - FR-018: after a Save with no change left, "Saved at HH:MM" (24 h) for
 *     8 seconds, then hidden; a new edit replaces it at once; the live
 *     region announces "Changes saved." once.
 *
 * Same BLOCKED-import pattern as saveBar.outcomes.test.tsx: the SaveBar does
 * not exist yet, so each test fails loudly with a BLOCKED message naming its
 * FR. GREEN may adjust prop names (same author); the oracles are frozen.
 * Proposed field contract: { key, label, value, kind, options?, min?, max?,
 * rangeText?, restart?, password? } — text inputs validated by the screen
 * against min/max with rangeText (D-10's messages live in the sections).
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, act } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

async function loadSaveBar(): Promise<any> {
  try {
    const mod: any = await vi.importActual('@/components/settings/SaveBar')
    return mod.SaveBar ?? mod.default
  } catch {
    throw new Error(
      'BLOCKED RED (FR-011/FR-013/FR-018, order 19): the Settings SaveBar component does not exist yet — ' +
        'required by FR-011 (the bar counts unsaved changes) and FR-013 (D-10 validation)',
    )
  }
}

// D-10 rows 1/2/13: Gateway → Port. Range and message are the spec's; the
// field is a text input because D-10 row 13 types "80a" (a number input
// would filter it before the screen's own validation could see it).
const PORT_FIELD = {
  key: 'gateway.port',
  label: 'Port',
  value: '8080',
  kind: 'text',
  min: 1024,
  max: 65535,
  rangeText: 'Enter a whole number from 1,024 to 65,535.',
  restart: true,
}
// Plain field (no restart, no password): Gateway → Log level.
const LOG_FIELD = {
  key: 'gateway.log_level',
  label: 'Log level',
  value: 'info',
  kind: 'select',
  options: ['info', 'warning', 'debug'],
}
// Password-marked (FR-011 counts "needs your password"); FR-025/FR-037 badge rows.
const SHELL_LIMIT_FIELD = {
  key: 'sandbox.workspace_path_guard',
  label: 'Shell workspace limit',
  value: false,
  kind: 'switch',
  password: true,
  restart: true,
}

async function renderSaveBar(fields: any[], props: Record<string, unknown> = {}) {
  const SaveBar = await loadSaveBar()
  // FR-018's "Changes saved." goes to the shell's one page-level live region
  // (FR-071). A Save bar rendered alone has no shell — its announce is a
  // noop — so the harness supplies the shell boundary with a recorder: the
  // same contract the real shell implements (SettingsShellContext.tsx). The
  // announcement is asserted at that boundary; the live-region DOM itself is
  // the shell's, covered by the shell tests.
  const { SettingsShellContext } = await vi.importActual<any>('@/components/settings/SettingsShellContext')
  const announceCalls: string[] = []
  const view = render(
    <SettingsShellContext.Provider
      value={{
        announce: (text: string) => announceCalls.push(text),
        setDraftCount: () => {},
        offerRestart: () => {},
        refreshPendingRestart: () => {},
        requestPassword: undefined,
        saveBarVisible: false,
        setSaveBarVisible: () => {},
        standalone: false,
      }}
    >
      <SaveBar
        section="gateway"
        fields={fields}
        onSave={props.onSave ?? vi.fn(async () => ({ ok: true, status: 200, json: async () => ({}) }))}
        onDiscard={props.onDiscard ?? vi.fn()}
      />
    </SettingsShellContext.Provider>,
  )
  return { ...view, announceCalls }
}

function changeByLabel(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } })
}

beforeEach(() => {
  vi.clearAllMocks()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('FR-011 — the bar counts unsaved changes', () => {
  it('one changed value: "1 unsaved change" + "1 needs a gateway restart", with Discard and Save changes, storing nothing', async () => {
    const onSave = vi.fn(async () => ({ ok: true, status: 200, json: async () => ({}) }))
    await renderSaveBar([PORT_FIELD], { onSave })
    changeByLabel('Port', '8081') // a valid change counts (BDD: Port 8080 → 8081)
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.getByText(/1 needs a gateway restart/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /discard/i })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /save changes/i })).toBeInTheDocument()
    expect(onSave).not.toHaveBeenCalled()
  })

  it('two changed values say "2 unsaved changes" (plural)', async () => {
    await renderSaveBar([PORT_FIELD, LOG_FIELD])
    changeByLabel('Port', '8081')
    fireEvent.change(screen.getByLabelText('Log level'), { target: { value: 'debug' } })
    expect(await screen.findByText(/2 unsaved changes/)).toBeInTheDocument()
  })

  it('a password-marked change is counted as needing the password (FR-011)', async () => {
    await renderSaveBar([SHELL_LIMIT_FIELD])
    fireEvent.click(screen.getByRole('switch', { name: 'Shell workspace limit' }))
    expect(await screen.findByText(/1 needs your password/)).toBeInTheDocument()
  })

  it('a value typed back to its saved value stops counting — the bar hides', async () => {
    await renderSaveBar([PORT_FIELD])
    changeByLabel('Port', '8081')
    expect(await screen.findByText(/1 unsaved change/)).toBeInTheDocument()
    changeByLabel('Port', '8080')
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    // Zero changes: the bar is hidden entirely (FR-011).
    expect(screen.queryByRole('button', { name: /save changes/i })).not.toBeInTheDocument()
  })
})

describe('FR-013 + D-10 — an invalid value blocks saving and names the range', () => {
  it.each([
    ['80', 'D-10 row 1 (min−1: below 1024)'],
    ['65536', 'D-10 row 2 (max+1)'],
    ['80a', 'D-10 row 13 (not a number)'],
  ])('Port %s is rejected with the exact range text; Save aria-disabled but focusable', async (input) => {
    await renderSaveBar([PORT_FIELD])
    changeByLabel('Port', input)
    expect(await screen.findByText('Enter a whole number from 1,024 to 65,535.')).toBeInTheDocument()
    expect(screen.getByText(/Fix 1 value before saving/)).toBeInTheDocument()
    const save = screen.getByRole('button', { name: /save changes/i })
    expect(save).toHaveAttribute('aria-disabled', 'true')
    expect(save).not.toBeDisabled() // stays focusable
    await userEvent.click(save)
    expect(screen.getByLabelText('Port')).toHaveFocus()
  })

  it('pressing Save changes with an invalid value sends nothing', async () => {
    const onSave = vi.fn(async () => ({ ok: true, status: 200, json: async () => ({}) }))
    await renderSaveBar([PORT_FIELD], { onSave })
    changeByLabel('Port', '80')
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(onSave).not.toHaveBeenCalled()
  })

  it('a value inside its range can be saved (D-10 row 4: max accepted)', async () => {
    const onSave = vi.fn(async () => ({ ok: true, status: 200, json: async () => ({}) }))
    await renderSaveBar([PORT_FIELD], { onSave })
    changeByLabel('Port', '65535')
    expect(screen.queryByText(/Fix 1 value before saving/)).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /save changes/i }))
    expect(await screen.findByText(/Saved at/)).toBeInTheDocument()
    expect(onSave).toHaveBeenCalledTimes(1)
  })
})

describe('FR-018 — the saved signal stays long enough to read', () => {
  function fakeClock() {
    vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
    vi.setSystemTime(new Date('2026-11-20T14:07:00'))
  }

  async function saveOnce(fields: any[]) {
    const view = await renderSaveBar(fields)
    fireEvent.change(screen.getByLabelText('Log level'), { target: { value: 'debug' } })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    })
    return view
  }

  it('the bar shows "Saved at 14:07" (HH:MM, 24 h) and the live region announces "Changes saved." once', async () => {
    fakeClock()
    const view = await saveOnce([LOG_FIELD])
    // Sync queries: findByText waits on the FAKE timers, whose queue never
    // advances, so the lookup hung to the test timeout — the act-wrapped
    // click above has already flushed the save, so the signal is in the DOM
    // now (harness fix; the oracle is unchanged).
    expect(screen.getByText('Saved at 14:07')).toBeInTheDocument()
    // The announcement is observed at the shell boundary the standalone
    // harness owns (see renderSaveBar): exactly once, with the spec's line.
    expect(view.announceCalls).toEqual(['Changes saved.'])
  })

  it('the saved signal hides after 8 seconds', async () => {
    fakeClock()
    await saveOnce([LOG_FIELD])
    expect(screen.getByText(/Saved at/)).toBeInTheDocument()
    await act(async () => {
      vi.advanceTimersByTime(8_000)
    })
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
  })

  it('a new edit within the 8 seconds replaces the saved signal with the change count at once', async () => {
    fakeClock()
    await renderSaveBar([LOG_FIELD, PORT_FIELD])
    fireEvent.change(screen.getByLabelText('Log level'), { target: { value: 'debug' } })
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: /save changes/i }))
    })
    expect(screen.getByText(/Saved at/)).toBeInTheDocument()
    await act(async () => {
      changeByLabel('Port', '8081')
    })
    expect(screen.getByText(/1 unsaved change/)).toBeInTheDocument()
    expect(screen.queryByText(/Saved at/)).not.toBeInTheDocument()
  })
})

// FR-017 (CHECK R3-01): exactly one gold action per screen. The synthetic
// Probe that used to live here (a private consumer of usePrimaryVariant under
// the harness shell) is REPLACED by the rendered proof on the real sections —
// saveBar.goldActions.rendered.test.tsx renders the real SettingsSectionBody
// for tab=providers and asserts the spec's oracle on the sections' real
// primary actions (the bar's Save changes, Connect a provider, the voice
// sheet's and the search panel's real Save keys), with the same gold/outline
// branches this probe pinned. Replacement cited per FR-017, which names the
// SCREEN, not a synthetic consumer — a probe cannot see a real action that
// skips the demotion (check mutant N31 survived the probe). The hook's two
// fault directions (always gold / always outline) are killed by the rendered
// file's bar-visible and bar-hidden assertions.
