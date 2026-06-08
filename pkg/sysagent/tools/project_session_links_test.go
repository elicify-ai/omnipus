// Omnipus — System Agent Tools
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// BDD: project session links tests — linker LRU dedup, ReadLinks, RemoveLinksForProject cascade.
// Traces to: FR-008 (project session linkage), FR-007 (cascade delete of links).

package systools

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadLinks_EmptyDir verifies ReadLinks returns nil on a directory with no link file.
// BDD: Given an empty directory,
// When ReadLinks(tmpDir, "pid") is called,
// Then nil is returned (no error, no panic).
// Traces to: FR-008 — project_session_links_test.go
func TestReadLinks_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	links := ReadLinks(tmpDir, "pid")
	assert.Nil(t, links, "ReadLinks on empty dir must return nil")
}

// TestProjectSessionLinker_LinkAndRead verifies LinkSession persists links readable by ReadLinks.
// BDD: Given a new linker,
// When LinkSession("proj1", "sess1") and LinkSession("proj1", "sess2") are called,
// Then ReadLinks returns 2 entries for proj1.
// Traces to: FR-008 — project_session_links.go
func TestProjectSessionLinker_LinkAndRead(t *testing.T) {
	home := t.TempDir()
	linker := NewProjectSessionLinker(home)

	linker.LinkSession("proj1", "sess1")
	linker.LinkSession("proj1", "sess2")

	links := ReadLinks(home, "proj1")
	require.Len(t, links, 2, "ReadLinks must return 2 entries after 2 distinct LinkSession calls")

	sessionIDs := make([]string, 0, len(links))
	for _, l := range links {
		sessionIDs = append(sessionIDs, l.SessionID)
		assert.Equal(t, "proj1", l.ProjectID, "every link must have project_id=proj1")
		assert.NotEmpty(t, l.CreatedAt, "every link must have a non-empty created_at")
	}
	assert.Contains(t, sessionIDs, "sess1", "ReadLinks must include sess1")
	assert.Contains(t, sessionIDs, "sess2", "ReadLinks must include sess2")
}

// TestProjectSessionLinker_DeduplicatesLinks verifies that calling LinkSession with the same
// pair twice does not create duplicate entries.
// BDD: Given a linker,
// When LinkSession("proj1", "sess1") is called twice,
// Then ReadLinks returns exactly 1 entry.
// Traces to: FR-008 — LRU dedup logic in project_session_links.go
func TestProjectSessionLinker_DeduplicatesLinks(t *testing.T) {
	home := t.TempDir()
	linker := NewProjectSessionLinker(home)

	linker.LinkSession("proj1", "sess1")
	linker.LinkSession("proj1", "sess1") // duplicate — must be suppressed

	links := ReadLinks(home, "proj1")
	require.Len(t, links, 1, "ReadLinks must return exactly 1 entry after 2 duplicate LinkSession calls")
	assert.Equal(t, "sess1", links[0].SessionID)
}

// TestRemoveLinksForProject_Cascade verifies RemoveLinksForProject removes only the specified
// project's links and leaves other projects' links intact.
// BDD: Given links for proj1 and proj2,
// When RemoveLinksForProject(home, "proj1") is called,
// Then ReadLinks(home, "proj1") returns nil,
// And ReadLinks(home, "proj2") returns non-nil (other project untouched).
// Traces to: FR-007 — cascade delete of project session links
func TestRemoveLinksForProject_Cascade(t *testing.T) {
	home := t.TempDir()
	linker := NewProjectSessionLinker(home)

	// Add links for two different projects.
	linker.LinkSession("proj1", "sess-a")
	linker.LinkSession("proj1", "sess-b")
	linker.LinkSession("proj2", "sess-c")

	// Remove only proj1.
	RemoveLinksForProject(home, "proj1")

	// proj1 links must be gone.
	proj1Links := ReadLinks(home, "proj1")
	assert.Nil(t, proj1Links, "proj1 links must be removed after RemoveLinksForProject")

	// proj2 links must survive.
	proj2Links := ReadLinks(home, "proj2")
	require.NotNil(t, proj2Links, "proj2 links must survive RemoveLinksForProject for proj1")
	assert.Len(t, proj2Links, 1, "proj2 must still have its 1 link")
	assert.Equal(t, "sess-c", proj2Links[0].SessionID)
}

// TestRemoveLinksForProject_MissingFile_NoError verifies RemoveLinksForProject is a no-op
// when the link file does not exist (no panic, no error).
// BDD: Given an empty directory,
// When RemoveLinksForProject(tmpDir, "nonexistent") is called,
// Then no panic, no error.
// Traces to: FR-007 — graceful no-op on missing file
func TestRemoveLinksForProject_MissingFile_NoError(t *testing.T) {
	tmpDir := t.TempDir()
	// Must not panic.
	assert.NotPanics(t, func() {
		RemoveLinksForProject(tmpDir, "nonexistent")
	}, "RemoveLinksForProject on empty dir must not panic")
}
