// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// S2-22 (part): getAgent's revision-read failure branch. GET /agents/{id}
// reads the agent's persisted revision to report it; when that read fails for
// any reason other than "absent", the handler must answer 500 naming the
// revision — never a 200 with a silently empty revision, which would let a
// client send a stale revision back and be refused for the wrong reason.

package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAgent_RevisionReadFailureIsAVisible500(t *testing.T) {
	api := buildExecutorTestAPI(t)
	const agentID = "test-agent"

	// Instrument: the same fixture answers 200 while the record is readable.
	w, ok := getAgent(t, api, agentID)
	require.Equal(t, http.StatusOK, w.Code, "instrument: the fixture agent must be readable first: %s", w.Body.String())
	require.NotEmpty(t, ok.Id)

	// Break the read WITHOUT relying on file permissions: the entity path
	// becomes a DIRECTORY, so os.ReadFile fails with EISDIR for root and
	// non-root alike (chmod 0o000 is ignored by root, which is why a
	// permission-based injection would silently skip on a root runner).
	entityPath := filepath.Join(agentstore.New(api.homePath).Dir(), agentID+".json")
	require.NoError(t, os.RemoveAll(entityPath))
	require.NoError(t, os.Mkdir(entityPath, 0o700))

	w, _ = getAgent(t, api, agentID)
	require.Equal(t, http.StatusInternalServerError, w.Code,
		"a revision that cannot be read is a server failure, not a silent empty revision: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), "could not read agent revision",
		"the 500 must name the failed read")
}
