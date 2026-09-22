#!/usr/bin/env node
// Exact-source repair codemod — lane S-STATUS (Stage B closure, C1), puts
// status colour on the one official map: docs/internal/design/
// design-system-definition.md D4 ("There is one map. The task palette is
// the winner.") as materialised in src/design-system/status.ts's
// `statusContract`.
//
// Input already analysed by lane M5 (read-only):
//   dist/design-system-baseline/cli-lanes/c1-prep/M5/mapping.json / mapping.md
// This script does NOT read that dist/ artifact at run time (it is scratch
// evidence, not a build input) — the identifier -> D4-state table below is
// transcribed from M5's analysis once, the same way the reference codemod
// (codemod-dead-textclass-read.mjs) hardcodes its target function names. The
// D4 CANONICAL HEX TABLE, by contrast, is re-derived live from the decision
// doc on every run (parseD4CanonicalHexTable) so a future edit to D4 is
// re-checked, never silently trusted.
//
// TWO SEPARATE PASSES, two flags, because they have opposite risk profiles:
//
//   --literal   design-system/status-literal (13 items, 2 files:
//               src/lib/statusColors.ts, src/lib/planStateColors.ts).
//               Every literal hex already numerically matches its D4
//               canonical value (verified live, not just asserted) — the fix
//               replaces the inline hex with `statusContract.<state>.
//               resolvedColor` from src/design-system/status.ts. Pure
//               hygiene: identical rendered colour, identical DOM.
//
//   --mismatch  design-system/status-mismatch true-mismatches (13 ledger
//               fingerprints / 14 source occurrences — TaskDetailPanel.tsx
//               has one fingerprint covering two occurrences of an identical
//               class string) across 5 files (taskStatusConfig.ts,
//               TaskChecklistField.tsx, TaskDetailPanel.tsx, calendar/
//               types.ts, DefaultModelCard.tsx). These edits CHANGE the
//               rendered colour on purpose — the founder approved status-
//               colour unification (D4's "Visual delta: Normalization —
//               approved").
//
//               The same pass also SCANS (never edits) five more files the
//               ledger flagged under the same rule but M5 could not close
//               mechanically: AgentCard.tsx (cross-domain — agent status is
//               outside the 7-state D4 table, needs a decision) and four
//               likely-false-positives (LibraryPdfPreview.tsx, onboarding.tsx,
//               SetGoalToolUI.tsx, AskUserQuestionCard.tsx — muted body copy,
//               not status-color logic). Finding the exact flagged literal in
//               one of these five produces a REFUSAL with a citation, never
//               an edit — this is what closes group_a_status_mismatch's
//               "distinctFiles": 10 without inventing a mapping for any of
//               them.
//
// Every edit is exact-source: the codemod refuses (does not guess) whenever
// the literal it expected has drifted, the count of occurrences is wrong, or
// (TaskDetailPanel.tsx's shared string) the two occurrences can't be told
// apart from their own rendered text. Idempotent: a file already carrying the
// governed reference/new class string is treated as already-migrated and
// produces neither an edit nor a refusal.
import { relative, resolve } from 'node:path'
import ts from 'typescript'
import {
  applyEdits, isInAllowedRoot, parseCodemodArgs, parseSourceFile, readFile,
  unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

const D4_DOC_REL = 'docs/internal/design/design-system-definition.md'
const STATUS_MODULE_SPECIFIER = '@/design-system/status'
const STATUS_IMPORT_NAME = 'statusContract'

export const MODE_LITERAL = 'literal'
export const MODE_MISMATCH = 'mismatch'

// ── D4 canonical table — re-derived live from the decision doc every run ───

/** Parses the D4 section of design-system-definition.md into state -> UPPERCASE hex. */
export function parseD4CanonicalHexTable(repoRoot) {
  const path = resolve(repoRoot, D4_DOC_REL)
  const text = readFile(path)
  const lines = text.split('\n')
  const startIdx = lines.findIndex((l) => l.startsWith('### D4.'))
  if (startIdx === -1) throw new Error(`D4 decision heading not found in ${D4_DOC_REL} — refusing (cannot verify canonical colours)`)
  let endIdx = lines.findIndex((l, i) => i > startIdx && /^###\s/.test(l))
  if (endIdx === -1) endIdx = lines.length
  const rowRe = /^\|\s*([A-Za-z][^|]*?)\s*\|\s*[^|]+\|\s*`(#[0-9A-Fa-f]{6})`\s*\|/
  const table = new Map()
  for (const line of lines.slice(startIdx, endIdx)) {
    const m = rowRe.exec(line)
    if (!m) continue
    const label = m[1].replace(/\s*\([^)]*\)\s*$/, '').trim()
    const words = label.split(/\s+/)
    const key = words.map((w, i) => (i === 0 ? w.toLowerCase() : w[0].toUpperCase() + w.slice(1).toLowerCase())).join('')
    table.set(key, m[2].toUpperCase())
  }
  return table
}

// ── generic AST helpers ─────────────────────────────────────────────────

function findVariableObjectLiteral(source, varName) {
  let result = null
  const visit = (node) => {
    if (result) return
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === varName && node.initializer && ts.isObjectLiteralExpression(node.initializer)) {
      result = node.initializer
    }
    ts.forEachChild(node, visit)
  }
  visit(source)
  return result
}

