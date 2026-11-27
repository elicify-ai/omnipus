/*
 * U8-A follow-up / FR-070: the Connect sheet's sign-in method radios get the
 * same coarse-pointer tap area as the Sandbox radios (globals.css
 * [data-radio-target]), and that area never reaches into the neighbouring
 * card. jsdom has no layout or hit testing, so the overlap check is a model
 * computed from the shipped classes and token values, stated here:
 *   glyph centre from the card's top = border + padding-top + wrapper
 *   margin-top + half the native radio glyph (13 px, as measured in Chromium
 *   by the hands-on lanes). Part 47 reads the rule's size/offset/translation
 *   to derive both edges, requires its centre to equal the glyph centre, and
 *   bounds its reach by the gap above and the card's OWN minimum-height class
 *   below. No constant card height or exact CSS spelling. Physical grids stay
 *   a browser check.
 */
import { describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { AuthMethodControl } from './AuthMethodControl'
import { pseudoTouchU2, stylesheetU2 } from '../../../tests/helpers/settings-u2-css'
import { expectRadioReachU47, fineBeforeU47 } from '../../../tests/helpers/settings-u47-radio'

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
    for (const radio of native) {
      const card = radio.labels?.[0] as HTMLElement
      const target = radio.parentElement as HTMLElement
      expect(target, `${radio.value}: wrapper inside its own card`).not.toBe(card)
      expect(card.contains(target)).toBe(true)
      expect(target.classList.contains('relative')).toBe(true)
      expect(pseudoTouchU2(css, target), `${radio.value}: 44 px coarse tap area`).toBe(true)

      expect(fineBeforeU47(css, target), `${radio.value}/U8-A: fine pointer has no added tap region`).toBe(false)
      expectRadioReachU47(css, target, card, radio.value)
    }
  })
})
