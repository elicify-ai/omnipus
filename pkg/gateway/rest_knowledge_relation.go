// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// GAP-02 / #700 (2026-09-14 fix round): the web door for relation writes.
//
// RelationWriteRequest existed as a contract component since ADR-068 D15 —
// with NO path. Its add/remove/replace verbs were served only by the agent's
// knowledge_edit tool, so the SPA's relation and person cells had no door at
// all: GAP-02/T3b found them as navigable links with no picker, and every
// relation write had to go "through the agent" (the UI's own inert-cell
// tooltip said so).
//
// NO PARALLEL SPLICE LOGIC. This handler composes the SAME exported
// knowledge-layer primitives the agent's op "relation" path is built from:
//
//   - the NoteEdit splice constructors knowledge.AddListValue /
//     RemoveListValue / SetPropertyList / SetPropertyScalarChecked /
//     RemoveProperty (knowledge_edit_list.go / author.go — the exact
//     functions knowledge_edit_relation.go's splices call);
//   - the locked compare-and-swap write knowledge.EditNote (the identical
//     write verb handleRecordUpdate uses for ordinary properties);
//   - knowledge.RefreshIndexesForNote for the post-write index refresh;
//   - records.IsRelationProperty / IsDerivedProperty for the same
//     FR-046/FR-045 gates the agent path checks.
//
// The two things re-stated here rather than called are the ~4-line idempotent
// "[[...]]" wrap (knowledgeEditRelationWikilink, unexported) and the scalar
// slot check FR-035 performs against the note's current value
// (knowledgeEditRelationCurrentScalar, unexported) — both rebuilt from
// records.ResolveProperty, the same read every gateway record handler uses.
// Their twin call sites are named in comments beside each.
//
// Route wiring lives in rest_knowledge.go's handleKnowledge dispatcher (the
// one place /knowledge/... sub-paths are parsed); everything else about the
// door lives here.

package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// relationWikilink is the stored spelling of one relation target. Twin of
// knowledge_edit_relation.go's unexported knowledgeEditRelationWikilink —
// kept byte-compatible: an already-wrapped target passes through unchanged,
// everything else is wrapped once.
func relationWikilink(target string) string {
	t := strings.TrimSpace(target)
	if len(t) >= 4 && strings.HasPrefix(t, "[[") && strings.HasSuffix(t, "]]") {
		return t
	}
	return "[[" + t + "]]"
}

// relationCurrentValue reads the relation/person property's CURRENT stored
// values off the parsed record, in stored order and stored spelling. Twin of
// knowledgeEditRelationCurrentScalar's read (plus the list half), built on
// records.ResolveProperty — the same read the GET record handler serves.
func relationCurrentValue(rec records.Record, prop *records.Property) []string {
	pv := records.ResolveProperty(rec, prop)
	out := make([]string, 0, len(pv.Values))
	for _, v := range pv.Values {
		switch v.Type {
		case records.TypeRelation, records.TypePerson:
			link := v.Link.Raw
			if link == "" {
				link = v.Link.Target
			}
			if link != "" {
				out = append(out, link)
			}
		default:
			// A relation property holding something the parser typed as text
			// (a hand-edited scalar without brackets): report the raw text so
			// the caller's comparison sees what the file actually says.
			if v.Text != "" {
				out = append(out, v.Text)
			}
		}
	}
	return out
}

