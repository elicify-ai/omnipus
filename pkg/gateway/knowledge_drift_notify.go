// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-067 FR-038a — the drift check's report, said out loud.
//
// # Why this file exists
//
// The drift check has always worked. What it could not do was TELL ANYONE.
// Its report went to a slog.Warn in gateway.log and to a DriftNotify hook that
// production left nil, because `Notification.type` in the contract admitted a
// single value — `schedule_failed` — so a knowledge-drift notification would
// have been rejected by the SPA's generated zod guard and dropped on the floor.
// Wiring the hook without widening the enum would have produced the worst of
// the three states available: code that LOOKS wired, an operator who is told
// nothing, and a green test suite. So the enum was widened first
// (contracts/components/schemas/Notification.yaml and NotificationFrame in
// contracts/asyncapi.yaml both carry `knowledge_drift` now), and this file is
// the other half.
//
// # The three rules this file is written to keep
//
//  1. IT NEVER FIRES ON A HEALTHY RUN (FR-038a: "It MUST report only when
//     something is wrong"). knowledge.HealthChecker already guarantees that, and
//     knowledgeDriftNotifier re-checks Healthy() anyway — a notification centre
//     that pings every six hours to say "all fine" is one an operator learns to
//     ignore, and then misses the one that mattered.
//
//  2. IT SAYS WHAT HAPPENED IN WORDS, NOT IN FIELD NAMES.
//     knowledge.DriftReport.Summary() renders "3 not_indexed, 1 stale_content",
//     which is the right thing in a log line and the wrong thing in a bell. The
//     reader of a notification is the person who owns the folder, not the person
//     who wrote the indexer.
//
//  3. IT DOES NOT CLAIM MORE THAN IT KNOWS. The repair (resyncAfterDrift) runs
//     asynchronously AFTER this notification is raised, and it can itself fail.
//     So the wording is "the index is being rebuilt", never "the index has been
//     repaired": one is true at the moment it is written, the other is a
//     prediction. It also states plainly that the operator's own files were not
//     touched, because "drift was found in your knowledge base" reads, to
//     someone who is not an engineer, like something happened to their notes.

package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/notifications"
)

// knowledgeDriftEmitter is the live-push half. *agent.AgentLoop satisfies it;
// a test supplies a recorder. Declared as an interface here rather than taking
// the loop directly so this file has no reason to reach for anything else on it.
type knowledgeDriftEmitter interface {
	EmitNotification(p agent.NotificationPayload)
}

