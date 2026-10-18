export interface AccountImport {
  token_v2: string
  user_id?: string
  space_id?: string
  user_name?: string
  user_email?: string
  space_name?: string
}

export function parseAccountImport(text: string, jsonOnly = false): AccountImport {
  const trimmed = text.trim()
  if (!jsonOnly && !trimmed.startsWith('{') && !trimmed.startsWith('[')) {
    if (!trimmed) throw new Error('invalid config')
    return { token_v2: trimmed }
  }
  const value: unknown = JSON.parse(trimmed)
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('invalid config')
  const config = value as Record<string, unknown>
  if (typeof config.token_v2 !== 'string' || !config.token_v2.trim()) throw new Error('invalid config')
  const result: AccountImport = { token_v2: config.token_v2.trim() }
  for (const key of ['user_id', 'space_id', 'user_name', 'user_email', 'space_name'] as const) {
    if (config[key] == null) continue
    if (typeof config[key] !== 'string') throw new Error('invalid config')
    if ((key === 'user_id' || key === 'space_id') && !config[key].trim()) throw new Error('invalid config')
    result[key] = config[key].trim()
  }
  return result
}
