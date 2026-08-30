// Omnipus — assembling one translated Base view into a
// records.ParseView-shaped YAML file, and the three-way per-base/per-view
// outcome this whole importer exists to report honestly (see doc.go).
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// Outcome is the three-way honesty contract SC-010/US-7 require of every
// imported Base view (and, rolled up, of every Base file).
type Outcome string

const (
	OutcomeConverted           Outcome = "CONVERTED"
	OutcomeConvertedWithLosses Outcome = "CONVERTED WITH NAMED LOSSES"
	OutcomeRefused             Outcome = "REFUSED"
)

// ViewOutcome is one produced (or refused) view.
type ViewOutcome struct {
	BaseRelPath  string
	DisplayName  string
	Status       Outcome
	ResolvedType string
	// Losses names every dropped clause/column/aggregate, in the order
	// found, prefixed with WHERE it came from (`[base outer filter]`,
	// `[view filter]`, `[group_by]`, `[properties]`, `[sort]`,
	// `[aggregates]`) — SC-010's "reported verbatim".
	Losses []string
	// RefusedReason is set only when Status == OutcomeRefused.
	RefusedReason string
	// OutputRelPath is the vault-relative view file path, set only when a
	// file was produced (Converted or ConvertedWithLosses).
	OutputRelPath string
}

// ProducedView is one view file this importer is about to write.
type ProducedView struct {
	RelPath string // vault-relative, under .omnipus-vault/views/
	Bytes   []byte
}

// BaseOutcome is one `.base` file's rolled-up result.
type BaseOutcome struct {
	BaseRelPath   string
	Status        Outcome
	RefusedReason string // set only when Status == OutcomeRefused
	Views         []ViewOutcome
}

// TranslateBase translates every view in one parsed Base file.
func TranslateBase(pb *ParsedBase, baseRelPath string, schemas *SchemaIndex, slugs *SlugRegistry) (BaseOutcome, []ProducedView) {
	outcome := BaseOutcome{BaseRelPath: baseRelPath}
	if len(pb.Views) == 0 {
		outcome.Status = OutcomeRefused
		outcome.RefusedReason = "the base file declares no views at all"
		return outcome, nil
	}

	outerTrans := TranslateFilterTree(pb.Filters)

	var produced []ProducedView
	anyNonRefused := false
	anyLossy := false
	for _, vraw := range pb.Views {
		name, _ := vraw["name"].(string)
		slug := slugs.Slug(baseRelPath, name)
		vo, pv := translateOneView(vraw, outerTrans, baseRelPath, slug, schemas)
		outcome.Views = append(outcome.Views, vo)
		if vo.Status != OutcomeRefused {
			anyNonRefused = true
		}
		if vo.Status == OutcomeConvertedWithLosses {
			anyLossy = true
		}
		if pv != nil {
			produced = append(produced, *pv)
		}
	}

	switch {
	case !anyNonRefused:
		outcome.Status = OutcomeRefused
		outcome.RefusedReason = "every view in this base failed to translate — see each view's own reason below"
	case anyLossy || hasRefusedView(outcome.Views):
		outcome.Status = OutcomeConvertedWithLosses
	default:
		outcome.Status = OutcomeConverted
	}
	return outcome, produced
}

func hasRefusedView(views []ViewOutcome) bool {
	for _, v := range views {
		if v.Status == OutcomeRefused {
			return true
		}
	}
	return false
}

