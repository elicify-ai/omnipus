package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/daemon"
	"github.com/stretchr/testify/require"
)

func repairChangeEvidence(t *testing.T, path string, change func(map[string]any)) {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(body, &fields))
	change(fields)
	body, err = json.Marshal(fields)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, body, 0o600))
}

func repairReplaceArg(t *testing.T, args []string, flag, value string) []string {
	t.Helper()
	out := append([]string(nil), args...)
	for i, arg := range out {
		if arg == flag {
			require.Less(t, i+1, len(out))
			out[i+1] = value
			return out
		}
	}
	t.Fatalf("test instrument did not find decided flag %s", flag)
	return nil
}

// Each case starts with genuine producer evidence and a complete verified copy;
// a missing earlier precondition cannot masquerade as the intended refusal.
func TestHistoryRepairCommand_GuardedApplyEvidenceRefusalClasses(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	for _, fault := range []string{
		"missing_confirmation", "missing_binding", "missing_receipt", "binding_as_receipt",
		"binding_hash_changed", "binding_instance_id", "receipt_instance_id", "receipt_pid",
		"binding_unknown_field", "receipt_unknown_field", "binding_version", "receipt_version",
		"binding_null", "receipt_null", "binding_pid_zero", "binding_pid_file", "trailing_object",
	} {
		t.Run(fault, func(t *testing.T) {
			f := repairStoppedCLIFixture(t, tool, helper, "stop", 0, false)
			args := f.applyArgs()
			class := "stopped_evidence_unknown"
			switch fault {
			case "missing_confirmation":
				args = args[:len(args)-1]
				class = "arguments"
			case "missing_binding", "missing_receipt":
				flag := "--instance-binding"
				if fault == "missing_receipt" {
					flag = "--stopped-receipt"
				}
				for i, arg := range args {
					if arg == flag {
						args = append(args[:i:i], args[i+2:]...)
						break
					}
				}
				class = "stopped_evidence_missing"
			case "binding_as_receipt":
				args = repairReplaceArg(t, args, "--stopped-receipt", f.binding)
			case "binding_hash_changed":
				body, err := os.ReadFile(f.binding)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(f.binding, append(body, '\n'), 0o600)) // Same object, different actual bytes.
			case "trailing_object":
				body, err := os.ReadFile(f.receipt)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(f.receipt, append(body, []byte("\n{}\n")...), 0o600))
			default:
				path, field, value := f.binding, "instance_id", any("hi1_not_the_captured_instance")
				switch fault {
				case "receipt_instance_id":
					path = f.receipt
				case "receipt_pid":
					path, field, value = f.receipt, "previous_pid", 0
				case "binding_unknown_field":
					field, value = "lock_held", true
				case "receipt_unknown_field":
					path, field, value = f.receipt, "service_stopped", true
				case "binding_version":
					field, value = "schema_version", 2
				case "receipt_version":
					path, field, value = f.receipt, "schema_version", 2
				case "binding_null":
					field, value = "process_start_id", nil
				case "receipt_null":
					path, field, value = f.receipt, "inspection_method", nil
				case "binding_pid_zero":
					field, value = "previous_pid", 0
				case "binding_pid_file":
					field, value = "pid_file", repairEvidencePath(t, "not-gateway.pid")
				}
				repairChangeEvidence(t, path, func(fields map[string]any) { fields[field] = value })
			}
			before, backup := repairTree(t, f.source), repairTree(t, f.backup)
			out := repairRun(t, tool, 2, args...)
			repairRequireClass(t, out, class)
			require.Equal(t, before, repairTree(t, f.source), "failed evidence/argument preflight publishes no session and does not clean PID")
			require.Equal(t, backup, repairTree(t, f.backup))
		})
	}
}

func TestHistoryRepairCommand_GuardedApplyRechecksNativeRootAndSource(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	for _, fault := range []string{"new_real_child", "malformed_pid", "changed_pid_bytes", "root_object_replaced", "canonical_drift"} {
		t.Run(fault, func(t *testing.T) {
			f := repairStoppedCLIFixture(t, tool, helper, "stop", 0, false)
			class := "source_changed"
			var newChild *repairNativeInstance
			switch fault {
			case "new_real_child":
				newChild = repairStartNative(t, helper, f.source)
				class = "instance_running"
			case "malformed_pid":
				require.NoError(t, os.WriteFile(daemon.PIDPath(f.source), []byte("not-a-pid"), 0o600))
				class = "stopped_evidence_unknown"
			case "changed_pid_bytes":
				require.NoError(t, os.WriteFile(daemon.PIDPath(f.source), []byte("0"), 0o600))
				class = "stopped_evidence_unknown"
			case "root_object_replaced":
				oldObject, err := os.Stat(f.source)
				require.NoError(t, err)
				moved := filepath.Join(t.TempDir(), "original-root-object")
				require.NoError(t, os.Rename(f.source, moved))
				repairCopy(t, moved, f.source)
				newObject, err := os.Stat(f.source)
				require.NoError(t, err)
				require.False(t, os.SameFile(oldObject, newObject), "instrument really replaced directory identity")
				require.Equal(t, repairFullCopyFacts(t, moved), repairFullCopyFacts(t, f.source), "bytes/mode/mtime equality alone must not hide replaced root")
			case "canonical_drift":
				require.NoError(t, os.WriteFile(filepath.Join(f.source, "uploads", "évidence.txt"), []byte("changed after verified backup"), 0o600))
			}
			before, backup := repairTree(t, f.source), repairTree(t, f.backup)
			out := repairRun(t, tool, 2, f.applyArgs()...)
			repairRequireClass(t, out, class)
			require.Equal(t, before, repairTree(t, f.source), "actual fresh checks before publication; no PID adoption or canonical repair")
			require.Equal(t, backup, repairTree(t, f.backup))
			if newChild != nil {
				repairStopNative(t, newChild, "stop", 0)
			}
		})
	}
}

func TestHistoryRepairCommand_CaptureCannotAdoptZeroOrActualExitedPID(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	for _, fault := range []string{"zero", "actual_reaped_pid"} {
		t.Run(fault, func(t *testing.T) {
			root := repairCanonicalFixture(t)
			child := repairStartNative(t, helper, root)
			repairStopNative(t, child, "crash", 86)
			if fault == "zero" {
				require.NoError(t, os.WriteFile(daemon.PIDPath(root), []byte("0"), 0o600))
			}
			before := repairTree(t, root)
			output := repairEvidencePath(t, "never-live-binding.json")
			out := repairRun(t, tool, 2, "stopped-receipt", "--capture-instance", "--data-root", root, "--gateway-executable", helper, "--operator", repairTestOperator, "--output", output)
			repairRequireClass(t, out, "stopped_evidence_unknown")
			repairNoEvidence(t, output)
			require.Equal(t, before, repairTree(t, root), "capture must not use mutating daemon.Status to clear bad/stale PID")
		})
	}
}
