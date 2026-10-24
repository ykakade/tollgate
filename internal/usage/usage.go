// Package usage records one usage event for each request, and reads the
// spend back from the usage file at start.
//
// The request path puts events on a bounded Queue and never waits. If the
// queue is full, the event is dropped and counted. One goroutine writes the
// events to a Sink in batches.
package usage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// maxBatch is the largest number of events in one Sink write.
const maxBatch = 256

// Event is the record of one request. The fields are the columns of the
// usage_events table in the project brief.
type Event struct {
	RequestID     string    `json:"request_id"`
	TS            time.Time `json:"ts"`
	TenantID      string    `json:"tenant_id"`
	KeyID         string    `json:"key_id"`
	SessionID     string    `json:"session_id,omitempty"`
	RouteModel    string    `json:"route_model"`
	Provider      string    `json:"provider"`
	ProviderModel string    `json:"provider_model"`
	Status        int       `json:"status"`
	Attempts      int       `json:"attempts"`
	// Cache is "hit", "miss" or "bypass".
	Cache              string `json:"cache"`
	InputTokens        int64  `json:"input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	CacheReadTokens    int64  `json:"cache_read_tokens"`
	CacheWriteTokens   int64  `json:"cache_write_tokens"`
	CacheWrite1hTokens int64  `json:"cache_write_1h_tokens"`
	ReasoningTokens    int64  `json:"reasoning_tokens"`
	// UsageEstimated is true if the provider sent no usage.
	UsageEstimated bool  `json:"usage_estimated"`
	CostMicroUSD   int64 `json:"cost_micro_usd"`
	// TTFTMs is nil if no byte of the response arrived.
	TTFTMs     *int64 `json:"ttft_ms"`
	DurationMs int64  `json:"duration_ms"`
	OverheadUs int64  `json:"overhead_us"`
}

// Sink stores events.
type Sink interface {
	Write(ctx context.Context, events []Event) error
	Close() error
}

// Queue is a bounded queue of events in front of a Sink. Use NewQueue to
// make one, and Close to stop it.
type Queue struct {
	ctx     context.Context
	ch      chan Event
	sink    Sink
	logger  *slog.Logger
	dropped atomic.Int64
	closed  atomic.Bool
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once
}

// NewQueue starts a queue with room for size events. The writes to the
// sink use the values of ctx, but do not stop when ctx ends: use Close to
// stop the queue. If logger is nil, the queue logs nothing.
func NewQueue(ctx context.Context, sink Sink, size int, logger *slog.Logger) *Queue {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	q := &Queue{
		ctx:    context.WithoutCancel(ctx),
		ch:     make(chan Event, size),
		sink:   sink,
		logger: logger,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go q.run()
	return q
}

// Emit puts e on the queue. It never waits. It returns false, and counts
// the event as dropped, if the queue is full or closed.
func (q *Queue) Emit(e Event) bool {
	if q.closed.Load() {
		q.dropped.Add(1)
		return false
	}
	select {
	case q.ch <- e:
		return true
	default:
		q.dropped.Add(1)
		return false
	}
}

// Dropped returns the number of events that the queue did not write.
func (q *Queue) Dropped() int64 {
	return q.dropped.Load()
}

func (q *Queue) run() {
	defer close(q.done)
	batch := make([]Event, 0, maxBatch)
	for {
		select {
		case e := <-q.ch:
			batch = q.fill(append(batch[:0], e))
			q.write(batch)
		case <-q.stop:
			for {
				batch = q.fill(batch[:0])
				if len(batch) == 0 {
					return
				}
				q.write(batch)
			}
		}
	}
}

// fill adds the events that are on the queue to batch, up to maxBatch.
func (q *Queue) fill(batch []Event) []Event {
	for len(batch) < maxBatch {
		select {
		case e := <-q.ch:
			batch = append(batch, e)
		default:
			return batch
		}
	}
	return batch
}

func (q *Queue) write(batch []Event) {
	if err := q.sink.Write(q.ctx, batch); err != nil {
		q.dropped.Add(int64(len(batch)))
		q.logger.ErrorContext(q.ctx, "usage events not written", "events", len(batch), "error", err.Error())
	}
}

// Close stops the queue: Emit drops new events, and the events on the
// queue go to the sink. Then Close closes the sink. If ctx ends first,
// Close returns without the remaining events.
func (q *Queue) Close(ctx context.Context) error {
	q.once.Do(func() {
		q.closed.Store(true)
		close(q.stop)
	})
	select {
	case <-q.done:
	case <-ctx.Done():
		return fmt.Errorf("close usage queue: %w", ctx.Err())
	}
	if err := q.sink.Close(); err != nil && !errors.Is(err, errClosed) {
		return fmt.Errorf("close usage sink: %w", err)
	}
	return nil
}