function findVariableDeclaration(source, varName) {
  let result = null
  const visit = (node) => {
    if (result) return
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === varName) result = node
    ts.forEachChild(node, visit)
  }
  visit(source)
  return result
}

function getProperty(objLiteral, propName) {
  return objLiteral.properties.find(
    (p) => ts.isPropertyAssignment(p) && p.name && ((ts.isIdentifier(p.name) && p.name.text === propName) || (ts.isStringLiteral(p.name) && p.name.text === propName)),
  )
}

function getNestedProperty(objLiteral, propName, nestedName) {
  const outer = getProperty(objLiteral, propName)
  if (!outer || !ts.isObjectLiteralExpression(outer.initializer)) return null
  return getProperty(outer.initializer, nestedName)
}

function findStringLiteralsByExactText(source, text) {
  const found = []
  const visit = (node) => {
    if (ts.isStringLiteral(node) && node.text === text) found.push(node)
    ts.forEachChild(node, visit)
  }
  visit(source)
  return found
}

/** Preserves the source's own quote character when swapping a string literal's content. */
function quoteLike(stringLiteralNode, innerText) {
  const raw = stringLiteralNode.getText()
  const quote = raw[0]
  return quote + innerText + quote
}

function locOf(source, relFile, node) {
  const loc = source.getLineAndCharacterOfPosition(node.getStart())
  return `${relFile}:${loc.line + 1}:${loc.character + 1}`
}

/**
 * Ensures `import { statusContract } from '@/design-system/status'` exists.
 * Returns an insertion edit (or null if already present/already migrated).
 */
function ensureStatusContractImport(source) {
  for (const stmt of source.statements) {
    if (!ts.isImportDeclaration(stmt) || !ts.isStringLiteral(stmt.moduleSpecifier) || stmt.moduleSpecifier.text !== STATUS_MODULE_SPECIFIER) continue
    const bindings = stmt.importClause && stmt.importClause.namedBindings
    if (bindings && ts.isNamedImports(bindings) && bindings.elements.some((el) => (el.propertyName ?? el.name).text === STATUS_IMPORT_NAME)) {
      return null
    }
  }
  const imports = source.statements.filter((s) => ts.isImportDeclaration(s))
  if (imports.length > 0) {
    const last = imports[imports.length - 1]
    return { start: last.getEnd(), end: last.getEnd(), replacement: `\nimport { ${STATUS_IMPORT_NAME} } from '${STATUS_MODULE_SPECIFIER}'` }
  }
  return { start: 0, end: 0, replacement: `import { ${STATUS_IMPORT_NAME} } from '${STATUS_MODULE_SPECIFIER}'\n` }
}

/**
 * Plans the replacement for one property/variable whose current initializer
 * is expected to be the string literal `entry.oldValue`.
 *  - kind 'expression': replacement is a raw JS expression (no quoting).
 *  - kind 'literal': replacement is new STRING CONTENT, quoted like the source.
 * Returns { status: 'edit'|'already'|'refuse', ... }.
 */
