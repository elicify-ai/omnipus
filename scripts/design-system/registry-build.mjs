#!/usr/bin/env node
// scripts/design-system/registry-build.mjs
//
// Deterministic, re-runnable builder that turns a design-system audit report
// (scripts/design-system-locks/audit.mjs output shape: { debt: { fingerprints }, ... })
// into a PERMANENT-exception fragment for the founder-approved Stage B closure
// categories (docs/internal/design/design-system-migration-plan.md §5):
// user-authored colours, live layout measurement, the user-adjustable root
// font, third-party widget values (where no repair is possible), and caller
// pass-through.
//
// Rules live in scripts/design-system/registry-rules.json: exact
// [ruleId, path, syntax] triples mapped to a category + reason + owner. No
// globs, no directory-wide exemptions — every rule is checked for an EXACT
// match against the audit report's debt.fingerprints before it is emitted.
//
// Only ruleIds ending in "/extension-boundary" or exactly
// "ts-colors/unverified-governed-value" may ever reach the output fragment —
// design-system/enforcement/contract.json's runtimeBoundaryClassification and
// unverifiedGovernedValuePolicy are the only registrable classes for this
// builder. Any other ruleId named by a rule (raw-color, off-scale, unsupported,
// or any other debt) is refused and reported, never emitted, even if the
// rules file names one.
//
// Output format matches design-system/enforcement/approved-fragments/*.ledger.json
// and design-system/enforcement/ledger.schema.json exactly: { version: 1,
// entries: [], exceptions: [{ ruleId, path, syntax, reason, owner }] }.

import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const LEDGER_VERSION = 1

/**
 * The only two registrable ruleId classes for this builder (lane R1 brief):
 * "*\/extension-boundary" (any scanner) or the exact ts-colors governed-value
 * rule. Never raw-color, off-scale, unsupported, or any other debt ruleId.
 */
export function isAllowedRuleId(ruleId) {
  if (typeof ruleId !== 'string' || ruleId.length === 0) return false
  if (ruleId === 'ts-colors/unverified-governed-value') return true
  return /\/extension-boundary$/.test(ruleId)
}

export function tripleKey(ruleId, path, syntax) {
  return JSON.stringify([ruleId, path, syntax])
}

function sortByTriple(a, b) {
  if (a.ruleId !== b.ruleId) return a.ruleId < b.ruleId ? -1 : 1
  if (a.path !== b.path) return a.path < b.path ? -1 : 1
  if (a.syntax !== b.syntax) return a.syntax < b.syntax ? -1 : 1
  return 0
}

function validateRuleShape(rule) {
  const missing = ['ruleId', 'path', 'syntax', 'category', 'reason', 'owner'].filter(
    (key) => typeof rule?.[key] !== 'string' || rule[key].length === 0,
  )
  return missing
}

/**
 * Build the permanent-exception fragment and the matched/stale/uncovered
 * report from an audit report and a rules document. Pure function: same
 * inputs always give byte-identical (after JSON serialization) output.
 *
 * @param {object} params
 * @param {object} params.auditReport - parsed audit.mjs report JSON
 * @param {object} params.rulesDoc - parsed registry-rules.json
 * @returns {{ fragment: object, report: object }}
 */
