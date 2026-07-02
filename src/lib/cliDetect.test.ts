// cliDetect.ts — shared detect→prefill helpers for the external-executor-
// cli-path-detection spec (ADR-030). Covers the WizardCli↔CliDetect key
// mapping (US-1/US-2) and the detection hint used before a validate() result
// exists (US-1 AC-1/AC-3).

import { describe, it, expect } from 'vitest'
import { CLI_TO_DETECT_KEY, detectEntryFor, resolveCliDetectHint } from './cliDetect'
import type { CliDetect } from './api'

const DETECT: CliDetect = {
  claude: { installed: true, path: '/usr/local/bin/claude', source: 'path' },
  codex: { installed: true, path: '/home/dev/.local/bin/codex', source: 'well-known' },
  opencode: { installed: false, path: null, source: null },
}

describe('CLI_TO_DETECT_KEY — WizardCli "claude-code" maps to the wire key "claude"', () => {
  it('maps claude-code -> claude, codex -> codex, opencode -> opencode', () => {
    expect(CLI_TO_DETECT_KEY['claude-code']).toBe('claude')
    expect(CLI_TO_DETECT_KEY.codex).toBe('codex')
    expect(CLI_TO_DETECT_KEY.opencode).toBe('opencode')
  })
})

describe('detectEntryFor', () => {
  it('returns the entry for the mapped key', () => {
    expect(detectEntryFor(DETECT, 'claude-code')).toEqual(DETECT.claude)
    expect(detectEntryFor(DETECT, 'codex')).toEqual(DETECT.codex)
  })

  it('returns undefined when detect has not resolved yet', () => {
    expect(detectEntryFor(null, 'claude-code')).toBeUndefined()
    expect(detectEntryFor(undefined, 'claude-code')).toBeUndefined()
  })

  it('returns undefined when no CLI is selected', () => {
    expect(detectEntryFor(DETECT, undefined)).toBeUndefined()
  })
})

describe('resolveCliDetectHint (US-1 AC-1/AC-3)', () => {
  it('"unknown" before detect resolves or before a CLI is selected', () => {
    expect(resolveCliDetectHint(null, 'claude-code')).toEqual({ kind: 'unknown' })
    expect(resolveCliDetectHint(DETECT, undefined)).toEqual({ kind: 'unknown' })
  })

  it('"detected" with source "path" for a $PATH hit (US-1 AC-1)', () => {
    expect(resolveCliDetectHint(DETECT, 'claude-code')).toEqual({ kind: 'detected', source: 'path' })
  })

  it('"detected" with source "well-known" for a well-known-dir hit (US-2 AC-1)', () => {
    expect(resolveCliDetectHint(DETECT, 'codex')).toEqual({ kind: 'detected', source: 'well-known' })
  })

  it('"not-found" when installed=false (US-1 AC-3)', () => {
    expect(resolveCliDetectHint(DETECT, 'opencode')).toEqual({ kind: 'not-found' })
  })

  it('F-17 stale-bundle safety: an unrecognized source value falls back to "path" rather than crashing', () => {
    const staleDetect = {
      ...DETECT,
      claude: { installed: true, path: '/usr/local/bin/claude', source: 'some-future-source' as unknown as 'path' },
    }
    expect(resolveCliDetectHint(staleDetect, 'claude-code')).toEqual({ kind: 'detected', source: 'path' })
  })
})
