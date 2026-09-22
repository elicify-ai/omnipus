#!/usr/bin/env node

// ---------------------------------------------------------------------------
// Stage B typography lock — D1/D2/D9 of docs/internal/design/design-system-definition.md.
//
// Contract: design-system/enforcement/contract.json — scan({ path, source, policy })
// returns Finding[] synchronously; `syntax` is the canonical offending syntax
// without position/trivia; scanners report raw findings and never apply
// policy/exceptions (the orchestrator owns that); parser failures and
// unsupported governed syntax become explicit findings, never empty success.
//
// Spec anchors (every constant below traces to one of these):
// - §D2  hard 12px floor for every UI text → FLOOR_PX = 12.
// - §D1  root is clamp(12px, var(--user-font-size, 14px), 20px) → rem sizes
//   are evaluated across ROOT_MIN_PX..ROOT_MAX_PX; foundations.json agrees
//   (font.root.minimum/default/maximum = 12/14/20px).
// - §D10 320px reflow floor → 1vw >= 3.2px, the only static bound for vw.
// - §E1  "computed UI text below 12px, or an arbitrary text-size utility" fail.
// - §D9  families are Outfit/Inter/JetBrains Mono through tokens; arbitrary
//   family values are rejected outside the exception registry.
// - Tailwind 4.3.3 defaults (node_modules/tailwindcss/theme.css): text-xs =
//   0.75rem, text-sm = 0.875rem, text-base = 1rem, larger steps >= 1.125rem.
//
// Composition with the browser harness: this lock proves nothing about the
// rendered DOM. From source it proves that a size expression cannot compute
// below 12px under the constitutional root/viewport bounds, or that the value
// is a registered token. The Stage A browser harness (computed-style floor
// tests) is the independent runtime proof; the two compose as static gate +
// runtime gate — no arithmetic here is claimed as computed-DOM evidence.
//
// Known limitations (mirrored in the lane report):
// - Bare unknown `text-<word>` utilities are skipped: a per-file scanner
//   cannot see theme-registered custom utilities. Needs a central
//   theme-utility registry in policy (requested from the lead).
// - Whitespace-separated standalone interpolations (`${cond}` between whole
//   class names) are skipped; only boundary-gluing dynamics are flagged.
// - Condition-position identifiers in cn()/clsx() (`cond && 'class'`) are
//   treated as booleans, not class sources.
// - Standalone SVG markup is parsed at the attribute boundary and embedded
//   CSS is delegated to PostCSS; malformed governed attributes fail closed.
// ---------------------------------------------------------------------------

import {
  TS_EXTENSIONS,
} from './typography/class-value-rules.mjs'
import { scanCss } from './typography/css-scan.mjs'
import { scanTypeScript } from './typography/ts-scan.mjs'

export const extensions = ['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs', '.css', '.svg']

export function scanSvg({ path, source, registeredTokens, resolvedTokenValues }) {
  const findings = []
  // XML declarations, doctypes and comments are not JSX nodes and cannot
  // carry typography. Mask them while preserving newlines and offsets; the
  // remaining element/attribute structure is parsed by the TypeScript JSX AST.
  let jsxSource = source.replace(/<\?[\s\S]*?\?>|<!DOCTYPE[\s\S]*?>|<!--[\s\S]*?-->/gi, (text) => text.replace(/[^\n]/g, ' '))
  for (const match of source.matchAll(/<style\b[^>]*>([\s\S]*?)<\/style>/gi)) {
    const cssOffset = match.index + match[0].indexOf(match[1])
    const baseLine = source.slice(0, cssOffset).split('\n').length - 1
    for (const finding of scanCss({ path, source: match[1], registeredTokens, resolvedTokenValues })) {
      findings.push({ ...finding, line: finding.line ? finding.line + baseLine : undefined })
    }
    // CSS braces are JSX expression delimiters. Mask only the already-parsed
    // style body, retaining newlines so AST locations still map to the SVG.
    const masked = match[1].replace(/[^\n]/g, ' ')
    jsxSource = `${jsxSource.slice(0, cssOffset)}${masked}${jsxSource.slice(cssOffset + match[1].length)}`
  }
  const jsxFindings = scanTypeScript({ path, source: `const __svg = (${jsxSource})`, registeredTokens, resolvedTokenValues })
  findings.push(...jsxFindings)
  return findings
}

// ---------------------------------------------------------------------------
// Entry point (design-system/enforcement/contract.json scanner API).
// ---------------------------------------------------------------------------

export function scan({ path, source, policy, modules }) {
  if (typeof path !== 'string' || typeof source !== 'string') {
    throw new Error('typography scanner requires string path and source')
  }
  const tokenCssNames = Array.isArray(policy?.tokenCssNames) ? policy.tokenCssNames : []
  const registeredTokens = new Set(tokenCssNames)
  const derivedResolvedTokens = Object.fromEntries(Object.entries(policy?.resolvedTokens ?? {}).map(([id, value]) => [`--${id.replace(/([a-z0-9])([A-Z])/g, '$1-$2').replaceAll('.', '-').toLowerCase()}`, value]))
  const resolvedTokenValues = new Map(Object.entries(policy?.resolvedCssTokens ?? derivedResolvedTokens))
  const context = { path, source, registeredTokens, resolvedTokenValues, modules, moduleCache: new Map() }
  const lowerPath = path.toLowerCase()
  if (lowerPath.endsWith('.css')) return scanCss(context)
  if (lowerPath.endsWith('.svg')) return scanSvg(context)
  const extension = lowerPath.slice(lowerPath.lastIndexOf('.'))
  if (TS_EXTENSIONS.includes(extension)) return scanTypeScript(context)
  throw new Error(`typography scanner does not handle extension: ${path}`)
}
