package nativebinding_test

import (
	"os"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/security"
	"github.com/elicify-ai/omnipus/tests/helpers/nativebinding"
	"github.com/stretchr/testify/require"
)

func testSideAction() {}

func TestNativeBinding_Provenance(t *testing.T) {
	for _, row := range []struct {
		name string
		fn   any
		text string
	}{
		{"nil", nil, "no native function is bound"},
		{"typed nil", (func())(nil), "no native function is bound"},
		{"not a function", true, "no native function is bound"},
		{"named test stub", testSideAction, "binding source is test code"},
		{"closure", func() {}, "binding source is test code"},
		{"test-side file deletion is not an installer", os.RemoveAll, "binding is not from production module"},
	} {
		t.Run(row.name, func(t *testing.T) {
			_, err := nativebinding.Validate("pkg/security", row.fn)
			require.ErrorContains(t, err, row.text)
		})
	}
	t.Run("known native constructor", func(t *testing.T) {
		path, err := nativebinding.Validate("pkg/security", security.NewApprovalGrantStore)
		require.NoError(t, err)
		require.Contains(t, path, "pkg/security/approvalgrants.go::github.com/elicify-ai/omnipus/pkg/security.NewApprovalGrantStore")
	})
	t.Run("known native receiver method expression", func(t *testing.T) {
		path, err := nativebinding.Validate("pkg/security", (*security.ApprovalGrantStore).Record)
		require.NoError(t, err)
		require.Contains(t, path, "pkg/security/approvalgrants.go::github.com/elicify-ai/omnipus/pkg/security.(*ApprovalGrantStore).Record")
	})
	t.Run("native function from the wrong module is refused", func(t *testing.T) {
		_, err := nativebinding.Validate("pkg/tools", security.NewApprovalGrantStore)
		require.ErrorContains(t, err, "binding is not from production module pkg/tools")
	})
}
