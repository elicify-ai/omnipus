#!/usr/bin/env node
// scripts/design-system/ledger-assemble.mjs
//
// ASSEMBLE step of the Stage B wave-4 install sequence (see
// dist/design-system-baseline/cli-lanes/fanout/W4-install brief): combines
// ledger-build.mjs's proposal (proposal-ledger.json + proposal-baseline.json)
// with registry-build.mjs's permanent-exception fragment and the approved
// calendar-token-references fragment into the final baseline.json/ledger.json
// pair the lead installs to design-system/enforcement/.
//
// Until now this step only existed as inline shell commands run by hand
// during the wave-4 rehearsal
// (dist/design-system-baseline/cli-lanes/wave4-rehearsal/); this is the first
// tracked, tested implementation. The logic below was reverse-engineered from
// how ledger.candidate3.json/baseline.candidate3.json were produced in that
// rehearsal, and verified to reproduce them byte-identically (see
// ledger-assemble.test.mjs regression case):
//
//   final ledger.entries = proposal-ledger.entries plus calendar-ledger
//     entries whose fingerprint is not already present, sorted ascending by
//     fingerprint, so each ledger row aligns with the same baseline row;
//   final ledger.exceptions = the registry-build fragment's exceptions,
//     unchanged (content and order);
//   final ledger.reviewedBoundaries = the CURRENTLY INSTALLED ledger's
//     reviewedBoundaries, copied byte-for-byte;
//   final baseline.fingerprints = proposal-baseline.fingerprints UNION
//     calendar-baseline.fingerprints, deduplicated by fingerprint (a
//     proposal fingerprint wins over a calendar one with the same value),
//     sorted ascending by fingerprint.
//
// Refuses — reports errors, writes nothing, exits non-zero — when: either
// output fails design-system/enforcement/{baseline,ledger}.schema.json; two
// ledger entries share a fingerprint; or an exception's [ruleId, path,
// syntax] triple duplicates a ledger entry's.
//
// CLI:
//   node scripts/design-system/ledger-assemble.mjs \
//     --proposal-dir <dir containing proposal-ledger.json + proposal-baseline.json> \
//     --fragment <registry-build.mjs fragment.json> \
//     --current-ledger <currently installed design-system/enforcement/ledger.json> \
//     --calendar-ledger <approved-fragments/calendar-token-references.ledger.json> \
//     --calendar-baseline <approved-fragments/calendar-token-references.baseline.json> \
//     --out-ledger <out ledger.json> \
//     --out-baseline <out baseline.json>

import { readFileSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const require = createRequire(import.meta.url)
const Ajv2020 = require('ajv/dist/2020')

function tripleKey(x) {
  return JSON.stringify([x.ruleId, x.path, x.syntax])
}

function byFingerprint(a, b) {
  if (a.fingerprint < b.fingerprint) return -1
  if (a.fingerprint > b.fingerprint) return 1
  return 0
}

/**
 * Pure assemble function: same inputs always give the same output. Never
 * writes; the caller decides whether/where to persist the result.
 *
 * @param {object} params
 * @param {object} params.proposalLedger - proposal-ledger.json ({ version, entries, exceptions })
 * @param {object} params.proposalBaseline - proposal-baseline.json ({ version, fingerprints })
 * @param {object} params.fragment - registry-build.mjs fragment ({ version, entries, exceptions })
 * @param {object} params.currentLedger - the currently installed ledger.json ({ reviewedBoundaries, ... })
 * @param {object} params.calendarLedger - calendar-token-references.ledger.json ({ entries })
 * @param {object} params.calendarBaseline - calendar-token-references.baseline.json ({ fingerprints })
 * @param {object} params.baselineSchema - baseline.schema.json
 * @param {object} params.ledgerSchema - ledger.schema.json
 * @returns {{ ok: boolean, ledger: object|null, baseline: object|null, errors: string[] }}
 */
export function assembleLedger({
  proposalLedger,
  proposalBaseline,
  fragment,
  currentLedger,
  calendarLedger,
  calendarBaseline,
  baselineSchema,
  ledgerSchema,
}) {
  const errors = []

  const proposalFps = new Set(proposalLedger.entries.map((e) => e.fingerprint))
  const extraCalendarEntries = calendarLedger.entries.filter((e) => !proposalFps.has(e.fingerprint))
  // Sorted by fingerprint so row N of the ledger is row N of the baseline
  // (ledger-validate checks this alignment).
  const entries = [...proposalLedger.entries, ...extraCalendarEntries].sort((x, y) => x.fingerprint.localeCompare(y.fingerprint))

  const exceptions = fragment.exceptions
  const reviewedBoundaries = currentLedger.reviewedBoundaries ?? []

  const ledger = { version: 1, entries, exceptions, reviewedBoundaries }

  const proposalBaseFps = new Set(proposalBaseline.fingerprints.map((f) => f.fingerprint))
  const extraCalendarFps = calendarBaseline.fingerprints.filter((f) => !proposalBaseFps.has(f.fingerprint))
  const fingerprints = [...proposalBaseline.fingerprints, ...extraCalendarFps].sort(byFingerprint)

  const baseline = { version: 1, fingerprints }

  // ---------- integrity checks (refuse, never write, on any failure) ----------
  const seenFp = new Set()
  for (const e of entries) {
    if (seenFp.has(e.fingerprint)) errors.push(`duplicate fingerprint across ledger entries: ${e.fingerprint} (${e.path} ${e.syntax})`)
    seenFp.add(e.fingerprint)
  }

  const entryTriples = new Set(entries.map(tripleKey))
  for (const ex of exceptions) {
    if (entryTriples.has(tripleKey(ex))) errors.push(`exception duplicates a ledger entry: ${tripleKey(ex)}`)
  }

  const ajv = new Ajv2020({ strict: false, allErrors: true })
  const vLed = ajv.compile(ledgerSchema)
  if (!vLed(ledger)) errors.push(`ledger schema validation failed: ${JSON.stringify(vLed.errors)}`)
  const vBase = ajv.compile(baselineSchema)
  if (!vBase(baseline)) errors.push(`baseline schema validation failed: ${JSON.stringify(vBase.errors)}`)

  const ok = errors.length === 0
  return { ok, ledger: ok ? ledger : null, baseline: ok ? baseline : null, errors }
}

export function loadJson(p) {
  return JSON.parse(readFileSync(p, 'utf8'))
}

export function serializeDeterministic(value) {
  return `${JSON.stringify(value, null, 2)}\n`
}

function parseArgs(argv) {
  const out = {}
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i]
    if (a === '--proposal-dir') out.proposalDir = argv[++i]
    else if (a === '--fragment') out.fragment = argv[++i]
    else if (a === '--current-ledger') out.currentLedger = argv[++i]
    else if (a === '--calendar-ledger') out.calendarLedger = argv[++i]
    else if (a === '--calendar-baseline') out.calendarBaseline = argv[++i]
    else if (a === '--out-ledger') out.outLedger = argv[++i]
    else if (a === '--out-baseline') out.outBaseline = argv[++i]
  }
  return out
}

