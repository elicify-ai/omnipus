package agent

// Spec rev8 S144 / FR-050 / I-2, dataset row 31: the verifier kind of S144 through
// its real session constructor (NewVerifierSession) and the real verifier
// dispatch (dispatchVerifierTurn, the entry the Judge adjudication uses). A
// command in the verifier's turn counts the program token. The test writes no
// origin, record or provenance.
// Oracle: dataset row 31 and the S-144 Then clause: a verifier session of
// Telegram-started work is withheld, so the command prints 0 and the turn says
// connected programs were not used.
// Ruling (chief, 2026-11-25, no founder scope change): there is NO production
// seam for the handoff row "Judge (verifier) run | the verified work's
// provenance". Today a verifier session carries no provenance, so it is withheld,
// which is the spec outcome. This row therefore asserts the outcome (0 plus the
// withheld notice) and deliberately not which notice: unresolved today, the
// work's own Telegram origin once a producer exists. It passes before and after
// such a producer, and fails if a verifier turn ever receives the token.
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_ActualVerifierSession(t *testing.T) {
	t.Run("S144_verifier_session_of_telegram_started_work_is_withheld_and_prints_0", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		dev := f.mainSession("sales", "dev")
		control, controlResult := programsCountInput(t, f, dev, "s144-verifier-control", programsWebInput())
		require.Equal(t, "16", control, "instrument: this fixture's actual connected identity receives the token on trusted web input")
		require.NotContains(t, controlResult, "Connected programs were not used for this command:")

		inst, ok := f.al.GetRegistry().GetAgent("dev")
		require.True(t, ok)
		verifier, err := f.al.GetSessionStore().NewVerifierSession("dev")
		require.NoError(t, err)
		path := filepath.Join(f.home, "workspaces", "sales", "work", "s144-verifier-count")
		const call = "s144-verifier-count"
		command := fmt.Sprintf("printenv GH_TOKEN | wc -c > %q; printf 'PROVENANCE_CHILD_RAN'", path)
		f.provider.setScript(verifier.ID, []providers.LLMResponse{wu5BashCall(call, command), wu5Stop("verifier probe completed")})

		ctx := tools.WithWorkspaceID(context.Background(), "sales")
		_, _, _, err = f.al.dispatchVerifierTurn(ctx, inst, "check the verified work", "s144-verifier", verifier.ID)
		require.NoError(t, err, "instrument: the real verifier dispatch completed its turn")

		result := f.provider.resultForCall(verifier.ID, call)
		require.Contains(t, result, "PROVENANCE_CHILD_RAN", "instrument: the verifier's actual command ran; a refusal or a missed request cannot establish zero")
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, "0", strings.TrimSpace(string(raw)), "S144/dataset row 31: a verifier session of Telegram-started work prints 0")
		assert.Contains(t, result, "Connected programs were not used for this command:", "S144/dataset row 31: withheld, with the notice that says so")
	})
}
