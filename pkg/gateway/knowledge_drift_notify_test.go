// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-067 FR-038a — the drift notification, tested against the requirement
// rather than against the implementation.
//
// FR-038a says two things about reporting, and both are asserted here:
//
//	"It MUST report only when something is wrong; a healthy run produces no
//	 notification."
//
// plus the founder's condition on the wording: a drift notification means the
// index disagreed with the operator's folder and is being put right, and it
// has to say that in words someone who is not an engineer can read.
//
// The expectations below are derived from those sentences. Where the wording
// is checked, the assertions are on MEANING (the folder is named; the files are
// said to be untouched; the repair is described) and on the ABSENCE of
// machine vocabulary — not on an exact sentence, which would be a snapshot of
// the implementation wearing a test's clothes.

package gateway

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/notifications"
)

// driftEmitRecorder records the live pushes instead of sending them.
type driftEmitRecorder struct {
	mu   sync.Mutex
	sent []agent.NotificationPayload
}

func (d *driftEmitRecorder) EmitNotification(p agent.NotificationPayload) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sent = append(d.sent, p)
}

func (d *driftEmitRecorder) all() []agent.NotificationPayload {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]agent.NotificationPayload, len(d.sent))
	copy(out, d.sent)
	return out
}

func driftCfgWithUser(username string) func() *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.Users = []config.UserConfig{{Username: username}}
	return func() *config.Config { return cfg }
}

func unhealthyDriftReport(root string) knowledge.DriftReport {
	return knowledge.DriftReport{
		Root:      root,
		CheckedAt: time.Now(),
		Findings: []knowledge.DriftFinding{
			{Kind: knowledge.DriftNotIndexed, Path: "notes/new.md"},
			{Kind: knowledge.DriftNotIndexed, Path: "notes/newer.md"},
			{Kind: knowledge.DriftStaleContent, Path: "notes/edited.md"},
		},
	}
}

// TestDriftNotificationType_IsAMemberOfTheContractEnum is the whole of
// DEFECT 1 in one assertion.
//
// A notification whose `type` is not in Notification.type's enum is rejected by
// the SPA's generated zod guard and vanishes: no bell item, no error, no log on
// the client. So emitting "knowledge_drift" is only meaningful if the contract
// admits it, and the two must be checked together — the Go constant alone
// proves nothing.
//
// DIES ON: removing `knowledge_drift` from
// contracts/components/schemas/Notification.yaml and regenerating; renaming the
// notifications.TypeKnowledgeDrift constant's value.
func TestDriftNotificationType_IsAMemberOfTheContractEnum(t *testing.T) {
	assert.True(t, gen.NotificationType(notifications.TypeKnowledgeDrift).Valid(),
		"notifications.TypeKnowledgeDrift (%q) is not a member of the generated "+
			"Notification.type enum. A notification carrying it is dropped by the SPA's "+
			"zod guard and the operator is told nothing — which is the defect this "+
			"change exists to remove, not a cosmetic mismatch",
		notifications.TypeKnowledgeDrift)
}

// TestDriftNotify_HealthyReportProducesNoNotification — FR-038a, verbatim:
// "It MUST report only when something is wrong; a healthy run produces no
// notification."
//
// A bell that pings every six hours to say nothing is wrong is a bell an
// operator learns to ignore, and then misses the one that mattered.
//
// DIES ON: deleting the `if r.Healthy() { return }` guard in
// knowledgeDriftNotifier.
func TestDriftNotify_HealthyReportProducesNoNotification(t *testing.T) {
	dir := t.TempDir()
	store := notifications.NewStore(filepath.Join(dir, "notifications"))
	rec := &driftEmitRecorder{}

	notify := knowledgeDriftNotifier(store, rec, driftCfgWithUser("dana"))
	require.NotNil(t, notify)

	notify(knowledge.DriftReport{Root: filepath.Join(dir, "vault"), CheckedAt: time.Now()})

	assert.Empty(t, rec.all(),
		"a healthy drift report must push nothing (FR-038a)")
	list, err := store.ListForUser("dana")
	require.NoError(t, err)
	assert.Empty(t, list,
		"a healthy drift report must persist nothing (FR-038a)")
}

