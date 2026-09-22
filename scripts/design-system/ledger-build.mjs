#!/usr/bin/env node
// scripts/design-system/ledger-build.mjs
//
// Debt-ledger proposal builder. Turns a design-system audit report (the
// scripts/design-system-locks/audit.mjs output shape: { debt: { fingerprints,
// perRule }, errors, scannedFileCount, ... }) into a proposed
// design-system/enforcement/baseline.json + ledger.json pair, plus a stats
// and blocking report — WITHOUT writing to design-system/enforcement/ itself.
// The lead reviews the --out directory and installs baseline.json/ledger.json
// by hand.
//
// This is a tracked port of the wave-4 prep script
// dist/design-system-baseline/cli-lanes/fanout/L12/build-with-decisions.mjs
// (gitignored scratch dir — not reproducible from a fresh checkout). Logic is
// unchanged; see the "Ported from L12" note below for the differences.
//
// Every fingerprint is SHA-256 of JSON.stringify([ruleId, path, syntax])
// (design-system/enforcement/baseline.schema.json). Unsupported/parser
// findings are NEVER baselined or ledgered — contract.json's failClosed rule
// — they only ever reach proposal-blocking.json.
//
// Six lead ownership decisions (paths/classifications the lead has already
// assigned an owner and checkpoint to, ahead of surfaces.json lane coverage)
// live in design-system/enforcement/ledger-decisions.json, not in this file —
// see loadDecisions() below for the schema.
//
// Ported from L12: three variables in the original script were read or
// computed and then never used anywhere else (confirmed by grep — no second
// reference):
//   - `runInputs` (read from the gitignored, non-reproducible
//     dist/design-system-baseline/cli-lanes/b-integrated-2ec-inputs.json —
//     this one is a functional bug, not just dead code: keeping it would
//     make this tracked CLI crash for anyone without that exact untracked
//     scratch file);
//   - `calLedgerPath`/`contractPath` (declared, never loaded).
// All three are dropped here; dropping an unread value changes no computed
// output. Also: the original computed `validLanes` from surfaces.json but
// never checked ledger-entry lanes against it, despite L12/FINAL-REPORT.md
// claiming "Validates every ledger entry lane against surfaces.json (all
// pass)" — no such check exists in the code. Reported, not silently carried
// forward as a real feature; also dropped as dead code here.
//
// CLI:
//   node scripts/design-system/ledger-build.mjs \
//     --audit <audit-report.json> \
//     [--decisions design-system/enforcement/ledger-decisions.json] \
//     --out <output-dir>
//
// Writes to --out only, never to design-system/enforcement/ directly:
//   proposal-baseline.json, proposal-ledger.json, proposal-blocking.json,
//   proposal-stats.json, proposal-meta.json.

