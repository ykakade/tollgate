package sse

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type event struct {
	Name string
	Data string
}

// parseAll feeds the stream to a new parser in chunks of the given size and
// returns the events.
func parseAll(t *testing.T, stream string, chunkSize, maxEvent int) ([]event, *Parser, error) {
	t.Helper()
	p := NewParser(maxEvent)
	var got []event
	fn := func(e Event) { got = append(got, event{e.Name, string(e.Data)}) }
	b := []byte(stream)
	for len(b) > 0 {
		n := min(chunkSize, len(b))
		if err := p.Feed(b[:n], fn); err != nil {
			return got, p, err
		}
		b = b[n:]
	}
	return got, p, nil
}

func TestParser(t *testing.T) {
	tests := []struct {
		name         string
		stream       string
		want         []event
		wantBoundary bool
	}{
		{
			name:         "named events",
			stream:       "event: message_start\ndata: {\"a\":1}\n\nevent: ping\ndata: {}\n\n",
			want:         []event{{"message_start", `{"a":1}`}, {"ping", "{}"}},
			wantBoundary: true,
		},
		{
			name:         "data only",
			stream:       "data: {\"x\":1}\n\ndata: [DONE]\n\n",
			want:         []event{{"", `{"x":1}`}, {"", "[DONE]"}},
			wantBoundary: true,
		},
		{
			name:         "CRLF line ends",
			stream:       "event: a\r\ndata: 1\r\n\r\n",
			want:         []event{{"a", "1"}},
			wantBoundary: true,
		},
		{
			name:         "CR line ends",
			stream:       "event: a\rdata: 1\r\r",
			want:         []event{{"a", "1"}},
			wantBoundary: true,
		},
		{
			name:         "multi-line data",
			stream:       "data: one\ndata: two\ndata\n\n",
			want:         []event{{"", "one\ntwo\n"}},
			wantBoundary: true,
		},
		{
			name:         "no space after colon",
			stream:       "data:x\n\n",
			want:         []event{{"", "x"}},
			wantBoundary: true,
		},
		{
			name:         "only one space removed",
			stream:       "data:  x\n\n",
			want:         []event{{"", " x"}},
			wantBoundary: true,
		},
		{
			name:         "comments and unknown fields",
			stream:       ": keep-alive\n\nid: 7\nretry: 10\nfoo: bar\ndata: y\n\n",
			want:         []event{{"", "y"}},
			wantBoundary: true,
		},
		{
			name:         "event with no data is not dispatched",
			stream:       "event: lonely\n\ndata: z\n\n",
			want:         []event{{"", "z"}},
			wantBoundary: true,
		},
		{
			name:         "byte order mark at start",
			stream:       "\xef\xbb\xbfdata: a\n\n",
			want:         []event{{"", "a"}},
			wantBoundary: true,
		},
		{
			name:         "incomplete line",
			stream:       "data: a\n\ndata: b",
			want:         []event{{"", "a"}},
			wantBoundary: false,
		},
		{
			name:         "event without blank line",
			stream:       "data: a\n\nevent: b\ndata: c\n",
			want:         []event{{"", "a"}},
			wantBoundary: false,
		},
		{
			name:         "empty stream",
			stream:       "",
			wantBoundary: true,
		},
	}
	for _, tt := range tests {
		for _, size := range []int{1, 2, 3, 7, 1 << 20} {
			got, p, err := parseAll(t, tt.stream, size, 1<<10)
			if err != nil {
				t.Fatalf("%s, chunk size %d: %v", tt.name, size, err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("%s, chunk size %d: events differ (-want +got):\n%s", tt.name, size, diff)
			}
			if p.AtBoundary() != tt.wantBoundary {
				t.Errorf("%s, chunk size %d: AtBoundary() = %v, want %v", tt.name, size, p.AtBoundary(), tt.wantBoundary)
			}
		}
	}
}

func TestParserLimit(t *testing.T) {
	tests := []struct {
		name   string
		stream string
	}{
		{"long line", "data: " + strings.Repeat("x", 100)},
		{"many data lines", strings.Repeat("data: xxxxxxxxxx\n", 20)}, {"long comment", ": " + strings.Repeat("c", 100) + "\n"},
	}
	for _, tt := range tests {
		for _, size := range []int{1, 1 << 20} {
			_, p, err := parseAll(t, tt.stream, size, 64)
			if !errors.Is(err, ErrEventTooLarge) {
				t.Fatalf("%s, chunk size %d: err = %v, want ErrEventTooLarge", tt.name, size, err)
			}
			if err2 := p.Feed([]byte("\n\n"), func(Event) {}); !errors.Is(err2, ErrEventTooLarge) {
				t.Errorf("%s: Feed after error = %v, want the same error", tt.name, err2)
			}
			if p.AtBoundary() {
				t.Errorf("%s: AtBoundary() = true after error", tt.name)
			}
		}
	}
}

func TestParserLimitResetsPerEvent(t *testing.T) {
	stream := strings.Repeat("data: "+strings.Repeat("x", 40)+"\n\n", 10)
	got, _, err := parseAll(t, stream, 5, 64)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 10 {
		t.Errorf("got %d events, want 10", len(got))
	}
}

func FuzzSSEParser(f *testing.F) {
	f.Add([]byte("event: a\ndata: 1\n\n"), 3)
	f.Add([]byte("data: x\r\ndata: y\r\n\r\n"), 1)
	f.Add([]byte(": c\rdata\r\r"), 2)
	f.Add([]byte("\xef\xbb\xbfdata: [DONE]\n\n"), 4)
	f.Fuzz(func(t *testing.T, stream []byte, chunkSize int) {
		if chunkSize <= 0 {
			chunkSize = 1
		}
		const limit = 256
		whole, _, wholeErr := parseAll(t, string(stream), len(stream)+1, limit)
		parts, _, partsErr := parseAll(t, string(stream), chunkSize, limit)
		if (wholeErr == nil) != (partsErr == nil) {
			t.Fatalf("whole err = %v, chunked err = %v", wholeErr, partsErr)
		}
		if wholeErr == nil && !cmp.Equal(whole, parts) {
			t.Fatalf("chunked parse differs from whole parse:\n%s", cmp.Diff(whole, parts))
		}
		for _, e := range whole {
			if len(e.Data) > limit {
				t.Fatalf("event data has %d bytes, limit is %d", len(e.Data), limit)
			}
			if bytes.ContainsAny([]byte(e.Name), "\r\n") {
				t.Fatalf("event name %q has a line end", e.Name)
			}
		}
	})
}
