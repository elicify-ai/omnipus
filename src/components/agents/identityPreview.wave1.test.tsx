import { describe, it, expect, vi, beforeAll, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { act } from 'react'
import { CreateAgentWizard } from './CreateAgentWizard'
import { AvatarColorPicker } from './AgentFormFields'
import { useUiStore } from '@/store/ui'

// FR-020 / BDD-06.1/06.2. The editor offers the locked vocabulary and a live
// AgentIcon preview. Draft choices are not a save. There is no upload control.
// Palette oracles are the spec's ten hexes, not AVATAR_COLORS.

const PALETTE: { name: string; hex: string }[] = [
  { name: 'Azure', hex: '#3B82F6' },
  { name: 'Sky', hex: '#38BDF8' },
  { name: 'Cyan', hex: '#22D3EE' },
  { name: 'Indigo', hex: '#818CF8' },
  { name: 'Violet', hex: '#A78BFA' },
  { name: 'Purple', hex: '#C084FC' },
  { name: 'Fuchsia', hex: '#E879F9' },
  { name: 'Pink', hex: '#F472B6' },
  { name: 'Orange', hex: '#FB923C' },
  { name: 'Grey', hex: '#9CA3AF' },
]

beforeAll(() => {
  if (typeof window !== 'undefined' && !window.ResizeObserver) {
    window.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  }
  if (!Element.prototype.scrollIntoView) Element.prototype.scrollIntoView = () => {}
})

beforeEach(() => {
  act(() => {
    useUiStore.setState({ createAgentModalOpen: false, toasts: [] })
  })
})

describe('identity colour choices', () => {
  it('offers the ten palette colours and not the retired brand swatches', () => {
    render(<AvatarColorPicker value="#3B82F6" onChange={vi.fn()} />)
    for (const swatch of PALETTE) {
      expect(screen.getByRole('button', { name: swatch.name })).toBeInTheDocument()
    }
    expect(screen.getAllByRole('button')).toHaveLength(10)
    for (const retired of ['Verdant', 'Saffron', 'Crimson', 'Forge Gold', 'Ember', 'Amethyst']) {
      expect(screen.queryByRole('button', { name: retired })).not.toBeInTheDocument()
    }
  })

  it('reports the canonical uppercase hex for a chosen swatch', () => {
    const onChange = vi.fn()
    render(<AvatarColorPicker value="#3B82F6" onChange={onChange} />)
    fireEvent.click(screen.getByRole('button', { name: 'Sky' }))
    expect(onChange).toHaveBeenCalledWith('#38BDF8')
  })
})

describe('create wizard identity preview', () => {
  it('previews the chosen figure, role and colour and does not offer an upload', () => {
    const onSubmit = vi.fn()
    render(<CreateAgentWizard initialType="Main" onSubmit={onSubmit} onClose={vi.fn()} connectedProviders={[]} />)
    expect(screen.getByRole('button', { name: 'Woman' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Writer' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Woman' }))
    fireEvent.click(screen.getByRole('button', { name: 'Writer' }))
    fireEvent.click(screen.getByRole('button', { name: 'Sky' }))
    const preview = document.querySelector('[data-art]')
    expect(preview?.getAttribute('data-art')).toBe('woman')
    expect(document.querySelector('[data-role="writer"]')).not.toBeNull()
    expect(screen.queryByLabelText(/upload/i)).not.toBeInTheDocument()
    expect(document.querySelector('input[type="file"]')).toBeNull()
    expect(onSubmit).not.toHaveBeenCalled()
  })
})
