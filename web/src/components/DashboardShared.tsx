import { useEffect, useState } from 'react'
import type { DependencyList, ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import type { LogCoverage, RequestAnalytics } from '../types'
import { emptyLogFilters, filterParams, metricValue, timestampValue } from '../dashboard'
import type { LogFilters } from '../dashboard'
import { startVisiblePolling } from '../polling'

export const buttonClass = 'rounded-md border border-border bg-bg-card px-3 py-2 text-[12px] text-text-primary hover:bg-bg-card-hover cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed'
export const inputClass = 'min-w-0 rounded-md border border-border bg-bg-input px-3 py-2 text-[12px] text-text-primary'

export function useAdminResource<T>(load: (signal: AbortSignal) => Promise<T>, dependencies: DependencyList, pollMs = 0) {
  const [data, setData] = useState<T | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [version, setVersion] = useState(0)
  useEffect(() => {
    setData(null)
    setLoading(true)
    setError('')
    return startVisiblePolling(async signal => {
      try {
        const result = await load(signal)
        if (!signal.aborted) { setData(result); setError('') }
      } catch (reason) {
        if (!signal.aborted) setError(reason instanceof Error ? reason.message : String(reason))
      } finally {
        if (!signal.aborted) setLoading(false)
      }
    }, pollMs)
  }, [...dependencies, version])
  return { data, error, loading, reload: () => setVersion(value => value + 1) }
}

export function ResourceState({ loading, error, retry }: { loading: boolean; error: string; retry: () => void }) {
  const { t } = useTranslation()
  return <>
    {loading && <p role="status" className="text-text-secondary text-sm py-4">{t('upgrade.loading')}</p>}
    {error && <div role="alert" className="rounded-lg border border-err/40 p-3 text-err text-sm mb-4 break-words">{error} <button className={buttonClass} onClick={retry}>{t('upgrade.retry')}</button></div>}
  </>
}

export function Panel({ title, children }: { title: string; children: ReactNode }) {
  return <section className="rounded-lg border border-border bg-bg-card p-4 min-w-0"><h2 className="font-semibold text-sm mb-4">{title}</h2>{children}</section>
}

export function Metric({ title, value }: { title: string; value: ReactNode }) {
  return <div className="rounded-lg border border-border bg-bg-card p-4 min-w-0"><div className="text-[12px] text-text-secondary mb-2">{title}</div><div className="text-xl font-semibold tabular-nums break-words">{value}</div></div>
}

export function Coverage({ data }: { data: LogCoverage }) {
  const { t } = useTranslation()
  return <div className="space-y-1 text-[12px] text-text-secondary mb-4">
    <p>{t('upgrade.coverage', { since: timestampValue(data.captured_since), retained: timestampValue(data.retained_from), days: data.retention_days })}</p>
    {!data.data_complete && <p role="status" className="text-warn">{t('upgrade.coverage_incomplete')}</p>}
    {data.storage_status !== 'ok' && <p role="alert" className="text-err">{t('upgrade.storage_warning', { status: data.storage_status })}</p>}
  </div>
}

export function RequestFilters({ onApply }: { onApply: (filters: LogFilters) => void }) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<LogFilters>({ ...emptyLogFilters })
  const [error, setError] = useState('')
  const field = (key: keyof LogFilters, type = 'text') => <label className="flex flex-col gap-1 text-[11px] text-text-secondary min-w-0">
    {t(`upgrade.filter_${key}`)}<input type={type} className={inputClass} value={draft[key]} onChange={event => setDraft(value => ({ ...value, [key]: event.target.value }))} />
  </label>
  return <form className="mb-5 space-y-3" onSubmit={event => {
    event.preventDefault()
    try { filterParams(draft); setError(''); onApply({ ...draft }) }
    catch { setError(t('upgrade.invalid_time')) }
  }}>
    <p className="text-[12px] text-text-secondary">{t('upgrade.filter_note')}</p>
    <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
      {field('from', 'datetime-local')}{field('to', 'datetime-local')}
      <label className="flex flex-col gap-1 text-[11px] text-text-secondary">{t('upgrade.filter_status')}
        <select className={inputClass} value={draft.status} onChange={event => setDraft(value => ({ ...value, status: event.target.value }))}>
          <option value="">{t('upgrade.all')}</option>{['in_progress', 'success', 'rejected', 'failed', 'cancelled', 'interrupted'].map(status => <option key={status} value={status}>{t(`upgrade.status_${status}`)}</option>)}
        </select>
      </label>
      <label className="flex flex-col gap-1 text-[11px] text-text-secondary">{t('upgrade.filter_protocol')}
        <select className={inputClass} value={draft.protocol} onChange={event => setDraft(value => ({ ...value, protocol: event.target.value }))}>
          <option value="">{t('upgrade.all')}</option><option value="anthropic_messages">Anthropic Messages</option><option value="openai_chat">OpenAI Chat Completions</option><option value="openai_responses">OpenAI Responses</option>
        </select>
      </label>
      {field('model')}{field('account_id')}{field('q')}
      <div className="flex gap-2 items-end"><button className={buttonClass} type="submit">{t('upgrade.apply')}</button><button className={buttonClass} type="button" onClick={() => { const filters = { ...emptyLogFilters }; setDraft(filters); setError(''); onApply(filters) }}>{t('upgrade.clear')}</button></div>
    </div>
    {error && <p role="alert" className="text-err text-sm">{error}</p>}
  </form>
}

