//go:build goolm && stdjson

package gateway

// Order 38 — TestPermissionsPatch_PerKey (S-82, S-99; Dataset D6; the PATCH
// rules block is the oracle). Rides the order 5/8 harness in this package
// (newPermissionsAPI / patchPermissions / getPermissions / requireErrContains
// / attachTestAuditor / readAuditEntries): the real route table built by
// registerAdditionalEndpoints, so today's red is a 404 for every leg until
// PATCH /security/permissions exists (C-2).
//
// Legs the earlier orders do NOT already pin:
//   - S-82: two tabs changing different keys both keep their change (a key
//     the request does not name is never written); each request names only
//     its own key.
//   - S-99: the audit holds exactly one change entry per changed key (actor,
//     key, old and new value); equal-value writes add none.
//   - D6: empty request → 400 "Nothing to change."; null member or value →
//     400 naming the key; one bad key → 400 naming the first bad key with
//     nothing saved (validate before write); two keys for one skill in one
//     request → 400 "`plan` is listed twice.".
import (
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"

	"github.com/stretchr/testify/require"
)

func TestPermissionsPatch_PerKey(t *testing.T) {
	api, routes := newPermissionsAPI(t)

	t.Run("S-82: two writes naming different keys both keep their change", func(t *testing.T) {
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "first tab's write (body: %s)", w.Body.String())
		w = patchPermissions(t, api, routes, `{"policies":{"send_email":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "second tab's write (body: %s)", w.Body.String())

		w = getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, w.Code, "read model (body: %s)", w.Body.String())
		require.Contains(t, w.Body.String(), `"plan":"ask"`, "tab A's change survives tab B's write")
		require.Contains(t, w.Body.String(), `"send_email":"deny"`, "tab B's change is stored")
	})

	t.Run("S-99: one audit entry per changed key, none for equal-value writes", func(t *testing.T) {
		auditDir := attachTestAuditor(t, api)
		before := len(readAuditEntries(t, auditDir, audit.EventSecuritySettingChange))

		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		w = patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "equal-value write is a valid no-op (body: %s)", w.Body.String())

		entries := readAuditEntries(t, auditDir, audit.EventSecuritySettingChange)
		require.Equal(t, before+1, len(entries), "exactly one change entry for one changed key — the equal-value write adds none")
	})

	t.Run("D6: an empty request is 400 Nothing to change.", func(t *testing.T) {
		w := patchPermissions(t, api, routes, `{}`)
		requireErrContains(t, w, http.StatusBadRequest, "Nothing to change.")
	})

	t.Run("D6: a null value is 400 naming the key", func(t *testing.T) {
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":null}}`)
		requireErrContains(t, w, http.StatusBadRequest, "plan")
	})

	t.Run("D6: one bad key refuses the whole request and saves nothing", func(t *testing.T) {
		w := patchPermissions(t, api, routes, `{"policies":{"send_email":"deny"},"skill_policies":{"not-a-known-skill":"ask"}}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "validate before write (body: %s)", w.Body.String())

		w = getPermissions(t, api, routes)
		require.NotContains(t, w.Body.String(), `"send_email":"deny"`, "nothing stored when one key is bad")
	})

	t.Run("D6: two keys for one skill in one request are refused naming it", func(t *testing.T) {
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"ask"},"skill_visibility":{"plan":"name_only"}}`)
		requireErrContains(t, w, http.StatusBadRequest, "`plan` is listed twice.")
	})
}
