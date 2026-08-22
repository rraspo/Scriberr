// Package remotehealth keeps the last known reachability observation for the
// remote execution host. Observations come only from events that contact the
// host anyway - a job actually running, or an explicit user-requested check -
// never from background polling: unsolicited probes can wake a sleeping
// wake-on-LAN host, so the absence of a fresh observation is presented as
// staleness rather than refreshed automatically.
package remotehealth

import (
	"sync"
	"time"
)

// Observation is one reachability fact about the remote host set.
type Observation struct {
	Reachable bool
	Source    string // "job" or "check"
	At        time.Time
}

var (
	mutex   sync.Mutex
	current *Observation
)

// Record stores the most recent reachability observation.
func Record(reachable bool, source string) {
	mutex.Lock()
	defer mutex.Unlock()
	current = &Observation{Reachable: reachable, Source: source, At: time.Now()}
}

// Last returns the most recent observation, if any exists since startup.
func Last() (Observation, bool) {
	mutex.Lock()
	defer mutex.Unlock()
	if current == nil {
		return Observation{}, false
	}
	return *current, true
}

// Reset clears the stored observation. It exists for tests.
func Reset() {
	mutex.Lock()
	defer mutex.Unlock()
	current = nil
}
