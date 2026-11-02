package skills

// The per-operation bodies of ApplyChange (§7.1 steps 1–7 for each kind).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
)

// runEdit is a single-file write into an existing skill (§5.5 kind=edit).
func (p *pipeline) runEdit() (PublishOutcome, error) {
	st, err := p.detect(p.req.Slug)
	if err != nil {
		return refusedOutcome(), err
	}
	if err = p.precondition(st); err != nil {
		return refusedOutcome(), err
	}
	rel := p.req.Path
	if r := validateRelPath(rel, true); r != nil {
		return refusedOutcome(), p.refuse(st, r)
	}
	live := st.liveMap()
	if other := caseFoldConflict(live, rel); other != "" {
		return refusedOutcome(), p.refuse(st, saveCheck(fmt.Sprintf("'%s' differs from '%s' only by letter case; a skill cannot hold both", rel, other)))
	}
	if r := checkLiveWritePath(st.dir, rel); r != nil {
		return refusedOutcome(), p.refuse(st, r)
	}
	exec := false
	if prev, ok := live[rel]; ok && prev.Type == entryTypeFile {
		exec = prev.Exec
	}
	attempt, err := newCaptureAttempt(st.hist)
	if err != nil {
		return refusedOutcome(), p.storageError(st.slug, err)
	}
	sum, blob, err := attempt.addContent(p.req.Content)
	if err != nil {
		return refusedOutcome(), p.storageErrorText(st.slug, err, abortSuffix(attempt.abort()))
	}
	next := st.liveMap()
	next[rel] = treeEntry{Path: rel, Type: entryTypeFile, Exec: exec, Size: int64(len(p.req.Content)), SHA256: sum, blob: blob}
	if err := p.saveCheckState(st, st.slug, next); err != nil {
		return refusedOutcome(), withSuffix(err, abortSuffix(attempt.abort()))
	}
	if err := writeLiveFile(filepath.Join(st.dir, filepath.FromSlash(rel)), p.req.Content, exec); err != nil {
		return refusedOutcome(), p.storageErrorText(st.slug, err, abortSuffix(attempt.abort()))
	}
	entries := entryListOf(next)
	return p.commitAndFinish(st, commitSpec{kind: manifestEdit, entries: entries, attempt: attempt, paths: diffPaths(st.entries, entries)})
}

// runCreate is the first write of <new>/SKILL.md (§5.5 kind=create).
func (p *pipeline) runCreate() (PublishOutcome, error) {
	st, err := p.observe(p.req.Slug)
	if err != nil {
		return refusedOutcome(), err
	}
	if err = p.precondition(st); err != nil {
		return refusedOutcome(), err
	}
	if p.req.Path != "" && p.req.Path != "SKILL.md" {
		return refusedOutcome(), p.refuse(st, saveCheck("create a skill by writing its SKILL.md first"))
	}
	attempt, err := newCaptureAttempt(st.hist)
	if err != nil {
		return refusedOutcome(), p.storageError(st.slug, err)
	}
	sum, blob, err := attempt.addContent(p.req.Content)
	if err != nil {
		return refusedOutcome(), p.storageErrorText(st.slug, err, abortSuffix(attempt.abort()))
	}
	next := map[string]treeEntry{"SKILL.md": {Path: "SKILL.md", Type: entryTypeFile, Size: int64(len(p.req.Content)), SHA256: sum, blob: blob}}
	if err := p.saveCheckState(st, st.slug, next); err != nil {
		return refusedOutcome(), withSuffix(err, abortSuffix(attempt.abort()))
	}
	if err := os.MkdirAll(p.root, 0o755); err != nil {
		return refusedOutcome(), p.storageErrorText(st.slug, err, abortSuffix(attempt.abort()))
	}
	if err := os.Mkdir(st.dir, 0o755); err != nil {
		return refusedOutcome(), p.storageErrorText(st.slug, err, abortSuffix(attempt.abort()))
	}
	if err := writeLiveFile(filepath.Join(st.dir, "SKILL.md"), p.req.Content, false); err != nil {
		suffix := abortSuffix(attempt.abort())
		if cleanupErr := removeEmptyCreateDir(st.dir); cleanupErr != nil {
			// §7.6 class 1c: the leftover is reported truthfully, nothing saved.
			text := fmt.Sprintf("the change to '%s' was not saved: %v%s; an empty folder '%s' remains; it is not a skill (no SKILL.md). To clean it up: list_directory skills/%s, then either write a valid SKILL.md (the folder counts as present and repairable, §7.3 — the retry is an existing-folder write, not a create) or edit_skill action=delete skill=%s and create afresh — or remove the folder outside Omnipus. (An immediate retry is refused SKILL_NOT_READ: the leftover folder counts as present (§7.3) and this session has no seen record for it. After the read, the same write of a valid SKILL.md commits — it is an existing-folder repair with expected revision R, never a SKILL_EXISTS collision.) Cleanup error: %v",
				st.slug, err, suffix, st.slug, st.slug, st.slug, cleanupErr)
			auditRefusal(p.req, st.slug, "storage_error")
			return refusedOutcome(), &ChangeError{Code: ErrStorage, Text: text, cause: err}
		}
		return refusedOutcome(), p.storageErrorText(st.slug, err, suffix)
	}
	entries := entryListOf(next)
	// §5.13a: a creation event stamps a fresh generation at publication.
	if err := writeGeneration(st.dir, newGeneration()); err != nil {
		return p.afterPublishFailure(st, err, revisionOfEntries(entries), attempt)
	}
	return p.commitAndFinish(st, commitSpec{kind: manifestCreate, entries: entries, attempt: attempt, paths: allPaths(entries)})
}

