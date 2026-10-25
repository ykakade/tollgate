-- Admit checks the rate limits and hard budgets of one request and, if the
-- request is allowed, adds its costs. Redis runs the script atomically.
-- The formulas are the same as in limiter.go.
--
-- KEYS: for each rate, the counter of this minute and the counter of the
-- previous minute; then the counter of each budget; then the sticky
-- routing entry, if any.
-- ARGV: the number of rates, the number of budgets, the milliseconds since
-- the start of this minute, the TTL of a rate counter in milliseconds; then
-- the limit and the cost of each rate; then the limit, the amount, hard (1
-- or 0) and the TTL in milliseconds of each budget.
--
-- The result is the 1-based index of the limit that stopped the request,
-- or 0, then the value of each counter before the change: this minute and
-- the previous minute of each rate, then each budget; then the value of the
-- sticky routing entry, or 0, if the call has one. The script does not
-- change the sticky routing entry.
local nr = tonumber(ARGV[1])
local nb = tonumber(ARGV[2])
local elapsed = tonumber(ARGV[3])
local rate_ttl = ARGV[4]
local w = 60000
local denied = 0
local out = {0}
for i = 1, nr do
  local cur = tonumber(redis.call('GET', KEYS[2 * i - 1]) or '0')
  local prev = tonumber(redis.call('GET', KEYS[2 * i]) or '0')
  local limit = tonumber(ARGV[3 + 2 * i])
  local cost = tonumber(ARGV[4 + 2 * i])
  if denied == 0 and prev * (w - elapsed) + (cur + cost) * w > limit * w then
    denied = i
  end
  out[#out + 1] = cur
  out[#out + 1] = prev
end
local base = 4 + 2 * nr
for j = 1, nb do
  local spend = tonumber(redis.call('GET', KEYS[2 * nr + j]) or '0')
  local limit = tonumber(ARGV[base + 4 * j - 3])
  local amount = tonumber(ARGV[base + 4 * j - 2])
  local hard = ARGV[base + 4 * j - 1] == '1'
  if denied == 0 and hard and spend + amount > limit then
    denied = nr + j
  end
  out[#out + 1] = spend
end
if denied == 0 then
  for i = 1, nr do
    redis.call('INCRBY', KEYS[2 * i - 1], ARGV[4 + 2 * i])
    redis.call('PEXPIRE', KEYS[2 * i - 1], rate_ttl)
  end
  for j = 1, nb do
    redis.call('INCRBY', KEYS[2 * nr + j], ARGV[base + 4 * j - 2])
    redis.call('PEXPIRE', KEYS[2 * nr + j], ARGV[base + 4 * j])
  end
end
if #KEYS > 2 * nr + nb then
  out[#out + 1] = tonumber(redis.call('GET', KEYS[#KEYS]) or '0') or 0
end
out[1] = denied
return out
