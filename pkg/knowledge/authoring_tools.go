// Omnipus — ADR-067 D7: the agent-facing knowledge AUTHORING tools.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---------------------------------------------------------------------------
// WHAT IS HERE, AND WHAT IS DELIBERATELY ELSEWHERE
//
// tools.go implements the RETRIEVAL half (knowledge_search, knowledge_graph).
// This file implements the seven names the policy seed enumerates alongside
// them: knowledge_create, knowledge_link, knowledge_set_property,
// knowledge_append_section, knowledge_tasks, knowledge_move and
// knowledge_rename. Everything follows tools.go's shape, so registration is
// uniform.
//
// THIS FILE OWNS NO WRITE MECHANICS. It composes the ones that already exist:
//
//	author.go    CreateNote / EditNote / SetProperty / AppendSectionAt
//	template.go  templates, and the fixed placeholder set
//	rename.go    Renamer.Rename — planning, journalling, link rewriting
//	journal.go   the crash-recovery record behind that rename
//	version.go   version tokens and the typed ConflictError
//
// What a tool adds on top is exactly four things, and each is a requirement
// rather than plumbing:
//
//  1. WORKSPACE SCOPE (US-9, P0). The collection is resolved from the CALLING
//     AGENT'S workspace, taken off the tool context the agent loop installs —
//     never from an argument, which the model controls.
//  2. THE LIFECYCLE GATE (US-16). scope.go deliberately treats a broken mount
//     as contributing nothing, which is right for a search and wrong for a
//     write. lifecycle.go re-asks at write time, so a revoked or broken mount
//     refuses instead of writing into a folder whose grant has gone.
//  3. AUDIT COVERAGE OF THE REFUSALS THE LOWER LAYERS NEVER SEE (FR-090).
//     author.go and rename.go audit everything that reaches them. A call
//     refused HERE — an out-of-scope collection, a revoked mount, a malformed
//     argument — never reaches them, and would otherwise be the one class of
//     refusal missing from the record. Every such refusal is written to the
//     same sink.
//  4. ARGUMENT SHAPE. Composing a destination from new_folder + new_name,
//     defaulting the extension, and turning a model's arguments into the
//     primitives' requests.
//
// TWO OF THE SEVEN NAMES DO NOT MEAN WHAT THE SEED IMPLIES. Both are recorded
// here because a reader arriving from the seed will otherwise assume the seed
// was the design:
//
//  1. knowledge_tasks IS A READ, NOT A WRITE. The spec defines it nowhere —
//     round-1 review finding M-14 records that knowledge_link,
//     knowledge_set_property, knowledge_append_section and knowledge_tasks
//     "appear nowhere in the document — no user story, no scenario, no
//     requirement, no test", and rounds 2, 3 and 4 never answered it. The ADR
//     lists the name under a heading called "Authoring", which is a bucket,
//     not a definition. The only actual semantics available are the
//     operator's `ev` CLI, where `tasks` sits with `read`, `links`,
//     `backlinks` and `unresolved` — the read commands — and apart from
//     `create`, `set`, `append`, `move` and `rename`. So this tool LISTS
//     checkbox tasks and changes nothing. It is rate-limited as retrieval and
//     writes no mutation audit record, because it performs no mutation.
//     Consequence of the seeding, which this file cannot fix: Mia (the
//     default agent) and Ray hold `ask` on knowledge_tasks while holding
//     `allow` on knowledge_search — an approval prompt in front of a read
//     whose every byte is already reachable, unprompted, through a different
//     tool. The fix is a data change in pkg/coreagent/core.go and
//     pkg/config/defaults.go.
//
//  2. knowledge_move AND knowledge_rename ARE ONE OPERATION. The ADR writes
//     them as a single slashed item ("knowledge_move / knowledge_rename") and
//     the spec states one requirement for both: FR-103, "rewrite inbound
//     links in both note bodies and frontmatter on rename OR MOVE". They are
//     the same act — a note's collection-relative path changes and every
//     inbound link must follow. There is one engine (renameEngine) and two
//     front doors, differing only in which argument composes the
//     destination. Two engines would be two chances to get FR-104's journal
//     wrong.
//
// WHY knowledge_rename AND knowledge_move CARRY NO expect_version, WHEN EVERY
// OTHER MUTATING TOOL NOW REQUIRES ONE. FR-106 says a version token is
// required for every write, and a rename IS a write — of N files, not one.
// There is no single "version of a rename" a caller could have read: the
// operation's compare-and-swap is the journal's own per-file BeforeHash, which
// journal.go checks INSIDE that file's tier-1 lock immediately before writing
// it. That is a stricter contract than a caller-supplied token, not a weaker
// one — it covers every inbound note, including the ones the caller never
// read and could not have tokenised — and a file that fails it blocks the step
// and retains the journal rather than being overwritten. A token parameter
// here would be a control that looks like the others and checks less.
//
// WHY A WRITE REFUSES WHERE A SEARCH RETURNS EMPTY. FR-053 makes an
// out-of-scope RETRIEVAL an empty result set rather than an error, so the
// response leaks nothing about other workspaces. A write cannot copy that:
// "wrote nothing, successfully" is the silent loss this stage exists to
// prevent. The write refuses — and the refusal text is identical whether the
// collection belongs to another workspace or does not exist at all, so it
// discloses exactly as little as the empty result set does.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Construction
// ---------------------------------------------------------------------------

// AuthoringDeps is what the authoring tools need from their host.
type AuthoringDeps struct {
	// Home is $OMNIPUS_HOME. An empty Home makes every tool resolve an empty
	// scope, so a misconstructed tool addresses nothing rather than the
	// process working directory.
	Home string

	// Audit receives one record per mutation attempt, applied or refused
	// (FR-090, US-15). It is the SAME sink author.go and rename.go write to —
	// the tool layer forwards its own pre-flight refusals into it, so the
	// record has no hole where the scope and lifecycle refusals should be.
	//
	// Nil disables auditing. That is a wiring defect for anything an agent
	// can reach, not a configuration: the tools still work and still refuse,
	// but the record US-15 requires is absent. Hosts MUST supply one.
	Audit AuthorAudit

	// RateLimiter bounds knowledge_tasks, which is a read (see the header).
	// Leave nil and the constructor installs the default; nil never means
	// "no limit".
	RateLimiter *RetrievalRateLimiter

	// Now is the clock. Nil means time.Now. It exists so a test can assert on
	// template date substitution and audit timestamps without waiting for a
	// real second to pass.
	Now func() time.Time

	// NameShape is the create-time name-shape rule handed to CreateNote. Nil
	// means author.go's default, which fails closed.
	NameShape NameShapeCheck
}

