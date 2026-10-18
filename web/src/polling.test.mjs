import { test } from 'node:test'
import assert from 'node:assert/strict'
import { startVisiblePolling } from './polling.ts'

test('polls sequentially, suspends in hidden tabs, resumes immediately and aborts on cleanup', async () => {
  const visibility = new EventTarget()
  visibility.hidden = false
  let timer
  const environment = { visibility, schedule: callback => { timer = callback; return 1 }, cancel: () => { timer = undefined } }
  let release
  let calls = 0
  let signal
  const stop = startVisiblePolling(async current => {
    signal = current
    calls++
    await new Promise(resolve => { release = resolve })
  }, 5000, environment)
  assert.equal(calls, 1)
  visibility.dispatchEvent(new Event('visibilitychange'))
  assert.equal(calls, 1, 'visibility events cannot overlap a pending request')
  release()
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(typeof timer, 'function')
  visibility.hidden = true
  visibility.dispatchEvent(new Event('visibilitychange'))
  assert.equal(timer, undefined)
  visibility.hidden = false
  visibility.dispatchEvent(new Event('visibilitychange'))
  assert.equal(calls, 2)
  stop()
  assert.equal(signal.aborted, true)
  release()
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(timer, undefined)
  visibility.dispatchEvent(new Event('visibilitychange'))
  assert.equal(calls, 2)
})

test('initially hidden pages wait for visibility before fetching', async () => {
  const visibility = new EventTarget()
  visibility.hidden = true
  let calls = 0
  const stop = startVisiblePolling(async () => { calls++ }, 0, { visibility, schedule: () => 1, cancel: () => {} })
  assert.equal(calls, 0)
  visibility.hidden = false
  visibility.dispatchEvent(new Event('visibilitychange'))
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(calls, 1)
  visibility.dispatchEvent(new Event('visibilitychange'))
  await new Promise(resolve => setImmediate(resolve))
  assert.equal(calls, 1, 'non-polling forms do not reload and overwrite drafts on focus')
  stop()
})
