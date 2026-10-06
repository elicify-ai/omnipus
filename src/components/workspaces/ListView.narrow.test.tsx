// ListView.narrow.test.tsx — wave-3 join pack for side-panel-shell-spec.md
// Wave 3, SP-35 (with SP-33's mechanism):
//
//   "List narrow hides Tags/Updated behind a '…' overflow control below a
//    measured width."  (§10 Wave 3 table; §13 SP-35)
//
//   Wireframe §2F: "List — narrow, Tags/Updated hidden behind '⋯'. Click the
//   '⋯' header to show Tags/Updated again — the real breakpoint for this
//   switch is measured live from the table's own content, see section 9."
//   (§9B: List's own breakpoint is measured, not guessed.)
//
// Oracle sources: spec §10 Wave 3 / §13 SP-35, wireframe §2F + §9B.
//
// JOIN STATUS (2026-10-05, work/side-panel-wave3-join-20261005) — delivered
// seam renegotiations (authorised by the 8773803cf pack's header), with the
// SPEC expectations unchanged:
//
//   1. `data-narrow` seam → NOT delivered. The narrow state lives in the
//      container query itself; the component reads it through a dedicated
//      narrow-probe span (`hidden @max-[648px]:block`) so the "⋯" control
//      knows which way "toggle" means WITHOUT re-deriving the breakpoint in
//      JS (the probe is the container query's own read-out). The measured
//      breakpoint is the delivered 648px. The declared fixed utilities total
//      31.5rem: nominally 504px at a 16px root, 441px at the product's 14px
//      default, and 630px at a 20px preference. The documented 144px Title
//      allowance is separate; that arithmetic is not a rendered-geometry proof.
//   2. The governed columns STAY IN THE DOM and sort/filter state persists.
//      Hiding uses display:none: the columns leave the accessibility tree
//      while hidden and return when the ⋯ control reveals them. Hiding is the
//      container-query class in the default `auto` state, a plain `hidden`
//      class when forced hidden, and no hide class when revealed. jsdom cannot
//      evaluate the query, so the pack pins the class mechanism and control
//      state machine, not browser accessibility-tree exposure.
//   3. The "⋯" control carries no test id — it is a real <button> whose
//      aria-label IS its contract: "Show or hide Tags and Updated columns"
//      (auto) / "Hide Tags and Updated columns" (revealed) / "Show Tags and
//      Updated columns" (forced hidden). The label names BOTH governed
//      columns, exactly what the RED pack required of the accessible name.
//
// jsdom-honest narrow default: a jsdom container measures 0 → narrow is the
// honest default (same convention the RED pack declared). The toggle's
// direction read is the browser's CSS evaluation (a process edge), so the
// tests stub getComputedStyle FOR THE PROBE ELEMENT ONLY — display 'block'
// is what the query yields at ≤648px. Browser-truth (the pixel flip at 648px
// of PANEL width) stays a UAT click-test row.

import { describe, it, expect, vi, afterEach } from 'vitest'
import { render, within, fireEvent } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { Task } from '@/lib/api'

function makeTask(overrides: Partial<Task> = {}): Task {
  return {
    id: `t-${Math.random().toString(36).slice(2)}`,
    title: 'A task',
    status: 'inbox',
    action: 'llm',
    priority: 3,
    workspace_id: 'ws-1',
    surface: 'user',
    owner: 'admin',
    created_by: 'admin',
    created_at: '2026-06-20T10:00:00Z',
    updated_at: '2026-06-20T10:00:00Z',
    tags: ['alpha'],
    ...overrides,
  }
}

function makeClient() {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
}

import { ListView } from './ListView'

const agents = [
  { id: 'jim', name: 'Jim' },
]

function renderList(tasks: Task[]) {
  return render(
    <QueryClientProvider client={makeClient()}>
      <ListView tasks={tasks} agents={agents} onTaskClick={vi.fn()} />
    </QueryClientProvider>,
  )
}

