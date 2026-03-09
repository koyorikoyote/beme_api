package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/beme/beme/internal/usecase"
	goredis "github.com/redis/go-redis/v9"
)

// Ensure RedisRateLimiterRepo implements the usecase.RateLimiter interface.
var _ usecase.RateLimiter = (*RedisRateLimiterRepo)(nil)

// gcraScript implements the Generic Cell Rate Algorithm (GCRA) atomically via Lua.
//
// KEYS[1]  = rate limit key (e.g. "ratelimit:{client_id}")
// ARGV[1]  = now (nanoseconds, as a string integer)
// ARGV[2]  = emission_interval (nanoseconds per request = 1e9 / rate_per_second)
// ARGV[3]  = burst_offset (nanoseconds = emission_interval * burst_size)
// ARGV[4]  = ttl (seconds for key expiry = 2 * burst_period)
//
// Returns a table: { allowed (0|1), new_tat (ns), remaining (int) }
var gcraScript = goredis.NewScript(`
local key              = KEYS[1]
local now              = tonumber(ARGV[1])
local emission         = tonumber(ARGV[2])
local burst_offset     = tonumber(ARGV[3])
local ttl_seconds      = tonumber(ARGV[4])

-- Read current TAT; default to now if key is absent.
local tat_str = redis.call("GET", key)
local tat
if tat_str then
    tat = tonumber(tat_str)
else
    tat = now
end

-- New TAT = max(TAT, now) + emission_interval
local new_tat = math.max(tat, now) + emission

-- If new_TAT - now > burst_offset the cell is full → reject.
if (new_tat - now) > burst_offset then
    -- Remaining is 0; reset is when the oldest slot opens up.
    local reset_ns = tat - burst_offset
    return {0, tat, 0, reset_ns}
end

-- Allow: persist new TAT with TTL.
redis.call("SET", key, tostring(new_tat), "EX", ttl_seconds)

-- Remaining slots = floor((burst_offset - (new_tat - now)) / emission)
local remaining = math.floor((burst_offset - (new_tat - now)) / emission)
return {1, new_tat, remaining, new_tat}
`)

// RedisRateLimiterRepo implements usecase.RateLimiter using the GCRA algorithm
// backed by an atomic Lua script executed on Redis.
type RedisRateLimiterRepo struct {
	client        *goredis.Client
	ratePerSecond int
	// Derived constants (computed once in the constructor).
	emissionInterval int64 // nanoseconds between requests at steady-state rate
	burstOffset      int64 // nanoseconds representing the full burst window
	ttlSeconds       int64 // Redis key TTL = 2 × burst period in seconds
}

// NewRedisRateLimiterRepo creates a RedisRateLimiterRepo.
// ratePerSecond is the sustained request rate; burst size defaults to ratePerSecond
// (i.e. one full second of requests may arrive instantaneously).
func NewRedisRateLimiterRepo(client *goredis.Client, ratePerSecond int) *RedisRateLimiterRepo {
	if ratePerSecond <= 0 {
		ratePerSecond = 1
	}

	emissionInterval := int64(1_000_000_000) / int64(ratePerSecond)
	burstSize := int64(ratePerSecond) // burst = 1 second worth of requests
	burstOffset := emissionInterval * burstSize
	// TTL = 2 × burst period (burst period = 1 second here)
	ttlSeconds := int64(2)

	return &RedisRateLimiterRepo{
		client:           client,
		ratePerSecond:    ratePerSecond,
		emissionInterval: emissionInterval,
		burstOffset:      burstOffset,
		ttlSeconds:       ttlSeconds,
	}
}

// Allow checks whether the given clientID is within its rate limit using GCRA.
// It returns whether the request is allowed and the current rate-limit headers.
// On Redis failure the method fails open (allows the request) to avoid blocking all traffic.
func (r *RedisRateLimiterRepo) Allow(ctx context.Context, clientID string) (bool, usecase.RateLimitHeaders, error) {
	key := fmt.Sprintf("ratelimit:%s", clientID)
	now := time.Now().UnixNano()

	result, err := gcraScript.Run(ctx, r.client, []string{key},
		now,
		r.emissionInterval,
		r.burstOffset,
		r.ttlSeconds,
	).Int64Slice()

	if err != nil {
		// Fail open: allow the request but surface the error to the caller.
		headers := usecase.RateLimitHeaders{
			Limit:     r.ratePerSecond,
			Remaining: r.ratePerSecond,
			Reset:     time.Now().Add(time.Second).Unix(),
		}
		return true, headers, fmt.Errorf("redis: gcra script: %w", err)
	}

	allowed := result[0] == 1
	remaining := int(result[2])
	resetNs := result[3]
	resetUnix := time.Unix(0, resetNs).Unix()

	headers := usecase.RateLimitHeaders{
		Limit:     r.ratePerSecond,
		Remaining: remaining,
		Reset:     resetUnix,
	}

	return allowed, headers, nil
}
