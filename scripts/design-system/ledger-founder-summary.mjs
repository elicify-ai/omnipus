#!/usr/bin/env node
// scripts/design-system/ledger-founder-summary.mjs
//
// Renders a one-page, plain-English founder summary from the ledger-build.mjs
// outputs (proposal-ledger.json, proposal-blocking.json, proposal-stats.json,
// proposal-meta.json).
//
// Tracked port of
// dist/design-system-baseline/cli-lanes/fanout/L12/founder-summary-builder.mjs
// (gitignored scratch dir). Two gaps found in a W4-prep dry run of that
// script are fixed here — both were verified against
// dist/design-system-baseline/cli-lanes/fanout/W4-prep/l12-out/proposal-stats.json:
//
//   (a) The original said only "Unresolved paths… tracked separately", with
//       no number, even though proposal-stats.json already carries the
//       breakdown (proposal.unresolved.{uiCatalog,unassignedPaths,
//       runtimeExtensionBoundaries}). A reader would think the blocking
//       count was the whole unresolved problem, when most of it (412 of 470
//       in the dry run) is runtime extension boundaries awaiting
//       registration, not blocking findings. Fixed by the "Unresolved,
//       By Bucket" table below, read straight from proposal-stats.json.
//
//   (b) `stats.diagnostic.fingerprints.unsupportedIdentities` (43 in the dry
//       run) and `stats.proposal.blocking` (47) are different counting
//       units, presented with no reconciliation. The first counts each
//       unique (rule, file, syntax) combination once; the second counts
//       every blocking-list row, and the same combination can appear more
//       than once in the source file (e.g. the same unsupported pattern
//       twice on one line). Fixed by computeBlockingReconciliation() below,
//       which recomputes both numbers directly from proposal-blocking.json
//       and states the relationship in one sentence, instead of leaving two
//       unexplained numbers next to each other.
//
// Also: the original had, in an earlier revision, put an invented release
// date in this summary. To make sure that class of bug cannot recur, every
// number and every date in the output here is read from one of the four
// input files — none are computed from process.argv, the OS clock, or any
// other source. The "Generated" date is proposal-meta.json's own
// build-time timestamp, not a fresh Date.now() call in this script.

import { readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

function pct(n, total) {
  if (!total) return '0%'
  return `${Math.round((n / total) * 1000) / 10}%`
}

export function computeByCheckpoint(ledger) {
  const byCheckpoint = {}
  for (const entry of ledger.entries) {
    const checkpoint = entry.expiryCheckpoint || 'unknown'
    byCheckpoint[checkpoint] = (byCheckpoint[checkpoint] || 0) + 1
  }
  return byCheckpoint
}

export function computeByLane(ledger) {
  const byLane = {}
  for (const entry of ledger.entries) {
    const ownerStr = entry.owner || ''
    let lane = 'shared-foundation'
    const laneMatch = ownerStr.match(/Lane (\d+)/)
    if (laneMatch) {
      lane = `Lane ${laneMatch[1]}`
    } else if (ownerStr.includes('Lead—shared')) {
      lane = 'Lead—shared-foundation'
    }
    byLane[lane] = (byLane[lane] || 0) + 1
  }
  return byLane
}

export function computeByRuleFamily(ledger) {
  const byRuleFamily = {}
  for (const entry of ledger.entries) {
    const family = entry.ruleId.split('/')[0]
    byRuleFamily[family] = (byRuleFamily[family] || 0) + 1
  }
  return byRuleFamily
}

/**
 * Reconciles the two blocking counting units directly from
 * proposal-blocking.json: "occurrences" (one row per blocking finding,
 * duplicates allowed) vs "identities" (one row per unique
 * (ruleId, path, syntax) combination — the same fingerprint scheme as
 * baseline.schema.json).
 */
export function computeBlockingReconciliation(blocking) {
  const rows = Array.isArray(blocking?.unsupported) ? blocking.unsupported : []
  const byIdentity = new Map()
  for (const r of rows) {
    const key = r.computedFingerprint
    byIdentity.set(key, (byIdentity.get(key) || 0) + 1)
  }
  const occurrences = rows.length
  const uniqueIdentities = byIdentity.size
  const repeatingIdentities = [...byIdentity.values()].filter((c) => c > 1).length
  const extraOccurrences = occurrences - uniqueIdentities
  return { occurrences, uniqueIdentities, repeatingIdentities, extraOccurrences }
}

export function computeUnresolvedBuckets(stats) {
  const u = stats?.proposal?.unresolved ?? {}
  const uiCatalog = u.uiCatalog ?? 0
  const unassignedPaths = u.unassignedPaths ?? 0
  const runtimeExtensionBoundaries = u.runtimeExtensionBoundaries ?? 0
  return { uiCatalog, unassignedPaths, runtimeExtensionBoundaries, total: uiCatalog + unassignedPaths + runtimeExtensionBoundaries }
}

/**
 * Build the founder summary markdown. Pure function of its four inputs —
 * every number and date in the output is read from ledger/blocking/stats/
 * meta, none are computed independently (see file header).
 *
 * @param {object} params
 * @param {object} params.ledger - proposal-ledger.json
 * @param {object} params.blocking - proposal-blocking.json
 * @param {object} params.stats - proposal-stats.json
 * @param {object} params.meta - proposal-meta.json
 */
export function buildFounderSummary({ ledger, blocking, stats, meta }) {
  const byCheckpoint = computeByCheckpoint(ledger)
  const byLane = computeByLane(ledger)
  const byRuleFamily = computeByRuleFamily(ledger)
  const reconciliation = computeBlockingReconciliation(blocking)
  const unresolved = computeUnresolvedBuckets(stats)

  if (reconciliation.occurrences !== stats.proposal.blocking) {
    throw new Error(
      `blocking count mismatch: proposal-blocking.json has ${reconciliation.occurrences} rows but proposal-stats.json says proposal.blocking=${stats.proposal.blocking}`,
    )
  }
  if (reconciliation.uniqueIdentities !== stats.diagnostic.fingerprints.unsupportedIdentities) {
    throw new Error(
      `unsupported-identity count mismatch: recomputed ${reconciliation.uniqueIdentities} unique identities from proposal-blocking.json but proposal-stats.json says diagnostic.fingerprints.unsupportedIdentities=${stats.diagnostic.fingerprints.unsupportedIdentities}`,
    )
  }

  const sortedCheckpoints = Object.keys(byCheckpoint).sort()
  const sortedLanes = Object.keys(byLane).sort()
  const sortedRuleFamilies = Object.keys(byRuleFamily).sort()

  const summary = `# Design System Debt Ledger — Founder Summary

**Generated:** ${meta.timestamp}
**From:** ${ledger.entries.length} ledger entries, ${reconciliation.occurrences} blocking findings, and the unresolved-item breakdown below — all read from this run's proposal-ledger.json, proposal-blocking.json and proposal-stats.json.

---

## The Numbers

| What | Count |
|---|---:|
| Ledger entries (repairable debt, has an owner) | ${ledger.entries.length} |
| Blocking findings (never enter the ledger — see "Blocking findings" below) | ${reconciliation.occurrences} |
| Unresolved items (not yet owned — see "Unresolved, by bucket" below) | ${unresolved.total} |

---

## By Repair Checkpoint

Each ledger entry carries a repair-batch label (\`expiryCheckpoint\` in ledger.schema.json). The batches run in the fixed order C1 → C6; this summary states the count in each batch only, not a date any batch is due.

| Repair batch | Entries |
|---|---:|
${sortedCheckpoints.map((cp) => `| **${cp}** | ${byCheckpoint[cp]} |`).join('\n')}

---

## By Lane Ownership

Each row is a lane (or the lead, for shared foundations) named in the ledger entries' own \`owner\` field.

| Lane | Entries |
|---|---:|
${sortedLanes.map((lane) => `| **${lane}** | ${byLane[lane]} |`).join('\n')}

---

## By Rule Family

The part of the rule id before the \`/\` (e.g. \`spacing\`, \`typography\`). Entries in the same family are usually fixed with the same kind of change.

| Rule family | Entries |
|---|---:|
${sortedRuleFamilies.map((fam) => `| **${fam}** | ${byRuleFamily[fam]} |`).join('\n')}

---

## Unresolved, By Bucket

"Unresolved" means the item has not been given an owner yet — it is neither in the ledger above nor in the blocking list below. There are ${unresolved.total} unresolved items, split three ways:

| Bucket | Count | What it means |
|---|---:|---|
| Runtime extension boundaries awaiting registration | ${unresolved.runtimeExtensionBoundaries} | The colour, spacing or type value is computed or passed in while the app is running (for example, forwarded from another component), not written directly in the file. The automatic scanner cannot check a value it cannot see in the source, so each one needs a person to confirm it fits an approved exception category and add it to a central approved list before it counts as resolved. |
| UI catalog, no lane owner yet | ${unresolved.uiCatalog} | The file is a shared building block already listed in the UI catalog, but no lane currently owns it. |
| Unassigned | ${unresolved.unassignedPaths} | The file has neither a lane owner nor a UI catalog entry, so nobody is currently responsible for it. |

**${pct(unresolved.runtimeExtensionBoundaries, unresolved.total)} of the unresolved items (${unresolved.runtimeExtensionBoundaries} of ${unresolved.total}) are runtime extension boundaries** — the blocking findings below are a separate, much smaller problem.

---

## Blocking Findings

Blocking findings are parser errors and syntax the scanner does not understand yet. They never enter the ledger or the baseline (fail-closed: design-system/enforcement/contract.json), and they stay blocking until either the scanner learns to prove them or the source is rewritten, without any visible change, into a form it can check.

**Two different counts appear for blocking findings, because they count two different things:**

- **${reconciliation.occurrences} occurrences** — one row per blocking finding in proposal-blocking.json. The same rule/file/pattern can appear more than once (for example, twice on one line), and each appearance is its own row.
- **${reconciliation.uniqueIdentities} unique identities** — the same ${reconciliation.occurrences} rows, but counting each distinct (rule, file, pattern) combination only once.

${reconciliation.repeatingIdentities} of those ${reconciliation.uniqueIdentities} identities show up more than once, adding ${reconciliation.extraOccurrences} extra occurrence(s) — which is why ${reconciliation.occurrences} (occurrences) and ${reconciliation.uniqueIdentities} (identities) are both correct, for different questions ("how many rows to fix" vs. "how many distinct patterns to fix").

---

*End of summary*
`

  return summary
}

export function loadJson(p) {
  return JSON.parse(readFileSync(p, 'utf8'))
}

function parseArgs(argv) {
  const out = {}
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i]
    if (a === '--ledger') out.ledger = argv[++i]
    else if (a === '--blocking') out.blocking = argv[++i]
    else if (a === '--stats') out.stats = argv[++i]
    else if (a === '--meta') out.meta = argv[++i]
    else if (a === '--out') out.out = argv[++i]
  }
  return out
}

