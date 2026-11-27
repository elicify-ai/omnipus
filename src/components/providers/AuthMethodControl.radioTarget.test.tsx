/*
 * U8-A follow-up / FR-070: the Connect sheet's sign-in method radios get the
 * same coarse-pointer tap area as the Sandbox radios (globals.css
 * [data-radio-target]), and that area never reaches into the neighbouring
 * card. jsdom has no layout or hit testing, so the overlap check is a model
 * computed from the shipped classes and token values, stated here:
 *   glyph centre from the card's top = border + padding-top + wrapper
 *   margin-top + half the native radio glyph (13 px, as measured in Chromium
 *   by the hands-on lanes); the tap box reaches (touch minimum / 2 - centre)
 *   above the card, which must stay inside the gap between cards, and
 *   (centre + touch minimum / 2) below the top, which must stay inside the
 *   card's coarse minimum height. Physical grids stay a browser check.
 */
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { Root } from 'postcss'
import { AuthMethodControl } from './AuthMethodControl'
import { pseudoTouchU2, stylesheetU2 } from '../../../tests/helpers/settings-u2-css'

const GLYPH = 13

function token(css: Root, name: string): number {
  let value: string | undefined
  css.walkDecls(name, (declaration) => { value ??= declaration.value })
  const px = /^(\d+(?:\.\d+)?)px$/.exec(value?.trim() ?? '')
  if (!px) throw new Error(`token ${name} is not a px value: ${value}`)
  return Number(px[1])
}

/** The px value of the first class `<prefix>[var(--token)]` on an element. */
function spacing(css: Root, element: Element, prefixes: string[]): number {
  for (const name of element.classList) {
    for (const prefix of prefixes) {
      const match = new RegExp(`^${prefix.replace(/[-[\]]/g, '\\$&')}\\[var\\((--[\\w-]+)\\)\\]$`).exec(name)
      if (match) return token(css, match[1])
    }
  }
  return 0
}

describe('U8-A sign-in method radios in the Connect sheet', () => {
  it('each radio has the coarse tap area inside its own card, never reaching the next card', async () => {
    render(
      <AuthMethodControl
        companyName="OpenAI"
        signInOptions={[
          { providerId: 'openai-chatgpt', label: 'ChatGPT' },
          { providerId: 'openai-codex', label: 'Codex' },
        ]}
      />,
    )
    const radios = screen.getAllByRole('radio') as HTMLInputElement[]
    const native = radios.filter((radio) => radio.matches('input[type="radio"]'))
    expect(native.length).toBeGreaterThanOrEqual(2)
    const css = await stylesheetU2()
    const touch = token(css, '--target-touch-minimum')
    for (const radio of native) {
      const card = radio.labels?.[0] as HTMLElement
      const target = radio.parentElement as HTMLElement
      expect(target, `${radio.value}: wrapper inside its own card`).not.toBe(card)
      expect(card.contains(target)).toBe(true)
      expect(target.classList.contains('relative')).toBe(true)
      expect(pseudoTouchU2(css, target), `${radio.value}: 44 px coarse tap area`).toBe(true)

      const border = card.classList.contains('border') ? 1 : 0
      const centre = border + spacing(css, card, ['pt-', 'py-', 'p-'])
        + spacing(css, target, ['mt-', 'my-', 'm-']) + GLYPH / 2
      const gap = spacing(css, card.parentElement as Element, ['gap-y-', 'gap-'])
      const above = touch / 2 - centre
      const below = centre + touch / 2
      expect(above, `${radio.value}: the tap box reaches ${above}px above the card; the gap is ${gap}px`).toBeLessThan(gap)
      expect(below, `${radio.value}: the tap box ends ${below}px below the card top; the coarse card is at least ${touch}px`).toBeLessThanOrEqual(touch)
    }
  })
})
