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
    --surface: #141416;
    --border: rgba(226, 232, 240, 0.10);
  }
  * { box-sizing: border-box; }
  html, body { height: 100%; margin: 0; }
  body {
    background: var(--deep-space);
    color: var(--liquid-silver);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    display: flex; align-items: center; justify-content: center;
    padding: 2rem; -webkit-font-smoothing: antialiased;
  }
  .wrap { width: 100%; max-width: 34rem; text-align: center; margin-top: -3rem; }
  .mark { margin: 0 auto 1.5rem; width: 5.5rem; }
  .mark svg { width: 100%; height: auto; display: block; }
  h1 { font-size: 1.5rem; font-weight: 600; letter-spacing: -0.02em; margin: 0 0 1.75rem; }
  h1 .accent { color: var(--forge-gold); }
  form { display: flex; align-items: center; gap: .5rem;
    background: var(--surface); border: 1px solid var(--border);
    border-radius: 999px; padding: .625rem 1.125rem;
    transition: border-color .15s, box-shadow .15s; }
  form:focus-within { border-color: rgba(212, 175, 55, .55);
    box-shadow: 0 0 0 3px rgba(212, 175, 55, .12); }
  .glass { flex: none; width: 18px; height: 18px; stroke: var(--muted); fill: none;
    stroke-width: 2; stroke-linecap: round; }
  input { flex: 1; background: none; border: 0; outline: none; color: var(--liquid-silver);
    font-size: .9375rem; font-family: inherit; min-width: 0; }
  input::placeholder { color: var(--muted); }
  button { flex: none; background: var(--forge-gold); color: #1a1a1a; border: 0;
    border-radius: 999px; padding: .375rem .875rem; font-size: .8125rem; font-weight: 600;
    font-family: inherit; cursor: pointer; }
  button:hover { filter: brightness(1.08); }
  p { color: var(--muted); font-size: .875rem; line-height: 1.6; margin: 1.5rem 0 0; }
  .hint { margin-top: 1.75rem; padding-top: 1.25rem; border-top: 1px solid var(--border);
    font-size: .8125rem; }
  .hint strong { color: var(--liquid-silver); font-weight: 500; }
  @media (prefers-reduced-motion: no-preference) {
    .wrap { animation: rise .4s ease-out both; }
    @keyframes rise { from { opacity: 0; transform: translateY(6px); } to { opacity: 1; transform: none; } }
  }
