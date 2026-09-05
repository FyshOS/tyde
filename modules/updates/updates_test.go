package updates

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2/test"
)

// TestMain disables the background schedule and runs UI callbacks inline. The
// Fyne test driver executes fyne.Do on the calling goroutine, so a scheduled
// check firing from a goroutine would race every test in this package.
func TestMain(m *testing.M) {
	test.NewTempApp(&testing.T{})

	autoStart = false
	doOnMain = func(fn func()) { fn() }

	os.Exit(m.Run())
}

// fakeBackend stands in for a package manager so checker behaviour can be
// tested without touching the real system.
type fakeBackend struct {
	res  Result
	err  error
	runs atomic.Int32
}

func (f *fakeBackend) Name() string          { return "fake" }
func (f *fakeBackend) UpgradeArgs() []string { return []string{"/bin/true"} }

func (f *fakeBackend) Check(context.Context) (Result, error) {
	f.runs.Add(1)
	return f.res, f.err
}

func newTestChecker(b Backend) *Checker {
	return &Checker{backend: b, done: make(chan struct{})}
}

// TestCheckerRestoresResultAcrossRestart covers the boot case: the schedule
// skips a check when the last one was recent, so a Checker that started with an
// empty result would hide pending updates until the next check came due.
func TestCheckerRestoresResultAcrossRestart(t *testing.T) {
	b := &fakeBackend{res: Result{Updates: []Update{{Name: "glibc", OldVersion: "1", NewVersion: "2"}}}}
	newTestChecker(b).Check()

	fresh := newTestChecker(b)
	fresh.load()

	res, _, checked, _ := fresh.State()
	if len(res.Updates) != 1 || res.Updates[0].Name != "glibc" {
		t.Errorf("result not restored: %+v", res.Updates)
	}
	if checked.IsZero() {
		t.Error("check time not restored")
	}
	if b.runs.Load() != 1 {
		t.Errorf("restoring should not re-run the backend, ran %d times", b.runs.Load())
	}
}

func TestCheckerStoresResult(t *testing.T) {
	b := &fakeBackend{res: Result{Updates: []Update{{Name: "glibc", OldVersion: "1", NewVersion: "2"}}}}
	c := newTestChecker(b)
	c.Check()

	res, checking, checked, err := c.State()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if checking {
		t.Error("still reporting a check in progress after it completed")
	}
	if len(res.Updates) != 1 || res.Updates[0].Name != "glibc" {
		t.Errorf("unexpected updates: %+v", res.Updates)
	}
	if checked.IsZero() {
		t.Error("expected a check timestamp")
	}
}

// TestCheckerKeepsLastGoodResult guards the property that a transient failure
// must not make a pending update vanish from the status area.
func TestCheckerKeepsLastGoodResult(t *testing.T) {
	b := &fakeBackend{res: Result{Updates: []Update{{Name: "glibc"}}}}
	c := newTestChecker(b)
	c.Check()

	b.err = errors.New("network unreachable")
	c.Check()

	res, _, _, err := c.State()
	if err == nil {
		t.Fatal("expected the failure to be reported")
	}
	if len(res.Updates) != 1 {
		t.Errorf("the previous result was discarded on failure: %+v", res.Updates)
	}
}

func TestCheckerNotifiesListeners(t *testing.T) {
	c := newTestChecker(&fakeBackend{})

	var calls int
	c.SetListener("test", func() { calls++ })
	c.Check()

	// One notification when the check starts, one when it finishes, so a view
	// can show progress rather than only the outcome.
	if calls < 2 {
		t.Errorf("expected at least 2 notifications, got %d", calls)
	}
}

// TestCheckerStaleIsNotRecordedAsChecked ensures a degraded result does not
// suppress retries for a full day.
func TestCheckerStaleIsNotRecordedAsChecked(t *testing.T) {
	c := newTestChecker(&fakeBackend{res: Result{Stale: true, StaleReason: "no fakeroot"}})
	c.Check()

	if got := c.lastCheck(); !got.IsZero() {
		t.Errorf("a stale result was recorded as a completed check: %v", got)
	}
}

