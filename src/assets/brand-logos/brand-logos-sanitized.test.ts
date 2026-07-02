// brand-logos-sanitized.test.ts — Build-time SVG allow-list guard (test #11).
//
// Reads every vendored SVG from the filesystem (via Node's `fs`) and asserts
// NONE contains any of the disallowed tokens that would indicate XSS potential.
//
// This is a defense-in-depth check that sits BELOW the DOMPurify runtime guard
// applied in brand-icon.tsx; it ensures the committed SVG files themselves are
// clean before any sanitization step runs.
//
// Traces to: connectors-providers-redesign-spec.md §7 test #11;
//            US-2 AS-4 (FR-015); BDD Scenario "Vendored SVGs pass sanitization".
//
// Disallowed tokens (spec §7 Test Dataset: SVG sanitization tokens):
//   <script         — embedded scripts
//   <style          — embedded stylesheets (also blocks @import-based exfil)
//   <foreignObject  — HTML injection via foreign content
//   <use            — SVG sprite reference (can be external)
//   on*=            — event handler attributes
//   javascript:     — javascript: URL in any attribute
//   external href   — http(s)://... in href= or xlink:href=

import { describe, it, expect } from 'vitest'
import { readdirSync, readFileSync } from 'fs'
import { join, dirname } from 'path'
import { fileURLToPath } from 'url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const LOGOS_DIR = join(__dirname)

// Collect all .svg files in the directory.
const svgFiles = readdirSync(LOGOS_DIR).filter((f) => f.endsWith('.svg'))

// Disallowed patterns — keyed by description for readable failure messages.
const DENY_PATTERNS: Array<{ name: string; re: RegExp }> = [
  { name: '<script>', re: /<script/i },
  { name: '<style>', re: /<style/i },
  { name: '<foreignObject>', re: /<foreignObject/i },
  { name: '<use>', re: /<use[\s>]/i },
  { name: 'on*= event handler', re: /\bon[a-z]+\s*=/i },
  { name: 'javascript: URL', re: /javascript\s*:/i },
  {
    name: 'external href',
    re: /(?:xlink:href|href)\s*=\s*["']https?:\/\//i,
  },
]

describe('Brand logo SVG sanitization guard (test #11)', () => {
  it('finds at least one vendored SVG file', () => {
    expect(svgFiles.length).toBeGreaterThan(0)
  })

  it('vendored SVG set contains the expected provider files', () => {
    const slugs = svgFiles.map((f) => f.replace(/^[pc]_/, '').replace(/\.svg$/, ''))
    // Spot-check a handful of required logos
    expect(slugs).toContain('openai')
    expect(slugs).toContain('anthropic')
    expect(slugs).toContain('telegram')
    expect(slugs).toContain('discord')
  })

  // One test per SVG file per deny-pattern for precise failure messages.
  for (const filename of svgFiles) {
    const filePath = join(LOGOS_DIR, filename)
    const content = readFileSync(filePath, 'utf-8')

    describe(`${filename}`, () => {
      for (const { name, re } of DENY_PATTERNS) {
        it(`must not contain ${name}`, () => {
          expect(content).not.toMatch(re)
        })
      }
    })
  }
})
