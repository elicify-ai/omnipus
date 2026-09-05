// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-057 U5 (Wave C) — W23 (FR-053...FR-060, FR-084), the session store's
// meta.json → four-file split, plus the FR-103 read seam.
//
// THE PROBLEM THIS CLOSES. Before this file, writeMetaLocked rewrote ONE
// whole-document meta.json on every mutation — including every streamed
// transcript line (AppendTranscript), every `/loop` tick, and every `/goal`
// judge round — because SessionMeta commingles identity/lifecycle (~11
// fields), SessionStats (9 fields), the 9 Goal* fields and the 9 Loop*
// fields in one struct. A loop tick rewrote goal state it never touched; a
// transcript append rewrote goal/loop state it never touched. FR-053 splits
// the ON-DISK representation into four independently-writable files —
// meta.json (identity/lifecycle/Type/ParentSessionID), stats.json,
// goal.json, loop.json — while FR-057 keeps the IN-MEMORY UnifiedMeta/
// SessionMeta shape (and therefore every wire payload) byte-identical: this
// is a persistence-layer change only, never a contracts/ change.
//
// Ownership Rule 6 (`[grill m-4]`): U2 creates pkg/session/unified_api.go in
// this SAME package in this SAME wave, so every unexported package-level
// identifier this file introduces is prefixed u5 to rule out a silent
// redeclaration collision that neither unit would see until integration.
package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// --- FR-053: the four on-disk group shapes ---

