// Omnipus — ADR-068 D15/FR-045 and FR-035: knowledge_edit's op "relation",
// the agent door's three explicit relation verbs.
//
// # Why this op exists
//
// FR-045 says a relation property is "modified through RelationWriteRequest's
// three explicit verbs, because a read-then-write round trip silently
// replaces a relation list" (ADR-083 §1540). That is a data-integrity rule,
// not a UI limitation, and it binds an agent at least as hard as a browser:
// an agent doing bulk work across a hundred notes is precisely the caller who
// will read a list, append one edge, write it back, and destroy whatever a
// concurrent writer added in between — a hundred times, each one returning
// success.
//
// Until this file, the agent door had no verbs to be pointed at. It had
// PIECES of them, which is worse than having none, because the pieces looked
// like the whole thing:
//
//   - op "link" with `relation` ADDED one edge, and only added. There was no
//     unlink. (Removed once this op landed — see Execute's `relation` refusal.
//     Two ways to write one property is one too many, and the one that
//     survives is the one with the whole verb set.)
//   - set_property with `list_op: add`/`remove` reaches add and remove, but
//     only by treating a relation as an untyped string list — no wikilink
//     wrapping, no declared-cardinality enforcement, no idea it is a
//     relation at all.
//   - set_property with a list `value` reaches replace, unnamed: exactly the
//     read-then-write FR-045 exists to stop, and the only way to get there
//     was to not realise that is what you were doing.
//
// So FR-045 could not simply be switched on. Switching it on first would
// have removed the only remove path on the agent door and left nothing in
// its place. This file builds the replacement; knowledge_edit_schema.go's
// posture then switches the rule on for set_property, and its refusal names
// this op and its arguments so an agent can retry correctly from the error
// text alone.
//
// # Why an op and not a tool (Hard Constraint #6)
//
// A new TOOL would need a catalogue entry, a global-ceiling entry in
// pkg/config/defaults.go, a per-agent seed in pkg/coreagent/core.go, and a
// bump to catalogSizeToday. An OPERATION on knowledge_edit inherits
// knowledge_edit's existing policy entry and needs none of them — the diff
// to tool policy is ZERO LINES in both layers. That is the same argument
// EMB-095/EMB-096 made for op "embed", and the test that pins it
// (knowledge_tool_policy_embed_test.go, set equality over the eight
// knowledge_* names) covers this op for free: it would fail if this work had
// added a ninth name.
//
// The operator-consent question that argument has to answer is whether this
// op is WIDER than what granting knowledge_edit already grants. It is not:
// it writes ONE frontmatter property of ONE note the caller named by path,
// which is narrower than replace_body (an arbitrary span of a note's body)
// and no wider than set_property, whose place it takes for this one property
// type. It cannot touch a second file, and it cannot change what any other
// note means.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// The three verbs, matching RelationWriteRequest.op's enum exactly
// (contracts/components/schemas/RelationWriteRequest.yaml). The names are
// the contract's, not this tool's invention, so an operator reading an audit
// line from either door sees the same word.
const (
	// relationOpAdd adds the targets, leaving existing ones in place. A
	// target already present is a no-op, never a duplicate.
	relationOpAdd = "add"
	// relationOpRemove removes the targets, leaving the rest in place. A
	// target that is not present is a no-op, never an error.
	relationOpRemove = "remove"
	// relationOpReplace DISCARDS the existing targets. Named explicitly so
	// that destroying a list is never the accidental outcome of a
	// read-modify-write (FR-045).
	relationOpReplace = "replace"
)

// knowledgeEditRelationOps lists the verbs in the order they are documented —
// used to render "relation_op must be one of ..." in a refusal.
var knowledgeEditRelationOps = []string{relationOpAdd, relationOpRemove, relationOpReplace}

// ---------------------------------------------------------------------------
// execRelation — argument handling
// ---------------------------------------------------------------------------

