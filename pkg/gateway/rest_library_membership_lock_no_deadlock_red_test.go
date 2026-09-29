// Omnipus — FR-VA-038 / TDD row 87: membership lock must not share
// the striped note-write mutex even when two different keys hash to it.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"fmt"
	"path"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/require"
)

func TestMembershipLock_TwoCollidingKeysDoNotDeadlock(t *testing.T) {
	api, _, vault, _ := twoKBWorkspace(t)
	// Derive a DISTINCT key hitting the same stripe using the production pool's
	// Get method, rather than guessing an index or relying on a 1-in-64 hit.
	var stripes task.StripedLock
	membership := vault + "\x00.omnipus-vault/view-membership.json"
	want := stripes.Get(membership)
	var colliding string
	for i := range 4096 {
		candidate := fmt.Sprintf("collision-%d.md", i)
		if stripes.Get(vault+"\x00"+path.Clean(candidate)) == want {
			colliding = candidate
			break
		}
	}
	require.NotEmpty(t, colliding, "the test must actually select a colliding note key")
	require.NotEqual(t, ".omnipus-vault/view-membership.json", colliding)

	// With an incorrectly reused striped lock, the membership acquisition
	// times out inside the note-lock callback instead of returning successfully.
	err := knowledge.WithNoteWriteLock(knowledge.NoteLockConfig{CollectionRoot: vault}, colliding,
		func() error {
			return knowledge.WithViewMembershipLock(api.homePath, vault, func() error { return nil })
		})
	require.NoError(t, err, "two different keys in one note stripe must not deadlock the exact-key membership lock")
}