export function buildRegistry({ auditReport, rulesDoc }) {
  const rawRules = Array.isArray(rulesDoc?.rules) ? rulesDoc.rules : []

  const usableRules = []
  const refusedRules = []
  const seenRuleTriples = new Set()

  for (const rule of rawRules) {
    const shapeErrors = validateRuleShape(rule)
    if (shapeErrors.length > 0) {
      refusedRules.push({
        ruleId: rule?.ruleId,
        path: rule?.path,
        syntax: rule?.syntax,
        refusalReason: `malformed rule, missing/empty fields: ${shapeErrors.join(', ')}`,
      })
      continue
    }
    if (!isAllowedRuleId(rule.ruleId)) {
      refusedRules.push({
        ruleId: rule.ruleId,
        path: rule.path,
        syntax: rule.syntax,
        refusalReason: `ruleId "${rule.ruleId}" is not an extension-boundary-class ruleId — only "*/extension-boundary" or "ts-colors/unverified-governed-value" may be registered as a PERMANENT exception; refused, never emitted`,
      })
      continue
    }
    const key = tripleKey(rule.ruleId, rule.path, rule.syntax)
    if (seenRuleTriples.has(key)) {
      refusedRules.push({
        ruleId: rule.ruleId,
        path: rule.path,
        syntax: rule.syntax,
        refusalReason: 'duplicate [ruleId,path,syntax] rule in registry-rules.json',
      })
      continue
    }
    seenRuleTriples.add(key)
    usableRules.push(rule)
  }

  const fingerprints = Array.isArray(auditReport?.debt?.fingerprints) ? auditReport.debt.fingerprints : []
  const findingByTriple = new Map()
  for (const finding of fingerprints) {
    findingByTriple.set(tripleKey(finding.ruleId, finding.path, finding.syntax), finding)
  }

  const matchedRules = []
  const staleRules = []
  for (const rule of usableRules) {
    const key = tripleKey(rule.ruleId, rule.path, rule.syntax)
    const finding = findingByTriple.get(key)
    if (finding) matchedRules.push({ rule, finding })
    else staleRules.push(rule)
  }

  const ruleTripleSet = new Set(usableRules.map((rule) => tripleKey(rule.ruleId, rule.path, rule.syntax)))
  const uncoveredFindings = fingerprints.filter(
    (finding) => isAllowedRuleId(finding.ruleId) && !ruleTripleSet.has(tripleKey(finding.ruleId, finding.path, finding.syntax)),
  )

  const exceptions = matchedRules
    .map(({ rule }) => ({
      ruleId: rule.ruleId,
      path: rule.path,
      syntax: rule.syntax,
      reason: rule.reason,
      owner: rule.owner,
    }))
    .sort(sortByTriple)

  const fragment = { version: LEDGER_VERSION, entries: [], exceptions }

  const report = {
    matched: matchedRules
      .map(({ rule, finding }) => ({ ruleId: rule.ruleId, path: rule.path, syntax: rule.syntax, category: rule.category, findingCount: finding.count ?? null }))
      .sort(sortByTriple),
    stale: staleRules
      .map((rule) => ({ ruleId: rule.ruleId, path: rule.path, syntax: rule.syntax, category: rule.category }))
      .sort(sortByTriple),
    uncovered: uncoveredFindings
      .map((finding) => ({ ruleId: finding.ruleId, path: finding.path, syntax: finding.syntax, count: finding.count ?? null }))
      .sort(sortByTriple),
    refused: refusedRules.slice().sort(sortByTriple),
    counts: {
      totalRules: rawRules.length,
      usableRules: usableRules.length,
      matched: matchedRules.length,
      stale: staleRules.length,
      uncovered: uncoveredFindings.length,
      refused: refusedRules.length,
      emittedExceptions: exceptions.length,
    },
  }

  return { fragment, report }
}

export function serializeDeterministic(value) {
  return `${JSON.stringify(value, null, 2)}\n`
}

export function loadJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'))
}

function main() {
  const [, , auditPath, rulesPathArg, outFragmentPath, outReportPath] = process.argv

  if (!auditPath) {
    console.error('usage: node registry-build.mjs <audit-report.json> [rules.json] [out-fragment.json] [out-report.json]')
    process.exitCode = 2
    return
  }

  const rulesPath = rulesPathArg ?? fileURLToPath(new URL('./registry-rules.json', import.meta.url))

  const auditReport = loadJson(auditPath)
  const rulesDoc = loadJson(rulesPath)

  const { fragment, report } = buildRegistry({ auditReport, rulesDoc })

  const fragmentText = serializeDeterministic(fragment)
  const reportText = serializeDeterministic(report)

  if (outFragmentPath) writeFileSync(outFragmentPath, fragmentText)
  else process.stdout.write(fragmentText)

  if (outReportPath) writeFileSync(outReportPath, reportText)

  console.error(
    `registry-build: ${report.counts.matched} matched, ${report.counts.stale} stale, ${report.counts.uncovered} uncovered, ${report.counts.refused} refused (of ${report.counts.totalRules} rules in ${rulesPath})`,
  )
}

const isMainModule = process.argv[1] && fileURLToPath(import.meta.url) === fileURLToPath(`file://${process.argv[1]}`)
if (isMainModule) {
  main()
}
