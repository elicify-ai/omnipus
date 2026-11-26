// FR-070: independent 44px thresholds for the CSS test instrument itself.
// No application/router import; these cases diagnose token/max parsing alone.
import { expect, it } from 'vitest'
import { parse } from 'postcss'
import { atLeastTouchU2 } from '../../../tests/helpers/settings-u2-css'

it.each([43, 44, 45])('T5-3 instrument — CSS max() at %ipx preserves the independent 44px threshold', (pixels) => {
  const css = parse(`:root { --target-touch-minimum:${pixels}px }`)
  const value = 'max(100%, var(--target-touch-minimum))'
  const literal = `max(100%, ${pixels}px)`
  const declarations: Record<string, string> = {}
  css.walkDecls((declaration) => { declarations[declaration.prop] = declaration.value })
  console.info(`T5-3 max instrument token stream: ${JSON.stringify(declarations)}`)
  expect.soft(atLeastTouchU2(literal, css), `literal ${literal}`).toBe(pixels >= 44)
  expect(atLeastTouchU2(value, css), `token ${literal}`).toBe(pixels >= 44)
})
