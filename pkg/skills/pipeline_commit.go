package skills

// Steps 5–7 of the change pipeline: folder publication through
// skills/.swap/<slug>, the version commit (finalize-then-manifest), the
// finishing step and §7.6's outcome texts.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// commitSpec describes the version step 6 records.
type commitSpec struct {
	kind         string
	entries      []treeEntry
	attempt      *captureAttempt // owns the new blobs' temporaries (nil = none)
	paths        []string
	restoredFrom string
	swap         string // skills/.swap/<slug> to remove at finish ("" = none)
}

// publishFolderAndCommit is step 5 for a staged folder (restore, install,
// copy) followed by steps 6–7. A present live folder moves to
// skills/.swap/<slug> first; a failed publication puts it back (§7.6 class 1)
// or reports the loss (class 1b).
func (p *pipeline) publishFolderAndCommit(st *state, staged string, attempt *captureAttempt, spec commitSpec) (PublishOutcome, error) {
	abort := func() string {
		if attempt == nil {
			return ""
		}
		return abortSuffix(attempt.abort())
	}
	if st.present {
		swap, err := p.prepareSwap(st.slug)
		if err != nil {
			return refusedOutcome(), p.storageErrorText(st.slug, err, abort())
		}
		if err := forwardRename(st.dir, swap); err != nil {
			return refusedOutcome(), p.storageErrorText(st.slug, err, abort())
		}
		if err := renamePublishedSkill(staged, st.dir); err != nil {
			if putErr := putBackRename(swap, st.dir); putErr != nil {
				return p.lostPrevious(st, errors.Join(err, putErr), abort())
			}
			return refusedOutcome(), p.storageErrorText(st.slug, err, abort())
		}
		spec.swap = swap
	} else {
		if err := os.MkdirAll(p.root, 0o755); err != nil {
			return refusedOutcome(), p.storageErrorText(st.slug, err, abort())
		}
		if err := renamePublishedSkill(staged, st.dir); err != nil {
			return refusedOutcome(), p.storageErrorText(st.slug, err, abort())
		}
	}
	return p.commitAndFinish(st, spec)
}

// lostPrevious is §7.6 class 1b: the replacement failed and the previous
// folder could not be put back; it waits in .swap for §9.3.
func (p *pipeline) lostPrevious(st *state, err error, suffix string) (PublishOutcome, error) {
	bumpGeneration()
	text := "replacement publication failed and the previous package could not be restored; no live package is available. "
	if reason := p.predecessorGate(st); reason != "" {
		text += fmt.Sprintf("The previous state is version %s, which cannot be restored (%s); exact undo through restore is unavailable — repair the skill or restore an older eligible version (get_skill fields:[\"versions\"]).", st.head.ID, reason)
	} else {
		text += fmt.Sprintf("The previous state is version %s; restore it.", st.head.ID)
	}
	text += fmt.Sprintf(" (%v%s)", err, suffix)
	auditPartial(p.req, st.slug, p.req.Kind, err.Error())
	return PublishOutcome{PersistenceStatus: PersistencePartial, ActivationStatus: ActivationNotAttempted, ChangedFields: []string{"installed"}, ErrorStage: PublicationErrorStageRestorePrevious, Message: text},
		&ChangeError{Code: ErrSkillPartiallySaved, Text: text, cause: err}
}

// commitAndFinish runs step 6 and, on success, step 7. A step-6 failure after
// publication is §7.6 class 2 / 2-create.
func (p *pipeline) commitAndFinish(st *state, spec commitSpec) (PublishOutcome, error) {
	m, err := p.commit(st, spec)
	if err != nil {
		return p.afterPublishFailure(st, err, revisionOfEntries(spec.entries), nil)
	}
	if spec.swap != "" {
		p.removeSwap(spec.swap)
	}
	return p.finish(st, m, m.Revision)
}