func translateOneView(vraw map[string]any, outer TreeTranslation, baseRelPath, slug string, schemas *SchemaIndex) (ViewOutcome, *ProducedView) {
	name, _ := vraw["name"].(string)
	vo := ViewOutcome{BaseRelPath: baseRelPath, DisplayName: name}

	viewTrans := TranslateFilterTree(vraw["filters"])

	resolvedType, conflict := resolveViewType(viewTrans.TypeLiterals, outer.TypeLiterals)
	if resolvedType == "" {
		vo.Status = OutcomeRefused
		if conflict != "" {
			vo.RefusedReason = "cannot determine one record type: " + conflict
		} else {
			vo.RefusedReason = "no `type == \"...\"` equality found anywhere in this view's own filter or the base's outer filter, so there is no single record type to declare — ViewDef requires exactly one `type:`"
		}
		return vo, nil
	}
	if !schemas.HasType(resolvedType) {
		vo.Status = OutcomeRefused
		vo.RefusedReason = fmt.Sprintf("resolved record type %q has no inferred schema — no note in the vault carries `type: %s`", resolvedType, resolvedType)
		return vo, nil
	}
	vo.ResolvedType = resolvedType

	var losses []string
	for _, l := range outer.Lost {
		losses = append(losses, "[base outer filter] "+l)
	}
	for _, l := range viewTrans.Lost {
		losses = append(losses, "[view filter] "+l)
	}

	allLeaves := make([]RawLeaf, 0, len(outer.Leaves)+len(viewTrans.Leaves))
	allLeaves = append(allLeaves, outer.Leaves...)
	allLeaves = append(allLeaves, viewTrans.Leaves...)

	var filterNodes []*yaml.Node
	for _, rl := range allLeaves {
		node, reason, ok := buildFilterLeafNode(resolvedType, rl, schemas)
		if !ok {
			losses = append(losses, fmt.Sprintf("[filter] %s — %s", describeLeaf(rl), reason))
			continue
		}
		filterNodes = append(filterNodes, node)
	}

	groupBy, groupLosses := translateGroupBy(vraw["groupBy"], resolvedType, schemas)
	losses = append(losses, groupLosses...)

	propsOut, propLosses := translateOrder(vraw["order"], resolvedType, schemas)
	losses = append(losses, propLosses...)

	sortOut, sortLosses := translateSort(vraw["sort"], resolvedType, schemas)
	losses = append(losses, sortLosses...)

	aggOut, aggLosses := translateSummaries(vraw["summaries"], resolvedType, schemas)
	losses = append(losses, aggLosses...)

	pairs := []ordPair{
		{Key: "schema_version", Value: records.SupportedViewVersion},
		{Key: "name", Value: slug},
		{Key: "type", Value: resolvedType},
	}
	if name != "" {
		pairs = append(pairs, ordPair{Key: "label", Value: name})
	}
	if len(filterNodes) > 0 {
		pairs = append(pairs, ordPair{Key: "filters", Value: seq(filterNodes...)})
	}
	if len(groupBy) > 0 {
		pairs = append(pairs, ordPair{Key: "group_by", Value: groupBy})
	}
	if len(sortOut) > 0 {
		pairs = append(pairs, ordPair{Key: "sort", Value: seq(sortOut...)})
	}
	if len(propsOut) > 0 {
		pairs = append(pairs, ordPair{Key: "properties", Value: propsOut})
	}
	if len(aggOut) > 0 {
		pairs = append(pairs, ordPair{Key: "aggregates", Value: seq(aggOut...)})
	}
	pairs = append(pairs, ordPair{Key: "source", Value: baseRelPath})
	if len(losses) > 0 {
		pairs = append(pairs, ordPair{Key: "untranslated", Value: losses})
	}

	top := orderedMap(pairs...)
	bytes, err := marshalDoc(top)
	if err != nil {
		vo.Status = OutcomeRefused
		vo.RefusedReason = fmt.Sprintf("internal: could not render the translated view as YAML: %v", err)
		return vo, nil
	}

	relPath := records.ViewsDirName + "/" + slug + ".yaml"
	vo.OutputRelPath = ".omnipus-vault/" + relPath
	vo.Losses = losses
	if len(losses) > 0 {
		vo.Status = OutcomeConvertedWithLosses
	} else {
		vo.Status = OutcomeConverted
	}
	return vo, &ProducedView{RelPath: relPath, Bytes: bytes}
}

func describeLeaf(rl RawLeaf) string {
	if len(rl.Values) > 0 {
		return fmt.Sprintf("%s %s %v", rl.Property, rl.Op, rl.Values)
	}
	return fmt.Sprintf("%s %s", rl.Property, rl.Op)
}

