// LibraryCodePreview.shiki.test.tsx — the ONE suite around this surface that
// runs the REAL react-shiki, and the one that can see the 2026-09-05 view-kinds
// UAT's D2.
//
// WHY IT EXISTS. Every other test here mocks react-shiki at its module boundary
// (BasePreview.test.tsx says so in its own header) and the mock renders
// `children` verbatim, whatever `language` it was handed. That mock is right for
// wiring tests — and it is exactly why D2 was invisible to them:
// `shikiLanguageFor('Damaged.base')` returned the string `"base"`, Shiki has no
// `base` grammar, and in a real browser the pane rendered NOTHING behind a
// `base` language chip. The mock rendered the file perfectly.
//
// AND WHY THE LANGUAGE ASSERTION IS THE LOAD-BEARING ONE. Under jsdom the real
// react-shiki degrades to plain text for an unknown grammar, so "the bytes are
// on screen" passes here even with the defect present — measured, not assumed,
// before this file was written. The browser does not degrade: Playwright
// (`tests/e2e-viewkinds/view-kinds.spec.ts`, tests 5 and 6) saw the raw pane's
// entire text content as the four characters `base`. So this file asserts BOTH:
// the bytes render (the property a reader cares about, which must never
// regress), AND that the grammar name handed to Shiki is one Shiki actually
// holds (the property that can fail in this environment, and does fail on the
// pre-fix code).

import { describe, it, expect } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { bundledLanguages } from 'shiki'
import { ShikiCodeBlock } from '@/components/chat/markdown-shared'
import { shikiLanguageFor } from './libraryLanguages'

const BASE_FILE = `filters:
  and:
    - type == "recipe"
views:
  - type: table
    name: Damaged
`

/** Shiki resolves these three internally without a bundled grammar; anything
 * else it is handed must be a real key of its own registry. */
const SHIKI_PLAIN_TEXT = new Set(['text', 'plaintext', 'txt'])

function shikiKnows(grammar: string): boolean {
  return SHIKI_PLAIN_TEXT.has(grammar) || Object.prototype.hasOwnProperty.call(bundledLanguages, grammar)
}

function renderRawPane(filename: string, content: string) {
  return render(
    <div data-testid="raw-pane">
      <ShikiCodeBlock language={shikiLanguageFor(filename)} code={content} />
    </div>,
  )
}

describe('the raw-file pane, through the real Shiki highlighter', () => {
  it('hands Shiki a grammar it actually has for a .base file, and renders the file’s own bytes', async () => {
    renderRawPane('Damaged.base', BASE_FILE)
    const pane = screen.getByTestId('raw-pane')

    // The property a reader actually cares about: the bytes are there. An
    // assertion that stops at "the pane appeared" cannot tell a working escape
    // hatch from an empty one — that is how the Playwright test first passed
    // against a blank pane. Highlighting is async, so this is the wait.
    await waitFor(() => {
      expect(pane.textContent).toContain('name: Damaged')
    })
    expect(pane.textContent).toContain('type == "recipe"')

    // THE ASSERTION D2 FAILS. Pre-fix this label reads `base`, which is not a
    // grammar Shiki holds — and a grammar Shiki does not hold is the whole of
    // the defect: the browser renders an empty surface for it.
    const label = pane.querySelector('#language-label')?.textContent ?? ''
    expect(shikiKnows(label), `Shiki was handed the grammar "${label}", which it does not have`).toBe(true)
  }, 20000)

  it('still highlights an extension Shiki does know, so the fallback is not a blanket give-up', async () => {
    renderRawPane('recipe.yaml', BASE_FILE)
    const pane = screen.getByTestId('raw-pane')

    await waitFor(() => {
      expect(pane.textContent).toContain('name: Damaged')
    })
    expect(pane.querySelector('#language-label')?.textContent).toBe('yaml')
  }, 20000)
})
