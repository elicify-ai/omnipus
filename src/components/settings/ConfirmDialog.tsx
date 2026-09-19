import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'

// ConfirmDialog is the deliberateness pause in front of a switch you cannot
// casually undo (ADR-0008 ruling 6). It replaced the password step-up that used
// to guard the six high-blast-radius controls — god mode, the three credential
// vault operations, the provider API key, the sandbox configuration, the
// integration provider and the performance settings.
//
// What it is NOT: a security control. Whoever triggers it already holds an
// authenticated session, so it proves nothing about who they are. It stops an
// accident, and the ADR says so in as many words.
//
// The shape is fixed, deliberately, so all six read the same:
//   - the title is the change, as a question ("Enable god mode?")
//   - the body is one or two plain sentences saying what happens
//   - the footer is exactly Cancel and one confirm button whose label repeats
//     the action verb
//   - no input of any kind, and never the destructive variant (FR-OB-042: a red
//     button on all six teaches people to click red buttons)
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  body,
  confirmLabel,
  onConfirm,
  busy = false,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  // The change, phrased as a question: "Enable god mode?", "Rotate the master key?".
  title: string
  // One or two plain sentences saying what happens.
  body: string
  // Repeats the action verb: "Enable god mode", "Rotate master key".
  confirmLabel: string
  onConfirm: () => void
  // Disables both actions while the save is in flight.
  busy?: boolean
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-sm" data-testid="confirm-dialog">
        <DialogHeader>
          {/* text-base, not the DialogTitle default text-lg — matches the
              agreed prototype (docs/mockups/auth-demo). */}
          <DialogTitle className="text-base">{title}</DialogTitle>
          <DialogDescription data-testid="confirm-body">{body}</DialogDescription>
        </DialogHeader>

        <DialogFooter>
          <Button
            variant="outline"
            size="sm"
            onClick={() => onOpenChange(false)}
            disabled={busy}
            data-testid="confirm-cancel"
          >
            Cancel
          </Button>
          {/* Never `destructive` — these are configuration changes (FR-OB-042). */}
          <Button size="sm" onClick={onConfirm} disabled={busy} data-testid="confirm-accept">
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
