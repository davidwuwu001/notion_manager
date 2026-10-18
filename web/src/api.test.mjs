import { test } from 'node:test'
import assert from 'node:assert/strict'
import { fetchManagedModels, fetchConnection, fetchRequestLogs, fetchRequestLog, fetchAnalytics, updateRouting } from './api.ts'
import { emptyLogFilters } from './dashboard.ts'

test('new management APIs use dashboard cookies and preserve filters, cursor and abort signals', async () => {
  const original = globalThis.fetch
  const calls = []
  globalThis.fetch = async (url, init) => {
    calls.push({ url, init })
    return new Response(JSON.stringify({ public_models: [{ id: 'public-model' }], api_key: 'fixture-key' }), { headers: { 'Content-Type': 'application/json' } })
  }
  try {
    const controller = new AbortController()
    assert.equal((await fetchManagedModels(controller.signal)).public_models[0].id, 'public-model')
    await fetchConnection(controller.signal)
    await fetchRequestLogs({ ...emptyLogFilters, model: 'a&b', account_id: 'workspace-a' }, 'cursor/+', 50, controller.signal)
    await fetchRequestLog('request/id', controller.signal)
    await fetchAnalytics(emptyLogFilters, controller.signal)
    for (const call of calls) {
      assert.equal(call.init.credentials, 'same-origin')
      assert.equal(call.init.signal, controller.signal)
      assert.equal(call.init.headers['x-api-key'], undefined)
      assert.equal(call.init.headers.Authorization, undefined)
    }
    assert.equal(calls[0].url, '/admin/models')
    assert.equal(calls[1].url, '/admin/connection')
    const params = new URL('http://localhost' + calls[2].url).searchParams
    assert.equal(params.get('model'), 'a&b')
    assert.equal(params.get('cursor'), 'cursor/+')
    assert.equal(params.get('account_id'), 'workspace-a')
    assert.equal(calls[3].url, '/admin/request-logs/request%2Fid')
    assert.equal(calls[4].url, '/admin/analytics?')
  } finally { globalThis.fetch = original }
})

test('routing writes send only configuration and unauthorized responses expire the dashboard', async () => {
  const originalFetch = globalThis.fetch
  const originalWindow = globalThis.window
  const events = []
  globalThis.window = { dispatchEvent: event => events.push(event.type) }
  const config = { mode: 'round_robin', max_attempts: 0, max_concurrent_per_account: 2, disabled_account_ids: [], retention_days: 30 }
  globalThis.fetch = async (url, init) => {
    assert.equal(url, '/admin/routing')
    assert.equal(init.method, 'PUT')
    assert.deepEqual(JSON.parse(init.body), config)
    return new Response(JSON.stringify({ error: 'dashboard login required' }), { status: 401 })
  }
  try {
    await assert.rejects(updateRouting(config), /dashboard login required/)
    assert.deepEqual(events, ['notion-manager-unauthorized'])
  } finally { globalThis.fetch = originalFetch; globalThis.window = originalWindow }
})

test('invalid JSON is an error rather than an empty successful dataset', async () => {
  const original = globalThis.fetch
  globalThis.fetch = async () => new Response('<html>error</html>', { status: 502 })
  try { await assert.rejects(fetchAnalytics(emptyLogFilters), /HTTP 502/) }
  finally { globalThis.fetch = original }
})
