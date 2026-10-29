/**
 * RED: the extracted assistant file card shows "Open in Library" when
 * library_ref is set (BS-5.1 / FR-022). The component does not exist yet, so
 * the import fails with BLOCKED instead of a collection error.
 */
import type { ReactNode } from 'react'
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'

describe('AssistantMediaItem Open in Library', () => {
  it('names the file on the button and does not add the button to images (BS-5.1 / BS-5.6)', async () => {
    let Item: (props: {
      filename: string
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
      )
    }
    render(
      <Item
        filename="plan.md"
        kind="file"
        libraryRef={{ workspace_id: 'W', path: 'notes/plan.md' }}
      />,
    )
    expect(screen.getByRole('button', { name: 'Open plan.md in Library' })).toBeInTheDocument()
    render(<Item filename="pic.png" kind="image" libraryRef={{ workspace_id: 'W', path: 'pic.png' }} />)
    expect(screen.queryByRole('button', { name: 'Open pic.png in Library' })).toBeNull()
  })
})
