import { describe, expect, it, onTestFinished, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'

// Regression guard for the shared afterEach in ./setup.ts (issue #915, PR #928).
//
// Radix FocusScope's unmount cleanup defers its "unmount auto-focus" work to a
// `setTimeout(0)` that builds `new CustomEvent(...)` from the GLOBAL
// constructor. If that timer outlives the test, it can fire after Vitest has
// torn jsdom down, and the shard dies on an unhandled
// "Failed to execute 'dispatchEvent' ... parameter 1 is not of type 'Event'".
//
// Radix Dialog passes `onCloseAutoFocus` to FocusScope as `onUnmountAutoFocus`
// (@radix-ui/react-dialog DialogContentImpl), and FocusScope calls it from
// inside that very timer — so the spy is an observable proxy for "the timer
// has fired". Vitest runs every afterEach hook (setup.ts's cleanup + flush
// included) BEFORE the onTestFinished hooks (@vitest/runner runTest:
// "test.afterEach" then "test.onFinished"), so asserting inside
// onTestFinished checks the state setup.ts leaves behind at the end of a test.
describe('src/test/setup.ts shared afterEach', () => {
  it('fires the Radix FocusScope unmount timer before the test is allowed to finish', () => {
    const onCloseAutoFocus = vi.fn()

    render(
      <Dialog open>
        <DialogContent onCloseAutoFocus={onCloseAutoFocus}>
          <DialogTitle>Flush probe</DialogTitle>
          <DialogDescription>Dialog mounted only to arm the unmount timer.</DialogDescription>
        </DialogContent>
      </Dialog>,
    )

    // The dialog is really mounted, so its FocusScope is live…
    expect(screen.getByRole('dialog', { name: 'Flush probe' })).toBeInTheDocument()
    // …and nothing has unmounted yet, so the unmount handler has not run.
    expect(onCloseAutoFocus).not.toHaveBeenCalled()

    onTestFinished(() => {
      // One Dialog mounted -> one FocusScope unmount -> exactly one call.
      // Zero calls means the timer was still pending when the test ended —
      // the exact state that let it fire after jsdom teardown.
      expect(
        onCloseAutoFocus,
        'FocusScope unmount timer had not fired by the end of afterEach',
      ).toHaveBeenCalledTimes(1)
    })
  })
})
