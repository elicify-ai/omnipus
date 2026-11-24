// Settings #1519 U1-2. Oracle: settings-redesign-spec.md::US-10 AS4
// (#1742) and the explicit U1-2 no-silent-drop requirement. The values are
// sanitized equivalents of the validator's real gateway audit payload.
// Real viewer, adapter, generated schema and QueryClient; only HTTP is a seam.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AuditLogViewer } from './AuditLogViewer'
import { jsonG1, renderG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

const knownGood = { timestamp: '2026-11-24T04:39:42Z', event: 'credential.deleted', details: { outcome: 'deleted' } }
const change = (resource: string, old_value: unknown, new_value: unknown) => ({
  timestamp: '2026-11-24T04:54:35Z', event: 'security_setting_change', actor: 'u1-test-user', resource, old_value, new_value,
})

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

async function openAudit(entries: unknown[]) {
  const t = transportG1()
  t.on('GET', '/audit-log', () => jsonG1({ entries, chain_status: 'valid' }))
  renderG1(<AuditLogViewer open onOpenChange={() => {}} />)
  const dialog = await screen.findByRole('dialog', { name: 'Audit Log' })
  // Positive witness: the very same response reached the actual table.
  await within(dialog).findByText('credential.deleted', { selector: 'span' })
  return dialog
}

describe('U1-2 — Settings security changes cannot disappear from the audit viewer', () => {
  it.each([
    ['sandbox.prompt_injection_level', 'medium', 'high'],
    ['sandbox.mode', '', 'permissive'],
  ])('%s with plain old/new strings is listed and its exact values can be read', async (resource, oldValue, newValue) => {
    const dialog = await openAudit([change(resource, oldValue, newValue), knownGood])
    const rows = dialog.querySelectorAll('tbody > tr')
    expect(rows, 'U1-2/US-10 AS4: both gateway-written entries must be listed, not silently filtered').toHaveLength(2)
    await userEvent.click(within(dialog).getByRole('button', { name: 'Show details for this security_setting_change entry' }))
    expect(within(dialog).getByText(resource, { exact: true })).toBeVisible()
    // Values are audit data, not a prescribed choice of JSON quoting/layout.
    const details = dialog.querySelector('tr[id]')!
    expect(details).toHaveTextContent(newValue)
    if (oldValue !== '') expect(details).toHaveTextContent(oldValue)
    expect(within(dialog).getByText(/Showing 2 of 2 entries/)).toBeVisible()
  })

  it('every malformed entry is represented or visibly reported unreadable, never silently excluded from the total', async () => {
    const dialog = await openAudit([knownGood, { event: 'unreadable-u1-entry' }])
    const count = dialog.querySelectorAll('tbody > tr').length
    const notices = within(dialog).queryAllByText(/unreadable|could not (?:be )?read|cannot (?:be )?read|could not (?:be )?display|failed.*(?:record|entry)/i)
    // A placeholder does not invent audit facts; an explicit one-entry error
    // is equally valid. A dev-only console/toast outside the viewer is not.
    const oneUnreadable = notices.some((n) => /\b1\b|\bone\b/i.test(n.textContent ?? ''))
    expect(count === 2 || (count === 1 && oneUnreadable),
      `U1-2 conservation: 2 supplied = rendered entries + explicitly unreadable entries; got ${count} rows and ${oneUnreadable ? 1 : 0} visible unreadable entries`).toBe(true)
    expect(within(dialog).queryByText('Showing 1 of 1 entry — auto-refreshes every 30s'),
      'U1-2: a silently shortened list must not claim it is the complete log').not.toBeInTheDocument()
  })
})
