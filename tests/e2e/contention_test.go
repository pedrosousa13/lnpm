package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// contentionRounds is how many times each worker launches a real lnpm process
// against the shared store. The two workers re-synchronize at the start of
// every round, so a handful of rounds is enough to guarantee genuinely
// overlapping processes without slowing the suite down.
const contentionRounds = 6

// invocation records the wall-clock window of one real lnpm process, so the
// test can prove the two workers' processes actually overlapped rather than
// assuming the start barrier did its job.
type invocation struct {
	worker string
	round  int
	start  time.Time
	end    time.Time
}

// overlap returns how long two process lifetimes intersected in wall time. A
// non-positive result means they never coexisted.
func (i invocation) overlap(o invocation) time.Duration {
	end := i.end
	if o.end.Before(end) {
		end = o.end
	}
	start := i.start
	if o.start.After(start) {
		start = o.start
	}
	return end.Sub(start)
}

// indicatesLockContention reports whether a failed lnpm run failed *because*
// another process held the store's bbolt lock.
//
// internal/db opens bbolt with a lock timeout and turns bbolt's ErrTimeout into
// "another lnpm process appears to be running (database is locked): timeout".
// Anything else — a missing package, a bad manifest, a panic — is deliberately
// NOT matched: those must fail the test.
//
// Deliberately not matching bare "timeout": this classifier decides whether a
// failure is acceptable, so a marker broad enough to appear in unrelated output
// would reclassify a real bug as an expected outcome. Erring wide here is the
// dangerous direction. A reword of the wrapper cannot slip past unnoticed
// either way — internal/db's own test pins the phrase (lockMessage, "another
// lnpm process"), so it fails first.
func indicatesLockContention(out string) bool {
	lower := strings.ToLower(out)
	for _, marker := range []string{
		"another lnpm process appears to be running",
		"database is locked",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// TestConcurrentProcessesSharedStore drives two real lnpm processes at ONE
// shared store at the same time and proves that:
//
//   - every invocation either succeeds or fails with output that clearly names
//     lock contention (any other failure fails the test),
//   - the two processes genuinely overlapped in wall time (otherwise the test
//     measures nothing),
//   - the store is still usable afterwards and holds exactly the content of the
//     last successful publish from each worker — i.e. contention did not corrupt
//     or silently drop state.
//
// This test is the deliberate exception to the per-test-store rule described on
// newStore: every other e2e test takes a private store so parallel binaries
// never fight over the bbolt file lock, whereas this one shares a store
// precisely to make them fight. It therefore does NOT call t.Parallel() — it
// must not compete with the rest of the suite for CPU while it is timing
// overlapping processes.
//
// It needs no node runtime (nothing is require()d here), so it runs everywhere
// Go runs.
func TestConcurrentProcessesSharedStore(t *testing.T) {
	store := newStore(t)

	const pkgA, pkgB = "contend-lib-a", "contend-lib-b"
	repoA := makePkgDir(t, pkgA, `module.exports = "a-v0";`+"\n")
	repoB := makePkgDir(t, pkgB, `module.exports = "b-v0";`+"\n")
	app := makeAppDir(t, "contend-app")

	// Warm-up. Only pkg-a reaches the store here; pkg-b is published solely by
	// worker B inside the contention phase, so the health check below cannot
	// pass unless worker B really did work.
	runLNPM(t, store, repoA, "publish")
	runLNPM(t, store, app, "add", pkgA)

	body := func(worker string, round int) string {
		return fmt.Sprintf("module.exports = %q;\n", fmt.Sprintf("%s-v%d", strings.ToLower(worker), round))
	}

	workers := []struct{ name, dir string }{{"A", repoA}, {"B", repoB}}

	var (
		mu          sync.Mutex
		invocations []invocation
		unexpected  []string
		contended   int
		lastOK      = map[string]int{}
	)

	for round := 1; round <= contentionRounds; round++ {
		// Each worker republishes its own package with fresh content, so every
		// round is a real write to the shared database, not a no-op. The files
		// are written from the test goroutine (writeFile uses t.Fatalf) and
		// each worker only ever touches its own repo.
		writeFile(t, filepath.Join(repoA, "index.js"), body("A", round))
		writeFile(t, filepath.Join(repoB, "index.js"), body("B", round))

		// A per-round barrier: both goroutines block on start, so the two
		// processes are launched within microseconds of each other and their
		// lifetimes are forced to overlap. Re-synchronizing every round is what
		// keeps the overlap deterministic instead of hoping one long-running
		// loop happens to interleave with the other.
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, w := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start

				begin := time.Now()
				out, err := runLNPMErr(t, store, w.dir, "publish")
				rec := invocation{worker: w.name, round: round, start: begin, end: time.Now()}

				mu.Lock()
				defer mu.Unlock()
				invocations = append(invocations, rec)
				switch {
				case err == nil:
					lastOK[w.name] = round
				case indicatesLockContention(out):
					contended++
				default:
					unexpected = append(unexpected,
						fmt.Sprintf("worker %s, round %d: %v\n%s", w.name, round, err, out))
				}
			}()
		}
		close(start)
		wg.Wait()
	}

	// AC: any failure that is not lock contention is a bug.
	if len(unexpected) > 0 {
		t.Fatalf("%d lnpm invocation(s) failed for reasons other than store lock contention:\n%s",
			len(unexpected), strings.Join(unexpected, "\n"))
	}

	// Overlap evidence. Without it the rest of the test proves nothing: two
	// processes that never coexist never contend.
	overlapping := 0
	minOverlap := time.Duration(0)
	for round := 1; round <= contentionRounds; round++ {
		a, okA := invocationFor(invocations, "A", round)
		b, okB := invocationFor(invocations, "B", round)
		if !okA || !okB {
			continue
		}
		if d := a.overlap(b); d > 0 {
			if overlapping == 0 || d < minOverlap {
				minOverlap = d
			}
			overlapping++
		}
	}
	if overlapping == 0 {
		t.Fatalf("no round had overlapping lnpm processes across %d rounds — the workers ran sequentially, so this test exercised no contention at all\n%s",
			contentionRounds, describeInvocations(invocations))
	}
	// The contention count is normally 0: internal/db waits up to openTimeout
	// (30s) for the lock, and a publish takes milliseconds, so the loser of each
	// round blocks and then wins rather than failing. That is the behaviour
	// being pinned — overlapping processes both complete correctly.
	//
	// The contention branch above is therefore a regression guard, not dead
	// code: shortening openTimeout to 1ms makes exactly one invocation per round
	// report contention and the test still passes, which is what confirms these
	// processes genuinely fight for the bbolt lock rather than merely coexisting.
	t.Logf("contention: %d/%d rounds had genuinely overlapping lnpm processes (shortest overlap %v), %d invocation(s) reported lock contention",
		overlapping, contentionRounds, minOverlap, contended)

	// Both workers must have landed at least one publish, otherwise "no
	// failures" is vacuously true.
	for _, w := range workers {
		if lastOK[w.name] == 0 {
			t.Fatalf("worker %s never completed a successful publish, so it never touched the shared store", w.name)
		}
	}

	// The store must still be usable, and must serve exactly the content of the
	// last publish that succeeded for each package — proving contention neither
	// corrupted the database nor lost a committed write.
	runLNPM(t, store, app, "add", pkgA, pkgB)
	for _, tc := range []struct {
		pkg, worker string
	}{{pkgA, "A"}, {pkgB, "B"}} {
		assertSymlink(t, filepath.Join(app, "node_modules", tc.pkg))
		assertDepValue(t, app, tc.pkg, "file:.lnpm/"+tc.pkg)

		linked := filepath.Join(app, ".lnpm", tc.pkg, "index.js")
		got, err := os.ReadFile(linked)
		if err != nil {
			t.Fatalf("failed to read linked %s after contention: %v", linked, err)
		}
		if want := body(tc.worker, lastOK[tc.worker]); string(got) != want {
			t.Fatalf("after contention, %s holds %q, want %q (last successful publish was round %d)",
				linked, got, want, lastOK[tc.worker])
		}
	}

	// A final read-side command must still work against the shared database.
	status := runLNPM(t, store, app, "status")
	for _, pkg := range []string{pkgA, pkgB} {
		if !strings.Contains(status, pkg) {
			t.Fatalf("expected `lnpm status` to list %s after contention, got:\n%s", pkg, status)
		}
	}
}

// invocationFor returns the recorded process window for a worker in a round.
func invocationFor(all []invocation, worker string, round int) (invocation, bool) {
	for _, inv := range all {
		if inv.worker == worker && inv.round == round {
			return inv, true
		}
	}
	return invocation{}, false
}

// describeInvocations renders every recorded process window relative to the
// earliest start, so a failure message shows why the workers did not overlap.
func describeInvocations(all []invocation) string {
	if len(all) == 0 {
		return "(no invocations recorded)"
	}
	base := all[0].start
	for _, inv := range all {
		if inv.start.Before(base) {
			base = inv.start
		}
	}
	var b strings.Builder
	for _, inv := range all {
		fmt.Fprintf(&b, "  worker %s round %d: %v -> %v\n",
			inv.worker, inv.round, inv.start.Sub(base), inv.end.Sub(base))
	}
	return b.String()
}
