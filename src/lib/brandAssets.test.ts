import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { afterEach, describe, expect, it, vi } from 'vitest'

// Bundle-only change: reuse the already-shipped favicon without changing the
// artwork or losing the deployment base prefix that imported asset URLs had.
afterEach(() => {
  vi.unstubAllEnvs()
  vi.resetModules()
})

describe('shared Omnipus mark asset', () => {
  it.each([
    ['/', '/favicon.svg'],
    ['/omnipus/', '/omnipus/favicon.svg'],
    ['./', './favicon.svg'],
  ])('keeps the deployment base %s in the image URL', async (base, expected) => {
    vi.stubEnv('BASE_URL', base)
    vi.resetModules()
    const { OMNIPUS_MARK_URL } = await import('./brandAssets')
    expect(OMNIPUS_MARK_URL).toBe(expected)
  })

  it('serves byte-identical logo and avatar artwork from the existing public asset', () => {
    const favicon = readFileSync(resolve('public/favicon.svg'))
    const logo = readFileSync(resolve('src/assets/logo/omnipus-logo.svg'))
    const avatar = readFileSync(resolve('src/assets/logo/omnipus-avatar.svg'))
    expect(favicon).toEqual(logo)
    expect(favicon).toEqual(avatar)
  })
})
