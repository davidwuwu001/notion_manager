import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { fetchDashboardData } from '../api'
import { missingGroupAccountIds, toggleGroupAccount } from '../access-groups'
import { Panel, ResourceState, buttonClass, inputClass, useAdminResource } from './DashboardShared'

interface Group { name: string; account_ids: string[] }
interface Key { id: string; name: string; group: string }
interface Access { groups: Group[]; keys: Key[] }

async function accessRequest<T>(body?: object, signal?: AbortSignal): Promise<T> {
  const response = await fetch('/admin/access', { method: body ? 'POST' : 'GET', credentials: 'same-origin', signal, headers: { Accept: 'application/json', ...(body ? { 'Content-Type': 'application/json' } : {}) }, body: body ? JSON.stringify(body) : undefined })
  if (response.status === 401) window.dispatchEvent(new Event('notion-manager-unauthorized'))
  if (!response.ok) throw new Error(`HTTP ${response.status}`)
  return response.json() as Promise<T>
}

export function AccessGroups() {
  const { t } = useTranslation()
  const access = useAdminResource(signal => accessRequest<Access>(undefined, signal), [])
  const accounts = useAdminResource(signal => fetchDashboardData({}, signal), [])
  const [name, setName] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const [editing, setEditing] = useState(false)
  const [keyName, setKeyName] = useState('')
  const [keyGroup, setKeyGroup] = useState('')
  const [keyID, setKeyID] = useState('')
  const [secret, setSecret] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const missing = accounts.data ? missingGroupAccountIds(selected, accounts.data.accounts) : []
  const run = async (body: object) => {
    setPending(true); setError(''); setSecret('')
    try { const result = await accessRequest<{ secret?: string }>(body); if (result.secret) setSecret(result.secret); access.reload() }
    catch { setError(t('group_keys.failed')); accounts.reload() }
    finally { setPending(false) }
  }
  return <Panel title={t('group_keys.title')}>
    <p className="text-xs text-text-secondary mb-3">{t('group_keys.note')}</p>
    <ResourceState {...access} retry={access.reload} /><ResourceState {...accounts} retry={accounts.reload} />
    {error && <p role="alert" className="text-err text-sm">{error}</p>}
    {access.data && accounts.data && <div className="space-y-5">
      <section className="space-y-2">
        <h3 className="text-sm font-semibold">{t('group_keys.groups')}</h3>
        {access.data.groups.map(group => <div key={group.name} className="flex flex-wrap gap-2 items-center text-sm"><span>{group.name} · {group.account_ids.length}</span><button type="button" className={buttonClass} onClick={() => { setName(group.name); setSelected(group.account_ids); setEditing(true) }}>{t('group_keys.edit')}</button><button type="button" className={buttonClass} disabled={pending} onClick={() => { if (window.confirm(t('group_keys.delete_group_confirm'))) void run({ action: 'delete_group', name: group.name }) }}>{t('group_keys.delete')}</button></div>)}
        <form className="space-y-2" onSubmit={event => { event.preventDefault(); void run({ action: 'set_group', name: name.trim(), account_ids: selected }) }}>
          <input className={inputClass} placeholder={t('group_keys.group_name')} aria-label={t('group_keys.group_name')} value={name} disabled={pending || editing} onChange={event => setName(event.target.value)} required maxLength={64} />
          <div className="max-h-48 overflow-auto grid gap-1">
            {accounts.data.accounts.map(account => <label className="text-sm flex gap-2" key={account.account_id}><input type="checkbox" checked={selected.includes(account.account_id)} onChange={event => setSelected(old => toggleGroupAccount(old, account.account_id, event.target.checked))} />{account.space || account.name} · {account.email}</label>)}
            {missing.map(id => <label className="text-sm text-warn flex gap-2 break-all" key={id}><input type="checkbox" checked onChange={() => setSelected(old => toggleGroupAccount(old, id, false))} />{t('group_keys.missing_account', { id })}</label>)}
          </div>
          {missing.length > 0 && <p role="status" className="text-xs text-warn">{t('group_keys.remove_missing')}</p>}
          <button className={buttonClass} disabled={pending || !name.trim() || missing.length > 0 || !!accounts.error}>{t('group_keys.save_group')}</button>{editing && <button type="button" className={buttonClass} onClick={() => { setEditing(false); setName(''); setSelected([]) }}>{t('group_keys.new_group')}</button>}
        </form>
      </section>
      <section className="space-y-2">
        <h3 className="text-sm font-semibold">{t('group_keys.keys')}</h3>
        {access.data.keys.map(key => <div key={key.id} className="flex flex-wrap gap-2 items-center text-sm"><span>{key.name} · {key.group}</span><button type="button" className={buttonClass} onClick={() => { setKeyID(key.id); setKeyName(key.name); setKeyGroup(key.group); setSecret('') }}>{t('group_keys.edit')}</button><button type="button" className={buttonClass} disabled={pending} onClick={() => { if (window.confirm(t('group_keys.delete_key_confirm'))) void run({ action: 'delete_key', id: key.id }) }}>{t('group_keys.revoke')}</button></div>)}
        <form className="flex flex-wrap gap-2" onSubmit={event => { event.preventDefault(); void run({ action: keyID ? 'update_key' : 'create_key', id: keyID, name: keyName.trim(), group: keyGroup }) }}>
          <input className={inputClass} placeholder={t('group_keys.key_name')} aria-label={t('group_keys.key_name')} value={keyName} onChange={event => setKeyName(event.target.value)} required maxLength={64} />
          <select className={inputClass} aria-label={t('group_keys.group_name')} value={keyGroup} onChange={event => setKeyGroup(event.target.value)} required><option value="">{t('group_keys.choose_group')}</option>{access.data.groups.map(group => <option key={group.name} value={group.name}>{group.name}</option>)}</select>
          <button className={buttonClass} disabled={pending || !keyName.trim() || !keyGroup}>{keyID ? t('group_keys.save_key') : t('group_keys.create_key')}</button>
          {keyID && <button type="button" className={buttonClass} onClick={() => { setKeyID(''); setKeyName(''); setKeyGroup('') }}>{t('group_keys.new_key')}</button>}
        </form>
        {secret && <div role="status" className="rounded border border-border p-3 text-sm break-all"><p>{t('group_keys.secret_once')}</p><code className="select-all">{secret}</code><button type="button" className={buttonClass} onClick={() => setSecret('')}>{t('group_keys.close_secret')}</button></div>}
      </section>
    </div>}
  </Panel>
}
