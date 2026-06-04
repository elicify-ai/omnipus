//go:build !cgo

package gateway

import (
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/dapicom-ai/omnipus/pkg/channels"
	"github.com/dapicom-ai/omnipus/pkg/gateway/inboundschemas"
)

// TestWhatsAppPairingStatusEnumMatchesContract guards against drift between the
// Go channels.PairingStatus constant set and the WhatsAppPairingFrame.status
// enum in the wire contract (#283). If they diverge, a status the backend emits
// could be silently dropped by the SPA's strict zod validation (or a contract
// value would have no Go constant). The embedded schema is the same one the
// generated TypeScript/zod is built from, so matching it transitively pins the
// Go side to the SPA's accepted values.
func TestWhatsAppPairingStatusEnumMatchesContract(t *testing.T) {
	raw, err := inboundschemas.FS.ReadFile("WhatsAppPairingFrame.yaml")
	if err != nil {
		t.Fatalf("read embedded contract schema: %v", err)
	}

	var doc struct {
		Properties struct {
			Status struct {
				Enum []string `yaml:"enum"`
			} `yaml:"status"`
		} `yaml:"properties"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse contract schema: %v", err)
	}

	contract := slices.Clone(doc.Properties.Status.Enum)
	if len(contract) == 0 {
		t.Fatal("contract schema has no status enum; check the embedded schema path")
	}

	goStatuses := make([]string, len(channels.AllPairingStatuses))
	for i, s := range channels.AllPairingStatuses {
		goStatuses[i] = string(s)
	}

	slices.Sort(contract)
	slices.Sort(goStatuses)

	if !slices.Equal(contract, goStatuses) {
		t.Errorf(
			"PairingStatus drift:\n  Go constants:  %v\n  contract enum: %v\n"+
				"update channels.AllPairingStatuses and contracts/components/schemas/WhatsAppPairingFrame.yaml together",
			goStatuses, contract,
		)
	}
}
