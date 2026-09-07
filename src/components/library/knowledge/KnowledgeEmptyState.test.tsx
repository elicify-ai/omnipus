// KnowledgeEmptyState.test.tsx — the first-run states, and the honesty rules
// they exist to keep (ADR-067 US-4, US-6, FR-035, FR-036, FR-112, E-1, E-9).
//
// ORACLE. Every expected value below is derived from the specification text,
// not from what the component happens to render:
//
//   - "the state shown is indeterminate — a count found so far, never a ratio
//      and never '0 of 0'"                                    US-6 AS-1 / FR-036
//   - "a ratio of indexed to total is shown alongside the results"
//                                                             US-6 AS-2 / FR-035
//   - "Given indexing has finished … no incompleteness statement is shown"
//                                                             US-6 AS-4
//   - "the indexing state is shown, not the empty-collection first run"
//                                                             US-6 AS-5
//   - "Collection with 0 notes → First-run offer to create a note"        E-1
//   - "Marker present but unreadable → Detection fails loudly; the folder is
//      not silently downgraded to 'ordinary'"                              E-9
//   - "The system MUST report files it cannot address, rather than omitting
//      them silently"                                                  FR-112
//
// The negative assertions carry most of the weight here. A test that only
// checks the right words appear cannot catch the failure this feature is about
// — which is an EXTRA claim, a percentage or a ratio, appearing next to them.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'

import {
  KnowledgeEmptyState,
  canShowIndexRatio,
  type KnowledgeFirstRunState,
} from './KnowledgeEmptyState'

/** Any digit-run followed by a percent sign, e.g. "40%" or "12.5 %". */
const PERCENTAGE = /\d[\d.,]*\s*%/
/** The "X of Y" ratio shape, in any of the words the component might use. */
const RATIO = /\d[\d,]*\s+of\s+\d[\d,]*/i

describe('KnowledgeEmptyState — indexing, total not yet known (US-6 AS-1, FR-036)', () => {
  const enumerating: KnowledgeFirstRunState = {
    kind: 'indexing',
    name: 'Research vault',
    phase: 'enumerating',
    indexedFiles: 340,
    totalKnown: false,
  }

  it('states a count found so far', () => {
    render(<KnowledgeEmptyState state={enumerating} />)
    const line = screen.getByTestId('knowledge-index-progress-indeterminate')
    expect(line).toHaveTextContent('340')
    expect(line.textContent).toMatch(/so far/i)
  })

  it('says the total is not yet known, in words', () => {
    render(<KnowledgeEmptyState state={enumerating} />)
    expect(screen.getByTestId('knowledge-state-indexing').textContent).toMatch(
      /total is not yet known/i,
    )
  })

  it('renders NO ratio and NO percentage anywhere in the block', () => {
    render(<KnowledgeEmptyState state={enumerating} />)
    const text = screen.getByTestId('knowledge-state-indexing').textContent ?? ''
    expect(text).not.toMatch(RATIO)
    expect(text).not.toMatch(PERCENTAGE)
    expect(screen.queryByTestId('knowledge-index-progress-ratio')).not.toBeInTheDocument()
  })

  it('renders an INDETERMINATE progressbar — no aria-valuenow, no aria-valuemax', () => {
    // An indeterminate progressbar is defined by the ABSENCE of aria-valuenow.
    // Present-but-zero is the specific lie being guarded against: it reports
    // "0% done" against a total nobody has.
    render(<KnowledgeEmptyState state={enumerating} />)
    const bar = screen.getByRole('progressbar')
    expect(bar).not.toHaveAttribute('aria-valuenow')
    expect(bar).not.toHaveAttribute('aria-valuemax')
  })

  it('stays indeterminate when the indexer claims a known total of zero — never "0 of 0" (US-6 AS-1)', () => {
    // total_known true with total_files 0 is the shape that produces the
    // literal forbidden rendering. It must fall back to indeterminate, not be
    // rendered as a completed ratio.
    render(
      <KnowledgeEmptyState
        state={{ ...enumerating, phase: 'indexing', indexedFiles: 0, totalKnown: true, totalFiles: 0 }}
      />,
    )
    expect(screen.getByTestId('knowledge-index-progress-indeterminate')).toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-index-progress-ratio')).not.toBeInTheDocument()
    expect(screen.getByTestId('knowledge-state-indexing').textContent ?? '').not.toMatch(/0 of 0/)
  })

  it('stays indeterminate when the total is claimed known but absent', () => {
    render(<KnowledgeEmptyState state={{ ...enumerating, totalKnown: true, totalFiles: undefined }} />)
    expect(screen.getByTestId('knowledge-index-progress-indeterminate')).toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-index-progress-ratio')).not.toBeInTheDocument()
  })

  it('names the collection it is indexing', () => {
    render(<KnowledgeEmptyState state={enumerating} />)
    expect(screen.getByTestId('knowledge-state-indexing').textContent).toContain('Research vault')
  })

  it('says results drawn now are partial', () => {
    render(<KnowledgeEmptyState state={enumerating} />)
    expect(screen.getByTestId('knowledge-state-indexing').textContent).toMatch(/partial/i)
  })
})

