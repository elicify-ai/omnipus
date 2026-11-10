package session

import (
	"reflect"
	"testing"
)

// WU-22 / #1178: L1-AMENDMENTS-20261013.md §4.2 is the oracle.
// Reflection deliberately makes the old void interface runtime-red, not
// compile-red: the behavioural tests and every existing test still build.
func TestWU22SessionWriterMethodsReturnError(t *testing.T) {
	errorType := reflect.TypeFor[error]()
	types := []struct {
		name string
		typ  reflect.Type
	}{
		{"SessionWriter", reflect.TypeFor[SessionWriter]()},
		{"archiveBackend", reflect.TypeFor[*archiveBackend]()},
		{"UnifiedStore", reflect.TypeFor[*UnifiedStore]()},
	}
	for _, typ := range types {
		for _, name := range []string{"AddMessage", "AddFullMessage", "SetProjectionState"} {
			t.Run(typ.name+"/"+name, func(t *testing.T) {
				method, ok := typ.typ.MethodByName(name)
				if !ok {
					t.Fatalf("BLOCKED: %s.%s not implemented — required by amendment §4.2", typ.name, name)
				}
				if method.Type.NumOut() != 1 {
					t.Fatalf("BLOCKED: %s.%s returns %d values, want exactly error — required by amendment §4.2", typ.name, name, method.Type.NumOut())
				}
				if method.Type.Out(0) != errorType {
					t.Errorf("%s.%s result = %v, want error", typ.name, name, method.Type.Out(0))
				}
			})
		}
	}
}
