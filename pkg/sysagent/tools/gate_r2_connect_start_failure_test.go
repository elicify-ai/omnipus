package systools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/channels"
	"github.com/elicify-ai/omnipus/pkg/config"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "dial tcp 203.0.113.9:443: i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// TestConnectChannel_StartFailureKeepsItsCauseAndRepair is Train 2 gate r2
// T2-SF-10 (re-aimed for gate r3 M10, where the reload's start reports its
// failure through LastStartError): when the channel's start fails, the saved-but-not-working result
// names a value-free cause and the matching repair — a damaged local session
// store is not a credential problem — and never the raw platform text.
func TestConnectChannel_StartFailureKeepsItsCauseAndRepair(t *testing.T) {
	cases := map[string]struct {
		err      error
		want     []string
		mustLack []string
	}{
		"local session store": {
			err:      fmt.Errorf("open whatsapp store: %w: %w", channels.ErrLocalState, errors.New("open /private/omnipus/whatsapp.db: permission denied")),
			want:     []string{"local data", "readable and writable", "will not fix this"},
			mustLack: []string{"/private/omnipus", "permission denied"},
		},
		"platform unreachable": {
			err:      fmt.Errorf("connect: %w", timeoutError{}),
			want:     []string{"could not be reached", "network"},
			mustLack: []string{"203.0.113.9"},
		},
		"rejected or failed": {
			err:      errors.New(`whatsapp: server said {"error":"device banned"}`),
			want:     []string{"rejected the credentials or the start failed", "server log"},
			mustLack: []string{"device banned"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tool, _ := connectRevisionFixture(t, func(*config.Config) {})
			tool.deps.ChannelLiveStatus = func(string) (bool, string, bool) { return false, "", true }
			tool.deps.LastStartError = func(string) error { return tc.err }
			res := tool.Execute(context.Background(), map[string]any{
				"id": "whatsapp", "workspace_id": "sales", "agent_id": "mia", "expected_revision": "absent"})
			require.NotNil(t, res)
			out := strings.ToLower(res.ForLLM)
			require.Contains(t, out, "saved but not working", res.ForLLM)
			for _, w := range tc.want {
				require.Contains(t, out, w, "the result names the cause and its repair: %s", res.ForLLM)
			}
			for _, raw := range tc.mustLack {
				require.NotContains(t, out, strings.ToLower(raw), "raw platform/local text must not reach the model")
			}
		})
	}
}
