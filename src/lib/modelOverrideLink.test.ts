/**
 * modelOverrideLink.test.ts — O10 regression coverage.
 *
 * The app mounts on `createHashHistory()` (src/main.tsx — required for
 * go:embed static serving; history mode 404s on deep links). A link built as
 * a plain `/settings?...` path has no `#`, so following it is a REAL
 * navigation under a hash router: full page reload, fresh SPA boot, landing
 * wherever the boot's default route is — not the model-override screen the
 * link promises. This is O10.
 *
 * These tests exercise the real `modelOverrideHref` export, not a
 * hand-rolled reimplementation, so a regression in the actual helper is what
 * fails them.
 */
import { describe, it, expect } from 'vitest'
import { modelOverrideHref } from './modelOverrideLink'

describe('modelOverrideHref (O10)', () => {
  it('is hash-prefixed so it stays an in-page navigation under the hash router', () => {
    const href = modelOverrideHref('ollama', 'llama3.3:70b')
    expect(href).toBe('/#/settings?tab=models&provider=ollama&model=llama3.3%3A70b')
  })

  it('always starts with "/#/" regardless of the pair', () => {
    expect(modelOverrideHref('openai', 'gpt-5')).toMatch(/^\/#\//)
    expect(modelOverrideHref('anthropic', 'claude-sonnet-4-5')).toMatch(/^\/#\//)
  })

  it('still carries the tab=models + provider/model search params the settings route reads', () => {
    const href = modelOverrideHref('openai', 'A')
    // Strip the hash prefix before parsing as a query string.
    const query = href.replace(/^\/#\/settings\?/, '')
    const params = new URLSearchParams(query)
    expect(params.get('tab')).toBe('models')
    expect(params.get('provider')).toBe('openai')
    expect(params.get('model')).toBe('A')
  })
})
