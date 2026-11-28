// programs.ts — connected command-line programs (spec cli-credentials S1:
// FR-002, FR-003, FR-009, FR-019, FR-023). Every route here is person-only
// (browser session + CSRF), so only the SPA calls them. Wire types and
// response schemas are the generated ones (Hard Constraint #8).

import type { ZodType } from 'zod'
import {
  ChannelProgramsSetting as ChannelProgramsSettingSchema,
  ProgramEntry as ProgramEntrySchema,
  ProgramList as ProgramListSchema,
} from '@/lib/api/generated/schemas'
import type {
  ChannelProgramsSetting,
  ProgramCredentialRequest,
  ProgramCustomRequest,
  ProgramEntry,
  ProgramList,
  ProgramSettingsUpdate,
} from '@/lib/api/generated/openapi-types'
import { request } from './http'

export const programsQueryKeys = {
  list: ['programs'] as const,
  channel: (channelId: string) => ['channel-programs', channelId] as const,
}

const entryPath = (id: string) => `/programs/${encodeURIComponent(id)}`

/** GET /programs — presets and custom entries with their value-free state. */
export function fetchPrograms(): Promise<ProgramList> {
  return request<ProgramList>('/programs', undefined, ProgramListSchema as ZodType<ProgramList>)
}

/** POST /programs — create a custom entry (201). */
export function createProgram(body: ProgramCustomRequest): Promise<ProgramEntry> {
  return request<ProgramEntry>('/programs', { method: 'POST', body: JSON.stringify(body) }, ProgramEntrySchema as ZodType<ProgramEntry>)
}

/** PUT /programs/{id} — edit a custom entry; `revision` is the one the person reviewed. */
export function updateProgram(id: string, body: ProgramCustomRequest): Promise<ProgramEntry> {
  return request<ProgramEntry>(entryPath(id), { method: 'PUT', body: JSON.stringify(body) }, ProgramEntrySchema as ZodType<ProgramEntry>)
}

/** DELETE /programs/{id} — delete a custom entry (204). Refused while it is on for an agent. */
export function deleteProgram(id: string): Promise<void> {
  return request<void>(entryPath(id), { method: 'DELETE' })
}

/** PUT /programs/{id}/credential — save a token; the response is `checking` with the new generation. */
export function saveProgramCredential(id: string, body: ProgramCredentialRequest): Promise<ProgramEntry> {
  return request<ProgramEntry>(
    `${entryPath(id)}/credential`,
    { method: 'PUT', body: JSON.stringify(body) },
    ProgramEntrySchema as ZodType<ProgramEntry>,
  )
}

/** DELETE /programs/{id}/credential — disconnect (local copy removed, switches kept). */
export function disconnectProgram(id: string): Promise<ProgramEntry> {
  return request<ProgramEntry>(`${entryPath(id)}/credential`, { method: 'DELETE' }, ProgramEntrySchema as ZodType<ProgramEntry>)
}

/** POST /programs/{id}/check — check the current token again (202, `checking`). */
export function recheckProgram(id: string): Promise<ProgramEntry> {
  return request<ProgramEntry>(`${entryPath(id)}/check`, { method: 'POST' }, ProgramEntrySchema as ZodType<ProgramEntry>)
}

/** PUT /programs/settings — the web-chat setting. */
export function updateProgramSettings(body: ProgramSettingsUpdate): Promise<ProgramList> {
  return request<ProgramList>('/programs/settings', { method: 'PUT', body: JSON.stringify(body) }, ProgramListSchema as ZodType<ProgramList>)
}

/** GET /channels/{id}/programs — the channel's program switch (absent reads off). */
export function fetchChannelPrograms(channelId: string): Promise<ChannelProgramsSetting> {
  return request<ChannelProgramsSetting>(
    `/channels/${encodeURIComponent(channelId)}/programs`,
    undefined,
    ChannelProgramsSettingSchema as ZodType<ChannelProgramsSetting>,
  )
}

/** PUT /channels/{id}/programs — the only writer of the channel's program switch. */
export function setChannelPrograms(channelId: string, body: ChannelProgramsSetting): Promise<ChannelProgramsSetting> {
  return request<ChannelProgramsSetting>(
    `/channels/${encodeURIComponent(channelId)}/programs`,
    { method: 'PUT', body: JSON.stringify(body) },
    ChannelProgramsSettingSchema as ZodType<ChannelProgramsSetting>,
  )
}
