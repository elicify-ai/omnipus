import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { TanStackRouterVite } from '@tanstack/router-vite-plugin'
import { fileURLToPath, URL } from 'url'

// SPA build — embedded into Go binary via go:embed (hash routing required)
export default defineConfig({
  plugins: [
    tailwindcss(),
    react(),
    TanStackRouterVite(),
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
    // highlighting). Two categories legitimately exceed 500 kB and cannot be
    // fixed by build config alone:
    //   1. Lazily-loaded Shiki language grammars (emacs-lisp ~760 kB, cpp,
    //      wasm) — single vendor modules, loaded only when highlighting that
    //      language, so their size doesn't hit the initial load.
    //   2. The eager entry chunk (~1.9 MB) — dominated by APP code, because
    //      top-level routes/screens are statically imported into the entry
    //      rather than lazy-loaded. Genuinely reducing this needs route-based
    //      code-splitting (TanStack Router lazy routes) — a larger refactor
    //      tracked separately, not a build-config change.
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
