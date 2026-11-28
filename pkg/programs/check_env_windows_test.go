//go:build windows

package programs

import (
	"strings"
	"testing"
)

// Ruling WU19-win 2: on Windows the account check keeps Path (any case),
// SystemRoot and the temp folders, replaces USERPROFILE with the temporary
// home and drops gateway secrets.
func TestCheckEnv_WindowsKeepsSystemVariables(t *testing.T) {
	t.Setenv("Path", `C:\Windows\system32`)
	t.Setenv("SystemRoot", `C:\Windows`)
	t.Setenv("TEMP", `C:\Temp`)
	t.Setenv("USERPROFILE", `C:\Users\someone`)
	t.Setenv("OMNIPUS_MASTER_KEY", "check-env-synthetic-secret")
	env := checkEnv(ProgramEntry{TokenVariables: []string{"GH_TOKEN"}}, "tok", `C:\check-home`)
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, want := range []string{"SystemRoot=", "TEMP=", "\nHOME=C:\\check-home\n", "\nUSERPROFILE=C:\\check-home\n", "\nGH_TOKEN=tok\n"} {
		if !strings.Contains(joined, want) {
			t.Errorf("account check env lacks %q", want)
		}
	}
	if !strings.Contains(strings.ToUpper(joined), "\nPATH=") {
		t.Error("account check env lacks Path")
	}
	for _, banned := range []string{"OMNIPUS_MASTER_KEY", `C:\Users\someone`} {
		if strings.Contains(joined, banned) {
			t.Errorf("account check env carries %q", banned)
		}
	}
}
