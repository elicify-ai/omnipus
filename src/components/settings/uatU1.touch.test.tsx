// U1-5; oracle: settings-redesign-spec.md::US-9 AS5/FR-070 (44x44),
// design-system rule12. jsdom cannot measure hit geometry. These are actual
// control DOM + shipped CSS contracts, NOT claimed browser measurements.
import { parse } from 'postcss'
import { Button } from '@/components/ui/button'
import { Switch } from '@/components/ui/switch'
import { pseudoTouchU2, stylesheetU2, touchContractU2 } from '../../../tests/helpers/settings-u2-css'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SettingsScreen } from '@/components/screens/SettingsScreen'
import { jsonG1, renderG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

// U1-C2: share U2-1's actual-control matcher. No U2 source is changed.
// A Switch-only rule cannot certify an ordinary data-ds-action Button.
async function expectTouchContract(control: HTMLElement, name: string) {
  const result = await touchContractU2(control)
  expect(result.pass,
    `U1-5/FR-070: ${name} needs a selector-matched shipped 44x44 expander or both explicit sizing axes; classes=${control.className}; contract=${JSON.stringify(result)}`).toBe(true)
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

function gateway() {
  transportG1()
  renderG1(<SettingsScreen section="gateway" requestedTab="gateway" />)
}

describe('U1-5 — real Settings controls carry a functioning touch contract', () => {
  it('U1-C2 instrument — a Switch-only 44x44 rule rejects an ordinary stamped Button', async () => {
    renderG1(<><Button>Ordinary action</Button><Switch aria-label="Instrument switch" /></>)
    const css = parse(`
      [role='switch'][data-ds-action]::before {
        content: ''; position: absolute; pointer-events: auto;
      }
      @media (pointer: coarse) {
        [role='switch'][data-ds-action]::before { width: 44px; height: 44px; }
      }
    `)
    const button = await screen.findByRole('button', { name: 'Ordinary action' })
    expect(button).toHaveAttribute('data-ds-action')
    expect(button).toHaveClass('relative')
    // Preserve the original compiler/parser positive control on actual kit CSS.
    expect(pseudoTouchU2(await stylesheetU2('library.css'), button)).toBe(true)
    // Same checker and same rule: positive witness, then wrong-target rejection.
    expect(pseudoTouchU2(css, screen.getByRole('switch', { name: 'Instrument switch' }))).toBe(true)
    expect(pseudoTouchU2(css, button), 'FR-070: a rule must match this actual control, not just another kit control').toBe(false)
  })
  it('Gateway Preview server switch has a 44x44 coarse-pointer hit region', async () => {
    gateway()
    await expectTouchContract(await screen.findByRole('switch', { name: 'Preview server' }), 'Preview server switch')
  })
  it('the phone Settings back button has a 44x44 coarse-pointer hit region', async () => {
    gateway()
    await screen.findByRole('spinbutton', { name: 'Port' })
    await expectTouchContract(screen.getByRole('button', { name: 'Settings' }), 'Settings back')
  })
  it('the real Port input has coarse-pointer minimum height and full-row width', async () => {
    gateway()
    await expectTouchContract(await screen.findByRole('spinbutton', { name: 'Port' }), 'Port')
  })
  it('Limits disclosure rows carry explicit coarse-pointer minimum sizing and still toggle', async () => {
    transportG1()
    sectionG1('limits')
    const disclosure = await screen.findByRole('button', { name: 'Context and tool results' })
    await expectTouchContract(disclosure, 'Limits disclosure')
    expect(disclosure).toHaveAttribute('aria-expanded', 'false')
    await userEvent.click(disclosure)
    expect(disclosure).toHaveAttribute('aria-expanded', 'true')
  })
  it('Sandbox custom-address remove buttons have a 44x44 coarse-pointer hit region', async () => {
    const t = transportG1()
    t.on('GET', '/security/sandbox-config', () => jsonG1({
      mode: 'enforce', applied_mode: 'enforce', auto_approve: false, allowed_paths: [],
      ssrf: { allow_internal: ['10.20.30.40'] }, requires_restart: false,
    }))
    sectionG1('sandbox')
    const advanced = await screen.findByRole('button', { name: 'Advanced (custom list)' })
    if (advanced.getAttribute('aria-expanded') !== 'true') await userEvent.click(advanced)
    await expectTouchContract(await screen.findByRole('button', { name: 'Delete SSRF entry 10.20.30.40' }), 'Sandbox remove address')
    expect(t.writes(), 'inspecting touch targets never changes network policy').toEqual([])
  })
})
