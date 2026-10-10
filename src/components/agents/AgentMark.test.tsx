/**
 * AgentMark.test.tsx — the ONE mark (founder 2026-10-10: every surface draws
 * an agent with one mark: figure + role badge + colour).
 *
 * Oracles (never the implementation):
 *  - MESSAGES.md FOUNDER DECISIONS 2026-10-10 — one mark everywhere; the old
 *    per-agent Phosphor "icon" system is gone.
 *  - Earlier founder rulings — an agent that is not loaded draws the Omnipus
 *    figure (fallback); Monogram = the name's UPPERCASE first character; a
 *    name starting with a symbol or emoji draws "?".
 *  - ARCH-DECISIONS.md "Colour enum" — Sky #38BDF8, Grey #9CA3AF (create
 *    default). jsdom normalises inline hex colours to rgb(): #38BDF8 →
 *    rgb(56, 189, 248), #9CA3AF → rgb(156, 163, 175).
 */
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { AgentMark } from './AgentMark'
import { makeAgent } from '@/test/factories'

describe('AgentMark — the one mark', () => {
  it('draws a loaded agent with its own figure, role badge and colour', () => {
    render(<AgentMark agent={makeAgent({ name: 'Rivet', figure: 'Man', role: 'developer', color: '#38BDF8' })} size={40} />)
    const mark = screen.getByTestId('agent-icon')
    expect(mark).toHaveAttribute('data-figure', 'Man')
    // The role badge is part of the mark: the badge group carries the role.
    expect(mark.querySelector('g[data-role="developer"]')).not.toBeNull()
    // Sky #38BDF8 (palette order 2) as the ink colour.
    expect(mark).toHaveStyle({ color: 'rgb(56, 189, 248)' })
  })

  it('draws an agent that is not loaded as the Omnipus figure with the General badge in the create-default colour', () => {
    // Founder fallback ruling: unloaded ⇒ Omnipus, never a guessed figure.
    // Create defaults (docs/agents.md): Omnipus / general / #9CA3AF Grey.
    render(<AgentMark agent={undefined} name="Frame Name" size={26} />)
    const mark = screen.getByTestId('agent-icon')
    expect(mark).toHaveAttribute('data-figure', 'Omnipus')
    expect(mark.querySelector('g[data-role="general"]')).not.toBeNull()
    expect(mark).toHaveStyle({ color: 'rgb(156, 163, 175)' }) // Grey #9CA3AF
  })

  it('draws Monogram with the name initial, uppercased', () => {
    // Founder Q-A: Monogram = the name's UPPERCASE first character.
    render(<AgentMark agent={makeAgent({ name: 'ava', figure: 'Monogram' })} size={48} />)
    const mark = screen.getByTestId('agent-icon')
    expect(mark).toHaveAttribute('data-figure', 'Monogram')
    const letter = mark.querySelector('text[data-initial]')
    expect(letter).not.toBeNull()
    expect(letter?.getAttribute('data-initial')).toBe('A')
    expect(letter?.textContent).toBe('A')
  })

  it('draws Monogram for a symbol-leading name as "?"', () => {
    // Founder Q-B: a name starting with a symbol draws "?".
    render(<AgentMark agent={makeAgent({ name: '$ponsor', figure: 'Monogram' })} size={48} />)
    const letter = screen.getByTestId('agent-icon').querySelector('text[data-initial]')
    expect(letter?.getAttribute('data-initial')).toBe('?')
    expect(letter?.textContent).toBe('?')
  })

  it('draws Monogram for an emoji-leading name as "?"', () => {
    // Founder Q-B covers emoji: a astral-plane leading character is not a
    // letter or digit, so the mark must show "?" and not half a surrogate
    // pair (initialOf's code-point fix — docs/agents.md Monogram rule).
    render(<AgentMark agent={makeAgent({ name: '🎉party', figure: 'Monogram' })} size={48} />)
    const letter = screen.getByTestId('agent-icon').querySelector('text[data-initial]')
    expect(letter?.getAttribute('data-initial')).toBe('?')
  })

  it('labels the mark with the supplied name when the agent is not loaded', () => {
    // The name prop is the display name used when `agent` is undefined
    // (e.g. a name from a chat frame) — with decorative=false it becomes
    // the accessible name.
    render(<AgentMark agent={undefined} name="Frame Name" size={26} decorative={false} />)
    expect(screen.getByRole('img', { name: 'Frame Name' })).toBeInTheDocument()
  })
})
