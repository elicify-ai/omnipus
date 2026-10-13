import { describe, expect, it } from 'vitest'

import { agentIconInner, monogramInner } from './agentIconArt'

// Dispatch guard contract: both art builders accept only their own keys, never
// Object.prototype properties or unknown art/role keys. Casts deliberately
// exercise invalid runtime inputs without widening the production API.
const INVALID_KEYS = ['__proto__', 'constructor', 'unknown-art-key'] as const

describe('agentIconArt own-key guards', () => {
  it.each(INVALID_KEYS)('agentIconInner rejects figure key %s with its explicit art error', (key) => {
    const draw = () => agentIconInner(key as Parameters<typeof agentIconInner>[0], 'general', 'figure-mask')
    expect(draw).toThrowError(new Error(`AgentIcon has no art for ${key}/general`))
  })

  it.each(INVALID_KEYS)('agentIconInner rejects badge key %s with its explicit art error', (key) => {
    const draw = () => agentIconInner('man', key as Parameters<typeof agentIconInner>[1], 'badge-mask')
    expect(draw).toThrowError(new Error(`AgentIcon has no art for man/${key}`))
  })

  it.each(INVALID_KEYS)('monogramInner rejects badge key %s with its explicit badge error', (key) => {
    const draw = () => monogramInner(key as Parameters<typeof monogramInner>[0], 'monogram-mask', 'R')
    expect(draw).toThrowError(new Error(`AgentIcon has no badge for ${key}`))
  })

  it('keeps valid own-key figure and badge art in the composed SVG', () => {
    const svg = new DOMParser().parseFromString(
      `<svg xmlns="http://www.w3.org/2000/svg">${agentIconInner('man', 'general', 'figure-mask')}</svg>`,
      'image/svg+xml',
    )
    expect(svg.querySelector('parsererror')).toBeNull()
    expect(svg.querySelectorAll('svg > g')).toHaveLength(2)
    expect(svg.querySelector('mask')?.getAttribute('id')).toBe('figure-mask')
    expect(svg.querySelector('svg > g[mask]')?.getAttribute('mask')).toBe('url(#figure-mask)')
    expect(svg.querySelector('svg > g:last-child')?.getAttribute('transform')).toBe('translate(140 140) scale(0.44)')
  })

  it('keeps the valid own-key Monogram badge and the painted initial', () => {
    const svg = new DOMParser().parseFromString(
      `<svg xmlns="http://www.w3.org/2000/svg">${monogramInner('general', 'monogram-mask', 'R')}</svg>`,
      'image/svg+xml',
    )
    expect(svg.querySelector('parsererror')).toBeNull()
    expect(svg.querySelectorAll('svg > g')).toHaveLength(2)
    expect(svg.querySelector('mask')?.getAttribute('id')).toBe('monogram-mask')
    const letter = svg.querySelector('g[mask="url(#monogram-mask)"] > text')
    expect(letter?.getAttribute('data-initial')).toBe('R')
    expect(letter?.textContent).toBe('R')
    expect(svg.querySelector('svg > g:last-child')?.getAttribute('transform')).toBe('translate(140 140) scale(0.44)')
  })
})
