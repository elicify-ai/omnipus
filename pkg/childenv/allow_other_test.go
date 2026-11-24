//go:build !windows

package childenv

import "testing"

// Ruling WU19-win 2: Linux and macOS keep the exact-case allowlist; the
// Windows system variables stay out.
func TestAllowedKey_NonWindowsUnchanged(t *testing.T) {
	for name, want := range map[string]bool{
		"PATH": true, "HOME": true, "TMPDIR": true, "LC_ALL": true,
		"Path": false, "SystemRoot": false, "ComSpec": false, "USERPROFILE": false,
		"TEMP": false, "windir": false, "OMNIPUS_MASTER_KEY": false, "home": false,
	} {
		if got := AllowedKey(name); got != want {
			t.Errorf("AllowedKey(%q) = %t, want %t", name, got, want)
		}
	}
	if SameKey("Path", "PATH") {
		t.Error("SameKey must be exact-case outside Windows")
	}
}
