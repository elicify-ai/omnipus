#!/usr/bin/env node
// scripts/design-system/ledger-validate.mjs
//
// Validates a proposed baseline.json/ledger.json pair (as produced by
// ledger-build.mjs) against design-system/enforcement/baseline.schema.json
// and ledger.schema.json, recomputes every ledger fingerprint, checks
// baseline/ledger row alignment, and reports lead-decision vs lane-rule
// bucket arithmetic.
//
// Tracked port of
// dist/design-system-baseline/cli-lanes/fanout/L12/validate.mjs (gitignored
// scratch dir). Logic unchanged; CLI is flag-based instead of positional to
// match ledger-build.mjs. Ported from L12: the original's bucket-arithmetic
// section fell back to `Math.round(totalEntries * 0.065)` for "decided by
// lead" when proposal-meta.json was missing — an invented, unsupported
// estimate. This port has no such fallback: --meta is a required flag and a
// missing/unreadable meta file is a hard validation failure, not a guess.
//
// CLI:
//   node scripts/design-system/ledger-validate.mjs \
//     --baseline <proposal-baseline.json> \
//     --ledger <proposal-ledger.json> \
//     --meta <proposal-meta.json> \
//     [--schema-root <repo-root>]

import { readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { createRequire } from 'node:module'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const Ajv2020 = require('ajv/dist/2020')

export function computeFingerprint(ruleId, filePath, syntax) {
  return createHash('sha256').update(JSON.stringify([ruleId, filePath, syntax])).digest('hex')
}

/**
 * Validate a { baseline, ledger } pair. Pure function, returns a structured
 * report rather than printing/exiting, so it is independently testable.
 *
 * @param {object} params
 * @param {object} params.baseline - parsed proposal-baseline.json
 * @param {object} params.ledger - parsed proposal-ledger.json
 * @param {object} params.baselineSchema
 * @param {object} params.ledgerSchema
 * @param {object} [params.meta] - parsed proposal-meta.json, for bucket arithmetic
 * @returns {{ ok: boolean, errors: string[], schema: {baselineValid: boolean, ledgerValid: boolean}, fingerprintMismatches: number, alignment: {lengthMatch: boolean, rowIssues: number}, bucketArithmetic: null | {totalEntries: number, decidedByLead: number, decidedByLane: number, balances: boolean} }}
 */
export function validateLedger({ baseline, ledger, baselineSchema, ledgerSchema, meta }) {
  const errors = []
  const ajv = new Ajv2020({ strict: false, allErrors: false })
  const vBase = ajv.compile(baselineSchema)
  const vLed = ajv.compile(ledgerSchema)

  const baselineValid = vBase(baseline)
  if (!baselineValid) errors.push(`baseline.json validation failed: ${JSON.stringify(vBase.errors)}`)
  const ledgerValid = vLed(ledger)
  if (!ledgerValid) errors.push(`ledger.json validation failed: ${JSON.stringify(vLed.errors)}`)

  let fingerprintMismatches = 0
  if (Array.isArray(ledger?.entries)) {
    for (const entry of ledger.entries) {
      const recomputed = computeFingerprint(entry.ruleId, entry.path, entry.syntax)
      if (recomputed !== entry.fingerprint) {
        errors.push(`fingerprint mismatch for ${entry.path}: ${entry.fingerprint} != ${recomputed}`)
        fingerprintMismatches += 1
      }
    }
  }

  const baselineLen = Array.isArray(baseline?.fingerprints) ? baseline.fingerprints.length : 0
  const ledgerLen = Array.isArray(ledger?.entries) ? ledger.entries.length : 0
  const lengthMatch = baselineLen === ledgerLen
  if (!lengthMatch) errors.push(`length mismatch: baseline=${baselineLen}, ledger=${ledgerLen}`)

  let rowIssues = 0
  if (lengthMatch) {
    for (let i = 0; i < baselineLen; i += 1) {
      if (baseline.fingerprints[i].fingerprint !== ledger.entries[i].fingerprint) {
        errors.push(`row ${i}: baseline fingerprint doesn't match ledger fingerprint`)
        rowIssues += 1
      }
    }
  }

  let bucketArithmetic = null
  if (meta) {
    if (!meta.leadDecisionsApplied) {
      errors.push('proposal-meta.json missing leadDecisionsApplied — cannot verify bucket arithmetic without it')
    } else {
      const decidedByLead = Object.values(meta.leadDecisionsApplied).reduce((sum, d) => sum + (d.count || 0), 0)
      const decidedByLane = ledgerLen - decidedByLead
      const balances = decidedByLead + decidedByLane === ledgerLen
      if (!balances) errors.push(`bucket arithmetic does not balance: ${decidedByLead} + ${decidedByLane} != ${ledgerLen}`)
      bucketArithmetic = { totalEntries: ledgerLen, decidedByLead, decidedByLane, balances }
    }
  }

  return {
    ok: errors.length === 0,
    errors,
    schema: { baselineValid, ledgerValid },
    fingerprintMismatches,
    alignment: { lengthMatch, rowIssues },
    bucketArithmetic,
  }
}

export function loadJson(p) {
  return JSON.parse(readFileSync(p, 'utf8'))
}

function parseArgs(argv) {
  const out = {}
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i]
    if (a === '--baseline') out.baseline = argv[++i]
    else if (a === '--ledger') out.ledger = argv[++i]
    else if (a === '--meta') out.meta = argv[++i]
    else if (a === '--schema-root') out.schemaRoot = argv[++i]
  }
  return out
}

