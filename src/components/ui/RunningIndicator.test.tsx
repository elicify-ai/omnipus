// RunningIndicator.test.tsx — RED pack for side-panel-shell-spec.md Wave 3,
// FR-022 (NEW requirement, SP-41) — the COMPONENT half:
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
// RED↔GREEN contracts declared by this pack:
//   1. Component path: `src/components/ui/RunningIndicator.tsx`, exporting
//      `RunningIndicator` — the components/ui directory is what the
//      design-system catalog indexes (design-system/catalog.json entries key
//      on `source` paths under src/components/ui/), so "a catalogued
//      omnipus-design-system component" means: present in that catalog.
//   2. Minimal props: `{ tokens: number }` — the token count the indicator
//      displays. (How a task's run/session data reaches this prop is GREEN
//      wiring FR-022 leaves open; the surfacing pack covers placement.)
//   3. Rendering: the formatted token count is visible, and the spinning
//      icon uses the repo's established treatment (a spinning svg — the
//      `animate-spin` convention chat's own spinners use).
//
// RED evidence (2026-10-04): grepped the whole repo for a running-indicator
// component — nothing under src/components/ui/ renders a spinner+token
// treatment, and design-system/catalog.json has no such entry. EVERY test
// below fails today; the import failures are the BLOCKED analogue this
// role mandates for missing implementations (loud, naming the missing
// module — never a skip).
//
// Surfaces (which views render it for which tasks) live in
// RunningIndicator.surfaces.test.tsx.

import type { ReactElement } from 'react'
import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { formatTokens } from '@/lib/formatTokens'

const COMPONENT_PATH = '@/components/ui/RunningIndicator'
const CATALOG_SOURCE = 'src/components/ui/RunningIndicator.tsx'

/** Dynamic import so each test fails INDEPENDENTLY with the mandated BLOCKED
 * shape (a static import would fail the whole file at collection, hiding the
 * individual assertions behind one resolution error). */
async function loadComponent() {
  try {
    // @vite-ignore: the missing module must fail at RUN time (this test's
    // own BLOCKED error), not fail the whole file at transform time.
    return await import(/* @vite-ignore */ COMPONENT_PATH)
  } catch {
    throw new Error(
      `BLOCKED: ${CATALOG_SOURCE} not implemented — required by FR-022 / SP-41 (§10 Wave 3: ONE catalogued running indicator)`,
    )
  }
}

describe('RunningIndicator — FR-022: it is a catalogued design-system component', () => {
  it('exists under src/components/ui and is entered in design-system/catalog.json', async () => {
    const mod = (await loadComponent()) as { RunningIndicator?: unknown }
    expect(typeof mod.RunningIndicator).toBe('function')

    const catalog = (await import('../../../design-system/catalog.json')).default as {
      entries: ReadonlyArray<{ source: string }>
    }
    const entry = catalog.entries.find((candidate) => candidate.source === CATALOG_SOURCE)
    expect(entry, `design-system/catalog.json must contain an entry with source "${CATALOG_SOURCE}"`).toBeDefined()
  })
})

describe('RunningIndicator — FR-022: the chat running treatment (spinner + token count)', () => {
  it.each([
    { tokens: 0, note: 'a task that has just started' },
    { tokens: 999, note: 'under the formatter\'s 1k boundary' },
    { tokens: 1000, note: 'at the formatter\'s 1k boundary' },
    { tokens: 44_000, note: 'the kilo range' },
    { tokens: 1_500_000, note: 'the mega range' },
  ])('renders the spinning icon plus the token count for $note ($tokens tokens)', async ({ tokens }) => {
    const { RunningIndicator } = (await loadComponent()) as {
      RunningIndicator: (props: { tokens: number }) => ReactElement
    }
    const mounted = render(<RunningIndicator tokens={tokens} />)
    // The token count is the CHAT treatment's number: the shared formatter's
    // exact output ("44.0k" / "1.2M" / as-is under 1000), not an ad-hoc one.
    expect(mounted.container.textContent).toContain(formatTokens(tokens))
  })

  it('renders a spinning icon (the chat treatment, not a static badge)', async () => {
    const { RunningIndicator } = (await loadComponent()) as {
      RunningIndicator: (props: { tokens: number }) => ReactElement
    }
    const mounted = render(<RunningIndicator tokens={44_000} />)
    const spinning = mounted.container.querySelector('svg.animate-spin')
    expect(spinning, 'the indicator must carry the repo\'s spinning-icon treatment (animate-spin svg)').not.toBeNull()
  })
})