// commit is step 6: finalize the attempt's blobs, then write the manifest. On
// any failure the attempt removes its temporaries and exactly the finals it
// created (§9.5 rule 3) before the error is returned.
func (p *pipeline) commit(st *state, spec commitSpec) (*versionManifest, error) {
	parentID := ""
	if st.head != nil {
		parentID = st.head.ID
	}
	paths := spec.paths
	if paths == nil {
		paths = []string{}
	}
	m := &versionManifest{
		ID: nextVersionID(st.head, p.now), Parent: parentID, CreatedAt: p.now.Format(manifestTimeFormat),
		Slug: st.slug, Kind: spec.kind, Actor: p.req.Actor.label(), Tool: p.req.Actor.Tool,
		SessionID: p.req.Actor.SessionID, WorkspaceID: p.req.Actor.WorkspaceID,
		Files: manifestFilesOf(spec.entries), Paths: paths, PathsTotal: len(paths), RestoredFrom: spec.restoredFrom,
	}
	if spec.kind != manifestDeleteSkill {
		m.Revision = revisionOfEntries(spec.entries)
	}
	if spec.attempt != nil {
		if err := spec.attempt.finalize(spec.entries); err != nil {
			return nil, suffixedError{err: err, suffix: abortSuffix(spec.attempt.abort())}
		}
	}
	if err := st.hist.writeManifest(m); err != nil {
		if spec.attempt != nil {
			return nil, suffixedError{err: err, suffix: abortSuffix(spec.attempt.abort())}
		}
		return nil, err
	}
	if spec.attempt != nil {
		spec.attempt.committed()
		if err := spec.attempt.removeScratch(); err != nil {
			p.warnings = append(p.warnings, "capture scratch cleanup failed: "+err.Error())
		}
	}
	return m, nil
}

// suffixedError carries a cleanup-failure suffix next to the primary error.
type suffixedError struct {
	err    error
	suffix string
}

func (e suffixedError) Error() string { return e.err.Error() + e.suffix }
func (e suffixedError) Unwrap() error { return e.err }

// afterPublishFailure is §7.6 class 2 (live was present) or 2-create-i/ii
// (live was absent): the change is live, its version record is not.
func (p *pipeline) afterPublishFailure(st *state, err error, newRev string, attempt *captureAttempt) (PublishOutcome, error) {
	reason := err.Error()
	if attempt != nil {
		reason += abortSuffix(attempt.abort())
	}
	prefix := fmt.Sprintf("the change to '%s' is live, but its version record could not be written (%s).", st.slug, reason)
	var text string
	switch {
	case st.present:
		if gate := p.predecessorGate(st); gate != "" {
			text = fmt.Sprintf("%s The previous state is version %s, which cannot be restored (%s); exact undo through restore is unavailable — repair the skill or restore an older eligible version (get_skill fields:[\"versions\"]). Read the skill again before the next change.", prefix, st.head.ID, gate)
		} else {
			text = fmt.Sprintf("%s The previous state is version %s; restore it to undo. Read the skill again before the next change.", prefix, st.head.ID)
		}
	case st.before == nil:
		text = prefix + " No version history exists for this skill; remove the skill with edit_skill action=delete (or REST DELETE) to undo. Read the skill again before the next change."
	default:
		note := "the folder had been removed outside Omnipus"
		if st.before.Kind == manifestDeleteSkill {
			note = "the skill had been deleted"
		}
		text = fmt.Sprintf("%s The previous live state was absent (%s); the skill's earlier history is unchanged. To undo, remove the skill with edit_skill action=delete (or REST DELETE). Read the skill again before the next change.", prefix, note)
	}
	bumpGeneration()
	auditPartial(p.req, st.slug, p.req.Kind, reason)
	return PublishOutcome{Revision: newRev, PersistenceStatus: PersistencePartial, ActivationStatus: ActivationActive, ChangedFields: []string{"installed"}, Message: text},
		&ChangeError{Code: ErrSkillPartiallySaved, Text: text, cause: err}
}

// finish is step 7's outcome: the undo anchor (§5.5), class-3 warnings, the
// audit line and the generation bump.
func (p *pipeline) finish(st *state, m *versionManifest, rev string) (PublishOutcome, error) {
	msg := fmt.Sprintf("saved as skill version %s; %s", m.ID, p.undoSuffix(st))
	warning := ""
	if len(p.warnings) > 0 {
		warning = fmt.Sprintf("saved as skill version %s; %s", m.ID, strings.Join(p.warnings, "; "))
	}
	auditCommitted(p.req, st, m)
	bumpGeneration()
	return PublishOutcome{Revision: rev, PersistenceStatus: PersistenceComplete, ActivationStatus: ActivationActive, ChangedFields: []string{"installed"}, Message: msg, Warning: warning}, nil
}

// undoSuffix is §5.5's five-variant undo anchor, keyed to the pre-operation
// live state and the complete restore gate on the predecessor.
func (p *pipeline) undoSuffix(st *state) string {
	switch {
	case st.present:
		if reason := p.predecessorGate(st); reason != "" {
			return fmt.Sprintf("the version before it is %s, which cannot be restored (%s); exact undo through restore is unavailable — repair the skill (e.g. write a valid SKILL.md) or restore an older eligible version (get_skill fields:[\"versions\"])", st.head.ID, reason)
		}
		return "the version before it is " + st.head.ID
	case st.before == nil:
		return "there is no earlier version; to undo, delete this skill (edit_skill action=delete)"
	case st.before.Kind == manifestDeleteSkill:
		return fmt.Sprintf("it follows the recorded deletion (version %s, retained but not restorable); to undo, delete this skill with edit_skill action=delete", st.before.ID)
	default:
		return fmt.Sprintf("it follows an outside removal (version %s, retained); to undo, delete this skill with edit_skill action=delete", st.before.ID)
	}
}

