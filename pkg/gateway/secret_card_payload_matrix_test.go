package gateway

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/secretrequest"
	"github.com/stretchr/testify/require"
)

// FR-069/071, §6.2, D2: byte boundaries and malformed bodies must be
// enforced on a real pending request. Never accept a 404 as payload validation.
func TestSecretK5_ValueBoundsAndMalformedPayloads(t *testing.T) {
	cases := []struct {
		name, value, raw string
		status           int
	}{
		{name: "empty", value: "", status: 400},
		{name: "min_minus_1", value: "abc", status: 400},
		{name: "min", value: "abcd", status: 200},
		{name: "min_plus_1", value: "abcde", status: 200},
		{name: "max_minus_1", value: strings.Repeat("a", 16383), status: 200},
		{name: "max", value: strings.Repeat("a", 16384), status: 200},
		{name: "max_plus_1", value: strings.Repeat("a", 16385), status: 400},
		{name: "multibyte_at_max", value: strings.Repeat("\U0001f511", 4096), status: 200},
		{name: "multibyte_above_max", value: strings.Repeat("\U0001f511", 4096) + "a", status: 400},
		{name: "nul", value: "abcd\x00efgh", status: 400},
		{name: "null_value", raw: `{"values":{"api_key":null}}`, status: 400},
		{name: "null_map", raw: `{"values":null}`, status: 400},
		{name: "null_body", raw: `null`, status: 400},
		{name: "empty_object", raw: `{}`, status: 400},
		{name: "empty_map", raw: `{"values":{}}`, status: 400},
		{name: "missing_requested_key", raw: `{"values":{"different":"FAKE-WRONG-KEY"}}`, status: 400},
		{name: "extra_value_key", raw: `{"values":{"api_key":"FAKE-EXTRA-API","extra":"FAKE-EXTRA-VALUE"}}`, status: 400},
		{name: "values_and_cancel", raw: `{"values":{"api_key":"FAKE-BOTH"},"cancel":true}`, status: 400},
		{name: "cancel_false", raw: `{"cancel":false}`, status: 400},
		{name: "null_cancel", raw: `{"cancel":null}`, status: 400},
		{name: "unexpected_top_level_key", raw: `{"values":{"api_key":"FAKE-EXTRA-TOP"},"key":"arbitrary_store_key"}`, status: 400},
		{name: "non_string_value", raw: `{"values":{"api_key":12345}}`, status: 400},
		{name: "invalid_json", raw: `{"values":`, status: 400},
		{name: "invalid_utf8", raw: "{\"values\":{\"api_key\":\"abcd\xff\"}}", status: 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newCardMatrixHTTP(t, secretrequest.Input{}, nil)
			body := tc.raw
			if body == "" {
				body = f.valuesBody(t, map[string]string{"api_key": tc.value})
			}
			response := f.call(t, http.MethodPost, body)
			require.Equal(t, tc.status, response.Code, "body: %s", response.Body.String())
			if tc.status == http.StatusBadRequest {
				f.requireNoCommitOrResume(t)
				requireCardMatrixState(t, f.call(t, http.MethodGet, ""), http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStatePending)
				// Errors are value-free even when the field is malformed.
				if len(tc.value) >= 4 {
					require.NotContains(t, response.Body.String(), tc.value)
				}
				for _, marker := range []string{"FAKE-WRONG-KEY", "FAKE-EXTRA-API", "FAKE-EXTRA-VALUE", "FAKE-BOTH", "FAKE-EXTRA-TOP"} {
					require.NotContains(t, response.Body.String(), marker)
				}
			} else {
				state := requireCardMatrixState(t, response, http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
				require.NotNil(t, state.ReferenceNames)
				require.Len(t, *state.ReferenceNames, 1)
				stored, err := f.store.Get((*state.ReferenceNames)[0])
				require.NoError(t, err)
				require.Equal(t, tc.value, stored, "accepted UTF-8 bytes must be stored unchanged")
				require.NotContains(t, response.Body.String(), tc.value)
				resume := f.takeResume(t)
				require.NotContains(t, resume.Content, tc.value)
			}
		})
	}
}

// D2's body limit: boundary status must not be accidentally tested with an
// unknown request id or an invalid JSON body that would fail for another reason.
func TestSecretK5_BodyLimitCountsBytesAt65536(t *testing.T) {
	for _, size := range []int{65535, 65536, 65537} {
		t.Run(fmt.Sprintf("bytes_%d", size), func(t *testing.T) {
			f := newCardMatrixHTTP(t, secretrequest.Input{}, nil)
			body := f.valuesBody(t, map[string]string{"api_key": cardMatrixHTTPValue})
			body += strings.Repeat(" ", size-len(body)) // JSON whitespace, not extra fields
			require.Equal(t, size, len(body))
			response := f.call(t, http.MethodPost, body)
			if size <= 65536 {
				requireCardMatrixState(t, response, http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStateSubmitted)
				f.takeResume(t)
			} else {
				require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
				f.requireNoCommitOrResume(t)
				requireCardMatrixState(t, f.call(t, http.MethodGet, ""), http.StatusOK, f.rec.Card.RequestId, gen.SecretRequestStateStatePending)
			}
		})
	}
}
