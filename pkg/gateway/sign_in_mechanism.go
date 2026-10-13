// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"fmt"
	"strings"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/providers/catalog"
)

// ── Which catalog rows can really sign in (provider hotfix H1) ──────────────
//
// A catalog row's `auth_methods` is registry data, mirrored from a separate
// repository; it can — and on the `openai` row does — declare `sign_in` for
// a row this build has no way to sign in with. Offering or accepting sign-in
// on such a row created a provider that stays "disconnected" forever beside
// the row that really owns the vendor's sign-in (openai-chatgpt).
//
// signInMechanism is therefore the gate every "is sign-in offered here?"
// decision uses on top of the declared method: the served catalog projection
// (catalog.WithSignInGate at boot), PUT /providers/{id}, the sign-in routes
// (signInMethodFor), onboarding completion, and POST /providers/{id}/test.

// signInMechanism reports whether this build can actually exercise a sign-in
// for row. It is exactly the three mechanisms probeSignIn dispatches on:
//
//   - a `cli_kind` row (a vendor CLI holds the login),
//   - a first-party OAuth owner (providers.OAuthEntryOwner — the only ids the
//     device-code flow ever writes credentials for),
//   - a `token_source: codex-auth-json` row (reads the Codex CLI's saved
//     token).
//
// No provider id is named here beyond what OAuthEntryOwner already encodes.
// Keep this in step with probeSignIn's switch: a mechanism added there must
// be added here, or the row's sign-in is hidden.
func signInMechanism(row catalog.Provider) bool {
	return row.CLIKind != "" ||
		providers.OAuthEntryOwner(row.ID) ||
		row.TokenSource == catalog.TokenSourceCodexAuthJSON
}

// rowOffersSignIn reports whether row both declares `sign_in` and has a
// mechanism this build can exercise.
func rowOffersSignIn(row catalog.Provider) bool {
	return catalogOffersAuth(row.AuthMethods, gen.ProbeProviderRequestAuthSignIn) && signInMechanism(row)
}

// signInElsewhereMsg answers a sign-in request on a row that cannot sign in
// by naming the rows of the SAME company that can — in-app sign-in rows
// (no cli_kind) first, then vendor-CLI rows, catalog order within each. It
// returns "" when providerID is not in doc, has no company, or no sibling
// offers sign-in; the caller then keeps its generic refusal.
//
// Example (the committed catalog): `sign-in is not available for "OpenAI";
// OpenAI sign-in lives on "ChatGPT (subscription)" or "Codex CLI"`.
func signInElsewhereMsg(doc *catalog.Document, providerID string) string {
	if doc == nil {
		return ""
	}
	var self *catalog.Provider
	for i := range doc.Providers {
		if doc.Providers[i].ID == providerID {
			self = &doc.Providers[i]
			break
		}
	}
	if self == nil || strings.TrimSpace(self.Company) == "" {
		return ""
	}
	var inApp, viaCLI []string
	for i := range doc.Providers {
		p := &doc.Providers[i]
		if p.ID == self.ID || p.Company != self.Company || !rowOffersSignIn(*p) {
			continue
		}
		if p.CLIKind == "" {
			inApp = append(inApp, fmt.Sprintf("%q", p.Name))
		} else {
			viaCLI = append(viaCLI, fmt.Sprintf("%q", p.Name))
		}
	}
	names := append(inApp, viaCLI...)
	if len(names) == 0 {
		return ""
	}
	where := names[0]
	if len(names) > 1 {
		where = strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
	}
	return fmt.Sprintf("sign-in is not available for %q; %s sign-in lives on %s", self.Name, self.Company, where)
}

// signInRefusalMsg is the operator-facing refusal for sign-in on providerID:
// the pointer to the row that can sign in when the catalog has one, else the
// generic "provider does not support sign-in".
func (a *restAPI) signInRefusalMsg(providerID string) string {
	if msg := signInElsewhereMsg(a.signInCatalogDocument(), providerID); msg != "" {
		return msg
	}
	return onboardingSignInUnsupportedMsg
}

// signInCatalogDocument is the document sign-in decisions read: the catalog
// this gateway serves, or the process catalog (embedded fallback) when none
// was installed on this restAPI.
func (a *restAPI) signInCatalogDocument() *catalog.Document {
	if a.providerCatalog != nil {
		if doc := a.providerCatalog.Document(); doc != nil {
			return doc
		}
	}
	return providers.ProviderCatalog().Document()
}

// misplacedSignInRowMsg explains a CONFIGURED row saved with auth_method
// sign_in whose catalog row cannot sign in (an install that saved one
// before this gate existed). "" when the row is fine or not in the catalog.
func (a *restAPI) misplacedSignInRowMsg(providerID string) string {
	var row catalog.Provider
	var known bool
	if a.providerCatalog != nil {
		row, known = a.providerCatalog.Provider(providerID)
	} else {
		row, known = providers.CatalogProvider(providerID)
	}
	if !known || rowOffersSignIn(row) {
		return ""
	}
	return a.signInRefusalMsg(providerID)
}
