/**
 * RiskySettingControl.revert.test.tsx — Issue #317 ACs (revert / persisted-value tests).
 *
 * Verifies that the standing "This lowers your protection" badge tracks the
 * PERSISTED value (the `currentValue` prop) — not internal dialog state.
 *
 * The caller owns persistence. These tests simulate the caller persisting or
 * reverting by re-rendering with a new `currentValue` prop.
 */

import { describe, it, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { RiskySettingControl } from './RiskySettingControl'

type Mode = 'strict' | 'permissive'

const OPTIONS = [
  { value: 'strict' as Mode, label: 'Strict' },
  { value: 'permissive' as Mode, label: 'Permissive' },
]

const COPY = {
  dialogTitle: 'Weaken protection?',
  dialogDescription: 'Switching to permissive reduces security.',
  confirmLabel: 'Switch to permissive',
  cancelLabel: 'Stay strict',
}

describe('RiskySettingControl — persisted-value-derived badge (revert tests)', () => {
  it('badge is NOT shown when the persisted value is safe on mount', () => {
    render(
      <RiskySettingControl
        options={OPTIONS}
        currentValue="strict"
        safeValue="strict"
        copy={COPY}
        onConfirm={vi.fn()}
        onSelectSafe={vi.fn()}
      />,
    )
    expect(screen.queryByTestId('risky-standing-badge')).not.toBeInTheDocument()
  })

  it('badge IS shown when the persisted value is risky on mount', () => {
    render(
      <RiskySettingControl
        options={OPTIONS}
        currentValue="permissive"
        safeValue="strict"
        copy={COPY}
        onConfirm={vi.fn()}
        onSelectSafe={vi.fn()}
      />,
    )
    expect(screen.getByTestId('risky-standing-badge')).toBeInTheDocument()
  })

  it('confirming a risky value in the dialog does NOT itself clear the badge — that only happens when the caller passes safeValue as currentValue', async () => {
    /**
     * Simulate a typical flow:
     *   1. currentValue = 'strict' (safe) → no badge
     *   2. User clicks 'permissive' → dialog opens
     *   3. User confirms → onConfirm('permissive') called
     *   4. Caller has NOT yet re-rendered with new currentValue → badge still absent
     *      (because currentValue is still 'strict' from the prop perspective)
     *   5. Caller re-renders with currentValue = 'permissive' → badge appears
     */
    const user = userEvent.setup()
    const onConfirm = vi.fn()

    const { rerender } = render(
      <RiskySettingControl
        options={OPTIONS}
        currentValue="strict"
        safeValue="strict"
        copy={COPY}
        onConfirm={onConfirm}
        onSelectSafe={vi.fn()}
      />,
    )

    // Step 1: no badge yet
    expect(screen.queryByTestId('risky-standing-badge')).not.toBeInTheDocument()

    // Step 2-3: click risky and confirm
    await user.click(screen.getByTestId('risky-option-permissive'))
    await user.click(screen.getByRole('button', { name: /switch to permissive/i }))
    expect(onConfirm).toHaveBeenCalledWith('permissive')

    // Step 4: prop hasn't changed — badge should still be absent
    expect(screen.queryByTestId('risky-standing-badge')).not.toBeInTheDocument()

    // Step 5: caller persists and re-renders with new currentValue
    rerender(
      <RiskySettingControl
        options={OPTIONS}
        currentValue="permissive"
        safeValue="strict"
        copy={COPY}
        onConfirm={onConfirm}
        onSelectSafe={vi.fn()}
      />,
    )
    expect(screen.getByTestId('risky-standing-badge')).toBeInTheDocument()
  })

  it('badge clears only when the caller re-renders with the safe value as currentValue', async () => {
    const user = userEvent.setup()
    const onSelectSafe = vi.fn()

    const { rerender } = render(
      <RiskySettingControl
        options={OPTIONS}
        currentValue="permissive"
        safeValue="strict"
        copy={COPY}
        onConfirm={vi.fn()}
        onSelectSafe={onSelectSafe}
      />,
    )

    // Badge visible because currentValue is risky
    expect(screen.getByTestId('risky-standing-badge')).toBeInTheDocument()

    // User selects the safe option
    await user.click(screen.getByTestId('risky-option-strict'))
    expect(onSelectSafe).toHaveBeenCalledWith('strict')

    // Badge is still showing because prop hasn't changed yet
    expect(screen.getByTestId('risky-standing-badge')).toBeInTheDocument()

    // Caller persists and re-renders with currentValue = safe
    rerender(
      <RiskySettingControl
        options={OPTIONS}
        currentValue="strict"
        safeValue="strict"
        copy={COPY}
        onConfirm={vi.fn()}
        onSelectSafe={onSelectSafe}
      />,
    )

    // Now badge should be gone
    expect(screen.queryByTestId('risky-standing-badge')).not.toBeInTheDocument()
  })
})
