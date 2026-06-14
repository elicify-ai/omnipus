// memory_file.go — per-memory .md file format (FR-7.2).
//
// Every memory is stored as an individual file:
//
//	<room>/memories/<id>.md
//
// Format:
//
//	---
//	id: <ulid>
//	title: <string>
//	type: <decision|fact|reference|lesson|person|project|moc|note>
//	tags: []
//	confidence: 0
//	status: active
//	supersedes: ""
//	author: <agent_id>
//	born_in: <session_id>
//	---
//
//	Body text with [[wikilink]] narrative edges.
//
// Every field is present even when empty/zero (NFR-7) so v0.2 can enrich
// without file migration.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package memrooms

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/logger"
)

// MemoryType is the closed 8-member enum for memory kind (FR-7.2).
type MemoryType string

const (
	MemoryTypeDecision  MemoryType = "decision"
	MemoryTypeFact      MemoryType = "fact"
	MemoryTypeReference MemoryType = "reference"
	MemoryTypeLesson    MemoryType = "lesson"
	MemoryTypePerson    MemoryType = "person"
	MemoryTypeProject   MemoryType = "project"
	MemoryTypeMOC       MemoryType = "moc"
	MemoryTypeNote      MemoryType = "note"
)

// ParseMemoryType validates a string into a MemoryType.
func ParseMemoryType(s string) (MemoryType, error) {
	switch MemoryType(s) {
	case MemoryTypeDecision, MemoryTypeFact, MemoryTypeReference, MemoryTypeLesson,
		MemoryTypePerson, MemoryTypeProject, MemoryTypeMOC, MemoryTypeNote:
		return MemoryType(s), nil
	}
	return "", fmt.Errorf("invalid memory type %q; expected one of: decision, fact, reference, lesson, person, project, moc, note", s)
}

// MemoryStatus is the closed 3-member enum for memory lifecycle status (FR-7.2).
type MemoryStatus string

const (
	MemoryStatusActive     MemoryStatus = "active"
	MemoryStatusSuperseded MemoryStatus = "superseded"
	MemoryStatusArchived   MemoryStatus = "archived"
)

// MemoryFrontmatter is the complete, pinned frontmatter schema for a memory file (FR-7.2).
//
// Every field is present even when empty — NFR-7 guarantees no file migration in v0.2.
// The `confidence` field is a denormalized cache; counters.jsonl is authoritative.
// The `born_in` field records session provenance (not a log; it IS frontmatter per FR-7.5).
// The body carries [[id]] wikilink narrative edges.
type MemoryFrontmatter struct {
	// ID is the memory's stable unique identifier (ULID string).
	ID string `yaml:"id"`
	// Title is a human-readable short title.
	Title string `yaml:"title"`
	// Type is one of the 8 closed-enum values (FR-7.2).
	Type MemoryType `yaml:"type"`
	// Tags is a free list of string labels. Empty by default.
	Tags []string `yaml:"tags"`
	// Confidence is a 0–1 cache of the memory's recall-weight. 0 on creation.
	// counters.jsonl is authoritative; this is updated lazily.
	Confidence float64 `yaml:"confidence"`
	// Status is the lifecycle state: active | superseded | archived.
	Status MemoryStatus `yaml:"status"`
	// Supersedes is the memory ID this entry replaces, or "" if none.
	Supersedes string `yaml:"supersedes"`
	// Author is the agent ID that wrote this memory.
	Author string `yaml:"author"`
	// BornIn is the session ID in which this memory was created (provenance).
	BornIn string `yaml:"born_in"`
}

// MemoryFile is the parsed form of a per-memory .md file.
type MemoryFile struct {
	Frontmatter MemoryFrontmatter
	// Body is the body text after the closing --- of the frontmatter.
	// May contain [[wikilink]] narrative edges.
	Body string
}

// Filename returns the expected filename for the memory (e.g., "01ABC.md").
func Filename(id string) string {
	return id + ".md"
}

// WriteMemoryFile atomically writes a MemoryFile to <memoriesDir>/<id>.md.
func WriteMemoryFile(memoriesDir string, m MemoryFile) error {
	content := serializeMemoryFile(m)
	path := filepath.Join(memoriesDir, Filename(m.Frontmatter.ID))
	return fileutil.WriteFileAtomic(path, []byte(content), 0o600)
}

// ReadMemoryFile reads and parses <memoriesDir>/<id>.md.
// Returns ErrNotFound when the file does not exist.
func ReadMemoryFile(memoriesDir, id string) (MemoryFile, error) {
	path := filepath.Join(memoriesDir, Filename(id))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return MemoryFile{}, ErrMemoryNotFound(id)
		}
		return MemoryFile{}, fmt.Errorf("memrooms: read memory file %s: %w", id, err)
	}
	return parseMemoryFile(string(data))
}

// ListMemoryIDs returns all memory IDs in memoriesDir (files matching "*.md").
func ListMemoryIDs(memoriesDir string) ([]string, error) {
	entries, err := os.ReadDir(memoriesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memrooms: list memories: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".md") {
			ids = append(ids, strings.TrimSuffix(name, ".md"))
		}
	}
	return ids, nil
}

