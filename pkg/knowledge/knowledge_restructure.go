// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

// knowledge_restructure.go — ADR-068 D15.3 item 5 / spec §4.1.5:
// knowledge_restructure, the only tool permitted to write bytes into a file
// the caller did not name (C-A), and the one place `trash` and `restore` live
// even though FR-070d classifies trash as C-B (see rename.go and
// knowledge_restructure_trash.go's own headers for why: this tier is "reaches
// beyond one named note", not "C-A only").
//
// THIS FILE IS A TOOL ADAPTER, NOT A SECOND WRITE MECHANISM. Every op below
// resolves the caller's arguments into a request and DELEGATES:
//
//	rename, move   -> rename.go's Renamer (Plan/Rename) — planning, the
//	                  journalled multi-file rewrite, crash recovery.
//	trash, restore -> knowledge_restructure_trash.go's Trasher.
//
// No link is rewritten, no byte is moved, and no file under .omnipus-vault/
// is written from THIS file — every one of those actions happens inside the
// two engines above, which is what keeps FR-045's "the rewriting path" as one
// implementation reachable from one tool name, rather than a second one
// growing here under time pressure. What this file adds on top of the
// engines, matching vault_edit's own four additions (see knowledge_edit.go's
// header) minus the schema-conformance layer restructure has no need of:
//
//  1. WORKSPACE SCOPE + THE LIFECYCLE GATE + AUDIT OF PRE-ENGINE REFUSALS —
//     via AuthoringDeps.begin, the same preamble every mutation in this
//     package shares.
//  2. THE BLAST-RADIUS REDIRECTS (FR-070b) — an op that belongs to
//     knowledge_edit (a one-file write) or knowledge_configure (a schema/view
//     change) is refused BY NAME here, never attempted under a different
//     argument spelling.
//  3. THE expect_version REFUSAL (AC-X3) — this tool declares no version
//     token parameter at all (Parameters() below has none), and a caller that
//     sends one anyway is told exactly why, in the spec's own wording.
//  4. COMPACT-TEXT RENDERING (FR-072) — every response, success or failure,
//     is prose, never a JSON document.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Operation names knowledge_restructure's `op` argument accepts.
const (
	restructureOpRename  = "rename"
	restructureOpMove    = "move"
	restructureOpTrash   = "trash"
	restructureOpRestore = "restore"
)

// restructureOps lists the accepted ops, in the order they are documented.
var restructureOps = []string{restructureOpRename, restructureOpMove, restructureOpTrash, restructureOpRestore}

// restructureEditRedirect names the ops that write exactly one named file —
// knowledge_edit's whole territory (FR-070b) — refused here by name rather
// than attempted, mirroring knowledge_edit's own vaultEditRedirect in the
// opposite direction.
var restructureEditRedirect = map[string]string{
	"create":         "create",
	"set_property":   "set_property",
	"append_section": "append_section",
	"link":           "link",
	"replace_body":   "replace_body",
}

// restructureConfigureOps names the ops that change what existing notes MEAN
// without writing them (C-B) — knowledge_configure's control plane.
var restructureConfigureOps = map[string]struct{}{
	"create_record_type": {},
	"edit_record_type":   {},
	"delete_record_type": {},
	"write_view":         {},
	"delete_view":        {},
}

// restructureArgNames is every argument knowledge_restructure's Parameters()
// declares. Deliberately excludes expect_version — AC-X3 requires it be
// absent from the schema, not merely unused — so a caller that sends it is
// caught by the dedicated check in Execute, ahead of the generic
// unknownArgs sweep, and told the SPEC's reason rather than a generic
// "unknown argument".
var restructureArgNames = []string{
	"op", "collection", "path", "new_name", "new_folder", "allow_ambiguity", "trashed_at",
}

// Audit operation names, local to this file (matching authoring_tools.go's
// own knowledgeRenameOp precedent rather than adding to author.go's shared
// AuthorOperation block).
const (
	restructureRenameOp  AuthorOperation = "knowledge.note.rename"
	restructureTrashOp   AuthorOperation = "knowledge.note.trash"
	restructureRestoreOp AuthorOperation = "knowledge.note.restore"
)

// RestructureTool is knowledge_restructure.
type RestructureTool struct {
	tools.BaseTool
	deps AuthoringDeps
}

