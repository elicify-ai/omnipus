// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Regression for the ADR-068 expansion bypass (code review round 3, 2026-08-24).
//
// absolutePathPattern treats `~` and a token-start `$VAR` as BOUNDARY characters
// and captures only the suffix after them. So `$OMNIPUS_HOME/agents/x/SOUL.md`
// reached the guard as the candidate `/agents/x/SOUL.md` — a path naming no real
// file. Measured before the fix, with the literal absolute spelling of the SAME
// file correctly refused:
//
//	cat <home>/agents/victim/SOUL.md         -> BLOCKED
//	cat $HOME/.omnipus/agents/victim/SOUL.md -> ALLOWED   <- the bypass
//	cat ~/.omnipus/agents/victim/SOUL.md     -> ALLOWED   <- the bypass
//
// The ADR-068 read exemption was reachable through a different spelling of a
// file inside the turn's secret set, because IsCarveOut cannot match a carve-out
// root against a path that never contained $OMNIPUS_HOME. The write half was
// escapable the same way: a fabricated suffix can be made to land INSIDE the
// work dir (`printf x > $HOME<abs-cwd>/pwned`) while the real target is outside
// it and under no mount, and `~/dev/null` reached the safePaths table while bash
// wrote a real file at $HOME/dev/null.
//
// Oracle (ADR-068 §1, not the code): a rule must not depend on how the path is
// SPELLED. `cat X` and `cat ~/X` name the same file and must get the same
// verdict. The fix resolves `~`, `$HOME` and `$OMNIPUS_HOME` at the scan site
// and refuses any other `$VAR` outright — resolving rather than blanket-refusing
// so that `cat ~/notes.txt`, an ordinary read ADR-068 exists to permit, keeps
// working.
func TestGuardCommand_ExpansionDerivedPathsCannotBypassTheSecretSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	victimSoul := filepath.Join(home, "agents", "victim", "SOUL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(victimSoul), 0o755))
	require.NoError(t, os.WriteFile(victimSoul, []byte("another agent's persona"), 0o600))

	work := filepath.Join(home, "agents", "me")
	require.NoError(t, os.MkdirAll(work, 0o755))
	workAbs, err := filepath.EvalSymlinks(work)
	require.NoError(t, err)

	tool, err := NewExecToolWithConfig(workAbs, true, nil)
	require.NoError(t, err)

	guard := func(cmd string) string {
		return tool.guardCommand(context.Background(), cmd, workAbs)
	}

	t.Run("a secret-set read is refused however it is spelled", func(t *testing.T) {
		// The literal spelling is the control: if this ever stops being
		// blocked, the secret-set check itself has regressed and the
		// expansion cases below would pass for the wrong reason.
		require.NotEmpty(t, guard("cat "+victimSoul),
			"control: the literal absolute path into another agent's home must be refused")

		require.NotEmpty(t, guard("cat $OMNIPUS_HOME/agents/victim/SOUL.md"),
			"$OMNIPUS_HOME spelling of the same file must get the same verdict")
		require.NotEmpty(t, guard("cat ${OMNIPUS_HOME}/agents/victim/SOUL.md"),
			"braced ${OMNIPUS_HOME} spelling must get the same verdict")
		require.NotEmpty(t, guard("grep -r persona $OMNIPUS_HOME/agents"),
			"a recursive read of the agents root must be refused too")
	})

	t.Run("a write cannot escape the mount rule through a fabricated suffix", func(t *testing.T) {
		// The pre-fix shape: the candidate `<abs-cwd>/pwned` looks like it is
		// INSIDE the work dir, so the guard waved it through before ever
		// reaching the mount check — while bash writes to $HOME<abs-cwd>/pwned.
		require.NotEmpty(t, guard("printf x > $HOME"+workAbs+"/pwned"),
			"a write whose real target is outside the work dir must not pass because its SUFFIX looks inside")

		// The safePaths variant: candidate /dev/null is exempt, real target is not.
		require.NotEmpty(t, guard("printf x > ~/dev/null"),
			"~/dev/null must not inherit the /dev/null safePaths exemption")
	})

	t.Run("an unresolvable variable fails closed", func(t *testing.T) {
		msg := guard("printf x > $SOME_UNKNOWN_VAR/x")
		require.NotEmpty(t, msg,
			"a $VAR this process cannot resolve must be refused, not guessed at")
		require.Contains(t, msg, "unresolvable path",
			"the refusal must name WHY, so an operator is not left guessing")
	})

	t.Run("ordinary reads ADR-068 permits still work", func(t *testing.T) {
		// The whole reason the fix RESOLVES the prefix instead of refusing
		// every expansion: these are the reads ADR-068 exists to allow, and a
		// blanket refusal broke all three.
		require.Empty(t, guard("cat ~/notes.txt"),
			"a plain read under the user's home is open under ADR-068")
		require.Empty(t, guard("cat $HOME/notes.txt"),
			"the $HOME spelling of the same read must agree with the ~ spelling")
		require.Empty(t, guard("cat /etc/hosts"),
			"an ordinary literal outside-the-work-dir read is open under ADR-068")
	})
}
