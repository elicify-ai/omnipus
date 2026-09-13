// Omnipus — ADR-068 D15.3 / spec 4.1.2: the agent-facing surface of knowledge_find.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledgefind

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/records"
)

// ToolName is the one retrieval tool. There is no second.
const ToolName = "knowledge_find"

// ---------------------------------------------------------------------------
// THE DESCRIPTION IS THE HIGHEST-LEVERAGE STRING IN THE SYSTEM
//
// It is roughly 150 tokens and it is the ONLY thing the model sees before
// deciding whether to call this tool and with what. Harness framing alone moved
// measured accuracy by about 17 points in the research this design rests on —
// comparable to changing the retriever — so this text is tuned, not written.
//
// Three rules govern what belongs here and what does not:
//
//  1. TEACH THE LOOP, not the parameters. Orient, prefer a saved view, narrow
//     when more than a screenful matches, then read the winners. A model that
//     knows the loop composes calls; a model that knows only the fields guesses
//     at them.
//  2. OPERATION DETAIL GOES IN THE PARAMETER DESCRIPTIONS AND THE ERROR
//     MESSAGES, where it is read at the moment it is needed and costs nothing
//     until then. Every refusal this tool returns names its own remedy, so the
//     description does not have to enumerate them.
//  3. STATE THE GUARANTEE. "Refused, not silently empty" is the single most
//     useful thing a caller can know about this surface, because it is what
//     makes a zero-row answer trustworthy — and a zero-row answer nobody trusts
//     sends the model back to guess again.
// ---------------------------------------------------------------------------

// Description is the tool description, verbatim.
const Description = `Search the knowledge base — one call for plain words, typed filters, saved views, relations, and tasks.

The loop: call knowledge_describe first (property names are declared per record type, and a guessed one is refused rather than silently empty). Use a saved view when one fits. Start with words; when more than a screenful matches, narrow with filter instead of paging. Then knowledge_read the winners.

Every answer opens with its completeness verdict, names each record it could not evaluate and the fix for it, and ends with the calls to make next. An unknown property, operator or value is refused with the valid ones listed — never as zero results, so an empty answer means the knowledge base is empty.`

// ---------------------------------------------------------------------------
// THE ADVERTISED SUMMARY OPS — ONE LIST, NOT A SECOND ONE
//
// This block previously advertised four ops — count, sum, min, max — while
// request.go accepted fifteen (generated.VaultFindAggregateOp.Valid()). Eleven
// working, tested, contract-defined summaries were therefore invisible to the
// only caller that matters. A model that guessed `median` happened to succeed;
// a provider doing strict structured output against the advertised enum could
// not emit it at all, so three quarters of FR-150..FR-155 was dead in practice.
//
// WHAT CAUSED IT was a hand-copied list, so the fix is not a longer hand-copied
// list. Two properties make the drift structural rather than remembered:
//
//  1. Every entry below names a GENERATED CONSTANT, never a string literal.
//     Rename or delete a member in the contract and this file stops compiling.
//  2. TestAdvertisedAggregateOpsMatchTheAcceptedEnum reads the generated enum's
//     members out of the generated source itself and fails, naming the missing
//     ops, if this table and that enum ever differ in either direction. That
//     test is what catches an ADDED member, which the compiler cannot see.
//
// WHY A TABLE IN THIS PACKAGE RATHER THAN A LIST FROM THE GENERATED ONE: the
// generated package exposes no iterable list of members — oapi-codegen emits
// the constants and a `Valid()` switch and nothing else, and Go has no
// reflection over constants. So there is no runtime expression that enumerates
// the enum. The closest thing to a single source of truth is therefore the
// generated CONSTANTS (compile-checked here) plus a test that enumerates the
// generated SOURCE (drift-checked there); the glosses have to live somewhere
// hand-written regardless, because the contract carries no per-member text.
// ---------------------------------------------------------------------------

