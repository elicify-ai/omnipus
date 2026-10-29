// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// RED pack for show_to_user policy (BS-6.14, SEC-16, FR-019).
// Expected postures come from the chat-links spec, not from the seed maps.
package coreagent_test

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

func TestShowToUser_PolicySeed(t *testing.T) {
	if got := len(coreagent.AllStaticToolNames()); got != 120 {
		t.Errorf("static catalog size = %d, want 120 after show_to_user (BS-6.14 test 12: 119 → 120)", got)
	}
	inCatalog := false
	for _, name := range coreagent.AllStaticToolNames() {
		if name == "show_to_user" {
			inCatalog = true
		}
	}
	if !inCatalog {
		t.Errorf("show_to_user is absent from AllStaticToolNames — required by FR-019")
	}

	if got := tools.AutoApproveClassOf("show_to_user"); got != tools.AutoRuns {
		t.Errorf("AutoApproveClassOf(show_to_user) = %s, want runs (FR-019 AutoRuns)", got)
	}
	for _, name := range tools.FullManifestToolNames() {
		if name == "show_to_user" {
			t.Errorf("show_to_user is in FullManifestToolNames; BS-6.14 says it stays ManifestLazy")
		}
	}
	for _, name := range tools.PreviewedLazyToolNames() {
		if name == "show_to_user" {
			t.Errorf("show_to_user is in PreviewedLazyToolNames; BS-6.14 keeps the 36-name preview list unchanged")
		}
	}
	if got := tools.ToolManifestTier("show_to_user"); got != tools.ManifestLazy {
		t.Errorf("ToolManifestTier(show_to_user) = %v, want ManifestLazy", got)
	}

	custom := coreagent.NewCustomAgentToolsCfg()
	if custom == nil || custom.Builtin.Policies["show_to_user"] != config.ToolPolicyAllow {
		t.Errorf("NewCustomAgentToolsCfg show_to_user = %q, want allow (BS-6.14 fresh custom Main)", custom.Builtin.Policies["show_to_user"])
	}
	worker := coreagent.NewCustomAgentToolsCfg()
	coreagent.RestrictForWorkerTier(worker)
	if worker.Builtin.Policies["show_to_user"] != config.ToolPolicyDeny {
		t.Errorf("worker-tier show_to_user = %q, want deny (BS-6.14 / workerTierDeniedTools)", worker.Builtin.Policies["show_to_user"])
	}

	cfg := config.DefaultConfig()
	if !coreagent.SeedConfig(cfg) {
		t.Fatal("SeedConfig on an empty config must seed")
	}
	wantEffective := map[string]string{
		string(coreagent.IDMia):            "allow",
		string(coreagent.IDJim):            "allow",
		string(coreagent.IDOmnipus):        "deny",
		string(coreagent.IDPlanner):        "deny",
		string(coreagent.IDResearcher):     "deny",
		string(coreagent.IDWorker):         "deny",
		string(coreagent.IDJudge):          "deny",
		string(coreagent.IDPlanSupervisor): "deny",
	}
	for id, want := range wantEffective {
		if got := resolveFor(t, cfg, id, "show_to_user", nil); got != want {
			t.Errorf("effective policy (%s, show_to_user) = %q, want %q (BS-6.14 / SEC-16)", id, got, want)
		}
	}

	// Omnipus persists an explicit deny (D-F2), not a missing key that happens to resolve deny.
	omni := findSeeded(t, cfg, string(coreagent.IDOmnipus))
	if omni.Tools == nil || omni.Tools.Builtin.Policies["show_to_user"] != config.ToolPolicyDeny {
		got := ""
		if omni.Tools != nil {
			got = string(omni.Tools.Builtin.Policies["show_to_user"])
		}
		t.Errorf("omnipus explicit show_to_user = %q, want deny (D-F2 / SEC-16)", got)
	}

	known := map[string]struct{}{}
	for _, name := range coreagent.AllStaticToolNames() {
		known[name] = struct{}{}
	}
	known["show_to_user"] = struct{}{}

	upgraded := config.DefaultConfig()
	delete(upgraded.Sandbox.ToolPolicies, "show_to_user")
	config.ReconcileToolPolicyCeiling(upgraded, known)
	if upgraded.Sandbox.ToolPolicies["show_to_user"] != string(config.ToolPolicyAllow) {
		t.Errorf("reconciled ceiling show_to_user = %q, want allow (BS-6.14 old install)", upgraded.Sandbox.ToolPolicies["show_to_user"])
	}

	operator := config.DefaultConfig()
	operator.Sandbox.ToolPolicies["show_to_user"] = string(config.ToolPolicyDeny)
	config.ReconcileToolPolicyCeiling(operator, known)
	if operator.Sandbox.ToolPolicies["show_to_user"] != string(config.ToolPolicyDeny) {
		t.Errorf("Reconcile overwrote operator deny with %q (BS-6.14)", operator.Sandbox.ToolPolicies["show_to_user"])
	}
}
