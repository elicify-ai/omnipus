package skills

// The per-skill history store (§5.11) and the capture-attempt lifecycle
// (§9.5 rules 1–8).

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// Manifest kinds (§5.11).
const (
	manifestBaseline       = "baseline"
	manifestEdit           = "edit"
	manifestCreate         = "create"
	manifestDeleteFile     = "delete_file"
	manifestDeleteSkill    = "delete_skill"
	manifestRestore        = "restore"
	manifestInstall        = "install"
	manifestInstallReplace = "install_replace"
	manifestExternalChange = "external_change"
)

// manifestTimeFormat is §5.11's UTC timestamp format.
const manifestTimeFormat = "20060102T150405Z"

// SkillHistoryRoot derives §5.11's history location from the skills root:
// `<parent-of-skills>/system/skill-history/`.
func SkillHistoryRoot(skillsRoot string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(skillsRoot)), "system", "skill-history")
}

// manifestFile is one files[] entry of a version manifest.
type manifestFile struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Exec       bool   `json:"exec,omitempty"`
	Type       string `json:"type,omitempty"`
	LinkTarget string `json:"link_target,omitempty"`
}

// versionManifest is §5.11's versions/<id>.json.
type versionManifest struct {
	ID           string         `json:"id"`
	Parent       string         `json:"parent,omitempty"`
	CreatedAt    string         `json:"created_at"`
	Slug         string         `json:"slug"`
	Kind         string         `json:"kind"`
	Actor        string         `json:"actor"`
	Tool         string         `json:"tool,omitempty"`
	SessionID    string         `json:"session_id,omitempty"`
	WorkspaceID  string         `json:"workspace_id,omitempty"`
	Files        []manifestFile `json:"files"`
	Paths        []string       `json:"paths"`
	PathsTotal   int            `json:"paths_total"`
	Revision     string         `json:"revision"`
	RestoredFrom string         `json:"restored_from,omitempty"`
}

// historyStore is one skill key's history directory.
type historyStore struct{ dir string }

func historyFor(skillsRoot, slug string) historyStore {
	return historyStore{dir: filepath.Join(SkillHistoryRoot(skillsRoot), SkillKey(slug))}
}

func (h historyStore) versionsDir() string { return filepath.Join(h.dir, "versions") }
func (h historyStore) blobsDir() string    { return filepath.Join(h.dir, "blobs") }
func (h historyStore) blobPath(sum string) string {
	return filepath.Join(h.blobsDir(), sum)
}

// versionSeq parses the sequence number of a version id (v<6 digits>-<ts>).
func versionSeq(id string) (int, bool) {
	if !strings.HasPrefix(id, "v") {
		return 0, false
	}
	digits, _, ok := strings.Cut(id[1:], "-")
	if !ok || digits == "" {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return n, true
}

// ids lists the manifest ids, oldest first (by sequence).
func (h historyStore) ids() ([]string, error) {
	entries, err := os.ReadDir(h.versionsDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("list skill versions: %w", err)
	}
	var ids []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		id := strings.TrimSuffix(name, ".json")
		if _, ok := versionSeq(id); ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, _ := versionSeq(ids[i])
		b, _ := versionSeq(ids[j])
		if a != b {
			return a < b
		}
		return ids[i] < ids[j]
	})
	return ids, nil
}

func (h historyStore) load(id string) (*versionManifest, error) {
	raw, err := os.ReadFile(filepath.Join(h.versionsDir(), id+".json"))
	if err != nil {
		return nil, err
	}
	var m versionManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("read skill version %s: %w", id, err)
	}
	return &m, nil
}

// newest returns the head manifest (highest sequence), or nil.
func (h historyStore) newest() (*versionManifest, error) {
	ids, err := h.ids()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	return h.load(ids[len(ids)-1])
}

// NewestVersion exposes a skill key's head manifest id and kind (empty when
// the key has no history).
func NewestVersion(skillsRoot, slug string) (id, kind string, err error) {
	m, err := historyFor(skillsRoot, slug).newest()
	if err != nil || m == nil {
		return "", "", err
	}
	return m.ID, m.Kind, nil
}

func nextVersionID(newest *versionManifest, now time.Time) string {
	seq := 1
	if newest != nil {
		if n, ok := versionSeq(newest.ID); ok {
			seq = n + 1
		}
	}
	return fmt.Sprintf("v%06d-%s", seq, now.UTC().Format(manifestTimeFormat))
}

// entriesFromManifest converts a manifest's files into tree entries whose
// bytes live in the store's final blobs.
func (h historyStore) entriesFromManifest(m *versionManifest) []treeEntry {
	out := make([]treeEntry, 0, len(m.Files))
	for _, f := range m.Files {
		e := treeEntry{Path: f.Path, Type: f.Type, Exec: f.Exec, Size: f.Size, SHA256: f.SHA256, LinkTarget: f.LinkTarget}
		if f.Type == "file" {
			e.Type = entryTypeFile
		}
		if e.Type == entryTypeSpecial {
			e.ModeType = f.LinkTarget
		}
		if f.SHA256 != "" {
			e.blob = h.blobPath(f.SHA256)
		}
		out = append(out, e)
	}
	return out
}