// resolveViewType finds the single `type == "X"` this view unconditionally
// asserts, preferring the view's own filter over the base's outer filter —
// a view that narrows further (Content.base's per-view `type == "content"`)
// always wins over an outer filter that only narrows to a set (Content.base's
// outer `or: [type == "content", type == "brand-kit"]`, which the OR
// translator never harvests literals from at all — see translate.go).
func resolveViewType(viewLits, outerLits []string) (resolved, conflict string) {
	vd := distinctSorted(viewLits)
	if len(vd) == 1 {
		return vd[0], ""
	}
	if len(vd) > 1 {
		return "", fmt.Sprintf("the view's own filter asserts more than one type (%s)", strings.Join(vd, ", "))
	}
	od := distinctSorted(outerLits)
	if len(od) == 1 {
		return od[0], ""
	}
	if len(od) > 1 {
		return "", fmt.Sprintf("the base's outer filter asserts more than one type (%s) and the view does not narrow it further", strings.Join(od, ", "))
	}
	return "", ""
}

func distinctSorted(ss []string) []string {
	seen := map[string]struct{}{}
	for _, s := range ss {
		seen[s] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func buildFilterLeafNode(recordType string, rl RawLeaf, schemas *SchemaIndex) (*yaml.Node, string, bool) {
	prop, ok := schemas.Lookup(recordType, rl.Property)
	if !ok {
		return nil, fmt.Sprintf("property %q is not declared in the %q schema (never observed on a %s note)", rl.Property, recordType, recordType), false
	}
	if rl.Op == "contains" {
		if prop.Type != records.TypeText && !prop.Many {
			return nil, fmt.Sprintf("`contains` is not defined on %q (declared %s, not many-valued and not text)", rl.Property, prop.Type), false
		}
	} else if rl.Op != "is_absent" && prop.Type == records.TypeText {
		return nil, fmt.Sprintf("operator %q is not defined on text property %q (only contains/is_absent are defined for text)", rl.Op, rl.Property), false
	}

	pairs := []ordPair{{Key: "property", Value: rl.Property}, {Key: "op", Value: rl.Op}}
	if rl.Negate {
		pairs = append(pairs, ordPair{Key: "negate", Value: true})
	}
	if len(rl.Values) > 0 {
		valNodes := make([]*yaml.Node, 0, len(rl.Values))
		for _, raw := range rl.Values {
			vn, reason, ok := buildValueNode(prop, raw)
			if !ok {
				return nil, reason, false
			}
			valNodes = append(valNodes, vn)
		}
		pairs = append(pairs, ordPair{Key: "values", Value: seq(valNodes...)})
	}
	return orderedMap(pairs...), "", true
}

func buildValueNode(prop InferredProperty, raw string) (*yaml.Node, string, bool) {
	switch prop.Type {
	case records.TypeText:
		return orderedMap(ordPair{Key: "type", Value: "text"}, ordPair{Key: "text", Value: raw}), "", true
	case records.TypeEnum:
		for _, v := range prop.EnumValues {
			if records.FoldKey(v) == records.FoldKey(raw) {
				return orderedMap(ordPair{Key: "type", Value: "enum"}, ordPair{Key: "enum", Value: v}), "", true
			}
		}
		return nil, fmt.Sprintf("value %q is not one of %q's declared enum values (%s)", raw, prop.Name, strings.Join(prop.EnumValues, ", ")), false
	case records.TypeDate:
		return orderedMap(ordPair{Key: "type", Value: "date"}, ordPair{Key: "date", Value: raw}), "", true
	case records.TypeInteger:
		return orderedMap(ordPair{Key: "type", Value: "integer"}, ordPair{Key: "integer", Value: raw}), "", true
	case records.TypeDecimal:
		return orderedMap(ordPair{Key: "type", Value: "decimal"}, ordPair{Key: "decimal", Value: raw}), "", true
	case records.TypeRelation, records.TypePerson:
		link := raw
		if w, ok := records.ParseWikilink(raw); ok {
			link = w.Target
		}
		field := "relation"
		if prop.Type == records.TypePerson {
			field = "person"
		}
		ref := orderedMap(ordPair{Key: "link", Value: link}, ordPair{Key: "resolved", Value: false})
		return orderedMap(ordPair{Key: "type", Value: string(prop.Type)}, ordPair{Key: field, Value: ref}), "", true
	}
	return nil, "internal: unknown property type " + string(prop.Type), false
}

func translateGroupBy(raw any, recordType string, schemas *SchemaIndex) (groupBy []string, losses []string) {
	gb, ok := raw.(map[string]any)
	if !ok {
		return nil, nil
	}
	prop, _ := gb["property"].(string)
	dir, _ := gb["direction"].(string)
	if prop == "" {
		return nil, nil
	}
	if strings.HasPrefix(prop, "formula.") {
		return nil, []string{fmt.Sprintf("[group_by] grouping by computed property %q dropped — this vault's `.base` formulas have no representation (no computed/derived property type exists)", prop)}
	}
	if _, ok := schemas.Lookup(recordType, prop); !ok {
		return nil, []string{fmt.Sprintf("[group_by] grouping by %q dropped — not a declared property of %q", prop, recordType)}
	}
	groupBy = []string{prop}
	if dir != "" {
		losses = append(losses, fmt.Sprintf("[group_by] direction %q on %q dropped — group_by has no sort-direction field on the wire (ViewDef.group_by is a bare property list)", dir, prop))
	}
	return groupBy, losses
}

func translateOrder(raw any, recordType string, schemas *SchemaIndex) (props []string, losses []string) {
	ord, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	for _, o := range ord {
		s, _ := o.(string)
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if s == "file.name" || strings.HasPrefix(s, "file.") {
			// The note's own identity/path is always shown regardless of
			// `properties:` — nothing queryable is lost by not naming it.
			continue
		}
		if strings.HasPrefix(s, "formula.") {
			losses = append(losses, fmt.Sprintf("[properties] column %q dropped — computed/formula properties have no representation", s))
			continue
		}
		if _, ok := schemas.Lookup(recordType, s); !ok {
			losses = append(losses, fmt.Sprintf("[properties] column %q dropped — not a declared property of %q", s, recordType))
			continue
		}
		props = append(props, s)
	}
	return props, losses
}

func translateSort(raw any, recordType string, schemas *SchemaIndex) (nodes []*yaml.Node, losses []string) {
	srt, ok := raw.([]any)
	if !ok {
		return nil, nil
	}
	for _, s := range srt {
		sm, ok := s.(map[string]any)
		if !ok {
			continue
		}
		prop, _ := sm["property"].(string)
		dir, _ := sm["direction"].(string)
		if prop == "" {
			continue
		}
		if strings.HasPrefix(prop, "formula.") {
			losses = append(losses, fmt.Sprintf("[sort] sorting by computed property %q dropped — formulas have no representation", prop))
			continue
		}
		if _, ok := schemas.Lookup(recordType, prop); !ok {
			losses = append(losses, fmt.Sprintf("[sort] sorting by %q dropped — not a declared property of %q", prop, recordType))
			continue
		}
		nodes = append(nodes, orderedMap(ordPair{Key: "property", Value: prop}, ordPair{Key: "direction", Value: strings.ToLower(strings.TrimSpace(dir))}))
	}
	return nodes, losses
}

var supportedAggregateOps = map[string]string{"sum": "sum", "min": "min", "max": "max", "count": "count"}

func translateSummaries(raw any, recordType string, schemas *SchemaIndex) (nodes []*yaml.Node, losses []string) {
	summ, ok := raw.(map[string]any)
	if !ok {
		return nil, nil
	}
	for _, prop := range sortedKeys(summ) {
		opRaw, _ := summ[prop].(string)
		op, known := supportedAggregateOps[strings.ToLower(strings.TrimSpace(opRaw))]
		if !known {
			losses = append(losses, fmt.Sprintf("[aggregates] summary %q on %q dropped — unsupported aggregate (only sum/min/max/count exist; there is deliberately no avg)", opRaw, prop))
			continue
		}
		if strings.HasPrefix(prop, "formula.") {
			losses = append(losses, fmt.Sprintf("[aggregates] %s(%s) dropped — formulas have no representation", op, prop))
			continue
		}
		if _, ok := schemas.Lookup(recordType, prop); !ok {
			losses = append(losses, fmt.Sprintf("[aggregates] %s(%s) dropped — not a declared property of %q", op, prop, recordType))
			continue
		}
		nodes = append(nodes, orderedMap(ordPair{Key: "op", Value: op}, ordPair{Key: "property", Value: prop}))
	}
	return nodes, losses
}