// TestDriftNotify_UnhealthyReportReachesTheBell — the positive half.
//
// The report must become a real notification-centre item: persisted for the
// account so it survives a reload, AND pushed live so the bell moves now.
//
// DIES ON: leaving KnowledgeLifecycleOptions.DriftNotify nil in production;
// dropping either the store.Create or the EmitNotification call.
func TestDriftNotify_UnhealthyReportReachesTheBell(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "vault")
	store := notifications.NewStore(filepath.Join(dir, "notifications"))
	rec := &driftEmitRecorder{}

	notify := knowledgeDriftNotifier(store, rec, driftCfgWithUser("dana"))
	require.NotNil(t, notify)
	notify(unhealthyDriftReport(root))

	list, err := store.ListForUser("dana")
	require.NoError(t, err)
	require.Len(t, list, 1, "the drift report must be persisted for the account")
	assert.Equal(t, notifications.TypeKnowledgeDrift, list[0].Type)
	assert.Equal(t, notifications.SeverityWarning, list[0].Severity)
	assert.NotEmpty(t, list[0].Title)

	sent := rec.all()
	require.Len(t, sent, 1, "the drift report must also be pushed live")
	assert.Equal(t, "dana", sent[0].Recipient)
	assert.Equal(t, notifications.TypeKnowledgeDrift, sent[0].NotificationType)
	assert.Equal(t, list[0].ID, sent[0].ID,
		"the live push and the stored row must be the same item, or marking the "+
			"bell item read reaches nothing")
	assert.Positive(t, sent[0].CreatedAtMs,
		"created_at_ms is required on the wire; a zero renders as 1970 in the bell")
}

// TestDriftNotify_PushedFrameCarriesAnID — NotificationFrame.id is required and
// minLength 1 on the wire, so an empty id is dropped by the same zod guard this
// change exists to get past. The id must therefore survive the paths where
// persistence did not supply one: no store at all, and no account to persist to.
//
// DIES ON: removing the `if stored.ID == ""` mint in knowledgeDriftNotifier.
func TestDriftNotify_PushedFrameCarriesAnID(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")

	t.Run("no notification store", func(t *testing.T) {
		rec := &driftEmitRecorder{}
		notify := knowledgeDriftNotifier(nil, rec, driftCfgWithUser("dana"))
		require.NotNil(t, notify)
		notify(unhealthyDriftReport(root))
		sent := rec.all()
		require.Len(t, sent, 1)
		assert.NotEmpty(t, sent[0].ID)
	})

	t.Run("no accounts configured", func(t *testing.T) {
		dir := t.TempDir()
		store := notifications.NewStore(filepath.Join(dir, "notifications"))
		rec := &driftEmitRecorder{}
		emptyCfg := func() *config.Config { return &config.Config{} }
		notify := knowledgeDriftNotifier(store, rec, emptyCfg)
		require.NotNil(t, notify)
		notify(unhealthyDriftReport(root))
		sent := rec.all()
		require.Len(t, sent, 1)
		assert.Equal(t, agent.NotificationAdminBroadcast, sent[0].Recipient,
			"with no accounts the alert must still reach whoever is connected")
		assert.NotEmpty(t, sent[0].ID)
	})
}

// TestDriftNotify_SaysWhatHappenedInWordsAPersonReads — the honesty condition.
//
// Four things the text must do, each for a stated reason:
//   - name the folder, because an operator may have several knowledge bases;
//   - count the findings in English, because "3 not_indexed" is not a sentence;
//   - say the operator's own files were NOT changed, because "drift was found
//     in your knowledge base" otherwise reads as damage to the notes;
//   - describe the repair as under way, because at the moment the text is
//     written resyncAfterDrift has not run and can still fail — "was repaired"
//     would be a prediction dressed as a fact.
//
// And one thing it must NOT do: leak the machine vocabulary. DriftReport.Summary()
// renders "3 not_indexed, 1 stale_content"; that belongs in a log line.
//
// DIES ON: swapping knowledgeDriftMessage's body for r.Summary(); dropping the
// "not changed" reassurance; claiming the index "was repaired".
func TestDriftNotify_SaysWhatHappenedInWordsAPersonReads(t *testing.T) {
	root := filepath.Join(t.TempDir(), "team-vault")
	title, body := knowledgeDriftMessage(unhealthyDriftReport(root))

	assert.Contains(t, title, "team-vault",
		"the title must name the folder — an operator may have several")

	lower := strings.ToLower(body)
	assert.Contains(t, body, root, "the body must name the folder it is about")
	assert.Contains(t, lower, "2 files",
		"two not_indexed findings must be counted in English, not left as a kind name")
	assert.Contains(t, lower, "1 file",
		"one stale_content finding must be counted in English")
	assert.Contains(t, lower, "your files were not changed",
		"the operator must be told their own notes were untouched; without it "+
			"'drift' reads as damage to the folder")
	assert.Contains(t, lower, "re-reading the folder",
		"the body must say the repair is under way")

	for _, jargon := range []string{"not_indexed", "stale_content", "missing_from_disk", "DriftReport"} {
		assert.NotContains(t, body, jargon,
			"the notification is read by the person who owns the folder, not the "+
				"person who wrote the indexer: %q must not appear", jargon)
	}

	assert.NotContains(t, lower, "was repaired",
		"the repair runs asynchronously AFTER this text is composed and can fail; "+
			"claiming it is done is a prediction, not a report")
}

