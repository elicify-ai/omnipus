// Omnipus — condition (4): a note is only typed T when T would ACCEPT the
// values it carries. Plus the already-typed write guard.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// conformanceShapes builds one inferred type, `project`, whose declarations
// exercise every kind of value judgement condition (4) makes:
//
//	status  enum(active, done), single
//	due     date, single
//	owner   relation to person, single
//	tags    text, many
//	notes   text, single
func conformanceShapes() map[string]typeShape {
	return buildTypeShapes(map[string][]InferredProperty{
		"project": {
			{Name: "status", Type: records.TypeEnum, EnumValues: []string{"active", "done"}},
			{Name: "due", Type: records.TypeDate},
			{Name: "owner", Type: records.TypeRelation, To: "person"},
			{Name: "tags", Type: records.TypeText, Many: true},
			{Name: "notes", Type: records.TypeText},
		},
	})
}

func recordFrom(t *testing.T, frontmatter string) records.Record {
	t.Helper()
	rec := records.ParseRecord("/vault/subject.md", []byte("---\n"+frontmatter+"---\n\nbody\n"))
	if rec.ParseError != "" {
		t.Fatalf("the fixture frontmatter does not parse: %s", rec.ParseError)
	}
	return rec
}

// TestEveryValueAccepted covers condition (4) directly. The ACCEPTED cases
// matter as much as the rejected ones: a rule that blocks everything would
// pass a suite of rejection tests alone, and would silently turn every
// untyped note into a NO-MATCH.
func TestEveryValueAccepted(t *testing.T) {
	shapes := conformanceShapes()
	shape := shapes["project"]

	cases := []struct {
		name         string
		frontmatter  string
		wantAccepted bool
		// wantProperty is the key expected to be blamed, for rejections.
		wantProperty string
		// wantDetail, when set, must appear in the explanation — the report
		// has to name the offending value, not merely say "no".
		wantDetail string
		why        string
	}{
		{
			name:         "every value conforms",
			frontmatter:  "status: active\ndue: 2026-04-01\nowner: \"[[Alice]]\"\ntags:\n  - a\n  - b\nnotes: anything at all\n",
			wantAccepted: true,
			why:          "the ordinary case; if this fails, condition (4) refuses everything and every note becomes NO-MATCH",
		},
		{
			name:         "enum value not in the declared set",
			frontmatter:  "status: blocked\n",
			wantAccepted: false,
			wantProperty: "status",
			wantDetail:   "blocked",
			why:          "the original defect: `status: blocked` was written as a project whose values are active/done",
		},
		{
			name:         "enum value differing only by case is ACCEPTED",
			frontmatter:  "status: Active\n",
			wantAccepted: true,
			why:          "FR-011a matching is case-insensitive; refusing `Active` would refuse a note the schema accepts",
		},
		{
			name:         "text where a date is declared",
			frontmatter:  "due: sometime next spring\n",
			wantAccepted: false,
			wantProperty: "due",
			wantDetail:   "sometime next spring",
			why:          "real vaults carry prose in date fields; it must block the type, not be written and then reported invalid",
		},
		{
			name:         "a list where a single value is declared",
			frontmatter:  "owner:\n  - \"[[Alice]]\"\n  - \"[[Bob]]\"\n",
			wantAccepted: false,
			wantProperty: "owner",
			why:          "arity is part of what a type accepts",
		},
		{
			name:         "a single value where a list is declared",
			frontmatter:  "tags: just-one\n",
			wantAccepted: false,
			wantProperty: "tags",
			why:          "the arity rule is symmetric; records.validate.go reports this direction too",
		},
		{
			name:         "a relation pointing at the WRONG record type is accepted",
			frontmatter:  "owner: \"[[Some Project]]\"\n",
			wantAccepted: true,
			why:          "DELIBERATE: a wrong relation target is a relation_type_mismatch finding, exactly as it is for an already-typed note. Holding an untyped note to a stricter standard than a typed one would refuse notes the vault is full of.",
		},
		{
			name:         "an undeclared key is not condition (4)'s business",
			frontmatter:  "mood: pensive\n",
			wantAccepted: true,
			why:          "condition (2) already rejects this type; condition (4) must not double-report it",
		},
		{
			name:         "an explicit null is absence, not a bad value",
			frontmatter:  "due:\n",
			wantAccepted: true,
			why:          "FR-007: a key with no value is not a value, and absence is condition (3)'s question",
		},
		{
			name:         "an empty scalar on a non-text property is absence",
			frontmatter:  "due: \"\"\n",
			wantAccepted: true,
			why:          "FR-007a: on a non-text type `x: \"\"` is unset, so it is not a date fault",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := recordFrom(t, tc.frontmatter)
			block, ok := everyValueAccepted(rec, shape)

			if ok != tc.wantAccepted {
				t.Fatalf("accepted = %v, want %v (block=%+v)\n  why this case exists: %s",
					ok, tc.wantAccepted, block, tc.why)
			}
			if tc.wantAccepted {
				return
			}
			if block.Property != tc.wantProperty {
				t.Errorf("blamed property = %q, want %q — a refusal that names the wrong key sends the operator to fix the wrong thing",
					block.Property, tc.wantProperty)
			}
			if block.Type != "project" {
				t.Errorf("blamed type = %q, want project", block.Type)
			}
			if strings.TrimSpace(block.Detail) == "" {
				t.Error("no detail — an inference that cannot be explained in one line is a bad inference")
			}
			if tc.wantDetail != "" && !strings.Contains(block.Detail, tc.wantDetail) {
				t.Errorf("detail %q does not quote the offending value %q", block.Detail, tc.wantDetail)
			}
		})
	}
}

