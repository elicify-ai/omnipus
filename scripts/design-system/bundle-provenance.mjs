import { createHash } from 'node:crypto'
import { mkdir, writeFile } from 'node:fs/promises'
import { dirname, resolve } from 'node:path'

const FORBIDDEN = /(?:^|[/\\])(?:\.storybook|storybook-static)(?:[/\\]|$)|(?:^|[/\\])[^/\\]+\.stories\.[^/\\]+$|(?:^|[/\\])node_modules[/\\](?:@storybook|storybook)[/\\]/i

export function designSystemProductionProvenance({ evidencePath }) {
  let report
  let enabled = false
  return {
    name: 'omnipus:design-system-production-provenance',
    apply: 'build',
    configResolved(config) {
      enabled = /(?:^|[/\\])dist[/\\]spa[/\\]?$/.test(config.build.outDir)
    },
    // Record the bundle after generateBundle transformations. The separate disk-byte
    // audit rejects any later mutation instead of silently trusting changed output.
    writeBundle(_options, bundle) {
      if (!enabled) return
      const chunks = Object.values(bundle).filter((entry) => entry.type === 'chunk').map((chunk) => ({
        file: chunk.fileName,
        sha256: createHash('sha256').update(chunk.code).digest('hex'),
        modules: Object.keys(chunk.modules).sort(),
      }))
      const javascriptAssets = Object.values(bundle).filter((entry) => entry.type === 'asset' && /\.m?js$/i.test(entry.fileName)).map((asset) => ({
        file: asset.fileName,
        sha256: createHash('sha256').update(typeof asset.source === 'string' ? asset.source : Buffer.from(asset.source)).digest('hex'),
      }))
      const forbiddenModules = chunks.flatMap((chunk) => chunk.modules.filter((id) => FORBIDDEN.test(id)).map((id) => ({ chunk: chunk.file, id })))
      report = { schemaVersion: 1, clean: forbiddenModules.length === 0, chunks, javascriptAssets, forbiddenModules }
      if (!report.clean) this.error(`Storybook module entered production bundle: ${forbiddenModules[0].id}`)
    },
    async closeBundle() {
      if (!enabled) return
      if (!report) throw new Error('production provenance plugin did not observe a generated bundle')
      const target = resolve(evidencePath)
      await mkdir(dirname(target), { recursive: true })
      await writeFile(target, `${JSON.stringify(report, null, 2)}\n`)
    },
  }
}