// u5IdentityFile is meta.json's on-disk shape: identity + lifecycle + Type +
// ParentSessionID (FR-053). Deliberately excludes Stats and every Goal*/
// Loop* field — including them here (even as a zero-valued, non-omitempty
// "stats" key) would violate AC-21(a)'s "each file contains only its own
// group's fields", since encoding/json's omitempty never omits a non-empty
// STRUCT type regardless of its field values. Field tags are verbatim
// copies of SessionMeta's own tags so u5ComposeUnifiedMeta's round-trip
// reproduces byte-identical JSON when UnifiedMeta is marshalled again
// (FR-057/AC-21(e)).
type u5IdentityFile struct {
	ID          string        `json:"id"`
	AgentID     string        `json:"agent_id"`
	Title       string        `json:"title,omitempty"`
	Status      SessionStatus `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Model       string        `json:"model,omitempty"`
	Provider    string        `json:"provider,omitempty"`
	WorkspaceID string        `json:"workspace_id,omitempty"`
	TaskID      string        `json:"task_id,omitempty"`
	Channel     string        `json:"channel"`
	// InstanceID is the channel INSTANCE key ("whatsapp.eu"), not the bare
	// type. Channel alone is not an identity when an install holds many
	// instances of one platform — see UnifiedMeta.InstanceID.
	InstanceID            string             `json:"instance_id,omitempty"`
	PeerID                string             `json:"peer_id,omitempty"`
	Partitions            []string           `json:"partitions"`
	LastCompactionSummary string             `json:"last_compaction_summary,omitempty"`
	Owner                 string             `json:"owner,omitempty"`
	AgentIDs              []string           `json:"agent_ids,omitempty"`
	ActiveAgentID         string             `json:"active_agent_id,omitempty"`
	CompactionSummaries   map[string]string  `json:"compaction_summaries,omitempty"`
	Type                  UnifiedSessionType `json:"type"`
	ParentSessionID       string             `json:"parent_session_id,omitempty"`
}

// u5StatsFile is stats.json's on-disk shape (FR-053): SessionStats plus its
// OWN UpdatedAt, independent of meta.json's — FR-066 composes the two
// (u5LaterOf, below) into the single exposed UnifiedMeta.UpdatedAt field on
// every read. SessionStats is embedded WITHOUT a json tag so its fields
// flatten directly into stats.json's top level.
type u5StatsFile struct {
	SessionStats
	UpdatedAt time.Time `json:"updated_at"`
}

// u5GoalFile is goal.json's on-disk shape: the 9 Goal* fields, verbatim
// tags. Carries no UpdatedAt of its own (only stats.json does, FR-053) — a
// goal round does not bump the session's composed recency (BDD-59).
type u5GoalFile struct {
	GoalID             string `json:"goal_id,omitempty"`
	GoalCondition      string `json:"goal_condition,omitempty"`
	GoalRoundsUsed     int    `json:"goal_rounds_used,omitempty"`
	GoalMaxRounds      int    `json:"goal_max_rounds,omitempty"`
	GoalLatestReason   string `json:"goal_latest_reason,omitempty"`
	GoalStartedAt      string `json:"goal_started_at,omitempty"`
	GoalLastActivityAt string `json:"goal_last_activity_at,omitempty"`
	GoalCriteriaJSON   string `json:"goal_criteria,omitempty"`
	GoalPendingJSON    string `json:"goal_pending,omitempty"`
	// PendingAskJSON (AskUserQuestion durable pending set) rides in the goal
	// group: pending interaction state, no recency bump (see SessionMeta).
	PendingAskJSON string `json:"pending_ask,omitempty"`
}

// u5LoopFile is loop.json's on-disk shape: the 9 Loop* fields, verbatim
// tags. Carries no UpdatedAt of its own, for the same reason as u5GoalFile.
type u5LoopFile struct {
	LoopMode           string `json:"loop_mode,omitempty"`
	LoopPrompt         string `json:"loop_prompt,omitempty"`
	LoopRunCount       int    `json:"loop_run_count,omitempty"`
	LoopMaxRuns        int    `json:"loop_max_runs,omitempty"`
	LoopIntervalMS     int64  `json:"loop_interval_ms,omitempty"`
	LoopNextDelayMS    int64  `json:"loop_next_delay_ms,omitempty"`
	LoopJobID          string `json:"loop_job_id,omitempty"`
	LoopStartedAt      string `json:"loop_started_at,omitempty"`
	LoopLastActivityAt string `json:"loop_last_activity_at,omitempty"`
}

// --- extraction: *UnifiedMeta -> group shape (used by the targeted writers
// and by writeMetaLocked's diff-based dispatch) ---

func u5IdentityFromMeta(meta *UnifiedMeta) u5IdentityFile {
	return u5IdentityFile{
		ID:                    meta.ID,
		AgentID:               meta.AgentID,
		Title:                 meta.Title,
		Status:                meta.Status,
		CreatedAt:             meta.CreatedAt,
		UpdatedAt:             meta.UpdatedAt,
		Model:                 meta.Model,
		Provider:              meta.Provider,
		WorkspaceID:           meta.WorkspaceID,
		TaskID:                meta.TaskID,
		Channel:               meta.Channel,
		InstanceID:            meta.InstanceID,
		PeerID:                meta.PeerID,
		Partitions:            meta.Partitions,
		LastCompactionSummary: meta.LastCompactionSummary,
		Owner:                 meta.Owner,
		AgentIDs:              meta.AgentIDs,
		ActiveAgentID:         meta.ActiveAgentID,
		CompactionSummaries:   meta.CompactionSummaries,
		Type:                  meta.Type,
		ParentSessionID:       meta.ParentSessionID,
	}
}

func u5StatsFromMeta(meta *UnifiedMeta) u5StatsFile {
	return u5StatsFile{SessionStats: meta.Stats, UpdatedAt: meta.UpdatedAt}
}

func u5GoalFromMeta(meta *UnifiedMeta) u5GoalFile {
	return u5GoalFile{
		GoalID:             meta.GoalID,
		GoalCondition:      meta.GoalCondition,
		GoalRoundsUsed:     meta.GoalRoundsUsed,
		GoalMaxRounds:      meta.GoalMaxRounds,
		GoalLatestReason:   meta.GoalLatestReason,
		GoalStartedAt:      meta.GoalStartedAt,
		GoalLastActivityAt: meta.GoalLastActivityAt,
		GoalCriteriaJSON:   meta.GoalCriteriaJSON,
		GoalPendingJSON:    meta.GoalPendingJSON,
		PendingAskJSON:     meta.PendingAskJSON,
	}
}

func u5LoopFromMeta(meta *UnifiedMeta) u5LoopFile {
	return u5LoopFile{
		LoopMode:           meta.LoopMode,
		LoopPrompt:         meta.LoopPrompt,
		LoopRunCount:       meta.LoopRunCount,
		LoopMaxRuns:        meta.LoopMaxRuns,
		LoopIntervalMS:     meta.LoopIntervalMS,
		LoopNextDelayMS:    meta.LoopNextDelayMS,
		LoopJobID:          meta.LoopJobID,
		LoopStartedAt:      meta.LoopStartedAt,
		LoopLastActivityAt: meta.LoopLastActivityAt,
	}
}

// u5LaterOf returns whichever of a/b is later (FR-066: UnifiedMeta.UpdatedAt
// composes as the later of meta.json's and stats.json's own UpdatedAt).
func u5LaterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// u5ComposeUnifiedMeta builds one *UnifiedMeta from the four group values
// (FR-055). Called only by readUnifiedMeta (unified.go) once all four groups
// have been read/defaulted according to that function's absent-vs-corrupt
// rules.
func u5ComposeUnifiedMeta(identity u5IdentityFile, stats u5StatsFile, goal u5GoalFile, loop u5LoopFile) *UnifiedMeta {
	return &UnifiedMeta{
		SessionMeta: SessionMeta{
			ID:                    identity.ID,
			AgentID:               identity.AgentID,
			Title:                 identity.Title,
			Status:                identity.Status,
			CreatedAt:             identity.CreatedAt,
			UpdatedAt:             u5LaterOf(identity.UpdatedAt, stats.UpdatedAt),
			Model:                 identity.Model,
			Provider:              identity.Provider,
			Stats:                 stats.SessionStats,
			WorkspaceID:           identity.WorkspaceID,
			TaskID:                identity.TaskID,
			Channel:               identity.Channel,
			InstanceID:            identity.InstanceID,
			PeerID:                identity.PeerID,
			Partitions:            identity.Partitions,
			LastCompactionSummary: identity.LastCompactionSummary,
			Owner:                 identity.Owner,
			AgentIDs:              identity.AgentIDs,
			ActiveAgentID:         identity.ActiveAgentID,
			CompactionSummaries:   identity.CompactionSummaries,
			ParentSessionID:       identity.ParentSessionID,
			GoalID:                goal.GoalID,
			GoalCondition:         goal.GoalCondition,
			GoalRoundsUsed:        goal.GoalRoundsUsed,
			GoalMaxRounds:         goal.GoalMaxRounds,
			GoalLatestReason:      goal.GoalLatestReason,
			GoalStartedAt:         goal.GoalStartedAt,
			GoalLastActivityAt:    goal.GoalLastActivityAt,
			GoalCriteriaJSON:      goal.GoalCriteriaJSON,
			GoalPendingJSON:       goal.GoalPendingJSON,
			PendingAskJSON:        goal.PendingAskJSON,
			LoopMode:              loop.LoopMode,
			LoopPrompt:            loop.LoopPrompt,
			LoopRunCount:          loop.LoopRunCount,
			LoopMaxRuns:           loop.LoopMaxRuns,
			LoopIntervalMS:        loop.LoopIntervalMS,
			LoopNextDelayMS:       loop.LoopNextDelayMS,
			LoopJobID:             loop.LoopJobID,
			LoopStartedAt:         loop.LoopStartedAt,
			LoopLastActivityAt:    loop.LoopLastActivityAt,
		},
		Type: identity.Type,
	}
}

// --- FR-103 read seam + the four group readers (FR-055/FR-056) ---

// u5ReadIdentityFile reads meta.json — the ONE mandatory group (FR-055): its
// absence or a parse failure is always an error.
func u5ReadIdentityFile(sessionDir string) (u5IdentityFile, error) {
	var f u5IdentityFile
	data, err := readFileFn(filepath.Join(sessionDir, "meta.json"))
	if err != nil {
		return f, fmt.Errorf("unified_store: read meta.json in %q: %w", sessionDir, err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("unified_store: parse meta.json in %q: %w", sessionDir, err)
	}
	return f, nil
}

// u5ReadStatsFile reads stats.json. Absent -> zero value, nil error
// (FR-055). Present-but-corrupt -> error (FR-056), never silently zeroed.
func u5ReadStatsFile(sessionDir string) (u5StatsFile, error) {
	var f u5StatsFile
	data, err := readFileFn(filepath.Join(sessionDir, "stats.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return f, fmt.Errorf("read %q: %w", filepath.Join(sessionDir, "stats.json"), err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("parse %q: %w", filepath.Join(sessionDir, "stats.json"), err)
	}
	return f, nil
}

// u5ReadGoalFile reads goal.json. Same absent/corrupt contract as
// u5ReadStatsFile.
func u5ReadGoalFile(sessionDir string) (u5GoalFile, error) {
	var f u5GoalFile
	data, err := readFileFn(filepath.Join(sessionDir, "goal.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return f, fmt.Errorf("read %q: %w", filepath.Join(sessionDir, "goal.json"), err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("parse %q: %w", filepath.Join(sessionDir, "goal.json"), err)
	}
	return f, nil
}

// u5ReadLoopFile reads loop.json. Same absent/corrupt contract as
// u5ReadStatsFile.
func u5ReadLoopFile(sessionDir string) (u5LoopFile, error) {
	var f u5LoopFile
	data, err := readFileFn(filepath.Join(sessionDir, "loop.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return f, nil
		}
		return f, fmt.Errorf("read %q: %w", filepath.Join(sessionDir, "loop.json"), err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("parse %q: %w", filepath.Join(sessionDir, "loop.json"), err)
	}
	return f, nil
}

// --- FR-054/FR-084: the four targeted field-group writers ---
//
// Every writer below shares the same shape, mirroring the retired
// writeMetaLocked's own discipline: (1) marshal ONLY this group's fields;
// (2) write ONLY this group's file, disk write BEFORE any cache mutation
// (FR-049 — never hold cacheMu across an os.*/fileutil.* call; also
// preserves the MB-1 guarantee: a failed disk write must never touch the
// cache, so a failed SetMeta/AppendTranscript can never leave metaCache
// reporting an unpersisted value); (3) update ONLY this group's fields on
// the LIVE cached entry in place — never `us.metaCache[sessionID] =
// meta.Clone()` wholesale (FR-084; that whole-document replace is exactly
// what let a `/goal set` or `Status` transition discard another writer's
// unflushed, cache-only deltas — Alternative F, one layer up from the file
// this split already proved it away on). Callers hold sessionID's shard
// throughout (the "Locked" suffix, same convention as the retired
// writeMetaLocked and readMetaLocked).

// u5WriteIdentityLocked writes meta.json and updates ONLY the identity
// field group (including Type and the ADR-057 FR-008 ParentSessionID) on
// the cached entry. Also wires FR-097's parent-index ADD side
// (u4IndexAddChild, unified.go) — safe to call unconditionally on every
// identity write since it no-ops for an empty/unchanged ParentSessionID and
// is idempotent for a re-registration of the same pair.
func (us *UnifiedStore) u5WriteIdentityLocked(sessionID string, meta *UnifiedMeta) error {
	file := u5IdentityFromMeta(meta)
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("unified_store: marshal meta.json: %w", err)
	}
	metaPath := filepath.Join(us.baseDir, sessionID, "meta.json")
	if err := fileutil.WithFlock(metaPath, func() error {
		return writeFileAtomicFn(metaPath, data, 0o600)
	}); err != nil {
		return err
	}

	us.cacheMu.Lock()
	if cached, ok := us.metaCache[sessionID]; ok {
		cached.ID = meta.ID
		cached.AgentID = meta.AgentID
		cached.Title = meta.Title
		cached.Status = meta.Status
		cached.CreatedAt = meta.CreatedAt
		cached.Model = meta.Model
		cached.Provider = meta.Provider
		cached.WorkspaceID = meta.WorkspaceID
		cached.TaskID = meta.TaskID
		cached.Channel = meta.Channel
		cached.InstanceID = meta.InstanceID
		cached.PeerID = meta.PeerID
		cached.Partitions = slices.Clone(meta.Partitions)
		cached.LastCompactionSummary = meta.LastCompactionSummary
		cached.Owner = meta.Owner
		cached.AgentIDs = slices.Clone(meta.AgentIDs)
		cached.ActiveAgentID = meta.ActiveAgentID
		cached.CompactionSummaries = maps.Clone(meta.CompactionSummaries)
		cached.ParentSessionID = meta.ParentSessionID
		cached.Type = meta.Type
		if meta.UpdatedAt.After(cached.UpdatedAt) {
			cached.UpdatedAt = meta.UpdatedAt
		}
	} else {
		us.metaCache[sessionID] = meta.Clone()
	}
	us.cacheMu.Unlock()

	// FR-097 ADD side — outside the cacheMu critical section above (it takes
	// cacheMu itself; lock order is one-directional, never nested/reentrant).
	us.u4IndexAddChild(meta.ParentSessionID, sessionID)
	return nil
}

