-- Settle adds a change to each counter. A counter does not go below 0.
-- Then it sets the sticky routing entry, if the call has one.
--
-- KEYS: the counters, then the sticky routing entry, if any.
-- ARGV: the number of counters, then the change and the TTL in
-- milliseconds of each counter, then the value and the TTL in milliseconds
-- of the sticky routing entry, if any.
local n = tonumber(ARGV[1])
for i = 1, n do
  local v = redis.call('INCRBY', KEYS[i], ARGV[2 * i])
  if v < 0 then
    redis.call('SET', KEYS[i], 0)
  end
  redis.call('PEXPIRE', KEYS[i], ARGV[2 * i + 1])
end
if #KEYS > n then
  redis.call('SET', KEYS[n + 1], ARGV[2 * n + 2], 'PX', ARGV[2 * n + 3])
end
return 0
