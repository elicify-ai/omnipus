# confirm-dialog

Source: `src/components/ui/confirm-dialog.tsx`. Owner: design-system.

This component preserves the existing Sovereign Deep presentation. Its public exports are `ConfirmDialog`. The shared overlay story records default, keyboard, pointer, forced-colour, reduced-motion, root-size, zoom and reflow coverage.

## Contract

`ConfirmDialog` is controlled by the caller. Its modal surface traps focus, locks background interaction, contains scrolling, and dismisses with Escape, including while confirmation is pending. Because it has no Radix trigger, it captures the focused opener when it opens and restores that element after Cancel or Escape, including an opener inside a parent dialog.

Outside interactions never dismiss the dialog. Confirm invokes the caller without automatically closing. While confirmation is pending, the action and dialog report `aria-busy`, and Confirm and Cancel remain disabled to prevent duplicate actions. Escape calls `onOpenChange(false)` and restores the opener; it does not invoke `onConfirm` again. The caller owns the pending operation and decides whether closing affects that operation.

## Evidence setup

Where activation begins from a closed trigger, dedicated keyboard and pointer fixtures verify that initial transition. Stable-state fixtures verify accessibility, motion, forced-colour, zoom, and reflow behavior without toggling during setup.