function planLiteralReplacement({ relFile, source, node, entry }) {
  if (!node) {
    return { status: 'refuse', reason: 'expected declaration/property not found (structural drift)', syntax: entry.propName ?? entry.varName }
  }
  const init = node.initializer
  if (!init) {
    return { status: 'refuse', reason: 'declaration has no initializer (structural drift)', syntax: entry.propName ?? entry.varName, loc: locOf(source, relFile, node) }
  }
  const loc = locOf(source, relFile, init)
  if (ts.isStringLiteral(init)) {
    const currentText = init.text
    const oldMatches = entry.caseInsensitive ? currentText.toLowerCase() === entry.oldValue.toLowerCase() : currentText === entry.oldValue
    if (oldMatches) {
      const replacementCode = entry.kind === 'expression' ? entry.replacement : quoteLike(init, entry.replacement)
      return { status: 'edit', start: init.getStart(), end: init.getEnd(), replacement: replacementCode, loc, syntax: `${init.getText()} -> ${replacementCode}` }
    }
    if (entry.kind === 'literal' && currentText === entry.replacement) return { status: 'already' }
    return { status: 'refuse', reason: `expected literal ${JSON.stringify(entry.oldValue)}, found ${JSON.stringify(currentText)} — value changed since analysis, refusing rather than guess`, loc, syntax: init.getText() }
  }
  const currentCode = init.getText()
  if (entry.kind === 'expression' && currentCode === entry.replacement) return { status: 'already' }
  return { status: 'refuse', reason: 'unexpected non-literal initializer already present — refusing to guess', loc, syntax: currentCode }
}

// ── PASS 1: design-system/status-literal (invisible hygiene) ───────────────

const STATUS_COLORS_ENTRIES = [
  { propName: 'inbox', oldValue: '#9ca3af', d4State: 'inbox' },
  { propName: 'next', oldValue: '#3B82F6', d4State: 'next' },
  { propName: 'in_progress', oldValue: '#D4AF37', d4State: 'inProgress' },
  { propName: 'blocked', oldValue: '#F97316', d4State: 'blocked' },
  { propName: 'done', oldValue: '#10b981', d4State: 'done' },
  { propName: 'failed', oldValue: '#ef4444', d4State: 'failed' },
]
const TASK_CANCELLED_ENTRY = { varName: 'TASK_CANCELLED_COLOR', oldValue: '#EAB308', d4State: 'cancelled' }

const PLAN_STATE_COLORS_ENTRIES = [
  { propName: 'draft', oldValue: '#9ca3af', d4State: 'inbox' },
  { propName: 'approved', oldValue: '#3B82F6', d4State: 'next' },
  { propName: 'running', oldValue: '#D4AF37', d4State: 'inProgress' },
  { propName: 'done', oldValue: '#10b981', d4State: 'done' },
  { propName: 'failed', oldValue: '#ef4444', d4State: 'failed' },
]
const PLAN_CANCELLED_ENTRY = { varName: 'PLAN_CANCELLED_COLOR', oldValue: '#EAB308', d4State: 'cancelled' }

