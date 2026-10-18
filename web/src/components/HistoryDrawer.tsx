import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { RegisterJob, RegisterStep } from '../types'
import { getJob, listJobs } from '../api'
import { buttonClass, ResourceState, useAdminResource } from './DashboardShared'
import { IconCheck, IconChevronRight, IconSpinner, IconX } from './Icons'

export function HistoryDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  if (!open) return null
  return <ReadonlyHistory onClose={onClose} />
}

function ReadonlyHistory({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation()
  const jobs = useAdminResource(signal => listJobs(50, signal), [])
  const [selected, setSelected] = useState('')
  const [detail, setDetail] = useState<RegisterJob | null>(null)
  const [detailError, setDetailError] = useState('')
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    const element = dialog.current
    element?.showModal()
    return () => { element?.close() }
  }, [])
  useEffect(() => {
    if (!selected) return
    const controller = new AbortController()
    setDetail(null); setDetailError('')
    getJob(selected, controller.signal).then(value => { if (!controller.signal.aborted) setDetail(value) }).catch(reason => {
      if (!controller.signal.aborted) setDetailError(reason instanceof Error ? reason.message : String(reason))
    })
    return () => controller.abort()
  }, [selected])
  return <dialog ref={dialog} onCancel={onClose} onClick={event => { if (event.target === event.currentTarget) onClose() }} aria-labelledby="history-title" className="w-[min(640px,94vw)] max-h-[90vh] overflow-auto rounded-xl bg-bg-secondary text-text-primary border border-border p-5 backdrop:bg-black/70">
      <div className="flex justify-between items-center mb-4 gap-3"><h2 id="history-title" className="font-semibold">{t('upgrade.history_readonly')}</h2><button className={buttonClass} onClick={onClose} autoFocus>{t('common.close')}</button></div>
      <ResourceState {...jobs} retry={jobs.reload} />
      {jobs.data?.length === 0 && <p className="text-text-secondary text-sm">{t('history.no_tasks')}</p>}
      <ul className="space-y-3">{jobs.data?.map(job => <li key={job.id} className="border border-border rounded-md p-3">
        <button className="w-full text-left cursor-pointer text-[12px]" onClick={() => setSelected(selected === job.id ? '' : job.id)} aria-expanded={selected === job.id}>{new Date(job.created_at).toLocaleString()} · {job.state} · {job.ok}/{job.total}</button>
        {selected === job.id && <div className="mt-3">{detailError && <p role="alert" className="text-err text-sm">{detailError}</p>}{!detail && !detailError && <p role="status">{t('upgrade.loading')}</p>}{detail?.steps.map((step,index) => <DetailStep key={index} step={step} index={index} />)}</div>}
      </li>)}</ul>
  </dialog>
}

function DetailStep({ step, index }: { step: RegisterStep; index: number }) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  let icon: React.ReactNode
  let label: string
  let cls = ''
  switch (step.status) {
    case 'ok':
      icon = <IconCheck size={11} />
      label = t('history.step_success')
      cls = 'text-ok'
      break
    case 'fail':
      icon = <IconX size={11} />
      label = t('history.step_failed')
      cls = 'text-err'
      break
    case 'running':
      icon = <IconSpinner size={11} className="animate-spin" />
      label = t('history.step_running')
      cls = 'text-notion-blue'
      break
    default:
      icon = null
      label = t('history.step_wait')
      cls = 'text-text-muted'
  }
  return (
    <div className={`px-3 py-2 ${step.status === 'fail' ? 'bg-err/[.04]' : ''}`}>
      <div className="flex items-center gap-2">
        <span className="text-[10px] text-text-muted w-7 tabular-nums shrink-0">#{index + 1}</span>
        <span className="text-[12px] text-text-primary font-mono truncate flex-1 min-w-0">{step.email || '—'}</span>
        <span className={`inline-flex items-center gap-1 text-[11px] ${cls} shrink-0`}>
          {icon}
          {label}
        </span>
      </div>
      {step.status === 'fail' && step.message && (
        <div className="mt-1 ml-9">
          <button
            onClick={() => setOpen((v) => !v)}
            className="text-[11px] text-text-secondary hover:text-text-primary inline-flex items-center gap-1 bg-transparent border-none p-0 cursor-pointer"
          >
            <IconChevronRight size={11} className={open ? 'rotate-90 transition-transform' : 'transition-transform'} />
            {open ? t('modal.register.collapse') : t('modal.register.view_details')}
          </button>
          {open && (
            <pre className="mt-1 p-2 bg-bg-input border border-border rounded text-[11px] text-text-secondary whitespace-pre-wrap break-all max-h-48 overflow-auto font-mono">
              {step.message}
            </pre>
          )}
        </div>
      )}
      {step.status === 'ok' && step.file && (
        <div className="mt-0.5 ml-9 text-[10px] text-text-muted truncate">→ {step.file}</div>
      )}
    </div>
  )
}
