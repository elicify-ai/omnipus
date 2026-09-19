# Button

Button is a public presentation primitive in the Omnipus design system. It preserves the existing Sovereign Deep appearance and consumes shared foundation tokens.

## Contract

- Variants: default, destructive, outline, secondary, ghost, link.
- Sizes: sm, default, lg, icon.
- States: idle, pending, success, error, disabled, focus-visible.
- A non-idle `actionState` opts into a reserved 16px feedback slot. Pending feedback appears after 400ms and, once visible, remains for at least 300ms. Success uses a check-circle and “Action succeeded” accessible description; error uses a warning-circle and “Action failed” description while keeping the action enabled for retry. Presented feedback is announced through a polite status region. Icons are additional non-colour cues and never replace caller content or the accessible name. Buttons without a non-idle action state keep their existing geometry.
- Public exports: Button, buttonVariants.
- Theme: dark.
- Destructive actions use Deep Space text on Ruby (5.26:1). The dedicated destructive-action hover colour (#EA2626) preserves the dark foreground at 4.53:1. It replaces #DC2626 only for filled destructive actions because that pair reached 4.10:1; the shared error hover token remains unchanged for other uses.

The colocated stories cover every declared variant, size and state, plus dense and narrow-viewport presentations. The manifest maps applicable unit, accessibility, keyboard, pointer, motion, forced-colour, root-size, zoom and reflow checks to executable evidence.


The `asChild` contract preserves Radix’s enabled click and auxiliary-click capture order: child callback, then Button callback, each exactly once. Preventing the default action does not discard either callback. Pending and disabled actions block primary, keyboard and direct middle-button activation before either callback and expose a dimmed, unavailable cursor treatment through `aria-disabled`. Their links retain their destination metadata and remain in the tab order so keyboard and assistive-technology users can discover the unavailable action. Because the `href` remains present, browser chrome and platform link menus may still expose or copy that address; this contract does not suppress the context menu. Native buttons default to `type="button"`; explicit submit buttons retain real form submission, while pending and disabled submit buttons do not submit. Adjacent-target pointer evidence uses `--space-control-gap` from the closed spacing scale.

In forced-colors mode, Button uses the paired system `ButtonFace` background and `ButtonText` foreground, with a one-pixel `ButtonText` boundary. These cues are scoped to forced colors and do not alter ordinary variant geometry or colours.

Action feedback uses a persistent, initially empty polite live region beside the control in the same modal subtree. State changes update its text after render; returning to idle clears stale feedback. The region does not change the button’s accessible name or native disabled behavior. Automated checks verify DOM placement and updates; human screen-reader speech remains a separate acceptance check.

The loading delay applies to pending announcements. Once the caller reports success, error, or idle, the accessible description and live-region text follow that actual state immediately, even if the spinner is finishing its minimum visual dwell. A retry cannot erase an error solely because that dwell has not finished.

With `asChild`, an explicitly supplied `type` is forwarded through Slot. An explicit type on the child retains Slot precedence. No default button type is added to a composed link. Native button, reset, and submit behavior therefore remains available through composition.
