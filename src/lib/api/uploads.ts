// uploads.ts: File upload, voice transcription and live-browser element inspect

import { ApiError } from '../api-error'
import { maybeDevToast } from '../dev-toast'
import type { ZodType } from 'zod'
import {
  UploadFilesResponse as UploadFilesResponseSchema,
  TranscribeResponse as TranscribeResponseSchema,
  // ADR-039 — user-initiated browsing + annotate-a-region-and-discuss:
  BrowserInspectResponse as BrowserInspectResponseSchema,
} from '@/lib/api/generated/schemas'
import type {
  UploadFilesResponse,
  TranscribeResponse,
  // ADR-039 — user-initiated browsing + annotate-a-region-and-discuss:
  BrowserInspectRequest,
  BrowserInspectResponse,
} from '@/lib/api/generated/openapi-types'
import { ApiSchemaError, BASE_URL, CSRF_HEADER_NAME, _recordApiSchemaError, readCSRFCookie, request, withCsrfRetry } from './http'

// ── File Upload ───────────────────────────────────────────────────────────────

// UploadedFile — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/UploadedFile.yaml.

export async function uploadFiles(sessionId: string, files: File[], workspaceId?: string): Promise<UploadFilesResponse> {
  const formData = new FormData()
  formData.append('session_id', sessionId)
  if (workspaceId) {
    formData.append('workspace_id', workspaceId)
  }
  for (const file of files) {
    formData.append('files', file)
  }
  // Upload is a state-changing POST — fail fast if we have no CSRF cookie
  // (see request() for the same pattern).
  if (readCSRFCookie() === null) {
    throw new ApiError(
      403,
      'CSRF cookie missing — cannot upload files. Log in first so the server can issue the CSRF cookie.',
      { code: 'csrf_missing' },
    )
  }
  // FormData holds File/Blob references (not a consumed stream), so retrying
  // with the same formData on a CSRF-recovery pass (withCsrfRetry) re-sends
  // the same bytes safely.
  return withCsrfRetry(() => doUploadFiles(formData))
}

async function doUploadFiles(formData: FormData): Promise<UploadFilesResponse> {
  // Read fresh — never cache (see readCSRFCookie).
  const csrf = readCSRFCookie()
  // Build headers by hand because FormData must NOT have a Content-Type
  // set — the browser needs to fill in the multipart boundary itself. No
  // Authorization header: auth is the omnipus-session cookie (US-5 / FR-010).
  const headers: Record<string, string> = {}
  if (csrf) headers[CSRF_HEADER_NAME] = csrf
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1/upload`, {
      method: 'POST',
      credentials: 'include',
      headers,
      body: formData,
    })
  } catch (cause) {
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }
  if (!res.ok) {
    throw await ApiError.fromResponse(res)
  }
  const raw: unknown = await res.json()
  const parsed = (UploadFilesResponseSchema as ZodType<UploadFilesResponse>).safeParse(raw)
  if (!parsed.success) {
    _recordApiSchemaError('/upload', parsed.error.issues.length)
    const issues = parsed.error.issues.map((i) => ({ path: i.path as (string | number)[], message: i.message }))
    void maybeDevToast(`[api] uploadFiles response schema mismatch: ${issues[0]?.message ?? 'unknown'}`, 'POST:/upload:schema')
    throw new ApiSchemaError('/upload', issues, raw)
  }
  return parsed.data
}

// ── Live Browser (ADR-039) ──────────────────────────────────────────────────

/**
 * Best-effort DOM-element resolution at a point in an agent's live browser
 * tab (ADR-039 D-B3). Used by the annotate-and-discuss flow in
 * BrowserLiveView to enrich a cropped-image annotation with the underlying
 * element's tag/text when possible.
 *
 * The endpoint is best-effort SERVER-side (`ok:false` + `reason` on a
 * cross-origin frame / detached node / timeout) — that is a normal 200
 * response, not a request failure. Callers must not treat a transport-level
 * failure (network error, 4xx/5xx) any differently: either way, the caller's
 * job is to fall back to the image+comment alone, never to block on this.
 */
export function inspectBrowserElement(req: BrowserInspectRequest): Promise<BrowserInspectResponse> {
  return request<BrowserInspectResponse>(
    '/browser/inspect',
    { method: 'POST', body: JSON.stringify(req) },
    BrowserInspectResponseSchema as ZodType<BrowserInspectResponse>,
  )
}

// ── Voice transcription (composer mic, Spec-6 FR-12.1) ─────────────────────────
//
// transcribeAudio uploads a recorded audio Blob to the active transcriber and
// returns the recognised text. Multipart form-data; the request() helper is
// JSON-only, so this uses fetch directly with the CSRF header (auth is the
// omnipus-session cookie, sent via credentials:'include' below — US-5 / FR-010).
// MIME-type-fragment → file-extension lookup for transcribeAudio, in priority
// order. 'webm' is both the first check and the fallback, so no matching
// fragment falls through to the same value a match on 'webm' would produce.
const AUDIO_EXT_BY_MIME_FRAGMENT: readonly (readonly [fragment: string, ext: string])[] = [
  ['webm', 'webm'],
  ['ogg', 'ogg'],
  ['wav', 'wav'],
  ['mp4', 'm4a'],
  ['mpeg', 'm4a'],
]

function audioFileExtension(mimeType: string): string {
  return AUDIO_EXT_BY_MIME_FRAGMENT.find(([fragment]) => mimeType.includes(fragment))?.[1] ?? 'webm'
}

export async function transcribeAudio(audio: Blob): Promise<TranscribeResponse> {
  const form = new FormData()
  // Preserve the recorded mime type's extension hint where possible.
  const ext = audioFileExtension(audio.type)
  form.append('audio', audio, `recording.${ext}`)

  // Blob is not a consumed stream, so retrying with the same form on a
  // CSRF-recovery pass (withCsrfRetry) re-sends the same bytes safely.
  return withCsrfRetry(() => doTranscribeAudio(form))
}

async function doTranscribeAudio(form: FormData): Promise<TranscribeResponse> {
  // Read fresh — never cache (see readCSRFCookie).
  const csrf = readCSRFCookie()
  let res: Response
  try {
    res = await fetch(`${BASE_URL}/api/v1/voice/transcribe`, {
      method: 'POST',
      credentials: 'include',
      // NOTE: do NOT set Content-Type — the browser sets the multipart boundary.
      headers: {
        ...(csrf ? { [CSRF_HEADER_NAME]: csrf } : {}),
      },
      body: form,
    })
  } catch (cause) {
    throw new ApiError(0, 'Network unavailable. Check your connection.', { cause })
  }
  if (!res.ok) {
    throw await ApiError.fromResponse(res)
  }
  const raw = (await res.json()) as unknown
  const parsed = TranscribeResponseSchema.safeParse(raw)
  if (!parsed.success) {
    const issues = parsed.error.issues
    _recordApiSchemaError('/voice/transcribe', issues.length)
    void maybeDevToast(
      `[api] transcribe response schema mismatch: ${issues[0]?.message ?? 'unknown'}`,
      'POST:/voice/transcribe:schema',
    )
    throw new ApiSchemaError('/voice/transcribe', issues, raw)
  }
  return parsed.data as TranscribeResponse
}
