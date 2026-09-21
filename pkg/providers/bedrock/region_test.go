package bedrock

import "testing"

// Issue #800 (Bedrock region contract) — "Runtime model-id resolution":
//
//  1. id starts with "arn:" -> send unchanged.
//  2. id already starts with a known group prefix (us./eu./apac./jp./au./
//     global.) -> send unchanged.
//  3. g = the selected region's group. If g != "" AND g is in
//     model.inference_profiles -> send g + "." + id.
//  4. Otherwise -> send the bare id (on-demand in that region).
//
// Never auto-select "global" — ResolveModelID never prepends "global."
// itself; a "global." id only ever reaches it prefixed already (rule 2).
func TestResolveModelID_ArnPassesThroughUnchanged(t *testing.T) {
	id := "arn:aws:bedrock:us-east-1:111122223333:inference-profile/my-profile"
	got := ResolveModelID(id, "us", []string{"us"})
	if got != id {
		t.Fatalf("ResolveModelID(arn) = %q, want unchanged %q", got, id)
	}
}

func TestResolveModelID_AlreadyPrefixedPassesThroughUnchanged(t *testing.T) {
	for _, prefix := range []string{"us.", "eu.", "apac.", "jp.", "au.", "global."} {
		id := prefix + "anthropic.claude-sonnet-4-6"
		// Even with a group present in inference_profiles, an
		// already-prefixed id is sent verbatim — it is never re-prefixed
		// or stripped.
		got := ResolveModelID(id, "us", []string{"us", "eu"})
		if got != id {
			t.Fatalf("ResolveModelID(%q) = %q, want unchanged", id, got)
		}
	}
}

func TestResolveModelID_GroupPrefixAppliedWhenPresentInInferenceProfiles(t *testing.T) {
	got := ResolveModelID("anthropic.claude-sonnet-4-6", "eu", []string{"us", "eu", "apac"})
	if want := "eu.anthropic.claude-sonnet-4-6"; got != want {
		t.Fatalf("ResolveModelID = %q, want %q", got, want)
	}
}

func TestResolveModelID_BareIDWhenGroupAbsentFromInferenceProfiles(t *testing.T) {
	got := ResolveModelID("anthropic.claude-sonnet-4-6", "jp", []string{"us", "eu"})
	if want := "anthropic.claude-sonnet-4-6"; got != want {
		t.Fatalf("ResolveModelID = %q, want bare id %q", got, want)
	}
}

func TestResolveModelID_BareIDWhenEmptyGroup(t *testing.T) {
	// An on-demand-only region (ProviderRegion.Group == "") never prefixes,
	// even when the model's inference_profiles is non-empty.
	got := ResolveModelID("anthropic.claude-sonnet-4-6", "", []string{"us", "eu"})
	if want := "anthropic.claude-sonnet-4-6"; got != want {
		t.Fatalf("ResolveModelID = %q, want bare id %q", got, want)
	}
}

func TestResolveModelID_BareIDWhenInferenceProfilesEmpty(t *testing.T) {
	got := ResolveModelID("amazon.nova-pro-v1:0", "us", nil)
	if want := "amazon.nova-pro-v1:0"; got != want {
		t.Fatalf("ResolveModelID = %q, want bare id %q", got, want)
	}
}

// NeverAutoSelectsGlobal: even a model whose inference_profiles carries
// "global" must not be silently sent as global.<id> for a region whose OWN
// group is "global" auto-derived — that never happens because no catalog
// region is ever assigned group "global" by product decision (regions map
// to us/eu/apac/jp/au or "", never global); this test pins that ResolveModelID
// itself has no special-case that would invent a "global" prefix beyond
// exactly mirroring the selected region's own group.
func TestResolveModelID_NeverInventsGlobalPrefix(t *testing.T) {
	got := ResolveModelID("anthropic.claude-sonnet-4-6", "global", []string{"us", "eu", "global"})
	// If a region's own group WERE "global" (never happens in the shipped
	// catalog, but the function must not special-case it away either), rule
	// 3 applies exactly like any other group — the function does not add
	// extra protection here; the "never auto-select global" guarantee comes
	// from the CALLER never resolving a region to group "global" on a miss
	// or a default (see TestResolveRegion below), not from this function
	// refusing the literal value.
	if want := "global.anthropic.claude-sonnet-4-6"; got != want {
		t.Fatalf("ResolveModelID = %q, want %q", got, want)
	}
}

// TestResolveRegion pins the exact precedence CONTRACT.md specifies: row
// setting -> AWS_REGION environment variable -> catalog default region.
// TestNewProvider_RejectsMalformedRegion guards against a host-redirection
// SSRF: region is operator-settable (issue #800), and regionalEndpoint built
// its URL by naive string concatenation before this fix — a region carrying
// a "/" could steer the constructed URL's host to an attacker-chosen domain
// while still ending in the literal substring ".amazonaws.com" (as a PATH
// segment, not the actual host), which net/url's own host/path split does
// not catch on its own.
func TestNewProvider_RejectsMalformedRegion(t *testing.T) {
	cases := []string{
		"us-east-1.evil.com/x",
		"evil.com",
		"us-east-1/../../evil.com",
		"us-east-1#@evil.com",
		"",
		" ",
		"US-EAST-1", // uppercase never appears in a real AWS region code
	}
	for _, region := range cases {
		if _, err := NewProvider("test-key", WithRegion(region)); err == nil {
			t.Errorf("NewProvider(WithRegion(%q)) = nil error, want a rejection", region)
		}
	}
}

func TestNewProvider_AcceptsWellFormedRegions(t *testing.T) {
	for _, region := range []string{"us-east-1", "eu-central-1", "us-gov-west-1", "ap-northeast-1"} {
		p, err := NewProvider("test-key", WithRegion(region))
		if err != nil {
			t.Errorf("NewProvider(WithRegion(%q)): unexpected error %v", region, err)
			continue
		}
		if want := "https://bedrock-runtime." + region + ".amazonaws.com"; p.Endpoint() != want {
			t.Errorf("Endpoint() = %q, want %q", p.Endpoint(), want)
		}
	}
}

func TestResolveRegion(t *testing.T) {
	cases := []struct {
		name       string
		row        string
		env        string
		catalogDef string
		want       string
	}{
		{name: "row wins over everything", row: "eu-central-1", env: "ap-northeast-1", catalogDef: "us-east-1", want: "eu-central-1"},
		{name: "env wins when row unset", row: "", env: "ap-northeast-1", catalogDef: "us-east-1", want: "ap-northeast-1"},
		{name: "catalog default when both unset", row: "", env: "", catalogDef: "us-east-1", want: "us-east-1"},
		{name: "row wins even when env also set", row: "us-gov-west-1", env: "eu-central-1", catalogDef: "us-east-1", want: "us-gov-west-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveRegion(tc.row, tc.env, tc.catalogDef)
			if got != tc.want {
				t.Fatalf("ResolveRegion(%q,%q,%q) = %q, want %q", tc.row, tc.env, tc.catalogDef, got, tc.want)
			}
		})
	}
}