/** The narrow-probe span — the container query's DOM read-out the "⋯"
 * control consults (aria-hidden, no semantics of its own). */
function narrowProbe(container: HTMLElement): HTMLElement {
  const probe = container.querySelector('span[aria-hidden="true"]')
  expect(probe, 'ListView renders its narrow-probe read-out span').not.toBeNull()
  return probe as HTMLElement
}

/** The "⋯" overflow control — a real button whose aria-label names both
 * governed columns (located by role + name, no test id in the contract). */
function overflowControl(mounted: ReturnType<typeof renderList>): HTMLElement {
  return mounted.getByRole('button', { name: /Tags and Updated columns/ })
}

const AUTO_LABEL = 'Show or hide Tags and Updated columns'
const SHOWN_LABEL = 'Hide Tags and Updated columns'

describe('ListView narrow — SP-33 mechanism: the List owns a measured, container-driven breakpoint', () => {
  it('the list declares itself a CSS container (@container)', () => {
    const mounted = renderList([makeTask({ title: 'Row task' })])
    const declaresContainer = Array.from(mounted.container.querySelectorAll('*')).some((el) =>
      el.classList.contains('@container'),
    )
    expect(declaresContainer).toBe(true)
  })

  it('the narrow-probe read-out exists at the measured breakpoint, so JS never re-derives it', () => {
    // Oracle: SP-33/SP-35 — the breakpoint is "measured from the real
    // table's own content" (the table's documented 648px fixed-column
    // budget). The probe span carries exactly the ≤648px container variant —
    // a second number anywhere would be an unmeasured guess.
    const mounted = renderList([makeTask({ title: 'Row task' })])
    const probe = narrowProbe(mounted.container)
    expect(probe.className).toContain('hidden')
    expect(probe.className).toContain('@max-[648px]:block')
  })
})

