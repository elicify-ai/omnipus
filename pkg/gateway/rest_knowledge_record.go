// Omnipus — ADR-083 Step 5 (CW-4/CW-7): the first REST wiring of the typed
// record layer (ADR-068). Three paths:
//
//	GET  /api/v1/library/{workspace_id}/knowledge/record-schema
//	GET  /api/v1/library/{workspace_id}/knowledge/records/{id}
//	POST /api/v1/library/{workspace_id}/knowledge/records
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/fileutil"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ---------------------------------------------------------------------------
// WHAT THIS FILE IS, AND WHAT IT REUSES RATHER THAN REIMPLEMENTS
//
// A record IS a note (ADR-068 D1). This file therefore never touches disk
// itself: schema loading is records.LoadSchemas, the write mechanics (lock,
// version compare-and-swap, splice, atomic write, audit-on-refusal shape) are
// pkg/knowledge's EditNote/CreateNote, and the conflict body is
// pkg/knowledge/version.go's *ConflictError.Wire() — the SAME KnowledgeConflictError
// type and the SAME version-token scheme VaultFindRow.version_token uses
// (ADR-083 EMB-086), never a second one.
//
// THE WRITE DOOR (CW-7) enforces, SERVER-SIDE, the two ADR-068 guards a client
// cannot be trusted to police itself: a property whose declared type is
// "relation" or "person" is refused (FR-045 — those are RelationWriteRequest's
// job), and a property carrying a Formula declaration is refused (FR-046 — a
// derived value is never written into frontmatter). Both checks run against
// the property's OWN declaration from the resolved schema, never against
// anything the request claims about itself.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// GET .../knowledge/record-schema
// ---------------------------------------------------------------------------

// handleKnowledgeRecordSchema answers RecordSchema: every record type
// declared across the caller's workspace scope (FR-060), plus every schema
// file that failed to load.
//
// Scoped to the WORKSPACE (there is no collection selector on this path,
// unlike knowledge_find/knowledge/view) — a workspace with several mounted
// knowledge bases sees the union of what each declares, first-declaration
// wins on a type name collision ACROSS collections (a collision within one
// vault is FR-003's duplicate-type-declaration and is already reported as a
// problem by that vault's own LoadSchemas).
func (a *restAPI) handleKnowledgeRecordSchema(w http.ResponseWriter, r *http.Request, workspaceID string) {
	if !a.allowKnowledgeRetrieval(w, workspaceID) {
		return
	}

	out := gen.RecordSchema{Types: []gen.RecordType{}, Problems: []gen.RecordProblem{}}
	seen := map[string]bool{}
	for _, col := range knowledge.ResolveScope(a.homePath, workspaceID).Collections() {
		set, report, err := records.LoadSchemas(col.Root)
		if err != nil {
			logger.ErrorCF("rest", "knowledge: load record schemas",
				map[string]any{"workspace_id": workspaceID, "collection": col.Name, "error": err.Error()})
			continue
		}
		for _, typeName := range set.Types() {
			if seen[typeName] {
				continue
			}
			seen[typeName] = true
			sc, ok := set.Get(typeName)
			if !ok {
				continue
			}
			out.Types = append(out.Types, recordTypeWire(sc))
		}
		if report != nil {
			for _, rej := range report.Rejections {
				if p, ok := schemaRejectionProblem(rej); ok {
					out.Problems = append(out.Problems, p)
				} else {
					// FR-002/FR-003 are the two schema-load faults
					// RecordSchema's `problems` enum names
					// (RecordProblemCode has exactly missing_schema_version
					// and duplicate_type_declaration for this class); the
					// other SchemaRejectionCode values (unreadable, invalid
					// YAML, unsupported version, missing type, no
					// properties, bad property, unknown key) have no
					// faithful RecordProblemCode to report under — reporting
					// them under a code that means something else would be a
					// worse answer than a log line naming the file, so they
					// are logged for the operator instead.
					logger.WarnCF("rest", "knowledge: schema file rejected with no reportable RecordProblem code",
						map[string]any{"workspace_id": workspaceID, "collection": col.Name,
							"code": string(rej.Code), "reason": rej.Reason, "paths": rej.Paths})
				}
			}
		}
	}
	sort.Slice(out.Types, func(i, j int) bool { return out.Types[i].Type < out.Types[j].Type })
	jsonOK(w, out)
}

