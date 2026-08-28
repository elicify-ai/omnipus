// Omnipus — ADR-068 D15.3 / spec 4.1.2: the agent-facing surface of vault_find.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultfind

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
)

// ToolName is the one retrieval tool. There is no second.
const ToolName = "vault_find"

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
const Description = `Search the vault — one call for plain words, typed filters, saved views, relations, and tasks.

The loop: call vault_describe first (property names are declared per record type, and a guessed one is refused rather than silently empty). Use a saved view when one fits. Start with words; when more than a screenful matches, narrow with filter instead of paging. Then vault_read the winners.

Every answer opens with its completeness verdict, names each record it could not evaluate and the fix for it, and ends with the calls to make next. An unknown property, operator or value is refused with the valid ones listed — never as zero results, so an empty answer means the vault is empty.`

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
				"description": "Default note. Use task to get CHECKBOX LINES rather than notes — each row " +
					"carries its path, line number, open/done and text.",
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
				"description": "A note path or [[wikilink]]. Restricts the answer to notes within hops link " +
					"steps of it, and composes with words and filter.",
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
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"maxItems":    MaxGroupLevels,
				"description": "Up to two levels. A record holding several values appears in every group it belongs to.",
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
						"op":       map[string]any{"type": "string", "enum": []string{"count", "sum", "min", "max"}},
						"property": map[string]any{"type": "string"},
					},
					"required": []string{"op"},
				},
				"description": "Totals over the full evaluated set, never the page shown. Each states its own scope.",
			},
			"explain": map[string]any{
				"type":        "boolean",
				"description": "Report the plan and evaluate nothing. Use it to check a query before running it over a large vault.",
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
		return Render(refusalResponse(generated.VaultFindRequest{}, "", r)), r
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

	if err := json.Unmarshal(raw, &req); err != nil {
		return req, refuse(problem(generated.UnsupportedParameter,
			fmt.Sprintf("the arguments did not match the expected shape: %v", err),
			"check the argument types; call vault_describe if you are unsure what a property holds"), err)
	}
	return req, nil
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
	return "drop the argument, or call vault_describe to see what this vault supports"
}

func quoteAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, `"`+s+`"`)
	}
	return out
}