// removeEmptyCreateDir is step 5's create cleanup: os.Remove deletes only an
// empty directory, so content that appeared meanwhile is never removed.
func removeEmptyCreateDir(dir string) error {
	if f := FaultEmptyDirCleanup; f != nil {
		if err := f(dir); err != nil {
			return err
		}
	}
	return os.Remove(dir)
}

// writeLiveFile is step 5's single-file publication.
func writeLiveFile(abs string, content []byte, exec bool) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if f := FaultLiveWrite; f != nil {
		if err := f(abs); err != nil {
			return err
		}
	}
	perm := os.FileMode(0o644)
	if exec {
		perm = 0o755
	}
	return fileutil.WriteFileAtomic(abs, content, perm)
}

// runDeleteFile removes one file (§5.4 delete_file).
func (p *pipeline) runDeleteFile() (PublishOutcome, error) {
	st, err := p.detect(p.req.Slug)
	if err != nil {
		return refusedOutcome(), err
	}
	if err := p.precondition(st); err != nil {
		return refusedOutcome(), err
	}
	rel := p.req.Path
	if rel == "SKILL.md" {
		return refusedOutcome(), p.refuse(st, &refusal{code: ErrSkillMDRequired, reason: "SKILL.md cannot be deleted; delete the whole skill with action=delete"})
	}
	if r := validateRelPath(rel, true); r != nil {
		return refusedOutcome(), p.refuse(st, r)
	}
	next := st.liveMap()
	if _, ok := next[rel]; !ok {
		return refusedOutcome(), p.refuse(st, saveCheck(fmt.Sprintf("'%s' is not a file of the skill", rel)))
	}
	if r := checkParentsAreFolders(st.dir, rel); r != nil {
		return refusedOutcome(), p.refuse(st, r)
	}
	delete(next, rel)
	if reason := instructionValidity(st.slug, next); reason != "" {
		return refusedOutcome(), p.refuse(st, saveCheck(fmt.Sprintf("deleting '%s' would leave the skill without a valid instruction file (%s)", rel, reason)))
	}
	if err := os.Remove(filepath.Join(st.dir, filepath.FromSlash(rel))); err != nil {
		return refusedOutcome(), p.storageError(st.slug, err)
	}
	entries := entryListOf(next)
	return p.commitAndFinish(st, commitSpec{kind: manifestDeleteFile, entries: entries, paths: diffPaths(st.entries, entries)})
}

// instructionValidity is the delete_file resulting-instruction rule (§7.2):
// the instruction file resolved within the would-be entries must exist and
// pass the loader's own validation. "" when valid.
func instructionValidity(slug string, entries map[string]treeEntry) string {
	content, ref, err := instructionBytes(entries)
	if err != nil {
		return err.Error()
	}
	switch ref.state {
	case instructionMissing:
		return "no SKILL.md"
	case instructionUnresolved:
		return fmt.Sprintf("SKILL.md is a symlink to '%s', which is not a regular file of the skill", ref.target)
	}
	return validateInstruction(slug, content)
}