// aggregateOpDoc is one advertised summary: the generated enum member, and the
// half-line that tells a model what distinguishes it from its neighbours.
//
// A model that can see `median` but cannot tell it from `avg` is barely better
// off than one that cannot see it, so the gloss is part of the fix rather than
// a nicety.
type aggregateOpDoc struct {
	op   generated.VaultFindAggregateOp
	help string
}

// aggregateOpCatalog is the fifteen, in TEACHING order rather than alphabetical
// order: the one that needs no property, then the number domain, then the date
// domain, then checkboxes, then the two defined for every type.
var aggregateOpCatalog = []aggregateOpDoc{
	{generated.VaultFindAggregateOpCount, "rows evaluated, the one op that needs no property"},
	{generated.VaultFindAggregateOpSum, "exact total of a number property"},
	{generated.VaultFindAggregateOpAvg, "arithmetic mean, rounded, with the label naming the scale"},
	{generated.VaultFindAggregateOpMedian, "middle value, an even count averaging the two middle ones"},
	{generated.VaultFindAggregateOpStddev, "POPULATION standard deviation (divisor n)"},
	{generated.VaultFindAggregateOpMin, "smallest value, numbers or dates"},
	{generated.VaultFindAggregateOpMax, "largest value, numbers or dates"},
	{generated.VaultFindAggregateOpRange, "max minus min, rendered as a duration over dates"},
	{generated.VaultFindAggregateOpEarliest, "earliest date, the date domain's name for min"},
	{generated.VaultFindAggregateOpLatest, "latest date, the date domain's name for max"},
	{generated.VaultFindAggregateOpChecked, "checkbox values that are true"},
	{generated.VaultFindAggregateOpUnchecked, "checkbox values that are false, an ABSENT checkbox counting toward neither"},
	{generated.VaultFindAggregateOpEmpty, "rows carrying NO value for the property, defined for every type"},
	{generated.VaultFindAggregateOpFilled, "rows carrying one, defined for every type"},
	{generated.VaultFindAggregateOpUnique, "DISTINCT values, by the comparator's equality (3 and 3.0 are one)"},
}

// aggregateOpSeparator divides one gloss from the next.
//
// It is deliberately NOT a semicolon: the glosses themselves read as clauses,
// and a semicolon between them made "count — rows evaluated; the ONLY op that
// takes no property; sum — ..." ambiguous about where one op ended and the next
// began. A separator that appears nowhere inside a gloss is what makes the list
// parseable by eye, and TestEveryAdvertisedAggregateOpCarriesAGloss holds that
// property.
const aggregateOpSeparator = " · "

// AdvertisedAggregateOps is the enum the model receives, in catalogue order.
//
// It is exported because the drift guard has to be able to ask what was
// advertised without re-deriving it from the rendered schema, and a guard that
// re-derives its own oracle is a guard that agrees with itself.
func AdvertisedAggregateOps() []string {
	out := make([]string, 0, len(aggregateOpCatalog))
	for _, d := range aggregateOpCatalog {
		out = append(out, string(d.op))
	}
	return out
}

// ---------------------------------------------------------------------------
// THE ORACLE, EXPORTED — SO A WRITER CAN REFUSE EXACTLY WHAT THIS READER DOES
//
// request.go's aggregate branch refuses `sum` over a text property, and that
// refusal aborts the WHOLE find request rather than just the total. Anything
// that WRITES a saved view therefore has to be able to ask the same question
// before it writes, or it produces view files that cannot be served — which is
// precisely what pkg/vaultimport did: it checked that a summary's property NAME
// resolved and never asked its TYPE.
//
// These two functions exist so that the writer asks THIS table rather than
// keeping a second copy of it. A second copy is how the writer and the reader
// came to disagree in the first place, and the disagreement is invisible until
// an operator opens a view and gets a refusal instead of rows.
// ---------------------------------------------------------------------------

