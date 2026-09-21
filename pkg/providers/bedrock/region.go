// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package bedrock

import "strings"

// knownGroupPrefixes is the closed set of cross-region inference profile
// prefixes AWS Bedrock recognizes on a model id (issue #800 / Bedrock
// region contract, "Runtime model-id resolution" rule 2). An id already
// carrying one of these is sent verbatim — it is never re-prefixed or
// stripped.
var knownGroupPrefixes = []string{"us.", "eu.", "apac.", "jp.", "au.", "global."}

// ResolveModelID applies the Bedrock region contract's runtime model-id
// resolution to a stored base model id, given the selected region's
// cross-region inference profile group and the model's own
// inference_profiles (both catalog DATA — this function carries no
// hand-typed model names or vendor knowledge):
//
//  1. id starts with "arn:" -> unchanged.
//  2. id already starts with a known group prefix -> unchanged.
//  3. group != "" AND group is present in inferenceProfiles -> group + "." + id.
//  4. Otherwise -> the bare id (on-demand access in the selected region).
//
// "global" is never auto-selected here: this function only ever prefixes
// with the group it is HANDED, and the caller (ResolveRegion, plus the
// catalog's own region list) never resolves a region to group "global" on
// a miss or a default — a "global."-prefixed id only ever reaches this
// function already prefixed by an explicit operator choice (rule 2).
func ResolveModelID(id, group string, inferenceProfiles []string) string {
	if strings.HasPrefix(id, "arn:") {
		return id
	}
	for _, prefix := range knownGroupPrefixes {
		if strings.HasPrefix(id, prefix) {
			return id
		}
	}
	if group == "" {
		return id
	}
	for _, g := range inferenceProfiles {
		if g == group {
			return group + "." + id
		}
	}
	return id
}

// ResolveRegion applies the Bedrock region contract's precedence for a
// provider row's operational AWS region: the row's own setting wins, then
// the AWS_REGION environment variable, then the catalog's own default
// region. The first non-empty value (after TrimSpace) wins.
func ResolveRegion(rowRegion, envRegion, catalogDefaultRegion string) string {
	if r := strings.TrimSpace(rowRegion); r != "" {
		return r
	}
	if r := strings.TrimSpace(envRegion); r != "" {
		return r
	}
	return strings.TrimSpace(catalogDefaultRegion)
}