// NewRestructureTool builds knowledge_restructure over deps — the same
// AuthoringDeps every mutation in this package shares.
func NewRestructureTool(deps AuthoringDeps) *RestructureTool { return &RestructureTool{deps: deps} }

// Name is the registered tool name.
func (t *RestructureTool) Name() string { return "knowledge_restructure" }

// Description names the WIDEST operation this tool grants (FR-070c, FR-079,
// and the trash design note's finding A-10 remedy, which names `trash`
// specifically as vault_restructure's widest capability): trash can make a
// note and everything pointing at it unreachable in one call, which is a
// broader capability than a rename or move (which at least repair what they
// touch).
func (t *RestructureTool) Description() string {
	return "Change a knowledge base in ways that reach files you did not name. Rename or move a " +
		"note: every inbound link is rewritten, in bodies and frontmatter. Trash a note: a " +
		"reversible soft delete — it moves out of the way but every link that pointed at it is " +
		"left dangling, counted and named in the response, never silently repaired. Restore a " +
		"trashed note to its original path. Never edits a note's own content (use knowledge_edit) " +
		"and never authors or changes a record type or saved view (use knowledge_configure). " +
		"Takes no version token: a single-file token cannot honestly guard a change whose blast " +
		"radius is many notes."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *RestructureTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *RestructureTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in. No expect_version field —
// see this file's header and AC-X3.
func (t *RestructureTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"op": map[string]any{
				"type":        "string",
				"enum":        restructureOps,
				"description": "Which change to make.",
			},
			"collection": collectionParam(),
			"path": pathParam(
				"rename/move/trash: the note to change. restore: the note's ORIGINAL path, from " +
					"before it was trashed — never a path inside .omnipus-vault/trash/"),
			"new_name": map[string]any{
				"type": "string",
				"description": "rename: the note's new name, without a folder (required). move: " +
					"optionally rename it as it moves. The '.md' extension is added when left off.",
			},
			"new_folder": map[string]any{
				"type": "string",
				"description": "move: the folder to move the note into, relative to the " +
					"collection root (required). Use '' or '.' for the collection root itself.",
			},
			"allow_ambiguity": map[string]any{
				"type": "boolean",
				"description": "rename/move: proceed even if the destination name would be " +
					"shared with another note elsewhere in the collection. Default false, which " +
					"refuses instead.",
			},
			"trashed_at": map[string]any{
				"type": "string",
				"description": "restore: which trashed copy to restore, when the note was " +
					"trashed more than once — the timestamp a trash response reported " +
					"('-> trashed at ...'). Nothing lists them ahead of time; a restore " +
					"naming a timestamp that has no copy is refused with every available " +
					"timestamp for that path listed, which is the way to discover them. " +
					"Leave unset for the most recently trashed copy.",
			},
		},
		"required": []string{"op", "path"},
	}
}

// Execute dispatches by op.
func (t *RestructureTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	op := strings.TrimSpace(stringArg(args["op"]))
	authorOp := restructureAuditOpFor(op)
	target, refusal := t.deps.begin(ctx, authorOp, args)
	if refusal != nil {
		return refusal
	}
	// AC-X3, checked ahead of the generic unknown-argument sweep so the
	// caller gets the SPEC's own reason rather than a generic refusal.
	if _, sentVersion := args["expect_version"]; sentVersion {
		return t.deps.refuse(authorOp, target, nil,
			"knowledge_restructure takes no expect_version: a single-file token cannot guard a "+
				"rename that rewrites inbound links in notes you did not name. Re-read with "+
				"knowledge_read and re-send")
	}
	if unknown := unknownArgs(args, restructureArgNames); len(unknown) > 0 {
		return t.deps.refuse(authorOp, target, nil, fmt.Sprintf(
			"unknown argument(s) %s; accepted: %s",
			strings.Join(unknown, ", "), strings.Join(restructureArgNames, ", ")))
	}
	switch op {
	case restructureOpRename:
		return t.execRenameMove(target, args, false)
	case restructureOpMove:
		return t.execRenameMove(target, args, true)
	case restructureOpTrash:
		return t.execTrash(target, args)
	case restructureOpRestore:
		return t.execRestore(target, args)
	default:
		return t.refuseOp(target, op)
	}
}

