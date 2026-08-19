// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

func modelOpts(t *testing.T, model string) SandboxApplyOptions {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Sandbox.Mode = config.SandboxModeOff // no kernel apply; this test is about reporting
	cfg.Sandbox.FilesystemModel = model
	return SandboxApplyOptions{
		Cfg:      cfg,
		HomePath: t.TempDir(),
		GetEnv:   func(string) string { return "" },
	}
}

// TestApplySandbox_ReportsModelEvenWhenSandboxIsOff is spec FR-7.1/7.2, and it
// targets the exit path most likely to drop the field.
//
// mode=off returns long before any policy is computed. An operator debugging
// "why can this agent read that file" needs to know which model is configured
// precisely on the paths where nothing was applied — and an empty model reads
// as "confined" to every consumer, so a drop here is a silent wrong answer
// rather than a visible gap.
func TestApplySandbox_ReportsModelEvenWhenSandboxIsOff(t *testing.T) {
	for _, model := range []string{"confined", "open"} {
		t.Run(model, func(t *testing.T) {
			result, err := applySandbox(modelOpts(t, model))
			if err != nil {
				t.Fatalf("applySandbox: %v", err)
			}
			if got := string(result.ApplyState.FilesystemModel); got != model {
				t.Errorf("ApplyState.FilesystemModel = %q, want %q "+
					"(mode=off returns before the policy is built; the model must be stamped anyway)", got, model)
			}
		})
	}
}

// TestApplySandbox_RejectsUnknownModel: a typo must abort boot, not resolve to
// a posture the operator never chose.
func TestApplySandbox_RejectsUnknownModel(t *testing.T) {
	_, err := applySandbox(modelOpts(t, "opne"))
	if err == nil {
		t.Fatal("an unknown filesystem_model must abort boot")
	}
	for _, want := range []string{"confined", "open"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must name the valid value %q so the operator can fix it", err, want)
		}
	}
}

// TestApplySandbox_DefaultModelIsOpen pins the shipped posture.
//
// This test previously asserted "confined", which was correct while ADR-062 was
// landing inert so the "confined is unchanged" guarantee could be measured
// against an untouched baseline. That gate has passed and the operator decided
// open is the default for every install, upgrading ones included.
//
// It is kept rather than deleted because the default is now a DECISION with a
// posture consequence — existing installs pick it up on their next boot — and a
// decision of that weight should not be reversible by an unnoticed edit to a
// defaults file.
func TestApplySandbox_DefaultModelIsOpen(t *testing.T) {
	cfg := config.DefaultConfig()
	if got := cfg.Sandbox.FilesystemModel; got != string(config.FilesystemModelOpen) {
		t.Fatalf("seeded filesystem_model = %q, want %q — this is the change that makes agents "+
			"able to run installed toolchains; without it ADR-062 has no effect at all",
			got, config.FilesystemModelOpen)
	}
}

// TestApplySandbox_ModelReachesTheStatusEndpoint closes the loop: the value has
// to survive into what DescribeBackendWithState actually returns, not merely
// exist on the intermediate struct.
func TestApplySandbox_ModelReachesTheStatusEndpoint(t *testing.T) {
	result, err := applySandbox(modelOpts(t, "open"))
	if err != nil {
		t.Fatalf("applySandbox: %v", err)
	}
	status := sandbox.DescribeBackendWithState(result.Backend, result.ApplyState)
	if status.FilesystemModel != sandbox.FilesystemModelOpen {
		t.Errorf("status.FilesystemModel = %q, want open", status.FilesystemModel)
	}
}
