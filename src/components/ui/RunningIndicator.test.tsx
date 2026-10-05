// RunningIndicator.test.tsx — wave-3 join pack for side-panel-shell-spec.md
// Wave 3, FR-022 (NEW requirement, SP-41) — the COMPONENT half:
//
//   "Every running task surfaced in the Tasks panel's Board, List and Graph
//    views, and in the Plans band, MUST show ONE standard 'running'
//    indicator — the same spinning-icon-plus-token-count treatment already
//    used in chat. This indicator MUST be a catalogued
//    omnipus-design-system component; the implementing lead extracts chat's
//    existing indicator into that catalogued component if it is not already
//    one, rather than building a second, divergent copy."  (FR-022)
//
// Oracle sources: spec §13 FR-022 / SP-41, §10 Wave 3 last table row.
// Token-count formatting comes from the repo's own shared formatter
// (src/lib/formatTokens.ts: "44.0k / 1.2M, under 1000 as-is") — the same
// vocabulary chat's indicator uses — never from observed output.
//
// JOIN STATUS (2026-10-05, work/side-panel-wave3-join-20261005): the slice
// delivered src/components/ui/RunningIndicator.tsx (catalogued) plus the six
// tests in the first describe block below. Numeric cases are retained;
// only the obsolete 3.4s reduced-motion expectation is repinned to the
// standing static-motion policy under the coordinator's explicit ruling.
// Their original GREEN claims are not re-labelled independent CHECK evidence.
// The join adds the 8773803cf RED
// pack's two remaining oracles the delivered file did not pin:
//   1. the design-system CATALOG entry (FR-022's "MUST be a catalogued
//      omnipus-design-system component" — the entry, not just the file), and
//   2. the formatTokens boundary sweep (0 / 999 / 1k / kilo / mega), derived
//      from the shared formatter's documented boundaries.
// Surfaces (which views render it for which tasks) live in
// RunningIndicator.surfaces.test.tsx — still RED in this join: the indicator
// is not yet wired into Board/List/Graph/Plans (production gap, FR-022).

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import type { ReactElement } from 'react'
import { RunningIndicator } from '@/components/ui/RunningIndicator'
import { formatTokens } from '@/lib/formatTokens'

describe('RunningIndicator', () => {
  it('renders the short "{n} tok" label for values under 1000', () => {
    render(<RunningIndicator tokens={44} />)
    expect(screen.getByText('44 tok')).toBeInTheDocument()
  })

  it('formats large counts through formatTokens ("4.4k tok")', () => {
    render(<RunningIndicator tokens={4400} />)
    expect(screen.getByText('4.4k tok')).toBeInTheDocument()
  })

  it('is a polite status region whose accessible name carries the full word "tokens"', () => {
    render(<RunningIndicator tokens={44} />)
    const status = screen.getByRole('status')
    expect(status).toHaveAttribute('aria-label', '44 tokens')
    expect(status).toHaveAttribute('title', 'Running')
  })

  it('spins while streaming and disables animation under the reduced-motion policy', () => {
    // Supersession authorised by coordinator 2026-10-05: the wireframe's
    // 3.4s mockup style was not an approved SP decision. Existing design-
    // system/browser.spec.ts reduced-motion policy requires inactive motion
    // (<=0.01s). The actual arrow stays rendered and animated normally;
    // reduced preference stops animation instead of hiding the status.
    const { container } = render(<RunningIndicator tokens={44} />)
    const icon = container.querySelector('svg')
    expect(icon).not.toBeNull()
    expect(icon).toHaveClass('animate-spin')
    expect(icon).toHaveClass('motion-reduce:animate-none')
    expect(icon).not.toHaveClass('motion-reduce:[animation-duration:3.4s]')
    expect(screen.getByRole('status')).toBeInTheDocument()
  })

  it('holds the spinner still when streaming is false', () => {
    const { container } = render(<RunningIndicator tokens={44} streaming={false} />)
    const icon = container.querySelector('svg')
    expect(icon).not.toHaveClass('animate-spin')
    expect(icon).toHaveAttribute('aria-hidden', 'true')
  })

  it('renders only the spinner and the count — nothing else (SP-41 treatment)', () => {
    const { container } = render(<RunningIndicator tokens={44} />)
    expect(container.querySelectorAll('svg')).toHaveLength(1)
    expect(container.textContent).toBe('44 tok')
  })
})