func (d AuthoringDeps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// AuthoringTools builds the seven authoring-family tools.
func AuthoringTools(deps AuthoringDeps) []tools.Tool {
	if deps.RateLimiter == nil {
		deps.RateLimiter = NewRetrievalRateLimiter(RetrievalRateLimitConfig{})
	}
	return []tools.Tool{
		&CreateTool{deps: deps},
		&LinkTool{deps: deps},
		&SetPropertyTool{deps: deps},
		&AppendSectionTool{deps: deps},
		&TasksTool{deps: deps},
		&MoveTool{deps: deps},
		&RenameTool{deps: deps},
	}
}

// AuthoringToolNames returns the names AuthoringTools builds, read from the
// tool objects themselves rather than restated as a literal — so a tool
// renamed here and nowhere else fails the policy-seeding test instead of
// shipping silently denied (FR-070/FR-071).
func AuthoringToolNames() []string {
	built := AuthoringTools(AuthoringDeps{})
	out := make([]string, 0, len(built))
	for _, t := range built {
		out = append(out, t.Name())
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// The shared mutation preamble
// ---------------------------------------------------------------------------

// mutationTarget is a resolved, writable collection plus the identity the
// audit record needs.
type mutationTarget struct {
	col         ScopedCollection
	collection  *Collection
	agentID     string
	workspaceID string
	lock        NoteLockConfig
}

// actor is the identity every lower-layer audit record carries.
func (t mutationTarget) actor() AuthorActor {
	return AuthorActor{AgentID: t.agentID, WorkspaceID: t.workspaceID}
}

// begin performs the checks every mutation shares, in the order their failure
// modes require:
//
//  1. resolve the caller's workspace scope and select the collection — the P0
//     isolation gate (US-9);
//  2. re-ask the lifecycle question at write time, so a revoked or broken
//     mount refuses rather than writing into a folder whose grant is gone
//     (US-16);
//  3. open the collection, which is where an unreadable marker surfaces.
//
// Every failure is audited as a refusal before it is returned, so a refusal
// path added later cannot forget to record itself: the only way out of this
// function other than success goes through refuse.
func (d AuthoringDeps) begin(ctx context.Context, op AuthorOperation, args map[string]any) (mutationTarget, *tools.ToolResult) {
	t := mutationTarget{
		agentID: tools.ToolAgentID(ctx),
	}

	// FR-090 is a precondition of writing, not a side effect of it. With no
	// sink there is nowhere to record the mutation OR the refusal, so the
	// only outcome that keeps the requirement true is to refuse the whole
	// operation — loudly, and before anything is touched.
	//
	// This is deliberately not "nil disables auditing". Optional auditing on
	// the agent-reachable path means an agent can write to the operator's
	// real files and leave no record, which is the requirement inverted. The
	// refusal cannot itself be audited, and saying so in the message is the
	// honest form of that.
	if d.Audit == nil {
		return t, tools.ErrorResult(string(op) +
			": refused — no audit sink is configured, and FR-090 requires a record of every " +
			"knowledge-base mutation and every refusal. This refusal is itself unrecorded, " +
			"which is why the write cannot proceed. Configure AuthoringDeps.Audit.")
	}

	// ResolveTurnScope, not ResolveScope(…, ToolWorkspaceID(ctx)): a CLI or
	// scheduled turn carries no workspace id and would otherwise refuse every
	// write over a workspace whose mounts exist (scope_turn.go). The workspace
	// id it resolved is what the audit record names, so the record says which
	// workspace the call was judged against rather than leaving it empty.
	scope, workspaceID := ResolveTurnScope(ctx, d.Home)
	t.workspaceID = workspaceID
	ref := strings.TrimSpace(stringArg(args["collection"]))
	col, ok := scope.Select(ref)
	if !ok {
		return t, d.refuse(op, t, nil, strings.Join(scopeNotes(scope, ref), " "))
	}
	t.col = col

	if err := RequireWritableCollection(string(op), d.Home, t.workspaceID, col); err != nil {
		return t, d.refuse(op, t, nil, err.Error())
	}

	c, err := OpenCollection(col.Root)
	if err != nil {
		return t, d.refuse(op, t, nil, err.Error())
	}
	t.collection = c

	// D14 tier 1's cross-process half needs a lock directory under
	// $OMNIPUS_HOME. Failing to derive one is a refusal rather than a quiet
	// downgrade to in-process-only exclusion: a write that silently drops the
	// guarantee it is documented to hold is the shape of defect this whole
	// stage exists to prevent.
	lockDir, err := LockDirFor(d.Home, col.Root)
	if err != nil {
		return t, d.refuse(op, t, nil, "the write lock directory could not be resolved: "+err.Error())
	}
	t.lock = NoteLockConfig{CollectionRoot: col.Root, LockDir: lockDir}
	return t, nil
}

// refuse audits a refusal to the same sink the lower layers use, and renders
// it for the model.
func (d AuthoringDeps) refuse(op AuthorOperation, t mutationTarget, paths []string, reason string) *tools.ToolResult {
	d.record(AuthorAuditRecord{
		Operation: op, Outcome: AuthorOutcomeRefused,
		AgentID: t.agentID, WorkspaceID: t.workspaceID,
		Collection: t.col.Name, Root: t.col.Root,
		Paths: paths, Reason: reason, At: d.now(),
	})
	return tools.ErrorResult(string(op) + ": " + reason)
}

func (d AuthoringDeps) record(rec AuthorAuditRecord) {
	if d.Audit == nil {
		return
	}
	d.Audit.RecordKnowledgeWrite(rec)
}

// lowerLayerFailure renders an error from author.go or rename.go.
//
// It does NOT re-audit: those layers audit their own refusals, and a second
// record for one refusal is a log an operator cannot count. The one thing it
// adds is the typed conflict payload — a stale write must reach the caller in
// KnowledgeConflictError's shape (code, path, expected_version,
// actual_version) rather than as prose, because "re-read, merge, retry" is
// not something a caller can do with a sentence.
func lowerLayerFailure(op AuthorOperation, err error) *tools.ToolResult {
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		// EVERY conflict from EVERY write path arrives here, carrying both
		// tokens. It did not use to: author.go raised a wrapped sentinel with
		// no actual_version, so a caller was told the note had changed and not
		// what it had changed to — which is precisely the information the
		// re-read/merge/retry loop needs and the only reason the field exists.
		// One conflict type is what makes that impossible to regress.
		return conflictResult(conflict.Path, string(conflict.Expected), string(conflict.Actual), conflict.Error())
	}
	var lockErr *LockTimeoutError
	if errors.As(err, &lockErr) {
		// FR-108: a lock that cannot be acquired ERRORS within the bound. It
		// is a refusal the caller can retry, not a fault of the note.
		return tools.ErrorResult(fmt.Sprintf("%s: %v", op, lockErr))
	}
	return tools.ErrorResult(fmt.Sprintf("%s: %v", op, err))
}

// conflictResult renders KnowledgeConflictError's field set. Empty tokens are
// OMITTED rather than sent as "", matching ConflictError.Wire's rule, so a
// caller cannot mistake "absent" for "the empty version".
func conflictResult(relPath, expected, actual, message string) *tools.ToolResult {
	body := map[string]any{
		"error": message,
		"code":  ConflictCode,
		"path":  relPath,
	}
	if expected != "" {
		body["expected_version"] = expected
	}
	if actual != "" {
		body["actual_version"] = actual
	}
	res := jsonResult(body)
	res.IsError = true
	return res
}

// ---------------------------------------------------------------------------
// Argument shaping
// ---------------------------------------------------------------------------

// ErrNotePathArgument is returned for a note-path ARGUMENT this layer will not
// compose a destination from.
//
// It is not a second containment check: library.CleanRelPath, called by
// author.go and rename.go, remains the authority on what may be addressed, and
// anything this misses is refused there. What it buys is a message naming the
// argument the model got wrong, at the point the model can still fix it.
//
// It deliberately says NOTHING about filename shape. Stage 0 established that
// shape rules do not apply inside a mounted folder (FR-0001b): an operator's
// note called "Meeting: 2026-01-01.md" is a note, not an error.
var ErrNotePathArgument = errors.New("knowledge: invalid note path argument")

// cleanNoteArg normalises a caller-supplied collection-relative note path.
func cleanNoteArg(raw string) (string, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		return "", fmt.Errorf("%w: the path is empty", ErrNotePathArgument)
	}
	if IsAbsoluteTarget(candidate) {
		return "", fmt.Errorf("%w: %q is an absolute path; give a path relative to the collection root", ErrNotePathArgument, raw)
	}
	cleaned := path.Clean(strings.ReplaceAll(candidate, "\\", "/"))
	if cleaned == "." || cleaned == "/" {
		return "", fmt.Errorf("%w: %q names the collection root, not a note", ErrNotePathArgument, raw)
	}
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("%w: %q leaves the collection", ErrNotePathArgument, raw)
	}
	return cleaned, nil
}

