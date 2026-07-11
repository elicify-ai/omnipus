import { describe, it, expect } from 'vitest'
import { normalizeNavigateUrl } from './browserLiveUrl'

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
