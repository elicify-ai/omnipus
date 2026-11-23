package agent

// Spec rev 8, S46/D4 and S130/D9. The full turn, store, connected check,
// loop-wired bash and delivered model result are real. Only the provider and
// external account-check executable are process-edge fakes. Background sinks
// remain separate rows, not implied by these foreground assertions.
import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func programsForeground(t *testing.T, f *wu5Fixture, id, command string) string {
	t.Helper()
	sid := f.mainSession("sales", "dev")
	f.provider.setScript(sid, []providers.LLMResponse{wu5BashCall(id, command), wu5Stop("output received")})
	programsWebTurn(t, f, sid, "run the output probe")
	f.waitForCalls(sid, 2, id+" actual child output")
	return strings.TrimSpace(f.provider.resultForCall(sid, id))
}

func TestProgramMasking_Boundaries(t *testing.T) {
	t.Run("S46_masking_regardless_of_filter_settings_D4", func(t *testing.T) {
		const token = "tok_valid_12345"
		rows := []struct {
			name, command, expected string
			filter                  bool
			minimum                 int
		}{
			{"D4_row1_filter_on", "echo $GH_TOKEN", "[FILTERED]", true, 8},
			{"D4_row2_filter_off", "echo $GH_TOKEN", "[FILTERED]", false, 8},
			{"D4_row3_min_100000", "echo $GH_TOKEN", "[FILTERED]", true, 100000},
			{"D4_row4_repetition", "echo $GH_TOKEN $GH_TOKEN", "[FILTERED] [FILTERED]", false, 8},
			// D4 row5 explicitly accepts transformed plaintext; derive base64
			// mathematically from echo's plaintext plus newline, not observed output.
			{"D4_row5_base64_residual", "echo $GH_TOKEN | base64", base64.StdEncoding.EncodeToString([]byte(token + "\n")), false, 8},
		}
		for _, row := range rows {
			t.Run(row.name, func(t *testing.T) {
				f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) {
					cfg.Tools.FilterSensitiveData, cfg.Tools.FilterMinLength = row.filter, row.minimum
				})
				require.Equal(t, row.expected, programsForeground(t, f, row.name, row.command), "S46/D4: program masking is independent of the general filter")
			})
		}
		t.Run("D4_row7_other_secret_unchanged", func(t *testing.T) {
			f := newProgramsTurnFixtureFor(t, func(cfg *config.Config) { cfg.Tools.FilterSensitiveData = false })
			const other = "other_secret_123456"
			_, err := f.al.sysagentDeps.CredStore.CommitCard("s46-other", "s46-other-session", []credentials.CardValue{{
				BaseName: "S46_OTHER", Recipient: credentials.CardRecipient{Kind: credentials.KindAgentPrivate, WorkspaceID: "sales", AgentID: "dev", Field: "S46_OTHER_TOKEN"}, Value: other,
			}}, time.Now())
			require.NoError(t, err)
			selected, err := f.al.sysagentDeps.CredStore.PrivateEnvironment("sales", "dev", false)
			require.NoError(t, err)
			require.Equal(t, []credentials.PrivateValue{{Name: "S46_OTHER_TOKEN", Value: other}}, selected, "instrument: actual selected non-program secret")
			require.Equal(t, other, programsForeground(t, f, "s46-other", "printf '%s' \"$S46_OTHER_TOKEN\""), "D4 row7: program masking must not silently enable the general filter")
		})
	})
}

func TestProgramMasking_ActualInterruptedForeground(t *testing.T) {
	const sentinel = "sentinel_9f3c2a7e5b1d" // S130/D9, 21 bytes.
	rows := []struct{ name, fragment, redirect, termination string }{
		{"D9_row1_seven_stdout_exit0", sentinel[:7], "", ""},
		{"D9_row2_eight_stdout_exit0", sentinel[:8], "", ""},
		{"D9_row3_ten_stderr_SIGKILL", sentinel[:10], " >&2", "; kill -KILL $$"},
		{"D9_row8_middle_ten_stdout_exit0", sentinel[4:14], "", ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			f := newProgramsTurnFixtureToken(t, sentinel, func(cfg *config.Config) { cfg.Tools.FilterSensitiveData = false })
			// A child-ran control precedes the actual literal interrupted write.
			// These fragments are deliberate fixture inputs. Only the delivered
			// result is asserted; the scripted request is NOT a secret-free sink.
			command := fmt.Sprintf("printf 'S130_CHILD_RAN\\n'; printf '%%s' '%s'%s%s", row.fragment, row.redirect, row.termination)
			result := programsForeground(t, f, row.name, command)
			require.Contains(t, result, "S130_CHILD_RAN", "a denied/missing child cannot satisfy the no-fragment oracle")
			for n := 0; n+8 <= len(sentinel); n++ {
				assert.NotContains(t, result, sentinel[n:n+8], "S130: no eight-byte sentinel window leaves the source writer")
			}
			if len(row.fragment) >= 8 {
				assert.Contains(t, result, "[FILTERED]", "D9 mandates a replacement, not empty output")
			}
		})
	}
}
