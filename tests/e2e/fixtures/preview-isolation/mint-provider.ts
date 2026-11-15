import http from 'node:http'
import { E2E_FALLBACK } from '../e2e-model'
import type { BundleFiles } from './webserve-bundle'

/**
 * Scripted model for the preview-isolation shard. Only the external model HTTP
 * edge is scripted; the gateway, the agent loop, the file tool, serve_web, the
 * registry and the preview handlers are all real. A mint is one chat message
 * carrying a directive, and this provider answers it in rounds: first a
 * write_file call per file (plus ToolSearch for serve_web when it is not yet
 * offered), then serve_web, then a final text. It advances to serve_web once
 * the history after the directive holds an assistant write_file call; it does
 * NOT check that the writes succeeded -- a failed write surfaces as a failed
 * serve_web or a missing file in the row itself.
 */

const DIRECTIVE_PREFIX = 'E2E-MINT-DIRECTIVE '

// not-wire-format: a fixture message the spec types into the chat box.
interface MintDirective {
  dir: string
  files: BundleFiles
  dev: boolean
}

/** The chat message that makes the scripted provider mint `dirName`. */
export function mintDirective(dirName: string, files: BundleFiles, dev: boolean): string {
  const directive: MintDirective = { dir: dirName, files, dev }
  return DIRECTIVE_PREFIX + Buffer.from(JSON.stringify(directive), 'utf8').toString('base64')
}

type ChatMessage = {
  role?: string
  content?: unknown
  tool_calls?: Array<{ function?: { name?: string } }>
}

function textOf(content: unknown): string {
  if (typeof content === 'string') return content
  if (Array.isArray(content)) {
    return content
      .map((part) => (part && typeof part === 'object' ? String((part as { text?: unknown }).text ?? '') : ''))
      .join('')
  }
  return ''
}

function directiveOf(message: ChatMessage): MintDirective | undefined {
  const text = textOf(message.content)
  const at = text.indexOf(DIRECTIVE_PREFIX)
  if (at < 0) return undefined
  const encoded = text.slice(at + DIRECTIVE_PREFIX.length).trim().split(/\s+/)[0]
  return JSON.parse(Buffer.from(encoded, 'base64').toString('utf8')) as MintDirective
}

function sse(delta: Record<string, unknown>, finishReason: string | null): string {
  return (
    'data: ' +
    JSON.stringify({
      id: 'preview-mint-stream',
      object: 'chat.completion.chunk',
      model: E2E_FALLBACK.model,
      created: Math.floor(Date.now() / 1000),
      choices: [{ index: 0, delta, finish_reason: finishReason }],
    }) +
    '\n\n'
  )
}

function toolCalls(calls: Array<{ id: string; name: string; args: Record<string, unknown> }>): string {
  return (
    sse(
      {
        role: 'assistant',
        tool_calls: calls.map((call, index) => ({
          index,
          id: call.id,
          type: 'function',
          function: { name: call.name, arguments: JSON.stringify(call.args) },
        })),
      },
      null,
    ) +
    sse({}, 'tool_calls') +
    'data: [DONE]\n\n'
  )
}

