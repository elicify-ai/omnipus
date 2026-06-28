import { useEffect, useRef, useState } from 'react'
import { useMutation } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { updateMilestone } from '@/lib/api'
import { useUiStore } from '@/store/ui'
import type { MilestoneDatePopoverProps } from './types'

/**
 * MilestoneDatePopover — keyboard/single-pointer reschedule path for milestones.
 *
 * Spec: docs/internal/specs/workspace-calendar-fullcalendar-spec.md (v2)
 * References: US-3/AS-8, US-5/AS-2, FR-009, FR-010, C-4, C-5.
 *
 * Implements MilestoneDatePopoverProps (types.ts):
 *   workspaceId    — passed to updateMilestone as first arg
 *   milestone      — null = closed; non-null = open, prefills the date input
 *   onClose        — called on cancel, dialog-close (X), and after successful save
 *   onRescheduled  — called after successful save so the host can invalidate queries
 *
 * updateMilestone call: updateMilestone(workspaceId, milestone.id, { due_date: 'YYYY-MM-DD' | null })
 * — 3 args, matching the `updateMilestone(workspaceId, id, body)` signature in `lib/api.ts`.
 * An EMPTY date input sends `null` to CLEAR the due date (body: { due_date: null }).
 */
export function MilestoneDatePopover({
  workspaceId,
  milestone,
  onClose,
  onRescheduled,
}: MilestoneDatePopoverProps) {
  const addToast = useUiStore((s) => s.addToast)
  const inputRef = useRef<HTMLInputElement>(null)

  // Local date string state — YYYY-MM-DD
  const [dateValue, setDateValue] = useState<string>('')

  // Prefill the date input whenever the dialog opens (milestone becomes non-null).
  // Controlled-open prefill ownership is the dialog's: it reads milestone.due_date.
  useEffect(() => {
    if (milestone != null) {
      setDateValue(milestone.due_date ?? '')
    }
  }, [milestone])

  // Focus the date input on open. This setTimeout is LOAD-BEARING: without it Radix's
  // focus trap would land on the first tabbable element (the Cancel button). Moving focus
  // explicitly to the input lets the user start editing immediately (C-4).
  useEffect(() => {
    if (milestone != null) {
      // Small defer so the dialog animation does not fight focus management.
      const id = setTimeout(() => {
        inputRef.current?.focus()
      }, 50)
      return () => clearTimeout(id)
    }
  }, [milestone])

  const mutation = useMutation({
    mutationFn: () => {
      // milestone is guaranteed non-null when Save is clicked (button disabled otherwise).
      return updateMilestone(workspaceId, milestone!.id, { due_date: dateValue || null })
    },
    onSuccess: (_data, _vars, _ctx) => {
      // Capture all closure values into consts NOW — the milestone prop may be null
      // by the time the Undo action is invoked (dialog already closed). FR-010 Undo.
      const savedMilestoneId = milestone!.id
      const savedMilestoneName = milestone?.name ?? 'milestone'
      const savedWorkspaceId = workspaceId
      const priorDueDate = milestone!.due_date ?? null

      addToast({
        message: `Rescheduled ${savedMilestoneName}`,
        variant: 'success',
        duration: 5000,
        // role=status is the semantic for a non-disruptive confirmation (I-8/FR-010).
        // The toast store renders with aria-live="polite" for the status variant.
        action: {
          label: 'Undo',
          onClick: () => {
            updateMilestone(savedWorkspaceId, savedMilestoneId, { due_date: priorDueDate })
              .then(() => {
                onRescheduled?.()
              })
              .catch((err: unknown) => {
                console.error('[calendar] milestone reschedule undo failed', {
                  milestoneId: savedMilestoneId,
                  err,
                })
                addToast({
                  message: "Couldn't undo",
                  variant: 'error',
                })
              })
          },
        },
      })
      onRescheduled?.()
      onClose()
    },
    onError: (err) => {
      console.error('[calendar] milestone reschedule failed', { milestoneId: milestone?.id, err })
      addToast({
        message: "Couldn't reschedule milestone",
        variant: 'error',
        // role=alert is the semantic for an actionable error (I-8/FR-010).
        // The toast store renders with aria-live="assertive" for the error variant.
      })
      // Dialog stays open so the user can correct / retry (FR-010).
    },
  })

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!dateValue) return
    mutation.mutate()
  }

  // Radix onOpenChange fires false when the user presses Escape or clicks the X.
  // We forward all close gestures to onClose; no write is performed (FR-010).
  function handleOpenChange(open: boolean) {
    if (!open) {
      // Reset local state so the next open starts clean.
      setDateValue('')
      mutation.reset()
      onClose()
    }
  }

  return (
    <Dialog open={milestone != null} onOpenChange={handleOpenChange}>
      <DialogContent data-testid="milestone-date-dialog">
        <DialogHeader>
          <DialogTitle>
            Reschedule {milestone?.name ?? ''}
          </DialogTitle>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <label
              htmlFor="milestone-date-input"
              className="text-sm font-medium text-[var(--color-secondary)]"
            >
              Due date
            </label>
            <input
              ref={inputRef}
              id="milestone-date-input"
              type="date"
              value={dateValue}
              onChange={(e) => setDateValue(e.target.value)}
              data-testid="milestone-date-input"
              className={[
                'h-10 w-full rounded-md border border-[var(--color-border)]',
                'bg-[var(--color-surface-2)] px-3 py-2',
                'text-sm text-[var(--color-secondary)]',
                'placeholder:text-[var(--color-muted)]',
                'focus:outline-none focus:ring-2 focus:ring-[var(--color-accent)] focus:ring-offset-2',
                'focus:ring-offset-[var(--color-surface-1)]',
                'disabled:cursor-not-allowed disabled:opacity-50',
                // date inputs in Chromium need explicit color-scheme to honour dark bg
                '[color-scheme:dark]',
              ].join(' ')}
              disabled={mutation.isPending}
              aria-label="New due date"
            />
          </div>

          <DialogFooter>
            <Button
              type="button"
              variant="ghost"
              onClick={() => handleOpenChange(false)}
              disabled={mutation.isPending}
              data-testid="milestone-date-cancel"
            >
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={mutation.isPending || !dateValue}
              data-testid="milestone-date-save"
              className="bg-[var(--color-accent)] text-[var(--color-primary)] hover:bg-[var(--color-accent)]/90"
            >
              {mutation.isPending ? 'Saving…' : 'Save'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
