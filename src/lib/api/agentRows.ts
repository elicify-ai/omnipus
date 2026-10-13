// agentRows — one invalid agent must not take down the whole agent list
// (GitHub #1076).
//
// GET /agents is validated ROW BY ROW against the generated Agent schema: a
// top-level payload that is not an array still fails as a schema error, but
// a single row that fails validation is dropped from the list and named —
// agent id and the failing field — instead of the old whole-list failure with
// a misleading "server may be a different version" message. The healthy rows
// stay usable. Generated schema only (Hard Constraint #8); nothing is
// re-declared here.
import type { Agent } from './generated/openapi-types'
import { Agent as AgentSchema } from './generated/schemas'
import { logDiagnostic } from '@/lib/telemetry'

export type InvalidAgentRow = { // not-wire-format: a diagnosis of one rejected /agents row, kept in the browser
  id: string
  field: string
  message: string
}

function rowId(row: unknown, index: number): string {
  const id = (row as { id?: unknown } | null)?.id
  return typeof id === 'string' && id.trim() !== '' ? id : `#${index + 1}`
}

/** Split the rows into valid agents and diagnosed invalid rows. */
export function partitionAgentRows(rows: readonly unknown[]): { agents: Agent[]; invalid: InvalidAgentRow[] } {
  const agents: Agent[] = []
  const invalid: InvalidAgentRow[] = []
  rows.forEach((row, index) => {
    const parsed = AgentSchema.safeParse(row)
    if (parsed.success) {
      agents.push(parsed.data as Agent)
      return
    }
    const issue = parsed.error.issues[0]
    invalid.push({
      id: rowId(row, index),
      field: issue && issue.path.length > 0 ? issue.path.join('.') : '(row)',
      message: issue?.message ?? 'invalid',
    })
  })
  return { agents, invalid }
}

/** The user-facing description of one rejected row. */
export function invalidAgentRowText(row: InvalidAgentRow): string {
  return `One agent's data is invalid and is hidden from the list: ${row.id}, field ${row.field} (${row.message}).`
}

const reported = new Set<string>()

/**
 * Report each rejected row once per page life (the list is polled — the same
 * bad row must not toast on every poll): console, diagnostics and one toast
 * naming the agent and field.
 */
export function reportInvalidAgentRows(invalid: readonly InvalidAgentRow[]): void {
  for (const row of invalid) {
    const key = `${row.id}|${row.field}|${row.message}`
    if (reported.has(key)) continue
    reported.add(key)
    console.warn('[agents] invalid agent row dropped', row)
    logDiagnostic('agentsInvalidRowDropped', { agentId: row.id, field: row.field })
    if (typeof window === 'undefined') continue
    void import('@/store/ui')
      .then(({ useUiStore }) => {
        useUiStore.getState().addToast({ message: invalidAgentRowText(row), variant: 'warning' })
      })
      .catch((err) => console.error('[agents] failed to load toast store', err))
  }
}

/** Test seam: forget which rows were already reported. */
export function resetInvalidAgentRowReports(): void {
  reported.clear()
}