// recordTypeWire renders one loaded *records.Schema as a RecordType.
func recordTypeWire(sc *records.Schema) gen.RecordType {
	rt := gen.RecordType{
		SchemaVersion: sc.SchemaVersion,
		Type:          sc.Type,
		Properties:    []gen.PropertyDef{},
	}
	if sc.Label != "" {
		label := sc.Label
		rt.Label = &label
	}
	if sc.Identity.Prefix != "" {
		prefix := sc.Identity.Prefix
		rt.IdentityPrefix = &prefix
	}
	if sc.SourcePath != "" {
		src := sc.SourcePath
		rt.SourcePath = &src
	}
	for _, name := range sc.PropertyOrder {
		if p, ok := sc.Property(name); ok {
			rt.Properties = append(rt.Properties, p.Wire())
		}
	}
	return rt
}

// schemaRejectionProblem maps the two SchemaRejectionCode values RecordSchema's
// closed RecordProblemCode enum has a faithful code for (see the call site's
// own comment for the other seven).
func schemaRejectionProblem(rej records.SchemaRejection) (gen.RecordProblem, bool) {
	var code gen.RecordProblemCode
	switch rej.Code {
	case records.RejectMissingVersion:
		code = gen.MissingSchemaVersion
	case records.RejectDuplicateType:
		code = gen.DuplicateTypeDeclaration
	default:
		return gen.RecordProblem{}, false
	}
	p := gen.RecordProblem{Code: code, Reason: rej.Reason, Records: []string{}}
	if len(rej.Paths) > 0 {
		paths := append([]string(nil), rej.Paths...)
		p.Paths = &paths
	}
	return p, true
}

// ---------------------------------------------------------------------------
// GET .../knowledge/records/{id}
// ---------------------------------------------------------------------------

// handleKnowledgeRecordGet answers VaultRecord for one record id, searching
// every knowledge base in the caller's workspace scope.
func (a *restAPI) handleKnowledgeRecordGet(w http.ResponseWriter, r *http.Request, workspaceID, id string) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid record id")
		return
	}
	if !a.allowKnowledgeRetrieval(w, workspaceID) {
		return
	}

	found, ok, err := findVaultRecordByID(a.homePath, workspaceID, id)
	if err != nil {
		logger.ErrorCF("rest", "knowledge: search for record by id",
			map[string]any{"workspace_id": workspaceID, "id": id, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		jsonErr(w, http.StatusNotFound, "record not found")
		return
	}

	rec := records.ParseRecord(found.relPath, found.frontmatter)
	jsonOK(w, buildVaultRecordWire(found.schema, rec, found.relPath, string(found.version)))
}

// buildVaultRecordWire renders one parsed record against its resolved
// schema. Derived values (D9, FR-046) are never present here because a
// schema loaded through LoadSchemas can never declare a Formula property in
// the first place (see records.Property.Wire's own note) — every property
// this walks is a genuinely stored one.
func buildVaultRecordWire(sc *records.Schema, rec records.Record, relPath, versionToken string) gen.VaultRecord {
	out := gen.VaultRecord{
		Id:         rec.ID(),
		Type:       sc.Type,
		Path:       relPath,
		Properties: []gen.RecordPropertyValue{},
	}
	if versionToken != "" {
		vt := versionToken
		out.VersionToken = &vt
	}
	title := titleFromPath(relPath)
	if title != "" {
		out.Title = &title
	}
	for _, name := range sc.PropertyOrder {
		prop, ok := sc.Property(name)
		if !ok {
			continue
		}
		pv := records.ResolveProperty(rec, prop)
		item := gen.RecordPropertyValue{Property: name, Values: []gen.RecordValue{}}
		t := gen.RecordPropertyValueType(prop.Type)
		item.Type = &t
		for _, v := range pv.Values {
			item.Values = append(item.Values, typedValueWire(v))
		}
		out.Properties = append(out.Properties, item)
	}
	return out
}

// titleFromPath mirrors knowledgefind/project.go's titleOf (the note's
// filename stem, D7's "filename is identity") — deliberately a second,
// tiny copy rather than an exported dependency on the query engine package
// for one three-line function.
func titleFromPath(relPath string) string {
	base := relPath
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".md")
}

// typedValueWire converts one resolved value into its wire RecordValue,
// tagged by the type the value itself carries.
//
// Relation and person values render with Resolved: false always. Building an
// accurate RecordRef needs a link-graph resolution pass (the same one
// knowledge_find's Deps.Resolve performs), which this single-record read does
// not open — Resolved: false is a documented, legitimate state (RecordRef.yaml:
// "false for a dangling, ambiguous or wrong-type target"), not a defect, and
// Link (the durable, human-editable form) is always present regardless.
func typedValueWire(v records.TypedValue) gen.RecordValue {
	rv := gen.RecordValue{Type: gen.RecordValueType(v.Type)}
	switch v.Type {
	case records.TypeText:
		t := v.Text
		rv.Text = &t
	case records.TypeEnum:
		e := v.Enum.Name
		rv.Enum = &e
	case records.TypeDate:
		d := v.Date.String()
		rv.Date = &d
	case records.TypeInteger:
		n := v.Number.String()
		rv.Integer = &n
	case records.TypeDecimal:
		n := v.Number.String()
		rv.Decimal = &n
	case records.TypeCheckbox:
		b := v.Bool
		rv.Checkbox = &b
	case records.TypeRelation, records.TypePerson:
		ref := &gen.RecordRef{Link: v.Link.Raw, Resolved: false}
		if ref.Link == "" {
			ref.Link = v.Link.Target
		}
		if v.Type == records.TypeRelation {
			rv.Relation = ref
		} else {
			rv.Person = ref
		}
	}
	return rv
}

