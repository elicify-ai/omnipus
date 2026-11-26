// Office-preview spec §5.3 enlarged view, §5.4, B-14, T-64, B-41.
// User-facing sentences: docs/documents.md "Looking at a page up close" and
// "Screen readers". Expected text is from those sources, not from a render log.
//
// Drives PreviewPageViewer with the catalogued dialog. Page changes must be
// visible (indicator, image alt, polite "Page n of N") and the viewer node
// must stay the same node — §5.4: keyed by the group, not the page.

import { useState, type ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { PreviewPagesLightboxContent, PreviewViewerPage } from '@/store/ui'
import { PreviewPageViewer } from './PreviewPageViewer'
import { PREVIEW_GROUP_ATTR, PREVIEW_THUMB_ATTR, thumbAttrValue } from './previewGroups'

const GROUP = 'call-pages'
const DOC = 'report.docx'

function pageList(numbers: readonly number[], pageCount: number): PreviewViewerPage[] {
  return numbers.map((pageNumber) => ({
    src: `https://example.test/${GROUP}/page-${pageNumber}.png`,
    caption: `${DOC} · page ${pageNumber} of ${pageCount}`,
    pageNumber,
    filename: `page-${pageNumber}.png`,
  }))
}

function content(index: number, numbers: readonly number[], pageCount = numbers.length): PreviewPagesLightboxContent {
  const pages = pageList(numbers, pageCount)
  return {
    kind: 'pages',
    groupKey: GROUP,
    pages,
    index,
    pageCount,
    openerPage: pages[index].pageNumber,
  }
}

function indicator(): HTMLElement {
  const span = screen.getAllByText(/^Page \d+ of \d+$/).find((node) => node.tagName === 'SPAN')
  expect(span, '§5.4 visible page indicator').toBeTruthy()
  return span as HTMLElement
}

function announcement(): HTMLElement {
  const live = screen.getByTestId('preview-page-announcement')
  expect(live).toHaveAttribute('aria-live', 'polite')
  return live
}

async function openViewer(value: PreviewPagesLightboxContent) {
  render(<PreviewPageViewer content={value} onClose={vi.fn()} />)
  await waitFor(() => expect(screen.getByTestId('preview-page-region')).toHaveFocus())
  return screen.getByTestId('preview-page-viewer')
}

function FocusHost({ value, targets }: { value: PreviewPagesLightboxContent; targets: ReactNode }) {
  const [open, setOpen] = useState(true)
  return (
    <>
      {targets}
      {open ? <PreviewPageViewer content={value} onClose={() => setOpen(false)} /> : null}
    </>
  )
}

describe('PreviewPageViewer (§5.4, docs/documents.md)', () => {
  it('opens on the page it was given, with that caption as the title, the image text, and the indicator', async () => {
    // A batch that does not start at page 1: "page 12 of 30", not the index.
    const pageCount = 30
    const shown = content(1, [11, 12, 13], pageCount)
    await openViewer(shown)
    const caption = `${DOC} · page 12 of ${pageCount}`

    expect(screen.getByRole('heading', { name: caption })).toBeInTheDocument()
    expect(screen.getByRole('img', { name: caption })).toBeInTheDocument()
    expect(indicator()).toHaveTextContent('Page 12 of 30')
    const region = screen.getByTestId('preview-page-region')
    expect(region).toHaveFocus()
    const labelId = region.getAttribute('aria-labelledby')
    expect(labelId).toBeTruthy()
    expect(document.getElementById(labelId as string)).toHaveTextContent(caption)
  })

  it('moves one page at a time with Next page and Previous page and announces Page n of N', async () => {
    const user = userEvent.setup()
    await openViewer(content(1, [11, 12, 13], 30))

    await user.click(screen.getByRole('button', { name: 'Next page' }))
    expect(indicator()).toHaveTextContent('Page 13 of 30')
    expect(announcement()).toHaveTextContent('Page 13 of 30')
    expect(screen.getByRole('img', { name: `${DOC} · page 13 of 30` })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Previous page' }))
    await user.click(screen.getByRole('button', { name: 'Previous page' }))
    expect(indicator()).toHaveTextContent('Page 11 of 30')
    expect(announcement()).toHaveTextContent('Page 11 of 30')
  })

  it('moves three pages with the Right arrow without remounting, then Left moves back one', async () => {
    const user = userEvent.setup()
    const viewer = await openViewer(content(0, [1, 2, 3, 4]))
    const announced = ['Page 2 of 4', 'Page 3 of 4', 'Page 4 of 4']

    for (const text of announced) {
      await user.keyboard('{ArrowRight}')
      expect(screen.getByTestId('preview-page-viewer')).toBe(viewer)
      expect(indicator()).toHaveTextContent(text)
      expect(announcement()).toHaveTextContent(text)
    }

    await user.keyboard('{ArrowLeft}')
    expect(screen.getByTestId('preview-page-viewer')).toBe(viewer)
    expect(indicator()).toHaveTextContent('Page 3 of 4')
    expect(announcement()).toHaveTextContent('Page 3 of 4')
  })

  it('does nothing on the first page: Left leaves the indicator and the announcement unchanged, and Previous page is unavailable', async () => {
    const user = userEvent.setup()
    await openViewer(content(0, [1, 2, 3, 4]))
    const before = announcement().textContent

    expect(screen.getByRole('button', { name: 'Previous page' })).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByRole('button', { name: 'Next page' })).not.toHaveAttribute('aria-disabled', 'true')
    await user.keyboard('{ArrowLeft}')
    await user.click(screen.getByRole('button', { name: 'Previous page' }))

    expect(indicator()).toHaveTextContent('Page 1 of 4')
    expect(announcement().textContent).toBe(before)
    expect(screen.getByRole('heading', { name: `${DOC} · page 1 of 4` })).toBeInTheDocument()
  })

  it('does nothing on the last page of the group even when the document has more pages, and Next page is unavailable', async () => {
    // Group is pages 1–3 of a 12-page document (caption shape "page 3 of 12").
    // The boundary is the group, not page 12.
    const user = userEvent.setup()
    await openViewer(content(2, [1, 2, 3], 12))
    const before = announcement().textContent

    expect(indicator()).toHaveTextContent('Page 3 of 12')
    expect(screen.getByRole('button', { name: 'Next page' })).toHaveAttribute('aria-disabled', 'true')
    expect(screen.getByRole('button', { name: 'Previous page' })).not.toHaveAttribute('aria-disabled', 'true')
    await user.keyboard('{ArrowRight}')
    await user.click(screen.getByRole('button', { name: 'Next page' }))

    expect(indicator()).toHaveTextContent('Page 3 of 12')
    expect(announcement().textContent).toBe(before)
  })

  it('returns focus to the opening page image on Close, not to another page in the group', async () => {
    const user = userEvent.setup()
    const value = content(1, [1, 2, 3, 4])
    render(
      <FocusHost
        value={value}
        targets={
          <>
            <button type="button" {...{ [PREVIEW_THUMB_ATTR]: thumbAttrValue(GROUP, 1) }}>page 1</button>
            <button type="button" data-testid="opening-thumb" {...{ [PREVIEW_THUMB_ATTR]: thumbAttrValue(GROUP, value.openerPage) }}>page 2</button>
          </>
        }
      />,
    )
    await waitFor(() => expect(screen.getByTestId('preview-page-region')).toHaveFocus())

    await user.click(screen.getByRole('button', { name: 'Close' }))

    await waitFor(() => expect(screen.getByTestId('opening-thumb')).toHaveFocus())
    expect(screen.queryByRole('dialog')).toBeNull()
  })

  it('returns focus to the group when the opening page image is gone', async () => {
    const user = userEvent.setup()
    render(
      <FocusHost
        value={content(0, [1, 2, 3])}
        targets={<div tabIndex={-1} data-testid="group" {...{ [PREVIEW_GROUP_ATTR]: GROUP }} />}
      />,
    )
    await waitFor(() => expect(screen.getByTestId('preview-page-region')).toHaveFocus())

    await user.keyboard('{Escape}')

    await waitFor(() => expect(screen.getByTestId('group')).toHaveFocus())
  })

  it('returns focus to the message box when the page image and the group are both gone', async () => {
    const user = userEvent.setup()
    render(
      <FocusHost
        value={content(0, [1, 2])}
        targets={<textarea data-testid="chat-input" aria-label="Message" />}
      />,
    )
    await waitFor(() => expect(screen.getByTestId('preview-page-region')).toHaveFocus())

    await user.click(screen.getByRole('button', { name: 'Close' }))

    await waitFor(() => expect(screen.getByTestId('chat-input')).toHaveFocus())
  })

  it('keeps Tab and Shift-Tab inside the viewer', async () => {
    const user = userEvent.setup()
    render(
      <>
        <button type="button">Outside the viewer</button>
        <PreviewPageViewer content={content(0, [1, 2, 3, 4])} onClose={vi.fn()} />
      </>,
    )
    const dialog = screen.getByRole('dialog')
    // The dialog hides the rest of the page from assistive tech, so the
    // outside control is not in the accessibility tree while the viewer is open.
    const outside = screen.getByRole('button', { name: 'Outside the viewer', hidden: true })
    expect(outside.closest('[aria-hidden="true"]')).not.toBeNull()
    await waitFor(() => expect(screen.getByTestId('preview-page-region')).toHaveFocus())

    for (let step = 0; step < 8; step++) {
      await user.tab()
      expect(dialog.contains(document.activeElement), `Tab ${step + 1} left the viewer`).toBe(true)
      expect(outside).not.toHaveFocus()
    }
    for (let step = 0; step < 8; step++) {
      await user.tab({ shift: true })
      expect(dialog.contains(document.activeElement), `Shift-Tab ${step + 1} left the viewer`).toBe(true)
      expect(outside).not.toHaveFocus()
    }
  })

  it('shows "Page n could not be loaded." when that page fails, Retry loads it again, and Right still opens the next page', async () => {
    const user = userEvent.setup()
    // Not a batch that starts at page 1: page 12 is index 1, so an error that
    // used the index would say "Page 2" (docs: "Page n could not be loaded.").
    const pageCount = 30
    const failed = `${DOC} · page 12 of ${pageCount}`
    const viewer = await openViewer(content(1, [11, 12, 13], pageCount))
    fireEvent.error(screen.getByRole('img', { name: failed }))

    expect(screen.getByRole('alert')).toHaveTextContent('Page 12 could not be loaded.')
    expect(screen.queryByRole('img', { name: failed })).toBeNull()

    await user.click(screen.getByRole('button', { name: 'Retry' }))

    // Retry re-fetches this page: the error panel goes and the image is back.
    // A Retry that does nothing leaves the error up.
    expect(screen.queryByText('Page 12 could not be loaded.')).toBeNull()
    expect(screen.queryByRole('alert')).toBeNull()
    expect(screen.getByRole('img', { name: failed })).toBeInTheDocument()

    await user.keyboard('{ArrowRight}')

    expect(screen.getByTestId('preview-page-viewer')).toBe(viewer)
    expect(indicator()).toHaveTextContent('Page 13 of 30')
    expect(screen.getByRole('img', { name: `${DOC} · page 13 of 30` })).toBeInTheDocument()
  })
})
