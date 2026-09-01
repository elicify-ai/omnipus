// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-067 W3 — the indexing lifecycle for knowledge-base mounts
// (FR-030..FR-034a, FR-038a, FR-039, FR-080).
//
// # What this file is
//
// pkg/knowledge can detect a collection, open an index for it, reconcile that
// index with the disk, search it and check it for drift. Until this file
// existed, nothing in the product ever CALLED any of it: the package compiled,
// its tests passed, and it was reachable from no binary
// (`go list -deps ./cmd/omnipus | grep pkg/knowledge` returned nothing). This
// file is the wiring, and it deliberately contains no indexing logic of its own
// — every decision about what a knowledge base is, where its index lives, what
// counts as changed and what counts as drift belongs to pkg/knowledge and stays
// there.
//
// # The four things it wires
//
//  1. MOUNT → INDEX. A workspace mount whose target is a knowledge base gets an
//     open index. Two mounts of one folder — in one workspace or several —
//     share ONE index, reference counted by the folder's resolved real path
//     (FR-031). pkg/knowledge already owns that keying (knowledge.OpenIndex is
//     itself the reference count); this file calls it once per mount and closes
//     once per revoke, so the count it keeps is the authority and this file
//     never second-guesses it.
//
//     BOOT-TIME ENUMERATION IS knowledge.ResolveScope, NOT THE MOUNT STORE
//     (R2, docs/internal/design/knowledge-tools-remediation.md). Before this
//     was true, AttachAllMounts read ONLY workspace.LoadMounts, which is a
//     narrower answer to "which collections exist" than the one every
//     knowledge tool (describe/read/find) already consults. A collection
//     living directly in a workspace's own work tree — the ONLY route
//     ADR-067 D11 ("workspace-first, movable") leaves available, since a work
//     tree can never become a mount (pkg/workspace/mount.go's
//     ErrMountRefused/CheckMountTarget, FR-7.5, refuses any target inside
//     $OMNIPUS_HOME) — was therefore permanently DISCOVERABLE by every tool
//     and permanently UNINDEXED, with no tool, REST endpoint or CLI verb able
//     to change that. AttachAllMounts now enumerates every workspace's
//     knowledge.ResolveScope(home, workspaceID).Collections() — the same
//     answer the tools use — so a collection any tool can address is
//     guaranteed indexable. This is a strict widening: ResolveScope's roots
//     are themselves built from workspace.AllowedMountRoots (the mount grant)
//     PLUS the work tree, so a mount-backed collection is still discovered
//     exactly as before and still flows through AttachMount/RevokeMount
//     keyed by mount name — see attachResolvedCollection's doc comment for
//     how a collection is routed to one path or the other, and why that
//     routing does not trust ScopedCollection.Origin's string value to make
//     the call.
//
//     A DOCUMENT IS NOT ONLY A NOTE (operator ruling O1, same design doc §7).
//     A vault folder holds PDFs, spreadsheets and other files alongside
//     notes, and those must be findable too — but full-text extraction from
//     them is deliberately NOT wanted (it would make indexing latency depend
//     on document size, to answer a question nobody asked). This file does
//     not need to do anything new to satisfy that: pkg/knowledge's existing
//     FR-039a "attachment" path (Scan classifies every non-.md/.markdown file
//     as ScanKindAttachment; Index.indexAttachment records its path, its
//     name tokens and its filename-stem TITLE, and reads not one byte of
//     content) already applies, unconditionally, to any collection this file
//     attaches — mount-backed or work-tree. pkg/knowledge never imports
//     pkg/docextract, so there is no code path here or in pkg/knowledge that
//     could regress into extracting a PDF's text. The admitted file set is
//     therefore "everything in the collection, with no extension filter" —
//     the same set describe/read already show an operator — and the title
//     for a document with no frontmatter is the filename stem (stemTitle),
//     exactly as it already is for an image or any other attachment. Size,
//     modification time and extension are already captured cheaply (Scan
//     never opens a file to record them) and already persist in the
//     manifest/ScanEntry for freshness and drift purposes; they are
//     deliberately NOT duplicated onto the bleve index document, because
//     indexDoc's mapping is closed by design (fields.go's "ONE PARSER, NOT
//     TWO" / D21.2 header) and name+title search is what O1 asked
//     for ("findable, and can be referred to") — a size/mtime/kind facet on
//     search results is a real, separable feature this ruling does not ask
//     for and this change does not add.
//
//  2. PROGRESS → WEBSOCKET. Indexing progress is a value that moves, so it is
//     pushed as a knowledge_index_progress frame rather than polled (FR-080).
//     The honesty rule is structural here, not a convention: total_files is
//     ABSENT unless total_known is true, and total_known is forced false while
//     enumerating. A client therefore cannot compute a ratio against a total
//     the server has not measured — see newKnowledgeIndexProgressFrame.
//
//  3. DRIFT → SCHEDULE. knowledge.HealthChecker runs the drift check once on
//     mount and every interval thereafter, in-process (FR-038a). There is no
//     button, and there is deliberately no method here that exposes one.
//
//  4. BROKEN MOUNT → HONEST FAILURE. A mount whose target has been moved or
//     deleted returns ErrKnowledgeMountUnreachable and attaches nothing. It does
//     not panic, and — the failure that actually matters — it does not attach an
//     empty index that would answer every search with "no results" as though the
//     collection were genuinely empty (FR-110, FR-111, US-16 AS-4/AS-5).
//
// # What this file does NOT do
//
// It does not register the knowledge tools with any agent, serve any REST
// endpoint, or render anything. Those are separate lanes.
//
// It also does not COMPOSE the drift notification — knowledge_drift_notify.go
// owns the wording and the recipients, and hands this file a plain
// func(knowledge.DriftReport) through KnowledgeLifecycleOptions.DriftNotify.
// That hook was nil in production until `Notification.type` gained the
// `knowledge_drift` member (contracts/components/schemas/Notification.yaml and
// NotificationFrame in contracts/asyncapi.yaml), because before that a drift
// notification would have been rejected by the SPA's generated zod guard and
// the operator told nothing while the code looked wired.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/vaultprops"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// knowledgeIndexProgressFrameType is the frame's discriminator, as declared by
// contracts/asyncapi.yaml (`const: knowledge_index_progress` on
// KnowledgeIndexProgressFrame).
//
// It is a literal here rather than a generated gen.WsFrameType constant because
// the value is MISSING from the WsFrameType enum in contracts/asyncapi.yaml —
// every other frame is listed there and this one is not, so no constant is
// generated for it. That is a contract gap, not a decision: adding
// `knowledge_index_progress` to that enum and regenerating is the fix, and it
// was left out of this change only because regenerating the contracts sweeps
// every other in-flight lane's generated output into one diff. The payload type
// itself is the generated gen.KnowledgeIndexProgressFrame, so Constraint #8
// holds: there is no hand-written wire struct anywhere in this file.
const knowledgeIndexProgressFrameType = "knowledge_index_progress"

