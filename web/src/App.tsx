import { useState, useEffect, useMemo, useCallback, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import type { DashboardData, AccountInfo, AccountSummary, RefreshStatus, TokenStats, RoutingData, RoutingMember } from './types'
import { fetchDashboardData, openProxy, openBestProxy, checkAuth, login, logout, triggerRefresh, addAccount, fetchTokenStats, fetchRouting, updateRouting } from './api'
import { fmt, formatTokens, getQuotaStatusByUsage, getQuotaPct, avatarColor, avatarLetter, formatCheckedAt, formatTimestampMs, providerDisplay } from './utils'
import { AccountMenu } from './components/AccountMenu'
import { LanguageToggle } from './components/LanguageToggle'
import { ModelsPanel } from './components/ModelsPanel'
import { parseAccountImport } from './account-import'
import type { AccountImport } from './account-import'
import { dashboardTabs, dashboardTab, timestampValue, quotaRefreshProgress, confirmedPremiumBalance, metricValue, routingMetrics, quotaErrorKey } from './dashboard'
import type { DashboardTab } from './dashboard'
import { OverviewPanel } from './components/OverviewPanel'
import { LogsPanel } from './components/LogsPanel'
import { AnalyticsPanel } from './components/AnalyticsPanel'
import { SettingsPanel } from './components/SettingsPanel'
import { ResourceState, buttonClass } from './components/DashboardShared'
import { startVisiblePolling } from './polling'

// --- Icons ---
const IconBarChart = () => (
  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
    <line x1="18" y1="20" x2="18" y2="10"/><line x1="12" y1="20" x2="12" y2="4"/><line x1="6" y1="20" x2="6" y2="14"/>
  </svg>
)
const IconRefresh = () => (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
    <path d="M21 2v6h-6"/><path d="M21 13a9 9 0 1 1-3-7.7L21 8"/>
  </svg>
)
const IconZap = () => (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
    <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
  </svg>
)
const IconClock = () => (
  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
    <circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>
  </svg>
)
const IconFlask = () => (
  <svg className="w-3 h-3" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M10 2v7.31" />
    <path d="M14 9.3V1.99" />
    <path d="M8.5 2h7" />
    <path d="M14 9.3a6.5 6.5 0 1 1-4 0" />
    <path d="M5.52 16h12.96" />
  </svg>
)
const IconActivity = () => (
  <svg width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round">
    <polyline points="22 12 18 12 15 21 9 3 6 12 2 12" />
  </svg>
)
const IconSettings = () => (
  <svg className="w-3.5 h-3.5 text-text-secondary" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <path d="M12.22 2h-.44a2 2 0 0 0-2 2v.18a2 2 0 0 1-1 1.73l-.43.25a2 2 0 0 1-2 0l-.15-.08a2 2 0 0 0-2.73.73l-.22.38a2 2 0 0 0 .73 2.73l.15.1a2 2 0 0 1 1 1.72v.51a2 2 0 0 1-1 1.74l-.15.09a2 2 0 0 0-.73 2.73l.22.38a2 2 0 0 0 2.73.73l.15-.08a2 2 0 0 1 2 0l.43.25a2 2 0 0 1 1 1.73V20a2 2 0 0 0 2 2h.44a2 2 0 0 0 2-2v-.18a2 2 0 0 1 1-1.73l.43-.25a2 2 0 0 1 2 0l.15.08a2 2 0 0 0 2.73-.73l.22-.39a2 2 0 0 0-.73-2.73l-.15-.08a2 2 0 0 1-1-1.74v-.5a2 2 0 0 1 1-1.74l.15-.09a2 2 0 0 0 .73-2.73l-.22-.38a2 2 0 0 0-2.73-.73l-.15.08a2 2 0 0 1-2 0l-.43-.25a2 2 0 0 1-1-1.73V4a2 2 0 0 0-2-2z" />
    <circle cx="12" cy="12" r="3" />
  </svg>
)

const IconPlus = () => (
  <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
  </svg>
)
const IconTrash = () => (
  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
    <polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
  </svg>
)

// --- Add Account Modal ---

function AddAccountModal({ onClose, onSuccess }: { onClose: () => void; onSuccess: () => void }) {
  const { t } = useTranslation()
  const [token, setToken] = useState('')
  const [importFile, setImportFile] = useState<{ name: string; config: AccountImport } | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [result, setResult] = useState<{ name: string; email: string; space: string; space_id?: string; plan_type: string } | null>(null)
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const successTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(successTimer.current), [])

  useEffect(() => { inputRef.current?.focus() }, [])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose() }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [onClose])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    const trimmed = token.trim()
    if (!trimmed) return
    setLoading(true)
    setError('')
    setResult(null)
    try {
      let config: AccountImport
      try { config = parseAccountImport(trimmed) }
      catch { throw new Error(t('browser_import.invalid_json')) }
      const res = await addAccount(config.token_v2, config.user_id, config.space_id)
      if (res.error) {
        setError(res.error)
      } else if (res.account) {
        setResult(res.account)
        successTimer.current = setTimeout(() => {
          onSuccess()
          onClose()
        }, 1500)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t('modal.register.submit_failed'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="fixed inset-0 z-[100] flex items-center justify-center bg-black/60 backdrop-blur-sm" onClick={onClose}>
      <div className="w-full max-w-lg max-h-[90vh] overflow-y-auto bg-[#1a1a1a] border border-white/10 rounded-xl shadow-2xl p-6" onClick={e => e.stopPropagation()}>
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-[16px] font-semibold">{t('modal.add.title')}</h2>
          <button onClick={onClose} className="text-text-muted hover:text-white bg-transparent border-none cursor-pointer text-lg px-1">×</button>
        </div>

        <div className="text-[12px] text-text-secondary mb-4 space-y-1.5">
          <p>{t('modal.add.subtitle')}</p>
          <p className="text-text-muted">{t('modal.add.how_to')}</p>
        </div>

        <details className="rounded-lg border border-border bg-bg-card p-3 text-[12px] mb-4">
          <summary className="font-medium text-text-primary cursor-pointer">{t('browser_import.title')}</summary>
          <ol className="list-decimal pl-4 mt-2 space-y-2 text-text-secondary">
            <li><a href="/dashboard/extension.zip" className="text-notion-blue underline" download>{t('browser_import.download')}</a>{t('browser_import.unzip')}</li>
            <li>{t('browser_import.install')} <code className="select-all">chrome://extensions</code>{t('browser_import.load')}</li>
            <li>{t('browser_import.use')}</li>
          </ol>
          <p className="mt-2 text-text-muted">{t('browser_import.privacy')}</p>
          <p className="mt-2 text-text-muted">{t('browser_import.refresh')}</p>
        </details>

        <form onSubmit={handleSubmit}>
          <label className="block text-[13px] font-medium mb-2" htmlFor="account-json-file">{t('browser_import.select_file')}</label>
          <input id="account-json-file" type="file" accept=".json,application/json" disabled={loading} className="block w-full text-[12px] mb-3"
            onChange={async event => {
              const file = event.currentTarget.files?.[0]
              event.currentTarget.value = ''
              if (!file) return
              setLoading(true)
              setError('')
              setResult(null)
              setImportFile(null)
              setToken('')
              try {
                if (file.size > 1024 * 1024) throw new Error(t('browser_import.file_too_large'))
                const text = await file.text()
                let config: AccountImport
                try { config = parseAccountImport(text, true) }
                catch { throw new Error(t('browser_import.invalid_json')) }
                setImportFile({ name: file.name, config })
                setToken(JSON.stringify(config))
              } catch (error) { setError(error instanceof Error ? error.message : t('browser_import.invalid_json')) }
              finally { setLoading(false) }
            }} />
          {importFile && <div className="text-[12px] border border-border rounded-md p-3 mb-3 break-all space-y-1">
            <p>{importFile.name}</p>
            <p>{importFile.config.user_name || '—'} · {importFile.config.user_email || '—'}</p>
            <p>{t('modal.add.space')} {importFile.config.space_name || '—'}</p>
            <p>{t('account.workspace_id')} <code>{importFile.config.space_id || t('browser_import.auto_workspace')}</code></p>
            <button type="button" className="text-notion-blue cursor-pointer" onClick={() => { setImportFile(null); setToken(''); setError(''); setResult(null) }}>{t('browser_import.use_manual')}</button>
          </div>}
          <p className="text-[11px] text-text-muted mb-2">{t('browser_import.file_privacy')}</p>
          {!importFile && <textarea
            ref={inputRef}
            value={token}
            onChange={e => setToken(e.target.value)}
            placeholder={t('modal.add.placeholder')}
            rows={3}
            className="w-full py-2.5 px-3 bg-transparent border border-white/10 rounded-lg text-[13px] text-text-primary outline-none focus:border-white/30 focus:ring-1 focus:ring-white/10 transition-all placeholder:text-white/25 resize-none font-mono"
          />}
          {error && (
            <div className="text-err text-[12px] mt-2 px-1">{error}</div>
          )}
          {result && (
            <div className="mt-3 p-3 bg-[#0a3d0a]/50 border border-[#1b5e20]/50 rounded-lg text-[12px]">
              <div className="text-[#4ade80] font-medium mb-1.5">{t('modal.add.success')}</div>
              <div className="space-y-0.5 text-text-secondary">
                <div>{t('modal.add.user')} <span className="text-white">{result.name}</span> ({result.email})</div>
                <div>{t('modal.add.space')} <span className="text-white">{result.space}</span> · {result.plan_type}</div>
                {result.space_id && <div className="break-all">{t('account.workspace_id')} {result.space_id}</div>}
              </div>
            </div>
          )}
          <div className="flex gap-2.5 mt-4">
            <button
              type="button"
              onClick={onClose}
              className="flex-1 py-2.5 bg-transparent hover:bg-white/5 text-text-secondary rounded-lg text-[13px] font-medium cursor-pointer transition-colors border border-white/10"
            >
              {t('common.cancel')}
            </button>
            <button
              type="submit"
              disabled={loading || !token.trim() || !!result}
              className="flex-1 py-2.5 bg-white hover:bg-white/90 text-black rounded-lg text-[13px] font-semibold cursor-pointer transition-colors border-none disabled:opacity-40 disabled:cursor-not-allowed"
            >
              {loading ? t('modal.add.verifying') : t('actions.add_account')}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}

// --- Login Page ---

function LoginPage({ onSuccess }: { onSuccess: () => void }) {
  const { t } = useTranslation()
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => { inputRef.current?.focus() }, [])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!password.trim()) return
    setLoading(true)
    setError('')
    try {
      const result = await login(password)
      if (result.ok) {
        onSuccess()
        return
      }
      setError(result.error || t('auth.wrong_password'))
      setPassword('')
      inputRef.current?.focus()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('auth.login_failed'))
      setPassword('')
      inputRef.current?.focus()
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center">
      <div className="w-full max-w-sm">
        <div className="flex flex-col items-center mb-8">
          <div className="w-12 h-12 bg-[#1a1a1a] border border-white/10 rounded-xl flex items-center justify-center text-xl font-extrabold text-white mb-4">N</div>
          <h1 className="text-xl font-semibold tracking-tight">notion-manager</h1>
          <p className="text-[13px] text-text-muted mt-1">{t('auth.prompt')}</p>
        </div>
        <form onSubmit={handleSubmit}>
          <div className="relative mb-4">
            <input
              ref={inputRef}
              type="password"
              value={password}
              onChange={e => setPassword(e.target.value)}
              placeholder={t('auth.placeholder')}
              autoComplete="current-password"
              className="w-full py-2.5 px-4 bg-transparent border border-white/10 rounded-lg text-[14px] text-text-primary outline-none focus:border-white/30 focus:ring-1 focus:ring-white/10 transition-all placeholder:text-white/25"
            />
          </div>
          {error && (
            <div className="text-err text-[12px] mb-3 px-1">{error}</div>
          )}
          <button
            type="submit"
            disabled={loading || !password.trim()}
            className="w-full py-2.5 bg-white hover:bg-white/90 text-black rounded-lg text-[14px] font-semibold cursor-pointer transition-colors border-none disabled:opacity-40 disabled:cursor-not-allowed"
          >
            {loading ? t('auth.logging_in') : t('auth.login')}
          </button>
        </form>
      </div>
    </div>
  )
}

