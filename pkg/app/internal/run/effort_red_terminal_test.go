//go:build goolm && stdjson

package run_test

// CHECK-15 / S-70: observe the actual omnipus run client's outbound socket.
// The gateway is the process edge here. The real server command branch is
// independently exercised in pkg/agent; this is not a fake command-handler test.
import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/app/internal/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestT_R1_S70_TerminalSendsNoEffortMetadata(t *testing.T) {
	for _, prompt := range []string{"perform task 7731", "/effort high"} {
		for _, model := range []string{"", "terminal-model"} {
			t.Run(prompt+"/model="+model, func(t *testing.T) {
				received := make(chan map[string]any, 1)
				serverErrors := make(chan error, 1)
				const reply = "Use the effort chip next to the model in the web chat. Effort isn't available in omnipus run yet."
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := wsUpgrader.Upgrade(w, r, nil)
					if err != nil {
						serverErrors <- err
						return
					}
					defer conn.Close()
					var auth map[string]any
					if err = conn.ReadJSON(&auth); err != nil {
						serverErrors <- err
						return
					}
					if auth["token"] != testToken {
						serverErrors <- context.Canceled
						return
					}
					var frame map[string]any
					if err = conn.ReadJSON(&frame); err != nil {
						serverErrors <- err
						return
					}
					received <- frame
					// Only a transport peer: the fixed hint is also checked against the
					// actual command runtime, never inferred from this scripted response.
					for _, raw := range []string{`{"type":"token","content":` + jsonStringForTerminal(reply) + `}`, `{"type":"done","stats":{}}`} {
						if err = conn.WriteMessage(1, []byte(raw)); err != nil {
							serverErrors <- err
							return
						}
					}
				}))
				defer srv.Close()
				var out bytes.Buffer
				err := run.Run(context.Background(), run.Options{Agent: "mia", Prompt: prompt, Model: model, Addr: strings.TrimPrefix(srv.URL, "http://"), Token: testToken, Stdout: &out, Timeout: 5 * time.Second})
				require.NoError(t, err)
				select {
				case err := <-serverErrors:
					require.NoError(t, err)
				default:
				}
				var frame map[string]any
				select {
				case frame = <-received:
				case <-time.After(time.Second):
					t.Fatal("actual terminal client emitted no message frame")
				}
				want := map[string]any{"type": "message", "agent_id": "mia", "content": prompt}
				if model != "" {
					want["metadata"] = map[string]any{"model_name": model}
				}
				assert.Equal(t, want, frame, "S-70: no provider/effort metadata or command substitution in the terminal client")
				assert.Equal(t, reply+"\n", out.String())
			})
		}
	}
}
func jsonStringForTerminal(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
