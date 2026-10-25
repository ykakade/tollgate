// Package respcache is the exact response cache of Tollgate (ADR 0030).
//
// The cache keeps complete, successful responses in the memory of the
// process, up to a total size, and removes the least recently used
// responses first. The key of a response is a hash of the tenant, the API,
// the route, the API version headers and the exact request body, so two
// tenants never share a response, and a request that differs in one byte
// is a miss.
package respcache

import (
	"container/list"
	"crypto/sha256"
	"net/http"
	"sync"
	"time"
)

// Key is the key of one response.
type Key [sha256.Size]byte

// NewKey returns the key of a request. Header is the client request
// header: the anthropic-version and anthropic-beta values are part of the
// key, because they change the response.
func NewKey(tenant, api, route string, header http.Header, body []byte) Key {
	h := sha256.New()
	for _, part := range []string{tenant, api, route, header.Get("Anthropic-Version")} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	for _, v := range header.Values("Anthropic-Beta") {
		h.Write([]byte(v))
		h.Write([]byte{','})
	}
	h.Write([]byte{0})
	h.Write(body)
	var k Key
	h.Sum(k[:0])
	return k
}

// Entry is one cached response.
type Entry struct {
	ContentType string
	Body        []byte
	// Provider and Model are the target that made the response.
	Provider, Model string
	// CostMicroUSD is the cost of the response when the provider made it.
	CostMicroUSD int64
}

type item struct {
	key     Key
	entry   *Entry
	expires time.Time
	size    int64
}

// Cache is an LRU cache with a size limit. Use New to make one. It is safe
// for concurrent use.
type Cache struct {
	maxBytes, maxEntry int64

	mu    sync.Mutex
	size  int64
	order *list.List // front: most recently used
	items map[Key]*list.Element
}

// itemOverhead is an estimate of the memory of one item without its body.
const itemOverhead = 256

// New returns a cache that keeps at most maxBytes, and no response larger
// than maxEntry bytes.
func New(maxBytes, maxEntry int64) *Cache {
	return &Cache{maxBytes: maxBytes, maxEntry: maxEntry, order: list.New(), items: make(map[Key]*list.Element)}
}

// MaxEntry returns the size of the largest response that the cache keeps.
func (c *Cache) MaxEntry() int64 { return c.maxEntry }

// Get returns the response of k, if the cache has it and it did not
// expire at now.
func (c *Cache) Get(k Key, now time.Time) (*Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return nil, false
	}
	it := el.Value.(*item)
	if !now.Before(it.expires) {
		c.remove(el)
		return nil, false
	}
	c.order.MoveToFront(el)
	return it.entry, true
}

// Put keeps e for k until now + ttl. It does nothing if e is larger than
// the entry limit.
func (c *Cache) Put(k Key, e *Entry, now time.Time, ttl time.Duration) {
	size := int64(len(e.Body)) + itemOverhead
	if int64(len(e.Body)) > c.maxEntry || size > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[k]; ok {
		c.remove(el)
	}
	for c.size+size > c.maxBytes {
		c.remove(c.order.Back())
	}
	c.items[k] = c.order.PushFront(&item{key: k, entry: e, expires: now.Add(ttl), size: size})
	c.size += size
}

func (c *Cache) remove(el *list.Element) {
	it := el.Value.(*item)
	c.order.Remove(el)
	delete(c.items, it.key)
	c.size -= it.size
}

// Stats returns the number of responses and their size in bytes, with the
// overhead of each item.
func (c *Cache) Stats() (entries int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items), c.size
}
