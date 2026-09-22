# popover

Source: `src/components/ui/popover.tsx`. Owner: design-system.

This component preserves the existing Sovereign Deep presentation. Its public exports are `Popover, PopoverTrigger, PopoverContent, PopoverAnchor`. The shared overlay story records default, keyboard, pointer, forced-colour, reduced-motion, root-size and 320px reflow coverage.

## Contract

Popover delegates open state, positioning, focus management, and dismissal to Radix Popover. Its root is nonmodal by default, so the default popover does not trap focus or block background interaction. `modal={true}` selects Radix’s modal behavior. Escape and outside interaction request dismissal unless the caller prevents the relevant event. Focus restoration follows Radix’s handling of how the popover was dismissed and any caller focus handlers.

`PopoverContent` is portalled, defaults to centered alignment and a 4px trigger offset, and has a default width of 18rem. Callers may supply Radix positioning and interaction props. `PopoverAnchor` supports positioning against a separate anchor. Reduced motion disables the content’s entrance animation.

## Evidence setup

Where activation begins from a closed trigger, dedicated keyboard and pointer fixtures verify that initial transition. Stable-state fixtures verify accessibility, motion, forced-colour and reflow behavior without toggling during setup.

The automated CSS-zoom proxy is inapplicable to Popover. Radix measures the portalled content in the layout coordinate space while CSS `zoom` changes the visual coordinate space, so the proxy can report overflow that native browser zoom does not produce. The 320px reflow check remains applicable and automated. Native 200% browser zoom is a mandatory manual release check, currently pending in `docs/internal/design/evidence/native-200-percent-zoom-checklist.md`; this disposition is a verification limitation, not evidence that Popover passes native zoom.
