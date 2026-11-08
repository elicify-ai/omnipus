# List Group

`ListGroup` is a collapsible group header with its body (W2-1, R35). The header is a `DisclosureRow` toggle (a `Button` with `aria-expanded` and `aria-controls="<id>-body"`), with a leading caret, the label in the uppercase label style and an optional count in muted text. Its accessible name is the label plus the count ("Telegram 3"); a count of 0 is not shown, and the group still renders — hiding empty groups is the caller's choice.

Groups start collapsed (`defaultExpanded` false). A collapsed body keeps its id and carries `hidden`, and its children are not mounted. `expanded` with `onExpandedChange` makes the group controlled (for example "search opens matching groups"). `actions` render beside the toggle, never inside it, so a click on an action never toggles the group.
