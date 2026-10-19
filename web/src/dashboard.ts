export const dashboardTabs = ['overview', 'accounts', 'models', 'logs', 'analytics', 'settings'] as const
export type DashboardTab = typeof dashboardTabs[number]

export function dashboardTab(hash: string): DashboardTab {
  const value = hash.replace(/^#/, '')
  return dashboardTabs.includes(value as DashboardTab) ? value as DashboardTab : 'accounts'
}

export function quotaRefreshProgress(pending: boolean, refreshing: boolean | undefined) {
  return { pending: refreshing ?? pending, completed: pending && refreshing === false }
}

export function quotaErrorKey(error: string | undefined): string {
  const known = ['authentication_failed', 'permission_denied', 'rate_limited', 'upstream_error', 'timeout', 'network_error', 'invalid_response']
  return error && known.includes(error) ? `quota.error_${error}` : 'quota.error_unavailable'
}

export function routingMetrics(routing: { total: number; available: number; exhausted: number } | null | undefined, exhaustedOnly?: number) {
  return {
    available: routing?.available ?? null,
    availableRate: routing && routing.total > 0 ? Math.round(routing.available / routing.total * 100) : null,
    exhausted: routing?.exhausted ?? exhaustedOnly ?? null,
  }
}

export interface LogFilters {
  from: string
  to: string
  status: string
  model: string
  protocol: string
  account_id: string
  q: string
}

export const emptyLogFilters: LogFilters = { from: '', to: '', status: '', model: '', protocol: '', account_id: '', q: '' }

export function filterParams(filters: LogFilters): URLSearchParams {
  const params = new URLSearchParams()
  for (const key of ['from', 'to'] as const) {
    if (!filters[key]) continue
    const time = new Date(filters[key]).getTime()
    if (!Number.isFinite(time)) throw new Error('invalid_time')
    params.set(key, String(time))
  }
  if (params.has('from') && params.has('to') && Number(params.get('from')) >= Number(params.get('to'))) throw new Error('invalid_time')
  for (const key of ['status', 'model', 'protocol', 'account_id', 'q'] as const) {
    if (filters[key].trim()) params.set(key, filters[key].trim())
  }
  return params
}

export function metricValue(value: number | null | undefined, suffix = ''): string {
  return value == null || !Number.isFinite(value) ? '—' : `${value.toLocaleString(undefined, { maximumFractionDigits: 1 })}${suffix}`
}

export function confirmedPremiumBalance(balance: number | null | undefined, known: boolean | undefined): number | null {
  return known === true && typeof balance === 'number' && Number.isFinite(balance) ? balance : null
}

export function timestampValue(value: number | null | undefined): string {
  return value ? new Date(value).toLocaleString() : '—'
}