// The four phases KnowledgeIndexProgressFrame.phase admits.
const (
	// knowledgeIndexPhaseEnumerating — walking the tree. The total is NOT known
	// and no total may be sent.
	knowledgeIndexPhaseEnumerating = "enumerating"
	// knowledgeIndexPhaseIndexing — parsing and writing documents against a
	// measured total.
	knowledgeIndexPhaseIndexing = "indexing"
	// knowledgeIndexPhaseIdle — terminal success: nothing in flight, the index
	// is current, the client should stop showing progress.
	knowledgeIndexPhaseIdle = "idle"
	// knowledgeIndexPhaseFailed — terminal failure, with the reason in `error`.
	// The client must surface it rather than leave a bar stalled forever.
	knowledgeIndexPhaseFailed = "failed"
)

// ErrKnowledgeMountUnreachable is returned when a mount's target cannot be
// listed — moved, deleted, or on an unmounted volume.
//
// It is an error and not a quiet "this is not a knowledge base" because those
// two states are indistinguishable to a caller that only gets a bool, and
// collapsing them is how a moved folder becomes a collection that silently
// searches as empty (FR-110/FR-111). knowledge.IsKnowledgeBase makes the same
// distinction for the same reason.
var ErrKnowledgeMountUnreachable = errors.New("gateway: knowledge mount target is unreachable")

// KnowledgeProgressEmitter delivers one progress frame to connected clients.
type KnowledgeProgressEmitter func(gen.KnowledgeIndexProgressFrame)

// knowledgeMountKey identifies one mount: a workspace and the mount's name
// within it. Two keys may resolve to the same collection root; that is exactly
// the case FR-031's reference counting exists for.
type knowledgeMountKey struct {
	workspaceID string
	name        string
}

// knowledgeCollection is one collection root and everything holding it open.
type knowledgeCollection struct {
	// root is the collection root's resolved real path — the identity FR-031
	// keys on.
	root string
	// collectionID is the opaque wire identifier derived from root.
	collectionID string
	// index is the shared handle. Every holder has the same pointer;
	// knowledge.OpenIndex returns it and counts the references itself.
	index *knowledge.Index
	// holders is every mount currently keeping this collection open. Its size
	// is this file's view of the reference count, used to decide when to stop
	// the drift schedule; knowledge.OpenIndex/Index.Close keeps the count that
	// actually decides when the file handle closes.
	holders map[knowledgeMountKey]struct{}
	// reconciled records that a first reconcile has already been run for this
	// collection, so a second mount of the same folder attaches to a live index
	// instead of re-indexing it.
	reconciled bool
	// resyncing is true while a drift-triggered re-index is in flight. FR-038a
	// allows at most one check per collection at a time and the repair it
	// triggers inherits that bound: without this flag a collection that stays
	// unhealthy would stack one re-index per tick forever.
	resyncing bool
	// lastReconcile is when this collection's most recent index run FINISHED.
	// It is what makes a drift-triggered repair safe: a report whose check
	// started before that instant was produced from a state the index has
	// already left, and repairing from it would re-index a collection that is
	// already current. AttachMount starts the drift schedule BEFORE the first
	// reconcile — deliberately, so a collection that never finishes indexing
	// is still watched — so the very first report on every mount is exactly
	// that stale kind: it sees an empty index and finds every file missing.
	lastReconcile time.Time
}

// workspaceIDs returns the distinct workspaces currently mounting this
// collection, sorted for a deterministic emission order.
func (c *knowledgeCollection) workspaceIDs() []string {
	seen := make(map[string]struct{}, len(c.holders))
	out := make([]string, 0, len(c.holders))
	for k := range c.holders {
		if _, dup := seen[k.workspaceID]; dup {
			continue
		}
		seen[k.workspaceID] = struct{}{}
		out = append(out, k.workspaceID)
	}
	sort.Strings(out)
	return out
}

// KnowledgeLifecycleOptions configures the lifecycle. Only Home is required.
type KnowledgeLifecycleOptions struct {
	// Home is $OMNIPUS_HOME. Every index lives underneath it (FR-030) and
	// never inside the operator's collection.
	Home string

	// Emit delivers progress frames. Nil disables emission entirely, which is
	// what a test that does not care about frames wants; production wires it to
	// the WebSocket broadcast.
	Emit KnowledgeProgressEmitter

	// DriftInterval is FR-038a's operator-configurable schedule. Zero means
	// knowledge.DefaultDriftInterval (six hours).
	DriftInterval time.Duration

	// DriftNotify receives every UNHEALTHY drift report and never a healthy one
	// — knowledge.HealthChecker enforces that, not this file. Nil logs a
	// structured warning instead.
	DriftNotify func(knowledge.DriftReport)

	// BaseContext bounds every background activity (the drift schedules and any
	// asynchronous attach). It must NOT be a request context: a mount created
	// over HTTP would otherwise have its drift schedule cancelled the moment the
	// response was written. Nil means context.Background().
	BaseContext context.Context

	// --- test seams. Production leaves every one of these nil. ---

	// OpenIndex overrides knowledge.OpenIndex.
	OpenIndex func(home, collectionRoot string) (*knowledge.Index, error)
	// Scan overrides knowledge.Scan — the enumeration walk that measures the
	// total for a first index.
	Scan func(root string) (*knowledge.ScanResult, error)
	// SyncWith overrides (*knowledge.Index).SyncWith.
	SyncWith func(ctx context.Context, ix *knowledge.Index, opts knowledge.SyncOptions) (knowledge.SyncStats, error)
	// PropsSync overrides vaultprops.Sync — the properties-index build/update
	// pass ADR-068 D16.2/D16.5 needs, run alongside the text-index reconcile.
	PropsSync func(ctx context.Context, home, collectionRoot string) (vaultprops.SyncStats, error)
	// DriftCheck overrides knowledge.CheckDrift.
	DriftCheck func(context.Context, *knowledge.Index) (knowledge.DriftReport, error)
	// NewTicker overrides the drift schedule's clock, so FR-038a's cadence is
	// asserted by counting runs against injected ticks rather than by sleeping.
	NewTicker func(time.Duration) (<-chan time.Time, func())
	// Now overrides the clock used for frame timestamps.
	Now func() time.Time
}

// KnowledgeLifecycle owns the mount→index lifecycle for one $OMNIPUS_HOME.
type KnowledgeLifecycle struct {
	home        string
	emitFn      KnowledgeProgressEmitter
	baseCtx     context.Context
	now         func() time.Time
	openIndex   func(home, root string) (*knowledge.Index, error)
	scan        func(root string) (*knowledge.ScanResult, error)
	syncWith    func(context.Context, *knowledge.Index, knowledge.SyncOptions) (knowledge.SyncStats, error)
	propsSync   func(ctx context.Context, home, collectionRoot string) (vaultprops.SyncStats, error)
	health      *knowledge.HealthChecker
	driftPeriod time.Duration

	mu     sync.Mutex
	byRoot map[string]*knowledgeCollection
	byKey  map[knowledgeMountKey]string // mount → collection root
	closed bool

	// attachWG lets Stop and tests wait for asynchronous attaches to finish
	// rather than racing them.
	attachWG sync.WaitGroup
	// pendingAttaches mirrors attachWG's counter under kl.mu. It exists solely
	// so WaitForAttaches can decide whether to call attachWG.Wait() at all
	// without ever calling it unsynchronized with an Add(): sync.WaitGroup's
	// own contract forbids a Wait() racing with an Add() that starts the
	// counter back up from zero, and every Add() below already happens under
	// kl.mu — but WaitForAttaches used to call attachWG.Wait() directly, with
	// no lock and no relationship to those Add() calls at all. Gating on this
	// counter turns that into an ordinary sequence: either the Add() this
	// mirrors already ran before WaitForAttaches looked (so it is safe to
	// wait — the Add() happened-before the read, via kl.mu), or it has not
	// run yet (so there is nothing yet to wait for, and skipping is correct:
	// a caller cannot be owed a wait for work that had not been registered
	// when it asked).
	pendingAttaches int
}

