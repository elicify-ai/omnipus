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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
// Type-only: the save callback's real contract is the Save bar's
// WriteResponse (saveOutcome.ts), the shape classifyWrite reads.
import type { WriteResponse } from './saveOutcome'

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  return { ...actual }
})

async function loadSaveBar(): Promise<typeof import('@/components/settings/SaveBar')['SaveBar']> {
  try {
    const mod = await vi.importActual<typeof import('@/components/settings/SaveBar')>('@/components/settings/SaveBar')
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
async function renderSaveBarWith(saveImpl: (body: unknown) => Promise<WriteResponse>) {
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
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
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
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
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

  it('Discard with an unknown result asks first — the confirm says the server may already hold the new value; Cancel keeps the draft and the result; confirmed Discard stores nothing (FR-012, spec BDD "An unknown result is checked by reading back")', async () => {
    const onSave = vi.fn(async () => ({ ok: false, status: 502, json: async () => ({}) }))
    vi.stubGlobal('fetch', vi.fn(async () => { throw new TypeError('network down') }))
    const SaveBar = await loadSaveBar()
    render(
      <SaveBar
        section="gateway"
        fields={[{ key: 'gateway.log_level', label: 'Log level', value: 'info', kind: 'select', options: ['info', 'debug'] }]}
        onSave={onSave}
        onDiscard={vi.fn()}
      />,
    )
    await userEvent.selectOptions(screen.getByLabelText('Log level'), 'debug')
    await userEvent.click(screen.getByRole('button', { name: /save/i }))
    // The write's answer AND both reads of the saved-state check failed:
    // the result is unknown, the bar says so with Retry, the draft stays.
    expect(await screen.findByText(/1 change may not have been saved/i)).toBeInTheDocument()
    expect(screen.getByLabelText('Log level')).toHaveValue('debug')
    // FR-012 (spec:1916): for a write whose result is still unknown, its
    // confirm says the server may already hold the new value. Pressing
    // Discard must NOT silently clear an unknown write. The confirm is the
    // catalogued ConfirmDialog (a Radix AlertDialog → role "alertdialog");
    // its exact wording pins the FR-012 meaning, and its safe default is
    // "Keep changes" (Cancel), with Discard as the destructive confirm.
    await userEvent.click(screen.getByRole('button', { name: /discard/i }))
    const confirm = await screen.findByRole('alertdialog')
    expect(confirm).toHaveTextContent(/already hold/i)
    expect(confirm).toHaveTextContent(/does not undo a stored value/i)
    // Cancel ("Keep changes"): the unknown result and the draft stay exactly
    // as they were.
    await userEvent.click(within(confirm).getByRole('button', { name: /keep changes/i }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(screen.getByText(/1 change may not have been saved/i)).toBeInTheDocument()
    expect(screen.getByLabelText('Log level')).toHaveValue('debug')
    // Confirmed Discard stores nothing: no further write goes out (one Save
    // so far), the draft returns to the saved value and the unknown result
    // is dismissed by the person who was told the server may hold it.
    await userEvent.click(screen.getByRole('button', { name: /discard/i }))
    const confirm2 = await screen.findByRole('alertdialog')
    await userEvent.click(within(confirm2).getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument())
    expect(onSave).toHaveBeenCalledTimes(1)
    expect(screen.getByLabelText('Log level')).toHaveValue('info')
    expect(screen.queryByText(/1 change may not have been saved/i)).not.toBeInTheDocument()
    vi.unstubAllGlobals()
  })
})
