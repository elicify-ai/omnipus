package catalog

import (
	"strings"
	"testing"
)

// Issue #800 (Bedrock region contract): the catalog document gains two
// optional fields — provider.regions ([{id, group}]) and
// model.inference_profiles ([]string) — parsed and validated the same way
// every other closed-set field in this package is (parseProtocol,
// AuthMethod, Status, …): TestParseDocument_Rejects already owns the
// table-driven reject/accept convention this file extends with fixture
// mutations scoped to provider[0] ("zai") — the field is provider-agnostic,
// so it needs no bedrock-shaped row to prove out.

// TestParseDocument_RegionsAndInferenceProfiles_Accepted proves the two new
// fields round-trip into the Go domain types on a conforming document.
func TestParseDocument_RegionsAndInferenceProfiles_Accepted(t *testing.T) {
	m := fixtureMap(t)
	p := provider(t, m, 0)
	p["regions"] = []any{
		map[string]any{"id": "us-east-1", "group": "us"},
		map[string]any{"id": "eu-central-1", "group": "eu"},
		map[string]any{"id": "us-gov-west-1", "group": ""},
	}
	model(t, p, 0)["inference_profiles"] = []any{"us", "eu"}
	data := encode(t, m)

	doc, err := ParseDocument(data)
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	got := doc.Providers[0]
	want := []ProviderRegion{
		{ID: "us-east-1", Group: "us"},
		{ID: "eu-central-1", Group: "eu"},
		{ID: "us-gov-west-1", Group: ""},
	}
	if len(got.Regions) != len(want) {
		t.Fatalf("Regions = %#v, want %#v", got.Regions, want)
	}
	for i := range want {
		if got.Regions[i] != want[i] {
			t.Fatalf("Regions[%d] = %#v, want %#v", i, got.Regions[i], want[i])
		}
	}
	gotProfiles := got.Models[0].InferenceProfiles
	if len(gotProfiles) != 2 || gotProfiles[0] != "us" || gotProfiles[1] != "eu" {
		t.Fatalf("InferenceProfiles = %#v, want [us eu]", gotProfiles)
	}
}

// TestParseDocument_RegionsAndInferenceProfiles_Absent proves both fields
// are genuinely optional: a document that never mentions them parses with
// nil slices, never a validation error (FR-002-style optional field).
func TestParseDocument_RegionsAndInferenceProfiles_Absent(t *testing.T) {
	doc, err := ParseDocument(loadFixture(t))
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if doc.Providers[0].Regions != nil {
		t.Fatalf("Regions = %#v, want nil when the field is absent", doc.Providers[0].Regions)
	}
	if doc.Providers[0].Models[0].InferenceProfiles != nil {
		t.Fatalf("InferenceProfiles = %#v, want nil when the field is absent",
			doc.Providers[0].Models[0].InferenceProfiles)
	}
}

func TestParseDocument_RegionsAndInferenceProfiles_Rejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, m map[string]any)
		wantErr string
	}{
		{
			name: "unknown region group",
			mutate: func(t *testing.T, m map[string]any) {
				p := provider(t, m, 0)
				p["regions"] = []any{map[string]any{"id": "us-east-1", "group": "mars"}}
			},
			wantErr: "providers[0].regions[0].group",
		},
		{
			name: "empty region id",
			mutate: func(t *testing.T, m map[string]any) {
				p := provider(t, m, 0)
				p["regions"] = []any{map[string]any{"id": "", "group": "us"}}
			},
			wantErr: "providers[0].regions[0].id",
		},
		{
			name: "duplicate region id",
			mutate: func(t *testing.T, m map[string]any) {
				p := provider(t, m, 0)
				p["regions"] = []any{
					map[string]any{"id": "us-east-1", "group": "us"},
					map[string]any{"id": "us-east-1", "group": ""},
				}
			},
			wantErr: "providers[0].regions[1].id",
		},
		{
			name: "unknown inference_profiles value",
			mutate: func(t *testing.T, m map[string]any) {
				model(t, provider(t, m, 0), 0)["inference_profiles"] = []any{"mars"}
			},
			wantErr: "providers[0].models[0].inference_profiles[0]",
		},
		{
			// The empty string is a legal ProviderRegion.Group (on-demand
			// only) but never a legal inference_profiles ENTRY — a model
			// cannot have a cross-region profile in "no group".
			name: "empty-string inference_profiles entry",
			mutate: func(t *testing.T, m map[string]any) {
				model(t, provider(t, m, 0), 0)["inference_profiles"] = []any{""}
			},
			wantErr: "providers[0].models[0].inference_profiles[0]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := fixtureMap(t)
			tc.mutate(t, m)
			data := encode(t, m)
			_, err := ParseDocument(data)
			if err == nil {
				t.Fatalf("expected rejection containing %q, got accept", tc.wantErr)
			}
			if want := tc.wantErr; !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not name %q", err.Error(), want)
			}
		})
	}
}
