// onboard-via-api.test.ts — design.md §1 row 3 and §7 R9.
// A local fake gateway. No real keys and no network beyond 127.0.0.1.
// The request order and bodies are the contract in api-contract.md §6.

import http from 'node:http'
import type { AddressInfo } from 'node:net'
import { afterEach, describe, expect, it } from 'vitest'
import { onboardViaAPI } from './onboard-via-api.js'

type Hit = { method: string; path: string; body: unknown }

const KEYS = ['MINIMAX_API_KEY_CI', 'MINIMAX_API_KEY', 'OPENROUTER_API_KEY_CI', 'OPENROUTER_API_KEY'] as const

function scrubKeys(): void {
  for (const name of KEYS) delete process.env[name]
}

afterEach(() => {
  scrubKeys()
})

function startGateway(): Promise<{ url: string; hits: Hit[]; close: () => Promise<void> }> {
  const hits: Hit[] = []
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (c) => chunks.push(c as Buffer))
    req.on('end', () => {
      const raw = Buffer.concat(chunks).toString('utf8')
      let body: unknown = null
      if (raw !== '') body = JSON.parse(raw) as unknown
      hits.push({ method: req.method ?? '', path: req.url ?? '', body })
      res.setHeader('content-type', 'application/json')
      if (req.method === 'GET' && req.url === '/api/v1/agents') {
        res.end(JSON.stringify([{ id: 'mia' }, { id: 'jim' }]))
        return
      }
      res.end(JSON.stringify({ token: 'eval-token' }))
    })
  })
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address() as AddressInfo
      resolve({
        url: `http://127.0.0.1:${addr.port}`,
        hits,
        close: () => new Promise((done) => server.close(() => done())),
      })
    })
  })
}

describe('onboardViaAPI seeds the fallback (design.md §7 R9)', () => {
  it('onboards the primary with its key, then puts the fallback provider and each agent', async () => {
    scrubKeys()
    process.env.MINIMAX_API_KEY_CI = 'test-key-aaaa'
    process.env.OPENROUTER_API_KEY_CI = 'test-key-bbbb'
    const gw = await startGateway()
    try {
      await onboardViaAPI({ baseURL: gw.url })
      expect(gw.hits.map((h) => `${h.method} ${h.path}`)).toEqual([
        'POST /api/v1/onboarding/complete',
        'PUT /api/v1/providers/openrouter',
        'GET /api/v1/agents',
        'PUT /api/v1/agents/mia',
        'PUT /api/v1/agents/jim',
      ])
      expect(gw.hits[0].body).toMatchObject({
        provider: {
          auth_method: 'api_key',
          id: 'minimax-coding-plan',
          api_key: 'test-key-aaaa',
          model: 'MiniMax-M3.1-Flash-Preview',
        },
      })
      expect(gw.hits[1].body).toEqual({
        auth_method: 'api_key',
        api_key: 'test-key-bbbb',
        model: 'deepseek/deepseek-v4.1-flash',
      })
      const fallbackBody = {
        fallback_models: [{ provider: 'openrouter', model: 'deepseek/deepseek-v4.1-flash' }],
      }
      expect(gw.hits[3].body).toEqual(fallbackBody)
      expect(gw.hits[4].body).toEqual(fallbackBody)
    } finally {
      await gw.close()
    }
  })

  it('uses MINIMAX_API_KEY when MINIMAX_API_KEY_CI is unset', async () => {
    scrubKeys()
    process.env.MINIMAX_API_KEY = 'test-key-aaaa'
    process.env.OPENROUTER_API_KEY_CI = 'test-key-bbbb'
    const gw = await startGateway()
    try {
      await onboardViaAPI({ baseURL: gw.url })
      expect(gw.hits[0].body).toMatchObject({
        provider: { id: 'minimax-coding-plan', api_key: 'test-key-aaaa', model: 'MiniMax-M3.1-Flash-Preview' },
      })
    } finally {
      await gw.close()
    }
  })

  it('fails fast naming both MiniMax env vars and sends nothing when the primary key is missing', async () => {
    scrubKeys()
    process.env.OPENROUTER_API_KEY_CI = 'test-key-bbbb'
    const gw = await startGateway()
    try {
      await expect(onboardViaAPI({ baseURL: gw.url })).rejects.toThrow(/MINIMAX_API_KEY_CI[\s\S]*MINIMAX_API_KEY|MINIMAX_API_KEY[\s\S]*MINIMAX_API_KEY_CI/)
      expect(gw.hits).toEqual([])
    } finally {
      await gw.close()
    }
  })

  it('does not use an OpenRouter key for a MiniMax primary when both MiniMax vars are unset', async () => {
    scrubKeys()
    const gw = await startGateway()
    try {
      await expect(onboardViaAPI({ baseURL: gw.url })).rejects.toThrow(/MINIMAX_API_KEY_CI/)
      expect(gw.hits).toEqual([])
    } finally {
      await gw.close()
    }
  })
})
