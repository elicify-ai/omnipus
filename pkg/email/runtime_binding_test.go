package email

import (
	"reflect"
	"testing"
)

// This is a loud missing-implementation diagnostic, NOT a behavioral proof.
// The runtime pack binds W1 §3.1's optional session-source injection using an
// indicative SetSessionSource setter. The publisher may rename the binding.
// A minimal scratch observation of this file can explain the missing seam
// even while the full pool pack is blocked at compilation by absent types.
func TestRuntime_PoolSourceBindingRequired(t *testing.T) {
	clientType := reflect.TypeOf((*Client)(nil))
	if _, exists := clientType.MethodByName("SetSessionSource"); !exists {
		t.Fatal("BLOCKED: Client session-source injection (RED binding: SetSessionSource) not implemented — required by W1 §3.1/FR-W1-1/FR-W1-21")
	}
}