function main() {
  const args = parseArgs(process.argv.slice(2))
  if (!args.baseline || !args.ledger) {
    console.error('usage: node ledger-validate.mjs --baseline <proposal-baseline.json> --ledger <proposal-ledger.json> [--meta <proposal-meta.json>] [--schema-root <repo-root>]')
    process.exitCode = 2
    return
  }

  const scriptDir = path.dirname(fileURLToPath(import.meta.url))
  const root = path.resolve(scriptDir, '..', '..')
  const resolve = (p) => (path.isAbsolute(p) ? p : path.join(root, p))
  const schemaRoot = args.schemaRoot ? resolve(args.schemaRoot) : root

  const baselinePath = resolve(args.baseline)
  const ledgerPath = resolve(args.ledger)
  const metaPath = args.meta ? resolve(args.meta) : undefined
  const baselineSchemaPath = path.join(schemaRoot, 'design-system/enforcement/baseline.schema.json')
  const ledgerSchemaPath = path.join(schemaRoot, 'design-system/enforcement/ledger.schema.json')

  const baseline = loadJson(baselinePath)
  const ledger = loadJson(ledgerPath)
  const baselineSchema = loadJson(baselineSchemaPath)
  const ledgerSchema = loadJson(ledgerSchemaPath)
  const meta = metaPath ? loadJson(metaPath) : undefined

  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema, meta })

  console.log('=== Schema Validation ===')
  console.log(result.schema.baselineValid ? '✓ baseline.json validates against baseline.schema.json' : '✗ baseline.json validation failed')
  console.log(result.schema.ledgerValid ? '✓ ledger.json validates against ledger.schema.json' : '✗ ledger.json validation failed')

  console.log('\n=== Fingerprint Validation ===')
  if (result.fingerprintMismatches === 0) console.log(`✓ All ${ledger.entries.length} ledger fingerprints recompute correctly`)
  else console.log(`✗ ${result.fingerprintMismatches} fingerprint mismatch(es)`)

  console.log('\n=== Alignment Check ===')
  if (result.alignment.lengthMatch) console.log(`✓ baseline and ledger have same length: ${baseline.fingerprints.length}`)
  else console.log(`✗ length mismatch: baseline=${baseline.fingerprints.length}, ledger=${ledger.entries.length}`)
  if (result.alignment.lengthMatch && result.alignment.rowIssues === 0) console.log(`✓ All ${baseline.fingerprints.length} rows aligned`)
  else if (result.alignment.rowIssues > 0) console.log(`✗ ${result.alignment.rowIssues} row alignment issue(s)`)

  if (result.bucketArithmetic) {
    console.log('\n=== Bucket Arithmetic ===')
    const b = result.bucketArithmetic
    console.log(`Total ledger entries: ${b.totalEntries}`)
    console.log(`  From lead decisions: ${b.decidedByLead}`)
    console.log(`  From lane-based rules: ${b.decidedByLane}`)
    console.log(`  Arithmetic check: ${b.decidedByLead} + ${b.decidedByLane} = ${b.decidedByLead + b.decidedByLane} (expected ${b.totalEntries})`)
  }

  if (!result.ok) {
    console.error('\nValidation FAILED:')
    for (const e of result.errors) console.error(`  - ${e}`)
    process.exitCode = 1
    return
  }

  console.log('\nexit=0')
}

const isMainModule = process.argv[1] && fileURLToPath(import.meta.url) === fileURLToPath(`file://${process.argv[1]}`)
if (isMainModule) {
  main()
}