function main() {
  const args = parseArgs(process.argv.slice(2))
  if (!args.ledger || !args.blocking || !args.stats || !args.meta || !args.out) {
    console.error(
      'usage: node ledger-founder-summary.mjs --ledger <proposal-ledger.json> --blocking <proposal-blocking.json> --stats <proposal-stats.json> --meta <proposal-meta.json> --out <founder-summary.md>',
    )
    process.exitCode = 2
    return
  }

  const scriptDir = path.dirname(fileURLToPath(import.meta.url))
  const root = path.resolve(scriptDir, '..', '..')
  const resolve = (p) => (path.isAbsolute(p) ? p : path.join(root, p))

  let summary
  try {
    const ledger = loadJson(resolve(args.ledger))
    const blocking = loadJson(resolve(args.blocking))
    const stats = loadJson(resolve(args.stats))
    const meta = loadJson(resolve(args.meta))
    summary = buildFounderSummary({ ledger, blocking, stats, meta })
  } catch (err) {
    console.error(`FOUNDER SUMMARY ABORTED: ${err.message}`)
    process.exitCode = 1
    return
  }

  const outPath = resolve(args.out)
  writeFileSync(outPath, summary)
  console.log(`Founder summary written: ${outPath}`)
  console.log('exit=0')
}

const isMainModule = process.argv[1] && fileURLToPath(import.meta.url) === fileURLToPath(`file://${process.argv[1]}`)
if (isMainModule) {
  main()
}
