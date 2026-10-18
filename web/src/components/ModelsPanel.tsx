import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

export function ModelsPanel() {
  const { t } = useTranslation()
  const [models, setModels] = useState<string[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [refresh, setRefresh] = useState(0)
  const [copied, setCopied] = useState('')
  const [copyError, setCopyError] = useState('')
  const origin = window.location.origin
  const buttonClass = 'shrink-0 rounded-md border border-border bg-bg-card px-3 py-1.5 text-[12px] text-text-primary hover:bg-bg-card-hover cursor-pointer disabled:opacity-50'

  useEffect(() => {
    const controller = new AbortController()
    setLoading(true)
    setError('')
    const apiKey = document.querySelector('meta[name="api-key"]')?.getAttribute('content') || ''
    fetch('/v1/models', { headers: { 'x-api-key': apiKey }, signal: controller.signal })
      .then(async response => {
        if (!response.ok) throw new Error(`HTTP ${response.status}`)
        const body = await response.json()
        if (!Array.isArray(body.data) || !body.data.every((model: { id?: unknown }) => model && typeof model.id === 'string')) {
          throw new Error(t('models.invalid_response'))
        }
        setModels([...new Set<string>(body.data.map((model: { id: string }) => model.id))].sort())
      })
      .catch((err: Error) => { if (!controller.signal.aborted) setError(err.message) })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [refresh, t])

  async function copy(value: string) {
    setCopyError('')
    try {
      await navigator.clipboard.writeText(value)
      setCopied(value)
    } catch {
      setCopyError(t('models.copy_failed'))
    }
  }

  const protocols = [
    { name: 'OpenAI Chat Completions', base: `${origin}/v1`, endpoint: `${origin}/v1/chat/completions` },
    { name: 'Anthropic Messages', base: origin, endpoint: `${origin}/v1/messages` },
    { name: 'OpenAI Responses', base: `${origin}/v1`, endpoint: `${origin}/v1/responses` },
  ]

  return (
    <div className="space-y-6">
      <section aria-label={t('models.connections')}>
        <h2 className="text-base font-semibold mb-2">{t('models.connections')}</h2>
        <p className="text-[12px] text-text-secondary mb-4">{t('models.address_note')}</p>
        <div className="grid gap-3 lg:grid-cols-3">
          {protocols.map(protocol => (
            <div key={protocol.name} className="rounded-lg border border-border bg-bg-card p-4 space-y-3">
              <h3 className="font-medium text-[13px]">{protocol.name}</h3>
              {[
                { label: t('models.base'), value: protocol.base },
                { label: t('models.endpoint'), value: protocol.endpoint },
              ].map(field => (
                <div key={field.label}>
                  <div className="text-[11px] text-text-secondary mb-1">{field.label}</div>
                  <code className="block break-all text-[12px] mb-2 select-all">{field.value}</code>
                  <button className={buttonClass} onClick={() => copy(field.value)} aria-label={`${t('models.copy')} ${protocol.name} ${field.label}`}>
                    {copied === field.value ? t('api.copied') : t('models.copy')}
                  </button>
                </div>
              ))}
              <div className="text-[11px] text-text-secondary">{t('models.method')} POST</div>
            </div>
          ))}
        </div>
        <p className="text-[12px] text-text-secondary mt-3">{t('models.auth_note')}</p>
      </section>
      <section aria-label={t('models.list')}>
        <div className="flex items-center justify-between gap-3 mb-2">
          <h2 className="font-semibold">{t('models.list')} {!loading && !error && `(${models.length})`}</h2>
          <button className={buttonClass} disabled={loading} onClick={() => setRefresh(value => value + 1)}>{t('models.reload')}</button>
        </div>
        <p className="text-[12px] text-text-secondary mb-3">{t('models.source_note')}</p>
        {loading ? <p role="status">{t('models.loading')}</p>
          : error ? <p role="alert" className="text-err">{t('models.load_failed', { error })}</p>
          : models.length === 0 ? <p>{t('models.empty')}</p>
          : <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
            {models.map(id => (
              <div key={id} className="flex items-center justify-between gap-3 border border-border rounded-md bg-bg-card p-3">
                <code className="text-[13px] break-all select-all">{id}</code>
                <button className={buttonClass} onClick={() => copy(id)} aria-label={`${t('models.copy_id')} ${id}`}>
                  {copied === id ? t('api.copied') : t('models.copy_id')}
                </button>
              </div>
            ))}
          </div>}
      </section>
      <p role="status" className="text-[12px] text-ok">{copied && t('models.copied_value', { value: copied })}</p>
      {copyError && <p role="alert" className="text-err text-[12px]">{copyError}</p>}
    </div>
  )
}
