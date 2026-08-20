// sanitizeChildText.test.ts — FE-7 / MAJ-12 untrusted-child-text sanitization.
//
// MAJ-12 acceptance: child-originated text renders as plain text / sanctioned
// markdown, NO raw HTML, links non-clickable, untrusted-origin chrome visible.
// This spec covers the pure-utility layer (DOMPurify pre-clean); the React
// render guarantees (inert links, badge) are covered in
// UntrustedChildText.test.tsx.

import { describe, it, expect } from 'vitest'
import {
  sanitizeChildText,
  stripUnsafeHtml,
  stripControlChars,
  isPlainText,
  SANCTIONED_MARKDOWN_TAGS,
} from './sanitizeChildText'

describe('sanitizeChildText — MAJ-12 raw-HTML stripping', () => {
  it('strips <script> blocks entirely (no content leakage)', () => {
    const out = sanitizeChildText('hello <script>alert("xss")</script> world')
    expect(out).not.toContain('<script>')
    expect(out).not.toContain('alert')
    expect(out).toContain('hello')
    expect(out).toContain('world')
  })

  it('strips event-handler attributes (onerror/onload) and the carrying tag', () => {
    const out = sanitizeChildText('<img src=x onerror=alert(1)>')
    // <img> is NOT in the sanctioned set → dropped entirely (no attribute survives).
    expect(out).not.toContain('<img')
    expect(out).not.toContain('onerror')
    expect(out).not.toContain('alert')
  })

  it('strips ALL attributes from surviving tags (style/href/src/class)', () => {
    const out = stripUnsafeHtml('<p style="color:red" class="x" onclick="alert(1)">keep me</p>')
    expect(out).toContain('<p>keep me</p>')
    expect(out).not.toContain('style')
    expect(out).not.toContain('onclick')
    expect(out).not.toContain('class')
  })

  it('keeps the inner text of a dropped <a javascript:> link (content survives, anchor does not)', () => {
    const out = sanitizeChildText('<a href="javascript:alert(1)">click here</a>')
    expect(out).not.toContain('<a')
    expect(out).not.toContain('javascript')
    expect(out).not.toContain('href')
    expect(out).toContain('click here')
  })

  it('strips iframe / object / embed / form / input entirely', () => {
    const out = sanitizeChildText('<iframe src="https://evil"></iframe><form><input name=x></form>')
    expect(out).not.toContain('<iframe')
    expect(out).not.toContain('<form')
    expect(out).not.toContain('<input')
    expect(out).not.toContain('evil')
  })
})

describe('sanitizeChildText — sanctioned markdown survives', () => {
  it('preserves bold/italic/code markdown (plain text to DOMPurify)', () => {
    const out = sanitizeChildText('**bold** and *italic* and `code`')
    expect(out).toContain('**bold**')
    expect(out).toContain('*italic*')
    expect(out).toContain('`code`')
  })

  it('preserves list markdown', () => {
    const out = sanitizeChildText('- one\n- two\n- three')
    expect(out).toContain('- one')
    expect(out).toContain('- two')
    expect(out).toContain('- three')
  })

  it('preserves sanctioned block tags that ARE in the allow-list', () => {
    // <pre><code> are sanctioned — they survive, attributes stripped.
    const out = stripUnsafeHtml('<pre><code>const x = 1</code></pre>')
    expect(out).toContain('<pre>')
    expect(out).toContain('<code>')
    expect(out).toContain('const x = 1')
  })

  it('the sanctioned tag allow-list excludes a/img/iframe/script/table', () => {
    const excluded = ['a', 'img', 'iframe', 'script', 'style', 'table', 'td', 'form', 'input']
    for (const tag of excluded) {
      expect(SANCTIONED_MARKDOWN_TAGS).not.toContain(tag)
    }
  })
})

describe('sanitizeChildText — control-char / null-byte hardening', () => {
  it('strips null bytes used to smuggle tag-like payloads', () => {
    // A smuggled "<scr\x00ipt>" must not survive as a tag-like token.
    const nullByte = String.fromCharCode(0)
    const out = stripControlChars(`foo<scr${nullByte}ipt>bar`)
    expect(out).not.toContain(nullByte)
    expect(out).not.toContain(`<scr${nullByte}ipt>`)
  })

  it('strips C0 control chars but preserves tab/newline/carriage-return', () => {
    const out = stripControlChars('a\x01\x02b\tc\nd\re')
    expect(out).toBe('ab\tc\nd\re')
  })

  it('sanitizeChildText collapses excessive blank lines but keeps structure', () => {
    const out = sanitizeChildText('line one\n\n\n\n\nline two')
    expect(out).toBe('line one\n\nline two')
  })
})

describe('sanitizeChildText — defensive coercion', () => {
  it('returns empty string for null/undefined/non-string input', () => {
    expect(sanitizeChildText(null as unknown as string)).toBe('')
    expect(sanitizeChildText(undefined as unknown as string)).toBe('')
    expect(sanitizeChildText({} as unknown as string)).toBe('')
  })

  it('returns empty string for empty input', () => {
    expect(sanitizeChildText('')).toBe('')
  })

  it('trims leading/trailing whitespace', () => {
    expect(sanitizeChildText('  hello  ')).toBe('hello')
  })
})

describe('isPlainText', () => {
  it('true for plain prose with a bare less-than (values < 5)', () => {
    expect(isPlainText('values < 5 and > 2')).toBe(true)
  })

  it('false when a tag-like token is present', () => {
    expect(isPlainText('hi <b>there</b>')).toBe(false)
    expect(isPlainText('<script>')).toBe(false)
    expect(isPlainText('see </a>')).toBe(false)
  })
})
