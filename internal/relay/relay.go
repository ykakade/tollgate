// Package relay sends an upstream response to the client without changes,
// gives a copy of the bytes to a meter, and applies the four timeouts of an
// upstream request (ADR 0009, ADR 0016).
//
// The timeouts are:
//
//   - connect: from the start of the attempt until a connection is ready.
//   - first token: from a ready connection until the first body byte.
//   - idle: the longest wait for the next body bytes after the first byte.
//     It is also the write deadline for each chunk to the client.
//   - total: the longest time for the whole attempt.
//
// When a timeout occurs, the attempt context is canceled with one of the
// Err timeout values as its cause. The cancel closes the upstream
// connection.
package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

// The causes of an attempt that ended early.
var (
	ErrConnectTimeout    = errors.New("connect timeout")
	ErrFirstTokenTimeout = errors.New("first token timeout")
	ErrIdleTimeout       = errors.New("idle timeout")
	ErrTotalTimeout      = errors.New("total timeout")
	ErrClientWrite       = errors.New("client write failed")
	// errDone is the cause after Stop. It is not visible to callers.
	errDone = errors.New("attempt done")
)

// bufferSize is the size of the copy buffer. It is the most memory that
// one relay keeps for a slow client.
const bufferSize = 32 << 10

// Timeouts are the four timeouts of an attempt.
type Timeouts struct {
	Connect    time.Duration
	FirstToken time.Duration
	Idle       time.Duration
	Total      time.Duration
}

// Attempt is one upstream request with its timeouts. Use Start to make one,
// and call Stop when the response body is closed.
type Attempt struct {
	ctx       context.Context
	cancel    context.CancelCauseFunc
	stopTotal context.CancelFunc
	timeouts  Timeouts

	mu       sync.Mutex
	timer    *time.Timer
	cause    error // the cause that the timer gives when it fires
	deadline time.Time
	stopped  bool

	firstByte time.Time
}

// Start starts the connect timer and returns the attempt. Send the
// upstream request with the context of Context, so the attempt knows when
// the connection is ready.
func Start(parent context.Context, t Timeouts) *Attempt {
	totalCtx, stopTotal := context.WithTimeoutCause(parent, t.Total, ErrTotalTimeout)
	ctx, cancel := context.WithCancelCause(totalCtx)
	a := &Attempt{cancel: cancel, stopTotal: stopTotal, timeouts: t, cause: ErrConnectTimeout, deadline: time.Now().Add(t.Connect)}
	a.timer = time.AfterFunc(t.Connect, a.fire)
	a.ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { a.arm(t.FirstToken, ErrFirstTokenTimeout) },
	})
	return a
}

// Context returns the context for the upstream request.
func (a *Attempt) Context() context.Context { return a.ctx }

// Err returns the cause if the attempt ended early: a timeout, the
// ErrClientWrite error, or the cause of the parent context. It returns nil
// if the attempt did not end early.
func (a *Attempt) Err() error {
	if a.ctx.Err() == nil {
		return nil
	}
	if cause := context.Cause(a.ctx); !errors.Is(cause, errDone) {
		return cause
	}
	return nil
}

// FirstByte returns the time of the first body byte, or the zero time if
// no body byte came.
func (a *Attempt) FirstByte() time.Time { return a.firstByte }

// Stop stops the timers and releases the attempt context. Call it after the
// response body is closed.
func (a *Attempt) Stop() {
	a.mu.Lock()
	a.stopped = true
	a.timer.Stop()
	a.mu.Unlock()
	a.cancel(errDone)
	a.stopTotal()
}

func (a *Attempt) arm(d time.Duration, cause error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopped {
		return
	}
	a.cause = cause
	a.deadline = time.Now().Add(d)
	a.timer.Reset(d)
}

func (a *Attempt) pause() {
	a.mu.Lock()
	a.deadline = time.Time{}
	a.timer.Stop()
	a.mu.Unlock()
}

func (a *Attempt) fire() {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Stop and Reset do not wait for an AfterFunc callback that already
	// started. A callback from an earlier read must not cancel a later read.
	if a.stopped || a.deadline.IsZero() {
		return
	}
	if remaining := time.Until(a.deadline); remaining > 0 {
		a.timer.Reset(remaining)
		return
	}
	a.cancel(a.cause)
}

// Copy reads body until it ends and writes each chunk to w at once, with a
// flush. After each write, it gives the same bytes to meter, if meter is
// not nil. It returns the number of bytes written to w.
//
// If the attempt ends early, Copy returns its cause (see Err). If a write to
// the client fails, Copy cancels the upstream request and returns an error
// that wraps ErrClientWrite.
func (a *Attempt) Copy(w http.ResponseWriter, body io.Reader, meter io.Writer) (int64, error) {
	rc := http.NewResponseController(w)
	defer func() { _ = rc.SetWriteDeadline(time.Time{}) }()
	buf := make([]byte, bufferSize)
	var written int64
	for {
		if !a.firstByte.IsZero() {
			a.arm(a.timeouts.Idle, ErrIdleTimeout)
		}
		n, rerr := body.Read(buf)
		if n > 0 {
			a.pause()
			if a.firstByte.IsZero() {
				a.firstByte = time.Now()
			}
			if err := a.write(rc, w, buf[:n]); err != nil {
				a.cancel(ErrClientWrite)
				return written, fmt.Errorf("%w: %w", ErrClientWrite, err)
			}
			written += int64(n)
			if meter != nil {
				_, _ = meter.Write(buf[:n])
			}
		}
		if errors.Is(rerr, io.EOF) {
			return written, nil
		}
		if rerr != nil {
			if cause := a.Err(); cause != nil {
				return written, cause
			}
			return written, fmt.Errorf("read upstream body: %w", rerr)
		}
	}
}

func (a *Attempt) write(rc *http.ResponseController, w io.Writer, p []byte) error {
	if err := rc.SetWriteDeadline(time.Now().Add(a.timeouts.Idle)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return fmt.Errorf("set write deadline: %w", err)
	}
	if _, err := w.Write(p); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := rc.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	return nil
}
