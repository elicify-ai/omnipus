# AvatarStack

AvatarStack is a public composite in the Omnipus design system, built on `Avatar`. It draws up to `max` (default 3) overlapping avatars and an overflow marker "+N" for the rest, where N is `total` (default: the number of items) minus the avatars drawn.

## Contract

- Variants: no-overflow, overflow.
- Sizes: sm (compact chrome), md (list rows).
- States: default.
- Public exports: AvatarStack. Public types: AvatarStackItem, AvatarStackProps.
- Theme: dark.

The stack is presentational and hidden from assistive technology (`aria-hidden`). The control that contains it owns the accessible name, focus and the touch target: for example the group chat's Participants button names itself "Participants" plus the member count.

Used by the group chat header (Participants button), the Participants panel and the activity pills under the message box (#1449, ADR-20261028 A9.3).
