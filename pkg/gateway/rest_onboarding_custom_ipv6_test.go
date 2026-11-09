package gateway

// rest_onboarding_custom_ipv6_test.go — #1700 security re-check finding F1:
// a permitted custom endpoint that redirects to a blocked IPv6 address
// written WITHOUT a port (http://[::1]/x, http://[::ffff:127.0.0.2]/x) was
// saved with an "unreachable" warning instead of the 422 an address refused
// by the guard gets. The same literals given directly as the endpoint must
// be refused with 422 too, and nothing may be written in any refused case.
// Explicit-port forms are the controls.
//
// Helpers come from rest_onboarding_custom_test.go and
// rest_onboarding_custom_review_test.go in the same package.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/elicify-ai/omnipus/pkg/security"
)

var blockedIPv6Targets = []struct {
	name   string
	target string
}{
	{"loopback_portless", "http://[::1]/x"},
	{"mapped_loopback_portless", "http://[::ffff:127.0.0.2]/x"},
	{"loopback_port_control", "http://[::1]:80/x"},
	{"mapped_loopback_port_control", "http://[::ffff:127.0.0.2]:80/x"},
}

func TestOnboardingCompleteCustomIPv6_RedirectToBlocked_422(t *testing.T) {
	for _, tc := range blockedIPv6Targets {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				mu.Lock()
				hits++
				mu.Unlock()
				http.Redirect(w, r, tc.target, http.StatusTemporaryRedirect)
			}))
			t.Cleanup(srv.Close)

			api, tmpDir := newCustomOnboardingAPI(t, false)
			// The endpoint itself (IPv4 loopback) is allowed; the IPv6
			// redirect targets are not.
			api.ssrfChecker = security.NewSSRFChecker([]string{"127.0.0.1"})

			w := postComplete(t, api, customCompleteBody(srv.URL, map[string]string{"endpoint": srv.URL + "/v1"}))

			requireErrNoField(t, w, http.StatusUnprocessableEntity, msgM8)
			mu.Lock()
			assert.Equal(t, 1, hits, "only the allowed endpoint is asked; the redirect is not followed")
			mu.Unlock()
			assertNothingStored(t, api, tmpDir, "my-azure")
		})
	}
}

func TestOnboardingCompleteCustomIPv6_DirectBlocked_422(t *testing.T) {
	for _, tc := range blockedIPv6Targets {
		t.Run(tc.name, func(t *testing.T) {
			api, tmpDir := newCustomOnboardingAPI(t, false)
			checker := security.NewSSRFChecker(nil)
			checker.SetResolver(&rebindingResolver{}) // any name lookup answers public first: only literal handling decides
			api.ssrfChecker = checker

			w := postComplete(t, api, customCompleteBody("", map[string]string{"endpoint": tc.target}))

			requireErrNoField(t, w, http.StatusUnprocessableEntity, msgM8)
			assertNothingStored(t, api, tmpDir, "my-azure")
		})
	}
}
