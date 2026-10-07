// Pure Sessions hierarchy (Wave 1, FR-030/FR-033/FR-034).
//
// Helpers nest under their real parent in every filter. A parent that is not
// in a COMPLETE fetch becomes one "parent chat unavailable" placeholder per
// missing id — not a made-up title, and not a session you can open. An
// incomplete fetch never uses that placeholder: a missing parent may simply
// be a page that did not load. Consecutive helper siblings with the same
// title and kind fold under their own parent only.

import type { Session } from '@/lib/api'
import { sessionKindLabel } from './sessionLabels'

export const FOLD_ID_PREFIX = 'fold:'
export const MISSING_PARENT_ID_PREFIX = 'missing-parent:'
export const UNPLACED_ID_PREFIX = 'unplaced:'

const FOLD_MIN = 2

export type SessionActivation = 'open' | 'expand'

export function sessionActivation(id: string): SessionActivation {
  if (
    id.startsWith(FOLD_ID_PREFIX) ||
    id.startsWith(MISSING_PARENT_ID_PREFIX) ||
    id.startsWith(UNPLACED_ID_PREFIX)
  ) {
    return 'expand'
  }
  return 'open'
}

export interface SessionOverviewOptions {
  filter: 'all' | 'running'
  query: string
  fromDate: string
  toDate: string
  incomplete: boolean
  workspaceName: (workspaceId: string | undefined) => string
  agentName: (agentId: string | undefined) => string
}

export interface SessionOverview {
  /** Real roots, plus placeholder / unplaced marker rows. */
  roots: Session[]
  childrenByParent: Map<string, Session[]>
  /** Ancestors and folds that must open so a hit stays visible, plus every placeholder. */
  autoExpandIds: ReadonlySet<string>
}

export function buildSessionOverview(sessions: Session[], opts: SessionOverviewOptions): SessionOverview {
  const byId = new Map(sessions.map((session) => [session.id, session]))
  const parentIndex = indexByParent(sessions)
  const narrowing = isNarrowing(opts)
  const hits = new Set(sessions.filter((session) => isHit(session, opts)).map((session) => session.id))
  const keep = keptIds(byId, hits)
  const foldOf = new Map<string, string>()
  const childrenByParent = new Map<string, Session[]>()
  const autoExpandIds = new Set<string>()

  const roots: Session[] = []
  const missing = new Map<string, Session[]>()
  const unplaced = new Map<string, Session[]>()

  for (const session of sessions) {
    if (!shown(session.id, keep, parentIndex)) continue
    if (!session.parent_session_id || byId.has(session.parent_session_id)) {
      if (!session.parent_session_id) roots.push(session)
      continue
    }
    // Key by the real parent id in both cases so identical helpers from
    // different parents are never folded together. Incomplete fetches use a
    // different row than "parent chat unavailable".
    const bucket = opts.incomplete ? unplaced : missing
    pushBucket(bucket, session.parent_session_id, session)
  }

  const attached = new Set<string>()
  for (const root of roots) {
    attachChildren(root.id, parentIndex, keep, childrenByParent, foldOf, attached, autoExpandIds)
  }
  for (const [parentId, kids] of missing) {
    const marker = markerSession(
      `${MISSING_PARENT_ID_PREFIX}${parentId}`,
      'parent chat unavailable',
      kids,
    )
    roots.push(marker)
    attachListed(marker.id, kids, parentIndex, keep, childrenByParent, foldOf, attached, autoExpandIds)
    autoExpandIds.add(marker.id)
  }
  for (const [parentId, kids] of unplaced) {
    const marker = markerSession(
      `${UNPLACED_ID_PREFIX}${parentId}`,
      'Parent not in this partial list',
      kids,
    )
    roots.push(marker)
    attachListed(marker.id, kids, parentIndex, keep, childrenByParent, foldOf, attached, autoExpandIds)
    autoExpandIds.add(marker.id)
  }

  if (narrowing) {
    for (const hitId of hits) revealHit(hitId, byId, foldOf, autoExpandIds)
  }
  return { roots, childrenByParent, autoExpandIds }
}

function isNarrowing(opts: SessionOverviewOptions): boolean {
  return opts.query.trim() !== '' || opts.filter === 'running' || opts.fromDate !== '' || opts.toDate !== ''
}

function isHit(session: Session, opts: SessionOverviewOptions): boolean {
  if (!inDateRange(session, opts.fromDate, opts.toDate)) return false
  if (opts.query.trim() !== '' && !matchesQuery(session, opts)) return false
  if (opts.filter === 'running' && session.execution !== 'running') return false
  return true
}

function matchesQuery(session: Session, opts: SessionOverviewOptions): boolean {
  const query = opts.query.trim().toLowerCase()
  const title = (session.title ?? '').toLowerCase()
  const workspace = opts.workspaceName(session.workspace_id).toLowerCase()
  const agent = opts.agentName(session.active_agent_id ?? session.agent_id).toLowerCase()
  return title.includes(query) || workspace.includes(query) || agent.includes(query)
}

function inDateRange(session: Session, fromDate: string, toDate: string): boolean {
  if (fromDate === '' && toDate === '') return true
  const updated = new Date(session.updated_at).getTime()
  if (Number.isNaN(updated)) return false
  if (fromDate !== '') {
    const from = new Date(`${fromDate}T00:00:00`).getTime()
    if (updated < from) return false
  }
  if (toDate !== '') {
    const to = new Date(`${toDate}T23:59:59`).getTime()
    if (updated > to) return false
  }
  return true
}