import { createHash } from 'node:crypto'
import { createRequire } from 'node:module'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { execSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const require = createRequire(import.meta.url)
const Ajv2020 = require('ajv/dist/2020')

export const LANE_NAMES = {
  1: 'Workspace board, list, graph and task/plan controls',
  2: 'Calendar and workspace management/team/settings',
  3: 'Chat, sessions and conversation surfaces',
  4: 'Library, knowledge, media and document previews',
  5: 'Agents and skills',
  6: 'Settings, providers, connectors and policy controls',
  7: 'Shell, navigation, authentication/onboarding, profile and usage',
  8: 'Live browser, global search/approval overlays and remaining integration surfaces',
}

export const RULE_CHECKPOINT = {
  'ts-colors/raw-color': 'C1',
  'ts-colors/undefined-token': 'C1',
  'css-colors/raw-color': 'C1',
  'css-colors/unregistered-token': 'C1',
  'typography/text-size-below-floor': 'C1',
  'typography/font-size-below-floor': 'C1',
  'typography/arbitrary-text-size': 'C1',
  'typography/font-weight-arbitrary': 'C1',
  'typography/line-height-arbitrary': 'C1',
  'typography/letter-spacing-arbitrary': 'C1',
  'typography/font-family-off-token': 'C1',
  'typography/font-family-literal': 'C1',
  'typography/unregistered-font-size-token': 'C1',
  'typography/missing-arbitrary-type-hint': 'C1',
  'typography/token-shadow': 'C1',
  'spacing/root-dependent': 'C1',
  'spacing/off-scale': 'C1',
  'spacing/missing-safe-area-fallback': 'C1',
  'spacing/invalid-var': 'C1',
  'design-system/status-literal': 'C1',
  'design-system/status-mismatch': 'C1',
  'controls/raw-button': 'C2',
  'controls/checkbox-as-switch': 'C2',
  'controls/global-confirm': 'C2',
  'controls/shadcn-low-level-import': 'C5',
  'controls/radix-import': 'C5',
}

export const EXTENSION_RULES = new Set(['ts-colors/extension-boundary', 'typography/extension-boundary', 'spacing/extension-boundary'])

function quote(s) {
  const t = s.length > 100 ? `${s.slice(0, 100)}…` : s
  return `\`${t}\``
}

export const REPLACEMENT_TEMPLATES = {
  'ts-colors/raw-color': (s) => `Replace raw colour literal ${quote(s)} with the registered design-system colour token for the same role; if no registered token resolves to the rendered value, propose a token through the lead instead of keeping the literal.`,
  'ts-colors/undefined-token': (s) => `Resolve ${quote(s)} to a registered token id (map to the existing semantic/component token with the same resolved value, or use the governed token for that role); an undefined token must not survive C1.`,
  'css-colors/raw-color': (s) => `Replace raw colour literal ${quote(s)} in this stylesheet with the registered design-system colour token for the same role; if no registered token resolves to the rendered value, propose a token through the lead instead of keeping the literal.`,
  'css-colors/unregistered-token': (s) => `Reference ${quote(s)} through a registered design-system token: either register the component token through the lead or read the governed token that already carries this value.`,
  'typography/text-size-below-floor': (s) => `Raise ${quote(s)} to the 12px type floor: use the governed type-scale token at or above 12px for this role (named 12px role), or register a deliberate small-print token through the lead.`,
  'typography/font-size-below-floor': (s) => `Raise ${quote(s)} to the 12px type floor: use the governed type-scale token at or above 12px for this role (named 12px role), or register a deliberate small-print token through the lead.`,
  'typography/arbitrary-text-size': (s) => `Replace arbitrary text size ${quote(s)} with the governed type-scale token matching the intended role and size.`,
  'typography/font-weight-arbitrary': (s) => `Replace arbitrary font-weight ${quote(s)} with the registered fontWeight token for this role.`,
  'typography/line-height-arbitrary': (s) => `Replace arbitrary line-height ${quote(s)} with the registered lineHeight token for this role.`,
  'typography/letter-spacing-arbitrary': (s) => `Replace arbitrary letter-spacing ${quote(s)} with the registered letter-spacing token for this role.`,
  'typography/font-family-off-token': (s) => `Replace font utility ${quote(s)} with the governed font-family token/utility for this role.`,
  'typography/font-family-literal': (s) => `Replace literal font family ${quote(s)} with the registered fontFamily token.`,
  'typography/unregistered-font-size-token': (s) => `Route ${quote(s)} through the registered typography token set: map the legacy --font-* name to its --type-* token, or register the missing token through the lead.`,
  'typography/missing-arbitrary-type-hint': (s) => `State the intended type for ${quote(s)} with the proper utility hint (length: for size, color: for colour) and separately resolve the referenced token to a registered id; a type hint never approves an unknown token.`,
  'typography/token-shadow': (s) => `Consume ${quote(s)} through the generated token surface only; hand-copies of generated typography tokens must be removed in favour of the generated declarations.`,
  'spacing/root-dependent': (s) => `Express ${quote(s)} on the closed 4px/8px scale under the activated root font size: convert to the equivalent registered spacing token (hairline/1px border exception only where justified) so the value survives root-size changes.`,
  'spacing/off-scale': (s) => `Move ${quote(s)} onto the registered 4px/8px spacing scale using the nearest governed token that preserves the rendered value (hairline/1px exception only where justified).`,
  'spacing/missing-safe-area-fallback': (s) => `Supply a justified closed-scale fallback in ${quote(s)} — env(safe-area-inset-*, <registered closed-scale value>) — so layout degrades safely without the inset; the fallback value itself stays subject to central review.`,
  'spacing/invalid-var': (s) => `Replace the unregistered custom property in ${quote(s)} with a registered spacing token (or register the property in the token contract with a closed-scale value) so the value is governed.`,
  'design-system/status-literal': (s) => `Replace the raw status colour ${quote(s)} with the governed D4 status token for that state.`,
  'design-system/status-mismatch': (s) => `Align ${quote(s)} with the D4 status map: use the governed status token for the state instead of the off-palette literal.`,
  'controls/raw-button': (s) => `Replace raw ${quote(s)} with the governed Button primitive (variant/size props), or an approved low-level wrapper where the primitive provably cannot apply.`,
  'controls/checkbox-as-switch': (s) => `Replace ${quote(s)} with the governed Switch primitive.`,
  'controls/global-confirm': (s) => `Replace ${quote(s)} with the governed ConfirmDialog flow; window.confirm is forbidden in feature code.`,
  'controls/shadcn-low-level-import': (s) => `Relocate ${quote(s)}: import the public primitive/composite from the library entry instead of the low-level shadcn/ui internal; if the required public part does not exist yet, propose it through the lead.`,
  'controls/radix-import': (s) => `Move ${quote(s)} behind the governed ui wrapper: application and domain code consume the public primitive, never Radix directly; direct Radix imports remain only inside approved library wrapper implementations.`,
}

export function computeFingerprint(ruleId, filePath, syntax) {
  return createHash('sha256').update(JSON.stringify([ruleId, filePath, syntax])).digest('hex')
}

/**
 * design-system/enforcement/ledger-decisions.json schema:
 *   { version: 1, description: string, decisions: Decision[] }
 * Decision:
 *   - name (string, required): human-readable label used in reporting.
 *   - type (string, required): "catalogClassification" | "exactPaths".
 *   - checkpoint (string, required): one of ledger.schema.json's
 *     expiryCheckpoint enum (A, B, C1..C6) — the repair batch this
 *     decision's entries are due in.
 *   - owner (string, required): ownership label; a matched ledger entry's
 *     "owner" field is "<checkpoint> <owner>".
 *   - classifications (string[], required when type=catalogClassification):
 *     matches a finding when design-system/catalog.json has an entry for the
 *     finding's path whose "classification" is one of these values.
 *   - paths (string[], required when type=exactPaths): matches a finding
 *     when its path is exactly one of these repo-relative paths.
 * Decisions are tried in array order; the first match wins. A path already
 * resolved by lane ownership (surfaces.json) or an extension-boundary rule
 * never reaches this list — see buildLedgerProposal().
 */
export function loadDecisions(decisionsDoc) {
  const rawDecisions = Array.isArray(decisionsDoc?.decisions) ? decisionsDoc.decisions : []
  return rawDecisions.map((d) => {
    if (!d || typeof d.name !== 'string' || !d.name) throw new Error('ledger-decisions.json: decision missing "name"')
    if (!d.checkpoint) throw new Error(`ledger-decisions.json: decision "${d.name}" missing "checkpoint"`)
    if (!d.owner) throw new Error(`ledger-decisions.json: decision "${d.name}" missing "owner"`)
    if (d.type === 'catalogClassification') {
      if (!Array.isArray(d.classifications) || d.classifications.length === 0) {
        throw new Error(`ledger-decisions.json: decision "${d.name}" (type catalogClassification) missing non-empty "classifications"`)
      }
      const classifications = new Set(d.classifications)
      return {
        name: d.name,
        checkpoint: d.checkpoint,
        owner: d.owner,
        test: (_p, catEntry) => Boolean(catEntry) && classifications.has(catEntry.classification),
      }
    }
    if (d.type === 'exactPaths') {
      if (!Array.isArray(d.paths) || d.paths.length === 0) {
        throw new Error(`ledger-decisions.json: decision "${d.name}" (type exactPaths) missing non-empty "paths"`)
      }
      const paths = new Set(d.paths)
      return {
        name: d.name,
        checkpoint: d.checkpoint,
        owner: d.owner,
        test: (p) => paths.has(p),
      }
    }
    throw new Error(`ledger-decisions.json: decision "${d.name}" has unknown type "${d.type}"`)
  })
}

export function applyLeadDecision(filePath, catEntry, decisions) {
  for (const dec of decisions) {
    if (dec.test(filePath, catEntry)) return dec
  }
  return null
}

function fail(msg) {
  throw new Error(msg)
}

/**
 * Build the proposed baseline/ledger/blocking/stats documents from an audit
 * report and supporting inputs. Pure function: same inputs always give the
 * same output (proposal-meta.json's timestamp/gitHead are added by the CLI,
 * not by this function, so the function itself is fully deterministic).
 *
 * @param {object} params
 * @param {object} params.diag - parsed audit report ({ debt, errors, scannedFileCount })
 * @param {object} params.boundariesFrag - reviewed-source-boundaries.ledger.json ({ reviewedBoundaries })
 * @param {object} params.calBaseline - calendar-token-references.baseline.json ({ fingerprints })
 * @param {object} params.surfaces - design-system/surfaces.json ({ sourceOwners })
 * @param {object} params.catalog - design-system/catalog.json ({ entries })
 * @param {Array} params.decisions - output of loadDecisions()
 * @param {object} params.baselineSchema - baseline.schema.json
 * @param {object} params.ledgerSchema - ledger.schema.json
 */
export function buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema }) {
  const boundaryPaths = new Set(boundariesFrag.reviewedBoundaries.map((b) => b.path))
  const ownerLane = new Map(surfaces.sourceOwners.map((e) => [e.source, e.lane]))
  const catalogBySrc = new Map(catalog.entries.map((e) => [e.source, e]))

  // ---------- integrity: recompute every fingerprint ----------
  const fps = diag.debt.fingerprints
  for (const f of fps) {
    if (computeFingerprint(f.ruleId, f.path, f.syntax) !== f.fingerprint) {
      fail(`fingerprint mismatch ${f.fingerprint} (${f.ruleId} ${f.path})`)
    }
  }
  const seen = new Set()
  for (const f of fps) {
    if (seen.has(f.fingerprint)) fail(`duplicate fingerprint ${f.fingerprint}`)
    seen.add(f.fingerprint)
  }
  const perRuleSum = new Map()
  for (const f of fps) perRuleSum.set(f.ruleId, (perRuleSum.get(f.ruleId) ?? 0) + f.count)
  for (const [rule, total] of Object.entries(diag.debt.perRule)) {
    if (perRuleSum.get(rule) !== total) fail(`perRule mismatch for ${rule}`)
  }
  const acceptedCal = fps.filter((f) => f.accepted === true)
  const calFpSet = new Set(calBaseline.fingerprints.map((x) => x.fingerprint))
  if (acceptedCal.length !== calFpSet.size || !acceptedCal.every((f) => calFpSet.has(f.fingerprint))) {
    fail('diagnostic accepted set does not equal approved calendar fragment')
  }

  // ---------- unsupported / parser failures (ALWAYS BLOCKING, never suppressed) ----------
  const unsupported = diag.errors.filter((e) => e.code === 'unsupported')
  for (const e of unsupported) {
    if (!e.ruleId || !e.path || !e.syntax) fail(`unsupported error missing identity fields: ${e.message}`)
  }
  const unsupportedRecs = unsupported
    .map((e) => {
      const onBoundary = boundaryPaths.has(e.path)
      const lane = ownerLane.get(e.path)
      const inCatalog = catalogBySrc.has(e.path)
      return {
        computedFingerprint: computeFingerprint(e.ruleId, e.path, e.syntax),
        ruleId: e.ruleId,
        path: e.path,
        syntax: e.syntax,
        classification: onBoundary ? 'reviewed-boundary-path' : lane ? `lane-${lane}` : inCatalog ? 'ui-catalog' : 'unassigned',
        message: e.message,
      }
    })
    .sort((a, b) => a.computedFingerprint.localeCompare(b.computedFingerprint))
  const upIdentities = new Map()
  for (const r of unsupportedRecs) {
    upIdentities.set(r.computedFingerprint, (upIdentities.get(r.computedFingerprint) ?? 0) + 1)
    if (seen.has(r.computedFingerprint)) fail(`unsupported finding collides with debt fingerprint: ${r.ruleId} ${r.path}`)
  }
  const unsupportedIdentityCounts = [...upIdentities.entries()]
    .map(([fingerprint, count]) => ({ fingerprint, count }))
    .sort((a, b) => a.fingerprint.localeCompare(b.fingerprint))

  // ---------- classification of remaining fingerprints ----------
  const buckets = { ledger: [], boundaryExcluded: [], unresolvedUi: [], unresolvedUnassigned: [], unresolvedExtension: [], decidedLeadship: [] }
  const decisionStats = {}
  for (const dec of decisions) {
    decisionStats[dec.name] = { count: 0, paths: new Set() }
  }

  for (const f of fps) {
    if (f.accepted === true) continue
    if (boundaryPaths.has(f.path)) {
      buckets.boundaryExcluded.push(f)
      continue
    }
    if (EXTENSION_RULES.has(f.ruleId)) {
      buckets.unresolvedExtension.push(f)
      continue
    }

    const catEntry = catalogBySrc.get(f.path)
    const decision = applyLeadDecision(f.path, catEntry, decisions)
    if (decision) {
      buckets.decidedLeadship.push({ ...f, decision })
      decisionStats[decision.name].count += 1
      decisionStats[decision.name].paths.add(f.path)
      continue
    }

    const lane = ownerLane.get(f.path)
    if (lane !== undefined && RULE_CHECKPOINT[f.ruleId]) {
      buckets.ledger.push({ ...f, lane })
      continue
    }
    if (catalogBySrc.has(f.path)) {
      buckets.unresolvedUi.push(f)
      continue
    }
    buckets.unresolvedUnassigned.push(f)
  }
  const accounted =
    acceptedCal.length +
    buckets.boundaryExcluded.length +
    buckets.ledger.length +
    buckets.decidedLeadship.length +
    buckets.unresolvedUi.length +
    buckets.unresolvedUnassigned.length +
    buckets.unresolvedExtension.length
  if (accounted !== fps.length) {
    fail(`accounting mismatch: ${accounted} != ${fps.length}`)
  }

  // ---------- build proposal baseline + ledger ----------
  const ledgerEntries = buckets.ledger
    .map((f) => {
      const checkpoint = RULE_CHECKPOINT[f.ruleId]
      if (!checkpoint) fail(`no checkpoint for ledger rule ${f.ruleId}`)
      const owner = `${checkpoint} Lane ${f.lane}: ${LANE_NAMES[f.lane]}`
      return {
        fingerprint: f.fingerprint,
        ruleId: f.ruleId,
        path: f.path,
        syntax: f.syntax,
        owner,
        replacement: REPLACEMENT_TEMPLATES[f.ruleId](f.syntax),
        expiryCheckpoint: checkpoint,
      }
    })
    .concat(
      buckets.decidedLeadship.map((f) => {
        const dec = f.decision
        const owner = `${dec.checkpoint} ${dec.owner}`
        return {
          fingerprint: f.fingerprint,
          ruleId: f.ruleId,
          path: f.path,
          syntax: f.syntax,
          owner,
          replacement: REPLACEMENT_TEMPLATES[f.ruleId](f.syntax),
          expiryCheckpoint: dec.checkpoint,
        }
      }),
    )
    .sort((a, b) => a.fingerprint.localeCompare(b.fingerprint))

  const baselineFingerprints = buckets.ledger
    .concat(buckets.decidedLeadship)
    .map((f) => ({ fingerprint: f.fingerprint, ruleId: f.ruleId, path: f.path, syntax: f.syntax, count: f.count }))
    .sort((a, b) => a.fingerprint.localeCompare(b.fingerprint))

  if (baselineFingerprints.length !== ledgerEntries.length) fail('baseline/ledger length mismatch')
  for (let i = 0; i < baselineFingerprints.length; i += 1) {
    if (baselineFingerprints[i].fingerprint !== ledgerEntries[i].fingerprint) {
      fail(`baseline/ledger row misalignment at ${i}`)
    }
    if (computeFingerprint(ledgerEntries[i].ruleId, ledgerEntries[i].path, ledgerEntries[i].syntax) !== ledgerEntries[i].fingerprint) {
      fail(`ledger row fingerprint recompute failed at ${i}`)
    }
  }
  const proposalBaseline = { version: 1, fingerprints: baselineFingerprints }
  const proposalLedger = { version: 1, entries: ledgerEntries, exceptions: [] }

  // ---------- schema validation (build aborts before anything is written) ----------
  const ajv = new Ajv2020({ strict: false, allErrors: true })
  const vBase = ajv.compile(baselineSchema)
  if (!vBase(proposalBaseline)) fail(`baseline schema validation failed: ${JSON.stringify(vBase.errors)}`)
  const vLed = ajv.compile(ledgerSchema)
  if (!vLed(proposalLedger)) fail(`ledger schema validation failed: ${JSON.stringify(vLed.errors)}`)

  const leadDecisionsApplied = Object.fromEntries(
    decisions.map((d) => [d.name, { count: decisionStats[d.name].count, paths: Array.from(decisionStats[d.name].paths).sort() }]),
  )

  const proposalStats = {
    diagnostic: {
      scannedFileCount: diag.scannedFileCount,
      fingerprints: {
        total: fps.length,
        accepted: acceptedCal.length,
        unsupportedIdentities: unsupportedIdentityCounts.length,
      },
    },
    proposal: {
      ledger: {
        entries: ledgerEntries.length,
        decidedByLead: buckets.decidedLeadship.length,
      },
      unresolved: {
        uiCatalog: buckets.unresolvedUi.length,
        unassignedPaths: buckets.unresolvedUnassigned.length,
        runtimeExtensionBoundaries: buckets.unresolvedExtension.length,
      },
      blocking: unsupportedRecs.length,
    },
  }

  const proposalBlocking = { version: 1, unsupported: unsupportedRecs }

  return { proposalBaseline, proposalLedger, proposalBlocking, proposalStats, leadDecisionsApplied }
}

