/*
 * U8-A / FR-070: at 390 coarse, a 44 x 44 box centred on each Sandbox radio
 * glyph (13 x 13, close to its option card's edge) reached past the card, and
 * about 420 of 1936 points selected nothing. Every Sandbox radio sits in a
 * wrapper inside its own option label whose ::before grows the coarse-pointer
 * tap area to the touch minimum around the glyph; a fine pointer gets no
 * such region. jsdom has no hit testing: this reads the shipped CSS rule
 * that matches the wrapper (the same reading the Switch expander gets); the
 * physical elementFromPoint grid stays a browser check.
 */
import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, waitFor } from '@testing-library/react'
import { jsonG1, sectionG1, transportG1 } from '../../../tests/helpers/settings-gate-g1'
import { pseudoTouchU2, stylesheetU2 } from '../../../tests/helpers/settings-u2-css'
import { expectRadioReachU47, fineBeforeU47 } from '../../../tests/helpers/settings-u47-radio'

afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

it('U8-A: every Sandbox radio has a coarse-only touch-sized tap area inside its own option', async () => {
  const t = transportG1()
  t.on('GET', '/security/sandbox-config', () => jsonG1({
    ...t.defaults['/security/sandbox-config'] as object, workspace_path_guard: true,
  }))
  const { client } = sectionG1('sandbox')
  await waitFor(() => expect(t.calls.some((call) => call.path === '/security/sandbox-config')).toBe(true))
  await waitFor(() => expect(client.isFetching()).toBe(0))
  const radios = [...document.querySelectorAll<HTMLInputElement>('input[type="radio"]')]
  const names = new Set(radios.map((radio) => radio.name))
  expect([...names].sort()).toEqual(expect.arrayContaining(['sandbox-mode', 'workspace-limit']))
  const css = await stylesheetU2()
  for (const radio of radios) {
    const label = radio.labels?.[0]
    const target = radio.parentElement as HTMLElement
    expect(label, radio.getAttribute('aria-label') ?? radio.value).toBeDefined()
    expect(target, `${radio.name}/${radio.value}: wrapper inside its own option`).not.toBe(label)
    expect(label?.contains(target)).toBe(true)
    expect(target.classList.contains('relative'), `${radio.name}/${radio.value}`).toBe(true)
    expect(pseudoTouchU2(css, target), `${radio.name}/${radio.value}: coarse tap area`).toBe(true)
    expect(fineBeforeU47(css, target), `${radio.name}/${radio.value}: fine pointer unchanged`).toBe(false)
    expectRadioReachU47(css, target, label!, `${radio.name}/${radio.value}`)
  }
})
