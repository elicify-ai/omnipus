package main

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func repairRequireClass(t *testing.T, output, class string) {
	t.Helper()
	require.Regexp(t, `(^|[^a-zA-Z0-9_])`+regexp.QuoteMeta(class)+`($|[^a-zA-Z0-9_])`, output, "the exact decided refusal class")
}

func repairNoEvidence(t *testing.T, path string) {
	t.Helper()
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "refusal cannot leave a successful evidence output")
}

// Frozen independent Python hashlib/struct derivations of L's five UTF8 strings,
// not observed repair producer output. This also tests the test-side instrument.
func TestHistoryRepairCommand_InstanceBindingCodecOracleVectors(t *testing.T) {
	for _, tc := range []struct {
		binding repairInstanceBinding
		want    string
	}{
		{repairInstanceBinding{DataRoot: "/owned/source", RootIdentity: "dev:7/file:42", PreviousPID: 123, ProcessStartID: "birth-456", ProcessExecutable: "/owned/gateway"}, "hi1_KlJBBaYf62Ye3tjjL_Kd9mNwkf7J_j4vLpymLCpVSo4"},
		{repairInstanceBinding{DataRoot: "/evidence/évidence", RootIdentity: "volume:α/object:β", PreviousPID: 9, ProcessStartID: "native:birth:雪", ProcessExecutable: "/evidence/工具.exe"}, "hi1_3p0g9S56FmLUDxWFti26qKS7SLmucoB1W0IMcYHEn8Y"},
	} {
		require.Equal(t, tc.want, repairExpectedInstanceID(tc.binding), "length prefixes count UTF8 bytes, not runes")
	}
}

func TestHistoryRepairCommand_CaptureBindsActualNativeInstance(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	root := repairCanonicalFixture(t)
	child := repairStartNative(t, helper, root)
	_, first := repairCapture(t, tool, helper, root, child)
	_, repeat := repairCapture(t, tool, helper, root, child)
	require.Equal(t, first.RootIdentity, repeat.RootIdentity, "same actual native directory object")
	require.Equal(t, first.ProcessStartID, repeat.ProcessStartID, "birth identity is stable across captures, not generated success evidence")
	require.Equal(t, first.InstanceID, repeat.InstanceID, "capture time/output path is not part of instance identity")
	repairStopNative(t, child, "stop", 0)
}

func TestHistoryRepairCommand_StoppedReceiptRequiresActualReapedInstance(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	for _, tc := range []struct {
		name, action, state string
		exit                int
	}{{"graceful", "stop", "absent", 0}, {"crash", "crash", "same_stale", 86}} {
		t.Run(tc.name, func(t *testing.T) {
			root := repairCanonicalFixture(t)
			child := repairStartNative(t, helper, root)
			bindingPath, binding := repairCapture(t, tool, helper, root, child)
			refused := repairEvidencePath(t, "running-must-not-be-a-receipt.json")
			before := repairTree(t, root)
			out := repairRun(t, tool, 2, "stopped-receipt", "--data-root", root, "--instance-binding", bindingPath, "--operator", repairTestOperator, "--output", refused)
			repairRequireClass(t, out, "instance_running")
			repairNoEvidence(t, refused)
			require.Equal(t, before, repairTree(t, root), "running refusal never stops/cleans/rewrites the instance")
			repairStopNative(t, child, tc.action, tc.exit)
			repairProduceReceipt(t, tool, root, bindingPath, binding, tc.state)
		})
	}
}

func TestHistoryRepairCommand_CaptureRejectsWrongExecutable(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	root := repairCanonicalFixture(t)
	child := repairStartNative(t, helper, root)
	output := repairEvidencePath(t, "wrong-executable-binding.json")
	before := repairTree(t, root)
	out := repairRun(t, tool, 2, "stopped-receipt", "--capture-instance", "--data-root", root, "--gateway-executable", tool, "--operator", repairTestOperator, "--output", output)
	repairRequireClass(t, out, "stopped_evidence_unknown")
	repairNoEvidence(t, output)
	require.Equal(t, before, repairTree(t, root), "basename/name mismatch never means safe stopped, and never clears PID")
	repairStopNative(t, child, "stop", 0)
}

func TestHistoryRepairCommand_ChangedClaimedBirthRefusesRealLiveProbe(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	root := repairCanonicalFixture(t)
	child := repairStartNative(t, helper, root)
	bindingPath, binding := repairCapture(t, tool, helper, root, child)
	binding.ProcessStartID += "-not-the-native-birth"
	binding.InstanceID = repairExpectedInstanceID(binding)
	body, err := json.Marshal(binding)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bindingPath, body, 0o600))
	output := repairEvidencePath(t, "wrong-birth-receipt.json")
	before := repairTree(t, root)
	out := repairRun(t, tool, 2, "stopped-receipt", "--data-root", root, "--instance-binding", bindingPath, "--operator", repairTestOperator, "--output", output)
	repairRequireClass(t, out, "stopped_evidence_unknown")
	repairNoEvidence(t, output)
	require.Equal(t, before, repairTree(t, root))
	repairStopNative(t, child, "stop", 0)
	// This probes a real birth mismatch; it does not claim the OS reused a PID.
}