func manifestFilesOf(entries []treeEntry) []manifestFile {
	sorted := make([]treeEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	out := make([]manifestFile, 0, len(sorted))
	for _, e := range sorted {
		f := manifestFile{Path: e.Path, SHA256: e.SHA256, Size: e.Size, Exec: e.Exec, Type: e.Type, LinkTarget: e.LinkTarget}
		if e.Type == entryTypeSpecial {
			f.LinkTarget = ""
		}
		out = append(out, f)
	}
	return out
}

// diffPaths lists the paths that differ between two states (§5.11 paths).
func diffPaths(before, after []treeEntry) []string {
	b := map[string]string{}
	for _, e := range before {
		b[e.Path] = e.line()
	}
	set := map[string]bool{}
	for _, e := range after {
		if b[e.Path] != e.line() {
			set[e.Path] = true
		}
		delete(b, e.Path)
	}
	for p := range b {
		set[p] = true
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func allPaths(entries []treeEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	sort.Strings(out)
	return out
}

// writeManifest writes m atomically through the manifest fault seam.
func (h historyStore) writeManifest(m *versionManifest) error {
	path := filepath.Join(h.versionsDir(), m.ID+".json")
	if f := FaultManifestWrite; f != nil {
		if err := f(path); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.WriteFileAtomic(path, raw, 0o644)
}

// captureAttempt owns one attempt's temporary blobs and the finals it
// created (the in-memory ledger, §9.5 rule 2).
type captureAttempt struct {
	hist           historyStore
	dir            string
	createdHistory bool
	ledger         []string
}

func newCaptureAttempt(h historyStore) (*captureAttempt, error) {
	_, statErr := os.Stat(h.dir)
	created := errors.Is(statErr, os.ErrNotExist)
	if err := os.MkdirAll(h.blobsDir(), 0o755); err != nil {
		return nil, fmt.Errorf("create skill history: %w", err)
	}
	dir, err := os.MkdirTemp(h.blobsDir(), ".incoming-")
	if err != nil {
		return nil, fmt.Errorf("create capture scratch: %w", err)
	}
	return &captureAttempt{hist: h, dir: dir, createdHistory: created}, nil
}

// addContent stores in-memory bytes as a temporary blob of this attempt.
func (a *captureAttempt) addContent(data []byte) (string, string, error) {
	sum := sha256Hex(data)
	p := filepath.Join(a.dir, sum)
	if _, err := os.Stat(p); err == nil {
		return sum, p, nil
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		return "", "", fmt.Errorf("write capture scratch: %w", err)
	}
	return sum, p, nil
}

// finalize promotes every temporary blob the entries reference into
// blobs/<sha256> (an existing final is kept, the temporary discarded) and
// records the finals it created. Entries are updated to the final paths.
func (a *captureAttempt) finalize(entries []treeEntry) error {
	for i := range entries {
		e := &entries[i]
		if e.blob == "" || filepath.Dir(e.blob) != a.dir {
			continue
		}
		final := a.hist.blobPath(e.SHA256)
		if _, err := os.Lstat(final); err == nil {
			e.blob = final
			continue
		}
		if f := FaultBlobWrite; f != nil {
			if err := f(final); err != nil {
				return err
			}
		}
		if err := os.Rename(e.blob, final); err != nil {
			if _, statErr := os.Lstat(final); statErr == nil {
				e.blob = final
				continue
			}
			return fmt.Errorf("finalize blob: %w", err)
		}
		a.ledger = append(a.ledger, final)
		for j := i + 1; j < len(entries); j++ {
			if entries[j].blob == e.blob {
				entries[j].blob = final
			}
		}
		e.blob = final
	}
	return nil
}

// abort removes the attempt's temporaries and exactly the finals its ledger
// recorded (§9.5 rule 3). A history directory this attempt created and that
// holds no manifest is removed too.
func (a *captureAttempt) abort() error {
	var errs []error
	for _, final := range a.ledger {
		if err := os.Remove(final); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	a.ledger = nil
	if err := a.removeScratch(); err != nil {
		errs = append(errs, err)
	}
	if a.createdHistory && len(errs) == 0 {
		if ids, err := a.hist.ids(); err == nil && len(ids) == 0 {
			if err := os.RemoveAll(a.hist.dir); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// removeScratch removes the attempt's temporary directory (§9.5 rule 4/5).
func (a *captureAttempt) removeScratch() error {
	if f := FaultStagingRemove; f != nil {
		if err := f(a.dir); err != nil {
			return err
		}
	}
	return os.RemoveAll(a.dir)
}

// committed is called once the manifest referencing the finals committed:
// the ledger is no longer the attempt's to remove.
func (a *captureAttempt) committed() { a.ledger = nil }