// --- Header ---

function Header({ query, onQuery, onLogout, authRequired, showSearch }: {
  query: string; onQuery: (q: string) => void; onLogout: () => void; authRequired: boolean; showSearch: boolean
}) {
  const { t } = useTranslation()
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!showSearch) return
    const handler = (e: KeyboardEvent) => {
      if (e.key === '/') {
        // Don't hijack "/" when the user is already typing in another
        // input/textarea/contenteditable — otherwise modals like the
        // register form (proxy URL, credentials textarea, etc.) lose
        // focus mid-keystroke.
        const ae = document.activeElement as HTMLElement | null
        const inEditable =
          !!ae &&
          (ae.tagName === 'INPUT' ||
            ae.tagName === 'TEXTAREA' ||
            ae.tagName === 'SELECT' ||
            ae.isContentEditable)
        if (inEditable) return
        e.preventDefault()
        inputRef.current?.focus()
      }
      if (e.key === 'Escape' && document.activeElement === inputRef.current) {
        inputRef.current?.blur()
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [showSearch])

  return (
    <header className="sticky top-0 z-50 flex flex-wrap gap-3 items-center justify-between px-4 py-2.5 border-b border-border bg-bg-secondary/80 backdrop-blur-xl">
      <div className="flex items-center gap-2.5">
        <div className="w-7 h-7 bg-[#333] rounded-md flex items-center justify-center text-sm font-extrabold text-white">N</div>
        <span className="text-[15px] font-semibold tracking-tight">
          notion-manager
          <span className="text-text-secondary font-normal text-[13px] ml-1.5">dashboard</span>
        </span>
      </div>
      <div className="flex items-center gap-3">
        <div className={`relative w-72 max-sm:w-40 ${showSearch ? '' : 'hidden'}`}>
          <svg className="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-text-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <circle cx="11" cy="11" r="8" /><path d="m21 21-4.35-4.35" />
          </svg>
          <input
            ref={inputRef}
            value={query}
            onChange={e => onQuery(e.target.value)}
            placeholder={t('header.search_placeholder')}
            className="w-full py-1.5 pl-8 pr-10 bg-bg-input border border-border rounded-md text-[13px] text-text-primary outline-none focus:border-white/20 transition-colors placeholder:text-text-muted"
          />
          <kbd className="absolute right-2.5 top-1/2 -translate-y-1/2 text-[11px] text-text-muted bg-bg-card border border-border rounded px-1.5 py-0.5">/</kbd>
        </div>
        <LanguageToggle />
        {authRequired && (
          <button
            onClick={onLogout}
            className="text-[12px] text-text-secondary hover:text-text-primary cursor-pointer transition-colors bg-transparent border-none px-2 py-1"
            title={t('header.logout_title')}
          >
            {t('header.logout')}
          </button>
        )}
      </div>
    </header>
  )
}

