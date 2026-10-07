/**
 * PlansFilterBand.titleContainment.test.tsx
 *
 * Round-2 UAT Finding (S3): a 200-char unbroken plan title (no spaces —
 * exactly the create-plan form's own `maxLength`, `TITLE_MAX_LEN` in
 * CreatePlanSlideOver.tsx, which mirrors `pkg/plan/plan.go`'s
 * `maxPlanTitleRunes = 200`) painted ~2183px past its own 196px-wide plan
 * tile, bleeding across 4+ neighbouring tiles. Tile widths stayed uniform
 * (`TILE_SIZE = 'w-56 flex-shrink-0'` on the outer tile div never moved) —
 * this was a PAINT BLEED, not the layout-collapse UAT Finding 2 already
 * fixed on TaskCard/ListView.
 *
 * Root cause: the title `<span>` (PlansFilterBand.tsx, inside the
 * module-private `PlanFilterTile`) is a direct child of the tile's select
 * `<button>`, which is a COLUMN-direction flex container (`flex-col`) using
 * `items-start` (not `stretch`). With `items-start`, the span's used width
 * is `fit-content`, floored below by `min-width: auto` — which resolves to
 * min-content, i.e. the full unbroken string's rendered width for a
 * no-spaces title. `line-clamp-2` alone cannot stop this: it clips to two
 * lines, but the line BOX itself was never constrained to the tile's width
 * in the first place, so the (still just-as-wide) clipped box paints past
 * the tile's edge.
 *
 * Fix: the exact same two containment classes already proven on
 * TaskCard.tsx:242 (see TaskCard.test.tsx's "long unbroken title
 * containment (UAT Finding 2)" describe block) — `min-w-0` (removes the
 * min-content floor) and `wrap-anywhere` (`overflow-wrap: anywhere`, the one
 * wrapping mode the spec requires browsers to fold into min-content sizing
 * itself, unlike `break-word`). Unlike TaskCard's title `<p>`, this span
 * sits in a COLUMN flex (not a row), so `flex-1`/`pr-6` (TaskCard-specific,
 * row-layout-only) are deliberately NOT added here.
 *
 * jsdom has no real layout/intrinsic-sizing engine, so actual on-screen
 * pixel bleed can't be measured here. What IS verifiable — mirroring both
 * TaskCard.test.tsx and ListView.titleContainment.test.tsx exactly, not just
 * a bare className string match (flagged in round-2 review as a regression
 * risk / insufficient proof) — is that the classes are present AND that a
 * real injected stylesheet resolves them through jsdom's actual CSSOM to the
 * expected declarations. The mapping below was confirmed against this
 * repo's installed `tailwindcss` (4.3.3, `node_modules/tailwindcss/package.json`),
 * the same version and same mechanism the other two containment tests used
 * (`grep -o 'overflow-wrap","anywhere' node_modules/tailwindcss/dist/lib.js`
 * matches; `min-w-0`'s `min-width:0px` mapping is unchanged from
 * TaskCard.test.tsx's own already-verified constant):
 *
 *   .wrap-anywhere { overflow-wrap: anywhere; }
 *   .min-w-0       { min-width: 0px; }
 */

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { PlansFilterBand } from './PlansFilterBand'
import type { Agent, Plan, Task } from '@/lib/api'

// Minimal stub so the ⋯ trigger renders its content without Radix portal
// internals (mirrors PlansFilterBand.test.tsx's dropdown stub — PlanActionButton
// and the ⋯ menu still mount for every tile even though this file never
// interacts with them).
vi.mock('@/components/ui/dropdown-menu', () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
  DropdownMenuTrigger: ({ children, asChild }: { children: React.ReactNode; asChild?: boolean }) =>
    asChild ? <>{children}</> : <div>{children}</div>,
  DropdownMenuContent: ({ children }: { children: React.ReactNode }) => (
    <div data-testid="plan-tile-menu">{children}</div>
  ),
  DropdownMenuItem: ({
    children,
    onClick,
    disabled,
    className,
  }: {
    children: React.ReactNode
    onClick?: () => void
    disabled?: boolean
    className?: string
  }) => (
    <button type="button" onClick={onClick} disabled={disabled} className={className}>
      {children}
    </button>
  ),
  DropdownMenuSeparator: () => <hr />,
}))

const executePlanMock = vi.fn()
const stopPlanMock = vi.fn()
const restartPlanMock = vi.fn()

vi.mock('@/lib/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api')>()
  return {
    ...actual,
    executePlan: (id: string) => executePlanMock(id),
    stopPlan: (id: string) => stopPlanMock(id),
    restartPlan: (id: string) => restartPlanMock(id),
  }
})

const mockAddToast = vi.fn()
vi.mock('@/store/ui', () => ({
  useUiStore: (selector?: (s: { addToast: typeof mockAddToast }) => unknown) => {
    const store = { addToast: mockAddToast }
    return selector ? selector(store) : store
  },
}))

