/**
 * RED: the docked Library receives the path it was opened with (BS-1.17 / U6).
 * Fullscreen already passes `address`. The docked branch must hand over the
 * same address. This file does not change LibraryPanel.test.tsx.
 */
import { useEffect } from 'react'
import { render } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useUiStore } from '@/store/ui'

const explorerProps = vi.fn()
vi.mock('./LibraryExplorer', () => ({
  LibraryExplorer: (props: { address?: { workspaceId?: string; path?: string } }) => {
    explorerProps(props)
    useEffect(() => {}, [])
    return <div data-testid="mock-library-explorer" />
  },
}))

import { LibraryPanel } from './LibraryPanel'

beforeEach(() => {
  explorerProps.mockClear()
  useUiStore.getState().closePanel()
})

describe('docked Library path', () => {
  it('hands the opened address to the explorer (BS-1.17)', () => {
    useUiStore.getState().openPanel('library', { workspaceId: 'W', path: 'notes/plan.md' })
    render(<LibraryPanel />)
    expect(explorerProps).toHaveBeenCalledWith(
      expect.objectContaining({
        address: { workspaceId: 'W', path: 'notes/plan.md' },
      }),
    )
  })
})
