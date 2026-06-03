/**
 * RiskySettingControl — safe option + "Recommended" pill; selecting a risky
 * value opens a consequence AlertDialog (safe = the default/cancel button);
 * a STANDING amber "This lowers your protection" badge persists while the
 * PERSISTED value is risky (F-09 / F-G04).
 *
 * Design decisions:
 * - The badge derives from `currentValue` (the persisted, saved value), NOT
 *   from internal state. The caller owns persistence; this component is pure-UI.
 * - No retroactive dialog fires when `currentValue` is already risky on mount.
 * - No per-site branching: `copy`, `safeValue`, and `onConfirm` are always props
 *   (F-G09). The stdio MCP gate and sandbox-profile selector reuse this pattern.
 * - The dialog's default (safe) button is the CANCEL path; the confirm (weaken)
 *   button is the secondary "danger" action. This is the correct AlertDialog
 *   semantics for a destructive choice.
 *
 * Spec §2 / Issue #317.
 */

import { useState } from 'react'
import { Warning } from '@phosphor-icons/react'
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogCancel,
  AlertDialogAction,
} from '@/components/ui/alert-dialog'

export interface RiskyOption<T extends string> {
  value: T
  label: string
}

export interface RiskySettingCopy {
  /** Title shown in the AlertDialog header. */
  dialogTitle: string
  /** Body shown in the AlertDialog. Explain the consequence of weakening. */
  dialogDescription: string
  /** Label on the "confirm weakening" button (danger action). */
  confirmLabel: string
  /** Label on the "keep it safe" button (cancel = default action). */
  cancelLabel: string
}

export interface RiskySettingControlProps<T extends string> {
  /** Array of all available options in display order. */
  options: RiskyOption<T>[]
  /**
   * The persisted (saved) value. The "standing" amber badge derives ONLY from
   * this prop — it does NOT track internal pending state.
   */
  currentValue: T
  /**
   * The safe value. Options whose value equals safeValue get a "Recommended"
   * pill. Selecting any OTHER value opens the consequence AlertDialog.
   */
  safeValue: T
  /**
   * Copy bundle for the consequence AlertDialog (no in-component branching).
   */
  copy: RiskySettingCopy
  /**
   * Called when the user confirms they want to switch to a risky value.
   * The caller is responsible for persisting the new value and passing it back
   * as `currentValue` to update the standing badge.
   */
  onConfirm: (value: T) => void
  /**
   * Called when the user selects the safe value directly.
   * The caller should persist this and pass the new value back as `currentValue`.
   */
  onSelectSafe: (value: T) => void
  /** Whether the control is disabled (e.g. while a mutation is in flight). */
  disabled?: boolean
}

/**
 * RiskySettingControl renders a set of radio-style option buttons.
 *
 * - The safe option renders a "Recommended" pill.
 * - Selecting a non-safe option opens an AlertDialog to confirm the weakening.
 * - While `currentValue !== safeValue`, a standing amber badge is shown.
 * - No dialog fires on initial render even if `currentValue` is already risky.
 */
export function RiskySettingControl<T extends string>({
  options,
  currentValue,
  safeValue,
  copy,
  onConfirm,
  onSelectSafe,
  disabled = false,
}: RiskySettingControlProps<T>) {
  // `pendingRiskyValue` is only set when the user clicks a non-safe option.
  // It is cleared after dialog resolve (either direction). It is NEVER derived
  // from `currentValue` to avoid a retroactive dialog on mount.
  const [pendingRiskyValue, setPendingRiskyValue] = useState<T | null>(null)

  const isCurrentRisky = currentValue !== safeValue

  function handleOptionClick(clickedValue: T) {
    if (disabled) return
    // Clicking the already-current (persisted) value is a no-op — it is not a
    // new safe selection nor a new risky weakening. Do NOT open the dialog.
    if (clickedValue === currentValue) return
    if (clickedValue === safeValue) {
      onSelectSafe(clickedValue)
    } else {
      // Open the dialog — do NOT apply the value yet.
      setPendingRiskyValue(clickedValue)
    }
  }

  function handleConfirmWeaken() {
    if (disabled) return
    if (pendingRiskyValue !== null) {
      onConfirm(pendingRiskyValue)
    }
    setPendingRiskyValue(null)
  }

  function handleCancelDialog() {
    setPendingRiskyValue(null)
  }

  return (
    <>
      <div className="space-y-2">
        {/* Option buttons */}
        <div className="flex flex-wrap gap-2" role="group">
          {options.map((opt) => {
            const isSafe = opt.value === safeValue
            const isActive = currentValue === opt.value
            return (
              <button
                key={opt.value}
                type="button"
                disabled={disabled}
                onClick={() => handleOptionClick(opt.value)}
                data-testid={`risky-option-${opt.value}`}
                aria-pressed={isActive}
                className={`inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-[11px] font-medium border transition-colors disabled:opacity-40 disabled:cursor-not-allowed ${
                  isActive
                    ? 'bg-[var(--color-accent)]/20 text-[var(--color-accent)] border-[var(--color-accent)]/40'
                    : 'border-[var(--color-border)] text-[var(--color-muted)] hover:text-[var(--color-secondary)] hover:bg-[var(--color-surface-2)]'
                }`}
              >
                {opt.label}
                {isSafe && (
                  <span
                    className="px-1.5 py-0.5 rounded text-[9px] font-semibold bg-emerald-500/20 text-emerald-400 border border-emerald-500/40"
                    data-testid="recommended-pill"
                  >
                    Recommended
                  </span>
                )}
              </button>
            )
          })}
        </div>

        {/* Standing amber badge — shown while persisted value is risky */}
        {isCurrentRisky && (
          <div
            className="flex items-center gap-1.5 text-[11px] text-amber-400"
            data-testid="risky-standing-badge"
            role="status"
            aria-live="polite"
          >
            <Warning size={13} weight="fill" />
            <span>This lowers your protection</span>
          </div>
        )}
      </div>

      {/* Consequence AlertDialog — only opens when a risky value is pending */}
      <AlertDialog open={pendingRiskyValue !== null} onOpenChange={(open) => { if (!open) handleCancelDialog() }}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{copy.dialogTitle}</AlertDialogTitle>
            <AlertDialogDescription>{copy.dialogDescription}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            {/* Safe = the default / cancel button (AlertDialogCancel auto-closes) */}
            <AlertDialogCancel onClick={handleCancelDialog}>
              {copy.cancelLabel}
            </AlertDialogCancel>
            {/* Weaken = the secondary danger action */}
            <AlertDialogAction
              onClick={handleConfirmWeaken}
              className="bg-amber-600/20 text-amber-400 border border-amber-500/40 hover:bg-amber-600/30"
            >
              {copy.confirmLabel}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
