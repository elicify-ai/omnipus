// mailCacheView.ts — the Mail panel's cache-view adapter (W3 spec §2.1):
// per-surface display state fed by a cache-first read plus AT MOST ONE live
// refresh per eligible event. Pure functions over a plain state object — no
// React, no JSX, no timers — so the event/revision rules are testable
// without mounting the panel (U1–U5).
//
// The rules this module encodes (FR-W3-2; §3.1's frozen cache-view
// semantics; founder Q-C stale-gating):
//   - Issuing is governed by the PANEL-LOCAL absent-or-older-than-five-
//     minutes computation on open and folder switch; a manual Refresh and
//     the panel's own successful action ALWAYS refresh. A response's
//     stale/refresh_needed flags govern presentation only (grill R2-M3) —
//     never issuance.
//   - Every refresh is keyed by publication_revision: a response answering a
//     superseded request, or a second response for an already-applied
//     request, is dropped unrendered (MC-W3-3; US-1 AS-5; U2). Revisions are
//     opaque — compared for identity, never parsed (the generated field's
//     contract); request ordering is the client-side recency signal.
//   - A failed refresh preserves the cached rows and their ORIGINAL
//     last-validated time and sets the retry state; timestamps never
//     advance on failure (U4; US-2 AS-4).
//   - source=none (no cache) falls through to exactly one live read — once,
//     never a loop (U5; US-1 AS-4).
//   - No repeating timer exists anywhere in this module: state changes only
//     when an event function is called (MC-W3-10).

/** The founder-set staleness threshold (ADR P1.1): cache data validated
 * within the last five minutes is fresh — an open or folder switch on it
 * issues NO live request. */
export const MAIL_STALE_AFTER_MS = 5 * 60 * 1000

/** The four generated `MailReadMetadata.source` values (W0, register row 2).
 * not-wire-format: a mirrored union for view-state typing — every VALUE still
 * arrives on the generated object; nothing here re-declares the wire shape. */
export type MailViewSource = 'live' | 'memory' | 'encrypted_disk' | 'none'

/** Freshness projection of one generated `MailReadMetadata` instance (the
 * §2.4 mapping table's three instances feed this — never a fourth). */
export interface MailViewMeta {
  source: MailViewSource
  last_validated_at: string | null
  stale: boolean
  refresh_needed: boolean
  notice_code: 'cache_unavailable' | null
  publication_revision: string | null
}

/** Project a generated MailReadMetadata object into the view-state shape.
 * Accepts the generated type structurally; a missing metadata object (the
 * pre-producer wire window) becomes null at the caller, never here. */
export function mailViewMetaFrom(meta: {
  source: MailViewSource
  last_validated_at: string | null
  stale: boolean
  refresh_needed: boolean
  notice_code: 'cache_unavailable' | null
  publication_revision: string | null
}): MailViewMeta {
  return {
    source: meta.source,
    last_validated_at: meta.last_validated_at,
    stale: meta.stale,
    refresh_needed: meta.refresh_needed,
    notice_code: meta.notice_code,
    publication_revision: meta.publication_revision,
  }
}

/** The panel's eligible events (§3.1: open, folder switch, manual Refresh,
 * own successful action, one rediscovery after missing-folder). */
export type MailCacheEvent =
  | { kind: 'open' }
  | { kind: 'folder_switch' }
  | { kind: 'manual_refresh' }
  | { kind: 'own_action' }
  | { kind: 'rediscovery' }

/** Display state for ONE surface (the folder rail read, or one folder's
 * message list). `meta === null` means no read has applied yet — data is
 * ABSENT (the issuing rule's absent branch), never "checked, empty". */
export interface MailFolderViewState {
  meta: MailViewMeta | null
  /** Newest applied publication_revision (opaque; identity-compared). */
  revision: string | null
  /** Sequence number of the newest request issued for this surface. */
  issuedSeq: number
  /** Sequence number of the request whose response was last applied. */
  appliedSeq: number | null
  /** The event's one live refresh is in flight (the "Checking…" input). */
  checking: boolean
  /** The newest live refresh failed — stale rows stay, Retry shows, and the
   * displayed checked time remains the cached value (never advanced). */
  refreshFailed: boolean
}

export function createMailFolderView(): MailFolderViewState {
  return {
    meta: null,
    revision: null,
    issuedSeq: 0,
    appliedSeq: null,
    checking: false,
    refreshFailed: false,
  }
}

/**
 * shouldIssueLive — the issuing rule for the events that CARRY a freshness
 * decision (open, folder_switch): live only when the surface's data is
 * absent (a source=none read) or its last validation is older than five
 * minutes (or unknown-but-labelled — an unknown age cannot be proven
 * young). manual_refresh, own_action and rediscovery always refresh and
 * never consult this rule (FR-W3-2; US-1 AS-8).
 *
 * A response carrying NO metadata object at all (the transitional
 * pre-producer wire window — the is_knowledge_base precedent) is NOT
 * "absent data": the gateway had no cache to serve from, so its cache-first
 * read already ran live server-side, and a second live request would double
 * the open event's dial for no information. The presentation still renders
 * the unknown state ("Last checked unknown." — see
 * MAIL_UNKNOWN_PROVENANCE_META); only the issuance stays silent. Once the
 * producers land, metadata is always present and this branch is dead.
 */
