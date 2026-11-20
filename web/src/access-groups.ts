export function missingGroupAccountIds(selected: string[], accounts: Array<{ account_id: string }>): string[] {
  const known = new Set(accounts.map(account => account.account_id))
  return selected.filter(id => !known.has(id))
}

export function toggleGroupAccount(selected: string[], id: string, checked: boolean): string[] {
  return checked ? (selected.includes(id) ? selected : [...selected, id]) : selected.filter(value => value !== id)
}
