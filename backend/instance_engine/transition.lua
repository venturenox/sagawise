-- transition.lua: one atomic state transition on a workflow instance
-- (contract §2, §3, §4, §5). Redis runs a script without interleaving any
-- other command, so the state check, the state write, the deadline change
-- and the queue enqueues below happen as one step (T4, T5, I3).
--
-- KEYS[1] workflow_instance:<id>
-- KEYS[2] task_deadlines      zset  member <id>:<index>  score deadline ms
-- KEYS[3] archive_pending     zset  member <id>          score due ms
-- KEYS[4] webhook_pending     zset  member <id>:<index>  score due ms
--
-- ARGV[1] action    publish | consume | fail | reap | reap_batch
-- ARGV[2] is_retry  0 | 1
-- ARGV[3] indexes   comma-separated task indexes, e.g. "0" or "0,2"
-- ARGV[4] now_s     engine clock, unix seconds (document stamps)
-- ARGV[5] now_ms    engine clock, unix milliseconds (deadline and queue scores)
-- ARGV[6] payload   JSON body (publish only, else "")
-- ARGV[7] id        the instance id (deadline and queue members)
--
-- Returns {code, task_states_json, instance_state, terminal_now, webhook_queued, refused_index}
--   code            OK | IDEMPOTENT | INSTANCE_TERMINAL | TASK_NOT_PUBLISHED |
--                   TASK_ALREADY_PUBLISHED | TASK_ALREADY_COMPLETED | TASK_ALREADY_FAILED |
--                   STALE (reap only) | NOT_FOUND | TASK_NOT_FOUND | BAD_SCHEMA
--   task_states     JSON array: the state of every target index after the call
--   instance_state  the instance state after the call
--   terminal_now    1 if this call moved the instance to a terminal state
--   webhook_queued  1 if a task became FAILED and a webhook was enqueued
--   refused_index   the task index a refusal is about, or -1
--
-- reap_batch is the exception to all of the above. It takes no instance key
-- (KEYS[1] is ignored), reads the overdue members of task_deadlines itself
-- and reaps every one of them in this single call, so a tick costs one
-- round-trip instead of one per expired task. ARGV[3] is the batch limit
-- and ARGV[8] the key prefix used to rebuild an instance key from a member.
-- It returns {reaped, webhooks_queued, archives_queued, dropped} counts;
-- the reaper logs those rather than acting per task. (phase 7)

local key, deadlines, archive, webhooks = KEYS[1], KEYS[2], KEYS[3], KEYS[4]
local action, retry = ARGV[1], ARGV[2] == '1'
local now_s, now_ms, payload = ARGV[4], ARGV[5], ARGV[6]

