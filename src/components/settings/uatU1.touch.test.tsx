// U1-5; oracle: settings-redesign-spec.md::US-9 AS5/FR-070 (44x44),
// design-system rule12. jsdom cannot measure hit geometry. These are actual
// control DOM + shipped CSS contracts, NOT claimed browser measurements.
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { compile } from '@tailwindcss/node'
import { parse } from 'postcss'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { cleanup, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { SettingsScreen } from '@/components/screens/SettingsScreen'
import { jsonG1, renderG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'

// Compile the actual SPA entry (including imports), not library.css, whose
// expander alone would not establish that the application ships it.
async function cssShipsTouchExpander(entryName: string) {
    const entry = resolve('src/styles', entryName)
    const compiled = await compile(readFileSync(entry, 'utf8'), { base: dirname(entry), onDependency() {} })
    const css = parse(compiled.build([]))
    const tokens = new Map<string, string>()
    css.walkDecls((d) => { if (d.prop.startsWith('--')) tokens.set(d.prop, d.value) })
    const expand = (value: string, seen = new Set<string>()): string => value.replace(/var\((--[\w-]+)\)/g, (_, key: string) => {
      if (seen.has(key)) return key
      return expand(tokens.get(key) ?? key, new Set([...seen, key]))
    })
    const atLeast44 = (value: string) => {
      const resolved = expand(value).trim()
      const literal = /^(\d+(?:\.\d+)?)px$/.exec(resolved)
      const maximum = /^max\(100%,\s*(\d+(?:\.\d+)?)px\)$/.exec(resolved)
      return Number(literal?.[1] ?? maximum?.[1] ?? 0) >= 44
    }
    let present = false
    const base = new Map<string, string>()
    css.walkRules((rule) => {
      if (!rule.selector.includes('[data-ds-action]') || !/::?before\b/.test(rule.selector)) return
      let coarse = false
      let parent: import('postcss').Root | import('postcss').Rule | import('postcss').AtRule | import('postcss').Document | undefined = rule.parent
      while (parent) {
        if (parent.type === 'atrule' && parent.name === 'media' && /pointer:\s*coarse/.test(parent.params)) coarse = true
        parent = parent.parent
      }
      const declarations = new Map<string, string>()
      rule.walkDecls((d) => { declarations.set(d.prop, d.value) })
      if (!coarse) for (const [key, value] of declarations) base.set(key, value)
      if (coarse && atLeast44(declarations.get('width') ?? '') && atLeast44(declarations.get('height') ?? '')) present = true
    })
    return present && ["''", '""'].includes(base.get('content') ?? '')
      && base.get('position') === 'absolute' && base.get('pointer-events') === 'auto'
}

let sharedExpander: Promise<boolean> | undefined
function appShipsTouchExpander() {
  sharedExpander ??= (async () => {
    expect(await cssShipsTouchExpander('library.css'),
      'instrument: the same parser/compiler must recognize the actual kit touch expander').toBe(true)
    return cssShipsTouchExpander('globals.css')
  })()
  return sharedExpander
}

async function expectTouchContract(control: HTMLElement, name: string) {
  const classes = [...control.classList]
  const coarse = '(?:pointer-coarse:|\\[@media\\(pointer:coarse\\)\\]:)'
  const minimum = '\\[(?:var\\(--target-touch-minimum\\)|44px)\\]'
  const height = classes.some((c) => new RegExp(`^${coarse}(?:min-)?h-${minimum}$`).test(c))
  const fullRow = classes.includes('w-full') || (classes.includes('flex-1') && control.parentElement?.classList.contains('flex'))
  const width = fullRow || classes.some((c) => new RegExp(`^${coarse}(?:min-)?w-${minimum}$`).test(c))
  const stamped = !(height && width) && control.hasAttribute('data-ds-action') && control.classList.contains('relative') && await appShipsTouchExpander()
  expect((height && width) || stamped,
    `U1-5/FR-070: ${name} needs a shipped [data-ds-action] 44x44 expander or explicit coarse-pointer minimum sizing; classes=${control.className}`).toBe(true)
}

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

function gateway() {
  transportG1()
  renderG1(<SettingsScreen section="gateway" requestedTab="gateway" />)
}

describe('U1-5 — real Settings controls carry a functioning touch contract', () => {
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
