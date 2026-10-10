// unified_conv_archive.go: the FAITHFUL model-content half of the one-time CONV
// saved-chat cutover (spec CONV "Content / continuation", FR-038;
// ARCHITECT-ANSWER-CONV-PROVENANCE.md, CONV-P A). It converts each legacy
// `.context/<key>.jsonl` model archive — and, per N3, only when it PROVABLY
// decodes — into the addressed day archive in the session directory `<baseDir>/<id>/`,
// preserving every field the saved line actually holds byte-for-byte, joining
// tool results to their producing assistant occurrence, and marking each
// converted record with the private `model_origin` provenance (CONV-P A).
//
// Nothing is fabricated: the parsed arguments map and the top-level
// thought_signature stay ABSENT (the live producer never stored them); the tool
// NAME is mirrored from the saved `function.name` (a copy of a saved field,
// required by the direct Anthropic adapter). Validate() makes that a write-side
// invariant.
//
// Publication order (spec CONV / Publication): materialize + verify the same-ID
// canonical archive and its window metadata FIRST, then retire the legacy
// source — never remove the only readable copy first. A refusal (D3) leaves the
// original byte-for-byte untouched and returns a VISIBLE error naming the chat.
//
// N2 (open pending founder → ruled B): a PROVABLY torn final line — the last
// line of the file, no closing newline, does not decode — is tolerated: every
// earlier line converts, the torn bytes are dropped as excluded leftovers, the
// original is never rewritten, and a visible startup notice names the chat and
// the byte offset of the torn tail. Any other bad line refuses visibly.
package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/memory"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

const convContextDir = ".context"

// convConvertLegacyModelArchives converts every legacy `.context/<key>.jsonl`
// model archive under baseDir into the addressed archive, translating the
// sibling `.context/<key>.meta.json` window state. It is idempotent and retry
// safe (a completed conversion is recognized and not rewritten).
func convConvertLegacyModelArchives(baseDir string) error {
	return convConvertLegacyModelArchivesFrom(baseDir, filepath.Join(baseDir, convContextDir), false)
}

// convConvertLegacyModelArchivesFrom is convConvertLegacyModelArchives with an
// explicit source directory: the per-agent transfer (DEL-10) converts a
// per-agent store's `.context` into the SHARED destination baseDir. When
// perAgent is set, an archive whose key does not name an owning session id is
// left where it is with a WARN (it has no chat to join; nothing is lost).
func convConvertLegacyModelArchivesFrom(baseDir, contextDir string, perAgent bool) error {
	entries, err := os.ReadDir(contextDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // fresh install: no legacy model archive
		}
		// A path that is not a directory (a stray file where the sessions
		// directory or its .context should be) cannot hold a legacy archive:
		// there is nothing to convert, so this scan has no work. Any other read
		// error still refuses the cutover visibly.
		if info, statErr := os.Stat(contextDir); statErr == nil && !info.IsDir() {
			return nil
		}
		if info, statErr := os.Stat(filepath.Dir(contextDir)); statErr == nil && !info.IsDir() {
			return nil
		}
		return fmt.Errorf("conversion: read %s: %w", contextDir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		base := strings.TrimSuffix(name, ".jsonl")
		if err := convConvertOneLegacyArchive(baseDir, contextDir, base, perAgent); err != nil {
			return err
		}
	}
	return nil
}

