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
  },
  build: {
    outDir: 'dist/spa',
    rollupOptions: {
      output: {
        // Vite 8 dropped the object form of manualChunks. The function form
        // takes a module-id string and returns a chunk name. Keep the same
        // 4 vendor splits as before (react / router / motion / icons).
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