// foundVaultRecord is one record located by a workspace-scope search.
type foundVaultRecord struct {
	scoped      knowledge.ScopedCollection
	collection  *knowledge.Collection
	relPath     string
	frontmatter []byte
	schema      *records.Schema
	schemaSet   *records.SchemaSet
	version     knowledge.VersionToken
}

// findVaultRecordByID walks every knowledge base in scope looking for a note
// whose declared identifier equals id AND whose declared type resolves in
// that collection's own schema set (FR-005: an id on a note of an
// unrecognised type is not a record).
//
// A FULL WALK, NOT AN INDEX LOOKUP — the same trade-off
// pkg/knowledge/knowledge_restructure_trash.go's findLiveRecordByID makes and
// documents: correctness of a lookup that governs a write is worth more than
// the cost of a walk, and the properties index is not reachable from the
// gateway package without duplicating pkg/records/propindex's contract here.
// A collection that declares no record types at all is skipped without a
// walk (there is nothing an id there could match).
func findVaultRecordByID(home, workspaceID, id string) (foundVaultRecord, bool, error) {
	fsys := knowledge.OSLinkFS()
	for _, sc := range knowledge.ResolveScope(home, workspaceID).Collections() {
		set, _, err := records.LoadSchemas(sc.Root)
		if err != nil {
			return foundVaultRecord{}, false, fmt.Errorf("load record schemas for %q: %w", sc.Name, err)
		}
		if set.Len() == 0 {
			continue
		}
		col, err := knowledge.OpenCollection(sc.Root)
		if err != nil {
			continue
		}
		root, err := knowledge.NewCollectionRoot(fsys, col.Root())
		if err != nil {
			continue
		}
		wr, err := knowledge.WalkContained(fsys, root)
		if err != nil {
			return foundVaultRecord{}, false, fmt.Errorf("walk %q: %w", sc.Name, err)
		}
		for _, rel := range wr.Files {
			if !knowledge.IsMarkdownPath(rel) {
				continue
			}
			abs := filepath.Join(root.Path(), filepath.FromSlash(rel))
			data, rerr := os.ReadFile(abs)
			if rerr != nil {
				continue
			}
			rec := records.ParseRecord(rel, data)
			if rec.ID() != id {
				continue
			}
			schema, ok := set.Get(rec.TypeName())
			if !ok {
				continue
			}
			nv, verr := knowledge.ReadNoteVersion(col, rel)
			token := knowledge.VersionToken("")
			if verr == nil && nv.Exists {
				token = nv.Token
			}
			return foundVaultRecord{
				scoped: sc, collection: col, relPath: rel, frontmatter: data,
				schema: schema, schemaSet: set, version: token,
			}, true, nil
		}
	}
	return foundVaultRecord{}, false, nil
}

// ---------------------------------------------------------------------------
// POST .../knowledge/records
// ---------------------------------------------------------------------------

// recordWriteRefusalReasons — the audit vocabulary for a record write that
// never reached disk, matching the precedent rest_library.go's
// libraryRefusal* constants set (pkg/knowledge/audit.go's Mutation reason
// tokens, applied to this door too): "the write was refused" alone does not
// tell an operator whether to fix a client, chase a second writer, or look at
// a stuck lock.
const (
	recordRefusalTypeUnknown       = "unknown_record_type"
	recordRefusalPropertyUnknown   = "unknown_property"
	recordRefusalDerivedProperty   = "derived_property"
	recordRefusalRelationProperty  = "relation_property"
	recordRefusalInvalidValue      = "invalid_value"
	recordRefusalVersionMissing    = "expect_version_missing"
	recordRefusalVersionConflict   = "version_conflict"
	recordRefusalLockTimeout       = "lock_timeout"
	recordRefusalWriteFailed       = "write_failed"
	recordRefusalAmbiguousLocation = "ambiguous_collection"
	recordRefusalNotFound          = "record_not_found"
)

// recordWriteRefusal pairs the HTTP outcome with the audit reason, so every
// return path can carry both without the caller re-deriving one from the
// other.
type recordWriteRefusal struct {
	status  int
	reason  string
	message string
}