// SummaryOpDefinedForType reports whether FR-150's op is defined over a
// property of type t — the same predicate request.go gates every aggregate on.
func SummaryOpDefinedForType(op string, t records.PropertyType) bool {
	return opDefinedForType(op, t)
}

// SummaryOpsDefinedFor names the summaries type t DOES define, sorted, so a
// caller refusing an op can name the alternatives the way this package's own
// refusals do rather than leaving the reader to guess.
func SummaryOpsDefinedFor(t records.PropertyType) []string { return opsDefinedFor(t) }

// aggregateOpDescription is the glosses, folded into the one string JSON Schema
// gives an enum-valued property.
//
// JSON Schema has no per-member description — `enum` is a bare array of values
// — and the alternatives (a oneOf of const branches, or `x-`extensions) are
// either hostile to strict structured output or dropped by every provider that
// forwards a schema. So the glosses ride in the property's own description,
// derived from the same table as the enum so the two cannot disagree.
func aggregateOpDescription() string {
	var b strings.Builder
	b.WriteString("The reduction. ")
	for i, d := range aggregateOpCatalog {
		if i > 0 {
			b.WriteString(aggregateOpSeparator)
		}
		b.WriteString(string(d.op))
		b.WriteString(" — ")
		b.WriteString(d.help)
	}
	b.WriteString(". Every op except count needs a property.")
	return b.String()
}

