/**
 * Interaction tests for EdgeModeEditor + EdgeLabelChip — the per-edge delegation
 * modes/depth editor. These render plain DOM (no React Flow canvas), so the
 * edit-edge behaviour required by the Team-tab spec is deterministically
 * testable: toggling a mode chip, setting depth, deleting, and the
 * last-remaining-mode guard.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { EdgeModeEditor, EdgeLabelChip, MODE_LABEL } from './EdgeModeEditor'
import type { TeamEdgeModel } from './teamGraphModel'

const DEFAULT_DEPTH = 3

function edge(over: Partial<TeamEdgeModel> = {}): TeamEdgeModel {
  return {
    id: 'mia->jim',
    from: 'mia',
    to: 'jim',
    modes: ['direct', 'task'],
    depth: undefined,
    unknownEndpoint: false,
    ...over,
  }
}

function renderEditor(model: TeamEdgeModel, defaultDepth: number = DEFAULT_DEPTH) {
  const onToggleMode = vi.fn()
  const onSetDepth = vi.fn()
  const onDelete = vi.fn()
  const onClose = vi.fn()
  render(
    <EdgeModeEditor
      model={model}
      defaultDepth={defaultDepth}
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
    renderEditor(edge({ modes: ['direct'] }))
    expect(screen.getByTestId('team-edge-mode-direct')).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByTestId('team-edge-mode-task')).toHaveAttribute('aria-pressed', 'false')
  })

  it('toggling a mode calls onToggleMode with (from, to, mode)', () => {
    const { onToggleMode } = renderEditor(edge({ modes: ['direct'] }))
    fireEvent.click(screen.getByTestId('team-edge-mode-task'))
    expect(onToggleMode).toHaveBeenCalledWith('mia', 'jim', 'task')
  })

  it('disables the last remaining mode (no empty-modes / "all allowed" trap)', () => {
    renderEditor(edge({ modes: ['direct'] }))
    const last = screen.getByTestId('team-edge-mode-direct')
    expect(last).toBeDisabled()
  })

  it('renders the human-readable MODE_LABEL, not the raw enum value', () => {
    renderEditor(edge({ modes: ['direct'] }))
    expect(screen.getByTestId('team-edge-mode-direct')).toHaveTextContent(MODE_LABEL.direct)
    expect(screen.getByTestId('team-edge-mode-task')).toHaveTextContent(MODE_LABEL.task)
    expect(screen.getByTestId('team-edge-mode-direct')).toHaveAttribute(
      'aria-label',
      MODE_LABEL.direct,
    )
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

  it('pre-fills the concrete defaultDepth when the edge has no depth of its own', () => {
    renderEditor(edge({ depth: undefined }), 7)
    expect((screen.getByTestId('team-edge-depth') as HTMLInputElement).value).toBe('7')
  })

  it('has no "∞" placeholder — depth is always a concrete number now', () => {
    renderEditor(edge({ depth: undefined }))
    const input = screen.getByTestId('team-edge-depth') as HTMLInputElement
    expect(input.placeholder).toBe('')
  })

  it('clearing the depth still reports undefined to the caller (the model layer resolves it)', () => {
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
    render(
      <EdgeLabelChip
        model={edge({ modes: ['direct', 'task'], depth: 2 })}
        defaultDepth={DEFAULT_DEPTH}
        onClick={onClick}
      />,
    )
    const btn = screen.getByLabelText('Edit delegation mia to jim')
    expect(btn).toHaveTextContent('direct')
    expect(btn).toHaveTextContent('task')
    expect(btn).toHaveTextContent('2')
    fireEvent.click(btn)
    expect(onClick).toHaveBeenCalled()
  })

  it('shows "all modes" when the edge has no modes', () => {
    render(
      <EdgeLabelChip model={edge({ modes: [] })} defaultDepth={DEFAULT_DEPTH} onClick={() => {}} />,
    )
    expect(screen.getByText('all modes')).toBeInTheDocument()
  })

  it('always shows a concrete depth badge, falling back to defaultDepth when the edge has none', () => {
    render(
      <EdgeLabelChip
        model={edge({ modes: ['direct'], depth: undefined })}
        defaultDepth={9}
        onClick={() => {}}
      />,
    )
    expect(screen.getByLabelText('Edit delegation mia to jim')).toHaveTextContent('9')
  })
})