// NewKnowledgeLifecycle builds the lifecycle. It performs no I/O.
func NewKnowledgeLifecycle(opts KnowledgeLifecycleOptions) (*KnowledgeLifecycle, error) {
	if strings.TrimSpace(opts.Home) == "" {
		return nil, errors.New("gateway: knowledge lifecycle requires $OMNIPUS_HOME")
	}
	kl := &KnowledgeLifecycle{
		home:        opts.Home,
		emitFn:      opts.Emit,
		baseCtx:     opts.BaseContext,
		now:         opts.Now,
		openIndex:   opts.OpenIndex,
		scan:        opts.Scan,
		syncWith:    opts.SyncWith,
		propsSync:   opts.PropsSync,
		driftPeriod: opts.DriftInterval,
		byRoot:      make(map[string]*knowledgeCollection),
		byKey:       make(map[knowledgeMountKey]string),
	}
	if kl.baseCtx == nil {
		kl.baseCtx = context.Background()
	}
	if kl.now == nil {
		kl.now = time.Now
	}
	if kl.openIndex == nil {
		kl.openIndex = knowledge.OpenIndex
	}
	if kl.scan == nil {
		kl.scan = knowledge.Scan
	}
	if kl.syncWith == nil {
		kl.syncWith = func(ctx context.Context, ix *knowledge.Index, o knowledge.SyncOptions) (knowledge.SyncStats, error) {
			return ix.SyncWith(ctx, o)
		}
	}
	if kl.propsSync == nil {
		kl.propsSync = func(ctx context.Context, home, collectionRoot string) (vaultprops.SyncStats, error) {
			return vaultprops.Sync(ctx, home, collectionRoot, vaultprops.SyncOptions{})
		}
	}
	if kl.driftPeriod <= 0 {
		kl.driftPeriod = knowledge.DefaultDriftInterval
	}

	report := opts.DriftNotify
	if report == nil {
		report = func(r knowledge.DriftReport) {
			slog.Warn("knowledge: drift detected",
				"collection", r.Root, "findings", len(r.Findings), "summary", r.Summary())
		}
	}
	// DETECTING DRIFT AND REPAIRING NOTHING IS THE DEFECT, NOT THE FEATURE.
	//
	// The drift check answers "does this index still match the operator's
	// folder?". Before this wrapper existed the answer went to a log line and
	// the index was left exactly as stale as it was found, while
	// knowledge_search kept returning SearchReport.Complete=true — because
	// Complete is derived from the progress tracker ("no index run is in
	// flight"), which is idle for a stale index. So a note added to a mounted
	// vault after boot was unfindable until the process restarted, and the
	// search said, in words, that it had searched the whole collection. That is
	// the confidently-incomplete answer US-6 exists to make impossible.
	//
	// The repair runs AFTER the report, so an operator-supplied notify still
	// sees the unhealthy state that triggered it rather than a report about a
	// collection this code has already quietly fixed.
	notify := func(r knowledge.DriftReport) {
		report(r)
		kl.resyncAfterDrift(r)
	}
	health, err := knowledge.NewHealthChecker(knowledge.HealthCheckerOptions{
		Interval:  kl.driftPeriod,
		Notify:    notify,
		Check:     opts.DriftCheck,
		NewTicker: opts.NewTicker,
		OnError: func(root string, cErr error) {
			slog.Warn("knowledge: drift check failed", "collection", root, "error", cErr)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("gateway: knowledge health checker: %w", err)
	}
	kl.health = health
	return kl, nil
}

// DriftInterval is the effective schedule, after the zero-means-default rule.
func (kl *KnowledgeLifecycle) DriftInterval() time.Duration { return kl.driftPeriod }

// CollectionIDFor is the opaque wire identifier for a collection root
// (KnowledgeBaseInfo.collection_id, FR-031).
//
// It is the basename of the index directory knowledge.IndexDirFor chooses,
// which is itself derived from the root's RESOLVED REAL PATH. Deriving it that
// way rather than hashing the path here is deliberate: it makes "same
// collection_id" and "same index" the same statement by construction, so the
// two can never drift apart.
func (kl *KnowledgeLifecycle) CollectionIDFor(collectionRoot string) (string, error) {
	dir, err := knowledge.IndexDirFor(kl.home, collectionRoot)
	if err != nil {
		return "", err
	}
	return filepath.Base(dir), nil
}

// IndexForRoot returns the open index for a collection root, if one is
// attached. The root may be given by any spelling that resolves to it.
func (kl *KnowledgeLifecycle) IndexForRoot(collectionRoot string) (*knowledge.Index, bool) {
	realRoot, err := knowledge.ResolveCollectionRoot(collectionRoot)
	if err != nil {
		return nil, false
	}
	kl.mu.Lock()
	defer kl.mu.Unlock()
	c, ok := kl.byRoot[realRoot]
	if !ok {
		return nil, false
	}
	return c.index, true
}

// AttachedRoots lists the collection roots currently open, sorted.
func (kl *KnowledgeLifecycle) AttachedRoots() []string {
	kl.mu.Lock()
	defer kl.mu.Unlock()
	out := make([]string, 0, len(kl.byRoot))
	for root := range kl.byRoot {
		out = append(out, root)
	}
	sort.Strings(out)
	return out
}

// HoldersFor reports how many mounts currently hold a collection open.
func (kl *KnowledgeLifecycle) HoldersFor(collectionRoot string) int {
	realRoot, err := knowledge.ResolveCollectionRoot(collectionRoot)
	if err != nil {
		return 0
	}
	kl.mu.Lock()
	defer kl.mu.Unlock()
	if c, ok := kl.byRoot[realRoot]; ok {
		return len(c.holders)
	}
	return 0
}

// AttachMount opens (or joins) the index for one mount and reconciles it.
//
// It returns:
//   - nil and no attachment when the target is an ordinary folder — most mounts
//     are, and that is not an error;
//   - ErrKnowledgeMountUnreachable when the target cannot be listed at all;
//   - any other error when the index could not be opened or reconciled.
//
// It is synchronous, so a caller that must not block (an HTTP handler) should
// use AttachMountAsync. Attaching a mount that is already attached is a no-op.
func (kl *KnowledgeLifecycle) AttachMount(ctx context.Context, workspaceID, mountName, hostPath string) error {
	isKB, err := knowledge.IsKnowledgeBase(hostPath)
	if err != nil {
		// A target that cannot be listed is BROKEN, and the honest report of a
		// broken mount is an error naming it — never an attached, empty index,
		// which would answer searches with silence that reads as "you have no
		// notes about that".
		return fmt.Errorf("%w: %s: %w", ErrKnowledgeMountUnreachable, hostPath, err)
	}
	if !isKB {
		return nil
	}

	realRoot, err := knowledge.ResolveCollectionRoot(hostPath)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrKnowledgeMountUnreachable, hostPath, err)
	}

	collection, fresh, err := kl.acquire(workspaceID, mountName, realRoot)
	if err != nil || collection == nil {
		return err
	}
	if !fresh {
		// Another mount already opened and reconciled this collection. FR-031:
		// one folder, one index, one indexing run — not one per mount.
		return nil
	}

	// Index FIRST, then start the drift schedule.
	//
	// The order used to be the other way round, and it made the on-mount check
	// meaningless: it ran against an index the first reconcile had not written
	// yet, so it found every file "not_indexed" and reported a healthy, freshly
	// mounted collection as drifted. Harmless while the report only produced a
	// log line — and not harmless at all now that an unhealthy report triggers
	// a repair, because the repair would re-index every mount a second time.
	//
	// FR-038a's "once on mount" still holds: the check runs once per attach,
	// just after the thing it is checking exists. The Watch is started even
	// when the reconcile failed — a collection that could not be indexed is
	// exactly one an operator wants the schedule to keep reporting on.
	reconcileErr := kl.reconcile(ctx, collection)
	if wErr := kl.health.Watch(kl.baseCtx, collection.index); wErr != nil {
		slog.Warn("knowledge: drift schedule not started",
			"collection", collection.root, "error", wErr)
	}
	return reconcileErr
}

