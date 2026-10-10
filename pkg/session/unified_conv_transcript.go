// unified_conv_transcript.go: the chat half of the one-time CONV saved-chat
// cutover for the single append-only archive (session-core U2, effects design
// S5; spec CONV).
//
// Before the cutover a session directory held chat lines with no membership, a
// separate provenance.jsonl, write-phase markers on provenance-paired lines and
// (in the legacy model archive's window metadata) transcript identities that were
// line indices. This pass normalizes what it finds ONCE, before the store
// serves anything:
//
//   - a chat line with no view_membership (or the earlier-slice "both" default
//     on a line that holds no model payload) gets membership "chat";
//   - a provenance-paired line folds its provenance.jsonl record into the line's
//     private source member, and provenance.jsonl is retired after the converted
//     partitions are published;
//   - a marker line with NO provenance record is the residue of an unfinished
//     paired save: it is never shown. It is kept in place, byte-position and
//     all, as an inert model-view record without a payload, so every other
//     line's index and address is unchanged (an excluded leftover, like an
//     unpublished archive record);
//   - the legacy `transcript_line` index of a projection row is translated to
//     the exact address of that line in the converted partitions (an index that
//     does not resolve to a record carrying that tool call is dropped with a
//     WARN naming the chat and call, never guessed).
//
// Lines that do not decode are kept verbatim. A completed pass rewrites nothing,
// and a repeated pass is a no-op.
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
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// convLegacyProvenanceFile is the retired per-session provenance record file.
const convLegacyProvenanceFile = "provenance.jsonl"

// convLegacyProvenance is one record of the retired provenance.jsonl.
type convLegacyProvenance struct {
	MessageID string `json:"message_id"`
	Principal string `json:"principal,omitempty"`
}

// convConvergeTranscripts normalizes every saved chat's chat partitions.
func convConvergeTranscripts(baseDir string) error {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("conversion: read sessions dir: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == convContextDir {
			continue
		}
		if err := convConvergeSessionTranscript(filepath.Join(baseDir, e.Name()), e.Name()); err != nil {
			return err
		}
	}
	return nil
}

// convSessionPartitions lists a session directory's partition files in
// chronological order with their addressing keys. mark is the day the current
// file holds ("" when it has no mark yet).
func convSessionPartitions(dir string) (parts []partitionRef, mark string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, "", err
	}
	data, rerr := os.ReadFile(filepath.Join(dir, transcriptDayMarkFile))
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return nil, "", rerr
	}
	mark = strings.TrimSpace(string(data))
	for _, name := range rolledPartitionNames(entries) {
		parts = append(parts, partitionRef{key: strings.TrimSuffix(name, ".jsonl"), path: filepath.Join(dir, name)})
	}
	cur := filepath.Join(dir, transcriptFileName)
	if _, statErr := os.Stat(cur); statErr == nil {
		parts = append(parts, partitionRef{key: mark, path: cur})
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, "", statErr
	}
	return parts, mark, nil
}

