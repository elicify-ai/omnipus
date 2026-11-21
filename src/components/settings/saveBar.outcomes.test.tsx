/**
 * saveBar.outcomes.test.tsx — Settings redesign #1519, RED.
 *
 * Test Implementation Order row 21 (FR-020, C-6 outcome table; review
 * R2-MAJ-005): each write ends in one of FOUR outcomes, classified in this
 * order — a RECOGNIZED signal beats the HTTP status:
 *   (1) saved, not in use — 500 with code "saved_not_refreshed" (keys from
 *       details.keys), or 200 with requires_restart: true, or 200
 *       PerformanceSettings with pending_apply;
 *   (2) saved and in use — any other 2xx;
 *   (3) not saved — 4xx refusal: draft kept, message under the field;
 *   (4) unknown — any other 5xx, connection failure or lost response: the
 *       screen runs the saved-state check (GET /config/pending-restart plus
 *       the section's read) — draft == saved value → saved (in use when the
 *       key is not listed, not in use when listed); draft == pre-save value
 *       → not saved; neither → the draft stays and the field says the value
 *       was changed somewhere else; both reads fail → the bar says
 *       "N change(s) may not have been saved" with Retry.
 *
 * The SaveBar component does not exist yet, so every test below loads it
 * through vi.importActual and fails with a loud BLOCKED message naming its
 * FR — never a module-resolution crash that kills the file's other tests.
 * After GREEN the same file loads the real component and the assertions
 * run. GREEN's exact prop names may need a test-side adjustment (same
 * author); the ORACLES — the FR-020 wording, the classification order and
 * the read-back rule — are frozen.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  return { ...actual }
})

async function loadSaveBar(): Promise<any> {
  try {
    const mod: any = await vi.importActual('@/components/settings/SaveBar')
    return mod.SaveBar ?? mod.default
  } catch {
    throw new Error(
      'BLOCKED RED (FR-020, C-6 outcome table): the Settings SaveBar component does not exist yet — ' +
        'required by FR-016/FR-019/FR-020 so every write ends in one of four outcomes',
    )
  }
}

// One editable Gateway field (log level) rendered through the Save bar; the
// save callback resolves to the given fetch-like response.
async function renderSaveBarWith(saveImpl: (body: unknown) => Promise<any>) {
  const SaveBar = await loadSaveBar()
  render(<SaveBar section="gateway" fields={[{ key: 'gateway.log_level', label: 'Log level', value: 'info', kind: 'select', options: ['info', 'debug'] }]} onSave={saveImpl} onDiscard={vi.fn()} />)
  await userEvent.selectOptions(screen.getByLabelText('Log level'), 'debug')
  await userEvent.click(screen.getByRole('button', { name: /save/i }))
}

beforeEach(() => {
  vi.clearAllMocks()
})

describe('FR-020 — each write ends in one of four outcomes', () => {
  it('a 500 with code saved_not_refreshed says saved, but not in use, and lists the key', async () => {
    await renderSaveBarWith(async () => ({
      ok: false,
      status: 500,
      json: async () => ({ error: 'settings saved, but the running configuration could not be refreshed', code: 'saved_not_refreshed', details: { keys: ['gateway.log_level'] } }),
    }))
    await waitFor(() => expect(screen.getByText(/Saved, but not in use yet/i)).toBeInTheDocument())
    expect(screen.getByText(/gateway\.log_level/)).toBeInTheDocument()
  })

  it('a 200 with requires_restart true also says saved, but not in use (recognized signal before status)', async () => {
    await renderSaveBarWith(async () => ({ ok: true, status: 200, json: async () => ({ requires_restart: true }) }))
    await waitFor(() => expect(screen.getByText(/Saved, but not in use yet/i)).toBeInTheDocument())
  })

  it('any other 2xx says saved and in use', async () => {
    await renderSaveBarWith(async () => ({ ok: true, status: 200, json: async () => ({}) }))
    await waitFor(() => expect(screen.getByText(/saved/i)).toBeInTheDocument())
    expect(screen.queryByText(/not in use/i)).not.toBeInTheDocument()
  })

  it('a 4xx refusal keeps the draft and shows the message under the field', async () => {
    await renderSaveBarWith(async () => ({
      ok: false,
      status: 400,
      json: async () => ({ error: 'log_level must be one of info, warning, debug', field: 'log_level' }),
    }))
    await waitFor(() => expect(screen.getByText(/must be one of/i)).toBeInTheDocument())
    expect(screen.getByLabelText('Log level')).toHaveValue('debug')
  })

  it('an unknown result is checked by reading the saved value back — draft equals the saved value, key not listed → saved and in use (FR-020, C-6)', async () => {
    const fetchMock = vi.fn(async (input: any) => {
      const url = String(input)
      if (url.includes('/config/pending-restart')) {
        return { ok: true, status: 200, json: async () => [] }
      }
      return { ok: false, status: 502, json: async () => ({}) }
    })
    vi.stubGlobal('fetch', fetchMock)
    // The saved-value read is SUPPLIED: pending-restart does not list the
    // key, so the saved value equals the running value, which the read back
    // reports as the draft's own value ('debug') — the C-6 rule for an
    // unlisted key. Without this read the check stays unknown, and a bare
    // /saved/i match cannot tell the two apart (CHECK F07: the old oracle
    // matched "may not have been saved").
    const SaveBar = await loadSaveBar()
    render(
      <SaveBar
        section="gateway"
        fields={[{ key: 'gateway.log_level', label: 'Log level', value: 'info', kind: 'select', options: ['info', 'debug'] }]}
        onSave={async () => ({ ok: false, status: 502, json: async () => ({}) })}
        onDiscard={vi.fn()}
        readBack={async () => 'debug'}
      />,
    )
    await userEvent.selectOptions(screen.getByLabelText('Log level'), 'debug')
    await userEvent.click(screen.getByRole('button', { name: /save/i }))
    // The saved-state check ran (C-6's file read was consulted).
    await waitFor(() => expect(fetchMock.mock.calls.some(([u]) => String(u).includes('/config/pending-restart'))).toBe(true))
    // FR-020 outcome 2 (saved and in use, reached through the check): the
    // bar shows its exact saved presentation, "Saved at HH:MM" (FR-018).
    await waitFor(() => expect(screen.getByText(/Saved at/)).toBeInTheDocument())
    // The draft is resolved — nothing is offered as an unsaved change (FR-012) —
    // and none of the other three outcomes' presentations may appear.
    expect(screen.queryByText(/unsaved change/)).not.toBeInTheDocument()
    expect(screen.queryByText(/may not have been saved/)).not.toBeInTheDocument()
    expect(screen.queryByText(/could not be saved/)).not.toBeInTheDocument()
    expect(screen.queryByText(/changed somewhere else/i)).not.toBeInTheDocument()
    expect(screen.getByLabelText('Log level')).toHaveValue('debug')
    vi.unstubAllGlobals()
  })

  it('a changed-elsewhere value is reported as neither saved nor unsaved', async () => {
    const fetchMock = vi.fn(async (input: any) => {
      if (String(input).includes('/config/pending-restart')) {
        return { ok: true, status: 200, json: async () => [] }
      }
      return { ok: false, status: 502, json: async () => ({}) }
    })
    vi.stubGlobal('fetch', fetchMock)
    // The section read-back returns a value that is neither the draft nor
    // the pre-save value: the draft stays and the field names the change.
    const SaveBar = await loadSaveBar()
    render(
      <SaveBar
        section="gateway"
        fields={[{ key: 'gateway.log_level', label: 'Log level', value: 'info', kind: 'select', options: ['info', 'warning', 'debug'] }]}
        onSave={async () => ({ ok: false, status: 502, json: async () => ({}) })}
        onDiscard={vi.fn()}
        readBack={async () => 'warning'}
      />,
    )
    await userEvent.selectOptions(screen.getByLabelText('Log level'), 'debug')
    await userEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() =>
      expect(screen.getByText(/changed somewhere else.*Saved value: warning/i)).toBeInTheDocument(),
    )
    vi.unstubAllGlobals()
  })

  it('if both reads fail the bar says N changes may not have been saved, with Retry', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('network down') }))
    await renderSaveBarWith(async () => { throw new TypeError('network down') })
    await waitFor(() => expect(screen.getByText(/1 change[s]? may not have been saved/i)).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /retry/i })).toBeInTheDocument()
    vi.unstubAllGlobals()
  })
})
