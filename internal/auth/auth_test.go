package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/ykakade/tollgate/internal/config"
)

var keyRE = regexp.MustCompile(`^tg_[0-9A-Za-z]{43}$`)

func TestNewKey(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
	}{
		{"zero bytes", make([]byte, 32)},
		{"max bytes", bytes.Repeat([]byte{0xff}, 32)},
		{"mixed bytes", []byte("0123456789abcdef0123456789abcdef")},
	}
	for _, tt := range tests {
		key, err := NewKey(bytes.NewReader(tt.in))
		if err != nil {
			t.Fatal(err)
		}
		if !keyRE.MatchString(key) {
			t.Errorf("%s: key %q does not match %s", tt.name, key, keyRE)
		}
	}
	if _, err := NewKey(bytes.NewReader(make([]byte, 31))); err == nil {
		t.Error("NewKey with 31 bytes: err = nil, want an error")
	}
}

func TestNewRandomKeyIsUnique(t *testing.T) {
	seen := make(map[string]bool)
	for range 100 {
		key, err := NewRandomKey()
		if err != nil {
			t.Fatal(err)
		}
		if seen[key] {
			t.Fatalf("key %q made two times", key)
		}
		seen[key] = true
	}
}

func TestHash(t *testing.T) {
	sum := sha256.Sum256([]byte("tg_abc"))
	if got, want := Hash("tg_abc"), "sha256:"+hex.EncodeToString(sum[:]); got != want {
		t.Errorf("Hash() = %q, want %q", got, want)
	}
}

func TestKeyring(t *testing.T) {
	laptop, _ := NewRandomKey()
	ci, _ := NewRandomKey()
	k, err := NewKeyring([]config.Tenant{
		{ID: "personal", Keys: []config.Key{{ID: "laptop", Hash: Hash(laptop)}}},
		{ID: "team", Keys: []config.Key{{ID: "ci", Hash: Hash(ci)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		key    string
		want   Identity
		wantOK bool
	}{
		{laptop, Identity{"personal", "laptop"}, true},
		{ci, Identity{"team", "ci"}, true},
		{laptop + "x", Identity{}, false},
		{strings.TrimPrefix(laptop, Prefix), Identity{}, false},
		{"", Identity{}, false},
	}
	for _, tt := range tests {
		got, ok := k.Find(tt.key)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("Find(%q) = %v, %v, want %v, %v", tt.key, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestNewKeyringErrors(t *testing.T) {
	h := Hash("tg_x")
	tests := []struct {
		name    string
		tenants []config.Tenant
	}{
		{"bad hash", []config.Tenant{{ID: "a", Keys: []config.Key{{ID: "k", Hash: "sha256:zz"}}}}},
		{"no prefix", []config.Tenant{{ID: "a", Keys: []config.Key{{ID: "k", Hash: strings.TrimPrefix(h, "sha256:")}}}}},
		{"duplicate", []config.Tenant{
			{ID: "a", Keys: []config.Key{{ID: "k", Hash: h}}},
			{ID: "b", Keys: []config.Key{{ID: "k", Hash: h}}},
		}},
	}
	for _, tt := range tests {
		if _, err := NewKeyring(tt.tenants); err == nil {
			t.Errorf("%s: err = nil, want an error", tt.name)
		}
	}
}

func TestFromHeader(t *testing.T) {
	tests := []struct {
		name   string
		header map[string]string
		want   string
		wantOK bool
	}{
		{"x-api-key", map[string]string{"X-Api-Key": "tg_a"}, "tg_a", true},
		{"bearer", map[string]string{"Authorization": "Bearer tg_b"}, "tg_b", true},
		{"bearer lowercase", map[string]string{"Authorization": "bearer tg_c"}, "tg_c", true},
		{"x-api-key wins", map[string]string{"X-Api-Key": "tg_a", "Authorization": "Bearer tg_b"}, "tg_a", true},
		{"basic auth", map[string]string{"Authorization": "Basic dXNlcg=="}, "", false},
		{"empty bearer", map[string]string{"Authorization": "Bearer "}, "", false},
		{"none", nil, "", false},
	}
	for _, tt := range tests {
		h := http.Header{}
		for k, v := range tt.header {
			h.Set(k, v)
		}
		got, ok := FromHeader(h)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("%s: FromHeader() = %q, %v, want %q, %v", tt.name, got, ok, tt.want, tt.wantOK)
		}
	}
}
