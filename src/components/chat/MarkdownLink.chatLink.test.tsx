/**
 * RED: chat links in both markdown renderers (BS-1.1, BS-1.8, SEC-4, SEC-5, SEC-6).
 * The query (workspace and path) comes from BS-1.15. The hash prefix is the
 * app's address for that route: main.tsx mounts createHashHistory, and
 * KnowledgeBacklinks.tsx::libraryNoteHref already writes /#/library?...
 * Chat message rendering opts in with createLinkRenderer(null, { chatLinks: true }).
 * The default renderer (Mail and Library) must not build a Library anchor (BS-1.18).
 */
import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { registerChatView } from '@/store/chatView'
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