// ensureMarkdown appends the markdown extension when the caller left it off.
// Agents write "Weekly Review"; operators expect "Weekly Review.md".
func ensureMarkdown(rel string) string {
	if IsMarkdownPath(rel) {
		return rel
	}
	return rel + ".md"
}

// collectionParam is the "collection" argument every tool here shares.
func collectionParam() map[string]any {
	return map[string]any{
		"type": "string",
		"description": "Which knowledge base, by its name. Leave unset when your workspace " +
			"has exactly one.",
	}
}

// pathParam is the "path" argument every note-addressing tool shares.
func pathParam(what string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": what + ", as a path relative to the collection root.",
	}
}

// expectVersionParam is FR-106's token, as the model sees it.
//
// The description no longer says "leave unset if you have not read the note".
// It used to, and the effect was that the model was being instructed to turn
// off the one mechanism standing between an agent and an operator's lost work:
// FR-106 says a version token is REQUIRED for every write, and a parameter the
// tool description tells you to omit is not required by anything.
func expectVersionParam() map[string]any {
	return map[string]any{
		"type": "string",
		"description": "Required. The version token of the note as you last saw it — every " +
			"knowledge tool that touches a note returns one as 'version'. The write is " +
			"refused if the note changed in the meantime, instead of overwriting whatever " +
			"changed. If you do not have a token, send this call anyway: the refusal comes " +
			"back with the note's current token as 'actual_version', and you retry with that.",
	}
}

// ---------------------------------------------------------------------------
// knowledge_create (US-12, FR-100..FR-102)
// ---------------------------------------------------------------------------

// CreateTool creates a note from the collection's own templates.
type CreateTool struct {
	tools.BaseTool
	deps AuthoringDeps
}

// Name is the registered tool name, seeded explicitly in
// pkg/config/defaults.go and pkg/coreagent/core.go (D17).
func (t *CreateTool) Name() string { return "knowledge_create" }

// Description is what the model reads.
func (t *CreateTool) Description() string {
	return "Create a new note in a knowledge base, starting from one of that collection's own " +
		"templates so it arrives with the frontmatter and structure the collection expects " +
		"instead of blank. Never overwrites an existing note. Only knowledge bases mounted " +
		"into your own workspace can be written to."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *CreateTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *CreateTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in.
func (t *CreateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"collection": collectionParam(),
			"path":       pathParam("Where the new note goes, e.g. 'projects/2026/Kickoff.md'"),
			"title": map[string]any{
				"type":        "string",
				"description": "The note's title. Fills the template's title placeholder.",
			},
			"template": map[string]any{
				"type": "string",
				"description": "Which of the collection's templates to start from, by name. " +
					"Leave unset to create the note from 'body' alone.",
			},
			"body": map[string]any{
				"type": "string",
				"description": "Literal content for the note. Written exactly as given — it is " +
					"never treated as a template.",
			},
		},
		"required": []string{"path"},
	}
}