export function shouldIssueLive(view: MailFolderViewState, event: MailCacheEvent, now: Date): boolean {
  if (event.kind === 'manual_refresh' || event.kind === 'own_action' || event.kind === 'rediscovery') {
    return true
  }
  const meta = view.meta
  if (meta === null) return false
  if (meta.source === 'none') return true
  if (meta.last_validated_at === null) return true
  const validatedAt = new Date(meta.last_validated_at)
  if (Number.isNaN(validatedAt.getTime())) return true
  return now.getTime() - validatedAt.getTime() > MAIL_STALE_AFTER_MS
}

/** The presentation fallback for a response with no metadata object
 * (transitional window): renders the unknown/stale presentation — "Last
 * checked unknown." — never "just checked", never a fabricated zero
 * (US-2 AS-7). not-wire-format: a display fill, not a claim about the
 * data's origin. */
export const MAIL_UNKNOWN_PROVENANCE_META: MailViewMeta = {
  source: 'memory',
  last_validated_at: null,
  stale: true,
  refresh_needed: false,
  notice_code: null,
  publication_revision: null,
}

/**
 * beginCacheRead — start an event's cache-first leg (open / folder switch).
 * Returns the next view and the request identity the caller must pass back
 * to onCacheReadSettled.
 */
export function beginCacheRead(view: MailFolderViewState): { view: MailFolderViewState; seq: number } {
  const seq = view.issuedSeq + 1
  return {
    view: { ...view, issuedSeq: seq },
    seq,
  }
}

/**
 * onCacheReadSettled — the cache-first response settled. Applies it (unless
 * superseded) and decides whether the SAME event now owes its one live
 * refresh: yes when the event is open/folder_switch AND the local rule says
 * the data is absent or stale (U5: exactly one follow-up live; a live leg
 * never cascades). `meta` is the projected response metadata, or null when
 * the response carried none (the pre-producer wire window renders unknown,
 * and the absent-data branch issues the live leg).
 */
export function onCacheReadSettled(
  view: MailFolderViewState,
  seq: number,
  meta: MailViewMeta | null,
  event: MailCacheEvent,
  now: Date,
): { view: MailFolderViewState; live: boolean } {
  // Superseded request (a newer event was issued while this was in flight):
  // the response is dropped unrendered and mutates nothing (MC-W3-3).
  if (seq !== view.issuedSeq) return { view, live: false }
  const applied: MailFolderViewState = {
    ...view,
    meta,
    revision: meta?.publication_revision ?? view.revision,
    appliedSeq: seq,
    // A settled cache read is never "checking"; a prior failure flag is
    // cleared by a successful read of any kind.
    checking: false,
    refreshFailed: false,
  }
  const live = shouldIssueLive(applied, event, now)
  if (!live) return { view: applied, live: false }
  return { view: beginLiveRefresh(applied).view, live: true }
}

/**
 * beginLiveRefresh — start one mode=live request (the event's live leg, a
 * manual Refresh, or an own-action refresh). Supersedes any in-flight
 * request: its seq becomes the only one whose response may render
 * (US-1 AS-5).
 */
export function beginLiveRefresh(view: MailFolderViewState): { view: MailFolderViewState; seq: number } {
  const seq = view.issuedSeq + 1
  return {
    view: {
      ...view,
      issuedSeq: seq,
      checking: true,
      refreshFailed: false,
    },
    seq,
  }
}

/**
 * onLiveSettled — a live response settled. Applies only when it answers the
 * NEWEST issued request and that request has not already applied a response
 * (U2: a superseded or duplicate response — including one carrying an older
 * publication_revision — mutates nothing). A fresh cache row is never
 * mistaken for a settled server check: the metadata decides the label, and
 * a mode=live request is a different request from a cache read by contract.
 */
export function onLiveSettled(
  view: MailFolderViewState,
  seq: number,
  meta: MailViewMeta,
): { view: MailFolderViewState; applied: boolean } {
  if (seq !== view.issuedSeq) return { view, applied: false }
  if (view.appliedSeq === seq) return { view, applied: false }
  return {
    view: {
      ...view,
      meta,
      revision: meta.publication_revision ?? view.revision,
      appliedSeq: seq,
      checking: false,
      refreshFailed: false,
    },
    applied: true,
  }
}

/**
 * onLiveFailed — the newest live refresh failed. The cached rows, their
 * metadata and the ORIGINAL last-validated time are preserved untouched
 * (timestamps never advance on failure); the retry state shows (U4;
 * US-2 AS-4). A superseded request's failure is ignored — the newest
 * request's outcome decides the surface.
 */
export function onLiveFailed(view: MailFolderViewState, seq: number): MailFolderViewState {
  if (seq !== view.issuedSeq) return view
  return {
    ...view,
    checking: false,
    refreshFailed: true,
  }
}

/**
 * markLocalMutation — the panel's own successful action (mark-seen, send,
 * draft save/discard) advances the local ordering state (U3): the next
 * issued request supersedes every response in flight, so a delayed response
 * carrying the pre-mutation view can never render. The adapter never
 * invents a revision value — the server's next response carries the new one.
 */
export function markLocalMutation(view: MailFolderViewState): MailFolderViewState {
  return {
    ...view,
    issuedSeq: view.issuedSeq + 1,
    checking: false,
  }
}
