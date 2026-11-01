package gateway

import "testing"

// Oracle: tool-parity r17 §3, §12 US-9 and D-6. The complete settings
// endpoint AND its sandbox.third_party_agents.clis configuration are absent
// at dbdd1da6a (implementation-premises-r6.log). These are loud commissioning
// blockers, not passing validation tests and not an invented endpoint mock.
// The one test author must bind the integration fixtures after GREEN adds it.
func TestThirdPartySettings_PathPresetAndPersonOnly(t *testing.T) {
	for _, row := range []struct{ id, want string }{
		{"9.1", "all three rows initially empty; detection distinguishes link_path from resolved path"},
		{"9.2", "whole-section save and read preserve the entered path"},
		{"9.3", "400 cli_path_invalid / clis.codex.path / not_absolute; nothing written"},
		{"9.4", "400 agent_writable_location"},
		{"9.5", "directory, no-exec, world-write file and world-write parent fail their exact checks"},
		{"9.6", "ordinary symlink accepted and stored as entered"},
		{"9.7", "world-write link parent refused with at equal to link"},
		{"9.8", "workspace link in the middle of the chain refused at that link"},
		{"9.20", "preset newline, NUL and 4097 characters refused with cli_preset_invalid"},
		{"9.21", "one invalid CLI row refuses the entire save"},
		{"9.22", "revision is an unknown field, not a stale-save token"},
		{"9.23", "agent/tool token 403; missing session 401 for GET and PUT"},
		{"9.24", "set_config cannot write this person-only section"},
		{"9.26", "detection never launches the planted executable"},
		{"9.27", "one path_changed and preset_changed audit event with old/new values"},
	} {
		t.Run(row.id, func(t *testing.T) {
			t.Fatalf("BLOCKED: GET/PUT /api/v1/settings/third-party-agents and sandbox.third_party_agents.clis not implemented — required by §3 / scenario %s: %s", row.id, row.want)
		})
	}
}
func TestThirdPartySettings_D6_Dataset(t *testing.T) {
	for _, row := range []struct{ id, want string }{
		{"D6.1", "absolute regular executable accepted"}, {"D6.2", "relative path not_absolute"}, {"D6.3", "directory not_regular_file"},
		{"D6.4", "0644 not_executable"}, {"D6.5", "workspace file agent_writable_location"}, {"D6.6", "home file agent_writable_location"},
		{"D6.7", "0757 file writable_by_others"}, {"D6.8", "0777 parent writable_by_others"}, {"D6.9", "ordinary symlink accepted without rejecting its lstat mode"},
		{"D6.10", "relative link target resolves"}, {"D6.11", "world-write link parent refused at link"}, {"D6.12", "workspace middle link refused at middle link"},
		{"D6.13", "dangling link not_found"}, {"D6.14", "link loop not_found"}, {"D6.15", "group-write-only parent accepted"},
		{"D6.16", "empty path saves no program"}, {"D6.17", "4096 checked and 4097 schema-refused"}, {"D6.18", "unknown CLI key schema-refused"},
		{"D6.19", "Windows extension, short-name and stream checks; requires Windows CI"}, {"D6.20", "detection claude maps to claude-code row"},
	} {
		t.Run(row.id, func(t *testing.T) {
			t.Fatalf("BLOCKED: Settings path-validation endpoint not implemented — required by §3 / %s: %s", row.id, row.want)
		})
	}
}
