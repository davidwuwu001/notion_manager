import { test } from 'node:test'
import assert from 'node:assert/strict'
import * as dashboard from './dashboard.ts'
import { dashboardTab, filterParams, emptyLogFilters, metricValue, quotaRefreshProgress, confirmedPremiumBalance, routingMetrics } from './dashboard.ts'

test('quota failure translation uses only known reasons and hides legacy or raw errors', () => {
  for (const code of ['authentication_failed', 'permission_denied', 'rate_limited', 'upstream_error', 'timeout', 'network_error', 'invalid_response']) {
    assert.equal(dashboard.quotaErrorKey?.(code), `quota.error_${code}`)
  }
  for (const error of ['basic quota unavailable', 'AI usage unavailable', 'secret-cookie', '__proto__', '', undefined]) {
    assert.equal(dashboard.quotaErrorKey?.(error), 'quota.error_unavailable')
  }
})

test('hash navigation defaults to the compatible accounts page', () => {
  assert.equal(dashboardTab(''), 'accounts')
  assert.equal(dashboardTab('#unknown'), 'accounts')
  for (const tab of ['overview', 'accounts', 'models', 'logs', 'analytics', 'settings']) assert.equal(dashboardTab('#' + tab), tab)
})

test('log filters encode metadata, UTC times, and reject reversed windows', () => {
  const params = filterParams({ ...emptyLogFilters, from: '2026-10-18T01:00:00Z', to: '2026-10-18T02:00:00Z', account_id: 'a/b', q: ' model & error ' })
  assert.equal(params.get('from'), String(Date.parse('2026-10-18T01:00:00Z')))
  assert.equal(params.get('account_id'), 'a/b')
  assert.equal(params.get('q'), 'model & error')
  assert.equal(params.has('status'), false)
  assert.throws(() => filterParams({ ...emptyLogFilters, from: 'invalid' }))
  assert.throws(() => filterParams({ ...emptyLogFilters, from: '2026-10-19', to: '2026-10-18' }))
})

test('unknown metrics never look like measured zeroes', () => {
  assert.equal(metricValue(null), '—')
  assert.equal(metricValue(undefined), '—')
  assert.equal(metricValue(NaN), '—')
  assert.equal(metricValue(0), '0')
  assert.equal(metricValue(95, '%'), '95%')
})

test('quota refresh completion invalidates routing once, including fast manual refreshes', () => {
  for (const { pending: initial, snapshots } of [
    { pending: false, snapshots: [false, true, true, undefined, false, false] },
    { pending: true, snapshots: [false, false] },
  ]) {
    let pending = initial
    let completions = 0
    for (const snapshot of snapshots) {
      const next = quotaRefreshProgress(pending, snapshot)
      pending = next.pending
      if (next.completed) completions++
    }
    assert.equal(completions, 1)
    assert.equal(pending, false)
  }
  assert.equal(quotaRefreshProgress(false, false).completed, false)
  assert.equal(quotaRefreshProgress(false, undefined).completed, false)
})

test('Premium balances retain reported zero and never promote missing or legacy values', () => {
  assert.equal(confirmedPremiumBalance(0, true), 0)
  assert.equal(confirmedPremiumBalance(300, true), 300)
  for (const [balance, known] of [[null, true], [undefined, true], [0, false], [300, false], [300, undefined], [NaN, true]]) {
    assert.equal(confirmedPremiumBalance(balance, known), null)
    assert.equal(metricValue(confirmedPremiumBalance(balance, known)), '—')
  }
  const fixtures = [{ balance: 0, known: true }, { balance: 300, known: true }, { balance: 900, known: false }, { balance: 500, known: undefined }]
  const knownBalances = fixtures.map(value => confirmedPremiumBalance(value.balance, value.known))
  assert.equal(knownBalances.reduce((sum, value) => sum + (value ?? 0), 0), 300)
  assert.equal(knownBalances.filter(value => value === null).length, 2)
})

test('availability uses routing members and never counts paused members as exhausted', () => {
  const fixture = { total: 2, available: 1, paused: 1, exhausted: 0, legacy_quota_available: 2 }
  assert.deepEqual(routingMetrics(fixture, 0), { available: 1, availableRate: 50, exhausted: 0 })
  assert.deepEqual(routingMetrics({ total: 5, available: 2, exhausted: 1 }), { available: 2, availableRate: 40, exhausted: 1 })
  assert.deepEqual(routingMetrics(null, 1), { available: null, availableRate: null, exhausted: 1 })
  assert.deepEqual(routingMetrics(undefined), { available: null, availableRate: null, exhausted: null })
  assert.deepEqual(routingMetrics({ total: 0, available: 0, exhausted: 0 }), { available: 0, availableRate: null, exhausted: 0 })
  assert.equal(metricValue(routingMetrics(null).available), '—')
})
