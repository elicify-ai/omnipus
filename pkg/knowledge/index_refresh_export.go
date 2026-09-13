// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import "context"

// RefreshIndexesForNote is the exported face of refreshIndexesForNote
// (author.go) for write doors that live OUTSIDE this package — today the
// gateway's REST record door (pkg/gateway/rest_knowledge_record.go).
//
// WHY IT EXISTS (UAT 2026-09-13, D-67). The agent door (knowledge_edit) calls
// refreshIndexesForNote after every landed write, so an agent's edit is
// visible in every view at once. The web door called nothing: a cell edit in a
// base view landed on disk, the response carried the new version token, and
// the properties index — the store every view answer is served from — kept
// the OLD value until the gateway restarted. Two doors, two freshness
// guarantees, and the human one was the broken one.
//
// The contract is refreshIndexesForNote's, unchanged: the note is already
// correct on disk, so a failure here is logged loudly and returned as one
// sentence for the caller to surface; it is never a refusal of the write.
func RefreshIndexesForNote(ctx context.Context, home, collectionRoot, relPath string) string {
	return refreshIndexesForNote(ctx, home, collectionRoot, relPath)
}
