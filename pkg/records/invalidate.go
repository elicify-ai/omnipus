// Omnipus — spec FR-015: a schema change invalidates the records it governs.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"os"
	"sort"
)

// ---------------------------------------------------------------------------
// WHY THIS FILE EXISTS, AND WHY IT DOES NOT USE MTIME
//
// FR-015 states the trap in its own text: "Schemas live under a directory the
// scanner does not walk, so no manifest entry or mtime exists for them; the
// system MUST track them explicitly rather than inheriting note-scanning
// behaviour."
//
// The failure that requirement prevents is quiet and expensive. An operator
// tightens an enum — removes a value, adds a required property — and every
// record that no longer conforms goes on being reported as valid, because
// nothing in the note-scanning path ever noticed the SCHEMA changed. The
// records did not change. Their meaning did.
//
// So this file tracks schema files by CONTENT HASH, not modification time.
// Content hashing is what makes the check honest across the three cases mtime
// gets wrong: a git checkout that rewrites every timestamp (a change reported
// where there is none), a same-second edit (a change missed entirely), and a
// file copied back with `cp -p`.
// ---------------------------------------------------------------------------

// SchemaSnapshot is what the schema directory contained at one moment.
type SchemaSnapshot struct {
	// FileHashes maps each schema file's path to a hash of its bytes. It
	// includes files that were REJECTED (FR-002's missing schema_version, say),
	// because fixing a broken schema is itself a change that must trigger
	// revalidation — and a rejected file has no entry in the schema set to
	// notice it by.
	FileHashes map[string]string
	// TypeByFile maps a path to the record type it declared, where that was
	// readable. A file too broken to declare a type maps to "".
	TypeByFile map[string]string
}

// SnapshotSchemas loads the vault's schemas and records what was on disk.
//
// It returns the snapshot alongside the loaded set and report, because the
// caller almost always wants all three and taking them in separate passes
// would let the directory change between them.
func SnapshotSchemas(vaultRoot string) (SchemaSnapshot, *SchemaSet, *SchemaLoadReport, error) {
	set, report, err := LoadSchemas(vaultRoot)
	if err != nil {
		return SchemaSnapshot{}, nil, nil, err
	}

	snap := SchemaSnapshot{
		FileHashes: map[string]string{},
		TypeByFile: map[string]string{},
	}

	// Hash every candidate file the loader looked at, accepted or not.
	for _, p := range report.ScannedFiles {
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			// Unreadable now; the loader has already reported it. Record the
			// absence of a hash so a later successful read reads as a change.
			continue
		}
		snap.FileHashes[p] = fingerprint(data)
		if sc, rej := ParseSchema(p, data); rej == nil {
			snap.TypeByFile[p] = sc.Type
		} else {
			snap.TypeByFile[p] = rej.Type
		}
	}
	return snap, set, report, nil
}

// SchemaChangeKind is what happened to a schema file.
type SchemaChangeKind string

const (
	SchemaAdded    SchemaChangeKind = "added"
	SchemaModified SchemaChangeKind = "modified"
	SchemaRemoved  SchemaChangeKind = "removed"
)

// SchemaChange is one schema file that is not what it was.
type SchemaChange struct {
	Path string
	// Type is the record type the file declared. For a removal it is the type
	// it declared BEFORE, which is the one whose records must be revalidated.
	Type string
	Kind SchemaChangeKind
}

// DiffSchemaSnapshots reports every schema file that changed between two
// snapshots, sorted by path so output is reproducible.
func DiffSchemaSnapshots(before, after SchemaSnapshot) []SchemaChange {
	changes := []SchemaChange{}

	for path, newHash := range after.FileHashes {
		oldHash, existed := before.FileHashes[path]
		switch {
		case !existed:
			changes = append(changes, SchemaChange{Path: path, Type: after.TypeByFile[path], Kind: SchemaAdded})
		case oldHash != newHash:
			// Report BOTH types when a file's declared type changed under the
			// edit: records of the old type lose their schema and records of
			// the new one gain it. Reporting only the new type would leave the
			// old type's records validated against a schema that no longer
			// describes them.
			changes = append(changes, SchemaChange{Path: path, Type: after.TypeByFile[path], Kind: SchemaModified})
			if oldType := before.TypeByFile[path]; oldType != "" && oldType != after.TypeByFile[path] {
				changes = append(changes, SchemaChange{Path: path, Type: oldType, Kind: SchemaModified})
			}
		}
	}
	for path := range before.FileHashes {
		if _, still := after.FileHashes[path]; !still {
			changes = append(changes, SchemaChange{Path: path, Type: before.TypeByFile[path], Kind: SchemaRemoved})
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Path != changes[j].Path {
			return changes[i].Path < changes[j].Path
		}
		return changes[i].Type < changes[j].Type
	})
	return changes
}

// AffectedRecordTypes expands a set of schema changes into every record type
// whose records must be revalidated.
//
// It is a CLOSURE, not a direct mapping, and the extra hop matters: if the
// `deal` type declares `company: {type: relation, to: company}`, then changing
// the `company` schema changes what a deal's relation is allowed to point at.
// Revalidating only `company` records would leave every deal still reported as
// valid against a target type that has moved (FR-034's territory).
//
// The closure is one hop deep by design. Relation declarations are shallow and
// a full transitive closure over a cyclic graph would, in a vault where most
// types relate to a hub type, mean "every schema change revalidates everything"
// — which is not tracking, it is giving up.
func AffectedRecordTypes(set *SchemaSet, changes []SchemaChange) []string {
	direct := map[string]struct{}{}
	for _, c := range changes {
		if c.Type != "" {
			direct[c.Type] = struct{}{}
		}
	}

	affected := map[string]struct{}{}
	for t := range direct {
		affected[t] = struct{}{}
	}

	if set != nil {
		for _, typeName := range set.order {
			sc := set.byType[typeName]
			for _, propName := range sc.PropertyOrder {
				p := sc.Properties[propName]
				if p.Type != TypeRelation && p.Type != TypePerson {
					continue
				}
				if p.To == "" {
					continue
				}
				if _, hit := direct[p.To]; hit {
					affected[sc.Type] = struct{}{}
				}
			}
		}
	}

	out := make([]string, 0, len(affected))
	for t := range affected {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// SelectRecordsForRevalidation returns the records governed by any of the
// affected types.
//
// A record whose type was REMOVED is included: it must be revalidated so its
// stale findings are dropped and it goes back to being an ordinary note
// (FR-005). Dropping it from the selection instead would leave a report
// asserting faults against a schema that no longer exists.
func SelectRecordsForRevalidation(recs []Record, affectedTypes []string) []Record {
	if len(affectedTypes) == 0 {
		return nil
	}
	want := make(map[string]struct{}, len(affectedTypes))
	for _, t := range affectedTypes {
		want[t] = struct{}{}
	}
	out := []Record{}
	for _, r := range recs {
		if _, hit := want[r.TypeName()]; hit {
			out = append(out, r)
		}
	}
	return out
}

// Revalidate is the FR-015 sequence in one call: diff the snapshots, expand to
// affected types, select the governed records, and validate them.
//
// It returns the report and the affected type list, so a caller can log what
// was invalidated and why rather than only what came out of it.
func Revalidate(before, after SchemaSnapshot, set *SchemaSet, recs []Record, opts ValidateOptions) (*ValidationReport, []string) {
	changes := DiffSchemaSnapshots(before, after)
	affected := AffectedRecordTypes(set, changes)
	selected := SelectRecordsForRevalidation(recs, affected)
	return Validate(set, selected, opts), affected
}