export function loadJson(p) {
  return JSON.parse(readFileSync(p, 'utf8'))
}

function parseArgs(argv) {
  const out = {}
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i]
    if (a === '--audit') out.audit = argv[++i]
    else if (a === '--decisions') out.decisions = argv[++i]
    else if (a === '--out') out.out = argv[++i]
  }
  return out
}

function main() {
  const args = parseArgs(process.argv.slice(2))
  if (!args.audit || !args.out) {
    console.error('usage: node ledger-build.mjs --audit <audit-report.json> [--decisions design-system/enforcement/ledger-decisions.json] --out <output-dir>')
    process.exitCode = 2
    return
  }

  const scriptDir = path.dirname(fileURLToPath(import.meta.url))
  const root = path.resolve(scriptDir, '..', '..')
  const resolve = (p) => (path.isAbsolute(p) ? p : path.join(root, p))

  const auditPath = resolve(args.audit)
  const decisionsPath = resolve(args.decisions ?? 'design-system/enforcement/ledger-decisions.json')
  const outDir = resolve(args.out)

  const boundariesPath = path.join(root, 'design-system/enforcement/approved-fragments/reviewed-source-boundaries.ledger.json')
  const calBasePath = path.join(root, 'design-system/enforcement/approved-fragments/calendar-token-references.baseline.json')
  const surfacesPath = path.join(root, 'design-system/surfaces.json')
  const catalogPath = path.join(root, 'design-system/catalog.json')
  const baselineSchemaPath = path.join(root, 'design-system/enforcement/baseline.schema.json')
  const ledgerSchemaPath = path.join(root, 'design-system/enforcement/ledger.schema.json')

  console.log(`Reading audit from: ${auditPath}`)
  console.log(`Reading decisions from: ${decisionsPath}`)
  console.log(`Output directory: ${outDir}`)

  let result
  try {
    const diag = loadJson(auditPath)
    const decisionsDoc = loadJson(decisionsPath)
    const decisions = loadDecisions(decisionsDoc)
    result = buildLedgerProposal({
      diag,
      boundariesFrag: loadJson(boundariesPath),
      calBaseline: loadJson(calBasePath),
      surfaces: loadJson(surfacesPath),
      catalog: loadJson(catalogPath),
      decisions,
      baselineSchema: loadJson(baselineSchemaPath),
      ledgerSchema: loadJson(ledgerSchemaPath),
    })
  } catch (err) {
    console.error(`BUILD ABORTED: ${err.message}`)
    process.exitCode = 1
    return
  }

  const { proposalBaseline, proposalLedger, proposalBlocking, proposalStats, leadDecisionsApplied } = result

  const meta = {
    timestamp: new Date().toISOString(),
    diagnosticInput: path.relative(root, auditPath),
    diagnosticHash: createHash('sha256').update(readFileSync(auditPath)).digest('hex'),
    gitHead: execSync('git rev-parse HEAD', { cwd: root }).toString().trim(),
    leadDecisionsApplied,
  }

  mkdirSync(outDir, { recursive: true })
  writeFileSync(path.join(outDir, 'proposal-baseline.json'), JSON.stringify(proposalBaseline, null, 2))
  writeFileSync(path.join(outDir, 'proposal-ledger.json'), JSON.stringify(proposalLedger, null, 2))
  writeFileSync(path.join(outDir, 'proposal-stats.json'), JSON.stringify(proposalStats, null, 2))
  writeFileSync(path.join(outDir, 'proposal-meta.json'), JSON.stringify(meta, null, 2))
  writeFileSync(path.join(outDir, 'proposal-blocking.json'), JSON.stringify(proposalBlocking, null, 2))

  console.log('Builder completed')
  console.log(`  Ledger entries: ${proposalLedger.entries.length} (${proposalStats.proposal.ledger.decidedByLead} from lead decisions)`)
  console.log(
    `  Unresolved: ${proposalStats.proposal.unresolved.uiCatalog} (ui) + ${proposalStats.proposal.unresolved.unassignedPaths} (unassigned) + ${proposalStats.proposal.unresolved.runtimeExtensionBoundaries} (extension)`,
  )
  console.log(`  Blocking: ${proposalStats.proposal.blocking}`)
  console.log('exit=0')
}

const isMainModule = process.argv[1] && fileURLToPath(import.meta.url) === fileURLToPath(`file://${process.argv[1]}`)
if (isMainModule) {
  main()
}
