// Carried over from docs/internal/experiments/preview-isolation/fixture/app.js.
// An EXTERNAL script, gated by script-src 'self'. Its flag is what proves the
// source directives are live and matching under an opaque origin — 'unsafe-inline'
// cannot explain an external src loading (experiment §2.1).
window.__APP_JS_RAN__ = true;
document.documentElement.setAttribute('data-js', 'ran');
