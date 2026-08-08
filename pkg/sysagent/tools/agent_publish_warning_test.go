// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// agent_publish_warning_test.go — regression coverage for fix-wave finding
// #3: create_agent/update_agent/delete_agent returned an UNQUALIFIED success
// payload (e.g. {"deleted":true}) even when the live-publish step
// (UpsertAgentFastFunc / ReloadFunc) failed — leaving the calling agent
// believing routing/registry state matches the just-persisted entity change
// when it does not (an agent that still 404s on delegate, a delete that
// still routes, an update whose old config keeps serving) until the next
// restart or reload. The entity-store write itself always succeeds in these
// tests (proven directly against agentstore below), so the tool call is
// correctly still reported as a SUCCESS — but the response now carries a
// "publish_warning" string field naming the failure, mirroring
// pkg/tools/task.go's update_task advance_warning field.
//
// New file (does not modify agent_test.go or agent_fast_upsert_test.go) so
// this lands cleanly alongside any other in-flight work on those files.
package systools_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
)

// newPublishWarningDeps builds Deps rooted at a real temp Home (so entity
// records and workspace files are real, on-disk, agentstore-readable state)
// with caller-supplied ReloadFunc/UpsertAgentFastFunc — letting each test
// inject a failure on exactly the publish step under test while every other
// step (entity persistence, workspace files) proceeds for real.
func newPublishWarningDeps(t *testing.T, reloadFunc func() error, upsertFastFunc func(string) error) *systools.Deps {
	t.Helper()
	home := t.TempDir()
	cfg := config.DefaultConfig()
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	return &systools.Deps{
		Home:                home,
		GetCfg:              getCfg,
		MutateConfig:        testMutateConfig(&mu, getCfg),
		SaveConfigLocked:    func(*config.Config) error { return nil },
		ReloadFunc:          reloadFunc,
		UpsertAgentFastFunc: upsertFastFunc,
	}
}

