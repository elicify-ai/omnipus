# FilterMenu

Published composite built on the catalogued shadcn DropdownMenu and Button. Flat ghost trigger, single-value or keep-open checkbox menu. Use for toolbar filters rather than rebuilding menus locally.

`mode: 'single'` accepts a string/null value and a required clear label. `mode: 'multiple'` accepts a string array and an optional clear label. Multi-select toggles do not close the menu; Clear closes it. Options carry a value, readable label and optional decorative icon. The trigger requires an accessible name. Long labels truncate in the trigger and wrap in the menu. Disabled uses the underlying Button. No app stores, wire formats or domain dependencies.

Verification: shared Tasks T7 regression and Default/Selection stories; all eleven manifest kinds are applicable. Adjacent coarse-pointer triggers reserve the tokenised touch minimum.
