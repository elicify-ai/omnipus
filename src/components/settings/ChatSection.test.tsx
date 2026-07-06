/**
 * ChatSection tests.
 *
 * Covers the Settings → Chat tab's single "Verbose chat" toggle: it reflects
 * the real useChatPreferencesStore state on render, and toggling it calls the
 * store setter and updates the rendered checked state.
 */

import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, act, fireEvent } from '@testing-library/react'
import { useChatPreferencesStore } from '@/store/chatPreferences'
import { ChatSection } from './ChatSection'

beforeEach(() => {
  act(() => {
    useChatPreferencesStore.setState({ verboseChatEnabled: false })
  })
})

describe('ChatSection — renders current store state', () => {
  it('renders the switch unchecked when verboseChatEnabled is false', () => {
    render(<ChatSection />)
    const toggle = screen.getByRole('switch', { name: 'Verbose chat' })
    expect(toggle).toHaveAttribute('aria-checked', 'false')
  })

  it('renders the switch checked when verboseChatEnabled is true', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    render(<ChatSection />)
    const toggle = screen.getByRole('switch', { name: 'Verbose chat' })
    expect(toggle).toHaveAttribute('aria-checked', 'true')
  })

  it('renders the label and description text', () => {
    render(<ChatSection />)
    expect(screen.getByText('Verbose chat')).toBeInTheDocument()
    expect(screen.getByText(/Show every tool call in the transcript/)).toBeInTheDocument()
  })

  it('renders the local-preference helper text', () => {
    render(<ChatSection />)
    expect(screen.getByText(/local, per-device display preference/)).toBeInTheDocument()
  })
})

describe('ChatSection — toggling calls the store setter and updates rendered state', () => {
  it('clicking the switch flips verboseChatEnabled from false to true and re-renders checked', () => {
    render(<ChatSection />)
    const toggle = screen.getByRole('switch', { name: 'Verbose chat' })

    expect(toggle).toHaveAttribute('aria-checked', 'false')
    fireEvent.click(toggle)

    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(true)
    expect(screen.getByRole('switch', { name: 'Verbose chat' })).toHaveAttribute('aria-checked', 'true')
  })

  it('clicking the switch flips verboseChatEnabled from true to false and re-renders unchecked', () => {
    act(() => {
      useChatPreferencesStore.setState({ verboseChatEnabled: true })
    })
    render(<ChatSection />)
    const toggle = screen.getByRole('switch', { name: 'Verbose chat' })

    expect(toggle).toHaveAttribute('aria-checked', 'true')
    fireEvent.click(toggle)

    expect(useChatPreferencesStore.getState().verboseChatEnabled).toBe(false)
    expect(screen.getByRole('switch', { name: 'Verbose chat' })).toHaveAttribute('aria-checked', 'false')
  })
})