// checkParentsAreFolders refuses a removal whose parent components are not
// real folders inside the skill (a symlinked folder would point outside).
func checkParentsAreFolders(dir, rel string) *refusal {
	parts := strings.Split(rel, "/")
	cur := dir
	for _, comp := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, comp)
		fi, err := os.Lstat(cur)
		if err != nil {
			return saveCheck(fmt.Sprintf("'%s' cannot be inspected (%v)", rel, err))
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return saveCheck(fmt.Sprintf("'%s' is not inside a folder of the skill", rel))
		}
	}
	return nil
}

// runDelete moves the folder away and records delete_skill (§5.4 delete).
func (p *pipeline) runDelete() (PublishOutcome, error) {
	st, err := p.detect(p.req.Slug)
	if err != nil {
		return refusedOutcome(), err
	}
	if err = p.precondition(st); err != nil {
		return refusedOutcome(), err
	}
	swap, err := p.prepareSwap(st.slug)
	if err != nil {
		return refusedOutcome(), p.storageError(st.slug, err)
	}
	if err = forwardRename(st.dir, swap); err != nil {
		return refusedOutcome(), p.storageError(st.slug, err)
	}
	m, err := p.commit(st, commitSpec{kind: manifestDeleteSkill})
	if err != nil {
		if putErr := putBackRename(swap, st.dir); putErr != nil {
			// §7.6 class 1b-del-2.
			bumpGeneration()
			text := fmt.Sprintf("the deletion of '%s' did not complete and its folder is temporarily unavailable; it will be restored at the next start. Do not recreate '%s' until then.", st.slug, st.slug)
			auditPartial(p.req, st.slug, manifestDeleteSkill, fmt.Sprintf("%v; put-back failed: %v", err, putErr))
			return PublishOutcome{PersistenceStatus: PersistencePartial, ActivationStatus: ActivationNotAttempted, ChangedFields: []string{"installed"}, ErrorStage: PublicationErrorStageRestorePrevious, Message: text},
				&ChangeError{Code: ErrSkillPartiallySaved, Text: text, cause: err}
		}
		// §7.6 class 1b-del: the put-back succeeded — an ordinary failure.
		return refusedOutcome(), p.storageError(st.slug, err)
	}
	p.removeSwap(swap)
	p.removeGrant(st.slug)
	return p.finish(st, m, "")
}

