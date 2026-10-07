# ViewSwitch

Published flat, gold-active view-selection composite. Uses the catalogued RadioGroup/RadioGroupItem so a selected view cannot be toggled off. A shadcn Toggle Group was not ported because its optional single-value selection would weaken the existing mandatory-selection contract and add a dependency for behavior already in the kit.

Controlled `value`, `onValueChange`, and typed options with value, label, optional icon, disabled state and test id. Supply aria-label or aria-labelledby. Exactly one enabled option is in the Tab sequence; arrow keys move/select, Home/End select the first/last enabled option. Presentation is borderless and transparent; the active option is Forge Gold. Coarse-pointer items reserve the tokenised touch minimum, avoiding overlapping expanded hit regions.

Verification: shared Tasks T7 regression and Default/Selection stories; all eleven manifest kinds apply. The component carries no task types, stores or application dependencies.