function planLiteralPassFile({ repoRoot, filePath, objectName, propertyEntries, variableEntry, d4Table }) {
  const relFile = relative(repoRoot, filePath)
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []
  let alreadyMigrated = 0

  const objLiteral = objectName ? findVariableObjectLiteral(source, objectName) : null
  if (objectName && !objLiteral) {
    for (const e of propertyEntries) refusals.push({ file: relFile, syntax: `${objectName}.${e.propName}`, reason: `object literal \`${objectName}\` not found (structural drift)` })
  } else {
    for (const entry of propertyEntries) {
      const d4Hex = d4Table.get(entry.d4State)
      if (!d4Hex) {
        refusals.push({ file: relFile, syntax: `${objectName}.${entry.propName}`, reason: `D4 doc no longer defines a canonical hex for state "${entry.d4State}" — refusing` })
        continue
      }
      if (d4Hex !== entry.oldValue.toUpperCase()) {
        refusals.push({ file: relFile, syntax: `${objectName}.${entry.propName}`, reason: `D4 canonical hex for "${entry.d4State}" is now ${d4Hex}, no longer ${entry.oldValue} — this would change rendered colour, not a hygiene fix; refusing` })
        continue
      }
      const node = getProperty(objLiteral, entry.propName)
      const plan = planLiteralReplacement({
        relFile, source, node,
        entry: { propName: entry.propName, oldValue: entry.oldValue, caseInsensitive: true, kind: 'expression', replacement: `statusContract.${entry.d4State}.resolvedColor` },
      })
      if (plan.status === 'edit') edits.push({ start: plan.start, end: plan.end, replacement: plan.replacement, loc: plan.loc, syntax: plan.syntax })
      else if (plan.status === 'already') alreadyMigrated += 1
      else refusals.push({ file: relFile, loc: plan.loc, syntax: plan.syntax ?? `${objectName}.${entry.propName}`, reason: plan.reason })
    }
  }

  if (variableEntry) {
    const d4Hex = d4Table.get(variableEntry.d4State)
    if (!d4Hex) {
      refusals.push({ file: relFile, syntax: variableEntry.varName, reason: `D4 doc no longer defines a canonical hex for state "${variableEntry.d4State}" — refusing` })
    } else if (d4Hex !== variableEntry.oldValue.toUpperCase()) {
      refusals.push({ file: relFile, syntax: variableEntry.varName, reason: `D4 canonical hex for "${variableEntry.d4State}" is now ${d4Hex}, no longer ${variableEntry.oldValue} — this would change rendered colour, not a hygiene fix; refusing` })
    } else {
      const node = findVariableDeclaration(source, variableEntry.varName)
      const plan = planLiteralReplacement({
        relFile, source, node,
        entry: { varName: variableEntry.varName, oldValue: variableEntry.oldValue, caseInsensitive: true, kind: 'expression', replacement: `statusContract.${variableEntry.d4State}.resolvedColor` },
      })
      if (plan.status === 'edit') edits.push({ start: plan.start, end: plan.end, replacement: plan.replacement, loc: plan.loc, syntax: plan.syntax })
      else if (plan.status === 'already') alreadyMigrated += 1
      else refusals.push({ file: relFile, loc: plan.loc, syntax: plan.syntax ?? variableEntry.varName, reason: plan.reason })
    }
  }

  if (edits.length > 0) {
    const importEdit = ensureStatusContractImport(source)
    if (importEdit) edits.push({ ...importEdit, loc: `${relFile}:1:1`, syntax: `+ import { ${STATUS_IMPORT_NAME} } from '${STATUS_MODULE_SPECIFIER}'` })
  }

  return { relFile, text, source, edits, refusals, alreadyMigrated }
}

// ── PASS 2: design-system/status-mismatch (rendered colour change) ─────────

const TASK_STATUS_CONFIG_ENTRIES = [
  { propName: 'next', oldValue: 'text-[color:var(--color-accent)] bg-[var(--color-accent)]/10', newValue: 'text-[color:var(--color-status-next)] bg-[var(--color-status-next)]/10' },
  { propName: 'in_progress', oldValue: 'text-[color:var(--color-warning)] bg-[var(--color-warning)]/10', newValue: 'text-[color:var(--color-status-in-progress)] bg-[var(--color-status-in-progress)]/10' },
  { propName: 'blocked', oldValue: 'text-[color:var(--color-warning)] bg-[var(--color-warning)]/10', newValue: 'text-[color:var(--color-status-blocked)] bg-[var(--color-status-blocked)]/10' },
]

function planTaskStatusConfig({ repoRoot, filePath }) {
  const relFile = relative(repoRoot, filePath)
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []
  let alreadyMigrated = 0
  const objLiteral = findVariableObjectLiteral(source, 'STATUS_BADGE')
  if (!objLiteral) {
    for (const e of TASK_STATUS_CONFIG_ENTRIES) refusals.push({ file: relFile, syntax: `STATUS_BADGE.${e.propName}`, reason: 'object literal `STATUS_BADGE` not found (structural drift)' })
    return { relFile, text, source, edits, refusals, alreadyMigrated }
  }
  for (const entry of TASK_STATUS_CONFIG_ENTRIES) {
    const node = getProperty(objLiteral, entry.propName)
    const plan = planLiteralReplacement({ relFile, source, node, entry: { propName: entry.propName, oldValue: entry.oldValue, caseInsensitive: false, kind: 'literal', replacement: entry.newValue } })
    if (plan.status === 'edit') edits.push({ start: plan.start, end: plan.end, replacement: plan.replacement, loc: plan.loc, syntax: plan.syntax })
    else if (plan.status === 'already') alreadyMigrated += 1
    else refusals.push({ file: relFile, loc: plan.loc, syntax: plan.syntax ?? `STATUS_BADGE.${entry.propName}`, reason: plan.reason })
  }
  return { relFile, text, source, edits, refusals, alreadyMigrated }
}

