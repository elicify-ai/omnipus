// The screen reader says the same sentence as the card. For storage_failed
// that sentence is "Page previews could not be saved", not "unavailable".
// Other reasons still announce "Page previews unavailable".

import { act } from 'react'
import { beforeEach, describe, expect, it } from 'vitest'
import { render, screen } from '@testing-library/react'
import { useChatStore, type ChatMessage } from '@/store/chat'
import { useSessionStore } from '@/store/session'
import type { MediaAttachment } from '@/store/chat'
import type { MediaPreview } from '@/lib/api/generated/asyncapi-types'
import { PreviewAnnouncer } from './PreviewAnnouncer'

function group(reason: NonNullable<MediaPreview['unavailable_reason']>): MediaAttachment {
  return {
    type: 'image',
    url: '',
    filename: 'report.docx',
    contentType: '',
    toolCallId: `call-${reason}`,
    presentation: 'page_thumbnails',
    preview: {
      source_name: 'report.docx',
      source_version: 'gatefix',
      page_count: 1,
      first_page: 1,
      last_page: 1,
      outcome: 'unavailable',
      unavailable_reason: reason,
    },
    unavailableReason: reason,
  }
}

function arrive(reason: NonNullable<MediaPreview['unavailable_reason']>) {
  render(<PreviewAnnouncer />)
  act(() => {
    useChatStore.setState({
      messages: [
        {
          id: `msg-${reason}`,
          role: 'assistant',
          content: '',
          timestamp: '2026-11-19T00:00:00Z',
          status: 'done',
          media: [group(reason)],
        } as ChatMessage,
      ],
      isReplaying: false,
    })
  })
  return screen.getByTestId('preview-announcement')
}

beforeEach(() => {
  useSessionStore.setState({ activeSessionId: 'sess-gatefix' })
  useChatStore.setState({ messages: [], isReplaying: false })
})

describe('PreviewAnnouncer storage_failed announcement', () => {
  it('announces the same words as the visible headline, not unavailable', () => {
    const live = arrive('storage_failed')
    expect(live.textContent).toBe('Page previews could not be saved')
    expect(live.textContent?.toLowerCase()).not.toContain('unavailable')
  })

  it('still announces other reasons as unavailable', () => {
    const live = arrive('renderer_failed')
    expect(live.textContent).toBe('Page previews unavailable')
  })
})
