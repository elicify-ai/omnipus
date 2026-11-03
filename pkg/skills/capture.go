package skills

// Content revision and the bounded capture walk (§7.3 contentRevision, §9.5
// snapshot domain, bounded capture, bounded instruction reads, instruction
// resolution within the captured domain).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Folder limits (§7.2 sizes, fixed by the spec). MaxSkillMarkdownBytes is
// the instruction limit.
const (
	MaxSkillFileBytes   int64 = 10485760
	MaxSkillFolderBytes int64 = 52428800
	MaxSkillFiles             = 1000
)

const (
	entryTypeFile    = ""
	entryTypeSymlink = "symlink"
	entryTypeSpecial = "special"
)

// captureBufferSize is the bounded-read buffer: consumption stops at the
// enforced limit plus at most one buffer (§9.5).
const captureBufferSize = 32 * 1024

// treeEntry is one versionable folder entry (§9.5 capture table).
type treeEntry struct {
	Path       string // slash-separated, relative to the skill folder
	Type       string // entryTypeFile | entryTypeSymlink | entryTypeSpecial
	Exec       bool
	Size       int64
	SHA256     string // content hash (file bytes, or the link target text)
	LinkTarget string
	ModeType   string // special entries only: the file-mode type
	blob       string // where the bytes live (temporary or final blob file)
	content    []byte // in-memory bytes (a single-file write), when set
}

func (e treeEntry) line() string {
	exec := "0"
	if e.Exec {
		exec = "1"
	}
	typ := e.Type
	if typ == entryTypeFile {
		typ = "file"
	}
	sum := e.SHA256
	if e.Type == entryTypeSpecial {
		sum = "special:" + e.ModeType
	}
	return e.Path + "\x00" + typ + "\x00" + exec + "\x00" + sum + "\n"
}

