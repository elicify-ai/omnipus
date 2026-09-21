#!/usr/bin/env node
/**
 * label-canonicalise — collapse every form label onto the catalogued `Label`
 * component's own typography, and widen the 4px label->control gap to 8px.
 *
 * Why this exists: the founder reported "labels should be consistent" and "in a
 * number of forms the label over inputs has not enough spacing". A scan of 110
 * `<Label>` sites found 42 carrying a className override — 26 merely re-stating
 * a value `Label` already applies (a standing drift vector), and 14 forcing
 * `utility-xs` (12px) where the component's `body-compact` (14px) is canonical.
 *
 * This edits ONLY the className attribute's value, never the tag's line
 * structure. An earlier version rewrote whole tags and collapsed their
 * whitespace, which joined multi-line JSX attributes onto one long line — this
 * repo has no Prettier to put them back, so the codemod must preserve layout.
 *
 * Deliberately NOT touched:
 *  - `--type-caption-size` overrides; at least one (PlanMemberFields) is
 *    documented in-code as an intentional divergence.
 *  - `text-[var(--color-muted)]`. In a read-only two-column row the muted
 *    label is intended hierarchy, not drift.
 *
 * Run with --write to apply; default is a dry run.
 */
import fs from 'fs'
import path from 'path'

const WRITE = process.argv.includes('--write')

const files = []
;(function walk(d) {
  for (const e of fs.readdirSync(d, { withFileTypes: true })) {
    const p = path.join(d, e.name)
    if (e.isDirectory()) walk(p)
    else if (p.endsWith('.tsx') && !p.includes('.test.') && !p.includes('.stories.')) files.push(p)
  }
})('src')

// Values `Label` already supplies, plus the one genuine size divergence.
const STRIP = [
  'text-[length:var(--type-body-compact-size)]',
  'text-[length:var(--type-utility-xs-size)]',
  'text-[var(--color-secondary)]',
  'font-medium',
]

const report = []
let changed = 0

for (const f of files) {
  const src = fs.readFileSync(f, 'utf8')
  let out = src

  out = out.replace(/<Label\b[\s\S]*?>/g, (tag) => {
    if (/--type-caption-size/.test(tag)) return tag // documented divergence
    let t = tag

    // Static className only. A template-literal className carries conditional
    // branches whose classes we have not classified, so it is left alone.
    t = t.replace(/className="([^"]*)"/, (attr, value) => {
      const kept = value.split(/\s+/).filter((c) => c && !STRIP.includes(c))
      if (kept.length === value.split(/\s+/).filter(Boolean).length) return attr
      report.push(`${f}\ttypography`)
      return kept.length ? `className="${kept.join(' ')}"` : '\u0000'
    })

    // Drop a now-empty className attribute along with the whitespace that
    // preceded it, and delete the line entirely if nothing else was on it.
    t = t.replace(/\n[ \t]*\u0000(?=\n)/g, '').replace(/[ \t]*\u0000/g, '')

    // The 4px gap where it sits on the Label itself.
    const g = t.replace(/mb-\[var\(--space-1\)\]/g, 'mb-[var(--space-2)]')
    if (g !== t) report.push(`${f}\tgap mb`)
    t = g

    return t
  })

  // A 4px gap on a wrapper whose FIRST child is a Label. The first-child test
  // is what keeps this off chat bubbles and calendar rows, which use the same
  // wrapper classes for unrelated reasons.
  for (const [re, tag] of [
    [/(<div className="(?:[^"]*\s)?flex flex-col gap-)\[var\(--space-1\)\](["\s][^>]*>\s*(?:\{[^}]*&&\s*)?)(<Label\b)/g, 'gap flex-col'],
    [/(<div className="(?:[^"]*\s)?space-y-)\[var\(--space-1\)\](["\s][^>]*>\s*(?:\{[^}]*&&\s*)?)(<Label\b)/g, 'gap space-y'],
  ]) {
    out = out.replace(re, (m, a, b, c) => { report.push(`${f}\t${tag}`); return `${a}[var(--space-2)]${b}${c}` })
  }

  if (out !== src) { changed++; if (WRITE) fs.writeFileSync(f, out) }
}

const counts = {}
for (const r of report) { const k = r.split('\t')[1]; counts[k] = (counts[k] || 0) + 1 }
console.log(WRITE ? 'APPLIED' : 'DRY RUN', '- files changed:', changed)
for (const [k, v] of Object.entries(counts)) console.log('  ', String(v).padStart(3), k)
