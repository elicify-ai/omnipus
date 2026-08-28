// modelOverrideLink.ts — the one place that builds the ADR-068 X-08 pointer.
//
// When a model's window cannot be resolved (`window_unknown`), the UI never
// shows a number and never shows a bare apology: it points at the screen that
// fixes it, with the row pre-filled. The route contract lives in
// `src/routes/_app/settings.tsx` (`?tab=models&provider=&model=`); this helper
// is what keeps the Default-model card (T068-25) and the row expand (T068-27)
// from each hand-rolling the query string.

/** Copy shown in place of a context-window number when it is unknown. */
export const NO_CONTEXT_LENGTH_COPY = 'No context length'

/** The link's own text — an instruction, not a bare "click here". */
export const MODEL_OVERRIDE_LINK_TEXT = 'Set it in Settings → Models → Model overrides'

/**
 * Settings → Models → Model overrides, pre-filled for this exact pair.
 *
 * `src/main.tsx` mounts the router on `createHashHistory()` (required for
 * go:embed static serving — history mode 404s on deep links). A plain
 * `/settings?...` path has no `#`, so a browser treats it as a real
 * navigation: full page reload, a fresh SPA boot, and the reader lands
 * wherever the boot's default route is — never the model-override screen
 * this link promises (O10). Prefixing the hash keeps it an in-page fragment
 * change the router's hash listener picks up, matching the `/#/<path>` shape
 * TanStack Router's own <Link> renders under hash history (see
 * Sidebar.test.tsx's "Connectors" nav assertion for the same convention).
 */
export function modelOverrideHref(provider: string, model: string): string {
  const params = new URLSearchParams({ tab: 'models', provider, model })
  return `/#/settings?${params.toString()}`
}
