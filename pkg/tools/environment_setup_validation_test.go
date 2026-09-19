// environment_setup_validation_test.go — ES-FR-02 input-contract rejection
// tests for the environment_setup tool (ADR-090 environment setup spec:
// docs/internal/specs/adr-090-environment-setup-spec.md).
//
// Lane ownership (coordinator-notes.md "Parallel validation-test ownership"):
// THIS file only. Table-driven PUBLIC-surface Execute/Parameters tests
// proving invalid input is rejected BEFORE any installation side effect — no
// storage allocation, no filesystem change, no command execution. Lifecycle,
// sandbox and session behaviour belong to the tool lane's own test file.
//
// Oracle: every expected limit is derived from the ES-FR-02 canonical input
// table (command ≤65,536 UTF-8 bytes; purpose nonblank ≤500 characters;
// scope workspace|shared; action run|poll|read|kill; timeout_seconds integer
// 1..3600 default 1800; target_workspace canonical workspace ID, never a
// filesystem path; foreign targets need authority), never from the
// implementation.
package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// validationFakeStore implements the tool's storage seam and REFUSES every
// allocation while counting attempts. Invalid input must never reach
// BeginInstall: a non-zero counter inside a rejection case is a failure —
// the allocation attempt itself is the side effect ES-BDD-03 forbids before
// validation.
type validationFakeStore struct{ began int }

func (s *validationFakeStore) BeginInstall(appDataRoot, workspaceRoot, scope string) (EnvironmentSetupTarget, error) {
	s.began++
	return nil, fmt.Errorf("validationFakeStore: allocation attempted during input-validation test")
}

// envSetupValidationTool builds the tool with the minimal wiring facts per
// lane instruction: isolated temp Home + own workspace dir, non-Admin,
// GodMode/Proxy/AuditFailClosed zero, counting fake Store.
func envSetupValidationTool(t *testing.T) (*EnvironmentSetupTool, *validationFakeStore, string) {
	t.Helper()
	home := t.TempDir()
	work := filepath.Join(home, "workspace-a")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatalf("workspace dir: %v", err)
	}
	store := &validationFakeStore{}
	tool := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{
		Home:         home,
		AgentWorkDir: work,
		Admin:        false,
		Store:        store,
	})
	return tool, store, home
}

