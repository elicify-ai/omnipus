package captureext

import (
	"os/exec"
	"testing"
)

func TestEncoderInputPressure(t *testing.T) {
	out, err := exec.Command("node", "testdata/input_pressure.cjs", "embedded/encoder.js").CombinedOutput()
	if err != nil {
		t.Fatalf("input pressure behavior: %v\n%s", err, out)
	}
}