const CHECKLIST_ENTRIES = [
  { oldValue: 'shrink-0 text-[color:var(--color-warning)]', newValue: 'shrink-0 text-[color:var(--color-status-in-progress)]' },
  { oldValue: 'text-[color:var(--color-warning)]', newValue: 'text-[color:var(--color-status-in-progress)]' },
]

function planTaskChecklistField({ repoRoot, filePath }) {
  const relFile = relative(repoRoot, filePath)
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []
  let alreadyMigrated = 0
  for (const entry of CHECKLIST_ENTRIES) {
    const matches = findStringLiteralsByExactText(source, entry.oldValue)
    if (matches.length === 1) {
      const node = matches[0]
      edits.push({ start: node.getStart(), end: node.getEnd(), replacement: quoteLike(node, entry.newValue), loc: locOf(source, relFile, node), syntax: `${node.getText()} -> ${quoteLike(node, entry.newValue)}` })
    } else if (matches.length === 0) {
      const already = findStringLiteralsByExactText(source, entry.newValue).length
      if (already >= 1) alreadyMigrated += 1
      else refusals.push({ file: relFile, syntax: entry.oldValue, reason: 'expected literal not found (structural drift)' })
    } else {
      refusals.push({ file: relFile, syntax: entry.oldValue, reason: `expected exactly 1 occurrence, found ${matches.length} — refusing (ambiguous)` })
    }
  }
  return { relFile, text, source, edits, refusals, alreadyMigrated }
}

const TASK_DETAIL_PANEL_SHARED_OLD = 'h-8 text-xs bg-[var(--color-warning)]/10 text-[color:var(--color-warning)] border-transparent rounded-md px-2 inline-flex items-center'
const TASK_DETAIL_PANEL_IN_PROGRESS_NEW = 'h-8 text-xs bg-[var(--color-status-in-progress)]/10 text-[color:var(--color-status-in-progress)] border-transparent rounded-md px-2 inline-flex items-center'
const TASK_DETAIL_PANEL_BLOCKED_NEW = 'h-8 text-xs bg-[var(--color-status-blocked)]/10 text-[color:var(--color-status-blocked)] border-transparent rounded-md px-2 inline-flex items-center'

function planTaskDetailPanel({ repoRoot, filePath }) {
  const relFile = relative(repoRoot, filePath)
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []
  let alreadyMigrated = 0
  const matches = findStringLiteralsByExactText(source, TASK_DETAIL_PANEL_SHARED_OLD)
  if (matches.length === 0) {
    const already = findStringLiteralsByExactText(source, TASK_DETAIL_PANEL_IN_PROGRESS_NEW).length + findStringLiteralsByExactText(source, TASK_DETAIL_PANEL_BLOCKED_NEW).length
    if (already >= 2) alreadyMigrated += 2
    else refusals.push({ file: relFile, syntax: TASK_DETAIL_PANEL_SHARED_OLD, reason: 'expected shared amber badge string not found (structural drift)' })
    return { relFile, text, source, edits, refusals, alreadyMigrated }
  }
  if (matches.length !== 2) {
    refusals.push({ file: relFile, syntax: TASK_DETAIL_PANEL_SHARED_OLD, reason: `expected exactly 2 occurrences of the shared amber badge string (In Progress + Blocked), found ${matches.length} — refusing (ambiguous partial-migration state)` })
    return { relFile, text, source, edits, refusals, alreadyMigrated }
  }
  for (const node of matches) {
    let jsxEl = node.parent
    while (jsxEl && !ts.isJsxElement(jsxEl)) jsxEl = jsxEl.parent
    const childText = (jsxEl ? jsxEl.getText() : '').toLowerCase()
    const loc = locOf(source, relFile, node)
    let replacement
    if (childText.includes('in progress')) replacement = TASK_DETAIL_PANEL_IN_PROGRESS_NEW
    else if (childText.includes('blocked')) replacement = TASK_DETAIL_PANEL_BLOCKED_NEW
    else {
      refusals.push({ file: relFile, loc, syntax: node.getText(), reason: 'could not disambiguate "In Progress" vs "Blocked" from the enclosing JSX element\'s rendered text — refusing rather than guess' })
      continue
    }
    const replacementCode = quoteLike(node, replacement)
    edits.push({ start: node.getStart(), end: node.getEnd(), replacement: replacementCode, loc, syntax: `${node.getText()} -> ${replacementCode}` })
  }
  return { relFile, text, source, edits, refusals, alreadyMigrated }
}