// runRestore rebuilds a version's files (§5.4 restore).
func (p *pipeline) runRestore() (PublishOutcome, error) {
	var st *state
	var err error
	if p.req.Expect.Absent {
		st, err = p.observe(p.req.Slug)
	} else {
		st, err = p.detect(p.req.Slug)
	}
	if err != nil {
		return refusedOutcome(), err
	}
	if err = p.precondition(st); err != nil {
		return refusedOutcome(), err
	}
	v := p.req.Version
	var m *versionManifest
	if _, ok := versionSeq(v); ok && !strings.ContainsAny(v, `/\`) && v != "." && v != ".." {
		m, err = st.hist.load(v)
	}
	if m == nil {
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return refusedOutcome(), p.storageError(st.slug, err)
		}
		return refusedOutcome(), p.refuse(st, &refusal{code: ErrUnknownVersion, reason: fmt.Sprintf("unknown version %q for skill %q", v, st.slug)})
	}
	if m.Kind == manifestDeleteSkill {
		return refusedOutcome(), p.refuse(st, saveCheck(fmt.Sprintf("version %s records the deleted state; use edit_skill action=delete to remove the skill", m.ID)))
	}
	entries := st.hist.entriesFromManifest(m)
	secrets, r := p.loadSecretsOnce()
	if r != nil {
		return refusedOutcome(), p.refuse(st, r)
	}
	if r := restoreGate(st.slug, entryMapOf(entries), secrets); r != nil {
		if errors.Is(r.code, ErrSaveCheckFailed) {
			r = saveCheck(fmt.Sprintf("version %s cannot be restored (%s)", m.ID, r.reason))
		}
		return refusedOutcome(), p.refuse(st, r)
	}
	staged, err := p.newStaged(st.slug)
	if err != nil {
		return refusedOutcome(), p.storageError(st.slug, err)
	}
	defer p.discardStaged(staged)
	if err := materialize(entries, staged); err != nil {
		return refusedOutcome(), p.storageError(st.slug, err)
	}
	if err := p.stampStaged(st, staged); err != nil {
		return refusedOutcome(), p.storageError(st.slug, err)
	}
	return p.publishFolderAndCommit(st, staged, nil, commitSpec{kind: manifestRestore, entries: entries, restoredFrom: m.ID, paths: diffPaths(st.entries, entries)})
}

// runInstall publishes a staged package (§5.6 install / install_replace).
func (p *pipeline) runInstall() (PublishOutcome, error) {
	var st *state
	var err error
	if p.req.Kind == KindInstall {
		st, err = p.observe(p.req.Slug)
	} else {
		st, err = p.detect(p.req.Slug)
	}
	if err != nil {
		return refusedOutcome(), err
	}
	if err = p.precondition(st); err != nil {
		return refusedOutcome(), err
	}
	// §5.6 ordering: the local-changes confirmation is evaluated after step
	// 1's detection in the same lock hold, so an unrecorded outside edit is
	// recorded first and then counted (§9.2).
	if p.req.Kind == KindInstallReplace && !p.req.ReplaceLocalChanges && localChanges(st.head, st.present, st.rev) {
		return refusedOutcome(), p.refuse(st, &refusal{code: ErrLocalChangesConfirmationRequired,
			reason: fmt.Sprintf("'%s' has local changes that this install would replace. They stay in the version history. Confirm with replace_local_changes to continue.", st.slug)})
	}
	staged, err := p.newStaged(st.slug)
	if err != nil {
		return refusedOutcome(), p.storageError(st.slug, err)
	}
	defer p.discardStaged(staged)
	r, err := copyExternalTree(p.req.StagedDir, staged)
	if err != nil {
		return refusedOutcome(), p.storageError(st.slug, err)
	}
	if r != nil {
		return refusedOutcome(), p.refuse(st, r)
	}
	attempt, entries, err := p.captureStaged(st, staged)
	if err != nil {
		return refusedOutcome(), err
	}
	if err := p.stampStaged(st, staged); err != nil {
		return refusedOutcome(), p.storageErrorText(st.slug, err, abortSuffix(attempt.abort()))
	}
	spec := commitSpec{kind: manifestInstall, entries: entries, attempt: attempt, paths: allPaths(entries)}
	if p.req.Kind == KindInstallReplace {
		spec.kind = manifestInstallReplace
		spec.paths = diffPaths(st.entries, entries)
	}
	return p.publishFolderAndCommit(st, staged, attempt, spec)
}

// runCopy is edit_skill copy: capture the source once, stage the destination
// from that captured version with the frontmatter name rewritten (§7.1 copy
// clause), publish it as a new skill.
func (p *pipeline) runCopy() (PublishOutcome, error) {
	dst, err := p.observe(p.req.To)
	if err != nil {
		return refusedOutcome(), err
	}
	if err = p.precondition(dst); err != nil {
		return refusedOutcome(), err
	}
	srcSlug, srcPresent, err := resolveLiveFolder(p.root, p.req.Slug)
	if err != nil {
		return refusedOutcome(), p.storageError(dst.slug, err)
	}
	if !srcPresent {
		return refusedOutcome(), p.refuse(dst, &refusal{code: ErrNotFound, reason: fmt.Sprintf("skill '%s' is not installed", p.req.Slug)})
	}
	var srcEntries []treeEntry
	var restoredFrom, scratch string
	if IsBuiltin(srcSlug) {
		srcEntries, scratch, err = p.captureBuiltinSource(dst, srcSlug)
		if err != nil {
			return refusedOutcome(), err
		}
		restoredFrom = fmt.Sprintf("builtin:%s@%s", srcSlug, config.GetVersion())
	} else {
		src, detectErr := p.detect(srcSlug)
		if detectErr != nil {
			return refusedOutcome(), detectErr
		}
		srcEntries = src.entries
		restoredFrom = src.slug + "@" + src.head.ID
	}
	entries, err := renameInstruction(srcEntries, p.req.To)
	if err != nil {
		p.disposeScratch(scratch)
		return refusedOutcome(), p.storageError(dst.slug, err)
	}
	staged, err := p.newStaged(dst.slug)
	if err != nil {
		p.disposeScratch(scratch)
		return refusedOutcome(), p.storageError(dst.slug, err)
	}
	defer p.discardStaged(staged)
	matErr := materialize(entries, staged)
	// §9.5 rule 6: a built-in capture's scratch is disposed in this lock hold
	// once the staged destination is built.
	p.disposeScratch(scratch)
	if matErr != nil {
		return refusedOutcome(), p.storageError(dst.slug, matErr)
	}
	attempt, staged2, err := p.captureStaged(dst, staged)
	if err != nil {
		return refusedOutcome(), err
	}
	if err := p.stampStaged(dst, staged); err != nil {
		return refusedOutcome(), p.storageErrorText(dst.slug, err, abortSuffix(attempt.abort()))
	}
	out, cerr := p.publishFolderAndCommit(dst, staged, attempt, commitSpec{kind: manifestCreate, entries: staged2, attempt: attempt, restoredFrom: restoredFrom, paths: allPaths(staged2)})
	if cerr == nil {
		out.Message = fmt.Sprintf("Created '%s' as your own copy of '%s'. No agent is granted it yet; grant it to the agents that should use it.\n%s", dst.slug, srcSlug, out.Message)
	}
	return out, cerr
}

// captureBuiltinSource captures a built-in copy source into staging-area
// scratch (never a history directory, §5.11) and verifies it equals the
// shipped revision (§7.2).
func (p *pipeline) captureBuiltinSource(dst *state, srcSlug string) ([]treeEntry, string, error) {
	stagingRoot := filepath.Join(p.root, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return nil, "", p.storageError(dst.slug, err)
	}
	scratch, err := os.MkdirTemp(stagingRoot, ".incoming-")
	if err != nil {
		return nil, "", p.storageError(dst.slug, err)
	}
	res, err := captureTree(filepath.Join(p.root, srcSlug), captureOptions{op: "capture", blobDir: scratch})
	if err != nil {
		return nil, "", p.captureError(dst.slug, err, scratchSuffix(removeScratchDir(scratch)))
	}
	shipped, err := shippedRevision(SkillKey(srcSlug))
	if err != nil {
		return nil, "", p.storageErrorText(dst.slug, err, scratchSuffix(removeScratchDir(scratch)))
	}
	if res.revision != shipped {
		return nil, "", p.refuseText(dst, saveCheck(fmt.Sprintf("built-in '%s' does not match its shipped version and will be replaced at the next start; copy it after the next start", srcSlug)), scratchSuffix(removeScratchDir(scratch)))
	}
	return res.entries, scratch, nil
}

func scratchSuffix(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("; capture scratch cleanup failed (%v)", err)
}

// removeScratchDir disposes of a capture scratch directory through the §9.5
// disposal seam.
func removeScratchDir(dir string) error {
	if f := FaultStagingRemove; f != nil {
		if err := f(dir); err != nil {
			return err
		}
	}
	return os.RemoveAll(dir)
}

func (p *pipeline) disposeScratch(dir string) {
	if dir == "" {
		return
	}
	if err := removeScratchDir(dir); err != nil {
		p.warnings = append(p.warnings, "capture scratch cleanup failed: "+err.Error())
	}
}

// captureStaged walks a staged folder once, writing its blobs into a fresh
// attempt of the target's history, and runs the authoring save check.
func (p *pipeline) captureStaged(st *state, staged string) (*captureAttempt, []treeEntry, error) {
	attempt, err := newCaptureAttempt(st.hist)
	if err != nil {
		return nil, nil, p.storageError(st.slug, err)
	}
	res, err := captureTree(staged, captureOptions{op: "stage", blobDir: attempt.dir})
	if err != nil {
		return nil, nil, p.captureError(st.slug, err, abortSuffix(attempt.abort()))
	}
	if res.special != nil {
		return nil, nil, p.refuseText(st, saveCheck(unsupportedEntryReason(*res.special)), abortSuffix(attempt.abort()))
	}
	for _, e := range res.entries {
		if r := validateRelPath(e.Path, true); r != nil {
			return nil, nil, p.refuseText(st, r, abortSuffix(attempt.abort()))
		}
	}
	if err := p.saveCheckState(st, st.slug, res.entryMap()); err != nil {
		return nil, nil, withSuffix(err, abortSuffix(attempt.abort()))
	}
	return attempt, res.entries, nil
}

// stampStaged writes the generation the published folder carries: the live
// stamp preserved on a live replace, a fresh one for a creation event
// (§5.13a).
func (p *pipeline) stampStaged(st *state, staged string) error {
	if st.present {
		gen, err := ReadGeneration(st.dir)
		if err != nil {
			return err
		}
		if gen == "" {
			return nil
		}
		return writeGeneration(staged, gen)
	}
	return writeGeneration(staged, newGeneration())
}

// withSuffix appends a cleanup suffix to a refusal's text.
func withSuffix(err error, suffix string) error {
	if suffix == "" {
		return err
	}
	var ce *ChangeError
	if errors.As(err, &ce) {
		ce.Text += suffix
		return err
	}
	return fmt.Errorf("%w%s", err, suffix)
}
