package respcache

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func TestNewKey(t *testing.T) {
	h := http.Header{"Anthropic-Version": {"2023-06-01"}}
	base := NewKey("a", "anthropic", "m", h, []byte(`{"x":1}`))
	if base != NewKey("a", "anthropic", "m", h.Clone(), []byte(`{"x":1}`)) {
		t.Error("the same request has two keys")
	}
	beta := h.Clone()
	beta.Set("Anthropic-Beta", "b1")
	for name, k := range map[string]Key{
		"tenant":  NewKey("b", "anthropic", "m", h, []byte(`{"x":1}`)),
		"api":     NewKey("a", "openai", "m", h, []byte(`{"x":1}`)),
		"route":   NewKey("a", "anthropic", "n", h, []byte(`{"x":1}`)),
		"version": NewKey("a", "anthropic", "m", http.Header{"Anthropic-Version": {"2024-01-01"}}, []byte(`{"x":1}`)),
		"beta":    NewKey("a", "anthropic", "m", beta, []byte(`{"x":1}`)),
		"body":    NewKey("a", "anthropic", "m", h, []byte(`{"x":2}`)),
	} {
		if k == base {
			t.Errorf("a different %s gives the same key", name)
		}
	}
}

func TestCache(t *testing.T) {
	c := New(3*(itemOverhead+100), 100)
	key := func(i int) Key { return NewKey("t", "a", "m", nil, []byte{byte(i)}) }
	body := []byte(strings.Repeat("x", 100))
	for i := range 3 {
		c.Put(key(i), &Entry{Body: body}, t0, time.Minute)
	}
	if n, size := c.Stats(); n != 3 || size != 3*(itemOverhead+100) {
		t.Fatalf("Stats = %d, %d", n, size)
	}
	// Use 0, so 1 is the least recently used.
	if _, ok := c.Get(key(0), t0); !ok {
		t.Fatal("miss for 0")
	}
	c.Put(key(3), &Entry{Body: body}, t0, time.Minute)
	if _, ok := c.Get(key(1), t0); ok {
		t.Error("1 is still in the cache; the cache did not remove the least recently used entry")
	}
	for _, i := range []int{0, 2, 3} {
		if _, ok := c.Get(key(i), t0); !ok {
			t.Errorf("miss for %d", i)
		}
	}
	if _, ok := c.Get(key(0), t0.Add(time.Minute)); ok {
		t.Error("an expired entry was a hit")
	}
	if n, _ := c.Stats(); n != 2 {
		t.Errorf("the expired entry is still in the cache: %d entries", n)
	}
	c.Put(key(9), &Entry{Body: append(body, 'y')}, t0, time.Minute)
	if _, ok := c.Get(key(9), t0); ok {
		t.Error("the cache kept an entry larger than the entry limit")
	}
}
