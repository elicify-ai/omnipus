#!/usr/bin/env node

import { readFile, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export const INITIAL_GZIP_BUDGET_BYTES = 25 * 1024
export const TOTAL_RAW_BUDGET_BYTES = 250 * 1024

export function compareProductionBundles(baseline, candidate, provenance = null) {
  const initialGzipDelta = candidate.initial.gzipBytes - baseline.initial.gzipBytes
  const totalRawDelta = candidate.total.rawBytes - baseline.total.rawBytes
  const assetHashes = new Map(candidate.assets?.map((asset) => [asset.path, asset.sha256]) ?? [])
  const candidateJavascript = [...assetHashes.keys()].filter((path) => /\.m?js$/i.test(path)).sort()
  const provenanceJavascript = [...(provenance?.chunks ?? []), ...(provenance?.javascriptAssets ?? [])]
  const provenancePaths = provenanceJavascript.map((entry) => entry.file).sort()
  const provenanceBound = provenanceJavascript.length > 0 &&
    JSON.stringify(provenancePaths) === JSON.stringify(candidateJavascript) &&
    provenanceJavascript.every((entry) => assetHashes.get(entry.file) === entry.sha256)
  const checks = {
    storybookOutputScanClean: candidate.storybook.clean === true,
    storybookModuleProvenanceClean: provenance?.clean === true,
    provenanceMatchesCandidateAssets: provenanceBound,
    initialGzipWithinBudget: initialGzipDelta <= INITIAL_GZIP_BUDGET_BYTES,
    totalRawWithinBudget: totalRawDelta <= TOTAL_RAW_BUDGET_BYTES,
  }
  return {
    schemaVersion: 1,
    pass: Object.values(checks).every(Boolean),
    checks,
    budgets: { initialGzipBytes: INITIAL_GZIP_BUDGET_BYTES, totalRawBytes: TOTAL_RAW_BUDGET_BYTES },
    deltas: { initialGzipBytes: initialGzipDelta, totalRawBytes: totalRawDelta },
    baseline: { revision: baseline.revision, initialGzipBytes: baseline.initial.gzipBytes, totalRawBytes: baseline.total.rawBytes },
    candidate: { revision: candidate.revision, initialGzipBytes: candidate.initial.gzipBytes, totalRawBytes: candidate.total.rawBytes },
    storybookEvidence: candidate.storybook,
  }
}

function args(argv) {
  const parsed = {}
  for (let i = 0; i < argv.length; i += 2) parsed[argv[i]?.replace(/^--/, '')] = argv[i + 1]
  return parsed
}

async function main() {
  const options = args(process.argv.slice(2))
  if (!options.baseline || !options.candidate || !options.output) {
    throw new Error('usage: bundle-audit.mjs --baseline <json> --candidate <json> --output <json>')
  }
  const [baseline, candidate] = await Promise.all([
    readFile(resolve(options.baseline), 'utf8').then(JSON.parse),
    readFile(resolve(options.candidate), 'utf8').then(JSON.parse),
  ])
  if (!options.provenance) throw new Error('bundle audit requires --provenance from the Vite module graph')
  const provenance = JSON.parse(await readFile(resolve(options.provenance), 'utf8'))
  const report = compareProductionBundles(baseline, candidate, provenance)
  await writeFile(resolve(options.output), `${JSON.stringify(report, null, 2)}\n`)
  process.stdout.write(`${JSON.stringify(report)}\n`)
  if (!report.pass) process.exitCode = 1
}

if (process.argv[1] && fileURLToPath(import.meta.url) === resolve(process.argv[1])) {
  main().catch((error) => {
    process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n`)
    process.exitCode = 1
  })
}
