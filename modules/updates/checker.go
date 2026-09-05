package updates

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"fyne.io/fyne/v2"
)

const (
	// checkInterval is how often we look for new updates.
	checkInterval = time.Hour * 24
	// tickInterval is how often we test whether checkInterval has elapsed.
	// Waking hourly rather than arming a single 24 hour timer means a machine
	// that suspends for a few days notices as soon as it resumes.
	tickInterval = time.Hour
	// checkTimeout bounds a single check, so an unreachable mirror leaves the
	// UI reporting a timeout rather than spinning until the next tick.
	checkTimeout = time.Minute * 10

	prefLastCheck = "updates.lastCheck"
	prefResult    = "updates.result"
)

// startupDelay holds the first check back until the desktop has settled.
// Checking hits the network and spawns package manager processes, which is the
// last thing login needs to contend with; a few seconds late costs nothing for
// something that only repeats daily. It is a variable so tests can shorten it.
var startupDelay = time.Second * 5

// doOnMain marshals a UI mutation onto the render thread. It is a variable so
// tests can substitute a direct call: the test driver runs fyne.Do on the
// calling goroutine, which races when a background goroutine invokes it.
var doOnMain = func(fn func()) { fyne.Do(fn) }

// Checker owns the update state shared by the status area indicator and the
// settings panel, so the two always agree and a single check serves both.
type Checker struct {
	backend Backend

	mu        sync.Mutex
	result    Result
	err       error
	checked   time.Time
	checking  bool
	running   bool
	listeners map[string]func()
	done      chan struct{}
}

var (
	sharedOnce sync.Once
	shared     *Checker
)

// Shared returns the process-wide Checker, detecting the package manager on
// first use. Its Backend is nil when the system's package manager is not
// supported; callers must handle that rather than assuming a backend exists.
func Shared() *Checker {
	sharedOnce.Do(func() {
		shared = &Checker{backend: DetectBackend()}
		shared.load()
	})
	return shared
}

// Backend reports the detected package manager, or nil if none is supported.
func (c *Checker) Backend() Backend {
	return c.backend
}

// State returns the most recent result, the error from the last attempt (if it
// failed), whether a check is running, and when the last check completed (zero
// if none has).
func (c *Checker) State() (res Result, checking bool, checked time.Time, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.result, c.checking, c.checked, c.err
}

// SetListener registers fn to run on the render thread whenever the state
// changes, replacing any previous listener under the same key. It is called for
// every transition, including the start and end of a check, so a view can show
// progress as well as results.
//
// Listeners are keyed rather than merely appended because the Checker outlives
// its views: the status widget is rebuilt whenever module settings change and
// the settings panel is rebuilt every time the window opens. Keying means each
// rebuild displaces its predecessor instead of leaving a listener holding a
// discarded widget alive forever.
func (c *Checker) SetListener(key string, fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.listeners == nil {
		c.listeners = map[string]func(){}
	}
	c.listeners[key] = fn
}

// Start begins the daily schedule, running an initial check if more than a day
// has passed since the last one. The recorded time persists across restarts so
// logging in repeatedly does not re-hit the mirrors each time.
//
// Start and Stop may be called repeatedly: the Checker is a process-wide
// singleton, but the module wrapping it is destroyed and rebuilt whenever the
// user changes their module settings, so each new instance restarts the
// schedule the previous one stopped.
func (c *Checker) Start() {
	if c.backend == nil {
		return
	}

	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	done := make(chan struct{})
	c.done = done
	c.mu.Unlock()

	delay := startupDelay // read here, so the goroutine holds no mutable package state
	go func() {
		// A Stop during the delay must cancel the pending first check, not have
		// it fire into a module that has already been torn down.
		select {
		case <-done:
			return
		case <-time.After(delay):
		}

		if time.Since(c.lastCheck()) >= checkInterval {
			c.Check()
		}

		tick := time.NewTicker(tickInterval)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				if time.Since(c.lastCheck()) >= checkInterval {
					c.Check()
				}
			}
		}
	}()
}

