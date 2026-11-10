/**
 * Starts the D36 built-in fake IMAP/SMTP server (tests/e2e/fixtures/fakemail).
 * The spec seeds mail through the control port; it does not drive a live model.
 */
import { type ChildProcess, spawn } from 'child_process'
import { createInterface } from 'readline'
import path from 'path'
import { fileURLToPath } from 'url'

export interface FakeMail {
  imapHost: string
  imapPort: number
  smtpHost: string
  smtpPort: number
  controlURL: string
  user: string
  password: string
  stop: () => Promise<void>
  append: (mailbox: string, raw: string, flags: string[]) => Promise<{ uid: number }>
  storeFlags: (mailbox: string, uid: number, flags: string[]) => Promise<void>
  smtpMessages: () => Promise<{ count: number; messages: string[] }>
}

export async function startFakeMail(): Promise<FakeMail> {
  const here = path.dirname(fileURLToPath(import.meta.url))
  const dir = path.join(here, 'fakemail')
  const child = spawn('go', ['run', '.'], {
    cwd: dir,
    env: { ...process.env, CGO_ENABLED: '0' },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  const line = await firstLine(child)
  const info = JSON.parse(line) as { imap: string; smtp: string; control: string; user: string; password: string }
  const [imapHost, imapPort] = split(info.imap)
  const [smtpHost, smtpPort] = split(info.smtp)
  const controlURL = `http://${info.control}`
  return {
    imapHost, imapPort, smtpHost, smtpPort, controlURL,
    user: info.user, password: info.password,
    stop: () => stopChild(child),
    append: (mailbox, raw, flags) => post(controlURL, '/append', {
      mailbox, raw_base64: Buffer.from(raw).toString('base64'), flags,
    }),
    storeFlags: async (mailbox, uid, flags) => {
      await post(controlURL, '/store', { mailbox, uid, flags })
    },
    smtpMessages: () => getJSON(controlURL + '/smtp'),
  }
}

function split(addr: string): [string, number] {
  const i = addr.lastIndexOf(':')
  return [addr.slice(0, i), Number(addr.slice(i + 1))]
}

function firstLine(child: ChildProcess): Promise<string> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('fakemail did not print its ports within 60s')), 60_000)
    const rl = createInterface({ input: child.stdout! })
    rl.on('line', (line) => {
      clearTimeout(timer)
      rl.close()
      resolve(line)
    })
    child.once('exit', (code) => {
      clearTimeout(timer)
      reject(new Error(`fakemail exited ${code} before printing ports`))
    })
  })
}

function stopChild(child: ChildProcess): Promise<void> {
  return new Promise((resolve) => {
    if (child.killed || child.exitCode !== null) {
      resolve()
      return
    }
    child.once('exit', () => resolve())
    child.kill()
  })
}

async function post(base: string, p: string, body: unknown): Promise<{ uid: number }> {
  const res = await fetch(base + p, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`fakemail ${p} ${res.status}: ${await res.text()}`)
  return res.json() as Promise<{ uid: number }>
}

async function getJSON(url: string): Promise<{ count: number; messages: string[] }> {
  const res = await fetch(url)
  if (!res.ok) throw new Error(`fakemail GET ${res.status}`)
  return res.json() as Promise<{ count: number; messages: string[] }>
}