// AttachMountAsync runs AttachMount on the base context in the background and
// logs any failure. Used by the mount-create handler and by boot.
func (kl *KnowledgeLifecycle) AttachMountAsync(workspaceID, mountName, hostPath string) {
	// Nil-receiver-safe: the mount handlers call this unconditionally, and a
	// gateway booted without a knowledge lifecycle (every harness that does not
	// exercise knowledge bases) must still be able to create a mount.
	if kl == nil {
		return
	}
	kl.mu.Lock()
	if kl.closed {
		kl.mu.Unlock()
		return
	}
	kl.attachWG.Add(1)
	kl.pendingAttaches++
	kl.mu.Unlock()

	go func() {
		defer kl.attachWG.Done()
		defer func() {
			kl.mu.Lock()
			kl.pendingAttaches--
			kl.mu.Unlock()
		}()
		if err := kl.AttachMount(kl.baseCtx, workspaceID, mountName, hostPath); err != nil {
			slog.Warn("knowledge: mount not indexed",
				"workspace_id", workspaceID, "mount", mountName, "host_path", hostPath, "error", err)
		}
	}()
}

// RevokeMount releases one mount's hold on its collection.
//
// The last release stops the drift schedule and closes the index; any earlier
// release leaves both running, because another workspace is still searching it
// (US-16 AS-2). Revoking a mount that never held a knowledge base is a no-op.
func (kl *KnowledgeLifecycle) RevokeMount(workspaceID, mountName string) error {
	// Nil-receiver-safe, for the same reason as AttachMountAsync.
	if kl == nil {
		return nil
	}
	key := knowledgeMountKey{workspaceID: workspaceID, name: mountName}

	kl.mu.Lock()
	root, held := kl.byKey[key]
	if !held {
		kl.mu.Unlock()
		return nil
	}
	delete(kl.byKey, key)
	collection := kl.byRoot[root]
	if collection == nil {
		kl.mu.Unlock()
		return nil
	}
	delete(collection.holders, key)
	last := len(collection.holders) == 0
	if last {
		delete(kl.byRoot, root)
	}
	index := collection.index
	kl.mu.Unlock()

	if last {
		// Unwatch waits for a check in flight, so the index is never closed
		// underneath a running drift check.
		kl.health.Unwatch(root)
	}
	// One Close per AttachMount that opened a handle. knowledge.OpenIndex is
	// itself the reference count, so the underlying handle survives until the
	// last of them (FR-031).
	if err := index.Close(); err != nil {
		return fmt.Errorf("gateway: close knowledge index %s: %w", root, err)
	}
	return nil
}

// AttachCollection opens (or joins) the index for one collection and
// reconciles it, exactly as AttachMount does — the difference is entirely in
// what identifies the attachment.
//
// AttachMount is keyed by (workspaceID, mountName), because a mount is a
// named, revocable grant and RevokeMount must be able to find it again by
// that same name after the mount record itself is gone. A work-tree
// collection has no such name: nothing ever "revokes" it through a REST
// verb, and its own resolved root is already the identity FR-031 keys the
// index itself on (kl.byRoot). So this is keyed by (workspaceID,
// collectionRoot) directly — R2's "AttachMount's per-mount key becomes a
// per-collection key" for the one case that has no mount name to begin with.
//
// Two consequences worth being explicit about, both already true of
// AttachMount and unchanged in kind here:
//
//   - a collection reachable from ONE workspace by both a mount AND (this
//     cannot happen in practice today, since a mount can never resolve
//     inside $OMNIPUS_HOME — see attachResolvedCollection) the work tree
//     would hold two independent holders, one per key scheme, both pointing
//     at the SAME *knowledgeCollection via kl.byRoot. That shared identity is
//     what makes "attached once, not twice" true regardless of key scheme:
//     acquire's `fresh` return is false on the second call no matter which
//     key reached it, so the collection is reconciled once.
//   - calling this twice for the SAME (workspaceID, root) is the ordinary
//     idempotent case AttachMount already has (acquire's `existing == root`
//     branch): a no-op, not a second holder.
func (kl *KnowledgeLifecycle) AttachCollection(ctx context.Context, workspaceID, collectionRoot string) error {
	realRoot, err := knowledge.ResolveCollectionRoot(collectionRoot)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrKnowledgeMountUnreachable, collectionRoot, err)
	}

	collection, fresh, err := kl.acquire(workspaceID, realRoot, realRoot)
	if err != nil || collection == nil {
		return err
	}
	if !fresh {
		return nil
	}

	// Same ordering as AttachMount, for the same reason: index first, THEN
	// start the drift schedule, so the on-attach check runs against a
	// reconciled index rather than an empty one.
	reconcileErr := kl.reconcile(ctx, collection)
	if wErr := kl.health.Watch(kl.baseCtx, collection.index); wErr != nil {
		slog.Warn("knowledge: drift schedule not started",
			"collection", collection.root, "error", wErr)
	}
	return reconcileErr
}

// AttachCollectionAsync runs AttachCollection on the base context in the
// background and logs any failure. Used by the boot-time scope sweep for a
// collection that has no mount name (see attachResolvedCollection).
func (kl *KnowledgeLifecycle) AttachCollectionAsync(workspaceID, collectionRoot string) {
	// Nil-receiver-safe, for the same reason as AttachMountAsync.
	if kl == nil {
		return
	}
	kl.mu.Lock()
	if kl.closed {
		kl.mu.Unlock()
		return
	}
	kl.attachWG.Add(1)
	kl.pendingAttaches++
	kl.mu.Unlock()

	go func() {
		defer kl.attachWG.Done()
		defer func() {
			kl.mu.Lock()
			kl.pendingAttaches--
			kl.mu.Unlock()
		}()
		if err := kl.AttachCollection(kl.baseCtx, workspaceID, collectionRoot); err != nil {
			slog.Warn("knowledge: collection not indexed",
				"workspace_id", workspaceID, "collection_root", collectionRoot, "error", err)
		}
	}()
}

