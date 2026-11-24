package bus

// Spec rev8 I2/S123/S126: provenance is a set; both join orders produce the
// same value/serialization for every value family, not just web/Telegram.
import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProgramProvenance_CanonicalValueRegression(t *testing.T) {
	for _, row := range []struct {
		name    string
		members []Origin
	}{
		{"S123_web_and_telegram", []Origin{OriginWeb, ConnectorOrigin("telegram")}},
		{"S123_independent_and_two_other_connectors", []Origin{OriginIndependent, ConnectorOrigin("zulu"), ConnectorOrigin("alpha")}},
	} {
		t.Run(row.name, func(t *testing.T) {
			forward := NewProvenance(row.members[0])
			reverse := NewProvenance(row.members[len(row.members)-1])
			for n := 1; n < len(row.members); n++ {
				forward = Join(forward, NewProvenance(row.members[n]))
				reverse = Join(reverse, NewProvenance(row.members[len(row.members)-1-n]))
			}
			require.ElementsMatch(t, row.members, forward.Origins(), "I2: no origin added or dropped")
			require.ElementsMatch(t, row.members, reverse.Origins(), "I2: reversed construction retains the entire set")
			require.Equal(t, forward, reverse, "S123/I2: equal origin sets must be identical values, not insertion-order dependent")
			a, err := json.Marshal(forward)
			require.NoError(t, err)
			b, err := json.Marshal(reverse)
			require.NoError(t, err)
			require.Equal(t, a, b, "durable origin serialization is independent of join order")
			var reloaded Provenance
			require.NoError(t, json.Unmarshal(b, &reloaded))
			require.Equal(t, forward, reloaded, "persisted round trip keeps the same origin value")
		})
	}
}
