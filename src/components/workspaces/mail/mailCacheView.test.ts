// W3 RED pack — U1–U5 (spec §8.1, file name per spec §8.1's table).
//
// Oracle source: docs/internal/specs/mail-live-access-w3-panel-and-settings-spec.md —
// §3.1's frozen cache-view semantics, §4 US-1 (AS-1/2/4/5/6/8), MC-W3-2/MC-W3-3,
// §7 scenarios 1.1/1.4/1.5/1.7, FR-W3-2. Every expected value below was derived
// from the spec BEFORE this module was read (receipts/w3-red-derivation.md).
// The unit is the real mailCacheView module — pure functions, zero mocks:
// no network edge exists to mock (the module does no I/O).
import { describe, it, expect } from 'vitest'
import {
  MAIL_STALE_AFTER_MS,
  mailViewMetaFrom,
  createMailFolderView,
  shouldIssueLive,
  beginCacheRead,
  onCacheReadSettled,
  beginLiveRefresh,
  onLiveSettled,
  onLiveFailed,
  markLocalMutation,
  type MailViewMeta,
} from './mailCacheView'

const NOW = new Date('2026-10-02T10:00:00Z')

const iso = (msBeforeNow: number): string => new Date(NOW.getTime() - msBeforeNow).toISOString()

/** D-1-style metadata projections. Values are the spec's, not the code's. */
const meta = (over: Partial<MailViewMeta> = {}): MailViewMeta => ({
  source: 'memory',
  last_validated_at: iso(40_000),
  stale: false,
  refresh_needed: false,
  notice_code: null,
  publication_revision: 'rev-1',
  ...over,
})

