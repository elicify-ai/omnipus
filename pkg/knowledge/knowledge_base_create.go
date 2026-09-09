// Omnipus — KB-1 (defect-list-knowledge-base-ux-2026-09-08.md, founder-
// ratified 2026-09-08): knowledge_base_create, the missing "make a knowledge
// base" verb.
//
// # The gap this closes
//
// An agent had sixteen knowledge_*-named things to reach for and none of
// them created a knowledge base — knowledge_create_note (formerly
// knowledge_create, see its own rename note in authoring_tools.go) creates a
// NOTE inside a knowledge base that already exists. An agent asked to "set
// up a knowledge base for this project" could not complete the task at all;
// the only door was the SPA's Library "+" menu (POST
// /library/{workspace_id}/vaults, pkg/gateway/rest_library.go's
// handleLibraryCreateVault).
//
// # Why this reuses CreateInWorkspace rather than reimplementing it
//
// "Make a knowledge base" is exactly one act: mint a marker at a folder
// inside the workspace's own work tree, refusing if something is already
// there. That act already lives in this package as CreateInWorkspace
// (detect.go) — handleLibraryCreateVault calls it too. There is no import-
// cycle problem to solve here (unlike the REST handler's OWN name-shape and
// collision checks, which live in pkg/library/pkg/gateway and this tool
// reaches directly, since pkg/knowledge already imports pkg/library and
// neither imports pkg/gateway back): this tool is a second FRONT DOOR onto
// the same primitive, not a second implementation of it. The only things
// this file adds on top are the four this package's other mutating tools
// add (see authoring_tools.go's own header): workspace scope, an
// FR-090 audit record, argument shaping, and the two preflight checks
// (name shape, collision) handleLibraryCreateVault performs before calling
// CreateInWorkspace — reused from pkg/library directly, not reinvented,
// because library.Root.ValidateCreateName/StatDir ARE the REST handler's own
// checks.
//
// # Why this does not go through AuthoringDeps.begin
//
// begin (authoring_tools.go) resolves the "collection" ARGUMENT to an
// EXISTING ScopedCollection via scope.Select — every other mutating tool in
// this package writes into a knowledge base that is already there. This
// tool's whole job is to bring one into existence, so there is nothing for
// scope.Select to find yet; begin's shared preamble does not fit and is not
// reused here beyond the two checks it itself reuses (the audit-sink
// precondition and the FR-090 refusal recording), which this file inlines
// against AuthoringDeps' own exported helpers.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/library"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// authorOpCreateBase is this tool's audit operation (FR-090, US-15).
//
// "knowledge.base.create", not "knowledge.note.create" (AuthorOpCreate):
// this operation's target is the collection itself, not a note inside one —
// the same "operation names what it actually does" rule authorOpConfigure's
// own doc comment states for the schema/view control plane.
const authorOpCreateBase AuthorOperation = "knowledge.base.create"

// createBaseArgNames is every argument this tool accepts. Consistent with
// this package's other tools (see describeArgNames/editArgNames), an
// argument outside this set is refused rather than silently ignored.
var createBaseArgNames = []string{"name", "parent_path"}

// CreateBaseTool is knowledge_base_create.
type CreateBaseTool struct {
	tools.BaseTool
	deps AuthoringDeps
}

// NewCreateBaseTool builds the tool.
func NewCreateBaseTool(deps AuthoringDeps) *CreateBaseTool {
	return &CreateBaseTool{deps: deps}
}

// Name is the registered tool name, seeded explicitly in
// pkg/config/defaults.go and pkg/coreagent/core.go.
func (t *CreateBaseTool) Name() string { return "knowledge_base_create" }

// Description is what the model reads.
func (t *CreateBaseTool) Description() string {
	return "Create a new, empty knowledge base in your workspace's own Library. Use this when " +
		"none of the knowledge bases in scope is the right place — for example, setting up a " +
		"knowledge base for a new project. This makes the BASE itself, not a note: to add a " +
		"note inside a knowledge base that already exists, use knowledge_edit's create op " +
		"instead. Refuses if something already exists at the target location, or if the " +
		"location would fall outside your workspace."
}

// Scope classifies the tool for per-agent visibility filtering.
func (t *CreateBaseTool) Scope() tools.ToolScope { return tools.ScopeGeneral }

// Category groups the tool in the picker UI.
func (t *CreateBaseTool) Category() tools.ToolCategory { return tools.CategoryMemory }

// Parameters is the JSON schema the model fills in.
func (t *CreateBaseTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type": "string",
				"description": "The knowledge base's name. Becomes both its folder name and its " +
					"display name — what you pass as 'collection' to every other knowledge_* tool.",
			},
			"parent_path": map[string]any{
				"type": "string",
				"description": "Where in your workspace's Library to create it, as a folder path " +
					"relative to the Library root. Leave unset to create it at the root.",
			},
		},
		"required": []string{"name"},
	}
}

