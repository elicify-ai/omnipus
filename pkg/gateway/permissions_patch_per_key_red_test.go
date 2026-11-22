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
//   - S-99: the audit holds exactly one change entry per changed key with
//     the entry's key, old value, new value and actor asserted (CHECK round
//     1 finding 6: count alone passed a wrong-keyed entry); equal-value
//     writes add none.
//   - D6: empty request → 400 "Nothing to change."; null member or value →
//     400 naming the key; one bad key → 400 naming the first bad key with
//     nothing saved — the FULL read model is compared unchanged against a
//     pre-request snapshot (CHECK round 1 finding 6: demanding one
//     earlier-leg value absent punished correct atomic preservation);
//     the same skill twice in one map, differing only by letter case →
//     400 "`plan` is listed twice." (spec D6 row 9, C-23 case-insensitive
//     skill ids) — CHECK round 1 finding 6: permission and visibility for
//     one skill together are INDEPENDENT per-key maps and must be accepted,
//     so a third leg asserts exactly that.
//
// Each leg builds a fresh API so legs cannot read each other's stored state
// (CHECK round 1 finding 6: the bad-key leg read the shared API's earlier
// leg and mistook a preserved value for a leak).
import (
	"net/http"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"

	"github.com/stretchr/testify/require"
)

func TestPermissionsPatch_PerKey(t *testing.T) {
	t.Run("S-82: two writes naming different keys both keep their change", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"ask"}}`)
		require.Equal(t, http.StatusOK, w.Code, "first tab's write (body: %s)", w.Body.String())
		w = patchPermissions(t, api, routes, `{"policies":{"send_email":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "second tab's write (body: %s)", w.Body.String())

		w = getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, w.Code, "read model (body: %s)", w.Body.String())
		require.Contains(t, w.Body.String(), `"plan":"ask"`, "tab A's change survives tab B's write")
		require.Contains(t, w.Body.String(), `"send_email":"deny"`, "tab B's change is stored")
	})

	t.Run("S-99: one audit entry per changed key with key, old, new and actor; none for equal-value writes", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		auditDir := attachTestAuditor(t, api)
		before := len(readAuditEntries(t, auditDir, audit.EventSecuritySettingChange))

		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		entries := readAuditEntries(t, auditDir, audit.EventSecuritySettingChange)
		require.Len(t, entries, before+1,
			"exactly one change entry for one changed key")
		if len(entries) == before+1 {
			e := entries[before]
			require.Contains(t, e["key"], "plan", "S-99: the entry names the changed key")
			require.Contains(t, e, "old", "S-99: the entry names the old value")
			require.Equal(t, "deny", e["new"], "S-99: the entry names the new value")
			require.NotEmpty(t, e["actor"], "S-99: the entry names the actor")
		}

		w = patchPermissions(t, api, routes, `{"skill_policies":{"plan":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "equal-value write is a valid no-op (body: %s)", w.Body.String())
		require.Len(t, readAuditEntries(t, auditDir, audit.EventSecuritySettingChange), before+1,
			"the equal-value write adds no entry")
	})

	t.Run("D6: an empty request is 400 Nothing to change.", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{}`)
		requireErrContains(t, w, http.StatusBadRequest, "Nothing to change.")
	})

	t.Run("D6: a null value is 400 naming the key", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":null}}`)
		requireErrContains(t, w, http.StatusBadRequest, "plan")
	})

	t.Run("D6: one bad key refuses the whole request and the full state is unchanged", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"policies":{"send_email":"deny"}}`)
		require.Equal(t, http.StatusOK, w.Code, "setup: the valid key stores (body: %s)", w.Body.String())

		snapshot := getPermissions(t, api, routes)
		require.Equal(t, http.StatusOK, snapshot.Code, "setup: the read model answers")

		w = patchPermissions(t, api, routes, `{"policies":{"send_email":"allow"},"skill_policies":{"not-a-known-skill":"ask"}}`)
		require.Equal(t, http.StatusBadRequest, w.Code, "validate before write (body: %s)", w.Body.String())

		after := getPermissions(t, api, routes)
		require.Equal(t, snapshot.Body.String(), after.Body.String(),
			"D6: nothing stored when one key is bad — the FULL read model is byte-identical, "+
				"including the value a correct atomic write preserves")
	})

	t.Run("D6 row 9: the same skill twice in one map, differing by letter case, is refused", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"ask","PLAN":"deny"}}`)
		requireErrContains(t, w, http.StatusBadRequest, "`plan` is listed twice.")

		after := getPermissions(t, api, routes)
		require.NotContains(t, after.Body.String(), `"plan"`,
			"D6 row 9: nothing saved for the duplicate request")
	})

	t.Run("D6: permission and visibility for one skill together are accepted (independent maps)", func(t *testing.T) {
		api, routes := newPermissionsAPI(t)
		w := patchPermissions(t, api, routes, `{"skill_policies":{"plan":"ask"},"skill_visibility":{"plan":"name_only"}}`)
		require.Equal(t, http.StatusOK, w.Code,
			"C-2's per-key maps are independent: one request may set a skill's permission "+
				"AND its visibility (body: %s)", w.Body.String())

		w = getPermissions(t, api, routes)
		require.Contains(t, w.Body.String(), `"plan":"ask"`, "the permission half is stored")
		require.Contains(t, w.Body.String(), `"plan":"name_only"`, "the visibility half is stored")
	})
}