func (t *EditTool) execRelation(ctx context.Context, target mutationTarget, args map[string]any) *tools.ToolResult {
	rel, err := cleanNoteArg(stringArg(args["path"]))
	if err != nil {
		return t.deps.refuse(AuthorOpEdit, target, nil, err.Error())
	}
	property := strings.TrimSpace(stringArg(args["property"]))
	if property == "" {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel}, "'property' is required")
	}
	relationOp := strings.TrimSpace(stringArg(args["relation_op"]))
	if relationOp == "" {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			"'relation_op' is required; one of "+strings.Join(knowledgeEditRelationOps, ", "))
	}
	if relationOp != relationOpAdd && relationOp != relationOpRemove && relationOp != relationOpReplace {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("'relation_op' must be one of %s, not %q",
				strings.Join(knowledgeEditRelationOps, ", "), relationOp))
	}
	targets, terr := knowledgeEditDecodeTargets(args["targets"])
	if terr != nil {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel}, terr.Error())
	}
	// An empty list is valid ONLY for replace, where it clears the property.
	// For add and remove it is rejected rather than run: both would be
	// no-ops, and a no-op that reports success is how a caller comes to
	// believe a write landed when nothing was written (the contract's own
	// wording: "silent no-ops that look like successful writes").
	if len(targets) == 0 && relationOp != relationOpReplace {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel},
			fmt.Sprintf("'targets' must name at least one target for relation_op %q; "+
				"an empty list is accepted only with relation_op \"replace\", where it clears %s",
				relationOp, property))
	}
	// expect is handed to EditNote unchanged, including when empty: EditNote's
	// own FR-106 compare-and-swap refuses an empty token itself (author.go's
	// checkVersion) with a *ConflictError knowledgeEditFailure already
	// renders. Matching every other op on this tool — see execSetProperty's
	// comment for why a second tool-level check was removed as dead weight.
	expect := strings.TrimSpace(stringArg(args["expect_version"]))

	set, report, serr := t.loadSchemas(target)
	if serr != nil {
		return t.deps.refuse(AuthorOpEdit, target, []string{rel}, serr.Error())
	}

	var gov knowledgeEditGovernance
	edit := knowledgeEditRelationEdit(set, report, property, relationOp, targets, &gov)

	res, err := EditNote(OSLinkFS(), target.collection, EditNoteRequest{
		RelPath: rel, Edits: []NoteEdit{edit}, ExpectVersion: expect,
		Now: t.deps.now(), Audit: t.deps.Audit, Actor: target.actor(), Lock: target.lock,
	})
	if err != nil {
		return knowledgeEditFailure(AuthorOpEdit, err)
	}
	// See execSetProperty's identical comment: a no-op edit changed no byte
	// on disk, so neither index has anything to re-derive.
	var indexWarning string
	if res.Changed {
		indexWarning = refreshIndexesForNote(ctx, t.deps.Home, target.col.Root, res.RelPath)
	}
	return tools.NewToolResult(RenderEdit(EditData{
		Op: opRelation, Path: res.RelPath, Version: res.Version,
		Property: property, RelationOp: relationOp, Targets: targets,
		Changed: res.Changed, SchemaNote: gov.Note(), IndexWarning: indexWarning,
	}))
}

// knowledgeEditDecodeTargets reads the `targets` argument.
//
// A JSON array of strings is the contract's shape and the expected one. A
// BARE STRING is also accepted, as the one-element list it obviously means:
// models send `"targets": "Acme Ltd"` routinely, and the alternative — a
// refusal an agent then has to guess its way out of — costs a round trip to
// enforce a distinction that carries no meaning here. What is NOT accepted
// is an object or a nested array: those are not a target under any reading,
// and stringifying one would write a line of Go-formatted struct text into
// the note's frontmatter.
//
// A nil/absent argument returns an EMPTY list rather than an error, because
// absent and `[]` mean the same thing to the verb that accepts an empty list
// (replace, which clears) and are both refused by the two that do not. The
// caller decides; this function only reports the shape.
func knowledgeEditDecodeTargets(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	if list, ok := raw.([]any); ok {
		out := make([]string, 0, len(list))
		for i, el := range list {
			s, sok := el.(string)
			if !sok {
				return nil, fmt.Errorf("'targets'[%d] must be a note name or path as text (got %T)", i, el)
			}
			s = strings.TrimSpace(s)
			if s == "" {
				return nil, fmt.Errorf("'targets'[%d] is empty; every target must name a note", i)
			}
			out = append(out, s)
		}
		return out, nil
	}
	if s, ok := raw.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, nil
		}
		return []string{s}, nil
	}
	return nil, fmt.Errorf("'targets' must be a list of note names or paths (got %T)", raw)
}

// knowledgeEditRelationWikilink renders one target as the quoted wikilink a
// relation is stored as (D5.1), without double-wrapping one that already
// arrived in that form.
//
// Accepting both spellings is deliberate. An agent that has just read a note
// sees `company: "[[Acme Ltd]]"` and will quite reasonably send the target
// back exactly as it read it; an agent working from a search result sends
// the bare name. Turning the first into `[[[[Acme Ltd]]]]` would store a
// link that resolves to nothing, silently, and would render as a broken edge
// nobody wrote.
func knowledgeEditRelationWikilink(target string) string {
	t := strings.TrimSpace(target)
	if len(t) >= 4 && strings.HasPrefix(t, "[[") && strings.HasSuffix(t, "]]") {
		return t
	}
	return "[[" + t + "]]"
}

