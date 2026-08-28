// ManageSignInDialog — the `signed_in`-row *Manage* surface (ADR-068 FR-034,
// T068-26): shows which account is signed in and offers Sign out. This is
// the destination for a sign-in-capable row's Manage action once it has
// actually connected — deliberately separate from the destructive one-click
// "Sign out" button T068-33 rendered directly on the row, which this
// replaces: a label as open-ended as "Manage" should not silently perform a
// destructive action on click (ADR-068 §8b; no explicit FR mandates this
// split, but firing sign-out directly off an ambiguously-labelled button
// would violate the "no dead/surprising handlers" rule this task ships
// under just as much as leaving Manage inert would).
//
// No network call happens on open — `provider.account_label` already came
// down with the row from GET /providers, so there is nothing to fetch.

import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { CheckCircle, SpinnerGap } from '@phosphor-icons/react'

export function ManageSignInDialog({
  open,
  onOpenChange,
  providerLabel,
  accountLabel,
  onSignOut,
  signingOut,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  providerLabel: string
  accountLabel?: string
  onSignOut: () => void
  signingOut?: boolean
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-sm" data-testid="manage-sign-in-dialog">
        <DialogHeader>
          <DialogTitle>Manage {providerLabel}</DialogTitle>
          <DialogDescription>Your sign-in for {providerLabel}.</DialogDescription>
        </DialogHeader>

        <div
          className="flex items-center gap-2 text-sm text-[var(--color-success)]"
          role="status"
          data-testid="manage-sign-in-status"
        >
          <CheckCircle size={16} weight="fill" />
          {accountLabel ? `Signed in as ${accountLabel}` : 'Signed in'}
        </div>

        <DialogFooter className="sm:justify-between gap-2">
          <Button
            variant="outline"
            onClick={onSignOut}
            disabled={signingOut}
            className="gap-2"
            data-testid="manage-sign-out-btn"
          >
            {signingOut ? <SpinnerGap size={14} className="animate-spin" /> : null}
            Sign out
          </Button>
          <Button onClick={() => onOpenChange(false)} data-testid="manage-sign-in-done-btn">
            Done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