// knowledgeDriftNotifier builds the KnowledgeLifecycleOptions.DriftNotify
// callback: persist one notification per recipient and push it live.
//
// Returns nil when there is nothing to deliver through, which makes
// NewKnowledgeLifecycle fall back to its slog.Warn — the previous behaviour,
// unchanged, for the test harnesses that boot no notification store.
func knowledgeDriftNotifier(
	store *notifications.Store,
	emitter knowledgeDriftEmitter,
	getCfg func() *config.Config,
) func(knowledge.DriftReport) {
	if store == nil && emitter == nil {
		return nil
	}
	return func(r knowledge.DriftReport) {
		// Rule 1, restated in code. HealthChecker is the authority and does not
		// call Notify for a healthy report; this is the second lock on the same
		// door, because the cost of being wrong is an operator who stops
		// reading the bell.
		if r.Healthy() {
			return
		}

		title, body := knowledgeDriftMessage(r)
		now := time.Now().UnixMilli()

		// THE LOG LINE IS THE FLOOR, NOT THE FALLBACK.
		//
		// Before this notifier existed, NewKnowledgeLifecycle's default hook
		// wrote a slog.Warn for every unhealthy report, and that WARN was the
		// operator's only record. Installing a notifier silently retired it:
		// the default hook is only used when DriftNotify is nil, and boot now
		// always passes this function, so that WARN became unreachable in
		// production. The notification then replaced it with something WEAKER
		// on the most common install shape — accountless, headless, or simply
		// no tab open — where the recipient is the broadcast sentinel, nothing
		// is persisted, and an emit with no subscriber is discarded in silence.
		// Drift was detected, a full re-index ran, and gateway.log said nothing.
		//
		// So log unconditionally, BEFORE delivery is attempted. The notification
		// is an addition to the log, never a replacement for it: the bell is
		// for the person watching, the log is for the person diagnosing after
		// the fact, and those are not the same person or the same moment.
		recipients := knowledgeDriftRecipients(getCfg)
		slog.Warn("knowledge: drift detected",
			"collection", r.Root, "findings", len(r.Findings), "summary", r.Summary(),
			"recipients", len(recipients))

		for _, recipient := range recipients {
			n := notifications.Notification{
				Recipient:   recipient,
				Type:        notifications.TypeKnowledgeDrift,
				Title:       title,
				Body:        body,
				Severity:    notifications.SeverityWarning,
				CreatedAtMs: now,
				// One live item per COLLECTION, updated in place. Drift that a
				// re-index cannot clear — an unreadable file, a stale rename
				// journal, a document-count mismatch — is re-reported every
				// cycle by design, because it is still true. Without a key
				// that is a new bell item every six hours, forever, and the
				// 50-item cap turns one bad file into an eviction engine for
				// every other notification the operator has.
				CoalesceKey: "knowledge_drift:" + r.Root,
			}
			stored := n
			// The admin-broadcast sentinel is not a real username; persisting it
			// would write a file no ListForUser ever reads (same rule the
			// schedule-failure path follows).
			if recipient == agent.NotificationAdminBroadcast {
				// Same situation schedules.go reports as finding M4, and the
				// same wording: there is no account to file this under, so the
				// bell cannot hold it and a reload cannot recover it. The WARN
				// above is the durable record; this line names why.
				slog.Warn("knowledge: drift notification has no persistable recipient; live-broadcast only",
					"collection", r.Root)
			}
			if store != nil && recipient != agent.NotificationAdminBroadcast {
				persisted, err := store.Create(n)
				if err != nil {
					// The bell will be empty after a restart, but the operator
					// must still be told NOW. Log loudly and push anyway.
					slog.Error("knowledge: could not persist drift notification; pushing live anyway",
						"collection", r.Root, "recipient", recipient, "error", err)
				} else {
					stored = persisted
				}
			}
			// NotificationFrame.id is required and minLength 1 on the wire, so a
			// frame with an empty id is dropped by the SPA's zod guard — which
			// is exactly the class of silent loss this whole change exists to
			// remove. Mint one whenever persistence did not supply it (store
			// absent, store failed, or the live-only broadcast recipient).
			if stored.ID == "" {
				stored.ID = knowledgeDriftNotificationID()
			}
			if emitter != nil {
				emitter.EmitNotification(agent.NotificationPayload{
					Recipient:        recipient,
					ID:               stored.ID,
					NotificationType: stored.Type,
					Title:            stored.Title,
					Body:             stored.Body,
					Severity:         stored.Severity,
					Read:             stored.Read,
					CreatedAtMs:      stored.CreatedAtMs,
				})
			}
		}
	}
}

// knowledgeDriftRecipients returns who is told. Single-user product: everyone
// with an account. When there are no accounts at all (a gateway running with
// auth bypassed, which is where a developer meets this first) it returns the
// live-push-only broadcast sentinel, so the message still reaches whoever is
// connected instead of being addressed to nobody.
func knowledgeDriftRecipients(getCfg func() *config.Config) []string {
	if getCfg == nil {
		return []string{agent.NotificationAdminBroadcast}
	}
	cfg := getCfg()
	if cfg == nil {
		return []string{agent.NotificationAdminBroadcast}
	}
	seen := make(map[string]bool, len(cfg.Gateway.Users))
	out := make([]string, 0, len(cfg.Gateway.Users))
	for _, u := range cfg.Gateway.Users {
		if u.Username == "" || seen[u.Username] {
			continue
		}
		seen[u.Username] = true
		out = append(out, u.Username)
	}
	if len(out) == 0 {
		return []string{agent.NotificationAdminBroadcast}
	}
	return out
}

