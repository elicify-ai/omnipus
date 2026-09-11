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
	"sync"
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
// itself: schema loading is records.LoadSchemas, and the write mechanics are
// pkg/knowledge's EditNote/CreateNote — the lock and the splice in both, the
// VERSION COMPARE-AND-SWAP in EditNote, the O_EXCL create-refusal in
// CreateNote (a create has no prior version to compare, so it is not a CAS
// and this file must not describe it as one), and the atomic write plus the
// audit-on-refusal shape in both. The conflict body is
// pkg/knowledge/version.go's *ConflictError.Wire() — the SAME
// KnowledgeConflictError type and the SAME version-token scheme
// VaultFindRow.version_token uses (ADR-083 EMB-086), never a second one.
//
// THE WRITE DOOR (CW-7) enforces, SERVER-SIDE, the guards a client cannot be
// trusted to police itself. Three of them refuse a property outright — one
// whose declared type is "relation" or "person" (FR-045), one carrying a
// Formula declaration (FR-046), and one that is LIST-VALUED where the write
// would shrink the list (ADR-083 §4.6, review C1) — and a fourth refuses the
// two frontmatter keys a record's identity is made of. Every one reads the
// SCHEMA's own declaration, never anything the request claims about itself.
//
// AND WHO IT SAYS DID IT. Every path through this file resolves its actor
// BEFORE it touches a file (§4.2b, founder ruling N4) and records that actor
// on both the applied and the refused population. See resolveRecordWriteActor.
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
//
// EVERY LOAD FAILURE REACHES THE CALLER (ADR-083 review H2/F9). It used to
// reach only the operator's log, and the consequence was that a vault whose
// schema files were broken answered `{types: [], problems: []}` — byte for
// byte what a healthy vault declaring no record types returns. Those two are
// different facts with the same remedy-shaped hole between them: in the first,
// every record editor in every view silently declines to appear and nothing on
// screen says why. RecordProblemCode now carries a faithful code for all nine
// SchemaRejectionCode values and one more for a directory that could not be
// read at all, so there is no longer any load failure this endpoint has to
// drop.
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
			// Reported, not merely logged: this collection contributed NO
			// types, and a caller that cannot tell that from "declares none"
			// has no way to know half its vault is missing from the answer.
			// The collection NAME, not err.Error(), because the loader's
			// message embeds an absolute host path.
			out.Problems = append(out.Problems, gen.RecordProblem{
				Code:    gen.SchemaLoadFailed,
				Reason:  fmt.Sprintf("the schema directory of knowledge base %q could not be read, so it contributed no record types to this answer", col.Name),
				Records: []string{},
			})
			continue
		}
		for _, typeName := range set.Types() {
			if seen[typeName] {
				continue
			}
			seen[typeName] = true
			sc, ok := set.Get(typeName)
			if !ok {
				// Impossible: typeName came out of set.Types() a line ago, so
				// the set must hold it. Logged rather than skipped in silence
				// (ADR-083 review L1) because an impossible state that is
				// merely `continue`d produces a SHORTER type list with no
				// evidence anywhere that anything went wrong — the reader
				// would be hunting a missing schema file that is fine.
				logger.ErrorCF("rest", "knowledge: schema set lists a type it cannot return",
					map[string]any{"workspace_id": workspaceID, "collection": col.Name, "type": typeName})
				continue
			}
			out.Types = append(out.Types, recordTypeWire(sc))
		}
		if report != nil {
			for _, rej := range report.Rejections {
				out.Problems = append(out.Problems, schemaRejectionProblem(rej))
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

// schemaRejectionProblem maps one refused schema file onto its RecordProblem.
//
// TOTAL, with no "and the rest are dropped" branch — that branch is what
// ADR-083 review H2/F9 found. Seven of the nine SchemaRejectionCode values
// used to have no faithful RecordProblemCode and were logged instead, and the
// defending argument ("reporting them under a code that means something else
// would be a worse answer than a log line") was sound about its two options
// and missed the third: add the codes. They are added, one for one, so this
// switch is exhaustive by construction.
//
// TestSchemaRejectionProblem_MapsEveryRejectionCode pins it against
// records.SchemaRejectionCodes, so a tenth rejection code fails a test rather
// than falling into the default arm and vanishing from an operator's screen.
func schemaRejectionProblem(rej records.SchemaRejection) gen.RecordProblem {
	var code gen.RecordProblemCode
	switch rej.Code {
	case records.RejectMissingVersion:
		code = gen.MissingSchemaVersion
	case records.RejectDuplicateType:
		code = gen.DuplicateTypeDeclaration
	case records.RejectUnreadable:
		code = gen.SchemaUnreadable
	case records.RejectInvalidYAML:
		code = gen.SchemaInvalidYaml
	case records.RejectUnsupportedVersion:
		code = gen.SchemaUnsupportedVersion
	case records.RejectMissingType:
		code = gen.SchemaMissingType
	case records.RejectNoProperties:
		code = gen.SchemaNoProperties
	case records.RejectBadProperty:
		code = gen.SchemaBadProperty
	case records.RejectUnknownKey:
		code = gen.SchemaUnknownKey
	default:
		// A rejection code this build does not know. Reported under the
		// generic code WITH its real reason rather than dropped: an operator
		// facing a broken schema file is better served by "something in this
		// file was refused, here is the loader's own words" than by silence.
		// The pinning test exists so this arm is never reached in a shipped
		// build.
		code = gen.SchemaLoadFailed
	}
	p := gen.RecordProblem{Code: code, Reason: rej.Reason, Records: []string{}}
	if len(rej.Paths) > 0 {
		paths := append([]string(nil), rej.Paths...)
		p.Paths = &paths
	}
	return p
}

// ---------------------------------------------------------------------------
// GET .../knowledge/records/{id}
// ---------------------------------------------------------------------------

// handleKnowledgeRecordGet answers VaultRecord for one record id, searching
// every knowledge base in the caller's workspace scope.
func (a *restAPI) handleKnowledgeRecordGet(w http.ResponseWriter, r *http.Request, workspaceID, id string) {
	if err := validateEntityID(id); err != nil {
		// The REASON travels with the refusal (ADR-083 review L3). "invalid
		// record id" alone tells a client-author nothing they did not already
		// know; validateEntityID knows exactly which rule was broken, and
		// discarding it costs a reader an hour for no gain.
		jsonErr(w, http.StatusBadRequest, "invalid record id: "+err.Error())
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
		t := records.WireRecordPropertyValueType(prop.Type)
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
	rv := gen.RecordValue{Type: records.WireRecordValueType(v.Type)}
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
//
// "COULD NOT WALK" IS NOT "WALKED AND FOUND NOTHING" (ADR-083 review C3).
// Three I/O failures used to be `continue`d in silence and collapse into
// found=false, which the update door then rendered as 404 "record not found"
// AND as an audit entry reading `reason=record_not_found`. That is worse than
// an omission: the one artefact designed to answer "why was this refused"
// asserted something false. A record whose file lost read permission still
// exists and still renders in every view, and nothing anywhere named the
// permission error. Every one of those failures is now an error, so the door
// answers 500 `write_failed` and the operator gets the real cause.
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
			return foundVaultRecord{}, false, fmt.Errorf("open collection %q: %w", sc.Name, err)
		}
		root, err := knowledge.NewCollectionRoot(fsys, col.Root())
		if err != nil {
			return foundVaultRecord{}, false, fmt.Errorf("resolve collection root %q: %w", sc.Name, err)
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
				return foundVaultRecord{}, false, fmt.Errorf("read %q in %q: %w", rel, sc.Name, rerr)
			}
			rec := records.ParseRecord(rel, data)
			if rec.ID() != id {
				continue
			}
			schema, ok := set.Get(rec.TypeName())
			if !ok {
				// FR-005, and a genuine "no match": an id on a note whose
				// declared type this vault does not describe is not a record.
				// Nothing failed, so the walk continues.
				continue
			}
			// A VERSION READ THAT FAILS IS A FAULT, NOT AN ABSENT TOKEN
			// (ADR-083 review M5). Discarding this error made a server-side
			// read failure indistinguishable from "the note does not exist",
			// and the empty token then propagated all the way to the client
			// as an omitted version_token — whereupon any write was refused
			// with "version_token is required when id is present", a message
			// about the CLIENT'S request that describes OUR read failure.
			nv, verr := knowledge.ReadNoteVersion(col, rel)
			if verr != nil {
				return foundVaultRecord{}, false, fmt.Errorf("read version of %q in %q: %w", rel, sc.Name, verr)
			}
			// TokenIfPresent, not nv.Token: see its doc comment for why the
			// Exists check must not be skippable here. A note that vanished
			// between the walk and this read legitimately has no token.
			token, _ := nv.TokenIfPresent()
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
	recordRefusalListProperty      = "list_property"
	recordRefusalIdentityProperty  = "identity_property"
	recordRefusalInvalidValue      = "invalid_value"
	recordRefusalInvalidRequest    = "invalid_request"
	recordRefusalVersionMissing    = "expect_version_missing"
	recordRefusalVersionMalformed  = "expect_version_malformed"
	recordRefusalVersionConflict   = "version_conflict"
	recordRefusalLockTimeout       = "lock_timeout"
	recordRefusalWriteFailed       = "write_failed"
	recordRefusalPathExists        = "path_exists"
	recordRefusalUnattributable    = "unattributable_actor"
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

// ---------------------------------------------------------------------------
// N4 / §4.2b — who the audit record names
// ---------------------------------------------------------------------------

const (
	// recordActorUserPrefix is the half N4's greppability obligation rests on.
	//
	// "`anonymous` entries must be greppable as a class. The actor is the
	// literal token `anonymous`, never `user:anonymous`, never an empty
	// string, and never a placeholder id — so grep on the audit file
	// separates the two populations exactly, with no false positives from a
	// user whose id happens to be 'anonymous' (impossible: every attributable
	// actor carries the `user:` prefix)."
	//
	// That impossibility IS the prefix. Without it there is no mechanical
	// rule at all separating identified writes from unidentified ones, and
	// the two populations an operator most needs to tell apart become one.
	recordActorUserPrefix = "user:"

	// recordActorAnonymous is N4's literal token for a write under
	// gateway.dev_mode_bypass: lower-case, no prefix, no colon, exactly this.
	//
	// The founder accepted, without softening, that such an entry CANNOT
	// ANSWER "WHO". It records that a human-shaped write happened, when, to
	// which file, and what changed — and nothing about the person. It is not
	// equivalent to a `user:<id>` entry and must never be read as one.
	recordActorAnonymous = "anonymous"
)

// resolveRecordWriteActor settles who this write is attributed to, or refuses
// it (§4.2b, founder ruling N4).
//
// WHY IT RUNS BEFORE ANYTHING TOUCHES A FILE. ADR §4.2's precondition 1 puts
// this check in knowledge.NewWriter — "that is the only place an actor with
// neither an agent nor a user is rejected, and an implementer who reads 'call
// EditNote' and does so directly bypasses it". This handler cannot literally
// route through NewWriter: knowledge.Writer's only mutating method is
// WriteNote, a WHOLE-FILE write, and a record edit is a SPLICE (EMB-085
// requires exactly the splice path, and a whole-file rewrite of a note to
// change one property is the lost-update bug this endpoint exists to close).
// So the precondition is implemented here instead, in the one place this door
// can enforce it, rather than being skipped because the constructor that
// usually carries it does not fit.
//
// Three outcomes, and they are the three §11.1 names as the step-5 exit
// criterion:
//
//	authenticated     "user:<username>" — Username is the only stable key
//	                  config.UserConfig carries (it has no separate id field),
//	                  so it is what `<auth user id>` resolves to on this
//	                  platform. The PREFIX is the part that is load-bearing.
//	dev_mode_bypass   "anonymous", the literal token. N4 overruled the
//	                  architect's 503 here: the write is ALLOWED and recorded.
//	neither           REFUSED, before the file is touched. N4 permits an
//	                  UNATTRIBUTED write, not an UNATTRIBUTABLE one.
func (a *restAPI) resolveRecordWriteActor(r *http.Request) (string, bool) {
	if name := strings.TrimSpace(a.callerIdentity(r).Username); name != "" {
		return recordActorUserPrefix + name, true
	}
	if a.agentLoop.GetConfig().Gateway.DevModeBypass {
		return recordActorAnonymous, true
	}
	return "", false
}

func (a *restAPI) handleKnowledgeRecordWrite(w http.ResponseWriter, r *http.Request, workspaceID string) {
	// THE WRITE DOOR IS RATE-LIMITED LIKE EVERY READ DOOR (review M8/F13).
	// It was the one knowledge endpoint that skipped allowKnowledgeRetrieval,
	// and it is also the most expensive request in the family: each write runs
	// findVaultRecordByID, a full walk reading every markdown file in every
	// in-scope collection. The outer withRateLimit on /api/v1/library/ meant
	// this was never an open door, but "the costliest call is the unmetered
	// one" is not a posture to leave standing.
	if !a.allowKnowledgeRetrieval(w, workspaceID) {
		return
	}

	// The actor is settled FIRST — before the body is even decoded — so that
	// an unattributable caller cannot reach any code that opens a file.
	actor, attributable := a.resolveRecordWriteActor(r)
	if !attributable {
		a.logRecordWriteRefusedAs(r, "", workspaceID, "", "", "", recordRefusalUnattributable)
		jsonErr(w, http.StatusForbidden,
			"a knowledge base cannot be written anonymously: sign in, or run the gateway with dev_mode_bypass for local development")
		return
	}

	var req gen.RecordWriteRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "RecordWriteRequest", &req, validateEnabled) {
		// decodeAndValidate has already written the 400. Audited here because
		// the refused population is what answers "was anything attempted
		// against this vault" (review H4), and a malformed-body flood is
		// exactly the case where that question gets asked.
		a.logRecordWriteRefusedAs(r, actor, workspaceID, "", "", "", recordRefusalInvalidRequest)
		return
	}
	typeName := strings.TrimSpace(req.Type)
	if typeName == "" {
		a.refuseRecordWrite(w, r, actor, workspaceID, "", "", "", &recordWriteRefusal{
			http.StatusBadRequest, recordRefusalInvalidRequest, "type is required"})
		return
	}
	if len(req.Properties) == 0 {
		a.refuseRecordWrite(w, r, actor, workspaceID, "", "", "", &recordWriteRefusal{
			http.StatusBadRequest, recordRefusalInvalidRequest, "properties must not be empty"})
		return
	}

	if req.Id != nil && strings.TrimSpace(*req.Id) != "" {
		a.handleRecordUpdate(w, r, actor, workspaceID, typeName, strings.TrimSpace(*req.Id), req)
		return
	}
	a.handleRecordCreate(w, r, actor, workspaceID, typeName, req)
}

// recordWriteTokenRefusal checks the SHAPE of a supplied version token, which
// is a different question from whether it is current (§4.2a(c)).
//
// THE QUOTED-TOKEN CASE IS THE REASON THIS EXISTS, and the ADR spells out the
// failure it prevents: a client that forgot to strip JSON quotes sends
// `"v1:9f2a…"` with the quotes still attached. Compared as an opaque string
// that simply does not match, so the natural answer is 409 — "this changed
// while you were editing" — about a record nobody touched. The client re-reads,
// gets a fresh token, fails to strip the quotes again, and conflicts forever,
// with every attempt recorded in the audit log as a genuine version_conflict.
// §4.2a(c): "never 409, because otherwise a client that forgot to strip the
// quotes gets a conflict indistinguishable from a real one, forever."
//
// The rule is general, not a quote special-case: this server MINTS exactly two
// token shapes (a `v1:` + 32 hex content hash, and the `v1:absent` sentinel),
// so anything else cannot be a token it ever issued, and therefore cannot be a
// STALE one. 400 names the real fault; 409 blames a writer who does not exist.
func recordWriteTokenRefusal(raw string) *recordWriteRefusal {
	if knowledge.IsWellFormedVersionToken(raw) {
		return nil
	}
	msg := fmt.Sprintf("version_token %q is not a token this server issues", raw)
	if len(raw) >= 2 && strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`) {
		msg += ` — it is still wrapped in JSON quotes; send the token's characters, not its JSON encoding`
	}
	return &recordWriteRefusal{http.StatusBadRequest, recordRefusalVersionMalformed, msg}
}

// handleRecordUpdate is CW-7's compare-and-swap door for an EXISTING record.
//
// The lock, the version compare and the splice happen inside ONE call to
// knowledge.EditNote — never a separate check followed by a separate write —
// so a write landing between them cannot produce a silent lost update behind
// a response that said 200 (the exact class of bug Step 0 closed on the
// Library door).
//
// AND THE RESPONSE IS BUILT FROM THAT SAME CALL'S OWN BYTES. It used to
// re-read the file after the lock was released and pair those bytes with the
// token from inside it — the shape of defect R-1/B1, fixed once on this
// branch already. See EditNoteResult.Content for both things that goes wrong.
func (a *restAPI) handleRecordUpdate(w http.ResponseWriter, r *http.Request, actor, workspaceID, typeName, id string, req gen.RecordWriteRequest) {
	if err := validateEntityID(id); err != nil {
		a.refuseRecordWrite(w, r, actor, workspaceID, "", "", id, &recordWriteRefusal{
			http.StatusBadRequest, recordRefusalInvalidRequest, "invalid record id: " + err.Error()})
		return
	}

	found, ok, err := findVaultRecordByID(a.homePath, workspaceID, id)
	if err != nil {
		logger.ErrorCF("rest", "knowledge: search for record by id",
			map[string]any{"workspace_id": workspaceID, "id": id, "error": err.Error()})
		// write_failed, NOT record_not_found: the walk did not complete, so
		// whether this record exists is unknown (review C3).
		a.refuseRecordWrite(w, r, actor, workspaceID, "", "", id, &recordWriteRefusal{
			http.StatusInternalServerError, recordRefusalWriteFailed, "internal server error"})
		return
	}
	if !ok {
		a.refuseRecordWrite(w, r, actor, workspaceID, "", "", id, &recordWriteRefusal{
			http.StatusNotFound, recordRefusalNotFound, "record not found"})
		return
	}
	if found.schema.Type != typeName {
		a.refuseRecordWrite(w, r, actor, workspaceID, found.scoped.Name, found.relPath, id, &recordWriteRefusal{
			http.StatusBadRequest, recordRefusalTypeUnknown,
			fmt.Sprintf("record %q is a %q, not a %q", id, found.schema.Type, typeName)})
		return
	}

	// FR-106/EMB-085: a version token is REQUIRED on every update, and an
	// absent or empty one is a 400, never a silent bypass — it is a
	// different failure from a STALE one (409): the caller never told us
	// which version it believed it was replacing at all.
	if req.VersionToken == nil || strings.TrimSpace(*req.VersionToken) == "" {
		a.refuseRecordWrite(w, r, actor, workspaceID, found.scoped.Name, found.relPath, id, &recordWriteRefusal{
			http.StatusBadRequest, recordRefusalVersionMissing, "version_token is required when id is present"})
		return
	}
	expectVersion := strings.TrimSpace(*req.VersionToken)
	if refusal := recordWriteTokenRefusal(expectVersion); refusal != nil {
		a.refuseRecordWrite(w, r, actor, workspaceID, found.scoped.Name, found.relPath, id, refusal)
		return
	}

	current := records.ParseRecord(found.relPath, found.frontmatter)
	edits, refusal := buildRecordPropertyEdits(found.schema, req.Properties, &current)
	if refusal != nil {
		a.refuseRecordWrite(w, r, actor, workspaceID, found.scoped.Name, found.relPath, id, refusal)
		return
	}

	lockDir, lockErr := knowledge.LockDirFor(a.homePath, found.collection.Root())
	if lockErr != nil {
		logger.ErrorCF("rest", "knowledge: resolve record write lock",
			map[string]any{"workspace_id": workspaceID, "path": found.relPath, "error": lockErr.Error()})
		a.refuseRecordWrite(w, r, actor, workspaceID, found.scoped.Name, found.relPath, id, &recordWriteRefusal{
			http.StatusInternalServerError, recordRefusalWriteFailed, "internal server error"})
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
		a.finishRecordWriteError(w, r, actor, workspaceID, found.scoped.Name, found.relPath, id, err)
		return
	}

	a.logRecordWriteAudit(r, actor, workspaceID, found.scoped.Name, found.relPath, id)

	// res.Content and res.Version describe the SAME bytes, both captured
	// inside the write lock. No re-read, so there is no window for another
	// writer's values to be paired with our token, and no post-commit read
	// that can turn a landed write into a 500 (review F8/H5).
	rec := records.ParseRecord(found.relPath, res.Content)
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
//
// THE IDENTIFIER IS MINTED BEFORE THE NOTE IS WRITTEN, and that ordering has
// a consequence worth stating rather than discovering (review G1). Minting
// persists the `.seq` counter to disk and commits; if the CreateNote that
// follows then fails — a path already taken, a lock timeout — the number has
// been consumed and is never returned to the pool, so the next successful
// create for that type SKIPS one. That is accepted, not a bug: D7's invariant
// is "unique within its type", which promises no REUSE, never no gaps. The
// alternative — mint after the write — would need an identifier to write in
// the first place, and holding the counter open across the file write would
// make a crash between them reuse an id that a note on disk already carries,
// which is the failure that actually matters.
func (a *restAPI) handleRecordCreate(w http.ResponseWriter, r *http.Request, actor, workspaceID, typeName string, req gen.RecordWriteRequest) {
	if req.Path == nil || strings.TrimSpace(*req.Path) == "" {
		a.refuseRecordWrite(w, r, actor, workspaceID, "", "", "", &recordWriteRefusal{
			http.StatusBadRequest, recordRefusalInvalidRequest, "path is required when id is absent"})
		return
	}
	relPath := strings.TrimSpace(*req.Path)

	scope := knowledge.ResolveScope(a.homePath, workspaceID)
	sc, ok := scope.Select("")
	if !ok {
		refusal := &recordWriteRefusal{http.StatusBadRequest, recordRefusalAmbiguousLocation,
			"this workspace has more than one knowledge base in scope; record creation needs an unambiguous one"}
		if n := len(scope.Collections()); n == 0 {
			refusal = &recordWriteRefusal{http.StatusNotFound, recordRefusalAmbiguousLocation,
				"no knowledge base is in scope for this workspace"}
		}
		a.refuseRecordWrite(w, r, actor, workspaceID, "", relPath, "", refusal)
		return
	}

	set, _, err := records.LoadSchemas(sc.Root)
	if err != nil {
		logger.ErrorCF("rest", "knowledge: load record schemas",
			map[string]any{"workspace_id": workspaceID, "collection": sc.Name, "error": err.Error()})
		a.refuseRecordWrite(w, r, actor, workspaceID, sc.Name, relPath, "", &recordWriteRefusal{
			http.StatusInternalServerError, recordRefusalWriteFailed, "internal server error"})
		return
	}
	schema, ok := set.Get(typeName)
	if !ok {
		a.refuseRecordWrite(w, r, actor, workspaceID, sc.Name, relPath, "", &recordWriteRefusal{
			http.StatusBadRequest, recordRefusalTypeUnknown,
			fmt.Sprintf("record type %q is not declared in this knowledge base", typeName)})
		return
	}

	// No current record: a create cannot shrink a list that does not exist
	// yet, so the arity-drop guard has nothing to compare against and is
	// correctly inert here.
	edits, refusal := buildRecordPropertyEdits(schema, req.Properties, nil)
	if refusal != nil {
		a.refuseRecordWrite(w, r, actor, workspaceID, sc.Name, relPath, "", refusal)
		return
	}

	col, err := knowledge.OpenCollection(sc.Root)
	if err != nil {
		logger.ErrorCF("rest", "knowledge: open collection for create",
			map[string]any{"workspace_id": workspaceID, "collection": sc.Name, "error": err.Error()})
		a.refuseRecordWrite(w, r, actor, workspaceID, sc.Name, relPath, "", &recordWriteRefusal{
			http.StatusInternalServerError, recordRefusalWriteFailed, "internal server error"})
		return
	}
	lockDir, lockErr := knowledge.LockDirFor(a.homePath, col.Root())
	if lockErr != nil {
		logger.ErrorCF("rest", "knowledge: resolve record write lock",
			map[string]any{"workspace_id": workspaceID, "path": relPath, "error": lockErr.Error()})
		a.refuseRecordWrite(w, r, actor, workspaceID, sc.Name, relPath, "", &recordWriteRefusal{
			http.StatusInternalServerError, recordRefusalWriteFailed, "internal server error"})
		return
	}

	id, minted, mintErr := allocateRecordID(a.homePath, col, schema)
	if mintErr != nil {
		// Routed through finishRecordWriteError rather than answering a flat
		// 500 (review H3): allocateRecordID takes a REAL write lock, so a
		// contended or stuck lock reaches here as a *LockTimeoutError, and
		// that deserves the 503 plus "another write is in progress; try
		// again" plus reason=lock_timeout that the sibling update path gives.
		// Reporting a retryable refusal as an unexplained server fault tells
		// the caller nothing about the one action that would work.
		logger.ErrorCF("rest", "knowledge: mint record identifier",
			map[string]any{"workspace_id": workspaceID, "type": typeName, "error": mintErr.Error()})
		a.finishRecordWriteError(w, r, actor, workspaceID, sc.Name, relPath, "", mintErr)
		return
	}
	logger.DebugCF("rest", "knowledge: minted record identifier",
		map[string]any{"workspace_id": workspaceID, "type": typeName, "id": id, "sequence": minted})

	// Assemble frontmatter purely in memory: `type` and `id` first, then
	// every requested property, through the SAME NoteEdit constructors (and
	// therefore the SAME splice+validation code) an update uses — the two
	// doors can never disagree about what a valid write looks like.
	content := []byte{}
	content, err = knowledge.SetProperty(records.RecordTypeKey, typeName)(content)
	if err != nil {
		logger.ErrorCF("rest", "knowledge: seed record type", map[string]any{"error": err.Error()})
		a.refuseRecordWrite(w, r, actor, workspaceID, sc.Name, relPath, id, &recordWriteRefusal{
			http.StatusInternalServerError, recordRefusalWriteFailed, "internal server error"})
		return
	}
	content, err = knowledge.SetProperty(records.RecordIDKey, id)(content)
	if err != nil {
		logger.ErrorCF("rest", "knowledge: seed record id", map[string]any{"error": err.Error()})
		a.refuseRecordWrite(w, r, actor, workspaceID, sc.Name, relPath, id, &recordWriteRefusal{
			http.StatusInternalServerError, recordRefusalWriteFailed, "internal server error"})
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
			a.refuseRecordWrite(w, r, actor, workspaceID, sc.Name, relPath, id, &recordWriteRefusal{
				http.StatusInternalServerError, recordRefusalWriteFailed, "internal server error"})
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
		a.finishRecordWriteError(w, r, actor, workspaceID, sc.Name, relPath, id, err)
		return
	}

	a.logRecordWriteAudit(r, actor, workspaceID, sc.Name, res.RelPath, id)
	rec := records.ParseRecord(res.RelPath, content)
	jsonCreated(w, buildVaultRecordWire(schema, rec, res.RelPath, res.Version))
}

// buildRecordPropertyEdits validates every RecordPropertyValue against sc's
// declaration — through records.ParseValue, the SAME authority
// pkg/knowledge/knowledge_edit_schema.go's knowledgeEditValidatePropertyAgainstSchema
// uses for the agent-facing write door, so the two can never disagree about
// what a valid value looks like — and composes the matching splice edits.
//
// `current` is the record as it stands on disk, or nil on a create. It is
// read for exactly one purpose: the arity-drop guard below, which cannot be
// evaluated without knowing what the property already holds.
//
// THE SERVER-SIDE REFUSALS ARE HERE, AND THEY RUN BEFORE arity or value
// conformance is even checked. All of them read the property's OWN
// declaration off the resolved schema — never a flag the request claims about
// itself — because the client is not a party this endpoint trusts to have
// respected VaultFindCell.derived/.relation/.many; those exist so the UI
// knows what to OFFER, not so the server can skip checking.
//
//	FR-046  a property carrying a Formula declaration is refused.
//	FR-045  a "relation" or "person" property is refused.
//	D1/D7   `type` and `id` are refused — see the identity guard.
//	§4.6    a LIST-VALUED property is refused when the write would SHRINK the
//	        list — see the arity-drop guard.
//
// ⚠️ THE FR-046 BRANCH IS UNREACHABLE THROUGH THIS ENDPOINT TODAY, AND MUST
// NOT BE DELETED AS DEAD CODE (review M3/F14). Every *records.Schema that
// reaches this function comes from records.LoadSchemas, which CANNOT produce
// a Formula-bearing property at all: a schema file declaring `formula:` on a
// property is refused at load (schema.go's propertyDeclKeys). The guarantee
// that makes the branch unreachable therefore lives in a DIFFERENT PACKAGE
// from the branch — so a reader working only in this file has every reason to
// think it is live, and a reader who checks has every reason to think it is
// dead. It is neither: it is defence in depth held in reserve, against the
// day a *Schema reaches here from somewhere other than LoadSchemas (a saved
// view's namespace synthesis already constructs Formula-bearing properties,
// one package away). See records.Property.Wire's doc comment for the other
// half of this argument.
func buildRecordPropertyEdits(sc *records.Schema, props []gen.RecordPropertyValue, current *records.Record) ([]knowledge.NoteEdit, *recordWriteRefusal) {
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

		// D1/D7: the identity keys are refused BEFORE the schema is even
		// consulted, because the refusal does not depend on what the schema
		// says (review M8). A schema is free to declare a property literally
		// named `id` — an external system's identifier is a plausible thing to
		// model — and nothing else in the stack forbids it. Writing through it
		// would rename the record; clearing it (an empty values array) would
		// DELETE the record's identifier, making it unreachable through every
		// record door AND invisible to the allocator's collision check, which
		// would then hand the same identifier to a second record. All behind
		// a 200.
		if refusal := recordIdentityKeyRefusal(sc, name); refusal != nil {
			return nil, refusal
		}

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
				fmt.Sprintf("%s.%s is a %s property; relations and person properties are not writable through this request — "+
					"they are modified through RelationWriteRequest's explicit add/remove/replace verbs (a future write door, not yet implemented)",
					sc.Type, name, prop.Type)}
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
		if refusal := listArityDropRefusal(sc, prop, current, len(values)); refusal != nil {
			return nil, refusal
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

// recordIdentityKeyRefusal refuses a write naming `type`, `id` or `omni_id`.
//
// A second, independent copy of the guard knowledge.RemoveProperty enforces at
// the splice, and deliberately so: this one exists to give the CLIENT a
// truthful, specific 400 with a distinct audit reason, where the splice-level
// one exists to protect every OTHER caller of the edit primitives. Neither
// makes the other redundant — a guard that only the HTTP door applies protects
// nothing an agent does, and a guard that only the splice applies surfaces as
// a generic 500.
func recordIdentityKeyRefusal(sc *records.Schema, name string) *recordWriteRefusal {
	switch name {
	case records.RecordTypeKey, records.RecordIDKey, records.RecordIDKeyNamespaced:
		return &recordWriteRefusal{http.StatusBadRequest, recordRefusalIdentityProperty,
			fmt.Sprintf("%s.%s carries the record's identity and cannot be written or cleared through this request "+
				"(ADR-068 D1/D7): a record with no type is not a record, and a record with no id can never be "+
				"found again and lets its identifier be minted to a second record", sc.Type, name)}
	}
	return nil
}

// listArityDropRefusal is §4.6's "a list-valued property gets no editor",
// enforced where a client cannot bypass it (ADR-083 review C1).
//
// THE DATA LOSS IT PREVENTS, concretely. A `many: true` property holding
// `[alpha, beta]` renders on the wire as the single string "alpha, beta" —
// VaultFindCell.value is one rendered string whatever the arity. An editor
// offered on that cell sends back ONE value, and SetPropertyList replaces the
// WHOLE list span: two tags become one tag whose text is "alpha, beta". HTTP
// 200, audit `decision: allow`, and the reader never sees that anything was
// lost. Worse for a `many` ENUM, where the joined text matches no declared
// member, so picking any real option from the dropdown drops the rest.
//
// THE RULE IS "MUST NOT SHRINK", NOT "MUST NOT TOUCH", and that choice is
// deliberate. §4.2c is explicit that the absence of a list editor is "a scope
// decision, not a platform limit ... a scope decision with a way forward,
// rather than a wall" — so refusing every write to every list property would
// build the wall the ADR had just declined to build, and would break a future
// multi-value control that legitimately sends the whole list. What must never
// happen is a write that silently discards values the record already holds. So:
//
//	N == 0   allowed. An empty values array is D3.2's explicit CLEAR, not a
//	         silent collapse; the caller plainly asked for it.
//	N >= K   allowed. The caller sent at least as many values as are stored,
//	         which is a deliberate whole-list write, not a joined string.
//	0 < N < K  REFUSED. The only way to reach this is a client treating the
//	         joined rendering as a single value.
//
// K is counted from the record's CONFORMING values, which is the same
// population a re-read would return — a non-conforming element is already
// absent from what the client was shown, so it cannot be what the client
// meant to preserve.
func listArityDropRefusal(sc *records.Schema, prop *records.Property, current *records.Record, sending int) *recordWriteRefusal {
	if !prop.Many || current == nil || sending == 0 {
		return nil
	}
	held := len(records.ResolveProperty(*current, prop).Values)
	if sending >= held {
		return nil
	}
	return &recordWriteRefusal{http.StatusBadRequest, recordRefusalListProperty,
		fmt.Sprintf("%s.%s is a list holding %d values and this write sends %d, which would discard the rest. "+
			"A list-valued property has no inline editor (ADR-083 §4.6) precisely because its cell renders as one "+
			"joined string: send every value the list should end up with, or an empty values array to clear it",
			sc.Type, prop.Name, held, sending)}
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
//
// IT ENUMERATES THE AUTHOR PACKAGE'S SENTINELS RATHER THAN COLLAPSING THEM
// (review M7/F8). It used to recognise two error shapes and answer 500
// `write_failed` for everything else — including ErrNoteExists, which
// CreateNote returns when its O_EXCL create finds a note already at the path.
// That is the single most ordinary way a create fails, it is entirely the
// caller's to fix, and reporting it as a server fault both misled the caller
// and polluted the decision=error population an operator greps for real
// faults.
func (a *restAPI) finishRecordWriteError(w http.ResponseWriter, r *http.Request, actor, workspaceID, collection, relPath, id string, err error) {
	var conflict *knowledge.ConflictError
	if errors.As(err, &conflict) {
		a.logRecordWriteRefusedAs(r, actor, workspaceID, collection, relPath, id, recordRefusalVersionConflict)
		writeJSON(w, http.StatusConflict, conflict.Wire())
		return
	}
	var timeout *knowledge.LockTimeoutError
	if errors.As(err, &timeout) {
		a.logRecordWriteRefusedAs(r, actor, workspaceID, collection, relPath, id, recordRefusalLockTimeout)
		jsonErr(w, http.StatusServiceUnavailable,
			"timed out waiting for this file's write lock — another write is in progress; try again")
		return
	}
	// 400 rather than 409 for a path collision. 409 on this operation is
	// contractually bound to the typed KnowledgeConflictError body with
	// code=knowledge_version_conflict (contracts/openapi.yaml), and a path
	// collision is not a version conflict — serving a second, differently
	// shaped body under one status is exactly the wire ambiguity Constraint
	// #8 exists to prevent. The caller's remedy is to choose another path,
	// which makes this a request they must change.
	if errors.Is(err, knowledge.ErrNoteExists) {
		a.logRecordWriteRefusedAs(r, actor, workspaceID, collection, relPath, id, recordRefusalPathExists)
		jsonErr(w, http.StatusBadRequest,
			fmt.Sprintf("a note already exists at %q; choose a path that is free", relPath))
		return
	}
	if errors.Is(err, knowledge.ErrNoteNameRefused) || errors.Is(err, knowledge.ErrReservedLocation) {
		a.logRecordWriteRefusedAs(r, actor, workspaceID, collection, relPath, id, recordRefusalInvalidRequest)
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	logger.ErrorCF("rest", "knowledge: record write failed",
		map[string]any{"workspace_id": workspaceID, "path": relPath, "error": err.Error()})
	a.logRecordWriteRefusedAs(r, actor, workspaceID, collection, relPath, id, recordRefusalWriteFailed)
	jsonErr(w, http.StatusInternalServerError, "internal server error")
}

// refuseRecordWrite writes one refusal: the audit entry and the HTTP response,
// from the one value that carries both.
//
// EVERY REFUSAL EXIT ON THIS DOOR GOES THROUGH IT (review H4). Eleven exits
// used to answer the client and record nothing, which contradicted this
// file's own stated contract — "the refused population is what answers who
// tried to write over a change they had not seen". A client hammering the
// door with malformed bodies produced ZERO audit entries, so the one artefact
// an operator consults to answer "was anything attempted against this vault"
// said nothing had been.
func (a *restAPI) refuseRecordWrite(w http.ResponseWriter, r *http.Request, actor, workspaceID, collection, relPath, id string, refusal *recordWriteRefusal) {
	a.logRecordWriteRefusedAs(r, actor, workspaceID, collection, relPath, id, refusal.reason)
	jsonErr(w, refusal.status, refusal.message)
}

// ---------------------------------------------------------------------------
// Identity minting (ADR-068 D7.1)
// ---------------------------------------------------------------------------

// maxIdentityMintAttempts bounds how far the allocator will scan forward past
// live collisions before giving up. Reached only when a vault holds that many
// consecutive hand-written identifiers above the counter.
const maxIdentityMintAttempts = 10000

// allocateRecordID mints the next identifier for sc, as "<prefix>-NNNN"
// (four digits minimum, widening rather than wrapping past 9999 — D7's own
// worked example is "CO-0142"). It is a monotonic counter persisted at
// <vault>/.omnipus-vault/records/<type>.seq — the exact location schema.go's
// own comment on the schema directory already reserves for "D7.1's `.seq`
// allocator state".
//
// WHAT THE LOCK ACTUALLY EXCLUDES, precisely (review, comment-accuracy C1).
// The counter is advanced under knowledge.WithNoteWriteLock — the same
// locking PRIMITIVE every note write uses, but keyed on this record type's own
// `.seq` path (`.omnipus-vault/records/<type>.seq`), a key ordinary note
// writes never pass. WithNoteWriteLock keys a 64-shard striped mutex on
// (collectionRoot, rel), so "the same function" is not "the same lock". Two
// concurrent creators OF THE SAME RECORD TYPE pass that identical key and
// therefore contend for the identical lock, and that — not a collection-wide
// write lock — is what stops them being handed the same next value. It does
// NOT serialise against unrelated note writes in the collection, and it does
// not need to: nothing about writing some other note can change what the next
// free identifier for this type is, except adding a note that already carries
// one, which the collision scan below handles by reading the vault, not by
// excluding the writer.
//
// A schema with no declared identity_prefix mints a bare zero-padded number
// with no prefix — every RecordType.identity_prefix is optional on the wire,
// and a vault that never declares one still needs create to produce SOME
// stable, unique id.
//
// minted echoes the raw numeric sequence value, for a caller that wants it.
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
		// ONE WALK, THEN AN IN-MEMORY SCAN (review I5/F7b).
		//
		// The collision check is real and must stay: an operator can
		// hand-write a note carrying an `id:` the counter has not reached
		// (imported data, a manually restored trash copy — FR-038a's own
		// scenario), and advancing past a live collision rather than minting
		// a duplicate is what makes "unique within its type" (D7) an
		// invariant this allocator actually holds.
		//
		// What changed is its cost. It used to walk and re-read the ENTIRE
		// collection once PER ATTEMPT, up to 10,000 times, with the
		// allocator's lock held throughout: importing 500 notes carrying
		// WD-0001…WD-0500 turned the next create into 500 full vault walks —
		// on a 5,000-note vault, ~2.5 million file reads in one request —
		// while every concurrent create of that type blocked behind it until
		// the lock bound elapsed and they 503'd. The set of live identifiers
		// does not change while this lock is held, so reading it once and
		// probing memory is not merely faster, it is the same answer.
		live, werr := liveRecordIDs(col, sc)
		if werr != nil {
			return werr
		}
		for attempts := 0; attempts < maxIdentityMintAttempts; attempts++ {
			candidate := identityFor(sc, next)
			if collidingPath, taken := live[candidate]; taken {
				// Logged, not discarded (review L2): when the allocator
				// advances past a hand-written id, the path of the note that
				// caused it is the one thing an operator needs to understand
				// why their counter jumped. It used to be thrown away.
				logger.InfoCF("rest", "knowledge: identity already in use, advancing",
					map[string]any{"type": sc.Type, "candidate": candidate, "held_by": collidingPath})
				next++
				continue
			}
			if serr := writeSequenceValue(seqPath, next); serr != nil {
				return serr
			}
			id, minted = candidate, next
			return nil
		}
		return fmt.Errorf("could not mint a unique identifier for record type %q after %d attempts",
			sc.Type, maxIdentityMintAttempts)
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

// liveRecordIDs collects every LIVE identifier sc's collection currently holds
// for sc's type, mapped to the path that holds it (FR-038a: never descending
// into .omnipus-vault/, so a trashed copy can never block an identifier).
//
// ONE walk, returning a set, rather than a per-candidate existence probe: see
// allocateRecordID's own comment for why the probe shape was the defect. The
// path is carried, not just the fact, because a collision an operator has to
// explain is a collision they have to be able to FIND.
//
// An unreadable file is an ERROR, not a skip. A note whose bytes cannot be
// read might hold the very identifier about to be minted, and a collision
// check that silently treats "I could not look" as "it is free" is not a
// collision check — it hands out a duplicate identifier and the D7 invariant
// this function exists to hold is quietly gone.
func liveRecordIDs(col *knowledge.Collection, sc *records.Schema) (map[string]string, error) {
	fsys := knowledge.OSLinkFS()
	root, err := knowledge.NewCollectionRoot(fsys, col.Root())
	if err != nil {
		return nil, err
	}
	wr, err := knowledge.WalkContained(fsys, root)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(wr.Files))
	for _, rel := range wr.Files {
		if !knowledge.IsMarkdownPath(rel) {
			continue
		}
		abs := filepath.Join(root.Path(), filepath.FromSlash(rel))
		data, rerr := os.ReadFile(abs)
		if rerr != nil {
			return nil, fmt.Errorf("read %q while checking identifier collisions: %w", rel, rerr)
		}
		rec := records.ParseRecord(rel, data)
		if rec.TypeName() != sc.Type {
			continue
		}
		if id := rec.ID(); id != "" {
			if _, dup := out[id]; !dup {
				out[id] = rel
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Audit (US-15's principle, applied to this door: refusals are recorded too)
// ---------------------------------------------------------------------------

// recordWriteAuditEvent is the audit event name for this door.
//
// `knowledge.note.write`, NOT a new `knowledge.record.write` (review I3/F4).
// ADR §4.2's precondition 3 decided this name for a concrete reason: it is
// already one of the five registered in pkg/audit's IsValidEventName, so no
// allowlist change is needed and no write trips the warn-once "unknown Event
// value" path. A record IS a note (D1), and this write really does go through
// EditNote, so the name is accurate as well as convenient. Inventing a sixth
// name produced exactly the failure precondition 2 spends a page warning
// about — and nothing caught it, because the two guard tests walk audit's
// Event* CONSTANTS while emitters like this one pass bare string literals.
const recordWriteAuditEvent = "knowledge.note.write"

// logRecordAuditorMissingOnce bounds the nil-auditor ERROR below to one line
// per process.
//
//nolint:gochecknoglobals // a warn-once latch has to outlive the request.
var logRecordAuditorMissingOnce sync.Once

// logRecordWriteAudit records a record write that landed on disk.
func (a *restAPI) logRecordWriteAudit(r *http.Request, actor, workspaceID, collection, relPath, id string) {
	a.logRecordAuditDecision(r, audit.DecisionAllow, actor, workspaceID, map[string]any{
		"collection": collection, "path": relPath, "id": id,
	})
}

// logRecordWriteRefusedAs records a record write that NEVER REACHED DISK —
// matching rest_library.go's logLibraryWriteRefused precedent: the refused
// population is what answers "who tried to write over a change they had not
// seen", and a single "refused" label collapsing every reason into one loses
// exactly that.
func (a *restAPI) logRecordWriteRefusedAs(r *http.Request, actor, workspaceID, collection, relPath, id, reason string) {
	decision := audit.DecisionDeny
	if reason == recordRefusalWriteFailed {
		decision = audit.DecisionError
	}
	a.logRecordAuditDecision(r, decision, actor, workspaceID, map[string]any{
		"collection": collection, "path": relPath, "id": id, "reason": reason,
	})
}

func (a *restAPI) logRecordAuditDecision(r *http.Request, decision, actor, workspaceID string, details map[string]any) {
	if a.auditor == nil {
		// A nil auditor silently voids every guarantee this file makes about
		// refusals being recorded, so it is stated once rather than
		// discovered by the absence of entries (review M9). Once, not per
		// call: a door being hammered must not turn a misconfiguration into a
		// log flood.
		logRecordAuditorMissingOnce.Do(func() {
			logger.ErrorCF("rest", "knowledge: no audit logger is wired to the record write door — "+
				"no record write or refusal will be recorded for the lifetime of this process",
				map[string]any{"workspace_id": workspaceID})
		})
		return
	}
	// The actor is resolved by resolveRecordWriteActor and passed in, never
	// re-derived here: a second derivation is a second chance to disagree
	// with the one that gated the write.
	details["actor"] = actor
	details["workspace_id"] = workspaceID
	if err := a.auditor.Log(&audit.Entry{
		Event:    recordWriteAuditEvent,
		Decision: decision,
		Details:  details,
	}); err != nil {
		logger.WarnCF("rest", "knowledge: record audit write failed",
			map[string]any{"workspace_id": workspaceID, "error": err.Error()})
	}
}