// Stop ends the daily schedule. A later Start resumes it.
func (c *Checker) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return
	}
	c.running = false
	close(c.done)
}

// Check runs a check now, blocking until it completes. Concurrent calls collapse
// into the running one rather than queueing a second sync of the same data.
func (c *Checker) Check() {
	if c.backend == nil {
		return
	}

	c.mu.Lock()
	if c.checking {
		c.mu.Unlock()
		return
	}
	c.checking = true
	c.mu.Unlock()
	c.notify()

	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	res, err := c.backend.Check(ctx)

	c.mu.Lock()
	c.checking = false
	c.err = err
	if err == nil {
		// Only replace the list on success: keeping the last good result means a
		// transient network failure does not make a pending update disappear
		// from the status area.
		c.result = res
		c.checked = time.Now()

		// A stale result is not worth resting on for a full day - record it as
		// unchecked so the next tick retries the refresh rather than waiting.
		if res.Stale {
			c.setLastCheck(time.Time{})
		} else {
			c.setLastCheck(c.checked)
		}
		c.save(res, c.checked)
	}
	c.mu.Unlock()
	c.notify()
}

// notify runs the listeners on the render thread.
func (c *Checker) notify() {
	c.mu.Lock()
	listeners := make([]func(), 0, len(c.listeners))
	for _, fn := range c.listeners {
		listeners = append(listeners, fn)
	}
	c.mu.Unlock()

	if len(listeners) == 0 {
		return
	}
	doOnMain(func() {
		for _, fn := range listeners {
			fn()
		}
	})
}

// MarkStale forgets when the last check ran, so the next scheduled tick checks
// again. Used after applying updates to refresh the pending list.
func (c *Checker) MarkStale() {
	c.setLastCheck(time.Time{})
}

// snapshot is the persisted form of a completed check. The result is stored
// alongside the time it was taken because the two are one fact: the schedule
// deliberately skips a check when the last one was recent, so without the
// result the indicator would have nothing to show until the next check was due
// - a whole day of hiding pending updates after every boot.
type snapshot struct {
	Updates     []Update
	Stale       bool
	StaleReason string
	Checked     int64 // Unix seconds, zero if unknown
}

// load restores the last recorded result, so the indicator is correct from the
// moment the desktop starts rather than from the first check of this session.
func (c *Checker) load() {
	app := fyne.CurrentApp()
	if app == nil {
		return
	}
	data := app.Preferences().String(prefResult)
	if data == "" {
		return
	}

	var snap snapshot
	if err := json.Unmarshal([]byte(data), &snap); err != nil {
		// A snapshot written by an older version is not worth reporting: the
		// pending check replaces it shortly anyway.
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.result = Result{Updates: snap.Updates, Stale: snap.Stale, StaleReason: snap.StaleReason}
	if snap.Checked != 0 {
		c.checked = time.Unix(snap.Checked, 0)
	}
}

// save records a completed check. Callers hold c.mu.
func (c *Checker) save(res Result, checked time.Time) {
	app := fyne.CurrentApp()
	if app == nil {
		return
	}

	data, err := json.Marshal(snapshot{
		Updates: res.Updates, Stale: res.Stale,
		StaleReason: res.StaleReason, Checked: checked.Unix(),
	})
	if err != nil {
		return
	}
	app.Preferences().SetString(prefResult, string(data))
}

func (c *Checker) lastCheck() time.Time {
	app := fyne.CurrentApp()
	if app == nil {
		return time.Time{}
	}
	sec := app.Preferences().Int(prefLastCheck)
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(int64(sec), 0)
}

func (c *Checker) setLastCheck(t time.Time) {
	app := fyne.CurrentApp()
	if app == nil {
		return
	}
	if t.IsZero() {
		app.Preferences().SetInt(prefLastCheck, 0)
		return
	}
	app.Preferences().SetInt(prefLastCheck, int(t.Unix()))
}