// Execute creates one note.
func (t *CreateTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	const op = AuthorOpCreate
	target, refusal := t.deps.begin(ctx, op, args)
	if refusal != nil {
		return refusal
	}
	rel, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(op, target, nil, err.Error())
	}

	res, err := CreateNote(OSLinkFS(), target.collection, CreateNoteRequest{
		RelPath:   rel,
		Template:  strings.TrimSpace(stringArg(args["template"])),
		Body:      []byte(stringArg(args["body"])),
		Title:     strings.TrimSpace(stringArg(args["title"])),
		Now:       t.deps.now(),
		NameShape: t.deps.NameShape,
		Audit:     t.deps.Audit,
		Actor:     target.actor(),
		Lock:      target.lock,
	})
	if err != nil {
		return lowerLayerFailure(op, err)
	}
	return jsonResult(map[string]any{
		"collection": target.col.Name,
		"path":       res.RelPath,
		"version":    res.Version,
		"template":   res.Template,
		"bytes":      res.Bytes,
		"created":    true,
	})
}

// ---------------------------------------------------------------------------
// knowledge_link
// ---------------------------------------------------------------------------

// LinkTool adds a wikilink from one note to another, so the calling model
// never has to emit raw "[[…]]" syntax — an explicit D7 authoring rule.
type LinkTool struct {
	tools.BaseTool
	deps AuthoringDeps
}

// Name is the registered tool name.
func (t *LinkTool) Name() string { return "knowledge_link" }

// Description is what the model reads.
func (t *LinkTool) Description() string {
	return "Link one note to another in the same knowledge base. Give the two notes and, " +
		"optionally, the words to display and the heading to put the link under — the link " +
		"syntax is written for you. Linking a note that is already linked changes nothing."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *LinkTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *LinkTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in.
func (t *LinkTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"collection": collectionParam(),
			"path":       pathParam("The note that will contain the link"),
			"target": map[string]any{
				"type": "string",
				"description": "The note to link to — its name, or its path relative to the " +
					"collection root. Must be inside the same collection.",
			},
			"alias": map[string]any{
				"type":        "string",
				"description": "Words to display instead of the target's name. Optional.",
			},
			"section": map[string]any{
				"type": "string",
				"description": "Heading to put the link under, e.g. 'Related'. Created at the " +
					"end of the note when it is not already there. Optional.",
			},
			"expect_version": expectVersionParam(),
		},
		"required": []string{"path", "target", "expect_version"},
	}
}

// Execute adds one link.
func (t *LinkTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	const op = AuthorOpEdit
	target, refusal := t.deps.begin(ctx, op, args)
	if refusal != nil {
		return refusal
	}
	rel, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(op, target, nil, err.Error())
	}
	linkTarget := strings.TrimSpace(stringArg(args["target"]))
	if linkTarget == "" {
		return t.deps.refuse(op, target, []string{rel}, "'target' is required")
	}
	// Containment applies to the LINK TARGET too, not only to the note being
	// edited. A link is a path, and US-10 forbids one that leaves the
	// collection; refusing here means such a link is never written at all,
	// rather than written and then reported unresolved forever.
	if _, cErr := cleanNoteArg(linkTarget); cErr != nil {
		return t.deps.refuse(op, target, []string{rel},
			fmt.Sprintf("the link target %q is not inside this collection", linkTarget))
	}

	expect := strings.TrimSpace(stringArg(args["expect_version"]))
	edit := AddWikilink(linkTarget,
		strings.TrimSpace(stringArg(args["alias"])),
		strings.TrimSpace(stringArg(args["section"])))
	res, err := EditNote(OSLinkFS(), target.collection, EditNoteRequest{
		RelPath:       rel,
		Edits:         []NoteEdit{edit},
		ExpectVersion: expect,
		Now:           t.deps.now(),
		Audit:         t.deps.Audit,
		Actor:         target.actor(),
		Lock:          target.lock,
	})
	if err != nil {
		return lowerLayerFailure(op, err)
	}
	return jsonResult(map[string]any{
		"collection": target.col.Name,
		"path":       res.RelPath,
		"version":    res.Version,
		"linked_to":  linkTarget,
		"changed":    res.Changed,
		// A no-op is reported as such rather than as a success that looks
		// identical to a real one: the caller needs to tell "the link is
		// there because I added it" from "it was already there".
		"already_linked": !res.Changed,
	})
}

// ---------------------------------------------------------------------------
// knowledge_set_property
// ---------------------------------------------------------------------------

// SetPropertyTool sets one frontmatter property, leaving every other byte of
// the note alone.
type SetPropertyTool struct {
	tools.BaseTool
	deps AuthoringDeps
}

// Name is the registered tool name.
func (t *SetPropertyTool) Name() string { return "knowledge_set_property" }

// Description is what the model reads.
func (t *SetPropertyTool) Description() string {
	return "Set one property in a note's frontmatter — status, a date, an owner, anything the " +
		"collection uses. Everything else in the note, including the rest of the frontmatter " +
		"and its comments, is left exactly as it was. You never write YAML yourself."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *SetPropertyTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *SetPropertyTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in.
//
// value is a STRING, and that is a real limitation rather than a
// simplification: the underlying SetProperty edit writes one scalar on one
// line, so a list-valued property such as `tags` cannot be set through this
// tool today. Offering an array parameter the layer beneath cannot honour
// would be a control that reports success and does something else.
func (t *SetPropertyTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"collection": collectionParam(),
			"path":       pathParam("The note whose frontmatter to change"),
			"name": map[string]any{
				"type":        "string",
				"description": "The property name, e.g. 'status'.",
			},
			"value": map[string]any{
				"type": "string",
				"description": "The value, as text. Single values only — a list-valued " +
					"property such as 'tags' cannot be set with this tool.",
			},
			"expect_version": expectVersionParam(),
		},
		"required": []string{"path", "name", "value", "expect_version"},
	}
}

// Execute sets one property.
func (t *SetPropertyTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	const op = AuthorOpEdit
	target, refusal := t.deps.begin(ctx, op, args)
	if refusal != nil {
		return refusal
	}
	rel, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(op, target, nil, err.Error())
	}
	name := strings.TrimSpace(stringArg(args["name"]))
	if name == "" {
		return t.deps.refuse(op, target, []string{rel}, "'name' is required")
	}
	raw, present := args["value"]
	if !present || raw == nil {
		return t.deps.refuse(op, target, []string{rel}, "'value' is required")
	}
	value, ok := raw.(string)
	if !ok {
		return t.deps.refuse(op, target, []string{rel},
			fmt.Sprintf("'value' must be text; lists and objects are not supported by this tool (got %T)", raw))
	}

	expect := strings.TrimSpace(stringArg(args["expect_version"]))
	res, err := EditNote(OSLinkFS(), target.collection, EditNoteRequest{
		RelPath:       rel,
		Edits:         []NoteEdit{SetProperty(name, value)},
		ExpectVersion: expect,
		Now:           t.deps.now(),
		Audit:         t.deps.Audit,
		Actor:         target.actor(),
		Lock:          target.lock,
	})
	if err != nil {
		return lowerLayerFailure(op, err)
	}
	return jsonResult(map[string]any{
		"collection": target.col.Name,
		"path":       res.RelPath,
		"version":    res.Version,
		"property":   name,
		"changed":    res.Changed,
	})
}