// convConvertOneLegacyArchive converts one `.context/<base>.jsonl` (+ meta) to
// the addressed archive for its owning session id, then retires the source.
func convConvertOneLegacyArchive(baseDir, contextDir, base string, perAgent bool) error {
	meta, metaErr := convReadLegacyMeta(filepath.Join(contextDir, base+".meta.json"))
	if metaErr != nil {
		return fmt.Errorf("conversion: saved chat %q: read legacy meta: %w", base, metaErr)
	}
	// The legacy meta's Key is the agent routing key; resolve it to the immutable
	// owning session id so the converted archive lands beside the chat transcript
	// (Decision D "Identity").
	id := owningSessionID(meta.Key)
	if meta.Key == "" {
		// The sanitized filename is the only identity available; use it rather
		// than guess an owner (spec CONV / Identity).
		id = base
	}
	if perAgent && (strings.ContainsAny(id, ":") || validateSessionID(id) != nil) {
		slog.Warn("conversion: per-agent model archive names no session; left in place",
			"archive", filepath.Join(contextDir, base+".jsonl"), "key", meta.Key)
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(contextDir, base+".jsonl"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("conversion: saved chat %q: read legacy archive: %w", id, err)
	}

	store, err := NewArchiveDayStore(baseDir, id)
	if err != nil {
		return fmt.Errorf("conversion: saved chat %q: %w", id, err)
	}
	already, tornRetained, err := convArchiveAlreadyMaterialized(store)
	if err != nil {
		return fmt.Errorf("conversion: saved chat %q: %w", id, err)
	}
	if already {
		// Verified same-ID completed output: finish publication/cleanup only —
		// unless the earlier conversion retained a torn source (N2=B), which
		// must stay byte-identical.
		if tornRetained {
			return nil
		}
		return convRetireLegacyArchive(contextDir, base)
	}

	origin := ModelOriginConvArchive
	if meta.Hydrated {
		origin = ModelOriginConvRebuilt
	}
	records, tornAt, err := convBuildRecords(id, raw, origin)
	if err != nil {
		return err // refusal names the chat and the line; source untouched
	}
	for _, rec := range records {
		if _, _, err := store.AppendIndexed(rec); err != nil {
			return fmt.Errorf("conversion: saved chat %q: append archive record %s: %w", id, rec.ID, err)
		}
	}
	addrs, callsAt, err := convTranscriptLineAddrs(store.dir())
	if err != nil {
		return fmt.Errorf("conversion: saved chat %q: index converted transcript: %w", id, err)
	}
	outMeta := convTranslateMeta(id, meta, len(records), addrs, callsAt)
	outMeta.ConvSourceTorn = tornAt >= 0
	if err := convWriteBackendMeta(store.dir(), outMeta); err != nil {
		return fmt.Errorf("conversion: saved chat %q: publish window metadata: %w", id, err)
	}
	if tornAt >= 0 {
		slog.Warn("conversion: tolerated a torn final archive line",
			"chat", id, "byte_offset", tornAt,
			"note", "final line has no closing newline and did not decode; kept as excluded leftovers, original retained")
		// N2=B: the torn bytes are not part of the chat; never rewrite the
		// original. Retain the whole source rather than retire it.
		return nil
	}
	return convRetireLegacyArchive(contextDir, base)
}

// convRetireLegacyArchive removes a legacy source whose converted same-ID copy
// is on disk. The converted copy is the readable one, so this never removes the
// only readable copy.
func convRetireLegacyArchive(contextDir, base string) error {
	for _, suffix := range []string{".jsonl", ".meta.json"} {
		p := filepath.Join(contextDir, base+suffix)
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("conversion: saved chat %q: retire %s: %w", base, p, err)
		}
	}
	// Best-effort: drop the directory once it is empty.
	_ = os.Remove(contextDir)
	return nil
}

// convArchiveAlreadyMaterialized reports whether the archive for this session
// already holds a completed model conversion: the backend window metadata is the
// completion mark, published last. The session's chat records share the file, so
// content alone says nothing. Converted model records WITHOUT the mark are a
// conflict (an interrupted append) and refuse. The second return is true when
// the earlier conversion retained a torn source (N2=B), which a later boot must
// not retire.
func convArchiveAlreadyMaterialized(store *ArchiveDayStore) (materialized bool, tornRetained bool, err error) {
	metaPath := filepath.Join(store.dir(), archiveBackendMetaFile)
	data, metaErr := os.ReadFile(metaPath)
	if metaErr == nil {
		var m archiveBackendMeta
		if jsonErr := json.Unmarshal(data, &m); jsonErr != nil {
			return false, false, jsonErr
		}
		return true, m.ConvSourceTorn, nil
	}
	if !errors.Is(metaErr, fs.ErrNotExist) {
		return false, false, metaErr
	}
	partial := false
	if scanErr := store.ScanAll(func(_ ArchiveAddress, rec ArchiveRecord) bool {
		if rec.ModelOrigin != "" {
			partial = true
			return false
		}
		return true
	}); scanErr != nil {
		return false, false, scanErr
	}
	if partial {
		return false, false, fmt.Errorf("destination already holds converted model records with no window metadata; refusing to overwrite")
	}
	return false, false, nil
}

// convTornTailDecision is the ONE isolated place the open N2 question lives
// (founder ruled B): tolerate a final line that has no closing newline and does
// not decode. If the founder ever reverses to A, this single function flips to
// `return false`. Callers must keep every other unprovable line refusing.
func convTornTailDecision(isLastLine, hasClosingNewline bool, decodeErr error) bool {
	return isLastLine && !hasClosingNewline && decodeErr != nil
}

