// @vitest-environment node
// Q66 A / #1725: test the fixture, not the product's deferred reload-drain fix.
// The real save helper and atomic config writes run; only gateway calls and
// the clock are simulated. No browser, gateway process or live model is used.
import * as fs from 'node:fs'
import * as os from 'node:os'
import * as path from 'node:path'
import type { Page } from '@playwright/test'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { saveStubCliProgram } from './stub-external-cli'

let home: string
const originalConfig = { gateway: { dev_mode_bypass: true }, untouched: 'keep this key' }

function configPath(): string {
  return path.join(home, 'config.json')
}

function readBypass(): boolean {
  return (JSON.parse(fs.readFileSync(configPath(), 'utf8')) as typeof originalConfig)
    .gateway.dev_mode_bypass
}

function expectRestoredConfig(): void {
  expect(JSON.parse(fs.readFileSync(configPath(), 'utf8'))).toEqual(originalConfig)
}

beforeEach(() => {
  home = fs.mkdtempSync(path.join(os.tmpdir(), 'omnipus-stub-cli-unit-'))
  vi.stubEnv('OMNIPUS_HOME', home)
  // The stub executable is also scratch data and is removed with this home.
  vi.stubEnv('TMPDIR', home)
  fs.writeFileSync(configPath(), JSON.stringify(originalConfig))
  vi.useFakeTimers({ toFake: ['Date', 'setTimeout', 'clearTimeout'] })
  vi.setSystemTime(new Date('2026-11-09T00:00:00Z'))
})

afterEach(() => {
  vi.useRealTimers()
  vi.restoreAllMocks()
  vi.unstubAllEnvs()
  fs.rmSync(home, { recursive: true, force: true })
})

function response(status: number, body: unknown) {
  return {
    ok: () => status >= 200 && status < 300,
    status: () => status,
    text: async () => typeof body === 'string' ? body : JSON.stringify(body),
    json: async () => body,
  }
}

function gateway(options: {
  offDelayMs?: number
  holdOff?: boolean
  failedReloads?: number[]
  putStatus?: number
} = {}) {
  const events: string[] = []
  let target = true
  let readyAt = 0
  let notifyOffProbe: (() => void) | undefined
  const firstOffProbe = new Promise<void>((resolve) => { notifyOffProbe = resolve })
  const post = vi.fn(async (url: string) => {
    expect(new URL(url).pathname).toBe('/reload')
    const value = readBypass()
    events.push(`reload:${value}`)
    const ordinal = post.mock.calls.length
    if (options.failedReloads?.includes(ordinal)) {
      return response(503, `reload ${ordinal} refused`)
    }
    target = value
    readyAt = Date.now() + (value ? 0 : options.offDelayMs ?? 0)
    return response(200, 'queued')
  })
  const get = vi.fn(async (url: string) => {
    expect(new URL(url).pathname).toBe('/api/v1/config')
    const seen = target || options.holdOff === true || Date.now() < readyAt
    events.push(`probe:${seen}`)
    if (!target) notifyOffProbe?.()
    // The real wire omits false; exercise that form on every successful off probe.
    return response(200, { gateway: seen ? { dev_mode_bypass: true } : {} })
  })
  const put = vi.fn(async (url: string, request: { data: string }) => {
    expect(new URL(url).pathname).toBe('/api/v1/settings/third-party-agents')
    events.push(`put:${readBypass()}`)
    if (options.putStatus) return response(options.putStatus, 'Settings refused')
    // Fake Settings storage echoes the submitted view, just like the gateway.
    return response(200, JSON.parse(request.data) as unknown)
  })
  const page = {
    request: { post, get, fetch: put },
    context: () => ({ cookies: async () => [{ name: 'csrf', value: 'unit-csrf' }] }),
  } as unknown as Page
  return { page, events, post, get, put, firstOffProbe }
}

function attempt(page: Page) {
  let settled = false
  // Capture immediately so deliberately advancing a failing poll cannot create
  // an unhandled rejection; the tests still assert the exact failure below.
  const result = saveStubCliProgram(page).then(
    () => { settled = true; return null },
    (error: unknown) => { settled = true; return error },
  )
  return { result, isSettled: () => settled }
}

const completeAttempt = ['reload:false', 'probe:false', 'put:false', 'reload:true', 'probe:true']

