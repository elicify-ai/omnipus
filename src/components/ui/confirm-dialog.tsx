import * as React from 'react'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from './alert-dialog'

export interface ConfirmDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: React.ReactNode
  description: React.ReactNode
  confirmLabel: React.ReactNode
  cancelLabel?: React.ReactNode
  onConfirm: () => void
  pending?: boolean
  destructive?: boolean
}

const ConfirmDialog = ({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  cancelLabel = 'Cancel',
  onConfirm,
  pending = false,
  destructive = false,
}: ConfirmDialogProps) => {
  const restoreFocusRef = React.useRef<HTMLElement | null>(null)

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent
        aria-busy={pending || undefined}
        onOpenAutoFocus={() => {
          restoreFocusRef.current = document.activeElement instanceof HTMLElement
            ? document.activeElement
            : null
        }}
        onCloseAutoFocus={(event) => {
          event.preventDefault()
          const restoreTarget = restoreFocusRef.current
          restoreFocusRef.current = null
          if (restoreTarget?.isConnected) restoreTarget.focus()
        }}
      >
      <AlertDialogHeader>
        <AlertDialogTitle>{title}</AlertDialogTitle>
        <AlertDialogDescription>{description}</AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel data-confirm-dialog-cancel disabled={pending}>{cancelLabel}</AlertDialogCancel>
        <AlertDialogAction data-confirm-dialog-action disabled={pending} actionState={pending ? 'pending' : 'idle'} variant={destructive ? 'destructive' : 'default'} onClick={onConfirm}>
          {confirmLabel}
        </AlertDialogAction>
      </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

ConfirmDialog.displayName = 'ConfirmDialog'

export { ConfirmDialog }