// ---------------------------------------------------------------------------
// knowledgeEditRelationEdit — the splice
// ---------------------------------------------------------------------------

// knowledgeEditRelationEdit composes schema resolution, FR-046/FR-045/FR-035
// enforcement and the frontmatter splice into one NoteEdit.
//
// # Which shape the property is written in
//
// Three cases, on the same D5 reading ("Cardinality is declared and enforced
// (many: true or not)") that op "link"'s removed `relation` mode once shared
// — the argument is restated here in full rather than cited, because this is
// now the only place it is made:
//
//   - Declared many: true  -> LIST. add/remove touch one element each and
//     leave every other byte alone; replace rewrites the whole list.
//   - Declared many: false -> SCALAR SLOT. FR-035's "adding a second target
//     to a scalar relation is refused" is enforced against the note's CURRENT
//     value, and the refusal names relation_op "replace" as the way to change
//     a slot that is already filled.
//   - Not declared at all -> LIST. FR-005's "ordinary notes are
//     unconstrained": an undeclared property carries no cardinality-of-one
//     guarantee to honour, and a relation is routinely put on a note whose
//     author never wrote a records/*.yaml. List is also the failure-safe
//     direction — AddListValue creates a fresh one-item list against an
//     absent key, and REFUSES rather than silently promotes an existing
//     scalar.
func knowledgeEditRelationEdit(set *records.SchemaSet, report *records.SchemaLoadReport, property, relationOp string, targets []string, gov *knowledgeEditGovernance) NoteEdit {
	return func(src []byte) ([]byte, error) {
		schema, typeName, reason, detail := knowledgeEditResolveSchema(set, report, src)
		governed := reason == knowledgeEditGoverned
		if gov != nil {
			*gov = knowledgeEditGovernance{Reason: reason, TypeName: typeName, RejectionReason: detail}
		}

		listShaped := true
		if governed {
			prop, ok := schema.Property(property)
			if !ok {
				return nil, fmt.Errorf("%w: %s declares no property %q; declared properties are %s",
					ErrUnknownProperty, typeName, property, strings.Join(schema.PropertyNames(), ", "))
			}
			// FR-046 first, matching CheckRecordPropertyWrites' precedence:
			// a derived property has no meaningful relation verb either, and
			// reporting the relation rule for one would send the caller off
			// to fix the wrong thing.
			if records.IsDerivedProperty(prop) {
				return nil, fmt.Errorf("%w: %s.%s is a derived value, computed from other properties "+
					"rather than stored — no caller can write one, through this op or any other",
					ErrDerivedProperty, typeName, property)
			}
			// This op writes relations and NOTHING ELSE. A caller who sends
			// a text or enum property here has confused two ops, and the
			// refusal has to name the other one or the retry is a guess.
			if !records.IsRelationProperty(prop) {
				return nil, fmt.Errorf("%w: %s.%s is a %s property, not a relation — op \"relation\" "+
					"writes relation and person properties only. Use op \"set_property\" with "+
					"property: %q and a value",
					ErrPropertyValue, typeName, property, prop.Type, property)
			}
			listShaped = prop.Many
		}

		wikilinks := make([]string, 0, len(targets))
		for _, target := range targets {
			wikilinks = append(wikilinks, knowledgeEditRelationWikilink(target))
		}

		// Validate only the values actually being WRITTEN. A remove is
		// deliberately not validated: the caller is naming a value to take
		// OUT, and refusing to remove a malformed edge — which is exactly
		// the edge most worth removing — would make the tool useless on the
		// data that needs it most.
		if governed && relationOp != relationOpRemove && len(wikilinks) > 0 {
			if err := knowledgeEditValidatePropertyAgainstSchema(
				schema, typeName, property, wikilinks, listShaped, knowledgeEditRelationAllowed); err != nil {
				return nil, err
			}
		}

		if !listShaped {
			return knowledgeEditRelationScalarSplice(src, typeName, property, relationOp, wikilinks)
		}
		return knowledgeEditRelationListSplice(src, property, relationOp, wikilinks)
	}
}