function keptIds(byId: Map<string, Session>, hits: Set<string>): Set<string> {
  const keep = new Set<string>()
  for (const hitId of hits) {
    keep.add(hitId)
    let parentId = byId.get(hitId)?.parent_session_id
    const seen = new Set<string>()
    while (parentId && byId.has(parentId) && !seen.has(parentId)) {
      seen.add(parentId)
      keep.add(parentId)
      parentId = byId.get(parentId)?.parent_session_id
    }
  }
  return keep
}

function shown(id: string, keep: Set<string>, parentIndex: Map<string, Session[]>): boolean {
  return keep.has(id) || hasKeptDescendant(id, keep, parentIndex, new Set())
}

function hasKeptDescendant(
  id: string,
  keep: Set<string>,
  parentIndex: Map<string, Session[]>,
  seen: Set<string>,
): boolean {
  if (seen.has(id)) return false
  seen.add(id)
  for (const child of parentIndex.get(id) ?? []) {
    if (keep.has(child.id) || hasKeptDescendant(child.id, keep, parentIndex, seen)) return true
  }
  return false
}

function attachChildren(
  parentId: string,
  parentIndex: Map<string, Session[]>,
  keep: Set<string>,
  childrenByParent: Map<string, Session[]>,
  foldOf: Map<string, string>,
  attached: Set<string>,
  autoExpandIds: Set<string>,
): void {
  if (attached.has(parentId)) return
  attached.add(parentId)
  const kids = (parentIndex.get(parentId) ?? []).filter((child) => shown(child.id, keep, parentIndex))
  attachListed(parentId, kids, parentIndex, keep, childrenByParent, foldOf, attached, autoExpandIds)
}

function attachListed(
  parentId: string,
  kids: Session[],
  parentIndex: Map<string, Session[]>,
  keep: Set<string>,
  childrenByParent: Map<string, Session[]>,
  foldOf: Map<string, string>,
  attached: Set<string>,
  autoExpandIds: Set<string>,
): void {
  const folded = foldHelperRuns(kids, foldOf)
  childrenByParent.set(parentId, folded)
  // A short nest starts open so a helper's kind and a fold summary are on
  // screen. A fan-out past the virtualize threshold stays collapsed (FR-094).
  if (folded.length > 0 && folded.length <= 20 && !parentId.startsWith(FOLD_ID_PREFIX)) {
    autoExpandIds.add(parentId)
  }
  for (const child of folded) {
    if (child.id.startsWith(FOLD_ID_PREFIX)) {
      const members = kids.filter((kid) => foldOf.get(kid.id) === child.id)
      childrenByParent.set(child.id, members)
      for (const member of members) {
        attachChildren(member.id, parentIndex, keep, childrenByParent, foldOf, attached, autoExpandIds)
      }
      continue
    }
    attachChildren(child.id, parentIndex, keep, childrenByParent, foldOf, attached, autoExpandIds)
  }
}

function foldHelperRuns(kids: Session[], foldOf: Map<string, string>): Session[] {
  const folded: Session[] = []
  let index = 0
  while (index < kids.length) {
    const start = kids[index]
    if (!start || start.type !== 'delegate') {
      if (start) folded.push(start)
      index += 1
      continue
    }
    let end = index + 1
    while (end < kids.length && sameHelperRun(start, kids[end])) end += 1
    const run = kids.slice(index, end)
    if (run.length >= FOLD_MIN) {
      const fold = foldSession(run)
      for (const member of run) foldOf.set(member.id, fold.id)
      folded.push(fold)
    } else {
      folded.push(...run)
    }
    index = end
  }
  return folded
}

function sameHelperRun(left: Session | undefined, right: Session | undefined): boolean {
  if (!left || !right) return false
  return left.type === 'delegate' && right.type === 'delegate'
    && left.title === right.title
    && sessionKindLabel(left.type) === sessionKindLabel(right.type)
}

function foldSession(members: Session[]): Session {
  const ids = members.map((member) => member.id).sort()
  return markerSession(`${FOLD_ID_PREFIX}${ids.join(',')}`, `${members.length} similar helper runs`, members)
}

function markerSession(id: string, title: string, members: Session[]): Session {
  const sample = newest(members)
  return {
    id,
    agent_id: sample.agent_id,
    active_agent_id: sample.active_agent_id,
    title,
    type: 'chat',
    created_at: sample.created_at,
    updated_at: sample.updated_at,
    message_count: 0,
    workspace_id: sample.workspace_id,
    child_count: members.length,
  }
}

function newest(members: Session[]): Session {
  return members.reduce((best, candidate) => (
    Date.parse(candidate.updated_at) > Date.parse(best.updated_at) ? candidate : best
  ))
}

function revealHit(
  hitId: string,
  byId: Map<string, Session>,
  foldOf: Map<string, string>,
  autoExpandIds: Set<string>,
): void {
  let current: string | undefined = hitId
  const seen = new Set<string>()
  while (current && !seen.has(current)) {
    seen.add(current)
    const foldId = foldOf.get(current)
    if (foldId) autoExpandIds.add(foldId)
    const parentId: string | undefined = byId.get(current)?.parent_session_id
    if (!parentId) return
    if (!byId.has(parentId)) {
      autoExpandIds.add(`${MISSING_PARENT_ID_PREFIX}${parentId}`)
      return
    }
    autoExpandIds.add(parentId)
    current = parentId
  }
}

function indexByParent(sessions: Session[]): Map<string, Session[]> {
  const map = new Map<string, Session[]>()
  for (const session of sessions) {
    if (!session.parent_session_id) continue
    pushBucket(map, session.parent_session_id, session)
  }
  return map
}

function pushBucket(map: Map<string, Session[]>, key: string, session: Session): void {
  const existing = map.get(key)
  if (existing) existing.push(session)
  else map.set(key, [session])
}