const CALENDAR_ENTRIES = [
  { propName: 'inbox', oldValue: '#94A3B8', d4State: 'inbox' },
  { propName: 'next', oldValue: '#94A3B8', d4State: 'next' },
  { propName: 'in_progress', oldValue: '#60A5FA', d4State: 'inProgress' },
  { propName: 'blocked', oldValue: '#FBBF24', d4State: 'blocked' },
  { propName: 'done', oldValue: '#34D399', d4State: 'done' },
  { propName: 'failed', oldValue: '#F87171', d4State: 'failed' },
]

function planCalendarTypes({ repoRoot, filePath, d4Table }) {
  const relFile = relative(repoRoot, filePath)
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []
  let alreadyMigrated = 0
  const objLiteral = findVariableObjectLiteral(source, 'STATUS_STYLE')
  if (!objLiteral) {
    for (const e of CALENDAR_ENTRIES) refusals.push({ file: relFile, syntax: `STATUS_STYLE.${e.propName}.bg`, reason: 'object literal `STATUS_STYLE` not found (structural drift)' })
    return { relFile, text, source, edits, refusals, alreadyMigrated }
  }
  for (const entry of CALENDAR_ENTRIES) {
    if (!d4Table.has(entry.d4State)) {
      refusals.push({ file: relFile, syntax: `STATUS_STYLE.${entry.propName}.bg`, reason: `D4 doc no longer defines a canonical hex for state "${entry.d4State}" — refusing` })
      continue
    }
    const node = getNestedProperty(objLiteral, entry.propName, 'bg')
    const plan = planLiteralReplacement({
      relFile, source, node,
      entry: { propName: `${entry.propName}.bg`, oldValue: entry.oldValue, caseInsensitive: false, kind: 'expression', replacement: `statusContract.${entry.d4State}.resolvedColor` },
    })
    if (plan.status === 'edit') edits.push({ start: plan.start, end: plan.end, replacement: plan.replacement, loc: plan.loc, syntax: plan.syntax })
    else if (plan.status === 'already') alreadyMigrated += 1
    else refusals.push({ file: relFile, loc: plan.loc, syntax: plan.syntax ?? `STATUS_STYLE.${entry.propName}.bg`, reason: plan.reason })
  }
  if (edits.length > 0) {
    const importEdit = ensureStatusContractImport(source)
    if (importEdit) edits.push({ ...importEdit, loc: `${relFile}:1:1`, syntax: `+ import { ${STATUS_IMPORT_NAME} } from '${STATUS_MODULE_SPECIFIER}'` })
  }
  return { relFile, text, source, edits, refusals, alreadyMigrated }
}

function planDefaultModelCard({ repoRoot, filePath }) {
  const relFile = relative(repoRoot, filePath)
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []
  let alreadyMigrated = 0
  const OLD = 'mt-1 text-sm text-red-400'
  const NEW = 'mt-1 text-sm text-[color:var(--color-error)]'
  const matches = findStringLiteralsByExactText(source, OLD)
  if (matches.length === 1) {
    const node = matches[0]
    edits.push({ start: node.getStart(), end: node.getEnd(), replacement: quoteLike(node, NEW), loc: locOf(source, relFile, node), syntax: `${node.getText()} -> ${quoteLike(node, NEW)}` })
  } else if (matches.length === 0) {
    if (findStringLiteralsByExactText(source, NEW).length >= 1) alreadyMigrated += 1
    else refusals.push({ file: relFile, syntax: OLD, reason: 'expected literal not found (structural drift)' })
  } else {
    refusals.push({ file: relFile, syntax: OLD, reason: `expected exactly 1 occurrence, found ${matches.length} — refusing (ambiguous)` })
  }
  return { relFile, text, source, edits, refusals, alreadyMigrated }
}