func (a *restAPI) handleKnowledgeRecordWrite(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var req gen.RecordWriteRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "RecordWriteRequest", &req, validateEnabled) {
		return
	}
	typeName := strings.TrimSpace(req.Type)
	if typeName == "" {
		jsonErr(w, http.StatusBadRequest, "type is required")
		return
	}
	if len(req.Properties) == 0 {
		jsonErr(w, http.StatusBadRequest, "properties must not be empty")
		return
	}

	if req.Id != nil && strings.TrimSpace(*req.Id) != "" {
		a.handleRecordUpdate(w, r, workspaceID, typeName, strings.TrimSpace(*req.Id), req)
		return
	}
	a.handleRecordCreate(w, r, workspaceID, typeName, req)
}

// handleRecordUpdate is CW-7's compare-and-swap door for an EXISTING record.
//
// The lock, the version compare and the splice happen inside ONE call to
// knowledge.EditNote — never a separate check followed by a separate write —
// so a write landing between them cannot produce a silent lost update behind
// a response that said 200 (the exact class of bug Step 0 closed on the
// Library door).
func (a *restAPI) handleRecordUpdate(w http.ResponseWriter, r *http.Request, workspaceID, typeName, id string, req gen.RecordWriteRequest) {
	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid record id")
		return
	}

	found, ok, err := findVaultRecordByID(a.homePath, workspaceID, id)
	if err != nil {
		logger.ErrorCF("rest", "knowledge: search for record by id",
			map[string]any{"workspace_id": workspaceID, "id": id, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		a.logRecordWriteRefused(r, workspaceID, "", "", id, recordRefusalNotFound)
		jsonErr(w, http.StatusNotFound, "record not found")
		return
	}
	if found.schema.Type != typeName {
		a.logRecordWriteRefused(r, workspaceID, found.scoped.Name, found.relPath, id, recordRefusalTypeUnknown)
		jsonErr(w, http.StatusBadRequest,
			fmt.Sprintf("record %q is a %q, not a %q", id, found.schema.Type, typeName))
		return
	}

	// FR-106/EMB-085: a version token is REQUIRED on every update, and an
	// absent or empty one is a 400, never a silent bypass — it is a
	// different failure from a STALE one (409): the caller never told us
	// which version it believed it was replacing at all.
	if req.VersionToken == nil || strings.TrimSpace(*req.VersionToken) == "" {
		a.logRecordWriteRefused(r, workspaceID, found.scoped.Name, found.relPath, id, recordRefusalVersionMissing)
		jsonErr(w, http.StatusBadRequest, "version_token is required when id is present")
		return
	}
	expectVersion := strings.TrimSpace(*req.VersionToken)

	edits, refusal := buildRecordPropertyEdits(found.schema, req.Properties)
	if refusal != nil {
		a.logRecordWriteRefused(r, workspaceID, found.scoped.Name, found.relPath, id, refusal.reason)
		jsonErr(w, refusal.status, refusal.message)
		return
	}

	lockDir, lockErr := knowledge.LockDirFor(a.homePath, found.collection.Root())
	if lockErr != nil {
		logger.ErrorCF("rest", "knowledge: resolve record write lock",
			map[string]any{"workspace_id": workspaceID, "path": found.relPath, "error": lockErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	res, err := knowledge.EditNote(knowledge.OSLinkFS(), found.collection, knowledge.EditNoteRequest{
		RelPath:       found.relPath,
		Edits:         edits,
		ExpectVersion: expectVersion,
		Now:           time.Now(),
		Actor:         knowledge.AuthorActor{WorkspaceID: workspaceID},
		Lock:          knowledge.NoteLockConfig{LockDir: lockDir},
	})
	if err != nil {
		a.finishRecordWriteError(w, r, workspaceID, found.scoped.Name, found.relPath, id, err)
		return
	}

	a.logRecordWriteAudit(r, workspaceID, found.scoped.Name, found.relPath, id)

	updatedData, rerr := os.ReadFile(filepath.Join(found.collection.Root(), filepath.FromSlash(found.relPath)))
	if rerr != nil {
		logger.ErrorCF("rest", "knowledge: re-read record after write",
			map[string]any{"workspace_id": workspaceID, "path": found.relPath, "error": rerr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	rec := records.ParseRecord(found.relPath, updatedData)
	jsonOK(w, buildVaultRecordWire(found.schema, rec, found.relPath, res.Version))
}

// handleRecordCreate is CW-7's create door: `id` absent, `path` required, the
// identifier server-minted (FR-036) so two concurrent creators cannot choose
// the same one.
//
// There is no collection selector on this path (RecordWriteRequest carries
// none): a workspace with exactly one knowledge base in scope creates there;
// one with several or none is an unambiguous refusal rather than a guess —
// the same rule knowledge.Scope.Select("") applies to an unqualified
// "collection" argument elsewhere in this codebase.
func (a *restAPI) handleRecordCreate(w http.ResponseWriter, r *http.Request, workspaceID, typeName string, req gen.RecordWriteRequest) {
	if req.Path == nil || strings.TrimSpace(*req.Path) == "" {
		jsonErr(w, http.StatusBadRequest, "path is required when id is absent")
		return
	}
	relPath := strings.TrimSpace(*req.Path)

	scope := knowledge.ResolveScope(a.homePath, workspaceID)
	sc, ok := scope.Select("")
	if !ok {
		a.logRecordWriteRefused(r, workspaceID, "", relPath, "", recordRefusalAmbiguousLocation)
		if n := len(scope.Collections()); n == 0 {
			jsonErr(w, http.StatusNotFound, "no knowledge base is in scope for this workspace")
		} else {
			jsonErr(w, http.StatusBadRequest,
				"this workspace has more than one knowledge base in scope; record creation needs an unambiguous one")
		}
		return
	}

	set, _, err := records.LoadSchemas(sc.Root)
	if err != nil {
		logger.ErrorCF("rest", "knowledge: load record schemas",
			map[string]any{"workspace_id": workspaceID, "collection": sc.Name, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	schema, ok := set.Get(typeName)
	if !ok {
		a.logRecordWriteRefused(r, workspaceID, sc.Name, relPath, "", recordRefusalTypeUnknown)
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf("record type %q is not declared in this knowledge base", typeName))
		return
	}

	edits, refusal := buildRecordPropertyEdits(schema, req.Properties)
	if refusal != nil {
		a.logRecordWriteRefused(r, workspaceID, sc.Name, relPath, "", refusal.reason)
		jsonErr(w, refusal.status, refusal.message)
		return
	}

	col, err := knowledge.OpenCollection(sc.Root)
	if err != nil {
		logger.ErrorCF("rest", "knowledge: open collection for create",
			map[string]any{"workspace_id": workspaceID, "collection": sc.Name, "error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	lockDir, lockErr := knowledge.LockDirFor(a.homePath, col.Root())
	if lockErr != nil {
		logger.ErrorCF("rest", "knowledge: resolve record write lock",
			map[string]any{"workspace_id": workspaceID, "path": relPath, "error": lockErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	id, minted, mintErr := allocateRecordID(a.homePath, col, schema)
	if mintErr != nil {
		logger.ErrorCF("rest", "knowledge: mint record identifier",
			map[string]any{"workspace_id": workspaceID, "type": typeName, "error": mintErr.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	_ = minted

	// Assemble frontmatter purely in memory: `type` and `id` first, then
	// every requested property, through the SAME NoteEdit constructors (and
	// therefore the SAME splice+validation code) an update uses — the two
	// doors can never disagree about what a valid write looks like.
	content := []byte{}
	content, err = knowledge.SetProperty(records.RecordTypeKey, typeName)(content)
	if err != nil {
		logger.ErrorCF("rest", "knowledge: seed record type", map[string]any{"error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	content, err = knowledge.SetProperty(records.RecordIDKey, id)(content)
	if err != nil {
		logger.ErrorCF("rest", "knowledge: seed record id", map[string]any{"error": err.Error()})
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	for _, edit := range edits {
		content, err = edit(content)
		if err != nil {
			// Every edit here was already validated against the schema by
			// buildRecordPropertyEdits over the SAME primitives — reaching
			// here means the splice itself failed on the freshly-assembled
			// bytes, not that the value was invalid.
			logger.ErrorCF("rest", "knowledge: assemble record frontmatter", map[string]any{"error": err.Error()})
			jsonErr(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}

	res, err := knowledge.CreateNote(knowledge.OSLinkFS(), col, knowledge.CreateNoteRequest{
		RelPath: relPath,
		Body:    content,
		Now:     time.Now(),
		Actor:   knowledge.AuthorActor{WorkspaceID: workspaceID},
		Lock:    knowledge.NoteLockConfig{LockDir: lockDir},
	})
	if err != nil {
		a.finishRecordWriteError(w, r, workspaceID, sc.Name, relPath, id, err)
		return
	}

	a.logRecordWriteAudit(r, workspaceID, sc.Name, res.RelPath, id)
	rec := records.ParseRecord(res.RelPath, content)
	jsonCreated(w, buildVaultRecordWire(schema, rec, res.RelPath, res.Version))
}

// buildRecordPropertyEdits validates every RecordPropertyValue against sc's
// declaration — through records.ParseValue, the SAME authority
// pkg/knowledge/knowledge_edit_schema.go's knowledgeEditValidatePropertyAgainstSchema
// uses for the agent-facing write door, so the two can never disagree about
// what a valid value looks like — and composes the matching splice edits.
//
// THE TWO SERVER-SIDE REFUSALS ADR-068 REQUIRES ARE HERE, AND THEY RUN FIRST,
// before arity or value conformance is even checked: a property whose
// declared type is "relation" or "person" is refused (FR-045), and a
// property carrying a Formula declaration is refused (FR-046). Both read the
// property's OWN declaration off the resolved schema — never a flag the
// request claims about itself — because the client is not a party this
// endpoint trusts to have respected VaultFindCell.derived/.relation; those
// exist so the UI knows what to OFFER, not so the server can skip checking.
func buildRecordPropertyEdits(sc *records.Schema, props []gen.RecordPropertyValue) ([]knowledge.NoteEdit, *recordWriteRefusal) {
	edits := make([]knowledge.NoteEdit, 0, len(props))
	seen := make(map[string]bool, len(props))
	for _, item := range props {
		name := strings.TrimSpace(item.Property)
		if name == "" {
			return nil, &recordWriteRefusal{http.StatusBadRequest, recordRefusalInvalidValue, "a property entry must name a property"}
		}
		if seen[name] {
			return nil, &recordWriteRefusal{http.StatusBadRequest, recordRefusalInvalidValue,
				fmt.Sprintf("property %q is named more than once in the same write", name)}
		}
		seen[name] = true

		prop, ok := sc.Property(name)
		if !ok {
			return nil, &recordWriteRefusal{http.StatusBadRequest, recordRefusalPropertyUnknown,
				fmt.Sprintf("%s declares no property %q; declared properties are %s", sc.Type, name, strings.Join(sc.PropertyNames(), ", "))}
		}
		// FR-046: derived values are never written. Checked before the
		// relation check and before arity — a formula property has no
		// meaningful "arity to satisfy" refusal, so this is the single
		// clearest reason to report first.
		if prop.Formula != "" {
			return nil, &recordWriteRefusal{http.StatusBadRequest, recordRefusalDerivedProperty,
				fmt.Sprintf("%s.%s is a derived value, computed rather than stored; it cannot be written", sc.Type, name)}
		}
		// FR-045: relation and person properties are not writable here.
		if prop.Type == records.TypeRelation || prop.Type == records.TypePerson {
			return nil, &recordWriteRefusal{http.StatusBadRequest, recordRefusalRelationProperty,
				fmt.Sprintf("%s.%s is a %s property; relations and person properties are not writable through this request — use RelationWriteRequest", sc.Type, name, prop.Type)}
		}

		if len(item.Values) == 0 {
			// D3.2/EMB-085: an empty values array CLEARS the property.
			edits = append(edits, knowledge.RemoveProperty(name))
			continue
		}

		values := make([]string, 0, len(item.Values))
		for i, rv := range item.Values {
			text, ok := recordValueText(rv, prop.Type)
			if !ok {
				return nil, &recordWriteRefusal{http.StatusBadRequest, recordRefusalInvalidValue,
					fmt.Sprintf("%s.%s[%d] does not carry a %s value", sc.Type, name, i, prop.Type)}
			}
			values = append(values, text)
		}
		if !prop.Many && len(values) != 1 {
			return nil, &recordWriteRefusal{http.StatusBadRequest, recordRefusalInvalidValue,
				fmt.Sprintf("%s.%s holds one value; got %d — send a single value, or declare many: true", sc.Type, name, len(values))}
		}
		for _, v := range values {
			node := records.Node{Kind: records.KindScalar, Text: v}
			if _, verr := records.ParseValue(prop, node); verr != nil {
				msg := fmt.Sprintf("%s.%s holds %q, which is not %s", sc.Type, name, v, verr.Expected)
				if len(verr.Permitted) > 0 {
					msg += "; permitted values are " + strings.Join(verr.Permitted, ", ")
				}
				return nil, &recordWriteRefusal{http.StatusBadRequest, recordRefusalInvalidValue, msg}
			}
		}
		if prop.Many {
			edits = append(edits, knowledge.SetPropertyList(name, values))
		} else {
			edits = append(edits, knowledge.SetPropertyScalarChecked(name, values[0]))
		}
	}
	if len(edits) == 0 {
		return nil, &recordWriteRefusal{http.StatusBadRequest, recordRefusalInvalidValue, "properties must not be empty"}
	}
	return edits, nil
}

// recordValueText extracts the plain scalar text SetProperty/ParseValue need
// from a wire RecordValue, per the SCHEMA's declared type — not the value's
// own optional `type` field, which the write-request contract explicitly
// makes advisory ("the schema is the authority").
func recordValueText(rv gen.RecordValue, propType records.PropertyType) (string, bool) {
	switch propType {
	case records.TypeText:
		if rv.Text != nil {
			return *rv.Text, true
		}
	case records.TypeEnum:
		if rv.Enum != nil {
			return *rv.Enum, true
		}
	case records.TypeDate:
		if rv.Date != nil {
			return *rv.Date, true
		}
	case records.TypeInteger:
		if rv.Integer != nil {
			return *rv.Integer, true
		}
	case records.TypeDecimal:
		if rv.Decimal != nil {
			return *rv.Decimal, true
		}
	case records.TypeCheckbox:
		if rv.Checkbox != nil {
			if *rv.Checkbox {
				return "true", true
			}
			return "false", true
		}
	}
	return "", false
}

// finishRecordWriteError resolves the outcome of an EditNote/CreateNote call
// and writes the matching HTTP response — mirroring
// rest_library.go's handleLibraryWriteLockErr for the Library door, over the
// KnowledgeConflictError body this door's ConflictError already produces.
func (a *restAPI) finishRecordWriteError(w http.ResponseWriter, r *http.Request, workspaceID, collection, relPath, id string, err error) {
	var conflict *knowledge.ConflictError
	if errors.As(err, &conflict) {
		a.logRecordWriteRefused(r, workspaceID, collection, relPath, id, recordRefusalVersionConflict)
		writeJSON(w, http.StatusConflict, conflict.Wire())
		return
	}
	var timeout *knowledge.LockTimeoutError
	if errors.As(err, &timeout) {
		a.logRecordWriteRefused(r, workspaceID, collection, relPath, id, recordRefusalLockTimeout)
		jsonErr(w, http.StatusServiceUnavailable,
			"timed out waiting for this file's write lock — another write is in progress; try again")
		return
	}
	logger.ErrorCF("rest", "knowledge: record write failed",
		map[string]any{"workspace_id": workspaceID, "path": relPath, "error": err.Error()})
	a.logRecordWriteRefused(r, workspaceID, collection, relPath, id, recordRefusalWriteFailed)
	jsonErr(w, http.StatusInternalServerError, "internal server error")
}

// ---------------------------------------------------------------------------
// Identity minting (ADR-068 D7.1)
// ---------------------------------------------------------------------------

// allocateRecordID mints the next identifier for sc, as "<prefix>-NNNN"
// (four digits minimum, widening rather than wrapping past 9999 — D7's own
// worked example is "CO-0142"). It is a monotonic counter persisted at
// <vault>/.omnipus-vault/records/<type>.seq — the exact location schema.go's
// own comment on the schema directory already reserves for "D7.1's `.seq`
// allocator state" — advanced under the SAME per-collection write lock every
// note write takes (knowledge.WithNoteWriteLock), so two concurrent creators
// of one type can never be handed the same next value.
//
// A schema with no declared identity_prefix mints a bare zero-padded number
// with no prefix — every RecordType.identity_prefix is optional on the wire,
// and a vault that never declares one still needs create to produce SOME
// stable, unique id.
//
// minted echoes the raw numeric sequence value, for a caller that wants it;
// callers here discard it, but it made the collision-avoidance loop's retry
// condition (== the same next value) simpler to state.
func allocateRecordID(home string, col *knowledge.Collection, sc *records.Schema) (id string, minted int64, err error) {
	lockDir, lerr := knowledge.LockDirFor(home, col.Root())
	if lerr != nil {
		return "", 0, fmt.Errorf("resolve identity allocator lock: %w", lerr)
	}
	cfg := knowledge.NoteLockConfig{CollectionRoot: col.Root(), LockDir: lockDir}
	seqPath := filepath.Join(col.Root(), records.VaultMarkerDirName, records.RecordsDirName, sc.Type+".seq")
	lockKey := records.VaultMarkerDirName + "/" + records.RecordsDirName + "/" + sc.Type + ".seq"

	lockErr := knowledge.WithNoteWriteLock(cfg, lockKey, func() error {
		next, rerr := nextSequenceValue(seqPath)
		if rerr != nil {
			return rerr
		}
		// A bound, real collision check: the counter is the fast path, but an
		// operator can hand-write a note carrying an `id:` the counter has
		// not reached yet (imported data, a manually restored trash copy —
		// FR-038a's own scenario). Advancing past a live collision, rather
		// than minting a duplicate, is what makes identity "unique within
		// its type" (D7) an invariant this allocator actually holds, not
		// just usually holds.
		for attempts := 0; attempts < 10000; attempts++ {
			candidate := identityFor(sc, next)
			live, exists, cerr := liveRecordExists(col, sc, candidate)
			if cerr != nil {
				return cerr
			}
			if !exists {
				if werr := writeSequenceValue(seqPath, next); werr != nil {
					return werr
				}
				id, minted = candidate, next
				return nil
			}
			_ = live
			next++
		}
		return fmt.Errorf("could not mint a unique identifier for record type %q after 10000 attempts", sc.Type)
	})
	if lockErr != nil {
		return "", 0, lockErr
	}
	return id, minted, nil
}

// identityFor renders D7's "CO-0142" shape, four digits minimum.
func identityFor(sc *records.Schema, n int64) string {
	if sc.Identity.Prefix == "" {
		return fmt.Sprintf("%04d", n)
	}
	return fmt.Sprintf("%s-%04d", sc.Identity.Prefix, n)
}

// nextSequenceValue reads the persisted counter and returns the NEXT value to
// try. A missing file starts the sequence at 1; a file whose content does not
// parse as a non-negative integer is a genuine fault (a corrupted counter can
// silently reuse an identifier already given out) and is reported, never
// silently reset to zero.
func nextSequenceValue(seqPath string) (int64, error) {
	data, err := os.ReadFile(seqPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 1, nil
		}
		return 0, fmt.Errorf("read identity sequence %q: %w", seqPath, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return 1, nil
	}
	n, perr := strconv.ParseInt(text, 10, 64)
	if perr != nil || n < 0 {
		return 0, fmt.Errorf("identity sequence %q holds %q, which is not a non-negative integer", seqPath, text)
	}
	return n + 1, nil
}

// writeSequenceValue persists the counter atomically.
func writeSequenceValue(seqPath string, n int64) error {
	if err := os.MkdirAll(filepath.Dir(seqPath), 0o700); err != nil {
		return fmt.Errorf("create identity sequence directory for %q: %w", seqPath, err)
	}
	if err := fileutil.WriteFileAtomic(seqPath, []byte(strconv.FormatInt(n, 10)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write identity sequence %q: %w", seqPath, err)
	}
	return nil
}

// liveRecordExists reports whether sc's collection already holds a LIVE
// record (FR-038a: never descending into .omnipus-vault/) with identifier
// candidate — the allocator's own collision guard, walking the same way
// findVaultRecordByID and knowledge_restructure_trash.go's
// findLiveRecordByID do, for the same documented reason (a data-loss-
// preventing check is worth more than the cost of one walk).
func liveRecordExists(col *knowledge.Collection, sc *records.Schema, candidate string) (foundPath string, exists bool, err error) {
	fsys := knowledge.OSLinkFS()
	root, err := knowledge.NewCollectionRoot(fsys, col.Root())
	if err != nil {
		return "", false, err
	}
	wr, err := knowledge.WalkContained(fsys, root)
	if err != nil {
		return "", false, err
	}
	for _, rel := range wr.Files {
		if !knowledge.IsMarkdownPath(rel) {
			continue
		}
		abs := filepath.Join(root.Path(), filepath.FromSlash(rel))
		data, rerr := os.ReadFile(abs)
		if rerr != nil {
			continue
		}
		rec := records.ParseRecord(rel, data)
		if rec.TypeName() != sc.Type {
			continue
		}
		if rec.ID() == candidate {
			return rel, true, nil
		}
	}
	return "", false, nil
}

// ---------------------------------------------------------------------------
// Audit (US-15's principle, applied to this door: refusals are recorded too)
// ---------------------------------------------------------------------------

// logRecordWriteAudit records a record write that landed on disk.
func (a *restAPI) logRecordWriteAudit(r *http.Request, workspaceID, collection, relPath, id string) {
	a.logRecordAuditDecision(r, audit.DecisionAllow, workspaceID, map[string]any{
		"collection": collection, "path": relPath, "id": id,
	})
}

// logRecordWriteRefused records a record write that NEVER REACHED DISK —
// matching rest_library.go's logLibraryWriteRefused precedent: the refused
// population is what answers "who tried to write over a change they had not
// seen", and a single "refused" label collapsing every reason into one loses
// exactly that.
func (a *restAPI) logRecordWriteRefused(r *http.Request, workspaceID, collection, relPath, id, reason string) {
	decision := audit.DecisionDeny
	if reason == recordRefusalWriteFailed {
		decision = audit.DecisionError
	}
	a.logRecordAuditDecision(r, decision, workspaceID, map[string]any{
		"collection": collection, "path": relPath, "id": id, "reason": reason,
	})
}

func (a *restAPI) logRecordAuditDecision(r *http.Request, decision, workspaceID string, details map[string]any) {
	if a.auditor == nil {
		return
	}
	details["actor"] = a.callerIdentity(r).Username
	details["workspace_id"] = workspaceID
	if err := a.auditor.Log(&audit.Entry{
		Event:    "knowledge.record.write",
		Decision: decision,
		Details:  details,
	}); err != nil {
		logger.WarnCF("rest", "knowledge: record audit write failed",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
	}
}