// Execute creates one knowledge base.
func (t *CreateBaseTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	const op = authorOpCreateBase

	if unknown := unknownArgs(args, createBaseArgNames); len(unknown) > 0 {
		return tools.ErrorResult(fmt.Sprintf(
			"knowledge_base_create: unknown argument(s) %s; accepted: %s",
			strings.Join(unknown, ", "), strings.Join(createBaseArgNames, ", ")))
	}

	// FR-090 precondition, mirroring AuthoringDeps.begin's own reasoning
	// (authoring_tools.go): with no sink there is nowhere to record either
	// the mutation or the refusal, so the only outcome that keeps FR-090 true
	// is to refuse the whole call before anything is touched.
	if t.deps.Audit == nil {
		return tools.ErrorResult(string(op) +
			": refused — no audit sink is configured, and FR-090 requires a record of every " +
			"knowledge-base mutation and every refusal. This refusal is itself unrecorded, " +
			"which is why the write cannot proceed. Configure AuthoringDeps.Audit.")
	}

	workspaceID := TurnWorkspaceID(ctx, t.deps.Home)
	agentID := tools.ToolAgentID(ctx)

	refuse := func(reason string) *tools.ToolResult {
		t.deps.record(AuthorAuditRecord{
			Operation: op, Outcome: AuthorOutcomeRefused,
			AgentID: agentID, WorkspaceID: workspaceID,
			Reason: reason, At: t.deps.now(),
		})
		return tools.ErrorResult(string(op) + ": " + reason)
	}

	if workspaceID == "" {
		return refuse("no workspace could be resolved for this turn; a knowledge base can only be created inside a workspace")
	}

	name := strings.TrimSpace(stringArg(args["name"]))
	if name == "" {
		return refuse("'name' is required")
	}
	if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
		return refuse(fmt.Sprintf(
			"%q is not a valid knowledge base name — it must not be '.', '..', or contain a path separator", name))
	}

	parentRel := ""
	if raw := strings.TrimSpace(stringArg(args["parent_path"])); raw != "" {
		cleaned, err := library.CleanRelPath(raw)
		if err != nil {
			return refuse(fmt.Sprintf("invalid parent_path %q: %v", raw, err))
		}
		parentRel = cleaned
	}
	joined := name
	if parentRel != "" {
		joined = parentRel + "/" + name
	}
	rel, err := library.CleanRelPath(joined)
	if err != nil || rel == "" {
		return refuse(fmt.Sprintf("invalid path %q", joined))
	}

	root, err := library.OpenRoot(t.deps.Home, workspaceID)
	if err != nil {
		return refuse(fmt.Sprintf("could not open the workspace library: %v", err))
	}
	defer func() {
		if cerr := root.Close(); cerr != nil {
			slog.Warn("knowledge_base_create: closing the library root failed",
				"workspace_id", workspaceID, "error", cerr)
		}
	}()

	// Name shape (FR-0001a) and the parent's own existence — the SAME checks
	// handleLibraryCreateVault runs before it calls CreateInWorkspace.
	if verr := root.ValidateCreateName(rel); verr != nil {
		return refuse(verr.Error())
	}
	if parentRel != "" {
		if _, statErr := root.StatDir(parentRel); statErr != nil {
			return refuse(fmt.Sprintf("parent_path %q: %v", parentRel, statErr))
		}
	}
	// Collision (409-equivalent): CreateInWorkspace's own check only catches
	// "already a knowledge base" (an existing marker) — it would otherwise
	// happily adopt a plain, non-knowledge-base folder that is already there.
	// Reject any existing entry at rel outright, exactly as the REST handler
	// does, rather than silently converting it.
	switch _, statErr := root.StatDir(rel); {
	case statErr == nil, errors.Is(statErr, library.ErrNotDir):
		return refuse(fmt.Sprintf("%q already exists", rel))
	case errors.Is(statErr, library.ErrNotFound):
		// Expected: nothing there yet.
	default:
		return refuse(statErr.Error())
	}

	collection, err := CreateInWorkspace(t.deps.Home, workspaceID, rel, Marker{DisplayName: name})
	if err != nil {
		switch {
		case errors.Is(err, ErrAlreadyKnowledgeBase):
			return refuse(fmt.Sprintf("%q already exists", rel))
		case errors.Is(err, ErrNestedKnowledgeBase):
			return refuse(fmt.Sprintf(
				"%q is inside an existing knowledge base; a knowledge base cannot be created inside another one — "+
					"choose a location that is not already part of a knowledge base, or use knowledge_edit's create op "+
					"to add a note inside the existing one instead", rel))
		case errors.Is(err, ErrMarkerInvalid):
			return refuse(fmt.Sprintf("invalid knowledge base name: %v", err))
		case errors.Is(err, ErrOutsideCollection):
			return refuse(fmt.Sprintf("%q resolves outside the workspace", rel))
		default:
			return refuse(err.Error())
		}
	}

	// Seed the two records subdirectories, matching what
	// handleLibraryCreateVault does for a Library-created knowledge base —
	// so a base created by an agent looks identical, on disk, to one created
	// from the SPA.
	if mkErr := os.MkdirAll(records.SchemaDir(collection.Root()), 0o755); mkErr != nil {
		return refuse(fmt.Sprintf("created the knowledge base but could not seed its schema directory: %v", mkErr))
	}
	if mkErr := os.MkdirAll(records.ViewsDir(collection.Root()), 0o755); mkErr != nil {
		return refuse(fmt.Sprintf("created the knowledge base but could not seed its views directory: %v", mkErr))
	}

	t.deps.record(AuthorAuditRecord{
		Operation: op, Outcome: AuthorOutcomeApplied,
		AgentID: agentID, WorkspaceID: workspaceID,
		Collection: name, Root: collection.Root(),
		Paths: []string{rel}, At: t.deps.now(),
	})
	return jsonResult(map[string]any{
		"collection": name,
		"path":       rel,
		"created":    true,
	})
}
