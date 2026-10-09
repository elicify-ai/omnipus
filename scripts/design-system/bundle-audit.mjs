#!/usr/bin/env node

import { readFile, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export const INITIAL_GZIP_BUDGET_BYTES = 25 * 1024
// Founder decision D50 (2026-09-29): raised from 250 KiB to 320 KiB to cover the
// Mail composer's Tiptap/ProseMirror engine (~432 kB raw), which is lazy-loaded
// and only fetched when the user opens Mail — first-load gzip budget is
// therefore unchanged. The first-load gzip ceiling remains 25 KiB.
// Founder decision (2026-10-01): raised again, 320 KiB to 383 KiB, to absorb
// +60,083 bytes of release/v0.1.1 drift unrelated to Mail that accumulated
// against the frozen baseline (commit 92aeb4d5d, 2026-09-17) since D50 —
// investigation tracked in #1135 rather than blocking Mail's ship on it.
// Founder-authorised raise (2026-10-03, goal: land the Mail live-access feature):
// raised again, 383 KiB to 422 KiB, to cover the Mail live-access feature's
// measured +39,764 raw bytes (attachment handoff/viewer, cache-first view,
// presence, paging, reply context), measured with bundle-measure.mjs as
// fork-point ee936a385 26,520,425 -> head 26,560,189. The fork point was already
// at +389,120 of the +392,192 budget (99.2%), leaving 3,072 bytes of headroom —
// release drift, not this feature, had consumed the budget. 422 KiB leaves
// ~3.2 KiB (8%) margin over the feature's addition, matching the 383-raise's
// ~7.4% margin. ROOT CAUSE — the budget is sized against the frozen baseline
// 92aeb4d5d (2026-09-17) instead of the current release tip — tracked in #1183.
// Founder decision AB1 (2026-10-05, Q4=A): raised 422 KiB to 430 KiB to cover
// the observed Lane A (+436,023 bytes) and sidebar-with-Lane-A (+437,825 bytes)
// growth. The frozen baseline, provenance checks, and initial-gzip ceiling stay
// unchanged; the unfinished panel still requires its own build and measurement.
// Founder-approved repo-wide allowance 2026-10-08/09 (482 KiB): covers Tasks panel + release-train drift + Wave-1.
// Authorized Wave-1 allowance (2026-10-08, confirmed by squad lead): 430 KiB
// to 482 KiB. Same-lock release base 641491cfb measures 26,570,995 raw bytes;
// Wave-1 head 04b990636 measures 26,620,796 (+49,801: JS +48,178, CSS +1,623).
// The release base had only 630 bytes left against the frozen-baseline cap.
// Cumulative delta +489,491 needs 482 KiB for the documented ~8% feature
// margin: 4,077 bytes / 49,801 = 8.19%; 481 KiB leaves only 6.13%.
// Baseline, initial-gzip ceiling and all Storybook/provenance checks stay fixed.
// Decision/evidence: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/nav-wave1-fe1/docs/internal/design/evidence/nav-wave1-production-budget-20261008.md.
export const TOTAL_RAW_BUDGET_BYTES = 493568

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
