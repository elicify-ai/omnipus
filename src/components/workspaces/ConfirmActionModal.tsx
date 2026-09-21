import { ConfirmDialog } from '@/components/ui/confirm-dialog'

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
 * focus trap, dismissal channels) never drifts between surfaces. A thin
 * wrapper over the catalogued `ConfirmDialog` (`@/components/ui/confirm-dialog.tsx`)
 * rather than a second hand-rolled confirm pattern — its `pending` prop
 * already disables both buttons and shows the in-flight state; the
 * `destructive` prop already produces the same Ruby (`--color-error`)
 * styling this file used to apply by hand. Dismissal: Cancel and Escape
 * close it (Escape is Radix Dialog's own default, not overridden); clicking
 * the overlay/outside the panel does NOT — that's explicitly blocked in
 * `AlertDialogContent` (`onPointerDownOutside`/`onInteractOutside`
 * preventDefault) so a destructive confirm is never dismissed by accident.
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
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description={description}
      // The caller (PlanActionButton/TaskActionButton) closes this modal
      // itself once the mutation settles (success or error), so the pending
      // label stays visible for the duration of the call and the caller
      // can decide the right moment to dismiss.
      confirmLabel={isPending ? pendingLabel : confirmLabel}
      destructive={destructive}
      pending={isPending}
      onConfirm={onConfirm}
    />
  )
}
