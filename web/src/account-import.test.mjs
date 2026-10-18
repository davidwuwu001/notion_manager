import { test } from 'node:test'
import assert from 'node:assert/strict'
import { parseAccountImport } from './account-import.ts'

test('accepts legacy raw tokens and selected workspace JSON without retaining extra secrets', () => {
  assert.deepEqual(parseAccountImport(' raw-token '), { token_v2: 'raw-token' })
  const result = parseAccountImport(JSON.stringify({ token_v2: ' token ', user_id: 'user', space_id: 'workspace-b', space_name: 'Same name', full_cookie: 'unused secret' }), true)
  assert.deepEqual(result, { token_v2: 'token', user_id: 'user', space_id: 'workspace-b', space_name: 'Same name' })
})

test('rejects malformed files, missing tokens and invalid identity selectors', () => {
  for (const text of ['null', '[]', '{}', '{bad', 'raw-token', '{"token_v2":42}', '{"token_v2":"t","space_id":42}', '{"token_v2":"t","space_id":" "}']) {
    assert.throws(() => parseAccountImport(text, true))
  }
})