// knowledgeEditRelationListSplice applies a verb to a list-shaped relation
// property.
//
// add and remove go one element at a time through AddListValue /
// RemoveListValue, which is the whole point of the verbs existing: each
// touches ONLY its own element's bytes and leaves every other edge in the
// list exactly as it was, including edges this caller has never seen. Both
// are idempotent by those primitives' own documented contract — an add of a
// target already present, or a remove of one that is not there, returns src
// unchanged and reports "unchanged", never an error.
//
// replace is the destructive verb and uses SetPropertyList, which rewrites
// the whole span. An empty list writes "key: []" rather than deleting the
// key, matching RemoveListValue's own documented choice: "present and empty"
// and "absent" are different validation findings, and a clear should not
// quietly become the other one.
func knowledgeEditRelationListSplice(src []byte, property, relationOp string, wikilinks []string) ([]byte, error) {
	if relationOp == relationOpReplace {
		return SetPropertyList(property, wikilinks)(src)
	}
	out := src
	for _, wl := range wikilinks {
		var next []byte
		var err error
		if relationOp == relationOpAdd {
			next, err = AddListValue(property, wl)(out)
		} else {
			next, err = RemoveListValue(property, wl)(out)
		}
		if err != nil {
			return nil, err
		}
		out = next
	}
	return out, nil
}

// knowledgeEditRelationScalarSplice applies a verb to a property the schema
// declares as a SINGLE relation (many is absent or false).
//
// FR-035 — "adding a second target to a scalar relation is refused" — is
// enforced here, against the note's current value rather than against a
// count of what the caller sent, because that is the only place the fact
// lives. The refusal names relation_op "replace", because a caller who
// genuinely wants to move a single-slot relation from one target to another
// has a verb for it and needs to be told which.
func knowledgeEditRelationScalarSplice(src []byte, typeName, property, relationOp string, wikilinks []string) ([]byte, error) {
	if len(wikilinks) > 1 {
		return nil, fmt.Errorf("%w: %s.%s holds one relation, not a list; got %d targets — send one, "+
			"or declare many: true in %s/%s/%s.yaml",
			ErrPropertyArity, typeName, property, len(wikilinks),
			records.VaultMarkerDirName, records.RecordsDirName, typeName)
	}
	current, present := knowledgeEditRelationCurrentScalar(src, property)

	switch relationOp {
	case relationOpAdd:
		if present && current != wikilinks[0] {
			return nil, fmt.Errorf("%w: %s.%s is a single relation and already points at %s (FR-035) — "+
				"adding a second target is refused. Send relation_op \"replace\" to point it at %s "+
				"instead, or relation_op \"remove\" to clear it first",
				ErrPropertyArity, typeName, property, current, wikilinks[0])
		}
		if present {
			return src, nil // already so — defined no-op (Changed: false)
		}
		return SetPropertyScalarChecked(property, wikilinks[0])(src)
	case relationOpRemove:
		if !present || current != wikilinks[0] {
			return src, nil // not present — defined no-op (Changed: false)
		}
		return RemoveProperty(property)(src)
	default: // relationOpReplace
		if len(wikilinks) == 0 {
			if !present {
				return src, nil // already absent — defined no-op
			}
			return RemoveProperty(property)(src)
		}
		return SetPropertyScalarChecked(property, wikilinks[0])(src)
	}
}

// knowledgeEditRelationCurrentScalar reports the single relation value a note
// currently carries for property, and whether it carries one at all.
//
// present is false for an absent key AND for an explicit null (`company:`
// with nothing after it), because FR-007 makes an explicit null an ABSENCE
// rather than a value — the same reading every other pass in this package
// takes of a KindNull node.
//
// A key whose current value is a SEQUENCE answers present=true with the
// rendered items, so FR-035's refusal fires rather than a scalar splice
// silently flattening a list the schema says should not exist. Reporting it
// as absent would let an "add" overwrite a real list of edges through a
// property the schema calls single-valued — the exact data loss this whole
// file exists to prevent, arrived at from the other direction.
//
// An unparsable frontmatter block answers absent. It is not this function's
// failure to report: the splice primitive called immediately after re-parses
// the same bytes and refuses with ErrFrontmatterUnterminated, which is the
// same reasoning knowledgeEditResolveSchema's own doc comment gives.
func knowledgeEditRelationCurrentScalar(src []byte, property string) (value string, present bool) {
	fm, ferr := records.ParseFrontmatter(src)
	if ferr != nil {
		return "", false
	}
	node, ok := fm.Values[property]
	if !ok {
		return "", false
	}
	switch node.Kind {
	case records.KindScalar:
		if strings.TrimSpace(node.Text) == "" {
			return "", false
		}
		return node.Text, true
	case records.KindSequence:
		if len(node.Items) == 0 {
			return "", false
		}
		texts := make([]string, 0, len(node.Items))
		for _, item := range node.Items {
			texts = append(texts, item.Text)
		}
		return strings.Join(texts, ", "), true
	default: // records.KindNull, records.KindMapping
		return "", false
	}
}
