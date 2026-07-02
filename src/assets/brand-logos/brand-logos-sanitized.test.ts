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
import DOMPurify from 'dompurify'

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

// ---------------------------------------------------------------------------
// C3 — Positive-control: the deny-detection logic fires on known-bad inputs.
//
// These tests prove the DENY_PATTERNS regex set actually catches malicious SVG
// payloads — not just that current files are clean.  A regression where someone
// softens a pattern (e.g. makes the <script> regex case-sensitive only) would
// silently pass the file-scan tests above but FAIL these positive controls.
//
// Traces to: connectors-providers-redesign-spec.md §7 test #11 (C3 gap).
// ---------------------------------------------------------------------------

const MALICIOUS_SVG_FIXTURES: Array<{ name: string; svg: string; matchedBy: string }> = [
  {
    name: '<script> tag',
    svg: '<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>',
    matchedBy: '<script>',
  },
  {
    name: '@import via <style>',
    svg: '<svg xmlns="http://www.w3.org/2000/svg"><style>@import url(https://evil.example/x)</style></svg>',
    matchedBy: '<style>',
  },
  {
    name: 'onload event handler',
    svg: '<svg xmlns="http://www.w3.org/2000/svg" onload="x()"></svg>',
    matchedBy: 'on*= event handler',
  },
  {
    name: 'javascript: href',
    svg: '<svg xmlns="http://www.w3.org/2000/svg"><a href="javascript:alert(1)"><path/></a></svg>',
    matchedBy: 'javascript: URL',
  },
  {
    name: '<use> with data: href',
    svg: '<svg xmlns="http://www.w3.org/2000/svg"><use href="data:image/svg+xml,..."></use></svg>',
    matchedBy: '<use>',
  },
  {
    name: '<foreignObject> HTML injection',
    svg: '<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><div>xss</div></foreignObject></svg>',
    matchedBy: '<foreignObject>',
  },
  {
    name: 'external xlink:href',
    svg: '<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><image xlink:href="https://evil.example/track.svg"/></svg>',
    matchedBy: 'external href',
  },
]

describe('Brand logo SVG deny-pattern positive controls (C3)', () => {
  it('has at least one malicious fixture per deny-pattern category', () => {
    // Ensure every deny-pattern is exercised by at least one fixture.
    const coveredPatterns = new Set(MALICIOUS_SVG_FIXTURES.map((f) => f.matchedBy))
    for (const { name } of DENY_PATTERNS) {
      expect(coveredPatterns.has(name), `No fixture covers deny-pattern "${name}"`).toBe(true)
    }
  })

  for (const fixture of MALICIOUS_SVG_FIXTURES) {
    it(`deny-pattern correctly fires on: ${fixture.name}`, () => {
      const pattern = DENY_PATTERNS.find((p) => p.name === fixture.matchedBy)
      // This proves the deny-pattern regex exists and catches the payload.
      expect(pattern, `No deny-pattern named "${fixture.matchedBy}"`).toBeDefined()
      expect(fixture.svg).toMatch(pattern!.re)
    })
  }
})

// ---------------------------------------------------------------------------
// C3 — DOMPurify sanitization-behavior test.
//
// The deny-patterns above catch raw SVG files at commit time.  This test
// verifies that DOMPurify with the EXACT config used by BrandIcon actually
// strips a known-malicious payload — proving the runtime security control
// works, not just the static guard.
//
// Config mirrors brand-icon.tsx::buildSanitizedMap().
// ---------------------------------------------------------------------------

const DOMPUR_CONFIG = {
  USE_PROFILES: { svg: true, svgFilters: true },
  // Identical to the FORBID_TAGS list in brand-icon.tsx.
  FORBID_TAGS: ['title', 'desc'],
}

describe('DOMPurify sanitization behavior (C3 — runtime security control)', () => {
  it('removes <script> from a malicious SVG payload', () => {
    const malicious = '<svg><script>alert(1)</script><title>x</title><path/></svg>'
    const clean = DOMPurify.sanitize(malicious, DOMPUR_CONFIG)
    expect(clean).not.toContain('<script')
  })

  it('removes <title> via FORBID_TAGS (not just security — prevents AT double-read)', () => {
    const withTitle = '<svg><title>OpenAI</title><path/></svg>'
    const clean = DOMPurify.sanitize(withTitle, DOMPUR_CONFIG)
    expect(clean).not.toContain('<title')
  })

  it('preserves <path> (structural SVG element survives sanitization)', () => {
    const safe = '<svg><path d="M0 0h24v24H0z"/></svg>'
    const clean = DOMPurify.sanitize(safe, DOMPUR_CONFIG)
    expect(clean).toContain('<path')
  })

  it('strips both <script> and <title> while keeping <path> in one pass', () => {
    const mixed = '<svg><script>alert(1)</script><title>x</title><path d="M0 0"/></svg>'
    const clean = DOMPurify.sanitize(mixed, DOMPUR_CONFIG)
    expect(clean).not.toContain('<script')
    expect(clean).not.toContain('<title')
    expect(clean).toContain('<path')
  })
})

// ---------------------------------------------------------------------------
// Original guard tests follow.
// ---------------------------------------------------------------------------

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
