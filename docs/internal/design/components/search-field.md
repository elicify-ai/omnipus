# Search Field

`SearchField` is the list-filter search box (W2-1, R33). It composes the kit `Input` (`type="search"`, so its role is `searchbox`) and an `IconButton` for clear; it is not a combobox — `CommandInput` stays the search for command palettes and pickers. `placeholder` and `aria-label` are required: the placeholder names what is searched ("Search connectors…") and the label is the searchbox's accessible name. The magnifier is decorative.

Clear and Escape (founder answer Q1 = A, 2026-11-06): the clear button ("Clear search") shows only while there is text and returns focus to the input; Escape clears the text when there is any and stops the event, and on an empty field Escape is not handled so a surrounding pop-up closes as usual. `clearable={false}` turns both off. Radix dismissable layers listen for Escape in the capture phase on `document`, so a Radix Dialog or Sheet still sees the key first; stopping propagation protects bubble-phase listeners only.

`SearchToolbar` lays out one `SearchField` followed by filter controls (ghost `SelectTrigger`, `FilterMenu`): the field grows with a 192px minimum (`3 × --space-8`), every other child keeps its content width, and the row wraps only when the filters no longer fit. With an `aria-label` it is a `role="search"` landmark.

The field is 36px on a fine pointer from `sm` up and 44px on a coarse pointer or below `sm` (the `Input` heights); the clear button grows to the 44px touch minimum on a coarse pointer.
