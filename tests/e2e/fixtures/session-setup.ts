/**
 * session-setup.ts — Shared helper for replay-fidelity E2E setup.
 *
 * Factors out the repeated 7-step setup block used by replay-fidelity.spec.ts
 * tests (a), (b), (d), and (e):
 *   goto('/') → waitForWsConnected → createSession → renameSession →
 *   seedTranscript → openSession → waitForReplayDone
 *
 * Traces to: temporal-puzzling-melody.md W5-16.
 */

import * as fs from 'fs'
import * as path from 'path'
import { expect, type Page } from '@playwright/test'
import { chatInput } from './selectors'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

// ── Transcript entry types (mirrors replay-fidelity.spec.ts) ──────────────────

export interface TranscriptToolCall {
  id: string
  tool: string
  status: string
  duration_ms?: number
  parameters?: Record<string, unknown>
  result?: Record<string, unknown>
  parent_tool_call_id?: string
}

export interface TranscriptEntry {
  id: string
  type?: string
  role: string
  content?: string
  summary?: string
  timestamp: string
  agent_id?: string
  tool_calls?: TranscriptToolCall[]
}

// ── Internal helpers ──────────────────────────────────────────────────────────

function getStoredAuthToken(): string | null {
  const authFile = process.env.OMNIPUS_AUTH_FILE
    ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
    : path.join(
        path.dirname(new URL(import.meta.url).pathname),
        '.auth/admin.json',
      )
  if (!fs.existsSync(authFile)) return null
  try {
    const raw = fs.readFileSync(authFile, 'utf-8')
    const state = JSON.parse(raw) as {
      origins?: Array<{
        origin: string
        localStorage?: Array<{ name: string; value: string }>
      }>
    }
    for (const origin of state.origins ?? []) {
      for (const item of origin.localStorage ?? []) {
        if (item.name === 'omnipus_auth_token') return item.value
      }
    }
  } catch {
    // Auth file may not exist on first run
  }
  return null
}

async function getCsrfToken(page: Page): Promise<string | null> {
  const cookies = await page.context().cookies()
  const csrfCookie = cookies.find((c) => c.name === '__Host-csrf')
  return csrfCookie?.value ?? null
}

async function apiHeaders(page: Page): Promise<Record<string, string>> {
  const authToken = getStoredAuthToken()
  const csrfToken = await getCsrfToken(page)
  return {
    'Content-Type': 'application/json',
    ...(authToken ? { Authorization: `Bearer ${authToken}` } : {}),
    ...(csrfToken ? { 'X-CSRF-Token': csrfToken } : {}),
  }
}

async function createSession(page: Page): Promise<string> {
  const resp = await page.request.post(`${BASE_URL}/api/v1/sessions`, {
    headers: await apiHeaders(page),
    data: { agent_id: 'main', type: 'chat' },
  })
  if (!resp.ok()) {
    const body = await resp.text()
    throw new Error(`POST /api/v1/sessions failed: ${resp.status()} — ${body}`)
  }
  const meta = (await resp.json()) as { id: string }
  if (!meta.id) throw new Error('POST /api/v1/sessions returned no id')
  return meta.id
}

async function renameSession(page: Page, sessionId: string, title: string): Promise<void> {
  const resp = await page.request.put(`${BASE_URL}/api/v1/sessions/${sessionId}`, {
    headers: await apiHeaders(page),
    data: { title },
  })
  if (!resp.ok()) {
    const body = await resp.text()
    throw new Error(`PUT /api/v1/sessions/${sessionId} failed: ${resp.status()} — ${body}`)
  }
}

function seedTranscript(sessionId: string, entries: TranscriptEntry[]): void {
  const sessionDir = path.join(OMNIPUS_HOME, 'sessions', sessionId)
  if (!fs.existsSync(sessionDir)) {
    throw new Error(
      `Session directory does not exist: ${sessionDir}. ` +
        'Create the session via REST API before seeding the transcript.',
    )
  }
  const transcriptPath = path.join(sessionDir, 'transcript.jsonl')
  const lines = entries.map((e) => JSON.stringify(e)).join('\n') + '\n'
  fs.writeFileSync(transcriptPath, lines, { encoding: 'utf-8' })
}

async function openSession(page: Page, sessionTitle: string): Promise<void> {
  await page.getByRole('button', { name: 'Open sessions panel' }).click()
  const sessionBtn = page
    .getByRole('button', { name: new RegExp(`Open session: ${sessionTitle}`, 'i') })
    .first()
  await expect(sessionBtn).toBeVisible({ timeout: 10_000 })
  await sessionBtn.click()
}

async function waitForWsConnected(page: Page): Promise<void> {
  await expect(chatInput(page)).toBeEnabled({ timeout: 15_000 })
}

async function waitForReplayDone(page: Page): Promise<void> {
  await expect(chatInput(page)).toBeEnabled({ timeout: 30_000 })
}

// ── Public API ────────────────────────────────────────────────────────────────

export interface SeedAndOpenResult {
  sessionId: string
  sessionTitle: string
}

/**
 * seedAndOpenSession — one-call replacement for the repeated 7-step setup block
 * in replay-fidelity.spec.ts tests (a), (b), (d), and (e).
 *
 * Steps:
 *   1. Navigate to '/'
 *   2. Wait for the app banner to be visible
 *   3. Wait for WebSocket connection
 *   4. Create a session via REST API
 *   5. Rename it to `${namePrefix}-${Date.now()}`
 *   6. Seed the transcript.jsonl with `entries`
 *   7. Open the session via the sessions panel
 *   8. Wait for replay to complete (done frame)
 *
 * Returns the session ID and title so callers can reference them in assertions.
 */
export async function seedAndOpenSession(
  page: Page,
  namePrefix: string,
  entries: TranscriptEntry[],
): Promise<SeedAndOpenResult> {
  await page.goto('/')
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })
  await waitForWsConnected(page)

  const sessionId = await createSession(page)
  const sessionTitle = `${namePrefix}-${Date.now()}`
  await renameSession(page, sessionId, sessionTitle)

  seedTranscript(sessionId, entries)

  await openSession(page, sessionTitle)
  await waitForReplayDone(page)

  return { sessionId, sessionTitle }
}
