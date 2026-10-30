/**
 * RED: chat links in both markdown renderers (BS-1.1, BS-1.8, SEC-4, SEC-5, SEC-6).
 * The query (workspace and path) comes from BS-1.15. The hash prefix is the
 * app's address for that route: main.tsx mounts createHashHistory, and
 * KnowledgeBacklinks.tsx::libraryNoteHref already writes /#/library?...
 * Chat message rendering opts in with createLinkRenderer(null, { chatLinks: true }).
 * The default renderer (Mail and Library) must not build a Library anchor (BS-1.18).
 */
import { fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { registerChatView } from '@/store/chatView'
import { useUiStore } from '@/store/ui'
import { createLinkRenderer } from './markdown-shared'

const MarkdownLink = createLinkRenderer(null, { chatLinks: true })
const PlainLink = createLinkRenderer(null)

describe('omnipus file links', () => {
  it('renders an internal Library anchor with a visible target suffix (BS-1.1 / SEC-6)', () => {
    render(
      <MarkdownLink href="omnipus://file/W/secrets/payroll.xlsx">click me</MarkdownLink>,
    )
    const link = screen.getByTestId('markdown-link')
    expect(link.tagName).toBe('A')
    expect(link).toHaveAttribute('href', '/#/library?workspace=W&path=secrets%2Fpayroll.xlsx')
    expect(link).not.toHaveAttribute('target')
    expect(link).toHaveTextContent('click me')
    expect(link).toHaveTextContent('W / secrets/payroll.xlsx')
    expect(link).not.toHaveAttribute('aria-description')
    const describedBy = link.getAttribute('aria-describedby')
    expect(describedBy, 'suffix is named by aria-describedby').toBeTruthy()
    const suffix = document.getElementById(describedBy ?? '')
    expect(suffix?.textContent).toBe('W / secrets/payroll.xlsx')
    expect(suffix?.getAttribute('title')).toBeTruthy()
    useUiStore.getState().closePanel()
    fireEvent.click(link)
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'W', path: 'secrets/payroll.xlsx' },
    })
  })

  it('keeps a struck span for schemes that must not become anchors (SEC-4 / SEC-5)', () => {
    const hrefs = [
      'javascript:alert(1)',
      'data:text/html,hi',
      'vbscript:msgbox(1)',
      'file:///etc/passwd',
      'blob:http://example/x',
      'ftp://example.com/a',
      'OMNIPUS://file/W/a',
      'omnipus:file/W/a',
      'omnipus://evil/W/a',
      'omnipus://file//a',
      'omnipus://kb/W/a',
    ]
    for (const href of hrefs) {
      const { unmount } = render(<MarkdownLink href={href}>x</MarkdownLink>)
      const el = screen.getByTestId('markdown-link')
      expect(el.tagName, href).toBe('SPAN')
      expect(el).toHaveAttribute('title', 'Link removed: unsafe URL scheme')
      expect(el).not.toHaveAttribute('href')
      unmount()
    }
  })
})

describe('default renderer has no chat links (BS-1.18)', () => {
  let release: (() => void) | undefined
  afterEach(() => {
    release?.()
    release = undefined
  })

  it('does not turn omnipus:// or a bare path into a Library anchor', () => {
    release = registerChatView('sess-mail', 'W')
    for (const href of ['omnipus://file/W/x.md', 'notes/x.md']) {
      const { unmount, container } = render(<PlainLink href={href}>a</PlainLink>)
      const library = [...container.querySelectorAll('a')].filter((anchor) =>
        (anchor.getAttribute('href') ?? '').includes('/#/library'),
      )
      expect(library, href).toEqual([])
      unmount()
    }
  })
})

describe('rejected bare targets stay the existing unsafe-link span (UI States, OBS-5)', () => {
  // Spec UI States: an unparseable link is the same node MarkdownLink already
  // renders for an unsafe href — a struck-through span titled
  // "Link removed: unsafe URL scheme", not a raw text node. Tests assert that
  // treatment. OBS-5 says the same. The grammar's "plain text" sentence
  // (Bare-link rule: README, docs/, and any final segment that is not a file)
  // names that non-anchor outcome, not a second rendering.
  // `views` and `readme` fail the file rule (no `.` then 1–16 alphanumerics),
  // so with a known workspace they are rejected forms.
  let release: (() => void) | undefined
  afterEach(() => {
    release?.()
    release = undefined
    useUiStore.getState().closePanel()
  })

  it('renders views and readme as the struck span, and a real file as a Library anchor', () => {
    release = registerChatView('sess-rejected', 'W')
    const acceptedView = render(<MarkdownLink href="notes/plan.md">the plan</MarkdownLink>)
    const accepted = screen.getByTestId('markdown-link')
    expect(accepted.tagName).toBe('A')
    expect(accepted).toHaveAttribute('href', '/#/library?workspace=W&path=notes%2Fplan.md')
    acceptedView.unmount()

    for (const href of ['views', 'readme']) {
      const { unmount } = render(<MarkdownLink href={href}>{href}</MarkdownLink>)
      const el = screen.getByTestId('markdown-link')
      expect(el.tagName, href).toBe('SPAN')
      expect(el, href).toHaveAttribute('title', 'Link removed: unsafe URL scheme')
      expect(el, href).not.toHaveAttribute('href')
      expect(el, href).toHaveTextContent(href)
      unmount()
    }
  })
})