</style>
</head>
<body>
  <main class="wrap">
    <div class="mark" role="img" aria-label="Omnipus"><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 640 570"><path d="M 310 20.965 C 263.808 26.950, 227.205 58.475, 215.304 102.523 C 212.858 111.577, 212.605 114.165, 212.566 130.500 C 212.527 147.109, 212.735 149.273, 215.269 158.500 C 217.209 165.566, 220.822 174.043, 227.584 187.390 C 238.831 209.591, 241.771 217.163, 247.235 238 C 252.121 256.633, 252.797 267.084, 249.416 271.694 C 243.774 279.387, 232.601 280.007, 222.931 273.164 C 214.391 267.122, 210.945 258.257, 208.485 236 C 206.323 216.433, 204.159 207.807, 198.576 196.500 C 188.125 175.335, 167.448 158.311, 145 152.388 C 135.673 149.927, 118.797 150.120, 108.856 152.802 C 83.929 159.526, 65.029 178.325, 57.838 203.545 C 54.606 214.882, 54.628 232.405, 57.888 242.975 C 60.827 252.500, 64.764 260.313, 66.309 259.683 C 80.469 253.909, 83.853 252.855, 89.764 252.377 C 99.597 251.581, 99.336 251.890, 96.051 244.943 C 86.243 224.205, 92.359 201.723, 110.273 192.659 C 117.982 188.759, 126.357 187.395, 133.651 188.853 C 149.539 192.030, 161.553 203.144, 167.588 220.250 C 168.344 222.391, 169.879 231.423, 171 240.321 C 174.717 269.828, 179.363 281.428, 192.948 295.119 C 205.155 307.422, 215.777 312.355, 233.227 313.826 L 241.500 314.523 235.500 317.509 C 230.793 319.851, 227.864 320.571, 221.909 320.846 C 204.971 321.630, 193.240 315.814, 166.893 293.571 C 144.175 274.392, 127.337 267.488, 103.500 267.578 C 78.653 267.673, 57.849 276.752, 40.696 294.986 C 31.878 304.359, 25.384 316.247, 21.842 329.500 C 18.795 340.900, 18.791 359.436, 21.833 370.291 C 28.595 394.423, 45.777 413.616, 69.121 423.116 C 77.709 426.611, 85.868 428.744, 92.310 429.177 L 97.120 429.500 99.863 422.500 C 103.134 414.152, 108.001 406.373, 114.345 399.355 L 118.996 394.211 107.248 393.787 C 98.472 393.471, 93.992 392.813, 89.539 391.184 C 72.419 384.921, 60.695 370.300, 59.269 353.433 C 56.941 325.913, 84.006 301.993, 112.034 306.799 C 121.120 308.357, 128.657 312.226, 138.541 320.403 C 164.707 342.052, 173.767 347.947, 188.842 353.128 C 198.169 356.333, 199.031 356.446, 214 356.430 C 228.565 356.414, 229.952 356.242, 237 353.580 C 241.125 352.023, 246.857 349.190, 249.737 347.285 C 255.579 343.423, 263.128 335.477, 266.102 330.060 L 268.056 326.500 267.432 330 C 267.089 331.925, 265.212 336.875, 263.260 341 C 258.863 350.295, 246.692 362.909, 237.500 367.700 C 227.344 372.993, 221.531 374.051, 197.500 374.978 C 180.047 375.651, 173.853 376.273, 167.530 377.985 C 127.928 388.708, 101.974 421.456, 101.739 461 C 101.701 467.325, 102.242 475.200, 102.940 478.500 C 109.718 510.545, 135.362 537.019, 168.500 546.180 C 175.302 548.060, 178.971 548.386, 193 548.352 C 207.986 548.316, 210.317 548.065, 218.400 545.617 C 249.156 536.303, 271.760 516.807, 285.509 487.734 C 291.315 475.457, 294.592 463.757, 297.514 444.862 C 298.844 436.261, 301.055 426.243, 302.427 422.599 C 305.308 414.943, 312.287 403.844, 316.951 399.500 L 320.172 396.500 325.411 402.108 C 335.468 412.875, 340.232 424.128, 343.089 443.865 C 344.154 451.228, 345.971 461.088, 347.125 465.778 C 356.954 505.705, 384.831 535.220, 422.642 545.730 C 431.579 548.215, 433.996 548.464, 448.500 548.398 C 461.690 548.339, 465.905 547.942, 472.500 546.140 C 488.627 541.734, 506.862 530.726, 517.330 519.079 C 523.212 512.534, 530.533 500.765, 533.417 493.217 C 537.308 483.036, 538.963 473.707, 538.983 461.860 C 539.023 437.396, 531.672 418, 516.500 402.534 C 508.346 394.221, 499.729 388.131, 489.320 383.322 C 476.108 377.217, 468.993 375.907, 444 374.977 C 419.350 374.060, 414.964 373.258, 403.415 367.553 C 390.247 361.049, 378.570 346.932, 373.872 331.835 C 371.279 323.505, 371.555 322.627, 374.873 328.644 C 381.360 340.408, 391.719 349.003, 405.023 353.659 C 416.021 357.509, 432.379 358.083, 445.392 355.076 C 461.947 351.251, 477.126 342.534, 496.154 325.925 C 512.634 311.541, 520.802 307.245, 533.500 306.282 C 542.140 305.627, 550.087 307.204, 557.946 311.131 C 566.469 315.390, 572.426 321.593, 576.809 330.770 C 580.349 338.185, 580.499 338.929, 580.468 349 C 580.430 361.541, 578.320 367.969, 571.483 376.361 C 562.561 387.315, 550.354 392.859, 533.186 393.755 L 522.872 394.294 526.838 398.397 C 533.184 404.962, 538.621 413.517, 541.548 421.544 L 544.268 429 548.904 429 C 554.606 429, 566.041 425.908, 574.464 422.089 C 590.312 414.903, 604.104 401.708, 611.524 386.633 C 618.243 372.984, 620.263 364.168, 620.263 348.500 C 620.263 338.388, 619.791 333.750, 618.140 327.622 C 610.855 300.596, 590.960 279.859, 564 271.192 C 541.757 264.041, 519.305 265.921, 497.500 276.761 C 488.726 281.123, 483.153 285.171, 470.512 296.366 C 455.445 309.708, 445.482 316.002, 434.068 319.388 C 423.955 322.389, 410.492 320.990, 403.142 316.174 C 400.746 314.604, 400.927 314.531, 410.091 313.379 C 427.028 311.250, 437.940 305.998, 448.395 294.946 C 455.629 287.299, 462.841 274.155, 465.945 262.961 C 467.083 258.858, 468.924 249.200, 470.036 241.500 C 471.148 233.800, 472.539 225.711, 473.128 223.525 C 476.287 211.792, 486.588 198.776, 496.819 193.592 C 509.547 187.142, 520.627 186.653, 531.697 192.051 C 549.826 200.892, 555.051 222.664, 544.557 245.642 L 542.209 250.785 549.730 251.489 C 557.648 252.231, 564.745 254.299, 571.895 257.946 C 574.259 259.153, 576.320 259.996, 576.474 259.820 C 577.747 258.367, 581.257 249.998, 583.528 243 C 585.924 235.615, 586.300 232.795, 586.391 221.500 C 586.511 206.729, 584.746 199.184, 578.208 186.500 C 573.233 176.851, 560.084 163.740, 550.300 158.673 C 529.399 147.848, 503.338 147.831, 481.670 158.628 C 466.525 166.175, 455.045 176.951, 445.851 192.248 C 439.321 203.112, 435.653 215.146, 432.655 235.531 C 431.282 244.871, 429.514 254.382, 428.727 256.666 C 423.846 270.833, 413.035 279.207, 401.606 277.674 C 393.995 276.653, 389.004 270.388, 389.002 261.851 C 389 256.199, 394.851 232.494, 399.656 218.690 C 401.589 213.136, 408.087 198.897, 414.095 187.046 C 427.627 160.356, 430.004 152.411, 430.714 131.500 C 431.302 114.173, 429.037 101.604, 422.567 86.304 C 408.465 52.952, 380.715 29.945, 345.500 22.409 C 336.904 20.569, 318.753 19.831, 310 20.965 M 273.096 214.265 C 268.366 216.696, 266.535 220.541, 266.167 228.818 C 265.747 238.263, 268.449 246.415, 273.960 252.322 C 277.106 255.695, 278.374 256.330, 282.826 256.757 C 287.757 257.231, 288.192 257.076, 291.370 253.706 C 298.329 246.328, 297.451 231.117, 289.463 220.656 C 284.071 213.593, 278.577 211.448, 273.096 214.265 M 359.500 214.164 C 352.949 217.779, 347.879 225.791, 346.119 235.313 C 343.660 248.619, 349.786 258.270, 359.673 256.666 C 366.730 255.521, 372.965 246.610, 375.037 234.707 C 377.108 222.819, 372.365 212.955, 364.615 213.030 C 362.902 213.047, 360.600 213.557, 359.500 214.164 M 363.849 388.059 C 367.306 393.263, 373.690 407.258, 375.964 414.618 C 377.075 418.217, 378.892 428.064, 380 436.500 C 383.114 460.201, 387.609 472.075, 398.437 485.203 C 421.204 512.804, 463.256 516.673, 486.693 493.322 C 500.279 479.785, 504.047 463.241, 497.594 445.463 C 494.471 436.859, 488.285 429.086, 480.166 423.565 C 469.105 416.043, 464.508 414.901, 441.500 413.963 C 413.594 412.825, 401.717 410.155, 384.608 401.170 C 379.718 398.601, 372.532 393.827, 368.639 390.559 C 362.398 385.320, 361.832 385.025, 363.849 388.059 M 271.553 390.689 C 261.468 399.848, 244.039 408.264, 228.500 411.479 C 224.047 412.400, 211.631 413.517, 200.500 413.998 C 189.500 414.473, 178.700 415.394, 176.500 416.045 C 168.946 418.280, 160.564 423.253, 154.438 429.135 C 143.916 439.238, 139.453 450.356, 140.200 464.597 C 140.568 471.595, 141.198 474.017, 144.272 480.237 C 153.788 499.490, 175.887 511.015, 198 508.255 C 213.470 506.325, 224.382 501.150, 236.133 490.173 C 247.021 480.003, 256.110 463.933, 258.856 450 C 259.452 446.975, 260.431 439.843, 261.031 434.151 C 262.452 420.675, 265.890 408.867, 271.984 396.537 C 274.692 391.058, 276.816 386.490, 276.704 386.386 C 276.592 386.282, 274.274 388.218, 271.553 390.689" stroke="none" fill="#D4AF37" fill-rule="evenodd"/></svg></div>
    <h1>Ready to browse with <span class="accent">omnipus</span></h1>

    <!-- Submits straight to a search engine via GET, so the page stays fully
         static and self-contained: no JS, no API call, nothing to break
         offline. The browser navigates normally, exactly as if the URL had
         been typed into the address bar above. -->
    <form action="https://duckduckgo.com/" method="GET" role="search">
      <svg class="glass" viewBox="0 0 24 24" aria-hidden="true">
        <circle cx="11" cy="11" r="7"></circle><path d="M20 20l-3.5-3.5"></path>
      </svg>
      <input type="text" name="q" autofocus autocomplete="off" spellcheck="false"
             aria-label="Search the web" placeholder="Search the web">
      <button type="submit">Search</button>
    </form>

    <p>This is a real browser. Search above, use the address bar, or ask your
       agent to take it somewhere &mdash; you can both drive.</p>
    <p class="hint"><strong>Tip:</strong> anything you open here is visible to
       your agent, and anything it opens is visible to you.</p>
  </main>
</body>
</html>
`
