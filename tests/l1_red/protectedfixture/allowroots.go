package protectedfixture

import (
	"reflect"
	"testing"
)

// Allowance is an independent test datum, not a production wire/API type.
// Chief ruling 2026-10-13 fixes the future production shape to Path/Writable.
// Reflection lets RED compile on the pre-implementation tree and fail loudly
// for a missing seam; once the seam exists, the SAME tests execute real probes.
type Allowance struct {
	Path     string
	Writable bool
}

func SetAllowRoots(t *testing.T, dst any, field string, roots []Allowance) {
	t.Helper()
	v := reflect.ValueOf(dst).Elem().FieldByName(field)
	if !v.IsValid() || v.Kind() != reflect.Slice || !v.CanSet() {
		t.Fatalf("BLOCKED: %s not implemented — required by amendment §2 D3/D5 and chief's Path/Writable ruling", field)
	}
	values := reflect.MakeSlice(v.Type(), len(roots), len(roots))
	for i, root := range roots {
		entry := values.Index(i)
		if entry.Kind() != reflect.Struct {
			t.Fatal("AllowRoot must be a Path/Writable struct (chief ruling)")
		}
		path, writable := entry.FieldByName("Path"), entry.FieldByName("Writable")
		if !path.IsValid() || path.Kind() != reflect.String || !path.CanSet() || !writable.IsValid() || writable.Kind() != reflect.Bool || !writable.CanSet() {
			t.Fatal("BLOCKED: AllowRoot.Path/Writable not implemented — required by chief ruling 2026-10-13")
		}
		path.SetString(root.Path)
		writable.SetBool(root.Writable)
	}
	v.Set(values)
}
