package usage

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// maxLineBytes is the largest line that ReadSpend reads. An event line is
// much smaller. ReadSpend skips a longer line.
const maxLineBytes = 64 << 10

var errClosed = errors.New("usage file is closed")

// JSONL is a Sink that appends one JSON line for each event to a file.
// Use OpenJSONL to make one.
type JSONL struct {
	mu  sync.Mutex
	f   *os.File
	buf []byte
}

// OpenJSONL opens the file at path for append, and makes the file and its
// directory if they do not exist. If the last line of the file is not
// complete, for example after a crash, it adds a line end, so that the
// next event starts on a new line.
func OpenJSONL(path string) (*JSONL, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("make usage directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // The operator gives the path in the configuration.
	if err != nil {
		return nil, fmt.Errorf("open usage file: %w", err)
	}
	if err := endLine(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &JSONL{f: f}, nil
}

// endLine adds a line end if the file does not end with one.
func endLine(f *os.File) error {
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("read usage file: %w", err)
	}
	if st.Size() == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, st.Size()-1); err != nil {
		return fmt.Errorf("read usage file: %w", err)
	}
	if last[0] != '\n' {
		if _, err := f.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("write usage file: %w", err)
		}
	}
	return nil
}

// Write implements Sink. It writes all events with one write call.
func (j *JSONL) Write(_ context.Context, events []Event) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return errClosed
	}
	j.buf = j.buf[:0]
	for i := range events {
		b, err := json.Marshal(&events[i])
		if err != nil {
			return fmt.Errorf("encode usage event: %w", err)
		}
		j.buf = append(append(j.buf, b...), '\n')
	}
	if _, err := j.f.Write(j.buf); err != nil {
		return fmt.Errorf("write usage file: %w", err)
	}
	return nil
}

// Close implements Sink. It writes the file to the disk and closes it.
func (j *JSONL) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.f == nil {
		return errClosed
	}
	f := j.f
	j.f = nil
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync usage file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close usage file: %w", err)
	}
	return nil
}

// SessionSpend is the spend of one session.
type SessionSpend struct {
	TenantID  string
	SessionID string
	Cost      int64
	// Last is the time of the last event of the session.
	Last time.Time
}

// Spend is the spend that ReadSpend found.
type Spend struct {
	// Month holds the spend of each tenant in the month of now.
	Month map[string]int64
	// Sessions holds the sessions with an event in the session window
	// before now.
	Sessions []SessionSpend
	// Events is the number of events that ReadSpend read.
	Events int
	// Bad is the number of lines that are not events.
	Bad int
}

// ReadSpend reads the usage file at path and adds the cost of each event:
// for each tenant in the month of now (UTC), and for each session with an
// event in the window before now. A missing file has no spend. ReadSpend
// skips lines that are not events, for example a partial last line.
func ReadSpend(path string, now time.Time, window time.Duration) (Spend, error) {
	sp := Spend{Month: make(map[string]int64)}
	f, err := os.Open(path) //nolint:gosec // The operator gives the path in the configuration.
	if errors.Is(err, fs.ErrNotExist) {
		return sp, nil
	}
	if err != nil {
		return sp, fmt.Errorf("open usage file: %w", err)
	}
	defer f.Close()
	return readSpend(f, now, window)
}

func readSpend(r io.Reader, now time.Time, window time.Duration) (Spend, error) {
	sp := Spend{Month: make(map[string]int64)}
	type sessionKey struct{ tenant, session string }
	sessions := make(map[sessionKey]*SessionSpend)
	y, m, _ := now.UTC().Date()
	var err error
	sp.Events, sp.Bad, err = scan(r, func(e *Event) {
		if ey, em, _ := e.TS.UTC().Date(); ey == y && em == m {
			sp.Month[e.TenantID] += e.CostMicroUSD
		}
		if e.SessionID != "" {
			k := sessionKey{e.TenantID, e.SessionID}
			s := sessions[k]
			if s == nil {
				s = &SessionSpend{TenantID: e.TenantID, SessionID: e.SessionID}
				sessions[k] = s
			}
			s.Cost += e.CostMicroUSD
			if e.TS.After(s.Last) {
				s.Last = e.TS
			}
		}
	})
	if err != nil {
		return sp, err
	}
	for _, s := range sessions {
		if now.Sub(s.Last) < window {
			sp.Sessions = append(sp.Sessions, *s)
		}
	}
	slices.SortFunc(sp.Sessions, func(a, b SessionSpend) int {
		return cmp.Or(strings.Compare(a.TenantID, b.TenantID), strings.Compare(a.SessionID, b.SessionID))
	})
	return sp, nil
}

// scan reads the events of a usage file and calls fn for each event. It
// skips lines that are not events, for example a partial last line, and
// lines that are longer than maxLineBytes. It returns the number of events
// and of bad lines.
func scan(r io.Reader, fn func(e *Event)) (events, bad int, err error) {
	add := func(line []byte) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			return
		}
		var e Event
		if err := json.Unmarshal(line, &e); err != nil || e.TenantID == "" {
			bad++
			return
		}
		events++
		fn(&e)
	}
	br := bufio.NewReaderSize(r, maxLineBytes)
	for {
		line, rerr := br.ReadSlice('\n')
		if errors.Is(rerr, bufio.ErrBufferFull) {
			// Skip the rest of a line that is too long.
			bad++
			for errors.Is(rerr, bufio.ErrBufferFull) {
				_, rerr = br.ReadSlice('\n')
			}
		} else {
			add(line)
		}
		if errors.Is(rerr, io.EOF) {
			return events, bad, nil
		}
		if rerr != nil {
			return events, bad, fmt.Errorf("read usage file: %w", rerr)
		}
	}
}

// ReadEvents reads the usage file at path and calls fn for each event. A
// missing file has no events. It returns the number of events and of bad
// lines.
func ReadEvents(path string, fn func(e *Event)) (events, bad int, err error) {
	f, err := os.Open(path) //nolint:gosec // The operator gives the path in the configuration.
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("open usage file: %w", err)
	}
	defer f.Close()
	return scan(f, fn)
}