// ── Excluded sites (mismatch pass only) — scan and REFUSE, never edit ──────
// Same ledger rule (design-system/status-mismatch), but M5 found no legal
// mechanical closure: one cross-domain policy question (AgentCard's agent
// 'draft' badge — agent status isn't one of D4's 7 task/plan states) and four
// likely-false-positives (muted body copy the scanner fired on for sitting
// near a status-shaped identifier, not for encoding a status colour).
const REFUSED_SITES = [
  {
    file: 'src/components/agents/AgentCard.tsx',
    literalText: 'text-[var(--color-warning)] border-[var(--color-warning)]/30 bg-[var(--color-warning)]/10',
    reason: "cross-domain-ambiguous (M5): agent 'draft' badge is a THIRD status domain outside D4's 7-state task/plan table — needs a founder/lead decision on whether Agent status joins the D4 map, not a mechanical rewrite.",
  },
  {
    file: 'src/components/library/preview/LibraryPdfPreview.tsx',
    literalText: 'mt-2 text-[var(--color-muted)]',
    reason: 'likely-false-positive (M5): muted caption text in a PDF-load error panel, not itself an encoding of task/plan/agent status.',
  },
  {
    file: 'src/routes/onboarding.tsx',
    literalText: 'var(--color-muted)',
    reason: 'likely-false-positive (M5): plain secondary/muted form-field label colour, unrelated to task/plan/agent status.',
  },
  {
    file: 'src/components/chat/tools/SetGoalToolUI.tsx',
    literalText: 'flex cursor-pointer list-none items-center gap-1.5 py-0.5 text-[var(--color-muted)]',
    reason: 'likely-false-positive (M5): muted summary label beside an already-correct --color-error icon — the actual failed-state colour here is right.',
  },
  {
    file: 'src/components/chat/AskUserQuestionCard.tsx',
    literalText: 'text-[var(--color-muted)]',
    reason: "likely-false-positive (M5): a dismissed/'Cancelled' question thread rendered muted rather than the vivid Cancelled amber — may be an intentional inert/historical treatment; needs a product decision, not a mechanical fix.",
  },
]

function planRefusedSite({ repoRoot, filePath, site }) {
  const relFile = relative(repoRoot, filePath)
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const refusals = []
  const matches = findStringLiteralsByExactText(source, site.literalText)
  if (matches.length > 0) {
    refusals.push({ file: relFile, loc: locOf(source, relFile, matches[0]), syntax: matches[0].getText(), reason: site.reason, occurrences: matches.length })
  }
  return { relFile, text, source, edits: [], refusals, alreadyMigrated: 0 }
}

// ── orchestration ────────────────────────────────────────────────────────

const LITERAL_FILES = [
  { file: 'src/lib/statusColors.ts', objectName: 'STATUS_COLORS', propertyEntries: STATUS_COLORS_ENTRIES, variableEntry: TASK_CANCELLED_ENTRY },
  { file: 'src/lib/planStateColors.ts', objectName: 'PLAN_STATE_COLORS', propertyEntries: PLAN_STATE_COLORS_ENTRIES, variableEntry: PLAN_CANCELLED_ENTRY },
]

const MISMATCH_EDIT_FILES = [
  { file: 'src/components/workspaces/taskStatusConfig.ts', plan: planTaskStatusConfig },
  { file: 'src/components/workspaces/TaskChecklistField.tsx', plan: planTaskChecklistField },
  { file: 'src/components/workspaces/TaskDetailPanel.tsx', plan: planTaskDetailPanel },
  { file: 'src/components/calendar/types.ts', plan: planCalendarTypes },
  { file: 'src/components/settings/DefaultModelCard.tsx', plan: planDefaultModelCard },
]

