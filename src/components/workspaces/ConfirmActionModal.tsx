import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'

interface ConfirmActionModalProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** e.g. "Execute this plan?" */
  title: string
  /** Concise copy naming the action + target (ADR-052 §6.8 — every ▶/■ is confirm-modal-gated). */
  description: string
  /** Label on the confirm button while idle, e.g. "Execute". */
  confirmLabel: string
  /** Label on the confirm button while the mutation is in flight, e.g. "Executing…". */
  pendingLabel: string
  onConfirm: () => void
  isPending?: boolean
  /** Ruby destructive styling for irreversible/halting actions (Stop). Execute/Play use the default accent. */
  destructive?: boolean
}

/**
 * Shared confirm-before-act modal (ADR-052 FR-020) — every ▶ Execute/Play and
 * ■ Stop affordance across Plan (PlansFilterBand) and Task (Board/List/Graph)
 * surfaces routes through this ONE component so the confirm UX (copy shape,
 * keyboard/focus trap, no-dismiss-on-outside-click) never drifts between
 * surfaces. Built on the existing `AlertDialog` primitives (already used by
 * PlansFilterBand's Stop/Clear confirms) rather than introducing a second
 * confirm pattern.
 */
export function ConfirmActionModal({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  pendingLabel,
  onConfirm,
  isPending = false,
  destructive = false,
}: ConfirmActionModalProps) {
  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel disabled={isPending}>Cancel</AlertDialogCancel>
          <AlertDialogAction
            // Unlike AlertDialogCancel, AlertDialogAction is NOT wrapped in
            // DialogPrimitive.Close — Radix does not auto-dismiss on click.
            // The caller (PlanActionButton/TaskActionButton) closes the
            // modal itself once the mutation settles (success or error), so
            // the pending label stays visible for the duration of the call
            // and the caller can decide the right moment to dismiss.
            onClick={onConfirm}
            disabled={isPending}
            className={
              destructive
                ? 'bg-[var(--color-error)] text-white hover:bg-[var(--color-error)]/90'
                : undefined
            }
          >
            {isPending ? pendingLabel : confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