// predecessorGate evaluates the complete §7.2 restore gate on the pre-change
// version (step 1 guarantees it equals the live state). "" = restorable.
func (p *pipeline) predecessorGate(st *state) string {
	secrets, r := p.loadSecretsOnce()
	if r != nil {
		return r.reason
	}
	if g := restoreGate(st.slug, st.liveMap(), secrets); g != nil {
		return g.reason
	}
	return ""
}

// prepareSwap returns skills/.swap/<slug>, removing a stale leftover first —
// safe because the live folder is present (§9.3's rule for that state).
func (p *pipeline) prepareSwap(slug string) (string, error) {
	dir := filepath.Join(p.root, ".swap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	swap := filepath.Join(dir, slug)
	if fileExists(swap) {
		if err := os.RemoveAll(swap); err != nil {
			return "", fmt.Errorf("remove stale %s: %w", swap, err)
		}
	}
	return swap, nil
}

func forwardRename(oldpath, newpath string) error {
	if f := FaultForwardRename; f != nil {
		if err := f(oldpath, newpath); err != nil {
			return err
		}
	}
	return os.Rename(oldpath, newpath)
}

func putBackRename(oldpath, newpath string) error {
	if f := FaultPutBackRename; f != nil {
		if err := f(oldpath, newpath); err != nil {
			return err
		}
	}
	return os.Rename(oldpath, newpath)
}

// removeSwap is step 7's .swap disposal; a failure is a class-3 warning.
func (p *pipeline) removeSwap(swap string) {
	err := func() error {
		if f := FaultSwapRemove; f != nil {
			if err := f(swap); err != nil {
				return err
			}
		}
		return os.RemoveAll(swap)
	}()
	if err != nil {
		p.warnings = append(p.warnings, "cleanup is incomplete: "+err.Error())
	}
}

// newStaged creates a staged folder in skills/.staging/.
func (p *pipeline) newStaged(slug string) (string, error) {
	dir := filepath.Join(p.root, ".staging")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(dir, slug+"-")
}

// discardStaged removes a staged folder that was not published. A failure is
// logged; §9.3 removes skills/.staging/ leftovers at the next start.
func (p *pipeline) discardStaged(dir string) {
	if !fileExists(dir) {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		slog.Warn("skills: staged folder not removed; it is removed at the next start", "dir", dir, "error", err)
	}
}

// materialize writes entries into dir: regular files from their stored bytes
// (hash-verified) with the recorded executable bit, symlinks as their target
// text. Special entries cannot be materialized.
func materialize(entries []treeEntry, dir string) error {
	for _, e := range entries {
		abs := filepath.Join(dir, filepath.FromSlash(e.Path))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		switch e.Type {
		case entryTypeSymlink:
			if err := os.Symlink(e.LinkTarget, abs); err != nil {
				return err
			}
		case entryTypeSpecial:
			return fmt.Errorf("unsupported entry %s (%s)", e.Path, e.ModeType)
		default:
			if err := writeEntryFile(e, abs); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeEntryFile(e treeEntry, abs string) error {
	mode := os.FileMode(0o644)
	if e.Exec {
		mode = 0o755
	}
	if e.content != nil {
		if err := os.WriteFile(abs, e.content, mode); err != nil {
			return err
		}
		return os.Chmod(abs, mode)
	}
	if e.blob == "" {
		return fmt.Errorf("no stored bytes for '%s'", e.Path)
	}
	src, err := os.Open(e.blob)
	if err != nil {
		return fmt.Errorf("read stored bytes for '%s': %w", e.Path, err)
	}
	defer src.Close()
	dst, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(dst, h), src); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
		return fmt.Errorf("stored bytes for '%s' do not match their recorded hash", e.Path)
	}
	return os.Chmod(abs, mode)
}

// copyExternalTree copies an install's staged package into the pipeline's
// own staging folder. Top-level Omnipus control metadata is never carried
// over; a non-regular, non-symlink entry is refused.
func copyExternalTree(src, dst string) (*refusal, error) {
	var ref *refusal
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, p)
		if err != nil || rel == "." {
			return err
		}
		slash := filepath.ToSlash(rel)
		if !strings.Contains(slash, "/") && isControlEntry(slash) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.IsDir():
			return os.MkdirAll(target, 0o755)
		case info.Mode().IsRegular():
			return copyRegularFile(p, target, info.Mode().Perm()&0o111 != 0)
		default:
			ref = saveCheck(fmt.Sprintf("unsupported entry %s (%s)", slash, info.Mode().Type()))
			return filepath.SkipAll
		}
	})
	return ref, err
}

