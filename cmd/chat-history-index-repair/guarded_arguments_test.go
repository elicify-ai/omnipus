package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func repairOmitOperand(t *testing.T, args []string, flag string) []string {
	t.Helper()
	for i, arg := range args {
		if arg == flag {
			require.Less(t, i+1, len(args))
			out := append([]string(nil), args[:i]...)
			return append(out, args[i+2:]...)
		}
	}
	t.Fatalf("test instrument cannot omit absent operand %s", flag)
	return nil
}

func TestHistoryRepairCommand_CaptureStrictFlagsAndOutputSafety(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	root := repairCanonicalFixture(t)
	child := repairStartNative(t, helper, root)
	for _, fault := range []string{
		"duplicate_root", "duplicate_mode", "incompatible_binding", "missing_gateway", "blank_operator",
		"previous_pid_override", "pid_file_override", "instance_id_override", "tool_commit_override",
		"force_override", "assume_stopped_override", "unknown_flag", "output_inside_source",
	} {
		t.Run(fault, func(t *testing.T) {
			output := repairEvidencePath(t, "must-not-exist.json")
			args := []string{"stopped-receipt", "--capture-instance", "--data-root", root, "--gateway-executable", helper, "--operator", repairTestOperator, "--output", output}
			class := "arguments"
			switch fault {
			case "duplicate_root":
				args = append(args, "--data-root", root)
			case "duplicate_mode":
				args = append(args, "--capture-instance")
			case "incompatible_binding":
				args = append(args, "--instance-binding", repairEvidencePath(t, "not-an-allowed-capture-input.json"))
			case "missing_gateway":
				args = repairOmitOperand(t, args, "--gateway-executable")
			case "blank_operator":
				args = repairReplaceArg(t, args, "--operator", " \t ")
			case "previous_pid_override":
				args = append(args, "--previous-pid", "0")
			case "pid_file_override":
				args = append(args, "--pid-file", repairEvidencePath(t, "wrong.pid"))
			case "instance_id_override":
				args = append(args, "--instance-id", "caller-success-stamp")
			case "tool_commit_override":
				args = append(args, "--tool-commit", "caller-success-stamp")
			case "force_override":
				args = append(args, "--force")
			case "assume_stopped_override":
				args = append(args, "--assume-stopped")
			case "unknown_flag":
				args = append(args, "--not-a-repair-flag")
			case "output_inside_source":
				output = filepath.Join(root, "must-not-write-binding.json")
				args = repairReplaceArg(t, args, "--output", output)
				class = "unsafe_path"
			}
			before := repairTree(t, root)
			out := repairRun(t, tool, 2, args...)
			repairRequireClass(t, out, class)
			repairNoEvidence(t, output)
			require.Equal(t, before, repairTree(t, root), "invalid flags/output do not touch source or the actual live PID")
		})
	}
	repairStopNative(t, child, "stop", 0)
}

func TestHistoryRepairCommand_ReceiptCannotInitializeFromAbsentPIDAlone(t *testing.T) {
	tool := repairBinary(t)
	root := repairCanonicalFixture(t)
	output := repairEvidencePath(t, "missing-binding-receipt.json")
	before := repairTree(t, root)
	out := repairRun(t, tool, 2, "stopped-receipt", "--data-root", root, "--operator", repairTestOperator, "--output", output)
	repairRequireClass(t, out, "stopped_evidence_missing")
	repairNoEvidence(t, output)
	require.Equal(t, before, repairTree(t, root), "missing PID file is not an instance capture or native stopped proof")
}

func TestHistoryRepairCommand_GracefulPIDRemovalInvalidatesRunningBackup(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	root, sid := repairMissingIndexFixture(t)
	child := repairStartNative(t, helper, root)
	binding, identity := repairCapture(t, tool, helper, root, child)
	backup, manifest := repairCountVerifiedBackup(t, tool, root) // Deliberately while the registered child still runs.
	repairStopNative(t, child, "stop", 0)
	receipt := repairProduceReceipt(t, tool, root, binding, identity, "absent")
	f := repairGuardedFixture{source: root, backup: backup, manifest: manifest, binding: binding, receipt: receipt, sid: sid}
	before, copyBefore := repairTree(t, root), repairTree(t, backup)
	out := repairRun(t, tool, 2, f.applyArgs()...)
	repairRequireClass(t, out, "source_changed")
	require.Equal(t, before, repairTree(t, root), "do not hide actual shutdown writes behind a PID equality exclusion")
	require.Equal(t, copyBefore, repairTree(t, backup), "stale full backup remains intact, not silently refreshed")
}
