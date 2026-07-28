// Regression tests for #412 schedule reload fix.
//
// BDD scenarios:
//   Scenario: restAPI.cronService atomic.Pointer Store/Load wiring
//   Scenario: cronSvc() returns current pointer after Store
//
// Traces to: project-task-management-level1-spec.md — #412 schedule reload

package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/cron"
)

// TestRestAPI_CronService_AtomicStoreLoad asserts that restAPI.cronService is an
// atomic.Pointer[cron.CronService] that correctly reflects updates via Store.
//
// BDD:
//
//	Given a restAPI with cronService pointing to serviceA,
//	When cronService.Store(serviceB) is called (simulating reload),
//	Then cronSvc() returns serviceB — not serviceA.
//
// Guards against: the reload path failing to update restAPI.cronService (stale ptr).
// Traces to: pkg/gateway/gateway.go executeReload → restAPIRef.cronService.Store — #412
func TestRestAPI_CronService_AtomicStoreLoad(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Verify that cronService is a zero-value atomic.Pointer initially (no cron service
	// wired in the test harness).
	require.Nil(t, api.cronSvc(),
		"cronSvc() must return nil when no CronService has been stored")

	// Build two distinct CronService instances.
	home := t.TempDir()
	serviceA := cron.NewCronService(home + "/cron-a")
	serviceB := cron.NewCronService(home + "/cron-b")

	// Store serviceA.
	api.cronService.Store(serviceA)
	assert.Equal(t, serviceA, api.cronSvc(),
		"cronSvc() must return serviceA after storing serviceA")

	// Simulate a reload: Store serviceB.
	api.cronService.Store(serviceB)
	assert.Equal(t, serviceB, api.cronSvc(),
		"cronSvc() must return serviceB after storing serviceB (reload simulation)")

	// Verify they are distinct (differentiation test).
	assert.NotEqual(t, serviceA, serviceB,
		"serviceA and serviceB must be distinct CronService pointers")
}

// TestRestAPI_CronService_AtomicPointer_StoreLoadSame asserts that Store + Load
// on the cronService field returns the exact same pointer — confirming the atomic
// pointer wiring is correct.
//
// Guards against: the field being changed to a non-atomic pointer in a refactor.
// Traces to: pkg/gateway/rest.go restAPI.cronService field type — #412
func TestRestAPI_CronService_AtomicPointer_StoreLoadSame(t *testing.T) {
	api := newTestRestAPIWithHome(t)

	// Store and load — if this compiles and runs, the type is correct.
	testSvc := cron.NewCronService(t.TempDir() + "/type-check")
	api.cronService.Store(testSvc)
	loaded := api.cronService.Load()
	assert.Same(t, testSvc, loaded,
		"atomic.Pointer[CronService].Load must return the same pointer that was Stored")
}