// AttachAllMounts attaches every collection reachable in every workspace
// under this $OMNIPUS_HOME — both a workspace's recorded mounts and any
// knowledge base living directly in its own work tree. This is the restart
// path: FR-039 requires an existing index to be reopened rather than
// rebuilt, and reopening is what makes that observable.
//
// See this file's header ("BOOT-TIME ENUMERATION IS knowledge.ResolveScope")
// for why the enumeration source changed from the mount store to
// knowledge.ResolveScope, and attachResolvedCollection's doc comment for how
// each discovered collection is routed to the mount-name-keyed path or the
// root-keyed path.
//
// Failures are logged per collection and never abort the sweep — one moved
// or unreadable collection must not stop the others from coming back.
func (kl *KnowledgeLifecycle) AttachAllMounts() {
	ids, err := workspace.ListWorkspaceIDs(kl.home)
	if err != nil {
		slog.Warn("knowledge: cannot list workspaces", "home", kl.home, "error", err)
		return
	}
	for _, workspaceID := range ids {
		kl.attachWorkspaceScope(workspaceID)
	}
}

// attachWorkspaceScope resolves one workspace's knowledge.ResolveScope and
// attaches every collection it names.
func (kl *KnowledgeLifecycle) attachWorkspaceScope(workspaceID string) {
	workDirReal := kl.resolvedWorkDir(workspaceID)
	scope := knowledge.ResolveScope(kl.home, workspaceID)
	for _, col := range scope.Collections() {
		kl.attachResolvedCollection(workspaceID, col, workDirReal)
	}
}

// resolvedWorkDir returns the workspace's own work tree, real-path-resolved,
// or "" when it cannot be resolved (an id that fails SafeWorkDir's own
// safety check, or a work tree that has not been created yet). "" can never
// match a collection root — every ScopedCollection.Root is itself
// real-path-resolved and non-empty (ResolveScope's own invariant) — so a
// resolution failure here can only ever route a collection to the
// mount-keyed path, never to the work-tree path it was not proven to be in.
func (kl *KnowledgeLifecycle) resolvedWorkDir(workspaceID string) string {
	workDir, err := workspace.SafeWorkDir(kl.home, workspaceID)
	if err != nil {
		return ""
	}
	real, err := knowledge.ResolveCollectionRoot(workDir)
	if err != nil {
		return ""
	}
	return real
}

// attachResolvedCollection routes one collection knowledge.ResolveScope
// returned to whichever attach path matches how it was granted.
//
// # Why routing does NOT trust col.Origin's string value
//
// ScopedCollection.Origin is either a mount's name or the literal constant
// knowledge.WorkTreeOrigin ("workspace") — and R3(c) of the remediation
// design already flags that those two are NOT reliably distinguishable by
// string comparison alone: nothing stops an operator naming a real mount
// "workspace" (ErrInvalidMountName forbids a path separator and the literal
// name "work", not this word), which would collide with the work-tree
// sentinel. Routing on that string here would misfile such a mount's
// collection onto the root-keyed path, silently orphaning it from
// RevokeMount's mount-name lookup — a mount the operator later deletes would
// never have its index closed.
//
// The reliable test is structural instead: a mount's target can never be
// inside $OMNIPUS_HOME at all (pkg/workspace/mount.go's ErrMountRefused,
// FR-7.5 — enforced at CreateMount, not re-checked here), so any collection
// root that IS the workspace's own work tree, or lies beneath it, cannot
// have come from a mount grant. It is compared against workDirReal, which
// the caller resolved via the exact same knowledge.ResolveCollectionRoot
// this collection's own Root was resolved through, so the comparison is
// between two values produced the same way rather than a raw-vs-resolved
// mismatch.
//
//   - Inside the work tree → attachCollectionAsync, keyed by the collection's
//     own root. There is no mount name to key on, and none is needed: a
//     work-tree collection is never revoked through the mount-delete REST
//     path, so there is no lookup this key must remain compatible with.
//   - Everywhere else → the EXISTING AttachMountAsync(workspaceID, col.Origin,
//     col.Root), unchanged from before this change. col.Origin is the
//     mount's Name here (ScopedCollection's own contract), so this is
//     byte-for-byte the same call boot used to make from the mount store
//     directly — RevokeMount(workspaceID, mountName) keeps finding it by the
//     same key it always has.
func (kl *KnowledgeLifecycle) attachResolvedCollection(workspaceID string, col knowledge.ScopedCollection, workDirReal string) {
	if workDirReal != "" && (col.Root == workDirReal || strings.HasPrefix(col.Root, workDirReal+string(filepath.Separator))) {
		kl.AttachCollectionAsync(workspaceID, col.Root)
		return
	}
	kl.AttachMountAsync(workspaceID, col.Origin, col.Root)
}

// WaitForAttaches blocks until every asynchronous attach registered by the
// time this is called has finished. Test support and shutdown ordering; it is
// never on a request path.
//
// It does NOT call attachWG.Wait() unconditionally. sync.WaitGroup forbids a
// Wait() racing with an Add() that starts the counter back up from zero, and
// AttachMountAsync/resyncAfterDrift's Add() calls happen on their own
// goroutines with no ordering relative to an arbitrary caller of this method
// — a caller could reach attachWG.Wait() before the Add() it was meant to
// wait for had even run, which is exactly the pattern go test -race flags
// (and rightly: WaitGroup gives no guarantee Wait() would have seen that
// work at all). Checking pendingAttaches under kl.mu first turns that into an
// ordinary sequence instead of a race: either the Add() already happened
// before this read (so attachWG.Wait() is safe to call — the Add()
// happened-before the read, via kl.mu), or it has not happened yet (so there
// is nothing yet to wait for, and returning immediately is correct: nothing
// was pending at the moment this was asked).
func (kl *KnowledgeLifecycle) WaitForAttaches() {
	kl.mu.Lock()
	pending := kl.pendingAttaches > 0
	kl.mu.Unlock()
	if pending {
		kl.attachWG.Wait()
	}
}

// Stop ends every drift schedule and closes every open index. Idempotent.
func (kl *KnowledgeLifecycle) Stop() {
	kl.mu.Lock()
	if kl.closed {
		kl.mu.Unlock()
		return
	}
	kl.closed = true
	kl.mu.Unlock()

	kl.attachWG.Wait()
	kl.health.Stop()

	kl.mu.Lock()
	collections := make([]*knowledgeCollection, 0, len(kl.byRoot))
	for _, c := range kl.byRoot {
		collections = append(collections, c)
	}
	kl.byRoot = make(map[string]*knowledgeCollection)
	kl.byKey = make(map[knowledgeMountKey]string)
	kl.mu.Unlock()

	for _, c := range collections {
		// One Close per holder, matching one OpenIndex per attach.
		for range c.holders {
			if err := c.index.Close(); err != nil {
				slog.Warn("knowledge: close index", "collection", c.root, "error", err)
			}
		}
	}
}

