/**
 * RED: the extracted assistant file card shows "Open in Library" when
 * library_ref is set (BS-5.1 / FR-022). The component does not exist yet, so
 * the import fails with BLOCKED instead of a collection error.
 */
import type { ReactNode } from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { useUiStore } from '@/store/ui'

describe('AssistantMediaItem Open in Library', () => {
  it('opens the Library when the file button is activated (BS-5.1 / BS-5.5)', async () => {
    let Item: (props: {
      filename: string
      url: string
      libraryRef?: { workspace_id: string; path: string }
      kind: 'file' | 'image'
    }) => ReactNode
    try {
      const spec = './AssistantMediaItem'
      const mod = await import(/* @vite-ignore */ spec) as { AssistantMediaItem?: typeof Item }
      if (typeof mod.AssistantMediaItem !== 'function') throw new Error('missing export')
      Item = mod.AssistantMediaItem
    } catch (err) {
      throw new Error(
        `BLOCKED: AssistantMediaItem not implemented — required by FR-022 / BS-5.1 (${err instanceof Error ? err.message : String(err)})`,
        { cause: err },
      )
    }
    useUiStore.getState().closePanel()
    render(
      <Item
        filename="plan.md"
        url="/api/v1/media/plan.md"
        kind="file"
        libraryRef={{ workspace_id: 'W', path: 'notes/plan.md' }}
      />,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Open plan.md in Library' }))
    expect(useUiStore.getState().activePanel).toEqual({
      id: 'library',
      context: { workspaceId: 'W', path: 'notes/plan.md' },
    })
    // A non-image file is a card with a download button that fetches the file
    // (docs/using-omnipus-ui.md). The button has no href; the address is the
    // media URL the card was given (BS-5.1: the card shows download).
    await expectFileDownload('/api/v1/media/plan.md', 'plan.md')
  })
})

/** Download is a button named "Download <filename>" that fetches the media URL. */
async function expectFileDownload(url: string, filename: string) {
  const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response(new Blob(['bytes']), { status: 200 }))
  const createObjectUrl = vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:chat-file')
  const revokeObjectUrl = vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
  try {
    fireEvent.click(screen.getByRole('button', { name: `Download ${filename}` }))
    await waitFor(() => {
      expect(fetchSpy).toHaveBeenCalledWith(url, { credentials: 'same-origin' })
    })
  } finally {
    fetchSpy.mockRestore()
    createObjectUrl.mockRestore()
    revokeObjectUrl.mockRestore()
  }
}