// ScanMemories reads every MemoryFile in memoriesDir, skipping files that fail to parse.
func ScanMemories(memoriesDir string) ([]MemoryFile, error) {
	entries, err := os.ReadDir(memoriesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("memrooms: scan memories: %w", err)
	}
	var out []MemoryFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(memoriesDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		mf, err := parseMemoryFile(string(data))
		if err != nil {
			continue
		}
		out = append(out, mf)
	}
	return out, nil
}

// SearchMemories performs a case-insensitive literal substring search over all
// MemoryFiles in memoriesDir. Searches both frontmatter (title, tags, type) and
// body text. Returns results in filesystem-order (deterministic, not ranked).
// This is the v0.1.0 scan-based recall (bleve FTS is a separate dependent unit).
func SearchMemories(memoriesDir, query string) ([]MemoryFile, error) {
	all, err := ScanMemories(memoriesDir)
	if err != nil {
		return nil, err
	}
	if query == "" {
		return all, nil
	}
	lq := strings.ToLower(query)
	var out []MemoryFile
	for _, mf := range all {
		if memoryMatchesQuery(mf, lq) {
			out = append(out, mf)
		}
	}
	return out, nil
}

// memoryMatchesQuery returns true when mf contains the query substring.
func memoryMatchesQuery(mf MemoryFile, lowerQuery string) bool {
	if strings.Contains(strings.ToLower(mf.Frontmatter.Title), lowerQuery) {
		return true
	}
	if strings.Contains(strings.ToLower(string(mf.Frontmatter.Type)), lowerQuery) {
		return true
	}
	for _, tag := range mf.Frontmatter.Tags {
		if strings.Contains(strings.ToLower(tag), lowerQuery) {
			return true
		}
	}
	if strings.Contains(strings.ToLower(mf.Body), lowerQuery) {
		return true
	}
	return false
}

// ErrMemoryNotFound is returned when a memory file does not exist.
type ErrMemoryNotFound string

func (e ErrMemoryNotFound) Error() string { return "memory not found: " + string(e) }

// Is satisfies errors.Is for ErrMemoryNotFound comparisons.
func (e ErrMemoryNotFound) Is(target error) bool {
	_, ok := target.(ErrMemoryNotFound)
	return ok
}

// --- Serialization ---------------------------------------------------------

