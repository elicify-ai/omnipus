import { defineConfig } from 'vitest/config'
import type { Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { TanStackRouterVite } from '@tanstack/router-vite-plugin'
import { fileURLToPath, URL } from 'url'
import { createRequire } from 'node:module'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { dirname, join } from 'node:path'

// ── PDF.js runtime assets (ADR-067 FR-018a/b/c/d, D15.3/D15.7) ──────────────
//
// pdfjs-dist is TWO things, and only one of them is JavaScript. The parser is
// bundled by rollup (see the `pdfjs` manualChunks branch below); the four
// RUNTIME asset directories are fetched over HTTP *per document* and are not
// reachable from the module graph at all, so bundling the library does not
// bring them. Omitting them degrades silently and specifically:
//
//   cmaps/          missing -> a Japanese/Chinese/Korean PDF renders BLANK
//   standard_fonts/ missing -> a PDF that embeds no fonts renders with wrong
//                              metrics (overlapping / clipped text)
//   wasm/           missing -> a scanned PDF (JPEG 2000 / JBIG2) loses images
//   iccs/           missing -> colour profiles are ignored
//
// FR-018c: the list is ENUMERATED FROM THE INSTALLED PACKAGE at build time,
// never hand-maintained — a hand-written list silently loses whatever the next
// version adds, invisible until someone opens the one PDF that needs it. The
// build FAILS if a directory is missing or empty rather than shipping a SPA
// whose PDF viewer is quietly broken for a whole class of documents.
//
// FR-018d: `pdfjs-dist` is pinned to an EXACT version in package.json (no
// caret) — it is a parser fed hostile input. Upgrade owner: the Library /
// preview maintainer; an upgrade must re-run the FR-019a build gates
// (no eval path in the shipped bundle, pdf.sandbox*.mjs absent).

/** URL path prefix, relative to the SPA base, that all PDF.js runtime assets
 *  and the worker are served from. The gateway's SPA handler MUST return a real
 *  404 under this prefix instead of its index.html fallback (FR-018b) —
 *  otherwise a missing character map arrives as an HTTP 200 HTML page and the
 *  page renders blank with nothing naming the cause. */
export const PDFJS_ASSET_PREFIX = 'pdfjs/'

/** The four runtime asset directories, exactly as pdfjs-dist ships them. */
export const PDFJS_ASSET_DIRS = ['cmaps', 'standard_fonts', 'wasm', 'iccs'] as const

/** The worker, copied verbatim rather than run through Vite's worker pipeline.
 *  That pipeline's default output format differs from what PDF.js ships, and
 *  getting it wrong produces no build error — just a silent fall back to
 *  parsing on the main thread (FR-019c). Copying the shipped file sidesteps it. */
export const PDFJS_WORKER_FILE = `${PDFJS_ASSET_PREFIX}pdf.worker.min.mjs`

/** Manifest of everything the build emitted, so the viewer can probe one file
 *  per directory and turn a missing asset into a VISIBLE error (FR-018b). */
export const PDFJS_MANIFEST_FILE = `${PDFJS_ASSET_PREFIX}asset-manifest.json`

/** The PDF scripting interpreter, in every form pdfjs-dist ships it. It runs a
 *  PDF's own JavaScript and MUST NOT ship (FR-019a): disabling scripting while
 *  shipping its interpreter leaves the control one edit from being undone.
 *  Absence beats a flag.
 *
 *  `pdf.sandbox*.mjs` is the module the ADR names. `wasm/quickjs-eval.*` is the
 *  QuickJS engine it executes PDF JavaScript ON — 469 kB of general-purpose
 *  JS interpreter that the runtime-asset enumeration would otherwise sweep in
 *  behind the ADR's back. Verified against 6.2.108: those two files are
 *  referenced ONLY by `build/pdf.sandbox*.mjs` — zero references from
 *  `pdf.mjs` or `pdf.worker.mjs` — so excluding them cannot break rendering.
 *
 *  This is a DENY rule over the enumerated list, not a hand-maintained ALLOW
 *  list, so FR-018c still holds: a new asset directory in the next version is
 *  still picked up automatically. */
const PDFJS_FORBIDDEN = /(?:^|\/)(?:pdf\.sandbox[^/]*\.mjs|quickjs-eval[^/]*)$/

function pdfjsPackageRoot(): string {
  return dirname(createRequire(import.meta.url).resolve('pdfjs-dist/package.json'))
}

function walkFiles(root: string, rel = ''): string[] {
  const out: string[] = []
  for (const name of readdirSync(join(root, rel)).sort()) {
    const child = rel ? `${rel}/${name}` : name
    if (statSync(join(root, child)).isDirectory()) out.push(...walkFiles(root, child))
    else out.push(child)
  }
  return out
}

/**
 * Enumerate PDF.js's runtime assets from the INSTALLED package (FR-018c).
 * Exported so the build-gate test can assert the same list against the built
 * SPA output rather than against a second, drifting copy of it.
 *
 * Throws if a directory is absent or empty — an empty enumeration must be a
 * build failure, never a quiet pass.
 */
export function enumeratePdfjsRuntimeAssets(
  pkgRoot: string = pdfjsPackageRoot(),
): Record<string, string[]> {
  const manifest: Record<string, string[]> = {}
  for (const dir of PDFJS_ASSET_DIRS) {
    let files: string[]
    try {
      files = walkFiles(join(pkgRoot, dir))
    } catch (err) {
      throw new Error(
        `pdfjs runtime assets: cannot read ${join(pkgRoot, dir)} — PDFs will render ` +
          `blank/mis-typeset for a whole class of documents (FR-018a). Cause: ${String(err)}`,
      )
    }
    files = files.filter((f) => !PDFJS_FORBIDDEN.test(`${dir}/${f}`))
    if (files.length === 0) {
      throw new Error(`pdfjs runtime assets: ${dir}/ is empty (FR-018c forbids an empty enumeration)`)
    }
    manifest[dir] = files
  }
  return manifest
}

/**
 * Emits PDF.js's runtime assets and its worker into the SPA output under
 * `pdfjs/`, and serves the same tree in `vite dev` so the viewer behaves
 * identically in both. Refuses to emit `pdf.sandbox*.mjs` (FR-019a).
 */
function pdfjsRuntimeAssetsPlugin(): Plugin {
  return {
    name: 'omnipus:pdfjs-runtime-assets',
    // Dev server: the assets live in node_modules, outside `public/`, so
    // nothing serves them under `vite dev` without this. Without it PDF
    // preview is broken in dev only, which is the most confusing shape a
    // breakage can take.
    configureServer(server) {
      const pkgRoot = pdfjsPackageRoot()
      server.middlewares.use(`/${PDFJS_ASSET_PREFIX}`.replace(/\/$/, ''), (req, res, next) => {
        const rel = decodeURIComponent((req.url ?? '').split('?')[0]).replace(/^\/+/, '')
        const allowed =
          rel === 'pdf.worker.min.mjs' ||
          PDFJS_ASSET_DIRS.some((d) => rel.startsWith(`${d}/`))
        if (!allowed || rel.includes('..') || PDFJS_FORBIDDEN.test(rel)) {
          next()
          return
        }
        const from = rel === 'pdf.worker.min.mjs' ? join(pkgRoot, 'build', rel) : join(pkgRoot, rel)
        let body: Buffer
        try {
          body = readFileSync(from)
        } catch {
          // A real 404, not the SPA fallback — the same contract the gateway
          // owes this prefix in production (FR-018b).
          res.statusCode = 404
          res.end('pdfjs asset not found')
          return
        }
        res.setHeader('Content-Type', rel.endsWith('.mjs') ? 'text/javascript' : 'application/octet-stream')
        res.end(body)
      })
    },
    generateBundle() {
      const pkgRoot = pdfjsPackageRoot()
      const manifest = enumeratePdfjsRuntimeAssets(pkgRoot)
      for (const [dir, files] of Object.entries(manifest)) {
        for (const rel of files) {
          this.emitFile({
            type: 'asset',
            fileName: `${PDFJS_ASSET_PREFIX}${dir}/${rel}`,
            source: readFileSync(join(pkgRoot, dir, rel)),
          })
        }
      }
      this.emitFile({
        type: 'asset',
        fileName: PDFJS_WORKER_FILE,
        source: readFileSync(join(pkgRoot, 'build', 'pdf.worker.min.mjs')),
      })
      this.emitFile({
        type: 'asset',
        fileName: PDFJS_MANIFEST_FILE,
        source: JSON.stringify(manifest),
      })
    },
    // Belt and braces for FR-019a: whatever route it arrived by — a future
    // import, a copy step, a dependency bump — the scripting interpreter does
    // not leave this build.
    writeBundle(_options, bundle) {
      const leaked = Object.keys(bundle).filter((f) => PDFJS_FORBIDDEN.test(f))
      if (leaked.length > 0) {
        throw new Error(
          `FR-019a: pdf.sandbox*.mjs must not ship — the PDF scripting interpreter ` +
            `reached the SPA build: ${leaked.join(', ')}`,
        )
      }
    },
  }
}

// autoCodeSplitting is enabled for the production build only, NOT under vitest.
// It rewrites route files at transform time — extracting `component` out of the
// createFileRoute() options into a lazy virtual module — which breaks the route
// unit tests that read `Route.component` via a passthrough createFileRoute mock
// (src/routes/**/-*.test.tsx). Those tests validate component BEHAVIOUR, which
// splitting doesn't change; the split build itself is validated end-to-end by
// the Playwright e2e suite (which runs `npm run build`). So we split at build
// time (the ~1.8 MB → ~0.27 MB entry win, issue #476) and keep components
// inline under test. VITEST=true is set by the vitest runner.
const isVitest = process.env.VITEST === 'true'

// SPA build — embedded into Go binary via go:embed (hash routing required)
export default defineConfig({
  plugins: [
    tailwindcss(),
    // MUST precede @vitejs/plugin-react when autoCodeSplitting is on: the
    // router's code-split transform has to run before the React JSX transform
    // (plugin-order requirement). Route loading shifts to the router — see the
    // defaultPendingComponent wired in src/main.tsx.
    TanStackRouterVite({ autoCodeSplitting: !isVitest }),
    react(),
    pdfjsRuntimeAssetsPlugin(),
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 18790,
    // Devpod preview: the public Fly URL ($DEVPOD_PREVIEW_URL) reaches this
    // Vite server through the proxy, so Vite must accept that Host header.
    allowedHosts: process.env.DEVPOD_PREVIEW_URL
      ? [new URL(process.env.DEVPOD_PREVIEW_URL).hostname]
      : true,
    proxy: {
      '/api': {
        target: 'http://127.0.0.1:5000',
        changeOrigin: true,
        ws: true,
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    // 'tests/**' covers unit tests for the E2E FIXTURES (helpers in
    // tests/e2e/fixtures/), not the Playwright specs themselves — those are
    // *.spec.ts and are deliberately not matched by this *.test.ts pattern.
    // Added 2026-08-13: tests/e2e/fixtures/selectors.test.ts was written,
    // passed locally, and then ran NOWHERE — the include was src-only, so
    // `npx vitest run <that path>` reported "No test files found" and CI
    // never executed it. A test that cannot run is worse than no test: it
    // reads as coverage while proving nothing.
    include: ['src/**/*.test.{ts,tsx}', 'tests/**/*.test.{ts,tsx}'],
    // Pin the locale (and time zone) the test workers run under. The date
    // pickers format via toLocaleDateString(undefined, ...) — i.e. the HOST
    // locale — while their tests assert US-style output ('Jun 22, 2026').
    // On a machine set to en-GB that renders '22 Jun 2026' and three tests
    // fail, so the suite passed on CI and failed on a developer's Mac for a
    // reason that had nothing to do with their change (found 2026-08-13).
    // Pinning here keeps the assertions meaningful AND machine-independent;
    // TZ is pinned for the same reason, since a date-only Date renders
    // differently across zones.
    env: { LC_ALL: 'en-US', LANG: 'en-US', TZ: 'UTC' },
    css: false,
    pool: 'forks',
    // Cap forks at half the LOGICAL cores, i.e. roughly one per physical core.
    //
    // This lived only in deploy/ci-worker/runci.sh ("cap workers: 8
    // oversubscribe shared vCPUs → perf-test timeouts") — so the worker was
    // protected and the other two places the suite runs were not: GitHub
    // Actions (.github/workflows/pr.yml) and every developer's `npx vitest
    // run`. Uncapped, each fork carries a full jsdom environment, and on a
    // 4-physical/8-logical machine the pool oversubscribes badly: the runner
    // starts reporting `[vitest-pool]: Timeout starting forks runner`, whichever
    // test is mid-flight dies on the 15s testTimeout, and a test that times out
    // leaves its DOM mounted — which then surfaces somewhere else entirely as a
    // "found multiple elements" assertion failure. The failing set moved
    // between runs and every test passed in isolation, which is what made it
    // read as unrelated flakiness for so long (issue #616).
    //
    // Measured on this repo, 4 physical cores, full suite: uncapped → 1–4
    // failures per run across five runs; capped → 413/413 files green, and
    // FASTER (import 3694s → 2209s, tests 1308s → 552s), because the forks
    // stop fighting over memory bandwidth.
    //
    // A percentage rather than a literal so a 32-core CI box still gets 16
    // workers instead of the worker script's hardcoded 4. (Vitest 4 takes this
    // as a top-level `maxWorkers`; the older `poolOptions.forks.maxForks`
    // shape is not in this version's config type and fails typecheck.)
    maxWorkers: '50%',
    // 30s, not 15s. The comment on hookTimeout below already establishes why:
    // importing a route/screen module triggers the router plugin's transform
    // across every route file, "20–30s on a cold run". That budget was granted
    // to HOOKS only — but ~23 test files do the same `await import('./X')`
    // inside the test BODY, where only testTimeout applies. Those are exactly
    // the files that kept timing out (#616): -app-auth, ConnectorsScreen,
    // ConnectorsScreen.keyRemount all import their subject in the body.
    //
    // Capping workers above cut the contention that made the transform slow
    // enough to matter, but did not remove it — the suite still lost one test
    // per run or two. 30s is under the documented worst case yet still an
    // order of magnitude above a healthy test, so it remains a real hang
    // detector rather than a blanket suppression.
    testTimeout: 30000,
    // beforeAll in onboarding.test.tsx dynamically imports the route module which
    // triggers TanStack router plugin transform across all route files — this can
    // take 20–30s on a cold run. Raise hookTimeout to prevent spurious timeouts.
    hookTimeout: 60000,
    // #7 (pre-existing fix): react-shiki and other node_modules import CSS files as
    // side effects. css:false suppresses processing but not the extension check that
    // throws "Unknown file extension .css". Redirect all .css imports to an empty
    // stub so tests don't crash on third-party stylesheet side effects.
    server: {
      deps: {
        // Inline react-shiki so the CSS import is handled by the module resolver
        // below rather than the external module loader which cannot handle .css files.
        inline: ['react-shiki'],
      },
    },
  },
  build: {
    outDir: 'dist/spa',
    // Vite's 500 kB default is too conservative for this feature-rich SPA
    // (chat + AssistantUI + katex + mermaid + cytoscape + Shiki syntax
    // highlighting). One category legitimately exceeds 500 kB and cannot be
    // fixed by build config alone: lazily-loaded Shiki language grammars
    // (emacs-lisp ~760 kB, cpp, wasm) — single vendor modules, loaded only
    // when highlighting that language, so their size doesn't hit the initial
    // load. Route-based code-splitting (TanStack Router autoCodeSplitting,
    // see main.tsx's defaultPendingComponent/defaultErrorComponent) has since
    // landed, shrinking the eager entry chunk from ~1.9 MB down to ~0.27 MB.
    // The threshold is set above today's legitimate sizes so the warning still
    // fires on a genuinely new oversized chunk (regression signal), rather
    // than on the known, understood large chunks above.
    chunkSizeWarningLimit: 2000,
    rollupOptions: {
      // Suppress INEFFECTIVE_DYNAMIC_IMPORT: store/ui, store/auth and
      // store/chat are dynamically imported in a few low-level files
      // (lib/queryClient, lib/authLogout, chat/mermaid-renderer) specifically
      // to avoid CIRCULAR IMPORTS / module-graph coupling — not for
      // code-splitting. Rollup can't split them (they're statically imported
      // elsewhere too) and flags the dynamic import as "ineffective", but the
      // intent is correct; converting them to static would reintroduce the
      // circular deps those files document. This is a false positive for our
      // usage, so drop it — every other warning still surfaces.
      onwarn(warning, warn) {
        if (warning.code === 'INEFFECTIVE_DYNAMIC_IMPORT') return
        warn(warning)
      },
      output: {
        // PDF.js gets a STABLE, unmistakable chunk name — and it is named here
        // rather than in `manualChunks` below, which is not a style choice.
        //
        // MEASURED on this repo (rolldown-vite), three builds:
        //   * `manualChunks` branch returning 'pdfjs'  -> the bundler folded
        //     Vite's virtual `\0vite/preload-helper.js` INTO that chunk. The
        //     helper is needed by the *importer*, so the entry then carried
        //     `import{n as E}from"./pdfjs-<hash>.js"` and the whole 428 kB
        //     parser loaded on first paint. Every name-based check still
        //     passed — the chunk existed, was named `pdfjs`, and was still
        //     also reached dynamically. Only the entry's STATIC imports showed
        //     it. `manualChunks` cannot move the helper back out: it is a
        //     virtual module and rolldown ignores manual assignment for it
        //     (verified by returning both 'preload-helper' and 'react' for it,
        //     twice, with no effect).
        //   * no branch at all -> lazy and correct, but the chunk is auto-named
        //     `pdf-<hash>.js` after pdf.mjs. (FR-018 assumed a bare dynamic
        //     import yields a hash-ONLY name; on this bundler it does not.)
        //     `pdf-` is too generic for a test to match safely.
        //   * this branch -> lazy AND named `pdfjs-<hash>.js`.
        //
        // So: name it at emit time, keep it out of the chunking function.
        chunkFileNames(chunk) {
          if (chunk.moduleIds?.some((id) => id.includes('node_modules/pdfjs-dist'))) {
            return 'assets/pdfjs-[hash].js'
          }
          return 'assets/[name]-[hash].js'
        },
        // Vite 8 dropped the object form of manualChunks. The function form
        // takes a module-id string and returns a chunk name. Keep the same
        // 4 vendor splits as before (react / router / motion / icons) — these
        // are the app-wide-eager libs worth caching separately. Broader vendor
        // splits (radix, react-query) were measured to NOT shrink the entry
        // (the entry is app-code-bound, not vendor-bound) and were dropped;
        // heavy, rarely-needed libs (cytoscape, katex, Shiki grammars, the
        // WS-parser worker) stay on Vite's automatic dynamic-import splitting.
        manualChunks(id) {
          if (id.includes('node_modules/react/') || id.includes('node_modules/react-dom/')) {
            return 'react'
          }
          if (id.includes('node_modules/@tanstack/react-router')) {
            return 'router'
          }
          if (id.includes('node_modules/framer-motion')) {
            return 'motion'
          }
          if (id.includes('node_modules/@phosphor-icons/react')) {
            return 'icons'
          }
        },
      },
    },
  },
})
