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
 * - No per-site branching: `copy` / `safeValue` / and `onConfirm` are always props
 *   (F-G09). The stdio MCP gate and sandbox-profile selector reuse this pattern.
 * - The dialog's default (safe) button is the CANCEL path; the confirm (weaken)
 *   button is the secondary "danger" action. This is the correct AlertDialog
 *   semantics for a destructive choice.
 * - `selectedValue` (optional) drives the active highlight independently from
 *   `currentValue`. When omitted it falls back to `currentValue`. This allows
 *   the caller to drive the highlight from local draft state so the button
 *   reflects the user's intent immediately — even during the useAutoSave debounce
 *   window before the save fires and the ['config'] query refetches. The
 *   STANDING BADGE always derives from `currentValue` (persisted), never from
 *   `selectedValue` (draft).
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
import { SegmentedControl, SegmentedControlItem } from '@/components/ui/segmented-control'
import { cn } from '@/lib/utils'

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
   * Optional: the locally-selected (draft) value for the active-highlight.
   * When provided, the radio highlight tracks this value so it reflects the
   * user's intent immediately — even during the useAutoSave debounce window
   * before the save fires and the query refetches.
   *
   * Invariant: the standing badge ALWAYS derives from `currentValue` (persisted),
   * never from `selectedValue` (draft). Omit to fall back to `currentValue`.
   */
  selectedValue?: T
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
  /**
   * Accessible name for the option group (SegmentedControl requires one).
   * Defaults to `copy.dialogTitle` with a trailing "?" stripped, which reads
   * naturally as a description of the choice being made (e.g. "Listen on all
   * network interfaces?" → "Listen on all network interfaces").
   */
  groupLabel?: string
}

/**
 * RiskySettingControl renders a set of radio-style option buttons.
 *
 * - The safe option renders a "Recommended" pill.
 * - Selecting a non-safe option opens an AlertDialog to confirm the weakening.
 * - While `currentValue !== safeValue`, a standing amber badge is shown.
 * - No dialog fires on initial render even if `currentValue` is already risky.
 * - Active highlight is driven by `selectedValue ?? currentValue` so callers
 *   can pass draft state for immediate visual feedback.
 */
export function RiskySettingControl<T extends string>({
  options,
  currentValue,
  selectedValue,
  safeValue,
  copy,
  onConfirm,
  onSelectSafe,
  disabled = false,
  groupLabel,
}: RiskySettingControlProps<T>) {
  const resolvedGroupLabel = groupLabel ?? copy.dialogTitle.replace(/\?+\s*$/, '')
  // `pendingRiskyValue` is only set when the user clicks a non-safe option.
  // It is cleared after dialog resolve (either direction). It is NEVER derived
  // from `currentValue` to avoid a retroactive dialog on mount.
  const [pendingRiskyValue, setPendingRiskyValue] = useState<T | null>(null)

  // The badge always derives from the PERSISTED value.
  const isCurrentRisky = currentValue !== safeValue

  // The active highlight uses the draft (selectedValue) when provided,
  // falling back to the persisted value. This keeps the button highlight
  // immediately responsive without changing badge semantics.
  const activeHighlightValue = selectedValue ?? currentValue

  function handleOptionClick(clickedValue: T) {
    if (disabled) return
    // Clicking the already-highlighted value is a no-op — not a new safe
    // selection nor a new risky weakening. Do NOT open the dialog.
    if (clickedValue === activeHighlightValue) return
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
      <div className="space-y-[var(--space-2)]">
        {/* Option buttons */}
        <SegmentedControl
          aria-label={resolvedGroupLabel}
          value={activeHighlightValue}
          onValueChange={(value) => handleOptionClick(value as T)}
          disabled={disabled}
          className="flex-wrap gap-[var(--space-2)] border-transparent bg-transparent p-0"
        >
          {options.map((opt) => {
            const isSafe = opt.value === safeValue
            const isActive = activeHighlightValue === opt.value
            return (
              <SegmentedControlItem
                key={opt.value}
                value={opt.value}
                data-testid={`risky-option-${opt.value}`}
                className={cn(
                  'h-auto min-w-0 items-center gap-[var(--space-1)] rounded-md border px-[var(--space-2-5)] py-[var(--space-1)] text-[length:var(--type-caption-size)] font-medium shadow-none',
                  isActive
                    ? 'bg-[var(--color-accent)]/20 text-[var(--color-accent)] border-[var(--color-accent)]/40 hover:bg-[var(--color-accent)]/20 hover:text-[var(--color-accent)]'
                    : 'border-[var(--color-border)] bg-transparent text-[var(--color-muted)] hover:bg-[var(--color-surface-2)] hover:text-[var(--color-secondary)]',
                )}
              >
                {opt.label}
                {isSafe && (
                  <span
                    className="px-[var(--space-1)] py-[var(--space-0-5)] rounded text-[length:var(--type-caption-size)] font-semibold bg-[var(--color-success)]/20 text-[var(--color-success)] border border-[var(--color-success)]/40"
                    data-testid="recommended-pill"
                  >
                    Recommended
                  </span>
                )}
              </SegmentedControlItem>
            )
          })}
        </SegmentedControl>

        {/* Standing amber badge — shown while PERSISTED value is risky */}
        {isCurrentRisky && (
          <div
            className="flex items-center gap-[var(--space-1)] text-[length:var(--type-caption-size)] text-amber-400"
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
