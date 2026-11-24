// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package programs

// Spec: docs/internal/specs/cli-credentials-s1-s2-spec.md rev 8.
// Traces to: scenario S-33 (create), S-34 (validation, dataset D-2 rows 1–27
// and 31 [S1]), S-84 (shared program word accepted), S-85's canonical-host
// rows — FR-019, FR-042, Scope Boundaries.
//
// The oracle is dataset D-2 (expected column) and the Scope Boundaries list;
// the REST status codes (400/409) of S-33/S-35 are covered by the gateway
// slice — this unit pins which inputs are refused, which are accepted, and
// that conflicts name what the spec says (GitHub, the count).
// D-2 rows 28–30 are [S2] and deliberately absent.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func acmeBase() CustomRequest {
	return CustomRequest{
		DisplayName:    "Acme",
		ProgramWords:   []string{"acme"},
		TokenVariables: []string{"ACME_TOKEN"},
		NetworkSites:   []string{"api.acme.example"},
	}
}

func githubPreset() ProgramEntry {
	return ProgramEntry{
		ID: "github", Kind: "preset", DisplayName: "GitHub",
		ProgramWords:   []string{"gh", "git"},
		TokenVariables: []string{"GH_TOKEN", "GITHUB_TOKEN"},
		NetworkSites: []string{"github.com", "api.github.com", "uploads.github.com",
			"codeload.github.com", "objects.githubusercontent.com", "raw.githubusercontent.com"},
	}
}

// d2ValidationRows is dataset D-2 [S1]: refused inputs with the reason class
// the spec gives. Message contents are asserted where the spec pins them
// (names of the conflicting entry, the reserved names).
var d2ValidationRows = []struct {
	name string
	mut  func(*CustomRequest)
}{
	{name: "D2_row1_empty_display_name", mut: func(r *CustomRequest) { r.DisplayName = "" }},
	{name: "D2_row2_display_name_65_chars", mut: func(r *CustomRequest) { r.DisplayName = strings.Repeat("a", 65) }},
	{name: "D2_row4_empty_program_words", mut: func(r *CustomRequest) { r.ProgramWords = []string{} }},
	{name: "D2_row5_nine_words", mut: func(r *CustomRequest) {
		r.ProgramWords = []string{"w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8", "w9"}
	}},
	{name: "D2_row6_word_is_a_path", mut: func(r *CustomRequest) { r.ProgramWords = []string{"/usr/bin/acme"} }},
	{name: "D2_row7_word_is_a_shell", mut: func(r *CustomRequest) { r.ProgramWords = []string{"bash"} }},
	{name: "D2_row9_empty_token_variables", mut: func(r *CustomRequest) { r.TokenVariables = []string{} }},
	{name: "D2_row10_five_variables", mut: func(r *CustomRequest) {
		r.TokenVariables = []string{"A1", "B2", "C3", "D4", "E5"}
	}},
	{name: "D2_row11_lowercase_variable", mut: func(r *CustomRequest) { r.TokenVariables = []string{"acme_token"} }},
	{name: "D2_row12_protected_variable", mut: func(r *CustomRequest) { r.TokenVariables = []string{"PATH"} }},
	{name: "D2_row13_reserved_prefix", mut: func(r *CustomRequest) { r.TokenVariables = []string{"OMNIPUS_ACME"} }},
	{name: "D2_row14a_reserved_git_name", mut: func(r *CustomRequest) { r.TokenVariables = []string{"GIT_CONFIG_COUNT"} }},
	{name: "D2_row14b_reserved_git_key", mut: func(r *CustomRequest) { r.TokenVariables = []string{"GIT_CONFIG_KEY_0"} }},
	{name: "D2_row14c_reserved_git_value", mut: func(r *CustomRequest) { r.TokenVariables = []string{"GIT_CONFIG_VALUE_0"} }},
	{name: "D2_row14d_git_config_parameters", mut: func(r *CustomRequest) { r.TokenVariables = []string{"GIT_CONFIG_PARAMETERS"} }},
	{name: "D2_row16_thirty_three_sites", mut: func(r *CustomRequest) {
		r.NetworkSites = make([]string, 33)
		for i := range r.NetworkSites {
			r.NetworkSites[i] = fmt.Sprintf("host%d.example", i)
		}
	}},
	{name: "D2_row17_site_with_scheme", mut: func(r *CustomRequest) { r.NetworkSites = []string{"https://api.acme.example"} }},
	{name: "D2_row18_site_is_an_ip", mut: func(r *CustomRequest) { r.NetworkSites = []string{"10.0.0.5"} }},
	{name: "D2_row20_bare_wildcard_site", mut: func(r *CustomRequest) { r.NetworkSites = []string{"*"} }},
	{name: "D2_row25_check_word_not_among_words", mut: func(r *CustomRequest) {
		r.IdentityCheck = &ProgramIdentityCheck{ProgramWord: "other", Args: []string{"x"}, TimeoutSeconds: 20}
	}},
	{name: "D2_row26_check_args_17_items", mut: func(r *CustomRequest) {
		args := make([]string, 17)
		for i := range args {
			args[i] = "a"
		}
		r.IdentityCheck = &ProgramIdentityCheck{ProgramWord: "acme", Args: args, TimeoutSeconds: 20}
	}},
	{name: "D2_row27_check_timeout_31", mut: func(r *CustomRequest) {
		r.IdentityCheck = &ProgramIdentityCheck{ProgramWord: "acme", Args: []string{"x"}, TimeoutSeconds: 31}
	}},
	{name: "bounds_check_timeout_0", mut: func(r *CustomRequest) {
		r.IdentityCheck = &ProgramIdentityCheck{ProgramWord: "acme", Args: []string{"x"}, TimeoutSeconds: 0}
	}},
}

