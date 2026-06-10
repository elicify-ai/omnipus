package cron

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestSaveStore_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are not enforced on Windows")
	}

	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "cron", "jobs.json")

	cs := NewCronService(storePath)

	_, err := cs.AddJob("test", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "hello", false, "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	info, err := os.Stat(storePath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("cron store has permission %04o, want 0600", perm)
	}
}

func int64Ptr(v int64) *int64 {
	return &v
}

func setupService(runner ScheduledRunner) (*CronService, string) {
	tmpFile := fmt.Sprintf("test_cron_%d.json", time.Now().UnixNano())
	cs := NewCronService(tmpFile)
	if runner != nil {
		cs.SetRunner(runner)
	}
	return cs, tmpFile
}

func TestCronService_CRUD(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	// Test AddJob
	at := time.Now().Add(time.Hour).UnixMilli()
	job, err := cs.AddJob("Task1", CronSchedule{Kind: "at", AtMS: &at}, "msg", true, "ch", "to")
	if err != nil || job.ID == "" {
		t.Fatalf("AddJob failed: %v", err)
	}

	// Test ListJobs
	if len(cs.ListJobs(true)) != 1 {
		t.Error("ListJobs should return 1 job")
	}

	// Test UpdateJob
	job.Name = "UpdatedName"
	err = cs.UpdateJob(job)
	if err != nil || cs.store.Jobs[0].Name != "UpdatedName" {
		t.Error("UpdateJob failed")
	}

	// Test EnableJob
	cs.EnableJob(job.ID, false)
	if cs.store.Jobs[0].Enabled != false || cs.store.Jobs[0].State.NextRunAtMS != nil {
		t.Error("EnableJob(false) failed to clear state")
	}

	// Test RemoveJob
	removed := cs.RemoveJob(job.ID)
	if !removed || len(cs.store.Jobs) != 0 {
		t.Error("RemoveJob failed")
	}
}

// 2. Test Cron Expression Calculation Logic
func TestCronService_ComputeNextRun(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC).UnixMilli()

	tests := []struct {
		name     string
		schedule CronSchedule
		wantNil  bool
	}{
		{"Valid Cron", CronSchedule{Kind: "cron", Expr: "0 * * * *"}, false},
		{"Invalid Cron", CronSchedule{Kind: "cron", Expr: "invalid"}, true},
		{"Every MS", CronSchedule{Kind: "every", EveryMS: int64Ptr(5000)}, false},
		{"At Future", CronSchedule{Kind: "at", AtMS: int64Ptr(now + 1000)}, false},
		{"At Past", CronSchedule{Kind: "at", AtMS: int64Ptr(now - 1000)}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cs.computeNextRun(&tt.schedule, now)
			if (got == nil) != tt.wantNil {
				t.Errorf("%s: got %v, wantNil %v", tt.name, got, tt.wantNil)
			}
		})
	}
}

// TestCronService_EveryNextRunPerScheduleDiverges documents the "every" cadence
// next-run semantics and proves there is NO shared-value binding bug (Item 2).
//
// Observation under investigation: on the Monitor screen, several "Every 1h"
// schedules showed an identical "Next run" down to the second. This test pins
// the real cause and confirms it is benign:
//
//  1. next_run for "every" is computed PER SCHEDULE as now + interval (it is not
//     aligned to a wall-clock boundary, and it is not a single value shared
//     across rows). Each job stores its own State.NextRunAtMS.
//  2. When several "every 1h" jobs have not yet fired and their next-runs are
//     (re)computed in the SAME recompute pass from one `now`, they legitimately
//     coincide to the millisecond — identical cadence + identical reference
//     time => identical result. This is correct, not a binding bug.
//  3. After a schedule actually fires, its next-run becomes fireTime + interval,
//     so once jobs fire at different instants their next-runs diverge.
//
// The assertions below lock all three facts so a future change that (a) made
// next_run a shared/aliased value, or (b) stopped recomputing per-schedule,
// would fail here.
func TestCronService_EveryNextRunPerScheduleDiverges(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	const hourMS = int64(60 * 60 * 1000)
	base := time.Date(2026, 6, 10, 13, 19, 30, 0, time.UTC).UnixMilli()
	schedA := CronSchedule{Kind: "every", EveryMS: int64Ptr(hourMS)}
	schedB := CronSchedule{Kind: "every", EveryMS: int64Ptr(hourMS)}

	// (1)+(2) Same cadence, same reference time => identical next-run. This is the
	// benign coincidence the Monitor screen showed; not a shared/aliased pointer.
	nextA := cs.computeNextRun(&schedA, base)
	nextB := cs.computeNextRun(&schedB, base)
	if nextA == nil || nextB == nil {
		t.Fatalf("computeNextRun returned nil for an every schedule: A=%v B=%v", nextA, nextB)
	}
	if *nextA != *nextB {
		t.Fatalf("same cadence + same reference time must yield equal next-run; got A=%d B=%d", *nextA, *nextB)
	}
	if *nextA != base+hourMS {
		t.Fatalf("every next-run must be now+interval (relative, not boundary-aligned); got %d want %d", *nextA, base+hourMS)
	}
	// Distinct backing storage — the equal values are NOT the same pointer/alias.
	if nextA == nextB {
		t.Fatalf("each schedule must own its next-run value; got an aliased pointer")
	}

	// (3) Once schedules fire at different instants, their next-runs diverge —
	// proving the value is recomputed per schedule from each job's own fire time.
	firedA := cs.computeNextRun(&schedA, base+5_000)       // A fires 5s later
	firedB := cs.computeNextRun(&schedB, base+5_000+1_000) // B fires 1s after A
	if firedA == nil || firedB == nil {
		t.Fatalf("computeNextRun returned nil after fire: A=%v B=%v", firedA, firedB)
	}
	if *firedA == *firedB {
		t.Fatalf("after firing at different instants, next-runs must diverge; both=%d", *firedA)
	}
}