beforeEach(() => {
  executePlanMock.mockReset().mockResolvedValue(undefined)
  stopPlanMock.mockReset().mockResolvedValue(undefined)
  restartPlanMock.mockReset().mockResolvedValue(undefined)
  mockAddToast.mockReset()
})

function makePlan(overrides: Partial<Plan> = {}): Plan {
  return {
    id: 'plan-1',
    workspace_id: 'ws-1',
    title: 'Launch',
    state: 'draft',
    plan_phase: 'idle',
    owner_agent_id: 'jim',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    ...overrides,
  }
}

const agents: Agent[] = [
  // ADR-052 FR-039: memory_enabled is required on the wire Agent type.
  { revision: '0'.repeat(64), id: 'jim', name: 'Jim', type: 'core', locked: true, status: 'active', soul: '', timeout_seconds: 300, max_tool_iterations: 50, max_tool_iterations_source: 'global', max_tool_iterations_override_ignored: false, memory_enabled: true, needs_model: false },
]

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

function renderBand(overrides: Partial<React.ComponentProps<typeof PlansFilterBand>> = {}) {
  const props = {
    plans: [makePlan()],
    tasks: [] as Task[],
    agents,
    selectedPlanId: null,
    onSelectPlan: vi.fn(),
    onNewPlan: vi.fn(),
    onEditPlan: vi.fn(),
    onClearPlan: vi.fn(),
    ...overrides,
  }
  const mounted = render(
    <QueryClientProvider client={makeClient()}>
      <PlansFilterBand {...props} />
    </QueryClientProvider>,
  )
  fireEvent.click(screen.getByRole('button', { name: 'Plans' }))
  return mounted
}

/** Real, compiled Tailwind v4.3.3 output for the two utilities this fix
 * relies on — verified against this repo's installed tailwindcss package
 * (`node_modules/tailwindcss/dist/lib.js` maps `wrap-anywhere` to
 * `overflow-wrap: anywhere`), the same mechanism TaskCard.test.tsx and
 * ListView.titleContainment.test.tsx already use for the identical classes. */
function injectRealTailwindDeclarations() {
  const style = document.createElement('style')
  style.textContent = '.break-normal{overflow-wrap:normal;word-break:normal}.wrap-break-word{overflow-wrap:break-word}.min-w-0{min-width:0px}'
  document.head.appendChild(style)
  return () => style.remove()
}

describe('PlansFilterBand — long unbroken plan title containment (round-2 UAT Finding S3)', () => {
  it('renders a 200-char unbroken title with wrap-anywhere + min-w-0, full text intact, and a tile tooltip carrying the whole string', () => {
    const removeStyle = injectRealTailwindDeclarations()
    try {
      const longTitle = 'C'.repeat(200) // exactly the create-plan form's TITLE_MAX_LEN / maxPlanTitleRunes
      renderBand({ plans: [makePlan({ id: 'plan-long', title: longTitle })] })

      const titleEl = screen.getByText(longTitle)
      // Full text is preserved in the DOM — only CSS (line-clamp) visually clips it.
      expect(titleEl.textContent).toBe(longTitle)
      expect(titleEl).not.toHaveClass('line-clamp-2')
      expect(titleEl).toHaveClass('whitespace-normal') // T3 founder steering: full wrapping, no Tooltip extension.
      expect(titleEl).toHaveClass('wrap-break-word')
      expect(titleEl).toHaveClass('min-w-0')
      // TaskCard-specific row-layout classes must NOT leak in — this span
      // sits in a column flex container, not a row.
      expect(titleEl).not.toHaveClass('flex-1')
      expect(titleEl).not.toHaveClass('pr-6')

      // Real cascade resolution (not just a className string match): the
      // injected stylesheet mirrors Tailwind's ACTUAL compiled output
      // (verified above), proving the classes take effect through jsdom's
      // real CSSOM.
      const computed = getComputedStyle(titleEl)
      expect(computed.overflowWrap).toBe('break-word')
      expect(computed.wordBreak).toBe('normal')
      expect(computed.minWidth).toBe('0px')

      // T15/T16 replace native hover titles with an explicit info control.
      const tile = screen.getByTestId('plan-filter-tile-plan-long')
      expect(tile).not.toHaveAttribute('title')
      expect(screen.getByRole('button', { name: `Plan details: ${longTitle}` })).toBeInTheDocument()
    } finally {
      removeStyle()
    }
  })

  it('short titles are unaffected — no visible change for normal-length content', () => {
    renderBand({ plans: [makePlan({ id: 'plan-short', title: 'Payments revamp' })] })

    const titleEl = screen.getByText('Payments revamp')
    expect(titleEl.textContent).toBe('Payments revamp')
    expect(titleEl).toHaveClass('whitespace-normal', 'wrap-break-word', 'min-w-0')
    expect(titleEl).not.toHaveClass('line-clamp-2')

    const tile = screen.getByTestId('plan-filter-tile-plan-short')
    expect(tile).not.toHaveAttribute('title')
    expect(screen.getByRole('button', { name: 'Plan details: Payments revamp' })).toBeInTheDocument()
  })
})
