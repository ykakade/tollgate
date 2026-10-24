// Package sse parses server-sent event streams in increments.
//
// The parser follows the event stream format of the WHATWG HTML standard. It
// reads the bytes of a stream in chunks of any size, so a caller can give it
// a copy of each chunk that it sends to a client. It keeps only the current
// line and the current event in memory, up to a fixed limit.
package sse

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrEventTooLarge is the error when one event is larger than the limit of
// the parser.
var ErrEventTooLarge = errors.New("sse event too large")

// Event is one dispatched event.
type Event struct {
	// Name is the value of the last event field, or empty if the event has
	// no event field.
	Name string
	// Data is the value of the data fields, joined with "\n". It is valid
	// only until the callback returns.
	Data []byte
}

// Parser reads a stream in chunks. Use NewParser to make one.
type Parser struct {
	max int

	line    []byte // the part of the current line that is not complete
	name    string
	data    []byte
	hasData bool
	// pending is true if the current event has a field line but no blank
	// line after it yet.
	pending bool
	// lastCR is true if the last chunk ended with "\r". A "\n" at the start
	// of the next chunk is then part of the same line end.
	lastCR bool
	// started is true after the first line, so a byte order mark is removed
	// only at the start of the stream.
	started bool
	err     error
}

// NewParser returns a parser with a limit of maxEventBytes. The limit
// applies to each line and to the data of each event.
func NewParser(maxEventBytes int) *Parser {
	return &Parser{max: maxEventBytes}
}

// Feed parses chunk and calls fn for each complete event in it. After an
// error, Feed does nothing and returns the same error again.
func (p *Parser) Feed(chunk []byte, fn func(Event)) error {
	if p.err != nil {
		return p.err
	}
	if len(chunk) == 0 {
		return nil
	}
	if p.lastCR && chunk[0] == '\n' {
		chunk = chunk[1:]
	}
	p.lastCR = false
	for len(chunk) > 0 {
		i := bytes.IndexAny(chunk, "\r\n")
		if i < 0 {
			if err := p.check(len(p.line) + len(chunk)); err != nil {
				return err
			}
			p.line = append(p.line, chunk...)
			return nil
		}
		if err := p.check(len(p.line) + i); err != nil {
			return err
		}
		line := chunk[:i]
		if len(p.line) > 0 {
			p.line = append(p.line, line...)
			line = p.line
		}
		end := i + 1
		if chunk[i] == '\r' {
			switch {
			case end < len(chunk) && chunk[end] == '\n':
				end++
			case end == len(chunk):
				p.lastCR = true
			}
		}
		// The line can share memory with p.line. The next append to p.line
		// happens after processLine, which copies what it keeps.
		p.line = p.line[:0]
		if err := p.processLine(line, fn); err != nil {
			return err
		}
		chunk = chunk[end:]
	}
	return nil
}

// AtBoundary reports whether the bytes fed so far end between two events:
// no line is incomplete and no event waits for its blank line. A writer can
// add a new event to the stream only at a boundary.
func (p *Parser) AtBoundary() bool {
	return p.err == nil && len(p.line) == 0 && !p.pending
}

// check returns an error if n is more than the limit of the parser.
func (p *Parser) check(n int) error {
	if n > p.max {
		p.err = fmt.Errorf("%w: limit is %d bytes", ErrEventTooLarge, p.max)
		return p.err
	}
	return nil
}

func (p *Parser) processLine(line []byte, fn func(Event)) error {
	if !p.started {
		p.started = true
		line = bytes.TrimPrefix(line, []byte("\xef\xbb\xbf"))
	}
	if len(line) == 0 {
		p.dispatch(fn)
		return nil
	}
	p.pending = true
	if line[0] == ':' {
		return nil
	}
	field, value, found := bytes.Cut(line, []byte(":"))
	if found {
		value = bytes.TrimPrefix(value, []byte(" "))
	}
	switch string(field) {
	case "event":
		p.name = string(value)
	case "data":
		if err := p.check(len(p.data) + len(value) + 1); err != nil {
			return err
		}
		if p.hasData {
			p.data = append(p.data, '\n')
		}
		p.data = append(p.data, value...)
		p.hasData = true
	}
	return nil
}

// dispatch sends the current event to fn and resets the event. As the
// standard says, an event with no data field is not dispatched.
func (p *Parser) dispatch(fn func(Event)) {
	if p.hasData {
		fn(Event{Name: p.name, Data: p.data})
	}
	p.name = ""
	p.data = p.data[:0]
	p.hasData = false
	p.pending = false
}
