// Omnipus — DEFECT 3: a knowledge base added to a RUNNING gateway never
// indexes.
//
// The governing principle is already ratified, in
// docs/internal/design/knowledge-index-freshness.md §2:
//
//	The watcher is an optimisation. It is never the source of truth.
//
// Correctness rests on the sweep. But the sweep — KnowledgeLifecycle's
// attachWorkspaceScope — had exactly ONE caller, AttachAllMounts, and
// AttachAllMounts had exactly one caller, startKnowledgeLifecycle at boot.
// Nothing re-swept when a collection appeared later. So the "floor" the
// freshness design names ("startup sweep: anything missed while stopped") was
// the only floor there was, and a vault that arrived while the gateway was up
// fell straight through it: the library API detected it (is_knowledge_base:
// true, a display_name, a collection_id) and every view against it answered
//
//	index_unavailable — "the properties index is not open, so no record can
//	be read" ... remedy: "re-open the vault"
//
// — a remedy naming no action the product offers. The 2026-09-05 UAT measured
// it at 120 s with no recovery on one instance and ~40 s on another; on disk
// the collection had bleve/ and index_format.json (side-effects of a
// query-time OpenIndex) but no properties.db and no manifest.json, both of
// which only reconcile writes, and reconcile is only reachable through an
// attach. A gateway restart cleared it every time.
//
//	Run: CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 \
//	       -run '^TestKnowledgeRuntimeAttach' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/records"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// kraDropVaultIn plants a knowledge base inside a workspace's work tree the
// way an operator does — a directory copy, with the gateway already up and
// nothing in Omnipus told about it. This is the UAT's own reproduction
// (`cp -R <vault> $OMNIPUS_HOME/workspaces/<id>/work/vault`), not a synthetic
// stand-in for it.
func kraDropVaultIn(t *testing.T, home, wsID, name string, files map[string]string) string {
	t.Helper()
	workDir, err := workspace.SafeWorkDir(home, wsID)
	require.NoError(t, err)
	root := filepath.Join(workDir, name)
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".omnipus-vault"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".omnipus-vault", "vault.json"),
		[]byte(`{"display_name":"Dropped In"}`), 0o644))
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	real, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	return real
}

// ---------------------------------------------------------------------------
// 1. The UAT's reproduction, end to end through the real boot and real handlers
// ---------------------------------------------------------------------------

// TestKnowledgeRuntimeAttach_VaultDroppedIntoARunningGatewayIndexesWithoutRestart
// is D3 stated as the thing a person actually does: drop a vault into a
// workspace, ask the product about it, get an answer.
//
// DIES ON: removing the runtime attach and leaving attachWorkspaceScope with
// only its boot-sweep caller.
func TestKnowledgeRuntimeAttach_VaultDroppedIntoARunningGatewayIndexesWithoutRestart(t *testing.T) {
	home := kltHome(t)
	wsID := krbSeedWorkspace(t, home)

	api, _ := krbBoot(t, home)
	kl := krbLifecycleFor(home)
	require.NotNil(t, kl, "precondition: boot published a lifecycle")

	// The gateway is UP, and it swept at boot — over a workspace whose work
	// tree held no collection at all.
	vault := kraDropVaultIn(t, home, wsID, "vault", map[string]string{
		".omnipus-vault/records/dino.yaml": "schema_version: 1\ntype: dino\n" +
			"properties:\n  name:   { type: text }\n  weight: { type: decimal }\n",
		".omnipus-vault/views/all-dinos.yaml": "name: all-dinos\ntype: dino\nlayout: table\n",
		"notes/stegosaurus.md":                "---\ntype: dino\nname: Stegosaurus\nweight: 5000\n---\n# Stegosaurus\n\nPlated, and slow.\n",
	})
	require.NotContains(t, kl.AttachedRoots(), vault,
		"precondition: the boot sweep cannot have attached a collection that did not exist yet")

	// The product's own "is this a knowledge base" call — the one the Library
	// makes, and the one the UAT's setup script polled. It answered true
	// throughout the 120 s of nothing happening.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/library/"+wsID+"/knowledge?path=vault", nil)
	api.HandleLibraryTree(rec, req)
	require.Equalf(t, http.StatusOK, rec.Code, "knowledge detect failed: %s", rec.Body.String())

	var info map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &info))
	require.Equal(t, true, info["is_knowledge_base"],
		"precondition: the file API detects the vault (it always did — that is what made D3 confusing)")

	// THE ASSERTION D3 FAILS: having said the vault exists, the gateway must
	// open its index without being restarted.
	krbEventually(t, "the dropped-in vault to be attached without a restart", func() bool {
		return kl.HoldersFor(vault) > 0
	})
	// The attach is asynchronous by design — a first index of a large
	// collection must not hold a request open — so the reconcile behind it is
	// still in flight here. This waits for it the way every other lifecycle
	// test does. "Without a restart" is the claim; "instantly" is not.
	kl.WaitForAttaches()

	// And the records layer specifically — the half that was dead. `bleve/`
	// and `index_format.json` appeared throughout the UAT as a side-effect of
	// a query-time OpenIndex; `manifest.json` and `properties.db` are written
	// only by reconcile, which only an attach reaches. Their presence is the
	// difference between "a directory exists" and "the vault got indexed".
	indexDir, err := knowledge.IndexDirFor(home, vault)
	require.NoError(t, err)
	krbEventually(t, "the text index manifest to be written by reconcile", func() bool {
		_, err := os.Stat(filepath.Join(indexDir, "manifest.json"))
		return err == nil
	})

	if !records.PropertyIndexAvailable {
		t.Skip("no properties index on this build; the typed half of D3 cannot be shown here")
	}

	// The properties store itself — written only by vaultprops.Sync, which the
	// lifecycle drives only from inside reconcile. Its absence beside a
	// present bleve/ is the exact on-disk signature the UAT recorded: the text
	// index open (a query-time OpenIndex creates it merely by opening) and the
	// records layer never touched.
	krbEventually(t, "the properties store to be written by the records reconcile", func() bool {
		_, err := os.Stat(filepath.Join(indexDir, "properties.db"))
		return err == nil
	})

	// THE END-TO-END ANSWER, over the endpoint that refused throughout the
	// UAT. `index_unavailable` — "the properties index is not open, so no
	// record can be read" — was every view's reply for 120 s. It must now
	// answer on its own, and it must answer with REAL ROWS: a refusal that
	// merely changed its code would be a different silence.
	//
	// This poll is deliberately SLOW and BOUNDED. The endpoint is rate-limited
	// (allowKnowledgeRetrieval), so krbEventually's 20 ms cadence would spend
	// the whole budget on 429s and time out over a working index — which is
	// exactly what happened when this assertion was first written that way.
	collectionID, _ := info["collection_id"].(string)
	require.NotEmpty(t, collectionID, "detection must have named the collection")

	var last gen.ViewResult
	var lastCode int
	for attempt := 0; attempt < 20; attempt++ {
		vw := httptest.NewRecorder()
		api.HandleLibraryTree(vw, httptest.NewRequest(http.MethodGet,
			"/api/v1/library/"+wsID+"/knowledge/view?collection_id="+collectionID+"&view=all-dinos", nil))
		lastCode = vw.Code
		if vw.Code == http.StatusOK &&
			json.Unmarshal(vw.Body.Bytes(), &last) == nil &&
			last.Refusal == nil && len(last.Rows) > 0 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	require.Equal(t, http.StatusOK, lastCode)
	require.Nilf(t, last.Refusal, "the view must answer, not refuse: %+v", last.Refusal)
	require.Len(t, last.Rows, 1, "the one note in the dropped-in vault must be readable as a record")
	require.NotEmpty(t, last.Rows[0].Cells)
	assert.Equal(t, "Stegosaurus", last.Rows[0].Cells[0].Value,
		"and the record's own typed value must come back, not an empty row")
}

