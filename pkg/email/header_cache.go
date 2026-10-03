package email

// W2 bounded in-memory header cache and folder counts (spec
// mail-live-access-w2-discovery-and-cache-spec §3.9, §3.10, §3.11; ADR-20261001
// P1.1 founder-set numbers and the P2.2 memory row).
//
// Memory only, never disk: at most the newest 50 envelope+flag rows per role
// per mailbox, five-minute freshness (serving never resets a timestamp; a
// stale set is served immediately but labelled), entries dropped 30 minutes
// after the panel was last open (the clock starts at panel close; reads
// never extend retention), inside a 4 MiB global reusable-metadata budget
// (overflow is the typed visible refusal, never truncation), keyed by the
// full pair + generation + role — never by account alone. Publications are
// revision-fenced (§3.10): a write whose captured revision is no longer
// current publishes nothing. There is no timer anywhere: the retention sweep
// is evaluated lazily at access and issues zero IMAP commands; nothing
// refreshes on its own.
//
// Counts (§3.9 counts block; register row 21; founder Q-C=A) live in the
// same cache with their OWN freshness metadata, stale-gated per trigger;
// manual Refresh is a publication and is accepted at any age.

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Header-cache error classes (§4.1 freeze: the budget overflow is the typed
// ErrCacheBudgetExceeded with a visible live-only outcome).
var (
	// ErrCacheBudgetExceeded: the publication would push the global
	// reusable-metadata budget past 4 MiB. Refused whole — no truncated
	// fields, no partial publication, no eviction of live work.
	ErrCacheBudgetExceeded = errors.New("mail cache: reusable-metadata budget exceeded (live-only outcome)")
	// ErrCachePairRemoved: the pair was removed/disabled; late completions
	// cannot recreate cache state (§3.11 removal row).
	ErrCachePairRemoved = errors.New("mail cache: pair removed; late publications are refused")
)

// Metadata sources for the landed MailReadMetadata wire shape: one producer
// per freshness field set (§4.1). "encrypted_disk" is the folder mapping's
// Phase-1 source (cache_file.go), never the header cache's.
const (
	MetadataSourceMemory = "memory"
	MetadataSourceLive   = "live"
	MetadataSourceNone   = "none"
)

const (
	headerCacheMaxRows     = 50               // newest per role per mailbox (founder-set)
	headerCacheFreshness   = 5 * time.Minute  // older than this is stale (founder-set)
	headerCacheRetention   = 30 * time.Minute // dropped this long after the panel was last open
	headerCacheBudgetBytes = 4 << 20          // global reusable-metadata budget (founder Q3=A)
	countsMetadataOverhead = 96               // fixed bytes charged to the budget per counts entry
	mailRowFixedOverhead   = 160              // struct + slice headers + timestamp, per cached row
)

// Metadata is the freshness/availability record every Phase 1 read response
// carries (§4.1: one producer for the landed MailReadMetadata fields).
type Metadata struct {
	// Source is memory | live | none — where the served data came from.
	Source string
	// LastValidatedAt is the timestamp the data was last confirmed against
	// the server; serving never advances it (a failed refresh keeps it).
	LastValidatedAt *time.Time
	// Stale marks data older than the freshness threshold — always shown
	// labelled, never presented as fresh.
	Stale bool
	// RefreshNeeded marks data a trigger must refresh (stale, or absent).
	RefreshNeeded bool
	// PublicationRevision is the revision the data was produced under (§3.10).
	PublicationRevision Revision
	// NoticeCode carries the safe closed-class notice ("cache_unavailable")
	// when the cache layer could not serve.
	NoticeCode string
}

// Counts is the folder rail's counts snapshot (memory-only; §3.9 counts
// block). A count is never fabricated: an unknown count is absent state, not
// a zero.
type Counts struct {
	Total       int
	Unseen      int
	UIDValidity uint32
}

// HeaderCache is the bounded memory cache. All bounds are enforced inside;
// callers cannot store more than the caps allow.
type HeaderCache struct {
	mu         sync.Mutex
	now        func() time.Time
	currentRev func(Scope) Revision
	usedBytes  int
	pages      map[pageKey]cachedPage
	counts     map[Scope]cachedCounts
	closedAt   map[string]time.Time // pair ID → panel-close time; absent = panel open
	removed    map[string]bool      // tombstoned pair IDs (all generations)
}

type pageKey struct {
	scope Scope
	role  string
}

type cachedPage struct {
	rows        []MailRow
	validatedAt time.Time
	revision    Revision
	bytes       int
}

type cachedCounts struct {
	counts      Counts
	validatedAt time.Time
	revision    Revision
}

// NewHeaderCache builds the cache. now is the injected clock (the freshness/
// retention boundaries are discrete; real sleeps are never involved);
// currentRev is the current publication revision (register row 10 — the
// counter itself is the gateway's; the cache only compares).
func NewHeaderCache(now func() time.Time, currentRev func(Scope) Revision) *HeaderCache {
	return &HeaderCache{
		now:        now,
		currentRev: currentRev,
		pages:      make(map[pageKey]cachedPage),
		counts:     make(map[Scope]cachedCounts),
		closedAt:   make(map[string]time.Time),
		removed:    make(map[string]bool),
	}
}

// Put publishes the newest-unfiltered page for one role. The cache keeps
// exactly the newest 50 rows (by UID, newest first) and nothing else; the
// live page is the caller's business and is never shortened here. Rows are
// ordered newest-first IN THE CALLER'S SLICE (Put may reorder it in place —
// the caller's slice is the page's authoritative order afterwards; row
// CONTENTS are never modified). The whole publication is refused (typed
// error, nothing stored) when the captured revision is stale, the pair is
// removed, or the budget would overflow.
func (c *HeaderCache) Put(scope Scope, role string, captured Revision, rows []MailRow) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.removed[scope.PairID] {
		return ErrCachePairRemoved
	}
	if captured != c.currentRev(scope) {
		return fmt.Errorf("%w: captured revision %d is no longer current (§3.10)", ErrStalePublication, captured)
	}
	// Newest-first ordering, in the caller's slice (see doc comment), then the
	// stored copy is trimmed to the cache's own bound.
	sort.Slice(rows, func(i, j int) bool { return rows[i].UID > rows[j].UID })
	kept := make([]MailRow, len(rows))
	copy(kept, rows)
	if len(kept) > headerCacheMaxRows {
		kept = kept[:headerCacheMaxRows]
	}
	size := 0
	for _, r := range kept {
		size += mailRowBytes(r)
	}
	key := pageKey{scope: scope, role: role}
	oldBytes := 0
	if old, ok := c.pages[key]; ok {
		oldBytes = old.bytes
	}
	if c.usedBytes-oldBytes+size > headerCacheBudgetBytes {
		return fmt.Errorf("%w: %d bytes requested with %d already in the %d-byte budget — refused whole, never truncated", ErrCacheBudgetExceeded, size, c.usedBytes-oldBytes, headerCacheBudgetBytes)
	}
	c.usedBytes = c.usedBytes - oldBytes + size
	c.pages[key] = cachedPage{rows: kept, validatedAt: c.now(), revision: captured, bytes: size}
	return nil
}

