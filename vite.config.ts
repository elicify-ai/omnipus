import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { TanStackRouterVite } from '@tanstack/router-vite-plugin'
import { fileURLToPath, URL } from 'url'

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
  ],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 18790,
    proxy: {
      '/api': {
        target: 'http://localhost:18790',
        changeOrigin: true,
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.test.{ts,tsx}'],
    css: false,
    pool: 'forks',
    testTimeout: 15000,
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
