#!/usr/bin/env node
// Exact-source repair codemod — Stage B closure (design-system-migration-plan.md
// §"Stage B closure — founder decisions, 2026-09-19"), bucket B pattern P4
// (see dist/design-system-baseline/cli-lanes/claude-codemods/triage.json).
//
// Pattern: react-markdown's `<th>`/`<td>` renderers in
// src/components/chat/markdown-shared.tsx forward the WHOLE `style` object
// they receive from remark-gfm's table-cell handling. The founder's rule for
// P4 is "exact-source repair first": only what truly cannot be fixed becomes
// a registered third-party boundary. This case CAN be fixed, because the
// upstream shape is provably narrow — traced through the actual installed
// packages (read-only, not hardcoded from memory):
//
//   - mdast-util-to-hast (node_modules/mdast-util-to-hast/lib/handlers/table-row.js)
//     sets a hast `align` PROPERTY (not a style) on each `<td>`/`<th>`, taken
//     from the markdown table's column-alignment syntax (`:---:` etc).
//   - hast-util-to-jsx-runtime (node_modules/hast-util-to-jsx-runtime/lib/index.js,
//     `createElementProps`) is the ONLY place that ever turns that `align`
//     property into a `style` prop: `tableCellElement = new Set(['td','th'])`
//     gates it to table cells only, and the object it builds sets EXACTLY one
//     key — `style.textAlign = alignValue` (or `style['text-align']` under
//     `stylePropertyNameCase: 'css'`, which react-markdown does not set) —
//     never any other CSS property. `tableCellAlignToStyle` defaults to
//     `true` and react-markdown/markdown-shared.tsx never overrides it or
//     `stylePropertyNameCase`.
//
// So for a `<th>`/`<td>` renderer whose `style` prop originates from
// react-markdown's default pipeline, `style` is always either `undefined` or
// `{ textAlign: 'left' | 'center' | 'right' }` — never any other shape.
// Forwarding only `style?.textAlign` is therefore byte-for-byte equivalent
// to forwarding `style` whole, for every value the library can ever produce.
//
// Verification is RE-DERIVED from the installed packages on every run (see
// `verifyRemarkGfmStyleIsTextAlignOnly` below) — if a future upgrade changes
// the shape, this codemod stops treating the pattern as proven-safe and
// refuses everything rather than silently miscompiling code.
//
// It targets the SOURCE PATTERN, not a frozen file list: it scans every
// .ts/.tsx file under src/ and packages/ui/src/ for a `<th>`/`<td>` JSX
// element whose `style` attribute is a bare passthrough (`style={style}`) of
// a same-named parameter destructured from an object pattern that is
// TYPE-ANNOTATED with `CSSProperties` — refusing (not guessing) anything
// else: a different tag, a renamed/aliased binding, an untyped parameter, or
// a `style` expression more complex than the bare identifier.
import { relative, resolve } from 'node:path'
import ts from 'typescript'
import {
  applyEdits, isInAllowedRoot, listSourceFiles,
  parseCodemodArgs, parseSourceFile, readFile, unifiedDiff, writeFileAtomic,
} from './codemod-lib.mjs'

const TABLE_CELL_TAGS = new Set(['th', 'td'])

/**
 * Re-derives, from the installed node_modules sources (not from memory), that
 * remark-gfm/hast-util-to-jsx-runtime's table-cell `style` prop only ever
 * carries `textAlign`. Returns `{ proven, reason }`.
 */