// envSetupSnapshotTree records every path and file content under root so a
// test can prove the filesystem is unchanged after a rejected request.
func envSetupSnapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		if d.IsDir() {
			snap[rel+"/"] = ""
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() {
			snap[rel] = "!regular:" + info.Mode().String()
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		snap[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot %s: %v", root, err)
	}
	return snap
}

// envSetupValidRunArgs is the baseline a case mutates: a plausible install
// command that must NEVER execute in this file (every case is rejected
// before allocation, proven by the fake-store counter and sentinel tree).
func envSetupValidRunArgs() map[string]any {
	return map[string]any{
		"command": "printf 'installed' > \"$OMNIPUS_ENV_PREFIX/marker.txt\"",
		"purpose": "install the validation suite fixture helper",
	}
}

// envSetupValidationCases derives from the ES-FR-02 canonical input table.
// wantField: substring the rejection message must contain (right-field
// blame). wantBegin: input is within spec and MUST pass validation into
// allocation (boundary-acceptance probe).
var envSetupValidationCases = []struct {
	name      string
	mutate    func(args map[string]any)
	wantField string
	wantBegin bool
}{
	// command — "Required and nonblank for run ... maximum 65,536 UTF-8
	// bytes; reject oversize input without executing or silently truncating".
	{"rejects missing command", func(a map[string]any) { delete(a, "command") }, "command", false},
	{"rejects empty command", func(a map[string]any) { a["command"] = "" }, "command", false},
	{"rejects whitespace-only command", func(a map[string]any) { a["command"] = " \n\t " }, "command", false},
	{"rejects wrong-type command", func(a map[string]any) { a["command"] = 123 }, "command", false},
	{"rejects oversized command 65537 UTF8 bytes", func(a map[string]any) { a["command"] = strings.Repeat("x", 65537) }, "command", false},

	// purpose — "Required and nonblank for run; maximum 500 characters".
	{"rejects missing purpose", func(a map[string]any) { delete(a, "purpose") }, "purpose", false},
	{"rejects empty purpose", func(a map[string]any) { a["purpose"] = "" }, "purpose", false},
	{"rejects whitespace-only purpose", func(a map[string]any) { a["purpose"] = "   " }, "purpose", false},
	{"rejects wrong-type purpose", func(a map[string]any) { a["purpose"] = true }, "purpose", false},
	{"rejects oversized purpose 501 characters", func(a map[string]any) { a["purpose"] = strings.Repeat("a", 501) }, "purpose", false},

	// scope — "workspace (default) or shared; reject other values".
	{"rejects unknown scope", func(a map[string]any) { a["scope"] = "system" }, "scope", false},
	{"rejects wrong-type scope", func(a map[string]any) { a["scope"] = 123 }, "scope", false},

	// action — "run (default), poll, read, kill".
	{"rejects unknown action", func(a map[string]any) { a["action"] = "restart" }, "action", false},
	{"rejects wrong-type action", func(a map[string]any) { a["action"] = 7 }, "action", false},

	// timeout_seconds — "integer; Default 1800; 1 through 3600".
	{"rejects fractional timeout", func(a map[string]any) { a["timeout_seconds"] = 1800.5 }, "timeout_seconds", false},
	{"rejects zero timeout", func(a map[string]any) { a["timeout_seconds"] = 0 }, "timeout_seconds", false},
	{"rejects negative timeout", func(a map[string]any) { a["timeout_seconds"] = -1 }, "timeout_seconds", false},
	{"rejects timeout above 3600", func(a map[string]any) { a["timeout_seconds"] = 3601 }, "timeout_seconds", false},
	{"rejects string timeout", func(a map[string]any) { a["timeout_seconds"] = "1800" }, "timeout_seconds", false},
	{"rejects bool timeout", func(a map[string]any) { a["timeout_seconds"] = true }, "timeout_seconds", false},

	// target_workspace — "Optional canonical workspace ID ... never a
	// filesystem path"; ES-BDD-03: forged cross-workspace/path requests fail
	// before changes.
	{"rejects path-like target relative traversal", func(a map[string]any) { a["target_workspace"] = "../escape" }, "target_workspace", false},
	{"rejects path-like target absolute host path", func(a map[string]any) { a["target_workspace"] = "/tmp/omnipus-evil" }, "target_workspace", false},
	{"rejects wrong-type target", func(a map[string]any) { a["target_workspace"] = 123 }, "target_workspace", false},
	{"rejects unauthorized cross-workspace target", func(a map[string]any) { a["target_workspace"] = "workspace-b" }, "target_workspace", false},

	// Boundary-acceptance probe: the purpose limit counts CHARACTERS (the
	// spec deliberately says BYTES for command, CHARACTERS for purpose), so
	// exactly 500 multi-byte characters are within spec and must pass input
	// validation. The fake store then refuses the allocation — this file's
	// stand-in for a wired installation area.
	{"accepts purpose of 500 multibyte characters", func(a map[string]any) { a["purpose"] = strings.Repeat("é", 500) }, "", true},
}

// TestEnvironmentSetupValidation runs the ES-FR-02 rejection table against
// the public Execute surface. Every case asserts: IsError, no allocation
// attempt (began counter), byte-identical filesystem sentinel tree, and
// right-field blame in the message.
func TestEnvironmentSetupValidation(t *testing.T) {
	ctx := context.Background()

	for _, tc := range envSetupValidationCases {
		t.Run(tc.name, func(t *testing.T) {
			// Fresh tool/store/sentinel per case: no shared mutable state.
			tool, store, home := envSetupValidationTool(t)
			args := envSetupValidRunArgs()
			tc.mutate(args)

			before := envSetupSnapshotTree(t, home)
			res := tool.Execute(ctx, args)
			after := envSetupSnapshotTree(t, home)

			if !reflect.DeepEqual(before, after) {
				t.Errorf("filesystem changed during request handling: before=%v after=%v", before, after)
			}
			if res == nil {
				t.Fatalf("Execute returned nil result")
			}
			if !res.IsError {
				t.Fatalf("expected IsError=true (fake store refuses allocation), got success: %+v", res)
			}

			if tc.wantBegin {
				if store.began != 1 {
					t.Errorf("valid 500-character purpose did not pass input validation: BeginInstall attempted %d times; result: %s", store.began, res.ForLLM)
				}
				if strings.Contains(res.ForLLM, "purpose") {
					t.Errorf("purpose of exactly 500 multi-byte characters is within the ES-FR-02 limit (500 characters, not bytes) yet the purpose validator rejected it: %s", res.ForLLM)
				}
				return
			}

			if store.began != 0 {
				t.Errorf("invalid input reached installation allocation (BeginInstall attempted %d times) — rejection must happen BEFORE any side effect: %s", store.began, res.ForLLM)
			}
			if tc.wantField != "" && !strings.Contains(res.ForLLM, tc.wantField) {
				t.Errorf("rejection message must name the offending field %q; got: %s", tc.wantField, res.ForLLM)
			}
		})
	}
}

// envSetupSchemaEnum extracts an enum declaration whether authored as
// []string or []any.
func envSetupSchemaEnum(t *testing.T, props map[string]any, field string) []string {
	t.Helper()
	f, ok := props[field].(map[string]any)
	if !ok {
		t.Fatalf("field %q missing from Parameters() properties", field)
	}
	switch e := f["enum"].(type) {
	case []string:
		return e
	case []any:
		out := make([]string, 0, len(e))
		for _, v := range e {
			s, ok := v.(string)
			if !ok {
				t.Fatalf("enum of %q holds a non-string value", field)
			}
			out = append(out, s)
		}
		return out
	default:
		t.Fatalf("field %q declares no enum", field)
		return nil
	}
}

// TestEnvironmentSetupValidationParametersSchema pins the public Parameters()
// declaration to the ES-FR-02 canonical input table — the one schema shared
// by discovery, execution, approval preview and tests.
func TestEnvironmentSetupValidationParametersSchema(t *testing.T) {
	schema := NewEnvironmentSetupTool(EnvironmentSetupToolDeps{}).Parameters()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("Parameters() must declare an object schema with properties; got: %#v", schema)
	}

	for _, field := range []string{"action", "command", "purpose", "scope", "target_workspace", "session_id", "timeout_seconds"} {
		if _, present := props[field]; !present {
			t.Errorf("Parameters() is missing the ES-FR-02 field %q", field)
		}
	}

	if got := envSetupSchemaEnum(t, props, "action"); !reflect.DeepEqual(got, []string{"run", "poll", "read", "kill"}) {
		t.Errorf("action enum must be exactly run|poll|read|kill (ES-FR-02); got: %v", got)
	}
	if got := envSetupSchemaEnum(t, props, "scope"); !reflect.DeepEqual(got, []string{"workspace", "shared"}) {
		t.Errorf("scope enum must be exactly workspace|shared (ES-FR-02); got: %v", got)
	}

	timeout, ok := props["timeout_seconds"].(map[string]any)
	if !ok {
		t.Fatalf("timeout_seconds missing from Parameters() properties")
	}
	if got, _ := timeout["type"].(string); got != "integer" {
		t.Errorf("timeout_seconds must be declared type integer (ES-FR-02); got: %q", got)
	}
}
