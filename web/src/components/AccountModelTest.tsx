import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import type { AccountInfo } from '../types'
import { testAccountModel } from '../api'

export function AccountModelTest({ account }: { account: AccountInfo }) {
  const { t } = useTranslation()
  const models = (account.models || []).filter(item => item.id !== 'researcher' && item.id !== 'fast-researcher')
  const [model, setModel] = useState(models[0]?.id || '')
  const [running, setRunning] = useState(false)
  const [result, setResult] = useState<{ account: string; model: string; text: string } | null>(null)
  useEffect(() => { setResult(null) }, [account.account_id])
  useEffect(() => {
    if (!models.some(item => item.id === model)) { setModel(models[0]?.id || ''); setResult(null) }
  }, [account.models, model])
  return <div className="flex flex-wrap items-center gap-2 mt-2" onClick={event => event.stopPropagation()}>
    <select aria-label={t('model_test.choose')} value={model} disabled={running} onChange={event => { setModel(event.target.value); setResult(null) }} className="bg-bg-card border border-border rounded px-2 py-1 text-xs">
      {!model && <option value="">{t('model_test.no_models')}</option>}
      {models.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
    </select>
    <button className="text-notion-blue text-xs disabled:opacity-50" disabled={!model || running} onClick={async () => {
      setRunning(true); setResult(null)
      const target = { account: account.account_id, model }
      try { const value = await testAccountModel(target.account, target.model); setResult({ ...target, text: value.ok ? t('model_test.success', { seconds: (value.duration_ms / 1000).toFixed(1) }) : t(`model_test.reason_${value.reason || 'inference_failed'}`) }) }
      catch { setResult({ ...target, text: t('model_test.reason_network_error') }) }
      finally { setRunning(false) }
    }}>{running ? t('model_test.running') : t('model_test.button')}</button>
    <span className="text-[11px] text-text-muted">{t('model_test.cost_note')}</span>
    {result && result.account === account.account_id && result.model === model && <span role="status" className="text-xs text-text-secondary">{result.text}</span>}
  </div>
}