// TestDriftNotify_PhrasesEveryDriftKind — an unrecognised kind must still be
// COUNTED, not silently omitted. A body that quietly drops a category tells the
// operator a smaller story than the truth.
//
// DIES ON: returning "" from knowledgeDriftPhrase's default branch.
func TestDriftNotify_PhrasesEveryDriftKind(t *testing.T) {
	kinds := []knowledge.DriftKind{
		knowledge.DriftNotIndexed,
		knowledge.DriftMissingFromDisk,
		knowledge.DriftStaleContent,
		knowledge.DriftDocumentCount,
		knowledge.DriftManifestUnusable,
		knowledge.DriftUnreadable,
		knowledge.DriftPendingRename,
		knowledge.DriftKind("a_kind_invented_after_this_test"),
	}
	for _, k := range kinds {
		phrase := knowledgeDriftPhrase(k, 2)
		assert.NotEmpty(t, phrase, "kind %q renders as nothing", k)
		assert.NotContains(t, phrase, string(k)+" ",
			"kind %q leaks its machine name into the sentence", k)
	}
}

// TestDriftNotify_ThroughTheRealLifecycle is the end-to-end half of FR-038a:
// not "my guard returns early", but "a scheduled check on a real collection
// reaches — or does not reach — the notification centre".
//
// Both directions are driven through the production notifier attached to a real
// KnowledgeLifecycle over a real vault, so the healthy case fails if EITHER
// knowledge.HealthChecker starts calling Notify for a healthy report OR the
// notifier's own guard is removed, and the unhealthy case fails if any link in
// the chain — check → notify → persist → push — is broken.
//
// DIES ON: leaving DriftNotify nil; removing either Healthy() gate; dropping
// the store.Create or the EmitNotification call.
func TestDriftNotify_ThroughTheRealLifecycle(t *testing.T) {
	t.Run("healthy collection notifies nobody", func(t *testing.T) {
		root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
		home := kltHome(t)
		store := notifications.NewStore(filepath.Join(home, "notifications"))
		rec := &driftEmitRecorder{}

		var mu sync.Mutex
		runs := 0
		kl := kltLifecycle(t, KnowledgeLifecycleOptions{
			Home:        home,
			DriftNotify: knowledgeDriftNotifier(store, rec, driftCfgWithUser("dana")),
			DriftCheck: func(_ context.Context, ix *knowledge.Index) (knowledge.DriftReport, error) {
				mu.Lock()
				runs++
				mu.Unlock()
				return knowledge.DriftReport{Root: ix.Root(), CheckedAt: time.Now()}, nil
			},
			NewTicker: func(time.Duration) (<-chan time.Time, func()) {
				return make(chan time.Time), func() {}
			},
		})
		require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))

		// Without this the assertions below would pass on a check that never ran.
		require.Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return runs >= 1
		}, 2*time.Second, 5*time.Millisecond, "the on-mount drift check must run")

		// Never, not Empty. `runs` is incremented INSIDE the check, so it goes
		// true a moment BEFORE the notify path would run — a bare assert.Empty
		// here races the very thing it is testing and passes even with both
		// healthy-gates deleted. (It did. That is why this reads the way it
		// does.) Never gives the wrong behaviour a real window to appear in.
		require.Never(t, func() bool {
			return len(rec.all()) > 0
		}, 500*time.Millisecond, 10*time.Millisecond,
			"a healthy collection must produce no notification, however often it is checked")

		list, err := store.ListForUser("dana")
		require.NoError(t, err)
		assert.Empty(t, list)
	})

	t.Run("drifted collection reaches the bell", func(t *testing.T) {
		root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
		home := kltHome(t)
		store := notifications.NewStore(filepath.Join(home, "notifications"))
		rec := &driftEmitRecorder{}

		kl := kltLifecycle(t, KnowledgeLifecycleOptions{
			Home:        home,
			DriftNotify: knowledgeDriftNotifier(store, rec, driftCfgWithUser("dana")),
			DriftCheck: func(_ context.Context, ix *knowledge.Index) (knowledge.DriftReport, error) {
				return knowledge.DriftReport{
					Root:      ix.Root(),
					CheckedAt: time.Now(),
					Findings: []knowledge.DriftFinding{
						{Kind: knowledge.DriftMissingFromDisk, Path: "a.md"},
					},
				}, nil
			},
			NewTicker: func(time.Duration) (<-chan time.Time, func()) {
				return make(chan time.Time), func() {}
			},
		})
		require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))

		require.Eventually(t, func() bool {
			return len(rec.all()) == 1
		}, 2*time.Second, 5*time.Millisecond,
			"one unhealthy on-mount check must reach the notification centre exactly once")

		sent := rec.all()[0]
		assert.Equal(t, notifications.TypeKnowledgeDrift, sent.NotificationType)
		assert.Equal(t, "dana", sent.Recipient)
		assert.Contains(t, sent.Body, root, "the notification must name the folder")

		list, err := store.ListForUser("dana")
		require.NoError(t, err)
		require.Len(t, list, 1, "it must survive a reload, not only the live push")
		assert.Equal(t, sent.ID, list[0].ID)
	})
}