// handleKnowledgeRecordRelation implements POST
// /library/{workspace_id}/knowledge/records/{id}/relation.
//
// Refusal mapping mirrors handleRecordUpdate's, through the same helpers:
// 400 malformed request/property shape, 404 unknown record, 409 a stale
// version token (typed KnowledgeConflictError body), 503 lock timeout.
func (a *restAPI) handleKnowledgeRecordRelation(w http.ResponseWriter, r *http.Request, workspaceID, id string) {
	actor, attributable := a.resolveRecordWriteActor(r)
	if !attributable {
		// Same posture as the record write door (ADR-083 §4.2b / founder
		// ruling N4): a relation write is a write, and an unattributable
		// caller is refused rather than recorded as nobody.
		jsonErr(w, http.StatusUnauthorized, "an authenticated caller is required to write a record")
		return
	}

	var req gen.RelationWriteRequest
	validateEnabled := a.agentLoop.GetConfig().Gateway.ValidateInbound
	if !decodeAndValidate(w, r, "RelationWriteRequest", &req, validateEnabled) {
		return
	}

	if err := validateEntityID(id); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid record id: "+err.Error())
		return
	}
	// The body's id is redundant on this route (the path carries it) but it
	// is REQUIRED by the contract — and a disagreement is a client bug that
	// must never write one record while its caller believes another was
	// addressed.
	if req.Id != id {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf(
			"path names record %q but the body names %q — they must agree", id, req.Id))
		return
	}

	refuse := func(status int, reason, msg string) {
		a.logRecordWriteRefusedAs(r, actor, workspaceID, "", "", id, reason)
		jsonErr(w, status, msg)
	}

	found, ok, err := findVaultRecordByID(a.homePath, workspaceID, id)
	if err != nil {
		a.logRecordWriteRefusedAs(r, actor, workspaceID, "", "", id, recordRefusalWriteFailed)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if !ok {
		refuse(http.StatusNotFound, recordRefusalNotFound, "record not found")
		return
	}

	// Same whitespace-token posture as handleRecordUpdate: a required field
	// of spaces satisfies minLength and would otherwise surface as a 409
	// blaming a concurrent editor who does not exist.
	expectVersion := strings.TrimSpace(req.VersionToken)
	if expectVersion == "" {
		refuse(http.StatusBadRequest, recordRefusalVersionMissing,
			"version_token is required on a relation write")
		return
	}
	if refusal := recordWriteTokenRefusal(expectVersion); refusal != nil {
		refuse(refusal.status, refusal.reason, refusal.message)
		return
	}

	// The property gates — the same three the agent path checks inside
	// knowledgeEditRelationEdit, in the same order (derived first: reporting
	// the relation rule for a derived property would send the caller to fix
	// the wrong thing).
	prop, declared := found.schema.Property(req.Property)
	if !declared {
		refuse(http.StatusBadRequest, recordRefusalPropertyUnknown, fmt.Sprintf(
			"%s declares no property %q; declared properties are %s",
			found.schema.Type, req.Property, strings.Join(found.schema.PropertyNames(), ", ")))
		return
	}
	if records.IsDerivedProperty(prop) {
		refuse(http.StatusBadRequest, recordRefusalDerivedProperty, fmt.Sprintf(
			"%s.%s is a derived value, computed from other properties rather than stored — "+
				"no caller can write one, through this door or any other",
			found.schema.Type, req.Property))
		return
	}
	if !records.IsRelationProperty(prop) {
		refuse(http.StatusBadRequest, recordRefusalRelationProperty, fmt.Sprintf(
			"%s.%s is a %s property, not a relation — this door writes relation and "+
				"person properties only (op add, remove or replace, per FR-045)",
			found.schema.Type, req.Property, prop.Type))
		return
	}

	// Empty targets: valid ONLY with replace, where it clears the property
	// (RelationWriteRequest.targets' own contract, mirroring execRelation's
	// refusal for the silent-no-op ops).
	if len(req.Targets) == 0 && req.Op != gen.RelationWriteRequestOpReplace {
		refuse(http.StatusBadRequest, recordRefusalInvalidRequest, fmt.Sprintf(
			"'targets' must name at least one target for op %q; an empty list is accepted "+
				"only with op \"replace\", where it clears %s", req.Op, req.Property))
		return
	}

	wikilinks := make([]string, 0, len(req.Targets))
	for _, target := range req.Targets {
		wikilinks = append(wikilinks, relationWikilink(target))
	}

	current := records.ParseRecord(found.relPath, found.frontmatter)
	currentValues := relationCurrentValue(current, prop)

	// FR-035 for a scalar (many: false) slot — the same refusals
	// knowledgeEditRelationScalarSplice renders, read through
	// records.ResolveProperty instead of a private frontmatter walk.
	if !prop.Many {
		if len(wikilinks) > 1 {
			refuse(http.StatusBadRequest, recordRefusalInvalidRequest, fmt.Sprintf(
				"%s.%s holds one relation, not a list; got %d targets — send one, or declare "+
					"many: true in the schema",
				found.schema.Type, req.Property, len(wikilinks)))
			return
		}
		if req.Op == gen.RelationWriteRequestOpAdd && len(currentValues) == 1 && currentValues[0] != wikilinks[0] {
			refuse(http.StatusBadRequest, recordRefusalInvalidRequest, fmt.Sprintf(
				"%s.%s is a single relation and already points at %s (FR-035) — adding a "+
					"second target is refused. Send op \"replace\" to point it at %s instead, "+
					"or op \"remove\" to clear it first",
				found.schema.Type, req.Property, currentValues[0], wikilinks[0]))
			return
		}
	}

	// The splice itself — composed from the same exported NoteEdit
	// constructors knowledge_edit_relation.go's splices call, never re-typed
	// here. add/remove go one element at a time (each touches only its own
	// bytes and leaves every other edge alone); replace rewrites the span.
	var edit knowledge.NoteEdit
	if prop.Many {
		switch req.Op {
		case gen.RelationWriteRequestOpReplace:
			edit = knowledge.SetPropertyList(req.Property, wikilinks)
		case gen.RelationWriteRequestOpAdd:
			edit = chainRelationEdits(req.Property, knowledge.AddListValue, wikilinks)
		default: // remove
			edit = chainRelationEdits(req.Property, knowledge.RemoveListValue, wikilinks)
		}
	} else {
		switch req.Op {
		case gen.RelationWriteRequestOpReplace:
			// An empty replace clears a filled slot; on an already-absent
			// one it is a defined no-op. A non-empty replace writes the
			// slot (byte-identical content still reports Changed: false —
			// EditNote compares bytes, not intent).
			if len(wikilinks) == 0 {
				if len(currentValues) > 0 {
					edit = knowledge.RemoveProperty(req.Property)
				} else {
					edit = relationIdentityEdit
				}
			} else {
				edit = knowledge.SetPropertyScalarChecked(req.Property, wikilinks[0])
			}
		case gen.RelationWriteRequestOpAdd:
			// Only an EMPTY slot or the SAME value reaches here — the
			// filled-and-different case was refused above (FR-035).
			if len(currentValues) == 0 {
				edit = knowledge.SetPropertyScalarChecked(req.Property, wikilinks[0])
			} else {
				edit = relationIdentityEdit // already so — defined no-op
			}
		default: // remove
			if len(wikilinks) == 1 && len(currentValues) == 1 && currentValues[0] == wikilinks[0] {
				edit = knowledge.RemoveProperty(req.Property)
			} else {
				edit = relationIdentityEdit // not present — defined no-op
			}
		}
	}

	lockDir, lockErr := knowledge.LockDirFor(a.homePath, found.collection.Root())
	if lockErr != nil {
		a.logRecordWriteRefusedAs(r, actor, workspaceID, found.scoped.Name, found.relPath, id, recordRefusalWriteFailed)
		jsonErr(w, http.StatusInternalServerError, "internal server error")
		return
	}

	res, err := knowledge.EditNote(knowledge.OSLinkFS(), found.collection, knowledge.EditNoteRequest{
		RelPath:       found.relPath,
		Edits:         []knowledge.NoteEdit{edit},
		ExpectVersion: expectVersion,
		Now:           time.Now(),
		Actor:         knowledge.AuthorActor{WorkspaceID: workspaceID},
		Lock:          knowledge.NoteLockConfig{LockDir: lockDir},
	})
	if err != nil {
		a.finishRecordWriteError(w, r, actor, workspaceID, found.scoped.Name, found.relPath, id, err)
		return
	}

	a.logRecordWriteAudit(r, actor, workspaceID, found.scoped.Name, res.RelPath, id)

	warnings := []string{}
	if res.Changed {
		if warning := knowledge.RefreshIndexesForNote(r.Context(), a.homePath, found.collection.Root(), res.RelPath); warning != "" {
			warnings = append(warnings, warning)
		}
	}

	after := records.ParseRecord(res.RelPath, res.Content)
	jsonOK(w, gen.RelationWriteResponse{
		Record:        buildVaultRecordWire(found.schema, after, res.RelPath, res.Version),
		Changed:       res.Changed,
		StoredTargets: relationCurrentValue(after, prop),
		Warnings:      warnings,
	})
}

// chainRelationEdits composes one per-target NoteEdit into a single edit, the
// same left-to-right fold knowledgeEditRelationListSplice performs over
// AddListValue/RemoveListValue.
func chainRelationEdits(
	property string,
	constructor func(key, value string) knowledge.NoteEdit,
	wikilinks []string,
) knowledge.NoteEdit {
	return func(src []byte) ([]byte, error) {
		out := src
		for _, wl := range wikilinks {
			next, err := constructor(property, wl)(out)
			if err != nil {
				return nil, err
			}
			out = next
		}
		return out, nil
	}
}

// relationIdentityEdit is a defined no-op: byte-identical output, so EditNote
// reports Changed: false for the verbs whose contract makes "already so" a
// success rather than an error.
var relationIdentityEdit knowledge.NoteEdit = func(src []byte) ([]byte, error) { return src, nil }