describe('RunningIndicator — FR-022: it is a catalogued design-system component', () => {
  it('is entered in design-system/catalog.json at its source path', async () => {
    // Oracle: FR-022 — "MUST be a catalogued omnipus-design-system
    // component". The catalog (design-system/catalog.json) keys entries on
    // `source` paths under src/components/ui/; a file that is not entered is
    // an uncatalogued copy, exactly the divergence FR-022 forbids.
    const catalog = (await import('../../../design-system/catalog.json')).default as {
      entries: ReadonlyArray<{ source: string }>
    }
    const entry = catalog.entries.find(
      (candidate) => candidate.source === 'src/components/ui/RunningIndicator.tsx',
    )
    expect(
      entry,
      'design-system/catalog.json must contain an entry with source "src/components/ui/RunningIndicator.tsx"',
    ).toBeDefined()
  })
})

describe('RunningIndicator — FR-022: the chat running treatment across the formatter boundaries', () => {
  // Boundaries of the shared formatter (src/lib/formatTokens.ts: under 1000
  // as-is, then "{n/1000, 1dp}k", then "{n/1e6, 1dp}M") — min, min-1/min+1
  // and one value per range, expected values derived from the formatter's
  // documented contract, never from observed output. PI3 note: the count
  // treatment now belongs to the CHAT-style usage only (founder overlay,
  // PANEL-INDICATOR-DECISION-20261005.md PI3 — task surfaces show the
  // spinner ONLY); these retained cases pin the OPTIONAL-token behaviour the
  // component keeps for it.
  it.each([
    { tokens: 0, expected: '0 tok', note: 'a task that has just started (formatter min)' },
    { tokens: 999, expected: '999 tok', note: '1 under the formatter k boundary' },
    { tokens: 1000, expected: '1.0k tok', note: 'at the formatter k boundary' },
    { tokens: 44_000, expected: '44.0k tok', note: 'the kilo range' },
    { tokens: 1_500_000, expected: '1.5M tok', note: 'the mega range' },
  ])('renders spinner + "$expected" for $note ($tokens tokens)', ({ tokens, expected }) => {
    // The count is the CHAT treatment's number: the shared formatter's exact
    // output, formatted inline ("{formatTokens(tokens)} tok" — the label
    // shortening the wireframe's provenance note approves for narrow
    // card/row widths).
    const mounted = render(<RunningIndicator tokens={tokens} />)
    expect(mounted.container.textContent).toContain(expected)
    expect(mounted.container.textContent).toContain(formatTokens(tokens))
  })
})

function assertSpinnerOnlyStatus(status: HTMLElement) {
  expect(status).toHaveAccessibleName('Running')
  expect(status.querySelectorAll('svg')).toHaveLength(1)
  expect(status.querySelector('svg')).toHaveClass('animate-spin')
  expect(status.textContent?.trim(), 'PI3: no count or placeholder is permitted').toBe('')
}

describe('RunningIndicator — PI3 spinner-only mode (task surfaces: NO token count)', () => {
  // PI3 (founder Q2 = A): Board/List/Graph/Plans surfaces show the animated
  // arrow WITHOUT task token counts — no fake values, no placeholders. The
  // component must therefore render a spinner-only variant whose accessible
  // identity is "Running", never a count. RED today: the delivered component
  // has no tokens-less mode (tokens is required and the count is always
  // rendered) — this test fails naming the missing mode until production
  // adds it. The `as unknown as` cast is the declared BLOCKED seam, keeping
  // typecheck honest while the mode is missing.
  it('renders with NO token text: role=status, animated svg, accessible name "Running"', async () => {
    const mod = (await import('@/components/ui/RunningIndicator')) as unknown as {
      RunningIndicator: (props?: { tokens?: number }) => ReactElement
    }
    // Missing mode is a loud RED, never a type suppression or skipped test.
    let mounted: ReturnType<typeof render>
    try {
      mounted = render(<mod.RunningIndicator />)
    } catch (error) {
      throw new Error('BLOCKED: src/components/ui/RunningIndicator.tsx spinner-only mode is not implemented — required by PI1/PI3', { cause: error })
    }
    const status = mounted.getByRole('status', { name: 'Running' })
    assertSpinnerOnlyStatus(status)
  })

  it('instrument: injecting even "0 tok" fails the SAME spinner-only assertion', () => {
    // Test-instrument proof only, not a production mutation claim: first the
    // control satisfies the exact assertion, then a leaked placeholder dies.
    const mounted = render(
      <span role="status" aria-label="Running"><svg className="animate-spin" /></span>,
    )
    const status = mounted.getByRole('status', { name: 'Running' })
    expect(() => assertSpinnerOnlyStatus(status)).not.toThrow()
    status.appendChild(document.createTextNode('0 tok'))
    expect(() => assertSpinnerOnlyStatus(status)).toThrow(/no count or placeholder/)
  })
})
