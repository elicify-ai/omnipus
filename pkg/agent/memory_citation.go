// memory_citation.go — cited_in (op:cited) event emission (FR-7.5 / NFR-1).
//
// The CounterOpCited constant (pkg/memrooms/memory_file.go) was declared but
// never written. This file closes that gap: a per-turn citation tracker
// collects the memories surfaced by recall_memory, and after each LLM response
// the agent loop scans the response text for references to those memories'
// IDs/titles. On a hit it appends an op:cited record to the citing memory's
// room counters.jsonl — so v0.2 ranking has citation history from v0.1.0
// sessions with no backfill (NFR-1).
//
// Detection is deliberately simple: a case-insensitive substring match on the
// memory ID or title. One cited event per recalled memory per turn (deduped) —
// "citation frequency" is measured per-turn, not per-mention, so a chatty agent
// cannot inflate a memory's rank by repeating the same ID.
package agent

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dapicom-ai/omnipus/pkg/logger"
	"github.com/dapicom-ai/omnipus/pkg/memrooms"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// citationTracker is the pkg/agent implementation of tools.CitationTracker.
// It is scoped to a single agent turn: the agent loop constructs one per turn,
// installs it on the tool context, and calls EmitCitations after each LLM
// response.
type citationTracker struct {
	ms *MemoryStore

	mu       sync.Mutex
	recalled []recalledMem
	// emitted dedupes by memory ID so each recalled memory logs at most one
	// op:cited event per turn.
	emitted map[string]bool
}

// recalledMem is a memory surfaced by recall_memory in the current turn.
type recalledMem struct {
	id    string
	title string
}

// newCitationTracker builds a turn-scoped citation tracker backed by ms.
// Returns nil when ms is nil (main gateway agent has no memory store) — the
// agent loop treats nil as "no tracking", matching WithCitationTracker's
// nil-short-circuit.
func newCitationTracker(ms *MemoryStore) *citationTracker {
	if ms == nil {
		return nil
	}
	return &citationTracker{ms: ms, emitted: make(map[string]bool)}
}

// RecordRecalled implements tools.CitationTracker. It captures the IDs/titles
// of memories surfaced by recall_memory for later citation scanning. Entries
// without an ID are ignored (legacy stores / test doubles that don't surface
// per-memory IDs cannot be cited by ID/title, so tracking them is pointless).
func (c *citationTracker) RecordRecalled(entries []tools.MemoryEntry) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		if strings.TrimSpace(e.ID) == "" {
			continue
		}
		c.recalled = append(c.recalled, recalledMem{id: e.ID, title: e.Title})
	}
}

// EmitCitations scans assistantText for references to any memory recalled in
// this turn and appends an op:cited counter record for each hit (deduped per
// memory per turn). Safe to call after every LLM response; a memory cited in
// multiple iterations of the same turn is logged once.
func (c *citationTracker) EmitCitations(assistantText string) {
	if c == nil || strings.TrimSpace(assistantText) == "" {
		return
	}
	// Snapshot under the lock; the scan + append happens outside to keep the
	// critical section short. emit decisions are reconciled back under the lock.
	c.mu.Lock()
	candidates := make([]recalledMem, len(c.recalled))
	copy(candidates, c.recalled)
	c.mu.Unlock()

	lower := strings.ToLower(assistantText)
	by := c.ms.resolveAuthor()
	ts := c.ms.now()

	for _, m := range candidates {
		c.mu.Lock()
		already := c.emitted[m.id]
		c.mu.Unlock()
		if already {
			continue
		}
		if !citationMatches(lower, m) {
			continue
		}
		countersPath := c.resolveCountersPath(m.id)
		if countersPath == "" {
			// Memory no longer on disk (deleted between recall and citation).
			// Mark emitted so we don't repeatedly re-scan a missing file.
			c.mu.Lock()
			c.emitted[m.id] = true
			c.mu.Unlock()
			continue
		}
		rec := memrooms.CounterRecord{
			TS:       ts,
			MemoryID: m.id,
			Op:       memrooms.CounterOpCited,
			By:       by,
		}
		if err := memrooms.AppendCounterRecord(countersPath, rec); err != nil {
			logger.WarnCF("agent.memory", "citation: failed to append op:cited counter",
				map[string]any{"memory_id": m.id, "error": err.Error()})
			continue
		}
		c.mu.Lock()
		c.emitted[m.id] = true
		c.mu.Unlock()
	}
}

// citationMatches reports whether the lowercased assistant text references the
// memory by ID or title. ID match is on the raw (case-insensitive) ID; title
// match is skipped for empty/whitespace titles to avoid false positives on
// generic single-word titles.
func citationMatches(lowerText string, m recalledMem) bool {
	if strings.Contains(lowerText, strings.ToLower(m.id)) {
		return true
	}
	t := strings.TrimSpace(strings.ToLower(m.title))
	if t == "" {
		return false
	}
	// Avoid trivial false positives on very short titles (e.g. "go", "api").
	// A 4-rune floor keeps the match meaningful while still catching real titles.
	if len([]rune(t)) < 4 {
		return false
	}
	return strings.Contains(lowerText, t)
}

// resolveCountersPath locates which room holds the memory file and returns that
// room's counters.jsonl path. Private room is checked first, then the active
// shared room. Returns "" when the memory is not found in either room.
func (c *citationTracker) resolveCountersPath(memoryID string) string {
	memFile := filepath.Join(c.ms.privateRoom.MemoriesDir, memrooms.Filename(memoryID))
	if _, err := os.Stat(memFile); err == nil {
		return c.ms.privateRoom.CountersPath
	}
	if shared := c.ms.currentSharedRoom(); shared != nil {
		memFile := filepath.Join(shared.MemoriesDir, memrooms.Filename(memoryID))
		if _, err := os.Stat(memFile); err == nil {
			return shared.CountersPath
		}
	}
	return ""
}
