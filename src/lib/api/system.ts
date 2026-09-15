// system.ts: Gateway status, storage, doctor, activity, about, devices, notifications

import type { ZodType } from 'zod'
import { z } from 'zod'
import {
  DoctorResult as DoctorResultSchema,
  DevicesResponse as DevicesResponseSchema,
  StorageStats as StorageStatsSchema,
  GatewayStatus as GatewayStatusSchema,
  ActivityEventsResponse as ActivityEventsResponseSchema,
  // fix-AC: promoted from hand-written inline schemas:
  UserContextResponse as UserContextResponseSchema,
  // #264 Notifications (contract-first #8):
  NotificationList as NotificationListSchema,
  TokenUsageSummary as TokenUsageSummarySchema,
  // Version drift detection (used by fetchVersion → useVersionCheck):
  VersionResponse as VersionResponseSchema,
  // Voice provider capability detection (used by fetchVoiceProvider):
  VoiceProvider as VoiceProviderSchema,
} from '@/lib/api/generated/schemas'
import type {
  GatewayStatus,
  ActivityEventsResponse,
  DoctorResult,
  DevicesResponse,
  StorageStats,
  // fix-AC: promoted from hand-written inline schemas:
  UserContextResponse,
  TokenUsageSummary,
  // #264 Notifications (contract-first #8):
  NotificationList,
  // Version drift detection (used by useVersionCheck):
  VersionResponse,
  // Voice provider capability detection (used by voice-provider-detect):
  VoiceProvider,
} from '@/lib/api/generated/openapi-types'
import { request } from './http'

// ── #264 Schedules ──────────────────────────────────────────────────────────────
// The SPA schedules client was DELETED (2026-07-19, operator directive): the
// Schedules UI is retired — scheduled/recurring work lives in the workspace
// Calendar; heartbeats are the only agent-level exception. The backend
// /api/v1/schedules entity and the pkg/cron engine remain (they execute task
// triggers + heartbeats). Do NOT reintroduce a schedules UI or these wrappers
// when merging older branches — see CLAUDE.md "Retired surfaces".

// ── #264 Notifications ────────────────────────────────────────────────────────
//
// Header notification center. The REST surface seeds the store on mount and the
// `notification` WS frame keeps it live. NotificationList wire type is re-exported
// from generated openapi-types (contract-first #8); see
// contracts/components/schemas/Notification*.yaml.

export function fetchNotifications(): Promise<NotificationList> {
  // GET /notifications → { notifications: Notification[], unread_count }.
  return request<NotificationList>('/notifications', undefined, NotificationListSchema as ZodType<NotificationList>)
}

export function markNotificationRead(id: string): Promise<void> {
  // POST /notifications/{id}/read — void response.
  return request<void>(`/notifications/${encodeURIComponent(id)}/read`, { method: 'POST' })
}

export function markAllNotificationsRead(): Promise<void> {
  // POST /notifications/read-all — void response.
  return request<void>('/notifications/read-all', { method: 'POST' })
}

// ── Gateway Status ────────────────────────────────────────────────────────────

// GatewayStatus — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/GatewayStatus.yaml.

export function fetchGatewayStatus(): Promise<GatewayStatus> {
  return request<GatewayStatus>('/status', undefined, GatewayStatusSchema as ZodType<GatewayStatus>)
}

// ── Storage Stats ─────────────────────────────────────────────────────────────

// StorageStats — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/StorageStats.yaml.

export function fetchStorageStats(): Promise<StorageStats> {
  return request<StorageStats>('/storage/stats', undefined, StorageStatsSchema)
}

// ── Doctor ────────────────────────────────────────────────────────────────────

// DoctorIssue — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/DoctorIssue.yaml.
// DoctorResult — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/DoctorResult.yaml.

export function fetchDoctorResults(): Promise<DoctorResult | null> {
  return request<DoctorResult | null>('/doctor', undefined, DoctorResultSchema.nullable())
}

export function runDoctor(): Promise<DoctorResult> {
  return request<DoctorResult>('/doctor', { method: 'POST' }, DoctorResultSchema)
}

// ── Activity Feed ─────────────────────────────────────────────────────────────

// ActivityEvent — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/ActivityEvent.yaml.
// GET /activity returns ActivityEventsResponse ({ events, warning? }), not a bare
// array — the backend surfaces a `warning` when a session store was unreadable
// (partial results). See contracts/components/schemas/ActivityEventsResponse.yaml.

export function fetchActivity(): Promise<ActivityEventsResponse> {
  return request<ActivityEventsResponse>('/activity', undefined, ActivityEventsResponseSchema)
}

// ── Devices ───────────────────────────────────────────────────────────────────

// DevicePending — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/DevicePending.yaml.
// DevicePaired — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/DevicePaired.yaml.
// DevicesResponse — re-exported from generated openapi-types (contract-first #8).
// See contracts/components/schemas/DevicesResponse.yaml.

export function fetchDevices(): Promise<DevicesResponse> {
  return request<DevicesResponse>('/devices', undefined, DevicesResponseSchema)
}

// ── About ─────────────────────────────────────────────────────────────────────