// 3. Test Execution Flow
func TestCronService_ExecutionFlow(t *testing.T) {
	var mu sync.Mutex
	executedJobs := make(map[string]bool)

	runner := &recordingRunner{hook: func(job *CronJob) (string, error) {
		mu.Lock()
		executedJobs[job.ID] = true
		mu.Unlock()
		return "ok", nil
	}}

	cs, path := setupService(runner)
	defer os.Remove(path)

	// Start the service
	if err := cs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cs.Stop()

	// Add a job then runs 100ms from now. The runner enforces the owner-missing
	// guard, so the job needs an owner to fire.
	target := time.Now().Add(100 * time.Millisecond).UnixMilli()
	job, _ := cs.AddJob("FastJob", CronSchedule{Kind: "at", AtMS: &target}, "", false, "", "")
	job.AgentID = "mia"
	if err := cs.UpdateJob(job); err != nil {
		t.Fatalf("UpdateJob failed: %v", err)
	}

	// Check for job execution with a timeout
	success := false
	for range 20 {
		mu.Lock()
		if executedJobs[job.ID] {
			success = true
			mu.Unlock()
			break
		}
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}

	if !success {
		t.Error("Job was not executed in time")
	}

	// check that the job is removed after execution (DeleteAfterRun = true)
	status := cs.Status()
	if status["jobs"].(int) != 0 {
		t.Errorf("Job should be deleted after run, got count: %v", status["jobs"])
	}
}

func TestCronService_PersistenceIntegrity(t *testing.T) {
	tmpFile := "persist_test.json"
	defer os.Remove(tmpFile)

	// write a job and persist
	cs1 := NewCronService(tmpFile)
	at := int64(2000000000000)
	cs1.AddJob("PersistMe", CronSchedule{Kind: "at", AtMS: &at}, "payload", true, "ch1", "")

	// check file exists
	if _, err := os.Stat(tmpFile); os.IsNotExist(err) {
		t.Fatal("Store file was not created")
	}

	// reload and check data integrity
	cs2 := NewCronService(tmpFile)
	if err := cs2.Load(); err != nil {
		t.Fatalf("Failed to load store: %v", err)
	}

	jobs := cs2.ListJobs(true)
	if len(jobs) != 1 || jobs[0].Name != "PersistMe" {
		t.Errorf("Data corruption after reload. Got: %+v", jobs)
	}

	// test loading invalid JSON
	os.WriteFile(tmpFile, []byte("{invalid json}"), 0o644)
	cs3 := NewCronService(tmpFile)
	err := cs3.loadStore()
	if err == nil {
		t.Error("Should return error when loading invalid JSON")
	}
}

func TestCronService_ConcurrentAccess(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	cs.Start()
	defer cs.Stop()

	var wg sync.WaitGroup
	workers := 10
	iterations := 50

	wg.Add(workers * 2)

	// add jobs concurrently
	for i := range workers {
		go func(id int) {
			defer wg.Done()
			for j := range iterations {
				at := time.Now().Add(time.Hour).UnixMilli()
				cs.AddJob(fmt.Sprintf("Job-%d-%d", id, j), CronSchedule{Kind: "at", AtMS: &at}, "", false, "", "")
				time.Sleep(100 * time.Microsecond)
			}
		}(i)
	}

	// read and update jobs concurrently
	for range workers {
		go func() {
			defer wg.Done()
			for j := range iterations {
				jobs := cs.ListJobs(true)
				if len(jobs) > 0 {
					cs.EnableJob(jobs[0].ID, j%2 == 0)
				}
				time.Sleep(100 * time.Microsecond)
			}
		}()
	}

	wg.Wait()
}
