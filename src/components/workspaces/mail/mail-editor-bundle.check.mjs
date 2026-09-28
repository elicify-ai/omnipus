import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { test } from 'node:test'
import { posix, resolve } from 'node:path'
import { init, parse } from 'es-module-lexer'

async function collectInitialJavaScript(distribution) {
  const html = await readFile(resolve(distribution, 'index.html'), 'utf8')
  const pending = [...html.matchAll(/(?:src|href)=["']\/?([^"']+\.js)["']/g)].map((match) => match[1])
  const initial = new Set()
  await init
  while (pending.length > 0) {
    const path = pending.shift()
    if (!path || initial.has(path)) continue
    initial.add(path)
    const source = await readFile(resolve(distribution, path), 'utf8')
    for (const entry of parse(source)[0]) {
      if ((entry.type !== 'static' && entry.type !== 'reexport-star') || !entry.specifier.endsWith('.js')) continue
      pending.push(posix.normalize(posix.join(posix.dirname(path), entry.specifier)))
    }
  }
  return initial
}

test('the fresh production build keeps the Tiptap runtime in the deferred Mail chunk', async () => {
  const distribution = resolve('dist/spa')
  const initial = await collectInitialJavaScript(distribution)
  const provenance = JSON.parse(await readFile(
    resolve('docs/internal/design/evidence/design-system-production-provenance.json'),
    'utf8',
  ))
  const runtimeChunks = provenance.chunks.filter(({ modules }) =>
    modules.some((id) => /[/\\]node_modules[/\\]@tiptap[/\\]/.test(id)),
  )

  assert.ok(runtimeChunks.length > 0, 'positive control: Rollup provenance must contain Tiptap modules')
  assert.ok(runtimeChunks.every(({ file }) => file.includes('MailPanel-')), 'Tiptap modules must be emitted with the Mail chunk')
  assert.ok(runtimeChunks.every(({ file }) => !initial.has(file)), 'Tiptap chunks must not be reachable from initial JavaScript')
})