// convBuildRecords decodes a legacy archive's bytes into addressed records.
// It returns the records, the byte offset of a tolerated torn tail (-1 if none),
// or a visible refusal error naming the offending byte position.
func convBuildRecords(id string, raw []byte, origin string) ([]ArchiveRecord, int, error) {
	var records []ArchiveRecord
	var open *convOpenAssistant
	off := 0
	for off < len(raw) {
		lineEnd := bytes.IndexByte(raw[off:], '\n')
		hasNL := lineEnd >= 0
		var line []byte
		if hasNL {
			line = raw[off : off+lineEnd]
		} else {
			line = raw[off:]
		}
		next := off + len(line)
		if hasNL {
			next++
		}
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			off = next
			continue
		}
		isLast := next >= len(raw)
		msg, decErr := convDecodeLegacyLine(trimmed)
		if decErr != nil {
			if convTornTailDecision(isLast, hasNL, decErr) {
				return records, off, nil
			}
			return nil, -1, fmt.Errorf("conversion: saved chat %q: undecodable archive line at byte %d: %w", id, off, decErr)
		}
		rec, nextOpen, err := convBuildRecord(id, off, msg, origin, &open)
		if err != nil {
			return nil, -1, err
		}
		open = nextOpen
		records = append(records, rec)
		off = next
	}
	return records, -1, nil
}

// convOpenAssistant tracks the assistant occurrence that issued tool calls still
// awaiting results (the occurrence join of Decision A/B). A user or assistant
// line closes it.
type convOpenAssistant struct {
	entryID string
	calls   map[string]bool
}

// convDecodeLegacyLine decodes one stored provider-message line. A line that is
// not valid JSON or has no role does not decode (D3(1)).
func convDecodeLegacyLine(line []byte) (providers.Message, error) {
	var archived memory.ArchivedMessage
	if err := json.Unmarshal(line, &archived); err != nil {
		return providers.Message{}, err
	}
	if archived.Role == "" {
		return providers.Message{}, fmt.Errorf("line has no role")
	}
	return archived.Message, nil
}

// convBuildRecord builds one addressed record from a decoded legacy message,
// applying the fixed CONV-P A D2 rule and the occurrence join.
func convBuildRecord(id string, offset int, msg providers.Message, origin string, open **convOpenAssistant) (ArchiveRecord, *convOpenAssistant, error) {
	// D3(4): a top-level tool-call name present in the source is foreign (the
	// default encoder drops it); refuse rather than accept a conflicting value.
	if err := convRejectForeignToolFields(id, offset, msg); err != nil {
		return ArchiveRecord{}, nil, err
	}
	mp, err := EncodeModelPayload(msg)
	if err != nil {
		return ArchiveRecord{}, nil, fmt.Errorf("conversion: saved chat %q: encode line at byte %d: %w", id, offset, err)
	}
	// D2: mirror the tool NAME from the saved function.name; leave the parsed
	// arguments map and the top-level thought_signature absent.
	for i := range mp.ToolCalls {
		tc := &mp.ToolCalls[i]
		if tc.Function == nil || tc.Function.Name == "" {
			return ArchiveRecord{}, nil, fmt.Errorf("conversion: saved chat %q: tool call %q at byte %d has no function.name to mirror", id, tc.ID, offset)
		}
		tc.Name = tc.Function.Name
		tc.ThoughtSignature = ""
		tc.Arguments = nil
	}

	rec := ArchiveRecord{
		TranscriptEntry: TranscriptEntry{
			ID:             "conv_" + id + "_" + fmt.Sprintf("%d", offset),
			ViewMembership: ViewMembershipModel,
		},
		ModelMessage: &mp,
		ModelOrigin:  origin,
	}

	switch msg.Role {
	case "assistant":
		calls := make(map[string]bool, len(mp.ToolCalls))
		for _, tc := range mp.ToolCalls {
			if tc.ID == "" {
				continue
			}
			if calls[tc.ID] {
				return ArchiveRecord{}, nil, fmt.Errorf("conversion: saved chat %q: assistant at byte %d has two calls with id %q", id, offset, tc.ID)
			}
			calls[tc.ID] = true
		}
		*open = &convOpenAssistant{entryID: rec.ID, calls: calls}
	case "user":
		*open = nil
	case "tool":
		if msg.ToolCallID == "" || *open == nil || !(*open).calls[msg.ToolCallID] {
			return ArchiveRecord{}, nil, fmt.Errorf("conversion: saved chat %q: tool result %q at byte %d has no provable producing assistant occurrence", id, msg.ToolCallID, offset)
		}
		rec.ToolResultFor = &ToolResultFor{AssistantEntryID: (*open).entryID, ToolCallID: msg.ToolCallID}
	}
	if err := rec.Validate(); err != nil {
		return ArchiveRecord{}, nil, fmt.Errorf("conversion: saved chat %q: %w", id, err)
	}
	return rec, *open, nil
}