func copyRegularFile(src, dst string, exec bool) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	mode := os.FileMode(0o644)
	if exec {
		mode = 0o755
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

// frontmatterName matches the frontmatter `name:` line.
var frontmatterName = regexp.MustCompile(`(?m)^name:[ \t]*[^\r\n]*`)

// renameInstruction applies the copy's declared transform: the instruction
// file's frontmatter `name` is rewritten to the destination slug; every other
// byte is unchanged.
func renameInstruction(entries []treeEntry, to string) ([]treeEntry, error) {
	m := entryMapOf(entries)
	ref := resolveInstructionInEntries(m)
	if ref.state != instructionRegular && ref.state != instructionLinked {
		return entries, nil
	}
	e := m[ref.rel]
	raw, err := entryBytes(e)
	if err != nil {
		return nil, err
	}
	rewritten := rewriteFrontmatterName(raw, to)
	e.content, e.blob = rewritten, ""
	e.SHA256, e.Size = sha256Hex(rewritten), int64(len(rewritten))
	m[ref.rel] = e
	return entryListOf(m), nil
}

func rewriteFrontmatterName(raw []byte, to string) []byte {
	s := string(raw)
	if !strings.HasPrefix(s, "---") {
		return raw
	}
	end := strings.Index(s[3:], "\n---")
	if end < 0 {
		return raw
	}
	front := s[:3+end]
	loc := frontmatterName.FindStringIndex(front)
	if loc == nil {
		return raw
	}
	return []byte(s[:loc[0]] + "name: " + to + s[loc[1]:])
}

var shippedRevisions sync.Map

// shippedRevision is §8.2's shipped revision: contentRevision over the
// embedded files with the executable bit off for every file and no stamp.
func shippedRevision(name string) (string, error) {
	if v, ok := shippedRevisions.Load(name); ok {
		if rev, isString := v.(string); isString {
			return rev, nil
		}
	}
	base := embeddedRoot + "/" + name
	var entries []treeEntry
	err := fs.WalkDir(embeddedSkills, base, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == base || d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, base+"/")
		if !strings.Contains(rel, "/") && isControlEntry(rel) {
			return nil
		}
		data, err := embeddedSkills.ReadFile(p)
		if err != nil {
			return err
		}
		entries = append(entries, treeEntry{Path: rel, Type: entryTypeFile, Size: int64(len(data)), SHA256: sha256Hex(data)})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("shipped revision of %s: %w", name, err)
	}
	rev := revisionOfEntries(entries)
	shippedRevisions.Store(name, rev)
	return rev, nil
}

// Audit (§5.12 skill.write). Field values never carry secret values.

func auditEntry(req ChangeRequest, decision string, details map[string]any) *audit.Entry {
	return &audit.Entry{
		Timestamp: time.Now().UTC(), Event: "skill.write", Decision: decision,
		AgentID: req.Actor.AgentID, SessionID: req.Actor.SessionID, Tool: req.Actor.Tool, Details: details,
	}
}

func auditRefusal(req ChangeRequest, slug, reason string) {
	emitAudit(auditEntry(req, "refused", map[string]any{
		"slug": slug, "shelf": "registry", "kind": string(req.Kind), "outcome": "refused",
		"reason": reason, "workspace": req.Actor.WorkspaceID,
	}))
}

func auditPartial(req ChangeRequest, slug string, kind any, reason string) {
	emitAudit(auditEntry(req, "partial", map[string]any{
		"slug": slug, "shelf": "registry", "kind": fmt.Sprint(kind), "outcome": "partial",
		"reason": reason, "workspace": req.Actor.WorkspaceID,
	}))
}

func auditCommitted(req ChangeRequest, st *state, m *versionManifest) {
	emitAudit(auditEntry(req, "committed", map[string]any{
		"slug": st.slug, "shelf": "registry", "kind": m.Kind, "paths": m.Paths, "version": m.ID,
		"revision_before": st.rev, "revision_after": m.Revision, "outcome": "committed",
		"workspace": req.Actor.WorkspaceID,
	}))
}

func auditCheckpoint(slug string, m *versionManifest) {
	emitAudit(&audit.Entry{
		Timestamp: time.Now().UTC(), Event: "skill.write", Decision: "external_change",
		Details: map[string]any{
			"slug": slug, "shelf": "registry", "kind": m.Kind, "paths": m.Paths, "version": m.ID,
			"revision_after": m.Revision, "outcome": "external_change",
		},
	})
}
