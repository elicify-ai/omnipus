package gateway

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// K5 / FR-063/075/079/087: a committed-but-unconfirmed response is the state
// variant, never an unavailable envelope or an invitation to create v2. The
// existing metadata/dispatch cuts are re-used through the real authenticated
// handler, adding operation-contract validation and duplicate-version oracles.
func TestWU4Selection_K5CommittedFailuresAndSameIDDuplicateConform(t *testing.T) {
	for _, cut := range []string{"terminal_metadata", "resume_dispatch"} {
		t.Run(cut, func(t *testing.T) {
			f := newCardMatrixHTTP(t, secretrequest.Input{}, nil)
			selectionK5AssertAuthentication(t, f, f.al.GetConfig())
			if cut == "terminal_metadata" {
				path := filepath.Join(f.al.GetSessionStore().BaseDir(), f.rec.Card.SessionId, "secret_requests.json")
				require.NoError(t, os.Remove(path))
				require.NoError(t, os.Mkdir(path, 0o700))
			} else {
				f.bus.Close() // real transport rejects continuation publication after the durable save
			}
			response := f.call(t, http.MethodPost, f.valuesBody(t, map[string]string{"api_key": cardMatrixHTTPValue}))
			state := requireCardMatrixState(t, response, http.StatusServiceUnavailable, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
			selectionK5ValidateActualResponse(t, http.MethodPost, response)
			require.NotNil(t, state.ReferenceNames)
			require.Len(t, *state.ReferenceNames, 1)
			receipt, exists, err := f.store.CardReceipt(f.rec.Card.RequestId)
			require.NoError(t, err)
			require.True(t, exists)
			require.Equal(t, *state.ReferenceNames, receipt.References)
			stored, err := f.store.Get(receipt.References[0])
			require.NoError(t, err)
			require.Equal(t, cardMatrixHTTPValue, stored)
			names, err := f.store.List()
			require.NoError(t, err)
			require.Equal(t, receipt.References, names, "one version was committed, not a half-save or replacement")
			storePath := filepath.Join(f.api.homePath, "credentials.json")
			before, err := os.ReadFile(storePath)
			require.NoError(t, err)

			for range 2 {
				read := f.call(t, http.MethodGet, "")
				readState := requireCardMatrixState(t, read, http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
				assert.Equal(t, state, readState)
				selectionK5ValidateActualResponse(t, http.MethodGet, read)
			}
			// This explicit control is not an automatic client replay. Different
			// plaintext makes an accidental second version easy to distinguish.
			duplicate := f.call(t, http.MethodPost, f.valuesBody(t, map[string]string{"api_key": "FAKE-EXPLICIT-DUPLICATE-NOT-V2"}))
			duplicateState := requireCardMatrixState(t, duplicate, http.StatusConflict, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
			assert.Equal(t, state, duplicateState)
			selectionK5ValidateActualResponse(t, http.MethodPost, duplicate)
			assert.NotContains(t, duplicate.Body.String(), "FAKE-EXPLICIT-DUPLICATE-NOT-V2")
			after, err := os.ReadFile(storePath)
			require.NoError(t, err)
			assert.Equal(t, before, after, "GET reconciliation and same-ID duplicate cannot rewrite values/bindings/receipt")
			afterNames, err := f.store.List()
			require.NoError(t, err)
			assert.Equal(t, names, afterNames)
			afterReceipt, exists, err := f.store.CardReceipt(f.rec.Card.RequestId)
			require.NoError(t, err)
			require.True(t, exists)
			assert.Equal(t, receipt, afterReceipt)
		})
	}
}