describe('KnowledgeEmptyState — indexing with a known total (US-6 AS-2, FR-035)', () => {
  const indexing: KnowledgeFirstRunState = {
    kind: 'indexing',
    name: 'Research vault',
    phase: 'indexing',
    indexedFiles: 12,
    totalKnown: true,
    totalFiles: 500,
  }

  it('shows the ratio of indexed to total', () => {
    render(<KnowledgeEmptyState state={indexing} />)
    expect(screen.getByTestId('knowledge-index-progress-ratio')).toHaveTextContent('12 of 500')
  })

  it('renders a DETERMINATE progressbar carrying the real numbers', () => {
    render(<KnowledgeEmptyState state={indexing} />)
    const bar = screen.getByRole('progressbar')
    expect(bar).toHaveAttribute('aria-valuenow', '12')
    expect(bar).toHaveAttribute('aria-valuemax', '500')
    expect(bar).toHaveAttribute('aria-valuemin', '0')
  })

  it('drops the indeterminate wording once the total is known', () => {
    render(<KnowledgeEmptyState state={indexing} />)
    expect(screen.queryByTestId('knowledge-index-progress-indeterminate')).not.toBeInTheDocument()
  })
})

describe('canShowIndexRatio — the denominator guard (US-6 AS-1)', () => {
  it.each([
    { totalKnown: false, totalFiles: undefined, expected: false },
    { totalKnown: false, totalFiles: 500, expected: false },
    { totalKnown: true, totalFiles: undefined, expected: false },
    { totalKnown: true, totalFiles: 0, expected: false },
    { totalKnown: true, totalFiles: 1, expected: true },
    { totalKnown: true, totalFiles: 500, expected: true },
  ])('total_known=$totalKnown total_files=$totalFiles → $expected', ({ totalKnown, totalFiles, expected }) => {
    expect(canShowIndexRatio(totalKnown, totalFiles)).toBe(expected)
  })
})

describe('KnowledgeEmptyState — a finished index says nothing (US-6 AS-4, AS-6)', () => {
  it('renders nothing at all when the index is ready', () => {
    const { container } = render(
      <KnowledgeEmptyState state={{ kind: 'ready', name: 'Research vault', indexedFiles: 4200 }} />,
    )
    expect(container).toBeEmptyDOMElement()
  })

  it('renders nothing even for a single-note collection (E-2 — no "1 note" banner)', () => {
    const { container } = render(
      <KnowledgeEmptyState state={{ kind: 'ready', name: 'Research vault', indexedFiles: 1 }} />,
    )
    expect(container).toBeEmptyDOMElement()
  })
})

describe('KnowledgeEmptyState — detection failed (E-9)', () => {
  const failed: KnowledgeFirstRunState = {
    kind: 'detection_error',
    code: 'marker_unreadable',
    message: 'cannot read notes/vault/.omnipus-vault: permission denied',
    rootPath: 'notes/vault',
  }

  it('reports the failure loudly, as an alert', () => {
    render(<KnowledgeEmptyState state={failed} />)
    const block = screen.getByTestId('knowledge-state-detection-error')
    expect(block).toHaveAttribute('role', 'alert')
  })

  it("does NOT downgrade the folder to 'ordinary' — the ordinary-folder state is not rendered", () => {
    render(<KnowledgeEmptyState state={failed} />)
    expect(screen.queryByTestId('knowledge-state-not-a-knowledge-base')).not.toBeInTheDocument()
    // And it says the verdict is unknown rather than negative.
    expect(screen.getByTestId('knowledge-state-detection-error').textContent).toMatch(/unknown/i)
  })

  it("shows the server's own explanation verbatim", () => {
    render(<KnowledgeEmptyState state={failed} />)
    expect(screen.getByTestId('knowledge-detection-error-message')).toHaveTextContent(
      'cannot read notes/vault/.omnipus-vault: permission denied',
    )
  })

  it.each([
    { code: 'root_missing' as const, match: /no longer/i },
    { code: 'not_a_directory' as const, match: /file, not a folder/i },
    { code: 'root_unreadable' as const, match: /could not read this folder/i },
  ])('explains $code in its own words', ({ code, match }) => {
    render(<KnowledgeEmptyState state={{ ...failed, code }} />)
    expect(screen.getByTestId('knowledge-state-detection-error').textContent).toMatch(match)
  })
})