// Parameters is the JSON Schema the model sees.
//
// The per-parameter text carries the operation detail the description
// deliberately omits. That split is the point: the description is paid for on
// every request whether or not the tool is called, and these are read only once
// the model has decided to call it.
func Parameters() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"words": map[string]any{
				"type": "string",
				"description": "Free text, ranked. Composes with every other argument — the answer is the " +
					"intersection, never the union. If nothing matches, the reply lists the terms the " +
					"index actually holds; it does not broaden your query for you.",
			},
			"type": map[string]any{
				"type": "string",
				"description": "Record type. Required before any typed filter, sort, group or total, " +
					"because property names are scoped to their type. Unknown → refused, listing the declared types.",
			},
			"kind": map[string]any{
				"type": "string",
				"enum": []string{KindNote, KindRecord, KindTask, KindAttachment},
				"description": "Default note. record narrows note to the notes that DECLARE a record type, " +
					"which is what to use before a typed follow-up. task returns CHECKBOX LINES rather than " +
					"notes — each row carries its path, line number, open/done and text. attachment returns " +
					"non-note files.",
			},
			"filter": map[string]any{
				"type": "object",
				"description": "A tree of {all:[...]}, {any:[...]}, {not:{...}} over leaves " +
					"{property, op, value}. op is SQL's: = <> < <= > >= LIKE IN \"IS NULL\" \"IS NOT NULL\". " +
					"LIKE is anchored to the WHOLE value (% and _ are the wildcards), so LIKE 'Acme' is " +
					"exactly = 'Acme' and never a substring match. IN takes a non-empty values list. " +
					"Use {not:{p,'=',v}} to include records where p is absent; {p,'<>',v} excludes them, " +
					"as it does in SQL. Anything else — JOIN, BETWEEN, a subquery, a function — is refused " +
					"naming the supported set and the argument that does the job.",
			},
			"view": map[string]any{
				"type":        "string",
				"description": "A saved view, applied first; filter then refines it. Prefer this when one fits.",
			},
			"near": map[string]any{
				"type": "string",
				"description": "A NOTE path or [[wikilink]] — not free text. Restricts the answer to notes " +
					"within hops link steps of that note, and composes with words and filter. A near that " +
					"names no note is refused; use words for text.",
			},
			"collection": map[string]any{
				"type": "string",
				"description": "Which knowledge base to query, by name, when more than one is mounted into " +
					"this workspace. Omit when exactly one is in scope. Every row carries the collection " +
					"it came from.",
			},
			"hops": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     MaxHops,
				"description": "Link steps from near. 1 or 2; a third is refused — run a second search from a result instead.",
			},
			"join": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Relation properties whose columns to borrow onto each row. Borrowed values render as borrowed.",
			},
			"group_by": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"property":  map[string]any{"type": "string"},
						"direction": map[string]any{"type": "string", "enum": []string{"asc", "desc"}},
					},
					"required": []string{"property"},
				},
				"maxItems": MaxGroupLevels,
				"description": "Up to two levels, outermost first. A record holding several values appears in every " +
					"group it belongs to. Each key takes its own direction for the GROUP order (default asc); " +
					"a number or date orders naturally, an enum lexically, and the absent group is last either way.",
			},
			"sort": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"property":  map[string]any{"type": "string"},
						"direction": map[string]any{"type": "string", "enum": []string{"asc", "desc"}},
					},
					"required": []string{"property"},
				},
				"description": "Default relevance. An enum sorts lexically on its folded form; express a domain " +
					"order by prefixing the declared values (1-lead, 2-qualified).",
			},
			"select": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Columns to render. Changes what is SHOWN, never what is matched.",
			},
			"aggregate": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"op": map[string]any{
							"type":        "string",
							"enum":        AdvertisedAggregateOps(),
							"description": aggregateOpDescription(),
						},
						"property": map[string]any{"type": "string"},
					},
					"required": []string{"op"},
				},
				"description": "Totals over the full evaluated set, never the page shown. Each states its own scope. " +
					"An op is scoped to the property's DOMAIN — a summary the type does not define is refused " +
					"naming the ones it does, never answered with a zero. " +
					"A number the record type pairs with a companion unit (a currency, a measure) is totalled ONCE PER " +
					"UNIT VALUE and never across units, so ONE aggregate can answer with SEVERAL totals, each naming its " +
					"unit; rows whose unit is missing or ambiguous are shown, excluded from every total and counted. " +
					"Totalling such a number without `type` is refused, because no single record type can resolve the unit.",
			},
			"explain": map[string]any{
				"type":        "boolean",
				"description": "Report the plan and evaluate nothing. Use it to check a query before running it over a large knowledge base.",
			},
			"limit": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf("Rows per page, default %d, capped at %d. Over the cap it is clamped and the clamp is reported.",
					DefaultLimit, MaxLimit),
			},
			"cursor": map[string]any{
				"type":        "string",
				"description": "From a previous reply's next call. A cursor that can no longer be honoured is an error, never a silent restart.",
			},
			"detail": map[string]any{
				"type":        "string",
				"enum":        []string{"minimal", "standard"},
				"description": "minimal drops the columns and keeps the verdict. Use it when scanning many rows for one path.",
			},
		},
	}
}

// Call is the executable entry point: raw arguments in, the compact text the
// model reads out.
//
// It returns the RENDERED TEXT on a refusal as well as on an answer, together
// with the error. A refusal the model cannot read is a refusal it cannot act on,
// and the whole point of naming the remedy in the message is that the next call
// writes itself.
func Call(ctx context.Context, d Deps, raw []byte) (string, error) {
	req, r := decodeRequest(raw)
	if r != nil {
		// The arguments could not be decoded, so the echo is the bytes as sent —
		// which is the only honest report of what arrived, and the one a caller
		// needs in order to see their own typo.
		return Render(refusalResponse(req, "arguments as sent: "+string(raw), r)), r
	}
	resp, err := Find(ctx, d, req)
	return Render(resp), err
}