export function runCodemod({ repoRoot, apply, mode }) {
  if (mode !== MODE_LITERAL && mode !== MODE_MISMATCH) {
    throw new Error(`runCodemod: mode must be "${MODE_LITERAL}" or "${MODE_MISMATCH}", got ${JSON.stringify(mode)}`)
  }
  const d4Table = parseD4CanonicalHexTable(repoRoot)
  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0
  let totalAlreadyMigrated = 0
  let totalFilesScanned = 0

  const processOne = (relPathForGuard, planResult) => {
    const abs = resolve(repoRoot, relPathForGuard)
    if (!isInAllowedRoot(repoRoot, abs)) throw new Error(`refusing: ${relPathForGuard} is outside the allowed edit roots`)
    totalFilesScanned += 1
    const { relFile, text, edits, refusals, alreadyMigrated } = planResult
    let after = text
    if (edits.length > 0) after = applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
    const diff = edits.length > 0 ? unifiedDiff(relFile, text, after) : ''
    if (edits.length > 0 || refusals.length > 0) {
      fileResults.push({ file: relFile, editCount: edits.length, refusalCount: refusals.length, edits: edits.map((e) => ({ loc: e.loc, syntax: e.syntax })), refusals, diff })
    }
    totalEdits += edits.length
    totalRefusals += refusals.length
    totalAlreadyMigrated += alreadyMigrated
    if (apply && edits.length > 0) writeFileAtomic(abs, after)
  }

  const readOrMissing = (relPath) => {
    const abs = resolve(repoRoot, relPath)
    try {
      readFile(abs)
      return abs
    } catch (err) {
      if (err && err.code === 'ENOENT') return null
      throw err
    }
  }

  if (mode === MODE_LITERAL) {
    for (const spec of LITERAL_FILES) {
      const abs = readOrMissing(spec.file)
      if (!abs) {
        fileResults.push({ file: spec.file, editCount: 0, refusalCount: 1, edits: [], refusals: [{ file: spec.file, reason: 'file not found (structural drift)' }], diff: '' })
        totalFilesScanned += 1
        totalRefusals += 1
        continue
      }
      processOne(spec.file, planLiteralPassFile({ repoRoot, filePath: abs, objectName: spec.objectName, propertyEntries: spec.propertyEntries, variableEntry: spec.variableEntry, d4Table }))
    }
  } else {
    for (const spec of MISMATCH_EDIT_FILES) {
      const abs = readOrMissing(spec.file)
      if (!abs) {
        fileResults.push({ file: spec.file, editCount: 0, refusalCount: 1, edits: [], refusals: [{ file: spec.file, reason: 'file not found (structural drift)' }], diff: '' })
        totalFilesScanned += 1
        totalRefusals += 1
        continue
      }
      processOne(spec.file, spec.plan({ repoRoot, filePath: abs, d4Table }))
    }
    for (const site of REFUSED_SITES) {
      const abs = readOrMissing(site.file)
      if (!abs) {
        totalFilesScanned += 1
        continue
      }
      processOne(site.file, planRefusedSite({ repoRoot, filePath: abs, site }))
    }
  }

  return {
    mode,
    totalFilesScanned,
    totalFilesTouched: fileResults.filter((r) => r.editCount > 0).length,
    totalEdits,
    totalRefusals,
    totalAlreadyMigrated,
    files: fileResults,
    applied: apply,
  }
}

// ── CLI entry point ──────────────────────────────────────────────────────
const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const argv = process.argv.slice(2)
  const wantsLiteral = argv.includes('--literal')
  const wantsMismatch = argv.includes('--mismatch')
  if (wantsLiteral === wantsMismatch) {
    console.error('usage: codemod-status-source.mjs (--literal | --mismatch) [--apply] [--root <path>]')
    process.exit(1)
  }
  const { apply, root } = parseCodemodArgs(argv, resolve(new URL('../..', import.meta.url).pathname))
  const result = runCodemod({ repoRoot: root, apply, mode: wantsLiteral ? MODE_LITERAL : MODE_MISMATCH })
  for (const f of result.files) {
    if (f.diff) console.log(f.diff)
    for (const r of f.refusals) console.log(`REFUSED ${r.file}${r.loc ? ':' + r.loc.split(':').slice(1).join(':') : ''} \`${r.syntax ?? ''}\` — ${r.reason}`)
  }
  console.log(JSON.stringify({
    mode: result.mode,
    applied: result.applied,
    totalFilesScanned: result.totalFilesScanned,
    totalFilesTouched: result.totalFilesTouched,
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
    totalAlreadyMigrated: result.totalAlreadyMigrated,
  }, null, 2))
  process.exit(0)
}
