import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

type FoundationToken = {
  id: string
  css: string
  layer: 'primitive' | 'semantic' | 'component'
  kind: string
  value?: string | number
  ref?: string
  description?: string
  responsive?: boolean
}

const source = JSON.parse(
  readFileSync(resolve(process.cwd(), 'design-system/tokens/foundations.json'), 'utf8'),
) as { version: number; tokens: FoundationToken[] }

const byId = new Map(source.tokens.map((token) => [token.id, token]))

function expectValue(id: string, value: string | number) {
  expect(byId.get(id), `missing ${id}`).toMatchObject({ value })
}

function expectRef(id: string, ref: string) {
  expect(byId.get(id), `missing ${id}`).toMatchObject({ ref })
}

describe('non-colour foundation token contract', () => {
  it('uses the shared versioned schema with unique ids and CSS properties', () => {
    expect(source.version).toBe(1)
    expect(source.tokens.length).toBeGreaterThan(70)
    expect(new Set(source.tokens.map(({ id }) => id)).size).toBe(source.tokens.length)
    expect(new Set(source.tokens.map(({ css }) => css)).size).toBe(source.tokens.length)

    for (const token of source.tokens) {
      expect(token.id).toMatch(/^[a-z][a-zA-Z0-9]*(\.[a-zA-Z0-9]+)+$/)
      expect(token.css).toMatch(/^--[a-z0-9-]+$/)
      expect(['primitive', 'semantic', 'component']).toContain(token.layer)
      expect(['color', 'dimension', 'number', 'duration', 'fontFamily', 'fontWeight', 'lineHeight', 'shadow', 'easing', 'string']).toContain(token.kind)
      expect(Number(token.value !== undefined) + Number(token.ref !== undefined)).toBe(1)
      expect(token.description?.trim().length).toBeGreaterThan(0)
    }
  })

  it('defines the three font families and complete product typography roles', () => {
    expectValue('font.family.heading', "'Outfit', system-ui, -apple-system, sans-serif")
    expectValue('font.family.body', "'Inter', system-ui, -apple-system, sans-serif")
    expectValue('font.family.mono', "'JetBrains Mono', ui-monospace, monospace")
    expectValue('font.size.floor', '12px')
    expectValue('font.root.minimum', '12px')
    expectValue('font.root.default', '14px')
    expectValue('font.root.maximum', '20px')

    for (const role of ['display', 'pageTitle', 'sectionTitle', 'body', 'bodyCompact', 'label', 'caption', 'code']) {
      for (const property of ['family', 'size', 'weight', 'lineHeight', 'letterSpacing', 'measure']) {
        expect(byId.has(`type.${role}.${property}`), `missing type.${role}.${property}`).toBe(true)
      }
    }
    expectValue('font.size.caption', 'max(12px, 0.857142857rem)')
    expectValue('font.size.utilityXs', 'max(12px, 0.75rem)')
    expectRef('type.utilityXs.size', 'font.size.utilityXs')
    expect(byId.get('type.utilityXs.size')?.responsive).toBe(true)
    expect(byId.get('type.body.size')?.responsive).toBe(true)
  })

  it('defines one closed 4px/8px spacing scale and preserves exceptional fixed geometry', () => {
    // 2px and 12px are the intermediate rungs the founder approved on 2026-09-20
    // (definition D10 amendment): D10's own mapping needs them for Tailwind's odd steps.
    const allowed = ['0px', '2px', '4px', '8px', '12px', '16px', '24px', '32px', '40px', '48px', '64px', '80px']
    const scale = source.tokens
      .filter(({ id }) => id.startsWith('space.scale.'))
      .map(({ value }) => value)
    expect(scale).toEqual(allowed)
    expectRef('space.control.gap', 'space.scale.2')
    expectRef('space.content.padding', 'space.scale.4')
    expectRef('space.section.gap', 'space.scale.6')
    expectRef('space.layout.gutter', 'space.scale.5')
    expectRef('space.page.margin', 'space.scale.6')
    expectValue('layout.sidebar.width', 'min(256px, 80vw)')
    expectValue('chrome.browserTabs.height', '34px')
    expectValue('target.pointer.minimum', '24px')
    expectValue('target.touch.minimum', '44px')
  })

  it('defines behavioral breakpoints and density without changing type or hit-area floors', () => {
    expectValue('breakpoint.reflow.minimum', '320px')
    expectValue('breakpoint.content.medium', '640px')
    expectValue('breakpoint.content.wide', '1024px')
    expectRef('density.comfortable.controlGap', 'space.scale.3')
    expectRef('density.compact.controlGap', 'space.scale.2')
    expectRef('density.comfortable.touchTarget', 'target.touch.minimum')
    expectRef('density.compact.touchTarget', 'target.touch.minimum')
    expectRef('density.comfortable.typeFloor', 'font.size.floor')
    expectRef('density.compact.typeFloor', 'font.size.floor')
  })

  it('closes radius, border, elevation and overlay scales over preserved values', () => {
    for (const [id, value] of [
      ['radius.none', '0px'], ['radius.small', '0.25rem'], ['radius.medium', '0.375rem'],
      ['radius.large', '0.5rem'], ['radius.extraLarge', '0.75rem'], ['radius.2xl', '1rem'],
      ['radius.literal.3', '3px'], ['radius.literal.4', '4px'], ['radius.literal.6', '6px'],
      ['radius.literal.10', '10px'], ['radius.full', '9999px'],
      ['border.width.hairline', '1px'], ['border.style.solid', 'solid'], ['border.style.dashed', 'dashed'],
      ['elevation.raised', '0 1px 2px rgba(0, 0, 0, 0.35)'],
      ['elevation.overlay', '0 8px 24px rgba(0, 0, 0, 0.5)'],
      ['overlay.base', 0], ['overlay.sticky', 10], ['overlay.menu', 20],
      ['overlay.dialog', 50], ['overlay.alert', 60], ['overlay.viewer', 200],
    ] as const) expectValue(id, value)
    for (const id of ['radius.small', 'radius.medium', 'radius.large', 'radius.extraLarge', 'radius.2xl']) {
      expect(byId.get(id)?.responsive, `${id} must continue to follow the 12/14/20px root`).toBe(true)
    }
  })

  it('defines preserved normal motion, reduced motion, and shared loading timing', () => {
    expectValue('motion.duration.fast', '150ms')
    expectValue('motion.duration.normal', '200ms')
    expectValue('motion.duration.slow', '300ms')
    expectValue('motion.duration.entrance', '500ms')
    expectValue('motion.loading.delay', '400ms')
    expectValue('motion.loading.minimumVisible', '300ms')
    expectValue('motion.loading.escalation', '10000ms')
    expectValue('motion.reduced.duration', '0ms')
    expectValue('motion.reduced.distance', '0px')
    expectValue('motion.easing.spring', 'cubic-bezier(0.34, 1.56, 0.64, 1)')
  })

  it('defines the Phosphor icon metric policy without shrinking accessible targets', () => {
    expectValue('icon.family', 'Phosphor')
    expectValue('icon.size.small', '12px')
    expectValue('icon.size.compact', '14px')
    expectValue('icon.size.medium', '16px')
    expectValue('icon.size.control', '18px')
    expectValue('icon.size.large', '20px')
    expectValue('icon.size.prominent', '24px')
    expectValue('icon.size.feature', '28px')
    expectValue('icon.size.display', '32px')
    expectValue('icon.size.hero', '48px')
    expectValue('icon.weight.default', 'regular')
    expectValue('icon.weight.emphasis', 'fill')
    expectRef('icon.target.pointer', 'target.pointer.minimum')
    expectRef('icon.target.touch', 'target.touch.minimum')
  })

  it('keeps references defined and directed from semantic tokens to primitives', () => {
    const rank = { primitive: 0, semantic: 1, component: 2 }
    for (const token of source.tokens) {
      if (!token.ref) continue
      const target = byId.get(token.ref)
      expect(target, `${token.id} references missing ${token.ref}`).toBeDefined()
      expect(rank[target!.layer]).toBeLessThan(rank[token.layer])
    }
  })
})