// TestCheckerRestartsAfterStop covers the module being destroyed and rebuilt
// when the user changes module settings: the shared Checker must resume rather
// than stay permanently stopped.
func TestCheckerRestartsAfterStop(t *testing.T) {
	c := newTestChecker(&fakeBackend{})

	c.Start()
	c.Stop()
	c.Start()
	defer c.Stop()

	c.mu.Lock()
	running := c.running
	c.mu.Unlock()
	if !running {
		t.Error("the schedule did not resume after a stop/start cycle")
	}
}

// TestCheckerStartDelaysFirstCheck covers the first check staying off the
// critical path during login rather than firing the moment the module loads.
func TestCheckerStartDelaysFirstCheck(t *testing.T) {
	b := &fakeBackend{}
	c := newTestChecker(b)

	defer swapStartupDelay(time.Hour)() // long enough that a prompt check fails the test
	c.Start()
	defer c.Stop()

	time.Sleep(time.Millisecond * 50)
	if got := b.runs.Load(); got != 0 {
		t.Errorf("expected the first check to be delayed, but it ran %d times", got)
	}
}

// TestCheckerStopCancelsPendingFirstCheck ensures a module torn down during the
// startup delay does not still fire a check afterwards.
func TestCheckerStopCancelsPendingFirstCheck(t *testing.T) {
	b := &fakeBackend{}
	c := newTestChecker(b)

	defer swapStartupDelay(time.Millisecond * 20)()
	c.Start()
	c.Stop()

	time.Sleep(time.Millisecond * 80)
	if got := b.runs.Load(); got != 0 {
		t.Errorf("a stopped checker still ran %d checks", got)
	}
}

// swapStartupDelay sets the startup delay for one test, returning a restore func.
func swapStartupDelay(d time.Duration) func() {
	old := startupDelay
	startupDelay = d
	return func() { startupDelay = old }
}

// TestCheckerListenersAreKeyed covers views being rebuilt: a replacement must
// displace its predecessor rather than accumulate alongside it.
func TestCheckerListenersAreKeyed(t *testing.T) {
	c := newTestChecker(&fakeBackend{})

	var first, second int
	c.SetListener("status", func() { first++ })
	c.SetListener("status", func() { second++ })
	c.Check()

	if first != 0 {
		t.Errorf("the replaced listener was still called %d times", first)
	}
	if second == 0 {
		t.Error("the replacement listener was never called")
	}
}

func TestCheckerNoBackendIsInert(t *testing.T) {
	c := newTestChecker(nil)
	c.Start() // must not panic or schedule anything
	c.Check()

	if res, _, _, _ := c.State(); len(res.Updates) != 0 {
		t.Errorf("expected no updates without a backend, got %+v", res.Updates)
	}
}

func TestCheckerConcurrentChecksCollapse(t *testing.T) {
	b := &blockingBackend{release: make(chan struct{})}
	c := newTestChecker(b)

	go c.Check()
	// Wait for the first check to be in flight before racing a second one.
	for i := 0; i < 100; i++ {
		if _, checking, _, _ := c.State(); checking {
			break
		}
		time.Sleep(time.Millisecond)
	}

	c.Check() // must return immediately rather than starting a second sync
	close(b.release)

	if got := b.runs.Load(); got > 1 {
		t.Errorf("expected concurrent checks to collapse, got %d runs", got)
	}
}

type blockingBackend struct {
	release chan struct{}
	runs    atomic.Int32
}

func (b *blockingBackend) Name() string          { return "blocking" }
func (b *blockingBackend) UpgradeArgs() []string { return []string{"/bin/true"} }

func (b *blockingBackend) Check(context.Context) (Result, error) {
	b.runs.Add(1)
	<-b.release
	return Result{}, nil
}
