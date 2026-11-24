//go:build windows

package childenv

import "testing"

// Ruling WU19-win 2: on Windows the allowlist also admits the Windows system
// variables, case-insensitively, and still drops secrets.
func TestAllowedKey_WindowsSystemVariables(t *testing.T) {
	for name, want := range map[string]bool{
		"Path": true, "PATH": true, "SystemRoot": true, "SYSTEMROOT": true, "SystemDrive": true,
		"windir": true, "ComSpec": true, "PATHEXT": true, "TEMP": true, "Tmp": true, "USERPROFILE": true,
		"lc_all": true, "OMNIPUS_MASTER_KEY": false, "OMNIPUS_BEARER_TOKEN": false, "APPDATA": false,
	} {
		if got := AllowedKey(name); got != want {
			t.Errorf("AllowedKey(%q) = %t, want %t", name, got, want)
		}
	}
	if !SameKey("Path", "PATH") {
		t.Error("SameKey must be case-insensitive on Windows")
	}
}
