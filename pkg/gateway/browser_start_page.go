package gateway

import (
	"net/http"
)

// browser_start_page.go serves the Omnipus start page — what a freshly created
// browser tab opens instead of about:blank.
//
// Why this exists (operator report, 2026-08-03): reopening the live panel
// landed on about:blank, a featureless void. On a surface whose real failures
// (a stalled capture, a dead stream) ALSO render as an empty rectangle, "blank"
// and "broken" are indistinguishable — the operator reasonably read a working
// blank tab as another bug. A branded page that says the browser is ready makes
// the idle state legible as a state.
//
// Served from the gateway itself rather than a remote URL so it works offline,
// on an air-gapped install, and before any provider is configured. Registered
// bare (no auth): it is a static, non-sensitive page, and the browsing context
// that loads it is headless Chrome, which holds no session cookie — requiring
// auth here would just make every new tab fail to load.

// browserStartPagePath is the URL path the start page is served on. Kept in one
// place because it is referenced twice: by the route registration and by the
// default value threaded into BrowserConfig.StartPageURL.
const browserStartPagePath = "/browser-start"

// registerBrowserStartPage registers the start page on the main mux.
func (a *restAPI) registerBrowserStartPage(reg httpHandlerRegistrar) {
	reg.RegisterHTTPHandler(browserStartPagePath, http.HandlerFunc(handleBrowserStartPage))
}

// handleBrowserStartPage serves the start page.
//
// Self-contained by construction: no external fonts, scripts, images or
// stylesheets. A start page that depends on the network would defeat its own
// purpose the moment the network is the thing that is broken.
func handleBrowserStartPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Never cached: the page is cheap, and a stale cached copy surviving an
	// upgrade is a needless way to show an old build's start page forever.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write([]byte(browserStartPageHTML))
}

// browserStartPageHTML is the page body. Brand per
// docs/internal/brand/brand-guidelines.md: Deep Space Black background, Liquid
// Silver text, Forge Gold accent. Fonts are a system stack — the brand faces
// (Outfit/Inter) are not embedded here because loading them would require a
// network fetch this page deliberately avoids.
const browserStartPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Omnipus Browser</title>
<style>
  :root {
    --deep-space: #0A0A0B;
    --liquid-silver: #E2E8F0;
    --forge-gold: #D4AF37;
    --muted: #6B7280;
  }
  * { box-sizing: border-box; }
  html, body { height: 100%; margin: 0; }
  body {
    background: var(--deep-space);
    color: var(--liquid-silver);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    display: flex;
    align-items: center;
    justify-content: center;
    text-align: center;
    padding: 2rem;
    -webkit-font-smoothing: antialiased;
  }
  .wrap { max-width: 30rem; }
  /* Inline SVG, not an emoji: the container image ships no emoji font, so the
     octopus glyph rendered as blank space (live-observed on UAT v40). */
  .mark { margin-bottom: 1.25rem; }
  .mark svg { width: 3rem; height: 3rem; }
  h1 {
    font-size: 1.375rem;
    font-weight: 600;
    letter-spacing: -0.01em;
    margin: 0 0 0.5rem;
  }
  h1 .accent { color: var(--forge-gold); }
  p { color: var(--muted); font-size: 0.9375rem; line-height: 1.6; margin: 0; }
  .hint {
    margin-top: 2rem;
    padding-top: 1.25rem;
    border-top: 1px solid rgba(226, 232, 240, 0.08);
    font-size: 0.8125rem;
    color: var(--muted);
  }
  .hint strong { color: var(--liquid-silver); font-weight: 500; }
  @media (prefers-reduced-motion: no-preference) {
    .wrap { animation: rise 0.4s ease-out both; }
    @keyframes rise {
      from { opacity: 0; transform: translateY(6px); }
      to { opacity: 1; transform: none; }
    }
  }
</style>
</head>
<body>
  <main class="wrap">
    <div class="mark" role="img" aria-label="Omnipus">
      <svg viewBox="0 0 24 24" fill="none" stroke="#D4AF37" stroke-width="1.5"
           stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M12 2.75a6 6 0 0 0-6 6v2.5h12v-2.5a6 6 0 0 0-6-6Z"/>
        <circle cx="9.75" cy="8.5" r="0.9" fill="#D4AF37" stroke="none"/>
        <circle cx="14.25" cy="8.5" r="0.9" fill="#D4AF37" stroke="none"/>
        <path d="M7 11.25c-1.2 2-1.6 4.4-3.2 5.9"/>
        <path d="M9.6 11.25c-.5 2.6-.9 5.2-2.2 7.4"/>
        <path d="M12 11.25v8.2"/>
        <path d="M14.4 11.25c.5 2.6.9 5.2 2.2 7.4"/>
        <path d="M17 11.25c1.2 2 1.6 4.4 3.2 5.9"/>
      </svg>
    </div>
    <h1>Ready to browse with <span class="accent">omnipus</span></h1>
    <p>This is a real browser. Use the address bar above, or ask your agent to
       take it somewhere &mdash; you can both drive.</p>
    <p class="hint"><strong>Tip:</strong> anything you open here is visible to
       your agent, and anything it opens is visible to you.</p>
  </main>
</body>
</html>
`
