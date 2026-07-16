import { describe, it, expect } from 'vitest'
import { readFileSync } from 'fs'
import { resolve } from 'path'

// Tripwire test for the central focus-ring doctrine (globals.css): ONE
// definition for every focusable element — 1px solid Forge Gold, 2px
// outline-offset, tw-ring shadows zeroed — plus the one sanctioned opt-out
// pair for composite widgets (e.g. the chat textarea, whose card is the
// focus surface instead). If either half of the opt-out regresses to only
// existing on one side, the composer silently loses its focus cue — that's
// the exact past bug this guards against.
const cssPath = resolve(process.cwd(), 'src/styles/globals.css')
const cssContent = readFileSync(cssPath, 'utf-8')

const chatScreenPath = resolve(process.cwd(), 'src/components/chat/ChatScreen.tsx')
const chatScreenContent = readFileSync(chatScreenPath, 'utf-8')

describe('globals.css — central focus-ring doctrine', () => {
  it('defines the central :focus-visible:not([data-no-focus-ring]) rule with 1px solid accent + 2px offset', () => {
    const ruleMatch = cssContent.match(
      /:focus-visible:not\(\[data-no-focus-ring\]\)\s*\{([^}]+)\}/,
    )
    expect(ruleMatch, 'central focus-visible rule not found in globals.css').not.toBeNull()
    const rule = ruleMatch![1]
    expect(rule).toContain('1px solid var(--color-accent)')
    expect(rule).toContain('outline-offset: 2px')
  })

  it('defines the [data-no-focus-ring] opt-out selector', () => {
    expect(cssContent).toContain('[data-no-focus-ring]')
  })

  it('ChatScreen.tsx carries BOTH halves of the composer opt-out — data-no-focus-ring on the textarea AND the focus-within:border replacement cue on its card', () => {
    // Only one half existing (the textarea opting out of the ring without a
    // replacement cue on the card, or vice versa) is the exact past bug:
    // the composer would render with no visible focus indicator at all.
    expect(chatScreenContent).toContain('data-no-focus-ring')
    expect(chatScreenContent).toContain('focus-within:border-[var(--color-accent)]')
  })
})
