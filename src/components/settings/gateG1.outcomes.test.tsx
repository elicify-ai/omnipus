// Settings G1 F3/F6/F7/F16. FR-015/019/020/021/022 and C-6.
// Inputs and expected outcomes are the spec/gate scenarios. Only fetch is
// replaced: serializers, generated validators, QueryClient and sections are real.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonG1, retentionG1, sectionG1, thirdPartyG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })
const save = () => screen.getByRole('button', { name: 'Save changes' })
const notInUse = /saved, but (it is |they are )?not in use|saved.*not.*in use/i

describe('F3 FR-015/021 — two stale Protection tabs write only changed rate members', () => {
  it.each(['model-first', 'tool-first'])('%s preserves both changes in actual serialized bodies', async (order) => {
    const t = transportG1()
    const stored = { enabled: true, max_agent_llm_calls_per_hour: 7, max_agent_tool_calls_per_minute: 9 }
    t.on('GET', '/security/rate-limits', () => jsonG1(stored))
    t.on('PUT', '/security/rate-limits', ({ body }) => {
      Object.assign(stored, body)
      return jsonG1({ saved: true, requires_restart: false, applied: stored })
    })
    const a = sectionG1('protection')
    const b = sectionG1('protection')
    await waitFor(() => expect(within(a.container).getByRole('spinbutton', { name: 'Model calls per hour' })).toHaveValue(7))
    await waitFor(() => expect(within(b.container).getByRole('spinbutton', { name: 'Tool calls per minute' })).toHaveValue(9))
    fireEvent.change(within(a.container).getByRole('spinbutton', { name: 'Model calls per hour' }), { target: { value: '42' } })
    fireEvent.change(within(b.container).getByRole('spinbutton', { name: 'Tool calls per minute' }), { target: { value: '17' } })
    const views = order === 'model-first' ? [a, b] : [b, a]
    for (const [i, view] of views.entries()) {
      await userEvent.click(within(view.container).getByRole('button', { name: 'Save changes' }))
      await waitFor(() => expect(t.writes('/security/rate-limits')).toHaveLength(i + 1))
    }
    const expected = [{ max_agent_llm_calls_per_hour: 42 }, { max_agent_tool_calls_per_minute: 17 }]
    expect(t.writes('/security/rate-limits').map((c) => c.body), 'F3: untouched sibling values must never be serialized from a stale tab').toEqual(order === 'model-first' ? expected : [...expected].reverse())
    expect(stored, 'F3: saving the other member must not overwrite the first save').toEqual({ enabled: true, max_agent_llm_calls_per_hour: 42, max_agent_tool_calls_per_minute: 17 })
  })
})

describe('F6 FR-020/022 — CLI paths and arguments are opaque, case-sensitive strings', () => {
  it.each([
    ['path', 'Codex program path', '/opt/bin/codex'],
    ['preset_args', 'Codex parameters', '--profile work'],
  ])('%s case-only failed write does not become a saved success', async (field, label, draft) => {
    const t = transportG1()
    t.on('PUT', '/settings/third-party-agents', () => jsonG1({ error: 'G1 write failed before persistence' }, 500))
    sectionG1('third-party-agents')
    const input = await screen.findByRole('textbox', { name: label })
    fireEvent.change(input, { target: { value: draft } })
    await userEvent.click(save())
    await waitFor(() => expect(t.calls.filter((c) => c.path === '/settings/third-party-agents' && c.method === 'GET').length).toBeGreaterThanOrEqual(2))
    await screen.findByText(/Saved at|may not have been saved|could not be saved|changed somewhere else/)
    expect(t.writes('/settings/third-party-agents')[0]?.body).toEqual({ clis: {
      'claude-code': { path: thirdPartyG1.clis['claude-code'].path, preset_args: '' },
      codex: { path: thirdPartyG1.clis.codex.path, preset_args: thirdPartyG1.clis.codex.preset_args, [field]: draft },
      opencode: { path: '', preset_args: '' },
    } })
    expect(screen.queryByText(/Saved at/), 'F6: an old value differing only in case is NOT the value this Save sent').not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: label })).toHaveValue(draft)
    expect(screen.queryByRole('button', { name: 'Discard' }), 'F6: failed exact-value reconciliation must retain the unsaved draft').not.toBeNull()
  })
})

describe('F7 C-6 — lost answers use canonical pending keys', () => {
  it.each([
    ['gateway', 'Bind address', 'gateway.host', '0.0.0.0', '127.0.0.1'],
    ['protection', 'Model calls per hour', 'sandbox.rate_limits.max_agent_llm_calls_per_hour', 42, 7],
  ] as const)('%s recovers the persisted value as saved, not in use', async (section, label, key, draft, before) => {
    const t = transportG1()
    const path = section === 'gateway' ? '/config' : '/security/rate-limits'
    let saved = false
    t.on('PUT', path, () => { saved = true; throw new TypeError('G1 response lost after persistence') })
    t.on('GET', '/config/pending-restart', () => jsonG1(saved ? [{ key, persisted_value: draft, applied_value: before, reason: section === 'gateway' ? 'restart_required' : 'apply_failed' }] : []))
    sectionG1(section)
    const input = await screen.findByRole(section === 'gateway' ? 'combobox' : 'spinbutton', { name: label })
    if (section === 'gateway') await userEvent.selectOptions(input, String(draft))
    else fireEvent.change(input, { target: { value: String(draft) } })
    await userEvent.click(save())
    if (section === 'gateway') await userEvent.click(await screen.findByRole('button', { name: 'Listen on all interfaces' }))
    await waitFor(() => expect(t.writes(path)).toHaveLength(1))
    expect(await screen.findByText(notInUse), `F7: ${key} must match the UI row and prevent false not-saved/in-use claims`).toBeVisible()
    expect(screen.queryByText(/This change was not saved/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Discard' })).not.toBeInTheDocument()
  })
})

describe('F16 C-6/B1 — retention saved-not-refreshed is never in-use success', () => {
  it.each(['recognized-500', 'lost-answer'] as const)('%s preserves the persisted value with a saved-not-in-use warning', async (mode) => {
    const t = transportG1()
    let persisted = false
    t.on('PUT', '/settings/retention', () => {
      persisted = true
      if (mode === 'lost-answer') throw new TypeError('G1 retention response lost')
      return jsonG1({ error: 'saved but live refresh failed', code: 'saved_not_refreshed', details: { keys: ['storage.retention.memory_retros_forever'] } }, 500)
    })
    t.on('GET', '/settings/retention', () => jsonG1({ ...retentionG1, memory_retros_forever: persisted }))
    t.on('GET', '/config/pending-restart', () => jsonG1(persisted ? [{ key: 'storage.retention.memory_retros_forever', applied_value: false, persisted_value: true, reason: 'apply_failed' }] : []))
    sectionG1('data')
    await userEvent.click(await screen.findByRole('switch', { name: 'Keep forever' }))
    await userEvent.click(save())
    expect(await screen.findByText(notInUse), 'F16: a disk-equal retention read must never imply that the old live deletion policy stopped').toBeVisible()
    expect(screen.getByRole('switch', { name: 'Keep forever' })).toBeChecked()
    expect(screen.queryByRole('button', { name: 'Discard' }), 'F16: a value known to be persisted is not discardable').not.toBeInTheDocument()
    expect(t.writes('/settings/retention').map((c) => c.body)).toEqual([{ memory_retros_forever: true }])
  })
})