func TestAgentCreate_UpsertFastFuncFailure_StillSucceeds_WithPublishWarning(t *testing.T) {
	deps := newPublishWarningDeps(t, nil, func(string) error {
		return errors.New("registry busy")
	})
	tool := systools.NewAgentCreateTool(deps)

	result := tool.Execute(context.Background(), map[string]any{
		"name":        "Publish Warning Create Agent",
		"description": "regression fixture for fix-wave finding #3",
		"soul":        "You are a test agent.",
		"model":       "test/model",
	})
	if result.IsError {
		t.Fatalf("expected success (entity persisted) even though the fast publish failed, got error: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)

	id, _ := body["id"].(string)
	if id != "publish-warning-create-agent" {
		t.Fatalf("id = %q, want the expected slug", id)
	}

	warning, ok := body["publish_warning"].(string)
	if !ok || warning == "" {
		t.Fatalf("expected a non-empty publish_warning field naming the fast-publish failure, got body: %s", result.ForLLM)
	}
	if !strings.Contains(warning, "registry busy") {
		t.Fatalf("publish_warning must name the underlying error, got: %q", warning)
	}
	if !strings.Contains(warning, id) {
		t.Fatalf("publish_warning must name the affected agent id, got: %q", warning)
	}

	// Prove the success is real, not fabricated: the entity record actually
	// exists on disk via the real agentstore, independent of the tool's own
	// response.
	if _, err := agentstore.New(deps.Home).Get(id); err != nil {
		t.Fatalf("agent entity record must exist on disk despite the publish failure: %v", err)
	}
}

func TestAgentCreate_ReloadFuncFailure_StillSucceeds_WithPublishWarning(t *testing.T) {
	// UpsertAgentFastFunc nil forces the ReloadFunc branch (mirrors
	// production wiring when the fast path is unavailable).
	deps := newPublishWarningDeps(t, func() error {
		return errors.New("config reload failed: disk full")
	}, nil)
	tool := systools.NewAgentCreateTool(deps)

	result := tool.Execute(context.Background(), map[string]any{
		"name":        "Publish Warning Reload Agent",
		"description": "regression fixture for fix-wave finding #3",
		"soul":        "You are a test agent.",
		"model":       "test/model",
	})
	if result.IsError {
		t.Fatalf("expected success even though hot-reload failed, got error: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	warning, ok := body["publish_warning"].(string)
	if !ok || warning == "" {
		t.Fatalf("expected a non-empty publish_warning field naming the reload failure, got body: %s", result.ForLLM)
	}
	if !strings.Contains(warning, "disk full") {
		t.Fatalf("publish_warning must name the underlying error, got: %q", warning)
	}
}

func TestAgentCreate_PublishSucceeds_NoWarningField(t *testing.T) {
	// Regression guard against the opposite failure mode: the field must
	// NOT appear at all on the ordinary happy path.
	deps := newPublishWarningDeps(t, func() error { return nil }, nil)
	tool := systools.NewAgentCreateTool(deps)

	result := tool.Execute(context.Background(), map[string]any{
		"name":        "No Warning Agent",
		"description": "regression fixture for fix-wave finding #3",
		"soul":        "You are a test agent.",
		"model":       "test/model",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	if _, present := body["publish_warning"]; present {
		t.Fatalf("publish_warning must be absent when the publish step succeeds, got body: %s", result.ForLLM)
	}
}

func TestAgentUpdate_ReloadFuncFailure_StillSucceeds_WithPublishWarning(t *testing.T) {
	// Create for real first (with a working publish) so update_agent has a
	// genuine target — only the UPDATE's own publish step is made to fail.
	createDeps := newPublishWarningDeps(t, func() error { return nil }, nil)
	createResult := systools.NewAgentCreateTool(createDeps).Execute(context.Background(), map[string]any{
		"name":        "Publish Warning Update Target",
		"description": "regression fixture for fix-wave finding #3",
		"soul":        "original soul",
		"model":       "test/model",
	})
	if createResult.IsError {
		t.Fatalf("setup create failed: %s", createResult.ForLLM)
	}
	created := parseSuccess(t, createResult.ForLLM)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("setup create returned no id: %s", createResult.ForLLM)
	}

	updateDeps := newPublishWarningDeps(t, func() error {
		return errors.New("hot-reload panic recovered")
	}, nil)
	updateDeps.Home = createDeps.Home // same on-disk entity store as the setup create

	result := systools.NewAgentUpdateTool(updateDeps).Execute(context.Background(), map[string]any{
		"id":   id,
		"name": "Publish Warning Update Target (renamed)",
	})
	if result.IsError {
		t.Fatalf("expected success (entity updated) even though hot-reload failed, got error: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	warning, ok := body["publish_warning"].(string)
	if !ok || warning == "" {
		t.Fatalf("expected a non-empty publish_warning field naming the reload failure, got body: %s", result.ForLLM)
	}
	if !strings.Contains(warning, "hot-reload panic recovered") {
		t.Fatalf("publish_warning must name the underlying error, got: %q", warning)
	}

	// Prove the update itself really landed on disk.
	rec, err := agentstore.New(updateDeps.Home).Get(id)
	if err != nil {
		t.Fatalf("updated entity record must exist: %v", err)
	}
	if rec.Name != "Publish Warning Update Target (renamed)" {
		t.Fatalf("entity Name = %q, want the renamed value — the update itself must persist despite the "+
			"publish warning", rec.Name)
	}
}

func TestAgentDelete_ReloadFuncFailure_StillSucceeds_WithPublishWarning(t *testing.T) {
	createDeps := newPublishWarningDeps(t, func() error { return nil }, nil)
	createResult := systools.NewAgentCreateTool(createDeps).Execute(context.Background(), map[string]any{
		"name":        "Publish Warning Delete Target",
		"description": "regression fixture for fix-wave finding #3",
		"soul":        "original soul",
		"model":       "test/model",
	})
	if createResult.IsError {
		t.Fatalf("setup create failed: %s", createResult.ForLLM)
	}
	created := parseSuccess(t, createResult.ForLLM)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("setup create returned no id: %s", createResult.ForLLM)
	}

	deleteDeps := newPublishWarningDeps(t, func() error {
		return errors.New("registry rebuild failed")
	}, nil)
	deleteDeps.Home = createDeps.Home

	result := systools.NewAgentDeleteTool(deleteDeps).Execute(context.Background(), map[string]any{
		"id":      id,
		"confirm": true,
	})
	if result.IsError {
		t.Fatalf("expected success (entity deleted) even though hot-reload failed, got error: %s", result.ForLLM)
	}
	body := parseSuccess(t, result.ForLLM)
	if deleted, _ := body["deleted"].(bool); !deleted {
		t.Fatalf("expected deleted=true, got body: %s", result.ForLLM)
	}
	warning, ok := body["publish_warning"].(string)
	if !ok || warning == "" {
		t.Fatalf("expected a non-empty publish_warning field naming the reload failure, got body: %s", result.ForLLM)
	}
	if !strings.Contains(warning, "registry rebuild failed") {
		t.Fatalf("publish_warning must name the underlying error, got: %q", warning)
	}

	// Prove the delete itself really landed on disk.
	if _, err := agentstore.New(deleteDeps.Home).Get(id); err == nil {
		t.Fatalf("entity record must be gone from disk despite the publish warning")
	}
}
