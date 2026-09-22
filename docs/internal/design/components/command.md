# command

Source: `src/components/ui/command.tsx`. Owner: design-system.

This component preserves the existing Sovereign Deep presentation. Its public exports are `Command, CommandInput, CommandList, CommandEmpty, CommandGroup, CommandItem, CommandSeparator`. The shared overlay story records default, keyboard, pointer, forced-colour, reduced-motion, root-size, zoom and reflow coverage.

## Contract

Command wraps cmdk’s inline command interface. cmdk owns filtering, selection, and keyboard navigation; item actions are supplied through `CommandItem` callbacks. The root’s `label` defaults to “Command search” and provides the search input’s accessible name. Callers should provide a label that describes their own search.

`CommandList` scrolls vertically with a 300px maximum height. `CommandEmpty`, `CommandGroup`, and `CommandSeparator` retain cmdk’s empty-result, grouping, and separator behavior. Command does not itself create a modal, trap focus, or implement overlay dismissal; any enclosing dialog or popover owns those behaviors.

## Evidence setup

Where activation begins from a closed trigger, dedicated keyboard and pointer fixtures verify that initial transition. Stable-state fixtures verify accessibility, motion, forced-colour, zoom, and reflow behavior without toggling during setup.

The search input keeps its existing fine-pointer height and has a 44px minimum height when the primary pointer is coarse. The minimum applies to the real input element, so the full visible target accepts native focus and text entry.