// TestEveryValueAccepted_UsesTheValidatorsOwnAnswer is the anti-drift guard.
// Condition (4) exists so that a note the importer types PASSES
// records.Validate. If the two ever disagree, that promise is broken —
// so the two are asked the same question here and compared.
func TestEveryValueAccepted_UsesTheValidatorsOwnAnswer(t *testing.T) {
	shape := conformanceShapes()["project"]
	probes := []string{
		"status: active\ndue: 2026-04-01\n",
		"status: blocked\n",
		"due: not-a-date\n",
		"owner:\n  - \"[[A]]\"\n  - \"[[B]]\"\n",
		"tags: single\n",
		"status: Active\n",
		"due: \"\"\n",
		"notes: \"\"\n",
	}
	for _, fm := range probes {
		t.Run(strings.ReplaceAll(strings.TrimSpace(fm), "\n", " | "), func(t *testing.T) {
			rec := recordFrom(t, fm)
			_, accepted := everyValueAccepted(rec, shape)

			// The same question, asked of the validator's resolver directly.
			validatorHappy := true
			for _, key := range rec.Frontmatter.Keys {
				decl, declared := shape.declared[key]
				if !declared {
					continue
				}
				if records.ResolveProperty(rec, probeProperty(decl)).State == records.StateNonConforming {
					validatorHappy = false
				}
			}
			if accepted != validatorHappy {
				t.Errorf("condition (4) says accepted=%v but records.ResolveProperty says conforming=%v — the importer and the validator disagree, which is exactly the drift this rule exists to prevent",
					accepted, validatorHappy)
			}
		})
	}
}

// TestWriteTypeKey_RefusesAnAlreadyTypedFile guards a refusal the function's
// own documentation promised and the code did not implement: writing a
// SECOND `type:` key into a file that already has one. It cannot happen
// through InferTypesForUntypedNotes, which is exactly why nothing would have
// noticed it was missing.
func TestWriteTypeKey_RefusesAnAlreadyTypedFile(t *testing.T) {
	const src = "---\ntype: book\ntitle: Dune\n---\n\nbody\n"
	path := filepath.Join(t.TempDir(), "note.md")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	err := writeTypeKey(path, "film")
	if err == nil {
		t.Fatal("writeTypeKey wrote a second `type:` key into a file that already declared one — the file now has a duplicate key")
	}
	if !strings.Contains(err.Error(), "already declares") {
		t.Errorf("error %q does not say the file is already typed", err)
	}
	raw, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("re-reading: %v", readErr)
	}
	if string(raw) != src {
		t.Errorf("a refused write still modified the file:\n  want %q\n  got  %q", src, raw)
	}
}
