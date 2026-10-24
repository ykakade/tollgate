package limiter

import (
	"context"
	"sync"
	"time"
)

// sweepEvery is the time between two removals of expired counters.
const sweepEvery = time.Minute

// Memory is a Store in the memory of the process. It is for a single
// Tollgate copy. It uses the same key names as the Redis store. Use
// NewMemory to make one.
type Memory struct {
	mu        sync.Mutex
	items     map[string]item
	lastSweep time.Time
}

type item struct {
	v       int64
	expires time.Time
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{items: make(map[string]item)}
}

// get returns the value of key at now, or 0 if it expired.
func (m *Memory) get(key string, now time.Time) int64 {
	it, ok := m.items[key]
	if !ok || !now.Before(it.expires) {
		return 0
	}
	return it.v
}

// add adds delta to key. The value does not go below 0.
func (m *Memory) add(key string, delta int64, ttl time.Duration, now time.Time) {
	m.items[key] = item{v: max(m.get(key, now)+delta, 0), expires: now.Add(ttl)}
}

// sweep removes expired counters, at most once each sweepEvery.
func (m *Memory) sweep(now time.Time) {
	if now.Sub(m.lastSweep) < sweepEvery {
		return
	}
	m.lastSweep = now
	for k, it := range m.items {
		if !now.Before(it.expires) {
			delete(m.items, k)
		}
	}
}

// Admit implements Store.
func (m *Memory) Admit(_ context.Context, r *Request) (Decision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := r.Now
	m.sweep(now)
	n, elapsed := minute(now)
	c := counts{
		cur:   make([]int64, len(r.Rates)),
		prev:  make([]int64, len(r.Rates)),
		spend: make([]int64, len(r.Budgets)),
	}
	for i, rt := range r.Rates {
		c.cur[i] = m.get(rateKey(rt.Key, n), now)
		c.prev[i] = m.get(rateKey(rt.Key, n-1), now)
	}
	for j, b := range r.Budgets {
		c.spend[j] = m.get(b.Key, now)
	}
	idx := denied(r, c, elapsed)
	if idx == 0 {
		for _, rt := range r.Rates {
			m.add(rateKey(rt.Key, n), rt.Cost, rateTTL, now)
		}
		for _, b := range r.Budgets {
			m.add(b.Key, b.Amount, b.TTL, now)
		}
	}
	return decide(r, c, idx, n, elapsed), nil
}

// Settle implements Store.
func (m *Memory) Settle(_ context.Context, s *Settlement) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := s.Now
	if n, _ := minute(now); s.Minute >= n-1 {
		for _, a := range s.Rates {
			m.add(rateKey(a.Key, s.Minute), a.Delta, rateTTL, now)
		}
	}
	for _, a := range s.Budgets {
		m.add(a.Key, a.Delta, a.TTL, now)
	}
	return nil
}

// Ping implements Store. The memory store is always available.
func (m *Memory) Ping(context.Context) error { return nil }

// Value returns the value of key at now. Tests use it.
func (m *Memory) Value(key string, now time.Time) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.get(key, now)
}