// restructureAuditOpFor picks the audited event name from the requested op,
// falling back to the rename event for an unrecognised op (the refusal path
// this feeds still names the real reason; the audited operation name only
// needs to be A valid one, and rename covers the case where op is empty or
// unknown).
func restructureAuditOpFor(op string) AuthorOperation {
	switch op {
	case restructureOpTrash:
		return restructureTrashOp
	case restructureOpRestore:
		return restructureRestoreOp
	default:
		return restructureRenameOp
	}
}

// refuseOp handles an empty, redirected, or genuinely unsupported op — every
// branch names the valid alternative and, for a redirect, the exact tool to
// call instead (spec §4.1.5's normative refusal wording).
func (t *RestructureTool) refuseOp(target mutationTarget, op string) *tools.ToolResult {
	authorOp := restructureAuditOpFor(op)
	if op == "" {
		return t.deps.refuse(authorOp, target, nil,
			"'op' is required; one of "+strings.Join(restructureOps, ", "))
	}
	if _, ok := restructureEditRedirect[op]; ok {
		return t.deps.refuse(authorOp, target, nil,
			fmt.Sprintf("%s writes one note; use knowledge_edit", op))
	}
	if _, ok := restructureConfigureOps[op]; ok {
		return t.deps.refuse(authorOp, target, nil,
			fmt.Sprintf("%s changes what existing notes mean; use knowledge_configure", op))
	}
	return t.deps.refuse(authorOp, target, nil,
		fmt.Sprintf("unsupported op %q; supported ops are %s", op, strings.Join(restructureOps, ", ")))
}

// ---------------------------------------------------------------------------
// rename / move — delegates to rename.go's Renamer
// ---------------------------------------------------------------------------

func (t *RestructureTool) execRenameMove(target mutationTarget, args map[string]any, isMove bool) *tools.ToolResult {
	op := restructureRenameOp
	from, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(op, target, nil, err.Error())
	}
	from = ensureMarkdown(from)

	newName := strings.TrimSpace(stringArg(args["new_name"]))
	var to string
	if isMove {
		if newName == "" {
			newName = path.Base(from)
		}
		if strings.ContainsAny(newName, "/\\") {
			return t.deps.refuse(op, target, []string{from},
				fmt.Sprintf("'new_name' must be a name, not a path: %q", newName))
		}
		to = path.Join(normalizeMoveFolder(stringArg(args["new_folder"])), newName)
	} else {
		if newName == "" {
			return t.deps.refuse(op, target, []string{from}, "'new_name' is required")
		}
		if strings.ContainsAny(newName, "/\\") {
			return t.deps.refuse(op, target, []string{from},
				fmt.Sprintf("'new_name' must be a name, not a path — give 'new_folder' to move it: %q", newName))
		}
		to = path.Join(path.Dir(from), newName)
	}
	to, err = cleanNoteArg(to)
	if err != nil {
		return t.deps.refuse(op, target, []string{from}, err.Error())
	}
	to = ensureMarkdown(to)

	root, err := NewCollectionRoot(OSLinkFS(), target.col.Root)
	if err != nil {
		return t.deps.refuse(op, target, []string{from}, err.Error())
	}
	renamer := &Renamer{
		FS: OSLinkFS(), Root: root, AgentID: target.agentID,
		Audit: restructureRenameAuditFunc(t.deps, target), Lock: target.lock,
	}
	res, err := renamer.Rename(RenameRequest{
		From: from, To: to, AllowAmbiguity: boolArg(args["allow_ambiguity"]),
	})
	if err != nil {
		// rename.go has already audited this outcome (including "incomplete",
		// where the journal is retained and completable) — see renameEngine's
		// own precedent in authoring_tools.go for the same rule.
		msg := fmt.Sprintf("%s: %v", op, err)
		if res != nil && res.JournalID != "" {
			msg += fmt.Sprintf(" (journal %s is retained; the change can be completed or undone)", res.JournalID)
		}
		return tools.ErrorResult(msg)
	}
	return tools.NewToolResult(RenderRestructureRename(RestructureRenameData{
		Op: restructureOpFor(isMove), From: res.From, To: res.To, NoOp: res.NoOp,
		FilesRewritten: res.FilesRewritten, LinksRewritten: res.LinksRewritten,
		Ambiguity: res.Ambiguity, Skipped: res.Skipped, Incomplete: res.Incomplete,
	}))
}

func restructureOpFor(isMove bool) string {
	if isMove {
		return restructureOpMove
	}
	return restructureOpRename
}

