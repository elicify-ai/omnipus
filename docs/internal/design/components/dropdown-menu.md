# dropdown-menu

Source: `src/components/ui/dropdown-menu.tsx`. Owner: design-system.

This component preserves the existing Sovereign Deep presentation. Its public exports are `DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuCheckboxItem, DropdownMenuRadioItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuShortcut, DropdownMenuGroup, DropdownMenuPortal, DropdownMenuSub, DropdownMenuSubContent, DropdownMenuSubTrigger, DropdownMenuRadioGroup`. The shared overlay story records default, keyboard, pointer, forced-colour, reduced-motion, root-size, zoom and reflow coverage.

## Contract

DropdownMenu delegates open state, menu-item focus, keyboard navigation, dismissal, and submenu nesting to Radix Dropdown Menu. The root retains Radix’s default modal mode and its `modal` option. Escape dismisses the menu and normally returns focus to its trigger; outside interaction and item selection retain Radix behavior, including caller event handlers that can prevent dismissal.

`DropdownMenuContent` is portalled and defaults to a 4px offset from its trigger. Checkbox and radio items retain Radix’s selection semantics and caller-controlled checked or selected values. Submenu triggers and content retain Radix’s nested keyboard navigation. `DropdownMenuShortcut` only displays caller-supplied shortcut text; it does not register a keyboard shortcut.

## Evidence setup

Where activation begins from a closed trigger, dedicated keyboard and pointer fixtures verify that initial transition. Stable-state fixtures verify accessibility, motion, forced-colour, zoom, and reflow behavior without toggling during setup.