function StatCard({ label, value, sub, color, icon }: { label: string; value: string | number; sub: string; color?: string; icon?: React.ReactNode }) {
  return (
    <div className="px-6 py-5">
      <div className="text-[11px] text-text-secondary uppercase tracking-wider mb-1 flex items-center gap-1.5">
        {icon}
        <span>{label}</span>
      </div>
      <div className="text-2xl font-bold tracking-tight tabular-nums" style={color ? { color } : undefined}>{value}</div>
      <div className="text-[11px] text-text-muted mt-1 truncate">{sub}</div>
    </div>
  )
}

function hasPremiumAccess(account: AccountInfo): boolean {
  const balance = confirmedPremiumBalance(account.premium_balance, account.premium_balance_known)
  return !!account.has_premium || (account.premium_limit || 0) > 0 || (balance !== null && balance > 0)
}

function getSpaceQuota(account: AccountInfo) {
  const usage = account.space_usage ?? account.usage ?? 0
  const limit = account.space_limit ?? account.limit ?? 0
  const remaining = account.space_remaining ?? Math.max(limit - usage, 0)
  return { usage, limit, remaining }
}

function getUserQuota(account: AccountInfo) {
  const usage = account.user_usage ?? 0
  const limit = account.user_limit ?? 0
  const remaining = account.user_remaining ?? Math.max(limit - usage, 0)
  return { usage, limit, remaining }
}

function isSameQuota(a: { usage: number; limit: number }, b: { usage: number; limit: number }): boolean {
  return a.limit > 0 && a.limit === b.limit && a.usage === b.usage
}

function isResearchLimited(account: AccountInfo): boolean {
  return !hasPremiumAccess(account) && (account.research_usage ?? 0) >= 3
}

function mergeQuotaStatus(statuses: Array<'ok' | 'low' | 'exhausted'>): 'ok' | 'low' | 'exhausted' {
  if (statuses.includes('exhausted')) return 'exhausted'
  if (statuses.includes('low')) return 'low'
  return 'ok'
}

