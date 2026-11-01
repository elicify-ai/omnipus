package agent

// RED scaffolding: reflection adapts only the mandated AdmissionFor and
// ResolveAdmission APIs. All reads execute the real owner; nothing here
// implements resolution, lifecycle fallback, admission or marker policy.

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// not-wire-format: internal test observation of the r6 admission-view API.
type capRedView struct {
	state                      string
	position, occupancy, limit int
	cause                      string
	at                         time.Time
	present                    bool
	err                        error
}

func capRedAdmissionView(t *testing.T, al *AgentLoop, claim executionClaim, resolve bool) capRedView {
	t.Helper()
	name := "AdmissionFor"
	if resolve {
		name = "ResolveAdmission"
	}
	for _, owner := range []any{NewSteerLauncher(al), al, al.steerAdmission()} {
		method := reflect.ValueOf(owner).MethodByName(name)
		if !method.IsValid() {
			continue
		}
		if method.Type().NumIn() != 1 || method.Type().In(0).Kind() != reflect.Struct {
			t.Fatalf("CAP-B10 %s must accept one full ExecutionClaim, got %s", name, method.Type())
		}
		arg := reflect.New(method.Type().In(0)).Elem()
		values := map[string]any{"SessionID": claim.SessionID, "Generation": claim.Generation, "RunID": claim.RunID, "BootSeq": claim.BootSeq}
		for key, value := range values {
			f := arg.FieldByName(key)
			if !f.IsValid() || !f.CanSet() {
				t.Fatalf("CAP-B10 full claim field %s missing", key)
			}
			f.Set(reflect.ValueOf(value).Convert(f.Type()))
		}
		out := method.Call([]reflect.Value{arg})
		n := 2
		if resolve {
			n = 3
		}
		if len(out) != n {
			t.Fatalf("CAP-B10 %s returns %d values, want %d", name, len(out), n)
		}
		v := capRedView{present: out[1].Bool()}
		if resolve && !out[2].IsNil() {
			v.err = capRedValue[error](t, "real resolver error", out[2].Interface())
		}
		if !v.present {
			return v
		}
		s := out[0]
		v.state = fmt.Sprint(s.FieldByName("State").Interface())
		v.position = int(s.FieldByName("QueuePosition").Int())
		v.occupancy = int(s.FieldByName("Occupancy").Int())
		v.limit = int(s.FieldByName("Limit").Int())
		v.cause = s.FieldByName("Cause").String()
		v.at = capRedValue[time.Time](t, "real view ObservedAt", s.FieldByName("ObservedAt").Interface())
		return v
	}
	t.Errorf("CAP-B10 RED: real %s(full claim) is absent; no resolver or view was substituted", name)
	return capRedView{state: "MISSING_CAP_B10_API"}
}

func capRedClaim(t *testing.T, al *AgentLoop, sid string) executionClaim {
	t.Helper()
	r, err := al.GetSessionLifecycleStore().Load(sid)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExecutionID == nil {
		t.Fatal("real producer did not persist an execution identity")
	}
	return executionClaim{SessionID: sid, Generation: r.Generation, RunID: r.ExecutionID.RunID, BootSeq: r.ExecutionID.BootSeq}
}

// This is a filesystem boundary observer, not a fake lifecycle/resolver. The
// consumer gets the actual first disk read; the callback changes real bytes
// or publishes a real replacement before the resolver's independent disk read.
type capRedLifecycleReadBoundary struct {
	*session.LifecycleStore
	target string
	after  func()
}

func (b *capRedLifecycleReadBoundary) Load(id string) (*session.LifecycleRecord, error) {
	r, err := b.LifecycleStore.Load(id)
	if err == nil && id == b.target && b.after != nil {
		fn := b.after
		b.after = nil
		fn()
	}
	return r, err
}
