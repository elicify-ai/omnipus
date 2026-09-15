package browser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFocusTreatmentActions pins the two action sequences themselves, so a
// future edit that drops the emulation half from foregroundTabActions (the
// original defect) fails here even if every call site is still wired up.
func TestFocusTreatmentActions(t *testing.T) {
	assert.Equal(t, "foreground", focusTreatment(foregroundTabActions()),
		"foregroundTabActions must bring the tab to front AND enable focus emulation")
	assert.Equal(t, "background", focusTreatment(backgroundTabActions()),
		"backgroundTabActions must disable focus emulation and must NOT bring anything to front")
}