// Get serves the cached newest-unfiltered page for one role, with its
// freshness metadata. Rows are served immediately whatever their age —
// display first — but a stale set is labelled Stale + RefreshNeeded and the
// recorded validation time is preserved. An expired-retention or removed
// entry is a clean miss; a dropped entry never resurrects.
func (c *HeaderCache) Get(scope Scope, role string) ([]MailRow, Metadata, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.removed[scope.PairID] {
		return nil, Metadata{Source: MetadataSourceNone}, false
	}
	if c.retentionExpiredLocked(scope.PairID, now) {
		c.dropPairEntriesLocked(scope.PairID)
	}
	page, ok := c.pages[pageKey{scope: scope, role: role}]
	if !ok {
		return nil, Metadata{Source: MetadataSourceNone}, false
	}
	stale := now.Sub(page.validatedAt) > headerCacheFreshness
	validated := page.validatedAt
	return append([]MailRow(nil), page.rows...), Metadata{
		Source:              MetadataSourceMemory,
		LastValidatedAt:     &validated,
		Stale:               stale,
		RefreshNeeded:       stale,
		PublicationRevision: page.revision,
	}, true
}

// PutCounts publishes the folder rail's counts. Manual Refresh is the
// explicit human action and is accepted at any age; the only refusals are a
// removed pair and a superseded revision.
func (c *HeaderCache) PutCounts(scope Scope, captured Revision, counts Counts) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.removed[scope.PairID] {
		return ErrCachePairRemoved
	}
	if captured != c.currentRev(scope) {
		return fmt.Errorf("%w: captured revision %d is no longer current (§3.10)", ErrStalePublication, captured)
	}
	// Account the replaced entry exactly as Put does (gate F3): a
	// republication replaces the scope's single counts entry, so its charge
	// is net-zero — the replaced entry's bytes come off before the new
	// entry's go on. Charging unconditionally leaked countsMetadataOverhead
	// bytes on EVERY refresh (a stale-gated counts trigger fires ~every five
	// minutes per open panel), stranding phantom charges in usedBytes that
	// no InvalidateFolder/drop could ever reclaim — weeks later the
	// founder-set budget exhausts and every header Put refuses with
	// ErrCacheBudgetExceeded for every mailbox until restart, a symptom
	// pointing nowhere near its cause.
	oldBytes := 0
	if _, ok := c.counts[scope]; ok {
		oldBytes = countsMetadataOverhead
	}
	c.usedBytes = c.usedBytes - oldBytes + countsMetadataOverhead
	c.counts[scope] = cachedCounts{counts: counts, validatedAt: c.now(), revision: captured}
	return nil
}