// All expected times and restore behavior come from the Q66 A brief, not from
// the helper's implementation or its previous output.
describe('saveStubCliProgram reload budget and restoration (Q66 A)', () => {
  it('waits past 20 seconds for a value live at 30 seconds, then saves and restores', async () => {
    const gw = gateway({ offDelayMs: 30_000 })
    const saving = attempt(gw.page)
    await gw.firstOffProbe
    await vi.advanceTimersByTimeAsync(20_000)
    expect(saving.isSettled()).toBe(false)
    expect(gw.put).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(10_000)
    expect(await saving.result).toBeNull()
    expect(gw.events.filter((event) => !event.startsWith('probe:')))
      .toEqual(['reload:false', 'put:false', 'reload:true'])
    expect(gw.events.at(-1)).toBe('probe:true')
    expect(gw.put).toHaveBeenCalledTimes(1)
    expectRestoredConfig()
  })

  it('expires at 90 seconds with an honest message, restores, and makes the next attempt wait again', async () => {
    const options = { holdOff: true }
    const gw = gateway(options)
    const saving = attempt(gw.page)
    await gw.firstOffProbe
    await vi.advanceTimersByTimeAsync(89_999)
    expect.soft(saving.isSettled()).toBe(false)
    expect(gw.put).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    const error = await saving.result
    expect(error).toBeInstanceOf(Error)
    expect.soft((error as Error).message).toBe(
      'saveStubCliProgram: dev_mode_bypass=false was not live within 90 s ' +
      '(last observation: true); refusing to continue on a gateway in an unknown auth mode.',
    )
    expect.soft(JSON.parse(fs.readFileSync(configPath(), 'utf8'))).toEqual(originalConfig)
    expect.soft(gw.events.slice(-2)).toEqual(['reload:true', 'probe:true'])

    // This is an explicit second call, not an automatic retry in the helper.
    // A leaked false flag would take "already off" and omit both off probes.
    options.holdOff = false
    const beforeRetry = gw.events.length
    expect(await attempt(gw.page).result).toBeNull()
    expect(gw.events.slice(beforeRetry)).toEqual(completeAttempt)
    expectRestoredConfig()
  })

  it.each(['flip', 'reload'] as const)('restores after a failed %s and repeats setup on the next call', async (stage) => {
    const fault = new Error('flip rename denied')
    const gw = gateway({ failedReloads: stage === 'reload' ? [1] : [] })
    if (stage === 'flip') vi.spyOn(fs.promises, 'rename').mockRejectedValueOnce(fault)
    const error = await attempt(gw.page).result
    if (stage === 'flip') {
      expect(error).toBe(fault)
    } else {
      expect(error).toBeInstanceOf(Error)
      expect((error as Error).message).toBe('saveStubCliProgram: POST /reload failed 503: reload 1 refused')
    }
    expect(gw.put).not.toHaveBeenCalled()
    expectRestoredConfig()
    expect(gw.events).toEqual(stage === 'flip'
      ? ['reload:true', 'probe:true']
      : ['reload:false', 'reload:true', 'probe:true'])

    const beforeRetry = gw.events.length
    expect(await attempt(gw.page).result).toBeNull()
    expect(gw.events.slice(beforeRetry)).toEqual(completeAttempt)
    expectRestoredConfig()
  })

  it('keeps the existing restoration and exact failure when the Settings body fails', async () => {
    const gw = gateway({ putStatus: 500 })
    const error = await attempt(gw.page).result
    expect(error).toBeInstanceOf(Error)
    expect((error as Error).message).toBe(
      'saveStubCliProgram: PUT /api/v1/settings/third-party-agents failed 500: Settings refused',
    )
    expect(gw.events).toEqual(completeAttempt)
    expectRestoredConfig()
  })

  it('reports both a setup failure and a restoration failure instead of masking either', async () => {
    const gw = gateway({ failedReloads: [1, 2] })
    const error = await attempt(gw.page).result
    expect(error).toBeInstanceOf(Error)
    expect((error as Error).message).toBe(
      'saveStubCliProgram: POST /reload failed 503: reload 1 refused ' +
      '(AND restoring dev_mode_bypass afterwards also failed: Error: ' +
      'saveStubCliProgram: POST /reload failed 503: reload 2 refused)',
    )
    expect((error as Error).cause).toBeInstanceOf(Error)
    expect(((error as Error).cause as Error).message)
      .toBe('saveStubCliProgram: POST /reload failed 503: reload 2 refused')
    expect(gw.events).toEqual(['reload:false', 'reload:true'])
    expect(gw.put).not.toHaveBeenCalled()
    expectRestoredConfig()
  })

  it('leaves an initially off flag alone, with no reload or poll', async () => {
    const offConfig = { ...originalConfig, gateway: { dev_mode_bypass: false } }
    fs.writeFileSync(configPath(), JSON.stringify(offConfig))
    const gw = gateway()
    expect(await attempt(gw.page).result).toBeNull()
    expect(gw.events).toEqual(['put:false'])
    expect(gw.post).not.toHaveBeenCalled()
    expect(gw.get).not.toHaveBeenCalled()
    expect(JSON.parse(fs.readFileSync(configPath(), 'utf8'))).toEqual(offConfig)
  })
})
