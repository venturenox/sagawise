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
-- ARGV[1] action    publish | consume | fail | reap
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

local key, deadlines, archive, webhooks = KEYS[1], KEYS[2], KEYS[3], KEYS[4]
local action, retry = ARGV[1], ARGV[2] == '1'
local now_s, now_ms, payload, id = ARGV[4], ARGV[5], ARGV[6], ARGV[7]

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
for s in string.gmatch(ARGV[3], '[^,]+') do
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

local function set(path, json) redis.call('JSON.SET', key, path, json) end

local webhook_queued = false
for n, i in ipairs(indexes) do
  local task = '$.tasks[' .. tostring(i) .. ']'
  local d = decisions[n]
  if action == 'publish' and (d == 'OK' or states[i + 1] == 'PUBLISHED') then
    -- A fresh publish, or a retried publish on a PUBLISHED task: the payload
    -- is (re)placed and the deadline (re)armed from now (D2).
    set(task .. '.state', '"PUBLISHED"')
    set(task .. '.publishedAt', now_s)
    set(task .. '.payload', payload)
    redis.call('ZADD', deadlines, string.format('%.0f', tonumber(now_ms) + tonumber(timeouts[i + 1])), member(i))
    states[i + 1] = 'PUBLISHED'
  elseif d == 'OK' and action == 'consume' then
    set(task .. '.state', '"COMPLETED"')
    set(task .. '.consumedAt', now_s)
    redis.call('ZREM', deadlines, member(i))
    states[i + 1] = 'COMPLETED'
  elseif d == 'OK' then -- fail or reap
    set(task .. '.state', '"FAILED"')
    set(task .. '.failedAt', now_s)
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
    set('$.state', '"FAILED"')
    set('$.completedAt', now_s)
    set('$.failedAt', now_s)
    -- Siblings freeze: a PUBLISHED sibling keeps its state and loses its
    -- deadline, so the reaper never fires a second webhook (D1).
    for i = 0, #states - 1 do
      redis.call('ZREM', deadlines, member(i))
    end
    terminal_now = true
  elseif completed == #states then
    inst = 'COMPLETED'
    set('$.state', '"COMPLETED"')
    set('$.completedAt', now_s)
    terminal_now = true
  end
  if terminal_now then
    redis.call('ZADD', archive, now_ms, id)
  end
end

local code = any_ok and 'OK' or 'IDEMPOTENT'
return {code, current_states(), inst, terminal_now and 1 or 0, webhook_queued and 1 or 0, -1}
