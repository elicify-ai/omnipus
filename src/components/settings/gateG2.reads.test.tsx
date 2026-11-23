// G2-F3. Oracles: settings-redesign-spec FR-066, Read dependencies / UI
// states, and docs/settings.md, Saving changes. GET /config belongs to
// neither Protection nor Keys & secrets. All production code remains real.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

function configReadFails() {
  const t = transportG1()
  t.on('GET', '/config', () => jsonG1({ error: 'G2 unrelated config read failed' }, 500))
  return t
}

describe('G2-F3 FR-066 — failed unused config read cannot block successful groups', () => {
  it('Protection renders Approvals, God Mode, defense and rate groups and saves its changed limit', async () => {
    const t = configReadFails()
    let limit = 7 // The transport's saved A; 42 is the user's valid new choice.
    t.on('GET', '/security/rate-limits', () => jsonG1({ enabled: true, max_agent_llm_calls_per_hour: limit, max_agent_tool_calls_per_minute: 9 }))
    t.on('PUT', '/security/rate-limits', ({ body }) => {
      limit = (body as { max_agent_llm_calls_per_hour: number }).max_agent_llm_calls_per_hour
      return jsonG1({ saved: true, requires_restart: false, applied: { enabled: true, max_agent_llm_calls_per_hour: limit, max_agent_tool_calls_per_minute: 9 } })
    })
    const { client } = sectionG1('protection')
    await waitFor(() => {
      expect(client.getQueryState(['sandbox-config'])?.status).toBe('success')
      expect(client.getQueryState(['prompt-guard-k'])?.status).toBe('success')
      expect(client.getQueryState(['security', 'rate-limits'])?.status).toBe('success')
      expect(client.isFetching()).toBe(0)
    })
    expect(screen.queryByText('The Protection settings could not be loaded'), 'G2-F3: a successful section cannot be replaced by an unrelated whole-section error').not.toBeInTheDocument()
    expect(screen.getByRole('switch', { name: 'Auto-approve' })).toBeEnabled()
    expect(await screen.findByTestId('god-mode-control')).toBeVisible()
    expect(screen.getByRole('region', { name: 'Prompt injection defense' })).toBeVisible()
    expect(screen.getByRole('region', { name: 'Rate limits per agent' })).toBeVisible()
    expect(screen.getByRole('region', { name: 'Security health' })).toBeVisible()
    fireEvent.change(screen.getByRole('spinbutton', { name: 'Model calls per hour' }), { target: { value: '42' } })
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    await screen.findByText(/Saved at/)
    expect(t.writes('/security/rate-limits').map((c) => c.body), 'FR-015: successful dependent reads still permit a sparse save').toEqual([{ max_agent_llm_calls_per_hour: 42 }])
    expect(limit).toBe(42)
  })

  it('Keys & secrets renders its successful credential list and can store a new key', async () => {
    const t = configReadFails()
    const keys = ['G2_EXISTING_KEY']
    t.on('GET', '/credentials', () => jsonG1(keys))
    t.on('POST', '/credentials', ({ body }) => {
      keys.push((body as { key: string }).key)
      return jsonG1({})
    })
    const { client } = sectionG1('keys')
    await waitFor(() => {
      expect(client.getQueryState(['credentials'])?.status).toBe('success')
      expect(client.isFetching()).toBe(0)
    })
    expect(screen.queryByText('The Keys & secrets settings could not be loaded'), 'G2-F3: failed unused config cannot hide a successfully read vault').not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Rotate master key' })).toBeEnabled()
    expect(screen.getByRole('button', { name: 'Remove credential G2_EXISTING_KEY' })).toBeEnabled()
    await userEvent.click(screen.getByRole('button', { name: 'Add key' }))
    const add = await screen.findByRole('dialog', { name: 'Add Credential' })
    await userEvent.type(within(add).getByRole('textbox', { name: 'Key name' }), 'G2_NEW_KEY')
    // Synthetic value is local test input, not a real credential.
    await userEvent.type(within(add).getByLabelText('Value', { exact: true }), 'g2-fixture-value')
    await userEvent.click(within(add).getByRole('button', { name: /^Save$/ }))
    const consent = await screen.findByRole('dialog', { name: 'Confirm your password' })
    await userEvent.type(within(consent).getByLabelText('Password', { exact: true }), 'g2-fixture-password')
    await userEvent.click(within(consent).getByRole('button', { name: /^Confirm$/ }))
    await screen.findByRole('button', { name: 'Remove credential G2_NEW_KEY' })
    expect(t.writes('/credentials').map((c) => c.body), 'FR-055: key buttons remain actual immediate actions, not draft Save fields').toEqual([{ key: 'G2_NEW_KEY', value: 'g2-fixture-value' }])
    expect(new Headers(t.writes('/credentials')[0].init?.headers).get('X-Reauth-Token')).toBe('g1-single-use-consent')
  })
})
