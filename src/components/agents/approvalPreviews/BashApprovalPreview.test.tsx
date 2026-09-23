// BashApprovalPreview.test.tsx — highlightBinaryInText unit coverage.
//
// pkg/tools' D3/D4 matcher resolves a segment's binary through PATH to an
// absolute path (CommandSegmentInfo.resolved_binary, e.g. "/bin/rm") while
// CommandSegmentInfo.command_text is the raw text the agent typed (e.g.
// "rm -rf x"). A plain commandText.indexOf(resolved_binary) essentially
// never matches, which is why ToolApprovalModal's per-segment display never
// highlighted anything in practice — see ToolApprovalModal.scopeAndSegments.
// test.tsx's integration-level regression test for the rendered result;
// these are the function-level cases behind it.

import { describe, it, expect } from 'vitest'
import { highlightBinaryInText } from './BashApprovalPreview'

describe('highlightBinaryInText', () => {
  it('matches a bare binary name that appears verbatim (pre-existing case)', () => {
    const result = highlightBinaryInText('npm run build', 'npm')
    expect(result).toEqual({ before: '', binary: 'npm', after: ' run build' })
  })

  it('falls back to the basename when resolved_binary is a resolved absolute path', () => {
    const result = highlightBinaryInText('rm -rf /tmp/scratch', '/bin/rm')
    expect(result.binary).toBe('rm')
    expect(result.before).toBe('')
    expect(result.after).toBe(' -rf /tmp/scratch')
  })

  it('matches the full resolved path when it DOES appear literally in the typed text', () => {
    const result = highlightBinaryInText('/usr/bin/curl https://example.com', '/usr/bin/curl')
    expect(result.binary).toBe('/usr/bin/curl')
    expect(result.after).toBe(' https://example.com')
  })

  it('does not false-positive the basename inside an unrelated longer word', () => {
    // "rm" must not light up inside "confirm" or "arm64" — only a
    // standalone "rm" token counts.
    const result = highlightBinaryInText('echo confirm && uname -m # arm64', '/bin/rm')
    expect(result).toEqual({ before: '', binary: '', after: 'echo confirm && uname -m # arm64' })
  })

  it('finds a bounded basename occurrence that is not the first substring match', () => {
    // The literal substring "rm" first appears inside "confirm"; the real
    // standalone "rm" token is later in the string and must be the one used.
    const result = highlightBinaryInText('echo confirm; rm -rf /tmp/x', '/bin/rm')
    expect(result.binary).toBe('rm')
    expect(result.before).toBe('echo confirm; ')
    expect(result.after).toBe(' -rf /tmp/x')
  })

  it('returns no highlight when neither the resolved path nor its basename appear', () => {
    const result = highlightBinaryInText('git push origin main', '/bin/rm')
    expect(result).toEqual({ before: '', binary: '', after: 'git push origin main' })
  })

  it('returns no highlight when binary is undefined', () => {
    const result = highlightBinaryInText('git push origin main', undefined)
    expect(result).toEqual({ before: '', binary: '', after: 'git push origin main' })
  })
})
