/**
 * Follow-up 3: a workspace image sent with send_file gets Open in Library.
 * Today's card ignores libraryRef on images, so this stays red until that changes.
 */
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { useUiStore } from '@/store/ui'
import { AssistantMediaItem } from './AssistantMediaItem'

describe('workspace image Open in Library (follow-up 3)', () => {
  it('opens the image path from the card', () => {
    useUiStore.getState().closePanel()
    render(
      <AssistantMediaItem
        kind="image"
        url="/api/v1/media/pic.png"
        filename="pic.png"
        libraryRef={{ workspace_id: 'W', path: 'pics/pic.png' }}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Open pic.png in Library' }))
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'W', path: 'pics/pic.png' },
    })
  })
})
