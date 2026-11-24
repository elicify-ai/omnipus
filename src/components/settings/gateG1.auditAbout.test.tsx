// Settings #1519 gate G1, F14/PTA-01 and PTA-02. FR-107: both audit
// payload carriers expand. FR-105: Copy includes the build commit. Real API
// adapters/generated validators/components/store; only fetch and clipboard are edges.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AuditLogViewer } from './AuditLogViewer'
import { AboutSection } from './AboutSection'
import { useUiStore } from '@/store/ui'
import { aboutG1, jsonG1, renderG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

afterEach(() => {
  cleanup()
  for (const toast of useUiStore.getState().toasts) useUiStore.getState().removeToast(toast.id)
  vi.unstubAllGlobals(); vi.restoreAllMocks()
})

describe('F14/PTA-01 FR-107 — external CLI audit payloads can be read', () => {
  it.each(['fields', 'details'] as const)('%s carrying preset_args exposes Show details and reveals the payload', async (carrier) => {
    const t = transportG1()
    t.on('GET', '/audit-log', () => jsonG1({
      entries: [{ timestamp: '2026-11-22T12:00:00Z', event: 'external_cli.run', [carrier]: { preset_args: '--profile G1Work' } }],
      chain_status: 'unknown',
    }))
    renderG1(<AuditLogViewer open onOpenChange={() => {}} />)
    await screen.findByText('external_cli.run')
    const expand = screen.queryByRole('button', { name: /show details for this external_cli\.run entry/i })
    expect(expand, `F14/FR-107: an audit row carrying only ${carrier}.preset_args must have Show details`).not.toBeNull()
    await userEvent.click(expand!)
    expect(await screen.findByText(/"preset_args": "--profile G1Work"/), `F14: expanding ${carrier} must reveal its actual payload`).toBeVisible()
  })
})

it('PTA-02 FR-105 — About Copy includes the exact build commit from GET version', async () => {
  const t = transportG1()
  const user = userEvent.setup()
  const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue()
  renderG1(<AboutSection />)
  await screen.findByText('abc1234')
  await user.click(screen.getByRole('button', { name: 'Copy' }))
  await waitFor(() => expect(copy, 'PTA-02/FR-105: the support copy must include the commit displayed by About').toHaveBeenCalledExactlyOnceWith(
    `Omnipus ${aboutG1.version}\nBuild commit: abc1234\nGo: ${aboutG1.go_version}\nOS: linux\nArch: amd64\nUptime: 1h 0m`,
  ))
  expect(t.calls.filter((c) => c.path === '/version')).toHaveLength(1)
})
