package childenv

import (
	"runtime"
	"strings"
	"testing"
)

// #1960 S1 security round 2, F-win: on Windows every child's base environment
// also carries the Windows system variables, so a private or program variable
// must not be able to replace one of them. Windows names are case-insensitive;
// the uppercase-only grammar means the uppercase spelling is the one to refuse.
// Outside Windows the behaviour is unchanged.
func TestCheckPrivateVariableName_WindowsSystemNames(t *testing.T) {
	names := []string{"COMSPEC", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "TEMP", "TMP", "USERPROFILE"}
	for _, name := range names {
		err := checkPrivateVariableNameFor(name, true)
		if err == nil {
			t.Errorf("windows: %s accepted, want refused", name)
			continue
		}
		if !strings.Contains(err.Error(), "protected name") {
			t.Errorf("windows: %s refused for the wrong reason: %v", name, err)
		}
		if err := checkPrivateVariableNameFor(name, false); err != nil {
			t.Errorf("non-windows: %s refused, want accepted (Linux and macOS unchanged): %v", name, err)
		}
	}
	// Controls: an ordinary name stays allowed on both, an existing protected
	// name stays refused on both.
	for _, windows := range []bool{true, false} {
		if err := checkPrivateVariableNameFor("MY_API_KEY", windows); err != nil {
			t.Errorf("windows=%t: MY_API_KEY refused: %v", windows, err)
		}
		if err := checkPrivateVariableNameFor("PATH", windows); err == nil {
			t.Errorf("windows=%t: PATH accepted, want refused", windows)
		}
	}
	// The public check follows the platform it runs on.
	err := CheckPrivateVariableName("SYSTEMROOT")
	if runtime.GOOS == "windows" && err == nil {
		t.Error("CheckPrivateVariableName(SYSTEMROOT) accepted on Windows")
	}
	if runtime.GOOS != "windows" && err != nil {
		t.Errorf("CheckPrivateVariableName(SYSTEMROOT) refused outside Windows: %v", err)
	}
}
