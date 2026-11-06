#!/usr/bin/env node
// llm-fallback-smoke.mjs — one real turn against a running gateway, checking
// which model answered. Used by .github/workflows/llm-fallback-smoke.yml
// (design: CI on MiniMax M3.1 Flash with DeepSeek on OpenRouter as fallback,
// section 5.3).
//
// It creates one Main agent on the primary pair with fallback_models =
// [fallback pair], adds it to the default workspace team, opens a chat over
// the WebSocket, asks for an exact token and reads the frames:
//
//   --expect fallback : the reply contains the token, and a provider_fallback
//                       frame names the fallback as answered_model and the
//                       primary as unavailable_model;
//   --expect primary  : the reply contains the token and NO provider_fallback
//                       frame arrived (the positive control: the primary path
//                       really is the primary).
//
// Output is frame types, model ids and verdicts only — never a key, never the
// bearer token. Exit 0 pass, 1 fail, 2 usage.
//
// Env: E2E_PRIMARY_PROVIDER, E2E_PRIMARY_MODEL, E2E_FALLBACK_PROVIDER,
// E2E_FALLBACK_MODEL (from `scripts/e2e-model.sh env`).
// Args: --base URL --token BEARER --expect fallback|primary --run-id ID
//       [--timeout-seconds N]

function arg(name, fallback) {
  const i = process.argv.indexOf(`--${name}`)
  if (i === -1 || i + 1 >= process.argv.length) return fallback
  return process.argv[i + 1]
}

function need(v, what) {
  if (!v) {
    console.error(`llm-fallback-smoke: ${what} is required`)
    process.exit(2)
  }
  return v
}

const base = need(arg('base'), '--base').replace(/\/$/, '')
const token = need(arg('token'), '--token')
const expect = need(arg('expect'), '--expect')
const runId = need(arg('run-id'), '--run-id')
const timeoutMs = Number(arg('timeout-seconds', '180')) * 1000
if (expect !== 'fallback' && expect !== 'primary') {
  console.error('llm-fallback-smoke: --expect must be fallback or primary')
  process.exit(2)
}
const primary = {
  provider: need(process.env.E2E_PRIMARY_PROVIDER, 'E2E_PRIMARY_PROVIDER'),
  model: need(process.env.E2E_PRIMARY_MODEL, 'E2E_PRIMARY_MODEL'),
}
const fallback = {
  provider: need(process.env.E2E_FALLBACK_PROVIDER, 'E2E_FALLBACK_PROVIDER'),
  model: need(process.env.E2E_FALLBACK_MODEL, 'E2E_FALLBACK_MODEL'),
}
const answerToken = `FALLBACK-SMOKE-${runId}`

async function api(method, path, body) {
  const res = await fetch(base + path, {
    method,
    headers: {
      Authorization: `Bearer ${token}`,
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await res.text()
  if (!res.ok) {
    throw new Error(`${method} ${path} returned HTTP ${res.status}`)
  }
  return text === '' ? null : JSON.parse(text)
}

async function createAgent() {
  const agent = await api('POST', '/api/v1/agents', {
    type: 'Main',
    name: `Fallback smoke ${runId}`,
    soul: 'You are a CI smoke probe. Reply with exactly the text you are asked for and nothing else. Never call a tool.',
    provider: primary.provider,
    model: primary.model,
    fallback_models: [{ provider: fallback.provider, model: fallback.model }],
  })
  const workspaces = await api('GET', '/api/v1/workspaces')
  const list = Array.isArray(workspaces) ? workspaces : workspaces?.workspaces ?? []
  const ws = list.find((w) => w.is_default === true)
  if (!ws) throw new Error('no default workspace to host the smoke agent')
  await api('PUT', `/api/v1/workspaces/${encodeURIComponent(ws.id)}`, {
    revision: ws.revision,
    core_team: [...(ws.core_team ?? []), agent.id],
  })
  return agent.id
}

function chat(agentId) {
  return new Promise((resolve, reject) => {
    const wsURL = base.replace(/^http/, 'ws') + '/api/v1/chat/ws'
    const socket = new WebSocket(wsURL)
    const seen = { text: '', fallbackFrames: [], types: [], error: '' }
    const timer = setTimeout(() => {
      socket.close()
      reject(new Error(`no done frame within ${timeoutMs / 1000}s (frames: ${seen.types.join(',')})`))
    }, timeoutMs)
    socket.addEventListener('open', () => {
      socket.send(JSON.stringify({ type: 'auth', token }))
      socket.send(JSON.stringify({
        type: 'message',
        agent_id: agentId,
        content: `Reply with exactly this text and nothing else: ${answerToken}`,
      }))
    })
    socket.addEventListener('message', (ev) => {
      let frame
      try {
        frame = JSON.parse(String(ev.data))
      } catch {
        return
      }
      seen.types.push(frame.type)
      if (frame.type === 'token' && typeof frame.content === 'string') seen.text += frame.content
      if (frame.type === 'provider_fallback') {
        seen.fallbackFrames.push({ answered: frame.answered_model, unavailable: frame.unavailable_model, code: frame.unavailable_code })
      }
      if (frame.type === 'error') {
        seen.error = typeof frame.message === 'string' ? frame.message.slice(0, 300) : 'error frame'
      }
      if (frame.type === 'done' || frame.type === 'error') {
        clearTimeout(timer)
        socket.close()
        resolve(seen)
      }
    })
    socket.addEventListener('error', () => {
      clearTimeout(timer)
      reject(new Error('websocket error'))
    })
  })
}

try {
  const agentId = await createAgent()
  const seen = await chat(agentId)
  const answered = seen.text.includes(answerToken)
  console.log(`frames: ${seen.types.join(',')}`)
  console.log(`provider_fallback frames: ${JSON.stringify(seen.fallbackFrames)}`)
  console.log(`reply contains the token: ${answered}`)
  const failures = []
  if (seen.error) failures.push(`error frame: ${seen.error}`)
  if (!answered) failures.push('the reply does not contain the requested token')
  if (expect === 'fallback') {
    const ok = seen.fallbackFrames.some((f) => f.answered === fallback.model && f.unavailable === primary.model)
    if (!ok) failures.push(`no provider_fallback frame answered by ${fallback.model} for ${primary.model}`)
  } else if (seen.fallbackFrames.length > 0) {
    failures.push('the positive control fell back; the primary did not answer')
  }
  if (failures.length > 0) {
    console.log(`FAIL (${expect}): ${failures.join('; ')}`)
    process.exit(1)
  }
  console.log(`PASS (${expect})`)
} catch (err) {
  console.log(`FAIL (${expect}): ${err.message}`)
  process.exit(1)
}
