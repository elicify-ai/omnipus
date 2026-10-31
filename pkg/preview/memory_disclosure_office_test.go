package preview

import "testing"

// T-80 / §3.1 D1.4. The source sweep finds no kernel-release/boot-option
// enforcement probe or injection seam in this commissioned revision. Do not
// invent a look-alike test implementation and assert its own output. Keep
// the missing implementation loud until the developer supplies the real seam;
// this same author then replaces these BLOCKED cases with behavioural tests.
// This is an executable missing-implementation finding, NOT a claim that
// result/audit/doctor disclosure has been tested.
func TestPreviewMemoryEnforcementDisclosure(t *testing.T) {
	for _, name := range []string{
		"kernel_4_6_discloses_in_result_audit_doctor",
		"ignore_rlimit_data_Y_discloses_in_result_audit_doctor",
		"kernel_4_7_and_N_has_no_disclosure",
		"unreadable_parameter_assumes_enforcing_and_logs_once",
	} {
		t.Run(name, func(t *testing.T) {
			t.Fatal("BLOCKED: kernel-release/ignore_rlimit_data injection and result/audit/doctor disclosure not implemented — required by office-preview §3.1 D1.4 / T-80")
		})
	}
}