describe('KnowledgeEmptyState — an ordinary folder (US-4 AS-3, AS-5)', () => {
  const ordinary: KnowledgeFirstRunState = {
    kind: 'not_a_knowledge_base',
    rootPath: 'projects/notes',
  }

  it('says knowledge-base features are off here', () => {
    render(<KnowledgeEmptyState state={ordinary} onCreateCollection={vi.fn()} />)
    expect(screen.getByTestId('knowledge-state-not-a-knowledge-base').textContent).toMatch(
      /search and linked mentions stay switched off/i,
    )
  })

  it('does not claim heading outlines are off — those work on any markdown file (FR-062)', () => {
    render(<KnowledgeEmptyState state={ordinary} onCreateCollection={vi.fn()} />)
    expect(screen.getByTestId('knowledge-state-not-a-knowledge-base').textContent).toMatch(
      /outlines still work/i,
    )
  })

  it('offers to create a collection when creation is wired, and calls the handler', () => {
    const onCreateCollection = vi.fn()
    render(<KnowledgeEmptyState state={ordinary} onCreateCollection={onCreateCollection} />)
    fireEvent.click(screen.getByTestId('knowledge-create-collection'))
    expect(onCreateCollection).toHaveBeenCalledTimes(1)
  })

  it('states that creating one writes .omnipus-vault and never .obsidian (US-4 AS-5, FR-022/FR-023)', () => {
    render(<KnowledgeEmptyState state={ordinary} onCreateCollection={vi.fn()} />)
    const text = screen.getByTestId('knowledge-state-not-a-knowledge-base').textContent ?? ''
    expect(text).toContain('.omnipus-vault')
    expect(text).toMatch(/never creates an\s*\.obsidian/i)
  })

  // US-4 AS-3: an ordinary folder is an ordinary folder. With no create handler
  // there is no offer to make, and a card explaining that an unavailable
  // feature is switched off would render above the listing of EVERY folder in
  // every workspace, forever. Silence is the correct rendering of "nothing to
  // say", and it is also what stops this spot being trained out of the
  // reader's attention before a real warning ever appears there.
  it('renders nothing at all when creation is unwired — no card, no dead control', () => {
    const { container } = render(<KnowledgeEmptyState state={ordinary} />)
    expect(screen.queryByTestId('knowledge-create-collection')).not.toBeInTheDocument()
    expect(screen.queryByTestId('knowledge-state-not-a-knowledge-base')).not.toBeInTheDocument()
    expect(container).toBeEmptyDOMElement()
  })
})

describe('KnowledgeEmptyState — an empty collection (E-1)', () => {
  const empty: KnowledgeFirstRunState = { kind: 'empty_collection', name: 'Research vault' }

  it('names it as empty and says that is not a fault', () => {
    render(<KnowledgeEmptyState state={empty} />)
    const text = screen.getByTestId('knowledge-state-empty').textContent ?? ''
    expect(text).toContain('Research vault')
    expect(text).toMatch(/nothing is wrong/i)
  })

  it('offers to create the first note when creation is wired', () => {
    const onCreateNote = vi.fn()
    render(<KnowledgeEmptyState state={empty} onCreateNote={onCreateNote} />)
    fireEvent.click(screen.getByTestId('knowledge-create-note'))
    expect(onCreateNote).toHaveBeenCalledTimes(1)
  })

  it('says what to do next when creation is unwired, and renders no dead button', () => {
    render(<KnowledgeEmptyState state={empty} />)
    expect(screen.queryByTestId('knowledge-create-note')).not.toBeInTheDocument()
    expect(screen.getByTestId('knowledge-create-note-unavailable').textContent).toMatch(
      /index it automatically/i,
    )
  })
})

