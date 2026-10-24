-- Settle adds a change to each counter. A counter does not go below 0.
--
-- KEYS: the counters.
-- ARGV: the change and the TTL in milliseconds of each counter.
for i = 1, #KEYS do
  local v = redis.call('INCRBY', KEYS[i], ARGV[2 * i - 1])
  if v < 0 then
    redis.call('SET', KEYS[i], 0)
  end
  redis.call('PEXPIRE', KEYS[i], ARGV[2 * i])
end
return 0