export function verifyRemarkGfmStyleIsTextAlignOnly(repoRoot) {
  let toHastText
  let toJsxRuntimeText
  try {
    toHastText = readFile(resolve(repoRoot, 'node_modules/mdast-util-to-hast/lib/handlers/table-row.js'))
    toJsxRuntimeText = readFile(resolve(repoRoot, 'node_modules/hast-util-to-jsx-runtime/lib/index.js'))
  } catch (err) {
    return { proven: false, reason: `could not read the upstream packages to re-verify the style shape: ${err.message}` }
  }

  if (!/properties\.align\s*=\s*alignValue/.test(toHastText)) {
    return { proven: false, reason: 'mdast-util-to-hast/lib/handlers/table-row.js no longer sets an `align` property the way this codemod assumes' }
  }

  // The `createElementProps` guard: only table cells, only via the `align`
  // hast property, and the resulting style object sets exactly the
  // `textAlign`/`text-align` key from `alignValue` — nothing else.
  const gateOk = /tableCellElement\.has\(node\.tagName\)/.test(toJsxRuntimeText)
  const setOk = /style\[state\.stylePropertyNameCase === 'css' \? 'text-align' : 'textAlign'\]\s*=\s*\n?\s*alignValue/.test(toJsxRuntimeText)
  const tableCellsAreThTd = /tableCellElement\s*=\s*new Set\(\['td', 'th'\]\)/.test(toJsxRuntimeText)
  if (!gateOk || !setOk || !tableCellsAreThTd) {
    return { proven: false, reason: 'hast-util-to-jsx-runtime/lib/index.js no longer matches the proven `align`→`style.textAlign`-only shape this codemod assumes (table-cell gate, key assignment, or td/th set changed)' }
  }
  return { proven: true, reason: 'confirmed: table-cell `style` is produced ONLY from `align`, and only ever sets `textAlign`' }
}

function hasCssPropertiesType(paramNode) {
  const typeNode = paramNode.type
  if (!typeNode) return false
  return /CSSProperties/.test(typeNode.getText())
}

/** True when `bindingName` is a shorthand-bound `style` element of an object binding pattern. */
function findStyleBindingName(param) {
  if (!ts.isObjectBindingPattern(param.name)) return undefined
  for (const el of param.name.elements) {
    if (el.dotDotDotToken) continue
    const propName = el.propertyName ? el.propertyName.getText() : el.name.getText()
    if (propName === 'style' && ts.isIdentifier(el.name)) {
      if (el.propertyName && el.propertyName.getText() !== el.name.getText()) return undefined // aliased — refuse to guess
      return el.name.text
    }
  }
  return undefined
}

function narrowedReplacement(bindingName) {
  return `${bindingName}?.textAlign ? { textAlign: ${bindingName}.textAlign } : undefined`
}

function isAlreadyNarrowed(exprText, bindingName) {
  const expected = narrowedReplacement(bindingName).replace(/\s+/g, '')
  return exprText.replace(/\s+/g, '') === expected
}

/** Processes one file: returns { edits, refusals } — never mutates. */
export function planFileEdits(repoRoot, filePath) {
  const text = readFile(filePath)
  const source = parseSourceFile(filePath, text)
  const edits = []
  const refusals = []

  const visit = (node) => {
    if (ts.isArrowFunction(node) && node.parameters.length >= 1) {
      const param = node.parameters[0]
      if (ts.isObjectBindingPattern(param.name) && hasCssPropertiesType(param)) {
        const bindingName = findStyleBindingName(param)
        if (bindingName) {
          visitBodyForCellStyleAttrs(node.body, bindingName)
        }
      }
    }
    ts.forEachChild(node, visit)
  }

  function visitBodyForCellStyleAttrs(bodyNode, bindingName) {
    const walk = (n) => {
      if (ts.isJsxAttribute(n) && n.name.getText() === 'style' && n.initializer && ts.isJsxExpression(n.initializer) && n.initializer.expression) {
        const exprNode = n.initializer.expression
        const jsxOpening = n.parent.parent // JsxAttribute -> JsxAttributes -> JsxSelfClosingElement | JsxOpeningElement
        const tagName = ts.isJsxSelfClosingElement(jsxOpening) || ts.isJsxOpeningElement(jsxOpening) ? jsxOpening.tagName.getText() : undefined
        const loc = source.getLineAndCharacterOfPosition(n.getStart())
        const locStr = `${relative(repoRoot, filePath)}:${loc.line + 1}:${loc.character + 1}`
        const exprText = exprNode.getText()

        const isBareIdentifier = ts.isIdentifier(exprNode) && exprNode.text === bindingName
        const alreadyNarrowed = isAlreadyNarrowed(exprText, bindingName)

        if (alreadyNarrowed) {
          // Already fixed — no-op, not a refusal.
        } else if (isBareIdentifier && tagName && TABLE_CELL_TAGS.has(tagName)) {
          edits.push({
            start: exprNode.getStart(),
            end: exprNode.getEnd(),
            replacement: narrowedReplacement(bindingName),
            loc: locStr,
            syntax: `style={${exprText}}`,
          })
        } else if (isBareIdentifier && (!tagName || !TABLE_CELL_TAGS.has(tagName))) {
          refusals.push({
            file: relative(repoRoot, filePath),
            loc: locStr,
            syntax: `style={${exprText}}`,
            reason: `narrowing to textAlign is only proven safe for <th>/<td> (remark-gfm/hast-util-to-jsx-runtime only sets \`style\` on table cells) — found on <${tagName ?? '?'}>`,
          })
        } else {
          refusals.push({
            file: relative(repoRoot, filePath),
            loc: locStr,
            syntax: `style={${exprText}}`,
            reason: `ambiguous style expression on a table cell — expected a bare \`${bindingName}\` passthrough, found \`${exprText}\``,
          })
        }
      }
      ts.forEachChild(n, walk)
    }
    walk(bodyNode)
  }

  visit(source)
  return { edits, refusals, text, source }
}