// GetCounts serves the stored counts with their own freshness metadata —
// the trigger's zero-live-dial face: the caller dials only when the metadata
// says stale. No timer advances anything between triggers.
func (c *HeaderCache) GetCounts(scope Scope) (Counts, Metadata, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.removed[scope.PairID] {
		return Counts{}, Metadata{Source: MetadataSourceNone}, false
	}
	if c.retentionExpiredLocked(scope.PairID, now) {
		c.dropPairEntriesLocked(scope.PairID)
	}
	cc, ok := c.counts[scope]
	if !ok {
		return Counts{}, Metadata{Source: MetadataSourceNone}, false
	}
	stale := now.Sub(cc.validatedAt) > headerCacheFreshness
	validated := cc.validatedAt
	return cc.counts, Metadata{
		Source:              MetadataSourceMemory,
		LastValidatedAt:     &validated,
		Stale:               stale,
		RefreshNeeded:       stale,
		PublicationRevision: cc.revision,
	}, true
}

// InvalidateFolder discards every cached header and count for the folder's
// old epoch before any new row may publish (§3.11 UIDVALIDITY row).
func (c *HeaderCache) InvalidateFolder(scope Scope, role string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if page, ok := c.pages[pageKey{scope: scope, role: role}]; ok {
		c.usedBytes -= page.bytes
		delete(c.pages, pageKey{scope: scope, role: role})
	}
	if _, ok := c.counts[scope]; ok {
		c.usedBytes -= countsMetadataOverhead
		delete(c.counts, scope)
	}
}

// MarkPairRemoved revokes the pair's cache ownership: everything stored for
// the pair (all generations) is dropped and late writes are refused
// (§3.11 removal row — late completions cannot recreate cache state).
func (c *HeaderCache) MarkPairRemoved(scope Scope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removed[scope.PairID] = true
	c.dropPairEntriesLocked(scope.PairID)
}

// PanelOpened marks the panel open for the pair: the retention clock is
// cleared going forward, and dropped entries stay dropped (a reopen never
// resurrects; the reopen fetches live).
func (c *HeaderCache) PanelOpened(scope Scope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.closedAt, scope.PairID)
}

// PanelClosed marks the panel closed: the retention clock starts now. No
// refresh of any kind happens while closed; the drop itself is evaluated
// lazily at the next access and issues zero IMAP commands.
func (c *HeaderCache) PanelClosed(scope Scope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closedAt[scope.PairID] = c.now()
}

// retentionExpiredLocked reports whether the pair's entries are past the
// post-close retention window. A panel that has never been observed closed
// never expires by elapsed time alone.
func (c *HeaderCache) retentionExpiredLocked(pairID string, now time.Time) bool {
	closedAt, ok := c.closedAt[pairID]
	return ok && now.Sub(closedAt) >= headerCacheRetention
}

// dropPairEntriesLocked removes every page and count stored for the pair.
// Dropped entries are permanent for the panel generation: a reopen must
// fetch live, never resurrect.
func (c *HeaderCache) dropPairEntriesLocked(pairID string) {
	for key, page := range c.pages {
		if key.scope.PairID == pairID {
			c.usedBytes -= page.bytes
			delete(c.pages, key)
		}
	}
	for scope := range c.counts {
		if scope.PairID == pairID {
			c.usedBytes -= countsMetadataOverhead
			delete(c.counts, scope)
		}
	}
}

// mailRowBytes charges a row's stored footprint against the global budget:
// the string fields' bytes plus a fixed per-row overhead. Subject and
// address bytes are never truncated to fit — a row that does not fit refuses
// the publication whole.
func mailRowBytes(r MailRow) int {
	n := mailRowFixedOverhead
	n += len(r.Subject) + len(r.MessageID) + len(r.From) + len(r.FromName) + len(r.ReplyTo)
	for _, s := range r.To {
		n += len(s) + 16
	}
	for _, s := range r.Cc {
		n += len(s) + 16
	}
	return n
}