func TestCustomProgram_Validation(t *testing.T) {
	existing := []ProgramEntry{githubPreset()}

	t.Run("D2_refused_rows", func(t *testing.T) {
		for _, row := range d2ValidationRows {
			t.Run(row.name, func(t *testing.T) {
				req := acmeBase()
				row.mut(&req)
				err := ValidateCustomRequest(req, existing)
				require.Error(t, err, "D-2 refuses this input")
			})
		}
	})

	t.Run("D2_row3_duplicate_display_name_conflict_names_github", func(t *testing.T) {
		// D-2 row 3: the duplicate is a 409-class conflict naming GitHub.
		req := acmeBase()
		req.DisplayName = "github"
		err := ValidateCustomRequest(req, existing)
		require.Error(t, err)
		require.Contains(t, err.Error(), "GitHub", "the conflict names the existing entry")
	})

	t.Run("D2_row31_thirty_third_entry_is_a_conflict", func(t *testing.T) {
		// D-2 row 31: the collection cap (≤ 32 entries) is a 409-class
		// conflict, not a field rule.
		req := acmeBase()
		full := make([]ProgramEntry, 32)
		for i := range full {
			full[i] = ProgramEntry{ID: fmt.Sprintf("entry-%02d", i), Kind: "custom",
				DisplayName: fmt.Sprintf("Entry %02d", i), ProgramWords: []string{fmt.Sprintf("w%d", i)},
				TokenVariables: []string{fmt.Sprintf("TOKEN_%d", i)}, NetworkSites: []string{fmt.Sprintf("host%d.example", i)}}
		}
		err := ValidateCustomRequest(req, full)
		require.Error(t, err)
	})

	t.Run("accepted_rows", func(t *testing.T) {
		// D-2 row 8: the shared word `git` is accepted (S-84); row 15: a
		// shared variable is accepted (conflict only per agent); row 19: the
		// wildcard site is accepted; row 21: empty sites are accepted.
		shared := acmeBase()
		shared.ProgramWords = []string{"git"}
		require.NoError(t, ValidateCustomRequest(shared, existing), "D-2 row 8: shared word accepted")

		sharedVar := acmeBase()
		sharedVar.TokenVariables = []string{"GH_TOKEN"}
		require.NoError(t, ValidateCustomRequest(sharedVar, existing), "D-2 row 15: shared variable accepted")

		wildcard := acmeBase()
		wildcard.NetworkSites = []string{"*.acme.example"}
		require.NoError(t, ValidateCustomRequest(wildcard, existing), "D-2 row 19: wildcard accepted")

		noSites := acmeBase()
		noSites.NetworkSites = []string{}
		require.NoError(t, ValidateCustomRequest(noSites, existing), "D-2 row 21: empty sites accepted")

		validCheck := acmeBase()
		validCheck.IdentityCheck = &ProgramIdentityCheck{ProgramWord: "acme", Args: []string{"whoami"}, TimeoutSeconds: 20}
		require.NoError(t, ValidateCustomRequest(validCheck, existing), "bounds in range accepted")
	})

	t.Run("accepted_boundaries_exactly_at_every_max", func(t *testing.T) {
		// CHECK F17: each refused D-2 row has an otherwise-valid boundary
		// twin — exactly at the max the request is accepted, so a refusal
		// can never be attributed to the neighbouring field. The 400/409
		// class split itself arrives with GREEN's typed validation errors
		// (the RED stub returns plain errors only; see test-disputes B).
		atMax := acmeBase()
		atMax.DisplayName = strings.Repeat("a", 64)                                   // row 2's max − refused only at 65
		atMax.ProgramWords = []string{"w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8"} // row 5's max − 8
		atMax.TokenVariables = []string{"A1", "B2", "C3", "D4"}                       // row 10's max − 4
		atMax.NetworkSites = make([]string, 32)                                       // row 16's max − 32
		for i := range atMax.NetworkSites {
			atMax.NetworkSites[i] = fmt.Sprintf("host%d.example", i)
		}
		atMax.IdentityCheck = &ProgramIdentityCheck{ProgramWord: "w1", Args: make([]string, 16), TimeoutSeconds: 30} // rows 26/27's max
		for i := range atMax.IdentityCheck.Args {
			atMax.IdentityCheck.Args[i] = "a"
		}
		require.NoError(t, ValidateCustomRequest(atMax, existing),
			"every field exactly at its dataset maximum is accepted (D-2 boundaries)")
	})

	t.Run("D2_row22_git_host_normalises_to_canonical", func(t *testing.T) {
		// D-2 row 22: `GitHub.com.` is accepted and stored `github.com`
		// (canonical: lower-case, no trailing dot); the username defaults to
		// x-access-token (contract ProgramGit).
		git, err := ValidateGit(ProgramGit{Host: "GitHub.com."})
		require.NoError(t, err)
		require.Equal(t, "github.com", git.Host)
		require.Equal(t, "x-access-token", git.Username, "the default username")
	})

	t.Run("D2_row23_git_host_with_port_refused", func(t *testing.T) {
		_, err := ValidateGit(ProgramGit{Host: "github.com:443"})
		require.Error(t, err, "a host with a port is refused")
	})

	t.Run("D2_row24_git_username_grammar", func(t *testing.T) {
		// `x access` violates ^[A-Za-z0-9._@-]{1,64}$; `x-access-token` fits.
		_, err := ValidateGit(ProgramGit{Host: "gitlab.example", Username: "x access"})
		require.Error(t, err, "username with a space is refused")
		_, err = ValidateGit(ProgramGit{Host: "gitlab.example", Username: "x-access-token"})
		require.NoError(t, err, "the default username form is valid")
		_, err = ValidateGit(ProgramGit{Host: "gitlab.example", Username: strings.Repeat("a", 65)})
		require.Error(t, err, "65 characters exceed the 64 cap")
	})
}