describe('mailCacheView — the §3.1 cache-view semantics (U1–U5)', () => {
  describe('U1 — live refresh is stale-gated (MC-W3-2, US-1 AS-8, scenario 1.1/1.7)', () => {
    it('issues no live request when the cache was validated 40 seconds ago (scenario 1.1)', () => {
      const view = createMailFolderView()
      const applied = { ...view, meta: meta({ last_validated_at: iso(40_000) }) }
      expect(shouldIssueLive(applied, { kind: 'open' }, NOW)).toBe(false)
      const started = beginCacheRead(applied)
      const settled = onCacheReadSettled(started.view, started.seq, applied.meta, { kind: 'open' }, NOW)
      expect(settled.live).toBe(false)
    })

    it('issues exactly one live request when the cache is 7 minutes stale (scenario 1.7)', () => {
      const stale = meta({ last_validated_at: iso(7 * 60_000), stale: true, refresh_needed: true })
      const view = { ...createMailFolderView(), meta: stale }
      expect(shouldIssueLive(view, { kind: 'open' }, NOW)).toBe(true)
      const started = beginCacheRead(view)
      const settled = onCacheReadSettled(started.view, started.seq, stale, { kind: 'open' }, NOW)
      expect(settled.live).toBe(true)
      // The one live leg never cascades: settling it issues no second live.
      const liveApplied = onLiveSettled(
        settled.view,
        settled.view.issuedSeq,
        meta({ source: 'live', last_validated_at: NOW.toISOString() }),
      )
      expect(liveApplied.applied).toBe(true)
      expect(shouldIssueLive(liveApplied.view, { kind: 'open' }, NOW)).toBe(false)
    })

    it('issues the live leg when the data is absent (source=none — scenario 1.4)', () => {
      const absent = meta({ source: 'none', last_validated_at: null })
      const view = { ...createMailFolderView(), meta: absent }
      const started = beginCacheRead(view)
      const settled = onCacheReadSettled(started.view, started.seq, absent, { kind: 'open' }, NOW)
      expect(settled.live).toBe(true)
    })

    it('always refreshes for manual_refresh, own_action and rediscovery, even when fresh (US-1 AS-6/AS-8)', () => {
      const fresh = meta({ last_validated_at: iso(1_000) })
      const view = { ...createMailFolderView(), meta: fresh }
      expect(shouldIssueLive(view, { kind: 'manual_refresh' }, NOW)).toBe(true)
      expect(shouldIssueLive(view, { kind: 'own_action' }, NOW)).toBe(true)
      expect(shouldIssueLive(view, { kind: 'rediscovery' }, NOW)).toBe(true)
    })

    // Boundary derivation from the spec's "older than five minutes" (US-1,
    // FR-W3-2): an age of exactly five minutes is NOT older — only ages
    // strictly beyond the threshold issue the live leg.
    it('treats exactly five minutes as fresh and one second beyond as stale (the boundary)', () => {
      const exactlyAt = meta({ last_validated_at: iso(MAIL_STALE_AFTER_MS) })
      const oneSecondUnder = meta({ last_validated_at: iso(MAIL_STALE_AFTER_MS - 1_000) })
      const oneSecondOver = meta({ last_validated_at: iso(MAIL_STALE_AFTER_MS + 1_000) })
      expect(shouldIssueLive({ ...createMailFolderView(), meta: oneSecondUnder }, { kind: 'open' }, NOW)).toBe(false)
      expect(shouldIssueLive({ ...createMailFolderView(), meta: exactlyAt }, { kind: 'open' }, NOW)).toBe(false)
      expect(shouldIssueLive({ ...createMailFolderView(), meta: oneSecondOver }, { kind: 'open' }, NOW)).toBe(true)
    })

    // Spec §3.1: the panel "issues that live request ONLY when the folder's
    // data is absent or its last validation is older than five minutes".
    // A null validation time is neither absent (rows are present — the
    // source is not `none`) nor KNOWN to be older than five minutes, and
    // §7 scenario 2.6 pins "(no refresh follows)" for exactly this input
    // (source=live, last_validated_at=null, refresh_needed=false). D-1 row 6
    // and scenario 2.5 describe an ALREADY-in-flight refresh (their Given),
    // not one this rule starts — the two readings only reconcile this way.
    it('issues no live leg for a present, null-stamped response (scenario 2.6: no refresh follows)', () => {
      const liveUnknownTime = meta({
        source: 'live',
        last_validated_at: null,
        stale: false,
        refresh_needed: false,
      })
      const view = { ...createMailFolderView(), meta: liveUnknownTime }
      const started = beginCacheRead(view)
      const settled = onCacheReadSettled(started.view, started.seq, liveUnknownTime, { kind: 'open' }, NOW)
      expect(settled.live).toBe(false)
    })

    // CHARACTERIZATION TEST (no independent oracle): the transitional window
    // where a response carries NO metadata object at all. US-2 AS-7 pins the
    // PRESENTATION (unknown, never "just checked") but the spec does not pin
    // whether such a read issues the live leg; the implementation chose "no
    // dial" (the gateway's cache-first read already ran live server-side).
    // This test pins that choice so it cannot drift silently; it is not
    // spec verification.
    it('[characterization] issues no live leg for a metadata-less response', () => {
      const view = createMailFolderView()
      const started = beginCacheRead(view)
      const settled = onCacheReadSettled(started.view, started.seq, null, { kind: 'open' }, NOW)
      expect(settled.live).toBe(false)
    })
  })

  describe('U2 — a superseded response is dropped unrendered (MC-W3-3, US-1 AS-5)', () => {
    it('drops a cache response that answers a superseded request and mutates nothing', () => {
      const view = { ...createMailFolderView(), meta: meta() }
      const first = beginCacheRead(view)
      // A newer event supersedes seq 1 before it settles.
      const second = beginCacheRead(first.view)
      expect(second.seq).toBe(first.seq + 1)
      const late = onCacheReadSettled(second.view, first.seq, meta({ publication_revision: 'rev-9' }), { kind: 'folder_switch' }, NOW)
      expect(late.live).toBe(false)
      expect(late.view).toBe(second.view) // identity: nothing mutated
    })

    it('drops a live response that answers a superseded request, even arriving last (US-1 AS-5)', () => {
      const view = { ...createMailFolderView(), meta: meta() }
      const first = beginCacheRead(view)
      const second = beginCacheRead(first.view)
      const newest = onLiveSettled(second.view, second.seq, meta({ publication_revision: 'rev-2', source: 'live' }))
      expect(newest.applied).toBe(true)
      const staleArrival = onLiveSettled(newest.view, first.seq, meta({ publication_revision: 'rev-0', source: 'memory' }))
      expect(staleArrival.applied).toBe(false)
      expect(staleArrival.view).toBe(newest.view)
      expect(staleArrival.view.revision).toBe('rev-2')
    })

    it('applies a live response only once — the duplicate settle is a no-op', () => {
      const view = { ...createMailFolderView(), meta: meta() }
      const live = beginLiveRefresh(view)
      const liveMeta = meta({ source: 'live', publication_revision: 'rev-2' })
      const firstApply = onLiveSettled(live.view, live.seq, liveMeta)
      expect(firstApply.applied).toBe(true)
      const secondApply = onLiveSettled(firstApply.view, live.seq, liveMeta)
      expect(secondApply.applied).toBe(false)
    })
  })

  describe('U3 — the panel’s own mutation supersedes in-flight responses (§8.1 U3)', () => {
    it('a mutation bumps the ordering state so a delayed pre-mutation response cannot render', () => {
      const view = { ...createMailFolderView(), meta: meta() }
      const live = beginLiveRefresh(view)
      const mutated = markLocalMutation(live.view)
      expect(mutated.issuedSeq).toBe(live.view.issuedSeq + 1)
      const late = onLiveSettled(mutated, live.seq, meta({ source: 'live', publication_revision: 'rev-3' }))
      expect(late.applied).toBe(false)
      expect(late.view).toBe(mutated)
    })

    it('never invents a revision value — the revision changes only when a response applies', () => {
      const view = createMailFolderView()
      expect(view.revision).toBeNull()
      const afterMutation = markLocalMutation(view)
      expect(afterMutation.revision).toBeNull()
      const live = beginLiveRefresh(afterMutation)
      const applied = onLiveSettled(live.view, live.seq, meta({ publication_revision: 'rev-7' }))
      expect(applied.view.revision).toBe('rev-7')
    })
  })

  describe('U4 — a failed refresh preserves rows and the ORIGINAL checked time (US-2 AS-4, scenario 2.2)', () => {
    it('keeps the cached metadata and its last-validated time; sets the retry state', () => {
      const cached = meta({ last_validated_at: iso(7 * 60_000), stale: true, refresh_needed: true })
      const view = { ...createMailFolderView(), meta: cached, revision: 'rev-1' }
      const live = beginLiveRefresh(view)
      expect(live.view.checking).toBe(true)
      const failed = onLiveFailed(live.view, live.seq)
      expect(failed.refreshFailed).toBe(true)
      expect(failed.checking).toBe(false)
      // The displayed time is the cache's original value — never advanced.
      expect(failed.meta).toBe(cached)
      expect(failed.meta?.last_validated_at).toBe(iso(7 * 60_000))
      expect(failed.revision).toBe('rev-1')
    })

    it('ignores a superseded request’s failure — the newest request decides the surface', () => {
      const view = { ...createMailFolderView(), meta: meta() }
      const first = beginLiveRefresh(view)
      const second = beginLiveRefresh(first.view)
      const failed = onLiveFailed(second.view, first.seq)
      expect(failed.refreshFailed).toBe(false)
      expect(failed.checking).toBe(true) // the newest live leg is still in flight
    })
  })

  describe('U5 — source=none falls through to exactly one live read, never a loop (scenario 1.4)', () => {
    it('one cache read yields one live leg; the superseded duplicate is dropped', () => {
      const absent = meta({ source: 'none', last_validated_at: null, refresh_needed: true })
      const view = createMailFolderView()
      const started = beginCacheRead(view)
      const settled = onCacheReadSettled(started.view, started.seq, absent, { kind: 'open' }, NOW)
      expect(settled.live).toBe(true)
      // While that live leg is in flight, a late/duplicate cache settle for
      // the SAME seq cannot start a second one.
      const duplicate = onCacheReadSettled(settled.view, started.seq, absent, { kind: 'open' }, NOW)
      expect(duplicate.live).toBe(false)
    })

    it('projects a generated metadata object losslessly (the three landed instances feed this)', () => {
      const raw = {
        source: 'encrypted_disk' as const,
        last_validated_at: iso(26 * 60 * 60_000),
        stale: true,
        refresh_needed: true,
        notice_code: 'cache_unavailable' as const,
        publication_revision: 'rev-mapping',
      }
      const projected = mailViewMetaFrom(raw)
      expect(projected).toEqual(raw)
    })
  })
})
