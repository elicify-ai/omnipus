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
  it('previews the chosen figure, role and colour and does not offer an upload', async () => {
    const onSubmit = vi.fn()
    render(<CreateAgentWizard initialType="Main" onSubmit={onSubmit} onClose={vi.fn()} connectedProviders={[]} />)
    // The role is ONE searchable dropdown (founder 2026-10-10): each option
    // shows its badge; picking one previews the badge on the mark.
    expect(screen.getByRole('button', { name: 'Woman' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Role badge' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Woman' }))
    fireEvent.click(screen.getByRole('combobox', { name: 'Role badge' }))
    fireEvent.click(await screen.findByRole('option', { name: 'Writer' }))
    fireEvent.click(screen.getByRole('button', { name: 'Sky' }))
    const preview = document.querySelector('[data-art]')
    expect(preview?.getAttribute('data-art')).toBe('woman')
    expect(document.querySelector('[data-role="writer"]')).not.toBeNull()
    for (const swatch of PALETTE) {
      fireEvent.click(screen.getByRole('button', { name: swatch.name }))
      const mark = document.querySelector('[data-testid="agent-icon"]')
      expect(mark, `${swatch.name} preview mark`).not.toBeNull()
      // Independently derive CSS RGB from the spec's literal hex table,
      // never from the picker state or AgentIcon's supplied props.
      const channels = [1, 3, 5].map((offset) => Number.parseInt(swatch.hex.slice(offset, offset + 2), 16))
      expect(getComputedStyle(mark as Element).color, `${swatch.name} painted preview ink`).toBe(`rgb(${channels.join(', ')})`)
      expect(mark?.querySelector('[data-ink] svg')).toHaveAttribute('fill', 'currentColor')
      expect(mark?.querySelector('[data-ink]')).toHaveStyle({ opacity: '1' })
      expect(mark?.querySelector('[data-art="woman"]')).not.toBeNull()
      expect(mark?.querySelector('[data-role="writer"]')).not.toBeNull()
      expect(onSubmit, `${swatch.name} is an unsaved draft`).not.toHaveBeenCalled()
    }
    expect(screen.queryByLabelText(/upload/i)).not.toBeInTheDocument()
    expect(document.querySelector('input[type="file"]')).toBeNull()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('keeps the chosen preview draft and reports the exact save error without closing after rejection', async () => {
    // BDD-06.3: failed saving is not saved/activated identity. The submit
    // callback is the external persistence edge; the wizard stays real.
    const onSubmit = vi.fn().mockRejectedValue(new Error('Identity save denied'))
    const onClose = vi.fn()
    render(<CreateAgentWizard initialType="Subagent" onSubmit={onSubmit} onClose={onClose} connectedProviders={[]} />)
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'Draft helper' } })
    fireEvent.change(screen.getByTestId('wizard-description'), { target: { value: 'Writes release notes' } })
    fireEvent.click(screen.getByTestId('wizard-inherit-model'))
    fireEvent.click(screen.getByRole('button', { name: 'Woman' }))
    fireEvent.click(screen.getByRole('combobox', { name: 'Role badge' }))
    fireEvent.click(await screen.findByRole('option', { name: 'Writer' }))
    fireEvent.click(screen.getByRole('button', { name: 'Sky' }))
    expect(onSubmit).not.toHaveBeenCalled()
    fireEvent.click(screen.getByTestId('wizard-next-1'))
    fireEvent.change(await screen.findByTestId('wizard-soul'), { target: { value: 'Write clear release notes.' } })
    fireEvent.click(screen.getByTestId('wizard-next-2'))
    fireEvent.click(await screen.findByTestId('wizard-create'))
    expect(await screen.findByTestId('wizard-submit-error')).toHaveTextContent('Identity save denied')
    expect(screen.getByRole('alert')).toHaveTextContent('Identity save denied')
    expect(onSubmit).toHaveBeenCalledTimes(1)
    expect(onSubmit).toHaveBeenCalledWith(expect.objectContaining({ figure: 'Woman', role: 'writer', color: '#38BDF8' }))
    expect(onClose).not.toHaveBeenCalled()
    expect(screen.getByTestId('wizard-stepper')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('wizard-back'))
    fireEvent.click(screen.getByTestId('wizard-back'))
    expect(await screen.findByTestId('wizard-name')).toHaveValue('Draft helper')
    const mark = document.querySelector('[data-testid="agent-icon"]')
    expect(mark).not.toBeNull()
    expect(getComputedStyle(mark as Element).color).toBe('rgb(56, 189, 248)') // Spec Sky hex -> RGB.
    expect(mark?.querySelector('[data-art="woman"]')).not.toBeNull()
    expect(mark?.querySelector('[data-role="writer"]')).not.toBeNull()
  })
})

// The 5th figure, Monogram, in the create wizard. Oracles are the picker
// thumbnails and the preview's data-initial seam (ARCH-RULING-monogram D2b/D3).
describe('create wizard Monogram figure', () => {
  it('offers exactly the five figure choices in the founder order (AC-13, founder 2026-10-10)', () => {
    render(<CreateAgentWizard initialType="Main" onSubmit={vi.fn()} onClose={vi.fn()} connectedProviders={[]} />)
    // The Look row is ONE row of five picture thumbnails (founder 2026-10-10:
    // "order Omnipus, Man, Robot, Woman, Monogram" — the pre-decision order
    // [Robot, Man, Woman, Omnipus, Monogram] is superseded). Each thumbnail
    // is a picture (an AgentIcon), so the stable handle is its test id.
    const figureWords = ['Omnipus', 'Man', 'Robot', 'Woman', 'Monogram']
    const thumbnails = screen.getAllByTestId(/^wizard-figure-/)
    expect(thumbnails.map((button) => button.getAttribute('data-testid'))).toEqual(
      figureWords.map((figure) => `wizard-figure-${figure}`),
    )
    for (const figure of figureWords) {
      expect(screen.getByRole('button', { name: figure })).toBeEnabled()
    }
  })

  it('shows the Monogram initial in the preview and tracks the name as typed (AC-14)', () => {
    render(<CreateAgentWizard initialType="Main" onSubmit={vi.fn()} onClose={vi.fn()} connectedProviders={[]} />)
    fireEvent.click(screen.getByRole('button', { name: 'Monogram' }))
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'Research Assistant' } })
    const mark = document.querySelector('[data-testid="agent-icon"]')
    expect(mark).not.toBeNull()
    expect(mark).toHaveAttribute('data-figure', 'Monogram')
    const letter = document.querySelector('[data-initial]')
    expect(letter, 'preview letter node').not.toBeNull()
    // Attribute AND painted text (F-1): a wrong painted glyph must fail RED.
    expect(letter!.tagName.toLowerCase(), 'preview letter is an SVG <text>').toBe('text')
    expect(letter!.getAttribute('data-initial')).toBe('R')
    expect(letter!.textContent, 'painted preview initial').toBe('R')
    // The preview initial tracks the name as it is typed.
    fireEvent.change(screen.getByTestId('wizard-name'), { target: { value: 'Zeta' } })
    const updated = document.querySelector('[data-initial]')
    expect(updated!.tagName.toLowerCase()).toBe('text')
    expect(updated!.getAttribute('data-initial')).toBe('Z')
    expect(updated!.textContent, 'painted preview initial after typing').toBe('Z')
  })
})
