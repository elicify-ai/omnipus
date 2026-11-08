/**
 * Page-title token — W2-1 contract §2.4 "Token change" (R40, founder
 * 2026-10-06: "value is 21 px").
 *
 * RED on 69aa07d9a: the token is 1.875rem today (≈26px at the 14px root).
 * The contract requires 1.5rem (= 21px at --font-root-default: 14px, equal
 * to today's text-2xl). The value is asserted at its two SOURCES — the
 * foundations.json token entry and the generated CSS — never as a literal
 * inside a component: src/components/ui/page-header.test.tsx pins that the
 * h1 consumes `var(--type-page-title-size)` instead.
 *
 * G9: `--font-root-default` is 14px (asserted here too, as the guard for the
 * 1.5rem→21px arithmetic).
 */
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const root = resolve(import.meta.dirname, '../..')
const foundations = JSON.parse(
  readFileSync(resolve(root, 'design-system/tokens/foundations.json'), 'utf8'),
) as { tokens: Array<{ id: string; css: string; value: string }> }
const generatedCss = readFileSync(resolve(root, 'src/styles/tokens.generated.css'), 'utf8')

describe('page-title token (W2-1 §2.4)', () => {
  it('foundations.json pins font.size.pageTitle at 1.5rem', () => {
    const token = foundations.tokens.find((t) => t.id === 'font.size.pageTitle')
    expect(token?.css).toBe('--font-size-page-title')
    expect(token?.value).toBe('1.5rem')
  })

  it('the generated CSS exposes --font-size-page-title: 1.5rem through the type alias', () => {
    expect(generatedCss).toContain('--font-size-page-title: 1.5rem;')
    // The alias indirection stays intact — consumers read --type-page-title-size.
    expect(generatedCss).toMatch(/--type-page-title-size:\s*var\(--font-size-page-title\)/)
  })

  it('the root stays 14px, so 1.5rem resolves to the founder’s 21px', () => {
    // 1.5rem × 14px = 21px (R40). If the root default ever changes, this
    // arithmetic must be re-derived — that is why it is asserted, not assumed.
    expect(generatedCss).toContain('--font-root-default: 14px;')
  })
})