// ---------------------------------------------------------------------------
// knowledge_append_section
// ---------------------------------------------------------------------------

// AppendSectionTool appends a section to a note.
type AppendSectionTool struct {
	tools.BaseTool
	deps AuthoringDeps
}

// Name is the registered tool name.
func (t *AppendSectionTool) Name() string { return "knowledge_append_section" }

// Description is what the model reads.
func (t *AppendSectionTool) Description() string {
	return "Add a section — a heading and its content — to the end of a note. The content is " +
		"written literally: anything in it that looks like an instruction or a placeholder " +
		"stays text. Adding a section the note already carries changes nothing."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *AppendSectionTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *AppendSectionTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in.
func (t *AppendSectionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"collection": collectionParam(),
			"path":       pathParam("The note to append to"),
			"heading": map[string]any{
				"type":        "string",
				"description": "The section's heading, e.g. 'Decisions'.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "What the section says. Plain markdown, written literally.",
			},
			"level": map[string]any{
				"type":        "integer",
				"description": "Heading level, 1 to 6. Default 2.",
			},
			"expect_version": expectVersionParam(),
		},
		"required": []string{"path", "heading", "content", "expect_version"},
	}
}

// Execute appends one section.
func (t *AppendSectionTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	const op = AuthorOpEdit
	target, refusal := t.deps.begin(ctx, op, args)
	if refusal != nil {
		return refusal
	}
	rel, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(op, target, nil, err.Error())
	}
	heading := strings.TrimSpace(stringArg(args["heading"]))
	if heading == "" {
		return t.deps.refuse(op, target, []string{rel}, "'heading' is required")
	}
	content := stringArg(args["content"])
	if strings.TrimSpace(content) == "" {
		return t.deps.refuse(op, target, []string{rel}, "'content' is required")
	}
	level := intArg(args["level"], 2)
	if level < 1 || level > 6 {
		return t.deps.refuse(op, target, []string{rel},
			fmt.Sprintf("'level' must be between 1 and 6, got %d", level))
	}

	expect := strings.TrimSpace(stringArg(args["expect_version"]))
	res, err := EditNote(OSLinkFS(), target.collection, EditNoteRequest{
		RelPath:       rel,
		Edits:         []NoteEdit{AppendSectionOnce(level, heading, content)},
		ExpectVersion: expect,
		Now:           t.deps.now(),
		Audit:         t.deps.Audit,
		Actor:         target.actor(),
		Lock:          target.lock,
	})
	if err != nil {
		return lowerLayerFailure(op, err)
	}
	return jsonResult(map[string]any{
		"collection": target.col.Name,
		"path":       res.RelPath,
		"version":    res.Version,
		"heading":    heading,
		"changed":    res.Changed,
	})
}

// ---------------------------------------------------------------------------
// knowledge_move and knowledge_rename — one engine, two front doors
// ---------------------------------------------------------------------------

// knowledgeRenameOp is the audit operation name for a rename or a move. It is
// audit.go's EventKnowledgeNoteRename, so all three write paths — create, edit
// and rename — land in one namespace rather than three.
const knowledgeRenameOp AuthorOperation = EventKnowledgeNoteRename

// RenameTool renames a note in place, keeping its folder.
type RenameTool struct {
	tools.BaseTool
	deps AuthoringDeps
}

// Name is the registered tool name.
func (t *RenameTool) Name() string { return "knowledge_rename" }

// Description is what the model reads.
func (t *RenameTool) Description() string {
	return "Rename a note, keeping it in its folder. Every link pointing at it is updated — in " +
		"note bodies and in other notes' frontmatter — so nothing in the collection is left " +
		"pointing at a name that no longer exists."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *RenameTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *RenameTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in.
func (t *RenameTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"collection": collectionParam(),
			"path":       pathParam("The note to rename"),
			"new_name": map[string]any{
				"type": "string",
				"description": "The note's new name, without a folder. The '.md' extension is " +
					"added when you leave it off.",
			},
			"allow_ambiguity": map[string]any{
				"type": "boolean",
				"description": "Proceed even if the new name would be shared with another note " +
					"elsewhere in the collection. Default false, which refuses instead.",
			},
		},
		"required": []string{"path", "new_name"},
	}
}

// Execute renames one note.
func (t *RenameTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return renameEngine(ctx, t.deps, args, false)
}

// MoveTool moves a note to another folder.
//
// It is the SAME operation as RenameTool — see this file's header. The only
// difference is which argument composes the destination.
type MoveTool struct {
	tools.BaseTool
	deps AuthoringDeps
}

// Name is the registered tool name.
func (t *MoveTool) Name() string { return "knowledge_move" }

// Description is what the model reads.
func (t *MoveTool) Description() string {
	return "Move a note to another folder in the same knowledge base, optionally renaming it " +
		"at the same time. Every link pointing at it is updated, in note bodies and in other " +
		"notes' frontmatter."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *MoveTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *MoveTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in.
func (t *MoveTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"collection": collectionParam(),
			"path":       pathParam("The note to move"),
			"new_folder": map[string]any{
				"type": "string",
				"description": "The folder to move it into, relative to the collection root. " +
					"Use '' or '.' for the collection root itself.",
			},
			"new_name": map[string]any{
				"type":        "string",
				"description": "Optionally rename the note as it moves. Keeps its current name when unset.",
			},
			"allow_ambiguity": map[string]any{
				"type": "boolean",
				"description": "Proceed even if the destination name would be shared with " +
					"another note elsewhere in the collection. Default false, which refuses instead.",
			},
		},
		"required": []string{"path", "new_folder"},
	}
}