-- run performs one transition against instance `key`, targeting the task
-- indexes in `idx_csv`. Every action funnels through here -- including each
-- member of a reap_batch -- so the state machine lives in exactly one place.
local function run(key, id, idx_csv)
  local raw = redis.call('JSON.GET', key, '$.state', '$.tasks[*].state', '$.tasks[*].timeout')
  if not raw then
    return {'NOT_FOUND', '[]', '', 0, 0, -1}
  end
  local doc = cjson.decode(raw)
  local inst = doc['$.state'] and doc['$.state'][1]
  local states = doc['$.tasks[*].state'] or {}
  local timeouts = doc['$.tasks[*].timeout'] or {}
  if inst == nil or #states == 0 then
    return {'BAD_SCHEMA', '[]', '', 0, 0, -1}
  end

  local indexes = {}
  for s in string.gmatch(idx_csv, '[^,]+') do
    local i = tonumber(s)
    if i == nil or i < 0 or i >= #states then
      return {'TASK_NOT_FOUND', '[]', inst, 0, 0, i or -1}
    end
    indexes[#indexes + 1] = i
  end

  local terminal = inst == 'COMPLETED' or inst == 'FAILED'

  -- decide returns the refusal code for one task, or 'OK' (a transition) or
  -- 'IDEMPOTENT' (a duplicate accepted under is_retry=true). The table is
  -- contract §2 with the duplicate column of §4 folded in.
  local function decide(state)
    if action == 'reap' then
      if state == 'PUBLISHED' then return 'OK' end
      return 'STALE'
    elseif action == 'publish' then
      if state == 'PENDING' then return 'OK' end
      if retry then return 'IDEMPOTENT' end
      return 'TASK_ALREADY_' .. state
    elseif action == 'consume' then
      if state == 'PENDING' then return 'TASK_NOT_PUBLISHED' end
      if state == 'PUBLISHED' then return 'OK' end
      if state == 'COMPLETED' and retry then return 'IDEMPOTENT' end
      return 'TASK_ALREADY_' .. state
    else -- fail
      if state == 'PENDING' then return 'TASK_NOT_PUBLISHED' end
      if state == 'PUBLISHED' then return 'OK' end
      if state == 'FAILED' and retry then return 'IDEMPOTENT' end
      return 'TASK_ALREADY_' .. state
    end
  end

  local function member(i) return id .. ':' .. tostring(i) end

  local function current_states()
    local out = {}
    for n, i in ipairs(indexes) do out[n] = states[i + 1] end
    return cjson.encode(out)
  end

  -- A stale deadline on a terminal instance is discarded, nothing else (I4, I5).
  if action == 'reap' and terminal then
    redis.call('ZREM', deadlines, member(indexes[1]))
    return {'STALE', current_states(), inst, 0, 0, -1}
  end

  -- Evaluate every target first: a publish over several tasks is all-or-nothing.
  local decisions, any_ok = {}, false
  for n, i in ipairs(indexes) do
    local d = decide(states[i + 1])
    decisions[n] = d
    if d == 'OK' then
      any_ok = true
    elseif d ~= 'IDEMPOTENT' then
      if action == 'reap' then
        -- The task moved on since the deadline was written; the member is
        -- stale and is dropped here so the next tick does not see it again.
        redis.call('ZREM', deadlines, member(i))
      end
      return {d, current_states(), inst, 0, 0, i}
    end
  end

  -- A terminal instance answers only idempotent duplicates, and with no side
  -- effects at all: no re-armed deadline, no replaced payload (I4).
  if terminal then
    if any_ok then
      return {'INSTANCE_TERMINAL', current_states(), inst, 0, 0, indexes[1]}
    end
    return {'IDEMPOTENT', current_states(), inst, 0, 0, -1}
  end

  -- One write per task, not one per field. RedisJSON re-indexes the whole
  -- document on every write, so the number of write commands (not the number
  -- of fields in them) is what Redis CPU pays for: the profile measured
  -- JSON.SET at 5x the cost of JSON.GET. A publish went from three writes to
  -- one and a terminal consume from four to two. (phase 7)
  --
  -- JSON.MERGE carries the state changes, which are all scalar fields whose
  -- RFC 7386 merge is a plain overwrite. The payload is the one field that
  -- must be *replaced* rather than deep-merged (a republish with fewer keys
  -- must not leave the old ones behind), so it rides on its own JSON.SET.
  local function merge(path, json) redis.call('JSON.MERGE', key, path, json) end

  local webhook_queued = false
  for n, i in ipairs(indexes) do
    local task = '$.tasks[' .. tostring(i) .. ']'
    local d = decisions[n]
    if action == 'publish' and (d == 'OK' or states[i + 1] == 'PUBLISHED') then
      -- A fresh publish, or a retried publish on a PUBLISHED task: the payload
      -- is (re)placed and the deadline (re)armed from now (D2).
      redis.call('JSON.SET', key, task .. '.payload', payload)
      merge(task, '{"state":"PUBLISHED","publishedAt":' .. now_s .. '}')
      redis.call('ZADD', deadlines, string.format('%.0f', tonumber(now_ms) + tonumber(timeouts[i + 1])), member(i))
      states[i + 1] = 'PUBLISHED'
    elseif d == 'OK' and action == 'consume' then
      merge(task, '{"state":"COMPLETED","consumedAt":' .. now_s .. '}')
      redis.call('ZREM', deadlines, member(i))
      states[i + 1] = 'COMPLETED'
    elseif d == 'OK' then -- fail or reap
      merge(task, '{"state":"FAILED","failedAt":' .. now_s .. '}')
      redis.call('ZREM', deadlines, member(i))
      redis.call('ZADD', webhooks, now_ms, member(i))
      states[i + 1] = 'FAILED'
      webhook_queued = true
    end
  end

  -- Instance transition in the same step (I1, I2, I3, I5).
  local terminal_now = false
  if any_ok then
    local failed, completed = false, 0
    for _, s in ipairs(states) do
      if s == 'FAILED' then failed = true end
      if s == 'COMPLETED' then completed = completed + 1 end
    end
    if failed then
      inst = 'FAILED'
      merge('$', '{"state":"FAILED","completedAt":' .. now_s .. ',"failedAt":' .. now_s .. '}')
      -- Siblings freeze: a PUBLISHED sibling keeps its state and loses its
      -- deadline, so the reaper never fires a second webhook (D1).
      for i = 0, #states - 1 do
        redis.call('ZREM', deadlines, member(i))
      end
      terminal_now = true
    elseif completed == #states then
      inst = 'COMPLETED'
      merge('$', '{"state":"COMPLETED","completedAt":' .. now_s .. '}')
      terminal_now = true
    end
    if terminal_now then
      redis.call('ZADD', archive, now_ms, id)
    end
  end

  local code = any_ok and 'OK' or 'IDEMPOTENT'
  return {code, current_states(), inst, terminal_now and 1 or 0, webhook_queued and 1 or 0, -1}
end

-- ---- entry points ----

if action ~= 'reap_batch' then
  return run(KEYS[1], ARGV[7], ARGV[3])
end

-- reap_batch: read the overdue deadlines and reap each, all in this call.
local limit, prefix = tonumber(ARGV[3]), ARGV[8]
local due = redis.call('ZRANGEBYSCORE', deadlines, '-inf', now_ms, 'LIMIT', 0, limit)
local reaped, hooks, archives, dropped = 0, 0, 0, 0

for _, m in ipairs(due) do
  local sep = string.find(m, ':[^:]*$')
  local id_part = sep and string.sub(m, 1, sep - 1)
  local idx_part = sep and string.sub(m, sep + 1)
  if not sep or tonumber(idx_part) == nil then
    -- Malformed member: it can never resolve, so drop it rather than
    -- retrying it every tick.
    redis.call('ZREM', deadlines, m)
    dropped = dropped + 1
  else
    local r = run(prefix .. id_part, id_part, idx_part)
    local code = r[1]
    if code == 'OK' then
      reaped = reaped + 1
      if r[5] == 1 then hooks = hooks + 1 end
      if r[4] == 1 then archives = archives + 1 end
    elseif code == 'NOT_FOUND' or code == 'BAD_SCHEMA' or code == 'TASK_NOT_FOUND' then
      -- The document is gone or unreadable; the deadline can never resolve.
      redis.call('ZREM', deadlines, m)
      dropped = dropped + 1
    end
    -- STALE: run() already removed the member.
  end
end

return {reaped, hooks, archives, dropped}