// decodeRequest refuses an undeclared argument BY NAME (FR-022c).
//
// It decodes twice on purpose. encoding/json silently drops a field the target
// struct does not declare, so decoding straight into the generated type would
// accept `where:` or `sql:` or a misspelled `filtr:` and answer a DIFFERENT
// question from the one asked, with nothing saying so. The first pass into a
// map is what makes the drop visible.
func decodeRequest(raw []byte) (generated.VaultFindRequest, *RefusalError) {
	var req generated.VaultFindRequest
	if len(raw) == 0 {
		return req, nil
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return req, refuse(problem(generated.UnsupportedParameter,
			fmt.Sprintf("the arguments could not be read as JSON: %v", err),
			"send an object whose keys are: "+strings.Join(AcceptedParameters, ", ")), err)
	}

	accepted := map[string]bool{}
	for _, name := range AcceptedParameters {
		accepted[name] = true
	}
	var unknown []string
	for name := range probe {
		if !accepted[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		p := problem(generated.UnsupportedParameter,
			fmt.Sprintf("%s is not an argument of %s; accepted: %s",
				strings.Join(quoteAll(unknown), ", "), ToolName, strings.Join(AcceptedParameters, ", ")),
			unknownParameterRemedy(unknown))
		names := append([]string{}, AcceptedParameters...)
		p.Permitted = &names
		return req, refuse(p, nil)
	}

	// THE SAME CHECK, ONE LEVEL DOWN. The probe above is top-level only, and
	// the second pass is a plain Unmarshal with no DisallowUnknownFields — so
	// an extra key on a filter LEAF vanished exactly the way `where:` used to
	// vanish at the top. VaultFilterNode has six optional pointer fields, so
	// `{"property":"status","op":"=","value":"open","negate":true}` decoded
	// cleanly and the caller received the exact complement of what they asked
	// for, with a "complete" verdict. records.ParseView already uses
	// DisallowUnknownFields for this reason; this is find's equivalent.
	if r := checkFilterNodeKeys(probe["filter"]); r != nil {
		return req, r
	}

	// LITERALS ARRIVE IN ANY JSON SHAPE AND LEAVE AS LEXICAL STRINGS (UAT
	// 2026-09-13, D-11). The generated type reads `value` as a string, so a
	// JSON number or an array was rejected by the decoder with a Go struct
	// path in the message — while the `IN` operator demanded exactly that
	// array. The conversion happens HERE, on the literal's own bytes, so a
	// decimal keeps every digit the caller wrote.
	if filter, ok := probe["filter"]; ok && len(filter) > 0 {
		normalized, r := normalizeFilterLiterals(filter, "filter", 0)
		if r != nil {
			return req, r
		}
		if string(normalized) != string(filter) {
			probe["filter"] = normalized
			rebuilt, err := json.Marshal(probe)
			if err != nil {
				return req, refuse(problem(generated.UnsupportedParameter,
					fmt.Sprintf("the arguments could not be re-encoded after reading the filter: %v", err),
					"check the argument types; call knowledge_describe if you are unsure what a property holds"), err)
			}
			raw = rebuilt
		}
	}

	if err := json.Unmarshal(raw, &req); err != nil {
		return req, refuse(problem(generated.UnsupportedParameter,
			plainDecodeMessage(err),
			"check the argument types; call knowledge_describe if you are unsure what a property holds"), err)
	}
	return req, nil
}

// plainDecodeMessage turns the standard decoder's error into a sentence that
// names the argument, what it expects and what was received — in plain words
// (UAT 2026-09-13, D-31).
//
// encoding/json's own text is written for a Go programmer: "cannot unmarshal
// array into Go struct field VaultFilterNode.filter.all.value of type string".
// Every part of that an agent could act on is also carried structurally on
// *json.UnmarshalTypeError — the JSON path (Field), the JSON kind that arrived
// (Value) and the Go type the field wanted (Type) — so the sentence is rebuilt
// from those and the Go type name, struct name and the word "unmarshal" never
// reach the caller.
func plainDecodeMessage(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "the arguments"
		} else {
			field = fmt.Sprintf("argument %q", field)
		}
		return fmt.Sprintf("%s expects %s, but %s was sent",
			field, plainExpectedType(typeErr.Type), plainReceivedKind(typeErr.Value))
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return fmt.Sprintf("the arguments are not well-formed JSON (at byte %d)", syntaxErr.Offset)
	}
	return "the arguments did not match the expected shape"
}

