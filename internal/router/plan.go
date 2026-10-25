package router

// Stop tells why a Plan has no next attempt.
type Stop int

// The reasons for no next attempt.
const (
	// NoTarget: no target after the current one can get an attempt.
	NoTarget Stop = iota
	// MaxAttempts: the request used retry.max_attempts.
	MaxAttempts
	// BudgetExhausted: the retry budget has no room.
	BudgetExhausted
)

// Skip is a target that a Plan did not try, and the reason.
type Skip struct {
	Index  int
	Reason Blocked
}

// Plan walks the ordered targets of one request. Use Router.Plan to make
// one. A Plan is not safe for concurrent use.
type Plan struct {
	r    *Router
	keys []Key
	max  int
	// order holds the target indexes in the order of the attempts.
	order []int

	// cur is the position in order of the current target.
	cur      int
	attempts int
	retried  bool // The current target had its one RetrySame.
	ticket   Ticket
	skipped  []Skip
}

// Plan returns a plan for a request with the targets keys, in order, and
// at most maxAttempts attempts. It counts the request for the retry
// budget.
func (r *Router) Plan(keys []Key, maxAttempts int) *Plan {
	return r.PlanFrom(keys, maxAttempts, 0)
}

// PlanFrom is Plan with the target first in front of the other targets,
// for sticky routing. The other targets keep their order. If first is not
// a valid index, the order does not change.
func (r *Router) PlanFrom(keys []Key, maxAttempts, first int) *Plan {
	r.request()
	order := make([]int, 0, len(keys))
	if first > 0 && first < len(keys) {
		order = append(order, first)
	}
	for i := range keys {
		if i != first || len(order) == 0 {
			order = append(order, i)
		}
	}
	return &Plan{r: r, keys: keys, max: maxAttempts, order: order, cur: -1}
}

// First returns the index of the target of the first attempt. It returns
// false if no target can get an attempt, for example because all breakers
// are open.
func (p *Plan) First() (int, bool) {
	return p.find(0)
}

// Next returns the index of the target of the next attempt after an attempt
// with the action a. Call Report for the current attempt first. It returns
// false and the reason if the request has no next attempt.
func (p *Plan) Next(a Action) (int, Stop, bool) {
	if a == Done {
		return 0, NoTarget, false
	}
	if p.attempts >= p.max {
		return 0, MaxAttempts, false
	}
	start := p.cur + 1
	if a == RetrySame && !p.retried {
		start = p.cur
	}
	for pos := start; pos < len(p.order); pos++ {
		i := p.order[pos]
		same := pos == p.cur
		t, why := p.r.Allow(p.keys[i])
		if why != NotBlocked {
			if !same {
				p.skipped = append(p.skipped, Skip{Index: i, Reason: why})
			}
			continue
		}
		if !p.r.tryRetry() {
			p.r.Report(t, Neutral)
			return 0, BudgetExhausted, false
		}
		p.use(pos, t)
		p.retried = same
		return i, NoTarget, true
	}
	return 0, NoTarget, false
}

func (p *Plan) find(start int) (int, bool) {
	for pos := start; pos < len(p.order); pos++ {
		i := p.order[pos]
		t, why := p.r.Allow(p.keys[i])
		if why != NotBlocked {
			p.skipped = append(p.skipped, Skip{Index: i, Reason: why})
			continue
		}
		p.use(pos, t)
		return i, true
	}
	return 0, false
}

func (p *Plan) use(pos int, t Ticket) {
	p.cur = pos
	p.ticket = t
	p.attempts++
}

// Report records the outcome of the current attempt for its breaker. Call
// it one time for each attempt.
func (p *Plan) Report(o Outcome) {
	p.r.Report(p.ticket, o)
	p.ticket = Ticket{}
}

// Attempts returns the number of attempts that the plan started.
func (p *Plan) Attempts() int { return p.attempts }

// Skipped returns the targets that the plan did not try.
func (p *Plan) Skipped() []Skip { return p.skipped }
