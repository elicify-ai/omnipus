// U3-1 / U3-3; independent oracle: fix-round-u3.md, US-9 AS5 / FR-070 / SC-009.
// Real shared controls and real Settings paths, not pixel measurements.
// jsdom cannot prove 14px-root arithmetic or actual hit geometry. The 390px
// coarse-pointer browser re-test must verify the resulting size and overlap.
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Dialog, DialogContent, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { Sheet, SheetContent, SheetTitle, SheetDescription } from '@/components/ui/sheet'
import { jsonG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { tokenAxisU3, tokenTouchU3 } from '../../../tests/helpers/settings-u3-contracts'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

it('U3 instrument — token sizing accepts both axes and rejects rem, raw-pixel and wrong-token substitutes', () => {
  for (const axis of ['h', 'w'] as const) {
    expect(tokenAxisU3([`pointer-coarse:min-${axis}-[var(--target-touch-minimum)]`], axis)).toBe(true)
    expect(tokenAxisU3([`pointer-coarse:min-${axis}-(--target-touch-minimum)`], axis)).toBe(true)
    expect(tokenAxisU3(['pointer-coarse:size-[var(--target-touch-minimum)]'], axis)).toBe(true)
    expect(tokenAxisU3([`pointer-coarse:${axis}-11`], axis)).toBe(false)
    expect(tokenAxisU3([`pointer-coarse:min-${axis}-[44px]`], axis)).toBe(false)
    expect(tokenAxisU3([`pointer-coarse:min-${axis}-[var(--target-pointer-minimum)]`], axis)).toBe(false)
    expect(tokenAxisU3([`pointer-coarse:min-${axis}-(--target-pointer-minimum)`], axis)).toBe(false)
    expect(tokenAxisU3([`min-${axis}-[var(--target-touch-minimum)]`], axis)).toBe(false)
  }
})

describe('U3-1 — shared sheet/dialog Close and Connect row use the touch-minimum token', () => {
  it.each(['sheet', 'dialog'] as const)('%s Close uses token sizing on both coarse-pointer axes', async (kind) => {
    if (kind === 'sheet') {
      render(<Sheet defaultOpen><SheetContent><SheetTitle>Shared test sheet</SheetTitle><SheetDescription>Synthetic close-control fixture.</SheetDescription></SheetContent></Sheet>)
    } else {
      render(<Dialog defaultOpen><DialogContent><DialogTitle>Shared test dialog</DialogTitle><DialogDescription>Synthetic close-control fixture.</DialogDescription></DialogContent></Dialog>)
    }
    const dialog = screen.getByRole('dialog', { name: `Shared test ${kind}` })
    const close = within(dialog).getByRole('button', { name: 'Close' })
    expect(close).toBeVisible()
    const contract = tokenTouchU3(close)
    const classes = close.className
    await userEvent.click(close)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(contract, `U3-1/FR-070: ${kind} Close needs the root-independent coarse token on both axes; classes=${classes}`).toEqual({ height: true, width: true })
  })
  it('the actual Connect sheet Custom endpoint row and Close have token touch contracts', async () => {
    const t = transportG1()
    sectionG1('providers')
    await userEvent.click(await screen.findByRole('button', { name: 'Connect a provider' }))
    const sheet = await screen.findByRole('dialog', { name: 'Connect a provider' })
    const custom = within(sheet).getByRole('option', { name: 'Custom endpoint' })
    const close = within(sheet).getByRole('button', { name: 'Close' })
    expect(custom).toBeVisible()
    expect(close).toBeVisible()
    expect(t.writes()).toEqual([])
    // The reported row defect is height only (302x32 measured): a block-level flex row already fills
    // its listbox, so only the token minimum height is pinned. Close is absolutely positioned and
    // needs both axes.
    expect([{ name: 'Custom endpoint', height: tokenAxisU3(custom.classList, 'h') }, { name: 'Close', ...tokenTouchU3(close) }],
      `U3-1/SC-009: the Custom endpoint row needs the token minimum height and Close needs both axes; Custom endpoint: ${custom.className}; Close: ${close.className}`).toEqual([
      { name: 'Custom endpoint', height: true }, { name: 'Close', height: true, width: true },
    ])
  })
})

it('U3-3 — expanded Protection health action Open Sandbox & network has the token touch contract', async () => {
  const t = transportG1()
  t.on('GET', '/doctor', () => jsonG1({ score: 50, checked_at: '2026-11-25T00:00:00Z', issues: [
    { id: 'u3-sandbox-disabled', severity: 'high', title: 'Sandbox is disabled', description: 'Synthetic disabled-sandbox diagnostic.', recommendation: 'Review Sandbox & network.', action_label: 'Open Sandbox & network', action_link: '/settings?tab=sandbox' },
  ] }))
  sectionG1('protection')
  const issue = await screen.findByRole('button', { name: /Sandbox is disabled/ })
  expect(issue).toHaveAttribute('aria-expanded', 'false')
  await userEvent.click(issue)
  expect(issue).toHaveAttribute('aria-expanded', 'true')
  const action = screen.getByRole('link', { name: 'Open Sandbox & network' })
  expect(action).toBeVisible()
  expect(action).toHaveAttribute('href', '/#/settings?tab=sandbox')
  expect(t.writes()).toEqual([])
  expect(tokenTouchU3(action), `U3-3/FR-070: the real expanded health link needs a token touch target; classes=${action.className}`).toEqual({ height: true, width: true })
})
