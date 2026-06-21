/**
 * Interaction tests for EdgeModeEditor + EdgeLabelChip — the per-edge delegation
 * modes/depth editor. These render plain DOM (no React Flow canvas), so the
 * edit-edge behaviour required by the Team-tab spec is deterministically
 * testable: toggling a mode chip, setting depth, deleting, and the
 * last-remaining-mode guard.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { EdgeModeEditor, EdgeLabelChip } from './EdgeModeEditor'
import type { TeamEdgeModel } from './teamGraphModel'

function edge(over: Partial<TeamEdgeModel> = {}): TeamEdgeModel {
  return {
    id: 'mia->jim',
    from: 'mia',
    to: 'jim',
    modes: ['await', 'background'],
    depth: undefined,
    unknownEndpoint: false,
    ...over,
  }
}

function renderEditor(model: TeamEdgeModel) {
  const onToggleMode = vi.fn()
  const onSetDepth = vi.fn()
  const onDelete = vi.fn()
  const onClose = vi.fn()
  render(
    <EdgeModeEditor
      model={model}
      onToggleMode={onToggleMode}
      onSetDepth={onSetDepth}
      onDelete={onDelete}
      onClose={onClose}
    />,
  )
  return { onToggleMode, onSetDepth, onDelete, onClose }
}

describe('EdgeModeEditor — modes', () => {
  it('shows enabled modes as pressed and disabled ones as not pressed', () => {
    renderEditor(edge({ modes: ['await'] }))
    expect(screen.getByTestId('team-edge-mode-await')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('team-edge-mode-background')).toHaveAttribute('aria-pressed', 'false')
    expect(screen.getByTestId('team-edge-mode-task')).toHaveAttribute('aria-pressed', 'false')
  })

  it('toggling a mode calls onToggleMode with (from, to, mode)', () => {
    const { onToggleMode } = renderEditor(edge({ modes: ['await', 'background'] }))
    fireEvent.click(screen.getByTestId('team-edge-mode-task'))
    expect(onToggleMode).toHaveBeenCalledWith('mia', 'jim', 'task')
  })

  it('disables the last remaining mode (no empty-modes / "all allowed" trap)', () => {
    renderEditor(edge({ modes: ['await'] }))
    const last = screen.getByTestId('team-edge-mode-await')
    expect(last).toBeDisabled()
  })
})

describe('EdgeModeEditor — depth', () => {
  it('shows the current depth and reports a new positive cap', () => {
    const { onSetDepth } = renderEditor(edge({ depth: 2 }))
    const input = screen.getByTestId('team-edge-depth') as HTMLInputElement
    expect(input.value).toBe('2')
    fireEvent.change(input, { target: { value: '5' } })
    expect(onSetDepth).toHaveBeenCalledWith('mia', 'jim', 5)
  })

  it('clearing the depth reports undefined (inherit default)', () => {
    const { onSetDepth } = renderEditor(edge({ depth: 3 }))
    fireEvent.change(screen.getByTestId('team-edge-depth'), { target: { value: '' } })
    expect(onSetDepth).toHaveBeenCalledWith('mia', 'jim', undefined)
  })
})

describe('EdgeModeEditor — delete + close', () => {
  it('delete calls onDelete with (from, to)', () => {
    const { onDelete } = renderEditor(edge())
    fireEvent.click(screen.getByTestId('team-edge-delete-mia-jim'))
    expect(onDelete).toHaveBeenCalledWith('mia', 'jim')
  })

  it('close button calls onClose', () => {
    const { onClose } = renderEditor(edge())
    fireEvent.click(screen.getByLabelText('Close edge editor'))
    expect(onClose).toHaveBeenCalled()
  })
})

describe('EdgeLabelChip', () => {
  it('renders the modes and opens on click', () => {
    const onClick = vi.fn()
    render(<EdgeLabelChip model={edge({ modes: ['await', 'task'], depth: 2 })} onClick={onClick} />)
    const btn = screen.getByLabelText('Edit delegation mia to jim')
    expect(btn).toHaveTextContent('await')
    expect(btn).toHaveTextContent('task')
    expect(btn).toHaveTextContent('2')
    fireEvent.click(btn)
    expect(onClick).toHaveBeenCalled()
  })

  it('shows "all modes" when the edge has no modes', () => {
    render(<EdgeLabelChip model={edge({ modes: [] })} onClick={() => {}} />)
    expect(screen.getByText('all modes')).toBeInTheDocument()
  })
})