// serializeMemoryFile renders a MemoryFile to its on-disk YAML-frontmatter format.
// We hand-write YAML rather than importing a YAML library to avoid adding a new
// dependency — the format is simple enough (no nesting beyond the tags list).
func serializeMemoryFile(m MemoryFile) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "id: %s\n", yamlStr(m.Frontmatter.ID))
	fmt.Fprintf(&sb, "title: %s\n", yamlStr(m.Frontmatter.Title))
	fmt.Fprintf(&sb, "type: %s\n", string(m.Frontmatter.Type))
	sb.WriteString("tags:")
	if len(m.Frontmatter.Tags) == 0 {
		sb.WriteString(" []\n")
	} else {
		sb.WriteString("\n")
		for _, tag := range m.Frontmatter.Tags {
			fmt.Fprintf(&sb, "  - %s\n", yamlStr(tag))
		}
	}
	fmt.Fprintf(&sb, "confidence: %.4f\n", m.Frontmatter.Confidence)
	fmt.Fprintf(&sb, "status: %s\n", string(m.Frontmatter.Status))
	fmt.Fprintf(&sb, "supersedes: %s\n", yamlStr(m.Frontmatter.Supersedes))
	fmt.Fprintf(&sb, "author: %s\n", yamlStr(m.Frontmatter.Author))
	fmt.Fprintf(&sb, "born_in: %s\n", yamlStr(m.Frontmatter.BornIn))
	sb.WriteString("---\n")
	if m.Body != "" {
		sb.WriteString("\n")
		sb.WriteString(m.Body)
		if !strings.HasSuffix(m.Body, "\n") {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// yamlStr returns a YAML-safe scalar string. Single-word values are unquoted;
// anything else is double-quoted with minimal escaping.
func yamlStr(s string) string {
	if s == "" {
		return `""`
	}
	// Needs quoting if it contains spaces, colons, or YAML special chars.
	needsQuote := strings.ContainsAny(s, `: "'[]{}#`)
	if !needsQuote {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// --- Parsing ---------------------------------------------------------------

// parseMemoryFile parses the YAML-frontmatter format produced by serializeMemoryFile.
// It is lenient: unknown keys are silently skipped; missing fields default to zero values.
func parseMemoryFile(raw string) (MemoryFile, error) {
	if !strings.HasPrefix(raw, "---\n") && !strings.HasPrefix(raw, "---\r\n") {
		return MemoryFile{}, fmt.Errorf("memrooms: missing opening --- delimiter")
	}
	// Find the closing --- delimiter. The normal form ends with "\n---\n"; a file
	// with no trailing newline after the closing fence ends with "\n---". We must
	// remember WHICH delimiter matched so we skip exactly its length — a hardcoded
	// +5 would drop the first body byte in the 4-char EOF case.
	rest := raw[4:] // skip opening "---\n"
	const delimFull = "\n---\n"
	const delimEOF = "\n---"
	closeIdx := strings.Index(rest, delimFull)
	delimLen := len(delimFull)
	if closeIdx < 0 {
		// Try with trailing EOF (no trailing newline after last ---).
		closeIdx = strings.Index(rest, delimEOF)
		if closeIdx < 0 {
			return MemoryFile{}, fmt.Errorf("memrooms: missing closing --- delimiter")
		}
		delimLen = len(delimEOF)
	}

	frontmatterText := rest[:closeIdx]
	afterClose := rest[closeIdx+delimLen:] // skip the matched closing delimiter
	if strings.HasPrefix(afterClose, "\n") {
		afterClose = afterClose[1:] // skip blank separator line
	}
	body := afterClose

	fm, err := parseFrontmatter(frontmatterText)
	if err != nil {
		return MemoryFile{}, err
	}
	return MemoryFile{Frontmatter: fm, Body: body}, nil
}

// parseFrontmatter parses the key:value lines of the YAML frontmatter block.
func parseFrontmatter(text string) (MemoryFrontmatter, error) {
	var fm MemoryFrontmatter
	scanner := bufio.NewScanner(strings.NewReader(text))
	inTags := false

	for scanner.Scan() {
		line := scanner.Text()
		// Detect tags list mode.
		if strings.TrimSpace(line) == "tags: []" {
			fm.Tags = []string{}
			inTags = false
			continue
		}
		if strings.TrimSpace(line) == "tags:" {
			inTags = true
			fm.Tags = []string{}
			continue
		}
		if inTags {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "- ") {
				fm.Tags = append(fm.Tags, unquoteYAML(strings.TrimPrefix(trimmed, "- ")))
				continue
			}
			// No longer in a list item — fall through to parse as a regular key.
			inTags = false
		}

		key, val, ok := parseKV(line)
		if !ok {
			continue
		}
		switch key {
		case "id":
			fm.ID = unquoteYAML(val)
		case "title":
			fm.Title = unquoteYAML(val)
		case "type":
			fm.Type = MemoryType(unquoteYAML(val))
		case "confidence":
			var f float64
			if _, scanErr := fmt.Sscanf(val, "%f", &f); scanErr != nil {
				// Malformed confidence: keep the zero default (counters.jsonl is
				// authoritative anyway) but don't silently swallow the signal.
				logger.WarnCF("memrooms", "parseFrontmatter: invalid confidence value; defaulting to 0",
					map[string]any{"value": val, "error": scanErr.Error()})
				f = 0
			}
			fm.Confidence = f
		case "status":
			fm.Status = MemoryStatus(unquoteYAML(val))
		case "supersedes":
			fm.Supersedes = unquoteYAML(val)
		case "author":
			fm.Author = unquoteYAML(val)
		case "born_in":
			fm.BornIn = unquoteYAML(val)
		}
	}
	return fm, scanner.Err()
}

// parseKV splits "key: value" into (key, value, true). Returns (_, _, false) on mismatch.
func parseKV(line string) (string, string, bool) {
	idx := strings.Index(line, ": ")
	if idx < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+2:]), true
}

// unquoteYAML removes surrounding double-quotes and unescapes internal \" sequences.
// Handles the "empty" case — `""` → `""`.
func unquoteYAML(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		inner := s[1 : len(s)-1]
		return strings.ReplaceAll(inner, `\"`, `"`)
	}
	return s
}

// --- Counter log (FR-7.5) --------------------------------------------------

// CounterOp is the frozen set of operations for counters.jsonl (FR-7.5).
type CounterOp string

const (
	// CounterOpAccess is appended when a memory is returned by recall_memory.
	CounterOpAccess CounterOp = "access"
	// CounterOpDrift is appended by v0.2 confidence-drift engine (not written in v0.1.0).
	CounterOpDrift CounterOp = "drift"
	// CounterOpCited is appended when an agent explicitly references a memory by ID/title.
	CounterOpCited CounterOp = "cited"
)

// CounterRecord is the frozen v0.1.0 schema for counters.jsonl (FR-7.5).
// One JSON line per event. Fields must never be removed; v0.2 ranking reads them.
type CounterRecord struct {
	// TS is the UTC RFC3339 timestamp of the event.
	TS time.Time `json:"ts"`
	// MemoryID is the ID of the memory this event pertains to.
	MemoryID string `json:"memory_id"`
	// Op is the operation kind: access | drift | cited.
	Op CounterOp `json:"op"`
	// By is the agent ID that triggered this event.
	By string `json:"by"`
	// Amount is optional; used by the v0.2 drift engine. Omitted on access/cited.
	Amount *float64 `json:"amount,omitempty"`
}

// AppendCounterRecord appends a CounterRecord to the room's counters.jsonl.
// One JSON line < PIPE_BUF (4 KB) — POSIX-safe multi-process append (FR-7.5).
func AppendCounterRecord(countersPath string, rec CounterRecord) error {
	return fileutil.AppendJSONL(countersPath, rec)
}
