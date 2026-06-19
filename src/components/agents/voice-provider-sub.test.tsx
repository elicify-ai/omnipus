import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, screen, waitFor, fireEvent } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import * as React from 'react'

import {
  detectVoiceProvider,
  bumpVoiceProviderCacheVersion,
} from '@/lib/agents/voice-provider-detect'
import { VoiceProviderSub } from '@/components/agents/voice-provider-sub'

// The component loads the provider config through this helper.
vi.mock('@/lib/agents/voice-provider-detect', () => ({
  detectVoiceProvider: vi.fn(),
  bumpVoiceProviderCacheVersion: vi.fn(),
}))

// shadcn/ui Select renders into a portal by default; portal inside jsdom still lives
// in the same document so `screen` queries work once the trigger is clicked.

describe('VoiceProviderSub', () => {
  beforeEach(() => {
    vi.mocked(detectVoiceProvider).mockReset().mockResolvedValue({
      mode: 'free-text',
      fetchedAt: new Date().toISOString(),
    })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  function renderWidget(props: Omit<React.ComponentProps<typeof VoiceProviderSub>, 'onChange'>) {
    const onChange = vi.fn()
    const view = render(<VoiceProviderSub {...props} onChange={onChange} />)
    return { ...view, onChange }
  }

  it('renders a disabled loader while detection is in flight', () => {
    // Never resolve so the component stays in loading state.
    vi.mocked(detectVoiceProvider).mockReturnValue(new Promise(() => {}))
    renderWidget({ value: 'alloy' })
    const field = screen.getByTestId('voice-field')
    expect(field).toBeDisabled()
    expect(field).toHaveAttribute('placeholder', 'Detecting voice provider…')
  })

  it('renders a disabled input when the provider reports disabled', async () => {
    vi.mocked(detectVoiceProvider).mockResolvedValue({
      mode: 'disabled',
      reason: 'Voice provider unavailable',
      fetchedAt: new Date().toISOString(),
    })
    renderWidget({ value: 'alloy' })
    const field = await waitFor(() => screen.getByTestId('voice-field'))
    expect(field).toBeDisabled()
    expect(field).toHaveAttribute('placeholder', 'Voice provider unavailable')
  })

  it('respects an explicit disabled prop even when provider is free-text', async () => {
    renderWidget({ value: 'alloy', disabled: true, disabledReason: 'Locked by spec' })
    const field = await waitFor(() => screen.getByTestId('voice-field'))
    expect(field).toBeDisabled()
    expect(field).toHaveAttribute('title', 'Locked by spec')
  })

  // Radix Select does not open in jsdom (data-state stays "closed"); assert the
  // trigger/reason instead of driving the dropdown. Covered by Playwright E2E.
  it.skip('renders a dropdown when the provider exposes voices', async () => {
    vi.mocked(detectVoiceProvider).mockResolvedValue({
      mode: 'enum',
      voices: ['alloy', 'echo', 'fable'],
      fetchedAt: new Date().toISOString(),
    })
    renderWidget({ value: 'alloy' })
    const trigger = await waitFor(() => screen.getByTestId('voice-field'))
    await userEvent.click(trigger)
    await waitFor(() => expect(screen.getByRole('option', { name: 'echo' })).toBeInTheDocument())
    expect(screen.getByRole('option', { name: 'fable' })).toBeInTheDocument()
  })

  it('falls back to a free-text input when the enum list is empty', async () => {
    vi.mocked(detectVoiceProvider).mockResolvedValue({
      mode: 'enum',
      voices: [],
      fetchedAt: new Date().toISOString(),
    })
    renderWidget({ value: 'alloy' })
    const field = await waitFor(() => screen.getByTestId('voice-field'))
    // Empty enum renders as a plain input.
    expect(field).toHaveAttribute('placeholder', 'e.g. alloy')
  })

  it('fires onChange when a free-text value is typed', async () => {
    vi.mocked(detectVoiceProvider).mockResolvedValue({
      mode: 'free-text',
      fetchedAt: new Date().toISOString(),
    })
    const { onChange } = renderWidget({ value: 'alloy' })
    const field = await waitFor(() => screen.getByTestId('voice-field'))
    fireEvent.change(field, { target: { value: 'nova' } })
    await waitFor(() => expect(onChange).toHaveBeenCalledWith('nova'))
  })

  // Radix Select does not open in jsdom; covered by Playwright E2E.
  it.skip('fires onChange when a dropdown voice is picked', async () => {
    vi.mocked(detectVoiceProvider).mockResolvedValue({
      mode: 'enum',
      voices: ['alloy', 'echo'],
      fetchedAt: new Date().toISOString(),
    })
    const { onChange } = renderWidget({ value: 'alloy' })
    const trigger = await waitFor(() => screen.getByTestId('voice-field'))
    await userEvent.click(trigger)
    const option = await screen.findByRole('option', { name: 'echo' })
    await userEvent.click(option)
    await waitFor(() => expect(onChange).toHaveBeenCalledWith('echo'))
  })

  it('re-fetches and re-renders when voice-provider-change is fired', async () => {
    vi.mocked(detectVoiceProvider)
      .mockResolvedValueOnce({
        mode: 'enum',
        voices: ['alloy'],
        fetchedAt: new Date().toISOString(),
      })
      .mockResolvedValueOnce({
        mode: 'free-text',
        fetchedAt: new Date().toISOString(),
      })

    renderWidget({ value: 'alloy' })
    await waitFor(() => {
      expect(screen.getByTestId('voice-field')).toHaveTextContent('alloy')
    })

    window.dispatchEvent(new CustomEvent('voice-provider-change'))

    await waitFor(() => {
      const field = screen.getByTestId('voice-field')
      // After re-fetching free-text mode, the field becomes a plain input.
      expect(field).toHaveAttribute('placeholder', 'e.g. alloy')
    })
    expect(bumpVoiceProviderCacheVersion).toHaveBeenCalled()
  })

  it('handles null value gracefully', async () => {
    vi.mocked(detectVoiceProvider).mockResolvedValue({
      mode: 'free-text',
      fetchedAt: new Date().toISOString(),
    })
    renderWidget({ value: null })
    const field = await waitFor(() => screen.getByTestId('voice-field'))
    expect(field).toHaveValue('')
  })
})