// restructureRenameAuditFunc adapts rename.go's event shape onto the tool
// layer's audit sink. Deliberately NOT a call to authoring_tools.go's own
// renameAuditFunc: that method exists only to serve knowledge_rename/
// knowledge_move, which are being retired on this branch, and this file must
// not depend on code another agent may delete as dead weight once those two
// tools are gone. The logic is the same four lines either way.
func restructureRenameAuditFunc(d AuthoringDeps, t mutationTarget) RenameAuditFunc {
	if d.Audit == nil {
		return nil
	}
	return func(ev RenameAuditEvent) {
		outcome := AuthorOutcomeApplied
		if ev.Outcome == RenameOutcomeRefused || ev.Outcome == RenameOutcomeIncomplete {
			outcome = AuthorOutcomeRefused
		}
		at := ev.At
		if at.IsZero() {
			at = d.now()
		}
		reason := ev.Reason
		if ev.Outcome == RenameOutcomeIncomplete && reason != "" {
			reason = "incomplete, journal retained: " + reason
		}
		d.record(AuthorAuditRecord{
			Operation: restructureRenameOp,
			Outcome:   outcome,
			AgentID:   t.agentID, WorkspaceID: t.workspaceID,
			Collection: t.col.Name, Root: t.col.Root,
			Paths:  ev.Paths,
			Reason: reason,
			At:     at,
		})
	}
}

// ---------------------------------------------------------------------------
// trash / restore — delegates to knowledge_restructure_trash.go's Trasher
// ---------------------------------------------------------------------------

func (t *RestructureTool) newTrasher(target mutationTarget) (*Trasher, error) {
	root, err := NewCollectionRoot(OSLinkFS(), target.col.Root)
	if err != nil {
		return nil, err
	}
	return &Trasher{
		FS: OSLinkFS(), Root: root, AgentID: target.agentID,
		Now: t.deps.Now, Lock: target.lock,
		Audit: restructureTrashAuditFunc(t.deps, target),
	}, nil
}

// restructureTrashAuditFunc adapts the Trasher's event shape onto the tool
// layer's audit sink, the same pattern restructureRenameAuditFunc uses for
// Renamer.
func restructureTrashAuditFunc(d AuthoringDeps, t mutationTarget) TrashAuditFunc {
	if d.Audit == nil {
		return nil
	}
	return func(ev TrashAuditEvent) {
		outcome := AuthorOutcomeApplied
		if ev.Outcome != "applied" {
			outcome = AuthorOutcomeRefused
		}
		op := restructureTrashOp
		if ev.Op == trashOpRestore {
			op = restructureRestoreOp
		}
		d.record(AuthorAuditRecord{
			Operation: op, Outcome: outcome,
			AgentID: t.agentID, WorkspaceID: t.workspaceID,
			Collection: t.col.Name, Root: t.col.Root,
			Paths: ev.Paths, Reason: ev.Reason, At: ev.At,
		})
	}
}

func (t *RestructureTool) execTrash(target mutationTarget, args map[string]any) *tools.ToolResult {
	tr, err := t.newTrasher(target)
	if err != nil {
		return t.deps.refuse(restructureTrashOp, target, nil, err.Error())
	}
	res, err := tr.Trash(TrashRequest{Path: stringArg(args["path"])})
	if err != nil {
		// The Trasher has already audited this outcome — see execRenameMove's
		// identical rule for Renamer.
		return restructureFailure(restructureTrashOp, err)
	}
	// A note LEAVING the index (epoch.go's second bump site) — never on
	// execRestore, which re-enters a note rather than removing one.
	bumpIndexEpochOrWarn("trash", t.deps.Home, target.col.Root)
	return tools.NewToolResult(RenderRestructureTrash(*res))
}

func (t *RestructureTool) execRestore(target mutationTarget, args map[string]any) *tools.ToolResult {
	tr, err := t.newTrasher(target)
	if err != nil {
		return t.deps.refuse(restructureRestoreOp, target, nil, err.Error())
	}
	res, err := tr.Restore(RestoreRequest{
		Path:      stringArg(args["path"]),
		TrashedAt: strings.TrimSpace(stringArg(args["trashed_at"])),
	})
	if err != nil {
		return restructureFailure(restructureRestoreOp, err)
	}
	return tools.NewToolResult(RenderRestructureRestore(*res))
}

