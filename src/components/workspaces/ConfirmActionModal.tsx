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
 * focus trap, dismissal channels) never drifts between surfaces. Dismissal,
 * per the underlying `AlertDialogContent` (`@/components/ui/alert-dialog.tsx`):
 * Cancel and Escape close it (Escape is Radix Dialog's own default here, not
 * overridden); clicking the overlay/outside the panel does NOT — that's
 * explicitly blocked there (`onPointerDownOutside`/`onInteractOutside`
 * preventDefault) so a destructive confirm is never dismissed by accident.
 * Built on the existing `AlertDialog` primitives (already used by
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
            // AlertDialogAction's own click does NOT auto-dismiss (unlike
            // AlertDialogCancel, which IS wrapped in DialogPrimitive.Close) —
            // this is specifically about the Confirm button's click, separate
            // from Escape/overlay-click dismissal (handled entirely by
            // `AlertDialogContent`, see the header comment above: Escape
            // closes, overlay/outside click does not). The caller
            // (PlanActionButton/TaskActionButton) closes this modal itself
            // once the mutation settles (success or error), so the pending
            // label stays visible for the duration of the call and the
            // caller can decide the right moment to dismiss.
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
