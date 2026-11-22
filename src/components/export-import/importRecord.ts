// importRecord — the per-tab import record (spec "Per-tab import record").
//
// One record per browser tab, in session storage under
// `omnipus.exportImport.import`. It holds no bundle bytes, no password and no
// secret names: the staging ID, the phase, the chosen workspace, the time it
// was written, and after a success the result summary. It is evidence for
// reloads only and is never sent to the server. A record older than two hours
// is discarded on read.

export const IMPORT_RECORD_KEY = 'omnipus.exportImport.import'
const MAX_AGE_MS = 2 * 60 * 60 * 1000

export type ImportRecordPhase = 'previewed' | 'apply_sent' | 'done'

export interface ImportRecordResult { // not-wire-format: per-tab session-storage summary of a finished import, never sent
  name: string
  new_id: string
  workspace_name: string
  activation: string
}

export interface ImportRecord { // not-wire-format: per-tab session-storage reload evidence (spec "Per-tab import record"), never sent
  staging_id: string
  phase: ImportRecordPhase
  workspace_id?: string
  workspace_name?: string
  written_at: string
  result?: ImportRecordResult
}

function storage(): Storage | null {
  try {
    return typeof window === 'undefined' ? null : window.sessionStorage
  } catch {
    return null
  }
}

const PHASES: readonly string[] = ['previewed', 'apply_sent', 'done']

export function readImportRecord(): ImportRecord | null {
  const store = storage()
  const raw = store?.getItem(IMPORT_RECORD_KEY)
  if (!raw) return null
  let parsed: Partial<ImportRecord>
  try {
    parsed = JSON.parse(raw) as Partial<ImportRecord>
  } catch {
    store?.removeItem(IMPORT_RECORD_KEY)
    return null
  }
  if (typeof parsed.staging_id !== 'string' || !PHASES.includes(String(parsed.phase))) {
    store?.removeItem(IMPORT_RECORD_KEY)
    return null
  }
  const writtenAt = Date.parse(String(parsed.written_at))
  if (Number.isNaN(writtenAt) || Date.now() - writtenAt > MAX_AGE_MS) {
    store?.removeItem(IMPORT_RECORD_KEY)
    return null
  }
  return parsed as ImportRecord
}

export function writeImportRecord(record: Omit<ImportRecord, 'written_at'>): void {
  storage()?.setItem(IMPORT_RECORD_KEY, JSON.stringify({ ...record, written_at: new Date().toISOString() }))
}

/** Merges a change into the current record for the same staging ID; no record, no write. */
export function updateImportRecord(stagingId: string, patch: Partial<Omit<ImportRecord, 'staging_id' | 'written_at'>>): void {
  const current = readImportRecord()
  if (!current || current.staging_id !== stagingId) return
  const { written_at: _writtenAt, ...rest } = current
  void _writtenAt
  writeImportRecord({ ...rest, ...patch })
}

export function clearImportRecord(): void {
  storage()?.removeItem(IMPORT_RECORD_KEY)
}
