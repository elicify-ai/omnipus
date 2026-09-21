// LibraryMermaidPreview.test.tsx — proves the Library diagram preview gets
// its full-screen action FOR FREE, by reusing <MermaidDiagram> verbatim
// (the file's own header comment: "a drop-in, not a second diagram-rendering
// path"). D18's scope extension
// (docs/internal/design/components/zoomable-view.md) asks for "the same new
// full-screen button" as chat's Mermaid diagrams — since LibraryMermaidPreview
// renders the SAME MermaidDiagram component chat markdown does, that is
// literally the same button, with no separate Library-only affordance to
// build or maintain.
//
// `LibraryTextPreview` is replaced with a passthrough shell (the same
// substitution LibraryMarkdownPreview.test.tsx uses) so this file's
// assertions are independent of `useLibraryFileEditor`'s save/version-token
// machinery, which is out of this lane's scope. `mermaid` itself is mocked,
// matching mermaid-renderer.test.tsx's own pattern; MermaidDiagram is real.

import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent, cleanup } from '@testing-library/react'
import type { ReactNode } from 'react'
import { LibraryMermaidPreview } from './LibraryMermaidPreview'
import type { LibraryEntry } from '@/lib/api'

const initialize = vi.fn()
const renderFn = vi.fn()
vi.mock('mermaid', () => ({ default: { initialize, render: renderFn } }))

vi.mock('./LibraryTextPreview', () => ({
  LibraryTextPreview: ({
    content,
    renderView,
  }: {
    content: string
    renderView: (draft: string, helpers: { switchToEdit: () => void }) => ReactNode
  }) => <div data-testid="text-preview-shell">{renderView(content, { switchToEdit: () => {} })}</div>,
}))

const ENTRY: LibraryEntry = {
  name: 'diagram.mmd',
  path: 'notes/diagram.mmd',
  is_dir: false,
  is_hidden: false,
  size: 128,
  modified_at: '2026-09-20T00:00:00Z',
  is_text_editable: true,
}

// A diagram wide enough that the pre-fix ImageLightbox (see
// image-lightbox.test.tsx's defect-1 regression) would have collapsed it to
// ~300px on enlarge — the same fixture shape, rendered through the Library
// preview this time.
const WIDE_DIAGRAM_SVG = '<svg viewBox="0 0 3000 200"><g id="node"/></svg>'

beforeEach(() => {
  cleanup()
  initialize.mockReset().mockReturnValue(undefined)
  renderFn.mockReset()
})

describe('LibraryMermaidPreview — reuses the chat MermaidDiagram verbatim (D18 scope extension)', () => {
  it('renders the diagram via the real MermaidDiagram component, not a Library-only renderer', async () => {
    renderFn.mockResolvedValue({ svg: WIDE_DIAGRAM_SVG })
    render(<LibraryMermaidPreview workspaceId="ws-1" entry={ENTRY} content="graph TD; A-->B" />)

    await waitFor(() => expect(document.querySelector('svg')).toBeInTheDocument())
    expect(renderFn).toHaveBeenCalledWith(expect.any(String), 'graph TD; A-->B')
  })

  it('the "Enlarge diagram" action opens the shared media viewer with the diagram\'s own svg — the same handoff chat uses', async () => {
    renderFn.mockResolvedValue({ svg: WIDE_DIAGRAM_SVG })
    const { useUiStore } = await import('@/store/ui')
    useUiStore.getState().closeMediaLightbox()

    render(<LibraryMermaidPreview workspaceId="ws-1" entry={ENTRY} content="graph TD; A-->B" />)
    await waitFor(() => expect(document.querySelector('svg')).toBeInTheDocument())

    expect(useUiStore.getState().mediaLightbox).toBeNull()
    fireEvent.click(screen.getByRole('button', { name: 'Enlarge diagram' }))

    const lb = useUiStore.getState().mediaLightbox
    expect(lb?.kind).toBe('svg')
    // The full wide viewBox survives the handoff — the viewer resolves its
    // real intrinsic size from this string (defect 1's fix), so passing it
    // through unmodified is what makes the Library preview eligible for the
    // exact same fix as chat, with no separate wiring.
    expect(lb?.kind === 'svg' && lb.svg).toContain('viewBox="0 0 3000 200"')
  })
})