// plainExpectedType names a Go type the way a reader of the tool's own
// parameter description would: text, a whole number, a list, an object.
func plainExpectedType(t reflect.Type) string {
	if t == nil {
		return "a different shape"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "text"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Bool:
		return "true or false"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Struct, reflect.Map:
		return "an object"
	}
	return "a different shape"
}

// plainReceivedKind names the JSON kind encoding/json reports in
// UnmarshalTypeError.Value ("string", "number", "array", "object", "bool",
// "null", or a value-specific spelling such as "number -3") in the same words
// plainExpectedType uses, so the two halves of the sentence read alike.
func plainReceivedKind(value string) string {
	kind, _, _ := strings.Cut(value, " ")
	switch kind {
	case "string":
		return "text"
	case "number":
		return "a number"
	case "array":
		return "a list"
	case "object":
		return "an object"
	case "bool":
		return "true/false"
	case "null":
		return "null"
	}
	if value == "" {
		return "something else"
	}
	return value
}

// filterNodeKeys is the closed set of keys a filter node may carry, READ OFF
// THE GENERATED TYPE rather than written out here.
//
// A hand-written list would be a second copy of the contract, and the day
// VaultFilterNode gains a field this file would start refusing it. Reflection
// over the json tags is the one expression that cannot drift.
func filterNodeKeys() map[string]bool {
	out := map[string]bool{}
	t := reflect.TypeOf(generated.VaultFilterNode{})
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}

// checkFilterNodeKeys walks the filter tree as RAW JSON and refuses any key a
// filter node does not declare, naming it and where it sat.
//
// The walk is ITERATIVE. A recursive one would descend as deep as the caller's
// own JSON, and this runs before FR-023c's depth bound has been measured.
func checkFilterNodeKeys(root json.RawMessage) *RefusalError {
	if len(root) == 0 {
		return nil
	}
	accepted := filterNodeKeys()
	names := make([]string, 0, len(accepted))
	for name := range accepted {
		names = append(names, name)
	}
	sort.Strings(names)

	type framed struct {
		raw   json.RawMessage
		where string
	}
	stack := []framed{{raw: root, where: "filter"}}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		var node map[string]json.RawMessage
		if err := json.Unmarshal(f.raw, &node); err != nil {
			// Not an object. The shape refusal belongs to buildNode and to the
			// generated type's own decode, both of which say it better than a
			// key check could.
			continue
		}
		var unknown []string
		for name := range node {
			if !accepted[name] {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			p := problem(generated.UnsupportedParameter,
				fmt.Sprintf("%s is not part of a filter node at %s; a node carries only: %s",
					strings.Join(quoteAll(unknown), ", "), f.where, strings.Join(names, ", ")),
				"a leaf is {property, op, value} (or values for IN); negate a leaf with "+
					"{not: {...}}, which is a node of its own")
			permitted := append([]string{}, names...)
			p.Permitted = &permitted
			return refuse(p, nil)
		}

		for _, key := range []string{"all", "any"} {
			child, ok := node[key]
			if !ok {
				continue
			}
			var kids []json.RawMessage
			if err := json.Unmarshal(child, &kids); err != nil {
				continue
			}
			for i, kid := range kids {
				stack = append(stack, framed{raw: kid, where: fmt.Sprintf("%s.%s[%d]", f.where, key, i)})
			}
		}
		if child, ok := node["not"]; ok {
			stack = append(stack, framed{raw: child, where: f.where + ".not"})
		}
	}
	return nil
}

// filterLiteralMaxDepth bounds the recursive literal walk the same way
// FR-023c bounds the tree itself; a deeper tree is refused rather than
// descended.
const filterLiteralMaxDepth = 64

