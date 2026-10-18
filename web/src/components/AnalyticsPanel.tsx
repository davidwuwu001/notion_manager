import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { fetchAnalytics } from '../api'
import { emptyLogFilters, metricValue, timestampValue } from '../dashboard'
import { buttonClass, Coverage, Metric, Panel, RequestFilters, RequestMetrics, RequestTimeline, ResourceState, useAdminResource } from './DashboardShared'

export function AnalyticsPanel() {
  const { t } = useTranslation()
  const [filters, setFilters] = useState({ ...emptyLogFilters })
  const analytics = useAdminResource(signal => fetchAnalytics(filters, signal), [filters])
  const data = analytics.data
  return <div>
    <div className="flex justify-between gap-3 items-center mb-5"><h1 className="text-xl font-semibold">{t('upgrade.analytics')}</h1><button className={buttonClass} onClick={analytics.reload}>{t('upgrade.refresh')}</button></div>
    <RequestFilters onApply={setFilters} />
    <ResourceState {...analytics} retry={analytics.reload} />
    {data && <>
      <Coverage data={data} /><p className="text-[12px] text-text-secondary mb-4">{timestampValue(data.from)} — {timestampValue(data.to)} · {t('upgrade.metrics_note')}</p>
      <RequestMetrics data={data} />
      <div className="grid sm:grid-cols-2 lg:grid-cols-4 gap-3 mb-5">
        <Metric title={t('upgrade.status_success')} value={metricValue(data.overview.total_requests ? data.overview.success : null)} />
        <Metric title={t('upgrade.status_failed')} value={metricValue(data.overview.total_requests ? data.overview.failed : null)} />
        <Metric title={t('upgrade.status_rejected')} value={metricValue(data.overview.total_requests ? data.overview.rejected : null)} />
        <Metric title={t('upgrade.status_interrupted')} value={metricValue(data.overview.total_requests ? data.overview.interrupted : null)} />
        <Metric title={t('upgrade.status_cancelled')} value={metricValue(data.overview.total_requests ? data.overview.cancelled : null)} />
        <Metric title={t('upgrade.status_in_progress')} value={metricValue(data.overview.total_requests ? data.overview.in_progress : null)} />
        <Metric title={t('upgrade.input_tokens')} value={metricValue(data.overview.input_tokens)} /><Metric title={t('upgrade.output_tokens')} value={metricValue(data.overview.output_tokens)} />
        <Metric title={t('upgrade.p50')} value={metricValue(data.overview.p50_duration_ms, ' ms')} /><Metric title={t('upgrade.usage_source')} value={t(`upgrade.usage_${data.overview.usage_source}`)} />
      </div>
      <RequestTimeline data={data} />
      <div className="space-y-5 mt-5">
        <Panel title={t('upgrade.model_distribution')}>
          {data.models.length === 0 ? <p className="text-text-secondary text-sm">{t('upgrade.empty')}</p> : <div className="overflow-x-auto"><table className="dashboard-table"><thead><tr>{['model', 'requests', 'success_rate', 'average', 'p50', 'p95', 'tokens'].map(key => <th key={key}>{t(`upgrade.${key}`)}</th>)}</tr></thead><tbody>{data.models.map(model => <tr key={model.model}><td className="min-w-40 break-all">{model.model}<div className="h-1 bg-notion-blue mt-2 rounded" style={{ width: `${data.overview.total_requests ? model.total_requests / data.overview.total_requests * 100 : 0}%` }} /></td><td>{model.total_requests}</td><td>{metricValue(model.success_rate, '%')}</td><td>{metricValue(model.avg_duration_ms, ' ms')}</td><td>{metricValue(model.p50_duration_ms, ' ms')}</td><td>{metricValue(model.p95_duration_ms, ' ms')}</td><td>{metricValue(model.input_tokens)} / {metricValue(model.output_tokens)}</td></tr>)}</tbody></table></div>}
        </Panel>
        <Panel title={t('upgrade.account_distribution')}>
          <p className="text-[12px] text-text-secondary mb-3">{t('upgrade.account_metrics_note')}</p>
          {data.accounts.length === 0 ? <p className="text-text-secondary text-sm">{t('upgrade.empty')}</p> : <div className="overflow-x-auto"><table className="dashboard-table"><thead><tr>{['filter_account_id', 'requests', 'attempts', 'status_success', 'status_failed', 'status_cancelled', 'tokens'].map(key => <th key={key}>{t(`upgrade.${key}`)}</th>)}</tr></thead><tbody>{data.accounts.map(account => <tr key={account.account_id}><td className="font-mono break-all">{account.account_id}</td><td>{account.requests}</td><td>{account.attempts}</td><td>{account.success}</td><td>{account.failed}</td><td>{account.cancelled}</td><td>{metricValue(account.input_tokens)} / {metricValue(account.output_tokens)}</td></tr>)}</tbody></table></div>}
        </Panel>
      </div>
    </>}
  </div>
}
