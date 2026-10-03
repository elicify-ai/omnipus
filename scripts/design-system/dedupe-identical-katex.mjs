import { createHash } from 'node:crypto'
import { createRequire } from 'node:module'
import { readFileSync } from 'node:fs'
import { dirname, sep } from 'node:path'

// Mermaid and rehype-katex each nest katex 0.16.47. When those dist/katex.mjs
// files are byte-identical they still have different module ids, so the
// production build emits both. Sharing one module id removes the second copy.
// The alias is refused unless version and sha256 both match, so a patched
// same-version file is a build error rather than a silent fold. The hoisted
// katex 0.18.1 package is a different file and is what
// `katex/dist/katex.min.css` resolves to; this plugin never rewrites that
// subpath.
const IDENTICAL_KATEX_VERSION = '0.16.47'
const KATEX_MJS = `${sep}dist${sep}katex.mjs`

function canonicalKatexMjs() {
  const root = createRequire(import.meta.url)
  const rehypeEntry = root.resolve('rehype-katex')
  const cjs = createRequire(rehypeEntry).resolve('katex')
  if (!cjs.endsWith(`${sep}katex.js`)) {
    throw new Error(
      `omnipus:dedupe-identical-katex: rehype-katex resolved katex to ${cjs}, expected dist/katex.js`,
    )
  }
  const mjs = `${cjs.slice(0, -'js'.length)}mjs`
  let version
  try {
    version = JSON.parse(readFileSync(dirname(dirname(mjs)) + `${sep}package.json`, 'utf8')).version
  } catch (error) {
    throw new Error(
      `omnipus:dedupe-identical-katex: cannot read the katex package next to ${mjs}`,
      { cause: error },
    )
  }
  if (version !== IDENTICAL_KATEX_VERSION) {
    throw new Error(
      `omnipus:dedupe-identical-katex: refusing to alias katex ${version}; only ${IDENTICAL_KATEX_VERSION} copies are byte-identical`,
    )
  }
  return mjs
}

function packageVersion(entryId) {
  const pkgPath = `${dirname(dirname(entryId))}${sep}package.json`
  return JSON.parse(readFileSync(pkgPath, 'utf8')).version
}

function sha256(file) {
  return createHash('sha256').update(readFileSync(file)).digest('hex')
}

export function dedupeIdenticalKatex() {
  // Computed on each call. The path is a pure function of the install, so
  // parallel resolveId calls cannot pick two different ids.
  let canonical
  return {
    name: 'omnipus:dedupe-identical-katex',
    apply: 'build',
    enforce: 'pre',
    async resolveId(source, importer, options) {
      if (source !== 'katex') return null
      const resolved = await this.resolve(source, importer, { ...options, skipSelf: true })
      if (!resolved?.id || !resolved.id.endsWith(KATEX_MJS)) return null
      if (packageVersion(resolved.id) !== IDENTICAL_KATEX_VERSION) return null
      canonical ??= canonicalKatexMjs()
      if (resolved.id !== canonical && sha256(resolved.id) !== sha256(canonical)) {
        throw new Error(
          `omnipus:dedupe-identical-katex: refusing to alias ${resolved.id} onto ${canonical}: both are katex ${IDENTICAL_KATEX_VERSION} but the files are not byte-identical`,
        )
      }
      return canonical
    },
  }
}