export function runCodemod({ repoRoot, apply }) {
  const verification = verifyRemarkGfmStyleIsTextAlignOnly(repoRoot)
  if (!verification.proven) {
    return {
      proven: false,
      reason: verification.reason,
      totalFilesScanned: 0,
      totalFilesTouched: 0,
      totalEdits: 0,
      totalRefusals: 0,
      files: [],
      applied: apply,
    }
  }

  const files = listSourceFiles(repoRoot).filter((f) => isInAllowedRoot(repoRoot, f))
  const fileResults = []
  let totalEdits = 0
  let totalRefusals = 0
  for (const filePath of files) {
    const { edits, refusals, text } = planFileEdits(repoRoot, filePath)
    if (edits.length === 0 && refusals.length === 0) continue
    let after = text
    if (edits.length > 0) {
      after = applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
    }
    const diff = edits.length > 0 ? unifiedDiff(relative(repoRoot, filePath), text, after) : ''
    fileResults.push({
      file: relative(repoRoot, filePath),
      editCount: edits.length,
      refusalCount: refusals.length,
      edits: edits.map((e) => ({ loc: e.loc, syntax: e.syntax })),
      refusals,
      diff,
    })
    totalEdits += edits.length
    totalRefusals += refusals.length
    if (apply && edits.length > 0) writeFileAtomic(filePath, after)
  }

  return {
    proven: true,
    reason: verification.reason,
    totalFilesScanned: files.length,
    totalFilesTouched: fileResults.filter((r) => r.editCount > 0).length,
    totalEdits,
    totalRefusals,
    files: fileResults,
    applied: apply,
  }
}

// ── CLI entry point ──────────────────────────────────────────────────────
const isMain = import.meta.url === `file://${process.argv[1]}`
if (isMain) {
  const { apply, root } = parseCodemodArgs(process.argv.slice(2), resolve(new URL('../..', import.meta.url).pathname))
  const result = runCodemod({ repoRoot: root, apply })
  if (!result.proven) {
    console.error(`REFUSED ALL: ${result.reason}`)
  } else {
    for (const f of result.files) {
      if (f.diff) console.log(f.diff)
      for (const r of f.refusals) console.log(`REFUSED ${r.file}:${r.loc.split(':').slice(1).join(':')} \`${r.syntax}\` — ${r.reason}`)
    }
  }
  console.log(JSON.stringify({
    mode: result.applied ? 'apply' : 'dry-run',
    proven: result.proven,
    reason: result.reason,
    totalFilesScanned: result.totalFilesScanned,
    totalFilesTouched: result.totalFilesTouched,
    totalEdits: result.totalEdits,
    totalRefusals: result.totalRefusals,
  }, null, 2))
  process.exit(0)
}
