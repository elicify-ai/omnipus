package omnpkg

import (
	"encoding/json"
	"sort"
	"strconv"
)

// checkManifestStructure applies the manifest-level relationship and layout
// rules before any payload byte is read (FR-003, FR-007, FR-009, Dataset
// D12). Order matters for the reported code: listing and ownership problems
// (manifest_mismatch) come before the element-count rules. A package with no
// agent element is no_supported_element even when it lists files. The agent
// ID and layout rules run later (checkAgentLayouts), after the payload's
// universal entry checks.
func checkManifestStructure(m *Manifest) error {
	for _, name := range sortedFileNames(m.Files) {
		if UnsafeEntryPath(name) {
			return newErr(CodeUnsafePath, "file", name)
		}
	}
	if err := checkOwnership(m); err != nil {
		return err
	}
	if err := checkCounts(m); err != nil {
		return err
	}
	agents := 0
	for _, el := range m.Elements {
		if el.Type == ElementTypeAgent {
			agents++
		}
	}
	if agents == 0 {
		return newErr(CodeNoSupportedElement)
	}
	if err := checkEveryFileOwned(m); err != nil {
		return err
	}
	if agents > 1 {
		return newErr(CodeTooManyAgents, "count", strconv.Itoa(agents))
	}
	return nil
}

// checkAgentLayouts applies the agent layout checks to every agent element.
// They run after the universal entry checks over the whole payload, so an
// unsafe entry type is reported as such even when the element lists it.
func checkAgentLayouts(m *Manifest) error {
	for _, el := range m.Elements {
		if el.Type != ElementTypeAgent {
			continue
		}
		if err := checkAgentLayout(el); err != nil {
			return err
		}
	}
	return nil
}

// checkOwnership: unique element IDs per type, every element file listed in
// files, and no file owned twice.
func checkOwnership(m *Manifest) error {
	ids := map[string]bool{}
	owner := map[string]bool{}
	for _, el := range m.Elements {
		key := el.Type + "\x00" + el.SourceID
		if ids[key] {
			return newErr(CodeManifestMismatch, "element", el.SourceID, "reason", "duplicate element ID")
		}
		ids[key] = true
		for _, f := range el.Files {
			if _, listed := m.Files[f]; !listed {
				return newErr(CodeManifestMismatch, "file", f, "reason", "element file missing from files")
			}
			if owner[f] {
				return newErr(CodeManifestMismatch, "file", f, "reason", "file owned by two elements")
			}
			owner[f] = true
		}
	}
	return nil
}

// checkEveryFileOwned: every listed file belongs to an element (FR-003).
func checkEveryFileOwned(m *Manifest) error {
	owner := map[string]bool{}
	for _, el := range m.Elements {
		for _, f := range el.Files {
			owner[f] = true
		}
	}
	for _, name := range sortedFileNames(m.Files) {
		if !owner[name] {
			return newErr(CodeManifestMismatch, "file", name, "reason", "file owned by no element")
		}
	}
	return nil
}

// checkCounts: counts must match the elements per type (a zero count for an
// absent type is allowed).
func checkCounts(m *Manifest) error {
	actual := map[string]int{}
	for _, el := range m.Elements {
		actual[el.Type]++
	}
	types := map[string]bool{}
	for t := range actual {
		types[t] = true
	}
	for t := range m.Counts {
		types[t] = true
	}
	keys := make([]string, 0, len(types))
	for t := range types {
		keys = append(keys, t)
	}
	sort.Strings(keys)
	for _, t := range keys {
		if m.Counts[t] != actual[t] {
			return newErr(CodeManifestMismatch, "count", t, "reason", "counts do not match the elements")
		}
	}
	return nil
}

// checkAgentLayout: a storable source ID, agent.json present, and only the
// agent layout entries under omnipus/agents/<source-id>/ (FR-007, D2 5, D2 10).
func checkAgentLayout(el ManifestElement) error {
	if !validAgentID(el.SourceID) {
		return newErr(CodeInvalidElement, "element", el.SourceID, "reason", "source_id is not a valid agent ID")
	}
	hasRecord := false
	for _, f := range el.Files {
		if !agentLayoutAllowed(el.SourceID, f) {
			return newErr(CodeUnsafePath, "file", f, "reason", "outside the agent layout")
		}
		if f == agentRecordPath(el.SourceID) {
			hasRecord = true
		}
	}
	if !hasRecord {
		return newErr(CodeManifestMismatch, "element", el.SourceID, "reason", "agent element without agent.json")
	}
	return nil
}

// recordIdentity is the part of an agent record the manifest relationships
// compare (D12 rows 8 and 9).
type recordIdentity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// checkRecordIdentity: element name = record name and source_id = record id.
// A record that is not a JSON object is left to ValidateElements
// (invalid_element): it is a content problem, not a format problem.
func checkRecordIdentity(m *Manifest, payload map[string][]byte) error {
	for _, el := range m.Elements {
		if el.Type != ElementTypeAgent {
			continue
		}
		var rec recordIdentity
		if err := json.Unmarshal(payload[agentRecordPath(el.SourceID)], &rec); err != nil {
			continue
		}
		if rec.ID != el.SourceID {
			return newErr(CodeManifestMismatch, "element", el.SourceID, "reason", "source_id differs from the record id")
		}
		if rec.Name != el.Name {
			return newErr(CodeManifestMismatch, "element", el.SourceID, "reason", "element name differs from the record name")
		}
	}
	return nil
}
