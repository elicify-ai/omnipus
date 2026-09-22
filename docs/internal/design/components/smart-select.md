# SmartSelect

SmartSelect is a controlled choice composite. Five or fewer items use Select; larger sets use a searchable command popover. Both branches preserve the caller-owned value, accessible field name, disabled state and selection callback.

Decorative icons in the searchable branch — the trigger caret and the selected-item check — are marked `aria-hidden` so screen readers announce only the field name, current value and options, matching the Select branch. Search state is deliberately retained when the popover is dismissed without a selection and cleared only on selection; the query is part of the caller-visible behavior of the open popover, not transient chrome.

Both branches carry executed coverage: unit tests hold the controlled threshold contract, icon hiding, and the query-filter-select flow of the searchable branch, and the Searchable story's play function exercises that same flow (type a query, observe the filtered list, select a match) under the Storybook browser runner via the manifest's interaction checks.

Both branches forward Field control metadata to their actual `combobox` trigger: `id`, `aria-describedby`, `aria-invalid`, and `aria-required`. The explicit `ariaLabel` remains the accessible name. Field composition must wrap SmartSelect or an actual SelectTrigger; a bare Select root is state plumbing and is not labelable.
