import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { fetchRouting, fetchSettings, updateRouting, updateSettings } from '../api'
import type { SearchSettings } from '../api'
import type { RoutingConfig } from '../types'
import { buttonClass, inputClass, Panel, ResourceState, useAdminResource } from './DashboardShared'
import { HistoryDrawer } from './HistoryDrawer'

export function SettingsPanel() {
  const { t } = useTranslation()
  const settings = useAdminResource(fetchSettings, [])
  const routing = useAdminResource(fetchRouting, [])
  const [settingsDraft, setSettingsDraft] = useState<SearchSettings | null>(null)
  const [routingDraft, setRoutingDraft] = useState<RoutingConfig | null>(null)
  const [saving, setSaving] = useState<'settings' | 'routing' | null>(null)
  const [saveError, setSaveError] = useState('')
  const [saved, setSaved] = useState('')
  const [historyOpen, setHistoryOpen] = useState(false)
  useEffect(() => { if (settings.data) setSettingsDraft(settings.data) }, [settings.data])
  useEffect(() => {
    if (!routing.data) return
    const { mode, max_attempts, max_concurrent_per_account, disabled_account_ids, retention_days } = routing.data
    setRoutingDraft({ mode, max_attempts, max_concurrent_per_account, disabled_account_ids, retention_days })
  }, [routing.data])

  const save = async (kind: 'settings' | 'routing') => {
    setSaving(kind); setSaveError(''); setSaved('')
    try {
      if (kind === 'settings' && settingsDraft) {
        const { enable_web_search, enable_workspace_search, ask_mode_default, debug_logging, notion_proxy } = settingsDraft
        await updateSettings({ enable_web_search, enable_workspace_search, ask_mode_default, debug_logging, notion_proxy: notion_proxy.trim() })
        setSettingsDraft(await fetchSettings())
      } else if (kind === 'routing' && routingDraft) {
        const { max_attempts, max_concurrent_per_account, retention_days } = routingDraft
        if (![max_attempts, max_concurrent_per_account, retention_days].every(Number.isInteger) || max_attempts < 0 || max_concurrent_per_account < 0 || retention_days < 1 || retention_days > 3650) throw new Error(t('upgrade.invalid_number'))
        // Keep member toggles made elsewhere while this settings draft was open.
        const latest = await fetchRouting()
        await updateRouting({ ...routingDraft, disabled_account_ids: latest.disabled_account_ids })
        const updated = await fetchRouting()
        setRoutingDraft({ mode: updated.mode, max_attempts: updated.max_attempts, max_concurrent_per_account: updated.max_concurrent_per_account, disabled_account_ids: updated.disabled_account_ids, retention_days: updated.retention_days })
      }
      setSaved(t('upgrade.saved'))
    } catch (reason) { setSaveError(reason instanceof Error ? reason.message : String(reason)) }
    finally { setSaving(null) }
  }

  return <div className="space-y-5">
    <h1 className="text-xl font-semibold">{t('upgrade.settings')}</h1>
    {saveError && <p role="alert" className="text-err text-sm break-words">{saveError}</p>}{saved && <p role="status" className="text-ok text-sm">{saved}</p>}
    <Panel title={t('upgrade.request_settings')}>
      <ResourceState {...settings} retry={settings.reload} />
      {settingsDraft && <form className="space-y-4" onSubmit={event => { event.preventDefault(); void save('settings') }}>
        <div className="grid sm:grid-cols-2 gap-4">{(['enable_web_search', 'enable_workspace_search', 'ask_mode_default', 'debug_logging'] as const).map(key => <label key={key} className="flex gap-3 items-center text-sm cursor-pointer"><input type="checkbox" checked={settingsDraft[key]} disabled={!!saving} onChange={event => setSettingsDraft(value => value && ({ ...value, [key]: event.target.checked }))} />{t(`upgrade.setting_${key}`)}</label>)}</div>
        <p className="text-[12px] text-text-secondary">{t('upgrade.ask_note')}</p>
        <label className="flex flex-col gap-2 text-sm">{t('upgrade.notion_proxy')}<input className={inputClass} value={settingsDraft.notion_proxy} disabled={!!saving} placeholder={t('api.direct_connection_tip')} onChange={event => setSettingsDraft(value => value && ({ ...value, notion_proxy: event.target.value }))} autoComplete="off" spellCheck={false} /></label>
        <button className={buttonClass} disabled={!!saving}>{saving === 'settings' ? t('upgrade.saving') : t('common.save')}</button>
      </form>}
    </Panel>
    <Panel title={t('upgrade.pool_settings')}>
      <ResourceState {...routing} retry={routing.reload} />
      {routingDraft && <form className="space-y-4" onSubmit={event => { event.preventDefault(); void save('routing') }}>
        <p className="text-[12px] text-text-secondary">{t('upgrade.pool_note')}</p>
        <label className="flex flex-col gap-2 text-sm">{t('upgrade.mode')}<select className={inputClass} value={routingDraft.mode} disabled={!!saving} onChange={event => setRoutingDraft(value => value && ({ ...value, mode: event.target.value as RoutingConfig['mode'] }))}><option value="quota">{t('upgrade.mode_quota')}</option><option value="round_robin">{t('upgrade.mode_round_robin')}</option></select></label>
        <div className="grid sm:grid-cols-3 gap-4">{(['max_attempts', 'max_concurrent_per_account', 'retention_days'] as const).map(key => <label key={key} className="flex flex-col gap-2 text-sm">{t(`upgrade.${key}`)}<input type="number" min={key === 'retention_days' ? 1 : 0} max={key === 'retention_days' ? 3650 : undefined} step={1} className={inputClass} value={Number.isNaN(routingDraft[key]) ? '' : routingDraft[key]} disabled={!!saving} onChange={event => setRoutingDraft(value => value && ({ ...value, [key]: event.target.value === '' ? NaN : Number(event.target.value) }))} required /></label>)}</div>
        <p className="text-[12px] text-text-secondary">{t('upgrade.budget_note')}</p><p className="text-[12px] text-text-secondary">{t('upgrade.retention_note')}</p>
        <button className={buttonClass} disabled={!!saving}>{saving === 'routing' ? t('upgrade.saving') : t('common.save')}</button>
      </form>}
    </Panel>
    <Panel title={t('upgrade.import_policy')}><p className="text-sm text-text-secondary mb-3">{t('upgrade.business_only')}</p><button className={buttonClass} onClick={() => setHistoryOpen(true)}>{t('upgrade.history_readonly')}</button></Panel>
    <HistoryDrawer open={historyOpen} onClose={() => setHistoryOpen(false)} />
  </div>
}
