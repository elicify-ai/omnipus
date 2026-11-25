// permissions.ts: the canonical global permissions of every tool and skill
// (Skills & Tools spec C-2, C-3). One read model replaces the retired
// /security/tool-policies and /security/skill-trust clients (C-22 row 6).

import type { ZodType } from 'zod'
import { PermissionSettings as PermissionSettingsSchema } from '@/lib/api/generated/schemas'
import type { PermissionSettings, PermissionSettingsPatch } from '@/lib/api/generated/openapi-types'
import { request } from './http'

/** GET /security/permissions: the complete stored global settings. */
export function fetchPermissionSettings(): Promise<PermissionSettings> {
  return request<PermissionSettings>(
    '/security/permissions',
    undefined,
    PermissionSettingsSchema as ZodType<PermissionSettings>,
  )
}

/**
 * PATCH /security/permissions with only the changed keys. `keepalive` lets a
 * save started just before the page or tab closes still reach the server
 * (C-16). The answer is the whole stored state.
 */
export function patchPermissionSettings(patch: PermissionSettingsPatch): Promise<PermissionSettings> {
  return request<PermissionSettings>(
    '/security/permissions',
    { method: 'PATCH', body: JSON.stringify(patch), keepalive: true },
    PermissionSettingsSchema as ZodType<PermissionSettings>,
  )
}

/** The server's own words from an error body ({error}), or null. */
export function serverErrorText(body: string | undefined): string | null {
  if (!body) return null
  try {
    const parsed = JSON.parse(body) as { error?: unknown; message?: unknown }
    if (typeof parsed.error === 'string' && parsed.error.trim()) return parsed.error.trim()
    if (typeof parsed.message === 'string' && parsed.message.trim()) return parsed.message.trim()
  } catch {
    // Not JSON: no server sentence to show; the caller uses its own wording.
  }
  return null
}