export interface AboutInfo { // not-wire-format: SPA-internal backward-compatible subset of AboutResponse. The generated AboutResponse has required fields (uptime, pid, frame_ancestors_fallback) and different optionality for preview_enabled. The SPA uses this looser interface to maintain backward compatibility with older gateway versions that may not send all AboutResponse fields.
  version: string
  go_version: string
  os: string
  arch: string
  uptime_seconds: number
  // preview_enabled reflects whether gateway.preview_enabled is currently on
  // (US-4 / FR-006/FR-015, ADR-044). When true, /preview/ is served on the
  // main gateway listener (no separate preview listener/port/origin exists
  // anymore — the preview_port/preview_origin/preview_listener_enabled
  // fields this replaces are retired, not deprecated). Read live: toggling
  // the Settings → Gateway "Preview" switch takes effect on the next fetch,
  // no restart. Absent on old gateway versions that predate the field
  // (treat as true — the same long-standing-default-on convention the
  // retired preview_listener_enabled used).
  preview_enabled?: boolean
  // warmup_timeout_seconds is sourced from
  // cfg.Tools.RunInWorkspace.WarmupTimeoutSeconds (default 60). Used by
  // RunInWorkspaceUI to cap the warmup polling loop.
  warmup_timeout_seconds?: number
  // device_pairing_enabled reflects Sandbox.Experimental.DevicePairingEnabled —
  // a dark-launched flag (default false). Absent on old gateway versions that
  // predate the field (treat as false — opposite default from
  // preview_enabled, since this is a new opt-in feature, not a long-standing
  // one being made optional).
  device_pairing_enabled?: boolean
}

// AboutInfoSchema is a hand-written local schema (not from generated schemas —
// see the GAP REPORT note above the generated-schema import block). It
// mirrors the AboutInfo interface field-for-field: the five fields every
// gateway version has always sent are required; every field added since are
// `.optional()` so a response from an older gateway that predates them still
// validates. A response failing this schema (e.g. version/os/arch missing or
// wrong-typed) is a genuine contract break worth surfacing as ApiSchemaError.
const AboutInfoSchema: ZodType<AboutInfo> = z.object({
  version: z.string(),
  go_version: z.string(),
  os: z.string(),
  arch: z.string(),
  uptime_seconds: z.number(),
  preview_enabled: z.boolean().optional(),
  warmup_timeout_seconds: z.number().optional(),
  device_pairing_enabled: z.boolean().optional(),
})

export function fetchAboutInfo(): Promise<AboutInfo> {
  return request<AboutInfo>('/about', undefined, AboutInfoSchema)
}

/**
 * Returns the gateway's version string and build SHA. Used by the
 * `useVersionCheck` hook to detect version drift. The `/version` endpoint
 * is unauthenticated and lives outside the regular auth/CSRF envelope, so we
 * go through `request` (not raw `fetch`) to keep the request shape uniform
 * with every other call (credentials, error handling). The response is
 * validated by the generated `VersionResponse` Zod schema (per
 * `contracts/openapi.yaml`).
 */
export function fetchVersion(): Promise<VersionResponse> {
  return request<VersionResponse>(
    '/version',
    undefined,
    VersionResponseSchema as ZodType<VersionResponse>,
  )
}

/**
 * Returns the active voice provider descriptor. Used by
 * `voice-provider-detect` to decide whether the SPA should render a
 * dropdown, a free-text input, or hide the voice field. The response is
 * validated by the generated `VoiceProvider` Zod schema.
 */
export function fetchVoiceProvider(): Promise<VoiceProvider> {
  return request<VoiceProvider>(
    '/voice/provider',
    undefined,
    VoiceProviderSchema as ZodType<VoiceProvider>,
  )
}

/**
 * Returns whether the preview feature (gateway.preview_enabled) is on.
 *
 * `preview_enabled` is an optional bool where `undefined` means "true" — old
 * gateway versions that predate the field did not include it, and those
 * versions always served previews. Reading the field directly risks
 * treating `undefined` as falsy; use this accessor instead. Live: re-fetch
 * `/about` after toggling Settings → Gateway to see the new value (no
 * restart required — US-4/FR-006).
 */
export function isPreviewEnabled(info: AboutInfo | undefined): boolean {
  return info?.preview_enabled !== false
}

/**
 * Returns whether the (dark-launched) device-pairing feature is enabled.
 * Unlike `isPreviewEnabled`, `undefined` means "false" here — this is
 * a new opt-in feature (default off), not a long-standing one being made
 * backward-compatibly optional.
 */
export function isDevicePairingEnabled(info: AboutInfo | undefined): boolean {
  return info?.device_pairing_enabled === true
}

// ── User Context (USER.md) ────────────────────────────────────────────────────

export function fetchUserContext(): Promise<UserContextResponse> {
  return request<UserContextResponse>('/user-context', undefined, UserContextResponseSchema as ZodType<UserContextResponse>)
}

export function updateUserContext(content: string): Promise<void> {
  // no-schema: void response; PUT returns 204 No Content.
  return request<void>('/user-context', {
    method: 'PUT',
    body: JSON.stringify({ content }),
  })
}

// ── Token Usage Stats ─────────────────────────────────────────────────────────
//
// Token usage summary by agent for the current month.
// See contracts/components/schemas/TokenUsageSummary.yaml.

export type TokenStatsPeriod = 'day' | 'week' | 'month' | 'all'

export const tokenStatsQueryKeys = {
  monthly: () => ['token-stats', 'month'] as const,
  byPeriod: (period: TokenStatsPeriod) => ['token-stats', period] as const,
}

export function fetchTokenStats(period: TokenStatsPeriod = 'month'): Promise<TokenUsageSummary> {
  return request<TokenUsageSummary>(
    `/stats/tokens?period=${period}`,
    undefined,
    TokenUsageSummarySchema as ZodType<TokenUsageSummary>,
  )
}
