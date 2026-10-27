package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

// FR-087 / BDD-6.24 plus chief ruling of record, 2026-10-16:
// post-publication uncertainty is 503 WITH submitted SecretRequestState, not
// pre-publication 500/pending and not 200 claiming a confirmed continuation.
func TestSecretK5_CommittedButUnconfirmedIs503WithSubmittedState(t *testing.T) {
	for _, cut := range []string{"terminal_metadata", "resume_dispatch"} {
		t.Run(cut, func(t *testing.T) {
			f := newCardMatrixHTTP(t, secretrequest.Input{}, nil)
			if cut == "terminal_metadata" {
				path := filepath.Join(f.al.GetSessionStore().BaseDir(), f.rec.Card.SessionId, "secret_requests.json")
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Mkdir(path, 0o700))
			} else {
				// A closed real transport rejects publication; no fake registry or
				// swallowed write is substituted for the continuation path.
				f.bus.Close()
			}
			response := f.call(t, http.MethodPost, f.valuesBody(t, map[string]string{"api_key": cardMatrixHTTPValue}))
			state := requireCardMatrixState(t, response, http.StatusServiceUnavailable, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
			require.NotNil(t, state.ReferenceNames)
			require.Len(t, *state.ReferenceNames, 1)
			receipt, exists, err := f.store.CardReceipt(f.rec.Card.RequestId)
			require.NoError(t, err)
			require.True(t, exists)
			require.Equal(t, *state.ReferenceNames, receipt.References)
			stored, err := f.store.Get(receipt.References[0])
			require.NoError(t, err)
			require.Equal(t, cardMatrixHTTPValue, stored, "503 must never imply that a durable value was not saved")
			read := requireCardMatrixState(t, f.call(t, http.MethodGet, ""), http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
			require.Equal(t, state, read)
		})
	}
}
