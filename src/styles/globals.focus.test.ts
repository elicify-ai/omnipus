import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'fs'
import { resolve, join, relative } from 'path'

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

describe('focus-visible:outline / focus-visible:ring — repo-wide tripwire', () => {
  // Doctrine closure (OBS-1 / T8): the central `:focus-visible:not([data-no-focus-ring])`
  // rule above owns EVERY focusable element's focus cue. A per-component
  // `focus-visible:outline*` or `focus-visible:ring*` Tailwind utility class
  // is banned — at best it's inert (the central rule's selector specificity
  // wins), at worst it silently overrides the doctrine for that one element.
  // Several such classes were found and removed across the app in a prior
  // audit; this scan makes the fix permanent instead of relying on review
  // catching the next one. Test files are excluded deliberately — they
  // legitimately reference these strings as literal assertion targets (e.g.
  // `expect(el.className).not.toContain('focus-visible:ring')`), which is
  // the tripwire working as intended, not a violation of it.
  const FORBIDDEN = ['focus-visible:outline', 'focus-visible:ring'] as const
  const SRC_ROOT = resolve(process.cwd(), 'src')

  function isScannableSourceFile(path: string): boolean {
    if (!/\.tsx?$/.test(path)) return false
    if (/\.(test|spec)\.tsx?$/.test(path)) return false
    return true
  }

  function walk(dir: string, out: string[]): void {
    for (const entry of readdirSync(dir)) {
      const full = join(dir, entry)
      const stat = statSync(full)
      if (stat.isDirectory()) {
        walk(full, out)
      } else if (isScannableSourceFile(full)) {
        out.push(full)
      }
    }
  }

  it('appears in zero non-test .ts/.tsx files under src/', () => {
    const files: string[] = []
    walk(SRC_ROOT, files)
    expect(files.length).toBeGreaterThan(0) // sanity: the walk actually found source files

    const offenders: string[] = []
    for (const file of files) {
      const content = readFileSync(file, 'utf-8')
      for (const needle of FORBIDDEN) {
        if (content.includes(needle)) {
          offenders.push(`${relative(SRC_ROOT, file)} — contains "${needle}"`)
        }
      }
    }

    expect(
      offenders,
      `Banned per-component focus-visible override(s) found — the central focus-ring rule ` +
        `(globals.css) must be the only source of a focus-visible outline/ring:\n${offenders.join('\n')}`,
    ).toEqual([])
  })
})
