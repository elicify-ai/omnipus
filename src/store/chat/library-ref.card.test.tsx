/**
 * T7: library_ref copied by the media reducer is what the card opens.
 */
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { AssistantMediaItem } from '@/components/chat/AssistantMediaItem'
import { useUiStore } from '@/store/ui'
import { emptySessionState } from './session'
import { applyMediaFrame } from './slices/media-frames'
import type { SessionChatState } from './types'

describe('library_ref reaches the file card (BS-5.1)', () => {
  it('keeps the reducer copy and opens that workspace path', () => {
    let bucket: SessionChatState = emptySessionState()
    applyMediaFrame({
      type: 'media',
      session_id: 'sess-1',
      parts: [{
        type: 'file',
        url: '/api/v1/media/plan.md',
        filename: 'plan.md',
        library_ref: { workspace_id: 'W', path: 'notes/plan.md' },
      }],
    } as never, 'sess-1', (_sid, updater) => {
      bucket = { ...bucket, ...updater(bucket) }
    })
    const attachment = Object.values(bucket.messagesById)[0]?.media?.[0]
    expect(attachment?.libraryRef).toEqual({ workspace_id: 'W', path: 'notes/plan.md' })
    expect(attachment?.url).toBe('/api/v1/media/plan.md')

    useUiStore.getState().closePanel()
    render(
      <AssistantMediaItem
        kind="file"
        url={attachment?.url ?? ''}
        filename={attachment?.filename ?? ''}
        libraryRef={attachment?.libraryRef}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Open plan.md in Library' }))
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'W', path: 'notes/plan.md' },
    })
  })

  it('does not add the button when the part has no library_ref (BS-5.2)', () => {
    render(<AssistantMediaItem kind="file" url="/api/v1/media/plain.md" filename="plain.md" />)
    expect(screen.queryByRole('button', { name: /Open plain.md in Library/ })).toBeNull()
  })
})