// TestDriftNotify_NilDependenciesFallBackToTheLog — every gateway test harness
// that boots no notification store must keep working exactly as before, and
// must not panic. A nil return is the signal NewKnowledgeLifecycle already
// understands (it substitutes its slog.Warn).
//
// DIES ON: returning a non-nil closure that dereferences a nil store or emitter.
func TestDriftNotify_NilDependenciesFallBackToTheLog(t *testing.T) {
	assert.Nil(t, knowledgeDriftNotifier(nil, nil, nil),
		"with nothing to deliver through, the notifier must be nil so the "+
			"lifecycle falls back to its structured log")

	// And a lifecycle built that way still constructs and stops cleanly.
	kl, err := NewKnowledgeLifecycle(KnowledgeLifecycleOptions{
		Home:        t.TempDir(),
		DriftNotify: knowledgeDriftNotifier(nil, nil, nil),
	})
	require.NoError(t, err)
	kl.Stop()
}

// TestDriftNotify_BootActuallyPassesTheNotifier is the guard against the exact
// failure this whole change exists to remove: code that LOOKS wired.
//
// Every test above builds the notifier itself. None of them would notice if
// gateway.go went on passing nil, and the product would ship with a fully
// tested notifier that production never installs — which is precisely the state
// the file was in before (DriftNotify left nil on purpose, with a comment
// explaining why).
//
// It reads the boot file's source rather than booting a gateway, for the same
// reason its sibling TestStartKnowledgeLifecycle_IsCalledUNCONDITIONALLYFromBoot
// does: a whole gateway is not cheap enough to stand up here.
//
// DIES ON: passing nil (or any non-call expression) as startKnowledgeLifecycle's
// drift-notify argument; dropping the argument entirely.
func TestDriftNotify_BootActuallyPassesTheNotifier(t *testing.T) {
	const bootFile = "gateway.go"
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, bootFile, nil, 0)
	require.NoError(t, err, "parse %s", bootFile)

	var (
		found      bool
		argCount   int
		notifierFn string
	)
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "startKnowledgeLifecycle" {
			return true
		}
		found = true
		argCount = len(call.Args)
		if len(call.Args) >= 4 {
			if inner, isCall := call.Args[3].(*ast.CallExpr); isCall {
				if fn, isIdent := inner.Fun.(*ast.Ident); isIdent {
					notifierFn = fn.Name
				}
			}
		}
		return false
	})

	require.Truef(t, found, "startKnowledgeLifecycle is never called from %s", bootFile)
	require.Equalf(t, 4, argCount,
		"startKnowledgeLifecycle is called from %s without a drift-notify argument", bootFile)
	assert.Equalf(t, "knowledgeDriftNotifier", notifierFn,
		"startKnowledgeLifecycle's 4th argument in %s is not a knowledgeDriftNotifier(...) "+
			"call. A nil there ships the whole drift-notification lane dead — the operator "+
			"is told nothing while every test in this file stays green, which is the exact "+
			"failure mode this change was written to end", bootFile)
}
