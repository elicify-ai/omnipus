package agent

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
)

// #1968 R3/R4: even a capability-only catalogue uses the shipped default.
// Operator changes are read on the next attempt without replacing the catalogue.
func TestAttachPDF_ConfigDefaultsAndLiveOverlay(t *testing.T) {
	peer := newPdf1968Peer(t, pdf1968Step{content: "default"}, pdf1968Step{content: "unlimited"}, pdf1968Step{content: "restored"})
	cat := pdf1968Catalog(t, []pdf1968ProviderRow{pdf1968CapableRow("fx/config-pdf", "fx/pdf", nil)})
	al, agents, store, mediaStore := pdf1968NewPeerHarness(t, peer, cat, cat, func(c *config.Config) {
		c.PDFLimits = nil // real shipped defaults, not the single-limit fixtures' neutralization
	}, pdf1968Pair{provider: "fx/config-pdf", model: "fx/pdf"})
	data := make([][]byte, 6)
	refs := make([]string, len(data))
	for i := range data {
		data[i] = pdf1968PDFOfSize(t, 5000+i*1000)
		refs[i] = pdf1968StorePDF(t, mediaStore, "config.pdf", data[i])
	}
	for attempt, section := range []string{
		"", `{"overrides":[{"provider":"fx/config-pdf","model":"fx/pdf","max_documents_per_request":0}]}`, "",
	} {
		if attempt > 0 {
			require.NoError(t, al.MutateConfig(func(c *config.Config) error {
				c.PDFLimits = nil
				if section != "" {
					return json.Unmarshal([]byte(section), &c.PDFLimits)
				}
				return nil
			}))
		}
		sid := pdf1968NewSession(t, store, "pdf1968-agent")
		_, err := pdf1968RunTurn(t, al, agents["pdf1968-agent"], store, sid, refs...)
		require.NoError(t, err)
		bodies := peer.Bodies()
		require.Len(t, bodies, attempt+1)
		carried := pdf1968BodyPDFs(t, bodies[attempt])
		if section == "" {
			assert.Equal(t, data[1:], carried, "spec §4.3 default keeps the newest five, in wire order")
			pdf1968AssertL2(t, "shipped count default", bodies[attempt], "max_documents_per_request", []string{"fx/config-pdf", "fx/pdf"}, 5)
		} else {
			assert.Equal(t, data, carried, "operator explicit zero disables the default document count")
			assert.NotContains(t, bodies[attempt], "PDF sent as text")
		}
	}
}
