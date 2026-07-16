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
 *
 * `<Link>`/`<NavLink>` (TanStack Router): these render a bare `<a href>`
 * under the hood with no tabIndex of their own — three independent reviewers
 * found this as a systemic, repo-wide WebKit gap (every nav Link, not one
 * isolated bug). They're checked the same way `<a href>` is: an element with
 * a `to` prop (the router's navigation prop, standing in for `href`) must
 * also carry `tabIndex`. NOTE this only catches the router's `<Link>` by tag
 * name, which collides with `@phosphor-icons/react`'s unrelated `<Link>` SVG
 * icon component (used e.g. in WorkspaceHeader.tsx) — that collision is
 * harmless here because the icon never has a `to` prop, so `isNavigational`
 * is false for it and it's silently skipped rather than false-flagged.
 *
 * Radix `asChild` composites (e.g. `<DropdownMenuItem asChild><Link
 * .../></DropdownMenuItem>`) clone their OWN runtime-managed tabIndex onto
 * their single child (Radix Slot semantics) — a source-level tabIndex prop
 * on that child would fight the composite's management, so these are
 * legitimately exempt. Two ways to express that exemption were considered:
 * a hand-maintained allowlist of (file, line) pairs, or walking the JSX
 * ancestor chain for an `asChild` marker. The allowlist was rejected: line
 * numbers drift the moment the file is reformatted or another region edited
 * (this file, Sidebar.tsx, is under concurrent edit by another agent right
 * now), silently going stale in either direction — a moved exempt Link
 * starts failing, or a moved non-exempt one stops being checked. The
 * ancestor walk (`hasAsChildAncestor`) instead asks "is this element the
 * child of something Radix is managing", which is what actually matters,
 * survives file motion, and needs zero upkeep. `asChild` is a narrow,
 * deliberate Radix API marker not otherwise used in ordinary layout JSX, so
 * false-positive exemptions are not a practical concern.
 *
 * Incident note — `tabIndex` prop-forwarding pitfall (ModelSelector,
 * 2026-07): a component whose trigger renders a native `<button
 * tabIndex={tabIndex}>` with `tabIndex` an OPTIONAL prop and NO default value
 * is a latent WebKit trap even though the source line "has a tabIndex prop"
 * and looks compliant to this exact tripwire. Every call site historically
 * omitted the prop, so `tabIndex={undefined}` was rendered, which OMITS the
 * DOM attribute entirely (React does not render `tabindex` for `undefined`)
 * — identical in effect to never having written `tabIndex` at all, and
 * unreachable via Tab on Safari. This class of bug is invisible to AST
 * analysis (it's a runtime/type-level value, not a syntactic gap), so it
 * can't be caught here. The fix is a non-undefined default on any prop that
 * forwards to a native element's `tabIndex` (`tabIndex = 0` in the props
 * destructure, not `tabIndex?: number` alone) — flag this pattern in review
 * whenever a component forwards `tabIndex` as an optional prop.
 */
const SRC_ROOT = join(__dirname, '..')
const TARGETS = new Set(['button', 'summary', 'input', 'select', 'textarea'])
// TanStack Router link components. Both render a bare <a> with no tabIndex
// of their own; NavLink is not currently used in this repo but is included
// so the check doesn't silently miss it if introduced later.
const LINK_COMPONENTS = new Set(['Link', 'NavLink'])

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

/**
 * True when `node` sits inside a Radix `asChild` composite — walks the JSX
 * ancestor chain looking for any enclosing element carrying an `asChild`
 * attribute (shorthand `asChild` or `asChild={true}`; `asChild={false}` does
 * NOT count). See the file-level doc comment above for why this ancestor
 * walk is used instead of a hand-maintained (file, line) allowlist.
 */
function hasAsChildAncestor(node: ts.Node): boolean {
  let current: ts.Node | undefined = node.parent
  let hops = 0
  while (current && hops < 40) {
    if (ts.isJsxElement(current)) {
      const hasAsChild = current.openingElement.attributes.properties.some((p) => {
        if (!ts.isJsxAttribute(p) || p.name.getText() !== 'asChild') return false
        if (!p.initializer) return true // shorthand `asChild` => true
        if (ts.isJsxExpression(p.initializer) && p.initializer.expression) {
          if (p.initializer.expression.kind === ts.SyntaxKind.FalseKeyword) return false
        }
        return true
      })
      if (hasAsChild) return true
    }
    current = current.parent
    hops++
  }
  return false
}

function findOffenders(file: string): string[] {
  const src = readFileSync(file, 'utf8')
  const sf = ts.createSourceFile(file, src, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  const offenders: string[] = []
  const visit = (node: ts.Node) => {
    if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
      const tag = node.tagName.getText(sf)
      const isAnchor = tag === 'a'
      const isLinkComponent = LINK_COMPONENTS.has(tag)
      if (TARGETS.has(tag) || isAnchor || isLinkComponent) {
        const props = node.attributes.properties
        const hasTabIndex = props.some(
          (p) => ts.isJsxAttribute(p) && p.name.getText(sf) === 'tabIndex'
        )
        const hasHref = props.some(
          (p) => ts.isJsxAttribute(p) && p.name.getText(sf) === 'href'
        )
        const hasTo = props.some(
          (p) => ts.isJsxAttribute(p) && p.name.getText(sf) === 'to'
        )
        // Non-anchor/non-Link targets (button/input/…) are always checked.
        // Raw <a> is only checked when it navigates (has href — a named
        // anchor with no href is not interactive). <Link>/<NavLink> is only
        // checked when it navigates (has a `to` prop).
        const isNavigational = isAnchor ? hasHref : isLinkComponent ? hasTo : true
        if (!hasTabIndex && isNavigational && !hasAsChildAncestor(node)) {
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
