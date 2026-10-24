package wecom

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// TestMain runs the package under a throwaway home (Train 2 gate r1 side
// finding): NewChannel's route store resolves "$HOME/.omnipus/wecom/..." and
// loads it, so without this every test constructing a channel read the
// developer's real data dir. OMNIPUS_HOME (which pkg/media writes under) gets
// its own throwaway directory beside it, so an OMNIPUS_HOME exported by the
// developer's shell is never used either; the run fails if anything fell back
// to "$HOME/.omnipus".
func TestMain(m *testing.M) {
	home, err := testutil.IsolateProcessHome()
	if err != nil {
		panic("pkg/channels/wecom TestMain: " + err.Error())
	}
	if err := os.Setenv(config.EnvHome, filepath.Join(home.Dir, "omnipus-home")); err != nil {
		panic("pkg/channels/wecom TestMain: set " + config.EnvHome + ": " + err.Error())
	}
	code := m.Run()
	if leaks := home.Leaks(); len(leaks) > 0 {
		fmt.Fprintln(os.Stdout, "wecom TestMain: a test wrote into the shared test home instead of its own temp dir:",
			strings.Join(leaks, ", "))
		if code == 0 {
			code = 1
		}
	}
	home.Remove()
	os.Exit(code)
}
