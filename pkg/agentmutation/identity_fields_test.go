package agentmutation

import (
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// figure and role follow colour: locked on ordinary built-ins and on hidden
// system agents, editable on a custom agent, and not operator-only.
// ARCH-DECISIONS 1.6. Icon stays protected; it is not dropped from the set.

func TestIdentityFields_LockedOnBuiltInsAndHiddenEditableOnCustom(t *testing.T) {
	ordinary := config.AgentConfig{ID: "mia", Type: config.AgentTypeCore, Locked: true}
	hidden := config.AgentConfig{ID: "judge", Type: config.AgentTypeSystem, Locked: true}
	hiddenSupervisor := config.AgentConfig{ID: "plansupervisor", Type: config.AgentTypeSystem, Locked: true}
	custom := config.AgentConfig{ID: "custom-writer", Type: config.AgentTypeCustom}
	external := externalAgent("codex")

	t.Run("ordinary built-in rejects figure and role", func(t *testing.T) {
		assertProtected(t, ValidateOperatorFields(ordinary, []string{"figure"}))
		assertProtected(t, ValidateFields(ordinary, []string{"role"}))
		assertNotEditable(t, OperatorFieldDescriptors(ordinary), "figure")
		assertNotEditable(t, OperatorFieldDescriptors(ordinary), "role")
	})

	t.Run("hidden agents reject figure and role", func(t *testing.T) {
		assertProtected(t, ValidateOperatorFields(hidden, []string{"figure", "role"}))
		assertProtected(t, ValidateOperatorFields(hiddenSupervisor, []string{"figure"}))
		assertNotEditable(t, FieldDescriptors(hidden), "role")
	})

	t.Run("custom agent may edit figure and role", func(t *testing.T) {
		if err := ValidateOperatorFields(custom, []string{"figure", "role", "color"}); err != nil {
			t.Fatalf("operator write of figure/role/color on a custom agent: %v", err)
		}
		if err := ValidateFields(custom, []string{"figure", "role"}); err != nil {
			t.Fatalf("agent-path write of figure/role on a custom agent: %v", err)
		}
		assertEditable(t, OperatorFieldDescriptors(custom), "figure")
		assertEditable(t, OperatorFieldDescriptors(custom), "role")
	})

	t.Run("external worker may edit figure and role like colour", func(t *testing.T) {
		if err := ValidateOperatorFields(external, []string{"figure", "role", "color"}); err != nil {
			t.Fatalf("external worker figure/role/color: %v", err)
		}
	})

	t.Run("icon stays protected on a built-in", func(t *testing.T) {
		assertProtected(t, ValidateOperatorFields(ordinary, []string{"icon"}))
	})

	t.Run("figure is not confused with an unknown field", func(t *testing.T) {
		err := ValidateOperatorFields(custom, []string{"figure"})
		var fe *FieldError
		if errors.As(err, &fe) && fe.Code == InvalidInput {
			t.Fatalf("figure is a known field, got INVALID_INPUT: %v", err)
		}
	})
}

func assertProtected(t *testing.T, err error) {
	t.Helper()
	var fe *FieldError
	if !errors.As(err, &fe) || fe.Code != ProtectedField || len(fe.Fields) == 0 {
		t.Fatalf("error=%#v want PROTECTED_FIELD", err)
	}
}

func assertEditable(t *testing.T, descriptors []FieldDescriptor, name string) {
	t.Helper()
	d := descriptorFor(descriptors, name)
	if d == nil || !d.Editable {
		t.Fatalf("descriptor %#v for %s want present and editable", d, name)
	}
}

func assertNotEditable(t *testing.T, descriptors []FieldDescriptor, name string) {
	t.Helper()
	d := descriptorFor(descriptors, name)
	if d == nil || d.Editable || d.Reason == "" {
		t.Fatalf("descriptor %#v for %s want present, not editable, with a reason", d, name)
	}
}
