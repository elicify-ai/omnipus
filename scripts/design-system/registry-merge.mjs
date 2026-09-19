#!/usr/bin/env node
// scripts/design-system/registry-merge.mjs
//
// MERGE step of the Stage B wave-4 install sequence (see
// dist/design-system-baseline/cli-lanes/fanout/W4-install brief). Merges the
// current permanent extension-boundary exception rules
// (scripts/design-system/registry-rules.json) with a registry-verify.mjs run
// (verdicts.json + its PASS draft-rules.json) and the lead's NEEDS-READ
// approvals (lead-needs-read-decisions.json shape — see
// dist/design-system-baseline/cli-lanes/tools/lead-needs-read-decisions.json)
// into a new registry-rules.json for registry-build.mjs to consume.
//
// Tracked port of
// dist/design-system-baseline/cli-lanes/tools/merge-registry-rules.mjs
// (gitignored scratch script). Logic is unchanged EXCEPT one bug fix: the
// scratch script wrote the merged-rules file unconditionally — on a blocker
// (unapproved NEEDS-READ, a REJECT, or a rule matching no finding) it only
// set process.exitCode = 1 but still ran fs.writeFileSync afterwards. This
// port refuses correctly: on any blocker, nothing is written and the process
// exits non-zero, matching the brief ("REFUSES (non-zero exit, nothing
// written)").
//
// CLI:
//   node scripts/design-system/registry-merge.mjs \
//     --audit <audit-report.json> \
//     --rules <current-registry-rules.json> \
//     --verdicts <verdicts.json> \
//     --drafts <draft-rules.json> \
//     --decisions <lead-needs-read-decisions.json> \
//     --out <out-registry-rules.json>

import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

export function tripleKey(x) {
  return JSON.stringify([x.ruleId, x.path, x.syntax])
}

/**
 * Pure merge function: same inputs always give the same output. Never
 * writes; the caller decides whether/where to persist `rulesDoc`.
 *
 * @param {object} params
 * @param {object} params.audit - parsed audit report ({ debt: { fingerprints } })
 * @param {object} params.rulesDoc - parsed current registry-rules.json ({ version, source, rules })
 * @param {object|Array} params.verdictsRaw - parsed verdicts.json (array, or { verdicts: [...] })
 * @param {object|Array} params.draftsRaw - parsed draft-rules.json (array, or { rules: [...] })
 * @param {object} params.dec - parsed lead-needs-read-decisions.json ({ date, decisions: [...] })
 * @returns {{
 *   ok: boolean,
 *   rulesDoc: object|null,
 *   kept: object[], stale: object[], drafts: object[], leadRules: object[],
 *   dupes: string[], blockers: string[]
 * }}
 */
export function mergeRegistryRules({ audit, rulesDoc, verdictsRaw, draftsRaw, dec }) {
  const verdicts = Array.isArray(verdictsRaw) ? verdictsRaw : verdictsRaw.verdicts
  const drafts = Array.isArray(draftsRaw) ? draftsRaw : draftsRaw.rules
  const key = tripleKey

  const findingKeys = new Set(audit.debt.fingerprints.map(key))
  const ownerByCategory = Object.fromEntries(rulesDoc.rules.map((r) => [r.category, r.owner]))

  const kept = [], stale = []
  for (const r of rulesDoc.rules) (findingKeys.has(key(r)) ? kept : stale).push(r)

  const approved = new Map(dec.decisions.filter((d) => d.decision === 'approve').map((d) => [`${d.path}|${d.syntax}`, d]))

  const leadRules = [], blockers = []
  for (const v of verdicts) {
    if (v.verdict === 'PASS') continue
    const d = approved.get(`${v.path}|${v.syntax}`)
    if (v.verdict === 'NEEDS-READ' && d) {
      leadRules.push({
        ruleId: v.ruleId,
        path: v.path,
        syntax: v.syntax,
        category: d.category,
        owner: ownerByCategory[d.category] ?? `${d.category} exception registry`,
        reason: `Lead read (${dec.date}): ${d.evidence}`,
      })
    } else {
      blockers.push(`${v.verdict} ${v.ruleId} ${v.path} ${v.syntax}`)
    }
  }

  const all = [...kept, ...drafts, ...leadRules]
  const seen = new Set(), dupes = []
  const rules = all.filter((r) => {
    const k = key(r)
    if (seen.has(k)) {
      dupes.push(k)
      return false
    }
    seen.add(k)
    return true
  })

  for (const r of rules) {
    if (!findingKeys.has(key(r))) blockers.push(`rule matches no finding: ${key(r)}`)
  }

  rules.sort((a, b) => key(a).localeCompare(key(b)))

  const ok = blockers.length === 0
  return {
    ok,
    rulesDoc: ok ? { ...rulesDoc, rules } : null,
    kept,
    stale,
    drafts,
    leadRules,
    dupes,
    blockers,
  }
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
    if (a === '--audit') out.audit = argv[++i]
    else if (a === '--rules') out.rules = argv[++i]
    else if (a === '--verdicts') out.verdicts = argv[++i]
    else if (a === '--drafts') out.drafts = argv[++i]
    else if (a === '--decisions') out.decisions = argv[++i]
    else if (a === '--out') out.out = argv[++i]
  }
  return out
}

function main() {
  const args = parseArgs(process.argv.slice(2))
  const required = ['audit', 'rules', 'verdicts', 'drafts', 'decisions', 'out']
  const missing = required.filter((k) => !args[k])
  if (missing.length) {
    console.error(
      'usage: node registry-merge.mjs --audit <audit.json> --rules <current-rules.json> --verdicts <verdicts.json> --drafts <draft-rules.json> --decisions <lead-decisions.json> --out <out.json>',
    )
    console.error(`missing: ${missing.join(', ')}`)
    process.exitCode = 2
    return
  }

  const audit = loadJson(args.audit)
  const rulesDoc = loadJson(args.rules)
  const verdictsRaw = loadJson(args.verdicts)
  const draftsRaw = loadJson(args.drafts)
  const dec = loadJson(args.decisions)

  const result = mergeRegistryRules({ audit, rulesDoc, verdictsRaw, draftsRaw, dec })

  console.log(
    `kept ${result.kept.length}, stale dropped ${result.stale.length}, drafts ${result.drafts.length}, lead ${result.leadRules.length}, duplicates removed ${result.dupes.length}, total ${
      result.ok ? result.rulesDoc.rules.length : '(refused)'
    }`,
  )
  for (const s of result.stale) console.log(`  STALE ${tripleKey(s)} [${s.category}]`)

  if (!result.ok) {
    console.log('BLOCKERS:')
    result.blockers.forEach((b) => console.log('  ' + b))
    console.error('registry-merge: REFUSED — nothing written')
    process.exitCode = 1
    return
  }

  writeFileSync(args.out, serializeDeterministic(result.rulesDoc))
  console.log('exit=0')
}

const isMainModule = process.argv[1] && fileURLToPath(import.meta.url) === fileURLToPath(`file://${process.argv[1]}`)
if (isMainModule) {
  main()
}
