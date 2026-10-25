package fakeprovider

import (
	"bytes"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"sync"
	"time"
)

// maxCacheEntries is the size at which the prompt cache removes its
// expired entries.
const maxCacheEntries = 10000

// promptCache simulates the prompt cache of a provider. It keeps a hash of
// each prefix of a request: the system member and the messages up to each
// message. A later request reads the longest prefix that is in the cache
// and did not expire, and writes the rest of its prompt to the cache. Each
// use of an entry starts its lifetime again, as the Anthropic prompt cache
// does.
//
// This is a simulation for tests and benchmarks. The token counts use the
// ratio of the fake provider, not the tokenizer of a real model.
type promptCache struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[[sha256.Size]byte]time.Time
}

func newPromptCache(ttl time.Duration) *promptCache {
	return &promptCache{ttl: ttl, entries: make(map[[sha256.Size]byte]time.Time)}
}

// promptParts holds the members of a request body that make its prefixes.
type promptParts struct {
	System   jsontext.Value   `json:"system"`
	Messages []jsontext.Value `json:"messages"`
}

// use returns the cache read and cache write tokens of a request body at
// now, and adds its prefixes to the cache.
func (c *promptCache) use(body []byte, now time.Time) (read, write int) {
	var p promptParts
	if json.Unmarshal(body, &p) != nil || len(p.Messages) == 0 {
		return 0, 0
	}
	h := sha256.New()
	h.Write(p.System)
	size := len(p.System)
	keys := make([][sha256.Size]byte, len(p.Messages))
	sizes := make([]int, len(p.Messages))
	for i, m := range p.Messages {
		h.Write([]byte{0})
		h.Write(bytes.TrimSpace(m))
		size += len(m)
		h.Sum(keys[i][:0])
		sizes[i] = size
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	readBytes := 0
	for i := len(keys) - 1; i >= 0; i-- {
		if exp, ok := c.entries[keys[i]]; ok && now.Before(exp) {
			readBytes = sizes[i]
			break
		}
	}
	if len(c.entries) > maxCacheEntries {
		for k, exp := range c.entries {
			if !now.Before(exp) {
				delete(c.entries, k)
			}
		}
	}
	for _, k := range keys {
		c.entries[k] = now.Add(c.ttl)
	}
	read = tokensFor(readBytes)
	return read, tokensFor(size) - read
}

// tokensFor returns the token count of n bytes, with the ratio of the fake
// provider.
func tokensFor(n int) int {
	return (n + bytesPerInputToken - 1) / bytesPerInputToken
}
