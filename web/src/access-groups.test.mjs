import { test } from 'node:test'
import assert from 'node:assert/strict'
import { missingGroupAccountIds, toggleGroupAccount } from './access-groups.ts'

test('deleted group members remain visible until explicitly removed', () => {
  const group = ['valid-account', 'deleted-account']
  const accounts = [{account_id:'valid-account'}]
  assert.deepEqual(missingGroupAccountIds(group, accounts), ['deleted-account'])
  assert.deepEqual(group, ['valid-account', 'deleted-account'])
  const edited = toggleGroupAccount(group, 'deleted-account', false)
  assert.deepEqual(edited, ['valid-account'])
  assert.deepEqual(missingGroupAccountIds(edited, accounts), [])
  assert.deepEqual(group, ['valid-account', 'deleted-account'])
})

test('an empty account list never silently clears the group', () => {
  const group = ['a', 'b']
  assert.deepEqual(missingGroupAccountIds(group, []), ['a', 'b'])
  assert.deepEqual(toggleGroupAccount(group, 'a', false), ['b'])
})
