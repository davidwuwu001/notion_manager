import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { fetchRequestLog, fetchRequestLogs } from '../api'
import { emptyLogFilters, metricValue, timestampValue } from '../dashboard'
import type { RequestLogDetail } from '../types'
import { buttonClass, Coverage, Panel, RequestFilters, ResourceState, useAdminResource } from './DashboardShared'

export function LogsPanel() {
  const { t } = useTranslation()
  const [filters, setFilters] = useState({ ...emptyLogFilters })
  const [cursors, setCursors] = useState<string[]>([''])
  const [selected, setSelected] = useState('')
  const cursor = cursors[cursors.length - 1]
  const logs = useAdminResource(signal => fetchRequestLogs(filters, cursor, 50, signal), [filters, cursor])
  return <div>
    <h1 className="text-xl font-semibold mb-5">{t('upgrade.logs')}</h1>
    <RequestFilters onApply={value => { setFilters(value); setCursors(['']); setSelected('') }} />
    <ResourceState {...logs} retry={logs.reload} />
    {logs.data && <>
      <Coverage data={logs.data} />
      <div className="flex justify-between items-center mb-3 gap-3 text-sm"><span>{t('upgrade.total_rows', { count: logs.data.total })}</span><button className={buttonClass} onClick={logs.reload}>{t('upgrade.refresh')}</button></div>
      <div className="overflow-x-auto border border-border rounded-lg">
        <table className="dashboard-table"><thead><tr>{['time', 'model', 'filter_protocol', 'status', 'tokens', 'duration', 'attempts'].map(key => <th key={key}>{t(`upgrade.${key}`)}</th>)}</tr></thead><tbody>
          {logs.data.items.map(log => <tr key={log.request_id}>
            <td><button className="text-notion-blue underline text-left cursor-pointer" onClick={() => setSelected(log.request_id)}>{timestampValue(log.started_at_ms)}</button><div className="text-[10px] text-text-secondary font-mono">{log.request_id}</div></td>
            <td className="break-all">{log.requested_model || '—'}</td><td>{log.protocol}</td><td>{t(`upgrade.status_${log.status}`)}</td>
            <td className="whitespace-nowrap">{metricValue(log.input_tokens)} / {metricValue(log.output_tokens)}<div className="text-[10px] text-text-secondary">{t(`upgrade.usage_${log.usage_source}`)}</div></td>
            <td>{metricValue(log.duration_ms, ' ms')}</td><td>{log.attempt_count}</td>
          </tr>)}
          {logs.data.items.length === 0 && <tr><td colSpan={7}>{t('upgrade.empty')}</td></tr>}
        </tbody></table>
      </div>
      <div className="flex justify-center gap-3 items-center mt-4">
        <button className={buttonClass} disabled={cursors.length === 1 || logs.loading} onClick={() => setCursors(values => values.slice(0, -1))}>{t('common.prev_page')}</button>
        <span className="text-sm">{t('upgrade.page', { page: cursors.length })}</span>
        <button className={buttonClass} disabled={!logs.data.next_cursor || logs.loading} onClick={() => { const next = logs.data?.next_cursor; if (next) setCursors(values => [...values, next]) }}>{t('common.next_page')}</button>
      </div>
    </>}
    {selected && <LogDetail id={selected} onClose={() => setSelected('')} />}
  </div>
}

function LogDetail({ id, onClose }: { id: string; onClose: () => void }) {
  const { t } = useTranslation()
  const detail = useAdminResource<RequestLogDetail>(signal => fetchRequestLog(id, signal), [id])
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const element = dialog.current
    element?.showModal()
    return () => { element?.close() }
  }, [])
  const request = detail.data?.request
  const rows = request ? [
    ['request_id', request.request_id], ['time', timestampValue(request.started_at_ms)],
    ['model', request.requested_model || '—'], ['resolved_model', request.resolved_model || '—'],
    ['filter_protocol', request.protocol], ['status', t(`upgrade.status_${request.status}`)],
    ['http_status', request.http_status || '—'], ['duration', metricValue(request.duration_ms, ' ms')],
    ['first_token', metricValue(request.first_token_ms, ' ms')], ['filter_account_id', request.final_account_id || '—'],
    ['stream', request.stream ? 'SSE' : 'JSON'], ['error_code', request.error_code || '—'],
    ['input_tokens', metricValue(request.input_tokens)], ['output_tokens', metricValue(request.output_tokens)],
    ['usage_source', t(`upgrade.usage_${request.usage_source}`)], ['usage_completeness', request.usage_completeness],
    ['partial_response', t(request.partial_response ? 'upgrade.yes' : 'upgrade.no')],
    ['reported_tokens', `${metricValue(request.reported_input_tokens)} / ${metricValue(request.reported_output_tokens)}`],
    ['estimated_tokens', `${metricValue(request.estimated_input_tokens)} / ${metricValue(request.estimated_output_tokens)}`],
    ['upstream_calls', `${request.upstream_call_count} (${t('upgrade.unknown_calls')}: ${request.unknown_calls})`],
  ] : []
  return <dialog ref={dialog} onCancel={onClose} onClick={event => { if (event.target === event.currentTarget) onClose() }} aria-labelledby="request-detail-title" className="w-[min(900px,94vw)] max-h-[90vh] rounded-xl border border-border bg-bg-primary text-text-primary p-5 backdrop:bg-black/70">
    <div className="flex items-center justify-between gap-3 mb-4"><h2 id="request-detail-title" className="font-semibold">{t('upgrade.request_detail')}</h2><button className={buttonClass} onClick={onClose}>{t('common.close')}</button></div>
    <ResourceState {...detail} retry={detail.reload} />
    {detail.data && <><Coverage data={detail.data} /><dl className="grid sm:grid-cols-2 gap-x-6 gap-y-3 mb-5 text-[12px]">{rows.map(([key, value]) => <div key={key} className="min-w-0"><dt className="text-text-secondary">{t(`upgrade.${key}`)}</dt><dd className="break-all font-mono mt-1">{value}</dd></div>)}</dl>
      <Panel title={t('upgrade.attempts')}>
        {!detail.data.attempts.length && <p className="text-sm text-text-secondary">{t('upgrade.empty')}</p>}
        <ol className="space-y-3">{detail.data.attempts.map(attempt => <li key={attempt.attempt_no} className="border border-border rounded-md p-3 text-[12px] space-y-1 break-words">
          <div className="font-semibold">#{attempt.attempt_no} · {attempt.account_id} · {attempt.outcome}</div>
          <p>{timestampValue(attempt.started_at_ms)} · {metricValue(attempt.duration_ms, ' ms')}</p>
          <p>{t('upgrade.error_code')}: {attempt.error_code || '—'} · {t('upgrade.retry_reason')}: {attempt.retry_reason || '—'}</p>
          <p>{t('upgrade.recovery')}: {timestampValue(attempt.cooldown_until_ms)}</p>
          <p>{t('upgrade.tokens')}: {metricValue(attempt.input_tokens)} / {metricValue(attempt.output_tokens)} · {t(`upgrade.usage_${attempt.usage_source}`)} · {attempt.usage_completeness}</p>
          <p>{t('upgrade.committed')}: {t(attempt.response_committed ? 'upgrade.yes' : 'upgrade.no')} · {t('upgrade.content_emitted')}: {t(attempt.content_emitted ? 'upgrade.yes' : 'upgrade.no')} · {t('upgrade.tool_emitted')}: {t(attempt.tool_call_emitted ? 'upgrade.yes' : 'upgrade.no')}</p>
        </li>)}</ol>
      </Panel>
    </>}
  </dialog>
}