// Execute moves one note.
func (t *MoveTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return renameEngine(ctx, t.deps, args, true)
}

// renameEngine is the single implementation behind both names.
//
// isMove selects only how the destination is composed. Everything after that
// — containment, the no-op case, the journalled rewrite, the audited path set
// — is identical, because the operation is identical.
func renameEngine(ctx context.Context, deps AuthoringDeps, args map[string]any, isMove bool) *tools.ToolResult {
	const op = knowledgeRenameOp
	target, refusal := deps.begin(ctx, op, args)
	if refusal != nil {
		return refusal
	}
	from, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return deps.refuse(op, target, nil, err.Error())
	}
	from = ensureMarkdown(from)

	newName := strings.TrimSpace(stringArg(args["new_name"]))
	var to string
	if isMove {
		if newName == "" {
			newName = path.Base(from)
		}
		if strings.ContainsAny(newName, "/\\") {
			return deps.refuse(op, target, []string{from},
				fmt.Sprintf("'new_name' must be a name, not a path: %q", newName))
		}
		to = path.Join(normalizeMoveFolder(stringArg(args["new_folder"])), newName)
	} else {
		if newName == "" {
			return deps.refuse(op, target, []string{from}, "'new_name' is required")
		}
		if strings.ContainsAny(newName, "/\\") {
			return deps.refuse(op, target, []string{from},
				fmt.Sprintf("'new_name' must be a name, not a path — use knowledge_move to change folder: %q", newName))
		}
		to = path.Join(path.Dir(from), newName)
	}
	to, err = cleanNoteArg(to)
	if err != nil {
		return deps.refuse(op, target, []string{from}, err.Error())
	}
	to = ensureMarkdown(to)

	root, err := NewCollectionRoot(OSLinkFS(), target.col.Root)
	if err != nil {
		return deps.refuse(op, target, []string{from}, err.Error())
	}
	renamer := &Renamer{
		FS:      OSLinkFS(),
		Root:    root,
		AgentID: target.agentID,
		Audit:   deps.renameAuditFunc(target),
		Lock:    target.lock,
	}
	res, err := renamer.Rename(RenameRequest{
		From:           from,
		To:             to,
		AllowAmbiguity: boolArg(args["allow_ambiguity"]),
	})
	if err != nil {
		// rename.go has already audited this outcome — including the
		// "incomplete" case, where the journal is retained and the operation
		// is completable. Saying so is the difference between an operator who
		// knows recovery is available and one who does not.
		msg := fmt.Sprintf("%s: %v", op, err)
		if res != nil && res.JournalID != "" {
			msg += fmt.Sprintf(" (journal %s is retained; the rename can be completed or undone)", res.JournalID)
		}
		return tools.ErrorResult(msg)
	}

	out := map[string]any{
		"collection":      target.col.Name,
		"from":            res.From,
		"to":              res.To,
		"changed":         !res.NoOp,
		"rewritten_paths": res.Touched,
		"files_rewritten": res.FilesRewritten,
		"links_rewritten": res.LinksRewritten,
	}
	if res.Ambiguity != nil {
		out["ambiguity"] = fmt.Sprintf(
			"the name %q is now shared with another note in this collection", path.Base(res.To))
	}
	return jsonResult(out)
}

// renameAuditFunc adapts rename.go's event shape onto the one audit sink the
// tool layer uses, so a rename's record sits alongside a create's and an
// edit's rather than in a second place with a second shape.
//
// The full touched-path set rides through: US-15 AS-2 requires "the full set
// of touched paths … not just the renamed note".
func (d AuthoringDeps) renameAuditFunc(t mutationTarget) RenameAuditFunc {
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
			Operation: knowledgeRenameOp,
			Outcome:   outcome,
			AgentID:   t.agentID, WorkspaceID: t.workspaceID,
			Collection: t.col.Name, Root: t.col.Root,
			Paths:  ev.Paths,
			Reason: reason,
			At:     at,
		})
	}
}

// normalizeMoveFolder turns a destination-folder argument into a clean
// relative prefix, with "", "." and "/" all meaning the collection root.
// Anything trying to leave the collection collapses to "", and the composed
// path is then re-checked by cleanNoteArg and again by rename.go.
func normalizeMoveFolder(raw string) string {
	f := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if f == "" {
		return ""
	}
	f = path.Clean(f)
	if f == "." || f == "/" || f == ".." || strings.HasPrefix(f, "../") {
		return ""
	}
	return strings.TrimPrefix(f, "/")
}