// knowledgeDriftMessage turns a report into the two strings a person reads.
//
// Every sentence here is load-bearing:
//   - the folder is named, because an operator may have several;
//   - the findings are counted in English, because "4 stale_content" is not a
//     sentence;
//   - "your files were not changed" is stated, because the alternative reading
//     of "drift" is that something happened to the notes themselves;
//   - the repair is described as UNDER WAY, not DONE, because at the moment
//     this text is composed it has not started and may still fail.
func knowledgeDriftMessage(r knowledge.DriftReport) (title, body string) {
	folder := filepath.Base(r.Root)
	if folder == "" || folder == "." || folder == string(filepath.Separator) {
		folder = r.Root
	}
	title = fmt.Sprintf("Search index for %q was out of date", folder)

	var b strings.Builder
	b.WriteString("Omnipus checks each knowledge base against its folder on a schedule. ")
	b.WriteString(fmt.Sprintf("This check found that its search index for %s no longer matched the folder: ", r.Root))
	b.WriteString(knowledgeDriftFindingsSentence(r))
	b.WriteString(" Your files were not changed — only Omnipus's own index of them was wrong. ")
	b.WriteString("It is re-reading the folder now; search results for this knowledge base may be incomplete until that finishes.")
	return title, b.String()
}

// knowledgeDriftFindingsSentence renders the findings as a plain-English list,
// in the report's own deterministic order so two identical states read
// identically. A kind with no phrase falls back to its raw name rather than
// being dropped: an unexplained count is bad, a MISSING count is worse.
func knowledgeDriftFindingsSentence(r knowledge.DriftReport) string {
	counts := map[knowledge.DriftKind]int{}
	order := make([]knowledge.DriftKind, 0, len(r.Findings))
	for _, f := range r.Findings {
		if _, seen := counts[f.Kind]; !seen {
			order = append(order, f.Kind)
		}
		counts[f.Kind]++
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		parts = append(parts, knowledgeDriftPhrase(k, counts[k]))
	}
	return strings.Join(parts, "; ") + "."
}

// knowledgeDriftPhrase is the whole English vocabulary of this feature, kept in
// one switch so the wording can be read and judged in one place.
func knowledgeDriftPhrase(kind knowledge.DriftKind, n int) string {
	switch kind {
	case knowledge.DriftNotIndexed:
		return fmt.Sprintf("%s in the folder that had never been added to the index", knowledgeDriftFiles(n))
	case knowledge.DriftMissingFromDisk:
		return fmt.Sprintf("%s the index still listed that are no longer in the folder", knowledgeDriftFiles(n))
	case knowledge.DriftStaleContent:
		return fmt.Sprintf("%s that have been edited since they were indexed", knowledgeDriftFiles(n))
	case knowledge.DriftUnreadable:
		return fmt.Sprintf("%s that could not be read", knowledgeDriftFiles(n))
	case knowledge.DriftPendingRename:
		return fmt.Sprintf("%d rename%s that was interrupted part-way", n, knowledgeDriftPlural(n))
	case knowledge.DriftDocumentCount:
		return "the index holds a different number of entries than the folder accounts for"
	case knowledge.DriftManifestUnusable:
		return "the index's own record of what it contains could not be read"
	default:
		return fmt.Sprintf("%d problem%s of type %q", n, knowledgeDriftPlural(n), string(kind))
	}
}

func knowledgeDriftFiles(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func knowledgeDriftPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// knowledgeDriftNotificationID mints an id for a notification that was never
// persisted. Opaque and collision-free enough for a bell item; it is never a
// key anything else looks up.
func knowledgeDriftNotificationID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not a reason to lose the alert. A timestamp is
		// a weaker id, not an absent one.
		return fmt.Sprintf("kdrift-%d", time.Now().UnixNano())
	}
	return "kdrift-" + hex.EncodeToString(b[:])
}
