import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { storybookTest } from '@storybook/addon-vitest/vitest-plugin'
import { playwright } from '@vitest/browser-playwright'
import { defineConfig } from 'vitest/config'
import tailwindcss from '@tailwindcss/vite'

const configDir = path.dirname(fileURLToPath(import.meta.url))
const requestedBrowser = process.env.DESIGN_SYSTEM_BROWSER
if (requestedBrowser && !['chromium', 'firefox', 'webkit'].includes(requestedBrowser)) {
  throw new Error(`Unsupported DESIGN_SYSTEM_BROWSER: ${requestedBrowser}`)
}

export default defineConfig({
  plugins: [tailwindcss()],
  resolve: {
    alias: { '@': path.resolve(configDir, '../src') },
  },
  test: {
    projects: [
      {
        extends: true,
        plugins: [storybookTest({ configDir })],
        test: {
          name: 'storybook',
          browser: {
            enabled: true,
            headless: true,
            provider: playwright({}),
            instances: (requestedBrowser
              ? [{ browser: requestedBrowser }]
              : [{ browser: 'chromium' }, { browser: 'firefox' }, { browser: 'webkit' }]
            ) as Array<{ browser: 'chromium' | 'firefox' | 'webkit' }>,
          },
        },
      },
    ],
  },
})
