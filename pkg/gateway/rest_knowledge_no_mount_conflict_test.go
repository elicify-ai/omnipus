// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-067 FR-026a — the mount conflict belongs to the operation that MOUNTS,
// and to no other.
//
// GET /library/{workspace_id}/knowledge is the DETECTION endpoint: it reports
// whether a folder looks like a knowledge base. It mounts nothing and changes
// nothing, so it has no way to produce a mount conflict — yet it documented a
// 409 KnowledgeMountConflictError.
//
// That is worse than dead documentation, because it states the wrong RULE.
// FR-026 constrains what ONE knowledge base is (exactly one folder); it does
// not cap how many a workspace may hold, and knowledge.ResolveScope
// deliberately supports several. A client written against that 409 would treat
// an ordinary second collection as an error.
//
// Two tests, because the defect has two halves:
//   - the BEHAVIOUR: a workspace with two knowledge bases answers 200 for both;
//   - the CONTRACT: the operation no longer declares a response it cannot send.
//
// The second is a source-reading guard on purpose. A comment cannot stop a
// merge from re-adding the block, and this repository has had exactly that
// happen to deleted surfaces before.

package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
)

// TestKnowledgeInfo_TwoKnowledgeBasesInOneWorkspaceIsNotAConflict_FR026a —
// the behaviour FR-026a protects.
//
// DIES ON: making detection refuse, 409, or otherwise fail the second
// collection in a workspace.
func TestKnowledgeInfo_TwoKnowledgeBasesInOneWorkspaceIsNotAConflict_FR026a(t *testing.T) {
	api, ws := buildLibraryTestAPI(t)
	work := workDir(api, ws)

	first := filepath.Join(work, "vault")
	makeKnowledgeBase(t, first, "First vault")
	writeNote(t, first, "a.md", "# A\n")

	second := filepath.Join(work, "research")
	makeKnowledgeBase(t, second, "Second vault")
	writeNote(t, second, "b.md", "# B\n")

	firstW := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge?path=vault")
	require.Equal(t, http.StatusOK, firstW.Code, firstW.Body.String())
	firstInfo := decodeJSON[gen.KnowledgeBaseInfo](t, firstW)
	require.True(t, firstInfo.IsKnowledgeBase)

	secondW := knowledgeGet(t, api, "/api/v1/library/"+ws+"/knowledge?path=research")
	require.NotEqual(t, http.StatusConflict, secondW.Code,
		"a workspace may hold more than one knowledge base. FR-026 says what ONE "+
			"knowledge base is — exactly one folder — not how many a workspace may "+
			"have, and knowledge.ResolveScope supports several. Refusing the second "+
			"here would break an ordinary, legitimate setup")
	require.Equal(t, http.StatusOK, secondW.Code, secondW.Body.String())
	secondInfo := decodeJSON[gen.KnowledgeBaseInfo](t, secondW)
	require.True(t, secondInfo.IsKnowledgeBase,
		"the second folder is a knowledge base and detection must say so")

	require.NotNil(t, firstInfo.CollectionId)
	require.NotNil(t, secondInfo.CollectionId)
	assert.NotEqual(t, *firstInfo.CollectionId, *secondInfo.CollectionId,
		"two folders are two collections; collapsing them is the merge FR-026 forbids")
}

// TestContract_DetectionOperationDeclaresNoMountConflict_FR026a — the contract
// half. The detection operation must not document a 409 it cannot produce.
//
// It reads contracts/openapi.yaml rather than the generated Go, because the
// spec is the source of truth and the generated types are downstream of it.
//
// DIES ON: re-adding the `'409'` response block to getKnowledgeBaseInfo — which
// is how it would come back, since a merge from a branch cut before the removal
// re-adds it as an ordinary, conflict-free addition.
func TestContract_DetectionOperationDeclaresNoMountConflict_FR026a(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "contracts", "openapi.yaml"))
	require.NoError(t, err, "the contract is the source of truth and must be readable")

	// Path items mix operations with a sibling `parameters:` sequence, so each
	// entry is decoded lazily and anything that is not an operation is skipped.
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	type operation struct {
		OperationID string               `yaml:"operationId"`
		Responses   map[string]yaml.Node `yaml:"responses"`
	}

	var found bool
	for path, methods := range doc.Paths {
		for method, node := range methods {
			if node.Kind != yaml.MappingNode {
				continue
			}
			var op operation
			if err := node.Decode(&op); err != nil {
				continue
			}
			if op.OperationID != "getKnowledgeBaseInfo" {
				continue
			}
			found = true
			_, has409 := op.Responses["409"]
			assert.Falsef(t, has409,
				"%s %s (getKnowledgeBaseInfo) declares a 409. That operation MOUNTS "+
					"NOTHING — it reports whether a folder looks like a knowledge base — "+
					"so it can never send one, and documenting it states the wrong rule: "+
					"a client would treat a workspace's ordinary second knowledge base as "+
					"an error (FR-026a). The mount conflict belongs to the operation that "+
					"mounts, where Collection.AttachRoot enforces it",
				method, path)

			// Positive control: this test found a real operation with real
			// responses, so the assertion above was not vacuous.
			assert.NotEmpty(t, op.Responses,
				"getKnowledgeBaseInfo parsed with no responses at all — the parse is "+
					"wrong, not the contract")
			_, has200 := op.Responses["200"]
			assert.True(t, has200, "getKnowledgeBaseInfo must still answer 200")
		}
	}
	require.True(t, found,
		"operationId getKnowledgeBaseInfo not found in contracts/openapi.yaml; this "+
			"guard is silently checking nothing")
}