// acquire registers one mount against a collection, opening the index on the
// first holder. fresh is true only for the holder that opened it.
func (kl *KnowledgeLifecycle) acquire(workspaceID, mountName, realRoot string) (c *knowledgeCollection, fresh bool, err error) {
	key := knowledgeMountKey{workspaceID: workspaceID, name: mountName}

	kl.mu.Lock()
	if kl.closed {
		kl.mu.Unlock()
		return nil, false, errors.New("gateway: knowledge lifecycle is stopped")
	}
	if existing, held := kl.byKey[key]; held {
		kl.mu.Unlock()
		if existing != realRoot {
			// The same mount name now points at a different folder. Release the
			// old collection before taking the new one, or the old index leaks.
			if rErr := kl.RevokeMount(workspaceID, mountName); rErr != nil {
				return nil, false, rErr
			}
			return kl.acquire(workspaceID, mountName, realRoot)
		}
		return nil, false, nil // already attached: no-op
	}
	kl.mu.Unlock()

	// OpenIndex is called OUTSIDE the lock: it creates directories and opens a
	// bolt file, and holding a gateway-wide mutex across that would serialize
	// every mount behind the slowest disk.
	index, err := kl.openIndex(kl.home, realRoot)
	if err != nil {
		return nil, false, fmt.Errorf("gateway: open knowledge index %s: %w", realRoot, err)
	}
	collectionID, err := kl.CollectionIDFor(realRoot)
	if err != nil {
		_ = index.Close()
		return nil, false, err
	}

	kl.mu.Lock()
	if kl.closed {
		kl.mu.Unlock()
		_ = index.Close()
		return nil, false, errors.New("gateway: knowledge lifecycle is stopped")
	}
	c, existing := kl.byRoot[realRoot]
	if !existing {
		c = &knowledgeCollection{
			root:         realRoot,
			collectionID: collectionID,
			index:        index,
			holders:      make(map[knowledgeMountKey]struct{}),
		}
		kl.byRoot[realRoot] = c
	}
	c.holders[key] = struct{}{}
	kl.byKey[key] = realRoot
	fresh = !c.reconciled
	c.reconciled = true
	kl.mu.Unlock()
	return c, fresh, nil
}

// resyncAfterDrift re-indexes a collection an unhealthy drift report named.
//
// It is a no-op when the collection is not attached, when the lifecycle is
// stopped, or when a resync for the same collection is already in flight. It
// runs on the BASE context, never a request context, for the same reason the
// drift schedule does: this work outlives whatever caused it to be scheduled.
//
// Failures are logged and swallowed. A collection that cannot be re-indexed is
// already reporting drift on every tick, which is the louder signal; turning
// the repair's failure into a second error channel would say the same thing
// twice and stop neither.
func (kl *KnowledgeLifecycle) resyncAfterDrift(report knowledge.DriftReport) {
	realRoot, err := knowledge.ResolveCollectionRoot(report.Root)
	if err != nil {
		// A root that no longer resolves is a moved or deleted collection. The
		// drift report itself is the honest signal for that; re-indexing a path
		// that is gone would only manufacture a second failure.
		return
	}

	kl.mu.Lock()
	if kl.closed {
		kl.mu.Unlock()
		return
	}
	c := kl.byRoot[realRoot]
	if c == nil || c.resyncing {
		kl.mu.Unlock()
		return
	}
	// SUPERSEDED REPORTS REPAIR NOTHING. If an index run finished after this
	// check started, the check described a state that no longer exists — most
	// often the empty index the on-mount check sees while the first reconcile
	// is still running. Re-indexing on it would double every mount's work and,
	// worse, would make an unhealthy collection re-index on a loop.
	if !report.CheckedAt.After(c.lastReconcile) {
		kl.mu.Unlock()
		return
	}
	c.resyncing = true
	kl.attachWG.Add(1)
	kl.pendingAttaches++
	kl.mu.Unlock()

	go func() {
		defer kl.attachWG.Done()
		defer func() {
			kl.mu.Lock()
			c.resyncing = false
			kl.pendingAttaches--
			kl.mu.Unlock()
		}()
		if rErr := kl.reconcile(kl.baseCtx, c); rErr != nil {
			slog.Warn("knowledge: drift resync failed", "collection", c.root, "error", rErr)
		}
	}()
}

// reconcile runs one index build and reports its progress.
//
// TWO SHAPES, ONE REASON. A FIRST index walks the tree here to measure a real
// total, then hands the same tree to the indexer: the extra walk is bought back
// many times over by the parse it precedes, and it is the only way this file
// can put a measured denominator on the wire (FR-036 forbids an invented one).
// An INCREMENTAL reconcile does NOT get that second walk: MV-4 budgets two
// seconds for the whole freshness check of 100,000 unchanged files, and
// doubling its walk to decorate a bar that will be gone in a moment is the
// wrong trade. It runs through knowledge.SyncTracked instead, which reports an
// honestly indeterminate state throughout.
func (kl *KnowledgeLifecycle) reconcile(ctx context.Context, c *knowledgeCollection) error {
	tracker := knowledge.SharedProgressTracker(c.root)
	incremental := kl.manifestExists(c)

	// Announce the run before any work: a client that connects mid-build must
	// see an indeterminate state immediately, not silence followed by a total.
	kl.emit(c, knowledgeIndexUpdate{Phase: knowledgeIndexPhaseEnumerating})

	var (
		stats knowledge.SyncStats
		err   error
	)
	if incremental {
		stats, err = knowledge.SyncTracked(ctx, c.index, tracker, knowledge.SyncOptions{})
	} else {
		stats, err = kl.firstIndex(ctx, c, tracker)
	}

	update := knowledgeIndexUpdate{
		Phase:      knowledgeIndexPhaseIdle,
		Indexed:    stats.Indexed + stats.Unchanged,
		Total:      stats.Scanned,
		TotalKnown: true,
		Skipped:    len(stats.Problems),
		HasSkipped: true,
		Err:        err,
	}
	kl.emit(c, update)

	// ADR-068 D16.2/D16.5 — keep the derived PROPERTIES index in step with the
	// same reconcile that just moved the text index. It runs after the text
	// sync (never before, and never in place of it): typed record retrieval
	// is additive on top of plain-word search, and a properties-index failure
	// here must not make the mount attach fail or the collection stop being
	// text-searchable — RequirePropertyIndex's own posture ("plain-word
	// search and knowledge_read still work") applies here too, so this is
	// logged and swallowed rather than folded into reconcile's own error.
	//
	// It runs UNCONDITIONALLY, not gated on `incremental`: vaultprops.Sync
	// does its own diff against what the store already holds (there is no
	// separate "first build" branch to choose between — see sync.go's
	// header), so the same call is correct on a brand-new mount and on the
	// ten-thousandth reconcile of an old one.
	if _, perr := kl.propsSync(ctx, kl.home, c.root); perr != nil && !errors.Is(perr, records.ErrPropertyIndexUnavailable) {
		// The platform-unavailable case (errors.Is match above) is not logged
		// again here: records.RequirePropertyIndex already logged its own WARN,
		// by name, at the point Sync asked the question. Logging it a second
		// time here would say the same thing twice under a different message,
		// which is the kind of noise that makes a real failure easy to miss.
		slog.Warn("knowledge: properties index sync failed; typed record queries may be stale or refuse",
			"collection", c.root, "error", perr)
	}

	// Stamp the completion instant so a drift report produced from the state
	// this run has just replaced cannot trigger a redundant repair. Stamped on
	// failure too: a run that failed still moved the index, and a report from
	// before it is no more current for having failed.
	kl.mu.Lock()
	c.lastReconcile = kl.now()
	kl.mu.Unlock()

	if err != nil {
		return fmt.Errorf("gateway: index knowledge base %s: %w", c.root, err)
	}
	return nil
}

