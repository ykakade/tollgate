package limiter

import (
	"os"
	"testing"
)

// redisURL returns the URL of the test Redis server from
// TOLLGATE_TEST_REDIS_URL. If the variable is not set, the test is
// skipped. CI sets it.
func redisURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("TOLLGATE_TEST_REDIS_URL")
	if u == "" {
		t.Skip("TOLLGATE_TEST_REDIS_URL is not set")
	}
	return u
}

func newTestRedis(t *testing.T) *Redis {
	t.Helper()
	s, err := NewRedis(redisURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestRedis(t *testing.T) {
	redisURL(t)
	storeSuite(t, func(t *testing.T) Store { return newTestRedis(t) })
}

func TestRedisURLErrorHasNoPassword(t *testing.T) {
	_, err := NewRedis("redis://user:hunter2@[bad")
	if err == nil {
		t.Fatal("NewRedis() of a bad URL returned no error")
	}
	if got := err.Error(); got != "parse redis url: the url is not valid" {
		t.Errorf("error = %q", got)
	}
}

func TestRedisDown(t *testing.T) {
	// Nothing listens on port 1.
	s, err := NewRedis("redis://127.0.0.1:1/0")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.Admit(t.Context(), &Request{Now: at(1), Rates: []Rate{{Key: prefix(), Limit: 1, Cost: 1}}}); err == nil {
		t.Error("Admit() with no Redis returned no error")
	}
	if err := s.Settle(t.Context(), &Settlement{Now: at(1), Budgets: []Adjust{{Key: "k", Delta: 1}}}); err == nil {
		t.Error("Settle() with no Redis returned no error")
	}
	if err := s.Ping(t.Context()); err == nil {
		t.Error("Ping() with no Redis returned no error")
	}
}