func convConvergeSessionTranscript(dir, id string) error {
	parts, mark, err := convSessionPartitions(dir)
	if err != nil {
		return fmt.Errorf("conversion: saved chat %q: list partitions: %w", id, err)
	}
	if len(parts) == 0 {
		return nil
	}
	prov, err := convReadLegacyProvenance(dir, id)
	if err != nil {
		return err
	}
	var lastTS time.Time
	for _, part := range parts {
		data, rerr := os.ReadFile(part.path)
		if rerr != nil {
			return fmt.Errorf("conversion: saved chat %q: read %s: %w", id, filepath.Base(part.path), rerr)
		}
		out, changed, ts := convRewriteChatLines(data, prov)
		if ts.After(lastTS) {
			lastTS = ts
		}
		if !changed {
			continue
		}
		if werr := fileutil.WriteFileAtomic(part.path, out, 0o600); werr != nil {
			return fmt.Errorf("conversion: saved chat %q: publish %s: %w", id, filepath.Base(part.path), werr)
		}
	}
	if mark == "" {
		// A pre-partitioning file adopts the day of its newest record as the
		// current day (a file never splits), so the archive's addresses are keyed
		// from the first append on.
		day := lastTS
		if day.IsZero() {
			day = time.Now()
		}
		if werr := fileutil.WriteFileAtomic(filepath.Join(dir, transcriptDayMarkFile),
			[]byte(day.UTC().Format(transcriptDayLayout)+"\n"), 0o600); werr != nil {
			return fmt.Errorf("conversion: saved chat %q: write day mark: %w", id, werr)
		}
	}
	// Retire the provenance records only after every converted partition is
	// published: the folded copy is the readable one.
	if err := os.Remove(filepath.Join(dir, convLegacyProvenanceFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("conversion: saved chat %q: retire %s: %w", id, convLegacyProvenanceFile, err)
	}
	return nil
}

// convReadLegacyProvenance reads provenance.jsonl. A line that does not decode
// refuses the conversion visibly (the old store failed closed the same way).
func convReadLegacyProvenance(dir, id string) (map[string]convLegacyProvenance, error) {
	data, err := os.ReadFile(filepath.Join(dir, convLegacyProvenanceFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("conversion: saved chat %q: read %s: %w", id, convLegacyProvenanceFile, err)
	}
	out := map[string]convLegacyProvenance{}
	for n, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec convLegacyProvenance
		if jerr := json.Unmarshal(line, &rec); jerr != nil || rec.MessageID == "" {
			return nil, fmt.Errorf("conversion: saved chat %q: %s line %d does not decode; refusing cutover", id, convLegacyProvenanceFile, n+1)
		}
		out[rec.MessageID] = rec
	}
	return out, nil
}

// convRewriteChatLines returns the partition bytes with every chat line
// normalized, whether anything changed, and the newest record timestamp seen.
func convRewriteChatLines(data []byte, prov map[string]convLegacyProvenance) (out []byte, changed bool, newest time.Time) {
	lines := bytes.Split(data, []byte{'\n'})
	var buf bytes.Buffer
	buf.Grow(len(data) + 64)
	for i, raw := range lines {
		line := raw
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 {
			var obj map[string]json.RawMessage
			if json.Unmarshal(trimmed, &obj) == nil && obj != nil {
				if ts := convLineTimestamp(obj); ts.After(newest) {
					newest = ts
				}
				if edited, ok := convNormalizeChatObject(obj, prov); ok {
					if enc, merr := json.Marshal(edited); merr == nil {
						line = enc
						changed = true
					}
				}
			}
		}
		buf.Write(line)
		if i < len(lines)-1 {
			buf.WriteByte('\n')
		}
	}
	return buf.Bytes(), changed, newest
}

func convLineTimestamp(obj map[string]json.RawMessage) time.Time {
	var ts time.Time
	if raw, ok := obj["timestamp"]; ok {
		_ = json.Unmarshal(raw, &ts)
	}
	return ts
}

// convNormalizeChatObject applies the S5 rules to one decoded line and reports
// whether it changed.
func convNormalizeChatObject(obj map[string]json.RawMessage, prov map[string]convLegacyProvenance) (map[string]json.RawMessage, bool) {
	str := func(k string) string {
		var v string
		if raw, ok := obj[k]; ok {
			_ = json.Unmarshal(raw, &v)
		}
		return v
	}
	id, typ, membership := str("id"), str("type"), str("view_membership")
	_, hasModel := obj["model_message"]
	_, hasRef := obj["model_ref"]
	changed := false
	set := func(k, v string) {
		enc, _ := json.Marshal(v)
		obj[k] = enc
		changed = true
	}
	if typ != string(EntryTypeModelRef) && !hasModel && !hasRef &&
		(membership == "" || membership == ViewMembershipBoth) {
		set("view_membership", ViewMembershipChat)
		membership = ViewMembershipChat
	}
	var pending bool
	if raw, ok := obj["provenance_pending"]; ok {
		_ = json.Unmarshal(raw, &pending)
		delete(obj, "provenance_pending")
		changed = true
	}
	rec, paired := prov[id]
	switch {
	case paired && membership == ViewMembershipChat:
		if _, has := obj["source"]; !has {
			kind := "user"
			if rec.Principal == "" {
				kind = "anonymous"
			}
			enc, _ := json.Marshal(EntrySource{Kind: kind, Principal: rec.Principal})
			obj["source"] = enc
			changed = true
		}
	case pending && !paired:
		// An unfinished paired save: keep the line in place as an inert,
		// payload-free model-view record so no other index moves.
		set("view_membership", ViewMembershipModel)
	}
	return obj, changed
}

// convTranscriptLineAddrs returns the address of every non-empty line of the
// converted partitions, in order: the k-th entry is the record the legacy
// `transcript_line` index k named. Lines that do not decode keep their slot (a
// zero address), as the legacy index counted them.
func convTranscriptLineAddrs(dir string) ([]ArchiveAddress, map[int]string, error) {
	parts, _, err := convSessionPartitions(dir)
	if err != nil {
		return nil, nil, err
	}
	var addrs []ArchiveAddress
	callsAt := map[int]string{}
	for _, part := range parts {
		data, rerr := os.ReadFile(part.path)
		if rerr != nil {
			return nil, nil, rerr
		}
		for pos := 0; pos < len(data); {
			start := pos
			end := bytes.IndexByte(data[pos:], '\n')
			var raw []byte
			if end < 0 {
				raw, pos = data[pos:], len(data)
			} else {
				raw, pos = data[pos:pos+end], pos+end+1
			}
			line := bytes.TrimSpace(raw)
			if len(line) == 0 {
				continue
			}
			var rec ArchiveRecord
			if json.Unmarshal(line, &rec) != nil {
				addrs = append(addrs, ArchiveAddress{})
				continue
			}
			idx := len(addrs)
			addrs = append(addrs, ArchiveAddress{PartitionKey: part.key, ByteOffset: int64(start), EntryID: rec.ID})
			ids := make([]string, 0, len(rec.ToolCalls))
			for _, tc := range rec.ToolCalls {
				ids = append(ids, string(tc.ID))
			}
			callsAt[idx] = "\x00" + strings.Join(ids, "\x00") + "\x00"
		}
	}
	slog.Debug("conversion: indexed converted transcript lines", "dir", dir, "lines", len(addrs))
	return addrs, callsAt, nil
}