describe('KnowledgeEmptyState — skipped files are never silent (FR-112, FR-044, FR-111)', () => {
  it('reports skipped files while indexing', () => {
    render(
      <KnowledgeEmptyState
        state={{
          kind: 'indexing',
          name: 'Research vault',
          phase: 'indexing',
          indexedFiles: 12,
          totalKnown: true,
          totalFiles: 500,
          skippedFiles: 3,
        }}
      />,
    )
    expect(screen.getByTestId('knowledge-skipped-files')).toHaveTextContent('3')
  })

  it('reports skipped files on an otherwise-empty collection', () => {
    render(
      <KnowledgeEmptyState state={{ kind: 'empty_collection', name: 'Research vault', skippedFiles: 7 }} />,
    )
    expect(screen.getByTestId('knowledge-skipped-files')).toHaveTextContent('7')
  })

  it('says nothing when nothing was skipped', () => {
    render(
      <KnowledgeEmptyState state={{ kind: 'empty_collection', name: 'Research vault', skippedFiles: 0 }} />,
    )
    expect(screen.queryByTestId('knowledge-skipped-files')).not.toBeInTheDocument()
  })
})

describe('KnowledgeEmptyState — indexing failed, and status unknown', () => {
  it('surfaces the failure reason rather than leaving a bar stalled', () => {
    render(
      <KnowledgeEmptyState
        state={{
          kind: 'index_failed',
          name: 'Research vault',
          message: 'index directory is locked by another process',
          indexedFiles: 40,
        }}
      />,
    )
    const block = screen.getByTestId('knowledge-state-index-failed')
    expect(block).toHaveAttribute('role', 'alert')
    expect(screen.getByTestId('knowledge-index-failed-message')).toHaveTextContent(
      'index directory is locked by another process',
    )
    // No progressbar left behind at some percentage.
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument()
  })

  it('says results are incomplete when indexing stopped part-way', () => {
    render(
      <KnowledgeEmptyState
        state={{ kind: 'index_failed', name: 'Research vault', message: 'disk full', indexedFiles: 40 }}
      />,
    )
    expect(screen.getByTestId('knowledge-state-index-failed').textContent).toMatch(/incomplete/i)
  })

  it('admits when it has no progress information at all, rather than implying completeness', () => {
    render(<KnowledgeEmptyState state={{ kind: 'index_status_unknown', name: 'Research vault' }} />)
    const text = screen.getByTestId('knowledge-state-index-status-unknown').textContent ?? ''
    // It must say the state is UNKNOWN — not imply the index is fine, and not
    // assert that results are partial either, which is a claim of its own. The
    // per-answer completeness statement on each search response is the
    // authority, and this defers to it rather than contradicting it.
    expect(text).toMatch(/no indexing progress has arrived/i)
    expect(text).toMatch(/cannot tell you here how far the index has got/i)
    expect(text).toMatch(/states its own completeness/i)
    expect(text).not.toMatch(PERCENTAGE)
    expect(text).not.toMatch(RATIO)
  })
})

describe('KnowledgeEmptyState — UI chrome rules (CLAUDE.md)', () => {
  it.each<KnowledgeFirstRunState>([
    { kind: 'not_a_knowledge_base', rootPath: 'notes' },
    { kind: 'empty_collection', name: 'Vault' },
    { kind: 'indexing', name: 'Vault', phase: 'enumerating', indexedFiles: 1, totalKnown: false },
    { kind: 'index_status_unknown', name: 'Vault' },
    { kind: 'index_failed', name: 'Vault', message: 'x', indexedFiles: 0 },
    { kind: 'detection_error', code: 'marker_unreadable', message: 'x', rootPath: 'notes' },
  ])('renders no emoji in chrome ($kind)', (state) => {
    // onCreateCollection is passed so the `not_a_knowledge_base` row actually
    // RENDERS something — without it that state is silent by design and the
    // assertion would pass over an empty container, proving nothing.
    const { container } = render(<KnowledgeEmptyState state={state} onCreateCollection={vi.fn()} onCreateNote={vi.fn()} />)
    expect(container).not.toBeEmptyDOMElement()
    // Pictographic emoji only — this deliberately does not match ordinary
    // punctuation, accented Latin or the em dashes the copy uses.
    expect(container.textContent ?? '').not.toMatch(/\p{Extended_Pictographic}/u)
  })
})
