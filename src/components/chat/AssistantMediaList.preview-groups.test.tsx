// Office-document preview — the assistant media list's preview-group
// behaviour (office-preview spec §5.2–§5.4) and the shared file card.
//
// No vitest file named AssistantMediaList or ChatFileAttachment before this
// one (the merge that introduced ChatFileAttachment shipped typecheck only),
// so the rows' own render contract was untested: inline file items render the
// keyboard-operable card, page_thumbnails media joins ONE group per tool call
// placed where its first item appeared with pages in page order and the
// preview record's facts in each caption, and an unavailable outcome renders
// the persistent unavailable card — never a broken image.
//
// Oracles from the spec (§5.2/§5.3), never from the implementation: media is
// grouped by (message, tool_call_id); page captions carry
// "<source> · page N of <page_count>"; the unavailable card shows
// "Page previews unavailable — <reason>".

import { describe, expect, it } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import { AssistantMediaList } from './AssistantMediaList'
import type { MediaAttachment } from '@/store/chat'

const PREVIEW_COMPLETE = {
  source_name: 'report.docx',
  source_version: 'a1b2c3d4e5f6',
  page_count: 12,
  first_page: 1,
  last_page: 2,
  outcome: 'complete' as const,
}

function fileItem(filename: string): MediaAttachment {
  return { type: 'file', url: `/api/v1/media/${filename}`, filename, contentType: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' }
}

function pageItem(toolCallId: string, pageNumber: number, url: string): MediaAttachment {
  return {
    type: 'image',
    url,
    filename: `page-${pageNumber}.png`,
    contentType: 'image/png',
    toolCallId,
    presentation: 'page_thumbnails',
    pageNumber,
    preview: PREVIEW_COMPLETE,
  }
}

describe('AssistantMediaList preview groups (§5.2–§5.4)', () => {
  it('renders inline file items as the shared keyboard-operable ChatFileAttachment card', () => {
    render(<AssistantMediaList media={[fileItem('notes.docx'), fileItem('spec.pdf')]} />)
    const cards = screen.getAllByTestId('chat-file-attachment')
    expect(cards).toHaveLength(2)
    expect(screen.getByText('notes.docx')).toBeDefined()
    expect(screen.getByText('spec.pdf')).toBeDefined()
    // FR-002/FR-081: the download control is keyboard-operable (a real
    // button) and named for its file.
    const downloads = screen.getAllByTestId('chat-file-download')
    expect(downloads).toHaveLength(2)
    expect(downloads.every((button) => button.tagName === 'BUTTON')).toBe(true)
    expect(downloads.map((button) => button.getAttribute('aria-label'))).toEqual([
      'Download notes.docx',
      'Download spec.pdf',
    ])
  })

  it('groups page_thumbnails per tool call at the first-item position, pages in page order', () => {
    // Deliberately OUT of page order: the list must present pages 1, 2.
    const media = [
      pageItem('call-1', 2, '/api/v1/media/p2'),
      fileItem('notes.docx'),
      pageItem('call-1', 1, '/api/v1/media/p1'),
      pageItem('call-2', 1, '/api/v1/media/p1'),
    ]
    render(<AssistantMediaList media={media} />)
    const groups = screen.getAllByTestId('preview-thumbnail-group')
    expect(groups).toHaveLength(2)
    expect(groups[0].getAttribute('data-preview-group')).toBe('call-1')
    expect(groups[0].getAttribute('data-outcome')).toBe('complete')
    expect(groups[1].getAttribute('data-preview-group')).toBe('call-2')
    // The inline file card renders AFTER call-1's group (first-item position),
    // before call-2's — §5.2: a group is placed where its first item appeared.
    const container = groups[0].parentElement as HTMLElement
    const kinds = Array.from(container.children).map((child) => {
      if (child.getAttribute('data-preview-group')) return child.getAttribute('data-preview-group')
      const isFile = child.matches('[data-testid="chat-file-attachment"]') || child.querySelector('[data-testid="chat-file-attachment"]')
      return isFile ? 'file' : 'other'
    })
    expect(kinds).toEqual(['call-1', 'file', 'call-2'])
    // Page order within the first group, from the caption paragraphs.
    const captions = within(groups[0])
      .getAllByText(/report\.docx · page \d of 12/)
      .filter((el) => el.tagName === 'P')
    expect(captions).toHaveLength(2)
    expect(captions[0].textContent).toBe('report.docx · page 1 of 12')
    expect(captions[1].textContent).toBe('report.docx · page 2 of 12')
  })

  it('renders an unavailable outcome as the persistent unavailable card with its reason', () => {
    const media: MediaAttachment[] = [
      {
        type: 'image',
        url: '',
        filename: 'report.docx',
        contentType: '',
        toolCallId: 'call-3',
        presentation: 'page_thumbnails',
        preview: { ...PREVIEW_COMPLETE, outcome: 'unavailable', unavailable_reason: 'renderer_missing' },
        unavailableReason: 'renderer_missing',
      },
    ]
    render(<AssistantMediaList media={media} />)
    const group = screen.getByTestId('preview-thumbnail-group')
    expect(group.getAttribute('data-outcome')).toBe('unavailable')
    expect(screen.getByTestId('preview-unavailable-card')).toBeDefined()
    expect(screen.getByText('Page previews unavailable — the PDF renderer is not installed')).toBeDefined()
    expect(screen.queryByTestId('chat-file-attachment')).toBeNull()
  })
})
