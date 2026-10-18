import { useTranslation } from 'react-i18next'
import { fetchAnalytics, fetchDashboardData, fetchRequestLogs, fetchRouting, fetchTokenStats } from '../api'
import { confirmedPremiumBalance, emptyLogFilters, metricValue, routingMetrics, timestampValue } from '../dashboard'
import { buttonClass, Coverage, Metric, Panel, RequestMetrics, RequestTimeline, ResourceState, useAdminResource } from './DashboardShared'

export function OverviewPanel() {
  const { t } = useTranslation()
  const pool = useAdminResource(signal => fetchDashboardData({ page: 0, pageSize: 1 }, signal), [])
  const routing = useAdminResource(fetchRouting, [])
  const analytics = useAdminResource(signal => fetchAnalytics(emptyLogFilters, signal), [], 15000)
  const logs = useAdminResource(signal => fetchRequestLogs(emptyLogFilters, '', 8, signal), [], 15000)
  const history = useAdminResource(fetchTokenStats, [])
  const availability = routingMetrics(routing.data)
  return <div className="space-y-5">
    <h1 className="text-xl font-semibold">{t('upgrade.overview')}</h1>
    <ResourceState {...pool} retry={pool.reload} />
    {pool.data && <><div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4"><Metric title={t('stats.total_accounts')} value={pool.data.total} /><Metric title={`${t('upgrade.pool')} · ${t('stats.available')}`} value={metricValue(availability.available)} /><Metric title={t('stats.basic_remaining')} value={metricValue(pool.data.summary?.total_remaining)} /><Metric title={t('stats.premium_remaining')} value={<>{metricValue(confirmedPremiumBalance(pool.data.summary?.total_premium_balance, pool.data.summary?.premium_unknown_accounts != null))}<div className="text-[12px] font-normal text-text-secondary mt-2">{t('quota.unknown_accounts', { count: metricValue(pool.data.summary?.premium_unknown_accounts) })}</div></>} /></div><p className="text-[12px] text-text-secondary">{t('quota.compatibility_note')}</p></>}
    <ResourceState {...routing} retry={routing.reload} />
    {routing.data && <Panel title={t('upgrade.pool')}><p className="text-sm">{t('upgrade.pool_summary', routing.data as unknown as Record<string, unknown>)}</p><p className="text-[12px] text-text-secondary mt-2">{t(`upgrade.mode_${routing.data.mode}`)}</p></Panel>}
    <Panel title={t('upgrade.request_metrics')}>
      <ResourceState {...analytics} retry={analytics.reload} />
      {analytics.data && <><Coverage data={analytics.data} /><p className="text-[12px] text-text-secondary mb-3">{timestampValue(analytics.data.from)} — {timestampValue(analytics.data.to)}</p><RequestMetrics data={analytics.data} /><RequestTimeline data={analytics.data} /></>}
    </Panel>
    <Panel title={t('upgrade.recent_requests')}>
      <ResourceState {...logs} retry={logs.reload} />
      {logs.data && <><Coverage data={logs.data} /><div className="overflow-x-auto"><table className="dashboard-table"><thead><tr>{['time', 'model', 'status', 'duration'].map(key => <th key={key}>{t(`upgrade.${key}`)}</th>)}</tr></thead><tbody>{logs.data.items.map(log => <tr key={log.request_id}><td>{timestampValue(log.started_at_ms)}</td><td>{log.requested_model || '—'}</td><td>{t(`upgrade.status_${log.status}`)}</td><td>{metricValue(log.duration_ms, ' ms')}</td></tr>)}{logs.data.items.length === 0 && <tr><td colSpan={4}>{t('upgrade.empty')}</td></tr>}</tbody></table></div></>}
      <a href="#logs" className={`${buttonClass} inline-block mt-3`}>{t('upgrade.view_logs')}</a>
    </Panel>
    <Panel title={t('upgrade.historical_tokens')}><ResourceState {...history} retry={history.reload} />{history.data && <><p className="text-[12px] text-text-secondary mb-3">{t('upgrade.history_note')}</p><div className="grid sm:grid-cols-2 gap-3"><Metric title={t('upgrade.total_tokens')} value={metricValue(history.data.total.total)} /><Metric title={t('upgrade.today_tokens')} value={metricValue(history.data.today.total)} /></div></>}</Panel>
  </div>
}