// boolArg reads a boolean argument, defaulting to false. A model that sends
// the string "true" means true; anything unrecognised means false, which is
// the safe direction for every flag this layer has.
func boolArg(raw any) bool {
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Edits this layer owns
//
// SetProperty and AppendSectionAt come from author.go. These two do not exist
// there, and they are the two tools' own semantics rather than write
// mechanics: what "add a link" and "add this section unless it is already
// there" mean in markdown.
// ---------------------------------------------------------------------------

// AddWikilink returns an edit that links the note to target.
//
// Adding a link the note already has produces byte-identical content, which
// EditNote reports as Changed=false and does not write — D7's "idempotent
// where the operation allows", achieved by producing the same bytes rather
// than by a separate is-it-there call that could disagree with the edit.
func AddWikilink(target, alias, section string) NoteEdit {
	return func(src []byte) ([]byte, error) {
		if strings.TrimSpace(target) == "" {
			return nil, fmt.Errorf("%w: empty link target", ErrEmptyTarget)
		}
		if linkAlreadyPresent(src, target) {
			return append([]byte(nil), src...), nil
		}
		link := "[[" + target
		if alias != "" {
			link += "|" + alias
		}
		link += "]]"
		return insertUnderSection(src, "- "+link, section, 2)
	}
}

// AppendSectionOnce is AppendSectionAt plus an idempotency guard: a section
// whose heading AND body the note already carries is not added again.
//
// The guard belongs to the tool, not to author.go. AppendSectionAt is
// deliberately an unconditional append whose output has the input as a literal
// byte prefix, and that property is what makes it safe; folding a condition
// into it would weaken the guarantee for every caller. Here the condition is
// checked first, and the primitive is then either used unchanged or not used
// at all.
func AppendSectionOnce(level int, heading, body string) NoteEdit {
	appendIt := AppendSectionAt(level, heading, body)
	return func(src []byte) ([]byte, error) {
		if start, end, found := sectionBounds(src, heading); found {
			if strings.Contains(string(src[start:end]), strings.TrimRight(body, "\r\n")) {
				return append([]byte(nil), src...), nil
			}
		}
		return appendIt(src)
	}
}

// linkAlreadyPresent reports whether the note already links to target.
//
// Comparison ignores the markdown extension and letter case, matching how a
// wikilink actually resolves. A stricter comparison would let "[[Notes/Foo]]"
// and "[[Notes/Foo.md]]" both be written, producing two links to one note.
//
// The case fold is records.FoldKey, not strings.ToLower — a link target is a
// note path, derived from a note NAME, which may be non-ASCII. strings.ToLower
// would let "[[Notes/straße]]" and "[[Notes/STRASSE]]" be written as two
// distinct links to what is, once folded correctly, one note.
func linkAlreadyPresent(src []byte, target string) bool {
	want := records.FoldKey(trimMarkdownExt(normalizeRel(target)))
	for _, l := range ExtractLinks(src) {
		if records.FoldKey(trimMarkdownExt(normalizeRel(l.Target))) == want {
			return true
		}
	}
	return false
}

// insertUnderSection puts text at the end of the named section, creating that
// section at the end of the note when it is not there, or appending to the end
// of the note when no section was named.
func insertUnderSection(src []byte, text, section string, level int) ([]byte, error) {
	eol := "\n"
	if strings.Contains(string(src), "\r\n") {
		eol = "\r\n"
	}
	if section != "" {
		if _, end, found := sectionBounds(src, section); found {
			out := make([]byte, 0, len(src)+len(text)+2*len(eol))
			out = append(out, src[:end]...)
			out = append(out, eol...)
			out = append(out, text...)
			out = append(out, eol...)
			out = append(out, src[end:]...)
			return out, nil
		}
		return AppendSectionAt(level, section, text)(src)
	}
	var b strings.Builder
	b.Write(src)
	if len(src) > 0 && !strings.HasSuffix(string(src), "\n") {
		b.WriteString(eol)
	}
	b.WriteString(eol)
	b.WriteString(text)
	b.WriteString(eol)
	return []byte(b.String()), nil
}

// sectionBounds locates the BODY of the section introduced by heading.
//
// start is the offset just after the heading line; end is the offset of the
// last non-blank byte of the section — which is where an append belongs,
// because a blank line separating this section from the next heading must
// stay a separator rather than become the middle of the section.
//
// Headings inside frontmatter and inside fenced code blocks are not headings.
// ExtractHeadings already excludes both, which is why this uses it rather than
// scanning for "#" itself: a "# TODO" comment inside a shell fence would
// otherwise become a section an agent appends into.
func sectionBounds(src []byte, heading string) (start, end int, found bool) {
	want := strings.TrimSpace(heading)
	if want == "" {
		return 0, 0, false
	}
	headings := ExtractHeadings(src)
	// records.FoldKey, not strings.EqualFold: a heading is note text and may
	// be non-ASCII (this package's rule for text comparison — see
	// normalizeHeading in graph.go, which folds the identical way for the
	// identical reason).
	wantFold := records.FoldKey(want)
	for i, h := range headings {
		if records.FoldKey(strings.TrimSpace(h.Text)) != wantFold {
			continue
		}
		lineEnd := int(h.Offset)
		for lineEnd < len(src) && src[lineEnd] != '\n' {
			lineEnd++
		}
		if lineEnd < len(src) {
			lineEnd++
		}
		sectionEnd := len(src)
		for _, next := range headings[i+1:] {
			if next.Level <= h.Level {
				sectionEnd = int(next.Offset)
				break
			}
		}
		trimmed := sectionEnd
		for trimmed > lineEnd && (src[trimmed-1] == '\n' || src[trimmed-1] == '\r') {
			trimmed--
		}
		return lineEnd, trimmed, true
	}
	return 0, 0, false
}

// ---------------------------------------------------------------------------
// knowledge_tasks — A READ. See this file's header for why.
// ---------------------------------------------------------------------------

const (
	// TasksDefaultLimit is how many task items one call returns by default.
	TasksDefaultLimit = 100
	// TasksMaxLimit is the ceiling. A larger request is CLAMPED and the clamp
	// is reported, matching knowledge_search's FR-037 behaviour rather than
	// erroring — an agent that asks for too much should get an answer and be
	// told it was trimmed.
	TasksMaxLimit = 500
	// TasksMaxFiles bounds how many notes one call reads. Reaching it sets the
	// response's incomplete flag; an incomplete scan presented as a complete
	// one is the dishonesty US-6 exists to prevent.
	TasksMaxFiles = 5000
)

// taskLine matches a markdown checkbox item: an optional indent, a list
// bullet, a bracketed single-character state, then the text.
var taskLine = regexp.MustCompile(`^[ \t]*[-*+][ \t]+\[([ xX])\][ \t]*(.*)$`)

// TasksTool lists the checkbox tasks in a knowledge base.
type TasksTool struct {
	tools.BaseTool
	deps AuthoringDeps
}

// Name is the registered tool name.
func (t *TasksTool) Name() string { return "knowledge_tasks" }

// Description is what the model reads.
func (t *TasksTool) Description() string {
	return "List the tasks written as checkboxes in a knowledge base — the '- [ ]' and '- [x]' " +
		"lines — with the note and line each one is on. Reads only; it does not tick, add or " +
		"change anything. Only knowledge bases mounted into your own workspace can be read."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *TasksTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *TasksTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in.
func (t *TasksTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"collection": collectionParam(),
			"path": map[string]any{
				"type":        "string",
				"description": "List tasks in this one note only, relative to the collection root. Optional.",
			},
			"folder": map[string]any{
				"type":        "string",
				"description": "Restrict to this folder inside the collection. Optional.",
			},
			"status": map[string]any{
				"type":        "string",
				"enum":        []string{"open", "done", "all"},
				"description": "Which tasks to list. Default 'open'.",
			},
			"limit": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf("How many tasks to return. Default %d, maximum %d; a "+
					"larger request is reduced to the maximum and the response says so.",
					TasksDefaultLimit, TasksMaxLimit),
			},
		},
	}
}

// knowledgeTask is one checkbox item as the model sees it.
type knowledgeTask struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Status string `json:"status"`
	Text   string `json:"text"`
}