// firstIndex is the measured-total path: enumerate, publish the total, index.
//
// It is also the ONLY path that can move a number while it works, which is why
// the running report is wired here and not in reconcile: an incremental run has
// no measured denominator (see reconcile), and a count that rises against no
// total is not progress, it is a number nobody can read.
//
// The running report goes through knowledge.SyncOptions.OnProgress, whose
// call rate the indexer itself bounds (at most one call per 200ms AND per 0.1%
// of the collection, plus a final one). This file therefore does no throttling
// of its own: one callback becomes one frame per mounting workspace, and the
// worst case for a 100,000-file collection is ~1,001 callbacks rather than
// 100,000. Adding a second throttle here would only make the two disagree.
func (kl *KnowledgeLifecycle) firstIndex(
	ctx context.Context, c *knowledgeCollection, tracker *knowledge.ProgressTracker,
) (knowledge.SyncStats, error) {
	tracker.BeginEnumeration(false)

	scan, err := kl.scan(c.root)
	if err != nil {
		tracker.Finish(false)
		return knowledge.SyncStats{}, err
	}
	total := len(scan.Entries)
	tracker.SetFound(total)
	tracker.BeginIndexing(total)

	if total > 0 {
		// Only now is a total honest, and only now may one be sent.
		// BeginIndexing(0) returns the tracker to idle rather than entering a
		// phase whose only rendering would be "0 of 0", so an empty collection
		// deliberately never produces an indexing frame at all.
		kl.emit(c, kl.updateFromProgress(tracker.Progress()))
	}

	stats, syncErr := kl.syncWith(ctx, c.index, knowledge.SyncOptions{
		OnProgress: kl.progressReporter(c, tracker),
	})
	if syncErr == nil {
		tracker.SetIndexed(stats.Indexed + stats.Unchanged)
	}
	tracker.Finish(stats.Scanned == 0)
	return stats, syncErr
}

// progressReporter is the running report a first index publishes as it works:
// it advances the shared tracker and pushes one frame per mounting workspace.
//
// THE DENOMINATOR COMES FROM THE TRACKER, NOT FROM THE CALLBACK, and the
// callback's own total is deliberately discarded. The indexer walks the tree a
// second time, so its total can differ from the one this collection's clients
// were already given — a file created between the two walks is enough. Swapping
// denominators mid-run would make an on-screen ratio jump backwards, which
// reads as a bug even when both numbers are true. The tracker clamps the count
// to the total it published, so the bar can sit at full while the last few
// stragglers finish, and reconcile's terminal frame — which carries the
// indexer's own measured Scanned — is what corrects the total. That frame is
// the authority on the final numbers; this one is the authority on movement.
//
// It emits nothing unless the tracker is actually in the indexing phase. An
// "idle" frame arriving mid-run would tell a client the work had stopped.
func (kl *KnowledgeLifecycle) progressReporter(
	c *knowledgeCollection, tracker *knowledge.ProgressTracker,
) func(indexed, total int) {
	return func(indexed, _ int) {
		tracker.SetIndexed(indexed)
		p := tracker.Progress()
		if p.Phase != knowledge.IndexPhaseIndexing {
			return
		}
		kl.emit(c, kl.updateFromProgress(p))
	}
}

// manifestExists reports whether this collection has been indexed before. A
// manifest on disk is the same signal knowledge.SyncTracked uses to decide a
// run is a reconcile rather than a first index.
func (kl *KnowledgeLifecycle) manifestExists(c *knowledgeCollection) bool {
	_, err := os.Stat(c.index.ManifestPath())
	return err == nil
}

// knowledgeIndexUpdate is the internal shape a progress frame is built from.
// It exists so the frame's honesty rule lives in exactly one function.
type knowledgeIndexUpdate struct {
	Phase      string
	Indexed    int
	Total      int
	TotalKnown bool
	Skipped    int
	HasSkipped bool
	Err        error
}

// updateFromProgress converts pkg/knowledge's progress snapshot into an update.
//
// TotalKnown is ANDed with Total > 0 here, mirroring IndexProgress.Ratio's own
// rule: a known total of zero is not a denominator, and "0 of 0" must not be
// constructible from anything this file emits.
func (kl *KnowledgeLifecycle) updateFromProgress(p knowledge.IndexProgress) knowledgeIndexUpdate {
	u := knowledgeIndexUpdate{
		Indexed:    p.Indexed,
		Total:      p.Total,
		TotalKnown: p.TotalKnown && p.Total > 0,
	}
	switch p.Phase {
	case knowledge.IndexPhaseEnumerating:
		u.Phase = knowledgeIndexPhaseEnumerating
	case knowledge.IndexPhaseIndexing:
		u.Phase = knowledgeIndexPhaseIndexing
	default:
		u.Phase = knowledgeIndexPhaseIdle
	}
	return u
}

// newKnowledgeIndexProgressFrame builds the wire frame.
//
// THE HONESTY RULE LIVES HERE, and it is enforced twice on purpose:
//
//   - total_files is written if and only if total_known is true, so the two can
//     never disagree;
//   - while enumerating, total_known is forced FALSE and total_files dropped,
//     whatever the caller passed. A future caller that mistakenly hands an
//     optimistic total to an enumerating frame produces an indeterminate frame,
//     not a ratio against a number nobody measured (FR-036).
//
// A client that computed a percentage from a total it did not have would show a
// confidently wrong number — the single failure ADR-067 refuses everywhere.
func newKnowledgeIndexProgressFrame(
	collectionID, workspaceID string, u knowledgeIndexUpdate, now time.Time,
) gen.KnowledgeIndexProgressFrame {
	indexed := u.Indexed
	if indexed < 0 {
		indexed = 0
	}
	f := gen.KnowledgeIndexProgressFrame{
		Type:         knowledgeIndexProgressFrameType,
		CollectionId: collectionID,
		WorkspaceId:  workspaceID,
		Phase:        u.Phase,
		IndexedFiles: int64(indexed),
		TotalKnown:   u.TotalKnown && u.Total > 0,
	}
	if u.Err != nil {
		f.Phase = knowledgeIndexPhaseFailed
		msg := u.Err.Error()
		f.Error = &msg
	}
	if f.Phase == knowledgeIndexPhaseEnumerating {
		f.TotalKnown = false
	}
	if f.TotalKnown {
		total := int64(u.Total)
		f.TotalFiles = &total
	}
	if u.HasSkipped {
		skipped := int64(u.Skipped)
		if skipped < 0 {
			skipped = 0
		}
		f.SkippedFiles = &skipped
	}
	ts := now.UTC().Format(time.RFC3339Nano)
	f.UpdatedAt = &ts
	return f
}