// convRejectForeignToolFields refuses a legacy line whose tool_calls carry a
// top-level `name` key (a field the default encoder never wrote).
func convRejectForeignToolFields(id string, offset int, msg providers.Message) error {
	// EncodeModelPayload never reads a top-level name from providers.Message
	// (it is json:"-"); detection happens on the raw bytes instead, so this is a
	// guard against a message whose Name was populated out-of-band.
	for _, tc := range msg.ToolCalls {
		if tc.Name != "" {
			return fmt.Errorf("conversion: saved chat %q: tool call at byte %d carries a top-level name; source is not a default-encoder line", id, offset)
		}
	}
	return nil
}

// convLegacyProjectionRow is one projection row of the legacy sidecar, which
// named its chat tool_call record by a transcript LINE INDEX.
type convLegacyProjectionRow struct {
	ToolCallID     string                 `json:"tool_call_id"`
	ArchiveLine    int                    `json:"archive_line"`
	State          memory.ProjectionState `json:"state,omitempty"`
	SourceRunes    *int                   `json:"retained_source_runes,omitempty"`
	TranscriptLine *int                   `json:"transcript_line,omitempty"`
}

// convLegacyMeta is the legacy `.context/<key>.meta.json` sidecar (memory's
// sessionMeta), decoded here so CONV carries the window state forward.
type convLegacyMeta struct {
	Key        string                    `json:"key"`
	Skip       int                       `json:"skip"`
	Count      int                       `json:"count"`
	Projection []convLegacyProjectionRow `json:"projection,omitempty"`
	Hydrated   bool                      `json:"hydrated,omitempty"`
	AnchorLine *int                      `json:"anchor_archive_line,omitempty"`
	Retracted  []memory.ArchiveSpan      `json:"retracted,omitempty"`
}

func convReadLegacyMeta(path string) (convLegacyMeta, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return convLegacyMeta{}, nil
	}
	if err != nil {
		return convLegacyMeta{}, err
	}
	var m convLegacyMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return convLegacyMeta{}, err
	}
	return m, nil
}

// convTranslateMeta carries the legacy window state onto the new backend meta.
// A projection row's legacy transcript line index becomes the exact address of
// that record in the converted partitions; an index that does not resolve to a
// record carrying the row's tool call is dropped with a WARN, never guessed.
func convTranslateMeta(chat string, m convLegacyMeta, convertedCount int, addrs []ArchiveAddress, callsAt map[int]string) archiveBackendMeta {
	out := archiveBackendMeta{
		Skip:       m.Skip,
		Count:      convertedCount,
		AnchorLine: m.AnchorLine,
		Retracted:  m.Retracted,
	}
	for _, r := range m.Projection {
		row := archiveProjectionRow{ToolCallID: r.ToolCallID, ArchiveLine: r.ArchiveLine, State: r.State, SourceRunes: r.SourceRunes}
		if r.TranscriptLine != nil {
			k := *r.TranscriptLine
			if k >= 0 && k < len(addrs) && addrs[k].EntryID != "" && strings.Contains(callsAt[k], "\x00"+r.ToolCallID+"\x00") {
				a := addrs[k]
				row.TranscriptAddr = &a
			} else {
				slog.Warn("conversion: dropped an unresolvable transcript identity",
					"chat", chat, "tool_call_id", r.ToolCallID, "transcript_line", k)
			}
		}
		out.Projection = append(out.Projection, row)
	}
	if out.Count == 0 {
		out.Count = m.Count
	}
	return out
}

// convWriteBackendMeta publishes the new backend meta atomically.
func convWriteBackendMeta(dir string, meta archiveBackendMeta) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(filepath.Join(dir, archiveBackendMetaFile), data, 0o600)
}