// tasksResponse is the tool's payload.
type tasksResponse struct {
	Collection         string          `json:"collection,omitempty"`
	Status             string          `json:"status"`
	Tasks              []knowledgeTask `json:"tasks"`
	Open               int             `json:"open"`
	Done               int             `json:"done"`
	Incomplete         bool            `json:"incomplete,omitempty"`
	Clamped            bool            `json:"clamped,omitempty"`
	Notes              []string        `json:"notes,omitempty"`
	Problems           []string        `json:"problems,omitempty"`
	CollectionsInScope []string        `json:"collections_in_scope,omitempty"`
}

// Execute lists tasks. It writes nothing and emits no mutation audit record,
// because it performs no mutation.
func (t *TasksTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	const op = "knowledge_tasks"
	if res := checkRetrievalRate(t.deps.RateLimiter, op, tools.ToolAgentID(ctx)); res != nil {
		return res
	}
	// See scope_turn.go: a CLI or scheduled turn carries no workspace id.
	scope, workspaceID := ResolveTurnScope(ctx, t.deps.Home)
	ref := strings.TrimSpace(stringArg(args["collection"]))
	col, ok := scope.Select(ref)
	if !ok {
		// FR-053: out of scope is an EMPTY RESULT SET for a read, so the
		// response cannot distinguish "another workspace's" from "absent".
		return jsonResult(tasksResponse{
			Status: "open", Tasks: []knowledgeTask{},
			Notes: scopeNotes(scope, ref), CollectionsInScope: scope.Names(),
		})
	}

	status := strings.ToLower(strings.TrimSpace(stringArg(args["status"])))
	switch status {
	case "", "open":
		status = "open"
	case "done", "all":
	default:
		return tools.ErrorResult(fmt.Sprintf("%s: 'status' must be open, done or all, got %q", op, status))
	}

	limit := intArg(args["limit"], TasksDefaultLimit)
	resp := tasksResponse{
		Collection: col.Name, Status: status, Tasks: []knowledgeTask{},
		CollectionsInScope: scope.Names(),
	}
	if limit > TasksMaxLimit {
		limit = TasksMaxLimit
		resp.Clamped = true
		resp.Notes = append(resp.Notes,
			fmt.Sprintf("More tasks were requested than the %d-item maximum, so the request was reduced.", TasksMaxLimit))
	}
	if limit < 1 {
		limit = TasksDefaultLimit
	}
	if scope.Truncated() {
		resp.Incomplete = true
		resp.Notes = append(resp.Notes,
			"Collection enumeration hit its bound, so some knowledge bases in this workspace may not be listed.")
	}

	// A collection can go broken between scope resolution and this read.
	// Saying so beats a confident empty list (US-16 AS-4).
	if state, name := ResolveMountState(t.deps.Home, workspaceID, col.Root); state != MountStateActive {
		resp.Incomplete = true
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"The mount %q for this knowledge base is %s, so its notes could not be read.", name, state))
		return jsonResult(resp)
	}

	fsys := OSLinkFS()
	root, err := NewCollectionRoot(fsys, col.Root)
	if err != nil {
		return tools.ErrorResult(fmt.Sprintf("%s: %v", op, err))
	}

	var files []string
	if single := strings.TrimSpace(stringArg(args["path"])); single != "" {
		rel, cErr := cleanNoteArg(single)
		if cErr != nil {
			return tools.ErrorResult(fmt.Sprintf("%s: %v", op, cErr))
		}
		files = []string{ensureMarkdown(rel)}
	} else {
		walk, wErr := WalkContained(fsys, root)
		if wErr != nil {
			return tools.ErrorResult(fmt.Sprintf("%s: %v", op, wErr))
		}
		folder := normalizeFolder(args["folder"])
		for _, f := range walk.Files {
			if !IsMarkdownPath(f) {
				continue
			}
			if folder != "" && !strings.HasPrefix(f, folder+"/") {
				continue
			}
			files = append(files, f)
		}
		for _, s := range walk.Skipped {
			resp.Problems = append(resp.Problems, fmt.Sprintf("%s: skipped (%s)", s.RelPath, s.Reason))
		}
		if len(walk.Skipped) > 0 {
			resp.Incomplete = true
		}
	}

	if len(files) > TasksMaxFiles {
		files = files[:TasksMaxFiles]
		resp.Incomplete = true
		resp.Notes = append(resp.Notes, fmt.Sprintf(
			"Only the first %d notes were read; the collection has more.", TasksMaxFiles))
	}

	for _, rel := range files {
		abs, rErr := root.ResolveContained(fsys, rel)
		if rErr != nil {
			resp.Problems = append(resp.Problems, fmt.Sprintf("%s: %v", rel, rErr))
			resp.Incomplete = true
			continue
		}
		content, cErr := ReadNoteContent(fsys, abs)
		if cErr != nil {
			// FR-111: an evicted or unreadable note is REPORTED and absent
			// from the answer, never silently counted as having no tasks.
			resp.Problems = append(resp.Problems, fmt.Sprintf("%s: %v", rel, cErr))
			resp.Incomplete = true
			continue
		}
		for _, item := range extractTasks(rel, content) {
			if item.Status == "open" {
				resp.Open++
			} else {
				resp.Done++
			}
			if status != "all" && item.Status != status {
				continue
			}
			if len(resp.Tasks) < limit {
				resp.Tasks = append(resp.Tasks, item)
			} else {
				resp.Incomplete = true
			}
		}
	}
	if resp.Incomplete && len(resp.Notes) == 0 {
		resp.Notes = append(resp.Notes, "Some notes could not be read, so this list may be incomplete.")
	}
	return jsonResult(resp)
}

// extractTasks pulls every checkbox item out of one note.
func extractTasks(relPath string, src []byte) []knowledgeTask {
	var out []knowledgeTask
	line := 0
	for _, raw := range strings.Split(string(src), "\n") {
		line++
		m := taskLine.FindStringSubmatch(strings.TrimSuffix(raw, "\r"))
		if m == nil {
			continue
		}
		status := "open"
		if m[1] != " " {
			status = "done"
		}
		out = append(out, knowledgeTask{
			Path: relPath, Line: line, Status: status,
			Text: strings.TrimSpace(m[2]),
		})
	}
	return out
}
