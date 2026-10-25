package router

import "time"

// State is the state of a circuit breaker. The values are the values of
// the breaker_state metric of the brief.
type State int

// The breaker states.
const (
	// Closed: attempts go to the target.
	Closed State = iota
	// HalfOpen: one test attempt can go to the target. Its outcome closes
	// or opens the breaker again.
	HalfOpen
	// Open: no attempt goes to the target until the open time ends.
	Open
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case HalfOpen:
		return "half-open"
	case Open:
		return "open"
	}
	return "unknown"
}

// breaker is the state of one Key. The Router lock protects it.
type breaker struct {
	key   Key
	state State
	// gen changes at each state change, so that Report can ignore the
	// outcome of an attempt from an earlier state.
	gen       uint64
	window    counter // Attempts and failures while closed.
	openUntil time.Time
	probing   bool
	coolUntil time.Time
}

// Window is the time that a breaker and the retry budget look back.
const Window = time.Minute

// buckets is the number of parts of the window.
const buckets = 6

// counter counts two values in a sliding window of buckets.
type counter struct {
	b [buckets]struct {
		slot int64 // The number of the bucket since the Unix epoch.
		a, b int64
	}
}

func slot(t time.Time) int64 {
	return t.UnixNano() / int64(Window/buckets)
}

// add adds a and b to the bucket of now.
func (c *counter) add(now time.Time, a, b int64) {
	s := slot(now)
	e := &c.b[s%buckets]
	if e.slot != s {
		e.slot, e.a, e.b = s, 0, 0
	}
	e.a += a
	e.b += b
}

// sum returns the totals of the buckets in the window that ends at now.
func (c *counter) sum(now time.Time) (a, b int64) {
	s := slot(now)
	for _, e := range c.b {
		if e.slot > s-buckets && e.slot <= s {
			a += e.a
			b += e.b
		}
	}
	return a, b
}