// ---------------------------------------------------------------------------
// 2. A workspace created at runtime, whose vault arrives after it
// ---------------------------------------------------------------------------

// TestKnowledgeRuntimeAttach_WorkspaceCreatedAtRuntimeGetsItsScopeSwept covers
// the other half of "at runtime": the workspace itself did not exist when the
// gateway booted, so the boot sweep never named it at all.
func TestKnowledgeRuntimeAttach_WorkspaceCreatedAtRuntimeGetsItsScopeSwept(t *testing.T) {
	home := kltHome(t)
	api, _ := krbBoot(t, home)
	kl := krbLifecycleFor(home)
	require.NotNil(t, kl)

	body, err := json.Marshal(map[string]any{"name": "Made At Runtime", "core_team": []string{"mia"}})
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/workspaces", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	api.HandleWorkspaces(rec, req)
	require.Equalf(t, http.StatusCreated, rec.Code, "workspace create failed: %s", rec.Body.String())

	var created gen.Workspace
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created.Id)

	vault := kraDropVaultIn(t, home, created.Id, "vault", map[string]string{
		"notes/ankylosaur.md": "---\ntype: dino\nname: Ankylosaur\n---\n# Ankylosaur\n",
	})

	detect := httptest.NewRecorder()
	api.HandleLibraryTree(detect, httptest.NewRequest(http.MethodGet,
		"/api/v1/library/"+created.Id+"/knowledge?path=vault", nil))
	require.Equalf(t, http.StatusOK, detect.Code, "knowledge detect failed: %s", detect.Body.String())

	krbEventually(t, "a runtime-created workspace's vault to be attached", func() bool {
		return kl.HoldersFor(vault) > 0
	})
}

// ---------------------------------------------------------------------------
// 3. The sweep stays idempotent — it now runs far more often than at boot
// ---------------------------------------------------------------------------

// TestKnowledgeRuntimeAttach_RepeatedSweepsDoNotDoubleAttach is the dedupe the
// brief asks for, asserted rather than assumed. attachWorkspaceScope funnels
// into acquire, which no-ops for a key already mapped to the same root — but
// once the sweep is reachable from a request path it runs orders of magnitude
// more often than it did, so "it was idempotent when boot called it once" is
// no longer a claim worth leaving untested.
func TestKnowledgeRuntimeAttach_RepeatedSweepsDoNotDoubleAttach(t *testing.T) {
	home := kltHome(t)
	wsID := krbSeedWorkspace(t, home)
	api, _ := krbBoot(t, home)
	kl := krbLifecycleFor(home)
	require.NotNil(t, kl)

	vault := kraDropVaultIn(t, home, wsID, "vault", map[string]string{
		"notes/triceratops.md": "# Triceratops\n",
	})

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		api.HandleLibraryTree(rec, httptest.NewRequest(http.MethodGet,
			"/api/v1/library/"+wsID+"/knowledge?path=vault", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	}
	krbEventually(t, "the vault to attach", func() bool { return kl.HoldersFor(vault) > 0 })
	kl.WaitForAttaches()

	assert.Equal(t, 1, kl.HoldersFor(vault),
		"five sweeps of one unchanged workspace must leave exactly one holder, not five")
	seen := 0
	for _, root := range kl.AttachedRoots() {
		if root == vault {
			seen++
		}
	}
	assert.Equal(t, 1, seen, "the collection must appear once in the attached set")
}
