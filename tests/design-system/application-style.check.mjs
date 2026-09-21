// Stage A application integration: compile the actual app stylesheet through
// its Vite/Tailwind pipeline, then check resolved values in browser engines.
// Expected new values come from the approved token definitions. Mutations:
// omit the token import, remove its layer, and move it after the legacy
// theme. Each must break either availability or preservation assertions.
//
// `legacyCancelled` and `legacyPadding4` were characterized "pre-C1"; C1 row
// 8 (commit af865be2d, decision D-B in
// docs/internal/design/evidence/c1-execution-record.md) has since landed and
// resolved both:
//   - `--color-cancelled` was deliberately corrected from #F97316 (a
//     status-mismatch bug -- that hex is Blocked-orange) to the registered
//     `color.cancelled` -> `primitive.color.amber` token. The probe now
//     expects amber, citing that decision instead of the stale orange.
//   - C1 also migrated every real `p-4` usage in the app onto `--space-*`
//     tokens, so the literal string `p-4` no longer appears anywhere under
//     `src/`. Tailwind builds this stylesheet with `source(none)` (see
//     globals.css), so a utility only exists in the compiled CSS if some
//     scanned `@source` file contains its literal class name -- `p-4` was
//     silently computing 0px because nothing scanned contained that string
//     any more, not because spacing resolution broke. The probe's real
//     intent is "the default, un-namespaced Tailwind spacing scale still
//     resolves correctly against the document root font-size", independent
//     of whether the app happens to use that exact class today. `@source
//     inline()` is Tailwind's own mechanism for forcing a utility to exist
//     without it being present in any scanned file; using it here (from a
//     throwaway build entry, never written into src/) keeps the probe
//     honest -- it still fails if the default spacing scale (`p-4` = 1rem)
//     or the root font-size changes, it just no longer depends on an app
//     file coincidentally still using the literal `p-4` class.
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import test from 'node:test'
import { build } from 'vite'
import tailwindcss from '@tailwindcss/vite'
import { chromium, firefox, webkit } from '@playwright/test'

const globalsPath = resolve('src/styles/globals.css')

// Force Tailwind to emit `.p-4` (see block comment above) via a throwaway
// entry that imports the real, unmodified stylesheet and adds one
// `@source inline()` directive. Nothing under src/ is touched.
const probeEntry = join(tmpdir(), `application-style-probe-${randomUUID()}.css`)
writeFileSync(probeEntry, `@import "${globalsPath}";\n@source inline("p-4");\n`)

let css
try {
  const result = await build({
    configFile: false,
    logLevel: 'silent',
    plugins: [tailwindcss()],
    build: { write: false, cssMinify: false, rollupOptions: { input: probeEntry } },
  })
  const outputs = Array.isArray(result) ? result.flatMap((item) => item.output) : result.output
  css = outputs.filter((item) => item.type === 'asset' && item.fileName.endsWith('.css'))
} finally {
  rmSync(probeEntry, { force: true })
}
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
        legacyCancelled: 'rgb(234, 179, 8)', // #EAB308 amber -- C1 D-B correction, was orange
        legacyPadding4: '14px', // default (un-namespaced) p-4 = 1rem at the default root
        rootFont: '14px',
      })
    } finally {
      await browser.close()
    }
  })
}