// revisionOfEntries is the content revision of a set of entries. Entries are
// hashed in path order, so the revision is independent of input order.
func revisionOfEntries(entries []treeEntry) string {
	sorted := make([]treeEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	for _, e := range sorted {
		_, _ = io.WriteString(h, e.line())
	}
	return hex.EncodeToString(h.Sum(nil))
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ContentRevision is §7.3's revision function: regular files hashed by
// relative path, executable bit and bytes; symlinks hashed as type + link
// target (never dereferenced); special entries as type; directories ignored;
// top-level .versions/, .omnipus-builtin, .skill-origin.json and
// .omnipus-identity skipped. ErrNotFound only when the folder is absent — a
// folder without SKILL.md is present.
func ContentRevision(dir string) (string, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("content revision: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("content revision: %s is not a folder", dir)
	}
	var entries []treeEntry
	err = walkSkillTree(dir, func(rel, abs string, d fs.DirEntry, fi os.FileInfo) error {
		e := treeEntry{Path: rel}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, rlErr := os.Readlink(abs)
			if rlErr != nil {
				return rlErr
			}
			e.Type, e.LinkTarget, e.SHA256, e.Size = entryTypeSymlink, target, sha256Hex([]byte(target)), int64(len(target))
		case fi.Mode().IsRegular():
			f, openErr := os.Open(abs)
			if openErr != nil {
				return openErr
			}
			h := sha256.New()
			n, copyErr := io.Copy(h, f)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			e.SHA256, e.Size, e.Exec = hex.EncodeToString(h.Sum(nil)), n, fi.Mode().Perm()&0o111 != 0
		default:
			e.Type, e.ModeType = entryTypeSpecial, fi.Mode().Type().String()
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("content revision: %w", err)
	}
	return revisionOfEntries(entries), nil
}

// walkSkillTree visits every non-directory entry of a skill folder in lexical
// order, skipping the top-level control entries. Symlinks are reported, never
// followed.
func walkSkillTree(dir string, visit func(rel, abs string, d fs.DirEntry, fi os.FileInfo) error) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !strings.Contains(rel, "/") && isControlEntry(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return visit(rel, p, d, fi)
	})
}

// captureLimitError is a §7.2 capture-time limit crossing.
type captureLimitError struct{ reason string }

func (e *captureLimitError) Error() string { return e.reason }

// instructionLimitError: the instruction file exceeds MaxSkillMarkdownBytes.
type instructionLimitError struct {
	rel string
	n   int64
}

func (e *instructionLimitError) Error() string {
	return fmt.Sprintf("the instruction file '%s' exceeds the limit of %s bytes (%d bytes)", e.rel, formatCount(MaxSkillMarkdownBytes), e.n)
}

func formatCount(n int64) string {
	s := fmt.Sprintf("%d", n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func perFileLimitReason(rel string, n int64) string {
	return fmt.Sprintf("'%s' exceeds the per-file limit of %s bytes (%d bytes); reduce or remove it and retry", rel, formatCount(MaxSkillFileBytes), n)
}

func totalLimitReason(n int64) string {
	return fmt.Sprintf("the folder exceeds the total size limit of %s bytes (%d bytes); reduce or remove content and retry", formatCount(MaxSkillFolderBytes), n)
}

func fileCountReason() string {
	return fmt.Sprintf("the folder contains more than %s files; reduce the file count and retry", formatCount(MaxSkillFiles))
}

// instructionState classifies the instruction file of an observation.
type instructionState int

const (
	instructionMissing instructionState = iota
	instructionRegular
	instructionLinked
	instructionUnresolved
)

// instructionRef is the instruction file's resolution against a folder
// (§9.5 captured-domain rule), decided without opening any target.
type instructionRef struct {
	state  instructionState
	rel    string // included entry whose bytes are the instructions
	target string // the link text, for refusals
}

// resolveInstructionOnDisk resolves dir/SKILL.md by Lstat/Readlink only.
func resolveInstructionOnDisk(dir string) (instructionRef, error) {
	p := filepath.Join(dir, "SKILL.md")
	fi, err := os.Lstat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return instructionRef{state: instructionMissing}, nil
		}
		return instructionRef{}, err
	}
	if fi.Mode().IsRegular() {
		return instructionRef{state: instructionRegular, rel: "SKILL.md"}, nil
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return instructionRef{state: instructionUnresolved, target: "SKILL.md"}, nil
	}
	target, err := os.Readlink(p)
	if err != nil {
		return instructionRef{}, err
	}
	rel, ok := linkedInstructionRel(target)
	if !ok {
		return instructionRef{state: instructionUnresolved, target: target}, nil
	}
	// A target that cannot be observed as a regular file of the skill is an
	// unresolved instruction (§5.10), reported to the caller as that state.
	if tfi, lerr := os.Lstat(filepath.Join(dir, filepath.FromSlash(rel))); lerr != nil || !tfi.Mode().IsRegular() {
		return instructionRef{state: instructionUnresolved, target: target}, nil //nolint:nilerr // the state carries the failure
	}
	return instructionRef{state: instructionLinked, rel: rel, target: target}, nil
}

// linkedInstructionRel maps a SKILL.md link target to the folder-relative
// entry it names, or false when the target is absolute, escaping or Omnipus
// control metadata.
func linkedInstructionRel(target string) (string, bool) {
	if target == "" || filepath.IsAbs(target) || path.IsAbs(filepath.ToSlash(target)) || filepath.VolumeName(target) != "" {
		return "", false
	}
	rel := path.Clean(filepath.ToSlash(target))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || rel == "SKILL.md" {
		return "", false
	}
	first := strings.SplitN(rel, "/", 2)[0]
	if isControlEntry(first) || isControlEntry(rel) {
		return "", false
	}
	return rel, true
}

// resolveInstructionInEntries resolves SKILL.md against an entry table — a
// manifest, a would-be state or a capture.
func resolveInstructionInEntries(entries map[string]treeEntry) instructionRef {
	e, ok := entries["SKILL.md"]
	if !ok {
		return instructionRef{state: instructionMissing}
	}
	switch e.Type {
	case entryTypeFile:
		return instructionRef{state: instructionRegular, rel: "SKILL.md"}
	case entryTypeSymlink:
		rel, ok := linkedInstructionRel(e.LinkTarget)
		if !ok {
			return instructionRef{state: instructionUnresolved, target: e.LinkTarget}
		}
		t, ok := entries[rel]
		if !ok || t.Type != entryTypeFile {
			return instructionRef{state: instructionUnresolved, target: e.LinkTarget}
		}
		return instructionRef{state: instructionLinked, rel: rel, target: e.LinkTarget}
	default:
		return instructionRef{state: instructionUnresolved, target: "SKILL.md"}
	}
}

// captureOptions configures one bounded capture walk.
type captureOptions struct {
	op      string // probe op for every open
	blobDir string // "" = hash only; else every blob is written here
}

// captureResult is one observation of a skill folder.
type captureResult struct {
	entries     []treeEntry
	revision    string
	instr       instructionRef
	instruction []byte // the instruction entry's bytes (bounded), when read
	special     *treeEntry
}

func (r *captureResult) entryMap() map[string]treeEntry {
	m := make(map[string]treeEntry, len(r.entries))
	for _, e := range r.entries {
		m[e.Path] = e
	}
	return m
}

// captureTree walks dir once in bounded memory (§9.5): each regular file is
// streamed through a bounded buffer, hashed and — when opts.blobDir is set —
// written to a temporary blob in the same pass; symlinks are captured as
// their target text; the §7.2 limits are enforced against consumed counts.
// A limit crossing returns *captureLimitError; an instruction file beyond
// MaxSkillMarkdownBytes returns *instructionLimitError.
func captureTree(dir string, opts captureOptions) (*captureResult, error) {
	instr, err := resolveInstructionOnDisk(dir)
	if err != nil {
		return nil, err
	}
	res := &captureResult{instr: instr}
	var total int64
	count := 0
	written := map[string]string{}
	err = walkSkillTree(dir, func(rel, abs string, d fs.DirEntry, fi os.FileInfo) error {
		count++
		if count > MaxSkillFiles {
			return &captureLimitError{reason: fileCountReason()}
		}
		e := treeEntry{Path: rel}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, rlErr := os.Readlink(abs)
			if rlErr != nil {
				return rlErr
			}
			e.Type, e.LinkTarget, e.SHA256, e.Size = entryTypeSymlink, target, sha256Hex([]byte(target)), int64(len(target))
			if opts.blobDir != "" {
				blob, wErr := writeTempBlob(opts.blobDir, e.SHA256, []byte(target), written)
				if wErr != nil {
					return wErr
				}
				e.blob = blob
			}
		case fi.Mode().IsRegular():
			isInstr := (instr.state == instructionRegular || instr.state == instructionLinked) && rel == instr.rel
			limit := MaxSkillFileBytes
			if isInstr {
				limit = MaxSkillMarkdownBytes
			}
			sum, n, kept, blob, readErr := streamEntry(abs, opts, limit, total, isInstr, written)
			if readErr != nil {
				var lim *entryOverLimitError
				if errors.As(readErr, &lim) {
					if isInstr {
						return &instructionLimitError{rel: rel, n: lim.n}
					}
					if lim.total {
						return &captureLimitError{reason: totalLimitReason(lim.n)}
					}
					return &captureLimitError{reason: perFileLimitReason(rel, lim.n)}
				}
				return readErr
			}
			total += n
			e.SHA256, e.Size, e.Exec, e.blob = sum, n, fi.Mode().Perm()&0o111 != 0, blob
			if isInstr {
				res.instruction = kept
			}
		default:
			e.Type, e.ModeType = entryTypeSpecial, fi.Mode().Type().String()
			if res.special == nil {
				sp := e
				res.special = &sp
			}
		}
		res.entries = append(res.entries, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.revision = revisionOfEntries(res.entries)
	return res, nil
}

// entryOverLimitError is returned by streamEntry when a limit was crossed.
type entryOverLimitError struct {
	n     int64
	total bool
}

func (e *entryOverLimitError) Error() string { return "limit crossed" }

// streamEntry consumes one regular file through a bounded buffer.
func streamEntry(abs string, opts captureOptions, limit, totalBefore int64, keep bool, written map[string]string) (string, int64, []byte, string, error) {
	probeOpen(opts.op, abs)
	f, err := os.Open(abs)
	if err != nil {
		return "", 0, nil, "", err
	}
	defer f.Close()
	var tmp *os.File
	if opts.blobDir != "" {
		tmp, err = os.CreateTemp(opts.blobDir, "part-*")
		if err != nil {
			return "", 0, nil, "", err
		}
	}
	discardTmp := func() {
		if tmp != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}
	h := sha256.New()
	buf := make([]byte, captureBufferSize)
	var n int64
	var kept []byte
	for {
		k, rErr := f.Read(buf)
		if k > 0 {
			n += int64(k)
			probeConsumed(abs, n)
			if n > limit {
				discardTmp()
				return "", 0, nil, "", &entryOverLimitError{n: n}
			}
			if totalBefore+n > MaxSkillFolderBytes {
				discardTmp()
				return "", 0, nil, "", &entryOverLimitError{n: totalBefore + n, total: true}
			}
			h.Write(buf[:k])
			if keep {
				kept = append(kept, buf[:k]...)
			}
			if tmp != nil {
				if _, wErr := tmp.Write(buf[:k]); wErr != nil {
					discardTmp()
					return "", 0, nil, "", wErr
				}
			}
		}
		if rErr == io.EOF {
			break
		}
		if rErr != nil {
			discardTmp()
			return "", 0, nil, "", rErr
		}
	}
	sum := hex.EncodeToString(h.Sum(nil))
	blob := ""
	if tmp != nil {
		if err = tmp.Close(); err != nil {
			_ = os.Remove(tmp.Name())
			return "", 0, nil, "", err
		}
		blob, err = placeTempBlob(opts.blobDir, sum, tmp.Name(), written)
		if err != nil {
			return "", 0, nil, "", err
		}
	}
	return sum, n, kept, blob, nil
}

// placeTempBlob names a finished temporary blob by its hash inside the
// attempt directory (one temporary per distinct content).
func placeTempBlob(blobDir, sum, tmpPath string, written map[string]string) (string, error) {
	if existing, ok := written[sum]; ok {
		if err := os.Remove(tmpPath); err != nil {
			return "", err
		}
		return existing, nil
	}
	final := filepath.Join(blobDir, sum)
	if err := os.Rename(tmpPath, final); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	written[sum] = final
	return final, nil
}

// writeTempBlob writes small in-memory blob bytes into the attempt directory.
func writeTempBlob(blobDir, sum string, data []byte, written map[string]string) (string, error) {
	if existing, ok := written[sum]; ok {
		return existing, nil
	}
	final := filepath.Join(blobDir, sum)
	if err := os.WriteFile(final, data, 0o644); err != nil {
		return "", err
	}
	written[sum] = final
	return final, nil
}

// readBoundedInstruction reads an instruction file directly (op
// "instruction" or "metadata") through the bounded reader.
func readBoundedInstruction(op, abs, rel string) ([]byte, error) {
	probeOpen(op, abs)
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, captureBufferSize)
	var out []byte
	var n int64
	for {
		k, rErr := f.Read(buf)
		if k > 0 {
			n += int64(k)
			probeConsumed(abs, n)
			if n > MaxSkillMarkdownBytes {
				return nil, &instructionLimitError{rel: rel, n: n}
			}
			out = append(out, buf[:k]...)
		}
		if rErr == io.EOF {
			return out, nil
		}
		if rErr != nil {
			return nil, rErr
		}
	}
}
