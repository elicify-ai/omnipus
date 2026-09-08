// ViewCellLink.test.tsx — KB-8(b): a base view's cell renders a stored
// `[[wikilink]]` as a real link, using the SAME parser and the SAME
// three-state honesty model the note-reading surface uses
// (knowledgeMarkdown.tsx), never a fourth state and never a raw `[[…]]`.

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { CellText } from './ViewCellLink'
import type { KbLinkResolution } from '../knowledgeMarkdown'

describe('CellText — plain values', () => {
  it('renders a value with no wikilink token verbatim, as plain text', () => {
    render(<CellText value="Acme Corp" />)
    expect(screen.getByText('Acme Corp')).toBeInTheDocument()
    expect(screen.queryByTestId('viewpart-cell-link')).not.toBeInTheDocument()
  })

  it('renders an empty value as empty, never throwing', () => {
    const { container } = render(<CellText value="" />)
    expect(container.textContent).toBe('')
  })
})

describe('CellText — the raw-wikilink defect (KB-8b)', () => {
  it('never leaves the brackets in the rendered text once a resolver is supplied', () => {
    const resolveWikilink = (): KbLinkResolution => ({ state: 'resolved', path: 'Companies/korn-ferry.md' })
    render(<CellText value="[[Korn Ferry]]" resolver={{ resolveWikilink }} />)
    expect(screen.queryByText('[[Korn Ferry]]')).not.toBeInTheDocument()
    expect(screen.getByText('Korn Ferry')).toBeInTheDocument()
  })

  it('renders surrounding text and a wikilink token in the middle of a longer value', () => {
    const resolveWikilink = (): KbLinkResolution => ({ state: 'resolved', path: 'x.md' })
    render(<CellText value="Owner: [[Korn Ferry]] (primary)" resolver={{ resolveWikilink }} />)
    expect(screen.getByText(/Owner:/)).toBeInTheDocument()
    expect(screen.getByText('Korn Ferry')).toBeInTheDocument()
    expect(screen.getByText(/\(primary\)/)).toBeInTheDocument()
  })

  it('honours an alias — [[Target|Alias]] displays the alias, not the target', () => {
    const resolveWikilink = (): KbLinkResolution => ({ state: 'resolved', path: 'x.md' })
    render(<CellText value="[[Korn Ferry Pte Ltd (SG)|Korn Ferry]]" resolver={{ resolveWikilink }} />)
    expect(screen.getByText('Korn Ferry')).toBeInTheDocument()
    expect(screen.queryByText(/Korn Ferry Pte Ltd/)).not.toBeInTheDocument()
  })
})

describe('CellText — the three-state honesty model, reused verbatim', () => {
  it('renders `resolved` as a real, followable link (a real href when linkHref is given)', () => {
    const resolveWikilink = (): KbLinkResolution => ({ state: 'resolved', path: 'Companies/korn-ferry.md' })
    const linkHref = (p: string) => `/#/library?path=${p}`
    render(<CellText value="[[Korn Ferry]]" resolver={{ resolveWikilink, linkHref }} />)
    const link = screen.getByTestId('viewpart-cell-link')
    expect(link.tagName).toBe('A')
    expect(link).toHaveAttribute('href', '/#/library?path=Companies/korn-ferry.md')
    expect(link).toHaveAttribute('data-kb-state', 'resolved')
  })

  it('renders `unknown` (no resolver answered yet) as visibly unverified — never as a working link and never as broken', () => {
    render(<CellText value="[[Korn Ferry]]" />)
    const link = screen.getByTestId('viewpart-cell-link')
    expect(link).toHaveAttribute('data-kb-state', 'unknown')
    // Not the verified accent color/underline treatment.
    expect(link.className).not.toContain('underline')
  })

  it('renders `unresolved` as inert text — no href, no click behavior, and it says so', () => {
    const resolveWikilink = (): KbLinkResolution => ({ state: 'unresolved' })
    const onOpenPath = vi.fn()
    render(<CellText value="[[Nowhere]]" resolver={{ resolveWikilink, onOpenPath }} />)
    expect(screen.queryByTestId('viewpart-cell-link')).not.toBeInTheDocument()
    const unresolved = screen.getByTestId('markdown-link')
    expect(unresolved).toHaveAttribute('data-kb-unresolved', 'true')
    fireEvent.click(unresolved)
    expect(onOpenPath).not.toHaveBeenCalled()
  })
})

describe('CellText — click behavior', () => {
  it('a plain click on a resolved link calls onOpenPath with the resolved path and does not navigate the anchor', () => {
    const resolveWikilink = (): KbLinkResolution => ({ state: 'resolved', path: 'Companies/korn-ferry.md' })
    const linkHref = (p: string) => `/#/library?path=${p}`
    const onOpenPath = vi.fn()
    render(<CellText value="[[Korn Ferry]]" resolver={{ resolveWikilink, linkHref, onOpenPath }} />)
    fireEvent.click(screen.getByTestId('viewpart-cell-link'))
    expect(onOpenPath).toHaveBeenCalledWith('Companies/korn-ferry.md')
    expect(onOpenPath).toHaveBeenCalledTimes(1)
  })

  it('stops a click from bubbling to an ancestor click handler (KB-8c)', () => {
    const resolveWikilink = (): KbLinkResolution => ({ state: 'resolved', path: 'Companies/korn-ferry.md' })
    const onOpenPath = vi.fn()
    const rowClick = vi.fn()
    render(
      <div onClick={rowClick} data-testid="row">
        <CellText value="[[Korn Ferry]]" resolver={{ resolveWikilink, onOpenPath }} />
      </div>,
    )
    fireEvent.click(screen.getByTestId('viewpart-cell-link'))
    expect(onOpenPath).toHaveBeenCalledTimes(1)
    expect(rowClick).not.toHaveBeenCalled()
  })

  it('does not intercept a modified click (browser handles new-tab/new-window itself)', () => {
    const resolveWikilink = (): KbLinkResolution => ({ state: 'resolved', path: 'x.md' })
    const linkHref = () => '/#/library?path=x.md'
    const onOpenPath = vi.fn()
    render(<CellText value="[[X]]" resolver={{ resolveWikilink, linkHref, onOpenPath }} />)
    fireEvent.click(screen.getByTestId('viewpart-cell-link'), { metaKey: true })
    expect(onOpenPath).not.toHaveBeenCalled()
  })
})

describe('CellText — resolution has no target to check', () => {
  it('renders a same-note heading token ([[#Heading]]) as plain text — a relation cell has no "current note" to scroll', () => {
    render(<CellText value="[[#Section]]" resolver={{ resolveWikilink: () => ({ state: 'resolved', path: 'x.md' }) }} />)
    expect(screen.queryByTestId('viewpart-cell-link')).not.toBeInTheDocument()
    expect(screen.getByText('#Section')).toBeInTheDocument()
  })
})
