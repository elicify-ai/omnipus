// @vitest-environment node
/**
 * mint-provider.test.ts — the scripted model edge of the preview-isolation
 * shard (mint-provider.ts), driven over real HTTP with the request shapes the
 * gateway sends. Not browser acceptance: no gateway, no agent turn.
 *
 * Pins the rounds a mint takes (write every file [+ load serve_web], then
 * serve_web, then a final text) and the regression the first local run found:
 * the gateway appends its own user-role context (the lazy "More tools" list)
 * AFTER the chat message, so the directive is the latest user message that
 * carries one, not the last user message.
 */

import { afterEach, describe, expect, it } from 'vitest'
import { mintDirective, startMintProvider } from './mint-provider'
import { staticBundleFiles } from './webserve-bundle'

type Provider = Awaited<ReturnType<typeof startMintProvider>>
let provider: Provider | undefined

afterEach(async () => {
  await provider?.close()
  provider = undefined
})

interface StreamedCall {
  name: string
  args: Record<string, unknown>
}

/** POSTs one streamed chat completion and returns the tool calls (or text) it streamed. */
async function round(
  p: Provider,
  messages: unknown[],
  tools: string[],
): Promise<{ calls: StreamedCall[]; text: string }> {
  const res = await fetch(`${p.url}/v1/chat/completions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ stream: true, messages, tools: tools.map((name) => ({ type: 'function', function: { name } })) }),
  })
  expect(res.status).toBe(200)
  const calls: StreamedCall[] = []
  let text = ''
  for (const line of (await res.text()).split('\n')) {
    if (!line.startsWith('data: ') || line === 'data: [DONE]') continue
    const delta = JSON.parse(line.slice(6)).choices[0].delta as {
      content?: string
      tool_calls?: Array<{ function: { name: string; arguments: string } }>
    }
    text += delta.content ?? ''
    for (const c of delta.tool_calls ?? []) calls.push({ name: c.function.name, args: JSON.parse(c.function.arguments) })
  }
  return { calls, text }
}

describe('startMintProvider', () => {
  const dir = 'e2e-unit-1'
  const files = staticBundleFiles(dir)
  const chat = { role: 'user', content: [{ type: 'text', text: mintDirective(dir, files, false) }] }
  const moreTools = { role: 'user', content: '# More tools (load before use)\n- serve_web' }
  const offered = ['write_file', 'ToolSearch']

  it('finds the directive behind a trailing gateway user message and writes every file, loading serve_web', async () => {
    provider = await startMintProvider()
    const r1 = await round(provider, [{ role: 'system', content: 's' }, chat, moreTools], offered)
    expect(r1.calls).toEqual([
      ...Object.entries(files).map(([name, content]) => ({ name: 'write_file', args: { path: `${dir}/${name}`, content } })),
      { name: 'ToolSearch', args: { names: ['serve_web'] } },
    ])
    expect(provider.failures()).toEqual([])
  })

  it('calls serve_web after the writes, then ends with text', async () => {
    provider = await startMintProvider()
    const wrote = { role: 'assistant', tool_calls: [{ function: { name: 'write_file' } }] }
    const r2 = await round(provider, [chat, moreTools, wrote], [...offered, 'serve_web'])
    expect(r2.calls).toEqual([{ name: 'serve_web', args: { path: dir, duration_seconds: 1800 } }])

    const served = { role: 'assistant', tool_calls: [{ function: { name: 'serve_web' } }] }
    const r3 = await round(provider, [chat, moreTools, wrote, served], [...offered, 'serve_web'])
    expect(r3.calls).toEqual([])
    expect(r3.text).toBe('Served.')
    expect(provider.failures()).toEqual([])
  })

  it('dev mints call serve_web with the dev command', async () => {
    provider = await startMintProvider()
    const devChat = { role: 'user', content: mintDirective(dir, files, true) }
    const wrote = { role: 'assistant', tool_calls: [{ function: { name: 'write_file' } }] }
    const r = await round(provider, [devChat, wrote], [...offered, 'serve_web'])
    expect(r.calls).toEqual([{ name: 'serve_web', args: { path: dir, command: 'npm run dev' } }])
  })

  it('records a failure when write_file is not offered', async () => {
    provider = await startMintProvider()
    await round(provider, [chat], ['ToolSearch'])
    expect(provider.failures().join('\n')).toContain('write_file is not offered')
  })
})
