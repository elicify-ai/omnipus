package agent

// tool_manifest_adr057_test.go — tests for ADR-057 W10 / unit U17a
// (pkg/agent/tool_manifest.go).
//
// Per the ADR-057 session-unification spec's binding rule 5, every unit's new
// tests go in a NEW file named `<subject>_adr057_test.go`; this file is U17a's
// and covers ONLY the loaded-tool manifest bucket. It pins regression-dataset
// row 7 — "Child's loaded-tool manifest: inherited the parent's bucket → starts
// empty" — at the layer that owns the bucket key.
//
// Binding rule 1 (real state, never a spy): the assertions below drive the real
// markToolsLoaded / sessionLoadedTools pair over a real AgentLoop map and read
// back what is actually in the bucket. Nothing asserts that a function ran.

import "testing"

// newManifestBucketLoop returns the minimal REAL AgentLoop the manifest bucket
// helpers need: they touch only loadedTools and its mutex, so this is the whole
// subject rather than a stand-in for it.
func newManifestBucketLoop() *AgentLoop {
	return &AgentLoop{loadedTools: make(map[string]map[string]bool)}
}

// TestManifestBucket_ChildBucketIsItsOwnNotTheParents pins regression-dataset
// row 7 and the "MUST NOT use a routing session id as a tool-manifest bucket"
// non-behaviour.
//
//	Given  a parent turn that has loaded a lazy tool into its own bucket
//	When   a child turn derives its bucket from ITS OWN transcript session id
//	Then   the child's bucket is empty — it does not inherit the parent's
//	And    what the child loads does not appear in the parent's bucket
//	But    a child that derives its bucket from the PARENT's transcript session
//	       id (the pre-ADR-057 shape, where spawnSubTurn copied
//	       parentTS.transcriptSessionID into the child's processOptions) DOES
//	       see the parent's loaded tools — the negative control that proves this
//	       test discriminates rather than passing on any implementation
func TestManifestBucket_ChildBucketIsItsOwnNotTheParents(t *testing.T) {
	const (
		parentSid = "sess_manifest_parent_11aa"
		childSid  = "sess_manifest_child_22bb"
		// The routing/session key — never a legal bucket while a real
		// transcript session id is available.
		parentSessionKey = "agent:jim:session:" + parentSid
		childSessionKey  = "agent:ava:session:" + childSid
		lazyTool         = "web_fetch"
	)
	if parentSid == childSid {
		t.Fatal("fixture defect: parent and child session ids must be distinct")
	}

	al := newManifestBucketLoop()

	parentBucket := manifestSessionID(parentSid, parentSessionKey)
	childBucket := manifestSessionID(childSid, childSessionKey)
	if parentBucket != parentSid {
		t.Fatalf("the bucket must be the acting session's transcript id; got %q, want %q",
			parentBucket, parentSid)
	}
	if childBucket != childSid {
		t.Fatalf("the bucket must be the acting session's transcript id; got %q, want %q",
			childBucket, childSid)
	}
	if parentBucket == childBucket {
		t.Fatal("two sessions with distinct transcript ids must derive distinct buckets")
	}

	// The parent loads a lazy tool.
	al.markToolsLoaded(parentBucket, []string{lazyTool})
	if !al.sessionLoadedTools(parentBucket)[lazyTool] {
		t.Fatalf("fixture: %q must be loaded in the parent's bucket", lazyTool)
	}

	// Row 7: the child starts empty.
	if got := al.sessionLoadedTools(childBucket); len(got) != 0 {
		t.Errorf("regression row 7: the child's bucket must start EMPTY, got %v — a "+
			"child that inherits the parent's bucket pays the parent's tool-def token "+
			"and latency cost on every delegation", got)
	}

	// And the isolation holds in the other direction too.
	al.markToolsLoaded(childBucket, []string{"send_file"})
	if al.sessionLoadedTools(parentBucket)["send_file"] {
		t.Error("the child's own load must not appear in the parent's bucket")
	}
	if al.sessionLoadedTools(childBucket)[lazyTool] {
		t.Errorf("the parent's load must not appear in the child's bucket")
	}

	// --- Negative control. Reconstruct the PRE-ADR-057 shape: the child's
	// processOptions carried the PARENT's transcript session id. If this does
	// not reproduce the shared bucket, the assertions above are vacuous and
	// would pass against an implementation that ignored its inputs.
	sharedBucket := manifestSessionID(parentSid, childSessionKey)
	if sharedBucket != parentBucket {
		t.Fatalf("negative control is broken: a child carrying the parent's transcript "+
			"id must derive the parent's bucket, got %q want %q", sharedBucket, parentBucket)
	}
	if !al.sessionLoadedTools(sharedBucket)[lazyTool] {
		t.Fatal("negative control is broken: the pre-ADR-057 shape must see the " +
			"parent's loaded tools — if it does not, this test cannot distinguish " +
			"the fix from the bug")
	}
}

// TestManifestBucket_EmptyKeyIsANoOpNotASharedBucket pins the contract the
// manifestSessionID doc comment states: a both-empty derivation yields "", and
// "" is rejected by both the writer and the reader rather than becoming a
// shared unkeyed bucket that every session with no ids would collide in.
func TestManifestBucket_EmptyKeyIsANoOpNotASharedBucket(t *testing.T) {
	al := newManifestBucketLoop()

	if got := manifestSessionID("", ""); got != "" {
		t.Fatalf("manifestSessionID(\"\", \"\") = %q, want \"\"", got)
	}

	al.markToolsLoaded("", []string{"web_fetch"})
	if n := len(al.loadedTools); n != 0 {
		t.Errorf("the empty key must create no bucket at all; loadedTools has %d entries", n)
	}
	if got := al.sessionLoadedTools(""); len(got) != 0 {
		t.Errorf("the empty key must resolve to an empty set, got %v", got)
	}

	// A real session's bucket is unaffected by the empty-key traffic.
	al.markToolsLoaded("sess_real_33cc", []string{"web_fetch"})
	if !al.sessionLoadedTools("sess_real_33cc")["web_fetch"] {
		t.Error("a real bucket must still work alongside rejected empty-key calls")
	}
	if got := al.sessionLoadedTools(""); len(got) != 0 {
		t.Errorf("the empty key must not alias a real bucket, got %v", got)
	}
}