export async function startMintProvider() {
  const failures: string[] = []
  let calls = 0

  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (part: Buffer) => chunks.push(part))
    req.on('error', (err) => failures.push(`mint provider request: ${err.message}`))
    req.on('end', () => {
      if (req.method === 'GET' && req.url?.includes('/models')) {
        res.writeHead(200, { 'Content-Type': 'application/json' })
        res.end(JSON.stringify({ object: 'list', data: [{ id: E2E_FALLBACK.model, object: 'model' }] }))
        return
      }
      if (req.method !== 'POST' || !req.url?.includes('/chat/completions')) {
        failures.push(`unexpected mint provider route: ${req.method} ${req.url}`)
        res.writeHead(404, { 'Content-Type': 'application/json' })
        res.end(JSON.stringify({ error: { message: failures.at(-1) } }))
        return
      }
      let body: { stream?: boolean; messages?: ChatMessage[]; tools?: Array<{ function?: { name?: string } }> }
      try {
        // External OpenAI-compatible protocol, not an Omnipus wire type.
        body = JSON.parse(Buffer.concat(chunks).toString('utf8'))
      } catch (err) {
        failures.push(`invalid mint provider JSON: ${String(err)}`)
        res.writeHead(400, { 'Content-Type': 'application/json' })
        res.end(JSON.stringify({ error: { message: failures.at(-1) } }))
        return
      }
      if (body.stream !== true) {
        // Onboarding / key validation is not a chat turn.
        res.writeHead(200, { 'Content-Type': 'application/json' })
        res.end(
          JSON.stringify({
            id: 'preview-mint-probe',
            object: 'chat.completion',
            model: E2E_FALLBACK.model,
            choices: [{ index: 0, message: { role: 'assistant', content: 'ready' }, finish_reason: 'stop' }],
            usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 },
          }),
        )
        return
      }
      calls += 1
      const messages = body.messages ?? []
      // The directive is the latest user message that carries one -- not
      // simply the last user message: the gateway appends its own user-role
      // context after the chat message (e.g. the lazy "More tools" list).
      let directiveAt = -1
      messages.forEach((m, i) => {
        if (m.role === 'user' && textOf(m.content).includes(DIRECTIVE_PREFIX)) directiveAt = i
      })
      let directive: MintDirective | undefined
      try {
        directive = directiveAt >= 0 ? directiveOf(messages[directiveAt]) : undefined
      } catch (err) {
        failures.push(`unreadable mint directive: ${String(err)}`)
        res.writeHead(400, { 'Content-Type': 'application/json' })
        res.end(JSON.stringify({ error: { message: failures.at(-1) } }))
        return
      }
      res.writeHead(200, { 'Content-Type': 'text/event-stream' })
      if (!directive) {
        res.end(sse({ role: 'assistant', content: 'ok' }, null) + sse({}, 'stop') + 'data: [DONE]\n\n')
        return
      }
      const offered = new Set((body.tools ?? []).map((t) => t.function?.name ?? ''))
      const sinceDirective = messages.slice(directiveAt + 1)
      const called = new Set(
        sinceDirective.flatMap((m) => (m.tool_calls ?? []).map((c) => c.function?.name ?? '')),
      )
      if (!called.has('write_file')) {
        if (!offered.has('write_file')) {
          failures.push(`write_file is not offered to the agent; offered: ${[...offered].join(',')}`)
        }
        const writes: Array<{ id: string; name: string; args: Record<string, unknown> }> = Object.entries(directive.files).map(([name, content], i) => ({
          id: `mint-write-${i}`,
          name: 'write_file',
          args: { path: `${directive.dir}/${name}`, content },
        }))
        // serve_web is a lazy-tier tool: load it in the same round when the
        // gateway has not offered it yet (ToolSearch is infrastructure).
        if (!offered.has('serve_web')) {
          writes.push({ id: 'mint-load', name: 'ToolSearch', args: { names: ['serve_web'] } })
        }
        res.end(toolCalls(writes))
        return
      }
      if (!called.has('serve_web')) {
        const args: Record<string, unknown> = directive.dev
          ? { path: directive.dir, command: 'npm run dev' }
          : { path: directive.dir, duration_seconds: 1800 }
        res.end(toolCalls([{ id: 'mint-serve', name: 'serve_web', args }]))
        return
      }
      res.end(sse({ role: 'assistant', content: 'Served.' }, null) + sse({}, 'stop') + 'data: [DONE]\n\n')
    })
  })

  const url = await new Promise<string>((resolve, reject) => {
    server.once('error', reject)
    server.listen(0, '127.0.0.1', () => {
      const address = server.address()
      if (!address || typeof address === 'string') {
        reject(new Error('mint provider: no ephemeral port'))
        return
      }
      resolve(`http://127.0.0.1:${address.port}`)
    })
  })

  return {
    url,
    calls: () => calls,
    failures: () => [...failures],
    close: async () => {
      await new Promise<void>((resolve, reject) => {
        server.close((err) => (err ? reject(err) : resolve()))
        server.closeAllConnections()
      })
    },
  }
}