// restructureFailure renders an error from the Trasher as compact text
// (FR-072).
func restructureFailure(op AuthorOperation, err error) *tools.ToolResult {
	var lockErr *LockTimeoutError
	if errors.As(err, &lockErr) {
		return tools.ErrorResult(fmt.Sprintf("%s: %v", op, lockErr))
	}
	return tools.ErrorResult(fmt.Sprintf("%s: %v", op, err))
}

// ---------------------------------------------------------------------------
// Compact-text rendering (FR-072) — no JSON document, ever
// ---------------------------------------------------------------------------

// RestructureRenameData is what RenderRestructureRename needs to describe one
// rename/move outcome.
type RestructureRenameData struct {
	Op                             string
	From, To                       string
	NoOp                           bool
	FilesRewritten, LinksRewritten int
	Ambiguity                      *AmbiguityReport
	Skipped                        []SkippedEntry
	Incomplete                     bool
}

// RenderRestructureRename renders one rename/move response, matching
// §4.1.5's normative cascade line: "CASCADE: 7 notes rewritten (inbound
// wikilinks), 1 note moved".
func RenderRestructureRename(d RestructureRenameData) string {
	var b strings.Builder
	if d.NoOp {
		fmt.Fprintf(&b, "%s — no change: from and to are the same path\n", d.From)
		return b.String()
	}
	fmt.Fprintf(&b, "%s -> %s\n", d.From, d.To)
	fmt.Fprintf(&b, "CASCADE: %d notes rewritten (inbound wikilinks), 1 note %s (%d links rewritten)\n",
		d.FilesRewritten, restructureMoveVerb(d.Op), d.LinksRewritten)
	if d.Ambiguity != nil {
		fmt.Fprintf(&b, "AMBIGUITY: %q is now shared with %d note(s): %s\n",
			d.Ambiguity.Basename, len(d.Ambiguity.Candidates), strings.Join(d.Ambiguity.Candidates, ", "))
	}
	if d.Incomplete {
		b.WriteString("INCOMPLETE: this collection has unreadable notes this change could not see:\n")
		for _, s := range d.Skipped {
			fmt.Fprintf(&b, "  %s (%s)\n", s.RelPath, s.Reason)
		}
	}
	return b.String()
}

func restructureMoveVerb(op string) string {
	if op == restructureOpMove {
		return "moved"
	}
	return "renamed"
}

// RenderRestructureTrash renders one trash response.
func RenderRestructureTrash(r TrashResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s -> trashed at %s (%s)\n", r.OriginalPath, r.TrashID, r.TrashPath)
	if r.RecordType != "" {
		fmt.Fprintf(&b, "TYPE: %s", r.RecordType)
		if r.RecordID != "" {
			fmt.Fprintf(&b, " (id %s)", r.RecordID)
		}
		b.WriteString("\n")
	}
	if r.DanglingLinkCount == 0 {
		b.WriteString("CASCADE: 0 inbound links — nothing now dangles\n")
	} else {
		fmt.Fprintf(&b, "CASCADE: %d inbound link(s) now unrepairable across %d note(s)",
			r.DanglingLinkCount, len(r.DanglingNotes))
		if r.DanglingNotesTruncated {
			b.WriteString(fmt.Sprintf(" (showing first %d)", len(r.DanglingNotes)))
		}
		b.WriteString(":\n")
		for _, n := range r.DanglingNotes {
			fmt.Fprintf(&b, "  %s\n", n)
		}
	}
	if len(r.PriorTrashings) > 0 {
		fmt.Fprintf(&b, "%s was already trashed at %s; this copy is at %s\n",
			r.OriginalPath, strings.Join(r.PriorTrashings, ", "), r.TrashID)
	}
	return b.String()
}

// RenderRestructureRestore renders one restore response.
func RenderRestructureRestore(r RestoreResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s <- restored from trash (%s)\n", r.OriginalPath, r.RestoredFrom)
	if r.RecordType != "" {
		fmt.Fprintf(&b, "TYPE: %s", r.RecordType)
		if r.RecordID != "" {
			fmt.Fprintf(&b, " (id %s)", r.RecordID)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "CASCADE: %d inbound link(s) resolve again\n", r.ResolvedLinksCount)
	if len(r.OtherAvailable) > 0 {
		fmt.Fprintf(&b, "OTHER TRASHED COPIES: %s\n", strings.Join(r.OtherAvailable, ", "))
	}
	return b.String()
}
