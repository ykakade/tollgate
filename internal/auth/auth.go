// Package auth makes virtual keys and finds the tenant of a key.
//
// A virtual key is "tg_" and 32 random bytes in base62. Tollgate stores only
// the SHA-256 hash of each key (ADR 0012). A fixed-length random key has
// enough entropy that a fast hash is safe, and a map lookup on the hash does
// not leak the key through timing: the hash of a guess tells an attacker
// nothing about the hash of the real key.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"

	"github.com/ykakade/tollgate/internal/config"
)

// Prefix is the start of every virtual key. Secret scanners use it to find
// keys that leaked.
const Prefix = "tg_"

const (
	keyBytes = 32
	// keyChars is the length of the base62 part of a key. 62^43 is more
	// than 2^256, so 43 characters hold any 32-byte value.
	keyChars = 43
)

// NewKey makes a key from 32 bytes of r. Use crypto/rand.Reader as r,
// except in tests.
func NewKey(r io.Reader) (string, error) {
	b := make([]byte, keyBytes)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	text := new(big.Int).SetBytes(b).Text(62)
	return Prefix + strings.Repeat("0", keyChars-len(text)) + text, nil
}

// NewRandomKey makes a key from crypto/rand.
func NewRandomKey() (string, error) {
	return NewKey(rand.Reader)
}

// Hash returns the hash of key in the format of the configuration file:
// "sha256:" and 64 lowercase hex digits.
func Hash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return config.HashPrefix + hex.EncodeToString(sum[:])
}

// Identity is the tenant and key that a request uses.
type Identity struct {
	TenantID string
	KeyID    string
}

// Keyring finds the identity of a key. It is safe for concurrent use,
// because it does not change after NewKeyring.
type Keyring struct {
	byHash map[[sha256.Size]byte]Identity
}

// NewKeyring makes a keyring from the tenants of a validated configuration.
func NewKeyring(tenants []config.Tenant) (*Keyring, error) {
	k := &Keyring{byHash: make(map[[sha256.Size]byte]Identity)}
	for _, t := range tenants {
		for _, key := range t.Keys {
			h, err := parseHash(key.Hash)
			if err != nil {
				return nil, fmt.Errorf("key %s of tenant %s: %w", key.ID, t.ID, err)
			}
			if _, dup := k.byHash[h]; dup {
				return nil, fmt.Errorf("key %s of tenant %s: %w", key.ID, t.ID, errDuplicateHash)
			}
			k.byHash[h] = Identity{TenantID: t.ID, KeyID: key.ID}
		}
	}
	return k, nil
}

var (
	errBadHash       = errors.New("hash is not sha256 hex")
	errDuplicateHash = errors.New("hash is used two times")
)

func parseHash(s string) ([sha256.Size]byte, error) {
	var h [sha256.Size]byte
	hexPart, ok := strings.CutPrefix(s, config.HashPrefix)
	if !ok || hex.DecodedLen(len(hexPart)) != sha256.Size {
		return h, errBadHash
	}
	if _, err := hex.Decode(h[:], []byte(hexPart)); err != nil {
		return h, errBadHash
	}
	return h, nil
}

// Find returns the identity of key. The bool is false if the key is not
// known.
func (k *Keyring) Find(key string) (Identity, bool) {
	if !strings.HasPrefix(key, Prefix) {
		return Identity{}, false
	}
	id, ok := k.byHash[sha256.Sum256([]byte(key))]
	return id, ok
}

// FromHeader returns the key of a request. Anthropic clients send it in
// x-api-key. OpenAI clients, and Claude Code with ANTHROPIC_AUTH_TOKEN, send
// it as a bearer token. The bool is false if the request has no key.
func FromHeader(h http.Header) (string, bool) {
	if key := h.Get("X-Api-Key"); key != "" {
		return key, true
	}
	scheme, token, ok := strings.Cut(h.Get("Authorization"), " ")
	if ok && strings.EqualFold(scheme, "Bearer") {
		token = strings.TrimSpace(token)
		return token, token != ""
	}
	return "", false
}