function main() {
  const args = parseArgs(process.argv.slice(2))
  const required = ['proposalDir', 'fragment', 'currentLedger', 'calendarLedger', 'calendarBaseline', 'outLedger', 'outBaseline']
  const missing = required.filter((k) => !args[k])
  if (missing.length) {
    console.error(
      'usage: node ledger-assemble.mjs --proposal-dir <dir> --fragment <fragment.json> --current-ledger <ledger.json> --calendar-ledger <cal.ledger.json> --calendar-baseline <cal.baseline.json> --out-ledger <out-ledger.json> --out-baseline <out-baseline.json>',
    )
    console.error(`missing: ${missing.join(', ')}`)
    process.exitCode = 2
    return
  }

  const scriptDir = path.dirname(fileURLToPath(import.meta.url))
  const root = path.resolve(scriptDir, '..', '..')
  const ledgerSchemaPath = path.join(root, 'design-system/enforcement/ledger.schema.json')
  const baselineSchemaPath = path.join(root, 'design-system/enforcement/baseline.schema.json')

  const proposalLedger = loadJson(path.join(args.proposalDir, 'proposal-ledger.json'))
  const proposalBaseline = loadJson(path.join(args.proposalDir, 'proposal-baseline.json'))
  const fragment = loadJson(args.fragment)
  const currentLedger = loadJson(args.currentLedger)
  const calendarLedger = loadJson(args.calendarLedger)
  const calendarBaseline = loadJson(args.calendarBaseline)
  const ledgerSchema = loadJson(ledgerSchemaPath)
  const baselineSchema = loadJson(baselineSchemaPath)

  const result = assembleLedger({
    proposalLedger,
    proposalBaseline,
    fragment,
    currentLedger,
    calendarLedger,
    calendarBaseline,
    baselineSchema,
    ledgerSchema,
  })

  if (!result.ok) {
    console.error('ledger-assemble: REFUSED — nothing written')
    result.errors.forEach((e) => console.error('  ' + e))
    process.exitCode = 1
    return
  }

  writeFileSync(args.outLedger, serializeDeterministic(result.ledger))
  writeFileSync(args.outBaseline, serializeDeterministic(result.baseline))
  console.log(
    `ledger-assemble: entries ${result.ledger.entries.length}, exceptions ${result.ledger.exceptions.length}, reviewedBoundaries ${result.ledger.reviewedBoundaries.length}, baseline fingerprints ${result.baseline.fingerprints.length}`,
  )
  console.log('exit=0')
}

const isMainModule = process.argv[1] && fileURLToPath(import.meta.url) === fileURLToPath(`file://${process.argv[1]}`)
if (isMainModule) {
  main()
}
