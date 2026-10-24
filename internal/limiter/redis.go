package limiter

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Timeouts of the Redis client, if the URL has no query. They are short,
// because a request waits for the store before it goes to the provider.
// When Redis does not answer in time, the Failover store takes over.
const (
	redisDialTimeout = time.Second
	redisIOTimeout   = 500 * time.Millisecond
	redisMaxRetries  = 1
)

var (
	//go:embed admit.lua
	admitLua string
	//go:embed settle.lua
	settleLua string
)

// Redis is a Store in Redis, for more than one Tollgate copy. Each Admit
// and each Settle is one Lua script, so each is one round trip and is
// atomic. Use NewRedis to make one.
//
// All keys of one request must be in one Redis Cluster slot. Callers put
// the tenant in a hash tag, for example "tg:rl:req:{personal}".
type Redis struct {
	client *redis.Client
	admit  *redis.Script
	settle *redis.Script
}

// NewRedis returns a store for the Redis server at rawURL, in the format
// redis://[user:password@]host:port/db or rediss:// for TLS.
func NewRedis(rawURL string) (*Redis, error) {
	opts, err := redis.ParseURL(rawURL)
	if err != nil {
		// Do not wrap err: it can contain the password.
		return nil, fmt.Errorf("parse redis url: the url is not valid")
	}
	if u, err := url.Parse(rawURL); err == nil && u.RawQuery == "" {
		opts.DialTimeout = redisDialTimeout
		opts.ReadTimeout = redisIOTimeout
		opts.WriteTimeout = redisIOTimeout
		opts.MaxRetries = redisMaxRetries
	}
	return &Redis{
		client: redis.NewClient(opts),
		admit:  redis.NewScript(admitLua),
		settle: redis.NewScript(settleLua),
	}, nil
}

// Close closes the connections to Redis.
func (s *Redis) Close() error {
	if err := s.client.Close(); err != nil {
		return fmt.Errorf("close redis client: %w", err)
	}
	return nil
}

// Admit implements Store.
func (s *Redis) Admit(ctx context.Context, r *Request) (Decision, error) {
	n, elapsed := minute(r.Now)
	keys := make([]string, 0, 2*len(r.Rates)+len(r.Budgets))
	args := make([]any, 0, 4+2*len(r.Rates)+4*len(r.Budgets))
	args = append(args, len(r.Rates), len(r.Budgets), elapsed, rateTTL.Milliseconds())
	for _, rt := range r.Rates {
		keys = append(keys, rateKey(rt.Key, n), rateKey(rt.Key, n-1))
		args = append(args, rt.Limit, rt.Cost)
	}
	for _, b := range r.Budgets {
		hard := 0
		if b.Hard {
			hard = 1
		}
		keys = append(keys, b.Key)
		args = append(args, b.Limit, b.Amount, hard, ttlMs(b.TTL))
	}
	vals, err := s.admit.Run(ctx, s.client, keys, args...).Int64Slice()
	if err != nil {
		return Decision{}, fmt.Errorf("redis admit: %w", err)
	}
	if len(vals) != 1+2*len(r.Rates)+len(r.Budgets) {
		return Decision{}, fmt.Errorf("redis admit: the script returned %d values", len(vals))
	}
	c := counts{
		cur:   make([]int64, len(r.Rates)),
		prev:  make([]int64, len(r.Rates)),
		spend: make([]int64, len(r.Budgets)),
	}
	for i := range r.Rates {
		c.cur[i], c.prev[i] = vals[1+2*i], vals[2+2*i]
	}
	for j := range r.Budgets {
		c.spend[j] = vals[1+2*len(r.Rates)+j]
	}
	return decide(r, c, int(vals[0]), n, elapsed), nil
}

// Settle implements Store.
func (s *Redis) Settle(ctx context.Context, st *Settlement) error {
	var keys []string
	var args []any
	if n, _ := minute(st.Now); st.Minute >= n-1 {
		for _, a := range st.Rates {
			if a.Delta != 0 {
				keys = append(keys, rateKey(a.Key, st.Minute))
				args = append(args, a.Delta, rateTTL.Milliseconds())
			}
		}
	}
	for _, a := range st.Budgets {
		if a.Delta != 0 {
			keys = append(keys, a.Key)
			args = append(args, a.Delta, ttlMs(a.TTL))
		}
	}
	if len(keys) == 0 {
		return nil
	}
	if err := s.settle.Run(ctx, s.client, keys, args...).Err(); err != nil {
		return fmt.Errorf("redis settle: %w", err)
	}
	return nil
}

// Ping implements Store.
func (s *Redis) Ping(ctx context.Context) error {
	if err := s.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	return nil
}

// ttlMs returns d in milliseconds, at least 1, for PEXPIRE.
func ttlMs(d time.Duration) string {
	return strconv.FormatInt(max(d.Milliseconds(), 1), 10)
}
