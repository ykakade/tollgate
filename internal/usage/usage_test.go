package usage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// memSink is a Sink that keeps the events. If gate is not nil, Write waits
// for it.
type memSink struct {
	mu     sync.Mutex
	events []Event
	gate   chan struct{}
	err    error
	closed bool
}

func (s *memSink) Write(_ context.Context, events []Event) error {
	if s.gate != nil {
		<-s.gate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.events = append(s.events, events...)
	return nil
}

func (s *memSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *memSink) ids() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.events {
		out = append(out, e.RequestID)
	}
	return out
}

func TestQueueWritesAllOnClose(t *testing.T) {
	sink := &memSink{}
	q := NewQueue(t.Context(), sink, 1000, nil)
	var want []string
	for i := range 600 {
		id := string(rune('a'+i%26)) + strings.Repeat("x", i/26)
		want = append(want, id)
		if !q.Emit(Event{RequestID: id}) {
			t.Fatalf("Emit() %d = false", i)
		}
	}
	if err := q.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, sink.ids()); diff != "" {
		t.Errorf("events differ (-want +got):\n%s", diff)
	}
	if !sink.closed || q.Dropped() != 0 {
		t.Errorf("closed %v, dropped %d", sink.closed, q.Dropped())
	}
	if q.Emit(Event{RequestID: "late"}) || q.Dropped() != 1 {
		t.Error("Emit() after Close() was not dropped")
	}
	if err := q.Close(t.Context()); err != nil {
		t.Errorf("second Close() = %v", err)
	}
}

func TestQueueDropsWhenFull(t *testing.T) {
	sink := &memSink{gate: make(chan struct{})}
	q := NewQueue(t.Context(), sink, 2, nil)
	q.Emit(Event{RequestID: "1"})
	// Wait until the worker holds event 1 in a blocked Write.
	deadline := time.Now().Add(2 * time.Second)
	for len(q.ch) != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	ok := 0
	for range 5 {
		if q.Emit(Event{RequestID: "n"}) {
			ok++
		}
	}
	if ok != 2 || q.Dropped() != 3 {
		t.Errorf("queued %d, dropped %d; want 2 and 3", ok, q.Dropped())
	}
	close(sink.gate)
	if err := q.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := len(sink.ids()); got != 3 {
		t.Errorf("wrote %d events, want 3", got)
	}
}

func TestQueueCountsWriteErrors(t *testing.T) {
	sink := &memSink{err: errors.New("disk full")}
	q := NewQueue(t.Context(), sink, 10, nil)
	q.Emit(Event{RequestID: "1"})
	q.Emit(Event{RequestID: "2"})
	if err := q.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if q.Dropped() != 2 {
		t.Errorf("Dropped() = %d, want 2", q.Dropped())
	}
}

func TestQueueCloseTimeout(t *testing.T) {
	sink := &memSink{gate: make(chan struct{})}
	defer close(sink.gate)
	q := NewQueue(t.Context(), sink, 10, nil)
	q.Emit(Event{RequestID: "1"})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := q.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Close() = %v, want a deadline error", err)
	}
}

func event(id, tenant, session string, ts time.Time, cost int64) Event {
	return Event{RequestID: id, TS: ts, TenantID: tenant, KeyID: "k", SessionID: session, Cache: "bypass", CostMicroUSD: cost}
}

func TestJSONLAndReadSpend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "usage.jsonl")
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	j, err := OpenJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	events := []Event{
		event("1", "a", "", now.Add(-time.Hour), 100),
		event("2", "a", "s1", now.Add(-2*time.Hour), 200),
		event("3", "b", "s1", now.Add(-time.Minute), 7),
		// Last month: counts for the session, not for the month.
		event("4", "a", "old", time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC), 1000),
		// A session with its last event before the window is not kept.
		event("5", "a", "s1", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), 50),
		event("6", "a", "gone", now.Add(-25*time.Hour), 9),
	}
	if err := j.Write(t.Context(), events[:3]); err != nil {
		t.Fatal(err)
	}
	if err := j.Write(t.Context(), events[3:]); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := j.Write(t.Context(), events); !errors.Is(err, errClosed) {
		t.Errorf("Write() after Close() = %v", err)
	}
	if err := j.Close(); !errors.Is(err, errClosed) {
		t.Errorf("second Close() = %v", err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("usage file mode: %v, %v; want 0600", st, err)
	}

	// A crash leaves a partial line. The next open ends it, so the next
	// event is on its own line.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"request_id":"partial","tenant_id":"a","cost_micro_`)
	_ = f.Close()
	j, err = OpenJSONL(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Write(t.Context(), []Event{event("7", "a", "", now, 1)}); err != nil {
		t.Fatal(err)
	}
	_ = j.Close()

	sp, err := ReadSpend(path, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(map[string]int64{"a": 100 + 200 + 50 + 9 + 1, "b": 7}, sp.Month); diff != "" {
		t.Errorf("month spend differs (-want +got):\n%s", diff)
	}
	wantSessions := []SessionSpend{
		{TenantID: "a", SessionID: "s1", Cost: 250, Last: now.Add(-2 * time.Hour)},
		{TenantID: "b", SessionID: "s1", Cost: 7, Last: now.Add(-time.Minute)},
	}
	if diff := cmp.Diff(wantSessions, sp.Sessions); diff != "" {
		t.Errorf("sessions differ (-want +got):\n%s", diff)
	}
	if sp.Events != 7 || sp.Bad != 1 {
		t.Errorf("events %d, bad %d; want 7 and 1", sp.Events, sp.Bad)
	}
}

func TestReadSpendEdgeCases(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	sp, err := ReadSpend(filepath.Join(t.TempDir(), "none.jsonl"), now, time.Hour)
	if err != nil || len(sp.Month) != 0 || sp.Events != 0 {
		t.Errorf("ReadSpend() of a missing file = %+v, %v", sp, err)
	}
	long := strings.Repeat("x", maxLineBytes*2)
	in := long + "\n" + `{"tenant_id":"a","ts":"2026-10-02T00:00:00Z","cost_micro_usd":5}` + "\n\n" + `{"ts":"2026-10-02T00:00:00Z"}` + "\n" + `{"tenant_id":"a","ts":"2026-10-03T00:00:00Z","cost_micro_usd":6}`
	sp, err = readSpend(strings.NewReader(in), now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Month["a"] != 11 || sp.Events != 2 || sp.Bad != 2 {
		t.Errorf("spend %v, events %d, bad %d; want a=11, 2 events, 2 bad lines", sp.Month, sp.Events, sp.Bad)
	}
	if _, err := ReadSpend(t.TempDir(), now, time.Hour); err == nil {
		t.Error("ReadSpend() of a directory returned no error")
	}
}
