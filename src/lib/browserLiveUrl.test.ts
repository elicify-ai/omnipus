import { describe, it, expect } from 'vitest'
import { normalizeNavigateUrl, resolveOmniboxInput } from './browserLiveUrl'

describe('normalizeNavigateUrl', () => {
  it('prepends https:// to a bare hostname', () => {
    expect(normalizeNavigateUrl('example.com')).toBe('https://example.com')
  })

  it('prepends https:// to a hostname with a path', () => {
    expect(normalizeNavigateUrl('example.com/foo/bar')).toBe('https://example.com/foo/bar')
  })

  it('prepends https:// to a bare host:port (no "//" present)', () => {
    expect(normalizeNavigateUrl('localhost:3000')).toBe('https://localhost:3000')
  })

  it('trims surrounding whitespace before normalizing', () => {
    expect(normalizeNavigateUrl('  example.com  ')).toBe('https://example.com')
  })

  it('leaves an explicit http:// URL untouched', () => {
    expect(normalizeNavigateUrl('http://example.com')).toBe('http://example.com')
  })

  it('leaves an explicit https:// URL untouched', () => {
    expect(normalizeNavigateUrl('https://example.com')).toBe('https://example.com')
  })

  it('leaves a non-http scheme with "://" untouched (server-side ValidateURL is the real gate)', () => {
    expect(normalizeNavigateUrl('ftp://example.com')).toBe('ftp://example.com')
  })

  it('returns null for an empty string', () => {
    expect(normalizeNavigateUrl('')).toBeNull()
  })

  it('returns null for a whitespace-only string', () => {
    expect(normalizeNavigateUrl('   ')).toBeNull()
  })

  it('neutralizes a bare "javascript:" scheme (no "//") by prepending https://', () => {
    // No "://" present, so this is NOT treated as an existing scheme — it
    // gets "https://" prepended, turning it into an inert URL rather than
    // honouring the javascript: scheme.
    expect(normalizeNavigateUrl('javascript:alert(1)')).toBe('https://javascript:alert(1)')
  })
})

describe('resolveOmniboxInput (ADR-040 D5)', () => {
  it('returns null for an empty string', () => {
    expect(resolveOmniboxInput('')).toBeNull()
  })

  it('returns null for a whitespace-only string', () => {
    expect(resolveOmniboxInput('   ')).toBeNull()
  })

  it('normalizes a bare hostname (has a dot, no spaces) into a URL', () => {
    expect(resolveOmniboxInput('example.com')).toBe('https://example.com')
  })

  it('normalizes a bare hostname with a path into a URL', () => {
    expect(resolveOmniboxInput('example.com/foo/bar')).toBe('https://example.com/foo/bar')
  })

  it('leaves an explicit https:// URL untouched', () => {
    expect(resolveOmniboxInput('https://example.com')).toBe('https://example.com')
  })

  it('leaves an explicit http:// URL untouched', () => {
    expect(resolveOmniboxInput('http://example.com/path?q=1')).toBe('http://example.com/path?q=1')
  })

  it('treats an explicit-scheme value as a URL even with no dot (server SSRF gate is the real authority)', () => {
    expect(resolveOmniboxInput('http://localhost:3000')).toBe('http://localhost:3000')
  })

  it('trims surrounding whitespace before deciding', () => {
    expect(resolveOmniboxInput('  example.com  ')).toBe('https://example.com')
  })

  it('routes a multi-word phrase to a Google search', () => {
    expect(resolveOmniboxInput('cheap flights to tokyo')).toBe(
      'https://www.google.com/search?q=cheap%20flights%20to%20tokyo',
    )
  })

  it('routes a bare word with no dot to a Google search (not treated as a domain)', () => {
    expect(resolveOmniboxInput('wikipedia')).toBe('https://www.google.com/search?q=wikipedia')
  })

  // Deliberate boundary: "host:port" with no dot and no explicit scheme is
  // NOT treated as a URL by the omnibox decision (ADR-040 D5's rule is
  // strictly "has a dot, no spaces, or an explicit scheme") — distinct from
  // normalizeNavigateUrl's own broader bare-hostname handling.
  it('routes a bare host:port with no dot to a Google search', () => {
    expect(resolveOmniboxInput('localhost:3000')).toBe('https://www.google.com/search?q=localhost%3A3000')
  })

  it('URL-encodes special characters in a search query', () => {
    expect(resolveOmniboxInput('C++ vs Rust?')).toBe('https://www.google.com/search?q=C%2B%2B%20vs%20Rust%3F')
  })

  it('routes a single word containing a colon but no dot to a search (e.g. a scheme-less "javascript:" paste)', () => {
    expect(resolveOmniboxInput('javascript:alert(1)')).toBe(
      'https://www.google.com/search?q=javascript%3Aalert(1)',
    )
  })
})