// u5WriteStatsLocked writes stats.json and updates ONLY the Stats field
// group on the cached entry. Never touches meta.json/goal.json/loop.json —
// this is what makes AppendTranscript's per-line write cheap again (R-8/
// FR-084): a delegation fan-out's streamed tokens no longer rewrite goal or
// loop state on every line.
func (us *UnifiedStore) u5WriteStatsLocked(sessionID string, meta *UnifiedMeta) error {
	file := u5StatsFromMeta(meta)
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("unified_store: marshal stats.json: %w", err)
	}
	statsPath := filepath.Join(us.baseDir, sessionID, "stats.json")
	if err := fileutil.WithFlock(statsPath, func() error {
		return writeFileAtomicFn(statsPath, data, 0o600)
	}); err != nil {
		return err
	}

	us.cacheMu.Lock()
	if cached, ok := us.metaCache[sessionID]; ok {
		cached.Stats = meta.Stats
		cached.Stats.ByModel = maps.Clone(meta.Stats.ByModel)
		if meta.UpdatedAt.After(cached.UpdatedAt) {
			cached.UpdatedAt = meta.UpdatedAt
		}
	} else {
		us.metaCache[sessionID] = meta.Clone()
	}
	us.cacheMu.Unlock()
	return nil
}

// u5WriteGoalLocked writes goal.json and updates ONLY the 9 Goal* fields on
// the cached entry. Never touches meta.json/stats.json/loop.json, and never
// bumps the composed UpdatedAt (goal.json carries no UpdatedAt of its own,
// FR-053) — a `/goal` round is not "session activity" for recency-sort
// purposes (BDD-59).
func (us *UnifiedStore) u5WriteGoalLocked(sessionID string, meta *UnifiedMeta) error {
	file := u5GoalFromMeta(meta)
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("unified_store: marshal goal.json: %w", err)
	}
	goalPath := filepath.Join(us.baseDir, sessionID, "goal.json")
	if err := fileutil.WithFlock(goalPath, func() error {
		return writeFileAtomicFn(goalPath, data, 0o600)
	}); err != nil {
		return err
	}

	us.cacheMu.Lock()
	if cached, ok := us.metaCache[sessionID]; ok {
		cached.GoalID = meta.GoalID
		cached.GoalCondition = meta.GoalCondition
		cached.GoalRoundsUsed = meta.GoalRoundsUsed
		cached.GoalMaxRounds = meta.GoalMaxRounds
		cached.GoalLatestReason = meta.GoalLatestReason
		cached.GoalStartedAt = meta.GoalStartedAt
		cached.GoalLastActivityAt = meta.GoalLastActivityAt
		cached.GoalCriteriaJSON = meta.GoalCriteriaJSON
		cached.GoalPendingJSON = meta.GoalPendingJSON
		cached.PendingAskJSON = meta.PendingAskJSON
	} else {
		us.metaCache[sessionID] = meta.Clone()
	}
	us.cacheMu.Unlock()
	return nil
}

