//go:build !windows

package gateway

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Instrument check, not a product GREEN claim: removing private diagnostics must
// not turn R7's actual-class comparison into a comparison of expected inputs.
func TestR8b_RunRefusalParserReadsActualClass_Q48(t *testing.T) {
	for _, check := range []string{"not_found", "not_absolute", "agent_writable_location", "location_unverifiable"} {
		for _, historical := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/historical_%t", check, historical), func(t *testing.T) {
				suffix := ")"
				if historical {
					suffix = " at private-fixture-marker)"
				}
				text := "Unrelated token other_check; program refusal (" + check + suffix + "."
				got := t11cReadCheck(t, text, t11cRunCheckPattern)
				assert.Equal(t, check, got, "the parser receives actual text and a fixed pattern, never an expected class")
			})
		}
	}
	assert.Nil(t, regexp.MustCompile(t11cRunCheckPattern).FindStringSubmatch("program refusal (not_found-extra)"),
		"instrument: a partial class match is not accepted")
}