// normalizeFilterLiterals rewrites one filter node — and, through `all`,
// `any` and `not`, its children — so that every literal reaches the
// generated type in the ONE shape it declares: `value` a string, `values` a
// list of strings (D-11).
//
//   - a JSON number becomes the exact digits the caller wrote (`100000`,
//     `12.50`), taken from the raw bytes and never through a float;
//   - a JSON boolean becomes `true` / `false`;
//   - an ARRAY in `value` is the `IN` operand and is MOVED to `values`,
//     element by element under the same rules — the array is what the
//     operator's own refusal told the caller to send, and there was no field
//     that would take it;
//   - a string is left exactly as it is.
//
// An object, a nested array, or an array given in BOTH `value` and `values`
// is refused by name: none of those has a lexical form a property parser
// could read, and guessing one would be the silent coercion FR-022e forbids.
func normalizeFilterLiterals(raw json.RawMessage, where string, depth int) (json.RawMessage, *RefusalError) {
	if depth > filterLiteralMaxDepth {
		return nil, refuse(problem(generated.UnsupportedParameter,
			fmt.Sprintf("the filter tree at %s is nested more than %d levels deep", where, filterLiteralMaxDepth),
			"flatten the filter; a tree this deep cannot be a real predicate"), nil)
	}
	node, isObject := filterNodeObject(raw)
	if !isObject {
		// Not an object: the generated type's own decode refuses it with the
		// right message, so the bytes pass through unchanged.
		return raw, nil
	}
	changed := false

	for _, key := range []string{"all", "any"} {
		child, ok := node[key]
		if !ok {
			continue
		}
		var kids []json.RawMessage
		if err := json.Unmarshal(child, &kids); err != nil {
			continue
		}
		kidChanged := false
		for i := range kids {
			out, r := normalizeFilterLiterals(kids[i], fmt.Sprintf("%s.%s[%d]", where, key, i), depth+1)
			if r != nil {
				return nil, r
			}
			if string(out) != string(kids[i]) {
				kids[i] = out
				kidChanged = true
			}
		}
		if kidChanged {
			enc, err := json.Marshal(kids)
			if err != nil {
				return nil, refuse(problem(generated.UnsupportedParameter,
					fmt.Sprintf("the filter at %s.%s could not be re-encoded: %v", where, key, err), ""), err)
			}
			node[key] = enc
			changed = true
		}
	}
	if child, ok := node["not"]; ok {
		out, r := normalizeFilterLiterals(child, where+".not", depth+1)
		if r != nil {
			return nil, r
		}
		if string(out) != string(child) {
			node["not"] = out
			changed = true
		}
	}

	if v, ok := node["value"]; ok {
		trimmed := bytes.TrimSpace(v)
		switch {
		case len(trimmed) == 0:
		case trimmed[0] == '"':
			// Already lexical.
		case trimmed[0] == '[':
			if _, both := node["values"]; both {
				return nil, refuse(problem(generated.UnsupportedParameter,
					fmt.Sprintf("the leaf at %s gives a list in both value and values", where),
					"send the IN list in `values` (or in `value`), not both"), nil)
			}
			list, r := normalizeLiteralList(trimmed, where+".value")
			if r != nil {
				return nil, r
			}
			delete(node, "value")
			node["values"] = list
			changed = true
		default:
			lit, r := scalarLiteralText(trimmed, where+".value")
			if r != nil {
				return nil, r
			}
			enc, _ := json.Marshal(lit)
			node["value"] = enc
			changed = true
		}
	}
	if v, ok := node["values"]; ok {
		trimmed := bytes.TrimSpace(v)
		if len(trimmed) > 0 && trimmed[0] == '[' {
			list, r := normalizeLiteralList(trimmed, where+".values")
			if r != nil {
				return nil, r
			}
			if string(list) != string(trimmed) {
				node["values"] = list
				changed = true
			}
		}
	}

	if !changed {
		return raw, nil
	}
	out, err := json.Marshal(node)
	if err != nil {
		return nil, refuse(problem(generated.UnsupportedParameter,
			fmt.Sprintf("the filter node at %s could not be re-encoded: %v", where, err), ""), err)
	}
	return out, nil
}