describe('ListView narrow — SP-35: Tags/Updated hidden behind a "⋯" control below the measured width', () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('default (auto) state: the Tags and Updated column headers carry the narrow-hide container class', () => {
    // jsdom cannot evaluate the container query and the columns stay in the
    // DOM by design, so the honest narrow oracle is the class mechanism: in
    // `auto` the two governed headers hide below the measured breakpoint.
    // Browser-truth (they are visually gone at ≤648px) is a UAT row.
    const mounted = renderList([
      makeTask({ id: 't-1', title: 'Tagged task', tags: ['alpha'] }),
    ])
    // "Updated" carries the default sort arrow ("Updated ↓"), so the header
    // lookup matches the label prefix, not the arrow suffix.
    const tagsHeader = mounted.getByText('Tags').closest('th') as HTMLElement
    const updatedHeader = mounted.getByText(/^Updated/).closest('th') as HTMLElement
    expect(tagsHeader.className).toContain('@max-[648px]:hidden')
    expect(updatedHeader.className).toContain('@max-[648px]:hidden')
    // …and the control sits in its measured three-way start state.
    expect(overflowControl(mounted).getAttribute('aria-label')).toBe(AUTO_LABEL)
  })

  it('the "⋯" overflow control is a real button whose accessible name names BOTH governed columns', () => {
    const mounted = renderList([makeTask({ title: 'Tagged task' })])
    const toggle = overflowControl(mounted)
    expect(toggle.tagName).toBe('BUTTON')
    const name = toggle.getAttribute('aria-label') ?? toggle.textContent ?? ''
    expect(name).toMatch(/Tags/i)
    expect(name).toMatch(/Updated/i)
  })

  it('toggling reveals Tags and Updated; toggling again returns them to the container query (state round-trip)', () => {
    // jsdom-honest narrow default: the probe READS as displayed (≤648px),
    // so the first toggle from `auto` REVEALS the governed columns ("what
    // am I looking at right now?" — hidden → show them). The probe's CSS
    // evaluation is a process edge, stubbed for the probe element only.
    const mounted = renderList([makeTask({ title: 'Tagged task' })])
    const realGetComputedStyle = window.getComputedStyle.bind(window)
    vi.spyOn(window, 'getComputedStyle').mockImplementation((el) => {
      if (el instanceof HTMLElement && el.className.includes('@max-[648px]:block')) {
        return { display: 'block' } as CSSStyleDeclaration
      }
      return realGetComputedStyle(el as Element)
    })
    const toggle = overflowControl(mounted)

    // Intermediate assertion pins the hidden start state so the click below
    // proves a transition, not a static condition.
    const tagsHeader = mounted.getByText('Tags').closest('th') as HTMLElement
    expect(tagsHeader.className).toContain('@max-[648px]:hidden')

    fireEvent.click(toggle)
    expect(toggle.getAttribute('aria-label')).toBe(SHOWN_LABEL)
    expect(tagsHeader.className).not.toContain('@max-[648px]:hidden')
    expect(tagsHeader.className).not.toContain('hidden')
    expect(mounted.getByText(/^Updated/).closest('th')?.className).not.toContain('hidden')

    fireEvent.click(toggle)
    // Back to `auto`: the container query is the single source of truth again.
    expect(toggle.getAttribute('aria-label')).toBe(AUTO_LABEL)
    expect((mounted.getByText('Tags').closest('th') as HTMLElement).className).toContain('@max-[648px]:hidden')
    expect((mounted.getByText(/^Updated/).closest('th') as HTMLElement).className).toContain('@max-[648px]:hidden')
  })

  it('the toggle governs ONLY Tags and Updated — Pri, Title, Status, Agent stay visible throughout', () => {
    const mounted = renderList([makeTask({ title: 'Tagged task' })])
    const realGetComputedStyle = window.getComputedStyle.bind(window)
    vi.spyOn(window, 'getComputedStyle').mockImplementation((el) => {
      if (el instanceof HTMLElement && el.className.includes('@max-[648px]:block')) {
        return { display: 'block' } as CSSStyleDeclaration
      }
      return realGetComputedStyle(el as Element)
    })
    const toggle = overflowControl(mounted)

    const ungoverned = ['Pri', 'Title', 'Status', 'Agent'].map(
      (label) => mounted.getByText(label).closest('th') as HTMLElement,
    )
    // The untouched columns never carry either hide mechanism…
    for (const th of ungoverned) {
      expect(th.className).not.toContain('@max-[648px]:hidden')
      expect(th.className).not.toContain('hidden')
    }

    fireEvent.click(toggle)
    // …and remain present with Tags/Updated revealed. ("Updated" carries the
    // default sort arrow, hence the prefix match.)
    for (const label of ['Pri', 'Title', 'Status', 'Agent']) {
      expect(mounted.getByText(label)).toBeInTheDocument()
    }
    expect((mounted.getByText('Tags').closest('th') as HTMLElement).className).not.toContain('hidden')
    expect((mounted.getByText(/^Updated/).closest('th') as HTMLElement).className).not.toContain('hidden')
  })

  it('revealed columns keep their data: a tagged task shows its tag once Tags is revealed', () => {
    const mounted = renderList([
      makeTask({ id: 't-1', title: 'Tagged task', tags: ['alpha'] }),
    ])
    const realGetComputedStyle = window.getComputedStyle.bind(window)
    vi.spyOn(window, 'getComputedStyle').mockImplementation((el) => {
      if (el instanceof HTMLElement && el.className.includes('@max-[648px]:block')) {
        return { display: 'block' } as CSSStyleDeclaration
      }
      return realGetComputedStyle(el as Element)
    })
    fireEvent.click(overflowControl(mounted))

    const row = mounted.getByText('Tagged task').closest('tr') as HTMLElement
    const tagCell = within(row).getByText('alpha').closest('td') as HTMLElement
    expect(tagCell, 'the revealed Tags cell still carries its data').toBeDefined()
    // The row's Tags cell left the narrow-hidden state with its header.
    expect(tagCell.className).not.toContain('@max-[648px]:hidden')
    expect(tagCell.className).not.toContain('hidden')
  })
})