export function RequestMetrics({ data }: { data: RequestAnalytics }) {
  const { t } = useTranslation()
  const overview = data.overview
  const measured = overview.total_requests > 0
  return <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4 mb-5">
    <Metric title={t('upgrade.requests')} value={metricValue(measured ? overview.total_requests : null)} />
    <Metric title={t('upgrade.success_rate')} value={metricValue(overview.success_rate, '%')} />
    <Metric title={t('upgrade.average')} value={metricValue(overview.avg_duration_ms, ' ms')} />
    <Metric title={t('upgrade.p95')} value={metricValue(overview.p95_duration_ms, ' ms')} />
  </div>
}

export function RequestTimeline({ data }: { data: RequestAnalytics }) {
  const { t } = useTranslation()
  const maximum = Math.max(1, ...data.timeline.map(point => point.total_requests))
  return <Panel title={t('upgrade.timeline')}>
    {!data.overview.total_requests ? <p className="text-text-secondary text-sm">{t('upgrade.empty')}</p> : <>
      <div className="flex gap-1 items-end h-36 overflow-x-auto" aria-label={t('upgrade.timeline')}>
        {data.timeline.map(point => <div key={point.started_at_ms} className="flex-1 min-w-2 h-full flex items-end" title={`${timestampValue(point.started_at_ms)} · ${point.total_requests}`}>
          <div className="w-full flex flex-col overflow-hidden rounded-t-sm" style={{ height: `${point.total_requests / maximum * 100}%` }}>
            <div className="bg-notion-blue" style={{ flex: point.in_progress }} /><div className="bg-warn" style={{ flex: point.cancelled }} /><div className="bg-err" style={{ flex: point.failed + point.rejected + point.interrupted }} /><div className="bg-ok" style={{ flex: point.success }} />
          </div>
        </div>)}
      </div>
      <div className="flex justify-between text-[10px] text-text-secondary mt-2"><span>{timestampValue(data.from)}</span><span>{timestampValue(data.to)}</span></div>
      <p className="text-[11px] text-text-secondary mt-3">{t('upgrade.chart_legend')}</p>
      <details className="mt-3 text-[12px]"><summary className="cursor-pointer">{t('upgrade.table_view')}</summary><div className="overflow-x-auto"><table className="dashboard-table"><thead><tr><th>{t('upgrade.time')}</th><th>{t('upgrade.requests')}</th>{['success', 'failed', 'rejected', 'cancelled', 'interrupted', 'in_progress'].map(status => <th key={status}>{t(`upgrade.status_${status}`)}</th>)}</tr></thead><tbody>{data.timeline.map(point => <tr key={point.started_at_ms}><td>{timestampValue(point.started_at_ms)}</td><td>{point.total_requests}</td><td>{point.success}</td><td>{point.failed}</td><td>{point.rejected}</td><td>{point.cancelled}</td><td>{point.interrupted}</td><td>{point.in_progress}</td></tr>)}</tbody></table></div></details>
    </>}
  </Panel>
}
