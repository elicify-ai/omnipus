/**
 * ProviderValidationBanner.test.tsx — Test #23 (spec TDD plan)
 *
 * Spec: provider-validation-centralization-spec.md, US8 / D3 / R-H/m2.
 *
 * Verifies:
 *   - Correct Phosphor icon rendered per outcome (wallet / wifi-slash / lock)
 *   - Server-provided message is shown as copy
 *   - Nothing rendered for valid outcome
 *   - Nothing rendered when validation is absent (undefined / null)
 */

import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ProviderValidationBanner } from './ProviderValidationBanner'
import type { ProviderValidation } from '@/lib/api/generated/openapi-types'

describe('ProviderValidationBanner', () => {
  // ── No-render cases ───────────────────────────────────────────────────────

  it('renders nothing when validation is undefined', () => {
    const { container } = render(<ProviderValidationBanner validation={undefined} />)
    expect(container.firstChild).toBeNull()
  })

  it('renders nothing when validation is null', () => {
    const { container } = render(<ProviderValidationBanner validation={null} />)
    expect(container.firstChild).toBeNull()
  })

  it('renders nothing when outcome is valid', () => {
    const v: ProviderValidation = { outcome: 'valid' }
    const { container } = render(<ProviderValidationBanner validation={v} />)
    expect(container.firstChild).toBeNull()
  })

  // ── Banner presence ───────────────────────────────────────────────────────

  it('renders an amber banner for no_credit', () => {
    const v: ProviderValidation = {
      outcome: 'no_credit',
      message: 'Your key works, but the account has no credit.',
    }
    render(<ProviderValidationBanner validation={v} />)
    const banner = screen.getByTestId('provider-validation-banner')
    expect(banner).toBeInTheDocument()
    expect(banner).toHaveAttribute('data-outcome', 'no_credit')
    // Amber styling check — border and bg applied
    expect(banner.className).toContain('amber')
    // Message from server is shown
    expect(screen.getByText('Your key works, but the account has no credit.')).toBeInTheDocument()
  })

  it('renders an amber banner for unreachable', () => {
    const v: ProviderValidation = {
      outcome: 'unreachable',
      message: "Couldn't reach OpenRouter to check the key.",
    }
    render(<ProviderValidationBanner validation={v} />)
    const banner = screen.getByTestId('provider-validation-banner')
    expect(banner).toHaveAttribute('data-outcome', 'unreachable')
    expect(screen.getByText("Couldn't reach OpenRouter to check the key.")).toBeInTheDocument()
  })

  it('renders an amber banner for restricted', () => {
    const v: ProviderValidation = {
      outcome: 'restricted',
      message: 'The request was blocked in your region.',
    }
    render(<ProviderValidationBanner validation={v} />)
    const banner = screen.getByTestId('provider-validation-banner')
    expect(banner).toHaveAttribute('data-outcome', 'restricted')
    expect(screen.getByText('The request was blocked in your region.')).toBeInTheDocument()
  })

  // ── Per-outcome icon ──────────────────────────────────────────────────────

  it('shows the wallet icon for no_credit', () => {
    const v: ProviderValidation = { outcome: 'no_credit', message: 'No credit.' }
    render(<ProviderValidationBanner validation={v} />)
    // Icon uses data-testid="banner-icon-wallet"
    expect(screen.getByTestId('banner-icon-wallet')).toBeInTheDocument()
    expect(screen.queryByTestId('banner-icon-wifi-slash')).not.toBeInTheDocument()
    expect(screen.queryByTestId('banner-icon-lock')).not.toBeInTheDocument()
  })

  it('shows the wifi-slash icon for unreachable', () => {
    const v: ProviderValidation = { outcome: 'unreachable', message: 'Unreachable.' }
    render(<ProviderValidationBanner validation={v} />)
    expect(screen.getByTestId('banner-icon-wifi-slash')).toBeInTheDocument()
    expect(screen.queryByTestId('banner-icon-wallet')).not.toBeInTheDocument()
    expect(screen.queryByTestId('banner-icon-lock')).not.toBeInTheDocument()
  })

  it('shows the lock icon for restricted', () => {
    const v: ProviderValidation = { outcome: 'restricted', message: 'Restricted.' }
    render(<ProviderValidationBanner validation={v} />)
    expect(screen.getByTestId('banner-icon-lock')).toBeInTheDocument()
    expect(screen.queryByTestId('banner-icon-wallet')).not.toBeInTheDocument()
    expect(screen.queryByTestId('banner-icon-wifi-slash')).not.toBeInTheDocument()
  })

  // ── Fallback copy when message is absent ──────────────────────────────────

  it('falls back to built-in copy when server omits message for no_credit', () => {
    const v: ProviderValidation = { outcome: 'no_credit' }
    render(<ProviderValidationBanner validation={v} />)
    const banner = screen.getByTestId('provider-validation-banner')
    expect(banner.textContent).toBeTruthy()
    // Should still render something meaningful (the fallback copy)
    expect(banner.textContent?.length).toBeGreaterThan(5)
  })

  // ── accessible role ───────────────────────────────────────────────────────

  it('has role=status for non-blocking announcement', () => {
    const v: ProviderValidation = { outcome: 'no_credit', message: 'test' }
    render(<ProviderValidationBanner validation={v} />)
    expect(screen.getByRole('status')).toBeInTheDocument()
  })
})
