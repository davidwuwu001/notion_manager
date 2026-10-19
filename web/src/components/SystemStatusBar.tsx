import { useState } from 'react'
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { fetchSystemStatus } from '../api'
import { timestampValue } from '../dashboard'
import { buttonClass, useAdminResource } from './DashboardShared'

function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  if (days > 0) return `${days}d ${hours}h`
  if (hours > 0) return `${hours}h ${minutes}m`
  return `${minutes}m`
}

function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return '—'
  const mb = bytes / 1024 / 1024
  return mb >= 1024 ? `${(mb / 1024).toFixed(1)} GB` : `${mb.toFixed(0)} MB`
}

function DetailGroup({ title, rows }: { title: string; rows: Array<[string, ReactNode]> }) {
  return <div>
    <div className="text-[11px] uppercase tracking-wider text-text-secondary mb-2">{title}</div>
    <dl className="space-y-1 text-[12px]">
      {rows.map(([label, value]) => <div key={label} className="flex justify-between gap-3">
        <dt className="text-text-secondary shrink-0">{label}</dt><dd className="tabular-nums break-words text-right">{value}</dd>
      </div>)}
    </dl>
  </div>
}

const chipClass = 'rounded-full border border-border px-2 py-0.5 text-[11px] text-text-secondary tabular-nums'

export function SystemStatusBar() {
  const { t } = useTranslation()
  const system = useAdminResource(fetchSystemStatus, [], 10000)
  const [open, setOpen] = useState(false)
  const data = system.data
  if (system.loading && !data) {
    return <section className="rounded-lg border border-border bg-bg-card px-4 py-3 mb-5 text-[12px] text-text-secondary">{t('upgrade.loading')}</section>
  }
  if (system.error && !data) {
    return <section className="rounded-lg border border-border bg-bg-card px-4 py-3 mb-5 text-[12px]">
      <span className="text-err">{t('system.error')} · {system.error}</span>
      <button className={`${buttonClass} ml-2`} onClick={system.reload}>{t('upgrade.retry')}</button>
    </section>
  }
  if (!data) return null

  const healthy = data.status === 'ok'
  const pool = data.pool
  const storage = data.storage
  const agent = data.launch_agent
  const storageLabel = storage.status === 'ok' ? t('system.storage_ok') : storage.status === 'degraded' ? t('system.storage_degraded') : t('system.storage_unavailable')
  const storageTone = storage.status === 'ok' ? 'text-text-secondary' : 'text-err'

  return <section className="rounded-lg border border-border bg-bg-card px-4 py-3 mb-5">
    <button type="button" className="flex w-full flex-wrap items-center gap-x-4 gap-y-2 text-left cursor-pointer" onClick={() => setOpen(value => !value)} aria-expanded={open}>
      <span className={`inline-block w-2 h-2 rounded-full ${healthy ? 'bg-ok' : 'bg-err'}`} aria-hidden />
      <span className="text-[13px] font-medium">{healthy ? t('system.running') : t('system.error')}</span>
      <span className="text-[11px] text-text-secondary tabular-nums">{t('system.uptime', { value: formatUptime(data.uptime_seconds) })}</span>
      <span className={pool.available > 0 ? chipClass : `${chipClass} text-warn`}>{t('system.pool_chip', { available: pool.available, total: pool.total })}</span>
      <span className={`${chipClass} ${storageTone}`}>{t('system.storage_chip', { status: storageLabel })}</span>
      <span className={`${chipClass} ${agent.managed ? 'text-ok' : 'text-warn'}`}>{agent.managed ? t('system.agent_managed') : t('system.agent_manual')}</span>
      <span className="ml-auto text-[11px] text-text-muted">{open ? t('system.collapse') : t('system.details')}</span>
    </button>
    {open && <div className="mt-4 border-t border-border pt-4 grid gap-5 sm:grid-cols-2 xl:grid-cols-4">
      <DetailGroup title={t('system.service')} rows={[
        [t('system.pid'), data.pid],
        [t('system.memory'), formatBytes(data.memory_bytes)],
        [t('system.go_version'), data.go_version],
        [t('system.listen'), data.listen_addr],
      ]} />
      <DetailGroup title={t('system.pool')} rows={[
        [t('stats.available'), `${pool.available} / ${pool.total}`],
        [t('system.paused'), pool.paused],
        [t('system.exhausted'), pool.exhausted],
        [t('system.cooldown'), pool.cooldown],
        [t('system.invalid'), pool.invalid],
        [t('system.in_flight'), pool.busy],
        [t('system.last_quota_check'), pool.last_quota_checked_ms > 0 ? timestampValue(pool.last_quota_checked_ms) : t('system.never')],
      ]} />
      <DetailGroup title={t('system.storage')} rows={[
        [t('system.storage_status'), storageLabel],
        [t('system.retention'), t('system.days', { count: storage.retention_days })],
        [t('system.data_complete'), storage.data_complete ? t('upgrade.yes') : t('upgrade.no')],
        [t('system.captured_since'), storage.captured_since_ms > 0 ? timestampValue(storage.captured_since_ms) : t('system.never')],
      ]} />
      <DetailGroup title="LaunchAgent" rows={[
        [t('system.managed'), agent.managed ? t('upgrade.yes') : t('upgrade.no')],
        [t('system.label'), agent.label || '—'],
        [t('system.state'), agent.state || '—'],
        [t('system.last_exit'), agent.last_exit_code ?? '—'],
      ]} />
    </div>}
  </section>
}