// normalizeLiteralList turns a JSON array of scalars into a JSON array of
// lexical strings, refusing any element that has no lexical form.
func normalizeLiteralList(raw []byte, where string) (json.RawMessage, *RefusalError) {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, refuse(problem(generated.UnsupportedParameter,
			fmt.Sprintf("the list at %s could not be read: %v", where, err),
			"send a JSON array of values"), err)
	}
	out := make([]string, 0, len(elems))
	for i, e := range elems {
		lit, r := scalarLiteralText(bytes.TrimSpace(e), fmt.Sprintf("%s[%d]", where, i))
		if r != nil {
			return nil, r
		}
		out = append(out, lit)
	}
	enc, err := json.Marshal(out)
	if err != nil {
		return nil, refuse(problem(generated.UnsupportedParameter,
			fmt.Sprintf("the list at %s could not be re-encoded: %v", where, err), ""), err)
	}
	return enc, nil
}

// scalarLiteralText returns the lexical text of one JSON scalar: a string's
// contents, a number's exact digits, a boolean's name.
func scalarLiteralText(raw []byte, where string) (string, *RefusalError) {
	if len(raw) == 0 {
		return "", refuse(problem(generated.LiteralTypeMismatch,
			fmt.Sprintf("the literal at %s is empty", where), "give the value as a string"), nil)
	}
	switch raw[0] {
	case '"':
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return "", refuse(problem(generated.LiteralTypeMismatch,
				fmt.Sprintf("the literal at %s could not be read as a string: %v", where, err), ""), err)
		}
		return str, nil
	case '{', '[':
		return "", refuse(problem(generated.LiteralTypeMismatch,
			fmt.Sprintf("the literal at %s is a %s, which has no lexical form a property can hold",
				where, map[byte]string{'{': "JSON object", '[': "nested list"}[raw[0]]),
			"give each value as a string, a number or a boolean; for IN, a flat list of them"), nil)
	}
	text := string(raw)
	switch text {
	case "true", "false":
		return text, nil
	case "null":
		return "", refuse(problem(generated.LiteralTypeMismatch,
			fmt.Sprintf("the literal at %s is null", where),
			"to match records with no value use the IS NULL operator; to match a value, give it"), nil)
	}
	// A number: json.Valid guarantees the token is a well-formed literal, and
	// its own bytes ARE the exact lexical form — no float in between.
	if !json.Valid(raw) {
		return "", refuse(problem(generated.LiteralTypeMismatch,
			fmt.Sprintf("the literal at %s is not valid JSON", where), ""), nil)
	}
	return text, nil
}

// filterNodeObject reads one filter node as a JSON object, reporting only
// whether it IS one — a non-object is not an error at this layer (the
// generated decoder refuses it by name), so no error is carried.
func filterNodeObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var node map[string]json.RawMessage
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, false
	}
	return node, true
}

// unknownParameterRemedy points at the argument that does the job, for the
// mistakes a model fluent in SQL actually makes.
func unknownParameterRemedy(unknown []string) string {
	for _, u := range unknown {
		switch strings.ToLower(u) {
		case "where", "sql", "query":
			return "express the predicate as a structured filter tree; there is no query language here"
		case "having":
			return "use group_by, then filter on the grouped property"
		case "order_by", "orderby":
			return "use sort"
		case "offset", "page", "skip":
			return "page with cursor, which is returned in the previous reply's next block"
		case "fields", "columns", "properties":
			return "use select"
		}
	}
	return "drop the argument, or call knowledge_describe to see what this knowledge base supports"
}

func quoteAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, `"`+s+`"`)
	}
	return out
}