// Each ladder mutates exactly one otherwise-valid field. D-2 pins the HTTP
// class, not a Go error name or verbatim message for these fields (H19).
func TestCustomProgram_IndependentBoundaries(t *testing.T) {
	for _, field := range []struct {
		name     string
		min, max int
		set      func(*CustomRequest, int)
	}{
		{"display_name", 1, 64, func(r *CustomRequest, n int) { r.DisplayName = strings.Repeat("a", n) }},
		{"program_words", 1, 8, func(r *CustomRequest, n int) { r.ProgramWords = uniqueProgramStrings("word", n) }},
		{"token_variables", 1, 4, func(r *CustomRequest, n int) { r.TokenVariables = uniqueProgramStrings("TOKEN_", n) }},
		{"network_sites", 0, 32, func(r *CustomRequest, n int) {
			r.NetworkSites = nil
			for i := 0; i < n; i++ {
				r.NetworkSites = append(r.NetworkSites, fmt.Sprintf("host%d.example", i))
			}
		}},
		{"check_args", 0, 16, func(r *CustomRequest, n int) {
			r.IdentityCheck = &ProgramIdentityCheck{ProgramWord: "acme", Args: uniqueProgramStrings("arg", n), TimeoutSeconds: 20}
		}},
		{"check_arg_length", 0, 256, func(r *CustomRequest, n int) {
			r.IdentityCheck = &ProgramIdentityCheck{ProgramWord: "acme", Args: []string{strings.Repeat("a", n)}, TimeoutSeconds: 20}
		}},
		{"check_timeout", 1, 30, func(r *CustomRequest, n int) {
			r.IdentityCheck = &ProgramIdentityCheck{ProgramWord: "acme", TimeoutSeconds: n}
		}},
	} {
		counts := []int{field.min, field.min + 1, field.max - 1, field.max, field.max + 1}
		if field.min > 0 {
			counts = append(counts, field.min-1)
		} // negative slice/string lengths have no input representation
		for _, n := range counts {
			t.Run(fmt.Sprintf("%s_%d", field.name, n), func(t *testing.T) {
				r := acmeBase()
				field.set(&r, n)
				err := ValidateCustomRequest(r, []ProgramEntry{githubPreset()})
				if n < field.min || n > field.max {
					require.Error(t, err, "only %s violates its bound", field.name)
				} else {
					require.NoError(t, err, "%s at its independent bound", field.name)
				}
			})
		}
	}
}

func uniqueProgramStrings(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}