// u5WriteLoopLocked writes loop.json and updates ONLY the 9 Loop* fields on
// the cached entry. Same isolation/no-UpdatedAt-bump rationale as
// u5WriteGoalLocked.
func (us *UnifiedStore) u5WriteLoopLocked(sessionID string, meta *UnifiedMeta) error {
	file := u5LoopFromMeta(meta)
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("unified_store: marshal loop.json: %w", err)
	}
	loopPath := filepath.Join(us.baseDir, sessionID, "loop.json")
	if err := fileutil.WithFlock(loopPath, func() error {
		return writeFileAtomicFn(loopPath, data, 0o600)
	}); err != nil {
		return err
	}

	us.cacheMu.Lock()
	if cached, ok := us.metaCache[sessionID]; ok {
		cached.LoopMode = meta.LoopMode
		cached.LoopPrompt = meta.LoopPrompt
		cached.LoopRunCount = meta.LoopRunCount
		cached.LoopMaxRuns = meta.LoopMaxRuns
		cached.LoopIntervalMS = meta.LoopIntervalMS
		cached.LoopNextDelayMS = meta.LoopNextDelayMS
		cached.LoopJobID = meta.LoopJobID
		cached.LoopStartedAt = meta.LoopStartedAt
		cached.LoopLastActivityAt = meta.LoopLastActivityAt
	} else {
		us.metaCache[sessionID] = meta.Clone()
	}
	us.cacheMu.Unlock()
	return nil
}

// u5SameJSON reports whether a and b marshal to byte-identical JSON. Used
// only by writeMetaLocked's (unified.go) diff-based dispatch to decide which
// of the four field groups a caller outside this file's direct control
// (pkg/session/unified_api.go) actually changed. A marshal failure on
// either side is treated as "changed" so the real write proceeds and
// surfaces the genuine error, rather than this helper silently swallowing
// one.
func u5SameJSON(a, b any) bool {
	ab, aErr := json.Marshal(a)
	bb, bErr := json.Marshal(b)
	if aErr != nil || bErr != nil {
		return false
	}
	return bytes.Equal(ab, bb)
}