function OverviewBar({ label, usage, limit }: { label: string; usage: number; limit: number }) {
  const { t } = useTranslation()
  const pct = getQuotaPct(usage, limit)
  const remaining = Math.max(limit - usage, 0)
  const status = getQuotaStatusByUsage(usage, limit)
  const fillClass = status === 'exhausted' ? 'bg-err opacity-40'
    : status === 'low' ? 'bg-warn' : 'bg-ok'
  const numColor = status === 'exhausted' ? 'text-err'
    : status === 'low' ? 'text-warn' : 'text-text-primary'

  return (
    <div>
      <div className="flex justify-between items-center mb-1.5">
        <span className="text-[10px] text-text-muted uppercase tracking-wider">{label}</span>
        <span className={`text-[11px] font-semibold tabular-nums ${numColor}`}>
          {fmt(remaining)} <span className="text-text-muted font-normal">/ {fmt(limit)} {t('stats.remaining')}</span>
        </span>
      </div>
      <div className="h-[2px] bg-white/[.06] rounded-full overflow-hidden">
        <div className={`h-full rounded-full transition-all duration-500 ${fillClass}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

function TotalQuotaBar({ summary }: { summary?: AccountSummary | null }) {
  const { t } = useTranslation()
  const totalSpaceUsage = summary?.total_space_usage ?? 0
  const totalSpaceLimit = summary?.total_space_limit ?? 0
  const totalUserUsage = summary?.total_user_usage ?? 0
  const totalUserLimit = summary?.total_user_limit ?? 0
  const totalPremiumBalance = confirmedPremiumBalance(summary?.total_premium_balance, summary?.premium_unknown_accounts != null)
  const sameBasicQuota = isSameQuota(
    { usage: totalSpaceUsage, limit: totalSpaceLimit },
    { usage: totalUserUsage, limit: totalUserLimit },
  )

  return (
    <div className="mb-5 space-y-3">
      <div className="flex flex-wrap gap-3 justify-between items-center">
        <span className="text-[11px] text-text-secondary uppercase tracking-wider flex items-center gap-1.5"><IconBarChart /> {t('stats.basic_overview')}</span>
        {summary && (
          <span className="text-[12px] text-text-muted tabular-nums">
            {t('quota.known_premium_summary', { balance: metricValue(totalPremiumBalance), count: metricValue(summary.premium_unknown_accounts) })}
          </span>
        )}
      </div>
      <p className="text-[11px] text-text-secondary">{t('quota.compatibility_note')}</p>
      {sameBasicQuota ? (
        <OverviewBar label="Basic" usage={totalSpaceUsage} limit={totalSpaceLimit} />
      ) : (
        <>
          <OverviewBar label="Space" usage={totalSpaceUsage} limit={totalSpaceLimit} />
          <OverviewBar label="User" usage={totalUserUsage} limit={totalUserLimit} />
        </>
      )}
    </div>
  )
}

function AIUsage({ account }: { account: AccountInfo }) {
  const { t } = useTranslation()
  const rate = account.rate_limit
  const windows = rate ? [
    { label: t('ai_usage.rolling', { window: rate.window.window }), used: rate.window.used, limit: rate.window.limit, reset: rate.rolling_reset_at_ms },
    { label: t('ai_usage.monthly'), used: rate.billingPeriodWindow.used, limit: rate.billingPeriodWindow.limit, reset: rate.billingPeriodWindow.periodEndMs },
  ] : []
  return (
    <section className="my-3 rounded-md border border-border bg-bg-card p-3" aria-label={t('ai_usage.title')}>
      <div className="text-[12px] font-semibold mb-2">{t('ai_usage.title')}</div>
      {account.quota_sources && <div className="space-y-1 mb-2 text-[10px] text-text-secondary">
        {(['basic', 'premium', 'allowance'] as const).map(source => {
          const state = account.quota_sources?.[source]
          if (!state) return null
          return <div key={source}>
            {t(`quota.source_${source}`)} · {t('quota.last_success', { time: timestampValue(state.succeeded_at_ms) })}
            {state.error && <p role="status" className="text-warn">{t('quota.source_failed', { time: timestampValue(state.checked_at_ms), reason: t(quotaErrorKey(state.error)) })}</p>}
          </div>
        })}
      </div>}
      {account.rate_limit_error && <p role="status" className="text-[11px] text-warn mb-2">{t('quota.rate_read_failed', { reason: t(quotaErrorKey(account.rate_limit_error)) })}</p>}
      {!rate && <p className="text-[11px] text-text-secondary">{t('ai_usage.unavailable')}</p>}
      {windows.map(w => w.limit > 0 ? (
        <div key={w.label} className="mb-2 text-[11px]">
          <div className="flex flex-wrap justify-between gap-2">
            <span>{w.label} · {t('ai_usage.used', { percent: Math.round(w.used / w.limit * 100) })}</span>
            <span className="text-text-secondary">{w.reset > 0 ? t('ai_usage.resets', { date: new Date(w.reset).toLocaleString() }) : t('ai_usage.reset_unknown')}</span>
          </div>
          <progress aria-label={w.label} max={w.limit} value={Math.max(0, Math.min(w.used, w.limit))} className="w-full h-1.5 accent-[#2383e2]" />
          <div className="text-text-muted tabular-nums">{w.used} / {w.limit}</div>
        </div>
      ) : <p key={w.label} className="text-text-secondary text-[11px]">{w.label} · {t('ai_usage.unavailable')}</p>)}
      <p className="text-[10px] text-text-muted">{t('ai_usage.note')}</p>
    </section>
  )
}

function QuotaBar({ label, labelClass, usage, limit, status }: { label: string; labelClass?: string; usage?: number; limit?: number; status?: 'ok' | 'low' | 'exhausted' }) {
  const pct = getQuotaPct(usage, limit)
  const resolvedStatus = status || getQuotaStatusByUsage(usage, limit)
  const fillClass = resolvedStatus === 'exhausted' ? 'bg-err opacity-40'
    : resolvedStatus === 'low' ? 'bg-warn' : 'bg-ok'
  const numColor = resolvedStatus === 'exhausted' ? 'text-err'
    : resolvedStatus === 'low' ? 'text-warn' : 'text-text-primary'

  return (
    <div className="mb-1.5">
      <div className="flex justify-between items-baseline mb-1">
        <span className={`text-[10px] ${labelClass || 'text-text-muted'}`}>{label}</span>
        <span className={`text-[11px] font-semibold tabular-nums ${numColor}`}>
          {fmt(usage || 0)} <span className="text-text-muted font-normal">/</span> {fmt(limit || 0)}
        </span>
      </div>
      <div className="h-[2px] bg-white/[.06] rounded-full overflow-hidden">
        <div className={`h-full rounded-full transition-all duration-500 ${fillClass}`} style={{ width: `${pct}%` }} />
      </div>
    </div>
  )
}

function Badge({ children, variant }: { children: React.ReactNode; variant: 'plan' | 'premium' | 'research' | 'warning' | 'model' }) {
  const cls: Record<string, string> = {
    plan: 'text-text-secondary',
    premium: 'text-[#7eb8ff]',
    research: 'text-research',
    warning: 'text-red-400 bg-red-500/10 px-1.5 rounded',
    model: 'text-text-secondary hover:text-white transition-colors cursor-pointer',
  }
  return (
    <span className={`inline-flex items-center gap-1.5 py-0.5 text-[11px] font-medium whitespace-nowrap ${cls[variant] || ''}`}>
      {children}
    </span>
  )
}

function AccountCard({ account, onChanged, routing, onToggle, toggling }: { account: AccountInfo; onChanged: () => void; routing?: RoutingMember; onToggle: (member: RoutingMember) => Promise<void>; toggling: boolean }) {
  const { t } = useTranslation()
  const [workspaceCopy, setWorkspaceCopy] = useState<'idle' | 'copied' | 'failed'>('idle')
  const [showModels, setShowModels] = useState(false)
  const spaceQuota = getSpaceQuota(account)
  const userQuota = getUserQuota(account)
  const sameBasicQuota = isSameQuota(spaceQuota, userQuota)
  const premium = hasPremiumAccess(account)
  const premiumBalance = confirmedPremiumBalance(account.premium_balance, account.premium_balance_known)
  const researchLimited = isResearchLimited(account)
  const noWorkspace = !!account.no_workspace
  // Availability is determined by backend eligible/exhausted flags,
  // NOT by usage > limit or remaining === 0.
  const isAvailable = account.eligible === true && !account.exhausted && !account.permanent && !noWorkspace
  const status = !isAvailable
    ? 'exhausted'
    : mergeQuotaStatus([
      getQuotaStatusByUsage(spaceQuota.usage, spaceQuota.limit),
      getQuotaStatusByUsage(userQuota.usage, userQuota.limit),
    ])
  const modelCount = account.models?.length || 0

  const dotCls = !isAvailable ? 'bg-err' : status === 'low' ? 'bg-warn' : 'bg-ok'
  // no_workspace shares the exhausted card style so the operator
  // immediately sees the account is unhealthy. Click-through is blocked
  // because Notion's /ai SPA hangs indefinitely on these accounts (the
  // root-cause this fix is for).
  const cardBg = account.permanent ? 'bg-bg-exhausted border-white/[0.03] opacity-55'
    : account.exhausted || noWorkspace ? 'bg-bg-exhausted border-white/[0.03]'
    : 'bg-bg-card hover:bg-bg-card-hover border-white/[0.03] hover:border-white/[0.07]'

  const handleClick = () => {
    if (noWorkspace) {
      // Use a native alert — we don't have a toast infra and openProxy
      // would otherwise pop a tab that displays raw JSON 409 to the user.
      alert(t('account.no_workspace_alert'))
      return
    }
    openProxy(account.account_id)
  }

  return (
    <div
      className={`rounded-lg p-4 border ${noWorkspace ? 'cursor-not-allowed' : 'cursor-pointer hover:-translate-y-0.5 hover:shadow-lg hover:shadow-black/30'} transition-all duration-200 ${cardBg}`}
      onClick={handleClick}
      title={noWorkspace ? t('account.no_workspace_tooltip') : undefined}
    >
      {/* Header */}
      <div className="flex items-center gap-2.5 mb-2.5">
        <div
          className="w-8 h-8 rounded-full flex items-center justify-center text-sm font-bold text-white shrink-0"
          style={{ background: avatarColor(account.name) }}
        >
          {avatarLetter(account.name)}
        </div>
        <div className="flex-1 min-w-0">
          <div className="text-[13px] font-semibold truncate">
            {account.name || 'Unknown'}
            {account.space && <span className="text-text-secondary font-normal"> · {account.space}</span>}
          </div>
          <div className="text-[11px] text-text-secondary truncate">{account.email || '—'}</div>
        </div>
        <div className="flex items-center gap-1 shrink-0">
          <div className={`w-2 h-2 rounded-full ${dotCls}`} />
          <AccountMenu account={account} onChanged={onChanged} />
        </div>
      </div>

      {/* Badges */}
      {account.space_id && <div className="flex items-center gap-2 text-[11px] text-text-secondary mb-2">
        <span>{t('account.workspace_id')}</span>
        <code className="truncate select-all" title={account.space_id}>{account.space_id}</code>
        <button className="shrink-0 text-notion-blue cursor-pointer" aria-label={`${t('account.copy_workspace_id')} ${account.space_id}`}
          onClick={async event => {
            event.stopPropagation()
            try { await navigator.clipboard.writeText(account.space_id!); setWorkspaceCopy('copied') }
            catch { setWorkspaceCopy('failed') }
          }}>{workspaceCopy === 'copied' ? t('api.copied') : t('account.copy_workspace_id')}</button>
      </div>}
      {workspaceCopy === 'failed' && <p role="alert" className="text-err text-[11px]">{t('models.copy_failed')}</p>}
      {routing && <div className="rounded-md border border-border p-2 mb-2 text-[11px] text-text-secondary space-y-1">
        <div className="flex items-center justify-between gap-2"><span>{t(`upgrade.member_${routing.status}`)} · {t('upgrade.in_flight')}: {routing.in_flight}</span><button className="text-notion-blue cursor-pointer disabled:opacity-40" disabled={toggling} onClick={event => { event.stopPropagation(); void onToggle(routing) }}>{t(routing.enabled ? 'upgrade.pause' : 'upgrade.enable')}</button></div>
        {routing.reason && <p className="break-words">{t(`upgrade.reason_${routing.reason}`, { defaultValue: routing.reason })}</p>}
        <p>{t('upgrade.score_source')}: {t(`upgrade.score_${routing.score_source}`, { defaultValue: routing.score_source })}</p>
        <p>{t('upgrade.checked')}: {timestampValue(routing.quota_checked_at_ms)}</p>
        {routing.cooldown_until_ms && <p>{t('upgrade.recovery')}: {timestampValue(routing.cooldown_until_ms)}</p>}
      </div>}
      <div className="flex gap-3 flex-wrap mt-3 mb-2.5 items-center">
        <Badge variant="plan">{account.plan || 'unknown'}</Badge>
        <Badge variant={account.subscription_status === 'verified_business' ? 'plan' : 'warning'}>{t(account.subscription_status === 'verified_business' ? 'upgrade.verified_business' : 'upgrade.legacy_unverified')}</Badge>
        {account.registered_via && (
          <Badge variant="plan">via {providerDisplay(account.registered_via)}</Badge>
        )}
        {premium && <Badge variant="premium">{t('quota.premium_metrics')}</Badge>}
        {(account.research_usage != null && account.research_usage > 0) && (
          <Badge variant={researchLimited ? 'warning' : 'research'}>
            <IconFlask /> Research {t('account.used', { count: account.research_usage })}{premium ? '' : '/3'}
          </Badge>
        )}
        {account.exhausted && !account.permanent && <Badge variant="warning">Basic blocked</Badge>}
        {account.permanent && <Badge variant="warning">Free cap</Badge>}
        {noWorkspace && <Badge variant="warning">{t('account.no_workspace')}</Badge>}
        {modelCount > 0 && (
          <button
            onClick={e => { e.stopPropagation(); setShowModels(!showModels) }}
            className="cursor-pointer border-none bg-transparent p-0 text-[11px] text-text-secondary hover:text-white transition-colors"
          >
            {modelCount} models {showModels ? '▴' : '▾'}
          </button>
        )}
      </div>

      {/* Quotas */}
      <AIUsage account={account} />
      <p className="text-[10px] text-text-secondary mb-2">{t('quota.compatibility_note')}</p>
      {account.quota_status_reason && <p role="status" className="text-[11px] text-warn mb-2">{t(`quota.${account.quota_status_reason}`)}</p>}
      {sameBasicQuota ? (
        <QuotaBar label="Basic" usage={spaceQuota.usage} limit={spaceQuota.limit} />
      ) : (
        <>
          <QuotaBar label="Space" usage={spaceQuota.usage} limit={spaceQuota.limit} />
          {userQuota.limit > 0 && <QuotaBar label="User" usage={userQuota.usage} limit={userQuota.limit} />}
        </>
      )}
      {premium && <QuotaBar label="Premium" labelClass="text-[#7eb8ff]" usage={account.premium_usage} limit={account.premium_limit} />}
      <div className="flex flex-wrap gap-3 mt-2 text-[10px] text-text-muted">
        <span>{t('account.basic_remaining')} {fmt(account.remaining || 0)}</span>
        {premium && <span>{t('account.premium_remaining')} {premiumBalance === null ? t('quota.unknown') : metricValue(premiumBalance)}</span>}
      </div>

      {/* Models (expandable) */}
      {showModels && account.models && account.models.length > 0 && (
        <div className="flex flex-wrap gap-1 mt-1.5 mb-1">
          {account.models.map(m => (
            <span key={m.id} className="text-[10px] px-1.5 py-0.5 bg-white/[.06] rounded text-text-secondary">
              {m.name || m.id}
            </span>
          ))}
        </div>
      )}

      {/* Footer */}
      <div className="flex justify-between items-center mt-2 pt-2 border-t border-border">
        <span className="text-[10px] text-text-muted flex items-center gap-1 min-w-0">
          <IconClock />
          <span className="truncate">{t('account.last_checked', { date: formatCheckedAt(account.checked_at) })} · {t('account.recent_ai', { date: formatTimestampMs(account.last_usage_at) })}</span>
        </span>
        {noWorkspace ? (
          <span className="text-[11px] text-err font-medium">{t('account.unavailable')}</span>
        ) : (
          <button className="text-[11px] text-text-secondary hover:text-white font-medium transition-colors cursor-pointer" onClick={event => { event.stopPropagation(); handleClick() }}>{t('account.open_proxy')}</button>
        )}
      </div>
    </div>
  )
}

export default function App() {
  const { t, i18n } = useTranslation()
  const [authState, setAuthState] = useState<'checking' | 'login' | 'authenticated'>('checking')
  const [authRequired, setAuthRequired] = useState(false)
  const [authError, setAuthError] = useState('')
  const [authVersion, setAuthVersion] = useState(0)
  const [importVersion, setImportVersion] = useState(0)
  const [routingVersion, setRoutingVersion] = useState(0)
  const [activeTab, setActiveTab] = useState<DashboardTab>(() => dashboardTab(window.location.hash))
  const [data, setData] = useState<DashboardData | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [quotaRefreshing, setQuotaRefreshing] = useState(false)
  const [refreshStatus, setRefreshStatus] = useState<RefreshStatus | null>(null)
  const [query, setQuery] = useState('')
  const [debouncedQuery, setDebouncedQuery] = useState('')
  const [refreshTime, setRefreshTime] = useState('')
  const [page, setPage] = useState(0)
  const [tokenStats, setTokenStats] = useState<TokenStats | null>(null)
  const [tokenStatsError, setTokenStatsError] = useState('')
  const [routing, setRouting] = useState<RoutingData | null>(null)
  const [routingError, setRoutingError] = useState('')
  const [memberSaving, setMemberSaving] = useState('')
  const [showAddModal, setShowAddModal] = useState(false)
  const accountRequest = useRef<AbortController | null>(null)
  const quotaRefreshPending = useRef(false)
  const PAGE_SIZE = 20

  useEffect(() => {
    const change = () => { setActiveTab(dashboardTab(window.location.hash)); setShowAddModal(false) }
    window.addEventListener('hashchange', change)
    return () => window.removeEventListener('hashchange', change)
  }, [])

  useEffect(() => {
    let live = true
    setAuthState('checking'); setAuthError('')
    const controller = new AbortController()
    checkAuth(controller.signal).then(status => {
      if (!live) return
      setAuthRequired(status.required)
      setAuthState(!status.required || status.authenticated ? 'authenticated' : 'login')
    }).catch(reason => { if (live) setAuthError(reason instanceof Error ? reason.message : String(reason)) })
    return () => { live = false; controller.abort() }
  }, [authVersion])

  useEffect(() => {
    const expired = () => {
      accountRequest.current?.abort()
      setAuthState(authRequired ? 'login' : 'checking'); setData(null); setRouting(null); setTokenStats(null); setTokenStatsError(''); setShowAddModal(false)
      if (!authRequired) setAuthVersion(value => value + 1)
    }
    window.addEventListener('notion-manager-unauthorized', expired)
    const imported = () => setImportVersion(value => value + 1)
    window.addEventListener('notion-manager-account-imported', imported)
    return () => { window.removeEventListener('notion-manager-unauthorized', expired); window.removeEventListener('notion-manager-account-imported', imported) }
  }, [authRequired])

  useEffect(() => {
    const timer = setTimeout(() => { setDebouncedQuery(query.trim()); setPage(0) }, 250)
    return () => clearTimeout(timer)
  }, [query])

  const loadData = useCallback(async (signal?: AbortSignal) => {
    accountRequest.current?.abort()
    const controller = new AbortController()
    accountRequest.current = controller
    const requestSignal = signal ? AbortSignal.any([signal, controller.signal]) : controller.signal
    try {
      const result = await fetchDashboardData({ page, pageSize: PAGE_SIZE, query: debouncedQuery }, requestSignal)
      if (requestSignal.aborted) return
      setData(result); setError('')
      setRefreshStatus(result.refresh || null)
      setRefreshTime(new Date().toLocaleTimeString(i18n.language === 'zh' ? 'zh-CN' : 'en-US'))
    } catch (reason) {
      if (!requestSignal.aborted) setError(reason instanceof Error ? reason.message : String(reason))
    } finally { if (!requestSignal.aborted) setLoading(false) }
  }, [page, debouncedQuery, i18n.language])

  useEffect(() => {
    if (authState !== 'authenticated' || activeTab !== 'accounts') return
    setLoading(true)
    const stop = startVisiblePolling(loadData, refreshStatus?.refreshing ? 3000 : 10000)
    return () => { stop(); accountRequest.current?.abort() }
  }, [authState, activeTab, loadData, importVersion, refreshStatus?.refreshing])

  useEffect(() => {
    if (authState !== 'authenticated' || activeTab !== 'accounts') return
    return startVisiblePolling(async signal => {
      await Promise.all([
        fetchTokenStats(signal).then(value => { if (!signal.aborted) { setTokenStats(value); setTokenStatsError('') } }).catch(reason => {
          if (!signal.aborted) setTokenStatsError(reason instanceof Error ? reason.message : String(reason))
        }),
        fetchRouting(signal).then(value => { if (!signal.aborted) { setRouting(value); setRoutingError('') } }).catch(reason => {
          if (!signal.aborted) setRoutingError(reason instanceof Error ? reason.message : String(reason))
        }),
      ])
    }, 10000)
  }, [authState, activeTab, importVersion, routingVersion])

  useEffect(() => {
    if (authState !== 'authenticated' || activeTab !== 'accounts') {
      quotaRefreshPending.current = false
      return
    }
    const next = quotaRefreshProgress(quotaRefreshPending.current, refreshStatus?.refreshing)
    quotaRefreshPending.current = next.pending
    if (next.completed) setRoutingVersion(value => value + 1)
  }, [authState, activeTab, refreshStatus])

  const refresh = async () => { setRefreshing(true); setRoutingVersion(value => value + 1); await loadData(); setRefreshing(false) }
  const handleQuotaRefresh = async () => {
    setQuotaRefreshing(true)
    try {
      await triggerRefresh()
      quotaRefreshPending.current = true
      setRefreshStatus(previous => ({ refreshing: true, done: 0, total: previous?.total || 0 }))
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
    finally { setQuotaRefreshing(false) }
  }
  const toggleMember = async (member: RoutingMember) => {
    setMemberSaving(member.account_id); setRoutingError('')
    try {
      const latest = await fetchRouting()
      const disabled = new Set(latest.disabled_account_ids)
      if (member.enabled) disabled.add(member.account_id)
      else disabled.delete(member.account_id)
      setRouting(await updateRouting({
        mode: latest.mode, max_attempts: latest.max_attempts,
        max_concurrent_per_account: latest.max_concurrent_per_account,
        retention_days: latest.retention_days, disabled_account_ids: [...disabled],
      }))
    } catch (reason) { setRoutingError(reason instanceof Error ? reason.message : String(reason)) }
    finally { setMemberSaving('') }
  }
  const handleLogout = async () => {
    try {
      await logout()
      accountRequest.current?.abort()
      setAuthState('login'); setData(null); setTokenStats(null); setTokenStatsError(''); setRouting(null); setShowAddModal(false)
    } catch (reason) { setAuthError(reason instanceof Error ? reason.message : String(reason)) }
  }

  const accounts = data?.accounts || []
  const paged = accounts
  const filteredTotal = data?.filtered_total ?? data?.total ?? accounts.length
  const totalPages = Math.max(1, Math.ceil(filteredTotal / PAGE_SIZE))
  useEffect(() => { if (page >= totalPages) setPage(Math.max(0, totalPages - 1)) }, [page, totalPages])
  const summary = useMemo(() => {
    if (!data) return null
    const s = data.summary
    return {
      ...routingMetrics(routing, s?.exhausted_only),
      noWorkspace: s?.no_workspace,
      totalResearchUsage: s?.total_research_usage ?? 0, totalRemaining: s?.total_remaining ?? 0,
      totalSpaceRemaining: s?.total_space_remaining ?? 0, totalUserRemaining: s?.total_user_remaining ?? 0,
      totalPremiumBalance: confirmedPremiumBalance(s?.total_premium_balance, s?.premium_unknown_accounts != null), totalPremiumLimit: s?.total_premium_limit ?? 0,
      premiumUnknownAccounts: s?.premium_unknown_accounts,
      premiumAccounts: s?.premium_accounts ?? 0, researchLimited: s?.research_limited ?? 0,
      sameBasicQuota: isSameQuota({ usage: s?.total_space_usage ?? 0, limit: s?.total_space_limit ?? 0 }, { usage: s?.total_user_usage ?? 0, limit: s?.total_user_limit ?? 0 }),
    }
  }, [data, routing])
  if (authState === 'checking') return <div className="max-w-lg mx-auto p-8"><ResourceState loading={!authError} error={authError} retry={() => setAuthVersion(value => value + 1)} /></div>
  if (authState === 'login') return <LoginPage onSuccess={() => { setAuthState('authenticated'); setLoading(true) }} />
  const dateLocale = i18n.language === 'zh' ? 'zh-CN' : 'en-US'

  return <div className="min-h-screen">
    <Header query={query} onQuery={setQuery} onLogout={handleLogout} authRequired={authRequired} showSearch={activeTab === 'accounts'} />
    <main className="max-w-[1280px] mx-auto px-4 sm:px-6 py-6">
      <nav aria-label={t('upgrade.navigation')} className="flex flex-wrap gap-2 border-b border-border pb-3 mb-5">
        {dashboardTabs.map(tab => <a key={tab} href={`#${tab}`} aria-current={activeTab === tab ? 'page' : undefined} className={`${buttonClass} ${activeTab === tab ? 'bg-notion-blue! text-white' : 'text-text-secondary'}`}>{t(`upgrade.${tab}`)}</a>)}
      </nav>
      {authError && <p role="alert" className="text-err text-sm mb-4">{authError}</p>}
      {activeTab === 'overview' && <OverviewPanel key={importVersion} />}
      {activeTab === 'models' && <ModelsPanel key={importVersion} />}
      {activeTab === 'logs' && <LogsPanel />}
      {activeTab === 'analytics' && <AnalyticsPanel />}
      {activeTab === 'settings' && <SettingsPanel />}
      {activeTab === 'accounts' && <section aria-label={t('upgrade.accounts')}>
        <h1 className="text-xl font-semibold mb-4">{t('upgrade.accounts')}</h1>
        <ResourceState loading={loading && !data} error={error} retry={() => { void loadData() }} />
        {routingError && <p role="alert" className="text-err text-sm mb-4 break-words">{routingError}</p>}
        {tokenStatsError && <p role="alert" className="text-warn text-sm mb-4 break-words">
          {t('stats.read_failed', { error: tokenStatsError })} {t(tokenStats ? 'stats.cached_usage' : 'stats.unavailable')}
          <button className={`${buttonClass} ml-2`} onClick={() => setRoutingVersion(value => value + 1)}>{t('upgrade.retry')}</button>
        </p>}
        {routing && <div className="text-[12px] text-text-secondary border border-border rounded-lg p-3 mb-4">{t('upgrade.pool_summary', routing as unknown as Record<string, unknown>)} · {t(`upgrade.mode_${routing.mode}`)}</div>}
        {/* Summary */}
        {summary && (
          <div className="grid grid-cols-5 divide-x divide-white/[.05] mb-6 max-lg:grid-cols-3 max-md:grid-cols-2 max-md:divide-x-0 max-sm:grid-cols-1">
            <StatCard
              label={t('stats.total_accounts')} value={data!.total}
              sub={t('stats.available_spent_no_workspace', { available: metricValue(summary.available), spent: metricValue(summary.exhausted), noWorkspace: metricValue(summary.noWorkspace) })}
            />
            <StatCard
              label={`${t('upgrade.pool')} · ${t('stats.available')}`} value={metricValue(summary.available)}
              sub={summary.availableRate === null ? '—' : t('stats.ratio', { percent: summary.availableRate })}
              color="var(--color-ok)"
            />
            <StatCard
              label={t('stats.basic_remaining')} value={fmt(summary.totalRemaining)}
              sub={summary.sameBasicQuota
                ? t('account.quota_unified')
                : `Space ${fmt(summary.totalSpaceRemaining)} · User ${fmt(summary.totalUserRemaining)}`}
            />
            <StatCard
              label={t('stats.premium_remaining')} value={metricValue(summary.totalPremiumBalance)}
              sub={t('quota.unknown_accounts', { count: metricValue(summary.premiumUnknownAccounts) }) + ' · ' + (summary.totalPremiumLimit > 0
                ? t('stats.premium_accounts_count', { count: summary.premiumAccounts, usage: summary.totalResearchUsage })
                : t('stats.no_premium_credits', { limit: summary.researchLimited }))}
              color="var(--color-research, #9b51e0)"
            />
            <StatCard
              icon={<IconActivity />}
              label={t('stats.token_usage')}
              value={tokenStats ? formatTokens(tokenStats.total.total) : '—'}
              sub={tokenStats
                ? t('stats.today_usage', { today: formatTokens(tokenStats.today.total), input: formatTokens(tokenStats.today.input), output: formatTokens(tokenStats.today.output) })
                : t(tokenStatsError ? 'stats.unavailable' : 'upgrade.loading')}
              color="var(--color-notion-blue)"
            />
          </div>
        )}

        {/* Total Quota Bar */}
        {data && <TotalQuotaBar summary={data.summary} />}
        {data?.accounts.length === 1 && <AIUsage account={data.accounts[0]} />}


        {refreshStatus?.refreshing && <p role="status" className="text-notion-blue text-sm mb-3">{t('common.status_refreshing', { current: refreshStatus.done, total: refreshStatus.total })}</p>}
        <div className="flex flex-wrap gap-2 mb-4">
          <button className={buttonClass} onClick={openBestProxy}><IconZap /> {t('actions.open_best_account')}</button>
          <button className={buttonClass} disabled={quotaRefreshing || refreshStatus?.refreshing} onClick={handleQuotaRefresh}>{t('actions.refresh_quota')}</button>
          <button className={buttonClass} disabled={refreshing} onClick={refresh}>{t('actions.refresh_data')}</button>
          <button className={buttonClass} onClick={() => setShowAddModal(true)}>{t('actions.add_account')}</button>
          {refreshTime && <span className="text-[11px] text-text-secondary self-center">{t('actions.updated_at', { time: refreshTime })}{refreshStatus?.last_refresh_at && <> · {t('actions.quota_refreshed_at', { time: new Date(refreshStatus.last_refresh_at).toLocaleTimeString(dateLocale) })}</>}</span>}
        </div>
        <p className="text-[12px] text-text-secondary mb-5">{t('upgrade.business_only')}</p>
        {/* Section Title */}
        <div className="text-[12px] font-semibold text-text-secondary uppercase tracking-wider mb-3.5 flex items-center gap-1.5">
          <span>{t('common.accounts_pool')}</span>
          <span className="font-normal text-text-muted">({data ? filteredTotal : '—'})</span>
        </div>

        {/* Grid */}
        {data && (filteredTotal === 0 ? (
          <div className="text-center py-16 text-text-secondary text-sm">
            {t('common.no_matching_accounts')}
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-[repeat(auto-fill,minmax(300px,1fr))] gap-2.5 mb-4">
            {paged.map(acc => (
              <AccountCard key={acc.account_id} account={acc} onChanged={() => { void loadData(); setRoutingVersion(value => value + 1) }} routing={routing?.accounts.find(member => member.account_id === acc.account_id)} onToggle={toggleMember} toggling={!!memberSaving} />
            ))}
          </div>
        ))}

        {/* Pagination */}
        {totalPages > 1 && (
          <div className="flex items-center justify-center gap-2 mb-10">
            <button
              onClick={() => setPage(0)}
              disabled={page === 0}
              className="px-2.5 py-1.5 bg-bg-card hover:bg-bg-card-hover text-text-secondary rounded-md text-[12px] cursor-pointer transition-colors border border-border disabled:opacity-30 disabled:cursor-not-allowed"
            >
              «
            </button>
            <button
              onClick={() => setPage(p => Math.max(0, p - 1))}
              disabled={page === 0}
              className="px-2.5 py-1.5 bg-bg-card hover:bg-bg-card-hover text-text-secondary rounded-md text-[12px] cursor-pointer transition-colors border border-border disabled:opacity-30 disabled:cursor-not-allowed"
            >
              ‹ {t('common.prev_page')}
            </button>
            <span className="text-[12px] text-text-secondary tabular-nums px-3">
              {page + 1} / {totalPages}
            </span>
            <button
              onClick={() => setPage(p => Math.min(totalPages - 1, p + 1))}
              disabled={page >= totalPages - 1}
              className="px-2.5 py-1.5 bg-bg-card hover:bg-bg-card-hover text-text-secondary rounded-md text-[12px] cursor-pointer transition-colors border border-border disabled:opacity-30 disabled:cursor-not-allowed"
            >
              {t('common.next_page')} ›
            </button>
            <button
              onClick={() => setPage(totalPages - 1)}
              disabled={page >= totalPages - 1}
              className="px-2.5 py-1.5 bg-bg-card hover:bg-bg-card-hover text-text-secondary rounded-md text-[12px] cursor-pointer transition-colors border border-border disabled:opacity-30 disabled:cursor-not-allowed"
            >
              »
            </button>
          </div>
        )}

      </section>}
    </main>
    {showAddModal && <AddAccountModal onClose={() => setShowAddModal(false)} onSuccess={() => setImportVersion(value => value + 1)} />}
  </div>
}
