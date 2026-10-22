package generated

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// C-85 only: the remaining copy/enum/required-delta comparisons keep their
// existing recursive oracle. Section R2 of seams-decision.md requires resolving
// references in their own document, not deleting references or origin members.
type c85SchemaSet struct { // not-wire-format: test-only schema documents.
	docs      map[string]any
	component string
	async     string
	origin    string
}

func c85LoadSchemas(t *testing.T) c85SchemaSet {
	t.Helper()
	root := contractsDir()
	s := c85SchemaSet{
		component: filepath.Join(root, "components", "schemas", "BrowserHandoverNoticeFrame.yaml"),
		async:     filepath.Join(root, "asyncapi.yaml"),
		origin:    filepath.Join(root, "components", "schemas", "HistoryEntryOrigin.yaml"),
		docs:      map[string]any{},
	}
	for _, path := range []string{s.component, s.async, s.origin} {
		s.docs[path] = loadYAMLDoc(t, path)
	}
	return s
}

func c85ResolvedCopies(t *testing.T) (any, any) {
	t.Helper()
	s := c85LoadSchemas(t)
	canonical, handSync, err := s.copies()
	require.NoError(t, err, "C-85 must inspect both actual referenced origin schemas")
	return canonical, handSync
}

func (s c85SchemaSet) copies() (any, any, error) {
	inline, err := c85Pointer(s.docs[s.async], "/components/schemas/BrowserHandoverNoticeFrame")
	if err != nil {
		return nil, nil, err
	}
	canonical, err := s.resolve(inline, s.async, "/components/schemas/BrowserHandoverNoticeFrame", map[string]bool{})
	if err != nil {
		return nil, nil, err
	}
	handSync, err := s.resolve(s.docs[s.component], s.component, "", map[string]bool{})
	if err != nil {
		return nil, nil, err
	}
	return canonical, handSync, nil
}

func c85Pointer(root any, pointer string) (any, error) {
	if pointer == "" {
		return root, nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("C-85 unsupported reference fragment %q", pointer)
	}
	node := root
	for _, raw := range strings.Split(pointer[1:], "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		switch value := node.(type) {
		case map[string]any:
			var ok bool
			node, ok = value[key]
			if !ok {
				return nil, fmt.Errorf("C-85 unresolved pointer %q at %q", pointer, key)
			}
		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(value) {
				return nil, fmt.Errorf("C-85 unresolved array pointer %q", pointer)
			}
			node = value[index]
		default:
			return nil, fmt.Errorf("C-85 unresolved non-container pointer %q", pointer)
		}
	}
	return node, nil
}

func (s c85SchemaSet) reference(base, ref string) (any, string, string, error) {
	u, err := url.Parse(ref)
	if err != nil || u.Scheme != "" || u.Host != "" || u.RawQuery != "" {
		return nil, "", "", fmt.Errorf("C-85 invalid or nonlocal reference %q", ref)
	}
	path := base
	if u.Path != "" {
		path = filepath.Clean(filepath.Join(filepath.Dir(base), filepath.FromSlash(u.Path)))
	}
	doc, ok := s.docs[path]
	if !ok {
		return nil, "", "", fmt.Errorf("C-85 unresolved document reference %q from %s", ref, base)
	}
	node, err := c85Pointer(doc, u.Fragment)
	return node, path, u.Fragment, err
}

func (s c85SchemaSet) resolve(node any, doc, pointer string, active map[string]bool) (any, error) {
	switch value := node.(type) {
	case map[string]any:
		out := map[string]any{}
		if rawRef, hasRef := value["$ref"]; hasRef {
			ref, ok := rawRef.(string)
			if !ok || ref == "" {
				return nil, fmt.Errorf("C-85 invalid reference at %s#%s", doc, pointer)
			}
			target, targetDoc, targetPointer, err := s.reference(doc, ref)
			if err != nil {
				return nil, err
			}
			identity := targetDoc + "#" + targetPointer
			if active[identity] {
				return nil, fmt.Errorf("C-85 cyclic reference %s", identity)
			}
			active[identity] = true
			resolved, err := s.resolve(target, targetDoc, targetPointer, active)
			delete(active, identity)
			if err != nil {
				return nil, err
			}
			out, ok = resolved.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("C-85 reference target is not a schema object: %s", identity)
			}
			if annotation, annotated := value["x-go-type"]; annotated {
				isOriginProperty := (doc == s.component && pointer == "/properties/history_origin") ||
					(doc == s.async && pointer == "/components/schemas/BrowserHandoverNoticeFrame/properties/history_origin")
				isOriginTarget := (targetDoc == s.origin && targetPointer == "") ||
					(targetDoc == s.async && targetPointer == "/components/schemas/HistoryEntryOrigin")
				if annotation != "HistoryEntryOrigin" || !isOriginProperty || !isOriginTarget || out["type"] != "object" {
					return nil, fmt.Errorf("C-85 unexpected generator annotation %v at %s#%s", annotation, doc, pointer)
				}
			}
		}
		for key, child := range value {
			// At a properties container, title/description/example are wire
			// member names, not schema annotations. Never erase member drift.
			propertyMap := strings.HasSuffix(pointer, "/properties") || strings.HasSuffix(pointer, "/patternProperties")
			if (yamlNoiseKeys[key] && !propertyMap) || key == "$ref" {
				continue
			}
			// Only this checked annotation on this resolved reference is metadata.
			// Arbitrary extensions, including unrelated x-go-type values, survive
			// comparison or fail above; no extension baseline exists.
			if key == "x-go-type" && value["$ref"] != nil {
				continue
			}
			if _, duplicate := out[key]; duplicate {
				return nil, fmt.Errorf("C-85 ambiguous reference sibling %q at %s#%s", key, doc, pointer)
			}
			resolved, err := s.resolve(child, doc, pointer+"/"+key, active)
			if err != nil {
				return nil, err
			}
			out[key] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, len(value))
		for index, child := range value {
			resolved, err := s.resolve(child, doc, fmt.Sprintf("%s/%d", pointer, index), active)
			if err != nil {
				return nil, err
			}
			out[index] = resolved
		}
		return out, nil
	default:
		return node, nil
	}
}
