# Page Header

`PageHeader` is the page title row (W2-1; R40, R36, R38): a `div[data-slot="page-header"]`, deliberately not a `header`/banner landmark, holding `leading` content, the one `h1` with its optional description, and the actions with the primary action always last. The title uses the page-title token family (`--type-page-title-*`); `--font-size-page-title` is 1.5rem, 21px at the 14px root (R40). It reads no application state: the drawer menu button arrives through `leading`.

`ScreenHeader` stays unchanged until the page trains swap to `PageHeader` (W2-2/3/4); W2-7 deletes it.
