// Stage A application integration: compile the actual app stylesheet through
// its Vite/Tailwind pipeline, then check resolved values in browser engines.
// Expected new values come from the approved token definitions; legacy spacing
// and cancelled color remain characterized until the C1 application conversion.
// Mutations: omit the token import, remove its layer, and move it after the
// legacy theme. Each must break either availability or preservation assertions.
import assert from 'node:assert/strict'
import { resolve } from 'node:path'
import test from 'node:test'
import { build } from 'vite'
import tailwindcss from '@tailwindcss/vite'
import { chromium, firefox, webkit } from '@playwright/test'

const result = await build({
  configFile: false,
  logLevel: 'silent',
  plugins: [tailwindcss()],
  build: { write: false, cssMinify: false, rollupOptions: { input: resolve('src/styles/globals.css') } },
})
const outputs = Array.isArray(result) ? result.flatMap((item) => item.output) : result.output
const css = outputs.filter((item) => item.type === 'asset' && item.fileName.endsWith('.css'))
assert.equal(css.length, 1, 'the app stylesheet must produce one CSS entry')

for (const [name, engine] of Object.entries({ chromium, firefox, webkit })) {
  test(`${name}: app resolves new component tokens while preserving pre-C1 foundations`, async () => {
    const browser = await engine.launch()
    try {
      const page = await browser.newPage()
      await page.setContent('<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"></head><body></body></html>')
      await page.addStyleTag({ content: String(css[0].source) })
      const actual = await page.evaluate(() => {
        const doc = globalThis.document
        const probe = doc.createElement('div')
        probe.style.cssText = 'background-color:var(--color-destructive-action-hover);width:var(--space-3);min-height:var(--target-touch-minimum);color:var(--color-cancelled)'
        doc.body.append(probe)
        const style = globalThis.getComputedStyle(probe)
        const geometry = doc.createElement('div')
        geometry.className = 'p-4'
        doc.body.append(geometry)
        return {
          destructiveHover: style.backgroundColor,
          space3: style.width,
          touchMinimum: style.minHeight,
          legacyCancelled: style.color,
          legacyPadding4: globalThis.getComputedStyle(geometry).paddingTop,
          rootFont: globalThis.getComputedStyle(doc.documentElement).fontSize,
        }
      })
      assert.deepEqual(actual, {
        destructiveHover: 'rgb(234, 38, 38)', // approved #EA2626 action hover
        space3: '16px',
        touchMinimum: '44px',
        legacyCancelled: 'rgb(249, 115, 22)', // characterized pre-C1 alias
        legacyPadding4: '14px', // current p-4 = 1rem at the default root
        rootFont: '14px',
      })
    } finally {
      await browser.close()
    }
  })
}