// emit sends one frame per workspace currently mounting this collection.
//
// One frame per WORKSPACE, not one per collection, because a client that filters
// by the workspace it is showing would otherwise never see the progress of a
// collection whose frame carries some other workspace's id. The frames share a
// collection_id, which is the key the contract tells clients to deduplicate on,
// so a client showing the collection twice still updates both.
func (kl *KnowledgeLifecycle) emit(c *knowledgeCollection, u knowledgeIndexUpdate) {
	if kl.emitFn == nil {
		return
	}
	kl.mu.Lock()
	workspaces := c.workspaceIDs()
	kl.mu.Unlock()

	now := kl.now()
	for _, ws := range workspaces {
		kl.emitFn(newKnowledgeIndexProgressFrame(c.collectionID, ws, u, now))
	}
}

// --- process-wide registry -------------------------------------------------

// knowledgeLifecycles maps $OMNIPUS_HOME to its lifecycle.
//
// It is a package variable rather than a field on restAPI for one reason: the
// three call sites that need it — boot, mount create and mount delete — live in
// files this change does not own, and adding a field to restAPI would put this
// lane's edit in the middle of a struct several other lanes are editing at the
// same time. The key is $OMNIPUS_HOME, which is the process's data root, so a
// test with its own temp home gets its own lifecycle and none of the leakage a
// singleton would cause. Moving this to a restAPI field later is a mechanical
// change to registerKnowledgeLifecycle and knowledgeLifecycle().
var knowledgeLifecycles = struct {
	mu     sync.Mutex
	byHome map[string]*KnowledgeLifecycle
}{byHome: make(map[string]*KnowledgeLifecycle)}

// registerKnowledgeLifecycle publishes a lifecycle for a home directory,
// replacing (and stopping) any previous one.
func registerKnowledgeLifecycle(home string, kl *KnowledgeLifecycle) {
	knowledgeLifecycles.mu.Lock()
	prev := knowledgeLifecycles.byHome[home]
	knowledgeLifecycles.byHome[home] = kl
	knowledgeLifecycles.mu.Unlock()
	if prev != nil && prev != kl {
		prev.Stop()
	}
}

// unregisterKnowledgeLifecycle removes and stops the lifecycle for a home.
func unregisterKnowledgeLifecycle(home string) {
	knowledgeLifecycles.mu.Lock()
	kl := knowledgeLifecycles.byHome[home]
	delete(knowledgeLifecycles.byHome, home)
	knowledgeLifecycles.mu.Unlock()
	if kl != nil {
		kl.Stop()
	}
}

// stopKnowledgeLifecycles stops every registered lifecycle. Called from the
// gateway's shutdown path.
func stopKnowledgeLifecycles() {
	knowledgeLifecycles.mu.Lock()
	all := make([]*KnowledgeLifecycle, 0, len(knowledgeLifecycles.byHome))
	for _, kl := range knowledgeLifecycles.byHome {
		all = append(all, kl)
	}
	knowledgeLifecycles.byHome = make(map[string]*KnowledgeLifecycle)
	knowledgeLifecycles.mu.Unlock()
	for _, kl := range all {
		kl.Stop()
	}
}

// knowledgeLifecycle returns the lifecycle for this API's home, or nil.
// Every caller must tolerate nil: a gateway booted without one (every test
// harness that does not exercise knowledge bases) still creates and deletes
// mounts, and must not crash doing it.
func (a *restAPI) knowledgeLifecycle() *KnowledgeLifecycle {
	if a == nil || a.homePath == "" {
		return nil
	}
	knowledgeLifecycles.mu.Lock()
	defer knowledgeLifecycles.mu.Unlock()
	return knowledgeLifecycles.byHome[a.homePath]
}

// startKnowledgeLifecycle builds the lifecycle for a booting gateway, publishes
// it, and attaches every mount already on disk (FR-039's reopen-don't-rebuild).
//
// wsh may be nil, in which case progress is computed and dropped rather than
// broadcast — a gateway with no WebSocket handler still indexes.
// WHAT AN OPERATOR ACTUALLY SEES WHEN DRIFT IS FOUND, stated plainly:
//
//   - They see the collection RE-INDEX. NewKnowledgeLifecycle wraps DriftNotify
//     so an unhealthy report triggers resyncAfterDrift, and that re-index emits
//     knowledge_index_progress frames the SPA already routes into the Library's
//     knowledge panel. So the visible consequence is real and is the one that
//     matters: the stale answers stop.
//   - They now also get a NOTIFICATION in the header bell, in words rather than
//     field names — driftNotify is built by knowledgeDriftNotifier
//     (knowledge_drift_notify.go) and passed in by gateway.go. A nil driftNotify
//     restores the previous behaviour (a slog.Warn and nothing else), which is
//     what every test harness that boots no notification store gets.
//   - There is still no CONFIG KEY for the interval: gateway.go passes a
//     literal 0, which NewKnowledgeLifecycle turns into FR-038a's six-hour
//     default. FR-038a says "operator-configurable"; that half is owed.
func startKnowledgeLifecycle(
	homePath string,
	wsh *WSHandler,
	driftInterval time.Duration,
	driftNotify func(knowledge.DriftReport),
) *KnowledgeLifecycle {
	opts := KnowledgeLifecycleOptions{
		Home:          homePath,
		DriftInterval: driftInterval,
		DriftNotify:   driftNotify,
	}
	if wsh != nil {
		opts.Emit = wsh.broadcastKnowledgeIndexProgress
	}
	kl, err := NewKnowledgeLifecycle(opts)
	if err != nil {
		slog.Error("knowledge: lifecycle not started", "error", err)
		return nil
	}
	registerKnowledgeLifecycle(homePath, kl)
	kl.AttachAllMounts()
	return kl
}

// --- WebSocket delivery ----------------------------------------------------

// broadcastKnowledgeIndexProgress sends one knowledge_index_progress frame to
// every connected client (FR-080).
//
// Best-effort, exactly like broadcastToolApprovalRequired: a client whose send
// buffer is full misses this frame and is corrected by the next one — the
// terminal idle/failed frame is always sent, so a client can never be left
// showing a bar that stopped moving because a frame was dropped.
func (h *WSHandler) broadcastKnowledgeIndexProgress(frame gen.KnowledgeIndexProgressFrame) {
	if h == nil {
		return
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		slog.Error("ws: marshal knowledge_index_progress", "error", err)
		return
	}

	h.mu.Lock()
	conns := make([]*wsConn, 0, len(h.sessions))
	for _, wc := range h.sessions {
		conns = append(conns, wc)
	}
	h.mu.Unlock()

	for _, wc := range conns {
		select {
		case wc.sendCh <- raw:
		default:
			slog.Warn("ws: knowledge_index_progress dropped — send buffer full",
				"collection_id", frame.CollectionId)
			wc.droppedFrames.Add(1)
		}
	}
}
