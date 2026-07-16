import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
import ts from 'typescript'

/**
 * Repo convention (operator-mandated): every native interactive JSX element
 * carries an EXPLICIT tabIndex. WebKit's default Tab policy (Safari macOS
 * default prefs; iPadOS with a hardware keyboard unless Full Keyboard Access
 * is on) visits ONLY form fields and elements with an explicit tabindex
 * attribute — native <button>/<a href> without one are unreachable by Tab
 * there. tabIndex={0} is order-neutral in Chrome/Firefox (DOM order), so the
 * stamp only changes WebKit reachability.
 *
 * Elements that manage their own tabIndex (roving widgets, the composer ring,
 * opt-outs via tabIndex={-1}) satisfy the rule by having ANY tabIndex prop.
 * Dynamic-tag components (e.g. ui/button.tsx's <Comp>) are out of AST reach —
 * they carry the stamp manually.
 */
const SRC_ROOT = join(__dirname, '..')
const TARGETS = new Set(['button', 'summary', 'input', 'select', 'textarea'])

function collectTsxFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) {
      if (entry === 'generated' || entry === 'node_modules') continue
      collectTsxFiles(full, out)
    } else if (
      full.endsWith('.tsx') &&
      !full.endsWith('.test.tsx') &&
      !full.endsWith('.gen.ts')
    ) {
      out.push(full)
    }
  }
  return out
}

function findOffenders(file: string): string[] {
  const src = readFileSync(file, 'utf8')
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const offenders: string[] = []
  const visit = (node: ts.Node) => {
    if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
      const tag = node.tagName.getText(sf)
      const isAnchor = tag === 'a'
      if (TARGETS.has(tag) || isAnchor) {
        const props = node.attributes.properties
        const hasTabIndex = props.some(
          (p) => ts.isJsxAttribute(p) && p.name.getText(sf) === 'tabIndex'
        )
        const hasHref = props.some(
          (p) => ts.isJsxAttribute(p) && p.name.getText(sf) === 'href'
        )
        if (!hasTabIndex && (!isAnchor || hasHref)) {
          const { line } = sf.getLineAndCharacterOfPosition(node.getStart())
          offenders.push(`${file.replace(SRC_ROOT, 'src')}:${line + 1} <${tag}>`)
        }
      }
    }
    ts.forEachChild(node, visit)
  }
  visit(sf)
  return offenders
}

describe('tabindex convention (WebKit tabbability)', () => {
  it('every native interactive JSX element in src/ has an explicit tabIndex', () => {
    const files = collectTsxFiles(SRC_ROOT)
    expect(files.length).toBeGreaterThan(100) // sanity: the scan actually ran
    const offenders = files.flatMap(findOffenders)
    expect(offenders, `Add tabIndex={0} (or a managed value) to:\n${offenders.join('\n')}`).toEqual([])
  })
})
